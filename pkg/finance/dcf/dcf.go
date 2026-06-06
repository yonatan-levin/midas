package dcf

import (
	"errors"
	"fmt"
	"math"
)

// Inputs represents all inputs needed for DCF calculation
type Inputs struct {
	// Base financial data
	BaseOperatingIncome float64   // Current normalized operating income
	GrowthRate          float64   // Projected annual growth rate (backward-compatible single rate)
	GrowthRates         []float64 // Per-year growth rates (optional, overrides GrowthRate when non-empty)
	TerminalGrowthRate  float64   // Long-term perpetual growth rate
	WACC                float64   // Weighted Average Cost of Capital (discount rate)
	TaxRate             float64   // Effective tax rate

	// Projection parameters
	ProjectionYears int // Number of explicit forecast years (typically 5-7)

	// Optional: Capital expenditure and working capital assumptions (legacy, percentage-based)
	CapexAsPercentOfRevenue float64 // CapEx as % of revenue (for FCF calculation)
	WorkingCapitalChange    float64 // Annual working capital change
	DepreciationRate        float64 // Depreciation as % of revenue

	// True FCF components (preferred over percentage-based when available).
	// FCF = NOPAT + D&A - CapEx - deltaWC
	DepreciationAndAmortization float64 // Actual D&A amount to add back (non-cash)
	CapitalExpenditures         float64 // Actual CapEx amount to subtract (cash outflow, positive value)
	NetWorkingCapitalChange     float64 // Change in NWC (positive = cash consumed)
	UseTrueFCF                  bool    // When true, use actual D&A/CapEx instead of percentage-based

	// Exit multiple terminal value (Phase 4: cross-check).
	// When non-zero, the terminal value is averaged between Gordon Growth TV
	// and an exit-multiple-based TV to reduce single-model dependency.
	ExitMultiple float64 // Sector median EV/EBITDA multiple; 0 = use Gordon Growth only

	// --- Layer A: reinvestment / operating-leverage projection (DCF-path only) ---
	//
	// When ReinvestmentMethod is "" or "legacy_proportional", the projection uses
	// the existing proportional × growthFactor scaling and ALL of the fields below
	// are ignored — output is bit-for-bit identical to the pre-Layer-A engine.
	//
	// When ReinvestmentMethod is "sales_to_capital" or "declining_capex_intensity"
	// (and BaseRevenue > 0), a unified reinvestment term replaces the proportional
	// CapEx/ΔWC/D&A scaling so projected FCF = NOPAT_t − Reinvestment_t can cross
	// from negative to positive WITHIN the explicit horizon for reinvestment-heavy,
	// scaling firms (spec docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md §5).
	//
	// All fields are plain numerics so pkg/finance/dcf stays config-free; the
	// service layer derives them from the resolved AssumptionProfile.
	ReinvestmentMethod string // "" | "legacy_proportional" | "sales_to_capital" | "declining_capex_intensity"

	// Revenue series anchors. Revenue_0 = BaseRevenue; Revenue_t = Revenue_{t-1}×(1+g_t)
	// where g_t comes from RevenueGrowthRates (falling back to GrowthRates/GrowthRate).
	BaseRevenue        float64   // TTM / normalized revenue base
	RevenueGrowthRates []float64 // per-year revenue growth; empty ⇒ reuse GrowthRates

	// sales_to_capital path: Reinvestment_t = ΔRevenue_t / SalesToCapital_t, where
	// SalesToCapital_t RISES (efficiency improves) from start → target over the fade.
	SalesToCapitalStart  float64
	SalesToCapitalTarget float64

	// declining_capex_intensity fallback path: Reinvestment_t = NetReinvestIntensity_t × Revenue_t,
	// where NetReinvestIntensity_t DECLINES from start → mature norm over the fade.
	// Interpreted as a NET-reinvestment-to-revenue ratio so it stays in the unified
	// FCF = NOPAT − Reinvestment frame and avoids the §5.3 "hybrid trap".
	CapExIntensityStart  float64
	CapExIntensityMature float64

	ReinvestmentFadeYears int     // years over which sales-to-capital / capex-intensity reaches target
	MaintenanceCapexFloor float64 // §7.1 — reinvestment may not fall below this × Revenue_t

	// Margin-convergence path. OperatingMargin_t = BaseOperatingMargin +
	// (TargetOperatingMargin − BaseOperatingMargin) × min(t/MarginConvergenceYears, 1).
	// NOPAT_t = Revenue_t × OperatingMargin_t × (1 − TaxRate). When base == target
	// (flat margin) the NOPAT path reduces to BaseOperatingIncome scaled by revenue
	// growth — identical to the legacy OI-growth path when revenue growth == OI growth.
	BaseOperatingMargin    float64 // = BaseOperatingIncome / BaseRevenue (pre-tax operating margin)
	TargetOperatingMargin  float64 // archetype/industry-capped ceiling
	MarginConvergenceYears int     // years over which margin expands base → target
}

