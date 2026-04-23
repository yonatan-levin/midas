# BUG-004: DataFetcher coordinator extracts only 1 period with incomplete fields — valuation always fails

| Field | Value |
|-------|-------|
| **ID** | BUG-004 |
| **Title** | Coordinator's simplified SEC extraction returns single period with missing Revenue/OI/Shares — causes "insufficient financial data" |
| **Severity** | BLOCKER |
| **Status** | Resolved (2026-04-05) |
| **Component** | DataFetcher Coordinator / Valuation Service |
| **Reported** | 2026-04-05 |
| **Environment** | Development (Windows 11, Go 1.24.4) |
| **Affects** | Every non-seeded ticker — no real-world valuation can complete end-to-end |
| **Depends on** | BUG-001 (Resolved), BUG-002 (Resolved), BUG-003 (Resolved) |

## Summary

With BUG-001, BUG-002, and BUG-003 all resolved, the SEC data is fetched and parsed (546 concepts for MSFT), market data is fetched (price=$373.46), and the valuation service correctly uses the FetchResult. However, the **DataFetcher coordinator** uses a simplified extraction path (`extractLatestUSDValue`) that only pulls TotalAssets and Revenue from the generic `map[string]interface{}` — and even that fails silently because the SEC concept name for Microsoft's revenue is `RevenueFromContractWithCustomerExcludingAssessedTax`, not `Revenues`. The result is a single `FinancialData` record with zero Revenue, zero OperatingIncome, and zero SharesOutstanding — which causes `HasMinimumData(1)` to reject it.

Meanwhile, the full SEC parser (`Parser.ParseFinancialData`) already handles all of this correctly — it tries multiple concept name alternatives, extracts multiple fiscal periods, and populates all required fields. **The coordinator simply doesn't use the parser.**

## Impact

**No real-world valuation can complete.** This is the last remaining blocker. All external data is now fetched correctly (SEC, Yahoo Finance, FRED), but the coordinator's simplified extraction produces unusable financial data.

## Steps to Reproduce

1. Start the server:
   ```bash
   go run cmd/server/main.go
   ```

2. Request any non-seeded ticker:
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

4. Check server logs — you'll see:
   ```
   "msg":"Successfully fetched company facts","cik":"789019",
   "entity_name":"MICROSOFT CORPORATION","taxonomy_count":2,"concept_count":546
   ```
   Then:
   ```
   "msg":"Successfully fetched data via DataFetcher","ticker":"MSFT",
   "periods":1,"has_market_data":true,"has_macro_data":true
   ```
   Then the failure:
   ```
   "msg":"Valuation calculation failed","ticker":"MSFT",
   "error":"insufficient financial data: need at least 1 year of data"
   ```

5. The `periods:1` with "insufficient" means the single period has Revenue=0 AND OperatingIncome=0 AND SharesOutstanding=0, so `HasMinimumData(1)` rejects it at `financial_data.go:222-227`.

## Root Cause Analysis

There are **three interacting problems**:

### Problem 1: Coordinator uses simplified extraction instead of the full parser

The SEC gateway returns `*entities.CompanyFactsResponse` with `Facts map[string]interface{}`. The coordinator at `coordinator.go:240-257` does its own simplified extraction:

```go
financialData.TotalAssets = extractLatestUSDValue(companyFacts.Facts, "us-gaap", "Assets")
financialData.Revenue = extractLatestUSDValue(companyFacts.Facts, "us-gaap", "Revenues")
if financialData.Revenue == 0 {
    financialData.Revenue = extractLatestUSDValue(companyFacts.Facts, "us-gaap",
        "RevenueFromContractWithCustomerExcludingAssessedTax")
}
```

This only extracts TotalAssets and Revenue. It completely misses:
- **OperatingIncome** (`OperatingIncomeLoss`)
- **SharesOutstanding** (`CommonStockSharesOutstanding` or `EntityCommonStockSharesOutstanding` in "dei" taxonomy)
- **InterestExpense**, **TotalDebt**, **Goodwill**, **OtherIntangibles**, **TaxRate**
- **Multiple fiscal periods** (only takes the last entry, not historical data)

