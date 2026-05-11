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
| Multi-Stage DCF | 3-stage growth model: high-growth, fade, terminal (7-year explicit projection) |
| Industry-Aware Models | Auto-selects DDM (banks), FFO (REITs, with subsector calibration), Revenue Multiple (pre-profit, TTM-based), or DCF |
| Sub-Industry Sector Multiples | Sub-industry-keyed EV/Revenue, EV/EBITDA, P/E, P/FFO, and cap rate tables — semis (`MFG_SEMI`), banks (`FIN_BANK`), data center / cell tower / industrial REITs, etc. |
| Graham-Floor Diagnostics | Per-share asset-floor block (`current_assets_per_share`, `ncav_per_share`, `graham_floor_per_share`, `graham_discount_pct`) on every valuation, independent of operating earnings/growth/WACC |
| TTM Revenue Base | `revenue_multiple` model uses Trailing-Twelve-Months revenue with a 5-tier fallback chain (`TTM_PRIOR_BRIDGE` → `TTM_4Q` → `ANNUAL_FY` → `ANNUALIZED_QUARTER` → `INSUFFICIENT_HISTORY`) and surfaces the source path in warnings for replay-tooling auditability |
| International (ADR) Support | Country risk premium adjustments for 50+ ADR tickers |
| IFRS-FPI Pipeline | Foreign private issuers (TSM, ASML, BABA, …) auto-converted to USD per-ADR via `ifrs-full` taxonomy + FRED FX rates |
| Data Quality Scoring | 0-100 score with A-F grade on every valuation |
| Bulk Valuations | Value up to 10 tickers in a single request (capped by `valuation.max_bulk_size`) |
| Analyst Consensus Blending | Merges Yahoo Finance analyst estimates with historical data |
| Sanity Cross-Checks | Compares DCF-implied multiples against sector medians; REITs use subsector-specific cap rates for NAV cross-check |
| Per-Request Artifact Bundles | Opt-in or auto-on-error / on-quality-flag / always-on capture of full request narrative to disk |
| Replay Tooling for Regression Testing | `cmd/replay` re-runs captured artifact bundles through the current code and diffs the response against the saved `17-response.json`. Use it to check whether a code change moved any of your watchlist's fair values. See [§10.7](#107-replay-tooling). |
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

**Success Response (200 OK) — healthy positive-NCAV case:**
```json
{
  "ticker": "EXAMPLE",
  "wacc": 0.092,
  "growth_rate": 0.045,
  "growth_rates": [0.05, 0.048, 0.046, 0.044, 0.042, 0.038, 0.034],
  "growth_source": "analyst_blend",
  "growth_confidence": "high",
  "tangible_value_per_share": 38.20,
  "dcf_value_per_share": 84.50,
  "current_assets_per_share": 55.13,
  "ncav_per_share": 4.55,
  "graham_floor_per_share": 3.03,
  "graham_discount_pct": 23.30,
  "as_of": "2026-05-10T22:15:34Z",
  "data_quality_score": 90,
  "data_quality_grade": "A",
  "calculation_method": "multi_stage_dcf",
  "calculation_version": "4.1",
  "warnings": [],
  "sanity_check": {
    "implied_pe": 18.5,
    "implied_ev_ebitda": 14.2,
    "implied_pfcf": 22.1,
    "is_reasonable": true,
    "flags": []
  },
  "industry": {
    "sic_code": "3571",
    "sic": "TECH",
    "heuristic_code": "45",
    "heuristic_name": "Information Technology",
    "match": true
  },
  "currency": "USD",
  "adr_ratio_applied": 1,
  "current_price": 73.64
}
```

**Distressed (deep-value) case — negative NCAV, floor clamped to 0:**

`graham_floor_per_share` stays in JSON as `0` (the data-says-zero signal); `graham_discount_pct` is **omitted** (no meaningful ratio against a zero floor):

```json
{
  "ticker": "DISTRESSED",
  "wacc": 0.105,
  "tangible_value_per_share": 1.20,
  "dcf_value_per_share": 12.40,
  "current_assets_per_share": 2.85,
  "ncav_per_share": -0.765,
  "graham_floor_per_share": 0,
  "calculation_method": "revenue_multiple",
  "calculation_version": "4.1",
  "current_price": 11.20,
  "warnings": [
    "Applied 6.5x EV/Revenue multiple for MFG_SEMI sector",
    "revenue_base: source=ANNUAL_FY revenue=$467641000",
    "revenue_base: TTM unavailable, used latest FY ($467641000 dated 2026-01-29) [2025FY]"
  ]
}
```

**Unresolved-liabilities case — all four Graham fields absent:**

When `total_liabilities` cannot be sourced (umbrella XBRL tag missing AND derivation `TotalAssets − StockholdersEquity` produces a non-positive value — typically the cleaner-asymmetry signature documented in `docs/reviewer/DC-1-...`), all four Graham fields drop from the JSON and a single warning is appended:

```json
{
  "ticker": "UNRESOLVED",
  "warnings": [
    "graham_floor: insufficient balance-sheet data (total_liabilities unresolved)"
  ]
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
| `tangible_value_per_share` | Net tangible book value per share (floor value). Denominator priority chain: **diluted shares first**, then market basic, then financial basic — same chain as `dcf_value_per_share`, `ncav_per_share`, and the Graham fields. Pre-v0.10.0 builds used market-basic first; expect a ~0.4-1% downward drift on first recompute for large-caps with options/RSU/convertible dilution |
| `dcf_value_per_share` | DCF-derived intrinsic value per share. For tickers routed to non-DCF models (DDM, FFO, revenue_multiple) this field carries that model's per-share output — `calculation_method` distinguishes which math fired |
| `current_assets_per_share` | Current assets ÷ diluted shares — pure asset-side floor with no liability subtraction. Omitted when `total_liabilities` cannot be resolved |
| `ncav_per_share` | Graham's Net Current Asset Value per share: `(current_assets − total_liabilities) / diluted_shares`. **May be negative** for distressed companies (raw value, no clamping). Omitted when `total_liabilities` cannot be resolved |
| `graham_floor_per_share` | Graham's "buy below" trigger: `max(ncav_per_share × 2/3, 0)`. Clamps to 0 when NCAV is negative — that case represents "no asset floor exists, the company has more obligations than liquid assets" |
| `graham_discount_pct` | `(current_price − graham_floor_per_share) / graham_floor_per_share`. Positive = price above floor; negative = price below floor (Graham net-net territory). **Returned as `null` and omitted from JSON when `graham_floor_per_share` is 0** |
| `data_quality_score` | 0-100 score reflecting data freshness and completeness |
| `data_quality_grade` | Letter grade: A (90+), B (80+), C (70+), D (60+), F (<60) |
| `calculation_method` | Model used: `multi_stage_dcf`, `ddm`, `ffo`, `revenue_multiple` |
| `sanity_check` | Cross-validation against sector median multiples |
| `industry` | Dual industry classification (SIC + heuristic) — see [Industry Classification](#421-industry-classification) |
| `currency` | ISO-4217 code that all monetary per-share fields are denominated in. Always `"USD"` — non-USD reporting currencies are FX-converted upstream by the IFRS-FPI pipeline |
| `adr_ratio_applied` | Ordinary-shares-per-ADR multiplier the engine divided SEC share counts by before computing per-share values. `1` for domestic 10-K filers; non-1 for ADRs (TSM=5, BABA=8). Omitted when zero |
| `current_price` | Live per-share market price from Yahoo/Finzive at calculation time, in the same per-share basis as `dcf_value_per_share` (per-ADR for ADRs). Compute upside/downside as `(dcf_value_per_share - current_price) / current_price` without a second quote lookup. Omitted when unavailable |

##### 4.2.1 Industry Classification

The `industry` object exposes both classifiers the engine runs on every request:

| Sub-field | Source | Description |
|-----------|--------|-------------|
| `sic_code` | SEC filing header | Raw SIC code (e.g., `3674` for semiconductors) |
| `sic` | `IndustryClassifier.Classify` | High-level label from SIC code + company name. Top-level codes: `TECH`, `MFG`, `RETAIL`, `UTIL`, `FIN`, `HEALTH`, `ENERGY`, `RESTATE`, `TELECOM`, `TRANS`, `CONS`. Sub-industry refinements: `TECH_SAAS`, `TECH_AI`, `MFG_SEMI` (fabless / IDM semis), `HEALTH_BIOTECH`, `HEALTH_PHARMA`, `FIN_BANK`, `FIN_INSURANCE`, `FIN_IB`. REIT subsectors (when `RESTATE` parent matches): `RESIDENTIAL`, `INDUSTRIAL`, `OFFICE`, `RETAIL_REIT`, `HEALTHCARE_REIT`, `DATA_CENTER`, `CELLTOWER`, `SPECIALTY` |
| `heuristic_code` | `IndustryClassifier.ClassifyIndustry` | GICS sector code from balance-sheet ratios: `45` (IT), `25` (Consumer Discretionary), `20` (Industrials), `35` (Health Care), `55` (Utilities), `40` (Financials), `50` (Communication Services), `60` (Real Estate), `30` (Consumer Staples), `10` (Energy) |
| `heuristic_name` | `IndustryClassifier.ClassifyIndustry` | Human-readable GICS sector name |
| `match` | Computed in handler | `true` when `sic` and `heuristic_code` agree per a canonical SIC→GICS mapping (sub-industries normalize to their parent or have explicit entries); `false` signals classification drift |

**Canonical match mapping** (handler-owned):

| `sic` label | Matching `heuristic_code` values |
|-------------|----------------------------------|
| `TECH` | `45` |
| `MFG` | `20`, `45` (semiconductors and hardware mfrs are both Industrials *and* IT — deliberate multi-map) |
| `RETAIL` | `25`, `30` (grocery/staples retailers) |
| `UTIL` | `55` |
| `FIN` | `40` |
| `HEALTH` | `35` |
| `ENERGY` | `10` |
| `RESTATE` | `60` |
| `TELECOM` | `50` |
| `TRANS` | `20` |
| `CONS` | `30`, `25` |
| `RESIDENTIAL`, `INDUSTRIAL`, `OFFICE`, `RETAIL_REIT`, `HEALTHCARE_REIT`, `DATA_CENTER`, `CELLTOWER`, `SPECIALTY` | `60` (each REIT subsector has its own explicit `sicToGICS` entry — exact-match-first lookup prevents `RETAIL_REIT` falling through prefix-strip to `RETAIL → 25`) |

Sub-industry codes for non-REIT sectors (`TECH_SAAS`, `MFG_SEMI`, `HEALTH_BIOTECH`, `FIN_BANK`, …) normalize to their parent prefix for match computation. REIT subsector codes have explicit map entries because they would otherwise prefix-strip into the wrong sector.

**Known classifier gaps** (tracked in `docs/refactoring/industry-classification-unification-spec.md`):

- **Financials** (`sic = "FIN"`): `ClassifyIndustry` has no GICS-40 config and defaults to `20` → `match: false` for banks like JPM. Not drift — a known heuristic config gap.
- **Owned-store retailers** (Target, Home Depot, Costco, Lowe's): the heuristic's retail predicate rejects retailers with tangibles > 70% and intangibles < 10%, so they fall through to Industrials (`heuristic_code = "20"`) → `match: false`. Tracked as a 2026-04-24 FEEDBACK-LOG entry.
- **Missing R&D data** (AMD and similar): when the datacleaner pipeline doesn't populate `ResearchAndDevelopment`, `isTechnologyCompany` returns false and the ticker drops to `heuristic_code = "20"` Industrials. `sic = "MFG"` multi-maps to `{20, 45}`, so `match: true` **still** — but the Industrials label is misleading.
- **REIT subsectors** (`sic ∈ {RESIDENTIAL, INDUSTRIAL, OFFICE, RETAIL_REIT, HEALTHCARE_REIT, DATA_CENTER, CELLTOWER, SPECIALTY}`): the heuristic classifies REITs from balance-sheet ratios as Industrials (`heuristic_code = "20"`) because their asset structure looks industrial-shaped to the heuristic. SIC says GICS 60 (Real Estate). `match: false` is the intentional dual-classifier disagreement signal; the FFO model still routes correctly (it consumes `sic`, not `heuristic_code`).
- **Digital Realty Trust class** — DLR has SIC 6798 (REIT) but its company name "Digital Realty" matches the TECH parent's keyword pattern at priority 100, outranking RESTATE (priority 65). Routes to TECH not RESTATE; misses the REIT FFO model. Documented inline at `internal/services/datacleaner/industry/classifier_val3p1_reit_test.go`. Same defect class as the next item.
- **Healthpeak / Omega / Medical Properties Trust class** (`docs/reviewer/VAL-6-...`): healthcare REIT names containing `health` / `medical` match HEALTH parent (priority 85) before RESTATE (65), so they route to the HEALTH multiples instead of `HEALTHCARE_REIT`. The HEALTHCARE_REIT subsector multiple (17.5× P/FFO) and cap rate (6.0%) are bypassed. Welltower / Ventas don't trip this (no HEALTH keyword in name).

The feature's purpose is drift detection. When `match: false`, consult the gaps above; it may be a known classifier limitation rather than a real disagreement. The long-term plan (tracked in `docs/refactoring/industry-classification-unification-spec.md`) is to invert classifier precedence so SIC outranks company-name keywords, then unify on SIC alone and retire the heuristic.

**Error Responses:**

| Status | Code | When |
|--------|------|------|
| 400 | `INVALID_TICKER` | Ticker format is invalid |
| 400 | `INVALID_PARAMETER` | override_beta or override_rf out of range |
| 404 | `TICKER_NOT_FOUND` | Ticker is not present in SEC's ticker→CIK index (genuinely unknown symbol) |
| 422 | `INSUFFICIENT_DATA` | Ticker exists but cannot be valued — e.g. SEC has no usable XBRL facts (some pre-revenue issuers, clinical-stage biotechs), or fewer than the required financial periods |
| 422 | `MODEL_NOT_APPLICABLE` | No valuation model can be applied |
| 422 | `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` | 20-F filer using a taxonomy or currency Midas does not yet cover. Most ADRs (TSM, ASML, BABA, …) are supported via the IFRS-FPI pipeline; this code only fires for genuinely unmapped taxonomies (JGAAP, K-IFRS, ifrs-smes) or currencies absent from FRED + `config/fx_rates.json` |
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

**Response (200 OK — all-success or partial-success share the same shape):**

The bulk response carries `results[]` and a `summary` block. Failed tickers are inlined into the same `results` array (with their Problem-Details error fields populated) so consumers can iterate the array in request order. There is no separate `failures[]` array.

```json
{
  "results": [
    {
      "ticker": "AAPL",
      "wacc": 0.092,
      "dcf_value_per_share": 156.42,
      "calculation_method": "multi_stage_dcf",
      "calculation_version": "4.1",
      "data_quality_score": 90,
      "data_quality_grade": "A",
      "...": "...full FairValueResponse fields..."
    },
    {
      "ticker": "MSFT",
      "wacc": 0.088,
      "dcf_value_per_share": 310.15,
      "data_quality_score": 95,
      "data_quality_grade": "A",
      "...": "..."
    }
  ],
  "summary": {
    "total_requested": 3,
    "successful": 2,
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
Value per Share = (FFO / Diluted Shares) * P/FFO Multiple
```

The P/FFO multiple is **subsector-keyed** — REIT subsectors trade at meaningfully different multiples in 2025-2026 and the FFO model picks the right one per ticker:

| Subsector | NTM P/FFO | Cap rate (NAV cross-check) | Examples |
|-----------|-----------|----------------------------|----------|
| `RESIDENTIAL` | 20× | 5.0% | EQR, AVB |
| `INDUSTRIAL` | 22.5× | 4.5% | PLD, DRE |
| `OFFICE` | 14× | 7.5% | BXP, VNO |
| `RETAIL_REIT` | 10× | 8.5% | SPG, KIM |
| `HEALTHCARE_REIT` | 17.5× | 6.0% | WELL, VTR |
| `DATA_CENTER` | 31× | 4.0% | EQIX, DLR |
| `CELLTOWER` | 25× | 4.5% | AMT, CCI, SBAC |
| `SPECIALTY` | 17.5× | n/a (uses default 6.0%) | (classifier matchers TBD) |
| _default REIT_ | 15× | 6.0% | unmapped subsector falls back here |

Subsector resolution is keyword-based on company name (`equinix` → `DATA_CENTER`, `american tower` → `CELLTOWER`, `prologis` → `INDUSTRIAL`, `simon property` → `RETAIL_REIT`, etc.); see `config/datacleaner/industry_codes.json`. The model emits the subsector code on `industry.sic` and warns when the P/FFO value diverges from the NAV cross-check by >2× or <0.5×:

```text
"P/FFO value ($152.3) diverges from NAV cross-check ($32.12/share, cap rate 8.5%); ratio=4.74x"
```

#### Revenue Multiple

Used for **pre-profit companies** with negative operating income (early-stage tech, biotech, capex-cyclical semis at trough).

```
Enterprise Value = TTM Revenue * Sector EV/Revenue Multiple
Equity Value = EV - Debt + Cash
Value per Share = Equity Value / Diluted Shares
```

Two refinements from RM-1 + RM-2 Phase 1 (RM-3 forward model is tracked separately and not yet shipped):

1. **TTM revenue base** (RM-1) — the model consumes `HistoricalFinancialData.TrailingTwelveMonthsRevenue()`, a 5-tier fallback chain that tries each path in order:

   | Source | Fires when | Lossy? |
   |--------|-----------|--------|
   | `TTM_PRIOR_BRIDGE` | Latest year has 1-3 quarters AND prior year has corresponding 4-N quarters (partial-year IPO shape) | No — calendar-aligned |
   | `TTM_4Q` | Latest 4 contiguous quarters span exactly 12 months | No |
   | `ANNUAL_FY` | A 10-K is the most recent qualifying filing | No |
   | `ANNUALIZED_QUARTER` | Only 1-3 partial-year quarters available with no prior-year bridge data; multiplies latest quarter × 4 | **Yes** — emits `revenue_base: annualized single-quarter revenue (4× extrapolation, ignores seasonality)` |
   | `INSUFFICIENT_HISTORY` | No qualifying revenue at all | error path |

   The source string is always surfaced in `result.Warnings` as `revenue_base: source=<TIER> revenue=$<amount>` so replay tooling and dashboards can audit which path fired.

2. **Sub-industry sector multipliers** (RM-2 P1) — `config/industry_multiples.json` is keyed by sub-industry where the classifier resolves it. Populated entries (longest-prefix-match falls back to the parent code, then to default `2.0×`):

   | Sub-industry | EV/Revenue | Note |
   |--------------|-----------|------|
   | `MFG_SEMI` | 6.5× | Fabless / IDM semiconductors (was incorrectly bucketed under `MFG: 1.5×` pre-RM-2) |
   | `TECH_SAAS` | 8.0× | |
   | `TECH_AI` | 10.0× | |
   | `HEALTH_BIOTECH` | 6.0× | Clinical-stage biotechs |
   | `HEALTH_PHARMA` | 4.0× | |
   | `FIN_BANK` | 2.0× | |
   | `FIN_INSURANCE` | 1.0× | |
   | _parent codes_ | various (1.0×-5.0×) | `TECH`, `HEALTH`, `FIN`, `MFG`, `RETAIL`, `ENERGY`, `TELECOM`, `CONS`, `TRANS`, `RESTATE` |

   The model emits a warning naming the sub-industry and the multiple applied:

   ```text
   "Applied 6.5x EV/Revenue multiple for MFG_SEMI sector"
   ```

Always flagged at the model's confidence level (typically `low`) since the revenue-multiple path ignores profitability and growth dynamics.

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

Cross-check flags are **advisory only** — they do not invalidate the valuation but are included in the `warnings` array for transparency.

**REIT NAV cross-check.** When the FFO model fires, an additional NAV cross-check runs alongside the standard sanity checks. It uses the **subsector-specific cap rate** (see the FFO subsector table in §5.2) rather than a uniform 6% default — so a `RETAIL_REIT` divergence is computed against an 8.5% cap rate, while a `DATA_CENTER` divergence uses 4.0%. The warning surfaces both the NAV per share and the cap rate so consumers can recompute:

```text
"P/FFO value ($152.3) diverges from NAV cross-check ($32.12/share, cap rate 8.5%); ratio=4.74x"
```

A divergence ratio outside `[0.5x, 2.0x]` triggers the warning. The NAV is `OperatingIncome / cap_rate / DilutedShares`.

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
| `environment` | `ENVIRONMENT` | `development` | `development` / `staging` / `production`. Drives per-environment defaults (logging format, file sink, artifact store, log level). |
| `port` | `PORT` | `8080` | HTTP server port |
| `server.read_timeout` | `SERVER_READ_TIMEOUT` | `30s` | Request read timeout |
| `server.write_timeout` | `SERVER_WRITE_TIMEOUT` | `30s` | Response write timeout |
| `server.idle_timeout` | `SERVER_IDLE_TIMEOUT` | `120s` | Keep-alive idle timeout |
| `log_level` | `LOG_LEVEL` | `debug` | Legacy logging level. Prefer `LOGGING_LEVEL` (see §8.10); falls back to this when `LOGGING_LEVEL` is unset. |
| `enable_swagger` | `ENABLE_SWAGGER` | `false` | Enable Swagger UI at `/swagger` |
| `enable_pprof` | `ENABLE_PPROF` | `false` | Enable Go pprof at `/debug/pprof` |

Container-only flags consumed outside the Viper config (so not in the table above): `RUN_MIGRATIONS=true` triggers `cmd/migrate` from `docker-entrypoint.sh`; `GIN_MODE=debug|release` is read by the Gin framework directly.

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
| `sec.ticker_mapping_url` | `SEC_TICKER_MAPPING_URL` | `https://www.sec.gov/files/company_tickers.json` | Ticker → CIK mapping endpoint |
| `sec.user_agent` | `SEC_USER_AGENT` | `Midas DCF API admin@example.com` | Required User-Agent header — SEC policy mandates a real contact email |
| `sec.rate_limit` | `SEC_RATE_LIMIT` | `10` | Max requests per second |
| `sec.request_timeout` | `SEC_REQUEST_TIMEOUT` | `30s` | Per-request timeout |
| `sec.max_retries` | `SEC_MAX_RETRIES` | `3` | Retry attempts |
| `sec.retry_backoff_base` | `SEC_RETRY_BACKOFF_BASE` | `1s` | Base for exponential retry backoff |

### 8.5 Market Data

**Yahoo Finance (primary)**

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `market.yfinance.enabled` | `MARKET_YFINANCE_ENABLED` | `true` | Enable Yahoo Finance |
| `market.yfinance.base_url` | `MARKET_YFINANCE_BASE_URL` | `https://query2.finance.yahoo.com` | API base URL |
| `market.yfinance.cookie_url` | `MARKET_YFINANCE_COOKIE_URL` | `https://fc.yahoo.com` | Cookie endpoint for auth |
| `market.yfinance.crumb_url` | `MARKET_YFINANCE_CRUMB_URL` | `https://query2.finance.yahoo.com/v1/test/getcrumb` | Crumb endpoint for auth |
| `market.yfinance.request_timeout` | `MARKET_YFINANCE_REQUEST_TIMEOUT` | `30s` | Per-request timeout |
| `market.yfinance.max_retries` | `MARKET_YFINANCE_MAX_RETRIES` | `3` | Retry attempts |
| `market.yfinance.auth_ttl` | `MARKET_YFINANCE_AUTH_TTL` | `6h` | Cookie+crumb cache duration |

**Finzive (fallback scraper)**

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `market.finzive.enabled` | `MARKET_FINZIVE_ENABLED` | `true` | Enable Finzive fallback |
| `market.finzive.base_url` | `MARKET_FINZIVE_BASE_URL` | `https://finzive.com` | Base URL |
| `market.finzive.request_timeout` | `MARKET_FINZIVE_REQUEST_TIMEOUT` | `60s` | Per-request timeout (longer; scraper) |
| `market.finzive.max_retries` | `MARKET_FINZIVE_MAX_RETRIES` | `2` | Retry attempts |
| `market.finzive.user_agent` | `MARKET_FINZIVE_USER_AGENT` | `Mozilla/5.0 (compatible; Midas/1.0)` | User-Agent header (be polite) |

### 8.6 Macro Data (FRED)

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `macro.fred_enabled` | `MACRO_FRED_ENABLED` | `false` | Enable FRED API |
| `macro.fred_base_url` | `MACRO_FRED_BASE_URL` | `https://api.stlouisfed.org/fred` | FRED API base URL |
| `macro.fred_api_key` | `MACRO_FRED_API_KEY` | _(required if enabled)_ | FRED API key — free at fred.stlouisfed.org |
| `macro.manual_risk_free_rate` | `MACRO_MANUAL_RISK_FREE_RATE` | `0.045` | Manual risk-free rate (when FRED disabled) |
| `macro.manual_market_risk_premium` | `MACRO_MANUAL_MARKET_RISK_PREMIUM` | `0.05` | Manual market risk premium |

### 8.7 Valuation

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `valuation.default_market_risk_premium` | `VALUATION_DEFAULT_MARKET_RISK_PREMIUM` | `0.05` | Default equity risk premium |
| `valuation.default_terminal_growth_cap` | `VALUATION_DEFAULT_TERMINAL_GROWTH_CAP` | `0.03` | Max terminal growth (3%) |
| `valuation.default_tax_rate` | `VALUATION_DEFAULT_TAX_RATE` | `0.21` | Corporate tax rate (21%) |
| `valuation.min_data_points_for_growth` | `VALUATION_MIN_DATA_POINTS_FOR_GROWTH` | `2` | Min historical points required for growth estimate |
| `valuation.max_bulk_size` | `VALUATION_MAX_BULK_SIZE` | `50` | Max tickers per bulk request (1-100) |
| `valuation.cache_ttl` | `VALUATION_CACHE_TTL` | `1h` | Valuation result cache |
| `valuation.slow_request_threshold` | `VALUATION_SLOW_REQUEST_THRESHOLD` | `5s` | Warn-log threshold for slow valuations |
| `valuation.data_fetch_timeout` | `VALUATION_DATA_FETCH_TIMEOUT` | `10s` | Data fetching timeout |
| `valuation.dcf_projection_years` | `VALUATION_DCF_PROJECTION_YEARS` | `5` | Reserved DCF forecast-horizon override. **Currently inert** — the live engine derives the horizon from the multi-stage growth estimator (`Stage1Years=3` + `Stage2Years=4` = 7 explicit years, hard-coded in `internal/services/growth/estimator.go`). See §5.2; the `growth_rates` array in the response is length 7 regardless of this config value. |
| `valuation.dcf_max_growth_rate` | `VALUATION_DCF_MAX_GROWTH_RATE` | `0.5` | Growth rate ceiling (50%) |
| `valuation.dcf_min_growth_rate` | `VALUATION_DCF_MIN_GROWTH_RATE` | `-0.3` | Growth rate floor (-30%) |
| `valuation.dcf_iteration_tolerance` | `VALUATION_DCF_ITERATION_TOLERANCE` | `0.0001` | Tolerance for implied-growth iteration |
| `valuation.dcf_max_iterations` | `VALUATION_DCF_MAX_ITERATIONS` | `100` | Max iterations for implied-growth |

### 8.8 Data Cleaner

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `datacleaner.enabled` | `DATACLEANER_ENABLED` | `true` | Enable cleaning pipeline |
| `datacleaner.rules_path` | `DATACLEANER_RULES_PATH` | `./config/datacleaner/rules.json` | Path to main rules file |
| `datacleaner.industry_rules_path` | `DATACLEANER_INDUSTRY_RULES_PATH` | `./config/datacleaner/industry` | Path to industry-specific rules directory |
| `datacleaner.schema_path` | `DATACLEANER_SCHEMA_PATH` | `./config/datacleaner/schema.json` | Path to JSON schema |
| `datacleaner.min_quality_score` | `DATACLEANER_MIN_QUALITY_SCORE` | `60.0` | Minimum acceptable quality score (0-100) |
| `datacleaner.high_quality_score` | `DATACLEANER_HIGH_QUALITY_SCORE` | `85.0` | High-quality threshold (0-100) |
| `datacleaner.enable_risk_flags` | `DATACLEANER_ENABLE_RISK_FLAGS` | `true` | Enable risk flag detection |
| `datacleaner.enable_caching` | `DATACLEANER_ENABLE_CACHING` | `true` | Cache cleaned results |
| `datacleaner.cache_ttl` | `DATACLEANER_CACHE_TTL` | `6h` | Cleaning-result cache TTL |
| `datacleaner.enable_industry_rules` | `DATACLEANER_ENABLE_INDUSTRY_RULES` | `true` | Apply industry-specific rules |
| `datacleaner.enable_audit_trail` | `DATACLEANER_ENABLE_AUDIT_TRAIL` | `true` | Capture full adjustment audit trail |
| `datacleaner.log_adjustments` | `DATACLEANER_LOG_ADJUSTMENTS` | `true` | Log every adjustment made |
| `datacleaner.log_flags` | `DATACLEANER_LOG_FLAGS` | `true` | Log every flag raised |
| `datacleaner.enable_ai_integration` | `DATACLEANER_ENABLE_AI_INTEGRATION` | `false` | Enable AI footnote analysis (requires AI service) |
| `datacleaner.ai_service_url` | `DATACLEANER_AI_SERVICE_URL` | _(unset)_ | External AI service URL |
| `datacleaner.ai_service_timeout` | `DATACLEANER_AI_SERVICE_TIMEOUT` | `30s` | AI service request timeout |

### 8.9 Scheduler

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `scheduler.enabled` | `SCHEDULER_ENABLED` | `false` | Enable background scheduler |
| `scheduler.interval` | `SCHEDULER_INTERVAL` | `24h` | Batch refresh interval |
| `scheduler.max_concurrency` | `SCHEDULER_MAX_CONCURRENCY` | `2` | Concurrent fetch workers |

### 8.10 Logging

All `logging.*` config knobs in one place. See [§10 Monitoring & Observability](#10-monitoring--observability) for what each knob actually does at runtime.

**Core**

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `logging.level` | `LOGGING_LEVEL` | `debug` (dev), `info` (staging/prod) | Minimum log level (`debug` / `info` / `warn` / `error`). |
| `logging.format` | `LOGGING_FORMAT` | `console` (dev), `json` (staging/prod) | `console` for human reading, `json` for log shippers. |
| `logging.trace_calculations` | `LOGGING_TRACE_CALCULATIONS` | `true` (dev), `false` (staging/prod) | Emit per-stage DCF calc entries at Info instead of Debug. |
| `logging.access_log_skip_paths` | `LOGGING_ACCESS_LOG_SKIP_PATHS` | `["/metrics", "/health", "/ready"]` | Paths logged at Debug instead of Info to keep noise down. |

**Rolling file sink (`logging.file.*`)** — off by default in staging/prod; on in dev for `tail -f ./logs/midas.log`.

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `logging.file.enabled` | `LOGGING_FILE_ENABLED` | `true` (dev), `false` (staging/prod) | Master switch for the file sink. |
| `logging.file.path` | `LOGGING_FILE_PATH` | `./logs/midas.log` | File path. |
| `logging.file.max_size_mb` | `LOGGING_FILE_MAX_SIZE_MB` | `100` | Rotate at this size. |
| `logging.file.max_backups` | `LOGGING_FILE_MAX_BACKUPS` | `10` | Keep this many rotated backups. |
| `logging.file.max_age_days` | `LOGGING_FILE_MAX_AGE_DAYS` | `14` | Delete backups older than this. |
| `logging.file.compress` | `LOGGING_FILE_COMPRESS` | `true` | Gzip rotated backups. |

**Narrate stream (`logging.narrate.*`)** — one structured Info line per pipeline phase.

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `logging.narrate.enabled` | `LOGGING_NARRATE_ENABLED` | `true` | Master switch. |
| `logging.narrate.sample_rate` | `LOGGING_NARRATE_SAMPLE_RATE` | `1.0` | Fraction of requests to narrate (`[0.0, 1.0]`). |
| `logging.narrate.redact_fields` | `LOGGING_NARRATE_REDACT_FIELDS` | `[]` | Field keys to drop from narrate lines. |

**Artifact-store core (`logging.artifact_store.*`)** — per-request bundles on disk; see [§10.5](#105-per-request-artifact-bundles).

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `logging.artifact_store.enabled` | `LOGGING_ARTIFACT_STORE_ENABLED` | `true` (dev), `false` (staging/prod) | Master switch. When `false`, all triggers are inert. |
| `logging.artifact_store.root_path` | `LOGGING_ARTIFACT_STORE_ROOT_PATH` | `./artifacts` | Where bundles land. |
| `logging.artifact_store.retention_days` | `LOGGING_ARTIFACT_STORE_RETENTION_DAYS` | `7` | Delete bundles older than this. |
| `logging.artifact_store.max_total_bytes` | `LOGGING_ARTIFACT_STORE_MAX_TOTAL_BYTES` | `5368709120` (5 GiB) | Evict oldest-first when total exceeds this. |
| `logging.artifact_store.queue_size` | `LOGGING_ARTIFACT_STORE_QUEUE_SIZE` | `256` | Per-bundle snapshot queue depth. |
| `logging.artifact_store.pending_bytes_cap` | `LOGGING_ARTIFACT_STORE_PENDING_BYTES_CAP` | `10485760` (10 MiB) | Per-bundle in-memory buffer ceiling. |

**Artifact-store triggers (`logging.artifact_store.triggers.*`)** — which requests get a bundle. Precedence: header/query > `on_quality_flag` > `on_error` > `always`.

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `logging.artifact_store.triggers.manual` | _(not via env)_ | `true` | Honors `?trace=1` and `X-Midas-Trace: 1`. |
| `logging.artifact_store.triggers.on_error` | `LOGGING_ARTIFACT_STORE_TRIGGERS_ON_ERROR` | `false` | Capture every 5xx response. |
| `logging.artifact_store.triggers.quality_flag_threshold` | `LOGGING_ARTIFACT_STORE_TRIGGERS_QUALITY_FLAG_THRESHOLD` | `""` (disabled) | Capture when cleaner raises a flag at this severity or above. Accepts `info` / `low` / `warning` / `medium` / `high` / `critical`. |
| `logging.artifact_store.triggers.always` | `LOGGING_ARTIFACT_STORE_TRIGGERS_ALWAYS` | `false` | Capture every request. For short debugging sessions only. |

The replay binary (`cmd/replay`) takes CLI flags only — see [§10.7 Replay Tooling](#107-replay-tooling).

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

All logs are structured JSON (or colored console in development). Format defaults are environment-driven; override with `LOGGING_FORMAT=console|json`. See [§8.10 Logging](#810-logging) for all knobs.

Every HTTP request emits one access-log line after the handler returns:

```json
{
  "ts": "2026-04-23T06:14:02.138Z",
  "level": "info",
  "msg": "access",
  "request_id": "7f9b3e0a-5c18-4b81-9c55-4a2e6b7d0a11",
  "user_id": "u_47",
  "key_id": "k_3",
  "method": "GET",
  "path": "/api/v1/fair-value/AAPL",
  "route": "/api/v1/fair-value/:ticker",
  "status": 200,
  "latency": "487ms",
  "client_ip": "192.168.1.1",
  "user_agent": "midas-sdk/1.0",
  "bytes_out": 2184
}
```

`user_id` / `key_id` appear only on authenticated routes. Errors include `error_code`. Paths in `logging.access_log_skip_paths` (default `/metrics`, `/health`, `/ready`) log at `debug` to keep the info stream clean.

### 10.3 Request Tracing (`X-Request-ID`)

Every request gets a UUIDv4 request ID that appears on every log line for that request and on the `X-Request-ID` response header.

- Supply `X-Request-ID` on the request and the server reuses it (must match `^[A-Za-z0-9_.:-]{1,128}$`; malformed values are silently replaced with a fresh one).
- Otherwise the server generates a fresh ID.
- The same ID flows through every service and gateway log line for that request, so `jq 'select(.request_id == "...")'` reconstructs the full story.

### 10.4 Calculation Tracing

Every DCF valuation emits 12 structured trace entries — one per math stage (`data_fetch`, `wacc`, `fcf_projection`, `terminal_value`, `discount`, `equity_bridge`, `cross_check`, `final`, etc.) — all sharing the request's `request_id`.

Set `LOGGING_TRACE_CALCULATIONS=true` to surface them at Info (default in development). When `false` (default in staging/prod), they emit at Debug so they're available on demand without dominating the info stream.

Example:

```json
{"ts":"...","level":"info","msg":"calc","event":"calc","request_id":"...","stage":"wacc","ticker":"AAPL","rf":0.0421,"beta_raw":1.18,"beta_blume":1.12,"erp":0.055,"crp":0.000,"tax_rate":0.21,"cost_of_debt":0.045,"wacc":0.1039}
```

`jq 'select(.request_id == "...") | {stage, msg}'` reconstructs the full math story for any one request.

### 10.5 Per-Request Artifact Bundles

When you need to inspect a request's full state — raw upstream payloads, cleaner before/after, full math — the server can write a self-describing directory of JSON / JSONL files for that request. By default no bundle is written; you opt in per-request or configure auto-capture for specific conditions. To inspect a captured bundle, see [§10.7 Replay Tooling](#107-replay-tooling).

#### Triggers

Five ways to capture a bundle. When more than one applies to the same request, precedence is **header/query > on_quality_flag > on_error > always**.

| Trigger | How to enable | Fires when |
|---------|---------------|------------|
| Header | `X-Midas-Trace: 1` request header | Every request you set it on |
| Query | `?trace=1` query param | Every request you set it on |
| On error | `LOGGING_ARTIFACT_STORE_TRIGGERS_ON_ERROR=true` | Response status ≥ 500 (or handler panic) |
| On quality flag | `LOGGING_ARTIFACT_STORE_TRIGGERS_QUALITY_FLAG_THRESHOLD=<severity>` (`info` / `low` / `warning` / `medium` / `high` / `critical`) | Cleaner produces 1+ flags at or above this severity |
| Always-on (debugging) | `LOGGING_ARTIFACT_STORE_TRIGGERS_ALWAYS=true` | Every request — for short debugging sessions only |

Per-request capture from a client:

```bash
# Either form works
curl -H "X-API-Key: <key>" -H "X-Midas-Trace: 1" \
  "http://localhost:8080/api/v1/fair-value/AAPL"

curl -H "X-API-Key: <key>" \
  "http://localhost:8080/api/v1/fair-value/AAPL?trace=1"
```

#### Server-side master switch

`LOGGING_ARTIFACT_STORE_ENABLED` defaults to `true` in development and `false` in staging/production. Even with triggers configured, bundles are suppressed when this is `false`. Override per environment as needed.

#### Bundle layout on disk

Bundles land under `./artifacts/<UTC-date>/<TICKER>/req_<id>/`. Each directory is one request:

```
artifacts/
  2026-05-09/
    AAPL/
      req_01HW8ZQXKR.../
        00-manifest.json             # request id, trigger, ticker, schema versions, git_sha
        01-request.json              # original HTTP request (auth headers redacted)
        02-handler-options.json      # parsed ValuationOptions
        05-fetch-sec.raw.json        # raw SEC EDGAR payload (auth-redacted)
        05-fetch-sec.parsed.json     # parsed SEC struct
        06-fetch-market.raw.json     # Yahoo / Finzive raw payload
        06-fetch-market.parsed.json
        07-fetch-macro.raw.json      # FRED raw payload (api_key redacted)
        07-fetch-macro.parsed.json
        10-clean-input.json          # cleaner input
        10-clean-output.json         # cleaner output
        11-classify.json             # industry classification
        12-growth-curve.json         # multi-stage growth curve
        13-wacc.json                 # WACC inputs + final value
        14-model-selection.json      # model router decision
        15-valuation.json            # full DCF / DDM working
        16-crosscheck.json           # implied multiples vs sector medians
        17-response.json             # final response body sent to client
        99-narrate.jsonl             # narrate stream
        99-debug-trace.jsonl         # debug stream (only when log level = debug)
```

The numeric prefix matches the request's pipeline phase, so `ls` reads in pipeline order. `.raw.json` files are exactly what the upstream API returned (after auth redaction); `.parsed.json` files are what the gateway parser produced. Diff them to separate upstream drift from parser drift.

Secrets are always redacted: `Authorization`, `Cookie`, `X-API-Key`, Yahoo `crumb`, FRED `api_key`, and any JSON key matching `password` / `secret` / `token` / `bearer`.

#### Retention

A reaper sweeps `artifacts/` once per hour and removes bundles older than `LOGGING_ARTIFACT_STORE_RETENTION_DAYS` (default 7). When the bundle root exceeds `LOGGING_ARTIFACT_STORE_MAX_TOTAL_BYTES` (default 5 GiB), oldest bundles are evicted first.

#### Phase 2 status

All sub-phases of the observability narrative + artifacts spec are SHIPPED on master:
- **Phase 1** — manual triggers + bundle layout
- **Phase 2.A** — auto-on-error trigger
- **Phase 2.B** — auto-on-quality-flag trigger
- **Phase 2.C** — always-on knob
- **Phase 2.D** — replay tooling (`cmd/replay`); see [§10.7](#107-replay-tooling)

### 10.6 Health Checks

| Endpoint | Purpose | Auth | Status Mapping |
|----------|---------|------|---------------|
| `GET /health` | Liveness probe (K8s) | No | 200 = alive |
| `GET /ready` | Readiness probe (K8s) | No | 200 = ready to serve |
| `GET /api/v1/health/detailed` | Deep component check | Yes | 200/206/503 |

### 10.7 Replay Tooling

`cmd/replay` is a standalone binary that re-runs a captured artifact bundle through the current code and diffs the produced response against the bundle's saved `17-response.json`. Use it to regression-test code changes against your watchlist without re-fetching upstream data. No external network calls — bundle files supply everything.

#### Build

```bash
go build -o ./replay.exe ./cmd/replay   # Windows
go build -o /tmp/replay ./cmd/replay    # Linux/macOS
```

#### Quick start

```bash
# Replay one bundle. Production bundles often only ship *.parsed.json for
# some gateways, so the default --from=raw may fail with a hint to use --from=parsed.
./replay --from=parsed artifacts/2026-05-09/AAPL/req_<id>/

# See WHERE the drift came from (which pipeline stage)
./replay --diff-stages --verbose --from=parsed artifacts/2026-05-09/AAPL/req_<id>/

# Replay a whole day's worth of bundles in parallel
./replay --workers=4 --from=parsed artifacts/2026-05-09/

# Filter to one ticker, or only recent bundles
./replay --filter-ticker=AAPL --filter-since=7d artifacts/

# JSON output for scripting
./replay --diff-stages --format=json --from=parsed <bundle-dir> | jq '.results[0].stage_diffs'

# Full flag help
./replay --help
```

#### CLI flag reference

| Flag | Default | Purpose |
|------|---------|---------|
| `<bundle-dir>` (positional, required) | — | Bundle directory OR a parent directory; walked recursively to find every `req_*/00-manifest.json`. |
| `--format` | `text` | `text` (one row per bundle + SUMMARY) or `json` (machine-parseable, stable shape). |
| `--out` | stdout | Write output to a file instead of stdout. |
| `--from` | `raw` | `raw` re-runs gateway parsers from `*.raw.json`; `parsed` reads `*.parsed.json` directly. Use `parsed` when a bundle lacks raw payloads (common). |
| `--quiet` | off | Suppress per-bundle rows; only emit the SUMMARY. |
| `--verbose` | off | Text-mode only: emit per-field diff rows + `Stage diffs:` section (when combined with `--diff-stages`). |
| `--diff-stages` | off | Also diff the bundle's intermediate-stage files (`10-clean-output.json`, `12-growth-curve.json`, `13-wacc.json`, `15-valuation.json`) against current engine output — pinpoints WHICH stage drifted. |
| `--workers` | `runtime.NumCPU()` | Parallel-replay worker count. Override with `REPLAY_WORKERS=N` env var or `--workers=N` flag. Use lower values (1-4) for laptops on battery; higher for batches on workstations. |
| `--filter-ticker` | _(unset)_ | Skip bundles whose manifest ticker doesn't match (case-sensitive). |
| `--filter-since` | _(unset)_ | Skip bundles older than this duration. Accepts `30m`, `2h`, `7d`. |
| `--float-rel-tol` | `1e-9` | Relative float tolerance. `0` means "use default", NOT "exact match". |
| `--float-abs-tol` | `1e-12` | Absolute float tolerance. Same `0`-as-default rule. |
| `--allow-schema-drift` | off | Allow replay to proceed when the bundle's schema versions don't match the current code (downgrades to warning). |
| `--allow-git-drift` | off | Suppress the `git_drift` annotation when the bundle was captured against a different code revision (the expected case). |

#### Exit codes

| Code | Meaning |
|------|---------|
| `0` | Every bundle's response matched its saved `17-response.json` (within tolerance). |
| `1` | At least one bundle differed outside tolerance. |
| `2` | Infrastructure failure: missing files, schema-version mismatch, invalid flags, empty bundle directory, etc. |

#### Sample output

Verbose text mode with `--diff-stages`:

```
artifacts/2026-05-09/AAPL/req_<id>/   FAIL   fields=2/47   duration=92ms
  - dcf_value_per_share: 156.42 -> 156.81 (rel_drift=0.002494)
  - wacc: 0.092 -> 0.094 (rel_drift=0.021739)
  Stage diffs:
    13-wacc.json:
      - cost_of_equity: 0.118 -> 0.121 (rel_drift=0.025424)
    15-valuation.json:
      - dcf_value_per_share: 156.42 -> 156.81 (rel_drift=0.002494)

SUMMARY: 0/1 passed, 1 failed, 0 errored, total duration=92ms
```

A `~` marker means "drifted within tolerance" — the field moved but stayed inside the configured tolerance, so the bundle still PASSed.

JSON mode emits the same information under `results[].diffs` and `results[].stage_diffs`. The shape is stable; consumers can rely on the field names and nesting. **Bundle paths in JSON output use forward-slash separators on all platforms** (so `jq '.results[].bundle' | xargs ...` pipelines work portably between Windows-captured bundles and Linux processing). Text-mode output preserves native separators for human readability.

#### Workflow — regression-test a code change

```bash
# 1. Make your code change (e.g., adjust growth blend, retune WACC).

# 2. Replay your watchlist with full per-stage detail.
./replay --diff-stages --verbose --workers=4 --from=parsed artifacts/2026-05-09/

# 3. Read the SUMMARY:
#    - 0 failed → no drift; safe to commit.
#    - N failed → the per-stage diffs show WHICH pipeline stage introduced the drift.
#    - errored → if you used --from=raw, try --from=parsed.

# 4. If drifts are intentional but small, loosen tolerances to confirm magnitude bounds.
./replay --diff-stages --verbose --float-rel-tol=1e-3 --from=parsed artifacts/2026-05-09/
```

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
| `INSUFFICIENT_DATA` | 422 | Ticker resolves but cannot be valued: SEC has no usable XBRL facts (some pre-revenue / clinical-stage issuers) or too few financial periods |
| `MODEL_NOT_APPLICABLE` | 422 | No valuation model can be applied to this company |
| `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` | 422 | 20-F filer using a taxonomy (JGAAP, K-IFRS, ifrs-smes) or reporting currency (no FRED series + missing from `config/fx_rates.json`) the IFRS-FPI pipeline does not yet cover |
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
- Look for `INSUFFICIENT_DATA`, `MODEL_NOT_APPLICABLE`, or `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` errors
- `INSUFFICIENT_DATA` (422) indicates SEC has filings for the ticker but no usable XBRL facts (e.g. clinical-stage biotechs, pre-revenue issuers) or too few periods. Distinct from FPI cases — most ADRs (TSM, ASML, BABA, …) are now supported via the IFRS-FPI pipeline and produce real per-ADR valuations.
- `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` (422) indicates a 20-F filer whose taxonomy (JGAAP, K-IFRS, ifrs-smes) or reporting currency falls outside the IFRS-FPI pipeline's coverage. Both are config-extensible — see `internal/infra/gateways/macro/gateway.go: fredSeriesFor` and `config/fx_rates.json` for currencies, `internal/infra/gateways/sec/parser.go: findValue` for taxonomies.

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
| **NCAV** | Net Current Asset Value = Current Assets − Total Liabilities. Graham's deep-value metric; the basis for the "buy below" floor (`ncav × 2/3`) |
| **NOPAT** | Net Operating Profit After Tax |
| **NWC** | Net Working Capital = Current Assets - Current Liabilities |
| **P/E** | Price-to-Earnings ratio |
| **P/FCF** | Price-to-Free-Cash-Flow ratio |
| **P/FFO** | Price-to-Funds-From-Operations ratio — the equivalent of P/E for REITs (FFO replaces earnings because depreciation depresses GAAP earnings for property-heavy entities). Subsector-calibrated in Midas: residential 20×, industrial 22.5×, office 14×, retail 10×, healthcare 17.5×, data center 31×, cell tower 25×, default 15× |
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
