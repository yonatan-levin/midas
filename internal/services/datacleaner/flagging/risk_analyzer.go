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

// Inventory turnover thresholds used to refine the concentration-only
// obsolescence severity. NOTE: the codebase computes InventoryTurnover as
// Revenue / Inventory (sec/parser.go:958) — NOT the textbook COGS / Inventory
// (the Revenue-based ratio runs higher by the gross-margin factor). These
// bounds are deliberately industry-agnostic, conservative, and aligned with the
// existing Revenue/Inventory thresholds elsewhere in the tree:
//   - lowInventoryTurnoverThreshold mirrors constants.InventoryTurnoverThreshold
//     (2.0×/yr — the established "slow-moving / write-down-risk" signal); below
//     it we ESCALATE the concentration severity one notch.
//   - At/above healthyInventoryTurnoverThreshold (4.0× — between A5's <3.0 and the
//     <6.0 applicability gate) inventory is fast-moving and rarely goes obsolete
//     even at a large share of assets, so we DE-ESCALATE one notch.
//
// Between the two bounds, or when turnover is unreported (0), severity is the
// concentration-only result — preserving pre-TDB-8 behavior for the many filers
// that do not report a usable turnover figure.
//
// FOLLOW-UP (TDB-8 REVIEWER NITs, deferred): make these cutoffs industry-aware
// via GetIndustryThresholds (a heavy-equipment filer structurally ~2× is
// over-escalated today); add helper cap/floor + exact-boundary tests; note the
// FY-vs-Q turnover asymmetry (an FY snapshot runs ~4× a single quarter's).
const (
	// Keep in lock-step with constants.InventoryTurnoverThreshold (2.0).
	lowInventoryTurnoverThreshold     = 2.0
	healthyInventoryTurnoverThreshold = 4.0
)

// AssessInventoryRisk evaluates inventory levels and potential obsolescence.
// inventoryTurnover is annual inventory turns (Revenue / inventory, per
// sec/parser.go:958); pass 0 when it is not reported, which leaves the
// assessment on the concentration-only path.
func (ra *RiskAnalyzer) AssessInventoryRisk(inventory, totalAssets, industryThreshold, inventoryTurnover float64, industryCode string) *entities.Flag {
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

	// Refine the concentration-only severity with the turnover signal. Only act
	// when turnover is actually reported (> 0); turnover == 0 means "unknown",
	// in which case we keep the concentration-only result unchanged.
	description := fmt.Sprintf("Inventory concentration: %.1f%% of total assets", ratio*100)
	recommendation := "Analyze inventory turnover and consider writedowns for obsolete stock"

	if inventoryTurnover > 0 {
		switch {
		case inventoryTurnover < lowInventoryTurnoverThreshold:
			// Slow-moving inventory compounds the concentration risk.
			severity = escalateSeverity(severity)
			description = fmt.Sprintf(
				"Inventory concentration: %.1f%% of total assets with low turnover (%.1f turns/yr) indicating slow-moving / potentially obsolete stock",
				ratio*100, inventoryTurnover,
			)
			recommendation = "Slow inventory turnover signals obsolescence risk; review aging and book write-downs for slow-moving stock"
		case inventoryTurnover >= healthyInventoryTurnoverThreshold:
			// Fast-moving inventory rarely goes obsolete despite concentration.
			severity = deescalateSeverity(severity)
		}
	}

	return &entities.Flag{
		ID:             fmt.Sprintf("inventory-risk-%d", time.Now().UnixNano()),
		RuleID:         "A5", // References inventory rule in SEC guide
		Type:           "inventory_obsolescence",
		Severity:       severity,
		Amount:         inventory,
		Percentage:     ratio * 100,
		Description:    description,
		Recommendation: recommendation,
		Industry:       industryCode,
		Threshold:      industryThreshold * 100,
		Timestamp:      time.Now(),
	}
}

// escalateSeverity bumps a flag one severity level higher, capped at Critical.
func escalateSeverity(s entities.FlagSeverity) entities.FlagSeverity {
	switch s {
	case entities.FlagSeverityLow:
		return entities.FlagSeverityMedium
	case entities.FlagSeverityMedium:
		return entities.FlagSeverityHigh
	case entities.FlagSeverityHigh:
		return entities.FlagSeverityCritical
	default:
		return s // already Critical (or unknown) — no further escalation
	}
}

// deescalateSeverity lowers a flag one severity level, floored at Low.
func deescalateSeverity(s entities.FlagSeverity) entities.FlagSeverity {
	switch s {
	case entities.FlagSeverityCritical:
		return entities.FlagSeverityHigh
	case entities.FlagSeverityHigh:
		return entities.FlagSeverityMedium
	case entities.FlagSeverityMedium:
		return entities.FlagSeverityLow
	default:
		return s // already Low (or unknown) — no further de-escalation
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
