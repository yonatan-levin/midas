# VAL-2 — DDM is single-stage Gordon; should be multi-stage for non-mature dividend payers, and discount discipline (cost of equity not WACC) needs explicit guards

**Status:** OPEN — filed 2026-05-06 as part of the cross-model review.
**Severity:** Medium-High. Today's `ddm.go` is single-stage Gordon (`Value = DPS × (1+g) / (CoE − g)`) routed for ALL FIN-prefix tickers (banks, insurers, mature dividend tech if it ever matched). Damodaran's recommendation for *non-mature* dividend payers (most banks, AAPL, MSFT, V, MA, etc.) is **multi-stage DDM**: explicit dividend forecast for years 1-5 (or 5-10) with rising-payout assumption, then Gordon stable at year-N+1. The single-stage version is the right model only for fully mature firms (utilities, large mature banks at steady state).
**Origin:** Cross-model review prompted by RM-3's findings. `thinkdeep` second-round review noted: *"DDM should use cost of equity, not WACC. Single-stage Gordon is appropriate for utilities and large-cap mature banks; multi-stage for everything else."* Damodaran (perplexity-cited from his NYU 2025 sessions): *"Two-stage DDM for maturing tech like AAPL/MSFT: high-growth phase (5-10y explicit dividends from EPS forecasts, payout rising), then stable Gordon (g=3-4%, payout=50-60%)."*
**Blocks:** Nothing. DDM works correctly for utilities and stable banks today. The gap is in coverage, not correctness.
**Related specs:** RM-3 (revenue multiple unified profile-driven design), VAL-1 (DCF), VAL-3 (FFO), `internal/services/valuation/models/ddm.go`.

---

## Context

Reading `ddm.go` (256 lines):

- **Algorithm.** Single-stage Gordon: `Value = DPS × (1+g) / (CoE − g)`. One growth rate, perpetuity.
- **Growth estimation** (`estimateDividendGrowth`, lines 196-256). Priority chain: historical DPS CAGR (capped at 15% / floor -5%) → sustainable growth (ROE × retention ratio, capped at 15%) → growth.TerminalGrowthRate → 0.03 default.
- **Numerical stability.** Two guards: (a) if `g >= CoE`, cap g at 70% of CoE with WARN log; (b) if `CoE − g <= 0.005`, force denominator to 0.005.
- **P/BV cross-check** (lines 130-167). Compares implied P/BV against ROE-justified P/BV `(ROE − g) / (CoE − g)`. Flags >2× or <0.5× divergence as a warning.
- **Discount rate.** Uses `input.CostOfEquity` correctly. ✓ (this is the one thing the current implementation gets right that the other non-DCF models get wrong).
- **Data flow**. `input.GrowthEstimate.ProjectedGrowthRates` is **never used**. The full 7-stage growth curve from the engine is computed but ignored — DDM derives its own dividend growth from historical DPS CAGR.

## Why it matters

