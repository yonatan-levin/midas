package sec

import (
	"context"
	"fmt"
	"math"
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
// ErrCompanyFactsNotFound based on which taxonomies the SEC response
// carried. Called when ParseFinancialData extracted zero usable periods.
//
// Post-Phase-B6 invariant (see
// docs/refactoring/archive/ifrs-foreign-private-issuer-support-spec.md): the parser
// now reads IFRS-full concepts (Revenue, ProfitLossFromOperatingActivities,
// CashAndCashEquivalents, …) and stamps the period's reporting currency
// from any ISO-4217 unit key. So for the well-mapped IFRS-full filers (TSM,
// ASML, NVO, AZN, SAP, BABA, BIDU, TM, RIO, BHP, NVS, SHEL, BP, …)
// extractFiscalPeriods + parsePeriodData succeed and this function is
// NEVER called.
//
// This branch now fires ONLY when the parser could not extract ANY usable
// period AFTER reading IFRS-full. That happens in two scenarios:
//
//  1. The taxonomy IS `ifrs-full` but every concept the filer used falls
//     outside the Phase B6 mapping table — e.g., custom IFRS extensions,
//     concepts that landed under `ifrs-full` but the filer chose
//     non-standard tag names. Pinned by
//     TestParser_ParseFinancialData_ForeignPrivateIssuer_UnmappedConcepts.
//  2. The taxonomy is something else entirely — JGAAP, K-IFRS,
//     `ifrs-smes`, or future SEC additions we have not yet mapped.
//
// In both cases the data exists in the SEC response but our parser cannot
// read it; ErrForeignPrivateIssuer is the more helpful classification for
// the user (and lets the API return 422 FOREIGN_PRIVATE_ISSUER_UNSUPPORTED
// instead of the misleading INSUFFICIENT_DATA fallback).
//
// Anything else (no `us-gaap` and no IFRS, or `us-gaap` present but every
// period missing Revenue/OperatingIncome) falls through to
// ErrCompanyFactsNotFound — the same classification the valuation service
// uses for clinical-stage biotechs and pre-revenue US companies.
//
// FX-failure path (Phase B11): if the parser succeeded but the
// service-layer FX conversion failed for non-USD periods, the service
// itself maps that to ErrForeignPrivateIssuer via convertFinancialsToUSD +
// hasNonUSDPeriod. That code path does NOT come through this function.
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
	// valuesByCurrency segregates monetary fact values by the currency unit
	// they were reported in (outer key = ISO-4217 code, e.g. "USD", "TWD").
	// Phase B post-launch hotfix: filers like TSM publish the SAME concept
	// (e.g., ifrs-full:Assets) under BOTH a TWD unit AND a USD unit. Without
	// segregation, the second iteration silently overwrites the first
	// (Go map iteration order is randomized), producing periods stamped
	// with one currency but holding values from the other — which then
	// FX-converts incorrectly. Once the dominant currency is resolved, the
	// matching bucket is collapsed into `values` and the others are
	// discarded so downstream findValue lookups see only currency-coherent
	// data.
	//
	// Dimensionless facts (shares, metadata) write directly to `values`
	// and are never touched by the collapse step.
	valuesByCurrency map[string]map[string]float64
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

	// Resolve the dominant currency for each period AND collapse the
	// per-currency value buckets into `payload.values`. The single-
	// currency case (overwhelmingly common — e.g., AAPL with USD only)
	// just merges the one bucket. The multi-currency case (e.g., TSM
	// publishing both TWD and USD for every monetary concept) picks the
	// dominant currency by fact count, merges that bucket only, and logs
	// a warning so operators can audit.
	//
	// Phase B post-launch hotfix: prior to bucketing, the multi-currency
	// case suffered from a silent last-write-wins race because Go map
	// iteration order is randomized — the period was stamped with one
	// currency (typically the higher-count one) but the values map could
	// contain a mix of values from BOTH currencies, making FX conversion
	// incoherent. Collapsing from a deterministic single bucket fixes
	// this.
	for periodKey, payload := range periods {
		if len(payload.currencyCounts) == 0 {
			// Period has no monetary facts (dimensionless-only — e.g., a
			// period that only carries DEI cover-page shares). Nothing to
			// collapse; values already holds everything.
			continue
		}

		// Resolve dominant currency. Single-currency path keeps payload.currency
		// (set on first write in processFacts) unchanged.
		if len(payload.currencyCounts) > 1 {
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

		// Collapse: merge the chosen-currency bucket into `values`. Other
		// buckets are discarded. This guarantees every monetary entry in
		// `values` is reported in `payload.currency`, satisfying the
		// invariant that `convertFinancialsToUSD` relies on.
		if bucket, ok := payload.valuesByCurrency[payload.currency]; ok {
			for k, v := range bucket {
				payload.values[k] = v
			}
		}
		// Free the bucket map — values is now the single source of truth.
		payload.valuesByCurrency = nil
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
				values:           make(map[string]float64),
				currencyCounts:   make(map[string]int),
				valuesByCurrency: make(map[string]map[string]float64),
			}
			periods[periodKey] = payload
		}

		// Store the value. Monetary facts (currencyUnit != "") go into the
		// per-currency bucket so we can later collapse to the period's
		// resolved dominant currency without stale-overwrite races between
		// concurrent currency units (e.g., TSM publishing the same concept
		// in BOTH TWD and USD). Dimensionless facts (shares, period
		// metadata) write directly to `values` because they are never
		// disambiguated by currency.
		if currencyUnit != "" {
			bucket, bucketExists := payload.valuesByCurrency[currencyUnit]
			if !bucketExists {
				bucket = make(map[string]float64)
				payload.valuesByCurrency[currencyUnit] = bucket
			}
			bucket[conceptName] = fact.Val

			if payload.currency == "" {
				payload.currency = currencyUnit
			}
			payload.currencyCounts[currencyUnit]++
		} else {
			// Dimensionless: shares / metadata go to values directly.
			payload.values[conceptName] = fact.Val
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

	// Non-recurring earnings-normalization sources (TDB-1).
	//
	// These three income-statement fields feed the Category-C earnings
	// adjusters (C1 restructuring, C3 litigation, C6 capitalized interest).
	// Each adjuster expects a POSITIVE add-back magnitude; absAddBack
	// normalizes the JNJ-style credit-presentation case (a debit-balance
	// charge tagged negative). See
	// docs/refactoring/spec/tdb-1-parser-nonrecurring-extraction-spec.md.

	// C1 (restructuring) add-back source. Alternative presentations of the
	// same period total → findValue first-hit (NOT sumValues — they overlap,
	// summing would double-count). RestructuringAndRelatedCostIncurredCost is a
	// dimensional/disclosure-axis element (per-restructuring-type axis); it is
	// last in priority so the two undimensioned totals win first — the
	// dimension-unaware fact store only reaches it when neither total is
	// present. TDB-1 spec §3.2.
	if val, exists := p.findValue(data, []string{
		"RestructuringCharges",
		"RestructuringCosts",
		"RestructuringAndRelatedCostIncurredCost",
	}); exists {
		financialData.RestructuringCharges = absAddBack(val)
	}

	// C3 (litigation) add-back source. LitigationSettlementExpense is the
	// direct expense line; LossContingencyLossInPeriod is the broader ASC-450
	// fallback that captures ALL loss contingencies (litigation, environmental,
	// warranty, product-liability) — so when it is the matched tag, C3 may fire
	// on a non-litigation contingency BY DESIGN (any material one-time loss
	// contingency is a defensible normalization; gated by C3's 1%-of-revenue
	// threshold). GainLossRelatedToLitigationSettlement is DELIBERATELY EXCLUDED
	// — it is a credit-balance net gain/loss with inverted semantics (a
	// positive value is a GAIN, the opposite of a settlement charge); mapping
	// it would corrupt C3 by adding back a gain. TDB-1 spec §3.1 / §3.2 / Q2.
	if val, exists := p.findValue(data, []string{
		"LitigationSettlementExpense",
		"LossContingencyLossInPeriod",
	}); exists {
		financialData.LitigationSettlements = absAddBack(val)
	}

	// C6 (capitalized interest) reclassification source. Both us-gaap variants
	// are debit-balance income-statement period amounts (period / incurred) →
	// first-hit. NOTE: us-gaap:InterestPaidCapitalized is DELIBERATELY EXCLUDED
	// — it is a credit-balance cash-flow supplemental disclosure ("cash paid for
	// interest capitalized, investing activity"), a different measure than the
	// period capitalized-interest expense C6 reclassifies; populating it would
	// fire C6 with the wrong quantity (TDB-1 REVIEWER MAJOR).
	// ifrs-full:BorrowingCostsCapitalised (British spelling, IAS 23) appended
	// for IFRS filers — MEDIUM confidence, unverified against a live basket
	// filer (TDB-1 Q4). TDB-1 spec §3.2.
	if val, exists := p.findValue(data, []string{
		"InterestCostsCapitalized",
		"InterestCostsIncurredCapitalized",
		// IFRS-full (IAS 23) — note British spelling.
		"BorrowingCostsCapitalised",
	}); exists {
		financialData.CapitalizedInterest = absAddBack(val)
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

	// Cash flow statement items (for true FCF calculation).
	//
	// Phase B post-launch follow-up (TSM live verification, 2026-04-29):
	// TSM publishes D&A as `ifrs-full:DepreciationExpense` (NT$653.6B for
	// 2024FY) — NOT under the umbrella `DepreciationAndAmortisationExpense`
	// concept the original Phase B6 mapping anticipated. The Phase B6 fallback
	// list is preserved (other IFRS-full filers may use it), and
	// `DepreciationExpense` is appended so TSM-style filers extract correctly.
	// `findValue` is first-hit, so the existing US-GAAP and umbrella IFRS
	// names are still tried first before falling through to this tag.
	//
	// `AmortisationExpense` is intentionally NOT in this list: TSM's
	// AmortisationExpense (~NT$9B) is < 1.5% of DepreciationExpense, so
	// adding it would only marginally improve coverage but would also
	// double-count for filers that publish BOTH the umbrella tag (with
	// amortisation already included) AND the separate AmortisationExpense
	// (would never hit because findValue is first-hit, but kept out for
	// clarity).
	//
	// `DepreciationAmortizationAndAccretionNet` is added per
	// docs/columns name.txt (line 135) as a US-GAAP fallback for
	// energy/utility filers with depletion-accretion components.
	if val, exists := p.findValue(data, []string{
		"DepreciationDepletionAndAmortization",
		"DepreciationAndAmortization",
		"DepreciationAmortizationAndAccretionNet",
		"Depreciation",
		// IFRS-full equivalents (Phase B6 + post-launch follow-up).
		"DepreciationAndAmortisationExpense",
		"DepreciationAmortisationAndImpairmentLossReversalOfImpairmentLossRecognisedInProfitOrLoss",
		"DepreciationExpense",
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

	// Total liabilities umbrella tag — feeds the Graham-floor diagnostic
	// (internal/services/valuation/graham.go). Distinct from
	// CurrentLiabilities (short-term subset). The us-gaap:Liabilities tag is
	// already in the requested-tags list at the top of this file; this block
	// just plumbs the parsed value onto FinancialData. ifrs-full:Liabilities
	// covers TSM, ASML, SAP, BABA and other 20-F filers without a separate
	// derivation step.
	if val, exists := p.findValue(data, []string{
		"Liabilities",
	}); exists {
		financialData.TotalLiabilities = val
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

	// TotalDebt extraction has two paths:
	//
	// 1) US-GAAP / umbrella IFRS: a single concept already aggregates the
	//    full debt balance (e.g., `us-gaap:LongTermDebt` for AAPL,
	//    `ifrs-full:Borrowings` when the filer publishes the umbrella). The
	//    findValue first-hit semantic is correct here.
	//
	// 2) IFRS component-level filers (TSM is the canonical case): no single
	//    concept aggregates everything — TSM splits debt across
	//    `LongtermBorrowings`, `CurrentPortionOfLongtermBorrowings`,
	//    `NoncurrentPortionOfNoncurrentBondsIssued`,
	//    `CurrentBondsIssuedAndCurrentPortionOfNoncurrentBondsIssued`, and
	//    `ShorttermBorrowings`. Path (1) misses everything for these filers
	//    (returns 0); the datacleaner then injects the operating-lease PV
	//    as a stand-in, which is wrong (lease ≠ financing debt) and produces
	//    a tiny denominator → cost_of_debt explodes (26% for TSM in the
	//    2026-04-29 live trace). Path (2) sums the components to recover
	//    the correct face-value debt.
	//
	// Path-2 component tags are mutually disjoint: each represents a
	// distinct slice of TSM's balance sheet (current vs noncurrent,
	// borrowings vs bonds). The `Borrowings` umbrella is NOT summed with
	// the components — if it's present, it already replaced them in the
	// filer's reporting. Path-1 captures that umbrella case; the components
	// in path-2 only fire when path-1 returned nothing (i.e., umbrella
	// absent).
	//
	// Hard exclusion: ifrs-full:LeaseLiabilities and us-gaap:FinanceLeaseLiability*
	// are intentionally NOT in either list. Lease liabilities are operating
	// obligations under IFRS 16 / ASC 842, not financing debt. They map
	// only to OperatingLeaseLiability below — mirroring the US-GAAP
	// convention where the LongTermDebt family excludes operating leases.
	// Including them here would double-count for lease-heavy filers like
	// TSM, once as debt and once as a lease obligation in adjustments
	// (Phase B post-launch hotfix preserved).
	//
	// Cross-checked against docs/columns name.txt: added
	// `DebtInstrumentCarryingAmount` (line 89) and `OtherShortTermBorrowings`
	// (line 348) as US-GAAP fallbacks for filers that don't use the
	// `LongTermDebt*` family but still publish debt under those concepts.
	if val, exists := p.findValue(data, []string{
		"LongTermDebt",
		"LongTermDebtNoncurrent",
		"LongTermDebtCurrent",
		"LongTermDebtAndCapitalLeaseObligations",
		"DebtCurrent",
		"DebtInstrumentCarryingAmount",
		"OtherShortTermBorrowings",
		// IFRS-full umbrella concepts (filers using the aggregate tag).
		"Borrowings",
		"NoncurrentBorrowings",
		"CurrentBorrowings",
	}); exists {
		financialData.TotalDebt = val
		financialData.InterestBearingDebt = val // Assume all debt is interest-bearing
	} else if ifrsDebt, ok := p.sumValues(data, []string{
		// IFRS-full component-level concepts (TSM-style filers).
		// Sum order doesn't matter — these are disjoint balance-sheet slices.
		"LongtermBorrowings",
		"ShorttermBorrowings",
		"CurrentPortionOfLongtermBorrowings",
		"NoncurrentPortionOfNoncurrentBondsIssued",
		"CurrentBondsIssuedAndCurrentPortionOfNoncurrentBondsIssued",
	}); ok {
		financialData.TotalDebt = ifrsDebt
		financialData.InterestBearingDebt = ifrsDebt
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

	// Right-of-Use assets (ASC 842 / IFRS 16) — A6 (TDB-2). Stored as a parallel
	// informational field; deliberately NOT folded into computePlugs (see spec §3.7),
	// so the TotalAssets == sum(components)+plug invariant is unchanged.
	if val, exists := p.findValue(data, []string{
		"OperatingLeaseRightOfUseAsset",
		"RightOfUseAssets",
		"OperatingLeaseRightOfUseAssetAfterAccumulatedAmortization",
		// IFRS 16 equivalent
		"RightofuseAssets",
	}); exists {
		financialData.OperatingLeaseRightOfUseAsset = val
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

	// B3 contingent-liability inputs (TDB-12). Recognized ASC 450 / ASC 410
	// balance-sheet accruals (instant, credit-balance). B3 (ApplyB3Contingent)
	// SUMS all three as the gross exposure, then probability-weights it into a
	// DebtLikeClaims overlay (the EV->Equity bridge subtracts it -> lower fair
	// value for filers carrying material accruals). The three candidate lists
	// are mutually DISJOINT (general vs environmental vs litigation) and
	// AGGREGATE-FIRST within each — findValue (first-hit), NOT sumValues — so a
	// filer reporting both an aggregate AND its current/noncurrent split is not
	// double-counted (MSFT/MXL report both). Negatives clamp to 0 (val > 0): a
	// negative recognized liability is a data anomaly, not a credit-presentation
	// flip, so absAddBack/math.Abs (TDB-1's idiom for income-statement charges)
	// is deliberately NOT used here. These three fields are parallel B3 inputs —
	// NOT components of any computePlugs triple or recomputeUmbrellas formula
	// (same disposition as the A6 RightOfUse asset above). See
	// docs/refactoring/spec/tdb-12-contingent-liability-parser-extraction-spec.md.

	// General ASC 450 loss-contingency accrual. Aggregate first; the current/
	// noncurrent split are fallbacks for filers reporting ONLY the split.
	if val, exists := p.findValue(data, []string{
		"LossContingencyAccrualAtCarryingValue",
		"LossContingencyAccrualCarryingValueCurrent",
		"LossContingencyAccrualCarryingValueNoncurrent",
	}); exists && val > 0 {
		financialData.ContingentLiabilities = val
	}

	// Environmental remediation accrual (ASC 410). Aggregate first, then split.
	if val, exists := p.findValue(data, []string{
		"AccrualForEnvironmentalLossContingencies",
		"AccruedEnvironmentalLossContingenciesNoncurrent",
		"AccruedEnvironmentalLossContingenciesCurrent",
	}); exists && val > 0 {
		financialData.EnvironmentalLiabilities = val
	}

	// Recognized litigation reserve. NOT LitigationSettlementExpense /
	// LossContingencyLossInPeriod — those are income-statement charges already
	// mapped to LitigationSettlements (C3) by TDB-1; reusing them would
	// double-count the same dollars across two rules. MEDIUM confidence — these
	// concepts appear in no basket fixture (large filers tag litigation
	// dimensionally / via custom elements, which the dimension-unaware
	// companyfacts ingestion does not expose). TDB-12 spec §3.4 / Q5.
	if val, exists := p.findValue(data, []string{
		"EstimatedLitigationLiability",
		"LitigationReserve",
	}); exists && val > 0 {
		financialData.LitigationLiabilities = val
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

	// DC-1 Phase 0: fill the four Other* plug fields as residuals so components
	// sum to umbrellas. Runs after every findValue/sumValues call and after
	// the missing-fields stamp; runs before return so callers see a balanced
	// FinancialData. computePlugs assumes currency coherence — guaranteed by
	// extractFiscalPeriods's dominant-currency collapse (parser.go:309-363).
	// See docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md.
	computePlugs(financialData, p.logger)

	return financialData, nil
}

// absAddBack normalizes an XBRL charge fact to the positive add-back magnitude
// the C1/C3/C6 earnings adjusters expect. These concepts are debit-balance
// charge/cost elements, but filers occasionally sign them as credits (e.g. JNJ
// tags LitigationSettlementExpense as -379M). A negative tag is the same dollar
// charge with an inverted presentation sign, not a different line — taking the
// magnitude yields the intended add-back. See
// docs/refactoring/spec/tdb-1-parser-nonrecurring-extraction-spec.md §3.1.
func absAddBack(v float64) float64 { return math.Abs(v) }

// findValue finds a value by trying multiple possible field names
func (p *Parser) findValue(data map[string]float64, fieldNames []string) (float64, bool) {
	for _, fieldName := range fieldNames {
		if val, exists := data[fieldName]; exists {
			return val, true
		}
	}
	return 0, false
}

// sumValues returns the SUM of all matching field values present in the
// data map (vs `findValue`, which returns the first hit only).
//
// Use this when the underlying XBRL taxonomy splits a single economic
// quantity across multiple disjoint concepts and the filer publishes
// each component independently. The canonical example is TSM, which
// splits interest-bearing debt across `LongtermBorrowings`,
// `CurrentPortionOfLongtermBorrowings`,
// `NoncurrentPortionOfNoncurrentBondsIssued`,
// `CurrentBondsIssuedAndCurrentPortionOfNoncurrentBondsIssued`, and
// `ShorttermBorrowings` — no umbrella tag aggregates them, so first-hit
// would miss the bulk of debt and produce a wildly wrong cost-of-debt
// denominator.
//
// The caller is responsible for ensuring the supplied tag list is
// mutually disjoint (no double-counting). For TotalDebt this is the
// case: each tag represents a distinct slice of the balance sheet
// (current vs noncurrent, borrowings vs bonds). Umbrella concepts like
// `ifrs-full:Borrowings` are handled by the first-hit `findValue` path
// BEFORE the component-sum fallback fires, so they never combine with
// the components.
//
// Returns (0, false) if no fields matched, so callers can branch on the
// boolean exactly like findValue.
func (p *Parser) sumValues(data map[string]float64, fieldNames []string) (float64, bool) {
	var sum float64
	found := false
	for _, fieldName := range fieldNames {
		if val, exists := data[fieldName]; exists {
			sum += val
			found = true
		}
	}
	return sum, found
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
// (docs/refactoring/archive/ifrs-foreign-private-issuer-support-spec.md) so 20-F
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

		// Income Statement - Non-recurring earnings-normalization items (TDB-1).
		// Feed the C1 (restructuring), C3 (litigation), C6 (capitalized-interest)
		// earnings adjusters. GainLossRelatedToLitigationSettlement is
		// deliberately NOT listed (inverted net-gain semantics — TDB-1 Q2).
		"us-gaap:RestructuringCharges",
		"us-gaap:RestructuringCosts",
		"us-gaap:RestructuringAndRelatedCostIncurredCost",
		"us-gaap:LitigationSettlementExpense",
		"us-gaap:LossContingencyLossInPeriod",
		"us-gaap:InterestCostsCapitalized",
		"us-gaap:InterestCostsIncurredCapitalized",

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
		// Phase B post-launch: cross-checked against docs/columns name.txt.
		"us-gaap:DebtInstrumentCarryingAmount",
		"us-gaap:OtherShortTermBorrowings",
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

		// Balance Sheet - Contingent Liabilities (B3 — TDB-12).
		// Recognized ASC 450 / ASC 410 accruals B3 probability-weights into a
		// DebtLikeClaims overlay. Aggregate-first per field; the
		// income-statement litigation-expense (C3's, via TDB-1) and possible-loss
		// DISCLOSURE tags are deliberately NOT listed (TDB-12 spec §3.2). No
		// IFRS-full IAS 37 Provisions mapping ships (TDB-12 Q4 — deferred).
		"us-gaap:LossContingencyAccrualAtCarryingValue",
		"us-gaap:LossContingencyAccrualCarryingValueCurrent",
		"us-gaap:LossContingencyAccrualCarryingValueNoncurrent",
		"us-gaap:AccrualForEnvironmentalLossContingencies",
		"us-gaap:AccruedEnvironmentalLossContingenciesNoncurrent",
		"us-gaap:AccruedEnvironmentalLossContingenciesCurrent",
		"us-gaap:EstimatedLitigationLiability",
		"us-gaap:LitigationReserve",

		// Share Information
		"us-gaap:CommonStockSharesOutstanding",
		"us-gaap:CommonStockSharesIssued",
		"us-gaap:WeightedAverageNumberOfDilutedSharesOutstanding",
		"us-gaap:WeightedAverageNumberOfSharesOutstandingBasic",

		// Cash Flow Statement
		"us-gaap:CashAndCashEquivalentsAtCarryingValue",
		"us-gaap:NetCashProvidedByUsedInOperatingActivities",
		// Phase B post-launch: D&A umbrella for energy/utility filers.
		"us-gaap:DepreciationAmortizationAndAccretionNet",

		// ---- IFRS-full (Phase B6) -------------------------------------------
		// Income Statement
		"ifrs-full:Revenue",
		"ifrs-full:RevenueFromContractsWithCustomers",
		"ifrs-full:ProfitLossFromOperatingActivities",
		"ifrs-full:ProfitBeforeTax",
		"ifrs-full:ProfitLoss",
		"ifrs-full:ProfitLossAttributableToOwnersOfParent",
		"ifrs-full:FinanceCosts",
		// Capitalized interest (IAS 23 — British spelling). Feeds C6 for IFRS
		// filers. MEDIUM confidence — unverified against a live filer (TDB-1 Q4).
		"ifrs-full:BorrowingCostsCapitalised",

		// Balance Sheet
		"ifrs-full:CashAndCashEquivalents",
		"ifrs-full:Cash",
		"ifrs-full:CurrentAssets",
		"ifrs-full:CurrentLiabilities",
		// Umbrella total-liabilities tag for IFRS filers — feeds the
		// Graham-floor diagnostic. Most 20-F filers publish this directly;
		// findValue is namespace-aware so the same code path that picks up
		// us-gaap:Liabilities will pick this up for IFRS reporters.
		"ifrs-full:Liabilities",
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
		// Component-level debt tags (TSM-style filers — summed via sumValues).
		"ifrs-full:LongtermBorrowings",
		"ifrs-full:ShorttermBorrowings",
		"ifrs-full:CurrentPortionOfLongtermBorrowings",
		"ifrs-full:NoncurrentPortionOfNoncurrentBondsIssued",
		"ifrs-full:CurrentBondsIssuedAndCurrentPortionOfNoncurrentBondsIssued",
		"ifrs-full:LeaseLiabilities",

		// Cash Flow Statement
		"ifrs-full:CashFlowsFromUsedInOperatingActivities",
		"ifrs-full:PurchaseOfPropertyPlantAndEquipmentClassifiedAsInvestingActivities",
		"ifrs-full:DepreciationAndAmortisationExpense",
		"ifrs-full:DepreciationAmortisationAndImpairmentLossReversalOfImpairmentLossRecognisedInProfitOrLoss",
		// Component-level depreciation (TSM-style filers — first-hit fallback).
		"ifrs-full:DepreciationExpense",

		// Pension
		"ifrs-full:DefinedBenefitObligationAtPresentValue",

		// TODO: Add dynamic mapping framework for future extensibility
		// This static approach should be replaced with configurable mapping
		// to support new SEC fields without code changes
	}
}
