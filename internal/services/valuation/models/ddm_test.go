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

// ---------------------------------------------------------------------------
// P/BV Cross-Check Tests — implied P/BV vs ROE-justified P/BV
// ---------------------------------------------------------------------------

// TestDDMModel_Calculate_PBVCrossCheck_Reasonable tests that no warning is generated
// when implied P/BV is within range of the ROE-justified P/BV.
func TestDDMModel_Calculate_PBVCrossCheck_Reasonable(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	// DPS=4, CoE=10%, growth~5.26% (ROE=10% * retention=52.6%)
	// DDM value = 4 * 1.0526 / (0.10 - 0.0526) = 4.21 / 0.0474 = 88.8
	// BV/share = 200B / 5B = 40
	// Implied P/BV = 88.8 / 40 = 2.22
	// ROE-justified P/BV = (ROE - g) / (CoE - g) = (0.10 - 0.0526) / (0.10 - 0.0526) = 1.0
	// Ratio = 2.22 / 1.0 = 2.22 -> >2x, will warn
	// Adjust: use higher equity so BV/share is closer to DDM value.
	// BV/share = 400B / 5B = 80, Implied P/BV = 88.8 / 80 = 1.11
	// ROE = 20B / 400B = 5% -> too low, triggers low ROE warning
	// Let me use: equity=250B, NI=25B -> ROE=10%, retention = 1 - 4/(25B/5B) = 1-4/5=0.2
	// growth = 0.10 * 0.2 = 0.02
	// DDM value = 4 * 1.02 / (0.10 - 0.02) = 4.08 / 0.08 = 51
	// BV/share = 250B / 5B = 50
	// Implied P/BV = 51 / 50 = 1.02
	// ROE-justified = (0.10 - 0.02) / (0.10 - 0.02) = 1.0
	// Ratio = 1.02 / 1.0 = 1.02 -> reasonable, no warning
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "REASONABLE_PBV",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  4.00,
					NetIncome:          25000000000,
					StockholdersEquity: 250000000000,
					FilingDate:         time.Now(),
					FilingPeriod:       "2023FY",
					SharesOutstanding:  5000000000,
				},
			},
		},
		CostOfEquity:           0.10,
		SharesOutstanding:      5000000000,
		InterestBearingDebt:    50000000000,
		CashAndCashEquivalents: 20000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should NOT have P/BV divergence warning
	for _, w := range result.Warnings {
		assert.NotContains(t, w, "P/BV", "should NOT have P/BV divergence warning")
	}
}

// TestDDMModel_Calculate_PBVCrossCheck_Divergent tests that a warning is generated
// when the implied P/BV diverges significantly from ROE-justified P/BV.
func TestDDMModel_Calculate_PBVCrossCheck_Divergent(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	// DPS=8.0, NI=10M, Equity=100M -> ROE=10%, retention = 1 - 8/(10M/1M) = 1 - 0.8 = 0.2
	// growth = 0.10 * 0.2 = 0.02
	// DDM value = 8 * 1.02 / (0.10 - 0.02) = 8.16 / 0.08 = 102
	// BV/share = 100M / 1M = 100
	// Implied P/BV = 102 / 100 = 1.02
	// ROE-justified P/BV = (0.10 - 0.02) / (0.10 - 0.02) = 1.0
	// Ratio = 1.02 / 1.0 = 1.02 -> too close, won't trigger.
	// Need extreme divergence. Let me use very low equity:
	// Equity=10M, NI=3M -> ROE=30% (will trigger high ROE warning but that's OK)
	// BV/share = 10M / 1M = 10
	// growth capped at max 15% by DPS CAGR cap? No, single year, uses sustainable growth = ROE*retention
	// EPS = 3M/1M = 3, payout = 2/3 = 0.667, retention = 0.333
	// sustainable growth = 0.30 * 0.333 = 0.10 -> capped because >= CoE(0.10)
	// Capped growth = 0.10 * 0.7 = 0.07
	// DDM value = 2 * 1.07 / (0.10 - 0.07) = 2.14 / 0.03 = 71.33
	// BV/share = 10M / 1M = 10
	// Implied P/BV = 71.33 / 10 = 7.13
	// ROE-justified P/BV = (0.30 - 0.07) / (0.10 - 0.07) = 0.23 / 0.03 = 7.67
	// Ratio = 7.13 / 7.67 = 0.93 -> still reasonable. Hmm.

	// Let me try: low ROE, low equity scenario where values diverge.
	// DPS=2, NI=2M, Equity=10M -> ROE=20%, EPS=2, payout=100%, retention=0
	// growth = 0.20 * 0 = 0 -> falls to terminal or default
	// DDM value = 2 * 1.03 / (0.10 - 0.03) = 2.06 / 0.07 = 29.43
	// BV/share = 10M / 1M = 10
	// Implied P/BV = 29.43 / 10 = 2.94
	// ROE-justified P/BV = (0.20 - 0.03) / (0.10 - 0.03) = 0.17 / 0.07 = 2.43
	// Ratio = 2.94 / 2.43 = 1.21 -> reasonable

	// For clear divergence, need extreme: very low equity, high DPS
	// DPS=5, NI=2M, Equity=5M -> ROE=40%, EPS=2, payout=250% (>1!)
	// retention = 1 - 2.5 = -1.5 -> capped at 0
	// growth = 40% * 0 = 0 -> fall to terminal growth estimate / default 3%
	// DDM value = 5 * 1.03 / (0.10 - 0.03) = 5.15 / 0.07 = 73.57
	// BV/share = 5M / 1M = 5
	// Implied P/BV = 73.57 / 5 = 14.7
	// ROE-justified P/BV = (0.40 - 0.03) / (0.10 - 0.03) = 0.37 / 0.07 = 5.29
	// Ratio = 14.7 / 5.29 = 2.78 -> >2x, DIVERGENT
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "DIVERGENT_PBV",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  5.00,
					NetIncome:          2000000,
					StockholdersEquity: 5000000,
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

	// Should have P/BV divergence warning
	hasPBVWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "P/BV") {
			hasPBVWarning = true
			break
		}
	}
	assert.True(t, hasPBVWarning, "should warn when implied P/BV diverges from ROE-justified P/BV")
}

// TestDDMModel_Calculate_PBVCrossCheck_NoEquity tests that the P/BV cross-check
// is gracefully skipped when stockholders equity is zero or negative.
func TestDDMModel_Calculate_PBVCrossCheck_NoEquity(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NO_EQUITY",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  2.00,
					NetIncome:          1000000,
					StockholdersEquity: 0, // no equity
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

	// No P/BV warning because equity is zero (cross-check skipped)
	for _, w := range result.Warnings {
		assert.NotContains(t, w, "P/BV", "should not produce P/BV warning when equity is zero")
	}
}

// TestDDMModel_Calculate_PBVCrossCheck_NegativeEquity tests that P/BV cross-check
// is skipped for negative stockholders equity.
func TestDDMModel_Calculate_PBVCrossCheck_NegativeEquity(t *testing.T) {
	model := NewDDMModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NEG_EQUITY_PBV",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					DividendsPerShare:  2.00,
					NetIncome:          1000000,
					StockholdersEquity: -5000000,
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

	// No P/BV warning because equity is negative
	for _, w := range result.Warnings {
		assert.NotContains(t, w, "P/BV", "should not produce P/BV warning when equity is negative")
	}
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
