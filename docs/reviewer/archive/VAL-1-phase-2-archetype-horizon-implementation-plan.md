# VAL-1 Phase 2 — Archetype-aware explicit DCF horizon (Implementation Plan)

MODE: PLAN_AND_CREATE
ROLE: ARCH

Plan target: `docs/reviewer/implementations/VAL-1-phase-2-archetype-horizon-implementation-plan.md`
Worktree: `.claude/worktrees/val-1-dcf-phases` (branch `val-1-dcf-archetype-phases`)
Tracker: `docs/reviewer/spec/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md`

---

# Summary

**Goal.** Make the DCF explicit-projection horizon archetype-driven *in production*, resolved from the shared `AssumptionProfile.HorizonYears` (mature large-scale ~3y, standard ~5y, high-growth profitable ~7y, hyper-growth ~10y), each paired with a Gordon terminal.

**The surprising reality (reconciled against current code, the tracker is stale).** Phase 2's *resolution and diagnostic logic is already implemented and unit-tested* — but only behind a **test-only estimator rebuild**. The single remaining gap is **production wiring**: the shared growth estimator is constructed with `Stage3Years = 0`, so it emits only 7 per-year growth rates. Because `params.ResolveInputs` clamps any profile horizon down to the growth-rate-slice length (and to the stage-sum), a profile that asks for `horizon_years: 10` is **silently clamped to 7 in production today**. Profiles asking for 3 or 5 already work (they are ≤ 7). So Phase 2 is *correct for 3y/5y/7y but broken for 10y in production*.

This plan closes that gap by deriving the shared estimator's `Stage3Years` from the maximum horizon any profile in the registry can request (capped at the engine ceiling), so the resolver never clamps a legitimate profile horizon. It is a **bounded, additive, single-seam change** with a hard byte-identity guarantee on the default (no-profile / horizon ≤ 7) path.

**Non-goals (out of Phase 2 scope).**
- Cyclical-base normalization (Phase 3).
- Exit-multiple terminal as a primary/alternative path (Phase 4) — Phase 2 is **Gordon terminal only**. The exit-multiple plumbing already exists in `params`/`service.go`/config but Phase 2 does not change its behavior or add profiles that select it.
- Diluted-share-forward adjustment (Phase 5).
- Any change to DDM / FFO / revenue_multiple paths.
- A `CalculationVersion` bump beyond what the value-change for 10y-horizon tickers warrants (decision below).
- Re-deriving or expanding the `AssumptionProfile` archetype→horizon *values* — `config/assumption_profiles.json` already carries the four target horizons (3/4/5/7/10).

---

# Analysis (current-code reality vs tracker; cited)

### A. The tracker's "flat 7y horizon" premise is stale
The tracker (`VAL-1-...-normalization.md:37`, `:79`) describes today's DCF as a hard-coded ~7y horizon and proposes Phase 2 as "Adds ~50 lines to service.go's DCF block, extends growth.Estimator to produce a longer rate slice when horizon ≥ 7." That proposal is **substantially already done** by the request-valuation-overrides (T4) + Tier-2-P2 work that landed after the tracker text was written. What remains is *only* the production estimator-length wiring.

### B. Horizon already resolves from the profile through the single authoritative path
`internal/services/valuation/service.go:936-943` reads `resolvedProfile.HorizonYears` into `resolverDefaults.ProfileHorizonYears`, and `params.ResolveInputs` applies the precedence chain at `internal/services/valuation/params/resolve.go:208-220`:

```
p.HorizonYears = growthRateLen           // default source (estimator slice length)
if d.ProfileHorizonYears > 0 { p.HorizonYears = d.ProfileHorizonYears; Source=Profile }
if o.HorizonYears != nil    { p.HorizonYears = *o.HorizonYears;        Source=Request }
```

The resolved horizon flows to the DCF at `service.go:1259` (`projectionYears := p.HorizonYears`) and into `dcf.Inputs.ProjectionYears` (`service.go:1276`). The growth-rate slice is truncated to match at `service.go:1265-1268`. The diagnostic `result.DCFHorizonYears = projectionYears` is stamped at `service.go:1528`. **So horizon resolution, application, truncation, and diagnostic surfacing are all wired.**

