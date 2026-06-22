package valuation

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/params"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// ---------------------------------------------------------------------------
// VAL-1 Phase 2 — archetype-aware DCF horizon (production-wiring tests).
//
// These tests exercise the PRODUCTION path: a Service built via NewService with
// a profile.Registry, where NewService derives the shared growth estimator's
// Stage3Years from registry.MaxHorizonYears(). No test-only estimator rebuild.
// ---------------------------------------------------------------------------

// TestService_DCF_ArchetypeHorizonGrid_ProductionWiring is plan T1: the
// load-bearing production-path grid. Each archetype routes (via the wildcard
// rule) to a single profile with the target horizon; the resolved DCF horizon
// and per-year-PV slice length MUST match, with a Gordon terminal. The 10y row
// is the one that fails before Phase 2's production wiring (it clamped to 7).
func TestService_DCF_ArchetypeHorizonGrid_ProductionWiring(t *testing.T) {
	cases := []struct {
		name      string
		archetype string
		maturity  string
		horizon   int
	}{
		{"mature_large_scale_3y", "mature_large_scale", "mature", 3},
		{"standard_growth_5y", "software_like_scaling", "standard_growth", 5},
		{"high_growth_profitable_7y", "software_like_large_scale", "high_growth", 7},
		{"hypergrowth_profitable_10y", "hypergrowth_profitable", "high_growth", 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := loadP2TestRegistry(t, tc.archetype, tc.maturity, tc.horizon, "gordon_growth")
			svc := buildP2TestService(t, reg)
			historicalData, marketData, macroData := createTestData()

			result, err := svc.performValuation(context.Background(), historicalData, marketData, macroData, nil, nil)
			require.NoError(t, err)
			require.NotNil(t, result)

			assert.Equal(t, tc.horizon, result.DCFHorizonYears,
				"DCF horizon must equal the profile's HorizonYears (no silent clamp)")
			assert.Len(t, result.DCFPerYearPV, tc.horizon,
				"DCFPerYearPV must have exactly HorizonYears elements")
			assert.Equal(t, "gordon_growth", result.DCFTerminalMethod,
				"Phase 2 is Gordon-terminal only")
			assert.Greater(t, result.DCFTerminalPctOfEV, 0.0,
				"terminal_pv / EV must be > 0 for a valid DCF")
			assert.LessOrEqual(t, result.DCFTerminalPctOfEV, 1.0,
				"terminal_pv cannot exceed total EV")
			assert.Greater(t, result.DCFTerminalGrowthUsed, 0.0,
				"terminal growth must be stamped for transparency")
		})
	}
}

// TestNewService_DeriveStage3FromRegistryMaxHorizon is plan T4: the NewService
// Stage3Years derivation against varying registry max horizons. With Stage1=3,
// Stage2=4 (DefaultEstimatorConfig), Stage3 = max(0, maxHorizon - 7), clamped so
// the total never exceeds params.MaxDCFProjectionYears.
func TestNewService_DeriveStage3FromRegistryMaxHorizon(t *testing.T) {
	cases := []struct {
		name           string
		regMaxHorizon  int // -1 sentinel => nil registry
		wantStage3     int
		wantTotalRates int
		wantInjected   int
	}{
		{"registry_max_10_gives_stage3_3", 10, 3, 10, 3},
		{"registry_max_7_gives_stage3_0", 7, 0, 7, 0},
		{"registry_max_3_gives_stage3_0", 3, 0, 7, 0},
		{"nil_registry_gives_stage3_0", -1, 0, 7, 0},
		{"registry_max_above_ceiling_clamped", 60, params.MaxDCFProjectionYears - 7, params.MaxDCFProjectionYears, params.MaxDCFProjectionYears - 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reg profile.Registry
			if tc.regMaxHorizon >= 0 {
				// A single-profile wildcard registry whose only profile carries the
				// target horizon, so MaxHorizonYears() == regMaxHorizon. profile
				// validation caps horizon at 15, so the 60 case is built via a
				// dedicated stub registry below.
				if tc.regMaxHorizon <= 15 {
					reg = loadP2TestRegistry(t, "hypergrowth_profitable", "high_growth", tc.regMaxHorizon, "gordon_growth")
				} else {
					reg = stubMaxHorizonRegistry{max: tc.regMaxHorizon}
				}
			}

			svc := buildServiceWithRegistry(t, reg)
			cfg := svc.growthEstimator.Config()

			assert.Equal(t, tc.wantStage3, cfg.Stage3Years, "Stage3Years derivation")
			assert.Equal(t, tc.wantTotalRates, cfg.Stage1Years+cfg.Stage2Years+cfg.Stage3Years,
				"total growth-rate slice length")
			assert.Equal(t, tc.wantInjected, svc.estimatorInjectedStage3,
				"estimatorInjectedStage3 must equal the Stage3 added beyond the legacy 0")
		})
	}
}

