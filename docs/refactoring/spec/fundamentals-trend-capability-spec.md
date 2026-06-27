# Fundamentals-Trend Capability — Change-Request Spec

| | |
|---|---|
| **Status** | NOT STARTED — change request, drafted for owner review |
| **Author** | PM agent (delegated change request, owner-authorized for the midas repo) |
| **Date** | 2026-06-27 |
| **Type** | New REST capability (read-only diagnostic endpoint) |
| **Consumer** | The **orchestrator's `thesis`/`explain` operation (X1)** — see parent PRD `Strade/docs/pendingwork/2026-06-27-thesis-operation-prd.md` (P0 story #3, the "`fundamentals_trend` is upstream" decision, §6 contract sketch). **This is the critical-path dependency for X1's trend block — Milestone A in that PRD gates orchestrator Milestone B.** |
| **Related (midas, existing)** | `GET /api/v1/fair-value/{ticker}` (`docs/CONTRACTS.md` §1), `FairValueResponse.data_quality_grade`/`as_of`/`currency`/`warnings` provenance pattern (the model this spec mirrors), `graham-floor-metrics-spec.md` (archived — precedent: surface already-pulled SEC data as a provenanced response block), `HistoricalFinancialData` entity + `FinancialDataRepository.GetHistorical(ctx, ticker, periods)` (existing internal multi-period source) |
| **Downstream follow-ups (NOT in this spec, NOT owned here)** | Parent `Strade/docs/TOOL_REGISTRY.md` gains a `fundamentals_trend` capability entry; `orchestrator/adapters/midas.py` gains a `fundamentals_trend()` adapter method + a `docs/MODULE_REFERENCE.md` note. Those are separate delegated changes in their own repos/contexts. |

---

## 1. Summary

Add a read-only REST endpoint to midas that returns a **multi-period fundamentals
trend** for one ticker — at minimum **revenue, operating margin, and free cash
flow (FCF)** across the most recent annual fiscal periods — with **per-value
provenance** (as-of + data-quality flag) and **explicit missing-period flags**.
Values are **never back-filled or interpolated**: a period that midas cannot
resolve is returned as an explicit gap, not a synthesized number.

This is a **transparency / diagnostics capability, not a new valuation model**.
It reuses the SEC `HistoricalFinancialData` midas already fetches and parses for
DCF; the valuation engine, growth estimator, model router, and WACC math are all
untouched. It is the midas-side half of the parent thesis operation: the
orchestrator composes the provenanced series into a narrated thesis and computes
**no** trend math of its own (parent PRD Design Principle #1, Non-Goal #5).

---

## 2. Motivation

The orchestrator's X1 thesis operation needs to show **direction, not just a
point estimate** — "is revenue/margin/FCF improving or deteriorating over the
last several years?" — alongside the single DCF fair value midas already returns.
Today midas exposes only a **single-period** valuation surface
(`/api/v1/fair-value/{ticker}`); there is **no** multi-period / trend /
time-series endpoint (confirmed: README §endpoint table and `CONTRACTS.md` list
only fair-value, bulk, health, metrics, auth — see §3 below).

The data, however, already exists inside midas: `FinancialDataRepository`
exposes `GetHistorical(ctx, ticker, periods)` returning `HistoricalFinancialData`,
and the SEC CompanyFacts pull underlying DCF is inherently multi-period. So this
is **surfacing existing multi-period data through a new contract**, mirroring
exactly how the Graham-floor spec surfaced already-pulled balance-sheet data —
not a new data acquisition or modeling effort.

The parent PRD makes honesty-by-construction an **enforced, tested** property: the
orchestrator's narration-faithfulness gate (every numeral in prose must exist in
the tool payload) and provenance-coverage gate (every surfaced figure is
tool-sourced, blocking in CI) can only pass if midas hands the orchestrator
**provenanced, gap-flagged** series. This spec exists to make those gates passable.

