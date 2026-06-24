# VAL-1 — DCF model needs archetype-aware horizon, cyclical-base normalization, and explicit terminal handling

**Status:** Phase 1 RESOLVED 2026-05-23. Phase 2 SHIPPED 2026-06-22 (archetype horizon). Phase 3 SHIPPED 2026-06-23 (cyclical-base normalization). Phase 4 SHIPPED 2026-06-24 (profile-driven exit-multiple terminal, EV/EBITDA basis, 50/50 blend preserved). Phase 5 SHIPPED 2026-06-24 (diluted-share-forward adjustment, DEFAULT-OFF + profile-gated; no profile enables it yet — ships a dormant capability). CalculationVersion bump deferred to a single end-of-branch bump covering the whole VAL-1 arc (Phase 5 is default-off ⇒ changes no production value, so the bump is deferred to the first config commit that flips the flag on a shipping profile).
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
**Related specs:** RM-1 (TTM revenue base), RM-2 (sector multiple coverage), RM-3 (revenue multiple forward), VAL-2 (DDM multi-stage), VAL-3 (FFO forward), `docs/refactoring/spec/valuation-engine-upgrade-spec.md`.

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

### Phase 3 — Cyclical-base normalization — SHIPPED 2026-06-23

When the resolved profile is `(cyclical, *)` (`cyclical_mid_cycle` | `cyclical_trough`):
- Uses `max(latest/TTM OI, mean_OI_3y)` as the DCF base, not raw latest_OI. The
  3-year mean is computed over FY-only periods via `GetRecentYears(3)` (clock-free).
  The normalization is applied to the `baseOI` scalar at the single seam after the
  BUG-015 TTM rebase and before the `<=0` guard, so it flows into BOTH the legacy
  proportional DCF (`dcfInputs.BaseOperatingIncome`) and the Layer-A reinvestment
  margin seed.
- The `dcf_base_normalization` diagnostic field shows which method fired
  (`3y_mean` when the floor bound; `latest` otherwise). Omitempty + present ONLY on
  the cyclical DCF path → non-cyclical and DDM/FFO/revenue_multiple responses are
  byte-identical.
- Mid-cycle OI estimate (industry-specific) remains OUT OF SCOPE — documented as the
  VAL-1.1 follow-up. The existing `RevenueBaseMethod`/`BaseMarginMethod` enums already
  carry `two_year_average`/`mid_cycle` values for that richer normalization; Phase 3
  deliberately uses the simple `max(latest, 3y_mean)` rule.

Files: `internal/services/valuation/cyclical.go` (pure helper),
`profile.IsCyclicalArchetype()` predicate, `service.go` seam + diagnostic stamp,
`entities.ValuationResult.DCFBaseNormalization` + `handlers.FairValueResponse`
DTO + `replay/diff.go` field-count (46→47). CalculationVersion deliberately NOT
bumped here — a single end-of-branch bump covers the whole VAL-1 arc.

Effort: ~1 day. Mirrors the cyclical handling in RM-3.

### Phase 4 — Exit-multiple terminal as an alternative to Gordon — SHIPPED 2026-06-24

When the profile says `terminal_method: exit_multiple`:
- The engine BLENDS the exit-multiple terminal value 50/50 with the Gordon
  terminal value (preserved request-override semantics — `exit_multiple` is NOT
  a pure exit-multiple terminal, and `gordon_growth` suppresses the blend).
- Both raw terminal estimates are emitted as diagnostics:
  `dcf_gordon_terminal_value` + `dcf_exit_multiple_terminal_value` (nominal,
  pre-discount, pre-blend). The blended primary remains `enterprise_value` /
  `dcf_value_per_share`; `dcf_terminal_method` names the driving method.
- Useful for early-stage growth tickers where year-N is unlikely to be a Gordon-stable state.

**BASIS DECISION (supersedes the original "EV/Revenue" wording):** the engine,
the 17 shipped profile `terminal_multiple` values (3.0–25.0), and the
sanity-check crosscheck all speak **EV/EBITDA** (terminal EBITDA × multiple,
where terminal EBITDA = terminal operating income + scaled D&A). The original
spec phrase `year_N_revenue × mature_sector_EV/Revenue` is **superseded** — Phase
4 keeps the engine's existing, contract-stable EV/EBITDA definition (ARCH Option
A: zero engine-math change). Switching to EV/Revenue would have required
re-authoring all 17 profile multiples and changing `ExitMultipleTV` semantics for
the shipped request-override path — explicitly out of scope.

