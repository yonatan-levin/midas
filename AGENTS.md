# AGENTS.md — Context Loading Contract

This file defines the **canonical loading order** for any AI agent (Claude Code, Cursor, Copilot, etc.) working on the Midas codebase. If you are an AI agent opening this repository, **start here**.

The goal is simple: every agent reads the same files in the same order, so context is predictable and reproducible across sessions and tools.

> Principle: *If it's not written to a file, it doesn't exist.* Durable context lives on disk, not in conversation memory.

---

## Subject-Folder Convention (`docs/<subject>/{archive,spec,implementations}/`)

Every subject folder under `docs/` follows a three-subfolder convention:

| Subfolder | Purpose | Lifecycle |
|---|---|---|
| `spec/` | Design specs, kickoff briefs, future-work trackers, multi-phase rollout plans. The *what* and *why*. Reviewable by an architect. | Durable reference. Stays after implementation ships. |
| `implementations/` | Implementer-grade plans BACKEND consumes — exact file paths, full code blocks per task, RED→GREEN TDD steps, commit templates. The *how* and *in what order*. | One file per implementation cycle. Historical plans stay here for traceability. |
| `archive/` | Explicitly retired or superseded work — closed trackers, replaced specs, deprecated plans. | Read-only reference; not maintained. |

**Authoring flow (ARCH → BACKEND handoff):**
1. ARCH writes a spec under `<subject>/spec/<feature>-spec.md`
2. ARCH writes (or regenerates via `/plan-and-create`) an implementer plan under `<subject>/implementations/<feature>-implementation-plan.md`
3. BACKEND executes the implementer plan task-by-task
4. After implementation ships, the spec stays in `spec/`; the implementation plan stays in `implementations/`; only closed trackers move to `archive/`

**Reading flow (any agent picking up work):**
1. Read `<subject>/spec/` first for design context
2. Read `<subject>/implementations/<feature>-implementation-plan.md` for the executable steps
3. Reference `<subject>/archive/` only when researching historical decisions

This convention applies to every subject folder under `docs/` (currently: `refactoring/`, `reviewer/`, `bugs/`, `integration/`, `superpowers/`). New subject folders adopt the same shape.

---

## Loading Order (Read Top-to-Bottom)

At the start of any work session, read these files in order. Stop at the first tier that gives you enough context for the task.

### Tier 1 — Identity & Direction (Always Read)

| # | File | Purpose |
|---|------|---------|
| 1 | `CLAUDE.md` | Project identity, tech stack, conventions, important files, build commands |
| 2 | `AGENTS.md` (this file) | Loading contract and cross-file relationships |
| 3 | `docs/THESIS.md` | Product direction, current phase, roadmap, scope boundaries |

### Tier 2 — Working Memory (Read When Resuming Work)

| # | File | Purpose |
|---|------|---------|
| 4 | `.claude/projects/<project-hash>/memory/MEMORY.md` | Index of durable facts, preferences, upgrade status |
| 5 | `docs/FEEDBACK-LOG.md` | User corrections and preferences not yet promoted to MEMORY |
| 6 | `.claude/projects/<project-hash>/memory/daily/YYYY-MM-DD.md` | Today's session notes (if exists) |

### Tier 3 — Operational Rules (Read When Acting in a Specific Role)

| # | File | Purpose |
|---|------|---------|
| 7 | `agents/rules/_shared-workflow.mdc` | Shared workflow for all roles (auto-loaded by `.claude/hooks/load-rules.js` for Claude Code) |
| 8 | `agents/rules/preflight.mdc` | Pre-implementation checklist (auto-loaded by hook) |
| 9 | `agents/rules/orchestrator.mdc` | Routing logic and specialist dispatch (auto-loaded by hook) |
| 10 | `agents/rules/<mode>.mdc` | Mode-specific rules (codeexecution, load-context, project-planing, qa-automation, scaffold-module, ux-first-bug-fix-research) |
| 11 | `agents/roles/<role>.md` | Role-specific operational rules (BACKEND, ARCH, QA, REVIEWER, etc.) |

### Tier 4 — Task-Specific Deep Dive (Read Only When Relevant)

