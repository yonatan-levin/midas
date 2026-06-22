# TODO Burn-Down — FINAL Closeout (all 12 TDB items COMPLETE)

**Date:** 2026-06-09 · **Local master tip:** `3d6cadf` (NOT pushed — origin held back pending concurrent Layer A/B work) · **Engine:** `CalculationVersion 4.7` (Layer A reinvestment model).

This supersedes the partial `todo-burndown-part2-closeout.md` (which covered only TDB-7/1/2). All twelve prioritized backlog issues filed from the TODO-catalog burn-down — **TDB-1 … TDB-12 (GitHub #1–#12)** — are SHIPPED to local master and CLOSED on GitHub. Each went through the same cycle: worktree off local master → ARCH spec/plan (design-heavy items) → BACKEND/TDD → VERIFIER + REVIEWER + QA → docs (CLAUDE.md gotcha + tracker) → `--no-ff` merge "Closes #N" → merged-tree validation → GitHub comment + close.

## The twelve items

| Issue | Item | Disposition | Merge |
|---|---|---|---|
| **#1 / TDB-1** | SEC parser populates restructuring / litigation / capitalized-interest → C1/C3/C6 fire | DONE | `21fb60f` |
| **#2 / TDB-2** | Implement A6 (ROU) + A7 (excess-cash) adjusters (were `enabled:true`, silently skipped) | DONE | `b82035c` |
| **#3 / TDB-3** | Contingent-liability AI-failed fallback uses the industry heuristic (not flat 0.40) | RESOLVED | `0ff62a6` |
| **#4 / TDB-4** | Per-adjustment audit log (`logctx` `trace.`) + `datacleaner_adjustments_total` counter | DONE | `034f9bc` |
| **#5 / TDB-5** | Externalize the 9 flat asset-rule gate thresholds (defaults==constants) | DONE | `b328463` |
| **#6 / TDB-6** | Cloud deploy config — Docker Compose prod env template + ops runbook | DONE | `3d6cadf` |
| **#7 / TDB-7** | Delete proven-dead code (applyRule chain, getCompanySize, IntegrationService) | DONE | `18f4ec6` |
| **#8 / TDB-8** | Inventory-turnover obsolescence flag refinement | DONE | `39fb1ef` |
| **#9 / TDB-9** | Industry-mapping expansion | RESOLVED — **documented defer** (classifier emits only `{45,20,25}`; bare TODO → tracked reference) | `4eb27d7` |
| **#10 / TDB-10** | Residual XBRL-matcher / flag-evaluator sub-TODOs | DONE — 4 IMPLEMENT + 3 DE-SCOPE; zero bare TODOs | `5b432ac` |
| **#11 / TDB-11** | Expose `cleaning_adjustments` on the fair-value API | DONE | `220bf6e` |
| **#12 / TDB-12** | SEC parser populates contingent-liability fields → B3 fires in production | DONE | `d9cf8b1` |

## Validation (cumulative merged master `3d6cadf`)
- `go build ./...` + `go vet ./...` exit 0; full `go test ./... -count=1` = **50 packages ok, 0 FAIL**.
- Load-bearing invariants byte-identical at every merge: `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC), recompute-shadow snapshots (`git diff --quiet internal/integration/testdata/recompute-shadow/` exit 0), `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_*` (incl. T2-BS-3 AMD/KO).
- **Live API verification (2026-06-09):** server built from `3d6cadf`, booted, drove AAPL/JPM/KO fair-value requests — all HTTP 200, `calculation_version 4.7`, coherent valuations. Three-way data consistency confirmed: API `cleaning_adjustments` (TDB-11) == `trace.datacleaner.adjustment` audit logs (TDB-4) == `datacleaner_adjustments_total` Prometheus counter (TDB-4), all from the one `adjustmentsFromLedger` projection. TDB-9's "classifier emits only `{45,20,25}`" confirmed live (JPM + KO → heuristic `20`).

## Engine / schema versions
`CalculationVersion 4.7` (Layer A). The TDB items are **output-neutral** except TDB-1 + TDB-12, which only move valuations for filers that report the newly-parsed fields (the intended correction) — none bumped `CalculationVersion`. `SchemaVersion["FinancialData"]` is `10` (TDB-2's `OperatingLeaseRightOfUseAsset` field; bumped 9→10 in TDB-2).

## Discovered follow-up (OUT of the TDB set)
- **GitHub #13** — `DATABASE_DRIVER=postgres` does not boot: no `lib/pq`/`pgx` driver imported (`sqlx.Connect("postgres", …)` fails at `internal/di/container.go:427`); `cmd/migrate`/`cmd/seed-demo-key` are SQLite-only. Surfaced by REVIEWER during TDB-6. The TDB-6 template defaults `sqlite3`; the runbook documents the gap. A real code defect for a future session.

## Remaining (non-blocking, operator / future)
- TDB-1 / TDB-12 operator live-replay verification on a fresh CalcVersion-4.7 baseline (the `artifacts/tier2-baseline/` is older, drift-confounded).
- The advisory NITs folded into each tracker (e.g. TDB-10 regex-cache mutex; TDB-4 `range`-value loop) — none blocking.
- GitHub #13 (Postgres driver) — the one code follow-up.

## Doc locations
- Burn-down catalog (reconciled): `docs/integration/TODO_TASKS_CATALOG.md` — see the "2026-06-08/09 — TDB BURN-DOWN COMPLETE" pass.
- Per-item trackers (archived): `docs/reviewer/archive/TDB-*.md`.
- Per-item specs/plans (live design record): `docs/refactoring/spec/tdb-*.md` + `docs/refactoring/implementations/tdb-*.md`.
- Partial (TDB-7/1/2) closeout: `docs/reviewer/todo-burndown-part2-closeout.md`.
- Obsolete handoff (archived): `docs/reviewer/archive/todo-burndown-next-session-handoff.md`.
