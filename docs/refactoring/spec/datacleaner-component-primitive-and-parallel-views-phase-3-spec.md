# Datacleaner — Phase 3 Spec (View Reconstruction: `CleanedFinancialData`)

**Status:** DESIGN (authored 2026-05-23, ready for BACKEND dispatch)
**Phase:** Phase 3 of the DC-1 refactor sequence (5 phases total)
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-spec.md](datacleaner-component-primitive-and-parallel-views-spec.md) — §"Phasing & implementation sequence" row "Phase 3 — `CleanedFinancialData` + view reconstruction"
**Phase 2 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md](../implementations/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md)
**Implementer plan:** [datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md](../implementations/datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md)
**Tracker:** [docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md](../../reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md)
**Estimated effort:** 1–2 agent shifts (1 PR or a 2-PR split — see §"PR strategy")

---

## 1. Phase context

Phase 2 closed on 2026-05-23 with the full 4-PR stack on `dc1-phase-2-pr-4` (PR-1 `39cf0fa`, PR-2 `2e8f83b`, PR-3 `207f41a`, PR-4 final tip after Task 4.7; awaiting HUMAN merge to master). All 17 cleaner-side adjusters now implement the `Adjuster` interface natively across four role flavors (OverlayEmitter, Restater, Restater+TaxShieldDTA, FlagEmitter); PR-1's `shimLedgerEntriesFromLegacy*` helpers are FULLY deleted; `data.AdjustmentLedger` + `data.Overlays` are populated natively by every adjuster; dual-write is preserved at the dispatcher level. `SchemaVersion["FinancialData"]` is at 8.

**Phase 3 is the first consumer-visible *capability* change since Phase 0** — it introduces the three-view accessor surface (`AsReported`/`Restated`/`InvestedCapital`) that consumes the Phase 2 ledger + overlays. Phase 3 itself ships zero downstream behavior change because no consumer migrates yet; Phase 4 is the consumer-migration gate that flips read sites and deletes the dual-write.

This spec also resolves the two Q-deferrals carried over from Phase 2:
- **Q2:** A2 `TaxShieldDTA` actual population.
- **Q4:** `AIProvenance` SHA-256 hash computation (`PromptHash` + `SourceDocHash`).

And handles two design hygiene items raised across the PR-2/PR-3/PR-4 reviewer threads:
- **ctx threading** through `ProcessLiabilityAdjustments` (and its asset/earnings siblings, for symmetry).
- **Translator extraction decision** — settled here so Phase 4 inherits a clear stance.

---

## 2. Goals