// TestService_DCF_DefaultPath_ByteIdentity is plan T3 (load-bearing): with a
// long-horizon registry (max 10 → shared estimator emits 10 rates), a ticker
// that resolves to NO profile-supplied horizon must still produce the legacy 7y
// DCF — byte-identical to the legacy 7-rate estimator. This proves D2: the
// longer slice does not leak into the default/no-profile path.
//
// We simulate "no profile horizon" by routing to a profile whose HorizonYears
// is 0 (legacy signal), so the resolver falls through to the default source,
// which the LegacyDefaultHorizonYears neutralizer pins at 7.
func TestService_DCF_DefaultPath_ByteIdentity(t *testing.T) {
	// reg has a 10y sibling profile (raising MaxHorizonYears to 10) AND the
	// wildcard-routed profile carries horizon_years: 0 — the legacy signal.
	reg := loadP2TestRegistryWithMaxSibling(t, "cyclical_mid_cycle", "mature", 0, "gordon_growth", 10)
	svc := buildP2TestService(t, reg)
	require.Equal(t, 3, svc.estimatorInjectedStage3,
		"sanity: the 10y sibling must have driven the shared estimator to 10 rates")

	historicalData, marketData, macroData := createTestData()
	withLongSlice, err := svc.performValuation(context.Background(), historicalData, marketData, macroData, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, withLongSlice)

	// Reference: the SAME profile but with a registry whose max horizon is 7, so
	// the shared estimator stays at the legacy 7 rates (no Stage3 injection).
	regRef := loadP2TestRegistry(t, "cyclical_mid_cycle", "mature", 0, "gordon_growth")
	svcRef := buildP2TestService(t, regRef)
	require.Equal(t, 0, svcRef.estimatorInjectedStage3,
		"sanity: reference service must keep the legacy 7-rate estimator")
	hd2, md2, mc2 := createTestData()
	legacy7y, err := svcRef.performValuation(context.Background(), hd2, md2, mc2, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, legacy7y)

	// Byte-identity: the longer shared slice must NOT change the default-path DCF.
	assert.Equal(t, 7, withLongSlice.DCFHorizonYears,
		"default/no-profile horizon must stay at the legacy 7 even with a 10-rate slice")
	assert.Equal(t, legacy7y.DCFHorizonYears, withLongSlice.DCFHorizonYears,
		"horizon must match the legacy 7-rate reference")
	assert.Equal(t, len(legacy7y.DCFPerYearPV), len(withLongSlice.DCFPerYearPV),
		"per-year-PV length must match the legacy reference")
	assert.Equal(t, math.Float64bits(legacy7y.DCFValuePerShare), math.Float64bits(withLongSlice.DCFValuePerShare),
		"dcf_value_per_share must be bit-for-bit identical to the legacy 7-rate run")
	for i := range legacy7y.DCFPerYearPV {
		assert.Equal(t, math.Float64bits(legacy7y.DCFPerYearPV[i]), math.Float64bits(withLongSlice.DCFPerYearPV[i]),
			"per-year PV[%d] must be bit-for-bit identical", i)
	}
}

// TestService_DCF_HighGrowth7y_NoDriftFromLongerSlice is plan T5 (AAPL-class
// regression): a high-growth profile pinned at 7y is UNCHANGED by Phase 2 (its
// horizon was already ≤ 7). With a 10-rate shared slice (long-horizon sibling in
// the registry) the resolved horizon stays 7 and the DCF value does not drift
// more than ±5% versus the legacy 7-rate run — in practice 0% (bit-for-bit),
// since the resolved horizon truncates the slice to exactly 7 rates.
func TestService_DCF_HighGrowth7y_NoDriftFromLongerSlice(t *testing.T) {
	reg := loadP2TestRegistryWithMaxSibling(t, "software_like_large_scale", "high_growth", 7, "gordon_growth", 10)
	svc := buildP2TestService(t, reg)
	historicalData, marketData, macroData := createTestData()
	got, err := svc.performValuation(context.Background(), historicalData, marketData, macroData, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, got)

	regRef := loadP2TestRegistry(t, "software_like_large_scale", "high_growth", 7, "gordon_growth")
	svcRef := buildP2TestService(t, regRef)
	hd2, md2, mc2 := createTestData()
	ref, err := svcRef.performValuation(context.Background(), hd2, md2, mc2, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, ref)

	assert.Equal(t, 7, got.DCFHorizonYears, "7y profile must stay at 7y")
	require.Greater(t, ref.DCFValuePerShare, 0.0, "reference value must be positive for a meaningful tolerance")
	drift := math.Abs(got.DCFValuePerShare-ref.DCFValuePerShare) / ref.DCFValuePerShare
	assert.LessOrEqual(t, drift, 0.05,
		"7y DCF value must not drift more than ±5%% from the legacy 7-rate run (got %.4f%%)", drift*100)
}

// ---------------------------------------------------------------------------
// Test helpers (Phase 2)
// ---------------------------------------------------------------------------

// buildServiceWithRegistry is buildP2TestService without the createTestData
// coupling — used by the NewService derivation test (T4), which only inspects
// the constructed estimator config, not a valuation run.
func buildServiceWithRegistry(t *testing.T, reg profile.Registry) *Service {
	t.Helper()
	return buildP2TestService(t, reg)
}

// stubMaxHorizonRegistry is a minimal profile.Registry whose MaxHorizonYears is
// arbitrary, used to exercise the engine-ceiling clamp (D3) with a value above
// profile validation's 15-year cap. Resolve/Lookup are never called by the
// NewService Stage3 derivation, so they return zero values.
type stubMaxHorizonRegistry struct{ max int }

func (s stubMaxHorizonRegistry) Resolve(profile.Facts) (*profile.ResolvedProfile, profile.ResolutionTrace) {
	return nil, profile.ResolutionTrace{}
}
func (s stubMaxHorizonRegistry) Lookup(profile.Archetype, profile.Maturity) (*profile.AssumptionProfile, bool) {
	return nil, false
}
func (s stubMaxHorizonRegistry) ConfigVersion() string { return "stub" }
func (s stubMaxHorizonRegistry) ConfigHash() string    { return "stub" }
func (s stubMaxHorizonRegistry) MaxHorizonYears() int  { return s.max }

// compile-time assertion that the stub satisfies the interface.
var _ profile.Registry = stubMaxHorizonRegistry{}
