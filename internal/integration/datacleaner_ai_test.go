package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// TestDataCleaner_B3_ContingentLiabilities_AIEnabled tests AI-powered contingent liability analysis
// This test follows TDD: written first to drive the AI integration implementation
func TestDataCleaner_B3_ContingentLiabilities_AIEnabled(t *testing.T) {
	// Setup test environment with AI enabled
	testEnv := SetupTestEnvironment(t)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	// CRITICAL: Enable AI integration for this test
	testEnv.Config.DataCleaner.EnableAIIntegration = true
	testEnv.Config.DataCleaner.AIServiceURL = "http://mock-ai-service"
	testEnv.Config.DataCleaner.AIServiceTimeout = 5 * time.Second

	// Create mock AI service for test
	mockAI := ai.NewMockAIService(&ai.AIServiceConfig{})

	// Create DataCleaner service with AI enabled
	dataCleaner, err := datacleaner.NewDataCleanerService(testEnv.Config, mockAI, nil)
	require.NoError(t, err, "Failed to create DataCleaner service with AI enabled")

	ctx := context.Background()

	// Test case: AI returns higher confidence probability than conservative default
	t.Run("AI_Returns_Higher_Probability_Than_Conservative", func(t *testing.T) {
		// NOTE: Mock AI service returns 60% probability by default (vs 40% conservative)
		expectedAIProbability := 0.60                         // Updated to match mock AI service
		expectedAIAdjustment := 25000 * expectedAIProbability // 25k * 60% = 15,000

		data := &entities.FinancialData{
			Ticker:                   "AI_TEST",
			LitigationLiabilities:    25000, // 2.5% of revenue
			Revenue:                  1000000,
			TotalAssets:              2000000,
			TotalDebt:                300000,
			SharesOutstanding:        1000000,
			DilutedSharesOutstanding: 1100000,
			FilingDate:               time.Now(),
		}

		// For testing: add footnote text to the data struct temporarily
		// TODO: Replace with proper CleaningContext when pipeline supports it

		// Execute data cleaning - AI should be triggered via config
		result, err := dataCleaner.CleanFinancialData(ctx, data)
		require.NoError(t, err, "CleanFinancialData with AI should not error")
		require.NotNil(t, result, "CleaningResult should not be nil")

		// Debug: Log what we got
		t.Logf("AI-enabled result: Total adjustments: %d", len(result.Adjustments))
		for i, adj := range result.Adjustments {
			t.Logf("Adjustment %d: RuleID=%s, Amount=%.2f, Reasoning=%s", i, adj.RuleID, adj.Amount, adj.Reasoning)
		}

		// Filter for contingent liability adjustments
		var aiAdjustments []entities.Adjustment
		for _, adj := range result.Adjustments {
			if adj.RuleID == "contingent_liabilities" {
				aiAdjustments = append(aiAdjustments, adj)
			}
		}

		// Assert AI-driven adjustment
		require.NotEmpty(t, aiAdjustments, "AI should generate contingent liability adjustments")

		totalAIAdjustment := 0.0
		for _, adj := range aiAdjustments {
			totalAIAdjustment += adj.Amount
		}

		// AI should provide more accurate estimate than conservative default (40%)
		assert.InDelta(t, expectedAIAdjustment, totalAIAdjustment, 100.0,
			"AI adjustment should be ~%.0f (65%% of 25k), got %.0f", expectedAIAdjustment, totalAIAdjustment)

		// Verify AI metadata is captured
		assert.Contains(t, result.AIMetadata, "ai_confidence", "Should capture AI confidence score")
		assert.Contains(t, result.AIMetadata, "ai_model_used", "Should capture AI model information")

		// Verify reasoning mentions AI analysis
		foundAIReasoning := false
		for _, adj := range aiAdjustments {
			if assert.Contains(t, adj.Reasoning, "AI analysis") || assert.Contains(t, adj.Reasoning, "footnote") {
				foundAIReasoning = true
				break
			}
		}
		assert.True(t, foundAIReasoning, "Adjustment reasoning should reference AI analysis")
	})

	// Test case: AI fails, falls back to conservative probability
	t.Run("AI_Fails_Fallback_To_Conservative", func(t *testing.T) {
		// Create a failing AI service for this test
		failingAI := &FailingAIService{}

		// Create DataCleaner service with failing AI
		failingDataCleaner, err := datacleaner.NewDataCleanerService(testEnv.Config, failingAI, nil)
		require.NoError(t, err, "Failed to create DataCleaner service with failing AI")

		data := &entities.FinancialData{
			Ticker:                   "FAIL_TEST",
			LitigationLiabilities:    25000, // 2.5% of revenue
			Revenue:                  1000000,
			TotalAssets:              2000000,
			TotalDebt:                300000,
			SharesOutstanding:        1000000,
			DilutedSharesOutstanding: 1100000,
			FilingDate:               time.Now(),
		}

		// Execute data cleaning - AI should fail and fallback to conservative
		result, err := failingDataCleaner.CleanFinancialData(ctx, data)
		require.NoError(t, err, "CleanFinancialData should not error even with failing AI")
		require.NotNil(t, result, "CleaningResult should not be nil")

		// Debug: Log what we got
		t.Logf("Failing AI result: Total adjustments: %d", len(result.Adjustments))
		for i, adj := range result.Adjustments {
			t.Logf("Adjustment %d: RuleID=%s, Amount=%.2f, Reasoning=%s", i, adj.RuleID, adj.Amount, adj.Reasoning)
		}

		// Filter for contingent liability adjustments
		var fallbackAdjustments []entities.Adjustment
		for _, adj := range result.Adjustments {
			if adj.RuleID == "contingent_liabilities" {
				fallbackAdjustments = append(fallbackAdjustments, adj)
			}
		}

		// Non-vacuous: the B3 rule MUST fire on this input (litigation
		// liabilities disclosed) so the fallback assertions below cannot pass
		// silently.
		require.NotEmpty(t, fallbackAdjustments, "B3 must fire on disclosed contingent liabilities so the fallback is exercised")

		// Deterministic: getIndustryCode maps ticker "FAIL_TEST" → GICS "45"
		// (Tech), whose contingent-liability heuristic rate is 0.40. The
		// expected amount is therefore 25k * 0.40 = 10,000 — the heuristic
		// fallback (TDB-3) coincides with the old flat-0.40 number here.
		require.Equal(t, "45", result.IndustryCode, "FAIL_TEST must classify to GICS 45 (Tech) for a deterministic 0.40 heuristic rate")

		totalFallbackAdjustment := 0.0
		for _, adj := range fallbackAdjustments {
			totalFallbackAdjustment += adj.Amount
		}

		// Should fall back to the industry heuristic (Tech 0.40 → 10,000).
		assert.InDelta(t, 10000.0, totalFallbackAdjustment, 1.0,
			"With AI failure, should fall back to the Tech industry heuristic (0.40 → 10k), got %.0f", totalFallbackAdjustment)

		// Reasoning should name the failure AND the industry-heuristic fallback.
		foundFailureReasoning := false
		foundHeuristicReasoning := false
		for _, adj := range fallbackAdjustments {
			if assert.Contains(t, adj.Reasoning, "AI analysis failed") {
				foundFailureReasoning = true
			}
			if assert.Contains(t, adj.Reasoning, "industry heuristic fallback") {
				foundHeuristicReasoning = true
			}
		}
		assert.True(t, foundFailureReasoning, "Adjustment reasoning should reference the AI failure")
		assert.True(t, foundHeuristicReasoning, "Adjustment reasoning should reference the industry-heuristic fallback")

		// Should not have AI metadata on failure
		assert.Empty(t, result.AIMetadata, "Should not capture AI metadata when AI fails")
	})
}

