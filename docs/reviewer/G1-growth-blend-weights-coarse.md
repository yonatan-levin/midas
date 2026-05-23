# G-1 — `growth.estimated` narrate weights are coarse 0.5/0.5

**Status:** RESOLVED — fixed 2026-05-23 on branch `feat/g-1-growth-blend-weights` via Option A (the recommended path).
**Severity:** Minor (field accuracy; does not block production).
**Origin:** QA finding MINOR-2 on the observability-narrative Phase 1 spec implementation.

## Resolution summary (2026-05-23)

- `entities.GrowthEstimate` gained two fields: `AnalystWeight float64` and `HistoricalWeight float64` (JSON: `analyst_weight`, `historical_weight`). They sum to 1.0 by construction.
- `internal/services/growth/estimator.go::blendGrowthRate` populates both fields on every path:
  - No analyst coverage (or `NumberOfAnalysts == 0`) → 0.0 / 1.0.
  - 1-2 analysts → 0.40 / 0.60 (low confidence).
  - 3-9 analysts → 0.60 / 0.40 (medium confidence).
  - 10+ analysts → 0.80 / 0.20 (high confidence).
- `internal/services/valuation/service.go` (the `growth.estimated` narrate emit site) now reads `growthEstimate.AnalystWeight` / `growthEstimate.HistoricalWeight` directly, replacing the coarse `analystData != nil ? 0.5 : 0.0` ternary. The "Coarse signal — actual weighting is internal to estimator" comment is gone.
- Tests:
  - Existing `TestEstimator_NoAnalystData_UsesHistorical`, `TestEstimator_HighAnalystCoverage`, `TestEstimator_LowAnalystCoverage`, `TestEstimator_MediumAnalystCoverage` now assert the new fields alongside the existing math.
  - New `TestEstimator_BlendWeights_AllBuckets` table-test pins all four bucket → weight mappings + the sum-to-1.0 invariant in one place.
  - New `TestEstimator_BlendWeights_AnalystPresentButZeroAnalysts` pins the "non-nil but empty" edge case.
  - `TestNarrateArtifact_TraceOn_EmitsStreamAndBundle` (integration) was extended with a G-1 assertion block asserting the emitted `growth.estimated` line carries the estimator-sourced weights, not the legacy placeholder.
- Coverage on `internal/services/growth/` after the change: 95.4% of statements (unchanged from pre-G-1; new lines are exercised by existing + new tests).

## Original context (for traceability)

The `growth.estimated` narrate phase (one of the 17 phases defined in `internal/observability/narrate/phases.go`) is emitted from `internal/services/valuation/service.go:475-481` after the growth service returns its multi-stage estimate. Per the observability narrative spec §5 row 12, this phase carries the fields:

```
stage_count, analyst_weight, historical_weight, g_year_1, g_terminal
```

The current implementation hard-codes `analyst_weight = 0.5` / `historical_weight = 0.5` whenever `analystData != nil`, and `0.0 / 1.0` when `analystData == nil`. The honest comment in the code reads:

```go
// Coarse signal — actual weighting is internal to estimator and not exposed
// on growth.Result today. Filed as docs/reviewer/G1-growth-blend-weights-coarse.md.
```

## Why it matters

- A reader debugging a fair-value calculation that surprises them might inspect the `growth.estimated` line in the bundle expecting to see what mix the estimator actually used. Today they see `0.5/0.5` regardless of whether the estimator gave 70/30 weight to historical (e.g. if analyst data was thin) or 80/20 to analyst (e.g. if the historical series was noisy).
- Phase 1 of the spec is intentionally bounded — it ships the *taxonomy* of phases plus the bulk of the field set. Closing this gap is in scope for a Phase 2 follow-up.

## Proposed fix (one of)

### Option A — Extend `growth.Result` to expose blend weights (preferred)

`internal/services/growth/estimator.go` computes the blend internally inside `EstimateGrowthRates`. Surface it on the return type:

```go
type Result struct {
    Rates             []float64
    AnalystWeight     float64  // NEW: 0.0 if no analyst data; weighting actually applied
    HistoricalWeight  float64  // NEW: 1.0 - AnalystWeight by construction
    Source            Source
    Confidence        Confidence
}
```

Then in `valuation/service.go:475-481`:

```go
narrate.From(ctx).Emit(narrate.PhaseGrowthEstimated, narrate.OutcomeOK,
    zap.Int("stage_count", len(estimate.Rates)),
    zap.Float64("analyst_weight", estimate.AnalystWeight),
    zap.Float64("historical_weight", estimate.HistoricalWeight),
    zap.Float64("g_year_1", estimate.Rates[0]),
    zap.Float64("g_terminal", estimate.Rates[len(estimate.Rates)-1]),
)
```

Pros: keeps the spec field set intact, minimal narrate code change.
Cons: touches `growth.Result` (used by callers in tests and `models/router.go`). Need to add zero-value defaults to existing tests.

### Option B — Replace the two fields with `analyst_blend_used: bool`

Change the spec to drop `analyst_weight`/`historical_weight` and emit a single `analyst_blend_used` bool. Lossier signal but no API surface change.

Pros: zero-touch on `growth.Result`.
Cons: amends the spec; reduces debuggability.

## Recommendation

Option A. The growth blend math is exactly the kind of thing an investor wants to introspect when a valuation surprises them. Extending the return type is a one-package change with a well-understood call-site fan-out (a couple of tests + one production caller).

## Acceptance criteria

- [ ] `growth.Result` carries `AnalystWeight` + `HistoricalWeight` fields (option A) OR spec amended (option B).
- [ ] `valuation/service.go:475-481` emits the actual values (option A) OR the new bool (option B).
- [ ] Coverage on `internal/services/growth/` unchanged or improved.
- [ ] Existing `growth.Estimator` test fixtures updated to populate the new fields (option A only).
- [ ] One narrate-layer test asserting non-default weights in a representative happy path (e.g., analyst-rich fixture).

## Traceability

- Filed by: QA pass 2026-04-25 on `feat/observability-narrative` branch
- Spec it relates to: `docs/refactoring/observability-narrative-and-artifacts-spec.md` §5 row 12
- Code it relates to: `internal/services/valuation/service.go:475-481`, `internal/services/growth/estimator.go`
