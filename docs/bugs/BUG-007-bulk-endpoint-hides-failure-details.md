# BUG-007: Bulk endpoint returns 200 with no per-ticker error details

| Field | Value |
|-------|-------|
| **ID** | BUG-007 |
| **Title** | Bulk fair value endpoint hides per-ticker failure reasons — consumers cannot diagnose errors |
| **Severity** | MEDIUM |
| **Status** | Resolved (2026-04-05) |
| **Component** | Fair Value Handler |
| **Reported** | 2026-04-05 |

## Summary

When tickers fail in `POST /api/v1/fair-value/bulk`, the response only includes a count of failures in the summary. There are no error messages or error codes per ticker. When all tickers fail, the response is still 200 OK with an empty results array. Consumers cannot distinguish between "ticker not found", "insufficient data", and "external API failure".

## Steps to Reproduce

```bash
curl -X POST -H "X-API-Key: <key>" -H "Content-Type: application/json" \
  -d '{"tickers":["AAPL","INVALID_TICKER","ZZZZ"]}' \
  http://localhost:8080/api/v1/fair-value/bulk

# Returns:
# {"results":[{...AAPL...}],"summary":{"total_requested":3,"successful":1,"failed":2}}
# No info about WHY the 2 tickers failed
```

## Proposed Fix

1. Add a `failures` array to `BulkFairValueResponse` with ticker, error_code, error_message per failed ticker
2. Consider returning 207 Multi-Status when there are partial failures
3. When ALL tickers fail, return 422 instead of 200 with empty results

## Acceptance Criteria

- [ ] Bulk response includes per-ticker error details for failures
- [ ] Each failure has ticker, error_code, and human-readable message
- [ ] Partial success still returns 200 with both results and failures
- [ ] All-failures returns appropriate status code (not 200 with empty results)
