package leases

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// EstimationConfig holds configurable parameters for lease estimation
type EstimationConfig struct {
	DiscountRateMethod     string  `json:"discount_rate_method"`
	LeaseTermMethod        string  `json:"lease_term_method"`
	PaymentMethod          string  `json:"payment_method"`
	DefaultDiscountRate    float64 `json:"default_discount_rate"`
	DefaultLeaseTermYears  int     `json:"default_lease_term_years"`
	DefaultEscalationRate  float64 `json:"default_escalation_rate"`
	MinimumRate            float64 `json:"minimum_rate"`
	MaximumRate            float64 `json:"maximum_rate"`
	MinimumConfidenceScore float64 `json:"minimum_confidence_score"`

	// Industry-specific adjustments
	IndustryAdjustments map[string]IndustryAdjustment `json:"industry_adjustments"`

	// Caching and performance settings
	CacheEnabled       bool          `json:"cache_enabled"`
	CacheTTL           time.Duration `json:"cache_ttl"`
	CalculationTimeout time.Duration `json:"calculation_timeout"`
}

// IndustryAdjustment holds industry-specific parameters
type IndustryAdjustment struct {
	DiscountRateAdjustment float64 `json:"discount_rate_adjustment"`
	TypicalLeaseTermYears  int     `json:"typical_lease_term_years"`
	EscalationRate         float64 `json:"escalation_rate"`
	MaterialityThreshold   float64 `json:"materiality_threshold"`
}

// PresentValueResult contains the results of present value calculation
type PresentValueResult struct {
	PresentValue      float64                `json:"present_value"`
	DiscountRate      float64                `json:"discount_rate"`
	LeaseTermYears    int                    `json:"lease_term_years"`
	TotalPayments     float64                `json:"total_payments"`
	ConfidenceScore   float64                `json:"confidence_score"`
	CalculationMethod string                 `json:"calculation_method"`
	EstimationQuality string                 `json:"estimation_quality"`
	DataSources       []string               `json:"data_sources"`
	Assumptions       map[string]interface{} `json:"assumptions"`
	CalculationTime   time.Time              `json:"calculation_time"`
	ValidationFlags   []string               `json:"validation_flags"`
}

// DiscountRateResult contains discount rate estimation results
type DiscountRateResult struct {
	Rate             float64                `json:"rate"`
	Method           string                 `json:"method"`
	ConfidenceScore  float64                `json:"confidence_score"`
	DataSources      []string               `json:"data_sources"`
	IndustryAdjusted bool                   `json:"industry_adjusted"`
	Assumptions      map[string]interface{} `json:"assumptions"`
}

// LeaseTermResult contains lease term estimation results
type LeaseTermResult struct {
	LeaseTermYears  int                    `json:"lease_term_years"`
	Method          string                 `json:"method"`
	ConfidenceScore float64                `json:"confidence_score"`
	DataSources     []string               `json:"data_sources"`
	Assumptions     map[string]interface{} `json:"assumptions"`
}

// PaymentScheduleResult contains payment schedule estimation results
type PaymentScheduleResult struct {
	TotalPayments   float64                `json:"total_payments"`
	AnnualPayments  []float64              `json:"annual_payments"`
	Method          string                 `json:"method"`
	ConfidenceScore float64                `json:"confidence_score"`
	EscalationRate  float64                `json:"escalation_rate"`
	Assumptions     map[string]interface{} `json:"assumptions"`
}

// PresentValueCalculator is the main engine for calculating present value of leases
type PresentValueCalculator struct {
	config                *EstimationConfig
	discountRateEstimator *DiscountRateEstimator
	leaseTermEstimator    *LeaseTermEstimator
	paymentEstimator      *PaymentScheduleEstimator
}

// NewPresentValueCalculator creates a new present value calculator
func NewPresentValueCalculator(config *EstimationConfig) *PresentValueCalculator {
	return &PresentValueCalculator{
		config:                config,
		discountRateEstimator: NewDiscountRateEstimator(config),
		leaseTermEstimator:    NewLeaseTermEstimator(config),
		paymentEstimator:      NewPaymentScheduleEstimator(config),
	}
}

