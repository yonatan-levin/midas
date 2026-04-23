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

- [x] Regexes pre-compiled during config loading
- [x] `Classify()` uses cached regexes, no `regexp.Compile` calls
- [x] All classifier tests still pass
- [x] Benchmark: `Classify()` is faster after fix (optional but nice to verify)

## Resolution (verified 2026-04-23)

- **Classification:** RESOLVED
- **Commits:** `4d46142` "Resolve reviewer Tier 1+2 follow-ups: W-1, W-2, W-3, W-4, S-5"
- **Evidence:**
  - `internal/services/datacleaner/industry/classifier.go:27-28` and `:48-49` — `industryMapping` and `subIndustryMapping` now carry `compiledKeywords []*regexp.Regexp` and `compiledPatterns []*regexp.Regexp` cache slices.
  - `classifier.go:215-226` (`compileCodesConfig`) — pre-compiles all parent and sub-industry regexes at config load time.
  - `classifier.go:178-192` (`LoadIndustryCodesConfig`) — invokes `compileCodesConfig` after JSON unmarshal.
  - `classifier.go:334-340` — `matchesParent` uses `mapping.compiledKeywords` / `mapping.compiledPatterns` cached regexes, no runtime `regexp.Compile` in hot path.
  - The only remaining `regexp.Compile` calls (lines 237, 251) live inside `compileKeywordRegexes` / `compilePatternRegexes`, which run once at config load.
  - Tests: `classifier_subindustry_test.go:70-90` asserts compiled slice length parity with input keywords/patterns, proving the cache is populated at load.
- **Verification:** Read `classifier.go` end-to-end, grepped for `regexp.Compile` (only 2 occurrences, both in compile helpers), and read the subindustry test asserting compilation.
