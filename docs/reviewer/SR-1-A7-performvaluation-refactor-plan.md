# SR-1 A7 — `performValuation` Refactor Plan (behavior-preserving)

**MODE:** REFACTOR (Phase 1 — ARCH planning) · **ROLE:** ARCH
**GitHub issue:** #24 · **Branch:** `refactor/sr1-a7-performvaluation`
**Worktree:** `midas-sr1-a7` (do ALL work here) · **Date:** 2026-06-27

> This is a **mechanical, zero-behavior-change** refactor of
> `internal/services/valuation/service.go::performValuation`. The math read-order
> MUST NOT move; only plumbing changes. Every load-bearing test stays GREEN with
> assertions UNCHANGED. The implementer follows this plan step-by-step.

---

## 0. Summary

`performValuation` (service.go **685–1864**, ~1,180 lines) is the largest function
in the repo. It does six things in a fixed order: validate → growth estimate →
profile/guidance resolution → params resolution → WACC → DCF → assemble result.
Two refactors, in two reviewable chunks:

1. **`stageEmitter` extraction (Commit 1)** — collapse the repeated observability
   rituals (`if s.calcEmitter != nil { s.calcEmitter.Emit(...) }` and the
   `narrate.From(ctx).Emit(...)` + `if b := artifact.From(ctx); b != nil { b.Snapshot(...) }`
   pairs) into a small ctx+ticker-bound helper with one method per stage. Emit-only,
   no math touched.
2. **Phase split (Commits 2–N)** — break the body into named phases
   (`resolveValuationInputs` / `computeWACC` / `runDCF` / `assembleResult`) threading
   shared locals through a single private carrier struct. Read-order preserved exactly.

There is **precedent**: `classifyTickerIndustry` (service.go 1881+) was already
extracted verbatim from this same function under the SR-1 A7 banner. This plan
continues that pattern.

---

## 1. Current State — actual structure of `performValuation`

Signature (UNCHANGED contract — see §4):

```go
func (s *Service) performValuation(
    ctx context.Context,
    historicalData *entities.HistoricalFinancialData,
    marketData *entities.MarketData,
    macroData *entities.MacroData,
    opts *ValuationOptions,
    cleaned *cleaneddata.CleanedFinancialData,
) (*entities.ValuationResult, error)
```

### 1.1 Logical stages, in order (line ranges)

| # | Stage | Lines | What it does |
|---|-------|-------|--------------|
| A | **Input validation** | 694–705 | `HasMinimumData(1)`, `marketData.IsComplete()`, `macroData.IsComplete()` → `ErrInsufficientData` |
| B | **Historical growth + analyst + sustainable growth** | 707–743 | `CalculateAverageGrowthRate(5)` (with default fallback); `getAnalystEstimates`; ROIC via `restatedViewOr` → `sustainableGrowth` |
| C | **Estimator selection + growth estimate** | 745–806 | `normalizeOverrides`; per-request estimator (`overridesAffectEstimator` → `ValidateEstimatorConfig` → `growthEstimatorFor`); `EstimateGrowthRates`; **EMIT growth.estimated** (narrate+artifact, 786–806) |
| D | **Latest period + tangible value** | 808–818 | `GetLatestPeriod`; `calculateTangibleValuePerShare(asReportedViewOr(...))` |
| E | **Industry classification** | 820–821 | `s.classifyTickerIndustry(...)` (already extracted) → `industryCode, sicLabel, heuristicCode, heuristicName` |
| F | **Shares outstanding resolution** | 823–834 | diluted → market.basic → financial.basic priority chain → `ErrInsufficientData` |
| G | **AssumptionProfile resolution** | 836–909 | build `Facts` (via `restatedViewOr`); `profileRegistry.Resolve` → `resolvedProfile, resolutionTrace`; Debug log; **EMIT manifest** (`SetAssumptionProfileManifest`, 899) |
| H | **Guidance + authority resolution** | 911–928 | `s.resolveGuidance(ctx, CIK, clock.Now(), resolvedProfile, nil)` → `guidanceResolution` |
| I | **Params resolution (WACC-independent knobs)** | 930–1031 | `hasAnyOverride`; `growthRateLen`; `industryExitMultiple`; profile knob baselines; `estCfg`; `legacyDefaultHorizon`; `resolverDefaults`; `params.ResolveInputs` → `p` |
| J | **Beta ladder** | 1033–1080 | `p.Beta`/`p.RiskFreeRate`; Blume; unlever/relever via `restatedViewOr` (`waccRestated`); Debug log |
| K | **Country risk premium** | 1082–1091 | `GetCountryForTicker` + `GetCountryRiskPremium`; Info log |
| L | **WACC compute + emit** | 1093–1151 | `wacc.Inputs`; `wacc.Calculate` → `waccResult`; metrics; **EMIT wacc** (calcEmitter 1116–1130 + narrate 1134 + artifact 1142–1151) |
| M | **Terminal growth finalize** | 1153–1164 | `params.ResolveTerminal(&p, waccResult.WACC, ...)` → `terminalGrowthRate` |
| N | **Model selection + emit** | 1166–1194 | `s.modelRouter.SelectModel(...)`; **EMIT model.selected** (narrate 1183 + artifact 1187) |
| O | **Alt-model path (early return)** | 1196–1242 | `performAlternativeValuation` when non-DCF; `errFallbackToDCF` handling; **returns altResult on success** |
| P | **DCF base OI (Restated, TTM, cyclical)** | 1244–1326 | `baseOI = effectiveOI(dcfRestated)`; BUG-015 TTM annualize; VAL-1 cyclical normalize (**EMIT cyclical_base_normalization** 1310); `<=0` guard |
| Q | **DCF inputs assembly** | 1328–1450 | `nwcChange`; `projectionYears`/`terminalMethodLabel`; `growthRatesForDCF` truncation; `dcfInputs`; `averageCapExAndDA`; exit-multiple switch; `applyReinvestmentModel` |
| R | **DCF compute + emit** | 1452–1511 | `dcf.CalculateDCF` → `dcfResult`; metrics; **EMIT fcf_projection (1468) / terminal_value (1492) / discount (1505)** (calcEmitter) |
| S | **Equity bridge + per-share + emit** | 1513–1566 | `investedCapitalOr`; `CalculateEquityValueWithDebtLikeClaims` → `equityValue`; `applyDilutedShareForward` → `denomShares`; `dcfValuePerShare`; **EMIT equity_bridge** (1555) |
| T | **Freshness + Graham + result struct** | 1568–1665 | `calculateDataFreshnessScore` (+NOPAT penalty); `calculateGrahamFloorMetrics`; build `result`; stamp profile/trace; stamp 5 DCF diagnostics |
| U | **Warnings assembly** | 1667–1766 | dilution / terminal-dominance / NOPAT / reinvestment / guidance / TTM-OI / fallback / param-advisory / applied-overrides / graham warnings |
| V | **Sanity cross-check + emit** | 1768–1854 | `CalculateSanityCheck`; **EMIT cross_check (1812) + narrate crosscheck (1840) + artifact (1846)** |
| W | **Final artifact snapshot + return** | 1856–1863 | **EMIT valuation.computed** (artifact 1858); `return result, nil` |

