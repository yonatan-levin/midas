package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// TestDataCleaner_B3_ContingentLiabilities_BaselineNoAI verifies that contingent liability
// processing with AI disabled produces consistent, deterministic outputs. This test serves
// as a regression lock before integrating AI-powered analysis.
func TestDataCleaner_B3_ContingentLiabilities_BaselineNoAI(t *testing.T) {
	// Setup test environment with AI disabled (default)
	testEnv := SetupTestEnvironment(t)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	// Verify AI is disabled in config (critical for baseline)
	require.False(t, testEnv.Config.DataCleaner.EnableAIIntegration, "AI integration must be disabled for baseline test")

	// Create DataCleaner service instance
	// Create mock AI service for test (AI disabled by default)
	mockAI := ai.NewMockAIService(&ai.AIServiceConfig{})

	dataCleaner, err := datacleaner.NewDataCleanerService(testEnv.Config, mockAI, nil)
	require.NoError(t, err, "Failed to create DataCleaner service")

	ctx := context.Background()

	// Test cases with realistic contingent liability scenarios
	tests := []struct {
		name                        string
		data                        *entities.FinancialData
		expectedApplied             bool
		expectedAdjustmentAmount    float64 // Exact amount expected
		expectedFlagCount           int
		expectedFlagSeverity        entities.FlagSeverity
		expectedAdjustmentReasoning string // Partial match on reasoning
	}{
		{
			name: "No contingent liabilities - no adjustment",
			data: &entities.FinancialData{
				Ticker:                   "TEST1",
				ContingentLiabilities:    0.0,
				EnvironmentalLiabilities: 0.0,
				LitigationLiabilities:    0.0,
				Revenue:                  1000000,
				TotalAssets:              2000000,
				TotalDebt:                500000,
				SharesOutstanding:        1000000,    // Required for validation
				DilutedSharesOutstanding: 1100000,    // Required for validation
				FilingDate:               time.Now(), // Required for validation
			},
			expectedApplied:          false,
			expectedAdjustmentAmount: 0.0,
			expectedFlagCount:        0,
		},
		{
			name: "Technology company with patent litigation (2.5% revenue)",
			data: &entities.FinancialData{
				Ticker:                   "TECH",
				ContingentLiabilities:    0.0,
				LitigationLiabilities:    25000, // 2.5% of revenue
				Revenue:                  1000000,
				TotalAssets:              2000000,
				TotalDebt:                300000,
				SharesOutstanding:        1000000,    // Required for validation
				DilutedSharesOutstanding: 1100000,    // Required for validation
				FilingDate:               time.Now(), // Required for validation
			},
			expectedApplied:             true,
			expectedAdjustmentAmount:    10000, // 25k * 40% probability weighting (tech industry)
			expectedFlagCount:           1,
			expectedFlagSeverity:        entities.FlagSeverityLow,
			expectedAdjustmentReasoning: "40% probability weighting",
		},
		{
			name: "High-risk industry with environmental liabilities (5% revenue)",
			data: &entities.FinancialData{
				Ticker:                   "CHEM",
				ContingentLiabilities:    0.0,
				EnvironmentalLiabilities: 50000, // 5% of revenue
				LitigationLiabilities:    0.0,
				Revenue:                  1000000,
				TotalAssets:              3000000,
				TotalDebt:                800000,
				SharesOutstanding:        1000000,    // Required for validation
				DilutedSharesOutstanding: 1100000,    // Required for validation
				FilingDate:               time.Now(), // Required for validation
			},
			expectedApplied:             true,
			expectedAdjustmentAmount:    30000, // 50k * 60% probability weighting (energy industry)
			expectedFlagCount:           1,
			expectedFlagSeverity:        entities.FlagSeverityMedium,
			expectedAdjustmentReasoning: "60% probability weighting",
		},
		{
			name: "Multiple contingent liability types combined",
			data: &entities.FinancialData{
				Ticker:                   "MULTI",
				ContingentLiabilities:    15000, // 1.5% of revenue
				EnvironmentalLiabilities: 20000, // 2.0% of revenue
				LitigationLiabilities:    10000, // 1.0% of revenue
				Revenue:                  1000000,
				TotalAssets:              2500000,
				TotalDebt:                600000,
				SharesOutstanding:        1000000,    // Required for validation
				DilutedSharesOutstanding: 1100000,    // Required for validation
				FilingDate:               time.Now(), // Required for validation
			},
			expectedApplied:             true,
			expectedAdjustmentAmount:    22500, // 45k total * 50% probability weighting (healthcare industry)
			expectedFlagCount:           1,
			expectedFlagSeverity:        entities.FlagSeverityMedium, // 4.5% ratio will be classified as medium severity (exactly 1.5x threshold)
			expectedAdjustmentReasoning: "50% probability weighting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Execute data cleaning
			result, err := dataCleaner.CleanFinancialData(ctx, tt.data)
			require.NoError(t, err, "CleanFinancialData should not error")
			require.NotNil(t, result, "CleaningResult should not be nil")

			// Debug: Log what we got
			t.Logf("Total adjustments: %d", len(result.Adjustments))
			for i, adj := range result.Adjustments {
				t.Logf("Adjustment %d: ID=%s, Category=%s, RuleID=%s, Amount=%.2f", i, adj.ID, adj.Category, adj.RuleID, adj.Amount)
			}
			t.Logf("Total flags: %d", len(result.Flags))
			for i, flag := range result.Flags {
				t.Logf("Flag %d: ID=%s, RuleID=%s, Type=%s, Severity=%s", i, flag.ID, flag.RuleID, flag.Type, flag.Severity)
			}

			// Filter for Category B (LiabilityCompleteness) adjustments specifically
			var liabilityAdjustments []entities.Adjustment
			var liabilityFlags []entities.Flag

			for _, adj := range result.Adjustments {
				if adj.Category == entities.LiabilityCompleteness && adj.RuleID == "contingent_liabilities" {
					liabilityAdjustments = append(liabilityAdjustments, adj)
				}
			}

			for _, flag := range result.Flags {
				if flag.RuleID == "contingent_liabilities" {
					liabilityFlags = append(liabilityFlags, flag)
				}
			}

			// Verify adjustment application
			if tt.expectedApplied {
				assert.NotEmpty(t, liabilityAdjustments, "Expected contingent liability adjustments to be applied")

				if len(liabilityAdjustments) > 0 {
					totalAdjustment := 0.0
					for _, adj := range liabilityAdjustments {
						totalAdjustment += adj.Amount
					}

					// Assert exact adjustment amount (critical for baseline)
					assert.InDelta(t, tt.expectedAdjustmentAmount, totalAdjustment, 0.01,
						"Adjustment amount must be exact for baseline: expected %.2f, got %.2f",
						tt.expectedAdjustmentAmount, totalAdjustment)

					// Verify reasoning contains expected probability weighting info
					if tt.expectedAdjustmentReasoning != "" {
						found := false
						for _, adj := range liabilityAdjustments {
							if assert.Contains(t, adj.Reasoning, tt.expectedAdjustmentReasoning) {
								found = true
								break
							}
						}
						assert.True(t, found, "Expected reasoning '%s' not found in any adjustment", tt.expectedAdjustmentReasoning)
					}
				}
			} else {
				assert.Empty(t, liabilityAdjustments, "Expected no contingent liability adjustments to be applied")
			}

			// Verify flag generation
			assert.Len(t, liabilityFlags, tt.expectedFlagCount,
				"Expected %d flags, got %d", tt.expectedFlagCount, len(liabilityFlags))

			if tt.expectedFlagCount > 0 && len(liabilityFlags) > 0 {
				assert.Equal(t, tt.expectedFlagSeverity, liabilityFlags[0].Severity,
					"Flag severity should match expected baseline")
			}

			// Verify that debt was adjusted correctly if adjustments were applied
			if tt.expectedApplied && len(liabilityAdjustments) > 0 {
				expectedDebt := tt.data.TotalDebt + tt.expectedAdjustmentAmount
				assert.InDelta(t, expectedDebt, result.CleanedData.TotalDebt, 0.01,
					"Total debt should be increased by adjustment amount")
			}

			// Critical baseline checks: Ensure no AI-specific fields or metadata
			for _, adj := range liabilityAdjustments {
				assert.NotContains(t, adj.Reasoning, "AI", "Baseline should not reference AI")
				assert.NotContains(t, adj.Reasoning, "footnote", "Baseline should not reference footnotes")
				assert.NotContains(t, adj.Reasoning, "confidence", "Baseline should not reference AI confidence")
			}

			for _, flag := range liabilityFlags {
				assert.NotContains(t, flag.Description, "AI", "Baseline flags should not reference AI")
				assert.NotContains(t, flag.Description, "footnote", "Baseline flags should not reference footnotes")
			}
		})
	}
}

