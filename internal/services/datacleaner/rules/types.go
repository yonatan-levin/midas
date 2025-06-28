package rules

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
	// FlagForReview marks the item for analyst review without adjustment
	FlagForReview AdjustmentType = "flag"
)

// FlagSeverity indicates the importance level of a flag
type FlagSeverity string

const (
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

// RuleEngine defines the interface for the rules processing engine
type RuleEngine interface {
	// LoadRules loads rules from configuration
	LoadRules(configPath string) error
	// LoadIndustryRules loads industry-specific rule overrides
	LoadIndustryRules(industryPath string) error
	// GetRules returns all loaded rules, optionally filtered by category
	GetRules(category *RuleCategory) []CleaningRule
	// GetIndustryRules returns rules for a specific industry
	GetIndustryRules(industry string) []CleaningRule
	// GetRulesByCategory returns all enabled rules for a specific category
	GetRulesByCategory(category RuleCategory) []CleaningRule
	// ValidateRules validates loaded rules for consistency
	ValidateRules() error
	// GetRuleByID returns a specific rule by ID
	GetRuleByID(id string) (*CleaningRule, error)
	// GetRuleVersion returns the version of loaded rules
	GetRuleVersion() string
}

// RuleLoader defines the interface for loading rules from external sources
type RuleLoader interface {
	// LoadFromFile loads rules from a JSON file
	LoadFromFile(path string) (*RulesConfig, error)
	// LoadIndustryFromFile loads industry rules from a JSON file
	LoadIndustryFromFile(path string) (*IndustryConfig, error)
	// ValidateSchema validates rules against JSON schema
	ValidateSchema(rules *RulesConfig, schemaPath string) error
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
