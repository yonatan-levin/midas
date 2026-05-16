package profile_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile/testhelpers"
)

// TestResolve_JPM_ResolvesToMatureLargeBank pins the load-bearing JPM
// classification: a large bank with stable growth must resolve to the
// mature_large_bank:mature profile so the legacy single-stage Gordon
// branch fires (bit-for-bit DDM preservation invariant).
func TestResolve_JPM_ResolvesToMatureLargeBank(t *testing.T) {
	reg := testhelpers.MustLoadFullFixture(t)
	revenue := 150e9
	oi := 60e9
	yoy := 0.05
	facts := profile.Facts{
		Industry:           "FIN_LARGE_BANK",
		IndustryNormalized: "FIN_LARGE_BANK",
		Revenue:            &revenue,
		OperatingIncome:    &oi,
		RevenueGrowthYoY:   &yoy,
	}
	resolved, trace := reg.Resolve(facts)
	require.NotNil(t, resolved)

	assert.Equal(t, profile.ArchetypeMatureLargeBank, resolved.Archetype)
	assert.Equal(t, profile.MaturityMature, resolved.Maturity)
	assert.Equal(t, profile.SourceExplicit, trace.Source)
	assert.Equal(t, "fin_large_bank", trace.MatchedRuleID)
	assert.True(t, resolved.IsLegacyMatureLargeBankDDM())
}

// TestResolve_UnknownIndustry_FallsBackWithTrace verifies the wildcard
// fallback path: an industry with no explicit rule resolves to the
// fallback archetype and the trace marks Source=fallback even though
// Stage 1 technically matched the wildcard.
func TestResolve_UnknownIndustry_FallsBackWithTrace(t *testing.T) {
	reg := testhelpers.MustLoadFullFixture(t)
	facts := profile.Facts{
		Industry:           "MYSTERY_SECTOR",
		IndustryNormalized: "MYSTERY_SECTOR",
	}
	resolved, trace := reg.Resolve(facts)
	require.NotNil(t, resolved)
	assert.Equal(t, profile.ArchetypeSoftwareLikeScaling, resolved.Archetype)
	assert.Equal(t, profile.SourceFallback, trace.Source)
	assert.Equal(t, "fallback_default", trace.MatchedRuleID)
}

// TestResolve_CyclicalTroughOverride_NegativeOI verifies Stage-1b: a
// cyclical_mid_cycle archetype (e.g. semis) with negative operating
// income flips to cyclical_trough so trough calibrations apply.
func TestResolve_CyclicalTroughOverride_NegativeOI(t *testing.T) {
	reg := testhelpers.MustLoadFullFixture(t)
	revenue := 600e6
	oiNeg := -100e6
	facts := profile.Facts{
		Industry:           "MFG_SEMI",
		IndustryNormalized: "MFG_SEMI",
		Revenue:            &revenue,
		OperatingIncome:    &oiNeg,
	}
	resolved, trace := reg.Resolve(facts)
	require.NotNil(t, resolved)
	assert.Equal(t, profile.ArchetypeCyclicalTrough, resolved.Archetype)
	assert.Contains(t, trace.HumanReason, "cyclical_trough_override")
}

// TestResolve_RuleOrderingDeterministic verifies the priority ordering:
// the higher-priority fin_large_bank (priority 100) wins over the
// lower-priority fin_generic (priority 50) when both could match.
func TestResolve_RuleOrderingDeterministic(t *testing.T) {
	reg := testhelpers.MustLoadFullFixture(t)
	facts := profile.Facts{
		Industry:           "FIN_LARGE_BANK",
		IndustryNormalized: "FIN_LARGE_BANK",
	}
	_, trace := reg.Resolve(facts)
	assert.Equal(t, "fin_large_bank", trace.MatchedRuleID,
		"higher-priority rule must win over fin_generic")
}
