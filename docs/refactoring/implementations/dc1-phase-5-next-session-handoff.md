# DC-1 Phase 5 — Next-Session Handoff

**Date:** 2026-05-27
**Status:** READY FOR PHASE 5 BACKEND DISPATCH
**Master tip:** `ce94f70` (DC-1 Phase 4 merge). Phase 5 spec + implementer plan + this handoff await on branch `dc1-phase-5-prep` (commit `caa227e` ARCH spec+plan; the docs-update + this handoff land alongside).

---

## TL;DR for the next session

DC-1 **Phase 4 is MERGED to master** (`ce94f70`, 2026-05-27). It migrated 13 valuation consumer read sites onto the `cleaneddata` views, realized the B3 routing flip, deleted the dispatcher dual-writes (§8.2.1 Option A), and bumped `CalculationVersion` 4.2 → 4.3. It was reviewed across **4 rounds** (`/execute` B-V-R-Q + holistic `/code-review` + zen-mcp **gpt-5.5** cross-model + a second B-V-R-Q on the fix), which caught and fixed **two correctness regressions**: `e521c53` (NWC `Restated()` → `AsReported()` — recomputed-umbrella drift) and `2ea9978` (`revenue_multiple` now subtracts `InvestedCapital().DebtLikeClaims`).

**Phase 5 is the DC-1 closeout phase.** ARCH has filed the spec + implementer plan. The next session's job: HUMAN-approve the spec (or merge the prep branch), then BACKEND-dispatch the 5-commit-cluster implementation per the plan.

**ARCH spec:** `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md` (424 lines).
**ARCH implementer plan:** `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md` (230 lines).

---

## What's on master right now (verified post-merge)

