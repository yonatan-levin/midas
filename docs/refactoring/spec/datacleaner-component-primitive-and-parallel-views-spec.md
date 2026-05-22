# Datacleaner — Component Primitive & Parallel Views Spec

**Status:** DESIGN (brainstorm-validated, 2026-05-15 — superseded only by implementation plan)
**Tracker origin:** `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` (filed 2026-05-05)
**Estimated effort:** ~3 weeks focused work, 5 independently-mergeable phases
**Pressure tested by:** GPT-5.5-pro deep-analysis pass (2026-05-15) — three substantive corrections incorporated (single `Adjuster` interface, B3 → `DebtLikeClaims`, `AmountSemantics`)

---

## Problem

The current datacleaner mutates `entities.FinancialData` in place via a battery of adjuster rules (A1 goodwill exclusion, A2 intangible writedown, A4 DTA valuation allowance, A5 inventory writedown, B1-B3 liability adjustments, C1-C7 earnings normalizations). Three structural defects coexist:

1. **Umbrella/component desync.** `assets.go:228-232` reduces `Inventory` and `TotalAssets` together but does NOT propagate to `CurrentAssets`. Any consumer reading post-clean `CurrentAssets` sees a stale value.

2. **`StockholdersEquity` is never mutated.** Grep across `internal/services/datacleaner/` confirms zero `StockholdersEquity` mutations. After cleaning, MXL Q1 2026 has `total_assets = $387.4M`, `stockholders_equity = $454.2M`, implied `total_liabilities = −$66.8M` — violating the accounting identity.

3. **"Valuation overlay" conflated with "as-of restatement".** A1 goodwill exclusion is a Damodaran ROIC normalization (goodwill is real, just excluded from invested-capital math), but the implementation hard-restates `TotalAssets` as if goodwill never existed. The audit-trail wording at `assets.go:107` even says "exclusion" — but the code says "restatement". Wording and implementation disagree.

For MXL specifically: $383.86M of total-assets reduction post-clean, of which $318.6M (83%) comes from goodwill exclusion alone — the single largest contributor to the asymmetry.

The asymmetry is currently harmless because no downstream math depends on a balanced balance sheet — but every new feature that does (NCAV, tangible-book-equity-per-share, ROE-based screens, Altman-Z, Piotroski-F, P/B ratios) immediately hits it.

## Solution shape — Three views, one ledger, lazy reconstruction

Replace the cleaner's current single-output contract (`*FinancialData` mutated in place) with a triple-view output:

```go
type CleanedFinancialData struct {
    AsReported *FinancialData     // pristine; SEC parser output + plug fill
    Ledger     *AdjustmentLedger  // chronological adjuster outputs
    Overlays   []OverlaySpec      // declarative overlay declarations
}

func (c *CleanedFinancialData) Restated() *FinancialData         // lazy + cached
func (c *CleanedFinancialData) InvestedCapital() *FinancialData  // lazy + cached
```

**Three views, three audiences:**

| View | Built from | Consumed by | Semantic meaning |
|---|---|---|---|
| `AsReported` | SEC parser + plug-fill | Graham NCAV, audit/reconciliation | Pristine as-filed numbers |
| `Restated` | `AsReported` + ledger entries | DCF cash-flow, ROIC, ROE, working capital | Real economic position after impairments |
| `InvestedCapital` | `Restated` + overlay specs | WACC weights, EV→Equity bridge | Damodaran-style analytical view |

The Restated and InvestedCapital views are *deterministic functions* of their inputs. We do not persist them; we persist the inputs (`AsReported` + `Ledger` + `Overlays`) and reconstruct views on read. This keeps the SQL schema delta minimal (one new JSON column) and makes replay backwards-compatible (old bundles with empty ledgers render as AsReported-only).

---

## Data model

### Entity extensions (Phase 0)

Add **four plug fields** to `entities.FinancialData`. The SEC parser computes them at parse time so components always sum exactly to umbrellas:

```go
// internal/core/entities/financial_data.go — additions:
OtherCurrentAssets         float64 `json:"other_current_assets"`         // plug: CA − (Cash + Inventory + Prepaid_unknown)
OtherNonCurrentAssets      float64 `json:"other_non_current_assets"`     // plug: NCA − (Goodwill + Intangibles + DTA + PPE_unknown)
OtherCurrentLiabilities    float64 `json:"other_current_liabilities"`    // plug
OtherNonCurrentLiabilities float64 `json:"other_non_current_liabilities"`// plug
```

No new XBRL tag extraction is required — the SEC parser computes each plug as `umbrella − sum(known components)` at the end of `parsePeriodData`. This bounds the entity growth to exactly 4 fields regardless of how many components adjusters touch.

### Adjuster output

