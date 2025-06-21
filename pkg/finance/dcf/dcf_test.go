package dcf

import (
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateDCF_ValidInputs(t *testing.T) {
	tests := []struct {
		name   string
		inputs Inputs
		checks func(t *testing.T, result *Result)
	}{
		{
			name: "Apple-like high-growth company",
			inputs: Inputs{
				BaseOperatingIncome: 100.0, // $100B operating income
				GrowthRate:          0.15,  // 15% growth
				TerminalGrowthRate:  0.025, // 2.5% terminal
				WACC:                0.10,  // 10% WACC
				TaxRate:             0.25,  // 25% tax rate
				ProjectionYears:     5,
			},
			checks: func(t *testing.T, result *Result) {
				assert.Greater(t, result.EnterpriseValue, 1000.0) // Should be > $1T
				assert.Len(t, result.Projections, 5)
				assert.Greater(t, result.TerminalValue, 500.0) // Terminal should be significant
				assert.True(t, result.IsReasonable)
			},
		},
		{
			name: "Mature utility company",
			inputs: Inputs{
				BaseOperatingIncome: 10.0, // $10B operating income
				GrowthRate:          0.03, // 3% growth
				TerminalGrowthRate:  0.02, // 2% terminal
				WACC:                0.08, // 8% WACC
				TaxRate:             0.25, // 25% tax rate
				ProjectionYears:     5,
			},
			checks: func(t *testing.T, result *Result) {
				assert.Greater(t, result.EnterpriseValue, 100.0) // Should be reasonable
				assert.Less(t, result.EnterpriseValue, 300.0)    // But not excessive
				assert.True(t, result.IsReasonable)
				// Terminal value should be majority for mature company
				assert.Greater(t, result.TerminalValue/result.EnterpriseValue, 0.6)
			},
		},
		{
			name: "Declining business",
			inputs: Inputs{
				BaseOperatingIncome: 50.0,  // $50B initial
				GrowthRate:          -0.05, // -5% decline
				TerminalGrowthRate:  0.01,  // 1% terminal (conservative recovery)
				WACC:                0.12,  // 12% WACC (riskier)
				TaxRate:             0.25,  // 25% tax rate
				ProjectionYears:     5,
			},
			checks: func(t *testing.T, result *Result) {
				assert.Greater(t, result.EnterpriseValue, 0.0) // Still positive
				// Each year should decline
				for i := 1; i < len(result.Projections); i++ {
					assert.Less(t, result.Projections[i].OperatingIncome, result.Projections[i-1].OperatingIncome)
				}
			},
		},
		{
			name: "Conservative assumptions",
			inputs: Inputs{
				BaseOperatingIncome: 20.0,
				GrowthRate:          0.05,
				TerminalGrowthRate:  0.02,
				WACC:                0.09,
				TaxRate:             0.25,
				ProjectionYears:     5,
			},
			checks: func(t *testing.T, result *Result) {
				assert.Greater(t, result.EnterpriseValue, 150.0)
				assert.Less(t, result.EnterpriseValue, 400.0)
				assert.True(t, result.IsReasonable)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CalculateDCF(tt.inputs)
			require.NoError(t, err)
			require.NotNil(t, result)
			tt.checks(t, result)
		})
	}
}

func TestCalculateDCF_InvalidInputs(t *testing.T) {
	tests := []struct {
		name    string
		inputs  Inputs
		wantErr string
	}{
		{
			name: "Negative operating income",
			inputs: Inputs{
				BaseOperatingIncome: -100.0,
				GrowthRate:          0.10,
				TerminalGrowthRate:  0.03,
				WACC:                0.10,
				TaxRate:             0.25,
				ProjectionYears:     5,
			},
			wantErr: "base operating income must be positive",
		},
		{
			name: "Invalid projection years",
			inputs: Inputs{
				BaseOperatingIncome: 100.0,
				GrowthRate:          0.10,
				TerminalGrowthRate:  0.03,
				WACC:                0.10,
				TaxRate:             0.25,
				ProjectionYears:     0,
			},
			wantErr: "projection years must be between 1 and 10",
		},
		{
			name: "Negative WACC",
			inputs: Inputs{
				BaseOperatingIncome: 100.0,
				GrowthRate:          0.10,
				TerminalGrowthRate:  0.03,
				WACC:                -0.05,
				TaxRate:             0.25,
				ProjectionYears:     5,
			},
			wantErr: "WACC must be between 0% and 50%",
		},
		{
			name: "Terminal growth higher than WACC",
			inputs: Inputs{
				BaseOperatingIncome: 100.0,
				GrowthRate:          0.10,
				TerminalGrowthRate:  0.15, // Higher than WACC
				WACC:                0.10,
				TaxRate:             0.25,
				ProjectionYears:     5,
			},
			wantErr: "terminal growth rate must be between 0% and 5%",
		},
		{
			name: "Extreme growth rate",
			inputs: Inputs{
				BaseOperatingIncome: 100.0,
				GrowthRate:          2.0, // 200% growth
				TerminalGrowthRate:  0.03,
				WACC:                0.10,
				TaxRate:             0.25,
				ProjectionYears:     5,
			},
			wantErr: "growth rate must be between -50% and 100%",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CalculateDCF(tt.inputs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, result)
		})
	}
}

