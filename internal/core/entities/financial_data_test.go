package entities

import (
	"encoding/json"
	"testing"
	"time"
)

// TestFinancialData_MinorityAndPreferred_JSONRoundTrip pins the M-1d entity
// extension: MinorityInterest and PreferredEquity must serialize through the
// canonical JSON tags `minority_interest` and `preferred_equity` and round-trip
// without loss. Downstream log consumers and persisted blobs depend on these
// exact tag names.
func TestFinancialData_MinorityAndPreferred_JSONRoundTrip(t *testing.T) {
	original := FinancialData{
		Ticker:           "TEST",
		MinorityInterest: 1234.56,
		PreferredEquity:  789.01,
	}

	encoded, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify canonical tag names appear in the serialized form.
	if !contains(encoded, []byte(`"minority_interest":1234.56`)) {
		t.Errorf("missing or wrong-tagged minority_interest in JSON: %s", encoded)
	}
	if !contains(encoded, []byte(`"preferred_equity":789.01`)) {
		t.Errorf("missing or wrong-tagged preferred_equity in JSON: %s", encoded)
	}

	var decoded FinancialData
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.MinorityInterest != original.MinorityInterest {
		t.Errorf("MinorityInterest round-trip: got %v, want %v",
			decoded.MinorityInterest, original.MinorityInterest)
	}
	if decoded.PreferredEquity != original.PreferredEquity {
		t.Errorf("PreferredEquity round-trip: got %v, want %v",
			decoded.PreferredEquity, original.PreferredEquity)
	}
}

// contains is a tiny substring helper to keep the test self-contained.
func contains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestGetSortedPeriods(t *testing.T) {
	tests := []struct {
		name     string
		periods  []string
		expected []string
	}{
		{
			name:     "annual periods only",
			periods:  []string{"2024FY", "2022FY", "2023FY"},
			expected: []string{"2022FY", "2023FY", "2024FY"},
		},
		{
			name:     "quarterly periods only",
			periods:  []string{"2023Q3", "2023Q1", "2023Q4", "2023Q2"},
			expected: []string{"2023Q1", "2023Q2", "2023Q3", "2023Q4"},
		},
		{
			name:     "mixed quarterly and annual",
			periods:  []string{"2023FY", "2022Q4", "2023Q1", "2022FY", "2024Q1"},
			expected: []string{"2022Q4", "2022FY", "2023Q1", "2023FY", "2024Q1"},
		},
		{
			name:     "full multi-year sequence",
			periods:  []string{"2024Q1", "2022FY", "2023Q2", "2023Q1", "2023FY", "2023Q3", "2023Q4"},
			expected: []string{"2022FY", "2023Q1", "2023Q2", "2023Q3", "2023Q4", "2023FY", "2024Q1"},
		},
		{
			name:     "single period",
			periods:  []string{"2023FY"},
			expected: []string{"2023FY"},
		},
		{
			name:     "empty",
			periods:  []string{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HistoricalFinancialData{
				Ticker: "TEST",
				Data:   make(map[string]*FinancialData),
			}
			for _, p := range tt.periods {
				h.Data[p] = &FinancialData{FilingPeriod: p}
			}

			result := h.GetSortedPeriods()

			if len(result) != len(tt.expected) {
				t.Fatalf("got %d periods, want %d", len(result), len(tt.expected))
			}
			for i, got := range result {
				if got != tt.expected[i] {
					t.Errorf("position %d: got %q, want %q", i, got, tt.expected[i])
				}
			}
		})
	}
}

func TestParsePeriodKey(t *testing.T) {
	tests := []struct {
		period       string
		expectedYear int
		expectedSub  int
	}{
		{"2023FY", 2023, 5},
		{"2023Q1", 2023, 1},
		{"2023Q2", 2023, 2},
		{"2023Q3", 2023, 3},
		{"2023Q4", 2023, 4},
		{"2024FY", 2024, 5},
		{"invalid", 0, 0},
		{"", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			year, sub := parsePeriodKey(tt.period)
			if year != tt.expectedYear || sub != tt.expectedSub {
				t.Errorf("parsePeriodKey(%q) = (%d, %d), want (%d, %d)",
					tt.period, year, sub, tt.expectedYear, tt.expectedSub)
			}
		})
	}
}

func TestGetOperatingIncomeHistory_UsesCorrectOrder(t *testing.T) {
	// Verifies that growth rate calculation receives values in chronological order
	h := &HistoricalFinancialData{
		Ticker: "TEST",
		Data: map[string]*FinancialData{
			"2024FY": {NormalizedOperatingIncome: 300, FilingDate: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
			"2022FY": {NormalizedOperatingIncome: 100, FilingDate: time.Date(2023, 2, 1, 0, 0, 0, 0, time.UTC)},
			"2023FY": {NormalizedOperatingIncome: 200, FilingDate: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
		},
	}

	income := h.GetOperatingIncomeHistory(5)

	if len(income) != 3 {
		t.Fatalf("got %d values, want 3", len(income))
	}
	// Should be ascending chronological: 100, 200, 300
	if income[0] != 100 || income[1] != 200 || income[2] != 300 {
		t.Errorf("got %v, want [100 200 300] (chronological order)", income)
	}
}