```go
// internal/services/datacleaner/adjustments/contracts.go (new)

type Adjuster interface {
    // Apply runs the adjuster against the working-copy FinancialData.
    // Restaters mutate component fields and return LedgerEntries with
    // EquityOffset set. OverlayEmitters return OverlaySpec entries and
    // leave the working copy untouched. Hybrid adjusters return both.
    // Flags preserve the existing Flag-emission surface.
    Apply(working *entities.FinancialData, ctx *entities.CleaningContext) AdjusterOutput
}

type AdjusterOutput struct {
    LedgerEntries []LedgerEntry
    Overlays      []OverlaySpec
    Flags         []entities.Flag
}
```

One interface, three roles emerging from the output shape:
- **Pure Restater:** returns `LedgerEntries` only (A2, A4, A5, C1-C7).
- **Pure OverlayEmitter:** returns `Overlays` only (A1, B1, B2, B3).
- **Hybrid:** returns both (no current adjusters; future factoring-with-recourse where failed-derecognition tests fail).

### Ledger entry

```go
type AdjustmentLedger struct {
    Entries []LedgerEntry  // ordered chronologically; each carries Fired
}

type LedgerEntry struct {
    Fired        bool                // true=applied, false=skipped (observability)
    AdjusterID   string              // "A5_inventory_writedown"
    RuleID       string

    // Populated when Fired:
    Component    string              // "Inventory"
    DeltaAmount  float64             // signed; e.g. −34_336_000
    EquityOffset float64             // signed; flows to Restated.StockholdersEquity
    TaxShieldDTA float64             // optional; counterpart entry to DeferredTaxAssets

    // Populated when !Fired:
    SkipReason   string              // "goodwill_ratio=4.2% below 5% threshold"
    SkipMetrics  map[string]float64  // {"goodwill_ratio": 0.042, "threshold": 0.05}

    Reasoning    string
    Timestamp    time.Time
}
```

`Fired` is a bool (per design discussion): skipped rules still receive a ledger entry so observability can answer "why didn't A1 fire on this ticker?" without code reading.

### Overlay specification

```go
type OverlaySpec struct {
    OverlayID       string             // "B1_operating_lease_capitalization"
    RuleID          string
    Field           string             // "TotalDebt", "InterestBearingDebt", "DebtLikeClaims"
    Operation       string             // "add" | "subtract" | "zero"
    Amount          float64
    AmountSemantics AmountSemantics    // Incremental | Replacement | Delta
    Reasoning       string
    Flags           []entities.Flag
    AIProvenance    *AIProvenance      // populated when AI-derived (B3 AI path)
}

type AmountSemantics string
const (
    Incremental AmountSemantics = "incremental" // amount added on top of field's current value
    Replacement AmountSemantics = "replacement" // amount replaces the field's current value
    Delta       AmountSemantics = "delta"       // amount is a relative delta vs field's current value
)

type AIProvenance struct {
    ModelName     string    // "claude-haiku-4-5-20251001"
    PromptHash    string    // sha256 of prompt template
    SourceDocHash string    // sha256 of footnote text consumed
    ExtractedSpan string    // exact text span the AI processed
    Probability   float64   // AI's output
    Confidence    float64   // AI's confidence
    Timestamp     time.Time
}
```

**AmountSemantics matters for double-count prevention.** B1 operating-lease capitalization today routes the full PV to TotalDebt with implicit Incremental semantics. The SEC parser explicitly excludes lease liabilities from `TotalDebt` extraction (`parser.go:714-721`), so Incremental is correct for current behavior. The explicit tagging prevents future drift.

**AI provenance for B3.** When the AI service is enabled, contingent-liability probability weighting is non-deterministic across runs. The `AIProvenance` block captures everything needed to reconstruct the AI decision deterministically. The AI result must be cached per `(ticker, period, prompt_version)` so replay golden bundles are bit-for-bit reproducible.

---

## Adjuster reclassification

Every existing adjuster gets reclassified into the new contract. The classification rule:

- **Restater** when the adjustment represents real economic information not yet reflected in the as-filed balance sheet (impairments, write-downs that flow through retained earnings).
- **OverlayEmitter** when the adjustment is an analytical convention applied for valuation purposes only (Damodaran ROIC normalization, re-bucketization for WACC weights).
- **Hybrid** when the adjustment touches both (no current examples).

