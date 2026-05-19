//go:build pincapture

// One-shot pin-capture helper. Build-tag-gated so the default `go test ./...`
// run does NOT execute it (default build excludes it). Workflow:
//
//  1. Run: go test -tags pincapture -run TestCapturePins \
//     ./internal/services/valuation/profile/... -v
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
	// so the latest period is discoverable. Doing this in the test rather
	// than in the shared fixture keeps the Bootstrap helper untouched.
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
	if err != nil {
		t.Fatalf("MXL pin capture failed: %v", err)
	}
	fmt.Printf("expectedMXLPrimaryValue = %.15g\n", result.IntrinsicValuePerShare)
	fmt.Printf("expectedMXLTrailingValue = %.15g\n", result.TrailingValue)
	fmt.Printf("expectedMXLForwardValue = %.15g\n", result.ForwardValue)
}
