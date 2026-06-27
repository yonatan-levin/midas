# XV-1 — DCF fair value ≫ market price on energy names, yet graded "A"

- **Raised by:** Strade **orchestrator** (downstream consumer of `/api/v1/fair-value`), during a live cross-tool pipeline run.
- **Date:** 2026-06-27
- **Component:** valuation (multi-stage DCF), `data_quality_grade`, sanity-check wiring, WACC/CAPM inputs.
- **Severity:** HIGH — not a crash, but **wrong-looking numbers presented as high-confidence (grade A)**. For the orchestrator this is the worst failure mode: a figure that *looks* trustworthy (grade A → `actionable: true`) but is implausible.
- **Status:** OPEN — needs midas-side triage. This doc is a consumer report; the fix (if any) belongs in the midas repo/harness.
- **midas version:** `calculation_version: 4.10`, `config_version: 1.1.0`, `config_hash: 023eca27587eb1c786277b322054aee2551a44ae1e68f74708bdfd45aca832e3`.

> **Context / how it surfaced.** Running the orchestrator's `screen_and_value`
> pipeline (algo_beta screens *energy, P/E < 20* → midas values each → rank by
> discount-to-fair-value) produced a watchlist where the top names showed
> discounts of **1,000–5,700%** — i.e. DCF fair values 10–55× the market price —
> all stamped `data_quality_grade: A`. The orchestrator carries midas's numbers
> through unchanged (by design), so this points at midas, not the composition layer.

---

## Summary of findings

| # | Finding | Evidence |
|---|---------|----------|
| F1 | **DCF fair value is an order of magnitude above market price** for energy small/mid-caps | REPX `dcf=365.03` vs `price=33.74` (**10.8×**); NBR `dcf=2400.29` vs `price=82.61` (**29×**) |
| F2 | **`data_quality_grade: A` despite `sanity_check.is_reasonable: false`** | REPX graded A (score 90) while its own sanity check flags implied EV/EBITDA 113 vs sector median 6 |
| F3 | **Implausibly low WACC inflates terminal value** | NBR `wacc=0.0342` (3.4%) with `terminal_dominance: terminal_pv is 90.2% of EV` |
| F4 | **Industry classifier disagrees with SIC** (`match: false`) — may feed wrong sector multiples | REPX `sic: ENERGY` but `heuristic_name: Industrials`, `match: false` |
| F5 | **Large day-over-day swing** (observation, not yet a confirmed bug) | Same REPX: `dcf≈1903.96` on 2026-06-26 → `dcf=365.03` on 2026-06-27 |

The grade is the load-bearing problem: **F2 means the sanity check exists but does
not influence the grade**, so downstream consumers that trust the grade (the
orchestrator gates `actionable` on grade ≥ B) treat implausible numbers as decision-grade.

---

## Evidence — requests & responses (captured live, 2026-06-27)

All calls are direct to midas (`:8080`) with the demo key, so they reproduce
without the orchestrator.

### Case A — REPX (single) `GET /api/v1/fair-value/REPX`

**Request**
```http
GET /api/v1/fair-value/REPX HTTP/1.1
Host: localhost:8080
X-API-Key: dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788
```
```bash
curl -H "X-API-Key: <demo-key>" http://localhost:8080/api/v1/fair-value/REPX
```

**Response — HTTP 200** (verbatim; note `dcf_value_per_share` 10.8× `current_price`, grade A, `sanity_check.is_reasonable: false`)
```json
{
  "ticker": "REPX",
  "wacc": 0.07317658654875549,
  "growth_rate": 0.11925722342814349,
  "growth_source": "analyst_blend",
  "growth_confidence": "medium",
  "tangible_value_per_share": 56.547079400067084,
  "dcf_value_per_share": 365.03112750017885,
  "current_price": 33.74,
  "data_quality_score": 90,
  "data_quality_grade": "A",
  "calculation_method": "multi_stage_dcf",
  "calculation_version": "4.10",
  "warnings": [
    "operating_income_base: source=ANNUAL_FY operating_income=$152955667",
    "operating_income_base: TTM unavailable, used latest FY ($133279000 dated 2026-03-04) [2025FY]",
    "DCF-implied EV/EBITDA (113.0) is >2x sector median (6.0) for ENERGY"
  ],
  "sanity_check": {
    "implied_pe": 0,
    "sector_median_pe": 10,
    "implied_ev_ebitda": 113.0234990604011,
    "sector_median_ev_ebitda": 6,
    "sector_median_pfcf": 12,
    "is_reasonable": false,
    "flags": ["DCF-implied EV/EBITDA (113.0) is >2x sector median (6.0) for ENERGY"]
  },
  "industry": {
    "sic_code": "1311", "sic": "ENERGY",
    "heuristic_code": "20", "heuristic_name": "Industrials", "match": false
  },
  "currency": "USD",
  "assumption_profile": "cyclical_mid_cycle:standard_growth",
  "dcf_horizon_years": 5,
  "dcf_terminal_method": "exit_multiple",
  "dcf_terminal_pct_of_ev": 0.7456013634461963,
  "dcf_terminal_growth_used": 0.03,
  "dcf_base_normalization": "3y_mean",
  "dcf_gordon_terminal_value": 15530742607.596155,
  "dcf_exit_multiple_terminal_value": 1117111857.1383777
}
```
*(cleaning_adjustments and dcf_per_year_pv omitted for brevity; full payload available on request.)*

