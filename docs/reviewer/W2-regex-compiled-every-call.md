# W-2: Regex Compiled on Every Classify() Call

| Field | Value |
|-------|-------|
| **ID** | W-2 |
| **Severity** | MEDIUM |
| **Status** | Resolved (2026-04-19) |
| **Found In** | Phase 3 Code Review |
| **File** | `internal/services/datacleaner/industry/classifier.go:215, 232` |

## Description

`Classify()` calls `regexp.Compile()` inside the inner loop for every keyword match and pattern match. For short keywords (line 215), this compiles `\b...\b` word-boundary regexes. For patterns (line 232), it compiles the full regex string.

Since `Classify()` is called once per valuation request (not in a tight loop), this is not a production blocker, but it violates Go best practices for regex usage.

## Impact

- Unnecessary CPU allocation on every valuation request
- Would become a real bottleneck if batch processing or scheduler runs many valuations
- Go regex compilation is not trivial — involves parsing, NFA construction, allocation

## Recommended Fix

Pre-compile the word-boundary regexes during `LoadIndustryCodesConfig()` and cache them on the `industryMapping` struct:

```go
type industryMapping struct {
    // ... existing fields ...
    compiledKeywords []*regexp.Regexp // pre-compiled from Keywords
    compiledPatterns []*regexp.Regexp // pre-compiled from Patterns
}
```

Compile once at config load time, reuse on every `Classify()` call.

## Acceptance Criteria

- [ ] Regexes pre-compiled during config loading
- [ ] `Classify()` uses cached regexes, no `regexp.Compile` calls
- [ ] All classifier tests still pass
- [ ] Benchmark: `Classify()` is faster after fix (optional but nice to verify)
