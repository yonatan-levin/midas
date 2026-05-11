# RM-1 — Revenue-multiple model uses quarterly revenue without annualisation

**Status:** OPEN — filed 2026-05-06 during live-API verification of the Graham-floor PR.
**Severity:** Major. Affects every ticker that routes to `revenue_multiple` (i.e., every company with negative or zero operating income whose latest filing is a 10-Q). Default behaviour silently understates fair value by ~4×.
**Origin:** Live MXL response inspection during HUMAN sign-off of the Graham-floor PR (`docs/refactoring/graham-floor-metrics-spec.md`). The user noted that `dcf_value_per_share = $1.32` was lower than `current_assets_per_share = $2.85` despite a 7-stage growth curve averaging 37%/yr. Investigation found the model treats `FinancialData.Revenue` as if it were annual, but for 10-Q filings it is quarterly.
**Blocks:** Nothing — this is a long-standing latent bug, not a regression. The Graham-floor PR did not introduce it.
**Related specs:** `docs/refactoring/graham-floor-metrics-spec.md` (where the bug surfaced), `docs/reviewer/RM-3-forward-revenue-multiple-model.md` (a forward-looking model that would also benefit from a TTM revenue base).

---

## Context

`internal/services/valuation/models/revenue_multiple.go:74-79` reads:

```go
revenue := latest.Revenue
if revenue <= 0 {
    return nil, fmt.Errorf("revenue_multiple: company has no revenue ...")
}
multiple := m.getMultiple(input.Industry)
enterpriseValue := revenue * multiple
```

The variable `latest` is the `*entities.FinancialData` for the most recent reporting period — which can be a quarter (10-Q) or a fiscal year (10-K). For 10-Q filings, `latest.Revenue` is the **single-quarter** revenue. Sector EV/Revenue multiples (e.g. the 1.5× MFG default in `config/industry_multiples.json`) are conventionally calibrated against **annual** revenue.

Concrete MXL example from `artifacts/2026-05-06/MXL/req_c01bec94-9c3c-46f6-afad-9458672c8534/`:

| Field | Value | Note |
|---|---|---|
| `filing_period` | `"2026Q1"` | Single quarter |
| `latest.Revenue` | $137,188,000 | Q1 2026 only |
| MXL true TTM revenue (4 trailing quarters) | ~$560,000,000 | Industry-tracker estimates |
| Multiple applied | 1.5× | (separate problem — see RM-2) |
| EV computed | $137M × 1.5 = $206M | **Wrong base** |
| EV that should have been computed | $560M × 1.5 = $840M | TTM base |
| Per-share equity output | $1.32 | Reported |
| Per-share equity, with TTM base | $8.56 | What the model "should" produce |

The understatement is **~6.5×** for MXL — not exactly 4× because the equity bridge (`EV − Debt + Cash`) compresses the ratio when debt is non-trivial.

For tickers whose latest filing is a 10-K, the bug is silent: `latest.Revenue` is annual and the model produces the intended output. So the bug surfaces only on the most recent 1-3 quarters of the year, and only for revenue-multiple-routed companies (negative OI). Estimated blast radius based on a one-year sample: any negative-OI ticker checked between filing dates of Q1 and FY is affected, which is roughly **75% of negative-OI tickers** at any given moment.

## Why it matters

1. **Silent.** No warning fires. `data_quality_grade` stays at A. The response looks plausible.
2. **Compounds with RM-2.** When a semi gets the 1.5× MFG multiple instead of a 6× sector-correct multiple AND the revenue base is ¼ of TTM, the combined error is ~25× — turning a "this stock is roughly fairly valued" signal into a "this stock is wildly overvalued" signal. The user's MXL screening workflow could give exactly the wrong call.
3. **Inconsistent across same-ticker time series.** If you query MXL three times — once after Q1, once after Q2, once after FY — you get three wildly different "fair values" purely from the revenue base flipping. Trend analysis is impossible.
4. **The DCF path also reads `latest.Revenue` for projection seeding.** Specifically `service.go:`-around-where-FCF-projection-bootstraps. Need to audit whether the DCF path is similarly affected on quarter-only data, though the impact there is smaller because DCF projects forward from the seed.

## Proposed fix (one of)

### Option A — Crude annualisation (minimal, ~5 lines)

```go
revenue := latest.Revenue
if isQuarterlyPeriod(latest.FilingPeriod) {
    revenue *= 4
}
```