### 1.2 Emission sites — actual inventory (NOT one uniform shape)

The prompt's "~24 identical 3-line blocks" is an over-simplification. The reality
is **two families** with **different shapes**, plus one manifest call. The
implementer must NOT assume a single uniform block — the helper API below is
shaped to match what actually exists. Confirmed inventory (grep over 685–1864):

**Family 1 — `calcEmitter` (pointer-guarded), 7 stage sites + 1 nested:**

| Line | Stage string | Guard |
|------|--------------|-------|
| 1117 | `"wacc"` | `if s.calcEmitter != nil` (1116) |
| 1311 | `"cyclical_base_normalization"` | `if s.calcEmitter != nil` (1310), **nested inside** the `method=="3y_mean"` branch |
| 1468 | `"fcf_projection"` | `if s.calcEmitter != nil` (1463) |
| 1492 | `"terminal_value"` | `if s.calcEmitter != nil` (1478) |
| 1505 | `"discount"` | `if s.calcEmitter != nil` (1504) |
| 1555 | `"equity_bridge"` | `if s.calcEmitter != nil` (1554) |
| 1812 | `"cross_check"` | `if s.calcEmitter != nil` (1811), **nested inside** `if s.industryMultiples != nil` |

**Family 2 — `narrate` + `artifact` stage-boundary pairs:**

| Lines | narrate phase | artifact file |
|-------|---------------|---------------|
| 795–806 | `PhaseGrowthEstimated` | `12-growth-curve.json` + `AddSchemaVersion("GrowthEstimate",1)` |
| 1134–1151 | `PhaseWACCComputed` | `13-wacc.json` (map payload) |
| 1183–1194 | `PhaseModelSelected` | `14-model-selection.json` (map payload) |
| 1840–1848 | `PhaseCrosscheckEvaluated` | `16-crosscheck.json` |

**Family 3 — bare artifact calls (no narrate sibling):**

| Lines | call |
|-------|------|
| 899–908 | `artifact.From(ctx).SetAssumptionProfileManifest(...)` (inside `if resolvedProfile != nil`) |
| 802–805 | the artifact half of the growth pair (`b.Snapshot` + `AddSchemaVersion`) |
| 1858–1861 | `artifact.From(ctx).Snapshot("valuation.computed","15-valuation.json",result)` + `AddSchemaVersion("ValuationResult",2)` |

**Deviations that constrain the helper API (READ CAREFULLY):**

- The `calcEmitter` blocks do NOT all share field lists — each computes
  stage-specific locals *inside* the guard (e.g. `fcfSeries` at 1464–1467,
  `gordonTV`/`exitMultipleUsed` at 1485–1491, `perYearPV`-style loops). A naive
  "one method, fixed fields" helper does NOT fit. **The helper must accept
  `...zap.Field`** so the call site keeps computing its own fields, and the helper
  only removes the `if s.calcEmitter != nil` boilerplate + the literal stage string.
- Two `calcEmitter` blocks are **conditionally nested** (cyclical inside
  `3y_mean`; cross_check inside `industryMultiples != nil`). The helper does NOT
  hoist them out — they stay nested; only the inner `if s.calcEmitter != nil`
  collapses.
- The narrate/artifact pairs each carry **different payload types** (struct vs
  `map[string]any`) and **different outcome logic** (`OutcomeOK` vs computed
  `gOutcome`/`ccOutcome`). They do NOT collapse to one signature. Treat the
  narrate emit and the artifact snapshot as **two separate thin helpers**, not a
  fused "stage" method (fusing them would force the implementer to move the
  outcome/payload computation, which risks reordering — forbidden).

### 1.3 Intermediate locals that cross stage boundaries (the crux)

These are the locals defined in an early stage and read by a later one. They
determine the phase-split carrier. (Ordered by first definition.)

