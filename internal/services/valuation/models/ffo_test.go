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

// TestFFOModel_Calculate_StandardREIT tests standard FFO calculation for a REIT
func TestFFOModel_Calculate_StandardREIT(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "AMT",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   2000000000,
					DepreciationAndAmortization: 1500000000,
					GainOnPropertySales:         100000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding:      500000000,
		InterestBearingDebt:    30000000000,
		CashAndCashEquivalents: 2000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// FFO = 2B + 1.5B - 0.1B = 3.4B
	// FFO/share = 3.4B / 500M = 6.8
	// Value/share = 6.8 * 15 = 102.0
	assert.Equal(t, "ffo", result.ModelType)
	assert.InDelta(t, 102.0, result.IntrinsicValuePerShare, 0.01)
	assert.Greater(t, result.EnterpriseValue, 0.0)
	assert.Equal(t, "high", result.Confidence)
}

// TestFFOModel_Calculate_MissingGainsData tests FFO when gain on property sales is zero
func TestFFOModel_Calculate_MissingGainsData(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "PLD",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   1000000000,
					DepreciationAndAmortization: 800000000,
					GainOnPropertySales:         0, // no property sale gains
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding:      200000000,
		InterestBearingDebt:    10000000000,
		CashAndCashEquivalents: 500000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// FFO = 1B + 0.8B - 0 = 1.8B
	// FFO/share = 1.8B / 200M = 9.0
	// Value/share = 9.0 * 15 = 135.0
	assert.InDelta(t, 135.0, result.IntrinsicValuePerShare, 0.01)
	assert.Equal(t, "high", result.Confidence)
}

// TestFFOModel_Calculate_NegativeFFO tests FFO with negative result
func TestFFOModel_Calculate_NegativeFFO(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "DISTRESSED",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   -500000000, // net loss
					DepreciationAndAmortization: 100000000,
					GainOnPropertySales:         200000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding:      100000000,
		InterestBearingDebt:    5000000000,
		CashAndCashEquivalents: 200000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// FFO = -500M + 100M - 200M = -600M -> value should be 0
	assert.Equal(t, 0.0, result.IntrinsicValuePerShare, "negative FFO should result in zero value")
	assert.Equal(t, "low", result.Confidence)
}

// TestFFOModel_Calculate_NoData tests FFO with no financial data
func TestFFOModel_Calculate_NoData(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "EMPTY",
			Data:   map[string]*entities.FinancialData{},
		},
		SharesOutstanding: 100000000,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no financial data")
}

// TestFFOModel_Calculate_ZeroShares tests FFO with zero shares outstanding
func TestFFOModel_Calculate_ZeroShares(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NO_SHARES",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   1000000,
					DepreciationAndAmortization: 500000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding: 0,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "shares outstanding")
}

// TestFFOModel_Calculate_NilInput tests FFO with nil input
func TestFFOModel_Calculate_NilInput(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	result, err := model.Calculate(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestFFOModel_Calculate_MissingNetIncomeAndDA tests FFO with no net income or D&A
func TestFFOModel_Calculate_MissingNetIncomeAndDA(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NO_DATA",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   0,
					DepreciationAndAmortization: 0,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding: 100000000,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "insufficient data")
}

// TestFFOModel_ModelType tests model type identifier
func TestFFOModel_ModelType(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	assert.Equal(t, "ffo", model.ModelType())
}

// TestFFOModel_CustomMultiple tests FFO with a custom P/FFO multiple
func TestFFOModel_CustomMultiple(t *testing.T) {
	model := NewFFOModelWithMultiple(20.0, testLogger()) // 20x instead of 15x
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "PREMIUM",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   100000000,
					DepreciationAndAmortization: 50000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding: 10000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)

	// FFO = 100M + 50M = 150M, FFO/share = 15.0, Value = 15 * 20 = 300
	assert.InDelta(t, 300.0, result.IntrinsicValuePerShare, 0.01)
}

// TestFFOModel_Calculate_NegativeFFOWithDA tests FFO with negative net income but positive D&A
// producing a negative FFO (net income + D&A < gains). Verifies value is capped at zero.
func TestFFOModel_Calculate_NegativeFFOWithDA(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NEG_FFO_DA",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   -200000000,
					DepreciationAndAmortization: 50000000,
					GainOnPropertySales:         0,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding:      100000000,
		InterestBearingDebt:    5000000000,
		CashAndCashEquivalents: 200000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// FFO = -200M + 50M - 0 = -150M -> value should be 0
	assert.Equal(t, 0.0, result.IntrinsicValuePerShare, "negative FFO should result in zero value")
	assert.Equal(t, "low", result.Confidence)
	// Should have warnings about negative FFO and zero value
	assert.GreaterOrEqual(t, len(result.Warnings), 2)
}

// TestFFOModel_Calculate_MissingDAWarning tests that missing D&A generates a data quality warning
func TestFFOModel_Calculate_MissingDAWarning(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NO_DA",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   500000000,
					DepreciationAndAmortization: 0, // missing D&A
					GainOnPropertySales:         0,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding:      100000000,
		InterestBearingDebt:    1000000000,
		CashAndCashEquivalents: 200000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have warning about missing D&A
	hasDAWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "D&A not available") {
			hasDAWarning = true
			break
		}
	}
	assert.True(t, hasDAWarning, "should warn about missing D&A")
	// Confidence should not be "high" since D&A is missing
	assert.NotEqual(t, "high", result.Confidence)
}

