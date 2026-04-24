# AGENTS.md — Context Loading Contract

This file defines the **canonical loading order** for any AI agent (Claude Code, Cursor, Copilot, etc.) working on the Midas codebase. If you are an AI agent opening this repository, **start here**.

The goal is simple: every agent reads the same files in the same order, so context is predictable and reproducible across sessions and tools.

> Principle: *If it's not written to a file, it doesn't exist.* Durable context lives on disk, not in conversation memory.

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
| 14 | `docs/refactoring/valuation-engine-upgrade-spec.md` | Upgrade spec details |
| 15 | `docs/refactoring/industry-classification-unification-spec.md` | Planned SIC-only classification refactor (heuristic retirement) |
| 16 | `docs/superpowers/specs/` | Per-feature design specs (chronological by date) |
| 17 | `docs/reviewer/` | Tracked review follow-ups (S-1, S-4, W-2, W-3, W-4) |
| 18 | `docs/bugs/` | Bug tracker |
| 19 | `internal/services/<package>/` | Source code for the task at hand |

---

## File Roles (Quick Reference)

| Role | Files | Lifecycle |
|------|-------|-----------|
| **Identity** | `CLAUDE.md` | Rarely changes; updated when project scope shifts |
| **Direction** | `docs/THESIS.md` | Changes per major phase or pivot |
| **Durable memory** | `memory/MEMORY.md` + linked files | Curated weekly; keep concise (~150 lines for index) |
| **Volatile preferences** | `docs/FEEDBACK-LOG.md` | Append-only; pruned quarterly |
| **Daily notes** | `memory/daily/YYYY-MM-DD.md` | Append during session; promoted to MEMORY weekly |
| **Operational rules** | `agents/rules/*.mdc`, `agents/roles/*.md` | Changes when workflow evolves |
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

The hook at `.claude/hooks/load-rules.js` reads three foundation rules from `agents/rules/` on every `UserPromptSubmit`:

1. `agents/rules/_shared-workflow.mdc`
2. `agents/rules/preflight.mdc`
3. `agents/rules/orchestrator.mdc`

It injects them into context with a header `# Loaded Workflow Rules (agents/rules/)`. Deduplication is session+content-hash based with a 1-hour TTL.

The remaining rules (`codeexecution.mdc`, `load-context.mdc`, `project-planing.mdc`, `qa-automation.mdc`, `scaffold-module.mdc`, `ux-first-bug-fix-research.mdc`) are **not auto-loaded** — they are read on-demand when acting in the corresponding mode.

### Cursor Users

Cursor auto-discovers rules from `.cursor/rules/` only. Since the canonical location is now `agents/rules/`, Cursor will no longer auto-attach these rules. Options:

- **(Recommended)** Invoke rules explicitly with `@agents/rules/<name>.mdc` when using Cursor.
- **(Alternative)** Create symlinks from `.cursor/rules/` to `agents/rules/` if Cursor auto-attach is needed.

---

## Change Log

| Date | Change |
|------|--------|
| 2026-04-18 | Initial file. Established loading order inspired by OpenClaw's agent context model. |
| 2026-04-19 | Moved rules from `.cursor/rules/` to `agents/rules/` (tool-neutral). Updated `load-rules.js` hook paths. Tier 3 now references new canonical location. |
| 2026-04-23 | Added Tier 4 entries for `docs/refactoring/industry-classification-unification-spec.md` and `docs/superpowers/specs/` (per-feature design specs). Reflects the AMD retail-misclassification hotfix + Industry-in-response feature shipped 2026-04-23/24. |
