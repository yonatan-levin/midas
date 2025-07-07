package flagging

import (
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// RiskAnalyzer performs specific financial risk assessments
type RiskAnalyzer struct {
	// TODO: Add configuration for risk thresholds
}

// NewRiskAnalyzer creates a new risk analyzer
func NewRiskAnalyzer() *RiskAnalyzer {
	return &RiskAnalyzer{}
}

// AssessGoodwillRisk evaluates goodwill concentration relative to total assets
func (ra *RiskAnalyzer) AssessGoodwillRisk(goodwill, totalAssets, industryThreshold float64) *entities.Flag {
	if totalAssets <= 0 || goodwill <= 0 {
		return nil // No risk if no goodwill or invalid data
	}

	ratio := goodwill / totalAssets

	// No flag if below industry threshold
	if ratio <= industryThreshold {
		return nil
	}

	// Determine severity based on how much it exceeds threshold
	var severity entities.FlagSeverity
	var description string

	excessRatio := ratio / industryThreshold
	switch {
	case excessRatio >= 2.5: // 2.5x industry threshold
		severity = entities.FlagSeverityCritical
		description = fmt.Sprintf("Critical goodwill concentration (%.1f%% vs %.1f%% industry threshold)", ratio*100, industryThreshold*100)
	case excessRatio >= 2.0: // 2x industry threshold
		severity = entities.FlagSeverityHigh
		description = fmt.Sprintf("High goodwill concentration (%.1f%% vs %.1f%% industry threshold)", ratio*100, industryThreshold*100)
	case excessRatio >= 1.25: // 1.5x industry threshold
		severity = entities.FlagSeverityMedium
		description = fmt.Sprintf("Moderate goodwill concentration (%.1f%% vs %.1f%% industry threshold)", ratio*100, industryThreshold*100)
	default:
		severity = entities.FlagSeverityLow
		description = fmt.Sprintf("Goodwill slightly above industry average (%.1f%% vs %.1f%% threshold)", ratio*100, industryThreshold*100)
	}

	return &entities.Flag{
		ID:             fmt.Sprintf("goodwill-risk-%d", time.Now().UnixNano()),
		RuleID:         "A1", // References goodwill rule in SEC guide
		Type:           "goodwill_concentration",
		Severity:       severity,
		Amount:         goodwill,
		Percentage:     ratio * 100,
		Description:    description,
		Recommendation: ra.getGoodwillRecommendation(severity),
		Timestamp:      time.Now(),
	}
}

// AssessIntangibleRisk evaluates intangible asset concentration
func (ra *RiskAnalyzer) AssessIntangibleRisk(intangibles, totalAssets, industryThreshold float64) *entities.Flag {
	if totalAssets <= 0 || intangibles <= 0 {
		return nil
	}

	ratio := intangibles / totalAssets

	if ratio <= industryThreshold {
		return nil
	}

	var severity entities.FlagSeverity
	excessRatio := ratio / industryThreshold

	switch {
	case excessRatio >= 2.0:
		severity = entities.FlagSeverityHigh
	case excessRatio >= 1.5:
		severity = entities.FlagSeverityMedium
	default:
		severity = entities.FlagSeverityLow
	}

	return &entities.Flag{
		ID:             fmt.Sprintf("intangible-risk-%d", time.Now().UnixNano()),
		RuleID:         "A2", // References intangible rule in SEC guide
		Type:           "intangible_risk",
		Severity:       severity,
		Amount:         intangibles,
		Percentage:     ratio * 100,
		Description:    fmt.Sprintf("Intangible assets concentration: %.1f%% of total assets", ratio*100),
		Recommendation: "Consider conservative amortization and regular impairment testing",
		Timestamp:      time.Now(),
	}
}

// AssessInventoryRisk evaluates inventory levels and potential obsolescence
func (ra *RiskAnalyzer) AssessInventoryRisk(inventory, totalAssets, industryThreshold float64, industryCode string) *entities.Flag {
	if totalAssets <= 0 || inventory <= 0 {
		return nil
	}

	ratio := inventory / totalAssets

	// Use industryThreshold directly - single source of truth from IndustryAnalyzer
	if ratio <= industryThreshold {
		return nil
	}

	var severity entities.FlagSeverity
	excessRatio := ratio / industryThreshold

	switch {
	case excessRatio >= 2.0:
		severity = entities.FlagSeverityHigh
	case excessRatio >= 1.5:
		severity = entities.FlagSeverityMedium
	default:
		severity = entities.FlagSeverityLow
	}

	// TODO: Add inventory turnover analysis for better obsolescence detection
	return &entities.Flag{
		ID:             fmt.Sprintf("inventory-risk-%d", time.Now().UnixNano()),
		RuleID:         "A5", // References inventory rule in SEC guide
		Type:           "inventory_obsolescence",
		Severity:       severity,
		Amount:         inventory,
		Percentage:     ratio * 100,
		Description:    fmt.Sprintf("Inventory concentration: %.1f%% of total assets", ratio*100),
		Recommendation: "Analyze inventory turnover and consider writedowns for obsolete stock",
		Industry:       industryCode,
		Threshold:      industryThreshold * 100,
		Timestamp:      time.Now(),
	}
}

// AssessLeverageRisk evaluates debt levels relative to assets
func (ra *RiskAnalyzer) AssessLeverageRisk(totalDebt, totalAssets, industryThreshold float64) *entities.Flag {
	if totalAssets <= 0 {
		return nil
	}

	ratio := totalDebt / totalAssets

	// Use default threshold if not provided
	if industryThreshold <= 0 {
		industryThreshold = 0.6 // 60% default threshold
	}

	if ratio <= industryThreshold {
		return nil
	}

	var severity entities.FlagSeverity
	excessRatio := ratio / industryThreshold

	switch {
	case ratio >= 0.9: // 90%+ debt ratio is critical regardless of industry
		severity = entities.FlagSeverityCritical
	case excessRatio >= 1.5:
		severity = entities.FlagSeverityHigh
	case excessRatio >= 1.2:
		severity = entities.FlagSeverityMedium
	default:
		severity = entities.FlagSeverityLow
	}

	return &entities.Flag{
		ID:             fmt.Sprintf("leverage-risk-%d", time.Now().UnixNano()),
		RuleID:         "LEV1", // General leverage rule
		Type:           "leverage_concern",
		Severity:       severity,
		Amount:         totalDebt,
		Percentage:     ratio * 100,
		Description:    fmt.Sprintf("High leverage ratio: %.1f%% debt-to-assets", ratio*100),
		Recommendation: "Consider debt reduction or asset growth strategies",
		Threshold:      industryThreshold * 100,
		Timestamp:      time.Now(),
	}
}

// Helper methods

func (ra *RiskAnalyzer) getGoodwillRecommendation(severity entities.FlagSeverity) string {
	switch severity {
	case entities.FlagSeverityCritical:
		return "Immediate goodwill impairment testing required; consider significant writedown"
	case entities.FlagSeverityHigh:
		return "Perform detailed goodwill impairment analysis; likely writedown needed"
	case entities.FlagSeverityMedium:
		return "Enhanced goodwill monitoring recommended; consider impairment triggers"
	default:
		return "Monitor goodwill levels; ensure appropriate acquisition integration"
	}
}

// TODO: Add more risk assessment methods:
// - AssessWorkingCapitalQuality
// - AssessEarningsQuality
// - AssessCashFlowQuality
// - AssessPensionRisk
// - AssessLeaseRisk
// - AssessContingentLiabilityRisk
