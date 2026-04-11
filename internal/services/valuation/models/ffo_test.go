package models

import (
	"context"
	"os"
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

// TestFFOModel_SupportsIndustry tests industry support
func TestFFOModel_SupportsIndustry(t *testing.T) {
	model := NewFFOModelWithMultiple(15.0, testLogger())

	assert.True(t, model.SupportsIndustry("REIT"))
	assert.True(t, model.SupportsIndustry("RESTATE"))
	assert.True(t, model.SupportsIndustry("reit"))
	assert.False(t, model.SupportsIndustry("FIN"))
	assert.False(t, model.SupportsIndustry("TECH"))
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

// TestLoadPFFOMultiple_ValidFile tests loading P/FFO multiple from a valid temp config file
func TestLoadPFFOMultiple_ValidFile(t *testing.T) {
	// Create a temporary config file with valid JSON
	tmpFile := t.TempDir() + "/industry_multiples.json"
	content := `{"reit_pffo_multiples": {"default": 18.5, "residential": 20.0}}`
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	multiple, err := loadPFFOMultiple(tmpFile)
	require.NoError(t, err)
	assert.InDelta(t, 18.5, multiple, 0.001)
}

// TestLoadPFFOMultiple_InvalidJSON tests loading P/FFO multiple from invalid JSON
func TestLoadPFFOMultiple_InvalidJSON(t *testing.T) {
	tmpFile := t.TempDir() + "/bad.json"
	err := os.WriteFile(tmpFile, []byte("not valid json"), 0644)
	require.NoError(t, err)

	_, err = loadPFFOMultiple(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
}

// TestLoadPFFOMultiple_MissingFile tests loading P/FFO multiple from non-existent file
func TestLoadPFFOMultiple_MissingFile(t *testing.T) {
	_, err := loadPFFOMultiple("/nonexistent/path/file.json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read")
}

// TestLoadPFFOMultiple_NoDefaultKey tests loading when the config has no "default" key
func TestLoadPFFOMultiple_NoDefaultKey(t *testing.T) {
	tmpFile := t.TempDir() + "/no_default.json"
	content := `{"reit_pffo_multiples": {"residential": 20.0}}`
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	_, err = loadPFFOMultiple(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no default P/FFO multiple")
}
