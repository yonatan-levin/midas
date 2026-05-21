package profile_test

// Tier 2 cross-model regression suite. Pins 6 fields per ticker per
// spec §8.2:
//   - assumption_profile (exact)
//   - horizon_selected (exact)
//   - chosen_model (exact)
//   - primary_value (bit-for-bit for mature_large_bank, ε=1e-9 elsewhere)
//   - trailing_value (ε=1e-9 where applicable)
//   - warning_count (exact)
//
// Populated incrementally by P1-P4 worktrees. Skeleton lands in
// Phase Bootstrap so the file exists at master HEAD before parallel
// work dispatches.
//
// Tier 2 P4 (VAL-3 P3) adds EQIX + PLD pins. Captures use the synthetic
// builders (testhelpers.BuildSyntheticDataCenterREITInput + the inline
// PLD builder in tier2_pin_inputs_test.go) and call FFOModel.Calculate
// directly — bypassing the resolver isolates FFO model drift from
// resolver / profile-row drift. The plan's Closeout Z.1 task re-pins
// against live engine output once P1-P3 all merge to master.

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile/testhelpers"
)

func TestTier2_BasketRegression(t *testing.T) {
	t.Skip("Populated by P1-P4 worktrees; per-ticker pin tests live alongside this skeleton (e.g., TestTier2_MXL_Pin)")
}

// TestTier2_MXL_Pin (Tier 2 P1, RM-3): asserts the trailing
// IntrinsicValuePerShare a RevenueMultipleModel produces for the canonical
// MXL fixture under the cyclical_trough:standard_growth profile remains
// equal to the captured baseline (profile.ExpectedMXLPrimaryValue).
//
// The pin runs at the model layer rather than via the full Service
// because testhelpers.RunValuation is a t.Skip() stub at Phase Bootstrap;
// wiring the full fx Service is out of scope for P1 (would require
// touching service.go, which is off-limits for this worktree). When a
// downstream phase wires RunValuation, this test should be extended to
// also assert AssumptionProfile == "cyclical_trough:standard_growth" and
// ChosenModel == "revenue_multiple" on the service-layer result.
//
// Regenerate ExpectedMXLPrimaryValue via:
//
//	go test -tags pincapture -run TestCapturePins \
//	  ./internal/services/valuation/profile/... -v
//
// and paste the printed value into pins.go.
func TestTier2_MXL_Pin(t *testing.T) {
	input := testhelpers.BuildMXLModelInput(t)
	// Bootstrap fixture leaves FilingDate at zero (only AsOf is set); the
	// model uses GetLatestPeriod which keys on FilingDate. Patch here so
	// the pin runs without modifying the shared fixture.
	for _, d := range input.HistoricalData.Data {
		if d.FilingDate.IsZero() {
			d.FilingDate = d.AsOf
		}
	}
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:         "cyclical_trough:standard_growth",
			Archetype:         profile.ArchetypeCyclicalTrough,
			Maturity:          profile.MaturityStandardGrowth,
			HorizonYears:      5,
			CompoundGrowthCap: 3.0,
			RevenueBaseMethod: profile.RevenueBaseMaxTTMOrFloor,
			TerminalMethod:    profile.TerminalExitMultiple,
			TerminalMultiple:  4.0,
			DiscountMethod:    profile.DiscountCostOfEquity,
		},
	}
	rm := models.NewRevenueMultipleModelWithMultiples(map[string]float64{
		"default":  2.0,
		"MFG_SEMI": 1.5,
		"MFG":      1.5,
	}, zap.NewNop())
	result, err := rm.Calculate(context.Background(), input)
	require.NoError(t, err)
	// Primary (trailing) value is the IntrinsicValuePerShare field — the
	// forward path is additive and surfaced via ForwardValue. Pin only
	// trailing here so future calibration tweaks of TerminalMultiple etc.
	// don't trip the pin for a calibration-only change.
	assert.InEpsilon(t, profile.ExpectedMXLPrimaryValue, result.IntrinsicValuePerShare, 1e-9)
	assert.InEpsilon(t, profile.ExpectedMXLPrimaryValue, result.TrailingValue, 1e-9)
	// Sanity: horizon was resolved from the profile, not zeroed out.
	assert.Equal(t, 5, result.HorizonSelected)
	assert.Equal(t, "revenue_multiple", result.ModelType)
}

