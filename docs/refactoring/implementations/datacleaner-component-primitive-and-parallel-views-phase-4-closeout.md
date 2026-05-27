# DC-1 Phase 4 — Closeout (Consumer Migration + B3 Routing Flip + Dual-Write Deletion)

**Status:** IMPLEMENTED on branch `dc1-phase-4` (forked from master `9d745a9`) — awaiting REVIEWER + HUMAN merge. NOT yet merged to master.
**Date:** 2026-05-27
**Spec:** [datacleaner-component-primitive-and-parallel-views-phase-4-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md)
**Plan:** [datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md](./datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md)
**Phase 3 followup closeout:** [dc1-phase-3-followup-closeout.md](../archive/dc1-phase-3-followup-closeout.md)

---

## 1. What landed

Phase 4 migrated the valuation engine's consumer read sites off direct
`data.X` reads onto the `cleaneddata` view accessors (`AsReported()` /
`Restated()` / `InvestedCapital()`), atomically deleted the cleaner dispatcher
dual-writes that fed those reads (via §8.2.1 Option A), realized the **B3
routing flip** (contingent liabilities → `InvestedCapital().DebtLikeClaims`,
out of the WACC capital-structure denominator), and bumped `CalculationVersion`
4.2 → 4.3. DDM consumer migration is DEFERRED to Phase 5 (bit-for-bit
preservation). The `cleaneddata.Raw()` escape hatch and the legacy
`{Asset,Liability,Earnings}AdjustmentResult` translator stack survive to
Phase 5 (still load-bearing — see §6).

This is the FIRST consumer-visible numeric change since v0.10.0.

## 2. Commit ladder (branch `dc1-phase-4`)

| Cluster | SHA | Scope |
|---|---|---|
| C-1 | `f65604a` | Plumbing: `CleanFinancialDataWithViews` at the cleaner callsite; thread `*cleaneddata.CleanedFinancialData` through `performValuation` + `performAlternativeValuation`. Landed `TestDDM_ConsumerPath_UnaffectedByPhase4`. Zero behavior change. |
| C-2 | `4f8a06c` | ROIC + NWC + `effectiveOI` → `Restated()`; **Graham → `AsReported()` (pulled forward from C-5 — see §7 judgment call)**; delete A2/A4/A5 asset dispatcher dual-writes via the new `applyLedgerComponentDeltas` helper. |
| C-3 | `9bc885e` | NOPAT-fallback guard + negative-OI sentinel + cross-check + router OI + alt-model OI (revenue_multiple/ffo) → `Restated()`; DDM keeps `latestFinancialData` (branch on model type); delete C1/C2/C3/C5/C6 earnings dual-writes. |
| C-4 | `7349a1e` | WACC inputs + EV→Equity bridge → `Restated()`/`InvestedCapital()`; B3 routing flip realized (new `dcf.CalculateEquityValueWithDebtLikeClaims`); delete A1 + B1/B2/B3 dual-writes; `CalculationVersion` 4.2 → 4.3. |
| C-5 | (this commit) | Tangible value → `AsReported()` (see §7 judgment call); `currency.go` annotated (NO migration); vestigial-translator deletion DEFERRED to Phase 5 (still load-bearing); docs sweep; this closeout. DDM (`ddm.go`) confirmed untouched. |

## 3. §8.2.1 Option A mechanism (the load-bearing dispatcher contract change)

The dispatcher dual-writes were NOT simply deleted. They were replaced by a
generic helper `applyLedgerComponentDeltas(ctx, working, out)` at
`internal/services/datacleaner/adjustments/ledger_apply.go`:

```go
func applyLedgerComponentDeltas(ctx context.Context, working *entities.FinancialData, out AdjusterOutput) {
	_ = ctx
	if working == nil {
		return
	}
	for _, e := range out.LedgerEntries {
		if !e.Fired || e.Component == "" {
			continue
		}
		switch e.Component {
		case "OtherIntangibles":
			working.OtherIntangibles += e.DeltaAmount
		case "Inventory":
			working.Inventory += e.DeltaAmount
		case "DeferredTaxAssets":
			working.DeferredTaxAssets += e.DeltaAmount
		case "NormalizedOperatingIncome":
			working.NormalizedOperatingIncome += e.DeltaAmount
		case "InterestExpense":
			working.InterestExpense += e.DeltaAmount
		}
	}
}
```

- Each Restater arm (A2/A4/A5 in `assets.go`; C1/C2/C3/C5/C6 in `earnings.go`)
  replaced its hand-rolled `data.X ±= amount` block with a call to this helper.
- The helper applies the fired LedgerEntry's signed `DeltaAmount` to the named
  COMPONENT field ONLY. It NEVER mutates an umbrella (`TotalAssets`,
  `TotalDebt`) — umbrellas recompute inside `cleaneddata.Restated()` at the
  view level. It skips empty-`Component` entries (OverlayEmitters A1/B1/B2/B3;
  FlagEmitters C4/C7).
- **Why this keeps `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_*`
  GREEN:** the Phase 3 followup `Restated()` reducer seeds from the POST-CLEAN
  entity and applies ONLY `EquityOffset + TaxShieldDTA`. The helper ensures the
  post-clean entity's COMPONENT fields still carry the restater delta (applied
  exactly once — by the helper, not the deleted dual-write, not the reducer),
  so the reducer's seed is correct and no double-count occurs. Option B
  (re-applying `DeltaAmount` in the reducer) was REJECTED — it walks back the
  HIGH-1 fix.
- A1 / B1 / B2 / B3 are OverlayEmitters: their effect lives on the OverlaySpec
  (`data.Overlays`), realized by `InvestedCapital()`. Their dispatcher
  umbrella/debt dual-writes were deleted outright (A1 in `assets.go`; the B-rule
  `dualWrite` closure in `liabilities.go` is now a no-op stub).

**Documented transitional state:** after Phase 4 the post-clean
`*FinancialData`'s UMBRELLA fields (`TotalAssets`, `TotalDebt`) may be
INCOHERENT relative to their components. No Phase 4 consumer reads the umbrellas
off the entity directly — they read views, which recompute. (See §4 KNOWN
DEVIATION for the shadow-snapshot consequence.)

## 4. KNOWN DEVIATION from plan §8.1 — shadow snapshots regenerated (NOT byte-identical)

The plan listed "Shadow snapshots byte-identical (`git diff --quiet` exits 0)"
as a per-commit invariant. **This invariant could NOT be held under Option A
and was intentionally relaxed for the asset-side dual-write deletions (C-2, C-4).**

Root cause: `recompute.go::recomputeUmbrellas` (the shadow shim, UNCHANGED in
Phase 4) reads the post-clean `data.TotalAssets` as its "reported" value and
compares it to `sum(components)`. Before Phase 4 the umbrella dual-write reduced
`data.TotalAssets` after each asset Restater / A1 fire, so "reported" tracked
the reduction. After Phase 4 the umbrella dual-write is deleted (Option A), so
`data.TotalAssets` stays un-reduced — `recomputeUmbrellas` faithfully records a
DIFFERENT (larger or smaller) divergence. This is the documented "umbrella may
be incoherent" outcome, not a behavior bug.

Affected basket tickers (asset-Restater / A1 / B-rule firers): **all 10**
basket tickers — AAPL, AMD, BABA, EQIX, F, JNJ, KO, MSFT, MXL, TSM — saw shadow
regeneration across C-2 (A2/A4/A5) and C-4 (A1 + B1/B2/B3 liability dual-write
deletions). BABA's record changed too: its three `TotalLiabilities` divergence
entries (driven by the deleted B-rule liability-umbrella dual-write) collapsed
to `divergences: []` once the umbrella mutation was removed. C-3 (C-rules,
earnings-only) and C-5 (consumer-only) produced NO shadow change because
`recomputeUmbrellas` only recomputes balance-sheet umbrellas.

