# Handoff — Valuation-Accuracy stream (next session)

**Date:** 2026-06-06
**Status:** READY — pick up at Priority 1 below.
**Scope of this stream:** DCF valuation accuracy/calibration. Distinct from the parallel **TODO burn-down (TDB-1..TDB-10)** stream, which has its own handoff at `docs/reviewer/todo-burndown-next-session-handoff.md` (datacleaner/adjuster/config/deployment work). Coordinate to avoid master collisions — see Gotchas.

---

## Starting prompt (paste into the next session)

> Resume the midas **valuation-accuracy** work. First read `docs/refactoring/implementations/valuation-accuracy-next-session-handoff.md` and the spec `docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md`.
>
> Implement **Phase 1 = Layer A** of that spec: the **reinvestment / operating-leverage model** in midas's DCF (sales-to-capital ratio, or declining capex-intensity, parameterized per archetype in the `AssumptionProfile` system) so projected FCF can cross positive within the explicit horizon for hypergrowth, reinvestment-heavy firms. Work on a **fresh worktree/branch off master** and run the full **`/execute` B-V-R-Q** cycle. Bump `CalculationVersion` 4.6 → 4.7. Preserve DDM/FFO/revenue_multiple bit-for-bit (DCF-path only) and the load-bearing invariants (`TestDDM_LegacyPath_BitForBit`, recompute-shadow byte-identity). Honor the accounting-consistency guardrails in spec §7 (maintenance-capex floor; capped margin expansion; terminal reinvestment consistent with terminal growth & ROIC).
>
> **Validate against the regression oracle:** `go run ./cmd/accuracy --dir artifacts/tier2-baseline/2026-06-05` before vs after — target: AMD's per-year `dcf_per_year_pv` turns positive in-window, terminal-dominance share drops, mean absolute gap shrinks from 59.1%, no regressions. Then do a **live run + log validation** (the user wants this): start the server, value KO/AMD/NVDA, confirm via the narrate/`17-response` logs that FCF/intrinsic improved.
>
> Before merging: rebase onto latest master (a parallel session is actively committing to master) and use a **guarded** merge. Do not work in the shared main checkout.

---

## What this session shipped (all merged to master)

| Item | Commit(s) | Result |
|---|---|---|
| `cmd/accuracy` offline accuracy harness + tests + 4.4 baseline | `19c8e5c` (merge) | The regression oracle. Surfaced the engine was ~87% off. |
| **BUG-014** — DCF working capital now excludes cash | `90ea8f5` (merge) | Cash-rich negatives fixed (NVDA/AAPL/MSFT). CalcVersion → 4.5. |
| Shadow-test baseline pin (stop auto-repoint churn) | in `90ea8f5` | `git diff --quiet recompute-shadow/` stays clean. |
| **BUG-015** — DCF operating-income base TTM-annualized for 10-Q filers | `1d4e853` | KO/AMD flip from negative to positive. CalcVersion → 4.6. |
| 4.6 baseline + report (live validation) | `5db1df7` | mean gap 86.7%→59.1%; NEG_INTRINSIC 2→0; NEG_FCF_YEARS 4→1. |
| **Spec** — reinvestment/operating-leverage + filing-intelligence | `f271378` | The plan for the work below. |
| Archived stale-resolved trackers RM-1, RM-3, VAL-2 | `c9ae4ce` | Moved to `docs/reviewer/archive/` (fixes had shipped; headers were stale). |

**Engine is at `CalculationVersion 4.6`.** The two *defects* are fixed; what remains is the *calibration* gap (operating leverage).

---

## Prioritized next tasks (this stream)

### P1 — Implement Layer A: reinvestment / operating-leverage model  ⬅ start here
- **Why first:** highest leverage, **no new data**, oracle already in place. It's the fix that stops the DCF undervaluing hypergrowth — AMD's intrinsic is positive but its per-year FCF is still negative (terminal = 247% of EV) because the engine scales cost and growth in lockstep (`FCF_t = growth_factor × FCF_base`).
- **What:** Spec Phase 1 — `pkg/finance/dcf/dcf.go` projection loop (~100–165) + `internal/services/valuation/service.go` `dcf.Inputs` (~1088–1208) + new per-archetype reinvestment params in the `AssumptionProfile` (`profile.go` + `config/assumption_profiles.json`).
- **Spec:** `docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md` §5–§7. Resolve Open Question §12 first: sales-to-capital vs declining-capex-intensity, and surgical-patch vs FCFF refactor.

### P2 — DC-1 phase-5 operator replay verification
- The fresh-baseline half is now satisfied (4.6 baseline captured this session). **Still open:** confirm the DDM `DebtLikeClaims`→EV correction live on a **B-rule-firing bank** (JPM/BAC/WFC fire zero B-rules). Tracker: `docs/reviewer/DC-1-phase-5-replay-verification-followup.md`. Candidate banks + steps in `docs/accuracy/baseline-capture-runbook.md` §4.2.

### P3 — Layer B: Filing Intelligence (AI-on-MD&A) — sequenced after Layer A
- Spec Phases 2–3. **Phase 2 first:** define the guidance-artifact contract + the **assumption-authority hierarchy** (§9) and make midas consume a **hand-authored fixture artifact deterministically** — *before* writing any LLM code. Then build the offline, accession-keyed extraction tool (Python, separate repo/harness; not a service initially). Do NOT let it block or replace Layer A.

### Parallel (not this stream) — TODO burn-down TDB-1..TDB-10
- Owned by the parallel session; see `docs/reviewer/todo-burndown-next-session-handoff.md`. Mostly datacleaner/adjuster/config/deployment. Listed here only so the next session knows it exists and doesn't double-work or collide on master.

---

## Key artifacts / pointers
- **Regression oracle:** `cmd/accuracy` (README at `cmd/accuracy/README.md`); 4.6 baseline at `artifacts/tier2-baseline/2026-06-05/`; report `docs/accuracy/report-2026-06-05.md`.
- **Capture runbook:** `docs/accuracy/baseline-capture-runbook.md` (how to re-capture a baseline live; §4 operator residuals).
- **Spec:** `docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md`.
- **Bug history:** `docs/bugs/BUG-014-*.md`, `docs/bugs/BUG-015-*.md`.

## Gotchas (multi-session reality + environment)
- **Master is actively co-edited** by the parallel TODO-burndown session — it advanced ~4× during this session. ALWAYS `git rebase master` before merging and use a **guarded** merge (verify main checkout is on master + tracked-clean, or fetch-ff when master isn't checked out). Never edit in the shared main checkout.
- **Work in a worktree off master**, not the main checkout (avoids cross-session contamination).
- **CRLF autocrlf** noise on every commit ("LF will be replaced by CRLF") — benign; `git checkout -- <path>` clears spurious " M" line-ending diffs if they block a rebase.
- **gopls in worktrees** reports false `undefined:`/`not in workspace` errors (worktree not in `go.work`) — trust CLI `go build ./...` / `go test ./...`, not the IDE.
- **Leftover worktrees** may linger from this session: `accuracy-harness` (orphaned dir, process-locked) and `dcf-quarterly-base` — remove with `git worktree prune` / `git worktree remove` if present.
- **Live capture needs:** dev-mode server (`ENVIRONMENT` unset → artifact capture on), `?trace=1`, a seeded demo key (`cmd/seed-demo-key`), first-request-per-ticker = cold cache for a full bundle. No `FRED_API_KEY` is fine (config-fallback treasury curve). Nothing is pushed — master is local-only.