// CalculatePresentValue calculates the present value of operating lease commitments
func (c *PresentValueCalculator) CalculatePresentValue(ctx context.Context, data *entities.FinancialData, context *entities.CleaningContext) (*PresentValueResult, error) {
	startTime := time.Now()

	// Step 1: Validate input data
	if err := c.validateInputData(data, context); err != nil {
		// If no lease data at all, return emergency fallback result
		if data != nil && context != nil && len(data.OperatingLeaseCommitments) == 0 &&
			data.OperatingLeaseLiability == 0 && data.OperatingLeaseLiabilityCurrent == 0 &&
			data.OperatingLeaseLiabilityNoncurrent == 0 {
			return &PresentValueResult{
				PresentValue:      0,
				DiscountRate:      c.config.DefaultDiscountRate,
				LeaseTermYears:    c.config.DefaultLeaseTermYears,
				TotalPayments:     0,
				ConfidenceScore:   0.3,
				CalculationMethod: "emergency_fallback",
				EstimationQuality: "low",
				DataSources:       []string{"default_config"},
				Assumptions:       map[string]interface{}{"reason": "no_lease_data_available"},
				CalculationTime:   startTime,
				ValidationFlags:   []string{"insufficient_data"},
			}, nil
		}
		return nil, fmt.Errorf("input validation failed: %w", err)
	}

	// Step 2: Check if we have detailed commitments or just liability amounts
	hasDetailedCommitments := len(data.OperatingLeaseCommitments) > 0

	if !hasDetailedCommitments {
		// Use simplified calculation based on liability amounts
		return c.calculateSimplifiedPresentValue(data, context, startTime)
	}

	// Step 3: Estimate discount rate
	discountResult, err := c.discountRateEstimator.EstimateIncrementalBorrowingRate(ctx, data, context)
	if err != nil {
		return nil, fmt.Errorf("discount rate estimation failed: %w", err)
	}

	// Step 4: Estimate lease term
	leaseTermResult, err := c.leaseTermEstimator.EstimateFromCommitments(ctx, data.OperatingLeaseCommitments, context)
	if err != nil {
		return nil, fmt.Errorf("lease term estimation failed: %w", err)
	}

	// Step 5: Estimate payment schedule
	paymentResult, err := c.paymentEstimator.EstimateFromSchedule(ctx, data.OperatingLeaseCommitments, leaseTermResult.LeaseTermYears)
	if err != nil {
		return nil, fmt.Errorf("payment schedule estimation failed: %w", err)
	}

	// Step 6: Calculate present value
	presentValue, err := c.calculatePresentValue(paymentResult.AnnualPayments, discountResult.Rate)
	if err != nil {
		return nil, fmt.Errorf("present value calculation failed: %w", err)
	}

	// Step 7: Calculate overall confidence score
	confidenceScore := c.calculateConfidenceScore(discountResult, leaseTermResult, paymentResult)

	// Step 8: Determine estimation quality
	quality := c.determineEstimationQuality(confidenceScore)

	// Step 9: Validate result reasonableness
	validationFlags := c.validateResult(presentValue, data, context)

	// Step 10: Collect data sources and assumptions
	dataSources := c.collectDataSources(discountResult, leaseTermResult, paymentResult)
	assumptions := c.collectAssumptions(discountResult, leaseTermResult, paymentResult)

	return &PresentValueResult{
		PresentValue:      presentValue,
		DiscountRate:      discountResult.Rate,
		LeaseTermYears:    leaseTermResult.LeaseTermYears,
		TotalPayments:     paymentResult.TotalPayments,
		ConfidenceScore:   confidenceScore,
		CalculationMethod: c.determineCalculationMethod(discountResult, leaseTermResult, paymentResult),
		EstimationQuality: quality,
		DataSources:       dataSources,
		Assumptions:       assumptions,
		CalculationTime:   startTime,
		ValidationFlags:   validationFlags,
	}, nil
}

