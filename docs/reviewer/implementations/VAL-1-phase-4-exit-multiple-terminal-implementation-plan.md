# VAL-1 Phase 4 — Exit-multiple terminal as an alternative to Gordon (implementation plan)

MODE: PLAN_AND_CREATE
ROLE: ARCH

**Branch:** `val-1-dcf-archetype-phases` · **Worktree:** `.claude/worktrees/val-1-dcf-phases`
**Engine at plan time:** `CalculationVersion 4.8` · **Status:** PLAN ONLY (no production code written)
**Tracker:** `docs/reviewer/spec/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md` §Phase 4
**Handoff:** `docs/refactoring/implementations/A1-val-1-archetype-horizon-handoff.md`

---

# Summary

Phase 4 makes the resolved `AssumptionProfile`'s `terminal_method: exit_multiple` actually
drive the DCF terminal value. Today (verified in code) the request-valuation-overrides
feature already ships a `terminal_method` selector and 17 profiles already *declare*
`terminal_method: exit_multiple` — but the engine only ever applies an exit multiple when a
**request override** touches `terminal_method`/`terminal_multiple` (`service.go:1326`). A
profile-sourced `exit_multiple` is deliberately ignored, producing a latent inconsistency:
those tickers report `dcf_terminal_method: "exit_multiple"` in the diagnostic field while the
math runs pure Gordon. Phase 4 closes that gap: the profile drives the exit-multiple terminal
through the existing `params` precedence path, BOTH terminal estimates are emitted, and the
behavior is reconciled with the documented 50/50 blend so we do not silently break the request
override contract. The change is DCF-path-only; DDM/FFO/revenue_multiple stay bit-for-bit, and
tickers whose *resolved* `terminal_method` is unchanged from today stay byte-identical.

---

# Analysis (file:line evidence; reconciliation with the existing 50/50 blend selector)

## What already exists (do NOT contradict)

1. **Engine exit-multiple math + 50/50 blend** — `pkg/finance/dcf/dcf.go:347-371`.
   When `inputs.ExitMultiple > 0`, the engine computes
   `terminalEBITDA = finalYearProjection.OperatingIncome + scaledDA` (D&A scaled by
   `terminalOI / BaseOperatingIncome`), then `exitMultipleTV = terminalEBITDA × ExitMultiple`,
   then **averages** it 50/50 with Gordon TV: `TerminalValueNominal = (gordonTV + exitMultipleTV)/2`
   (`dcf.go:366`). It persists `ExitMultipleTV` (raw component, `dcf.go:138`) and `GordonTV`
   (`dcf.go:147`). This is an **EV/EBITDA** multiple basis, NOT EV/Revenue.

2. **`terminal_method` selector semantics (request-override contract)** — documented in
   CLAUDE.md and implemented at `service.go:1313-1340`:
   - `"gordon_growth"` → `exitMultiple = 0` → SUPPRESS blending (pure Gordon TV).
   - `"exit_multiple"` → use `p.TerminalMultiple` → engine BLENDS 50/50 (not pure exit-multiple).
   - This branch fires **only when** `overrides.TerminalMultiple != nil || overrides.TerminalMethod != nil`
     (`service.go:1326`). Otherwise `exitMultiple = industryExitMultiple` (`service.go:1325`), the
     legacy unconditional EV/EBITDA-industry-lookup blend.

3. **Resolver already plumbs method + multiple through precedence** —
   `params/resolve.go:153-181`: `TerminalMethod` resolves const-default ← profile ← override;
   `TerminalMultiple` resolves industry-lookup ← profile ← override; and
   `resolve.go:237-243` already returns a typed 422 when `method == "exit_multiple"` and no
   multiple is resolvable. So the precedence machinery Phase 4 needs **already exists** — the
   missing piece is purely the **service-layer gate** that decides whether the profile (not just
   the request) is allowed to switch on the exit-multiple path.

