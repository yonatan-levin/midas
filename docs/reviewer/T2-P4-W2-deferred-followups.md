# T2-P4-W2 — Tier 2 deferred follow-up findings consolidated from 12 B-V-R-Q gates

**Status:** OPEN
**Severity:** MIXED (mostly NIT / LOW, one CONCERN, one LATENT, one MINOR, one DEFERRED)
**Filed:** 2026-05-21 by Tier 2 Closeout sweep (consolidates findings from B-V-R-Q gates against P2/P3/P4)
**Phase context:** Tier 2 — surfaced during the 12 parallel quality gates (3 phases × 4 gates each: BACKEND / VERIFIER / REVIEWER / QA)
**Owner:** TBD (next polishing pass / before Tier 3)

---

## Context

The 12 B-V-R-Q gates that ran across P2 (`877fa76`), P3 (`59c0fdc`), and P4 (`362b63b`) all returned with PASS/APPROVED/VERIFIED/DONE verdicts, recommending HUMAN merge. They also surfaced ~10 "non-blocking but worth tracking" findings that were collectively decided NOT to fix in the per-phase merge gate. Some were addressed in defect-fixup commits at merge time; the rest are deferred to a Closeout follow-up pass and recorded here.

This tracker captures 12 items that span style nits, latent invariants, a coverage gap, a parity concern, and a deferred rename. None of these blocked a merge. They are grouped here so a future polishing pass can resolve them as a single sweep rather than re-discover them piecemeal.

---

## From P2 gates

### 1. NIT — `terminal_dominance` warning fires on legacy path — **RESOLVED 2026-05-23 (this commit; branch `chore/t2-p4-w2-batch-1-3-doc-and-comment`)**