// calculateSimplifiedPresentValue provides a simpler calculation when only liability amounts are available
func (c *PresentValueCalculator) calculateSimplifiedPresentValue(data *entities.FinancialData, context *entities.CleaningContext, startTime time.Time) (*PresentValueResult, error) {
	// Get total lease liability
	totalLeaseLiability := data.OperatingLeaseLiability
	if totalLeaseLiability == 0 {
		totalLeaseLiability = data.OperatingLeaseLiabilityCurrent + data.OperatingLeaseLiabilityNoncurrent
	}

	// Estimate discount rate using simple method
	discountRate := c.estimateSimpleDiscountRate(data, context)
	if discountRate <= 0 {
		return nil, fmt.Errorf("discount rate must be positive")
	}

	// Use industry-specific lease term
	leaseTermYears := c.getIndustryLeaseTermYears(context.IndustryCode)

	// For simplified calculation, treat the current liability as the present value
	// and reverse-engineer the payment schedule
	annualPayment := (totalLeaseLiability * discountRate) / (1 - math.Pow(1+discountRate, -float64(leaseTermYears)))
	totalPayments := annualPayment * float64(leaseTermYears)

	// Build assumptions for audit trail
	assumptions := map[string]interface{}{
		"total_lease_liability": totalLeaseLiability,
		"discount_rate":         discountRate,
		"lease_term_years":      leaseTermYears,
		"annual_payment":        annualPayment,
		"calculation_method":    "simplified_liability_based",
	}

	return &PresentValueResult{
		PresentValue:      totalLeaseLiability, // Use the liability amount as PV
		DiscountRate:      discountRate,
		LeaseTermYears:    leaseTermYears,
		TotalPayments:     totalPayments,
		ConfidenceScore:   0.7, // Medium confidence for simplified calculation
		CalculationMethod: "simplified_liability_based",
		EstimationQuality: "medium",
		DataSources:       []string{"operating_lease_liability", "industry_benchmarks"},
		Assumptions:       assumptions,
		CalculationTime:   startTime,
		ValidationFlags:   []string{}, // TODO: Add validation for simplified method
	}, nil
}

// estimateSimpleDiscountRate provides a simple discount rate estimation
func (c *PresentValueCalculator) estimateSimpleDiscountRate(data *entities.FinancialData, context *entities.CleaningContext) float64 {
	// Start with risk-free rate or default
	baseRate := data.RiskFreeRate
	if baseRate <= 0 {
		baseRate = 0.025 // Default 2.5%
	}

	// Add credit spread based on cost of debt if available
	creditSpread := 0.03 // Default 3%
	if data.TotalDebt > 0 && data.InterestExpense > 0 {
		effectiveRate := data.InterestExpense / data.TotalDebt
		if effectiveRate > baseRate {
			creditSpread = effectiveRate - baseRate
		}
	}

	// Apply industry adjustment
	industryAdjustment := c.getIndustryDiscountAdjustment(context.IndustryCode)

	finalRate := baseRate + creditSpread + industryAdjustment

	// Apply reasonable bounds
	if finalRate < 0.01 { // 1% minimum
		finalRate = 0.01
	} else if finalRate > 0.20 { // 20% maximum
		finalRate = 0.20
	}

	return finalRate
}

// getIndustryLeaseTermYears returns typical lease terms for industries
func (c *PresentValueCalculator) getIndustryLeaseTermYears(industryCode string) int {
	switch industryCode {
	case "44": // Retail Trade
		return 12
	case "45", "51": // Technology/Information services
		return 8
	case "31", "32", "33": // Manufacturing
		return 15
	case "52": // Finance and Insurance
		return 10
	default:
		return c.config.DefaultLeaseTermYears
	}
}

// getIndustryDiscountAdjustment returns industry-specific discount rate adjustments
func (c *PresentValueCalculator) getIndustryDiscountAdjustment(industryCode string) float64 {
	switch industryCode {
	case "44": // Retail Trade
		return 0.005 // Slightly higher risk
	case "45", "51": // Technology/Information services
		return -0.01 // Lower risk
	case "31", "32", "33": // Manufacturing
		return 0.002 // Moderate risk
	case "52": // Finance and Insurance
		return -0.005 // Lower risk
	default:
		return 0.0 // Neutral
	}
}

// validateInputData validates the input financial data
func (c *PresentValueCalculator) validateInputData(data *entities.FinancialData, context *entities.CleaningContext) error {
	if data == nil {
		return fmt.Errorf("financial data cannot be nil")
	}

	if context == nil {
		return fmt.Errorf("cleaning context cannot be nil")
	}

	// Check if we have any lease-related data
	hasLeaseData := len(data.OperatingLeaseCommitments) > 0 ||
		data.OperatingLeaseLiability > 0 ||
		data.OperatingLeaseLiabilityCurrent > 0 ||
		data.OperatingLeaseLiabilityNoncurrent > 0

	if !hasLeaseData {
		return fmt.Errorf("no operating lease data available for calculation")
	}

	return nil
}

