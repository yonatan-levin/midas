package sec

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// Parser handles parsing and normalization of SEC data
type Parser struct {
	logger *zap.Logger
}

// NewParser creates a new SEC data parser
func NewParser(logger *zap.Logger) *Parser {
	return &Parser{
		logger: logger.Named("sec-parser"),
	}
}

// ParseFinancialData extracts financial data from SEC company facts
func (p *Parser) ParseFinancialData(ctx context.Context, facts *ports.SECCompanyFacts) (*entities.HistoricalFinancialData, error) {
	if facts == nil {
		return nil, fmt.Errorf("facts cannot be nil")
	}

	p.logger.Debug("Parsing financial data",
		zap.String("cik", facts.CIK.String()),
		zap.String("entity_name", facts.EntityName),
		zap.Int("fact_groups", len(facts.Facts)))

	historical := &entities.HistoricalFinancialData{
		Ticker: "", // Will be set by the caller
		Data:   make(map[string]*entities.FinancialData),
	}

	// Extract data by fiscal periods
	periods, err := p.extractFiscalPeriods(facts)
	if err != nil {
		return nil, fmt.Errorf("failed to extract fiscal periods: %w", err)
	}

	p.logger.Debug("Extracted fiscal periods", zap.Int("period_count", len(periods)))

	// Parse each period
	for period, periodData := range periods {
		financialData, err := p.parsePeriodData(facts.CIK.String(), period, periodData)
		if err != nil {
			p.logger.Warn("Failed to parse period data",
				zap.String("period", period),
				zap.Error(err))
			continue
		}

		if financialData != nil {
			historical.Data[period] = financialData
		}
	}

	if len(historical.Data) == 0 {
		return nil, fmt.Errorf("no valid financial data found")
	}

	p.logger.Info("Successfully parsed financial data",
		zap.String("cik", facts.CIK.String()),
		zap.Int("periods_parsed", len(historical.Data)))

	return historical, nil
}

// NormalizeFinancialData applies normalization rules to financial data
func (p *Parser) NormalizeFinancialData(ctx context.Context, data *entities.FinancialData) (*entities.FinancialData, error) {
	if data == nil {
		return nil, fmt.Errorf("data cannot be nil")
	}

	normalized := *data // Copy the data
	normalized.HasNormalizedData = true

	p.logger.Debug("Normalizing financial data",
		zap.String("ticker", data.Ticker),
		zap.String("period", data.FilingPeriod))

	// 1. Calculate normalized operating income
	normalizedOI := p.normalizeOperatingIncome(data.OperatingIncome)
	normalized.NormalizedOperatingIncome = normalizedOI

	// 2. Calculate tangible assets (remove goodwill and intangibles)
	tangibleAssets := data.TotalAssets - data.Goodwill - data.OtherIntangibles
	if tangibleAssets < 0 {
		tangibleAssets = 0
	}
	normalized.TangibleAssets = tangibleAssets

	// 3. Detect and write down dead inventory
	deadInventoryWritedown := p.calculateDeadInventoryWritedown(data)
	normalized.DeadInventoryWritedown = deadInventoryWritedown

	// Adjust tangible assets for dead inventory
	if deadInventoryWritedown > 0 {
		normalized.TangibleAssets -= deadInventoryWritedown
		if normalized.TangibleAssets < 0 {
			normalized.TangibleAssets = 0
		}
	}

	// 4. Validate and calculate effective tax rate
	if data.TaxRate <= 0 || data.TaxRate > 1 {
		// Use default tax rate if invalid
		normalized.TaxRate = 0.21 // 21% default corporate tax rate
		normalized.MissingFields = append(normalized.MissingFields, "tax_rate")
	}

	p.logger.Debug("Normalization completed",
		zap.String("ticker", data.Ticker),
		zap.Float64("original_operating_income", data.OperatingIncome),
		zap.Float64("normalized_operating_income", normalizedOI),
		zap.Float64("tangible_assets", tangibleAssets),
		zap.Float64("dead_inventory_writedown", deadInventoryWritedown))

	return &normalized, nil
}