---

## 3. Current State — Does a Trend Capability Already Exist?

**No multi-period / trend / time-series REST capability exists in midas today.**
The published surface (`README.md` endpoint table, `CONTRACTS.md` §1–§9) is
exclusively single-point: `GET`/`POST /api/v1/fair-value/{ticker}`,
`POST /api/v1/fair-value/bulk` (each ticker still a single point estimate),
plus health/metrics/auth. A grep of the midas docs for
`trend`/`multi-period`/`time-series` surfaces only **internal** multi-period
machinery and unrelated mentions:

- `FinancialDataRepository.GetHistorical(ctx, ticker, periods)` and the
  `HistoricalFinancialData` entity (`CONTRACTS.md` §Repository Interfaces) —
  multi-period SEC data is **fetched and stored internally** for the growth
  estimator, but **never exposed** as a response.
- `financial_data` table carries `(ticker, filing_period)` rows (`CONTRACTS.md`
  §Database Schema) — the per-period substrate already exists.
- `cmd/replay` per-stage diffs, `growth_rates[]` per-year array in
  `FairValueResponse` — these are valuation internals, not a fundamentals trend.

**Conclusion:** build a thin **new read endpoint** over the existing
`GetHistorical` path. Do **not** duplicate the historical fetch/parse pipeline,
and do **not** re-derive series the engine already computes — read the stored
`HistoricalFinancialData` and shape it into the provenanced contract below.

---

## 4. Proposed Capability Contract

### 4.1 Endpoint

```
GET /api/v1/fundamentals-trend/{ticker}
```

- **Auth:** `X-API-Key` header (consistent with all protected midas endpoints).
  Suggested permission: `read:fundamentals_trend` (or reuse `read:fair_value` —
  **open question Q4**).
- **Method/shape rationale:** GET single-ticker, matching `GET /api/v1/fair-value/{ticker}`
  exactly so the orchestrator adapter mirrors its existing midas call style. Bulk
  is **out of v1** (open question Q3) — X1 is strictly single-ticker.

**Path Parameters**

| Name | Type | Required | Constraints |
|------|------|----------|-------------|
| `ticker` | string | Yes | 1–5 chars, uppercase — same validation as fair-value |

**Query Parameters**

| Name | Type | Required | Description | Constraints |
|------|------|----------|-------------|-------------|
| `periods` | int | No | Max annual periods to return, most-recent-first | 1–10; default 5. Out-of-range → 400 `INVALID_PARAMETER` (mirror fair-value GET guard) |
| `metrics` | string (csv) | No | Subset of series to return | Subset of `{revenue,operating_margin,fcf}`; unknown member → 400 `INVALID_PARAMETER`. Default = all three |

### 4.2 The metric set (confirm/justify)

v1 ships **exactly three series**, matching the parent PRD's `fundamentals_trend`
block and chosen because each is directly derivable from data midas already
parses for DCF, and together they answer the "direction" question:

| Series | Definition | Source (already in midas) |
|---|---|---|
| `revenue` | GAAP revenue per fiscal period | SEC CompanyFacts `Revenue`/`Revenues` → `HistoricalFinancialData` |
| `operating_margin` | `operating_income / revenue` per period (decimal, e.g. `0.28`) | `FinancialData.operating_income` ÷ `revenue` (both stored) |
| `fcf` | Free cash flow per period = `operating_cash_flow − capex` | SEC cash-flow facts already pulled for DCF |

**Justification for this set, no more:** these three are the parent PRD's named
trio and the minimum that distinguishes top-line growth, profitability direction,
and cash generation. `writing-minimal-code` posture — additional series (EPS,
gross margin, debt) are **out of v1**; add later if the thesis narration proves a
gap. Use the **normalized** operating income basis already used by DCF
(`normalized_operating_income`) where available, and flag in `data_quality` when
the normalized figure was substituted (so the orchestrator can caveat it).

