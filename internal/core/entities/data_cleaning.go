package entities

import (
	"time"
)

// RuleCategory represents the three main categories from SEC cleaning guide
type RuleCategory string

const (
	// AssetQuality represents Category A: Over-stated/low-quality assets
	AssetQuality RuleCategory = "asset_quality"
	// LiabilityCompleteness represents Category B: Under-stated liabilities & off-balance-sheet exposures
	LiabilityCompleteness RuleCategory = "liability_completeness"
	// EarningsNormalization represents Category C: Earnings/cash-flow distortion items
	EarningsNormalization RuleCategory = "earnings_normalization"

	// Legacy names for backward compatibility with tests
	RuleCategoryAssetQuality          RuleCategory = "asset_quality"
	RuleCategoryLiabilityCompleteness RuleCategory = "liability_completeness"
	RuleCategoryEarningsNormalization RuleCategory = "earnings_normalization"
)

// AdjustmentType defines how a rule should be applied
type AdjustmentType string

const (
	// Exclude completely removes the item from calculations
	Exclude AdjustmentType = "exclude"
	// Writedown reduces the value by a specified percentage
	Writedown AdjustmentType = "writedown"
	// Reclassify moves the item to a different account
	Reclassify AdjustmentType = "reclassify"
	// TreatAsDebt treats the item as debt for WACC calculations
	TreatAsDebt AdjustmentType = "treat_as_debt"
	// ProbabilityWeighted applies probability weighting to contingent liabilities
	ProbabilityWeighted AdjustmentType = "probability_weighted"
	// FlagForReview marks the item for analyst review without adjustment
	FlagForReview AdjustmentType = "flag"

	// Legacy/Alternative names for backward compatibility with tests
	AdjustmentTypeExclusion          AdjustmentType = "exclude"
	AdjustmentTypeWritedown          AdjustmentType = "writedown"
	AdjustmentTypeValuationAllowance AdjustmentType = "valuation_allowance"
)

// FlagSeverity indicates the importance level of a flag
type FlagSeverity string

const (
	// FlagSeverityLow indicates minor issues that may not require immediate action
	FlagSeverityLow FlagSeverity = "low"
	// FlagSeverityMedium indicates moderate issues that should be reviewed
	FlagSeverityMedium FlagSeverity = "medium"
	// FlagSeverityHigh indicates significant issues that require attention
	FlagSeverityHigh FlagSeverity = "high"
	// FlagSeverityCritical indicates critical issues that require immediate action
	FlagSeverityCritical FlagSeverity = "critical"

	// Legacy constants for backward compatibility
	Info     FlagSeverity = "info"
	Warning  FlagSeverity = "warning"
	Critical FlagSeverity = "critical"
)

// ThresholdConfig defines conditional logic for rule application
type ThresholdConfig struct {
	// Percentage-based thresholds
	PercentageOfRevenue *float64 `json:"percentage_of_revenue,omitempty"`
	PercentageOfAssets  *float64 `json:"percentage_of_assets,omitempty"`
	PercentageOfEquity  *float64 `json:"percentage_of_equity,omitempty"`

	// Growth and ratio thresholds (for inventory obsolescence)
	GrowthMultiple  *float64 `json:"growth_multiple,omitempty"`
	TurnoverDecline *float64 `json:"turnover_decline,omitempty"`
	WritedownRate   *float64 `json:"writedown_rate,omitempty"`

	// Absolute value thresholds
	MinAmount *float64 `json:"min_amount,omitempty"`
	MaxAmount *float64 `json:"max_amount,omitempty"`

	// Time-based thresholds
	AgeInYears *int `json:"age_in_years,omitempty"`
}

