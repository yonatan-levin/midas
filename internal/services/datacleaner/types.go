package datacleaner

import (
	"context"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/rules"
)

// DataCleanerService defines the interface for the main data cleaning service
type DataCleanerService interface {
	// CleanFinancialData cleans and normalizes financial data using configured rules
	CleanFinancialData(ctx context.Context, data *entities.FinancialData) (*CleaningResult, error)

	// GetIndustryRules returns applicable rules for a specific industry
	GetIndustryRules(industryCode string) ([]rules.CleaningRule, error)

	// GetQualityScore calculates quality score for financial data without applying changes
	GetQualityScore(ctx context.Context, data *entities.FinancialData) (float64, error)

	// ValidateData performs basic data validation before cleaning
	ValidateData(data *entities.FinancialData) error
}

// CleaningResult represents the complete result of data cleaning
type CleaningResult struct {
	// Processing metadata
	Success        bool          `json:"success"`
	RulesApplied   int           `json:"rules_applied"`
	ProcessingTime time.Duration `json:"processing_time"`
	Timestamp      time.Time     `json:"timestamp"`

	// Cleaned data
	CleanedData *entities.FinancialData `json:"cleaned_data"`

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

// Adjustment represents a specific data cleaning adjustment made
type Adjustment struct {
	ID          string               `json:"id"`
	RuleID      string               `json:"rule_id"`
	Category    rules.RuleCategory   `json:"category"`
	Type        rules.AdjustmentType `json:"type"`
	Amount      float64              `json:"amount"`
	FromAccount string               `json:"from_account"`
	ToAccount   string               `json:"to_account,omitempty"`
	Percentage  float64              `json:"percentage,omitempty"`
	Reasoning   string               `json:"reasoning"`
	Applied     bool                 `json:"applied"`
	Timestamp   time.Time            `json:"timestamp"`
}

// Flag represents a risk flag raised during cleaning
type Flag struct {
	ID             string             `json:"id"`
	RuleID         string             `json:"rule_id"`
	Type           string             `json:"type"`
	Severity       rules.FlagSeverity `json:"severity"`
	Amount         float64            `json:"amount"`
	Percentage     float64            `json:"percentage,omitempty"`
	Description    string             `json:"description"`
	Recommendation string             `json:"recommendation,omitempty"`
	Industry       string             `json:"industry,omitempty"`
	Threshold      float64            `json:"threshold,omitempty"`
	Timestamp      time.Time          `json:"timestamp"`
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