### 4.3 Response 200 (`application/json`)

```json
{
  "ticker": "AAPL",
  "as_of": "2026-06-27T10:30:00Z",
  "currency": "USD",
  "reporting_basis": "annual",
  "periods": ["FY22", "FY23", "FY24"],
  "series": {
    "revenue": [
      { "period": "FY22", "value": 394328000000, "as_of": "2022-09-24",
        "data_quality": "A", "missing": false },
      { "period": "FY23", "value": 383285000000, "as_of": "2023-09-30",
        "data_quality": "A", "missing": false },
      { "period": "FY24", "value": 391035000000, "as_of": "2024-09-28",
        "data_quality": "A", "missing": false }
    ],
    "operating_margin": [
      { "period": "FY22", "value": 0.302, "as_of": "2022-09-24",
        "data_quality": "A", "missing": false },
      { "period": "FY23", "value": null, "as_of": null,
        "data_quality": null, "missing": true,
        "missing_reason": "operating_income unresolved for FY23" },
      { "period": "FY24", "value": 0.315, "as_of": "2024-09-28",
        "data_quality": "B", "missing": false }
    ],
    "fcf": [ "…same per-point shape…" ]
  },
  "adr_ratio_applied": 1,
  "warnings": []
}
```

**Top-level response schema (`FundamentalsTrendResponse`)**

| Field | Type | Description |
|---|---|---|
| `ticker` | string | Echoed ticker |
| `as_of` | datetime (ISO 8601) | When midas assembled this response (request-time), not a data timestamp |
| `currency` | string | ISO-4217; always `"USD"` — engine FX-converts upstream (mirror `FairValueResponse.currency`) |
| `reporting_basis` | string | `"annual"` for v1. (`"ttm"`/`"quarterly"` reserved — **open question Q1**) |
| `periods` | string[] | Ordered period labels (oldest→newest), e.g. `["FY22","FY23","FY24"]` |
| `series` | object | Map of `{revenue, operating_margin, fcf}` → array of `TrendPoint` (one per label in `periods`, same length & order) |
| `adr_ratio_applied` | integer | Ordinary-shares-per-ADR multiplier, mirroring `FairValueResponse` (omitted when zero). Per-share metrics out of v1, but carried for cross-field consistency if added |
| `warnings` | string[] | Assumption/quality warnings, mirror `FairValueResponse.warnings` (optional, omit if empty) |

**Per-value schema (`TrendPoint`)** — the provenance unit:

| Field | Type | Description |
|---|---|---|
| `period` | string | Fiscal-period label, e.g. `"FY24"` |
| `value` | float \| null | The figure; **`null` iff `missing == true`** — never a back-filled/interpolated number |
| `as_of` | date \| null | Filing/data as-of for **this period's** underlying facts; `null` when missing |
| `data_quality` | string \| null | Per-value grade `A/B/C/D/F`, reusing midas's existing `data_quality_grade` vocabulary; `null` when missing |
| `missing` | bool | `true` when midas could not resolve this period for this series |
| `missing_reason` | string | Present **only** when `missing == true`; names which input was unresolved. Omitted otherwise |

**Invariants (testable):**
1. Every series array has **exactly** `len(periods)` points, **same order** as `periods`.
2. `value != null ⇔ missing == false`. No interpolation, no carry-forward.
3. Provenance is **data, not prose** — every non-missing `value` carries `as_of`
   + `data_quality`. (This is what makes the orchestrator provenance-coverage gate
   pass.)

### 4.4 Error modes (align with existing midas typed failures)

All errors use the existing **RFC 7807** `application/problem+json` envelope and
**reuse the existing error codes** (`CONTRACTS.md` §Error Codes) — no new error
taxonomy:

| Condition | HTTP | Code | Notes |
|---|---|---|---|
| Empty/invalid ticker | 400 | `INVALID_TICKER` | Same as fair-value |
| `periods`/`metrics` out of range / unknown | 400 | `INVALID_PARAMETER` | Mirror fair-value GET override guard (incl. non-finite) |
| Missing/invalid/expired API key | 401 | `AUTH_00x` | Unchanged auth middleware |
| Insufficient permission | 403 | `AUTH_008` | If a dedicated permission is chosen (Q4) |
| Ticker not in any data source | 404 | `TICKER_NOT_FOUND` | Same as fair-value |
| Foreign private issuer (20-F, unsupported taxonomy) | 422 | `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED` | **Whole-ticker** failure — same trigger as fair-value |
| Not enough historical data for **any** period of **any** requested series | 422 | `INSUFFICIENT_DATA` | Whole-ticker failure |
| Rate limit | 429 | `RATE_LIMIT_EXCEEDED` | Unchanged |
| Internal failure | 500 | `CALCULATION_ERROR` | Unchanged |

**Whole-ticker 422 vs per-period gap — the load-bearing distinction:**
- If midas can resolve **at least one** period for **at least one** requested
  series, return **200** with per-period `missing:true` flags for the gaps.
- Return **422 `INSUFFICIENT_DATA`** only when **nothing** is resolvable
  (consistent with fair-value semantics, and lets the orchestrator degrade to
  `partial` rather than fabricate).

---

## 5. Engineering User Stories

Each story: role / want / so-that, acceptance criteria (AC), priority, deps.
"Provenance-coverage gate" / "narration-faithfulness gate" refer to the
**orchestrator's** CI gates (parent PRD §3) that this capability must feed.

---

### FT-1 — Happy path: multi-period trend for a domestic filer (P0)

**As** the orchestrator thesis operation, **I want** a provenanced multi-period
revenue/operating-margin/FCF series for a valid US 10-K filer, **so that** the
thesis can show direction, not just a point estimate.

**AC**
- `GET /api/v1/fundamentals-trend/AAPL` with a valid key → **200**.
- Response carries `periods` (≥3 where SEC data allows) and a `series` block with
  `revenue`, `operating_margin`, `fcf`, each array length == `len(periods)`, same order.
- Each non-missing point carries `value`, `as_of`, `data_quality`, `missing:false`.
- `currency:"USD"`; `reporting_basis:"annual"`.
- Reads the existing `GetHistorical` path; **no** new SEC fetch pipeline added;
  valuation engine untouched (verified: no change under `internal/services/valuation/`).

**Priority:** P0. **Deps:** existing `FinancialDataRepository.GetHistorical`.

---

### FT-2 — Missing periods are flagged, never back-filled (P0)

**As** the orchestrator, **I want** unresolved periods returned as explicit gaps,
**so that** the narration never presents an interpolated number as reported and
the honesty-by-construction property holds.

**AC**
- When a period/series cell is unresolved: that `TrendPoint` has `value:null`,
  `as_of:null`, `data_quality:null`, `missing:true`, and a `missing_reason` string.
- **No** interpolation, carry-forward, or zero-fill — asserted by a test where a
  known-gap fixture (e.g. a restatement gap) yields `missing:true`, not a synthesized value.
- Series array length still == `len(periods)` (gaps occupy their slot).
- `periods` still lists every requested/available label including the gapped one.

**Priority:** P0. **Deps:** FT-1.

---

### FT-3 — FPI / insufficient-data return typed failures, never a fabricated trend (P0)

**As** the orchestrator, **I want** un-trendable tickers to fail with the same
typed errors as fair-value, **so that** the thesis can emit a first-class
"can't, because X" outcome instead of inventing a series.

**AC**
- A 20-F foreign-private-issuer ticker → **422 `FOREIGN_PRIVATE_ISSUER_UNSUPPORTED`**,
  RFC 7807 body, **no** partial series payload.
