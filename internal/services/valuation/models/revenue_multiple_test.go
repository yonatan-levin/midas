package models

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
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
	// Warnings: base + multiple + revenue_base source line + multiple_source
	// line (RM-2 Phase 2) + negative OI = 5.
	// FY-only data routes through ANNUAL_FY (no ttmWarning).
	assert.Len(t, result.Warnings, 5)
	// RM-1: source line MUST be present so dashboards can pivot on it.
	assertHasWarningPrefix(t, result.Warnings, "revenue_base: source=ANNUAL_FY")
}

// TestRevenueMultiple_SubtractsDebtLikeClaims is the DC-1 Phase 4 followup
// regression: the EV→Equity bridge must subtract InvestedCapital().DebtLikeClaims
// (B1 lease + B2 pension + B3 contingent overlay amounts) in BOTH the trailing
// and forward paths. Pre-fix, the bridge read the B-rule-free
// Restated().InterestBearingDebt but never re-subtracted DebtLikeClaims, so
// those claims were silently dropped and equity was overstated for B-rule-
// firing revenue_multiple tickers. Mirrors the DCF path's
// dcf.CalculateEquityValueWithDebtLikeClaims.
func TestRevenueMultiple_SubtractsDebtLikeClaims(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	const (
		revenue    = 1_000_000_000.0 // 1B
		multiple   = 5.0             // TECH
		debt       = 100_000_000.0
		cash       = 50_000_000.0
		shares     = 100_000_000.0
		claims     = 80_000_000.0 // B1+B2+B3 overlay total
		enterprise = revenue * multiple
	)

	t.Run("trailing_bridge_subtracts_claims", func(t *testing.T) {
		input := &ModelInput{
			HistoricalData: &entities.HistoricalFinancialData{
				Ticker: "B_RULE_TICKER",
				Data: map[string]*entities.FinancialData{
					"2023FY": {
						Revenue:      revenue,
						FilingDate:   time.Now(),
						FilingPeriod: "2023FY",
					},
				},
			},
			Industry:               "TECH",
			SharesOutstanding:      shares,
			InterestBearingDebt:    debt,
			CashAndCashEquivalents: cash,
			DebtLikeClaims:         claims,
		}

		result, err := model.Calculate(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Equity = EV - Debt + Cash - DebtLikeClaims
		//        = 5B - 100M + 50M - 80M = 4.87B
		wantEquity := enterprise - debt + cash - claims
		assert.InDelta(t, wantEquity, result.EquityValue, 1.0,
			"equity bridge must subtract DebtLikeClaims")
		assert.InDelta(t, wantEquity/shares, result.IntrinsicValuePerShare, 1e-6,
			"per-share must reflect the DebtLikeClaims subtraction")
	})

	t.Run("zero_claims_unchanged_backward_compat", func(t *testing.T) {
		input := &ModelInput{
			HistoricalData: &entities.HistoricalFinancialData{
				Ticker: "NO_B_RULE",
				Data: map[string]*entities.FinancialData{
					"2023FY": {
						Revenue:      revenue,
						FilingDate:   time.Now(),
						FilingPeriod: "2023FY",
					},
				},
			},
			Industry:               "TECH",
			SharesOutstanding:      shares,
			InterestBearingDebt:    debt,
			CashAndCashEquivalents: cash,
			DebtLikeClaims:         0, // no B-rule fires → bridge unchanged
		}

		result, err := model.Calculate(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Equity = EV - Debt + Cash (legacy bridge, DebtLikeClaims=0).
		wantEquity := enterprise - debt + cash
		assert.InDelta(t, wantEquity, result.EquityValue, 1.0,
			"DebtLikeClaims=0 must leave the bridge unchanged (backward compat)")
	})
}

// TestRevenueMultiple_Forward_SubtractsDebtLikeClaims verifies the forward
// (RM-3) bridge also subtracts DebtLikeClaims. Asserts the forward per-share
// value drops by exactly DebtLikeClaims/shares relative to a zero-claims run
// on the same fixture.
func TestRevenueMultiple_Forward_SubtractsDebtLikeClaims(t *testing.T) {
	const claims = 80_000_000.0

	buildInput := func(t *testing.T, dlc float64) *ModelInput {
		in := buildMXLLikeInput(t)
		in.DebtLikeClaims = dlc
		in.Profile = &profile.ResolvedProfile{
			AssumptionProfile: profile.AssumptionProfile{
				ProfileID:         "cyclical_trough:standard_growth",
				Archetype:         profile.ArchetypeCyclicalTrough,
				Maturity:          profile.MaturityStandardGrowth,
				HorizonYears:      5,
				CompoundGrowthCap: 3.0,
				RevenueBaseMethod: profile.RevenueBaseMaxTTMOrFloor,
				TerminalMultiple:  4.0,
				TerminalMethod:    profile.TerminalExitMultiple,
				DiscountMethod:    profile.DiscountCostOfEquity,
			},
		}
		return in
	}

	rmMultiples := map[string]float64{
		"default":  2.0,
		"MFG_SEMI": 1.5,
		"MFG":      1.5,
	}

	rm := NewRevenueMultipleModelWithMultiples(rmMultiples, testLogger())
	withClaims, err := rm.Calculate(context.Background(), buildInput(t, claims))
	require.NoError(t, err)
	noClaims, err := rm.Calculate(context.Background(), buildInput(t, 0))
	require.NoError(t, err)

	shares := buildMXLLikeInput(t).SharesOutstanding
	require.Greater(t, noClaims.ForwardValue, 0.0, "forward must be computed")
	require.Greater(t, withClaims.ForwardValue, 0.0, "forward must be computed")

	// Forward value with claims must be lower by exactly claims/shares
	// (forward equity = forwardEV - debt + cash - DebtLikeClaims, then /shares).
	wantDrop := claims / shares
	assert.InDelta(t, wantDrop, noClaims.ForwardValue-withClaims.ForwardValue, 1e-6,
		"forward per-share must drop by DebtLikeClaims/shares")
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

	// RM-1: warnings = base + multiple info + revenue_base source line +
	// multiple_source line (RM-2 Phase 2). No negative-OI warning fires because
	// OI is positive. FY-only data routes through ANNUAL_FY so no ttmWarning is
	// appended either.
	assert.Len(t, result.Warnings, 4)
	assertHasWarningPrefix(t, result.Warnings, "revenue_base: source=ANNUAL_FY")
}

// TestRevenueMultipleModel_resolveMultiple covers the RM-2 Phase 2 four-tier
// resolution order and the provenance source string contract.
func TestRevenueMultipleModel_resolveMultiple(t *testing.T) {
	phase1 := map[string]float64{
		"default":   2.0,
		"MFG_SEMI":  6.5,
		"TECH_SAAS": 8.0,
	}
	damodaran := map[string]float64{
		"Semiconductor":                   15.7006,
		"Software (System & Application)": 11.4088,
	}
	xwalk := map[string]string{
		"3674": "Semiconductor",
		"9999": "Nonexistent Industry", // dangling: absent from damodaran table
	}
	model := NewRevenueMultipleModelWithDamodaran(phase1, damodaran, xwalk, "2026-01-01", testLogger())

	tests := []struct {
		name         string
		sic          string
		industry     string
		wantMultiple float64
		wantSource   string
	}{
		{
			name:         "mapped SIC wins over classifier bucket (Damodaran first)",
			sic:          "3674",
			industry:     "MFG_SEMI", // Phase 1 would give 6.5; Damodaran must win
			wantMultiple: 15.7006,
			wantSource:   "Damodaran 2026-01-01",
		},
		{
			name:         "unmapped SIC falls back to Phase 1 prefix bucket",
			sic:          "1234",
			industry:     "TECH_SAAS",
			wantMultiple: 8.0,
			wantSource:   "sector-bucket",
		},
		{
			name:         "empty SIC falls back to Phase 1 bucket",
			sic:          "",
			industry:     "MFG_SEMI",
			wantMultiple: 6.5,
			wantSource:   "sector-bucket",
		},
		{
			name:         "dangling crosswalk entry degrades to Phase 1 (no panic)",
			sic:          "9999",
			industry:     "MFG_SEMI",
			wantMultiple: 6.5,
			wantSource:   "sector-bucket",
		},
		{
			name:         "unknown industry falls back to default bucket",
			sic:          "1234",
			industry:     "UNKNOWN",
			wantMultiple: 2.0,
			wantSource:   "sector-bucket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mult, source, _ := model.resolveMultiple(tt.sic, tt.industry)
			if mult != tt.wantMultiple {
				t.Errorf("multiple = %v, want %v", mult, tt.wantMultiple)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
		})
	}
}

// TestRevenueMultipleModel_resolveMultiple_ZeroRegression is the load-bearing
// pin: when the Damodaran tables are nil (config absent / a ctor that did not
// inject them), resolveMultiple's multiplier is bit-for-bit identical to the
// legacy getMultiple(industry) — even for a SIC that WOULD have resolved a
// Damodaran multiple had the tables been present.
func TestRevenueMultipleModel_resolveMultiple_ZeroRegression(t *testing.T) {
	phase1 := map[string]float64{
		"default":   2.0,
		"MFG_SEMI":  6.5,
		"TECH_SAAS": 8.0,
		"HEALTH":    3.0,
	}
	// No Damodaran tables injected → tier (1) is skipped entirely.
	model := NewRevenueMultipleModelWithMultiples(phase1, testLogger())

	cases := []struct {
		sic      string
		industry string
	}{
		{"3674", "MFG_SEMI"}, // SIC mapped in production, but tables nil here
		{"", "TECH_SAAS"},    // empty SIC
		{"1234", "HEALTH"},   // unmapped SIC
		{"9999", "UNKNOWN"},  // falls to default
	}
	for _, c := range cases {
		mult, source, _ := model.resolveMultiple(c.sic, c.industry)
		legacy := model.getMultiple(c.industry)
		if math.Float64bits(mult) != math.Float64bits(legacy) {
			t.Errorf("sic=%q industry=%q: resolveMultiple=%v (bits %x) != getMultiple=%v (bits %x)",
				c.sic, c.industry, mult, math.Float64bits(mult), legacy, math.Float64bits(legacy))
		}
		if source != "sector-bucket" {
			t.Errorf("sic=%q industry=%q: source = %q, want sector-bucket (nil tables)",
				c.sic, c.industry, source)
		}
	}
}

// TestRevenueMultipleModel_Calculate_DamodaranSIC is the end-to-end pin: a
// mapped SIC drives the Damodaran multiple into EV and stamps MultipleSource +
// the multiple_source: warning line.
func TestRevenueMultipleModel_Calculate_DamodaranSIC(t *testing.T) {
	phase1 := map[string]float64{"default": 2.0, "MFG_SEMI": 6.5}
	damodaran := map[string]float64{"Semiconductor": 15.7006}
	xwalk := map[string]string{"3674": "Semiconductor"}
	model := NewRevenueMultipleModelWithDamodaran(phase1, damodaran, xwalk, "2026-01-01", testLogger())

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "MXL",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Revenue:                   1000000000,
					OperatingIncome:           -50000000,
					NormalizedOperatingIncome: -50000000,
					FilingDate:                time.Now(),
					FilingPeriod:              "2023FY",
				},
			},
		},
		Industry:          "MFG_SEMI",
		SICCode:           "3674",
		SharesOutstanding: 100000000,
	}

	result, err := model.Calculate(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// EV = 1B * 15.7006 (Damodaran), NOT 1B * 6.5 (Phase 1 bucket).
	assert.InDelta(t, 15.7006e9, result.EnterpriseValue, 1.0)
	assert.Equal(t, "Damodaran 2026-01-01", result.MultipleSource)
	assertHasWarningPrefix(t, result.Warnings, "multiple_source: Damodaran 2026-01-01")
	// The audit "Applied" line names the matched Damodaran industry alongside the
	// classifier code, so the 15.7x is not misread as the MFG_SEMI bucket value.
	assertHasWarningPrefix(t, result.Warnings, "Applied 15.7x EV/Revenue multiple for MFG_SEMI (Damodaran: Semiconductor) sector")
}

