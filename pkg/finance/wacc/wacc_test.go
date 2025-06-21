package wacc

import (
	"math"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculate_ValidInputs(t *testing.T) {
	tests := []struct {
		name     string
		inputs   Inputs
		expected struct {
			wacc         float64
			costOfEquity float64
			tolerance    float64
		}
	}{
		{
			name: "Apple-like company",
			inputs: Inputs{
				MarketValueOfEquity: 3000000, // $3T market cap
				MarketValueOfDebt:   100000,  // $100B debt
				Beta:                1.2,
				RiskFreeRate:        0.03, // 3%
				MarketRiskPremium:   0.05, // 5%
				InterestExpense:     2000, // $2B interest
				TaxRate:             0.21, // 21% tax rate
			},
			expected: struct {
				wacc         float64
				costOfEquity float64
				tolerance    float64
			}{
				wacc:         0.0876, // Corrected calculation: E/(E+D)*Re + D/(E+D)*Rd*(1-T)
				costOfEquity: 0.09,   // 3% + 1.2 * 5% = 9%
				tolerance:    0.001,
			},
		},
		{
			name: "High-growth tech company",
			inputs: Inputs{
				MarketValueOfEquity: 500000,
				MarketValueOfDebt:   50000,
				Beta:                1.8,
				RiskFreeRate:        0.035,
				MarketRiskPremium:   0.06,
				InterestExpense:     2000,
				TaxRate:             0.25,
			},
			expected: struct {
				wacc         float64
				costOfEquity float64
				tolerance    float64
			}{
				wacc:         0.1327, // Corrected calculation
				costOfEquity: 0.143,  // 3.5% + 1.8 * 6% = 14.3%
				tolerance:    0.001,
			},
		},
		{
			name: "Debt-free company",
			inputs: Inputs{
				MarketValueOfEquity: 1000000,
				MarketValueOfDebt:   0,
				Beta:                1.0,
				RiskFreeRate:        0.025,
				MarketRiskPremium:   0.05,
				InterestExpense:     0,
				TaxRate:             0.21,
			},
			expected: struct {
				wacc         float64
				costOfEquity float64
				tolerance    float64
			}{
				wacc:         0.075, // Should equal cost of equity
				costOfEquity: 0.075, // 2.5% + 1.0 * 5% = 7.5%
				tolerance:    0.0001,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Calculate(tt.inputs)
			require.NoError(t, err)

			assert.InDelta(t, tt.expected.wacc, result.WACC, tt.expected.tolerance, "WACC mismatch")
			assert.InDelta(t, tt.expected.costOfEquity, result.CostOfEquity, tt.expected.tolerance, "Cost of equity mismatch")

			// Test weights sum to 1
			assert.InDelta(t, 1.0, result.WeightOfEquity+result.WeightOfDebt, 0.0001, "Weights should sum to 1")

			// Test reasonableness
			assert.True(t, result.IsReasonable(), "Result should be reasonable")
		})
	}
}

func TestCalculate_InvalidInputs(t *testing.T) {
	tests := []struct {
		name    string
		inputs  Inputs
		wantErr string
	}{
		{
			name: "Negative equity",
			inputs: Inputs{
				MarketValueOfEquity: -1000,
				MarketValueOfDebt:   100,
				Beta:                1.0,
				RiskFreeRate:        0.03,
				MarketRiskPremium:   0.05,
				TaxRate:             0.21,
			},
			wantErr: "market value of equity must be positive",
		},
		{
			name: "Negative beta",
			inputs: Inputs{
				MarketValueOfEquity: 1000,
				MarketValueOfDebt:   100,
				Beta:                -0.5,
				RiskFreeRate:        0.03,
				MarketRiskPremium:   0.05,
				TaxRate:             0.21,
			},
			wantErr: "beta cannot be negative",
		},
		{
			name: "Extreme risk-free rate",
			inputs: Inputs{
				MarketValueOfEquity: 1000,
				MarketValueOfDebt:   100,
				Beta:                1.0,
				RiskFreeRate:        0.25, // 25% risk-free rate is unrealistic
				MarketRiskPremium:   0.05,
				TaxRate:             0.21,
			},
			wantErr: "risk-free rate must be between 0% and 20%",
		},
		{
			name: "Invalid tax rate",
			inputs: Inputs{
				MarketValueOfEquity: 1000,
				MarketValueOfDebt:   100,
				Beta:                1.0,
				RiskFreeRate:        0.03,
				MarketRiskPremium:   0.05,
				TaxRate:             1.5, // 150% tax rate
			},
			wantErr: "tax rate must be between 0% and 100%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Calculate(tt.inputs)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, result)
		})
	}
}

