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

- [ ] Sub-industry classification produces codes like `TECH_SAAS`, `FIN_IB`, `HEALTH_BIOTECH`
- [ ] Revenue multiple model uses sub-industry-specific multiples when available
- [ ] Tests verify sub-industry matching from keywords and SIC codes