// extractFiscalPeriods extracts data organized by fiscal periods
// Updated to handle nested SEC Company Facts API structure
func (p *Parser) extractFiscalPeriods(facts *ports.SECCompanyFacts) (map[string]map[string]float64, error) {
	periods := make(map[string]map[string]float64)

	// Iterate through taxonomy namespaces (e.g., "dei", "us-gaap")
	for taxonomyNamespace, taxonomyGroup := range facts.Facts {

		// TODO: Handle the real nested structure where concepts are inside taxonomy namespaces
		// For now, check if this is the old flat structure or new nested structure
		if taxonomyGroup.Units != nil {
			// Old flat structure: "us-gaap:Revenues" -> factGroup
			conceptName := taxonomyNamespace
			if colonIndex := strings.LastIndex(taxonomyNamespace, ":"); colonIndex >= 0 {
				conceptName = taxonomyNamespace[colonIndex+1:]
			}

			// Look for USD values (most common)
			if usdUnits, exists := taxonomyGroup.Units["USD"]; exists {
				p.processFacts(periods, conceptName, usdUnits)
			}

			// Also check for shares units for share count data
			if sharesUnits, exists := taxonomyGroup.Units["shares"]; exists {
				p.processFacts(periods, conceptName, sharesUnits)
			}
		} else {
			// New nested structure: taxonomy -> concepts -> units
			// For now, try to parse any available nested data using reflection
			p.logger.Debug("Attempting nested taxonomy structure parsing",
				zap.String("taxonomy", taxonomyNamespace))

			// Try to handle nested structure by checking if taxonomyGroup is a map
			if nestedFactsFound := p.tryParseNestedFacts(periods, taxonomyNamespace, taxonomyGroup); nestedFactsFound {
				p.logger.Debug("Successfully parsed nested facts",
					zap.String("taxonomy", taxonomyNamespace))
			} else {
				p.logger.Debug("No nested facts found in taxonomy",
					zap.String("taxonomy", taxonomyNamespace))
			}
		}
	}

	if len(periods) == 0 {
		return nil, fmt.Errorf("no financial periods extracted - data structure may be nested and not yet supported")
	}

	return periods, nil
}

// processFacts processes individual facts and organizes them by fiscal periods
func (p *Parser) processFacts(periods map[string]map[string]float64, conceptName string, facts []ports.SECFact) {
	for _, fact := range facts {
		// Create period key (e.g., "2023FY", "2023Q4")
		periodKey := fmt.Sprintf("%d%s", fact.Fy, fact.Fp)

		// Initialize period data if needed
		if periods[periodKey] == nil {
			periods[periodKey] = make(map[string]float64)
		}

		// Store the value using the local concept name
		periods[periodKey][conceptName] = fact.Val

		// Also store metadata for the most recent fact in this period
		if _, exists := periods[periodKey]["_filing_date"]; !exists {
			if filingDate, err := time.Parse("2006-01-02", fact.Filed); err == nil {
				periods[periodKey]["_filing_date"] = float64(filingDate.Unix())
			}
		}
		if _, exists := periods[periodKey]["_end_date"]; !exists {
			if endDate, err := time.Parse("2006-01-02", fact.End); err == nil {
				periods[periodKey]["_end_date"] = float64(endDate.Unix())
			}
		}
	}
}

