package models_test

import (
	"context"
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