- **Severity:** NIT
- **Surfacing gate:** P2 REVIEWER (LOW finding)
- **Location:** `internal/services/valuation/service.go:1306-1310` (the warning emission site; the tracker's original `:1316` pointer was off-by-a-few-lines after intervening edits)
- **Why not fixed at merge:** The warning is strictly more diagnostic value when it fires broadly; there is no false-positive risk to suppressing it. The plan §6.2 specified "only profile-driven", but the implementation fires on BOTH profile-driven AND legacy DCF paths. This is an implementation/spec drift, not a bug.
- **Suggested resolution:** Two options:
  - (a) Update the plan doc to match the implementation (recommended — the broader firing is strictly better diagnostically).
  - (b) Gate the warning behind `resolvedProfile != nil` to honor the plan as written.
- **Resolution:** Chose option (a) per recommendation. Added a post-merge reconciliation NOTE to `docs/refactoring/implementations/assumption-profile-implementation-plan.md` Task P2.2's terminal-dominance snippet explaining that the warning emission is intentionally unconditional (fires on both profile-driven and legacy paths) because terminal-PV dominance is a real model-risk signal regardless of how the horizon was chosen, and gating behind `resolvedProfile != nil` would silently mask the same signal on legacy tickers. No code change. `go test ./...` PASS.
- **Priority:** Opportunistic (doc reconciliation only)

### 2. NIT — `itoaP2` shim could be `strconv.Itoa` — **RESOLVED 2026-05-23 (commit `106c960`)**

- **Severity:** NIT
- **Surfacing gate:** P2 REVIEWER
- **Location:** `internal/services/valuation/service_test.go` (test helper)
- **Why not fixed at merge:** Trivial style cleanup; not behavior-affecting.
- **Suggested resolution:** Replace `itoaP2` shim with `strconv.Itoa` (stdlib equivalent). Single-commit cleanup.
- **Resolution:** Imported `strconv`, replaced the single `itoaP2(horizonYears)` call site with `strconv.Itoa(horizonYears)`, and removed the shim function. `go test ./internal/services/valuation/...` green.
- **Priority:** Opportunistic

### 3. NIT — Stale exit-multiple comment — **RESOLVED 2026-05-23 (this commit; branch `chore/t2-p4-w2-batch-1-3-doc-and-comment`)**

- **Severity:** NIT
- **Surfacing gate:** P2 REVIEWER
- **Location:** `internal/services/valuation/service.go:1052` (comment was at `:1057` pre-edit; now removed) vs `:1112-1123` (the actual exit-multiple wiring that does NOT reassign `terminalMethodLabel`)
- **Why not fixed at merge:** The comment says terminal_method_label "may be overridden by exit-multiple wiring below" but the wiring at `:1119-1128` does not actually reassign `terminalMethodLabel`. Misleading comment but no runtime impact.
- **Suggested resolution:** Either:
  - (a) Remove the misleading comment; OR
  - (b) Stamp `"gordon_growth+exit_multiple"` when `ExitMultiple > 0` to make the comment true.
  - Defer the (a)-vs-(b) decision to the polishing pass — non-blocking.
- **Resolution:** Chose option (a) — clean delete. Replaced `terminalMethodLabel := "gordon_growth" // default; may be overridden by profile or exit-multiple wiring below` with just `terminalMethodLabel := "gordon_growth"`. The profile-driven override at `:1069-1071` (`if resolvedProfile.TerminalMethod != "" { ... }`) speaks for itself and is the only reassignment site; the exit-multiple wiring at `:1112-1123` sets `dcf.Inputs.ExitMultiple` so `pkg/finance/dcf` can average the exit-multiple TV in, but does NOT touch the label. Option (b) was rejected as a behavior change (response would gain a `"gordon_growth+exit_multiple"` value not currently in the contract). `go test ./internal/services/valuation/...` PASS.
- **Priority:** Opportunistic

---

## From P3 gates

### 4. LATENT — No load-time invariant for `len(PayoutPath) == DividendForecastHorizon`

- **Severity:** LATENT
- **Surfacing gate:** P3 REVIEWER (N1 finding)
- **Location:** Runtime guard at `internal/services/valuation/models/ddm.go:342` (`i < len(p.PayoutPath)`); missing invariant in `internal/services/valuation/profile/validation.go`
- **Why not fixed at merge:** Not P3-introduced; the gap was latent in the existing 9-invariant validator. A malformed profile with mismatched PayoutPath length would silently truncate the payout-multiplier effect via the runtime guard rather than fail fast at registry load.
- **Suggested resolution:** Add a 10th load-time invariant in `validation.go`: for any rule with `dividend_forecast_horizon > 0` and a non-empty `payout_path`, assert `len(PayoutPath) == DividendForecastHorizon`. Spec §4.4 already covers the fail-loud philosophy.
- **Priority:** Before Tier 3 (small surface, high consistency value)

### 5. CONCERN — Multi-stage DDM path missing ROE/payout/P/BV diagnostics parity

- **Severity:** CONCERN
- **Surfacing gate:** P3 REVIEWER (N3 finding)
- **Location:** Multi-stage DDM branch in `internal/services/valuation/models/ddm.go` (emits fixed `Confidence: "medium"` + single warning string); contrast with legacy path which adjusts confidence based on warning count and runs ROE / payout / P/BV diagnostics.
- **Why not fixed at merge:** Not a regression — the multi-stage path is a separate model branch. But the asymmetry is worth normalizing before Tier 3 adds further model variants.
- **Suggested resolution:** Lift the diagnostics emitter (ROE / payout / P/BV) into a shared helper invoked by both DDM branches; replace fixed `Confidence: "medium"` with a warning-count-adjusted scoring identical to the legacy path.
- **Priority:** Before Tier 3 (parity concern; will accumulate if Tier 3 adds more branches)

### 6. GAP — `ddm_multistage_test.go` covers shared math via one profile only

- **Severity:** GAP (test coverage)
- **Surfacing gate:** P3 VERIFIER (CONCERN-1 finding)
- **Location:** `internal/services/valuation/models/ddm_multistage_test.go`
- **Why not fixed at merge:** Tests use `maturing_tech_first_dividend` profile to exercise the shared multi-stage math. The other 4 archetype configurations that share the same code path (growth_bank × 2, insurance_company:mature, mature_dividend_tech:mature) aren't pin-tested individually. Pin tests were deferred until `testhelpers.RunValuation` lands (cross-package test helper that lets phase tests drive a full `Service.Valuate` call without re-wiring fx).
- **Suggested resolution:** Once `testhelpers.RunValuation` is available, add per-archetype regression pins for each of the 4 uncovered configurations (1 pin test per archetype × maturity bucket).
- **Priority:** Before Tier 3 (depends on `testhelpers.RunValuation` shipping)

---

## From P4 gates

### 7. DEFERRED — `ArchetypeREITCommercial` enum rename

- **Severity:** DEFERRED
- **Surfacing gate:** P4 REVIEWER (NIT-1) + P4 BACKEND
- **Location:** `internal/services/valuation/profile/profile.go` (archetype enum), `config/assumption_profiles.json` (rule entry); industry_prefix is `REIT_OFFICE` while archetype id remains `reit_commercial`
- **Why not fixed at merge:** Out-of-scope refactor would cascade through `profile.go` + `validation.go` + multiple consumers (test fixtures, golden files, plan doc references). The P4 defect-fixup chose to document the asymmetric naming in the rule's `notes` field instead.
- **Suggested resolution:** Coordinate with a type-system audit pass; rename `ArchetypeREITCommercial` → `ArchetypeREITOffice` (or alternative agreed naming) and update all consumers in a single coordinated commit.
- **Priority:** Before Tier 3 (asymmetry is a footgun for new contributors)

### 8. MINOR — Per-function coverage on FFO subsector loaders below 90% — RESOLVED 2026-05-23

- **Severity:** MINOR
- **Surfacing gate:** P4 QA (C4 finding)
- **Location:** `internal/services/valuation/models/ffo.go` — `loadFFOSubsectorTables` 71.4% / `lookupSubsectorValue` 76.5%; package total 94.1% (gate met)
- **Why not fixed at merge:** The package-level gate (≥90%) passed; per-function gap is below the per-function bar but does not block under the package criterion.
- **Suggested resolution:** Add a malformed-row regression test in `internal/services/valuation/models/ffo_test.go` (e.g., subsector row with non-numeric multiple, missing key, empty subsector string) to cover the error branches of `loadFFOSubsectorTables` and `lookupSubsectorValue`.
- **Priority:** Opportunistic (gate passed; nice-to-have for resilience)
- **Resolution:** branch `test/t2-p4-w2-item8-ffo-loader-coverage` adds 12 targeted defensive tests in `internal/services/valuation/models/ffo_test.go` (test-only addition; no changes to `ffo.go`). Final per-function coverage:
  - `lookupSubsectorValue`: 76.5% → **100.0%** (all branches covered: exact match, longest-prefix-match, underscore-boundary guard, `default`-key skip, nil/empty table, empty industry, no-match fall-through)
  - `loadFFOSubsectorTables`: 71.4% → 71.4% (unchanged — the two remaining branches are `configfs.Read` failure and `json.Unmarshal` failure; both are structurally unreachable because `configfs.Read` is backed by `embed.FS` rooted at `config/` and always returns the same valid JSON bytes baked into the binary, and the task scope explicitly excluded modifying `ffo.go` to add a production-side seam). The happy-path return is pinned by new tests `TestLoadFFOSubsectorTables_EmbeddedConfig` (asserts all 8 REIT_* subsector keys + `default` present in both tables) and `TestLoadFFOSubsectorTables_FeedsLookup` (regression pin for the loader↔lookup data-shape contract).
  - Package total: 94.4% → **95.3%**.

### 9. NIT — Long notes field on `reit_commercial` — **RESOLVED 2026-05-23 (commit `106c960`)**

- **Severity:** NIT
- **Surfacing gate:** P4 REVIEWER (NIT-1 alternative)
- **Location:** `config/assumption_profiles.json` — `reit_commercial` rule's `notes` field documenting the prefix↔archetype-id asymmetry
- **Why not fixed at merge:** The notes field documents real rationale that should not be lost; shortening it without a destination would lose context.
- **Suggested resolution:** Now that this tracker exists, extract the rationale here (where item #7 already captures it) and shorten the rule's `notes` to a pointer like `"see docs/reviewer/T2-P4-W2 item 7 — archetype id retained as reit_commercial pending coordinated rename"`.
- **Resolution:** Shortened the `reit_commercial` rule's `notes` to a pointer referencing item #7 of this tracker; thesis (office headwinds, low terminal multiple) retained. `go test ./internal/services/valuation/profile/...` green.
- **Priority:** Opportunistic (after item #7 resolves, this self-closes)

### 10. NIT — `reit_specialty` notes-style inconsistency — **RESOLVED 2026-05-23 (commit `106c960`)**

- **Severity:** NIT
- **Surfacing gate:** P4 REVIEWER (NIT-2)
- **Location:** `config/assumption_profiles.json` — `reit_specialty` rule's `notes` lists specific tickers ("self-storage / billboard / prison / timber") while siblings follow a shorter "<subsector> REIT subsector; <thesis>" pattern.
- **Why not fixed at merge:** Style consistency only; no functional impact.
- **Suggested resolution:** Align `reit_specialty` notes to the sibling pattern when doing a notes-style sweep across `config/assumption_profiles.json`.
- **Resolution:** Rewrote notes to the sibling pattern: "specialty REIT subsector (self-storage, billboard, prison, timber); heterogeneous niche assets with subsector-specific durability." `go test ./internal/services/valuation/profile/...` green.
- **Priority:** Opportunistic (batch with item #9 in a single notes-style sweep)

---

## Cross-phase findings

### 11. CONVERGENT — `FilingDate := AsOf` fixture-patch duplicated across phase tests

- **Severity:** NIT (test-helper duplication)
- **Surfacing gate:** P1 REVIEWER (NIT #4) + P1 VERIFIER observation + P3 VERIFIER
- **Location:** Same 4-line patch appears in:
  - `internal/services/valuation/tier2_regression_test.go::TestTier2_MXL_Pin` (P1)
  - `internal/services/valuation/pin_capture_test.go::TestCapturePins` (P1)
  - Similar pin paths in P3 multi-stage tests
- **Why not fixed at merge:** Each phase landed independently; consolidation would have required coordinating against worktrees in flight. Now that all three phases are merged on master, the helper can be extracted in one commit.
- **Suggested resolution:** Extract to a `testhelpers` helper like `PatchFilingDatesFromAsOf(input *models.ModelInput)` (loops over `input.HistoricalData.Data` setting `FilingDate = AsOf` when zero). Update the three call sites to use the helper.
- **Priority:** Opportunistic (low blast radius, closes a small duplication)

### 12. CONVERGENT — Replay walker (T2-P0b-1) closed by P2; `ResolutionTrace` gap remains [RESOLVED 2026-05-23]

- **Severity:** NIT / scope-tracking
- **Surfacing gate:** P2 BACKEND + P3 VERIFIER
- **Location:** `internal/observability/replay/compare.go` (gained `DCFPerYearPV` diff in P2); `ResolutionTrace` walker gap mentioned in T2-P0b-1's filing remains unaddressed.
- **Why not fixed at merge:** P2's replay walker work closed the DCFPerYearPV portion; the `ResolutionTrace` walker addition is a separate scope.
- **Suggested resolution:** If the `ResolutionTrace` walker gap is still material after Tier 2 Closeout, file a separate tracker (T2-CL-Wx) rather than re-using T2-P0b-1. Confirm scope first — may be obsolete now that profile-driven resolution flows are merged across P2/P3/P4.
- **Priority:** Opportunistic (verify scope first; file separately if still needed)

**Resolution (2026-05-23, branch `feat/t2-p4-w2-item12-replay-walker-resolution-trace`):** Scope confirmed real — the hand-rolled `compareFairValueResponses` in `internal/observability/replay/compare.go` (the walker `Replay()` actually invokes at `replay.go:147` for drift detection) did NOT walk `ResolutionTrace`. The reflection-based `CompareResponse` in `diff.go` auto-discovers it via go-cmp but is not on the Replay() orchestration path. Extension landed:

- `compare.go` — added `compareResolutionTrace(bundle, current *profile.ResolutionTrace, d *ResultDiff)` mirroring the `compareIndustry` pattern: nil-vs-nil no-op, nil-vs-non-nil single sentinel `StringDiff` at path `resolution_trace`, populated-vs-populated walks 8 scalar fields (`profile_id`, `source`, `resolver_version`, `config_version`, `config_hash`, `matched_rule_id`, `fallback_reason`, `human_reason`) + `missing_facts` slice (length-mismatch sentinel + per-index walk, mirroring `Warnings` / `SanityCheck.Flags`).
- `diff.go::goFieldToJSON` — added 9 `ProfileID`/`Source`/`ResolverVersion`/`ConfigVersion`/`ConfigHash`/`MatchedRuleID`/`FallbackReason`/`MissingFacts`/`HumanReason` → snake_case mappings so a future migration of `Replay()` to `CompareResponse` produces the same dotted paths the hand-rolled walker emits.
- `compare_response_test.go` — added 5 new top-level test functions covering the full nil-aware matrix: `BothNil_NoFalsePositive`, `NilVsPopulated` (sentinel path + no per-field noise assertion), `PopulatedVsPopulated_NoDrift`, `PerFieldDrift` (8-row table-driven across every scalar field), `MissingFactsDrift` (length + element). 14 sub-tests total, all PASS.
- `countFairValueFields` unchanged — `init()` reflection guard counts `reflect.NumField` of `FairValueResponse` top-level only; `ResolutionTrace` is a single field at that level (was already counted in the existing 30).

Validation: `go test ./internal/observability/replay/...` PASS; `go test ./...` PASS across all packages.

---

## Closing this tracker

Move to `docs/reviewer/archive/` once:

- ~~Items 1, 2, 3, 9, 10, 11 (NIT / cleanup batch) are resolved in a single polishing-pass commit OR explicitly waived~~ — items 1, 3 RESOLVED 2026-05-23 (this commit); items 2, 9, 10 previously resolved 2026-05-23 (commit `106c960`); item 11 still OPEN
- Item 4 (LATENT validation invariant) is added to `profile/validation.go` with a test
- Item 5 (CONCERN — DDM multi-stage diagnostics parity) is either implemented OR explicitly deferred to a Tier 3 design note
- Item 6 (GAP — per-archetype DDM pins) lands once `testhelpers.RunValuation` ships
- Item 7 (DEFERRED — `ArchetypeREITCommercial` rename) is either executed OR explicitly deferred to a named Tier 3 phase
- ~~Item 8 (MINOR — FFO loader coverage) has a regression test added OR is explicitly waived (package-level gate continues to pass)~~ [RESOLVED 2026-05-23 — branch `test/t2-p4-w2-item8-ffo-loader-coverage`]
- ~~Item 12 (replay `ResolutionTrace` walker) scope is confirmed and either resolved or refiled~~ [RESOLVED 2026-05-23 — see item 12]

This tracker SHOULD close before Tier 3 begins. None of its items are blockers for Tier 2 ship; all are quality-of-life improvements that compound if left unaddressed.