**Implementation (Option A — activate latent behavior):** the engine already had
the exit-multiple math + the 50/50 blend, and `params` already plumbed
`terminal_method`/`terminal_multiple` through config→profile→request precedence.
The only change was the SERVICE-layer gate: it now switches on the resolved
terminal-method PROVENANCE (`params.EffectiveValuationParams.TerminalMethodSource()`,
returning `SourceDefault|SourceProfile|SourceRequest`) so a PROFILE-sourced
`exit_multiple` drives the blend with the profile's `terminal_multiple`. The
request-override branch is preserved verbatim. **Byte-identity:** a profile- or
default-sourced `gordon_growth` ticker keeps the legacy industry-EV/EBITDA blend
(today's gate only fired on a request override, so profiled-gordon tickers —
banks, mature-large — already get the industry blend; suppressing them would have
silently zeroed it). CalculationVersion bump deferred to the single end-of-branch
bump covering the whole VAL-1 arc.

Plan: `docs/reviewer/implementations/VAL-1-phase-4-exit-multiple-terminal-implementation-plan.md`.

### Phase 5 — Diluted-share-forward adjustment — SHIPPED 2026-06-24

For high-SBC tickers:
- Project diluted share count forward at the historical dilution rate.
- Use the forward-diluted count as the DCF per-share denominator.
- Default off; enable via profile.

**As shipped:**
- Two profile fields (`AssumptionProfile` + `ResolvedProfile`): `DilutedShareForwardEnabled bool` (default-off gate) and `MaxAnnualDilutionRate float64` (clamp ceiling; 0 ⇒ 8% code default). `validateProfile` range-checks the ceiling ∈ [0,1]. **No shipping profile sets the flag in `config/assumption_profiles.json` this phase — a dormant capability.**
- **Derivation method = share-count CAGR across FY periods** (`deriveAnnualDilutionRate` in `internal/services/valuation/diluted_forward.go`): `rate = (sharesₙ/shares₀)^(1/years) − 1` over annual diluted-share counts, NOT an SBC-expense/price model (would need a price series + reintroduce noise). `StockBasedCompensation` is the ELIGIBILITY GATE (a non-trivial SBC issuer), not the rate source. Pure + clock-free (replay-deterministic). Ineligible (no-op) on <2 FY periods, no SBC, or rate ≤ 0 (flat/buyback decline never inflates value).
- **Seam = DCF path only.** `service.go::performValuation` computes a LOCAL `denomShares` (= forward-diluted count when the adjustment fires, else `sharesOutstanding`) immediately before `dcfValuePerShare := equityValue / denomShares`. `sharesOutstanding` is left UNMUTATED — Graham, the sanity cross-check, and the already-computed tangible value all keep reading today's count. The alt-model path (`performAlternativeValuation`) is untouched.
- **Diagnostics:** `dcf_forward_diluted_shares` (number) + `dcf_applied_dilution_rate` (number, decimal), both omitempty on `entities.ValuationResult` + `handlers.FairValueResponse`, registered in `replay/diff.go` (`countFairValueFields()` 49 → 51).
- **Default-off byte-identity (3 layers):** (a) the flag defaults false; (b) on the no-op path `denomShares == sharesOutstanding`; (c) the two diagnostic fields stay zero → omitempty drops them. Pinned by `TestService_performValuation_DilutedForward_FlagOff_ByteIdentical` (`math.Float64bits` on `dcf_value_per_share`) + the unit-level `TestApplyDilutedShareForward_NoOp`.
- **CalcVersion:** Phase 5 itself is default-off (changes no production value), so the bump was deferred to the VAL-1 end-of-branch reconciliation step. That step bumps the engine ONCE for the whole VAL-1 arc, `"4.8" → "4.9"`, because Phases 2-4 (archetype horizon / cyclical-base normalization / profile-driven exit-multiple) DO change DCF output for opted-in shipping profiles. Phase 5 remains default-off under 4.9.

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

Phase 4 (exit multiple) — SHIPPED:
- `TestService_DCF_ProfileExitMultiple_BlendsProfileMultiple`: profile says
  `terminal_method: exit_multiple` → blends the PROFILE `terminal_multiple`
  (engineered ≠ industry default); asserts a non-zero exit component and that EV
  differs from the industry-blend counterfactual.
- `TestService_DCF_ProfileGordonGrowth_KeepsIndustryBlend` +
  `TestService_DCF_NoIndustryMultiple_PureGordon` +
  `TestService_DCF_NoProfileVsGordonProfile_SameTerminal`: byte-identity pins —
  profile/default `gordon_growth` keeps the legacy industry blend, pure-Gordon
  when no industry multiple.
- `TestService_Crosscheck_ExitMultipleProfile_NoSpuriousEVEBITDAFlag`: the
  blended terminal must not raise a spurious EV/EBITDA divergence flag (benign
  circularity, documented in `crosscheck.go`).
