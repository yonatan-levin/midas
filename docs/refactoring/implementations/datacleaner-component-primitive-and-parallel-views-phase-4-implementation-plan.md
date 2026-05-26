# DC-1 Phase 4 — Implementation Plan (Consumer Migration + B3 Routing Flip + Dual-Write Deletion)

**Phase:** Phase 4 of the DC-1 refactor sequence — 13-site consumer migration, B3 routing flip, dispatcher dual-write deletion, `CalculationVersion` bump
**Status:** READY FOR BACKEND DISPATCH (authored 2026-05-26)
**Estimated effort:** 12–18 agent-hours single PR (5 commit clusters; ~2–3 hours per cluster average; cluster C-4 ~4–5 hours due to drift verification)
**Branch base:** master `3490227` (DC-1 Phase 3 followup merge) or later

**Spec:** [datacleaner-component-primitive-and-parallel-views-phase-4-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md)
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-spec.md)
**Phase 3 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md](./datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md)
**Phase 3 followup closeout:** [dc1-phase-3-followup-closeout.md](./dc1-phase-3-followup-closeout.md)

---

## Worktree workflow (REQUIRED)

Per `feedback_worktree_first_workflow` MEMORY rule: the main `midas/` directory MUST stay on `master`. Phase 4 work happens in a sibling worktree:

```bash
# From the main midas/ directory (on master)
git worktree add ../midas-dc1-phase-4 -b dc1-phase-4 master
cd ../midas-dc1-phase-4
```

Confirm before EVERY `git commit`:
- `pwd` should end with `midas-dc1-phase-4`
- `git rev-parse --abbrev-ref HEAD` should print `dc1-phase-4`
- `git worktree list` should show your worktree alongside the main midas/ at master

If anything looks wrong, STOP and re-check. The bash-branch-switch friction is real; the worktree is the firewall.

The current `midas-dc1-phase-4-prep` worktree on branch `dc1-phase-4-prep` holds ONLY the spec + plan + handoff docs; BACKEND should create a separate `dc1-phase-4` worktree from master for the implementation work. The prep branch's documentation lands ahead of implementation (a small ARCH-only PR if desired, or coalesced into the Phase 4 PR — implementer's choice).

---

## Required reading (in order)

### Tier 1 — Identity & continuity (read in 5 minutes)
1. **`CLAUDE.md`** — DC-1 Phase 2/3/followup SHIPPED bullets (post-sweep — records merge SHAs + Phase 4 references).
2. **`AGENTS.md` row 17b** — DC-1 entry.
3. **`docs/THESIS.md` DC-1 row** — phase status table.

