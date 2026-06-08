package flagging

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// FlaggingSystem orchestrates automated risk flagging and quality assessment
type FlaggingSystem struct {
	riskAnalyzer     *RiskAnalyzer
	industryAnalyzer *IndustryAnalyzer
}

// NewFlaggingSystem creates a new flagging system with all analyzers
func NewFlaggingSystem() *FlaggingSystem {
	return &FlaggingSystem{
		riskAnalyzer:     NewRiskAnalyzer(),
		industryAnalyzer: NewIndustryAnalyzer(),
	}
}

// QualityResult represents the overall quality assessment result
type QualityResult struct {
	QualityScore  float64  `json:"quality_score"`  // 0-100 scale
	QualityGrade  string   `json:"quality_grade"`  // A, B, C, D, F
	QualityIssues []string `json:"quality_issues"` // List of issues found
}

// Recommendation represents an automated recommendation for data cleaning
type Recommendation struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Priority    string    `json:"priority"`    // High, Medium, Low
	Description string    `json:"description"` // What the issue is
	Action      string    `json:"action"`      // What should be done
	Impact      string    `json:"impact"`      // Expected impact of fix
	Timestamp   time.Time `json:"timestamp"`
}

// CalculateQualityScore computes an overall quality score (0-100) based on flags and financial data
func (fs *FlaggingSystem) CalculateQualityScore(data *entities.FinancialData, flags []entities.Flag) *QualityResult {
	if data == nil {
		return &QualityResult{
			QualityScore:  0.0,
			QualityGrade:  "F",
			QualityIssues: []string{"No financial data available"},
		}
	}

	// Start with perfect score and deduct for issues
	score := 100.0
	var issues []string

	// Analyze flags and deduct points based on severity
	for _, flag := range flags {
		deduction := fs.calculateFlagPenalty(flag)
		score -= deduction

		issue := fs.createIssueDescription(flag)
		if issue != "" {
			issues = append(issues, issue)
		}
	}

	// Additional deductions for structural issues
	structuralPenalty, structuralIssues := fs.assessStructuralQuality(data)
	score -= structuralPenalty
	issues = append(issues, structuralIssues...)

	// Ensure score is between 0 and 100
	score = math.Max(0.0, math.Min(100.0, score))

	return &QualityResult{
		QualityScore:  score,
		QualityGrade:  fs.calculateGrade(score),
		QualityIssues: issues,
	}
}

// AnalyzeRisks performs comprehensive risk analysis on financial data
func (fs *FlaggingSystem) AnalyzeRisks(data *entities.FinancialData, context *entities.CleaningContext) []entities.Flag {
	if data == nil || context == nil {
		return []entities.Flag{}
	}

	var flags []entities.Flag

	// Get industry-specific thresholds
	thresholds := fs.industryAnalyzer.GetIndustryThresholds(context.IndustryCode)

	// Assess goodwill concentration risk
	if goodwillFlag := fs.riskAnalyzer.AssessGoodwillRisk(
		data.Goodwill,
		data.TotalAssets,
		thresholds["goodwill_threshold"],
	); goodwillFlag != nil {
		flags = append(flags, *goodwillFlag)
	}

	// Assess intangible asset risk
	if intangibleFlag := fs.riskAnalyzer.AssessIntangibleRisk(
		data.OtherIntangibles,
		data.TotalAssets,
		thresholds["intangible_threshold"],
	); intangibleFlag != nil {
		flags = append(flags, *intangibleFlag)
	}

	// Assess inventory obsolescence risk
	if inventoryFlag := fs.riskAnalyzer.AssessInventoryRisk(
		data.Inventory,
		data.TotalAssets,
		thresholds["inventory_threshold"],
		data.InventoryTurnover,
		context.IndustryCode,
	); inventoryFlag != nil {
		flags = append(flags, *inventoryFlag)
	}

	// Assess leverage concerns
	if leverageFlag := fs.riskAnalyzer.AssessLeverageRisk(
		data.TotalDebt,
		data.TotalAssets,
		thresholds["leverage_threshold"],
	); leverageFlag != nil {
		flags = append(flags, *leverageFlag)
	}

	// TODO: Add more risk assessments
	// - Operating lease obligations
	// - Pension underfunding
	// - Working capital quality
	// - Earnings quality

	return flags
}

