package adjustments

import (
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// LiabilityAdjuster handles Category B adjustments from SEC cleaning guide
// Implements under-stated liabilities and off-balance-sheet exposures
type LiabilityAdjuster struct {
	// TODO: Add configuration for adjustment thresholds
}

// NewLiabilityAdjuster creates a new liability adjuster instance
func NewLiabilityAdjuster() *LiabilityAdjuster {
	return &LiabilityAdjuster{}
}

// LiabilityAdjustmentResult represents the result of applying liability adjustments
type LiabilityAdjustmentResult struct {
	Applied                  bool                  `json:"applied"`
	TotalLiabilityAdjustment float64               `json:"total_liability_adjustment"`
	AdjustedTotalDebt        float64               `json:"adjusted_total_debt"`
	Adjustments              []entities.Adjustment `json:"adjustments"`
	Flags                    []entities.Flag       `json:"flags"`
	AuditTrail               string                `json:"audit_trail"`
}

// ProcessLiabilityAdjustments orchestrates all Category B liability adjustments
func (la *LiabilityAdjuster) ProcessLiabilityAdjustments(data *entities.FinancialData, rules []*entities.CleaningRule, context *entities.CleaningContext) *LiabilityAdjustmentResult {
	var allAdjustments []entities.Adjustment
	var allFlags []entities.Flag
	var totalAdjustment float64
	originalDebt := data.TotalDebt

	// Process each Category B rule
	for _, rule := range rules {
		if rule.Category != entities.LiabilityCompleteness || !rule.Enabled {
			continue
		}

		var result *AdjustmentResult

		switch rule.ID {
		case "operating_leases":
			result = la.ProcessOperatingLeaseAdjustment(data, rule, context)
		case "pension_obligations":
			result = la.ProcessPensionAdjustment(data, rule, context)
		case "contingent_liabilities":
			result = la.ProcessContingentLiabilityAdjustment(data, rule, context)
		default:
			continue // Skip unknown rules
		}

		if result != nil && result.Applied {
			allAdjustments = append(allAdjustments, result.Adjustments...)
			allFlags = append(allFlags, result.Flags...)
			totalAdjustment += result.Amount

			// Add to debt base for WACC calculations
			data.TotalDebt += result.Amount
			data.InterestBearingDebt += result.Amount
		}
	}

	applied := len(allAdjustments) > 0
	auditTrail := fmt.Sprintf("Processed %d Category B liability rules, total adjustment: %.0f, debt increased from %.0f to %.0f",
		len(rules), totalAdjustment, originalDebt, data.TotalDebt)

	return &LiabilityAdjustmentResult{
		Applied:                  applied,
		TotalLiabilityAdjustment: totalAdjustment,
		AdjustedTotalDebt:        data.TotalDebt,
		Adjustments:              allAdjustments,
		Flags:                    allFlags,
		AuditTrail:               auditTrail,
	}
}

// ProcessOperatingLeaseAdjustment implements B1 rule: Operating lease liabilities as debt
func (la *LiabilityAdjuster) ProcessOperatingLeaseAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	// Calculate total operating lease liability
	totalLeaseObligation := data.OperatingLeaseLiability
	if totalLeaseObligation == 0 {
		totalLeaseObligation = data.OperatingLeaseLiabilityCurrent + data.OperatingLeaseLiabilityNoncurrent
	}

	if totalLeaseObligation <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No operating lease obligations present to capitalize",
		}
	}

	// Calculate lease-to-asset ratio for materiality assessment
	leaseRatio := totalLeaseObligation / data.TotalAssets

	// Industry-specific threshold application
	threshold := la.getLeaseThresholdForIndustry(context.IndustryCode)

	// Create adjustment record (always treat leases as debt for WACC)
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("lease-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.LiabilityCompleteness,
		Type:        entities.TreatAsDebt,
		Amount:      totalLeaseObligation,
		FromAccount: "OperatingLeaseObligations",
		ToAccount:   "InterestBearingDebt",
		Percentage:  leaseRatio * 100,
		Reasoning:   fmt.Sprintf("Treated operating lease obligations (%.1f%% of assets) as debt per B1 rule", leaseRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Generate flags based on materiality and industry context
	var flags []entities.Flag
	if leaseRatio >= threshold {
		severity := la.getSeverityForLeaseRatio(leaseRatio, context.IndustryCode)

		flag := entities.Flag{
			ID:             fmt.Sprintf("lease-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "operating_lease_obligation",
			Severity:       severity,
			Amount:         totalLeaseObligation,
			Percentage:     leaseRatio * 100,
			Description:    fmt.Sprintf("Material operating lease obligations (%.1f%% of assets) added to debt", leaseRatio*100),
			Recommendation: la.getLeaseRecommendation(context.IndustryCode, leaseRatio),
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      totalLeaseObligation,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Capitalized %.0f in operating lease obligations as debt", totalLeaseObligation),
	}
}

// ProcessPensionAdjustment implements B2 rule: Under-funded pension obligations as debt
func (la *LiabilityAdjuster) ProcessPensionAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	// Calculate pension underfunding
	var pensionUnderfunding float64
	if data.ProjectedBenefitObligation > 0 && data.PensionPlanAssets > 0 {
		underfunding := data.ProjectedBenefitObligation - data.PensionPlanAssets
		if underfunding > 0 {
			pensionUnderfunding = underfunding
		}
	} else if data.PensionLiabilities > 0 {
		// Use net pension liability if PBO/plan assets not available
		pensionUnderfunding = data.PensionLiabilities
	}

	// Add OPEB liability
	totalPensionObligation := pensionUnderfunding + data.OPEBLiability

	if totalPensionObligation <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No under-funded pension or OPEB obligations present",
		}
	}

	// Calculate pension-to-revenue ratio for assessment
	pensionRatio := totalPensionObligation / data.Revenue

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("pension-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.LiabilityCompleteness,
		Type:        entities.TreatAsDebt,
		Amount:      totalPensionObligation,
		FromAccount: "PensionUnderfunding",
		ToAccount:   "InterestBearingDebt",
		Percentage:  pensionRatio * 100,
		Reasoning:   fmt.Sprintf("Added under-funded pension/OPEB obligations (%.1f%% of revenue) to debt per B2 rule", pensionRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Generate flags for material pension obligations
	var flags []entities.Flag
	threshold := la.getPensionThresholdForIndustry(context.IndustryCode)

	if pensionRatio >= threshold {
		severity := la.getSeverityForPensionRatio(pensionRatio, context.IndustryCode)

		flag := entities.Flag{
			ID:             fmt.Sprintf("pension-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "pension_underfunding",
			Severity:       severity,
			Amount:         totalPensionObligation,
			Percentage:     pensionRatio * 100,
			Description:    fmt.Sprintf("Material pension underfunding (%.1f%% of revenue) added to debt", pensionRatio*100),
			Recommendation: "Monitor pension funding status and potential cash flow impact from required contributions",
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      totalPensionObligation,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Added %.0f in under-funded pension/OPEB obligations to debt", totalPensionObligation),
	}
}

// ProcessContingentLiabilityAdjustment implements B3 rule: Contingent liability estimation
func (la *LiabilityAdjuster) ProcessContingentLiabilityAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	// Aggregate all contingent liability sources
	totalContingentLiability := data.ContingentLiabilities +
		data.EnvironmentalLiabilities +
		data.LitigationLiabilities

	if totalContingentLiability <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No contingent liabilities disclosed to assess",
		}
	}

	// Calculate contingent liability ratio for materiality
	contingentRatio := totalContingentLiability / data.Revenue

	// Apply conservative probability weighting (typically 30-70% for disclosed contingencies)
	// TODO: Integrate AI service for footnote analysis to get more precise probability estimates
	probabilityWeight := la.getContingentLiabilityProbability(context.IndustryCode, totalContingentLiability)
	weightedAmount := totalContingentLiability * probabilityWeight

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("contingent-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.LiabilityCompleteness,
		Type:        entities.ProbabilityWeighted,
		Amount:      weightedAmount,
		FromAccount: "ContingentLiabilities",
		ToAccount:   "EstimatedLiabilities",
		Percentage:  contingentRatio * 100,
		Reasoning:   fmt.Sprintf("Applied %.0f%% probability weighting to contingent liabilities (%.1f%% of revenue) per B3 rule", probabilityWeight*100, contingentRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Generate flags for material contingent exposures
	var flags []entities.Flag
	threshold := la.getContingentLiabilityThreshold(context.IndustryCode)

	if contingentRatio >= threshold {
		severity := la.getSeverityForContingentRatio(contingentRatio, context.IndustryCode)

		flag := entities.Flag{
			ID:             fmt.Sprintf("contingent-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "contingent_liability_exposure",
			Severity:       severity,
			Amount:         weightedAmount,
			Percentage:     contingentRatio * 100,
			Description:    fmt.Sprintf("Material contingent liability exposure (%.1f%% of revenue) with %.0f%% probability weighting", contingentRatio*100, probabilityWeight*100),
			Recommendation: la.getContingentLiabilityRecommendation(context.IndustryCode),
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      weightedAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Applied probability-weighted adjustment of %.0f for contingent liabilities", weightedAmount),
	}
}

// Industry-specific threshold and severity methods

func (la *LiabilityAdjuster) getLeaseThresholdForIndustry(industryCode string) float64 {
	switch industryCode {
	case "44": // Retail Trade - high lease tolerance
		return 0.15 // 15% threshold
	case "45": // Technology - low lease tolerance
		return 0.08 // 8% threshold
	case "31", "32", "33": // Manufacturing - moderate tolerance
		return 0.12 // 12% threshold
	case "52": // Financial Services - minimal leases expected
		return 0.05 // 5% threshold
	default:
		return 0.10 // 10% default threshold
	}
}

func (la *LiabilityAdjuster) getPensionThresholdForIndustry(industryCode string) float64 {
	switch industryCode {
	case "22": // Utilities - typically high pension exposure
		return 0.08 // 8% of revenue threshold
	case "31", "32", "33": // Manufacturing - moderate pension exposure
		return 0.05 // 5% of revenue threshold
	case "45": // Technology - typically minimal pension exposure
		return 0.02 // 2% of revenue threshold
	default:
		return 0.03 // 3% default threshold
	}
}

func (la *LiabilityAdjuster) getContingentLiabilityThreshold(industryCode string) float64 {
	switch industryCode {
	case "21": // Energy - high environmental/regulatory exposure
		return 0.03 // 3% of revenue threshold
	case "62": // Healthcare - high litigation exposure
		return 0.03 // 3% of revenue threshold
	case "45": // Technology - patent litigation exposure
		return 0.02 // 2% of revenue threshold
	default:
		return 0.01 // 1% default threshold
	}
}

func (la *LiabilityAdjuster) getSeverityForLeaseRatio(ratio float64, industryCode string) entities.FlagSeverity {
	threshold := la.getLeaseThresholdForIndustry(industryCode)

	if ratio >= threshold*2.0 {
		return entities.FlagSeverityHigh
	} else if ratio >= threshold*1.5 {
		return entities.FlagSeverityMedium
	}
	return entities.FlagSeverityLow
}

func (la *LiabilityAdjuster) getSeverityForPensionRatio(ratio float64, industryCode string) entities.FlagSeverity {
	if ratio >= 0.15 { // 15% of revenue is critical
		return entities.FlagSeverityCritical
	} else if ratio >= 0.08 { // 8% is high
		return entities.FlagSeverityHigh
	} else if ratio >= 0.05 { // 5% is medium
		return entities.FlagSeverityMedium
	}
	return entities.FlagSeverityLow
}

func (la *LiabilityAdjuster) getSeverityForContingentRatio(ratio float64, industryCode string) entities.FlagSeverity {
	threshold := la.getContingentLiabilityThreshold(industryCode)

	if ratio >= threshold*3.0 {
		return entities.FlagSeverityCritical
	} else if ratio >= threshold*2.0 {
		return entities.FlagSeverityHigh
	} else if ratio >= threshold*1.5 {
		return entities.FlagSeverityMedium
	}
	return entities.FlagSeverityLow
}

func (la *LiabilityAdjuster) getContingentLiabilityProbability(industryCode string, amount float64) float64 {
	// Conservative probability estimates for disclosed contingent liabilities
	// TODO: Replace with AI-powered footnote analysis for more precise estimates

	switch industryCode {
	case "21": // Energy - environmental liabilities often materialize
		return 0.60 // 60% probability
	case "62": // Healthcare - litigation often settled
		return 0.50 // 50% probability
	case "45": // Technology - patent disputes often settled
		return 0.40 // 40% probability
	default:
		return 0.30 // 30% conservative default
	}
}

func (la *LiabilityAdjuster) getLeaseRecommendation(industryCode string, ratio float64) string {
	switch industryCode {
	case "44": // Retail
		return "Monitor lease obligation trends and renewal terms, especially for store locations"
	case "45": // Technology
		return "Evaluate lease commitments against asset utilization and growth projections"
	case "31", "32", "33": // Manufacturing
		return "Assess equipment lease obligations and potential purchase options"
	default:
		return "Review lease portfolio for optimization opportunities and renewal risks"
	}
}

func (la *LiabilityAdjuster) getContingentLiabilityRecommendation(industryCode string) string {
	switch industryCode {
	case "21": // Energy
		return "Monitor environmental remediation progress and regulatory developments"
	case "62": // Healthcare
		return "Track litigation settlement patterns and establish appropriate reserves"
	case "45": // Technology
		return "Assess patent portfolio risks and consider defensive strategies"
	default:
		return "Regularly evaluate contingent liability exposure and disclosure adequacy"
	}
}
