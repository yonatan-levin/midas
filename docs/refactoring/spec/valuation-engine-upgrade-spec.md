# Midas Valuation Engine — Full Upgrade Specification

**Version:** 1.1  
**Date:** 2026-04-16 (updated from 2026-04-07)  
**Status:** Phases 0-4 complete. 5 follow-up items in progress (see Implementation Status below).  
**Scope:** Upgrade the valuation engine to correctly value any company — growth, value, US, international — at fintech-platform-grade accuracy.

---

## Implementation Status

| Phase | Status | Commits | Notes |
|-------|--------|---------|-------|
| Phase 0 | **DONE** | `49b0afa` | GetSortedPeriods fix, ErrModelNotApplicable |
| Phase 1 | **DONE** | `49b0afa` | True FCF, growth caps, diluted shares, equity bridge, WACC spread |
| Phase 2 | **DONE** | `66ece97` | Growth estimator, analyst blending, ROIC ceiling, 3-stage |
| Phase 3 | **DONE** | `7eaa488`, `01f4db0` | DDM, FFO, Revenue Multiple, ModelRouter, classifier, fallback chain |
| Phase 4 | **DONE** | `440d204` | CRP, ADR, Blume/Hamada beta, exit-multiple TV, sanity check |
| Validation | **DONE** | `ed9cf63` | 10 correctness fixes, server tests (0%→96.2%), docs sync |

### Remaining Spec Items (v4.1)

| # | Spec Section | Item | Status |
|---|---|---|---|
| 1 | 2.1 | Analyst estimate caching (7-day TTL) | IN PROGRESS |
| 2 | 4.5 | ImpliedPFCF in SanityCheck | IN PROGRESS |
| 3 | 4.2 | SIC code extraction from SEC filing header | IN PROGRESS |
| 4 | 3.4 | REIT NAV calculation (NOI / Cap Rate) | IN PROGRESS |
| 5 | 3.3 | DDM P/BV cross-check vs ROE-justified multiple | IN PROGRESS |
| 6 | 4.3b | Size premium (Fama-French SMB) | DEFERRED (spec says "implement later") |

---

## Table of Contents

