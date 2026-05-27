# Datacleaner — Phase 4 Spec (Consumer Migration + B3 Routing Flip + Dual-Write Deletion)

**Status:** DESIGN (authored 2026-05-26, ready for BACKEND dispatch)
**Phase:** Phase 4 of the DC-1 refactor sequence (5 phases total)
**Parent spec:** [datacleaner-component-primitive-and-parallel-views-spec.md](datacleaner-component-primitive-and-parallel-views-spec.md) — §"Phasing & implementation sequence" row "Phase 4 — Consumer migration + WACC boundary" + §"Consumer migration map"
**Phase 3 spec:** [datacleaner-component-primitive-and-parallel-views-phase-3-spec.md](datacleaner-component-primitive-and-parallel-views-phase-3-spec.md)
**Phase 3 followup spec:** [dc1-phase-3-followup-spec.md](dc1-phase-3-followup-spec.md)
**Phase 3 closeout:** [datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md](../archive/datacleaner-component-primitive-and-parallel-views-phase-3-closeout.md)
**Phase 3 followup closeout:** [dc1-phase-3-followup-closeout.md](../archive/dc1-phase-3-followup-closeout.md)
**Implementer plan:** [datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md](../implementations/datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md)
**Tracker:** [docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md](../../reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md)
**Estimated effort:** 12–18 agent-hours (single PR with 5 commit clusters — see §11 PR strategy)

---

## 1. Phase context

Phase 3 merged to master 2026-05-24 as `46e84b1` and introduced the three-view accessor surface — `cleaneddata.CleanedFinancialData` with `AsReported()` / `Restated()` / `InvestedCapital()` accessors fed by `Service.CleanFinancialDataWithViews(ctx, data)`. The Phase 3 followup merged 2026-05-26 as `3490227` and closed 9 cross-model review findings — most consequentially HIGH-1, which restructured `Restated()` to seed from the POST-CLEAN entity and apply ONLY `LedgerEntry.EquityOffset + TaxShieldDTA` (the dispatcher dual-write has already restated the component fields, so re-applying `DeltaAmount` would double-count).

What Phase 3 + followup explicitly **did NOT** ship (Phase 3 spec §3 Non-goals, all of which are now Phase 4 goals):

| NON-goal (Phase 3) | → Phase 4 goal |
|---|---|
| No consumer migration — 13 `data.*` read sites in `internal/services/valuation/*` stay unchanged | Migrate all 13 read sites to `CleanedFinancialData` view accessors |
| No B3 routing flip — WACC reads dispatcher-mutated `data.TotalDebt` for B3 | WACC reads `InvestedCapital().DebtLikeClaims` for B3 (with B1+B2 also flowing through `DebtLikeClaims`); WACC's capital-structure denominator continues to read `Restated().InterestBearingDebt` only |
| No dispatcher dual-write deletion — `data.X ±= Y` mutations in `Process{Asset,Liability,Earnings}Adjustments` switch arms stay | Delete every dispatcher dual-write atomically with consumer migration |
| No `CalculationVersion` bump (stays at `"4.2"`) | Bump to `"4.3"` atomic with the first commit that produces consumer-visible numeric drift |
| No translator extraction | (Closed in Phase 3 §5.4 — KEEP per-rule. Phase 4 deletes them alongside the dual-write.) |

