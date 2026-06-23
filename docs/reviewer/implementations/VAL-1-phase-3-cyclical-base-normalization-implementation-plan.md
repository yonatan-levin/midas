# VAL-1 Phase 3 — Cyclical-base normalization — Implementation Plan

MODE: PLAN_AND_CREATE
ROLE: ARCH

> Plan-only artifact. No production code is written here. Target worktree:
> `C:\Users\Yonatan Levin\Documents\Programming\Projects\FinTech\Strade\midas\.claude\worktrees\val-1-dcf-phases`
> (branch `val-1-dcf-archetype-phases`).

---

# Summary

When the resolved `AssumptionProfile` archetype is cyclical (`cyclical_mid_cycle` or
`cyclical_trough`), the standard DCF base operating income must be normalized to
`max(latest_OI, mean_OI_3y)` instead of the raw latest (or TTM) operating income. A
trough-year base makes the projected rebound look aggressive; using the 3-year mean as a
floor produces a more defensible base. The plan adds one new diagnostic field,
`dcf_base_normalization`, recording which method fired (`latest` vs `3y_mean`), and is a
strict no-op for every non-cyclical ticker (byte-identical to today). The change is
purely additive and intervenes at a single seam in `performValuation`. It composes with
both the Layer-A reinvestment path and the legacy proportional path because both consume
the same `baseOI` scalar.

---

# Analysis (cited against current code)

### Where the DCF base is established
- `internal/services/valuation/service.go:1199-1200` — `dcfRestated := restatedViewOr(cleaned, latestFinancialData); baseOI := effectiveOI(dcfRestated)`. `effectiveOI` (`service.go:2284-2292`) returns `NormalizedOperatingIncome` if `>0`, else `OperatingIncome`.
- `internal/services/valuation/service.go:1224-1237` — when the latest period is a **quarter**, `baseOI` is **re-based to TTM** via `historicalData.TrailingTwelveMonthsOperatingIncome()` (BUG-015). For FY-latest, `baseOI` is left exactly as `effectiveOI(dcfRestated)` to preserve bit-for-bit.
- `internal/services/valuation/service.go:1239-1244` — guard: `if baseOI <= 0` → `ErrModelNotApplicable`.
- `internal/services/valuation/service.go:1270-1278` — `baseOI` is written into `dcfInputs.BaseOperatingIncome`.
- `internal/services/valuation/service.go:1345` — `baseOI` is **also** passed to `s.applyReinvestmentModel(ctx, &dcfInputs, resolvedProfile, baseOI, …)`. In `reinvestment.go:64` it computes `baseMargin = baseOI / ttmRevenue`. **Therefore normalizing `baseOI` before this line automatically flows into BOTH the legacy proportional DCF (`dcfInputs.BaseOperatingIncome`) and the Layer-A reinvestment margin seed.** This is the key reason to intervene on the single `baseOI` scalar rather than on `dcfInputs`.

**Conclusion: the correct seam is immediately after the TTM re-base block (after `service.go:1237`) and before the `baseOI <= 0` guard at `:1239`.** Normalizing here means: (a) the guard sees the normalized value (a trough firm whose latest OI is barely positive but whose 3y mean is healthy still passes — desirable); (b) both downstream consumers see the normalized base; (c) the BUG-015 `operating_income_base:` audit tag (`service.go:1581-1587`) still reports the value that actually fed the engine.

### How to detect `(cyclical, *)`
- `internal/services/valuation/profile/profile.go:27-28` — `ArchetypeCyclicalMidCycle = "cyclical_mid_cycle"` and `ArchetypeCyclicalTrough = "cyclical_trough"`.
- `ResolvedProfile` (`profile.go:175-178`) embeds `AssumptionProfile`, so `resolvedProfile.Archetype` is directly readable.
- `(cyclical, *)` = archetype string has prefix `cyclical_` (covers both mid-cycle and trough across all maturities). I recommend a small predicate `profile.IsCyclicalArchetype()` rather than a raw `strings.HasPrefix` at the call site, to keep the archetype taxonomy owned by the `profile` package (consistent with `IsLegacyMatureLargeBankDDM` at `profile.go:188`).
- `resolvedProfile` can be `nil` on test paths (`service.go:1509` guards `if resolvedProfile != nil`). The normalization MUST be guarded on non-nil + cyclical.

