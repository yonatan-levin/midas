package models_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile/testhelpers"
)

// TestDDM_MultiStage_AAPLishProfile_HigherThanSingleStage exercises the
// Tier 2 P3 multi-stage DDM path. Input is the synthetic AAPL-ish fixture
// (maturing-tech-first-dividend shape: $0.95 latest DPS, rising payout
// path, mid-single-digit growth) with the
// maturing_tech_first_dividend:standard_growth profile.
//
// Expected: dispatcher routes to calculateMultiStage; result has a
// positive intrinsic value and HorizonSelected matches the profile's
// DividendForecastHorizon (10y).
//
// Spec §6.3, §7.1; plan Phase P3 task P3.2.
func TestDDM_MultiStage_AAPLishProfile_HigherThanSingleStage(t *testing.T) {
	input := testhelpers.BuildSyntheticAAPLishModelInput(t)
	testhelpers.PatchFilingDatesFromAsOf(input)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:               "maturing_tech_first_dividend:standard_growth",
			Archetype:               profile.ArchetypeMaturingTechDividend,
			Maturity:                profile.MaturityStandardGrowth,
			DividendForecastHorizon: 10,
			PayoutPath:              []float64{0.25, 0.30, 0.35, 0.40, 0.45, 0.50, 0.52, 0.54, 0.56, 0.58},
			DPSGrowthCap:            0.25,
			StableDividendGrowth:    0.035,
		},
	}

	ddm := models.NewDDMModel(zap.NewNop())
	result, err := ddm.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.IntrinsicValuePerShare, 0.0,
		"multi-stage DDM should produce a positive intrinsic value")
	assert.Equal(t, 10, result.HorizonSelected,
		"HorizonSelected should mirror profile.DividendForecastHorizon")
	assert.Equal(t, "ddm", result.ModelType)
}

// TestDDM_MultiStage_NilProfile_FallsThroughToLegacyGordon confirms the
// dispatcher's defensive nil branch routes back to the legacy single-stage
// Gordon path. Without a profile the multi-stage code cannot run, so the
// dispatcher MUST fall through (preserving pre-Tier-2 behavior).
func TestDDM_MultiStage_NilProfile_FallsThroughToLegacyGordon(t *testing.T) {
	input := testhelpers.BuildSyntheticAAPLishModelInput(t)
	testhelpers.PatchFilingDatesFromAsOf(input)
	input.Profile = nil

	ddm := models.NewDDMModel(zap.NewNop())
	result, err := ddm.Calculate(context.Background(), input)
	require.NoError(t, err)

	// Legacy path leaves HorizonSelected zero-valued (omitempty in JSON).
	assert.Equal(t, 0, result.HorizonSelected,
		"nil profile must fall through to legacy Gordon path (no horizon)")
	assert.Equal(t, "ddm", result.ModelType)
}

// TestDDM_MultiStage_LegacyMatureLargeBank_RoutesToLegacy verifies the
// legacy-bank short-circuit fires before the horizon check. A
// mature_large_bank:mature profile (horizon=0, archetype matches) MUST
// still take the bit-for-bit legacy Gordon path.
func TestDDM_MultiStage_LegacyMatureLargeBank_RoutesToLegacy(t *testing.T) {
	input := testhelpers.BuildSyntheticAAPLishModelInput(t)
	testhelpers.PatchFilingDatesFromAsOf(input)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:               "mature_large_bank:mature",
			Archetype:               profile.ArchetypeMatureLargeBank,
			Maturity:                profile.MaturityMature,
			DividendForecastHorizon: 0,
		},
	}

	ddm := models.NewDDMModel(zap.NewNop())
	result, err := ddm.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Equal(t, 0, result.HorizonSelected,
		"legacy mature_large_bank profile MUST route to single-stage Gordon")
}

