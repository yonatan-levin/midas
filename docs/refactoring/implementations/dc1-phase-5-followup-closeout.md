# DC-1 Phase 5 follow-up — Closeout (P5-C3-full Adjustments-projection + P5-C4 deletion + DDM modelIBD flip)

**Status:** IMPLEMENTATION COMPLETE on branch `dc1-phase-5-followup` (forked from master `974570c`) — awaiting HUMAN review/merge. NOT yet merged to master.
**Date:** 2026-06-02
**ARCH decision note:** [dc1-phase-5-followup-percentage-decision.md](../spec/dc1-phase-5-followup-percentage-decision.md) (Path (a) — preserve `Adjustment.Percentage` via `LedgerEntry.SkipMetrics`)
**Phase 5 PARTIAL closeout:** [datacleaner-component-primitive-and-parallel-views-phase-5-closeout.md](./datacleaner-component-primitive-and-parallel-views-phase-5-closeout.md) §6 (the deferred chunks this PR closes)
**Phase 5 spec:** [datacleaner-component-primitive-and-parallel-views-phase-5-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md) §3.4 (translator retirement), §11 (DC-1 done gate)

---

## 1. What this PR closes

DC-1 Phase 5 PARTIAL (merged to master as `e816fcc`) shipped P5-C1 (DDM EV-bridge DebtLikeClaims correction), P5-C2 (DDM `Restated()` view migration), P5-C3-SCOPED (orchestrator firing-signal via `nativeFired`), and P5-C5-PARTIAL (`Raw()` deletion). It DEFERRED four chunks. This follow-up PR closes three of them (the fourth — fresh CalcVersion-4.4 replay baseline capture — stays an operator follow-up):

| Deferred chunk (Phase 5 PARTIAL §6) | Status here |
|---|---|
| P5-C3-full Adjustments-projection (`result.Adjustments` from native ledger, not the 16 translators) | ✅ DONE (commits A0–A4) |
| P5-C4 translator + struct + dormant-fallback deletion | ✅ DONE (commit B1) + legacy dead-helper sweep (FIX-2) |
| DDM `modelIBD` view-migration flip | ✅ DONE (commit C1) |
| Fresh CalcVersion-4.4 replay baseline capture | 🚫 OPERATOR follow-up (unchanged; needs live SEC/market capture) |

## 2. Commit ladder (branch `dc1-phase-5-followup`, on top of master `974570c`)

The 7-commit substantive ladder (A0–C1), 4 review-fix commits (FIX-1..FIX-4), and 2 doc commits (this closeout + its count fix, folded into FIX-4). Total branch range `974570c..HEAD` is 12 commits.

| # | SHA | Subject |
|---|---|---|
| A0 | `06a2baa` | docs(dc1-p5fu): file ARCH percentage-handling decision note |
| A1 | `c303aa7` | test(dc1-p5fu): add basket-parity test for result.Adjustments + capture pre-rewrite golden |
| A2 | `f4f2d6a` | feat(dc1-p5fu): capture pre-state on Restater Apply* LedgerEntries (A2 + C2/C3/C5/C6 + C4) |
| A3 | `dc11fcf` | feat(dc1-p5fu): add adjustmentsFromLedger projection helper + ruleMeta + 3 defensive pins |
| A4 | `aa58576` | refactor(dc1-p5fu): orchestrator derives result.Adjustments from native projection |
| B1 | `569a892` | refactor(dc1-p5fu): delete legacy translator stack + result structs + dormant fallbacks (P5-C4) |
| C1 | `b023694` | refactor(dc1-p5fu): flip DDM modelIBD to Restated() view |
| FIX-1 | `2281f25` | fix(dc1-p5fu): harden adjustmentsFromLedger pre-state guard + tangible-recompute predicate (review findings) |
| FIX-3 | `e24a669` | docs(dc1-p5fu): align percentage-decision addendum with as-built (synthetic basket + legacy timestamp) |
| FIX-2 | `c57df97` | refactor(dc1-p5fu): delete test-only-dead legacy Process* helpers (P5-C4 closeout) |
| — | `fc99d44` | docs(dc1-p5fu): file Phase 5 follow-up closeout (this doc) |
| FIX-4 | _(this commit)_ | docs(dc1-p5fu): fix-commit B-V-R-Q + gpt-5.5 review NITs — comment/godoc accuracy + commit-count |

## 3. Design — the Adjustments-projection (P5-C3-full)