### How to get 3 years of operating income (clock-free)
- `internal/core/entities/financial_data.go:452-476` — `GetRecentYears(3)` returns the most recent N **annual (FY)** periods (`GetAnnualPeriods()` filtered, sorted by `FilingDate` descending). This is the correct source: it is FY-only (so it does not mix a single quarter into the mean) and clock-free (no `time.Now()`).
- `internal/core/entities/financial_data.go:296-316` — `GetOperatingIncomeHistory(periods)` exists but reads `GetSortedPeriods()` which **includes quarters**, so it can blend a quarterly OI into the mean. **Do NOT use it** for the mean; use `GetRecentYears(3)` and read `NormalizedOperatingIncome` (falling back to `OperatingIncome` when normalized is 0) on each, mirroring `effectiveOI`'s precedence.
- The mean must be computed over the FY periods' OI using the **same effective-OI precedence** (`NormalizedOperatingIncome>0 ? NormalizedOperatingIncome : OperatingIncome`) so the floor is comparable to `baseOI`.

### Diagnostic field plumbing (mirror the 5 existing DCF diagnostics)
- Entity: `internal/core/entities/valuation.go:141-145` — the 5 existing DCF diagnostics (`DCFHorizonYears` … `DCFTerminalGrowthUsed`). Add `DCFBaseNormalization string` here.
- Handler DTO: `internal/api/v1/handlers/fair_value.go:238-242` — same 5 fields. Add `DCFBaseNormalization string` here.
- Handler copy: `internal/api/v1/handlers/fair_value.go:785-789` — the `buildFairValueResponse` projection. Add `DCFBaseNormalization: result.DCFBaseNormalization,`.
- Stamp site: `internal/services/valuation/service.go:1528-1537` (the P2 diagnostic stamping block) — add `result.DCFBaseNormalization = <method>` where `<method>` is captured at the normalization seam.

### Replay field-count guard
- `internal/observability/replay/diff.go:415-446` — `goFieldToJSON` map; add `"DCFBaseNormalization": "dcf_base_normalization",`.
- `internal/observability/replay/diff.go:542-548` — `countFairValueFields()` returns `33 + 5 + 8 = 46`; bump the FairValueResponse count to **34** → `return 34 + 5 + 8`. Update the godoc comment (`:532-533`, `:543-544`) to say 34 (… + DCFBaseNormalization). The `init()` guard at `diff.go:41-46` panics on every replay test if this is skipped.

---

# Plan

## Requirements
- **R1**: When `resolvedProfile != nil` AND archetype ∈ {`cyclical_mid_cycle`, `cyclical_trough`}, the DCF base OI = `max(baseOI, mean3yOI)` where `mean3yOI` is the mean of effective OI over the 3 most recent FY periods.
- **R2**: A new `dcf_base_normalization` diagnostic records `"3y_mean"` when the floor raised the base, `"latest"` when latest/TTM was already ≥ the mean (or when the mean was unavailable). Field is `omitempty` so non-cyclical responses drop it (byte-identity).
- **R3**: Non-cyclical tickers and the DDM/FFO/revenue_multiple alt-model paths are **byte-identical** to today.
- **R4**: The mean is computed from FY periods only (no quarter mixing), via `GetRecentYears(3)`, clock-free (no entity-side wall clock).
- **R5**: Composes correctly with the Layer-A reinvestment path (margin seed) and the legacy proportional path.
- **R6**: ≥90% unit coverage on the new finance code.

## Architecture + tradeoffs

**Decision 1 — intervene on the `baseOI` scalar, after the TTM re-base, before the `<=0` guard.**
Single seam (`service.go`, between `:1237` and `:1239`). One mutation point flows into both DCF consumers. Alternative (mutating `dcfInputs.BaseOperatingIncome` and separately re-deriving the reinvestment margin) would require two edits and risk drift between the legacy and Layer-A base. Rejected.

**Decision 2 — `max(latest, mean3y)` floor semantics (not replace-with-mean).**
The tracker specifies `max(latest_OI, mean_OI_3y)`. This means normalization only ever *raises* a trough base; a peak-year cyclical (`cyclical_mid_cycle` near a high) keeps its latest base. This guarantees normalization is monotonic and never *lowers* value for a firm already above its 3y mean. The diagnostic distinguishes the two outcomes.

