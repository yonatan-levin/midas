package industry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassify_VAL3P1_REITSubsectors pins the REIT-subsector keyword
// classifier that drives the FFO model's per-subsector P/FFO multiple and
// cap-rate lookup (VAL-3 Phase 1 + Phase 4).
//
// The classifier keys off HistoricalFinancialData.CompanyName — REIT SIC
// codes (6798) lack the granularity to distinguish residential vs industrial
// vs data center, but the company name almost always carries the signal.
// Tested tickers cover all eight subsectors plus the no-match fall-through
// to the parent RESTATE label (where the FFO model's default 15x applies).
func TestClassify_VAL3P1_REITSubsectors(t *testing.T) {
	classifier := newTestClassifier(t)

	tests := []struct {
		name        string
		companyName string
		expected    string // result.Industry
	}{
		// Data center.
		// NOTE: "Digital Realty Trust" is intentionally excluded — the TECH
		// parent's pattern "\b(tech|software|digital|cyber)\b" outranks RESTATE
		// (priority 100 vs 65) on company-name matching, so DLR currently lands
		// under TECH despite SIC 6798. Fixing that is out of scope for VAL-3 P1
		// (would require SIC > keyword priority globally — see
		// docs/refactoring/spec/industry-classification-unification-spec.md). The
		// "data center" / "data centre" / "interconnection" keywords still fire
		// reliably for tickers without "digital" in their name.
		{name: "Equinix → REIT_DATACENTER", companyName: "Equinix, Inc.", expected: "REIT_DATACENTER"},
		{name: "Generic Data Center REIT → REIT_DATACENTER", companyName: "GreenStar Data Center Holdings", expected: "REIT_DATACENTER"},
		// Cell tower
		{name: "American Tower → REIT_CELLTOWER", companyName: "American Tower Corporation", expected: "REIT_CELLTOWER"},
		{name: "Crown Castle → REIT_CELLTOWER", companyName: "Crown Castle Inc.", expected: "REIT_CELLTOWER"},
		// Industrial
		{name: "Prologis → REIT_INDUSTRIAL", companyName: "Prologis Inc.", expected: "REIT_INDUSTRIAL"},
		// Residential
		{name: "Equity Residential → REIT_RESIDENTIAL", companyName: "Equity Residential Properties Trust", expected: "REIT_RESIDENTIAL"},
		{name: "AvalonBay → REIT_RESIDENTIAL", companyName: "AvalonBay Communities Inc.", expected: "REIT_RESIDENTIAL"},
		// Healthcare
		{name: "Welltower → REIT_HEALTHCARE", companyName: "Welltower Inc.", expected: "REIT_HEALTHCARE"},
		// Retail
		{name: "Simon Property → REIT_RETAIL", companyName: "Simon Property Group Inc.", expected: "REIT_RETAIL"},
		{name: "Kimco → REIT_RETAIL", companyName: "Kimco Realty Corporation", expected: "REIT_RETAIL"},
		// Office
		{name: "Boston Properties → REIT_OFFICE", companyName: "Boston Properties Inc.", expected: "REIT_OFFICE"},
		{name: "Vornado → REIT_OFFICE", companyName: "Vornado Realty Trust", expected: "REIT_OFFICE"},
		// Specialty (VAL-7): self-storage, billboard, prison/corrections, timber.
		// Wires the previously-inert REIT_SPECIALTY config so these tickers stop
		// falling through to the 15x / 6% default in the FFO model.
		{name: "Public Storage → REIT_SPECIALTY (self-storage)", companyName: "Public Storage", expected: "REIT_SPECIALTY"},
		{name: "Lamar Advertising → REIT_SPECIALTY (billboard)", companyName: "Lamar Advertising Company", expected: "REIT_SPECIALTY"},
		{name: "CoreCivic → REIT_SPECIALTY (prison/corrections)", companyName: "CoreCivic, Inc.", expected: "REIT_SPECIALTY"},
		{name: "Weyerhaeuser → REIT_SPECIALTY (timber)", companyName: "Weyerhaeuser Company", expected: "REIT_SPECIALTY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// SIC 6798 (REITs) ensures the parent RESTATE matches first; the
			// subsector then refines via the company-name keyword pass.
			result, err := classifier.Classify(context.Background(), "6798", "", tt.companyName)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Industry,
				"REIT %q should classify as %s, got %s", tt.companyName, tt.expected, result.Industry)
			assert.Equal(t, "RESTATE", result.Sector,
				"REIT subsector parent must remain RESTATE, got %s", result.Sector)
		})
	}
}

// TestClassify_VAL3P1_REIT_UnmatchedFallsToRESTATE pins the no-keyword-match
// branch — a generic REIT (SIC 6798, no recognizable subsector keyword in
// the name) must stay at the parent RESTATE label so the FFO model uses its
// default 15x multiple rather than mis-pricing it as a specific subsector.
func TestClassify_VAL3P1_REIT_UnmatchedFallsToRESTATE(t *testing.T) {
	classifier := newTestClassifier(t)

	// Carefully chosen name: no subsector keyword present.
	result, err := classifier.Classify(context.Background(), "6798", "", "Generic Trust Holdings")
	require.NoError(t, err)
	assert.Equal(t, "RESTATE", result.Industry,
		"REIT with no subsector keyword must stay at parent RESTATE")
	assert.Empty(t, result.SubIndustry,
		"SubIndustry must be empty when no REIT subsector matched")
}
