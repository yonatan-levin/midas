# BUG-014 — DCF working-capital term includes cash → negative projected FCF → systematic undervaluation

**Status:** OPEN — root cause confirmed (read from source + quantified on captured 4.4 bundles). Engine fix NOT yet implemented (large change; needs its own branch + regression tests + DC-1-invariant care).
**Severity:** HIGH — core DCF valuation is wrong across the whole basket. 9/10 tickers value below market by a mean of −84%; KO and AMD produce **negative** intrinsic values (model breakdown, not conservatism).
**Filed:** 2026-06-03, from the `/debug` track opened on the finding the new `cmd/accuracy` harness surfaced.
**Area:** BACKEND — valuation engine (`internal/services/valuation` + `pkg/finance/dcf`).
**Regression oracle:** `cmd/accuracy` over `artifacts/tier2-baseline/2026-06-03/` (report: `docs/accuracy/report-2026-06-03.md`, on branch `feat/accuracy-harness`). After the fix, the mean absolute gap should collapse and `NEG_INTRINSIC` / `NEG_FCF_YEARS` flags should clear for cash-rich names.

---

## 1. Symptom

`cmd/accuracy` over the fresh CalcVersion-4.4 baseline:

- Mean **absolute** intrinsic-vs-price gap = **86.7%**; 9/10 tickers below market (mean −84%).
- **KO intrinsic = −$14.77**, **AMD intrinsic = −$38.70** (negative).
- AAPL/NVDA/MSFT: terminal PV = 96–106% of enterprise value (explicit window sums ~0 or negative).
- `NEG_FCF_YEARS` on KO, AMD, AAPL, NVDA; `TERMINAL_DOMINANCE` on AAPL, NVDA, MSFT.

Signature (NVDA `17-response.json`): every explicit-year discounted FCF is negative —
`dcf_per_year_pv = [-3.85e9, -4.91e9, -6.26e9, -7.58e9, -8.68e9]`, growing more negative each year; `dcf_terminal_pct_of_ev = 1.043`.

## 2. Evidence (captured 4.4 bundles, hermetic — no live calls)

| | NVDA | KO |
|---|---|---|
| Revenue | $81.6B | $12.47B (quarterly — see §5 obs) |
| Operating income | $53.5B | $4.36B |
| D&A | $1.0B | — |
| CapEx | $1.8B | $0.27B |
| → NOPAT + D&A − CapEx | **≈ +$42B** | ≈ +$3.2B |
| **CurrentAssets** | **$150.995B** | $30.39B |
| CurrentLiabilities | $43.884B | $22.378B |
| **NWC = CA − CL (as coded)** | **$107.1B** | $8.0B |
| Cash & equivalents (inside CA) | $13.2B | **$10.57B** |
| `operating_cash_flow` in cleaned data | 0 | 0 |

NVDA's coded NWC is **$107B on $81.6B of revenue** — impossible as *operating* working capital; it is dominated by the cash + short-term-investment hoard lumped into `other_current_assets` ($112B). KO's cash ($10.57B) **exceeds its entire coded NWC ($8.0B)** — true operating NWC is negative (supplier float), but the engine sees +$8B.

Back-solving NVDA year-1 FCF (PV −3.85B ⇒ undiscounted ≈ −4.4B): `42 + scaledDA − scaledCapEx − scaledΔNWC = −4.4` ⇒ ΔNWC ≈ **$31B** — i.e. the year-over-year jump in the cash-polluted NWC, not an operating-WC change.

## 3. Reproduction

```bash
# Hermetic re-run through the current engine:
go run ./cmd/replay --from=parsed --diff-stages --verbose \
  artifacts/tier2-baseline/2026-06-03/NVDA/req_*/
# Or read the captured outputs directly:
#   artifacts/tier2-baseline/2026-06-03/{NVDA,KO,AMD}/req_*/{10-clean-output,15-valuation,17-response}.json
# Or run the harness report:
go run ./cmd/accuracy --dir artifacts/tier2-baseline/2026-06-03
```

## 4. Root cause

**PRIMARY — working capital includes cash.** `internal/services/valuation/service.go::calculateNetWorkingCapitalChange` (≈ line 1980) computes:

```go
latestNWC := latestView.CurrentAssets - latestView.CurrentLiabilities   // includes cash, ST investments, ST debt
...
return latestNWC - priorNWC
```

