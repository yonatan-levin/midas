# DC-1 Phase 5 — Partial Closeout (DDM Migration + DebtLikeClaims Correction + Firing-Signal Migration + Raw() Deletion)

**Status:** PARTIAL IMPLEMENTATION on branch `dc1-phase-5` (forked from master `b0239ed`) — awaiting HUMAN review. NOT yet merged to master.
**Date:** 2026-05-28
**Spec:** [datacleaner-component-primitive-and-parallel-views-phase-5-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md)
**Plan:** [datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md](./datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md)
**Phase 4 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md](./datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md)

---

## 1. What landed in this PR

| Cluster | SHA | Status | Scope |
|---|---|---|---|
| P5-C1 | `d76be69` | ✅ SHIPPED | DDM EV-bridge DebtLikeClaims correction (ADDED for DDM, OPPOSITE direction from DCF/revenue_multiple); CalculationVersion 4.3 → 4.4. Only numeric-drift commit. |
| P5-C2 | `0535fc5` | ✅ SHIPPED | DDM consumer migration to `Restated()` view via `LatestRestatedView`; 4-step bit-for-bit re-proof executed; `TestDDM_LegacyPath_BitForBit` GREEN at every commit. |
| P5-C3 | `586c370` | ⚠️ PARTIAL | Orchestrator firing-signal migrated to native sources (`Applied` bool → native ledger/overlays/flags). Adjustments-projection deferred (see §6). |
| P5-C4 | — | 🚫 DEFERRED | Per-rule translator + result-struct + dormant-fallback deletion. Gated on the full P5-C3 Adjustments-projection (see §6). |
| P5-C5 | `b617407` | ⚠️ PARTIAL | `cleaneddata.Raw()` deletion + concurrency-contract godoc + verify-then-decide on legacy `historicalData` slot (KEEP, with grep evidence). Phase-5 SHIPPED-bullet + DC-1 closeout-tracker archival deferred to follow-up. |

The two HIGHEST-risk commits (P5-C1 + P5-C2 — the DDM consumer-side bit-for-bit-load-bearing work) shipped with comprehensive verification. The cleaner-side scaffolding-retirement work (P5-C3 Adjustments-projection + P5-C4 deletion) was scoped down because the full projection requires a 16-entry per-AdjusterID metadata table (Category, Type, FromAccount, ToAccount) that this session did not complete; the firing-signal half of P5-C3 SHIPPED safely.

## 2. Commit ladder (branch `dc1-phase-5`)

| Cluster | SHA | Scope |
|---|---|---|
| P5-C1 | `d76be69` | DDM EV-bridge `+DebtLikeClaims` (legacy Gordon `ddm.go:127` + multi-stage `:399`); `modelDebtLikeClaims` populated for DDM in `service.go::performAlternativeValuation`; `CalculationVersion 4.3 → 4.4` both stamp sites; renamed `TestCalculationVersion_IsV43` → `IsV44`; four service_test.go version pins updated; new `TestDDM_EVBridge_AddsDebtLikeClaims` / `…_ZeroClaims_Unchanged` / `…_GoldenFixtures_ZeroDebtLikeClaims`. |
| P5-C2 | `0535fc5` | DDM reads migrated to `Restated()` view via `input.LatestRestatedView` in `runDividendDiagnostics` (SE/NI) + `estimateDividendGrowth` (SE/NI/DPS); service.go populates `LatestRestatedView` for DDM (nil-branch deleted); imports gained `cleaneddata`; phase4 invariance test → `ddm_phase5_invariance_test.go::TestDDM_ConsumerPath_RestatedViewParity` (superset: output bits + view-equals-entity property); two temporary re-proof tests added in-commit then deleted in-commit (4-step protocol). |
| P5-C3 (scoped) | `586c370` | Orchestrator `XResult.Applied` reads (3 sites) replaced with native firing-signal (`len(NativeLedgerEntries) > 0 \|\| len(NativeOverlays) > 0 \|\| len(Flags) > 0`); `result.Adjustments` / `result.Flags` slice reads NOT migrated (deferred); new parity tests `TestApplyActiveAdjustments_FiringSignalParity` + `…_EmptyFixture`. |
| P5-C5 (partial) | `b617407` | `cleaneddata.CleanedFinancialData.Raw()` deleted (`cleaned.go:79-96` removed); contract-test `views.Raw()` assertion deleted (`service_cleanwithviews_test.go` — historical Phase-3 pointer-identity assertion no longer applicable post-deletion); concurrency-safety godoc strengthened on `CleanedFinancialData` as a HARD request-local invariant (spec §3.6: no `sync.Once` retrofit); `historicalData.Data[latestPeriod]` slot population kept with documented grep evidence (six remaining `GetLatestPeriod()` consumers — spec §3.7 verify-then-decide → KEEP). |

