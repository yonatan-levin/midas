# BUG-015 — DCF base operating income is a single quarter for 10-Q filers → ~4× understated FCF

**Status:** FIXED (pending operator re-baseline) — TTM operating-income base implemented on branch `fix/dcf-quarterly-base`; passed `/execute` B-V-R-Q (VERIFIER VERIFIED, REVIEWER APPROVE_WITH_NITS, QA PASS). CalcVersion 4.5 → 4.6. Hermetic replay flips KO −14.77 → **+15.74** and AMD −38.70 → **+6.85** (see §6). Root cause confirmed from source + replay of the captured **4.4** baseline through the engine. **STILL OPEN:** an operator fresh-4.6 `cmd/accuracy` baseline to confirm KO/AMD positive un-confounded from BUG-014 (the on-disk baseline is calc-version 4.4, so replay drift bundles BUG-014 + BUG-015 together).
**Severity:** HIGH — for any ticker whose latest filing is a 10-Q, the DCF projects from a single quarter of operating income (~4× too small), systematically understating intrinsic value. This is the second defect (after BUG-014) keeping KO and AMD negative.
**Filed:** 2026-06-05, split out of BUG-014 §5 (explicitly out of scope there).
**Area:** BACKEND — valuation engine (`internal/services/valuation`).
**Regression oracle:** `cmd/accuracy` over a fresh baseline; KO/AMD should flip from negative to positive/sane.

---

## 1. Symptom

After BUG-014 (cash excluded from working capital, CalcVersion 4.5), KO and AMD still produce **negative** intrinsic values — replaying the captured 4.4 baseline through the 4.5 engine yields KO −15.52, AMD −21.68. (The saved 4.4 responses were KO −14.77 / AMD −38.70.) BUG-014's cash fix flipped the cash-rich names (NVDA/AAPL/MSFT) positive but did not help KO/AMD.

## 2. Root cause (confirmed)

`internal/services/valuation/service.go`:
```go
baseOI := effectiveOI(dcfRestated)   // line ~1088 — effectiveOI (line 1778) reads ONE period's view
...
BaseOperatingIncome: baseOI,         // line ~1140 — fed to the DCF projection
```
`effectiveOI(*cleaneddata.FinancialDataView)` returns the **latest period's** (normalized) operating income. There is **no annualization / TTM logic anywhere** in the valuation service (grep `annualiz|ttm|quarter` → nothing). When the latest filing is a 10-Q, `baseOI` is a **single quarter** of operating income.

Evidence (captured 4.x bundle, KO): `financial_data_period: "2026Q1"`, `operating_income: 4,359M` on `revenue: 12,472M` — both quarterly. The DCF then grows and discounts a base that is ~4× too small, so NOPAT − reinvestment goes negative.

This is the **same defect class as RM-1** (the revenue-multiple model used quarterly revenue without annualizing), which was fixed with a TTM helper (`entities.HistoricalFinancialData.TrailingTwelveMonthsRevenue()`), **not** a crude ×4 — because ×4 ignores seasonality (KO/beverages is highly seasonal).

## 3. Fix (TTM operating-income base)

1. Add a TTM operating-income helper on `entities.HistoricalFinancialData` mirroring `TrailingTwelveMonthsRevenue` — same fallback chain and source-tag/warning contract: `TTM_PRIOR_BRIDGE → TTM_4Q → ANNUAL_FY → ANNUALIZED_QUARTER → INSUFFICIENT_HISTORY`. It must sum the **same** operating-income metric `effectiveOI` uses (normalized operating income) across the trailing four quarters; for an `ANNUAL_FY` latest period it returns the FY value unchanged (no double-counting).
2. In `service.go`, when the latest period is a **quarter**, set `baseOI` from the TTM helper instead of the single-quarter `effectiveOI`. When the latest is `FY`, behavior is unchanged (TTM resolves to the FY value). Surface the source tag + any lossy-path warning on `result.Warnings` (consistent with RM-1).
3. **No crude ×4** — seasonality matters.

## 4. Constraints

- **CalculationVersion bump 4.5 → 4.6** (changes `dcf_value_per_share` for 10-Q-latest tickers by design). Update both stamp sites + the `service_test.go` version pins.
- **LOAD-BEARING — must stay green, do NOT regenerate goldens:** `TestDDM_LegacyPath_BitForBit` (DDM is dividend-derived and doesn't use the DCF OI base — verify), plus the full suite and DC-1 invariants (`TestRecomputeUmbrellas_NoMutation`, shadow snapshots byte-identical, ledger basket).
- **Scope:** ONLY the DCF operating-income base annualization. FY-latest tickers must be bit-for-bit unchanged. Do not touch DDM/FFO/revenue_multiple base logic, the BUG-014 NWC code, or anything else.

## 5. Validation

- Regression test (mandatory, table-driven): TTM helper fallback chain (4 contiguous quarters; partial-year bridge; FY-latest passthrough; insufficient history) + a service-level test that a quarterly-latest fixture uses the TTM base and yields a larger/positive intrinsic than the single-quarter base. Must fail on pre-fix code.
- FY-latest invariance: a fixture whose latest period is FY must produce a bit-for-bit unchanged `baseOI` (and intrinsic).
- Before/after via `cmd/replay --from=parsed` on the captured KO/AMD bundles: intrinsic should rise materially (target: flip positive / sane).
- Live: fresh CalcVersion-4.6 capture + `cmd/accuracy` (operator re-baseline) — confirm KO/AMD positive and the basket mean-gap shrinks, validated against the narrate/debug logs.