**Decision 3 — a `profile.IsCyclicalArchetype()` predicate.**
Keeps the cyclical taxonomy inside the `profile` package (matches `IsLegacyMatureLargeBankDDM`). If a future `cyclical_*` archetype is added, only the predicate updates. Implementation: method on `*ResolvedProfile` (or on `AssumptionProfile`) returning `r.Archetype == ArchetypeCyclicalMidCycle || r.Archetype == ArchetypeCyclicalTrough`. Avoid raw `strings.HasPrefix` at the service call site.

**Decision 4 — a small pure helper in the valuation package for the mean + decision.**
Add `normalizeCyclicalBaseOI(baseOI float64, hist *entities.HistoricalFinancialData) (normalizedOI float64, method string)` (suggested file `internal/services/valuation/cyclical.go`, parallel to `reinvestment.go`). It is pure (no clock, no logging) so it is trivially unit-testable to ≥90%. The service calls it only inside the cyclical guard. `method` ∈ {`"latest"`, `"3y_mean"`}. When fewer than 2 FY periods are available (mean undefined / not meaningful) → return `(baseOI, "latest")` (no-op, conservative). Threshold note: require ≥2 FY periods for a meaningful "mean"; using a single FY period as a "mean" would be a no-op anyway since `max(latest, latest)==latest`, but the explicit guard keeps `method` honest.

**Decision 5 — stamp the diagnostic only on the cyclical path.**
On the non-cyclical path the local `baseNormalizationMethod` string stays `""`, so `result.DCFBaseNormalization` is never set → `omitempty` drops it → byte-identity. Do NOT default it to `"latest"` for everyone (that would add a field to every DCF response and break byte-identity).

**Tradeoff — when the latest period is a quarter (BUG-015 TTM rebase).**
`baseOI` at the seam may be a TTM-annualized figure (`service.go:1227`). The 3y FY mean is annual. Comparing TTM-annual vs FY-mean-annual is apples-to-apples (both annualized), so the `max` is sound. The diagnostic `"3y_mean"` still means "the FY 3-year mean was the binding floor", which is correct.

## Files touched (all additive)

| File | Change |
|---|---|
| `internal/services/valuation/profile/profile.go` | Add `IsCyclicalArchetype()` predicate (method on `*ResolvedProfile` or `AssumptionProfile`). |
| `internal/services/valuation/cyclical.go` (new) | Add pure `normalizeCyclicalBaseOI(baseOI, hist) (float64, string)` helper + an effective-OI-of-period local helper. |
| `internal/services/valuation/service.go` | At the seam after `:1237`, before `:1239`: if `resolvedProfile != nil && resolvedProfile.IsCyclicalArchetype()` → call helper, reassign `baseOI`, capture `baseNormalizationMethod`, log + calc-trace emit. At `:1528-1537` stamp `result.DCFBaseNormalization = baseNormalizationMethod`. |
| `internal/core/entities/valuation.go` | Add `DCFBaseNormalization string` field (omitempty) after `:145`. |
| `internal/api/v1/handlers/fair_value.go` | Add DTO field after `:242`; add copy line after `:789`. |
| `internal/observability/replay/diff.go` | Add `goFieldToJSON` entry (`:446` area); bump `countFairValueFields` 33→34 (`:547`) + comments. |
| `docs/openapi.yaml` | Add `dcf_base_normalization` property (string, enum `[latest, 3y_mean]`). |

## Build order
1. `profile.IsCyclicalArchetype()` + its unit test (no dependencies).
2. `cyclical.go` pure helper + table-driven unit tests (depends on nothing but `entities`).
3. Entity field (`valuation.go`).
4. `diff.go` field-count + map update (must land in the SAME commit as the entity field or replay tests panic).
5. Service wiring (seam + stamp).
6. Handler DTO + copy.
7. OpenAPI.
8. Integration test (MXL trough fixture) + non-cyclical byte-identity test.

> Steps 3+4 MUST be a single atomic commit to keep the `init()` guard green.

---

# API Contracts

New `FairValueResponse` field:

```
dcf_base_normalization  string  // omitempty
```

- **Type**: string enum.
- **Values**: `"latest"` | `"3y_mean"`.
  - `"latest"` — the latest (or TTM-annualized) operating income was ≥ the 3-year FY mean, so no floor was applied; OR the cyclical profile fired but <2 FY periods were available.
  - `"3y_mean"` — the 3-year FY mean of effective operating income exceeded the latest base and was used as the DCF base (trough normalization fired).
