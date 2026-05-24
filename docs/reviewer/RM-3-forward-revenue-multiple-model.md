# RM-3 — Add forward revenue-multiple model so growth and WACC stop being inert for negative-OI tickers

**Status:** OPEN — filed 2026-05-06 during live-API verification of the Graham-floor PR.
**Severity:** Major. Today's `revenue_multiple` model produces present-snapshot valuations that ignore the engine's most expensive computations (the multi-stage growth curve and WACC). For high-growth negative-OI tickers (the canonical case: MXL, but also early-stage SaaS/biotech), this systematically understates fair value by 5-30× and renders the engine's growth/WACC stages decorative rather than load-bearing.
**Origin:** Live MXL response showed a 7-stage growth curve averaging 37%/yr and WACC of 19% computed at full cost, then completely unused — `revenue_multiple.go` reads only `latest.Revenue` and `Industry`. The user noticed the headline number ($1.32) made no economic sense given the growth assumptions.
**Blocks:** Nothing. This is a new feature, not a regression.
**Related specs:** `docs/reviewer/RM-1-revenue-multiple-quarterly-vs-ttm.md` (revenue base — RM-3 builds on the TTM helper), `docs/reviewer/RM-2-sector-multiple-coverage-gaps.md` (sector multiplier — RM-3 needs the Damodaran-quality multipliers to produce sane forward EVs), `docs/refactoring/spec/valuation-engine-upgrade-spec.md` (broader engine roadmap).

---

## Context

The model router (`internal/services/valuation/models/router.go`) currently has three branches for negative-OI tickers:

```
operating_income > 0  → DCF
operating_income ≤ 0  → revenue_multiple (with optional DDM/FFO short-circuit for FIN/REIT)
shares_outstanding ≤ 0 → ErrInsufficientData
```

For the revenue-multiple branch, the engine still computes the full multi-stage growth curve (`internal/services/growth/estimator.go`) and full WACC (`pkg/finance/wacc/`). Both are stamped onto the response for transparency. **Neither is consumed by `revenue_multiple.go`** — that file reads only `latest.Revenue` and `input.Industry`. The growth curve and WACC are inert decoration on this path.

This is mathematically defensible *only* when growth is genuinely uncertain (low confidence) — in which case applying a static sector multiple to trailing revenue is the conservative move. But the engine has explicit confidence signals (`growth.Confidence ∈ {high, medium, low}`) and we ignore them. For high-confidence growth tickers, the trailing multiple is just wrong.

For MXL specifically, the math difference is dramatic. From the artifact bundle:

| Quantity | Trailing model (today) | Forward model (proposed) |
|---|---|---|
| Revenue base | $137M (Q1 only, pre-RM-1) → $549M (post-RM-1 ANNUALIZED_QUARTER) | $560M TTM × growth chain to year 5 = ~$3,775M |
| Sector multiple | 1.5× (MFG default — see RM-2 bug) | 6.0× (MFG_SEMI — see RM-2 fix) |
| Discount | none (immediate value) | (1 + WACC)^5 = 1.19^5 ≈ 2.39 |
| EV (or PV(EV)) | $137M × 1.5 = $206M | $3,775M × 6.0 / 2.39 = $9,477M |
| Equity bridge | $206M − $151M + $61M = $116M | $9,477M − $151M + $61M = $9,387M |
| Per share | $1.32 | ~$107 |

Even with RM-1 and RM-2 fixed (i.e., TTM revenue and sector-correct multiplier), the trailing model still produces ~$37/share — far below the market's $80. The forward model produces $107, suggesting the market is pricing-in *some* of the analyst growth signal but not all of it. That's a useful disagreement signal — actionable for an investor — that the trailing model can't generate.

## Why it matters

1. **Wasted compute.** Every revenue-multiple-routed request runs the growth estimator (analyst-blend with API call to YFinance) and WACC (CAPM with beta resolution, Blume adjustment, country risk premium lookup). Both are non-trivial. Today this work is purely decorative on this code path. Forward-revenue-multiple makes it load-bearing.
2. **Systematic bias against growth tickers.** Negative-OI is overrepresented in young, fast-growing companies (early-stage biotech, pre-profit SaaS, capex-cyclical semis at trough). Today's model gives all of them a "revenue × small multiple − debt + cash" valuation that ignores their entire growth thesis. Investors using Midas to evaluate these tickers get systematically misleading signals.
3. **Inconsistent with how the rest of the engine reasons.** The DCF path takes growth and WACC seriously. The DDM path uses growth (in the Gordon model). The FFO path applies a forward FFO multiple. Only revenue-multiple ignores forward signals — a quirk of this model's history rather than a deliberate design choice.
4. **The sanity-check crosscheck (`crosscheck.go`) is also blind here.** Crosscheck flags when DCF-implied multiples diverge from sector medians — but that runs only on the DCF path. Revenue-multiple has no equivalent sanity check, partly because its output isn't compared against a forward-looking benchmark.

