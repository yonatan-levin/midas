package testhelpers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// MustLoadFullFixture loads a fuller test fixture with cyclical_mid_cycle,
// cyclical_trough, fin_large_bank, fin_generic profiles + rules. Suitable
// for resolver tests that need a richer rule set than the minimal config.
//
// Phase Bootstrap note: profile.LoadFromJSON parses only config_version +
// profiles map until P0a wires the full schema. Helper consumers that need
// archetype rules / maturity thresholds must wait for P0a.
func MustLoadFullFixture(t *testing.T) profile.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	require.NoError(t, os.WriteFile(path, []byte(fullFixtureConfig), 0o644))
	reg, err := profile.LoadFromJSON(path)
	require.NoError(t, err)
	return reg
}

const fullFixtureConfig = `{
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
    "cyclical_mid_cycle:standard_growth": {
      "profile_id": "cyclical_mid_cycle:standard_growth",
      "archetype": "cyclical_mid_cycle", "maturity": "standard_growth",
      "horizon_years": 5, "compound_growth_cap": 2.0,
      "revenue_base_method": "two_year_average", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": false, "fade_years": 1,
      "terminal_multiple": 3.0, "dps_growth_cap": 0,
      "payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "cyclical_trough:standard_growth": {
      "profile_id": "cyclical_trough:standard_growth",
      "archetype": "cyclical_trough", "maturity": "standard_growth",
      "horizon_years": 5, "compound_growth_cap": 3.0,
      "revenue_base_method": "max_ttm_or_floor", "discount_method": "cost_of_equity",
      "terminal_method": "exit_multiple", "stabilized": false, "fade_years": 2,
      "terminal_multiple": 4.0, "dps_growth_cap": 0,
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
    {"id":"fin_generic","priority":50,"industry_prefix":"FIN","archetype":"mature_large_bank"},
    {"id":"mfg_semi","priority":90,"industry_prefix":"MFG_SEMI","archetype":"cyclical_mid_cycle"},
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
