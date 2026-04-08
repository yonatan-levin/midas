# CONTRACTS.md - API Contracts & Service Interfaces

This document defines all API contracts, message schemas, and service interfaces for the Midas DCF Valuation API.

## REST API Contracts

**Base URL**: `http://localhost:8080`
**OpenAPI Spec**: `docs/openapi.yaml` (OpenAPI 3.0.3)
**Authentication**: `X-API-Key` header on all protected endpoints

### Error Response Format (RFC 7807)

All error responses follow the [Problem Details](https://datatracker.ietf.org/doc/html/rfc7807) standard:

```json
{
  "type": "https://problems.midas.dev/{ERROR_CODE}",
  "title": "HTTP Status Text",
  "status": 401,
  "detail": "Human-readable error description",
  "instance": "/api/v1/fair-value/AAPL",
  "code": "AUTH_002",
  "timestamp": "2025-01-31T10:30:00Z",
  "method": "GET"
}
```

**Content-Type**: `application/problem+json`

### Error Codes

| Code | HTTP Status | Meaning |
|------|------------|---------|
| `AUTH_001` | 401 | Missing API key |
| `AUTH_002` | 401 | Invalid API key |
| `AUTH_003` | 401 | API key expired |
| `AUTH_004` | 401 | API key inactive |
| `AUTH_005` | 500 | Authentication service error |
| `AUTH_006` | 401 | No authentication information |
| `AUTH_007` | 500 | Invalid authentication information |
| `AUTH_008` | 403 | Insufficient permissions |
| `INVALID_TICKER` | 400 | Empty or invalid ticker |
| `MODEL_NOT_APPLICABLE` | 422 | Standard DCF not applicable (e.g., negative operating income) |
| `RATE_LIMIT_EXCEEDED` | 429 | Rate limit exceeded |

---

## Endpoint Contracts

### 1. GET /api/v1/fair-value/{ticker}

**Auth**: Required (`read:fair_value` permission)

**Path Parameters**:
| Name | Type | Required | Constraints |
|------|------|----------|-------------|
| `ticker` | string | Yes | 1-5 characters, uppercase stock ticker |

**Query Parameters**:
| Name | Type | Required | Description |
|------|------|----------|-------------|
| `override_beta` | float | No | Override calculated beta value |
| `override_rf` | float | No | Override risk-free rate |

**Response 200** (`application/json`):
```json
{
  "ticker": "AAPL",
  "wacc": 0.095,
  "growth_rate": 0.033,
  "tangible_value_per_share": 3.47,
  "dcf_value_per_share": 167.23,
  "as_of": "2025-01-31T10:30:00Z",
  "data_quality_score": 0.92,
  "data_quality_grade": "A"
}
```

**Response Schema** (`FairValueResponse`):
| Field | Type | Description |
|-------|------|-------------|
| `ticker` | string | Stock ticker symbol |
| `wacc` | float | Weighted Average Cost of Capital (decimal, e.g., 0.095 = 9.5%) |
| `growth_rate` | float | Terminal growth rate (decimal) |
| `tangible_value_per_share` | float | Net Tangible Asset Value per share (USD) |
| `dcf_value_per_share` | float | DCF Fair Value per share (USD) |
| `as_of` | datetime | Timestamp of data used for calculation (ISO 8601) |
| `data_quality_score` | float | Data quality score (0.0 - 1.0) |
| `data_quality_grade` | string | Quality grade (A/B/C/D/F) |

**Error Responses**: 400 (`INVALID_TICKER`), 401, 404 (`TICKER_NOT_FOUND`), 422 (`INSUFFICIENT_DATA`, `MODEL_NOT_APPLICABLE`)

---

### 2. POST /api/v1/fair-value/bulk

**Auth**: Required (`read:fair_value` permission)

**Request Body** (`application/json`):
```json
{
  "tickers": ["AAPL", "MSFT", "GOOGL"],
  "override_beta": null,
  "override_rf": null
}
```

**Request Schema** (`BulkFairValueRequest`):
| Field | Type | Required | Constraints |
|-------|------|----------|-------------|
| `tickers` | string[] | Yes | 1-10 items, each 1-5 chars |
| `override_beta` | float | No | Override beta for all tickers |
| `override_rf` | float | No | Override risk-free rate |

**Response 200** (all succeed) / **207** (partial) / **422** (all fail):
```json
{
  "results": [
    {
      "ticker": "AAPL",
      "wacc": 0.095,
      "growth_rate": 0.033,
      "tangible_value_per_share": 3.47,
      "dcf_value_per_share": 167.23,
      "as_of": "2025-01-31T10:30:00Z",
      "data_quality_score": 0.92,
      "data_quality_grade": "A"
    }
  ],
  "failures": [
    {
      "ticker": "XYZA1",
      "error_code": "TICKER_NOT_FOUND",
      "message": "Ticker not found in any data source"
    }
  ],
  "summary": {
    "total_requested": 2,
    "successful": 1,
    "failed": 1
  }
}
```

**Response Schema** (`BulkFairValueResponse`):
| Field | Type | Description |
|-------|------|-------------|
| `results` | FairValueResponse[] | Successful valuations |
| `failures` | BulkFailure[] | Per-ticker error details for failed tickers |
| `summary.total_requested` | integer | Number of tickers requested |
| `summary.successful` | integer | Number of successful valuations |
| `summary.failed` | integer | Number of failed valuations |

**Status Codes**:
| Code | Meaning |
|------|---------|
| 200 | All tickers succeeded |
| 207 | Partial success — some succeeded, some failed |
| 422 | All tickers failed |

**Error Responses**: 400, 401

---

### 3. GET /health

**Auth**: None

**Response 200**:
```json
{
  "status": "ok",
  "timestamp": "2025-01-31T10:30:00Z",
  "service": "dcf-valuation-api"
}
```

---

### 4. GET /ready

**Auth**: None

**Response 200**:
```json
{
  "status": "ready",
  "timestamp": "2025-01-31T10:30:00Z",
  "checks": {
    "database": "ok",
    "external_apis": "ok",
    "cache": "ok"
  }
}
```

---

### 5. GET /api/v1/health/detailed

**Auth**: Required (`read:health` permission)

**Response Codes**:
| Status | Meaning |
|--------|---------|
| 200 | All components healthy |
| 206 | Some components degraded |
| 503 | Critical components unhealthy |

---

### 6. GET /api/v1/metrics

**Auth**: Required (`read:metrics` permission)

Returns application and business metrics in JSON format.

---

### 7. GET /metrics

**Auth**: None

Returns Prometheus-format metrics for monitoring systems.

---

### 8. GET /version

**Auth**: None

**Response 200**:
```json
{
  "version": "0.9.0-rc1",
  "environment": "development",
  "build_time": "",
  "git_commit": ""
}
```

---

### 9. POST /api/v1/auth/keys

**Auth**: Required (`manage:keys` permission)

**Request Body**:
```json
{
  "user_id": "service-account-1",
  "permissions": ["read:fair_value", "read:health"]
}
```

**Response 201**: Created (returns the raw API key - only time it's visible)
**Error Responses**: 400, 401, 403

---

### Rate Limit Response Headers

All responses include rate limit headers when rate limiting is active:

| Header | Description |
|--------|-------------|
| `X-RateLimit-Limit` | Maximum requests per window |
| `X-RateLimit-Remaining` | Remaining requests in window |
| `X-RateLimit-Reset` | Unix timestamp when window resets |

**429 Response Body**:
```json
{
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Rate limit exceeded",
    "type": "rate_limit_error"
  },
  "rate_limit": {
    "remaining": 0,
    "reset_time": 1706700000,
    "retry_after": 60
  },
  "timestamp": "2025-01-31T10:30:00Z",
  "path": "/api/v1/fair-value/AAPL",
  "method": "GET"
}
```

---

## Service Interfaces (Ports)

These are the Go interfaces that define boundaries between layers. All are defined in `internal/core/ports/`.

### Gateway Interfaces

#### SECGateway
```go
type SECGateway interface {
    GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error)
    GetCompanyConcepts(ctx context.Context, cik string, tag string) (*entities.ConceptResponse, error)
    GetTickerCIKMapping(ctx context.Context) (map[string]string, error)
    GetFinancialDataForTicker(ctx context.Context, ticker, cik string) (*entities.HistoricalFinancialData, error)
    HealthCheck(ctx context.Context) error
}
```

#### MarketDataGateway
```go
type MarketDataGateway interface {
    GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error)
    GetQuotes(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error)
    GetHistoricalPrices(ctx context.Context, ticker string, startDate, endDate time.Time) ([]*entities.PriceData, error)
    HealthCheck(ctx context.Context) error
}
```

#### MacroDataGateway
```go
type MacroDataGateway interface {
    GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error)
    GetMarketRiskPremium(ctx context.Context) (float64, error)
    HealthCheck(ctx context.Context) error
}
```

### Repository Interfaces

#### FinancialDataRepository
```go
type FinancialDataRepository interface {
    Store(ctx context.Context, data *entities.FinancialData) error
    GetLatest(ctx context.Context, ticker string) (*entities.FinancialData, error)
    GetHistorical(ctx context.Context, ticker string, periods int) (*entities.HistoricalFinancialData, error)
    GetByPeriod(ctx context.Context, ticker, period string) (*entities.FinancialData, error)
    StoreHistorical(ctx context.Context, data *entities.HistoricalFinancialData) error
    GetLastUpdated(ctx context.Context, ticker string) (time.Time, error)
}
```

#### MarketDataRepository
```go
type MarketDataRepository interface {
    Store(ctx context.Context, data *entities.MarketData) error
    GetLatest(ctx context.Context, ticker string) (*entities.MarketData, error)
    GetBatch(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error)
    IsStale(ctx context.Context, ticker string, maxAge time.Duration) (bool, error)
    GetLastUpdated(ctx context.Context, ticker string) (time.Time, error)
}
```

#### MacroDataRepository
```go
type MacroDataRepository interface {
    Store(ctx context.Context, data *entities.MacroData) error
    GetLatest(ctx context.Context) (*entities.MacroData, error)
    IsStale(ctx context.Context, maxAge time.Duration) (bool, error)
}
```

#### CacheRepository
```go
type CacheRepository interface {
    Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error
    Get(ctx context.Context, key string, dest interface{}) error
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error)
    GetKeys(ctx context.Context, pattern string) ([]string, error)
    DeletePattern(ctx context.Context, pattern string) error
}
```

#### WatchlistRepository
```go
type WatchlistRepository interface {
    GetActiveWatchlist(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error)
    GetAll(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error)
    GetByTicker(ctx context.Context, ticker string) (*entities.WatchlistEntry, error)
    Add(ctx context.Context, entry *entities.WatchlistEntry) error
    Update(ctx context.Context, ticker string, updates *entities.UpdateWatchlistEntryRequest) error
    Remove(ctx context.Context, ticker string) error
    RecordSuccess(ctx context.Context, ticker string, fetchedAt time.Time) error
    RecordFailure(ctx context.Context, ticker string) error
    GetStats(ctx context.Context) (*entities.WatchlistStats, error)
    BulkUpdateFailures(ctx context.Context, failures map[string]bool) error
}
```

#### MetricsService
```go
type MetricsService interface {
    // HTTP Metrics
    RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration, responseSize int)
    IncHTTPRequestsInFlight()
    DecHTTPRequestsInFlight()

    // Valuation Metrics
    RecordValuationRequest(ticker, requestType, status string, duration time.Duration)
    RecordValuationError(ticker, errorType string)
    IncDCFCalculations()
    IncWACCCalculations()

    // Data Source Metrics
    RecordSECAPIRequest(endpoint, status string)
    RecordMarketAPIRequest(provider, status string)
    RecordMacroAPIRequest(provider, status string)
    RecordDataFetch(source, ticker string, duration time.Duration)

    // Cache Metrics
    RecordCacheRequest(cacheType, operation, result string)
    SetCacheHitRatio(cacheType string, ratio float64)

    // Business Metrics
    SetAverageWACC(wacc float64)
    SetAverageGrowthRate(rate float64)

    // Getters
    GetTotalRequests() int64
    GetActiveConnections() int
    GetAverageResponseTime() float64
    GetErrorRate() float64
    GetCacheHitRate() float64
    GetTotalValuations() int64
    GetSuccessfulValuations() int64
    GetFailedValuations() int64
    GetAverageWACC() float64
    GetAverageGrowthRate() float64
    GetUniqueTickersServed() int64
    HealthCheck() error
}
```

### Resilience Interfaces

```go
type CircuitBreaker interface {
    Execute(ctx context.Context, fn func() error) error
    State() string
    Reset()
}

type RetryPolicy interface {
    Execute(ctx context.Context, fn func() error) error
    WithMaxAttempts(attempts int) RetryPolicy
    WithBackoff(strategy string) RetryPolicy
}
```

---

## Database Schema Contracts

See `internal/infra/database/schema.sql` for the full schema. Key table contracts:

### financial_data
- **Primary key**: `id` (autoincrement)
- **Unique constraint**: `(ticker, filing_period)`
- **Foreign key**: `ticker` → `companies.ticker`
- **Key fields**: `operating_income`, `normalized_operating_income`, `revenue`, `interest_expense`, `tax_rate`, `total_assets`, `tangible_assets`, `total_debt`, `shares_outstanding`

### market_data
- **Foreign key**: `ticker` → `companies.ticker`
- **Key fields**: `share_price`, `market_cap`, `beta`, `beta_3_year`, `average_volume`, `source`

### api_keys
- **Primary key**: `id` (UUID hex)
- **Unique constraint**: `key_hash`
- **Key fields**: `user_id`, `permissions` (JSON array as TEXT), `rate_limit`, `is_active`, `expires_at`

### scheduler_watchlist
- **Unique constraint**: `ticker`
- **Foreign key**: `ticker` → `companies.ticker`
- **Key fields**: `is_active`, `priority` (1=high, 2=medium, 3=low), `fetch_failures`, `max_failures`

---

## Configuration Contract

Configuration is loaded via Viper in this priority order:
1. `config/config.yaml` file
2. Environment variables (keys mapped: `database.driver` → `DATABASE_DRIVER`)
3. Default values (defined in `internal/config/config.go:setDefaults()`)

### Key Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Server port |
| `ENVIRONMENT` | `development` | Environment name |
| `LOG_LEVEL` | `debug` | Log level (debug/info/warn/error) |
| `DATABASE_DRIVER` | `sqlite3` | Database driver (sqlite3/postgres) |
| `DATABASE_SQLITE_PATH` | `./data/midas.db` | SQLite file path |
| `CACHE_REDIS_URL` | `redis://localhost:6379` | Redis connection URL |
| `SEC_USER_AGENT` | `Midas DCF API admin@example.com` | SEC API User-Agent |
| `SEC_RATE_LIMIT` | `10` | SEC API requests/second |
| `SCHEDULER_ENABLED` | `false` | Enable background scheduler |
| `SCHEDULER_INTERVAL` | `24h` | Scheduler run interval |
| `DATACLEANER_ENABLE_AI_INTEGRATION` | `false` | Enable AI footnote analysis |
| `MARKET_YFINANCE_BASE_URL` | `https://query2.finance.yahoo.com` | Yahoo Finance API base URL |
| `MARKET_YFINANCE_COOKIE_URL` | `https://fc.yahoo.com` | Yahoo Finance cookie endpoint |
| `MARKET_YFINANCE_CRUMB_URL` | `https://query2.finance.yahoo.com/v1/test/getcrumb` | Yahoo Finance crumb endpoint |
| `MARKET_YFINANCE_AUTH_TTL` | `6h` | Yahoo Finance auth credential cache duration |
| `ENABLE_SWAGGER` | `false` | Enable Swagger UI |
| `ENABLE_PPROF` | `false` | Enable pprof profiling |

---

## External API Contracts

### SEC EDGAR CompanyFacts API

**Request**: `GET https://data.sec.gov/api/xbrl/companyfacts/CIK{cik}.json`
**Headers**: `User-Agent: {company} {email}` (required by SEC)

**Response structure** (simplified):
```json
{
  "cik": 320193,
  "entityName": "Apple Inc.",
  "facts": {
    "us-gaap": {
      "Revenue": {
        "label": "Revenue",
        "units": {
          "USD": [
            { "end": "2024-09-30", "val": 391035000000, "fy": 2024, "fp": "FY", "form": "10-K", "filed": "2024-11-01" }
          ]
        }
      }
    }
  }
}
```

### SEC Ticker-CIK Mapping

**Request**: `GET https://www.sec.gov/files/company_tickers.json`

**Response**: Map of index → `{ "cik_str": "320193", "ticker": "AAPL", "title": "Apple Inc." }`