// Projection represents cash flow projection for a single year
type Projection struct {
	Year              int     `json:"year"`
	OperatingIncome   float64 `json:"operating_income"`
	NOPAT             float64 `json:"nopat"`               // Net Operating Profit After Tax
	FreeCashFlow      float64 `json:"free_cash_flow"`      // FCF to firm
	DiscountFactor    float64 `json:"discount_factor"`     // (1 + WACC)^year
	PresentValue      float64 `json:"present_value"`       // FCF / discount factor
	GrowthRateApplied float64 `json:"growth_rate_applied"` // Growth rate used for this year
}

// Result contains the complete DCF valuation result
type Result struct {
	// Core valuation
	EnterpriseValue     float64 `json:"enterprise_value"`      // Sum of all discounted cash flows
	TerminalValue       float64 `json:"terminal_value"`        // Present value of terminal value
	ExplicitPeriodValue float64 `json:"explicit_period_value"` // PV of explicit forecast years

	// Detailed projections
	Projections []Projection `json:"projections"` // Year-by-year projections

	// Terminal value details
	TerminalYearFCF float64 `json:"terminal_year_fcf"` // FCF in final explicit year
	// ExitMultipleTV is the raw exit-multiple terminal value component, before
	// averaging with Gordon Growth TV. Zero when ExitMultiple input is 0
	// (Gordon-only path). Surfaced on the "terminal_value" calc trace so
	// operators can audit the two TV estimates separately. Added per M-1c.
	ExitMultipleTV       float64 `json:"exit_multiple_tv,omitempty"`
	TerminalValueNominal float64 `json:"terminal_value_nominal"` // Terminal value before discounting
	// GordonTV is the Gordon-Growth terminal value component (nominal, before
	// discounting and before any exit-multiple averaging). Surfaced so the
	// valuation service can render the "terminal_value" calc trace without
	// re-deriving it (the legacy path's re-derivation assumed terminalFCF =
	// TerminalYearFCF×(1+g), which is incorrect on the Layer-A reinvestment
	// path where terminal FCF is derived from terminal growth + ROIC). Equal to
	// TerminalValueNominal on the Gordon-only path.
	GordonTV float64 `json:"gordon_tv"`

	// Input validation and quality
	IsReasonable bool     `json:"is_reasonable"`      // Sanity check result
	Warnings     []string `json:"warnings,omitempty"` // Any calculation warnings

	// Calculation metadata
	ProjectionYears    int     `json:"projection_years"`
	GrowthRate         float64 `json:"growth_rate"`
	TerminalGrowthRate float64 `json:"terminal_growth_rate"`
	WACC               float64 `json:"wacc"`
}

