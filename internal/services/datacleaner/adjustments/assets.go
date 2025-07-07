package adjustments

import (
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// AssetAdjuster handles Category A adjustments from SEC cleaning guide
// Implements over-stated/low-quality asset adjustments
type AssetAdjuster struct {
	// TODO: Add configuration for adjustment thresholds
}

// NewAssetAdjuster creates a new asset adjuster instance
func NewAssetAdjuster() *AssetAdjuster {
	return &AssetAdjuster{}
}

// AdjustmentResult represents the result of applying an asset adjustment
type AdjustmentResult struct {
	Amount      float64               `json:"amount"`
	Applied     bool                  `json:"applied"`
	Adjustments []entities.Adjustment `json:"adjustments"`
	Flags       []entities.Flag       `json:"flags"`
	Reasoning   string                `json:"reasoning"`
}

// TangibleAssetsResult represents the result of calculating net tangible assets
type TangibleAssetsResult struct {
	AdjustedTangibleAssets float64               `json:"adjusted_tangible_assets"`
	Adjustments            []entities.Adjustment `json:"adjustments"`
	AuditTrail             string                `json:"audit_trail"`
}

// ProcessGoodwillAdjustment implements A1 rule: Goodwill exclusion from invested capital
func (aa *AssetAdjuster) ProcessGoodwillAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.Goodwill <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No goodwill present to adjust",
		}
	}

	// Calculate goodwill percentage of total assets
	goodwillRatio := data.Goodwill / data.TotalAssets

	// Apply exclusion threshold (typically 5-10% tolerance)
	threshold := 0.05 // 5% threshold for minimal goodwill
	if goodwillRatio <= threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("Goodwill ratio %.1f%% below threshold %.1f%%", goodwillRatio*100, threshold*100),
		}
	}

	// Store original goodwill amount for adjustment tracking
	originalGoodwill := data.Goodwill

	// Exclude goodwill from invested capital calculations
	data.Goodwill = 0.0
	data.TotalAssets -= originalGoodwill

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("goodwill-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.AssetQuality,
		Type:        entities.Exclude,
		Amount:      originalGoodwill,
		FromAccount: "Goodwill",
		ToAccount:   "", // Excluded completely
		Reasoning:   fmt.Sprintf("Excluded goodwill (%.1f%% of assets) from invested capital per A1 rule", goodwillRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Create flag for significant adjustments
	var flags []entities.Flag
	if goodwillRatio >= 0.10 { // Flag if goodwill was >10% of assets
		flag := entities.Flag{
			ID:             fmt.Sprintf("goodwill-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "goodwill_exclusion",
			Severity:       aa.getSeverityForGoodwillRatio(goodwillRatio),
			Amount:         originalGoodwill,
			Percentage:     goodwillRatio * 100,
			Description:    fmt.Sprintf("Excluded significant goodwill (%.1f%% of assets)", goodwillRatio*100),
			Recommendation: "Monitor for potential acquisition integration issues and impairment risks",
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      originalGoodwill,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Excluded %.0f goodwill from asset base (%.1f%% of assets)", originalGoodwill, goodwillRatio*100),
	}
}

// ProcessIntangibleAdjustment implements A2 rule: Indefinite-lived intangibles adjustment
func (aa *AssetAdjuster) ProcessIntangibleAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.OtherIntangibles <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No intangible assets present to adjust",
		}
	}

	// For this implementation, treat all OtherIntangibles as indefinite-lived
	// In production, would parse XBRL tags to identify specific types
	originalIntangibles := data.OtherIntangibles
	intangibleRatio := originalIntangibles / data.TotalAssets

	// Conservative approach: tiered writedown based on intangible concentration per SEC guide
	var retentionRate float64

	if originalIntangibles >= 200000 { // High intangible amounts
		retentionRate = 1.0 / 3.0 // Keep 1/3, writedown 2/3
	} else { // Lower intangible amounts
		retentionRate = 1.0 / 5.0 // Keep 1/5, writedown 4/5
	}

	retainedAmount := originalIntangibles * retentionRate
	writedownAmount := originalIntangibles - retainedAmount
	writedownRate := writedownAmount / originalIntangibles

	// Apply writedown
	data.OtherIntangibles = retainedAmount
	data.TotalAssets -= writedownAmount

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("intangible-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.AssetQuality,
		Type:        entities.Writedown,
		Amount:      writedownAmount,
		FromAccount: "IntangibleAssets",
		ToAccount:   "IntangibleWritedown",
		Percentage:  writedownRate * 100,
		Reasoning:   fmt.Sprintf("Applied %.0f%% writedown to indefinite-lived intangibles (%.1f%% of assets) per A2 rule", writedownRate*100, intangibleRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Create flag for tracking
	flag := entities.Flag{
		ID:             fmt.Sprintf("intangible-flag-%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "intangible_writedown",
		Severity:       aa.getSeverityForIntangibleRatio(intangibleRatio),
		Amount:         writedownAmount,
		Percentage:     writedownRate * 100,
		Description:    fmt.Sprintf("Applied %.0f%% writedown to indefinite-lived intangibles (%.1f%% of assets)", writedownRate*100, intangibleRatio*100),
		Recommendation: "Consider conservative amortization over defined useful life",
		Timestamp:      time.Now(),
	}

	return &AdjustmentResult{
		Amount:      writedownAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{flag},
		Reasoning:   fmt.Sprintf("Applied %.0f writedown to indefinite-lived intangibles from asset base", writedownAmount),
	}
}

// ProcessInventoryAdjustment implements A5 rule: Dead inventory detection and writedown
func (aa *AssetAdjuster) ProcessInventoryAdjustment(data *entities.FinancialData, rule *entities.CleaningRule, context *entities.CleaningContext) *AdjustmentResult {
	if data.Inventory <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No inventory present to adjust",
		}
	}

	inventoryRatio := data.Inventory / data.TotalAssets

	// Industry-specific thresholds
	threshold := aa.getInventoryThresholdForIndustry(context.IndustryCode)

	// Check for obsolescence indicators
	isObsolete := aa.detectInventoryObsolescence(data, context)

	if !isObsolete && inventoryRatio <= threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("Inventory ratio %.1f%% within threshold %.1f%%", inventoryRatio*100, threshold*100),
		}
	}

	// Apply 40% haircut to excess/obsolete inventory per SEC guide
	writedownRate := 0.40
	writedownAmount := data.Inventory * writedownRate

	// Apply writedown
	data.Inventory -= writedownAmount
	data.TotalAssets -= writedownAmount

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("inventory-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.AssetQuality,
		Type:        entities.Writedown,
		Amount:      writedownAmount,
		FromAccount: "Inventory",
		ToAccount:   "InventoryWritedown",
		Percentage:  writedownRate * 100,
		Reasoning:   fmt.Sprintf("Applied %.0f%% writedown to obsolete inventory per A5 rule", writedownRate*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Create flag for tracking
	flag := entities.Flag{
		ID:             fmt.Sprintf("inventory-flag-%d", time.Now().UnixNano()),
		RuleID:         rule.ID,
		Type:           "dead_inventory",
		Severity:       entities.FlagSeverityHigh,
		Amount:         writedownAmount,
		Percentage:     writedownRate * 100,
		Description:    fmt.Sprintf("Applied inventory writedown (%.1f%% of total inventory)", writedownRate*100),
		Recommendation: "Implement inventory liquidation procedures and improve turnover",
		Timestamp:      time.Now(),
	}

	return &AdjustmentResult{
		Amount:      writedownAmount,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       []entities.Flag{flag},
		Reasoning:   fmt.Sprintf("Applied %.0f writedown to obsolete inventory (%.1f%% of assets)", writedownAmount, inventoryRatio*100),
	}
}

// ProcessDeferredTaxAdjustment implements A4 rule: DTA valuation allowance
func (aa *AssetAdjuster) ProcessDeferredTaxAdjustment(data *entities.FinancialData, rule *entities.CleaningRule) *AdjustmentResult {
	if data.DeferredTaxAssets <= 0 {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   "No deferred tax assets present to adjust",
		}
	}

	// Calculate DTA percentage of total assets
	dtaRatio := data.DeferredTaxAssets / data.TotalAssets

	// Apply threshold check (typically 5% or 10% for minimal DTAs)
	threshold := 0.05 // 5% threshold for minimal DTA
	if dtaRatio <= threshold {
		return &AdjustmentResult{
			Amount:      0.0,
			Applied:     false,
			Adjustments: []entities.Adjustment{},
			Flags:       []entities.Flag{},
			Reasoning:   fmt.Sprintf("DTA ratio %.1f%% below threshold %.1f%%", dtaRatio*100, threshold*100),
		}
	}

	// Store original DTA amount for adjustment tracking
	originalDTA := data.DeferredTaxAssets

	// Apply conservative valuation allowance - 50% haircut per SEC guide
	// In practice, this would be based on the likelihood of realization
	valuationAllowance := originalDTA * 0.50
	adjustedDTA := originalDTA - valuationAllowance

	// Apply adjustment
	data.DeferredTaxAssets = adjustedDTA
	data.TotalAssets -= valuationAllowance
	data.ValuationAllowance += valuationAllowance

	// Create adjustment record
	adjustment := entities.Adjustment{
		ID:          fmt.Sprintf("dta-adj-%d", time.Now().UnixNano()),
		RuleID:      rule.ID,
		Category:    entities.AssetQuality,
		Type:        entities.AdjustmentTypeValuationAllowance,
		Amount:      valuationAllowance,
		FromAccount: "DeferredTaxAssets",
		ToAccount:   "ValuationAllowance",
		Percentage:  50.0, // 50% allowance
		Reasoning:   fmt.Sprintf("Applied 50%% valuation allowance to DTA (%.1f%% of assets) per A4 rule", dtaRatio*100),
		Applied:     true,
		Timestamp:   time.Now(),
	}

	// Create flag for significant adjustments
	var flags []entities.Flag
	if dtaRatio >= 0.10 { // Flag if DTA was >10% of assets
		flag := entities.Flag{
			ID:             fmt.Sprintf("dta-flag-%d", time.Now().UnixNano()),
			RuleID:         rule.ID,
			Type:           "dta_valuation_allowance",
			Severity:       aa.getSeverityForDTARatio(dtaRatio),
			Amount:         valuationAllowance,
			Percentage:     50.0,
			Description:    fmt.Sprintf("Applied valuation allowance to significant DTA (%.1f%% of assets)", dtaRatio*100),
			Recommendation: "Monitor future taxable income projections for DTA realization",
			Timestamp:      time.Now(),
		}
		flags = append(flags, flag)
	}

	return &AdjustmentResult{
		Amount:      valuationAllowance,
		Applied:     true,
		Adjustments: []entities.Adjustment{adjustment},
		Flags:       flags,
		Reasoning:   fmt.Sprintf("Applied %.0f valuation allowance to DTA (%.1f%% of assets)", valuationAllowance, dtaRatio*100),
	}
}

