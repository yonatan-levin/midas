# DC-1 Phase 5 follow-up — `entities.Adjustment.Percentage` projection decision (spec addendum)

**Status:** ARCH DECISION — APPROVED (Path (a), SkipMetrics-based)
**Date:** 2026-05-31
**Author:** ARCH
**Branch:** `dc1-phase-5-followup`
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-phase-5-spec.md](./datacleaner-component-primitive-and-parallel-views-phase-5-spec.md) §3.4
**Phase 5 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-5-closeout.md](../implementations/datacleaner-component-primitive-and-parallel-views-phase-5-closeout.md) §6.1

---

## 1. Context

DC-1 Phase 5 PARTIAL merged to master 2026-05-31 (merge `e816fcc`). Phase 5 PARTIAL shipped P5-C1 (DDM EV-bridge `+DebtLikeClaims` correction, `CalculationVersion` 4.3 → 4.4), P5-C2 (DDM consumer migration to `Restated()` view), P5-C3 **SCOPED** (orchestrator firing-signal migrated to `nativeFired(...)` filtering `LedgerEntry.Fired==true`), and P5-C5 PARTIAL (`cleaneddata.Raw()` deletion + concurrency-contract godoc).

**Deferred to this follow-up session (this PR's scope):**

1. **P5-C3-full Adjustments-projection** — rewrite `result.Adjustments` aggregation in `internal/services/datacleaner/service.go::applyActiveAdjustments` so the aggregated `[]entities.Adjustment` audit-trail records are derived from `data.AdjustmentLedger` + `data.Overlays` via a single shared helper `adjustmentsFromLedger(ledger, overlays, perRuleMeta) []entities.Adjustment` — **NOT** through the per-rule `*AdjusterOutputToLegacyResult` translator chain.
2. **P5-C4 translator/struct/dormant-fallback deletion** — gated on (1).

`CleaningResult.Adjustments` flows out through `entities.ValuationResult.CleaningAdjustments` (a JSON-tagged public field at `internal/core/entities/valuation.go:64`), so the projection's output is part of the public REST API contract. The behavior-preservation gate for P5-C3-full is a basket-parity golden: pre-rewrite vs. post-rewrite `result.Adjustments` byte-identical (excluding non-deterministic `ID` + `Timestamp` fields) for the 10-ticker basket.

---

## 2. The decision

Today, 6 of the 16 per-rule translators (the Restater family — A2, C2, C3, C5, C6, C4-FlagEmitter) compute `entities.Adjustment.Percentage` from pre-state captured at the dispatcher BEFORE `Apply*` runs (e.g., `originalIntangibles := data.OtherIntangibles` at `assets.go:1435`, `originalRevenue := data.Revenue` at `earnings.go:1401`). Today's `entities.LedgerEntry` struct (`internal/core/entities/adjustment_ledger.go`) does NOT preserve this pre-mutation snapshot.

If `adjustmentsFromLedger(ledger, overlays, perRuleMeta)` is built from the ledger alone, the Restater family's `Adjustment.Percentage` is forced to one of two outcomes:

- **Path (a) — Preserve `Percentage` byte-for-byte.** Capture the necessary pre-state into the `LedgerEntry` (the existing `SkipMetrics map[string]float64` field is the natural carrier; it has the right type and is already serialized in the audit-trail JSON). The projection reads it back. Basket golden trivially passes.
- **Path (b) — Accept `Percentage = 0` in the projection.** No adjuster touches. LOSSY: `entities.ValuationResult.CleaningAdjustments[*].Percentage` silently drops to `0` for every Restater family entry. Public API contract degradation that needs explicit ARCH approval. Basket golden needs to be re-baselined with `Percentage = 0`.

---

## 3. Audit of pre-state-capturing translators

The 16 per-rule translators (asset-side `a*`, liability-side `b*`, earnings-side `c*`) audited by role:

| Rule | File:line of translator | Role | Reads pre-state? | Pre-state value(s) | `Percentage` formula |
|---|---|---|---|---|---|
| **A1** Goodwill exclusion | `assets.go:1639` | OverlayEmitter | **No** (field omitted) | n/a | `Percentage = 0` (field unset → zero value) |
| **A2** Intangible writedown | `assets.go:1700` | Restater | **Yes** | `originalIntangibles` (captured `assets.go:1435`) | `(writedownAmount / originalIntangibles) * 100` if `originalIntangibles > 0` (`assets.go:1711-1714`) |
| **A4** DTA valuation allowance | `assets.go:1774` | Restater | (`originalDTA` threaded but unused; `_ = originalDTA` at line 1775) | n/a | **Constant 50.0** (`assets.go:1795`) |
| **A5** Inventory writedown | `assets.go:1847` | Restater | (`originalInventory` threaded but unused; `_ = originalInventory` at line 1848) | n/a | **Constant 40.0** (`assets.go:1868`) |
| **A-RD** R&D-capitalization review | `assets.go:1916` | FlagEmitter | **No** | n/a | No `entities.Adjustment` emitted (review only, `[]entities.Adjustment{}`) |
| **A-CapSoftware** Cap-software review | `assets.go:1949` | FlagEmitter | **No** | n/a | No `entities.Adjustment` emitted |
| **B1** Operating lease capitalization | `liabilities.go:609` | OverlayEmitter | **No** (field omitted) | n/a | `Percentage = 0` (field unset) |
| **B2** Pension under-funding | `liabilities.go:954` | OverlayEmitter | **No** (field omitted) | n/a | `Percentage = 0` (field unset) |
| **B3** Contingent liabilities | `liabilities.go:1175` | OverlayEmitter | **No** (field omitted) | n/a | `Percentage = 0` (field unset) |
| **C1** Restructuring add-back | `earnings.go:218` | Restater | (`originalRestructuring` threaded but unused; `_ = originalRestructuring` at line 219; explicit comment: "Percentage is not strictly needed for downstream consumers (no regression test reads it for C1); leave as 0") | n/a | `Percentage = 0` (field unset by design — `earnings.go:247-252`) |
| **C2** Asset-sale gains subtraction | `earnings.go:393` | Restater | **Yes** | `originalRevenue` (captured `earnings.go:1401`) | `(gains / originalRevenue) * 100` if `originalRevenue > 0` (`earnings.go:403-404`) |
| **C3** Litigation settlements add-back | `earnings.go:553` | Restater | **Yes** | `originalRevenue` (captured `earnings.go:1422`) | `(settlements / originalRevenue) * 100` if `originalRevenue > 0` (`earnings.go:560-561`) |
| **C4** Stock-based-compensation review | `earnings.go:1062` | FlagEmitter (Applied:true) | **Yes** | `originalRevenue` (captured at dispatcher) + `sbcAmount` from `LedgerEntry.SkipMetrics["sbc_amount"]` (ALREADY on the ledger) | `(sbcAmount / originalRevenue) * 100` if `originalRevenue > 0` (`earnings.go:1078-1079`) |
| **C5** Derivative gains/losses | `earnings.go:712` | Restater | **Yes** | `originalRevenue` (captured at dispatcher) | `(reportingAmount / originalRevenue) * 100` if `originalRevenue > 0` (`earnings.go:723-724`) |
| **C6** Capitalized-interest reclassification | `earnings.go:873` | Restater | **Yes** | `originalRevenue` (captured at dispatcher; `originalCapitalizedInterest` also threaded but unused — `_ = originalCapitalizedInterest` at line 874) | `(capInterest / originalRevenue) * 100` if `originalRevenue > 0` (`earnings.go:881-884`) |
| **C7** Working-capital window-dressing | `earnings.go:1275` | FlagEmitter (Applied:true, empty Adjustments) | **No** | n/a | No `entities.Adjustment` emitted (`earnings.go:1292`: `[]entities.Adjustment{}`) |

### 3.1 Summary

- **6 of 16 translators read pre-state** to compute a non-trivial `Adjustment.Percentage`: **A2, C2, C3, C4, C5, C6**.
- **2 translators set `Percentage` to a CONSTANT** (A4 = 50.0, A5 = 40.0). The threaded `original*` argument is unused (`_ = original*`); these are trivial to migrate (the constant is rule-intrinsic, encoded in the per-AdjusterID metadata table).
- **1 translator (C1) deliberately leaves `Percentage = 0`** despite threading `originalRestructuring`. The translator's inline comment is explicit: "Percentage is not strictly needed for downstream consumers (no regression test reads it for C1); leave as 0 for now and rely on the Reasoning string for the formatted ratio." Migrate as `Percentage = 0`.
- **7 translators emit NO `Percentage`** (the field is omitted from the `entities.Adjustment` literal, so Go's zero value `0.0` ships): **A1, A-RD, A-CapSoftware, B1, B2, B3, C7** (C7 emits no `entities.Adjustment` at all).
- **One pre-state value (`originalRevenue`) covers 5 of the 6 pre-state-reading rules** (C2, C3, C4, C5, C6) — it is the single shared denominator across the C-rule family. Capturing it ONCE per category dispatcher loop (already done — `earnings.go:1401`, `:1422`) means the per-LedgerEntry capture is only **one** unique pre-state value to encode: `originalRevenue`. A2 needs a different field: `originalIntangibles`.

So the LedgerEntry-side capture surface is **TWO** distinct pre-state values across the 16 adjusters — `originalIntangibles` (A2 only) and `originalRevenue` (C2/C3/C4/C5/C6). The A4/A5 constants and the FlagEmitter zero-Percentages need NO pre-state at all.

---

## 4. Path (a) mechanism (the chosen path)

### 4.1 Carrier

Use the existing `entities.LedgerEntry.SkipMetrics map[string]float64` field. Rationale:

- The field is **`map[string]float64`** (not `string` — the prompt's premise of "string-encoded float" was based on an outdated assumption; the actual type fits the use case natively).
- The field is already serialized in the audit-trail JSON (`json:"skip_metrics,omitempty"`).
- No `SchemaVersion["FinancialData"]` bump required (stays 9). A new typed field on `LedgerEntry` (e.g., `PreState map[string]float64`) WOULD require a schema bump.
- The DC-1 Phase 5 closeout §6.1 explicitly recommended this approach.

Today the field is populated on `Fired:false` skip paths as a diagnostic ("why didn't this rule fire?"). It is currently EMPTY on `Fired:true` paths. We extend the convention: **`SkipMetrics` is the carrier for any per-ticker scalar an adjuster wants to preserve on its LedgerEntry, regardless of `Fired` value.** This is a behaviorally additive change — no consumer reads `SkipMetrics` on `Fired:true` entries today (grep `SkipMetrics\[` in `internal/services/datacleaner/` — only firing-signal predicates `len(SkipMetrics) > 0` in the FlagEmitter convention, plus C4's `SkipMetrics["sbc_amount"]` read in its own translator).

The minor godoc cosmetic — the `LedgerEntry.SkipMetrics` field comment currently says "Populated when Fired=false" — gets relaxed to "Populated when the adjuster wants to attach scalar context (skip path diagnostics on Fired=false, or pre-state inputs to downstream projections on Fired=true)."

### 4.2 Key-naming convention

Snake-case `original_<Field>` where `<Field>` is the canonical entity field name (the same name carried in `LedgerEntry.Component` on Restaters). Concretely:

| AdjusterID | Key | Captured value |
|---|---|---|
| `a2_intangible_writedown` | `original_OtherIntangibles` | `working.OtherIntangibles` at adjuster entry |
| `c2_asset_sale_gains` | `original_Revenue` | `working.Revenue` at adjuster entry |
| `c3_litigation_settlements` | `original_Revenue` | `working.Revenue` at adjuster entry |
| `c4_stock_compensation` | `original_Revenue` | `working.Revenue` at adjuster entry (also: `sbc_amount` is ALREADY on `SkipMetrics`; keep it) |
| `c5_derivative_gains_losses` | `original_Revenue` | `working.Revenue` at adjuster entry |
| `c6_capitalized_interest` | `original_Revenue` | `working.Revenue` at adjuster entry |

C4 needs BOTH `sbc_amount` (already captured) AND `original_Revenue` (new). A2 needs only `original_OtherIntangibles` (`writedownAmount` itself is on `LedgerEntry.DeltaAmount` — no need to duplicate; the projection computes `writedownAmount = -DeltaAmount`).

### 4.3 Capture site

Pre-state capture lives **INSIDE the `Apply*` method** that emits the LedgerEntry, NOT at the dispatcher. Rationale:

- The `Apply*` method already reads the entity field (`working.OtherIntangibles`, `working.Revenue`) early in its body before any restate computation. Capturing into the LedgerEntry it is constructing is a one-line, locally-visible change per adjuster.
- The dispatcher capture pattern (`originalIntangibles := data.OtherIntangibles` at `assets.go:1435`) was a workaround for the legacy translator's pre-mutation-snapshot need. Once translators are deleted (P5-C4), this dispatcher-side capture has no reader; moving the capture into `Apply*` lets P5-C4 delete the dispatcher-side `original*` variables alongside the translator calls.
- The `LedgerEntry` is the single canonical audit record; co-locating its full payload at its emission site is the cleaner architecture.

Two `Apply*` methods need a `LedgerEntry.SkipMetrics` write on `Fired:true`:

1. **`ApplyA2Intangible` (file: `internal/services/datacleaner/adjustments/assets.go`)** — on the fired branch, write `SkipMetrics: map[string]float64{"original_OtherIntangibles": originalIntangibles}` onto the fired LedgerEntry (where `originalIntangibles` is already a local variable inside the method).

2. **`ApplyC2AssetSaleGains` / `ApplyC3Litigation` / `ApplyC4StockCompensation` / `ApplyC5DerivativeGainsLosses` / `ApplyC6CapitalizedInterest`** — on the fired branch, write `SkipMetrics: map[string]float64{"original_Revenue": working.Revenue}` onto the fired LedgerEntry. `ApplyC4StockCompensation` ALREADY writes `SkipMetrics{sbc_amount, sbc_ratio}` on the fired path — extend that map with `original_Revenue`.

### 4.4 Projection helper signature

```go
// adjustmentsFromLedger derives the public Adjustment audit-trail from the
// native LedgerEntry + OverlaySpec slices on cleaned FinancialData. Replaces
// the per-rule *AdjusterOutputToLegacyResult translator chain.
//
// The perRuleMeta map carries the static-per-rule context (Category, Type,
// FromAccount, ToAccount, ConstantPercentage if any) that each old translator
// hard-coded. Variable Percentages are computed from LedgerEntry.SkipMetrics
// pre-state per §4.5 below.
func adjustmentsFromLedger(
    ledger entities.AdjustmentLedger,
    overlays []entities.OverlaySpec,
    perRuleMeta map[string]ruleMeta,
) []entities.Adjustment
```

Lives at `internal/services/datacleaner/adjustment_projection.go` (new file).

### 4.5 Projection algorithm (`ruleMeta`-driven)

```go
type ruleMeta struct {
    Category    entities.RuleCategory
    Type        entities.AdjustmentType
    FromAccount string
    ToAccount   string
    // Percentage handling — exactly one of these fires per rule:
    PercentageMode    percentageMode  // "absent" | "constant" | "from_pre_state"
    ConstantPct       float64         // populated when PercentageMode == constant (A4=50, A5=40)
    PreStateKey       string          // populated when PercentageMode == from_pre_state (e.g. "original_Revenue")
    AmountSource      amountSource    // "ledger_delta_abs" | "overlay_amount" | "skipmetrics_sbc_amount"
}
```

For each fired entry in the ledger (`entry.Fired == true` OR — for FlagEmitters like C4 — the entry's AdjusterID maps to a `ruleMeta` with `AmountSource = skipmetrics_sbc_amount` AND the entry carries the firing-signal flags from the partnered overlay/flag stream):

1. Look up `meta := perRuleMeta[entry.AdjusterID]`. Unknown AdjusterID → skip (defensive; should never happen in practice; emit a WARN log).
2. Resolve `amount`:
   - `ledger_delta_abs` → `math.Abs(entry.DeltaAmount)`
   - `overlay_amount` → find the OverlaySpec with `OverlayID == entry.AdjusterID` (1:1 with the entry) and read `.Amount`
   - `skipmetrics_sbc_amount` → `entry.SkipMetrics["sbc_amount"]`
3. Resolve `pct`:
   - `absent` → `0` (Go zero value, omitted from JSON via `omitempty`)
   - `constant` → `meta.ConstantPct`
   - `from_pre_state` → `(amount / entry.SkipMetrics[meta.PreStateKey]) * 100` guarded by `> 0` denominator check (matches the legacy translator guards)
4. Construct `entities.Adjustment{...}` from `meta` + `entry.RuleID` + `entry.Reasoning` + `entry.Timestamp` (use entry's timestamp, not `time.Now()` — the legacy `time.Now()` is the non-deterministic bit excluded from the basket-parity test; use the entry's deterministic capture timestamp instead for cleaner replay parity).

### 4.6 The metadata table (extracted from translator audit)

| AdjusterID | Category | Type | FromAccount | ToAccount | PercentageMode | ConstantPct / PreStateKey | AmountSource |
|---|---|---|---|---|---|---|---|
| `a1_goodwill_exclusion` | `AssetQuality` | `Exclude` | `Goodwill` | (empty) | absent | — | `overlay_amount` |
| `a2_intangible_writedown` | `AssetQuality` | `Writedown` | `IntangibleAssets` | `IntangibleWritedown` | from_pre_state | `original_OtherIntangibles` | `ledger_delta_abs` |
| `a4_dta_valuation_allowance` | `AssetQuality` | `AdjustmentTypeValuationAllowance` | `DeferredTaxAssets` | `ValuationAllowance` | constant | `50.0` | `ledger_delta_abs` |
| `a5_inventory_writedown` | `AssetQuality` | `Writedown` | `Inventory` | `InventoryWritedown` | constant | `40.0` | `ledger_delta_abs` |
| `b1_operating_lease_capitalization` | `LiabilityCompleteness` | `TreatAsDebt` | `OperatingLeaseCommitments` | `InterestBearingDebt` | absent | — | `overlay_amount` |
| `b2_pension_underfunding` | `LiabilityCompleteness` | `TreatAsDebt` | `PensionUnderfunding` | `InterestBearingDebt` | absent | — | `overlay_amount` |
| `b3_contingent_liability` | `LiabilityCompleteness` | `ProbabilityWeighted` | `ContingentLiabilities` | `EstimatedLiabilities` | absent | — | `overlay_amount` |
| `c1_restructuring_charges` | `EarningsNormalization` | `Exclude` | `RestructuringCharges` | `NormalizedOperatingIncome` | absent | — | `ledger_delta_abs` |
| `c2_asset_sale_gains` | `EarningsNormalization` | `Exclude` | `AssetSaleGains` | `NormalizedOperatingIncome` | from_pre_state | `original_Revenue` | `ledger_delta_abs` |
| `c3_litigation_settlements` | `EarningsNormalization` | `Exclude` | `LitigationSettlements` | `NormalizedOperatingIncome` | from_pre_state | `original_Revenue` | `ledger_delta_abs` |
| `c4_stock_compensation` | `EarningsNormalization` | `Reclassify` | `StockBasedCompensation` | `OperatingExpenses` | from_pre_state | `original_Revenue` | `skipmetrics_sbc_amount` |
| `c5_derivative_gains_losses` | `EarningsNormalization` | `Exclude` | `DerivativeGainsLosses` | `NormalizedOperatingIncome` | from_pre_state | `original_Revenue` | `ledger_delta_abs` |
| `c6_capitalized_interest` | `EarningsNormalization` | `Reclassify` | `CapitalizedInterest` | `InterestExpense` | from_pre_state | `original_Revenue` | `ledger_delta_abs` |
| `c7_working_capital_window_dressing` | (no entry — `Adjustment` not emitted) | — | — | — | — | — | — |
| `a_rd_capitalization_review` | (no entry — `Adjustment` not emitted) | — | — | — | — | — | — |
| `a_capitalized_software_review` | (no entry — `Adjustment` not emitted) | — | — | — | — | — | — |

C7, A-RD, A-CapSoftware are flag-only — they appear in `perRuleMeta` as a "skip emission" sentinel (e.g., `Type = ""`), and the projection short-circuits on that sentinel.

### 4.7 `Reasoning` string parity

The legacy translators format `Reasoning` strings via inline `fmt.Sprintf` with the magnitude (e.g., A2's `"intangible_writedown: Applied %.0f writedown to indefinite-lived intangibles from asset base"`). Today the FIRED `LedgerEntry.Reasoning` ALREADY carries the same magnitude-formatted string from `Apply*`. The projection reads `entry.Reasoning` directly — no per-rule string template needed. (Verified across all six pre-state-reading rules: the LedgerEntry's Reasoning string carries either the full legacy formatting or the formatting-equivalent variant.)

If the basket-parity golden uncovers a per-rule string drift, the implementer plan must either (a) normalize the `Apply*`-emitted `Reasoning` to match the legacy translator output exactly, or (b) carry a per-rule `ReasoningTemplate` field on `ruleMeta` that re-formats with `amount` from §4.5. Prefer (a) — it converges the audit record to a single source of truth.

---

## 5. Recommendation + final decision

**DECISION: Path (a) — preserve `Adjustment.Percentage` byte-for-byte via `LedgerEntry.SkipMetrics["original_*"]` capture.**

Rationale, in order of weight:

1. **Public API contract.** `entities.ValuationResult.CleaningAdjustments` is a JSON-tagged public field (`internal/core/entities/valuation.go:64`). The `Percentage` sub-field is consumed by REST API clients and (per the closeout) potentially persisted in `valuation_results` audit snapshots. A silent transition from `25.0` to `0.0` violates the DC-1 invariant that all refactor phases are behavior-preserving from the API perspective.
2. **Cost is small.** Only **2 pre-state values** need carrier wiring across the entire 16-adjuster surface (`original_OtherIntangibles` on A2; `original_Revenue` on C2/C3/C4/C5/C6 — already locally available inside each `Apply*` method). No `SchemaVersion` bump (uses existing `SkipMetrics map[string]float64`). No new entity field. The dispatcher-side `original*` capture lines (5 of them — `assets.go:1435/1479/1522`, `earnings.go:1365/1401/1422` and similar) become redundant and get DELETED in P5-C4 alongside the translators, so net code churn is small and net to the codebase is REDUCED.
3. **Basket-parity golden passes trivially.** The P5-C3-full acceptance gate (basket-parity for `result.Adjustments` content excluding `ID`/`Timestamp`) was designed assuming behavior preservation. Path (a) keeps the gate green without re-baselining; path (b) requires a deliberate golden re-baseline at every Percentage-emitting Restater.
4. **DC-1 user-memory rule.** "Prefer KEEP behavior-preservation unless cost is prohibitive." The audit proves cost is NOT prohibitive (5 `Apply*` methods, 1-2 lines each).
5. **Closeout §6.1's recommendation stands.** The closeout reviewer's analysis already correctly identified (a) as preferred. The audit performed here confirms that view with concrete numbers.

**Path (b) rejected.** The "zero adjuster touches" cost-saving is not worth a silent public API contract degradation, and the basket-parity test would need golden regeneration (the harder review surface).

ARCH-signed: ARCH, 2026-05-31.

---

## 6. BACKEND build instructions

### 6.1 No `LedgerEntry` shape change

`entities.LedgerEntry.SkipMetrics map[string]float64` already exists. The only required cosmetic change is **godoc relaxation** at `internal/core/entities/adjustment_ledger.go:40-42`:

- Before: `// Populated when Fired=false. … SkipMetrics map[string]float64`
- After: `// SkipMetrics may be populated regardless of Fired — on skip paths it carries diagnostic ratios ("why did this rule skip?"); on fire paths it may carry pre-state captures consumed by the LedgerEntry → Adjustment projection (see internal/services/datacleaner/adjustment_projection.go).`

No `CurrentSchemaVersions["FinancialData"]` bump. Stays at 9.

### 6.2 Five `Apply*` methods need a one-line `SkipMetrics` write on the fired branch

For each of `ApplyA2Intangible`, `ApplyC2AssetSaleGains`, `ApplyC3Litigation`, `ApplyC4StockCompensation`, `ApplyC5DerivativeGainsLosses`, `ApplyC6CapitalizedInterest`:

- **Identify the FIRED `entities.LedgerEntry` literal** in the method's `return AdjusterOutput{...}, nil` block.
- **Add (or extend) `SkipMetrics`** on that literal with the appropriate key from §4.2.
- For C4 specifically — the FIRED LedgerEntry already has `SkipMetrics: map[string]float64{"sbc_amount": ..., "sbc_ratio": ...}`. Extend the literal with one extra entry `"original_Revenue": working.Revenue`.

Concrete sketch (using A2 as the canonical example — same pattern applies to all five):

```go
// Fired branch in ApplyA2Intangible (internal/services/datacleaner/adjustments/assets.go)
return AdjusterOutput{
    LedgerEntries: []entities.LedgerEntry{{
        Timestamp:    now,
        AdjusterID:   adjusterIDA2IntangibleWritedown,
        RuleID:       rule.ID,
        Fired:        true,
        Reasoning:    fmt.Sprintf("intangible_writedown: Applied %.0f ...", writedownAmount),
        Component:    "OtherIntangibles",
        DeltaAmount:  -writedownAmount,
        EquityOffset: -writedownAmount,
        TaxShieldDTA: taxShield, // Q2 SHIPPED Phase 3
        SkipMetrics:  map[string]float64{"original_OtherIntangibles": originalIntangibles}, // NEW
    }},
}, nil
```

### 6.3 New file: `internal/services/datacleaner/adjustment_projection.go`

Carries:

1. The `perRuleAdjustmentMeta map[string]ruleMeta` (16-entry constant table, transcribed from §4.6).
2. The `adjustmentsFromLedger` projection helper (§4.4 / §4.5).
3. A package-private `ruleMeta` struct + enums (`percentageMode`, `amountSource`).

### 6.4 Orchestrator rewrite in `internal/services/datacleaner/service.go::applyActiveAdjustments`

Replace the three blocks of `if assetResult.Applied { allAdjustments = append(allAdjustments, assetResult.Adjustments...); allFlags = append(allFlags, assetResult.Flags...) }` with a single post-dispatcher call:

```go
allAdjustments := adjustmentsFromLedger(data.AdjustmentLedger, data.Overlays, perRuleAdjustmentMeta)
allFlags := /* native flag drain — already migrated in P5-C3-scoped */
```

The `totalRulesApplied` counter already migrated to `nativeFired(...)` in P5-C3-scoped (`internal/services/datacleaner/firing_signal.go`); no further change there.

### 6.5 P5-C4 deletion (in the same commit or next, gated on §6.4 acceptance)

After §6.4 lands and the basket-parity golden is green:

1. Delete the 16 `*AdjusterOutputToLegacyResult` translators (per §3 audit, locations enumerated).
2. Delete the dispatcher-side `original*` capture variables (`assets.go:1435/1479/1522`, `earnings.go:1365/1400-1401/1422/1455/1485/1518`). The pre-state is now read from the LedgerEntry by the projection — the dispatcher snapshot is obsolete.
3. Delete the per-category result structs `AssetAdjustmentResult` (`assets.go:2092`), `LiabilityAdjustmentResult` (`liabilities.go:81`), `EarningsAdjustmentResult` (`earnings.go:53`).
4. Delete the dormant `earnings.go` legacy-fallback helpers (`ProcessRestructuringChargesAdjustment`, `ProcessAssetSaleGainsAdjustment`, `ProcessLitigationSettlementsAdjustment`, capitalized-interest fallback) — these are reachable only on a dead `if err != nil` branch (per parent spec §3.5).
5. Delete the dead `entities.{Asset,Liability}AdjustmentResult` duplicates at `internal/core/entities/data_cleaning.go:468-485` (per parent spec §4.5 — verify-then-delete with a final grep at implementation time).
6. Re-point the ~20 `adjustments/*_test.go` assertions that read `result.Adjustments[0].Amount`/`.FromAccount`/`.ToAccount`/`.Type`/`.Percentage` onto the new slim native carrier OR onto an end-to-end basket assertion through `applyActiveAdjustments`.

---

## 7. Test gates

### 7.1 New basket-parity golden test (BLOCKING for P5-C3-full)

`TestApplyActiveAdjustments_AdjustmentsProjection_BasketParity` — runs the 10-ticker basket (`artifacts/tier2-baseline/`) through the cleaner pipeline twice:

- **Snapshot A:** pre-rewrite — captured in the FIRST commit of this PR with the old translator-driven aggregation still wired up, persisted as a golden JSON under `internal/services/datacleaner/testdata/adjustments_projection_basket_golden.json` keyed by ticker.
- **Snapshot B:** post-rewrite — produced by the projection helper in the SECOND commit.

The test loads Snapshot A and asserts byte-identity with Snapshot B for the `[]entities.Adjustment` slice content (`RuleID`, `Category`, `Type`, `Amount`, `FromAccount`, `ToAccount`, `Percentage`, `Reasoning`, `Applied`) PER ticker. Excludes `ID` (non-deterministic — embeds `time.Now().UnixNano()`) and `Timestamp` (replaced by the deterministic LedgerEntry timestamp; new test, can be re-baselined on first capture).

Acceptance bar: 0 ticker drifts. Any per-ticker drift = REVERT the projection change, audit the pre-state-capture coverage, fix, re-run. NEVER update the golden to make this pass.

### 7.2 Existing pins that MUST stay GREEN at every commit

- `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC bit-for-bit) — Phase 5 P5-C2 invariant, untouched by P5-C3-full/P5-C4.
- `TestDDM_ConsumerPath_RestatedViewParity` — DDM view-equals-entity property pin.
- `TestRecomputeUmbrellas_NoMutation` — shadow shim immutability.
- `TestOrchestrator_LedgerOrdering` — asset → liability → earnings partition.
- `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) — per-ticker AdjusterID set per category.
- `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` — AMD $9.679B / KO $60.912B Restated reconstruction.
- `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_*` — HIGH-1 reducer pin.
- `TestCleanedFinancialData_Restated_C6EquityOffsetZero` — C6 capitalized-interest equity-offset-zero pin.
- `TestRevenueMultiple_SubtractsDebtLikeClaims` + `…_Forward_…` — Phase 4 correction.
- `TestApplyActiveAdjustments_FiringSignalParity_*` — P5-C3-scoped firing-signal pins.
- Shadow snapshots byte-identical (`git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0).
- Full `go test ./... -count=1` EXIT=0 at every commit.

### 7.3 New defensive pins to add

1. `TestAdjustmentsProjection_HandlesUnknownAdjusterID` — projection emits 0 records (with a WARN) when an unknown AdjusterID appears in the ledger; does NOT panic.
2. `TestAdjustmentsProjection_FromPreStateMode_ZeroDenominatorYieldsZeroPct` — when `original_Revenue == 0`, `Percentage = 0` (matches legacy `if originalRevenue > 0` guard); no `Inf`/`NaN` leak.
3. `TestAdjustmentsProjection_ConstantPctMode_A4_A5` — A4 emits `Percentage = 50.0`, A5 emits `Percentage = 40.0`, regardless of `LedgerEntry.SkipMetrics` content.
4. `TestApplyA2Intangible_LedgerEntry_CarriesOriginalIntangibles` — the fired LedgerEntry's `SkipMetrics["original_OtherIntangibles"]` equals `working.OtherIntangibles` at adjuster entry. One pin per pre-state-capturing rule (A2, C2, C3, C4, C5, C6) — 6 pins total, table-driven if helpful.

These pins prevent silent regressions during P5-C4's mechanical deletion sweep.

---

## 8. Open questions for HUMAN

None blocking. The audit was conclusive; the closeout's recommended path is feasible and inexpensive. BACKEND can dispatch on this addendum.

One **non-blocking** observation worth flagging for HUMAN awareness: the projection's choice in §4.5 step 4 to use `LedgerEntry.Timestamp` (deterministic) instead of `time.Now()` (non-deterministic) on the emitted `Adjustment.Timestamp` is a small intentional behavioral improvement (deterministic replay; cleaner basket-parity test). If HUMAN prefers strict legacy parity on this field too, the implementer plan can wire `time.Now()` at projection time — but it bloats the golden's excluded-field list. Recommend keeping the deterministic timestamp.

---

## 9. Change log

| Date | Change |
|---|---|
| 2026-05-31 | ARCH addendum filed: Path (a) approved (preserve `Adjustment.Percentage` byte-for-byte via `LedgerEntry.SkipMetrics["original_*"]`); audit-table + projection-helper sketch + 16-row metadata table + 7-section test-gate spec laid out for BACKEND dispatch. |