Meanwhile, the full parser at `parser.go:140-155` correctly handles all of these with proper concept name fallbacks and multi-period extraction.

### Problem 2: The coordinator path bypasses the typed SEC struct

The flow is:
```
SEC Client → GetCompanyFacts() → *ports.SECCompanyFacts (typed struct)
     ↓
Gateway → convertFactsToMap() → *entities.CompanyFactsResponse (map[string]interface{})
     ↓
Coordinator → extractLatestUSDValue() → single FinancialData (lossy)
```

The typed struct `*ports.SECCompanyFacts` is available at the SEC Client level, but the SECGateway interface (`GetCompanyFacts`) returns `*entities.CompanyFactsResponse` with the lossy `map[string]interface{}` representation. The coordinator receives this lossy form and tries to navigate it with type assertions.

The full parser (`Parser.ParseFinancialData`) operates on `*ports.SECCompanyFacts` — the typed struct — and correctly extracts everything. But **the coordinator never calls the parser**.

### Problem 3: `HasMinimumData` rejects data with zero Revenue AND zero OperatingIncome

From `financial_data.go:222-224`:
```go
if data.Revenue <= 0 && data.OperatingIncome <= 0 {
    return false
}
```

Since the coordinator sets neither Revenue (concept name mismatch for most companies) nor OperatingIncome (not extracted at all), the period is rejected even though it technically exists.

## Affected Files

| File | Lines | Role |
|------|-------|------|
| `internal/services/datafetcher/coordinator.go` | 240-257 | Simplified extraction — misses most fields |
| `internal/services/datafetcher/coordinator.go` | 292-345 | `extractLatestUSDValue` — only gets 1 value, no history |
| `internal/infra/gateways/sec/gateway.go` | 34-48 | `GetCompanyFacts` returns lossy `CompanyFactsResponse` |
| `internal/infra/gateways/sec/gateway.go` | 84-125 | `GetFinancialDataForTicker` — full parser path (unused by coordinator) |
| `internal/infra/gateways/sec/parser.go` | 130-178 | Full parser — handles all concepts and multi-period |
| `internal/services/valuation/service.go` | 85-106 | Wraps single FinancialData into 1-period historical |
| `internal/core/entities/financial_data.go` | 214-231 | `HasMinimumData` rejects zero Revenue+OI |

## Proposed Fix

### Option A: Coordinator calls the full parser (Recommended)

The SEC gateway already has `GetFinancialDataForTicker(ctx, ticker, cik)` which:
1. Fetches the typed `*ports.SECCompanyFacts`
2. Calls `Parser.ParseFinancialData()` for multi-period extraction
3. Calls `Parser.NormalizeFinancialData()` for each period
4. Returns `*entities.HistoricalFinancialData` with all periods and all fields

Change the coordinator's `fetchSECData` to:
1. Resolve ticker → CIK (already done)
2. Call `secGateway.GetFinancialDataForTicker(ctx, ticker, cik)` instead of `GetCompanyFacts`
3. Return the full `HistoricalFinancialData` instead of a single `FinancialData`

This requires:
- Adding `GetFinancialDataForTicker` to the `ports.SECGateway` interface
- Changing `FetchResult.FinancialData` from `*FinancialData` to `*HistoricalFinancialData` (or adding a new field)
- Updating the valuation service to use the multi-period data directly

### Option B: Expand the coordinator's simplified extraction

Add missing concepts to `extractLatestUSDValue` calls:
```go
financialData.OperatingIncome = extractLatestUSDValue(facts, "us-gaap", "OperatingIncomeLoss")
financialData.SharesOutstanding = extractLatestUSDValue(facts, "dei", "EntityCommonStockSharesOutstanding")
financialData.InterestExpense = extractLatestUSDValue(facts, "us-gaap", "InterestExpense")
financialData.TotalDebt = extractLatestUSDValue(facts, "us-gaap", "LongTermDebt")
financialData.Goodwill = extractLatestUSDValue(facts, "us-gaap", "Goodwill")
financialData.TaxRate = extractLatestUSDValue(facts, "us-gaap", "EffectiveIncomeTaxRateConti...")
```