- A ticker with no resolvable period for any requested series → **422 `INSUFFICIENT_DATA`**.
- A ticker present but with **some** resolvable data → **200** with per-period
  `missing` flags (NOT 422) — the whole-ticker-vs-gap boundary in §4.4 is asserted.
- Unknown ticker → **404 `TICKER_NOT_FOUND`**. Error codes/envelope identical to
  fair-value (no new taxonomy).

**Priority:** P0. **Deps:** FT-1, FT-2.

---

### FT-4 — Per-value provenance carried as data (P0)

**As** the orchestrator, **I want** every returned value to carry its own as-of +
data-quality flag as structured fields, **so that** the
**provenance-coverage gate** (blocking in orchestrator CI) and
**narration-faithfulness gate** can pass.

**AC**
- Every non-missing `TrendPoint` carries `as_of` (this period's data timestamp,
  not the response time) and `data_quality` from the existing `A/B/C/D/F` vocabulary.
- Top-level `as_of` is the response-assembly time and is distinct from per-point `as_of`.
- A contract test asserts the invariant `value != null ⇔ (as_of != null && data_quality != null && missing == false)`.
- Provenance is structured fields, never embedded in a prose/string blob.

**Priority:** P0. **Deps:** FT-1. **Note:** this is the story the orchestrator's N1 provenance gate depends on.

---

### FT-5 — Operating margin / FCF derivation is correct and basis-consistent (P0)

**As** the orchestrator, **I want** `operating_margin` and `fcf` derived on the
same normalized basis midas uses for DCF, **so that** the trend is consistent with
the fair value shown beside it.

**AC**
- `operating_margin` = `operating_income / revenue` per period (decimal); uses
  `normalized_operating_income` where the engine does, and flags substitution via
  `data_quality` (or a `warnings[]` entry).
- `fcf` = `operating_cash_flow − capex` per period from the same SEC facts DCF uses.
- ADR tickers: if/when per-share metrics are added they honor `adr_ratio_applied`;
  v1 absolute-dollar series document the basis in `reporting_basis`/`warnings`.
- Golden-fixture test for one domestic + one ADR ticker pins the derived values.

**Priority:** P0. **Deps:** FT-1.

---

### FT-6 — `periods`/`metrics` query params (P1)

**As** the orchestrator, **I want** to bound period count and select a metric
subset, **so that** the thesis fetches only what it renders.

**AC**
- `?periods=3` returns at most 3 most-recent annual periods; out-of-range → 400 `INVALID_PARAMETER`.
- `?metrics=revenue,fcf` returns only those series; unknown member → 400 `INVALID_PARAMETER`.
- Defaults (5 periods, all three series) match FT-1 when params omitted.

**Priority:** P1. **Deps:** FT-1.

---

### FT-7 — Contract docs + OpenAPI updated within midas (P0, doc-only)

**As** a midas maintainer, **I want** the new endpoint reflected in midas's own
published contract, **so that** the orchestrator adapter binds to a documented surface.

**AC**
- `docs/openapi.yaml` gains the `GET /api/v1/fundamentals-trend/{ticker}` path,
  `FundamentalsTrendResponse` + `TrendPoint` schemas, params, and the reused error codes.
- `CONTRACTS.md` gains a new endpoint section (after §1b) with the response/error tables above.
- `README.md` endpoint table gains the row; `ARCHITECTURE.md` notes the read path
  reuses `GetHistorical` (no engine change).
- `swag`/Swagger annotations consistent with `swag-version-alignment-spec.md` conventions.

**Priority:** P0 (ships with the endpoint). **Deps:** FT-1 implemented.

---

### FT-8 — TTM row decision recorded (P1, decision)

**As** the owner, **I want** an explicit in/out decision on a TTM period for v1,
**so that** the orchestrator knows whether to expect a trailing row.

**AC**
- Open question Q1 resolved in this spec's changelog: annual-only for v1 (default
  recommendation) **or** annual+TTM with `reporting_basis` distinguishing them.
