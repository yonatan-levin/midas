# DC-1 Phase 4 — Next-Session Handoff

**Date:** 2026-05-26
**Status:** READY FOR PHASE 4 BACKEND DISPATCH
**Master tip:** `3490227` (DC-1 Phase 3 followup merge) — Phase 4 spec + plan await on `dc1-phase-4-prep` branch (`adfae3b` ARCH spec + plan; `515a957` post-merge doc sweep)

---

## TL;DR for the next session

DC-1 Phase 3 + Phase 3 followup are MERGED to master. The 11-commit Phase 3 work landed 2026-05-25 as `46e84b1` (introduces `cleaneddata` package + Q2/Q4 resolutions + ctx threading); the 12-commit Phase 3 followup landed 2026-05-26 as `3490227` (closes 9 cross-model gpt-5.5 review findings, including the HIGH-1 view-seed double-count that would otherwise have silently broken Phase 4 consumer migration).

**Phase 4 is the consumer-migration gate.** ARCH has filed the spec + implementer plan; the next session's job is HUMAN-approve the spec, then BACKEND-dispatch the 5-commit-cluster implementation per the plan.

**ARCH spec:** `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md` (14 sections, ~700 lines).
**ARCH implementer plan:** `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md` (28 tasks across 5 commit clusters, with per-task acceptance signals + DDM sub-plan + 12 inherited gotchas).

---

## What landed (this session, 2026-05-25 → 2026-05-26)

| Date | Milestone | Commit |
|---|---|---|
| 2026-05-25 | DC-1 Phase 3 MERGED to master | merge `46e84b1` |
| 2026-05-26 | Independent gpt-5.5 cross-model review surfaces 9 findings the in-session V/R/Q missed (3 HIGH, 2 MEDIUM, 4 LOW) | — |
| 2026-05-26 | DC-1 Phase 3 followup MERGED to master — closes all 9 findings via single PR (12 commits: ARCH + F.1-F.11) | merge `3490227` |
| 2026-05-26 | 4 stale Phase 2 worktrees + branches cleaned up | — |
| 2026-05-26 | Post-merge doc sweep on `dc1-phase-4-prep` worktree (CLAUDE.md / AGENTS.md / THESIS / Phase 3 spec + closeout / followup closeout / reviewer tracker — "awaiting HUMAN merge" → "MERGED to master") | `515a957` |
| 2026-05-26 | ARCH files Phase 4 spec + implementer plan + parent-spec update (Phase 4 + Phase 5 rows) | `adfae3b` |
| 2026-05-26 | Phase 4 next-session handoff (this document) | this commit |

**Phase 3 followup's load-bearing fix (HIGH-1):**

The pre-followup `Restated()` accessor started from `identityCopy(c.raw)` where `c.raw` was the post-dual-write entity, then re-applied each fired `LedgerEntry.DeltaAmount` on top — **double-counting** every Restater (A2 OtherIntangibles, A4 Inventory, A5 Inventory + TaxShieldDTA, C1/C2/C3/C5 NormalizedOperatingIncome, C6 InterestExpense). The bug was latent in Phase 3 (NON-goal: no consumer migration) but would have silently broken Phase 4 consumer migration for Restater-touched fields.

The followup's fix (F.1, Option A): pre-clean snapshot captured inside `CleanFinancialDataWithViews` BEFORE `CleanFinancialData` mutates the input; `cleaneddata.New(asReported, restated)` two-arg signature; `AsReported()` reads the pre-clean snapshot; `Restated()` reducer simplified to apply ONLY `EquityOffset` + `TaxShieldDTA` (the per-component DeltaAmount re-application was deleted as redundant with the dispatcher dual-write); `applyLedgerEntryToView` helper DELETED.

A new load-bearing regression pin `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` was RED-GREEN verified — confirmed to fail RED on pre-F.1 code with explicit `-$50M` double-count diagnostic, and to pass GREEN on the fix.

---

## What's on master right now (verified post-merge)