// TestTier2_JPM_Pin_BitForBit is the cross-model JPM regression anchor for
// VAL-2 (spec §7.1, plan Phase P3 task P3.5). Asserts that:
//
//  1. JPM resolves to the legacy mature_large_bank:mature profile (the
//     bit-for-bit preservation key — DividendForecastHorizon=0 routes the
//     DDM dispatcher to calculateLegacyGordon).
//  2. The DDM model is selected (FIN industry prefix → DDM).
//  3. The valuation's primary value (DCFValuePerShare — the canonical
//     per-share intrinsic-value field on entities.ValuationResult, populated
//     from ModelResult.IntrinsicValuePerShare regardless of which model
//     fired) is byte-identical to the captured pre-Tier-2 golden, using
//     math.Float64bits equality.
//
// Today this test skips because testhelpers.RunValuation is a
// Bootstrap-era stub awaiting full-service wiring (tracked as a P1
// prerequisite via T2-P0b-1 follow-up + the BuildTestService QA
// recommendation). When RunValuation lands the pin runs unmodified and
// becomes the load-bearing cross-model bit-for-bit anchor.
//
// The DDM-model-level bit-for-bit invariant (the actual canary for legacy
// drift in this commit) is pinned by TestDDM_LegacyPath_BitForBit in
// internal/services/valuation/models — that test runs today against
// JPM/BAC/WFC golden fixtures and gates every commit on this branch.
func TestTier2_JPM_Pin_BitForBit(t *testing.T) {
	result := testhelpers.RunValuation(t, "JPM")
	require.NotNil(t, result, "RunValuation must return a ValuationResult once wired")

	assert.Equal(t, "mature_large_bank:mature", result.AssumptionProfile,
		"JPM must resolve to the legacy mature_large_bank:mature profile (bit-for-bit anchor)")

	// CalculationMethod stamps the selected model on ValuationResult (no
	// separate ChosenModel field exists on entities.ValuationResult; the
	// model identifier flows through CalculationMethod = "ddm" for DDM
	// runs). Asserting on the canonical field rather than a plan
	// pseudo-name keeps the pin compileable in midas's real entity shape.
	assert.Equal(t, "ddm", result.CalculationMethod,
		"JPM must route through DDM (FIN prefix → DDM)")

	expected := testhelpers.LoadGoldenJPMPrimaryValue(t)
	assert.Equal(t,
		math.Float64bits(expected),
		math.Float64bits(result.DCFValuePerShare),
		"JPM DCFValuePerShare must be bit-for-bit identical to pre-Tier-2 (Float64bits equality)")
}

// TestTier2_EQIX_Pin pins the FFO model's intrinsic value for the
// synthetic data-center REIT (EQIX-ish) at the reit_datacenter:high_growth
// profile (horizon=5, terminal=28.0).
func TestTier2_EQIX_Pin(t *testing.T) {
	input := buildEQIXPinInput(t)
	ffo := models.NewFFOModel(zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Equal(t, "reit_datacenter:high_growth", input.Profile.ProfileID,
		"pin must exercise the reit_datacenter:high_growth profile shape")
	assert.Equal(t, 5, result.HorizonSelected,
		"horizon_selected must be 5 (profile horizon_years)")
	assert.Equal(t, "ffo", result.ModelType,
		"chosen_model must be ffo for the REIT path")
	assert.InEpsilon(t, profile.ExpectedEQIXPrimaryValue, result.IntrinsicValuePerShare, 1e-9,
		"EQIX primary value must match the captured pin")
	assert.InEpsilon(t, profile.ExpectedEQIXForwardValue, result.ForwardValue, 1e-9,
		"EQIX forward value must match the captured pin")
}

// TestTier2_PLD_Pin pins the FFO model's intrinsic value for the
// synthetic industrial REIT (PLD-ish) at the
// reit_industrial:standard_growth profile (horizon=3, terminal=22.5).
func TestTier2_PLD_Pin(t *testing.T) {
	input := buildPLDPinInput(t)
	ffo := models.NewFFOModel(zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Equal(t, "reit_industrial:standard_growth", input.Profile.ProfileID,
		"pin must exercise the reit_industrial:standard_growth profile shape")
	assert.Equal(t, 3, result.HorizonSelected,
		"horizon_selected must be 3 (profile horizon_years)")
	assert.Equal(t, "ffo", result.ModelType,
		"chosen_model must be ffo for the REIT path")
	assert.InEpsilon(t, profile.ExpectedPLDPrimaryValue, result.IntrinsicValuePerShare, 1e-9,
		"PLD primary value must match the captured pin")
	assert.InEpsilon(t, profile.ExpectedPLDForwardValue, result.ForwardValue, 1e-9,
		"PLD forward value must match the captured pin")
}