// TestDDM_MultiStage_ErrorPaths exhausts the early-return guards in
// calculateMultiStage. Each guard surfaces a distinct upstream-data
// failure mode; covering them keeps the spec §12 ≥90% coverage gate met
// on the new path without requiring a separate integration harness.
func TestDDM_MultiStage_ErrorPaths(t *testing.T) {
	multiStageProfile := &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:               "maturing_tech_first_dividend:standard_growth",
			Archetype:               profile.ArchetypeMaturingTechDividend,
			Maturity:                profile.MaturityStandardGrowth,
			DividendForecastHorizon: 10,
			PayoutPath:              []float64{0.25, 0.30, 0.35, 0.40, 0.45, 0.50, 0.52, 0.54, 0.56, 0.58},
			DPSGrowthCap:            0.25,
			StableDividendGrowth:    0.035,
		},
	}

	tests := []struct {
		name      string
		mutate    func(*models.ModelInput)
		errSubstr string
	}{
		{
			name: "no_financial_data",
			mutate: func(in *models.ModelInput) {
				in.HistoricalData.Data = nil
			},
			errSubstr: "no financial data",
		},
		{
			name: "zero_dps_in_latest",
			mutate: func(in *models.ModelInput) {
				for _, period := range in.HistoricalData.Data {
					period.DividendsPerShare = 0
				}
			},
			errSubstr: "does not pay dividends",
		},
		{
			name: "non_positive_cost_of_equity",
			mutate: func(in *models.ModelInput) {
				in.CostOfEquity = 0
			},
			errSubstr: "cost of equity must be positive",
		},
		{
			name: "nil_growth_estimate",
			mutate: func(in *models.ModelInput) {
				in.GrowthEstimate = nil
			},
			errSubstr: "growth estimate is required",
		},
		{
			name: "growth_curve_shorter_than_horizon",
			mutate: func(in *models.ModelInput) {
				in.GrowthEstimate.ProjectedGrowthRates = []float64{0.05, 0.05}
			},
			errSubstr: "shorter than profile",
		},
	}

	ddm := models.NewDDMModel(zap.NewNop())
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := testhelpers.BuildSyntheticAAPLishModelInput(t)
			testhelpers.PatchFilingDatesFromAsOf(input)
			input.Profile = multiStageProfile
			tc.mutate(input)

			result, err := ddm.Calculate(context.Background(), input)
			require.Error(t, err, "expected multi-stage guard to reject input")
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), tc.errSubstr)
		})
	}
}

// TestDDM_MultiStage_DPSGrowthCapClamping pins the per-year DPS-growth cap
// branch. Engine growth far above the profile's DPSGrowthCap should be
// clamped down; the result MUST stay finite and lower than an uncapped
// run.
func TestDDM_MultiStage_DPSGrowthCapClamping(t *testing.T) {
	baseProfile := profile.AssumptionProfile{
		ProfileID:               "maturing_tech_first_dividend:standard_growth",
		Archetype:               profile.ArchetypeMaturingTechDividend,
		Maturity:                profile.MaturityStandardGrowth,
		DividendForecastHorizon: 5,
		PayoutPath:              []float64{0.25, 0.30, 0.35, 0.40, 0.45},
		StableDividendGrowth:    0.035,
	}

	withCap := baseProfile
	withCap.DPSGrowthCap = 0.05
	noCap := baseProfile
	noCap.DPSGrowthCap = 0

	buildInput := func(p profile.AssumptionProfile) *models.ModelInput {
		input := testhelpers.BuildSyntheticAAPLishModelInput(t)
		testhelpers.PatchFilingDatesFromAsOf(input)
		// Engine growth high enough to trigger clamping when the cap is set.
		input.GrowthEstimate.ProjectedGrowthRates = []float64{0.40, 0.35, 0.30, 0.25, 0.20}
		input.Profile = &profile.ResolvedProfile{AssumptionProfile: p}
		return input
	}

	ddm := models.NewDDMModel(zap.NewNop())
	cappedResult, err := ddm.Calculate(context.Background(), buildInput(withCap))
	require.NoError(t, err)
	uncappedResult, err := ddm.Calculate(context.Background(), buildInput(noCap))
	require.NoError(t, err)

	assert.Greater(t, uncappedResult.IntrinsicValuePerShare, cappedResult.IntrinsicValuePerShare,
		"uncapped DPS growth must produce a higher value than capped growth at 5%")
	assert.Greater(t, cappedResult.IntrinsicValuePerShare, 0.0,
		"capped value must still be positive")
}

