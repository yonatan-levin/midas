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

// TestHistoricalFinancialData_RecentYoYGrowth pins the Tier 2 P0b entity
// helper used by service.go::performValuation to populate
// profile.Facts.RevenueGrowthYoY. Computes (latest - prior) / prior over the
// two most recent annual (FY) periods.
//
// Returns nil when:
//   - fewer than 2 annual periods are available
//   - the prior period's revenue is zero (cannot compute growth from zero base)
//
// All other branches return a non-nil pointer (zero is a valid result).
func TestHistoricalFinancialData_RecentYoYGrowth(t *testing.T) {
	cases := []struct {
		name        string
		annualData  map[string]*FinancialData
		wantNil     bool
		wantValue   float64
		wantEpsilon float64
	}{
		{
			name: "two_periods_positive_growth",
			annualData: map[string]*FinancialData{
				"2024FY": {Revenue: 110_000_000},
				"2023FY": {Revenue: 100_000_000},
			},
			wantValue:   0.10,
			wantEpsilon: 1e-9,
		},
		{
			name: "two_periods_negative_growth",
			annualData: map[string]*FinancialData{
				"2024FY": {Revenue: 90_000_000},
				"2023FY": {Revenue: 100_000_000},
			},
			wantValue:   -0.10,
			wantEpsilon: 1e-9,
		},
		{
			name: "one_period_insufficient",
			annualData: map[string]*FinancialData{
				"2024FY": {Revenue: 100_000_000},
			},
			wantNil: true,
		},
		{
			name: "zero_prior_revenue",
			annualData: map[string]*FinancialData{
				"2024FY": {Revenue: 100_000_000},
				"2023FY": {Revenue: 0},
			},
			wantNil: true, // cannot compute growth from zero base
		},
		{
			name:       "no_periods_at_all",
			annualData: map[string]*FinancialData{},
			wantNil:    true,
		},
		{
			name: "three_periods_uses_two_most_recent",
			annualData: map[string]*FinancialData{
				"2024FY": {Revenue: 220_000_000}, // most recent
				"2023FY": {Revenue: 200_000_000}, // prior
				"2022FY": {Revenue: 100_000_000}, // older — must be ignored
			},
			wantValue:   0.10, // (220 - 200) / 200
			wantEpsilon: 1e-9,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &HistoricalFinancialData{
				Ticker: "TEST",
				Data:   tc.annualData,
			}
			yoy := h.RecentYoYGrowth()
			if tc.wantNil {
				if yoy != nil {
					t.Errorf("RecentYoYGrowth: expected nil, got %v", *yoy)
				}
				return
			}
			if yoy == nil {
				t.Fatalf("RecentYoYGrowth: expected non-nil, got nil")
			}
			diff := *yoy - tc.wantValue
			if diff < 0 {
				diff = -diff
			}
			// Compare against an absolute tolerance — InEpsilon-equivalent
			// hand-rolled to avoid pulling in testify here.
			if diff > tc.wantEpsilon {
				t.Errorf("RecentYoYGrowth: got %v, want %v (diff %v > eps %v)",
					*yoy, tc.wantValue, diff, tc.wantEpsilon)
			}
		})
	}
}

// TestHistoricalFinancialData_RecentYoYGrowth_NilReceiver — the method must
// be nil-safe so callers can chain it on a maybe-nil historical without
// guarding. Returns nil (no signal) per the Facts.RevenueGrowthYoY contract.
func TestHistoricalFinancialData_RecentYoYGrowth_NilReceiver(t *testing.T) {
	var h *HistoricalFinancialData
	if got := h.RecentYoYGrowth(); got != nil {
		t.Errorf("nil receiver must return nil, got %v", *got)
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
