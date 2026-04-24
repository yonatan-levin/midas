# BUG-008: Empty ticker route returns non-RFC 7807 error format

| Field | Value |
|-------|-------|
| **ID** | BUG-008 |
| **Title** | GET /api/v1/fair-value/ returns inconsistent error format missing RFC 7807 fields |
| **Severity** | LOW |
| **Status** | Resolved (2026-04-05) |
| **Component** | API Server Routes |
| **Reported** | 2026-04-05 |

## Summary

The empty ticker route at `server.go:191-195` returns `{"error":"ticker parameter is required","code":"INVALID_TICKER"}` which lacks the RFC 7807 fields (type, title, status, detail, instance) used by all other error responses. This inconsistency can break API consumers that expect uniform error structure.

## Steps to Reproduce

```bash
curl http://localhost:8080/api/v1/fair-value/
# Returns: {"error":"ticker parameter is required","code":"INVALID_TICKER"}
# Missing: type, title, status, detail, instance fields

curl http://localhost:8080/api/v1/fair-value/TOOLONG
# Returns proper RFC 7807: {"type":"...","title":"...","status":400,"detail":"...","instance":"..."}
```

## Root Cause

`server.go:191-195` uses inline `gin.H{}` instead of the `respondWithError` helper or the handler's `sendError` method.

## Proposed Fix

Replace the inline response with a call to `respondWithError` using the same format as all other error endpoints.

## Acceptance Criteria

- [ ] `GET /fair-value/` returns RFC 7807 format with type, title, status, detail, instance
- [ ] Status code is 400
- [ ] Error code is INVALID_TICKER

## Resolution (verified 2026-04-23)

- **Classification**: RESOLVED
- **Fix commit**: `9841939` ("Fix 9 bugs: real-world valuations working end-to-end")
- **Evidence inspected**:
  - `internal/api/server.go:189-192` — inline `gin.H{}` replaced with `s.respondWithError(c, http.StatusBadRequest, "INVALID_TICKER", "Ticker parameter is required")`, giving the RFC 7807 shape produced by the same helper used elsewhere
  - `internal/api/server_test.go:986-1010,1358-1364` — tests verify the empty-ticker path returns 400 with `code: INVALID_TICKER` in RFC 7807 form
