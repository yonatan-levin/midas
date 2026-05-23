//go:build pincapture

// One-shot pin-capture helper. Build-tag-gated so the default `go test ./...`
// run does NOT execute it (default build excludes it). Workflow:
//
//  1. Run: go test -tags pincapture -run TestCapturePins \
//     ./internal/services/valuation/profile/... -v
//     (or -run TestCaptureFFOPins for the P4 REIT pins)
//  2. Copy the printed `expected<Ticker>PrimaryValue` lines into pins.go.
//  3. Re-run the regression suite WITHOUT -tags pincapture; the pinned
//     constants in pins.go drive TestTier2_MXL_Pin et al.
//
// This file lives in package profile_test (external test package) so the
// build-tag separation is unambiguous: pin captures never link into the
// default test binary, and pins.go (regular .go) never participates in the
// capture run.
package profile_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile/testhelpers"
)

// TestCapturePins prints the captured IntrinsicValuePerShare for each
// pinned ticker. P1 owns MXL; later phases extend the list.
//
// We exercise the model layer directly (RevenueMultipleModel.Calculate) so
// the pin can be regenerated without standing up the full fx-wired Service.
// The full-service pin (assumption_profile + chosen_model assertions) lands
// when testhelpers.RunValuation is wired in a downstream phase.
func TestCapturePins(t *testing.T) {
	// MXL: cyclical_trough:standard_growth profile against the canonical
	// MXL fixture. Mirrors the assertion in TestTier2_MXL_Pin.
	input := testhelpers.BuildMXLModelInput(t)
	// The Bootstrap fixture leaves FilingDate at the zero value (only AsOf
	// is set), which makes GetLatestPeriod return nil and the model error
	// out with "no financial data available". Patch FilingDate from AsOf
	// via the shared helper so the latest period is discoverable, keeping
	// the Bootstrap fixture untouched.
	testhelpers.PatchFilingDatesFromAsOf(input)
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
	if err != nil {
		t.Fatalf("MXL pin capture failed: %v", err)
	}
	fmt.Printf("expectedMXLPrimaryValue = %.15g\n", result.IntrinsicValuePerShare)
	fmt.Printf("expectedMXLTrailingValue = %.15g\n", result.TrailingValue)
	fmt.Printf("expectedMXLForwardValue = %.15g\n", result.ForwardValue)
}

// TestCaptureFFOPins prints the pinned IntrinsicValuePerShare for EQIX
// (datacenter, high_growth, horizon=5) and PLD (industrial,
// standard_growth, horizon=3), captured via direct FFOModel.Calculate
// against the synthetic builders in tier2_pin_inputs_test.go.
//
// The pin uses the synthetic inputs (not a live engine basket) because
// the full testhelpers.RunValuation Service builder is still a stub at
// this commit; the plan's Closeout Z.1 task will re-pin against live
// engine output once all P1-P4 streams merge.
//
// Tier 2 P4 (VAL-3 P3 forward FFO).
func TestCaptureFFOPins(t *testing.T) {
	ctx := context.Background()
	ffo := models.NewFFOModel(zap.NewNop())

	eqixInput := buildEQIXPinInput(t)
	eqixResult, err := ffo.Calculate(ctx, eqixInput)
	require.NoError(t, err)
	fmt.Printf("ExpectedEQIXPrimaryValue = %.15g\n", eqixResult.IntrinsicValuePerShare)
	fmt.Printf("ExpectedEQIXForwardValue = %.15g\n", eqixResult.ForwardValue)

	pldInput := buildPLDPinInput(t)
	pldResult, err := ffo.Calculate(ctx, pldInput)
	require.NoError(t, err)
	fmt.Printf("ExpectedPLDPrimaryValue = %.15g\n", pldResult.IntrinsicValuePerShare)
	fmt.Printf("ExpectedPLDForwardValue = %.15g\n", pldResult.ForwardValue)

	// Reference the profile package symbols so a future refactor that
	// renames either constant fails the capture build at compile time
	// rather than producing stale pin values.
	_ = profile.ExpectedEQIXPrimaryValue
	_ = profile.ExpectedPLDPrimaryValue
}
