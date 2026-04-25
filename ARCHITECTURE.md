# ARCHITECTURE.md - Midas System Architecture

This document serves as the source of truth for Midas's system architecture.

## System Overview

Midas is a **monolithic Go REST API** that computes intrinsic equity valuations using Discounted Cash Flow (DCF) analysis. It aggregates data from three external sources (SEC EDGAR, Yahoo Finance/Finzive, FRED), normalizes and cleans the financial data through a configurable pipeline, and returns fair value per share through a secured REST API.

```
                          ┌─────────────────────┐
                          │    API Consumers     │
                          │  (HTTP + API Key)    │
                          └──────────┬──────────┘
                                     │
                          ┌──────────▼──────────┐
                          │     Gin HTTP Layer   │
                          │  (Auth, RateLimit,   │
                          │   Metrics, CORS)     │
                          └──────────┬──────────┘
                                     │
                 ┌───────────────────┼───────────────────┐
                 │                   │                   │
        ┌────────▼───────┐  ┌───────▼───────┐  ┌───────▼───────┐
        │  Fair Value     │  │   Health      │  │   Auth        │
        │  Handler        │  │   Handler     │  │   Handler     │
        └────────┬───────┘  └───────────────┘  └───────────────┘
                 │
        ┌────────▼───────────────────────────────────┐
        │             Valuation Service               │
        │  (Orchestrates full valuation pipeline)     │
        └────────┬───────────────────────────────────┘
                 │
     ┌───────────┼───────────┐
     │           │           │
┌────▼────┐ ┌───▼───┐ ┌─────▼─────┐
│  Data   │ │ Data  │ │ Financial │
│ Fetcher │ │Cleaner│ │  Calcs    │
│(coord.) │ │(pipe) │ │(WACC+DCF) │
└────┬────┘ └───────┘ └───────────┘
     │
     ├─── SEC Gateway ──────► SEC EDGAR API
     ├─── Market Gateway ───► Yahoo Finance / Finzive
     └─── Macro Gateway ────► FRED API (or manual config)
```

## Architectural Pattern: Clean Architecture (Hexagonal)

The codebase follows **Clean Architecture** (Ports & Adapters), enforcing strict dependency rules:

```
┌──────────────────────────────────────────────────────────┐
│                   cmd/ (Entry Points)                    │
├──────────────────────────────────────────────────────────┤
│                internal/api/ (HTTP Transport)            │
│  Handlers, Middleware, Routes (Gin)                      │
├──────────────────────────────────────────────────────────┤
│             internal/services/ (Use Cases)               │
│  valuation, valuation/models, growth, datafetcher,       │
│  datacleaner, auth, scheduler                            │
├──────────────────────────────────────────────────────────┤
│               internal/core/ (Domain)                    │
│  entities/ (models)    ports/ (interfaces)               │
│  ← NO EXTERNAL DEPENDENCIES →                           │
├──────────────────────────────────────────────────────────┤
│             internal/infra/ (Adapters)                   │
│  gateways/ (SEC, Market, Macro HTTP clients)             │
│  repositories/ (SQLite, Redis, Memory cache)             │
│  resilience/ (Circuit breakers, Retry policies)          │
├──────────────────────────────────────────────────────────┤
│               internal/di/ (Wiring)                      │
│  uber/fx dependency injection container                  │
├──────────────────────────────────────────────────────────┤
│                pkg/finance/ (Shared Libs)                │
│  dcf/, wacc/, growth/, leases/ - pure calculation logic  │
└──────────────────────────────────────────────────────────┘
```

**Dependency Rule**: Inner layers never import outer layers. `core/` has zero imports from `infra/`, `services/`, or `api/`. All communication crosses boundaries through interfaces defined in `core/ports/`.

## Dependency Injection

All wiring uses **uber/fx** in `internal/di/container.go`:

| Module | Provides |
|--------|----------|
| `CoreModule` | Logger, Database, Redis, Repositories, Gateways, Resilience |
| `ServiceModule` | Auth, DataCleaner, DataFetcher, Valuation, Metrics, RateLimiter, Scheduler, Watchlist, AI |
| `HandlerModule` | HealthHandler, Lifecycle hooks |

Services receive interfaces (ports), never concrete types. This enables testing with mocks and swapping implementations.

**Handler interfaces**: `FairValueHandler` depends on `ValuationCalculator` interface (not `*valuation.Service` directly), and `AuthHandler` depends on `AuthKeyManager` interface. These are bridged in the DI container via `fx.Provide` bindings from the concrete types.

## Data Flow: Fair Value Calculation