- Master tip: `ce94f70` (the Phase 4 merge). `go build ./...` exit 0; full `go test ./...` exit 0.
- `cleaneddata` views are the canonical consumer read path; dispatcher dual-writes deleted (umbrellas recomputed in `Restated()`).
- `CalculationVersion` at `"4.3"` (two inline literals in `internal/services/valuation/service.go`).
- **DDM (`ddm.go`) UNTOUCHED** — still reads `latest.StockholdersEquity` / `.NetIncome` / `.DividendsPerShare` via `GetLatestPeriod()`; `ModelInput.LatestRestatedView` is nil for DDM; `ModelInput.DebtLikeClaims` is non-DDM-only (0 for DDM). `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC `math.Float64bits`) + `TestDDM_ConsumerPath_UnaffectedByPhase4` GREEN.
- `cleaneddata.Raw()` survives with a `TODO(phase-5)` marker — zero production `internal/` callers (only a contract test).
- Per-rule translators + `{Asset,Liability,Earnings}AdjustmentResult` structs survive — STILL load-bearing (orchestrator `applyActiveAdjustments` reads `result.Applied/.Adjustments/.Flags` for flag aggregation + quality scoring).
- Dormant legacy-fallback umbrella mutations in `earnings.go` (`ProcessRestructuringChargesAdjustment` etc.) survive — reachable only on the never-fired `Apply* err != nil` branch.

---

## Phase 5 scope (per ARCH spec + plan — single PR, 5 commit clusters)

| Cluster | Scope | Risk |
|---|---|---|
| **P5-C1** | **DDM DebtLikeClaims correction** — thread `ModelInput.DebtLikeClaims` into DDM's EV bridge. The only numeric-drift commit; **bumps `CalculationVersion` 4.3 → 4.4.** | HIGH |
| **P5-C2** | **DDM view migration** — migrate `ddm.go` StockholdersEquity/NetIncome/DividendsPerShare reads to `Restated()` via a now-populated `LatestRestatedView`. The load-bearing centerpiece — `TestDDM_LegacyPath_BitForBit` must stay bit-for-bit. 4-step re-proof (shadow re-run → temp parallel-write `TestDDM_RestatedView_BitForBit` → migrate reads → delete temps). | HIGHEST |
| **P5-C3** | **Orchestrator native-slice aggregation** — migrate `applyActiveAdjustments`' `result.Applied/.Adjustments/.Flags` reads onto the native ledger/overlays/flags. Behavior-preserving, gated by a basket parity golden. MUST land BEFORE any translator deletion. | MEDIUM |
| **P5-C4** | **Translator + struct + dormant-fallback deletion** (gated on C3) — delete the 16 per-rule `*AdjusterOutputToLegacyResult` translators, the `{Asset,Liability,Earnings}AdjustmentResult` structs, and the dormant `earnings.go` legacy-fallback umbrella mutations. | MEDIUM |
| **P5-C5** | **`Raw()` deletion + cleanup + closeout** — delete `cleaneddata.Raw()`; formalize the accessor request-local contract (no `sync.Once`); verify-then-decide on the legacy `historicalData` slot; docs sweep + DC-1 close. | LOW |

### ARCH's key decisions (the calls BACKEND inherits)
1. **DDM bit-for-bit is safe to migrate.** The DDM-consumed fields are carried-through (StockholdersEquity = identity + `EquityOffset`; NetIncome/DividendsPerShare = identity-copied) — NOT recomputed-umbrella fields. So `Restated().X == latest.X` when no Restater fires, and the JPM/BAC/WFC fixtures fire none. **DDM migrates NONE of the recomputed-umbrella fields** (CurrentAssets/CurrentLiabilities/TotalAssets/TotalLiabilities/TangibleAssets) that Phase 4 had to keep on `AsReported()`.
2. **DDM DebtLikeClaims is ADDED, not subtracted.** DDM derives EV *from* equity (`EV = equity + debt − cash`) — the opposite direction from the DCF/revenue_multiple bridge. So the headline dividend-derived `IntrinsicValuePerShare`/`EquityValue` (what the endpoint surfaces) are UNAFFECTED; only the derived, reported `EnterpriseValue` drifts for B-rule-firing banks. **ARCH recommends bundling this into P5-C1, NOT a standalone Phase-4.x hotfix** (it doesn't corrupt the headline value, unlike the revenue_multiple bug that warranted the in-window `2ea9978` fix).
3. **`CalculationVersion` 4.3 → 4.4** atomic with P5-C1; **no `SchemaVersion` bump** (FinancialData stays 9, ValuationResult stays 2).
4. **Cluster ordering** isolates the riskiest first (C1 numeric drift, C2 bit-for-bit) and sequences consumer-migration-BEFORE-deletion (C3 then C4).

### Open questions ARCH surfaced for HUMAN (all have recommendations)
1. Phase-4.x hotfix vs bundle the DDM DebtLikeClaims → ARCH recommends **bundle (P5-C1)**.
2. `result.Adjustments` audit-trail projection shape (P5-C3) → resolve empirically at impl time; prefer ≤1 shared `adjustmentsFromLedger` projection over 16 per-rule translators.
3. Accessor `sync.Once` → ARCH recommends **formalize the request-local contract in godoc, do NOT add locking** (revisit when a parallel-read batch consumer lands).
4. Stop populating the legacy `historicalData` slot → **verify-then-decide** (keep unless grep proves zero latest-period readers after P5-C2; DPS-CAGR `GetRecentYears` still needs prior periods).

---

## Load-bearing invariants (must stay GREEN at every Phase 5 commit)

| Invariant | Why |
|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) | Cross-Tier-2 contract; `math.Float64bits` equality. **Most at-risk in Phase 5** (DDM is migrating). A failure = REVERT, never update goldens. |
| `TestDDM_ConsumerPath_UnaffectedByPhase4` | Rename/retire appropriately once DDM migrates (the plan specifies `…RestatedViewParity`). |
| `TestRecomputeUmbrellas_NoMutation` | Recompute shim stays read-only. |
| `TestOrchestrator_LedgerOrdering` | Asset → liability → earnings partition (critical for the C3/C4 translator retirement). |
| Shadow snapshots byte-identical | `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0. |
| `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) | Per-ticker AdjusterID sets unchanged. |
| `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` | AMD $9.679B / KO $60.912B exact. |
| `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_*` | HIGH-1 regression pins. |
| Full `go test ./...` exit 0 | At every commit. |

---

## Worktree workflow for Phase 5

Per the `feedback_worktree_first_workflow` memory:
- Main `midas/` stays on `master` (currently `ce94f70`).
- Phase 5 work in a sibling worktree at `../midas-dc1-phase-5/` branched off master.

```bash
# First merge the prep branch (it holds the spec + plan + this handoff + the Phase-4 docs-update):
cd "/c/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas"
git merge --no-ff dc1-phase-5-prep -m "Merge dc1-phase-5-prep — Phase 5 spec + plan + handoff + Phase-4 docs sweep"
git worktree remove --force ../midas-dc1-phase-5-prep
git branch -d dc1-phase-5-prep

