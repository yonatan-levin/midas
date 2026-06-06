# Closeout — DCF Reinvestment / Operating-Leverage Model (Layer A, Phase 1)

**Date:** 2026-06-06
**Status:** SHIPPED on branch `feat/dcf-reinvestment-layer-a` (commits `c21e1e6` + `94b529f`).
**Spec:** `docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md` §5–§7, §11, §12.
**Engine version:** `CalculationVersion 4.6 → 4.7`. Config: `assumption_profiles.json 1.0.0 → 1.1.0`.

---

## 1. What shipped

Layer A replaces the proportional `× growthFactor` scaling of CapEx/ΔWC/D&A in the
DCF projection — which algebraically sign-locks projected FCF to the base year
(`FCF_t = growthFactor_t × FCF_base`, the AMD failure signature) — with a **unified
Damodaran sales-to-capital reinvestment term** plus a **per-archetype
margin-convergence path**, so projected FCF can cross negative→positive *within* the
explicit horizon for reinvestment-heavy, scaling firms.

```
Revenue_t       = Revenue_{t-1} × (1 + g_t)
OperatingMargin_t = base + (target − base) × min(t/MarginConvergenceYears, 1)
NOPAT_t         = Revenue_t × OperatingMargin_t × (1 − tax)
Reinvestment_t  = ΔRevenue_t / SalesToCapital_t        (S2C rising start→target)
FCF_t           = NOPAT_t − Reinvestment_t             (floored at MaintenanceCapexFloor × Revenue_t)
```

Terminal value uses §7.3 consistency: terminal reinvestment = `Revenue_{n+1} × g / SalesToCapitalTarget`,
which makes the terminal reinvestment rate equal `g / ROIC` exactly (ROIC =
after-tax target margin × sales-to-capital).

### Touch-points
| File | Change |
|---|---|
| `pkg/finance/dcf/dcf.go` | New `ReinvestmentMethod` projection branch (`sales_to_capital` + `declining_capex_intensity`); §7.3 terminal consistency; §7.1 maintenance floor (explicit window only); `GordonTV` exposed on `Result`. Legacy path byte-identical (gated by `useReinvestmentModel`). |
| `pkg/finance/dcf/reinvestment_test.go` | 8 golden tests (cross-positive, capex-intensity decline, terminal `g/ROIC`, floor clamp, declining-capex fallback, legacy bit-for-bit, no-revenue fallback). |
| `internal/services/valuation/reinvestment.go` | `applyReinvestmentModel` — derives params from the resolved profile + TTM revenue base; `max(base,target)` margin guard; audit source tag + `reinvestment_model` calc-trace stage. |
| `internal/services/valuation/service.go` | Wires `applyReinvestmentModel` into the DCF path; reads `dcfResult.GordonTV` in the terminal calc-trace; `CalculationVersion 4.7` at both stamp sites. |
| `internal/services/valuation/profile/{profile,validation}.go` | 11 additive `AssumptionProfile` fields + 2 enums + method-aware validation invariants (empty method == legacy; floor < target_margin; target ≥ start > 0). |
| `config/assumption_profiles.json` | `sales_to_capital` params on 7 growth profiles; `config_version 1.1.0`. |

### Profiles opted in (§6.2)
`cyclical_mid_cycle:high_growth` (AMD/NVDA), `hypergrowth_early:high_growth`,
`hypergrowth_profitable:high_growth`, `software_like_scaling:high_growth`,
`software_like_large_scale:{mature,standard_growth,high_growth}`. **All other
profiles keep the empty (legacy_proportional) method → bit-for-bit unchanged.** The
wildcard fallback (`software_like_scaling:standard_growth`) was deliberately left
legacy to bound the blast radius for unknown industries.

---

## 2. Open questions resolved (spec §12)

- **§12.1 (primary trajectory):** sales-to-capital primary; declining-capex-intensity
  shipped as a working, unit-tested fallback method (not assigned to any profile yet).
  Runtime falls back to legacy when no revenue base is available.
- **§12.2 (surgical vs FCFF refactor):** surgical patch — new branch gated by
  `useReinvestmentModel`, legacy path untouched.
