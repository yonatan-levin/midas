# Midas DCF Valuation API — Consumer Reference

**Version:** 0.9.0-rc1 (MVP)

---

> ## 📌 Purpose & Scope of This Document
>
> **This file documents how to interact with the Midas REST API as a consumer.** It is the reference for anyone building an integration against the API endpoints. It covers:
>
> - **HTTP endpoints** — paths, methods, parameters
> - **Request formats** — required headers, body schemas, query parameters
> - **Response formats** — every field on the response, with examples and interpretation
> - **Authentication** — how to obtain and use API keys; permission model
> - **Error handling** — error codes, HTTP statuses, RFC 7807 problem-details format
> - **Rate limiting** — quotas, headers, retry guidance
> - **Security** — headers the API returns, CORS, API-key handling
>
> ### What does NOT belong in this file
>
> Information about the project *internals* drifted this doc to 1933 lines on a prior audit. It is now ~900 lines because the following topics have been **deliberately removed and moved to their proper homes**. Please do not add them back:
>
> | Topic | Lives in |
> |---|---|
> | Project overview, mission, phases | `docs/THESIS.md` |
> | Build / install / clone / Docker setup | `README.md`, `CLAUDE.md` |
> | Valuation engine internals (DCF math, growth estimation, WACC pipeline, ADR mechanics, sanity-check thresholds) | `docs/THESIS.md`, source code under `internal/services/valuation/`, `pkg/finance/` |
> | Data-cleaner pipeline (adjusters, normalization categories) | `internal/services/datacleaner/` source + `docs/refactoring/spec/` |
> | Configuration reference (env vars, viper keys, YAML knobs) | `internal/config/config.go` `setDefaults()`; `config/` directory |
> | Deployment (Docker compose, Traefik, scripts) | `docker-compose*.yml`, `scripts/`, `Dockerfile` |
> | Internal observability (Prometheus metric names, structured logging fields, narrate stream, calc-trace, artifact-bundle layout, replay tooling) | `CLAUDE.md`, source under `internal/observability/` |
> | Architecture (hexagonal layering, DI graph, middleware stack, DB schema) | `CLAUDE.md`, source code |
> | Build/test troubleshooting | `CLAUDE.md` |
>
> **Test for whether something belongs here:** *"Would a third-party developer hitting the API need this to make a successful request or interpret a response?"* If no — it belongs elsewhere.
>
> **Last scope audit:** 2026-05-24. If you find this doc has drifted past ~1000 lines or has grown sections matching the "lives in" table above, redirect that content to its proper home rather than expanding this file.

---

## Table of Contents

