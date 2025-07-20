package adjustments

import (
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// EarningsAdjuster handles Category C adjustments from SEC cleaning guide
// Implements earnings distortion removal and normalization
type EarningsAdjuster struct {
	// TODO: Add configuration for adjustment thresholds
}

// NewEarningsAdjuster creates a new earnings adjuster instance
func NewEarningsAdjuster() *EarningsAdjuster {
	return &EarningsAdjuster{}
}

// ProcessEarningsAdjustments applies all Category C earnings normalization rules
func (ea *EarningsAdjuster) ProcessEarningsAdjustments(data *entities.FinancialData, rules []*entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	var allAdjustments []entities.Adjustment
	var allFlags []entities.Flag
	totalAmount := 0.0
	applied := false

	for _, rule := range rules {
		if !rule.Enabled || rule.Category != entities.EarningsNormalization {
			continue
		}

		var result *AdjustmentResult

		switch rule.ID {
		case "restructuring_charges":
			result = ea.ProcessRestructuringChargesAdjustment(data, rule)
		case "asset_sale_gains":
			result = ea.ProcessAssetSaleGainsAdjustment(data, rule)
		case "litigation_settlements":
			result = ea.ProcessLitigationSettlementsAdjustment(data, rule)
		case "stock_compensation":
			result = ea.ProcessStockCompensationAdjustment(data, rule)
		case "derivative_gains_losses":
			result = ea.ProcessDerivativeGainsLossesAdjustment(data, rule)
		case "capitalized_interest":
			result = ea.ProcessCapitalizedInterestAdjustment(data, rule)
		case "working_capital_window_dressing":
			result = ea.ProcessWorkingCapitalAdjustment(data, rule, context)
		default:
			// Skip unknown rules
			continue
		}

		if result != nil && result.Applied {
			allAdjustments = append(allAdjustments, result.Adjustments...)
			allFlags = append(allFlags, result.Flags...)
			totalAmount += result.Amount
			applied = true
		}
	}

	reasoning := fmt.Sprintf("Applied %d earnings normalization adjustments totaling $%.1fM",
		len(allAdjustments), totalAmount/1000000)

	return &AdjustmentResult{
		Amount:      totalAmount,
		Applied:     applied,
		Adjustments: allAdjustments,
		Flags:       allFlags,
		Reasoning:   reasoning,
	}
}

// ProcessRestructuringChargesAdjustment implements C1 rule: Remove recurring restructuring charges
func (ea *EarningsAdjuster) ProcessRestructuringChargesAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.Revenue <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "Insufficient revenue data to calculate restructuring charges",
		}
	}

	// Use actual restructuring charges if available, otherwise estimate
	restructuringAmount := data.RestructuringCharges
	if restructuringAmount <= 0 {
		// Estimate based on revenue (conservative approach)
		restructuringAmount = data.Revenue * 0.015 // Estimate 1.5% of revenue
	}

	// Check materiality threshold
	restructuringRatio := restructuringAmount / data.Revenue
	threshold := 0.02 // Default 2% threshold
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	if restructuringRatio < threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning: fmt.Sprintf("Restructuring charges below materiality threshold (%.1f%% < %.1f%%)",
				restructuringRatio*100, threshold*100),
		}
	}

	// Apply adjustment - add back to normalized operating income
	data.NormalizedOperatingIncome += restructuringAmount

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("restructuring_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Exclude,
		Amount:      restructuringAmount,
		FromAccount: "RestructuringCharges",
		ToAccount:   "NormalizedOperatingIncome",
		Percentage:  restructuringRatio * 100,
		Reasoning: fmt.Sprintf("Excluded restructuring charges of $%.1fM (%.1f%% of revenue)",
			restructuringAmount/1000000, restructuringRatio*100),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Restructuring charges adjustment: Excluded $%.1fM (%.1f%% of revenue) from normalized operating income",
		restructuringAmount/1000000, restructuringRatio*100)

	return &AdjustmentResult{
		Amount:      restructuringAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessAssetSaleGainsAdjustment implements C2 rule: Remove non-core asset sale gains
func (ea *EarningsAdjuster) ProcessAssetSaleGainsAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.AssetSaleGains <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No asset sale gains to adjust",
		}
	}

	// Remove asset sale gains from normalized operating income
	data.NormalizedOperatingIncome -= data.AssetSaleGains

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("asset_gains_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Exclude,
		Amount:      data.AssetSaleGains,
		FromAccount: "AssetSaleGains",
		ToAccount:   "NormalizedOperatingIncome",
		Percentage:  (data.AssetSaleGains / data.Revenue) * 100,
		Reasoning: fmt.Sprintf("Excluded asset sale gains of $%.1fM from operating income",
			data.AssetSaleGains/1000000),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Asset sale gains adjustment: Excluded $%.1fM from normalized operating income",
		data.AssetSaleGains/1000000)

	return &AdjustmentResult{
		Amount:      data.AssetSaleGains,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessLitigationSettlementsAdjustment implements C3 rule: Remove episodic litigation costs
func (ea *EarningsAdjuster) ProcessLitigationSettlementsAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.LitigationSettlements <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No litigation settlements to adjust",
		}
	}

	// Check materiality threshold
	litigationRatio := data.LitigationSettlements / data.Revenue
	threshold := 0.01 // Default 1% threshold
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	if litigationRatio < threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning: fmt.Sprintf("Litigation settlements below materiality threshold (%.1f%% < %.1f%%)",
				litigationRatio*100, threshold*100),
		}
	}

	// Add back litigation settlements to normalized operating income
	data.NormalizedOperatingIncome += data.LitigationSettlements

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("litigation_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Exclude,
		Amount:      data.LitigationSettlements,
		FromAccount: "LitigationSettlements",
		ToAccount:   "NormalizedOperatingIncome",
		Percentage:  litigationRatio * 100,
		Reasoning: fmt.Sprintf("Excluded litigation settlements of $%.1fM (%.1f%% of revenue)",
			data.LitigationSettlements/1000000, litigationRatio*100),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Litigation settlements adjustment: Excluded $%.1fM (%.1f%% of revenue) from normalized operating income",
		data.LitigationSettlements/1000000, litigationRatio*100)

	return &AdjustmentResult{
		Amount:      data.LitigationSettlements,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessStockCompensationAdjustment implements C4 rule: Handle stock-based compensation
func (ea *EarningsAdjuster) ProcessStockCompensationAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.StockBasedCompensation <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No stock-based compensation to adjust",
		}
	}

	// Stock-based compensation is reclassified, not excluded from operating income
	// It's treated as a real expense but flagged for dilution analysis
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("stock_comp_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Reclassify,
		Amount:      data.StockBasedCompensation,
		FromAccount: "StockBasedCompensation",
		ToAccount:   "OperatingExpenses",
		Percentage:  (data.StockBasedCompensation / data.Revenue) * 100,
		Reasoning: fmt.Sprintf("Reclassified stock-based compensation of $%.1fM for dilution analysis",
			data.StockBasedCompensation/1000000),
		Applied:   true,
		Timestamp: time.Now(),
	}

	// Create flag for dilution analysis
	flag := entities.Flag{
		ID:             fmt.Sprintf("stock_dilution_%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "earnings_quality",
		Severity:       rule.Severity,
		Amount:         data.StockBasedCompensation,
		Percentage:     (data.StockBasedCompensation / data.Revenue) * 100,
		Description:    "High stock-based compensation may indicate dilution risk",
		Recommendation: "Consider dilution impact in per-share calculations",
		Timestamp:      time.Now(),
	}

	reasoning := fmt.Sprintf("Stock-based compensation adjustment: Reclassified $%.1fM (%.1f%% of revenue) for dilution analysis",
		data.StockBasedCompensation/1000000, (data.StockBasedCompensation/data.Revenue)*100)

	return &AdjustmentResult{
		Amount:      data.StockBasedCompensation,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{flag},
		Reasoning:   reasoning,
	}
}