# Then create the Phase 5 implementation worktree from updated master:
git worktree add ../midas-dc1-phase-5 -b dc1-phase-5 master
cd ../midas-dc1-phase-5

# Verify before EVERY commit:
git rev-parse --abbrev-ref HEAD        # dc1-phase-5
git worktree list                      # main midas at master + midas-dc1-phase-5
```

---

## Acceptance gates (run before every commit)

```bash
go build ./...
go test ./internal/services/valuation/models/ -run 'TestDDM_LegacyPath_BitForBit|TestDDM_RestatedView_BitForBit' -count=1   # bit-for-bit — most consequential
go test ./internal/services/datacleaner/... -count=1
go test ./internal/integration/... -run 'TestLedger_BasketSnapshot|TestDataCleanerRecompute_ShadowMode' -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/    # MUST exit 0
go test ./... -count=1   # full suite
```

For closeout: replay against AAPL/MSFT/JPM bundles + a live API spot-check (DDM ticker — confirm headline IntrinsicValue unchanged, EnterpriseValue corrected for B-firing banks) + capture a fresh `artifacts/tier2-baseline/` snapshot. **Note the inherited operator follow-up from Phase 4:** the `2026-05-19` baseline is `calc_version 4.1`, so a fresh `4.2`/`4.3`-current baseline must be captured (needs live SEC/market capture) before clean per-cluster drift attribution is possible.

---

## Stack ladder (read in this order to bootstrap)

1. `CLAUDE.md` DC-1 Phase 2/3/followup/Phase-4 SHIPPED bullets (the Phase-4 bullet now says MERGED `ce94f70`).
2. `AGENTS.md` row 17b — DC-1 entry (Phase 4 MERGED).
3. `docs/THESIS.md` DC-1 row.
4. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md`** — the design BACKEND consumes.
5. **`docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md`** — 5 clusters, file paths, acceptance signals, agent-hour estimates.
6. `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md` — what Phase 4 left for Phase 5 (§6 translator/Raw() status, §8 DDM migration sub-plan, §7 judgment calls).
7. `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` — parent spec, Phase 5 row.
8. `internal/services/valuation/models/ddm.go` — the 5 read sites + the EV↔equity bridge (127/399).
9. `internal/services/datacleaner/cleaneddata/{restate.go,asreported.go,invested_capital.go}` — which fields recompute vs carry.
10. `internal/services/valuation/service.go::applyActiveAdjustments` (~523/555/594) — the legacy `*AdjustmentResult` reads C3 migrates.

---

## Bootstrap prompt for the next session

Copy-paste the block below into a fresh session to bootstrap directly into Phase 5 BACKEND dispatch:

````
I'm starting Phase 5 of the DC-1 datacleaner refactor (DDM migration + DebtLikeClaims correction + translator retirement + Raw() deletion — the DC-1 closeout phase).

