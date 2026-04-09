package models

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func newTestRevenueMultipleModel() *RevenueMultipleModel {
	multiples := map[string]float64{
		"default": 2.0,
		"TECH":    5.0,
		"HEALTH":  3.0,
		"FIN":     2.5,
		"RETAIL":  1.0,
	}
	return NewRevenueMultipleModelWithMultiples(multiples, testLogger())
}

// TestRevenueMultipleModel_Calculate_StandardTech tests revenue multiple for a tech company
func TestRevenueMultipleModel_Calculate_StandardTech(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "STARTUP",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Revenue:                   500000000,
					OperatingIncome:           -50000000, // pre-profit
					NormalizedOperatingIncome: -50000000,
					FilingDate:                time.Now(),
					FilingPeriod:              "2023FY",
				},
			},
		},
		Industry:               "TECH",
		SharesOutstanding:      100000000,
		InterestBearingDebt:    50000000,
		CashAndCashEquivalents: 200000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// EV = 500M * 5.0 = 2.5B
	// Equity = 2.5B - 50M + 200M = 2.65B
	// Value/share = 2.65B / 100M = 26.5
	assert.Equal(t, "revenue_multiple", result.ModelType)
	assert.InDelta(t, 26.5, result.IntrinsicValuePerShare, 0.01)
	assert.Equal(t, "low", result.Confidence, "revenue multiple should always be low confidence")
	assert.Len(t, result.Warnings, 3) // base warning + multiple info + negative OI warning
}

// TestRevenueMultipleModel_Calculate_ZeroRevenue tests revenue multiple with zero revenue
func TestRevenueMultipleModel_Calculate_ZeroRevenue(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "PRE_REVENUE",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Revenue:      0,
					FilingDate:   time.Now(),
					FilingPeriod: "2023FY",
				},
			},
		},
		Industry:          "TECH",
		SharesOutstanding: 100000000,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no revenue")
}

// TestRevenueMultipleModel_Calculate_DefaultMultiple tests default multiple for unknown industry
func TestRevenueMultipleModel_Calculate_DefaultMultiple(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "UNKNOWN",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Revenue:      1000000000,
					FilingDate:   time.Now(),
					FilingPeriod: "2023FY",
				},
			},
		},
		Industry:          "UNKNOWN_INDUSTRY",
		SharesOutstanding: 100000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// EV = 1B * 2.0 (default) = 2B
	// Equity = 2B - 0 + 0 = 2B
	// Value/share = 2B / 100M = 20
	assert.InDelta(t, 20.0, result.IntrinsicValuePerShare, 0.01)
}

// TestRevenueMultipleModel_Calculate_NilInput tests nil input handling
func TestRevenueMultipleModel_Calculate_NilInput(t *testing.T) {
	model := newTestRevenueMultipleModel()
	result, err := model.Calculate(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestRevenueMultipleModel_Calculate_ZeroShares tests zero shares handling
func TestRevenueMultipleModel_Calculate_ZeroShares(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NO_SHARES",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Revenue:      1000000,
					FilingDate:   time.Now(),
					FilingPeriod: "2023FY",
				},
			},
		},
		Industry:          "TECH",
		SharesOutstanding: 0,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "shares outstanding")
}

// TestRevenueMultipleModel_Calculate_HighDebt tests equity bridge with high debt
func TestRevenueMultipleModel_Calculate_HighDebt(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "HIGH_DEBT",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Revenue:      100000000,
					FilingDate:   time.Now(),
					FilingPeriod: "2023FY",
				},
			},
		},
		Industry:               "RETAIL",
		SharesOutstanding:      10000000,
		InterestBearingDebt:    500000000, // debt > EV
		CashAndCashEquivalents: 10000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// EV = 100M * 1.0 = 100M
	// Equity = 100M - 500M + 10M = -390M -> value capped at 0
	assert.Equal(t, 0.0, result.IntrinsicValuePerShare, "value should be zero when debt exceeds EV")
}

// TestRevenueMultipleModel_GetMultiple tests multiple selection by industry
func TestRevenueMultipleModel_GetMultiple(t *testing.T) {
	model := newTestRevenueMultipleModel()

	tests := []struct {
		name     string
		industry string
		expected float64
	}{
		{"tech industry", "TECH", 5.0},
		{"health industry", "HEALTH", 3.0},
		{"retail industry", "RETAIL", 1.0},
		{"unknown industry uses default", "TELECOM", 2.0},
		{"empty industry uses default", "", 2.0},
		{"case insensitive", "tech", 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			multiple := model.getMultiple(tt.industry)
			assert.Equal(t, tt.expected, multiple)
		})
	}
}

// TestRevenueMultipleModel_SupportsIndustry tests that revenue multiple supports all industries
func TestRevenueMultipleModel_SupportsIndustry(t *testing.T) {
	model := newTestRevenueMultipleModel()

	assert.True(t, model.SupportsIndustry("TECH"))
	assert.True(t, model.SupportsIndustry("FIN"))
	assert.True(t, model.SupportsIndustry("REIT"))
	assert.True(t, model.SupportsIndustry(""))
	assert.True(t, model.SupportsIndustry("ANYTHING"))
}

// TestRevenueMultipleModel_ModelType tests model type identifier
func TestRevenueMultipleModel_ModelType(t *testing.T) {
	model := newTestRevenueMultipleModel()
	assert.Equal(t, "revenue_multiple", model.ModelType())
}