// CalculateDCF performs the complete DCF valuation
func CalculateDCF(inputs Inputs) (*Result, error) {
	if err := validateInputs(inputs); err != nil {
		return nil, err
	}

	result := &Result{
		ProjectionYears:    inputs.ProjectionYears,
		GrowthRate:         inputs.GrowthRate,
		TerminalGrowthRate: inputs.TerminalGrowthRate,
		WACC:               inputs.WACC,
		Projections:        make([]Projection, inputs.ProjectionYears),
		Warnings:           []string{},
	}

	// Generate yearly projections
	currentOperatingIncome := inputs.BaseOperatingIncome
	currentRevenue := inputs.BaseRevenue
	useReinv := useReinvestmentModel(inputs)
	floorClampWarned := false
	explicitPeriodValue := 0.0

	for year := 1; year <= inputs.ProjectionYears; year++ {
		// Select growth rate: per-year if available, otherwise single rate. On
		// the reinvestment path a dedicated revenue-growth slice wins when set;
		// otherwise both paths share GrowthRates (legacy behavior unchanged).
		rateForYear := inputs.GrowthRate
		if useReinv && len(inputs.RevenueGrowthRates) >= year {
			rateForYear = inputs.RevenueGrowthRates[year-1]
		} else if len(inputs.GrowthRates) >= year {
			rateForYear = inputs.GrowthRates[year-1]
		}

		if useReinv {
			// --- Layer A: unified reinvestment / operating-leverage projection ---
			// Revenue compounds at the growth rate; the operating margin converges
			// base→target; NOPAT = Revenue × margin × (1−tax); reinvestment is a
			// single unified term (§5.2). FCF = NOPAT − Reinvestment can cross
			// positive in-window because reinvestment efficiency improves while
			// growth fades — decoupling the FCF sign from the base year (§4).
			prevRevenue := currentRevenue
			currentRevenue = prevRevenue * (1 + rateForYear)

			marginFrac := convergeFraction(year, inputs.MarginConvergenceYears)
			margin := inputs.BaseOperatingMargin + (inputs.TargetOperatingMargin-inputs.BaseOperatingMargin)*marginFrac
			operatingIncome := currentRevenue * margin
			nopat := operatingIncome * (1 - inputs.TaxRate)

			reinvest, clamped := reinvestmentForYear(inputs, year, currentRevenue, prevRevenue)
			if clamped && !floorClampWarned {
				result.Warnings = append(result.Warnings,
					"maintenance_capex_floor: projected reinvestment clamped up to the maintenance-capex floor; FCF reflects the floor, not the lower modeled reinvestment")
				floorClampWarned = true
			}
			freeCashFlow := nopat - reinvest

			discountFactor := math.Pow(1+inputs.WACC, float64(year))
			presentValue := freeCashFlow / discountFactor
			result.Projections[year-1] = Projection{
				Year:              year,
				OperatingIncome:   operatingIncome,
				NOPAT:             nopat,
				FreeCashFlow:      freeCashFlow,
				DiscountFactor:    discountFactor,
				PresentValue:      presentValue,
				GrowthRateApplied: rateForYear,
			}
			explicitPeriodValue += presentValue
			continue
		}

		// --- Legacy proportional projection (unchanged; bit-for-bit) ---
		// Apply growth to operating income
		currentOperatingIncome *= (1 + rateForYear)

		// Calculate NOPAT (Net Operating Profit After Tax)
		nopat := currentOperatingIncome * (1 - inputs.TaxRate)

		// Calculate Free Cash Flow
		var freeCashFlow float64

		if inputs.UseTrueFCF {
			// True FCF: NOPAT + D&A - CapEx - delta_WC
			// D&A and CapEx scale proportionally with operating income growth.
			growthFactor := currentOperatingIncome / inputs.BaseOperatingIncome
			scaledDA := inputs.DepreciationAndAmortization * growthFactor
			scaledCapEx := inputs.CapitalExpenditures * growthFactor
			scaledNWCChange := inputs.NetWorkingCapitalChange * growthFactor

			freeCashFlow = nopat + scaledDA - scaledCapEx - scaledNWCChange
		} else if inputs.CapexAsPercentOfRevenue > 0 || inputs.WorkingCapitalChange != 0 {
			// Legacy percentage-based approximation
			grossInvestment := currentOperatingIncome * inputs.CapexAsPercentOfRevenue
			freeCashFlow = nopat - grossInvestment - inputs.WorkingCapitalChange
		} else {
			// Fallback: FCF = NOPAT (no reinvestment data available)
			freeCashFlow = nopat
		}

		// Calculate discount factor and present value
		discountFactor := math.Pow(1+inputs.WACC, float64(year))
		presentValue := freeCashFlow / discountFactor

		// Store projection
		result.Projections[year-1] = Projection{
			Year:              year,
			OperatingIncome:   currentOperatingIncome,
			NOPAT:             nopat,
			FreeCashFlow:      freeCashFlow,
			DiscountFactor:    discountFactor,
			PresentValue:      presentValue,
			GrowthRateApplied: rateForYear,
		}

		explicitPeriodValue += presentValue
	}

	result.ExplicitPeriodValue = explicitPeriodValue

	// Calculate terminal value using Gordon Growth Model
	finalYearProjection := result.Projections[inputs.ProjectionYears-1]
	result.TerminalYearFCF = finalYearProjection.FreeCashFlow

	// Terminal value = terminalFCF / (WACC - terminal growth).
	// The minimum WACC-vs-terminal spread (MinWACCTerminalSpread) is enforced by
	// validateInputs, and earlier as a typed 422 by the valuation resolver.
	var gordonTV float64
	if useReinv {
		// §7.3 terminal consistency: derive the perpetuity reinvestment from
		// terminal growth + the MATURED sales-to-capital (or capex-intensity
		// mature norm), NOT the final explicit-year FCF — which still carries the
		// last explicit year's elevated growth and reinvestment. Using the matured
		// efficiency makes the terminal reinvestment rate equal
		// terminal_growth / terminal_ROIC by construction (ROIC = after-tax target
		// margin × sales-to-capital), so a firm growing at g in perpetuity also
		// reinvests exactly the capital that g requires.
		//
		// The §7.1 maintenance-capex floor is deliberately NOT applied here: it is
		// an EXPLICIT-window guardrail (§7.1 "enforced in the projection loop")
		// against the fade curve manufacturing implausibly-high early FCF, and in
		// the net-reinvestment frame maintenance capex is already netted out — so a
		// terminal floor would double-count it and silently break the g/ROIC
		// identity above. The terminal efficiency deliberately snaps to the matured
		// ratio even when ReinvestmentFadeYears exceeds the explicit horizon
		// (§7.3 forbids free-running the fade past the horizon).
		revNext := currentRevenue * (1 + inputs.TerminalGrowthRate)
		nopatNext := revNext * inputs.TargetOperatingMargin * (1 - inputs.TaxRate)
		reinvestNext := terminalReinvestment(inputs, revNext)
		terminalFCF := nopatNext - reinvestNext
		gordonTV = terminalFCF / (inputs.WACC - inputs.TerminalGrowthRate)
	} else {
		// Legacy: terminalFCF = FCF(final year) × (1 + terminal growth).
		terminalFCF := result.TerminalYearFCF * (1 + inputs.TerminalGrowthRate)
		gordonTV = terminalFCF / (inputs.WACC - inputs.TerminalGrowthRate)
	}
	result.GordonTV = gordonTV
	result.TerminalValueNominal = gordonTV

	// When an exit multiple is provided, average Gordon Growth TV with exit-multiple TV.
	// This reduces model risk by blending two independent terminal value estimates.
	if inputs.ExitMultiple > 0 {
		// Terminal EBITDA = terminal year OI + scaled D&A.
		// We use the projection's OI directly (already grown) and scale D&A
		// by the same growth factor to reflect terminal-year magnitude.
		terminalOI := finalYearProjection.OperatingIncome
		growthFactor := 1.0
		if inputs.BaseOperatingIncome > 0 {
			growthFactor = terminalOI / inputs.BaseOperatingIncome
		}
		scaledDA := inputs.DepreciationAndAmortization * growthFactor
		terminalEBITDA := terminalOI + scaledDA
		if terminalEBITDA > 0 {
			exitMultipleTV := terminalEBITDA * inputs.ExitMultiple
			// Persist the raw exit-multiple TV component so the valuation service can
			// surface it on the "terminal_value" calc trace alongside Gordon Growth TV.
			// Zero when this branch isn't taken — omitempty keeps JSON clean. (M-1c)
			result.ExitMultipleTV = exitMultipleTV
			result.TerminalValueNominal = (gordonTV + exitMultipleTV) / 2
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Terminal value averaged: Gordon Growth (%.0f) and Exit Multiple %.1fx (%.0f)",
					gordonTV, inputs.ExitMultiple, exitMultipleTV))
		}
	}

	// Discount terminal value to present
	terminalDiscountFactor := math.Pow(1+inputs.WACC, float64(inputs.ProjectionYears))
	result.TerminalValue = result.TerminalValueNominal / terminalDiscountFactor

	// Calculate total enterprise value
	result.EnterpriseValue = result.ExplicitPeriodValue + result.TerminalValue

	// Perform reasonableness checks
	result.IsReasonable = isResultReasonable(result)
	// Append generated warnings to any warnings already accumulated (e.g., from exit multiple averaging)
	result.Warnings = append(result.Warnings, generateWarnings(inputs, result)...)

	return result, nil
}