All SEMANTIC cleaner invariants stayed GREEN at every commit:
`TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`,
`TestLedger_BasketSnapshot_ClusterPrediction` (10/10),
`TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` (AMD $9.679B / KO
$60.912B). The shadow snapshot is a regenerating capture, not a behavioral
golden; the regenerated content is the truthful record of the new transitional
state. `.gitattributes` was extended to pin `recompute-shadow/*.json` to
`eol=lf` so the committed diff stays content-only (CRLF-noise-free).

**REVIEWER action:** review the regenerated `internal/integration/testdata/recompute-shadow/`
JSON as part of C-2 + C-4 (the divergence-record deltas are the expected
umbrella-incoherence signal).

## 5. Replay drift verification — DEFERRED to operator

Tasks 4.20 (cluster replay verification) + 4.26 (fresh baseline capture) are
DEFERRED. The replay binary RAN against `artifacts/tier2-baseline/2026-05-19/`
(`go run ./cmd/replay --allow-schema-drift --workers=4 ...`), but that baseline
is `calculation_version: "4.1"` — it predates Phases 2, 3, AND 4 plus the
assumption-profile config changes (`config_hash` differs;
`software_like_scaling` fallback → explicit profile matches). The observed drift
is therefore CONFOUNDED across multiple phases and the profile-config change; it
cannot be cleanly attributed to Phase 4's B3 routing flip alone. Example: AAPL
(a Phase-4 Class-I zero-drift ticker — no B-rule fires) shows
`dcf_value_per_share 19.09 → 20.48` driven by the profile/horizon change between
4.1 and 4.3, NOT by Phase 4.

No UNEXPECTED drift PATTERN emerged (no ticker drifted in a direction spec §5
forbids; the diffs are per-share-value / WACC / version-field shifts consistent
with the §5 drift classes). But a clean per-cluster Phase-4 attribution requires
a fresh `4.2`/config-current baseline, which needs live SEC/market capture
(cache-bypass) NOT available in this environment.

**Operator follow-up (Phase 4 → Phase 5 gate items 4 + 5):**
1. Capture a fresh `artifacts/tier2-baseline/<date>/` at master's pre-Phase-4
   tip (`9d745a9`, calc_version 4.2) for the 10-ticker basket (TSM needs the
   IFRS-FPI FX path live — it ERRORED in the hermetic replay).
2. Replay the Phase-4 branch tip against that fresh baseline and confirm:
   Class I tickers byte-identical except `calculation_version`; Class III
   (B-rule firers) show the expected WACC + equity-bridge drift from the routing
   flip; Class IV (A1 firers) show the cross-check ratio shift.
3. Refresh `artifacts/tier2-baseline/` with the post-Phase-4 bundles at ship sha.

## 6. Vestigial-translator + `Raw()` deletion status — DEFERRED to Phase 5

- **Per-rule translators (`aXAdjusterOutputToLegacyResult`, etc.) + the
  adjustments-package `{Asset,Liability,Earnings}AdjustmentResult` structs:
  NOT deleted.** Grep evidence: the cleaner orchestrator
  (`service.go::applyActiveAdjustments` lines 523/555/594) still reads
  `assetResult.Applied / .Adjustments / .Flags` (and liability/earnings
  equivalents) to aggregate flags + adjustments for quality scoring. The
  dispatcher arms still call the translators to populate `result.Applied`. They
  are LOAD-BEARING, not vestigial — spec §4.4's "IF AND ONLY IF grep confirms no
  remaining callers" condition is NOT met. Deletion DEFERRED to Phase 5 (gotcha
  #9 honored). Note: `entities.AssetAdjustmentResult` /
  `entities.LiabilityAdjustmentResult` in `core/entities/data_cleaning.go` are
  separate, also-live types.
