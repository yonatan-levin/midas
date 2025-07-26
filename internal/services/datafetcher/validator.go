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

// NewDataValidator creates a new DataValidator instance
func NewDataValidator(config *DataFetcherConfig) *DataValidator {
	return &DataValidator{
		config: config,
	}
}

// ValidateDataQuality performs comprehensive validation of fetch results
func (dv *DataValidator) ValidateDataQuality(result *entities.FetchResult, level entities.ValidationLevel) (*entities.DataQualityReport, error) {
	if result == nil {
		return nil, fmt.Errorf("fetch result cannot be nil")
	}

	report := &entities.DataQualityReport{
		ValidationLevel: level,
		Validations:     make([]entities.ValidationResult, 0),
		Recommendations: make([]string, 0),
		CompletedAt:     time.Now(),
	}

	// Perform validations based on level
	switch level {
	case entities.ValidationCritical:
		dv.performCriticalValidations(result, report)
		fallthrough
	case entities.ValidationStrict:
		dv.performStrictValidations(result, report)
		fallthrough
	case entities.ValidationBasic:
		dv.performBasicValidations(result, report)
	case entities.ValidationNone:
		// No validation needed
		return report, nil
	}

	// Calculate overall quality score and grade
	dv.calculateQualityScore(report)
	dv.generateRecommendations(report)

	return report, nil
}

// performBasicValidations performs fundamental data checks
func (dv *DataValidator) performBasicValidations(result *entities.FetchResult, report *entities.DataQualityReport) {
	if result.FinancialData != nil {
	dv.validateRequiredFields(result.FinancialData, report)
	dv.validateBasicReasonability(result.FinancialData, report)
}

	if result.MarketData != nil {
		dv.validateMarketData(result.MarketData, report)
	}

	if result.MacroData != nil {
		dv.validateMacroData(result.MacroData, report)
	}
}

// performStrictValidations performs enhanced data quality checks
func (dv *DataValidator) performStrictValidations(result *entities.FetchResult, report *entities.DataQualityReport) {
	if result.FinancialData != nil {
		dv.validateDataConsistency(result.FinancialData, report)
		dv.validateAdvancedReasonability(result.FinancialData, report)
	}

	// Cross-source validation
	dv.validateCrossSourceConsistency(result, report)
	dv.validateDataFreshness(result, report)
}

// performCriticalValidations performs the most rigorous data checks
func (dv *DataValidator) performCriticalValidations(result *entities.FetchResult, report *entities.DataQualityReport) {
	// Additional critical validations would go here
	// For now, includes all strict validations
}

// validateRequiredFields checks for presence of essential data fields
func (dv *DataValidator) validateRequiredFields(data *entities.FinancialData, report *entities.DataQualityReport) {
	requiredFields := map[string]interface{}{
		"TotalAssets":       data.TotalAssets,
		"Revenue":           data.Revenue,
		"SharesOutstanding": data.SharesOutstanding,
		"Ticker":            data.Ticker,
	}

	for fieldName, fieldValue := range requiredFields {
		validation := entities.ValidationResult{
			CheckName: fmt.Sprintf("required_field_%s", fieldName),
			CheckType: entities.ValidationCheckRequiredField,
			Passed:    !dv.isFieldEmpty(fieldValue),
			Severity:  entities.ValidationSeverityCritical,
		}

		if validation.Passed {
			validation.Message = fmt.Sprintf("Required field '%s' is present", fieldName)
		} else {
			validation.Message = fmt.Sprintf("Required field '%s' is missing or empty", fieldName)
			validation.Suggestion = fmt.Sprintf("Ensure '%s' data is available from data sources", fieldName)
			report.CriticalIssues++
		}

		report.Validations = append(report.Validations, validation)
	}
}

