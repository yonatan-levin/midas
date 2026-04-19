---
name: docs-update
description: Update Midas documentation after code changes. Keeps AGENTS.md, THESIS.md, API_DOCUMENTATION.md, openapi.yaml, and related artifacts in sync with the Go codebase.
---

# Docs Update Skill (Midas)

When invoked with `@docs-update {scope}`, update relevant Midas documentation to reflect code or workflow changes.

## Purpose

Keeps documentation synchronized with code changes in the Midas DCF valuation API project. Prevents documentation drift and ensures specs remain accurate.

Midas uses a layered documentation system (see `AGENTS.md` loading contract for the full tier structure):

- **Tier 1 (Identity + Direction)**: `CLAUDE.md`, `AGENTS.md`, `docs/THESIS.md`
- **Tier 2 (Working memory)**: `MEMORY.md`, `docs/FEEDBACK-LOG.md`, `memory/daily/`
- **Tier 3 (Operational rules)**: `agents/rules/*.mdc`, `agents/roles/*.md`
- **Tier 4 (Reference)**: `docs/API_DOCUMENTATION.md`, `docs/openapi.yaml`, `docs/refactoring/`, `docs/reviewer/`, `docs/bugs/`

## Documentation Files (Midas-Specific)

| File | Purpose | When to Update |
|------|---------|----------------|
| `CLAUDE.md` | Project identity, tech stack, build commands | Commands change, new gotchas, conventions shift |
| `AGENTS.md` | Canonical load order for AI agents (tier 1-4) | Files move between directories, new tier entries |
| `docs/THESIS.md` | Product direction, phase status, scope | Phase completion, roadmap adjustment |
| `docs/FEEDBACK-LOG.md` | User corrections log | A correction recurs — promote to MEMORY index |
| `docs/API_DOCUMENTATION.md` | Human-readable API + engine + deployment reference | Endpoint changes, valuation model changes, config additions |
| `docs/openapi.yaml` | Machine-readable API contract (OpenAPI 3.0) | Endpoint signature changes |
| `docs/postman_collection.json` | Postman collection for manual testing | New endpoints, auth changes |
| `docs/refactoring/valuation-engine-upgrade-spec.md` | Upgrade spec | Phase work lands |
| `docs/reviewer/` | Tracked review follow-ups (S-*, W-*) | Item opened, resolved, or re-scoped |
| `docs/bugs/` | Bug tracker | Bug found or fixed |
| `agents/rules/*.mdc` | Workflow rules (shared-workflow, preflight, orchestrator, codeexecution, etc.) | Workflow evolves |
| `agents/roles/*.md` | Role operational definitions (code-architect, backend-architect, qa-debugger, verifier, etc.) | Role responsibilities change |

## Scope Options

- `@docs-update all` — Update all relevant Midas docs
- `@docs-update api` — Update `docs/API_DOCUMENTATION.md` + `docs/openapi.yaml` + `docs/postman_collection.json` after API changes
- `@docs-update architecture` — Update `CLAUDE.md` "Architecture" section after structural changes
- `@docs-update thesis` — Update `docs/THESIS.md` after a phase completes or scope shifts
- `@docs-update agents` — Update `AGENTS.md` loading contract after workflow file moves
- `@docs-update reviewer` — Update `docs/reviewer/` entries after follow-ups are resolved
- `@docs-update valuation` — Update API docs' valuation engine section after model/growth/WACC changes

## Automatic Actions

### Step 1: Identify Changes

Use `git diff` and `git log` to determine the nature of recent changes:

| Change signature | Likely docs to update |
|------------------|----------------------|
| `internal/api/v1/handlers/*.go` | `docs/API_DOCUMENTATION.md`, `docs/openapi.yaml`, `docs/postman_collection.json` |
| `internal/services/valuation/**` | `docs/API_DOCUMENTATION.md` (Valuation Engine section), `docs/THESIS.md` (if phase work) |
| `internal/services/growth/**` | `docs/API_DOCUMENTATION.md` (Growth Estimation section) |
| `internal/services/datacleaner/**` | `docs/API_DOCUMENTATION.md` (Data Quality section) |
| `internal/infra/gateways/**` | `docs/API_DOCUMENTATION.md` (Data Sources section) |
| `internal/config/config.go` | `docs/API_DOCUMENTATION.md` (Configuration Reference) |
| `internal/infra/database/schema.sql` | `docs/API_DOCUMENTATION.md` (Database section in Architecture) |
| `docker-compose*.yml`, `Dockerfile` | `docs/API_DOCUMENTATION.md` (Deployment Guide) |
| `agents/rules/**`, `agents/roles/**` | `AGENTS.md` (Tier 3 rows), `.claude/hooks/load-rules.js` (RULE_FILES if auto-loaded changes) |
| Phase commit (e.g., `Phase 5: X`) | `docs/THESIS.md` (Current State table), `CLAUDE.md` (version if bumped) |

### Step 2: Read Current Documentation

Load only the documentation files that exist. Don't assume; verify with `ls` or `Read` first.

### Step 3: Identify Gaps

Compare code with docs to find:
- Undocumented endpoints or config keys
- Stale version numbers (e.g., `CalculationVersion` bumped in code but not in docs)
- Broken internal links after file moves
- Phases marked "in progress" in THESIS that are actually complete

### Step 4: Generate Updates

Match the existing style. Midas docs use:
- No emojis (user preference — see `CLAUDE.md`)
- Plain ASCII tables for tabular data
- Backticked file paths
- Go-idiomatic examples (curl for API, `go run ./cmd/...` for CLI)

### Step 5: Apply Updates

Apply the diff. Prefer `Edit` over `Write` for existing files to preserve unrelated content.

## Midas-Specific Templates

### New Endpoint in `docs/API_DOCUMENTATION.md`

Add a row to the endpoint table in Section 4 and a detailed block with the following structure:

```markdown
#### {METHOD} /api/v1/{path}

{One-sentence description}

**Permission:** `{permission:scope}`

**Path Parameters:** (if any)

| Parameter | Type | Constraints | Description |
|-----------|------|-------------|-------------|
| `{name}` | {type} | {constraints} | {description} |

**Example Request:**
`​``bash
curl -H "X-API-Key: <key>" \
     "http://localhost:8080/api/v1/{path}"
`​``

**Success Response (200 OK):**
`​``json
{ ... }
`​``

**Error Responses:**

| Status | Code | When |
|--------|------|------|
| 400 | `INVALID_*` | ... |
| 401 | `AUTH_*` | ... |
```

Also update `docs/openapi.yaml` and `docs/postman_collection.json` to match.

### New Configuration Key in `docs/API_DOCUMENTATION.md`

Add a row to the appropriate table in Section 8 (Configuration Reference):

```markdown
| `{yaml.key}` | `{ENV_VAR}` | `{default}` | {description} |
```

### New Valuation Model

Update Section 5.2 (Valuation Models) with a subsection:

```markdown
#### {Model Name}

Used for **{target company category}**.

`​``
{Formula(s)}
`​``

{Explanation of inputs, safety guards, fallback behavior}
```

Also update Section 5.1 pipeline diagram if the model router logic changes.

### New Data Source

Update Section 6 with a subsection:

```markdown
### 6.N {Source Name}

| Field | Value |
|-------|-------|
| **URL** | `{base URL}` |
| **Data** | {what it provides} |
| **Rate Limit** | {limits} |
| **Cache TTL** | {TTL} |

{Usage notes, auth requirements, fallback behavior}
```

### Phase Completion in `docs/THESIS.md`

Add or update a row in the Phases table:

```markdown
| Phase {N}: {Name} | COMPLETE ({YYYY-MM-DD}) | `{commit hash}` | {Key work in one line} |
```

If the phase produces known follow-ups, append them to the Known Follow-Ups table with severity (Warning/Structural) and a short description.

### Workflow File Relocation in `AGENTS.md`

When files under `agents/` move or new tier entries are added:

1. Update the relevant Tier table (Tier 1/2/3/4) with new path
2. Renumber rows to stay sequential
3. Update the File Roles table at the bottom
4. Add a change-log entry at the bottom of `AGENTS.md`:

```markdown
| {YYYY-MM-DD} | {one-line description of the change} |
```

5. If the auto-loaded rule set in `.claude/hooks/load-rules.js` changed, update the hook's `RULE_FILES` constant.

## Required Output Format

```
## Documentation Updated

### Files Updated
| File | Changes |
|------|---------|
| docs/API_DOCUMENTATION.md | {description} |
| docs/openapi.yaml | {description} |
| AGENTS.md | {description} |

### Changes Summary
- Added: {new sections}
- Updated: {modified sections}
- Removed: {deleted sections}

### Cross-Reference Checklist
- [ ] `docs/openapi.yaml` matches `docs/API_DOCUMENTATION.md` endpoint shapes
- [ ] `docs/postman_collection.json` has entries for new endpoints
- [ ] `CLAUDE.md` Important Files table references any new canonical files
- [ ] `AGENTS.md` Tier numbering is sequential
- [ ] No broken internal file links (grep for old paths after renames)
- [ ] `CalculationVersion` in docs matches `internal/services/valuation/service.go`
- [ ] Version in `docs/openapi.yaml` `info.version` reflects current state
```

## Documentation Quality Rules

1. **Accuracy**: Code and docs must match — a lying doc is worse than no doc.
2. **Completeness**: All public API endpoints and config keys documented.
3. **Examples**: Use real Midas tickers (AAPL, TSM, NU, VNQ) and real config values in examples.
4. **Consistency**: No emojis; match existing tone; follow tier structure in `AGENTS.md`.
5. **Currency**: Remove deprecated info rather than bolting new info next to it.
6. **No orphan links**: After a file move, grep for the old path across the whole repo and fix every hit.

## Composability

This skill works with:
- After `@scaffold-module` — document the new module in `CLAUDE.md`
- After implementing a new valuation model — `@docs-update valuation`
- Before `@review-prep` — ensure docs are current
- After a phase lands — `@docs-update thesis`

## Example Usage

### API change
```
User: @docs-update api

AI: [Reads docs/API_DOCUMENTATION.md, docs/openapi.yaml, docs/postman_collection.json]
    [git diff on internal/api/]
    [Identifies new endpoint GET /api/v1/watchlist]
    [Updates all three API doc files with matching content]
    [Runs cross-reference checklist]
```

### Phase completion
```
User: @docs-update thesis

AI: [Reads docs/THESIS.md + last commit message]
    [Detects Phase 5 completion]
    [Updates Current State table row]
    [Updates Known Follow-Ups if review produced any]
    [Adds changelog entry]
```

### Workflow file move
```
User: @docs-update agents

AI: [Detects files moved between agents/ subdirs]
    [Updates AGENTS.md Tier rows with new paths]
    [Updates .claude/hooks/load-rules.js RULE_FILES if auto-loaded set changed]
    [Adds change-log entry to AGENTS.md]
    [Greps repo for orphan references to old paths]
```

## Auto-Detection

When invoked without scope, detect signals from `git diff`:

1. Which directories saw changes (api / services / gateways / config / agents / docs)
2. Whether version strings or phase identifiers moved
3. Whether new files need tier placement in `AGENTS.md`

Map each signal to the table in Step 1 above and update the indicated files.

## Project Ground Truth (Pinned)

These facts are stable and anchor all updates. Update this list if any of them change.

- **Module path:** `github.com/midas/dcf-valuation-api`
- **Go version:** 1.23+ (toolchain 1.24.4)
- **Current version:** `v0.9.0-rc1` (MVP — Phases 0-4 complete)
- **CalculationVersion:** `4.0`
- **Architecture:** Hexagonal (ports & adapters), `uber/fx` DI, `zap` logging
- **Entry point:** `cmd/server/main.go`
- **DI wiring:** `internal/di/container.go`
- **HTTP server:** `internal/api/server.go`
- **Valuation orchestration:** `internal/services/valuation/service.go`
- **DB schema:** `internal/infra/database/schema.sql` (SQLite + PostgreSQL compatible)
- **OpenAPI contract:** `docs/openapi.yaml` (version `0.1.0` — lags behind code version)
- **Canonical loading contract:** `AGENTS.md` (repo root)
