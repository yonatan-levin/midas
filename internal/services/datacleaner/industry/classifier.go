package industry

import (
	"fmt"
	"strings"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// IndustryClassifier provides enhanced industry classification logic
// TODO: Replace hardcoded industry detection with dynamic classification system
type IndustryClassifier struct {
	sectorConfigs map[string]*SectorConfig
}

// SectorConfig defines industry-specific configuration and thresholds
type SectorConfig struct {
	SectorCode        string                  `json:"sector_code"`        // GICS sector code
	SectorName        string                  `json:"sector_name"`        // Human-readable name
	SubIndustries     []string                `json:"sub_industries"`     // List of sub-industry codes
	RiskProfile       RiskProfile             `json:"risk_profile"`       // Industry risk characteristics
	Thresholds        IndustryThresholds      `json:"thresholds"`         // Industry-specific thresholds
	CommonAdjustments []string                `json:"common_adjustments"` // Typical adjustments for this industry
	KeyMetrics        []string                `json:"key_metrics"`        // Important metrics to monitor
	Characteristics   IndustryCharacteristics `json:"characteristics"`    // Industry-specific traits
}

// RiskProfile defines risk characteristics for an industry
type RiskProfile struct {
	CyclicalityRisk    RiskLevel `json:"cyclicality_risk"`     // Economic cycle sensitivity
	RegulatoryRisk     RiskLevel `json:"regulatory_risk"`      // Regulatory change risk
	TechnologyRisk     RiskLevel `json:"technology_risk"`      // Technology disruption risk
	CompetitiveRisk    RiskLevel `json:"competitive_risk"`     // Competitive pressure risk
	CapitalIntensity   RiskLevel `json:"capital_intensity"`    // Capital requirements
	WorkingCapitalRisk RiskLevel `json:"working_capital_risk"` // Working capital volatility
}

// RiskLevel defines risk intensity levels
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// IndustryThresholds defines industry-specific adjustment thresholds
type IndustryThresholds struct {
	// Asset Adjustment Thresholds
	GoodwillThreshold         float64 `json:"goodwill_threshold"`          // % of total assets
	IntangibleThreshold       float64 `json:"intangible_threshold"`        // % of total assets
	InventoryObsolescenceRate float64 `json:"inventory_obsolescence_rate"` // % haircut for dead inventory
	DeferredTaxThreshold      float64 `json:"deferred_tax_threshold"`      // % of total assets

	// Liability Adjustment Thresholds
	OperatingLeaseRate      float64 `json:"operating_lease_rate"`      // Discount rate for lease capitalization
	PensionFundingThreshold float64 `json:"pension_funding_threshold"` // Underfunding % threshold
	ContingentLiabilityRate float64 `json:"contingent_liability_rate"` // Probability weighting

	// Earnings Adjustment Thresholds
	RestructuringThreshold float64 `json:"restructuring_threshold"` // % of revenue threshold
	StockCompThreshold     float64 `json:"stock_comp_threshold"`    // % of revenue threshold
	LitigationThreshold    float64 `json:"litigation_threshold"`    // % of revenue threshold

	// Quality Score Adjustments
	QualityScoreAdjustment float64 `json:"quality_score_adjustment"` // Industry-specific quality adjustment
	MinimumQualityScore    float64 `json:"minimum_quality_score"`    // Industry minimum quality threshold
}

// IndustryCharacteristics defines unique traits of an industry
type IndustryCharacteristics struct {
	AssetHeavy            bool     `json:"asset_heavy"`             // High PP&E relative to revenue
	InventoryIntensive    bool     `json:"inventory_intensive"`     // High inventory levels
	IntangibleIntensive   bool     `json:"intangible_intensive"`    // High intangible assets
	RegulatedIndustry     bool     `json:"regulated_industry"`      // Subject to regulatory oversight
	CyclicalEarnings      bool     `json:"cyclical_earnings"`       // Earnings vary with economic cycles
	HighRDIntensity       bool     `json:"high_rd_intensity"`       // High R&D spending
	LongTermContracts     bool     `json:"long_term_contracts"`     // Revenue from long-term contracts
	SeasonalBusiness      bool     `json:"seasonal_business"`       // Seasonal revenue patterns
	HighStockCompensation bool     `json:"high_stock_compensation"` // Above-average stock compensation
	FrequentRestructuring bool     `json:"frequent_restructuring"`  // Regular restructuring activities
	TypicalAdjustments    []string `json:"typical_adjustments"`     // Common adjustment types
}

// NewIndustryClassifier creates a new industry classifier with default configurations
func NewIndustryClassifier() *IndustryClassifier {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
	}

	// Load default sector configurations
	classifier.loadDefaultConfigurations()

	return classifier
}