- If annual-only: `reporting_basis` is always `"annual"` and a `warnings`/doc note
  states TTM is deferred, so the orchestrator does not silently assume a trailing period.

**Priority:** P1. **Deps:** none (decision). **Blocks:** orchestrator trend-block shape lock.

---

## 6. Open Questions

| # | Question | Recommendation | Owner | Needed by |
|---|---|---|---|---|
| Q1 | Annual-only, or annual + a TTM row in v1? | **Annual-only v1** (simplest provenance story; SEC FY facts are cleanest). Reserve `reporting_basis:"ttm"`. | Yonatan (midas) | before FT-1 build (FT-8) |
| Q2 | Is the metric set exactly {revenue, operating_margin, fcf}? | **Yes for v1** — matches parent PRD trio; expand only if narration shows a gap. | Yonatan | before FT-1 build |
| Q3 | Bulk variant (`POST /fundamentals-trend/bulk`) in v1? | **No** — X1 is single-ticker; defer to X2 ranked theses. | Yonatan | non-blocking |
| Q4 | Dedicated `read:fundamentals_trend` permission or reuse `read:fair_value`? | **Reuse `read:fair_value`** for v1 (same consumer, same key) unless RBAC separation is wanted. | Yonatan | before FT-7 |
| Q5 | Period-label format: `"FY24"` vs ISO fiscal-end date? | **`"FY24"` labels + per-point `as_of` date** (human-readable trend axis + precise provenance). | Yonatan | before FT-1 build |

---

## 7. Non-Goals

1. **No new valuation model / no engine math change.** Read-only over existing
   `GetHistorical`; `internal/services/valuation/` untouched.
2. **No new SEC data acquisition.** Surfaces multi-period facts midas already pulls.
3. **No interpolation/back-fill/smoothing.** Gaps are explicit; this is load-bearing.
4. **No bulk / multi-ticker** (Q3) and **no per-share trend** in v1.
5. **No narration.** Prose is the orchestrator's job; midas returns structured
   numbers + provenance only.
6. **No parent/orchestrator edits here.** `TOOL_REGISTRY.md` + `adapters/midas.py`
   are downstream follow-ups in their own repos — explicitly out of this spec.

---

## 8. Acceptance / Definition of Done (capability-level)

- [ ] `GET /api/v1/fundamentals-trend/{ticker}` live behind `X-API-Key`, returning the §4.3 contract.
- [ ] FT-1…FT-5 acceptance criteria pass (happy path, gaps, typed failures, provenance, derivation).
- [ ] Invariants in §4.3 enforced by contract tests (length/order, value⇔missing, provenance-completeness).
- [ ] Reuses existing RFC 7807 error codes; no new error taxonomy.
- [ ] FT-7 doc set updated **within midas** (openapi, CONTRACTS, README, ARCHITECTURE).
- [ ] Q1 (TTM) recorded in the changelog (FT-8).
- [ ] midas's own V/R/Q gates green (the midas harness governs this — run inside `midas/`).

---

## 9. Handoff / Downstream (tracked, NOT done here)

Once this capability ships in midas, the following land as **separate** delegated changes:
1. **Parent `Strade/docs/TOOL_REGISTRY.md`** — add a `fundamentals_trend` capability
   row for midas (endpoint, params, response shape, error modes).
2. **`orchestrator/adapters/midas.py`** — add a `fundamentals_trend(ticker, ...)`
   adapter method mapping this contract into the thesis operation's payload, with
   failure isolation (parent PRD P1 story #10).
3. **Orchestrator thesis operation (X1) Milestone B** — consume the provenanced
   block; its provenance-coverage + narration-faithfulness gates validate against this contract.

---

## 10. Change Log

| Date | Change |
|---|---|
| 2026-06-27 | Initial draft — change request created for the midas repo per parent thesis-operation PRD (X1, Milestone A). Status NOT STARTED. |
