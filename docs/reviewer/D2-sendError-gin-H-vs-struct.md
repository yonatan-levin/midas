# D2: sendError Uses gin.H Instead of ErrorResponse Struct

**Status:** OPEN  
**Severity:** WARNING  
**Found by:** REVIEWER + Superpowers Code-Reviewer (2026-04-13)  
**Location:** `internal/api/v1/handlers/fair_value.go:388-402`

## Description

`sendError` emits error responses using `gin.H` (a `map[string]interface{}`), but the `ErrorResponse` struct exists with matching fields. This creates two issues:

1. **No compile-time safety**: If a field is added to `ErrorResponse` but not to the `gin.H` map (or vice versa), there is no compiler warning.
2. **Timestamp type mismatch**: `sendError` passes `time.Now().UTC()` (a `time.Time`), but `ErrorResponse.Timestamp` is declared as `string`. This works because Go's JSON marshaller serializes `time.Time` as RFC 3339, but it's implicit.

## Recommended Fix

Option A (preferred): Use the `ErrorResponse` struct directly in `sendError`:
```go
func (h *FairValueHandler) sendError(...) {
    c.Header("Content-Type", "application/problem+json")
    c.JSON(status, ErrorResponse{
        Type:      "https://problems.midas.dev/" + errorType,
        Title:     title,
        Status:    status,
        Detail:    detail,
        Instance:  c.Request.URL.Path,
        Context:   ctx,
        Code:      errorType,
        Timestamp: time.Now().UTC().Format(time.RFC3339),
        Method:    c.Request.Method,
    })
    c.Abort()
}
```

Option B: Keep `gin.H` but explicitly format timestamp:
```go
"timestamp": time.Now().UTC().Format(time.RFC3339),
```

## Risk if Not Fixed

Low — works correctly today. Risk is maintenance drift between struct and map if either is modified independently.