// CalculateEquityValue applies the EV → equity bridge. The standard
// formula is EV - Debt + Cash; M-1d adds the two correction terms that
// matter for accuracy on companies with significant non-controlling
// interest or preferred stock outstanding.
//
//	Common Equity = EV - Debt + Cash - MinorityInterest - PreferredEquity
//
// For tickers without minority interest or preferred stock the new
// terms are zero and per-share output is unchanged versus the prior
// signature.
func CalculateEquityValue(enterpriseValue, debt, cash, minorityInterest, preferredEquity float64) float64 {
	return enterpriseValue - debt + cash - minorityInterest - preferredEquity
}

// CalculateEquityValueWithDebtLikeClaims extends the EV → equity bridge with a
// sixth term: debt-like claims that compete with shareholders for enterprise
// cash flows but are not part of the interest-bearing capital structure
// (DC-1 Phase 4 B3 routing flip + B1/B2 reroute):
//
//	Common Equity = EV - Debt + Cash - MinorityInterest - PreferredEquity - DebtLikeClaims
//
// debtLikeClaims is the sum of the B1 (capitalized operating lease), B2
// (pension underfunding), and B3 (contingent liability) overlay amounts, read
// from cleaneddata.InvestedCapital().DebtLikeClaims. Before Phase 4 those
// amounts were (incorrectly) folded into the interest-bearing debt term AND
// missing from this subtraction; Phase 4 corrects both — they are subtracted
// here and excluded from the WACC capital-structure denominator.
//
// ADDITIVE to the legacy 5-arg CalculateEquityValue: the alt-model
// revenue_multiple / ffo paths and existing tests keep the 5-arg signature
// (they build equity differently and have no B-rule story today). When
// debtLikeClaims == 0 (no B1/B2/B3 fired) this is identical to the 5-arg form.
func CalculateEquityValueWithDebtLikeClaims(enterpriseValue, debt, cash, minorityInterest, preferredEquity, debtLikeClaims float64) float64 {
	return CalculateEquityValue(enterpriseValue, debt, cash, minorityInterest, preferredEquity) - debtLikeClaims
}