// calculatePresentValue calculates the present value of annual payments
func (c *PresentValueCalculator) calculatePresentValue(annualPayments []float64, discountRate float64) (float64, error) {
	if len(annualPayments) == 0 {
		return 0, fmt.Errorf("no annual payments provided")
	}

	if discountRate <= 0 {
		return 0, fmt.Errorf("discount rate must be positive")
	}

	var presentValue float64
	for year, payment := range annualPayments {
		if payment <= 0 {
			continue
		}

		// Calculate present value factor: 1 / (1 + r)^n
		discountFactor := math.Pow(1+discountRate, float64(year+1))
		presentValue += payment / discountFactor
	}

	return presentValue, nil
}

// calculateConfidenceScore calculates overall confidence in the estimation
func (c *PresentValueCalculator) calculateConfidenceScore(discountResult *DiscountRateResult,
	leaseTermResult *LeaseTermResult, paymentResult *PaymentScheduleResult) float64 {

	// Weighted average of component confidence scores
	discountWeight := 0.4
	leaseTermWeight := 0.3
	paymentWeight := 0.3

	score := discountWeight*discountResult.ConfidenceScore +
		leaseTermWeight*leaseTermResult.ConfidenceScore +
		paymentWeight*paymentResult.ConfidenceScore

	// Ensure score is within valid range
	if score < 0 {
		score = 0
	} else if score > 1 {
		score = 1
	}

	return score
}

// determineEstimationQuality determines the quality level of the estimation
func (c *PresentValueCalculator) determineEstimationQuality(confidenceScore float64) string {
	if confidenceScore >= 0.8 {
		return "high"
	} else if confidenceScore >= 0.6 {
		return "medium"
	} else if confidenceScore >= 0.4 {
		return "low"
	}
	return "very_low"
}

// validateResult validates the reasonableness of the calculated result
func (c *PresentValueCalculator) validateResult(presentValue float64, data *entities.FinancialData, context *entities.CleaningContext) []string {
	var flags []string

	// Check if present value is reasonable relative to revenue
	if data.Revenue > 0 {
		pvToRevenue := presentValue / data.Revenue
		if pvToRevenue > 2.0 {
			flags = append(flags, "present_value_exceeds_200_percent_of_revenue")
		}
	}

	// Check if present value is reasonable relative to assets
	if data.TotalAssets > 0 {
		pvToAssets := presentValue / data.TotalAssets
		if pvToAssets > 0.8 {
			flags = append(flags, "present_value_exceeds_80_percent_of_assets")
		}
	}

	// Check if result is suspiciously low
	if presentValue < 1000 {
		flags = append(flags, "present_value_suspiciously_low")
	}

	// Check if result is suspiciously high
	if presentValue > 100000000 { // $100M
		flags = append(flags, "present_value_suspiciously_high")
	}

	return flags
}

// collectDataSources collects all data sources used in the calculation
func (c *PresentValueCalculator) collectDataSources(discountResult *DiscountRateResult,
	leaseTermResult *LeaseTermResult, paymentResult *PaymentScheduleResult) []string {

	sources := make(map[string]bool)

	// Add sources from discount rate estimation
	for _, source := range discountResult.DataSources {
		sources[source] = true
	}

	// Add sources from lease term estimation
	for _, source := range leaseTermResult.DataSources {
		sources[source] = true
	}

	// Convert map to slice
	var result []string
	for source := range sources {
		result = append(result, source)
	}

	return result
}

// collectAssumptions collects all assumptions used in the calculation
func (c *PresentValueCalculator) collectAssumptions(discountResult *DiscountRateResult,
	leaseTermResult *LeaseTermResult, paymentResult *PaymentScheduleResult) map[string]interface{} {

	assumptions := make(map[string]interface{})

	// Add discount rate assumptions
	assumptions["discount_rate_method"] = discountResult.Method
	assumptions["discount_rate"] = discountResult.Rate
	assumptions["discount_rate_industry_adjusted"] = discountResult.IndustryAdjusted

	// Add lease term assumptions
	assumptions["lease_term_method"] = leaseTermResult.Method
	assumptions["lease_term_years"] = leaseTermResult.LeaseTermYears

	// Add payment schedule assumptions
	assumptions["payment_method"] = paymentResult.Method
	assumptions["escalation_rate"] = paymentResult.EscalationRate
	assumptions["total_payments"] = paymentResult.TotalPayments

	return assumptions
}

