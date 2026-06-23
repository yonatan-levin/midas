package models_test

// VAL-3 Phase 3 — forward FFO/AFFO projection acceptance tests (plan §6).
//
// These build self-contained REIT ModelInputs with hand-controlled growth,
// cost-of-equity, and (optionally) maintenance capex, and set an EXPLICIT
// Profile so the forward math is validated independently of classifier /
// maturity bucketing (plan §5/D1). The model is constructed with
// NewFFOModelWithConfig(spotMultiple, 0, …): NAV cross-check disabled and a
// fixed spot P/FFO multiple so the trailing leg is exactly base*spotMultiple.

import (
	"math"
	"strings"
	"testing"
	"time"

	"context"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// reitForwardInput builds a single-period REIT ModelInput. maintCapEx<=0 and
// capEx<=0 means AFFO is unavailable (FFO base); set maintCapEx>0 to exercise
// the AFFO base. Growth/CostOfEquity/WACC are caller-controlled.
func reitForwardInput(netIncome, da, shares, maintCapEx, capEx, costOfEquity, wacc float64, rates []float64) *models.ModelInput {
	return &models.ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Ticker: "SYNTH_REIT",
			Data: map[string]*entities.FinancialData{
				"2025FY": {
					NetIncome:                   netIncome,
					DepreciationAndAmortization: da,
					GainOnPropertySales:         0,
					MaintenanceCapEx:            maintCapEx,
					CapitalExpenditures:         capEx,
					FilingDate:                  time.Now(),
					FilingPeriod:                "2025FY",
				},
			},
		},
		GrowthEstimate:         &entities.GrowthEstimate{ProjectedGrowthRates: rates},
		SharesOutstanding:      shares,
		CostOfEquity:           costOfEquity,
		WACC:                   wacc,
		InterestBearingDebt:    0,
		CashAndCashEquivalents: 0,
	}
}

// explicitProfile returns a ResolvedProfile carrying only the forward-path
// knobs (horizon, terminal multiple, cost-of-equity discount).
func explicitProfile(horizon int, terminal float64) *profile.ResolvedProfile {
	return &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			HorizonYears:     horizon,
			TerminalMultiple: terminal,
			DiscountMethod:   profile.DiscountCostOfEquity,
		},
	}
}

// expectedForward replicates the engine's forward math for hand-verification.
func expectedForward(basePerShare float64, rates []float64, horizon int, terminal, costOfEquity float64) float64 {
	fwd := basePerShare
	for i := 0; i < horizon; i++ {
		fwd *= 1 + rates[i]
	}
	fwd *= terminal
	return fwd / math.Pow(1+costOfEquity, float64(horizon))
}