// ClassifyIndustry determines the industry classification for a company
func (ic *IndustryClassifier) ClassifyIndustry(ticker string, data *entities.FinancialData) (*SectorConfig, error) {
	// TODO: Implement proper industry classification logic
	// For now, use simple heuristics based on financial characteristics

	if data == nil {
		return nil, fmt.Errorf("financial data is required for industry classification")
	}

	// Retail sector detection (check before technology to avoid misclassification)
	if ic.isRetailCompany(data) {
		return ic.sectorConfigs["25"], nil // Consumer Discretionary sector
	}

	// Technology sector detection
	if ic.isTechnologyCompany(ticker, data) {
		return ic.sectorConfigs["45"], nil // Technology sector
	}

	// Manufacturing sector detection
	if ic.isManufacturingCompany(data) {
		return ic.sectorConfigs["20"], nil // Industrials sector
	}

	// Utilities sector detection
	if ic.isUtilitiesCompany(data) {
		return ic.sectorConfigs["55"], nil // Utilities sector
	}

	// Financial services detection
	if ic.isFinancialCompany(data) {
		// TODO: Add financial sector configuration (sector "40")
		// For now, default to industrials
		return ic.sectorConfigs["20"], nil // Default to industrials
	}

	// Healthcare sector detection
	if ic.isHealthcareCompany(data) {
		// TODO: Add healthcare sector configuration (sector "35")
		// For now, default to industrials
		return ic.sectorConfigs["20"], nil // Default to industrials
	}

	// Default to general industrial classification
	return ic.sectorConfigs["20"], nil
}

// GetSectorConfig returns configuration for a specific sector
func (ic *IndustryClassifier) GetSectorConfig(sectorCode string) (*SectorConfig, bool) {
	config, exists := ic.sectorConfigs[sectorCode]
	return config, exists
}

// GetAllSectorConfigs returns all available sector configurations
func (ic *IndustryClassifier) GetAllSectorConfigs() map[string]*SectorConfig {
	return ic.sectorConfigs
}