// TestDataCleaner_B3_ContingentLiabilities_AIDisabled ensures no behavior change when AI is disabled
func TestDataCleaner_B3_ContingentLiabilities_AIDisabled(t *testing.T) {
	// Setup test environment with AI disabled (default)
	testEnv := SetupTestEnvironment(t)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	// Verify AI is disabled
	require.False(t, testEnv.Config.DataCleaner.EnableAIIntegration, "AI must be disabled for this test")

	// Create mock AI service (but AI is disabled)
	mockAI := ai.NewMockAIService(&ai.AIServiceConfig{})

	dataCleaner, err := datacleaner.NewDataCleanerService(testEnv.Config, mockAI, nil)
	require.NoError(t, err)

	ctx := context.Background()

	data := &entities.FinancialData{
		Ticker:                   "NO_AI_TEST",
		LitigationLiabilities:    25000, // 2.5% of revenue
		Revenue:                  1000000,
		TotalAssets:              2000000,
		TotalDebt:                300000,
		SharesOutstanding:        1000000,
		DilutedSharesOutstanding: 1100000,
		FilingDate:               time.Now(),
	}

	// Execute data cleaning with AI disabled (should use conservative approach)
	result, err := dataCleaner.CleanFinancialData(ctx, data)
	require.NoError(t, err)

	// Should behave exactly like baseline (conservative 40% probability)
	var conservativeAdjustments []entities.Adjustment
	for _, adj := range result.Adjustments {
		if adj.RuleID == "contingent_liabilities" {
			conservativeAdjustments = append(conservativeAdjustments, adj)
		}
	}

	if len(conservativeAdjustments) > 0 {
		totalConservativeAdjustment := 0.0
		for _, adj := range conservativeAdjustments {
			totalConservativeAdjustment += adj.Amount
		}

		// Should be exactly 40% probability (10,000)
		assert.InDelta(t, 10000.0, totalConservativeAdjustment, 1.0,
			"With AI disabled, should use conservative 40%% probability (10k), got %.0f", totalConservativeAdjustment)

		// Reasoning should NOT mention AI
		for _, adj := range conservativeAdjustments {
			assert.NotContains(t, adj.Reasoning, "AI", "Reasoning should not reference AI when disabled")
			assert.NotContains(t, adj.Reasoning, "footnote", "Reasoning should not reference footnotes when AI disabled")
		}
	}

	// Should not capture AI metadata when disabled
	assert.Empty(t, result.AIMetadata, "Should not capture AI metadata when AI disabled")
}