- Master tip: `3490227` (the Phase 3 followup merge).
- 50+ Go packages pass `go test ./... -count=1 -timeout 300s` with zero failures.
- `cleaneddata` package surface: `CleanedFinancialData{raw, asReported, restated, investedCap}` + `New(asReported, restated)` two-arg constructor + `FinancialDataView` DTO (25 fields, zero pointers) + `ViewKind` enum + three lazy memoized accessors (`AsReported()` / `Restated()` / `InvestedCapital()`) + `Raw()` escape hatch (TODO(phase-5) marker for deletion).
- `Service.CleanFinancialDataWithViews(ctx, data) (*CleaningResult, *CleanedFinancialData, error)` is the additive sibling entry point. The legacy `CleanFinancialData(ctx, data)` signature is UNCHANGED.
- 13 consumer read sites in `internal/services/valuation/*` still read `data.X` directly — Phase 4 migrates them.
- Dispatcher dual-write in `Process{Asset,Liability,Earnings}Adjustments` still mutates `data.X ±= Y` for every Restater + OverlayEmitter — Phase 4 deletes this atomically with consumer migration.
- B3 routing intent is recorded in `OverlaySpec.Field:"DebtLikeClaims"` but the WACC consumer still reads from `data.TotalDebt` (which includes B3's dual-write contribution) — Phase 4 flips the read site.
- `CurrentSchemaVersions["FinancialData"]` at 9.
- `CalculationVersion` at `"4.2"` (the literal in `internal/services/valuation/service.go`).
- Load-bearing invariants all GREEN: `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC bit-for-bit), `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, shadow byte-identity, `TestLedger_BasketSnapshot_ClusterPrediction`, `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` (AMD=$9.679B / KO=$60.912B), `TestCleanedFinancialData_Restated_C6EquityOffsetZero`, `TestQ2_A2TaxShieldDTA_Populated`, `TestQ4_AIProvenance_SHA256_Deterministic`, plus the new HIGH-1 regression pin.

---

## What's NEXT — Phase 4

### Spec + Plan ready to consume

- **Spec:** `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md`
- **Implementer plan:** `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md`

Both are committed at `adfae3b` on branch `dc1-phase-4-prep` (worktree at `../midas-dc1-phase-4-prep/`).

### Phase 4 architectural calls (the ARCH decisions BACKEND inherits)

1. **Single PR with 5 commit clusters** (NOT 5 separate PRs). Each cluster atomically migrates a consumer group AND deletes the dispatcher dual-writes that fed it. No commit straddles a value read from a partially-migrated state.

2. **DDM consumer migration DEFERRED to Phase 5** (high-leverage architectural call). Migrating DDM in Phase 4 risks the `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC `math.Float64bits` equality) cross-Tier-2 contract. Phase 4 ships a NEW `TestDDM_ConsumerPath_UnaffectedByPhase4` guard pin that asserts DDM's upstream inputs are byte-equal through every Phase 4 commit. Phase 5 owns the DDM read-site migration with a documented 4-step verification sub-plan.

3. **B3 routing flip realized in cluster C-4.** WACC reads `Restated().InterestBearingDebt` (B-rules no longer feed capital structure). EV→Equity bridge subtracts `InvestedCapital().DebtLikeClaims` via a new `dcf.CalculateEquityValueWithDebtLikeClaims` function. The legacy 5-arg `dcf.CalculateEquityValue` is preserved for alt-model callers.

4. **Dispatcher dual-write deletion via Option A.** The `data.X ±= Y` lines in `Process{Asset,Liability,Earnings}Adjustments` switch arms are deleted. Replacement: dispatcher applies `LedgerEntry.DeltaAmount` to `working.<Component>` only (no umbrella mutation). Post-clean entity's umbrella fields may now be incoherent — no Phase 4 consumer reads them directly. The followup's HIGH-1 reducer contract is preserved.

5. **`CalculationVersion` 4.2 → 4.3 atomic with C-4** (the first numerically-consequential commit). The bump signals to operators that bundles produced post-Phase-4 are NOT bit-for-bit comparable with pre-Phase-4 bundles.

6. **`SchemaVersion["FinancialData"]` stays at 9.** No new `omitempty` fields populated in Phase 4 — `DebtLikeClaims` is internal to `cleaneddata.FinancialDataView`, not persisted.

### Commit cluster summary (per ARCH plan §3 Tasks)

| Cluster | Scope | Estimated effort |
|---|---|---|
| C-1 | Plumbing: thread `*CleanedFinancialData` through `performValuation` + downstream callers | ~2 h |
| C-2 | Working-capital + ROIC + A2/A4/A5 dispatcher delete (NWC prior-period view bootstrap is the time sink) | ~3 h |
| C-3 | DCF + cross-check + alt-model OI + C-rule dispatcher delete | ~3 h |
| C-4 | WACC + EV→Equity bridge + B3 routing flip + A1/B-rule dispatcher delete + `CalculationVersion` 4.2 → 4.3 | ~4–5 h (highest-risk; B3 test design + drift verification) |
| C-5 | Graham + tangible + DDM/currency confirmation + vestigial deletion (translator funcs) + closeout doc | ~3 h |
| **Total** | | **~12–18 agent-hours** |

### Phase 4 NON-goals

These stay for Phase 5:
- DDM consumer migration (the load-bearing bit-for-bit invariant)
- `Raw()` escape hatch deletion
- Phase 5 closeout doc

### Phase 4 → Phase 5 gate

Per spec §12, all 22 acceptance criteria checkboxes met:
- `TestDDM_LegacyPath_BitForBit` GREEN at every Phase 4 commit (most consequential)
- `TestDDM_ConsumerPath_UnaffectedByPhase4` (NEW) GREEN at every commit
- `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_*` GREEN under the dispatcher contract change
- `TestPerformValuation_WACCUnaffectedByB3` (NEW) GREEN — the defining B3 routing flip pin
- `TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims` (NEW) GREEN
- All dispatcher dual-write sites in spec §4.4 DELETED
- `CalculationVersion: "4.3"` everywhere in `internal/services/valuation/service.go`
- `artifacts/tier2-baseline/<post-Phase-4-date>/` baseline refreshed (operator follow-up)
- Phase 4 closeout doc filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md`

---

## Stack ladder (read in this order to bootstrap)

For a new session that wants to pick up Phase 4:

1. **`CLAUDE.md`** DC-1 Phase 3 + Phase 3 followup SHIPPED bullets (consolidated; describes what landed on master).
2. **`AGENTS.md` row 17b** — DC-1 status + spec/plan link cluster (post-sweep — includes Phase 4 spec/plan paths).
3. **`docs/THESIS.md` DC-1 row** — Phase status table (Phase 4 PENDING with spec/plan path).
4. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — parent spec (Phase 0 through Phase 5 phasing).
5. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md`** — Phase 4 spec (the design BACKEND consumes).
6. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md`** — Phase 4 implementer plan (28 tasks across 5 commit clusters, acceptance signals, DDM sub-plan, gotchas).
7. **`docs/refactoring/archive/dc1-phase-3-followup-closeout.md`** — what just landed pre-Phase-4 (the inputs Phase 4 consumes).
8. **`docs/refactoring/archive/dc1-phase-3-followup-spec.md`** — the HIGH-1 fix design (Option A pre-clean snapshot) since Phase 4 consumers will read from `Restated()` / `InvestedCapital()` whose behavior the followup corrected.
9. **`docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`** — DC-1 tracker (per-phase progress paragraphs).
10. **`internal/services/datacleaner/cleaneddata/`** — the package Phase 4 consumers read from.
11. **`internal/services/valuation/`** — the 13 consumer read sites (grep `data.TotalAssets|TotalDebt|StockholdersEquity|CurrentAssets|CurrentLiabilities|OperatingIncome|NormalizedOperatingIncome|InterestExpense|Goodwill|OtherIntangibles|Inventory|DeferredTaxAssets|TangibleAssets`).

---

## Worktree workflow for Phase 4

Per the established `feedback_worktree_first_workflow` memory:
- Main `midas/` stays on `master` (currently at `3490227`).
- Phase 4 work in a sibling worktree at `../midas-dc1-phase-4/` branched off master.

```bash
# From the main midas/ directory (on master)
git worktree add ../midas-dc1-phase-4 -b dc1-phase-4 master
cd ../midas-dc1-phase-4

# Verify before EVERY commit:
pwd                                    # should end with midas-dc1-phase-4
git rev-parse --abbrev-ref HEAD        # should print dc1-phase-4
git worktree list                      # main midas at master + midas-dc1-phase-4
```

The `dc1-phase-4-prep` worktree currently at `../midas-dc1-phase-4-prep/` holds ARCH's spec + plan commit (`adfae3b`) + this handoff. Once Phase 4 BACKEND dispatches, the `dc1-phase-4` work happens in a SEPARATE sibling worktree (a fresh branch off master that includes the doc-prep commits as the base). Recommended: merge `dc1-phase-4-prep` into master first, THEN start `dc1-phase-4` from the updated master.

---

## Acceptance gates for Phase 4 (run before every commit)

```bash
go build ./...
go test ./internal/services/datacleaner/... -count=1
go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1
go test ./internal/services/datacleaner/ -run 'TestOrchestrator_LedgerOrdering|TestRecomputeUmbrellas_NoMutation' -count=1
go test ./internal/integration/... -run 'TestLedger_BasketSnapshot|TestDataCleanerRecompute_ShadowMode' -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/   # MUST exit 0
go test ./... -count=1   # full suite
go test ./internal/services/valuation/ -run 'TestDDM_ConsumerPath_UnaffectedByPhase4|TestPerformValuation_WACCUnaffectedByB3|TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims' -count=1   # new Phase 4 pins (after C-1 lands)
```

For final Phase 4 closeout, run replay against AAPL/MSFT/JPM bundles + a live API spot-check + capture a fresh `artifacts/tier2-baseline/` snapshot.

---

## Load-bearing invariants (must stay GREEN through every Phase 4 commit)

| Invariant | Path | Why load-bearing |
|---|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) | `internal/services/valuation/models/ddm_bitforbit_test.go` | Cross-Tier-2 contract; `math.Float64bits` equality. **Most at-risk in Phase 4** — DDM migration is deferred to Phase 5 specifically to protect this. |
| `TestDDM_ConsumerPath_UnaffectedByPhase4` (NEW Phase 4) | TBD by BACKEND per plan | Asserts DDM's upstream inputs are byte-equal through every Phase 4 commit. Catches accidental DDM-input regressions. |
| `TestRecomputeUmbrellas_NoMutation` | `internal/services/datacleaner/recompute_test.go` | Phase 1 shim stays read-only. |
| `TestOrchestrator_LedgerOrdering` | `internal/services/datacleaner/ledger_invariants_test.go` | Asset → liability → earnings partition. |
| Shadow snapshots byte-identical | `internal/integration/testdata/recompute-shadow/` | `git diff --quiet` exits 0 at every commit. |
| `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) | `internal/integration/datacleaner_ledger_basket_test.go` | Basket invariant. |
| `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` | same file | AMD=$9.679B / KO=$60.912B exact reconstruction. |
| `TestCleanedFinancialData_Restated_C6EquityOffsetZero` | `internal/services/datacleaner/cleaneddata/restate_test.go` | C6 capitalized-interest stays out of equity. |
| `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_*` | `internal/services/datacleaner/service_cleanwithviews_no_double_count_test.go` | HIGH-1 regression pin from followup. **Behavior under Phase 4's dispatcher contract change is non-trivial** — verify carefully. |
| `TestQ2_A2TaxShieldDTA_Populated` | `internal/services/datacleaner/adjustments/` | Q2 contract. |
| `TestQ4_AIProvenance_SHA256_Deterministic` | same | Q4 contract. |
| `TestB1LeasePath_HasNoContextBackgroundLiteral` (followup-era) | same | MEDIUM-1 followup static guard. |

---

## Known deferred work (NOT Phase 4 scope; flagged for visibility)

| Item | Status | Owner |
|---|---|---|
| Operator baseline refresh (`artifacts/tier2-baseline/`) post-Phase-3 + followup | DEFERRED | Operator (cache-bypass + 10-ticker capture; baseline goes stale every phase merge) |
| Phase 5: DDM consumer migration + `Raw()` escape hatch deletion + Phase 5 closeout | DEFERRED to Phase 5 | Phase 5 ARCH (TBD) |
| `T2-P4-W1` tracker — 2 deferred acceptance rows (live API regression on EQIX+PLD + replay regression) | OPEN | Tier 2 Closeout follow-up |
| `T2-BS-3` tracker — parser fix still deferred | OPEN | Phase 3 `Restated` view surfaces the truthful value; parser fix is separate |

---

## Bootstrap prompt for the next session

Copy-paste the block below into a fresh session to bootstrap directly into Phase 4 BACKEND dispatch:

````
I'm starting Phase 4 of the DC-1 datacleaner refactor (consumer migration).

WORKTREE-FIRST WORKFLOW (mandatory per the feedback_worktree_first_workflow memory):
The main midas/ directory STAYS on master. Phase 4 work happens in a sibling
worktree. Recommended sequence:

  1. First, merge dc1-phase-4-prep into master (it holds ARCH's spec + plan +
     post-merge doc sweep + this handoff):
       cd "/c/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas"
       git merge --no-ff dc1-phase-4-prep -m "Merge dc1-phase-4-prep — Phase 4 spec + plan + handoff"
       git worktree remove --force ../midas-dc1-phase-4-prep
       git branch -d dc1-phase-4-prep

  2. Create the Phase 4 implementation worktree from updated master:
       git worktree add ../midas-dc1-phase-4 -b dc1-phase-4 master
       cd ../midas-dc1-phase-4

All subsequent Phase 4 commands MUST run inside ../midas-dc1-phase-4/.
Before EVERY git commit:
       pwd                              # must end with midas-dc1-phase-4
       git rev-parse --abbrev-ref HEAD  # must print dc1-phase-4
       git worktree list                # should show main midas at master,
                                        # midas-dc1-phase-4 at dc1-phase-4

If any check is wrong, STOP — you're in the wrong worktree. Never `git checkout`
a non-master branch inside the main midas/ directory.

CONTEXT:
Phase 3 + Phase 3 followup MERGED to master. Phase 3 (`46e84b1`) introduced the
cleaneddata package + Q2/Q4 + ctx threading. Phase 3 followup (`3490227`) closed
9 cross-model gpt-5.5 review findings — most importantly HIGH-1, the Restated()
view-seed double-count that would silently break Phase 4 consumer migration if
left unfixed. Phase 4 is the consumer-migration gate.

Phase 4 spec authored at:
  docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md

Phase 4 implementer plan at:
  docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md

Next-session handoff at:
  docs/refactoring/implementations/dc1-phase-4-next-session-handoff.md
  (READ THIS FIRST — covers what landed, what's next, the architectural calls
  ARCH made, acceptance gates, known deferred work, and Phase 4's NON-goals.)

PHASE 4 SCOPE (per ARCH's spec + plan):
- Single PR, 5 commit clusters (C-1 through C-5).
- 12 of 13 consumer read sites migrate from `data.X` to `CleanedFinancialData`
  view accessors. DDM (the 13th) is DEFERRED to Phase 5 to protect bit-for-bit.
- B3 routing flip realized in C-4 — WACC reads `Restated().InterestBearingDebt`,
  EV→Equity bridge subtracts `InvestedCapital().DebtLikeClaims`.
- Dispatcher dual-write deletion via Option A — `data.X ±= Y` lines deleted;
  dispatcher applies `LedgerEntry.DeltaAmount` to `working.<Component>` only.
- `CalculationVersion` bump 4.2 → 4.3 atomic with C-4.
- `SchemaVersion["FinancialData"]` stays at 9 (no new omitempty fields).
- 22 acceptance criteria checkboxes (spec §13).

PHASE 4 NON-GOALS (Phase 5 scope):
- DDM consumer migration (deferred to protect TestDDM_LegacyPath_BitForBit)
- `Raw()` escape hatch deletion
- Phase 5 closeout doc

LOAD-BEARING INVARIANTS (must stay GREEN at every commit):
- TestDDM_LegacyPath_BitForBit (JPM/BAC/WFC bit-for-bit DDM) — most at-risk;
  ARCH ships a NEW `TestDDM_ConsumerPath_UnaffectedByPhase4` guard pin in C-1
- TestRecomputeUmbrellas_NoMutation
- TestOrchestrator_LedgerOrdering (asset → liability → earnings partition)
- TestLedger_BasketSnapshot_ClusterPrediction (10/10 basket tickers)
- TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction (AMD/KO exact)
- TestDataCleanerRecompute_ShadowMode_TickerBasket + shadow byte-identity
- TestCleanedFinancialData_Restated_C6EquityOffsetZero
- TestCleanFinancialDataWithViews_Restated_NoDoubleCount_* (HIGH-1 followup pin
  — behavior under Phase 4's dispatcher contract change is non-trivial; verify
  carefully)

AUTHORITATIVE DOCUMENTS (read in order, inside ../midas-dc1-phase-4/):

1. docs/refactoring/implementations/dc1-phase-4-next-session-handoff.md
   — READ THIS FIRST. State-of-world, architectural calls, acceptance gates,
   bootstrap context.

2. docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md
   — Phase 4 spec (14 sections: architecture, migration map, DDM bit-for-bit
   preservation strategy, replay drift expectation, testing strategy, PR
   strategy, Phase 4 → Phase 5 gate, acceptance criteria).

3. docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md
   — Phase 4 implementer plan (28 tasks across 5 commit clusters, file paths,
   per-task acceptance signals, DDM sub-plan, gotchas inherited from Phase 3 +
   followup).

4. docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
   — parent spec (Phase 0 through Phase 5 phasing).

5. docs/refactoring/archive/dc1-phase-3-followup-closeout.md
   — Phase 3 followup closeout (the inputs Phase 4 consumes; HIGH-1 fix details;
   B3 routing intent recorded in OverlaySpec.Field:"DebtLikeClaims").

6. docs/refactoring/archive/dc1-phase-3-followup-spec.md
   — HIGH-1 fix design (Option A pre-clean snapshot); ARCH's reasoning for
   why the Restated() reducer applies ONLY EquityOffset + TaxShieldDTA.

7. CLAUDE.md DC-1 Phase 3 + followup SHIPPED bullets — concise consolidated
   reference.

8. internal/services/datacleaner/cleaneddata/ — entity shapes Phase 4
   consumers read by name.

9. internal/services/valuation/ — the 13 consumer read sites Phase 4 migrates.

Please proceed with Phase 4 BACKEND dispatch per the implementer plan.
````

---

## Change log

| Date | Change |
|---|---|
| 2026-05-26 | Initial filing post Phase 3 followup merge to master. Documents the 2-merge sequence (Phase 3 `46e84b1` + followup `3490227`), the followup's HIGH-1 fix that unblocks Phase 4, the post-merge doc sweep landing site (`515a957`), ARCH's Phase 4 spec + plan (`adfae3b`), and the ready-to-copy bootstrap prompt. Anchored at master `3490227` + the prep-branch commits (`515a957` + `adfae3b` + this commit). |