WORKTREE-FIRST WORKFLOW (mandatory per the feedback_worktree_first_workflow memory):
The main midas/ directory STAYS on master. Phase 5 work happens in a sibling worktree.

  1. First merge dc1-phase-5-prep into master (it holds ARCH's spec + plan + this
     handoff + the Phase-4 docs sweep):
       cd "/c/Users/Yonatan Levin/Documents/Programming/Projects/FinTech/Strade/midas"
       git merge --no-ff dc1-phase-5-prep -m "Merge dc1-phase-5-prep — Phase 5 spec + plan + handoff"
       git worktree remove --force ../midas-dc1-phase-5-prep
       git branch -d dc1-phase-5-prep

  2. Create the Phase 5 implementation worktree from updated master:
       git worktree add ../midas-dc1-phase-5 -b dc1-phase-5 master
       cd ../midas-dc1-phase-5

All subsequent Phase 5 commands MUST run inside ../midas-dc1-phase-5/.
Before EVERY git commit: verify `git rev-parse --abbrev-ref HEAD` prints dc1-phase-5
and `git worktree list` shows the main midas at master.

CONTEXT:
DC-1 Phase 4 MERGED to master as `ce94f70` (consumer migration + B3 routing flip +
dual-write deletion + CalcVersion 4.3). Phase 5 is the closeout phase.

READ FIRST (in order):
1. docs/refactoring/implementations/dc1-phase-5-next-session-handoff.md (THIS doc — state of world, ARCH calls, gates, open questions)
2. docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md
3. docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md
4. docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md (§6 translator/Raw() status, §8 DDM sub-plan, §7 judgment calls)
5. CLAUDE.md DC-1 Phase 4 SHIPPED bullet

PHASE 5 SCOPE (single PR, 5 commit clusters):
- P5-C1: DDM DebtLikeClaims correction (ADDED not subtracted — DDM derives EV from
  equity); bumps CalculationVersion 4.3 → 4.4. Only numeric-drift commit.
- P5-C2: DDM view migration (ddm.go reads → Restated() via LatestRestatedView) with
  the 4-step bit-for-bit re-proof. THE load-bearing centerpiece.
- P5-C3: migrate orchestrator off result.Applied/.Adjustments/.Flags onto native
  ledger/overlays/flags (behavior-preserving, parity-gated) — BEFORE any deletion.
- P5-C4: delete the 16 per-rule translators + {Asset,Liability,Earnings}AdjustmentResult
  structs + dormant earnings.go legacy-fallback umbrella mutations (gated on C3).
- P5-C5: delete cleaneddata.Raw(); formalize accessor request-local contract; verify-
  then-decide on the legacy historicalData slot; docs sweep + DC-1 closeout doc.

LOAD-BEARING INVARIANTS (GREEN at every commit):
- TestDDM_LegacyPath_BitForBit (JPM/BAC/WFC math.Float64bits) — MOST at-risk (DDM is
  migrating). Failure = REVERT, never update goldens.
- TestRecomputeUmbrellas_NoMutation, TestOrchestrator_LedgerOrdering, shadow byte-
  identity, TestLedger_BasketSnapshot_ClusterPrediction (10/10),
  TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction, the NoDoubleCount pins.

OPEN QUESTIONS (ARCH recommendations in the spec §Open questions):
1. DDM DebtLikeClaims hotfix-vs-bundle → bundle into P5-C1.
2. Audit-trail projection shape → resolve at impl time (prefer 1 shared projection).
3. Accessor sync.Once → formalize contract, don't add locking.
4. Stop populating legacy historicalData slot → verify-then-decide.

Please proceed with Phase 5 BACKEND dispatch per the implementer plan, with a full
B-V-R-Q cycle per cluster (and a gpt-5.5 cross-model code-review pass before merge —
it caught real regressions in both the Phase 4 base and the revenue_multiple fix).
````

---

## Change log

| Date | Change |
|---|---|
| 2026-05-27 | Initial filing post Phase 4 merge to master (`ce94f70`). Documents the Phase 4 merge + 4 review rounds + 2 regression fixes (`e521c53` NWC, `2ea9978` revenue_multiple), ARCH's Phase 5 spec + plan (`caa227e`), the 5-cluster scope, ARCH's architectural calls (DDM bit-for-bit safety, DDM DebtLikeClaims ADDED-not-subtracted + bundle recommendation, CalcVersion 4.4), the 4 open questions, and a copy-ready bootstrap prompt. |
