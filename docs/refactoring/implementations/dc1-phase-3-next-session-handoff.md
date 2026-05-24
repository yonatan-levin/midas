# DC-1 Phase 3 — Next-Session Handoff

**Date:** 2026-05-24
**Status:** READY FOR PHASE 3 BACKEND DISPATCH
**Master tip:** `3238d61` (DC-1 Phase 2 4-PR stack merged) + this archive-cleanup commit

---

## TL;DR for the next session

The DC-1 Phase 2 datacleaner refactor is **merged to master**. All 17 cleaner-side adjusters implement the `Adjuster` interface natively across 4 role flavors (OverlayEmitter, Restater, Restater+TaxShieldDTA, FlagEmitter). The PR-1 orchestrator shim and both shim helpers (`shimLedgerEntriesFromLegacy` + `shimLedgerEntriesFromLegacyExcluding`) are FULLY DELETED. SchemaVersion is at 8. The full 4-PR stack landed via merge commit `3238d61` with all load-bearing invariants GREEN (`TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, shadow byte-identity, basket integration). Live `/api/v1/fair-value/AAPL` returns HTTP 200 with a valid valuation against real SEC/Yahoo.

**Phase 3 is the next architectural gate.** Its goal: introduce `CleanedFinancialData` with `AsReported()` / `Restated()` / `InvestedCapital()` view accessors that consume the Phase 2 LedgerEntry + OverlaySpec emissions. Phase 3 ships **zero downstream behavior change** (accessors are additive; the existing `data.*` read sites continue to work unchanged); consumer migration is Phase 4. Phase 3 also resolves Q2 (A2 TaxShieldDTA actual population) and Q4 (B3 AIProvenance SHA-256 hash computation) and threads `ctx context.Context` through `Process*Adjustments`.

**Spec is authored, plan is ready, gate is green.** BACKEND can dispatch immediately.

---

## What landed (this session, 2026-05-22 → 2026-05-24)

| Date | Milestone | Branch / Commit |
|---|---|---|
| 2026-05-22 | DC-1 Phase 2 PR-2: 6 A-rules native + asset-shim deletion + SchemaVersion 7→8 | `dc1-phase-2-pr-2` tip `2e8f83b` |
| 2026-05-22 | DC-1 Phase 2 PR-3: 7 C-rules native + earnings-shim deletion | `dc1-phase-2-pr-3` tip `207f41a` |
| 2026-05-22 → 2026-05-23 | DC-1 Phase 2 PR-4: 3 B-rules native + Option α orchestrator absorption + PR-1 shim FULLY deleted + basket integration test + Phase 2 closeout docs + 3 NIT fixes | `dc1-phase-2-pr-4` tip `cc4d8aa..c275a79` |
| 2026-05-23 | DC-1 Phase 3 ARCH spec + implementation plan authored | `ed1dadd` (on PR-4 branch) |
| 2026-05-24 | 4-PR stack MERGED to master via `--no-ff` (3 doc conflicts resolved combining HEAD's archive-path updates with PR-4's Phase 2 SHIPPED content) | master merge commit `3238d61` |
| 2026-05-24 | Phase 2 docs archived (this commit) | this commit |

**Aggregate diff for Phase 2 across the 4 PRs:** ~36 commits, +~9,000 / -~200 LOC across `internal/services/datacleaner/adjustments/*.go`, `internal/services/datacleaner/service.go`, `internal/core/entities/adjustment_ledger.go` (new), `internal/integration/datacleaner_ledger_basket_test.go` (new), `internal/observability/replay/schema.go`, plus 10 new + 6 modified doc files.

---

## What's on master right now (verified fresh this session)

- All 17 cleaner adjusters native; PR-1 shim deleted (grep returns zero callers, zero function definitions for `shimLedgerEntriesFromLegacy*`).
- `SchemaVersion["FinancialData"]` = 8 (atomic with PR-2 Task 2.1).
- 4 role flavors locked: OverlayEmitter (A1, B1, B2, B3), Restater (A2, A4, C1/C2/C3/C5/C6), Restater+TaxShieldDTA (A5 only), FlagEmitter (C4, C7, plus 2 PR-2 reviews).
- B3 `OverlaySpec.Field:"DebtLikeClaims"` recording Phase 4 routing intent (dual-write still mutates `data.TotalDebt` for B3 in Phase 2/3; Phase 4 flips consumer).
- C6 LOAD-BEARING `EquityOffset=0` for capitalized-interest reclassification (Phase 3's `Restated()` MUST NOT add C6's DeltaAmount to retained earnings — pinned by `TestC6CapitalizedInterestAdjuster_Adjuster_Interface_Contract` and dispatcher test).
- T2-BS-3 Option B carve-out documented (AMD/KO `AsReported.TotalLiabilities=0` preserved; Phase 3 `Restated` view reconstruction from sum(components)+plug fixes downstream consumption).
- Q1 SHIPPED (PR-1 `recompute` WARN `recent_adjusters`). Q3 SHIPPED (PR-2 Task 2.7 A-FY-NULL tracker at `docs/reviewer/DC-1-FY-enable-predicate-investigation.md`). Q2 + Q4 DEFERRED to Phase 3 (designs in Phase 3 spec).
- New `TestLedger_BasketSnapshot_ClusterPrediction` integration test passing 10/10 basket tickers (AAPL, MSFT, JPM, AMD, KO, F, EQIX, MXL, JNJ, TSM, BABA).
- All load-bearing invariants GREEN: `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC bit-for-bit), `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, shadow byte-identity.
- Full `go test ./... -count=1` GREEN across 30+ packages.
- Live API verified: `GET /api/v1/fair-value/AAPL` HTTP 200 in 1.97s with full valuation (WACC 9.54%, DCF $19.28/share, quality A, multi_stage_dcf v4.1).

---

## What's NEXT — Phase 3

### Spec + Plan ready to consume

- **Spec:** `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md` (615 lines, status DESIGN, authored 2026-05-23)
- **Plan:** `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md` (289 lines, status READY FOR BACKEND DISPATCH, Tasks 3.1-3.14)

### Phase 3 goals

1. Introduce `internal/services/datacleaner/cleaneddata` package with `CleanedFinancialData{raw, asReported, restated, investedCap}` + `FinancialDataView` DTO + `ViewKind` enum.
2. Implement 3 view accessors:
   - **`AsReported()`** — preserves parser-stamped values verbatim (T2-BS-3 carve-out: AMD/KO TotalLiabilities=0 stays)
   - **`Restated()`** — reconstructs balance sheet from `sum(components) + plug`, applying `LedgerEntry.EquityOffset` / `TaxShieldDTA` for fired Restater-role adjusters; AMD/KO get truthful TotalLiabilities here
   - **`InvestedCapital()`** — applies OverlaySpec entries: B1+B2+B3 → DebtLikeClaims, A1 goodwill exclusion per Damodaran
3. **Q2 resolution:** A2 populates `TaxShieldDTA = writedownAmount × working.EffectiveTaxRate` when rate > 0 (mirrors A5).
4. **Q4 resolution:** B3 `AIProvenance.PromptHash` + `SourceDocHash` = SHA-256 hex of rendered-prompt + footnote-text, computed in `captureB3AIProvenance` pre-API-call.
5. Thread `ctx context.Context` through `Process*Adjustments` signatures (Asset, Liability, Earnings).
6. New `Service.CleanWithViews(ctx, ...)` sibling method (additive; no signature changes to existing `Clean`).
7. **Translator-extraction decision:** LOCKED as KEEP per-rule (Phase 4 deletes alongside dual-write deletion).

### Phase 3 NON-goals

- B3 routing flip (Phase 4)
- Consumer migration of 13 valuation read sites (Phase 4)
- Dual-write deletion (Phase 4)
- CalculationVersion bump (Phase 4 — accessors are additive; consumer behavior unchanged)
- SchemaVersion bump in this Phase 3 commit (plan documents 8→9 atomic with first populating implementation commit; do NOT bump in any docs-only commit)

### Phase 3 → Phase 4 gate

All Phase 3 invariants green AND `TestLedger_BasketSnapshot_ClusterPrediction` extended to assert `Restated.TotalLiabilities` reconstruction for AMD/KO produces non-zero (T2-BS-3 acceptance criterion).

---

## Stack ladder (read in this order to bootstrap)

For a new session that wants to pick up Phase 3:

1. **`CLAUDE.md`** — DC-1 Phase 2 SHIPPED 2026-05-23 bullet (consolidated; describes all 17 native adjusters + 4 role flavors + Q-disposition status + T2-BS-3 + load-bearing invariants).
2. **`AGENTS.md`** Tier 4 row 17b — DC-1 status + spec/plan link cluster.
3. **`docs/THESIS.md`** DC-1 row — Phase 2 SHIPPED + Phase 3 spec authored.
4. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — parent spec (Phase 0 through Phase 4 phasing).
5. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md`** — Phase 3 spec (the design BACKEND consumes).
6. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-3-implementation-plan.md`** — Phase 3 implementer plan (Tasks 3.1-3.14, acceptance gates, gotchas).
7. **`docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md`** — Phase 2 closeout (the inputs Phase 3 consumes; Q-resolutions, role taxonomy, what was deferred and why).
8. **`docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`** — DC-1 tracker (per-phase progress paragraphs).
9. **`internal/core/entities/adjustment_ledger.go`** — entity shapes Phase 3 reads by name (LedgerEntry, OverlaySpec, AdjustmentLedger, AmountSemantics, AIProvenance).
10. **`internal/services/datacleaner/adjustments/`** — 17 adapter files + 17 test files; the canonical pattern Phase 3 view accessors consume.

---

## Worktree workflow for Phase 3

Per the established `feedback_worktree_first_workflow` memory:
- Main `midas/` stays on `master` (currently at `3238d61` post-Phase-2-merge + archive-cleanup commit).
- Phase 3 work in a sibling worktree at `../midas-dc1-phase-3/` branched off master.

```bash
# From the main midas/ directory (on master)
git worktree add ../midas-dc1-phase-3 -b dc1-phase-3 master
cd ../midas-dc1-phase-3

# Verify before EVERY commit:
pwd                                    # should end with midas-dc1-phase-3
git rev-parse --abbrev-ref HEAD        # should print dc1-phase-3
git worktree list                      # should show main midas at master + midas-dc1-phase-3
```

The Phase 2 worktrees (`midas-dc1-phase-2-pr-1`, `..-pr-2`, `..-pr-3`, `..-pr-4`) can be cleaned up via `git worktree remove` once you're certain you don't need to inspect them. They're stale relative to merged master but still hold useful per-PR per-task commit history.

---

## Acceptance gates for Phase 3 (run before every commit)

```bash
go build ./...
go test ./internal/services/datacleaner/... -count=1
go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1   # LOAD-BEARING
go test ./internal/services/datacleaner/ -run 'TestOrchestrator_LedgerOrdering|TestRecomputeUmbrellas_NoMutation' -count=1
go test ./internal/integration/... -run 'TestLedger_BasketSnapshot_ClusterPrediction|TestDataCleanerRecompute_ShadowMode_TickerBasket' -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/   # MUST exit 0
go test ./... -count=1   # full suite
```

For final Phase 3 closeout, run replay against AAPL/MSFT/JPM bundles + a live API spot-check.

---

## Known deferred work (NOT Phase 3 scope; flagged for visibility)

| Item | Status | Owner |
|---|---|---|
| Operator baseline refresh (`artifacts/tier2-baseline/`) — needs `valuation_cache` clear before re-capture | DEFERRED | Operator (cache-bypass + 10-ticker capture sequence) |
| Phase 4: 13-site consumer migration + B3 routing flip + dual-write deletion | DEFERRED to Phase 4 | Phase 4 ARCH |
| `T2-P4-W1` tracker — 2 deferred acceptance rows (live API regression on EQIX+PLD + replay regression) | OPEN | Tier 2 Closeout follow-up |
| `T2-BS-3` tracker — Option B disposition; parser fix DEFERRED | OPEN | Phase 3 Restated view fixes downstream; parser fix is separate |


---

## Change log

| Date | Change |
|---|---|
| 2026-05-24 | Initial filing post Phase 2 merge to master. Documents the 4-PR stack outcomes + Phase 3 readiness + bootstrap ladder + ready-to-copy starting prompt. Anchored at master `3238d61` + this archive-cleanup commit. |