## Proposed fix (one of)

### Option A — New model class: `forward_revenue_multiple.go`

Add `internal/services/valuation/models/forward_revenue_multiple.go` implementing the existing `models.ValuationModel` interface. Router selects this in preference to the trailing model when:

- `effectiveOI(fd) ≤ 0` (negative-OI as today)
- `growthEstimate.Confidence ∈ {"high", "medium"}` (confidence gate; "low" still uses trailing)
- `wacc > 0` AND `wacc < 0.50` (sanity guards: rejects degenerate WACC)
- `len(growthEstimate.ProjectedGrowthRates) ≥ 5` (need at least 5 years to project)

Algorithm:

```go
func (m *ForwardRevenueMultipleModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
    revenue, source, warning := input.HistoricalData.TrailingTwelveMonthsRevenue() // RM-1 helper
    if revenue <= 0 { return nil, fmt.Errorf("forward_revenue_multiple: no revenue available (%s)", source) }

    // Project revenue 5 years forward using the engine's growth curve.
    rates := input.GrowthEstimate.ProjectedGrowthRates
    projectionYears := 5
    if len(rates) < projectionYears {
        return nil, fmt.Errorf("forward_revenue_multiple: insufficient growth horizon (%d years)", len(rates))
    }
    revenueY5 := revenue
    for i := 0; i < projectionYears; i++ {
        revenueY5 *= 1 + rates[i]
    }

    // Apply sector multiple at year 5 (RM-2 helper provides Damodaran-grade multiplier).
    multiple := m.getMultiple(input.Industry)
    enterpriseValueY5 := revenueY5 * multiple

    // Discount back at WACC.
    discountFactor := math.Pow(1+input.WACC, float64(projectionYears))
    presentValueEV := enterpriseValueY5 / discountFactor

    // Standard equity bridge.
    equityValue := presentValueEV - input.InterestBearingDebt + input.CashAndCashEquivalents
    if input.SharesOutstanding <= 0 {
        return nil, fmt.Errorf("forward_revenue_multiple: shares outstanding must be positive")
    }
    valuePerShare := equityValue / input.SharesOutstanding
    if valuePerShare < 0 { valuePerShare = 0 }

    return &ModelResult{
        IntrinsicValuePerShare: valuePerShare,
        EnterpriseValue:        presentValueEV,
        EquityValue:            equityValue,
        ModelType:              "forward_revenue_multiple",
        Confidence:             "medium",
        Warnings: []string{
            fmt.Sprintf("Forward revenue multiple: %dy projection at avg %.1f%% growth, %.1fx %s sector multiple, WACC %.2f%%",
                projectionYears, avg(rates[:projectionYears])*100, multiple, input.Industry, input.WACC*100),
        },
    }, nil
}
```

Routing logic in `router.go`:

```go
if effectiveOI(latest) <= 0 {
    if shouldUseForwardRevenueMultiple(growthEstimate, wacc) {
        return forwardRevModel, "forward_revenue_multiple"
    }
    return trailingRevModel, "revenue_multiple"
}
```

The trailing model **stays as the conservative fallback**. Low-confidence growth, missing WACC, or insufficient growth horizon all route back to it.

**Pros:**
- Clean separation: new model is independently testable.
- Trailing model behaviour preserved for low-confidence signal — no regression for tickers where today's model is correctly conservative.
- Standard finance: forward multiples discounted to PV is textbook valuation, well-understood by both code reviewers and end users.
- Surfaces the engine's growth/WACC computation as load-bearing — they finally do work.
- Calculation method label tells the user which path fired (`"forward_revenue_multiple"` vs `"revenue_multiple"`).

