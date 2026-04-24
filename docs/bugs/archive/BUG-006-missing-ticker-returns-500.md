# BUG-006: Non-existent tickers return 500 instead of 404

| Field | Value |
|-------|-------|
| **ID** | BUG-006 |
| **Title** | Valid-format but non-existent tickers return CALCULATION_ERROR (500) instead of TICKER_NOT_FOUND (404) |
| **Severity** | MEDIUM |
| **Status** | Resolved (2026-04-05) |
| **Component** | Fair Value Handler / Valuation Service |
| **Reported** | 2026-04-05 |

## Summary

When a ticker like `XYZA1` has valid format (1-5 chars) but doesn't exist in SEC data, the error message from the valuation service is `"failed to fetch financial data: no historical data found for ticker XYZA1"`. The handler at `fair_value.go:120` checks `strings.Contains(err.Error(), "not found")` to classify as 404. However, the string `"no historical data found"` does NOT contain the substring `"not found"` — `"found"` is present but `"not found"` as a contiguous substring is not. This causes the handler to fall through to the 500 catch-all.

## Steps to Reproduce

```bash
curl -H "X-API-Key: <key>" http://localhost:8080/api/v1/fair-value/XYZA1
# Returns: 500 CALCULATION_ERROR
# Expected: 404 TICKER_NOT_FOUND
```

## Root Cause

String mismatch between error message and handler check:
- `valuation/service.go`: `"no historical data found for ticker %s"` — contains "found" not "not found"
- `fair_value.go:120`: `strings.Contains(err.Error(), "not found")` — doesn't match

## Proposed Fix

Use typed sentinel errors instead of fragile string matching. Define `ErrTickerNotFound` in the valuation package and check with `errors.Is()`.

## Acceptance Criteria

- [ ] `GET /fair-value/XYZA1` returns HTTP 404 with `TICKER_NOT_FOUND` code
- [ ] `GET /fair-value/AAPL` still returns HTTP 200 (no regression)
- [ ] Error response follows RFC 7807 format

## Resolution (verified 2026-04-23)

- **Classification**: RESOLVED
- **Fix commits**: `9841939` (sentinel errors introduced), later refined by `e1c9f1f` (XRTX/foreign-issuer 422 path)
- **Evidence inspected**:
  - `internal/services/valuation/errors.go:14-23` — exports `ErrTickerNotFound`, `ErrInsufficientData`, `ErrModelNotApplicable` sentinels
  - `internal/services/valuation/service.go:150,188` — wraps `ErrTickerNotFound` when SEC has no data at all; wraps `ErrInsufficientData` at `service.go:160,186` when US-GAAP XBRL is missing
  - `internal/services/valuation/service_test.go:1593-1620` — `TestService_CalculateValuation_TickerNotFound` asserts `errors.Is(err, ErrTickerNotFound)`
  - Handler uses `errors.Is()` (via `classifyBulkError` in `fair_value.go:371-397`) — no more fragile `strings.Contains` on "not found"
