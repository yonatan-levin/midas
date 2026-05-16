package profile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestLoadFromJSON_ValidConfig_LoadsSuccessfully exercises the happy path:
// a well-formed config with the required fallback rule must load, expose
// the declared config_version, and report a non-empty SHA-256 config_hash.
func TestLoadFromJSON_ValidConfig_LoadsSuccessfully(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	require.NoError(t, os.WriteFile(path, []byte(minimalValidConfig), 0o644))

	reg, err := profile.LoadFromJSON(path)
	require.NoError(t, err)

	assert.Equal(t, "1.0.0", reg.ConfigVersion())
	assert.NotEmpty(t, reg.ConfigHash())
}

// TestLoadFromJSON_Malformed_FailsLoudly verifies the fail-fast contract on
// malformed JSON. The engine MUST refuse to boot rather than silently degrade
// to an empty registry (spec §4.4).
func TestLoadFromJSON_Malformed_FailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	require.NoError(t, os.WriteFile(path, []byte("{ not valid json"), 0o644))

	_, err := profile.LoadFromJSON(path)
	assert.Error(t, err, "malformed config must error, never silently degrade")
}

// TestLoadFromJSON_MissingFallbackRule_FailsValidation pins invariant 7
// from spec §4.3: a config without an industry_prefix:"*" fallback rule
// must fail load-time validation.
func TestLoadFromJSON_MissingFallbackRule_FailsValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	raw := `{"config_version":"1.0.0","resolver_version":"1.0.0","profiles":{},"archetype_rules":[],"maturity_thresholds_fallback":{}}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

	_, err := profile.LoadFromJSON(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fallback")
}

// TestRegistry_Lookup_Hit verifies a direct (archetype, maturity) lookup
// returns the seeded profile.
func TestRegistry_Lookup_Hit(t *testing.T) {
	reg := mustLoadMinimal(t)
	p, ok := reg.Lookup(profile.ArchetypeMatureLargeBank, profile.MaturityMature)
	require.True(t, ok)
	assert.Equal(t, "mature_large_bank:mature", p.ProfileID)
}

// TestRegistry_Lookup_Miss verifies an unknown (archetype, maturity) pair
// returns ok=false (not nil-with-true, not a default profile).
func TestRegistry_Lookup_Miss(t *testing.T) {
	reg := mustLoadMinimal(t)
	_, ok := reg.Lookup(profile.ArchetypeREITDataCenter, profile.MaturityHighGrowth)
	assert.False(t, ok)
}

const minimalValidConfig = `{
  "config_version": "1.0.0",
  "resolver_version": "1.0.0",
  "profiles": {
    "mature_large_bank:mature": {
      "profile_id": "mature_large_bank:mature",
      "archetype": "mature_large_bank", "maturity": "mature",
      "horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": 0.8, "dps_growth_cap": 0.08,
      "payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth": {
      "profile_id": "software_like_scaling:standard_growth",
      "archetype": "software_like_scaling", "maturity": "standard_growth",
      "horizon_years": 5, "compound_growth_cap": 4.0,
      "revenue_base_method": "raw_ttm", "discount_method": "wacc",
      "terminal_method": "gordon_growth", "stabilized": false, "fade_years": 1,
      "terminal_multiple": 4.0, "dps_growth_cap": 0,
      "payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    }
  },
  "archetype_rules": [
    {"id":"fin_large_bank","priority":100,"industry_prefix":"FIN_LARGE_BANK","archetype":"mature_large_bank"},
    {"id":"fallback_default","priority":0,"industry_prefix":"*","archetype":"software_like_scaling"}
  ],
  "maturity_thresholds_fallback": {
    "large_cap_revenue_min_usd": 50000000000,
    "mid_cap_revenue_min_usd": 10000000000,
    "high_growth_revenue_yoy_min": 0.30,
    "mature_revenue_yoy_max": 0.10,
    "trough_oi_threshold": 0.0
  }
}`

func mustLoadMinimal(t *testing.T) profile.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	require.NoError(t, os.WriteFile(path, []byte(minimalValidConfig), 0o644))
	reg, err := profile.LoadFromJSON(path)
	require.NoError(t, err)
	return reg
}