// TestFFO_Forward_IndustrialREIT_PLD_ForwardHigherThanSnapshot — PLD-style
// industrial REIT: 5y at ~10%/yr, terminal 24x, spot 20x, CoE 9% ⇒ forward
// per-share 15–30% higher than the snapshot (plan acceptance §6).
func TestFFO_Forward_IndustrialREIT_PLD_ForwardHigherThanSnapshot(t *testing.T) {
	rates := []float64{0.10, 0.10, 0.10, 0.10, 0.10}
	input := reitForwardInput(1_000_000_000, 1_400_000_000, 200_000_000, 0, 0, 0.09, 0.07, rates)
	input.Profile = explicitProfile(5, 24.0)

	ffo := models.NewFFOModelWithConfig(20.0, 0, zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	require.Greater(t, result.TrailingValue, 0.0)
	require.Greater(t, result.ForwardValue, 0.0)
	ratio := result.ForwardValue / result.TrailingValue
	assert.GreaterOrEqual(t, ratio, 1.15, "PLD forward should be >=15%% above snapshot")
	assert.LessOrEqual(t, ratio, 1.30, "PLD forward should be <=30%% above snapshot")
	assert.Equal(t, 5, result.HorizonSelected)
	assert.InEpsilon(t, 24.0, result.TerminalMultiple, 1e-9)
}

// TestFFO_Forward_MallREIT_SPG_ForwardNearSnapshot — SPG-style mall REIT:
// 3y at ~3%/yr, terminal 10x, spot 10x, CoE 5% ⇒ forward ≈ snapshot ±10%.
func TestFFO_Forward_MallREIT_SPG_ForwardNearSnapshot(t *testing.T) {
	rates := []float64{0.03, 0.03, 0.03}
	input := reitForwardInput(800_000_000, 900_000_000, 300_000_000, 0, 0, 0.05, 0.06, rates)
	input.Profile = explicitProfile(3, 10.0)

	ffo := models.NewFFOModelWithConfig(10.0, 0, zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	require.Greater(t, result.TrailingValue, 0.0)
	require.Greater(t, result.ForwardValue, 0.0)
	ratio := result.ForwardValue / result.TrailingValue
	assert.LessOrEqual(t, math.Abs(ratio-1.0), 0.10, "SPG forward should be within ±10%% of snapshot, got ratio=%.4f", ratio)
	assert.Equal(t, 3, result.HorizonSelected)
}

// TestFFO_Forward_BothLegsEmitted_Divergence — both trailing and forward are
// populated, distinct, and the horizon/terminal are echoed.
func TestFFO_Forward_BothLegsEmitted_Divergence(t *testing.T) {
	rates := []float64{0.08, 0.07, 0.06, 0.05, 0.04}
	input := reitForwardInput(1_000_000_000, 1_500_000_000, 250_000_000, 0, 0, 0.085, 0.07, rates)
	input.Profile = explicitProfile(5, 22.0)

	ffo := models.NewFFOModelWithConfig(18.0, 0, zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.TrailingValue, 0.0)
	assert.Greater(t, result.ForwardValue, 0.0)
	assert.NotEqual(t, result.TrailingValue, result.ForwardValue)
	assert.Equal(t, 5, result.HorizonSelected)
	assert.InEpsilon(t, 22.0, result.TerminalMultiple, 1e-9)
}

// TestFFO_Forward_DiscountsAtCostOfEquity_NotWACC — Issue 4: the forward leg
// discounts at cost-of-equity, NOT WACC. Pin the exact value by hand AND
// confirm changing WACC alone leaves ForwardValue byte-identical.
func TestFFO_Forward_DiscountsAtCostOfEquity_NotWACC(t *testing.T) {
	rates := []float64{0.06, 0.06, 0.06, 0.06, 0.06}
	const coe = 0.09
	const spot, terminal = 18.0, 22.0
	const ni, da, shares = 1_000_000_000.0, 1_400_000_000.0, 200_000_000.0

	// FFO base/share = (ni+da)/shares.
	basePerShare := (ni + da) / shares
	want := expectedForward(basePerShare, rates, 5, terminal, coe)

	input := reitForwardInput(ni, da, shares, 0, 0, coe, 0.04 /*WACC≠CoE*/, rates)
	input.Profile = explicitProfile(5, terminal)
	ffo := models.NewFFOModelWithConfig(spot, 0, zap.NewNop())

	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)
	assert.InEpsilon(t, want, result.ForwardValue, 1e-9,
		"forward must discount at cost-of-equity (%.2f), not WACC", coe)

	// Second run: WACC changes, CoE fixed ⇒ ForwardValue byte-identical.
	input2 := reitForwardInput(ni, da, shares, 0, 0, coe, 0.12 /*different WACC*/, rates)
	input2.Profile = explicitProfile(5, terminal)
	result2, err := ffo.Calculate(context.Background(), input2)
	require.NoError(t, err)
	assert.Equal(t, math.Float64bits(result.ForwardValue), math.Float64bits(result2.ForwardValue),
		"changing WACC alone must not move the forward leg (it is cost-of-equity discounted)")
}

// TestFFO_Forward_ProjectsAFFOWhenAvailable — when AFFO is available the
// forward leg projects AFFO/share (strictly below the FFO projection).
func TestFFO_Forward_ProjectsAFFOWhenAvailable(t *testing.T) {
	rates := []float64{0.07, 0.07, 0.07, 0.07, 0.07}
	const coe = 0.09
	const spot, terminal = 18.0, 24.0
	const ni, da, shares = 1_000_000_000.0, 1_500_000_000.0, 200_000_000.0
	const maintCapEx = 500_000_000.0

	ffoBasePerShare := (ni + da) / shares
	affoBasePerShare := (ni + da - maintCapEx) / shares

	input := reitForwardInput(ni, da, shares, maintCapEx, 0, coe, 0.07, rates)
	input.Profile = explicitProfile(5, terminal)
	ffo := models.NewFFOModelWithConfig(spot, 0, zap.NewNop())

	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	wantAFFO := expectedForward(affoBasePerShare, rates, 5, terminal, coe)
	wantFFO := expectedForward(ffoBasePerShare, rates, 5, terminal, coe)
	assert.InEpsilon(t, wantAFFO, result.ForwardValue, 1e-9, "forward must project AFFO/share")
	assert.Less(t, result.ForwardValue, wantFFO, "AFFO forward must be strictly below the FFO forward")

	// Trailing and forward share the AFFO base (consistency rule).
	assert.InEpsilon(t, affoBasePerShare*spot, result.TrailingValue, 1e-9)
	assertWarningContains(t, result.Warnings, "forward AFFO")
}

// TestFFO_Forward_FallsBackToFFOWhenNoAFFO — no maintenance capex (and no total
// capex to estimate from) ⇒ forward projects FFO/share.
func TestFFO_Forward_FallsBackToFFOWhenNoAFFO(t *testing.T) {
	rates := []float64{0.07, 0.07, 0.07, 0.07, 0.07}
	const coe = 0.09
	const spot, terminal = 18.0, 24.0
	const ni, da, shares = 1_000_000_000.0, 1_500_000_000.0, 200_000_000.0

	ffoBasePerShare := (ni + da) / shares
	input := reitForwardInput(ni, da, shares, 0, 0, coe, 0.07, rates)
	input.Profile = explicitProfile(5, terminal)
	ffo := models.NewFFOModelWithConfig(spot, 0, zap.NewNop())

	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	want := expectedForward(ffoBasePerShare, rates, 5, terminal, coe)
	assert.InEpsilon(t, want, result.ForwardValue, 1e-9, "forward must project FFO/share when AFFO unavailable")
	assert.InEpsilon(t, ffoBasePerShare*spot, result.TrailingValue, 1e-9)
	assertWarningContains(t, result.Warnings, "forward FFO")
}

// TestFFO_Forward_NegativeAFFO_FloorsToZero_NoFFOFallback — AFFO available but ≤0
// (maintenance capex exceeds FFO): both trailing and forward legs floor to 0 and a
// distress warning is present. The forward leg must NOT project a negative base, and
// must NOT fall back to the FFO base — falling back would break the load-bearing
// trailing↔forward base-consistency invariant (valuePerShare == headlineBasePerShare *
// pffoMultiple), since the trailing headline is already 0 (D2 floor).
func TestFFO_Forward_NegativeAFFO_FloorsToZero_NoFFOFallback(t *testing.T) {
	rates := []float64{0.07, 0.07, 0.07, 0.07, 0.07}
	const ni, da, shares = 200_000_000.0, 300_000_000.0, 100_000_000.0
	const maintCapEx = 1_000_000_000.0 // > FFO (500M) ⇒ AFFO < 0

	input := reitForwardInput(ni, da, shares, maintCapEx, 0, 0.09, 0.07, rates)
	input.Profile = explicitProfile(5, 24.0)
	ffo := models.NewFFOModelWithConfig(18.0, 0, zap.NewNop())

	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Equal(t, 0.0, result.TrailingValue, "distressed REIT trailing floors to 0")
	assert.Equal(t, 0.0, result.ForwardValue, "forward base zeroed when AFFO floored to 0")
	assertWarningContains(t, result.Warnings, "distressed REIT")
}

// TestFFO_Forward_InsufficientGrowthRates_NoForward — fewer projected growth
// rates than the horizon ⇒ forward stays zero (no panic, no partial leg).
func TestFFO_Forward_InsufficientGrowthRates_NoForward(t *testing.T) {
	rates := []float64{0.07, 0.07} // only 2, horizon needs 5
	input := reitForwardInput(1_000_000_000, 1_500_000_000, 200_000_000, 0, 0, 0.09, 0.07, rates)
	input.Profile = explicitProfile(5, 24.0)
	ffo := models.NewFFOModelWithConfig(18.0, 0, zap.NewNop())

	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.TrailingValue, 0.0)
	assert.Equal(t, 0.0, result.ForwardValue)
	assert.Equal(t, 0, result.HorizonSelected)
	assert.Equal(t, 0.0, result.TerminalMultiple)
}

func assertWarningContains(t *testing.T, warnings []string, substr string) {
	t.Helper()
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Fatalf("expected a warning containing %q; got %v", substr, warnings)
}