**Cons:**
- ~150 lines of new code + comprehensive tests.
- Router selection logic is now branchy (3+ predicates instead of 1); needs explicit unit tests per branch.
- Sensitivity to bad growth signals: a 100% YoY analyst extrapolation (analyst-blend can produce these) compounds disastrously over 5 years. Need a sanity-cap on projected revenue (e.g., reject if projected revenue / current revenue > 25× over 5 years; that's the "every 5 years revenue grows 25×" sanity bound).

**Risk:** medium. The model is mathematically conventional but can produce extreme outputs on bad inputs. Mitigate with the cap above and the confidence gate.

### Option B — Extend the existing `revenue_multiple.go` with an optional forward mode

Add a `ForwardProjectionYears int` field to `ModelInput`. If nonzero AND growth confidence is medium+, the existing model projects forward; otherwise it does today's trailing math.

**Pros:** less code surface; one model class.
**Cons:** the model now answers two semantically different questions ("snapshot value" vs. "PV of forward value"); harder to test; harder to read; the calculation_method label can't differentiate the two without additional metadata; consumers can't easily filter "show me only tickers valued by forward model."

**Risk:** medium. The conflation makes the model harder to evolve over time.

### Option C — Replace the trailing model entirely

Delete `revenue_multiple.go`. Everything that was negative-OI now uses forward.

**Pros:** simplest router logic.
**Cons:** removes the conservative fallback for low-confidence growth, low-quality data, or insufficient history. Companies whose growth is genuinely uncertain would get aggressive forward valuations they don't deserve. Regression for the "trailing was actually the right call" case.

**Risk:** high. Trades the current systematic-understatement bias for a systematic-overstatement bias on low-confidence tickers.

## Recommendation (post-research, REVISED)

> **Substantive update 2026-05-06** — second-round `thinkdeep` review revealed two errors in the original Option A framing: (1) the new-model-class boundary is wrong; one unified `revenue_multiple.go` with a profile-driven mode is better. (2) The 5-year forward horizon is wrong for multiple-based models; equity research uses **1-3 year forward anchor**, not 5y. 5y horizon stacks projection risk on top of terminal-multiple risk and double-counts growth. The recommendation below supersedes the earlier "Option A vs B" framing.

### Why one unified model, not two model classes

Original framing was "new `forward_revenue_multiple.go` class + keep trailing `revenue_multiple.go` as fallback." Second-round review challenged this:

- **Class explosion risk.** Doing the same per-metric split across DDM (single-stage + multi-stage) and FFO (snapshot + forward) means **6 model classes for 3 metrics**. The router becomes a pile of selection rules.
- **The "switch" is the assumption profile, not the class.** Once we have an `AssumptionProfile` keyed by `(archetype, maturity)`, the trailing-vs-forward decision is just a profile field (`horizon: 0` for trailing, `horizon: 1-3` for forward). One model class, two modes.
- **Diagnostics are clearer.** Response carries `assumption_profile: "cyclical_mid_cycle"` + `horizon_selected: 2`. A consumer reading the response knows exactly which mode fired without checking which of two `calculation_method` strings landed.

### Corrected design (consolidated)

**Single `revenue_multiple.go` with profile-driven behaviour.** Profile resolution happens upstream (in router or a dedicated `AssumptionProfileResolver`); the model receives the resolved profile and produces:
1. A `trailing_value` field (always emitted; static `revenue × multiple − debt + cash`).
2. A `forward_value` field (emitted when profile.horizon > 0).
3. A primary `intrinsic_value_per_share` chosen by profile policy (default: forward when emitted, else trailing).
4. Full diagnostic fields (assumption_profile, horizon_selected, multiple_used, terminal_method, etc.).

Both numbers are always visible. The consumer can:
- Read the headline `intrinsic_value_per_share` (the primary, profile-chosen value).
- Read both `trailing_value` and `forward_value` to see divergence.
- Filter by `assumption_profile` to compare like-for-like across tickers.

This is what `thinkdeep` recommended in its second round: *"one model class per family + shared typed assumptions/profile + shared valuation policy utilities + model-local financial semantics."*

### Corrected horizon (1-3y, not 5y)

Equity-research convention (per Damodaran and Wall Street Prep, perplexity-cited):

| Model | Horizon | Reasoning |
|---|---|---|
| **Multi-stage DCF** | 5-7y (10y for high-growth) | Full FCFF projection; terminal usually 60-80% of EV |
| **Multi-stage DDM** | 5y (10y for maturing payers) | Explicit dividend forecast + Gordon terminal |
| **Forward P/FFO (REIT)** | 1-2y NTM anchor | Multiples-based; longer horizon stacks risk |
| **Forward EV/Revenue** | **1-3y forward anchor** | Same logic as P/FFO; this corrects the spec |

Why 1-3y for revenue multiple specifically:
- **5y forward EV/Revenue stacks three risks**: revenue projection error + terminal multiple uncertainty + WACC discount sensitivity.
- **The terminal multiple is doing most of the work** at 5y — at year 5 you've projected 5 years of revenue then applied a multiple that itself reflects growth assumptions. Double-counting.
- **Practitioners use NTM (next-twelve-months) or 2y forward** for relative valuation. The signal is "is this stock cheap relative to *near-term* sector peers?" — not "what's the PV of operations 5 years out?"
- **For PV-of-operations questions, use DCF** (which does year-by-year FCFF, not multiple-based shortcuts).

### Discount rate: cost of equity, NOT WACC

Critical correction from `thinkdeep`: revenue multiple is *relative valuation*. WACC is for enterprise cash-flow discounting (DCF). For a forward-revenue-multiple to be a coherent equity-level valuation:

- Either (a) use **cost of equity** as the discount rate (because we're producing per-share equity value).
- Or (b) skip the discount entirely (treat as relative-multiple snapshot at year N).

The original RM-3 sketch used WACC, which is wrong for this model class. Either change to cost-of-equity or just use the forward-NTM anchor without an additional discount.

### Revised algorithm

```go
func (m *RevenueMultipleModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
    profile, err := m.resolveProfile(input)  // (archetype, maturity, horizon, base_method, ...)
    if err != nil { return nil, err }

    revenue, _, _ := input.HistoricalData.TrailingTwelveMonthsRevenue()  // RM-1 helper
    if revenue <= 0 { return nil, fmt.Errorf("revenue_multiple: no revenue") }

    // Apply revenue base normalization (handles cyclical trough cases per profile.BaseMethod).
    revenueBase := m.normalizeRevenueBase(revenue, profile, input.HistoricalData)

    // Trailing value: always computed.
    multiple := m.getMultiple(input.Industry)
    trailingEV := revenueBase * multiple
    trailingEquity := trailingEV - input.InterestBearingDebt + input.CashAndCashEquivalents
    trailingPS := trailingEquity / input.SharesOutstanding

    // Forward value: only if profile.Horizon > 0.
    var forwardPS float64
    if profile.Horizon > 0 {
        rates := input.GrowthEstimate.ProjectedGrowthRates
        if len(rates) >= profile.Horizon {
            forwardRevenue := revenueBase
            for i := 0; i < profile.Horizon; i++ {
                forwardRevenue *= 1 + rates[i]
            }
            // Apply mature-state terminal multiple (lower than current peer multiple,
            // to avoid double-counting growth).
            terminalMultiple := profile.TerminalMultiple  // e.g., 0.7 × current
            forwardEV := forwardRevenue * terminalMultiple
            // Optional discount at cost-of-equity — pure NTM anchor doesn't discount.
            if profile.DiscountAtCostOfEquity {
                discount := math.Pow(1+input.CostOfEquity, float64(profile.Horizon))
                forwardEV /= discount
            }
            forwardEquity := forwardEV - input.InterestBearingDebt + input.CashAndCashEquivalents
            forwardPS = forwardEquity / input.SharesOutstanding
        }
    }

    primary := trailingPS
    if forwardPS > 0 && profile.PreferForward {
        primary = forwardPS
    }

    return &ModelResult{
        IntrinsicValuePerShare: primary,
        TrailingValue:          trailingPS,  // NEW field on ModelResult
        ForwardValue:           forwardPS,   // NEW field, may be 0 if not computed
        ModelType:              "revenue_multiple",
        AssumptionProfile:      profile.Name,        // NEW
        HorizonSelected:        profile.Horizon,     // NEW
        TerminalMultiple:       profile.TerminalMultiple,  // NEW
        Confidence:             confidenceFromProfile(profile, input),
        Warnings:               warnings,
    }, nil
}
```

### Backward compatibility

- `calculation_method` stays `"revenue_multiple"` (no breaking label change).
- `intrinsic_value_per_share` stays the headline number.
- New fields on `ModelResult` (`TrailingValue`, `ForwardValue`, `AssumptionProfile`, etc.) are additive.
- The router stops gating "trailing-vs-forward" — it always picks `revenue_multiple` for negative-OI tickers, and the model itself decides what to do based on the profile.

This corrects the architectural drift and consolidates with the broader profile-driven roadmap (see VAL-1, VAL-2, VAL-3 trackers).

### Original three reasons (still valid)

1. **The trailing model has legitimate uses.** When growth confidence is "low" — analyst data thin, historical CAGR volatile, or insufficient history — applying a static multiple to trailing revenue is the right conservative move. Don't lose it.
2. **Two named models > one toggle.** `calculation_method = "forward_revenue_multiple"` is a clear signal to consumers about which math fired. Toggles inside one model are harder to reason about.
3. **Independently testable.** Option A's new model can be unit-tested in isolation; the router selection logic can be unit-tested separately.

### Caveat from the architectural review

`thinkdeep` raised a real challenge: if the only differences between forward and trailing are **policy** (horizon, cap, terminal method) rather than **mechanics** (different inputs, state variables, decomposition), Option B (toggle inside existing model with an `assumption_profile` parameter) may be cheaper to maintain. Decision criteria: use a new class only if it changes (a) required inputs, (b) state variables forecasted, (c) valuation decomposition, or (d) result diagnostics.

Forward-revenue-multiple does change (a) — it requires `GrowthEstimate.ProjectedGrowthRates`, which the trailing model doesn't read. So the new-class boundary is justified. But the assumption-profile abstraction proposed below applies to both options.

### Dependency chain

The recommendation is conditional on:
- **RM-1 (TTM revenue base) shipping first.** Without it, the forward model would project from a quarterly base, so 5-year projected revenue would be 4× too low — same compounding error.
- **RM-2 Phase 1 (sector multiple buckets) shipping first or alongside.** Without it, the forward model would still apply MFG (1.5×) to a semi, giving a forward output that's still meaningfully wrong. RM-2 Phase 2 (Damodaran data) can come later.

This is a **dependency chain**: RM-1 → RM-2 (Phase 1) → RM-3. Each step amplifies the previous step's correction.

---

## Refined design (post-research)

Five corrections to the sketch above, in priority order:

### 1. The 25× compound-growth cap is the wrong abstraction (and the wrong number)

**Original proposal:** flat compound-growth cap of 25× over 5 years (~85% CAGR).

**Damodaran's actual rule** (per perplexity-ask research, sourced from Damodaran NYU Stern dataset notes 2024-2026): *"max 2× current revenue in 5 years"* for most companies (~15% CAGR baseline). Hard rejection if 5y CAGR exceeds **50-60%** without strong moats. My 25× cap was ~5-6× more permissive than industry practice would tolerate.

**Architectural finding** (per `thinkdeep`): a flat global cap is the wrong abstraction regardless of the number. Different company archetypes warrant different caps:

| Archetype × maturity | Reasonable 5y compound cap | Notional max CAGR |
|---|---|---|
| Mature large-scale (e.g. Coca-Cola) | 1.5× | ~8% |
| Software-like, large-scale (e.g. MSFT) | 2× | ~15% |
| Software-like, scaling | 4× | ~32% |
| Cyclical, mid-cycle | 2× | ~15% |
| Cyclical, trough (the MXL case!) | **3×** | ~25% — needs special trough-base handling, see §3 below |
| Hypergrowth early-stage SaaS | 6× | ~43% |
| Pre-revenue biotech with clinical readouts | 8× | ~52%, reflects binary-outcome nature |

**Replacement design:** instead of a flat cap, introduce an `assumption_profile` table keyed by `(archetype, maturity)` 2-tuple. Default profile is `(software-like, scaling)` if classification is unknown. The profile drives:
- compound-growth cap
- horizon (see §2 below)
- growth-fade requirement (rates must decay year-over-year)
- normalization handling (see §3 below)

This is ~30 lines of config + ~50 lines of router logic. Lots of leverage for small surface area.

### 2. Horizon should be maturity-driven, not sector-driven

**Original proposal:** flat 5-year horizon for all forward-revenue calculations.

**Research finding** (Damodaran blog Jan 2024, perplexity-cited): *"For young growth companies, I use a 5-year period, with transition thereafter."* Damodaran uses 5y as the default for growth firms.

**Architectural finding** (per `thinkdeep`): sector-uniform horizons are weak. Maturity matters more than sector — two semis at different cycle positions need different horizons even though they share SIC 3674. Use 3 buckets:

| Maturity bucket | Horizon | Rationale |
|---|---|---|
| Mature / already stabilized | 3y | Short forecast period; rely on terminal multiple sooner |
| Standard growth (Damodaran's default for growth firms) | 5y | Damodaran's published default |
| High-growth / long-convergence (early biotech, frontier SaaS, clinical-stage pharma) | 7y | Longer projection captures the convergence-to-stability arc |

Horizon lives in the assumption-profile table, derived from (archetype, maturity).

### 3. Cyclicals need a normalized revenue base, not raw TTM

**Critical research finding** (per `thinkdeep`): for cyclical sectors, applying a forward model to *raw TTM at trough* will infer absurd rebounds. The cap will trigger or the model will produce wildly optimistic outputs.

**The MXL case is exactly this.** MXL is a fabless semi at a cyclical trough. Its TTM revenue is depressed; if we just project 50%/yr growth off the trough, the year-5 projected revenue is hugely inflated. The model output of $107/share is plausibly *too high* — the analyst projection is itself reflecting cyclical recovery bias.

**Replacement design:** the assumption profile carries a `revenue_base` policy:

```
revenue_base ∈ {
    raw_ttm,                  // default
    two_year_average,         // for cyclicals: avg of TTM and TTM-1y
    max_ttm_or_floor,         // floor = 5y revenue average; max(TTM, floor) prevents trough-base
    mid_cycle_normalized      // for deeply cyclical (oil & gas, some semis): mid-cycle revenue
}
```

For MXL, with `archetype=cyclical, maturity=mid-cycle`, the profile would set `revenue_base = max_ttm_or_floor` and the forward projection uses the larger of (current TTM, 5-year average). This is more conservative than projecting off trough.

This is the single most important addition. Without it, the forward model is **biased upward for trough-cyclicals** — which is exactly the population of negative-OI tickers it most often runs on.

### 4. Year-5 multiple is a terminal value; make it explicit

**Architectural finding** (per `thinkdeep`): applying the sector multiple at year 5 IS effectively a terminal value. The math is conventional. But making it implicit hides three failure modes:
1. **Double optimism**: strong year-5 growth + generous terminal multiple = compounded bias upward.
2. **Unstabilized terminal**: if growth is still 30%+/year at year 5, year 5 is NOT a steady state. Applying a stable-state multiple to a not-stable year is wrong.
3. **No transparency**: consumers can't see whether the model assumed a stabilized terminal or just slapped the multiple on whatever year-5 happened to look like.

**Replacement design:** add explicit terminal-value handling. Two new fields on the model result:
```
terminal_method:    "exit_multiple"       // applied at horizon
terminal_year:      5                     // when (year offset from base)
stabilized:         true | false          // is the terminal year a steady state?
fade_years:         0 | 1 | 2             // additional years of fade before terminal
```

If `stabilized: false` AND high-growth profile, add a 1-2 year **fade bridge** between the explicit forecast and the terminal. The fade applies linear deceleration in growth rate from year-5 to year-(5+fade_years), then the terminal multiple is applied to the faded year. This adds 5-10 lines of code and addresses the unstabilized-terminal failure mode.

For MXL: profile `(cyclical, mid-cycle)` would NOT stabilize at year 5 (semi cycles are 3-4 years; year 5 might be peak or trough). Set `stabilized: false, fade_years: 2`. Project years 1-5, then years 6-7 with linearly-decelerating growth, then apply the terminal multiple at year 7.

### 5. Year-by-year vs. single-shot discount

**Original proposal:** discount the year-5 EV back to t=0 using a single discount factor `(1+WACC)^5`.

**Research finding**: for **revenue-multiple forward valuation specifically**, single-shot at horizon is the conventional approach (no annual cash flows to discount). The Damodaran year-by-year FCFF approach applies to full DCF, not revenue-multiple. **My original proposal was correct on this point.**

The fade bridge from §4 above means the "single shot" is actually `(1+WACC)^(5+fade_years)`, but it's still one discount factor.

---

## Updated routing logic

Replacing the simple-gates predicate from the original sketch:

```go
// shouldUseForwardRevenueMultiple returns the assumption profile to use for
// forward valuation, or nil if the trailing model should be used.
//
// Profile resolution: archetype × maturity is derived from classifier
// outputs (industry SIC) and balance-sheet signals (revenue trajectory,
// margin profile, asset intensity). Falls back to (software-like, scaling)
// when classification is uncertain.
func shouldUseForwardRevenueMultiple(
    g *entities.GrowthEstimate,
    wacc float64,
    fd *entities.FinancialData,
    industry string,
) (*AssumptionProfile, string /*reason*/) {
    if g == nil || g.Confidence == "" || g.Confidence == "low" {
        return nil, "growth_confidence_too_low"
    }
    if wacc <= 0 || wacc >= 0.50 {
        return nil, "wacc_out_of_sane_range"
    }

    profile := DeriveAssumptionProfile(industry, fd)
    if len(g.ProjectedGrowthRates) < profile.HorizonYears {
        return nil, "insufficient_growth_horizon"
    }

    // Apply profile-keyed compound cap (replaces the flat 25×).
    compound := 1.0
    for i := 0; i < profile.HorizonYears; i++ {
        compound *= 1 + g.ProjectedGrowthRates[i]
    }
    if compound > profile.CompoundGrowthCap {
        return nil, "compound_growth_exceeds_archetype_cap"
    }

    // Path constraint: growth must fade. Reject if rate at year HorizonYears-1
    // is more than 90% of the rate at year 0 (i.e. growth not decaying).
    if g.ProjectedGrowthRates[profile.HorizonYears-1] > g.ProjectedGrowthRates[0] * 0.90 {
        return nil, "growth_path_does_not_fade"
    }

    return &profile, "ok"
}
```

The string `reason` flows into the response as `route_reason`, giving consumers full visibility into why a particular path fired.

## Updated diagnostics

Per `thinkdeep`'s repeated emphasis on auditability, every result MUST carry:

| Field | Type | Example |
|---|---|---|
| `selected_model` | string | `"forward_revenue_multiple"` |
| `route_reason` | string | `"ok"`, `"growth_confidence_too_low"`, `"compound_growth_exceeds_archetype_cap"`, etc. |
| `assumption_profile` | string | `"cyclical_mid_cycle"`, `"software_like_scaling"`, etc. |
| `cap_applied` | float | `2.0` (the compound-growth cap from the profile) |
| `horizon_selected` | int | `5` or `7` |
| `revenue_base_method` | string | `"raw_ttm"`, `"max_ttm_or_floor"` |
| `terminal_method` | string | `"exit_multiple"` |
| `terminal_year` | int | `5` (or `7` if fade applied) |
| `stabilized` | bool | `true` or `false` |
| `fade_years` | int | `0`, `1`, or `2` |

These ride alongside the existing `calculation_method`, `calculation_version` fields on the response. Replay tooling, dashboards, and audit trails get full visibility.

## Updated test invariants

Per `thinkdeep`, regression fixtures across archetypes are required, and these invariants must hold:

| Invariant | Test |
|---|---|
| Higher discount rate → lower per-share value | Hold all else; double WACC; result must shrink |
| Higher dilution → lower per-share value | Hold all else; double diluted shares; result must shrink linearly |
| Higher terminal multiple → higher per-share value | Hold all else; bump multiple; result must grow |
| Stronger growth → higher per-share value | UNLESS the cap fires (in which case the test should assert cap_applied flag) |
| Cyclical at trough vs. mid-cycle: trough produces SMALLER per-share value than mid-cycle (because revenue_base normalization caps the trough projection) | Pin per-archetype |

## Architecture / interface details

**File layout (new):**
```
internal/services/valuation/models/
  forward_revenue_multiple.go        (NEW — ~150 lines)
  forward_revenue_multiple_test.go   (NEW — ~250 lines)
  router.go                          (MODIFIED — ~10 lines added for routing predicate + helper `shouldUseForwardRevenueMultiple()`)
  router_test.go                     (MODIFIED — ~5 new test cases for the new branch)
```

**Routing predicate:**
```go
func shouldUseForwardRevenueMultiple(g *entities.GrowthEstimate, wacc float64) bool {
    if g == nil || len(g.ProjectedGrowthRates) < 5 { return false }
    if g.Confidence == "" || g.Confidence == "low" { return false }
    if wacc <= 0 || wacc >= 0.50 { return false }
    // Sanity-cap on projected growth: reject if compound growth over 5y > 25× (i.e., annual avg > ~85%).
    compound := 1.0
    for i := 0; i < 5; i++ { compound *= 1 + g.ProjectedGrowthRates[i] }
    if compound > 25.0 { return false }
    return true
}
```

**ModelInput contract:** uses fields the engine already populates (`HistoricalData`, `Industry`, `WACC`, `GrowthEstimate`, `SharesOutstanding`, `InterestBearingDebt`, `CashAndCashEquivalents`). No new input plumbing.

**ModelResult contract:** same as today; just `ModelType: "forward_revenue_multiple"`. Confidence reported as the growth-estimate confidence (not the model's own — the model is deterministic given inputs).

**Sensitivity / sanity guards:**
1. Compound-growth cap (25× over 5y rejects analyst-noise-driven blowups).
2. WACC sanity range [0, 0.50). Below 0 is a bug; above 50% suggests a data error in beta.
3. Negative `valuePerShare` clamped to 0 (matches existing model behaviour).
4. Floor-clamping of forward revenue at TTM-trailing if the projected number is *less* than today's revenue (analyst blend can occasionally project decline; in that case, forward = trailing is the conservative move).

**Cross-check integration:** Once forward-revenue-multiple lands, extend `crosscheck.go` to compute implied EV/Revenue and EV/EBITDA at year 5 and compare against sector medians. Out of scope for this tracker; track as RM-3.A.

## Tests required

| # | Scenario | Expected behaviour |
|---|---|---|
| FR1 | High-confidence growth, healthy WACC, full 5-year horizon | Routes to forward_revenue_multiple; output > trailing model output |
| FR2 | Low-confidence growth | Routes to trailing revenue_multiple (regression-pin against today's behaviour) |
| FR3 | Missing WACC (NaN or 0) | Routes to trailing |
| FR4 | WACC > 0.50 (degenerate beta) | Routes to trailing + warning |
| FR5 | Compound growth >25× over 5y (analyst noise) | Routes to trailing |
| FR6 | <5 years of growth horizon | Routes to trailing |
| FR7 | MXL fixture with TTM revenue + MFG_SEMI multiplier | Output ~$107 (sanity-pinned to ±10% of recomputed value) |
| FR8 | Healthy SaaS fixture (positive forward growth, sector multiplier) | Output reflects PV of EV at year 5 / forward sector-multiple |
| FR9 | Forward-projected revenue < trailing TTM | Falls back to trailing (don't punish for analyst miscalibration) |
| FR10 | Negative WACC (impossible but defensive) | Routes to trailing + warning |
| FR11 | Boundary: WACC == 0 exactly | Routes to trailing (avoids divide-by-zero in compound) |

Coverage target: 100% on the new file (per CLAUDE.md ≥90% finance floor; the model is small enough to hit 100%).

## Out of scope

- Cross-check integration for forward-revenue-multiple outputs (track as RM-3.A).
- Two-stage forward models (5-year explicit + perpetual terminal). The router could in principle pick a 2-stage model when confidence is high AND the growth curve has a clean terminal handoff — but that's closer to a DCF than a revenue multiple. Track as RM-3.B if needed.
- Sector-aware projection horizons (some sectors warrant 7-year explicit projections, e.g. biotech with long clinical timelines). Track as RM-3.C.
- Damodaran's "stable growth" forward sector multiples — if Damodaran publishes them in a future dataset, the forward model could use a different multiple at year 5 than at year 1. RM-2 Phase 2 lays the groundwork.

## Acceptance for closing this tracker

- [x] RM-1 (TTM revenue helper) has landed. Shipped via merge `cfdf7b4` (entity helper `2428ae1` + consumer wire-up `3902703` + V/R/Q bridge-ordering follow-up `9da6c68`); RM-1.A T7 stale-data follow-up via `9a32d94`. See `docs/reviewer/RM-1-revenue-multiple-quarterly-vs-ttm.md` for the closeout status.
- [ ] RM-2 Phase 1 (missing sector buckets including MFG_SEMI) has landed.
- [ ] `internal/services/valuation/models/forward_revenue_multiple.go` ships with the algorithm above.
- [ ] Router (`models/router.go`) selects forward when `shouldUseForwardRevenueMultiple()` returns true; selects trailing otherwise.
- [ ] All 11 unit-test cases (FR1–FR11) pass; coverage on new file ≥90%.
- [ ] Live MXL response shows `calculation_method: "forward_revenue_multiple"` and a per-share value in the $80-120 range (instead of today's $1.32 → $37 after RM-1+RM-2 → $107 after RM-3).
- [ ] Live AAPL response is unchanged (positive-OI; never routes to revenue-multiple of either flavour).
- [ ] Unit test pinning that low-confidence growth routes to trailing (no regression).
- [ ] CLAUDE.md "Common Gotchas" documents the new model selection rules.
- [ ] Spec doc `docs/refactoring/spec/valuation-engine-upgrade-spec.md` updated to mention forward-revenue-multiple alongside the existing DCF/DDM/FFO models.
- [ ] CHANGELOG entry: "added forward_revenue_multiple model for high-confidence-growth negative-OI tickers; trailing revenue_multiple retained as low-confidence fallback".