The core product flow for `GET /api/v1/fair-value/{ticker}`:

```
1. HTTP Request
     │
2. Auth Middleware ──── Validate API Key (SHA-256 hash lookup)
     │
3. Rate Limit Middleware ──── Check token bucket (Redis or in-memory)
     │
4. FairValueHandler.GetFairValue()
     │
5. ValuationService.CalculateFairValue()
     │
     ├── 5a. Check cache for recent valuation result
     │
     ├── 5b. DataFetcher.FetchAll() ──── Parallel fetching:
     │        ├── SEC Gateway: GetCompanyFacts(CIK) → FinancialData
     │        ├── Market Gateway: GetQuote(ticker) → MarketData
     │        └── Macro Gateway: GetTreasuryRates() → MacroData
     │
     ├── 5b'. Data Quality Guardrail:
     │        ├── isFinancialDataIncomplete(): checks D&A, CapEx, Cash fields
     │        ├── If missing → re-fetch from SEC EDGAR, persist updated data
     │        └── averageCapExAndDA(): smooth CapEx/D&A over available annual periods
     │
     ├── 5c. DataCleaner.Clean(FinancialData) ──── Pipeline:
     │        ├── Validate raw data
     │        ├── Apply asset adjustments (goodwill, intangibles, inventory, ROU, etc.)
     │        ├── Apply liability adjustments (contingent, pensions, leases, etc.)
     │        ├── Apply earnings normalization (restructuring, SBC, litigation, etc.)
     │        ├── Industry-specific rules (tech R&D capitalization, finance regulatory, etc.)
     │        ├── Risk flag detection
     │        └── Quality scoring (0-100)
     │
     ├── 5d. WACC Calculation (pkg/finance/wacc/)
     │        ├── Input validation: beta [0,∞), RF [0,20%], CRP [0,20%], MRP [0,15%]
     │        ├── Beta: Blume adjusted (0.67β + 0.33), unlevered/relevered (Hamada)
     │        ├── Cost of Equity = Rf + β(ERP) + CRP (country risk premium)
     │        ├── Cost of Debt = Interest Expense / Total Debt * (1 - Tax Rate)
     │        └── WACC = E/(E+D) * Ke + D/(E+D) * Kd
     │
     ├── 5e. Industry Classification + Model Selection
     │        ├── IndustryClassifier: SIC/NAICS/keyword from SEC EntityName
     │        │   ├── Two-pass: parent industry (TECH, FIN) then sub-industry (TECH_SAAS, FIN_IB)
     │        │   └── Pre-compiled regexes at config load (no per-call compilation)
     │        ├── ModelRouter selects: DDM (financials), FFO (REITs),
     │        │   Revenue Multiple (pre-revenue), Multi-Stage DCF (default)
     │        └── Fallback chain: if primary model fails (e.g., FIN with zero DPS),
     │            falls back to DCF (positive OI) or Revenue Multiple (negative OI)
     │
     ├── 5f. DCF Calculation (pkg/finance/dcf/) — for standard companies
     │        ├── Growth rate capped to config bounds (BUG-010 fix)
     │        ├── True FCF = NOPAT + D&A - CapEx - ΔWorkingCapital (fallback: NOPAT)
     │        ├── 7-year multi-stage projection (3 high-growth + 4 fade)
     │        ├── Terminal value: Gordon Growth averaged with exit-multiple TV when available
     │        ├── Discount all cash flows to present value
     │        ├── Enterprise Value = Sum of PV(FCFs) + PV(Terminal Value)
     │        └── Equity Value = EV - Debt + Cash - MinorityInterest - PreferredEquity → per share (diluted shares preferred)
     │
     ├── 5g. Tangible Book Value = (Total Assets - Intangibles - Liabilities) / Shares
     │
     └── 5h. Cache result, return FairValueResponse
```

## External Integrations

### SEC EDGAR API
- **Endpoint**: `https://data.sec.gov/api/xbrl/companyfacts/CIK{cik}.json`
- **Data**: Company financial facts from 10-K/10-Q filings
- **Rate limit**: 10 requests/second (SEC policy)
- **Resilience**: Circuit breaker (3 failures → 30s open) + Retry (3 attempts, exponential backoff)
- **CIK resolution**: Ticker → CIK mapping from `https://www.sec.gov/files/company_tickers.json`

### Yahoo Finance (Market Data)
- **Endpoint**: `https://query2.finance.yahoo.com` (unofficial, cookie+crumb auth)
- **Authentication**: Cookie from `fc.yahoo.com` + crumb token from `/v1/test/getcrumb` (automatic, cached 6h)
- **Data**: Share price, market cap, beta, volume, shares outstanding
- **Resilience**: Retry (3 attempts, linear backoff) + automatic auth refresh on 401

