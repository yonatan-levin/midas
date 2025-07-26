package wacc

import (
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
)

// TestWACCPropertyMonotonicity tests that WACC behaves monotonically
// as expected according to financial theory using property-based testing
func TestWACCPropertyMonotonicity(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.Rng.Seed(1234) // For reproducible results
	parameters.MinSuccessfulTests = 100

	properties := gopter.NewProperties(parameters)

	// Property 1: WACC increases with beta (risk increases cost of equity)
	properties.Property("WACC increases with beta", prop.ForAll(
		func(riskFreeRate, marketRiskPremium, beta1, beta2, mvEquity, mvDebt, costDebt, taxRate float64) bool {
			// Ensure beta2 > beta1 and both are reasonable
			if beta2 <= beta1 || beta1 < 0 || beta2 > 3.0 {
				return true // Skip invalid cases
			}

			// Calculate WACC with lower beta
			inputs1 := Inputs{
				RiskFreeRate:        riskFreeRate,
				MarketRiskPremium:   marketRiskPremium,
				Beta:                beta1,
				MarketValueOfEquity: mvEquity,
				MarketValueOfDebt:   mvDebt,
				InterestExpense:     costDebt * mvDebt, // Interest = cost * debt
				TaxRate:             taxRate,
			}

			// Calculate WACC with higher beta
			inputs2 := Inputs{
				RiskFreeRate:        riskFreeRate,
				MarketRiskPremium:   marketRiskPremium,
				Beta:                beta2,
				MarketValueOfEquity: mvEquity,
				MarketValueOfDebt:   mvDebt,
				InterestExpense:     costDebt * mvDebt, // Interest = cost * debt
				TaxRate:             taxRate,
			}

			result1, err1 := Calculate(inputs1)
			result2, err2 := Calculate(inputs2)

			if err1 != nil || err2 != nil {
				return true // Skip if calculation fails
			}

			// WACC should increase with beta (higher risk)
			return result2.WACC >= result1.WACC
		},
		gen.Float64Range(0.01, 0.08), // Risk-free rate: 1%-8%
		gen.Float64Range(0.03, 0.10), // Market risk premium: 3%-10%
		gen.Float64Range(0.1, 1.5),   // Beta 1: 0.1-1.5
		gen.Float64Range(1.5, 3.0),   // Beta 2: 1.5-3.0 (higher than beta1)
		gen.Float64Range(1e9, 1e12),  // Market value equity: $1B-$1T
		gen.Float64Range(1e8, 1e11),  // Market value debt: $100M-$100B
		gen.Float64Range(0.02, 0.08), // Cost of debt: 2%-8%
		gen.Float64Range(0.15, 0.35), // Tax rate: 15%-35%
	))

	// Property 2: WACC decreases with tax rate (tax shield effect)
	properties.Property("WACC decreases with tax rate", prop.ForAll(
		func(riskFreeRate, marketRiskPremium, beta, mvEquity, mvDebt, costDebt, taxRate1, taxRate2 float64) bool {
			// Ensure taxRate2 > taxRate1 and both are reasonable
			if taxRate2 <= taxRate1 || taxRate1 < 0 || taxRate2 > 0.5 {
				return true // Skip invalid cases
			}

			// Only test when there's debt to create tax shield effect
			if mvDebt <= 0 {
				return true
			}

			inputs1 := Inputs{
				RiskFreeRate:        riskFreeRate,
				MarketRiskPremium:   marketRiskPremium,
				Beta:                beta,
				MarketValueOfEquity: mvEquity,
				MarketValueOfDebt:   mvDebt,
				InterestExpense:     costDebt * mvDebt,
				TaxRate:             taxRate1,
			}

			inputs2 := Inputs{
				RiskFreeRate:        riskFreeRate,
				MarketRiskPremium:   marketRiskPremium,
				Beta:                beta,
				MarketValueOfEquity: mvEquity,
				MarketValueOfDebt:   mvDebt,
				InterestExpense:     costDebt * mvDebt,
				TaxRate:             taxRate2,
			}

			result1, err1 := Calculate(inputs1)
			result2, err2 := Calculate(inputs2)

			if err1 != nil || err2 != nil {
				return true
			}

			// WACC should decrease with higher tax rate due to tax shield
			return result2.WACC <= result1.WACC
		},
		gen.Float64Range(0.01, 0.08), // Risk-free rate: 1%-8%
		gen.Float64Range(0.03, 0.10), // Market risk premium: 3%-10%
		gen.Float64Range(0.5, 2.0),   // Beta: 0.5-2.0
		gen.Float64Range(1e9, 1e12),  // Market value equity: $1B-$1T
		gen.Float64Range(1e8, 1e11),  // Market value debt: $100M-$100B
		gen.Float64Range(0.02, 0.08), // Cost of debt: 2%-8%
		gen.Float64Range(0.10, 0.25), // Tax rate 1: 10%-25%
		gen.Float64Range(0.25, 0.40), // Tax rate 2: 25%-40% (higher than taxRate1)
	))

	// Property 3: WACC is positive and reasonable
	properties.Property("WACC is positive and reasonable", prop.ForAll(
		func(riskFreeRate, marketRiskPremium, beta, mvEquity, mvDebt, costDebt, taxRate float64) bool {
					inputs := Inputs{
			RiskFreeRate:        riskFreeRate,
			MarketRiskPremium:   marketRiskPremium,
			Beta:                beta,
			MarketValueOfEquity: mvEquity,
			MarketValueOfDebt:   mvDebt,
			InterestExpense:     costDebt * mvDebt,
			TaxRate:             taxRate,
		}

			result, err := Calculate(inputs)
			if err != nil {
				return true // Skip invalid inputs
			}

			// WACC should be positive and typically less than 30%
			return result.WACC > 0 && result.WACC < 0.30
		},
		gen.Float64Range(0.01, 0.08), // Risk-free rate: 1%-8%
		gen.Float64Range(0.03, 0.10), // Market risk premium: 3%-10%
		gen.Float64Range(0.1, 3.0),   // Beta: 0.1-3.0
		gen.Float64Range(1e9, 1e12),  // Market value equity: $1B-$1T
		gen.Float64Range(0, 1e11),    // Market value debt: $0-$100B
		gen.Float64Range(0.01, 0.10), // Cost of debt: 1%-10%
		gen.Float64Range(0.10, 0.40), // Tax rate: 10%-40%
	))

	// Property 4: Cost of equity increases with beta
	properties.Property("cost of equity increases with beta", prop.ForAll(
		func(riskFreeRate, marketRiskPremium, beta1, beta2 float64) bool {
			if beta2 <= beta1 || beta1 < 0 {
				return true
			}

			costOfEquity1 := riskFreeRate + beta1*marketRiskPremium
			costOfEquity2 := riskFreeRate + beta2*marketRiskPremium

			return costOfEquity2 > costOfEquity1
		},
		gen.Float64Range(0.01, 0.08), // Risk-free rate: 1%-8%
		gen.Float64Range(0.03, 0.10), // Market risk premium: 3%-10%
		gen.Float64Range(0.1, 1.5),   // Beta 1: 0.1-1.5
		gen.Float64Range(1.5, 3.0),   // Beta 2: 1.5-3.0
	))

	properties.TestingRun(t)
}

