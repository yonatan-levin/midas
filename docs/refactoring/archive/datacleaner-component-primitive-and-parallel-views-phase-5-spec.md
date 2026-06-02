# Datacleaner — Phase 5 Spec (DDM Migration + DebtLikeClaims Correction + Legacy Translator/Struct Retirement + Cleanup)

**Status:** SHIPPED — PARTIAL merged `e816fcc`; follow-up (P5-C3-full Adjustments-projection + P5-C4 translator/dead-helper deletion + DDM `modelIBD` view flip) merged `8ca0841` (2026-06-02). Closes DC-1. Closeouts: [phase-5-closeout](../implementations/datacleaner-component-primitive-and-parallel-views-phase-5-closeout.md) + [follow-up closeout](../implementations/dc1-phase-5-followup-closeout.md). ARCH `Adjustment.Percentage` decision: [dc1-phase-5-followup-percentage-decision.md](dc1-phase-5-followup-percentage-decision.md).
**Phase:** Phase 5 of the DC-1 refactor sequence (FINAL phase — closes DC-1)
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-spec.md](datacleaner-component-primitive-and-parallel-views-spec.md) — §"Phasing & implementation sequence" row "Phase 5 — DDM migration + `Raw()` cleanup + vestigial deletion" + §"Consumer migration map" (DDM rows)
**Phase 4 spec:** [datacleaner-component-primitive-and-parallel-views-phase-4-spec.md](datacleaner-component-primitive-and-parallel-views-phase-4-spec.md) — §7 (DDM bit-for-bit preservation), §9 (DDM migration sub-plan §9.4), §4.2.11 (DDM read sites)
**Phase 4 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md](../implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md) — §6 (translator + `Raw()` deferral, grep evidence), §7 (judgment calls #2/#4/#6), §8 (DDM 4-step sub-plan), §11 (Phase 4 → 5 gate)
**Implementer plan:** [datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md](../implementations/datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md)
**Tracker:** [docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md](../../reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md)
**Estimated effort:** 9–14 agent-hours (single PR with 5 commit clusters — see §11 PR strategy). If the DebtLikeClaims correction (§3.2) is split out as a Phase 4.x hotfix, that hotfix is ~2–3 agent-hours and Phase 5 proper is ~7–11 agent-hours.

---

## 1. Phase context

Phase 4 merged to master (post-`9d745a9`; this spec is authored against the just-merged tree at `ce94f70`). It migrated 12 of the 13 valuation consumer read sites onto the `cleaneddata` view accessors (`AsReported()` / `Restated()` / `InvestedCapital()`), realized the B3 routing flip (contingent liabilities → `InvestedCapital().DebtLikeClaims`, out of the WACC capital-structure denominator), deleted every cleaner dispatcher dual-write via the `applyLedgerComponentDeltas` helper (Phase 4 §8.2.1 Option A), and bumped `CalculationVersion` 4.2 → 4.3.

Phase 4 **deliberately deferred** five items to Phase 5 (Phase 4 closeout §6 + §8 + §11):

| Deferred item (Phase 4) | → Phase 5 goal |
|---|---|
| DDM consumer migration — `ddm.go` still reads `latest.{StockholdersEquity,NetIncome,DividendsPerShare}` via `GetLatestPeriod()` (bit-for-bit risk) | Migrate DDM reads to `Restated()` via a new `ModelInput.LatestRestatedView` populated for DDM, preserving `TestDDM_LegacyPath_BitForBit` through a 4-step re-proof |
| `cleaneddata.CleanedFinancialData.Raw()` carries `TODO(phase-5)`; zero production callers | Delete `Raw()` + its marker + the contract-test reference |
| Per-rule translators + adjustments-package `{Asset,Liability,Earnings}AdjustmentResult` structs STILL load-bearing (orchestrator reads `result.Applied/.Adjustments/.Flags`) | Migrate the orchestrator's flag/adjustment aggregation onto the native `AdjustmentLedger`/`Overlays`/`Flags` slices, THEN delete the translators + the local result structs |
| Dormant legacy-fallback umbrella mutations in `earnings.go` (never-fired `Apply* err != nil` branch) | Delete alongside the translator retirement |
| `historicalData.Data[latestPeriod] = cleaningResult.CleanedData` legacy slot still populated (DDM reads it via `GetLatestPeriod()`) | (Optional) Stop populating it once DDM no longer reads it |

Phase 5 ALSO surfaces a **newly-identified latent regression** discovered while reviewing the Phase 4 closeout (§7 #6, the `revenue_multiple` DebtLikeClaims finding): **DDM's EV↔equity bridge silently drops B-rule DebtLikeClaims for B-rule-firing banks** (§3.2 below). This is the DDM analog of the `revenue_multiple` finding and is the most consequential *correctness* change in Phase 5.

**Phase 5 is the SECOND consumer-visible numeric change since v0.10.0** (Phase 4 was the first). The drift is confined to (a) DDM `EnterpriseValue` for B-rule-firing banks (§3.2 correction) — the DDM `IntrinsicValuePerShare` and `EquityValue` do NOT change because DDM derives equity from dividends, not from EV; and (b) any DDM read that flips to `Restated()` for a Restater-firing financial (none in the current basket). Phase 5 closes DC-1.

---

## 2. Goals (in priority order)

1. **Migrate DDM consumer reads** (`ddm.go:178/181/213/291/292`) from `latest.{StockholdersEquity,NetIncome,DividendsPerShare}` (via `GetLatestPeriod()`) to the `Restated()` view, threaded through a now-populated-for-DDM `ModelInput.LatestRestatedView`. **Preserve `TestDDM_LegacyPath_BitForBit`** (JPM/BAC/WFC `math.Float64bits` equality) at every commit via the 4-step re-proof sub-plan (§7).
2. **Correct the DDM EV↔equity bridge to ADD `DebtLikeClaims`** (the DDM analog of the Phase 4 `revenue_multiple` finding — closeout §7 #6). Thread `ModelInput.DebtLikeClaims` into DDM's bridge so B1/B2/B3 claims are not silently dropped for B-rule-firing banks: `EnterpriseValue = EquityValue + InterestBearingDebt + DebtLikeClaims − Cash`. Note the SIGN: DDM ADDS DebtLikeClaims (the opposite of DCF and `revenue_multiple`, which subtract); see §3.2 for the EV-from-equity rationale. Prove invariant-safe (DebtLikeClaims=0 for JPM/BAC/WFC fixtures → bit-for-bit unchanged).
3. **Retire the legacy translator stack**: migrate the cleaner orchestrator's flag/adjustment aggregation (`service.go::applyActiveAdjustments` reads of `result.Applied/.Adjustments/.Flags`) onto the native `AdjustmentLedger`/`Overlays`/`Flags` slices, THEN delete the per-rule `*AdjusterOutputToLegacyResult` translators and the adjustments-package `{Asset,Liability,Earnings}AdjustmentResult` structs. Sequence consumer-migration-before-deletion.
4. **Delete the dormant legacy-fallback umbrella mutations** in `earnings.go` (`ProcessRestructuringChargesAdjustment` / `ProcessAssetSaleGainsAdjustment` / `ProcessLitigationSettlementsAdjustment` / capitalized-interest helper) — reachable only on the never-fired `Apply* err != nil` branch.
5. **Delete `cleaneddata.CleanedFinancialData.Raw()`** + its `TODO(phase-5)` marker + the contract-test reference.
6. **Delete the dead `entities.{Asset,Liability}AdjustmentResult` duplicate structs** in `core/entities/data_cleaning.go` (zero code callers — grep-confirmed; see §4.5).
7. **(Optional, gated on goal 1) Stop populating the legacy `historicalData.Data[latestPeriod]` slot** once DDM no longer reads it via `GetLatestPeriod()`.
8. **(Optional) `cleaneddata` accessor concurrency hardening** — either retrofit `sync.Once` on the three accessors OR formalize the immutable/request-local contract (§3.6 recommends the latter).
9. **Bump `CalculationVersion` 4.3 → 4.4** atomic with the first commit that produces consumer-visible numeric drift (the §3.2 DDM DebtLikeClaims correction). See §6.

---

## 3. Architecture

### 3.1 Migration strategy

**Single PR, five commit clusters.** Each cluster is one atomic commit so the load-bearing invariants stay GREEN at every commit boundary. The clusters are ordered so the riskiest, most load-bearing change (DDM bit-for-bit) is isolated early, and the translator retirement correctly sequences consumer-migration BEFORE deletion.

| Cluster | Scope | `CalculationVersion` | Risk |
|---|---|---|---|
| **P5-C1: DDM DebtLikeClaims correction** | Thread `ModelInput.DebtLikeClaims` into DDM's EV bridge (ADD it — opposite sign vs DCF/revenue_multiple; see §3.2); populate `modelDebtLikeClaims` for DDM in `service.go::performAlternativeValuation`. `ddm.go` reads STILL via `GetLatestPeriod()` (no view migration yet). Bump 4.3 → 4.4. | 4.3 → **4.4** | **HIGH** — touches `EnterpriseValue` on the bit-for-bit path; safe only because DebtLikeClaims=0 for JPM/BAC/WFC |
| **P5-C2: DDM view migration** | Populate `ModelInput.LatestRestatedView` for DDM (currently nil-for-DDM); migrate `ddm.go:178/181/213/291/292` reads from `latest.X` → the restated view. 4-step re-proof (§7). | 4.4 | **HIGHEST** — the load-bearing centerpiece; `TestDDM_LegacyPath_BitForBit` must stay byte-identical |
| **P5-C3: Orchestrator native-slice migration** | Rewrite `service.go::applyActiveAdjustments` flag/adjustment aggregation to read `data.AdjustmentLedger`/`data.Overlays`/the native `Flags` rather than `result.Applied/.Adjustments/.Flags`. NO struct deletion yet. | 4.4 | MEDIUM — `result.Adjustments`/`result.Flags` are public `CleaningResult` fields surfaced to audit/quality scoring; mapping must be behavior-preserving |
| **P5-C4: Translator + struct + dormant-fallback deletion** | Delete the per-rule `*AdjusterOutputToLegacyResult` translators; delete the adjustments-package `{Asset,Liability,Earnings}AdjustmentResult` structs; delete the dormant `earnings.go` legacy-fallback `Process*Adjustment` umbrella mutations; delete the dead `entities.{Asset,Liability}AdjustmentResult` structs. | 4.4 | MEDIUM — large mechanical deletion; depends on P5-C3 landing first |
| **P5-C5: `Raw()` deletion + optional cleanup + closeout** | Delete `cleaneddata.Raw()` + marker + contract-test ref; (optional) stop populating the legacy `historicalData` slot; (optional) accessor concurrency contract formalization; docs sweep + closeout. | 4.4 | LOW |

The cluster ordering isolates the two HIGH-risk DDM changes (P5-C1, P5-C2) as the first two commits so a bisect over a bit-for-bit regression lands on a tiny, well-understood diff. The translator retirement (P5-C3 → P5-C4) is split into consumer-migration-THEN-deletion so REVIEWER sees the behavior-preserving rewrite separately from the mechanical deletion.

**Alternative considered — fold P5-C1 into P5-C2.** Rejected: the DebtLikeClaims correction (§3.2) is a *numeric* change (EnterpriseValue drift on B-rule banks) while the view migration (P5-C2) is a *coherence/forward-compat* change (zero numeric drift today). Keeping them as separate commits gives a clean attribution: any EnterpriseValue drift bisects to P5-C1; any IntrinsicValue/EquityValue drift bisects to P5-C2 (and must be ZERO).

### 3.2 DDM DebtLikeClaims correction (NEW — the DDM analog of the Phase 4 `revenue_multiple` finding)

**The finding.** Phase 4 C-4 deleted the B1/B2/B3 dispatcher dual-writes. Pre-Phase-4, those dual-writes folded B-rule claim amounts into `data.TotalDebt` / `data.InterestBearingDebt`. Post-Phase-4, `latestFinancialData.InterestBearingDebt` is **B-rule-free for ALL tickers including DDM** — the B-rule amounts live ONLY in `InvestedCapital().DebtLikeClaims`. But DDM's EV↔equity bridge still reads `input.InterestBearingDebt` directly:

```go
// ddm.go:127 (legacy Gordon) and ddm.go:399 (multi-stage), today:
enterpriseValue := equityValue + input.InterestBearingDebt - input.CashAndCashEquivalents
```

and `service.go::performAlternativeValuation` sets `modelDebtLikeClaims = 0` and `modelIBD = latestFinancialData.InterestBearingDebt` for DDM (the `model.ModelType() != "ddm"` guard skips the restated/DebtLikeClaims plumbing). So for a **B-rule-firing bank** (banks commonly fire B1 operating-lease capitalization, B2 pension underfunding, and B3 litigation/contingency reserves), DDM's `EnterpriseValue` now **silently omits** those debt-like claims → `EnterpriseValue` is understated relative to the economically-correct EV (which should include all debt-like claims competing with equity). This is the **same class of bug** the gpt-5.5 cross-model review caught for `revenue_multiple` (Phase 4 closeout §7 #6, commit `2ea9978`).

**Why it passed `TestDDM_LegacyPath_BitForBit`.** The JPM/BAC/WFC golden fixtures fire NO B-rules (DebtLikeClaims=0), so the omitted term is zero → `EnterpriseValue` is unchanged. The bug is latent: it only manifests for live B-rule-firing banks that are NOT in the bit-for-bit fixture set.

**The crucial DDM-specific nuance (NOT present in `revenue_multiple`).** DDM derives equity from dividends FIRST (`equityValue = valuePerShare × shares`), then derives EV from equity (`EV = equity + debt − cash`). Therefore:

- DDM's `IntrinsicValuePerShare` and `EquityValue` are **NOT a function of `InterestBearingDebt` or `DebtLikeClaims`** — they come purely from the dividend stream and cost of equity.
- ONLY DDM's `EnterpriseValue` (a *reported/derived* convenience field) depends on the debt terms.

This is the OPPOSITE direction from `revenue_multiple` (where EV is computed first and equity is derived by SUBTRACTING debt + claims). For DDM the bridge is `EV = equity + debt − cash`, so debt-like claims must be **ADDED** to derive the correct EV:

```go
// Phase 5 P5-C1 — corrected DDM bridge (both legacy + multi-stage paths):
enterpriseValue := equityValue + input.InterestBearingDebt + input.DebtLikeClaims - input.CashAndCashEquivalents
```

**Invariant-safety proof.** For JPM/BAC/WFC the fixtures fire no B-rules ⇒ `input.DebtLikeClaims == 0` ⇒ `enterpriseValue` term `+ 0` ⇒ `EnterpriseValue` byte-identical ⇒ `TestDDM_LegacyPath_BitForBit` and `TestDDM_ConsumerPath_UnaffectedByPhase4` (which both pin `EnterpriseValue` bits) stay GREEN. The correction is drift-free for the pinned tickers and ONLY changes EV for live B-rule-firing banks — the intended accuracy correction.

**Plumbing required (P5-C1).** In `service.go::performAlternativeValuation`, populate `modelDebtLikeClaims` for DDM as well (remove the `model.ModelType() != "ddm"` exclusion for the DebtLikeClaims term — but keep DDM on the *legacy `latestFinancialData.InterestBearingDebt` read* for the IBD term until P5-C2, since IBD is not Restater-touched and migrating it is orthogonal):

```go
// P5-C1: DDM gains DebtLikeClaims (EV-bridge correction); IBD stays legacy until P5-C2.
modelDebtLikeClaims := investedCapitalOr(cleaned, latestFinancialData).DebtLikeClaims  // now for ALL models
modelIBD := latestFinancialData.InterestBearingDebt
if model.ModelType() != "ddm" {
    modelIBD = restatedViewOr(cleaned, latestFinancialData).InterestBearingDebt
}
```

> **NOTE on `restated.InterestBearingDebt` vs `latest.InterestBearingDebt` for DDM.** They are equal today (no Restater touches `InterestBearingDebt`; B-rules are OverlayEmitters that feed `DebtLikeClaims`, NOT Restaters that touch the component). P5-C2 may optionally flip DDM's IBD read to `restated.InterestBearingDebt` for coherence; doing so is bit-for-bit safe by the same DebtLikeClaims=0 argument. P5-C1 keeps it legacy to minimize the bit-for-bit surface in the EV-correction commit.

#### 3.2.1 RECOMMENDATION: Phase 4.x hotfix vs bundle into Phase 5

**ARCH recommends: BUNDLE into Phase 5 as commit P5-C1 (do NOT cut a separate Phase 4.x hotfix).** Reasoning:

1. **Severity is bounded and non-corrupting.** The bug understates DDM `EnterpriseValue` for B-rule-firing banks. But DDM's headline output — `IntrinsicValuePerShare` and `EquityValue`, the values the fair-value endpoint surfaces and that users act on — are **unaffected** (DDM equity is dividend-derived, not EV-derived). The drift is confined to the *derived* `EnterpriseValue` convenience field. Contrast with the `revenue_multiple` finding, which corrupted the *headline* per-share equity value — that one genuinely warranted the immediate `2ea9978` fix inside the Phase 4 window. The DDM analog does not rise to "live headline corruption."
2. **No partial-merge exposure.** Phase 4 is already merged with `CalculationVersion 4.3`. A Phase 4.x hotfix would require its own `4.3.1`/`4.4` decision, its own baseline refresh, and its own replay-attribution pass — duplicating exactly the gate work Phase 5 does anyway. Bundling avoids two CalculationVersion churns within days.
3. **It is the natural lead commit for Phase 5.** P5-C1 is small (two bridge lines + one `service.go` plumbing change + one test), isolated, and shares the DDM bit-for-bit verification machinery with P5-C2. Sequencing it first inside Phase 5 gives the same fast-bisect benefit a standalone hotfix would, without the merge/version overhead.
4. **The fix is invariant-safe and low-risk** (DebtLikeClaims=0 for pinned tickers), so there is no urgency argument that the broader Phase 5 work would jeopardize it.

If HUMAN judges the latent EV understatement material enough to ship ahead of the rest of Phase 5 (e.g., an operator audits DDM `enterprise_value` for a B-rule-firing bank and the number must be correct *now*), then cut P5-C1 as a standalone `dc1-phase-4.1-ddm-debtlikeclaims` PR bumping `CalculationVersion` 4.3 → 4.4, and Phase 5 proper starts at P5-C2 (no further version bump). The spec is structured so P5-C1 is cleanly separable either way.

### 3.3 DDM view migration (P5-C2) — the load-bearing centerpiece

DDM's `ddm.go` reads three entity fields via `input.HistoricalData.GetLatestPeriod()`:

| Read site | Field | View-field classification |
|---|---|---|
| `ddm.go:178` | `latest.StockholdersEquity > 0` (hasROE guard) | **carried + EquityOffset** (NOT recomputed-umbrella) |
| `ddm.go:181` | `latest.NetIncome / latest.StockholdersEquity` (ROE) | NetIncome = **carried**; SE = carried + EquityOffset |
| `ddm.go:213` | `latest.StockholdersEquity / shares` (book value/share) | carried + EquityOffset |
| `ddm.go:291` | `latest.StockholdersEquity > 0 && latest.NetIncome > 0` (sustainable-growth guard, in `estimateDividendGrowth`) | carried + EquityOffset / carried |
| `ddm.go:292` | `latest.NetIncome / latest.StockholdersEquity` (ROE for retention) | carried / carried + EquityOffset |
| `ddm.go:95,264,269,295,298` | `latest.DividendsPerShare` | **carried** (identity-copied; no Restater/Overlay touches DPS) |

**KEY SAFETY ANALYSIS — why DDM migration to `Restated()` is bit-for-bit safe for JPM/BAC/WFC:**

The DDM-consumed fields are **NOT recomputed-umbrella fields**:

- `StockholdersEquity` in `Restated()` = `identityCopy(post-clean).StockholdersEquity + Σ(fired LedgerEntry.EquityOffset)` (`restate.go:62`). When NO Restater fires (JPM/BAC/WFC fixtures have empty `recent_adjusters`), the sum is zero ⇒ `Restated().StockholdersEquity == AsReported().StockholdersEquity == latest.StockholdersEquity`.
- `NetIncome` and `DividendsPerShare` are **pure identity-copied** in `Restated()` — `restate.go` never touches them ⇒ always equal to `latest.X`.

Contrast with the **recomputed-umbrella fields** (`CurrentAssets`, `CurrentLiabilities`, `TotalAssets`, `TotalLiabilities`, `TangibleAssets`) which `restate.go:72-83` rebuilds as `sum(components) + plug`. Phase 4 had to keep `tangible_value_per_share` (closeout §7 #2) and `NetWorkingCapitalChange` (closeout §7 #4) on `AsReported()` precisely because those recomputed umbrellas drift from the parser-stamped value on plug-shortfall tickers (AMD: stamped 16,505M vs recomputed 14,678M). **DDM must NOT migrate any recomputed-umbrella read.** DDM reads none — all five DDM read sites are `StockholdersEquity` / `NetIncome` / `DividendsPerShare`, none of which `restate.go` recomputes. This is the architectural reason DDM migration is *safe in principle*, not just *empirically zero-drift on today's fixtures*.

**The migration mechanics (P5-C2):**

1. Populate `ModelInput.LatestRestatedView` for DDM (remove the nil-for-DDM branch in `service.go:1566-1571`).
2. Change `ddm.go`'s helper signatures so the diagnostics + growth helpers read the restated view's `StockholdersEquity`/`NetIncome` instead of `latest.X`. `DividendsPerShare` may also flip to the view for uniformity (identity-copied, zero drift). `latest *entities.FinancialData` is still needed for the nil-guard plumbing and any non-view field; the view supplies the three migrated fields.

> **Path-discipline caveat (CRITICAL — CLAUDE.md DDM gotcha).** `calculateLegacyGordon`'s body is BYTE-IDENTICAL to the pre-Tier-2 `Calculate` body. The migrated reads land inside `runDividendDiagnostics` (shared by both paths) and `estimateDividendGrowth`. The bit-for-bit test pins `Warnings` (content + order), `Confidence`, AND the three floats. Migration MUST preserve the exact arithmetic: `restatedView.NetIncome / restatedView.StockholdersEquity` must produce the same `float64` bits as `latest.NetIncome / latest.StockholdersEquity` when the view equals the entity. Since `Restated()` identity-copies these fields when no Restater fires, the bits are identical. **No reordering, no intermediate-variable-introduced rounding, no helper extraction in the legacy path** — the §7 re-proof sub-plan enforces this.

### 3.4 Legacy translator + struct retirement (P5-C3 → P5-C4)

Today the cleaner orchestrator (`internal/services/datacleaner/service.go::applyActiveAdjustments`, lines ~522-597) reads the per-category result structs:

```go
assetResult := s.assetAdjuster.ProcessAssetAdjustments(ctx, data, assetRules, cleaningCtx)
if assetResult.Applied {
    allAdjustments = append(allAdjustments, assetResult.Adjustments...)
    allFlags = append(allFlags, assetResult.Flags...)
    totalRulesApplied += len(assetRules)
}
// (and the native drain of assetResult.NativeLedgerEntries / .NativeOverlays)
```

`allAdjustments` (`[]entities.Adjustment`) and `allFlags` (`[]entities.Flag`) become `result.Adjustments` / `result.Flags` on `CleaningResult` (`service.go:217-218`), which feed quality scoring (`calculateQualityScore`) + the audit trail + (potentially) the persisted `valuation_results` snapshot. The dispatcher arms populate `result.Adjustments` via the per-rule `*AdjusterOutputToLegacyResult` translators (Phase 4 closeout §6: these are LOAD-BEARING, not vestigial).

**P5-C3 — consumer migration (behavior-preserving).** Rewrite the orchestrator aggregation to derive flags/adjustments from the native slices instead of the legacy result structs:

- **Flags** already flow natively: the `Apply*` methods emit `AdjusterOutput.Flags`, drained into the result's native flag accounting. P5-C3 reads the native flag stream (the same `[]entities.Flag` content the FlagEmitter convention produces) rather than `result.Flags`. Where the legacy translator and the native flag drain produce the SAME flags (they must — both derive from the same `Apply*` output), this is a no-op on content.
- **Adjustments** (`[]entities.Adjustment`, the audit-trail records): derive from `data.AdjustmentLedger` (the native `[]entities.LedgerEntry`). This requires a small, behavior-preserving projection `LedgerEntry → entities.Adjustment` for the audit-trail/quality-scoring consumers, OR — preferred — assess whether `calculateQualityScore` + the audit trail actually need the `Adjustment` shape at all, vs. a count/flag summary. The implementer plan (Task P5-C3) enumerates the exact `result.Adjustments` consumers and chooses the minimal projection.
- **`totalRulesApplied`** stays as `len(rules)` per category (independent of the result struct's `.Applied` bool — replace `if result.Applied` with `if len(category.NativeLedgerEntries) > 0 || len(category.Flags) > 0`, or the equivalent native firing signal).

**The behavior-preservation gate (P5-C3):** `result.Adjustments` and `result.Flags` content + ordering must be byte-identical before/after the rewrite for the 10-ticker basket. Pinned by a new `TestApplyActiveAdjustments_NativeAggregation_Parity` test that runs the basket through both the pre-rewrite and post-rewrite aggregation and asserts equality (the pre-rewrite snapshot captured as a golden in P5-C3's first commit, then the rewrite lands in the same commit asserting against it). `TestOrchestrator_LedgerOrdering` (asset → liability → earnings partition) MUST stay GREEN.

**P5-C4 — deletion (mechanical, gated on P5-C3).** Once P5-C3 proves no orchestrator consumer reads `result.Applied/.Adjustments/.Flags`:

1. Delete the per-rule `*AdjusterOutputToLegacyResult` translators (`a1…`, `a2…`, `a4…`, `a5…`, `aRD…`, `aCapSoftware…` in `assets.go`; `b1…`, `b2…`, `b3…` in `liabilities.go`; `c1…`-`c7…` in `earnings.go`).
2. Change the `Process{Asset,Liability,Earnings}Adjustments` dispatcher signatures to STOP returning the legacy `*AdjustmentResult` struct — return only the native carrier (`AdjusterOutput` aggregate, or a slimmed result that carries `NativeLedgerEntries`/`NativeOverlays`/native `Flags`/`NativelyEmittedRuleIDs`).
3. Delete the adjustments-package `AssetAdjustmentResult` (`assets.go:2092`), `LiabilityAdjustmentResult` (`liabilities.go:81`), `EarningsAdjustmentResult` (`earnings.go:53`) structs.
4. Delete the dormant `earnings.go` legacy-fallback `Process*Adjustment` helpers (§3.5).
5. Delete the dead `entities.{Asset,Liability}AdjustmentResult` structs (§4.5).

> **Scope-cut decision (translator retirement).** If P5-C3's enumeration reveals a consumer that genuinely needs the `entities.Adjustment` audit-trail shape and a clean `LedgerEntry → Adjustment` projection is non-trivial, P5-C4 may KEEP a single shared `adjustmentsFromLedger(ledger)` projection helper (one function, not 16 per-rule translators) and still delete the result structs + per-rule translators. The goal is "one native producer, ≤1 projection," not "zero projections at any cost." The implementer plan records the chosen shape.

### 3.5 Dormant legacy-fallback umbrella mutations (P5-C4)

`earnings.go` retains `ProcessRestructuringChargesAdjustment` (1574), `ProcessAssetSaleGainsAdjustment` (1641), `ProcessLitigationSettlementsAdjustment` (1683), and the capitalized-interest legacy helper. Each performs a direct `data.NormalizedOperatingIncome ±= X` (and `data.InterestExpense += X`) mutation. Post-Phase-4 these are reachable ONLY on the `if err != nil { result = ea.ProcessX...; break }` fallback branch of the corresponding `ApplyCx*` method — and the `Apply*` methods are pure component-delta computations that do not return errors in practice (the error branch is dead). Phase 4 closeout §6 explicitly deferred deleting these to Phase 5 because they entangle the still-load-bearing translator chain. P5-C4 deletes them together with the translators: remove the `if err != nil` fallback arm AND the dormant `Process*Adjustment` helpers in the same commit. `TestRecomputeUmbrellas_NoMutation` + shadow-snapshot byte-identity MUST stay GREEN (these helpers never fire, so deleting them changes no behavior).

### 3.6 `Raw()` deletion + accessor concurrency (P5-C5)

**`Raw()` deletion.** Phase 4 verified zero production `internal/` callers — the only reference is `internal/services/datacleaner/service_cleanwithviews_test.go:45` (a contract test). After P5-C2 migrates DDM (the conceptual "last consumer that might have needed an entity escape hatch"), delete `cleaneddata.CleanedFinancialData.Raw()` (`cleaned.go:79-96`) + its `TODO(phase-5)` comment, and update/remove the contract test. Acceptance: `grep -rn '\.Raw()' internal/` returns no matches.

**Accessor concurrency — RECOMMENDATION: formalize the request-local/immutability contract, do NOT retrofit `sync.Once`.** The three accessors (`AsReported`/`Restated`/`InvestedCapital`) lazily populate cached `*FinancialDataView` pointers without locking (flagged by gpt-5.5 + Phase 3 followup F.6 godoc). Reasoning for NOT adding locking now:

1. Every current consumer runs on a single request goroutine (no batch/parallel-fan-out endpoint exists — Phase 4 non-goal preserved).
2. `sync.Once` per accessor adds three `sync.Once` fields + lock acquisition on the hot path for a concern that has no current trigger — speculative complexity (KISS).
3. The accessors return **shared mutable memoized pointers**; a `sync.Once` only makes *initialization* safe, not concurrent *reads of a shared mutable view* if a future caller mutated the returned `*FinancialDataView`. The real contract that must hold is "callers treat the returned view as read-only and do not share a `*CleanedFinancialData` across goroutines." P5-C5 STRENGTHENS the existing godoc (`cleaned.go:32-40`) to state this as a hard contract and adds a one-line note at each accessor. If/when a parallel-read batch consumer lands (a future phase beyond DC-1), that phase owns the `sync.Once` retrofit with a concrete benchmark. This is a documented deferral, not a silent gap.

This is the single Phase 5 "optional" item ARCH recommends NOT implementing as code; it is formalized as contract documentation instead.

### 3.7 Optional: stop populating the legacy `historicalData` slot (P5-C5, gated on P5-C2)

`service.go` (valuation) populates `historicalData.Data[latestPeriod] = cleaningResult.CleanedData` after the cleaner runs. DDM was the last consumer reading the latest period via `GetLatestPeriod()` (for `StockholdersEquity`/`NetIncome`/`DividendsPerShare`). After P5-C2 migrates those reads to `LatestRestatedView`, assess whether ANY remaining consumer reads `historicalData.Data[latestPeriod]` for the *latest* period (the multi-year growth math reads PRIOR periods via `GetRecentYears`, which is a separate, still-needed population). **RECOMMENDATION: KEEP populating the slot** unless the implementer's grep proves zero latest-period readers remain. The slot is cheap, the growth math + `GetRecentYears` still rely on `historicalData` being populated, and `GetLatestPeriod()` is still used by DDM's `estimateDividendGrowth` for the DPS-CAGR walk (which reads `GetRecentYears`, not the cleaned latest slot — verify). If the grep proves the latest-period slot is genuinely unread after P5-C2, stop populating it; otherwise document why it stays. This is a "verify-then-decide" optional, not a mandated change.

### 3.8 Folder / file touch map

No new packages or folders. Phase 5 touches existing files only:

```
internal/
  services/
    valuation/
      models/
        ddm.go                          # P5-C1 (EV bridge +DebtLikeClaims), P5-C2 (Restated reads)
        ddm_phase4_invariance_test.go   # P5-C2 rename/retire → ddm_phase5_invariance_test.go (§7.4)
        ddm_bitforbit_test.go           # UNCHANGED — load-bearing guard
        router.go                       # ModelInput doc updates (DDM now reads DebtLikeClaims + LatestRestatedView)
      service.go                        # P5-C1 (modelDebtLikeClaims for DDM), P5-C2 (LatestRestatedView for DDM),
                                        #   CalculationVersion 4.3 → 4.4 (both stamp sites: 1323, 1635)
    datacleaner/
      service.go                        # P5-C3 (applyActiveAdjustments native aggregation), P5-C5 (optional slot)
      adjustments/
        assets.go                       # P5-C4 (delete a* translators + AssetAdjustmentResult)
        liabilities.go                  # P5-C4 (delete b* translators + LiabilityAdjustmentResult)
        earnings.go                     # P5-C4 (delete c* translators + EarningsAdjustmentResult + dormant fallbacks)
      cleaneddata/
        cleaned.go                      # P5-C5 (delete Raw() + marker; strengthen concurrency godoc)
        service_cleanwithviews_test.go  # P5-C5 (remove/replace Raw() contract-test ref)  [in datacleaner pkg]
  core/entities/
    data_cleaning.go                    # P5-C4 (delete dead entities.{Asset,Liability}AdjustmentResult)
docs/refactoring/...                    # spec + plan + closeout + parent-spec row update
```

---

## 4. Module / boundary notes

### 4.1 DDM ↔ valuation service boundary

DDM gains two `ModelInput` reads it did not have before: `DebtLikeClaims` (P5-C1) and `LatestRestatedView` (P5-C2). Both are already declared on `ModelInput` (`router.go:62`, `:70`) — Phase 4 added the fields but left DDM excluded. Phase 5 simply stops excluding DDM. No `ModelInput` shape change; no new constructor.

### 4.2 cleaneddata view contract (unchanged)

Phase 5 reads the existing `Restated()` / `InvestedCapital()` accessors; it does NOT change `restate.go` / `invested_capital.go` / `asreported.go`. The `Restated()` reducer's HIGH-1 contract (seed from post-clean entity; apply only `EquityOffset + TaxShieldDTA`; never re-apply `DeltaAmount`) is untouched — DDM reads the existing output.

### 4.3 Cleaner orchestrator ↔ adjustments boundary (P5-C3/C4)

The dispatcher return type changes (P5-C4 drops the legacy `*AdjustmentResult`), but the orchestrator's *needs* (native ledger entries, overlays, flags, fired-rule count) are already carried by the native fields. P5-C3 makes the orchestrator consume only those; P5-C4 removes the now-unread legacy carrier. The asset→liability→earnings call ORDER and the `data.AdjustmentLedger` partition are invariant.

### 4.4 Audit-trail / quality-scoring contract

`CleaningResult.Adjustments` / `.Flags` are public fields. P5-C3 must preserve their content for the 10-ticker basket (the behavior-preservation gate). If a downstream persisted consumer (the `valuation_results` snapshot / `adjustment_ledger` JSON column) reads these, the projection in P5-C3 keeps them stable. No SQLite schema migration (DC-1 non-goal, preserved).

### 4.5 Dead-type assessment — `entities.{Asset,Liability}AdjustmentResult`

`grep -rn 'entities\.AssetAdjustmentResult\|entities\.LiabilityAdjustmentResult\|entities\.EarningsAdjustmentResult' internal/` returns ONLY documentation matches (Phase 4 closeout). The adjustments package uses its OWN package-local `AssetAdjustmentResult` / `LiabilityAdjustmentResult` / `EarningsAdjustmentResult` structs (distinct types in `adjustments/*.go`). The `entities.*` duplicates in `core/entities/data_cleaning.go:468-485` are **dead code** with zero code references. **In scope for Phase 5 P5-C4 deletion** as a clean dead-code removal. (Verify the grep at implementation time before deleting — if a serialization/reflection path references them, defer with rationale.)

---

## 5. Replay drift expectation

The replay basket (`artifacts/tier2-baseline/` — 10 tickers: AAPL, AMD, BABA, EQIX, F, JNJ, KO, MSFT, MXL, TSM; JPM/BAC/WFC exercised by `TestDDM_LegacyPath_BitForBit`).

### 5.1 DDM tickers (JPM/BAC/WFC — fixture set)

- **`IntrinsicValuePerShare`, `EquityValue`: ZERO drift** (dividend-derived; independent of debt terms and of the Restated view when no Restater fires).
- **`EnterpriseValue`: ZERO drift on the fixtures** (DebtLikeClaims=0 ⇒ `+0` term). For LIVE B-rule-firing banks (NOT in the fixture set), `EnterpriseValue` INCREASES by the B1+B2+B3 amount — the intended §3.2 correction.
- `CalculationVersion: "4.3" → "4.4"` (structural field text, not numeric).

### 5.2 Non-DDM basket tickers

Phase 5 does NOT touch the DCF / revenue_multiple / ffo consumer paths (those migrated in Phase 4). Expected: **ZERO numeric drift** except `CalculationVersion: "4.3" → "4.4"` field text. The P5-C3/C4 translator retirement is internal to the cleaner audit trail and MUST NOT change `17-response.json` numerics.

### 5.3 Cleaner-stage artifacts

`10-clean-output.json` / `13-cleaner-audit.json`: P5-C3's native-aggregation rewrite MUST preserve `result.Adjustments` / `result.Flags` content (the behavior-preservation gate). If the audit-trail `Adjustment` projection differs in any field, the per-stage diff (`--diff-stages`) surfaces it — that is the P5-C3 STOP-AND-INVESTIGATE signal.

### 5.4 Replay attribution caveat (inherited from Phase 4 closeout §5)

The `2026-05-19` baseline is `calculation_version 4.1` (predates Phases 2/3/4). Clean per-cluster Phase-5 attribution requires a fresh `4.3` baseline captured at master's pre-Phase-5 tip via live SEC/market capture (cache-bypass), which is an operator follow-up. Phase 5 BACKEND runs the hermetic replay for pattern-detection (no UNEXPECTED ticker drift), and the operator does the clean 4.3→4.4 attribution at gate time.

---

## 6. CalculationVersion / SchemaVersion decision

**`CalculationVersion` bump 4.3 → 4.4.** Per `feedback_schema_version_atomic_bump` MEMORY (and the version-field semantics), the bump is atomic with the FIRST commit producing consumer-visible numeric drift — that is **P5-C1** (the DDM DebtLikeClaims EV correction). Both stamp sites (`service.go:1323` DCF path, `:1635` alt-model path) move to `"4.4"` in P5-C1. P5-C2 (view migration, zero numeric drift) does NOT bump again. Rationale for bumping even though only DDM `EnterpriseValue` drifts: `EnterpriseValue` is a consumer-visible response field, so a value-changing correction on a real (live) ticker class warrants the version signal; operators auditing a `4.3` DDM `enterprise_value` for a B-rule-firing bank may see a different (corrected) number at `4.4`.

> If P5-C1 is cut as the standalone Phase 4.x hotfix (§3.2.1), the 4.3 → 4.4 bump rides with that hotfix and Phase 5 proper ships entirely at `4.4` with no further bump.

**No `SchemaVersion["FinancialData"]` bump.** Phase 5 adds zero new `omitempty` fields to `entities.FinancialData` (stays at 9). The bump rule does not fire.

**No `SchemaVersion["ValuationResult"]` bump.** Phase 5 adds zero new `omitempty` fields to `entities.ValuationResult` (stays at 2). `DebtLikeClaims` is internal to `ModelInput` / `cleaneddata.FinancialDataView`, not persisted to the result.

**No SQLite schema migration** (DC-1 non-goal preserved). The `valuation_results` / `adjustment_ledger` columns are unchanged; P5-C4's struct deletion is internal-only.

---

## 7. DDM bit-for-bit preservation strategy (P5-C2) — 4-step re-proof sub-plan

This is the focal architectural risk. The sub-plan follows Phase 4 spec §9.4 + closeout §8, made concrete:

### 7.1 Step 1 — Re-run shadow snapshots + verify field equality on JPM/BAC/WFC

Before touching `ddm.go`'s reads:

1. `go test ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1` and confirm `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0 (no Restater fires for JPM/BAC/WFC — empty `recent_adjusters`).
2. Add a one-shot assertion (temporary, deleted in step 4) `TestDDM_RestatedEqualsLatest_OnFixtures`: for each of JPM/BAC/WFC golden inputs, build `cleaneddata.New(latest, latest)` and assert `math.Float64bits(Restated().StockholdersEquity) == math.Float64bits(latest.StockholdersEquity)`, same for `NetIncome` and `DividendsPerShare`. This PROVES the field-level bit-equality the migration relies on, independent of the DDM math.

### 7.2 Step 2 — Temporary parallel-write test `TestDDM_RestatedView_BitForBit`

Add a temporary test that runs the DDM model with `Restated()`-sourced reads (via a populated `LatestRestatedView`) against the SAME JPM/BAC/WFC goldens, asserting `math.Float64bits` equality on `IntrinsicValuePerShare` / `EquityValue` / `EnterpriseValue` / `Warnings` / `Confidence`. Verify GREEN BEFORE migrating the production reads. This catches any arithmetic-reordering drift introduced by the view indirection (there should be none).

### 7.3 Step 3 — Migrate the `ddm.go` reads

ONLY after steps 1+2 are GREEN: change `runDividendDiagnostics` + `estimateDividendGrowth` to read the restated view's `StockholdersEquity`/`NetIncome` (and optionally `DividendsPerShare`). Populate `ModelInput.LatestRestatedView` for DDM in `service.go`. Re-run `TestDDM_LegacyPath_BitForBit` + `TestDDM_ConsumerPath_UnaffectedByPhase4` + the step-2 parallel-write test → all GREEN. **If `TestDDM_LegacyPath_BitForBit` fails, REVERT the commit — do NOT update the goldens** (CLAUDE.md gotcha, non-negotiable cross-Tier-2 contract).

### 7.4 Step 4 — Delete the temporary tests; rename/retire the Phase 4 guard

- Delete the temporary `TestDDM_RestatedEqualsLatest_OnFixtures` (step 1) and `TestDDM_RestatedView_BitForBit` (step 2) — their job is done once the production path reads the view and `TestDDM_LegacyPath_BitForBit` guards the math.
- **`TestDDM_ConsumerPath_UnaffectedByPhase4` rename/retire decision:** this test (`ddm_phase4_invariance_test.go`) pins that DDM's *inputs via `GetLatestPeriod()`* are unchanged. After P5-C2 DDM reads the view, the "input unchanged" framing is obsolete for the migrated fields. **RECOMMENDATION: RENAME it to `TestDDM_ConsumerPath_RestatedViewParity`** and re-point its assertions to verify (a) output bits still equal the goldens AND (b) the `LatestRestatedView` fields DDM now consumes (`StockholdersEquity`/`NetIncome`/`DividendsPerShare`) are bit-equal to the entity fields for the fixtures. This keeps a permanent superset guard (input-parity + output-parity) under the new read path, rather than deleting the only test that pins the view-equals-entity property for DDM. `ddm_bitforbit_test.go` (`TestDDM_LegacyPath_BitForBit`) stays UNCHANGED as the canonical math guard.

### 7.5 Why this ordering is safe

`TestDDM_LegacyPath_BitForBit` is GREEN at every commit because (P5-C1) the `+DebtLikeClaims` term is `+0` for the fixtures and (P5-C2) `Restated().{SE,NI,DPS} == latest.{SE,NI,DPS}` for the fixtures (no Restater fires; NI/DPS identity-copied). The two changes are independent and each is individually bit-for-bit safe; sequencing P5-C1 before P5-C2 means a bit-for-bit failure at P5-C1 implicates the EV term and at P5-C2 implicates the view read — disjoint bisect targets.

---

## 8. Testing strategy

### 8.1 Load-bearing invariants — MUST stay GREEN at every Phase 5 commit

| Invariant | Path | Why load-bearing in Phase 5 |
|---|---|---|
| `TestDDM_LegacyPath_BitForBit` | `internal/services/valuation/models/ddm_bitforbit_test.go` | Cross-Tier-2 contract. P5-C1 (+DebtLikeClaims=0) + P5-C2 (Restated==latest) preserve it. Failure ⇒ REVERT, never update goldens. |
| `TestDDM_ConsumerPath_UnaffectedByPhase4` → renamed `…_RestatedViewParity` (§7.4) | `models/ddm_phase4_invariance_test.go` → `…phase5…` | Superset pin: output bits + view-field parity under the new read path. |
| `TestRecomputeUmbrellas_NoMutation` | `internal/services/datacleaner/recompute_test.go` | `recompute.go` untouched; P5-C4 dormant-fallback deletion changes no live behavior. |
| `TestOrchestrator_LedgerOrdering` | `internal/services/datacleaner/ledger_invariants_test.go` (or sibling) | P5-C3 rewrites aggregation but preserves asset→liability→earnings partition + `data.AdjustmentLedger` order. |
| Shadow snapshots byte-identical | `internal/integration/testdata/recompute-shadow/<TICKER>.json` (`git diff --quiet` exits 0) | Cleaner adjuster EXECUTION is unchanged in Phase 5 (P5-C3/C4 touch aggregation + dead code only). UNLIKE Phase 4 (which relaxed this for Option A), Phase 5 MUST hold byte-identity — if a snapshot regenerates, P5-C3/C4 accidentally changed adjuster output. STOP-AND-INVESTIGATE. |
| `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) | `internal/integration/datacleaner_ledger_basket_test.go` | Per-ticker AdjusterID sets unchanged. |
| `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` | same file | AMD $9.679B / KO $60.912B reconstruction unchanged. |
| `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` + `…_OnEarningsFire` | `internal/services/datacleaner/service_cleanwithviews_no_double_count_test.go` | HIGH-1 pins. `restate.go` untouched; stay GREEN. |
| `TestCleanedFinancialData_Restated_C6EquityOffsetZero` | `cleaneddata/restate_test.go` | C6 invariant; `restate.go` untouched. |
| `TestRevenueMultiple_SubtractsDebtLikeClaims` + `…_Forward_…` | `models/revenue_multiple_test.go` | Phase 4 correction; untouched by Phase 5. |
| Full `go test ./... -count=1` | (full suite) | GREEN at every commit. |

### 8.2 New tests required for Phase 5

1. **`TestDDM_EVBridge_AddsDebtLikeClaims`** (P5-C1; shipped under this name, NOT the spec's draft "Subtracts" name — the parallel with `TestRevenueMultiple_SubtractsDebtLikeClaims` is by PURPOSE, not by sign) — fire a synthetic B-rule with $X DebtLikeClaims through the DDM model; assert `EnterpriseValue == EquityValue + InterestBearingDebt + DebtLikeClaims − Cash` (note the `+` for DDM, per §3.2), AND assert `IntrinsicValuePerShare` + `EquityValue` are UNCHANGED versus the zero-claims case (DDM equity is dividend-derived). Both legacy-Gordon and multi-stage paths.
2. **`TestDDM_EVBridge_ZeroClaims_Unchanged`** (P5-C1, backward-compat) — DebtLikeClaims=0 leaves the bridge byte-identical (guards the bit-for-bit safety argument explicitly).
3. **Temporary `TestDDM_RestatedEqualsLatest_OnFixtures`** (P5-C2 step 1, deleted in step 4) — §7.1.
4. **Temporary `TestDDM_RestatedView_BitForBit`** (P5-C2 step 2, deleted in step 4) — §7.2.
5. **`TestApplyActiveAdjustments_NativeAggregation_Parity`** (P5-C3) — basket run asserting `result.Adjustments` + `result.Flags` content/order are byte-identical before/after the native-aggregation rewrite (golden captured pre-rewrite in the same commit).
6. **`TestDispatcherSignature_NoLegacyResultStruct`** (P5-C4) — a compile-time/grep guard (or a small reflection test) asserting the dispatcher return types no longer reference `{Asset,Liability,Earnings}AdjustmentResult`; complemented by the deletion itself (the structs are gone, so any stray reference fails to compile).
7. **`TestRaw_Deleted`** (P5-C5) — not a Go test but an acceptance check: `grep -rn '\.Raw()' internal/` returns no matches; the `Raw` method symbol is gone (compile guard via deleting the contract test's call).

### 8.3 Replay regression methodology (per cluster)

1. Before each commit: `go run ./cmd/replay --diff-stages --workers=4 artifacts/tier2-baseline/<latest>/` and capture per-ticker numeric diff.
2. Compare against §5:
   - **P5-C1:** ZERO numeric drift on the hermetic basket (no fixture fires B-rules for a DDM ticker); `CalculationVersion 4.3 → 4.4` field text. (Live B-rule banks would show EV drift — not in the hermetic basket.)
   - **P5-C2:** ZERO numeric drift (Restated==entity for the fixtures). `17-response.json` byte-identical except the already-bumped version field.
   - **P5-C3:** ZERO `17-response.json` drift; verify `10-clean-output.json` / `13-cleaner-audit.json` `Adjustments`/`Flags` content unchanged (behavior-preservation gate).
   - **P5-C4:** ZERO drift anywhere (dead-code + internal-type deletion).
   - **P5-C5:** ZERO drift.
3. Any UNEXPECTED drift ⇒ STOP-AND-INVESTIGATE before the next commit.

---

## 9. PR strategy

**Single PR, 5 commit clusters** (P5-C1..P5-C5), branch `dc1-phase-5` from master (post-Phase-4 merge). The single-PR justification mirrors Phase 4: the DDM changes (P5-C1, P5-C2) and the translator retirement (P5-C3, P5-C4) are tightly coupled by the migration sequence; splitting into multiple PRs creates intermediate master states with a half-retired translator stack (compile-fragile) and two separate `CalculationVersion` decisions. Commits are independently reviewable by `git show <sha>`.

**Fallback Option B (RECOMMENDED-CONDITIONAL):** if HUMAN elects the Phase 4.x hotfix per §3.2.1, P5-C1 ships as a standalone `dc1-phase-4.1` PR (bump 4.3 → 4.4) and the remaining clusters (P5-C2..P5-C5) ship as the `dc1-phase-5` PR at `4.4`. This is the ONLY clean split point because P5-C1 is the only numeric-drift commit; everything after is zero-drift refactor/cleanup.

**Worktree-first workflow** (MEMORY `feedback_worktree_first_workflow`): the Phase 5 implementation runs in a `dc1-phase-5` git worktree (sibling `midas-dc1-phase-5/`); main `midas/` stays on master. This spec + plan were authored in the `dc1-phase-5-prep` worktree.

---

## 10. Open questions

None blocking. Four items for HUMAN awareness with explicit dispositions:

1. **Phase 4.x hotfix vs bundle for the DDM DebtLikeClaims correction (§3.2.1).** ARCH recommends BUNDLE into Phase 5 (P5-C1). HUMAN decides; the spec supports either.
2. **`result.Adjustments` audit-trail projection shape (P5-C3).** Whether the orchestrator needs a full `LedgerEntry → entities.Adjustment` projection or a lighter summary depends on the enumerated consumers; the implementer plan resolves it empirically with a grep + the behavior-preservation gate. Disposition: decide during P5-C3 implementation; prefer ≤1 shared projection helper over 16 per-rule translators.
3. **Accessor `sync.Once` retrofit (§3.6).** ARCH recommends formalizing the request-local contract in godoc, NOT adding locking. Revisit only when a parallel-read batch consumer lands (post-DC-1).
4. **Stop populating the legacy `historicalData` slot (§3.7).** "Verify-then-decide": keep it unless the implementer's grep proves zero latest-period readers after P5-C2. DDM's `GetRecentYears` DPS-CAGR walk still needs `historicalData` populated for PRIOR periods regardless.

---

## 11. Phase 5 → DC-1 done gate

DC-1 closes when ALL of:

1. All Phase 5 invariants GREEN (§8.1), INCLUDING shadow-snapshot byte-identity (`git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0 — Phase 5 does NOT relax this, unlike Phase 4).
2. `TestDDM_LegacyPath_BitForBit` GREEN at every commit (JPM/BAC/WFC `math.Float64bits` equality).
3. DDM reads the `Restated()` view (`ddm.go` no longer reads `latest.{StockholdersEquity,NetIncome,DividendsPerShare}` via `GetLatestPeriod()` for the migrated fields) AND the DDM EV bridge ADDS `DebtLikeClaims` (§3.2 correction live; DDM ADDS, opposite sign vs DCF/revenue_multiple which subtract).
4. `grep -rn '\.Raw()' internal/` returns no matches; `cleaneddata.Raw()` deleted.
5. Per-rule `*AdjusterOutputToLegacyResult` translators + adjustments-package `{Asset,Liability,Earnings}AdjustmentResult` structs + dead `entities.{Asset,Liability}AdjustmentResult` structs + dormant `earnings.go` legacy-fallback helpers all deleted; `go build ./...` + `go test ./... -count=1` GREEN.
6. `CalculationVersion` is `"4.4"` everywhere stamped (both DCF + alt-model paths).
7. Replay diff on the hermetic basket matches §5 (DDM tickers: IntrinsicValue/EquityValue zero-drift, EnterpriseValue zero-drift on fixtures; non-DDM basket zero-drift; all modulo the `CalculationVersion` field).
8. (Operator follow-up) Fresh `4.3` baseline captured + clean 4.3→4.4 attribution pass; `artifacts/tier2-baseline/` refreshed at the Phase 5 ship sha.
9. Phase 5 closeout doc filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-5-closeout.md`.
10. **DC-1 close-out steps** (parent spec "Acceptance criteria for closing DC-1"): CLAUDE.md "Common Gotchas" retires the single-view note + the translator-still-load-bearing note; DC-1 tracker archived to `docs/reviewer/archive/`; THESIS/AGENTS/ARCHITECTURE rows updated.

---

## 12. Acceptance criteria (checklist for BACKEND self-validation)

- [ ] This spec lands at `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md`
- [ ] Implementer plan filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-5-implementation-plan.md`
- [ ] Parent spec's "Phasing & implementation sequence" Phase 5 row updated to reference this spec + plan
- [ ] P5-C1: DDM EV bridge ADDS `DebtLikeClaims` (both legacy + multi-stage); `service.go` populates `modelDebtLikeClaims` for DDM; `CalculationVersion 4.3 → 4.4`; `TestDDM_EVBridge_AddsDebtLikeClaims` + `…_ZeroClaims_Unchanged` + `…_GoldenFixtures_ZeroDebtLikeClaims` GREEN
- [ ] P5-C2: DDM reads `Restated()` via `LatestRestatedView`; 4-step re-proof executed; `TestDDM_LegacyPath_BitForBit` GREEN at every commit; temp tests added then deleted; Phase 4 guard renamed to `…RestatedViewParity`
- [ ] P5-C3: `applyActiveAdjustments` aggregates from native slices; `TestApplyActiveAdjustments_NativeAggregation_Parity` GREEN; `result.Adjustments`/`.Flags` content byte-identical on basket
- [ ] P5-C4: per-rule translators + adjustments-package result structs + dead `entities.*` structs + dormant `earnings.go` fallbacks deleted; `go build ./...` GREEN
- [ ] P5-C5: `cleaneddata.Raw()` + marker deleted; contract-test ref removed; `grep -rn '\.Raw()' internal/` empty; concurrency godoc strengthened; (optional) legacy slot decision documented
- [ ] Shadow snapshots byte-identical: `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0 at every commit
- [ ] All §8.1 load-bearing invariants GREEN at every commit
- [ ] Full `go test ./... -count=1` exit 0 at every commit
- [ ] Replay diff matches §5 per ticker
- [ ] Phase 5 closeout doc filed; DC-1 tracker archived; CLAUDE.md/AGENTS/THESIS/ARCHITECTURE rows updated

---

## 13. Change log

| Date | Change |
|---|---|
| 2026-05-27 | Phase 5 spec + implementer plan AUTHORED on branch `dc1-phase-5-prep` (worktree, forked from master `ce94f70` post-Phase-4). Scope: (1) DDM consumer migration to `Restated()` via `LatestRestatedView` with a 4-step bit-for-bit re-proof (§7); (2) NEW DDM EV-bridge `DebtLikeClaims` correction (DDM analog of the Phase 4 `revenue_multiple` finding — ARCH recommends BUNDLE into Phase 5 as commit P5-C1, not a standalone hotfix, §3.2.1); (3) legacy translator + adjustments-package + dead `entities.*` result-struct retirement via consumer-migration-then-deletion (P5-C3 → P5-C4); (4) dormant `earnings.go` legacy-fallback deletion; (5) `cleaneddata.Raw()` deletion; (6) accessor concurrency — formalize request-local contract, no `sync.Once` (recommended); (7) optional legacy-slot stop-populate (verify-then-decide). `CalculationVersion 4.3 → 4.4` atomic with P5-C1. No `SchemaVersion` bump. Single PR, 5 commit clusters (P5-C1 isolates the EV-correction numeric drift; P5-C2 isolates the load-bearing view migration; P5-C3 behavior-preserving native aggregation; P5-C4 mechanical deletion; P5-C5 cleanup + closeout). Estimated 9–14 agent-hours. DC-1 done gate documented (§11). |