### Finzive (Fallback Market Data)
- **Endpoint**: `https://finzive.com`
- **Data**: Financial and market data via web scraping
- **Role**: Fallback source when Yahoo Finance is unavailable

### FRED (Macroeconomic Data)
- **Endpoint**: `https://api.stlouisfed.org/fred`
- **Data**: 10-year Treasury rate (risk-free rate), inflation
- **Fallback**: Manual configuration when FRED is disabled (`manual_risk_free_rate: 0.045`)

## Database Architecture

### Storage Options
- **SQLite3** (default): Single-file database at `./data/midas.db`, ideal for development and small deployments
- **PostgreSQL**: Production option via `database.driver: postgres`

**Transaction Safety**: `StoreHistorical` wraps all period inserts in a single transaction (`BeginTxx`/`Commit`/`defer Rollback`). If any period fails, the entire batch is rolled back.

### Core Tables

| Table | Purpose |
|-------|---------|
| `companies` | Company master data (ticker, CIK, name, sector) |
| `financial_data` | SEC filing data (income, balance sheet, normalized) |
| `market_data` | Market prices, beta, volume from Yahoo/Finzive |
| `macro_data` | Risk-free rate, market risk premium from FRED |
| `ticker_mapping` | Ticker ↔ CIK resolution cache |
| `valuation_results` | Cached DCF calculation results |
| `api_keys` | API key authentication (SHA-256 hashes) |
| `api_key_usage` | Request tracking per key |
| `scheduler_watchlist` | Background ingestion ticker list |
| `raw_sec_data` | Raw SEC JSON blobs for reprocessing |
| `cache_metadata` | Cache entry metadata and hit tracking |
| `audit_log` | Event audit trail |

### Views
- `latest_financial_data` - Most recent filing per ticker
- `latest_market_data` - Most recent market snapshot per ticker
- `complete_valuation_data` - Joined view combining financial + market + macro

## Caching Strategy

Two-tier caching with Redis (primary) and in-memory (fallback):

| Data Type | TTL | Rationale |
|-----------|-----|-----------|
| Valuation Results | 1h | Primary user-facing cache — recompute when stale |
| Ticker→CIK Mapping | 24h | Static SEC data, rarely changes |
| Yahoo Finance Auth | 6h | Cookie+crumb credentials, reused across requests |
| Macro Data | 4h | Treasury rates change slowly |
| Default | 30m | General cache entries |

**Design principle**: Caching is done at the valuation layer (final result) and per-credential layer (auth tokens). The DataFetcher always fetches fresh data from external APIs to avoid serving stale market prices. Requests with override parameters (`override_beta`, `override_rf`) bypass the valuation cache entirely to prevent cache poisoning.

Cache keys are versioned and pattern-based: `valuation:v4:{ticker}`. The version prefix is bumped when the calculation engine changes (e.g., Phase 4 upgrade), automatically invalidating stale cached results without a manual flush.

## Security Architecture

### Authentication
- API key-based authentication via `X-API-Key` header
- Keys stored as SHA-256 hashes in `api_keys` table
- Key lifecycle: create, expire, deactivate, usage tracking
- Admin key creation via `POST /api/v1/auth/keys`

### Authorization (Permission Model)
| Permission | Grants Access To |
|------------|-----------------|
| `read:fair_value` | `GET /api/v1/fair-value/{ticker}`, `POST /api/v1/fair-value/bulk` |
| `read:health` | `GET /api/v1/health/detailed` |
| `read:metrics` | `GET /api/v1/metrics` |
| `manage:keys` | `POST /api/v1/auth/keys` |
| `admin` | All endpoints |