**Phase 4 is the FIRST consumer-visible numeric change since v0.10.0** (the Graham PR #2 tangible-share denominator flip). Replay diff on the basket WILL show numeric drift in `17-response.json` for any ticker where (a) a Restater fires AND a non-DDM consumer reads a Restater-touched field, or (b) B3 fires (contingent-liability tickers — large pharma, mining, financials with material legal/environmental exposures). The drift is the entire point of the refactor: the B3 routing flip corrects a known accuracy bug where WACC weights were distorted by contingent liabilities being treated as interest-bearing capital.

This spec also defines the strategy for preserving the load-bearing **`TestDDM_LegacyPath_BitForBit`** invariant (JPM/BAC/WFC `math.Float64bits` equality) through DDM's consumer migration — the most consequential architectural risk in Phase 4.

---

## 2. Goals (in priority order)

1. **Migrate 13 consumer read sites** in `internal/services/valuation/*` from `data.X` direct reads to `CleanedFinancialData` view accessor reads (`AsReported()` / `Restated()` / `InvestedCapital()`). Each read site picks ONE view at migration time per the consumer migration map in §4.2.
2. **Flip B3 routing**: the WACC consumer reads `InvestedCapital().DebtLikeClaims` for B3's contingent-liability contribution; capital-structure denominator continues to read interest-bearing debt only.
3. **Delete every dispatcher dual-write** (`data.X ±= Y` lines) in `Process{Asset,Liability,Earnings}Adjustments` switch arms, atomically with the consumer migration that depends on each value.
4. **Delete per-rule translator helpers** (and the legacy `{Asset,Liability,Earnings}AdjustmentResult` structs, if they have no remaining callers) — these existed solely to maintain dual-write to the legacy result shape (Phase 3 spec §5.4 closed this decision).
5. **Bump `CalculationVersion` 4.2 → 4.3** atomic with the first commit that produces consumer-visible numeric drift.
6. **Refresh `artifacts/tier2-baseline/` bundle baseline** at the Phase 4 ship sha so downstream replay regression has a stable reference.
7. **Preserve `TestDDM_LegacyPath_BitForBit`** (JPM/BAC/WFC `math.Float64bits` equality) through every commit. DDM consumer migration follows a special bit-for-bit-preserving sub-plan (see §9 DDM migration sub-plan).

## 3. Non-goals

- **No `Raw()` deletion.** `cleaneddata.CleanedFinancialData.Raw()` carries a `TODO(phase-5)` comment from the followup. Phase 5 owns its deletion AFTER Phase 4 closes the migration window (no consumer can call `Raw()` once migration is complete). Phase 4 verifies it has zero remaining callers but does NOT remove it.
- **No new view types** (e.g. a future `CashFlowView` for working-capital schedule audits). Stay within the three views Phase 3 ships.
- **No `sync.Once` retrofit on `cleaneddata` accessors.** Phase 5 owns this if and when a parallel-read consumer lands. Phase 4 consumers all run on the request goroutine.
- **No new B-rule overlay types** (e.g. a future B4 environmental remediation reserve). The current 4 overlay-emitting adjusters (A1, B1, B2, B3) plus 8 Restaters + 3 FlagEmitters define the adjustment set Phase 4 migrates against.
- **No SEC parser fix for T2-BS-3 (AMD/KO `TotalLiabilities==0` dropout).** Phase 3's `Restated()` reconstruction makes the issue invisible to migrated consumers; the parser fix stays deferred (Option B carve-out).
- **No `CleaningResult` schema migration** in the SQLite `valuation_results` table. The `adjustment_ledger` JSON column carries the necessary state already (added in earlier phases).
- **No batch endpoint or parallel-fan-out consumer.** Phase 4 stays sequential; goroutine-safety concerns stay deferred.
- **No DDM math change.** DDM stays bit-for-bit on JPM/BAC/WFC (§9). The only DDM file touched is the seam where it receives `*FinancialData` from `service.go` — the entity it consumes downstream is unchanged.
- **No `WACCInputs` struct extraction** as a public Go type. The parent spec §"Consumer migration map" originally proposed `wacc.WACCInputs` as a compile-time gate; Phase 4 inlines the same compile-time boundary by passing `cleaned *cleaneddata.CleanedFinancialData` to the WACC build site directly. Reasoning: a separate `WACCInputs` struct duplicates the field set already on `FinancialDataView`, and the compile-time gate is already provided by the typed `*FinancialDataView` return value from accessors. A future refactor can promote it if the inlined version proves awkward.

---

## 4. Architecture

### 4.1 Migration strategy

**Single PR, five commit clusters.** The 13 read sites cluster naturally by consumer; each cluster migrates as one atomic commit so that load-bearing invariants stay GREEN at every commit boundary. The five clusters:

| Cluster | Read sites | Consumer-side commits | Dispatcher dual-writes deleted in this cluster |
|---|---|---|---|
| **C-1: Plumbing** | (none — wires the views into `service.go`) | 1 commit: capture `*cleaneddata.CleanedFinancialData` at the `s.dataCleaner.CleanFinancialData` callsite (line 478); thread through `performValuation` / `runAlternativeModel` signatures as an additional parameter | (none — no dual-writes deleted yet) |
| **C-2: Working capital + ROIC + freshness** | service.go:604/606/607 (ROIC denominator), 1753-1771 (NWC change latest+prior), 1635 (effectiveOI helper) | 1 commit: flip reads to `Restated()` for these 5 fields; load-bearing invariant: ROIC NOPAT numerator and denominator both come from `Restated()` (coherent restatement) | A2 + A4 + A5 asset Restater dual-writes (lines 1145, 1219, 1220, 1296 of assets.go) — Restated.Inventory / OtherIntangibles / DeferredTaxAssets / TotalAssets are now driven by the ledger reducer + `Restated()` umbrella recompute, not the dual-write |
| **C-3: DCF FCF inputs + cross-check** | service.go:919 (OI for NOPAT fallback), 1033 (NOPAT negative-OI error), 1330-1343 (cross-check EPS/EBITDA/FCF), router.go:158-166 (router OI), revenue_multiple.go:108/149/151/203, ffo.go:331/357/358/362 | 1 commit: flip reads to `Restated()` for OperatingIncome / NormalizedOperatingIncome / NetIncome / D&A / CapEx; flip `InterestBearingDebt` + `CashAndCashEquivalents` to `Restated()` (B3 routing flip touches WACC only, NOT these) | C1/C2/C3/C5/C6 earnings Restater dual-writes (earnings.go:1380, 1407, 1427, 1490, 1524, 1615, 1657, 1717, 1816-1819, 1863) — Restated.NormalizedOperatingIncome / InterestExpense are driven by the ledger reducer; legacy in-line mutations (e.g. `data.NormalizedOperatingIncome += result.Amount`) are deleted |
| **C-4: WACC + EV→Equity bridge + B3 routing flip** | service.go:678-682 (D/E ratio + unlever/relever beta), 708-710 (waccInputs.MarketValueOfDebt + InterestExpense + TaxRate), 1191-1197 (CalculateEquityValue bridge), 1205-1208 (equity_bridge calc trace) | 1 commit: flip WACC inputs to `Restated().InterestBearingDebt` + `Restated().InterestExpense` + `Restated().TaxRate`; flip EV→Equity bridge `InterestBearingDebt` and `CashAndCashEquivalents` to `Restated()`; **add new `InvestedCapital().DebtLikeClaims` subtraction in the EV→Equity bridge call** (NEW input parameter to `dcf.CalculateEquityValue`); WACC MarketValueOfDebt continues to read `Restated().InterestBearingDebt` (B3 does NOT enter the capital-structure denominator — that's the point of the routing flip) | A1 goodwill dual-write (assets.go:1056-1057, 1410-1411); B1 + B2 + B3 liability dual-writes (liabilities.go:303 + the inline `data.TotalDebt += result.Amount` arms in the dispatcher switch). **This is the most numerically-consequential commit.** |
| **C-5: Graham + DDM + tangible value + currency** | graham.go:61/64/106-117 (NCAV / current_assets / total_liabilities derivation), ddm.go:178/181/213/291/292 (ROE + bookValuePerShare), service.go:1563-1578 (`calculateTangibleValuePerShare`), currency.go:189-224 (FX conversion site) | 1 commit: Graham → `AsReported()` (NCAV is intentionally conservative as-filed); DDM → preserve via path-discipline (see §9 DDM sub-plan); tangible value → `Restated()`; currency.go: NO change (FX conversion runs PRE-cleaner per Phase 0 invariant, on the raw `*FinancialData` before any view exists; staying at `data.X` is correct) | (no dispatcher dual-writes — A1 + B-rules were absorbed in C-4; A2/A4/A5/C* were absorbed in C-2/C-3; nothing left to delete) |

The cluster ordering minimizes co-mutation risk: each commit migrates a consumer cluster AND deletes the dual-writes that fed it, with no commit straddling a value that's read from a partially-migrated state.

**Alternative considered: atomic mega-PR with one consumer-migration commit.** Rejected because a single commit that deletes all 13 dual-writes and flips all 13 read sites is impossible to bisect if a regression surfaces; the cluster split gives REVIEWER + bisect tooling a one-commit-per-regression-class signal. The PR remains single, but the commit ladder is explicit.

### 4.2 Consumer migration map (13 read sites + 1 plumbing change)

For each read site, the new read pinpoints which view, why, and the BEFORE/AFTER shape.

#### 4.2.1 ROIC denominator (service.go:604-607)

**Current:**
```go
nopat := latestForROIC.NormalizedOperatingIncome * (1 - latestForROIC.TaxRate)
investedCapital := growth.CalculateInvestedCapital(
    latestForROIC.StockholdersEquity,
    latestForROIC.InterestBearingDebt,
    latestForROIC.CashAndCashEquivalents,
)
```

**Migrated (cluster C-2):**
```go
restated := cleaned.Restated()
nopat := restated.NormalizedOperatingIncome * (1 - latestForROIC.TaxRate)  // TaxRate stays from latestForROIC; not Restater-touched
investedCapital := growth.CalculateInvestedCapital(
    restated.StockholdersEquity,
    restated.InterestBearingDebt,
    latestForROIC.CashAndCashEquivalents,  // not Restater-touched today; explicit AsReported would be equivalent
)
```

**Rationale:** ROIC NOPAT is computed from Restater-touched `NormalizedOperatingIncome` (C1-C7). For coherence the denominator (`StockholdersEquity` after equity offsets from A2/A4/A5 Restaters, plus `InterestBearingDebt`) must also reflect the restated economic position. Mixing `Restated().NOPAT` with `AsReported().StockholdersEquity` produces inflated ROIC for any company with impairments.

#### 4.2.2 NOPAT-fallback guard (service.go:915-919)

**Current:**
```go
if !(latestFinancialData.Revenue == 0 && latestFinancialData.NormalizedOperatingIncome == 0) {
    // ...
}
oiVal := latestFinancialData.OperatingIncome
```

**Migrated (cluster C-3):**
```go
restated := cleaned.Restated()
if !(latestFinancialData.Revenue == 0 && restated.NormalizedOperatingIncome == 0) {
    // Revenue is not Restater-touched; stays at latestFinancialData.Revenue
}
oiVal := restated.OperatingIncome
```

#### 4.2.3 Negative-OI sentinel guard (service.go:1033)

**Current:**
```go
return nil, fmt.Errorf("%w: company has non-positive operating income (%.2f); ...", ErrModelNotApplicable, latestFinancialData.NormalizedOperatingIncome)
```

**Migrated (cluster C-3):** read `restated.NormalizedOperatingIncome` (the sentinel must match the math the engine actually used).

#### 4.2.4 WACC inputs (service.go:678-682, 708-710)

**Current:**
```go
if marketEquity > 0 && latestFinancialData.InterestBearingDebt > 0 {
    debtEquityRatio := latestFinancialData.InterestBearingDebt / marketEquity
    // ... unlever/relever beta ...
}

waccInputs := wacc.Inputs{
    // ...
    MarketValueOfDebt:   latestFinancialData.InterestBearingDebt,
    InterestExpense:     latestFinancialData.InterestExpense,
    TaxRate:             latestFinancialData.TaxRate,
}
```

**Migrated (cluster C-4):**
```go
restated := cleaned.Restated()
if marketEquity > 0 && restated.InterestBearingDebt > 0 {
    debtEquityRatio := restated.InterestBearingDebt / marketEquity
    // ...
}

waccInputs := wacc.Inputs{
    // ...
    MarketValueOfDebt:   restated.InterestBearingDebt,  // B-rules NO LONGER feed this; B3 flip realized
    InterestExpense:     restated.InterestExpense,     // C6 restater touches this
    TaxRate:             latestFinancialData.TaxRate,
}
```

**Rationale & B3 ROUTING FLIP:** the parent spec §"B3 routing correction" calls out the substantive accuracy change. After the followup, `Restated().InterestBearingDebt` is identical to `latestFinancialData.InterestBearingDebt` because dispatcher dual-write mutates `data.TotalDebt` and `data.InterestBearingDebt` for B1/B2/B3 today. Phase 4 C-4 deletes those B-rule dual-writes; consequently `Restated().InterestBearingDebt` becomes the parser-stamped value (no B-rule inflation), and `InvestedCapital().DebtLikeClaims` carries B1+B2+B3 contributions separately. **For any ticker with material contingent liabilities, WACC weights will shift** — the entire point of the refactor.

#### 4.2.5 EV → Equity bridge (service.go:1191-1197)

**Current:**
```go
equityValue := dcf.CalculateEquityValue(
    dcfResult.EnterpriseValue,
    latestFinancialData.InterestBearingDebt,
    latestFinancialData.CashAndCashEquivalents,
    latestFinancialData.MinorityInterest,
    latestFinancialData.PreferredEquity,
)
```

**Migrated (cluster C-4):**
```go
restated := cleaned.Restated()
investedCap := cleaned.InvestedCapital()
equityValue := dcf.CalculateEquityValueWithDebtLikeClaims(
    dcfResult.EnterpriseValue,
    restated.InterestBearingDebt,
    latestFinancialData.CashAndCashEquivalents,  // not Restater-touched
    latestFinancialData.MinorityInterest,        // not Restater-touched
    latestFinancialData.PreferredEquity,         // not Restater-touched
    investedCap.DebtLikeClaims,                  // NEW — B1+B2+B3 amounts subtracted from EV
)
```

**Rationale:** the bridge from enterprise value to common-equity value must subtract `CapitalStructureDebt + DebtLikeClaims` — both compete with shareholders for cash flows. Phase 4 adds `DebtLikeClaims` as a sixth parameter to a NEW function `dcf.CalculateEquityValueWithDebtLikeClaims` (Phase 5 may collapse this with the existing `dcf.CalculateEquityValue` once all callers migrate). For tickers with zero `DebtLikeClaims` (no B1/B2/B3 fires), `equityValue` is unchanged.

**Open question CLOSED here**: should the new function be additive (`CalculateEquityValueWithDebtLikeClaims`) or replace `CalculateEquityValue` in-place? Answer: ADDITIVE. The legacy 5-arg signature stays for backwards compatibility with tests + the alt-model `revenue_multiple`/`ffo` paths which build equity differently (they compute `equityValue = enterpriseValue - input.InterestBearingDebt + input.CashAndCashEquivalents` inline; those sites flip to read `restated.InterestBearingDebt` but do not gain the `DebtLikeClaims` subtraction in Phase 4 — alt models compute their own enterprise value, not a DCF one, and don't have a B3 contingent-liability story today).

#### 4.2.6 DCF cross-check inputs (service.go:1330-1343)

**Current:**
```go
if latestFinancialData.NetIncome > 0 && sharesOutstanding > 0 {
    eps = latestFinancialData.NetIncome / sharesOutstanding
}
ebitda := latestFinancialData.OperatingIncome + latestFinancialData.DepreciationAndAmortization
// ...
fcf := latestFinancialData.NetIncome + latestFinancialData.DepreciationAndAmortization - latestFinancialData.CapitalExpenditures
```

**Migrated (cluster C-3):** read `restated.NetIncome`, `restated.OperatingIncome`, `restated.DepreciationAndAmortization`, `restated.CapitalExpenditures`. **Caveat:** D&A and CapEx are NOT Restater-touched in Phase 4 (no current Restater touches them); `Restated().DepreciationAndAmortization == latestFinancialData.DepreciationAndAmortization` today. The migration is still correct (coherent with the rest of the EBITDA/FCF formula); future Restaters that touch D&A/CapEx (e.g. a hypothetical impairment-of-PPE rule) would automatically propagate.

#### 4.2.7 Working capital (service.go:1753, 1757, 1767, 1771)

**Current:**
```go
if latest.CurrentAssets <= 0 || latest.CurrentLiabilities <= 0 { return 0 }
latestNWC := latest.CurrentAssets - latest.CurrentLiabilities
// ... 
if prior.CurrentAssets <= 0 || prior.CurrentLiabilities <= 0 { return 0 }
priorNWC := prior.CurrentAssets - prior.CurrentLiabilities
```

**Migrated (cluster C-2):**
```go
latestView := cleaned.Restated()  // latest period — has views
priorView := buildPriorViewFromHistorical(prior)  // see below
if latestView.CurrentAssets <= 0 || latestView.CurrentLiabilities <= 0 { return 0 }
latestNWC := latestView.CurrentAssets - latestView.CurrentLiabilities
// ... 
if priorView.CurrentAssets <= 0 || priorView.CurrentLiabilities <= 0 { return 0 }
priorNWC := priorView.CurrentAssets - priorView.CurrentLiabilities
```

**Special challenge — prior period lacks a `CleanedFinancialData` wrapper.** The cleaner runs only on the latest period (`service.go:478`: `cleaningResult, err = s.dataCleaner.CleanFinancialData(ctx, latest)`). Prior periods are never cleaned — they're raw `historicalData.Data[priorPeriod]`. There are two options for Phase 4:

- **Option A (chosen): treat prior as AsReported only.** The `prior` *FinancialData has empty `AdjustmentLedger`/`Overlays`, so `cleaneddata.New(prior, prior).Restated()` reduces to identity (no adjusters fired, no recompute drift beyond Phase 0 plug fields). Build a one-shot `cleaneddata.New(prior, prior)` at the call site, read `.Restated().CurrentAssets` / `.CurrentLiabilities`. Bit-for-bit identical to today's `prior.CurrentAssets` / `CurrentLiabilities` (Phase 0 plug fields don't change umbrellas).
- **Option B (rejected): clean every historical period.** Multiplies cleaner CPU cost by 5-10x per request; no behavior change benefit (NWC change only uses two periods; the historical 5-year growth math reads raw revenue/OI, neither of which are Restater-touched today).

Option A is also forward-compatible: when a Restater that touches working-capital fields lands (Phase 5+), the prior-period clean can be added without rippling Phase 4's call site shape.

#### 4.2.8 `effectiveOI` helper (service.go:1634-1637)

**Current:**
```go
if fd.NormalizedOperatingIncome > 0 {
    return fd.NormalizedOperatingIncome
}
return fd.OperatingIncome
```

**Migrated (cluster C-2):** the helper takes a `*FinancialDataView` instead of `*FinancialData`. Call sites in `service.go:1479` (`if effectiveOI(latestFinancialData) > 0`) update to pass `cleaned.Restated()` (or, in alt-model paths, the same view). Helper body unchanged.

**Helper signature change:**
```go
// BEFORE
func effectiveOI(fd *entities.FinancialData) float64 { ... }
// AFTER
func effectiveOI(fd *cleaneddata.FinancialDataView) float64 { ... }
```

#### 4.2.9 Graham NCAV (graham.go:61, 64, 106-117)

**Current:**
```go
caps := fd.CurrentAssets / dilutedShares
// ...
ncav := (fd.CurrentAssets - totalLiabilities) / dilutedShares
// ...
if fd.TotalLiabilities > 0 { return fd.TotalLiabilities, true }
if fd.TotalAssets > 0 && fd.StockholdersEquity > 0 { derived := fd.TotalAssets - fd.StockholdersEquity; ... }
```

**Migrated (cluster C-5):**
```go
asReported := cleaned.AsReported()
caps := asReported.CurrentAssets / dilutedShares
// ...
ncav := (asReported.CurrentAssets - totalLiabilities) / dilutedShares
// ...
if asReported.TotalLiabilities > 0 { return asReported.TotalLiabilities, true }
if asReported.TotalAssets > 0 && asReported.StockholdersEquity > 0 { derived := asReported.TotalAssets - asReported.StockholdersEquity; ... }
```

**Rationale — AsReported, NOT Restated:** NCAV is intentionally a conservative as-filed metric (Graham's "two-thirds of NCAV" defensive buy floor). Using `Restated().CurrentAssets` would shift the floor upward for any company with goodwill exclusions (A1) or inventory writedowns (A5), which **defeats Graham's intent** — the metric is designed to ignore the analyst's restatement opinion and report what the filer said. The parent spec §"Consumer migration map" rows 11-13 specify AsReported; preserved.

**Caveat:** today, `AsReported().TotalLiabilities` for AMD/KO is 0 (T2-BS-3 parser dropout). Phase 4's Graham reader gets `AsReported.TotalLiabilities == 0` and triggers the existing derivation fallback (`AsReported.TotalAssets - AsReported.StockholdersEquity`). The Graham warning "graham_floor: derived total_liabilities from balance-sheet identity..." continues to fire for AMD/KO. This is **load-bearing data-quality signal** per CLAUDE.md "Common Gotchas" — do NOT silence it. (A future SEC parser fix or a deliberate Phase 5 decision to read `Restated().TotalLiabilities` here can revisit.)

#### 4.2.10 Currency conversion (currency.go:189-224)

**Current:** the FX-conversion site mutates `fd.CurrentAssets *= rate`, `fd.TotalAssets *= rate`, etc., across all monetary fields.

**Migrated (cluster C-5):** **NO CHANGE.** Currency conversion runs PRE-cleaner per Phase 0 invariant — the SEC parser stamps `ReportingCurrency`, `convertFinancialsToUSD` FX-converts all monetary fields, THEN `CleanFinancialData` runs. By the time `cleaned *CleanedFinancialData` exists, the raw `*FinancialData` is already in USD; both views inherit USD automatically. Currency.go's mutation site stays at `fd.X *= rate`. The parent spec §"Consumer migration map" row 14 explicitly documents this — "runs on AsReported pre-cleaner; all three views inherit USD automatically."

This is the single migration map row where Phase 4 deliberately does NOT migrate.

#### 4.2.11 DDM ROE + book-value-per-share (ddm.go:178, 181, 213, 291, 292)

**Current:**
```go
hasROE := latest.StockholdersEquity > 0 && latest.NetIncome > 0
// ...
roe = latest.NetIncome / latest.StockholdersEquity
// ...
bookValuePerShare := latest.StockholdersEquity / input.SharesOutstanding
```

**Migrated (cluster C-5) — SPECIAL BIT-FOR-BIT-PRESERVING PATH:** see §9 DDM migration sub-plan. The short version: DDM continues to read `latest` directly via the existing `*FinancialData` path (no change to `ddm.go`); the bit-for-bit invariant is preserved by NOT migrating DDM in Phase 4. Migration happens in Phase 5 ONLY after `Restated().StockholdersEquity` is proven bit-for-bit equal to `latest.StockholdersEquity` for JPM/BAC/WFC. Phase 4 documents this explicitly as a deferral; the consumer migration map row for DDM is reclassified as "deferred to Phase 5."

#### 4.2.12 Tangible value per share (service.go:1561-1578)

**Current:**
```go
tangibleEquity := financial.TangibleAssets
```

**Migrated (cluster C-5):** read `cleaned.Restated().TangibleAssets`. **Caveat:** today, `TangibleAssets` is parser-stamped (not Restater-touched); `Restated().TangibleAssets` equals `AsReported().TangibleAssets` for every current ticker. The migration is correct (coherent with future Restaters that touch intangibles); zero numeric drift today.

#### 4.2.13 ModelInput plumbing (service.go:1450-1467, alternative-model dispatcher)

**Current:** `runAlternativeModel` builds `*models.ModelInput` reading `latestFinancialData.InterestBearingDebt`, `latestFinancialData.CashAndCashEquivalents`. Subsequent reads in `revenue_multiple.go:108/203`, `ffo.go:331`, and `ddm.go:127/399` read `input.InterestBearingDebt` and `input.CashAndCashEquivalents` to compute equity-from-EV.

**Migrated (cluster C-3 for revenue_multiple/ffo; cluster C-5 deferral for DDM):**
- For **revenue_multiple + ffo**: `modelInput.InterestBearingDebt = restated.InterestBearingDebt` (Restated view). Alt-model EV→Equity bridges do NOT subtract `DebtLikeClaims` today (alt models don't compute a Damodaran-style enterprise value; they compute revenue/FFO multiples; B-rule overlays are out of scope for them in Phase 4).
- For **DDM**: `modelInput.InterestBearingDebt = latestFinancialData.InterestBearingDebt` (UNCHANGED — preserves bit-for-bit). See §9.

### 4.3 Plumbing: from `CleanFinancialData` to `CleanFinancialDataWithViews`

The single architectural change to the valuation service flow is at `service.go:478`:

**Current:**
```go
cleaningResult, err = s.dataCleaner.CleanFinancialData(ctx, latest)
// ...
historicalData.Data[latestPeriod] = cleaningResult.CleanedData
```

**Migrated (cluster C-1):**
```go
var cleaned *cleaneddata.CleanedFinancialData
cleaningResult, cleaned, err = s.dataCleaner.CleanFinancialDataWithViews(ctx, latest)
// ...
historicalData.Data[latestPeriod] = cleaningResult.CleanedData  // unchanged — legacy slot still populated for callers that read historicalData directly
```

The `cleaned` wrapper is then threaded as an additional parameter through `performValuation` and `runAlternativeModel`. The plumbing change is one commit (C-1) with NO consumer reads migrated yet — every existing `latestFinancialData.X` read continues to work. C-2..C-5 then migrate consumer clusters one at a time.

**Why thread `cleaned` rather than read it back from `historicalData`:** the `historicalData` map continues to hold `*FinancialData` (the existing `entities.HistoricalFinancialData` shape stays unchanged; modifying it would ripple through every test fixture in the repo). The `cleaned` wrapper is passed as a function parameter — explicit, grep-able, and unit-testable.

### 4.4 Dispatcher dual-write deletion

For each dispatcher dual-write site, the deletion happens atomically with the consumer cluster that depended on the mutated value:

| Adjuster | Field mutated today | Phase 4 commit that deletes the mutation | What replaces the read |
|---|---|---|---|
| A1 goodwill exclusion | `data.Goodwill = 0`, `data.TotalAssets -= originalGoodwill` (assets.go:1056-1057 + 1410-1411) | C-4 | `InvestedCapital().TotalAssets` (Goodwill=0; A1 overlay applied) for consumers that need the Damodaran view; `Restated().TotalAssets` for consumers that need post-impairment but goodwill-included |
| A2 intangible writedown | `data.OtherIntangibles -= writedownAmount`, `data.TotalAssets -= writedownAmount` (assets.go:1145, 1459) | C-2 | `Restated().OtherIntangibles` (LedgerEntry.DeltaAmount + recompute) |
| A4 DTA valuation allowance | `data.DeferredTaxAssets -= valuationAllowance`, `data.TotalAssets -= valuationAllowance` (assets.go:1557 + 1296) | C-2 | `Restated().DeferredTaxAssets` |
| A5 inventory writedown | `data.Inventory -= writedownAmount`, `data.TotalAssets -= writedownAmount` (assets.go:1219-1220, 1508) | C-2 | `Restated().Inventory`; TaxShieldDTA flows to `Restated().DeferredTaxAssets` |
| B1 operating lease cap | `data.TotalDebt += presentValue`, `data.InterestBearingDebt += presentValue` (liabilities.go:303 inline) | C-4 | `InvestedCapital().DebtLikeClaims` (B1 overlay applied); WACC `Restated().InterestBearingDebt` excludes B1 |
| B2 pension underfunding | `data.TotalDebt += totalPensionObligation`, `data.InterestBearingDebt += totalPensionObligation` | C-4 | `InvestedCapital().DebtLikeClaims`; same WACC exclusion |
| B3 contingent liabilities | `data.TotalDebt += amount` (TODAY — Phase 2 routing intent preserved) | C-4 | `InvestedCapital().DebtLikeClaims` (B3 overlay `Field:"DebtLikeClaims"` realized) |
| C1 restructuring | `data.NormalizedOperatingIncome += restructuringAmount` (earnings.go:1615) | C-3 | `Restated().NormalizedOperatingIncome` |
| C2 asset sale gains | `data.NormalizedOperatingIncome -= AssetSaleGains` (earnings.go:1657, 1407) | C-3 | `Restated().NormalizedOperatingIncome` |
| C3 litigation settlements | `data.NormalizedOperatingIncome += LitigationSettlements` (earnings.go:1717, 1427) | C-3 | `Restated().NormalizedOperatingIncome` |
| C5 derivative gains/losses | `data.NormalizedOperatingIncome -= adjustmentAmount` (earnings.go:1816-1819) | C-3 | `Restated().NormalizedOperatingIncome` |
| C6 capitalized interest | `data.InterestExpense += CapitalizedInterest` (earnings.go:1863, 1524) | C-3 | `Restated().InterestExpense` |
| C4 stock compensation | FLAG-ONLY (no mutation) | — | (no migration needed) |
| C7 (review) | FLAG-ONLY (no mutation) | — | (no migration needed) |

After C-5 lands, the dispatcher `Process{Asset,Liability,Earnings}Adjustments` switch arms hold ONLY:
1. The call to the `Apply*` method (mutation-free; returns `AdjusterOutput`).
2. The drain of `AdjusterOutput.LedgerEntries` / `Overlays` / `Flags` into the result's `Native*` slices.
3. The legacy `*AdjustmentResult` translator (per-rule helper).

The per-rule translators become vestigial — they exist solely to keep `LiabilityAdjustmentResult` / `AssetAdjustmentResult` / `EarningsAdjustmentResult` structs populated for the legacy result-shape contract. Phase 4 C-5 also deletes these translators AND the legacy `*AdjustmentResult` structs IF and only if grep confirms no remaining callers. (If the legacy structs are still referenced by tests or external observability, defer deletion to Phase 5.)

### 4.5 `CalculationVersion` bump (4.2 → 4.3)

**When:** atomic with cluster C-4 (the most numerically-consequential commit — B3 routing flip + B1/B2 reroute + A1 overlay flip for the WACC + bridge consumer). Before C-4, the dual-writes for B-rules and A1 still drive `Restated()` consumers identically to pre-Phase-4; the bump would be premature. After C-4, every consumer-visible monetary surface that depends on B-rules or A1 shifts.

**What changes for operators:**
- Bundles produced post-Phase-4 carry `calculation_version: "4.3"` in `17-response.json`.
- Replay tooling treats `4.2` bundles as "pre-DC-1-Phase-4" baselines and `4.3` bundles as "post-DC-1-Phase-4" current. The replay diff between them is the documented numeric drift table (§6).
- Operators auditing a fair-value endpoint that returned `4.2` historically may see different values in `4.3` for contingent-liability tickers — flag as expected via the changelog.

**No `SchemaVersion["FinancialData"]` bump in Phase 4.** Phase 3 bumped 8→9 atomic with Q2/Q4 first-populating commits. Phase 4 does NOT add new `omitempty` fields to `FinancialData`; the bump rule (`feedback_schema_version_atomic_bump` MEMORY) doesn't fire. `SchemaVersion["ValuationResult"]` stays at 2 (no new omitempty fields on the result, either — `DebtLikeClaims` is internal to `cleaneddata.FinancialDataView`, which is not persisted).

---

## 5. Replay drift expectation

The replay basket (`artifacts/tier2-baseline/2026-05-19/` — 10 tickers: AAPL, AMD, BABA, EQIX, F, JNJ, KO, MSFT, MXL, TSM; JPM is exercised only by `TestDDM_LegacyPath_BitForBit`) splits into three classes:

### 5.1 Class I — Zero-Restater-fire tickers → ZERO numeric drift expected

For any ticker where NO Restater (A2, A4, A5, C1, C2, C3, C5, C6) fires AND no B-rule overlay (B1, B2, B3) fires AND A1 goodwill exclusion doesn't fire, every consumer migration is a no-op:
- `Restated().X == AsReported().X == latestFinancialData.X` for every Restater-touched field (no Restater fired).
- `InvestedCapital().DebtLikeClaims == 0` (no overlay fired).
- `InvestedCapital().TotalAssets == Restated().TotalAssets` (no A1 fired).

Expected: `17-response.json` byte-identical. (Modulo `CalculationVersion: "4.2" → "4.3"` field text — that's structural drift, not numeric.)

**Tickers in this class:** verified via the existing `internal/integration/testdata/recompute-shadow/` snapshots — tickers with empty `recent_adjusters` in the shadow log. Based on Phase 1 shadow analysis: most of the basket lands here (AAPL, MSFT, JNJ, F, MXL, EQIX, JPM). The Phase 4 implementer plan re-verifies on PR open.

### 5.2 Class II — Restater-firing, B-rule-quiet tickers → drift only on Restater-touched fields

Tickers where Restaters fire but no B-rule overlays fire (and no A1 goodwill exclusion). Drift surfaces on:
- ROIC (`Restated().NormalizedOperatingIncome` × `Restated().StockholdersEquity` + `Restated().InterestBearingDebt`).
- Cross-check ImpliedPE / ImpliedPFCF (Restated NetIncome / OperatingIncome / D&A).
- Tangible value per share IF a Restater touches `TangibleAssets` (none today; expected zero drift on this field even in Class II).
- Sentinel guards (NOPAT-fallback, model-not-applicable) read different OI; the guard outcome changes only if the restated OI crosses zero (rare).

**Tickers in this class:** AMD, KO (T2-BS-3 carve-out — `TotalLiabilities` Restated reconstruction now flows into Graham's derivation fallback for AMD/KO **only if Phase 4 migrates Graham to `Restated()`**, which it doesn't — Graham stays on `AsReported()`. So AMD/KO drift surfaces only on `Restated().NormalizedOperatingIncome` if any C-rule fires for them, which is currently zero per the Phase 1 shadow analysis). Expected drift on AMD/KO: zero in `17-response.json` (the Restated reconstruction is internal to the view; Phase 4 doesn't surface it to a consumer that wasn't reading liabilities from it before).

### 5.3 Class III — B-rule-firing tickers → WACC + EV-bridge drift expected (the substantive correction)

Tickers with B1 lease capitalization, B2 pension underfunding, OR B3 contingent liabilities firing. Drift surfaces on:
- WACC (cost-of-debt weight changes because `Restated().InterestBearingDebt` no longer includes B-rule amounts).
- `EnterpriseValue` if WACC changes (mostly invariant — WACC weight shifts are typically <50bps).
- `EquityValue` (B-rule amounts subtracted via `InvestedCapital().DebtLikeClaims`).
- `DCFValuePerShare` (`EquityValue / sharesOutstanding`).

**Magnitude estimate:** for a ticker with $500M B3 contingent liability and a $1.5B enterprise value, the equity bridge subtracts an additional $500M → equity value drops ~33% → DCF per share drops ~33%. The change is **directionally correct** — contingent liabilities ARE claims against shareholders. Pre-Phase-4 the same $500M was being added to capital-structure debt (distorting WACC) AND missing from the equity bridge subtraction (undercounting claims). Phase 4 corrects both.

**Tickers in this class within the basket:** TSM (potential — pharma-like contingencies sometimes surface for ADRs); BABA (regulatory contingencies); KO (litigation reserves). Re-verify on PR open via the shadow snapshots' overlay-fire signals.

### 5.4 Class IV — A1 goodwill-firing tickers

A1 goodwill exclusion fires when `goodwill_ratio > 5%`. Effect: `InvestedCapital().TotalAssets` drops by the goodwill amount, `InvestedCapital().Goodwill = 0`. Today the dispatcher dual-write produces the same drop on `data.TotalAssets`, so `latestFinancialData.TotalAssets` (read by the cross-check ImpliedEV path) already reflects the exclusion. Phase 4 deletes the dual-write; the cross-check reads `Restated().TotalAssets` (which is NOT goodwill-excluded — see §4.2.7 / parent spec convention). Net effect: the cross-check `ImpliedEV / Total Assets` ratio changes for A1-firing tickers.

**Magnitude:** typically <20% drift on the cross-check ratio; downstream `SanityCheck.Flags` may shift from "in range" to "above sector median" or vice versa for borderline tickers. Re-verify on PR open.

---

## 6. SchemaVersion decision

**No `SchemaVersion["FinancialData"]` bump.** Phase 4 adds zero new `omitempty` fields to `entities.FinancialData`; the SchemaVersion-atomic-bump rule (MEMORY: `feedback_schema_version_atomic_bump`) doesn't fire.

**No `SchemaVersion["ValuationResult"]` bump.** Phase 4 adds zero new `omitempty` fields to `entities.ValuationResult` (`DebtLikeClaims` is internal to `cleaneddata.FinancialDataView`, not persisted to the result).

**`CalculationVersion` bump 4.2 → 4.3.** See §4.5. This is the consumer-visible version field that already exists in `ValuationResult.CalculationVersion`; Phase 4 changes only the value, not the schema.

The replay `--allow-schema-drift` flag is NOT needed for Phase 4 — there is no structural drift in `10-clean-output.json`, `13-cleaner-audit.json`, or `15-valuation.json`. The drift is purely numeric on `15-valuation.json` + `17-response.json`.

---

## 7. DDM bit-for-bit preservation strategy — THE focal architectural decision

`TestDDM_LegacyPath_BitForBit` asserts `math.Float64bits` equality on JPM/BAC/WFC `IntrinsicValuePerShare` / `EquityValue` / `EnterpriseValue` post-Tier-2 (pinned at master `0324057`). DDM today reads `latest.StockholdersEquity` / `latest.NetIncome` / `latest.DividendsPerShare` etc. via `historicalData.GetLatestPeriod()` — the `*FinancialData` that flows in via `runAlternativeModel`'s `modelInput.HistoricalData` slot.

After Phase 4's dispatcher dual-write deletion, **`latest.StockholdersEquity` for JPM/BAC/WFC may no longer be byte-identical** to its pre-Phase-4 value if any Restater fires for these tickers. The risk:

- JPM/BAC/WFC are large banks classified as FIN_BANK → `fin_generic` archetype → `mature_large_bank` profile → legacy-Gordon path.
- Per Phase 1 shadow analysis, no asset-side Restater (A2/A4/A5) fires for JPM/BAC/WFC in the test fixtures.
- BUT: `latest.StockholdersEquity` is read by `latest.NetIncome / latest.StockholdersEquity` in the ROE check. If ANY future Restater touches StockholdersEquity (via EquityOffset summed onto Restated.StockholdersEquity), and the consumer migration switches DDM to `Restated().StockholdersEquity`, the bit-for-bit invariant breaks.

### 7.1 Decision: DEFER DDM migration to Phase 5

Phase 4 does NOT migrate DDM consumer reads. The 5 read sites in `ddm.go` (lines 178, 181, 213, 291, 292) continue to read `latest.StockholdersEquity` / `latest.NetIncome` via `*FinancialData`. The bit-for-bit invariant is trivially preserved because the DDM code path is untouched.

**Why this is the right call:**
1. The cross-Tier-2 contract is non-negotiable. The CLAUDE.md gotcha is explicit: "Any change to mature-large-bank DDM math that fails this test must be REVERTED — do NOT update the goldens to make it pass."
2. Phase 4's consumer migration is independent of DDM's read path — the alt-model `modelInput.HistoricalData` slot stays as `*HistoricalFinancialData`; DDM's reads via `GetLatestPeriod()` are unaffected by the dispatcher dual-write deletion ONLY IF the entity returned by `GetLatestPeriod()` remains bit-for-bit equal to its pre-Phase-4 state.
3. After C-4 lands, `data.TotalDebt` for B-rule-firing tickers will no longer include B1+B2+B3 amounts. But JPM/BAC/WFC are banks — their B-rule overlays are typically B3 (litigation) only; B1 leases are tiny for banks; B2 pension underfunding is small relative to interest-bearing debt. The dispatcher mutation of `data.TotalDebt += B3-amount` was small for JPM/BAC/WFC pre-Phase-4 AND the DDM math doesn't use `data.TotalDebt` (DDM uses `latest.InterestBearingDebt` only in the EV-from-equity-value bridge at `ddm.go:127`).
4. The Phase 1 shadow snapshots show empty `recent_adjusters` for JPM/BAC/WFC in the test fixtures → no Restaters fire → `Restated().X == AsReported().X == latest.X` today. The bit-for-bit invariant would hold even if DDM migrated.
5. **But we cannot rely on (4) staying true forever.** A future Restater or a future test-fixture refresh could change the empirical state. Deferring is the safer architectural choice: Phase 5 owns DDM migration AFTER a deliberate `TestDDM_LegacyPath_BitForBit` re-proof step.

### 7.2 Phase 4 DDM gate-test: explicit no-op verification

Phase 4 adds a NEW test `TestDDM_ConsumerPath_UnaffectedByPhase4` that exercises the JPM/BAC/WFC test fixtures end-to-end through the alt-model dispatcher and asserts the DDM output equals the pre-Phase-4 golden bit-for-bit. This is a SUPERSET assertion of `TestDDM_LegacyPath_BitForBit`: it pins not only the DDM math but also that the upstream `modelInput.HistoricalData` field values arriving at DDM are unchanged.

If this test fails, Phase 4 has accidentally rippled into DDM's input — the failure diagnoses which migration commit introduced it via bisect.

### 7.3 Phase 4 → Phase 5 DDM follow-up

The Phase 4 closeout document filed at ship time will include a §"DDM migration deferred to Phase 5" section enumerating the steps Phase 5 must take:

1. Re-run shadow snapshots on JPM/BAC/WFC with the current test fixtures; verify `Restated().X == AsReported().X` for `StockholdersEquity`, `NetIncome`, `DividendsPerShare`.
2. Add a temporary parallel-write test: `TestDDM_RestatedView_BitForBit` that runs DDM with `Restated()`-sourced reads against the same goldens. Verify GREEN.
3. ONLY THEN migrate `ddm.go:178/181/213/291/292` to `Restated()` reads.
4. Delete the temporary parallel-write test; `TestDDM_LegacyPath_BitForBit` continues to guard the math.

The Phase 5 DDM migration is small (5 read sites, all in one file); deferring it doesn't slip Phase 5's schedule.

---

## 8. Testing strategy

### 8.1 Load-bearing invariants — must stay GREEN at every Phase 4 commit

| Invariant | Path | Why load-bearing in Phase 4 |
|---|---|---|
| `TestDDM_LegacyPath_BitForBit` | `internal/services/valuation/models/ddm_bitforbit_test.go` | Cross-Tier-2 contract. DDM is deferred per §7; trivially preserved. |
| **NEW `TestDDM_ConsumerPath_UnaffectedByPhase4`** | NEW: `internal/services/valuation/models/ddm_phase4_invariance_test.go` | Verifies DDM's upstream inputs are unchanged through all 5 Phase 4 commit clusters. |
| `TestRecomputeUmbrellas_NoMutation` | `internal/services/datacleaner/recompute_test.go` | Recompute shim is untouched; Phase 4 does not modify `recompute.go`. |
| `TestOrchestrator_LedgerOrdering` | `internal/services/datacleaner/ledger_invariants_test.go` | Asset → liability → earnings partition. Dispatcher signature stays the same; only dual-write lines deleted. |
| Shadow snapshots byte-identical | `internal/integration/testdata/recompute-shadow/<TICKER>.json` (`git diff --quiet` exits 0) | Cleaner-side adjuster execution unchanged; shadow snapshots stay byte-identical at every commit. |
| `TestLedger_BasketSnapshot_ClusterPrediction` (10/10) | `internal/integration/datacleaner_ledger_basket_test.go` | Per-ticker expected AdjusterID sets unchanged. |
| `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` | `internal/integration/datacleaner_ledger_basket_test.go` | AMD $9.679B / KO $60.912B Restated reconstruction. Phase 4 doesn't migrate Graham to Restated; reconstruction stays internal. |
| `TestQ2_A2TaxShieldDTA_Populated` | `internal/services/datacleaner/adjustments/a2_intangible_adjuster_test.go` | A2 TaxShieldDTA contract preserved. |
| `TestQ4_AIProvenance_SHA256_Deterministic` | `internal/services/datacleaner/adjustments/liabilities_test.go` (or sibling) | B3 AI hashes unchanged. |
| `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` | `internal/services/datacleaner/service_cleanwithviews_no_double_count_test.go` | HIGH-1 regression. Phase 4's dispatcher dual-write deletion CHANGES the post-clean entity (some fields stop being dual-written) — the test must continue to pass under the new state. |
| `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnEarningsFire` | same file | Same as above for C-rules. |
| `TestCleanedFinancialData_Restated_C6EquityOffsetZero` | `internal/services/datacleaner/cleaneddata/restate_test.go` | C6 capitalized-interest stays out of equity in Restated. Phase 4 doesn't change `restate.go`. |
| `TestIdentityCopy_CoversEveryViewField` | `internal/services/datacleaner/cleaneddata/identitycopy_fields_test.go` | If Phase 4 adds fields to FinancialDataView, this catches missing copy lines. |
| Full `go test ./... -count=1` | (full suite) | GREEN at every commit. |

### 8.2 HIGH-1 regression test under Phase 4

The HIGH-1 pin (`TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire`) was designed to catch the pre-followup double-count where dispatcher dual-write + ledger reducer both applied the same delta. The Phase 4 dispatcher dual-write deletion REMOVES the dual-write source of one delta — the ledger reducer still applies `EquityOffset + TaxShieldDTA`, but the component-field delta is now applied... by **whom?**

Looking at the followup's `restate.go` reducer:
```go
v := identityCopy(c.restated)  // seeds from POST-CLEAN entity
// for each fired ledger entry: v.StockholdersEquity += e.EquityOffset; v.DeferredTaxAssets += e.TaxShieldDTA
// (NO DeltaAmount re-application)
// Then umbrella recompute from v's components.
```

The seed reads `c.restated.OtherIntangibles` — which is the POST-CLEAN entity's value. With dual-write in place (today): `c.restated.OtherIntangibles = original - writedown` (dispatcher mutated it). With dual-write DELETED (Phase 4 C-2): `c.restated.OtherIntangibles = original` (dispatcher no longer mutates it). The Restated view's `OtherIntangibles` would equal `original`, NOT `original - writedown`. **The HIGH-1 pin would fail under Phase 4 because the post-clean entity itself has changed.**

**This is the most subtle invariant Phase 4 must handle.** Two options:

#### 8.2.1 Option A (CHOSEN): The Adjuster's `Apply*` method, paired with the dispatcher's drain, must write the restated component value to `working` (the post-clean entity) ITSELF — not just emit a LedgerEntry that the reducer will apply later

Today, the dispatcher pattern is:
```go
// In Process{Asset,Liability,Earnings}Adjustments switch arm:
output := adjuster.Apply(working, cleaningCtx)  // mutation-free; returns AdjusterOutput
// ...drain LedgerEntries/Overlays/Flags into result...
data.X -= writedown  // DISPATCHER DUAL-WRITE — to be deleted in Phase 4
```

After Phase 4 C-2, the dual-write is deleted. But the Restater interface's contract becomes: **the dispatcher MUST apply the LedgerEntry's `Component`/`DeltaAmount` to `working.X` before returning.** This is the cleanest way to preserve the followup's reducer contract (which seeds from `c.restated.X` and assumes the dispatcher already applied the delta).

Concretely, Phase 4 C-2 replaces:
```go
// BEFORE
data.OtherIntangibles -= writedownAmount  // dispatcher dual-write
data.TotalAssets -= writedownAmount       // dispatcher dual-write
// ...drain natives...
```

with a generic helper that reads the `AdjusterOutput.LedgerEntries`, applies `DeltaAmount` to the named Component via reflection (or a small switch), and drains natives. The umbrella `TotalAssets` recompute is no longer dispatcher-side — it happens implicitly in `Restated()`'s umbrella reducer at the view level. (The recompute shim `recomputeUmbrellas` continues to fire as today; it's a no-op observer.)

**The contract change is**:
- BEFORE Phase 4: dispatcher dual-writes mutate `data.Component` AND `data.TotalUmbrella`.
- AFTER Phase 4: dispatcher applies LedgerEntry deltas to `data.Component` ONLY (no `data.TotalUmbrella` mutation). The umbrella values on the post-clean `*FinancialData` may now be INCOHERENT relative to its components (e.g., `data.OtherIntangibles = 70` after A2 fires with writedown=30, but `data.TotalAssets` still = pre-A2 value). This is acceptable because no Phase 4 consumer reads `data.TotalUmbrella` directly — they read `Restated().TotalUmbrella` which recomputes from components.

**Implication for the post-clean entity:** the legacy `historicalData.Data[latestPeriod] = cleaningResult.CleanedData` slot continues to be populated, but its umbrella values may be incoherent. Phase 4 documents this in `CLAUDE.md` as a known transitional state; Phase 5 (alongside `Raw()` deletion) can stop populating the legacy slot entirely if no consumer reads it.

**This option keeps the followup's reducer contract intact** and preserves the HIGH-1 pin's assertion shape.

#### 8.2.2 Option B (REJECTED): Restate `restate.go` to re-apply `LedgerEntry.DeltaAmount`

Re-introduce the per-component switch in the reducer. The reducer seeds from `AsReported` snapshot (NOT `restated`) and applies BOTH `DeltaAmount` to components AND `EquityOffset` + `TaxShieldDTA` to equity/DTA. Then the seed-vs-applied state is well-defined.

**Why rejected:**
1. The followup explicitly deleted `applyLedgerEntryToView` per HIGH-1 fix (followup spec §4.1 "applyLedgerEntryToView is **deleted**. The per-component switch existed solely to route `DeltaAmount` to the correct field, which is precisely the work we no longer do."). Reintroducing it walks back a HIGH severity fix.
2. The followup's chosen path uses the POST-CLEAN entity as the seed precisely because the dispatcher applied the delta. Option B requires CHANGING the seed to the pre-clean snapshot, which is a larger refactor than Option A.
3. Option A's contract change (dispatcher applies component delta only; umbrella recompute moves to view) is closer to the parent spec's "post-Phase-4 cleaner is non-mutating" end state. Phase 5 then completes the transition.

### 8.3 New tests required for Phase 4

1. **`TestPerformValuation_RestatedReadsAtROIC`** — exercises the full valuation pipeline on a synthetic fixture firing A2; asserts the ROIC computed in `service.go:604` uses `Restated().StockholdersEquity`. (Cluster C-2 invariant.)
2. **`TestPerformValuation_CrossCheckReadsRestated`** — fires C1 restructuring; asserts the cross-check ImpliedPE uses `Restated().NetIncome`. (Cluster C-3 invariant.)
3. **`TestPerformValuation_WACCUnaffectedByB3`** — fires B3 contingent liability with $1B amount; asserts WACC weight is unchanged versus the no-B3 case. **The defining B3 routing flip pin.** (Cluster C-4 invariant; the parent spec §"T3" exact test.)
4. **`TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims`** — fires B3 with $1B amount; asserts `result.EquityValue == EV - debt + cash - minority - preferred - 1_000_000_000`. (Cluster C-4 invariant; companion to #3.)
5. **`TestPerformValuation_DDMUntouched`** — runs JPM fixture through alt-model dispatcher; asserts intermediate `modelInput.HistoricalData.Data[latest].StockholdersEquity / NetIncome / DividendsPerShare` are byte-equal to pre-Phase-4 captures. (§7 DDM-deferral invariant.)
6. **`TestPerformValuation_GrahamUsesAsReported`** — fires A1 goodwill exclusion; asserts `result.CurrentAssetsPerShare` reflects the as-filed (NOT goodwill-excluded) CurrentAssets. (Cluster C-5 invariant.)
7. **`TestCalculationVersion_IsV43`** — verifies any post-C-4 `ValuationResult` carries `CalculationVersion: "4.3"`. (Cluster C-4 invariant.)
8. **`TestDispatcherDualWriteDeleted_Assets`** — fires A1; asserts the post-clean `data.TotalAssets` is UNCHANGED from pre-clean (dispatcher no longer mutates umbrella). Companion tests for B-rules and C-rules. (Phase 4 contract change pin.)

### 8.4 Replay regression methodology

For each commit cluster:

1. Before commit: `go run ./cmd/replay --diff-stages --workers=4 artifacts/tier2-baseline/2026-05-19/` and capture the per-ticker numeric diff.
2. Compare against the documented drift expectation (§5):
   - Cluster C-1: ZERO drift expected (plumbing only).
   - Cluster C-2: zero drift for non-Restater-firing tickers; ROIC drift only for Restater-firers (none in the basket today per Phase 1 shadow analysis).
   - Cluster C-3: same as C-2 plus cross-check drift; expected zero in the current basket.
   - Cluster C-4: WACC + bridge drift for B-rule-firers (TSM/BABA/KO possible; re-verify). `CalculationVersion: "4.3"` field change.
   - Cluster C-5: Graham drift for A1-firers; tangible-value-per-share unchanged today; currency.go unchanged.
3. Any UNEXPECTED drift (i.e., a ticker shifts where the §5 expectation said it shouldn't) requires a STOP-AND-INVESTIGATE pause before the next commit.
4. At PR ship time, refresh the `artifacts/tier2-baseline/` directory with the post-Phase-4 bundle output (new date subdirectory; previous baseline preserved for cross-Tier-2 comparison).

---

## 9. DDM migration sub-plan — most consequential subsection

The architectural decision is documented in §7. This subsection is the BACKEND-actionable plan.

### 9.1 What Phase 4 does to DDM: NOTHING beyond plumbing

- `ddm.go` is NOT modified in Phase 4.
- The 5 read sites at lines 178, 181, 213, 291, 292 continue to read `latest.StockholdersEquity` / `latest.NetIncome` / `latest.DividendsPerShare` via the `*FinancialData` returned by `input.HistoricalData.GetLatestPeriod()`.
- The `modelInput.HistoricalData` slot in `runAlternativeModel` (service.go:1451) continues to carry the same `*HistoricalFinancialData` it does today (`historicalData`, which has `Data[latestPeriod] = cleaningResult.CleanedData` after the cleaner runs).

### 9.2 What changes UNDER DDM that could break bit-for-bit

When cluster C-2 / C-3 / C-4 land, the dispatcher dual-writes are deleted. The `cleaningResult.CleanedData` returned to `service.go:478` no longer has dispatcher-mutated umbrellas (per §8.2.1 Option A). Specifically:
- `data.OtherIntangibles` is now applied by the dispatcher's component-delta apply step (per Option A).
- `data.TotalAssets` is NOT mutated by the dispatcher post-Phase-4.

For JPM/BAC/WFC, the existing test fixtures show no asset-side Restaters fire (Phase 1 shadow analysis: empty `recent_adjusters`). So:
- `data.OtherIntangibles` is unchanged (no Restater touched it).
- `data.TotalAssets` is unchanged (no Restater touched it, no A1 fired).
- `data.StockholdersEquity` is unchanged (no EquityOffset accumulated — no Restater fired).
- `data.NetIncome` is unchanged (no C-rule fired).

**Conclusion: DDM's reads through `latest.X` continue to return byte-identical values post-Phase-4.** The `TestDDM_LegacyPath_BitForBit` test stays GREEN.

### 9.3 The verification step Phase 4 BACKEND must run

For each commit C-1 through C-5:

1. `go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1` → GREEN.
2. `go test ./internal/services/valuation/models/ -run TestDDM_ConsumerPath_UnaffectedByPhase4 -count=1` → GREEN.

If either fails, the commit is reverted. No "update the golden" remedy is permitted (per CLAUDE.md gotcha).

### 9.4 Phase 5 DDM migration (FUTURE — not Phase 4 scope)

When Phase 5 begins:

1. Re-run the verification at the commit-base (Phase 4 ship sha) to confirm baseline bit-for-bit.
2. Migrate `ddm.go:178/181/213/291/292` to read `cleaned.Restated().StockholdersEquity` / `.NetIncome` / `.DividendsPerShare` (DDM gains a `*CleanedFinancialData` parameter via `ModelInput`).
3. Re-run `TestDDM_LegacyPath_BitForBit` → GREEN (because Restated == AsReported == latest for JPM/BAC/WFC when no Restater fires).
4. Re-run `TestDDM_ConsumerPath_UnaffectedByPhase4` → still GREEN (the pin asserts INPUT byte-equality; outputs come from the same math).
5. Phase 5 ARCH document the migration; Phase 4 BACKEND is OUT OF SCOPE for it.

---

## 10. Open questions

None blocking. Three non-blocking items for HUMAN awareness, with explicit dispositions:

1. **Future B-rule expansion (B4 environmental reserves, B5 unfunded operating commitments).** Out of Phase 4 scope. Each future B-rule would be a Phase-N addition that follows the same `OverlayEmitter` → `InvestedCapital().DebtLikeClaims` routing. No Phase 4 architectural change required.

2. **`WACCInputs.IncludeDebtLikeClaimsInCapitalStructure` opt-in knob.** The parent spec §"Open questions" item 4 contemplates this. Phase 4 does NOT add it; the routing flip's whole point is to KEEP DebtLikeClaims out of capital structure. If a future user explicitly wants a ROIC-conservative analysis that re-includes them, a config-knob can be added in Phase 6+ without architectural change.

3. **Legacy `cleaningResult.CleanedData` slot (continued population in `historicalData`).** Phase 4 continues to populate `historicalData.Data[latestPeriod] = cleaningResult.CleanedData` (service.go:488). After Phase 4, this slot is no longer the primary read path for the migrated consumers — they read views via `cleaned`. The slot stays populated for two reasons: (a) DDM still reads via `GetLatestPeriod()` (deferral per §7); (b) currency.go pre-cleaner mutates the entity and the same pointer flows into the cleaner. Phase 5 can re-evaluate after DDM migrates.

---

## 11. PR strategy

**Single PR, 5 commit clusters.** The 5-cluster split is justified in §4.1. Branch: `dc1-phase-4` from master (post the followup merge at `3490227`). Branch base ref: `master` at `3490227` or later.

Why single PR rather than 5 PRs:

1. The dispatcher dual-write deletion (Option A in §8.2.1) introduces a temporary "umbrella values may be incoherent on `data.X`" state. Splitting into 5 PRs would mean 4 intermediate master states with this incoherence — REVIEWER pain and operational risk. A single PR keeps the incoherent state confined to the branch.
2. The `CalculationVersion` bump (cluster C-4) is consumer-visible. Shipping it without C-5's tail (Graham + tangible + currency confirmation) leaves operators in a confused state if a `4.3` value gets queried via cache during the inter-PR window.
3. The 5 clusters are tightly coupled by the migration map; each cluster's correctness depends on the prior cluster's accessor wiring. A failed C-3 PR would force a revert of C-1+C-2 — 5x the rollback complexity.

The PR's commits are independently reviewable (each commit cluster's diff scope is enumerated in §4.1); a REVIEWER can review-by-commit using `git log --oneline` and `git show <sha>`.

**Fallback Option B (NOT recommended):** if the C-4 commit's review surfaces design changes, split out C-4 + C-5 into a separate PR after C-1 + C-2 + C-3 merge. C-4 is the highest-risk cluster (B3 routing flip drift); a separate PR has a cleaner blast radius. The implementer plan documents the fallback.

---

## 12. Phase 4 → Phase 5 gate

Phase 5 starts only after **all** of:

1. All Phase 4 invariants GREEN (§8.1).
2. `TestPerformValuation_WACCUnaffectedByB3` + `TestPerformValuation_EquityBridgeSubtractsDebtLikeClaims` GREEN — confirms B3 routing flip is live.
3. `TestDDM_LegacyPath_BitForBit` + `TestDDM_ConsumerPath_UnaffectedByPhase4` GREEN — confirms DDM unaffected.
4. Replay diff on the basket matches the §5 drift expectation (Class I tickers byte-identical; Class III tickers show expected B3 routing drift).
5. `artifacts/tier2-baseline/` refreshed with post-Phase-4 bundles (new date subdirectory).
6. Phase 4 closeout doc filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md` documenting: (a) per-cluster commit SHAs, (b) per-ticker drift table, (c) HIGH-1 pin status, (d) DDM-deferral handoff to Phase 5.

Phase 5 scope (per parent spec §"Phasing & implementation sequence"):
- DDM consumer migration (`ddm.go`:178/181/213/291/292; see §7.3, §9.4).
- `cleaneddata.CleanedFinancialData.Raw()` deletion (zero remaining callers after Phase 4).
- Optional: `sync.Once`-protected accessors if a parallel-read consumer materializes.
- Optional: stop populating `historicalData.Data[latestPeriod] = cleaningResult.CleanedData` if DDM migration removes the last consumer.
- Optional: legacy `{Asset,Liability,Earnings}AdjustmentResult` struct deletion if zero remaining callers.

---

## 13. Acceptance criteria (checklist for BACKEND self-validation)

- [ ] This spec lands at `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-phase-4-spec.md`
- [ ] Implementer plan filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md`
- [ ] Parent spec's "Phasing & implementation sequence" Phase 4 row updated to reference this spec + plan
- [ ] `s.dataCleaner.CleanFinancialDataWithViews(ctx, latest)` is called at `service.go` (replaces `CleanFinancialData`)
- [ ] All 12 migration map rows from §4.2.1 through §4.2.12 land in cluster commits (DDM row §4.2.11 deferred per §7)
- [ ] Cluster C-1 through C-5 each have ≥1 commit on the branch with a clear scope
- [ ] All dispatcher dual-write sites in §4.4's table are DELETED in the cluster commit indicated; `grep -n 'data\.\(TotalDebt\|TotalAssets\|Goodwill\|OtherIntangibles\|Inventory\|DeferredTaxAssets\|NormalizedOperatingIncome\|InterestExpense\)\s*[+-]\?=' internal/services/datacleaner/adjustments/` returns the test-fixture-only matches expected
- [ ] Per-rule translator helpers + legacy `{Asset,Liability,Earnings}AdjustmentResult` structs deleted (or deferred to Phase 5 with rationale documented if callers remain)
- [ ] `CalculationVersion` is `"4.3"` everywhere it's stamped (both DCF and alt-model paths)
- [ ] `TestDDM_LegacyPath_BitForBit` GREEN at every Phase 4 commit
- [ ] `TestDDM_ConsumerPath_UnaffectedByPhase4` GREEN at every Phase 4 commit
- [ ] `TestCleanFinancialDataWithViews_Restated_NoDoubleCount_OnRestaterFire` GREEN under Phase 4 (with the dispatcher contract change per §8.2.1 Option A)
- [ ] Shadow snapshots byte-identical: `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0 at every commit
- [ ] `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` GREEN (AMD $9.679B / KO $60.912B)
- [ ] New Phase 4 tests from §8.3 GREEN
- [ ] Full `go test ./... -count=1` exit 0 at every commit
- [ ] Replay diff on `artifacts/tier2-baseline/2026-05-19/` matches §5 drift expectation per ticker
- [ ] `artifacts/tier2-baseline/<post-phase-4-date>/` baseline refreshed at PR ship sha
- [ ] Phase 4 closeout doc filed at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md`
- [ ] CLAUDE.md / AGENTS.md row 17b / THESIS DC-1 row / DC-1 reviewer tracker / parent spec changelog all updated

---

## 14. Change log

| Date | Change |
|---|---|
| 2026-05-27 | Phase 4 IMPLEMENTED on branch `dc1-phase-4` (commits C-1 `f65604a`, C-2 `4f8a06c`, C-3 `9bc885e`, C-4 `7349a1e`, C-5 docs/closeout). Implementation deviations from this spec, all documented in the closeout: (a) Graham migrated in C-2 not C-5 (co-mutation hazard — `graham.go` reads `data.TotalAssets` directly); (b) tangible value reads `AsReported()` not `Restated()` — §4.2.12's premise that `Restated().TangibleAssets` is identity is INCORRECT (it recomputes from components, which would drift `tangible_value_per_share`); (c) `CashAndCashEquivalents` not migrated (not a `FinancialDataView` field); (d) §8.1 shadow-byte-identical invariant RELAXED — Option A's umbrella-dual-write deletion makes the unchanged `recomputeUmbrellas` observe a different post-clean `TotalAssets`, so shadow snapshots were intentionally regenerated (all SEMANTIC invariants GREEN); (e) replay verification (§5/§8.4) DEFERRED to operator — the `2026-05-19` baseline is `calc_version 4.1`, confounding drift across phases. Vestigial-translator + `Raw()` deletion correctly DEFERRED to Phase 5 (grep confirms still-load-bearing callers). Closeout: `../implementations/datacleaner-component-primitive-and-parallel-views-phase-4-closeout.md`. |
| 2026-05-26 | Initial Phase 4 spec authored. Covers 13 consumer-site migration map (12 active + 1 deferred for DDM per §7), B3 routing flip via `InvestedCapital().DebtLikeClaims`, dispatcher dual-write deletion via Option A (dispatcher applies component delta; umbrellas recomputed in view), `CalculationVersion` 4.2 → 4.3 atomic with cluster C-4, no `SchemaVersion` bump, 5-commit-cluster single-PR strategy. DDM bit-for-bit invariant preserved by deferring DDM consumer migration to Phase 5; Phase 4 adds `TestDDM_ConsumerPath_UnaffectedByPhase4` as a guard. Replay drift expectation split into 4 classes (Class I zero-drift, II Restater-only, III B-rule-firing, IV A1-firing). Phase 4 → Phase 5 gate documented. Implementation plan filed alongside at `datacleaner-component-primitive-and-parallel-views-phase-4-implementation-plan.md`. |
