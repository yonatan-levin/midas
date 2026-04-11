package models

import (
	"context"
	"strings"
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

// TestDDMModel_Calculate_GrowthExceedsCostOfEquity tests the guard that caps
// dividend growth when it exceeds the cost of equity.
func TestDDMModel_Calculate_GrowthExceedsCostOfEquity(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	// Create multi-year data with rapidly growing DPS to trigger CAGR > CoE
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "RAPID_DIV",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  10.00, // High DPS
					NetIncome:          50000000,
					StockholdersEquity: 300000000,
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
					SharesOutstanding:  1000000,
				},
				"2022FY": {
					DividendsPerShare: 6.00,
					NetIncome:         40000000,
					FilingDate:        time.Now().Add(-365 * 24 * time.Hour),
					FilingPeriod:      "2022FY",
					SharesOutstanding: 1000000,
				},
				"2021FY": {
					DividendsPerShare: 2.00, // Very low starting DPS -> high CAGR
					NetIncome:         30000000,
					FilingDate:        time.Now().Add(-2 * 365 * 24 * time.Hour),
					FilingPeriod:      "2021FY",
					SharesOutstanding: 1000000,
				},
			},
		},
		CostOfEquity:           0.08, // 8% — the high DPS CAGR will exceed this
		SharesOutstanding:      1000000,
		InterestBearingDebt:    0,
		CashAndCashEquivalents: 0,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, result.IntrinsicValuePerShare, 0.0,
		"should still produce a valid value after capping growth")
}

// TestDDMModel_Calculate_HistoricalDPSCAGR tests the historical DPS CAGR path
// in estimateDividendGrowth with valid multi-year data.
func TestDDMModel_Calculate_HistoricalDPSCAGR(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	// Multi-year data with moderate dividend growth (should use CAGR path)
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "STEADY_DIV",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  4.00,
					NetIncome:          50000000000,
					StockholdersEquity: 300000000000,
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
					SharesOutstanding:  3000000000,
				},
				"2022FY": {
					DividendsPerShare:  3.80,
					NetIncome:          48000000000,
					StockholdersEquity: 290000000000,
					FilingDate:         time.Now().Add(-365 * 24 * time.Hour),
					FilingPeriod:       "2022FY",
					SharesOutstanding:  3000000000,
				},
				"2021FY": {
					DividendsPerShare:  3.50,
					NetIncome:          45000000000,
					StockholdersEquity: 280000000000,
					FilingDate:         time.Now().Add(-2 * 365 * 24 * time.Hour),
					FilingPeriod:       "2021FY",
					SharesOutstanding:  3000000000,
				},
			},
		},
		CostOfEquity:           0.10,
		SharesOutstanding:      3000000000,
		InterestBearingDebt:    100000000000,
		CashAndCashEquivalents: 50000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "ddm", result.ModelType)
	assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
}

// TestDDMModel_Calculate_SustainableGrowthFallback tests the ROE * retention ratio
// fallback in estimateDividendGrowth when DPS history doesn't yield a valid CAGR.
func TestDDMModel_Calculate_SustainableGrowthFallback(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	// Only 1 year of data — no CAGR possible. Has positive equity and net income
	// to trigger the sustainable growth (ROE * retention) path.
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "SINGLE_YEAR",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  3.00,
					NetIncome:          20000000000,
					StockholdersEquity: 200000000000, // ROE = 10%
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
					SharesOutstanding:  5000000000, // EPS = 4.0, payout = 3/4 = 75%, retention = 25%
				},
			},
		},
		CostOfEquity:           0.10,
		SharesOutstanding:      5000000000,
		InterestBearingDebt:    50000000000,
		CashAndCashEquivalents: 30000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
}

// TestDDMModel_Calculate_TerminalRateFallback tests the terminal growth rate fallback
// in estimateDividendGrowth when neither CAGR nor sustainable growth is available.
func TestDDMModel_Calculate_TerminalRateFallback(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	// Single year data with no equity (prevents ROE calculation), with growth estimate
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "TERM_FALLBACK",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  2.00,
					NetIncome:          0,
					StockholdersEquity: 0,
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
				},
			},
		},
		GrowthEstimate: &entities.GrowthEstimate{
			TerminalGrowthRate: 0.025,
		},
		CostOfEquity:      0.10,
		SharesOutstanding: 1000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
}

// TestDDMModel_Calculate_DefaultGrowthFallback tests the 3% default fallback when
// no growth estimate is available and CAGR/sustainable growth paths fail.
func TestDDMModel_Calculate_DefaultGrowthFallback(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	// Single year data with no equity, no growth estimate -> falls to 3% default
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "DEFAULT_GROWTH",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  2.00,
					NetIncome:          0,
					StockholdersEquity: 0,
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
				},
			},
		},
		GrowthEstimate:    nil, // No growth estimate
		CostOfEquity:      0.10,
		SharesOutstanding: 1000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
}

// TestDDMModel_Calculate_LowROEWarning tests that low ROE generates a warning
func TestDDMModel_Calculate_LowROEWarning(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "LOW_ROE",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  1.00,
					NetIncome:          1000000,   // Very low net income
					StockholdersEquity: 100000000, // High equity -> ROE = 1%
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

	// Should have warning about low ROE
	hasROEWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "Low ROE") {
			hasROEWarning = true
			break
		}
	}
	assert.True(t, hasROEWarning, "should warn about low ROE")
}

// TestDDMModel_Calculate_HighROEWarning tests that high ROE generates a warning
func TestDDMModel_Calculate_HighROEWarning(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "HIGH_ROE",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  2.00,
					NetIncome:          30000000,  // Very high net income
					StockholdersEquity: 100000000, // Low equity -> ROE = 30%
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

	// Should have warning about high ROE
	hasROEWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "High ROE") {
			hasROEWarning = true
			break
		}
	}
	assert.True(t, hasROEWarning, "should warn about high ROE")
}

// TestDDMModel_Calculate_NoFinancialData tests DDM with empty historical data map
func TestDDMModel_Calculate_NoFinancialData(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "EMPTY",
			Data:   map[string]*entities.FinancialData{},
		},
		CostOfEquity:      0.10,
		SharesOutstanding: 1000000,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no financial data")
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