1. [Quick Start](#1-quick-start)
2. [Authentication](#2-authentication)
3. [API Reference](#3-api-reference)
4. [Error Handling](#4-error-handling)
5. [Rate Limiting](#5-rate-limiting)
6. [Security](#6-security)
7. [Glossary](#7-glossary)
8. [Appendix A — Postman Collection](#appendix-a--postman-collection)
9. [Appendix B — OpenAPI Specification](#appendix-b--openapi-specification)

---

## 1. Quick Start

### Base URL

```
http://localhost:8080          # Development
https://your-domain.com        # Production
```

### Hello-world requests

```bash
# Health check (no auth needed)
curl http://localhost:8080/health

# Get Apple's fair value
curl -H "X-API-Key: <your-key>" \
     http://localhost:8080/api/v1/fair-value/AAPL

# Bulk valuation
curl -X POST \
     -H "X-API-Key: <your-key>" \
     -H "Content-Type: application/json" \
     -d '{"tickers": ["AAPL", "MSFT", "GOOGL"]}' \
     http://localhost:8080/api/v1/fair-value/bulk
```

### Common headers

| Header | Required | Description |
|--------|----------|-------------|
| `X-API-Key` | Yes (protected routes) | Authentication key |
| `Content-Type` | Yes (POST requests) | Must be `application/json` |
| `X-Request-ID` | No | Custom request ID (auto-generated if absent). Must match `^[A-Za-z0-9_.:-]{1,128}$` or the server replaces it with a fresh UUIDv4. Echoed back on the `X-Request-ID` response header. |
| `X-Midas-Trace` | No | Set to `1` to capture a per-request artifact bundle for debugging (see [§3.3.4 Per-request tracing](#334-per-request-tracing)). |

---

## 2. Authentication

### API Key Authentication

All protected endpoints require an `X-API-Key` header. Keys are cryptographically generated with the format `dcf_<64-character-hex>`.

```
X-API-Key: dcf_1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef
```

### Permissions

Each API key carries a set of permissions controlling which endpoints it can access:

| Permission | Grants Access To |
|------------|-----------------|
| `read:fair_value` | `GET /api/v1/fair-value/:ticker`, `POST /api/v1/fair-value/bulk` |
| `read:health` | `GET /api/v1/health/detailed` |
| `read:metrics` | `GET /api/v1/metrics` |
| `manage:keys` | `POST /api/v1/auth/keys` |
| `admin:all` | All endpoints (superuser) |

### Creating API Keys

Requires a key with `manage:keys` permission:

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

> **Important:** The raw `key` field is only returned in this response. Store it securely — it cannot be retrieved later.

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

## 3. API Reference

### 3.1 Public Endpoints (No Authentication)

#### `GET /health`

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

#### `GET /ready`

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

#### `GET /version`

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

#### `GET /metrics`

Prometheus-format metrics for monitoring systems. Returns `text/plain` in the Prometheus exposition format. (Metric naming and contents are an *internal* concern — they may change without API-version bump. Do not parse this as a stable contract.)

---

### 3.2 Valuation Endpoints

#### `GET /api/v1/fair-value/:ticker`

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
| `trace` | bool | `1` to enable | Capture a per-request artifact bundle for debugging — see [§3.3.4](#334-per-request-tracing) |

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
  "dcf_horizon_years": 7,
  "dcf_terminal_method": "gordon_growth",
  "dcf_terminal_pct_of_ev": 0.62,
  "dcf_per_year_pv": [13.4, 12.8, 12.0, 11.1, 10.2, 9.1, 7.9],
  "dcf_terminal_growth_used": 0.03,
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

When `total_liabilities` cannot be sourced from the underlying filings, the four Graham fields are dropped from the JSON and a single warning is appended:

```json
{
  "ticker": "UNRESOLVED",
  "warnings": [
    "graham_floor: insufficient balance-sheet data (total_liabilities unresolved)"
  ]
}
```

---

#### 3.2.1 Response Fields — `FairValueResponse`

| Field | Type | Description |
|-------|------|-------------|
| `ticker` | string | Echoed ticker symbol |
| `wacc` | float | Weighted Average Cost of Capital used for discounting |
| `growth_rate` | float | Blended first-stage growth rate |
| `growth_rates` | float[] | Per-year growth rates across projection period (length matches `dcf_horizon_years`) |
| `growth_source` | string | How growth was estimated: `analyst_blend`, `historical_only`, `default` |
| `growth_confidence` | string | Confidence level: `high`, `medium`, `low` |
| `analyst_weight` | float | Weight given to analyst consensus in the growth blend (0.0–1.0) |
| `historical_weight` | float | Weight given to historical CAGR in the growth blend (1.0 − `analyst_weight`) |
| `tangible_value_per_share` | float | Net tangible book value per share (floor value). Denominator priority chain: diluted shares first, then market basic, then financial basic — same chain as `dcf_value_per_share`, `ncav_per_share`, and the Graham fields. |
| `dcf_value_per_share` | float | Intrinsic per-share value. For tickers routed to non-DCF models (DDM, FFO, revenue_multiple) this field carries that model's per-share output — `calculation_method` distinguishes which math fired. |
| `current_assets_per_share` | float | Current assets ÷ diluted shares — pure asset-side floor with no liability subtraction. **Omitted** when `total_liabilities` cannot be resolved. |
| `ncav_per_share` | float | Graham's Net Current Asset Value per share: `(current_assets − total_liabilities) / diluted_shares`. **May be negative** for distressed companies (raw value, no clamping). **Omitted** when `total_liabilities` cannot be resolved. |
| `graham_floor_per_share` | float | Graham's "buy below" trigger: `max(ncav_per_share × 2/3, 0)`. Clamps to 0 when NCAV is negative (represents "no asset floor exists"). |
| `graham_discount_pct` | float | `(current_price − graham_floor_per_share) / graham_floor_per_share`. Positive = price above floor; negative = price below floor. **Omitted** when `graham_floor_per_share` is 0. |
| `dcf_horizon_years` | integer | Number of explicit DCF projection years selected (typically 3, 5, 7, or 10 depending on archetype profile). |
| `dcf_terminal_method` | string | Terminal value method: `"gordon_growth"` or `"exit_multiple"`. |
| `dcf_terminal_pct_of_ev` | float | `terminal_pv / enterprise_value`. Flag values >0.80 indicate a terminal-dominant valuation. |
| `dcf_per_year_pv` | float[] | Per-year present value of FCF for the explicit projection period. Useful for chart-friendly visualization. |
| `dcf_terminal_growth_used` | float | Final terminal growth rate after the WACC-spread guardrail clamps. |
| `as_of` | ISO 8601 | Timestamp the valuation was computed. |
| `data_quality_score` | integer | 0-100 score reflecting data freshness and completeness (see [§3.2.2](#322-data-quality-score)). |
| `data_quality_grade` | string | Letter grade derived from `data_quality_score` (see [§3.2.2](#322-data-quality-score)). |
| `calculation_method` | string | Which valuation model fired (see [§3.2.3](#323-calculation-method-values)). |
| `calculation_version` | string | Engine math version. Bumped when model math changes; consumers can use this to detect engine upgrades affecting historical comparisons. |
| `sanity_check` | object | Cross-validation against sector median multiples (see [§3.2.4](#324-sanity-check)). |
| `industry` | object | Dual industry classification (see [§3.2.5](#325-industry-classification)). |
| `currency` | string | ISO-4217 code. All monetary per-share fields are denominated in this currency. Always `"USD"` — non-USD reporting currencies are FX-converted upstream. |
| `adr_ratio_applied` | integer | Ordinary-shares-per-ADR multiplier the engine applied. `1` for domestic 10-K filers; non-1 for ADRs (TSM=5, BABA=8). Omitted when zero. |
| `current_price` | float | Live per-share market price at calculation time, in the same per-share basis as `dcf_value_per_share` (per-ADR for ADRs). Compute upside as `(dcf_value_per_share - current_price) / current_price`. Omitted when unavailable. |
| `warnings` | string[] | Diagnostic strings — sector-multiple notes, revenue-base path used by the revenue-multiple model, graham-floor data-quality signals, sanity-check divergences. Always present (may be empty). |

---

#### 3.2.2 Data Quality Score

Every valuation includes a 0-100 quality score and letter grade. Consumers can use this to filter low-quality results.

| Grade | Score Range | Interpretation |
|-------|------------|----------------|
| A | 90-100 | Excellent data quality |
| B | 80-89 | Reliable financial data |
| C | 70-79 | Moderate quality, some adjustments |
| D | 60-69 | Problematic, significant adjustments |
| F | < 60 | Highly questionable data integrity |

---

#### 3.2.3 `calculation_method` Values

| Value | Meaning |
|-------|---------|
| `multi_stage_dcf` | Default — 3-stage DCF used for most companies with positive operating income |
| `ddm` | Dividend Discount Model — used for mature banks and insurers with stable dividend histories |
| `ffo` | Funds-From-Operations model — used for REITs; multiple is subsector-calibrated (residential 20×, industrial 22.5×, office 14×, retail 10×, healthcare 17.5×, data center 31×, cell tower 25×, default 15×) |
| `revenue_multiple` | Sector EV/Revenue × TTM revenue — used for pre-profit companies (negative operating income: early biotech, pre-profit SaaS, capex-cyclical semis at trough) |

When `calculation_method = "revenue_multiple"`, the `warnings` array carries the sub-industry multiple applied (e.g., `"Applied 6.5x EV/Revenue multiple for MFG_SEMI sector"`) and the revenue-base source (e.g., `"revenue_base: source=TTM_4Q revenue=$560000000"`).

---

#### 3.2.4 `sanity_check` Object

| Field | Type | Description |
|-------|------|-------------|
| `implied_pe` | float | DCF Value / EPS |
| `implied_ev_ebitda` | float | Enterprise Value / EBITDA |
| `implied_pfcf` | float | DCF Value / FCF per Share |
| `is_reasonable` | bool | `false` if any implied multiple is >2× or <0.5× sector median |
| `flags` | string[] | Specific divergences observed (e.g., `"implied_pe > 2x sector median"`) |

Cross-check flags are **advisory only** — they do not invalidate the valuation; they are surfaced as a transparency signal.

For REIT-routed tickers (FFO model), an additional NAV cross-check runs using the subsector-specific cap rate. Divergences outside `[0.5x, 2.0x]` produce warnings like:

```text
"P/FFO value ($152.3) diverges from NAV cross-check ($32.12/share, cap rate 8.5%); ratio=4.74x"
```

---

#### 3.2.5 `industry` Object

The `industry` object exposes both classifiers the engine runs on every request:

| Sub-field | Description |
|-----------|-------------|
| `sic_code` | Raw SIC code from SEC filing header (e.g., `3674` for semiconductors) |
| `sic` | High-level label from SIC + company name. Top-level codes: `TECH`, `MFG`, `RETAIL`, `UTIL`, `FIN`, `HEALTH`, `ENERGY`, `RESTATE`, `TELECOM`, `TRANS`, `CONS`. Sub-industry refinements: `TECH_SAAS`, `TECH_AI`, `MFG_SEMI`, `HEALTH_BIOTECH`, `HEALTH_PHARMA`, `FIN_BANK`, `FIN_INSURANCE`, `FIN_IB`. REIT subsectors (`REIT_*` prefixed): `REIT_RESIDENTIAL`, `REIT_INDUSTRIAL`, `REIT_OFFICE`, `REIT_RETAIL`, `REIT_HEALTHCARE`, `REIT_DATACENTER`, `REIT_CELLTOWER`, `REIT_SPECIALTY`. |
| `heuristic_code` | GICS sector code from balance-sheet ratios: `45` (IT), `25` (Consumer Discretionary), `20` (Industrials), `35` (Health Care), `55` (Utilities), `40` (Financials), `50` (Communication Services), `60` (Real Estate), `30` (Consumer Staples), `10` (Energy). |
| `heuristic_name` | Human-readable GICS sector name. |
| `match` | `true` when `sic` and `heuristic_code` agree per the canonical SIC→GICS mapping; `false` signals classification drift. |

`match: false` is a drift-detection signal — it indicates the two classifiers disagree. The FFO/DDM/revenue-multiple model routing uses `sic` (not `heuristic_code`), so `match: false` does NOT mean the valuation is wrong — just that the heuristic and SIC paths disagree on sector labeling.

---

#### 3.2.6 Error Responses for `GET /api/v1/fair-value/:ticker`

| Status | Code | When |
|--------|------|------|
| 400 | `INVALID_TICKER` | Ticker format is invalid |
| 400 | `INVALID_PARAMETER` | `override_beta` or `override_rf` out of range |
| 404 | `TICKER_NOT_FOUND` | Ticker is not present in SEC's ticker→CIK index (genuinely unknown symbol) |
| 422 | `INSUFFICIENT_DATA` | Ticker exists but cannot be valued — typically pre-revenue issuers, clinical-stage biotechs, or fewer than the required financial periods |
| 422 | `MODEL_NOT_APPLICABLE` | No valuation model can be applied |
| 422 | `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` | 20-F filer using a taxonomy or reporting currency Midas does not yet cover. Most ADRs (TSM, ASML, BABA, …) are supported and produce real per-ADR values; this code only fires for genuinely unmapped taxonomies. |
| 429 | `RATE_LIMIT_EXCEEDED` | Rate limit exceeded |
| 500 | `CALCULATION_ERROR` | Internal calculation failure |

Full error-response format is documented in [§4 Error Handling](#4-error-handling).

---

#### `POST /api/v1/fair-value/bulk`

Calculate fair values for multiple stocks in a single request. Supports partial success — some tickers may succeed while others fail.

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
| `override_beta` | float | No | 0.0 - 3.0 | Shared beta override across all tickers |
| `override_rf` | float | No | 0.0 - 0.2 | Shared risk-free rate override |

**Response Shape:**

Failed tickers are inlined into the same `results[]` array (with Problem-Details error fields populated) so consumers can iterate the array in request order. There is no separate `failures[]` array.

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

### 3.3 Health & Monitoring Endpoints

#### `GET /api/v1/health/detailed`

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
    "cache": { "status": "healthy", "...": "..." },
    "external_apis": { "status": "healthy", "...": "..." },
    "memory": { "status": "healthy", "...": "..." }
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

#### `GET /api/v1/metrics`

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

### 3.4 API Key Management

#### `POST /api/v1/auth/keys`

Create a new API key. See [§2 Authentication](#2-authentication) for request and response format.

**Permission:** `manage:keys`

---

### 3.3.4 Per-Request Tracing

For debugging, you can ask the server to capture a full artifact bundle of one specific request — raw upstream payloads, intermediate stage outputs, the final response. Two ways to opt in:

```bash
# Header form
curl -H "X-API-Key: <key>" -H "X-Midas-Trace: 1" \
  "http://localhost:8080/api/v1/fair-value/AAPL"

# Query-param form
curl -H "X-API-Key: <key>" \
  "http://localhost:8080/api/v1/fair-value/AAPL?trace=1"
```

The response is unchanged from a normal request. The server-side bundle is written under `./artifacts/` (path is server-configurable; ask your administrator). The captured bundle is intended for engineers debugging the engine — its layout, retention, and consumption tooling are internal concerns and live in `CLAUDE.md`.

The `X-Request-ID` header on the response uniquely identifies the request and can be cross-referenced with server logs.

---

## 4. Error Handling

### 4.1 RFC 7807 Problem Details

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

| Field | Type | Description |
|-------|------|-------------|
| `type` | URI | Stable error-type URI (use this rather than `title` for branching) |
| `title` | string | Short human-readable label |
| `status` | integer | HTTP status code (matches the response status) |
| `detail` | string | Human-readable explanation specific to this request |
| `instance` | URI | Request path that produced the error |
| `code` | string | Machine-readable error code (see [§4.2](#42-error-code-reference)) — branch on this in client code |
| `timestamp` | ISO 8601 | When the error occurred |
| `method` | string | HTTP method of the failed request |
| `context` | object | Additional debugging context — fields vary per error code |

### 4.2 Error Code Reference

| Code | HTTP Status | Description |
|------|------------|-------------|
| `INVALID_TICKER` | 400 | Ticker format is invalid (must be 1-5 alphanumeric chars) |
| `INVALID_PARAMETER` | 400 | Query or body parameter is out of valid range |
| `INVALID_REQUEST` | 400 | Request body doesn't match expected schema |
| `TICKER_NOT_FOUND` | 404 | Ticker is not present in SEC's ticker→CIK index (genuinely unknown symbol) |
| `INSUFFICIENT_DATA` | 422 | Ticker resolves but cannot be valued: too few financial periods or no usable XBRL facts (e.g. pre-revenue / clinical-stage issuers) |
| `MODEL_NOT_APPLICABLE` | 422 | No valuation model can be applied to this company |
| `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` | 422 | 20-F filer using a taxonomy or reporting currency not yet covered. Most ADRs are supported. |
| `CALCULATION_ERROR` | 500 | Internal error during valuation calculation |
| `RATE_LIMIT_EXCEEDED` | 429 | Rate limit exceeded (check `Retry-After` header) |
| `AUTH_001` – `AUTH_008` | 401 / 403 | Authentication/authorization errors (see [§2](#2-authentication)) |

### 4.3 Distinguishing 422 Cases

The 422 codes (`INSUFFICIENT_DATA`, `MODEL_NOT_APPLICABLE`, `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED`) all mean "the ticker resolves but cannot be valued." They differ in *why*:

- **`INSUFFICIENT_DATA`** — SEC has filings but no usable XBRL fundamentals (typical for clinical-stage biotechs or pre-revenue issuers), or fewer than the required number of financial periods. Retry on this ticker later if the company starts filing usable data.
- **`MODEL_NOT_APPLICABLE`** — No valuation model in the engine's repertoire fits this company. Rare; usually indicates an unusual financial structure.
- **`FOREIGN_PRIVATE_ISSUER_UNSUPPORTED`** — The ticker is a foreign filer (20-F) using a taxonomy (JGAAP, K-IFRS, ifrs-smes) or reporting currency the engine has not been extended to handle. **Distinct from supported ADRs** — most major ADRs (TSM, ASML, BABA, BIDU, NVO, AZN, SAP, TM, RIO, BHP, etc.) are supported and return real per-ADR valuations.

---

## 5. Rate Limiting

### 5.1 Rate Limit Levels

Rate limits are checked in order. The most restrictive wins:

1. **API Key Rate Limit** — Per individual API key (default: 1000 req/period)
2. **IP Rate Limit** — Per client IP address
3. **Endpoint Rate Limit** — Per endpoint path
4. **Global Rate Limit** — Across all requests (1000 req/min default)

**Default Endpoint Limits:**

- Fair Value: 60 requests/minute
- Health endpoints: 30 requests/minute

### 5.2 Rate Limit Headers

All responses include rate limit information:

| Header | Description |
|--------|-------------|
| `X-RateLimit-Limit` | Total allowed requests in current window |
| `X-RateLimit-Remaining` | Requests remaining in current window |
| `X-RateLimit-Reset` | Unix timestamp when the window resets |
| `Retry-After` | Seconds to wait (only on 429 responses) |

### 5.3 Rate Limit Exceeded Response

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

When you receive a 429, wait at least `retry_after` seconds (or until `X-RateLimit-Reset`) before retrying. The two are equivalent — `retry_after` is delta-seconds, `X-RateLimit-Reset` is absolute timestamp.

---

## 6. Security

### 6.1 Security Response Headers

All API responses include the following headers:

| Header | Value | Purpose |
|--------|-------|---------|
| `X-Content-Type-Options` | `nosniff` | Prevent MIME type sniffing |
| `X-Frame-Options` | `DENY` | Prevent clickjacking |
| `X-XSS-Protection` | `1; mode=block` | Enable browser XSS filtering |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | Force HTTPS |
| `Content-Security-Policy` | `default-src 'self'` | Restrict resource loading |

### 6.2 CORS Configuration

| Setting | Value |
|---------|-------|
| Allowed Origins | `*` in development; restricted per deployment in production |
| Allowed Methods | `GET`, `POST`, `PUT`, `DELETE`, `OPTIONS` |
| Allowed Headers | `Origin`, `Content-Type`, `Authorization`, `X-API-Key`, `X-Request-ID` |
| Exposed Headers | `Content-Length`, `X-Request-ID` |
| Credentials | Allowed |
| Max Age | 12 hours |

### 6.3 API Key Security

- Keys are hashed with **SHA-256** before storage; raw keys are only returned at creation time.
- Keys can be individually **deactivated** or set to **expire** (`expires_at`).
- Usage is tracked per-endpoint with timestamps and IP addresses.
- Failed authentication attempts are logged with the key prefix (safe) for diagnostics.

### 6.4 API Key Best Practices for Consumers

- **Never commit API keys** — use environment variables or vault references.
- **Rotate keys regularly** — create new keys and deactivate old ones.
- **Use least-privilege permissions** — only grant the permissions each integration needs.
- **Set key expiration** — use `expires_at` for temporary or third-party access.
- **Treat keys like passwords** — they grant access on your behalf.

---

## 7. Glossary

Definitions of finance/engine terms that appear in API responses or error messages. (For deeper explanations of *how* these are calculated, see `docs/THESIS.md`.)

| Term | Definition |
|------|-----------|
| **ADR** | American Depositary Receipt — US-listed shares of a foreign company. |
| **Beta** | Measure of a stock's volatility relative to the market. |
| **CAGR** | Compound Annual Growth Rate. |
| **CapEx** | Capital Expenditures — spending on fixed assets. |
| **CIK** | Central Index Key — SEC's unique identifier for companies. |
| **CRP** | Country Risk Premium — additional return for investing in riskier countries. |
| **D&A** | Depreciation and Amortization. |
| **DCF** | Discounted Cash Flow — primary valuation method. |
| **DDM** | Dividend Discount Model — used for mature banks and insurers. |
| **DPS** | Dividends Per Share. |
| **EBITDA** | Earnings Before Interest, Taxes, Depreciation, and Amortization. |
| **EPS** | Earnings Per Share. |
| **EV** | Enterprise Value = Market Cap + Debt − Cash. |
| **FCF** | Free Cash Flow. |
| **FFO** | Funds From Operations — REIT-specific earnings metric; substitute for earnings when computing P/FFO. |
| **FRED** | Federal Reserve Economic Data (upstream macro source). |
| **GAAP** | Generally Accepted Accounting Principles (US filers). |
| **IFRS** | International Financial Reporting Standards (foreign filers). |
| **MRP** | Market Risk Premium — excess return of stocks over risk-free rate. |
| **NCAV** | Net Current Asset Value = Current Assets − Total Liabilities. Graham's deep-value metric; basis for the `graham_floor_per_share` field. |
| **NOPAT** | Net Operating Profit After Tax. |
| **NWC** | Net Working Capital = Current Assets − Current Liabilities. |
| **P/E** | Price-to-Earnings ratio. |
| **P/FCF** | Price-to-Free-Cash-Flow ratio. |
| **P/FFO** | Price-to-FFO ratio — the equivalent of P/E for REITs (FFO replaces earnings because depreciation depresses GAAP earnings for property-heavy entities). |
| **ROIC** | Return on Invested Capital. |
| **SEC EDGAR** | Securities and Exchange Commission's filing system (upstream fundamentals source). |
| **TTM** | Trailing Twelve Months — used by the `revenue_multiple` model as the revenue base. |
| **WACC** | Weighted Average Cost of Capital — the discount rate used in DCF. |
| **XBRL** | eXtensible Business Reporting Language — SEC filing data format. |

---

## Appendix A — Postman Collection

A Postman collection is available at `docs/postman_collection.json`. Import it into Postman and set:

- **Variable `base_url`**: `http://localhost:8080` (or your production URL)
- **Variable `api_key`**: Your API key

---

## Appendix B — OpenAPI Specification

The full OpenAPI 3.0.3 specification is available at:

- `docs/openapi.yaml` (YAML — canonical, hand-maintained)
- `docs/swagger.json` (JSON — auto-generated from struct annotations; may lag the canonical YAML)
- `http://localhost:8080/swagger` (interactive Swagger UI, when enabled)

When in doubt, **the OpenAPI YAML is the authoritative machine-readable contract.** This document and the OpenAPI spec should never disagree on endpoint or response shape — if they do, please open an issue.
