# S-4: Model Constructors Perform I/O (os.ReadFile)

| Field | Value |
|-------|-------|
| **ID** | S-4 |
| **Severity** | LOW |
| **Status** | Open |
| **Found In** | Phase 3 Code Review |
| **Files** | `ffo.go:34-47`, `revenue_multiple.go:34-49` |

## Description

`NewFFOModel()` and `NewRevenueMultipleModel()` call `os.ReadFile()` during construction to load `industry_multiples.json`. This is unusual for DI-managed objects — constructors should be fast and side-effect-free.

Currently mitigated by `NewFFOModelWithMultiple()` and `NewRevenueMultipleModelWithMultiples()` test-friendly alternatives that skip I/O.

## Impact

- Tests must be run from the correct working directory (project root)
- Constructor failure is silent (falls back to defaults) — could mask config issues
- Related to S-1 (relative paths)

## Recommended Fix

Load config in the DI container and pass it to constructors:

```go
func NewFFOModel(pffoMultiple float64, logger *zap.Logger) *FFOModel
func NewRevenueMultipleModel(multiples map[string]float64, logger *zap.Logger) *RevenueMultipleModel
```

The DI layer loads from config paths and injects the values.

## Acceptance Criteria

- [ ] Constructors accept pre-loaded config values instead of reading files
- [ ] DI container handles file loading
- [ ] Tests use direct value injection