Pros: Minimal change, unblocks valuation quickly.
Cons: Still only 1 period (no growth rate from history), duplicates concept-name logic already in the parser, misses edge cases the parser handles.

### Option C: Hybrid — expand extraction + add multi-period support

Like Option B, but also iterate multiple FY entries from the USD arrays to build multi-period data.

### Recommendation

**Option A** is architecturally correct. The parser already handles all edge cases (concept name fallbacks, multi-period extraction, normalization). Duplicating this in the coordinator is a maintenance burden. The gateway's `GetFinancialDataForTicker` method was designed for exactly this use case.

**Option B** is a quick unblock if Option A requires too many interface changes.

## Regression Risks

- **Option A**: Changes the `SECGateway` interface (adds method), affecting all implementations and mocks
- **Option B**: No interface changes, but adds tech debt (duplicate concept mapping logic)
- Both options: Existing tests use mock gateways and won't exercise the real SEC parser path

## Acceptance Criteria

- [ ] `GET /api/v1/fair-value/MSFT` returns HTTP 200 with non-zero `dcf_value_per_share`
- [ ] The `FinancialData` has Revenue > 0, OperatingIncome > 0, and SharesOutstanding > 0
- [ ] Server logs show successful valuation completion (no "insufficient financial data")
- [ ] At least 1 annual period with valid financial data (ideally 3+ for growth rate)
- [ ] Market data (price, beta) from BUG-002 fix is correctly used in WACC calculation
- [ ] Integration test: fetch MSFT end-to-end, verify all response fields are non-zero

## How to Verify the Root Cause

Add temporary debug logging to the coordinator after extraction:

```go
s.logger.Debug("Coordinator extraction result",
    zap.Float64("total_assets", financialData.TotalAssets),
    zap.Float64("revenue", financialData.Revenue),
    zap.Float64("operating_income", financialData.OperatingIncome),
    zap.Float64("shares_outstanding", financialData.SharesOutstanding),
)
```

Expected output: TotalAssets > 0 (usually works), Revenue = 0 (concept name mismatch for many companies), OperatingIncome = 0 (never extracted), SharesOutstanding = 0 (never extracted).

## Related Bugs

- **BUG-001** (Resolved): SEC struct type was flat — now correctly nested
- **BUG-002** (Resolved): Yahoo Finance 401 — now has cookie+crumb auth  
- **BUG-003** (Resolved): DataFetcher didn't persist — valuation now uses FetchResult directly
- **BUG-004** (This bug): Coordinator's simplified extraction produces unusable data

## References

- Full parser with correct concept mappings: `internal/infra/gateways/sec/parser.go:200-260`
- Gateway's `GetFinancialDataForTicker`: `internal/infra/gateways/sec/gateway.go:84-125`
- SEC EDGAR CompanyFacts for MSFT: `https://data.sec.gov/api/xbrl/companyfacts/CIK0000789019.json`
- Microsoft uses `RevenueFromContractWithCustomerExcludingAssessedTax` (not `Revenues`)
- Microsoft uses `OperatingIncomeLoss` (not `OperatingIncome`)
- Shares are in `dei` taxonomy: `EntityCommonStockSharesOutstanding`

## Resolution (verified 2026-04-23)

- **Classification**: RESOLVED (Option A — coordinator calls the full parser)
- **Fix commit**: `9841939` ("Fix 9 bugs: real-world valuations working end-to-end")
- **Evidence inspected**:
  - `internal/services/datafetcher/coordinator.go:204-253` — `fetchSECData` now resolves ticker->CIK and calls `dc.secGateway.GetFinancialDataForTicker(ctx, ticker, identifier)`, returning `*entities.HistoricalFinancialData` with all FY periods and normalized fields
  - `internal/services/datafetcher/coordinator.go:141` — `coordinateConcurrent` stores into `result.historicalData` (multi-period), not a single `FinancialData`
  - `internal/services/valuation/service.go:166-178` — valuation service prefers `fetchResult.HistoricalData` over the single-period fallback
  - `internal/services/datafetcher/coordinator_test.go:38,77` — tests exercise `fetchSECData` + `fakeSECGateway.GetFinancialDataForTicker`