| Adjuster | Today's location | New role | Target field(s) | Rationale |
|---|---|---|---|---|
| A1 goodwill exclusion | `assets.go:30-109` | OverlayEmitter | `Goodwill`→zero, `TotalAssets` propagate | Damodaran convention; goodwill is real and legally owned, just excluded from invested-capital |
| A2 intangible writedown | `assets.go:111-194` | Restater | `OtherIntangibles` mutation; EquityOffset = −writedown | Real impairment; flows through retained earnings |
| A4 DTA valuation allowance | `assets.go:271-322` | Restater | `DeferredTaxAssets` mutation; EquityOffset = −allowance | Real impairment; DTA won't be realized |
| A5 inventory writedown | `assets.go:196-269` | Restater | `Inventory` mutation; EquityOffset = −writedown; TaxShieldDTA = writedown × effective_tax_rate | Real impairment with counterpart tax-shield entry |
| **B1 operating lease cap** | `liabilities.go:107-224` | OverlayEmitter | `TotalDebt`, `InterestBearingDebt` with Incremental semantics | ASC 842 already capitalizes leases on balance sheet; B1 just re-buckets for WACC |
| **B2 pension/OPEB** | `liabilities.go:287-360` | OverlayEmitter | `TotalDebt`, `InterestBearingDebt` with Incremental semantics | GAAP recognizes underfunded pension as a balance-sheet liability; B2 re-buckets for WACC |
| **B3 contingent liabilities** | `liabilities.go:362-456` | OverlayEmitter | `DebtLikeClaims` (NEW — NOT TotalDebt) | Contingencies are claims against EV, not capital providers — must not distort WACC weights |
| C1-C7 earnings normalizations | `earnings.go` | Restater | `OperatingIncome`, `NetIncome` mutation; EquityOffset propagates | Real income statement adjustments flow to retained earnings |

### B3 routing correction — the substantive accuracy change

This is the only consumer-visible behavior change in the refactor. Today, B3 contingent liabilities flow into `TotalDebt` via the orchestrator at `liabilities.go:87-88`, which then feeds WACC. The result: for filers with material legal/environmental contingencies (large pharma, mining, financials), WACC weights are distorted because contingent liabilities are treated as if they were interest-bearing capital.

After this refactor:
- B3 emits `OverlaySpec{Field: "DebtLikeClaims", ...}`.
- WACC reads `InvestedCapital.CapitalStructureDebt()` (computed as `TotalDebt + B1 + B2` overlays only).
- EV→Equity bridge reads `InvestedCapital.CapitalStructureDebt() + InvestedCapital.DebtLikeClaims`.

The two metrics never collapse into one again.

---

## Pipeline flow

```
                  ┌─────────────────────────────────────────────┐
                  │  service.Clean(ctx, asReported, cleaningCtx)│
                  └─────────────────────────────────────────────┘
                                    │
                                    ▼
              ┌─────────────────────────────────────┐
              │  working := asReported.DeepCopy()   │   ← AsReported never touched again
              └─────────────────────────────────────┘
                                    │
                                    ▼
              ┌─────────────────────────────────────┐
              │  computePlugs(working)              │   ← fills 4 plug fields
              └─────────────────────────────────────┘
                                    │
                                    ▼
              ┌─────────────────────────────────────┐
              │  for each adjuster a in pipeline:   │
              │    output := a.Apply(working, ctx)  │
              │    ledger.Entries.append(output.LedgerEntries)
              │    overlays.append(output.Overlays) │
              │    flags.append(output.Flags)       │
              └─────────────────────────────────────┘
                                    │
                                    ▼
              ┌─────────────────────────────────────┐
              │  recomputeUmbrellas(working)        │   ← sums components into umbrellas
              │  applyEquityOffset(working, ledger) │   ← sum(EquityOffset) → StockholdersEquity
              │  applyTaxShieldDTA(working, ledger) │   ← sum(TaxShieldDTA) → DeferredTaxAssets
              └─────────────────────────────────────┘
                                    │
                                    ▼
              ┌──────────────────────────────────────┐
              │ return &CleanedFinancialData{        │
              │   AsReported: asReported,            │
              │   Ledger:     ledger,                │
              │   Overlays:   overlays,              │
              │ }                                    │
              │ // Restated() returns 'working'.     │
              │ // InvestedCapital() = Restated +    │
              │ //   applied overlays (computed on   │
              │ //   first access; cached).          │
              └──────────────────────────────────────┘
```

Two invariants this flow enforces by construction:

1. **`AsReported` is immutable** because `working := asReported.DeepCopy()` happens before anything mutates.
2. **Accounting equation holds in `Restated`** because (a) components mutate, (b) umbrellas recompute as sums of components, (c) equity offsets are explicit and applied last.

The recompute step is what fixes B1 — today the inventory writedown updates `Inventory` and `TotalAssets` but not `CurrentAssets`. Under the new flow, `TotalAssets` and `CurrentAssets` are both recomputed as sums of their components after all Restaters run.

---

## Observability — four layers