### C. The clamp is the production bug
`params.ResolveInputs` clamps a profile/default-sourced horizon to the growth-stage sum (`resolve.go:252-267`) and to the post-estimator growth-rate length (`resolve.go:272-287`), appending a warning and *not* a 422 (legacy silent-clamp preservation; 422 only when the horizon is request-sourced). With the shared estimator at `Stage3Years = 0`:
- `growth.DefaultEstimatorConfig()` (`estimator.go:36-47`) → `Stage1=3, Stage2=4, Stage3=0` → **7 rates** (`estimator.go:112-113`, `:140` no-op).
- `estimatorDefaults()` (`service.go:2177-2186`) feeds `Stage1/2/3` into `resolverDefaults`, so `stageSum = 7` and `growthRateLen = 7`.
- A profile with `horizon_years: 10` → clamped to 7 with a warning. **Hyper-growth tickers never get their 10y horizon in production.**

### D. The fix mechanism already exists in the estimator
`estimator.go:134-159` implements the Stage-3 long-horizon fade: setting `Stage3Years = 3` yields `3+4+3 = 10` rates. The estimator test `estimator_test.go:266-268` and the negative test `:292-299` pin both behaviors. **The estimator does not need new logic — it needs to be *constructed* with a non-zero `Stage3Years` in production.**

### E. Proof the only gap is production wiring
The Phase-2 horizon tests already exist and pass: `service_test.go:3824-3867` (`TestService_DCF_HorizonFromProfile_MatureLargeScale_3y`, `..._HypergrowthProfitable_10y`). The 10y test only passes because `buildP2TestService` (`service_test.go:3780-3815`) **manually rebuilds the estimator with `Stage3Years = 3`** (`:3811`) with the explicit comment (`:3808-3809`):

> "This mirrors what production wiring will do once Pre-P2's config-driven Stage3Years lands in cmd/server."

That production wiring is exactly this plan.

### F. Config + entity readiness
- `config/assumption_profiles.json` already carries horizons 3/4/5/7/10 across 31 profiles (`grep horizon_years` → max = 10, three profiles at 10).
- Profile validation already bounds `HorizonYears ∈ [0,15]` (`profile/validation.go:92-95`).
- The 5 DCF diagnostic fields already exist on `entities.ValuationResult` (`internal/core/entities/valuation.go:141-145`) and on `handlers.FairValueResponse` — **no new response field is added by Phase 2** (the replay field-count guard is not triggered).
- A `config.ValuationConfig` already has `DCFProjectionYears` (`config/config.go:282`), `DCFMaxGrowthRate`/`DCFMinGrowthRate` (`:283-284`), and `DefaultTerminalGrowthCap` (`:266`). The estimator is built in `NewService` (`service.go:127-135, 189`) from `DefaultEstimatorConfig()` overlaid with config max/min only — **`Stage3Years` is never set from config.**

### G. Byte-identity risk surface
Extending `Stage3Years` to ≥ 3 in production lengthens the growth-rate slice for **every** ticker, even mature ones. But the resolved horizon still clamps the slice fed into the DCF (`service.go:1265-1268`), so:
- A profile with horizon 3/5/7 still feeds a 3/5/7-length slice into `dcf.CalculateDCF` — **the DCF inputs are unchanged for those profiles.**
- A no-profile / nil-registry path keeps `ProfileHorizonYears = 0`, so the default source is `growthRateLen` — which now becomes 10, not 7. **This is the one place a non-overridden, no-profile valuation could change** (a longer default horizon). The plan neutralizes this (decision D2 below) so the default/no-profile path stays byte-identical: the default-sourced horizon stays clamped to its legacy value unless a profile explicitly raises it.

---

# Plan

## Requirements
1. In production, a ticker resolving to a profile with `horizon_years: H` (H ≤ 10) must produce `DCFHorizonYears == H` with `len(DCFPerYearPV) == H` — no silent clamp to 7.
2. The shared estimator must emit at least `maxProfileHorizon` growth rates (capped at the engine ceiling) so the resolver never clamps a legitimate profile horizon.
3. The no-profile / nil-registry / horizon-omitted default path must remain **byte-identical** to today (no horizon drift from the longer slice).
4. Gordon terminal only; no exit-multiple behavior change.
5. Coverage ≥ 90% on any new finance code; all load-bearing invariants stay green.

