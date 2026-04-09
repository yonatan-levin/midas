package models

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestDDMModel_Calculate_PositiveDPS tests standard DDM with positive dividends
func TestDDMModel_Calculate_PositiveDPS(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "JPM",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  4.00,
					NetIncome:          50000000000,
					StockholdersEquity: 300000000000,
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
					SharesOutstanding:  3000000000,
				},
			},
		},
		CostOfEquity:           0.10, // 10% cost of equity
		SharesOutstanding:      3000000000,
		InterestBearingDebt:    100000000000,
		CashAndCashEquivalents: 50000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "ddm", result.ModelType)
	assert.Greater(t, result.IntrinsicValuePerShare, 0.0, "intrinsic value should be positive")
	assert.Greater(t, result.EquityValue, 0.0, "equity value should be positive")
	assert.NotEmpty(t, result.Confidence)
}

// TestDDMModel_Calculate_ZeroDPS tests DDM with zero dividends
func TestDDMModel_Calculate_ZeroDPS(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "GROWTH",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare: 0,
					NetIncome:         1000000,
					FilingDate:        time.Now(),
					FilingPeriod:      "2023FY",
				},
			},
		},
		CostOfEquity:      0.10,
		SharesOutstanding: 1000000,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err, "should fail for zero DPS")
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "does not pay dividends")
}

// TestDDMModel_Calculate_NegativeDPS tests DDM with negative dividends
func TestDDMModel_Calculate_NegativeDPS(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NEG",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare: -1.0,
					FilingDate:        time.Now(),
					FilingPeriod:      "2023FY",
				},
			},
		},
		CostOfEquity:      0.10,
		SharesOutstanding: 1000000,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestDDMModel_Calculate_NegativeEquity tests DDM with negative stockholders equity
func TestDDMModel_Calculate_NegativeEquity(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NEG_EQ",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  2.50,
					NetIncome:          1000000,
					StockholdersEquity: -500000, // negative equity
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
					SharesOutstanding:  1000000,
				},
			},
		},
		CostOfEquity:      0.10,
		SharesOutstanding: 1000000,
	}

	// DDM should still work (uses growth estimate fallback, not ROE)
	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
}

// TestDDMModel_Calculate_HighPayoutRatio tests DDM warning for high payout ratio
func TestDDMModel_Calculate_HighPayoutRatio(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "HIGH_PAYOUT",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  9.50,
					NetIncome:          10000000, // EPS = 10
					StockholdersEquity: 50000000,
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
					SharesOutstanding:  1000000,
				},
			},
		},
		CostOfEquity:      0.10,
		SharesOutstanding: 1000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have warning about high payout ratio
	hasPayoutWarning := false
	for _, w := range result.Warnings {
		if assert.ObjectsAreEqual("High payout ratio", w) || len(w) > 0 {
			// Check if any warning mentions payout
			if contains(w, "payout") {
				hasPayoutWarning = true
			}
		}
	}
	assert.True(t, hasPayoutWarning, "should warn about high payout ratio")
}

// TestDDMModel_Calculate_NilInput tests DDM with nil input
func TestDDMModel_Calculate_NilInput(t *testing.T) {
	model := NewDDMModel(testLogger())
	result, err := model.Calculate(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestDDMModel_Calculate_ZeroCostOfEquity tests DDM with zero cost of equity
func TestDDMModel_Calculate_ZeroCostOfEquity(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "ZERO_COE",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare: 2.00,
					FilingDate:        time.Now(),
					FilingPeriod:      "2023FY",
				},
			},
		},
		CostOfEquity:      0,
		SharesOutstanding: 1000000,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cost of equity must be positive")
}

// TestDDMModel_SupportsIndustry tests industry support check
func TestDDMModel_SupportsIndustry(t *testing.T) {
	model := NewDDMModel(testLogger())

	assert.True(t, model.SupportsIndustry("FIN"))
	assert.True(t, model.SupportsIndustry("FIN_IB"))
	assert.True(t, model.SupportsIndustry("fin"))
	assert.False(t, model.SupportsIndustry("TECH"))
	assert.False(t, model.SupportsIndustry("REIT"))
}

// TestDDMModel_ModelType tests model type identifier
func TestDDMModel_ModelType(t *testing.T) {
	model := NewDDMModel(testLogger())
	assert.Equal(t, "ddm", model.ModelType())
}

// contains checks if a string contains a substring (case-insensitive for test readability)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