- **Omission**: absent for all non-cyclical tickers and all non-DCF models (DDM/FFO/revenue_multiple). Present ONLY when `resolvedProfile.IsCyclicalArchetype()` is true on the standard DCF path.

Example (cyclical trough, MXL-shaped):
```json
{
  "ticker": "MXL",
  "dcf_value_per_share": 12.40,
  "assumption_profile": "cyclical_trough:standard_growth",
  "dcf_horizon_years": 5,
  "dcf_base_normalization": "3y_mean"
}
```

No status-code or error-model changes. No request-shape changes. No versioning break (additive, omitempty).

---

# Tasks by Agent

## BACKEND (ordered)
1. Add `func (r *ResolvedProfile) IsCyclicalArchetype() bool` (or on `AssumptionProfile`) in `internal/services/valuation/profile/profile.go`, returning true for `ArchetypeCyclicalMidCycle` and `ArchetypeCyclicalTrough`. Add a unit test covering both cyclical archetypes (true) and at least two non-cyclical archetypes + nil receiver (false).
2. Create `internal/services/valuation/cyclical.go` with a pure `normalizeCyclicalBaseOI(baseOI float64, hist *entities.HistoricalFinancialData) (float64, string)`:
   - Read the 3 most recent FY periods via `hist.GetRecentYears(3)`.
   - For each period compute effective OI (`NormalizedOperatingIncome>0 ? NormalizedOperatingIncome : OperatingIncome`) — extract a small `effectiveOIOfPeriod` helper (or reuse the same logic as `effectiveOI`).
   - If <2 FY periods → return `(baseOI, "latest")`.
   - Compute `mean` of effective OI over the available FY periods (2 or 3).
   - If `mean > baseOI` → return `(mean, "3y_mean")`; else `(baseOI, "latest")`.
   - No logging, no clock, no mutation of `hist`.
3. Add `DCFBaseNormalization string` (json `dcf_base_normalization,omitempty`) to `entities.ValuationResult` (`valuation.go`, after `DCFTerminalGrowthUsed`). **In the same commit**: update `internal/observability/replay/diff.go` — add `"DCFBaseNormalization": "dcf_base_normalization"` to `goFieldToJSON` and bump `countFairValueFields` from `33 + 5 + 8` to `34 + 5 + 8`, updating the godoc comments. Run a replay test to confirm the `init()` guard is green.
4. Wire the service seam in `performValuation` (`service.go`), inserting after the TTM-rebase block (`:1237`) and before the `if baseOI <= 0` guard (`:1239`):
   ```
   var baseNormalizationMethod string
   if resolvedProfile != nil && resolvedProfile.IsCyclicalArchetype() {
       normalizedOI, method := normalizeCyclicalBaseOI(baseOI, historicalData)
       baseNormalizationMethod = method
       if method == "3y_mean" {
           s.log(ctx).Info("Cyclical-base normalization: using 3y mean OI floor", … ticker, latest=baseOI, mean=normalizedOI)
           if s.calcEmitter != nil { s.calcEmitter.Emit(ctx, "cyclical_base_normalization", …) }
           baseOI = normalizedOI
       }
   }
   ```
   Declare `baseNormalizationMethod` in a scope visible at the stamp site. At the P2 stamp block (`:1528-1537`) add `result.DCFBaseNormalization = baseNormalizationMethod`.
5. Add the handler DTO field (`fair_value.go:238-242` block) and the projection copy line (`:785-789` block).
6. Update `docs/openapi.yaml` `FairValueResponse` with the `dcf_base_normalization` property (string, enum `[latest, 3y_mean]`, description noting omission semantics).
7. Add tests (see Tests section). Run `go test ./internal/services/valuation/... ./internal/observability/replay/... ./internal/core/entities/... -count=1` plus the full `go test ./... -count=1 -short`.

## QA
- Validate that a cyclical-trough fixture (MXL) returns `dcf_base_normalization: "3y_mean"` and a per-share value that differs from a forced-`latest` run.
- Validate that a non-cyclical fixture (AAPL/MSFT) response is byte-identical to master (no `dcf_base_normalization` key present).
- Confirm `TestDDM_LegacyPath_BitForBit`, recompute-shadow byte-identity, and `TestRecomputeUmbrellas_NoMutation` all pass.
- Confirm replay tests do not panic (field-count guard satisfied).