// ProcessDerivativeGainsLossesAdjustment implements C5 rule: Remove volatile derivative marks
func (ea *EarningsAdjuster) ProcessDerivativeGainsLossesAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.DerivativeGainsLosses == 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No derivative gains/losses to adjust",
		}
	}

	// Remove derivative gains/losses from normalized operating income
	adjustmentAmount := data.DerivativeGainsLosses
	if adjustmentAmount > 0 {
		// Gains - subtract from operating income
		data.NormalizedOperatingIncome -= adjustmentAmount
	} else {
		// Losses - add back to operating income (remove the negative impact)
		data.NormalizedOperatingIncome -= adjustmentAmount // This adds back since amount is negative
		adjustmentAmount = -adjustmentAmount               // Make positive for reporting
	}

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("derivative_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Exclude,
		Amount:      adjustmentAmount,
		FromAccount: "DerivativeGainsLosses",
		ToAccount:   "NormalizedOperatingIncome",
		Percentage:  (adjustmentAmount / data.Revenue) * 100,
		Reasoning: fmt.Sprintf("Excluded derivative gains/losses of $%.1fM from operating income",
			adjustmentAmount/1000000),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Derivative gains/losses adjustment: Excluded $%.1fM from normalized operating income",
		adjustmentAmount/1000000)

	return &AdjustmentResult{
		Amount:      adjustmentAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessCapitalizedInterestAdjustment implements C6 rule: Reclassify capitalized interest
func (ea *EarningsAdjuster) ProcessCapitalizedInterestAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.CapitalizedInterest <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No capitalized interest to adjust",
		}
	}

	// Add capitalized interest back to interest expense
	data.InterestExpense += data.CapitalizedInterest

	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("cap_interest_%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.EarningsNormalization,
		Type:        entities.Reclassify,
		Amount:      data.CapitalizedInterest,
		FromAccount: "CapitalizedInterest",
		ToAccount:   "InterestExpense",
		Percentage:  (data.CapitalizedInterest / data.Revenue) * 100,
		Reasoning: fmt.Sprintf("Reclassified capitalized interest of $%.1fM to interest expense",
			data.CapitalizedInterest/1000000),
		Applied:   true,
		Timestamp: time.Now(),
	}

	reasoning := fmt.Sprintf("Capitalized interest adjustment: Reclassified $%.1fM from PP&E to interest expense",
		data.CapitalizedInterest/1000000)

	return &AdjustmentResult{
		Amount:      data.CapitalizedInterest,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{},
		Reasoning:   reasoning,
	}
}