4. **Profile config already declares exit_multiple** — `config/assumption_profiles.json`
   (`config_version 1.3.1` is the multiples file; the profiles file is a separate version): 17
   profiles carry `terminal_method: "exit_multiple"` with `terminal_multiple` values 3.0/5.0/6.0
   (growth/biotech) and up to 25.0 (REIT datacenter). Examples:
   `cyclical_mid_cycle:high_growth` (3.0, also `reinvestment_method: sales_to_capital`),
   `hypergrowth_early:high_growth` (5.0, sales_to_capital), `pre_revenue_biotech:high_growth` (6.0),
   `reit_datacenter:standard_growth` (25.0). These magnitudes are clearly **EV/EBITDA**, matching
   the engine's existing basis, NOT EV/Revenue.

5. **Diagnostic field already reflects the resolved method** — `service.go:1260`
   `terminalMethodLabel := p.TerminalMethod`; `service.go:1529`
   `result.DCFTerminalMethod = terminalMethodLabel`. So `dcf_terminal_method` ALREADY says
   `"exit_multiple"` for those 17 profiles even though the engine ignores it today. **This is the
   load-bearing inconsistency Phase 4 resolves** — after Phase 4 the label and the math agree.

6. **The tracked follow-up is explicit** — `service.go:1321-1324`:
   > "whether a profile-sourced terminal_method=exit_multiple should drive the DCF averaging
   > WITHOUT a request override is a deliberate behavior-change decision, intentionally OUT OF
   > SCOPE here to preserve byte-identity."
   **Phase 4 IS that decision.** This plan is the sanctioned place to flip it.

## Reconciliation verdict (the CRITICAL note)

The prompt asks whether Phase 4 is (a) already partly implemented via the blend, (b) needs the
profile to drive it, and/or (c) needs the diagnostic to reflect the resolved choice. Answer:

- **(a) Partly, yes.** The engine blend + the request-override selector already exist and are
  load-bearing. Phase 4 does NOT rewrite the blend math.
- **(b) Yes — this is the core of Phase 4.** The profile must drive the exit-multiple path via
  the *same* `params` precedence chain the request override uses. No parallel code path.
- **(c) The diagnostic already reflects the resolved label; Phase 4 makes the MATH match it**,
  and adds a second field to emit BOTH terminal values (spec requirement: "emit both").

**Decision on the 50/50 blend (explicitly justified):** The request-override semantics for
`exit_multiple` (= 50/50 blend) are PRESERVED unchanged. We do NOT change `exit_multiple` to mean
"pure exit-multiple TV" — that would break the documented contract and the
`TestPostFairValue` override tests. The profile-driven path reuses the SAME engine blend. The
spec's phrasing ("emit both; primary chosen by profile") is satisfied by the blend (which is the
project's chosen "primary": an averaged TV) PLUS emitting both raw components
(`gordon_terminal_value`, `exit_multiple_terminal_value`) as diagnostics so consumers can see the
two estimates separately. Rationale: the project already committed to "averaging reduces single-
model dependency" (`dcf.go:347-348`) and treats the 50/50 average as the defensible primary;
Damodaran uses both and the average is a conservative reconciliation. A future VAL-1.1 may add a
profile flag for "pure exit-multiple primary," but that is out of scope and would be a separate,
reviewed behavior change.

