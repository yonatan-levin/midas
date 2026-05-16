package profile_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestRealConfig_LoadsAndValidates pins that the shipped production
// assumption_profiles.json passes every load-time invariant (spec §4.3).
//
// LoadFromJSON fails startup on any of:
//   - malformed JSON
//   - resolver_version mismatch (config drift outrunning compiled resolver)
//   - duplicate rule IDs
//   - invalid archetype/maturity/method enum values
//   - missing wildcard fallback rule
//   - rule archetype with no profile entries
//   - negative maturity-threshold values
//
// Tier 2 P0b ships the minimum config (mature_large_bank:mature for the JPM
// bit-for-bit anchor + software_like_scaling:standard_growth fallback). P1/P2/
// P3/P4 add the remaining 19 rows per spec §10.1; each phase MUST keep this
// test green.
func TestRealConfig_LoadsAndValidates(t *testing.T) {
	// Path relative to this test file's directory (config_validation_test.go
	// lives at internal/services/valuation/profile/). Four "../" hops reach
	// the repo root where config/ lives.
	reg, err := profile.LoadFromJSON("../../../../config/assumption_profiles.json")
	require.NoError(t, err, "production config must validate")
	require.NotEmpty(t, reg.ConfigVersion())
	require.NotEmpty(t, reg.ConfigHash())

	// Spec §4.3 invariant: the JPM bit-for-bit anchor MUST resolve via the
	// fin_large_bank rule to the legacy single-stage Gordon DDM path
	// (DividendForecastHorizon == 0). Without this profile entry every DDM
	// call in production would silently take the new multi-stage path and
	// break legacy responses.
	p, ok := reg.Lookup(profile.ArchetypeMatureLargeBank, profile.MaturityMature)
	require.True(t, ok, "mature_large_bank:mature must exist for JPM bit-for-bit path")
	require.Equal(t, 0, p.DividendForecastHorizon,
		"mature_large_bank:mature MUST keep dividend_forecast_horizon=0 — this is the bit-for-bit preservation signal")

	// Wildcard fallback MUST resolve cleanly for unknown industries so the
	// resolver never returns nil.
	resolved, trace := reg.Resolve(profile.NewFactsForTest("UNKNOWN_INDUSTRY_XYZ", nil, nil))
	require.NotNil(t, resolved, "unknown industry must resolve to fallback (never nil)")
	require.Equal(t, profile.SourceFallback, trace.Source,
		"unknown industry must take the SourceFallback branch")
}
