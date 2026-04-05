# BUG-001: SEC CompanyFacts struct does not match real API response structure

| Field | Value |
|-------|-------|
| **ID** | BUG-001 |
| **Title** | SEC CompanyFacts Go struct uses flat map but real API returns nested taxonomy -> concept -> factGroup |
| **Severity** | BLOCKER |
| **Status** | Resolved (2026-04-02) |
| **Component** | SEC Gateway / Parser |
| **Reported** | 2026-03-30 |
| **Environment** | Development (Windows 11, Go 1.24.4) |
| **Affects** | Every non-seeded ticker (MSFT, GOOGL, JNJ, etc.) |

## Summary

The `SECCompanyFacts.Facts` field is typed as `map[string]SECFactGroup`, which expects a flat mapping of concept names to fact groups. The real SEC EDGAR CompanyFacts API returns a **two-level nested** structure: `facts -> taxonomy ("us-gaap", "dei") -> concept ("Assets", "Revenues") -> factGroup`. This means the parser only sees 2 top-level keys (the taxonomy namespaces) and extracts zero financial concepts.

## Impact

**All real-world valuations fail.** Any ticker not pre-seeded in the database returns HTTP 500 because the SEC data cannot be parsed. This is the primary reason the product does not work with live data.

## Steps to Reproduce

1. Start the server: `go run cmd/server/main.go`
2. Send a request for any non-seeded ticker:
   ```bash
   curl -H "X-API-Key: dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788" \
     http://localhost:8080/api/v1/fair-value/MSFT
   ```
3. Observe HTTP 500 response:
   ```json
   {
     "type": "https://api.dcf-valuation.com/errors/CALCULATION_ERROR",
     "title": "Valuation calculation failed",
     "status": 500,
     "detail": "An internal error occurred during valuation calculation"
   }
   ```
4. Check server logs — look for:
   ```
   "msg":"Extracted fiscal periods","period_count":0
   ```
   and:
   ```
   "msg":"Parsed financial data","fact_count":2
   ```
   The `fact_count:2` confirms only the taxonomy namespaces ("us-gaap", "dei") are seen, not the hundreds of concepts within them.

## Root Cause Analysis

### Current struct definition (`internal/core/ports/gateways.go:49-53`)
```go
type SECCompanyFacts struct {
    CIK        json.Number             `json:"cik"`
    EntityName string                  `json:"entityName"`
    Facts      map[string]SECFactGroup `json:"facts"`    // <-- FLAT: concept -> factGroup
    FilingDate time.Time               `json:"-"`
}
```

### What the real SEC API returns (simplified)
```json
{
  "cik": 789019,
  "entityName": "MICROSOFT CORP",
  "facts": {
    "dei": {                              // <-- Level 1: taxonomy namespace
      "EntityCommonStockSharesOutstanding": {  // <-- Level 2: concept name
        "label": "Entity Common Stock...",
        "units": {
          "shares": [
            { "end": "2024-06-30", "val": 7433000000, "fy": 2024, "fp": "FY" }
          ]
        }
      }
    },
    "us-gaap": {                          // <-- Level 1: taxonomy namespace
      "Assets": {                         // <-- Level 2: concept name
        "label": "Assets",
        "units": {
          "USD": [
            { "end": "2024-06-30", "val": 512163000000, "fy": 2024, "fp": "FY" }
          ]
        }
      },
      "Revenues": { ... },
      "OperatingIncomeLoss": { ... }
    }
  }
}
```

### What happens during deserialization

When Go unmarshals the JSON into `map[string]SECFactGroup`:
- Key `"us-gaap"` is mapped to a `SECFactGroup`
- But the value for `"us-gaap"` is `{"Assets": {...}, "Revenues": {...}, ...}` — a map of concepts, **not** a single `SECFactGroup` with `Label`, `Description`, `Units`
- Go's JSON decoder silently produces an empty/zero-value `SECFactGroup` (Units = nil)
- The parser enters the `else` branch at line 156 and calls `tryParseNestedFacts()` which always returns `false` (line 379: "Full nested parsing requires schema updates")

### Code path trace

1. `client.go:GetCompanyFacts()` — HTTP GET succeeds, JSON decoded into `SECCompanyFacts`
2. `parser.go:ParseFinancialData()` — calls `extractFiscalPeriods()`
3. `parser.go:132-178 extractFiscalPeriods()` — iterates `facts.Facts`:
   - Key `"dei"`: `taxonomyGroup.Units == nil` → enters `else` branch → `tryParseNestedFacts()` returns `false`
   - Key `"us-gaap"`: same → `tryParseNestedFacts()` returns `false`
   - `periods` map is empty → returns error "no financial periods extracted"
4. Valuation pipeline fails with no financial data

## Affected Files

| File | Lines | Role |
|------|-------|------|
| `internal/core/ports/gateways.go` | 49-53 | Struct definition |
| `internal/infra/gateways/sec/parser.go` | 132-178 | `extractFiscalPeriods` — period extraction logic |
| `internal/infra/gateways/sec/parser.go` | 365-380 | `tryParseNestedFacts` — always returns false |
| `internal/infra/gateways/sec/client.go` | ~60 | `GetCompanyFacts` — JSON deserialization target |

## Proposed Fix

### Option A: Fix the struct type (Recommended)

Change `SECCompanyFacts.Facts` to support the nested structure:

```go
type SECCompanyFacts struct {
    CIK        json.Number                          `json:"cik"`
    EntityName string                               `json:"entityName"`
    Facts      map[string]map[string]SECFactGroup   `json:"facts"`  // taxonomy -> concept -> factGroup
    FilingDate time.Time                             `json:"-"`
}
```

Then update `extractFiscalPeriods` to iterate both levels:

```go
for taxonomy, concepts := range facts.Facts {
    for conceptName, factGroup := range concepts {
        if usdUnits, exists := factGroup.Units["USD"]; exists {
            p.processFacts(periods, conceptName, usdUnits)
        }
        if sharesUnits, exists := factGroup.Units["shares"]; exists {
            p.processFacts(periods, conceptName, sharesUnits)
        }
    }
}
```

### Option B: Use `json.RawMessage` for flexible parsing

Keep the existing struct but use `map[string]json.RawMessage` for `Facts` and add a custom parser that detects the nesting level.

### Recommendation

Option A is simpler and directly models the real API structure. Option B is more flexible but adds complexity.

## Regression Risks

- All existing tests that mock `SECCompanyFacts` with the flat structure will need updating
- The `convertFactsToMap` function in `gateway.go` references the old structure
- Any code that directly accesses `facts.Facts["conceptName"]` must add the taxonomy level

## Acceptance Criteria

- [ ] `GET /api/v1/fair-value/MSFT` returns HTTP 200 with non-zero financial values
- [ ] Server logs show `fact_count` > 50 (not 2) after parsing
- [ ] Server logs show `period_count` > 0 after extracting fiscal periods
- [ ] Existing unit tests pass (updated to new struct format)
- [ ] New integration test: fetch real SEC data for CIK 789019 (MSFT), verify at least Revenue, Assets, and OperatingIncome are extracted

## References

- SEC EDGAR CompanyFacts API: `https://data.sec.gov/api/xbrl/companyfacts/CIK0000789019.json`
- SEC API documentation: `https://www.sec.gov/edgar/sec-api-documentation`