`CurrentAssets` includes cash & cash-equivalents and short-term/marketable investments; `CurrentLiabilities` includes the current portion of debt. For cash-accumulating firms the year-over-year change in `CA − CL` is dominated by the cash/investment build, which is mis-counted as **working capital invested** (a cash outflow) in FCF. Standard FCF practice uses *operating* NWC:

```
operating NWC = (CurrentAssets − Cash&Equivalents − ShortTermInvestments)
              − (CurrentLiabilities − ShortTermDebt)
ΔNWC          = operatingNWC_latest − operatingNWC_prior
```

Cash accumulation is a *result* of free cash flow, never a use of it — including it double-counts and inverts the sign of FCF for exactly the high-quality, cash-generative companies the engine most needs to value correctly.

**CONTRIBUTING — base ΔNWC scaled by cumulative growth.** `pkg/finance/dcf/dcf.go:122`:

```go
scaledNWCChange := inputs.NetWorkingCapitalChange * growthFactor   // growthFactor = cumulative OI growth
freeCashFlow = nopat + scaledDA - scaledCapEx - scaledNWCChange
```

A single base-year ΔNWC is multiplied by the **cumulative** operating-income growth factor every projection year, so the already-overstated ΔNWC grows monotonically across the explicit window — this is why `dcf_per_year_pv` gets *more* negative each year (−3.85B → −8.68B). Economically, incremental WC for year *t* should be ≈ `operatingNWC_{t-1} × revenueGrowth_t`, not `baseΔNWC × cumulativeGrowth`.

**Cascade:** overstated ΔNWC → negative projected FCF → negative terminal-year FCF → negative terminal value (Gordon) → negative/under-stated EV → negative or deeply-understated intrinsic value per share.

## 5. Observations (separate, do not fix under this ticket)

- **DC-1 Phase-0 plug lumping:** the cleaner currently absorbs the current-asset/-liability umbrellas into `other_current_assets/liabilities` (see CLAUDE.md DC-1 Phase-0 notes), so a clean cash/ST-debt exclusion may need either the parser to expose `CashAndCashEquivalents` + `ShortTermInvestments` + `ShortTermDebt` separately (NVDA cash is available: $13.2B) or a documented approximation. Scope the data availability before the fix.
- **`operating_cash_flow = 0`** in cleaned data (unused by the `UseTrueFCF` path, but a latent cleaner gap).
- **KO base looks quarterly** (OI $4.36B / revenue $12.47B) — if a 10-Q period feeds the DCF annual base, NOPAT is ~4× understated, compounding the negative FCF. Possible separate DCF-quarterly-base issue (cf. RM-1 for the revenue-multiple model). Confirm before fixing.

## 6. Proposed fix (for a dedicated branch — NOT this harness branch)

1. In `calculateNetWorkingCapitalChange`, subtract cash & cash-equivalents (and short-term investments, when available) from `CurrentAssets`, and short-term debt from `CurrentLiabilities`, on **both** latest and prior, before the delta. Guard the data-availability path (fall back to a documented behavior when components are unavailable).
2. Reconsider `dcf.go:122` — incremental WC should scale with the **per-year** revenue/OI growth increment, not the cumulative factor (or pass a pre-computed per-year ΔNWC series).
3. **Regression test (mandatory):** a per-year FCF decomposition assertion on NVDA/KO/AMD inputs — projected FCF must be positive for cash-generative firms; pin `dcf_per_year_pv > 0` and intrinsic > 0 for KO/AMD. Use the captured bundles as fixtures.
4. Re-run `cmd/accuracy` over a re-captured 4.4 baseline; the mean absolute gap should drop materially and `NEG_INTRINSIC`/`NEG_FCF_YEARS` should clear.

**Risk / care:** valuation math is load-bearing. Respect DC-1 invariants (this changes `dcf_value_per_share` for most tickers by design → a deliberate, documented `CalculationVersion` bump; coordinate with the DDM bit-for-bit golden fixtures, which are dividend-derived and unaffected, and with the `tier2-baseline` replay expectations). Capture a fresh baseline after the fix.

## 7. Change log

| Date | Change |
|---|---|
| 2026-06-03 | Filed from the `/debug` track. Root cause confirmed from source (`calculateNetWorkingCapitalChange` + `dcf.go:122`) and quantified on captured 4.4 bundles (NVDA NWC $107B incl. cash; KO cash > NWC). Engine fix deferred to a dedicated branch; `cmd/accuracy` is the regression oracle. |