// FailingAIService is a mock AI service that always fails for testing error scenarios
type FailingAIService struct{}

func (f *FailingAIService) AnalyzeFootnote(ctx context.Context, request *ai.FootnoteAnalysisRequest) (*ai.FootnoteAnalysisResponse, error) {
	// Simulate different types of failures for comprehensive testing
	switch request.Ticker {
	case "TIMEOUT_TEST":
		// Simulate timeout
		return nil, context.DeadlineExceeded
	case "NETWORK_TEST":
		// Simulate network error
		return nil, errors.New("network timeout: connection refused")
	default:
		// Generic AI service failure
		return nil, errors.New("AI service temporarily unavailable")
	}
}

func (f *FailingAIService) BatchAnalyzeFootnotes(ctx context.Context, requests []*ai.FootnoteAnalysisRequest) ([]*ai.FootnoteAnalysisResponse, error) {
	return nil, errors.New("batch analysis not supported in failing service")
}

func (f *FailingAIService) GetAnalysisCapabilities() []ai.FootnoteAnalysisType {
	return []ai.FootnoteAnalysisType{} // No capabilities when failing
}

func (f *FailingAIService) HealthCheck(ctx context.Context) error {
	return errors.New("AI service health check failed")
}

// TestDataCleaner_B3_ContingentLiabilities_AIFailureScenarios tests various AI failure modes
func TestDataCleaner_B3_ContingentLiabilities_AIFailureScenarios(t *testing.T) {
	// Setup test environment with AI enabled
	testEnv := SetupTestEnvironment(t)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	// Enable AI integration for failure testing
	testEnv.Config.DataCleaner.EnableAIIntegration = true
	testEnv.Config.DataCleaner.AIServiceURL = "http://mock-ai-service"
	testEnv.Config.DataCleaner.AIServiceTimeout = 5 * time.Second

	ctx := context.Background()

	failureScenarios := []struct {
		name           string
		ticker         string
		expectedReason string
	}{
		{
			name:           "Generic AI Service Failure",
			ticker:         "FAIL_TEST",
			expectedReason: "AI analysis failed",
		},
		{
			name:           "Network Timeout Failure",
			ticker:         "NETWORK_TEST",
			expectedReason: "AI analysis failed",
		},
		{
			name:           "Context Timeout Failure",
			ticker:         "TIMEOUT_TEST",
			expectedReason: "AI analysis failed",
		},
	}

	for _, scenario := range failureScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// Create failing AI service
			failingAI := &FailingAIService{}

			// Create DataCleaner service with failing AI
			failingDataCleaner, err := datacleaner.NewDataCleanerService(testEnv.Config, failingAI, nil)
			require.NoError(t, err, "Failed to create DataCleaner service with failing AI")

			data := &entities.FinancialData{
				Ticker:                   scenario.ticker,
				LitigationLiabilities:    30000, // 3% of revenue
				Revenue:                  1000000,
				TotalAssets:              2000000,
				TotalDebt:                300000,
				SharesOutstanding:        1000000,
				DilutedSharesOutstanding: 1100000,
				FilingDate:               time.Now(),
			}

			// Execute data cleaning - should not panic or error
			result, err := failingDataCleaner.CleanFinancialData(ctx, data)
			require.NoError(t, err, "CleanFinancialData should not error even with AI failures")
			require.NotNil(t, result, "CleaningResult should not be nil")

			// Debug: Log what we got
			t.Logf("%s result: Total adjustments: %d", scenario.name, len(result.Adjustments))

			// Filter for contingent liability adjustments
			var fallbackAdjustments []entities.Adjustment
			for _, adj := range result.Adjustments {
				if adj.RuleID == "contingent_liabilities" {
					fallbackAdjustments = append(fallbackAdjustments, adj)
				}
			}

			// Non-vacuous: B3 MUST fire on the disclosed litigation liability so
			// the fallback assertions below cannot pass silently.
			require.NotEmpty(t, fallbackAdjustments, "B3 must fire on disclosed contingent liabilities so the fallback is exercised")

			totalFallbackAdjustment := 0.0
			for _, adj := range fallbackAdjustments {
				totalFallbackAdjustment += adj.Amount
			}

			// Deterministic: the AI-failed fallback now uses the industry
			// heuristic (TDB-3), so the expected amount is the disclosed total
			// (30k) times the heuristic rate for the CLASSIFIED industry code.
			// Computing the rate from result.IndustryCode (rather than pinning a
			// literal) keeps the assertion correct regardless of how the
			// classifier maps each synthetic ticker.
			const disclosedTotal = 30000.0
			expectedRate := contingentHeuristicRate(result.IndustryCode)
			expectedFallback := disclosedTotal * expectedRate
			assert.InDelta(t, expectedFallback, totalFallbackAdjustment, 100.0,
				"AI failure should fall back to the industry heuristic (code %q rate %.2f → %.0f), got %.0f",
				result.IndustryCode, expectedRate, expectedFallback, totalFallbackAdjustment)

			// Reasoning should name the AI failure AND the industry-heuristic fallback.
			foundFailureReasoning := false
			foundHeuristicReasoning := false
			for _, adj := range fallbackAdjustments {
				if assert.Contains(t, adj.Reasoning, scenario.expectedReason) {
					foundFailureReasoning = true
				}
				if assert.Contains(t, adj.Reasoning, "industry heuristic fallback") {
					foundHeuristicReasoning = true
				}
			}
			assert.True(t, foundFailureReasoning, "Adjustment reasoning should reference the AI failure")
			assert.True(t, foundHeuristicReasoning, "Adjustment reasoning should reference the industry-heuristic fallback")

			// Service should remain stable - no panics, no crashes
			assert.True(t, result.Success, "Data cleaning should succeed despite AI failure")
		})
	}
}

// contingentHeuristicRate mirrors
// LiabilityAdjuster.getContingentLiabilityProbability's GICS-sector switch
// so the integration tests can compute the expected fallback amount from the
// CLASSIFIED industry code without depending on a non-deterministic literal.
//
// It must stay in lock-step with the production switch (45→0.40, 20→0.70,
// 25→0.65, 21→0.60, 62→0.50, default→0.30). The classifier's per-sector
// ContingentLiabilityRate config values for the loaded sectors (45/20/25)
// equal these switch values, so this single mirror is correct whether the
// production helper resolves the rate via the classifier config or the switch.
func contingentHeuristicRate(industryCode string) float64 {
	switch industryCode {
	case "45": // Information Technology
		return 0.40
	case "20": // Industrials/Manufacturing
		return 0.70
	case "25": // Consumer Discretionary
		return 0.65
	case "21": // Energy
		return 0.60
	case "62": // Healthcare
		return 0.50
	default:
		return 0.30
	}
}
