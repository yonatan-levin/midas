package wacc

import (
	"errors"
	"math"
)

// Inputs represents all inputs needed for WACC calculation
type Inputs struct {
	// Market data
	MarketValueOfEquity float64 // Current market cap (price * shares)
	MarketValueOfDebt   float64 // Book value of interest-bearing debt
	Beta                float64 // Stock's beta vs market
	RiskFreeRate        float64 // 10-year Treasury yield
	MarketRiskPremium   float64 // Expected market return - risk-free rate

	// International risk adjustment (Damodaran-style country risk premium).
	// Zero-value means no adjustment (domestic US company).
	CountryRiskPremium float64

	// Financial data
	InterestExpense float64 // Annual interest payments
	TaxRate         float64 // Effective tax rate
}

// Result contains the components and final WACC calculation
type Result struct {
	CostOfEquity       float64 `json:"cost_of_equity"`
	CostOfDebtPretax   float64 `json:"cost_of_debt_pretax"`
	CostOfDebtAfterTax float64 `json:"cost_of_debt_after_tax"`
	WeightOfEquity     float64 `json:"weight_of_equity"`
	WeightOfDebt       float64 `json:"weight_of_debt"`
	WACC               float64 `json:"wacc"`

	// Intermediate calculations for transparency
	TotalValue float64 `json:"total_value"` // E + D
	TaxShield  float64 `json:"tax_shield"`  // Tax benefit from debt
}

// Calculate computes WACC using the standard formula:
// WACC = (E/V * Re) + (D/V * Rd * (1-T))
// Where:
//
//	E = Market value of equity
//	D = Market value of debt
//	V = E + D (total value)
//	Re = Cost of equity (CAPM)
//	Rd = Cost of debt (interest expense / debt)
//	T = Tax rate
func Calculate(inputs Inputs) (*Result, error) {
	if err := validateInputs(inputs); err != nil {
		return nil, err
	}

	result := &Result{}

	// Calculate cost of equity using CAPM with country risk premium:
	// Re = Rf + β(Rm - Rf) + CRP
	// CRP is zero for domestic US companies, so the formula is backward-compatible.
	result.CostOfEquity = inputs.RiskFreeRate + inputs.Beta*inputs.MarketRiskPremium + inputs.CountryRiskPremium

	// Calculate cost of debt
	if inputs.MarketValueOfDebt > 0 {
		result.CostOfDebtPretax = inputs.InterestExpense / inputs.MarketValueOfDebt
		result.CostOfDebtAfterTax = result.CostOfDebtPretax * (1 - inputs.TaxRate)
		result.TaxShield = result.CostOfDebtPretax * inputs.TaxRate
	} else {
		// No debt case
		result.CostOfDebtPretax = 0
		result.CostOfDebtAfterTax = 0
		result.TaxShield = 0
	}

	// Calculate weights
	result.TotalValue = inputs.MarketValueOfEquity + inputs.MarketValueOfDebt
	if result.TotalValue > 0 {
		result.WeightOfEquity = inputs.MarketValueOfEquity / result.TotalValue
		result.WeightOfDebt = inputs.MarketValueOfDebt / result.TotalValue
	} else {
		return nil, errors.New("total value cannot be zero or negative")
	}

	// Calculate WACC
	result.WACC = (result.WeightOfEquity * result.CostOfEquity) +
		(result.WeightOfDebt * result.CostOfDebtAfterTax)

	return result, nil
}

// CalculateWithOverrides allows overriding specific parameters
func CalculateWithOverrides(inputs Inputs, overrides map[string]float64) (*Result, error) {
	// Apply overrides
	if beta, ok := overrides["beta"]; ok {
		inputs.Beta = beta
	}
	if rf, ok := overrides["risk_free_rate"]; ok {
		inputs.RiskFreeRate = rf
	}
	if mrp, ok := overrides["market_risk_premium"]; ok {
		inputs.MarketRiskPremium = mrp
	}
	if taxRate, ok := overrides["tax_rate"]; ok {
		inputs.TaxRate = taxRate
	}

	return Calculate(inputs)
}

// validateInputs checks that all inputs are within reasonable ranges
func validateInputs(inputs Inputs) error {
	if inputs.MarketValueOfEquity <= 0 {
		return errors.New("market value of equity must be positive")
	}

	if inputs.MarketValueOfDebt < 0 {
		return errors.New("market value of debt cannot be negative")
	}

	if inputs.Beta < 0 {
		return errors.New("beta cannot be negative")
	}

	if inputs.RiskFreeRate < 0 || inputs.RiskFreeRate > 0.2 {
		return errors.New("risk-free rate must be between 0% and 20%")
	}

	if inputs.MarketRiskPremium < 0 || inputs.MarketRiskPremium > 0.15 {
		return errors.New("market risk premium must be between 0% and 15%")
	}

	if inputs.TaxRate < 0 || inputs.TaxRate > 1 {
		return errors.New("tax rate must be between 0% and 100%")
	}

	if inputs.MarketValueOfDebt > 0 && inputs.InterestExpense < 0 {
		return errors.New("interest expense cannot be negative when debt is positive")
	}

	if inputs.CountryRiskPremium < 0 || inputs.CountryRiskPremium > 0.20 {
		return errors.New("country risk premium must be between 0% and 20%")
	}

	return nil
}

// CalculateCostOfEquity computes cost of equity using CAPM
func CalculateCostOfEquity(riskFreeRate, beta, marketRiskPremium float64) float64 {
	return riskFreeRate + beta*marketRiskPremium
}

// CalculateAfterTaxCostOfDebt computes after-tax cost of debt
func CalculateAfterTaxCostOfDebt(interestExpense, debt, taxRate float64) float64 {
	if debt <= 0 {
		return 0
	}
	pretaxCost := interestExpense / debt
	return pretaxCost * (1 - taxRate)
}

// CalculateEquityWeight computes weight of equity in capital structure
func CalculateEquityWeight(marketValueOfEquity, marketValueOfDebt float64) float64 {
	totalValue := marketValueOfEquity + marketValueOfDebt
	if totalValue <= 0 {
		return 0
	}
	return marketValueOfEquity / totalValue
}

// SensitivityAnalysis performs sensitivity analysis on WACC
// Returns WACC for different beta values
func SensitivityAnalysis(inputs Inputs, betaRange []float64) ([]float64, error) {
	results := make([]float64, len(betaRange))

	for i, beta := range betaRange {
		testInputs := inputs
		testInputs.Beta = beta

		result, err := Calculate(testInputs)
		if err != nil {
			return nil, err
		}
		results[i] = result.WACC
	}

	return results, nil
}

// IsReasonable checks if WACC result is within reasonable bounds
func (r *Result) IsReasonable() bool {
	// WACC should typically be between 3% and 25%
	return r.WACC >= 0.03 && r.WACC <= 0.25 &&
		r.CostOfEquity >= 0.03 && r.CostOfEquity <= 0.30 &&
		r.WeightOfEquity >= 0 && r.WeightOfEquity <= 1 &&
		r.WeightOfDebt >= 0 && r.WeightOfDebt <= 1 &&
		math.Abs(r.WeightOfEquity+r.WeightOfDebt-1) < 0.0001 // Should sum to 1
}
