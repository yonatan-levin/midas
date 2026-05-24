package entities

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
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

	// ReportingCurrency is the ISO-4217 code of the currency that every
	// monetary field on this struct (Revenue, Assets, OperatingIncome, …) is
	// denominated in, as taken from the SEC XBRL `Units` key. Set by the SEC
	// parser; populated by Phase B5 of the IFRS-FPI plan
	// (docs/refactoring/archive/ifrs-foreign-private-issuer-support-spec.md).
	//
	// Empty string is treated as "USD" by callers — backward compat for
	// FinancialData rows persisted before the field shipped, and for tests
	// that build FinancialData literals without setting it.
	//
	// SharesOutstanding / DilutedSharesOutstanding are dimensionless and
	// MUST NOT be FX-converted when ReportingCurrency != USD. The full list
	// of monetary vs. non-monetary fields is documented in
	// internal/services/valuation/currency.go (Phase B9).
	ReportingCurrency string `json:"reporting_currency,omitempty"`

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

	// Dividend and earnings data (for DDM and FFO models)
	DividendsPerShare   float64 `json:"dividends_per_share"`    // Cash dividends declared per common share
	NetIncome           float64 `json:"net_income"`             // Net income attributable to common shareholders
	GainOnPropertySales float64 `json:"gain_on_property_sales"` // Gain/loss on sale of properties (for REIT FFO calculation)

	// Cash Flow Statement fields (for true FCF calculation)
	DepreciationAndAmortization float64 `json:"depreciation_and_amortization"` // Non-cash charge to add back
	CapitalExpenditures         float64 `json:"capital_expenditures"`          // Cash outflow for PP&E (stored as positive)
	OperatingCashFlow           float64 `json:"operating_cash_flow"`           // Net cash from operations

	// Working capital components (for delta WC calculation)
	CurrentAssets      float64 `json:"current_assets"`
	CurrentLiabilities float64 `json:"current_liabilities"`

	// Plug fields — DC-1 Phase 0 (see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md).
	// Computed at the end of the SEC parser as residuals so the following
	// arithmetic invariants hold by construction:
	//
	//   CurrentAssets       == CashAndCashEquivalents + Inventory + OtherCurrentAssets
	//   TotalAssets         == CurrentAssets + Goodwill + OtherIntangibles +
	//                          DeferredTaxAssets + OtherNonCurrentAssets
	//   CurrentLiabilities  == OperatingLeaseLiabilityCurrent + OtherCurrentLiabilities
	//   TotalLiabilities    == CurrentLiabilities + TotalDebt +
	//                          OperatingLeaseLiabilityNoncurrent + OtherNonCurrentLiabilities
	//
	// All four are >= 0 by construction (negative residuals clamped with a Debug log).
	//
	// IMPORTANT — today's SEC XBRL parser only populates the umbrella
	// OperatingLeaseLiability field; the split fields
	// OperatingLeaseLiabilityCurrent / OperatingLeaseLiabilityNoncurrent are
	// treated as fallbacks for the umbrella, never as independent values
	// (see parser.go:775-784). Until a future phase teaches the parser to
	// preserve the current/noncurrent split, both lease split fields remain
	// zero on every parsed FinancialData and the operating-lease portion of
	// liabilities is absorbed into the OtherCurrentLiabilities /
	// OtherNonCurrentLiabilities plugs. The math invariants above still hold.
	//
	// OtherNonCurrentAssets also absorbs PP&E and any other non-current
	// asset line items not explicitly modeled on FinancialData.
	//
	// Phase 1+ uses these plugs to enforce components-sum-to-umbrellas in
	// the cleaner. Phase 0 invariant: NO consumer reads these fields yet —
	// they exist only to feed the Phase 1+ recomputeUmbrellas shim.
	OtherCurrentAssets         float64 `json:"other_current_assets"`
	OtherNonCurrentAssets      float64 `json:"other_non_current_assets"`
	OtherCurrentLiabilities    float64 `json:"other_current_liabilities"`
	OtherNonCurrentLiabilities float64 `json:"other_non_current_liabilities"`

	// TotalLiabilities is the as-reported balance-sheet line "total liabilities"
	// (us-gaap:Liabilities / ifrs-full:Liabilities). Populated by the SEC parser
	// when the umbrella XBRL tag is present; left at zero otherwise. Distinct
	// from CurrentLiabilities (the short-term subset). Consumed by the Graham-
	// floor diagnostic in internal/services/valuation/graham.go; not used by
	// the DCF or alt-model engines.
	TotalLiabilities float64 `json:"total_liabilities,omitempty"`

	// Cash position (for equity bridge: EV - Debt + Cash = Equity Value)
	CashAndCashEquivalents float64 `json:"cash_and_cash_equivalents"`

	// Equity (for ROIC / invested capital calculation)
	StockholdersEquity float64 `json:"stockholders_equity"`

	// MinorityInterest is the equity attributable to non-controlling interests
	// in consolidated subsidiaries. Subtracted from enterprise-to-equity
	// bridge per CFA convention. SEC XBRL tag: us-gaap:MinorityInterest
	// (also reported as MinorityInterestInLimitedPartnerships variants).
	MinorityInterest float64 `json:"minority_interest"`

	// PreferredEquity is the par or carrying value of preferred stock.
	// Subtracted from common-equity bridge so per-share value reflects
	// only common shareholders' claim. SEC XBRL tag: us-gaap:PreferredStockValue
	// (also PreferredStockValueOutstanding for some filers).
	PreferredEquity float64 `json:"preferred_equity"`

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
	Ticker      string                    `json:"ticker"`
	CompanyName string                    `json:"company_name,omitempty"` // From SEC EntityName, used for industry classification
	SICCode     string                    `json:"sic_code,omitempty"`     // Standard Industrial Classification code from SEC submissions
	Data        map[string]*FinancialData `json:"data"`                   // keyed by filing period (e.g., "2023Q4")
}