// TestWACCPropertyEdgeCases tests edge cases and boundary conditions
func TestWACCPropertyEdgeCases(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = 50

	properties := gopter.NewProperties(parameters)

	// Property: Zero debt case - WACC equals cost of equity
	properties.Property("zero debt WACC equals cost of equity", prop.ForAll(
		func(riskFreeRate, marketRiskPremium, beta, mvEquity float64) bool {
					inputs := Inputs{
			RiskFreeRate:        riskFreeRate,
			MarketRiskPremium:   marketRiskPremium,
			Beta:                beta,
			MarketValueOfEquity: mvEquity,
			MarketValueOfDebt:   0, // Zero debt
			InterestExpense:     0,
			TaxRate:             0.25,
		}

			result, err := Calculate(inputs)
			if err != nil {
				return true
			}

			expectedCostOfEquity := riskFreeRate + beta*marketRiskPremium
			// Allow small floating point tolerance
			return assert.InDelta(t, expectedCostOfEquity, result.WACC, 0.001)
		},
		gen.Float64Range(0.02, 0.06), // Risk-free rate
		gen.Float64Range(0.04, 0.08), // Market risk premium
		gen.Float64Range(0.5, 2.0),   // Beta
		gen.Float64Range(1e9, 1e11),  // Market value equity
	))

	properties.TestingRun(t)
}

// TestWACCPropertyInvariance tests mathematical invariants
func TestWACCPropertyInvariance(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = 50

	properties := gopter.NewProperties(parameters)

	// Property: Scaling all values proportionally should not change WACC
	properties.Property("proportional scaling invariance", prop.ForAll(
		func(riskFreeRate, marketRiskPremium, beta, mvEquity, mvDebt, costDebt, taxRate, scale float64) bool {
			if scale <= 0 || scale > 10 {
				return true // Skip invalid scaling factors
			}

					// Original inputs
		inputs1 := Inputs{
			RiskFreeRate:        riskFreeRate,
			MarketRiskPremium:   marketRiskPremium,
			Beta:                beta,
			MarketValueOfEquity: mvEquity,
			MarketValueOfDebt:   mvDebt,
			InterestExpense:     costDebt * mvDebt,
			TaxRate:             taxRate,
		}

		// Scaled inputs (only scale market values, not rates)
		inputs2 := Inputs{
			RiskFreeRate:        riskFreeRate,
			MarketRiskPremium:   marketRiskPremium,
			Beta:                beta,
			MarketValueOfEquity: mvEquity * scale,
			MarketValueOfDebt:   mvDebt * scale,
			InterestExpense:     costDebt * mvDebt * scale,
			TaxRate:             taxRate,
		}

			result1, err1 := Calculate(inputs1)
			result2, err2 := Calculate(inputs2)

			if err1 != nil || err2 != nil {
				return true
			}

			// WACC should be the same regardless of absolute values
			return assert.InDelta(t, result1.WACC, result2.WACC, 0.001)
		},
		gen.Float64Range(0.02, 0.06), // Risk-free rate
		gen.Float64Range(0.04, 0.08), // Market risk premium
		gen.Float64Range(0.8, 1.5),   // Beta
		gen.Float64Range(1e9, 1e11),  // Market value equity
		gen.Float64Range(1e8, 1e10),  // Market value debt
		gen.Float64Range(0.03, 0.07), // Cost of debt
		gen.Float64Range(0.20, 0.30), // Tax rate
		gen.Float64Range(0.1, 5.0),   // Scale factor
	))

	properties.TestingRun(t)
}
