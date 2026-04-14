# Q2: authMiddleware Uses switch Instead of errors.Is()

**Status:** OPEN
**Severity:** MINOR (production code)
**Found by:** QA (2026-04-14)
**Location:** `internal/api/server.go:403-411`

## Description

The `authMiddleware` uses `switch err { case auth.ErrKeyNotFound:` for sentinel error comparison instead of `errors.Is()`. This works today because the auth service returns unwrapped sentinel errors, but is fragile. If the auth service is ever refactored to wrap these errors (e.g., `fmt.Errorf("context: %w", ErrKeyNotFound)`), the switch would break silently, routing all errors to the AUTH_005 default case.

## Recommended Fix

Replace the switch with if/else using `errors.Is()`:

```go
if errors.Is(err, auth.ErrKeyNotFound) {
    s.respondWithError(c, http.StatusUnauthorized, "AUTH_002", "Invalid API key")
} else if errors.Is(err, auth.ErrKeyExpired) {
    s.respondWithError(c, http.StatusUnauthorized, "AUTH_003", "API key has expired")
} else if errors.Is(err, auth.ErrKeyInactive) {
    s.respondWithError(c, http.StatusUnauthorized, "AUTH_004", "API key is inactive")
} else {
    s.respondWithError(c, http.StatusInternalServerError, "AUTH_005", "Authentication service error")
}
```

## Risk if Not Fixed

Low today. Becomes a silent bug if auth error wrapping is introduced. Follows the same pattern as `fair_value.go` which already uses `errors.Is()` for valuation errors.