// CleaningRule represents a single data cleaning rule from the SEC guide
type CleaningRule struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Category    RuleCategory `json:"category"`

	// XBRL tags this rule applies to
	XBRLTags []string `json:"xbrl_tags"`
	// Text patterns for additional matching
	Patterns []string `json:"patterns,omitempty"`

	// How to apply the rule
	Adjustment AdjustmentType   `json:"adjustment"`
	Threshold  *ThresholdConfig `json:"threshold,omitempty"`

	// Industry applicability (GICS codes or "all")
	Industry []string `json:"industry"`

	// Flagging information
	Severity FlagSeverity `json:"severity"`

	// Rule metadata
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"` // Reference to SEC guide section

	// Dependencies - rules that must be applied before this one
	Dependencies []string `json:"dependencies,omitempty"`
}

// RulesConfig represents the complete configuration loaded from JSON
type RulesConfig struct {
	Version     string                 `json:"version"`
	Description string                 `json:"description"`
	CreatedAt   time.Time              `json:"created_at"`
	Rules       []CleaningRule         `json:"rules"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// IndustryRuleOverride allows industry-specific rule modifications
type IndustryRuleOverride struct {
	RuleID    string           `json:"rule_id"`
	Enabled   *bool            `json:"enabled,omitempty"`
	Threshold *ThresholdConfig `json:"threshold,omitempty"`
	Severity  *FlagSeverity    `json:"severity,omitempty"`
}

// IndustryConfig represents industry-specific rule configurations
type IndustryConfig struct {
	GICSCode     string                 `json:"gics_code"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Overrides    []IndustryRuleOverride `json:"overrides"`
	SpecialRules []CleaningRule         `json:"special_rules,omitempty"`
}

// Flag represents a risk flag raised during cleaning
type Flag struct {
	ID             string       `json:"id"`
	RuleID         string       `json:"rule_id"`
	Type           string       `json:"type"`
	Severity       FlagSeverity `json:"severity"`
	Amount         float64      `json:"amount"`
	Percentage     float64      `json:"percentage,omitempty"`
	Description    string       `json:"description"`
	Recommendation string       `json:"recommendation,omitempty"`
	Industry       string       `json:"industry,omitempty"`
	Threshold      float64      `json:"threshold,omitempty"`
	Timestamp      time.Time    `json:"timestamp"`
}

// Adjustment represents a data cleaning adjustment made
type Adjustment struct {
	ID          string         `json:"id"`
	RuleID      string         `json:"rule_id"`
	Category    RuleCategory   `json:"category"`
	Type        AdjustmentType `json:"type"`
	Amount      float64        `json:"amount"`
	FromAccount string         `json:"from_account"`
	ToAccount   string         `json:"to_account,omitempty"`
	Percentage  float64        `json:"percentage,omitempty"`
	Reasoning   string         `json:"reasoning"`
	Applied     bool           `json:"applied"`
	Timestamp   time.Time      `json:"timestamp"`
}

// RuleResult represents the outcome of applying rules to financial data
type RuleResult struct {
	Success        bool          `json:"success"`
	RulesApplied   int           `json:"rules_applied"`
	Adjustments    []Adjustment  `json:"adjustments"`
	Flags          []Flag        `json:"flags"`
	QualityScore   float64       `json:"quality_score"` // 0-100
	ProcessingTime time.Duration `json:"processing_time"`
	Errors         []string      `json:"errors,omitempty"`
}

