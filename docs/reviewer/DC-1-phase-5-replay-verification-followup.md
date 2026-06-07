# DC-1 Phase 5 — Replay / live-response verification follow-up

**Status:** PARTIALLY CLOSED (operator follow-up — not a code blocker). **Update 2026-06-03:** the "no current-version baseline" half of the gap (§3 confound) is **closed** — a fresh `CalculationVersion 4.4` baseline for the 10-ticker basket was captured live this session and landed at `artifacts/tier2-baseline/2026-06-03/` (recipe: `docs/accuracy/baseline-capture-runbook.md`). **Update 2026-06-07 (§4 item 2 — DebtLikeClaims EV correction):** the **DebtLikeClaims EV-bridge plumbing is now CONFIRMED LIVE on the DCF path** (WMT/COST/DAL/F fire the B1 `operating_leases` overlay → it flows into `InvestedCapital().DebtLikeClaims` → subtracted to the dollar in the EV→equity bridge; see §4.2 below). The **DDM-specific ADD path remains structurally unobservable on production** — every candidate bank (USB/PNC/TFC/MET) falls back to `multi_stage_dcf` (T2-BS-1 `DPS=0`) and fires zero B-rules, so no production bank ever executes the DDM branch. That sub-case stays unit-test-pinned (`TestDDM_EVBridge_AddsDebtLikeClaims`). §4.1 (4.3→4.4 FRED isolation) and §4.3 (cleaning-rule projection) remain OPEN.
**Type:** Verification gap (NOT a bug — no defect found; the code is behavior-preserving and the test suite is green)
**Date:** 2026-06-02 (baseline-captured update 2026-06-03)
**Origin:** `/verify` (app-level runtime observation) run on the `dc1-phase-5-followup` branch after the full B-V-R-Q + gpt-5.5 review cycle.
**Related:** [Phase 5 follow-up closeout §8](../refactoring/archive/dc1-phase-5-followup-closeout.md) · [Phase 5 spec §5.3/§5.4 replay attribution caveat](../refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-5-spec.md) · [Baseline-capture runbook](../accuracy/baseline-capture-runbook.md) · [Accuracy report 2026-06-03](../accuracy/report-2026-06-03.md)

---

## 1. What was verified at runtime (PASS)

Drove the **real valuation engine** via the hermetic `cmd/replay --from=raw` on captured SEC/market/macro bundles (JPM=DDM/bank, AAPL+AMD=DCF), diffing the current engine's response against the saved baseline:

- ✅ **`calculation_version: "4.1" → "4.4"`** in the response for every ticker — the Phase 5 P5-C1 bump is live in the served response.
- ✅ All model paths run end-to-end with **no errors / panics** after the P5-C4 translator + dead-helper deletions (JPM → `assumption_profile: mature_large_bank:mature` → legacy DDM; AAPL → `software_like_large_scale`; AMD → `cyclical_mid_cycle`).
- ✅ **No `enterprise_value` diff and no `cleaning_adjustments` diff** vs baseline → the P5-C1 EV bridge and the P5-C3-full adjustments projection are behavior-preserving end-to-end (no regression).
- ✅ Logs clean: only `WARN phase=RPL-7-raw-fallback` (benign — replay used the parsed macro snapshot because per-series files were absent) + the expected `schema_drift=true`. No engine WARN/ERROR.

## 2. What could NOT be exercised on the available bundles (the gap)

Two of Phase 5's headline behaviors are **intentionally invisible** on the current hermetic basket and remain unconfirmed at the live-response level (they are unit-test-covered):

| Phase 5 behavior | Why unobservable on the basket | Test coverage that stands in |
|---|---|---|
| **DDM EV-bridge ADDS `DebtLikeClaims`** (P5-C1) — only changes `enterprise_value` for a **B-rule-firing bank** | JPM is the only bank in the basket and it fires **zero** B-rules → the correction is a verified `+0` no-op (no `enterprise_value` diff). No B-rule-firing bank bundle exists. | `TestDDM_EVBridge_AddsDebtLikeClaims` (synthetic B-rule) |
| **`cleaning_adjustments` projection with non-empty output** (P5-C3-full) | All basket tickers produce an **empty/absent** `cleaning_adjustments` in the response (confirmed: AMD baseline `cleaning_adjustments` is absent/null). The real basket fires ~zero cleaning adjustments. | `TestApplyActiveAdjustments_AdjustmentsProjection_BasketParity` (3 synthetic fixtures driving every rule cluster) |

## 3. Confounded-baseline caveat (NOT a Phase 5 regression)

The only replay baseline available is `calculation_version 4.1` (`artifacts/tier2-baseline/2026-05-15/` and `2026-05-19/`), which **predates the assumption-profile config AND phases 2–4**. So the headline numeric drift seen in replay (e.g. JPM `dcf_value_per_share` 124.32 → 116.86, ~6%) is the **cumulative 4.1 → 4.4 span**, dominated by `assumption_profile: "" → mature_large_bank:mature` (profile routing the 4.1 baseline lacks) + phases 2–4 — **not attributable to Phase 5**. DDM intrinsic value is dividend-derived and Phase 5 does not change it (the bit-for-bit invariant is pinned on the JPM/BAC/WFC golden fixtures, not on these live bundles).

## 4. Recommended operator action to close this gap

A clean, Phase-5-attributable live verification needs **infrastructure not available in the hermetic worktree** (live SEC/Yahoo/FRED access + `FRED_API_KEY` + a seeded DB/API key):