| Local | Defined (line) | Last read (line) | Consumed by phase(s) |
|-------|----------------|------------------|----------------------|
| `historicalGrowth` | 708 | 1158 (`SummaryGrowthRate` is on estimate; `IsReliable` at 792) | C, (growth emit) |
| `analystData` | 720 | 779 | C |
| `sustainableGrowth` | 723 | 779 | C |
| `overrides` | 758 | 1424–1431 | C, I, Q |
| `estimator` | 763 | 988 | C, I |
| `growthEstimate` | 779 | 1596 | C…T (pervasive) |
| `latestFinancialData` | 809 | 1790 | D…V (pervasive) |
| `latestPeriod` | 809 | 1608 | P, T |
| `tangibleValuePerShare` | 818 | 1588 | T |
| `industryCode` | 821 | 1803 | E…V (pervasive) |
| `sicLabel` | 821 | 1620 | T (and alt-path 1219) |
| `heuristicCode`/`heuristicName` | 821 | 1622 (alt 1222–1223) | T, O |
| `sharesOutstanding` | 825 | 1790 | F…V |
| `resolvedProfile` | 851 | 1673-area | G…U (pervasive) |
| `resolutionTrace` | 852 | 1631 (alt 1229) | G, O, T |
| `guidanceResolution` | 925 | 1713 (alt 1238–1239) | H…U |
| `hasOverrides` | 941 | 1752 | I, U |
| `growthRateLen` | 956 | 1026 | I |
| `industryExitMultiple` | 963 | 1436 (default for `exitMultiple`) | I, Q |
| `p` (EffectiveValuationParams) | 1026 | 1759 | I…U (pervasive) |
| `beta`/`riskFreeRate` | 1036–1037 | 1119 | J, L |
| `rawBeta`/`blumeBeta`/`unleveredBeta`/`releveredBeta` | 1041–1045 | 1149 | J, L (wacc emit) |
| `waccRestated` | 1068 | 1558 | J, L, S |
| `marketEquity` | 1069 | 1070–1071 | J only (local) |
| `countryRiskPremium` | 1085 | 1139 | K, L |
| `waccResult` | 1105 | 1800 | L…V |
| `terminalGrowthRate` | 1163 | 1656 | M…T |
| `selectedModel` | 1171 | 1212 | N, O |
| `dcfFallbackWarning` | 1196 | 1730 | O, U |
| `dcfRestated` | 1252 | 1288 | P |
| `baseOI` | 1253 | 1450 (reinvestment seed) + 1325 | P, Q |
| `ttmOperatingIncomeSource`/`Warning` | 1258 | 1721–1725 | P, U |
| `baseNormalizationMethod` | 1301 | 1659 | P, T |
| `nwcChange` | 1329 | 1370 | Q |
| `projectionYears` | 1341 | 1647 (+1546 dilution) | Q, S, T |
| `terminalMethodLabel` | 1342 | 1648 | Q, T |
| `growthRatesForDCF` | 1347 | 1450 | Q |
| `dcfInputs` | 1352 | 1452 | Q, R |
| `avgDA`/`avgCapEx` | 1365 | 1369 | Q only (local) |
| `usingNOPATFallback` | 1380 | 1691 | Q, T, U |
| `exitMultiple` | 1422 | 1439 | Q only (local) |
| `reinvestmentWarnings` | 1450 | 1700 | Q, U |
| `dcfResult` | 1452 | 1800 | R…V (pervasive) |
| `bridgeInvestedCap`/`debtLikeClaims` | 1526–1527 | 1561 | S |
| `equityValue` | 1528 | 1800 | S, T, V |
| `denomShares` | 1544 | 1563 | S |
| `forwardShares`/`appliedDilutionRate`/`dilutionWarnings` | 1545 | 1676 | S, T, U |
| `dcfValuePerShare` | 1550 | 1589 | S, T |
| `dataFreshnessScore` | 1569 | 1611 | T |
| `gf` (Graham metrics) | 1579 | 1765 | T, U |
| `result` | 1582 | 1863 (return) | T…W |

**Conclusion on the crux:** the data-flow is dense and pervasive — `growthEstimate`,
`latestFinancialData`, `sharesOutstanding`, `industryCode`, `resolvedProfile`,
`guidanceResolution`, `p`, `waccResult`, `dcfResult`, `equityValue`, `result`
each cross many phases. A wide-parameter signature would mean 15–25-arg functions
(unmaintainable and itself error-prone). **Use a single private carrier struct**
(§3.3) passed by pointer between phases. This is the lowest-diff, lowest-risk
threading mechanism and keeps every local's name identical to today (critical for
a mechanical, reviewable diff).

---

## 2. Problems (concrete)

1. **Length.** ~1,180 lines in one function — far past any readable threshold;
   the single largest function in the codebase.
2. **Emission duplication.** 7 `if s.calcEmitter != nil { s.calcEmitter.Emit(ctx, "<stage>", …) }`
   wrappers + 4 narrate/artifact pairs + 3 bare artifact calls = repeated
   boilerplate where only the stage string + fields differ. The `if … != nil`
   guard is copy-pasted 7×.
3. **Drift risk.** A new stage today means hand-copying the 2-line calcEmitter
   guard or the 11-line narrate+artifact pair; easy to forget `AddSchemaVersion`
   or mis-key the artifact filename. Centralizing the *guard* (not the fields)
   removes that class of error.
4. **Reviewability.** Any change inside this function forces a reviewer to hold
   the entire 1,180-line context. Named phases let a reviewer reason about WACC
   independently of DCF.
5. **Cognitive load of the carrier-less style.** ~50 locals live in one scope;
   nothing signals which belong to which phase.

---

## 3. Refactor Plan — small reviewable commits

> **Golden rule for every commit:** the diff must be a pure *move* + *rename of
> the guard*. No expression is rewritten, no read is reordered, no `restatedViewOr`/
> `asReportedViewOr`/`investedCapitalOr` call is moved relative to its neighbors.
> After each commit run the full verification suite in §5 and (when a baseline
> exists) the replay diff. **Stop and revert** if any pin moves.

### Commit 1 — `stageEmitter` extraction (mechanical, lowest-risk)

