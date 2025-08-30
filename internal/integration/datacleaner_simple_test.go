package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/adjustments"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// TestLiabilityAdjuster_DirectCall_ContingentLiabilities tests calling the contingent liability method directly
func TestLiabilityAdjuster_DirectCall_ContingentLiabilities(t *testing.T) {
	// Create mock AI service for test
	mockAI := ai.NewMockAIService(&ai.AIServiceConfig{})
	adjuster := adjustments.NewLiabilityAdjuster(mockAI, nil)

	data := &entities.FinancialData{
		Ticker:                   "TEST",
		LitigationLiabilities:    25000, // 2.5% of revenue
		Revenue:                  1000000,
		TotalAssets:              2000000,
		TotalDebt:                300000,
		SharesOutstanding:        1000000,
		DilutedSharesOutstanding: 1100000,
		FilingDate:               time.Now(),
	}

	rule := &entities.CleaningRule{
		ID:       "contingent_liabilities",
		Category: entities.LiabilityCompleteness,
		Enabled:  true,
	}

	context := &entities.CleaningContext{
		IndustryCode: "45", // Technology
	}

	result := adjuster.ProcessContingentLiabilityAdjustment(data, rule, context)
	require.NotNil(t, result, "ProcessContingentLiabilityAdjustment should return a result")

	t.Logf("Direct call result: Applied=%v, Amount=%.2f, Adjustments=%d, Flags=%d",
		result.Applied, result.Amount, len(result.Adjustments), len(result.Flags))

	if result.Applied {
		assert.Greater(t, result.Amount, 0.0, "Should have positive adjustment amount")
		assert.NotEmpty(t, result.Adjustments, "Should have adjustments")
		t.Logf("Adjustment reasoning: %s", result.Reasoning)
		for i, adj := range result.Adjustments {
			t.Logf("Adjustment %d: %+v", i, adj)
		}
	}

	for i, flag := range result.Flags {
		t.Logf("Flag %d: %+v", i, flag)
	}
}
