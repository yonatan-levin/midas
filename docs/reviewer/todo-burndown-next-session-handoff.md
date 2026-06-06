# TODO Burn-Down — Next-Session Handoff

**Written:** 2026-06-06 · **Repo state:** `origin/master` (everything below is merged + pushed, tip `8521611` at write time) · **Engine:** `CalculationVersion 4.6`.

> **Read first (mandatory):** `AGENTS.md` → `CLAUDE.md` → this file. Then `docs/integration/TODO_TASKS_CATALOG.md` (the "2026-06-05" + "2026-06-06" passes) for the full reconciliation, and the per-task trackers under `docs/reviewer/TDB-*.md`.

---

## 1. Where things stand (DONE — do not redo)

The "TODO burn down" session reconciled `docs/integration/TODO_TASKS_CATALOG.md` against the live code and shipped these to `origin/master`:

- **API docs:** swaggo annotations on the two route-wired `/api/v1` handlers (`DetailedHealthCheck`, `GetMetrics`); `docs/swagger.{json,yaml}` regenerated (additive). Server-info already existed in `cmd/server/main.go:25-42`; fair-value already documented.
- **Staging:** `scripts/launch_staging.sh` now wires `go run ./cmd/migrate` + `./cmd/seed-demo-key`.
- **Dead code removed:** the performance-dashboard handler (`handlers/performance.go` + 2 test files, ~1,900 lines) — it was wired to **no production route**.
- **Two 2025 TODOs proven obsolete and CLOSED** (the project moved past them): "Financial Data Extraction (9 sites)" was dead code (`applyRule` chain, zero callers; 7/9 superseded by DC-1 adjusters) and "Company Size" was orphaned (producer-only, zero consumers).
- **Backlog filed:** 10 prioritized GitHub issues **#1–#10** + mirrored trackers `docs/reviewer/TDB-1..TDB-10-*.md` (issue #N ⟷ `TDB-N`).
- **Verified:** `go build ./...` + `go vet` clean; `go test ./... -count=1` = **47/47 packages ok, 0 failures**.

---

## 2. The prioritized backlog (what's NEXT)

| Issue | Tracker | Priority | Title | Ready to execute? |
|------|---------|----------|-------|-------------------|
| **#1** | `TDB-1-parser-restructuring-litigation-capex-not-populated.md` | **P1** | SEC parser doesn't populate restructuring/litigation/capitalized-interest → C1/C3/C6 never fire | Needs `/plan-and-create` (XBRL tag mapping) |
| **#2** | `TDB-2-missing-a6-rou-a7-excess-cash-adjusters.md` | **P1** | A6 (ROU) & A7 (excess-cash) adjusters enabled in config but unimplemented (silently skipped) | Small plan or decide to disable rules |
| **#3** | `TDB-3-contingent-liability-ai-footnote-analysis.md` | **P1** | Replace contingent-liability probability heuristic with AI footnote analysis | Needs a plan (reuse B3 AI infra) |
| **#4** | `TDB-4-adjustment-monitoring-and-audit-logging.md` | **P2** | Monitoring metrics + audit logging for adjustments | Logging half ready; metrics needs a decision |
| **#5** | `TDB-5-externalize-adjustment-thresholds-config.md` | **P2** | Externalize adjustment thresholds to config | Needs config-schema design |
| **#6** | `TDB-6-cloud-deployment-config-variables.md` | **P2** | Cloud deployment config variables | **Blocked** on target-platform decision |
| **#7** | `TDB-7-dead-code-cleanup-applyrule-getcompanysize.md` | **P3** | Delete dead code (applyRule chain, getCompanySize, orphaned IntegrationService) | **READY — zero behavior change** |
| **#8** | `TDB-8-inventory-turnover-obsolescence-analysis.md` | **P3** | Inventory turnover for obsolescence detection | Ready (small) |
| **#9** | `TDB-9-industry-mapping-expansion.md` | **P4** | Expand industry mapping coverage | Under-specified — needs a concrete list |
| **#10** | `TDB-10-residual-subtodos-xbrl-flag-evaluator.md` | **P4** | Residual sub-TODOs in XBRL matcher / flag evaluator | Triage-then-act |

---

## 3. Recommended next tasks (by priority)

