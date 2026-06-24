# VAL-1 Phase 5 — Diluted-share-forward adjustment — Implementation Plan

MODE: PLAN_AND_CREATE
ROLE: ARCH

> Plan-only. No production code in this document. Single deliverable. Target agent: BACKEND.
> Worktree: `.claude/worktrees/val-1-dcf-phases` (branch `val-1-dcf-archetype-phases`).

---

# Summary

For high-SBC (stock-based-compensation) tickers, the current DCF per-share step (`dcfValuePerShare := equityValue / sharesOutstanding`, `service.go:1431`) divides equity value by *today's* diluted share count. For SBC-heavy growth names (NVDA/TSLA-class) whose share count grows several percent per year, this understates the dilution borne by the year-N holder. Phase 5 adds a **DEFAULT-OFF, profile-gated** adjustment that projects the diluted share count forward at a derived historical SBC dilution rate and uses the **forward-diluted** share count as the per-share denominator in the DCF path only. It surfaces one diagnostic (`dcf_forward_diluted_shares`) plus the applied rate. With the profile flag off — the default for every current profile — every ticker is byte-identical to today.

---

# Analysis (cited)

**1. The per-share seam (the ONLY math edit).**
`internal/services/valuation/service.go:1431` —
```go
dcfValuePerShare := equityValue / sharesOutstanding
```
`sharesOutstanding` is resolved once at `service.go:786-797` via the priority chain diluted → market.basic → financial.basic (`latestFinancialData.DilutedSharesOutstanding` → `marketData.SharesOutstanding` → `latestFinancialData.SharesOutstanding`). This is the DCF/aggregate per-share denominator. The equity-bridge calc-trace at `service.go:1436-1446` already logs `diluted_shares` and `per_share` — Phase 5 must keep that trace honest (emit the forward count when used).

**2. This is the DCF path only.** The alternative-model path (`performAlternativeValuation`, builds its result at `service.go:1875-2040`) uses `modelResult.IntrinsicValuePerShare` (`service.go:2022`) — DDM/FFO/revenue_multiple compute their own per-share value inside the model and are **not** touched by Phase 5. `TestDDM_LegacyPath_BitForBit` is therefore unaffected by construction.

**3. Tangible-value path is separate and must NOT be perturbed.** `calculateTangibleValuePerShare` (`service.go:2115`, denominator `financial.DilutedSharesOutstanding` at `service.go:2120`) is the load-bearing `TestService_calculateTangibleValuePerShare_DilutedDenominator` pin. Phase 5 touches neither `calculateTangibleValuePerShare` nor the `sharesOutstanding` variable used elsewhere (Graham floor at `service.go:1460-1461`, sanity cross-check at `service.go:1649-1652`). The forward count is a **local** value introduced immediately before line 1431, consumed only by `dcfValuePerShare`.

**4. Data available to derive the dilution rate (entities stay clock-free).**
- `entities.FinancialData.DilutedSharesOutstanding` / `SharesOutstanding` (`financial_data.go:205-206`), per period.
- `entities.FinancialData.StockBasedCompensation` (`financial_data.go:50`).
- History accessors on `HistoricalFinancialData`: `GetSortedPeriods()` (`financial_data.go:231`), `GetAnnualPeriods()` (`financial_data.go:336`), `GetLatestPeriod()` (`financial_data.go:319`).
- **Decision:** derive the dilution rate from the **share-count CAGR across annual periods** (preferred, directly observable) — `rate = (sharesₙ / shares₀)^(1/years) − 1` over available FY periods, NOT from an SBC-expense model. SBC-expense ÷ price would require a price series and reintroduce noise. The `StockBasedCompensation` field is used only as an **eligibility gate** (a non-trivial SBC firm), not as the rate source. This keeps the derivation a pure function over `HistoricalFinancialData` with no clock and no new entity field.

**5. The wiring pattern is already established.** Layer-A reinvestment (`internal/services/valuation/reinvestment.go`) is the exact precedent: a profile-gated, default-off, service-layer adjustment that (a) early-returns a no-op when the profile opts out, (b) reads from `*profile.ResolvedProfile` + `*entities.HistoricalFinancialData`, (c) returns audit `[]string` for `result.Warnings`, (d) emits a calc-trace stage. Phase 5 follows this shape exactly. Whether the adjustment lives in the engine (`pkg/finance/dcf/dcf.go`) or in service.go post-processing: **service.go post-processing.** The forward-share adjustment operates on `equityValue` (an output of the bridge, not a DCF-engine input) and the share count (a service-layer concern resolved at `service.go:786`). `pkg/finance/dcf` has no concept of share count — keeping it out of the engine preserves the engine's bit-for-bit `TestCalculateDCF_LegacyProportional_BitForBit` surface.

