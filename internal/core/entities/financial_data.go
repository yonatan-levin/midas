package entities

import (
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/pkg/finance/growth"
)

// FinancialData represents the fundamental financial metrics for a company
// extracted and normalized from SEC XBRL filings
type FinancialData struct {
	// Company identification
	Ticker       string    `json:"ticker"`
	IndustryCode string    `json:"industry_code"` // TODO: Populate this field with the industry code and use it for the industry code detection.
	CIK          string    `json:"cik"`
	AsOf         time.Time `json:"as_of"`

	// Income Statement (normalized values)
	OperatingIncome           float64 `json:"operating_income"`            // Normalized operating income after adjustments
	NormalizedOperatingIncome float64 `json:"normalized_operating_income"` // After removing non-recurring items
	Revenue                   float64 `json:"revenue"`
	ResearchAndDevelopment    float64 `json:"research_and_development"` // R&D expenses for capitalization analysis
	InterestExpense           float64 `json:"interest_expense"`
	TaxRate                   float64 `json:"tax_rate"` // Effective tax rate

	// Earnings Normalization Fields (Category C from SEC guide)
	RestructuringCharges     float64 `json:"restructuring_charges"`      // C1: Restructuring and integration charges
	AssetSaleGains           float64 `json:"asset_sale_gains"`           // C2: Asset sale gains and impairment losses
	LitigationSettlements    float64 `json:"litigation_settlements"`     // C3: Litigation settlements and fines
	StockBasedCompensation   float64 `json:"stock_based_compensation"`   // C4: Stock-based compensation expense
	DerivativeGainsLosses    float64 `json:"derivative_gains_losses"`    // C5: Fair value gains/losses on derivatives
	CapitalizedInterest      float64 `json:"capitalized_interest"`       // C6: Capitalized interest adjustment
	WorkingCapitalAdjustment float64 `json:"working_capital_adjustment"` // C7: Working capital window dressing

	// Balance Sheet (adjusted values)
	TotalAssets         float64 `json:"total_assets"`
	TangibleAssets      float64 `json:"tangible_assets"`       // Total assets minus goodwill and intangibles
	Goodwill            float64 `json:"goodwill"`              // Removed from asset calculations
	OtherIntangibles    float64 `json:"other_intangibles"`     // Other intangible assets
	TotalDebt           float64 `json:"total_debt"`            // Interest-bearing debt
	InterestBearingDebt float64 `json:"interest_bearing_debt"` // Debt used for WACC calculation

	// Asset Quality Fields (Category A from SEC guide)
	IntangibleAssets           float64 `json:"intangible_assets"`            // Total intangible assets including goodwill
	IndefiniteLivedIntangibles float64 `json:"indefinite_lived_intangibles"` // Trademarks, broadcast licenses (A2)
	DeferredTaxAssets          float64 `json:"deferred_tax_assets"`          // Deferred tax assets gross (A4)
	ValuationAllowance         float64 `json:"valuation_allowance"`          // DTA valuation allowance
	EffectiveTaxRate           float64 `json:"effective_tax_rate"`           // Effective tax rate for DTA assessment
	CostOfGoodsSold            float64 `json:"cost_of_goods_sold"`           // For inventory turnover calculations

	// Inventory analysis
	Inventory              float64 `json:"inventory"`
	InventoryTurnover      float64 `json:"inventory_turnover"`
	DeadInventoryWritedown float64 `json:"dead_inventory_writedown"` // Amount written down

	// Liability Completeness Fields (Category B from SEC guide)
	OperatingLeaseLiabilityCurrent    float64            `json:"operating_lease_liability_current"`    // Current portion of operating lease liabilities (B1)
	OperatingLeaseLiabilityNoncurrent float64            `json:"operating_lease_liability_noncurrent"` // Non-current operating lease liabilities (B1)
	OperatingLeaseLiability           float64            `json:"operating_lease_liability"`            // Total operating lease liability (B1)
	OperatingLeaseCommitments         map[string]float64 `json:"operating_lease_commitments"`          // Future lease commitments by year (B1)
	PensionLiabilities                float64            `json:"pension_liabilities"`                  // Defined benefit pension obligations (B2)
	OPEBLiability                     float64            `json:"opeb_liability"`                       // Other post-employment benefit liabilities (B2)
	PensionPlanAssets                 float64            `json:"pension_plan_assets"`                  // Plan assets fair value (B2)
	ProjectedBenefitObligation        float64            `json:"projected_benefit_obligation"`         // PBO for pension plans (B2)
	ContingentLiabilities             float64            `json:"contingent_liabilities"`               // Disclosed contingent liabilities (B3)
	EnvironmentalLiabilities          float64            `json:"environmental_liabilities"`            // Environmental remediation liabilities (B3)
	LitigationLiabilities             float64            `json:"litigation_liabilities"`               // Litigation settlement liabilities (B3)
	IncrementalBorrowingRate          float64            `json:"incremental_borrowing_rate"`           // IBR for lease capitalization (B1)
	RiskFreeRate                      float64            `json:"risk_free_rate"`                       // Risk-free rate for discount rate calculations

	// Share information
	SharesOutstanding        float64 `json:"shares_outstanding"`
	DilutedSharesOutstanding float64 `json:"diluted_shares_outstanding"`

	// Filing metadata
	Period       string    `json:"period"`        // Short period identifier for tests (e.g., "2023Q4")
	FilingPeriod string    `json:"filing_period"` // e.g., "2023Q4"
	FilingDate   time.Time `json:"filing_date"`

	// Data quality flags
	HasNormalizedData bool     `json:"has_normalized_data"`      // Whether normalization was applied
	MissingFields     []string `json:"missing_fields,omitempty"` // List of fields that were missing
}

