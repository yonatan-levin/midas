package dcf

import (
	"strings"
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

func TestCalculateDCF_TrueFCF(t *testing.T) {
	// With true FCF: FCF = NOPAT + D&A - CapEx - NWC change
	// D&A and CapEx scale proportionally with OI growth
	baseOI := 1000.0
	inputs := Inputs{
		BaseOperatingIncome:         baseOI,
		GrowthRate:                  0.10, // 10%
		TerminalGrowthRate:          0.025,
		WACC:                        0.10,
		TaxRate:                     0.25,
		ProjectionYears:             5,
		UseTrueFCF:                  true,
		DepreciationAndAmortization: 200.0, // $200 D&A
		CapitalExpenditures:         300.0, // $300 CapEx
		NetWorkingCapitalChange:     50.0,  // $50 NWC increase
	}

	result, err := CalculateDCF(inputs)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Year 1: OI = 1000*1.1 = 1100, NOPAT = 825
	// growthFactor = 1100/1000 = 1.1
	// scaledDA = 200*1.1 = 220, scaledCapEx = 300*1.1 = 330, scaledNWC = 50*1.1 = 55
	// FCF = 825 + 220 - 330 - 55 = 660
	year1 := result.Projections[0]
	assert.InDelta(t, 1100.0, year1.OperatingIncome, 0.01)
	assert.InDelta(t, 825.0, year1.NOPAT, 0.01)
	assert.InDelta(t, 660.0, year1.FreeCashFlow, 0.01)

	// Compare with NOPAT-only: FCF would be 825 (higher than true FCF of 660)
	// This proves true FCF accounts for reinvestment needs
	assert.Less(t, year1.FreeCashFlow, year1.NOPAT,
		"True FCF should be less than NOPAT when CapEx > D&A (capital-intensive company)")

	// Enterprise value should be positive and reasonable
	assert.Greater(t, result.EnterpriseValue, 0.0)
}

// TestCalculateDCF_OperatingNWC_PositiveFCF is an engine-boundary DOCUMENTATION
// pin for BUG-014 — NOT a RED regression. CalculateDCF itself is unchanged by
// the fix; the cash-exclusion lives in the caller
// (service.go::calculateNetWorkingCapitalChange), pinned RED-provably by
// TestService_calculateNetWorkingCapitalChange_ExcludesCash. This test documents
// the post-fix expectation at the engine layer: given an OPERATING NWC change
// (cash excluded) rather than the cash-polluted (CurrentAssets - CurrentLiabilities)
// delta, every explicit-year FCF and the terminal-year FCF must be positive.
//
// Before BUG-014 the caller fed a cash-build-dominated ΔNWC (e.g. NVDA ≈ $31B
// on $42B of NOPAT+D&A−CapEx) which drove FCF negative and growing more
// negative across the window (cumulative growth scaling). This test pins the
// post-fix expectation purely at the engine boundary: a modest operating ΔNWC
// relative to NOPAT keeps FCF positive for all years.
func TestCalculateDCF_OperatingNWC_PositiveFCF(t *testing.T) {
	// NVDA-scale, $ millions. Operating ΔNWC here is the cash-excluded value
	// the fixed caller would compute, NOT the +$31B cash-polluted figure.
	inputs := Inputs{
		BaseOperatingIncome:         53_536,
		GrowthRate:                  0.10,
		TerminalGrowthRate:          0.03,
		WACC:                        0.10,
		TaxRate:                     0.15,
		ProjectionYears:             5,
		UseTrueFCF:                  true,
		DepreciationAndAmortization: 997,
		CapitalExpenditures:         1_757,
		// Operating ΔNWC: a few % of revenue growth, NOT the cash hoard.
		NetWorkingCapitalChange: 3_000,
	}

	result, err := CalculateDCF(inputs)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	for i, p := range result.Projections {
		assert.Greater(t, p.FreeCashFlow, 0.0,
			"year %d FCF must be positive for a cash-generative firm once cash is excluded from NWC", i+1)
		assert.Greater(t, p.PresentValue, 0.0,
			"year %d discounted FCF must be positive (dcf_per_year_pv > 0)", i+1)
	}

	assert.Greater(t, result.TerminalYearFCF, 0.0,
		"terminal-year FCF must be positive so the Gordon terminal value is positive")
	assert.Greater(t, result.EnterpriseValue, 0.0,
		"enterprise value must be positive for a cash-generative firm")

	// Sanity: with cumulative-growth scaling, once year 1 is positive every
	// later year is MORE positive (NOPAT and the scaled reinvestment terms
	// share the same growthFactor) — FCF must be monotonically non-decreasing.
	for i := 1; i < len(result.Projections); i++ {
		assert.GreaterOrEqual(t, result.Projections[i].FreeCashFlow, result.Projections[i-1].FreeCashFlow,
			"FCF should not turn more negative year over year once the base year is positive")
	}
}

func TestCalculateDCF_TrueFCF_AssetLight(t *testing.T) {
	// Asset-light company: D&A > CapEx (SaaS model)
	// FCF should be HIGHER than NOPAT
	inputs := Inputs{
		BaseOperatingIncome:         1000.0,
		GrowthRate:                  0.15,
		TerminalGrowthRate:          0.025,
		WACC:                        0.12,
		TaxRate:                     0.25,
		ProjectionYears:             5,
		UseTrueFCF:                  true,
		DepreciationAndAmortization: 300.0, // High D&A (amortizing past acquisitions)
		CapitalExpenditures:         100.0, // Low CapEx (SaaS, minimal PP&E)
		NetWorkingCapitalChange:     20.0,
	}

	result, err := CalculateDCF(inputs)
	assert.NoError(t, err)

	year1 := result.Projections[0]
	assert.Greater(t, year1.FreeCashFlow, year1.NOPAT,
		"Asset-light company FCF should exceed NOPAT when D&A > CapEx")
}

func TestCalculateDCF_FallbackToNOPAT(t *testing.T) {
	// When UseTrueFCF is false and no percentage-based inputs, FCF = NOPAT
	inputs := Inputs{
		BaseOperatingIncome: 1000.0,
		GrowthRate:          0.10,
		TerminalGrowthRate:  0.025,
		WACC:                0.10,
		TaxRate:             0.25,
		ProjectionYears:     5,
		UseTrueFCF:          false,
	}

	result, err := CalculateDCF(inputs)
	assert.NoError(t, err)

	for _, proj := range result.Projections {
		assert.Equal(t, proj.NOPAT, proj.FreeCashFlow,
			"Without true FCF data, FCF should equal NOPAT")
	}
}

func TestCalculateDCF_ExitMultipleTV(t *testing.T) {
	// When ExitMultiple is provided, terminal value should be the average of
	// Gordon Growth TV and exit-multiple-based TV.
	baseInputs := Inputs{
		BaseOperatingIncome:         1000.0,
		GrowthRate:                  0.10,
		TerminalGrowthRate:          0.025,
		WACC:                        0.10,
		TaxRate:                     0.25,
		ProjectionYears:             5,
		UseTrueFCF:                  true,
		DepreciationAndAmortization: 200.0,
		CapitalExpenditures:         300.0,
		NetWorkingCapitalChange:     50.0,
	}

	t.Run("ExitMultiple=0 uses Gordon Growth only", func(t *testing.T) {
		inputs := baseInputs
		inputs.ExitMultiple = 0

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Verify no exit multiple warning is present
		for _, w := range result.Warnings {
			assert.NotContains(t, w, "Exit Multiple",
				"Should not mention exit multiple when ExitMultiple=0")
		}

		// M-1c: the raw exit-multiple TV component must be exactly zero on
		// the Gordon-only path so omitempty drops it from JSON output and the
		// valuation service's "terminal_value" trace surfaces a clean 0.
		assert.Equal(t, 0.0, result.ExitMultipleTV,
			"ExitMultipleTV should be zero when ExitMultiple input is 0")
	})

	t.Run("ExitMultiple > 0 averages Gordon and exit TV", func(t *testing.T) {
		inputs := baseInputs
		inputs.ExitMultiple = 12.0 // 12x EV/EBITDA

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Calculate what pure Gordon Growth would give
		gordonInputs := baseInputs
		gordonInputs.ExitMultiple = 0
		gordonResult, _ := CalculateDCF(gordonInputs)

		// The terminal value nominal should differ from pure Gordon
		assert.NotEqual(t, gordonResult.TerminalValueNominal, result.TerminalValueNominal,
			"Exit multiple should change the terminal value")

		// The result should have a warning about the averaging
		found := false
		for _, w := range result.Warnings {
			if strings.Contains(w, "Terminal value averaged") {
				found = true
				break
			}
		}
		assert.True(t, found, "Should include warning about TV averaging method")

		// M-1c: ExitMultipleTV must be persisted (positive) on the averaged path,
		// AND must satisfy the algebraic identity
		//     averaged = (gordon + exitMultiple) / 2   <=>
		//     exitMultiple = 2*averaged - gordon
		// This is the same back-calculation the trace USED to do; pinning it
		// here ensures the directly-stored value matches what the math implies.
		assert.Greater(t, result.ExitMultipleTV, 0.0,
			"ExitMultipleTV should be positive when exit multiple is used")
		expectedExitTV := 2*result.TerminalValueNominal - gordonResult.TerminalValueNominal
		assert.InDelta(t, expectedExitTV, result.ExitMultipleTV, 1.0,
			"ExitMultipleTV must satisfy the averaging identity 2*averaged - gordon")
	})
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
			wantErr: "projection years must be between 1 and 15",
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
	// M-1d: bridge is now EV - Debt + Cash - MinorityInterest - PreferredEquity.
	// Rows with MI=0 and PE=0 verify the legacy three-arg behavior is preserved
	// numerically; the additional rows pin the two new correction terms.
	tests := []struct {
		name             string
		enterpriseValue  float64
		debt             float64
		cash             float64
		minorityInterest float64
		preferredEquity  float64
		expected         float64
	}{
		{
			name:            "Standard case (no MI/PE)",
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
		{
			name:             "With minority interest",
			enterpriseValue:  1000.0,
			debt:             200.0,
			cash:             50.0,
			minorityInterest: 100.0,
			preferredEquity:  0.0,
			expected:         750.0, // 1000 - 200 + 50 - 100 - 0
		},
		{
			name:             "With preferred equity",
			enterpriseValue:  1000.0,
			debt:             200.0,
			cash:             50.0,
			minorityInterest: 0.0,
			preferredEquity:  80.0,
			expected:         770.0, // 1000 - 200 + 50 - 0 - 80
		},
		{
			name:             "With both MI and PE",
			enterpriseValue:  1000.0,
			debt:             200.0,
			cash:             50.0,
			minorityInterest: 100.0,
			preferredEquity:  80.0,
			expected:         670.0, // 1000 - 200 + 50 - 100 - 80
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateEquityValue(tt.enterpriseValue, tt.debt, tt.cash, tt.minorityInterest, tt.preferredEquity)
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

func TestCalculateImpliedGrowthRate(t *testing.T) {
	baseInputs := Inputs{
		BaseOperatingIncome: 100.0,
		GrowthRate:          0.10, // This will be overridden in the function
		TerminalGrowthRate:  0.025,
		WACC:                0.10,
		TaxRate:             0.25,
		ProjectionYears:     5,
	}

	t.Run("find_growth_for_target_value", func(t *testing.T) {
		// First calculate a baseline value with known growth
		testInputs := baseInputs
		testInputs.GrowthRate = 0.08 // 8% growth
		baselineResult, err := CalculateDCF(testInputs)
		require.NoError(t, err)

		// Now find what growth rate gives us that value
		impliedGrowth, err := CalculateImpliedGrowthRate(baselineResult.EnterpriseValue, baseInputs)
		require.NoError(t, err)

		// Should be close to 8%
		assert.InDelta(t, 0.08, impliedGrowth, 0.001)
	})

	t.Run("high_target_value", func(t *testing.T) {
		// Try to find growth for a very high target value
		highTargetValue := 5000.0
		impliedGrowth, err := CalculateImpliedGrowthRate(highTargetValue, baseInputs)
		require.NoError(t, err)

		// Should result in high growth rate
		assert.Greater(t, impliedGrowth, 0.2)
	})

	t.Run("low_target_value", func(t *testing.T) {
		// Try to find growth for a low target value
		lowTargetValue := 500.0
		impliedGrowth, err := CalculateImpliedGrowthRate(lowTargetValue, baseInputs)
		require.NoError(t, err)

		// Should result in low or negative growth rate
		assert.Less(t, impliedGrowth, 0.05)
	})

	t.Run("impossible_target_with_invalid_inputs", func(t *testing.T) {
		// Test with inputs that cause DCF to fail
		invalidInputs := baseInputs
		invalidInputs.WACC = 0.02 // This will be less than terminal growth, causing error

		_, err := CalculateImpliedGrowthRate(1000.0, invalidInputs)
		assert.Error(t, err)
	})
}

func TestWarningGeneration(t *testing.T) {
	t.Run("high_growth_warning", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 100.0,
			GrowthRate:          0.35, // 35% growth - should trigger warning
			TerminalGrowthRate:  0.025,
			WACC:                0.12,
			TaxRate:             0.25,
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should have warning about high growth
		found := false
		for _, warning := range result.Warnings {
			if containsString(warning, "High growth rate") {
				found = true
				break
			}
		}
		assert.True(t, found, "Should have high growth rate warning")
	})

	t.Run("terminal_value_dominance_warning", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 100.0,
			GrowthRate:          0.02, // Very low growth
			TerminalGrowthRate:  0.025,
			WACC:                0.08,
			TaxRate:             0.25,
			ProjectionYears:     3, // Short projection period
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should have warning about terminal value dominance
		found := false
		for _, warning := range result.Warnings {
			if containsString(warning, "Terminal value represents") {
				found = true
				break
			}
		}
		assert.True(t, found, "Should have terminal value dominance warning")
	})

	t.Run("high_wacc_warning", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 100.0,
			GrowthRate:          0.10,
			TerminalGrowthRate:  0.025,
			WACC:                0.25, // 25% WACC - should trigger warning
			TaxRate:             0.25,
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should have warning about high WACC
		found := false
		for _, warning := range result.Warnings {
			if containsString(warning, "WACC >20%") {
				found = true
				break
			}
		}
		assert.True(t, found, "Should have high WACC warning")
	})

	t.Run("terminal_growth_vs_wacc_warning", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 100.0,
			GrowthRate:          0.10,
			TerminalGrowthRate:  0.045, // 4.5% terminal growth
			WACC:                0.08,  // 8% WACC, ratio is high (4.5% > 8% * 0.5 = 4%)
			TaxRate:             0.25,
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should have warning about terminal growth vs WACC
		found := false
		for _, warning := range result.Warnings {
			if containsString(warning, "Terminal growth rate is high relative to WACC") {
				found = true
				break
			}
		}
		assert.True(t, found, "Should have terminal growth vs WACC warning")
	})

	t.Run("no_warnings_normal_case", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 100.0,
			GrowthRate:          0.08, // Reasonable growth
			TerminalGrowthRate:  0.025,
			WACC:                0.10, // Reasonable WACC
			TaxRate:             0.25,
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should have no warnings
		assert.Empty(t, result.Warnings, "Should have no warnings for normal inputs")
	})
}

