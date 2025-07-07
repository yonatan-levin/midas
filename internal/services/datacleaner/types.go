package datacleaner

import (
	"context"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// DataCleanerService defines the interface for the main data cleaning service
type DataCleanerService interface {
	// CleanFinancialData cleans and normalizes financial data using configured rules
	CleanFinancialData(ctx context.Context, data *entities.FinancialData) (*entities.CleaningResult, error)

	// GetIndustryRules returns applicable rules for a specific industry
	GetIndustryRules(industryCode string) ([]entities.CleaningRule, error)

	// GetQualityScore calculates quality score for financial data without applying changes
	GetQualityScore(ctx context.Context, data *entities.FinancialData) (float64, error)

	// ValidateData performs basic data validation before cleaning
	ValidateData(data *entities.FinancialData) error
}
