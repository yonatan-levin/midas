package valuation

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// ---------------------------------------------------------------------------
// VAL-1 Phase 3 — cyclical-base normalization (production-wiring tests).
//
// These exercise the production path: a Service whose profileRegistry routes a
// ticker to a cyclical archetype. The seam after the BUG-015 TTM rebase floors
// the DCF base OI at the 3-year FY mean, recorded via DCFBaseNormalization. The
// non-cyclical path stays byte-identical (the field is omitempty-dropped).
// ---------------------------------------------------------------------------

// createTroughTestData builds an FY-only fixture whose LATEST period is a
// trough: the most recent FY (2023) operating income is far below the prior two
// years' mean, so max(latest, 3y_mean) raises the base. Built from createTestData's
// AAPL shape so WACC/share-count inputs stay realistic; only the OI per year is
// re-shaped into a trough.
func createTroughTestData() (*entities.HistoricalFinancialData, *entities.MarketData, *entities.MacroData) {
	hist, market, macro := createTestData()
	// Trough latest year, healthy prior years.
	// effective OI: 2023=40B (latest, trough), 2022=120B, 2021=120B → mean ≈ 93.3B > 40B.
	hist.Data["2023FY"].OperatingIncome = 40000000000
	hist.Data["2023FY"].NormalizedOperatingIncome = 40000000000
	hist.Data["2022FY"].OperatingIncome = 120000000000
	hist.Data["2022FY"].NormalizedOperatingIncome = 120000000000
	hist.Data["2021FY"].OperatingIncome = 120000000000
	hist.Data["2021FY"].NormalizedOperatingIncome = 120000000000
	return hist, market, macro
}

// TestService_CyclicalBaseNormalization_TroughFiresMean is the Phase 3 headline:
// for a cyclical_trough profile against a trough fixture, the DCF base is floored
// at the 3y FY mean (DCFBaseNormalization == "3y_mean") AND the resulting
// dcf_value_per_share differs from a control run on the SAME fixture under a
// non-cyclical profile (where the trough latest OI feeds the base unchanged).
func TestService_CyclicalBaseNormalization_TroughFiresMean(t *testing.T) {
	regCyclical := loadP2TestRegistry(t, "cyclical_trough", "standard_growth", 5, "gordon_growth")
	svcCyclical := buildP2TestService(t, regCyclical)
	hd, md, mc := createTroughTestData()

	cyclical, err := svcCyclical.performValuation(context.Background(), hd, md, mc, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, cyclical)

	assert.Equal(t, "3y_mean", cyclical.DCFBaseNormalization,
		"cyclical trough fixture must floor the base at the 3y FY mean")

	// Control: SAME fixture, non-cyclical profile → no normalization → the trough
	// latest OI drives the base. The cyclical-normalized run must value higher.
	regControl := loadP2TestRegistry(t, "mature_large_scale", "mature", 5, "gordon_growth")
	svcControl := buildP2TestService(t, regControl)
	hd2, md2, mc2 := createTroughTestData()
	control, err := svcControl.performValuation(context.Background(), hd2, md2, mc2, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, control)

	assert.Empty(t, control.DCFBaseNormalization,
		"non-cyclical control must leave the normalization field empty")
	// The control feeds the deep trough OI straight into the base; the cyclical
	// run floors it at the healthy 3y mean. The cyclical value must be both
	// strictly higher and bit-for-bit different from the un-normalized control.
	assert.NotEqual(t, math.Float64bits(control.DCFValuePerShare), math.Float64bits(cyclical.DCFValuePerShare),
		"flooring the trough base at the 3y mean must change dcf_value_per_share vs the un-normalized run")
	assert.Greater(t, cyclical.DCFValuePerShare, control.DCFValuePerShare,
		"raising the base to the (higher) 3y mean must raise the per-share value")
}

// TestService_CyclicalBaseNormalization_PeakKeepsLatest covers a cyclical
// profile where the latest OI is already above the 3y mean (rising series):
// method is "latest" and the value is byte-identical to a non-cyclical run on the
// same fixture (no flooring applied → identical base → identical DCF).
func TestService_CyclicalBaseNormalization_PeakKeepsLatest(t *testing.T) {
	// createTestData's AAPL OI rises (2023 highest), so a cyclical profile chooses "latest".
	regCyclical := loadP2TestRegistry(t, "cyclical_mid_cycle", "mature", 5, "gordon_growth")
	svcCyclical := buildP2TestService(t, regCyclical)
	hd, md, mc := createTestData()
	cyclical, err := svcCyclical.performValuation(context.Background(), hd, md, mc, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, cyclical)

	assert.Equal(t, "latest", cyclical.DCFBaseNormalization,
		"a rising (above-mean) cyclical series keeps the latest base")

	// Reference: same horizon/method, non-cyclical profile, same fixture.
	regRef := loadP2TestRegistry(t, "mature_large_scale", "mature", 5, "gordon_growth")
	svcRef := buildP2TestService(t, regRef)
	hd2, md2, mc2 := createTestData()
	ref, err := svcRef.performValuation(context.Background(), hd2, md2, mc2, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, ref)

	assert.Empty(t, ref.DCFBaseNormalization)
	assert.Equal(t, math.Float64bits(ref.DCFValuePerShare), math.Float64bits(cyclical.DCFValuePerShare),
		"when the floor does not bind, the cyclical base equals latest → bit-for-bit identical DCF")
}