// loadDefaultConfigurations loads default industry configurations
func (ic *IndustryClassifier) loadDefaultConfigurations() {
	// Technology Sector (45)
	ic.sectorConfigs["45"] = &SectorConfig{
		SectorCode:    "45",
		SectorName:    "Information Technology",
		SubIndustries: []string{"451010", "451020", "451030"}, // Software, Hardware, Semiconductors
		RiskProfile: RiskProfile{
			CyclicalityRisk:    RiskMedium,
			RegulatoryRisk:     RiskLow,
			TechnologyRisk:     RiskHigh,
			CompetitiveRisk:    RiskHigh,
			CapitalIntensity:   RiskLow,
			WorkingCapitalRisk: RiskLow,
		},
		Thresholds: IndustryThresholds{
			GoodwillThreshold:         0.15,  // 15% of total assets
			IntangibleThreshold:       0.20,  // 20% of total assets
			InventoryObsolescenceRate: 0.50,  // 50% haircut (high obsolescence)
			DeferredTaxThreshold:      0.05,  // 5% of total assets
			OperatingLeaseRate:        0.055, // 5.5% discount rate
			PensionFundingThreshold:   0.80,  // 80% funding threshold
			ContingentLiabilityRate:   0.60,  // 60% probability weighting
			RestructuringThreshold:    0.02,  // 2% of revenue
			StockCompThreshold:        0.08,  // 8% of revenue (high for tech)
			LitigationThreshold:       0.01,  // 1% of revenue
			QualityScoreAdjustment:    -5.0,  // -5 points for tech volatility
			MinimumQualityScore:       65.0,  // 65 minimum quality score
		},
		CommonAdjustments: []string{"stock_compensation", "intangible_writedown", "rd_capitalization"},
		KeyMetrics:        []string{"rd_intensity", "stock_compensation_ratio", "intangible_ratio"},
		Characteristics: IndustryCharacteristics{
			AssetHeavy:            false,
			InventoryIntensive:    false,
			IntangibleIntensive:   true,
			RegulatedIndustry:     false,
			CyclicalEarnings:      true,
			HighRDIntensity:       true,
			LongTermContracts:     false,
			SeasonalBusiness:      false,
			HighStockCompensation: true,
			FrequentRestructuring: false,
			TypicalAdjustments:    []string{"A2", "A3", "C4"}, // Intangibles, R&D, Stock comp
		},
	}

	// Manufacturing/Industrials Sector (20)
	ic.sectorConfigs["20"] = &SectorConfig{
		SectorCode:    "20",
		SectorName:    "Industrials",
		SubIndustries: []string{"201010", "201020", "201030"}, // Aerospace, Machinery, Transportation
		RiskProfile: RiskProfile{
			CyclicalityRisk:    RiskHigh,
			RegulatoryRisk:     RiskMedium,
			TechnologyRisk:     RiskMedium,
			CompetitiveRisk:    RiskMedium,
			CapitalIntensity:   RiskHigh,
			WorkingCapitalRisk: RiskHigh,
		},
		Thresholds: IndustryThresholds{
			GoodwillThreshold:         0.10,  // 10% of total assets
			IntangibleThreshold:       0.08,  // 8% of total assets
			InventoryObsolescenceRate: 0.25,  // 25% haircut
			DeferredTaxThreshold:      0.03,  // 3% of total assets
			OperatingLeaseRate:        0.050, // 5.0% discount rate
			PensionFundingThreshold:   0.85,  // 85% funding threshold
			ContingentLiabilityRate:   0.70,  // 70% probability weighting
			RestructuringThreshold:    0.03,  // 3% of revenue
			StockCompThreshold:        0.03,  // 3% of revenue
			LitigationThreshold:       0.015, // 1.5% of revenue
			QualityScoreAdjustment:    0.0,   // No adjustment (baseline)
			MinimumQualityScore:       70.0,  // 70 minimum quality score
		},
		CommonAdjustments: []string{"inventory_obsolescence", "pension_adjustment", "restructuring"},
		KeyMetrics:        []string{"asset_turnover", "inventory_turnover", "pension_funding_ratio"},
		Characteristics: IndustryCharacteristics{
			AssetHeavy:            true,
			InventoryIntensive:    true,
			IntangibleIntensive:   false,
			RegulatedIndustry:     true,
			CyclicalEarnings:      true,
			HighRDIntensity:       false,
			LongTermContracts:     true,
			SeasonalBusiness:      false,
			HighStockCompensation: false,
			FrequentRestructuring: true,
			TypicalAdjustments:    []string{"A5", "B2", "C1"}, // Inventory, Pensions, Restructuring
		},
	}

	// Consumer Discretionary/Retail Sector (25)
	ic.sectorConfigs["25"] = &SectorConfig{
		SectorCode:    "25",
		SectorName:    "Consumer Discretionary",
		SubIndustries: []string{"255010", "255020", "255030"}, // Retail, Restaurants, Hotels
		RiskProfile: RiskProfile{
			CyclicalityRisk:    RiskHigh,
			RegulatoryRisk:     RiskLow,
			TechnologyRisk:     RiskMedium,
			CompetitiveRisk:    RiskHigh,
			CapitalIntensity:   RiskMedium,
			WorkingCapitalRisk: RiskHigh,
		},
		Thresholds: IndustryThresholds{
			GoodwillThreshold:         0.12,  // 12% of total assets
			IntangibleThreshold:       0.10,  // 10% of total assets
			InventoryObsolescenceRate: 0.40,  // 40% haircut (fashion/seasonal)
			DeferredTaxThreshold:      0.04,  // 4% of total assets
			OperatingLeaseRate:        0.060, // 6.0% discount rate (store leases)
			PensionFundingThreshold:   0.85,  // 85% funding threshold
			ContingentLiabilityRate:   0.65,  // 65% probability weighting
			RestructuringThreshold:    0.025, // 2.5% of revenue
			StockCompThreshold:        0.04,  // 4% of revenue
			LitigationThreshold:       0.02,  // 2% of revenue
			QualityScoreAdjustment:    -3.0,  // -3 points for retail volatility
			MinimumQualityScore:       68.0,  // 68 minimum quality score
		},
		CommonAdjustments: []string{"inventory_obsolescence", "operating_lease_capitalization", "seasonal_adjustment"},
		KeyMetrics:        []string{"inventory_turnover", "same_store_sales", "lease_intensity"},
		Characteristics: IndustryCharacteristics{
			AssetHeavy:            false,
			InventoryIntensive:    true,
			IntangibleIntensive:   false,
			RegulatedIndustry:     false,
			CyclicalEarnings:      true,
			HighRDIntensity:       false,
			LongTermContracts:     false,
			SeasonalBusiness:      true,
			HighStockCompensation: false,
			FrequentRestructuring: true,
			TypicalAdjustments:    []string{"A5", "B1", "C7"}, // Inventory, Leases, Working capital
		},
	}
}