`result.Adjustments` (the public `ValuationResult.CleaningAdjustments` audit trail) was historically produced by 16 per-rule `*AdjusterOutputToLegacyResult` translators. This PR replaces them with ONE shared projection:

```
adjustmentsFromLedger(ledger entities.AdjustmentLedger, overlays []entities.OverlaySpec, perRuleMeta map[string]ruleMeta) []entities.Adjustment
```

at `internal/services/datacleaner/adjustment_projection.go`. It walks the native `data.AdjustmentLedger` in category order (asset → liability → earnings), looks up a static per-AdjusterID `ruleMeta` (Category/Type/FromAccount/ToAccount + Percentage mode + Amount source + Reasoning), and assembles the `entities.Adjustment`. The orchestrator (`service.go::applyActiveAdjustments`) calls it once after the three dispatchers run.

**`Adjustment.Percentage` preservation (ARCH Path (a)).** 6 Restater/FlagEmitter rules (A2, C2, C3, C4, C5, C6) compute a non-trivial Percentage from pre-state. That pre-state (`OtherIntangibles` for A2; `Revenue` for the C-rules) is captured on the FIRED `LedgerEntry.SkipMetrics` under the `original_<Field>` convention (`map[string]float64`, no schema bump). The projection reads it back with an explicit presence check (`d, ok := SkipMetrics[key]; ok && d > 0`) so a missing key is distinguishable from a legitimate zero — guarding the public API against silent `Percentage=0` degradation. Capture coverage is pinned by `TestPreStateCapture_OnFiredLedgerEntries` (6 sub-pins).

## 4. Deletions (P5-C4 + dead-helper sweep)

- **B1 (`569a892`):** 16 `*AdjusterOutputToLegacyResult` translators; dispatcher-side `original*` captures; 4 dormant earnings.go legacy-fallback helpers; dead `entities.{Asset,Liability}AdjustmentResult` duplicates. The 3 category `*AdjustmentResult` structs were SLIMMED to the native carrier (`Flags` + `NativeLedgerEntries` + `NativeOverlays` + `NativelyEmittedRuleIDs`) rather than renamed (they remain the live dispatcher return type). The legacy `result.Applied && result.Amount>0` tangible-recompute trigger was replaced by the native `assetArmTriggersTangibleRecompute(out)` predicate.
- **FIX-2 (`c57df97`):** 9 test-only-dead legacy singular `Process*Adjustment` helpers (A1/A2/A4/A5/C4 had unit-test-only callers; C5/C7 + the 2 flag-review helpers had zero callers) + their 5 orphaned unit tests. The live `Apply*` paths stay covered by the per-rule `*_adjuster_test.go` files. **Kept:** the live category dispatchers; B1/B2 calc engines (`ProcessOperatingLeaseAdjustment` / `ProcessPensionAdjustment`, delegated from `Apply*`); and B3's public `ProcessContingentLiabilityAdjustment` (a supported direct-call entry point exercised by the integration smoke test + AI-provenance coverage, deliberately ctx-threaded in Phase 3) plus its private `processContingentLiabilityAdjustment` calc engine.

Net: +5 / −935 lines in FIX-2; ~−2000 in B1.

## 5. DDM modelIBD flip (C1)

`service.go::performAlternativeValuation` removed the `model.ModelType() != "ddm"` exclusion so DDM's `modelIBD` reads `restatedViewOr(cleaned, latestFinancialData).InterestBearingDebt`, like every other alt-model. Bit-for-bit safe per Phase 5 spec §3.2 NOTE: no Restater touches `InterestBearingDebt` (B-rules are OverlayEmitters → `DebtLikeClaims`), so `Restated().InterestBearingDebt == latest.InterestBearingDebt` on the JPM/BAC/WFC fixtures. For FFO/revenue_multiple the prior `!=ddm` branch already overwrote with the same restated value, so the change is a true no-op for them.

## 6. Validation

**Full `/execute` B-V-R-Q + gpt-5.5 cross-model Q-pass** (per FEEDBACK-LOG 2026-05-30):
- **VERIFIER → VERIFIED:** build green; full suite 46 ok / 0 FAIL; all load-bearing pins PASS; DDM goldens untouched; basket-parity golden non-vacuous; deletion real.
- **REVIEWER → APPROVE_WITH_NITS:** all 16 rules' projection parity manually verified against the deleted translator bodies; no correctness defect.
- **QA → PASS:** DC-1 done-gate items 1–6 MET.
- **gpt-5.5 codereview (external) → no Critical/High blockers.** Surfaced 1 new MEDIUM (projection missing-key silent-degradation) + LOWs.