// determineCalculationMethod determines the primary calculation method used
func (c *PresentValueCalculator) determineCalculationMethod(discountResult *DiscountRateResult,
	leaseTermResult *LeaseTermResult, paymentResult *PaymentScheduleResult) string {

	// Prioritize the most sophisticated method used
	if paymentResult.Method == "schedule_extraction" {
		return "schedule_extraction"
	} else if leaseTermResult.Method == "disclosed_commitments" {
		return "disclosed_commitments"
	} else if discountResult.Method == "incremental_borrowing_rate" {
		return "incremental_borrowing_rate"
	}

	return "mixed_estimation"
}

// DiscountRateEstimator handles discount rate estimation
type DiscountRateEstimator struct {
	config *EstimationConfig
}

// NewDiscountRateEstimator creates a new discount rate estimator
func NewDiscountRateEstimator(config *EstimationConfig) *DiscountRateEstimator {
	return &DiscountRateEstimator{
		config: config,
	}
}

// EstimateIncrementalBorrowingRate estimates the incremental borrowing rate
func (e *DiscountRateEstimator) EstimateIncrementalBorrowingRate(ctx context.Context, data *entities.FinancialData, context *entities.CleaningContext) (*DiscountRateResult, error) {
	switch e.config.DiscountRateMethod {
	case "incremental_borrowing_rate":
		return e.estimateIncrementalBorrowingRate(data, context)
	case "cost_of_debt":
		return e.estimateCostOfDebt(data, context)
	case "risk_free_plus_spread":
		return e.estimateRiskFreePlusSpread(data, context)
	default:
		return e.estimateIncrementalBorrowingRate(data, context)
	}
}

// estimateIncrementalBorrowingRate implements the incremental borrowing rate method
func (e *DiscountRateEstimator) estimateIncrementalBorrowingRate(data *entities.FinancialData, context *entities.CleaningContext) (*DiscountRateResult, error) {
	// Start with risk-free rate
	baseRate := data.RiskFreeRate
	if baseRate <= 0 {
		baseRate = 0.025 // Default 2.5%
	}

	// Calculate credit spread based on existing debt costs
	creditSpread := 0.03 // Default 3%
	confidenceScore := 0.5
	var dataSources []string

	if data.TotalDebt > 0 && data.InterestExpense > 0 {
		effectiveRate := data.InterestExpense / data.TotalDebt
		creditSpread = effectiveRate - baseRate
		confidenceScore = 0.8
		dataSources = []string{"risk_free_rate", "effective_debt_cost"}
	}

	// Apply industry adjustment
	industryAdjustment := e.getIndustryAdjustment(context.IndustryCode)
	finalRate := baseRate + creditSpread + industryAdjustment

	// Apply bounds with reasonable defaults if not configured
	minRate := e.config.MinimumRate
	if minRate <= 0 {
		minRate = 0.005 // Default minimum 0.5%
	}

	maxRate := e.config.MaximumRate
	if maxRate <= 0 {
		maxRate = 0.25 // Default maximum 25%
	}

	if finalRate < minRate {
		finalRate = minRate
	} else if finalRate > maxRate {
		finalRate = maxRate
	}

	assumptions := map[string]interface{}{
		"base_rate":           baseRate,
		"credit_spread":       creditSpread,
		"industry_adjustment": industryAdjustment,
		"final_rate":          finalRate,
	}

	return &DiscountRateResult{
		Rate:             finalRate,
		Method:           "incremental_borrowing_rate",
		ConfidenceScore:  confidenceScore,
		DataSources:      dataSources,
		IndustryAdjusted: true,
		Assumptions:      assumptions,
	}, nil
}

