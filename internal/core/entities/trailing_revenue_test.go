package entities

import (
	"strings"
	"testing"
	"time"
)

// TestTrailingTwelveMonthsRevenue_FallbackChain exercises every documented
// path in the fallback chain (RM-1 spec, scenarios T1-T9 plus the
// TTM_PRIOR_BRIDGE bridge). The table is the single source of truth for
// the helper's behavior — replay tooling and dashboards key off the
// `source` strings, so they MUST match exactly.
//
// Ordering note: TTM_PRIOR_BRIDGE runs FIRST in the public chain so the
// partial-year IPO shape is reported via source=TTM_PRIOR_BRIDGE rather
// than being silently absorbed into TTM_4Q. Fixtures where the latest
// year has 1-3 quarters AND the prior year supplies the corresponding
// (4-N) quarters therefore route to TTM_PRIOR_BRIDGE; full-year shapes
// (latest year has 4 quarters) decline the bridge and route to TTM_4Q.
func TestTrailingTwelveMonthsRevenue_FallbackChain(t *testing.T) {
	// fixedDate gives FY rows a deterministic FilingDate so the FY warning
	// (which embeds the date) compares stably across platforms.
	fixedDate := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		data            map[string]*FinancialData
		expectRevenue   float64
		expectSource    string
		expectWarnSub   string  // empty means warning must be empty
		delta           float64 // tolerance for float comparison
		wantPositive    bool    // helper must return revenue > 0
		wantNonPositive bool    // helper must return revenue <= 0
	}{
		// ---- T1: 4 trailing quarters present, contiguous → TTM_4Q ----
		{
			name: "T1_four_contiguous_quarters_same_year",
			data: map[string]*FinancialData{
				"2025Q1": {Revenue: 100, FilingPeriod: "2025Q1"},
				"2025Q2": {Revenue: 110, FilingPeriod: "2025Q2"},
				"2025Q3": {Revenue: 120, FilingPeriod: "2025Q3"},
				"2025Q4": {Revenue: 130, FilingPeriod: "2025Q4"},
			},
			// Latest year (2025) has 4 quarters → bridge declines, TTM_4Q wins.
			expectRevenue: 460,
			expectSource:  revenueSourceTTM4Q,
			expectWarnSub: "",
			delta:         0.001,
			wantPositive:  true,
		},
		{
			// Cross-fiscal-year-boundary case where the latest year is partial:
			// 2025 has Q1 only, 2024 supplies Q2+Q3+Q4. With the bridge running
			// first, this fixture deliberately routes to TTM_PRIOR_BRIDGE — the
			// numeric answer is identical to what TTM_4Q would have produced
			// (sum of the same 4 quarters), but the source string preserves
			// the audit-trail signal that the latest year is partial.
			name: "T1b_partial_latest_year_routes_to_bridge",
			data: map[string]*FinancialData{
				"2024Q2": {Revenue: 90, FilingPeriod: "2024Q2"},
				"2024Q3": {Revenue: 95, FilingPeriod: "2024Q3"},
				"2024Q4": {Revenue: 100, FilingPeriod: "2024Q4"},
				"2025Q1": {Revenue: 105, FilingPeriod: "2025Q1"},
			},
			expectRevenue: 390,
			expectSource:  revenueSourceTTMPriorBridge,
			expectWarnSub: "partial-year TTM bridged",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- T2: 4 quarters with one missing → fall back to ANNUAL_FY ----
		{
			name: "T2_gap_in_quarters_falls_back_to_annual",
			data: map[string]*FinancialData{
				// Q3 is missing → no contiguous 4-quarter window AND
				// the bridge cannot be constructed (latest year 2025 has
				// Q1+Q2+Q4, not a Q1-start contiguous run).
				"2024Q4": {Revenue: 100, FilingPeriod: "2024Q4"},
				"2025Q1": {Revenue: 105, FilingPeriod: "2025Q1"},
				"2025Q2": {Revenue: 110, FilingPeriod: "2025Q2"},
				"2025Q4": {Revenue: 115, FilingPeriod: "2025Q4"},
				"2024FY": {Revenue: 400, FilingPeriod: "2024FY", FilingDate: fixedDate},
			},
			expectRevenue: 400,
			expectSource:  revenueSourceAnnualFY,
			expectWarnSub: "TTM unavailable",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- T3: only 1 quarter, no FY, no prior year → ANNUALIZED_QUARTER ----
		{
			name: "T3_single_quarter_only",
			data: map[string]*FinancialData{
				"2026Q1": {Revenue: 137_188_000, FilingPeriod: "2026Q1"},
			},
			expectRevenue: 137_188_000 * 4,
			expectSource:  revenueSourceAnnualizedQuarter,
			expectWarnSub: "annualized single-quarter",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- T4: only 2 quarters, no FY, no prior year → ANNUALIZED_QUARTER (×2) ----
		{
			name: "T4_two_quarters_no_fy",
			data: map[string]*FinancialData{
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
			},
			expectRevenue: 420, // (100+110)*2
			expectSource:  revenueSourceAnnualizedQuarter,
			expectWarnSub: "annualized single-quarter",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- T5: all annual periods (no quarters) → ANNUAL_FY clean ----
		{
			name: "T5_all_annual_no_quarters",
			data: map[string]*FinancialData{
				"2023FY": {Revenue: 800, FilingPeriod: "2023FY", FilingDate: fixedDate.AddDate(-2, 0, 0)},
				"2024FY": {Revenue: 900, FilingPeriod: "2024FY", FilingDate: fixedDate.AddDate(-1, 0, 0)},
				"2025FY": {Revenue: 1000, FilingPeriod: "2025FY", FilingDate: fixedDate},
			},
			expectRevenue: 1000,
			expectSource:  revenueSourceAnnualFY,
			expectWarnSub: "",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- T6: no revenue data at all → INSUFFICIENT_HISTORY ----
		{
			name:            "T6_no_data",
			data:            map[string]*FinancialData{},
			expectRevenue:   0,
			expectSource:    revenueSourceInsufficient,
			expectWarnSub:   "insufficient revenue history",
			delta:           0.001,
			wantNonPositive: true,
		},
		{
			// All periods present but with zero revenue — same INSUFFICIENT
			// outcome (the helper rejects non-positive revenue at every tier
			// to avoid silently producing $0 fair values).
			name: "T6b_all_zero_revenue",
			data: map[string]*FinancialData{
				"2025FY": {Revenue: 0, FilingPeriod: "2025FY", FilingDate: fixedDate},
				"2025Q1": {Revenue: 0, FilingPeriod: "2025Q1"},
			},
			expectRevenue:   0,
			expectSource:    revenueSourceInsufficient,
			expectWarnSub:   "insufficient revenue history",
			delta:           0.001,
			wantNonPositive: true,
		},

		// ---- T8: MXL-shaped fixture (Q1 2026 only) → ANNUALIZED_QUARTER ----
		// $137,188,000 × 4 = $548,752,000 ≈ $549M as called out in the spec.
		// The bridge correctly declines because there is no prior-year history.
		{
			name: "T8_mxl_q1_2026_only",
			data: map[string]*FinancialData{
				"2026Q1": {Revenue: 137_188_000, FilingPeriod: "2026Q1"},
			},
			expectRevenue: 548_752_000,
			expectSource:  revenueSourceAnnualizedQuarter,
			expectWarnSub: "annualized single-quarter",
			delta:         1.0,
			wantPositive:  true,
		},

		// ---- T9: AAPL-shaped fixture (FY 2024 + Q1-Q4 2024 + Q1 2025) ----
		// AAPL with positive OI never routes through revenue_multiple, but
		// the helper's behavior must still be correct. Latest year 2025 has
		// only Q1, prior year 2024 supplies Q2+Q3+Q4 → TTM_PRIOR_BRIDGE.
		// Numeric answer is the same as TTM_4Q would have produced; the
		// source string is the audit-trail signal.
		{
			name: "T9_aapl_fy_plus_q1_with_prior_quarters",
			data: map[string]*FinancialData{
				"2024Q1": {Revenue: 90, FilingPeriod: "2024Q1"},
				"2024Q2": {Revenue: 95, FilingPeriod: "2024Q2"},
				"2024Q3": {Revenue: 100, FilingPeriod: "2024Q3"},
				"2024Q4": {Revenue: 110, FilingPeriod: "2024Q4"},
				"2024FY": {Revenue: 395, FilingPeriod: "2024FY", FilingDate: fixedDate.AddDate(-1, 0, 0)},
				"2025Q1": {Revenue: 105, FilingPeriod: "2025Q1"},
			},
			// Bridge: 2025Q1 (105) + 2024Q2 (95) + 2024Q3 (100) + 2024Q4 (110) = 410.
			expectRevenue: 410,
			expectSource:  revenueSourceTTMPriorBridge,
			expectWarnSub: "partial-year TTM bridged",
			delta:         0.001,
			wantPositive:  true,
		},

		// ---- BRIDGE_partial_year_uses_prior_year_quarters: latest year has Q1+Q2,
		// prior year has Q4 (positive) and Q3 (zero — sentinel). Bridge can't
		// build because 2025Q3 has zero revenue. TTM_4Q also fails (zero in
		// the latest-4 window). ANNUALIZED_QUARTER picks up Q1+Q2 of 2026.
		{
			name: "BRIDGE_partial_year_uses_prior_year_quarters",
			data: map[string]*FinancialData{
				"2025Q4": {Revenue: 130, FilingPeriod: "2025Q4"},
				"2026Q1": {Revenue: 140, FilingPeriod: "2026Q1"},
				"2026Q2": {Revenue: 150, FilingPeriod: "2026Q2"},
				"2025Q3": {Revenue: 0, FilingPeriod: "2025Q3"}, // sentinel: zero blocks both paths
			},
			expectRevenue: (140 + 150) * 2,
			expectSource:  revenueSourceAnnualizedQuarter,
			expectWarnSub: "annualized single-quarter",
			delta:         0.001,
			wantPositive:  true,
		},
		{
			// Clean bridge case: latest 2026 has Q1+Q2, prior 2025 has Q3+Q4.
			// Both paths could mathematically produce 380, but the bridge runs
			// first and surfaces the partial-year shape via the source string.
			name: "BRIDGE_q3q4_prior_plus_q1q2_latest_routes_to_bridge",
			data: map[string]*FinancialData{
				"2025Q3": {Revenue: 80, FilingPeriod: "2025Q3"},
				"2025Q4": {Revenue: 90, FilingPeriod: "2025Q4"},
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
			},
			expectRevenue: 380,
			expectSource:  revenueSourceTTMPriorBridge,
			expectWarnSub: "partial-year TTM bridged",
			delta:         0.001,
			wantPositive:  true,
		},
		{
			// Bridge case with an additional orphan quarter: latest 2026 has
			// Q1+Q2 (partial), prior 2025 has Q3+Q4. An older 2024Q4 is
			// present but does not change the bridge eligibility. The bridge
			// fires first; the orphan is ignored.
			name: "BRIDGE_partial_year_with_orphan_quarter",
			data: map[string]*FinancialData{
				"2025Q3": {Revenue: 80, FilingPeriod: "2025Q3"},
				"2025Q4": {Revenue: 90, FilingPeriod: "2025Q4"},
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
				"2024Q4": {Revenue: 70, FilingPeriod: "2024Q4"}, // orphan
			},
			expectRevenue: 380,
			expectSource:  revenueSourceTTMPriorBridge,
			expectWarnSub: "partial-year TTM bridged",
			delta:         0.001,
			wantPositive:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HistoricalFinancialData{
				Ticker: "TEST",
				Data:   tt.data,
			}

			revenue, source, warning := h.TrailingTwelveMonthsRevenue()

			if source != tt.expectSource {
				t.Errorf("source: got %q, want %q (warning=%q)", source, tt.expectSource, warning)
			}
			if tt.wantPositive && revenue <= 0 {
				t.Errorf("expected positive revenue, got %v", revenue)
			}
			if tt.wantNonPositive && revenue > 0 {
				t.Errorf("expected non-positive revenue, got %v", revenue)
			}
			if absDiff(revenue, tt.expectRevenue) > tt.delta {
				t.Errorf("revenue: got %v, want %v (±%v)", revenue, tt.expectRevenue, tt.delta)
			}
			if tt.expectWarnSub == "" {
				if warning != "" {
					t.Errorf("expected empty warning, got %q", warning)
				}
			} else {
				if !strings.Contains(warning, tt.expectWarnSub) {
					t.Errorf("warning: got %q, want substring %q", warning, tt.expectWarnSub)
				}
			}
		})
	}
}

// TestTrailingTwelveMonthsRevenue_NilReceiver covers the defensive nil
// guard at the top of the helper — important because some callers may
// dereference a HistoricalFinancialData pointer that the data layer
// returned without populating.
func TestTrailingTwelveMonthsRevenue_NilReceiver(t *testing.T) {
	var h *HistoricalFinancialData
	revenue, source, warning := h.TrailingTwelveMonthsRevenue()
	if revenue != 0 || source != revenueSourceInsufficient || !strings.Contains(warning, "insufficient") {
		t.Fatalf("nil receiver: got (%v, %q, %q), want (0, %q, contains \"insufficient\")",
			revenue, source, warning, revenueSourceInsufficient)
	}
}

// TestTrailingTwelveMonthsRevenue_BridgeWinsOnPartialYearIPO is the canonical
// regression for the V/R/Q follow-up that fixed the TTM_PRIOR_BRIDGE
// ordering (RM-1 follow-up). Pre-fix, this fixture would have routed to
// TTM_4Q because the latest 4 quarters by (year, sub) are contiguous —
// silently hiding the partial-year shape from replay tooling. Post-fix,
// the bridge runs first and the source string correctly reflects the
// partial-year IPO case.
//
// Fixture: 2025Q3 + 2025Q4 (prior year) + 2026Q1 + 2026Q2 (latest year,
// partial). Latest year has 1-3 quarters (Q1+Q2), prior year supplies the
// missing Q3+Q4. Numeric answer (380) equals what TTM_4Q would have
// produced on the same data; the difference is the source string and the
// "partial-year TTM bridged" warning.
func TestTrailingTwelveMonthsRevenue_BridgeWinsOnPartialYearIPO(t *testing.T) {
	h := &HistoricalFinancialData{
		Ticker: "IPO_PARTIAL",
		Data: map[string]*FinancialData{
			// Prior year 2025: Q3+Q4 (the calendar-corresponding gap fillers).
			"2025Q3": {Revenue: 80, FilingPeriod: "2025Q3"},
			"2025Q4": {Revenue: 90, FilingPeriod: "2025Q4"},
			// Latest year 2026: Q1+Q2 (partial).
			"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
			"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
		},
	}
	revenue, source, warning := h.TrailingTwelveMonthsRevenue()
	if source != revenueSourceTTMPriorBridge {
		t.Fatalf("expected TTM_PRIOR_BRIDGE source for partial-year IPO; got %q (warning=%q)",
			source, warning)
	}
	if revenue != 380 {
		t.Errorf("revenue: got %v, want 380 (2026Q1+Q2+2025Q3+Q4)", revenue)
	}
	if !strings.Contains(warning, "partial-year TTM bridged") {
		t.Errorf("warning: got %q, want substring %q", warning, "partial-year TTM bridged")
	}
}

// TestTrailingTwelveMonthsRevenue_BridgeDeclinesForFullYear pins the
// declination path: when the latest year already has 4 quarters, the
// bridge must NOT fire — TTM_4Q is the canonical path and the response
// must carry source=TTM_4Q with an empty warning. This is the contract
// that keeps the bridge's first-position ordering safe for full-year
// shapes.
func TestTrailingTwelveMonthsRevenue_BridgeDeclinesForFullYear(t *testing.T) {
	h := &HistoricalFinancialData{
		Ticker: "FULL_YEAR",
		Data: map[string]*FinancialData{
			"2025Q1": {Revenue: 100, FilingPeriod: "2025Q1"},
			"2025Q2": {Revenue: 110, FilingPeriod: "2025Q2"},
			"2025Q3": {Revenue: 120, FilingPeriod: "2025Q3"},
			"2025Q4": {Revenue: 130, FilingPeriod: "2025Q4"},
		},
	}
	revenue, source, warning := h.TrailingTwelveMonthsRevenue()
	if source != revenueSourceTTM4Q {
		t.Fatalf("expected TTM_4Q for full-year shape; got %q (warning=%q)", source, warning)
	}
	if revenue != 460 {
		t.Errorf("revenue: got %v, want 460", revenue)
	}
	if warning != "" {
		t.Errorf("warning: got %q, want empty (full-year TTM is the gold-standard path)", warning)
	}
}

// TestTtmPriorBridgeRevenue_DirectInvocation reaches into the unexported
// helper to confirm the bridge construction logic itself across the full
// matrix of pre-conditions (1, 2, or 3 quarters of latest year; missing
// prior-year quarters; non-Q1 starts; zero revenue). The public helper
// now invokes this path first, but the direct-invocation table guards
// against silent regressions in the bridge's own predicate checks.
func TestTtmPriorBridgeRevenue_DirectInvocation(t *testing.T) {
	tests := []struct {
		name        string
		data        map[string]*FinancialData
		expectSum   float64
		expectOK    bool
		description string
	}{
		{
			name: "two_latest_plus_two_prior",
			data: map[string]*FinancialData{
				"2025Q3": {Revenue: 80, FilingPeriod: "2025Q3"},
				"2025Q4": {Revenue: 90, FilingPeriod: "2025Q4"},
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
			},
			expectSum:   380, // 100+110+80+90
			expectOK:    true,
			description: "Q1+Q2 latest + Q3+Q4 prior",
		},
		{
			name: "three_latest_plus_one_prior",
			data: map[string]*FinancialData{
				"2025Q4": {Revenue: 90, FilingPeriod: "2025Q4"},
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
				"2026Q3": {Revenue: 120, FilingPeriod: "2026Q3"},
			},
			expectSum:   420, // 100+110+120+90
			expectOK:    true,
			description: "Q1+Q2+Q3 latest + Q4 prior",
		},
		{
			name: "single_latest_plus_three_prior",
			data: map[string]*FinancialData{
				"2025Q2": {Revenue: 70, FilingPeriod: "2025Q2"},
				"2025Q3": {Revenue: 80, FilingPeriod: "2025Q3"},
				"2025Q4": {Revenue: 90, FilingPeriod: "2025Q4"},
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
			},
			expectSum:   340, // 100+70+80+90
			expectOK:    true,
			description: "Q1 latest + Q2+Q3+Q4 prior",
		},
		{
			name: "missing_prior_quarter_blocks_bridge",
			data: map[string]*FinancialData{
				"2025Q3": {Revenue: 80, FilingPeriod: "2025Q3"},
				// 2025Q4 missing
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
			},
			expectOK:    false,
			description: "missing prior Q4 → bridge fails",
		},
		{
			name: "non_q1_starting_latest_blocks_bridge",
			data: map[string]*FinancialData{
				// Latest year has Q2+Q3 only (no Q1) → bridge requires Q1 start.
				"2025Q3": {Revenue: 80, FilingPeriod: "2025Q3"},
				"2025Q4": {Revenue: 90, FilingPeriod: "2025Q4"},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
				"2026Q3": {Revenue: 120, FilingPeriod: "2026Q3"},
			},
			expectOK:    false,
			description: "latest year has Q2+Q3 (no Q1) → not a bridge case",
		},
		{
			name: "four_latest_quarters_not_partial",
			data: map[string]*FinancialData{
				"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
				"2026Q3": {Revenue: 120, FilingPeriod: "2026Q3"},
				"2026Q4": {Revenue: 130, FilingPeriod: "2026Q4"},
			},
			expectOK:    false,
			description: "latest year is full year (4 quarters) → bridge declines, TTM_4Q takes over",
		},
		{
			name:        "no_quarters_at_all",
			data:        map[string]*FinancialData{},
			expectOK:    false,
			description: "empty data",
		},
		{
			name: "zero_revenue_in_latest_blocks_bridge",
			data: map[string]*FinancialData{
				"2025Q3": {Revenue: 80, FilingPeriod: "2025Q3"},
				"2025Q4": {Revenue: 90, FilingPeriod: "2025Q4"},
				"2026Q1": {Revenue: 0, FilingPeriod: "2026Q1"}, // zero blocks
				"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
			},
			expectOK:    false,
			description: "non-positive revenue in latest year aborts bridge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HistoricalFinancialData{Ticker: "TEST", Data: tt.data}
			sum, ok := h.ttmPriorBridgeRevenue()
			if ok != tt.expectOK {
				t.Errorf("[%s] ok: got %v, want %v (sum=%v)", tt.description, ok, tt.expectOK, sum)
			}
			if tt.expectOK && absDiff(sum, tt.expectSum) > 0.001 {
				t.Errorf("[%s] sum: got %v, want %v", tt.description, sum, tt.expectSum)
			}
		})
	}
}

// TestQuartersAreContiguous covers the small helper used by ttmFourQuartersRevenue.
func TestQuartersAreContiguous(t *testing.T) {
	tests := []struct {
		name    string
		periods []string
		want    bool
	}{
		{"empty is contiguous", []string{}, true},
		{"single is contiguous", []string{"2025Q2"}, true},
		{"adjacent same year", []string{"2025Q1", "2025Q2"}, true},
		{"four same year", []string{"2025Q1", "2025Q2", "2025Q3", "2025Q4"}, true},
		{"cross year boundary", []string{"2024Q4", "2025Q1"}, true},
		{"four crossing boundary", []string{"2024Q3", "2024Q4", "2025Q1", "2025Q2"}, true},
		{"gap in same year", []string{"2025Q1", "2025Q3"}, false},
		{"year jump", []string{"2024Q4", "2026Q1"}, false},
		{"backwards", []string{"2025Q2", "2025Q1"}, false},
		{"FY marker rejected", []string{"2025Q1", "2025FY"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quartersAreContiguous(tt.periods); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTrailingTwelveMonthsRevenue_AnnualizedQuarter_ThreeQuarterScale pins
// the (4/3)*sum scaling for the 3-quarter case. The 3-quarter case occurs
// when the latest year has Q1+Q2+Q3 AND prior-year Q4 is missing (so the
// bridge fails) AND no FY filing is available.
func TestTrailingTwelveMonthsRevenue_AnnualizedQuarter_ThreeQuarterScale(t *testing.T) {
	h := &HistoricalFinancialData{
		Ticker: "TEST",
		Data: map[string]*FinancialData{
			// No prior-year Q4 → bridge fails; no FY → annual fails;
			// 3 contiguous quarters in latest year → annualized = sum * 4/3.
			"2026Q1": {Revenue: 100, FilingPeriod: "2026Q1"},
			"2026Q2": {Revenue: 110, FilingPeriod: "2026Q2"},
			"2026Q3": {Revenue: 120, FilingPeriod: "2026Q3"},
		},
	}
	revenue, source, warning := h.TrailingTwelveMonthsRevenue()
	if source != revenueSourceAnnualizedQuarter {
		t.Fatalf("source: got %q, want %q (warning=%q)", source, revenueSourceAnnualizedQuarter, warning)
	}
	want := (100.0 + 110.0 + 120.0) * 4.0 / 3.0
	if absDiff(revenue, want) > 0.001 {
		t.Errorf("revenue: got %v, want %v", revenue, want)
	}
}

// TestTrailingTwelveMonthsRevenue_AnnualizedQuarter_NonQ1Stub exercises
// the "no Q1 in latest year" branch: the helper falls back to a single-
// quarter 4× extrapolation off the latest available quarter.
func TestTrailingTwelveMonthsRevenue_AnnualizedQuarter_NonQ1Stub(t *testing.T) {
	h := &HistoricalFinancialData{
		Ticker: "STUB",
		Data: map[string]*FinancialData{
			// Latest year has Q3 only (no Q1, no Q2). Bridge requires
			// Q1-start so it fails. annualizedQuarterRevenue: count
			// starts at sub=1, finds nothing, falls into the
			// "no Q1" branch which scales the latest single quarter.
			"2026Q3": {Revenue: 250, FilingPeriod: "2026Q3"},
		},
	}
	revenue, source, _ := h.TrailingTwelveMonthsRevenue()
	if source != revenueSourceAnnualizedQuarter {
		t.Fatalf("expected ANNUALIZED_QUARTER for non-Q1 stub; got %q", source)
	}
	if revenue != 1000 {
		t.Errorf("expected 250*4=1000; got %v", revenue)
	}
}

// TestAnnualizedQuarterRevenue_DirectInvocation_DefensiveBranches reaches into
// the unexported helper to pin its defensive branches. The public helper
// preferentially routes around these (TTM_4Q wins on 4 contiguous quarters,
// FY wins when an FY exists), so direct invocation is the only way to keep
// coverage on the count==4 fall-through and the no-Q1 stub path.
func TestAnnualizedQuarterRevenue_DirectInvocation_DefensiveBranches(t *testing.T) {
	// count==4 fall-through: 4 contiguous quarters in the latest year. The
	// public helper would route this to TTM_4Q; calling annualizedQuarterRevenue
	// directly returns the raw sum without scaling.
	h := &HistoricalFinancialData{
		Ticker: "DEFENSIVE",
		Data: map[string]*FinancialData{
			"2025Q1": {Revenue: 100, FilingPeriod: "2025Q1"},
			"2025Q2": {Revenue: 110, FilingPeriod: "2025Q2"},
			"2025Q3": {Revenue: 120, FilingPeriod: "2025Q3"},
			"2025Q4": {Revenue: 130, FilingPeriod: "2025Q4"},
		},
	}
	sum, ok := h.annualizedQuarterRevenue()
	if !ok || sum != 460 {
		t.Errorf("count==4 fall-through: got (%v, %v), want (460, true)", sum, ok)
	}
}

// absDiff is a small helper for float comparisons in this test file.
func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}
