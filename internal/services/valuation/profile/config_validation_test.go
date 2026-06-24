package profile_test

import (
	"encoding/json"
	"os"
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

	// Layer A: the canonical hypergrowth/cyclical-high-growth profile (AMD/NVDA
	// route here via mfg_semi) MUST carry a validated sales_to_capital
	// trajectory — this is the profile whose reinvestment fix turns AMD's FCF
	// positive in-window. A missing/invalid method would silently demote it to
	// the legacy proportional path.
	amd, ok := reg.Lookup(profile.ArchetypeCyclicalMidCycle, profile.MaturityHighGrowth)
	require.True(t, ok, "cyclical_mid_cycle:high_growth must exist for the AMD/NVDA reinvestment path")
	require.Equal(t, profile.ReinvestmentSalesToCapital, amd.ReinvestmentMethod,
		"cyclical_mid_cycle:high_growth must opt into the Layer-A sales_to_capital trajectory")
	require.Greater(t, amd.SalesToCapitalTarget, amd.SalesToCapitalStart,
		"sales-to-capital must improve over the horizon")
	require.Positive(t, amd.TargetOperatingMargin)

	// The legacy mature-large-bank anchor MUST stay on the legacy proportional
	// path (empty method) so DDM/DCF bit-for-bit is untouched by Layer A.
	require.Empty(t, p.ReinvestmentMethod,
		"mature_large_bank:mature must NOT opt into Layer A (preserves bit-for-bit)")
}

// TestRealConfig_ExitMultipleProfilesHavePositiveMultiple is the VAL-1 Phase 4
// load-time invariant: every shipped profile that declares
// terminal_method == "exit_multiple" MUST carry terminal_multiple > 0.
//
// Phase 4 lets a PROFILE-sourced exit_multiple drive the DCF terminal through
// the same params precedence chain the request override uses. The resolver
// returns a typed 422 (params.ParamError) when method == "exit_multiple" and no
// positive multiple is resolvable. On the profile-driven path that 422 is now
// reachable for profile-sourced methods too, so a shipped exit_multiple profile
// with a zero/absent terminal_multiple would turn into a production 422 for
// every ticker that routes to it. This test keeps that from ever shipping.
func TestRealConfig_ExitMultipleProfilesHavePositiveMultiple(t *testing.T) {
	raw, err := os.ReadFile("../../../../config/assumption_profiles.json")
	require.NoError(t, err, "shipped assumption_profiles.json must be readable")

	var cfg struct {
		Profiles map[string]struct {
			TerminalMethod   string  `json:"terminal_method"`
			TerminalMultiple float64 `json:"terminal_multiple"`
		} `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg), "assumption_profiles.json must parse")
	require.NotEmpty(t, cfg.Profiles, "config must declare at least one profile")

	exitMultipleCount := 0
	for id, p := range cfg.Profiles {
		if p.TerminalMethod != string(profile.TerminalExitMultiple) {
			continue
		}
		exitMultipleCount++
		require.Greater(t, p.TerminalMultiple, 0.0,
			"profile %q declares terminal_method=exit_multiple but terminal_multiple=%v (must be >0 or the profile-driven path 422s)",
			id, p.TerminalMultiple)
	}
	// Guard against a vacuous pass: the shipped config is expected to carry
	// exit_multiple profiles (17 at plan time). If this drops to 0 the assertion
	// above silently stops protecting anything.
	require.Positive(t, exitMultipleCount,
		"expected at least one exit_multiple profile in the shipped config")
}