// GenerateRecommendations creates actionable recommendations based on identified flags
func (fs *FlaggingSystem) GenerateRecommendations(flags []entities.Flag, data *entities.FinancialData) []Recommendation {
	var recommendations []Recommendation

	// Group flags by category for better recommendations
	flagsByCategory := fs.groupFlagsByCategory(flags)

	// Generate category-specific recommendations
	for category, categoryFlags := range flagsByCategory {
		switch category {
		case entities.AssetQuality:
			recommendations = append(recommendations, fs.generateAssetQualityRecommendations(categoryFlags, data)...)
		case entities.LiabilityCompleteness:
			recommendations = append(recommendations, fs.generateLiabilityRecommendations(categoryFlags, data)...)
		case entities.EarningsNormalization:
			recommendations = append(recommendations, fs.generateEarningsRecommendations(categoryFlags, data)...)
		}
	}

	// Sort recommendations by priority
	sort.Slice(recommendations, func(i, j int) bool {
		priorityOrder := map[string]int{"High": 3, "Medium": 2, "Low": 1}
		return priorityOrder[recommendations[i].Priority] > priorityOrder[recommendations[j].Priority]
	})

	return recommendations
}

// Helper methods

func (fs *FlaggingSystem) calculateFlagPenalty(flag entities.Flag) float64 {
	// Penalty based on severity and percentage impact
	basePenalty := map[entities.FlagSeverity]float64{
		entities.FlagSeverityLow:      5.0,  // 5 point deduction
		entities.FlagSeverityMedium:   15.0, // 15 point deduction
		entities.FlagSeverityHigh:     30.0, // 30 point deduction
		entities.FlagSeverityCritical: 50.0, // 50 point deduction
	}

	penalty := basePenalty[flag.Severity]

	// Amplify penalty for large percentage impacts
	if flag.Percentage > 0 {
		amplifier := math.Min(flag.Percentage/100.0, 1.0) // Cap at 100%
		penalty *= (1.0 + amplifier)
	}

	return penalty
}

func (fs *FlaggingSystem) createIssueDescription(flag entities.Flag) string {
	// Handle severity-based descriptions for goodwill concentration
	if flag.Type == "goodwill_concentration" {
		switch flag.Severity {
		case entities.FlagSeverityLow:
			return "Minor goodwill concentration"
		case entities.FlagSeverityMedium:
			return "Moderate goodwill concentration"
		case entities.FlagSeverityHigh, entities.FlagSeverityCritical:
			return "Excessive goodwill concentration"
		}
	}

	// Default descriptions for other flag types
	descriptions := map[string]string{
		"goodwill_concentration": "Excessive goodwill concentration", // Fallback
		"intangible_risk":        "Significant intangible assets",
		"intangibles":            "Significant intangible assets",
		"dead_inventory":         "Significant dead inventory detected",
		"inventory_obsolescence": "Inventory obsolescence risk",
		"leverage_concern":       "High leverage concern",
		"lease_liability":        "Off-balance sheet lease obligations",
		"restructuring":          "Recurring restructuring charges",
	}

	if desc, exists := descriptions[flag.Type]; exists {
		return desc
	}
	return flag.Description
}

func (fs *FlaggingSystem) assessStructuralQuality(data *entities.FinancialData) (float64, []string) {
	penalty := 0.0
	var issues []string

	// Check for missing critical data
	if data.TotalAssets <= 0 {
		penalty += 20.0
		issues = append(issues, "Missing or invalid total assets")
	}

	if data.Revenue <= 0 {
		penalty += 15.0
		issues = append(issues, "Missing or invalid revenue data")
	}

	// Check for unrealistic ratios
	if data.TotalAssets > 0 {
		goodwillRatio := data.Goodwill / data.TotalAssets
		if goodwillRatio > 0.8 { // More than 80% goodwill is unrealistic
			penalty += 25.0
			issues = append(issues, "Unrealistic goodwill to assets ratio")
		}

		debtRatio := data.TotalDebt / data.TotalAssets
		if debtRatio > 1.0 { // Debt exceeds assets
			penalty += 20.0
			issues = append(issues, "Debt exceeds total assets")
		}
	}

	return penalty, issues
}

func (fs *FlaggingSystem) calculateGrade(score float64) string {
	switch {
	case score > 80:
		return "A+"
	case score > 60:
		return "B+"
	case score > 40:
		return "C+"
	case score > 20:
		return "D+"
	default:
		return "F+"
	}
}

