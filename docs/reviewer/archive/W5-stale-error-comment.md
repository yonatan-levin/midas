# W-5: ErrModelNotApplicable Doc Comment Is Stale

| Field | Value |
|-------|-------|
| **ID** | W-5 |
| **Severity** | LOW |
| **Status** | Open |
| **Found In** | Phase 3 Code Review |
| **File** | `internal/services/valuation/errors.go:17-19` |

## Description

The `ErrModelNotApplicable` comment says: *"Industry-specific models (DDM, FFO, revenue multiples) may handle it in a future phase."* These models now exist (Phase 3).

## Recommended Fix

```go
// ErrModelNotApplicable indicates neither the standard DCF model nor any
// alternative model (DDM, FFO, revenue multiple) could produce a result.
ErrModelNotApplicable = errors.New("model not applicable")
```

## Acceptance Criteria

- [x] Comment updated to reflect Phase 3 reality

## Resolution (verified 2026-04-23)

- **Classification:** RESOLVED
- **Commits:** `01f4db0` "Fix Phase 3 follow-ups: repo persistence, model fallback chain, reviewer nits" (commit message explicitly calls out "W-5: ErrModelNotApplicable comment updated (no longer says 'future')").
- **Evidence:** `internal/services/valuation/errors.go:21-23` now reads: `// ErrModelNotApplicable indicates neither the standard DCF model nor any / alternative model (DDM, FFO, revenue multiple) could produce a result.` — exactly the recommended wording. Grep for `future phase` / `may handle it` in `errors.go` returns no matches.
- **Verification:** Read `errors.go` at HEAD and grepped for the stale phrases.