// estimateCostOfDebt implements the cost of debt method
func (e *DiscountRateEstimator) estimateCostOfDebt(data *entities.FinancialData, context *entities.CleaningContext) (*DiscountRateResult, error) {
	var rate float64
	var confidenceScore float64
	var dataSources []string

	// Try to calculate effective debt cost
	if data.TotalDebt > 0 && data.InterestExpense > 0 {
		rate = data.InterestExpense / data.TotalDebt
		confidenceScore = 0.9
		dataSources = []string{"interest_expense", "total_debt"}
	} else {
		// Fallback to risk-free plus spread
		rate = data.RiskFreeRate + 0.03
		confidenceScore = 0.4
		dataSources = []string{"risk_free_rate", "default_spread"}
	}

	// Apply industry adjustment
	industryAdjustment := e.getIndustryAdjustment(context.IndustryCode)
	rate += industryAdjustment

	// Apply bounds with reasonable defaults if not configured
	minRate := e.config.MinimumRate
	if minRate <= 0 {
		minRate = 0.005 // Default minimum 0.5%
	}

	maxRate := e.config.MaximumRate
	if maxRate <= 0 {
		maxRate = 0.25 // Default maximum 25%
	}

	if rate < minRate {
		rate = minRate
	} else if rate > maxRate {
		rate = maxRate
	}

	assumptions := map[string]interface{}{
		"effective_debt_cost": rate - industryAdjustment,
		"industry_adjustment": industryAdjustment,
		"final_rate":          rate,
	}

	return &DiscountRateResult{
		Rate:             rate,
		Method:           "cost_of_debt",
		ConfidenceScore:  confidenceScore,
		DataSources:      dataSources,
		IndustryAdjusted: true,
		Assumptions:      assumptions,
	}, nil
}

// estimateRiskFreePlusSpread implements the risk-free plus spread method
func (e *DiscountRateEstimator) estimateRiskFreePlusSpread(data *entities.FinancialData, context *entities.CleaningContext) (*DiscountRateResult, error) {
	baseRate := data.RiskFreeRate
	if baseRate <= 0 {
		baseRate = 0.025 // Default 2.5%
	}

	// Base spread
	baseSpread := 0.03 // 3%

	// Apply industry adjustment
	industryAdjustment := e.getIndustryAdjustment(context.IndustryCode)
	finalRate := baseRate + baseSpread + industryAdjustment

	// Apply bounds
	if finalRate < e.config.MinimumRate {
		finalRate = e.config.MinimumRate
	} else if finalRate > e.config.MaximumRate {
		finalRate = e.config.MaximumRate
	}

	assumptions := map[string]interface{}{
		"risk_free_rate":      baseRate,
		"base_spread":         baseSpread,
		"industry_adjustment": industryAdjustment,
		"final_rate":          finalRate,
	}

	return &DiscountRateResult{
		Rate:             finalRate,
		Method:           "risk_free_plus_spread",
		ConfidenceScore:  0.6,
		DataSources:      []string{"risk_free_rate", "base_spread"},
		IndustryAdjusted: true,
		Assumptions:      assumptions,
	}, nil
}

// getIndustryAdjustment returns industry-specific discount rate adjustment
func (e *DiscountRateEstimator) getIndustryAdjustment(industryCode string) float64 {
	// Map NAICS industry codes to industry names used in configuration
	var industryName string
	switch industryCode {
	case "44": // Retail Trade
		industryName = "retail"
	case "45": // Technology/Information
		industryName = "technology"
	case "31", "32", "33": // Manufacturing
		industryName = "manufacturing"
	case "52": // Finance and Insurance
		industryName = "financial"
	case "51": // Information services
		industryName = "technology"
	default:
		// Return 0 for unknown industries (neutral adjustment)
		return 0.0
	}

	if adjustment, exists := e.config.IndustryAdjustments[industryName]; exists {
		return adjustment.DiscountRateAdjustment
	}

	return 0.0 // Default neutral adjustment
}

// LeaseTermEstimator handles lease term estimation
type LeaseTermEstimator struct {
	config *EstimationConfig
}

// NewLeaseTermEstimator creates a new lease term estimator
func NewLeaseTermEstimator(config *EstimationConfig) *LeaseTermEstimator {
	return &LeaseTermEstimator{
		config: config,
	}
}