// ---------------------------------------------------------------------------
// NAV Cross-Check Tests — REIT NAV = NOI / Cap Rate
// ---------------------------------------------------------------------------

// TestFFOModel_Calculate_NAVCrossCheck_Reasonable tests that NAV cross-check does NOT
// produce a warning when P/FFO value and NAV are within 2x of each other.
func TestFFOModel_Calculate_NAVCrossCheck_Reasonable(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	// Inject a cap rate that yields a NAV close to the P/FFO value
	model.navCapRate = 0.06 // 6% cap rate
	ctx := context.Background()

	// OperatingIncome as NOI proxy = 600M
	// NAV = 600M / 0.06 = 10B, NAV/share = 10B / 500M = 20.0
	// FFO = 2B + 1.5B - 0.1B = 3.4B, FFO/share = 6.8, Value = 6.8 * 15 = 102
	// ratio = 102 / 20 = 5.1 -> >2x, so this will warn.
	// Let me adjust: use OI=3B. NAV/share = 3B/0.06/500M = 100.
	// That's close to 102. No warning.
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "AMT",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   2000000000,
					DepreciationAndAmortization: 1500000000,
					GainOnPropertySales:         100000000,
					OperatingIncome:             3060000000, // NOI proxy -> NAV/share ~102
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding:      500000000,
		InterestBearingDebt:    30000000000,
		CashAndCashEquivalents: 2000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// FFO value = 102. NAV = 3.06B / 0.06 / 500M = 102. No NAV divergence warning.
	for _, w := range result.Warnings {
		assert.NotContains(t, w, "NAV", "should NOT have NAV divergence warning when values are close")
	}
}

// TestFFOModel_Calculate_NAVCrossCheck_Divergent tests that a NAV warning is produced
// when P/FFO value diverges significantly from NAV per share (>2x or <0.5x).
func TestFFOModel_Calculate_NAVCrossCheck_Divergent(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	model.navCapRate = 0.06
	ctx := context.Background()

	// FFO = 100M + 50M = 150M, FFO/share = 15, Value = 15 * 15 = 225
	// OI = 50M, NAV = 50M / 0.06 / 10M = 83.33
	// Ratio = 225 / 83.33 = 2.7 -> >2x, should warn
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "DIVERGENT",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   100000000,
					DepreciationAndAmortization: 50000000,
					OperatingIncome:             50000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding: 10000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have NAV divergence warning
	hasNAVWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "NAV") {
			hasNAVWarning = true
			break
		}
	}
	assert.True(t, hasNAVWarning, "should warn when P/FFO value diverges significantly from NAV")
}

// TestFFOModel_Calculate_NAVCrossCheck_NoCapRate tests that NAV cross-check is
// gracefully skipped when no cap rate is configured.
func TestFFOModel_Calculate_NAVCrossCheck_NoCapRate(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	model.navCapRate = 0 // No cap rate -> skip NAV
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NO_CAPRATE",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   100000000,
					DepreciationAndAmortization: 50000000,
					OperatingIncome:             80000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding: 10000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// No NAV warning because cap rate is zero (skipped)
	for _, w := range result.Warnings {
		assert.NotContains(t, w, "NAV", "should not produce NAV warning when cap rate is 0")
	}
}

// TestFFOModel_Calculate_NAVCrossCheck_ZeroOI tests that NAV cross-check is
// gracefully skipped when operating income (NOI proxy) is zero or negative.
func TestFFOModel_Calculate_NAVCrossCheck_ZeroOI(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())
	model.navCapRate = 0.06
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "ZERO_OI",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   100000000,
					DepreciationAndAmortization: 50000000,
					OperatingIncome:             0, // no OI data
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		SharesOutstanding: 10000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// No NAV warning because OI (NOI proxy) is zero
	for _, w := range result.Warnings {
		assert.NotContains(t, w, "NAV", "should not produce NAV warning when OI is zero")
	}
}

// TestLoadFFOConfig_UsesEmbed verifies loadFFOConfig returns the values from
// the embedded industry_multiples.json. Replaces the legacy tmpfile + path
// tests (TestLoadPFFOMultiple_*, TestLoadREITCapRate_*, TestLoadFFOConfig_*)
// that exercised os.ReadFile error paths no longer possible with embed.
func TestLoadFFOConfig_UsesEmbed(t *testing.T) {
	pffo, capRate := loadFFOConfig()
	// Embedded default per config/industry_multiples.json.
	assert.InDelta(t, 15.0, pffo, 0.001)
	assert.InDelta(t, 0.06, capRate, 0.0001)
}

// TestNewFFOModelWithConfig verifies the explicit-config constructor wires both fields.
func TestNewFFOModelWithConfig(t *testing.T) {
	model := NewFFOModelWithConfig(14.0, 0.055, testLogger())
	require.NotNil(t, model)
	assert.InDelta(t, 14.0, model.pffoMultiple, 0.001)
	assert.InDelta(t, 0.055, model.navCapRate, 0.0001)
}