**Primary — #1 / TDB-1 (P1, highest value).** It's the only item that *actively corrupts valuations today* and isn't handled elsewhere: the SEC parser never populates `RestructuringCharges` / `LitigationSettlements` / `CapitalizedInterest`, so earnings-normalization adjusters C1/C3/C6 don't fire on real filings (C1 falls back to a 1.5%-of-revenue guess). Start with `/plan-and-create` (which XBRL/us-gaap+IFRS concepts, per-statement mapping), then `/execute`.

**Quick win — #7 / TDB-7 (P3, zero-risk).** If you want a guaranteed-green warm-up first: delete the proven-dead `applyRule` chain (`datacleaner/service.go:712-1047`, `nolint:unused`, zero callers), `getCompanySize`, and the orphaned `alerting.IntegrationService`. No behavior change; the full suite already passed without them on the call graph. Good `/execute` candidate.

**Sibling to #1 — #2 / TDB-2 (P1).** Decision-first: implement A6/A7 adjusters or remove the dangling `enabled:true` rules. Pairs naturally with #1 (both are parser/adjuster-layer correctness).

Suggested order: **#1 → #2 → #3** (P1 correctness), with **#7** as an optional warm-up or filler.

---

## 4. How to work (process reminders for this repo)

- **Always work in a git worktree, never on `master`** (use the `EnterWorktree` tool). ⚠️ Windows gotcha: `gopls` holds a directory-watch handle, so worktree dirs often can't be auto-deleted at the end — remove the folder from the editor workspace first, then delete. (There are already several orphaned dirs under `.claude/worktrees/`; harmless, git-untracked.)
- **Run the tests for real:** `go test ./... -count=1` (the suite is fast, ~70s; `sec` package is the slow one at ~22s).
- **Load-bearing invariants — must stay green** when touching datacleaner/valuation: `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC), `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, shadow snapshots byte-identical (`git diff --quiet internal/integration/testdata/recompute-shadow/`), `TestLedger_BasketSnapshot_*`. Never regenerate DDM goldens to make a test pass.
- **swag regen gotcha:** if you touch API annotations, regenerate with the **module-pinned** tool — `go run github.com/swaggo/swag/cmd/swag init -g cmd/server/main.go -o docs/ --parseInternal --parseDependency` — NOT a global `swag` binary (avoids the `LeftDelim`/`RightDelim` v1.16-vs-v1.8.12 build break; see `docs/refactoring/spec/swag-version-alignment-spec.md`). Verify with whole-module `go build ./...`.
- **GitHub tracking:** issues #1–#10 exist; transition labels `planning → in-progress` (etc.) as you work, and reference `#N` in commits. Helper: `~/.claude/skills/github-tracking/scripts/gh-issue.sh`.
- **Update the tracker + catalog** when you close an item (mark the `TDB-*.md` Status, flip the catalog row).

---

## 5. Starting prompt (paste into the next session)

> Read `AGENTS.md`, `CLAUDE.md`, and `docs/reviewer/todo-burndown-next-session-handoff.md`. We're burning down the prioritized TODO backlog (GitHub issues #1–#10 / `docs/reviewer/TDB-*.md`).
>
> Start with the **P1, highest-value** item: **TDB-1 / issue #1** — the SEC parser doesn't populate `RestructuringCharges` / `LitigationSettlements` / `CapitalizedInterest`, so the C1/C3/C6 earnings-normalization adjusters never fire on real filings. Read `docs/reviewer/TDB-1-parser-restructuring-litigation-capex-not-populated.md`, **work in a worktree (not master)**, and take it through `/plan-and-create` first (which XBRL/us-gaap+IFRS concepts to map, where in `sec/parser.go` to populate them), then `/execute`. Keep the load-bearing invariants green and run `go test ./... -count=1`.
>
> If you'd rather bank a zero-risk quick win first, do **TDB-7 / issue #7** (delete the proven-dead `applyRule` chain + `getCompanySize` + orphaned `IntegrationService`) via `/execute` instead. Confirm which one before starting.

---

## 6. Key references

- `docs/integration/TODO_TASKS_CATALOG.md` — full reconciliation (2026-06-05 + 2026-06-06 passes).
- `docs/reviewer/TDB-1..TDB-10-*.md` — per-task trackers (mirror of issues #1–#10).
- GitHub: `yonatan-levin/midas` issues **#1–#10** (labels `P1`–`P4`, `planning`).
- `docs/refactoring/spec/swag-version-alignment-spec.md` — swagger regen playbook.
