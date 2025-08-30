# Midas - DCF Valuation API

> Professional-grade REST API for equity valuation using Discounted Cash Flow analysis and real-time financial data

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

## 🎯 Overview

Midas provides institutional-quality equity valuation through a simple REST API. It combines SEC financial data, market prices, and macroeconomic indicators to calculate intrinsic value using industry best practices.

### Key Capabilities

- **📊 Intrinsic Valuation** - Net tangible asset value and DCF fair value per share
- **🔄 Real-time Data Integration** - SEC EDGAR, Yahoo Finance, and Federal Reserve data
- **🧹 Financial Data Normalization** - Removes accounting distortions and adjusts for one-time items
- **🏭 Industry-Specific Analysis** - Tailored adjustments for technology, finance, healthcare, and more
- **⚡ Production Ready** - Built-in caching, rate limiting, and monitoring
- **🔐 Enterprise Security** - API key authentication and request throttling

## 🚀 Quick Start

### Prerequisites

- Go 1.22 or higher
- Docker & Docker Compose
- Git

### Installation

```bash
# Clone the repository
git clone https://github.com/your-org/midas.git
cd midas

# Launch the staging environment (single command)
./scripts/launch_staging.sh  # Linux/macOS
# OR
.\scripts\launch_staging.ps1  # Windows PowerShell
```

The launch script automatically:
- ✅ Creates configuration from template
- ✅ Starts Redis cache
- ✅ Builds and starts the API server
- ✅ Verifies health status
- ✅ Displays connection details

## 📖 Usage

### Authentication

All protected endpoints require an API key via the `X-API-Key` header:

```bash
X-API-Key: your-api-key-here
```

### 30-Second Demo (Windows)

1) Apply schema and migrations (includes a demo API key and demo AAPL data):

```powershell
go run ./cmd/migrate -db ./data/midas.db
```

2) Run local validation (starts server, runs contract fuzz via Schemathesis, then calls fair value):

```powershell
./scripts/contract_fuzz.ps1 -DemoKey 'dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788' -ApiBase 'http://localhost:8080' -DbPath './data/midas.db' -InstallSchemathesis
```

3) Or just curl with the seeded key (after server is running):

```powershell
Invoke-RestMethod -Method GET -Uri http://localhost:8080/api/v1/fair-value/AAPL -Headers @{ 'X-API-Key'='dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788' } | ConvertTo-Json -Depth 6
```

### API Endpoints

#### Get Fair Value for Single Ticker

```bash
GET /api/v1/fair-value/{ticker}
```

**Example Request:**
```bash
curl -H "X-API-Key: your-api-key-here" \
  http://localhost:8080/api/v1/fair-value/AAPL
```

**Example Response:**
```json
{
  "ticker": "AAPL",
  "wacc": 0.095,
  "growth_rate": 0.033,
  "tangible_value_per_share": 3.47,
  "dcf_value_per_share": 167.23,
  "as_of": "2024-01-31T10:30:00Z",
  "data_quality_score": 0.92,
  "data_quality_grade": "A"
}
```

#### Bulk Valuation Request

```bash
POST /api/v1/fair-value/bulk
```

**Example Request:**
```bash
curl -X POST -H "X-API-Key: your-api-key-here" \
  -H "Content-Type: application/json" \
  -d '{
    "tickers": ["AAPL", "MSFT", "GOOGL"]
  }' \
  http://localhost:8080/api/v1/fair-value/bulk
```

**Example Response:**
```json
{
  "results": [
    { "ticker": "AAPL", "wacc": 0.095, "growth_rate": 0.033, "tangible_value_per_share": 3.47, "dcf_value_per_share": 167.23, "as_of": "..." },
    { "ticker": "MSFT", "wacc": 0.091, "growth_rate": 0.058, "tangible_value_per_share": 24.18, "dcf_value_per_share": 380.45, "as_of": "..." }
  ],
  "summary": { "total_requested": 3, "successful": 2, "failed": 1 }
}
```

#### Health Check

```bash
GET /health
```

Returns service health status:

```json
{ "status": "ok", "service": "dcf-valuation-api", "timestamp": "..." }
```
```

### Understanding the Valuation Results

#### Key Metrics Explained

- **DCF Fair Value per Share**: Intrinsic value based on 5-year discounted cash flow projection
- **Net Tangible Asset Value per Share**: Book value excluding intangibles, adjusted for market conditions
- **Margin of Safety**: Percentage difference between fair value and current price (negative = overvalued)
- **Quality Score**: 0-1 score indicating data reliability and adjustment transparency

#### Industry-Specific Adjustments

The API automatically applies industry-specific normalizations:

- **Technology**: R&D capitalization, stock-based compensation adjustments
- **Finance**: Regulatory capital requirements, credit loss provisions
- **Healthcare**: Drug development costs, regulatory milestone adjustments
- **Retail**: Inventory valuation, lease obligation adjustments

## ⚙️ Configuration

### Environment & Config

You can configure via `config/config.yaml` or environment variables (Viper). Minimal example `config/config.yaml`:

```yaml
port: "8080"
environment: "development"
log_level: "debug"