1. **Capture a fresh `4.3` (pre-Phase-5-tip) baseline** for the basket via live capture (cache-bypass), then replay the Phase 5 ship-sha against it → isolates the Phase 5 delta (expected: non-DDM zero-drift; DDM `EnterpriseValue` zero-drift on non-B-rule banks; `calculation_version` field text only).
2. **Add a B-rule-firing bank ticker** to the capture set (a bank that fires B1 operating-lease / B2 pension / B3 litigation overlays) and confirm its `enterprise_value` increases by exactly the B1+B2+B3 `DebtLikeClaims` amount — the live confirmation of the P5-C1 correction.
   - **✅ DebtLikeClaims plumbing confirmed live on the DCF path (2026-06-07).** Captured USB/PNC/TFC/MET/PRU + lease-heavy non-banks. **Two structural facts emerged:** (a) every production bank (USB/PNC/TFC `mature_large_bank`, MET `insurance_company`) has its router pick DDM but executes `multi_stage_dcf` — the T2-BS-1 `DividendsPerShare=0` fallback — so the **DDM branch is never reached on production data**; PRU runs `revenue_multiple`. (b) **All 5 financials fire ZERO B-rules** (`overlays=0`, `DebtLikeClaims=0`), so even the DCF subtraction is a `−0` no-op on banks. The DDM-specific ADD correction is therefore not observable on any production bank — it stays pinned by `TestDDM_EVBridge_AddsDebtLikeClaims`.
   - However, **lease-heavy non-banks DO fire B1 and exercise the SAME `InvestedCapital().DebtLikeClaims` accessor + 6-arg `CalculateEquityValueWithDebtLikeClaims` bridge** (the DDM path differs only in sign). **WMT** (`mature_large_scale:mature`, legacy DCF path → Layer-A-independent, identical on master): B1 `operating_leases` overlay = **$1.662B**; reported `equity_value` = **$403,349,258,624** equals `EV ($437.521B) − InterestBearingDebt ($36.887B) + Cash ($10.729B) − MinorityInterest ($6.352B) − Preferred ($0) − DebtLikeClaims ($1.662B)` **to the dollar (diff = $0)**; omitting the DebtLikeClaims term gives $405.011B (off by exactly $1.662B). `InterestBearingDebt == TotalDebt == $36.887B` is **B-rule-free** (the lease is NOT folded into it) → the Phase-4 routing flip is confirmed live: WACC debt excludes the lease, the lease is subtracted separately via `DebtLikeClaims`. Reproduced on COST ($2.466B), DAL ($0.837B), F ($0.567B). **Net: the DebtLikeClaims EV-bridge plumbing is live-verified on the DCF path; only the DDM ADD-sign is production-unreachable.**
3. **Capture a ticker that fires cleaning rules** (non-empty `cleaning_adjustments`) and confirm the projected audit-trail rows (RuleID/Category/Type/Amount/Percentage/FromAccount/ToAccount) match expectations — the live confirmation of the P5-C3-full projection.

## 5. Disposition

- **Not a merge blocker.** The code is behavior-preserving (B-V-R-Q + gpt-5.5 + full suite green), and the project's prior phases (2/3/4) all merged with replay attribution deferred to the operator (Phase 4 closeout §5). This follow-up matches that precedent.
- **Owner:** operator (needs live data infra).
- **Close when:** §4 items 1–3 are captured and the replay matches the Phase 5 spec §5 per-ticker expectation.

## 6. Change log

| Date | Change |
|---|---|
| 2026-06-02 | Filed from the `/verify` runtime observation on `dc1-phase-5-followup`. Runtime PASS for the observable response intent (`calculation_version 4.4` live, no regression, clean run); two Phase-5-specific behaviors (DebtLikeClaims EV correction non-zero path; non-empty adjustments projection) not exercisable on the hermetic basket + confounded 4.1 baseline → operator live-capture follow-up recorded (§4). |
| 2026-06-03 | **§3 confound closed.** Captured a fresh `CalculationVersion 4.4` baseline for the 10-ticker basket via live `?trace=1` capture (cold-cache, config-fallback macro since no `FRED_API_KEY`); landed at `artifacts/tier2-baseline/2026-06-03/` (10/10 complete bundles; JPM 7/8 per the known RPL-8 DDM-skips-cleaner-snapshot). Recipe + operator residuals documented in `docs/accuracy/baseline-capture-runbook.md`. Built `cmd/accuracy` to report intrinsic-vs-price over the baseline (`docs/accuracy/report-2026-06-03.md`): mean absolute price gap **86.7%**, 9/10 intrinsic below market, negative intrinsic on KO + AMD, JPM `ddm→dcf` model divergence (live T2-BS-1 fallback). The 86.7%-gap / negative-FCF-projection finding is filed for a separate `/debug` track (engine FCF projection) — **out of scope** for this verification follow-up. §4.1–§4.3 stay OPEN (need B-rule bank + cleaning-rule ticker + live FRED). |
| 2026-06-07 | **§4 item 2 — DebtLikeClaims EV-bridge plumbing CONFIRMED LIVE on the DCF path.** Live-captured USB/PNC/TFC/MET/PRU (all fall back to `multi_stage_dcf` per T2-BS-1; all fire **zero** B-rules → DDM ADD-path production-unreachable, stays unit-pinned) + lease-heavy non-banks. **WMT** B1 `operating_leases` overlay $1.662B: `equity_value` $403,349,258,624 == `EV − IBD + cash − MI − PE − DebtLikeClaims` to the dollar (diff $0); excluding DebtLikeClaims is off by exactly $1.662B; `InterestBearingDebt` is B-rule-free → Phase-4 routing flip confirmed live. Reproduced on COST/DAL/F. Verified on the `feat/dcf-reinvestment-layer-a` worktree (Layer A is DCF-path-only + WMT runs the non-opted-in `mature_large_scale:mature` legacy path → result identical on master). No code change; no regression. §4.1 (FRED 4.3→4.4 isolation) + §4.3 (cleaning-rule projection) stay OPEN. |
