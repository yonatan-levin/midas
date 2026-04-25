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
	TerminalYearFCF      float64 `json:"terminal_year_fcf"`      // FCF in final explicit year
	TerminalValueNominal float64 `json:"terminal_value_nominal"` // Terminal value before discounting

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
	explicitPeriodValue := 0.0

	for year := 1; year <= inputs.ProjectionYears; year++ {
		// Select growth rate: per-year if available, otherwise single rate
		rateForYear := inputs.GrowthRate
		if len(inputs.GrowthRates) >= year {
			rateForYear = inputs.GrowthRates[year-1]
		}

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

	// Terminal value = FCF(final year) * (1 + terminal growth) / (WACC - terminal growth)
	// The minimum 1% spread is enforced by validateInputs.
	terminalFCF := result.TerminalYearFCF * (1 + inputs.TerminalGrowthRate)
	gordonTV := terminalFCF / (inputs.WACC - inputs.TerminalGrowthRate)
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

// Helper functions

func validateInputs(inputs Inputs) error {
	if inputs.BaseOperatingIncome <= 0 {
		return errors.New("base operating income must be positive")
	}

	if inputs.GrowthRate < -0.5 || inputs.GrowthRate > 1.0 {
		return errors.New("growth rate must be between -50% and 100%")
	}

	if inputs.TerminalGrowthRate < 0 || inputs.TerminalGrowthRate > 0.05 {
		return errors.New("terminal growth rate must be between 0% and 5%")
	}

	if inputs.WACC <= 0 || inputs.WACC > 0.5 {
		return errors.New("WACC must be between 0% and 50%")
	}

	if inputs.WACC-inputs.TerminalGrowthRate < 0.01 {
		return fmt.Errorf("WACC (%.2f%%) must exceed terminal growth rate (%.2f%%) by at least 1%%",
			inputs.WACC*100, inputs.TerminalGrowthRate*100)
	}

	if inputs.TaxRate < 0 || inputs.TaxRate > 1 {
		return errors.New("tax rate must be between 0% and 100%")
	}

	if inputs.ProjectionYears < 1 || inputs.ProjectionYears > 15 {
		return errors.New("projection years must be between 1 and 15")
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