// Industry classification helper methods
// TODO: Replace with more sophisticated classification logic using external data sources

// isTechnologyCompany detects technology companies based on financial characteristics
func (ic *IndustryClassifier) isTechnologyCompany(ticker string, data *entities.FinancialData) bool {
	// High R&D intensity
	if data.Revenue > 0 && data.ResearchAndDevelopment > 0 {
		rdIntensity := data.ResearchAndDevelopment / data.Revenue
		if rdIntensity > 0.10 { // >10% R&D intensity
			return true
		}
	}

	// High stock-based compensation
	if data.Revenue > 0 && data.StockBasedCompensation > 0 {
		stockCompRatio := data.StockBasedCompensation / data.Revenue
		if stockCompRatio > 0.05 { // >5% stock compensation
			return true
		}
	}

	// High intangible assets
	if data.TotalAssets > 0 && data.IntangibleAssets > 0 {
		intangibleRatio := data.IntangibleAssets / data.TotalAssets
		if intangibleRatio > 0.15 { // >15% intangible assets
			return true
		}
	}

	// Known technology tickers (temporary heuristic)
	techTickers := []string{"AAPL", "MSFT", "GOOGL", "GOOG", "AMZN", "META", "TSLA", "NVDA", "ORCL", "CRM"}
	for _, techTicker := range techTickers {
		if strings.EqualFold(ticker, techTicker) {
			return true
		}
	}

	return false
}

// isManufacturingCompany detects manufacturing companies
func (ic *IndustryClassifier) isManufacturingCompany(data *entities.FinancialData) bool {
	// High tangible assets (proxy for PP&E) relative to total assets
	if data.TotalAssets > 0 && data.TangibleAssets > 0 {
		tangibleRatio := data.TangibleAssets / data.TotalAssets
		if tangibleRatio > 0.60 { // >60% tangible assets (capital intensive)
			return true
		}
	}

	// Significant inventory levels
	if data.TotalAssets > 0 && data.Inventory > 0 {
		inventoryRatio := data.Inventory / data.TotalAssets
		if inventoryRatio > 0.15 { // >15% inventory
			return true
		}
	}

	return false
}

// isRetailCompany detects retail companies
func (ic *IndustryClassifier) isRetailCompany(data *entities.FinancialData) bool {
	// High inventory turnover characteristics
	if data.TotalAssets > 0 && data.Inventory > 0 {
		inventoryRatio := data.Inventory / data.TotalAssets
		// Retail typically has moderate inventory levels (10-30%)
		if inventoryRatio > 0.10 && inventoryRatio < 0.30 {
			// Asset-light model (high intangible ratio indicates brand value)
			if data.IntangibleAssets > 0 {
				intangibleRatio := data.IntangibleAssets / data.TotalAssets
				if intangibleRatio > 0.10 { // >10% intangibles (brand value)
					return true
				}
			}
			// Or moderate tangible asset ratio
			if data.TangibleAssets > 0 {
				tangibleRatio := data.TangibleAssets / data.TotalAssets
				if tangibleRatio < 0.70 { // <70% tangible assets (asset-light)
					return true
				}
			}
		}
	}

	return false
}