Today the artifact bundle has `99-narrate.jsonl` (Tier-1 pipeline phases), `99-debug-trace.jsonl` (Tier-2 trace lines), and per-stage JSON snapshots. This refactor adds four new observability surfaces.

### Layer A — Parse provenance (NEW artifact `09-parse-provenance.json`)

Captures which XBRL tag won for each entity field, multi-currency disambiguation, taxonomy presence, and plug computations.

```go
type ParseProvenance struct {
    Periods map[string]*PeriodProvenance  // keyed by "2024FY" etc.
}

type PeriodProvenance struct {
    CIK                 string
    Period              string
    Currency            string                       // chosen
    CandidateCurrencies map[string]int               // {"TWD": 47, "USD": 47} when multi-currency
    Taxonomies          []string                     // ["us-gaap", "ifrs-full", "dei"]
    FieldMatches        map[string]MatchRecord
    PlugComputations    map[string]PlugRecord
}

type MatchRecord struct {
    Field                  string   // "OperatingIncome"
    ChosenTag              string   // "ifrs-full:ProfitLossFromOperatingActivities"
    CandidatesConsidered   []string // full findValue input list, in priority order
    Strategy               string   // "first-hit" | "sum-of-components"
    Value                  float64
}

type PlugRecord struct {
    PlugField        string             // "OtherCurrentAssets"
    UmbrellaField    string             // "CurrentAssets"
    UmbrellaReported float64
    KnownComponents  map[string]float64 // {"CashAndCashEquivalents": 100M, "Inventory": 85M, ...}
    PlugComputed     float64            // residual
}
```

Answers: "which XBRL tag did `OperatingIncome` come from for TSM?", "did multi-currency disambiguation fire?", "what residual landed in OtherCurrentAssets and why?"

Size: ~3-5 KB per period. Written by the SEC parser before the cleaner runs.

### Layer B — Cleaning audit (single merged file `13-cleaner-audit.json`)

The `AdjustmentLedger` plus all `OverlaySpec` entries plus flags, in one file. Every adjuster that evaluated has an entry, regardless of whether it fired.

```jsonl
{
  "ledger": {"entries": [
    {"fired": true,  "adjuster_id": "A5_inventory_writedown", "component": "Inventory", "delta_amount": -34336000, "equity_offset": -34336000, "tax_shield_dta": 7210560, "reasoning": "..."},
    {"fired": false, "adjuster_id": "A1_goodwill_exclusion",  "skip_reason": "goodwill_ratio=4.2% below 5% threshold", "skip_metrics": {"goodwill_ratio": 0.042, "threshold": 0.05}}
  ]},
  "overlays": [
    {"overlay_id": "B1_operating_lease_capitalization", "field": "TotalDebt", "operation": "add", "amount": 254000000, "amount_semantics": "incremental"},
    {"overlay_id": "B3_contingent_liabilities", "field": "DebtLikeClaims", "operation": "add", "amount": 50000000, "amount_semantics": "incremental", "ai_provenance": {...}}
  ],
  "flags": [...]
}
```

Size: ~7 KB per request. Merged into a single file because total volume is small and `jq '.ledger.entries | group_by(.fired)'` is the natural query shape.

### Layer C — View reconstruction trace (DEV-ONLY, gated by `logging.trace_view_reconstruction`)

Per-call trace lines emitted from `.Restated()` and `.InvestedCapital()` getters, capturing the caller via `runtime.Caller(1)`.

```jsonl
// 99-debug-trace.jsonl — only emitted when dev flag enabled:
{"ts":"...","level":"DEBUG","msg":"trace.cleaner.view.consumed","view":"InvestedCapital",
 "call_site":"valuation.service.computeWACC","cached":false}
{"ts":"...","level":"DEBUG","msg":"trace.cleaner.view.consumed","view":"Restated",
 "call_site":"valuation.service.computeROIC","cached":true}
```

Production overhead: one boolean check per view access. Disabled by default — same pattern as the existing `logging.trace_calculations` gate.

Use case: empirical consumer-mapping verification after Phase 4 migration.

### Layer D — Narrate stream consolidation

One narrate phase replaces today's `clean.normalized`:

```jsonl
{"phase":"clean.completed",
 "match":   {"fields": 34, "plugs": 4, "currency": "TWD", "candidate_currencies": ["TWD","USD"]},
 "restate": {"fired": 3, "skipped": 9, "total_asset_delta": -68000000, "total_equity_offset": -68000000},
 "overlay": {"applied": 2, "investedcapital_total_assets_delta": -318600000}}
```

One Tier-1 line per request, three nested sub-objects (one per layer). Consistent with existing midas convention (`fetch.fanout`, `valuation.computed` — one line per pipeline stage).

---

## Persistence

**Schema delta:** one new JSON column on `financial_data` table:

```sql
ALTER TABLE financial_data ADD COLUMN adjustment_ledger TEXT;
-- Stores serialized {Ledger, Overlays} object.
-- Existing balance-sheet columns now semantically mean "AsReported" (no rename).
```

**Read path:**

```go
fd := loadFinancialData(...)            // AsReported view, existing columns
ledger := loadLedger(fd.adjustment_ledger) // tolerant JSON decode for forward compat
cleaned := datacleaner.Reconstruct(fd, ledger)
// cleaned.AsReported, cleaned.Restated(), cleaned.InvestedCapital() available
```

**Backwards compatibility:** old cached rows have `adjustment_ledger = NULL` → reconstruct yields `AsReported` only (Ledger and Overlays both empty). Cache version bump not required; old rows naturally degrade to single-view behavior until they expire.

---

## Replay golden bundles

After Phase 4 lands, all existing bundles regenerate. New bundle layout:

```
08-sec-raw.json              (existing) raw SEC response bodies
09-parse-provenance.json     (NEW)      Layer A — match records + plug math
10-asreported.json           (renamed)  was: 10-clean-input.json
11-restated.json             (NEW)      Layer B target view
12-investedcapital.json      (NEW)      Layer B target view
13-cleaner-audit.json        (NEW)      Layer B fired+skipped+overlays+flags (merged)
14-overlay-config.json       (NEW)      Overlay declarations (subset of 13)
15-valuation.json            (existing) downstream consumer of cleaned views
17-response.json             (existing) final HTTP response
99-narrate.jsonl             (existing) Tier-1 stream + consolidated clean.completed phase
99-debug-trace.jsonl         (existing) Tier-2 stream + new trace.cleaner.view lines
```

Replay tool's `--diff-stages` flag gains per-view diff modes: `--diff-view=asreported`, `--diff-view=restated`, `--diff-view=investedcapital`. Existing bundles ship via the `--allow-schema-drift` flag introduced in Phase 2.D R3a until they regenerate naturally.

---

## Consumer migration map

13 read sites in `internal/services/valuation/` and adjacent packages. Each migrates to a specific view:

| File:line | Today reads | New view | Rationale |
|---|---|---|---|
| `service.go:586` ROIC denominator | `latestForROIC.StockholdersEquity` | Restated | Coherent with NOPAT numerator (Restater-touched OperatingIncome) |
| `service.go:1567-1571` working capital (latest) | `latest.CurrentAssets/CurrentLiabilities` | Restated | Real economic balances |
| `service.go:1581-1585` working capital (prior) | `prior.CurrentAssets/CurrentLiabilities` | Restated | Same |
| `service.go: WACC entry` | `data.TotalDebt`, bridge inputs | InvestedCapital via `WACCInputs` type | Compile-time gate; new type wraps `CapitalStructureDebt` |
| `service.go: EV→Equity bridge` | `TotalDebt`, `Cash`, `MinorityInterest`, `PreferredEquity` | InvestedCapital | Bridge needs `CapitalStructureDebt + DebtLikeClaims` |
| `service.go: DCF FCF inputs` | `OperatingIncome`, `OperatingCashFlow`, `CapitalExpenditures`, `D&A` | Restated | Restated == AsReported for CFS today; explicit anyway |
| `service.go: NOPAT` | `OperatingIncome`, `TaxRate` | Restated | C1-C7 earnings normalizations flow through |
| `graham.go:61` NCAV current assets per share | `fd.CurrentAssets / dilutedShares` | AsReported | Conservative as-filed metric |
| `graham.go:64` NCAV formula | `(fd.CurrentAssets - totalLiabilities) / dilutedShares` | AsReported | Same |
| `graham.go:106-117` TotalLiabilities derivation | `fd.TotalLiabilities`, `fd.TotalAssets`, `fd.StockholdersEquity` | AsReported | Derivation path eliminated — AsReported is balanced by construction |
| `currency.go:209-224` FX conversion | `fd.CurrentAssets *= rate`, etc. | runs on AsReported pre-cleaner | All three views inherit USD automatically |
| `ddm.go:104,230` ROE | `latest.NetIncome / latest.StockholdersEquity` | Restated | Quality ratio; both sides real economic |
| `ddm.go:139` book-value-per-share | `latest.StockholdersEquity / SharesOutstanding` | Restated | Book equity = restated equity after writedowns |

**WACCInputs compile-time boundary** (new type to prevent silent-fail):

```go
// internal/services/valuation/wacc/inputs.go (new)
type WACCInputs struct {
    CapitalStructureDebt float64  // built from InvestedCapital view; explicit name
    MarketEquity         float64
    TaxRate              float64
}

// WACC signature changes from:
func ComputeWACC(data *entities.FinancialData, ...) float64
// to:
func ComputeWACC(inputs WACCInputs) float64

// Only constructor:
func BuildWACCInputs(ic *FinancialData /* InvestedCapital view */, market MarketData) WACCInputs
```