## Architecture decisions + tradeoffs

### D1 — Where to lengthen the slice: shared-estimator `Stage3Years`, derived from the registry's max horizon (RECOMMENDED)
Compute the maximum `HorizonYears` across all profiles the registry can resolve, then set the shared estimator's `Stage3Years` so `Stage1 + Stage2 + Stage3 >= maxProfileHorizon` (capped at the engine ceiling, `params.MaxDCFProjectionYears`). Concretely, with `Stage1=3, Stage2=4` and `maxProfileHorizon = 10`, set `Stage3 = max(0, 10 - 7) = 3`.

- **Pro:** One construction-time seam in `NewService`. The estimator already supports it. Self-tuning: if a future profile sets horizon 12, the slice grows to match (bounded by the ceiling). No per-request allocation on the default path.
- **Pro:** The growth-stage durations the resolver sees (`estimatorDefaults()`) stay in lock-step with the estimator that actually ran — the existing `estCfg := estimator.Config()` (`service.go:951`) flows the same `Stage3Years` into `resolverDefaults.Stage*Years`, so `stageSum` rises to 10 and the stage-sum clamp (`resolve.go:252`) no longer fires for a 10y profile.
- **Con:** Slightly longer slice computed for every ticker (negligible — pure arithmetic, no I/O). Neutralized for byte-identity by D2.

**Alternatives considered:**
- *Per-request estimator keyed on resolved profile horizon.* Rejected: ordering problem — the profile resolves (`service.go:814-847`) **after** the growth estimate runs (`service.go:742`); switching to a profile-driven per-request estimator would require reordering profile resolution before the estimate or a second estimate pass. Higher blast radius, touches the §3.7 ordering contract that T4/T5 carefully established. Out of proportion to the need.
- *Raise `Stage2Years` instead of adding `Stage3Years`.* Rejected: changes the fade curve shape for the 7y profiles (byte-identity break), and `Stage3Years` is the field explicitly designed for this (`estimator.go:23-28`).

### D2 — Preserve default-path byte-identity: keep the *default-sourced* horizon at its legacy value
Lengthening the shared slice makes `growthRateLen = 10`, which would raise the **default-sourced** horizon (no profile, no override) from 7 to 10 — a behavior change for any ticker that resolves no profile (nil registry, fallback). To prevent that:

Add a `LegacyDefaultHorizonYears` field to `params.Defaults` (value = the legacy default horizon, 7 — or, more precisely, `min(7, growthRateLen)` computed the legacy way). In `ResolveInputs`, when the horizon is **default-sourced** (no profile, no override), use `LegacyDefaultHorizonYears` instead of the raw `growthRateLen`. Profile- and request-sourced horizons are unaffected (they already win via the precedence chain) and are validated/clamped against the *new* longer `growthRateLen` (so a 10y profile is honored).

- This makes the change *purely additive*: profile horizons up to 10 now work; the no-profile path produces the identical 7y horizon and identical DCF inputs as today.
- `LegacyDefaultHorizonYears` is sourced in `service.go` from the **legacy** computation (`growthRateLen` capped to the pre-change stage-sum, i.e. 7) so the production default is unchanged even though the underlying slice is longer.

> Implementer note: confirm the exact legacy default by reading the pre-change behavior — with the shared `3/4/0` estimator, default horizon = `growthRateLen` (7) clamped to `stageSum` (7) = 7. `LegacyDefaultHorizonYears` must reproduce that 7 exactly. Pin it with the byte-identity test (T3 below).

### D3 — Engine ceiling clamp
`maxProfileHorizon` is clamped to `params.MaxDCFProjectionYears` (the `dcf.validateInputs` `ProjectionYears > 50` rail) when deriving `Stage3Years`, so a malformed config with a huge horizon cannot drive the slice past the engine ceiling. Profile validation already bounds horizon at 15 (`profile/validation.go:92-95`), so in practice this clamp never binds, but it is defensive and documents the invariant.

