# Handoff — A-1: VAL-1 archetype-aware horizon + terminal normalization (accuracy)

**Date filed:** 2026-06-20 · **Status:** READY · **Owner:** next session
**Engine at handoff:** `CalculationVersion 4.8` · **master tip:** `91e0b05`
**Bucket:** A (accuracy calibration — the real frontier)

---

## 1. Why this work, now

The engine systematically values our basket **below market**, and the cause is
**terminal-value dominance**: on the DCF names the terminal (Gordon) tail is
**80–89% of enterprise value**, so the explicit 5–10y FCF window barely matters and
small terminal assumptions swing the answer. This is the dominant remaining accuracy
problem after BUG-014/015 (FCF sign fixes) and Layer A (reinvestment model).

A-0 (2026-06-20) freshly **measured this at 4.8** and confirmed SR-1 did *not* move it:
- Report: [docs/accuracy/report-2026-06-20.md](../../accuracy/report-2026-06-20.md) — mean abs gap ~53.8%, `TERMINAL_DOMINANCE` on 6/10, AMD terminal 89%.
- Baseline (oracle input): [artifacts/tier2-baseline/2026-06-20/](../../../artifacts/tier2-baseline/2026-06-20/)
- Oracle: [cmd/accuracy](../../../cmd/accuracy/) (`go run ./cmd/accuracy --dir artifacts/tier2-baseline/2026-06-20`).

VAL-1 **Phase 1 is already done** (diagnostic fields shipped): `dcf_horizon_years`,
`dcf_terminal_method`, `dcf_terminal_pct_of_ev`, `dcf_per_year_pv`,
`dcf_terminal_growth_used`. **Phases 2–5 are open.** Tracker:
[docs/reviewer/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md](../../reviewer/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md).

## 2. ⚠️ Non-negotiable principle (read first)

**The market gap is a screening signal, NEVER an optimization target.** Do not tune
parameters to make intrinsic hug price — that builds a market parrot and defeats the
model. Calibrate to **valuation correctness** (a horizon/terminal choice defensible by
finance theory — e.g. Damodaran), and judge success by the **price-free** columns
(`TERMINAL_DOMINANCE`, `NEG_FCF_YEARS`, `NEG_INTRINSIC`), not by `gap → 0`. A correct
engine still disagrees with the market on genuinely mispriced names. Full rule:
[cmd/accuracy/README.md](../../../cmd/accuracy/README.md) §"The gap is a signal, not a
target" + [docs/FEEDBACK-LOG.md](../../FEEDBACK-LOG.md) (2026-06-20).

## 3. Goal & scope

**Goal:** reduce terminal dominance by giving the DCF an **archetype-aware explicit
horizon** and a **normalized terminal** so more value comes from the modeled window.
This is calibration justified by theory, measured (not targeted) against the oracle.

**In scope (VAL-1 Phases 2–5, sequence per the tracker):**
- Archetype-aware horizon: high-growth/cyclical archetypes get a longer explicit
  window so growth/margins converge *inside* the projection, not in the terminal.
- Cyclical-base normalization: don't terminalize a peak/trough year.
- Exit-multiple terminal option (already a selector — see request-overrides
  `terminal_method`); make the per-archetype default sane.
- Diluted-share-forward adjustment (Phase 5 item).
- New per-archetype params live in the `AssumptionProfile` + `config/assumption_profiles.json`.

**Non-goals:** no change to DDM / FFO / revenue_multiple math (DCF-path only); no
tuning to the gap; no new data sources; not Layer B (filing intelligence).

## 4. Mandatory workflow — WORKTREE (per FEEDBACK-LOG 2026-06-04)

**Never edit master's working tree. Work in an isolated git worktree.**

```bash
REPO="c:/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas"
WT="$TEMP/midas-wt-val1"          # any path OUTSIDE the Strade tree
git -C "$REPO" worktree add -b feat/val-1-archetype-horizon "$WT" master
cd "$WT"                          # do ALL edits + build + test here
```

- **Build/test in the worktree:** `CGO_ENABLED=1 go test ./... && go build ./cmd/server` (sqlite needs CGO).
- **gopls lies in worktrees** (`undefined:` / not-in-workspace) — trust CLI `go build`/`go test`, not the IDE.
- **CRLF warnings on commit are benign** ("LF will be replaced by CRLF").
- **Master is co-edited by parallel sessions** — `git -C "$WT" rebase master` immediately before merging.
- **Guarded FF merge** (only when main checkout is on `master` and tracked-clean):
  `git -C "$REPO" merge --ff-only feat/val-1-archetype-horizon`
- **Cleanup:** `git -C "$REPO" worktree remove "$WT"; git -C "$REPO" branch -d feat/val-1-archetype-horizon`
- **NEVER touch** the excluded worktree `midas-dc1-phase-5-followup/` or the
  unrelated `claude/sharp-morse-*` worktree.