// ProcessWorkingCapitalAdjustment implements C7 rule: Flag working capital window dressing
func (ea *EarningsAdjuster) ProcessWorkingCapitalAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	if data.WorkingCapitalAdjustment == 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No working capital adjustments detected",
		}
	}

	// Check materiality threshold
	wcRatio := data.WorkingCapitalAdjustment / data.Revenue
	threshold := 0.15 // Default 15% threshold
	if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
		threshold = *rule.Threshold.PercentageOfRevenue
	}

	if wcRatio < threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning: fmt.Sprintf("Working capital adjustment below materiality threshold (%.1f%% < %.1f%%)",
				wcRatio*100, threshold*100),
		}
	}

	// Create flag for working capital window dressing (no adjustment to income)
	flag := entities.Flag{
		ID:             fmt.Sprintf("wc_dressing_%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "earnings_quality",
		Severity:       rule.Severity,
		Amount:         data.WorkingCapitalAdjustment,
		Percentage:     wcRatio * 100,
		Description:    "Unusual working capital movements may indicate window dressing",
		Recommendation: "Review quarter-end receivables and payables patterns",
		Timestamp:      time.Now(),
	}

	reasoning := fmt.Sprintf("Working capital window dressing: Flagged $%.1fM (%.1f%% of revenue) unusual movement",
		data.WorkingCapitalAdjustment/1000000, wcRatio*100)

	return &AdjustmentResult{
		Amount:      data.WorkingCapitalAdjustment,
		Applied:     true,
		Adjustments: []entities.Adjustment{}, // No income adjustments, just flagging
		Flags:       []entities.Flag{flag},
		Reasoning:   reasoning,
	}
}
