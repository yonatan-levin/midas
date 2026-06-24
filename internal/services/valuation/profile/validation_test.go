package profile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// validationCase exercises one load-time invariant by mutating a known-good
// config and asserting the expected substring surfaces in the error chain.
// Keeping the table outside the tests so additional invariants can be
// pinned by appending one struct literal — no boilerplate per case.
type validationCase struct {
	name        string
	config      string
	wantErrPart string
}

// TestLoadFromJSON_NegativeValidation exercises each load-time invariant
// from spec §4.3 with a minimal targeted-mutation configuration.
func TestLoadFromJSON_NegativeValidation(t *testing.T) {
	cases := []validationCase{
		{
			name:        "non_semver_config_version",
			config:      replaceJSONField(minimalValidConfig, `"config_version": "1.0.0"`, `"config_version": "v1"`),
			wantErrPart: "not semver",
		},
		{
			name:        "resolver_version_mismatch",
			config:      replaceJSONField(minimalValidConfig, `"resolver_version": "1.0.0"`, `"resolver_version": "9.9.9"`),
			wantErrPart: "resolver_version mismatch",
		},
		{
			name:        "duplicate_rule_id",
			config:      replaceJSONField(minimalValidConfig, `{"id":"fin_large_bank","priority":100,"industry_prefix":"FIN_LARGE_BANK","archetype":"mature_large_bank"}`, `{"id":"fin_large_bank","priority":100,"industry_prefix":"FIN_LARGE_BANK","archetype":"mature_large_bank"},{"id":"fin_large_bank","priority":90,"industry_prefix":"FIN_SMALL","archetype":"mature_large_bank"}`),
			wantErrPart: "duplicate rule id",
		},
		{
			name:        "rule_references_unknown_archetype",
			config:      replaceJSONField(minimalValidConfig, `{"id":"fin_large_bank","priority":100,"industry_prefix":"FIN_LARGE_BANK","archetype":"mature_large_bank"}`, `{"id":"fin_large_bank","priority":100,"industry_prefix":"FIN_LARGE_BANK","archetype":"growth_bank"}`),
			wantErrPart: `archetype "growth_bank"`,
		},
		{
			name:        "negative_maturity_threshold",
			config:      replaceJSONField(minimalValidConfig, `"large_cap_revenue_min_usd": 50000000000`, `"large_cap_revenue_min_usd": -1`),
			wantErrPart: "negative value",
		},
		{
			name: "invalid_archetype_in_profile",
			config: replaceJSONField(minimalValidConfig,
				`"archetype": "mature_large_bank", "maturity": "mature"`,
				`"archetype": "not_a_real_archetype", "maturity": "mature"`),
			wantErrPart: "invalid archetype",
		},
		{
			name: "invalid_maturity_in_profile",
			config: replaceJSONField(minimalValidConfig,
				`"archetype": "mature_large_bank", "maturity": "mature"`,
				`"archetype": "mature_large_bank", "maturity": "not_a_maturity"`),
			wantErrPart: "invalid maturity",
		},
		{
			name: "invalid_revenue_base_method",
			config: replaceJSONField(minimalValidConfig,
				`"revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity"`,
				`"revenue_base_method": "wat", "discount_method": "cost_of_equity"`),
			wantErrPart: "invalid revenue_base_method",
		},
		{
			name: "invalid_terminal_method",
			config: replaceJSONField(minimalValidConfig,
				`"terminal_method": "gordon_growth", "stabilized": true`,
				`"terminal_method": "not_a_method", "stabilized": true`),
			wantErrPart: "invalid terminal_method",
		},
		{
			name: "invalid_discount_method",
			config: replaceJSONField(minimalValidConfig,
				`"discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth"`,
				`"discount_method": "not_a_method",
      "terminal_method": "gordon_growth"`),
			wantErrPart: "invalid discount_method",
		},
		{
			name: "horizon_out_of_range",
			config: replaceJSONField(minimalValidConfig,
				`"horizon_years": 3, "compound_growth_cap": 1.5`,
				`"horizon_years": 999, "compound_growth_cap": 1.5`),
			wantErrPart: "horizon_years out of range",
		},
		{
			name: "compound_growth_cap_too_low_for_horizon",
			config: replaceJSONField(minimalValidConfig,
				`"horizon_years": 3, "compound_growth_cap": 1.5`,
				`"horizon_years": 3, "compound_growth_cap": 0.5`),
			wantErrPart: "compound_growth_cap must be > 1.0",
		},
		{
			// Invariant 10 (T2-P4-W2 item 4): PayoutPath shorter than
			// DividendForecastHorizon must fail load-time validation rather
			// than silently truncate at runtime in ddm.go.
			name: "payout_path_shorter_than_horizon",
			config: replaceJSONField(minimalValidConfig,
				`"payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"payout_path": [0.30, 0.32], "dividend_forecast_horizon": 5,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`),
			wantErrPart: "payout_path length 2 must equal dividend_forecast_horizon 5",
		},
		{
			// Invariant 10 (T2-P4-W2 item 4): PayoutPath longer than
			// DividendForecastHorizon must fail load-time validation; extra
			// elements would be silently ignored by the runtime ddm.go guard.
			name: "payout_path_longer_than_horizon",
			config: replaceJSONField(minimalValidConfig,
				`"payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"payout_path": [0.30, 0.32, 0.34, 0.36, 0.38, 0.40], "dividend_forecast_horizon": 3,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`),
			wantErrPart: "payout_path length 6 must equal dividend_forecast_horizon 3",
		},
		{
			// VAL-1 Phase 5: max_annual_dilution_rate is an optional clamp ceiling
			// (0 ⇒ code default); a value above 1.0 (100%/yr) is never a real
			// calibration and must fail load-time validation (validation.go §128).
			name: "max_annual_dilution_rate_above_ceiling",
			config: replaceJSONField(minimalValidConfig,
				`"stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"stable_dividend_growth": 0.03,
      "max_annual_dilution_rate": 1.5
    },
    "software_like_scaling:standard_growth"`),
			wantErrPart: "max_annual_dilution_rate out of range",
		},
		{
			// Layer A (§6.3): a garbage reinvestment_method enum is a config
			// error, even though the field is optional.
			name: "invalid_reinvestment_method",
			config: replaceJSONField(minimalValidConfig,
				`"stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"stable_dividend_growth": 0.03,
      "reinvestment_method": "teleport_capital"
    },
    "software_like_scaling:standard_growth"`),
			wantErrPart: "invalid reinvestment_method",
		},
		{
			// Layer A (§6.3): sales_to_capital requires a positive start ratio.
			name: "sales_to_capital_missing_start",
			config: replaceJSONField(minimalValidConfig,
				`"stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"stable_dividend_growth": 0.03,
      "reinvestment_method": "sales_to_capital",
      "sales_to_capital_target": 3.0,
      "target_operating_margin": 0.30
    },
    "software_like_scaling:standard_growth"`),
			wantErrPart: "sales_to_capital_start must be > 0",
		},
		{
			// Layer A (§7.3): the converged margin is capped well below any real
			// sector norm; a 90% target is a typo, not a calibration.
			name: "target_operating_margin_above_ceiling",
			config: replaceJSONField(minimalValidConfig,
				`"stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"stable_dividend_growth": 0.03,
      "reinvestment_method": "sales_to_capital",
      "sales_to_capital_start": 2.0,
      "sales_to_capital_target": 3.0,
      "target_operating_margin": 0.90
    },
    "software_like_scaling:standard_growth"`),
			wantErrPart: "target_operating_margin out of range",
		},
		{
			// Layer A (§6.3): sales-to-capital must IMPROVE (target ≥ start).
			name: "sales_to_capital_target_below_start",
			config: replaceJSONField(minimalValidConfig,
				`"stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"stable_dividend_growth": 0.03,
      "reinvestment_method": "sales_to_capital",
      "sales_to_capital_start": 3.0,
      "sales_to_capital_target": 2.0,
      "target_operating_margin": 0.30
    },
    "software_like_scaling:standard_growth"`),
			wantErrPart: "must be ≥ sales_to_capital_start",
		},
		{
			// Layer A (§7.1/§7.3): a maintenance floor at/above the steady-state
			// margin forces non-positive FCF — reject it as an incoherent typo.
			name: "maintenance_floor_at_or_above_margin",
			config: replaceJSONField(minimalValidConfig,
				`"stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"stable_dividend_growth": 0.03,
      "reinvestment_method": "sales_to_capital",
      "sales_to_capital_start": 2.0,
      "sales_to_capital_target": 3.0,
      "maintenance_capex_floor": 0.25,
      "target_operating_margin": 0.20
    },
    "software_like_scaling:standard_growth"`),
			wantErrPart: "must be < target_operating_margin",
		},
		{
			// VAL-3 Phase 3 (C1): a zero terminal_multiple with a non-zero
			// horizon silently zeroes the forward leg; reject it as fail-open.
			name: "terminal_multiple_zero_with_horizon",
			config: replaceJSONField(minimalValidConfig,
				`"horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": 0.8`,
				`"horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": 0`),
			wantErrPart: "terminal_multiple must be > 0",
		},
		{
			// VAL-3 Phase 3 (C1): negative terminal_multiple is a config error.
			name: "terminal_multiple_negative_with_horizon",
			config: replaceJSONField(minimalValidConfig,
				`"horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": 0.8`,
				`"horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": -5`),
			wantErrPart: "terminal_multiple must be > 0",
		},
		{
			// VAL-3 Phase 3 (C1): a terminal_multiple above the sanity ceiling
			// (likely a misplaced decimal, e.g. 240 instead of 24) is rejected.
			name: "terminal_multiple_above_ceiling",
			config: replaceJSONField(minimalValidConfig,
				`"horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": 0.8`,
				`"horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": 240`),
			wantErrPart: "terminal_multiple out of sane range",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "profiles.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.config), 0o644))

			_, err := profile.LoadFromJSON(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErrPart)
		})
	}
}

