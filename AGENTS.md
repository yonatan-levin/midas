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
| 3 | `docs/THESIS.md` | Product direction, current phase, roadmap, scope boundaries — **the canonical home for phase/milestone history** |

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

> Each row is a **pointer** — *which file to open and why* — not a status board. For *what shipped when* (phase milestones, commit/merge history), read `docs/THESIS.md` (the single canonical home) plus the per-subject `*-closeout.md` docs under `docs/refactoring/archive/`. Keep these cells to one line.

| # | File | Purpose |
|---|------|---------|
| 12 | `docs/API_DOCUMENTATION.md` | Full consumer API reference — endpoints, request/response fields, errors |
| 13 | `docs/openapi.yaml` | Machine-readable API contract (canonical) |
| 14 | `docs/refactoring/spec/valuation-engine-upgrade-spec.md` | Valuation-engine upgrade spec — multi-stage growth, industry models, international |
| 15 | `docs/refactoring/spec/industry-classification-unification-spec.md` | Planned SIC-only classification refactor (heuristic retirement) |
| 16 | `docs/refactoring/archive/observability-upgrade-spec.md` | Observability v1.1 design — request correlation, file logging, 12-stage calc tracing |
| 17 | `docs/refactoring/spec/observability-narrative-and-artifacts-spec.md` + `docs/refactoring/archive/observability-replay-tooling-spec.md` | Observability narrative/artifacts (Tier-1 narrate, Tier-2 debug-tracer, Tier-3 per-request bundle) + replay-tooling design and the `cmd/replay` CLI surface |
| 17a | `docs/refactoring/archive/assumption-profile-spec.md` + `…-implementation-plan.md` | Tier 2 AssumptionProfile design + rollout — the `(archetype × maturity)` calibration backbone for DCF/DDM/FFO/RevenueMultiple |
| 17b | `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` (per-phase `…-closeout.md` under `archive/`) | DC-1 three-view datacleaner refactor design — `AsReported`/`Restated`/`InvestedCapital` views + `AdjustmentLedger`/`OverlaySpec` audit trail |
| 18 | `docs/superpowers/specs/` | Per-feature design specs (chronological by date) |
| 19 | `docs/reviewer/` | Review follow-up trackers — open at the directory root, resolved under `archive/` |
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
- **Not a project-history log** — phase milestones, commit SHAs, and merge ladders belong in `docs/THESIS.md` and the per-subject `*-closeout.md` docs, never in this loading contract

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

> This logs changes to **AGENTS.md itself** (structure, loading order, conventions) — not project phase history, which lives in `docs/THESIS.md`.

| Date | Change |
|------|--------|
| 2026-04-18 | Initial file — loading order inspired by OpenClaw's agent context model. |
| 2026-04-19 | Moved rules `.cursor/rules/` → `agents/rules/` (tool-neutral); updated `load-rules.js` hook paths. |
| 2026-04-23 | Added Tier 4 entries for the industry-classification-unification spec + `docs/superpowers/specs/`. |
| 2026-04-25 → 2026-05-09 | Tracked observability narrative/artifacts/replay phase status on Tier 4 row 17 (Phases 1 → 2.D). *Phase history now lives in `docs/THESIS.md`.* |
| 2026-05-14 | Established the Subject-Folder Convention (`docs/<subject>/{spec,implementations,archive}/`); migrated `docs/refactoring/` into it. |
| 2026-05-21 | Flipped Tier 4 row 17a (Tier 2 AssumptionProfile) to COMPLETE. *Milestone detail in `docs/THESIS.md`.* |
| 2026-06-04 | **Restored AGENTS.md to a pure loading contract** — collapsed the Tier 4 history walls (rows 17a/17b) and per-phase status annotations to one-line pointers; relocated milestone history to `docs/THESIS.md` (its designated home). |