**6. Diagnostic registration.** Any new `FairValueResponse` field competes for the `init()` field-count guard at `internal/observability/replay/diff.go:41-47`. `countFairValueFields()` (`diff.go:542-548`) currently returns `33 + 5 + 8 = 46`; `goFieldToJSON` (`diff.go:415-446`) maps Go→snake. The existing P1 DCF fields are registered there (`diff.go:442-446`) and declared on `FairValueResponse` (`fair_value.go:238-242`) + `entities.ValuationResult` (`valuation.go:141-145`). New fields must update **all three** sites + the count constant.

**7. CalcVersion.** Engine is at `"4.8"` (`service.go:1494`). Phase 5 is **DEFAULT-OFF**, so the default-path output is byte-identical and the version string does **not** bump on the default path. Per the established convention (a CalcVersion bump marks the *first* config flip that changes a production value), the bump is **deferred to the config commit that first enables the flag on a shipping profile** — not to this code commit. State this explicitly in the closeout.

---

# Plan

## Requirements

- **R1.** Add a default-off boolean to `AssumptionProfile` — `DilutedShareForwardEnabled bool` (`json:"diluted_share_forward_enabled,omitempty"`) — plus an optional cap `MaxAnnualDilutionRate float64` (`json:"max_annual_dilution_rate,omitempty"`, the clamp ceiling for the derived rate; 0 ⇒ a code default, e.g. 8%).
- **R2.** Derive the historical annual dilution rate as a pure function over `HistoricalFinancialData` (share-count CAGR across FY periods), with an SBC-presence eligibility gate. Clamp to `[0, MaxAnnualDilutionRate-or-default]`. A derived rate ≤ 0, < 2 usable FY periods, or zero SBC ⇒ no adjustment (no-op).
- **R3.** Project forward diluted shares over the resolved DCF horizon: `forwardShares = currentDiluted × (1 + rate)^horizon`. Use `forwardShares` as the per-share denominator for `dcfValuePerShare` ONLY.
- **R4.** Surface diagnostics: `dcf_forward_diluted_shares float64` and `dcf_applied_dilution_rate float64` on `ValuationResult` + `FairValueResponse`, both `omitempty` (absent on the no-op/default path).
- **R5.** DEFAULT-OFF byte-identity: with `DilutedShareForwardEnabled` false (the default for all current profiles, and the zero value when the profile registry is nil), the new code is a strict no-op and every ticker's response is byte-identical to today.
- **R6.** Coverage ≥ 90 % on new finance code (the derivation + projection function).

## Architecture + tradeoffs

- **Where:** a new file `internal/services/valuation/diluted_forward.go` (package `valuation`), mirroring `reinvestment.go`. One exported-to-package method `func (s *Service) applyDilutedShareForward(ctx, currentShares float64, rp *profile.ResolvedProfile, hist *entities.HistoricalFinancialData, horizon int) (forwardShares, appliedRate float64, audit []string)`. Pure-rate helper `deriveAnnualDilutionRate(hist *entities.HistoricalFinancialData) (rate float64, eligible bool)` lives here too and is independently unit-testable (clock-free, no `Service` receiver).
- **Call site:** in `performValuation`, **immediately before** `service.go:1431`. Compute `denomShares := sharesOutstanding`; if the adjustment fires, `denomShares = forwardShares`; then `dcfValuePerShare := equityValue / denomShares`. Append `audit` to `result.Warnings`; stamp the two diagnostic fields; update the equity-bridge calc-trace (`service.go:1444`) to log `denomShares`.
- **No-op contract (3 independent layers, mirroring Layer-A):** (1) `rp == nil` ⇒ no-op; (2) `!rp.DilutedShareForwardEnabled` ⇒ no-op; (3) `deriveAnnualDilutionRate` returns `eligible=false` (zero/negative rate, <2 FY periods, no SBC) ⇒ no-op. In every no-op case `denomShares == sharesOutstanding` and the two diagnostic fields stay zero (omitted). This is why the default path is byte-identical.
- **Tradeoff — engine vs service:** keeping it in service.go (not `pkg/finance/dcf`) preserves the engine's bit-for-bit test surface and respects the existing layering (the engine is share-count-agnostic). Cost: the per-share denominator now has a conditional branch in `performValuation`; mitigated by isolating all logic in `diluted_forward.go` so `performValuation` gains ~4 lines.
- **Tradeoff — CAGR vs SBC-expense model:** CAGR over reported share counts is directly observable and replay-deterministic; an SBC-expense/price model is more "Damodaran-canonical" but needs a price series and is noisier. Choose CAGR; document the SBC-expense refinement as a VAL-1.2 follow-up.
- **Horizon source:** use the resolved DCF horizon already in scope in `performValuation` (the Phase-2 horizon work; if Phase 2 has not landed at integration time, fall back to the existing explicit-projection length — coordinate via Shared-Contract Touchpoints). The horizon is read, not modified.