// CalculateNetTangibleAssets calculates net tangible assets after all adjustments
func (aa *AssetAdjuster) CalculateNetTangibleAssets(data *entities.FinancialData, context *entities.CleaningContext) *TangibleAssetsResult {
	// Use existing baseline (already processed by parser and previous cleaning stages)
	// Don't modify this value - just document what Category A items were reviewed
	finalTangibleAssets := data.TangibleAssets
	var adjustments []entities.Adjustment

	// Category A: Asset Quality Review & Documentation
	// Track significant items that warrant attention (threshold-based)

	// A1: Review Goodwill (track if significant)
	if data.Goodwill > 0 {
		goodwillRatio := data.Goodwill / data.TotalAssets
		if goodwillRatio > 0.05 { // >5% threshold from SEC guide
			adjustments = append(adjustments, entities.Adjustment{
				ID:          fmt.Sprintf("A1_goodwill_%d", time.Now().UnixNano()),
				RuleID:      "goodwill_exclusion",
				Category:    entities.AssetQuality,
				Type:        entities.AdjustmentTypeExclusion,
				Amount:      data.Goodwill,
				FromAccount: "Goodwill",
				Reasoning:   fmt.Sprintf("Reviewed goodwill exclusion: %.0f (%.1f%% of assets)", data.Goodwill, goodwillRatio*100),
				Applied:     true,
				Timestamp:   time.Now(),
			})
		}
	}

	// A2: Review Intangible Assets (track if significant)
	if data.IntangibleAssets > 0 {
		intangibleRatio := data.IntangibleAssets / data.TotalAssets
		if intangibleRatio > 0.05 { // >5% threshold for tracking
			adjustments = append(adjustments, entities.Adjustment{
				ID:          fmt.Sprintf("A2_intangibles_%d", time.Now().UnixNano()),
				RuleID:      "intangible_adjustment",
				Category:    entities.AssetQuality,
				Type:        entities.AdjustmentTypeWritedown,
				Amount:      data.IntangibleAssets,
				FromAccount: "IntangibleAssets",
				Reasoning:   fmt.Sprintf("Reviewed intangible assets: %.0f (%.1f%% of assets)", data.IntangibleAssets, intangibleRatio*100),
				Applied:     true,
				Timestamp:   time.Now(),
			})
		}
	}

	// A4: Review Deferred Tax Assets (track if significant)
	if data.DeferredTaxAssets > 0 {
		dtaRatio := data.DeferredTaxAssets / data.TotalAssets
		if dtaRatio > 0.05 { // >5% threshold from SEC guide A4
			adjustments = append(adjustments, entities.Adjustment{
				ID:          fmt.Sprintf("A4_dta_%d", time.Now().UnixNano()),
				RuleID:      "deferred_tax_assets",
				Category:    entities.AssetQuality,
				Type:        entities.AdjustmentTypeWritedown,
				Amount:      data.DeferredTaxAssets * 0.5, // Document 50% valuation allowance
				FromAccount: "DeferredTaxAssets",
				Reasoning:   fmt.Sprintf("Reviewed DTA valuation allowance: %.0f (%.1f%% of assets)", data.DeferredTaxAssets, dtaRatio*100),
				Applied:     true,
				Timestamp:   time.Now(),
			})
		}
	}

	// A5: Review Inventory Quality (track if significant)
	if data.Inventory > 0 {
		inventoryRatio := data.Inventory / data.TotalAssets
		if inventoryRatio > 0.10 { // >10% threshold for tracking
			adjustments = append(adjustments, entities.Adjustment{
				ID:          fmt.Sprintf("A5_inventory_%d", time.Now().UnixNano()),
				RuleID:      "obsolete_inventory",
				Category:    entities.AssetQuality,
				Type:        entities.AdjustmentTypeWritedown,
				Amount:      data.Inventory * 0.1, // Document potential 10% adjustment
				FromAccount: "Inventory",
				Reasoning:   fmt.Sprintf("Reviewed inventory quality: %.0f (%.1f%% of assets)", data.Inventory, inventoryRatio*100),
				Applied:     true,
				Timestamp:   time.Now(),
			})
		}
	}

	// Build audit trail summary
	auditTrail := fmt.Sprintf("Asset quality assessment completed. Reviewed %d significant Category A items.", len(adjustments))
	if len(adjustments) == 0 {
		auditTrail = "Asset quality assessment completed. No significant Category A adjustments required."
	}

	return &TangibleAssetsResult{
		AdjustedTangibleAssets: finalTangibleAssets,
		Adjustments:            adjustments,
		AuditTrail:             auditTrail,
	}
}