**Goal:** remove the `if s.calcEmitter != nil` boilerplate and the literal stage
strings, without touching field computation or narrate/artifact payloads.

**New file:** `internal/services/valuation/stage_emitter.go`

```go
// stageEmitter bundles the per-request emission context (ctx + the service's
// calcEmitter) so the repeated `if s.calcEmitter != nil { s.calcEmitter.Emit(...) }`
// ritual collapses to one call. Emit-only; carries NO math. Behavior is
// byte-identical: calc(stage, fields...) is exactly the old guarded Emit.
type stageEmitter struct {
    ctx     context.Context
    emitter *calclog.Emitter   // may be nil — calc() guards it, matching today
}

func (s *Service) newStageEmitter(ctx context.Context) stageEmitter {
    return stageEmitter{ctx: ctx, emitter: s.calcEmitter}
}

// calc replays the legacy `if s.calcEmitter != nil { s.calcEmitter.Emit(ctx, stage, fields...) }`.
func (se stageEmitter) calc(stage string, fields ...zap.Field) {
    if se.emitter != nil {
        se.emitter.Emit(se.ctx, stage, fields...)
    }
}
```

**Why `...zap.Field` (not one method per stage):** §1.2 shows the field lists are
stage-specific and several are computed inside the guard. A fixed-field-per-stage
API would force moving that computation — forbidden. `calc(stage, fields...)`
keeps every field expression exactly where it is.

**Edits in `performValuation`:** introduce `se := s.newStageEmitter(ctx)` once near
the top (after the validation guards, before the first calcEmitter use at 1116).
Then mechanically rewrite each of the 7 calcEmitter sites:

```go
// before
if s.calcEmitter != nil {
    s.calcEmitter.Emit(ctx, "wacc", zap.String("ticker", …), …)
}
// after
se.calc("wacc", zap.String("ticker", …), …)
```

Apply identically to `wacc` (1116), `cyclical_base_normalization` (1310 — stays
nested in the `3y_mean` branch), `fcf_projection` (1463), `terminal_value`
(1478), `discount` (1504), `equity_bridge` (1554), `cross_check` (1811 — stays
nested in `industryMultiples != nil`).

**Scope discipline for Commit 1:** Do the narrate/artifact pairs in this commit
too **only as guard-collapse**, OR leave them entirely for a later commit. The
narrate side is *already* nil-safe (`narrate.From(ctx).Emit` no-ops when
disabled), and the artifact side already uses `if b := artifact.From(ctx); b != nil`.
**Recommendation: leave narrate/artifact untouched in Commit 1.** They are not
duplicated boilerplate in the same way (each has a distinct payload + outcome),
and collapsing them buys little while widening the diff. Keep Commit 1 to the
7 calcEmitter sites + the manifest is NOT touched. This keeps Commit 1 a
trivially-reviewable ~30-line diff.