// parsePeriodData converts raw period data to FinancialData entity
func (p *Parser) parsePeriodData(cik, period string, data map[string]float64) (*entities.FinancialData, error) {
	filingDate := time.Unix(int64(data["_filing_date"]), 0)
	endDate := time.Unix(int64(data["_end_date"]), 0)

	financialData := &entities.FinancialData{
		CIK:          cik,
		FilingPeriod: period,
		FilingDate:   filingDate,
		AsOf:         endDate,
	}

	var missingFields []string

	// Extract income statement items
	if val, exists := p.findValue(data, []string{
		"OperatingIncomeLoss",
		"IncomeLossFromContinuingOperationsBeforeIncomeTaxesExtraordinaryItemsNoncontrollingInterest",
		"IncomeLossFromContinuingOperationsBeforeIncomeTaxes",
	}); exists {
		financialData.OperatingIncome = val
	} else {
		missingFields = append(missingFields, "operating_income")
	}

	if val, exists := p.findValue(data, []string{
		"Revenues",
		"RevenueFromContractWithCustomerExcludingAssessedTax",
		"SalesRevenueNet",
	}); exists {
		financialData.Revenue = val
	} else {
		missingFields = append(missingFields, "revenue")
	}

	if val, exists := p.findValue(data, []string{
		"InterestExpense",
		"InterestExpenseDebt",
	}); exists {
		financialData.InterestExpense = val
	}

	// Extract balance sheet items
	if val, exists := p.findValue(data, []string{
		"Assets",
		"AssetsCurrent",
		"AssetsNoncurrent",
	}); exists {
		financialData.TotalAssets = val
	} else {
		missingFields = append(missingFields, "total_assets")
	}

	if val, exists := p.findValue(data, []string{
		"Goodwill",
	}); exists {
		financialData.Goodwill = val
	}

	if val, exists := p.findValue(data, []string{
		"IntangibleAssetsNetExcludingGoodwill",
		"IntangibleAssetsNet",
	}); exists {
		financialData.OtherIntangibles = val
	}

	if val, exists := p.findValue(data, []string{
		"LongTermDebt",
		"LongTermDebtNoncurrent",
		"LongTermDebtCurrent",
		"LongTermDebtAndCapitalLeaseObligations",
		"DebtCurrent",
	}); exists {
		financialData.TotalDebt = val
		financialData.InterestBearingDebt = val // Assume all debt is interest-bearing
	}

	if val, exists := p.findValue(data, []string{
		"InventoryNet",
		"Inventory",
	}); exists {
		financialData.Inventory = val
	}

	// Deferred Tax Assets - Critical for Category A adjustments
	if val, exists := p.findValue(data, []string{
		"DeferredTaxAssetsNet",
		"DeferredIncomeTaxAssetsNet",
	}); exists {
		financialData.DeferredTaxAssets = val
	}

	// Operating Leases (ASC 842) - Critical for Category B adjustments
	if val, exists := p.findValue(data, []string{
		"OperatingLeaseLiability",
		"OperatingLeaseLiabilityCurrent",
		"OperatingLeaseLiabilityNoncurrent",
	}); exists {
		financialData.OperatingLeaseLiability = val
	}

	// Enhanced pension/benefit obligation mapping
	if val, exists := p.findValue(data, []string{
		"DefinedBenefitPlanPensionPlansProjectedBenefitObligationIncrease",
		"ProjectedBenefitObligation",
		"PensionAndOtherPostretirementBenefitPlansProjectedBenefitObligation",
	}); exists {
		financialData.ProjectedBenefitObligation = val
	}

	if val, exists := p.findValue(data, []string{
		"DefinedBenefitPlanAssets",
		"PensionPlanAssets",
		"PensionAndOtherPostretirementDefinedBenefitPlansAssets",
	}); exists {
		financialData.PensionPlanAssets = val
	}

	// Extract share information
	if val, exists := p.findValue(data, []string{
		"CommonStockSharesOutstanding",
		"CommonStockSharesIssued",
	}); exists {
		financialData.SharesOutstanding = val
	} else {
		missingFields = append(missingFields, "shares_outstanding")
	}

	if val, exists := p.findValue(data, []string{
		"WeightedAverageNumberOfDilutedSharesOutstanding",
		"WeightedAverageNumberOfSharesOutstandingBasicAndDiluted",
		"WeightedAverageNumberOfSharesOutstandingBasic",
	}); exists {
		financialData.DilutedSharesOutstanding = val
	} else {
		// Use regular shares outstanding as fallback
		financialData.DilutedSharesOutstanding = financialData.SharesOutstanding
	}

	// Calculate inventory turnover if we have both inventory and revenue
	if financialData.Inventory > 0 && financialData.Revenue > 0 {
		financialData.InventoryTurnover = financialData.Revenue / financialData.Inventory
	}

	// Store missing fields
	if len(missingFields) > 0 {
		financialData.MissingFields = missingFields
	}

	// Validate that we have minimum required data
	if financialData.Revenue <= 0 && financialData.OperatingIncome <= 0 {
		return nil, fmt.Errorf("insufficient data: no revenue or operating income")
	}

	return financialData, nil
}

// tryParseNestedFacts attempts to parse nested fact structures using interface{} type assertion
func (p *Parser) tryParseNestedFacts(periods map[string]map[string]float64, taxonomyNamespace string, taxonomyGroup ports.SECFactGroup) bool {
	// For nested structures, we need to use JSON unmarshaling to access the nested data
	// This is a simplified approach that tries to extract common GAAP concepts

	// Since we can't directly access nested structures with the current type definition,
	// we'll implement a basic fallback that looks for commonly needed fields
	// In a real implementation, we'd need to modify the SECFactGroup structure

	// Log the attempt and return false for now, indicating we tried but couldn't parse
	p.logger.Debug("Nested structure parsing attempted but not fully implemented",
		zap.String("taxonomy", taxonomyNamespace),
		zap.String("note", "Full nested parsing requires schema updates"))

	return false
}