1. Introduce `internal/services/datacleaner/cleaneddata.CleanedFinancialData` (new package) with three lazy view accessors: `AsReported()`, `Restated()`, `InvestedCapital()`.
2. Resolve **Q2** — A2 populates `TaxShieldDTA = writedownAmount * working.EffectiveTaxRate` when `EffectiveTaxRate > 0`, mirroring A5's Phase 2 pattern.
3. Resolve **Q4** — `AIProvenance.PromptHash` + `SourceDocHash` are SHA-256 hex strings computed at the B3 AI call site (`captureB3AIProvenance` or its caller).
4. Thread `ctx context.Context` through `ProcessLiabilityAdjustments`, `ProcessAssetAdjustments`, and `ProcessEarningsAdjustments` signatures. Real `ctx` propagated from `service.go::applyActiveAdjustments`.
5. Lock the canonical translator-extraction decision: **KEEP per-rule translators** (no extraction). Phase 4 deletes them alongside the legacy `*AdjustmentResult` shape.
6. Surface the T2-BS-3 carve-out in the `Restated()` accessor: when `AsReported.TotalLiabilities == 0` for an AMD/KO-style parser dropout (signal: `SourceReliability == "parser_known_dropout"` on any adjuster's ledger entry, OR direct heuristic check), the `Restated()` accessor returns a component-sum reconstruction of `TotalLiabilities`.

## 3. Non-goals

- **No consumer migration.** All 13 valuation read sites enumerated in the parent spec continue to read `data.*` directly until Phase 4. This means downstream DCF / WACC / DDM / FFO / Graham outputs are bit-for-bit unchanged after Phase 3.
- **No B3 routing flip.** B3 `OverlaySpec.Field:"DebtLikeClaims"` is recorded by Phase 2; the WACC consumer continues to read the dispatcher-mutated `data.TotalDebt` until Phase 4.
- **No dual-write deletion.** Phase 4 deletes the `data.X ±= Y` mutations atomically with the consumer migration. Phase 3 leaves all dispatcher-level dual-write in place.
- **No CalculationVersion bump.** Because Phase 3 has zero consumer-visible numeric change, `CalculationVersion` stays at 4.2. Phase 4 owns the next bump.
- **No SchemaVersion bump in the Phase 3 *spec* doc** — but the implementation will need to bump `SchemaVersion["FinancialData"]` 8 → 9 when the AIProvenance hash fields and `TaxShieldDTA` start populating with non-zero values (`feedback_schema_version_atomic_bump` MEMORY rule: atomic with the first populating commit). The implementer plan specifies the precise commit; **the spec does NOT bump it.**
- **No replay golden-bundle regeneration.** Phase 3 may surface structural drift in `10-clean-output.json` for periods where AIProvenance hashes go from `""` to a SHA-256 string, and where TaxShieldDTA goes from 0 to a populated value. Use `--allow-schema-drift` and bump the basket bundles in the SchemaVersion-bump commit only; reviewers must approve the structural delta as expected.
- **No parser fix for AMD/KO T2-BS-3.** Phase 3's `Restated()` accessor exposes the carve-out via a reconstructed TotalLiabilities; the parser fix stays deferred (Option B is preserved).

---

## 4. Architecture

### 4.1 Package layout

```
internal/services/datacleaner/
  cleaneddata/                   <-- NEW package
    cleaned.go                   CleanedFinancialData struct + constructor
    view.go                      FinancialDataView + view accessors
    restate.go                   Restated() reconstruction (pure, table-driven)
    invested_capital.go          InvestedCapital() reconstruction (pure)
    asreported.go                AsReported() identity accessor
    cleaned_test.go              constructor + view-equality property tests
    restate_test.go              Restater accessor unit tests
    invested_capital_test.go     InvestedCapital accessor unit tests
    bitforbit_test.go            "no fired adjusters → Restated == AsReported" property test
    t2bs3_test.go                AMD/KO carve-out reconstruction test
```

Placement rationale: a new sub-package under `datacleaner/` keeps the view-reconstruction logic out of `service.go` (already 1500+ lines), keeps `entities/` free of business logic (entities should stay pure data), and makes the package's exported surface trivial to grep (`cleaneddata.New`, `cleaneddata.CleanedFinancialData.{AsReported,Restated,InvestedCapital}`).

Import discipline: `cleaneddata` imports `internal/core/entities` (for `FinancialData`, `LedgerEntry`, `OverlaySpec`) and nothing else from inside `internal/services/`. An import-boundary test asserts this. The package depends only on standard library + `entities` so Phase 4 consumers can import it without cycles.

### 4.2 `CleanedFinancialData` struct

```go
package cleaneddata

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// CleanedFinancialData wraps a post-clean *entities.FinancialData together with
// its AdjustmentLedger and Overlays and exposes three semantically-distinct
// views over the same underlying entity: AsReported, Restated, and
// InvestedCapital. Views are computed on-demand and cached on the struct.
//
// Phase 3 invariant: NO production consumer reads from CleanedFinancialData
// yet. The accessor surface exists so Phase 4 can migrate one consumer at a
// time without further entity changes.
type CleanedFinancialData struct {
    // raw is the post-Phase-2 cleaner output. raw.AdjustmentLedger and
    // raw.Overlays are read by Restated() and InvestedCapital() to drive
    // the reconstruction; raw's other balance-sheet fields are the input
    // to AsReported().
    //
    // raw is NEVER mutated by accessor calls. View construction copies
    // fields into new FinancialDataView values.
    raw *entities.FinancialData

    // Memoized views. nil = not yet computed; populated on first access.
    asReported     *FinancialDataView
    restated       *FinancialDataView
    investedCap    *FinancialDataView
}

// New constructs a CleanedFinancialData around the cleaner's working copy.
// Caller MUST NOT mutate the input *FinancialData after the call returns;
// accessor caching assumes the underlying entity is stable.
func New(raw *entities.FinancialData) *CleanedFinancialData {
    return &CleanedFinancialData{raw: raw}
}

// AsReported returns the balance-sheet view that preserves parser-stamped
// values verbatim. Use for any analysis that must faithfully reflect what
// the filer disclosed, even if the parser missed a tag (e.g. AMD/KO
// TotalLiabilities=0 stays zero per T2-BS-3 Option B carve-out).
//
// First-call cost: O(field count). Subsequent calls: O(1) cached.
func (c *CleanedFinancialData) AsReported() *FinancialDataView { /* ... */ }

// Restated returns the balance-sheet view reconstructed from sum(components)
// + plug, applying LedgerEntry.EquityOffset and LedgerEntry.TaxShieldDTA for
// every fired Restater-role adjuster. For T2-BS-3 carve-out tickers
// (AsReported.TotalLiabilities == 0 with parser_known_dropout signal),
// Restated.TotalLiabilities is the component-sum reconstruction (truthful)
// rather than the parser-stamped zero.
//
// LOAD-BEARING: C6 (capitalized_interest) has EquityOffset=0 by design — the
// Restated accessor MUST NOT add C6's DeltaAmount to retained earnings. This
// is enforced in restate.go's reducer by reading EquityOffset directly
// (never assuming EquityOffset == DeltaAmount).
//
// First-call cost: O(adjusters + fields). Subsequent calls: O(1) cached.
func (c *CleanedFinancialData) Restated() *FinancialDataView { /* ... */ }

// InvestedCapital returns the analytical view that applies OverlaySpec
// entries on top of Restated:
//   - B1 lease capitalization (Field:"TotalDebt", Operation:"add") →
//     DebtLikeClaims += Amount
//   - B2 pension underfunding (Field:"TotalDebt", Operation:"add") →
//     DebtLikeClaims += Amount
//   - B3 contingent liabilities (Field:"DebtLikeClaims", Operation:"add") →
//     DebtLikeClaims += Amount (Phase 2 routing intent realized here)
//   - A1 goodwill exclusion (Field:"TotalAssets", Operation:"subtract") →
//     TotalAssets -= Amount, Goodwill = 0 (Damodaran convention)
//
// AmountSemantics governs the operator: Incremental adds on top of the
// current value (default for all current overlays); Replacement overwrites;
// Delta is a relative delta. Phase 3 only sees Incremental in practice.
//
// First-call cost: O(adjusters + fields). Subsequent calls: O(1) cached.
func (c *CleanedFinancialData) InvestedCapital() *FinancialDataView { /* ... */ }
```

### 4.3 `FinancialDataView` shape

The view is a read-only DTO mirroring **the consumed subset** of `FinancialData` (not all 100+ fields). Rationale:
- Discoverability: a reviewer reading `cleaneddata/view.go` can enumerate exactly which fields each view exposes.
- Audit: when Phase 4 migrates a consumer, the consumer code change is grep-able (`consumer reads view.X` instead of `data.X`).
- Future-proofing: future fields added to `FinancialData` (e.g. new XBRL tags) require deliberate addition to the view, preventing accidental exposure.

```go
// FinancialDataView is the read-only DTO returned by CleanedFinancialData's
// accessor methods. Fields are the consumed subset of entities.FinancialData;
// adding a field requires both a struct update here AND an update to whichever
// accessor populates it.
//
// NEVER expose pointer fields; consumers receive the value semantics they need
// without aliasing into the underlying CleanedFinancialData state.
type FinancialDataView struct {
    // Identification (always identity)
    Ticker            string
    CIK               string
    AsOf              time.Time
    ReportingCurrency string

    // Balance sheet (recomputed for Restated/InvestedCapital; identity for AsReported)
    TotalAssets         float64
    CurrentAssets       float64
    TangibleAssets      float64
    Goodwill            float64
    OtherIntangibles    float64
    Inventory           float64
    DeferredTaxAssets   float64
    TotalLiabilities    float64
    CurrentLiabilities  float64
    TotalDebt           float64
    InterestBearingDebt float64
    StockholdersEquity  float64

    // Phase 3 new field (only populated on InvestedCapital):
    //   Sum of B1 + B2 + B3 overlay amounts. Phase 4 WACC consumer reads this.
    DebtLikeClaims float64

    // Earnings (Restater-touched fields are recomputed for Restated)
    OperatingIncome           float64
    NormalizedOperatingIncome float64
    Revenue                   float64
    NetIncome                 float64
    InterestExpense           float64

    // Cash flow (identity across all three views in Phase 3 — no Restater touches them)
    OperatingCashFlow           float64
    CapitalExpenditures         float64
    DepreciationAndAmortization float64

    // Per-share (identity across all three views)
    SharesOutstanding        float64
    DilutedSharesOutstanding float64
    DividendsPerShare        float64

    // Provenance — Phase 3 records which view this is so consumer-side
    // debug logging can attribute reads.
    ViewKind ViewKind // AsReportedView | RestatedView | InvestedCapitalView
}

type ViewKind string

const (
    AsReportedView      ViewKind = "as_reported"
    RestatedView        ViewKind = "restated"
    InvestedCapitalView ViewKind = "invested_capital"
)
```

If Phase 4 surfaces a consumer needing a field not in the view, **add the field deliberately** — do not auto-mirror `FinancialData`. The intentional friction is the point.

### 4.4 `Restated()` reconstruction algorithm

```
Restated() returns *FinancialDataView reconstructed as follows:

1. Start with field-for-field copy of raw → result (mirroring AsReported's identity).
   result.ViewKind = RestatedView.

2. For each LedgerEntry e in raw.AdjustmentLedger where e.Fired:
     a. Apply e.DeltaAmount to result.Component (signed).
        - Component is a string key; switch over the canonical set
          ("Inventory", "OtherIntangibles", "DeferredTaxAssets",
          "OperatingIncome", "NormalizedOperatingIncome", "InterestExpense").
        - Unknown Component → emit WARN log, skip the entry (fail-soft).
     b. Apply e.EquityOffset to result.StockholdersEquity (signed).
        - C6 has EquityOffset=0 by design; LOAD-BEARING that we read
          EquityOffset and do NOT derive it from DeltaAmount.
     c. Apply e.TaxShieldDTA to result.DeferredTaxAssets (signed; typically +).
        - A2 Phase 3 populates this; A5 already populates in Phase 2.

3. Recompute umbrellas from components (mirroring Phase 1's recomputeUmbrellas):
     result.CurrentAssets  = CashAndCashEquivalents + Inventory + OtherCurrentAssets
     result.TotalAssets    = CurrentAssets + Goodwill + OtherIntangibles
                             + DeferredTaxAssets + OtherNonCurrentAssets
     result.CurrentLiabilities = OperatingLeaseLiabilityCurrent + OtherCurrentLiabilities
     result.TotalLiabilities   = CurrentLiabilities + TotalDebt
                                  + OperatingLeaseLiabilityNoncurrent
                                  + OtherNonCurrentLiabilities
     result.TangibleAssets = TotalAssets − Goodwill − OtherIntangibles

4. T2-BS-3 carve-out: if raw.TotalLiabilities == 0 (parser dropout signal),
   result.TotalLiabilities stays the recomputed value from step 3 (truthful).
   Otherwise — if raw.TotalLiabilities > 0 — result.TotalLiabilities also
   stays the recomputed value (the recompute is always the source of truth
   in Restated; AsReported preserves the original).

5. Return result.
```

**Idempotency:** calling Restated() twice returns the same `*FinancialDataView` pointer (memoized). Mutation of the returned view by a caller is undefined behavior; convention is read-only.

### 4.5 `InvestedCapital()` reconstruction algorithm

```
InvestedCapital() returns *FinancialDataView built as follows:

1. Start with Restated() output (cached if already called).
   Copy into result; result.ViewKind = InvestedCapitalView.

2. For each OverlaySpec o in raw.Overlays:
     Switch o.Field:
       case "TotalDebt":
         // B1 + B2 today; semantically these are DebtLikeClaims contributors.
         result.DebtLikeClaims += o.Amount
       case "DebtLikeClaims":
         // B3 today (Phase 2 routing intent).
         result.DebtLikeClaims += o.Amount
       case "TotalAssets" with Operation == "subtract":
         // A1 goodwill exclusion (Damodaran convention).
         result.TotalAssets -= o.Amount
         result.Goodwill = 0
         result.TangibleAssets = result.TotalAssets − result.OtherIntangibles
       default:
         // Unknown Field → emit WARN log, skip (fail-soft).

   AmountSemantics governs the operator:
     - Incremental: use Operation as written ("add" → +=, "subtract" → -=)
     - Replacement: assign (result.X = o.Amount)
     - Delta: same as Incremental (delta == incremental for additive fields)
   Phase 3 only sees Incremental in practice.

3. result.TotalDebt stays UNCHANGED from Restated().
   (Critical: in Phase 4 the WACC consumer reads InvestedCapital.TotalDebt for
   the capital-structure denominator and InvestedCapital.DebtLikeClaims separately
   for the EV→Equity bridge. The two numbers MUST never collapse.)

4. Return result.
```

**Damodaran convention** — A1 goodwill exclusion is the only Overlay that touches `TotalAssets` directly. The rationale (goodwill is real, just excluded from invested-capital math) is preserved by the asymmetry: `AsReported.TotalAssets > InvestedCapital.TotalAssets` and `AsReported.Goodwill > 0`, `InvestedCapital.Goodwill == 0`.

### 4.6 `AsReported()` accessor

The simplest of the three: field-for-field copy of `raw` into a `FinancialDataView` with `ViewKind = AsReportedView`. No mutation. No recompute. **Preserves parser-stamped values verbatim** — including `AMD/KO.TotalLiabilities == 0` (T2-BS-3 Option B carve-out: AsReported honors source data faithfully).

---

## 5. Q-resolutions

### 5.1 Q2 — A2 `TaxShieldDTA` actual population

**Decision:** Mirror A5's Phase 2 pattern.

```go
// internal/services/datacleaner/adjustments/a2_intangible_adjuster.go
// Inside A2's Apply (when fired path triggers):

writedownAmount := workingValue.IndefiniteLivedIntangibles
// existing Restater fields:
deltaAmount   := -writedownAmount
equityOffset  := deltaAmount
// NEW Phase 3 (Q2 resolution):
var taxShieldDTA float64
if working.EffectiveTaxRate > 0 {
    taxShieldDTA = writedownAmount * working.EffectiveTaxRate
}

return AdjusterOutput{
    LedgerEntries: []entities.LedgerEntry{{
        Fired:        true,
        AdjusterID:   "A2_intangible_writedown",
        RuleID:       rule.ID,
        Component:    "OtherIntangibles",
        DeltaAmount:  deltaAmount,
        EquityOffset: equityOffset,
        TaxShieldDTA: taxShieldDTA,  // <-- non-zero when EffectiveTaxRate > 0
        Reasoning:    "...",
    }},
}, nil
```

**Rationale:** Intangible impairments ARE tax-deductible in most jurisdictions (IRC §197 for the US; equivalent treatment in IFRS jurisdictions). Phase 2 deferred this to preserve the dual-write bit-for-bit contract — but in Phase 3, `Restated()` *consumes* `TaxShieldDTA` (adds to `DeferredTaxAssets`), so the population becomes structurally necessary for `Restated().DeferredTaxAssets` to reflect the real economic position post-impairment.

**Dual-write compatibility:** A2's Phase 2 dual-write mutates `data.OtherIntangibles -= writedownAmount` only — it does NOT mutate `data.DeferredTaxAssets`. Adding `TaxShieldDTA` to the LedgerEntry does NOT change the dual-write (dispatcher still mutates only OtherIntangibles); it only affects `Restated().DeferredTaxAssets`. So legacy consumers (which read `data.DeferredTaxAssets` directly) see no change; only `Restated()` consumers see the tax shield. Phase 4 migrates the consumers.

**Test pin:** `TestA2IntangibleAdjuster_TaxShieldDTA_PopulatedWhenEffectiveTaxRateNonZero` replaces the Phase 2 `fired_path_TaxShieldDTA_stays_zero_per_Q2_deferral` regression pin.

**Edge case:** `EffectiveTaxRate == 0` (foreign filers without tax-rate data, or zero-rate jurisdictions) → `TaxShieldDTA = 0`. Preserved from A5's convention.

### 5.2 Q4 — `AIProvenance` SHA-256 hash computation

**Decision:** Compute SHA-256 hex strings at the B3 AI call site (`captureB3AIProvenance` or equivalent), populate `PromptHash` + `SourceDocHash` on the `AIProvenance` struct.

**Hashing strategy:**

```go
import (
    "crypto/sha256"
    "encoding/hex"
)

func sha256Hex(s string) string {
    sum := sha256.Sum256([]byte(s))
    return hex.EncodeToString(sum[:])  // 64-char lowercase hex
}

// At the B3 AI call site (where ai.AnalyzeFootnote is invoked):
prompt := buildB3Prompt(footnoteText, ...)   // the rendered prompt template
sourceDoc := footnoteText                     // the raw footnote text passed in

aiResp, err := aiClient.AnalyzeFootnote(ctx, prompt, ...)
if err != nil { /* fail-soft: provenance stays empty + log */ }

provenance := &entities.AIProvenance{
    ModelName:     aiClient.ModelName(),                  // existing
    PromptHash:    sha256Hex(prompt),                     // NEW (Q4)
    SourceDocHash: sha256Hex(sourceDoc),                  // NEW (Q4)
    ExtractedSpan: aiResp.ExtractedSpan,                  // existing if present, else ""
    Probability:   aiResp.Probability,                    // existing
    Confidence:    aiResp.Confidence,                     // existing
    Timestamp:     time.Now().UTC(),                      // existing
}
```

**What gets hashed:**
- `PromptHash` = SHA-256 hex of the **rendered prompt** (after template substitution: ticker, period, footnote text inserted). This makes the hash a deterministic function of the inputs, NOT of the prompt template alone — replay reproducibility requires the prompt-as-sent.
- `SourceDocHash` = SHA-256 hex of the **footnote text** (`footnoteText` as passed to `ai.AnalyzeFootnote`). Identical footnote → identical hash, regardless of model version.

**Determinism guarantee:** Replay golden bundles stay reproducible because:
1. Given the same `(footnoteText, prompt template version)`, the hashes are byte-identical across runs.
2. Model upgrades change the AI **response** but NOT the **inputs** (hashes); a hash-mismatch under replay signals "input drift, not model drift" cleanly.
3. The hash is computed pre-API-call so a network failure does not leave a partially-hashed AIProvenance.

**Storage:** SHA-256 hex string (64 chars) in the existing `PromptHash` / `SourceDocHash` `string` fields on `entities.AIProvenance`. No struct schema change.

**Backwards compatibility:** Phase 2 ships `PromptHash == ""` / `SourceDocHash == ""`. Phase 3 starts populating them; downstream consumers (currently none) reading these fields must treat empty string as "Phase 2 era, hash unavailable".

**Test pin:** `TestQ4_AIProvenance_SHA256_Deterministic` — call B3 twice with identical footnote → assert hashes match (and equal known SHA-256 of the footnote text).

### 5.3 `ctx context.Context` threading

**Current state (Phase 2):**
```go
// internal/services/datacleaner/adjustments/liabilities.go
func (la *LiabilityAdjuster) ProcessLiabilityAdjustments(
    data *entities.FinancialData,
    rules []entities.AdjustmentRule,
    cleaningCtx *entities.CleaningContext,
) (*LiabilityAdjustmentResult, error) { /* ... */ }
```

**Phase 3 signature:**
```go
func (la *LiabilityAdjuster) ProcessLiabilityAdjustments(
    ctx context.Context,
    data *entities.FinancialData,
    rules []entities.AdjustmentRule,
    cleaningCtx *entities.CleaningContext,
) (*LiabilityAdjustmentResult, error) { /* ... */ }
```

**Same change for symmetry:**
- `ProcessAssetAdjustments(ctx, ...)`
- `ProcessEarningsAdjustments(ctx, ...)`

**Caller update:** `service.go::applyActiveAdjustments` already has `ctx` in scope (Phase 2 PR-1 added it). Pass it through.

**Existing test callers:** `liabilities_test.go`, `assets_test.go`, `earnings_test.go`, `datacleaner_simple_test.go`, and per-adjuster `*_adjuster_test.go` tests pass `context.Background()` or `context.TODO()` at the call site.

**Why ctx threading matters:**
- B3's AI path issues an outbound HTTP request that must respect request-scoped cancellation. Today it uses an internal `ctx` not derived from the caller, which means a client-cancellation during a `?trace=1` request leaves the AI call running.
- Future tracing instrumentation (OpenTelemetry spans on each adjuster) needs the ctx to attach spans.
- `logctx.From(ctx)` access for per-adjuster structured logging (instead of the package-singleton `*zap.Logger`) becomes possible — aligns with the `logctx.Or(ctx, ...)` convention enforced by `scripts/lint-logs.sh`.

**No `ctx` use yet in Phase 3.** The signature change is the deliverable; actual `ctx.Done()` checks + span attachment can come incrementally in Phase 4+. Setting up the plumbing now means later changes don't ripple the signature again.

### 5.4 Translator extraction decision — KEEP per-rule

PR-2 / PR-3 / PR-4 reviewer threads all surfaced the question "should we extract a single generic `translateOutputToLegacy` helper now that 13 translators exist?" Phase 2 deferred. Phase 3 settles it: **KEEP per-rule translators.** Rationale:

1. **Role flavors produce different output shapes.** OverlayEmitter (A1, B1/B2/B3) reads `out.Overlays[0].Amount`; Restater (A2/A4/A5/C1/C2/C3/C5/C6) reads `out.LedgerEntries[0].DeltaAmount`; FlagEmitter (C4/C7 + 2 reviews) always returns `Applied:false`. A single generic helper would need to switch on role, recovering exactly the per-rule code we'd "extract".
2. **The legacy `*AdjustmentResult` shape (`AssetAdjustmentResult`, `EarningsAdjustmentResult`, `LiabilityAdjustmentResult`) is itself category-specific.** A generic helper would need to take an interface parameter, defeating compile-time type safety.
3. **Phase 4 deletes them.** When consumers migrate to read from `CleanedFinancialData` views, the per-rule translators (which exist solely to maintain the dual-write to legacy `*AdjustmentResult`) become VESTIGIAL and are removed alongside the dual-write deletion. Extracting now would mean Phase 4 deletes the helper AND the per-rule call sites.
4. **YAGNI:** the 13 translators are 5–15 lines each. They are not duplicated across categories (they couldn't be; output shapes differ). Extraction has no callsite benefit.

**Phase 4 explicit:** the translator-extraction question is CLOSED. Phase 4 deletes translators alongside the dual-write; no extraction occurs in Phase 3 or Phase 4.

---

## 6. Pipeline integration

Phase 3 makes ONE change to `service.go::Clean`:

```diff
 func (s *Service) Clean(ctx context.Context, asReported *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.FinancialData, error) {
     working := asReported.DeepCopy()
     computePlugs(working)
     // ... existing applyActiveAdjustments(ctx, working, ...) ...
     // ... recomputeUmbrellas WARN log + ledger drain ...
     // ... createRiskWarningFlags + calculateQualityScore ...
-    return working, nil
+    return working, nil  // Phase 3 still returns *FinancialData for backwards compat
 }

 // NEW Phase 3 exported sibling:
 func (s *Service) CleanWithViews(
     ctx context.Context,
     asReported *entities.FinancialData,
     cleaningCtx *entities.CleaningContext,
 ) (*cleaneddata.CleanedFinancialData, error) {
     fd, err := s.Clean(ctx, asReported, cleaningCtx)
     if err != nil { return nil, err }
     return cleaneddata.New(fd), nil
 }
```

**Rationale:** keeping `Clean(ctx, ...)` returning `*FinancialData` means **zero call-site changes for any existing consumer** in Phase 3. Phase 4 consumers opt in to `CleanWithViews(ctx, ...)` one at a time as they migrate. Phase 5 (post-Phase-4) deletes the legacy `Clean(...)` signature.

The new method is a thin wrapper, but it gives Phase 4 a clean migration boundary: any consumer that calls `CleanWithViews` MUST consume views; any consumer still calling `Clean` is provably unmigrated. Grep-friendly: `grep -r "CleanWithViews"` enumerates migration progress.

---

## 7. Backwards compatibility

Phase 3 ships with **zero downstream behavior change**:

| Surface | Phase 3 state |
|---|---|
| `data.TotalAssets` read site | Unchanged (Phase 4 migrates) |
| `data.TotalDebt` read site | Unchanged (Phase 4 migrates; B3 routing flip pends) |
| `data.StockholdersEquity` read site | Unchanged (Phase 4 migrates) |
| `data.CurrentAssets` / `CurrentLiabilities` | Unchanged (Phase 4 migrates) |
| `data.OperatingIncome` / `NormalizedOperatingIncome` | Unchanged (Phase 4 migrates) |
| DCF / WACC / DDM / FFO / Graham outputs | Bit-for-bit unchanged |
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) | GREEN |
| Shadow snapshots | Byte-identical (no recompute changes) |
| Replay golden bundles | Numeric drift = 0; STRUCTURAL drift only on `13-cleaner-audit.json` (Q2 `tax_shield_dta` populated for A2; Q4 `prompt_hash`/`source_doc_hash` populated for B3). Use `--allow-schema-drift`. |

The only consumer change in Phase 3 is the **internal** addition of `CleanWithViews` as an additional `Service` method; no existing consumer is forced to call it.

---

## 8. Testing strategy

### 8.1 New tests (Phase 3)

1. **`TestCleanedFinancialData_Restated_BitForBitOnNoFiredAdjusters`** — property test (gopter): for any `FinancialData` with empty `AdjustmentLedger` and empty `Overlays`, `Restated()` returns a view byte-equal to `AsReported()`. Pins that the recompute-from-components path produces identity when no adjuster fired (assuming Phase 0 plugs are populated, which the cleaner guarantees post-Phase-0).

2. **`TestCleanedFinancialData_InvestedCapital_AppliesOverlays`** — table-driven: synthetic `FinancialData` with each overlay type. Assert `DebtLikeClaims` accumulates B1+B2+B3 amounts; `TotalAssets` decremented by A1 goodwill amount; `TotalDebt` unchanged from Restated.

3. **`TestCleanedFinancialData_AsReported_PreservesParserZeros_AMD_KO`** — load AMD and KO fixtures from `internal/integration/testdata/` (or synthesize from the shadow snapshots' `as_reported_total_liabilities=0` cases). Assert `AsReported().TotalLiabilities == 0` and `Restated().TotalLiabilities > 0` (component-sum reconstruction).

4. **`TestCleanedFinancialData_Restated_C6EquityOffsetZero`** — synthesize a `FinancialData` with C6 fired (`Component="InterestExpense"`, `DeltaAmount != 0`, `EquityOffset == 0`). Assert `Restated().StockholdersEquity == AsReported().StockholdersEquity` (C6's DeltaAmount does NOT flow through equity). Mirrors the Phase 2 dispatcher test's `NativeC6Emission` subtest invariant.

5. **`TestCleanedFinancialData_Restated_MemoizationIdempotent`** — call `Restated()` twice; assert pointer-identity on the returned `*FinancialDataView`.

6. **`TestQ2_A2TaxShieldDTA_Populated`** — fire A2 with `EffectiveTaxRate=0.25` and `writedownAmount=$100M`; assert `LedgerEntry.TaxShieldDTA == $25M`. Fire with `EffectiveTaxRate=0`; assert `TaxShieldDTA == 0`. **Replaces** the Phase 2 `fired_path_TaxShieldDTA_stays_zero_per_Q2_deferral` pin (delete the old subtest in the same commit).

7. **`TestQ4_AIProvenance_SHA256_Deterministic`** — call B3 AI path twice with identical footnote → assert `PromptHash` and `SourceDocHash` are equal across calls AND equal known SHA-256 hex of inputs. Call with different footnote → assert hashes differ. Test uses a stub `ai.AnalyzeFootnote` to avoid network.

8. **`TestCtxThreading_LiabilityAdjusterReceivesCtx`** — pass a `ctx` derived from `context.WithCancel(parent)`, cancel before calling `ProcessLiabilityAdjustments`, assert ctx-cancellation does not crash the adjuster (validates that adding `ctx` parameter does not introduce a nil-deref).

9. **`TestCleanedFinancialData_ImportBoundary`** — `cleaneddata` imports only `entities` from `internal/`. Mirrors the Phase 0/2 pattern of asserting the package's import boundary at test time.

### 8.2 Invariants pinned (must stay GREEN)

| Invariant | Pin |
|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) | DDM continues to read `data.*` directly until Phase 4 |
| `TestRecomputeUmbrellas_NoMutation` | Recompute shim unchanged |
| `TestOrchestrator_LedgerOrdering` | Asset → liability → earnings partition preserved |
| `TestDataCleanerRecompute_ShadowMode_TickerBasket` + shadow-snapshot byte-identity | Phase 3 does not change adjuster execution; snapshots stay byte-identical |
| `TestLedger_BasketSnapshot_ClusterPrediction` | Adjuster emission unchanged; 10/10 basket tickers PASS |
| Full `go test ./... -count=1` | GREEN (modulo pre-existing scheduler-test race noted in PR-1's QA report) |

### 8.3 Replay drift expectation

Run `go run ./cmd/replay artifacts/tier2-baseline/<date>/AAPL/` after each commit. Expected:
- **Numeric drift in `17-response.json`**: ZERO across all 10 basket tickers.
- **Structural drift in `13-cleaner-audit.json`**: Q2 populates `tax_shield_dta` for A2 (was 0); Q4 populates `ai_provenance.prompt_hash` + `source_doc_hash` for B3 (were `""`). Use `--allow-schema-drift` on the SchemaVersion-bump commit.
- **Numeric drift in `10-clean-output.json` ledger field**: ZERO (the ledger entries' float fields are unchanged; only the JSON shape gains populated `tax_shield_dta` + hash strings).

---

## 9. PR strategy

Two options for the implementer:

### Option A — Single PR (recommended if effort fits one agent shift)

- One PR containing: `cleaneddata` package + Q2 + Q4 + ctx threading + SchemaVersion 8→9 + test suite.
- Easier to review as a coherent unit (view reconstruction depends on Q2/Q4 populating the fields it consumes).
- Branch: `dc1-phase-3` from `dc1-phase-2-pr-4` final tip (or master after HUMAN merge — implementer's choice).

### Option B — 2-PR split

- **PR-1 (`dc1-phase-3-pr-1`)**: `cleaneddata` package (`AsReported`/`Restated`/`InvestedCapital` accessors) + `CleanWithViews` Service method + import-boundary test + property tests. Reads Phase 2's TaxShieldDTA=0 / empty hashes verbatim.
- **PR-2 (`dc1-phase-3-pr-2`)**: Q2 (A2 TaxShieldDTA population) + Q4 (B3 AIProvenance hashes) + ctx threading + SchemaVersion 8→9 + bundle baseline refresh.

**Recommendation:** Option A. The view accessor logic is small (~200 LOC), and the Q2/Q4 resolutions are independent 20-line changes. Splitting introduces extra HUMAN signoff cycles without test-isolation benefit; the SchemaVersion bump is atomic with Q4's first populating commit regardless of which PR holds it.

If PR-1's review surfaces design feedback, fall back to Option B for the iteration.

---

## 10. Phase 3 → Phase 4 gate

Phase 4 starts only after **all** of:

1. All Phase 3 invariants GREEN (see §8.2).
2. `TestCleanedFinancialData_AsReported_PreservesParserZeros_AMD_KO` GREEN — confirms the T2-BS-3 carve-out reconstruction works.
3. `TestQ2_A2TaxShieldDTA_Populated` and `TestQ4_AIProvenance_SHA256_Deterministic` GREEN — confirms the Q-resolutions land.
4. The basket integration test `TestLedger_BasketSnapshot_ClusterPrediction` (PR-4 Task 4.6) is **extended** to also assert the Restated view's truthful `TotalLiabilities` reconstruction for AMD and KO (the T2-BS-3 acceptance criterion the parent spec identifies as the gate).
5. Replay diff on the full basket (`go run ./cmd/replay artifacts/tier2-baseline/<date>/`) shows zero numeric drift and only the documented structural drift (`tax_shield_dta` + hash fields populated).
6. Phase 3 closeout doc filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md`.

Phase 4 itself is the consumer-migration gate; its scope (13 read sites + B3 routing flip + dual-write deletion) is enumerated in the parent spec §"Consumer migration map".

---

## 11. Open questions

None for Phase 3. The two Q-deferrals from Phase 2 are resolved in §5 of this spec; the translator-extraction question is closed in §5.4; ctx threading is fully specified in §5.3.

Phase 4 inherits the standard reviewer-deferred questions (B3 WACC opt-in knob, `WACCInputs.IncludeDebtLikeClaimsInCapitalStructure`) per the parent spec §"Open questions for implementation" — out of Phase 3 scope.

---

## 12. Acceptance criteria

- [ ] This spec lands at `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md`
- [ ] Implementer plan filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md`
- [ ] `cleaneddata` package exists with `New`, `AsReported`, `Restated`, `InvestedCapital`
- [ ] `Service.CleanWithViews(ctx, ...)` exists as a sibling of `Service.Clean(ctx, ...)`
- [ ] Q2 resolved: A2 populates `TaxShieldDTA` when `EffectiveTaxRate > 0`
- [ ] Q4 resolved: B3 populates `PromptHash` + `SourceDocHash` as SHA-256 hex strings
- [ ] ctx threaded through `ProcessLiabilityAdjustments` + symmetric asset/earnings sibling signatures
- [ ] `SchemaVersion["FinancialData"]` 8 → 9 atomic with the first populating commit
- [ ] All Phase 3 new tests GREEN
- [ ] All load-bearing Phase 2 invariants GREEN
- [ ] Replay diff on basket shows zero numeric drift, documented structural drift only
- [ ] Phase 3 closeout doc filed before Phase 4 dispatch

---

## 13. Change log

| Date | Change |
|---|---|
| 2026-05-23 | Initial spec authored by Phase 2 closeout ARCH. Covers `CleanedFinancialData` view reconstruction (`AsReported`/`Restated`/`InvestedCapital`), Q2 (A2 TaxShieldDTA), Q4 (AIProvenance SHA-256 hashes), `ctx` threading through `Process*Adjustments` signatures, translator-extraction decision (KEEP per-rule), and T2-BS-3 Option B carve-out reconstruction in `Restated()`. PR strategy: single PR recommended. Phase 3 → Phase 4 gate documented. Implementation plan filed alongside at `datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md`. |