**Also apply** the same `se.calc(...)` rewrite to the 2 other in-file calcEmitter
families that share the helper opportunistically *only if already touching the
file*: `performAlternativeValuation` has **no** calcEmitter sites (verified —
only a bare artifact snapshot at 2222), and `reinvestment.go`/`diluted_forward.go`
each have their own `if s.calcEmitter != nil` site. **Leave those out of Commit 1**
(different functions/files; out of scope for this issue's stated target). Note
them as a possible follow-up but do not expand scope.

**Verify Commit 1:** §5 suite + replay diff. The narrate/artifact integration
test (`TestNarrateArtifact_TraceOn_EmitsStreamAndBundle`) is the key net here —
it asserts the calc trace stream is present and unchanged.

---

### Commit 2 — introduce the phase carrier struct (no behavior, no extraction yet)

**Goal:** add the carrier type and populate it *inline at the existing definition
sites*, WITHOUT yet moving any code into sub-functions. This isolates the
"introduce struct" risk from the "move code" risk. After this commit the function
is the same length but every cross-phase local is also a carrier field.

> Optional but RECOMMENDED. If the reviewer prefers fewer commits, Commit 2 and
> Commit 3 can be fused — but separating them makes the move-only diff in 3
> easier to audit. The implementer chooses; default is split.

**New type (in `service.go` or a new `valuation_pipeline.go`):**

```go
// valuationCtx carries the intermediate state threaded between the phases of
// performValuation. It exists ONLY to avoid 20-arg phase functions; it holds no
// behavior. Every field maps 1:1 to a local that previously lived in
// performValuation's scope (same name), so the phase-split diff stays a pure move.
type valuationCtx struct {
    // inputs (set by the caller / validation)
    historicalData *entities.HistoricalFinancialData
    marketData     *entities.MarketData
    macroData      *entities.MacroData
    opts           *ValuationOptions
    cleaned        *cleaneddata.CleanedFinancialData

    // resolveValuationInputs outputs (stages B–N up to model selection)
    overrides            params.Overrides
    growthEstimate       *growthsvc.GrowthEstimate   // match the real type
    latestFinancialData  *entities.FinancialData
    latestPeriod         string
    tangibleValuePerShare float64
    industryCode, sicLabel, heuristicCode, heuristicName string
    sharesOutstanding    float64
    resolvedProfile      *profile.ResolvedProfile
    resolutionTrace      profile.ResolutionTrace
    guidanceResolution   <guidance resolution type>
    hasOverrides         bool
    industryExitMultiple float64
    p                    params.EffectiveValuationParams
    selectedModel        models.ValuationModel

    // computeWACC outputs
    waccRestated         *cleaneddata.FinancialDataView
    rawBeta, blumeBeta, unleveredBeta, releveredBeta float64
    beta, riskFreeRate   float64
    countryRiskPremium   float64
    waccResult           wacc.Result
    terminalGrowthRate   float64

    // runDCF outputs
    baseOI               float64
    baseNormalizationMethod string
    ttmOperatingIncomeSource, ttmOperatingIncomeWarning string
    projectionYears      int
    terminalMethodLabel  string
    usingNOPATFallback   bool
    reinvestmentWarnings []string
    dcfResult            dcf.Result
    equityValue          float64
    dcfValuePerShare     float64
    denomShares, forwardShares float64
    appliedDilutionRate  float64
    dilutionWarnings     []string
    dcfFallbackWarning   string
    // …any remaining cross-phase local from §1.3
}
```

> The implementer fills the exact Go types by reading the definition sites (the
> types above are sketched from the reads; confirm `growthEstimate`,
> `guidanceResolution`, `waccResult`, `dcfResult`, `selectedModel` concrete types
> at their declaration lines). Locals that are **phase-local only** (`marketEquity`,
> `avgDA`/`avgCapEx`, `exitMultiple`, `nwcChange`, `growthRatesForDCF`,
> `bridgeInvestedCap`, `dcfInputs`, `dcfRestated`, `gf`, `dataFreshnessScore`,
> `result`, `historicalGrowth`, `analystData`, `sustainableGrowth`, `estimator`,
> `growthRateLen`) DO NOT go in the carrier — they stay locals inside their phase.

**This commit:** declare `v := &valuationCtx{historicalData: …, marketData: …, …}`
after validation, and change later code to read/write `v.<field>` instead of the
bare local *only where it would otherwise have to* — i.e. defer the bulk of this
into Commit 3 by keeping Commit 2 to JUST adding the type + the input-field
population. (If fusing 2+3, skip this paragraph.)

---

### Commits 3a–3d — phase split, one phase per commit

Extract phases **bottom-up is NOT required; do it top-down** in this order so each
extraction's "produces" set is already a carrier field by the time the next phase
consumes it. Each phase becomes a method on `*Service` taking `(ctx, v *valuationCtx)`
and returning `error` (phases that can early-return like the alt-model path return
`(*entities.ValuationResult, bool, error)` — see 3b).

**Commit 3a — `resolveValuationInputs` (stages A–N, lines 694–1194):**

```go
// resolveValuationInputs runs validation, growth estimation, industry/profile/
// guidance resolution, params resolution, the beta ladder, WACC, terminal growth,
// and model selection — everything BEFORE the DCF-vs-alt-model branch. It mutates
// v in place. Read-order is byte-identical to the original 694–1194.
func (s *Service) resolveValuationInputs(ctx context.Context, v *valuationCtx) error
```

- Consumes: `v.historicalData/marketData/macroData/opts/cleaned`.
- Produces: every "resolveValuationInputs outputs" + "computeWACC outputs" field
  through model selection (`v.selectedModel`, `v.terminalGrowthRate`).
- **Wait — WACC is its own phase per the brief.** Resolve this by keeping WACC as
  an *inner* call: `resolveValuationInputs` calls `s.computeWACC(ctx, v)` at the
  point where line 1093 is today. That preserves read-order while still giving a
  named `computeWACC`. So **3a extracts A–I + M–N**, and **3b extracts J–L
  (computeWACC)** called from inside 3a at the original position. (See note.)

> **Ordering note (important):** because WACC (L) sits *between* params (I) and
> terminal-growth (M) which sits *between* WACC and model-selection (N), you
> cannot cleanly make `computeWACC` a sibling phase without splitting
> `resolveValuationInputs`. Two acceptable shapes:
> - **(preferred) nested:** `resolveValuationInputs` does I, then calls
>   `s.computeWACC(ctx, v)` (J–L), then does M (terminal) and N (model). One
>   top-level phase, WACC is a named helper it calls. Lowest diff.
> - **(alt) three top-level phases:** `resolveInputsPreWACC` (A–I) → `computeWACC`
>   (J–L) → `finalizeModelSelection` (M–N), called in sequence from
>   `performValuation`. More phases, slightly larger diff. Both preserve read-order.
> Default to **nested**.

**Commit 3b — `computeWACC` (stages J–L, lines 1033–1151):**

```go
// computeWACC runs the beta ladder (Blume + unlever/relever), country-risk
// premium, the WACC calculation, metrics, and the wacc-stage emissions. Mutates
// v (beta fields, waccRestated, countryRiskPremium, waccResult). Read-order
// byte-identical to 1033–1151.
func (s *Service) computeWACC(ctx context.Context, v *valuationCtx) error
```

- Consumes: `v.p` (beta/rf/MRP/tax), `v.marketData`, `v.cleaned`,
  `v.latestFinancialData`, `v.historicalData` (ticker, country), `v.riskFreeRate`.
- Produces: `v.beta/rawBeta/blumeBeta/unleveredBeta/releveredBeta`,
  `v.waccRestated`, `v.countryRiskPremium`, `v.waccResult`.
- Emissions: the `se.calc("wacc", …)` + narrate `PhaseWACCComputed` + artifact
  `13-wacc.json` stay here, in order.

**Commit 3c — `runDCF` (stages O–S, lines 1196–1566):**

```go
// runDCF handles the alt-model branch (which may early-return a completed
// result), then the standard multi-stage DCF: base-OI (Restated/TTM/cyclical),
// DCF input assembly, the DCF calculation, the equity bridge, the diluted-share-
// forward denominator, and the fcf/terminal/discount/equity_bridge emissions.
//
// Returns (result, done, err): when the alt-model path produced a final result,
// done==true and result is non-nil — performValuation returns it directly. On
// the DCF path done==false and runDCF has populated v.dcfResult/equityValue/etc.
func (s *Service) runDCF(ctx context.Context, v *valuationCtx) (*entities.ValuationResult, bool, error)
```

- This is the trickiest extraction because of the **alt-model early return**
  (1240) and the **`errFallbackToDCF`** flow. Preserve exactly:
  - non-DCF model + success → stamp SIC/heuristic/profile/guidance onto altResult,
    `return altResult, true, nil`.
  - `errFallbackToDCF` → set `v.dcfFallbackWarning`, fall through to DCF.
  - other altErr → `return nil, false, altErr`.
  - DCF path completes → `return nil, false, nil` (result built in assembleResult).
- Consumes: `v.selectedModel`, all growth/profile/guidance/p/waccResult/shares/
  industry fields, `v.cleaned`, `v.latestFinancialData`, `v.latestPeriod`,
  `v.terminalGrowthRate`, `v.tangibleValuePerShare`, `v.resolutionTrace`.
- Produces: `v.baseOI`, `v.baseNormalizationMethod`, `v.ttmOperatingIncome*`,
  `v.projectionYears`, `v.terminalMethodLabel`, `v.usingNOPATFallback`,
  `v.reinvestmentWarnings`, `v.dcfResult`, `v.equityValue`, `v.dcfValuePerShare`,
  `v.denomShares/forwardShares/appliedDilutionRate/dilutionWarnings`,
  `v.dcfFallbackWarning`.
- **`exitMultiple`, `dcfInputs`, `nwcChange`, `avgDA/avgCapEx`, `growthRatesForDCF`,
  `dcfRestated`, `bridgeInvestedCap`, `debtLikeClaims`** stay as locals inside
  `runDCF` (phase-local).

**Commit 3d — `assembleResult` (stages T–W, lines 1568–1863):**

```go
// assembleResult builds the ValuationResult from the DCF outputs, stamps the
// profile/trace/diagnostics, assembles all warnings (in the original order),
// runs the multiples sanity cross-check + its emissions, snapshots the final
// artifact, and returns. Read-order byte-identical to 1568–1863.
func (s *Service) assembleResult(ctx context.Context, v *valuationCtx) (*entities.ValuationResult, error)
```

- Consumes: nearly every output field on `v`.
- Produces: the final `*entities.ValuationResult`.
- **Warning order is observable** (POST==GET byte-identity, replay): the append
  sequence at 1676 → 1853 MUST stay exactly as-is. Do not reorder a single
  `result.Warnings = append(...)`.
- Emissions: `se.calc("cross_check", …)` + narrate `PhaseCrosscheckEvaluated` +
  artifact `16-crosscheck.json` + final `15-valuation.json` snapshot, in order.

**After 3a–3d, `performValuation` becomes a thin orchestrator:**

```go
func (s *Service) performValuation(ctx, historicalData, marketData, macroData, opts, cleaned) (*entities.ValuationResult, error) {
    v := &valuationCtx{historicalData: historicalData, marketData: marketData, macroData: macroData, opts: opts, cleaned: cleaned}
    if err := s.resolveValuationInputs(ctx, v); err != nil { return nil, err }
    if result, done, err := s.runDCF(ctx, v); err != nil || done { return result, err }
    return s.assembleResult(ctx, v)
}
```

(With the nested-WACC shape, `computeWACC` is called inside `resolveValuationInputs`,
so `performValuation` has three top-level calls. The validation guards (A) can stay
in `performValuation` or move into `resolveValuationInputs` — keep them in
`resolveValuationInputs` so the orchestrator is purely structural. Either is fine;
pick one and keep it.)

### Commit N (final) — tidy + doc

- Add a package-level comment block documenting the phase pipeline + the carrier
  contract (mirroring the `classifyTickerIndustry` godoc precedent).
- No code change. Run full suite + replay diff one last time.

---

## 4. Safety Constraints — the explicit unchanged contract

1. **Signature unchanged:** `performValuation(ctx, historicalData, marketData,
   macroData, opts, cleaned) (*entities.ValuationResult, error)` — same params,
   same return. The new phase methods are unexported helpers; they do NOT change
   the public/package surface.
2. **Return values unchanged:** same `*entities.ValuationResult` content and the
   same `CalculationVersion "4.10"` stamp. The alt-model early-return path returns
   the same `altResult` it does today.
3. **Error semantics unchanged:** every `return nil, err`/`return nil, <wrapped>`
   stays. Typed `*params.ParamError` propagation (ResolveInputs 1026, ResolveTerminal
   1158, ValidateEstimatorConfig 770) must still bubble untouched so the handler
   maps to 422. The four sentinel errors (`ErrInsufficientData`,
   `ErrModelNotApplicable`) at 696/700/704/811/833/1325 unchanged.
4. **Side effects unchanged & in order:** metrics increments (`IncWACCCalculations`,
   `SetAverageWACC`, `IncDCFCalculations`, `SetAverageGrowthRate`) at their exact
   positions; Debug/Info/Warn logs unchanged.
5. **Emission ORDER is observable — preserve it.** narrate phases + artifact
   snapshot files + calc-trace stages are asserted by
   `TestNarrateArtifact_TraceOn_EmitsStreamAndBundle`. The emit sequence
   (growth.estimated → wacc → model.selected → fcf_projection → terminal_value →
   discount → equity_bridge → crosscheck → valuation.computed, plus the manifest)
   MUST fire in the same order with the same payloads/filenames/schema versions.
6. **No reorder of view reads:** every `restatedViewOr` / `asReportedViewOr` /
   `investedCapitalOr` call stays in its exact relative position. Listed sites:
   735, 818, 865, 1068, 1171, 1252, 1526, 1580, 1777. Moving any of these (even
   into a phase fn) is fine ONLY if its relative order to surrounding reads is
   identical — which a pure move guarantees.
7. **`s.clock.Now()` call count + position unchanged** (1587 result + 925 guidance
   as-of). Replay determinism depends on it.
8. **Warning append order unchanged** (assembleResult, 1676–1853).

---

## 5. Test Strategy

### 5.1 Existing coverage over the refactored surface — ADEQUATE

The byte-identity + replay pins are the real net; no new tests needed. Confirmed
present and covering this surface:

| Test | File | What it pins |
|------|------|--------------|
| `TestDDM_LegacyPath_BitForBit` | `internal/services/valuation/models/ddm_bitforbit_test.go` | JPM/BAC/WFC `math.Float64bits` (alt-model path through `performAlternativeValuation`, reached via `runDCF`'s branch) |
| `TestPostFairValue_EmptyBody_EqualsGET` | `internal/api/v1/handlers/fair_value_post_test.go:126` | POST{}==GET byte-identity (full response incl. warning order) |
| `TestService_performValuation_BUG015_*` (×3) | `internal/services/valuation/bug015_quarterly_oi_base_test.go` | TTM-base annualization + FY-latest invariance (stage P) |
| `TestNarrateArtifact_TraceOn_EmitsStreamAndBundle` + 4 siblings | `internal/integration/narrate_artifact_test.go` | **emission order/content** — the calc-trace stream + artifact bundle files. This is the net for Commit 1 and the emission-order constraint (§4.5). |
| replay field-count guard `init()` | `internal/observability/replay/diff.go:36` | response struct shape unchanged (panics on drift) |
| VAL-1 phase tests | `val1_phase2_horizon_test.go`, `val1_phase3_cyclical_test.go`, `diluted_forward_service_test.go` | stages I/P/S diagnostics |
| `service_test.go` (CalculationVersion pins, ×4) | `internal/services/valuation/service_test.go` | `"4.10"` stamp |

**No characterization-test gap identified.** The combination of (a) byte-identity
(`TestPostFairValue_EmptyBody_EqualsGET`), (b) bit-for-bit DDM, (c) the narrate/
artifact integration assertions on emission order, and (d) the replay field-count
guard covers both the math read-order AND the emission order — exactly the two
things this refactor must not disturb. Do NOT pad with extra unit tests.

> **One caveat to verify, not a gap:** confirm `TestNarrateArtifact_TraceOn_…`
> runs in the local environment. It builds the real valuation service via
> `di.CoreModule` and values AAPL using DB-seeded financial data (`SeedTestData`),
> with macro on the config-fallback path (`FREDEnabled=false`,
> `ManualRiskFreeRate=0.04`). If it requires live SEC for AAPL in this worktree,
> note it and rely on the unit pins + replay diff instead — but it should run
> from seeded data. Run it once at baseline to confirm.

### 5.2 Per-commit verification commands (run ALL, every commit)

```bash
# from the worktree root: c:/Users/Yonatan Levin/.../midas-sr1-a7

# 1. Build (catches any signature/type mistake immediately)
go build ./...

# 2. The valuation engine + replay guard (fast, hermetic)
go test ./internal/services/valuation/... ./internal/observability/replay/... -count=1

# 3. The byte-identity + applied-overrides handler tests
go test ./internal/api/v1/handlers/ -run 'TestPostFairValue_EmptyBody_EqualsGET|AppliedOverrides' -count=1

# 4. The DDM bit-for-bit + BUG-015 pins (explicit, so a failure is unmissable)
go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1
go test ./internal/services/valuation/ -run 'BUG015' -count=1

# 5. The emission-order integration net (the key Commit-1 guard)
go test ./internal/integration/ -run 'TestNarrateArtifact|TestNarrate_' -count=1

# 6. Full suite (final check each commit; the ONE expected failure is unrelated —
#    see §5.3). Do NOT count it as a regression.
go test ./... -count=1
```

### 5.3 Replay diff — baseline is ABSENT, capture a local one FIRST

**Reality check (verified in this worktree):**
- `artifacts/tier2-baseline/2026-06-06/` **does NOT exist** — in fact the entire
  `artifacts/` directory is absent in this worktree.
- `artifacts/tier2-baseline/2026-05-19` is likewise absent (BUG-016), so
  `TestDataCleanerRecompute_ShadowMode_TickerBasket` **fails pre-existing**. That
  single failure in the full `go test ./...` run is EXPECTED and UNRELATED to this
  refactor — do not try to fix it, and do not treat it as a regression.

**Because there is no committed baseline, the per-commit replay diff needs a
fresh LOCAL baseline captured at HEAD before any refactoring:**

1. **At branch HEAD (before Commit 1)**, capture a baseline per
   `docs/accuracy/baseline-capture-runbook.md` §3 into a scratch dir
   (NOT committed — it goes in the scratchpad, since `.gitignore` only tracks
   `artifacts/tier2-baseline/**`):
   ```bash
   go build -o /tmp/midas-server.exe ./cmd/server
   go run ./cmd/seed-demo-key -db ./data/midas.db        # prints DEMO_API_KEY
   SERVER_PORT=8090 PORT=8090 SCHEDULER_ENABLED=false /tmp/midas-server.exe &
   KEY=dcf_...
   for T in AAPL AMD EQIX F JPM KO MSFT MXL NVDA PLD; do
     curl -s -H "X-API-Key: $KEY" "http://localhost:8090/api/v1/fair-value/$T?trace=1" -o /dev/null
   done
   # promote the day's captures to a scratch baseline dir:
   #   <scratch>/sr1-a7-baseline/
   ```
   > This requires **live SEC EDGAR** (financials) + Yahoo/Finzive (price). FRED is
   > optional (config-fallback curve). If the network is unavailable, the replay
   > diff cannot be run and the §5.2 test suite (esp. the integration emission net)
   > becomes the sole guard — which is acceptable for a pure-move refactor but
   > weaker; note it explicitly in the PR.

2. **After each commit**, diff the current engine against the scratch baseline:
   ```bash
   go run ./cmd/replay --diff-stages --from=parsed <scratch>/sr1-a7-baseline/
   ```
   Expect **zero drift** at every stage (the refactor changes no math). Any
   per-stage diff = revert and investigate. The JPM bundle may need
   `--allow-schema-drift` (documented quirk for missing `10-clean-output.json`).

3. **If live capture is impossible:** fall back to the unit + integration suite in
   §5.2 as the complete net. The DDM bit-for-bit, POST==GET byte-identity, BUG-015
   pins, and the narrate/artifact emission-order assertions together cover math
   read-order and emission order — the two invariants this refactor must hold.

---

## 6. Implementation Roadmap (order)

1. **Commit 1** — `stageEmitter` (`stage_emitter.go` + 7 calcEmitter rewrites). Verify.
2. **Commit 2** — `valuationCtx` carrier type + input-field population (optional split). Verify.
3. **Commit 3a** — extract `resolveValuationInputs` (A–I, M–N; nested WACC call). Verify.
4. **Commit 3b** — extract `computeWACC` (J–L). Verify.
5. **Commit 3c** — extract `runDCF` (O–S, with the alt-model early-return contract). Verify.
6. **Commit 3d** — extract `assembleResult` (T–W). Verify.
7. **Commit N** — pipeline godoc + final full-suite + replay diff. Verify.

Each commit is independently green and independently revertable.

---

## 7. Potential Challenges & Mitigations

| Risk | Mitigation |
|------|-----------|
| **Alt-model early-return** entangles control flow when extracting `runDCF`. | Use the `(result, done, err)` return triple (§3 Commit 3c). The branch logic moves verbatim; only the `return altResult, nil` becomes `return altResult, true, nil`. |
| **WACC sits mid-stream** between params and terminal/model. | Nested-WACC shape (§3a note): `resolveValuationInputs` calls `computeWACC` at the original line position. No read-order change. |
| **Warning append order** is byte-observable. | `assembleResult` keeps the exact 1676→1853 append sequence; pinned by `TestPostFairValue_EmptyBody_EqualsGET`. |
| **Carrier field-type mistakes** (e.g. `growthEstimate`/`waccResult`/`dcfResult` concrete types). | `go build ./...` after Commit 2 catches every type mismatch before any move. Confirm types at declaration sites (779, 1105, 1452, 1171, 925). |
| **Emission order regressions** from accidentally moving a `se.calc`/narrate/artifact call. | `TestNarrateArtifact_TraceOn_EmitsStreamAndBundle` asserts the stream + bundle; run it every commit. |
| **No replay baseline** in the worktree. | Capture a local scratch baseline at HEAD first (§5.3); if network-blocked, fall back to the test suite as the net and disclose in the PR. |
| **Phase-local vs carrier confusion** (putting a phase-local into the carrier or vice-versa). | §1.3 explicitly lists which locals cross boundaries (carrier) vs stay phase-local. Follow it literally. |

---

## 8. Acceptance Criteria

- `performValuation` signature, return type, and error semantics unchanged.
- All §5.1 pins GREEN with assertions UNCHANGED (DDM bit-for-bit, POST==GET,
  BUG-015, narrate/artifact emission order, replay field-count guard, CalcVersion
  `"4.10"`).
- `go build ./...` and `go test ./...` green except the single pre-existing,
  unrelated `TestDataCleanerRecompute_ShadowMode_TickerBasket` failure (BUG-016).
- Replay diff (against the local HEAD baseline) shows **zero** per-stage drift —
  OR, if live capture is impossible, the §5.2 suite is green and the limitation is
  disclosed in the PR.
- `performValuation` reduced to a thin orchestrator (≈10 lines) delegating to
  `resolveValuationInputs` / `computeWACC` / `runDCF` / `assembleResult`.
- The 7 calcEmitter sites go through `stageEmitter.calc(...)`.
- No math expression rewritten; no view-read reordered; no emission reordered.

---

## 9. Assumptions & Open Questions

**Assumptions:**
- The refactor is purely structural; no accuracy/behavior change is desired or
  acceptable.
- `narrate`/`artifact` pairs are intentionally LEFT as inline blocks (not fused
  into `stageEmitter`) because their payloads/outcomes differ per site and fusing
  risks reordering. The `stageEmitter` covers only the `calcEmitter` family.
- The nested-WACC shape (one top-level `resolveValuationInputs` calling
  `computeWACC`) is preferred over three separate top-level pre/WACC/post phases.

**Open questions (non-blocking):**
- Should `reinvestment.go` / `diluted_forward.go` (which have their own
  `if s.calcEmitter != nil` sites) also adopt `stageEmitter.calc`? **Out of scope
  for #24** (different functions); flagged as a possible follow-up only.
- Is fusing Commit 2 into Commit 3a acceptable to the reviewer? Default keeps them
  split for auditability; reviewer may collapse.

**Decisions needed before implementation:** none blocking. Confirm live-network
availability for the replay baseline (§5.3); if unavailable, proceed with the
test-suite-only net and disclose.

---

## 10. GitHub Issue Update

- **Issue:** #24
- **Status:** not updated (ARCH planning only; this doc is the durable artifact)
- **Proposed update:** link this plan path
  (`docs/reviewer/SR-1-A7-performvaluation-refactor-plan.md`) in #24; set label to
  in-progress when the implementer starts Commit 1.

---

**HANDOFF_TO:** BACKEND (implement Commits 1→N mechanically, verifying per §5
after each) → REVIEWER (audit each commit as a pure move; confirm zero
math/emission-order drift).