### D4 — `CalculationVersion` bump
Phase 2 **changes intrinsic value for hyper-growth (horizon-10) tickers in production** (they were silently valued at 7y, now valued at 10y). That is a deliberate, value-affecting calibration change on the DCF path. Per the established bump policy (a first production value-change warrants a bump), **bump `CalculationVersion` 4.7 → 4.8** at both stamp sites (the same two sites the Layer-A 4.6→4.7 and DDM 4.3→4.4 changes touched — implementer greps `CalculationVersion` / `"4.7"` to find them; update the 4 `service_test.go` version pins). Profiles at 3y/5y/7y are unchanged in value, but the version is global to the engine, so it drifts on all DCF paths — this is expected and matches how 4.6→4.7 behaved.

> If the BACKEND implementer finds that no production profile actually resolves to 10y for any real ticker today (i.e. the registry rules never emit a hypergrowth_profitable archetype in production), raise this with ARCH before bumping: the bump may be premature. The plan's default assumption is that the change is value-affecting and the bump is warranted.

## Files / modules touched
| File | Change | Kind |
|---|---|---|
| `internal/services/valuation/service.go` (`NewService`, ~`:127-189`) | Derive shared estimator `Stage3Years` from registry max horizon (capped at ceiling); pass `LegacyDefaultHorizonYears` baseline. | additive |
| `internal/services/valuation/service.go` (`estimatorDefaults`, `:2177`; resolver-defaults block `:953-971`) | Populate `LegacyDefaultHorizonYears` in `resolverDefaults`. | additive |
| `internal/services/valuation/params/params.go` (`Defaults` struct) | Add `LegacyDefaultHorizonYears int`. | additive field |
| `internal/services/valuation/params/resolve.go` (`ResolveInputs`, `:208-220`) | Default-sourced horizon uses `LegacyDefaultHorizonYears` (fallback to `growthRateLen` when 0, preserving existing callers). | minimal logic |
| `internal/services/valuation/profile/` (registry) | Add a small accessor to expose the max `HorizonYears` across loaded profiles (e.g. `Registry.MaxHorizonYears() int` or a package helper over the loaded set). | additive method |
| `internal/services/valuation/service.go` (`CalculationVersion` stamp sites ×2) | 4.7 → 4.8. | value bump |
| `internal/services/valuation/service_test.go` | Replace the test-only `Stage3Years=3` rebuild in `buildP2TestService` with reliance on production wiring (or keep as belt-and-suspenders but add a production-path test); update 4 version pins; add standard/high fixtures. | tests |
| `config/assumption_profiles.json` | **No change** — horizons already present. | — |
| `internal/core/entities/valuation.go`, handlers | **No change** — diagnostic fields already exist. | — |

## Per-step build order (TDD)
1. **RED — production-wiring test.** Add a test that constructs the service via the *production* `NewService` (no manual estimator rebuild) with a registry whose max horizon is 10, and asserts a hypergrowth ticker yields `DCFHorizonYears == 10` and `len(DCFPerYearPV) == 10`. It fails today (clamps to 7).
2. **GREEN — registry max-horizon accessor.** Add `Registry.MaxHorizonYears()` (or equivalent) returning the max `HorizonYears` over loaded profiles; unit-test it.
3. **GREEN — `NewService` Stage3 derivation.** In `NewService`, compute `maxH = min(registry.MaxHorizonYears(), params.MaxDCFProjectionYears)`; set `estimatorCfg.Stage3Years = max(0, maxH - (Stage1+Stage2))`. Guard nil registry (Stage3 stays 0 → legacy 7).
4. **GREEN — `LegacyDefaultHorizonYears` plumbing.** Add the field to `params.Defaults`; in `ResolveInputs`, when default-sourced, use it (fallback to `growthRateLen` when 0). Populate it in `resolverDefaults` from the legacy 7-clamp computation.
5. **VERIFY byte-identity.** Run the default-path byte-identity tests + recompute-shadow + DDM bit-for-bit (all must stay green). Add the explicit byte-identity pin (T3).
6. **GREEN — version bump.** Bump 4.7→4.8; fix the 4 version pins.
7. **Tests + coverage.** Add the standard(5y)/high(7y) fixtures; confirm ≥ 90% coverage on touched finance code; run `go test ./... -count=1`.
8. **Docs.** Tick the VAL-1 tracker Phase-2 checkboxes; add the CLAUDE.md gotcha; update OpenAPI description note + TESTING.