Where `isQuarterlyPeriod(s string) bool` matches `/Q[1-4]$/`.

**Pros:** trivial fix; covers 90% of cases; reversible.
**Cons:** ignores seasonality (retail Q4 vs. Q1, semi cyclicality, biotech non-recurring revenue events); a single-quarter blip propagates 4× into the headline number.
**Risk:** medium. Still better than current behaviour but not robust.

> **Damodaran (NYU Stern, 2024 dataset notes):** *"Annualization overstates for ramping firms — prefer actual TTM or pro-rata."* The 4×latest-quarter annualisation is conventional in Bloomberg/FactSet screens but is "frowned upon by purists." For ramping firms (most negative-OI tickers!) it systematically overstates fair value.

### Option B — Sum trailing four quarters (TTM proper)

Add a method to `entities.HistoricalFinancialData`:

```go
// TrailingTwelveMonthsRevenue returns the sum of the four most recent
// non-overlapping quarterly revenue figures, or the latest annual figure
// if the trailing window is incomplete.
//
// Returns:
//   revenue: the TTM (or annual fallback) revenue
//   source:  "TTM_4Q" | "ANNUAL_FY" | "ANNUALIZED_QUARTER" | "INSUFFICIENT_HISTORY"
//   warning: a non-empty diagnostic when the path was less than ideal
func (h *HistoricalFinancialData) TrailingTwelveMonthsRevenue() (revenue float64, source string, warning string)
```

The method tries, in order:
1. **TTM_4Q** — sum the four most recent distinct quarters whose `FilingPeriod` is in `{2025Q1..2025Q4}` style ordering. Verify that the four quarters span exactly one fiscal year (no gaps, no overlap).
2. **ANNUAL_FY** — if the latest period is an FY (10-K), use it directly.
3. **ANNUALIZED_QUARTER** — if neither TTM nor FY is available, multiply the latest quarter by 4 with a warning string `"revenue_base: annualized single-quarter revenue (4× extrapolation, ignores seasonality)"`.
4. **INSUFFICIENT_HISTORY** — return `(0, "INSUFFICIENT_HISTORY", "...")` and let the caller decide whether to fail or substitute.

Then `revenue_multiple.go` (and any other consumer) uses this helper:

```go
revenue, source, warning := input.HistoricalData.TrailingTwelveMonthsRevenue()
if revenue <= 0 {
    return nil, fmt.Errorf("revenue_multiple: insufficient revenue history (%s)", source)
}
if warning != "" {
    warnings = append(warnings, warning)
}
multiple := m.getMultiple(input.Industry)
enterpriseValue := revenue * multiple
```

**Pros:** standard finance convention; handles seasonality correctly; degrades gracefully through documented fallbacks; warning string surfaces the lossy paths to the consumer.
**Cons:** ~50 lines of code + tests; new entity method must be kept in sync with how the parser builds `HistoricalFinancialData`; requires that the SEC parser actually populates four trailing quarters (verified — the existing artifact bundles include 25+ periods per ticker).
**Risk:** low. The fallback chain ensures no regression: if a ticker only has one quarter of data, behaviour matches Option A.

### Option C — Always prefer the most recent annual filing

```go
revenue := mostRecentAnnualFiling(input.HistoricalData).Revenue
if revenue <= 0 {
    return nil, fmt.Errorf("revenue_multiple: no annual revenue available")
}
```

**Pros:** simplest semantically; no time-shifting needed; matches the multiple's calibration.
**Cons:** stale by up to 12 months for fast-growing companies; missing recent acquisitions or divestitures; explicitly rejects partial-year filers (new IPOs).
**Risk:** medium. Misses the entire growth signal that the broader engine works hard to compute elsewhere.

## Recommendation

**Adopt Option B**, with the explicit fallback chain plus a **prior-year-equivalent extrapolation** for the 1-3-quarter case (which industry research treats as more accurate than naive 4× annualisation).

Findings from external research (perplexity-ask sourced from Damodaran NYU Stern publications and Bloomberg/FactSet practice notes, 2024-2026):

1. The **TTM** path (sum the last 4 reported quarters) is the universally-accepted primary behaviour. Bloomberg, FactSet, S&P CapIQ all default to this.
2. **Naive 4×latest-quarter annualisation** is "conventional in Bloomberg/FactSet screens but frowned upon by purists." Damodaran himself recommends *against* it for ramping firms because it overstates revenue.
3. **Better fallback for 1-3 quarters**: extrapolate using the prior-year-equivalent period (e.g., for a company with Q1+Q2+Q3 only, use `Q1 + Q2 + Q3 + last_year.Q4`). This handles seasonality correctly. **A simple 4×latest-quarter is a third-tier fallback only.**
4. The **annual** fallback is correct when TTM is structurally unavailable (annual-only filers).
5. The **insufficient-history error** is the right escape hatch for companies with <1 quarter of data; better to fail loudly than silently produce a wrong number.

