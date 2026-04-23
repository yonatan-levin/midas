# W-3: SubIndustries Parsed From Config But Never Used in Classify()

| Field | Value |
|-------|-------|
| **ID** | W-3 |
| **Severity** | MEDIUM |
| **Status** | Resolved (2026-04-19) |
| **Found In** | Phase 3 Code Review |
| **File** | `internal/services/datacleaner/industry/classifier.go:27-36` |

## Description

The `industryMapping` struct defines `SubIndustries` with their own `Keywords`, `SICCodes`, `NAICSCodes`, and `Patterns`, but the `Classify()` method only checks top-level matchers. Sub-industry classification (e.g., `TECH_SAAS` vs `TECH`) never happens.

This means the `ev_revenue_multiples` entries in `config/industry_multiples.json` for sub-industries (`TECH_SAAS`, `TECH_AI`, `HEALTH_BIOTECH`, etc.) can never be selected via the classifier — it will only ever return the parent code like `"TECH"` or `"HEALTH"`.

## Impact

- Revenue multiple model uses generic sector multiples (e.g., 5.0x for all tech) instead of more specific ones (8.0x for SaaS, 3.0x for hardware)
- The `industry_codes.json` config has detailed sub-industry definitions that are parsed but wasted
- Less accurate valuations for pre-revenue companies where the multiple matters most

## Recommended Fix

Add a second pass in `Classify()` that, after finding the parent industry, checks sub-industry matchers to return a more specific code:

```go
func (c *IndustryClassifier) Classify(sicCode, naicsCode, companyName string) (string, error) {
    parentIndustry := c.classifyParent(sicCode, naicsCode, companyName)
    if parentIndustry == "" || parentIndustry == "NA" {
        return parentIndustry, nil
    }
    // Second pass: check sub-industries within the parent
    subIndustry := c.classifySubIndustry(parentIndustry, sicCode, naicsCode, companyName)
    if subIndustry != "" {
        return subIndustry, nil // e.g., "TECH_SAAS" instead of "TECH"
    }
    return parentIndustry, nil
}
```

## Acceptance Criteria

- [x] Sub-industry classification produces codes like `TECH_SAAS`, `FIN_IB`, `HEALTH_BIOTECH`
- [x] Revenue multiple model uses sub-industry-specific multiples when available
- [x] Tests verify sub-industry matching from keywords and SIC codes

## Resolution (verified 2026-04-23)

- **Classification:** RESOLVED
- **Commits:** `4d46142` "Resolve reviewer Tier 1+2 follow-ups: W-1, W-2, W-3, W-4, S-5"
- **Evidence:**
  - `internal/services/datacleaner/industry/classifier.go:273-310` (`Classify`) now runs a two-pass algorithm: Pass 1 finds the best parent by priority, Pass 2 delegates to `classifySubIndustry` to refine.
  - `classifier.go:349-367` (`classifySubIndustry`) — walks the matched parent's `SubIndustries` slice and returns sub-codes like `TECH_SAAS`, `FIN_IB`, or `HEALTH_BIOTECH` when any of SIC/NAICS/keyword/pattern matchers fire.
  - `internal/services/valuation/models/revenue_multiple.go:142-169` (`getMultiple`) uses longest-prefix-match on `m.multiples` so sub-industry codes like `TECH_SAAS` (8.0x) take precedence over parent `TECH` (5.0x) when present in the config map.
  - `config/industry_multiples.json:13-29` (`ev_revenue_multiples`) ships concrete sub-industry keys: `TECH_SAAS: 8.0`, `TECH_AI: 10.0`, `HEALTH_BIOTECH: 6.0`, `HEALTH_PHARMA: 4.0`.
  - `classifier_subindustry_test.go` exists and asserts sub-industry matching across keyword and SIC paths.
- **Verification:** Read the full two-pass `Classify` implementation, confirmed `classifySubIndustry` is wired in, and inspected the config JSON plus the dedicated sub-industry test file.