// TestRevenueMultipleModel_Calculate_UnmappedSIC_SectorBucket confirms the
// fallback path stamps "sector-bucket" and uses the Phase 1 multiple.
func TestRevenueMultipleModel_Calculate_UnmappedSIC_SectorBucket(t *testing.T) {
	phase1 := map[string]float64{"default": 2.0, "MFG_SEMI": 6.5}
	damodaran := map[string]float64{"Semiconductor": 15.7006}
	xwalk := map[string]string{"3674": "Semiconductor"}
	model := NewRevenueMultipleModelWithDamodaran(phase1, damodaran, xwalk, "2026-01-01", testLogger())

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "FOO",
			Data: map[string]*entities.FinancialData{
				"2023FY": {
					Revenue:                   1000000000,
					OperatingIncome:           -50000000,
					NormalizedOperatingIncome: -50000000,
					FilingDate:                time.Now(),
					FilingPeriod:              "2023FY",
				},
			},
		},
		Industry:          "MFG_SEMI",
		SICCode:           "1234", // unmapped
		SharesOutstanding: 100000000,
	}

	result, err := model.Calculate(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// EV = 1B * 6.5 (Phase 1 bucket).
	assert.InDelta(t, 6.5e9, result.EnterpriseValue, 1.0)
	assert.Equal(t, "sector-bucket", result.MultipleSource)
	assertHasWarningPrefix(t, result.Warnings, "multiple_source: sector-bucket")
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
			// AAPL-shaped fixture: latest year 2025 has only Q1, prior year
			// 2024 supplies Q2+Q3+Q4. With the bridge running first in the
			// fallback chain, this routes to TTM_PRIOR_BRIDGE — the numeric
			// answer is identical to what TTM_4Q would have produced on the
			// same data; the source string preserves the audit-trail signal
			// that the latest year is partial.
			name: "T9_AAPL_FY_plus_Q1_with_prior_quarters_uses_TTM_PRIOR_BRIDGE",
			data: map[string]*entities.FinancialData{
				"2024Q1": {Revenue: 90, FilingPeriod: "2024Q1", FilingDate: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)},
				"2024Q2": {Revenue: 95, FilingPeriod: "2024Q2", FilingDate: time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)},
				"2024Q3": {Revenue: 100, FilingPeriod: "2024Q3", FilingDate: time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC)},
				"2024Q4": {Revenue: 110, FilingPeriod: "2024Q4", FilingDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
				"2024FY": {Revenue: 395, FilingPeriod: "2024FY", FilingDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
				"2025Q1": {Revenue: 105, FilingPeriod: "2025Q1", FilingDate: time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)},
			},
			// Bridge: 2025Q1 (105) + 2024Q2 (95) + 2024Q3 (100) + 2024Q4 (110) = 410.
			expectRevenueInEV: 410,
			expectSourceTag:   "source=TTM_PRIOR_BRIDGE",
			expectWarnSub:     "partial-year TTM bridged",
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

