package entities

// RiskLevel represents the level of risk in various industry factors
type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "low"
	RiskLevelMedium   RiskLevel = "medium"
	RiskLevelHigh     RiskLevel = "high"
	RiskLevelCritical RiskLevel = "critical"
)

// SectorConfig represents industry-specific configuration and characteristics
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

// RiskProfile represents the risk characteristics of an industry sector
type RiskProfile struct {
	CyclicalityRisk    RiskLevel `json:"cyclicality_risk"`     // Economic cycle sensitivity
	RegulatoryRisk     RiskLevel `json:"regulatory_risk"`      // Regulatory change risk
	TechnologyRisk     RiskLevel `json:"technology_risk"`      // Technology disruption risk
	CompetitiveRisk    RiskLevel `json:"competitive_risk"`     // Competitive pressure risk
	CapitalIntensity   RiskLevel `json:"capital_intensity"`    // Capital requirements
	WorkingCapitalRisk RiskLevel `json:"working_capital_risk"` // Working capital volatility
}

// IndustryThresholds defines industry-specific thresholds for adjustments
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

// IndustryCharacteristics represents the operational characteristics of an industry
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
