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
	// Warnings: base + multiple + revenue_base source line + negative OI = 4.
	// FY-only data routes through ANNUAL_FY (no ttmWarning).
	assert.Len(t, result.Warnings, 4)
	// RM-1: source line MUST be present so dashboards can pivot on it.
	assertHasWarningPrefix(t, result.Warnings, "revenue_base: source=ANNUAL_FY")
}

// assertHasWarningPrefix is a small helper used by RM-1 tests to verify
// the revenue_base source line is emitted without coupling assertions to
// the exact float formatting of the embedded revenue value.
func assertHasWarningPrefix(t *testing.T, warnings []string, prefix string) {
	t.Helper()
	for _, w := range warnings {
		if len(w) >= len(prefix) && w[:len(prefix)] == prefix {
			return
		}
	}
	t.Errorf("expected a warning starting with %q; got %v", prefix, warnings)
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
	// RM-1: error message now references the TTM helper's INSUFFICIENT_HISTORY
	// source identifier (replaces the legacy "no revenue" wording).
	assert.Contains(t, err.Error(), "insufficient revenue history")
	assert.Contains(t, err.Error(), "INSUFFICIENT_HISTORY")
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

// TestRevenueMultipleModel_ModelType tests model type identifier
func TestRevenueMultipleModel_ModelType(t *testing.T) {
	model := newTestRevenueMultipleModel()
	assert.Equal(t, "revenue_multiple", model.ModelType())
}

// TestRevenueMultipleModel_GetMultiple_NoDefaultFallback tests getMultiple when no
// default key exists in the multiples map, falling back to DefaultEVRevenueMultiple constant.
func TestRevenueMultipleModel_GetMultiple_NoDefaultFallback(t *testing.T) {
	// Create model without a "default" key in multiples
	model := NewRevenueMultipleModelWithMultiples(map[string]float64{
		"TECH": 5.0,
	}, testLogger())

	// Unknown industry with no default key should return DefaultEVRevenueMultiple
	result := model.getMultiple("UNKNOWN")
	assert.Equal(t, DefaultEVRevenueMultiple, result)
}

// TestRevenueMultipleModel_GetMultiple_PrefixMatch tests prefix matching (e.g., "TECH_SAAS" matches "TECH")
func TestRevenueMultipleModel_GetMultiple_PrefixMatch(t *testing.T) {
	model := NewRevenueMultipleModelWithMultiples(map[string]float64{
		"TECH": 5.0,
	}, testLogger())

	result := model.getMultiple("TECH_SAAS")
	assert.Equal(t, 5.0, result)
}

// TestRevenueMultipleModel_GetMultiple_LongestPrefixWinsDeterministic (W-4)
// verifies that when multiple keys could prefix-match, the longest (most specific)
// wins deterministically — regardless of Go's random map iteration order.
// Run the same lookup many times to surface non-determinism if it exists.
func TestRevenueMultipleModel_GetMultiple_LongestPrefixWinsDeterministic(t *testing.T) {
	model := NewRevenueMultipleModelWithMultiples(map[string]float64{
		"HEALTH":         2.0,
		"HEALTH_BIOTECH": 4.0, // longer — should win for "HEALTH_BIO"
		"default":        1.0,
	}, testLogger())

	// "HEALTH_BIO" doesn't exactly match either key, but both could prefix-match.
	// HEALTH_BIOTECH (14 chars) should NOT match "HEALTH_BIO" (10 chars) since
	// HEALTH_BIO is shorter than the key, so only "HEALTH" prefix-matches.
	result := model.getMultiple("HEALTH_BIO")
	assert.Equal(t, 2.0, result, "HEALTH_BIO should prefix-match HEALTH (HEALTH_BIOTECH is longer than input)")

	// "HEALTH_BIOTECH_ONCOLOGY" prefix-matches both; longest (HEALTH_BIOTECH) must win deterministically
	for i := range 100 {
		r := model.getMultiple("HEALTH_BIOTECH_ONCOLOGY")
		require.Equal(t, 4.0, r, "longest prefix (HEALTH_BIOTECH) must always win, run %d", i)
	}
}

// TestRevenueMultipleModel_Calculate_NoFinancialData tests with empty historical data
func TestRevenueMultipleModel_Calculate_NoFinancialData(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "EMPTY",
			Data:   map[string]*entities.FinancialData{},
		},
		Industry:          "TECH",
		SharesOutstanding: 100000000,
	}

	result, err := model.Calculate(ctx, input)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no financial data")
}

