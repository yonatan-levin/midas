package profile_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestFacts_PointerSemantics_DistinguishesMissingFromZero pins the
// load-bearing pointer-field convention: nil means "no signal", a pointer
// to zero means "zero is meaningful". Used by the resolver to differentiate
// a pre-revenue biotech (Revenue == &0) from a malformed bundle (Revenue == nil).
func TestFacts_PointerSemantics_DistinguishesMissingFromZero(t *testing.T) {
	var zero float64
	factsZero := profile.Facts{Revenue: &zero}
	factsMissing := profile.Facts{Revenue: nil}

	assert.NotNil(t, factsZero.Revenue, "explicit zero should be a non-nil pointer")
	assert.Equal(t, 0.0, *factsZero.Revenue)
	assert.Nil(t, factsMissing.Revenue, "missing signal should be nil")
}

// TestFacts_IndustryNormalized_UpperCasedTrimmed verifies the test helper
// constructor normalizes industry whitespace + case so the resolver's
// prefix-match runs against a canonical form.
func TestFacts_IndustryNormalized_UpperCasedTrimmed(t *testing.T) {
	facts := profile.NewFactsForTest("  fin_large_bank  ", nil, nil)
	assert.Equal(t, "FIN_LARGE_BANK", facts.IndustryNormalized)
}