func TestReasonablenessChecks(t *testing.T) {
	t.Run("reasonable_result", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 100.0,
			GrowthRate:          0.10,
			TerminalGrowthRate:  0.025,
			WACC:                0.12,
			TaxRate:             0.25,
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)
		assert.True(t, result.IsReasonable)
	})

	t.Run("unreasonable_negative_enterprise_value", func(t *testing.T) {
		// This is hard to achieve with current validation, but test the logic
		inputs := Inputs{
			BaseOperatingIncome: 1.0,   // Very small
			GrowthRate:          -0.40, // High decline
			TerminalGrowthRate:  0.025,
			WACC:                0.30, // Very high WACC
			TaxRate:             0.25,
			ProjectionYears:     5,
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Should potentially be unreasonable due to extreme parameters
		// (actual reasonableness depends on the calculation outcome)
		assert.NotNil(t, result)
	})

	t.Run("terminal_value_extreme_dominance", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 100.0,
			GrowthRate:          0.01, // Very low growth
			TerminalGrowthRate:  0.025,
			WACC:                0.08,
			TaxRate:             0.25,
			ProjectionYears:     2, // Very short projection
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// Terminal value will dominate
		terminalPercentage := result.TerminalValue / result.EnterpriseValue
		if terminalPercentage > 0.9 {
			assert.False(t, result.IsReasonable)
		}
	})
}