## 3. What was preserved bit-for-bit

| Invariant | Path | Phase 5 result |
|---|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC `math.Float64bits`) | `models/ddm_bitforbit_test.go` | GREEN at every commit (P5-C1's `+0` DebtLikeClaims term + P5-C2's `Restated().X == latest.X` on fixtures both preserve bits). |
| `TestDDM_ConsumerPath_RestatedViewParity` (renamed from `…UnaffectedByPhase4`) | `models/ddm_phase5_invariance_test.go` | GREEN. Superset pin: output bits + view-field parity under the new view-consuming code path. |
| `TestRecomputeUmbrellas_NoMutation` | `datacleaner/recompute_test.go` | GREEN. Phase 5 leaves the recompute shadow shim untouched. |
| `TestOrchestrator_LedgerOrdering` | `datacleaner/ledger_invariants_test.go` | GREEN. Asset → liability → earnings partition preserved. |
| Shadow snapshots byte-identical | `internal/integration/testdata/recompute-shadow/<TICKER>.json` | `git diff --quiet` exits 0 at every commit (Phase 5 changes aggregation + dead code only — no adjuster execution change). |
| `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) | `datacleaner_ledger_basket_test.go` | GREEN. Per-ticker AdjusterID sets unchanged. |
| `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` | same | GREEN. AMD $9.679B / KO $60.912B reconstruction unchanged. |
| `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_*` | `datacleaner/service_cleanwithviews_no_double_count_test.go` | GREEN. HIGH-1 reducer fix unchanged. |
| `TestCleanedFinancialData_Restated_C6EquityOffsetZero` | `cleaneddata/restate_test.go` | GREEN. C6 invariant untouched. |
| `TestRevenueMultiple_SubtractsDebtLikeClaims` + `…_Forward_…` | `models/revenue_multiple_test.go` | GREEN. Phase 4 correction unaffected. |
| Full `go test ./... -count=1` | (full suite, 45 packages) | EXIT=0 at every commit. |

## 4. CalculationVersion / SchemaVersion

- `CalculationVersion 4.3 → 4.4` atomic with P5-C1 (the DDM EV-bridge correction is the only numeric-drift commit). Stamp sites: `service.go:1323` (DCF path), `:1635` (alt-model path). Five version-pin tests updated: `TestCalculationVersion_IsV43` → `IsV44`; four `service_test.go::result.CalculationVersion == "4.3"` assertions → `"4.4"`.
- No `SchemaVersion["FinancialData"]` bump (stays 9).
- No `SchemaVersion["ValuationResult"]` bump (stays 2).
- No SQLite schema migration.

## 5. Replay drift expectation

### 5.1 DDM tickers (JPM/BAC/WFC — fixture set)
- **IntrinsicValuePerShare, EquityValue:** ZERO drift (dividend-derived; independent of debt terms and of the Restated view when no Restater fires).
- **EnterpriseValue:** ZERO drift on the fixtures (DebtLikeClaims=0 ⇒ +0 term ⇒ unchanged bits). For LIVE B-rule-firing banks (NOT in the fixture set), EnterpriseValue INCREASES by exactly the B1+B2+B3 amount — the intended §3.2 correction.
- `CalculationVersion`: `"4.3" → "4.4"` (structural field text).

### 5.2 Non-DDM basket tickers
- **ZERO numeric drift.** Phase 5 does NOT touch DCF / revenue_multiple / FFO consumer paths (Phase 4 territory). Expected output diff: `CalculationVersion: "4.3" → "4.4"` field text only.

### 5.3 Replay attribution caveat
Inherited from Phase 4 closeout §5: the `2026-05-19` baseline is `calculation_version "4.1"` (predates Phases 2/3/4 + the assumption-profile config change). A clean Phase 5 attribution requires a fresh `4.3` baseline captured at master's pre-P5 tip via live SEC/market capture (cache-bypass). Phase 5 BACKEND ran the hermetic test suite for pattern-detection (no UNEXPECTED ticker drift); the operator does the clean 4.3→4.4 attribution at gate time.

## 6. DEFERRED: full P5-C3 Adjustments-projection + P5-C4 translator/struct retirement

The plan's full P5-C3 scope (rewrite `result.Adjustments` aggregation to derive from `data.AdjustmentLedger` via a shared `adjustmentsFromLedger(ledger)` projection helper) was scoped DOWN to just the firing-signal migration in this session. Reasons:

1. **Per-AdjusterID metadata is heterogeneous.** Each of the 16 per-rule translators encodes its own `Category` / `Type` / `FromAccount` / `ToAccount` for the `entities.Adjustment` audit-trail record. A clean ledger-only projection requires a 16-entry lookup table extracted from the translators. The extraction is mechanical (grep + tabulate) but exhaustive.
2. **Some translators capture pre-state.** A2's `Adjustment.Percentage` field is `writedownAmount / originalIntangibles`; `originalIntangibles` is captured BEFORE `ApplyA2Intangible` runs and is NOT preserved on the `LedgerEntry`. A strict ledger-only projection LOSES `Percentage`. Either: (a) thread the captured pre-state through to the projection (added scope), OR (b) accept `Percentage = 0` in the projected Adjustment (API contract degradation — needs explicit acceptance).
3. **API contract surface.** `ValuationResult.CleaningAdjustments` is a public JSON field (per `core/entities/valuation.go:64`). Any field-level content loss would break downstream API consumers.

### 6.1 Recommended next-session approach for the deferred work

**Single PR combining the deferred P5-C3 Adjustments-projection + P5-C4 deletion** (since they are tightly coupled — the projection MUST land before the translators are deleted, and the translators have no other reason to stay).

Tasks:

1. **Extract the per-AdjusterID metadata table.** Walk each `*AdjusterOutputToLegacyResult` translator in `assets.go` / `liabilities.go` / `earnings.go`; record the `entities.Adjustment{...}` literal's Category / Type / FromAccount / ToAccount / (optional) Percentage-source for each rule. Land as a private `var perRuleAdjustmentMeta = map[string]ruleMeta{...}` in `internal/services/datacleaner/adjustment_projection.go`.

2. **Decide Percentage handling.** Either (a) populate `LedgerEntry.SkipMetrics["original_X"]` at adjuster fire-time and read it in the projection, OR (b) accept Percentage=0 in the projected Adjustment with ARCH approval (document the API-contract impact). Recommend (a) for behavioral preservation.

3. **Build `adjustmentsFromLedger(ledger, overlays, perRuleAdjustmentMeta) []entities.Adjustment`.** Match overlays to LedgerEntries by AdjusterID; choose Amount from `|DeltaAmount|` (Restater) or `OverlaySpec.Amount` (OverlayEmitter).

4. **Add basket parity test.** Capture pre-rewrite `result.Adjustments` content as a golden for the 10-ticker basket; assert byte-identical content post-rewrite (excluding non-deterministic ID and Timestamp fields — pin RuleID, Category, Type, Amount, FromAccount, ToAccount, Applied).

5. **Rewrite the orchestrator** to call `adjustmentsFromLedger` after dispatcher completion and read flags from a new `NativeFlags []entities.Flag` field on the slimmed result struct (or directly from `AdjusterOutput.Flags` if the dispatchers are reshaped).

6. **P5-C4 deletion (gated on step 5):**
   - Delete the 16 per-rule translators (`a1…`/`a2…`/`a4…`/`a5…`/`aRD…`/`aCapSoftware…` in assets.go; `b1…`/`b2…`/`b3…` in liabilities.go; `c1…`-`c7…` in earnings.go).
   - Change dispatcher return types: drop the legacy `*{Asset,Liability,Earnings}AdjustmentResult` struct; return a slim native carrier with `Flags`/`NativeLedgerEntries`/`NativeOverlays`/`NativelyEmittedRuleIDs`.
   - Delete the per-category result structs `AssetAdjustmentResult` (assets.go:2092), `LiabilityAdjustmentResult` (liabilities.go:81), `EarningsAdjustmentResult` (earnings.go:53).
   - Delete the dormant `earnings.go` legacy-fallback `ProcessRestructuringChargesAdjustment` / `ProcessAssetSaleGainsAdjustment` / `ProcessLitigationSettlementsAdjustment` + capitalized-interest helper.
   - Verify-then-delete `entities.AssetAdjustmentResult` / `entities.LiabilityAdjustmentResult` in `core/entities/data_cleaning.go:468-485` (grep-confirmed dead code in this session — see spec §4.5).
   - Re-point the per-rule `adjustments/*_test.go` tests that assert on `result.Adjustments[0].Amount` etc.

7. **Re-run the full B-V-R-Q + gpt-5.5 cross-model review for the combined commit.**

Estimated effort: 4-7 agent-hours.

## 7. Judgment calls in this session

1. **P5-C1's test name `TestDDM_EVBridge_AddsDebtLikeClaims`** — renamed from the plan's `TestDDM_EVBridge_SubtractsDebtLikeClaims`. The plan acknowledged the original name was misleading ("despite the name, asserts the `+DebtLikeClaims` EV identity"). Renamed for sign clarity; the parallel with `TestRevenueMultiple_SubtractsDebtLikeClaims` is by purpose (both prove the bridge respects DebtLikeClaims), not by sign.
2. **Added `TestDDM_GoldenFixtures_ZeroDebtLikeClaims`** as a new defensive pin (not specified in the plan). It explicitly pins the property the bit-for-bit safety argument depends on (JPM/BAC/WFC golden JSONs deserialize `DebtLikeClaims=0`), so a future fixture refresh that adds a non-zero `DebtLikeClaims` would fail loudly here rather than silently breaking the `+0` argument.
3. **P5-C2 `modelIBD` kept on legacy entity read for DDM.** The spec §3.2 NOTE said an optional flip to `restated.InterestBearingDebt` would be bit-for-bit safe; this session kept the legacy `latestFinancialData.InterestBearingDebt` to minimize the bit-for-bit surface in the EV-correction commit (per ARCH's recommendation in the spec). Future migration is straightforward when desired.
4. **P5-C3 scoped to firing-signal only.** Full Adjustments-projection deferred per §6 reasoning above. The scope-cut is documented as the partial nature of this PR's deliverable.
5. **P5-C5 partial.** The Raw() deletion + concurrency contract + slot decision SHIPPED. The DC-1 closeout-tracker archival + CLAUDE.md "Common Gotchas" sweep are deferred alongside the P5-C4 work (those docs benefit from being filed AFTER the translator deletion lands).

## 8. NON-goals honored

| NON-goal | Status |
|---|---|
| No DDM math change (only view-source migration) | HONORED |
| No DDM legacy-Gordon path reordering / helper extraction | HONORED |
| No accessor `sync.Once` retrofit | HONORED (formalized request-local godoc instead) |
| No legacy `historicalData` slot population removal | HONORED (verify-then-decide → KEEP) |
| No `SchemaVersion` bump | HONORED (`FinancialData` stays 9; `ValuationResult` stays 2) |
| No SQLite schema migration | HONORED |
| No multi-stage DDM bit-for-bit pinning (legacy only) | HONORED |

## 9. Phase 5 → DC-1 close gate status

| Gate item | Status |
|---|---|
| 1. All Phase 5 invariants GREEN (§8.1) incl shadow byte-identity | DONE for the commits that shipped (C1, C2, C3-scoped, C5-partial). |
| 2. `TestDDM_LegacyPath_BitForBit` GREEN at every commit | DONE. |
| 3. DDM reads `Restated()` view; DDM EV bridge adds `DebtLikeClaims` | DONE. |
| 4. `grep -rn '\.Raw()' internal/` returns no matches | DONE (only doc-comment matches remain; the method symbol is gone). |
| 5. Per-rule translators + `*AdjustmentResult` structs + dormant fallbacks deleted | NOT DONE — deferred (§6). |
| 6. `CalculationVersion` 4.4 everywhere stamped | DONE. |
| 7. Replay diff matches §5 per ticker | DEFERRED to operator (stale baseline; see §5.3). |
| 8. Fresh `4.3` baseline captured for clean attribution | DEFERRED to operator. |
| 9. Phase 5 closeout doc filed | DONE (this file). |
| 10. DC-1 close-out (CLAUDE.md / AGENTS / THESIS / ARCHITECTURE row updates + tracker archive) | NOT DONE — deferred to follow up with the P5-C4 deletion work. |

Open: §6 (deferred P5-C3 projection + P5-C4 deletion), §9 items 5, 7, 8, 10. DC-1 is NOT closed by this PR; a follow-up PR completes the close.

## 10. Change log

| Date | Change |
|---|---|
| 2026-05-28 | Phase 5 PARTIAL implementation on branch `dc1-phase-5`: SHIPPED P5-C1 (`d76be69` — DDM EV-bridge DebtLikeClaims correction + CalcVersion 4.4); SHIPPED P5-C2 (`0535fc5` — DDM view migration with bit-for-bit re-proof); SHIPPED P5-C3 SCOPED (`586c370` — orchestrator firing-signal migration only); SHIPPED P5-C5 PARTIAL (`b617407` — Raw() deletion + concurrency contract godoc + verify-then-decide on legacy slot). DEFERRED: full P5-C3 Adjustments-projection + P5-C4 translator/struct deletion + DC-1 close docs sweep (§6 + §9). All load-bearing invariants GREEN at every commit; full `go test ./... -count=1` EXIT=0; shadow snapshots clean. |