**Findings addressed (FIX-1/FIX-2/FIX-3):**
- F1 (MEDIUM, gpt-5.5): explicit `ok` presence check on the from_pre_state denominator. FIX-1.
- F2 (MEDIUM): 9 dead legacy helpers + 5 orphaned tests deleted. FIX-2.
- F3 (LOW): `assetArmTriggersTangibleRecompute` hardened to `Component != "" && DeltaAmount != 0` + A1-scoped overlay branch. FIX-1.
- F4/F5 (LOW, gpt-5.5): stale orchestrator comment + WARN-vs-silent-skip comments corrected. FIX-1.
- F6 (NIT): SkipMetrics godoc split. FIX-1.
- F7/F8 (LOW): addendum doc aligned with as-built (synthetic basket + legacy `time.Now()`). FIX-3.

**Load-bearing invariants GREEN at the final tip (`c57df97`), reproduced independently:**
`TestDDM_LegacyPath_BitForBit`, `TestDDM_ConsumerPath_RestatedViewParity`, `TestApplyActiveAdjustments_AdjustmentsProjection_BasketParity`, `TestApplyActiveAdjustments_FiringSignalParity_*`, `TestAdjustmentsProjection_*`, `TestPreStateCapture_OnFiredLedgerEntries`, `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_ClusterPrediction` + `_T2BS3_RestatedReconstruction`, `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_*`, `TestCleanedFinancialData_Restated_C6EquityOffsetZero`. Shadow snapshots byte-identical (`git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0). Full `go test ./... -count=1` EXIT=0 (46 packages).

## 7. CalculationVersion / SchemaVersion

No bumps. `CalculationVersion` stays `"4.4"` (both stamp sites). `SchemaVersion["FinancialData"]` stays 9; `["ValuationResult"]` stays 2. No SQLite migration. (P5-C3-full/P5-C4/IBD-flip are behavior-preserving from the API perspective; the only Phase-5 numeric drift — DDM `EnterpriseValue` for B-rule-firing banks — shipped in the PARTIAL's P5-C1.)

## 8. Remaining for DC-1 CLOSE (HUMAN / at-merge)

This PR completes the code work. The DC-1 milestone-status docs sweep is intentionally NOT applied on the branch because it asserts the merged-closed state (and needs the merge SHA). At merge, apply:
1. CLAUDE.md DC-1 Phase 5 bullet: PARTIAL → SHIPPED/CLOSED, with this PR's commit ladder.
2. Retire the CLAUDE.md "translator-still-load-bearing" gotcha (now inaccurate — translators deleted).
3. AGENTS row 17b + THESIS row 42: "in flight" → COMPLETE.
4. Archive `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` → `docs/reviewer/archive/`.
5. (Operator) Fresh CalcVersion-4.4 replay baseline capture for clean attribution (the `2026-05-19` baseline is `4.1`).

## 9. Change log

| Date | Change |
|---|---|
| 2026-06-02 | Implementation complete on `dc1-phase-5-followup`: P5-C3-full (A0–A4) + P5-C4 (B1) + DDM IBD flip (C1) + 3 review-fix commits (FIX-1/FIX-2/FIX-3) closing all B-V-R-Q + gpt-5.5 findings. All load-bearing invariants GREEN; full suite 46 ok / 0 FAIL; shadow byte-identical. Awaiting HUMAN review/merge. DC-1 milestone docs sweep deferred to merge (§8). |
| 2026-06-02 | Ran the mandated `/execute` B-V-R-Q subagent cycle + gpt-5.5 Q-pass on the FIX-commit delta (`b023694..fc99d44`), per FEEDBACK-LOG 2026-05-30 (the fixes had only been inline-validated). VERIFIER=VERIFIED, REVIEWER=APPROVE, QA=PASS, gpt-5.5=no Critical/High (F1/F3/FIX-2 independently confirmed behavior-preserving). FIX-4 (this commit) closes the resulting comment/doc-accuracy NITs: stale `assetArmTriggersTangibleRecompute` call-site comment, overstated F1-guard comment, stale `LedgerEntry` struct godoc Phase-2 "no consumer reads it" invariant, and this §2 commit-count fix. Comment/doc-only; no logic change; full suite re-verified GREEN. |