## REVIEWER
- Confirm the seam placement: normalization happens AFTER the TTM rebase and BEFORE the `<=0` guard, so both `dcfInputs.BaseOperatingIncome` and `applyReinvestmentModel`'s margin seed observe the normalized base.
- Confirm `GetRecentYears(3)` (FY-only) is used, not `GetOperatingIncomeHistory` (quarter-mixing).
- Confirm `baseNormalizationMethod` stays `""` on non-cyclical paths → `omitempty` drop → byte-identity.
- Confirm the `diff.go` field-count bump landed in the same commit as the entity field.
- Confirm no CalcVersion bump is taken (see policy below) OR that the bump is applied consistently at all stamp sites if the team chooses to bump.
- Confirm the new finance code (`cyclical.go`) is clock-free and ≥90% covered.

---

# Spec Updates

- **VAL-1 tracker** (`docs/reviewer/spec/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md`): tick the Phase 3 acceptance row "Cyclical-base normalization fires when profile is `(cyclical, *)`"; note the `dcf_base_normalization` field shipped; reaffirm mid-cycle industry estimates remain VAL-1.1 (out of scope).
- **CLAUDE.md gotcha** (add a bullet): "VAL-1 Phase 3 — cyclical-base normalization fires ONLY for `(cyclical_*, *)` profiles; DCF base OI = `max(latest/TTM OI, 3y FY mean OI)` computed via `GetRecentYears(3)` (FY-only, clock-free). The `dcf_base_normalization` diagnostic (`latest` | `3y_mean`) is `omitempty` — non-cyclical responses are byte-identical. Normalization is applied to the `baseOI` scalar at the single seam after the BUG-015 TTM rebase, so it flows into both the legacy proportional DCF and the Layer-A reinvestment margin seed."
- **OpenAPI** (`docs/openapi.yaml`): add `dcf_base_normalization` to `FairValueResponse` (string enum `[latest, 3y_mean]`). Auto-generated swagger artifacts remain stale (tracked separately in `swag-version-alignment-spec.md`).
- **replay diff.go** (`internal/observability/replay/diff.go`): `goFieldToJSON` entry + `countFairValueFields` 46→47 (FairValueResponse 33→34).
- **TESTING.md**: note the new cyclical-normalization unit + integration tests and the byte-identity regression for non-cyclical tickers.

---

# Shared-Contract Touchpoints

Phase 2 (archetype horizon) is the foundation; this plan assumes `projectionYears = p.HorizonYears` (`service.go:1259`) is already profile-resolved. Phases 4 (exit-multiple) and 5 (diluted-forward) touch the same `performValuation` DCF block. To graft cleanly:

- **New profile fields**: NONE. Phase 3 reads only the existing `Archetype`. (The existing `RevenueBaseMethod` / `BaseMarginMethod` enums already carry `two_year_average` / `mid_cycle` values for a future richer normalization, but Phase 3 deliberately uses the simple `max(latest, 3y_mean)` rule and does not consume them — leaves room for VAL-1.1.)
- **`service.go` seam modified**: base-metric establishment, specifically the gap between the TTM-rebase block (`:1224-1237`) and the `baseOI <= 0` guard (`:1239`). This is a DIFFERENT seam from Phase 4 (terminal/exit-multiple, `:1300-1340`) and Phase 5 (per-share/diluted, downstream of `dcfResult`), so no merge collision on the mutation point. All three converge only at the shared P2 diagnostic stamp block (`:1528-1537`), where each adds its own `result.DCF*` assignment — additive, no conflict.
- **New response field(s)**: exactly one — `dcf_base_normalization` (string enum `latest`|`3y_mean`). **This name is reserved by Phase 3.** Phase 4's field will be e.g. `dcf_terminal_exit_value` / `dcf_terminal_method` (already exists) and Phase 5's e.g. `dcf_forward_diluted_shares`. Each phase MUST add its own `goFieldToJSON` entry and bump `countFairValueFields` by its own field count. **Reconciliation rule when phases merge**: the field-count constant is additive — whoever merges second rebases the constant to `33 + N_total` and confirms every phase's `goFieldToJSON` entry is present. Phase 3 contributes exactly **+1** (`DCFBaseNormalization` → `dcf_base_normalization`).
- **Config changes**: NONE. No new `assumption_profiles.json` keys (Phase 3 reads archetype only).