// TestRevenueMultipleModel_Calculate_PositiveOI tests that companies with positive OI
// do not get the negative OI warning
func TestRevenueMultipleModel_Calculate_PositiveOI(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "POSITIVE_OI",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Revenue:                   1000000000,
					OperatingIncome:           100000000, // positive OI
					NormalizedOperatingIncome: 100000000,
					FilingDate:                time.Now(),
					FilingPeriod:              "2023FY",
				},
			},
		},
		Industry:          "TECH",
		SharesOutstanding: 100000000,
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// RM-1: warnings = base + multiple info + revenue_base source line.
	// No negative-OI warning fires because OI is positive. FY-only data
	// routes through ANNUAL_FY so no ttmWarning is appended either.
	assert.Len(t, result.Warnings, 3)
	assertHasWarningPrefix(t, result.Warnings, "revenue_base: source=ANNUAL_FY")
}

// TestRevenueMultipleModel_Calculate_RM1_TTMWiring verifies the consumer
// wiring of HistoricalFinancialData.TrailingTwelveMonthsRevenue for the
// scenarios called out in docs/reviewer/RM-1-revenue-multiple-quarterly-vs-ttm.md.
// The helper itself is unit-tested in internal/core/entities; this table
// confirms the model surfaces the right `source` and warnings end-to-end.
func TestRevenueMultipleModel_Calculate_RM1_TTMWiring(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	tests := []struct {
		name              string
		data              map[string]*entities.FinancialData
		expectErr         bool
		expectErrContains string
		// expectRevenueInEV: revenue we expect the model to use as the
		// EV base. Asserted via EnterpriseValue / multiple to dodge any
		// reflection on the model internals.
		expectRevenueInEV float64
		expectSourceTag   string // substring expected in the source-line warning
		expectWarnSub     string // additional warning substring (lossy paths only)
	}{
		{
			name: "T1_TTM_4Q_four_contiguous_quarters",
			data: map[string]*entities.FinancialData{
				"2025Q1": {Revenue: 100, FilingPeriod: "2025Q1", FilingDate: time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)},
				"2025Q2": {Revenue: 110, FilingPeriod: "2025Q2", FilingDate: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)},
				"2025Q3": {Revenue: 120, FilingPeriod: "2025Q3", FilingDate: time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)},
				"2025Q4": {Revenue: 130, FilingPeriod: "2025Q4", FilingDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
			},
			expectRevenueInEV: 460,
			expectSourceTag:   "source=TTM_4Q",
		},
		{
			name: "T2_gap_falls_back_to_annual",
			data: map[string]*entities.FinancialData{
				"2024Q4": {Revenue: 100, FilingPeriod: "2024Q4", FilingDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
				"2025Q1": {Revenue: 105, FilingPeriod: "2025Q1", FilingDate: time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)},
				"2025Q2": {Revenue: 110, FilingPeriod: "2025Q2", FilingDate: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)},
				"2025Q4": {Revenue: 115, FilingPeriod: "2025Q4", FilingDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
				"2024FY": {Revenue: 400, FilingPeriod: "2024FY", FilingDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
			},
			expectRevenueInEV: 400,
			expectSourceTag:   "source=ANNUAL_FY",
			expectWarnSub:     "TTM unavailable",
		},
		{
			name: "T3_T8_MXL_single_quarter_only",
			data: map[string]*entities.FinancialData{
				"2026Q1": {Revenue: 137_188_000, FilingPeriod: "2026Q1", FilingDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
			},
			expectRevenueInEV: 137_188_000 * 4,
			expectSourceTag:   "source=ANNUALIZED_QUARTER",
			expectWarnSub:     "annualized single-quarter",
		},
		{
			name: "T4_two_quarters_only",
			data: map[string]*entities.FinancialData{
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1", FilingDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2", FilingDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
			},
			expectRevenueInEV: 420,
			expectSourceTag:   "source=ANNUALIZED_QUARTER",
			expectWarnSub:     "annualized single-quarter",
		},
		{
			name: "T5_all_annual_no_quarters",
			data: map[string]*entities.FinancialData{
				"2024FY": {Revenue: 900, FilingPeriod: "2024FY", FilingDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
				"2025FY": {Revenue: 1000, FilingPeriod: "2025FY", FilingDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
			},
			expectRevenueInEV: 1000,
			expectSourceTag:   "source=ANNUAL_FY",
		},
		{
			name: "T6_no_revenue_history_errors",
			data: map[string]*entities.FinancialData{
				// One financial-data row so GetLatestPeriod() returns non-nil,
				// but with zero revenue everywhere the TTM helper can find.
				"2025FY": {Revenue: 0, FilingPeriod: "2025FY", FilingDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
			},
			expectErr:         true,
			expectErrContains: "INSUFFICIENT_HISTORY",
		},
		{
			name: "T9_AAPL_FY_plus_Q1_with_prior_quarters_uses_TTM_4Q",
			data: map[string]*entities.FinancialData{
				"2024Q1": {Revenue: 90, FilingPeriod: "2024Q1", FilingDate: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)},
				"2024Q2": {Revenue: 95, FilingPeriod: "2024Q2", FilingDate: time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)},
				"2024Q3": {Revenue: 100, FilingPeriod: "2024Q3", FilingDate: time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC)},
				"2024Q4": {Revenue: 110, FilingPeriod: "2024Q4", FilingDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
				"2024FY": {Revenue: 395, FilingPeriod: "2024FY", FilingDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
				"2025Q1": {Revenue: 105, FilingPeriod: "2025Q1", FilingDate: time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)},
			},
			// Latest 4 by (year,sub): 2024Q2..Q4 + 2025Q1 = 95+100+110+105 = 410.
			expectRevenueInEV: 410,
			expectSourceTag:   "source=TTM_4Q",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &ModelInput{
				HistoricalData: &entities.HistoricalFinancialData{
					Ticker: "RM1_TEST",
					Data:   tt.data,
				},
				Industry:               "TECH", // 5x in the test multiples map
				SharesOutstanding:      100_000_000,
				InterestBearingDebt:    0,
				CashAndCashEquivalents: 0,
			}
			result, err := model.Calculate(ctx, input)
			if tt.expectErr {
				require.Error(t, err)
				require.Nil(t, result)
				assert.Contains(t, err.Error(), tt.expectErrContains)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			// EV = revenue * 5.0 (TECH multiple).
			assert.InDelta(t, tt.expectRevenueInEV*5.0, result.EnterpriseValue, 1.0,
				"enterprise value should reflect the expected TTM revenue base")
			assertHasWarningPrefix(t, result.Warnings, "revenue_base: "+tt.expectSourceTag)
			if tt.expectWarnSub != "" {
				found := false
				for _, w := range result.Warnings {
					if strings.Contains(w, tt.expectWarnSub) {
						found = true
						break
					}
				}
				assert.True(t, found, "expected warning containing %q in %v", tt.expectWarnSub, result.Warnings)
			}
		})
	}
}

// TestRevenueMultipleModel_Calculate_RM1_MXL_LiveShape asserts the headline
// MXL fix: a Q1-only fixture (the exact shape from the live MXL response in
// the RM-1 spec) now produces a per-share value derived from the
// annualized-quarter base, not the raw single-quarter base. With the same
// 1.5x MFG multiple and the same equity bridge, this is the smoke test
// that the bug is gone.
func TestRevenueMultipleModel_Calculate_RM1_MXL_LiveShape(t *testing.T) {
	// 1.5x MFG multiple per config/industry_multiples.json at filing time.
	model := NewRevenueMultipleModelWithMultiples(map[string]float64{
		"default": 2.0,
		"MFG":     1.5,
	}, testLogger())
	ctx := context.Background()

	// MXL Q1 2026 shape from artifacts/2026-05-06/MXL/...
	q1Revenue := 137_188_000.0
	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "MXL",
			Data: map[string]*entities.FinancialData{
				"2026Q1": {Revenue: q1Revenue, FilingPeriod: "2026Q1",
					FilingDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
		Industry:               "MFG",
		SharesOutstanding:      80_000_000, // representative; not load-bearing for the assertion
		InterestBearingDebt:    100_000_000,
		CashAndCashEquivalents: 50_000_000,
	}
	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// EV from annualized base: q1Revenue * 4 * 1.5 = ~$823M (vs legacy ~$206M).
	expectedEV := q1Revenue * 4 * 1.5
	assert.InDelta(t, expectedEV, result.EnterpriseValue, 1.0,
		"EV should use 4x annualized Q1 revenue, not raw single-quarter revenue")

	// Source line MUST identify the lossy ANNUALIZED_QUARTER path so
	// downstream consumers can filter/flag this kind of result.
	assertHasWarningPrefix(t, result.Warnings, "revenue_base: source=ANNUALIZED_QUARTER")
}

// TestLoadEVRevenueMultiples_UsesEmbed verifies loadEVRevenueMultiples reads
// from the embedded industry_multiples.json. Replaces the legacy tmpfile-path
// tests that exercised os.ReadFile error branches no longer possible with
// embed; the missing-file branch is covered by configfs.TestRead_MissingFile.
func TestLoadEVRevenueMultiples_UsesEmbed(t *testing.T) {
	multiples, err := loadEVRevenueMultiples()
	require.NoError(t, err)
	// Values from config/industry_multiples.json at the time of the sweep.
	assert.Equal(t, 2.0, multiples["default"])
	assert.Equal(t, 5.0, multiples["TECH"])
}
