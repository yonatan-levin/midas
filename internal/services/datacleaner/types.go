package datacleaner

import (
	"context"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
)

// DataCleanerService defines the interface for the main data cleaning service
type DataCleanerService interface {
	// CleanFinancialData cleans and normalizes financial data using configured rules
	CleanFinancialData(ctx context.Context, data *entities.FinancialData) (*entities.CleaningResult, error)

	// CleanFinancialDataWithViews is the Phase 3 sibling that returns the
	// same *entities.CleaningResult plus a *cleaneddata.CleanedFinancialData
	// wrapper exposing AsReported / Restated / InvestedCapital accessors.
	// Phase 3 adds this method as ADDITIVE — every existing call site that
	// uses CleanFinancialData continues to work unchanged. Phase 4
	// consumers opt in to this method as they migrate to view-based reads.
	//
	// Returns (result, views, nil) on success; (nil, nil, err) on error.
	// The returned CleanedFinancialData wraps result.CleanedData; mutating
	// result.CleanedData after this call invalidates the view cache.
	CleanFinancialDataWithViews(ctx context.Context, data *entities.FinancialData) (*entities.CleaningResult, *cleaneddata.CleanedFinancialData, error)

	// GetIndustryRules returns applicable rules for a specific industry
	GetIndustryRules(industryCode string) ([]entities.CleaningRule, error)

	// GetQualityScore calculates quality score for financial data without applying changes
	GetQualityScore(ctx context.Context, data *entities.FinancialData) (float64, error)

	// ValidateData performs basic data validation before cleaning
	ValidateData(data *entities.FinancialData) error
}