func TestCalculateWithOverrides(t *testing.T) {
	baseInputs := Inputs{
		MarketValueOfEquity: 1000000,
		MarketValueOfDebt:   200000,
		Beta:                1.0,
		RiskFreeRate:        0.03,
		MarketRiskPremium:   0.05,
		InterestExpense:     10000,
		TaxRate:             0.21,
	}

	overrides := map[string]float64{
		"beta":                1.5,
		"risk_free_rate":      0.04,
		"market_risk_premium": 0.06,
	}

	result, err := CalculateWithOverrides(baseInputs, overrides)
	require.NoError(t, err)

	// Verify overrides were applied
	expectedCostOfEquity := 0.04 + 1.5*0.06 // 4% + 1.5 * 6% = 13%
	assert.InDelta(t, expectedCostOfEquity, result.CostOfEquity, 0.0001)
}

func TestSensitivityAnalysis(t *testing.T) {
	baseInputs := Inputs{
		MarketValueOfEquity: 1000000,
		MarketValueOfDebt:   200000,
		Beta:                1.0,
		RiskFreeRate:        0.03,
		MarketRiskPremium:   0.05,
		InterestExpense:     10000,
		TaxRate:             0.21,
	}

	betaRange := []float64{0.5, 1.0, 1.5, 2.0}
	results, err := SensitivityAnalysis(baseInputs, betaRange)
	require.NoError(t, err)
	require.Len(t, results, len(betaRange))

	// WACC should increase with beta (monotonicity test)
	for i := 1; i < len(results); i++ {
		assert.Greater(t, results[i], results[i-1], "WACC should increase with beta")
	}
}

func TestIndividualCalculationFunctions(t *testing.T) {
	t.Run("CalculateCostOfEquity", func(t *testing.T) {
		costOfEquity := CalculateCostOfEquity(0.03, 1.2, 0.05)
		expected := 0.03 + 1.2*0.05 // 3% + 1.2 * 5% = 9%
		assert.InDelta(t, expected, costOfEquity, 0.0001)
	})

	t.Run("CalculateAfterTaxCostOfDebt", func(t *testing.T) {
		afterTaxCost := CalculateAfterTaxCostOfDebt(5000, 100000, 0.21)
		expected := (5000.0 / 100000.0) * (1 - 0.21) // 5% * (1 - 21%) = 3.95%
		assert.InDelta(t, expected, afterTaxCost, 0.0001)
	})

	t.Run("CalculateEquityWeight", func(t *testing.T) {
		weight := CalculateEquityWeight(800000, 200000)
		expected := 800000.0 / (800000.0 + 200000.0) // 80%
		assert.InDelta(t, expected, weight, 0.0001)
	})
}