// CleaningResult represents the complete result of data cleaning
type CleaningResult struct {
	// Processing metadata
	Success        bool          `json:"success"`
	RulesApplied   int           `json:"rules_applied"`
	ProcessingTime time.Duration `json:"processing_time"`
	Timestamp      time.Time     `json:"timestamp"`

	// Cleaned data
	CleanedData *FinancialData `json:"cleaned_data"`

	// Quality assessment
	QualityScore  float64  `json:"quality_score"`  // 0-100 scale
	QualityGrade  string   `json:"quality_grade"`  // A, B, C, D, F
	QualityIssues []string `json:"quality_issues"` // List of issues found

	// Adjustments made
	Adjustments []Adjustment `json:"adjustments"`

	// Risk flags raised
	Flags []Flag `json:"flags"`

	// Industry analysis
	IndustryCode     string `json:"industry_code"`
	IndustrySpecific bool   `json:"industry_specific"` // Whether industry rules were applied

	// Error information
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// CleaningContext holds contextual information for cleaning operations
type CleaningContext struct {
	IndustryCode     string
	CompanySize      CompanySize
	DataVintage      time.Time
	EnableIndustry   bool
	EnableCaching    bool
	QualityThreshold float64
}

// CompanySize enum for company size classification
type CompanySize string

const (
	SmallCap CompanySize = "small"
	MidCap   CompanySize = "mid"
	LargeCap CompanySize = "large"
	MegaCap  CompanySize = "mega"
)

// QualityGrade represents the quality grade for financial data
type QualityGrade string

const (
	GradeA QualityGrade = "A" // 90-100: Excellent quality
	GradeB QualityGrade = "B" // 80-89:  Good quality
	GradeC QualityGrade = "C" // 70-79:  Fair quality
	GradeD QualityGrade = "D" // 60-69:  Poor quality
	GradeF QualityGrade = "F" // 0-59:   Failed quality
)

// GetQualityGrade converts a numeric score to a letter grade
func GetQualityGrade(score float64) QualityGrade {
	switch {
	case score >= 90:
		return GradeA
	case score >= 80:
		return GradeB
	case score >= 70:
		return GradeC
	case score >= 60:
		return GradeD
	default:
		return GradeF
	}
}

// CleaningStats represents aggregate statistics from cleaning operations
type CleaningStats struct {
	TotalCompanies      int                  `json:"total_companies"`
	AverageQualityScore float64              `json:"average_quality_score"`
	QualityDistribution map[QualityGrade]int `json:"quality_distribution"`
	CommonAdjustments   map[string]int       `json:"common_adjustments"`
	CommonFlags         map[string]int       `json:"common_flags"`
	ProcessingTime      time.Duration        `json:"processing_time"`
}

// IndustryProfile represents industry-specific patterns and thresholds
type IndustryProfile struct {
	IndustryCode  string             `json:"industry_code"`
	IndustryName  string             `json:"industry_name"`
	CommonIssues  []string           `json:"common_issues"`
	TypicalRatios map[string]float64 `json:"typical_ratios"`
	RiskFactors   []string           `json:"risk_factors"`
	Thresholds    map[string]float64 `json:"thresholds"`
}

// Constants for well-known rule IDs from the SEC cleaning guide
const (
	// Asset Quality Rules (Category A)
	RuleGoodwillExclusion    = "goodwill_exclusion"
	RuleIntangibleAdjustment = "intangible_adjustment"
	RuleCapitalizedSoftware  = "capitalized_software"
	RuleDeferredTaxAssets    = "deferred_tax_assets"
	RuleObsoleteInventory    = "obsolete_inventory"
	RuleRightOfUseAssets     = "right_of_use_assets"
	RuleExcessCash           = "excess_cash"

	// Liability Completeness Rules (Category B)
	RuleOperatingLeases       = "operating_leases"
	RulePensionObligations    = "pension_obligations"
	RuleContingentLiabilities = "contingent_liabilities"

	// Earnings Normalization Rules (Category C)
	RuleRestructuringCharges  = "restructuring_charges"
	RuleAssetSaleGains        = "asset_sale_gains"
	RuleLitigationSettlements = "litigation_settlements"
	RuleStockCompensation     = "stock_compensation"
	RuleDerivativeGainsLosses = "derivative_gains_losses"
	RuleCapitalizedInterest   = "capitalized_interest"
)

// GICS Industry Codes for industry-specific rules
const (
	GICSInformationTechnology = "45"
	GICSConsumerDiscretionary = "25"
	GICSConsumerStaples       = "30"
	GICSFinancials            = "40"
	GICSUtilities             = "55"
	GICSIndustrials           = "20"
	GICSMaterials             = "15"
	GICSEnergy                = "10"
	GICSHealthCare            = "35"
	GICSRealEstate            = "60"
	GICSTelecom               = "50"
)
