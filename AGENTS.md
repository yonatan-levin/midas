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
| 16 | `docs/refactoring/spec/observability-upgrade-spec.md` | Observability upgrade v1.1 (request correlation, file logging, 12-stage calc tracing) — ALL PHASES COMPLETE |
| 17 | `docs/refactoring/spec/observability-narrative-and-artifacts-spec.md` + `docs/refactoring/implementations/observability-replay-tooling-r{2,3,3b}-implementation-plan.md` | Observability narrative + artifacts (Tier-1 narrate stream, Tier-2 Debug-tracer convention, Tier-3 per-request artifact bundle) — PHASE 1 + 2.A + 2.B + 2.C SHIPPED (manual `?trace=1`/`X-Midas-Trace` triggers + auto-on-error via `logging.artifact_store.triggers.on_error` + auto-on-quality-flag via `logging.artifact_store.triggers.quality_flag_threshold` + always-on via `logging.artifact_store.triggers.always`); **Phase 2.D (replay tooling) ALL R0–R3 SHIPPED** — `cmd/replay/main.go` re-runs captured artifact bundles through current code via `internal/observability/replay/`; see standalone spec `docs/refactoring/spec/observability-replay-tooling-spec.md` v0.5 for full design + the 14-flag CLI surface (`--format`, `--out`, `--allow-schema-drift`, `--allow-git-drift`, `--quiet`, `--verbose`, `--from`, `--workers`, `--filter-ticker`, `--filter-since`, `--float-rel-tol`, `--float-abs-tol`, `--diff-stages`); per-phase implementer plans live under `implementations/` |
| 17a | `docs/refactoring/spec/assumption-profile-spec.md` + `docs/refactoring/implementations/assumption-profile-implementation-plan.md` | **Tier 2 AssumptionProfile architectural sprint (in flight)** — unified profile backbone keyed by `(archetype × maturity)` driving DCF/DDM/FFO/RevenueMultiple calibration; closes RM-3 + VAL-1 + VAL-2 + VAL-3 P3. Spec, kickoff brief, future-DB tracker live in `spec/`; ARCH-produced implementer plan that BACKEND consumes lives in `implementations/`. **Phase Bootstrap SHIPPED 2026-05-16** (commit `265b9c9` on branch `tier2-bootstrap`): 10-ticker replay baseline captured under `artifacts/tier2-baseline/`, 6 DDM bit-for-bit golden fixtures pinned at `internal/services/valuation/models/testdata/golden/`, new `testhelpers` package at `internal/services/valuation/profile/testhelpers/` for P1-P4 consumption, load-bearing `TestDDM_LegacyPath_BitForBit` regression test guarding the JPM/BAC/WFC bit-for-bit invariant. **Phase P0a SHIPPED 2026-05-16** (commit `d2a586e` on branch `tier2-p0a`): full type system at `internal/services/valuation/profile/` — 21 Archetype constants + 3 Maturity + 4 enum types; `AssumptionProfile` struct (14 fields); `Facts` DTO with pointer-field missing-vs-zero semantics; `ResolutionTrace` + `AssumptionProfileManifest`; `Registry` interface + jsonRegistry impl with SHA-256 config_hash; 9 load-time validation invariants (fail-loud on malformed shipped config); 3-stage `Resolve()` algorithm (industry-rule match → cyclical-trough override → maturity bucketing → archetype-specific pin); pure function (no I/O, no time, no random); import-boundary test enforces no `models`/`entities` imports. 91.5% coverage. JPM bit-for-bit DDM invariant intact. **Phase P0b SHIPPED 2026-05-16** (commit `2e48fde` on branch `tier2-p0b`): wires Tier 2 plumbing through the engine without changing math. `config/assumption_profiles.json` carries initial config (mature_large_bank:mature anchor + software_like_scaling fallback + 3 archetype rules); `config/embed.go` embeds it; `LoadFromBytes` extracted alongside `LoadFromJSON` for embed.FS support. `Bundle.SetAssumptionProfileManifest(ctx, manifest)` writes `08-assumption-profile.json` (registered as schema v1). `HistoricalFinancialData.RecentYoYGrowth() *float64` (nil-safe; pointer return distinguishes missing-vs-zero). `NewService` gains 11th param `profile.Registry`; `performValuation` builds `profile.Facts` from latest financials + calls `profileRegistry.Resolve()` (after WACC, before `router.SelectModel`); resolved profile stamps onto `ModelInput.Profile`, `result.AssumptionProfile`, `result.ResolutionTrace`, and the artifact bundle manifest (both DCF + alt-model paths). `ModelResult` gains 4 omitempty Tier-2 fields (TrailingValue, ForwardValue, HorizonSelected, TerminalMultiple — populated by P1/P4). `ValuationResult` + `FairValueResponse` gain 7 omitempty Tier-2 fields (AssumptionProfile, ResolutionTrace, DCFHorizonYears, DCFTerminalMethod, DCFTerminalPctOfEV, DCFPerYearPV, DCFTerminalGrowthUsed — populated by P2). fx.Provide wires Registry in `internal/di/container.go` (production) AND `internal/observability/replay/module.go` (replay, hermetic). Replay walker `compareFairValueResponses` extended to cover 5 of 7 new fields (gap on `dcf_per_year_pv` + `resolution_trace` tracked as **T2-P0b-1** prerequisite for P2). JPM bit-for-bit DDM invariant intact. **T2-P4-W1 SHIPPED 2026-05-19** (merge `be92a79` via single commit `cdcc82f` on retired branch `t2-prefix-fix`): classifier emission reconciled to `REIT_*` prefixed form so Tier 2 archetype rules fire against real REIT requests once P4 merges. Pre-fix the classifier emitted bare codes (DATA_CENTER, INDUSTRIAL, RETAIL_REIT, …) while `assumption_profiles.json` rules used `REIT_DATACENTER`, `REIT_INDUSTRIAL`, etc. — every REIT subsector would have fallen through to the `software_like_scaling:standard_growth` wildcard fallback after P4 merge. Fix is config-driven: renames in `config/datacleaner/industry_codes.json` flow directly to classifier emission. Downstream consumers updated atomically: `config/industry_multiples.json` keys (v1.3.0), `models/router.go::reitIndustrySet` + defensive `strings.HasPrefix("REIT_")` fallback, FFO subsector tables, `handlers/fair_value.go::sicToGICS`. FIN side audit (documented in tracker): classifier already emits `FIN_INSURANCE` (no work needed) and `FIN_BANK` (no large/small split; matches existing `fin_generic` FIN-prefix rule, preserves JPM bit-for-bit). B-Q-V-R gate cycle clean; live API regression on EQIX+PLD and replay regression on `artifacts/tier2-baseline/` deferred to Tier 2 Closeout (need P4 merged to exercise REIT-specific rules). **P1-P4 worktrees still pending rebase + merge onto fixed master** — parallel-dispatched but each requires its own B-V-R-Q cycle on top of the new master. Tracker `docs/reviewer/T2-P4-W1-classifier-prefix-mismatch.md` stays open until Closeout phase validates the deferred acceptance rows. |
| 17b | `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` + `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-0-implementation-plan.md` | **DC-1 datacleaner refactor (in flight)** — moves the cleaner from in-place `FinancialData` mutation to a three-view output (`AsReported` / `Restated` / `InvestedCapital`) with an explicit `AdjustmentLedger` and `OverlaySpec` audit trail. Closes the balance-sheet asymmetry (today's cleaner mutates `TotalAssets` but not `StockholdersEquity` → `Assets ≠ Liabilities + Equity` post-clean) and unlocks future features (Altman-Z, P/B, ROE-decomposition, distress screens) that need a balanced post-clean balance sheet. **Phase 0 SHIPPED 2026-05-16** (merge `1640394`): 4 plug fields on `FinancialData` + `computePlugs` helper + wiring into `parsePeriodData`. Zero behavior change — populated but unread. **Phases 1-4 pending**: component primitive + `recomputeUmbrellas` shim → unified `Adjuster` interface with `AdjusterOutput{LedgerEntries, Overlays, Flags}` (one interface; Restater/Overlay/Hybrid emerge from output shape; per GPT-5.5-pro pressure test) → `CleanedFinancialData` view reconstruction → consumer migration of all 13 valuation read sites. **B3 contingent liabilities reroute to `DebtLikeClaims` (NOT TotalDebt)** is a substantive accuracy correction landing in Phase 4. Damodaran goodwill convention preserved (A1 stays Overlay, excluded from `InvestedCapital`). |
| 18 | `docs/superpowers/specs/` | Per-feature design specs (chronological by date) |
| 19 | `docs/reviewer/` | Review follow-up tracker — currently only `archive/` (all open items closed 2026-04-24/25 sweep). File new docs here when issues surface. |
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
| 2026-04-23 | Added Tier 4 entries for `docs/refactoring/industry-classification-unification-spec.md` and `docs/superpowers/specs/` (per-feature design specs). Reflects the AMD retail-misclassification hotfix + Industry-in-response feature shipped 2026-04-23/24. |
| 2026-04-25 | Added Tier 4 entry #17 for `docs/refactoring/observability-narrative-and-artifacts-spec.md` (Tier-1/2/3 observability upgrade, DESIGN phase). Updated `docs/reviewer/` row: all open items closed by the 2026-04-24/25 sweep, only `archive/` remains. Renumbered Tier 4 rows 17→22. |
| 2026-04-27 | Updated Tier 4 entry #17 status from "DESIGN, Phase 1 scoped" to "PHASE 1 + 2.A SHIPPED" after Phase 2.A (auto-on-error trigger) merged to master as `48a9578`. Entry now lists deferred 2.B / 2.C / 2.D. No row renumbering. |
| 2026-04-29 | Updated Tier 4 entry #17 status to "PHASE 1 + 2.A + 2.B SHIPPED" after Phase 2.B (auto-on-quality-flag trigger) merged to master as `fa89aa2`. Entry now lists only 2.C (always-on) and 2.D (replay tooling) as deferred. No row renumbering. |
| 2026-05-01 | Updated Tier 4 entry #17 status to "PHASE 1 + 2.A + 2.B + 2.C SHIPPED" after Phase 2.C (always-on knob) merged to master as `6e3ad8f`. Entry now lists only 2.D (replay tooling) as deferred. No row renumbering. |
| 2026-05-09 | Updated Tier 4 entry #17 status: **Phase 2.D (replay tooling) ALL R0–R3 SHIPPED** after R3b merged to master as `0741958` (preceded by R0+R1 `8a9878f` 2026-05-03, R2 `e4d2fb2` 2026-05-05, R3a `011d78c` 2026-05-06). Entry text now references the standalone spec `docs/refactoring/observability-replay-tooling-spec.md` v0.5 (carved out from §13 of the parent narrative spec during R0+R1 dispatch) and lists the full 14-flag `cmd/replay` CLI surface. **Phase 2.D = COMPLETE; no further sub-phases pending.** Entry #17 reads as one consolidated observability-narrative-and-artifacts row covering both the parent spec (Phases 1-2.C narrative/triggers/auto-on-error/quality-flag/always-on) AND the carved-out replay-tooling spec (R0+R1+R2+R3a+R3b: Clock + skeleton + gateway substitution + parallel batch + filter flags + tolerance flags + diff-stages + perf benches + reflection guard). No row renumbering. |
| 2026-05-14 | **Subject-Folder Convention established.** Added new top-level section "Subject-Folder Convention" describing the `docs/<subject>/{archive,spec,implementations}/` three-subfolder shape: `spec/` holds design specs + kickoff briefs + future-trackers + multi-phase rollout plans (the *what*); `implementations/` holds BACKEND-consumable implementer plans with full code blocks per task (the *how*); `archive/` holds retired/superseded work. Authoring flow: ARCH writes spec → ARCH writes implementer plan via `/plan-and-create` → BACKEND executes. Initial migration: `docs/refactoring/` files redistributed into `spec/` (all `*-spec.md` + `tier-2-assumption-profile-kickoff.md` + `assumption-profile-db-backed-future.md` + `assumption-profile-implementation-plan.md` (the multi-phase rollout plan)) and `implementations/` (the 3 `observability-replay-tooling-r*-implementation-plan.md` files). Added Tier 4 row 17a for the in-flight Tier 2 AssumptionProfile work. Tier 4 paths updated to new sub-folder locations. Other subject folders (`reviewer/`, `bugs/`, `integration/`, `superpowers/`) adopt the same convention going forward but are NOT migrated in this pass — they'll be reshaped lazily as they receive new work. |