// CalculateValuePerShare converts equity value to per-share value
func CalculateValuePerShare(equityValue, sharesOutstanding float64) (float64, error) {
	if sharesOutstanding <= 0 {
		return 0, errors.New("shares outstanding must be positive")
	}
	return equityValue / sharesOutstanding, nil
}

// SensitivityAnalysis performs sensitivity analysis on key variables
func SensitivityAnalysis(baseInputs Inputs, waccRange []float64, growthRange []float64) ([][]float64, error) {
	results := make([][]float64, len(waccRange))

	for i, wacc := range waccRange {
		results[i] = make([]float64, len(growthRange))

		for j, growth := range growthRange {
			inputs := baseInputs
			inputs.WACC = wacc
			inputs.GrowthRate = growth

			result, err := CalculateDCF(inputs)
			if err != nil {
				return nil, err
			}

			results[i][j] = result.EnterpriseValue
		}
	}

	return results, nil
}

// MinWACCTerminalSpread is the minimum required gap between WACC and the terminal
// growth rate. Below this gap the Gordon perpetuity denominator (WACC - g) becomes
// numerically unstable and the terminal value explodes. The valuation resolver
// enforces the SAME spread BEFORE CalculateDCF runs (see
// internal/services/valuation/params.ResolveTerminal), upgrading an explicit
// near-WACC terminal_growth_rate into a typed 422 — so this guard should never
// fire from the override path. It is kept as defense-in-depth: a 500 surfacing
// here now indicates a real internal bug, not a caller-supplied input.
const MinWACCTerminalSpread = 0.01

// Helper functions

// --- Layer A: reinvestment / operating-leverage helpers ---

// useReinvestmentModel reports whether the Layer-A unified reinvestment
// projection should run. It requires an explicit non-legacy method AND a
// positive revenue base (the reinvestment term projects off revenue). Absent
// either, the projection falls back to the legacy proportional path so the
// engine never divides by a missing base — this is the runtime half of the
// legacy_proportional opt-out (the config half is an unset/legacy method).
func useReinvestmentModel(inputs Inputs) bool {
	switch inputs.ReinvestmentMethod {
	case "sales_to_capital", "declining_capex_intensity":
		return inputs.BaseRevenue > 0
	default:
		return false
	}
}

// convergeFraction returns the linear convergence fraction at year t for a path
// that reaches its target at `years`, clamped to [0,1]. years <= 0 means the
// target is reached immediately (fraction 1.0) — used for flat (mature) profiles.
func convergeFraction(year, years int) float64 {
	if years <= 0 {
		return 1.0
	}
	f := float64(year) / float64(years)
	if f > 1.0 {
		return 1.0
	}
	return f
}

