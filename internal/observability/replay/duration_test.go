package replay

import (
	"testing"
	"time"
)

// TestParseDurationExtended_TableDriven covers the contract: spec-listed
// good cases pass, spec-listed bad cases fail. New cases land here when the
// surface evolves.
func TestParseDurationExtended_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		// Spec §12 unit row: accepts `7d` (= 168h), `30d`, `48h`, `5m`.
		{"7d_168h", "7d", 7 * 24 * time.Hour, false},
		{"30d_720h", "30d", 30 * 24 * time.Hour, false},
		{"48h_passthrough", "48h", 48 * time.Hour, false},
		{"5m_passthrough", "5m", 5 * time.Minute, false},
		// Sanity: smaller std units still work.
		{"100ms_passthrough", "100ms", 100 * time.Millisecond, false},
		{"2s_passthrough", "2s", 2 * time.Second, false},
		{"1h30m_compound_std", "1h30m", 90 * time.Minute, false},
		// Fractional days.
		{"half_day", "0.5d", 12 * time.Hour, false},
		// Spec §12 unit row: rejects `1w`, `1mo`, `1y`, `7days`, `7 d`, empty.
		{"weeks_rejected", "1w", 0, true},
		{"months_rejected", "1mo", 0, true},
		{"years_rejected", "1y", 0, true},
		{"days_word_rejected", "7days", 0, true},
		{"whitespace_rejected", "7 d", 0, true},
		{"empty_rejected", "", 0, true},
		// Pathological inputs.
		{"garbage", "abc", 0, true},
		{"only_suffix", "d", 0, true},
		{"only_unit_h", "h", 0, true},
		{"negative_days_allowed", "-1d", -24 * time.Hour, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDurationExtended(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDurationExtended(%q) succeeded, want error; got=%v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDurationExtended(%q) failed: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDurationExtended(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseDurationExtended_DoesNotAcceptCompoundDays verifies the parser
// rejects compound forms involving `d` (e.g. `1d12h`). This is intentional
// per the package doc — we keep the parser simple by refusing Go-grammar
// extensions that would force a full reimplementation.
func TestParseDurationExtended_DoesNotAcceptCompoundDays(t *testing.T) {
	cases := []string{"1d2h", "1d30m", "0.5d12h"}
	for _, c := range cases {
		// Go 1.22+ scopes range vars per iteration so the previous
		// `c := c` shadow is unnecessary.
		t.Run(c, func(t *testing.T) {
			_, err := ParseDurationExtended(c)
			if err == nil {
				t.Fatalf("ParseDurationExtended(%q) should reject compound days form", c)
			}
		})
	}
}

// TestParseDurationExtended_AcceptsMicroseconds covers the `µs` and `us`
// suffixes — these are Go-standard but rarely used so they need an
// explicit test to stay in the coverage matrix.
func TestParseDurationExtended_AcceptsMicroseconds(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"100us", 100 * time.Microsecond},
		{"100µs", 100 * time.Microsecond},
		{"500ns", 500 * time.Nanosecond},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseDurationExtended(tt.input)
			if err != nil {
				t.Fatalf("ParseDurationExtended(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDurationExtended(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
