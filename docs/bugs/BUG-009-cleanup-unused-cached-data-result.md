# BUG-009: CachedDataResult entity is unused after DataFetcher cache removal

| Field | Value |
|-------|-------|
| **ID** | BUG-009 |
| **Title** | CachedDataResult struct in data_fetcher.go is dead code after DataFetcher cache was removed |
| **Severity** | LOW |
| **Status** | Resolved (2026-04-05) |
| **Component** | Core Entities |
| **Reported** | 2026-04-05 |

## Summary

The `CachedDataResult` struct at `internal/core/entities/data_fetcher.go:70-78` was used by the DataFetcher's cache layer. That cache was removed (replaced by valuation-level caching + per-data-type caching). The struct is now unreferenced dead code.

## Steps to Reproduce

```bash
grep -r "CachedDataResult" --include="*.go" .
# Only appears in its own definition — zero usage
```

## Proposed Fix

Delete the `CachedDataResult` struct definition. Also clean up related cache status constants if unused.

## Acceptance Criteria

- [ ] `CachedDataResult` struct removed from `data_fetcher.go`
- [ ] `go build ./...` passes
- [ ] No references to `CachedDataResult` in any `.go` file