The compiler enforces the migration. No consumer can accidentally pass `Restated` instead of `InvestedCapital`.

---

## Phasing & implementation sequence

Five phases, each independently mergeable. User-visible behavior changes ONLY in Phase 4.

| Phase | Scope | Estimate | Gate to next phase |
|---|---|---|---|
| **0 — Plug + entity extension** | Add 4 plug fields to `FinancialData`. SEC parser computes plugs at parse time. Property test: components sum to umbrellas. No behavior change anywhere downstream. | 2-3 days | Plug values match (umbrella − sum) across ticker basket |
| **1 — Component primitive + recomputeUmbrellas shim** | Add `recomputeUmbrellas()` called at end of pipeline. **Shadow mode**: log warning if recomputed value diverges from existing mutated umbrella. No behavior change; pure observation. | 3-5 days | Shadow warnings analyzed across basket; expected divergences only |
| **2 — `Adjuster` interface + `AdjustmentLedger`** | Add `Adjuster` interface, `LedgerEntry`, `OverlaySpec`, `AdjusterOutput`. Refactor existing adjusters. Pipeline collects ledger + overlays but still also mutates input pointer to preserve current behavior. | 5-7 days | Ledger entries match expected adjuster outputs for basket |
| **3 — `CleanedFinancialData` + view reconstruction** | Add `CleanedFinancialData`, `Restated()`, `InvestedCapital()`. Pipeline stops mutating input; creates working copy. SQLite column for ledger. Replay bundles gain new artifacts. Consumers still read old `*FinancialData` shape via `cleaned.Restated()` to preserve byte-for-byte output. | 5-7 days | `Restated()` produces bit-for-bit identical results to today's single-view cleaner output across basket |
| **4 — Consumer migration + WACC boundary** | One PR per consumer group. After all 13 sites migrate, B3 routing finally takes effect → WACC weights shift for filers with material contingents. Replay golden bundles regenerate. | 7-10 days | Each consumer PR independently approved against pinned expectations |
| **Total** | | **~3 weeks** | |

**Shadow-mode warning gate (Phase 1 → Phase 2):** before Phase 2 lands, the shadow-mode warnings from Phase 1 must be analyzed across a representative ticker basket. Any unexpected divergence (i.e., umbrella does NOT equal sum-of-components for an unexpected reason) is a red flag for hidden assumptions we haven't surfaced.

**Bit-for-bit gate (Phase 3 → Phase 4):** before consumer migration, `cleaned.Restated()` must produce bit-for-bit identical results to today's `*FinancialData` output across the basket. If they diverge, Phase 3 has a bug to fix before consumers start reading different views.

---

## Testing strategy

### T1 — Accounting-equation property test (Phase 1+)

```go
// internal/services/datacleaner/recompute_test.go (new)
func TestRecomputeUmbrellas_AccountingEquationHolds(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        fd := genRandomFinancialData(t)  // gopter-style generator
        recomputeUmbrellas(&fd)
        epsilon := 0.01  // 1 cent float tolerance
        require.InDelta(t,
            fd.TotalAssets,
            fd.TotalLiabilities + fd.StockholdersEquity,
            epsilon,
        )
    })
}
```

### T2 — Restater preserves accounting equation; OverlayEmitter does not mutate statement totals (Phase 2+)

```go
func TestRestater_PreservesAccountingEquation(t *testing.T) { /* runs each Restater individually */ }
func TestOverlayEmitter_DoesNotMutateStatementTotals(t *testing.T) { /* runs each Overlay; asserts working copy unchanged */ }
```

### T3 — B3 routing correctness (Phase 4)

```go
func TestWACC_IsUnaffectedBy_B3ContingentLiabilities(t *testing.T) {
    // Fixture: AAPL-like with $1B contingent liability.
    // Expected: WACC weight unchanged vs no-B3 case.
    // Pins the DebtLikeClaims separation.
}

func TestEVtoEquityBridge_IncludesB3DebtLikeClaims(t *testing.T) {
    // Fixture: same; bridge SHOULD subtract $1B contingent.
    // Pins that B3 still affects intrinsic value via the bridge.
}
```

### T4 — Replay golden-bundle regression basket (Phase 3 + 4)

Ticker basket: **AAPL, MSFT, JNJ, KO, F, AMD, MXL, TSM, BABA, EQIX**. Bundle each, run replay, diff.