// TestDataCleaner_B3_IndustrySpecificProbabilities verifies that different industries
// apply different probability weightings as expected in the baseline (pre-AI) implementation.
func TestDataCleaner_B3_IndustrySpecificProbabilities(t *testing.T) {
	testEnv := SetupTestEnvironment(t)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	require.False(t, testEnv.Config.DataCleaner.EnableAIIntegration, "AI must be disabled for baseline")

	// Create mock AI service for test
	mockAI := ai.NewMockAIService(&ai.AIServiceConfig{})

	dataCleaner, err := datacleaner.NewDataCleanerService(testEnv.Config, mockAI, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Identical financial data across different industries to test probability variations
	baseData := &entities.FinancialData{
		ContingentLiabilities:    30000, // 3% of revenue
		Revenue:                  1000000,
		TotalAssets:              2000000,
		TotalDebt:                500000,
		SharesOutstanding:        1000000,    // Required for validation
		DilutedSharesOutstanding: 1100000,    // Required for validation
		FilingDate:               time.Now(), // Required for validation
	}

	industryTests := []struct {
		name                     string
		ticker                   string
		expectedProbabilityRange [2]float64 // [min, max] percentage
	}{
		{
			name:                     "Technology - Conservative probability",
			ticker:                   "TECH",
			expectedProbabilityRange: [2]float64{35.0, 45.0}, // Expect ~40%
		},
		{
			name:                     "Manufacturing - Higher probability",
			ticker:                   "MFG",
			expectedProbabilityRange: [2]float64{65.0, 75.0}, // Expect ~70% (industry classifier config)
		},
		{
			name:                     "Chemical - Higher environmental risk",
			ticker:                   "CHEM",
			expectedProbabilityRange: [2]float64{50.0, 60.0}, // Expect ~55%
		},
	}

	for _, tt := range industryTests {
		t.Run(tt.name, func(t *testing.T) {
			testData := *baseData // Copy
			testData.Ticker = tt.ticker

			result, err := dataCleaner.CleanFinancialData(ctx, &testData)
			require.NoError(t, err)
			require.NotNil(t, result)

			// Find contingent liability adjustment
			var totalAdjustment float64
			for _, adj := range result.Adjustments {
				if adj.Category == entities.LiabilityCompleteness && adj.RuleID == "contingent_liabilities" {
					totalAdjustment += adj.Amount
				}
			}

			// Calculate applied probability percentage
			if totalAdjustment > 0 {
				appliedProbability := (totalAdjustment / 30000) * 100 // 30k is the contingent liability amount

				assert.GreaterOrEqual(t, appliedProbability, tt.expectedProbabilityRange[0],
					"Applied probability %.1f%% should be >= %.1f%% for %s",
					appliedProbability, tt.expectedProbabilityRange[0], tt.name)

				assert.LessOrEqual(t, appliedProbability, tt.expectedProbabilityRange[1],
					"Applied probability %.1f%% should be <= %.1f%% for %s",
					appliedProbability, tt.expectedProbabilityRange[1], tt.name)

				t.Logf("Industry %s: Applied %.1f%% probability (%.0f adjustment on 30k contingent liability)",
					tt.ticker, appliedProbability, totalAdjustment)
			} else {
				t.Errorf("No adjustment applied for %s - this may indicate threshold issues", tt.name)
			}
		})
	}
}