// EstimateFromCommitments estimates lease term from commitment disclosures
func (e *LeaseTermEstimator) EstimateFromCommitments(ctx context.Context, commitments map[string]float64, context *entities.CleaningContext) (*LeaseTermResult, error) {
	switch e.config.LeaseTermMethod {
	case "disclosed_commitments":
		return e.estimateFromDisclosedCommitments(commitments, context)
	case "industry_benchmarks":
		return e.estimateFromIndustryBenchmarks(context)
	default:
		return e.estimateFromDisclosedCommitments(commitments, context)
	}
}

// estimateFromDisclosedCommitments extracts lease term from disclosed commitments
func (e *LeaseTermEstimator) estimateFromDisclosedCommitments(commitments map[string]float64, context *entities.CleaningContext) (*LeaseTermResult, error) {
	if len(commitments) == 0 {
		// Fallback to industry benchmarks
		return e.estimateFromIndustryBenchmarks(context)
	}

	// Count years with commitments
	maxYear := 0
	for yearStr := range commitments {
		// Parse year from string (e.g., "Year1", "Year2")
		if len(yearStr) >= 5 && yearStr[:4] == "Year" {
			if year, err := strconv.Atoi(yearStr[4:]); err == nil {
				if year > maxYear {
					maxYear = year
				}
			}
		}
	}

	if maxYear == 0 {
		return e.estimateFromIndustryBenchmarks(context)
	}

	assumptions := map[string]interface{}{
		"commitment_years": maxYear,
		"data_source":      "disclosed_commitments",
	}

	return &LeaseTermResult{
		LeaseTermYears:  maxYear,
		Method:          "disclosed_commitments",
		ConfidenceScore: 0.9,
		DataSources:     []string{"operating_lease_commitments"},
		Assumptions:     assumptions,
	}, nil
}

// estimateFromIndustryBenchmarks uses industry-specific benchmarks
func (e *LeaseTermEstimator) estimateFromIndustryBenchmarks(context *entities.CleaningContext) (*LeaseTermResult, error) {
	var leaseTermYears int

	switch context.IndustryCode {
	case "44": // Retail
		leaseTermYears = 12
	case "45": // Technology
		leaseTermYears = 8
	case "31", "32", "33": // Manufacturing
		leaseTermYears = 15
	case "52": // Financial Services
		leaseTermYears = 10
	default:
		leaseTermYears = e.config.DefaultLeaseTermYears
	}

	assumptions := map[string]interface{}{
		"industry_code":    context.IndustryCode,
		"lease_term_years": leaseTermYears,
		"data_source":      "industry_benchmarks",
	}

	return &LeaseTermResult{
		LeaseTermYears:  leaseTermYears,
		Method:          "industry_benchmarks",
		ConfidenceScore: 0.7,
		DataSources:     []string{"industry_benchmarks"},
		Assumptions:     assumptions,
	}, nil
}

// PaymentScheduleEstimator handles payment schedule estimation
type PaymentScheduleEstimator struct {
	config *EstimationConfig
}

// NewPaymentScheduleEstimator creates a new payment schedule estimator
func NewPaymentScheduleEstimator(config *EstimationConfig) *PaymentScheduleEstimator {
	return &PaymentScheduleEstimator{
		config: config,
	}
}

// EstimateFromSchedule estimates payment schedule from available data
func (e *PaymentScheduleEstimator) EstimateFromSchedule(ctx context.Context, commitments map[string]float64, leaseTermYears int) (*PaymentScheduleResult, error) {
	switch e.config.PaymentMethod {
	case "schedule_extraction":
		return e.estimateFromScheduleExtraction(commitments, leaseTermYears)
	case "straight_line":
		return e.estimateFromStraightLine(commitments, leaseTermYears)
	default:
		// Try schedule extraction first, fall back to straight line if needed
		result, err := e.estimateFromScheduleExtraction(commitments, leaseTermYears)
		if err != nil {
			return e.estimateFromStraightLine(commitments, leaseTermYears)
		}
		return result, nil
	}
}