- Phase 3 must produce bit-for-bit identical `Restated.*` to today's single-view cleaner output for all 10.
- Phase 4 will introduce diffs in the WACC denominator for any ticker with material B3 overlays (TSM, AMD, maybe BABA) — those diffs get manually approved as correctness improvements.

### T5 — AI provenance reproducibility (Phase 4)

If AI is enabled in the test, hashes for prompt/source/output must match the fixture. Ensures replay golden bundles stay deterministic.

---

## Open questions for implementation

Items intentionally not resolved in this spec because they're better decided during Phase 0/1 with empirical data:

1. **Exact set of cleaner stages that emit narrate sub-objects.** Today the narrate stream has `clean.normalized`; the new `clean.completed` consolidates match + restate + overlay. The spec mandates the structure but the specific field names inside each sub-object should match Phase 2's actual implementation.

2. **PlugRecord granularity for IFRS-full filers.** IFRS-full filers may have different umbrella decomposition (e.g., separate `OtherFinancialAssets` line that doesn't exist under US-GAAP). The Phase 0 parser implementation will need to decide whether to add taxonomy-specific plug-naming, or keep the four-plug shape and absorb taxonomy quirks into them.

3. **B1 lease-PV-vs-GAAP-PV delta semantics.** Today's code treats the full PV as Incremental. If a future tracker concludes that the delta is genuine economic information (not just discount-rate disagreement), B1's classification could shift to Hybrid (Restater for the delta + Overlay for re-bucketing). Not in scope for this spec.

4. **WACC for B3-affected filers — config knob?** After B3 stops feeding WACC, some users may want to optionally include `DebtLikeClaims` in WACC for ROIC-conservative analysis. A future `WACCInputs.IncludeDebtLikeClaimsInCapitalStructure` flag could enable this opt-in. Not in scope for this spec.

5. **CleaningResult schema migration in `valuation_results` table.** The current `valuation_results` schema persists a denormalized snapshot of cleaning outcomes. Phase 3 may need to add columns for the ledger reference. Decide during Phase 3.

---

## Acceptance criteria for closing DC-1

- [ ] This spec lands at `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` (committed)
- [ ] Phase 0 ships: 4 plug fields on `FinancialData`, SEC parser populates them, property test green
- [ ] Phase 1 ships: `recomputeUmbrellas` shadow-mode warnings analyzed across basket
- [ ] Phase 2 ships: unified `Adjuster` interface; all 14 existing adjusters implement it; ledger + overlay collection working in parallel with existing mutation
- [ ] Phase 3 ships: `CleanedFinancialData` returned by cleaner; bit-for-bit parity test green for basket
- [ ] Phase 4 ships: all 13 consumer sites migrated; `WACCInputs` boundary in place; B3 routing correction live; replay golden bundles regenerated
- [ ] Graham-floor metrics drop the `TotalLiabilities` derivation fallback (DC-1 cited motivation)
- [ ] CLAUDE.md "Common Gotchas" section retires the single-view note; references this spec for the new shape
- [ ] DC-1 tracker archived to `docs/reviewer/archive/`

---

## Change log

| Date | Change |
|------|--------|
| 2026-05-15 | Initial spec. Five-phase architecture (Plug + Component primitive + Adjuster interface + CleanedFinancialData + Consumer migration). Three-view output (AsReported / Restated / InvestedCapital). Single `Adjuster` interface returning `AdjusterOutput` (per GPT-5.5-pro deep-analysis recommendation). B3 routes to `DebtLikeClaims` instead of `TotalDebt` (substantive accuracy correction). `AmountSemantics` on OverlaySpec to prevent double-counting. Four observability layers (parse provenance, merged cleaner audit, dev-only view-reconstruction trace, consolidated narrate phase). Brainstorm decisions recorded in conversation; primary cited file empirics from `liabilities.go:87-88`, `assets.go:69/157/232/308`, `internal/services/datacleaner/` zero-equity-mutation grep. |
| 2026-05-16 | Phase 0 SHIPPED: four plug fields added to `FinancialData`; SEC parser fills them via `computePlugs` at end of `parsePeriodData`; property test + ticker-basket integration test pin the components-sum-to-umbrellas invariant. Zero downstream behavior change — no consumer reads the plug fields in Phase 0. Phase 1 (`recomputeUmbrellas` shadow shim) is now unblocked. |
| 2026-05-19 | Phase 1 SHIPPED: `recomputeUmbrellas` shadow-mode observer added to end of cleaner pipeline (between `createRiskWarningFlags` and `calculateQualityScore`); emits structured WARN logs (`phase: "DC-1-P1-shadow"`) on umbrella/component divergence WITHOUT mutating `FinancialData`. The `reflect.DeepEqual` snapshot test pins the no-mutation invariant; the gopter property test (4 properties × 200 iterations, pinned seed `20260517`) pins "well-formed input → zero WARN". Ticker-basket integration test records divergences as `internal/integration/testdata/recompute-shadow/<TICKER>.json` snapshots for Phase 2's input. Zero downstream behavior change. Phase 2's `Adjuster` interface refactor now unblocked once the shadow-analysis report is filed post-merge. (Live-baseline clamp carriers in `artifacts/tier2-baseline/2026-05-15/`: AMD 2023FY → 2026Q1 and KO 2023FY → 2026Q1, both `reported_TL=0` parser-side dropout, surfaced via `clamp_suspected: true`. Historical MXL 2017FY / EQIX 2013Q1 cases are documented in the Phase 0 closeout but fall outside this baseline's date range.) |
| 2026-05-21 | Phase 2 PR-1 SHIPPED: `Adjuster` interface at `internal/services/datacleaner/adjustments/adjuster.go`; `LedgerEntry`/`OverlaySpec`/`AdjustmentLedger`/`AmountSemantics`/`AIProvenance` entities at `internal/core/entities/adjustment_ledger.go`; `AdjustmentLedger` and `Overlays` fields appended to `FinancialData`; orchestrator scaffolding shim at `service.go::applyActiveAdjustments` emits `LedgerEntry` records from legacy `entities.Adjustment` results (three contiguous shim branches — assets / liabilities / earnings — each deleted as its category migrates in PR-2/3/4). Zero adjuster migration in PR-1. Zero downstream behavior change (dual-write invariant). New property test `TestOrchestrator_LedgerOrdering` pins the asset → liability → earnings ledger partition. `recomputeUmbrellas` WARN line additively renders a new `recent_adjusters` `[]string` field (last 5 AdjusterIDs from `fd.AdjustmentLedger`) per Q1 resolution 2026-05-21; load-bearing `TestRecomputeUmbrellas_NoMutation` invariant preserved. |
| 2026-05-22 (PR-3) | Phase 2 PR-3 SHIPPED: C1/C2/C3/C5/C6 (Restaters; C6 has EquityOffset=0 special case for interest reclassification) + C4/C7 (FlagEmitters; C4 is plan-vs-code disagreement — plan said Restater but legacy code has no mutation, ships as FlagEmitter) migrated to Adjuster interface. Earnings-side legacy shim deleted. Shim helpers preserved for PR-4. SchemaVersion stays at 8. Shadow-snapshot byte-identity preserved (predicted-zero per plan §4 row C). Branch `dc1-phase-2-pr-3` tip `4af3c33` awaiting final merge after PR-4. |
| 2026-05-22 (PR-2) | Phase 2 PR-2 SHIPPED: A1 (OverlayEmitter — `OverlaySpec{Field:"TotalAssets", Operation:"subtract"}`), A2/A4 (Restaters — `LedgerEntry{Component, DeltaAmount<0, EquityOffset=DeltaAmount}`), A5 (Restater + TaxShieldDTA derived from `writedown × working.EffectiveTaxRate` when rate > 0), and the 2 flag-only reviews (`RDCapitalizationReview`, `CapitalizedSoftwareReview`) — FlagEmitter convention: `Fired:false` LedgerEntry with non-empty `AdjusterOutput.Flags` signals the review fired — all 6 Category A adjusters migrated to the `Adjuster` interface. Canonical pattern locked: mutation-FREE `Apply*` on `AssetAdjuster`; dispatcher `ProcessAssetAdjustments` owns dual-write (capture → Apply → translate → mutate → drain natives). Asset-side legacy shim branch in `service.go::applyActiveAdjustments` deleted. `CurrentSchemaVersions["FinancialData"]` bumped 7→8 atomic with Task 2.1 (per `feedback_schema_version_atomic_bump` MEMORY rule — first populating PR). A-FY-NULL Task 2.7 read-only tracker filed at `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` (HIGH-confidence NOT-A-BUG; rooted in SEC XBRL FY-annual vs Qx-quarterly Revenue magnitude convention causing quarterly-tuned `InventoryTurnover<3.0` threshold to dropout on FY; heuristic fix punted to a Phase 4+ "FY-aware annualization" subtask). Q2 (A2 TaxShieldDTA) DEFERRED to Phase 3 — pinned by `TestA2IntangibleAdjuster_Adjuster_Interface_Contract/fired_path_TaxShieldDTA_stays_zero_per_Q2_deferral`. Q4 (AIProvenance hash fields) DEFERRED to Phase 3. Load-bearing invariants stayed GREEN through all 7 PR-2 commits: `TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, `TestDataCleanerRecompute_ShadowMode_TickerBasket` + shadow-snapshot byte-identity (`git diff --quiet internal/integration/testdata/recompute-shadow/` exit 0). |
