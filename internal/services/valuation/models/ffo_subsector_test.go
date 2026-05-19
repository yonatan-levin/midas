package models

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestFFOModel_GetMultiple_Subsectors pins VAL-3 Phase 1: the FFO model
// must look up the per-subsector P/FFO multiple from
// reit_pffo_multiples in the embedded industry_multiples.json. Without this,
// every REIT would silently fall back to the 15x default and data-center /
// cell-tower / mall REITs would be 2-3x mispriced.
func TestFFOModel_GetMultiple_Subsectors(t *testing.T) {
	model := NewFFOModel(testLogger())
	require.NotNil(t, model)

	tests := []struct {
		name     string
		industry string
		expected float64
	}{
		{name: "REIT_DATACENTER returns 31x (premium subsector)", industry: "REIT_DATACENTER", expected: 31.0},
		{name: "REIT_CELLTOWER returns 25x", industry: "REIT_CELLTOWER", expected: 25.0},
		{name: "REIT_INDUSTRIAL returns 22.5x", industry: "REIT_INDUSTRIAL", expected: 22.5},
		{name: "REIT_RESIDENTIAL returns 20x", industry: "REIT_RESIDENTIAL", expected: 20.0},
		{name: "REIT_HEALTHCARE returns 17.5x", industry: "REIT_HEALTHCARE", expected: 17.5},
		{name: "REIT_SPECIALTY returns 17.5x", industry: "REIT_SPECIALTY", expected: 17.5},
		{name: "REIT_OFFICE returns 14x (commercial discount)", industry: "REIT_OFFICE", expected: 14.0},
		{name: "REIT_RETAIL returns 10x (mall headwinds)", industry: "REIT_RETAIL", expected: 10.0},
		{name: "RESTATE parent (no subsector match) falls back to default 15x", industry: "RESTATE", expected: 15.0},
		{name: "empty industry falls back to default 15x", industry: "", expected: 15.0},
		{name: "unknown subsector falls back to default 15x", industry: "FROZEN_ASSETS", expected: 15.0},
		{name: "case-insensitive lookup (lower-case)", industry: "reit_datacenter", expected: 31.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.getMultiple(tt.industry)
			assert.InDelta(t, tt.expected, got, 0.0001,
				"getMultiple(%q) = %v; want %v", tt.industry, got, tt.expected)
		})
	}
}

// TestFFOModel_GetCapRate_Subsectors pins VAL-3 Phase 4: the NAV cross-check
// must use per-subsector cap rates so e.g. data center REITs use 4.0% and
// retail REITs use 8.5% rather than the blended 6% default.
func TestFFOModel_GetCapRate_Subsectors(t *testing.T) {
	model := NewFFOModel(testLogger())
	require.NotNil(t, model)

	tests := []struct {
		name     string
		industry string
		expected float64
	}{
		{name: "REIT_DATACENTER 4.0%", industry: "REIT_DATACENTER", expected: 0.04},
		{name: "REIT_CELLTOWER 4.5%", industry: "REIT_CELLTOWER", expected: 0.045},
		{name: "REIT_INDUSTRIAL 4.5%", industry: "REIT_INDUSTRIAL", expected: 0.045},
		{name: "REIT_RESIDENTIAL 5.0%", industry: "REIT_RESIDENTIAL", expected: 0.05},
		{name: "REIT_HEALTHCARE 6.0%", industry: "REIT_HEALTHCARE", expected: 0.06},
		{name: "REIT_OFFICE 7.5%", industry: "REIT_OFFICE", expected: 0.075},
		{name: "REIT_RETAIL 8.5%", industry: "REIT_RETAIL", expected: 0.085},
		// REIT_SPECIALTY (VAL-7): self-storage / billboard / corrections / timber blended
		// median. Pins the reit_cap_rates.REIT_SPECIALTY entry so the bucket no
		// longer falls through to the 6% default.
		{name: "REIT_SPECIALTY 5.5%", industry: "REIT_SPECIALTY", expected: 0.055},
		{name: "RESTATE parent falls back to default 6%", industry: "RESTATE", expected: 0.06},
		{name: "empty industry falls back to default 6%", industry: "", expected: 0.06},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.getCapRate(tt.industry)
			assert.InDelta(t, tt.expected, got, 0.00001,
				"getCapRate(%q) = %v; want %v", tt.industry, got, tt.expected)
		})
	}
}

// TestFFOModel_Calculate_REIT_DATACENTER_Uses31x verifies the end-to-end path:
// when ModelInput.Industry="REIT_DATACENTER" the model multiplies FFO/share by
// 31x rather than the 15x default. This is the headline VAL-3 P1 fix.
func TestFFOModel_Calculate_REIT_DATACENTER_Uses31x(t *testing.T) {
	model := NewFFOModel(testLogger())
	ctx := context.Background()

	// FFO = 1B + 500M - 0 = 1.5B
	// FFO/share = 1.5B / 100M = 15.0
	// Value/share = 15.0 * 31.0 = 465.0
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "DLR",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   1000000000,
					DepreciationAndAmortization: 500000000,
					GainOnPropertySales:         0,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		Industry:               "REIT_DATACENTER",
		SharesOutstanding:      100000000,
		InterestBearingDebt:    20000000000,
		CashAndCashEquivalents: 1000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.InDelta(t, 465.0, result.IntrinsicValuePerShare, 0.01,
		"REIT_DATACENTER subsector must apply 31x P/FFO multiple")
	assert.Equal(t, "ffo", result.ModelType)
}

