# Midas - DCF Valuation API

> Professional-grade REST API for equity valuation using Discounted Cash Flow analysis and real-time financial data

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

## Overview

Midas provides institutional-quality equity valuation through a simple REST API. It combines SEC financial data, market prices, and macroeconomic indicators to calculate intrinsic value using industry best practices.

### Key Capabilities

- **Intrinsic Valuation** - Net tangible asset value and DCF fair value per share
- **Real-time Data Integration** - SEC EDGAR, Yahoo Finance, Finzive, and FRED data
- **Financial Data Normalization** - Removes accounting distortions and adjusts for one-time items via configurable pipeline
- **Industry-Specific Analysis** - Tailored adjustments for technology, finance, healthcare, retail, and more
- **Production Ready** - Caching (Redis + in-memory fallback), rate limiting, API key auth, Prometheus metrics
- **Enterprise Security** - Permission-based API key authentication, security headers, request throttling
- **Background Scheduler** - Optional automated data ingestion via DB-driven watchlist

## Quick Start

### Prerequisites

- Go 1.23 or higher (CGO enabled for SQLite)
- Docker & Docker Compose (optional, for Redis and containerized deployment)
- Git

### Installation

```bash
# Clone the repository
git clone https://github.com/your-org/midas.git
cd midas

# Option 1: Launch staging environment (single command)
./scripts/launch_staging.sh  # Linux/macOS
.\scripts\launch_staging.ps1  # Windows PowerShell

# Option 2: Run locally without Docker
go run ./cmd/migrate -db ./data/midas.db   # Apply schema + seed demo data
go run cmd/server/main.go                   # Start the API server
```

The launch script automatically:
- Creates configuration from template
- Starts Redis cache (via Docker Compose)
- Builds and starts the API server
- Verifies health status
- Displays connection details

## Usage

### Authentication

All protected endpoints require an API key via the `X-API-Key` header:

```bash
X-API-Key: your-api-key-here
```

### 30-Second Demo

1) Apply schema and migrations (includes a demo API key and demo AAPL data):

```bash
go run ./cmd/migrate -db ./data/midas.db
```

2) Start the server:

```bash
go run cmd/server/main.go
```

3) Call the API with the seeded demo key:

```bash
# Linux/macOS
curl -H "X-API-Key: dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788" \
  http://localhost:8080/api/v1/fair-value/AAPL

# Windows PowerShell
Invoke-RestMethod -Method GET -Uri http://localhost:8080/api/v1/fair-value/AAPL `
  -Headers @{ 'X-API-Key'='dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788' } | ConvertTo-Json -Depth 6