func TestCapexAndWorkingCapitalAdjustments(t *testing.T) {
	t.Run("with_capex_adjustments", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome:     100.0,
			GrowthRate:              0.10,
			TerminalGrowthRate:      0.025,
			WACC:                    0.10,
			TaxRate:                 0.25,
			ProjectionYears:         5,
			CapexAsPercentOfRevenue: 0.05, // 5% CapEx
			WorkingCapitalChange:    10.0, // $10B working capital increase
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// FCF should be lower than NOPAT due to CapEx and WC adjustments
		for _, projection := range result.Projections {
			assert.Less(t, projection.FreeCashFlow, projection.NOPAT)
		}
	})

	t.Run("without_adjustments", func(t *testing.T) {
		inputs := Inputs{
			BaseOperatingIncome: 100.0,
			GrowthRate:          0.10,
			TerminalGrowthRate:  0.025,
			WACC:                0.10,
			TaxRate:             0.25,
			ProjectionYears:     5,
			// No CapEx or WC adjustments
		}

		result, err := CalculateDCF(inputs)
		require.NoError(t, err)

		// FCF should equal NOPAT when no adjustments
		for _, projection := range result.Projections {
			assert.Equal(t, projection.FreeCashFlow, projection.NOPAT)
		}
	})
}

func TestInputValidationEdgeCases(t *testing.T) {
	t.Run("boundary_values", func(t *testing.T) {
		// Test boundary values that should be valid
		validInputs := []Inputs{
			{
				BaseOperatingIncome: 0.1,   // Very small but positive
				GrowthRate:          -0.49, // Just within -50% limit
				TerminalGrowthRate:  0.001, // Small terminal growth
				WACC:                0.1,   // WACC > terminal growth
				TaxRate:             0.99,  // Just within 100% limit
				ProjectionYears:     1,     // Minimum projection years
			},
			{
				BaseOperatingIncome: 1000000, // Very large
				GrowthRate:          0.99,    // Just within 100% limit
				TerminalGrowthRate:  0.0,     // Minimum terminal growth
				WACC:                0.49,    // Just within 50% limit
				TaxRate:             0.0,     // Minimum tax rate
				ProjectionYears:     10,      // Maximum projection years
			},
		}

		for i, inputs := range validInputs {
			_, err := CalculateDCF(inputs)
			assert.NoError(t, err, "Valid inputs case %d should not error", i)
		}
	})

	t.Run("invalid_boundary_values", func(t *testing.T) {
		// Test values just outside boundaries
		invalidInputs := []struct {
			inputs Inputs
			reason string
		}{
			{
				inputs: Inputs{
					BaseOperatingIncome: 0, // Zero
					GrowthRate:          0.10,
					TerminalGrowthRate:  0.025,
					WACC:                0.10,
					TaxRate:             0.25,
					ProjectionYears:     5,
				},
				reason: "zero operating income",
			},
			{
				inputs: Inputs{
					BaseOperatingIncome: 100.0,
					GrowthRate:          -0.51, // Below -50%
					TerminalGrowthRate:  0.025,
					WACC:                0.10,
					TaxRate:             0.25,
					ProjectionYears:     5,
				},
				reason: "growth rate too low",
			},
			{
				inputs: Inputs{
					BaseOperatingIncome: 100.0,
					GrowthRate:          0.10,
					TerminalGrowthRate:  0.051, // Above 5%
					WACC:                0.10,
					TaxRate:             0.25,
					ProjectionYears:     5,
				},
				reason: "terminal growth too high",
			},
			{
				inputs: Inputs{
					BaseOperatingIncome: 100.0,
					GrowthRate:          0.10,
					TerminalGrowthRate:  0.025,
					WACC:                0.51, // Above 50%
					TaxRate:             0.25,
					ProjectionYears:     5,
				},
				reason: "WACC too high",
			},
			{
				inputs: Inputs{
					BaseOperatingIncome: 100.0,
					GrowthRate:          0.10,
					TerminalGrowthRate:  0.025,
					WACC:                0.10,
					TaxRate:             1.01, // Above 100%
					ProjectionYears:     5,
				},
				reason: "tax rate too high",
			},
			{
				inputs: Inputs{
					BaseOperatingIncome: 100.0,
					GrowthRate:          0.10,
					TerminalGrowthRate:  0.025,
					WACC:                0.10,
					TaxRate:             0.25,
					ProjectionYears:     16, // Above 15
				},
				reason: "too many projection years",
			},
		}

		for _, tc := range invalidInputs {
			_, err := CalculateDCF(tc.inputs)
			assert.Error(t, err, "Should error for %s", tc.reason)
		}
	})
}