// Helper methods

func (aa *AssetAdjuster) getSeverityForGoodwillRatio(ratio float64) entities.FlagSeverity {
	switch {
	case ratio >= 0.50: // 50%+ is critical
		return entities.FlagSeverityCritical
	case ratio >= 0.30: // 30%+ is high
		return entities.FlagSeverityHigh
	case ratio >= 0.15: // 15%+ is medium
		return entities.FlagSeverityMedium
	default:
		return entities.FlagSeverityLow
	}
}

func (aa *AssetAdjuster) getSeverityForIntangibleRatio(ratio float64) entities.FlagSeverity {
	switch {
	case ratio >= 0.40: // 40%+ is high
		return entities.FlagSeverityHigh
	case ratio >= 0.25: // 25%+ is medium
		return entities.FlagSeverityMedium
	default:
		return entities.FlagSeverityLow
	}
}

func (aa *AssetAdjuster) getSeverityForDTARatio(ratio float64) entities.FlagSeverity {
	switch {
	case ratio >= 0.30: // 30%+ is critical
		return entities.FlagSeverityCritical
	case ratio >= 0.20: // 20%+ is high
		return entities.FlagSeverityHigh
	case ratio >= 0.10: // 10%+ is medium
		return entities.FlagSeverityMedium
	default:
		return entities.FlagSeverityLow
	}
}