- **`cleaneddata.CleanedFinancialData.Raw()`: NOT deleted (Phase 5 owns it).**
  Grep confirms ZERO production/consumer `internal/` callers — the only
  reference is `service_cleanwithviews_test.go:45` (a contract test). The
  Phase 4 acceptance criterion (no dangling `Raw()` consumer) is satisfied.
- **Dormant legacy-fallback umbrella mutations in `earnings.go`
  (`ProcessRestructuringChargesAdjustment` / `ProcessAssetSaleGainsAdjustment` /
  `ProcessLitigationSettlementsAdjustment` / capitalized-interest, lines
  ~1611/1653/1713/1859): NOT deleted (Phase-5 cleanup).** These still perform a
  direct `data.NormalizedOperatingIncome ±= X` (and `data.InterestExpense += X`)
  mutation, but every dispatcher arm now calls them ONLY on the
  `if err != nil { result = ea.ProcessX...; break }` fallback branch — i.e. the
  never-fired error path of the corresponding `ApplyCx*` method (the Apply*
  methods are pure component-delta computations that do not return errors in
  practice). REVIEWER agreed leaving them is acceptable: they are unreachable in
  the steady state and deleting them now would entangle the still-load-bearing
  legacy `*AdjustmentResult` translator chain (above). Phase 5 deletes them
  together with the vestigial translators once the legacy result structs are
  retired.

## 7. Judgment calls / spec deviations

1. **Graham migrated in C-2, not C-5.** The spec maps Graham to cluster C-5, but
   `graham.go` reads `data.TotalAssets` / `data.TotalLiabilities` DIRECTLY off
   the entity. Once C-2 deletes the A2/A4/A5 umbrella dual-writes (making
   `data.TotalAssets` incoherent), an un-migrated Graham reader would surface the
   incoherent umbrella to its derivation fallback (AMD/KO `TotalLiabilities==0`
   path) → unexpected drift on NCAV/Graham fields BEFORE C-5. Migrating Graham to
   `AsReported()` in C-2 (the first cluster that makes umbrellas incoherent)
   eliminates the co-mutation hazard — `AsReported()` reads the pre-clean
   snapshot, immune to umbrella incoherence regardless of cluster order. Graham
   stays on `AsReported()` per spec §4.2.9 (NCAV is a conservative as-filed
   metric).
2. **Tangible value reads `AsReported()`, not `Restated()` (spec §4.2.12 named
   Restated).** Spec §4.2.12's premise — "Restated().TangibleAssets ==
   AsReported().TangibleAssets for every current ticker" — is INCORRECT:
   `cleaneddata.Restated()` RECOMPUTES `TangibleAssets` as
   `(TotalAssets − Goodwill − OtherIntangibles)` from components, which is NOT
   bit-for-bit equal to the parser-stamped `TangibleAssets`. Using `Restated()`
   would introduce consumer-visible drift on `tangible_value_per_share`,
   violating the spec's own "zero numeric drift today" guarantee AND the
   load-bearing `TestService_calculateTangibleValuePerShare_DilutedDenominator`
   regression pin. `AsReported()` is identity-copied (parser value verbatim) and
   satisfies the spec's INTENT. No Restater touches `TangibleAssets` today, so
   the economic value agrees either way. Revisit if a future Restater touches
   intangibles (Phase 5+).
3. **A4's `data.ValuationAllowance` dual-write deleted (not just the
   `TotalAssets` umbrella).** `ValuationAllowance` is neither a view field nor
   read by any Phase 4 consumer; per Option A's "apply component delta only,"
   the whole A4 `if result.Applied {...}` block was deleted. The audit trail is
   preserved via the translated `*AdjustmentResult` + native LedgerEntry. The A4
   emission test was updated to `data.ValuationAllowance == 0`.