// GetSortedPeriods returns filing periods sorted chronologically.
// Period format: "2023FY", "2023Q1", "2023Q2", etc.
// Sort order: FY sorts after all quarters of the same year (it covers the full year).
// Example: 2022Q1 < 2022Q2 < 2022Q3 < 2022Q4 < 2022FY < 2023Q1 < ...
func (h *HistoricalFinancialData) GetSortedPeriods() []string {
	periods := make([]string, 0, len(h.Data))
	for period := range h.Data {
		periods = append(periods, period)
	}

	sort.Slice(periods, func(i, j int) bool {
		yi, si := parsePeriodKey(periods[i])
		yj, sj := parsePeriodKey(periods[j])
		if yi != yj {
			return yi < yj
		}
		return si < sj
	})

	return periods
}

// parsePeriodKey extracts (year, subOrder) from a period string.
// subOrder: Q1=1, Q2=2, Q3=3, Q4=4, FY=5 (FY sorts after all quarters).
func parsePeriodKey(period string) (int, int) {
	// Find where the suffix starts (e.g., "2023FY" -> year=2023, suffix="FY")
	suffixIdx := strings.IndexFunc(period, func(r rune) bool {
		return r < '0' || r > '9'
	})

	if suffixIdx <= 0 {
		return 0, 0
	}

	year, err := strconv.Atoi(period[:suffixIdx])
	if err != nil {
		return 0, 0
	}

	suffix := period[suffixIdx:]
	switch suffix {
	case "Q1":
		return year, 1
	case "Q2":
		return year, 2
	case "Q3":
		return year, 3
	case "Q4":
		return year, 4
	case "FY":
		return year, 5
	default:
		return year, 0
	}
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

// Source identifiers returned by TrailingTwelveMonthsRevenue. The string
// values are part of the helper's public contract — replay tooling and
// downstream dashboards key off them, so do not rename without coordinating
// with consumers (see docs/reviewer/RM-1-revenue-multiple-quarterly-vs-ttm.md).
const (
	revenueSourceTTM4Q             = "TTM_4Q"
	revenueSourceTTMPriorBridge    = "TTM_PRIOR_BRIDGE"
	revenueSourceAnnualFY          = "ANNUAL_FY"
	revenueSourceAnnualizedQuarter = "ANNUALIZED_QUARTER"
	revenueSourceInsufficient      = "INSUFFICIENT_HISTORY"
)

// TrailingTwelveMonthsRevenue returns a best-effort trailing-twelve-months
// (TTM) revenue figure for use by EV/Revenue-style models. It walks a
// documented fallback chain so callers can take the result at face value
// for the headline number while still inspecting `source` and `warning`
// for audit / replay purposes.
//
// Fallback chain (applied in order):
//
//  1. TTM_PRIOR_BRIDGE    — partial-year case (latest year has 1-3 quarters,
//     prior year supplies the missing quarters at the
//     same calendar position). Runs FIRST because it
//     preserves the audit-trail signal that the latest
//     year is partial; without this ordering the bridge
//     is structurally unreachable when prior-year
//     corresponding quarters are present (TTM_4Q would
//     pick the same 4 quarters and produce the same
//     numeric answer with a different source string,
//     hiding the partial-year shape from replay tooling).
//     Declines (returns no value) for full-year shapes,
//     letting TTM_4Q take over.
//  2. TTM_4Q              — sum of the 4 most recent contiguous quarters,
//     possibly crossing a fiscal-year boundary. The
//     gold-standard path for full-year shapes; warning
//     is empty.
//  3. ANNUAL_FY           — most recent fiscal-year filing when no usable
//     quarterly window exists.
//  4. ANNUALIZED_QUARTER  — naive scaling of the available quarters
//     (4*Q1, 2*(Q1+Q2), or (4/3)*(Q1+Q2+Q3)). Lossy:
//     ignores seasonality. Warning emitted.
//  5. INSUFFICIENT_HISTORY — no revenue at all; returns (0, …) and lets
//     the caller decide whether to fail.
//
// The signature is stable: replay tooling pattern-matches `source` strings
// (see RM-1 spec). Adding a new source string requires updating the spec
// and consumers; renaming an existing one is a breaking change.
//
// T7 stale-data check (>=18mo) lives in the consumer
// (internal/services/valuation/models/revenue_multiple.go), not here:
// adding a clock to this leaf entity would violate the layering invariant
// and break replay determinism. Resolved per RM-1.A Option B — the consumer
// receives a clock seam via ModelInput.Now plumbed from *Service.clock.
func (h *HistoricalFinancialData) TrailingTwelveMonthsRevenue() (revenue float64, source string, warning string) {
	if h == nil || len(h.Data) == 0 {
		return 0, revenueSourceInsufficient, "revenue_base: insufficient revenue history"
	}

	// 1) TTM_PRIOR_BRIDGE — partial-year + prior-year corresponding quarters.
	// Runs first so the partial-year IPO shape is surfaced via the source
	// string. Declines (returns false) when the latest year has 4 quarters,
	// at which point TTM_4Q takes over.
	if rev, ok := h.ttmPriorBridgeRevenue(); ok {
		return rev, revenueSourceTTMPriorBridge,
			"revenue_base: partial-year TTM bridged with prior-year quarters (handles seasonality)"
	}

	// 2) TTM_4Q — four most recent contiguous quarters, summed.
	if rev, ok := h.ttmFourQuartersRevenue(); ok {
		return rev, revenueSourceTTM4Q, ""
	}

	// 3) ANNUAL_FY — fall back to the latest fiscal-year filing.
	if rev, period, date, ok := h.latestAnnualRevenue(); ok {
		// When TTM was structurally unattempted (no quarters at all) the FY
		// path is "clean" and produces no warning. When TTM was attempted
		// but failed (gaps, missing prior-year quarters), surface the
		// reason so callers can correlate against data-quality dashboards.
		if len(h.GetQuarterlyPeriods()) == 0 {
			return rev, revenueSourceAnnualFY, ""
		}
		return rev, revenueSourceAnnualFY,
			fmt.Sprintf("revenue_base: TTM unavailable, used latest FY ($%.0f dated %s)",
				rev, date.Format("2006-01-02")) + fmt.Sprintf(" [%s]", period)
	}

	// 4) ANNUALIZED_QUARTER — scale up 1-3 available quarters of the latest year.
	if rev, ok := h.annualizedQuarterRevenue(); ok {
		return rev, revenueSourceAnnualizedQuarter,
			"revenue_base: annualized single-quarter revenue (4× extrapolation, ignores seasonality)"
	}

	// 5) INSUFFICIENT_HISTORY — no usable revenue at all.
	return 0, revenueSourceInsufficient, "revenue_base: insufficient revenue history"
}

// ttmFourQuartersRevenue returns the sum of the 4 most recent quarters when
// they form a contiguous span (no gaps, distinct quarters). Quarters may
// cross a fiscal-year boundary (e.g. 2025Q4 + 2026Q1 + 2026Q2 + 2026Q3).
// Returns (0, false) when the contiguity check fails or any quarter has
// non-positive revenue (a zero quarter would silently shrink the TTM).
func (h *HistoricalFinancialData) ttmFourQuartersRevenue() (float64, bool) {
	quarterly := h.GetQuarterlyPeriods()
	if len(quarterly) < 4 {
		return 0, false
	}

	// Sort the quarterly keys in chronological order (uses the same
	// (year, sub) tuple as GetSortedPeriods).
	periods := make([]string, 0, len(quarterly))
	for p := range quarterly {
		periods = append(periods, p)
	}
	sort.Slice(periods, func(i, j int) bool {
		yi, si := parsePeriodKey(periods[i])
		yj, sj := parsePeriodKey(periods[j])
		if yi != yj {
			return yi < yj
		}
		return si < sj
	})

	// Take the latest 4 quarters and verify contiguity.
	last4 := periods[len(periods)-4:]
	if !quartersAreContiguous(last4) {
		return 0, false
	}

	var sum float64
	for _, p := range last4 {
		d := quarterly[p]
		if d == nil || d.Revenue <= 0 {
			return 0, false
		}
		sum += d.Revenue
	}
	return sum, true
}

// quartersAreContiguous verifies that the given (chronologically-sorted)
// quarter keys form an unbroken sequence — each subsequent quarter is
// either the next quarter of the same year (Q2 after Q1) or Q1 of the
// next year after Q4 of the prior year.
func quartersAreContiguous(periods []string) bool {
	if len(periods) < 2 {
		return true
	}
	for i := 1; i < len(periods); i++ {
		yPrev, sPrev := parsePeriodKey(periods[i-1])
		yCur, sCur := parsePeriodKey(periods[i])
		// parsePeriodKey returns sub in {1,2,3,4} for quarters; reject
		// any zero/FY-marker that snuck through.
		if sPrev < 1 || sPrev > 4 || sCur < 1 || sCur > 4 {
			return false
		}
		// Same-year next quarter, e.g. Q2 follows Q1.
		if yCur == yPrev && sCur == sPrev+1 {
			continue
		}
		// Year boundary: Q1 of year N+1 follows Q4 of year N.
		if yCur == yPrev+1 && sPrev == 4 && sCur == 1 {
			continue
		}
		return false
	}
	return true
}

// ttmPriorBridgeRevenue handles the partial-year IPO case: the latest
// year has 1-3 quarters and the prior year supplies the (4-N) missing
// quarters at the same calendar position. Example: latest year has
// Q1+Q2 → bridge with prior year's Q3+Q4 to synthesise a 12-month sum.
//
// Runs ahead of ttmFourQuartersRevenue in the public fallback chain so
// the partial-year IPO shape is reported via source=TTM_PRIOR_BRIDGE
// instead of being silently absorbed into TTM_4Q. The function declines
// (returns 0,false) for full-year shapes (latest year has 4 quarters),
// letting TTM_4Q take over for the gold-standard path. It also declines
// when the bridge cannot be cleanly built (no quarters, gaps in the
// latest year's Q1-start run, or missing/non-positive prior-year
// quarters).
func (h *HistoricalFinancialData) ttmPriorBridgeRevenue() (float64, bool) {
	quarterly := h.GetQuarterlyPeriods()
	if len(quarterly) == 0 {
		return 0, false
	}

	// Identify the latest year that has at least one quarter.
	latestYear := 0
	for p := range quarterly {
		y, s := parsePeriodKey(p)
		if s < 1 || s > 4 {
			continue
		}
		if y > latestYear {
			latestYear = y
		}
	}
	if latestYear == 0 {
		return 0, false
	}

	// Collect the latest year's quarters in ascending Q order.
	type qEntry struct {
		sub  int
		data *FinancialData
	}
	var current []qEntry
	for p, d := range quarterly {
		y, s := parsePeriodKey(p)
		if y == latestYear && s >= 1 && s <= 4 {
			current = append(current, qEntry{sub: s, data: d})
		}
	}
	sort.Slice(current, func(i, j int) bool { return current[i].sub < current[j].sub })

	// The bridge only applies when the latest year has 1-3 quarters AND
	// they form a contiguous run starting at Q1 (Q1, Q1+Q2, Q1+Q2+Q3).
	// A non-Q1-starting sequence (e.g. Q2+Q3) would be a stub-period
	// filer and is left for the annualized-quarter fallback to handle.
	if len(current) < 1 || len(current) > 3 {
		return 0, false
	}
	for i, q := range current {
		if q.sub != i+1 {
			return 0, false
		}
		if q.data == nil || q.data.Revenue <= 0 {
			return 0, false
		}
	}

	// Build the bridge: pull the missing quarters from latestYear-1 at
	// the same calendar position. Every required quarter must be
	// present and have positive revenue, otherwise we cannot construct
	// a clean 12-month synthetic window.
	var sum float64
	for _, q := range current {
		sum += q.data.Revenue
	}
	priorYear := latestYear - 1
	for sub := len(current) + 1; sub <= 4; sub++ {
		key := fmt.Sprintf("%dQ%d", priorYear, sub)
		d, ok := quarterly[key]
		if !ok || d == nil || d.Revenue <= 0 {
			return 0, false
		}
		sum += d.Revenue
	}
	return sum, true
}

// latestAnnualRevenue returns the most recent fiscal-year filing's revenue,
// the period key, and the filing date. Returns (_, _, _, false) when no FY
// period has positive revenue.
func (h *HistoricalFinancialData) latestAnnualRevenue() (float64, string, time.Time, bool) {
	annual := h.GetAnnualPeriods()
	if len(annual) == 0 {
		return 0, "", time.Time{}, false
	}

	// Pick the latest period by (year, sub) ordering — this is robust
	// even when FilingDate is zero in test fixtures.
	periods := make([]string, 0, len(annual))
	for p := range annual {
		periods = append(periods, p)
	}
	sort.Slice(periods, func(i, j int) bool {
		yi, si := parsePeriodKey(periods[i])
		yj, sj := parsePeriodKey(periods[j])
		if yi != yj {
			return yi < yj
		}
		return si < sj
	})

	// Walk from latest backward to find an FY with positive revenue.
	for i := len(periods) - 1; i >= 0; i-- {
		d := annual[periods[i]]
		if d == nil || d.Revenue <= 0 {
			continue
		}
		return d.Revenue, periods[i], d.FilingDate, true
	}
	return 0, "", time.Time{}, false
}

// annualizedQuarterRevenue is the lossy third-tier fallback. It scales the
// available 1-3 contiguous quarters of the latest year up to a synthetic
// 12 months: 4*Q1, 2*(Q1+Q2), or (4/3)*(Q1+Q2+Q3). Ignores seasonality —
// callers MUST surface the warning string emitted by the parent helper.
func (h *HistoricalFinancialData) annualizedQuarterRevenue() (float64, bool) {
	quarterly := h.GetQuarterlyPeriods()
	if len(quarterly) == 0 {
		return 0, false
	}

	// Identify the latest year that has at least one quarter.
	latestYear := 0
	for p := range quarterly {
		y, s := parsePeriodKey(p)
		if s < 1 || s > 4 {
			continue
		}
		if y > latestYear {
			latestYear = y
		}
	}
	if latestYear == 0 {
		return 0, false
	}

	// Collect contiguous Q1.. quarters from the latest year. We do NOT
	// support non-Q1-starting stubs here because the seasonality
	// assumptions would be even more questionable than the standard
	// 4× annualisation already is.
	var sum float64
	count := 0
	for sub := 1; sub <= 4; sub++ {
		key := fmt.Sprintf("%dQ%d", latestYear, sub)
		d, ok := quarterly[key]
		if !ok || d == nil || d.Revenue <= 0 {
			break
		}
		sum += d.Revenue
		count++
	}

	// If we didn't find Q1 of the latest year, try the single most-recent
	// quarter as a last-ditch 4× extrapolation. This covers stub-period
	// filers and oddities where Q1 was never reported.
	if count == 0 {
		var latestSub int
		var latestData *FinancialData
		for p, d := range quarterly {
			y, s := parsePeriodKey(p)
			if y != latestYear || s < 1 || s > 4 || d == nil || d.Revenue <= 0 {
				continue
			}
			if s > latestSub {
				latestSub = s
				latestData = d
			}
		}
		if latestData == nil {
			return 0, false
		}
		return latestData.Revenue * 4, true
	}

	switch count {
	case 1:
		return sum * 4, true
	case 2:
		return sum * 2, true
	case 3:
		return sum * 4.0 / 3.0, true
	default:
		// count == 4 should have been picked up by ttmFourQuartersRevenue;
		// returning the raw sum here is the safe behaviour anyway.
		return sum, true
	}
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

// RecentYoYGrowth returns the year-over-year revenue growth between the two
// most recent annual (FY) periods. Sorting is keyed on the period string
// (e.g. "2024FY") via GetSortedPeriods, NOT on FilingDate, so callers that
// have not (yet) stamped a FilingDate still get a deterministic result.
//
// Returns nil when:
//   - the receiver is nil (defensive — service.go calls this on
//     historicalData unguarded),
//   - fewer than 2 annual periods are available, or
//   - the prior period's revenue is zero (cannot compute growth from a zero
//     base; this is a data-quality issue, not actual zero growth — collapsing
//     the two would mislead the resolver).
//
// Used by service.go::performValuation (Tier 2 P0b) to populate
// profile.Facts.RevenueGrowthYoY for the resolver's Stage-2 maturity
// bucketing. Spec §5.1.
func (h *HistoricalFinancialData) RecentYoYGrowth() *float64 {
	if h == nil {
		return nil
	}
	// GetSortedPeriods returns periods in ascending order; filter to FY
	// entries so we compare like-for-like (a Q4 → FY transition would
	// otherwise produce a misleading rate).
	sorted := h.GetSortedPeriods()
	annualKeys := make([]string, 0, len(sorted))
	for _, key := range sorted {
		if len(key) >= 2 && key[len(key)-2:] == "FY" {
			annualKeys = append(annualKeys, key)
		}
	}
	if len(annualKeys) < 2 {
		return nil
	}
	latestKey := annualKeys[len(annualKeys)-1]
	priorKey := annualKeys[len(annualKeys)-2]
	latest := h.Data[latestKey]
	prior := h.Data[priorKey]
	if latest == nil || prior == nil || prior.Revenue == 0 {
		return nil
	}
	yoy := (latest.Revenue - prior.Revenue) / prior.Revenue
	return &yoy
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