1. **Single-stage Gordon understates value for non-mature dividend payers.** AAPL/MSFT have rising payout ratios (still in the 25-35% range, growing toward 50-60% over a decade). Single-stage Gordon with current DPS × current implied g undervalues them because it ignores the payout ratio's expansion path. Damodaran's multi-stage version captures this: years 1-5 explicit dividend forecast with payout rising from 25% to 50%, then Gordon stable. Real-world impact for AAPL: depending on assumptions, multi-stage produces a fair value 20-40% higher than single-stage.
2. **All FIN-prefix tickers get the same treatment.** Mature large banks (JPM, BAC) and growth banks (small-cap regional banks, fintech-adjacent like SCHW) get the same single-stage Gordon, just with different historical CAGRs as inputs. Damodaran differentiates: large mature → single-stage acceptable; small/growth → multi-stage required.
3. **Historical DPS CAGR cap of 15% is reasonable but flat.** A small-cap regional bank growing dividends at 20%/year for 3 years gets capped to 15% — fine for stability, but the cap should be archetype-aware (mature large bank: 8% cap; growth bank: 15% cap; maturing-tech-first-dividend: 25% cap during the explicit phase).
4. **Growth-curve unused.** `input.GrowthEstimate.ProjectedGrowthRates` is computed by the engine and never consumed by DDM. The fade pattern (50% → 16% over 7 years) is exactly the right shape for a multi-stage explicit forecast — but DDM ignores it and re-derives a single number from historical DPS.
5. **Maturing-tech routing edge.** AAPL/MSFT have FIN-prefix-style dividend behaviour (recurring, growing) but are classified as `TECH` by the SIC classifier. They never route to DDM today. DCF is the primary model. Whether they *should* run DDM as a cross-check is a routing question (companion to VAL-1's "triangulate 2-3 models" recommendation).

## Proposed fix

### Phase 1 — Multi-stage DDM (the structural upgrade)

Add an explicit-forecast phase to `ddm.go`. Replace the single-stage Gordon body (lines 89-95) with:

```go
// Phase 1: Explicit dividend forecast for `horizon` years using
// engine's growth curve (or a derived dividend-growth slice).
horizon := profile.DividendForecastHorizon  // typically 5
explicitPV := 0.0
projectedDPS := dps
discount := 1.0
for i := 0; i < horizon; i++ {
    g := dividendGrowthRates[i]  // from profile + GrowthEstimate
    projectedDPS *= 1 + g
    discount *= 1 + costOfEquity
    explicitPV += projectedDPS / discount
}

// Phase 2: Gordon stable terminal.
terminalGrowth := profile.StableDividendGrowth  // 3-4% per Damodaran
denominator := costOfEquity - terminalGrowth
if denominator <= ddmDenominatorEpsilon {
    denominator = ddmDenominatorEpsilon
}
terminalDPS := projectedDPS * (1 + terminalGrowth)
terminalValue := terminalDPS / denominator
terminalPV := terminalValue / discount  // discount is (1+CoE)^horizon

valuePerShare := explicitPV + terminalPV
```

Two-stage explicit + Gordon. Standard practice.

**Profile-driven horizon** (mirrors RM-3 / VAL-1):

| Profile | Explicit horizon | Stable g | Terminal payout |
|---|---|---|---|
| Mature large bank (JPM, BAC) | 0 (single-stage Gordon, current behaviour) | 3% | 30-40% |
| Growth bank (small-cap regional) | 5y | 3.5% | 35-45% |
| Insurance company | 0 or 5y | 3% | 30-50% |
| Maturing tech first dividend (V, MA early-2010s) | 7-10y | 3.5% | 50-60% (rising payout!) |
| Mature dividend tech (AAPL today, MSFT) | 5y | 3% | 50-55% |

The single-stage Gordon (`horizon == 0`) is preserved for mature large banks where it's correct; multi-stage extends the model to non-mature payers.

### Phase 2 — Use the engine's growth curve

Today DDM computes its own `dividendGrowth` from historical DPS CAGR. The engine already has a 7-stage growth curve. Two options:

A. **Trust DDM's local estimation, ignore engine curve.** Current behaviour. Defensible because dividend growth ≠ revenue/earnings growth — payout ratio changes break the link. But conflicts with the cross-model unification goal.

B. **Use engine growth as a secondary signal.** Compute dividend growth as `engine_growth × profile.PayoutGrowthRatio`, where `PayoutGrowthRatio` is roughly 0.6-1.0 depending on archetype (mature firms: dividends grow ≈ earnings; growth firms with rising payout: dividends grow faster than earnings).

C. **Per-year explicit dividend forecast.** Use `growth.ProjectedGrowthRates` as earnings-growth, then apply a per-year payout-ratio path (`profile.PayoutPath: [0.30, 0.35, 0.40, 0.45, 0.50]`) to derive the per-year dividend forecast. Most flexible; matches Damodaran's full multi-stage method.

Recommended: **Option C** for the multi-stage path; keep current local-estimation as the single-stage fallback.

### Phase 3 — Profile-keyed growth caps

Replace the flat 15% historical-CAGR cap (line 217) with profile-keyed caps:

| Profile | DPS-CAGR cap |
|---|---|
| Mature large bank | 8% |
| Growth bank | 15% (current cap) |
| Insurance | 10% |
| Maturing tech first dividend | 25% (during explicit phase only) |
| Mature dividend tech | 12% |

Effort: small config table + 5-line code change.

### Phase 4 — Triangulation routing (optional)

Emit DDM as a *cross-check* alongside DCF for any ticker that pays dividends, regardless of industry. The router still picks one as primary, but the response includes a `cross_checks: { ddm: { value: X, confidence: Y } }` block. Lets consumers triangulate.

This is a richer architectural change and depends on the router redesign (cross-cutting with VAL-1, RM-3). Out of scope for an immediate fix; track as VAL-2.4.

## Recommendation

**Phase 1 + Phase 3 first** (multi-stage upgrade with profile-keyed caps). These together produce the right value for AAPL/MSFT-style maturing dividend payers, are testable in isolation, and don't depend on the broader unified-profile work.

**Phase 2 Option C** ships alongside the unified `AssumptionProfile` work (with RM-3, VAL-1, VAL-3). It requires the per-year payout-path machinery, which is profile-table data.

**Phase 4** is a nice-to-have. Defer.

## Tests required

Phase 1 (multi-stage):
- JPM fixture: profile = mature large bank, horizon = 0 (single-stage Gordon kept). Result unchanged from current behaviour.
- AAPL hypothetical fixture (if it routed to DDM): profile = mature dividend tech, horizon = 5y, rising payout. Result 20-40% higher than current single-stage would produce.
- Test the boundary: horizon = 0 must reproduce today's exact output bit-for-bit. Otherwise we've introduced a regression.

Phase 3 (caps):
- Growth bank with historical DPS CAGR of 18%: capped at 15% (current behaviour).
- Mature large bank with historical DPS CAGR of 12%: capped at 8% (new behaviour).
- Maturing tech with historical DPS CAGR of 28%: capped at 25%.

Cross-cutting:
- P/BV cross-check still fires for both single-stage and multi-stage. The implied P/BV calculation needs to use the multi-stage value, not just the Gordon component.
- ROE sanity warnings unchanged.
- Confidence rating logic unchanged.

Coverage: ≥90% on `ddm.go` per CLAUDE.md finance-module standard.

## Out of scope

- H-Model (Damodaran's smooth-fade variant of two-stage). Unnecessary complexity for a personal-investing tool.
- Three-stage explicit (transition phase between high-growth and Gordon). Most pre-IPO and cross-cycle cases are well-served by two-stage.
- Stochastic / Monte Carlo dividend forecasts. Out of scope.
- Alternative payout proxies (buybacks-as-dividends per Damodaran's "augmented dividends"). Worth tracking as VAL-2.5 — bottom-line: companies like AAPL return more value via buybacks than dividends, and a strict DDM understates them.

## Acceptance for closing this tracker

- [ ] `AssumptionProfile` (or DDM-local profile if shipped before unified work) defines per-archetype `(DividendForecastHorizon, StableDividendGrowth, TerminalPayout, DPSGrowthCap)`.
- [ ] Multi-stage code path lands; single-stage preserved as `horizon = 0` special case.
- [ ] Profile-keyed growth caps replace the flat 15% / -5%.
- [ ] At least 4 fixture archetypes tested: mature bank, growth bank, maturing-tech-first-dividend, mature-dividend-tech.
- [ ] Confidence and P/BV cross-check carried over correctly to multi-stage results.
- [ ] CHANGELOG/CLAUDE.md updated with the new model behaviour.
- [ ] Existing FIN-prefix tickers' DDM outputs don't regress unexpectedly (mature large banks land on `horizon = 0` which preserves bit-for-bit).