// TestRevenueMultipleModel_Calculate_StaleData_Warning verifies the RM-1.A
// resolution: when the latest filing is >= 18 months old (relative to the
// injected clock), the model emits a "revenue_base: data is N months old"
// warning. The check lives at the consumer (this model) rather than the
// entity-layer TTM helper so the entity package stays clock-free and replay
// determinism is preserved through the same Clock seam used by *Service.
//
// The check is orthogonal to the source-path classification: it fires
// regardless of which fallback path (TTM_4Q / TTM_PRIOR_BRIDGE / ANNUAL_FY /
// ANNUALIZED_QUARTER) won, because staleness is a property of the underlying
// filing date, not the synthesis algorithm.
func TestRevenueMultipleModel_Calculate_StaleData_Warning(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	// "Now" pinned to 2026-05-11; latest filing date set 20 months earlier
	// (2024-09-11) so monthsOld = 20 >= 18 — staleness threshold crossed.
	pinnedNow := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	staleFilingDate := time.Date(2024, 9, 11, 0, 0, 0, 0, time.UTC)

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "STALE_TICKER",
			Data: map[string]*entities.FinancialData{
				"2024FY": {
					Revenue:      1_000_000_000,
					FilingPeriod: "2024FY",
					FilingDate:   staleFilingDate,
				},
			},
		},
		Industry:          "TECH",
		SharesOutstanding: 100_000_000,
		Now:               func() time.Time { return pinnedNow },
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Spec format: "revenue_base: data is N months old". N=20 for this fixture.
	expected := "revenue_base: data is 20 months old"
	found := false
	for _, w := range result.Warnings {
		if w == expected {
			found = true
			break
		}
	}
	assert.True(t, found,
		"expected stale-data warning %q in warnings %v", expected, result.Warnings)
}

