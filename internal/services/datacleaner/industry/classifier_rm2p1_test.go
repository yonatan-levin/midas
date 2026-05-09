package industry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassify_RM2P1_NewSubIndustries pins the SIC → sub-industry mappings
// added in RM-2 Phase 1:
//   - SIC 3674/3672/3677 → MFG_SEMI (semiconductors lifted out of generic MFG)
//   - SIC 6020/6021/6022/6029/6035/6036 → FIN_BANK (commercial banks)
//   - SIC 6311/6321/6411 → FIN_INSURANCE (insurance carriers)
//
// These codes drive the EV/Revenue table lookup in
// internal/services/valuation/models/revenue_multiple.go::getMultiple. Without
// the sub-industry refinement, every fabless semi silently fell back to the
// MFG default (1.5x) instead of the calibrated 6.5x — the failure that
// triggered the tracker.
func TestClassify_RM2P1_NewSubIndustries(t *testing.T) {
	classifier := newTestClassifier(t)

	tests := []struct {
		name        string
		sicCode     string
		expectedSub string
		expectedSec string
	}{
		// Semiconductors
		{name: "SIC 3674 (semiconductors) → MFG_SEMI", sicCode: "3674", expectedSub: "MFG_SEMI", expectedSec: "MFG"},
		{name: "SIC 3672 (printed circuit boards) → MFG_SEMI", sicCode: "3672", expectedSub: "MFG_SEMI", expectedSec: "MFG"},
		{name: "SIC 3677 (electronic coils) → MFG_SEMI", sicCode: "3677", expectedSub: "MFG_SEMI", expectedSec: "MFG"},
		// Commercial banks
		{name: "SIC 6020 (state commercial banks) → FIN_BANK", sicCode: "6020", expectedSub: "FIN_BANK", expectedSec: "FIN"},
		{name: "SIC 6021 (national commercial banks) → FIN_BANK", sicCode: "6021", expectedSub: "FIN_BANK", expectedSec: "FIN"},
		{name: "SIC 6022 (state commercial banks - other) → FIN_BANK", sicCode: "6022", expectedSub: "FIN_BANK", expectedSec: "FIN"},
		{name: "SIC 6029 (commercial banks NEC) → FIN_BANK", sicCode: "6029", expectedSub: "FIN_BANK", expectedSec: "FIN"},
		// Insurance
		{name: "SIC 6311 (life insurance) → FIN_INSURANCE", sicCode: "6311", expectedSub: "FIN_INSURANCE", expectedSec: "FIN"},
		{name: "SIC 6321 (accident and health insurance) → FIN_INSURANCE", sicCode: "6321", expectedSub: "FIN_INSURANCE", expectedSec: "FIN"},
		{name: "SIC 6411 (insurance agents and brokers) → FIN_INSURANCE", sicCode: "6411", expectedSub: "FIN_INSURANCE", expectedSec: "FIN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(context.Background(), tt.sicCode, "", "")
			require.NoError(t, err)
			assert.Equal(t, tt.expectedSub, result.Industry,
				"SIC %s should refine to sub-industry %s, got %s", tt.sicCode, tt.expectedSub, result.Industry)
			assert.Equal(t, tt.expectedSec, result.Sector,
				"SIC %s parent sector should be %s, got %s", tt.sicCode, tt.expectedSec, result.Sector)
			assert.Equal(t, tt.expectedSub, result.SubIndustry,
				"SIC %s should populate SubIndustry with %s", tt.sicCode, tt.expectedSub)
		})
	}
}