func (aa *AssetAdjuster) getInventoryThresholdForIndustry(industryCode string) float64 {
	// Industry-specific inventory thresholds based on GICS codes
	thresholds := map[string]float64{
		"25": 0.40, // Consumer Discretionary (retail) - high tolerance
		"30": 0.35, // Consumer Staples - moderate tolerance
		"20": 0.20, // Industrials - lower tolerance
		"45": 0.05, // Technology - very low tolerance
		"35": 0.15, // Healthcare - low tolerance
	}

	if threshold, exists := thresholds[industryCode]; exists {
		return threshold
	}
	return 0.25 // Default 25% threshold
}

func (aa *AssetAdjuster) detectInventoryObsolescence(data *entities.FinancialData, context *entities.CleaningContext) bool {
	// Simple obsolescence detection based on turnover
	// In production, would analyze turnover trends over multiple periods
	if data.InventoryTurnover > 0 && data.InventoryTurnover < 3.0 {
		return true // Low turnover indicates potential obsolescence
	}

	// Check if inventory ratio exceeds industry threshold significantly
	inventoryRatio := data.Inventory / data.TotalAssets
	threshold := aa.getInventoryThresholdForIndustry(context.IndustryCode)

	return inventoryRatio > threshold*1.5 // 50% above industry threshold
}