// HistoricalFinancialData represents a time series of financial data
// Used for calculating growth rates and trends
type HistoricalFinancialData struct {
	Ticker string                    `json:"ticker"`
	Data   map[string]*FinancialData `json:"data"` // keyed by filing period (e.g., "2023Q4")
}

// GetSortedPeriods returns filing periods sorted chronologically
func (h *HistoricalFinancialData) GetSortedPeriods() []string {
	periods := make([]string, 0, len(h.Data))
	for period := range h.Data {
		periods = append(periods, period)
	}

	// TODO: Implement proper period sorting (2023Q1, 2023Q2, etc.)
	// For now, basic string sort
	return periods
}

// GetLatestData returns the most recent financial data
func (h *HistoricalFinancialData) GetLatestData() *FinancialData {
	periods := h.GetSortedPeriods()
	if len(periods) == 0 {
		return nil
	}

	// Return the last period (assuming sorted order)
	latestPeriod := periods[len(periods)-1]
	return h.Data[latestPeriod]
}

// GetOperatingIncomeHistory returns the operating income for the last N periods
func (h *HistoricalFinancialData) GetOperatingIncomeHistory(periods int) []float64 {
	sortedPeriods := h.GetSortedPeriods()
	if len(sortedPeriods) == 0 {
		return []float64{}
	}

	start := len(sortedPeriods) - periods
	if start < 0 {
		start = 0
	}

	income := make([]float64, 0)
	for i := start; i < len(sortedPeriods); i++ {
		data := h.Data[sortedPeriods[i]]
		if data != nil {
			income = append(income, data.NormalizedOperatingIncome)
		}
	}

	return income
}

// GetLatestPeriod returns the most recent period's financial data
func (h *HistoricalFinancialData) GetLatestPeriod() (*FinancialData, string) {
	var latestPeriod string
	var latestData *FinancialData
	var latestDate time.Time

	for period, data := range h.Data {
		if data.FilingDate.After(latestDate) {
			latestDate = data.FilingDate
			latestPeriod = period
			latestData = data
		}
	}

	return latestData, latestPeriod
}

// GetAnnualPeriods returns only annual (full year) periods
func (h *HistoricalFinancialData) GetAnnualPeriods() map[string]*FinancialData {
	annual := make(map[string]*FinancialData)

	for period, data := range h.Data {
		if len(period) >= 2 && period[len(period)-2:] == "FY" {
			annual[period] = data
		}
	}

	return annual
}

// GetQuarterlyPeriods returns only quarterly periods
func (h *HistoricalFinancialData) GetQuarterlyPeriods() map[string]*FinancialData {
	quarterly := make(map[string]*FinancialData)

	for period, data := range h.Data {
		if len(period) >= 2 && (period[len(period)-2:] == "Q1" ||
			period[len(period)-2:] == "Q2" ||
			period[len(period)-2:] == "Q3" ||
			period[len(period)-2:] == "Q4") {
			quarterly[period] = data
		}
	}

	return quarterly
}

// GetRecentYears returns financial data for the most recent N years
func (h *HistoricalFinancialData) GetRecentYears(years int) []*FinancialData {
	annual := h.GetAnnualPeriods()

	// Convert to slice and sort by filing date
	var periods []*FinancialData
	for _, data := range annual {
		periods = append(periods, data)
	}

	// Sort by filing date descending (most recent first)
	for i := 0; i < len(periods)-1; i++ {
		for j := i + 1; j < len(periods); j++ {
			if periods[i].FilingDate.Before(periods[j].FilingDate) {
				periods[i], periods[j] = periods[j], periods[i]
			}
		}
	}

	// Return the most recent N years
	if len(periods) > years {
		periods = periods[:years]
	}

	return periods
}

// HasMinimumData checks if we have enough data for valuation
func (h *HistoricalFinancialData) HasMinimumData(minYears int) bool {
	recent := h.GetRecentYears(minYears)
	if len(recent) < minYears {
		return false
	}

	// Check that each year has the minimum required fields
	for _, data := range recent {
		if data.Revenue <= 0 && data.OperatingIncome <= 0 {
			return false
		}
		if data.SharesOutstanding <= 0 && data.DilutedSharesOutstanding <= 0 {
			return false
		}
	}

	return true
}

// CalculateAverageGrowthRate calculates the average growth rate of operating income
func (h *HistoricalFinancialData) CalculateAverageGrowthRate(years int) (*growth.CalculationResult, error) {
	recent := h.GetRecentYears(years)
	if len(recent) < 2 {
		return nil, fmt.Errorf("insufficient data for growth calculation")
	}

	// Sort by filing date ascending (oldest first)
	for i := 0; i < len(recent)-1; i++ {
		for j := i + 1; j < len(recent); j++ {
			if recent[i].FilingDate.After(recent[j].FilingDate) {
				recent[i], recent[j] = recent[j], recent[i]
			}
		}
	}

	var values []float64
	for _, data := range recent {
		value := data.NormalizedOperatingIncome
		if value == 0 {
			value = data.OperatingIncome
		}
		if value > 0 {
			values = append(values, value)
		}
	}

	if len(values) < 2 {
		return nil, fmt.Errorf("insufficient positive operating income values for growth calculation")
	}

	// Use the growth package to calculate the best growth rate
	return growth.CalculateBestGrowthRate(values)
}