database:
  driver: "sqlite3"
  sqlite_path: "./data/midas.db"

cache:
  redis_url: "redis://localhost:6379" # falls back to in-memory if unavailable

sec:
  user_agent: "YourCompany you@example.com"
```

### Database Setup

Initialize the schema once per environment:

- SQLite (default):

```bash
mkdir -p ./data
sqlite3 ./data/midas.db < internal/infra/database/schema.sql
```

- Postgres (if `database.driver: postgres`):

```bash
psql "$DATABASE_URL" -f internal/infra/database/schema.sql
```

### Cache Configuration

Midas uses intelligent caching to optimize performance:

- **Financial Data**: 24-hour TTL (quarterly reports)
- **Market Data**: 15-minute TTL (prices, volume)
- **Macro Data**: 7-day TTL (interest rates)

### AI Integration Configuration

Midas supports optional AI-enhanced footnote analysis for improved financial data quality:

#### Environment Variables

```bash
# AI Integration Settings
DATACLEANER_ENABLE_AI_INTEGRATION=false    # Enable/disable AI features (default: false)
DATACLEANER_AI_SERVICE_URL=""              # External AI service endpoint
DATACLEANER_AI_SERVICE_TIMEOUT=5           # Request timeout in seconds (default: 5)
DATACLEANER_AI_API_KEY=""                  # API key for external AI service (optional)
```

#### Usage

When enabled, AI analysis is applied to:
- **Contingent Liabilities**: Enhanced probability estimation from legal footnotes
- **Pension Obligations**: Improved discount rate and assumption analysis  
- **Operating Leases**: Better present value calculations
- **Restructuring Charges**: One-time vs. recurring nature identification

**Benefits:**
- More accurate probability-weighted liability estimates
- Improved earnings normalization through better one-time item detection
- Enhanced valuation accuracy for companies with complex footnote disclosures

**Graceful Degradation:**
- AI failures automatically fall back to conservative industry-standard estimates
- No service interruption when AI is unavailable
- Comprehensive logging and monitoring for AI service health

**Security & Privacy:**
- Only non-sensitive metadata is logged (ticker, analysis type, timing)
- Footnote text is never stored in logs
- AI service calls respect strict timeouts to prevent blocking

#### Example Configuration

```bash
# Development setup with mock AI service
DATACLEANER_ENABLE_AI_INTEGRATION=true
DATACLEANER_AI_SERVICE_URL=mock://test
DATACLEANER_AI_SERVICE_TIMEOUT=5

# Production setup with external AI service
DATACLEANER_ENABLE_AI_INTEGRATION=true  
DATACLEANER_AI_SERVICE_URL=https://your-ai-service.com/analyze
DATACLEANER_AI_API_KEY=your-ai-api-key-here
DATACLEANER_AI_SERVICE_TIMEOUT=10
```

### Scheduler Configuration

Midas includes an optional background scheduler for automated data ingestion. The scheduler is **disabled by default** and uses a **DB-driven watchlist approach** for maximum flexibility.

#### Environment Variables

```bash
# Scheduler Settings
SCHEDULER_ENABLED=false           # Enable/disable scheduler (default: false)
SCHEDULER_INTERVAL=24h           # Run interval (default: 24h for daily)
SCHEDULER_MAX_CONCURRENCY=2      # Max concurrent jobs (default: 2)
```

#### How It Works

1. **DB-Driven Watchlist**: Add tickers to the `scheduler_watchlist` table to enable automatic fetching
2. **No-Op When Empty**: If the watchlist is empty, the scheduler performs no work
3. **Failure Tracking**: Automatically tracks fetch successes/failures per ticker
4. **Configurable Priorities**: Support for different priority levels and retry policies

#### Managing the Watchlist

```sql
-- Add tickers to watch
INSERT INTO scheduler_watchlist (ticker, is_active, priority) 
VALUES ('AAPL', true, 1), ('MSFT', true, 1), ('GOOGL', true, 2);

-- Enable/disable a ticker
UPDATE scheduler_watchlist SET is_active = false WHERE ticker = 'AAPL';

-- Remove a ticker from the watchlist
DELETE FROM scheduler_watchlist WHERE ticker = 'AAPL';

