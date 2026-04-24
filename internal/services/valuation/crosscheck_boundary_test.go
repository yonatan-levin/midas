package valuation

import "testing"

// TestLookupMultiple_UnderscoreBoundary pins B-2: the longest-prefix match
// must require a code boundary so "TECHNOLOGY" does not silently match key
// "TECH". Matches at the end of the string or before an underscore qualify.
func TestLookupMultiple_UnderscoreBoundary(t *testing.T) {
	multiples := map[string]float64{
		"default":   10.0,
		"TECH":      18.0,
		"TECH_SAAS": 22.0,
		"FIN":       12.0,
	}

	tests := []struct {
		name     string
		industry string
		want     float64
	}{
		{"exact TECH", "TECH", 18.0},
		{"exact TECH_SAAS", "TECH_SAAS", 22.0},
		{"TECH_SAAS_CLOUD longest-prefix wins", "TECH_SAAS_CLOUD", 22.0},
		{"TECHNOLOGY must NOT hit TECH; falls to default", "TECHNOLOGY", 10.0},
		{"FINESSE must NOT hit FIN; falls to default", "FINESSE", 10.0},
		{"unknown falls to default", "ZZZ", 10.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LookupMultiple(multiples, tc.industry)
			if got != tc.want {
				t.Fatalf("LookupMultiple(%q) = %v, want %v", tc.industry, got, tc.want)
			}
		})
	}
}