// TestLoadFromJSON_FileNotFound surfaces the os.ReadFile error path.
func TestLoadFromJSON_FileNotFound(t *testing.T) {
	_, err := profile.LoadFromJSON(filepath.Join(t.TempDir(), "does-not-exist.json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read")
}

// TestResolve_UnknownIndustry_ReturnsFallbackProfile pins the contract
// that a fallback resolution still returns a non-nil ResolvedProfile so
// consumers never have to nil-check the resolved value. Exercises the
// applyFallback path.
func TestResolve_UnknownIndustry_ReturnsFallbackProfile(t *testing.T) {
	reg := mustLoadMinimal(t)
	facts := profile.Facts{
		Industry:           "WEIRD_INDUSTRY",
		IndustryNormalized: "WEIRD_INDUSTRY",
	}
	resolved, trace := reg.Resolve(facts)
	require.NotNil(t, resolved)
	assert.Equal(t, "software_like_scaling:standard_growth", resolved.ProfileID)
	assert.Equal(t, profile.SourceFallback, trace.Source)
	assert.NotEmpty(t, resolved.Trace.ProfileID)
}

// TestResolve_HighGrowthBucket exercises the YoY-above-threshold branch
// of deriveMaturity. With small revenue + 50% YoY, the resolver should
// bucket to high_growth — but the fallback archetype pins to standard
// when it has no high_growth profile entry, so this becomes a Lookup
// miss → fallback path.
func TestResolve_HighGrowthBucket(t *testing.T) {
	reg := mustLoadMinimal(t)
	revenue := 100e6
	yoy := 0.50
	facts := profile.Facts{
		Industry:           "MYSTERY",
		IndustryNormalized: "MYSTERY",
		Revenue:            &revenue,
		RevenueGrowthYoY:   &yoy,
	}
	resolved, trace := reg.Resolve(facts)
	require.NotNil(t, resolved)
	// Wildcard rule fires (Source=fallback). Stage 2 picks high_growth
	// but the fallback archetype has no high_growth entry → Lookup miss
	// → applyFallback returns the standard_growth profile.
	assert.Equal(t, profile.SourceFallback, trace.Source)
}

// TestResolve_NoRevenueSignal exercises the nil-revenue branch of
// deriveMaturity: missing revenue must NOT panic and must fall through
// to standard_growth with the ambiguous-signal HumanReason.
func TestResolve_NoRevenueSignal(t *testing.T) {
	reg := mustLoadMinimal(t)
	facts := profile.Facts{
		Industry:           "FIN_LARGE_BANK",
		IndustryNormalized: "FIN_LARGE_BANK",
	}
	resolved, trace := reg.Resolve(facts)
	require.NotNil(t, resolved)
	// fin_large_bank pins to mature regardless of Stage-2 ambiguity, so
	// the missing-revenue signal is logged in HumanReason but the final
	// maturity is mature.
	assert.Equal(t, profile.MaturityMature, resolved.Maturity)
	assert.Contains(t, trace.HumanReason, "ambiguous_no_revenue_signal")
}

// TestJoinReasons_AllBranches covers joinReasons' three branches: empty
// first arg, empty second arg, both non-empty. Exercised indirectly via
// Resolve but pinned here for explicit coverage of the helper.
func TestResolve_TraceConfigStamping(t *testing.T) {
	reg := mustLoadMinimal(t)
	facts := profile.Facts{
		Industry:           "FIN_LARGE_BANK",
		IndustryNormalized: "FIN_LARGE_BANK",
	}
	_, trace := reg.Resolve(facts)
	assert.Equal(t, "1.0.0", trace.ResolverVersion)
	assert.Equal(t, "1.0.0", trace.ConfigVersion)
	assert.NotEmpty(t, trace.ConfigHash, "config_hash must be stamped onto every trace for replay determinism")
}

// TestLoadFromJSON_PayoutPathLengthInvariant_PositiveCases pins the three
// configurations that MUST NOT trip invariant 10 (T2-P4-W2 item 4):
//
//  1. Happy path: PayoutPath length matches DividendForecastHorizon exactly.
//  2. Edge: DividendForecastHorizon == 0 with non-empty PayoutPath. The
//     predicate requires BOTH `horizon > 0` AND non-empty PayoutPath, so
//     this is intentionally permitted (and harmless — ddm.go's legacy
//     dispatcher gates on horizon == 0 alone).
//  3. Edge: DividendForecastHorizon > 0 with empty PayoutPath. The
//     predicate requires non-empty PayoutPath to trigger; an empty path
//     with a positive horizon is a legitimate "defer payout-path
//     population to a later phase" configuration that should remain
//     loadable.
//
// Each case mutates minimalValidConfig's mature_large_bank:mature entry
// (which ships with horizon=0 + empty PayoutPath) so only the
// invariant-10-relevant fields change between cases.
func TestLoadFromJSON_PayoutPathLengthInvariant_PositiveCases(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{
			name: "matching_lengths_horizon_5",
			config: replaceJSONField(minimalValidConfig,
				`"payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"payout_path": [0.30, 0.32, 0.34, 0.36, 0.38], "dividend_forecast_horizon": 5,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`),
		},
		{
			name: "horizon_zero_with_nonempty_payout_path_predicate_not_fired",
			config: replaceJSONField(minimalValidConfig,
				`"payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"payout_path": [0.30, 0.32, 0.34], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`),
		},
		{
			name: "horizon_positive_with_empty_payout_path_predicate_not_fired",
			config: replaceJSONField(minimalValidConfig,
				`"payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`,
				`"payout_path": [], "dividend_forecast_horizon": 5,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth"`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "profiles.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.config), 0o644))

			reg, err := profile.LoadFromJSON(path)
			require.NoError(t, err, "invariant 10 must NOT fire for this configuration")
			require.NotNil(t, reg)
		})
	}
}

// replaceJSONField performs a literal string replacement in the JSON
// config — sufficient for these tests since the field markers in
// minimalValidConfig are unique substrings. Test helper only.
func replaceJSONField(config, oldStr, newStr string) string {
	return strings.Replace(config, oldStr, newStr, 1)
}