**Decision on EV/EBITDA vs EV/Revenue (explicitly justified):** The spec §Phase 4 text says
`year_N_revenue × mature_sector_EV/Revenue`. The SHIPPED engine and the SHIPPED profile
`terminal_multiple` values are **EV/EBITDA**. Switching the basis to EV/Revenue would (i) require
re-authoring all 17 profile multiples, (ii) change `ExitMultipleTV` semantics for the existing
request-override path (a behavior change to a shipped contract), and (iii) diverge from the
`terminal_value` calc-trace + `exit_multiple_tv` field consumers already key off. **Recommended:
KEEP the EV/EBITDA basis** (the engine's existing, contract-stable definition) and document the
spec wording as superseded — the engine multiple is `EV/EBITDA`, applied to terminal EBITDA.
Option B (EV/Revenue) is presented under Architecture tradeoffs but NOT recommended. This keeps
Phase 4 additive and the request-override path byte-identical.

## Byte-identity reasoning

Today, for the 17 exit-multiple profiles with NO request override: `exitMultiple = industryExitMultiple`
(`service.go:1325`) and the blend uses the *industry* EV/EBITDA lookup (e.g. TECH 18), NOT the
profile's `terminal_multiple` (e.g. 5). So those tickers TODAY already get a 50/50 blend with the
*industry* multiple. **This means most exit-multiple profiles are NOT pure-Gordon today** — they
are already blended with the industry multiple. Phase 4 changes WHICH multiple is used (profile's
`terminal_multiple` instead of industry default) for those profiles → this IS a value change for
them, and is exactly the calibration Phase 4 intends. Tickers whose resolved profile is
`gordon_growth` (or that resolve to no profile) keep `exitMultiple = industryExitMultiple`
unchanged → byte-identical. See "Tests / unchanged-method byte-identity" for the precise pinning.

---

# Plan

## Requirements

- R1. When the resolved `AssumptionProfile.TerminalMethod == "exit_multiple"`, the DCF terminal
  value uses an exit-multiple component (blended 50/50 with Gordon, per the preserved contract),
  using the **profile-resolved** `TerminalMultiple` (`p.TerminalMultiple`), NOT the raw industry
  default — driven through `params` precedence, no parallel path.
- R2. When the resolved `TerminalMethod == "gordon_growth"` (or no profile), behavior is
  byte-identical to today: `exitMultiple = industryExitMultiple` (the legacy unconditional blend).
- R3. Emit BOTH terminal estimates as diagnostics: the Gordon component and the exit-multiple
  component, plus the existing `dcf_terminal_method` (already resolved) and
  `dcf_terminal_pct_of_ev`. Add exactly ONE new `FairValueResponse` field for the second value
  (see API Contracts) — the Gordon component reuses existing `GordonTV`/`ExitMultipleTV` engine
  fields surfaced to the response.
- R4. The DCF-implied-multiple sanity crosscheck (`crosscheck.go`) must remain coherent: when the
  terminal blends an exit multiple, the implied EV/EBITDA the crosscheck reports must not falsely
  flag divergence *because* we used a sector multiple in the terminal. Document and test the
  interaction (see Tests).
- R5. The request-override `terminal_method`/`terminal_multiple` contract (50/50 blend semantics,
  gordon-suppress) is UNCHANGED.
- R6. CalcVersion bump policy: this is a production value change for the 17 exit-multiple profiles
  → bump `CalculationVersion 4.8 → 4.9` at BOTH stamp sites + all `service_test.go` pins + API-doc
  examples. Coordinate with sibling phases (see Shared-Contract Touchpoints) so only ONE bump
  lands per merge to master.
- R7. ≥90% coverage on new finance/service code (CLAUDE.md finance-module standard).

## Architecture + tradeoffs

### The single seam: `service.go:1325-1340` (the `exitMultiple` decision)

Today:
```
exitMultiple := industryExitMultiple
if overrides.TerminalMultiple != nil || overrides.TerminalMethod != nil {
    if terminalMethodLabel == gordon_growth { exitMultiple = 0 }
    else { exitMultiple = p.TerminalMultiple }
}
```

Phase 4 generalizes the gate so the **resolved method** drives it regardless of whether the
request or the profile supplied it. Proposed shape (illustrative pseudocode, BACKEND owns final
form):
```
exitMultiple := industryExitMultiple            // legacy default (R2)
switch terminalMethodLabel {
case gordon_growth:
    // suppress ONLY when the resolved method came from request or profile explicitly.
    // On the pure-default path (no profile, method==default==gordon_growth) keep the
    // legacy industry blend so non-profiled tickers stay byte-identical (R2).
    if methodSourceIsRequestOrProfile { exitMultiple = 0 }
case exit_multiple:
    exitMultiple = p.TerminalMultiple           // profile- or request-resolved (R1)
}
```
The key addition is reading the **provenance** of `terminal_method` from
`p.Provenance[knobTerminalMethod]` (already populated: `SourceDefault|SourceProfile|SourceRequest`,
`resolve.go:154-165`) to distinguish "default gordon" (legacy industry blend, byte-identical) from
"profile/request gordon" (explicit suppress). This is the minimal change that (a) honors the
profile, (b) preserves byte-identity for non-profiled tickers, (c) preserves the request contract.

**Tradeoff — provenance gate vs. unconditional:** The simplest change would be "if
`terminalMethodLabel == exit_multiple` use `p.TerminalMultiple`, else if `== gordon_growth` set 0."
But that would set `exitMultiple = 0` for EVERY ticker resolving to default `gordon_growth`
(no profile), removing the legacy industry blend and changing values for the entire non-profiled
universe. The provenance gate confines the change to tickers whose method is *explicitly*
profile/request-sourced. RECOMMENDED: provenance gate.

### Terminal-multiple basis (Option A recommended)

- **Option A (RECOMMENDED): keep EV/EBITDA.** Reuse the engine's existing
  `terminalEBITDA × ExitMultiple` block (`dcf.go:347-371`) verbatim. Profile `terminal_multiple`
  values are already EV/EBITDA-scaled. Zero engine math change; only the service gate changes.
  Pros: additive, request path byte-identical, no profile re-authoring, crosscheck already speaks
  EV/EBITDA. Cons: spec wording (EV/Revenue) is superseded → must update the tracker text.
- **Option B (NOT recommended): EV/Revenue.** Add a new engine branch using terminal revenue ×
  EV/Revenue multiple. Requires new `dcf.Inputs` field, re-authoring 17 profile multiples to
  EV/Revenue scale, and changes the meaning of `ExitMultipleTV` for the shipped request-override
  path (contract break). Defer to VAL-1.1 if ever needed.

### Reinvestment-path interaction (verified)

On the Layer-A reinvestment path (`useReinv`), `finalYearProjection.OperatingIncome = revenue ×
margin` and `BaseRevenue > 0`. The exit-multiple block reads `finalYearProjection.OperatingIncome`
and scales D&A by `terminalOI / BaseOperatingIncome` (`dcf.go:354-358`). This already works on the
reinvestment path (OI is populated). NOTE for BACKEND: the 5 reinvestment-method profiles that are
ALSO `gordon_growth` (`software_like_*`, `hypergrowth_profitable`) are unaffected (method gate →
gordon). The 2 profiles that are BOTH `sales_to_capital` AND `exit_multiple`
(`cyclical_mid_cycle:high_growth`, `hypergrowth_early:high_growth`) will now blend the profile
multiple into the reinvestment-path terminal — test these explicitly (see Tests).

### Crosscheck interaction (R4)

`crosscheck.go::CalculateSanityCheck` computes `ImpliedEVEBITDA = enterpriseValue / ebitda` and
flags divergence > 2× / < 0.5× sector median via `FlagDivergence`. When the terminal already
blends a sector EV/EBITDA multiple, the resulting EV is partly *defined* by the sector multiple,
so the implied EV/EBITDA is pulled toward the sector median — the crosscheck becomes partially
circular but in the SAFE direction (less likely to false-flag). No code change required, but the
plan REQUIRES a test asserting that an exit-multiple-terminal ticker does not gain a *spurious*
EV/EBITDA divergence flag relative to its Gordon-only counterpart, and a doc note in crosscheck.go
explaining the (benign) circularity. Do NOT attempt to "de-circularize" the crosscheck in Phase 4.

## Files touched (additive/minimal)

| File | Change |
|---|---|
| `internal/services/valuation/service.go` | Generalize the `exitMultiple` gate (`~1325-1340`) to read `p.Provenance[knobTerminalMethod]`; stamp the new "both TV" diagnostic onto `result`. Bump `CalculationVersion` (2 sites). |
| `internal/services/valuation/params/params.go` | (If needed) export a tiny accessor or constant so the service can read the terminal-method provenance without re-deriving (`knobTerminalMethod` is unexported in `params`). Prefer adding `EffectiveValuationParams.TerminalMethodSource Source` set in `resolve.go`, OR exporting a `ProvenanceOf(knob string)` — minimal. |
| `internal/core/entities/valuation.go` | Add ONE field for the second terminal value (e.g. `DCFExitMultipleTerminalValue float64 json:"dcf_exit_multiple_terminal_value,omitempty"`) and reuse `GordonTV` via a `DCFGordonTerminalValue` field. (See API Contracts for exact names + count.) |
| `internal/api/v1/handlers/fair_value.go` | Mirror the new field(s) on `FairValueResponse` + map in `buildFairValueResponse` + swagger annotations. |
| `internal/observability/replay/diff.go` | Register new field(s) in `goFieldToJSON` and bump `countFairValueFields()` (currently `33+5+8=46`). |
| `config/assumption_profiles.json` | NO new profiles needed (17 already exist). Optionally adjust `terminal_multiple` values only if QA/oracle shows a clearly-wrong magnitude — but treat as separate, justified change. Bump `config_version` only if values change. |
| `docs/openapi.yaml` | Add the new property/properties with type, example, omission semantics. |
| `docs/reviewer/spec/VAL-1-...md` | Mark Phase 4 acceptance; record EV/EBITDA-basis decision (supersedes EV/Revenue wording). |
| CLAUDE.md | One gotcha bullet: profile-driven exit-multiple now active; 50/50 blend preserved; EV/EBITDA basis. |
| `cmd/accuracy` baseline + `docs/accuracy/report-<date>.md` | Fresh 4.9 baseline; measure `TERMINAL_DOMINANCE` movement (judge by price-free columns, NOT the gap). |

## Build order

1. Spec + tracker decision (EV/EBITDA basis; 50/50 preserved). [no code]
2. TDD RED: write failing tests pinning (a) profile exit_multiple now blends profile multiple,
   (b) gordon/no-profile byte-identity, (c) both-TV diagnostics present, (d) crosscheck no
   spurious flag. (`superpowers:test-driven-development`.)
3. Add provenance accessor in `params` (+ test).
4. Generalize the `service.go` `exitMultiple` gate; add diagnostic stamping.
5. Add entities + handler fields; register in `replay/diff.go`; bump `countFairValueFields`.
6. CalcVersion 4.8→4.9 (all sites/pins/examples).
7. OpenAPI + CLAUDE.md + tracker.
8. Re-measure with `cmd/accuracy`; capture 4.9 baseline + report.
9. `/execute` B-V-R-Q (VERIFIER+REVIEWER+QA) + `mcp__zen-mcp__codereview` gpt-5.5.

---

# API Contracts

## New `FairValueResponse` fields (additive, omitempty)

Two new fields surface BOTH terminal estimates (R3). `dcf_terminal_method` and
`dcf_terminal_pct_of_ev` already exist (Phase 1) and are unchanged.

| JSON field | Go field | Type | Meaning |
|---|---|---|---|
| `dcf_gordon_terminal_value` | `DCFGordonTerminalValue` | float64 | Nominal Gordon-Growth terminal value (= engine `Result.GordonTV`), before discounting and before any blend. Omitted when 0. |
| `dcf_exit_multiple_terminal_value` | `DCFExitMultipleTerminalValue` | float64 | Nominal exit-multiple terminal value component (= engine `Result.ExitMultipleTV`), before discounting and before blend. Omitted (0) on the pure-Gordon path. |

These come directly from `dcfResult.GordonTV` and `dcfResult.ExitMultipleTV` (both already on
`dcf.Result`) — no engine change. `dcf_terminal_method` already tells the consumer which is
primary/selected; the blended primary remains the existing `enterprise_value`/`dcf_value_per_share`.

Example (exit-multiple profile, e.g. a hypergrowth name):
```json
{
  "dcf_terminal_method": "exit_multiple",
  "dcf_terminal_pct_of_ev": 0.74,
  "dcf_gordon_terminal_value": 1.82e10,
  "dcf_exit_multiple_terminal_value": 2.10e10
}
```
Example (mature gordon profile / non-profiled): both new fields omitted (gordon component is the
TV but we only surface it as a diagnostic when an exit-multiple was *considered* — see open
question Q3 on whether to emit `dcf_gordon_terminal_value` unconditionally).

## Replay field-count guard

`internal/observability/replay/diff.go`: add `"DCFGordonTerminalValue": "dcf_gordon_terminal_value"`
and `"DCFExitMultipleTerminalValue": "dcf_exit_multiple_terminal_value"` to `goFieldToJSON`, and
bump `countFairValueFields()` from `33+5+8` to `35+5+8` (FairValueResponse 33→35). The `init()`
guard (`diff.go:41-46`) panics on every replay test if this is missed.

## Error model

No new error paths. Resolver already returns typed `*params.ParamError` → 422 when
`method == exit_multiple` and no multiple resolvable (`resolve.go:237-243`). On the profile-driven
path the resolvability check now becomes reachable for profile-sourced methods too — confirm those
17 profiles all carry a positive `terminal_multiple` (verified: 3.0–25.0) so the 422 cannot fire
for shipped profiles; add a `config_validation_test` assertion to keep it that way.

---

# Tasks by Agent

## BACKEND (ordered)

1. **Spec + decision** (no code): write the EV/EBITDA-basis + 50/50-preserved decision into the
   VAL-1 tracker Phase 4 section before coding.
2. **TDD RED**: add failing table-driven tests in `internal/services/valuation/service_test.go`:
   - exit_multiple profile → `dcfResult.ExitMultipleTV > 0` and TV is blended; uses profile
     `terminal_multiple` (assert it differs from the industry default).
   - gordon profile + no-profile default → byte-identical TV to current (industry blend retained).
   - both-TV diagnostics populated correctly; `dcf_terminal_method` matches.
   - reinvestment+exit_multiple combo (`cyclical_mid_cycle:high_growth`,
     `hypergrowth_early:high_growth`) computes finite, positive TV.
3. **params provenance accessor**: add `EffectiveValuationParams.TerminalMethodSource Source`
   set in `ResolveInputs` (where `knobTerminalMethod` provenance is written, `resolve.go:154-165`);
   unit test it. Keep `params` a scalar leaf (no profile/entities import).
4. **service gate**: generalize `service.go:1325-1340` per Architecture pseudocode using the
   provenance accessor; keep the request-override branch semantics intact.
5. **diagnostics**: stamp `DCFGordonTerminalValue`/`DCFExitMultipleTerminalValue` from
   `dcfResult.GordonTV`/`dcfResult.ExitMultipleTV` near `service.go:1524-1537`.
6. **entities + handler + swagger**: add the 2 fields; map in `buildFairValueResponse`
   (~`fair_value.go:785-789`); add `example` tags.
7. **replay guard**: update `goFieldToJSON` + `countFairValueFields()` in `diff.go`.
8. **crosscheck doc note + test**: add a comment in `crosscheck.go` on the benign EV/EBITDA
   circularity; add a service-level test asserting no spurious EV/EBITDA divergence flag.
9. **CalcVersion 4.8→4.9**: both `service.go` stamp sites + every `service_test.go` version pin +
   `fair_value.go`/`docs.go`/swagger examples.
10. **accuracy**: run `cmd/accuracy` vs the 4.8 baseline; capture 4.9 baseline + dated report;
    confirm `TERMINAL_DOMINANCE` direction (price-free judgment, NOT gap-targeting).
11. **lint guards**: `scripts/lint-logs.ps1`, `scripts/lint-prometheus-registers.ps1`.

## QA

- Validate exit-multiple profile tickers (e.g. AMD/cyclical, a hypergrowth fixture, a REIT
  datacenter fixture) now produce a blended terminal using the profile multiple; both diagnostic
  fields present; `dcf_terminal_method == "exit_multiple"`.
- Validate `POST {}` == `GET` byte-identity still holds for a gordon/no-profile ticker.
- Validate DDM/FFO/revenue_multiple unchanged (`TestDDM_LegacyPath_BitForBit`, recompute-shadow,
  ledger-basket all GREEN).
- Validate replay suite passes (the `init()` field-count guard did not panic).
- Confirm no new 422s for shipped profiles (all 17 carry positive `terminal_multiple`).

## REVIEWER

- The provenance gate confines value change to explicitly profile/request-sourced methods —
  verify non-profiled `gordon_growth` tickers are byte-identical (the `industryExitMultiple`
  default blend must survive).
- 50/50 blend contract for request overrides is untouched.
- EV/EBITDA basis decision is documented and consistent (engine, profile multiples, crosscheck,
  spec wording all agree).
- CalcVersion bumped at all sites; replay `countFairValueFields` matches reflection.
- No DCF-path change leaks into alt-models; `params` stays a scalar leaf.

---

# Spec Updates

- **VAL-1 tracker** (`docs/reviewer/spec/VAL-1-...md`): tick Phase 4 acceptance ("Exit-multiple
  terminal optional and behind the profile"); add a NOTE that the engine multiple basis is
  **EV/EBITDA** (terminal EBITDA × multiple, 50/50 blend with Gordon), superseding the original
  "year_N_revenue × EV/Revenue" wording; record that the request-override 50/50 semantics are
  preserved; cross-reference this plan.
- **CLAUDE.md** (Common Gotchas): one bullet — "VAL-1 Phase 4: a resolved
  `AssumptionProfile.terminal_method == exit_multiple` now drives the DCF terminal (profile
  `terminal_multiple`, EV/EBITDA basis, 50/50 blend with Gordon). Non-profiled / `gordon_growth`
  tickers keep the legacy industry-EV/EBITDA blend (byte-identical). Request-override
  `terminal_method` semantics unchanged. Two new response fields:
  `dcf_gordon_terminal_value` + `dcf_exit_multiple_terminal_value` — register in `replay/diff.go`."
- **OpenAPI** (`docs/openapi.yaml`): 2 new `FairValueResponse` properties with types/examples and
  omission semantics.
- **replay diff.go**: `goFieldToJSON` += 2 entries; `countFairValueFields` 46→48.
- **TESTING.md**: note the new exit-multiple-profile test class and the byte-identity pin for
  non-profiled tickers.

---

# Shared-Contract Touchpoints

Sibling phases (2 horizon FOUNDATION, 3 cyclical base, 5 diluted-forward) all edit
`performValuation`'s DCF block and compete for `entities.ValuationResult` / `FairValueResponse`
fields and the `replay/diff.go` registration + `CalculationVersion` bump. Phase 4's footprint:

- **Profile fields RELIED ON (no new profile fields added):** `AssumptionProfile.TerminalMethod`
  (`profile.go:125`), `AssumptionProfile.TerminalMultiple` (`profile.go:128`). Phase 2 owns
  `HorizonYears`. No overlap in fields *added* — Phase 4 adds NONE to the profile.
- **Exact `service.go` seam:** the `exitMultiple` decision block at **`service.go:1325-1340`** and
  the diagnostic stamping at **`service.go:1524-1537`**. Phase 3 (cyclical base) edits `baseOI`
  derivation EARLIER (~`service.go:1271` `BaseOperatingIncome`); Phase 2 edits `projectionYears`
  (`service.go:1259`). Phase 4's seam is DOWNSTREAM of both → merge order should be Phase 2 → 3 →
  4 → 5 to minimize rebase churn (Phase 4 reads the already-resolved `projectionYears` and `baseOI`
  and does not modify them).
- **New response fields (PRECISE names — these claim `diff.go` slots):**
  `dcf_gordon_terminal_value`, `dcf_exit_multiple_terminal_value`. Phase 3 is expected to add a
  `dcf_base_normalization` field; Phase 5 a forward-diluted-shares field. **All four phases must
  coordinate the single `countFairValueFields()` constant** — last-to-merge reconciles the count.
  Recommend each phase's PR states "this adds N fields; new count = X" so the integrator sums them.
- **CalcVersion:** only ONE bump (4.8→4.9) should land on master per the combined VAL-1 merge.
  If phases merge separately, the FIRST to merge bumps to 4.9; subsequent phases keep 4.9 (or bump
  to 4.10 only if they ship independently after 4.9 is on master). Phase 4's plan assumes it may be
  the bump-owner; REVIEWER reconciles at integration.
- **config touchpoints:** Phase 4 touches `config/assumption_profiles.json` only if a multiple
  value is corrected (avoid). Phase 2/3 add per-archetype horizon/normalization params there —
  no key collision with `terminal_method`/`terminal_multiple`.

---

# Tests

Coverage target ≥90% on new code.

## Profile-driven exit_multiple — primary differs from Gordon (REQUIRED)
- Fixture resolving to an `exit_multiple` profile with `reinvestment_method` unset
  (`pre_revenue_biotech:high_growth`, mult 6.0) AND one with `sales_to_capital`
  (`hypergrowth_early:high_growth`, mult 5.0): assert `dcfResult.ExitMultipleTV > 0`,
  `TerminalValueNominal == (GordonTV + ExitMultipleTV)/2` (blend fired), and that
  `dcf_exit_multiple_terminal_value` uses the PROFILE multiple (engineered so the profile multiple
  ≠ industry default → EV differs from a hypothetical industry-blend run). Pin
  `dcf_terminal_method == "exit_multiple"`.

## Crosscheck interaction (REQUIRED)
- For an exit-multiple-terminal ticker vs. the same ticker forced to gordon: assert the
  EV/EBITDA divergence flag set does NOT gain a spurious entry attributable to the sector-multiple
  terminal (i.e., the blend pulls implied EV/EBITDA toward the median, never away). Assert
  `FlagDivergence` behavior is unchanged in form; document the benign circularity.

## Unchanged-method byte-identity (REQUIRED)
- A `gordon_growth`-profile ticker and a NO-profile ticker: assert full `ValuationResult`
  (EV, equityValue, dcf_value_per_share, TV) is byte-identical to a pre-Phase-4 golden capture
  (the legacy `industryExitMultiple` blend must survive untouched for these). This is the
  load-bearing regression pin distinguishing Phase 4 from a universe-wide change.
- `TestDDM_LegacyPath_BitForBit` GREEN; recompute-shadow `git diff --quiet` exits 0; ledger-basket
  GREEN.

## Request-override contract preserved (REQUIRED)
- Re-run the existing override tests (`TestPostFairValue_*`, override grid): `terminal_method:
  gordon_growth` suppresses blending; `terminal_method: exit_multiple` blends 50/50 with
  `p.TerminalMultiple`. No change.

## Replay guard
- Replay suite runs without the `init()` field-count panic after `countFairValueFields` bump.

## Diagnostics
- Both new fields present + finite for exit-multiple profiles; omitted/zero appropriately for
  pure-Gordon.

## config validation
- Assert every profile with `terminal_method == exit_multiple` carries `terminal_multiple > 0`
  (prevents the resolvable-multiple 422 from ever firing for shipped profiles).

---

# Next Steps

1. BACKEND: confirm the EV/EBITDA-basis + 50/50-preserved decision in the VAL-1 tracker, then TDD
   RED per the build order. Coordinate the `diff.go` field count + CalcVersion bump with the Phase
   2/3/5 owners at integration (merge order 2→3→4→5).
2. After implementation: QA the exit-multiple profile fixtures + byte-identity pins; REVIEWER
   verifies the provenance gate confines the change; then `/execute` B-V-R-Q + gpt-5.5 codereview;
   capture the 4.9 accuracy baseline.

HANDOFF_TO: BACKEND
