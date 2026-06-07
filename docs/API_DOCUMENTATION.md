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
| `read:fair_value` | `GET /api/v1/fair-value/:ticker`, `POST /api/v1/fair-value/:ticker`, `POST /api/v1/fair-value/bulk` |
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
| `AUTH_006` | 401 | No authentication information on the request context (internal precondition failure on a protected route) |
| `AUTH_007` | 500 | Authentication information malformed (internal type error) |
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
| `override_beta` | float | −5.0 to 5.0 | Custom beta for WACC calculation. Negative values allowed (inverse-correlated assets). |
| `override_rf` | float | −0.05 to 0.25 | Custom risk-free rate. Negative values allowed (EUR/JPY/CHF negative-rate regimes). |
| `trace` | bool | `1` to enable | Capture a per-request artifact bundle for debugging — see [§3.3.4](#334-per-request-tracing) |

> **NaN / ±Inf rejection:** `override_beta` and `override_rf` must be finite numbers.
> Passing `?override_beta=NaN`, `?override_beta=Inf`, or any non-finite value returns
> **400** `INVALID_PARAMETER`. `strconv.ParseFloat` accepts these strings without error,
> so the guard is explicit — a non-finite value would otherwise propagate silently into
> WACC/DCF and produce a non-finite response or an internal error.

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
  "calculation_version": "4.6",
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
  "calculation_version": "4.6",
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
| `data_quality_score` | number | 0-100 score reflecting data freshness and completeness — may be fractional (e.g. `85.5`), not strictly integer (see [§3.2.2](#322-data-quality-score)). |
| `data_quality_grade` | string | Letter grade derived from `data_quality_score` (see [§3.2.2](#322-data-quality-score)). |
| `calculation_method` | string | Which valuation model fired (see [§3.2.3](#323-calculation-method-values)). |
| `calculation_version` | string | Engine math version. Bumped when model math changes; consumers can use this to detect engine upgrades affecting historical comparisons. Current engine: `4.6`. |
| `assumption_profile` | string | Resolved assumption-profile ID in `archetype:maturity` form (e.g. `mature_large_bank:mature`). Identifies the calibration record (DCF horizon, terminal method, discount method, growth caps) the engine applied to this company. **Omitted** when no profile registry is wired (legacy/test paths). See [§3.2.7](#327-assumption_profile--resolution_trace). |
| `resolution_trace` | object | Structured audit trail of *how* the assumption profile was selected — matched rule, fallback reason, config hash, missing facts. See [§3.2.7](#327-assumption_profile--resolution_trace). |
| `sanity_check` | object | Cross-validation against sector median multiples (see [§3.2.4](#324-sanity-check)). |
| `industry` | object | Dual industry classification (see [§3.2.5](#325-industry-classification)). |
| `currency` | string | ISO-4217 code. All monetary per-share fields are denominated in this currency. Always `"USD"` — non-USD reporting currencies are FX-converted upstream. |
| `adr_ratio_applied` | integer | Ordinary-shares-per-ADR multiplier the engine applied. `1` for domestic 10-K filers; non-1 for ADRs (TSM=5, BABA=8). Omitted when zero. |
| `current_price` | float | Live per-share market price at calculation time, in the same per-share basis as `dcf_value_per_share` (per-ADR for ADRs). Compute upside as `(dcf_value_per_share - current_price) / current_price`. Omitted when unavailable. |
| `warnings` | string[] | Diagnostic strings — sector-multiple notes, revenue-base path used by the revenue-multiple model, graham-floor data-quality signals, sanity-check divergences. Always present (may be empty). |
| `applied_overrides` | map | Per-knob echo of valuation overrides that were explicitly set by the request. Each entry: `"knob_name": {"value": <scalar>, "source": "request"}`. **Omitted** (not `{}`) when no overrides were supplied. See [§3.2.9](#329-applied_overrides--per-knob-echo). |

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
| `sector_median_pe` | float | Sector median P/E the implied figure is compared against |
| `implied_ev_ebitda` | float | Enterprise Value / EBITDA |
| `sector_median_ev_ebitda` | float | Sector median EV/EBITDA |
| `implied_pfcf` | float | DCF Value / FCF per Share. **Omitted** when FCF is zero. |
| `sector_median_pfcf` | float | Sector median P/FCF. **Omitted** when unknown. |
| `is_reasonable` | bool | `true` only when **all** implied multiples fall within `[0.5×, 2.0×]` of their sector median; `false` if any single one breaches |
| `flags` | string[] | Per-metric divergence strings (e.g., `"DCF-implied P/E (9.3) is <0.5x sector median (28.0) for TECH"`) |

The entire `sanity_check` object is present **only** when the `multi_stage_dcf` path fires *and* `config/industry_multiples.json` has matching sector entries; it is omitted for DDM / FFO / revenue_multiple routes.

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
| 400 | `INVALID_PARAMETER` | `override_beta` or `override_rf` is out of range (beta: [−5, 5]; rf: [−5%, 25%]) OR is a non-finite value (NaN, +Inf, −Inf) |
| 404 | `TICKER_NOT_FOUND` | Ticker is not present in SEC's ticker→CIK index (genuinely unknown symbol) |
| 422 | `INSUFFICIENT_DATA` | Ticker exists but cannot be valued — typically pre-revenue issuers, clinical-stage biotechs, or fewer than the required financial periods |
| 422 | `MODEL_NOT_APPLICABLE` | No valuation model can be applied |
| 422 | `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` | 20-F filer using a taxonomy or reporting currency Midas does not yet cover. Most ADRs (TSM, ASML, BABA, …) are supported and produce real per-ADR values; this code only fires for genuinely unmapped taxonomies. |
| 429 | `RATE_LIMIT_EXCEEDED` | Rate limit exceeded |
| 500 | `CALCULATION_ERROR` | Internal calculation failure |

Full error-response format is documented in [§4 Error Handling](#4-error-handling).

---

#### `POST /api/v1/fair-value/:ticker`

Calculate the intrinsic fair value for a single stock with optional per-request valuation knob overrides.

**Permission:** `read:fair_value`

An empty body or omitted `options` block produces a response **byte-identical** to `GET /api/v1/fair-value/:ticker` for the same ticker (`TestPostFairValue_EmptyBody_EqualsGET` pins this). POST exists so override knobs can be passed without stuffing them into query parameters.

**Path Parameters:** Same as GET — `ticker` (string, 1-5 alphanumeric chars, uppercase).

**Request Body (optional):**

```json
{
  "options": {
    "terminal_growth_rate": -0.01,
    "horizon_years": 7,
    "tax_rate": 0.21,
    "beta": 1.1,
    "risk_free_rate": 0.045,
    "market_risk_premium": 0.05,
    "terminal_method": "exit_multiple",
    "terminal_multiple": 14.0,
    "max_growth_rate": 0.4,
    "min_growth_rate": -0.2,
    "terminal_growth_cap": 0.03,
    "growth_stages": {
      "stage1_years": 3,
      "stage2_years": 4,
      "stage3_years": 0
    }
  }
}
```

#### 3.2.8 `ValuationOverrides` — Knob Catalog

All fields are optional pointers. Omitted = use engine default.

| Knob | Type | Layer-1 Range / Allowed | Description |
|------|------|------------------------|-------------|
| `terminal_growth_rate` | float | [−20%, 50%] (negative allowed) | Explicit terminal growth rate for Gordon Growth formula. Layer-2 dynamic: must be at least 1% below computed WACC (`g ≤ WACC − 1%`). A value inside the static band can still fail when WACC is low. |
| `terminal_growth_cap` | float | [−20%, 50%] | Cap applied during auto-derivation of terminal growth. Default 0.03. No effect when `terminal_growth_rate` is explicit. |
| `horizon_years` | int | [1, 50] | DCF forecast horizon. Layer-2 dynamic: effective horizon must not exceed 50 (the DCF engine cap); must be ≥ sum of growth-stage years when stages also overridden. |
| `growth_stages.stage1_years` | int | ≥ 0 | High-growth phase duration (default 3). Request-sourced stages that push the effective horizon > 50 → 422 `knob = "growth_stages"`. |
| `growth_stages.stage2_years` | int | ≥ 0 | Fade/transition phase duration (default 4). |
| `growth_stages.stage3_years` | int | ≥ 0 | Long-tail extension (default 0). |
| `max_growth_rate` | float | [−100%, 1000%] | Upper clamp in the growth estimator. Default 0.5. Layer-2: must be ≥ `min_growth_rate`. |
| `min_growth_rate` | float | [−100%, 1000%] (negative ok) | Lower clamp in the growth estimator. Default −0.3. Layer-2: must be ≤ `max_growth_rate`. |
| `terminal_method` | string | `"gordon_growth"` \| `"exit_multiple"` | Terminal-value model selector. `"gordon_growth"` suppresses exit-multiple blending (pure Gordon TV). `"exit_multiple"` blends a 50/50 Gordon/exit-multiple average (NOT a pure exit-multiple TV). Layer-2: `exit_multiple` requires a resolvable multiple (Layer-2 error otherwise). |
| `terminal_multiple` | float | [0, 100] | EV/EBITDA exit multiple for `terminal_method = exit_multiple`. Falls back to industry default when absent. |
| `tax_rate` | float | [−50%, 100%] (negative ok for NOLs) | Effective corporate tax rate for FCF, WACC, and ModelInput. |
| `beta` | float | [−5, 5] (negative ok for inverse-correlated assets) | Equity beta for CAPM. Conflicts with legacy `override_beta` on the bulk endpoint. |
| `risk_free_rate` | float | [−5%, 25%] (negative ok for negative-rate regimes) | Nominal risk-free rate. Conflicts with legacy `override_rf` on bulk body and GET query param. |
| `market_risk_premium` | float | [0%, 30%] (**floor 0** — economically required) | Equity risk premium in CAPM. Negative value → 422 `INVALID_OVERRIDE`. |

**Validation layers:**
- **Layer 1 (static):** Per-knob range and enum checks (see table above). Any value inside the static band computes without 500; the engine ranges are aligned with the contract. Invalid → 422 `INVALID_OVERRIDE` immediately.
- **Layer 2 (dynamic, in `params.Resolve*`):** Invariants that depend on the ticker's computed values:
  - `terminal_growth_rate` within 1% of computed WACC (real ceiling = `WACC − 1%`, dynamic per ticker).
  - CAPM inputs (beta/rf/MRP) driving computed WACC ≤ 0 → 422 with `knob = "wacc"`.
  - Effective horizon > 50 from request-sourced `horizon_years` or `growth_stages` → 422 with `knob = "horizon_years"` or `knob = "growth_stages"`.
  - `min_growth_rate` > `max_growth_rate`; `horizon_years` < stage-sum; `exit_multiple` unresolvable.
  All Layer-2 failures → 422 `INVALID_OVERRIDE`; `context.knob` names the offending parameter, `context.value` the rejected value, `context.limit` the numeric threshold when applicable.

> **Validation asymmetry between GET and POST (important for clients):**
> The legacy GET query parameters (`override_beta`, `override_rf`) use simple range checks that return **400** `INVALID_PARAMETER` on out-of-range or non-finite (NaN, ±Inf) input. The POST `options` block uses the two-layer system above and always returns **422** `INVALID_OVERRIDE`. Do not rely on 400 for override failures on the POST endpoint.

**Success Response (200 OK):**

Same `FairValueResponse` shape as GET, with one additional field when overrides were supplied:

```json
{
  "ticker": "AAPL",
  "wacc": 0.092,
  "dcf_value_per_share": 162.10,
  "calculation_method": "multi_stage_dcf",
  "calculation_version": "4.6",
  "applied_overrides": {
    "tax_rate":    { "value": 0.21,  "source": "request" },
    "horizon_years": { "value": 7,   "source": "request" }
  },
  "...": "...all other FairValueResponse fields..."
}
```

#### 3.2.9 `applied_overrides` — Per-Knob Echo

`applied_overrides` is a map from knob name → `{value, source}` object:

| Sub-field | Type | Description |
|-----------|------|-------------|
| `value` | any | Resolved scalar the engine used. Float64 for rate/multiplier fields, int for year fields, string for method fields. |
| `source` | string | Always `"request"` in v1 — only request-set knobs appear here. Profile-sourced or config-sourced knobs are NOT echoed. |

`applied_overrides` is **omitted** (not `null`, not `{}`) when no overrides were supplied. `POST {}` and `GET` responses are byte-identical for the same ticker.

#### 3.2.10 Error Responses for `POST /api/v1/fair-value/:ticker`

| Status | Code | When |
|--------|------|------|
| 400 | `INVALID_TICKER` | Ticker format is invalid |
| 400 | `INVALID_REQUEST` | Request body malformed (not valid JSON, wrong shape) |
| 404 | `TICKER_NOT_FOUND` | Ticker not found |
| 422 | `INVALID_OVERRIDE` | Layer-1 or Layer-2 validation failure (see knob catalog above). `context.knob` names the offending parameter. |
| 422 | `INSUFFICIENT_DATA` | Ticker cannot be valued |
| 422 | `MODEL_NOT_APPLICABLE` | No model applicable |
| 422 | `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` | 20-F filer outside coverage |
| 429 | `RATE_LIMIT_EXCEEDED` | Rate limit exceeded |
| 500 | `CALCULATION_ERROR` | Internal failure |

---

#### 3.2.7 `assumption_profile` & `resolution_trace`

Every valuation resolves an **assumption profile** — the calibration record that drives the company's DCF horizon length, terminal-value method, discount method, and growth caps based on its *archetype* (e.g. mature large bank, maturing tech dividend payer, data-center REIT) and *maturity* stage. Two response fields expose this resolution:

- **`assumption_profile`** (string) — the resolved profile ID in `archetype:maturity` form, e.g. `mature_large_bank:mature`. Use it to correlate a result with the exact calibration that produced it.
- **`resolution_trace`** (object) — the structured audit trail of *how* that profile was chosen. Useful for explaining an unexpected horizon/method and for replay determinism.

| `resolution_trace` sub-field | Type | Description |
|------------------------------|------|-------------|
| `profile_id` | string | Same value as the top-level `assumption_profile`. |
| `source` | string | How the profile was selected: `explicit` (a non-wildcard industry rule matched and had a profile entry), `inferred` (partial match; reserved for future use), or `fallback` (no rule matched, or the matched archetype/maturity had no entry — the conservative default profile was applied). |
| `resolver_version` | string | Version of the resolution algorithm that ran. |
| `config_version` | string | Version of `config/assumption_profiles.json` in effect. |
| `config_hash` | string | SHA-256 of the profile config, for replay reproducibility. Omitted when unset. |
| `matched_rule_id` | string | ID of the archetype rule that matched. Omitted on `fallback`. |
| `fallback_reason` | string | Why the fallback fired. Present only when `source = fallback`. |
| `missing_facts` | string[] | Facts that were unavailable during resolution (drove inference/fallback). Omitted when none. |
| `human_reason` | string | Human-readable one-line summary of the resolution decision. |

Both fields are **omitted** on the legacy mature-large-bank DDM path and on test paths where no profile registry is wired. `source = "fallback"` is a useful dashboard signal: it means the engine could not match a calibrated archetype and fell back to conservative defaults.

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
| `override_beta` | float | No | −5.0 to 5.0 | Shared beta override across all tickers (legacy; prefer `options.beta`) |
| `override_rf` | float | No | −0.05 to 0.25 | Shared risk-free rate override (legacy; prefer `options.risk_free_rate`) |
| `options` | ValuationOverrides | No | See [§3.2.8](#328-valuationoverrides--knob-catalog) | Structured per-request valuation knob overrides, applied to ALL tickers. Must not duplicate `override_beta` / `override_rf` for the same knob — 422 `INVALID_OVERRIDE` if both are set. |

**Response Shape:**

The response has up to three top-level keys: `results[]` (successful valuations, in request order), `failures[]` (per-ticker failures), and `summary` (counts). `failures[]` is **omitted when every ticker succeeds**. Each failure entry is a compact `{ticker, error_code, message}` triple — **not** a full RFC 7807 Problem-Details object (those are returned only for whole-request errors such as a malformed body). Iterate `results[]` and `failures[]` separately and match items by `ticker`.

```json
{
  "results": [
    {
      "ticker": "AAPL",
      "wacc": 0.092,
      "dcf_value_per_share": 156.42,
      "calculation_method": "multi_stage_dcf",
      "calculation_version": "4.6",
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
  "failures": [
    {
      "ticker": "INVALID",
      "error_code": "INVALID_TICKER",
      "message": "Invalid ticker format: must be 1-5 alphanumeric characters"
    }
  ],
  "summary": {
    "total_requested": 3,
    "successful": 2,
    "failed": 1
  }
}
```

Each `failures[].error_code` is one of the [§4.2 error codes](#42-error-code-reference): `INVALID_TICKER`, `INVALID_OVERRIDE`, `TICKER_NOT_FOUND`, `INSUFFICIENT_DATA`, `MODEL_NOT_APPLICABLE`, `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED`, or `CALCULATION_ERROR`.

For `INVALID_OVERRIDE` failures, the `failures[].knob` field names the offending valuation parameter so programmatic consumers can identify the problem without string-parsing `message`. Example:

```json
{
  "ticker": "AAPL",
  "error_code": "INVALID_OVERRIDE",
  "message": "market_risk_premium must be >= 0",
  "knob": "market_risk_premium"
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
| `INVALID_PARAMETER` | 400 | GET query parameter out of valid range (`override_beta` / `override_rf`) |
| `INVALID_REQUEST` | 400 | Request body doesn't match expected schema |
| `INVALID_OVERRIDE` | 422 | A valuation knob in the POST `options` block failed Layer-1 static validation (out-of-range / wrong enum) or a Layer-2 cross-knob invariant (`terminal_growth_rate` ≥ WACC, `min_growth_rate` > `max_growth_rate`, exit multiple unresolvable, etc.). The `context.knob` field names the offending parameter. Also returned in `BulkFailure.knob` for per-ticker failures. |
| `TICKER_NOT_FOUND` | 404 | Ticker is not present in SEC's ticker→CIK index (genuinely unknown symbol) |
| `INSUFFICIENT_DATA` | 422 | Ticker resolves but cannot be valued: too few financial periods or no usable XBRL facts (e.g. pre-revenue / clinical-stage issuers) |
| `MODEL_NOT_APPLICABLE` | 422 | No valuation model can be applied to this company |
| `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` | 422 | 20-F filer using a taxonomy or reporting currency not yet covered. Most ADRs are supported. |
| `CALCULATION_ERROR` | 500 | Internal error during valuation calculation |
| `INTERNAL` | 500 | Unhandled internal error — a panic was recovered by the server; the underlying detail is intentionally not exposed in the response |
| `RATE_LIMIT_EXCEEDED` | 429 | Rate limit exceeded (check `Retry-After` header) |
| `AUTH_001` – `AUTH_008` | 401 / 403 / 500 | Authentication/authorization errors (see [§2](#2-authentication)) |

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
| Allowed Headers | `Origin`, `Content-Type`, `Content-Length`, `Accept-Encoding`, `X-CSRF-Token`, `Authorization`, `X-API-Key`, `X-Request-ID`, `X-Midas-Trace` |
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