---

# Tests

### Unit — `profile.IsCyclicalArchetype` (`profile/..._test.go`)
- `cyclical_mid_cycle` → true; `cyclical_trough` → true.
- `mature_large_scale`, `software_like_scaling`, `hypergrowth_profitable` → false.
- nil receiver → false.

### Unit — `normalizeCyclicalBaseOI` (`cyclical_test.go`, table-driven, ≥90%)
- Trough: latest OI = 100, FY OIs = [100, 400, 700] → mean 400 > 100 → returns `(400, "3y_mean")`.
- Peak/above-mean: latest OI = 700, FY OIs = [700, 400, 100] → mean 400 < 700 → returns `(700, "latest")`.
- `NormalizedOperatingIncome` precedence: a period with `Normalized=0, OperatingIncome=300` contributes 300 to the mean.
- <2 FY periods: 1 FY period → `(baseOI, "latest")`; 0 FY periods → `(baseOI, "latest")`.
- TTM-annualized base (latest period a quarter): baseOI = TTM 120, FY mean 400 → `(400, "3y_mean")` (apples-to-apples annual comparison).
- nil/empty `hist` → `(baseOI, "latest")` (defensive).

### Integration — service path (`service_test.go`)
- **MXL trough fixture** (`cyclical_trough:standard_growth`): assert `result.DCFBaseNormalization == "3y_mean"` AND the resulting `dcf_value_per_share` differs from a control run where the cyclical guard is bypassed (or where the fixture's 3y mean equals latest). Mirrors the tracker's Phase 3 test row.
- Assert the `operating_income_base:`/reinvestment audit reflects the normalized base when the Layer-A path is engaged for a cyclical profile (compose check).
- **Non-cyclical byte-identity**: an AAPL/MSFT (or any non-`cyclical_*`) fixture → `result.DCFBaseNormalization == ""` and the field is absent from the marshaled JSON; `dcf_value_per_share` unchanged vs the pre-change value (regression pin).

### Regression / invariants (must stay green)
- `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) — DDM path untouched.
- `TestRecomputeUmbrellas_NoMutation` and recompute-shadow byte-identity — datacleaner untouched.
- Replay `init()` field-count guard — green (field registered).
- `TestPostFairValue_EmptyBody_EqualsGET` — unaffected (field is response-only, deterministic per profile).

Edge cases that MUST have tests: latest exactly equals 3y mean (→ `"latest"`, no value drift); negative FY OI in the window (still averaged — a single negative year drags the mean down, which is the intended "don't over-credit a one-off peak" behavior; pin the arithmetic); the `<2 FY periods` no-op.

---

# CalcVersion bump policy

**Recommendation: DO bump `CalculationVersion` (4.7 → 4.8).** Phase 3 changes the DCF `dcf_value_per_share` output for any cyclical ticker at a trough (the base OI rises from latest to the 3y mean). Per the project's established convention (e.g. the Layer-A 4.6→4.7 bump that changed DCF output for scalers), the first production value-change to the DCF path warrants a CalcVersion increment so cached pre-change values are correctly invalidated and replay drift is attributable. Apply the bump at BOTH stamp sites consistently (search for the existing `4.7` literals in `service.go` and update together, mirroring the P5-C1 4.3→4.4 dual-site bump). Non-cyclical outputs are byte-identical, so only the `calculation_version` string drifts on those paths — acceptable and expected.

If the team prefers to defer the bump (treating Phase 3 as not-yet-production because cyclical archetypes may be config-gated), document that decision explicitly in the closeout; but the default for an output-changing DCF feature is to bump.

---

# Next Steps
1. BACKEND implements steps 1-7 in build order; steps 3+4 (entity field + diff.go) land atomically.
2. QA validates the MXL trough behavior and non-cyclical byte-identity, and confirms the load-bearing invariants.
3. REVIEWER checks seam placement, FY-only mean source, omitempty byte-identity, field-count atomicity, and CalcVersion consistency.
4. Reconcile `diff.go` field count with Phase 4/5 at merge time (Phase 3 contributes +1: `dcf_base_normalization`).

HANDOFF_TO: BACKEND
