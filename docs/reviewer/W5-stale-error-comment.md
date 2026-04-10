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

- [ ] Comment updated to reflect Phase 3 reality