1. [Context](#context)
2. [New Services — Role & Purpose](#new-services--role--purpose)
3. [Phase 0: Prerequisites](#phase-0-prerequisites)
4. [Phase 1: Fix the Fundamentals](#phase-1-fix-the-fundamentals)
5. [Phase 2: Forward-Looking Growth Model](#phase-2-forward-looking-growth-model)
6. [Phase 3: Industry-Aware Model Selection](#phase-3-industry-aware-model-selection)
7. [Phase 4: International Support & Cross-Checks](#phase-4-international-support--cross-checks)
8. [Cross-Cutting Concerns](#cross-cutting-concerns)
9. [Dependency Graph](#dependency-graph)
10. [Entity & Schema Changes](#entity--schema-changes)
11. [Testing Strategy](#testing-strategy)
12. [What Stays the Same](#what-stays-the-same)

---

## Context

Midas has a working end-to-end pipeline (SEC EDGAR -> data cleaning -> DCF -> REST API), but the valuation engine uses a simplified single-stage DCF with:

- **NOPAT-based FCF** (no D&A add-back, no CapEx deduction, no working capital changes)
- **Uniform historical growth** (single rate applied to all 5 projection years)
- **No industry awareness** (banks, REITs, utilities all use the same DCF)
- **US-only scope** (no country risk premium, no ADR handling)

This produces unreliable results:
- NVDA: 60% uniform growth x 5 years = absurd revenue projection
- JNJ: uncapped 64.7% growth (BUG-010) = $3,942 valuation vs $150 market price
- JPM: DCF applied to a bank where operating cash flow is meaningless
- Capital-heavy companies: NOPAT overstates FCF by 30-50%

**Goal:** Upgrade the engine to correctly value any company. No Monte Carlo simulation.

---

## New Services — Role & Purpose

This section explains **why** each new service or component exists, what business problem it solves, and where it sits in the valuation pipeline. The current pipeline is:

```
SEC Data ──> Data Cleaner ──> Valuation Service ──> DCF Calculation ──> API Response
                                    │
                              (single path,
                               one model,
                               backward-looking)
```

The upgraded pipeline becomes:

```
SEC Data ─┐                                          ┌─> Multi-Stage DCF
Yahoo     ├──> Data Cleaner ──> Growth Estimator ──>  │
  Finance ┘        │                                  │   Model Router ──> API Response
                   │                                  │      │
              Industry Classifier                     ├─> DDM (banks)
                                                      ├─> FFO (REITs)
                                                      ├─> Revenue Multiple (pre-revenue)
                                                      │
                                                      └─> Sanity Cross-Check
```

### Growth Estimation Service

`internal/services/growth/estimator.go`

**Business problem it solves:**

The current engine asks one question: *"How fast did this company grow in the past?"* and then assumes that exact rate continues for 5 years. This is like driving by looking only in the rearview mirror.

NVDA grew revenue 60% last year. But the market — hundreds of analysts, institutional investors, management guidance — collectively expects ~40% next year and ~25% the year after. That forward consensus is far more informative than backward-looking math.

**What it does:**

The Growth Estimation Service is the engine's "forward-looking brain." It gathers growth signals from multiple sources (analyst consensus, historical trend, and the company's own return on capital), then blends them into a per-year growth trajectory for the projection period.

Think of it like this:
- **Year 1-3 growth:** Mostly driven by what the market expects (analyst consensus), sanity-checked against history
- **Year 4-7 growth:** Gradually fades from the company's current growth toward a normal mature-company rate, because no company stays hypergrowth forever
- **Year 8+ (terminal):** Settles at long-term GDP growth (2-3%), reflecting a fully mature business

The blending is confidence-weighted: a company covered by 15 analysts gets 80% weight on consensus; a micro-cap with zero coverage falls back entirely to historical trends. The system degrades gracefully — it doesn't pretend to have forward visibility it doesn't have.

**Where it sits in the pipeline:**

After data fetching, before DCF calculation. It replaces the single `CalculateAverageGrowthRate()` call that currently feeds the DCF model.

---

### Industry Classifier

`internal/services/datacleaner/industry/classifier.go` (activation of existing placeholder)

**Business problem it solves:**

A bank and a software company are fundamentally different businesses, yet the current engine treats them identically. Applying a standard DCF to JPMorgan is like measuring a fish's ability to climb a tree — the model doesn't fit the business.

Banks earn money from the spread between what they pay depositors and what they charge borrowers. Their "operating cash flow" mixes operating and financing activities in ways that make a standard DCF meaningless. REITs must distribute 90% of income as dividends by law, and their buildings depreciate on paper while appreciating in reality — making standard earnings misleading.

**What it does:**

The Industry Classifier examines each company's SEC filing metadata (SIC code, NAICS code, company name) and assigns an industry category. This classification then determines which valuation model the company gets routed to.

It uses the existing `industry_codes.json` configuration (already has mappings for 10+ sectors with SIC/NAICS codes and keyword matchers). The classification is deterministic — same company always gets the same model — which is critical for consistency.

**Where it sits in the pipeline:**

After data cleaning, before model selection. It produces an industry code that feeds the Model Router.

---

### Model Router

`internal/services/valuation/models/router.go`

**Business problem it solves:**

Different types of companies need fundamentally different valuation approaches. There's no single formula that correctly values both a SaaS company and a savings bank. The Model Router is the decision layer that matches each company to the right valuation methodology.

**What it does:**

The Model Router receives the industry classification and financial characteristics, then selects the appropriate valuation model:

| Company Type | Why Standard DCF Fails | Model Selected | Key Metric |
|-------------|----------------------|----------------|------------|
| **Standard companies** (MSFT, JNJ, NVDA) | It doesn't — DCF works | Multi-Stage DCF | Free Cash Flow |
| **Banks & insurance** (JPM, GS, MET) | Operating vs financing cash flows are inseparable; DCF requires clean separation | Dividend Discount Model | Dividends, ROE, Book Value |
| **REITs** (O, SPG, AMT) | Depreciation makes earnings meaningless; real estate appreciates while being depreciated on paper | FFO/AFFO Model | Funds From Operations, NAV |
| **Pre-revenue companies** (early biotech, pre-product startups) | No positive operating income to project forward | Revenue Multiple | Revenue x Sector Multiple |

The router exposes a `ValuationModel` interface, so adding a new model (e.g., a sum-of-the-parts model for conglomerates) means implementing the interface and registering it — no changes to the router itself.

**Where it sits in the pipeline:**

After the Growth Estimator produces growth rates, the Model Router selects the model, passes it the data, and returns the result back to the Valuation Service.

---

### Dividend Discount Model (DDM)

`internal/services/valuation/models/ddm.go`

**Business problem it solves:**

For financial institutions, the concept of "free cash flow" breaks down. A bank's core business IS lending and borrowing — its cash flows are a mix of operating activity and financial engineering that can't be separated the way a manufacturer's can. What IS meaningful for a bank: how much profit does it generate on its equity (ROE), and how much does it return to shareholders (dividends)?

**What it does:**

Instead of projecting operating cash flows, the DDM projects dividends forward:

```
Value per share = Next Year's Dividend / (Cost of Equity - Dividend Growth Rate)
```

It cross-checks this against the bank's book value and ROE. If a bank earns 15% on equity but the market prices it at book value, that's a signal — the DDM captures this through the relationship between ROE, growth, and payout ratio.

**Why it works for banks:** Banks are regulated to maintain capital ratios, so their dividend policies tend to be stable and predictable. The DDM leverages this stability rather than fighting against the opacity of bank cash flows.

---

### FFO Model (Funds From Operations)

`internal/services/valuation/models/ffo.go`

**Business problem it solves:**

REITs own real estate. Accounting rules depreciate buildings over 25-40 years, creating large non-cash charges that make net income artificially low. But real estate typically *appreciates* — a building bought in 2000 is worth more today, not less. Standard earnings are misleading; the REIT industry created FFO specifically to fix this.

**What it does:**

FFO strips out the misleading depreciation:

```
FFO = Net Income + Depreciation & Amortization - Gains on Property Sales
```

This gives a cash-earnings figure that reflects the REIT's actual operating performance. The model then values the REIT using:
- **P/FFO multiple** (analogous to P/E for regular companies) against sector medians
- **Net Asset Value (NAV)** = Net Operating Income / Capitalization Rate — what the properties are worth if sold today

**Why it needs its own model:** A REIT reporting $500M net income might have $800M in FFO because of $300M in depreciation. Applying DCF to $500M net income dramatically undervalues the company.

---

### Revenue Multiple Model

`internal/services/valuation/models/revenue_multiple.go`

**Business problem it solves:**

Some companies have no positive operating income — pre-revenue biotechs, early-stage tech companies in growth-at-all-costs mode, turnaround companies. The standard DCF formula literally fails: `validateInputs` rejects negative operating income. But these companies still have value — sometimes enormous value (Amazon was unprofitable for years while building its moat).

**What it does:**

When a company has no positive operating income, this model values it using revenue multiples from comparable companies:

```
Estimated Value = Revenue x Sector Median EV/Revenue Multiple
```

This is inherently less precise than a DCF (you're using peer multiples as a proxy, not the company's own projected cash flows), so the result is always flagged as **low-confidence**. It answers: *"Given what the market pays for similar companies' revenue, what would this company be worth?"*

**Why it's better than an error:** Without this model, Midas returns "cannot value this company" for any company with negative operating income. That's a significant coverage gap. A low-confidence directional estimate is more useful than no answer at all.

---

### Sanity Cross-Check

`internal/services/valuation/crosscheck.go`

**Business problem it solves:**

DCF is powerful but fragile — small changes in growth or discount rate assumptions can swing the output by 2-5x. A DCF model can produce a technically "correct" answer (all math checks out) that is economically absurd. The cross-check catches these cases.

**What it does:**

After the valuation model produces its result, the cross-check asks: *"Does this answer make sense relative to how the market prices similar companies?"*

It calculates the **implied multiples** from the DCF result:
```
If DCF says MSFT is worth $400/share:
  Implied P/E    = $400 / EPS
  Implied EV/EBITDA = Enterprise Value / EBITDA
  Implied P/FCF  = $400 / FCF per share
```

Then compares these against sector medians:
- Software sector median P/E is ~30x. If your DCF implies 150x P/E, something is likely wrong with your growth assumptions.
- If your DCF implies 5x P/E for a high-growth tech company, you're probably being too conservative.

**It does NOT override the DCF.** It flags discrepancies (>2x or <0.5x sector median) so the user can investigate. The DCF result stands as the primary valuation — the cross-check provides context for interpreting it.

**Where it sits in the pipeline:**

Last step, after the valuation model returns its result. Adds a `SanityCheck` object to the API response. Purely informational — it never changes the calculated value.

---

### ROIC Sustainability Check

`pkg/finance/growth/sustainability.go`

**Business problem it solves:**

Can a company actually sustain the growth rate we're projecting? This is the question that separates realistic valuations from fantasy.

A company's growth rate is bounded by how much it reinvests and how productive those reinvestments are. If a company earns 12% return on invested capital (ROIC) and reinvests 60% of its earnings, its maximum sustainable growth rate is 12% x 60% = 7.2%. Projecting 20% growth for this company means assuming either ROIC will double (unlikely) or it will take on massive capital (changes risk profile).

**What it does:**

Calculates the theoretical ceiling on growth:

```
ROIC = Net Operating Profit After Tax / Invested Capital
Sustainable Growth = ROIC x Reinvestment Rate
```

Where `Invested Capital = Stockholders' Equity + Interest-Bearing Debt - Cash` (the actual capital deployed in the business).

The Growth Estimation Service uses this as a **ceiling** for Stage 1 growth rates. If analyst consensus says 30% growth but ROIC-sustainable growth is only 10%, the system either caps or blends downward and flags the divergence.

**Why it lives in `pkg/finance/growth/`:** It's pure math — no external dependencies, no config, no API calls. Just financial arithmetic that converts ROIC and reinvestment rate into a growth ceiling.

---

## Phase 0: Prerequisites

> Fix foundational bugs that silently corrupt calculations before any upgrade work begins.

### 0.1 — Fix `GetSortedPeriods()` period sorting

**Problem:** `financial_data.go:100` has a TODO: basic string sort handles "2023FY" < "2024FY" correctly but fails for mixed quarterly/annual ordering (e.g., "2022FY" vs "2023Q1"). This silently corrupts growth rate calculations via `GetOperatingIncomeHistory()` and `CalculateAverageGrowthRate()`.

**Files to modify:**
- `internal/core/entities/financial_data.go:94-103` — implement proper chronological sorting:
  - Parse year number from period string
  - Order: FY < Q1 < Q2 < Q3 < Q4 within same year
  - Sort ascending (oldest first)

**Verification:** Unit test with mixed periods: `["2022FY", "2023Q1", "2023Q2", "2023FY", "2024Q1"]` should sort in that exact order.

### 0.2 — Handle negative operating income gracefully

**Problem:** `dcf.validateInputs()` (dcf.go:186) rejects `BaseOperatingIncome <= 0`. Growth-stage and turnaround companies hit this before alternative models arrive in Phase 3.

**Files to modify:**
- `internal/services/valuation/service.go` — detect negative OI before calling DCF, return a structured error
- `internal/services/valuation/errors.go` — add `ErrModelNotApplicable` sentinel error
- `internal/api/v1/handlers/` — map `ErrModelNotApplicable` to HTTP 422 with body: `{"error": "model_not_applicable", "detail": "Standard DCF requires positive operating income. Industry-specific models for this company type are planned."}`

---

## Phase 1: Fix the Fundamentals

> Make the existing DCF model produce correct answers. `CalculationVersion` bumps to `"1.1"`.

### 1.1 — Enforce growth rate caps (BUG-010)

**Problem:** Config defines `DCFMaxGrowthRate: 0.5` and `DCFMinGrowthRate: -0.3` (config.go:128-129, defaults at 284-285) but `performValuation()` never applies them.

**Files to modify:**
- `pkg/finance/growth/growth.go:270` — rename `CapGrowthRate()` to `CapGrowthRateDefault()`, add new function:
  ```go
  func CapGrowthRateWithBounds(growthRate, minRate, maxRate float64) float64
  ```
  Note: `pkg/` must remain config-free. Bounds are passed as parameters, not read from config.
- `internal/services/valuation/service.go:279` — after growth result is computed (line 221), apply:
  ```go
  growthResult.GrowthRate = growth.CapGrowthRateWithBounds(
      growthResult.GrowthRate,
      s.config.Valuation.DCFMinGrowthRate,
      s.config.Valuation.DCFMaxGrowthRate,
  )
  ```
- Add warning to `ValuationResult.Warnings` when capping is triggered
- `pkg/finance/dcf/dcf.go:189-190` — loosen `validateInputs` bounds to `[-1.0, 2.0]` (wide safety net) since the service layer now enforces tighter config-driven bounds

### 1.2 — True Free Cash Flow calculation

**Problem:** `FCF = NOPAT` (dcf.go:89). Missing D&A add-back, CapEx deduction, working capital changes. Overvalues capital-heavy companies, undervalues asset-light.

**New entity fields** (add to `internal/core/entities/financial_data.go`):
```go
DepreciationAndAmortization float64 `json:"depreciation_and_amortization"`
CapitalExpenditures         float64 `json:"capital_expenditures"`
OperatingCashFlow           float64 `json:"operating_cash_flow"`
CurrentAssets               float64 `json:"current_assets"`
CurrentLiabilities          float64 `json:"current_liabilities"`
CashAndCashEquivalents      float64 `json:"cash_and_cash_equivalents"`
```

**XBRL tag extraction** (add to `internal/infra/gateways/sec/parser.go` in `parsePeriodData()`):

| Field | Primary XBRL Tag | Fallback Tags |
|-------|-------------------|---------------|
| D&A | `DepreciationDepletionAndAmortization` | `DepreciationAndAmortization`, `Depreciation` |
| CapEx | `PaymentsToAcquirePropertyPlantAndEquipment` | `PaymentsToAcquireProductiveAssets`, `CapitalExpenditureIncurredButNotYetPaid` |
| Operating CF | `NetCashProvidedByOperatingActivities` | `CashProvidedByOperatingActivities` |
| Current Assets | `AssetsCurrent` | _(no fallback)_ |
| Current Liabilities | `LiabilitiesCurrent` | _(no fallback)_ |
| Cash | `CashAndCashEquivalentsAtCarryingValue` | `CashCashEquivalentsAndShortTermInvestments`, `Cash` |

Note: `PaymentsToAcquirePropertyPlantAndEquipment` is reported as a positive number (cash outflow) in SEC XBRL. Store as positive, subtract in FCF formula.

Note: `CashAndCashEquivalentsAtCarryingValue` is already listed as a supported concept in the parser but never wired into `parsePeriodData()`. Fix this systematically — audit for other listed-but-unwired concepts.

**DCF calculation changes** (`pkg/finance/dcf/dcf.go`):

Add new optional fields to `Inputs`:
```go
DepreciationAndAmortization float64 // D&A to add back (non-cash)
CapitalExpenditures         float64 // CapEx to subtract
WorkingCapitalChangePrior   float64 // Prior period net working capital
WorkingCapitalChangeCurrent float64 // Current period net working capital
```

FCF calculation (replace lines 88-97):
```go
freeCashFlow := nopat
if inputs.DepreciationAndAmortization > 0 || inputs.CapitalExpenditures > 0 {
    freeCashFlow = nopat + inputs.DepreciationAndAmortization - inputs.CapitalExpenditures
    deltaWC := inputs.WorkingCapitalChangeCurrent - inputs.WorkingCapitalChangePrior
    freeCashFlow -= deltaWC
} // else: fallback to FCF = NOPAT with warning
```

**Service layer** (`internal/services/valuation/service.go`):
- Calculate net working capital: `NWC = CurrentAssets - CurrentLiabilities`
- Compute delta from prior period vs current period
- Wire D&A, CapEx, NWC delta into DCF inputs
- Add warning when falling back to NOPAT (D&A/CapEx unavailable)

**Database migration:** Add nullable columns for all new fields. SQLite `ALTER TABLE ADD COLUMN` is used (columns are nullable by default in SQLite).

### 1.3 — Use diluted shares outstanding

**Problem:** `service.go:296` uses `marketData.SharesOutstanding` (basic). `DilutedSharesOutstanding` is already parsed from SEC (parser.go:317-326) but never used.

**Priority order** (modify `service.go:296-303`):
```go
sharesOutstanding := latestFinancialData.DilutedSharesOutstanding
if sharesOutstanding <= 0 {
    sharesOutstanding = marketData.SharesOutstanding
}
if sharesOutstanding <= 0 {
    sharesOutstanding = latestFinancialData.SharesOutstanding
}
```

Rationale: diluted from financial data (SEC, most conservative) > basic from market data (Yahoo, most current) > basic from financial data (fallback).

### 1.4 — WACC-terminal growth safety buffer

**Problem:** `dcf.go:125-127` checks `WACC <= TerminalGrowthRate` but no minimum spread. If WACC=8% and terminal=7.5%, terminal value explodes.

**Files to modify:**
- `pkg/finance/dcf/dcf.go:125` — add minimum spread check:
  ```go
  if inputs.WACC - inputs.TerminalGrowthRate < 0.01 {
      return nil, errors.New("WACC must exceed terminal growth rate by at least 1%")
  }
  ```
- `internal/services/valuation/service.go` — `calculateTerminalGrowthRate()` signature changes to accept WACC:
  ```go
  func (s *Service) calculateTerminalGrowthRate(historicalCAGR, wacc float64) float64
  ```
  Cap terminal growth to `wacc - 0.02` if needed. Call order in `performValuation()` is already correct (WACC at line 264, terminal at line 274).

### 1.5 — Equity value bridge (Enterprise -> Equity)

**Problem:** `service.go:305` divides `EnterpriseValue / SharesOutstanding`. Should be `EquityValue = EV - Debt + Cash`, then divide. Line 320 already computes `EquityValue` partially (subtracts debt but doesn't add cash).

**Files to modify:**
- `internal/services/valuation/service.go:305` — use proper equity bridge:
  ```go
  cashAndEquiv := latestFinancialData.CashAndCashEquivalents
  equityValue := dcf.CalculateEquityValue(dcfResult.EnterpriseValue, latestFinancialData.InterestBearingDebt, cashAndEquiv)
  dcfValuePerShare := equityValue / sharesOutstanding
  ```
- Update `result.EquityValue` (line 320) to use the same formula
- `CashAndCashEquivalents` field added in Phase 1.2

---

## Phase 2: Forward-Looking Growth Model

> Replace backward-looking historical CAGR with analyst consensus + multi-stage projection. `CalculationVersion` bumps to `"2.0"`.

### 2.1 — Fetch analyst estimates from Yahoo Finance

**Problem:** Yahoo Finance `earningsTrend` module provides analyst consensus revenue/earnings growth. We only fetch `defaultKeyStatistics,financialData`.

**Files to modify:**
- `internal/infra/gateways/market/yfinance_client.go:190` — expand modules parameter:
  ```go
  params.Set("modules", "defaultKeyStatistics,financialData,earningsTrend")
  ```
  This is an expansion of the existing request, NOT a new API call (no additional latency).

- Add response parsing structs in `yfinance_client.go`:
  ```go
  type YFinanceEarningsTrend struct {
      Trend []YFinanceTrendEntry `json:"trend"`
  }
  type YFinanceTrendEntry struct {
      Period          string          `json:"period"` // "0q", "+1q", "0y", "+1y", "+5y"
      RevenueEstimate *YFinanceEstimate `json:"revenueEstimate"`
      EarningsEstimate *YFinanceEstimate `json:"earningsEstimate"`
      Growth          *YFinanceValue   `json:"growth"`
  }
  type YFinanceEstimate struct {
      Avg            *YFinanceValue `json:"avg"`
      Low            *YFinanceValue `json:"low"`
      High           *YFinanceValue `json:"high"`
      NumberOfAnalysts *YFinanceValue `json:"numberOfAnalysts"`
  }
  ```

- `internal/core/ports/gateways.go` — add new method to `YFinanceGateway` interface:
  ```go
  GetAnalystEstimates(ctx context.Context, ticker string) (*YFinanceAnalystEstimates, error)
  ```
  Or extend `YFinanceKeyStats` with analyst fields. Prefer new method for separation of concerns.

- Add new port struct:
  ```go
  type YFinanceAnalystEstimates struct {
      RevenueEstimateCurrentYear float64
      RevenueEstimateNextYear    float64
      EarningsGrowth5Year        float64
      NumberOfAnalysts           int
      RevenueEstimateLow         float64
      RevenueEstimateHigh        float64
  }
  ```

**Fallback:** When `earningsTrend` returns empty (foreign tickers, micro-caps, delisted), return nil estimates gracefully. The growth estimation service (2.3) handles 0-analyst case.

**Caching:** Cache analyst estimates separately with 7-day TTL (they change weekly, not daily). Key: `analyst:{ticker}`.

### 2.2 — New `GrowthEstimate` entity

**File:** `internal/core/entities/growth_estimate.go` (new)

Located in `internal/core/entities/` (not `pkg/`) because it contains source metadata from external APIs.

```go
type GrowthEstimate struct {
    // Per-year growth rates for explicit forecast period
    ProjectedGrowthRates []float64 `json:"projected_growth_rates"`
    TerminalGrowthRate   float64   `json:"terminal_growth_rate"`

    // Analyst consensus data
    AnalystRevenueGrowthY1  float64 `json:"analyst_revenue_growth_y1"`
    AnalystRevenueGrowthY2  float64 `json:"analyst_revenue_growth_y2"`
    AnalystEarningsGrowth5Y float64 `json:"analyst_earnings_growth_5y"`
    NumberOfAnalysts        int     `json:"number_of_analysts"`

    // Historical baseline
    HistoricalCAGR        float64 `json:"historical_cagr"`
    SustainableGrowthRate float64 `json:"sustainable_growth_rate"` // ROIC x reinvestment

    // Metadata
    Source     string `json:"source"`     // "analyst_blend", "historical_only", "default"
    Confidence string `json:"confidence"` // "high", "medium", "low"
    Method     string `json:"method"`     // human-readable description
}
```

### 2.3 — Growth estimation service

**File:** `internal/services/growth/estimator.go` (new)

Located in `internal/services/` (NOT `pkg/finance/growth/`) because it depends on analyst data and config. The existing `pkg/finance/growth/` remains a pure math library.

**Core function:**
```go
func (e *GrowthEstimator) EstimateGrowthRates(
    ctx context.Context,
    analystData *ports.YFinanceAnalystEstimates, // may be nil
    historicalGrowth *growth.CalculationResult,
    sustainableGrowth float64,
    config *config.ValuationConfig,
) (*entities.GrowthEstimate, error)
```

**Confidence-weighted blending:**
```
>=10 analysts: 80% analyst / 20% historical
3-9 analysts:  60% analyst / 40% historical
1-2 analysts:  40% analyst / 60% historical
0 analysts:    100% historical (capped, with ROIC ceiling)
```

**Divergence check:** If analyst consensus and historical CAGR diverge by >2x, add warning flag but still use blend. Don't silently ignore large disagreements.

**Three-stage projection:**
```
Stage 1 (Years 1-3):  Blended growth rate (analyst + historical)
Stage 2 (Years 4-7):  Linear fade from Stage 1 exit rate -> 8%
                       (or industry median when available from Phase 3)
Stage 3 (Year 8+):    Terminal = min(GDP proxy, 3%) — same conservative logic
```

All growth rates capped at every stage using `growth.CapGrowthRateWithBounds()`.

### 2.4 — Multi-stage DCF calculation

**Files to modify:**
- `pkg/finance/dcf/dcf.go` — add optional `GrowthRates []float64` field to `Inputs`:
  ```go
  type Inputs struct {
      // ... existing fields ...
      GrowthRate  float64   // Single rate (backward-compatible)
      GrowthRates []float64 // Per-year rates (optional, overrides GrowthRate)
  }
  ```
  **Backward compatibility:** If `GrowthRates` is nil or empty, expand `GrowthRate` into all projection years. `SensitivityAnalysis` and `CalculateImpliedGrowthRate` continue using `GrowthRate` unchanged.

- Projection loop (line 81-83) changes to:
  ```go
  rateForYear := inputs.GrowthRate
  if len(inputs.GrowthRates) >= year {
      rateForYear = inputs.GrowthRates[year-1]
  }
  currentOperatingIncome *= (1 + rateForYear)
  ```

- `ProjectionYears` — update `validateInputs` max from 10 to 15 (supports 7-year explicit + terminal). Use `s.config.Valuation.DCFProjectionYears` (config already has this field, default 5) instead of hardcoded 5 at service.go:282.

- `internal/services/valuation/service.go` — wire `GrowthEstimate.ProjectedGrowthRates` into `dcfInputs.GrowthRates`

**API response:** Keep `GrowthRate float64` in `ValuationResult` as a backward-compatible summary (e.g., CAGR of the projected rates). Add `GrowthRates []float64` as new additive field.

### 2.5 — ROIC sustainability check

**New entity fields** (add to `internal/core/entities/financial_data.go`):
```go
StockholdersEquity float64 `json:"stockholders_equity"`
```

**XBRL extraction** (add to parser.go):

| Field | Primary XBRL Tag | Fallback Tags |
|-------|-------------------|---------------|
| Stockholders' Equity | `StockholdersEquity` | `StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest` |

**New file:** `pkg/finance/growth/sustainability.go`

```go
func CalculateSustainableGrowth(nopat, investedCapital, payoutRatio float64) float64 {
    if investedCapital <= 0 { return 0 }
    roic := nopat / investedCapital
    return roic * (1 - payoutRatio)
}

func CalculateInvestedCapital(totalEquity, interestBearingDebt, cash float64) float64 {
    return totalEquity + interestBearingDebt - cash
}
```

This stays in `pkg/` because it's pure math with no external dependencies.

Use `SustainableGrowthRate` as ceiling for Stage 1 growth in `EstimateGrowthRates()`.

---

## Phase 3: Industry-Aware Model Selection

> Different company types get fundamentally different valuation models. `CalculationVersion` bumps to `"3.0"`.

### 3.1 — Model router interface

**File:** `internal/services/valuation/models/router.go` (new)

```go
// ValuationModel defines the interface for all valuation approaches
type ValuationModel interface {
    Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error)
    ModelType() string // "multi_stage_dcf", "ddm", "ffo", "revenue_multiple"
    SupportsIndustry(industry string) bool
}

// ModelInput is the unified input for all models
type ModelInput struct {
    HistoricalData *entities.HistoricalFinancialData
    MarketData     *entities.MarketData
    MacroData      *entities.MacroData
    GrowthEstimate *entities.GrowthEstimate
    Options        *ValuationOptions
    Industry       string
}

// ModelResult is the unified output
type ModelResult struct {
    IntrinsicValuePerShare float64
    EnterpriseValue        float64
    EquityValue            float64
    ModelType              string
    Projections            interface{} // model-specific detail
    Warnings               []string
}

// ModelRouter selects the appropriate model
type ModelRouter struct {
    models []ValuationModel
    logger *zap.Logger
}

func (r *ModelRouter) SelectModel(industry string, financials *entities.FinancialData) ValuationModel
```

**DI wiring:** Register `ModelRouter` as a port interface in `internal/core/ports/`. Inject all model implementations via `uber/fx` `fx.Group` tag. The valuation service receives `ModelRouter` instead of calling DCF directly.

### 3.2 — Activate industry classifier

**Files to modify:**
- `internal/services/datacleaner/industry/classifier.go` — implement `Classify()`:
  ```go
  func (c *IndustryClassifier) Classify(sicCode string, naicsCode string, companyName string) (string, error)
  ```
  Uses existing `config/datacleaner/industry_codes.json` (has SIC/NAICS mappings for 10+ sectors).

- `internal/infra/gateways/sec/parser.go` — extract SIC code from SEC EDGAR company facts response header (available in the `sic` field of the company tickers JSON)

- `internal/core/entities/financial_data.go:15` — populate `IndustryCode` field (currently has TODO comment)

### 3.3 — Dividend Discount Model (DDM) for financials

**File:** `internal/services/valuation/models/ddm.go` (new)

For banks, insurance, financial services where operating cash flow is meaningless.

**Additional data requirements:**
- Dividends per share — XBRL: `CommonStockDividendsPerShareDeclared`
- Payout ratio — derived: `DPS / EPS`
- Book value per share — already available via Yahoo Finance `bookValue`
- ROE — derived: `NetIncome / StockholdersEquity`

**New entity fields:**
```go
DividendsPerShare float64 `json:"dividends_per_share"`
NetIncome         float64 `json:"net_income"`
```

**Formula:** `Value = DPS * (1+g) / (CoE - g)` (Gordon Growth on dividends)
**Cross-check:** P/BV vs ROE-justified multiple

### 3.4 — FFO model for REITs

**File:** `internal/services/valuation/models/ffo.go` (new)

For REITs where standard earnings are distorted by depreciation.

**Data requirements:**
- D&A (from Phase 1.2 — already extracted)
- Net income (from Phase 3.3)
- Gains on property sales — XBRL: `GainLossOnSaleOfProperties`

**Formula:** `FFO = Net Income + D&A - Gains on Property Sales`
**NAV:** `NAV = NOI / Cap Rate`
**Cross-check:** P/FFO vs sector median

### 3.5 — Revenue multiple model for pre-revenue

**File:** `internal/services/valuation/models/revenue_multiple.go` (new)

For companies with no positive operating income (before Phase 0.2 routes them here).

**Data requirements:**
- Revenue (already available)
- Sector comparable EV/Revenue multiples (static table, same as Phase 4.4)

**Formula:** `Value = Revenue * Sector_Median_EV_Revenue_Multiple`
**Flag:** Always marked as low-confidence valuation.

### 3.6 — Wire model selection into valuation service

**File:** `internal/services/valuation/service.go`

- After data cleaning, classify industry via `IndustryClassifier`
- Select model via `ModelRouter.SelectModel(industry, financialData)`
- Execute selected model
- Populate `ValuationResult.CalculationMethod` with model type string
- Phase 0.2's `ErrModelNotApplicable` is replaced by routing to appropriate alternative model

**Dependency order within Phase 3:**
```
3.2 (classifier) -> 3.1 (router) -> 3.3, 3.4, 3.5 (models, parallel) -> 3.6 (wiring)
```

---

## Phase 4: International Support & Cross-Checks

> Handle non-US companies and provide sanity-check signals. `CalculationVersion` bumps to `"4.0"`.

### 4.1 — Country risk premium

**New entity fields** (add to `internal/core/entities/market_data.go`):
```go
DomicileCountry    string  `json:"domicile_country"`
CountryRiskPremium float64 `json:"country_risk_premium"`
```

**Files to modify:**
- `pkg/finance/wacc/wacc.go` — add `CountryRiskPremium` to `Inputs`, extend CAPM:
  ```go
  result.CostOfEquity = inputs.RiskFreeRate + inputs.Beta*inputs.MarketRiskPremium + inputs.CountryRiskPremium
  ```
- `config/country_risk.json` (new) — static Damodaran-style country risk premiums. Updated manually or via periodic scrape.

### 4.2 — ADR support

**Scope clarification:** ADR support means valuing ADRs that **file with SEC** (most large ADRs file 20-F: TSM, NVO, BABA, etc.). Non-SEC foreign companies are out of scope for now.

- `internal/infra/gateways/sec/parser.go` — extract country of domicile from SEC filing header
- Map ADR ticker to domicile country for CRP lookup
- Apply country risk premium from 4.1

### 4.3 — Beta improvements

**Easy (implement first):**
- **Blume adjustment:** `beta_adj = 0.67 * raw_beta + 0.33 * 1.0` — trivial, do in Phase 4.3a
- **Unlevered/relevered beta:**
  ```go
  beta_u = beta_l / (1 + (1-T) * (D/E))
  beta_relevered = beta_u * (1 + (1-T) * (target_D_E))
  ```
  All inputs already available (debt, equity, tax rate). Do in Phase 4.3a.

**Hard (implement later, Phase 4.3b):**
- **Size premium:** Needs market cap buckets and Fama-French SMB factor data. This is a new data source. Defer to after 4.3a is validated.

**File:** `pkg/finance/wacc/beta.go` (new) — pure math functions, no config dependency.

### 4.4 — Exit-multiple terminal value

**Files to modify:**
- `pkg/finance/dcf/dcf.go` — add `CalculateDCFWithExitMultiple()`:
  ```go
  TV_exit = Terminal_Year_EBITDA * Industry_EV_EBITDA_Multiple
  ```
- Average Gordon Growth TV and Exit Multiple TV for final terminal value (reduces model risk)
- `config/industry_multiples.json` (new) — sector median EV/EBITDA multiples. Static table, updated periodically.

**Data source for multiples:** Static JSON file initially. Can later be enriched from Yahoo Finance sector data or Finzive scraping.

### 4.5 — Multiples sanity cross-check

**File:** `internal/services/valuation/crosscheck.go` (new)

After DCF, calculate implied multiples and compare:
```go
type SanityCheck struct {
    ImpliedPE            float64  `json:"implied_pe"`
    SectorMedianPE       float64  `json:"sector_median_pe"`
    ImpliedEVEBITDA      float64  `json:"implied_ev_ebitda"`
    SectorMedianEVEBITDA float64  `json:"sector_median_ev_ebitda"`
    ImpliedPFCF          float64  `json:"implied_p_fcf"`
    IsReasonable         bool     `json:"is_reasonable"`
    Flags                []string `json:"flags,omitempty"`
}
```

Flag if DCF-implied multiple is >2x or <0.5x sector median.

Add `SanityCheck *SanityCheck` to `ValuationResult` (additive, backward-compatible).

---

## Cross-Cutting Concerns

### API backward compatibility

- All JSON response changes are **additive** (new fields only)
- `GrowthRate float64` remains in `ValuationResult` as summary (CAGR of projected rates)
- `GrowthRates []float64` added as new field alongside it
- `CalculationMethod` populated with model type string
- `SanityCheck` added as optional nested object

### `CalculationVersion` bumping

| Phase | Version | Description |
|-------|---------|-------------|
| Current | `"1.0"` | Single-stage NOPAT DCF |
| Phase 1 | `"1.1"` | True FCF, diluted shares, growth caps, equity bridge |
| Phase 2 | `"2.0"` | Multi-stage growth with analyst consensus |
| Phase 3 | `"3.0"` | Industry-aware model selection |
| Phase 4 | `"4.0"` | International support, cross-checks |

### Cache key versioning

Current key: `valuation:{ticker}` (service.go:68). After engine upgrades, stale cached results are wrong.

**Fix:** Include version in cache key: `valuation:v{major}:{ticker}` (e.g., `valuation:v2:AAPL`). Old-version cache entries naturally expire.

### Database migrations

All new `FinancialData` fields added as **nullable columns** via `ALTER TABLE ADD COLUMN`. SQLite allows this safely. No data loss, no table recreation needed.

### Performance

- Phase 2.1 expands existing Yahoo Finance request (no new API call, no latency impact)
- Analyst estimates cached separately with 7-day TTL
- Multi-stage DCF with 7 projection years: negligible compute impact
- Industry classification: SIC code lookup in JSON config, <1ms

---

## Dependency Graph

```
Phase 0 ──► Phase 1 ──► Phase 2 ──┬──► Phase 3
(Prerequisites)  (Correct)    (Accurate)   │    (Universal)
                                           │
                                           └──► Phase 4
                                                (Global + Cross-checks)
```

**Within Phase 1:** 1.1 and 1.3 are independent. 1.2 must precede 1.5 (both need new entity fields, 1.5 uses Cash from 1.2). 1.4 is independent.

**Within Phase 2:** 2.1 -> 2.2 -> 2.3 -> 2.4. 2.5 is independent (can run in parallel with 2.1-2.2).

**Within Phase 3:** 3.2 -> 3.1 -> (3.3, 3.4, 3.5 in parallel) -> 3.6.

**Phase 3 and 4** can run in parallel after Phase 2 completes.

Each phase delivers standalone value and can be released independently.

---

## Entity & Schema Changes

### Complete list of new `FinancialData` fields

| Field | Type | Phase | XBRL Source |
|-------|------|-------|-------------|
| `DepreciationAndAmortization` | `float64` | 1.2 | `DepreciationDepletionAndAmortization` |
| `CapitalExpenditures` | `float64` | 1.2 | `PaymentsToAcquirePropertyPlantAndEquipment` |
| `OperatingCashFlow` | `float64` | 1.2 | `NetCashProvidedByOperatingActivities` |
| `CurrentAssets` | `float64` | 1.2 | `AssetsCurrent` |
| `CurrentLiabilities` | `float64` | 1.2 | `LiabilitiesCurrent` |
| `CashAndCashEquivalents` | `float64` | 1.2 | `CashAndCashEquivalentsAtCarryingValue` |
| `StockholdersEquity` | `float64` | 2.5 | `StockholdersEquity` |
| `DividendsPerShare` | `float64` | 3.3 | `CommonStockDividendsPerShareDeclared` |
| `NetIncome` | `float64` | 3.3 | `NetIncomeLoss` |

### New `MarketData` fields

| Field | Type | Phase | Source |
|-------|------|-------|--------|
| `DomicileCountry` | `string` | 4.1 | SEC filing header |
| `CountryRiskPremium` | `float64` | 4.1 | `config/country_risk.json` |

### New `ValuationResult` fields

| Field | Type | Phase |
|-------|------|-------|
| `GrowthRates` | `[]float64` | 2.4 |
| `GrowthSource` | `string` | 2.3 |
| `GrowthConfidence` | `string` | 2.3 |
| `SanityCheck` | `*SanityCheck` | 4.5 |

---

## Testing Strategy

### Golden test cases

Known-correct valuations for reference tickers, validated against Bloomberg/Morningstar/GuruFocus with reasonable tolerance bands (within 30% is acceptable for DCF given different assumptions):

| Ticker | Type | Phase Validated | Expected Behavior |
|--------|------|-----------------|-------------------|
| MSFT | Large-cap tech, stable growth | Phase 1+ | Reasonable DCF, moderate growth |
| NVDA | High-growth tech | Phase 2 | Multi-stage captures decay, not absurd uniform growth |
| JNJ | Value/defensive | Phase 1 | Growth caps prevent $3,942 bug |
| AAPL | Large-cap, buybacks | Phase 1 | Diluted shares used correctly |
| JPM | Financial (bank) | Phase 3 | Routed to DDM, not DCF |
| O | REIT | Phase 3 | Routed to FFO model |
| TSM | ADR (Taiwan) | Phase 4 | Country risk premium applied |
| BRK-A | Conglomerate, no dividends | Phase 2 | Historical growth, 0 analysts edge case |
| RIVN | Pre-revenue/negative OI | Phase 0/3 | Graceful error -> revenue multiple model |

### Unit tests per package

- `pkg/finance/dcf/` — multi-stage with uniform rates equals single-stage (mathematical invariant)
- `pkg/finance/growth/` — `CapGrowthRateWithBounds()` edge cases, `CalculateSustainableGrowth()` with zero/negative inputs
- `pkg/finance/wacc/` — with/without CRP, beta adjustments
- `internal/services/growth/` — all blending scenarios (10 analysts, 5, 1, 0), divergence warnings

### Edge case tests

- Negative operating income (pre-Phase 3: returns `ErrModelNotApplicable`)
- Zero debt company (WACC = cost of equity)
- Zero shares outstanding (error)
- Single year of data (fallback to default growth)
- Missing D&A/CapEx (fallback to NOPAT with warning)
- WACC-terminal spread < 1% (capped)
- Empty analyst estimates (falls back to historical)

### Regression

- `go test ./...` must pass after each phase
- Existing tests in `internal/services/valuation/`, `pkg/finance/dcf/`, `pkg/finance/growth/`, `pkg/finance/wacc/` must not break

### Property-based tests (gopter)

- Multi-stage DCF: if all `GrowthRates[i]` equal `GrowthRate`, result matches single-rate DCF exactly
- EquityValue = EnterpriseValue - Debt + Cash (always)
- Diluted shares >= basic shares (always)
- Capped growth rate is within [min, max] bounds (always)

---

## What Stays the Same

These systems are solid and do not need rework:

- **Data fetching pipeline** — `internal/services/datafetcher/coordinator.go` (parallel/sequential orchestration)
- **SEC XBRL parser structure** — `internal/infra/gateways/sec/parser.go` (`findValue()` pattern, just adding more tags)
- **Data cleaner pipeline** — adjustments, flagging, quality scoring (`internal/services/datacleaner/`)
- **API layer** — Gin router, middleware, handlers (`internal/api/`)
- **Authentication & rate limiting** — API key auth, permission model
- **Cache & repository pattern** — Redis/in-memory cache, SQLite repos
- **DI container** — `uber/fx` wiring (`internal/di/container.go`)
- **Quality scoring** — confidence grades, data freshness scoring
- **Configuration system** — Viper-based config loading