Updated fallback chain (replaces the `TTM_4Q → ANNUAL_FY → ANNUALIZED_QUARTER → INSUFFICIENT_HISTORY` chain in Option B above):

```
TTM_PRIOR_BRIDGE   → for partial-year IPOs: current N quarters + prior-year equivalent (4-N) quarters (partial-year shape preserved for replay audit)
TTM_4Q             → sum of 4 most recent contiguous quarters (gold standard)
ANNUAL_FY          → most recent fiscal year filing
ANNUALIZED_QUARTER → naive 4×latest, with explicit "purists frown on this" warning
INSUFFICIENT_HISTORY → fail
```

Adding the `source` and `warning` returns in the helper signature makes the fallback path inspectable downstream — replay tooling, dashboards, and per-ticker audit trails can all distinguish "TTM was used" from "annualised quarter, take with grain of salt." Particularly important: consumers should be able to *filter out* `ANNUALIZED_QUARTER` results when doing serious comparison work.

## Tests required

| # | Scenario | Expected source | Expected warning |
|---|---|---|---|
| T1 | 4 trailing quarters present, contiguous | `TTM_4Q` | none |
| T2 | 4 trailing quarters with one missing → fall back to annual | `ANNUAL_FY` | `"revenue_base: TTM unavailable, used latest FY ($X dated YYYY-MM-DD)"` |
| T3 | Only 1 quarter available, no FY | `ANNUALIZED_QUARTER` | `"revenue_base: annualized single-quarter revenue (4× extrapolation, ignores seasonality)"` |
| T4 | Only 2 quarters available, no FY | `ANNUALIZED_QUARTER` | same warning + note that 2-quarter average is used |
| T5 | All annual periods (no quarters) | `ANNUAL_FY` | none |
| T6 | No revenue data at all | `INSUFFICIENT_HISTORY` | `"revenue_base: insufficient revenue history"` |
| T7 | Stale data: latest quarter > 18 months old | `STALE_TTM` (or whichever path fired) | `"revenue_base: data is N months old"` |
| T8 | MXL fixture (Q1 2026 only): produces ~$549M annualised | `ANNUALIZED_QUARTER` | the warning |
| T9 | AAPL fixture (FY 2025 + Q1 2026): produces TTM = sum of 4 quarters spanning the boundary | `TTM_PRIOR_BRIDGE` | `"revenue_base: partial-year TTM bridged with prior-year quarters (handles seasonality)"` |

Coverage target: 100% on the new helper (per CLAUDE.md ≥90% finance floor).

## Out of scope

- The DCF path's own use of `latest.Revenue` for projection seeding — likely benign because the engine projects forward using growth rates, but worth a follow-up audit. **Track as RM-1.A** if the audit shows correction is needed.
- The Damodaran sector-multiple coverage gap (RM-2). RM-1 corrects the revenue *base*; RM-2 corrects the *multiplier*. Both are needed for the headline number to be right.
- Forward-revenue projection (RM-3). RM-3's forward model would also benefit from TTM as the starting revenue.

## Acceptance for closing this tracker

- [ ] `entities.HistoricalFinancialData.TrailingTwelveMonthsRevenue()` lands with the documented fallback chain.
- [ ] `revenue_multiple.go` consumes the helper instead of `latest.Revenue`.
- [ ] Tests T1–T9 above all pass with ≥90% coverage on the new helper.
- [ ] A live MXL response (filing_period=Q1) shows `dcf_value_per_share` of approximately $8.50 (TTM × current 1.5× multiple, before RM-2 fixes the multiplier).
- [ ] A live AAPL response (filing_period=Q1) is unchanged or only mildly different from pre-fix (because AAPL's revenue is annualised correctly today via its FY filing — the helper should pick the same number).
- [ ] CHANGELOG/CLAUDE.md gotchas updated to note: "revenue_multiple model uses TTM revenue; the source is in the warnings string when the path was lossy."
- [ ] DC-1, RM-2, RM-3 cross-references updated to remove the "RM-1 latent bug" caveat.