-- View watchlist status
SELECT * FROM scheduler_watchlist ORDER BY priority, ticker;
```

#### Production Usage

```bash
# Enable scheduler for production
SCHEDULER_ENABLED=true
SCHEDULER_INTERVAL=24h           # Daily ingestion at startup time
SCHEDULER_MAX_CONCURRENCY=5      # Higher concurrency for production

# Monitor scheduler logs
docker logs midas-api | grep "scheduler"
```

#### Benefits

- **Automated Data Freshness**: Keep financial data up-to-date without manual intervention
- **Flexible Ticker Management**: Add/remove tickers without restarting the service
- **Robust Error Handling**: Automatic retry logic with failure tracking
- **Production Ready**: Clean shutdown, graceful failure handling, comprehensive logging

### 🔄 Feature Rollback Instructions

If you need to disable AI integration or the scheduler after enabling them, follow these steps:

#### Rolling Back AI Integration

```bash
# 1. Disable AI in environment/config
DATACLEANER_ENABLE_AI_INTEGRATION=false

# 2. Restart the service 
docker-compose restart api
# OR for local development
# kill the process and restart with: go run cmd/server/main.go

# 3. Verify AI is disabled (should show "AI integration: disabled")
curl -H "Authorization: Bearer your-api-key" "http://localhost:8080/api/v1/health/detailed"
```

**Impact of AI Rollback:**
- ✅ **No data loss** - All historical valuation data remains intact
- ✅ **Graceful degradation** - System automatically falls back to conservative estimates
- ✅ **No API changes** - All endpoints continue working normally
- ⚠️ **Reduced accuracy** - Contingent liability and footnote analysis reverts to industry defaults

#### Rolling Back Scheduler

```bash
# 1. Disable scheduler in environment/config
SCHEDULER_ENABLED=false

# 2. Restart the service
docker-compose restart api

# 3. Optionally clear the watchlist (if desired)
# Connect to your database and run:
# DELETE FROM scheduler_watchlist;

# 4. Verify scheduler is disabled in logs
docker logs midas-api | grep "scheduler.*disabled"
```

**Impact of Scheduler Rollback:**
- ✅ **No service interruption** - API continues serving requests normally
- ✅ **Manual data freshness** - You can still fetch data via API calls or manual processes
- ⚠️ **No automated updates** - Data won't be automatically refreshed
- 💡 **Watchlist preserved** - Tickers remain in database for easy re-enabling

#### Emergency Rollback (Full Reset)

If you encounter issues and need to completely reset both features:

```bash
# 1. Stop the service
docker-compose down

# 2. Create clean environment config
cp config.env.example config.env
# Edit config.env to ensure both features are disabled:
# DATACLEANER_ENABLE_AI_INTEGRATION=false
# SCHEDULER_ENABLED=false

# 3. Restart with clean state
docker-compose up -d

# 4. Verify both features are disabled
curl "http://localhost:8080/health" # Should return 200 OK
curl -H "Authorization: Bearer your-key" "http://localhost:8080/api/v1/health/detailed"
```

## 🔧 Operations

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
./scripts/stop_staging.sh  # Linux/macOS
# OR
.\scripts\stop_staging.ps1  # Windows
```

### Monitoring

- **Metrics**: Prometheus endpoint at `/metrics`
- **Health**: Health check at `/health`
- **Logs**: Structured JSON logging to stdout

## End-to-End Example

1) Start the server (any option above), ensure it listens on `:8080`.
2) Create a dev API key in SQLite:

```bash
sqlite3 ./data/midas.db "INSERT INTO api_keys (key_hash, user_id, permissions, rate_limit, is_active) VALUES ('<sha256_of_dcf_dev_key_123>', 'local-dev', '[\"read:fair_value\",\"read:health\",\"read:metrics\"]', 1000, 1);"
```

3) Call the API:

```bash
curl -H "X-API-Key: dcf_dev_key_123" http://localhost:8080/api/v1/fair-value/AAPL
```

## 🐛 Troubleshooting

### Common Issues

**"SEC rate limit exceeded"**
- The SEC API limits to 10 requests/second
- Solution: Enable Redis caching or reduce request rate

**"Market data unavailable"**
- Yahoo Finance may be temporarily down
- Solution: Cached data will be used if available

**"Invalid financial data"**
- Company may have unusual reporting structure
- Check the quality_score in response for details

### Debug Mode

Enable detailed logging:

```env
LOG_LEVEL=debug
DEBUG_MODE=true
```

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🤝 Support

- **Issues**: [GitHub Issues](https://github.com/your-org/midas/issues)
 - Swagger/OpenAPI docs are optional and disabled by default. When enabled, they will be available at `/docs`.

---

Built with ❤️ by the Midas Team