| # | File | Purpose |
|---|------|---------|
| 12 | `docs/API_DOCUMENTATION.md` | Full API reference, valuation engine internals, config, deployment |
| 13 | `docs/openapi.yaml` | Machine-readable API contract |
| 14 | `docs/refactoring/spec/valuation-engine-upgrade-spec.md` | Upgrade spec details |
| 15 | `docs/refactoring/spec/industry-classification-unification-spec.md` | Planned SIC-only classification refactor (heuristic retirement) |
| 16 | `docs/refactoring/archive/observability-upgrade-spec.md` | Observability upgrade v1.1 (request correlation, file logging, 12-stage calc tracing) — ALL PHASES COMPLETE |
| 17 | `docs/refactoring/spec/observability-narrative-and-artifacts-spec.md` + `docs/refactoring/archive/observability-replay-tooling-r{2,3,3b}-implementation-plan.md` | Observability narrative + artifacts (Tier-1 narrate stream, Tier-2 Debug-tracer convention, Tier-3 per-request artifact bundle) — PHASE 1 + 2.A + 2.B + 2.C SHIPPED (manual `?trace=1`/`X-Midas-Trace` triggers + auto-on-error via `logging.artifact_store.triggers.on_error` + auto-on-quality-flag via `logging.artifact_store.triggers.quality_flag_threshold` + always-on via `logging.artifact_store.triggers.always`); **Phase 2.D (replay tooling) ALL R0–R3 SHIPPED** — `cmd/replay/main.go` re-runs captured artifact bundles through current code via `internal/observability/replay/`; see standalone spec `docs/refactoring/archive/observability-replay-tooling-spec.md` v0.5 for full design + the 14-flag CLI surface (`--format`, `--out`, `--allow-schema-drift`, `--allow-git-drift`, `--quiet`, `--verbose`, `--from`, `--workers`, `--filter-ticker`, `--filter-since`, `--float-rel-tol`, `--float-abs-tol`, `--diff-stages`); per-phase implementer plans live under `implementations/` |
| 17a | `docs/refactoring/archive/assumption-profile-spec.md` + `docs/refactoring/archive/assumption-profile-implementation-plan.md` | **Tier 2 AssumptionProfile architectural sprint — COMPLETE 2026-05-21.** — unified profile backbone keyed by `(archetype × maturity)` driving DCF/DDM/FFO/RevenueMultiple calibration; closes RM-3 + VAL-1 + VAL-2 + VAL-3 P3. Spec, kickoff brief, and implementer plan live in `archive/`; the future-DB tracker lives in `spec/`. **Phase Bootstrap SHIPPED 2026-05-16** (commit `265b9c9` on branch `tier2-bootstrap`): 10-ticker replay baseline captured under `artifacts/tier2-baseline/`, 6 DDM bit-for-bit golden fixtures pinned at `internal/services/valuation/models/testdata/golden/`, new `testhelpers` package at `internal/services/valuation/profile/testhelpers/` for P1-P4 consumption, load-bearing `TestDDM_LegacyPath_BitForBit` regression test guarding the JPM/BAC/WFC bit-for-bit invariant. **Phase P0a SHIPPED 2026-05-16** (commit `d2a586e` on branch `tier2-p0a`): full type system at `internal/services/valuation/profile/` — 21 Archetype constants + 3 Maturity + 4 enum types; `AssumptionProfile` struct (14 fields); `Facts` DTO with pointer-field missing-vs-zero semantics; `ResolutionTrace` + `AssumptionProfileManifest`; `Registry` interface + jsonRegistry impl with SHA-256 config_hash; 9 load-time validation invariants (fail-loud on malformed shipped config); 3-stage `Resolve()` algorithm (industry-rule match → cyclical-trough override → maturity bucketing → archetype-specific pin); pure function (no I/O, no time, no random); import-boundary test enforces no `models`/`entities` imports. 91.5% coverage. JPM bit-for-bit DDM invariant intact. **Phase P0b SHIPPED 2026-05-16** (commit `2e48fde` on branch `tier2-p0b`): wires Tier 2 plumbing through the engine without changing math. `config/assumption_profiles.json` carries initial config (mature_large_bank:mature anchor + software_like_scaling fallback + 3 archetype rules); `config/embed.go` embeds it; `LoadFromBytes` extracted alongside `LoadFromJSON` for embed.FS support. `Bundle.SetAssumptionProfileManifest(ctx, manifest)` writes `08-assumption-profile.json` (registered as schema v1). `HistoricalFinancialData.RecentYoYGrowth() *float64` (nil-safe; pointer return distinguishes missing-vs-zero). `NewService` gains 11th param `profile.Registry`; `performValuation` builds `profile.Facts` from latest financials + calls `profileRegistry.Resolve()` (after WACC, before `router.SelectModel`); resolved profile stamps onto `ModelInput.Profile`, `result.AssumptionProfile`, `result.ResolutionTrace`, and the artifact bundle manifest (both DCF + alt-model paths). `ModelResult` gains 4 omitempty Tier-2 fields (TrailingValue, ForwardValue, HorizonSelected, TerminalMultiple — populated by P1/P4). `ValuationResult` + `FairValueResponse` gain 7 omitempty Tier-2 fields (AssumptionProfile, ResolutionTrace, DCFHorizonYears, DCFTerminalMethod, DCFTerminalPctOfEV, DCFPerYearPV, DCFTerminalGrowthUsed — populated by P2). fx.Provide wires Registry in `internal/di/container.go` (production) AND `internal/observability/replay/module.go` (replay, hermetic). Replay walker `compareFairValueResponses` extended to cover 5 of 7 new fields (gap on `dcf_per_year_pv` + `resolution_trace` tracked as **T2-P0b-1** prerequisite for P2). JPM bit-for-bit DDM invariant intact. **T2-P4-W1 SHIPPED 2026-05-19** (merge `be92a79` via single commit `cdcc82f` on retired branch `t2-prefix-fix`): classifier emission reconciled to `REIT_*` prefixed form so Tier 2 archetype rules fire against real REIT requests once P4 merges. Pre-fix the classifier emitted bare codes (DATA_CENTER, INDUSTRIAL, RETAIL_REIT, …) while `assumption_profiles.json` rules used `REIT_DATACENTER`, `REIT_INDUSTRIAL`, etc. — every REIT subsector would have fallen through to the `software_like_scaling:standard_growth` wildcard fallback after P4 merge. Fix is config-driven: renames in `config/datacleaner/industry_codes.json` flow directly to classifier emission. Downstream consumers updated atomically: `config/industry_multiples.json` keys (v1.3.0), `models/router.go::reitIndustrySet` + defensive `strings.HasPrefix("REIT_")` fallback, FFO subsector tables, `handlers/fair_value.go::sicToGICS`. FIN side audit (documented in tracker): classifier already emits `FIN_INSURANCE` (no work needed) and `FIN_BANK` (no large/small split; matches existing `fin_generic` FIN-prefix rule, preserves JPM bit-for-bit). B-Q-V-R gate cycle clean; live API regression on EQIX+PLD and replay regression on `artifacts/tier2-baseline/` deferred to Tier 2 Closeout (need P4 merged to exercise REIT-specific rules). **P1-P4 worktrees still pending rebase + merge onto fixed master** — parallel-dispatched but each requires its own B-V-R-Q cycle on top of the new master. Tracker `docs/reviewer/T2-P4-W1-classifier-prefix-mismatch.md` stays open until Closeout phase validates the deferred acceptance rows. |
| 17b | `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` + `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` + `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md` + `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md` + `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md` + `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-0-implementation-plan.md` + `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md` | **DC-1 datacleaner refactor — COMPLETE / CLOSED 2026-06-02 (Phase 5 SHIPPED; PARTIAL merged `e816fcc`, follow-up merged `8ca0841`).** All phases 0–5 are CODE-COMPLETE; the reviewer tracker is ARCHIVED to `docs/reviewer/archive/`. The only remaining DC-1 item is an OPERATOR follow-up (fresh CalcVersion-4.4 replay baseline capture + live DebtLikeClaims-EV confirmation on a B-rule-firing bank), tracked at `docs/reviewer/DC-1-phase-5-replay-verification-followup.md` — NOT a code blocker. Per-phase history follows. The refactor moves the cleaner from in-place `FinancialData` mutation to a three-view output (`AsReported` / `Restated` / `InvestedCapital`) with an explicit `AdjustmentLedger` and `OverlaySpec` audit trail. Closes the balance-sheet asymmetry (today's cleaner mutates `TotalAssets` but not `StockholdersEquity` → `Assets ≠ Liabilities + Equity` post-clean) and unlocks future features (Altman-Z, P/B, ROE-decomposition, distress screens) that need a balanced post-clean balance sheet. **Phase 0 SHIPPED 2026-05-16** (merge `1640394`): 4 plug fields on `FinancialData` + `computePlugs` helper + wiring into `parsePeriodData`. Zero behavior change — populated but unread. **Phase 1 SHIPPED 2026-05-19** (merge `2d916a7`): `recomputeUmbrellas` shadow-mode observer + basket integration test producing committed per-ticker `recompute-shadow/<TICKER>.json` snapshots as the Phase 2 punch-list input. Zero behavior change. **Phase 2 SHIPPED 2026-05-23** (full 4-PR stack merged from `dc1-phase-2-pr-4`). PR-1 (interface + entities + scaffolding shim) tip `39cf0fa`; PR-2 (6 A-rules + asset-shim deletion + SchemaVersion 7→8) tip `2e8f83b`; PR-3 (7 C-rules + earnings-shim deletion) tip `207f41a`; PR-4 (3 B-rules + orchestrator absorption + liability-shim + shim-helpers deletion + basket integration test + Phase 2 closeout docs sweep) tip `ed1dadd`. All 17 cleaner-side adjusters now native; PR-1 shim FULLY removed. **Four role flavors locked:** OverlayEmitter (A1, B1, B2, B3), Restater (A2, A4, C1-3, C5, C6 — with C6's LOAD-BEARING `EquityOffset=0` for interest reclassification), Restater+TaxShieldDTA (A5), FlagEmitter (C4, C7, plus 2 PR-2 reviews). **Canonical pattern across all 17:** mutation-FREE `Apply*` + dispatcher-owns-dual-write + per-rule translator + native-drain via `NativelyEmittedRuleIDs`. **B3 routes to `OverlaySpec.Field:"DebtLikeClaims"`** recording Phase 4 routing intent (substantive accuracy correction — Damodaran convention; today's Phase 2 dual-write still mutates `data.TotalDebt` for legacy preservation; Phase 4 flips the consumer read site and deletes the dual-write). **T2-BS-3 disposition:** Option B (carve-out) — parser fix deferred; AMD/KO `AsReported.TotalLiabilities=0` stays; Phase 3 `Restated` view reconstruction fixes downstream consumption. SchemaVersion at 8. **New `TestLedger_BasketSnapshot_ClusterPrediction` integration test** (10/10 basket tickers PASS) at `internal/integration/datacleaner_ledger_basket_test.go` — first integration test to read `data.AdjustmentLedger` directly. All load-bearing invariants GREEN throughout all 28 commits. **Phase 3 spec + implementer plan AUTHORED 2026-05-23** at `datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` + `datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md` — `CleanedFinancialData` accessors (`AsReported`/`Restated`/`InvestedCapital`), Q2 (A2 `TaxShieldDTA` populated when `EffectiveTaxRate > 0`), Q4 (B3 `AIProvenance.PromptHash` + `SourceDocHash` SHA-256 hex), `ctx` threading through `Process*Adjustments`, translator-extraction decision KEEP per-rule. **Phase 3 SHIPPED 2026-05-24** on branch `dc1-phase-3` (single-PR Option A; 10 commits — 8 implementation + 1 closeout/docs-sweep + 1 V/R/Q-driven cleanup `b997ce6`); **MERGED to master 2026-05-25 as `46e84b1`**. `cleaneddata` package landed with all three view accessors + memoization + import-boundary test; `Service.CleanFinancialDataWithViews` sibling added (additive — existing `CleanFinancialData` signature unchanged). Q2 + Q4 resolved with named tests (`TestQ2_A2TaxShieldDTA_Populated`, `TestQ4_AIProvenance_SHA256_Deterministic`). ctx threaded through all three `Process*Adjustments` dispatchers; B3 AI path now honors upstream cancellation. `SchemaVersion["FinancialData"]` 8 → 9 atomic with Q2 commit; replay testdata `happy/00-manifest.json` synced. **T2-BS-3 acceptance signal LIVE on real bundles** — AMD 2023Q2 reconstructs to $9.679B, KO 2023Q2 to $60.912B against the 2026-05-19 baseline. All load-bearing invariants stayed GREEN at every commit (`TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, shadow-snapshot byte-identity, `TestLedger_BasketSnapshot_ClusterPrediction`). NON-goals honored: no consumer migration, no B3 routing flip, no dual-write deletion, no CalcVersion bump (all Phase 4 scope). Phase 4 (13-site consumer migration + B3 routing flip + dual-write deletion) follows. Damodaran goodwill convention preserved (A1 stays Overlay, excluded from `InvestedCapital`). Closeout: `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md`. **Phase 3 followup SHIPPED 2026-05-25** on branch `dc1-phase-3-followup` (10 commits) — closes 9 cross-model review findings: HIGH-1 `Restated()` view-seed double-count (pre-clean snapshot in `CleanFinancialDataWithViews`; `cleaneddata.New(asReported, restated)` signature; reducer drives equity + DTA only — `applyLedgerEntryToView` DELETED); HIGH-2 + HIGH-3 (B3 single AI call via `analyzeContingentLiabilityWithAI(ctx, ...)`; `captureB3AIProvenance` DELETED); MEDIUM-1 (B1 lease PV ctx threading); MEDIUM-2 (Phase 3 §5.2 PromptHash spec amendment — canonical-request fingerprint, not literal LLM prompt-as-sent); LOW-1 (`hash.go` json.Marshal hardening); LOW-2 (cleaneddata goroutine-safety godoc); LOW-3 (`TestIdentityCopy_CoversEveryViewField` reflection test); LOW-4 (`Raw()` TODO(phase-5)). New load-bearing pin: `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire`. NON-goals preserved: no consumer migration, no B3 routing flip, no dual-write deletion, no `CalculationVersion` bump (4.2), no `SchemaVersion` bump (9), no changes to `recompute.go`. Spec: `docs/refactoring/archive/dc1-phase-3-followup-spec.md`. Plan: `docs/refactoring/archive/dc1-phase-3-followup-implementation-plan.md`. Closeout: `docs/refactoring/archive/dc1-phase-3-followup-closeout.md`. **Phase 4 MERGED to master 2026-05-27 as `ce94f70`** (8 commits: C-1..C-5 `f65604a`/`4f8a06c`/`9bc885e`/`7349a1e`/`ae9113a` + `e521c53` NWC→AsReported fix + `2ea9978` revenue_multiple DebtLikeClaims fix + `3931a41` docs; 4 review rounds incl. gpt-5.5 cross-model; Phase 5 spec+plan AUTHORED on `dc1-phase-5-prep`). 13-site consumer migration to `cleaneddata` views + B3 routing flip + dispatcher dual-write deletion (§8.2.1 Option A via `applyLedgerComponentDeltas`) + `CalculationVersion` 4.2→4.3. B3/B1/B2 contingent/lease/pension claims now flow to `InvestedCapital().DebtLikeClaims` (EV→Equity bridge via new `dcf.CalculateEquityValueWithDebtLikeClaims`), OUT of the WACC denominator (`Restated().InterestBearingDebt`). DDM migration + `Raw()` deletion + vestigial-translator deletion all DEFERRED to Phase 5 (still load-bearing / bit-for-bit). KNOWN DEVIATION: shadow snapshots intentionally regenerated (Option A umbrella incoherence observed by the unchanged `recomputeUmbrellas`); all SEMANTIC invariants GREEN. Judgment calls: Graham + tangible read `AsReported()` (spec §4.2.12 Restated premise was incorrect — `Restated()` recomputes TangibleAssets). Replay verification DEFERRED to operator (stale 4.1 baseline). Closeout: `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md`. **Phase 5 SHIPPED — PARTIAL merged `e816fcc`, follow-up merged `8ca0841` (2026-06-02); DC-1 CLOSED.** The PARTIAL (10 commits, tip `e6418e4`) ships P5-C1 (DDM EV-bridge `+DebtLikeClaims` correction + `CalculationVersion` 4.3→4.4 — DDM ADDS DebtLikeClaims, opposite sign vs DCF/revenue_multiple which subtract, because DDM derives equity FROM dividends and THEN EV from equity); P5-C2 (DDM consumer migration to `Restated()` via `LatestRestatedView` in `runDividendDiagnostics` + `estimateDividendGrowth` with 4-step in-commit bit-for-bit re-proof — JPM/BAC/WFC `TestDDM_LegacyPath_BitForBit` preserved); P5-C3 SCOPED (orchestrator firing-signal migrated to `nativeFired(...)` helper at `internal/services/datacleaner/firing_signal.go` filtering `LedgerEntry.Fired==true` — full Adjustments-projection deferred); P5-C5 PARTIAL (`cleaneddata.Raw()` deleted + concurrency contract godoc strengthened to HARD request-local invariant + verify-then-decide on legacy `historicalData` slot → KEEP via new `keepLatestCleanedSlot` helper). **Two review rounds on the Phase 5 partial:** (1) gpt-5.5 cross-model on the initial 5-commit ship (zen-mcp continuation `22fbf842`) found 1 HIGH (firing-signal native-equivalence drift on the rules-pass-applicability-but-Apply-skips path — skip diagnostics emit `Fired:false` LedgerEntries that the inline predicate over-counted, inflating `RulesApplied` telemetry) + 3 MEDIUM + 5 LOW; all 9 closed by 3 atomic fix commits `83e6cb2` + `e4ea146` + `b12a870` + 1 follow-up doc fix `de1a456`. (2) Full `/execute` B-V-R-Q cycle with subagents on the fix commits — VERIFIER independently simulated pre-fix predicate to confirm `TestApplyActiveAdjustments_FiringSignalParity_A1ApplicableButSkipped` regression test would have failed under the old code; REVIEWER walked the role-vs-emission contract for all 4 adjuster roles + confirmed `nativeFired` behavioral equivalence; QA verified per-finding spec conformance + zero scope drift; gpt-5.5 Q-pass (zen-mcp continuation `bea446b5`) caught a stale test docstring the inline subagents missed (closed by `e6418e4`) and confirmed the role contract via grep evidence (zero `Fired:false` literals co-located with `Overlays:` or non-empty `Flags:` in any AdjusterOutput literal). **Follow-up (merged `8ca0841`, 2026-06-02 — branch `dc1-phase-5-followup` off master `974570c`; 7-commit ladder A0–C1 + 4 review-fix commits FIX-1..FIX-4 + closeout/operator-followup docs)** closes the three deferred CODE chunks, completing DC-1: (1) **P5-C3-full Adjustments-projection** — `result.Adjustments` is now produced by ONE shared native projection `adjustmentsFromLedger(...)` at `internal/services/datacleaner/adjustment_projection.go` (walks `data.AdjustmentLedger` asset→liability→earnings, looks up a static per-AdjusterID `ruleMeta`); ARCH Path (a) preserves `Adjustment.Percentage` bit-for-bit by capturing fired-rule pre-state on `LedgerEntry.SkipMetrics["original_<Field>"]` (no schema bump) read with an explicit `ok`-check (ARCH note `docs/refactoring/archive/dc1-phase-5-followup-percentage-decision.md`); (2) **P5-C4 deletion** — the 16 `*AdjusterOutputToLegacyResult` translators, dispatcher `original*` captures, dormant `earnings.go` fallback helpers, dead `entities.{Asset,Liability}AdjustmentResult` duplicates, and 9 test-only-dead legacy `Process*Adjustment` helpers (+5 orphaned tests) are DELETED; the 3 category `*AdjustmentResult` structs SLIMMED to the native carrier; (3) **DDM `modelIBD` view-migration flip** — DDM's `modelIBD` now reads `Restated().InterestBearingDebt` like every other alt-model (bit-for-bit safe). Validation: full `/execute` B-V-R-Q ×2 + gpt-5.5 cross-model Q-pass ×2 (no Critical/High); `/verify` runtime replay confirmed `calculation_version "4.4"` live. CalcVersion stays `"4.4"` / SchemaVersion["FinancialData"] stays 9 (no follow-up bumps). **NEW REALITY (the former "translator stack still load-bearing for `result.Adjustments`" gotcha is now FALSE and RETIRED):** the per-rule translators are DELETED; `result.Adjustments` is produced solely by the native `adjustmentsFromLedger` projection over `data.AdjustmentLedger`. **STILL REMAINING (OPERATOR, NOT a code blocker):** fresh CalcVersion-4.4 replay baseline capture + live DebtLikeClaims-EV confirmation on a B-rule-firing bank — tracked at `docs/reviewer/DC-1-phase-5-replay-verification-followup.md`. Follow-up closeout: `docs/refactoring/archive/dc1-phase-5-followup-closeout.md`. Phase 5 PARTIAL closeout: `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-5-closeout.md`. Phase 5 spec: `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md`. Implementer plan: `docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md`. |
| 18 | `docs/superpowers/specs/` | Per-feature design specs (chronological by date) |
| 19 | `docs/reviewer/` | Review follow-up trackers. Open trackers live at the directory root until their close gate is satisfied; archived trackers live under `docs/reviewer/archive/`. **DC-1 closed 2026-06-02:** the `DC-1-datacleaner-component-primitive-and-parallel-views.md` tracker is now ARCHIVED to `docs/reviewer/archive/` (DC-1 refactor COMPLETE, Phase 5 follow-up merged `8ca0841`). The only OPEN DC-1 item is `DC-1-phase-5-replay-verification-followup.md` (operator-only: fresh CalcVersion-4.4 replay baseline capture + live DebtLikeClaims-EV confirmation — NOT a code blocker). File new docs here when issues surface. |
| 20 | `docs/bugs/` | Bug tracker |
| 21 | `internal/observability/` | Cross-cutting logger plumbing: `logctx` (context-scoped logger) + `calclog` (calculation-stage trace emitter) |
| 22 | `internal/services/<package>/` | Source code for the task at hand |

---

## File Roles (Quick Reference)

| Role | Files | Lifecycle |
|------|-------|-----------|
| **Identity** | `CLAUDE.md` | Rarely changes; updated when project scope shifts |
| **Direction** | `docs/THESIS.md` | Changes per major phase or pivot |
| **Durable memory** | `memory/MEMORY.md` + linked files | Curated weekly; keep concise (~150 lines for index) |
| **Volatile preferences** | `docs/FEEDBACK-LOG.md` | Append-only; pruned quarterly |
| **Daily notes** | `memory/daily/YYYY-MM-DD.md` | Append during session; promoted to MEMORY weekly |
| **Operational rules** | `agents/rules/*.md`, `agents/roles/*.md` | Changes when workflow evolves |
| **Reference docs** | `docs/*` | Updated alongside code changes |

---

## When to Write to These Files

### Write to `MEMORY.md` (durable)
- User tells you something non-obvious about the project that should persist across sessions
- A design decision is made that constrains future work
- A recurring pattern is identified

### Write to `FEEDBACK-LOG.md` (corrections)
- User explicitly corrects an approach: "don't do X, do Y"
- User validates a non-obvious choice: "yes, that bundled PR was right"
- Include **Why** and **How to apply** so future sessions can judge edge cases

### Write to `memory/daily/YYYY-MM-DD.md` (session notes)
- In-progress findings during a work session
- Commands run and their outputs
- Decisions made that may or may not be durable yet

### Write to `docs/THESIS.md` (direction)
- Phase completion
- Scope addition or removal
- Roadmap adjustment

---

## Curation Rhythm

| Cadence | Action |
|---------|--------|
| **Per session** | Append to `memory/daily/YYYY-MM-DD.md` as findings emerge |
| **End of session** | Promote durable insights from daily log to `MEMORY.md`; append corrections to `FEEDBACK-LOG.md` |
| **Weekly** | Review `FEEDBACK-LOG.md` → promote recurring items to `MEMORY.md`; archive stale daily logs |
| **Per phase** | Update `docs/THESIS.md` with completed/new milestones |

---

## Sub-Agent Context Diet

When spawning a sub-agent (via Claude Code's Agent tool or similar), **do not** inject the full Tier 1-4 context. Sub-agents should receive only:

- The task prompt (self-contained, with relevant file paths and line numbers)
- The specific `agents/roles/<role>.md` file matching their role
- The specific files they need to read (by path)

This keeps sub-agent context tight and avoids compaction pressure.

---

## What This File Is NOT

- **Not a tutorial** — see `docs/API_DOCUMENTATION.md` for that
- **Not a personality/tone guide** — Midas has no agent personality; `CLAUDE.md` defines project conventions
- **Not a replacement for `agents/rules/`** — those remain the authoritative mode/role rules; this file just tells you when to read them

---

## How Claude Code Auto-Loads Tier 3 Rules

The hook at `.claude/hooks/load-rules.js` reads three foundation rules from `agents/rules/` on every `SessionStart`:

1. `agents/rules/_shared-workflow.md`
2. `agents/rules/preflight.md`
3. `agents/rules/orchestrator.md`

It injects them into context with a header `# Loaded Workflow Rules (agents/rules/)`. Deduplication is session+content-hash based with a 1-hour TTL.

The remaining rules (`load-context.md`, `scaffold-module.md`) are **not auto-loaded** — they are read on-demand when acting in the corresponding mode.

### Cursor Users

Cursor auto-discovers rules from `.cursor/rules/` only. Since the canonical location is now `agents/rules/`, Cursor will no longer auto-attach these rules. Options:

- **(Recommended)** Invoke rules explicitly with `@agents/rules/<name>.md` when using Cursor.
- **(Alternative)** Create symlinks from `.cursor/rules/` to `agents/rules/` if Cursor auto-attach is needed.

---

## Change Log

| Date | Change |
|------|--------|
| 2026-04-18 | Initial file. Established loading order inspired by OpenClaw's agent context model. |
| 2026-04-19 | Moved rules from `.cursor/rules/` to `agents/rules/` (tool-neutral). Updated `load-rules.js` hook paths. Tier 3 now references new canonical location. |
| 2026-04-23 | Added Tier 4 entries for `docs/refactoring/spec/industry-classification-unification-spec.md` and `docs/superpowers/specs/` (per-feature design specs). Reflects the AMD retail-misclassification hotfix + Industry-in-response feature shipped 2026-04-23/24. |
| 2026-04-25 | Added Tier 4 entry #17 for `docs/refactoring/spec/observability-narrative-and-artifacts-spec.md` (Tier-1/2/3 observability upgrade, DESIGN phase). Updated `docs/reviewer/` row: all open items closed by the 2026-04-24/25 sweep, only `archive/` remains. Renumbered Tier 4 rows 17→22. |
| 2026-04-27 | Updated Tier 4 entry #17 status from "DESIGN, Phase 1 scoped" to "PHASE 1 + 2.A SHIPPED" after Phase 2.A (auto-on-error trigger) merged to master as `48a9578`. Entry now lists deferred 2.B / 2.C / 2.D. No row renumbering. |
| 2026-04-29 | Updated Tier 4 entry #17 status to "PHASE 1 + 2.A + 2.B SHIPPED" after Phase 2.B (auto-on-quality-flag trigger) merged to master as `fa89aa2`. Entry now lists only 2.C (always-on) and 2.D (replay tooling) as deferred. No row renumbering. |
| 2026-05-01 | Updated Tier 4 entry #17 status to "PHASE 1 + 2.A + 2.B + 2.C SHIPPED" after Phase 2.C (always-on knob) merged to master as `6e3ad8f`. Entry now lists only 2.D (replay tooling) as deferred. No row renumbering. |
| 2026-05-09 | Updated Tier 4 entry #17 status: **Phase 2.D (replay tooling) ALL R0–R3 SHIPPED** after R3b merged to master as `0741958` (preceded by R0+R1 `8a9878f` 2026-05-03, R2 `e4d2fb2` 2026-05-05, R3a `011d78c` 2026-05-06). Entry text now references the standalone spec `docs/refactoring/archive/observability-replay-tooling-spec.md` v0.5 (carved out from §13 of the parent narrative spec during R0+R1 dispatch) and lists the full 14-flag `cmd/replay` CLI surface. **Phase 2.D = COMPLETE; no further sub-phases pending.** Entry #17 reads as one consolidated observability-narrative-and-artifacts row covering both the parent spec (Phases 1-2.C narrative/triggers/auto-on-error/quality-flag/always-on) AND the carved-out replay-tooling spec (R0+R1+R2+R3a+R3b: Clock + skeleton + gateway substitution + parallel batch + filter flags + tolerance flags + diff-stages + perf benches + reflection guard). No row renumbering. |
| 2026-05-14 | **Subject-Folder Convention established.** Added new top-level section "Subject-Folder Convention" describing the `docs/<subject>/{archive,spec,implementations}/` three-subfolder shape: `spec/` holds design specs + kickoff briefs + future-trackers + multi-phase rollout plans (the *what*); `implementations/` holds BACKEND-consumable implementer plans with full code blocks per task (the *how*); `archive/` holds retired/superseded work. Authoring flow: ARCH writes spec → ARCH writes implementer plan via `/plan-and-create` → BACKEND executes. Initial migration: `docs/refactoring/` files redistributed into `spec/` (all `*-spec.md` + `tier-2-assumption-profile-kickoff.md` + `assumption-profile-db-backed-future.md` + `assumption-profile-implementation-plan.md` (the multi-phase rollout plan)) and `implementations/` (the 3 `observability-replay-tooling-r*-implementation-plan.md` files). Added Tier 4 row 17a for the in-flight Tier 2 AssumptionProfile work. Tier 4 paths updated to new sub-folder locations. Other subject folders (`reviewer/`, `bugs/`, `integration/`, `superpowers/`) adopt the same convention going forward but are NOT migrated in this pass — they'll be reshaped lazily as they receive new work. |
| 2026-05-21 | **Tier 4 row 17a Tier 2 status flipped to COMPLETE** after the Closeout docs sweep landed. Tier 2 merged across all 4 phase commits on master: P1 `9966175` (RM-3 forward revenue multiple), P2 `877fa76` (VAL-1 DCF archetype-aware horizon + Pre-P2 growth-estimator extension), P3 `59c0fdc` (VAL-2 DDM multi-stage via byte-identical legacy-Gordon path lift; defect-fixup `5a72208` deleted dead `fin_small_bank` + `fin_large_bank` rules), P4 `362b63b` (VAL-3 P3 forward FFO; defect-fixup `b8853c7` renamed `REIT_COMMERCIAL` → `REIT_OFFICE` + added missing `reit_specialty` rule). Earlier T2-P4-W1 classifier reconciliation merge `be92a79` set up REIT_* / FIN_* prefixed industry codes. Final state: **31 profiles + 19 rules** in `config/assumption_profiles.json`; all 8 REIT subsectors have working archetype rules end-to-end; JPM/BAC/WFC bit-for-bit DDM invariant preserved across all phase merges. Engine target version after Tier 2 close: `CalculationVersion 4.2` (spec-level; actual code bump deferred to follow-on code commit per docs-only Closeout scope). Deferred Closeout validation rows (live API on EQIX + PLD + replay against `artifacts/tier2-baseline/`) tracked at `docs/reviewer/T2-P4-W1-classifier-prefix-mismatch.md` (stays OPEN). Follow-up tracker `docs/reviewer/T2-P4-W2-deferred-followups.md` enumerates deferred-to-future-phase items consolidated from 12 B-V-R-Q gates (filed by sibling Closeout step 2026-05-21 at commit `e724018`). Spec bumped v0.1 → v0.2. Plan §8 entries for P1/P2/P3/P4/Closeout flipped from "Pending dispatch" to "SHIPPED" with B-V-R-Q verdicts. T2-P0b-1 tracker RESOLVED (P2 walker extension landed). No row renumbering. |
