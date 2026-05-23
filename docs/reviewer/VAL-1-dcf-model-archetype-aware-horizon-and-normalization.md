# VAL-1 — DCF model needs archetype-aware horizon, cyclical-base normalization, and explicit terminal handling

**Status:** Phase 1 RESOLVED 2026-05-23. Phases 2-5 OPEN (gated on the unified `AssumptionProfile` work — Tier 2 P2 already lit Phase 2 horizon resolution in code; tracker still treats Phases 2-5 as in-flight until the cyclical-base / exit-multiple / diluted-forward strands all ship).
**Original filing:** 2026-05-06 as part of the cross-model review prompted by RM-1/2/3 findings on revenue_multiple.

---

## Phase 1 SHIPPED

The 5 DCF diagnostic fields are live on `entities.ValuationResult` and `handlers.FairValueResponse`:

| Commit | Date | Scope |
|---|---|---|
| `a19506d` (merged via `877fa76`) | 2026-05-16 | P0b struct ownership + P2 wire-up: field declarations on both response shapes, P2 stamps them in `service.go::performValuation` (lines ~1276-1310), tests in `service_test.go` (`TestService_performValuation_DCFDiagnostics_*`) pin field semantics + length invariants + terminal-dominance warning trigger. |
| `<this commit>` | 2026-05-23 | OpenAPI schema closure: `docs/openapi.yaml` `FairValueResponse` gains the 5 properties with enums, types, examples, and descriptions documenting omission semantics. |

Auto-generated swagger artifacts (`docs/swagger.{json,yaml}`, `docs/docs.go`) remain stale vs. the struct annotations — separate technical debt tracked in `docs/refactoring/spec/swag-version-alignment-spec.md` (they also lack `assumption_profile` and `resolution_trace`). Regenerating them is gated on the swag CLI ↔ library version alignment and is out of scope for this Phase 1 close.

---

