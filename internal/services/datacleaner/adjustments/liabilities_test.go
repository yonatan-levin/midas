package adjustments

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestLiabilityAdjuster_ProcessOperatingLeaseAdjustment(t *testing.T) {
	adjuster := NewLiabilityAdjuster()

	tests := []struct {
		name           string
		data           *entities.FinancialData
		context        *entities.CleaningContext
		rule           *entities.CleaningRule
		expectApplied  bool
		expectAmount   float64
		expectFlags    int
		expectSeverity entities.FlagSeverity
	}{
		{
			name: "No lease liabilities present",
			data: &entities.FinancialData{
				Ticker:                  "TEST",
				OperatingLeaseLiability: 0.0,
				TotalAssets:             1000000,
				Revenue:                 500000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			rule: &entities.CleaningRule{
				ID:       "operating_leases",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied: false,
			expectAmount:  0.0,
			expectFlags:   0,
		},
		{
			name: "Technology company with minimal leases",
			data: &entities.FinancialData{
				Ticker:                  "TECH",
				OperatingLeaseLiability: 50000, // 5% of assets
				TotalAssets:             1000000,
				Revenue:                 500000,
				TotalDebt:               200000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			rule: &entities.CleaningRule{
				ID:       "operating_leases",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied:  true,
			expectAmount:   50000,
			expectFlags:    0,                        // No flags - below materiality threshold and medium quality is acceptable
			expectSeverity: entities.FlagSeverityLow, // Not used since no flags
		},
		{
			name: "Retail company with significant lease obligations",
			data: &entities.FinancialData{
				Ticker:                  "RETAIL",
				OperatingLeaseLiability: 200000, // 20% of assets
				TotalAssets:             1000000,
				Revenue:                 800000,
				TotalDebt:               150000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "44", // Retail
			},
			rule: &entities.CleaningRule{
				ID:       "operating_leases",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied:  true,
			expectAmount:   200000,
			expectFlags:    1,                        // Only materiality flag
			expectSeverity: entities.FlagSeverityLow, // 20% vs 15% threshold = low severity
		},
		{
			name: "Manufacturing with equipment leases",
			data: &entities.FinancialData{
				Ticker:                  "MFG",
				OperatingLeaseLiability: 120000, // 12% of assets
				TotalAssets:             1000000,
				Revenue:                 600000,
				TotalDebt:               300000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "31", // Manufacturing
			},
			rule: &entities.CleaningRule{
				ID:       "operating_leases",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied:  true,
			expectAmount:   120000,
			expectFlags:    1,                        // Only materiality flag
			expectSeverity: entities.FlagSeverityLow, // 12% vs 12% threshold = low severity
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adjuster.ProcessOperatingLeaseAdjustment(tt.data, tt.rule, tt.context)

			assert.Equal(t, tt.expectApplied, result.Applied, "Applied flag mismatch")
			assert.Equal(t, tt.expectAmount, result.Amount, "Adjustment amount mismatch")
			assert.Len(t, result.Flags, tt.expectFlags, "Number of flags mismatch")

			if len(result.Flags) > 0 {
				assert.Equal(t, tt.expectSeverity, result.Flags[0].Severity, "Flag severity mismatch")
			}
		})
	}
}

func TestLiabilityAdjuster_ProcessPensionAdjustment(t *testing.T) {
	adjuster := NewLiabilityAdjuster()

	tests := []struct {
		name           string
		data           *entities.FinancialData
		context        *entities.CleaningContext
		rule           *entities.CleaningRule
		expectApplied  bool
		expectAmount   float64
		expectFlags    int
		expectSeverity entities.FlagSeverity
	}{
		{
			name: "No pension obligations",
			data: &entities.FinancialData{
				Ticker:             "TEST",
				PensionLiabilities: 0.0,
				OPEBLiability:      0.0,
				TotalAssets:        1000000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			rule: &entities.CleaningRule{
				ID:       "pension_obligations",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied: false,
			expectAmount:  0.0,
			expectFlags:   0,
		},
		{
			name: "Under-funded pension plan",
			data: &entities.FinancialData{
				Ticker:                     "UTILITY",
				PensionLiabilities:         0.0, // Will be calculated
				ProjectedBenefitObligation: 500000,
				PensionPlanAssets:          300000, // Under-funded by 200,000
				OPEBLiability:              50000,
				TotalAssets:                2000000,
				Revenue:                    1000000,
				TotalDebt:                  400000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "22", // Utilities
			},
			rule: &entities.CleaningRule{
				ID:       "pension_obligations",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied:  true,
			expectAmount:   250000, // 200k underfunding + 50k OPEB
			expectFlags:    1,
			expectSeverity: entities.FlagSeverityCritical, // 25% >= 15% critical threshold
		},
		{
			name: "Over-funded pension plan",
			data: &entities.FinancialData{
				Ticker:                     "TECH",
				ProjectedBenefitObligation: 300000,
				PensionPlanAssets:          350000, // Over-funded by 50,000
				OPEBLiability:              0.0,
				TotalAssets:                1000000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			rule: &entities.CleaningRule{
				ID:       "pension_obligations",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied: false, // No adjustment needed for over-funded plans
			expectAmount:  0.0,
			expectFlags:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adjuster.ProcessPensionAdjustment(tt.data, tt.rule, tt.context)

			assert.Equal(t, tt.expectApplied, result.Applied, "Applied flag mismatch")
			assert.Equal(t, tt.expectAmount, result.Amount, "Adjustment amount mismatch")
			assert.Len(t, result.Flags, tt.expectFlags, "Number of flags mismatch")

			if len(result.Flags) > 0 {
				assert.Equal(t, tt.expectSeverity, result.Flags[0].Severity, "Flag severity mismatch")
			}
		})
	}
}

func TestLiabilityAdjuster_ProcessContingentLiabilityAdjustment(t *testing.T) {
	adjuster := NewLiabilityAdjuster()

	tests := []struct {
		name           string
		data           *entities.FinancialData
		context        *entities.CleaningContext
		rule           *entities.CleaningRule
		expectApplied  bool
		expectAmount   float64
		expectFlags    int
		expectSeverity entities.FlagSeverity
	}{
		{
			name: "No contingent liabilities",
			data: &entities.FinancialData{
				Ticker:                   "TEST",
				ContingentLiabilities:    0.0,
				EnvironmentalLiabilities: 0.0,
				LitigationLiabilities:    0.0,
				Revenue:                  1000000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			rule: &entities.CleaningRule{
				ID:       "contingent_liabilities",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied: false,
			expectAmount:  0.0,
			expectFlags:   0,
		},
		{
			name: "Technology company with patent litigation",
			data: &entities.FinancialData{
				Ticker:                "TECH",
				LitigationLiabilities: 25000, // 2.5% of revenue
				Revenue:               1000000,
				TotalAssets:           2000000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			rule: &entities.CleaningRule{
				ID:       "contingent_liabilities",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied:  true,
			expectAmount:   10000, // 25k * 40% probability weighting
			expectFlags:    1,
			expectSeverity: entities.FlagSeverityLow, // 2.5% < (2% * 1.5 = 3%)
		},
		{
			name: "Energy company with environmental liabilities",
			data: &entities.FinancialData{
				Ticker:                   "ENERGY",
				EnvironmentalLiabilities: 100000, // 10% of revenue
				ContingentLiabilities:    50000,  // 5% of revenue
				Revenue:                  1000000,
				TotalAssets:              3000000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "21", // Energy
			},
			rule: &entities.CleaningRule{
				ID:       "contingent_liabilities",
				Category: entities.LiabilityCompleteness,
			},
			expectApplied:  true,
			expectAmount:   90000, // (100k + 50k) * 60% probability weighting
			expectFlags:    1,
			expectSeverity: entities.FlagSeverityCritical, // 15% >= (3% * 3 = 9%)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adjuster.ProcessContingentLiabilityAdjustment(tt.data, tt.rule, tt.context)

			assert.Equal(t, tt.expectApplied, result.Applied, "Applied flag mismatch")
			assert.Equal(t, tt.expectAmount, result.Amount, "Adjustment amount mismatch")
			assert.Len(t, result.Flags, tt.expectFlags, "Number of flags mismatch")

			if len(result.Flags) > 0 {
				assert.Equal(t, tt.expectSeverity, result.Flags[0].Severity, "Flag severity mismatch")
			}
		})
	}
}

func TestLiabilityAdjuster_ProcessLiabilityAdjustments(t *testing.T) {
	adjuster := NewLiabilityAdjuster()

	// Comprehensive test with multiple liability types
	data := &entities.FinancialData{
		Ticker:                     "COMPREHENSIVE",
		OperatingLeaseLiability:    150000,
		ProjectedBenefitObligation: 400000,
		PensionPlanAssets:          250000, // Under-funded by 150,000
		OPEBLiability:              75000,
		ContingentLiabilities:      50000,
		EnvironmentalLiabilities:   25000,
		TotalAssets:                2000000,
		Revenue:                    1000000,
		TotalDebt:                  300000,
		InterestBearingDebt:        300000,
	}

	context := &entities.CleaningContext{
		IndustryCode:     "44", // Retail
		CompanySize:      entities.LargeCap,
		DataVintage:      time.Now(),
		EnableIndustry:   true,
		EnableCaching:    false,
		QualityThreshold: 0.8,
	}

	rules := []*entities.CleaningRule{
		{
			ID:       "operating_leases",
			Category: entities.LiabilityCompleteness,
			Enabled:  true,
		},
		{
			ID:       "pension_obligations",
			Category: entities.LiabilityCompleteness,
			Enabled:  true,
		},
		{
			ID:       "contingent_liabilities",
			Category: entities.LiabilityCompleteness,
			Enabled:  true,
		},
	}

	result := adjuster.ProcessLiabilityAdjustments(data, rules, context)

	// Verify comprehensive results
	require.NotNil(t, result, "Result should not be nil")
	assert.True(t, result.Applied, "Adjustments should be applied")
	assert.Greater(t, result.TotalLiabilityAdjustment, float64(0), "Total adjustment should be positive")
	assert.Len(t, result.Adjustments, 3, "Should have adjustments for all categories")
	assert.Greater(t, len(result.Flags), 0, "Should generate flags")

	// Calculate expected debt increase:
	// Operating leases: 150,000 (treated as debt)
	// Pension underfunding: (400,000 - 250,000) + 75,000 = 225,000
	// Contingent liabilities: (50,000 + 25,000) * 30% default probability = 22,500
	expectedDebtIncrease := 150000 + 225000 + 22500    // Total: 397,500
	expectedFinalDebt := 300000 + expectedDebtIncrease // 697,500

	// Verify debt was adjusted (allowing for small rounding differences)
	assert.InDelta(t, expectedFinalDebt, data.TotalDebt, 1000, "Total debt should include liability adjustments")
}

func TestLiabilityAdjuster_IndustryThresholds(t *testing.T) {
	adjuster := NewLiabilityAdjuster()

	tests := []struct {
		name         string
		industryCode string
		expectedType string
	}{
		{"Technology", "45", "low_lease_tolerance"},
		{"Retail Trade", "44", "high_lease_tolerance"},
		{"Manufacturing", "31", "moderate_lease_tolerance"},
		{"Utilities", "22", "high_pension_tolerance"},
		{"Financial Services", "52", "minimal_lease_tolerance"},
		{"Energy", "21", "high_environmental_risk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threshold := adjuster.getLeaseThresholdForIndustry(tt.industryCode)
			assert.Greater(t, threshold, float64(0), "Threshold should be positive")

			// Verify industry-specific thresholds are different
			techThreshold := adjuster.getLeaseThresholdForIndustry("45")
			retailThreshold := adjuster.getLeaseThresholdForIndustry("44")
			assert.NotEqual(t, techThreshold, retailThreshold, "Industry thresholds should differ")
		})
	}
}

// Helper functions for test data creation
func createTestFinancialData(ticker string) *entities.FinancialData {
	return &entities.FinancialData{
		Ticker:      ticker,
		AsOf:        time.Now(),
		TotalAssets: 1000000,
		Revenue:     500000,
		TotalDebt:   200000,
		Period:      "2023Q4",
		FilingDate:  time.Now(),
	}
}

func createTestCleaningContext(industryCode string) *entities.CleaningContext {
	return &entities.CleaningContext{
		IndustryCode:     industryCode,
		CompanySize:      entities.LargeCap,
		DataVintage:      time.Now(),
		EnableIndustry:   true,
		EnableCaching:    false,
		QualityThreshold: 0.8,
	}
}

func createTestLiabilityRule(ruleID string) *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:       ruleID,
		Category: entities.LiabilityCompleteness,
		Enabled:  true,
		Version:  "1.0",
	}
}
