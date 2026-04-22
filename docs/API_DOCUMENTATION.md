# Midas DCF Valuation API

## Complete Documentation

**Version:** 0.9.0-rc1 (MVP)  
**Go Version:** 1.23+ (toolchain 1.24.4)  
**License:** Private  

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Quick Start](#2-quick-start)
3. [Authentication](#3-authentication)
4. [API Reference](#4-api-reference)
5. [Valuation Engine](#5-valuation-engine)
6. [Data Sources](#6-data-sources)
7. [Data Quality & Cleaning](#7-data-quality--cleaning)
8. [Configuration Reference](#8-configuration-reference)
9. [Deployment Guide](#9-deployment-guide)
10. [Monitoring & Observability](#10-monitoring--observability)
11. [Error Handling](#11-error-handling)
12. [Rate Limiting](#12-rate-limiting)
13. [Security](#13-security)
14. [Architecture & Design](#14-architecture--design)
15. [Troubleshooting](#15-troubleshooting)
16. [Glossary](#16-glossary)

---

## 1. Project Overview

Midas is a production-grade REST API for equity valuation using **Discounted Cash Flow (DCF)** analysis. It fetches real-time financial data from multiple authoritative sources, normalizes and cleans it through a rule-based pipeline, then calculates the intrinsic value per share for publicly traded companies.

### What Midas Does

- Fetches **SEC EDGAR** filings (10-K, 10-Q) for fundamental financial data
- Retrieves **market prices, beta, and volume** from Yahoo Finance (with Finzive fallback)
- Pulls **macroeconomic indicators** (Treasury rates, market risk premium) from FRED
- **Normalizes and cleans** financial data through a multi-category adjustment pipeline
- Estimates **multi-stage growth rates** by blending analyst consensus with historical CAGRs
- Selects the **optimal valuation model** per industry (DCF, DDM, FFO, or Revenue Multiple)
- Calculates **WACC** with Blume-adjusted beta, country risk premiums, and capital structure
- Runs **sanity cross-checks** against sector median multiples (P/E, EV/EBITDA, P/FCF)
- Reports **data quality scores** and transparency flags on every valuation

### Key Features

| Feature | Description |
|---------|-------------|
| Multi-Stage DCF | 3-stage growth model: high-growth, fade, terminal |
| Industry-Aware Models | Auto-selects DDM (banks), FFO (REITs), Revenue Multiple (pre-profit), or DCF |
| International (ADR) Support | Country risk premium adjustments for 50+ ADR tickers |
| Data Quality Scoring | 0-100 score with A-F grade on every valuation |
| Bulk Valuations | Value up to 10 tickers in a single request |
| Analyst Consensus Blending | Merges Yahoo Finance analyst estimates with historical data |
| Sanity Cross-Checks | Compares DCF-implied multiples against sector medians |
| Rate Limiting | Per-key, per-IP, per-endpoint, and global rate limits |
| Caching | Redis (distributed) + in-memory fallback with configurable TTLs |
| Resilience | Circuit breaker + exponential retry on all external API calls |

---

## 2. Quick Start

### Prerequisites

- **Go 1.23+** (with CGO enabled for SQLite)
- **Git**
- **SQLite3** development libraries (for `mattn/go-sqlite3`)
- **FRED API Key** (optional, for live macro data - falls back to manual defaults)

### Installation

```bash
# Clone the repository
git clone https://github.com/midas/dcf-valuation-api.git
cd dcf-valuation-api

# Install dependencies
go mod download

# Build the binary
go build ./cmd/server
```

### Database Setup

```bash
# Apply schema, migrations, and seed demo data
go run ./cmd/migrate -db ./data/midas.db

# Create a demo API key for testing
go run ./cmd/seed-demo-key -db ./data/midas.db
```

The demo key is printed to stdout. Save it - you'll need it for all authenticated requests.

### Running the Server

```bash
# Run directly
go run cmd/server/main.go

# Or use the built binary
./dcf-valuation-api
```

The server starts on `http://localhost:8080` by default.

### Your First Valuation

```bash
# Health check (no auth needed)
curl http://localhost:8080/health

# Get Apple's fair value
curl -H "X-API-Key: <your-demo-key>" \
     http://localhost:8080/api/v1/fair-value/AAPL

# Bulk valuation
curl -X POST \
     -H "X-API-Key: <your-demo-key>" \
     -H "Content-Type: application/json" \
     -d '{"tickers": ["AAPL", "MSFT", "GOOGL"]}' \
     http://localhost:8080/api/v1/fair-value/bulk
```

### Docker Quick Start

```bash
# Development (SQLite + Redis)
docker-compose up -d

# Production (PostgreSQL + Redis + Traefik)
docker-compose -f docker-compose.prod.yml up -d
```

---

## 3. Authentication

### API Key Authentication

All protected endpoints require an `X-API-Key` header. Keys are cryptographically generated with the format `dcf_<64-character-hex>`.

```
X-API-Key: dcf_1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef
```

### How Authentication Works

1. Client sends a request with the `X-API-Key` header
2. The server computes the SHA-256 hash of the provided key
3. Looks up the hash in the `api_keys` database table
4. Validates the key is **active** and **not expired**
5. Loads the key's **permissions** and **rate limit** into the request context
6. Records usage asynchronously (non-blocking)

### Permissions

Each API key has a set of permissions that control which endpoints it can access:

| Permission | Grants Access To |
|------------|-----------------|
| `read:fair_value` | `GET /api/v1/fair-value/:ticker`, `POST /api/v1/fair-value/bulk` |
| `read:health` | `GET /api/v1/health/detailed` |
| `read:metrics` | `GET /api/v1/metrics` |
| `manage:keys` | `POST /api/v1/auth/keys` |
| `admin:all` | All endpoints (superuser) |

### Creating API Keys

You need an existing key with `manage:keys` permission:

```bash
curl -X POST \
     -H "X-API-Key: <admin-key>" \
     -H "Content-Type: application/json" \
     -d '{
       "user_id": "service-account-1",
       "permissions": ["read:fair_value", "read:health"]
     }' \
     http://localhost:8080/api/v1/auth/keys
```

**Response (201 Created):**
```json
{
  "id": "abc123def456...",
  "key": "dcf_1234567890abcdef...",
  "user_id": "service-account-1",
  "permissions": ["read:fair_value", "read:health"],
  "rate_limit": 1000,
  "created_at": "2025-08-13T22:15:34Z",
  "expires_at": null
}
```

> **Important:** The raw `key` field is only returned in this response. Store it securely - it cannot be retrieved later.

### Authentication Errors

| Code | HTTP Status | Description |
|------|------------|-------------|
| `AUTH_001` | 401 | Missing `X-API-Key` header |
| `AUTH_002` | 401 | Invalid API key (not found in database) |
| `AUTH_003` | 401 | API key has expired |
| `AUTH_004` | 401 | API key is inactive (disabled) |
| `AUTH_005` | 401 | Authentication service error |
| `AUTH_008` | 403 | Insufficient permissions for this endpoint |

---

## 4. API Reference

### Base URL

```
http://localhost:8080          # Development
https://your-domain.com        # Production (behind Traefik)
```

### Common Headers

| Header | Required | Description |
|--------|----------|-------------|
| `X-API-Key` | Yes (protected routes) | Authentication key |
| `Content-Type` | Yes (POST requests) | Must be `application/json` |
| `X-Request-ID` | No | Custom request ID (auto-generated if absent) |

---

### 4.1 Public Endpoints (No Authentication)

#### GET /health

Basic liveness probe. Returns `200 OK` if the service is running.

**Response:**
```json
{
  "status": "ok",
  "timestamp": "2025-08-13T22:15:34.402652598Z",
  "service": "dcf-valuation-api"
}
```

---

#### GET /ready

Readiness probe. Verifies database, cache, and external API connectivity.

**Response:**
```json
{
  "status": "ready",
  "timestamp": "2025-08-13T22:15:34.402652598Z",
  "checks": {
    "database": "ok",
    "external_apis": "ok",
    "cache": "ok"
  }
}
```

---

#### GET /version

Returns build metadata.

**Response:**
```json
{
  "version": "0.9.0-rc1",
  "environment": "development",
  "build_time": "2025-08-13T15:00:00Z",
  "git_commit": "abc123def456"
}
```

---

#### GET /metrics

Prometheus-format metrics for monitoring systems. Exposes 28+ metrics covering HTTP requests, valuations, cache performance, rate limiting, and data source health.

**Response:** Prometheus text format (`text/plain`)

---

### 4.2 Valuation Endpoints

#### GET /api/v1/fair-value/:ticker

Calculate the intrinsic fair value for a single stock.

**Permission:** `read:fair_value`

**Path Parameters:**

| Parameter | Type | Constraints | Description |
|-----------|------|-------------|-------------|
| `ticker` | string | 1-5 alphanumeric chars, uppercase | Stock ticker symbol (e.g., `AAPL`) |

**Query Parameters (optional):**

| Parameter | Type | Range | Description |
|-----------|------|-------|-------------|
| `override_beta` | float | 0.0 - 3.0 | Custom beta for WACC calculation |
| `override_rf` | float | 0.0 - 0.2 | Custom risk-free rate |

**Example Request:**
```bash
curl -H "X-API-Key: <key>" \
     "http://localhost:8080/api/v1/fair-value/AAPL?override_beta=1.2&override_rf=0.045"
```

**Success Response (200 OK):**
```json
{
  "ticker": "AAPL",
  "wacc": 0.092,
  "growth_rate": 0.045,
  "growth_rates": [0.05, 0.048, 0.046, 0.044, 0.042],
  "growth_source": "analyst_blend",
  "growth_confidence": "high",
  "tangible_value_per_share": 24.73,
  "dcf_value_per_share": 156.42,
  "as_of": "2025-08-13T22:15:34Z",
  "data_quality_score": 85.5,
  "data_quality_grade": "B",
  "calculation_method": "multi_stage_dcf",
  "calculation_version": "4.0",
  "warnings": [],
  "sanity_check": {
    "implied_pe": 18.5,
    "implied_ev_ebitda": 14.2,
    "implied_p_fcf": 22.1,
    "is_reasonable": true,
    "flags": []
  }
}
```

**Response Fields Explained:**

| Field | Description |
|-------|-------------|
| `wacc` | Weighted Average Cost of Capital used for discounting |
| `growth_rate` | Blended first-stage growth rate |
| `growth_rates` | Per-year growth rates across projection period |
| `growth_source` | How growth was estimated: `analyst_blend`, `historical_only`, `default` |
| `growth_confidence` | Confidence level: `high`, `medium`, `low` |
| `tangible_value_per_share` | Net tangible book value per share (floor value) |
| `dcf_value_per_share` | DCF-derived intrinsic value per share |
| `data_quality_score` | 0-100 score reflecting data freshness and completeness |
| `data_quality_grade` | Letter grade: A (90+), B (80+), C (70+), D (60+), F (<60) |
| `calculation_method` | Model used: `multi_stage_dcf`, `ddm`, `ffo`, `revenue_multiple` |
| `sanity_check` | Cross-validation against sector median multiples |

**Error Responses:**

| Status | Code | When |
|--------|------|------|
| 400 | `INVALID_TICKER` | Ticker format is invalid |
| 400 | `INVALID_PARAMETER` | override_beta or override_rf out of range |
| 404 | `TICKER_NOT_FOUND` | Ticker is not present in SEC's ticker→CIK index (genuinely unknown symbol) |
| 422 | `INSUFFICIENT_DATA` | Ticker exists but cannot be valued — e.g. SEC has no usable US-GAAP XBRL facts (common for foreign private issuers filing 20-F and some pre-revenue issuers), or fewer than the required financial periods |
| 422 | `MODEL_NOT_APPLICABLE` | No valuation model can be applied |
| 429 | `RATE_LIMIT_EXCEEDED` | Rate limit exceeded |
| 500 | `CALCULATION_ERROR` | Internal calculation failure |

---

#### POST /api/v1/fair-value/bulk

Calculate fair values for multiple stocks in a single request. Supports partial success - some tickers may succeed while others fail.

**Permission:** `read:fair_value`

**Request Body:**
```json
{
  "tickers": ["AAPL", "MSFT", "GOOGL", "INVALID"],
  "override_beta": 1.2,
  "override_rf": 0.045
}
```

| Field | Type | Required | Constraints | Description |
|-------|------|----------|-------------|-------------|
| `tickers` | string[] | Yes | 1-10 items, 1-5 chars each | Ticker symbols |
| `override_beta` | float | No | 0.0 - 3.0 | Shared beta override |
| `override_rf` | float | No | 0.0 - 0.2 | Shared risk-free rate override |

**Response (207 Multi-Status for partial success):**
```json
{
  "results": [
    {
      "ticker": "AAPL",
      "wacc": 0.092,
      "dcf_value_per_share": 156.42,
      "data_quality_score": 85.5,
      "..."
    },
    {
      "ticker": "MSFT",
      "wacc": 0.088,
      "dcf_value_per_share": 310.15,
      "data_quality_score": 90.0,
      "..."
    }
  ],
  "failures": [
    {
      "ticker": "INVALID",
      "error_code": "INVALID_TICKER",
      "message": "Invalid ticker format: must be 1-5 alphanumeric characters"
    }
  ],
  "summary": {
    "total_requested": 4,
    "successful": 3,
    "failed": 1
  }
}
```

**HTTP Status Logic:**

| Status | Condition |
|--------|-----------|
| 200 OK | All tickers succeeded |
| 207 Multi-Status | Some succeeded, some failed |
| 422 Unprocessable Entity | All tickers failed |
| 400 Bad Request | Invalid request body |
| 429 Too Many Requests | Rate limit exceeded |

---

### 4.3 Health & Monitoring Endpoints

#### GET /api/v1/health/detailed

Comprehensive health check with component-level status.

**Permission:** `read:health`

**Response:**
```json
{
  "status": "healthy",
  "timestamp": "2025-08-13T22:15:34Z",
  "service": "dcf-valuation-api",
  "version": "v1.0.0",
  "uptime": "48h15m30s",
  "checks": {
    "database": {
      "status": "healthy",
      "last_checked": "2025-08-13T22:15:34Z",
      "duration": "15ms",
      "message": "Database connection active",
      "details": {
        "connection_pool_size": 25,
        "active_connections": 5
      }
    },
    "cache": {
      "status": "healthy",
      "..."
    },
    "external_apis": {
      "status": "healthy",
      "..."
    },
    "memory": {
      "status": "healthy",
      "..."
    }
  },
  "metadata": {
    "check_duration_ms": 150,
    "go_version": "go1.23.0",
    "num_goroutines": 45
  }
}
```

**HTTP Status Mapping:**

| Status | Meaning |
|--------|---------|
| 200 OK | All components healthy |
| 206 Partial Content | Some components degraded |
| 503 Service Unavailable | Critical components unhealthy |

---

#### GET /api/v1/metrics

Application and business metrics in JSON format.

**Permission:** `read:metrics`

**Response:**
```json
{
  "system": {
    "go_version": "go1.23.0",
    "num_goroutines": 45,
    "num_cpu": 8,
    "memory_alloc": 52428800,
    "gc_count": 125,
    "uptime_seconds": 3600
  },
  "application": {
    "total_requests": 5280,
    "active_connections": 12,
    "average_response_time": 45.5,
    "error_rate": 0.02,
    "cache_hit_rate": 0.85
  },
  "business": {
    "total_valuations": 1250,
    "successful_valuations": 1225,
    "failed_valuations": 25,
    "average_wacc": 0.089,
    "average_growth_rate": 0.042,
    "unique_tickers_served": 342
  },
  "timestamp": "2025-08-13T22:15:34Z"
}
```

---

### 4.4 API Key Management

#### POST /api/v1/auth/keys

Create a new API key. See [Authentication](#3-authentication) for details.

**Permission:** `manage:keys`

---

## 5. Valuation Engine

### 5.1 How Valuation Works

When you request a fair value, Midas executes the following pipeline:

```
Request (ticker, optional overrides)
  |
  v
[1] Data Acquisition
  |-- Check cache (4h TTL for default params)
  |-- Load from database (pre-seeded tickers)
  |-- OR fetch live from SEC + Yahoo Finance + FRED
  |
  v
[2] Data Cleaning & Normalization
  |-- Asset quality adjustments (goodwill, intangibles)
  |-- Liability completeness (leases, pensions)
  |-- Earnings normalization (one-time items)
  |-- Quality score calculation (0-100)
  |
  v
[3] Growth Estimation
  |-- Fetch analyst consensus (Yahoo Finance)
  |-- Calculate historical CAGR (5-year)
  |-- Compute ROIC sustainability ceiling
  |-- Confidence-weighted blend
  |-- Generate 3-stage growth rates
  |
  v
[4] WACC Calculation
  |-- Beta: raw -> Blume-adjusted -> unlevered -> relevered
  |-- Cost of Equity = Rf + Beta * MRP + CRP
  |-- Cost of Debt = Interest Expense / Total Debt * (1 - Tax)
  |-- WACC = We * CoE + Wd * CoD
  |
  v
[5] Model Selection (Industry-Aware)
  |-- Financial sector -> Dividend Discount Model (DDM)
  |-- REITs -> Funds From Operations (FFO)
  |-- Negative operating income -> Revenue Multiple
  |-- Default -> Multi-Stage DCF
  |
  v
[6] Valuation Calculation
  |-- Project free cash flows (7 years)
  |-- Calculate terminal value (Gordon Growth + Exit Multiple)
  |-- Discount to present value
  |-- Enterprise Value -> Equity Value -> Per Share
  |
  v
[7] Sanity Cross-Check
  |-- Compare implied P/E vs sector median
  |-- Compare implied EV/EBITDA vs sector median
  |-- Compare implied P/FCF vs sector median
  |-- Flag if outside 0.5x - 2.0x range
  |
  v
[8] Result (with quality score, warnings, sanity check)
```

### 5.2 Valuation Models

#### Multi-Stage DCF (Default)

The primary model for most companies. Uses a 3-stage growth projection:

- **Stage 1 (Years 1-3):** High-growth rate from analyst/historical blend
- **Stage 2 (Years 4-7):** Linear fade toward terminal rate
- **Terminal:** Gordon Growth Model at conservative terminal rate

**Free Cash Flow Formula:**
```
FCF = Operating Income * (1 - Tax Rate) + D&A - CapEx - Change in NWC
```

**Terminal Value** is the average of two methods:
1. **Gordon Growth:** `FCF_7 * (1 + g) / (WACC - g)` where g is capped at 3%
2. **Exit Multiple:** `Operating Income_7 * Sector EV/EBITDA Multiple`

Averaging both methods reduces model sensitivity to any single assumption.

**Enterprise to Equity Bridge:**
```
Equity Value = Enterprise Value - Total Debt + Cash
Value per Share = Equity Value / Diluted Shares Outstanding
```

#### Dividend Discount Model (DDM)

Used for **mature financial companies** (banks, insurance) with stable dividend histories.

```
Value per Share = DPS * (1 + g) / (Cost of Equity - g)
```

Where `DPS` is dividends per share and `g` is the dividend growth rate (capped at 70% of Cost of Equity).

Falls back to DCF if the company does not pay dividends.

#### Funds From Operations (FFO)

Used for **Real Estate Investment Trusts (REITs)**.

```
FFO = Net Income + D&A - Gains on Property Sales
Value per Share = (FFO / Shares) * P/FFO Multiple
```

The P/FFO multiple comes from `config/industry_multiples.json` (default: 15.0x).

#### Revenue Multiple

Used for **pre-profit companies** with negative operating income (early-stage tech, biotech).

```
Enterprise Value = Revenue * Sector EV/Revenue Multiple
Equity Value = EV - Debt + Cash
Value per Share = Equity Value / Shares
```

Always flagged as **LOW confidence** since it ignores profitability.

### 5.3 Growth Rate Estimation

Growth rates blend multiple signals with confidence-weighted averaging:

| Analyst Coverage | Blend | Confidence |
|-----------------|-------|------------|
| 10+ analysts | 80% analyst / 20% historical | HIGH |
| 3-9 analysts | 60% analyst / 40% historical | MEDIUM |
| 1-2 analysts | 40% analyst / 60% historical | LOW |
| 0 analysts | 100% historical | MEDIUM |

**Safety Guards:**
- Stage 1 growth capped to [-30%, +50%]
- If growth exceeds ROIC sustainability, it is blended downward
- Terminal growth: minimum 2%, maximum 3%, always at least 2% below WACC
- Analyst-historical divergence > 100% triggers a warning

### 5.4 WACC Calculation

```
Cost of Equity = Risk-Free Rate + Beta * Market Risk Premium + Country Risk Premium

Cost of Debt  = Interest Expense / Total Debt * (1 - Tax Rate)

WACC = (E / (E + D)) * Cost of Equity + (D / (E + D)) * Cost of Debt
```

**Beta Processing Pipeline:**
1. Raw beta from Yahoo Finance
2. **Blume adjustment:** dampens extreme values toward market average
3. **Unlever** at current D/E ratio
4. **Relever** at target/current capital structure

### 5.5 International (ADR) Support

Midas recognizes 50+ ADR tickers and applies **Country Risk Premiums (CRP)** to the cost of equity:

| Region | Example Tickers | CRP Range |
|--------|----------------|-----------|
| China | BABA, JD, BIDU | ~3.0% |
| Brazil | NU, VALE, PBR | ~4.5% |
| India | INFY, HDB | ~2.5% |
| Europe | SAP, ASML, AZN | ~0.5-1.0% |
| US | AAPL, MSFT, GOOGL | 0% (baseline) |

CRP data is loaded from `config/country_risk.json` (sourced from Damodaran).

### 5.6 Sanity Cross-Checks

After calculating the DCF value, Midas compares the implied multiples against sector medians:

| Metric | Formula | Flag Threshold |
|--------|---------|---------------|
| Implied P/E | DCF Value / EPS | > 2x or < 0.5x sector median |
| Implied EV/EBITDA | Enterprise Value / EBITDA | > 2x or < 0.5x sector median |
| Implied P/FCF | DCF Value / FCF per Share | > 2x or < 0.5x sector median |

Cross-check flags are **advisory only** - they do not invalidate the valuation but are included in the `warnings` array for transparency.

---

## 6. Data Sources

### 6.1 SEC EDGAR

| Field | Value |
|-------|-------|
| **URL** | `https://data.sec.gov/api/xbrl` |
| **Data** | 10-K and 10-Q filings (XBRL format) |
| **Rate Limit** | 10 requests/second (SEC policy) |
| **User-Agent** | Required - must include contact email |
| **Cache TTL** | 48 hours |

Provides: Revenue, operating income, D&A, CapEx, total assets, debt, equity, shares outstanding, tax rate, dividends, working capital, goodwill, and 40+ other financial concepts.

### 6.2 Yahoo Finance

| Field | Value |
|-------|-------|
| **URL** | `https://query2.finance.yahoo.com` |
| **Data** | Current prices, beta, market cap, analyst estimates |
| **Auth** | Cookie + crumb mechanism (auto-refreshed every 6 hours) |
| **Retries** | 3 with exponential backoff |
| **Cache TTL** | 15 minutes (market data) |

Provides: Share price, shares outstanding, beta (raw and 3-year), market cap, average volume, dividend yield, analyst revenue/earnings estimates, and number of covering analysts.

### 6.3 Finzive (Fallback)

| Field | Value |
|-------|-------|
| **URL** | `https://finzive.com` |
| **Data** | Financial data (web-scraped) |
| **Purpose** | Fallback when Yahoo Finance is unavailable |
| **Timeout** | 60 seconds |
| **Retries** | 2 |

### 6.4 FRED (Federal Reserve Economic Data)

| Field | Value |
|-------|-------|
| **URL** | `https://api.stlouisfed.org/fred` |
| **Data** | 10Y/2Y Treasury rates, inflation |
| **API Key** | Optional (falls back to manual defaults) |
| **Cache TTL** | 4 hours |

**Default Fallback Values** (when FRED is disabled):
- Risk-free rate: 4.5%
- Market risk premium: 5.0%

---

## 7. Data Quality & Cleaning

### 7.1 Normalization Pipeline

Every valuation runs financial data through four categories of adjustments:

**Category A - Asset Quality:**
- Excludes goodwill from tangible assets
- Writes down indefinite-lived intangible assets
- Marks obsolete inventory for writedown
- Adjusts deferred tax asset risk

**Category B - Liability Completeness:**
- Treats operating leases as debt (~10% of revenue)
- Adjusts for pension underfunding (~5% of revenue)
- Flags contingent liabilities (litigation, environmental)

**Category C - Earnings Normalization:**
- Adds back restructuring charges (one-time)
- Removes asset sale gains (non-recurring)
- Adjusts stock-based compensation (~1% of revenue)
- Flags R&D capitalization / SaaS deferred revenue

**Category D - Risk Warnings:**
- Excessive goodwill (> 25% of assets): WARNING
- Excessive intangibles (> 20% of assets): WARNING
- Working capital window dressing patterns
- Contingent liabilities > 5% of revenue

### 7.2 Quality Score

Every valuation includes a quality score (0-100) and letter grade:

| Grade | Score Range | Interpretation |
|-------|------------|----------------|
| A | 90-100 | Excellent data quality |
| B | 80-89 | Reliable financial data |
| C | 70-79 | Moderate quality, some adjustments |
| D | 60-69 | Problematic, significant adjustments |
| F | < 60 | Highly questionable data integrity |

**Score Deductions:**

| Condition | Penalty |
|-----------|---------|
| Financial data > 90 days old | -30 points |
| Market data > 7 days old | -20 points |
| Macro data > 30 days old | -20 points |
| Using NOPAT fallback (missing D&A/CapEx) | -15 points |
| Critical risk flag | -20 points per flag |
| Warning risk flag | -10 points per flag |
| Info risk flag | -5 points per flag |

---

## 8. Configuration Reference

Midas uses Viper for configuration. Settings can be provided via:
1. `config/config.yaml` (file)
2. Environment variables (override file settings)

**Environment variable mapping:** Nested YAML keys use `_` separator.
Example: `database.driver` becomes `DATABASE_DRIVER`

### 8.1 Server

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `port` | `PORT` | `8080` | HTTP server port |
| `server.read_timeout` | `SERVER_READ_TIMEOUT` | `30s` | Request read timeout |
| `server.write_timeout` | `SERVER_WRITE_TIMEOUT` | `30s` | Response write timeout |
| `server.idle_timeout` | `SERVER_IDLE_TIMEOUT` | `120s` | Keep-alive idle timeout |
| `log_level` | `LOG_LEVEL` | `debug` | Logging level (debug, info, warn, error) |
| `enable_swagger` | `ENABLE_SWAGGER` | `false` | Enable Swagger UI at `/swagger` |
| `enable_pprof` | `ENABLE_PPROF` | `false` | Enable Go pprof at `/debug/pprof` |

### 8.2 Database

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `database.driver` | `DATABASE_DRIVER` | `sqlite3` | Database driver (`sqlite3` or `postgres`) |
| `database.sqlite_path` | `DATABASE_SQLITE_PATH` | `./data/midas.db` | SQLite file path |
| `database.postgres_url` | `DATABASE_POSTGRES_URL` | _(required for postgres)_ | PostgreSQL connection URL |
| `database.max_open_conn` | `DATABASE_MAX_OPEN_CONN` | `25` | Max open connections |
| `database.max_idle_conn` | `DATABASE_MAX_IDLE_CONN` | `10` | Max idle connections |

### 8.3 Cache (Redis)

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `cache.redis_url` | `CACHE_REDIS_URL` | `redis://localhost:6379` | Redis connection URL |
| `cache.sec_filings_ttl` | `CACHE_SEC_FILINGS_TTL` | `48h` | SEC data cache duration |
| `cache.market_data_ttl` | `CACHE_MARKET_DATA_TTL` | `15m` | Market prices cache |
| `cache.macro_data_ttl` | `CACHE_MACRO_DATA_TTL` | `4h` | Macro indicators cache |
| `cache.valuation_result_ttl` | `CACHE_VALUATION_RESULT_TTL` | `1h` | DCF result cache |
| `cache.default_ttl` | `CACHE_DEFAULT_TTL` | `30m` | Default cache duration |

> **Note:** Redis is optional. If unavailable, the app falls back to in-memory caching.

### 8.4 SEC EDGAR

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `sec.base_url` | `SEC_BASE_URL` | `https://data.sec.gov/api/xbrl` | SEC API base URL |
| `sec.user_agent` | `SEC_USER_AGENT` | `Midas DCF API admin@example.com` | Required User-Agent header |
| `sec.rate_limit` | `SEC_RATE_LIMIT` | `10` | Max requests per second |
| `sec.request_timeout` | `SEC_REQUEST_TIMEOUT` | `30s` | Per-request timeout |
| `sec.max_retries` | `SEC_MAX_RETRIES` | `3` | Retry attempts |

### 8.5 Market Data

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `market.yfinance.enabled` | `MARKET_YFINANCE_ENABLED` | `true` | Enable Yahoo Finance |
| `market.yfinance.request_timeout` | `MARKET_YFINANCE_REQUEST_TIMEOUT` | `30s` | Request timeout |
| `market.yfinance.auth_ttl` | `MARKET_YFINANCE_AUTH_TTL` | `6h` | Cookie+crumb validity |
| `market.finzive.enabled` | `MARKET_FINZIVE_ENABLED` | `true` | Enable Finzive fallback |

### 8.6 Macro Data (FRED)

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `macro.fred_enabled` | `MACRO_FRED_ENABLED` | `false` | Enable FRED API |
| `macro.fred_api_key` | `MACRO_FRED_API_KEY` | _(required if enabled)_ | FRED API key |
| `macro.manual_risk_free_rate` | `MACRO_MANUAL_RISK_FREE_RATE` | `0.045` | Manual risk-free rate (when FRED disabled) |
| `macro.manual_market_risk_premium` | `MACRO_MANUAL_MARKET_RISK_PREMIUM` | `0.05` | Manual market risk premium |

### 8.7 Valuation

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `valuation.default_market_risk_premium` | - | `0.05` | Default equity risk premium |
| `valuation.default_terminal_growth_cap` | - | `0.03` | Max terminal growth (3%) |
| `valuation.default_tax_rate` | - | `0.21` | Corporate tax rate (21%) |
| `valuation.max_bulk_size` | `VALUATION_MAX_BULK_SIZE` | `50` | Max tickers per bulk request |
| `valuation.cache_ttl` | `VALUATION_CACHE_TTL` | `1h` | Valuation result cache |
| `valuation.data_fetch_timeout` | `VALUATION_DATA_FETCH_TIMEOUT` | `10s` | Data fetching timeout |
| `valuation.dcf_projection_years` | - | `5` | DCF forecast horizon |
| `valuation.dcf_max_growth_rate` | - | `0.5` | Growth rate ceiling (50%) |
| `valuation.dcf_min_growth_rate` | - | `-0.3` | Growth rate floor (-30%) |

### 8.8 Data Cleaner

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `datacleaner.enabled` | - | `true` | Enable cleaning pipeline |
| `datacleaner.enable_ai_integration` | `DATACLEANER_ENABLE_AI_INTEGRATION` | `false` | Enable AI footnote analysis |
| `datacleaner.min_quality_score` | - | `60.0` | Minimum acceptable quality |
| `datacleaner.enable_risk_flags` | - | `true` | Enable risk flag detection |
| `datacleaner.enable_caching` | - | `true` | Cache cleaned results |

### 8.9 Scheduler

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `scheduler.enabled` | `SCHEDULER_ENABLED` | `false` | Enable background scheduler |
| `scheduler.interval` | - | `24h` | Batch refresh interval |
| `scheduler.max_concurrency` | - | `2` | Concurrent fetch workers |

---

## 9. Deployment Guide

### 9.1 Development (Docker Compose)

```bash
docker-compose up -d
```

This starts:
- **dcf-api** - The API server with SQLite, debug logging, Swagger UI, and pprof
- **redis** - Redis 7 Alpine with 256MB LRU cache
- **redis-commander** (optional, debug profile) - Web UI on port 8081

**Volumes:**
- `dcf_data` - Persistent SQLite database
- `dcf_logs` - Application logs
- `redis_data` - Redis AOF persistence

### 9.2 Production (Docker Compose)

```bash
docker-compose -f docker-compose.prod.yml up -d
```

Production deployment includes:
- **dcf-api** - 2 replicas, PostgreSQL, release mode, resource limits (2 CPU / 1GB RAM)
- **traefik** - Reverse proxy with automatic Let's Encrypt SSL
- **prometheus** (optional) - Metrics collection with 200h retention
- **grafana** (optional) - Dashboards and alerting

**Production Configuration:**
- Database: External PostgreSQL (via `DATABASE_POSTGRES_URL`)
- Cache: External Redis (via `REDIS_URL`)
- Rolling deploys: 1 replica at a time, 10s delay, 30% failure threshold with rollback
- Health checks: 30s intervals, 10s timeout, 3 retries

### 9.3 Manual Deployment

```bash
# Build optimized binary
CGO_ENABLED=1 go build -ldflags="-w -s" -o dcf-api ./cmd/server

# Build migration tool
CGO_ENABLED=1 go build -ldflags="-w -s" -o dcf-migrate ./cmd/migrate

# Run migrations
./dcf-migrate -db /path/to/midas.db

# Start server
ENV=production LOG_LEVEL=info GIN_MODE=release ./dcf-api
```

### 9.4 Staging Scripts

```bash
# Linux/macOS
./scripts/launch_staging.sh

# Windows
.\scripts\launch_staging.ps1
```

### 9.5 Docker Image

The Dockerfile uses a multi-stage build:

1. **Builder** (golang:1.23-alpine): Compiles with CGO for SQLite, strips debug symbols
2. **Runtime** (alpine:3.19): Minimal image with non-root user (UID 1001), ca-certificates, and timezone data

The entrypoint script optionally runs migrations on startup when `RUN_MIGRATIONS=true`.

---

## 10. Monitoring & Observability

### 10.1 Prometheus Metrics

Available at `GET /metrics` (no auth). Key metrics include:

**HTTP Metrics:**
- `midas_http_requests_total` - Total HTTP requests (method, path, status)
- `midas_http_request_duration_seconds` - Request latency histogram
- `midas_http_requests_in_flight` - Currently active requests

**Valuation Metrics:**
- `midas_valuations_total` - Total valuations (ticker, method, status)
- `midas_valuation_duration_seconds` - Valuation calculation time
- `midas_dcf_calculations_total` - DCF calculations performed
- `midas_wacc_calculations_total` - WACC calculations performed

**Data Source Metrics:**
- `midas_sec_api_requests_total` - SEC API calls (status)
- `midas_market_api_requests_total` - Market data API calls
- `midas_macro_api_requests_total` - Macro data API calls

**Cache Metrics:**
- `midas_cache_requests_total` - Cache operations (hit/miss)
- `midas_cache_hit_ratio` - Cache hit rate gauge

**Rate Limiting:**
- `midas_rate_limit_exceeded_total` - Rate limit violations

### 10.2 Structured Logging

All logs are structured JSON via `go.uber.org/zap`:

```json
{
  "level": "info",
  "ts": "2025-08-13T22:15:34.402Z",
  "caller": "api/server.go:123",
  "msg": "request completed",
  "method": "GET",
  "path": "/api/v1/fair-value/AAPL",
  "status": 200,
  "latency": "1.234s",
  "client_ip": "192.168.1.1",
  "request_id": "abc-123-def"
}
```

### 10.3 Request Tracing

Every request receives a unique `X-Request-ID` header (auto-generated or passed through from the client). This ID is:
- Logged with every related log entry
- Returned in the response headers
- Stored in the audit log for compliance

### 10.4 Health Checks

| Endpoint | Purpose | Auth | Status Mapping |
|----------|---------|------|---------------|
| `GET /health` | Liveness probe (K8s) | No | 200 = alive |
| `GET /ready` | Readiness probe (K8s) | No | 200 = ready to serve |
| `GET /api/v1/health/detailed` | Deep component check | Yes | 200/206/503 |

---

## 11. Error Handling

### 11.1 RFC 7807 Problem Details

All error responses follow the [RFC 7807](https://tools.ietf.org/html/rfc7807) Problem Details format with Midas-specific extensions:

```json
{
  "type": "https://problems.midas.dev/INVALID_TICKER",
  "title": "Bad Request",
  "status": 400,
  "detail": "Invalid ticker format: must be 1-5 alphanumeric characters",
  "instance": "/api/v1/fair-value/!!!",
  "code": "INVALID_TICKER",
  "timestamp": "2025-08-13T22:15:34.402652598Z",
  "method": "GET",
  "context": {
    "ticker": "!!!",
    "pattern": "^[A-Z0-9]{1,5}$"
  }
}
```

### 11.2 Error Code Reference

| Code | HTTP Status | Description |
|------|------------|-------------|
| `INVALID_TICKER` | 400 | Ticker format is invalid (must be 1-5 alphanumeric chars) |
| `INVALID_PARAMETER` | 400 | Query or body parameter is out of valid range |
| `INVALID_REQUEST` | 400 | Request body doesn't match expected schema |
| `TICKER_NOT_FOUND` | 404 | Ticker is not present in SEC's ticker→CIK index (genuinely unknown symbol) |
| `INSUFFICIENT_DATA` | 422 | Ticker resolves but cannot be valued: SEC has no usable US-GAAP XBRL facts (foreign private issuers, some pre-revenue issuers) or too few financial periods |
| `MODEL_NOT_APPLICABLE` | 422 | No valuation model can be applied to this company |
| `CALCULATION_ERROR` | 500 | Internal error during valuation calculation |
| `RATE_LIMIT_EXCEEDED` | 429 | Rate limit exceeded (check Retry-After header) |
| `AUTH_001` - `AUTH_008` | 401/403 | Authentication/authorization errors (see [Auth section](#3-authentication)) |

### 11.3 Graceful Degradation

Midas is designed to degrade gracefully rather than fail hard:

| Failure | Fallback |
|---------|----------|
| SEC data unavailable | Use cached/repository data |
| Yahoo Finance down | Use Finzive fallback |
| Redis unavailable | Use in-memory cache |
| FRED API disabled | Use manual rate defaults (4.5% Rf, 5% MRP) |
| Analyst data missing | Use 100% historical CAGR |
| D&A/CapEx unavailable | NOPAT approximation (-15 quality penalty) |
| Industry multiples missing | Skip exit-multiple terminal value and cross-checks |
| Country risk config missing | Default all CRP to 0% (US treatment) |
| DDM model fails | Fall back to DCF |
| All models fail | Return error with explanation |

---

## 12. Rate Limiting

### 12.1 Rate Limit Levels

Rate limits are checked in order. The most restrictive wins:

1. **API Key Rate Limit** - Per individual API key (default: 1000 req/period)
2. **IP Rate Limit** - Per client IP address
3. **Endpoint Rate Limit** - Per endpoint path
4. **Global Rate Limit** - Across all requests (1000 req/min default)

**Default Endpoint Limits:**
- Fair Value: 60 requests/minute
- Health endpoints: 30 requests/minute

### 12.2 Rate Limit Headers

All responses include rate limit information:

| Header | Description |
|--------|-------------|
| `X-RateLimit-Limit` | Total allowed requests in current window |
| `X-RateLimit-Remaining` | Requests remaining in current window |
| `X-RateLimit-Reset` | Unix timestamp when the window resets |
| `Retry-After` | Seconds to wait (only on 429 responses) |

### 12.3 Rate Limit Exceeded Response

```json
{
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Rate limit exceeded",
    "type": "rate_limit_error"
  },
  "rate_limit": {
    "remaining": 0,
    "reset_time": 1692010534,
    "retry_after": 45
  },
  "timestamp": "2025-08-13T22:15:34Z",
  "path": "/api/v1/fair-value/AAPL",
  "method": "GET"
}
```

---

## 13. Security

### 13.1 Security Headers

All responses include:

| Header | Value | Purpose |
|--------|-------|---------|
| `X-Content-Type-Options` | `nosniff` | Prevent MIME type sniffing |
| `X-Frame-Options` | `DENY` | Prevent clickjacking |
| `X-XSS-Protection` | `1; mode=block` | Enable browser XSS filtering |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | Force HTTPS |
| `Content-Security-Policy` | `default-src 'self'` | Restrict resource loading |

### 13.2 CORS Configuration

| Setting | Value |
|---------|-------|
| Allowed Origins | `*` (configure for production) |
| Allowed Methods | GET, POST, PUT, DELETE, OPTIONS |
| Allowed Headers | Origin, Content-Type, Authorization, X-API-Key, X-Request-ID |
| Exposed Headers | Content-Length, X-Request-ID |
| Credentials | Allowed |
| Max Age | 12 hours |

### 13.3 API Key Security

- Keys are hashed with **SHA-256** before storage
- Raw keys are only returned once (at creation time)
- Keys can be individually **deactivated** or set to **expire**
- Usage is tracked per-endpoint with timestamps and IP addresses
- Failed authentication attempts are logged with key prefix (safe) for diagnostics

### 13.4 Best Practices

- **Never commit API keys** - Use environment variables or vault references
- **Rotate keys regularly** - Create new keys and deactivate old ones
- **Use least-privilege permissions** - Only grant the permissions each client needs
- **Monitor the audit log** - Watch for unusual access patterns
- **Set key expiration** - Use `expires_at` for temporary access
- **Configure CORS for production** - Restrict `AllowOrigins` to your domain(s)

---

## 14. Architecture & Design

### 14.1 Clean Architecture

Midas follows **Hexagonal Architecture** (Ports & Adapters) with strict dependency rules:

```
                      +-----------------+
                      |    cmd/server   |  (Entry point)
                      +--------+--------+
                               |
                      +--------v--------+
                      |    internal/di  |  (Dependency injection via uber/fx)
                      +--------+--------+
                               |
              +----------------+----------------+
              |                |                |
     +--------v-------+ +-----v------+ +-------v--------+
     |  internal/api   | | internal/  | | internal/infra |
     |  (HTTP layer)   | | services   | | (Adapters)     |
     +--------+-------+ +-----+------+ +-------+--------+
              |                |                |
              +-------+--------+--------+-------+
                      |                 |
              +-------v-------+ +-------v--------+
              | internal/core | | internal/core  |
              |   /entities   | |    /ports      |
              | (Domain)      | | (Interfaces)   |
              +---------------+ +----------------+
```

**Key Principles:**
- **Domain layer** (`core/`) has zero external dependencies
- **All external I/O** is defined as interfaces in `core/ports/`
- **Adapters** (`infra/`) implement port interfaces
- **Services** contain business logic, depend only on ports
- **DI container** (`di/`) wires everything together via `uber/fx`

### 14.2 Database Schema

15 core tables supporting both **SQLite** (development) and **PostgreSQL** (production):

| Table | Purpose |
|-------|---------|
| `companies` | Master company registry (ticker, CIK, sector) |
| `financial_data` | Normalized SEC filing fundamentals |
| `market_data` | Daily pricing, beta, volume |
| `macro_data` | Treasury rates, market risk premium |
| `ticker_mapping` | Ticker to CIK lookup |
| `valuation_results` | Cached DCF calculation results |
| `cache_metadata` | Cache expiration and hit tracking |
| `raw_sec_data` | Optional raw SEC JSON storage |
| `audit_log` | Event tracking for compliance |
| `api_keys` | Authentication and rate limits |
| `api_key_usage` | Per-request usage tracking |
| `scheduler_watchlist` | Background refresh queue |

**Useful Views:**
- `latest_financial_data` - Most recent filing per ticker
- `latest_market_data` - Latest pricing per ticker
- `complete_valuation_data` - Denormalized join for DCF calculations

### 14.3 Resilience Patterns

**Circuit Breaker** (per external service):
- **CLOSED** (normal): Pass through, track failures
- **OPEN** (after 5 consecutive failures): Fast-fail all requests for 30 seconds
- **HALF_OPEN** (recovery): Allow limited requests, close after 3 successes

**Retry Policy** (all external API calls):
- Max 3 attempts
- Exponential backoff: 100ms, 200ms, 400ms (capped at 5s)
- Random jitter to prevent thundering herd

### 14.4 Middleware Stack

Requests are processed through this middleware chain (in order):

1. **Request ID** - Assigns/passes through `X-Request-ID`
2. **Security Headers** - Adds all security headers
3. **Metrics** - Records Prometheus metrics
4. **Recovery** - Catches panics, returns 500
5. **Logging** - Structured request/response logging
6. **CORS** - Cross-origin resource sharing
7. **Rate Limiting** - Per-key/IP rate enforcement
8. **Auth** (protected routes) - API key validation
9. **Permission** (protected routes) - Permission checking

---

## 15. Troubleshooting

### Common Issues

**"CGO is required" build error**
```bash
# Ensure CGO is enabled (required for SQLite driver)
CGO_ENABLED=1 go build ./cmd/server
```

**SEC API returns 403/429**
- Ensure `SEC_USER_AGENT` includes a valid contact email
- Respect the 10 req/sec rate limit
- Check if your IP has been temporarily blocked

**Yahoo Finance authentication failures**
- Cookie+crumb auth refreshes every 6 hours
- Check network connectivity to `fc.yahoo.com`
- Examine logs for `yfinance_auth` errors

**Redis connection refused**
- Redis is optional - the app falls back to in-memory cache
- Set `CACHE_REDIS_URL` to a valid Redis URL, or let it default

**Empty valuation results**
- Verify the ticker exists in SEC EDGAR
- Check that financial data is available (at least 2 periods)
- Look for `INSUFFICIENT_DATA` or `MODEL_NOT_APPLICABLE` errors
- `INSUFFICIENT_DATA` (422) with a `no US-GAAP XBRL facts` message indicates the ticker is real but SEC has no usable financial data — commonly foreign private issuers (e.g. Canadian pharmas filing 20-F) or early-stage pre-revenue companies. Not a bug; Midas cannot value these without a US-GAAP source.

**Stale data warnings**
- Run the scheduler (`SCHEDULER_ENABLED=true`) for automatic refresh
- Or re-trigger a fetch by clearing the cache

### Testing

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific package
go test ./internal/services/valuation/...

# Run integration tests
go test ./internal/integration/...

# Contract fuzz testing
./scripts/contract_fuzz.ps1 -DemoKey '<key>' -ApiBase 'http://localhost:8080' -DbPath './data/midas.db'

# Load testing
go run ./scripts/load_tester.go \
  -url http://localhost:8080 \
  -key <API_KEY> \
  -type single \
  -concurrency 20 \
  -duration 60s \
  -rps 20
```

### Utility Commands

```bash
# Hash an API key (for manual database insertion)
go run ./cmd/hash-key -key <your-key>

# Apply database schema
go run ./cmd/migrate -db ./data/midas.db

# Seed demo API key
go run ./cmd/seed-demo-key -db ./data/midas.db
```

---

## 16. Glossary

| Term | Definition |
|------|-----------|
| **ADR** | American Depositary Receipt - US-listed shares of a foreign company |
| **Beta** | Measure of a stock's volatility relative to the market |
| **Blume Adjustment** | Statistical technique to dampen extreme beta values toward 1.0 |
| **CAGR** | Compound Annual Growth Rate |
| **CapEx** | Capital Expenditures - spending on fixed assets |
| **CIK** | Central Index Key - SEC's unique identifier for companies |
| **CRP** | Country Risk Premium - additional return for investing in riskier countries |
| **D&A** | Depreciation and Amortization |
| **DCF** | Discounted Cash Flow - valuation method based on projected future cash flows |
| **DDM** | Dividend Discount Model - values companies based on expected dividends |
| **DPS** | Dividends Per Share |
| **EBITDA** | Earnings Before Interest, Taxes, Depreciation, and Amortization |
| **EPS** | Earnings Per Share |
| **EV** | Enterprise Value = Market Cap + Debt - Cash |
| **FCF** | Free Cash Flow = Operating Income * (1-Tax) + D&A - CapEx - delta NWC |
| **FFO** | Funds From Operations - REIT-specific earnings metric |
| **FRED** | Federal Reserve Economic Data |
| **Gordon Growth** | Terminal value model: `FCF*(1+g)/(WACC-g)` |
| **MRP** | Market Risk Premium - excess return of stocks over risk-free rate |
| **NOPAT** | Net Operating Profit After Tax |
| **NWC** | Net Working Capital = Current Assets - Current Liabilities |
| **P/E** | Price-to-Earnings ratio |
| **P/FCF** | Price-to-Free-Cash-Flow ratio |
| **ROIC** | Return on Invested Capital |
| **SEC EDGAR** | Securities and Exchange Commission's Electronic Data Gathering system |
| **Terminal Value** | Value of all cash flows beyond the explicit forecast period |
| **WACC** | Weighted Average Cost of Capital - blended discount rate |
| **XBRL** | eXtensible Business Reporting Language - SEC filing data format |

---

## Appendix A: Postman Collection

A Postman collection is available at `docs/postman_collection.json`. Import it into Postman and set:

- **Variable `base_url`**: `http://localhost:8080`
- **Variable `api_key`**: Your API key from `seed-demo-key`

## Appendix B: OpenAPI Specification

The full OpenAPI 3.0.3 specification is available at:
- `docs/openapi.yaml` (YAML format)
- `docs/swagger.json` (JSON format)
- `http://localhost:8080/swagger` (interactive Swagger UI, when enabled)

## Appendix C: Configuration Files

| File | Purpose |
|------|---------|
| `config/config.yaml` | Main application configuration |
| `config/country_risk.json` | Damodaran country risk premiums (30+ countries) |
| `config/industry_multiples.json` | Sector median P/E, EV/EBITDA, EV/Revenue, P/FFO multiples |
| `config/datacleaner/rules.json` | Financial data cleaning rules |
| `config/datacleaner/industry/` | Industry-specific cleaning rules |
| `config/datacleaner/schema.json` | Data validation schema |
| `config/alerting/` | Alert rules and notification channels |
