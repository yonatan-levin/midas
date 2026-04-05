# BUG-002: Yahoo Finance API returns 401 Unauthorized — no working market data source

| Field | Value |
|-------|-------|
| **ID** | BUG-002 |
| **Title** | Yahoo Finance v7/v10 API requires authentication; all market data requests fail with 401 |
| **Severity** | BLOCKER |
| **Status** | Resolved (2026-04-05) |
| **Component** | Market Data Gateway / YFinance Client |
| **Reported** | 2026-03-30 |
| **Environment** | Development (Windows 11, Go 1.24.4) |
| **Affects** | Every non-seeded ticker — no market price, beta, or shares outstanding |

## Summary

The Yahoo Finance unofficial API (`query1.finance.yahoo.com`) now requires authentication (either a RapidAPI key or cookie-based auth with crumb tokens). The Midas YFinance client sends only a generic `User-Agent` header with no authentication credentials. All requests to `/v7/finance/quote` and `/v10/finance/quoteSummary` return HTTP 401 with `"User is unable to access this feature"`. The Finzive fallback is stubbed but not implemented. This means **no market data can be fetched for any ticker**.

## Impact

Without market data, the valuation pipeline cannot compute:
- **WACC** (requires beta, market cap for equity weight)
- **DCF per share** (requires shares outstanding)
- **Data freshness score** (requires market data timestamp)

Combined with BUG-001 (SEC struct mismatch), this makes all real-world valuations impossible. Even if BUG-001 is fixed, valuations will still fail without market data.

## Steps to Reproduce

1. Start the server: `go run cmd/server/main.go`
2. Request fair value for any non-seeded ticker:
   ```bash
   curl -H "X-API-Key: dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788" \
     http://localhost:8080/api/v1/fair-value/MSFT
   ```
3. Observe HTTP 500 response
4. Check server logs for the Yahoo Finance error chain:
   ```
   "msg":"Quote request failed"
   "error":"quote request failed with status 401: ..."
   "msg":"Failed to fetch from yfinance, trying fallback"
   "msg":"failed to fetch market data for MSFT from all sources"
   ```
5. Confirm the 401 by calling Yahoo Finance directly:
   ```bash
   curl -v "https://query1.finance.yahoo.com/v7/finance/quote?symbols=MSFT&fields=regularMarketPrice"
   ```
   Response: `401 Unauthorized` with `{"finance":{"result":null,"error":{"code":"Unauthorized","description":"User is unable to access this feature"}}}`

## Root Cause Analysis

### Yahoo Finance API change

Yahoo Finance deprecated unauthenticated access to their v7/v10 JSON endpoints. The API now requires either:
1. **RapidAPI subscription** — paid API key via `x-rapidapi-key` header at `yfapi.net`
2. **Cookie + Crumb authentication** — session cookie from `fc.yahoo.com` + crumb token from `query2.finance.yahoo.com/v1/test/getcrumb`
3. **Alternative endpoints** — some endpoints at `query2.finance.yahoo.com` with different auth requirements

### Current implementation (`internal/infra/gateways/market/yfinance_client.go`)

```go
// Line 52: Uses the v7 endpoint which requires auth
endpoint := fmt.Sprintf("%s/v7/finance/quote", c.baseURL)

// Lines 265, 294, 323: Only sends a generic User-Agent, no auth
req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Midas/1.0)")
req.Header.Set("Accept", "application/json")
```

No authentication headers, cookies, or crumb tokens are set.

### Fallback chain failure

The market gateway (`internal/infra/gateways/market/gateway.go`) has a fallback pattern:

```go
// gateway.go:46-68 (simplified)
func (g *Gateway) GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error) {
    // Try YFinance first
    quote, err := g.yfinanceClient.GetQuote(ctx, ticker)
    if err == nil {
        return convertToMarketData(quote), nil
    }
    g.logger.Warn("Failed to fetch from yfinance, trying fallback")

    // Finzive fallback — NOT IMPLEMENTED
    // The FinziveGateway interface exists in ports but has no concrete implementation
    return nil, fmt.Errorf("failed to fetch market data for %s from all sources", ticker)
}
```

