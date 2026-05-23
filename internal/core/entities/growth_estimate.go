package entities

import "math"

// GrowthEstimate contains per-year projected growth rates and the metadata
// about how they were derived (analyst consensus, historical trends, or blend).
type GrowthEstimate struct {
	// Per-year growth rates for the explicit forecast period.
	// Length matches ProjectionYears (e.g., 7 entries for a 7-year projection).
	ProjectedGrowthRates []float64 `json:"projected_growth_rates"`
	TerminalGrowthRate   float64   `json:"terminal_growth_rate"`

	// Analyst consensus data (from Yahoo Finance earningsTrend)
	AnalystRevenueGrowthY1  float64 `json:"analyst_revenue_growth_y1"`
	AnalystRevenueGrowthY2  float64 `json:"analyst_revenue_growth_y2"`
	AnalystEarningsGrowth5Y float64 `json:"analyst_earnings_growth_5y"`
	NumberOfAnalysts        int     `json:"number_of_analysts"`

	// Blend weights — the actual mix applied when combining analyst consensus
	// with historical CAGR for Stage 1 growth. AnalystWeight is 0.0 when no
	// analyst data is available (Source == "historical_only"). The two fields
	// sum to 1.0 by construction. Surfaced for the `growth.estimated` narrate
	// phase (spec §5 row 12) and for downstream debuggers wanting to know
	// which signal dominated the projection. Filed as G-1 (closed).
	AnalystWeight    float64 `json:"analyst_weight"`
	HistoricalWeight float64 `json:"historical_weight"`

	// Historical baseline
	HistoricalCAGR        float64 `json:"historical_cagr"`
	SustainableGrowthRate float64 `json:"sustainable_growth_rate"` // ROIC x reinvestment rate

	// Metadata
	Source     string   `json:"source"`     // "analyst_blend", "historical_only", "default"
	Confidence string   `json:"confidence"` // "high", "medium", "low"
	Method     string   `json:"method"`     // human-readable description of how rates were derived
	Warnings   []string `json:"warnings,omitempty"`
}

// SummaryGrowthRate returns a single representative growth rate (CAGR of the
// projected rates) for backward-compatible API responses.
func (g *GrowthEstimate) SummaryGrowthRate() float64 {
	if len(g.ProjectedGrowthRates) == 0 {
		return 0
	}

	// Compute the compound effect of all per-year rates, then derive the CAGR
	compound := 1.0
	for _, r := range g.ProjectedGrowthRates {
		compound *= (1 + r)
	}

	n := float64(len(g.ProjectedGrowthRates))
	if compound <= 0 {
		return 0
	}

	// CAGR = compound^(1/n) - 1
	return math.Pow(compound, 1.0/n) - 1
}
