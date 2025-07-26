package entities

import (
	"time"
)

// ValidationLevel defines the strictness of data validation
type ValidationLevel string

const (
	ValidationNone     ValidationLevel = "none"
	ValidationBasic    ValidationLevel = "basic"
	ValidationStrict   ValidationLevel = "strict"
	ValidationCritical ValidationLevel = "critical"
)

// DataQualityReport represents the result of data quality validation
type DataQualityReport struct {
	OverallScore    float64            `json:"overall_score"` // 0-100
	Grade           QualityGrade       `json:"grade"`         // A, B, C, D, F
	CriticalIssues  int                `json:"critical_issues"`
	MajorIssues     int                `json:"major_issues"`
	MinorIssues     int                `json:"minor_issues"`
	ValidationLevel ValidationLevel    `json:"validation_level"`
	Validations     []ValidationResult `json:"validations"`
	Recommendations []string           `json:"recommendations"`
	CompletedAt     time.Time          `json:"completed_at"`
}

// ValidationResult represents the result of a single validation check
type ValidationResult struct {
	CheckName     string             `json:"check_name"`
	CheckType     ValidationCheck    `json:"check_type"`
	Passed        bool               `json:"passed"`
	Severity      ValidationSeverity `json:"severity"`
	Message       string             `json:"message"`
	ExpectedValue interface{}        `json:"expected_value,omitempty"`
	ActualValue   interface{}        `json:"actual_value,omitempty"`
	Suggestion    string             `json:"suggestion,omitempty"`
}

// ValidationCheck represents types of validation checks
type ValidationCheck string

const (
	ValidationCheckRequiredField ValidationCheck = "required_field"
	ValidationCheckReasonability ValidationCheck = "reasonability"
	ValidationCheckConsistency   ValidationCheck = "consistency"
	ValidationCheckFreshness     ValidationCheck = "freshness"
	ValidationCheckCrossSource   ValidationCheck = "cross_source"
)

// ValidationSeverity represents the severity level of validation issues
type ValidationSeverity string

const (
	ValidationSeverityLow      ValidationSeverity = "low"
	ValidationSeverityMedium   ValidationSeverity = "medium"
	ValidationSeverityHigh     ValidationSeverity = "high"
	ValidationSeverityCritical ValidationSeverity = "critical"
)
 