---

# API Contracts
**No new fields, params, or status codes.** The 5 DCF diagnostic fields (`dcf_horizon_years`, `dcf_terminal_method`, `dcf_terminal_pct_of_ev`, `dcf_per_year_pv`, `dcf_terminal_growth_used`) already exist on `FairValueResponse` and `entities.ValuationResult`. Observable behavior change: for tickers resolving to a 10y-horizon profile, `dcf_horizon_years` now reports `10` (was silently `7`) and `dcf_per_year_pv` has 10 elements. The `terminal_dominance` warning (`service.go:1545`) may now appear/disappear for those tickers since a longer horizon lowers `dcf_terminal_pct_of_ev`. The request-override `horizon_years` / `growth_stages` contract (Layer-1 400, Layer-2 422 effective-horizon>50) is **unchanged**. Replay field-count guard (`internal/observability/replay/diff.go`) is **not** triggered (no new field).

---

# Tasks by Agent

## BACKEND (ordered)
1. Add `params.Defaults.LegacyDefaultHorizonYears int` (`params/params.go`); document it as "the legacy default-sourced horizon used when no profile/override applies — preserves default-path byte-identity when the estimator slice is lengthened for long-horizon profiles."
2. In `params.ResolveInputs` (`resolve.go:208-220`): when the horizon resolves **default-sourced** (no profile, no override), set `p.HorizonYears = d.LegacyDefaultHorizonYears` if `> 0`, else `growthRateLen` (backward-compatible). Profile/request branches unchanged. Keep the clamps (`:252-328`) validating against the real `growthRateLen`/`stageSum`.
3. Add a registry accessor exposing the max `HorizonYears` across loaded profiles (`profile` package). Nil-safe; returns 0 when no profiles. Unit-test it against `config/assumption_profiles.json` (expect 10).
4. In `NewService` (`service.go:127-189`): after building `estimatorCfg`, derive `maxH = min(profileRegistry.MaxHorizonYears(), params.MaxDCFProjectionYears)` (guard nil registry → 0); set `estimatorCfg.Stage3Years = max(0, maxH - (estimatorCfg.Stage1Years + estimatorCfg.Stage2Years))`. Document the byte-identity rationale inline.
5. In the `resolverDefaults` block (`service.go:953-971`) populate `LegacyDefaultHorizonYears` with the legacy computation (7 for the shared 3/4 estimator: `growthRateLen` capped to the *pre-change* stage-sum). Implementer must verify this reproduces today's default horizon exactly.
6. Bump `CalculationVersion` 4.7 → 4.8 at both stamp sites; update the 4 `service_test.go` version pins. (See D4 caveat — confirm value-change before bumping.)
7. Remove (or demote to belt-and-suspenders) the test-only `Stage3Years=3` rebuild in `buildP2TestService` (`service_test.go:3810-3813`) once the production path produces 10 rates; ensure the existing P2 horizon tests pass against production wiring.
8. Run full gates: `go test ./... -count=1`, `go vet ./...`, `./scripts/lint-logs.ps1`, `./scripts/lint-prometheus-registers.ps1`.

## QA
- Live API: pick a ticker that resolves to a hypergrowth/10y profile; confirm `dcf_horizon_years == 10`, `len(dcf_per_year_pv) == 10`, and `dcf_terminal_pct_of_ev` is lower than a 7y run would give.
- Confirm a mature/large-scale ticker (KO-like) still reports `dcf_horizon_years == 3` and a standard mid-cap reports `5`.
- Confirm `POST {}` == `GET` byte-identity for a non-profile / default ticker (no value drift from the longer slice).
- Confirm request override `horizon_years` still returns 422 above the effective-horizon ceiling and 200 within range.

## REVIEWER
- Verify the default/no-profile path is byte-identical (the `LegacyDefaultHorizonYears` neutralization is the load-bearing detail — confirm it reproduces 7 exactly and that profile horizons are validated against the *new* longer `growthRateLen`).
- Confirm the truncation at `service.go:1265-1268` still feeds an `H`-length slice for 3/5/7 profiles (so their DCF inputs are unchanged).
- Confirm DDM/FFO/revenue_multiple paths are untouched and `TestDDM_LegacyPath_BitForBit` is green.
- Sanity-check the `CalculationVersion` bump scope and the 4 pin updates.

