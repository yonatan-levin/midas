# DC-1 Phase 5 — Implementation Plan (DDM Migration + DebtLikeClaims Correction + Translator/Struct Retirement + Cleanup)

**Status:** READY FOR BACKEND DISPATCH (authored 2026-05-27)
**Spec:** [datacleaner-component-primitive-and-parallel-views-phase-5-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md)
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-spec.md)
**Phase 4 closeout (inherited context):** [datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md](./datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md)
**Branch:** `dc1-phase-5` (single PR, 5 commit clusters) — runs in a `midas-dc1-phase-5/` worktree off master; main `midas/` stays on master (MEMORY `feedback_worktree_first_workflow`).
**Total estimate:** 9–14 agent-hours (if P5-C1 split as Phase 4.x hotfix: ~2–3h hotfix + ~7–11h Phase 5 proper).

---

## 0. How to use this plan

- Execute clusters **in order** (P5-C1 → P5-C5). Each is ONE commit.
- The load-bearing invariants in §8.1 of the spec MUST be GREEN at EVERY commit. Run the §0.1 gate after every cluster before committing.
- DDM bit-for-bit: **a failure means REVERT the commit, never update the goldens** (CLAUDE.md gotcha). This is non-negotiable.
- Shadow-snapshot byte-identity (`git diff --quiet internal/integration/testdata/recompute-shadow/`) MUST hold at every commit — UNLIKE Phase 4, Phase 5 does NOT relax it (Phase 5 changes aggregation + dead code only, not adjuster execution). A regenerated snapshot = STOP-AND-INVESTIGATE.

### 0.1 Per-commit verification gate (run before every commit)

```bash
# DDM bit-for-bit (the cross-Tier-2 contract)
go test ./internal/services/valuation/models/ -run 'TestDDM_LegacyPath_BitForBit|TestDDM_ConsumerPath' -count=1

# cleaner invariants
go test ./internal/services/datacleaner/... -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/   # MUST exit 0

# basket + reconstruction
go test ./internal/integration/... -run 'TestLedger_BasketSnapshot' -count=1

# full suite
go test ./... -count=1
```

(Bash shown; on Windows use the PowerShell equivalents per CLAUDE.md build commands. `git diff --quiet` returns exit 1 if the snapshots changed.)

---

## Cluster P5-C1 — DDM EV-bridge DebtLikeClaims correction + CalculationVersion bump (HIGH risk, numeric drift) — est. 2–3 agent-hours

**Goal:** thread `DebtLikeClaims` into DDM's EV↔equity bridge so B-rule claims are not silently dropped (spec §3.2). This is the ONLY numeric-drift commit in Phase 5. Bump `CalculationVersion` 4.3 → 4.4.

> **DDM-specific sign (do NOT copy the `revenue_multiple` sign).** DDM derives equity from dividends THEN derives EV: `EV = equity + debt − cash`. DebtLikeClaims are ADDED: `EV = equity + debt + DebtLikeClaims − cash`. (revenue_multiple SUBTRACTS because it derives equity FROM EV.) DDM's `IntrinsicValuePerShare` + `EquityValue` are unaffected — only `EnterpriseValue` changes.

### Tasks

