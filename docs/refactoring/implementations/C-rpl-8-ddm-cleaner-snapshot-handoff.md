# Handoff — C: RPL-8 DDM/bank path skips the cleaner snapshot (architecture debt)

**Date filed:** 2026-06-20 · **Status:** READY · **Owner:** next session
**Engine at handoff:** `CalculationVersion 4.8` · **master tip:** `91e0b05`
**Bucket:** C (architecture debt — has a workaround today)

---

## 1. The defect

The DDM/bank valuation path **bypasses the datacleaner-snapshot capture stage**, so
bank/DDM artifact bundles are written **missing `10-clean-output.json`** (and the
corresponding `FinancialData` manifest entry). Replaying such a bundle then requires
the `--allow-schema-drift` flag, and per-stage diffs can't inspect the cleaner output.

**Live repro in the fresh 4.8 baseline (verified 2026-06-20):**
- `artifacts/tier2-baseline/2026-06-20/JPM/req_*/` — **24 files, has `10-clean-input.json` but NOT `10-clean-output.json`.**
- `artifacts/tier2-baseline/2026-06-20/AAPL/req_*/` (DCF path) — **has `10-clean-output.json`.**

So the cleaner *input* is captured for JPM but the *output* snapshot is dropped — the
DDM path stamps stages differently from the DCF path.

Tracker: [docs/reviewer/RPL8-ddm-bank-path-skips-cleaner-snapshot.md](../../reviewer/RPL8-ddm-bank-path-skips-cleaner-snapshot.md).
Same defect class as the **Gap-3** fix (commit `4290266`) — use it as the pattern.

## 2. Goal & scope

**Goal:** DDM/bank bundles carry `10-clean-output.json` + the `FinancialData` manifest
entry, so replays no longer need `--allow-schema-drift` and per-stage diffs work for
banks.

**Two candidate fixes (pick one in the spec):**
- (a) Route the DDM/alt-model path through the **same cleaner-snapshot capture** the
  DCF path uses; or
- (b) Make stage stamping **idempotent via manifest finalization** so the snapshot is
  emitted regardless of which model path ran.

**Non-goals:** no valuation-math change (this is observability/replay plumbing — values
must stay bit-for-bit, incl. `TestDDM_LegacyPath_BitForBit`); not RPL-9 (replay-side
config consumer); no CalcVersion bump (no engine behavior change).

## 3. Mandatory workflow — WORKTREE (per FEEDBACK-LOG 2026-06-04)

**Never edit master's working tree. Work in an isolated git worktree.**

```bash
REPO="c:/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas"
WT="$TEMP/midas-wt-rpl8"          # any path OUTSIDE the Strade tree
git -C "$REPO" worktree add -b fix/rpl-8-ddm-cleaner-snapshot "$WT" master
cd "$WT"
```

- **Build/test in the worktree:** `CGO_ENABLED=1 go test ./... && go build ./cmd/server`.
- **gopls lies in worktrees** — trust CLI `go build`/`go test`.
- **CRLF warnings on commit are benign.**
- **Rebase before merge:** `git -C "$WT" rebase master` (master is co-edited).
- **Guarded FF merge** (main checkout on `master` + tracked-clean):
  `git -C "$REPO" merge --ff-only fix/rpl-8-ddm-cleaner-snapshot`.
- **Cleanup:** `worktree remove` + `branch -d`.
- **NEVER touch** `midas-dc1-phase-5-followup/` or `claude/sharp-morse-*` worktrees.

## 4. Code map

| Where | What |
|---|---|
| [internal/observability/artifact/bundle.go](../../../internal/observability/artifact/bundle.go) | Bundle writer + manifest + stage stamping (the snapshot mechanism). |
| [internal/services/valuation/service.go](../../../internal/services/valuation/service.go) | `performValuation` (DCF, captures cleaner output) vs `performAlternativeValuation` (DDM/FFO/RM — where the snapshot is skipped). Compare the two capture paths. |
| [internal/observability/replay/](../../../internal/observability/replay/) | Replay core + `--allow-schema-drift`, schema/manifest drift detection; the consumer that currently needs the flag. |
| commit `4290266` (Gap-3) | The precedent fix for the same "stage skipped on a path" class. |

**Repro to confirm your fix:** re-capture JPM with `?trace=1` (see
[docs/accuracy/baseline-capture-runbook.md](../../accuracy/baseline-capture-runbook.md))
and assert the bundle now contains `10-clean-output.json`; then
`go run ./cmd/replay --from=raw artifacts/<date>/JPM/req_*/` **without** `--allow-schema-drift`.

## 5. Acceptance criteria

- A freshly captured DDM/bank bundle (JPM) contains `10-clean-output.json` + the
  `FinancialData` manifest entry.
- `cmd/replay` on a bank bundle succeeds **without** `--allow-schema-drift`.
- **Hermeticity invariants preserved** (CLAUDE.md): F11 hermeticity (no DB/Redis/network
  in replay), bundle gateways return `ErrBundleMissingPayload` (not panic) on the
  goroutine path, ephemeral-snapshot-only ("no bundles of bundles").
- Valuation outputs **bit-for-bit unchanged** — `TestDDM_LegacyPath_BitForBit` GREEN.
- Full suite green; lint guards pass (`scripts/lint-prometheus-registers.ps1` — replay
  must use per-instance registries, never `prometheus.DefaultRegisterer`).
- Tracker updated/closed; runbook completeness-check note updated (JPM was the "7/8"
  exception — it should become 8/8).

## 6. Validation (project gate — FEEDBACK-LOG 2026-05-30)

`/execute` B-V-R-Q (VERIFIER + REVIEWER + QA subagents in parallel) + `mcp__zen-mcp__codereview`
with `gpt-5.5` as the independent Q pass. Pay special attention to the replay
import-boundary + hermeticity tests (`cmd/server/import_boundary_test.go`).

---

## STARTING PROMPT (copy-paste)

> Resume midas bucket-C work item **RPL-8 (DDM/bank path skips the cleaner snapshot)**.
> Read `docs/refactoring/implementations/C-rpl-8-ddm-cleaner-snapshot-handoff.md` in full
> and follow it. Live repro confirmed 2026-06-20: `artifacts/tier2-baseline/2026-06-20/JPM/req_*/`
> has `10-clean-input.json` but **no `10-clean-output.json`** (AAPL/DCF has it), so bank
> replays need `--allow-schema-drift`. Fix the alt-model/DDM path in
> `internal/services/valuation/service.go::performAlternativeValuation` to capture the
> cleaner-output snapshot like the DCF path (or finalize the manifest idempotently) —
> pattern: the Gap-3 fix commit `4290266`. This is replay/observability plumbing ONLY:
> valuation values must stay bit-for-bit (`TestDDM_LegacyPath_BitForBit`), no CalcVersion
> bump, and preserve replay hermeticity (F11 / `ErrBundleMissingPayload`-not-panic /
> ephemeral-snapshot-only). Work in an **isolated git worktree off master**; rebase before
> a guarded FF merge. Confirm by re-capturing JPM and replaying it without
> `--allow-schema-drift`. Run `/execute` B-V-R-Q + gpt-5.5 review before merge.
