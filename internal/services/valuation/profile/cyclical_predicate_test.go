package profile_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestResolvedProfile_IsCyclicalArchetype pins the VAL-1 Phase 3 gate: only
// the two cyclical archetypes are cyclical; everything else (and a nil
// receiver) is not.
func TestResolvedProfile_IsCyclicalArchetype(t *testing.T) {
	tests := []struct {
		name      string
		archetype profile.Archetype
		want      bool
	}{
		{"cyclical_mid_cycle", profile.ArchetypeCyclicalMidCycle, true},
		{"cyclical_trough", profile.ArchetypeCyclicalTrough, true},
		{"mature_large_scale", profile.ArchetypeMatureLargeScale, false},
		{"software_like_scaling", profile.ArchetypeSoftwareLikeScaling, false},
		{"hypergrowth_profitable", profile.ArchetypeHypergrowthProfitable, false},
		{"empty", profile.Archetype(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := &profile.ResolvedProfile{}
			rp.Archetype = tt.archetype
			assert.Equal(t, tt.want, rp.IsCyclicalArchetype())
		})
	}
}

// TestResolvedProfile_IsCyclicalArchetype_NilReceiver guards the test-path
// case where resolution did not run (service.go passes a possibly-nil
// *ResolvedProfile to the seam).
func TestResolvedProfile_IsCyclicalArchetype_NilReceiver(t *testing.T) {
	var rp *profile.ResolvedProfile
	assert.False(t, rp.IsCyclicalArchetype())
}