// reinvestmentForYear computes the explicit-year Layer-A reinvestment and reports
// whether the §7.1 maintenance-capex floor clamped it up. Both methods yield a
// NET reinvestment in the unified FCF = NOPAT − Reinvestment frame:
//
//   - sales_to_capital:          Reinvestment_t = ΔRevenue_t / SalesToCapital_t,
//     SalesToCapital_t rising start→target over the fade.
//   - declining_capex_intensity: Reinvestment_t = NetIntensity_t × Revenue_t,
//     NetIntensity_t declining start→mature over the fade.
//
// The floor (MaintenanceCapexFloor × Revenue) is a LOWER bound on reinvestment —
// a firm cannot grow on sub-maintenance capital, so it prevents the taper from
// manufacturing implausibly-high early FCF (§7.1).
func reinvestmentForYear(inputs Inputs, year int, revenue, prevRevenue float64) (reinvest float64, clamped bool) {
	fadeFrac := convergeFraction(year, inputs.ReinvestmentFadeYears)
	switch inputs.ReinvestmentMethod {
	case "declining_capex_intensity":
		intensity := inputs.CapExIntensityStart + (inputs.CapExIntensityMature-inputs.CapExIntensityStart)*fadeFrac
		reinvest = intensity * revenue
	default: // sales_to_capital
		s2c := inputs.SalesToCapitalStart + (inputs.SalesToCapitalTarget-inputs.SalesToCapitalStart)*fadeFrac
		if s2c <= 0 {
			// Defensive: validation keeps target ≥ start > 0, but never divide by
			// a non-positive ratio if a caller bypassed validation.
			s2c = inputs.SalesToCapitalTarget
		}
		reinvest = (revenue - prevRevenue) / s2c
	}
	return clampToFloor(reinvest, inputs.MaintenanceCapexFloor*revenue)
}

// terminalReinvestment computes the perpetuity (year n+1) reinvestment using the
// MATURED efficiency (sales-to-capital target / capex-intensity mature). revNext is
// the first perpetuity-year revenue (= Revenue_n×(1+g)).
//
// For sales_to_capital the reinvestment is the capital that funds the NEXT
// period's growth off the perpetuity base — Revenue_{n+1}×g / SalesToCapital —
// which makes the terminal reinvestment rate equal terminal_growth / terminal_ROIC
// EXACTLY (Damodaran stable-growth FCFF, §7.3), since
// ROIC = after-tax target margin × sales-to-capital.
//
// The §7.1 maintenance-capex floor is NOT applied to the terminal (see the call
// site): the floor is an explicit-window guardrail, and a terminal floor would
// double-count maintenance (already netted in this net-reinvestment frame) and
// break the g/ROIC identity that §7.3 requires.
func terminalReinvestment(inputs Inputs, revNext float64) float64 {
	switch inputs.ReinvestmentMethod {
	case "declining_capex_intensity":
		return inputs.CapExIntensityMature * revNext
	default: // sales_to_capital
		s2c := inputs.SalesToCapitalTarget
		if s2c <= 0 {
			s2c = 1.0
		}
		return revNext * inputs.TerminalGrowthRate / s2c
	}
}

// clampToFloor raises reinvest to floor when it would fall below it, reporting
// whether the clamp fired.
func clampToFloor(reinvest, floor float64) (float64, bool) {
	if reinvest < floor {
		return floor, true
	}
	return reinvest, false
}

