package datafetcher

import (
	"fmt"
	"math"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// DataValidator handles validation of fetched data quality
type DataValidator struct {
	config *DataFetcherConfig
}

// DataQualityReport represents a comprehensive data quality assessment
type DataQualityReport struct {
	OverallScore    float64               `json:"overall_score"` // 0-100
	Grade           entities.QualityGrade `json:"grade"`         // A, B, C, D, F
	CriticalIssues  int                   `json:"critical_issues"`
	MajorIssues     int                   `json:"major_issues"`
	MinorIssues     int                   `json:"minor_issues"`
	ValidationLevel ValidationLevel       `json:"validation_level"`
	Validations     []ValidationResult    `json:"validations"`
	Recommendations []string              `json:"recommendations"`
	CompletedAt     time.Time             `json:"completed_at"`
}

// ValidationResult represents the result of a specific validation check
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

// ValidationCheck represents the type of validation check
type ValidationCheck string

const (
	CompletenessCheck  ValidationCheck = "completeness"
	ConsistencyCheck   ValidationCheck = "consistency"
	ReasonabilityCheck ValidationCheck = "reasonability"
	FreshnessCheck     ValidationCheck = "freshness"
	AccuracyCheck      ValidationCheck = "accuracy"
)

// ValidationSeverity represents the severity of a validation issue
type ValidationSeverity string

const (
	SeverityCritical ValidationSeverity = "critical"
	SeverityMajor    ValidationSeverity = "major"
	SeverityMinor    ValidationSeverity = "minor"
	SeverityInfo     ValidationSeverity = "info"
)

// NewDataValidator creates a new DataValidator instance
func NewDataValidator(config *DataFetcherConfig) *DataValidator {
	return &DataValidator{
		config: config,
	}
}

// ValidateDataQuality performs comprehensive data quality validation
func (dv *DataValidator) ValidateDataQuality(result *FetchResult, level ValidationLevel) (*DataQualityReport, error) {
	if result == nil {
		return nil, fmt.Errorf("fetch result cannot be nil")
	}

	report := &DataQualityReport{
		ValidationLevel: level,
		Validations:     make([]ValidationResult, 0),
		Recommendations: make([]string, 0),
		CompletedAt:     time.Now(),
	}

	// Perform validation checks based on level
	switch level {
	case ValidationCritical:
		dv.performCriticalValidations(result, report)
		fallthrough
	case ValidationStrict:
		dv.performStrictValidations(result, report)
		fallthrough
	case ValidationBasic:
		dv.performBasicValidations(result, report)
	case ValidationNone:
		// No validation required
		return report, nil
	}

	// Calculate overall score and grade
	dv.calculateQualityScore(report)

	// Generate recommendations
	dv.generateRecommendations(report)

	return report, nil
}

// performBasicValidations performs basic data validation checks
func (dv *DataValidator) performBasicValidations(result *FetchResult, report *DataQualityReport) {
	// Check financial data presence
	if result.FinancialData == nil {
		report.Validations = append(report.Validations, ValidationResult{
			CheckName:  "financial_data_presence",
			CheckType:  CompletenessCheck,
			Passed:     false,
			Severity:   SeverityCritical,
			Message:    "Financial data is missing",
			Suggestion: "Ensure SEC data source is available and ticker is valid",
		})
		report.CriticalIssues++
		return
	}

	// Validate required financial fields
	dv.validateRequiredFields(result.FinancialData, report)

	// Basic reasonability checks
	dv.validateBasicReasonability(result.FinancialData, report)
}

// performStrictValidations performs strict validation checks
func (dv *DataValidator) performStrictValidations(result *FetchResult, report *DataQualityReport) {
	if result.FinancialData == nil {
		return // Already handled in basic validation
	}

	// Data consistency checks
	dv.validateDataConsistency(result.FinancialData, report)

	// Market data validation
	if result.MarketData != nil {
		dv.validateMarketData(result.MarketData, report)
	}

	// Cross-source consistency
	dv.validateCrossSourceConsistency(result, report)
}

// performCriticalValidations performs critical validation checks
func (dv *DataValidator) performCriticalValidations(result *FetchResult, report *DataQualityReport) {
	// Data freshness validation
	dv.validateDataFreshness(result, report)

	// Advanced reasonability checks
	if result.FinancialData != nil {
		dv.validateAdvancedReasonability(result.FinancialData, report)
	}

	// Macro data consistency
	if result.MacroData != nil {
		dv.validateMacroData(result.MacroData, report)
	}
}

// validateRequiredFields checks if all required fields are present and valid
func (dv *DataValidator) validateRequiredFields(data *entities.FinancialData, report *DataQualityReport) {
	requiredFields := map[string]interface{}{
		"ticker":             data.Ticker,
		"total_assets":       data.TotalAssets,
		"revenue":            data.Revenue,
		"shares_outstanding": data.SharesOutstanding,
	}

	for fieldName, value := range requiredFields {
		if dv.isFieldEmpty(value) {
			report.Validations = append(report.Validations, ValidationResult{
				CheckName:   fmt.Sprintf("required_field_%s", fieldName),
				CheckType:   CompletenessCheck,
				Passed:      false,
				Severity:    SeverityMajor,
				Message:     fmt.Sprintf("Required field %s is missing or zero", fieldName),
				ActualValue: value,
				Suggestion:  fmt.Sprintf("Ensure %s data is available in SEC filings", fieldName),
			})
			report.MajorIssues++
		} else {
			report.Validations = append(report.Validations, ValidationResult{
				CheckName: fmt.Sprintf("required_field_%s", fieldName),
				CheckType: CompletenessCheck,
				Passed:    true,
				Severity:  SeverityInfo,
				Message:   fmt.Sprintf("Required field %s is present", fieldName),
			})
		}
	}
}

// validateBasicReasonability performs basic reasonability checks
func (dv *DataValidator) validateBasicReasonability(data *entities.FinancialData, report *DataQualityReport) {
	// Check for negative values where they shouldn't be
	checks := map[string]float64{
		"total_assets":       data.TotalAssets,
		"shares_outstanding": data.SharesOutstanding,
	}

	for fieldName, value := range checks {
		if value < 0 {
			report.Validations = append(report.Validations, ValidationResult{
				CheckName:   fmt.Sprintf("positive_value_%s", fieldName),
				CheckType:   ReasonabilityCheck,
				Passed:      false,
				Severity:    SeverityMajor,
				Message:     fmt.Sprintf("%s has negative value", fieldName),
				ActualValue: value,
				Suggestion:  "Review data source for potential parsing errors",
			})
			report.MajorIssues++
		}
	}

	// Check for extremely large values (potential parsing errors)
	if data.TotalAssets > 1e15 { // $1 quadrillion threshold
		report.Validations = append(report.Validations, ValidationResult{
			CheckName:   "reasonable_asset_size",
			CheckType:   ReasonabilityCheck,
			Passed:      false,
			Severity:    SeverityMajor,
			Message:     "Total assets value appears unreasonably large",
			ActualValue: data.TotalAssets,
			Suggestion:  "Verify data parsing and units (millions vs raw values)",
		})
		report.MajorIssues++
	}
}

// validateDataConsistency performs data consistency checks
func (dv *DataValidator) validateDataConsistency(data *entities.FinancialData, report *DataQualityReport) {
	// Assets should be greater than or equal to liabilities
	// TODO: Add TotalLiabilities field to FinancialData entity
	totalLiabilities := data.TotalDebt // Use debt as proxy for now
	if totalLiabilities > 0 && data.TotalAssets < totalLiabilities {
		report.Validations = append(report.Validations, ValidationResult{
			CheckName:   "assets_vs_liabilities",
			CheckType:   ConsistencyCheck,
			Passed:      false,
			Severity:    SeverityMajor,
			Message:     "Total assets less than total liabilities",
			ActualValue: fmt.Sprintf("Assets: %.0f, Liabilities: %.0f", data.TotalAssets, totalLiabilities),
			Suggestion:  "Review balance sheet data for completeness",
		})
		report.MajorIssues++
	}

	// Revenue should have some relation to assets for most companies
	if data.Revenue > 0 && data.TotalAssets > 0 {
		assetTurnover := data.Revenue / data.TotalAssets
		if assetTurnover > 10 {
			report.Validations = append(report.Validations, ValidationResult{
				CheckName:   "asset_turnover_reasonability",
				CheckType:   ReasonabilityCheck,
				Passed:      false,
				Severity:    SeverityMinor,
				Message:     "Asset turnover ratio appears unusually high",
				ActualValue: assetTurnover,
				Suggestion:  "Consider industry context - some sectors have naturally high turnover",
			})
			report.MinorIssues++
		}
	}
}

// validateMarketData validates market data quality
func (dv *DataValidator) validateMarketData(data *entities.MarketData, report *DataQualityReport) {
	// Check share price reasonability
	if data.SharePrice <= 0 {
		report.Validations = append(report.Validations, ValidationResult{
			CheckName:   "positive_share_price",
			CheckType:   ReasonabilityCheck,
			Passed:      false,
			Severity:    SeverityMajor,
			Message:     "Share price is zero or negative",
			ActualValue: data.SharePrice,
			Suggestion:  "Verify ticker symbol and market data source",
		})
		report.MajorIssues++
	}

	// Check beta reasonability (typically between -3 and 3)
	if math.Abs(data.Beta) > 5 {
		report.Validations = append(report.Validations, ValidationResult{
			CheckName:   "reasonable_beta",
			CheckType:   ReasonabilityCheck,
			Passed:      false,
			Severity:    SeverityMinor,
			Message:     "Beta value appears extreme",
			ActualValue: data.Beta,
			Suggestion:  "Consider using industry-average beta if individual beta is unreliable",
		})
		report.MinorIssues++
	}
}

// validateCrossSourceConsistency validates consistency across data sources
func (dv *DataValidator) validateCrossSourceConsistency(result *FetchResult, report *DataQualityReport) {
	// This would check consistency between financial and market data
	// For example, market cap vs shares outstanding and price
	if result.FinancialData != nil && result.MarketData != nil {
		if result.FinancialData.SharesOutstanding > 0 && result.MarketData.SharePrice > 0 {
			marketCap := result.FinancialData.SharesOutstanding * result.MarketData.SharePrice
			// TODO: Add market cap consistency checks
			_ = marketCap
		}
	}
}

// validateDataFreshness checks how fresh the data is
func (dv *DataValidator) validateDataFreshness(result *FetchResult, report *DataQualityReport) {
	// Check data age for each source
	for source, sourceInfo := range result.SourceMetadata {
		if sourceInfo.DataAge > 90*24*time.Hour { // 90 days
			report.Validations = append(report.Validations, ValidationResult{
				CheckName:   fmt.Sprintf("data_freshness_%s", source),
				CheckType:   FreshnessCheck,
				Passed:      false,
				Severity:    SeverityMinor,
				Message:     fmt.Sprintf("Data from %s is older than 90 days", source),
				ActualValue: sourceInfo.DataAge,
				Suggestion:  "Consider data staleness in valuation analysis",
			})
			report.MinorIssues++
		}
	}
}

// validateAdvancedReasonability performs advanced reasonability checks
func (dv *DataValidator) validateAdvancedReasonability(data *entities.FinancialData, report *DataQualityReport) {
	// Check for unusual financial ratios that might indicate data quality issues

	// Debt-to-assets ratio check
	if data.TotalDebt > 0 && data.TotalAssets > 0 {
		debtRatio := data.TotalDebt / data.TotalAssets
		if debtRatio > 0.95 {
			report.Validations = append(report.Validations, ValidationResult{
				CheckName:   "debt_ratio_extreme",
				CheckType:   ReasonabilityCheck,
				Passed:      false,
				Severity:    SeverityMinor,
				Message:     "Debt-to-assets ratio is extremely high",
				ActualValue: debtRatio,
				Suggestion:  "Review capital structure and potential distress indicators",
			})
			report.MinorIssues++
		}
	}
}

// validateMacroData validates macro economic data
func (dv *DataValidator) validateMacroData(data *entities.MacroData, report *DataQualityReport) {
	// Check risk-free rate reasonability
	if data.RiskFreeRate < 0 || data.RiskFreeRate > 0.20 {
		report.Validations = append(report.Validations, ValidationResult{
			CheckName:   "reasonable_risk_free_rate",
			CheckType:   ReasonabilityCheck,
			Passed:      false,
			Severity:    SeverityMinor,
			Message:     "Risk-free rate appears outside normal range",
			ActualValue: data.RiskFreeRate,
			Suggestion:  "Verify macro data source and consider using alternative rate",
		})
		report.MinorIssues++
	}
}

// isFieldEmpty checks if a field is empty or zero
func (dv *DataValidator) isFieldEmpty(value interface{}) bool {
	switch v := value.(type) {
	case string:
		return v == ""
	case float64:
		return v == 0
	case int64:
		return v == 0
	case int:
		return v == 0
	default:
		return value == nil
	}
}

// calculateQualityScore calculates the overall quality score
func (dv *DataValidator) calculateQualityScore(report *DataQualityReport) {
	baseScore := 100.0

	// Deduct points based on issues
	penalty := float64(report.CriticalIssues*30 + report.MajorIssues*15 + report.MinorIssues*5)
	score := baseScore - penalty

	if score < 0 {
		score = 0
	}

	report.OverallScore = score
	report.Grade = entities.GetQualityGrade(score)
}

// generateRecommendations generates quality improvement recommendations
func (dv *DataValidator) generateRecommendations(report *DataQualityReport) {
	if report.CriticalIssues > 0 {
		report.Recommendations = append(report.Recommendations,
			"Critical data quality issues detected - review data sources and parsing logic")
	}

	if report.MajorIssues > 0 {
		report.Recommendations = append(report.Recommendations,
			"Major data quality concerns found - consider data cleaning before analysis")
	}

	if report.MinorIssues > 5 {
		report.Recommendations = append(report.Recommendations,
			"Multiple minor issues detected - review overall data collection process")
	}

	if report.OverallScore < 70 {
		report.Recommendations = append(report.Recommendations,
			"Data quality score is below acceptable threshold - consider additional validation")
	}
}