// TestRevenueMultipleModel_Calculate_StaleData_BoundaryAt18Months verifies
// the staleness threshold uses >= 18 months (inclusive). Exactly 18 months
// old fires the warning; 17 months stays silent.
func TestRevenueMultipleModel_Calculate_StaleData_BoundaryAt18Months(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	pinnedNow := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		filingDate  time.Time
		expectStale bool
		expectMsg   string
	}{
		{
			name:        "exactly_18_months_old_fires",
			filingDate:  time.Date(2024, 11, 11, 0, 0, 0, 0, time.UTC),
			expectStale: true,
			expectMsg:   "revenue_base: data is 18 months old",
		},
		{
			name:        "17_months_old_silent",
			filingDate:  time.Date(2024, 12, 11, 0, 0, 0, 0, time.UTC),
			expectStale: false,
		},
		{
			name:        "fresh_data_silent",
			filingDate:  time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			expectStale: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := &ModelInput{
				HistoricalData: &entities.HistoricalFinancialData{
					Ticker: "BOUNDARY",
					Data: map[string]*entities.FinancialData{
						"2024FY": {
							Revenue:      1_000_000_000,
							FilingPeriod: "2024FY",
							FilingDate:   tt.filingDate,
						},
					},
				},
				Industry:          "TECH",
				SharesOutstanding: 100_000_000,
				Now:               func() time.Time { return pinnedNow },
			}

			result, err := model.Calculate(ctx, input)
			require.NoError(t, err)
			require.NotNil(t, result)

			hasStaleWarn := false
			for _, w := range result.Warnings {
				if strings.HasPrefix(w, "revenue_base: data is ") &&
					strings.HasSuffix(w, " months old") {
					hasStaleWarn = true
					if tt.expectStale {
						assert.Equal(t, tt.expectMsg, w)
					}
					break
				}
			}
			assert.Equal(t, tt.expectStale, hasStaleWarn,
				"stale warning presence; warnings=%v", result.Warnings)
		})
	}
}