// findValue finds a value by trying multiple possible field names
func (p *Parser) findValue(data map[string]float64, fieldNames []string) (float64, bool) {
	for _, fieldName := range fieldNames {
		if val, exists := data[fieldName]; exists && val != 0 {
			return val, true
		}
	}
	return 0, false
}

// normalizeOperatingIncome removes non-recurring items from operating income
func (p *Parser) normalizeOperatingIncome(operatingIncome float64) float64 {
	// For now, return the operating income as-is
	// In a full implementation, this would:
	// 1. Check for non-recurring items using constants.NonRecurringItems
	// 2. Adjust the operating income accordingly
	// 3. Apply more sophisticated normalization logic

	// Basic validation
	if operatingIncome < 0 {
		// Negative operating income - return as-is but log
		return operatingIncome
	}

	return operatingIncome
}

// calculateDeadInventoryWritedown calculates writedown for dead inventory
func (p *Parser) calculateDeadInventoryWritedown(data *entities.FinancialData) float64 {
	if data.Inventory <= 0 || data.InventoryTurnover <= 0 {
		return 0
	}

	// Simple heuristic: if inventory turnover is very low (< 1), write down 40% of excess
	// This is a simplified version - a full implementation would:
	// 1. Compare to 5-year median inventory levels
	// 2. Check if turnover decreased by 25%+
	// 3. Mark excess inventory for writedown

	if data.InventoryTurnover < 1.0 {
		// Very low turnover suggests dead inventory
		excessInventory := data.Inventory * 0.5 // Assume 50% is excess
		writedown := excessInventory * 0.4      // Write down 40% of excess
		return writedown
	}

	return 0
}

// GetSupportedConcepts returns the list of SEC XBRL concepts we can parse
func (p *Parser) GetSupportedConcepts() []string {
	return []string{
		// Income Statement - Core P&L Items
		"us-gaap:OperatingIncomeLoss",
		"us-gaap:IncomeLossFromContinuingOperationsBeforeIncomeTaxes",
		"us-gaap:Revenues",
		"us-gaap:RevenueFromContractWithCustomerExcludingAssessedTax",
		"us-gaap:SalesRevenueNet",
		"us-gaap:InterestExpense",
		"us-gaap:InterestExpenseDebt",
		"us-gaap:CostOfGoodsAndServicesSold",

		// Balance Sheet - Assets
		"us-gaap:Assets",
		"us-gaap:AssetsCurrent",
		"us-gaap:AssetsNoncurrent",
		"us-gaap:Goodwill",
		"us-gaap:IntangibleAssetsNetExcludingGoodwill",
		"us-gaap:IntangibleAssetsNet",
		"us-gaap:InventoryNet",
		"us-gaap:Inventory",
		"us-gaap:DeferredTaxAssetsNet",
		"us-gaap:PropertyPlantAndEquipmentNet",

		// Balance Sheet - Liabilities & Debt
		"us-gaap:LongTermDebt",
		"us-gaap:LongTermDebtNoncurrent",
		"us-gaap:LongTermDebtCurrent",
		"us-gaap:Liabilities",
		"us-gaap:LiabilitiesCurrent",
		"us-gaap:LiabilitiesNoncurrent",

		// Operating Leases (ASC 842)
		"us-gaap:OperatingLeaseLiability",
		"us-gaap:OperatingLeaseLiabilityCurrent",
		"us-gaap:OperatingLeaseLiabilityNoncurrent",
		"us-gaap:OperatingLeaseRightOfUseAsset",

		// Pension & Benefits
		"us-gaap:DefinedBenefitPlanPensionPlansProjectedBenefitObligationIncrease",
		"us-gaap:DefinedBenefitPlanAssets",

		// Share Information
		"us-gaap:CommonStockSharesOutstanding",
		"us-gaap:CommonStockSharesIssued",
		"us-gaap:WeightedAverageNumberOfDilutedSharesOutstanding",
		"us-gaap:WeightedAverageNumberOfSharesOutstandingBasic",

		// Cash Flow Statement
		"us-gaap:CashAndCashEquivalentsAtCarryingValue",
		"us-gaap:NetCashProvidedByUsedInOperatingActivities",

		// TODO: Add dynamic mapping framework for future extensibility
		// This static approach should be replaced with configurable mapping
		// to support new SEC fields without code changes
	}
}