```

4) (Optional) Run contract fuzz testing:

```powershell
./scripts/contract_fuzz.ps1 -DemoKey '<key>' -ApiBase 'http://localhost:8080' -DbPath './data/midas.db' -InstallSchemathesis
```

### API Endpoints

#### Public Endpoints (No Auth)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Basic health check |
| GET | `/ready` | Readiness probe (DB, cache, APIs) |
| GET | `/version` | Version and build info |
| GET | `/metrics` | Prometheus metrics |

#### Protected Endpoints (API Key Required)

| Method | Path | Permission | Description |
|--------|------|------------|-------------|
| GET | `/api/v1/fair-value/{ticker}` | `read:fair_value` | Fair value for single ticker |
| POST | `/api/v1/fair-value/bulk` | `read:fair_value` | Fair value for multiple tickers (max 10) |
| GET | `/api/v1/health/detailed` | `read:health` | Detailed component health |
| GET | `/api/v1/metrics` | `read:metrics` | Application and business metrics (JSON) |
| POST | `/api/v1/auth/keys` | `manage:keys` | Create new API key |

#### Optional Endpoints

| Method | Path | Condition | Description |
|--------|------|-----------|-------------|
| GET | `/swagger/*` | `ENABLE_SWAGGER=true` | Swagger UI |
| GET | `/docs/openapi.yaml` | `ENABLE_SWAGGER=true` | OpenAPI spec |
| GET | `/debug/pprof/*` | `ENABLE_PPROF=true` | Go pprof profiling |

### Example Request & Response

**GET /api/v1/fair-value/AAPL**

```json
{
  "ticker": "AAPL",
  "wacc": 0.095,
  "growth_rate": 0.033,
  "tangible_value_per_share": 3.47,
  "dcf_value_per_share": 167.23,
  "as_of": "2025-01-31T10:30:00Z",
  "data_quality_score": 92,
  "data_quality_grade": "A"
}
```

**POST /api/v1/fair-value/bulk**

Request:
```json
{ "tickers": ["AAPL", "MSFT", "ZZZZ"] }
```

Response (HTTP 207 Multi-Status — partial success):
```json
{
  "results": [
    { "ticker": "AAPL", "wacc": 0.095, "growth_rate": 0.033, "tangible_value_per_share": 3.47, "dcf_value_per_share": 167.23, "as_of": "..." },
    { "ticker": "MSFT", "wacc": 0.091, "growth_rate": 0.058, "tangible_value_per_share": 24.18, "dcf_value_per_share": 380.45, "as_of": "..." }
  ],
  "failures": [
    { "ticker": "ZZZZ", "error_code": "TICKER_NOT_FOUND", "message": "Ticker not found in any data source" }
  ],
  "summary": { "total_requested": 3, "successful": 2, "failed": 1 }
}
```

### Understanding the Valuation Results

#### Key Metrics Explained

- **DCF Fair Value per Share**: Intrinsic value from a multi-year discounted cash flow projection (archetype-driven explicit horizon, typically 3–10 years)
- **Net Tangible Asset Value per Share**: Book value excluding intangibles, adjusted for market conditions
- **WACC**: Weighted Average Cost of Capital used as the discount rate
- **Growth Rate**: Summary first-stage growth rate — the CAGR of the per-year projected `growth_rates` (not the terminal rate; see `dcf_terminal_growth_used` for that)
- **Quality Score**: 0–100 score indicating data reliability and adjustment transparency
- **Quality Grade**: Letter grade (A/B/C/D/F) derived from the quality score

#### Query Parameter Overrides

Both fair-value endpoints accept optional overrides:
- `override_beta` (float) - Override the calculated beta for WACC computation
- `override_rf` (float) - Override the risk-free rate

#### Industry-Specific Adjustments

The API automatically applies industry-specific normalizations:

- **Technology**: R&D capitalization, stock-based compensation adjustments
- **Finance**: Regulatory capital requirements, credit loss provisions
- **Healthcare**: Drug development costs, regulatory milestone adjustments
- **Retail**: Inventory valuation, lease obligation adjustments

## Configuration

### Environment & Config

Configuration uses Viper with this priority: config file > environment variables > defaults.

Create `config/config.yaml` or set environment variables:

```yaml
port: "8080"
environment: "development"
log_level: "debug"

database:
  driver: "sqlite3"              # sqlite3 or postgres
  sqlite_path: "./data/midas.db"

cache:
  redis_url: "redis://localhost:6379"  # falls back to in-memory if unavailable

sec:
  user_agent: "YourCompany you@example.com"
```

Environment variable mapping: `database.driver` -> `DATABASE_DRIVER`, `cache.redis_url` -> `CACHE_REDIS_URL`, etc.

### Database Setup

```bash
# Option 1: Use the migrate command (recommended - handles schema + demo data)
go run ./cmd/migrate -db ./data/midas.db

# Option 2: Apply schema manually
# SQLite
sqlite3 ./data/midas.db < internal/infra/database/schema.sql

# PostgreSQL
psql "$DATABASE_URL" -f internal/infra/database/schema.sql
```

### Cache Configuration

Midas uses two-tier caching (Redis primary, in-memory fallback):

| Data Type | TTL | Rationale |
|-----------|-----|-----------|
| SEC Filings | 48h | Quarterly reports change infrequently |
| Market Data | 15m | Prices update throughout trading day |
| Macro Data | 4h | Treasury rates change slowly |
| Valuation Results | 1h | Recompute when underlying data changes |
| Cleaning Results | 6h | Normalization is CPU-intensive |

### Scheduler Configuration

Optional background scheduler for automated data ingestion. **Disabled by default**.

```bash
SCHEDULER_ENABLED=false           # Enable/disable scheduler
SCHEDULER_INTERVAL=24h           # Run interval
SCHEDULER_MAX_CONCURRENCY=2      # Max concurrent jobs
```

Uses a DB-driven watchlist approach:

```sql
-- Add tickers to watch
INSERT INTO scheduler_watchlist (ticker, is_active, priority)
VALUES ('AAPL', true, 1), ('MSFT', true, 1), ('GOOGL', true, 2);

-- View watchlist status
SELECT * FROM scheduler_watchlist ORDER BY priority, ticker;
```

### AI Integration (Optional)

Optional AI-enhanced footnote analysis. **Disabled by default**.

```bash
DATACLEANER_ENABLE_AI_INTEGRATION=false
DATACLEANER_AI_SERVICE_URL=""
DATACLEANER_AI_SERVICE_TIMEOUT=5
```

When enabled, improves analysis of contingent liabilities, pension obligations, operating leases, and restructuring charges. Falls back to conservative industry-standard estimates when AI is unavailable.

### Feature Flags

| Variable | Default | Description |
|----------|---------|-------------|
| `SCHEDULER_ENABLED` | `false` | Background data ingestion scheduler |
| `DATACLEANER_ENABLE_AI_INTEGRATION` | `false` | AI-powered footnote analysis |
| `ENABLE_SWAGGER` | `false` | Swagger UI (auto-enabled in development) |
| `ENABLE_PPROF` | `false` | pprof profiling endpoints |

## Architecture

Midas uses **Clean Architecture** (Hexagonal / Ports & Adapters) with `uber/fx` dependency injection.

```
API Consumers → Gin HTTP Layer → Handlers → Valuation Service
                                              ├── DataFetcher (SEC + Market + Macro)
                                              ├── DataCleaner (normalization pipeline)
                                              └── Financial Calcs (WACC + DCF)
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full system architecture documentation.
See [CONTRACTS.md](CONTRACTS.md) for API contracts and service interface definitions.
See [TESTING.md](TESTING.md) for testing strategy, conventions, and guidelines.

### Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.23+ |
| HTTP Framework | Gin |
| DI | uber/fx |
| Database | SQLite3 / PostgreSQL (sqlx) |
| Cache | Redis + in-memory fallback |
| Config | Viper |
| Logging | Zap (structured JSON) |
| Metrics | Prometheus |
| Testing | testify + gopter (property-based) |

## Operations

### Starting the Service

```bash
# Local (no Docker)
go run cmd/server/main.go

# Docker (development)
docker-compose up -d

# Docker (production-like)
docker-compose -f docker-compose.prod.yml up -d
```

### Stopping the Service

```bash
./scripts/stop_staging.sh       # Linux/macOS
.\scripts\stop_staging.ps1      # Windows
# Or: docker-compose down
```

### Monitoring

- **Prometheus Metrics**: `/metrics` (unauthenticated)
- **Health Check**: `/health` (liveness), `/ready` (readiness)
- **Detailed Health**: `/api/v1/health/detailed` (authenticated, returns 200/206/503)
- **Logs**: Structured JSON logging to stdout

### Performance Targets

| Metric | Target |
|--------|--------|
| p95 latency | < 300ms at 20 RPS |
| Error rate | < 1% |
| Throughput | >= 20 RPS sustained |
| Container size | ~53.5MB |

## Testing

```bash
# All tests
go test ./...

# With coverage
go test -cover ./...

# Integration tests
go test ./internal/integration/...

# Contract fuzz testing (requires Schemathesis)
schemathesis run http://localhost:8080/docs/openapi.yaml --header "X-API-Key: <key>" --checks all

# Load testing
go run ./scripts/load_tester.go -url http://localhost:8080 -key <API_KEY> -concurrency 20 -duration 60s -rps 20
```

See [TESTING.md](TESTING.md) for the full testing strategy.

## Troubleshooting

### Common Issues

**"SEC rate limit exceeded"**
- The SEC API limits to 10 requests/second
- Solution: Enable Redis caching or reduce request rate

**"Market data unavailable"**
- Yahoo Finance may be temporarily down; Finzive is used as fallback
- Cached data will be used if available

**"Invalid financial data"**
- Company may have unusual reporting structure
- Check the `data_quality_score` and `data_quality_grade` in the response

**Redis connection failed**
- Not fatal: Midas automatically falls back to in-memory cache
- Check logs for "Redis connection failed, will use memory cache"

**CGO errors**
- SQLite requires `CGO_ENABLED=1`
- On Windows: ensure GCC is available (e.g., via MSYS2 or TDM-GCC)

### Debug Mode

```bash
LOG_LEVEL=debug ENABLE_PPROF=true go run cmd/server/main.go
```

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Support

- **Issues**: [GitHub Issues](https://github.com/your-org/midas/issues)
- Swagger/OpenAPI docs are available at `/swagger/index.html` when enabled

---

Built with care by the Midas Team