// TestRevenueMultipleModel_Calculate_StaleData_NilNowFallsBackToWallClock
// verifies that when the caller omits the Now func (production path before
// the Service plumbs it, or any test that doesn't care about staleness), the
// model falls back to time.Now and does not panic. Asserts the function
// does not error and does not introduce a spurious stale warning for a
// fixture dated "now" (computed via the same wall clock).
func TestRevenueMultipleModel_Calculate_StaleData_NilNowFallsBackToWallClock(t *testing.T) {
	model := newTestRevenueMultipleModel()
	ctx := context.Background()

	input := &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "NIL_NOW",
			Data: map[string]*entities.FinancialData{
				"2025FY": {
					Revenue:      1_000_000_000,
					FilingPeriod: "2025FY",
					FilingDate:   time.Now(), // fresh as of test execution
				},
			},
		},
		Industry:          "TECH",
		SharesOutstanding: 100_000_000,
		// Now intentionally left nil.
	}

	result, err := model.Calculate(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	for _, w := range result.Warnings {
		assert.False(t,
			strings.HasPrefix(w, "revenue_base: data is ") && strings.HasSuffix(w, " months old"),
			"fresh fixture should not emit a stale warning; got %q", w)
	}
}

// TestMonthsSince_EdgeBranches exercises the small set of guard branches in
// the monthsSince helper that the higher-level Calculate tests don't reach:
// zero filing date, future filing date, and the day-of-month adjustment that
// keeps the 18-month threshold from firing at 17.x months.
func TestMonthsSince_EdgeBranches(t *testing.T) {
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return now }

	tests := []struct {
		name       string
		filingDate time.Time
		want       int
	}{
		{
			name:       "zero_filing_date_returns_zero",
			filingDate: time.Time{},
			want:       0,
		},
		{
			name:       "future_filing_date_returns_zero",
			filingDate: now.Add(30 * 24 * time.Hour),
			want:       0,
		},
		{
			name:       "same_instant_returns_zero",
			filingDate: now,
			want:       0,
		},
		{
			// 2024-12-15 → 2026-05-11: raw delta is (2026-2024)*12 + (5-12) = 17,
			// then day-of-month adjustment fires because 11 < 15, decrementing
			// to 16. Without the adjustment we'd over-report by a month at the
			// 18-month threshold.
			name:       "day_of_month_adjustment_decrements",
			filingDate: time.Date(2024, 12, 15, 0, 0, 0, 0, time.UTC),
			want:       16,
		},
		{
			// 2024-12-01 → 2026-05-11: raw delta is 17 months and no
			// day-of-month adjustment fires (11 >= 1).
			name:       "day_of_month_no_adjustment",
			filingDate: time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
			want:       17,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := monthsSince(tt.filingDate, nowFn)
			assert.Equal(t, tt.want, got)
		})
	}
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

