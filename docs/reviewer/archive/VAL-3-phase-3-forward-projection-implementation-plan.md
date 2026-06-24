# VAL-3 Phase 3 — Forward FFO/AFFO Projection — Implementation Plan

**Status:** EXECUTION-READY (depends on VAL-3 Phase 2 / GitHub #15 landing first in the same worktree/branch).
**Scope owner:** BACKEND (Go), with QA acceptance and REVIEWER sign-off.
**Engine touch surface:** `internal/services/valuation/models/ffo.go` + tests; optionally `config/assumption_profiles.json`; NO `service.go` source change required (CostOfEquity/GrowthEstimate/Profile already wired).
**Worktree root:** `c:\Users\Yonatan Levin\Documents\Programming\Projects\FinTech\Strade\midas\.claude\worktrees\val-3-affo-forward`

References:
- Spec: [VAL-3 tracker](VAL-3-ffo-affo-subsector-multiples-and-forward-projection.md) — Phase 3 (§ "Phase 3 — Forward FFO/AFFO projection"), Issue 4 (discount = cost of equity), Phase 3 acceptance checklist.
- Model: [ffo.go](../../../internal/services/valuation/models/ffo.go) — existing forward scaffold at lines 252–297.
- Result shape: [router.go](../../../internal/services/valuation/models/router.go) — `ModelResult` `TrailingValue`/`ForwardValue`/`HorizonSelected`/`TerminalMultiple` (lines 126–129); `ModelInput.Profile`/`GrowthEstimate`/`CostOfEquity` (lines 40–94).
- Profile types: [profile.go](../../../internal/services/valuation/profile/profile.go) (`HorizonYears` L119, `TerminalMultiple` L128); [validation.go](../../../internal/services/valuation/profile/validation.go).
- Wiring: [service.go](../../../internal/services/valuation/service.go) `performAlternativeValuation` (lines 1858–1968) — `CostOfEquity` L1948, `GrowthEstimate` L1945, `Profile` L1966.
- Config: [assumption_profiles.json](../../../config/assumption_profiles.json) — `reit_*` profiles (lines 401–588) + REIT archetype rules (lines ~663–730).
- Existing forward tests: [ffo_forward_test.go](../../../internal/services/valuation/models/ffo_forward_test.go).
- Phase 2 outputs (assumed): `entities.FinancialData.MaintenanceCapEx`, AFFO computation in `ffo.go`, `pffo_value_per_share`/`paffo_value_per_share` response fields.

---

## 1. Goal & scope

### Goal
Make the REIT FFO model project the **headline base metric forward** over a profile-driven horizon, discount at **cost of equity**, apply a **terminal P/FFO (P/AFFO) multiple**, and emit **both trailing and forward** values — so high-growth REIT subsectors (industrial, data center) show forward upside and low-growth subsectors (mall) stay snapshot-close.

The forward FFO scaffold **already exists** at `ffo.go:252–297` and is mathematically complete for FFO. Phase 3 is mostly an **extension + correctness/coverage hardening**, not a re-architecture. The single substantive code addition is: **make the forward base metric track the Phase-2 FFO-vs-AFFO headline selection** (today the scaffold always projects `ffoPerShare`).

### In scope
1. Forward base metric follows the headline FFO-vs-AFFO selection (project AFFO/share when AFFO available, else FFO/share).
2. Confirm/curate REIT profile `HorizonYears` + `TerminalMultiple` so PLD ≈ 5y and SPG ≈ 2–3y (small config edit; see §5).
3. Confirm cost-of-equity discount (already correct in scaffold) and pin it with a test.
4. Per-year growth from the engine growth curve via `ProjectedGrowthRates` (Option A — already what the scaffold does). Keep Option A.
5. Tests: PLD 15–30% higher, SPG ±10%, both-emitted, cost-of-equity-pinned, nil-profile no-op, DDM bit-for-bit, ≥90% coverage on `ffo.go`.
6. `TerminalMultiple` validation guard in `profile/validation.go` (currently unbounded — REIT terminal multiples are 10–31× and would silently fail-open).
7. CalculationVersion coordination with Phase 2 (see §9).

### Non-goals (deferred per spec "Out of scope")
- **Option B** — a dedicated FFO/AFFO growth series (Yahoo/SEC FFO history). Stay on Option A (revenue-growth proxy); document the divergence risk in a warning.
- mREITs (mortgage REITs) — different model entirely; should not route to FFO.
- International REIT analogs (J-REITs, A-REITs).
- Triple-net vs gross lease subclassification; per-property NAV; implied-cap-rate cross-check.
- Surfacing forward/trailing as **new** `FairValueResponse` fields (see §7 decision D3 — recommended deferred unless a consumer needs it).

---

## 2. Gap analysis — what the scaffold already does vs. what Phase 3 adds

### What `ffo.go:252–297` ALREADY does (verified)
- Gates the forward path on `input.Profile != nil && input.Profile.HorizonYears > 0` (L265). Nil/zero-horizon ⇒ `ForwardValue=0`, `HorizonSelected=0`, `TerminalMultiple=0` ⇒ JSON-omitempty preserves legacy shape. **This is the load-bearing nil-safety invariant — keep it intact.**
- Reads `input.GrowthEstimate.ProjectedGrowthRates` (Option A revenue-growth proxy, L268–270).
- Requires `len(rates) >= HorizonYears && CostOfEquity > 0` (L271).
- Compounds `ffoPerShare` forward over the horizon (L274–277).
- Applies `Profile.TerminalMultiple` (L280).
- Discounts at `input.CostOfEquity` via `math.Pow(1+CostOfEquity, HorizonYears)` (L282–284) — **already cost-of-equity, NOT WACC. Issue 4 already satisfied.**
- Populates `TrailingValue`/`ForwardValue`/`HorizonSelected`/`TerminalMultiple` on `ModelResult` (L367–372).
- Emits a forward audit warning (L293–295).

### What Phase 3 must ADD / CHANGE
| # | Gap | Where | Effort |
|---|-----|-------|--------|
| G1 | Forward projects `ffoPerShare` unconditionally; must project the **headline base** (AFFO/share when AFFO available, else FFO/share) to stay consistent with Phase 2's trailing headline. | `ffo.go` forward block | Small — change the base variable; depends on Phase 2's `affoPerShare` local. |
| G2 | REIT profiles need correct `HorizonYears`/`TerminalMultiple` so PLD≈5y, SPG≈2–3y. Industrial already has a `high_growth` 5y variant; residential/retail are 3y. Verify routing actually selects them. | `config/assumption_profiles.json` (+ resolver behavior) | Small — config only, possibly zero change (see §5). |
| G3 | `TerminalMultiple` has **no validation range** (`validation.go` validates `HorizonYears`, `CompoundGrowthCap`, reinvestment fields, but not `TerminalMultiple`). A typo'd 0 or negative would silently zero the forward value. Add a guard. | `profile/validation.go` | Small. |
| G4 | Coverage + acceptance tests for PLD/SPG behavior, cost-of-equity pinning, AFFO-forward selection, ≥90% on `ffo.go`. | `ffo_forward_test.go` | Medium (the bulk of the work). |
| G5 | Warning text should name the base metric (FFO vs AFFO) for operator transparency. | `ffo.go` warning | Trivial. |

### What Phase 3 explicitly does NOT need (verified, call out to avoid wasted work)
- **No `service.go` change** — `CostOfEquity` (L1948), `GrowthEstimate` (L1945), `Profile` (L1966) are already on `ModelInput` for the FFO path. Confirmed by reading `performAlternativeValuation`.
- **No `diff.go` change** — `ForwardValue`/`TrailingValue`/`HorizonSelected`/`TerminalMultiple` live on `ModelResult`, not `FairValueResponse`. They are NOT projected into the response today. Phase 3 only populates existing `ModelResult` fields + changes the headline `IntrinsicValuePerShare`. **No new `FairValueResponse` field ⇒ no `goFieldToJSON`/`countFairValueFields` edit** — UNLESS decision D3 (§7) chooses to surface forward/trailing, which is recommended deferred.
- **No `router.go` change** — `ModelResult` already carries the four forward fields.

---

## 3. File-by-file change list (commit ladder order)

| Order | File | Change | Gate |
|-------|------|--------|------|
| C0 (pre) | — | Confirm Phase 2 (#15) merged into branch: `entities.FinancialData.MaintenanceCapEx` exists, `ffo.go` computes `affoPerShare`, headline selects AFFO-when-available. | Build green. |
| C1 | `profile/validation.go` | Add `TerminalMultiple` range guard (`> 0` when `HorizonYears > 0`; upper sanity bound e.g. `<= 50`). | Unit test in `validation_test.go`. |
| C2 | `internal/services/valuation/models/ffo.go` | G1+G5: forward block projects the **headline base** (`forwardBase := affoPerShare` when AFFO available else `ffoPerShare`); warning names the base. Strictly additive — gating unchanged. | `ffo_forward_test.go` green; nil-profile pins still pass. |
| C3 | `config/assumption_profiles.json` | G2: ONLY IF §5 analysis shows PLD/SPG don't land on the right horizon today. Adjust horizons/terminal multiples (+ bump `config_version`). | Profile load-validation tests + Tier 2 pins. |
| C4 | `internal/services/valuation/models/ffo_forward_test.go` | G4: add PLD-15–30%, SPG-±10%, cost-of-equity-pin, AFFO-forward-selection, both-emitted tests; coverage ≥90%. | `go test ./internal/services/valuation/models/... -cover`. |
| C5 | `CLAUDE.md` + spec checklist | Tick Phase 3 acceptance boxes; document CalcVersion coordination. | docs-only. |

Each commit must keep the full suite green (`go test ./... -count=1`), especially `TestDDM_LegacyPath_BitForBit`, recompute-shadow byte-identity, and ledger-basket.

---

## 4. Forward-base-metric selection rule (exact)

Phase 2 establishes the trailing headline: `IntrinsicValuePerShare = pAffoValuePerShare` when AFFO is available, else `pFfoValuePerShare`. Phase 3 **must mirror that selection** for the forward projection so trailing and forward use the same base.

```
# pseudocode — extends ffo.go forward block (do NOT write production code here; this is contract intent)

affoAvailable := <Phase 2 signal: MaintenanceCapEx resolved / affoPerShare computed and > 0>

# headline base — SAME selection Phase 2 uses for the trailing headline:
if affoAvailable:
    headlineBasePerShare := affoPerShare      # Phase 2 local
    baseLabel := "AFFO"
else:
    headlineBasePerShare := ffoPerShare
    baseLabel := "FFO"

trailingValue := headlineBasePerShare * pffoMultiple   # = the Phase 2 headline value

# forward path (gated, unchanged gating):
if Profile != nil and Profile.HorizonYears > 0
   and len(rates) >= HorizonYears and CostOfEquity > 0:
       forwardBase := headlineBasePerShare           # <-- G1: was always ffoPerShare
       for i in 0..HorizonYears:
           forwardBase *= 1 + rates[i]
       forwardPreDiscount := forwardBase * Profile.TerminalMultiple
       forwardValue := forwardPreDiscount / pow(1+CostOfEquity, HorizonYears)
       if forwardValue < 0: forwardValue = 0
       warnings += "VAL-3 P3 forward %s: %dy at avg %.1f%% growth, terminal %.1fx" % (baseLabel, ...)
```

**Critical consistency rule:** `trailingValue` and `forwardValue` must derive from the **same** `headlineBasePerShare`. When AFFO is available, BOTH use AFFO/share; when not, BOTH use FFO/share. Never mix (e.g. AFFO trailing + FFO forward) — that would make the trailing↔forward divergence uninterpretable.

**Multiple consistency note:** the trailing leg uses the spot P/FFO subsector multiple (`m.getMultiple`); the forward leg uses `Profile.TerminalMultiple`. These are intentionally distinct knobs (spot vs terminal). The spec treats the terminal multiple as a profile calibration, not the spot table value — keep them separate. (Acceptance for SPG ±10% and PLD 15–30% is what calibrates whether the chosen terminal multiples are right; see §6.)

**Negative/zero AFFO edge:** if `affoAvailable` but `affoPerShare <= 0`, fall back to FFO base for BOTH legs and append the existing negative-metric warning. (Phase 2 likely already handles the trailing fallback — Phase 3 forward must respect the same fallback so it never projects a negative base into a positive terminal multiple.)

---

## 5. Profile / config changes

### Current REIT profile state (verified from `config/assumption_profiles.json`)
| Subsector profile | `horizon_years` | `terminal_multiple` | `discount_method` |
|---|---|---|---|
| `reit_residential:standard_growth` | 3 | 20.0 | cost_of_equity |
| `reit_commercial:standard_growth` | 3 | 14.0 | cost_of_equity |
| `reit_industrial:standard_growth` | 3 | 22.5 | cost_of_equity |
| `reit_industrial:high_growth` | **5** | 24.0 | cost_of_equity |
| `reit_healthcare:standard_growth` | 3 | 17.5 | cost_of_equity |
| `reit_datacenter:standard_growth` | 4 | 25.0 | cost_of_equity |
| `reit_datacenter:high_growth` | **5** | 28.0 | cost_of_equity |
| `reit_celltower:standard_growth` | 4 | 22.0 | cost_of_equity |
| `reit_celltower:high_growth` | 5 | 25.0 | cost_of_equity |
| `reit_retail:standard_growth` | 3 | 10.0 | cost_of_equity |
| `reit_specialty:standard_growth` | 3 | 17.5 | cost_of_equity |

All REIT profiles already declare `discount_method: cost_of_equity` (consistent with Issue 4). `terminal_method: exit_multiple`.

### Routing reality (verified)
- Archetype rules (lines ~663–730) map `REIT_INDUSTRIAL → reit_industrial`, `REIT_RETAIL → reit_retail`, etc. (priority 100).
- **PLD** classifies as `REIT_INDUSTRIAL`. To get the **5y** horizon it must resolve to `reit_industrial:high_growth`, which the resolver selects via the **maturity bucket** (`high_growth` vs `standard_growth`) driven by revenue-growth / size thresholds in `maturity_thresholds_fallback` (`high_growth_revenue_yoy_min: 0.30`). **Verify in C0/C3 that PLD's fixture actually buckets to `high_growth`** — if it buckets to `standard_growth`, it lands on the 3y industrial profile and the PLD-5y acceptance test must either (a) seed a fixture whose growth signal forces `high_growth`, or (b) the test constructs the profile explicitly (as the existing `ffo_forward_test.go` tests do — they set `input.Profile` directly).
- **SPG** classifies as `REIT_RETAIL → reit_retail:standard_growth` → **3y**, terminal 10.0. The spec's acceptance says SPG horizon **2y**. There is **no `reit_retail` 2y profile today**.

### Decision on config edits (see §9 open questions)
**Recommended minimal change (option G2-min):**
- **Do NOT add new profiles.** For the unit/acceptance tests, construct `input.Profile` explicitly (the existing forward tests already do this — `ffo_forward_test.go:43–52`). This decouples the math validation from classifier/maturity-bucketing flakiness and is the established pattern in this file.
- **SPG 2y vs 3y:** the spec says "horizon 2y (low-growth subsector)". The shipped `reit_retail` profile is 3y. Recommendation: **keep 3y in config** (3y is a defensible low-growth horizon and avoids a config churn + Tier-2-pin re-capture), and **write the SPG acceptance test against a 2y OR 3y explicit profile** asserting `forward ≈ trailing ±10%`. The ±10% band is the real acceptance signal; horizon 2 vs 3 at low growth both satisfy it. If product insists on exactly 2y, add a `reit_retail:low_growth` profile (horizon 2, terminal 10.0) + an archetype-maturity pin — documented as decision D1.

**If C3 config edit is taken:** bump `config_version` in `assumption_profiles.json`, re-run profile load-validation tests, and re-capture any Tier-2 numeric pins that key off REIT profiles (`profile/tier2_pin_inputs_test.go`, `tier2_regression_test.go`, `pins.go`). Verify `TestTier2_EQIX_Pin`/`TestTier2_PLD_Pin` still hold or are deliberately re-pinned.

### Validation guard (C1, `profile/validation.go`)
Add to `validateProfile` (alongside the existing `HorizonYears` check at L92–96):
```
if p.HorizonYears > 0 && p.TerminalMultiple <= 0 {
    return error "profile %s: terminal_multiple must be > 0 when horizon_years > 0"
}
if p.TerminalMultiple > 50 {  // sanity ceiling — REIT terminal multiples top out ~31x
    return error "profile %s: terminal_multiple out of sane range (0,50]: %v"
}
```
This closes the silent fail-open where a 0/negative terminal multiple zeroes the forward leg with no error. Confirm every shipped REIT profile passes (all are 10.0–28.0 ✓).

---

## 6. Test plan (TDD) — name each function and map to acceptance

All tests in `internal/services/valuation/models/ffo_forward_test.go` (external `models_test` package, reusing `profile/testhelpers` fixtures). Follow the existing pattern: build a synthetic REIT input, set `input.Profile` explicitly, call `ffo.Calculate`.

| Test function | Acceptance criterion | Assertion |
|---|---|---|
| `TestFFO_Forward_IndustrialREIT_PLD_ForwardHigherThanSnapshot` | PLD: forward 5y with growth curve, per-share **15–30% higher** than snapshot. | Build PLD-style industrial input with `ProjectedGrowthRates` ≈ industrial high-growth (e.g. ~8–12%/yr), `Profile{HorizonYears:5, TerminalMultiple:24.0, DiscountMethod:CostOfEquity}`, positive `CostOfEquity`. Assert `ForwardValue/TrailingValue ∈ [1.15, 1.30]`. Tune fixture growth + CoE so the band is hit (this calibrates the assumption set). |
| `TestFFO_Forward_MallREIT_SPG_ForwardNearSnapshot` | SPG: low-growth subsector, forward **≈ snapshot ±10%**. | Build SPG-style retail input with low `ProjectedGrowthRates` (~1–2%/yr), `Profile{HorizonYears:3 (or 2), TerminalMultiple:10.0, DiscountMethod:CostOfEquity}`. Assert `abs(ForwardValue/TrailingValue - 1) <= 0.10`. |
| `TestFFO_Forward_BothLegsEmitted_Divergence` | All REITs: trailing AND forward both emitted, divergence visible. | Assert `TrailingValue > 0 && ForwardValue > 0 && TrailingValue != ForwardValue && HorizonSelected == Profile.HorizonYears && TerminalMultiple == Profile.TerminalMultiple`. (Extends existing `TestFFO_Forward_DataCenterREIT_PopulatesForwardLeg`.) |
| `TestFFO_Forward_DiscountsAtCostOfEquity_NotWACC` | Issue 4 — discount is cost of equity, NOT WACC. | Set `CostOfEquity` and a DIFFERENT `WACC` on the input. Compute the expected forward value by hand using `pow(1+CostOfEquity, H)` and assert `InEpsilon(expected, ForwardValue, 1e-9)`. A second sub-assert: changing `WACC` only (CoE fixed) leaves `ForwardValue` byte-identical (WACC must not influence the forward leg). |
| `TestFFO_Forward_ProjectsAFFOWhenAvailable` | Phase 3 forward base follows headline FFO-vs-AFFO selection. | Build input with `MaintenanceCapEx` set so AFFO < FFO. Assert the forward value equals projecting **AFFO/share** (not FFO/share) — compute both by hand, assert forward matches the AFFO projection within epsilon and is strictly below the FFO projection. |
| `TestFFO_Forward_FallsBackToFFOWhenNoAFFO` | When AFFO unavailable, forward projects FFO/share (no regression). | Build input with no `MaintenanceCapEx`; assert forward == FFO/share projection. |
| `TestFFO_Forward_NegativeAFFO_FallsBackToFFOBase` | Edge: AFFO available but ≤0 ⇒ both legs fall back to FFO base. | Build input where AFFO ≤ 0; assert trailing and forward both use FFO base + warning present. |
| `TestFFO_NilProfile_FallsThroughToTrailing` (EXISTING) | Nil profile ⇒ ForwardValue zero (omitempty legacy shape). | Already present — keep passing. |
| `TestFFO_ProfileHorizonZero_BehavesLikeNoProfile` (EXISTING) | HorizonYears==0 ⇒ no forward path. | Already present — keep passing. |
| `TestFFO_Forward_InsufficientGrowthRates_NoForward` | `len(rates) < HorizonYears` ⇒ forward stays zero (no panic, no partial projection). | Build input with `HorizonYears:5` but only 2 `ProjectedGrowthRates`; assert `ForwardValue==0`, `HorizonSelected==0`. |
| `TestTerminalMultiple_Validation_*` (in `profile/validation_test.go`) | C1 guard. | Table-driven: `TerminalMultiple` of 0/negative with `HorizonYears>0` ⇒ error; valid 10–31 ⇒ ok; >50 ⇒ error. |

**Cross-model regression (run, don't author new):**
- `TestDDM_LegacyPath_BitForBit` — must stay GREEN (FFO change cannot touch DDM, but run it).
- Full `go test ./... -count=1` — recompute-shadow byte-identity + ledger-basket.
- `go test ./internal/services/valuation/models/ -cover` — assert `ffo.go` ≥90%.

---

## 7. Invariant checklist (MUST hold at every commit)

- [ ] **Nil/zero-horizon ⇒ `ForwardValue/HorizonSelected/TerminalMultiple` stay zero** → JSON-omitempty keeps legacy response shape. Gating at `ffo.go:265` unchanged. Pinned by the two existing nil-profile tests.
- [ ] **Forward path strictly additive/gated** — when the gate is open the headline `IntrinsicValuePerShare` may change (intended REIT value change), but the legacy-shape fields stay omitempty when the gate is closed.
- [ ] **Cost-of-equity discount, NOT WACC** — already true (`ffo.go:282–284`); pinned by `TestFFO_Forward_DiscountsAtCostOfEquity_NotWACC`.
- [ ] **DDM bit-for-bit** (`TestDDM_LegacyPath_BitForBit`) GREEN — Phase 3 touches only `ffo.go` + config + `validation.go`; no DDM path.
- [ ] **recompute-shadow byte-identity** (`git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0) — Phase 3 is valuation-model-only; cleaner untouched.
- [ ] **ledger-basket** (`TestLedger_BasketSnapshot_*`) GREEN — no datacleaner change.
- [ ] **diff.go field-count guard** — NO new `FairValueResponse` field added (see D3) ⇒ `countFairValueFields`/`goFieldToJSON` UNCHANGED. If D3 flips to surfacing forward/trailing, register BOTH and bump the count, or the `init()` guard panics every replay test.
- [ ] **Profile load-validation** — all shipped REIT profiles pass the new `TerminalMultiple` guard.
- [ ] **Coverage** ≥90% on `ffo.go`.
- [ ] **CalculationVersion** bumped exactly once, coordinated with Phase 2 (see §9 / D2).

---

## 8. Commit ladder

1. **C1 — validation guard.** `profile/validation.go` + `validation_test.go`: `TerminalMultiple` range. Green: profile package tests.
2. **C2 — forward base selection.** `ffo.go`: project headline base (AFFO-or-FFO) in the forward block; warning names base; negative-AFFO fallback. Green: `ffo_forward_test.go` (existing) + nil-profile pins.
3. **C3 — config (CONDITIONAL).** `assumption_profiles.json`: only if §5/D1 demands a `reit_retail:low_growth` 2y profile; bump `config_version`; re-capture Tier-2 pins. Green: profile config tests + Tier-2 pins.
4. **C4 — acceptance tests.** All new `ffo_forward_test.go` functions (§6). Green: package tests + `-cover ≥90%`.
5. **C5 — CalcVersion + docs.** Bump `CalculationVersion` (coordinated with Phase 2 — single increment if landing together); tick spec Phase 3 checklist; CLAUDE.md gotcha bullet. Green: full `go test ./... -count=1`.

After C5: run `/verify` (live API on a REIT ticker), confirm `calculation_version` reflects the bump, and confirm forward/trailing divergence in logs.

---

## 9. Open questions / decisions (with recommendations)

**D1 — SPG horizon: 2y (spec literal) vs 3y (shipped `reit_retail`)?**
- *Recommendation:* Keep config at 3y; write the SPG acceptance test against an explicit `Profile{HorizonYears:3, TerminalMultiple:10.0}` (or 2y) and assert the real signal — `forward ≈ trailing ±10%`. Both 2y and 3y at ~1–2% growth satisfy ±10%. Add a dedicated `reit_retail:low_growth` 2y profile only if product mandates exactly 2y (then C3 + Tier-2 pin re-capture applies). Avoids config churn and pin re-capture for no behavioral gain.

**D2 — CalculationVersion coordination with Phase 2.**
- Phase 2 (AFFO headline) is itself a production value change → it bumps CalcVersion. Phase 3 (forward projection changing the REIT headline) is another value change.
- *Recommendation:* If Phase 2 and Phase 3 land in the **same branch/release** (they will — Phase 3 is built on Phase 2 in this worktree), bump CalcVersion **once** for the combined VAL-3 Phase 2+3 REIT change. The current engine version is `4.7` (per CLAUDE.md). Recommend `4.8` for the combined VAL-3 P2+P3 REIT upgrade. Confirm with Phase 2's plan which exact bump it claims and do NOT double-bump. Update all stamp sites + the `service_test.go` version pins in one commit (C5).

**D3 — Surface forward/trailing/horizon in `FairValueResponse`?**
- Today `ForwardValue`/`TrailingValue`/`HorizonSelected`/`TerminalMultiple` live on `ModelResult` and are consumed internally (headline = `IntrinsicValuePerShare`); they do NOT reach `FairValueResponse`.
- *Recommendation:* **Defer.** Keep Phase 3 response-shape-neutral (only the headline value moves, via existing `pffo_value_per_share`/`paffo_value_per_share` from Phase 2). This avoids a `diff.go` `goFieldToJSON`/`countFairValueFields` change and the replay field-count guard churn. If a consumer (orchestrator/dashboard) later needs the forward/trailing split, that is a small additive follow-up: add `ffo_forward_value_per_share` + `ffo_trailing_value_per_share` to `FairValueResponse`, register BOTH in `diff.go`, bump `countFairValueFields`. Flag as VAL-3 P3.1.

**D4 — Option A (revenue-growth proxy) accuracy for heavy-acquisition REIT years.**
- *Recommendation:* Keep Option A (the scaffold's approach). It is acceptable per spec ("A initially, B later"). Ensure the forward warning makes the proxy explicit (e.g. "forward AFFO uses revenue-growth proxy; heavy-acquisition years may diverge"). Option B (dedicated FFO series) stays a documented non-goal.

**D5 — PLD maturity bucketing (`high_growth` vs `standard_growth`).**
- The 5y horizon requires `reit_industrial:high_growth`. Whether a live PLD classifies into that bucket depends on the resolver's revenue-growth/size thresholds.
- *Recommendation:* Validate the math with an explicit `Profile` in the unit test (decoupled from bucketing). Separately, in C0/C3, run a live/fixture PLD valuation and confirm it resolves to `high_growth`; if it lands on `standard_growth` (3y), that is a resolver-tuning question tracked as VAL-3 P3.2, not a blocker for the forward-projection math itself.

---

## 10. Acceptance criteria (closeout)

- [ ] Forward projects AFFO/share when AFFO available, else FFO/share; trailing and forward use the SAME base.
- [ ] PLD-style industrial REIT: forward 5y, per-share 15–30% higher than snapshot (`TestFFO_Forward_IndustrialREIT_PLD_ForwardHigherThanSnapshot`).
- [ ] SPG-style mall REIT: forward ≈ snapshot ±10% (`TestFFO_Forward_MallREIT_SPG_ForwardNearSnapshot`).
- [ ] All REITs: trailing AND forward both emitted, divergence visible (`TestFFO_Forward_BothLegsEmitted_Divergence`).
- [ ] Discount is cost of equity, NOT WACC, pinned (`TestFFO_Forward_DiscountsAtCostOfEquity_NotWACC`).
- [ ] Nil/zero-horizon profile ⇒ ForwardValue zero, legacy response shape (existing pins green).
- [ ] `TerminalMultiple` validation guard added + tested.
- [ ] No cross-model regressions: DDM bit-for-bit, recompute-shadow, ledger-basket all GREEN.
- [ ] `ffo.go` coverage ≥90%.
- [ ] CalculationVersion bumped once (coordinated with Phase 2); `service_test.go` version pins updated.
- [ ] Spec Phase 3 checklist + CLAUDE.md updated.

---

## Tasks by agent
- **BACKEND:** C1 (validation guard), C2 (forward base selection), C3 (conditional config), C5 (CalcVersion + docs). Keep the change confined to `ffo.go`, `validation.go`, and (conditionally) config.
- **QA:** Validate PLD-15–30%, SPG-±10%, both-emitted, cost-of-equity, AFFO-forward selection; run full suite for cross-model regressions; confirm live `calculation_version` post-bump.
- **REVIEWER:** Verify the nil/zero-horizon gate is untouched; confirm trailing↔forward base consistency; confirm WACC cannot leak into the forward leg; confirm no `FairValueResponse`/`diff.go` change (or correct registration if D3 flips); confirm DDM/cleaner paths untouched.

**HANDOFF_TO: BACKEND** (after Phase 2 / #15 merges into the branch).