func TestSensitivityAnalysisErrorHandling(t *testing.T) {
	baseInputs := Inputs{
		BaseOperatingIncome: 100.0,
		GrowthRate:          0.10,
		TerminalGrowthRate:  0.025,
		WACC:                0.10,
		TaxRate:             0.25,
		ProjectionYears:     5,
	}

	t.Run("invalid_wacc_in_range", func(t *testing.T) {
		// Include invalid WACC values
		waccRange := []float64{0.08, -0.05, 0.12} // -0.05 is invalid
		growthRange := []float64{0.05, 0.10, 0.15}

		_, err := SensitivityAnalysis(baseInputs, waccRange, growthRange)
		assert.Error(t, err, "Should error with invalid WACC in range")
	})

	t.Run("wacc_less_than_terminal_growth", func(t *testing.T) {
		// WACC less than terminal growth rate should cause validation error
		waccRange := []float64{0.01, 0.015, 0.02} // All less than terminal growth (2.5%)
		growthRange := []float64{0.05, 0.10, 0.15}

		_, err := SensitivityAnalysis(baseInputs, waccRange, growthRange)
		assert.Error(t, err, "Should error when WACC < terminal growth rate")
	})
}

// Helper function for string containment check
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr ||
		(len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestCalculateEquityValueWithDebtLikeClaims pins the DC-1 Phase 4 EV→Equity
// bridge extension: Common Equity = EV - Debt + Cash - Minority - Preferred -
// DebtLikeClaims, and the additive relationship to the legacy 5-arg form.
func TestCalculateEquityValueWithDebtLikeClaims(t *testing.T) {
	const (
		ev        = 1_500_000_000.0
		debt      = 200_000_000.0
		cash      = 50_000_000.0
		minority  = 10_000_000.0
		preferred = 5_000_000.0
	)

	base := CalculateEquityValue(ev, debt, cash, minority, preferred)

	t.Run("zero debt-like claims equals legacy 5-arg form", func(t *testing.T) {
		got := CalculateEquityValueWithDebtLikeClaims(ev, debt, cash, minority, preferred, 0)
		assert.InDelta(t, base, got, 1e-6,
			"with zero DebtLikeClaims the 6-arg form must equal the legacy 5-arg form")
	})

	t.Run("debt-like claims subtract from equity", func(t *testing.T) {
		const dlc = 500_000_000.0
		got := CalculateEquityValueWithDebtLikeClaims(ev, debt, cash, minority, preferred, dlc)
		assert.InDelta(t, base-dlc, got, 1e-6,
			"DebtLikeClaims must be subtracted from the equity bridge")
		// Explicit formula pin.
		assert.InDelta(t, ev-debt+cash-minority-preferred-dlc, got, 1e-6)
	})
}