---

# Spec Updates
- **VAL-1 tracker** (`docs/reviewer/spec/VAL-1-...-normalization.md`): tick Phase 2-5 checkbox rows that Phase 2 satisfies — `[x] Horizon resolved from profile; per-archetype values sane.` Add a "Phase 2 SHIPPED" note describing the production estimator-length wiring + `LegacyDefaultHorizonYears` byte-identity preservation, citing the resolver and `NewService` seams. Correct the stale "flat 7y horizon" framing (`:37`, `:79`) with a pointer to the T4/Tier-2-P2 mechanics.
- **CLAUDE.md gotcha** (append to midas `CLAUDE.md`): "VAL-1 Phase 2 — the DCF explicit horizon comes from `AssumptionProfile.HorizonYears` via `params.ResolveInputs`. Production honors horizons up to 10 because `NewService` derives the shared growth estimator's `Stage3Years` from the registry's max profile horizon (capped at `params.MaxDCFProjectionYears`); the no-profile/default path stays byte-identical via `params.Defaults.LegacyDefaultHorizonYears` (= legacy 7). A profile horizon is clamped (warn, not 422) only if it exceeds the estimator slice length — keep `Stage3Years` ≥ `maxProfileHorizon − (Stage1+Stage2)` or long-horizon profiles silently clamp. CalcVersion 4.8."
- **OpenAPI** (`docs/openapi.yaml`): update the `dcf_horizon_years` / `dcf_per_year_pv` descriptions to note the value is archetype-driven (3/5/7/10), not fixed. No schema/shape change. (Auto-generated swagger remains separately deferred per the tracker `:17`.)
- **TESTING.md**: add the new horizon fixtures (mature/standard/high/hyper) and the default-path byte-identity pin to the valuation test inventory.

---

# Shared-Contract Touchpoints (CRITICAL for parallel Phases 3/4/5)

Phases 3 (cyclical base), 4 (exit-multiple terminal), 5 (diluted-forward) all touch the same `performValuation` DCF block + `AssumptionProfile`. Phase 2 keeps its footprint minimal and additive:

**(a) New `AssumptionProfile` / `ResolvedProfile` fields:** **NONE.** Phase 2 consumes the existing `HorizonYears` (`profile/profile.go:119`) and `TerminalMethod` (`:125`). Phases 4/5 add their own fields independently; Phase 2 does not reserve or rename anything.

**(b) The exact `service.go` seam where horizon is resolved/applied:**
- Resolved: `resolverDefaults.ProfileHorizonYears` (`service.go:936-943, :967`) → `params.ResolveInputs` (`resolve.go:208-220`).
- Applied: `projectionYears := p.HorizonYears` (`service.go:1259`) → `dcf.Inputs.ProjectionYears` (`:1276`); growth-slice truncation at `:1265-1268`; diagnostic stamp at `:1528`.
- Phase 3 (cyclical base) should hook the **`baseOI` derivation upstream of `service.go:1271`** (it changes the base, not the horizon) — no conflict with Phase 2's horizon seam.
- Phase 4 (exit-multiple) should hook **`p.TerminalMethod` / `p.TerminalMultiple`** (`resolve.go:153-181`) and the terminal-blend block at `service.go:1300+` — orthogonal to horizon. Phase 4 must NOT change the horizon resolution.

**(c) New `params.Resolve*` function:** **NONE.** Phase 2 adds one **field** (`Defaults.LegacyDefaultHorizonYears`) and a small branch inside the existing `ResolveInputs` horizon block. No new exported resolver function. Phases 4/5 can extend `ResolveInputs`/`ResolveTerminal` additively without colliding with this field.

**(d) Config changes to `config/assumption_profiles.json`:** **NONE for Phase 2** (horizons already present). Phase 4 will add `terminal_method: "exit_multiple"` + `terminal_multiple` to selected profiles; Phase 2 does not touch any profile entry, so Phase 4 grafts cleanly.