## Files touched

| File | Change |
|---|---|
| `internal/services/valuation/profile/profile.go` | Add `DilutedShareForwardEnabled bool` + `MaxAnnualDilutionRate float64` to `AssumptionProfile` (additive, omitempty). |
| `internal/services/valuation/profile/validation.go` | Range-check `MaxAnnualDilutionRate` ∈ `[0, 1]` (0 allowed = use default); no other invariant. |
| `internal/services/valuation/diluted_forward.go` | **NEW.** `deriveAnnualDilutionRate` + `applyDilutedShareForward`. |
| `internal/services/valuation/service.go` | At the per-share seam (~`:1431`): compute `denomShares`, call `applyDilutedShareForward`, divide by `denomShares`, append audit, stamp 2 diagnostic fields, update calc-trace log field. |
| `internal/core/entities/valuation.go` | Add `DCFForwardDilutedShares float64` + `DCFAppliedDilutionRate float64` (omitempty) to `ValuationResult`. |
| `internal/api/v1/handlers/fair_value.go` | Add the 2 fields to `FairValueResponse` + map them in the response builder (alongside `:785-789`). |
| `internal/observability/replay/diff.go` | `countFairValueFields()` 33→35; add 2 `goFieldToJSON` entries. |
| `config/assumption_profiles.json` | (Opt-in, can be a separate commit) set `diluted_share_forward_enabled: true` on the high-SBC profiles — see below. |
| `docs/openapi.yaml` | Document the 2 new `FairValueResponse` properties. |

## Build order

1. Profile field + validation (R1).
2. `diluted_forward.go` derivation + projection with unit tests (R2/R3/R6) — TDD, this is the finance-critical code.
3. Entities + response field + `diff.go` registration (R4) — update count guard atomically.
4. service.go seam wiring (R3/R5) + calc-trace update.
5. Flag-off byte-identity integration test (R5).
6. OpenAPI doc.
7. **Separate commit:** config opt-in on high-SBC profiles + CalcVersion bump (the first production value change).

---

# API Contracts

New `FairValueResponse` fields (both `omitempty`, absent on default/no-op path):

| JSON field | Type | Meaning |
|---|---|---|
| `dcf_forward_diluted_shares` | number | Diluted share count projected to the DCF horizon at the applied annual dilution rate. Present only when the adjustment fired (DCF path, profile-enabled, eligible). |
| `dcf_applied_dilution_rate` | number | The clamped annual dilution rate (decimal, e.g. `0.04`) used for the forward projection. Present only when the adjustment fired. |

Behavioral contract:
- Default path (flag off / ineligible): both fields omitted; `dcf_value_per_share` byte-identical to today.
- Fired path: `dcf_forward_diluted_shares > current_diluted_shares` (rate > 0), and the resulting `dcf_value_per_share` is **strictly lower** than the unadjusted value (larger denominator) — the pinned relationship.
- DDM/FFO/revenue_multiple responses unaffected (fields always omitted on those paths).

No new endpoints, no request-shape change, no error-model change.

---

# Tasks by Agent

**BACKEND (ordered):**

