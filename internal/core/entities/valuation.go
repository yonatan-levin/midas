package entities

import (
	"time"
)

// ValuationResult represents the output of a DCF analysis
type ValuationResult struct {
	Ticker string    `json:"ticker"`
	AsOf   time.Time `json:"as_of"`

	// Core valuation metrics
	TangibleValuePerShare float64 `json:"tangible_value_per_share"` // Net tangible assets / shares
	DCFValuePerShare      float64 `json:"dcf_value_per_share"`      // Intrinsic value from DCF model

	// WACC components
	WACC           float64 `json:"wacc"`             // Weighted Average Cost of Capital
	CostOfEquity   float64 `json:"cost_of_equity"`   // CAPM-derived cost of equity
	CostOfDebt     float64 `json:"cost_of_debt"`     // After-tax cost of debt
	WeightOfEquity float64 `json:"weight_of_equity"` // E/(E+D)
	WeightOfDebt   float64 `json:"weight_of_debt"`   // D/(E+D)

	// Growth assumptions
	GrowthRate         float64   `json:"growth_rate"`                 // Summary growth rate (CAGR of projected rates, backward-compatible)
	GrowthRates        []float64 `json:"growth_rates,omitempty"`      // Per-year projected growth rates
	TerminalGrowthRate float64   `json:"terminal_growth_rate"`        // Long-term growth rate
	GrowthSource       string    `json:"growth_source,omitempty"`     // "analyst_blend", "historical_only", "default"
	GrowthConfidence   string    `json:"growth_confidence,omitempty"` // "high", "medium", "low"

	// DCF model details
	ProjectionYears int     `json:"projection_years"` // Number of explicit forecast years
	TerminalValue   float64 `json:"terminal_value"`   // Present value of terminal value

	// Input data timestamps
	FinancialDataAsOf time.Time `json:"financial_data_as_of"` // When the fundamental data was filed
	MarketDataAsOf    time.Time `json:"market_data_as_of"`    // When the market data was captured
	FilingPeriod      string    `json:"filing_period"`        // e.g., "2023Q4"

	// Calculation metadata
	CalculationMethod string   `json:"calculation_method"` // "standard_dcf", "simplified", etc.
	DataQualityScore  float64  `json:"data_quality_score"` // 0-100 score based on data completeness
	Warnings          []string `json:"warnings,omitempty"` // Any data quality or assumption warnings

	// Extended fields for comprehensive analysis
	CalculatedAt        time.Time       `json:"calculated_at"`
	DataQualityGrade    QualityGrade    `json:"data_quality_grade"`   // A, B, C, D, F
	CleaningReport      *CleaningReport `json:"cleaning_report"`      // Full cleaning report
	CleaningFlags       []Flag          `json:"cleaning_flags"`       // Key risk flags
	CleaningAdjustments []Adjustment    `json:"cleaning_adjustments"` // Applied adjustments
	MarketRiskPremium   float64         `json:"market_risk_premium"`
	EnterpriseValue     float64         `json:"enterprise_value"`
	EquityValue         float64         `json:"equity_value"`
	FinancialDataPeriod string          `json:"financial_data_period"`
	MarketDataDate      time.Time       `json:"market_data_date"`
	DataFreshnessScore  int             `json:"data_freshness_score"`
	CalculationVersion  string          `json:"calculation_version"`

	// Phase 4: Multiples sanity cross-check comparing DCF-implied multiples
	// against sector medians. Nil when cross-check data is unavailable.
	SanityCheck *SanityCheck `json:"sanity_check,omitempty"`
}

// SanityCheck contains cross-check multiples that compare the DCF-implied valuation
// against sector median multiples. Flags divergences > 2x or < 0.5x sector median.
type SanityCheck struct {
	ImpliedPE            float64  `json:"implied_pe"`              // DCF value / EPS
	SectorMedianPE       float64  `json:"sector_median_pe"`        // Sector median P/E ratio
	ImpliedEVEBITDA      float64  `json:"implied_ev_ebitda"`       // DCF enterprise value / EBITDA
	SectorMedianEVEBITDA float64  `json:"sector_median_ev_ebitda"` // Sector median EV/EBITDA
	IsReasonable         bool     `json:"is_reasonable"`           // True if implied multiples are within 0.5x-2x of sector medians
	Flags                []string `json:"flags,omitempty"`         // Specific warnings about divergences
}

// DCFProjection represents the detailed cash flow projections
type DCFProjection struct {
	Ticker string    `json:"ticker"`
	AsOf   time.Time `json:"as_of"`

	// Annual projections (typically 5 years)
	YearlyProjections []YearlyProjection `json:"yearly_projections"`

	// Terminal value calculation
	TerminalValue      float64 `json:"terminal_value"`
	TerminalGrowthRate float64 `json:"terminal_growth_rate"`

	// Discounting
	WACC                 float64 `json:"wacc"`
	PresentValue         float64 `json:"present_value"` // Sum of discounted cash flows
	PresentValuePerShare float64 `json:"present_value_per_share"`
}

// YearlyProjection represents cash flow projection for a single year
type YearlyProjection struct {
	Year            int     `json:"year"`             // Projection year (1, 2, 3, 4, 5)
	OperatingIncome float64 `json:"operating_income"` // Projected operating income
	NOPAT           float64 `json:"nopat"`            // Net Operating Profit After Tax
	FreeCashFlow    float64 `json:"free_cash_flow"`   // Free cash flow to firm
	DiscountFactor  float64 `json:"discount_factor"`  // (1 + WACC)^year
	PresentValue    float64 `json:"present_value"`    // FCF / discount factor

	// Growth calculations
	GrowthRate float64 `json:"growth_rate"` // Applied growth rate for this year
}

// CalculateTotalPresentValue returns the sum of all discounted cash flows plus terminal value
func (d *DCFProjection) CalculateTotalPresentValue() float64 {
	total := d.TerminalValue // Terminal value is already present value

	for _, projection := range d.YearlyProjections {
		total += projection.PresentValue
	}

	return total
}

// GetProjectionByYear returns the projection for a specific year (1-5)
func (d *DCFProjection) GetProjectionByYear(year int) *YearlyProjection {
	for i := range d.YearlyProjections {
		if d.YearlyProjections[i].Year == year {
			return &d.YearlyProjections[i]
		}
	}
	return nil
}

// CalculationInputs represents all the inputs used in valuation calculation
// Useful for debugging and transparency
type CalculationInputs struct {
	Ticker string `json:"ticker"`

	// Financial data inputs
	NormalizedOperatingIncome float64 `json:"normalized_operating_income"`
	TangibleAssets            float64 `json:"tangible_assets"`
	TotalDebt                 float64 `json:"total_debt"`
	SharesOutstanding         float64 `json:"shares_outstanding"`
	TaxRate                   float64 `json:"tax_rate"`

	// Market data inputs
	SharePrice          float64 `json:"share_price"`
	Beta                float64 `json:"beta"`
	MarketValueOfEquity float64 `json:"market_value_of_equity"`

	// Macro inputs
	RiskFreeRate      float64 `json:"risk_free_rate"`
	MarketRiskPremium float64 `json:"market_risk_premium"`

	// Calculated intermediates
	CostOfEquity       float64 `json:"cost_of_equity"`
	CostOfDebt         float64 `json:"cost_of_debt"`
	WeightOfEquity     float64 `json:"weight_of_equity"`
	WeightOfDebt       float64 `json:"weight_of_debt"`
	FiveYearGrowthRate float64 `json:"five_year_growth_rate"`

	// Configuration overrides
	Overrides map[string]interface{} `json:"overrides,omitempty"`
}