### Case B — REPX (bulk) `POST /api/v1/fair-value/bulk`

**Request**
```bash
curl -H "X-API-Key: <demo-key>" -H "Content-Type: application/json" \
  -X POST http://localhost:8080/api/v1/fair-value/bulk -d '{"tickers":["REPX","NBR"]}'
```

**Response — HTTP 200** (relevant fields; **bulk agrees with single today** — REPX 365.03, ruling out a single-vs-bulk discrepancy as of this run)
```json
{ "results": [
  { "ticker": "REPX", "dcf_value_per_share": 365.03112750017885, "current_price": 33.74,
    "wacc": 0.07317658654875549, "data_quality_grade": "A", "data_quality_score": 90 },
  { "ticker": "NBR",  "dcf_value_per_share": 2400.29124892502,   "current_price": 82.61,
    "wacc": 0.03422813332578766, "data_quality_grade": "A", "data_quality_score": 90,
    "warnings": ["terminal_dominance: terminal_pv is 90.2% of enterprise_value (>80% threshold ...)"] }
] }
```

### Case C — NBR (single) `GET /api/v1/fair-value/NBR`

**Request**
```bash
curl -H "X-API-Key: <demo-key>" http://localhost:8080/api/v1/fair-value/NBR
```

**Response — HTTP 200** (key fields — DCF 29× price; WACC 3.4%)
```json
{ "ticker": "NBR", "wacc": 0.03422813332578766, "dcf_value_per_share": 2400.29124892502,
  "current_price": 82.61, "data_quality_grade": "A", "data_quality_score": 90 }
```

### Context D — how the orchestrator surfaced it (`POST /operations/screen_and_value`, 2026-06-26)

**Request**
```bash
curl -X POST http://localhost:8088/operations/screen_and_value \
  -H 'Content-Type: application/json' \
  -d '{"filters":{"sec":"energy","fa_pe":"u20"},"limit":5}'
```
**Response — HTTP 207 `partial`** (watchlist excerpt; `discount_pct` is a ratio, so `57.05` = 5,705%)
```json
{ "status":"partial",
  "data": { "watchlist": [
    {"ticker":"REPX","current_price":32.80,"dcf_value_per_share":1903.96,"discount_pct":57.05,"data_quality_grade":"A","actionable":true},
    {"ticker":"NBR","current_price":84.13,"dcf_value_per_share":2396.18,"discount_pct":27.48,"data_quality_grade":"A","actionable":true}
  ] },
  "summary": {"screened":108,"valued":90,"failed":18} }
```

---

## What should be checked (midas-side)

1. **Grade ↔ sanity-check wiring (F2, highest priority).** Decide whether
   `data_quality_grade` should be **capped** when `sanity_check.is_reasonable == false`.
   Today grade reflects *input completeness* (score 90 → A) and ignores *output
   plausibility*. A grade-A figure that the model itself flags as unreasonable is a
   trap for any consumer that trusts the grade. Options: cap grade at (say) C/D when
   `is_reasonable == false`, or expose a separate `plausibility`/`output_grade`.

2. **WACC / CAPM inputs (F3).** NBR `wacc = 3.42%` is below plausible cost of
   capital for an oil-&-gas driller; combined with `terminal_growth = 3%` it drives
   `terminal_dominance = 90.2%`. Check the beta source, risk-free rate, and
   capital-structure weights for NBR (and whether a WACC floor / `WACC − g` minimum
   spread guard should apply before the DCF is accepted).

3. **Operating-income base & normalization (F1).** REPX uses
   `ANNUAL_FY` with `TTM unavailable`, `dcf_base_normalization: 3y_mean`, and a
   ~111% derivative-gains exclusion on a small base. Verify the normalized base
   isn't being scaled/annualized incorrectly for small-cap energy (units, per-share
   divisor, share count). A 10–29× fair-value/price ratio usually signals a
   base-magnitude or share-count error, not just optimistic growth.

4. **Industry classification mismatch (F4).** REPX `sic: ENERGY` but heuristic
   maps to `Industrials` with `match: false`. Confirm which classification feeds the
   **sector-median multiples** used by the sanity check — a wrong sector median
   would both mis-judge plausibility and (if it feeds the model) mis-set multiples.

5. **Day-over-day stability (F5).** REPX moved `dcf 1903.96 → 365.03` in one day.
   Quantify expected volatility from analyst-blend refresh vs. base/normalization
   switches; a 5× swing on a single data refresh is worth a regression check.

## Reproduction

```bash
# midas on :8080 with the seeded demo key (go run ./cmd/migrate -db ./data/midas.db prints it)
KEY=dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788
curl -H "X-API-Key: $KEY" http://localhost:8080/api/v1/fair-value/REPX
curl -H "X-API-Key: $KEY" http://localhost:8080/api/v1/fair-value/NBR
```

## Notes / non-claims

- This is a **consumer observation**, not a midas root-cause. The orchestrator
  passes midas figures through unchanged, so the numbers above are midas output.
- F5 is flagged as an **observation**, not a confirmed defect (could be legitimate
  data refresh).
- The single-vs-bulk path produced **identical** REPX values today (Case A vs B),
  so an earlier suspicion of single/bulk divergence is **not** substantiated.