1. `profile.go`: add `DilutedShareForwardEnabled bool` + `MaxAnnualDilutionRate float64` to `AssumptionProfile` with godoc noting additive/default-off/bit-for-bit semantics (mirror the Layer-A block comment at `profile.go:144-162`).
2. `validation.go`: extend `validateProfile` (or a small `validateDilutedForwardFields`) to require `MaxAnnualDilutionRate ∈ [0,1]`; empty/0 is valid (means "use code default"). Add a table-driven test.
3. `diluted_forward.go` (NEW), **TDD first**:
   - `deriveAnnualDilutionRate(hist) (rate float64, eligible bool)` — pure: pull FY diluted-share counts via `GetAnnualPeriods()`/`GetSortedPeriods()`; require ≥2 usable FY periods with positive share counts and non-zero `StockBasedCompensation` somewhere in the series; compute share-count CAGR; return `eligible=false` if rate ≤ 0 or inputs insufficient. No clock.
   - `applyDilutedShareForward(ctx, currentShares, rp, hist, horizon)` — no-op gates (nil profile, flag off, ineligible); clamp rate to `[0, cap]` (cap = `MaxAnnualDilutionRate` or default 0.08); `forward = currentShares × (1+rate)^horizon`; return `(forward, rate, audit)`; emit a `diluted_share_forward` calc-trace stage (guard `s.calcEmitter != nil`); audit line in the `reinvestment_model:`-style source-tag convention.
4. `service.go`: at the per-share seam (~`:1431`) introduce `denomShares`, invoke `applyDilutedShareForward`, divide, append `audit` to result warnings, stamp `DCFForwardDilutedShares`/`DCFAppliedDilutionRate`, and pass `denomShares` (not `sharesOutstanding`) to the equity-bridge calc-trace at `:1444`. Do NOT touch the `sharesOutstanding` used by Graham (`:1460`) or sanity-check (`:1649`).
5. `valuation.go`: add the 2 `ValuationResult` fields (omitempty) near the P1 DCF block (`:141-145`).
6. `fair_value.go`: add the 2 `FairValueResponse` fields (omitempty + `example`) near `:242`; map in the builder near `:789`.
7. `diff.go`: bump `countFairValueFields()` 33→35 and the godoc count (`46`→`48`); add `"DCFForwardDilutedShares":"dcf_forward_diluted_shares"` and `"DCFAppliedDilutionRate":"dcf_applied_dilution_rate"` to `goFieldToJSON`. Run replay tests so the `init()` guard passes.
8. `openapi.yaml`: document the 2 properties.
9. **Separate config commit:** enable the flag on high-SBC profiles + bump CalcVersion `4.8`→`4.9` with a changelog note; regenerate the relevant baseline (operator step).

**QA:** validate flag-off byte-identity across the basket; validate the fired-path relationship on a high-SBC fixture; confirm DDM/FFO/revenue_multiple responses unchanged; confirm replay field-count guard passes.

**REVIEWER:** confirm (a) `sharesOutstanding` is not mutated and Graham/sanity/tangible paths are untouched; (b) all 3 no-op layers; (c) entities stay clock-free; (d) `diff.go` count + map updated atomically with the response field; (e) CalcVersion bump policy honored (deferred to config commit).

---

# Spec Updates

- **VAL-1 tracker** (`docs/reviewer/spec/VAL-1-...md`): tick the Phase 5 acceptance row "Diluted-share-forward adjustment optional and behind the profile"; record the derivation method (share-count CAGR), the diagnostics, and the deferred CalcVersion bump.
- **CLAUDE.md** (midas): add a bullet documenting the default-off diluted-share-forward adjustment, the per-share seam, the derivation method, the two diagnostic fields, and the byte-identity invariant.
- **OpenAPI** (`docs/openapi.yaml`): 2 new `FairValueResponse` properties with types/examples and omission-condition descriptions.
- **replay/diff.go**: count constant 33→35 + 2 `goFieldToJSON` entries (this is code but is the field-count contract).
- **TESTING.md**: note the new flag-off byte-identity test and the fired-path relationship pin under the valuation finance-module section.

---

# Shared-Contract Touchpoints

Phase 2 (horizon, FOUNDATION), Phase 3 (cyclical base), Phase 4 (exit-multiple) all also edit `performValuation`. Phase 5's footprint, kept additive/minimal:

- **Profile fields added:** `DilutedShareForwardEnabled bool`, `MaxAnnualDilutionRate float64`. New names — no collision with Phase 2/3/4 fields (`HorizonYears`, `RevenueBaseMethod`/`BaseMarginMethod`, `TerminalMethod`/`TerminalMultiple`).
- **service.go seam:** the per-share division at `service.go:1431` (`equityValue / sharesOutstanding`). This is **downstream** of Phase 2's horizon resolution and Phase 3/4's EV/terminal work — Phase 5 only reads the resolved horizon and consumes the final `equityValue`. It does **not** touch growth rates, EV, terminal, or the bridge inputs. Lowest-conflict seam of the four phases.
- **Horizon dependency:** Phase 5 reads the resolved DCF horizon (Phase 2 output). If Phase 2 lands first, consume its resolved value; if Phase 5 integrates first, fall back to the current explicit-projection length and let Phase 2 supersede. Flag this in the merge order.
- **New response fields (competes with Phase 3/4 for `diff.go` registration):** `dcf_forward_diluted_shares`, `dcf_applied_dilution_rate`. Phase 3 likely adds `dcf_base_normalization`; Phase 4 likely adds exit-multiple terminal fields. **Coordination rule:** whoever merges last reconciles `countFairValueFields()` to the cumulative total and ensures each new field has a `goFieldToJSON` entry. Phase 5 claims +2.
- **Config touchpoints:** `config/assumption_profiles.json` — Phase 5 only adds `diluted_share_forward_enabled`/`max_annual_dilution_rate` keys to high-SBC profiles; additive to whatever Phase 2/3/4 add to the same records.

---

# Tests

**New unit tests (≥90 % on `diluted_forward.go`):**
- `TestDeriveAnnualDilutionRate_*`: rising share count → positive rate; flat → eligible=false (rate ≤ 0); <2 FY periods → ineligible; zero SBC across series → ineligible; declining share count (buybacks) → rate ≤ 0 → ineligible (no-op, never inflates value).
- `TestApplyDilutedShareForward_NoOp_*`: nil profile; flag off; ineligible history — assert `forward == currentShares`, `rate == 0`, empty audit.
- `TestApplyDilutedShareForward_Fired`: enabled + eligible high-SBC fixture (TSLA/NVDA-like, share-count growing): assert `forward > currentShares`, `rate` clamped to cap, audit non-empty.
- Clamp test: derived rate above `MaxAnnualDilutionRate` (or 8% default) is capped.

**service-level / integration:**
- `TestService_performValuation_DilutedForward_FlagOff_ByteIdentical`: a profile-enabled run vs the same fixture with the flag off — flag-off result byte-identical to the pre-Phase-5 golden; the two diagnostic fields omitted.
- `TestService_performValuation_DilutedForward_FiredRelationship`: high-SBC fixture, flag on — `dcf_forward_diluted_shares > current diluted shares` AND `dcf_value_per_share` strictly less than the same run with the flag off. **Pin the relationship**, not an absolute value.
- Negative pin: DDM/FFO/revenue_multiple fixture with the flag on at the profile — the 2 fields stay omitted; `TestDDM_LegacyPath_BitForBit` still green.

**Regression / invariants to keep green:**
- `TestDDM_LegacyPath_BitForBit`, recompute-shadow byte-identity, `TestService_calculateTangibleValuePerShare_DilutedDenominator`, `TestCalculateDCF_LegacyProportional_BitForBit`, replay `init()` field-count guard.

---

# Next Steps

1. BACKEND executes tasks 1–8 (code + tests + OpenAPI) on `val-1-dcf-archetype-phases`, TDD on `diluted_forward.go` first.
2. QA validates flag-off byte-identity + fired-path relationship + unchanged alt-model responses + replay guard.
3. REVIEWER audits the no-op layers, untouched share-count paths, clock-free entities, and atomic `diff.go` update.
4. Coordinate merge order with Phase 2 (horizon dependency) and reconcile `countFairValueFields()` with Phase 3/4 at the last merge.
5. **Separate config commit** flips the flag on high-SBC profiles (`hypergrowth_profitable:high_growth`, `software_like_scaling:high_growth`, `software_like_large_scale:*`, `hypergrowth_early:high_growth`) and bumps CalcVersion `4.8`→`4.9` — that is the first production value change.

HANDOFF_TO: BACKEND
