# Q3: requestIDMiddleware Registered Twice

**Status:** OPEN
**Severity:** MINOR (production code)
**Found by:** QA (2026-04-14)
**Location:** `internal/api/server.go:75` and `internal/api/server.go:122-130`

## Description

The request ID middleware is registered twice:
1. As a global middleware in `NewServer()` (line 75)
2. Again as an inline anonymous function in `setupMiddleware()` (lines 122-130)

Both do the same thing (generate or pass through X-Request-ID header). This means every request processes the request ID logic twice — the second registration overwrites the first's header value.

## Recommended Fix

Remove the duplicate registration in `setupMiddleware()` (lines 122-130), keeping only the global one at line 75.

## Risk if Not Fixed

Minimal. Causes unnecessary double-processing of every request. No functional impact since the second pass simply overwrites with an equivalent value.
