package sec

import (
	"context"
	"fmt"
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

// classifyEmptyParseError chooses between ErrForeignPrivateIssuer and
// ErrCompanyFactsNotFound based on which taxonomies the SEC response carried.
// Called when ParseFinancialData extracted zero usable periods.
//
// The heuristic is intentionally simple: a response that contains
// `ifrs-full` (or `ifrs`) but no `us-gaap` taxonomy is almost certainly a
// foreign private issuer's 20-F filing — the data exists, our parser just
// can't read it yet. Anything else (no `us-gaap` and no IFRS, or `us-gaap`
// present but every period missing Revenue/OperatingIncome) falls back to
// the existing ErrCompanyFactsNotFound classification used by the valuation
// service for clinical-stage biotechs and pre-revenue companies.
//
// Once Phase B of the IFRS-FPI support spec ships (see
// docs/refactoring/ifrs-foreign-private-issuer-support-spec.md) the parser
// will successfully extract IFRS data and this branch will only fire for
// taxonomies still outside our coverage (JGAAP, K-IFRS).
func classifyEmptyParseError(facts *ports.SECCompanyFacts) error {
	hasUSGAAP := false
	hasIFRS := false
	for taxonomy := range facts.Facts {
		switch taxonomy {
		case "us-gaap":
			hasUSGAAP = true
		case "ifrs-full", "ifrs":
			hasIFRS = true
		}
	}
	if !hasUSGAAP && hasIFRS {
		return fmt.Errorf("%w: SEC filing uses ifrs-full taxonomy (likely Form 20-F)", ports.ErrForeignPrivateIssuer)
	}
	return fmt.Errorf("%w: no periods with usable US-GAAP financials", ports.ErrCompanyFactsNotFound)
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
		Ticker:      "", // Will be set by the caller
		CompanyName: facts.EntityName,
		Data:        make(map[string]*entities.FinancialData),
	}

	// Extract data by fiscal periods. extractFiscalPeriods only fails when it
	// produced zero usable periods, which for a filer whose response carries
	// no readable currency-denominated facts (or no dei shares) ends up at
	// the same outcome as the late-failure path below — so we route both
	// through classifyEmptyParseError so the taxonomy-based classification
	// fires regardless of WHICH layer ran out of data first.
	//
	// Phase B5: extractFiscalPeriods now reads any 3-letter ISO-4217 currency
	// unit (TWD, EUR, CNY, JPY, …) so foreign private issuers reporting in
	// non-USD currencies are extracted into FinancialData with a
	// `ReportingCurrency` stamp. Phase B9 will FX-convert monetary fields;
	// for now ReportingCurrency is metadata only.
	periods, err := p.extractFiscalPeriods(facts)
	if err != nil {
		return nil, classifyEmptyParseError(facts)
	}

	p.logger.Debug("Extracted fiscal periods", zap.Int("period_count", len(periods)))

	// Parse each period
	for period, payload := range periods {
		financialData, err := p.parsePeriodData(facts.CIK.String(), period, payload)
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
		// Choose between ErrForeignPrivateIssuer and ErrCompanyFactsNotFound
		// based on which taxonomies the SEC response carried, so the HTTP
		// layer can emit a tailored 422 instead of the misleading
		// "INSUFFICIENT_DATA" message that gets emitted for both.
		return nil, classifyEmptyParseError(facts)
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

// periodPayload is the per-period bag accumulated by extractFiscalPeriods.
//
// Phase B5 introduced this struct (replacing the earlier
// `map[string]map[string]float64` shape) so the parser can capture which
// currency a period's monetary facts were reported in WITHOUT smuggling
// the currency code through the float64 values map. The `currency` field
// is stamped from the SEC `Units` key (an ISO-4217 code: USD, TWD, EUR,
// CNY, JPY, …) the first time a currency-denominated fact lands in this
// period. `values` continues to hold both monetary facts and dimensionless
// facts (shares, period metadata) — exactly as before — so all downstream
// findValue lookups keep working unchanged.
//
// Calculation-safety contract: SharesOutstanding /
// DilutedSharesOutstanding flow through `values` from `Units["shares"]`
// and are NEVER tagged with a currency. Phase B9 will FX-convert monetary
// fields; this struct gives that future converter the metadata it needs
// without requiring a second pass over the source facts.
type periodPayload struct {
	values   map[string]float64
	currency string
	// currencyCounts tracks how many facts landed in each currency for
	// this period. Used to pick the dominant currency when a period
	// (rarely) carries facts in multiple currencies — typically a
	// corporate-action artifact such as a mid-year reporting-currency
	// change. Not exposed to callers.
	currencyCounts map[string]int
}

// isCurrencyUnit reports whether the given SEC `Units` key is an
// ISO-4217 currency code (3 uppercase ASCII letters).
//
// This filter is intentionally strict: it accepts USD/TWD/EUR/JPY/… and
// rejects everything else (`shares`, `pure`, `decimal`, `USD/shares`,
// `Year`, …). Per-share metrics like `USD/shares` are intentionally
// dropped here — we already extract dividends-per-share via dedicated
// US-GAAP / IFRS concepts and the parser does not (yet) consume any
// other per-share XBRL facts.
func isCurrencyUnit(unit string) bool {
	if len(unit) != 3 {
		return false
	}
	for _, r := range unit {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// extractFiscalPeriods extracts data organized by fiscal periods from the nested
// SEC Company Facts structure: taxonomy -> concept -> factGroup -> units -> facts.
//
// Phase B5: iterates ALL ISO-4217 currency unit keys (not only "USD") so
// foreign private issuers reporting in TWD/EUR/CNY/JPY/… are no longer
// silently dropped. Each period stamps its `currency` field from the
// first currency-unit fact that lands; if multiple currencies are seen
// in a single period we pick the one with the most fact entries and log
// a warning (rare — typically a corporate-action artifact).
//
// Dimensionless `shares` facts continue to be processed via the same
// code path as before — they NEVER touch the currency stamp, preserving
// the calculation-safety guarantee that share counts cannot be
// FX-converted by downstream layers (see Phase B9 currency-conversion plan).
func (p *Parser) extractFiscalPeriods(facts *ports.SECCompanyFacts) (map[string]*periodPayload, error) {
	periods := make(map[string]*periodPayload)

	// Iterate through taxonomy namespaces (e.g., "dei", "us-gaap", "ifrs-full")
	for taxonomy, concepts := range facts.Facts {
		p.logger.Debug("Processing taxonomy",
			zap.String("taxonomy", taxonomy),
			zap.Int("concept_count", len(concepts)))

		// Iterate through concepts within this taxonomy (e.g., "Assets", "Revenues")
		for conceptName, factGroup := range concepts {
			// Process every currency-denominated unit key (USD, TWD, EUR, CNY, …).
			// Non-currency keys other than "shares" are skipped — `pure`,
			// `decimal`, `USD/shares`, etc. are either non-financial or
			// per-share metrics we intentionally don't ingest here.
			for unit, factList := range factGroup.Units {
				switch {
				case isCurrencyUnit(unit):
					p.processFacts(periods, conceptName, factList, unit)
				case unit == "shares":
					// Dimensionless — no currency stamp.
					p.processFacts(periods, conceptName, factList, "")
				}
			}
		}
	}

	if len(periods) == 0 {
		return nil, fmt.Errorf("no financial periods extracted from SEC data")
	}

	// Resolve the dominant currency for each period. In the overwhelmingly
	// common case there is exactly one currency per period and this is a
	// no-op; the multi-currency branch only fires for corporate-action
	// edge cases (e.g., a mid-year reporting-currency change) and emits a
	// structured warning so operators can investigate.
	for periodKey, payload := range periods {
		if len(payload.currencyCounts) <= 1 {
			continue
		}
		dominantCurrency := payload.currency
		dominantCount := payload.currencyCounts[dominantCurrency]
		seen := make([]string, 0, len(payload.currencyCounts))
		for c, n := range payload.currencyCounts {
			seen = append(seen, c)
			if n > dominantCount {
				dominantCurrency = c
				dominantCount = n
			}
		}
		p.logger.Warn("Period has facts in multiple currencies; picking dominant",
			zap.String("period", periodKey),
			zap.Strings("currencies_seen", seen),
			zap.String("currency_chosen", dominantCurrency))
		payload.currency = dominantCurrency
	}

	return periods, nil
}

// processFacts processes individual facts and organizes them by fiscal periods.
//
// `currencyUnit` is the ISO-4217 code for currency-denominated facts (USD,
// TWD, …) or the empty string for dimensionless facts (shares). The first
// non-empty currencyUnit observed for a period stamps the period's currency;
// subsequent currency observations bump per-currency counters so
// extractFiscalPeriods can resolve the dominant currency at the end.
func (p *Parser) processFacts(periods map[string]*periodPayload, conceptName string, facts []ports.SECFact, currencyUnit string) {
	for _, fact := range facts {
		// Create period key (e.g., "2023FY", "2023Q4")
		periodKey := fmt.Sprintf("%d%s", fact.Fy, fact.Fp)

		// Initialize period payload if needed
		payload, exists := periods[periodKey]
		if !exists {
			payload = &periodPayload{
				values:         make(map[string]float64),
				currencyCounts: make(map[string]int),
			}
			periods[periodKey] = payload
		}

		// Store the value using the local concept name
		payload.values[conceptName] = fact.Val

		// Track currency for monetary facts only — `shares` and other
		// dimensionless facts pass currencyUnit="" and must NOT influence
		// the currency stamp (calculation-safety contract).
		if currencyUnit != "" {
			if payload.currency == "" {
				payload.currency = currencyUnit
			}
			payload.currencyCounts[currencyUnit]++
		}

		// Also store metadata for the most recent fact in this period
		if _, exists := payload.values["_filing_date"]; !exists {
			if filingDate, err := time.Parse("2006-01-02", fact.Filed); err == nil {
				payload.values["_filing_date"] = float64(filingDate.Unix())
			}
		}
		if _, exists := payload.values["_end_date"]; !exists {
			if endDate, err := time.Parse("2006-01-02", fact.End); err == nil {
				payload.values["_end_date"] = float64(endDate.Unix())
			}
		}
	}
}

// parsePeriodData converts raw period data to FinancialData entity.
//
// Phase B5: signature now takes a *periodPayload so the per-period
// `currency` stamp can be propagated to FinancialData.ReportingCurrency.
// Empty currency defaults to "USD" for backward compatibility (e.g., a
// period that contains only `shares` facts).
//
// Phase B6: lookup tables now include IFRS-full equivalents AFTER the
// existing US-GAAP names — preserving identical priority order for
// domestic 10-K filers while enabling 20-F filers to be parsed.
func (p *Parser) parsePeriodData(cik, period string, payload *periodPayload) (*entities.FinancialData, error) {
	if payload == nil {
		return nil, fmt.Errorf("period payload cannot be nil")
	}
	data := payload.values

	filingDate := time.Unix(int64(data["_filing_date"]), 0)
	endDate := time.Unix(int64(data["_end_date"]), 0)

	// Default empty currency to USD — preserves backward compat for periods
	// where every fact happens to be dimensionless (purely shares-only) and
	// for legacy callers that haven't been wired through Phase B5 yet.
	reportingCurrency := payload.currency
	if reportingCurrency == "" {
		reportingCurrency = "USD"
	}

	financialData := &entities.FinancialData{
		CIK:               cik,
		FilingPeriod:      period,
		FilingDate:        filingDate,
		AsOf:              endDate,
		ReportingCurrency: reportingCurrency,
	}

	var missingFields []string

	// Extract income statement items.
	// Order: US-GAAP first (preserving domestic-filer priority), then IFRS-full.
	if val, exists := p.findValue(data, []string{
		"OperatingIncomeLoss",
		"IncomeLossFromContinuingOperationsBeforeIncomeTaxesExtraordinaryItemsNoncontrollingInterest",
		"IncomeLossFromContinuingOperationsBeforeIncomeTaxes",
		// IFRS-full equivalents (Phase B6).
		"ProfitLossFromOperatingActivities",
		"ProfitBeforeTax",
	}); exists {
		financialData.OperatingIncome = val
	} else {
		missingFields = append(missingFields, "operating_income")
	}

	if val, exists := p.findValue(data, []string{
		"Revenues",
		"RevenueFromContractWithCustomerExcludingAssessedTax",
		"SalesRevenueNet",
		// IFRS-full equivalents (Phase B6).
		"Revenue",
		"RevenueFromContractsWithCustomers",
	}); exists {
		financialData.Revenue = val
	} else {
		missingFields = append(missingFields, "revenue")
	}

	if val, exists := p.findValue(data, []string{
		"InterestExpense",
		"InterestExpenseDebt",
		// IFRS-full equivalent (Phase B6).
		"FinanceCosts",
	}); exists {
		financialData.InterestExpense = val
	}

	// Net income (for DDM and FFO models)
	if val, exists := p.findValue(data, []string{
		"NetIncomeLoss",
		"ProfitLoss",
		"NetIncomeLossAvailableToCommonStockholdersBasic",
		// IFRS-full equivalent (Phase B6) — `ProfitLoss` already on the list
		// above is also valid IFRS-full; `ProfitLossAttributableToOwnersOfParent`
		// is the parent-only flavor that we prefer when present.
		"ProfitLossAttributableToOwnersOfParent",
	}); exists {
		financialData.NetIncome = val
	}

	// Dividends per share (for DDM model)
	if val, exists := p.findValue(data, []string{
		"CommonStockDividendsPerShareDeclared",
		"CommonStockDividendsPerShareCashPaid",
	}); exists {
		financialData.DividendsPerShare = val
	}

	// Gain/loss on sale of properties (for REIT FFO calculation)
	if val, exists := p.findValue(data, []string{
		"GainLossOnSaleOfProperties",
		"GainLossOnSaleOfPropertyPlantEquipment",
		"GainsLossesOnSalesOfInvestmentRealEstate",
	}); exists {
		financialData.GainOnPropertySales = val
	}

	// Cash flow statement items (for true FCF calculation)
	if val, exists := p.findValue(data, []string{
		"DepreciationDepletionAndAmortization",
		"DepreciationAndAmortization",
		"Depreciation",
		// IFRS-full equivalents (Phase B6).
		"DepreciationAndAmortisationExpense",
		"DepreciationAmortisationAndImpairmentLossReversalOfImpairmentLossRecognisedInProfitOrLoss",
	}); exists {
		financialData.DepreciationAndAmortization = val
	}

	if val, exists := p.findValue(data, []string{
		"PaymentsToAcquirePropertyPlantAndEquipment",
		"PaymentsToAcquireProductiveAssets",
		// IFRS-full equivalent (Phase B6).
		"PurchaseOfPropertyPlantAndEquipmentClassifiedAsInvestingActivities",
	}); exists {
		financialData.CapitalExpenditures = val
	}

	if val, exists := p.findValue(data, []string{
		"NetCashProvidedByOperatingActivities",
		"CashProvidedByOperatingActivities",
		// IFRS-full equivalent (Phase B6).
		"CashFlowsFromUsedInOperatingActivities",
	}); exists {
		financialData.OperatingCashFlow = val
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
		"AssetsCurrent",
		// IFRS-full equivalent (Phase B6).
		"CurrentAssets",
	}); exists {
		financialData.CurrentAssets = val
	}

	if val, exists := p.findValue(data, []string{
		"LiabilitiesCurrent",
		// IFRS-full equivalent (Phase B6).
		"CurrentLiabilities",
	}); exists {
		financialData.CurrentLiabilities = val
	}

	if val, exists := p.findValue(data, []string{
		"CashAndCashEquivalentsAtCarryingValue",
		"CashCashEquivalentsAndShortTermInvestments",
		"Cash",
		// IFRS-full equivalent (Phase B6) — `Cash` already on the list above
		// is also valid IFRS-full; `CashAndCashEquivalents` is the standard
		// IFRS-full balance-sheet line.
		"CashAndCashEquivalents",
	}); exists {
		financialData.CashAndCashEquivalents = val
	}

	// Stockholders' equity (for ROIC / invested capital)
	if val, exists := p.findValue(data, []string{
		"StockholdersEquity",
		"StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest",
		// IFRS-full equivalents (Phase B6).
		"Equity",
		"EquityAttributableToOwnersOfParent",
	}); exists {
		financialData.StockholdersEquity = val
	}

	// Minority (non-controlling) interest — subtracted from EV→equity bridge
	// so per-share value reflects only the parent's claim. M-1d: see
	// docs/reviewer/M1-growth-and-model-selection-traces-missing-ticker.md.
	if val, exists := p.findValue(data, []string{
		"MinorityInterest",
		"MinorityInterestInLimitedPartnerships",
		// IFRS-full equivalent (Phase B6).
		"NoncontrollingInterests",
	}); exists {
		financialData.MinorityInterest = val
	}

	// Preferred stock par/carrying value — subtracted from EV→equity bridge
	// so per-share value reflects only common shareholders' claim. M-1d.
	if val, exists := p.findValue(data, []string{
		"PreferredStockValue",
		"PreferredStockValueOutstanding",
	}); exists {
		financialData.PreferredEquity = val
	}

	if val, exists := p.findValue(data, []string{
		"Goodwill",
		// `Goodwill` is identical between US-GAAP and IFRS-full; listed once
		// suffices and the spec confirms this — kept here as a marker.
	}); exists {
		financialData.Goodwill = val
	}

	if val, exists := p.findValue(data, []string{
		"IntangibleAssetsNetExcludingGoodwill",
		"IntangibleAssetsNet",
		// IFRS-full equivalent (Phase B6).
		"IntangibleAssetsOtherThanGoodwill",
	}); exists {
		financialData.OtherIntangibles = val
	}

	if val, exists := p.findValue(data, []string{
		"LongTermDebt",
		"LongTermDebtNoncurrent",
		"LongTermDebtCurrent",
		"LongTermDebtAndCapitalLeaseObligations",
		"DebtCurrent",
		// IFRS-full equivalents (Phase B6).
		"Borrowings",
		"NoncurrentBorrowings",
		"CurrentBorrowings",
		"LeaseLiabilities",
	}); exists {
		financialData.TotalDebt = val
		financialData.InterestBearingDebt = val // Assume all debt is interest-bearing
	}

	if val, exists := p.findValue(data, []string{
		"InventoryNet",
		"Inventory",
		// IFRS-full equivalent (Phase B6).
		"Inventories",
	}); exists {
		financialData.Inventory = val
	}

	// Deferred Tax Assets - Critical for Category A adjustments
	if val, exists := p.findValue(data, []string{
		"DeferredTaxAssetsNet",
		"DeferredIncomeTaxAssetsNet",
		// IFRS-full equivalent (Phase B6).
		"DeferredTaxAssets",
	}); exists {
		financialData.DeferredTaxAssets = val
	}

	// Operating Leases (ASC 842) - Critical for Category B adjustments
	if val, exists := p.findValue(data, []string{
		"OperatingLeaseLiability",
		"OperatingLeaseLiabilityCurrent",
		"OperatingLeaseLiabilityNoncurrent",
		// IFRS-full equivalent (Phase B6) — IFRS 16 puts all lease
		// liabilities under one umbrella tag.
		"LeaseLiabilities",
	}); exists {
		financialData.OperatingLeaseLiability = val
	}

	// Enhanced pension/benefit obligation mapping
	if val, exists := p.findValue(data, []string{
		"DefinedBenefitPlanPensionPlansProjectedBenefitObligationIncrease",
		"ProjectedBenefitObligation",
		"PensionAndOtherPostretirementBenefitPlansProjectedBenefitObligation",
		// IFRS-full equivalent (Phase B6).
		"DefinedBenefitObligationAtPresentValue",
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

	// Extract share information.
	// Order: US-GAAP `CommonStock*` first (preserving domestic-filer
	// priority), then DEI's `EntityCommonStockSharesOutstanding` which is
	// what 20-F filers (TSM, ASML, BABA, …) typically populate. DEI is a
	// dimensionless-shares concept on the cover page so it lives under
	// `Units["shares"]` regardless of the body taxonomy (US-GAAP or IFRS).
	if val, exists := p.findValue(data, []string{
		"CommonStockSharesOutstanding",
		"CommonStockSharesIssued",
		// DEI cover-page shares — primary source for IFRS/20-F filers.
		"EntityCommonStockSharesOutstanding",
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

// findValue finds a value by trying multiple possible field names
func (p *Parser) findValue(data map[string]float64, fieldNames []string) (float64, bool) {
	for _, fieldName := range fieldNames {
		if val, exists := data[fieldName]; exists {
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

// GetSupportedConcepts returns the list of SEC XBRL concepts we can parse.
//
// The list is grouped by taxonomy. US-GAAP concepts come first (preserving
// historical reporting behavior); IFRS-full concepts were added in Phase B6
// of the IFRS / foreign-private-issuer support plan
// (docs/refactoring/ifrs-foreign-private-issuer-support-spec.md) so 20-F
// filers like TSM, ASML, BABA, NVO, AZN, SAP can be parsed.
func (p *Parser) GetSupportedConcepts() []string {
	return []string{
		// ---- US-GAAP -------------------------------------------------------
		// Income Statement - Core P&L Items
		"us-gaap:OperatingIncomeLoss",
		"us-gaap:IncomeLossFromContinuingOperationsBeforeIncomeTaxes",
		"us-gaap:Revenues",
		"us-gaap:RevenueFromContractWithCustomerExcludingAssessedTax",
		"us-gaap:SalesRevenueNet",
		"us-gaap:InterestExpense",
		"us-gaap:InterestExpenseDebt",
		"us-gaap:CostOfGoodsAndServicesSold",
		"us-gaap:NetIncomeLoss",
		"us-gaap:CommonStockDividendsPerShareDeclared",
		"us-gaap:GainLossOnSaleOfProperties",
		"us-gaap:GainLossOnSaleOfPropertyPlantEquipment",

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

		// Balance Sheet - Equity bridge correction terms (M-1d)
		"us-gaap:MinorityInterest",
		"us-gaap:MinorityInterestInLimitedPartnerships",
		"us-gaap:PreferredStockValue",
		"us-gaap:PreferredStockValueOutstanding",

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

		// ---- IFRS-full (Phase B6) -------------------------------------------
		// Income Statement
		"ifrs-full:Revenue",
		"ifrs-full:RevenueFromContractsWithCustomers",
		"ifrs-full:ProfitLossFromOperatingActivities",
		"ifrs-full:ProfitBeforeTax",
		"ifrs-full:ProfitLoss",
		"ifrs-full:ProfitLossAttributableToOwnersOfParent",
		"ifrs-full:FinanceCosts",

		// Balance Sheet
		"ifrs-full:CashAndCashEquivalents",
		"ifrs-full:Cash",
		"ifrs-full:CurrentAssets",
		"ifrs-full:CurrentLiabilities",
		"ifrs-full:Goodwill",
		"ifrs-full:IntangibleAssetsOtherThanGoodwill",
		"ifrs-full:Inventories",
		"ifrs-full:DeferredTaxAssets",
		"ifrs-full:Equity",
		"ifrs-full:EquityAttributableToOwnersOfParent",
		"ifrs-full:NoncontrollingInterests",

		// Debt / lease liabilities
		"ifrs-full:Borrowings",
		"ifrs-full:NoncurrentBorrowings",
		"ifrs-full:CurrentBorrowings",
		"ifrs-full:LeaseLiabilities",

		// Cash Flow Statement
		"ifrs-full:CashFlowsFromUsedInOperatingActivities",
		"ifrs-full:PurchaseOfPropertyPlantAndEquipmentClassifiedAsInvestingActivities",
		"ifrs-full:DepreciationAndAmortisationExpense",
		"ifrs-full:DepreciationAmortisationAndImpairmentLossReversalOfImpairmentLossRecognisedInProfitOrLoss",

		// Pension
		"ifrs-full:DefinedBenefitObligationAtPresentValue",

		// TODO: Add dynamic mapping framework for future extensibility
		// This static approach should be replaced with configurable mapping
		// to support new SEC fields without code changes
	}
}