- **§12.3 (calibration):** coarse Damodaran-style sector norms per archetype, applied
  only to growth archetypes with a genuine operating-leverage story.
- **§12.5 (version):** `CalculationVersion = 4.7`.
- **§12.6 (terminal band):** golden tests assert *shape* (FCF crossing, capex-intensity
  declining, RR = g/ROIC), not a fixed terminal-share threshold (§7.4 keeps
  terminal-dominance a diagnostic).

---

## 3. Validation — `cmd/accuracy` 4.6 → 4.7 (10-ticker basket)

| Metric | 4.6 (`2026-06-05`) | 4.7 (`2026-06-06`) |
|---|---|---|
| Mean absolute price gap | 59.1% | **53.2%** |
| AMD intrinsic | +6.97 | **+112.57** |
| AMD per-year PV | all negative `[-2.05B … -3.14B]` | **`[-0.9B, +1.0B, +3.3B, +6.6B, +10.1B]`** (crosses at yr 2) |
| AMD `NEG_FCF_YEARS` | 5 | **1** |
| AMD terminal share | 247% | **89%** |
| NVDA gap | −33.1% | **−17.4%** |
| AAPL gap | −52.4% | **−38.6%** |
| MSFT gap | −21.9% | **−17.1%** |
| EXTREME_GAP count | 7 | **6** |

**Bit-for-bit preserved (live, full precision):** JPM/DDM `324.61046169914175`,
EQIX/FFO `300.18130805149553`, PLD/FFO `40.26202508247516`, MXL/revenue_multiple
`33.67277241851704` — all identical between 4.6 and 4.7 captures. (The transient
EQIX `379.16` seen in the first 4.7 capture was Yahoo/SEC input drift on that fetch;
the re-capture matches 4.6 exactly.) `recompute-shadow` snapshots byte-identical;
`TestDDM_LegacyPath_BitForBit` green; full `go test ./...` green.

Live log validation (AMD `17-response.json`): the `reinvestment_model:
method=sales_to_capital base_margin=10.7% target_margin=30.0%
sales_to_capital=1.50→2.50 fade=5y` audit tag is emitted; `terminal_dominance`
stays a soft warning (§7.4).

---

## 4. B-V-R-Q review resolution

Full `/execute` B-V-R-Q with independent subagents: **VERIFIER VERIFIED**, **QA PASS**,
**REVIEWER APPROVE_WITH_NITS** (1 HIGH, 1 MEDIUM, 1 LOW, 1 NIT). All addressed in
`94b529f`:

- **HIGH-1** — terminal maintenance-floor clamp was silent and broke the §7.3 `g/ROIC`
  identity. Resolved by REMOVING the floor from the terminal entirely (§7.1 is an
  explicit-window guardrail; the net-reinvestment frame already nets maintenance), not
  by adding a warning. Terminal reinvestment is now pure `g/ROIC`.
- **MEDIUM-2** — added cross-field invariant `MaintenanceCapexFloor < TargetOperatingMargin`.
- **LOW-1 / NIT** — terminal snap-to-matured comment; `strings.Contains` over a local helper.

---

## 5. Load-bearing invariants (GREEN at every commit)

`TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC); `TestCalculateDCF_LegacyProportional_BitForBit`
(new — Layer-A fields populated yet byte-identical output); recompute-shadow
byte-identity; `TestRealConfig_LoadsAndValidates` (incl. new Layer-A assertions);
full `go test ./... -count=1` EXIT=0 (47 packages).

---

## 6. Not in scope / deferred

- **Layer B (Filing Intelligence) + Layer X (assumption-authority hierarchy)** — spec
  Phases 2–4. Untouched.
- **declining_capex_intensity** is implemented + unit-tested but assigned to no profile
  (sales_to_capital covers all calibrated archetypes).
- **Calibration is coarse and first-pass.** The sales-to-capital / target-margin values
  are sector-norm estimates, not back-fit; a future calibration pass against a larger
  basket could refine them. The DCF intrinsics remain conservative vs market (AMD still
  −76%), which is expected — the goal was correct FCF *mechanics*, not price-matching.
