package main

import "testing"

// TestParseDatasetDate covers the workbook "Date updated:" cell decoder. The
// real psdata.xls carries a "YYYY.MM" string (the original plan's Excel-serial
// premise did not match the file), canonicalized to the first of the month.
func TestParseDatasetDate(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "2026.01", want: "2026-01-01"},     // the current psdata.xls value
		{in: "  2025.12  ", want: "2025-12-01"}, // tolerant of surrounding space
		{in: "2024.06", want: "2024-06-01"},
		{in: "not-a-date", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseDatasetDate(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseDatasetDate(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDatasetDate(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parseDatasetDate(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestRoundTo pins the EV/Sales precision normalization that lets the tool
// deterministically regenerate the committed 4-decimal table.
func TestRoundTo(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{2.1248944823347125, 2.1249},
		{1.026, 1.026},
		{15.70055, 15.7006},
	}
	for _, c := range cases {
		if got := roundTo(c.in, 4); got != c.want {
			t.Errorf("roundTo(%v, 4) = %v, want %v", c.in, got, c.want)
		}
	}
}
