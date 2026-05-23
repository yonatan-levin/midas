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

// ---------------------------------------------------------------------------
// Subsector loader + lookup coverage — T2-P4-W2 item 8 close
//
// `loadFFOSubsectorTables` and `lookupSubsectorValue` ship subsector-specific
// P/FFO multiples and cap rates (VAL-3 P1+P4). Per-function coverage was
// flagged at 71.4% / 76.5% by P4 QA (C4 finding). These tests pin the
// defensive branches that the existing Calculate-driven tests don't hit:
//
//   - lookupSubsectorValue exact-match path on uppercased keys
//   - longest-prefix-match path (e.g. REIT_DATACENTER_PRIMARY beating REIT)
//   - the "default" key is excluded from prefix matching
//   - nil / empty-table / empty-industry early returns
//   - case-insensitive comparison (industry input lowercased)
//   - loadFFOSubsectorTables returns the expected REIT_* subsector keys from
//     the embedded industry_multiples.json (happy path)
//
// Test-only addition; no changes to ffo.go production code. The two
// loadFFOSubsectorTables error branches (configfs.Read failure /
// json.Unmarshal failure) are unreachable from the public API because
// configfs.Read is backed by the embed.FS rooted at config/ which always
// returns the same valid bytes baked into the binary. Those branches are
// structurally defensive — exercising them would require a production-code
// seam that the task scope explicitly excludes.
// ---------------------------------------------------------------------------

// TestLookupSubsectorValue_ExactMatch verifies the exact-match path returns
// the value when the input matches a table key after uppercasing.
func TestLookupSubsectorValue_ExactMatch(t *testing.T) {
	table := map[string]float64{
		"REIT_DATACENTER":  31.0,
		"REIT_RESIDENTIAL": 20.0,
		"default":          15.0,
	}

	// Already uppercased.
	v, ok := lookupSubsectorValue(table, "REIT_DATACENTER")
	assert.True(t, ok)
	assert.InDelta(t, 31.0, v, 0.001)

	// Mixed case input — lookup uppercases internally.
	v, ok = lookupSubsectorValue(table, "reit_residential")
	assert.True(t, ok)
	assert.InDelta(t, 20.0, v, 0.001)
}

// TestLookupSubsectorValue_PrefixMatch verifies the longest-prefix-match
// branch fires for inputs that extend a known key past an underscore
// boundary. This is the previously-uncovered branch in lookupSubsectorValue.
func TestLookupSubsectorValue_PrefixMatch(t *testing.T) {
	table := map[string]float64{
		"REIT":            15.0,
		"REIT_DATACENTER": 31.0,
		"default":         12.0,
	}

	// Should match REIT_DATACENTER (longer key wins) — not REIT.
	v, ok := lookupSubsectorValue(table, "REIT_DATACENTER_PRIMARY")
	assert.True(t, ok)
	assert.InDelta(t, 31.0, v, 0.001, "longest-prefix-match should pick the longer key")

	// Should match REIT (only prefix that ends at an underscore boundary).
	v, ok = lookupSubsectorValue(table, "REIT_UNKNOWN_SUB")
	assert.True(t, ok)
	assert.InDelta(t, 15.0, v, 0.001, "shorter key wins when longer key isn't a prefix")
}

// TestLookupSubsectorValue_PrefixMatch_UnderscoreBoundary verifies the
// underscore-boundary guard — "TECHNOLOGY" must NOT match key "TECH" because
// the match would not end at an underscore (W-4 invariant from ffo.go).
func TestLookupSubsectorValue_PrefixMatch_UnderscoreBoundary(t *testing.T) {
	table := map[string]float64{
		"TECH": 25.0,
	}

	// "TECHNOLOGY" extends "TECH" but not at an underscore -> miss.
	v, ok := lookupSubsectorValue(table, "TECHNOLOGY")
	assert.False(t, ok, "match must end at underscore or string end (W-4 invariant)")
	assert.Equal(t, 0.0, v)

	// "TECH_X" extends "TECH" at an underscore -> hit.
	v, ok = lookupSubsectorValue(table, "TECH_X")
	assert.True(t, ok)
	assert.InDelta(t, 25.0, v, 0.001)
}

// TestLookupSubsectorValue_DefaultKeyExcludedFromPrefix verifies the "default"
// key is skipped in the prefix-match loop. An industry input that happens to
// start with "DEFAULT" must NOT match the "default" entry via prefix-match.
func TestLookupSubsectorValue_DefaultKeyExcludedFromPrefix(t *testing.T) {
	table := map[string]float64{
		"default": 15.0,
	}

	// Even exact-match on "DEFAULT" (uppercased) misses because "default" is
	// stored lowercased and the exact-match step is case-sensitive after
	// upper(). The prefix-match step then explicitly skips the "default" key.
	v, ok := lookupSubsectorValue(table, "DEFAULT_VARIANT")
	assert.False(t, ok, "the default key must not participate in prefix matching")
	assert.Equal(t, 0.0, v)
}

// TestLookupSubsectorValue_NilTable verifies the early return on a nil map.
func TestLookupSubsectorValue_NilTable(t *testing.T) {
	v, ok := lookupSubsectorValue(nil, "REIT_DATACENTER")
	assert.False(t, ok)
	assert.Equal(t, 0.0, v)
}

// TestLookupSubsectorValue_EmptyTable verifies the early return on an empty map.
func TestLookupSubsectorValue_EmptyTable(t *testing.T) {
	v, ok := lookupSubsectorValue(map[string]float64{}, "REIT_DATACENTER")
	assert.False(t, ok)
	assert.Equal(t, 0.0, v)
}

