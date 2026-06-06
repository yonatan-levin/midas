# Request Valuation Overrides — Fix Iterations Closeout

**Branch**: `feat/request-valuation-overrides`  
**Base feature merged**: T1-T10 (commit `f2c2ce7` T11 doc sync, 2026-06-05)  
**Fix iterations merged**: commits `c93f0b3`..`53a2426` (2026-06-05/06)  
**Cross-model review**: gpt-5.5-pro (zen-mcp) sign-off — no Critical/High blockers remaining

---

## Summary

The original T1-T10 feature shipped the override framework correctly but had a
validation-contract misalignment: the engine's internal `validateInputs` bounds in
`pkg/finance/wacc` and `pkg/finance/dcf` were narrower than the Layer-1 contract
ranges, so certain contract-accepted inputs would reach the engine and produce an
untyped error → HTTP 500 instead of the expected 200 or a typed 422.

Three fix iterations (plus integration tests and a doc sync) closed the gap.

---

## Fix Iterations

### FIX 1 — Engine range alignment (`c93f0b3`)
**Files**: `pkg/finance/wacc/wacc.go`, `pkg/finance/dcf/dcf.go`

The `validateInputs` functions in both packages had narrower rails than the contract:

| Parameter | Old engine bound | New engine bound | Contract range |
|-----------|-----------------|-----------------|----------------|
| beta | ≥ 0 | [−5, 5] | [−5, 5] |
| risk_free_rate | [0%, 20%] | [−5%, 25%] | [−5%, 25%] |
| market_risk_premium | [0%, 15%] | [0%, 30%] | [0%, 30%] |
| tax_rate | [0%, 100%] | [−50%, 100%] | [−50%, 100%] |
| terminal_growth_rate | [0%, 5%] | [−20%, 50%] | [−20%, 50%] |
| growth_rate | [−50%, 100%] | [−100%, 1000%] | [−100%, 1000%] |
| WACC upper rail | 50% cap | removed (math-only: WACC > 0) | no upper rail |
| projection_years | [1, 15] | [1, 50] | [1, 50] |

The widening only changes WHICH inputs are rejected by the engine; it never alters
the output for a previously-accepted input. Default-path byte-identity preserved.
The two MATHEMATICAL guards (WACC > 0 and WACC-vs-terminal spread ≥ 1%) are retained
as defense-in-depth — the valuation resolver catches both as typed 422s first.

### FIX 2 — Dynamic invariant 422s + terminal_method selector (`a77413f`, `d5c894c`, `ee5676a`, `9ea6fa7`)
**Files**: `internal/services/valuation/params/resolve.go`, `internal/services/valuation/service.go`

Three Layer-2 dynamic invariants that previously could reach the engine and 500 now
produce typed `*params.ParamError` → 422 `INVALID_OVERRIDE`:

1. **Near-WACC terminal growth**: `computedWACC - terminalGrowthRate < MinWACCTerminalSpread (1%)`
   → 422 `knob = "terminal_growth_rate"`, `context.limit = WACC − 1%` (dynamic per ticker).

2. **Non-positive WACC**: CAPM inputs (beta/rf/MRP) driving computed WACC ≤ 0
   → 422 `knob = "wacc"`. The Gordon perpetuity denominator and discount factors are
   undefined at WACC ≤ 0.

3. **Horizon > 50**: request-sourced `horizon_years` > MaxDCFProjectionYears (50)
   → 422 `knob = "horizon_years"`. Request-sourced `growth_stages` pushing horizon > 50
   → 422 `knob = "growth_stages"`. Profile/default-sourced: clamp + warn (legacy behavior).

**Predicate form alignment** (`53a2426`): the resolver spread check and `dcf.validateInputs`
now use the SAME predicate form (`computedWACC - g < spread`), not algebraically-rearranged
equivalents. The rearranged form (`g > wacc - spread`) differs by 1 ULP at the float
boundary, causing a resolver-accept / engine-reject mismatch. Pinned by
`TestResolverSpread_MatchesDCFPredicate`.