// TestFFOModel_Calculate_REIT_CELLTOWER_Uses25x mirrors the REIT_DATACENTER
// assertion for the cell-tower subsector (AMT, CCI). 25x P/FFO is the
// calibrated 2025-26 sector median per the tracker's perplexity citations.
func TestFFOModel_Calculate_REIT_CELLTOWER_Uses25x(t *testing.T) {
	model := NewFFOModel(testLogger())
	ctx := context.Background()

	// FFO = 2B + 1B - 100M = 2.9B; FFO/share = 2.9B / 500M = 5.8
	// Value/share = 5.8 * 25 = 145.0
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "AMT",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   2000000000,
					DepreciationAndAmortization: 1000000000,
					GainOnPropertySales:         100000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		Industry:               "REIT_CELLTOWER",
		SharesOutstanding:      500000000,
		InterestBearingDebt:    40000000000,
		CashAndCashEquivalents: 2000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.InDelta(t, 145.0, result.IntrinsicValuePerShare, 0.01,
		"REIT_CELLTOWER subsector must apply 25x P/FFO multiple")
}

// TestFFOModel_Calculate_REIT_RETAIL_Uses10x pins the downward subsector
// adjustment for mall REITs (SPG, KIM). 10x reflects the 2025-26 mall
// headwinds; the prior uniform 15x systematically overpriced the bucket.
func TestFFOModel_Calculate_REIT_RETAIL_Uses10x(t *testing.T) {
	model := NewFFOModel(testLogger())
	ctx := context.Background()

	// FFO = 1B + 500M = 1.5B; FFO/share = 1.5B / 300M = 5.0
	// Value/share = 5.0 * 10 = 50.0
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "SPG",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   1000000000,
					DepreciationAndAmortization: 500000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		Industry:               "REIT_RETAIL",
		SharesOutstanding:      300000000,
		InterestBearingDebt:    25000000000,
		CashAndCashEquivalents: 1000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.InDelta(t, 50.0, result.IntrinsicValuePerShare, 0.01,
		"REIT_RETAIL subsector must apply 10x P/FFO multiple")
}

// TestFFOModel_Calculate_RESTATE_FallbackToDefault verifies the no-subsector
// branch: a generic REIT (parent RESTATE, no subsector refinement) keeps
// using the default 15x. This guards against accidentally regressing the
// fallback path while adding subsector-specific entries.
func TestFFOModel_Calculate_RESTATE_FallbackToDefault(t *testing.T) {
	model := NewFFOModel(testLogger())
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "GENERIC_REIT",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   1000000000,
					DepreciationAndAmortization: 500000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		Industry:               "RESTATE",
		SharesOutstanding:      100000000,
		InterestBearingDebt:    5000000000,
		CashAndCashEquivalents: 500000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// FFO/share = 1.5B / 100M = 15; Value = 15 * 15 (default) = 225
	assert.InDelta(t, 225.0, result.IntrinsicValuePerShare, 0.01,
		"RESTATE parent must fall back to default 15x P/FFO when no subsector matched")
}

// TestFFOModel_Calculate_NAVCrossCheck_REIT_DATACENTER_UsesSubsectorCapRate
// verifies that the NAV cross-check picks the data-center cap rate (4%)
// rather than the default 6% when ModelInput.Industry="REIT_DATACENTER".
// 6% would have produced a different ratio and a different warning state;
// this test catches the case where the multiple is read per-subsector but
// the cap rate isn't.
func TestFFOModel_Calculate_NAVCrossCheck_REIT_DATACENTER_UsesSubsectorCapRate(t *testing.T) {
	model := NewFFOModel(testLogger())
	ctx := context.Background()

	// At cap rate 0.04 with OperatingIncome 1B and 100M shares:
	//   NAV = 1B / 0.04 = 25B; NAV/share = 250.
	// FFO value = (1B + 500M)/100M * 31 = 15 * 31 = 465.
	// ratio = 465/250 = 1.86 → within thresholds (no NAV warning).
	// At default 0.06 the ratio would be 465 / (1B/0.06/100M=166.67) = 2.79 → warning.
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "DLR",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					NetIncome:                   1000000000,
					DepreciationAndAmortization: 500000000,
					OperatingIncome:             1000000000,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2023FY",
				},
			},
		},
		Industry:               "REIT_DATACENTER",
		SharesOutstanding:      100000000,
		InterestBearingDebt:    20000000000,
		CashAndCashEquivalents: 1000000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// No NAV divergence warning at the subsector cap rate
	for _, w := range result.Warnings {
		assert.NotContains(t, w, "NAV",
			"REIT_DATACENTER cap rate 4%% should keep NAV-vs-PFFO ratio inside thresholds")
	}
}

// TestFFOModel_NewFFOModelWithTables_NilDisablesLookup pins the suppression
// path used by tests that want to exercise the default-only behaviour with
// no subsector lookup interference.
func TestFFOModel_NewFFOModelWithTables_NilDisablesLookup(t *testing.T) {
	// Pass nil maps — every lookup must fall back to the explicit defaults.
	model := NewFFOModelWithTables(15.0, 0.06, nil, nil, testLogger())
	require.NotNil(t, model)

	assert.InDelta(t, 15.0, model.getMultiple("REIT_DATACENTER"), 0.0001,
		"nil pffoMultiples must force fallback to default")
	assert.InDelta(t, 0.06, model.getCapRate("REIT_DATACENTER"), 0.00001,
		"nil capRates must force fallback to default")
}
