package entities

import (
	"testing"
	"time"
)

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