- `TestTerminalMethodSource` (params): provenance accessor unit pins.
- `TestRealConfig_ExitMultipleProfilesHavePositiveMultiple`: every shipped
  `exit_multiple` profile carries `terminal_multiple > 0` (prevents the
  resolvable-multiple 422).

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

### Phase 2 — SHIPPED 2026-06-22 (archetype-aware DCF horizon, production wiring)

The horizon resolution + diagnostics were already wired by Tier-2 P2 / the
request-valuation-overrides (T4) work; the remaining production gap was the
shared growth estimator's slice length. `growth.DefaultEstimatorConfig()` ships
`Stage3Years = 0` → only 7 per-year growth rates, and `params.ResolveInputs`
silently clamps any profile requesting `horizon_years: 10` down to 7. So Phase 2
was correct for 3y/5y/7y but broke hyper-growth (10y) in production.

Phase 2 closes that gap (decisions D1–D3):
- **D1:** `NewService` derives the shared estimator's `Stage3Years` from
  `Registry.MaxHorizonYears()` (new accessor) so the slice carries enough rates
  for the largest profile horizon (shipped config = 10 → `Stage3 = max(0, 10−7) = 3`),
  capped at `params.MaxDCFProjectionYears` (D3). Nil registry → `Stage3 = 0`
  (legacy 7-rate slice). Seams: `service.go::NewService`,
  `profile.Registry.MaxHorizonYears`.
- **D2 (byte-identity):** lengthening the shared slice raises `growthRateLen` for
  EVERY ticker, which would push the no-profile/default horizon 7 → 10. Neutralized
  via `params.Defaults.LegacyDefaultHorizonYears` (= legacy 7, computed in
  `performValuation` as `growthRateLen − estimatorInjectedStage3` when the request
  did not override `stage3_years`). The resolver uses it on the default-sourced
  branch only; profile/request horizons win via the precedence chain and are
  validated against the real (longer) `growthRateLen`. Pinned by
  `TestService_DCF_DefaultPath_ByteIdentity` (bit-for-bit vs a 7-rate reference).
- Gordon terminal only; no DDM/FFO/revenue_multiple change; **no new response
  field** (the 5 diagnostic fields already exist) so the replay field-count guard
  is not triggered. `CalculationVersion` is `4.9` as of the VAL-1 end-of-branch
  reconciliation, which bumps the engine ONCE (`4.8 → 4.9`) for the whole VAL-1 arc
  (Phases 2-5); the per-phase commits deferred the bump to that step.

Tests: `TestService_DCF_ArchetypeHorizonGrid_ProductionWiring` (3/5/7/10 grid via
production `NewService`), `TestNewService_DeriveStage3FromRegistryMaxHorizon`,
`TestService_DCF_DefaultPath_ByteIdentity`, `TestService_DCF_HighGrowth7y_NoDriftFromLongerSlice`,
`TestMaxHorizonYears_*`, `TestResolveInputs_LegacyDefaultHorizon_*`. The stale
"flat 7y horizon" framing above (§"Context" :37, §"Why it matters" :42) predates
the T4/Tier-2-P2 mechanics; horizon has resolved from the profile in code since
then — Phase 2 only made the 10y end work in production.

### Phase 2-5 (with unified profile work)
- [x] `AssumptionProfile` machinery shared with RM-3 / VAL-2 / VAL-3.
- [x] Horizon resolved from profile; per-archetype values sane. (Phase 2 SHIPPED 2026-06-22 — production estimator-length wiring + `LegacyDefaultHorizonYears` byte-identity preservation.)
- [x] Cyclical-base normalization fires when profile is `(cyclical, *)`. (Phase 3 SHIPPED 2026-06-23 — `max(latest/TTM OI, 3y FY mean OI)` floor + `dcf_base_normalization` diagnostic; byte-identical for non-cyclical.)
- [x] Exit-multiple terminal optional and behind the profile. (Phase 4 SHIPPED 2026-06-24 — profile-driven via terminal-method provenance; EV/EBITDA basis; 50/50 Gordon blend preserved; both terminal estimates emitted.)
- [x] Diluted-share-forward adjustment optional and behind the profile. (Phase 5 SHIPPED 2026-06-24 — `DilutedShareForwardEnabled` default-off; derivation = FY share-count CAGR; diagnostics `dcf_forward_diluted_shares` + `dcf_applied_dilution_rate`; CalcVersion bump deferred to the first config flip.)
- [ ] Comprehensive regression suite across (mature, growth, hyper-growth, cyclical) × (current, profile-driven) — produces a divergence report that humans can review before merging.
- [ ] CHANGELOG/CLAUDE.md updated.