The Finzive fallback is defined as an interface in `internal/core/ports/gateways.go:130-140` but has **no implementation**. The market gateway has no other fallback sources.

### Retry behavior

The client retries 3 times with 1-second delays between attempts (lines 64-80 in `yfinance_client.go`). All 3 attempts fail with 401 since the auth issue is permanent, not transient.

## Affected Files

| File | Lines | Role |
|------|-------|------|
| `internal/infra/gateways/market/yfinance_client.go` | 47-102 | `GetQuote` — makes unauthenticated requests |
| `internal/infra/gateways/market/yfinance_client.go` | 257-267 | `makeQuoteRequest` — no auth headers |
| `internal/infra/gateways/market/yfinance_client.go` | 139-149 | `GetKeyStatistics` — same auth issue |
| `internal/infra/gateways/market/yfinance_client.go` | 200-254 | `GetHistoricalPrices` — same auth issue |
| `internal/infra/gateways/market/gateway.go` | 46-68 | `GetQuote` — fallback chain with no working fallback |
| `internal/config/config.go` | 79-85 | `YFinanceConfig` — no auth configuration fields |
| `internal/core/ports/gateways.go` | 130-140 | `FinziveGateway` — interface only, no implementation |

## Proposed Fix Options

### Option A: Implement Cookie + Crumb Authentication (Recommended for free tier)

Mimic the `yfinance` Python library's approach:
1. GET `https://fc.yahoo.com` to obtain session cookies
2. GET `https://query2.finance.yahoo.com/v1/test/getcrumb` with the session cookie to get a crumb token
3. Include the session cookie and `&crumb={crumb}` parameter in all subsequent requests
4. Cache the cookie/crumb pair and refresh when expired (typically ~24h)
5. Switch base URL to `query2.finance.yahoo.com`

```go
type YFinanceAuth struct {
    Cookie    string
    Crumb     string
    ExpiresAt time.Time
    mu        sync.RWMutex
}
```

### Option B: Add RapidAPI Support (Recommended for production)

1. Add `APIKey` field to `YFinanceConfig`
2. Route requests through `yfapi.net` with `x-rapidapi-key` header
3. Add config env var: `MARKET_YFINANCE_API_KEY`
4. This is the most reliable option but requires a paid subscription

### Option C: Implement Finzive Fallback (Alternative source)

1. Implement the existing `FinziveGateway` interface with a web scraper
2. Parse financial data from `https://finzive.com/stock/{ticker}`
3. Wire into the market gateway's fallback chain
4. Slower but free and doesn't require API keys

### Option D: Use Financial Modeling Prep (FMP) API

1. Free tier: 250 requests/day
2. Add as an alternative market data gateway
3. Well-documented REST API with standard JSON responses
4. Add `FMPConfig` to the config struct

### Recommendation

**Short-term**: Option A (cookie + crumb) — free, proven approach used by `yfinance` Python library. Add caching for auth tokens to minimize overhead.

**Long-term**: Option B (RapidAPI) or Option D (FMP) — more reliable and officially supported.

## Regression Risks

- Existing `yfinance_client_test.go` uses mock HTTP servers and will continue to pass regardless of auth changes
- Need to add integration test that verifies at least one market data source returns a valid quote
- Cookie/crumb refresh logic must handle concurrent access (the auth token renewal race condition)

## Acceptance Criteria

- [ ] `GET /api/v1/fair-value/MSFT` successfully retrieves market data (share price, beta, market cap)
- [ ] Server logs show successful market data fetch (no 401 errors)
- [ ] At least one market data source is fully functional
- [ ] Fallback chain works: if primary source fails, secondary is attempted
- [ ] Configuration allows switching between market data sources via env vars
- [ ] New integration test: fetch market data for AAPL, verify share_price > 0 and beta > 0

## References

- Yahoo Finance API change discussion: Search "yahoo finance api 401 unauthorized 2024"
- `yfinance` Python library auth implementation: `github.com/ranaroussi/yfinance` (see `base.py` for cookie/crumb flow)
- Financial Modeling Prep API: `https://financialmodelingprep.com/developer/docs/`
- RapidAPI Yahoo Finance: `https://rapidapi.com/sparior/api/yahoo-finance15`
