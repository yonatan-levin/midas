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
	SectorName       string `json:"sector_name,omitempty"` // Human-readable GICS sector name resolved from IndustryCode (e.g. "Information Technology")
	IndustrySpecific bool   `json:"industry_specific"`     // Whether industry rules were applied

	// AI analysis metadata (Phase 3)
	AIMetadata map[string]string `json:"ai_metadata,omitempty"` // AI service metadata and confidence scores

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

	// AI-powered analysis support (Phase 3)
	FootnoteText string            `json:"footnote_text,omitempty"` // Free-form footnotes for AI analysis
	AIMetadata   map[string]string `json:"ai_metadata,omitempty"`   // AI service metadata and confidence scores
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

// Pipeline Stage Entities

// PipelineStage represents different stages in the data cleaning pipeline
type PipelineStage string

const (
	StageAssetQuality          PipelineStage = "asset_quality"
	StageLiabilityCompleteness PipelineStage = "liability_completeness"
	StageEarningsNormalization PipelineStage = "earnings_normalization"
	StageQualityAssessment     PipelineStage = "quality_assessment"
	StageFlagging              PipelineStage = "flagging"
)

// StageResult represents the result of a single pipeline stage
type StageResult struct {
	Stage        PipelineStage `json:"stage"`
	Success      bool          `json:"success"`
	Adjustments  []Adjustment  `json:"adjustments"`
	Flags        []Flag        `json:"flags"`
	Duration     time.Duration `json:"duration"`
	RulesApplied int           `json:"rules_applied"`
	Errors       []string      `json:"errors,omitempty"`
	Warnings     []string      `json:"warnings,omitempty"`
}

// PipelineResult represents the complete result of the data cleaning pipeline
type PipelineResult struct {
	Success       bool            `json:"success"`
	StageResults  []StageResult   `json:"stage_results"`
	TotalDuration time.Duration   `json:"total_duration"`
	CleanedData   *FinancialData  `json:"cleaned_data"`
	Summary       PipelineSummary `json:"summary"`
}

// PipelineSummary provides aggregate statistics for the pipeline execution
type PipelineSummary struct {
	TotalAdjustments  int `json:"total_adjustments"`
	TotalFlags        int `json:"total_flags"`
	TotalRulesApplied int `json:"total_rules_applied"`
	StagesProcessed   int `json:"stages_processed"`
	ErrorCount        int `json:"error_count"`
	WarningCount      int `json:"warning_count"`
}

// Reporting Entities

// CleaningReport represents a comprehensive report of the data cleaning process
type CleaningReport struct {
	ReportID       string                 `json:"report_id"`
	Ticker         string                 `json:"ticker"`
	GeneratedAt    time.Time              `json:"generated_at"`
	ProcessingTime time.Duration          `json:"processing_time"`
	QualityScore   float64                `json:"quality_score"` // 0-100
	QualityGrade   QualityGrade           `json:"quality_grade"` // A, B, C, D, F
	Success        bool                   `json:"success"`
	Summary        ReportSummary          `json:"summary"`
	AuditTrail     AuditTrail             `json:"audit_trail"`
	Sections       []ReportSection        `json:"sections"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// ReportSummary provides high-level summary statistics for the cleaning report
type ReportSummary struct {
	TotalAdjustments int           `json:"total_adjustments"`
	TotalFlags       int           `json:"total_flags"`
	RulesApplied     int           `json:"rules_applied"`
	OriginalAssets   float64       `json:"original_assets"`
	AdjustedAssets   float64       `json:"adjusted_assets"`
	AdjustmentImpact float64       `json:"adjustment_impact"` // Positive = increase, negative = decrease
	StagesProcessed  int           `json:"stages_processed"`
	ProcessingTime   time.Duration `json:"processing_time"`
}

// AuditTrail provides detailed tracking of all changes made during cleaning
type AuditTrail struct {
	Adjustments     []Adjustment  `json:"adjustments"`
	Flags           []Flag        `json:"flags"`
	StagesProcessed int           `json:"stages_processed"`
	TotalDuration   time.Duration `json:"total_duration"`
	ProcessingOrder []string      `json:"processing_order"` // Stage names in order processed
	Timestamp       time.Time     `json:"timestamp"`
}

// ReportSection represents a section of the cleaning report
type ReportSection struct {
	Title       string                 `json:"title"`
	Content     string                 `json:"content"`
	Data        map[string]interface{} `json:"data,omitempty"`
	Order       int                    `json:"order"`
	Collapsible bool                   `json:"collapsible"`
}

// Adjustment Result Entities

// AdjustmentResult represents the result of applying a data cleaning adjustment
type AdjustmentResult struct {
	Amount      float64      `json:"amount"`
	Applied     bool         `json:"applied"`
	Adjustments []Adjustment `json:"adjustments"`
	Flags       []Flag       `json:"flags"`
	Reasoning   string       `json:"reasoning"`
}

// TangibleAssetsResult represents the result of calculating net tangible assets
type TangibleAssetsResult struct {
	AdjustedTangibleAssets float64      `json:"adjusted_tangible_assets"`
	Adjustments            []Adjustment `json:"adjustments"`
	AuditTrail             string       `json:"audit_trail"`
}

// AssetAdjustmentResult represents the result of applying asset adjustments
type AssetAdjustmentResult struct {
	Applied                bool         `json:"applied"`
	TotalAssetAdjustment   float64      `json:"total_asset_adjustment"`
	AdjustedTangibleAssets float64      `json:"adjusted_tangible_assets"`
	Adjustments            []Adjustment `json:"adjustments"`
	Flags                  []Flag       `json:"flags"`
	AuditTrail             string       `json:"audit_trail"`
}

// LiabilityAdjustmentResult represents the result of applying liability adjustments
type LiabilityAdjustmentResult struct {
	Applied                  bool         `json:"applied"`
	TotalLiabilityAdjustment float64      `json:"total_liability_adjustment"`
	AdjustedTotalDebt        float64      `json:"adjusted_total_debt"`
	Adjustments              []Adjustment `json:"adjustments"`
	Flags                    []Flag       `json:"flags"`
	AuditTrail               string       `json:"audit_trail"`
}

// Quality Assessment Entities

// QualityResult represents the result of data quality assessment
type QualityResult struct {
	QualityScore  float64  `json:"quality_score"`  // 0-100 scale
	QualityGrade  string   `json:"quality_grade"`  // A, B, C, D, F
	QualityIssues []string `json:"quality_issues"` // List of issues found
}

// Recommendation represents a recommendation for improving data quality
type Recommendation struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Priority    string    `json:"priority"`    // High, Medium, Low
	Description string    `json:"description"` // What the issue is
	Action      string    `json:"action"`      // What should be done
	Impact      string    `json:"impact"`      // Expected impact of fix
	Timestamp   time.Time `json:"timestamp"`
}