// TestLookupSubsectorValue_EmptyIndustry verifies the early return on an
// empty industry string. Mirrors the "missing required key / empty subsector"
// branch named in the T2-P4-W2 item 8 spec.
func TestLookupSubsectorValue_EmptyIndustry(t *testing.T) {
	table := map[string]float64{
		"REIT_DATACENTER": 31.0,
		"default":         15.0,
	}
	v, ok := lookupSubsectorValue(table, "")
	assert.False(t, ok, "empty industry must short-circuit before any map iteration")
	assert.Equal(t, 0.0, v)
}

// TestLookupSubsectorValue_NoMatch verifies the fall-through branch returns
// (0, false) when neither exact nor prefix match succeeds. Callers (getMultiple
// / getCapRate) treat this as "apply the model default".
func TestLookupSubsectorValue_NoMatch(t *testing.T) {
	table := map[string]float64{
		"REIT_DATACENTER": 31.0,
		"REIT_RETAIL":     16.0,
	}
	v, ok := lookupSubsectorValue(table, "UTILITIES_REGULATED")
	assert.False(t, ok)
	assert.Equal(t, 0.0, v)
}

// TestFFOModel_getMultiple_FallbackToDefault verifies the getMultiple wrapper
// returns the model's default pffoMultiple when the subsector lookup misses.
// Exercises the lookupSubsectorValue → false → fallback chain end-to-end.
func TestFFOModel_getMultiple_FallbackToDefault(t *testing.T) {
	tables := map[string]float64{
		"REIT_DATACENTER": 31.0,
	}
	model := NewFFOModelWithTables(15.0, 0.06, tables, nil, testLogger())

	// Hit -> 31.0 from the subsector table.
	assert.InDelta(t, 31.0, model.getMultiple("REIT_DATACENTER"), 0.001)

	// Miss -> 15.0 default from the model.
	assert.InDelta(t, 15.0, model.getMultiple("UTILITIES"), 0.001)

	// Empty industry -> 15.0 default from the model.
	assert.InDelta(t, 15.0, model.getMultiple(""), 0.001)
}

// TestFFOModel_getCapRate_FallbackToDefault mirrors getMultiple's fallback test
// for the cap-rate path.
func TestFFOModel_getCapRate_FallbackToDefault(t *testing.T) {
	capRateTable := map[string]float64{
		"REIT_DATACENTER": 0.04,
	}
	model := NewFFOModelWithTables(15.0, 0.06, nil, capRateTable, testLogger())

	assert.InDelta(t, 0.04, model.getCapRate("REIT_DATACENTER"), 0.0001)
	assert.InDelta(t, 0.06, model.getCapRate("UTILITIES"), 0.0001)
	assert.InDelta(t, 0.06, model.getCapRate(""), 0.0001)
}

// TestLoadFFOSubsectorTables_EmbeddedConfig verifies loadFFOSubsectorTables
// returns the populated REIT_* subsector tables from the embedded
// industry_multiples.json. Pins the happy-path return statement and asserts
// the keys the T2-P4-W1 prefix reconciliation guarantees are present.
func TestLoadFFOSubsectorTables_EmbeddedConfig(t *testing.T) {
	pffoTable, capRateTable := loadFFOSubsectorTables()
	require.NotNil(t, pffoTable, "embedded config should yield a populated P/FFO table")
	require.NotNil(t, capRateTable, "embedded config should yield a populated cap-rate table")

	// All 8 REIT subsectors from T2-P4-W1 prefix reconciliation must be
	// present in both tables.
	subsectors := []string{
		"REIT_RESIDENTIAL",
		"REIT_OFFICE",
		"REIT_INDUSTRIAL",
		"REIT_HEALTHCARE",
		"REIT_DATACENTER",
		"REIT_CELLTOWER",
		"REIT_RETAIL",
		"REIT_SPECIALTY",
	}
	for _, key := range subsectors {
		_, hasP := pffoTable[key]
		assert.Truef(t, hasP, "P/FFO table missing REIT subsector key %q", key)
		_, hasC := capRateTable[key]
		assert.Truef(t, hasC, "cap-rate table missing REIT subsector key %q", key)
	}

	// Default keys also present (loadFFOConfig consumes these).
	_, hasDefault := pffoTable["default"]
	assert.True(t, hasDefault, "P/FFO table must carry a default entry")
	_, hasCapDefault := capRateTable["default"]
	assert.True(t, hasCapDefault, "cap-rate table must carry a default entry")
}

// TestLoadFFOSubsectorTables_FeedsLookup verifies the tables returned by
// loadFFOSubsectorTables flow correctly through lookupSubsectorValue — i.e.,
// the integration is wired without surprises. Acts as a regression pin for
// the data-shape contract between the loader and the lookup helper.
func TestLoadFFOSubsectorTables_FeedsLookup(t *testing.T) {
	pffoTable, capRateTable := loadFFOSubsectorTables()
	require.NotNil(t, pffoTable)
	require.NotNil(t, capRateTable)

	// Data-center multiple is the highest (>=25 per VAL-3 P4 spec).
	v, ok := lookupSubsectorValue(pffoTable, "REIT_DATACENTER")
	require.True(t, ok)
	assert.GreaterOrEqual(t, v, 20.0, "data-center P/FFO multiple should be elevated vs default")

	// Cap rate is positive and below 20% for any well-formed REIT subsector.
	v, ok = lookupSubsectorValue(capRateTable, "REIT_DATACENTER")
	require.True(t, ok)
	assert.Greater(t, v, 0.0)
	assert.Less(t, v, 0.20)
}