// validateBasicReasonability performs basic sanity checks on financial data
func (dv *DataValidator) validateBasicReasonability(data *entities.FinancialData, report *entities.DataQualityReport) {
	checks := []struct {
		name      string
		condition bool
		message   string
	}{
		{
			name:      "positive_total_assets",
			condition: data.TotalAssets > 0,
			message:   "Total assets should be positive",
		},
		{
			name:      "positive_revenue",
			condition: data.Revenue >= 0,
			message:   "Revenue should be non-negative",
		},
		{
			name:      "positive_shares",
			condition: data.SharesOutstanding > 0,
			message:   "Shares outstanding should be positive",
		},
	}

	for _, check := range checks {
		validation := entities.ValidationResult{
			CheckName: check.name,
			CheckType: entities.ValidationCheckReasonability,
			Passed:    check.condition,
			Severity:  entities.ValidationSeverityHigh,
			Message:   check.message,
		}

		if !validation.Passed {
			report.MajorIssues++
		}

		report.Validations = append(report.Validations, validation)
	}
}

// validateDataConsistency checks for internal consistency in financial data
func (dv *DataValidator) validateDataConsistency(data *entities.FinancialData, report *entities.DataQualityReport) {
	// Example: Check if tangible assets calculation is reasonable
	calculatedTangible := data.TotalAssets - data.Goodwill - data.OtherIntangibles
	tolerance := 0.01 // 1% tolerance

	validation := entities.ValidationResult{
		CheckName:     "tangible_assets_consistency",
		CheckType:     entities.ValidationCheckConsistency,
		Passed:        math.Abs(calculatedTangible-data.TangibleAssets)/data.TotalAssets <= tolerance,
		Severity:      entities.ValidationSeverityMedium,
		Message:       "Tangible assets should equal total assets minus intangibles",
		ExpectedValue: calculatedTangible,
		ActualValue:   data.TangibleAssets,
	}

	if !validation.Passed {
		validation.Suggestion = "Verify tangible asset calculation or data source integrity"
		report.MajorIssues++
	}

	report.Validations = append(report.Validations, validation)
}

// validateMarketData validates market data quality
func (dv *DataValidator) validateMarketData(data *entities.MarketData, report *entities.DataQualityReport) {
	checks := []struct {
		name      string
		condition bool
		message   string
		severity  entities.ValidationSeverity
	}{
		{
			name:      "positive_price",
			condition: data.SharePrice > 0,
			message:   "Stock price should be positive",
			severity:  entities.ValidationSeverityCritical,
		},
		{
			name:      "reasonable_beta",
			condition: data.Beta >= 0 && data.Beta <= 5.0,
			message:   "Beta should be between 0 and 5",
			severity:  entities.ValidationSeverityMedium,
		},
	}

	for _, check := range checks {
		validation := entities.ValidationResult{
			CheckName: check.name,
			CheckType: entities.ValidationCheckReasonability,
			Passed:    check.condition,
			Severity:  check.severity,
			Message:   check.message,
		}

		if !validation.Passed {
			if check.severity == entities.ValidationSeverityCritical {
				report.CriticalIssues++
			} else {
				report.MajorIssues++
			}
		}

		report.Validations = append(report.Validations, validation)
	}
}

// validateCrossSourceConsistency validates consistency across different data sources
func (dv *DataValidator) validateCrossSourceConsistency(result *entities.FetchResult, report *entities.DataQualityReport) {
	// Example validation: market cap consistency
	if result.FinancialData != nil && result.MarketData != nil {
		marketCap := result.MarketData.SharePrice * float64(result.FinancialData.SharesOutstanding)
		// Add validation logic here
		_ = marketCap // Prevent unused variable error
	}
}

// validateDataFreshness checks if data is recent enough
func (dv *DataValidator) validateDataFreshness(result *entities.FetchResult, report *entities.DataQualityReport) {
	maxAge := 7 * 24 * time.Hour // 7 days

	for source, metadata := range result.SourceMetadata {
		age := time.Since(metadata.FetchTime)
		validation := entities.ValidationResult{
			CheckName:     fmt.Sprintf("freshness_%s", source),
			CheckType:     entities.ValidationCheckFreshness,
			Passed:        age <= maxAge,
			Severity:      entities.ValidationSeverityMedium,
			Message:       fmt.Sprintf("Data from %s should be fresh", source),
			ExpectedValue: maxAge,
			ActualValue:   age,
		}

		if !validation.Passed {
			validation.Suggestion = fmt.Sprintf("Refresh data from %s source", source)
			report.MinorIssues++
		}

		report.Validations = append(report.Validations, validation)
	}
}

