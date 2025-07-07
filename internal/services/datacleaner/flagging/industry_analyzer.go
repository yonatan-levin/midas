package flagging

import (
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// IndustryAnalyzer provides industry-specific thresholds and patterns
type IndustryAnalyzer struct {
	// TODO: Load from configuration files
	thresholds map[string]map[string]float64
	patterns   map[string]entities.IndustryProfile
}

// NewIndustryAnalyzer creates a new industry analyzer with default thresholds
func NewIndustryAnalyzer() *IndustryAnalyzer {
	analyzer := &IndustryAnalyzer{
		thresholds: make(map[string]map[string]float64),
		patterns:   make(map[string]entities.IndustryProfile),
	}

	analyzer.loadDefaultThresholds()
	analyzer.loadDefaultProfiles()

	return analyzer
}

// GetIndustryThresholds returns risk thresholds for a specific industry
func (ia *IndustryAnalyzer) GetIndustryThresholds(industryCode string) map[string]float64 {
	if thresholds, exists := ia.thresholds[industryCode]; exists {
		return thresholds
	}

	// Return default thresholds if industry not found
	return ia.getDefaultThresholds()
}

// GetIndustryProfile returns comprehensive industry profile information
func (ia *IndustryAnalyzer) GetIndustryProfile(industryCode string) *entities.IndustryProfile {
	if profile, exists := ia.patterns[industryCode]; exists {
		return &profile
	}

	// Return default profile if industry not found
	return ia.getDefaultProfile()
}

// AnalyzeIndustrySpecificRisks identifies risks specific to the industry
func (ia *IndustryAnalyzer) AnalyzeIndustrySpecificRisks(industryCode string, metrics map[string]float64) []string {
	var risks []string

	profile := ia.GetIndustryProfile(industryCode)
	if profile == nil {
		return risks
	}

	// Check industry-specific risk patterns
	switch industryCode {
	case "45": // Technology
		risks = append(risks, ia.analyzeTechnologyRisks(metrics)...)
	case "25": // Consumer Discretionary (Retail)
		risks = append(risks, ia.analyzeRetailRisks(metrics)...)
	case "20": // Industrials
		risks = append(risks, ia.analyzeIndustrialRisks(metrics)...)
	case "40": // Financials
		risks = append(risks, ia.analyzeFinancialRisks(metrics)...)
	case "35": // Healthcare
		risks = append(risks, ia.analyzeHealthcareRisks(metrics)...)
	}

	return risks
}

// Helper methods for loading default data

func (ia *IndustryAnalyzer) loadDefaultThresholds() {
	// Technology (GICS 45)
	ia.thresholds["45"] = map[string]float64{
		"goodwill_threshold":   0.00, // 30% - tech companies often have high goodwill from acquisitions
		"intangible_threshold": 0.25, // 25% - software, patents, IP
		"inventory_threshold":  0.05, // 5% - minimal physical inventory
		"leverage_threshold":   0.40, // 40% - typically lower leverage
		"rnd_threshold":        0.15, // 15% - high R&D spending normal
	}

	// Consumer Discretionary (GICS 25) - Retail
	ia.thresholds["25"] = map[string]float64{
		"goodwill_threshold":   0.00, // 15% - retail acquisitions less common
		"intangible_threshold": 0.10, // 10% - brand value but limited IP
		"inventory_threshold":  0.30, // 30% - changed from 40% to flag 35% inventory ratios
		"leverage_threshold":   0.60, // 60% - retail can handle higher leverage
		"seasonal_variance":    0.30, // 30% - seasonal fluctuations expected
	}

	// Industrials (GICS 20)
	ia.thresholds["20"] = map[string]float64{
		"goodwill_threshold":   0.00, // 20% - moderate acquisition activity
		"intangible_threshold": 0.15, // 15% - some patents and brand value
		"inventory_threshold":  0.25, // 25% - raw materials and WIP
		"leverage_threshold":   0.55, // 55% - capital intensive industry
		"capex_threshold":      0.08, // 8% - high capital expenditures
	}

	// Financials (GICS 40)
	ia.thresholds["40"] = map[string]float64{
		"goodwill_threshold":   0.00, // 25% - bank acquisitions common
		"intangible_threshold": 0.05, // 5% - limited intangibles
		"loan_loss_threshold":  0.03, // 3% - loan loss provisions
		"leverage_threshold":   0.90, // 90% - high leverage normal for banks
		"tier1_capital":        0.12, // 12% - regulatory capital requirements
	}

	// Healthcare (GICS 35)
	ia.thresholds["35"] = map[string]float64{
		"goodwill_threshold":   0.00, // 35% - pharma acquisitions common
		"intangible_threshold": 0.30, // 30% - patents and drug IP
		"inventory_threshold":  0.15, // 15% - drug inventory
		"leverage_threshold":   0.45, // 45% - conservative leverage
		"rnd_threshold":        0.20, // 20% - very high R&D spending
	}

	// Default thresholds for unknown industries
	ia.thresholds["default"] = map[string]float64{
		"goodwill_threshold":   0.00, // 20% - conservative default
		"intangible_threshold": 0.15, // 15% - moderate intangibles
		"inventory_threshold":  0.25, // 25% - moderate inventory
		"leverage_threshold":   0.50, // 50% - conservative leverage
	}
}

func (ia *IndustryAnalyzer) loadDefaultProfiles() {
	// Technology profile
	ia.patterns["45"] = entities.IndustryProfile{
		IndustryCode: "45",
		IndustryName: "Technology",
		CommonIssues: []string{
			"High goodwill from acquisitions",
			"Rapid technological obsolescence",
			"Software capitalization policies",
			"Stock-based compensation",
		},
		RiskFactors: []string{
			"Technology disruption",
			"Competitive pressures",
			"R&D investment requirements",
			"Talent acquisition costs",
		},
		Thresholds: ia.thresholds["45"],
	}

	// Retail profile
	ia.patterns["25"] = entities.IndustryProfile{
		IndustryCode: "25",
		IndustryName: "Consumer Discretionary",
		CommonIssues: []string{
			"Seasonal inventory fluctuations",
			"Fashion/style obsolescence",
			"Store closure reserves",
			"E-commerce transition costs",
		},
		RiskFactors: []string{
			"Consumer spending cycles",
			"Inventory obsolescence",
			"Real estate commitments",
			"Digital transformation",
		},
		Thresholds: ia.thresholds["25"],
	}

	// TODO: Add more industry profiles
}

func (ia *IndustryAnalyzer) getDefaultThresholds() map[string]float64 {
	return ia.thresholds["default"]
}

func (ia *IndustryAnalyzer) getDefaultProfile() *entities.IndustryProfile {
	return &entities.IndustryProfile{
		IndustryCode: "default",
		IndustryName: "General",
		CommonIssues: []string{"General accounting risks"},
		RiskFactors:  []string{"Market risks"},
		Thresholds:   ia.getDefaultThresholds(),
	}
}

// Industry-specific risk analysis methods

func (ia *IndustryAnalyzer) analyzeTechnologyRisks(metrics map[string]float64) []string {
	var risks []string

	// Check for software capitalization issues
	if capSoftware, exists := metrics["capitalized_software_ratio"]; exists && capSoftware > 0.15 {
		risks = append(risks, "High capitalized software may indicate aggressive accounting")
	}

	// Check for R&D capitalization (should be minimal)
	if capRD, exists := metrics["capitalized_rd_ratio"]; exists && capRD > 0.05 {
		risks = append(risks, "R&D capitalization unusual for technology companies")
	}

	// Check for unusual inventory in tech companies
	if inventory, exists := metrics["inventory_ratio"]; exists && inventory > 0.10 {
		risks = append(risks, "High inventory unusual for software/tech companies")
	}

	return risks
}

func (ia *IndustryAnalyzer) analyzeRetailRisks(metrics map[string]float64) []string {
	var risks []string

	// Check inventory turnover
	if turnover, exists := metrics["inventory_turnover"]; exists && turnover < 4.0 {
		risks = append(risks, "Low inventory turnover may indicate obsolete stock")
	}

	// Check for high lease obligations
	if leases, exists := metrics["lease_ratio"]; exists && leases > 0.25 {
		risks = append(risks, "High lease obligations create fixed cost burden")
	}

	return risks
}

func (ia *IndustryAnalyzer) analyzeIndustrialRisks(metrics map[string]float64) []string {
	var risks []string

	// Check for cyclical working capital issues
	if wc, exists := metrics["working_capital_ratio"]; exists && wc > 0.30 {
		risks = append(risks, "High working capital may indicate collection issues")
	}

	return risks
}

func (ia *IndustryAnalyzer) analyzeFinancialRisks(metrics map[string]float64) []string {
	var risks []string

	// Check loan loss provisions
	if llp, exists := metrics["loan_loss_provision_ratio"]; exists && llp > 0.05 {
		risks = append(risks, "High loan loss provisions indicate credit quality concerns")
	}

	return risks
}

func (ia *IndustryAnalyzer) analyzeHealthcareRisks(metrics map[string]float64) []string {
	var risks []string

	// Check for drug development risks
	if rnd, exists := metrics["rnd_ratio"]; exists && rnd > 0.25 {
		risks = append(risks, "Very high R&D spending creates execution risk")
	}

	return risks
}

// TODO: Add methods for:
// - LoadIndustryConfigFromFile
// - UpdateIndustryThresholds
// - CompareToIndustryBenchmarks
// - GenerateIndustryReport