### Rate Limiting
- Dual strategy: API key-based (per key limit) or IP-based fallback
- Token bucket algorithm backed by Redis/memory cache
- Standard rate limit headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`

### Security Headers
All responses include: `X-Content-Type-Options`, `X-Frame-Options`, `X-XSS-Protection`, `Strict-Transport-Security`, `Content-Security-Policy`

## Observability

### Metrics (Prometheus)
- **HTTP metrics**: Request count, latency histograms, active connections, error rates
- **Valuation metrics**: Calculation count, duration, success/failure rates
- **Data source metrics**: API call counts per source, latency
- **Cache metrics**: Hit/miss ratios, operation counts
- **Business metrics**: Average WACC, growth rates, unique tickers served
- Endpoint: `GET /metrics` (unauthenticated, Prometheus format)

### Logging
- Structured JSON logging via `go.uber.org/zap`
- Request correlation via `X-Request-ID` header
- Log levels: debug (development), info/warn/error (production)

### Health Checks
| Endpoint | Auth | Purpose |
|----------|------|---------|
| `GET /health` | No | Basic liveness probe |
| `GET /ready` | No | Readiness probe (DB + cache + APIs) |
| `GET /api/v1/health/detailed` | Yes | Full component health with status codes (200/206/503) |

## Data Cleaning Pipeline

The DataCleaner service is a configurable pipeline that normalizes raw SEC data:

```
Raw Financial Data
    │
    ├── Asset Adjustments
    │   ├── A1: Goodwill write-down assessment
    │   ├── A2: Other intangible asset impairment
    │   ├── A3: Capitalized software amortization
    │   ├── A4: Inventory obsolescence (dead stock write-down)
    │   ├── A5: Deferred tax asset valuation
    │   ├── A6: Right-of-use asset adjustment
    │   └── A7: Excess cash identification
    │
    ├── Liability Adjustments
    │   ├── L1: Contingent liabilities (probability-weighted)
    │   ├── L2: Pension underfunding
    │   ├── L3: Operating lease capitalization
    │   └── L4: Environmental/legal provisions
    │
    ├── Earnings Normalization
    │   ├── C1: Restructuring charge removal
    │   ├── C2: Asset sale gain/loss removal
    │   ├── C3: Litigation cost normalization
    │   ├── C4: Stock-based compensation adjustment
    │   ├── C5: Derivative gain/loss removal
    │   ├── C6: Capitalized interest normalization
    │   └── C7: Working capital adjustment
    │
    ├── Industry-Specific Rules
    │   ├── Technology: R&D capitalization, SBC adjustments
    │   ├── Finance: Regulatory capital, credit loss provisions
    │   ├── Healthcare: Drug development costs, milestone adjustments
    │   └── Retail: Inventory valuation, lease obligations
    │
    ├── Risk Flag Detection
    │   ├── Aggressive accounting detection
    │   ├── Revenue recognition anomalies
    │   ├── Related party transactions
    │   └── Going concern indicators
    │
    └── Quality Scoring (0-100)
        ├── Data completeness
        ├── Normalization confidence
        └── Flag severity weighting → Grade (A/B/C/D/F)
```

Configuration is externalized in JSON files under `config/datacleaner/`:
- `rules.json` - Main cleaning rules
- `xbrl_tag_mappings.json` - SEC XBRL tag → field mappings
- `industry_codes.json` - Industry classification patterns
- `flag_conditions.json` - Risk flag evaluation rules

## Background Scheduler

Optional background service (disabled by default) for automated data ingestion:

- **Watchlist-driven**: Only fetches tickers in `scheduler_watchlist` table
- **Configurable interval**: Default 24h
- **Concurrency control**: Max concurrent jobs (default: 2)
- **Failure tracking**: Auto-disables tickers after N consecutive failures
- **Graceful shutdown**: Respects application lifecycle hooks

## Deployment Topology

### Development
```
Local machine → go run cmd/server/main.go
                + SQLite file database
                + Optional Redis (docker-compose.yml)
```

### Staging
```
Docker Compose:
  ├── midas-api (multi-stage build, 53.5MB image)
  └── redis:7-alpine (cache)
```

### Production
```
Docker Compose (prod):
  ├── midas-api (release mode, minimal image)
  ├── redis:7-alpine (cache)
  └── PostgreSQL (recommended for production)
```

## Performance Targets (MVP)

| Metric | Target |
|--------|--------|
| p95 latency | < 300ms at 20 RPS |
| Error rate | < 1% |
| Throughput | >= 20 RPS sustained |
| Container size | ~53.5MB |

## Technology Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| HTTP Framework | Gin | Performance, middleware ecosystem, production-proven |
| DI Framework | uber/fx | Compile-time safety, lifecycle management, modular |
| Database | SQLite + PostgreSQL | SQLite for simplicity, Postgres for production scale |
| Cache | Redis + in-memory | Redis for distributed, memory for zero-dependency fallback |
| Configuration | Viper | File + env var + defaults, standard Go library |
| Logging | Zap | Structured JSON, high performance, leveled |
| Metrics | Prometheus | Industry standard, Grafana-compatible |
| Testing | testify + gopter | Assertions + property-based testing for financial math |
| Resilience | Custom circuit breaker + retry | Tailored to SEC/market API failure patterns |