// isUtilitiesCompany detects utilities companies
func (ic *IndustryClassifier) isUtilitiesCompany(data *entities.FinancialData) bool {
	// Very high tangible assets (capital intensive)
	if data.TotalAssets > 0 && data.TangibleAssets > 0 {
		tangibleRatio := data.TangibleAssets / data.TotalAssets
		if tangibleRatio > 0.80 { // >80% tangible assets (very capital intensive)
			// Low inventory (utilities don't hold much inventory)
			inventoryRatio := 0.0
			if data.Inventory > 0 {
				inventoryRatio = data.Inventory / data.TotalAssets
			}
			if inventoryRatio < 0.05 { // <5% inventory
				return true
			}
		}
	}

	return false
}

// isFinancialCompany detects financial services companies
func (ic *IndustryClassifier) isFinancialCompany(data *entities.FinancialData) bool {
	// Financial companies have different balance sheet structure
	// Low tangible assets, low inventory, high debt levels

	if data.TotalAssets > 0 {
		// Low tangible assets (mostly financial assets)
		tangibleRatio := 0.0
		if data.TangibleAssets > 0 {
			tangibleRatio = data.TangibleAssets / data.TotalAssets
		}

		// Low inventory
		inventoryRatio := 0.0
		if data.Inventory > 0 {
			inventoryRatio = data.Inventory / data.TotalAssets
		}

		// High debt levels (financial leverage)
		debtRatio := 0.0
		if data.TotalDebt > 0 {
			debtRatio = data.TotalDebt / data.TotalAssets
		}

		if tangibleRatio < 0.30 && inventoryRatio < 0.02 && debtRatio > 0.20 {
			return true
		}
	}

	return false
}

// isHealthcareCompany detects healthcare companies
func (ic *IndustryClassifier) isHealthcareCompany(data *entities.FinancialData) bool {
	// High R&D intensity (similar to tech but different characteristics)
	if data.Revenue > 0 && data.ResearchAndDevelopment > 0 {
		rdIntensity := data.ResearchAndDevelopment / data.Revenue
		if rdIntensity > 0.15 { // >15% R&D intensity (higher than tech)
			// Lower stock compensation than tech
			stockCompRatio := 0.0
			if data.StockBasedCompensation > 0 {
				stockCompRatio = data.StockBasedCompensation / data.Revenue
			}
			if stockCompRatio < 0.05 { // <5% stock compensation
				return true
			}
		}
	}

	return false
}

// ApplyIndustrySpecificThresholds applies industry-specific thresholds to cleaning rules
func (ic *IndustryClassifier) ApplyIndustrySpecificThresholds(rules []*entities.CleaningRule, sectorConfig *SectorConfig) []*entities.CleaningRule {
	if sectorConfig == nil {
		return rules
	}

	adjustedRules := make([]*entities.CleaningRule, len(rules))
	copy(adjustedRules, rules)

	for i, rule := range adjustedRules {
		if rule.Threshold == nil {
			rule.Threshold = &entities.ThresholdConfig{}
		}

		// Apply industry-specific thresholds based on rule ID
		switch rule.ID {
		case "goodwill_exclusion":
			rule.Threshold.PercentageOfAssets = &sectorConfig.Thresholds.GoodwillThreshold
		case "intangible_writedown":
			rule.Threshold.PercentageOfAssets = &sectorConfig.Thresholds.IntangibleThreshold
		case "inventory_obsolescence":
			rule.Threshold.WritedownRate = &sectorConfig.Thresholds.InventoryObsolescenceRate
		case "deferred_tax_adjustment":
			rule.Threshold.PercentageOfAssets = &sectorConfig.Thresholds.DeferredTaxThreshold
		case "restructuring_charges":
			rule.Threshold.PercentageOfRevenue = &sectorConfig.Thresholds.RestructuringThreshold
		case "stock_compensation":
			rule.Threshold.PercentageOfRevenue = &sectorConfig.Thresholds.StockCompThreshold
		case "litigation_settlements":
			rule.Threshold.PercentageOfRevenue = &sectorConfig.Thresholds.LitigationThreshold
			// TODO: Add support for additional threshold types in ThresholdConfig entity
			// - DiscountRate for operating lease capitalization
			// - FundingRatio for pension adjustments
			// - ProbabilityWeight for contingent liabilities
		}

		adjustedRules[i] = rule
	}

	return adjustedRules
}