**Original framing preserved below for Phases 2-5 reference.**
**Severity:** Medium. DCF is the primary valuation path for profitable tickers (the majority of Midas's traffic). It already works, but it's the "least broken" model — gaps are calibration-quality rather than wrong-by-design. Fixes are mostly additions, not corrections.
**Origin:** During the cross-model review (companion to VAL-2 DDM, VAL-3 FFO, RM-1/2/3 revenue_multiple), `thinkdeep` flagged that DCF's horizon should be archetype/maturity-driven (not flat 5y) and that cyclical tickers need normalized starting metrics. Damodaran (perplexity-cited): explicit horizon "5-10y, longer for growth firms; terminal value typically 60-80% of EV; longer horizon reduces TV reliance." Midas's DCF currently runs a flat horizon and doesn't normalize cyclical bases.
**Blocks:** Nothing — this is calibration improvement, not a regression fix.
**Related specs:** RM-1 (TTM revenue base), RM-2 (sector multiple coverage), RM-3 (revenue multiple forward), VAL-2 (DDM multi-stage), VAL-3 (FFO forward), `docs/refactoring/valuation-engine-upgrade-spec.md`.

---

## Context

The DCF "model" file (`internal/services/valuation/models/dcf_model.go`, 42 lines) is just a marker class. The actual DCF logic lives in `internal/services/valuation/service.go`'s `performValuation` method (~600 lines, lines ~700-1230). Reading the existing implementation:

- **Multi-stage growth curve.** Engine consumes `growth.Estimator`'s 7-stage curve (50/50/50/41/33/25/16% for MXL is a typical shape). Decays from year-1 analyst rate to year-7 terminal rate (3% default).
- **Terminal value.** Computes via Gordon model on year-N FCF. Has guardrails (terminal_growth ≥ 2% below WACC; otherwise the denominator blows up). Does NOT support exit-multiple terminal as an alternative.
- **Discount.** Year-by-year via WACC (`pkg/finance/wacc/`). Standard FCFF DCF.
- **NOPAT fallback.** When D&A or CapEx is unavailable, falls back to NOPAT (NOPAT = OI × (1 − tax_rate)) instead of true FCF (NI + D&A − CapEx − ΔWC). Triggers a warning + 15-point freshness penalty.
- **Horizon.** Hard-coded to ~7 explicit years (the growth estimator's `ProjectedGrowthRates` slice length is 7), regardless of ticker. No archetype awareness.
- **Cyclical normalization.** None. The DCF projects forward from `latest.Revenue` (or `latest.OperatingIncome`-derived FCF) without any "is this a trough?" check. For semis like MXL at trough, even DCF (when applicable) would over-extrapolate the rebound.

## Why it matters

1. **Horizon mismatch with industry practice.** Damodaran recommends 5-10y explicit forecast, with longer horizons for high-growth tech/biotech and cyclicals (perplexity-cited from his NYU 2025 sessions). Today's flat 7y is arguable for mature growth firms but wrong for both ends:
   - Mature compounders (CVX, KO, JNJ): 7y is too long; 3-5y + Gordon is plenty.
   - Hyper-growth (NVDA, ANET in their early phases): 7y is too short; cuts off the convergence-to-stability arc and packs too much into terminal value.
2. **Terminal-value dominance in long-projection cases.** For high-growth tickers, after 7 years of 30%+ growth, terminal value can be 80-90% of total EV. Damodaran's rule of thumb: 60-80% is healthy; >80% means the model is "betting on the terminal year." Midas doesn't expose terminal-as-percent-of-EV today; consumers can't tell when a result is terminal-dominated.
3. **No exit-multiple terminal alternative.** Some valuation problems are better served by a sector-multiple terminal (`year-N revenue × mature-sector EV/Revenue`) rather than Gordon perpetual growth. Damodaran himself uses both, choosing based on whether the ticker has "reached steady state" by year N. Midas only supports Gordon.
4. **Cyclical-base normalization gap.** Same problem flagged in RM-3 §3 for the revenue-multiple path. DCF projects forward from *latest* operating income or FCF — at trough, this is depressed; the projected rebound looks aggressive. A 2-year-average-OI base or "max(latest OI, normalized OI)" would produce more defensible forecasts for cyclicals.
5. **Per-share vs aggregate dilution.** `thinkdeep` raised this for high-SBC growth tickers (NVDA, TSLA, every cloud-SaaS). DCF computes `EquityValue / DilutedShares`. If shares grow 5%/year (typical for SBC-heavy growth tickers), the current diluted share count understates the dilution effect on the year-5 holder. Damodaran adjusts terminal-equity by a forward-diluted share count; Midas doesn't.

## Proposed fix

### Phase 1 — Diagnostics (cheap, high value)

Surface the existing math more transparently. Adds ~5 fields to `ValuationResult`:

| Field | Type | Meaning |
|---|---|---|
| `dcf_horizon_years` | int | Number of explicit projection years (today: always 7) |
| `dcf_terminal_method` | string | `"gordon_growth"` (today) or `"exit_multiple"` (future) |
| `dcf_terminal_pct_of_ev` | float | `terminal_pv / enterprise_value` — flag if >80% |
| `dcf_per_year_pv` | array[float] | PV of each explicit year's FCF (for chart-friendly visualization) |
| `dcf_terminal_growth_used` | float | The clamped terminal growth rate after the WACC-spread guardrail |

Effort: ~2 hours. Pure additions to `entities.ValuationResult` and the response shaper.

### Phase 2 — Archetype-aware horizon

Resolve horizon from the same `AssumptionProfile` that drives RM-3 / VAL-2 / VAL-3:

| Profile | DCF horizon | Notes |
|---|---|---|
| Mature large-scale (CVX, KO, JNJ) | 3y explicit + Gordon terminal | Stable growth assumption is reasonable quickly |
| Standard growth (most profitable mid-caps) | 5y + Gordon | Damodaran's default |
| High-growth profitable (large-cap tech: AAPL, MSFT) | 7y + Gordon (today's behaviour, generalized) | Allow margin convergence over 7y |
| Hyper-growth profitable (NVDA, AMD-trough) | 10y + Gordon or exit-multiple | Capture full convergence; flag terminal dominance if >80% |

Profile resolution is the same machinery that drives the other models — see VAL-2 / VAL-3 / RM-3 for the shared `AssumptionProfile` design.

Effort: ~1 day. Adds ~50 lines to service.go's DCF block, extends growth.Estimator to produce a longer rate slice when horizon ≥ 7.

### Phase 3 — Cyclical-base normalization

When the resolved profile is `(cyclical, *)`:
- Use `max(latest_OI, mean_OI_3y)` as the base, not raw latest_OI.
- Or use mid-cycle OI estimate (industry-specific; out of immediate scope but worth documenting as VAL-1.1 follow-up).
- Add a `dcf_base_normalization` diagnostic field showing which method fired.

Effort: ~1 day. Mirrors the cyclical handling in RM-3.

### Phase 4 — Exit-multiple terminal as an alternative to Gordon

When the profile says `terminal_method: exit_multiple`:
- Compute `year_N_revenue × mature_sector_EV/Revenue` as the terminal EV.
- Compare against Gordon-implied terminal; emit both, primary chosen by profile.
- Useful for early-stage growth tickers where year-N is unlikely to be a Gordon-stable state.

Effort: ~2 days. Adds a new code path; needs careful testing because it interacts with the sanity-check crosscheck.

### Phase 5 — Diluted-share-forward adjustment

For high-SBC tickers:
- Project diluted share count forward at historical SBC dilution rate.
- Use forward-diluted shares for terminal equity-per-share calculation.
- Default off; enable via profile.

Effort: ~1 day. Adds ~30 lines + tests.

## Recommendation

**Phase 1 first, then Phase 2.** Phase 1 is two hours of diagnostic surfacing that makes the existing DCF output much more inspectable — operators and consumers can see when terminal dominates, when growth was clamped, etc. Phases 3-5 are real calibration improvements but are also the most likely to introduce regressions on the high-volume profitable-ticker path; they need careful unit tests and golden-file regression suites.

Phases 2-5 should ship together with the unified `AssumptionProfile` work (cross-cutting with RM-3/VAL-2/VAL-3) — same profile resolution, same archetype/maturity buckets, same diagnostic conventions. Don't ship them piecemeal; the profile design is the architectural backbone.

## Tests required

Phase 1 (diagnostics):
- Snapshot test: `terminal_pct_of_ev` reported and within 0-100% range for AAPL, MSFT, MXL fixtures.
- Regression: existing DCF outputs unchanged for AAPL (Phase 1 is additive).

Phase 2 (horizon):
- Mature ticker (KO): assert `dcf_horizon_years == 3`.
- Standard growth (AAPL): assert `dcf_horizon_years == 5` (or `7` if classified as high-growth).
- Hyper-growth (NVDA): assert `dcf_horizon_years == 10` (if classification supports it).
- Output regression: for AAPL, the existing $X dcf_value_per_share doesn't drift more than ±5% (tolerance for the horizon change).

Phase 3 (cyclical base):
- MXL fixture (semi at trough): when classified as `(cyclical, *)`, base uses 3y mean instead of latest. Per-share value differs from non-normalized run.

Phase 4 (exit multiple):
- Forward NVDA fixture: profile says `terminal_method: exit_multiple` → uses sector multiple at year 10. Assert primary value differs from Gordon-only.

Phase 5 (diluted-forward):
- TSLA fixture (high SBC): forward-diluted shares > current diluted shares; per-share value lower than non-adjusted. Pin the relationship.

Coverage: ≥90% on new code per CLAUDE.md finance-module standard.

## Out of scope

- Mid-cycle revenue/OI estimates per industry (would require sector-specific cycle dating; track as VAL-1.1).
- DDM as a cross-check sanity for DCF on dividend-paying profitable tickers (VAL-2 territory).
- Reverse-DCF (solve for implied growth from current price) — useful for screening but a separate feature.
- Two-stage vs three-stage explicit forecasts — current 1-stage with growth fade is fine for now.

## Acceptance for closing this tracker

### Phase 1 — RESOLVED 2026-05-23
- [x] 5 new diagnostic fields on `ValuationResult` and `FairValueResponse` — landed in commit `a19506d` (merged via `877fa76`, 2026-05-16). Field declarations in `internal/core/entities/valuation.go:141-145` and `internal/api/v1/handlers/fair_value.go:234-238`.
- [x] OpenAPI schema updated — landed in this Phase 1 close commit (`docs/openapi.yaml` `FairValueResponse` gains 5 properties with enums, types, examples, omission-condition descriptions).
- [x] Tests assert fields populate for representative fixtures (AAPL, MSFT, MXL) — `TestService_performValuation_DCFDiagnostics_*` in `internal/services/valuation/service_test.go` (lines ~3504-3705) pins field semantics, length invariants (`len(dcf_per_year_pv) == dcf_horizon_years`), and the >0.80 terminal-dominance warning trigger.
- [x] No existing test regresses — full `go test ./... -count=1 -short` PASS at the Phase 1 close.

### Phase 2-5 (with unified profile work)
- [ ] `AssumptionProfile` machinery shared with RM-3 / VAL-2 / VAL-3.
- [ ] Horizon resolved from profile; per-archetype values sane.
- [ ] Cyclical-base normalization fires when profile is `(cyclical, *)`.
- [ ] Exit-multiple terminal optional and behind the profile.
- [ ] Diluted-share-forward adjustment optional and behind the profile.
- [ ] Comprehensive regression suite across (mature, growth, hyper-growth, cyclical) × (current, profile-driven) — produces a divergence report that humans can review before merging.
- [ ] CHANGELOG/CLAUDE.md updated.