// TestService_CyclicalBaseNormalization_NonCyclicalByteIdentity is the
// load-bearing byte-identity pin: a non-cyclical profile must never set
// dcf_base_normalization, so omitempty drops it and the response shape is
// byte-identical to today.
func TestService_CyclicalBaseNormalization_NonCyclicalByteIdentity(t *testing.T) {
	reg := loadP2TestRegistry(t, "software_like_scaling", "standard_growth", 5, "gordon_growth")
	svc := buildP2TestService(t, reg)
	hd, md, mc := createTestData()
	result, err := svc.performValuation(context.Background(), hd, md, mc, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.DCFBaseNormalization,
		"non-cyclical profiles must never set dcf_base_normalization (omitempty drop)")
}

// TestService_CyclicalBaseNormalization_NilProfileGuard confirms the nil-registry
// (no profile resolution) path never sets the field and never panics on the
// nil *ResolvedProfile guard.
func TestService_CyclicalBaseNormalization_NilProfileGuard(t *testing.T) {
	svc := buildP2TestService(t, nil) // nil registry → resolvedProfile stays nil
	hd, md, mc := createTestData()
	result, err := svc.performValuation(context.Background(), hd, md, mc, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.DCFBaseNormalization,
		"nil profile path must leave the normalization field empty")
}

// TestService_CyclicalExitMultiple_Phase3And4CoOccur is the VAL-1 composition pin
// for the PRODUCTION shape: the shipped config gives every cyclical profile
// terminal_method=exit_multiple, so a cyclical ticker fires Phase 3 (3y-mean base
// normalization) AND Phase 4 (profile-sourced exit-multiple terminal) on the SAME
// valuation. Phase 3 tests pin gordon_growth and Phase 4 tests use non-cyclical
// archetypes, so neither exercises the combination — this does.
//
// Uses loadP4TestRegistry (cyclical_trough + exit_multiple + a positive profile
// multiple) so TerminalMethodSource()==SourceProfile, and the trough fixture so
// max(latest, 3y_mean) raises the base. buildP4TestService wires the industry
// EV/EBITDA default the blend reads against.
func TestService_CyclicalExitMultiple_Phase3And4CoOccur(t *testing.T) {
	const industryEVEBITDA = 8.0
	const profileMultiple = 25.0 // far above the industry default → provably profile-driven

	regCyclical := loadP4TestRegistry(t, "cyclical_trough", "standard_growth", "exit_multiple", profileMultiple)
	svcCyclical := buildP4TestService(t, regCyclical, industryEVEBITDA)
	hd, md, mc := createTroughTestData()

	res, err := svcCyclical.performValuation(context.Background(), hd, md, mc, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, res)

	// Phase 3 fired: the deep-trough latest OI was floored at the (higher) 3y FY mean.
	assert.Equal(t, "3y_mean", res.DCFBaseNormalization,
		"cyclical trough fixture must floor the DCF base at the 3y FY mean (Phase 3)")

	// Phase 4 fired: the profile-sourced exit_multiple drove the terminal blend.
	assert.Equal(t, "exit_multiple", res.DCFTerminalMethod,
		"the resolved terminal method must come from the cyclical profile (Phase 4)")
	assert.Greater(t, res.DCFExitMultipleTerminalValue, 0.0,
		"a profile-sourced exit_multiple must produce a non-zero exit-multiple TV component")
	assert.Greater(t, res.DCFGordonTerminalValue, 0.0,
		"the Gordon component is always surfaced alongside the exit-multiple blend")

	// Non-vacuity #1 (Phase 3 is live): a gordon-method control on the SAME cyclical
	// trough fixture still normalizes the base, but its per-share value differs from
	// the exit-multiple run — proving the exit-multiple terminal (Phase 4) materially
	// changed the value on top of the shared Phase-3 base.
	regGordon := loadP4TestRegistry(t, "cyclical_trough", "standard_growth", "gordon_growth", profileMultiple)
	svcGordon := buildP4TestService(t, regGordon, industryEVEBITDA)
	hd2, md2, mc2 := createTroughTestData()
	resGordon, err := svcGordon.performValuation(context.Background(), hd2, md2, mc2, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, resGordon)

	assert.Equal(t, "3y_mean", resGordon.DCFBaseNormalization,
		"the gordon-method control on the same cyclical trough fixture also normalizes the base")
	assert.NotEqual(t, math.Float64bits(resGordon.DCFValuePerShare), math.Float64bits(res.DCFValuePerShare),
		"the profile exit_multiple terminal must move dcf_value_per_share vs a gordon-method run on the same Phase-3 base")

	// Non-vacuity #2 (Phase 4 reads the PROFILE multiple, not the industry default):
	// the same cyclical+exit_multiple profile with multiple==industry default yields a
	// smaller exit-multiple TV, proving the profile multiple (25.0) drove the blend.
	regExitIndustry := loadP4TestRegistry(t, "cyclical_trough", "standard_growth", "exit_multiple", industryEVEBITDA)
	svcExitIndustry := buildP4TestService(t, regExitIndustry, industryEVEBITDA)
	hd3, md3, mc3 := createTroughTestData()
	resIndustry, err := svcExitIndustry.performValuation(context.Background(), hd3, md3, mc3, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, resIndustry)
	assert.Greater(t, res.DCFExitMultipleTerminalValue, resIndustry.DCFExitMultipleTerminalValue,
		"the higher profile multiple (25.0) must produce a larger exit-multiple TV than the industry default (8.0)")
}