**MRP response field** (`d5c894c`): `result.MarketRiskPremium` now stamps the RESOLVED MRP
that fed WACC (not the raw `macroData.MarketRiskPremium`). Before the fix the response
field disagreed with WACC under an MRP override. Pinned by
`TestService_CalculateValuation_OverrideMarketRiskPremium`.

**terminal_method selector** (`d5c894c`): corrected the `"gordon_growth"` / `"exit_multiple"`
selector logic.
- `"gordon_growth"`: SUPPRESSES exit-multiple blending → pure Gordon TV.
- `"exit_multiple"`: BLENDS 50/50 Gordon/exit average (NOT a pure exit-multiple TV).
Before the fix `"gordon_growth"` did not suppress the averaging — a selector-vs-DTO
mismatch. Now corrected; design spec §13 is the authoritative description.

**Nested echo keys** (`9ea6fa7`): growth-stage applied_overrides keys are now
`"growth_stages.stage1_years"` / `"growth_stages.stage2_years"` / `"growth_stages.stage3_years"`
(mirroring the wire path the request used), not the flat `"stage1_years"` etc. Pinned by
`TestRequestOverrides_AllKnobTypes`.

**ParamError context enrichment** (`9ea6fa7`): the single-ticker 422 body now carries
`context.value` (offending value) and `context.limit` (numeric threshold, when applicable)
in addition to `context.knob`, matching the bulk path's error detail. Gated by
`pe.HasLimit` to avoid surfacing a real `0.0` threshold as absent.

### FIX 3 — NaN/±Inf GET params → 400 + legacy range widening (`4c135a2`)
**Files**: `internal/api/v1/handlers/fair_value.go`

Two independent changes:

1. **NaN/Inf rejection**: `parseFloatParam` now returns `(nil, error)` (was `(*float64, nil)`
   on a successful parse or `nil` on any error). Non-finite values (NaN, +Inf, −Inf, Inf)
   produce 400 `INVALID_PARAMETER`. `strconv.ParseFloat` accepts these strings without error,
   so the guard is explicit — a non-finite value would silently propagate into WACC/DCF and
   produce a non-finite response or an internal error. Absent param → `(nil, nil)`.
   Unparseable string → `(nil, err)`.

2. **Legacy GET range widening**: `override_beta` and `override_rf` range checks in both
   the GET single-ticker handler and the bulk handler now use the shared constants
   `betaMin/betaMax` and `riskFreeRateMin/riskFreeRateMax` from `fair_value_validation.go`
   (previously hardcoded as `0-3.0` and `0-0.2`). Error messages use `fmt.Sprintf` with
   the constants for self-consistency.

---

## Integration Tests Added

- `internal/integration/overrides_regression_matrix_test.go` — 200-or-422 matrix covering
  the full override range (no 500s), including negative beta, negative rf, zero WACC,
  near-WACC terminal growth, horizon > 50.
- `internal/api/v1/handlers/fair_value_hardening_test.go` — unit tests for FIX 1 (NaN/Inf
  parseFloatParam), FIX 2 (resolver predicate parity), FIX 3 (range widening).

---

## Cross-Model Review (gpt-5.5-pro, zen-mcp)

Sign-off confirmed: no Critical/High blockers on the fix iterations. Findings addressed:
- MEDIUM-2 (`TestDDM_EVBridge` subtest correctness) — replaced with real multi-stage fixture.
- MEDIUM-3 (FFO DebtLikeClaims invariance) — `TestFFO_IgnoresDebtLikeClaims` added.
- MEDIUM-4 (MRP response field) — `result.MarketRiskPremium` stamped from resolved MRP.
- LOWs — spec wording, docstring, test name corrections.

---

## Spec & Design Reference

- Design spec (including §13 terminal_method reconciliation):
  `docs/refactoring/spec/2026-06-05-request-valuation-overrides-design.md`
- Implementation plan:
  `docs/refactoring/implementations/2026-06-05-request-valuation-overrides-implementation-plan.md`
- This closeout: `docs/refactoring/implementations/2026-06-06-request-valuation-overrides-fix-iterations-closeout.md`
