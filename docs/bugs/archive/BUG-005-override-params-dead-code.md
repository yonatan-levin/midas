# BUG-005: override_beta and override_rf query parameters are dead code

| Field | Value |
|-------|-------|
| **ID** | BUG-005 |
| **Title** | override_beta and override_rf parsed by handler but never passed to CalculateValuation |
| **Severity** | MEDIUM |
| **Status** | Resolved (2026-04-05) |
| **Component** | Fair Value Handler / Valuation Service |
| **Reported** | 2026-04-05 |

## Summary

The `GetFairValue` handler at `fair_value.go:104-105` parses `override_beta` and `override_rf` query parameters and logs them, but never passes them to `CalculateValuation(ctx, ticker)` which only accepts ticker. The `CalculateValuation` function has no mechanism to accept overrides. Users cannot customize valuations despite the API documenting these params.

## Steps to Reproduce

```bash
# Request with override_beta=0.5 (should lower WACC)
curl -H "X-API-Key: <key>" "http://localhost:8080/api/v1/fair-value/AAPL?override_beta=0.5"

# Request with override_beta=2.0 (should raise WACC)  
curl -H "X-API-Key: <key>" "http://localhost:8080/api/v1/fair-value/AAPL?override_beta=2.0"

# Both return IDENTICAL WACC — overrides are ignored
```

## Root Cause

`fair_value.go:113`: `result, err := h.valuationService.CalculateValuation(c.Request.Context(), ticker)` — overrides not passed.

## Proposed Fix

1. Add `ValuationOptions` struct with optional override fields
2. Change `CalculateValuation` signature to accept options
3. Apply overrides in WACC calculation when provided
4. Update handler to pass overrides through

## Acceptance Criteria

- [ ] `override_beta=0.5` produces lower WACC than `override_beta=2.0`
- [ ] `override_rf=0.03` vs `override_rf=0.06` changes the WACC
- [ ] Without overrides, behavior is unchanged
- [ ] Existing tests still pass

## Resolution (verified 2026-04-23)

- **Classification**: RESOLVED
- **Fix commit**: `9841939` ("Fix 9 bugs: real-world valuations working end-to-end")
- **Evidence inspected**:
  - `internal/services/valuation/options.go:5-7` — new `ValuationOptions` struct with `*float64` `OverrideBeta` / `OverrideRiskFree`
  - `internal/services/valuation/service.go:121` — `CalculateValuation(ctx, ticker, opts *ValuationOptions)` accepts options; `service.go:127` detects overrides for cache-bypass; `service.go:370-371` applies `OverrideBeta` during WACC computation
  - `internal/api/v1/handlers/fair_value.go:135-162` — handler parses `override_beta` / `override_rf` and forwards them via `&valuation.ValuationOptions{...}`
  - `internal/services/valuation/service_test.go:1487-1557` — `TestService_CalculateValuation_OverrideBeta` and `OverrideRiskFree` tests prove differing overrides produce differing WACC