// buildMXLLikeInput assembles a ModelInput approximating MXL (negative-OI
// cyclical-trough semi) for RM-3 forward-path testing. Synthetic but
// representative of the trough shape: TTM revenue ~$560M, 5y revenue mean
// ~$822M, OI ~-$50M. Mirrors the canonical fixture in
// profile/testhelpers/fixtures.go::BuildMXLModelInput; duplicated inline
// because testhelpers imports the models package (testhelpers → models →
// testhelpers would cycle for tests inside `package models`).
func buildMXLLikeInput(t *testing.T) *ModelInput {
	t.Helper()
	latest := &entities.FinancialData{
		Ticker:                    "MXL",
		Revenue:                   560_000_000,
		OperatingIncome:           -50_000_000,
		NormalizedOperatingIncome: -50_000_000,
		NetIncome:                 -75_000_000,
		InterestBearingDebt:       151_000_000,
		CashAndCashEquivalents:    61_000_000,
		StockholdersEquity:        300_000_000,
		TaxRate:                   0.21,
		FilingPeriod:              "2026FY",
		FilingDate:                time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		AsOf:                      time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	// Newest-first revenue history: 2026FY=560M, 2025FY=800M, 2024FY=1200M,
	// 2023FY=950M, 2022FY=600M. 5y mean = 822M (the cyclical-trough floor).
	revenueHistory := []float64{560e6, 800e6, 1200e6, 950e6, 600e6}
	data := make(map[string]*entities.FinancialData, len(revenueHistory))
	for i, rev := range revenueHistory {
		year := 2026 - i
		clone := *latest
		clone.Revenue = rev
		clone.AsOf = time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
		clone.FilingDate = time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
		clone.FilingPeriod = fyKey(year)
		data[fyKey(year)] = &clone
	}
	return &ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "MXL",
			Data:   data,
		},
		GrowthEstimate: &entities.GrowthEstimate{
			ProjectedGrowthRates: []float64{0.50, 0.50, 0.41, 0.33, 0.25, 0.16, 0.08},
			TerminalGrowthRate:   0.03,
			Confidence:           "high",
		},
		Industry:               "MFG_SEMI",
		WACC:                   0.19,
		CostOfEquity:           0.21,
		TaxRate:                0.21,
		SharesOutstanding:      82_000_000,
		InterestBearingDebt:    151_000_000,
		CashAndCashEquivalents: 61_000_000,
		Now:                    func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	}
}

func fyKey(year int) string {
	// Tiny inline formatter to avoid pulling strconv just for tests.
	buf := []byte{0, 0, 0, 0, 'F', 'Y'}
	buf[0] = byte('0' + (year/1000)%10)
	buf[1] = byte('0' + (year/100)%10)
	buf[2] = byte('0' + (year/10)%10)
	buf[3] = byte('0' + year%10)
	return string(buf)
}

// TestRevenueMultiple_Forward_ProjectsAtHorizon verifies the RM-3 forward
// path: when Profile.HorizonYears > 0 and CostOfEquity > 0, the model
// computes both trailing and forward values, surfaces HorizonSelected /
// TerminalMultiple, and remains a positive per-share value for the MXL
// cyclical-trough shape. Spec §6.1.
func TestRevenueMultiple_Forward_ProjectsAtHorizon(t *testing.T) {
	input := buildMXLLikeInput(t)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:         "cyclical_trough:standard_growth",
			Archetype:         profile.ArchetypeCyclicalTrough,
			Maturity:          profile.MaturityStandardGrowth,
			HorizonYears:      5,
			CompoundGrowthCap: 3.0,
			RevenueBaseMethod: profile.RevenueBaseMaxTTMOrFloor,
			TerminalMultiple:  4.0,
			TerminalMethod:    profile.TerminalExitMultiple,
			DiscountMethod:    profile.DiscountCostOfEquity,
		},
	}

	rm := NewRevenueMultipleModelWithMultiples(map[string]float64{
		"default":  2.0,
		"MFG_SEMI": 1.5,
		"MFG":      1.5,
	}, testLogger())
	result, err := rm.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.TrailingValue, 0.0, "trailing always computed")
	assert.Greater(t, result.ForwardValue, 0.0, "forward computed when horizon > 0")
	assert.Equal(t, 5, result.HorizonSelected)
	assert.InEpsilon(t, 4.0, result.TerminalMultiple, 1e-9)
	// RM-3 forward warning summary should mention horizon + avg growth + multiple.
	foundForwardWarn := false
	for _, w := range result.Warnings {
		if strings.HasPrefix(w, "RM-3 forward:") {
			foundForwardWarn = true
			break
		}
	}
	assert.True(t, foundForwardWarn, "expected RM-3 forward warning; got %v", result.Warnings)
}