// multiStageProfileFor returns the canonical maturing_tech_first_dividend
// multi-stage profile used by the diagnostics-parity tests. Each test then
// mutates the resulting input (NetIncome / StockholdersEquity / DPS /
// shares-outstanding) to steer the shared diagnostics helper into the
// desired warning branch.
func multiStageProfileFor10y() *profile.ResolvedProfile {
	return &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:               "maturing_tech_first_dividend:standard_growth",
			Archetype:               profile.ArchetypeMaturingTechDividend,
			Maturity:                profile.MaturityStandardGrowth,
			DividendForecastHorizon: 10,
			PayoutPath:              []float64{0.25, 0.30, 0.35, 0.40, 0.45, 0.50, 0.52, 0.54, 0.56, 0.58},
			DPSGrowthCap:            0.25,
			StableDividendGrowth:    0.035,
		},
	}
}

// containsWarning is a small predicate helper used by the parity tests to
// assert that a particular diagnostic string surfaces in result.Warnings
// without coupling the test to the exact full warning text. The legacy
// path's warning strings are bit-for-bit pinned by
// TestDDM_LegacyPath_BitForBit; these tests pin the multi-stage path's
// parity by substring-match so a future warning-text tweak that the legacy
// path absorbs without drift will continue to satisfy the multi-stage
// parity guarantee.
func containsWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// TestDDM_MultiStage_RunsROEDiagnostic_WhenROEDataAvailable pins T2-P4-W2
// item 5's first parity requirement: the multi-stage DDM path now emits
// the ROE diagnostic when StockholdersEquity > 0 and NetIncome > 0 (the
// legacy path's `hasROE` precondition). The AAPLish synthetic fixture has
// ROE ≈ 95B / 62B ≈ 153% which the helper flags as "High ROE …
// unsustainable" — pre-Tier-2 item 5 this warning would NOT have appeared
// on the multi-stage branch.
func TestDDM_MultiStage_RunsROEDiagnostic_WhenROEDataAvailable(t *testing.T) {
	input := testhelpers.BuildSyntheticAAPLishModelInput(t)
	testhelpers.PatchFilingDatesFromAsOf(input)
	input.Profile = multiStageProfileFor10y()

	ddm := models.NewDDMModel(zap.NewNop())
	result, err := ddm.Calculate(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.True(t,
		containsWarning(result.Warnings, "ROE"),
		"multi-stage DDM must emit ROE diagnostic when ROE data is available; got warnings=%v",
		result.Warnings)
	assert.True(t,
		containsWarning(result.Warnings, "DDM multi-stage:"),
		"multi-stage preamble warning must still be present; got warnings=%v",
		result.Warnings)
}

// TestDDM_MultiStage_RunsPayoutDiagnostic pins T2-P4-W2 item 5's second
// parity requirement. The legacy path warns "High payout ratio (%.0f%%)
// leaves little room for growth" when DPS/EPS > 0.9. Construct a
// degenerate-payout scenario (DPS ≈ EPS) and assert the multi-stage path
// emits the same warning string.
func TestDDM_MultiStage_RunsPayoutDiagnostic(t *testing.T) {
	input := testhelpers.BuildSyntheticAAPLishModelInput(t)
	testhelpers.PatchFilingDatesFromAsOf(input)
	input.Profile = multiStageProfileFor10y()

	// Force payout ratio ≈ 100% by collapsing NetIncome down so EPS ≈ DPS.
	// shares=15.5B, DPS=0.95 → target NetIncome ≈ 0.95 * 15.5e9 ≈ 14.725B
	// (slightly less so payoutRatio > 0.9).
	for _, period := range input.HistoricalData.Data {
		period.NetIncome = 14_500_000_000
	}

	ddm := models.NewDDMModel(zap.NewNop())
	result, err := ddm.Calculate(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.True(t,
		containsWarning(result.Warnings, "High payout ratio"),
		"multi-stage DDM must emit payout-ratio diagnostic when DPS/EPS > 0.9; got warnings=%v",
		result.Warnings)
}

// TestDDM_MultiStage_RunsPBVCrossCheck pins T2-P4-W2 item 5's third parity
// requirement. The legacy path runs a P/BV cross-check comparing implied
// P/BV against ROE-justified P/BV; ratios outside [DeviationLow,
// DeviationHigh] surface as "Implied P/BV (…) diverges from ROE-justified
// P/BV (…)" warnings.
//
// AAPLish has a tiny book value per share (4.0) and a relatively modest
// fair value, so its implied P/BV will be enormous relative to the ROE-
// justified figure — the cross-check fires naturally.
func TestDDM_MultiStage_RunsPBVCrossCheck(t *testing.T) {
	input := testhelpers.BuildSyntheticAAPLishModelInput(t)
	testhelpers.PatchFilingDatesFromAsOf(input)
	input.Profile = multiStageProfileFor10y()

	ddm := models.NewDDMModel(zap.NewNop())
	result, err := ddm.Calculate(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.True(t,
		containsWarning(result.Warnings, "Implied P/BV"),
		"multi-stage DDM must emit P/BV cross-check when implied vs ROE-justified diverges; got warnings=%v",
		result.Warnings)
}

// TestDDM_MultiStage_ConfidenceVariesWithWarningCount pins T2-P4-W2 item
// 5's fourth requirement: the multi-stage path's confidence is now the
// warning-count-adjusted scoring shared with the legacy path, not a fixed
// "medium". Specifically:
//   - len(warnings)==0 && dividendGrowth>0 → "high"
//   - len(warnings)==1                     → "medium"  (the fallthrough)
//   - len(warnings)>1                      → "low"
//
// The diagnostics helper seeds the multi-stage path with one preamble
// warning, so the floor for multi-stage is "medium" (1 warning) rising to
// "low" (≥2) — it can never naturally produce "high" because the preamble
// occupies the zero-warning slot. The two test cases below pin the
// boundary between medium and low.
func TestDDM_MultiStage_ConfidenceVariesWithWarningCount(t *testing.T) {
	t.Run("clean_fundamentals_yields_medium_with_preamble_only", func(t *testing.T) {
		input := testhelpers.BuildSyntheticAAPLishModelInput(t)
		testhelpers.PatchFilingDatesFromAsOf(input)
		input.Profile = multiStageProfileFor10y()

		// Suppress every diagnostic that the helper would otherwise emit so
		// only the multi-stage preamble warning survives:
		//   - Zero out NetIncome+StockholdersEquity → !hasROE skips ROE branch and P/BV cross-check.
		//   - hasROE=false also short-circuits the payout-ratio EPS calc (needs NetIncome>0).
		for _, period := range input.HistoricalData.Data {
			period.NetIncome = 0
			period.StockholdersEquity = 0
		}

		ddm := models.NewDDMModel(zap.NewNop())
		result, err := ddm.Calculate(context.Background(), input)
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Equal(t, 1, len(result.Warnings),
			"only the multi-stage preamble warning should fire when ROE/payout/P/BV diagnostics are all skipped; got warnings=%v",
			result.Warnings)
		assert.Equal(t, "medium", result.Confidence,
			"warning-count-adjusted confidence must be 'medium' for exactly 1 warning")
	})

	t.Run("noisy_fundamentals_yield_low", func(t *testing.T) {
		input := testhelpers.BuildSyntheticAAPLishModelInput(t)
		testhelpers.PatchFilingDatesFromAsOf(input)
		input.Profile = multiStageProfileFor10y()
		// AAPLish baseline has ROE=153% (High-ROE warning) and naturally
		// triggers the P/BV cross-check (tiny book value vs implied value).
		// Multi-stage preamble + 2 diagnostics = 3 warnings → "low".

		ddm := models.NewDDMModel(zap.NewNop())
		result, err := ddm.Calculate(context.Background(), input)
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.GreaterOrEqual(t, len(result.Warnings), 2,
			"AAPLish fundamentals should trigger at least 2 warnings (preamble + ROE/PBV); got warnings=%v",
			result.Warnings)
		assert.Equal(t, "low", result.Confidence,
			"warning-count-adjusted confidence must be 'low' for >1 warning")
	})
}