// validateInputs checks that all inputs are within reasonable ranges.
//
// The numeric bounds are reconciled with the request-override contract (the Layer-1
// ranges in internal/api/v1/handlers/fair_value_validation.go). Any override value
// the contract accepts MUST compute here rather than producing an untyped error →
// HTTP 500. The widening only changes WHICH inputs are rejected; it never alters the
// output for an already-accepted input, so the default (no-override) path stays
// byte-for-byte identical. The two MATHEMATICAL guards (WACC > 0 and the
// WACC-vs-terminal spread) are retained as defense-in-depth; the valuation resolver
// catches both as typed 422s before the engine runs.
func validateInputs(inputs Inputs) error {
	if inputs.BaseOperatingIncome <= 0 {
		return errors.New("base operating income must be positive")
	}

	// Growth rate: contraction floored at -100% (revenue base shrinks to zero but
	// not negative); ceiling at 10× (1000% CAGR) to catch unit errors. Contract
	// range [-1.0, 10.0] (max/min_growth).
	if inputs.GrowthRate < -1.0 || inputs.GrowthRate > 10.0 {
		return errors.New("growth rate must be between -100% and 1000%")
	}

	// Terminal growth rate: negative terminal growth (real-terms contraction) is a
	// supported, first-class scenario. Contract range [-20%, 50%].
	if inputs.TerminalGrowthRate < -0.20 || inputs.TerminalGrowthRate > 0.50 {
		return errors.New("terminal growth rate must be between -20% and 50%")
	}

	// WACC must be strictly positive for the discount factors and Gordon denominator
	// to be well-defined. No upper rail: a large-but-positive WACC discounts heavily
	// but computes correctly (a high-WACC advisory is surfaced via generateWarnings),
	// so we do not 500 a WACC purely for being large.
	if inputs.WACC <= 0 {
		return errors.New("WACC must be positive")
	}

	if inputs.WACC-inputs.TerminalGrowthRate < MinWACCTerminalSpread {
		return fmt.Errorf("WACC (%.2f%%) must exceed terminal growth rate (%.2f%%) by at least %.0f%%",
			inputs.WACC*100, inputs.TerminalGrowthRate*100, MinWACCTerminalSpread*100)
	}

	// Tax rate: negative effective rates are real (NOLs, credits). Contract range [-50%, 100%].
	if inputs.TaxRate < -0.5 || inputs.TaxRate > 1 {
		return errors.New("tax rate must be between -50% and 100%")
	}

	// Projection years: contract range [1, 50] (horizon_years).
	if inputs.ProjectionYears < 1 || inputs.ProjectionYears > 50 {
		return errors.New("projection years must be between 1 and 50")
	}

	return nil
}

func isResultReasonable(result *Result) bool {
	// Check if enterprise value is reasonable
	if result.EnterpriseValue <= 0 {
		return false
	}

	// Terminal value shouldn't dominate too much (typical range: 60-80% of total value)
	terminalPercentage := result.TerminalValue / result.EnterpriseValue
	if terminalPercentage > 0.9 || terminalPercentage < 0.4 {
		return false
	}

	// Check for reasonable cash flows
	for _, projection := range result.Projections {
		//Keep in eye on this check to see if it meet real life.
		// Growth should be reasonable year-over-year
		if projection.Year > 1 && projection.GrowthRateApplied > 1.0 {
			return false
		}
	}
	return true
}

func generateWarnings(inputs Inputs, result *Result) []string {
	warnings := []string{}

	// High growth rate warning
	if inputs.GrowthRate > 0.3 {
		warnings = append(warnings, "High growth rate (>30%) may be unsustainable")
	}

	// Terminal value dominance warning
	terminalPercentage := result.TerminalValue / result.EnterpriseValue
	if terminalPercentage > 0.8 {
		warnings = append(warnings, "Terminal value represents >80% of total value - consider longer explicit forecast period")
	}

	// High WACC warning
	if inputs.WACC > 0.2 {
		warnings = append(warnings, "WACC >20% is unusually high - verify calculation")
	}

	// Terminal growth vs WACC warning
	if inputs.TerminalGrowthRate > inputs.WACC*0.5 {
		warnings = append(warnings, "Terminal growth rate is high relative to WACC")
	}

	return warnings
}

// CalculateImpliedGrowthRate calculates what growth rate would justify current valuation
func CalculateImpliedGrowthRate(targetValue float64, inputs Inputs) (float64, error) {
	// Binary search for growth rate that produces target value
	// TODO: This is a very simple model and may not be accurate for all companies.
	// TODO: Move these variables to a config file.
	lowGrowth := -0.3
	highGrowth := 0.5
	tolerance := 0.0001
	maxIterations := 100

	for i := 0; i < maxIterations; i++ {
		midGrowth := (lowGrowth + highGrowth) / 2

		testInputs := inputs
		testInputs.GrowthRate = midGrowth

		result, err := CalculateDCF(testInputs)
		if err != nil {
			return 0, err
		}

		diff := result.EnterpriseValue - targetValue
		if math.Abs(diff) < tolerance {
			return midGrowth, nil
		}

		if diff > 0 {
			highGrowth = midGrowth
		} else {
			lowGrowth = midGrowth
		}
	}

	return (lowGrowth + highGrowth) / 2, nil
}