// validateAdvancedReasonability performs sophisticated reasonability checks
func (dv *DataValidator) validateAdvancedReasonability(data *entities.FinancialData, report *entities.DataQualityReport) {
	// Example: Asset turnover ratio check
	if data.Revenue > 0 && data.TotalAssets > 0 {
		assetTurnover := data.Revenue / data.TotalAssets
		validation := entities.ValidationResult{
			CheckName:   "asset_turnover_reasonability",
			CheckType:   entities.ValidationCheckReasonability,
			Passed:      assetTurnover >= 0.1 && assetTurnover <= 10.0,
			Severity:    entities.ValidationSeverityLow,
			Message:     "Asset turnover should be within reasonable bounds",
			ActualValue: assetTurnover,
		}

		if !validation.Passed {
			validation.Suggestion = "Review revenue or asset figures for accuracy"
			report.MinorIssues++
		}

		report.Validations = append(report.Validations, validation)
	}
}

// validateMacroData validates macro economic data
func (dv *DataValidator) validateMacroData(data *entities.MacroData, report *entities.DataQualityReport) {
	validation := entities.ValidationResult{
		CheckName:   "risk_free_rate_reasonability",
		CheckType:   entities.ValidationCheckReasonability,
		Passed:      data.RiskFreeRate >= 0 && data.RiskFreeRate <= 0.20, // 0-20%
		Severity:    entities.ValidationSeverityMedium,
		Message:     "Risk-free rate should be between 0% and 20%",
			ActualValue: data.RiskFreeRate,
	}

	if !validation.Passed {
		validation.Suggestion = "Verify macro data source accuracy"
		report.MajorIssues++
	}

	report.Validations = append(report.Validations, validation)
}

// isFieldEmpty checks if a field value is considered empty
func (dv *DataValidator) isFieldEmpty(value interface{}) bool {
	switch v := value.(type) {
	case string:
		return v == ""
	case int, int32, int64:
		return v == 0
	case float32, float64:
		return v == 0.0
	case bool:
		return false // Boolean values are never considered empty
	case nil:
		return true
	default:
		return false
	}
}

// calculateQualityScore computes an overall quality score
func (dv *DataValidator) calculateQualityScore(report *entities.DataQualityReport) {
	totalChecks := len(report.Validations)

	if totalChecks == 0 {
		report.OverallScore = 100.0
		report.Grade = entities.GradeA
		return
	}

	// Weight different issue types
	weightedIssues := float64(report.CriticalIssues)*3.0 + float64(report.MajorIssues)*2.0 + float64(report.MinorIssues)*1.0
	maxPossibleIssues := float64(totalChecks) * 3.0

	score := 100.0 * (1.0 - weightedIssues/maxPossibleIssues)
	if score < 0 {
		score = 0
	}

	report.OverallScore = score

	// Assign grade based on score
	switch {
	case score >= 90:
		report.Grade = entities.GradeA
	case score >= 80:
		report.Grade = entities.GradeB
	case score >= 70:
		report.Grade = entities.GradeC
	case score >= 60:
		report.Grade = entities.GradeD
	default:
		report.Grade = entities.GradeF
	}
}

// generateRecommendations provides actionable recommendations
func (dv *DataValidator) generateRecommendations(report *entities.DataQualityReport) {
	if report.CriticalIssues > 0 {
		report.Recommendations = append(report.Recommendations,
			"Address critical data quality issues before using data for analysis")
	}

	if report.MajorIssues > 3 {
		report.Recommendations = append(report.Recommendations,
			"Multiple major issues detected - consider reviewing data sources")
	}

	if report.OverallScore < 70 {
		report.Recommendations = append(report.Recommendations,
			"Overall data quality is below acceptable threshold - manual review recommended")
	}
}