func (fs *FlaggingSystem) groupFlagsByCategory(flags []entities.Flag) map[entities.RuleCategory][]entities.Flag {
	groups := make(map[entities.RuleCategory][]entities.Flag)

	for _, flag := range flags {
		category := fs.getCategoryFromRuleID(flag.RuleID)
		groups[category] = append(groups[category], flag)
	}

	return groups
}

// getCategoryFromRuleID determines the rule category based on the rule ID
func (fs *FlaggingSystem) getCategoryFromRuleID(ruleID string) entities.RuleCategory {
	if len(ruleID) == 0 {
		return entities.AssetQuality // Default fallback
	}

	switch ruleID[0] {
	case 'A': // Asset quality rules (A1, A2, A3, etc.)
		return entities.AssetQuality
	case 'B': // Liability completeness rules (B1, B2, B3, etc.)
		return entities.LiabilityCompleteness
	case 'C': // Earnings normalization rules (C1, C2, C3, etc.)
		return entities.EarningsNormalization
	default: // Other rules (LEV1, etc.) - categorize as asset quality for now
		return entities.AssetQuality
	}
}

func (fs *FlaggingSystem) generateAssetQualityRecommendations(flags []entities.Flag, data *entities.FinancialData) []Recommendation {
	var recommendations []Recommendation

	for _, flag := range flags {
		switch flag.Type {
		case "goodwill_concentration":
			recommendations = append(recommendations, Recommendation{
				ID:          fmt.Sprintf("rec-goodwill-%d", time.Now().Unix()),
				Type:        "goodwill_adjustment",
				Priority:    "High",
				Description: "Excessive goodwill concentration detected",
				Action:      "Consider goodwill impairment testing and potential writedown",
				Impact:      "Reduces asset overstatement and improves balance sheet quality",
				Timestamp:   time.Now(),
			})

		case "intangibles":
			recommendations = append(recommendations, Recommendation{
				ID:          fmt.Sprintf("rec-intangible-%d", time.Now().Unix()),
				Type:        "intangible_adjustment",
				Priority:    "Medium",
				Description: "Significant intangible assets identified",
				Action:      "Implement conservative amortization policy and regular impairment testing",
				Impact:      "Provides more realistic asset valuation",
				Timestamp:   time.Now(),
			})

		case "dead_inventory", "inventory_obsolescence":
			recommendations = append(recommendations, Recommendation{
				ID:          fmt.Sprintf("rec-inventory-%d", time.Now().Unix()),
				Type:        "inventory_adjustment",
				Priority:    "High",
				Description: "Dead or obsolete inventory detected",
				Action:      "Implement inventory liquidation or writedown procedures",
				Impact:      "Improves inventory turnover and reduces carrying costs",
				Timestamp:   time.Now(),
			})
		}
	}

	return recommendations
}

func (fs *FlaggingSystem) generateLiabilityRecommendations(flags []entities.Flag, data *entities.FinancialData) []Recommendation {
	var recommendations []Recommendation

	for _, flag := range flags {
		switch flag.Type {
		case "lease_liability":
			recommendations = append(recommendations, Recommendation{
				ID:          fmt.Sprintf("rec-lease-%d", time.Now().Unix()),
				Type:        "lease_adjustment",
				Priority:    "Medium",
				Description: "Off-balance sheet lease obligations identified",
				Action:      "Capitalize operating leases and adjust debt ratios accordingly",
				Impact:      "Provides complete picture of financial obligations",
				Timestamp:   time.Now(),
			})
		}
	}

	return recommendations
}

func (fs *FlaggingSystem) generateEarningsRecommendations(flags []entities.Flag, data *entities.FinancialData) []Recommendation {
	var recommendations []Recommendation

	for _, flag := range flags {
		switch {
		case strings.Contains(flag.Type, "restructuring"):
			recommendations = append(recommendations, Recommendation{
				ID:          fmt.Sprintf("rec-restructuring-%d", time.Now().Unix()),
				Type:        "earnings_normalization",
				Priority:    "Medium",
				Description: "Recurring restructuring charges identified",
				Action:      "Normalize earnings by excluding one-time restructuring costs",
				Impact:      "Provides clearer view of core operating performance",
				Timestamp:   time.Now(),
			})
		}
	}

	return recommendations
}