func TestCalculateEquityValue(t *testing.T) {
	tests := []struct {
		name            string
		enterpriseValue float64
		debt            float64
		cash            float64
		expected        float64
	}{
		{
			name:            "Standard case",
			enterpriseValue: 1000.0,
			debt:            200.0,
			cash:            50.0,
			expected:        850.0, // 1000 - 200 + 50
		},
		{
			name:            "Debt-free company",
			enterpriseValue: 500.0,
			debt:            0.0,
			cash:            100.0,
			expected:        600.0, // 500 + 100
		},
		{
			name:            "Cash-poor company",
			enterpriseValue: 800.0,
			debt:            300.0,
			cash:            0.0,
			expected:        500.0, // 800 - 300
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateEquityValue(tt.enterpriseValue, tt.debt, tt.cash)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateValuePerShare(t *testing.T) {
	tests := []struct {
		name              string
		equityValue       float64
		sharesOutstanding float64
		expected          float64
		expectError       bool
	}{
		{
			name:              "Standard calculation",
			equityValue:       1000.0,
			sharesOutstanding: 100.0,
			expected:          10.0, // 1000 / 100
			expectError:       false,
		},
		{
			name:              "High share count",
			equityValue:       500.0,
			sharesOutstanding: 50.0,
			expected:          10.0, // 500 / 50
			expectError:       false,
		},
		{
			name:              "Zero shares outstanding",
			equityValue:       1000.0,
			sharesOutstanding: 0.0,
			expected:          0.0,
			expectError:       true,
		},
		{
			name:              "Negative shares",
			equityValue:       1000.0,
			sharesOutstanding: -100.0,
			expected:          0.0,
			expectError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CalculateValuePerShare(tt.equityValue, tt.sharesOutstanding)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestSensitivityAnalysis(t *testing.T) {
	baseInputs := Inputs{
		BaseOperatingIncome: 100.0,
		GrowthRate:          0.10,
		TerminalGrowthRate:  0.025,
		WACC:                0.10,
		TaxRate:             0.25,
		ProjectionYears:     5,
	}

	waccRange := []float64{0.08, 0.10, 0.12}
	growthRange := []float64{0.05, 0.10, 0.15}

	results, err := SensitivityAnalysis(baseInputs, waccRange, growthRange)
	require.NoError(t, err)
	require.Len(t, results, len(waccRange))

	for _, waccResults := range results {
		require.Len(t, waccResults, len(growthRange))

		// Within same WACC, higher growth should lead to higher values
		for j := 1; j < len(waccResults); j++ {
			assert.Greater(t, waccResults[j], waccResults[j-1])
		}
	}

	// Lower WACC should generally lead to higher values (for same growth)
	for j := 0; j < len(growthRange); j++ {
		assert.Greater(t, results[0][j], results[2][j]) // 8% WACC > 12% WACC
	}
}

func TestProjectionCalculations(t *testing.T) {
	inputs := Inputs{
		BaseOperatingIncome: 100.0,
		GrowthRate:          0.10, // 10% growth
		TerminalGrowthRate:  0.025,
		WACC:                0.08,
		TaxRate:             0.25, // 25% tax rate
		ProjectionYears:     3,
	}

	result, err := CalculateDCF(inputs)
	require.NoError(t, err)
	require.Len(t, result.Projections, 3)

	// Check operating income growth
	assert.InDelta(t, 110.0, result.Projections[0].OperatingIncome, 0.1) // Year 1: 100 * 1.10
	assert.InDelta(t, 121.0, result.Projections[1].OperatingIncome, 0.1) // Year 2: 110 * 1.10
	assert.InDelta(t, 133.1, result.Projections[2].OperatingIncome, 0.1) // Year 3: 121 * 1.10

	// Check NOPAT calculations (after 25% tax)
	assert.InDelta(t, 82.5, result.Projections[0].NOPAT, 0.1)   // 110 * 0.75
	assert.InDelta(t, 90.75, result.Projections[1].NOPAT, 0.1)  // 121 * 0.75
	assert.InDelta(t, 99.825, result.Projections[2].NOPAT, 0.1) // 133.1 * 0.75

	// Check years
	for i, proj := range result.Projections {
		assert.Equal(t, i+1, proj.Year)
	}

	// Check that present values are less than future cash flows
	for _, proj := range result.Projections {
		assert.Less(t, proj.PresentValue, proj.FreeCashFlow)
	}
}

func TestTerminalValueCalculation(t *testing.T) {
	inputs := Inputs{
		BaseOperatingIncome: 100.0,
		GrowthRate:          0.10,
		TerminalGrowthRate:  0.025,
		WACC:                0.10,
		TaxRate:             0.25,
		ProjectionYears:     1, // Single year to make calculation clear
	}

	result, err := CalculateDCF(inputs)
	require.NoError(t, err)

	// Final year FCF should be 110 * 0.75 = 82.5
	expectedFinalFCF := 82.5
	assert.InDelta(t, expectedFinalFCF, result.TerminalYearFCF, 0.1)

	// Terminal FCF = 82.5 * 1.025 = 84.5625
	expectedTerminalFCF := expectedFinalFCF * 1.025
	assert.InDelta(t, expectedTerminalFCF, expectedTerminalFCF, 0.1)

	// Terminal value nominal = 84.5625 / (0.10 - 0.025) = 1128.33
	expectedTerminalValueNominal := expectedTerminalFCF / (0.10 - 0.025)
	assert.InDelta(t, expectedTerminalValueNominal, result.TerminalValueNominal, 1.0)

	// Terminal value PV should be less than nominal
	assert.Less(t, result.TerminalValue, result.TerminalValueNominal)
}

// Property-based tests
func TestDCFProperties(t *testing.T) {
	properties := gopter.NewProperties(nil)

	// Property 1: Higher growth rates should lead to higher valuations (all else equal)
	properties.Property("Higher growth increases valuation", prop.ForAll(
		func(baseGrowth, higherGrowth, operatingIncome, wacc float64) bool {
			if baseGrowth >= higherGrowth || operatingIncome <= 0 || wacc <= 0.01 || wacc >= 0.50 {
				return true // Skip invalid cases
			}
			if higherGrowth >= wacc || baseGrowth >= wacc || higherGrowth > 1.0 || baseGrowth < -0.5 {
				return true // Skip cases where growth >= wacc or growth is extreme
			}

			inputs1 := Inputs{
				BaseOperatingIncome: operatingIncome,
				GrowthRate:          baseGrowth,
				TerminalGrowthRate:  0.025,
				WACC:                wacc,
				TaxRate:             0.25,
				ProjectionYears:     5,
			}

			inputs2 := inputs1
			inputs2.GrowthRate = higherGrowth

			result1, err1 := CalculateDCF(inputs1)
			result2, err2 := CalculateDCF(inputs2)

			if err1 != nil || err2 != nil {
				return true
			}

			return result2.EnterpriseValue > result1.EnterpriseValue
		},
		gen.Float64Range(0.01, 0.15), // baseGrowth
		gen.Float64Range(0.01, 0.20), // higherGrowth
		gen.Float64Range(10, 500),    // operatingIncome
		gen.Float64Range(0.05, 0.25), // wacc
	))

	// Property 2: Higher WACC should lead to lower valuations
	properties.Property("Higher WACC decreases valuation", prop.ForAll(
		func(lowerWACC, higherWACC, operatingIncome, growth float64) bool {
			if lowerWACC >= higherWACC || operatingIncome <= 0 || growth < -0.3 || growth >= 0.50 {
				return true
			}
			if growth >= lowerWACC || lowerWACC <= 0.01 || higherWACC >= 0.50 {
				return true
			}

			inputs1 := Inputs{
				BaseOperatingIncome: operatingIncome,
				GrowthRate:          growth,
				TerminalGrowthRate:  0.025,
				WACC:                lowerWACC,
				TaxRate:             0.25,
				ProjectionYears:     5,
			}

			inputs2 := inputs1
			inputs2.WACC = higherWACC

			result1, err1 := CalculateDCF(inputs1)
			result2, err2 := CalculateDCF(inputs2)

			if err1 != nil || err2 != nil {
				return true
			}

			return result1.EnterpriseValue > result2.EnterpriseValue
		},
		gen.Float64Range(0.05, 0.15), // lowerWACC
		gen.Float64Range(0.05, 0.20), // higherWACC
		gen.Float64Range(10, 500),    // operatingIncome
		gen.Float64Range(0.01, 0.10), // growth
	))

	// Property 3: Enterprise value should always be positive for valid inputs
	properties.Property("Enterprise value is positive", prop.ForAll(
		func(operatingIncome, growth, wacc float64) bool {
			if operatingIncome <= 0 || wacc <= 0.025 || wacc >= 0.50 {
				return true
			}
			if growth >= wacc || growth < -0.5 || growth > 1.0 {
				return true
			}

			inputs := Inputs{
				BaseOperatingIncome: operatingIncome,
				GrowthRate:          growth,
				TerminalGrowthRate:  0.025,
				WACC:                wacc,
				TaxRate:             0.25,
				ProjectionYears:     5,
			}

			result, err := CalculateDCF(inputs)
			if err != nil {
				return true
			}

			return result.EnterpriseValue > 0
		},
		gen.Float64Range(1, 1000),    // operatingIncome
		gen.Float64Range(-0.3, 0.3),  // growth
		gen.Float64Range(0.03, 0.30), // wacc
	))

	properties.TestingRun(t)
}

// Benchmark tests
func BenchmarkCalculateDCF(b *testing.B) {
	inputs := Inputs{
		BaseOperatingIncome: 100.0,
		GrowthRate:          0.10,
		TerminalGrowthRate:  0.025,
		WACC:                0.10,
		TaxRate:             0.25,
		ProjectionYears:     5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = CalculateDCF(inputs)
	}
}

func BenchmarkSensitivityAnalysis(b *testing.B) {
	inputs := Inputs{
		BaseOperatingIncome: 100.0,
		GrowthRate:          0.10,
		TerminalGrowthRate:  0.025,
		WACC:                0.10,
		TaxRate:             0.25,
		ProjectionYears:     5,
	}

	waccRange := []float64{0.08, 0.09, 0.10, 0.11, 0.12}
	growthRange := []float64{0.05, 0.08, 0.10, 0.12, 0.15}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = SensitivityAnalysis(inputs, waccRange, growthRange)
	}
}

// Integration test with realistic scenarios
func TestRealWorldDCFScenarios(t *testing.T) {
	t.Run("Apple-like tech giant", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 114.0, // Apple's recent operating income ~$114B
			GrowthRate:          0.08,  // Maturing growth ~8%
			TerminalGrowthRate:  0.025, // 2.5% long-term
			WACC:                0.095, // ~9.5% WACC
			TaxRate:             0.25,  // 25% tax rate
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should be in reasonable range for Apple-sized company
		assert.Greater(t, result.EnterpriseValue, 1000.0) // > $1T
		assert.Less(t, result.EnterpriseValue, 5000.0)    // < $5T
		assert.True(t, result.IsReasonable)
	})

	t.Run("Small growth company", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 2.0,  // $2B operating income
			GrowthRate:          0.25, // High growth 25%
			TerminalGrowthRate:  0.03, // 3% terminal
			WACC:                0.12, // Higher risk 12%
			TaxRate:             0.25, // 25% tax rate
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should reflect high growth premium
		assert.Greater(t, result.EnterpriseValue, 30.0) // > $30B
		assert.Less(t, result.EnterpriseValue, 200.0)   // < $200B
	})

	t.Run("Mature dividend aristocrat", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 15.0, // $15B operating income
			GrowthRate:          0.04, // Low steady growth 4%
			TerminalGrowthRate:  0.02, // 2% terminal
			WACC:                0.08, // Lower risk 8%
			TaxRate:             0.25, // 25% tax rate
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should show steady value, high terminal portion
		assert.Greater(t, result.TerminalValue/result.EnterpriseValue, 0.70) // >70% terminal
		assert.True(t, result.IsReasonable)
	})
}