// TestRevenueMultiple_NilProfile_FallsThroughToTrailing verifies that the
// trailing path is preserved bit-for-bit when no profile is wired (legacy
// test call sites). The new fields stay zero so they are omitted from the
// JSON response under omitempty.
func TestRevenueMultiple_NilProfile_FallsThroughToTrailing(t *testing.T) {
	input := buildMXLLikeInput(t)
	input.Profile = nil

	rm := NewRevenueMultipleModelWithMultiples(map[string]float64{
		"default":  2.0,
		"MFG_SEMI": 1.5,
		"MFG":      1.5,
	}, testLogger())
	result, err := rm.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.TrailingValue, 0.0)
	assert.Equal(t, 0.0, result.ForwardValue)
	assert.Equal(t, 0, result.HorizonSelected)
	assert.Equal(t, 0.0, result.TerminalMultiple)
	// IntrinsicValuePerShare equals the trailing path output, so it must
	// match TrailingValue exactly (no forward blend on the nil-profile path).
	assert.Equal(t, result.TrailingValue, result.IntrinsicValuePerShare)
}

// TestRevenueMultiple_ProfileHorizonZero_BehavesLikeNoProfile verifies the
// HorizonYears == 0 gate. A profile with zero horizon is the bit-for-bit
// preservation signal: trailing-only behavior, no forward computation.
func TestRevenueMultiple_ProfileHorizonZero_BehavesLikeNoProfile(t *testing.T) {
	input := buildMXLLikeInput(t)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{HorizonYears: 0},
	}

	rm := NewRevenueMultipleModelWithMultiples(map[string]float64{
		"default":  2.0,
		"MFG_SEMI": 1.5,
		"MFG":      1.5,
	}, testLogger())
	result, err := rm.Calculate(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0.0, result.ForwardValue)
	assert.Equal(t, 0, result.HorizonSelected)
}

// TestRevenueMultiple_Forward_InsufficientGrowthRates verifies the safety
// gate: when ProjectedGrowthRates has fewer entries than HorizonYears, the
// forward path is skipped (no partial projection) and trailing is preserved.
func TestRevenueMultiple_Forward_InsufficientGrowthRates(t *testing.T) {
	input := buildMXLLikeInput(t)
	input.GrowthEstimate = &entities.GrowthEstimate{
		ProjectedGrowthRates: []float64{0.10, 0.10}, // only 2 of 5 required
		TerminalGrowthRate:   0.03,
		Confidence:           "medium",
	}
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:         "cyclical_trough:standard_growth",
			Archetype:         profile.ArchetypeCyclicalTrough,
			Maturity:          profile.MaturityStandardGrowth,
			HorizonYears:      5,
			CompoundGrowthCap: 3.0,
			RevenueBaseMethod: profile.RevenueBaseRawTTM,
			TerminalMultiple:  4.0,
			TerminalMethod:    profile.TerminalExitMultiple,
			DiscountMethod:    profile.DiscountCostOfEquity,
		},
	}
	rm := NewRevenueMultipleModelWithMultiples(map[string]float64{
		"default":  2.0,
		"MFG_SEMI": 1.5,
		"MFG":      1.5,
	}, testLogger())
	result, err := rm.Calculate(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0.0, result.ForwardValue, "no partial projection when growth-rates < horizon")
	assert.Equal(t, 0, result.HorizonSelected)
}

