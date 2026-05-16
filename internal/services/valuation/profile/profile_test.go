package profile_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestAssumptionProfile_AllFieldsPresent locks in the full 14-field JSON
// shape from spec §3.1. Drift on any field tag or enum spelling fails this
// test loudly so downstream P1-P4 worktrees never read stale shape.
func TestAssumptionProfile_AllFieldsPresent(t *testing.T) {
	raw := []byte(`{
        "profile_id": "mature_large_bank:mature",
        "archetype": "mature_large_bank",
        "maturity": "mature",
        "horizon_years": 3,
        "compound_growth_cap": 1.5,
        "revenue_base_method": "raw_ttm",
        "discount_method": "cost_of_equity",
        "terminal_method": "gordon_growth",
        "stabilized": true,
        "fade_years": 0,
        "terminal_multiple": 0.8,
        "dps_growth_cap": 0.08,
        "payout_path": [],
        "dividend_forecast_horizon": 0,
        "stable_dividend_growth": 0.03
    }`)
	var p profile.AssumptionProfile
	err := json.Unmarshal(raw, &p)
	assert.NoError(t, err)

	assert.Equal(t, "mature_large_bank:mature", p.ProfileID)
	assert.Equal(t, profile.ArchetypeMatureLargeBank, p.Archetype)
	assert.Equal(t, profile.MaturityMature, p.Maturity)
	assert.Equal(t, 3, p.HorizonYears)
	assert.InEpsilon(t, 1.5, p.CompoundGrowthCap, 1e-9)
	assert.Equal(t, profile.RevenueBaseRawTTM, p.RevenueBaseMethod)
	assert.Equal(t, profile.DiscountCostOfEquity, p.DiscountMethod)
	assert.Equal(t, profile.TerminalGordonGrowth, p.TerminalMethod)
	assert.True(t, p.Stabilized)
	assert.Equal(t, 0, p.FadeYears)
	assert.InEpsilon(t, 0.8, p.TerminalMultiple, 1e-9)
	assert.InEpsilon(t, 0.08, p.DPSGrowthCap, 1e-9)
	assert.Empty(t, p.PayoutPath)
	assert.Equal(t, 0, p.DividendForecastHorizon)
	assert.InEpsilon(t, 0.03, p.StableDividendGrowth, 1e-9)
}

// TestResolvedProfile_IsLegacyMatureLargeBankDDM pins the bit-for-bit
// preservation predicate that VAL-2 models consult to take the legacy
// single-stage Gordon branch (spec §3.1 final paragraph). Triplet of
// nil / archetype-mismatch / horizon-nonzero must all return false.
func TestResolvedProfile_IsLegacyMatureLargeBankDDM(t *testing.T) {
	cases := []struct {
		name string
		rp   *profile.ResolvedProfile
		want bool
	}{
		{"nil", nil, false},
		{"horizon_zero_mature_bank", &profile.ResolvedProfile{
			AssumptionProfile: profile.AssumptionProfile{
				Archetype:               profile.ArchetypeMatureLargeBank,
				DividendForecastHorizon: 0,
			},
		}, true},
		{"horizon_zero_wrong_archetype", &profile.ResolvedProfile{
			AssumptionProfile: profile.AssumptionProfile{
				Archetype:               profile.ArchetypeGrowthBank,
				DividendForecastHorizon: 0,
			},
		}, false},
		{"mature_bank_nonzero_horizon", &profile.ResolvedProfile{
			AssumptionProfile: profile.AssumptionProfile{
				Archetype:               profile.ArchetypeMatureLargeBank,
				DividendForecastHorizon: 5,
			},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.rp.IsLegacyMatureLargeBankDDM())
		})
	}
}