// estimateFromScheduleExtraction extracts payments from disclosed schedule
func (e *PaymentScheduleEstimator) estimateFromScheduleExtraction(commitments map[string]float64, leaseTermYears int) (*PaymentScheduleResult, error) {
	if len(commitments) == 0 {
		return e.estimateFromStraightLine(commitments, leaseTermYears)
	}

	annualPayments := make([]float64, leaseTermYears)
	totalPayments := 0.0

	// Extract explicit payments
	for i := 0; i < leaseTermYears; i++ {
		yearKey := fmt.Sprintf("Year%d", i+1)
		if payment, exists := commitments[yearKey]; exists {
			annualPayments[i] = payment
			totalPayments += payment
		}
	}

	// If we have some but not all payments, estimate the rest
	if len(commitments) > 0 && len(commitments) < leaseTermYears {
		// Calculate average escalation rate from available data
		escalationRate := e.calculateEscalationRate(commitments)

		// Fill in missing payments
		lastKnownPayment := 0.0
		lastKnownYear := 0

		for i := leaseTermYears - 1; i >= 0; i-- {
			if annualPayments[i] > 0 {
				lastKnownPayment = annualPayments[i]
				lastKnownYear = i
				break
			}
		}

		if lastKnownPayment > 0 {
			for i := lastKnownYear + 1; i < leaseTermYears; i++ {
				years := i - lastKnownYear
				annualPayments[i] = lastKnownPayment * math.Pow(1+escalationRate, float64(years))
				totalPayments += annualPayments[i]
			}
		}
	}

	assumptions := map[string]interface{}{
		"explicit_payments": len(commitments),
		"total_years":       leaseTermYears,
		"escalation_rate":   e.calculateEscalationRate(commitments),
		"data_source":       "schedule_extraction",
	}

	return &PaymentScheduleResult{
		TotalPayments:   totalPayments,
		AnnualPayments:  annualPayments,
		Method:          "schedule_extraction",
		ConfidenceScore: 0.8,
		EscalationRate:  e.calculateEscalationRate(commitments),
		Assumptions:     assumptions,
	}, nil
}

// estimateFromStraightLine estimates payments using straight-line method
func (e *PaymentScheduleEstimator) estimateFromStraightLine(commitments map[string]float64, leaseTermYears int) (*PaymentScheduleResult, error) {
	// Get base payment (either from Year1 or calculate average)
	basePayment := 0.0
	if len(commitments) > 0 {
		// Use average of available payments
		total := 0.0
		count := 0
		for _, payment := range commitments {
			total += payment
			count++
		}
		basePayment = total / float64(count)
	} else {
		// No data available - cannot estimate
		return nil, fmt.Errorf("no payment data available for straight-line estimation")
	}

	// Generate payments with escalation
	annualPayments := make([]float64, leaseTermYears)
	totalPayments := 0.0
	escalationRate := e.config.DefaultEscalationRate

	for i := 0; i < leaseTermYears; i++ {
		payment := basePayment * math.Pow(1+escalationRate, float64(i))
		annualPayments[i] = payment
		totalPayments += payment
	}

	assumptions := map[string]interface{}{
		"base_payment":    basePayment,
		"escalation_rate": escalationRate,
		"total_years":     leaseTermYears,
		"data_source":     "straight_line",
	}

	return &PaymentScheduleResult{
		TotalPayments:   totalPayments,
		AnnualPayments:  annualPayments,
		Method:          "straight_line",
		ConfidenceScore: 0.6,
		EscalationRate:  escalationRate,
		Assumptions:     assumptions,
	}, nil
}

// calculateEscalationRate calculates escalation rate from available payments
func (e *PaymentScheduleEstimator) calculateEscalationRate(commitments map[string]float64) float64 {
	if len(commitments) < 2 {
		return e.config.DefaultEscalationRate
	}

	// Sort years and calculate year-over-year growth
	var growthRates []float64
	for i := 1; i < len(commitments); i++ {
		year1Key := fmt.Sprintf("Year%d", i)
		year2Key := fmt.Sprintf("Year%d", i+1)

		if payment1, exists1 := commitments[year1Key]; exists1 {
			if payment2, exists2 := commitments[year2Key]; exists2 {
				if payment1 > 0 {
					growthRate := (payment2 - payment1) / payment1
					growthRates = append(growthRates, growthRate)
				}
			}
		}
	}

	if len(growthRates) == 0 {
		return e.config.DefaultEscalationRate
	}

	// Calculate average growth rate
	total := 0.0
	for _, rate := range growthRates {
		total += rate
	}

	return total / float64(len(growthRates))
}