4. **NWC reads `AsReported()`, not `Restated()` (REVIEWER-HIGH followup).** The
   C-2 commit initially migrated `calculateNetWorkingCapitalChange` to
   `Restated().CurrentAssets/.CurrentLiabilities`. But those are RECOMPUTED-
   umbrella fields (`restate.go:68` rebuilds CA as
   `Cash + Inventory + OtherCurrentAssets`), so for any ticker whose Phase-0 plug
   UNDER-reconstructs the stamped umbrella — AMD: stamped 16,505M vs recomputed
   14,678M, delta −1,827M, with ZERO Restaters firing; the per-period shortfall
   grows so it does NOT cancel in the latest−prior delta — the Restated read
   drifted `NetWorkingCapitalChange → FCF → dcf_value_per_share`, violating the
   Class II zero-drift expectation (spec §5.1/§5.2). This is the SAME recomputed-
   umbrella root cause already handled for `TangibleAssets` (judgment call #2);
   NWC was the one read that slipped through. The followup commit flips BOTH the
   latest and prior reads to `AsReported()` (identity-copied stamped umbrellas) ⇒
   bit-for-bit identical to pre-Phase-4. Pinned by
   `TestPerformValuation_NWCChangeUsesAsReported` (AMD-class fixture where
   `sum(components)+plug ≠ stamped umbrella`). A FUTURE deliberate decision to
   have NWC reflect current-asset Restaters (e.g. an A5 inventory writedown) may
   flip `AsReported → Restated` WITH a documented drift expectation; Phase 4's
   principle is "only the intended B3 routing flip drifts."
5. **`CashAndCashEquivalents` stays on the entity read (not migrated).**
   `FinancialDataView` has no `CashAndCashEquivalents` field and Cash is never
   Restater-touched, so the alt-model `ModelInput.CashAndCashEquivalents` + the
   EV-bridge cash term read `latestFinancialData.CashAndCashEquivalents`
   directly. Spec §4.2.5/§4.2.13's "restated.CashAndCashEquivalents" is moot.

## 8. DDM migration deferred to Phase 5

`ddm.go` is UNTOUCHED. The 5 read sites (lines 178/181/213/291/292) continue to
read `latest.StockholdersEquity` / `.NetIncome` / `.DividendsPerShare` via
`GetLatestPeriod()`. The alt-model `ModelInput` keeps
`latestFinancialData.InterestBearingDebt` for DDM (branch on
`model.ModelType() == "ddm"`), and `LatestRestatedView` is nil for DDM. The
JPM/BAC/WFC bit-for-bit invariant (`TestDDM_LegacyPath_BitForBit`) stayed GREEN
at every Phase 4 commit, and the new `TestDDM_ConsumerPath_UnaffectedByPhase4`
(superset pin — output bits + the `GetLatestPeriod()` input fields) is GREEN at
every commit.

**Phase 5 DDM migration steps (per spec §7.3 / §9.4):**
1. Re-run shadow snapshots on JPM/BAC/WFC; verify `Restated().X ==
   AsReported().X` for `StockholdersEquity`, `NetIncome`, `DividendsPerShare`.
   NOTE the Phase 4 finding: `Restated()` RECOMPUTES `TangibleAssets` and the
   balance-sheet umbrellas from components — Phase 5 must verify that for
   JPM/BAC/WFC the recomputed `StockholdersEquity` (NOT recomputed — it is
   identity-copied + EquityOffset; safe) and the DDM-consumed fields are
   bit-for-bit, OR migrate only the identity-copied fields.
2. Add a temporary parallel-write test `TestDDM_RestatedView_BitForBit` against
   the same goldens. Verify GREEN.
3. ONLY THEN migrate `ddm.go` reads to `Restated()` (DDM gains a
   `*CleanedFinancialData` / `LatestRestatedView` parameter via `ModelInput`).
4. Delete the temporary parallel-write test; `TestDDM_LegacyPath_BitForBit`
   continues to guard the math.

## 9. New tests (spec §8.3)

| Test | File | Cluster |
|---|---|---|
| `TestDDM_ConsumerPath_UnaffectedByPhase4` | `models/ddm_phase4_invariance_test.go` | C-1 |
| `TestPerformValuation_RestatedReadsAtROIC` | `valuation/phase4_consumer_migration_test.go` | C-2 |
| `TestPerformValuation_NWCChangeUsesRestated` | same | C-2 |
| `TestEffectiveOI_ReadsView` | same | C-2 |
| `TestDispatcherDualWriteDeleted_Assets` | `adjustments/dispatcher_dualwrite_deleted_test.go` | C-2 |
| `TestPerformValuation_CrossCheckReadsRestated` | `valuation/phase4_consumer_migration_test.go` | C-3 |
| `TestDispatcherDualWriteDeleted_Earnings` | `adjustments/dispatcher_dualwrite_deleted_test.go` | C-3 |
| `TestPerformValuation_WACCUnaffectedByB3` (defining B3 pin) | `valuation/phase4_consumer_migration_test.go` | C-4 |
| `TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims` | same | C-4 |
| `TestDispatcherDualWriteDeleted_Liabilities` | `adjustments/dispatcher_dualwrite_deleted_test.go` | C-4 |
| `TestCalculationVersion_IsV43` | `valuation/phase4_consumer_migration_test.go` | C-4 |
| `TestCalculateEquityValueWithDebtLikeClaims` | `pkg/finance/dcf/dcf_test.go` | C-4 |
| `TestPerformValuation_GrahamUsesAsReported` | `valuation/phase4_consumer_migration_test.go` | C-2/C-5 |
| `TestCalculateTangibleValuePerShare_UsesView` | same | C-5 |

Existing dispatcher emission tests (A1/A2/A4/A5/B1/B2/B3 + ActiveWorkflow +
baseline B3 + integration debt + the 5 `CalculationVersion` assertions) were
updated to the Option A + routing-flip contract.

## 10. NON-goals honored

| NON-goal | Status |
|---|---|
| No `Raw()` deletion | HONORED (Phase 5; zero consumer callers verified) |
| No new view types | HONORED |
| No `sync.Once` retrofit | HONORED |
| No new B-rule overlay types | HONORED |
| No SEC parser fix for T2-BS-3 | HONORED |
| No `CleaningResult` SQLite schema migration | HONORED |
| No batch / parallel-fan-out consumer | HONORED |
| No DDM math change | HONORED (ddm.go untouched; bit-for-bit GREEN) |
| No `WACCInputs` struct extraction | HONORED (inlined `cleaned` reads) |
| No `SchemaVersion` bump | HONORED (`FinancialData` stays 9; `ValuationResult` stays 2) |

## 11. Phase 4 → Phase 5 gate status

| Gate item | Status |
|---|---|
| 1. All Phase 4 invariants GREEN (§8.1, except shadow-byte-identical — see §4) | DONE (shadow relaxed-by-design + documented) |
| 2. `WACCUnaffectedByB3` + `EquityBridgeSubtractsDebtLikeClaims` GREEN | DONE |
| 3. `TestDDM_LegacyPath_BitForBit` + `TestDDM_ConsumerPath_UnaffectedByPhase4` GREEN | DONE |
| 4. Replay diff matches §5 per-ticker | DEFERRED to operator (§5) — stale baseline |
| 5. `artifacts/tier2-baseline/` refreshed | DEFERRED to operator (needs live capture) |
| 6. Closeout doc filed | DONE (this file) |
| HUMAN signoff + merge | PENDING |

Phase 5 scope: DDM consumer migration; `Raw()` deletion; optional `sync.Once`;
optional stop-populating the legacy `historicalData` slot; legacy
`*AdjustmentResult` translator/struct deletion.