## 5. Process

1. **Brainstorm + write a spec FIRST** (no VAL-1 spec exists yet). Use the
   `brainstorming` skill, then write to `docs/refactoring/spec/val-1-archetype-horizon-spec.md`.
   Resolve: which archetypes get which horizon; normalization method; terminal default
   per archetype; how this composes with Layer A reinvestment + request-overrides.
2. **TDD.** Pin behavior before implementing (project rule). Table-driven tests.
3. Implement DCF-path changes; keep alt-models bit-for-bit.
4. **Re-measure** with the oracle against the 4.8 baseline; capture a fresh
   `4.9` baseline + `docs/accuracy/report-<date>.md` (recipe:
   [docs/accuracy/baseline-capture-runbook.md](../../accuracy/baseline-capture-runbook.md)).
5. Bump `CalculationVersion 4.8 → 4.9` (first production value-change since 4.8) — both
   stamp sites in `internal/services/valuation/service.go` + every `service_test.go`
   version pin + the API-doc examples ([handlers/fair_value.go](../../../internal/api/v1/handlers/fair_value.go), [docs/docs.go](../../docs.go), swagger).

## 6. Code map

| Where | What |
|---|---|
| [pkg/finance/dcf/dcf.go](../../../pkg/finance/dcf/dcf.go) | DCF projection loop + terminal value; the horizon/terminal math. |
| [internal/services/valuation/service.go](../../../internal/services/valuation/service.go) | `performValuation` builds `dcf.Inputs`; stamps `CalculationVersion`. |
| [internal/services/valuation/reinvestment.go](../../../internal/services/valuation/reinvestment.go) | Layer A reinvestment wiring (compose with, don't fight). |
| [internal/services/valuation/profile/](../../../internal/services/valuation/profile/) | `AssumptionProfile` — add per-archetype horizon/normalization fields + `validation.go`. |
| [config/assumption_profiles.json](../../../config/assumption_profiles.json) | Per-archetype param values (bump `config_version`). |
| [internal/services/valuation/params/](../../../internal/services/valuation/params/) | Knob resolution (config→profile→request override) — horizon already resolvable; respect precedence. |

## 7. Acceptance criteria

- `TERMINAL_DOMINANCE` count **falls** on the basket; `NEG_FCF_YEARS` stays 0–1; no `NEG_INTRINSIC`.
- Each change has a **theory rationale** in the spec (not "it closed the gap").
- DDM/FFO/revenue_multiple **bit-for-bit unchanged** — `TestDDM_LegacyPath_BitForBit`, recompute-shadow byte-identity, ledger-basket all GREEN.
- Full suite green: `CGO_ENABLED=1 go test ./...`; lint guards pass (`scripts/lint-logs.ps1`, `scripts/lint-prometheus-registers.ps1`).
- Fresh `4.9` baseline + report committed; tracker updated.

## 8. Validation (project gate — FEEDBACK-LOG 2026-05-30)

Run `/execute` B-V-R-Q: dispatch **VERIFIER + REVIEWER + QA** subagents in parallel,
then `mcp__zen-mcp__codereview` with `gpt-5.5` as an independent Q pass.

## 9. Load-bearing invariants — do NOT break

- DCF-path-only; alt-models bit-for-bit (golden fixtures pinned).
- `recompute-shadow` snapshots byte-identical (`git diff --quiet internal/integration/testdata/recompute-shadow/`).
- Knob precedence config→profile→request-override stays in `params/` only.

---

## STARTING PROMPT (copy-paste)

> Resume midas bucket-A work item **A-1 (VAL-1 archetype-aware horizon + terminal
> normalization)**. Read `docs/refactoring/implementations/A1-val-1-archetype-horizon-handoff.md`
> in full and follow it. Engine is at `CalculationVersion 4.8` (master `91e0b05`); the
> fresh oracle baseline is `artifacts/tier2-baseline/2026-06-20/` with report
> `docs/accuracy/report-2026-06-20.md` (terminal dominance 80–89% on DCF names is the
> target problem). **CRITICAL:** the market gap is a screening signal, NEVER an
> optimization target — calibrate to valuation correctness, judge by the price-free
> columns (`TERMINAL_DOMINANCE`/`NEG_FCF_YEARS`), per `cmd/accuracy/README.md` §"The gap
> is a signal, not a target" and FEEDBACK-LOG 2026-06-20. Work in an **isolated git
> worktree off master** (never edit the main checkout); rebase before a guarded FF
> merge. Start by using the `brainstorming` skill and writing
> `docs/refactoring/spec/val-1-archetype-horizon-spec.md`; then TDD the DCF-path change,
> keeping DDM/FFO/revenue_multiple bit-for-bit. Re-measure with `cmd/accuracy`, bump
> CalcVersion 4.8→4.9, and run `/execute` B-V-R-Q + gpt-5.5 review before merge.