| # | Task | Files |
|---|---|---|
| P5-C1.1 | In `service.go::performAlternativeValuation`, populate `modelDebtLikeClaims` for ALL models (remove the `model.ModelType() != "ddm"` exclusion for the DebtLikeClaims term only). Keep DDM's `modelIBD = latestFinancialData.InterestBearingDebt` (legacy read — IBD migration is P5-C2/orthogonal). See spec §3.2 plumbing snippet. | `internal/services/valuation/service.go` (~1535-1549) |
| P5-C1.2 | In `ddm.go`, change BOTH EV bridges to add `DebtLikeClaims`: `enterpriseValue := equityValue + input.InterestBearingDebt + input.DebtLikeClaims - input.CashAndCashEquivalents` (legacy Gordon `:127`; multi-stage `:399`). | `internal/services/valuation/models/ddm.go:127,399` |
| P5-C1.3 | Bump `CalculationVersion` `"4.3"` → `"4.4"` at BOTH stamp sites (DCF path + alt-model path); update the inline comment to cite DC-1 Phase 5 DDM EV-bridge DebtLikeClaims correction. | `internal/services/valuation/service.go:1323,1635` |
| P5-C1.4 | Update `ModelInput.DebtLikeClaims` godoc (`router.go:51-62`): DDM now READS it in the EV bridge (it's no longer "0 for DDM / never read"). Note DDM ADDS it (EV-from-equity direction) vs revenue_multiple SUBTRACTS it (equity-from-EV direction). | `internal/services/valuation/models/router.go:51-62` |
| P5-C1.5 | Add `TestDDM_EVBridge_AddsDebtLikeClaims` (shipped under this name — the parallel with `TestRevenueMultiple_SubtractsDebtLikeClaims` is by PURPOSE, not by sign) + `TestDDM_EVBridge_ZeroClaims_Unchanged` + `TestDDM_EVBridge_AddsDebtLikeClaims/multistage_real` subtest (post-review MEDIUM-2 fix). Cover both legacy-Gordon AND multi-stage paths. Assert `IntrinsicValuePerShare`/`EquityValue` UNCHANGED vs zero-claims; assert `EnterpriseValue` increases by exactly `DebtLikeClaims`. | `internal/services/valuation/models/ddm_phase5_evbridge_test.go` |

### Acceptance signals

- `TestDDM_EVBridge_AddsDebtLikeClaims` (incl. `multistage_real` subtest) + `TestDDM_EVBridge_ZeroClaims_Unchanged` + `TestDDM_GoldenFixtures_ZeroDebtLikeClaims` GREEN.
- `TestDDM_LegacyPath_BitForBit` GREEN (DebtLikeClaims=0 for JPM/BAC/WFC ⇒ EV term `+0` ⇒ byte-identical EnterpriseValue).
- `TestDDM_ConsumerPath_RestatedViewParity` GREEN (renamed from `TestDDM_ConsumerPath_UnaffectedByPhase4` in P5-C2 — superset pin: output bits == goldens AND view fields == entity fields on JPM/BAC/WFC fixtures).
- Replay hermetic basket: zero numeric drift (no DDM ticker fires B-rules in the basket); `CalculationVersion 4.3 → 4.4` field text only.
- Full `go test ./... -count=1` GREEN.

### Inherited gotchas

- **DO NOT use the `revenue_multiple` sign.** Adding vs subtracting is direction-dependent (spec §3.2). Getting the sign wrong corrupts EV.
- `TestDDM_LegacyPath_BitForBit` pins EnterpriseValue — if it fails, the `+0` argument broke (a fixture unexpectedly fires a B-rule). REVERT + investigate the fixture, never the goldens.
- If HUMAN elected the Phase 4.x hotfix path (spec §3.2.1), THIS cluster IS that hotfix — ship it as a standalone `dc1-phase-4.1` PR and start Phase 5 proper at P5-C2.

---

## Cluster P5-C2 — DDM view migration to Restated() (HIGHEST risk, load-bearing centerpiece) — est. 3–4 agent-hours

**Goal:** migrate `ddm.go`'s `StockholdersEquity`/`NetIncome`/`DividendsPerShare` reads from `latest.X` (via `GetLatestPeriod()`) to the `Restated()` view, threaded through a now-populated-for-DDM `ModelInput.LatestRestatedView`. Execute the spec §7 4-step re-proof. ZERO numeric drift expected.

> **KEY SAFETY (spec §3.3):** the DDM-consumed fields are NOT recomputed-umbrella fields. `StockholdersEquity` = carried + Σ EquityOffset (zero when no Restater fires); `NetIncome`/`DividendsPerShare` = identity-copied. So `Restated().X == latest.X` for JPM/BAC/WFC. DDM reads NONE of the recomputed-umbrella fields (CurrentAssets/CurrentLiabilities/TotalAssets/TotalLiabilities/TangibleAssets) — do NOT migrate any of those (there are none in ddm.go anyway).

### Tasks (follow the 4-step ordering — do NOT migrate reads before steps 1+2 are GREEN)

| # | Task | Files |
|---|---|---|
| P5-C2.1 | **Step 1.** Confirm shadow byte-identity (gate §0.1). Add TEMPORARY `TestDDM_RestatedEqualsLatest_OnFixtures`: for JPM/BAC/WFC golden inputs build `cleaneddata.New(latest, latest)` and assert `Float64bits(Restated().{StockholdersEquity,NetIncome,DividendsPerShare}) == Float64bits(latest.X)`. Verify GREEN. | `models/ddm_phase5_reproof_test.go` (temp) |
| P5-C2.2 | **Step 2.** Add TEMPORARY `TestDDM_RestatedView_BitForBit`: run DDM with a populated `LatestRestatedView` (from `cleaneddata.New(latest,latest).Restated()`) against the JPM/BAC/WFC goldens; assert `Float64bits` equality on IntrinsicValuePerShare/EquityValue/EnterpriseValue + `Warnings` (content+order) + `Confidence`. Verify GREEN BEFORE touching production reads. | `models/ddm_phase5_reproof_test.go` (temp) |
| P5-C2.3 | **Step 3a.** In `service.go::performAlternativeValuation`, populate `ModelInput.LatestRestatedView` for DDM too (delete the `if model.ModelType() == "ddm" { return nil }` branch at ~1566-1571). Optionally flip DDM's `modelIBD` to `restatedViewOr(...).InterestBearingDebt` (bit-for-bit safe; spec §3.2 NOTE) — OR keep legacy IBD and document. | `internal/services/valuation/service.go:1566-1571` |
| P5-C2.4 | **Step 3b.** In `ddm.go`, migrate the 5 reads. `runDividendDiagnostics` reads `restatedView.StockholdersEquity`/`.NetIncome` (lines 178/181/213); `estimateDividendGrowth` reads `restatedView.StockholdersEquity`/`.NetIncome` (291/292). Pass the view in (helper signature gains a `*cleaneddata.FinancialDataView` param sourced from `input.LatestRestatedView`, with nil-fallback to `input.HistoricalData.GetLatestPeriod()` for the test/no-cleaner path). `DividendsPerShare` may also flip to the view (identity-copied; uniform). **Preserve exact arithmetic ordering — no helper extraction in the legacy path** (path-discipline, spec §3.3 caveat). | `internal/services/valuation/models/ddm.go:178,181,213,291,292` |
| P5-C2.5 | **Step 3c.** Re-run `TestDDM_LegacyPath_BitForBit` + `TestDDM_ConsumerPath_UnaffectedByPhase4` + the step-2 temp test. ALL GREEN. If `TestDDM_LegacyPath_BitForBit` fails → REVERT this commit, do NOT touch goldens. | (verification) |
| P5-C2.6 | **Step 4.** Delete the two temporary tests (P5-C2.1, P5-C2.2). RENAME `TestDDM_ConsumerPath_UnaffectedByPhase4` → `TestDDM_ConsumerPath_RestatedViewParity` (move/rename `ddm_phase4_invariance_test.go` → `ddm_phase5_invariance_test.go`); re-point assertions to verify (a) output bits == goldens AND (b) the `LatestRestatedView` fields DDM now consumes are bit-equal to entity fields for fixtures (spec §7.4). | `models/ddm_phase4_invariance_test.go` → `…phase5…` |

### Acceptance signals

- `TestDDM_LegacyPath_BitForBit` GREEN (the math is byte-identical because Restated==latest for fixtures).
- Renamed `TestDDM_ConsumerPath_RestatedViewParity` GREEN.
- Temporary re-proof tests added, verified GREEN, then deleted (no temp test survives the cluster).
- `ddm.go` no longer references `latest.StockholdersEquity` / `latest.NetIncome` for the migrated reads (grep: the migrated sites read the view). `latest` may still be referenced for nil-guards / `DividendsPerShare` plumbing — acceptable.
- Replay: zero numeric drift; `17-response.json` byte-identical except the (already-4.4) version field.
- Full suite GREEN.

### Inherited gotchas

- **CLAUDE.md DDM gotcha (load-bearing):** `calculateLegacyGordon` body is byte-identical to pre-Tier-2 master `0324057`. The migration touches `runDividendDiagnostics` + `estimateDividendGrowth` (shared helpers). The bit-for-bit test pins Warnings/Confidence/floats. Preserve exact arithmetic — `view.NetIncome / view.StockholdersEquity` must yield identical bits to `latest.NetIncome / latest.StockholdersEquity` (true when view==entity). No reordering.
- `LatestRestatedView` is nil on test/no-cleaner paths — DDM helpers MUST nil-check and fall back to the entity read (mirror the FFO nil-check pattern at `ffo.go:363`).
- Do NOT migrate any recomputed-umbrella field (there are none in ddm.go; this is a guardrail against accidentally pulling in CurrentAssets etc. if the read set is misread).

---

## Cluster P5-C3 — Orchestrator native-slice aggregation (MEDIUM risk, behavior-preserving) — est. 2–3 agent-hours

**Goal:** rewrite `service.go::applyActiveAdjustments` flag/adjustment aggregation to consume the native `AdjustmentLedger`/`Overlays`/`Flags` instead of `result.Applied/.Adjustments/.Flags`. NO struct deletion yet (that's P5-C4). `result.Adjustments`/`.Flags` content MUST stay byte-identical on the basket.

### Tasks

| # | Task | Files |
|---|---|---|
| P5-C3.1 | **Enumerate consumers first.** `grep -rn 'result\.Adjustments\|result\.Flags\|\.Applied\b' internal/services/datacleaner/` and `grep -rn 'CleaningResult' internal/ docs/` to map every reader of `CleaningResult.Adjustments`/`.Flags`. Record in the cluster commit message which consumers exist (quality scoring, audit trail, persisted snapshot, narrate). | (analysis) |
| P5-C3.2 | **Capture parity golden.** Add `TestApplyActiveAdjustments_NativeAggregation_Parity`: run the 10-ticker basket through the CURRENT aggregation, snapshot `result.Adjustments` + `result.Flags` (content + order) as a golden. (Same commit as the rewrite; the golden is the pre-rewrite reference.) | `internal/services/datacleaner/applyactive_parity_test.go` |
| P5-C3.3 | **Rewrite aggregation.** Replace the three `if XResult.Applied { allAdjustments = append(..., XResult.Adjustments...); allFlags = append(..., XResult.Flags...); totalRulesApplied += len(rules) }` blocks. Source flags from the native flag stream and adjustments from `data.AdjustmentLedger` via a single shared projection helper `adjustmentsFromLedger(ledger)` (spec §3.4 scope-cut: ≤1 projection, not 16 translators). Replace `XResult.Applied` firing signal with a native equivalent (`len(NativeLedgerEntries) > 0 || len(Flags) > 0`). Preserve asset→liability→earnings order. | `internal/services/datacleaner/service.go:520-618` |
| P5-C3.4 | Verify `TestApplyActiveAdjustments_NativeAggregation_Parity` GREEN (content byte-identical). Verify `TestOrchestrator_LedgerOrdering` GREEN. Verify shadow byte-identity. | (verification) |

### Acceptance signals

- `TestApplyActiveAdjustments_NativeAggregation_Parity` GREEN — `result.Adjustments`/`.Flags` content + order byte-identical pre/post rewrite on all 10 basket tickers.
- `TestOrchestrator_LedgerOrdering` GREEN (partition preserved).
- Shadow snapshots byte-identical.
- Replay `10-clean-output.json` / `13-cleaner-audit.json` `Adjustments`/`Flags` content unchanged; `17-response.json` zero drift.
- The orchestrator no longer reads `XResult.Applied/.Adjustments/.Flags` (only `Native*` + the new projection). The legacy translators STILL run (P5-C4 deletes them) but their output is now UNREAD by the orchestrator.

### Inherited gotchas

- `result.Adjustments` is `[]entities.Adjustment` (audit-trail shape); `data.AdjustmentLedger` is `[]entities.LedgerEntry` (component shape). The projection MUST map LedgerEntry → Adjustment faithfully (RuleID, Category, Amount/DeltaAmount, Reasoning, Applied=Fired). If the mapping can't reproduce the exact `entities.Adjustment` content the translators produced, the parity test fails — investigate field-by-field. If a clean projection is infeasible, KEEP one shared projection helper but still proceed (spec §3.4).
- FlagEmitters (C4/C7 + the 2 PR-2 reviews) emit via `AdjusterOutput.Flags` already drained natively — ensure those flags are in the native flag stream the rewrite reads.
- Do NOT change adjuster execution — only the post-execution aggregation. Shadow byte-identity is the canary.

---

## Cluster P5-C4 — Translator + result-struct + dormant-fallback deletion (MEDIUM risk, mechanical, gated on P5-C3) — est. 2–3 agent-hours

**Goal:** delete the now-unread legacy translator stack + result structs + dormant fallbacks. Gated on P5-C3 (orchestrator no longer reads the legacy results).

### Tasks

| # | Task | Files |
|---|---|---|
| P5-C4.1 | Delete the per-rule translators: `a1/a2/a4/a5/aRD/aCapSoftware AdjusterOutputToLegacyResult` (`assets.go`), `b1/b2/b3 AdjusterOutputToLegacyResult` (`liabilities.go`), `c1/c2/c3/c4/c5/c6/c7 AdjusterOutputToLegacyResult` (`earnings.go`). Remove their call sites in the `Process*Adjustments` dispatchers (the `result = …` assignments). | `assets.go`, `liabilities.go`, `earnings.go` |
| P5-C4.2 | Change `Process{Asset,Liability,Earnings}Adjustments` return types: drop the legacy `*{Asset,Liability,Earnings}AdjustmentResult`; return only the native carrier (the slimmed result holding `NativeLedgerEntries`/`NativeOverlays`/`Flags`/`NativelyEmittedRuleIDs`, OR `AdjusterOutput` aggregate). Update the orchestrator call sites (P5-C3 already reads only native fields, so the call-site change is type-only). | `assets.go:1352`, `liabilities.go:264`, `earnings.go:1317`, `datacleaner/service.go:522,554,593` |
| P5-C4.3 | Delete the adjustments-package result structs: `AssetAdjustmentResult` (`assets.go:2092`), `LiabilityAdjustmentResult` (`liabilities.go:81`), `EarningsAdjustmentResult` (`earnings.go:53`). | `assets.go`, `liabilities.go`, `earnings.go` |
| P5-C4.4 | Delete the dormant legacy-fallback helpers in `earnings.go`: `ProcessRestructuringChargesAdjustment` (1574), `ProcessAssetSaleGainsAdjustment` (1641), `ProcessLitigationSettlementsAdjustment` (1683), + the capitalized-interest legacy helper. Remove the `if err != nil { result = ea.ProcessX...; break }` fallback arms in `ProcessEarningsAdjustments` that called them. (The `Apply*` methods don't error in practice — the arm is dead.) | `internal/services/datacleaner/adjustments/earnings.go` |
| P5-C4.5 | **Verify dead, then delete** `entities.AssetAdjustmentResult` + `entities.LiabilityAdjustmentResult` (`core/entities/data_cleaning.go:468-485`). Re-run `grep -rn 'entities\.\(Asset\|Liability\|Earnings\)AdjustmentResult' internal/` — if ONLY doc matches (expected per spec §4.5), delete; if a code/serialization ref appears, DEFER with a comment. | `internal/core/entities/data_cleaning.go:468-485` |
| P5-C4.6 | Update/delete tests that reference the deleted translators / result structs (the `Process*Adjustments_NativeXEmission` tests assert on the dispatcher result — re-point to the native carrier). | `adjustments/*_test.go` |
| P5-C4.7 | Add `TestDispatcherSignature_NoLegacyResultStruct` (compile-guard / reflection) OR rely on the deletion itself as the guard (structs gone ⇒ stray refs fail to compile). | `adjustments/*_test.go` |

### Acceptance signals

- `go build ./...` GREEN (no dangling references to deleted translators/structs).
- `grep -rn 'AdjusterOutputToLegacyResult\|AssetAdjustmentResult\|LiabilityAdjustmentResult\|EarningsAdjustmentResult' internal/` returns only the renamed/native carrier (no translator/legacy-struct matches in non-test code; test matches updated).
- `grep -rn 'ProcessRestructuringChargesAdjustment\|ProcessAssetSaleGainsAdjustment\|ProcessLitigationSettlementsAdjustment' internal/` empty (dormant helpers gone).
- All §8.1 invariants GREEN; shadow byte-identity holds (dead code deletion changes nothing).
- Replay zero drift everywhere.

### Inherited gotchas

- This is the largest mechanical diff. Land it ONLY after P5-C3 proves the orchestrator reads no legacy result fields — otherwise the build breaks mid-cluster.
- The `Native*` drain in `applyActiveAdjustments` (the `data.AdjustmentLedger = append(...)` lines) STILL needs the native fields on whatever carrier the dispatcher returns — preserve those.
- Some `adjustments/*_test.go` tests assert `result.Applied`/`result.Adjustments` directly — re-point or delete per the native contract; do NOT weaken the per-rule emission assertions (A1/A2/A4/A5/B1/B2/B3/C1-C7 native LedgerEntry/Overlay/Flag emission must still be pinned).

---

## Cluster P5-C5 — Raw() deletion + optional cleanup + closeout (LOW risk) — est. 1–2 agent-hours

**Goal:** delete `cleaneddata.Raw()`; formalize accessor concurrency contract; (optional) legacy-slot decision; docs sweep + closeout; DC-1 close.

### Tasks

| # | Task | Files |
|---|---|---|
| P5-C5.1 | Delete `cleaneddata.CleanedFinancialData.Raw()` (`cleaned.go:79-96`) + its `TODO(phase-5)` comment. | `internal/services/datacleaner/cleaneddata/cleaned.go` |
| P5-C5.2 | Remove/replace the `Raw()` contract-test reference (`service_cleanwithviews_test.go:45` per Phase 4 closeout §6). Verify `grep -rn '\.Raw()' internal/` returns no matches. | `internal/services/datacleaner/service_cleanwithviews_test.go` |
| P5-C5.3 | Strengthen the `CleanedFinancialData` concurrency godoc (`cleaned.go:32-40`): state the request-local / read-only-view contract as a hard contract; add a one-line note at each accessor (`asreported.go`/`restate.go`/`invested_capital.go`). NO `sync.Once` code (spec §3.6 recommendation). | `cleaneddata/cleaned.go`, `asreported.go`, `restate.go`, `invested_capital.go` |
| P5-C5.4 | **Optional (verify-then-decide):** `grep -rn 'historicalData.Data\[' internal/services/valuation/` + confirm whether any LATEST-period reader remains after P5-C2. If zero, stop populating `historicalData.Data[latestPeriod] = cleaningResult.CleanedData`; else document why it stays (DPS-CAGR `GetRecentYears` still needs prior periods). | `internal/services/valuation/service.go` |
| P5-C5.5 | Docs sweep: file Phase 5 closeout at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-5-closeout.md`; update parent-spec changelog; add the Phase 5 SHIPPED bullet to CLAUDE.md (retire the "translators still load-bearing" + single-view gotchas); update THESIS/AGENTS/ARCHITECTURE rows; archive the DC-1 tracker to `docs/reviewer/archive/`. | `docs/**`, `CLAUDE.md`, `AGENTS.md` |

### Acceptance signals

- `grep -rn '\.Raw()' internal/` empty; `Raw` symbol gone; `go build ./...` GREEN.
- Concurrency godoc strengthened; no `sync.Once` introduced.
- Optional legacy-slot decision documented (kept or removed with grep evidence).
- Phase 5 closeout filed; DC-1 tracker archived; all DC-1 close-out checklist items (parent spec) satisfied.
- Full suite GREEN; replay zero drift.

### Inherited gotchas

- Deleting `Raw()` before P5-C2 lands would break if DDM still needed an entity escape hatch — order matters (P5-C5 is last).
- Do NOT delete the legacy `historicalData` slot population unless the grep PROVES zero latest-period readers — `GetRecentYears` (prior periods) is a separate, still-needed population.

---

## Appendix A — file/line reference map (as of `ce94f70`)

| Concern | File:line |
|---|---|
| DDM EV bridges (`+DebtLikeClaims`) | `internal/services/valuation/models/ddm.go:127` (legacy), `:399` (multi-stage) |
| DDM migrated reads | `ddm.go:178,181,213` (diagnostics), `:291,292` (estimateDividendGrowth) |
| DDM bit-for-bit guard (UNCHANGED) | `models/ddm_bitforbit_test.go` |
| DDM Phase-4 guard → rename | `models/ddm_phase4_invariance_test.go` |
| `ModelInput.DebtLikeClaims` / `LatestRestatedView` | `models/router.go:62`, `:70` |
| Alt-model plumbing (DDM DebtLikeClaims + LatestRestatedView) | `internal/services/valuation/service.go:1535-1571` |
| `CalculationVersion` stamp sites | `service.go:1323` (DCF), `:1635` (alt-model) |
| DCF EV bridge (Phase-4, reference for sign convention) | `service.go:1255-1264` (`CalculateEquityValueWithDebtLikeClaims`) |
| Orchestrator aggregation (native rewrite) | `internal/services/datacleaner/service.go:520-620` |
| `result.Adjustments`/`.Flags` set onto CleaningResult | `datacleaner/service.go:217-218` |
| Per-rule translators | `adjustments/assets.go:1632-1986`, `liabilities.go:596-1230`, `earnings.go:201-1310` |
| Dispatchers | `assets.go:1352`, `liabilities.go:264`, `earnings.go:1317` |
| Adjustments-package result structs | `assets.go:2092`, `liabilities.go:81`, `earnings.go:53` |
| Dormant earnings legacy fallbacks | `earnings.go:1574,1641,1683` (+ capitalized-interest helper) |
| Dead entities result structs | `core/entities/data_cleaning.go:468-485` |
| `cleaneddata.Raw()` + marker | `cleaneddata/cleaned.go:79-96` |
| `Restated()` reducer (UNCHANGED) | `cleaneddata/restate.go:40-88` |
| `InvestedCapital()` (UNCHANGED) | `cleaneddata/invested_capital.go:25-48` |
| view-or helpers | `service.go:1791` (`restatedViewOr`), `:1803` (`asReportedViewOr`), `:1815` (`investedCapitalOr`) |

## Appendix B — invariant quick-reference (run order)

1. `TestDDM_LegacyPath_BitForBit` — REVERT on failure, never update goldens.
2. `TestDDM_ConsumerPath_*` (renamed in P5-C2).
3. `git diff --quiet internal/integration/testdata/recompute-shadow/` — exit 0 (NOT relaxed in Phase 5).
4. `TestOrchestrator_LedgerOrdering`.
5. `TestRecomputeUmbrellas_NoMutation`.
6. `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) + `…_T2BS3_RestatedReconstruction` (AMD $9.679B / KO $60.912B).
7. `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_*`, `TestCleanedFinancialData_Restated_C6EquityOffsetZero`.
8. `TestApplyActiveAdjustments_NativeAggregation_Parity` (new, P5-C3).
9. Full `go test ./... -count=1`.