// TestRevenueMultiple_NormalizeRevenueBase_AllMethods covers the four
// RevenueBaseMethod enum values exercised by the helper. Uses a stable
// 5-period annual history so callers can pin the expected outputs.
func TestRevenueMultiple_NormalizeRevenueBase_AllMethods(t *testing.T) {
	// Newest-first revenue history. The helper consumes newest-first via
	// the entity API; this fixture pins what each method should select.
	ttm := 500.0
	histData := map[string]*entities.FinancialData{
		"2026FY": {Revenue: 500, FilingPeriod: "2026FY", FilingDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)},
		"2025FY": {Revenue: 800, FilingPeriod: "2025FY", FilingDate: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)},
		"2024FY": {Revenue: 1200, FilingPeriod: "2024FY", FilingDate: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)},
		"2023FY": {Revenue: 1000, FilingPeriod: "2023FY", FilingDate: time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)},
		"2022FY": {Revenue: 600, FilingPeriod: "2022FY", FilingDate: time.Date(2022, 12, 31, 0, 0, 0, 0, time.UTC)},
	}
	hist := &entities.HistoricalFinancialData{Ticker: "T", Data: histData}

	// 5y mean = (500+800+1200+1000+600)/5 = 820.
	// 2y mean (newest two annuals) = (500+800)/2 = 650.
	tests := []struct {
		name   string
		method profile.RevenueBaseMethod
		want   float64
	}{
		{"raw_ttm", profile.RevenueBaseRawTTM, ttm},
		{"two_year_average", profile.RevenueBaseTwoYearAverage, 650.0},
		{"max_ttm_or_floor_picks_floor", profile.RevenueBaseMaxTTMOrFloor, 820.0},
		{"mid_cycle_normalized", profile.RevenueBaseMidCycleNormalized, 820.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeRevenueBase(ttm, tt.method, hist)
			assert.InEpsilon(t, tt.want, got, 1e-9)
		})
	}

	// Defensive branches: nil hist + empty hist should not panic, both
	// fall back to the TTM input for methods that depend on history.
	assert.Equal(t, ttm, normalizeRevenueBase(ttm, profile.RevenueBaseTwoYearAverage, nil))
	emptyHist := &entities.HistoricalFinancialData{Ticker: "T", Data: map[string]*entities.FinancialData{}}
	assert.Equal(t, ttm, normalizeRevenueBase(ttm, profile.RevenueBaseTwoYearAverage, emptyHist))
	// mid_cycle on empty history returns 0 (the helper's "no signal" output);
	// callers that gate on history sufficiency must do so themselves.
	assert.Equal(t, 0.0, normalizeRevenueBase(ttm, profile.RevenueBaseMidCycleNormalized, emptyHist))
	// max_ttm_or_floor on empty history: floor=0, ttm=500 => returns ttm.
	assert.Equal(t, ttm, normalizeRevenueBase(ttm, profile.RevenueBaseMaxTTMOrFloor, emptyHist))
}

// TestRevenueMultiple_MeanRecentRevenue_BoundedByHistoryLength asserts that
// when fewer than `years` annuals exist, the helper averages whatever it has
// rather than dividing by `years` (which would understate the mean).
func TestRevenueMultiple_MeanRecentRevenue_BoundedByHistoryLength(t *testing.T) {
	hist := &entities.HistoricalFinancialData{
		Ticker: "T",
		Data: map[string]*entities.FinancialData{
			"2026FY": {Revenue: 100, FilingPeriod: "2026FY", FilingDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)},
			"2025FY": {Revenue: 200, FilingPeriod: "2025FY", FilingDate: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)},
		},
	}
	// Asking for 5y mean against only 2 periods: (100+200)/2 = 150.
	got := meanRecentRevenue(hist, 5)
	assert.InEpsilon(t, 150.0, got, 1e-9)

	// nil hist returns 0 (defensive).
	assert.Equal(t, 0.0, meanRecentRevenue(nil, 5))
}

// TestAvg_EmptyAndNonEmpty pins the small helper. Empty slice returns 0
// (no division-by-zero); non-empty returns the arithmetic mean.
func TestAvg_EmptyAndNonEmpty(t *testing.T) {
	assert.Equal(t, 0.0, avg(nil))
	assert.Equal(t, 0.0, avg([]float64{}))
	assert.InEpsilon(t, 2.0, avg([]float64{1, 2, 3}), 1e-9)
	assert.InEpsilon(t, 0.5, avg([]float64{0.5}), 1e-9)
}