// Property-based tests using gopter
func TestWACCProperties(t *testing.T) {
	properties := gopter.NewProperties(nil)

	// Property 1: WACC monotonicity with respect to beta
	properties.Property("WACC increases with beta", prop.ForAll(
		func(equity, debt, beta1, beta2, rf, mrp, interest, tax float64) bool {
			if beta2 <= beta1 {
				return true // Skip if beta2 is not greater than beta1
			}

			inputs1 := Inputs{
				MarketValueOfEquity: equity,
				MarketValueOfDebt:   debt,
				Beta:                beta1,
				RiskFreeRate:        rf,
				MarketRiskPremium:   mrp,
				InterestExpense:     interest,
				TaxRate:             tax,
			}

			inputs2 := inputs1
			inputs2.Beta = beta2

			result1, err1 := Calculate(inputs1)
			result2, err2 := Calculate(inputs2)

			if err1 != nil || err2 != nil {
				return true // Skip invalid inputs
			}

			return result2.WACC > result1.WACC
		},
		gen.Float64Range(100000, 10000000), // equity
		gen.Float64Range(0, 1000000),       // debt
		gen.Float64Range(0.1, 2.0),         // beta1
		gen.Float64Range(0.1, 3.0),         // beta2
		gen.Float64Range(0.01, 0.08),       // risk-free rate
		gen.Float64Range(0.03, 0.12),       // market risk premium
		gen.Float64Range(0, 50000),         // interest expense
		gen.Float64Range(0.1, 0.4),         // tax rate
	))

	// Property 2: Weights always sum to 1
	properties.Property("Weights sum to 1", prop.ForAll(
		func(equity, debt, beta, rf, mrp, interest, tax float64) bool {
			inputs := Inputs{
				MarketValueOfEquity: equity,
				MarketValueOfDebt:   debt,
				Beta:                beta,
				RiskFreeRate:        rf,
				MarketRiskPremium:   mrp,
				InterestExpense:     interest,
				TaxRate:             tax,
			}

			result, err := Calculate(inputs)
			if err != nil {
				return true // Skip invalid inputs
			}

			weightSum := result.WeightOfEquity + result.WeightOfDebt
			return math.Abs(weightSum-1.0) < 0.0001
		},
		gen.Float64Range(100000, 10000000), // equity
		gen.Float64Range(0, 1000000),       // debt
		gen.Float64Range(0.1, 3.0),         // beta
		gen.Float64Range(0.01, 0.08),       // risk-free rate
		gen.Float64Range(0.03, 0.12),       // market risk premium
		gen.Float64Range(0, 50000),         // interest expense
		gen.Float64Range(0.1, 0.4),         // tax rate
	))

	// Property 3: WACC bounds (should be reasonable)
	properties.Property("WACC within reasonable bounds", prop.ForAll(
		func(equity, debt, beta, rf, mrp, interest, tax float64) bool {
			inputs := Inputs{
				MarketValueOfEquity: equity,
				MarketValueOfDebt:   debt,
				Beta:                beta,
				RiskFreeRate:        rf,
				MarketRiskPremium:   mrp,
				InterestExpense:     interest,
				TaxRate:             tax,
			}

			result, err := Calculate(inputs)
			if err != nil {
				return true // Skip invalid inputs
			}

			return result.WACC > 0 && result.WACC < 0.5 // Between 0% and 50%
		},
		gen.Float64Range(100000, 10000000), // equity
		gen.Float64Range(0, 1000000),       // debt
		gen.Float64Range(0.1, 3.0),         // beta
		gen.Float64Range(0.01, 0.08),       // risk-free rate
		gen.Float64Range(0.03, 0.12),       // market risk premium
		gen.Float64Range(0, 50000),         // interest expense
		gen.Float64Range(0.1, 0.4),         // tax rate
	))

	properties.TestingRun(t)
}

func TestIsReasonable(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   bool
	}{
		{
			name: "Reasonable WACC result",
			result: Result{
				WACC:           0.08,
				CostOfEquity:   0.09,
				WeightOfEquity: 0.8,
				WeightOfDebt:   0.2,
			},
			want: true,
		},
		{
			name: "Unreasonable high WACC",
			result: Result{
				WACC:           0.30, // 30% is too high
				CostOfEquity:   0.35,
				WeightOfEquity: 0.8,
				WeightOfDebt:   0.2,
			},
			want: false,
		},
		{
			name: "Weights don't sum to 1",
			result: Result{
				WACC:           0.08,
				CostOfEquity:   0.09,
				WeightOfEquity: 0.7,
				WeightOfDebt:   0.2, // 0.7 + 0.2 = 0.9, not 1.0
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.result.IsReasonable())
		})
	}
}

// Benchmark tests
func BenchmarkCalculate(b *testing.B) {
	inputs := Inputs{
		MarketValueOfEquity: 1000000,
		MarketValueOfDebt:   200000,
		Beta:                1.2,
		RiskFreeRate:        0.03,
		MarketRiskPremium:   0.05,
		InterestExpense:     10000,
		TaxRate:             0.21,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Calculate(inputs)
	}
}

func BenchmarkSensitivityAnalysis(b *testing.B) {
	inputs := Inputs{
		MarketValueOfEquity: 1000000,
		MarketValueOfDebt:   200000,
		Beta:                1.0,
		RiskFreeRate:        0.03,
		MarketRiskPremium:   0.05,
		InterestExpense:     10000,
		TaxRate:             0.21,
	}

	betaRange := []float64{0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = SensitivityAnalysis(inputs, betaRange)
	}
}
