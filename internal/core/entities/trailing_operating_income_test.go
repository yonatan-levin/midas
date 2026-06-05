package entities

import (
	"strings"
	"testing"
	"time"
)

// TestTrailingTwelveMonthsOperatingIncome_FallbackChain mirrors the
// revenue-helper table (RM-1) for the operating-income base used by the
// standard DCF (BUG-015). The source strings are part of the public
// contract — replay tooling and dashboards key off them — so they MUST
// match the revenue helper's vocabulary exactly.
//
// The operating-income metric summed at every tier matches service.go's
// effectiveOI: NormalizedOperatingIncome when positive, else OperatingIncome.
// Each fixture sets NormalizedOperatingIncome so the per-period effective OI
// is unambiguous.
func TestTrailingTwelveMonthsOperatingIncome_FallbackChain(t *testing.T) {
	fixedDate := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		data            map[string]*FinancialData
		expectOI        float64
		expectSource    string
		expectWarnSub   string // empty means warning must be empty
		delta           float64
		wantPositive    bool
		wantNonPositive bool
	}{
		// ---- TTM_4Q: four contiguous quarters in the latest year ----
		{
			name: "four_contiguous_quarters_same_year",
			data: map[string]*FinancialData{
				"2025Q1": {NormalizedOperatingIncome: 100, FilingPeriod: "2025Q1"},
				"2025Q2": {NormalizedOperatingIncome: 110, FilingPeriod: "2025Q2"},
				"2025Q3": {NormalizedOperatingIncome: 120, FilingPeriod: "2025Q3"},
				"2025Q4": {NormalizedOperatingIncome: 130, FilingPeriod: "2025Q4"},
			},
			expectOI:     460,
			expectSource: oiSourceTTM4Q,
			delta:        0.001,
			wantPositive: true,
		},

		// ---- TTM_PRIOR_BRIDGE: partial latest year + prior-year quarters ----
		{
			name: "partial_latest_year_routes_to_bridge",
			data: map[string]*FinancialData{
				"2025Q3": {NormalizedOperatingIncome: 80, FilingPeriod: "2025Q3"},
				"2025Q4": {NormalizedOperatingIncome: 90, FilingPeriod: "2025Q4"},
				"2026Q1": {NormalizedOperatingIncome: 100, FilingPeriod: "2026Q1"},
				"2026Q2": {NormalizedOperatingIncome: 110, FilingPeriod: "2026Q2"},
			},
			expectOI:      380, // 100+110+80+90
			expectSource:  oiSourceTTMPriorBridge,
			expectWarnSub: "partial-year TTM bridged",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- ANNUAL_FY: latest period is FY → passthrough, value unchanged ----
		{
			name: "all_annual_no_quarters_passthrough",
			data: map[string]*FinancialData{
				"2023FY": {NormalizedOperatingIncome: 800, FilingPeriod: "2023FY", FilingDate: fixedDate.AddDate(-2, 0, 0)},
				"2024FY": {NormalizedOperatingIncome: 900, FilingPeriod: "2024FY", FilingDate: fixedDate.AddDate(-1, 0, 0)},
				"2025FY": {NormalizedOperatingIncome: 1000, FilingPeriod: "2025FY", FilingDate: fixedDate},
			},
			expectOI:     1000, // latest FY value returned unchanged
			expectSource: oiSourceAnnualFY,
			delta:        0.001,
			wantPositive: true,
		},

		// ---- ANNUAL_FY fallback when quarters exist but no usable window ----
		{
			name: "gap_in_quarters_falls_back_to_annual",
			data: map[string]*FinancialData{
				"2024Q4": {NormalizedOperatingIncome: 100, FilingPeriod: "2024Q4"},
				"2025Q1": {NormalizedOperatingIncome: 105, FilingPeriod: "2025Q1"},
				"2025Q2": {NormalizedOperatingIncome: 110, FilingPeriod: "2025Q2"},
				"2025Q4": {NormalizedOperatingIncome: 115, FilingPeriod: "2025Q4"}, // Q3 missing → no window, bridge fails
				"2024FY": {NormalizedOperatingIncome: 400, FilingPeriod: "2024FY", FilingDate: fixedDate},
			},
			expectOI:      400,
			expectSource:  oiSourceAnnualFY,
			expectWarnSub: "TTM unavailable",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- ANNUALIZED_QUARTER: single quarter only, no FY, no prior ----
		{
			name: "single_quarter_only",
			data: map[string]*FinancialData{
				"2026Q1": {NormalizedOperatingIncome: 4_359, FilingPeriod: "2026Q1"},
			},
			expectOI:      4_359 * 4, // KO-shaped quarterly OI annualized
			expectSource:  oiSourceAnnualizedQuarter,
			expectWarnSub: "annualized single-quarter",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- INSUFFICIENT_HISTORY: no usable OI at all ----
		{
			name:            "no_data",
			data:            map[string]*FinancialData{},
			expectOI:        0,
			expectSource:    oiSourceInsufficient,
			expectWarnSub:   "insufficient operating-income history",
			delta:           0.001,
			wantNonPositive: true,
		},

		// ---- OperatingIncome fallback when NormalizedOperatingIncome is zero ----
		{
			name: "uses_raw_operating_income_when_normalized_zero",
			data: map[string]*FinancialData{
				"2025Q1": {OperatingIncome: 100, FilingPeriod: "2025Q1"},
				"2025Q2": {OperatingIncome: 110, FilingPeriod: "2025Q2"},
				"2025Q3": {OperatingIncome: 120, FilingPeriod: "2025Q3"},
				"2025Q4": {OperatingIncome: 130, FilingPeriod: "2025Q4"},
			},
			expectOI:     460,
			expectSource: oiSourceTTM4Q,
			delta:        0.001,
			wantPositive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HistoricalFinancialData{Ticker: "TEST", Data: tt.data}

			oi, source, warning := h.TrailingTwelveMonthsOperatingIncome()

			if source != tt.expectSource {
				t.Errorf("source: got %q, want %q (warning=%q)", source, tt.expectSource, warning)
			}
			if tt.wantPositive && oi <= 0 {
				t.Errorf("expected positive OI, got %v", oi)
			}
			if tt.wantNonPositive && oi > 0 {
				t.Errorf("expected non-positive OI, got %v", oi)
			}
			if absDiff(oi, tt.expectOI) > tt.delta {
				t.Errorf("oi: got %v, want %v (±%v)", oi, tt.expectOI, tt.delta)
			}
			if tt.expectWarnSub == "" {
				if warning != "" {
					t.Errorf("expected empty warning, got %q", warning)
				}
			} else if !strings.Contains(warning, tt.expectWarnSub) {
				t.Errorf("warning: got %q, want substring %q", warning, tt.expectWarnSub)
			}
		})
	}
}

// TestTrailingTwelveMonthsOperatingIncome_NilReceiver covers the defensive
// nil guard.
func TestTrailingTwelveMonthsOperatingIncome_NilReceiver(t *testing.T) {
	var h *HistoricalFinancialData
	oi, source, warning := h.TrailingTwelveMonthsOperatingIncome()
	if oi != 0 || source != oiSourceInsufficient || !strings.Contains(warning, "insufficient") {
		t.Fatalf("nil receiver: got (%v, %q, %q), want (0, %q, contains \"insufficient\")",
			oi, source, warning, oiSourceInsufficient)
	}
}

// TestTrailingTwelveMonthsOperatingIncome_FYPassthrough is the canonical
// regression for the FY-latest invariance constraint (BUG-015 §4): when the
// latest filing is an FY, the helper must return that FY's effective OI
// unchanged with source=ANNUAL_FY and no warning — the value DCF would have
// used pre-fix. This keeps FY-latest tickers bit-for-bit.
func TestTrailingTwelveMonthsOperatingIncome_FYPassthrough(t *testing.T) {
	fixedDate := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	h := &HistoricalFinancialData{
		Ticker: "FY_LATEST",
		Data: map[string]*FinancialData{
			"2024FY": {NormalizedOperatingIncome: 27_000, FilingPeriod: "2024FY", FilingDate: fixedDate},
		},
	}
	oi, source, warning := h.TrailingTwelveMonthsOperatingIncome()
	if source != oiSourceAnnualFY {
		t.Fatalf("expected ANNUAL_FY for FY-latest; got %q (warning=%q)", source, warning)
	}
	if oi != 27_000 {
		t.Errorf("oi: got %v, want 27000 (FY value unchanged)", oi)
	}
	if warning != "" {
		t.Errorf("warning: got %q, want empty (clean FY passthrough)", warning)
	}
}

// TestTrailingTwelveMonthsOperatingIncome_BridgeWinsOnPartialYear pins the
// bridge-first ordering, mirroring the revenue helper's contract.
func TestTrailingTwelveMonthsOperatingIncome_BridgeWinsOnPartialYear(t *testing.T) {
	h := &HistoricalFinancialData{
		Ticker: "IPO_PARTIAL",
		Data: map[string]*FinancialData{
			"2025Q3": {NormalizedOperatingIncome: 80, FilingPeriod: "2025Q3"},
			"2025Q4": {NormalizedOperatingIncome: 90, FilingPeriod: "2025Q4"},
			"2026Q1": {NormalizedOperatingIncome: 100, FilingPeriod: "2026Q1"},
			"2026Q2": {NormalizedOperatingIncome: 110, FilingPeriod: "2026Q2"},
		},
	}
	oi, source, warning := h.TrailingTwelveMonthsOperatingIncome()
	if source != oiSourceTTMPriorBridge {
		t.Fatalf("expected TTM_PRIOR_BRIDGE for partial-year; got %q (warning=%q)", source, warning)
	}
	if oi != 380 {
		t.Errorf("oi: got %v, want 380", oi)
	}
	if !strings.Contains(warning, "partial-year TTM bridged") {
		t.Errorf("warning: got %q, want substring %q", warning, "partial-year TTM bridged")
	}
}
