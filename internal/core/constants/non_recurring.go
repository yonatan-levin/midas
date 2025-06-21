package constants

// NonRecurringItems contains XBRL tags and patterns that identify non-recurring
// income statement items that should be excluded from normalized operating income
var NonRecurringItems = []string{
	// Asset sales and disposals
	"GainsLossesOnSalesOfAssetsIncomeStatementParentheticalDisclosure",
	"GainsLossesOnSalesOfAssets",
	"GainLossOnSaleOfProperties",
	"GainLossOnSaleOfBusiness",
	"GainLossOnSaleOfInvestments",
	"AssetImpairmentCharges",

	// Restructuring and reorganization
	"RestructuringCharges",
	"RestructuringSettlementAndImpairmentProvisions",
	"BusinessCombinationIntegrationCosts",
	"EmployeeTerminationBenefits",
	"FacilityShutdownCosts",

	// Legal and settlement
	"LegalSettlementExpense",
	"LitigationSettlement",
	"LegalProceedingsExpense",
	"InsuranceSettlement",

	// Acquisition related
	"BusinessCombinationAcquisitionRelatedCosts",
	"BusinessCombinationStepAcquisitionEquityInterestInAcquireeRemeasurementGain",
	"MergersAndAcquisitionsExpense",
	"DueDiligenceCosts",

	// Impairments and write-downs
	"GoodwillImpairmentLoss",
	"IntangibleAssetsNetExcludingGoodwillImpairmentLoss",
	"InventoryWriteDown",
	"BadDebtExpense", // Unusual bad debt provisions
	"AssetWriteDown",

	// Discontinued operations
	"IncomeLossFromDiscontinuedOperationsNetOfTax",
	"DiscontinuedOperationGainLossOnDisposal",
	"DiscontinuedOperationIncomeLossFromOperationsNetOfTax",

	// Extraordinary items
	"ExtraordinaryItemGainLoss",
	"UnusualOrInfrequentItemNetGainLoss",
	"OtherSpecialCharges",

	// Foreign exchange (large one-time)
	"ForeignCurrencyTransactionGainLossUnrealized",
	"ForeignCurrencyTransactionGainLossRealized",

	// Tax-related extraordinary items
	"IncomeTaxBenefitCreditUnusualOrInfrequent",
	"DeferredTaxAssetsValuationAllowance",

	// Environmental and regulatory
	"EnvironmentalLossContingencies",
	"RegulatoryFines",
	"ComplianceCosts",

	// COVID-19 related (recent years)
	"COVID19RelatedExpenses",
	"PandemicRelatedCosts",
	"GovernmentGrantIncome", // PPP loans, etc.
}

// NonRecurringPatterns contains partial string matches for identifying
// non-recurring items in financial statement line items
var NonRecurringPatterns = []string{
	"unusual",
	"extraordinary",
	"one-time",
	"onetime",
	"special",
	"restructuring",
	"impairment",
	"settlement",
	"litigation",
	"acquisition",
	"merger",
	"discontinued",
	"disposal",
	"writedown",
	"write-down",
	"write-off",
	"writeoff",
	"spin-off",
	"spinoff",
	"divestiture",
	"gain on sale",
	"loss on sale",
	"asset sale",
	"business combination",
	"goodwill",
	"contingency",
	"environmental",
	"regulatory",
	"covid",
	"pandemic",
}

// InventoryFlags contains indicators for potentially obsolete or dead inventory
var InventoryFlags = []string{
	// Inventory categories that may indicate obsolescence
	"ObsoleteInventory",
	"SlowMovingInventory",
	"ExcessInventory",
	"DamagedInventory",
	"InventoryReserve",
	"InventoryObsolescenceReserve",
	"InventoryWriteDown",
	"InventoryAdjustment",
	"InventoryProvision",
	"InventoryLoss",
}

// DefaultMarketRiskPremium is the standard market risk premium assumption
// Used in CAPM calculation when not specified
const DefaultMarketRiskPremium = 0.05 // 5%

// DefaultTerminalGrowthRate is the long-term growth rate assumption
// Typically set to inflation or GDP growth rate
const DefaultTerminalGrowthRate = 0.025 // 2.5%

// MaxTerminalGrowthRate caps the terminal growth rate to prevent
// unrealistic perpetual growth assumptions
const MaxTerminalGrowthRate = 0.03 // 3%

// DefaultTaxRate is used when effective tax rate cannot be calculated
const DefaultTaxRate = 0.21 // 21% US corporate tax rate

// MinimumDataPoints is the minimum number of periods needed for growth calculation
const MinimumDataPoints = 2

// PreferredDataPoints is the ideal number of periods for growth calculation
const PreferredDataPoints = 5 // 5 years of data

// InventoryTurnoverThreshold - if inventory turnover falls below this
// and inventory has grown significantly, flag for potential obsolescence
const InventoryTurnoverThreshold = 2.0 // 2x per year

// InventoryGrowthThreshold - if inventory grows by more than this multiple
// of the 5-year median, flag for investigation
const InventoryGrowthThreshold = 1.5 // 1.5x median

// DeadInventoryWritedownRate - percentage to write down flagged inventory
const DeadInventoryWritedownRate = 0.40 // 40% haircut

// SECRateLimit is the maximum number of requests per second to SEC API
const SECRateLimit = 10

// CacheExpirationHours - how long to cache SEC filing data
const CacheExpirationHours = 24

// MarketDataCacheMinutes - how long to cache market data
const MarketDataCacheMinutes = 15