### Tier 2 — Phase 4 spec + Phase 3 + followup ground truth
4. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md`** — THIS PHASE's authoritative spec. Focus on §4 (Architecture / Migration map / Plumbing), §7 (DDM bit-for-bit), §8 (Testing), §9 (DDM sub-plan), §11 (PR strategy), §13 (Acceptance criteria).
5. **`docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-3-spec.md`** — Phase 3 spec, ESPECIALLY §3 Non-goals (now Phase 4 goals) and §"Phase 3 → Phase 4 gate."
6. **`docs/refactoring/archive/dc1-phase-3-followup-spec.md`** — followup spec; ESPECIALLY §4.1 HIGH-1 (Option A pre-clean snapshot). Phase 4's HIGH-1 invariant preservation depends on understanding this.
7. **`docs/refactoring/archive/dc1-phase-3-followup-closeout.md`** — what's on master now.
8. **`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`** — parent spec; re-read §"Consumer migration map" (the 13 read sites) + §"Phasing & implementation sequence" Phase 4 row + the new Phase 5 row.

### Tier 3 — Code Phase 4 builds on (read with the spec open)
9. **`internal/services/datacleaner/cleaneddata/`** — the full package. `cleaned.go` (`New(asReported, restated)`); `view.go` (`FinancialDataView` + `ViewKind`); `asreported.go` (identityCopy); `restate.go` (post-followup reducer applies ONLY EquityOffset + TaxShieldDTA); `invested_capital.go` (applies overlays).
10. **`internal/services/datacleaner/service.go`** — `CleanFinancialData(ctx, data)` (line ~280) and `CleanFinancialDataWithViews(ctx, data)` (line 351). The latter is the Phase 4 entry point.
11. **`internal/services/datacleaner/adjustments/assets.go`** — A1 + A2 + A4 + A5 dual-write sites (lines 1056, 1145, 1219-1220, 1296, plus the in-arm replicas at 1410-1411, 1459, 1508, 1557). These get DELETED in clusters C-2 + C-4.
12. **`internal/services/datacleaner/adjustments/liabilities.go`** — B1 + B2 + B3 dispatcher dual-write at line 303 (inline `data.TotalDebt += result.Amount`). Deleted in cluster C-4.
13. **`internal/services/datacleaner/adjustments/earnings.go`** — C1-C7 dual-write sites (lines 1380, 1407, 1427, 1490, 1524, 1615, 1657, 1717, 1816-1819, 1863). Deleted in cluster C-3.
14. **`internal/services/valuation/service.go`** — the 13 read sites at lines 478 (clean call), 604-607 (ROIC), 678-682 (D/E ratio), 708-710 (WACC inputs), 915-919 (NOPAT fallback), 1033 (negative-OI sentinel), 1191-1197 (EV→Equity bridge), 1205-1208 (equity_bridge trace), 1330-1343 (cross-check), 1451-1461 (ModelInput), 1479 (effectiveOI), 1563-1578 (tangible value), 1634-1637 (effectiveOI helper), 1753-1771 (NWC change).
15. **`internal/services/valuation/graham.go`** — Graham reads (lines 61, 64, 86-117).
16. **`internal/services/valuation/models/ddm.go`** — DDM reads (lines 178, 181, 213, 291, 292). **DO NOT MODIFY in Phase 4** per §7. Read to understand the bit-for-bit-pinned legacy Gordon path.
17. **`internal/services/valuation/models/router.go`**, `revenue_multiple.go`, `ffo.go` — alt-model reads that flip in cluster C-3.
18. **`internal/services/valuation/currency.go`** — FX-conversion site. **DO NOT MODIFY in Phase 4** per §4.2.10.
19. **`pkg/finance/dcf/equity_value.go`** (path inferred; verify) — `dcf.CalculateEquityValue` signature. Cluster C-4 adds `dcf.CalculateEquityValueWithDebtLikeClaims`.

### Tier 4 — Test fixtures + baselines
20. **`internal/services/datacleaner/service_cleanwithviews_no_double_count_test.go`** — HIGH-1 regression pin. Must stay GREEN under Phase 4's dispatcher contract change (§8.2.1 Option A).
21. **`internal/integration/datacleaner_ledger_basket_test.go`** — `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` (AMD/KO).
22. **`internal/integration/testdata/recompute-shadow/`** — per-ticker shadow snapshots. `git diff --quiet` must exit 0 at every commit.
23. **`internal/services/valuation/models/testdata/golden/`** — JPM/BAC/WFC DDM bit-for-bit goldens.
24. **`artifacts/tier2-baseline/2026-05-19/`** — replay baseline. The post-Phase-4 refresh creates a new date-subdirectory at ship time.

---

## Tasks

### Cluster C-1 — Plumbing: thread `*cleaneddata.CleanedFinancialData` through valuation service

**Goal:** wire `CleanFinancialDataWithViews` into `service.go`'s cleaner-call site; thread the resulting `*CleanedFinancialData` through `performValuation` and `runAlternativeModel` as an additional parameter. Zero consumer reads migrated yet. Zero behavior change.

**Effort estimate:** ~2 agent-hours.

| Task | Files touched | Acceptance signal |
|---|---|---|
| 4.1 | `internal/services/valuation/service.go`: replace `cleaningResult, err = s.dataCleaner.CleanFinancialData(ctx, latest)` (line 478) with `cleaningResult, cleaned, err = s.dataCleaner.CleanFinancialDataWithViews(ctx, latest)`. Add `cleaned *cleaneddata.CleanedFinancialData` local with nil-zero default. Thread `cleaned` as a new parameter through `performValuation(...)` (sig change) and `runAlternativeModel(...)` (sig change). Pass `cleaned` through; do NOT consume yet. | `internal/services/valuation/service.go` only | `go build ./...` exit 0; full `go test ./... -count=1` GREEN (no behavior change) |
| 4.2 | Update `service_test.go` `MockDataCleanerService` to implement the new `CleanFinancialDataWithViews` method if it doesn't already (Phase 3 followup added it; verify). Update any tests that construct a `*Service` directly. | `internal/services/valuation/service_test.go`; `internal/services/valuation/*_test.go` as needed | `go test ./internal/services/valuation/... -count=1` GREEN |

**Commit message template:**
```
phase 4 C-1: plumb *CleanedFinancialData through valuation service

Replace s.dataCleaner.CleanFinancialData with
CleanFinancialDataWithViews at service.go:478. Thread the returned
*cleaneddata.CleanedFinancialData as a new parameter through
performValuation and runAlternativeModel. No consumer reads from
the views yet — every existing latestFinancialData.X read continues
to fire identically. Clusters C-2..C-5 migrate consumers one at a
time.

Zero behavior change. Zero numeric drift. Zero replay diff.

Plan task: 4.1 + 4.2. Spec §4.3.
```

---

### Cluster C-2 — Working capital + ROIC + effective-OI migrate to `Restated()`; A2/A4/A5 dispatcher dual-write deleted

**Goal:** migrate the 5 read sites that consume Restater-touched balance-sheet fields (ROIC denominator, NWC change latest+prior, effectiveOI helper); atomically delete the A2/A4/A5 dispatcher dual-write sites that fed `data.OtherIntangibles` / `Inventory` / `DeferredTaxAssets` / `TotalAssets`.

**Effort estimate:** ~3 agent-hours (the prior-period view bootstrap for NWC change is the time sink).

| Task | Files touched | Acceptance signal |
|---|---|---|
| 4.3 | `service.go::performValuation` — flip ROIC reads (lines 604, 606, 607): `nopat := restated.NormalizedOperatingIncome * (1 - latestForROIC.TaxRate)`; `growth.CalculateInvestedCapital(restated.StockholdersEquity, restated.InterestBearingDebt, latestForROIC.CashAndCashEquivalents)`. `restated := cleaned.Restated()` local at top of the block; nil-guard `if cleaned != nil`. | `service.go` only | `TestPerformValuation_RestatedReadsAtROIC` GREEN |
| 4.4 | `service.go::calculateNetWorkingCapitalChange` — refactor signature to accept `*cleaneddata.CleanedFinancialData` (for the latest) AND `*entities.FinancialData` (prior). Inside: `latestView := cleaned.Restated()`; for `priorView`, build a one-shot `cleaneddata.New(prior, prior).Restated()` (per spec §4.2.7 Option A). | `service.go` only | `TestPerformValuation_NWCChangeUsesRestated` (new) GREEN; existing NWC tests GREEN under the new shape |
| 4.5 | `service.go::effectiveOI` — change signature from `*entities.FinancialData` to `*cleaneddata.FinancialDataView`. Update call sites at line 1479 to pass `cleaned.Restated()`. Update the helper body (lines 1634-1637) to read `fd.NormalizedOperatingIncome` / `fd.OperatingIncome` from the view (field names unchanged). | `service.go` only | `go build ./...` exit 0; `go test ./internal/services/valuation/... -count=1` GREEN |
| 4.6 | `internal/services/datacleaner/adjustments/assets.go` — DELETE the A2/A4/A5 dispatcher dual-write lines. A2: `data.OtherIntangibles -= writedownAmount` (line 1145), `data.TotalAssets -= writedownAmount` (line 1145 sibling). A4: `data.TotalAssets -= valuationAllowance` (line 1296, 1557). A5: `data.Inventory -= writedownAmount` (line 1219), `data.TotalAssets -= writedownAmount` (line 1220, 1508). Replace with a generic apply-LedgerEntry-component-delta helper per spec §8.2.1 Option A: after `Apply*` returns `AdjusterOutput`, the dispatcher reads `output.LedgerEntries`, applies `e.DeltaAmount` to `working.<e.Component>` via a small switch (covering "Inventory", "OtherIntangibles", "DeferredTaxAssets"). Do NOT touch the umbrella (`TotalAssets`) — umbrellas are recomputed in `Restated()`. | `internal/services/datacleaner/adjustments/assets.go` | `TestDispatcherDualWriteDeleted_Assets` (new) GREEN; `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` GREEN; shadow snapshots byte-identical |

**Commit message template:**
```
phase 4 C-2: migrate ROIC + NWC + effectiveOI to Restated(); delete A2/A4/A5 dispatcher dual-writes

Migrate the 5 consumer reads (ROIC denominator at service.go:604,
NWC latest+prior at service.go:1753+1767, effectiveOI helper) to
cleaned.Restated()-sourced reads. The NWC prior period is wrapped
in a one-shot cleaneddata.New(prior, prior) per spec §4.2.7 — bit-
for-bit identical to prior.X today because no Restater fires on
the prior period.

Atomically delete the A2/A4/A5 dispatcher dual-writes in assets.go
(lines 1145, 1219-1220, 1296, 1410-1411, 1459, 1508, 1557). The
dispatcher now applies the LedgerEntry's component delta only —
umbrellas are recomputed in Restated()'s reducer. data.TotalAssets
on the post-clean entity may be incoherent relative to its
components; no Phase 4 consumer reads data.TotalAssets directly
(see CLAUDE.md update in C-5).

Drift expectation: ZERO numeric drift in 17-response.json across
the basket (Phase 1 shadow analysis: no asset-side Restater fires
on AAPL/MSFT/JNJ/F/MXL/EQIX; AMD/KO/BABA/TSM unchanged).

Load-bearing invariants stayed GREEN.

Plan tasks: 4.3 + 4.4 + 4.5 + 4.6. Spec §4.2.1 + §4.2.7 + §4.2.8 + §4.4 + §8.2.1.
```

---

### Cluster C-3 — DCF FCF / cross-check / alt-model OI inputs migrate to `Restated()`; C-rule dispatcher dual-write deleted

**Goal:** migrate the 9 read sites that consume Restater-touched earnings fields (NOPAT-fallback guard, negative-OI sentinel, cross-check ImpliedPE/PFCF, router OI, alt-model OI inputs); atomically delete C1/C2/C3/C5/C6 dispatcher dual-writes.

**Effort estimate:** ~3 agent-hours.

| Task | Files touched | Acceptance signal |
|---|---|---|
| 4.7 | `service.go::performValuation` — flip NOPAT-fallback guard at line 915-919 + negative-OI sentinel at line 1033 to `restated := cleaned.Restated()`. | `service.go` | `TestPerformValuation_CrossCheckReadsRestated` (new) GREEN |
| 4.8 | `service.go::performValuation` — flip cross-check inputs at lines 1330-1343 to `restated.NetIncome` / `restated.OperatingIncome` / `restated.DepreciationAndAmortization` / `restated.CapitalExpenditures`. | `service.go` | same as 4.7 |
| 4.9 | `service.go::runAlternativeModel` — flip `modelInput.InterestBearingDebt = restated.InterestBearingDebt` (line 1460) and `modelInput.CashAndCashEquivalents = restated.CashAndCashEquivalents` (line 1461) FOR revenue_multiple and ffo paths. For DDM path, KEEP `latestFinancialData.InterestBearingDebt` (preserves DDM bit-for-bit per spec §7 + §9). Use a small branching helper that reads `model.ModelType()` to decide. | `service.go` | `TestDDM_LegacyPath_BitForBit` GREEN; `TestDDM_ConsumerPath_UnaffectedByPhase4` (new) GREEN |
| 4.10 | `internal/services/valuation/models/router.go` — flip `baseOI := financials.NormalizedOperatingIncome` (line 158) and fallback (line 160) to read from a `FinancialDataView` parameter the router now accepts (added in the router signature). | `models/router.go` | router tests GREEN |
| 4.11 | `internal/services/valuation/models/revenue_multiple.go` — lines 108, 149, 151, 203 flip `input.InterestBearingDebt` to read from the migrated `modelInput.InterestBearingDebt` (which is now `Restated()`-sourced per 4.9). NOTE: `input.InterestBearingDebt` field name is UNCHANGED — the value flowing in changes. | `models/revenue_multiple.go` only if the field migration needs a shape change | revenue_multiple tests GREEN |
| 4.12 | `internal/services/valuation/models/ffo.go` — line 331 reads `input.InterestBearingDebt` (Restated-sourced); lines 357-362 read `latest.OperatingIncome` — for the FFO model, migrate `latest` to the Restated view passed via ModelInput. Add a new `ModelInput.LatestRestatedView *cleaneddata.FinancialDataView` field; populate in `runAlternativeModel`. ffo.go reads `input.LatestRestatedView.OperatingIncome` instead of `latest.OperatingIncome`. | `models/ffo.go`, `models/model_input.go` (or equivalent) | ffo tests GREEN |
| 4.13 | `internal/services/datacleaner/adjustments/earnings.go` — DELETE C1/C2/C3/C5/C6 dispatcher dual-writes at lines 1380, 1407, 1427, 1490, 1524, 1615, 1657, 1717, 1816-1819, 1863. Replace with the generic Apply-LedgerEntry-component-delta helper (per 4.6's pattern; switch covers "NormalizedOperatingIncome", "InterestExpense"). | `internal/services/datacleaner/adjustments/earnings.go` | `TestDispatcherDualWriteDeleted_Earnings` (new) GREEN; HIGH-1 pin GREEN; shadow snapshots byte-identical |

**Commit message template:**
```
phase 4 C-3: migrate DCF + cross-check + alt-model OI to Restated(); delete C-rule dispatcher dual-writes

Migrate 9 consumer reads in performValuation, router, revenue_multiple,
ffo to Restated()-sourced reads. NOPAT-fallback guard, negative-OI
sentinel, ImpliedPE/PFCF cross-check, alt-model OI all flow through
cleaned.Restated(). FFO model gains modelInput.LatestRestatedView
slot for its latest.OperatingIncome NAV check.

DDM path EXPLICITLY preserved: runAlternativeModel reads
latestFinancialData.InterestBearingDebt for DDM (branching on
model.ModelType()). JPM/BAC/WFC bit-for-bit invariant intact.

Delete C1/C2/C3/C5/C6 dispatcher dual-writes in earnings.go (10
sites). Dispatcher now applies LedgerEntry component delta only;
no umbrella mutation. C4/C7 stay as FlagEmitter — no mutation
existed to delete.

Drift expectation: ZERO numeric drift in 17-response.json across
the basket (Phase 1 shadow: no C-rule fires on basket fixtures).

Plan tasks: 4.7-4.13. Spec §4.2.2 + §4.2.3 + §4.2.6 + §4.2.13 + §4.4 + §7.
```

---

### Cluster C-4 — WACC inputs + EV→Equity bridge + B3 routing flip + A1/B1/B2/B3 dispatcher dual-write deleted + `CalculationVersion 4.2 → 4.3`

**Goal:** the most numerically-consequential commit. WACC inputs read `Restated()` (B-rules no longer feed capital structure); EV→Equity bridge subtracts `InvestedCapital().DebtLikeClaims` via a NEW DCF function; A1 + B1 + B2 + B3 dispatcher dual-writes deleted; `CalculationVersion` bumped.

**Effort estimate:** ~4–5 agent-hours (drift verification + new DCF function + careful B3 test design).

| Task | Files touched | Acceptance signal |
|---|---|---|
| 4.14 | Add `dcf.CalculateEquityValueWithDebtLikeClaims(ev, debt, cash, minority, preferred, debtLikeClaims float64) float64` to `pkg/finance/dcf/`. Body: `ev - debt + cash - minority - preferred - debtLikeClaims`. Tests pin the formula. KEEP the legacy 5-arg `dcf.CalculateEquityValue` for alt-model callers (no change). | `pkg/finance/dcf/equity_value.go`; `pkg/finance/dcf/equity_value_test.go` | new function tests GREEN |
| 4.15 | `service.go::performValuation` — flip WACC inputs at lines 678-682 + 708-710 to `restated := cleaned.Restated()`; `waccInputs.MarketValueOfDebt = restated.InterestBearingDebt`; `waccInputs.InterestExpense = restated.InterestExpense`. | `service.go` | `TestPerformValuation_WACCUnaffectedByB3` (new) GREEN |
| 4.16 | `service.go::performValuation` — flip EV→Equity bridge at lines 1191-1197. Replace `dcf.CalculateEquityValue(...)` with `dcf.CalculateEquityValueWithDebtLikeClaims(dcfResult.EnterpriseValue, restated.InterestBearingDebt, latestFinancialData.CashAndCashEquivalents, latestFinancialData.MinorityInterest, latestFinancialData.PreferredEquity, cleaned.InvestedCapital().DebtLikeClaims)`. Update the `equity_bridge` calc trace at lines 1205-1208 to also emit `debt_like_claims`. | `service.go` | `TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims` (new) GREEN |
| 4.17 | `internal/services/datacleaner/adjustments/assets.go` — DELETE A1 dispatcher dual-write at lines 1056-1057 + 1410-1411 (`data.Goodwill = 0; data.TotalAssets -= originalGoodwill`). The A1 OverlaySpec is already emitted by `ApplyA1` (Phase 2 PR-2); the dispatcher dual-write was the legacy data.* mutation. Delete. | `internal/services/datacleaner/adjustments/assets.go` | shadow snapshots byte-identical (A1 fire still recorded in ledger; just no `data.X` mutation) |
| 4.18 | `internal/services/datacleaner/adjustments/liabilities.go` — DELETE the dispatcher's `data.TotalDebt += result.Amount` line (303) and any sibling B-rule mutation. After deletion, the dispatcher reads `output.Overlays` and drains them into `data.Overlays` (already done) but does NOT mutate `data.TotalDebt`. B-rules are OverlayEmitters — they DON'T have a `Component`/`DeltaAmount` on their LedgerEntry, so the generic apply-component-delta helper skips them via the `Component == ""` guard. | `internal/services/datacleaner/adjustments/liabilities.go` | `TestDispatcherDualWriteDeleted_Liabilities` (new) GREEN; HIGH-1 pin GREEN |
| 4.19 | Bump `CalculationVersion`. Search for `"4.2"` literal in `internal/services/valuation/service.go` (line 1255 DCF path + line 1524 alt-model path) and replace with `"4.3"`. | `service.go` | `TestCalculationVersion_IsV43` (new) GREEN |
| 4.20 | Run replay diff on AAPL + MSFT + KO + BABA + TSM bundles. Document drift in the commit message. Class III tickers (B-rule firers): WACC + EquityValue drift expected. Class I tickers (no Restater/B-rule): zero drift. Capture the per-ticker diff table in the commit description for REVIEWER. | `artifacts/tier2-baseline/2026-05-19/` (read-only) | drift matches spec §5 expectations; UNEXPECTED drift = STOP-AND-INVESTIGATE |

**Commit message template:**
```
phase 4 C-4: WACC + bridge migrate; B3 routing flip realized; A1+B1+B2+B3 dispatcher dual-writes deleted; CalcVersion 4.2 → 4.3

THE most numerically-consequential commit. The B3 routing flip
takes effect: WACC reads Restated().InterestBearingDebt (B-rule
amounts NO LONGER feed capital structure); EV→Equity bridge
subtracts InvestedCapital().DebtLikeClaims via the new
dcf.CalculateEquityValueWithDebtLikeClaims function.

For tickers with B-rule overlays (B1 lease, B2 pension, B3
contingent), WACC weights shift AND the equity-bridge subtraction
changes. The drift is the entire point of the refactor: contingent
liabilities ARE shareholder claims; they were being mis-treated as
interest-bearing capital previously.

CalculationVersion bumps 4.2 → 4.3 atomic with this commit. Both
DCF path (service.go:1255) and alt-model path (1524) updated.

Replay drift (basket measured at <ship-sha>):
  AAPL: zero numeric drift (Class I)
  MSFT: zero numeric drift (Class I)
  KO:   WACC <X> → <Y>, EquityValue <A> → <B>, DCF/share <P> → <Q>
  BABA: <…>
  TSM:  <…>

Plan tasks: 4.14-4.20. Spec §4.2.4 + §4.2.5 + §4.4 + §4.5 + §5.3.
```

---

### Cluster C-5 — Graham + tangible value migrate; currency.go untouched; DDM stays untouched; legacy translator + result-struct deletion (if eligible); docs sweep; closeout

**Goal:** finish the consumer migration tail (Graham → AsReported, tangible → Restated); explicitly skip DDM (per §7); explicitly skip currency.go (per §4.2.10); delete the now-vestigial per-rule translators and legacy `*AdjustmentResult` structs if grep confirms no callers; full docs sweep; Phase 4 closeout doc filed.

**Effort estimate:** ~3 agent-hours (docs sweep is the largest chunk).

| Task | Files touched | Acceptance signal |
|---|---|---|
| 4.21 | `internal/services/valuation/graham.go` — flip `fd.CurrentAssets` (line 61) → `asReported.CurrentAssets` where `asReported := cleaned.AsReported()`. Same for line 64 + the TotalLiabilities derivation at lines 106-117. Pass `cleaned` through `calculateGrahamFloorMetrics`'s signature; update its callers at service.go:1226 + service.go:1496-1497. | `internal/services/valuation/graham.go`; `internal/services/valuation/service.go` (2 call sites) | `TestPerformValuation_GrahamUsesAsReported` (new) GREEN; Graham warnings for AMD/KO still fire |
| 4.22 | `service.go::calculateTangibleValuePerShare` — flip `tangibleEquity := financial.TangibleAssets` (line 1563) → `tangibleEquity := cleaned.Restated().TangibleAssets`. Signature: accept `*cleaneddata.CleanedFinancialData` instead of `*entities.FinancialData`. Update call sites. | `internal/services/valuation/service.go` | `TestCalculateTangibleValuePerShare_UsesRestated` (new) GREEN |
| 4.23 | Verify `internal/services/valuation/currency.go` is UNCHANGED. Add a `// CURRENCY PRE-CLEANER INVARIANT — DO NOT MIGRATE` comment block at the top documenting why this file stays at `fd.X *= rate` (per spec §4.2.10). | `internal/services/valuation/currency.go` (comment only) | no behavior change |
| 4.24 | Verify `internal/services/valuation/models/ddm.go` is UNCHANGED. Add `TestDDM_ConsumerPath_UnaffectedByPhase4` to `internal/services/valuation/models/ddm_phase4_invariance_test.go` — exercises JPM/BAC/WFC end-to-end through `runAlternativeModel` AND asserts `latest.StockholdersEquity / .NetIncome / .DividendsPerShare` are byte-equal to pre-Phase-4 captures. | NEW: `internal/services/valuation/models/ddm_phase4_invariance_test.go` | new test GREEN at every Phase 4 commit (verify by `git rebase --exec` if needed) |
| 4.25 | Per-rule translator + legacy result-struct deletion. Grep for `{Asset,Liability,Earnings}AdjustmentResult` references; if grep yields ONLY internal datacleaner uses (no external test/observability callers), delete the structs + the per-rule translators (the small per-rule helpers in `internal/services/datacleaner/adjustments/*.go`). If grep yields external callers, DEFER to Phase 5 with a tracker entry. | `internal/services/datacleaner/adjustments/*.go`; deletion targets vary | `grep` confirms no remaining references OR deferral documented |
| 4.26 | `artifacts/tier2-baseline/` refresh — capture post-Phase-4 bundles for the basket. Create `artifacts/tier2-baseline/<post-ship-date>/` subdirectory with 10 tickers' bundles. Preserves the 2026-05-19 baseline as the pre-Phase-4 reference. | `artifacts/tier2-baseline/<date>/*` | replay baseline updated |
| 4.27 | Docs sweep — full update of: `CLAUDE.md` (DC-1 gotcha: append Phase 4 SHIPPED bullet); `AGENTS.md` row 17b (append); `docs/THESIS.md` DC-1 row (Phase 4 SHIPPED → Phase 5 next); parent spec changelog row dated ship-day; Phase 4 spec changelog row; this plan's changelog row; `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` (progress paragraph). | multiple docs | grep `Phase 4 SHIPPED` returns all expected files; cross-references resolve |
| 4.28 | Phase 4 closeout doc filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md` — mirrors the Phase 3 closeout template. Required sections: What landed; Commit ladder (with SHAs); Per-cluster drift table (per-ticker); HIGH-1 pin status under Phase 4 dispatcher contract change; DDM migration deferral to Phase 5 (with the Phase 5 follow-up steps from §7.3 + §9.4); Per-rule translator deletion status (or deferral); NON-goals honored table; Phase 4 → Phase 5 gate satisfied checklist. | NEW: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md` | closeout doc exists with all sections populated |

**Commit message template (C-5 is typically several small commits, one per docs/test, but a single commit is acceptable if scope-disciplined):**
```
phase 4 C-5: Graham + tangible migrate; DDM/currency confirmed untouched; vestigial translators deleted; docs sweep; closeout

Graham reader flips to AsReported() (NCAV stays a conservative as-
filed metric — A1 goodwill exclusion intentionally does NOT shift
the Graham floor upward). Tangible value per share flips to
Restated() (zero numeric change today; coherent for future
Restaters that touch intangibles).

currency.go EXPLICITLY untouched — FX conversion runs pre-cleaner;
both views inherit USD automatically. Annotation block added.

ddm.go EXPLICITLY untouched — bit-for-bit invariant preserved per
spec §7. New TestDDM_ConsumerPath_UnaffectedByPhase4 GREEN at every
Phase 4 commit.

Vestigial per-rule translators + {Asset,Liability,Earnings}-
AdjustmentResult structs deleted [OR deferred to Phase 5 if any
caller remains — see closeout doc].

artifacts/tier2-baseline/<post-ship-date>/ refreshed.

Docs sweep complete.

Closeout doc filed. Phase 4 → Phase 5 gate satisfied.

Plan tasks: 4.21-4.28. Spec §4.2.9 + §4.2.10 + §4.2.11 + §4.2.12 + §7 + §9 + §12 + §13.
```

---

## Per-commit acceptance gates (BEFORE every commit)

Every commit in the Phase 4 PR must pass:

1. **`go build ./...` exit 0**
2. **`go test ./internal/services/datacleaner/... -count=1`** GREEN — includes `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire`, `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire`, `TestCleanedFinancialData_Restated_C6EquityOffsetZero`, `TestQ2_A2TaxShieldDTA_Populated`, `TestQ4_AIProvenance_SHA256_Deterministic`, `TestIdentityCopy_CoversEveryViewField`
3. **`go test ./internal/services/valuation/models/... -run TestDDM_LegacyPath_BitForBit -count=1`** GREEN — JPM/BAC/WFC `math.Float64bits` equality. **THE most consequential gate.** If this fails on any Phase 4 commit, REVERT (per CLAUDE.md gotcha — do NOT update goldens).
4. **`go test ./internal/services/valuation/models/... -run TestDDM_ConsumerPath_UnaffectedByPhase4 -count=1`** GREEN (added in C-5, but the test file can land in C-1 in stub form and gain assertions through the clusters).
5. **`go test ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1`** GREEN
6. **`git diff --quiet internal/integration/testdata/recompute-shadow/`** exit 0 — shadow snapshots byte-identical
7. **`go test ./internal/integration/ -run TestLedger_BasketSnapshot_ClusterPrediction -count=1`** GREEN (10/10 tickers)
8. **`go test ./internal/integration/ -run TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction -count=1`** GREEN (AMD $9.679B / KO $60.912B)
9. **Full `go test ./... -count=1`** GREEN modulo any pre-existing scheduler-test race
10. **Phase 4-specific gate: replay drift verification.** After clusters C-2/C-3/C-4/C-5, run `go run ./cmd/replay --diff-stages --workers=4 artifacts/tier2-baseline/2026-05-19/`. The diff MUST match the spec §5 expectation for that cluster:
    - C-1: ZERO drift across basket.
    - C-2: ZERO drift across basket (no Restater fires on basket fixtures today).
    - C-3: ZERO drift across basket (no C-rule fires on basket fixtures today).
    - C-4: Class III tickers (B-rule firers) show expected WACC + EquityValue drift; `CalculationVersion: "4.2" → "4.3"` structural drift. Class I tickers byte-identical except for the version field.
    - C-5: ZERO additional drift (Graham/tangible migration has no fired-Restater target on basket).

Unexpected drift at any cluster = STOP-AND-INVESTIGATE before the next commit.

---

## Critical invariants — must stay GREEN at every commit

| Invariant | Path | Why load-bearing |
|---|---|---|
| `TestDDM_LegacyPath_BitForBit` | `internal/services/valuation/models/ddm_bitforbit_test.go` | Cross-Tier-2 contract. DDM deferred per spec §7; preserve trivially. |
| **NEW `TestDDM_ConsumerPath_UnaffectedByPhase4`** | NEW: `internal/services/valuation/models/ddm_phase4_invariance_test.go` | Pins that Phase 4's plumbing doesn't ripple into DDM's read path. |
| `TestRecomputeUmbrellas_NoMutation` | `internal/services/datacleaner/recompute_test.go` | Recompute shim untouched. |
| `TestOrchestrator_LedgerOrdering` | `internal/services/datacleaner/ledger_invariants_test.go` | Asset → liability → earnings partition. Dispatcher signature unchanged. |
| Shadow snapshots byte-identical | `internal/integration/testdata/recompute-shadow/<TICKER>.json` | Cleaner-side adjuster execution unchanged. |
| `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) | `internal/integration/datacleaner_ledger_basket_test.go` | Per-ticker expected AdjusterID sets unchanged. |
| `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` | same file | AMD/KO Restated reconstruction unchanged. |
| `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` | `internal/services/datacleaner/service_cleanwithviews_no_double_count_test.go` | HIGH-1 invariant under Phase 4's dispatcher contract change. **Critical: the dispatcher now applies the component delta itself (per spec §8.2.1 Option A); the test still expects single application, not zero or double.** |
| `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire` | same file | Same for C-rules. |
| `TestCleanedFinancialData_Restated_C6EquityOffsetZero` | `internal/services/datacleaner/cleaneddata/restate_test.go` | C6 capitalized-interest stays out of equity. `restate.go` unchanged. |
| Full `go test ./...` exit 0 | (suite) | GREEN at every commit. |

---

## Gotchas inherited from Phase 2 / Phase 3 / followup

1. **Worktree discipline.** Stay in `midas-dc1-phase-4/`. Verify `pwd` + `git rev-parse --abbrev-ref HEAD` before EVERY commit. (Note: the prep work happens in `midas-dc1-phase-4-prep/`; BACKEND creates a SEPARATE `dc1-phase-4` worktree from master for implementation.)

2. **CRLF noise on Windows.** `git status` and `git diff` print CRLF warnings on shadow snapshots. Use `git diff --quiet internal/integration/testdata/recompute-shadow/` (with `--quiet`) and check exit code; exit 0 = byte-identical.

3. **Atomic SchemaVersion bump rule REMINDER.** `feedback_schema_version_atomic_bump` MEMORY rule says bump in the FIRST commit that populates a new field non-zero. Phase 4 does NOT add new `omitempty` fields to versioned structs → no `SchemaVersion` bump. ONLY `CalculationVersion` bumps (4.2 → 4.3 in C-4, line 1255 + 1524 of service.go).

4. **Dual-write contract preservation INVERTED.** Phase 3's followup §6 said: "DCF / WACC / DDM / FFO / Graham outputs: Bit-for-bit unchanged (no consumer migration)." Phase 4 INVERTS this: the dispatcher dual-write is DELETED, and consumer-visible outputs WILL drift on Class III tickers (B-rule firers). The dual-write that the followup "MUST preserve" is now what Phase 4 "MUST delete atomically." Honor the cluster ordering (consumer migration first within each cluster, then deletion of the dual-write that fed the migrated read) to avoid intermediate-state breakage.

5. **HIGH-1 pin under Phase 4.** The `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` test was written under the assumption that the dispatcher dual-write WAS the source of the post-clean component delta. Phase 4's Option A (spec §8.2.1) replaces dispatcher dual-write with "dispatcher applies LedgerEntry's component delta only" — so the post-clean entity's component fields STILL contain the delta. The HIGH-1 pin's assertion (`Restated().OtherIntangibles == original - writedown`) stays GREEN because the post-clean entity still has `original - writedown` (just applied by a different mechanism). Re-verify after each cluster.

6. **DDM bit-for-bit pin priority.** If `TestDDM_LegacyPath_BitForBit` fails at any Phase 4 commit, **REVERT immediately**. Do not update goldens. Re-evaluate the migration cluster that introduced the regression. Per CLAUDE.md: "Any change to mature-large-bank DDM math that fails this test must be REVERTED — do NOT update the goldens to make it pass."

7. **`Raw()` survives Phase 4.** The `cleaneddata.CleanedFinancialData.Raw()` method carries a `TODO(phase-5)` comment from the followup. Phase 4 does NOT delete it — Phase 5 owns the deletion. But Phase 4's acceptance criterion (§13) checks that grep yields no remaining `.Raw()` callers in `internal/` — IF there are, document them in the closeout for Phase 5.

8. **`historicalData.Data[latestPeriod] = cleaningResult.CleanedData` slot.** Phase 4 continues to populate this for DDM's `GetLatestPeriod()` read path. Phase 5 can revisit after DDM migrates. Do NOT remove the line in Phase 4.

9. **Per-rule translator deletion contingency.** Task 4.25 deletes the per-rule translators IF grep confirms zero external callers. The legacy `{Asset,Liability,Earnings}AdjustmentResult` structs may be referenced in test fixtures or external observability code (e.g., the calc trace emitter). Grep BEFORE deleting; defer to Phase 5 with a tracker entry if non-trivial callers remain.

10. **Replay baseline date discipline.** The current baseline is `artifacts/tier2-baseline/2026-05-19/`. Phase 4 ship creates `artifacts/tier2-baseline/<post-ship-date>/`. Do NOT overwrite the 2026-05-19 baseline — it's the cross-Tier-2 pre-Phase-4 reference. Future Phase N comparisons (Phase 5 onward) read from the post-Phase-4 baseline.

11. **ctx propagation.** Every `Process*Adjustments` signature added ctx in Phase 3. Phase 4 dispatcher dual-write deletion does NOT remove the ctx parameter. The generic apply-LedgerEntry-component-delta helper added in C-2/C-3 should accept ctx as the first parameter (Go convention).

12. **NWC change prior-period gotcha.** The `calculateNetWorkingCapitalChange` helper (service.go:1747) reads BOTH `latest` and `prior`. Phase 4 §4.2.7 Option A wraps `prior` in a one-shot `cleaneddata.New(prior, prior).Restated()`. The `prior` *FinancialData has empty `AdjustmentLedger`/`Overlays`, so the view reduces to identity. This is NOT free CPU — each call allocates a `CleanedFinancialData` struct + a `*FinancialDataView` value. Cache once per request if the call site fires multiple times (it doesn't today, but document).

---

## DDM migration sub-plan — most consequential subsection

(Mirrors spec §9. The implementer plan re-states for actionability.)

### What Phase 4 does to DDM

NOTHING beyond plumbing. The 5 read sites at `ddm.go:178/181/213/291/292` are untouched. The `modelInput.HistoricalData` slot continues to carry `*HistoricalFinancialData`.

### What changes UNDER DDM during Phase 4

Cluster C-2 / C-3 / C-4 delete dispatcher dual-writes for A-rules / C-rules / B-rules + A1. This changes the post-clean `*FinancialData` (`cleaningResult.CleanedData`) for any ticker where Restaters/B-rules fire. For JPM/BAC/WFC test fixtures, the Phase 1 shadow analysis shows empty `recent_adjusters` → no Restaters fire. Therefore `data.X` for JPM/BAC/WFC is unchanged post-Phase-4.

### Phase 4 BACKEND verification steps

For each commit C-1 through C-5:

1. `go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1` → GREEN.
2. `go test ./internal/services/valuation/models/ -run TestDDM_ConsumerPath_UnaffectedByPhase4 -count=1` → GREEN.

If either fails, **REVERT the commit** and re-analyze.

### Phase 5 DDM migration follow-up steps (documented in Phase 4 closeout)

1. Re-run shadow snapshots on JPM/BAC/WFC with the current test fixtures; verify `Restated().X == AsReported().X` for `StockholdersEquity`, `NetIncome`, `DividendsPerShare`.
2. Add a temporary parallel-write test: `TestDDM_RestatedView_BitForBit` runs DDM with `Restated()`-sourced reads against the same goldens. Verify GREEN.
3. ONLY THEN migrate `ddm.go:178/181/213/291/292` to `Restated()`.
4. Delete the temporary parallel-write test; `TestDDM_LegacyPath_BitForBit` continues to guard the math.

---

## Spec / doc updates required (Task 4.27)

| File | Update |
|---|---|
| `CLAUDE.md` | DC-1 gotcha: append "Phase 4 SHIPPED <date> (merge `<sha>`)" sub-bullet describing the 13-site migration, B3 routing flip, dispatcher dual-write deletion, CalculationVersion 4.3 bump, and DDM migration deferral to Phase 5. |
| `AGENTS.md` row 17b | Append "Phase 4 SHIPPED <date>" two-line summary. |
| `docs/THESIS.md` | DC-1 row: Phase 4 SHIPPED → Phase 5 next (DDM migration + Raw() deletion). |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` | Change-log row for ship date. Update §"Consumer migration map" if any row's view assignment changed from the spec proposal. |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md` (this spec's parent) | Change-log row for ship date + final commit SHAs. |
| `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md` (this file) | Change-log row for ship date + final commit SHAs. |
| NEW: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md` | Phase 4 closeout report — mirrors Phase 3 closeout template. |
| `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` | Progress paragraph for Phase 4 SHIPPED; reduce remaining-scope list to Phase 5 items. |

---

## Phase 4 → Phase 5 gate

Phase 5 dispatch happens only after all of:

1. All Phase 4 acceptance criteria checked (spec §13).
2. Phase 4 closeout doc filed with per-cluster commit SHAs + per-ticker drift table.
3. `TestDDM_LegacyPath_BitForBit` GREEN.
4. `TestDDM_ConsumerPath_UnaffectedByPhase4` GREEN.
5. `TestPerformValuation_WACCUnaffectedByB3` GREEN (B3 routing flip live).
6. `TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims` GREEN.
7. Replay basket diff matches spec §5 expectation (Class I byte-identical except CalcVersion text; Class III shows expected B-rule WACC drift).
8. `artifacts/tier2-baseline/<post-phase-4-date>/` refreshed.
9. HUMAN signoff on Phase 4 PR and merge to master.

Phase 5 scope (parent spec):
- DDM consumer migration (`ddm.go:178/181/213/291/292`).
- `cleaneddata.CleanedFinancialData.Raw()` deletion.
- Optional: `sync.Once` retrofit.
- Optional: stop populating `historicalData.Data[latestPeriod] = cleaningResult.CleanedData` if DDM is the last consumer.
- Optional: delete legacy `{Asset,Liability,Earnings}AdjustmentResult` structs if Phase 4 deferred their deletion.

---

## Change log

| Date | Change |
|---|---|
| 2026-05-26 | Initial Phase 4 implementation plan filed. Covers 28 tasks across 5 commit clusters (C-1 plumbing; C-2 working-capital + ROIC + asset Restater delete; C-3 DCF + cross-check + earnings Restater delete; C-4 WACC + bridge + B3 routing flip + A1+B-rule delete + CalcVersion 4.3; C-5 Graham + tangible + DDM/currency confirmation + vestigial cleanup + docs + closeout). PR strategy: single PR (5 commit clusters) recommended; 2-PR fallback if C-4 review surfaces design changes. Phase 4 → Phase 5 gate documented; DDM migration deferred to Phase 5 per spec §7. Spec at `datacleaner-component-primitive-and-parallel-views-phase-4-spec.md`. |