**Coordination rule for the other phases:** the shared estimator's `Stage3Years` is now derived from `Registry.MaxHorizonYears()`. If Phase 4/5 introduces a profile with a horizon > 10, the slice auto-extends (bounded by `params.MaxDCFProjectionYears`) — no extra wiring needed, but those phases should add a fixture asserting the new max resolves un-clamped.

---

# Tests (table-driven; ≥ 90% coverage on new finance code)

### T1 — Production-path archetype horizon grid (the load-bearing new test)
Construct the service via **production `NewService`** (no manual estimator rebuild) with a registry loaded from `config/assumption_profiles.json` (or a fixture registry covering the four archetypes). Table:

| Case | Archetype/maturity | Expected `DCFHorizonYears` | `len(DCFPerYearPV)` | Terminal method |
|---|---|---|---|---|
| mature large-scale | `mature_large_scale:mature` | 3 | 3 | gordon_growth |
| standard growth | standard mid-cap profile | 5 | 5 | gordon_growth |
| high-growth profitable | large-cap tech (AAPL-like) | 7 | 7 | gordon_growth |
| hyper-growth profitable | `hypergrowth_profitable:high_growth` | 10 | 10 | gordon_growth |

Assert `0 < DCFTerminalPctOfEV ≤ 1`, `DCFTerminalGrowthUsed > 0`, `DCFTerminalMethod == "gordon_growth"` in every row. The hyper-growth row is the one that fails before this plan lands.

### T2 — Registry max-horizon accessor
Unit-test `Registry.MaxHorizonYears()` against the loaded config → expect `10`. Nil/empty registry → `0`.

### T3 — Default-path byte-identity pin (load-bearing)
For a no-profile / nil-registry ticker (and a `POST {}` vs `GET` pair), assert the DCF result is byte-identical to the pre-change behavior: `DCFHorizonYears == 7`, identical `DCFPerYearPV` length and values, identical `dcf_value_per_share`. This proves D2 (the `LegacyDefaultHorizonYears` neutralization) holds and the longer slice did not leak into the default path.

### T4 — Stage3 derivation
Unit-test the `NewService` derivation: with `Stage1=3, Stage2=4` and registry max 10 → `Stage3 == 3`; with registry max 7 → `Stage3 == 0`; nil registry → `Stage3 == 0`; with a (hypothetical) registry max 60 → `Stage3` clamped so `total ≤ MaxDCFProjectionYears`.

### T5 — AAPL ≤ ±5% regression (tracker acceptance)
For an AAPL fixture classified as high-growth (7y) — i.e. its horizon is *unchanged* by this plan — assert `dcf_value_per_share` does not drift more than ±5% vs the pre-change baseline (in practice it should be 0% drift since AAPL stays at 7y; the ±5% tolerance is the tracker's stated bound and a safety margin against incidental slice-length effects). Pin both the value and `DCFHorizonYears == 7`.

### T6 — Estimator slice length (existing, keep green)
`estimator_test.go:266-299` already pins `Stage3Years=3 → 10 rates` and `Stage3Years=0 → 7 rates`. No change; confirm green.

### Load-bearing invariants to keep green (run after every step)
- `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) — DDM untouched; horizon change is DCF-path only.
- `TestRecomputeUmbrellas_NoMutation` + recompute-shadow byte-identity (`git diff --quiet internal/integration/testdata/recompute-shadow/`).
- `TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_*`.
- The request-overrides byte-identity suite (`TestPostFairValue_EmptyBody_EqualsGET`) and the Layer-2 horizon 422 tests.
- Full `go test ./... -count=1` EXIT 0.

---

# Next Steps
1. BACKEND implements steps 1-8 in the build order (TDD: T3 + T1 first as RED where applicable).
2. QA validates the live archetype grid + default-path byte-identity.
3. REVIEWER focuses on the `LegacyDefaultHorizonYears` byte-identity neutralization and the CalcVersion bump scope.
4. Hand back to ARCH only if the D4 value-change/bump premise is wrong (no production ticker resolves to 10y) — otherwise proceed to merge and unblock Phases 3/4/5, which graft onto the documented seams above.

HANDOFF_TO: BACKEND
