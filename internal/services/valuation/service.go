package valuation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/industry"
	"github.com/midas/dcf-valuation-api/internal/services/datafetcher"
	growthsvc "github.com/midas/dcf-valuation-api/internal/services/growth"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
	"github.com/midas/dcf-valuation-api/pkg/finance/growth"
	"github.com/midas/dcf-valuation-api/pkg/finance/wacc"
)

// Service provides valuation operations
type Service struct {
	financialRepo      ports.FinancialDataRepository
	marketRepo         ports.MarketDataRepository
	macroRepo          ports.MacroDataRepository
	cache              ports.CacheRepository
	dataCleaner        datacleaner.DataCleanerService
	dataFetcher        *datafetcher.DataFetcher
	growthEstimator    *growthsvc.Estimator
	yfinanceGateway    ports.YFinanceGateway // optional, for analyst estimates
	metricsService     ports.MetricsService
	modelRouter        *models.ModelRouter          // Phase 3: industry-aware model selection
	industryClassifier *industry.IndustryClassifier // Phase 3: SIC/NAICS classification
	countryRiskMap     map[string]float64           // Phase 4: ISO-2 country code -> CRP
	industryMultiples  *industryMultiplesConfig     // Phase 4: EV/EBITDA and P/E multiples for cross-checks
	config             *config.Config
	logger             *zap.Logger
}

// NewService creates a new valuation service
func NewService(
	financialRepo ports.FinancialDataRepository,
	marketRepo ports.MarketDataRepository,
	macroRepo ports.MacroDataRepository,
	cache ports.CacheRepository,
	dataCleaner datacleaner.DataCleanerService,
	dataFetcher *datafetcher.DataFetcher,
	metricsService ports.MetricsService,
	cfg *config.Config,
	logger *zap.Logger,
) *Service {
	// Build growth estimator from valuation config
	estimatorCfg := growthsvc.DefaultEstimatorConfig()
	if cfg.Valuation.DCFMaxGrowthRate > 0 {
		estimatorCfg.MaxGrowthRate = cfg.Valuation.DCFMaxGrowthRate
	}
	if cfg.Valuation.DCFMinGrowthRate != 0 {
		estimatorCfg.MinGrowthRate = cfg.Valuation.DCFMinGrowthRate
	}

	// Initialize industry classifier for SIC/NAICS-based model selection
	classifier := industry.NewIndustryClassifier()

	// Initialize model router with all available valuation models.
	// Order matters: more specific models are listed first.
	allModels := []models.ValuationModel{
		models.NewDDMModel(logger),
		models.NewFFOModel(logger),
		models.NewRevenueMultipleModel(logger),
		models.NewMultiStageDCFModel(logger),
	}
	router := models.NewModelRouter(allModels, logger)

	// Phase 4: Load country risk premiums for international support.
	// Graceful degradation: if config file is missing, all tickers get CRP = 0 (domestic US).
	crpMap, crpErr := LoadCountryRiskPremiums(DefaultCountryRiskConfigPath)
	if crpErr != nil {
		logger.Warn("Country risk config unavailable, defaulting CRP to 0 for all tickers",
			zap.Error(crpErr))
		crpMap = map[string]float64{"US": 0, "default": 0}
	}

	// Phase 4: Load industry multiples for exit-multiple TV and sanity cross-checks.
	// Graceful degradation: if missing, exit-multiple and cross-checks are skipped.
	indMultiples, imErr := LoadIndustryMultiples(models.DefaultIndustryMultiplesPath)
	if imErr != nil {
		logger.Warn("Industry multiples config unavailable, exit-multiple TV and cross-checks disabled",
			zap.Error(imErr))
	}

	return &Service{
		financialRepo:      financialRepo,
		marketRepo:         marketRepo,
		macroRepo:          macroRepo,
		cache:              cache,
		dataCleaner:        dataCleaner,
		dataFetcher:        dataFetcher,
		growthEstimator:    growthsvc.NewEstimator(estimatorCfg, logger),
		metricsService:     metricsService,
		modelRouter:        router,
		industryClassifier: classifier,
		countryRiskMap:     crpMap,
		industryMultiples:  indMultiples,
		config:             cfg,
		logger:             logger,
	}
}

// SetYFinanceGateway injects the Yahoo Finance gateway for analyst estimates.
// This is optional — when nil, the growth estimator uses historical data only.
func (s *Service) SetYFinanceGateway(gw ports.YFinanceGateway) {
	s.yfinanceGateway = gw
}

// CalculateValuation performs a complete DCF valuation for a ticker.
// opts may be nil; when provided, overrides (e.g., beta, risk-free rate)
// replace the corresponding values fetched from data sources.
func (s *Service) CalculateValuation(ctx context.Context, ticker string, opts *ValuationOptions) (*entities.ValuationResult, error) {
	start := time.Now()
	s.logger.Info("Starting valuation calculation", zap.String("ticker", ticker))

	// Skip cache for requests with overrides — they represent ad-hoc user queries
	// that should not pollute (or be served from) the default-parameter cache.
	hasOverrides := opts != nil && (opts.OverrideBeta != nil || opts.OverrideRiskFree != nil)
	cacheKey := fmt.Sprintf("valuation:v4:%s", ticker)

	if !hasOverrides {
		var cachedResult entities.ValuationResult
		if err := s.cache.Get(ctx, cacheKey, &cachedResult); err == nil {
			s.logger.Info("Returning cached valuation", zap.String("ticker", ticker))
			s.metricsService.RecordValuationRequest(ticker, "single", "cache_hit", time.Since(start))
			return &cachedResult, nil
		}
	}

	// Try to load data from repositories first (for previously fetched/seeded tickers)
	historicalData, err := s.financialRepo.GetHistorical(ctx, ticker, 10)

	var marketData *entities.MarketData
	var macroData *entities.MacroData

	if err != nil || len(historicalData.Data) == 0 {
		// No data in repository — use DataFetcher to retrieve from external APIs
		s.logger.Info("No historical data in repository, fetching via DataFetcher", zap.String("ticker", ticker))

		if s.dataFetcher == nil {
			return nil, fmt.Errorf("%w: no historical data and data fetcher not configured for %s", ErrTickerNotFound, ticker)
		}

		fetchResult, fetchErr := s.dataFetcher.Fetch(ctx, &entities.FetchRequest{Ticker: ticker})
		if fetchErr != nil {
			return nil, fmt.Errorf("failed to fetch data via DataFetcher: %w", fetchErr)
		}

		// Use multi-period historical data if available (from full SEC parser)
		if fetchResult.HistoricalData != nil && len(fetchResult.HistoricalData.Data) > 0 {
			historicalData = fetchResult.HistoricalData
		} else if fetchResult.FinancialData != nil {
			// Fallback: wrap single FinancialData into HistoricalFinancialData
			periodKey := fetchResult.FinancialData.FilingPeriod
			if periodKey == "" || (len(periodKey) < 2 || periodKey[len(periodKey)-2:] != "FY") {
				periodKey = fmt.Sprintf("%dFY", time.Now().Year())
				fetchResult.FinancialData.FilingPeriod = periodKey
			}
			historicalData = &entities.HistoricalFinancialData{
				Ticker: ticker,
				Data:   map[string]*entities.FinancialData{periodKey: fetchResult.FinancialData},
			}
		} else {
			return nil, fmt.Errorf("%w: DataFetcher returned no financial data for %s", ErrTickerNotFound, ticker)
		}

		// Use market and macro data from FetchResult if available
		marketData = fetchResult.MarketData
		macroData = fetchResult.MacroData

		s.logger.Info("Successfully fetched data via DataFetcher",
			zap.String("ticker", ticker),
			zap.Int("periods", len(historicalData.Data)),
			zap.Bool("has_market_data", marketData != nil),
			zap.Bool("has_macro_data", macroData != nil))
	} else if s.isFinancialDataIncomplete(historicalData) && s.dataFetcher != nil {
		// Data exists in repo but is incomplete (missing FCF fields like D&A, CapEx, Cash).
		// Re-fetch from SEC to get complete data, then persist the update.
		s.logger.Info("Stored financial data incomplete (missing FCF fields), re-fetching from SEC",
			zap.String("ticker", ticker))

		fetchResult, fetchErr := s.dataFetcher.Fetch(ctx, &entities.FetchRequest{Ticker: ticker})
		if fetchErr == nil && fetchResult.HistoricalData != nil && len(fetchResult.HistoricalData.Data) > 0 {
			historicalData = fetchResult.HistoricalData
			if fetchResult.MarketData != nil {
				marketData = fetchResult.MarketData
			}
			if fetchResult.MacroData != nil {
				macroData = fetchResult.MacroData
			}

			// Persist the updated data so future requests don't re-fetch
			if s.financialRepo != nil {
				if storeErr := s.financialRepo.StoreHistorical(ctx, historicalData); storeErr != nil {
					s.logger.Warn("Failed to persist re-fetched financial data",
						zap.String("ticker", ticker), zap.Error(storeErr))
				} else {
					s.logger.Info("Persisted complete financial data to repository",
						zap.String("ticker", ticker),
						zap.Int("periods", len(historicalData.Data)))
				}
			}
		} else {
			s.logger.Warn("Re-fetch failed or returned no data, using incomplete stored data with NOPAT fallback",
				zap.String("ticker", ticker))
		}
	}

	// Fill in market and macro data from repositories if not already set by DataFetcher
	if marketData == nil {
		marketData, err = s.marketRepo.GetLatest(ctx, ticker)
		if err != nil {
			s.logger.Warn("Failed to fetch market data from repository", zap.Error(err), zap.String("ticker", ticker))
			return nil, fmt.Errorf("failed to fetch market data: %w", err)
		}
	}
	if macroData == nil {
		macroData, err = s.macroRepo.GetLatest(ctx)
		if err != nil {
			s.logger.Warn("Failed to fetch macro data from repository", zap.Error(err), zap.String("ticker", ticker))
			return nil, fmt.Errorf("failed to fetch macro data: %w", err)
		}
	}

	// Apply data cleaning if service is available
	var cleaningResult *entities.CleaningResult
	if s.dataCleaner != nil {
		latest, latestPeriod := historicalData.GetLatestPeriod()
		if latest != nil {
			var err error
			cleaningResult, err = s.dataCleaner.CleanFinancialData(ctx, latest)
			if err != nil {
				s.logger.Warn("Data cleaning failed, using original data",
					zap.Error(err),
					zap.String("ticker", ticker))
			} else {
				s.logger.Info("Data cleaning applied successfully",
					zap.String("ticker", ticker),
					zap.Float64("quality_score", cleaningResult.QualityScore))
				// Update historical data with cleaned data
				historicalData.Data[latestPeriod] = cleaningResult.CleanedData
			}
		}
	} else {
		s.logger.Info("DataCleaner service not available, using original data", zap.String("ticker", ticker))
	}

	// Calculate valuation using potentially cleaned data, applying any user overrides
	result, err := s.performValuation(ctx, historicalData, marketData, macroData, opts)
	if err != nil {
		s.metricsService.RecordValuationRequest(ticker, "single", "error", time.Since(start))
		s.metricsService.RecordValuationError(ticker, "calculation_failed")
		return nil, fmt.Errorf("failed to perform valuation: %w", err)
	}

	// Add cleaning results if available
	if cleaningResult != nil {
		result.DataQualityScore = cleaningResult.QualityScore
		result.DataQualityGrade = entities.GetQualityGrade(cleaningResult.QualityScore)
		result.CleaningFlags = cleaningResult.Flags
		result.CleaningAdjustments = cleaningResult.Adjustments
		// Note: CleaningReport would need the full report structure to be implemented
	}

	// Only cache default (no-override) results to avoid cache poisoning
	if !hasOverrides {
		if err := s.cache.Set(ctx, cacheKey, result, s.config.Valuation.CacheTTL); err != nil {
			s.logger.Warn("Failed to cache valuation result", zap.Error(err))
		}
	}

	// Record successful valuation metrics
	s.metricsService.RecordValuationRequest(ticker, "single", "success", time.Since(start))

	s.logger.Info("Valuation calculation completed", zap.String("ticker", ticker), zap.Float64("dcf_value", result.DCFValuePerShare))
	return result, nil
}

// performValuation executes the valuation calculation logic.
// opts may be nil; when provided, its fields override data-source values in the WACC calculation.
func (s *Service) performValuation(
	ctx context.Context,
	historicalData *entities.HistoricalFinancialData,
	marketData *entities.MarketData,
	macroData *entities.MacroData,
	opts *ValuationOptions,
) (*entities.ValuationResult, error) {

	// Validate minimum data requirements — need at least 1 annual period with revenue or OI
	if !historicalData.HasMinimumData(1) {
		return nil, fmt.Errorf("%w: need at least 1 year of financial data", ErrInsufficientData)
	}

	if !marketData.IsComplete() {
		return nil, fmt.Errorf("%w: incomplete market data", ErrInsufficientData)
	}

	if !macroData.IsComplete() {
		return nil, fmt.Errorf("%w: incomplete macro data", ErrInsufficientData)
	}

	// Calculate historical growth rate from SEC data.
	historicalGrowth, err := historicalData.CalculateAverageGrowthRate(5)
	if err != nil {
		historicalGrowth = &growth.CalculationResult{
			GrowthRate:  s.config.Valuation.DefaultTerminalGrowthCap,
			Method:      "default",
			DataQuality: "low",
			IsReliable:  false,
		}
	}

	// Fetch analyst consensus estimates (optional, degrades gracefully)
	var analystData *ports.YFinanceAnalystEstimates
	if s.yfinanceGateway != nil {
		analystData, _ = s.yfinanceGateway.GetAnalystEstimates(ctx, historicalData.Ticker)
	}

	// Calculate ROIC-sustainable growth ceiling
	sustainableGrowth := 0.0
	latestForROIC, _ := historicalData.GetLatestPeriod()
	if latestForROIC != nil {
		nopat := latestForROIC.NormalizedOperatingIncome * (1 - latestForROIC.TaxRate)
		investedCapital := growth.CalculateInvestedCapital(
			latestForROIC.StockholdersEquity,
			latestForROIC.InterestBearingDebt,
			latestForROIC.CashAndCashEquivalents,
		)
		sustainableGrowth = growth.CalculateSustainableGrowth(nopat, investedCapital, s.growthEstimator.Config().DefaultPayoutRatio)
	}

	// Produce multi-stage growth estimate (analyst + historical blend)
	growthEstimate := s.growthEstimator.EstimateGrowthRates(analystData, historicalGrowth, sustainableGrowth)

	// Get the latest financial data for asset calculations
	latestFinancialData, latestPeriod := historicalData.GetLatestPeriod()
	if latestFinancialData == nil {
		return nil, fmt.Errorf("%w: no latest financial data available", ErrInsufficientData)
	}

	// Calculate tangible value per share
	tangibleValuePerShare := s.calculateTangibleValuePerShare(latestFinancialData, marketData)

	// Determine beta and risk-free rate, applying user overrides when provided
	beta := marketData.GetEffectiveBeta()
	riskFreeRate := macroData.GetEffectiveRiskFreeRate()
	if opts != nil {
		if opts.OverrideBeta != nil {
			beta = *opts.OverrideBeta
		}
		if opts.OverrideRiskFree != nil {
			riskFreeRate = *opts.OverrideRiskFree
		}
	}

	// Phase 4: Beta improvements — Blume adjustment + unlever/relever.
	rawBeta := beta
	beta = wacc.BlumeAdjustedBeta(beta)

	// Unlever beta to remove capital structure effect, then relever at current D/E.
	// For a single company this is near-identity, but it normalizes extreme D/E betas
	// and prepares the pipeline for industry-average beta comparison.
	marketEquity := marketData.CalculateMarketValue()
	if marketEquity > 0 && latestFinancialData.InterestBearingDebt > 0 {
		debtEquityRatio := latestFinancialData.InterestBearingDebt / marketEquity
		unlevered := wacc.UnleveredBeta(beta, latestFinancialData.TaxRate, debtEquityRatio)
		beta = wacc.RelleveredBeta(unlevered, latestFinancialData.TaxRate, debtEquityRatio)
	}

	s.logger.Debug("Beta adjustments applied",
		zap.String("ticker", historicalData.Ticker),
		zap.Float64("raw_beta", rawBeta),
		zap.Float64("adjusted_beta", beta))

	// Phase 4: Look up country risk premium for international / ADR companies.
	// US-domiciled companies get CRP = 0, so the formula is backward-compatible.
	countryCode := GetCountryForTicker(historicalData.Ticker)
	countryRiskPremium := GetCountryRiskPremium(s.countryRiskMap, countryCode)
	if countryRiskPremium > 0 {
		s.logger.Info("Country risk premium applied",
			zap.String("ticker", historicalData.Ticker),
			zap.String("country", countryCode),
			zap.Float64("crp", countryRiskPremium))
	}

	// Calculate WACC (with CRP for international companies)
	waccInputs := wacc.Inputs{
		RiskFreeRate:        riskFreeRate,
		MarketRiskPremium:   macroData.MarketRiskPremium,
		Beta:                beta,
		CountryRiskPremium:  countryRiskPremium,
		MarketValueOfEquity: marketData.CalculateMarketValue(),
		MarketValueOfDebt:   latestFinancialData.InterestBearingDebt,
		InterestExpense:     latestFinancialData.InterestExpense,
		TaxRate:             latestFinancialData.TaxRate,
	}

	waccResult, err := wacc.Calculate(waccInputs)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate WACC: %w", err)
	}

	// Record WACC calculation metric
	s.metricsService.IncWACCCalculations()
	s.metricsService.SetAverageWACC(waccResult.WACC)

	// Use terminal growth from the growth estimate, with WACC safety guard
	terminalGrowthRate := s.calculateTerminalGrowthRate(growthEstimate.SummaryGrowthRate(), waccResult.WACC)
	growthEstimate.TerminalGrowthRate = terminalGrowthRate

	// --- Phase 3: Industry-aware model selection ---
	// Classify industry using IndustryClassifier (SIC/NAICS/keyword matching).
	// The IndustryCode on the financial data may already be populated from upstream.
	industryCode := latestFinancialData.IndustryCode
	if industryCode == "" && s.industryClassifier != nil {
		// Use company name from SEC EntityName for keyword-based classification.
		// Falls back to ticker if company name unavailable.
		companyName := historicalData.CompanyName
		if companyName == "" {
			companyName = historicalData.Ticker
		}
		classified, classifyErr := s.industryClassifier.Classify("", "", companyName)
		if classifyErr == nil && classified != "" && classified != "NA" {
			industryCode = classified
		}
		s.logger.Debug("Industry classification result",
			zap.String("ticker", historicalData.Ticker),
			zap.String("industry_code", industryCode))
	}

	// Resolve shares outstanding (needed by both DCF and alternative models)
	// Priority: diluted (most conservative) > market basic (most current) > financial basic
	sharesOutstanding := latestFinancialData.DilutedSharesOutstanding
	if sharesOutstanding <= 0 {
		sharesOutstanding = marketData.SharesOutstanding
	}
	if sharesOutstanding <= 0 {
		sharesOutstanding = latestFinancialData.SharesOutstanding
	}
	if sharesOutstanding <= 0 {
		return nil, fmt.Errorf("%w: shares outstanding not available", ErrInsufficientData)
	}

	// Select the appropriate valuation model based on industry and financials
	selectedModel := s.modelRouter.SelectModel(industryCode, latestFinancialData)
	var dcfFallbackWarning string

	// If an alternative model (non-DCF) is selected, use it.
	// When the primary model fails but the company has positive OI,
	// performAlternativeValuation returns errFallbackToDCF to signal DCF fallback.
	if selectedModel != nil && selectedModel.ModelType() != "multi_stage_dcf" {
		altResult, altErr := s.performAlternativeValuation(
			ctx, selectedModel, historicalData, marketData, macroData,
			growthEstimate, waccResult, latestFinancialData, latestPeriod,
			tangibleValuePerShare, sharesOutstanding, industryCode,
		)
		if errors.Is(altErr, errFallbackToDCF) {
			s.logger.Info("Falling back to standard DCF after alternative model failure",
				zap.String("ticker", historicalData.Ticker),
				zap.String("primary_model", selectedModel.ModelType()))
			dcfFallbackWarning = fmt.Sprintf("Primary model (%s) could not value this company; fell back to multi_stage_dcf", selectedModel.ModelType())
		} else if altErr != nil {
			return nil, altErr
		} else {
			return altResult, nil
		}
	}

	// --- Standard multi-stage DCF path (existing logic) ---

	// Guard: standard DCF requires positive operating income.
	// Companies with negative OI are routed to revenue_multiple model above.
	baseOI := effectiveOI(latestFinancialData)
	if baseOI <= 0 {
		return nil, fmt.Errorf("%w: company has non-positive operating income (%.2f); standard DCF requires positive operating income", ErrModelNotApplicable, latestFinancialData.NormalizedOperatingIncome)
	}

	// Calculate net working capital change from historical data if available
	nwcChange := s.calculateNetWorkingCapitalChange(historicalData, latestFinancialData)

	// Perform DCF calculation with multi-stage growth rates
	projectionYears := len(growthEstimate.ProjectedGrowthRates)
	if projectionYears == 0 {
		projectionYears = 5 // fallback
	}
	dcfInputs := dcf.Inputs{
		BaseOperatingIncome: baseOI,
		GrowthRate:          growthEstimate.SummaryGrowthRate(), // backward-compatible summary
		GrowthRates:         growthEstimate.ProjectedGrowthRates,
		TerminalGrowthRate:  terminalGrowthRate,
		WACC:                waccResult.WACC,
		ProjectionYears:     projectionYears,
		TaxRate:             latestFinancialData.TaxRate,
	}

	// Use true FCF when D&A and CapEx data are available.
	// Average CapEx and D&A over available annual periods to smooth cyclical spikes
	// (e.g., MSFT's $30B AI infrastructure buildout year shouldn't define all future CapEx).
	avgDA, avgCapEx := s.averageCapExAndDA(historicalData)
	if avgDA > 0 || avgCapEx > 0 {
		dcfInputs.UseTrueFCF = true
		dcfInputs.DepreciationAndAmortization = avgDA
		dcfInputs.CapitalExpenditures = avgCapEx
		dcfInputs.NetWorkingCapitalChange = nwcChange
		s.logger.Info("Using true FCF calculation (smoothed over available periods)",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("avg_da", avgDA),
			zap.Float64("avg_capex", avgCapEx),
			zap.Float64("nwc_change", nwcChange))
	} else {
		s.logger.Info("Falling back to NOPAT-based FCF (D&A/CapEx unavailable)",
			zap.String("ticker", historicalData.Ticker))
	}
	usingNOPATFallback := !dcfInputs.UseTrueFCF

	// Phase 4: Wire exit-multiple terminal value from industry config.
	// When available, DCF averages Gordon Growth TV with exit-multiple TV to reduce model risk.
	if s.industryMultiples != nil && s.industryMultiples.EVEBITDAMultiples != nil {
		exitMultiple := LookupMultiple(s.industryMultiples.EVEBITDAMultiples, industryCode)
		if exitMultiple > 0 {
			dcfInputs.ExitMultiple = exitMultiple
			s.logger.Debug("Exit multiple wired for terminal value averaging",
				zap.String("ticker", historicalData.Ticker),
				zap.String("industry", industryCode),
				zap.Float64("exit_multiple", exitMultiple))
		}
	}

	dcfResult, err := dcf.CalculateDCF(dcfInputs)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate DCF: %w", err)
	}

	// Record DCF calculation metrics
	s.metricsService.IncDCFCalculations()
	s.metricsService.SetAverageGrowthRate(growthEstimate.SummaryGrowthRate())

	// Equity value bridge: EV - Debt + Cash = Equity Value
	equityValue := dcf.CalculateEquityValue(
		dcfResult.EnterpriseValue,
		latestFinancialData.InterestBearingDebt,
		latestFinancialData.CashAndCashEquivalents,
	)
	dcfValuePerShare := equityValue / sharesOutstanding

	// Calculate data freshness score
	dataFreshnessScore := s.calculateDataFreshnessScore(latestFinancialData, marketData, macroData)

	// Apply NOPAT fallback penalty
	if usingNOPATFallback {
		dataFreshnessScore -= 15
		if dataFreshnessScore < 0 {
			dataFreshnessScore = 0
		}
	}

	result := &entities.ValuationResult{
		Ticker:                historicalData.Ticker,
		CalculatedAt:          time.Now(),
		TangibleValuePerShare: tangibleValuePerShare,
		DCFValuePerShare:      dcfValuePerShare,
		WACC:                  waccResult.WACC,
		GrowthRate:            growthEstimate.SummaryGrowthRate(),
		GrowthRates:           growthEstimate.ProjectedGrowthRates,
		TerminalGrowthRate:    terminalGrowthRate,
		GrowthSource:          growthEstimate.Source,
		GrowthConfidence:      growthEstimate.Confidence,
		MarketRiskPremium:     macroData.MarketRiskPremium,
		EnterpriseValue:       dcfResult.EnterpriseValue,
		EquityValue:           equityValue,
		FinancialDataPeriod:   latestPeriod,
		MarketDataDate:        marketData.AsOf,
		DataFreshnessScore:    dataFreshnessScore,
		CalculationMethod:     "multi_stage_dcf",
		CalculationVersion:    "4.0",
	}

	if usingNOPATFallback {
		result.Warnings = append(result.Warnings,
			"FCF using NOPAT approximation (D&A/CapEx unavailable from filing). Valuation may be less accurate for capital-intensive companies.")
	}

	if dcfFallbackWarning != "" {
		result.Warnings = append(result.Warnings, dcfFallbackWarning)
	}

	// Phase 4: Run multiples sanity cross-check to flag extreme divergences.
	// Uses EPS and EBITDA from financials to compute implied P/E and EV/EBITDA,
	// then compares against sector medians.
	if s.industryMultiples != nil {
		eps := 0.0
		if latestFinancialData.NetIncome > 0 && sharesOutstanding > 0 {
			eps = latestFinancialData.NetIncome / sharesOutstanding
		}
		ebitda := latestFinancialData.OperatingIncome + latestFinancialData.DepreciationAndAmortization

		sectorPE := LookupMultiple(s.industryMultiples.SectorMedianPE, industryCode)
		sectorEVEBITDA := LookupMultiple(s.industryMultiples.EVEBITDAMultiples, industryCode)

		sanity := CalculateSanityCheck(
			equityValue, dcfResult.EnterpriseValue,
			eps, ebitda,
			sharesOutstanding,
			industryCode,
			sectorPE, sectorEVEBITDA,
		)
		result.SanityCheck = sanity

		// Propagate sanity check flags as warnings for visibility in the API response
		if !sanity.IsReasonable {
			result.Warnings = append(result.Warnings, sanity.Flags...)
		}
	}

	return result, nil
}

// performAlternativeValuation executes a non-DCF valuation model (DDM, FFO, Revenue Multiple)
// and converts its result to the standard ValuationResult format.
func (s *Service) performAlternativeValuation(
	ctx context.Context,
	model models.ValuationModel,
	historicalData *entities.HistoricalFinancialData,
	marketData *entities.MarketData,
	macroData *entities.MacroData,
	growthEstimate *entities.GrowthEstimate,
	waccResult *wacc.Result,
	latestFinancialData *entities.FinancialData,
	latestPeriod string,
	tangibleValuePerShare float64,
	sharesOutstanding float64,
	industryCode string,
) (*entities.ValuationResult, error) {

	s.logger.Info("Using alternative valuation model",
		zap.String("ticker", historicalData.Ticker),
		zap.String("model_type", model.ModelType()),
		zap.String("industry", industryCode))

	// Build the model input with all pre-computed values
	modelInput := &models.ModelInput{
		HistoricalData:         historicalData,
		MarketData:             marketData,
		MacroData:              macroData,
		GrowthEstimate:         growthEstimate,
		Industry:               industryCode,
		WACC:                   waccResult.WACC,
		CostOfEquity:           waccResult.CostOfEquity,
		TaxRate:                latestFinancialData.TaxRate,
		SharesOutstanding:      sharesOutstanding,
		InterestBearingDebt:    latestFinancialData.InterestBearingDebt,
		CashAndCashEquivalents: latestFinancialData.CashAndCashEquivalents,
	}

	// Execute the alternative model
	modelResult, err := model.Calculate(ctx, modelInput)
	if err != nil {
		// Primary model failed — try fallback strategies before giving up
		s.logger.Warn("Primary valuation model failed, attempting fallback",
			zap.String("model", model.ModelType()),
			zap.String("ticker", historicalData.Ticker),
			zap.Error(err))

		// If company has positive OI, fall back to standard DCF path
		if effectiveOI(latestFinancialData) > 0 {
			return nil, errFallbackToDCF
		}

		// Negative OI — try revenue_multiple as last resort
		revModel := models.NewRevenueMultipleModelWithMultiples(nil, s.logger)
		modelResult, err = revModel.Calculate(ctx, modelInput)
		if err != nil {
			return nil, fmt.Errorf("%w: all models failed for this company", ErrModelNotApplicable)
		}
		modelResult.Warnings = append(modelResult.Warnings,
			fmt.Sprintf("Primary model (%s) failed, used revenue_multiple as fallback", model.ModelType()))
	}

	// Calculate data freshness score
	dataFreshnessScore := s.calculateDataFreshnessScore(latestFinancialData, marketData, macroData)

	// Convert ModelResult to ValuationResult
	result := &entities.ValuationResult{
		Ticker:                historicalData.Ticker,
		CalculatedAt:          time.Now(),
		TangibleValuePerShare: tangibleValuePerShare,
		DCFValuePerShare:      modelResult.IntrinsicValuePerShare,
		WACC:                  waccResult.WACC,
		GrowthRate:            growthEstimate.SummaryGrowthRate(),
		GrowthRates:           growthEstimate.ProjectedGrowthRates,
		TerminalGrowthRate:    growthEstimate.TerminalGrowthRate,
		GrowthSource:          growthEstimate.Source,
		GrowthConfidence:      modelResult.Confidence,
		MarketRiskPremium:     macroData.MarketRiskPremium,
		EnterpriseValue:       modelResult.EnterpriseValue,
		EquityValue:           modelResult.EquityValue,
		FinancialDataPeriod:   latestPeriod,
		MarketDataDate:        marketData.AsOf,
		DataFreshnessScore:    dataFreshnessScore,
		CalculationMethod:     modelResult.ModelType,
		CalculationVersion:    "4.0",
		Warnings:              modelResult.Warnings,
	}

	return result, nil
}

// calculateTangibleValuePerShare calculates the tangible book value per share
func (s *Service) calculateTangibleValuePerShare(financial *entities.FinancialData, market *entities.MarketData) float64 {
	// Calculate tangible equity (total assets - intangibles - liabilities)
	tangibleEquity := financial.TangibleAssets

	// Use market data shares if available, otherwise use financial data
	shares := market.SharesOutstanding
	if shares <= 0 {
		shares = financial.SharesOutstanding
	}

	if shares <= 0 {
		return 0
	}

	return tangibleEquity / shares
}

// calculateTerminalGrowthRate calculates a conservative terminal growth rate.
// It also enforces a minimum 2% spread below WACC to prevent terminal value explosion.
func (s *Service) calculateTerminalGrowthRate(historicalCAGR, wacc float64) float64 {
	// Conservative approach: min of 3% or half of historical CAGR
	terminalGrowth := historicalCAGR / 2
	maxTerminalGrowth := 0.03 // 3%

	if terminalGrowth > maxTerminalGrowth {
		terminalGrowth = maxTerminalGrowth
	}

	if terminalGrowth <= 0 {
		terminalGrowth = 0.02 // Minimum 2% for inflation (viable businesses grow at least with prices)
	}

	// Ensure terminal growth stays at least 2% below WACC to prevent TV explosion
	if wacc > 0 && terminalGrowth > wacc-0.02 {
		terminalGrowth = wacc - 0.02
		if terminalGrowth < 0.01 {
			terminalGrowth = 0.01
		}
	}

	return terminalGrowth
}

// averageCapExAndDA computes the average D&A and CapEx over available annual periods.
// This smooths cyclical spikes (e.g., a single year's massive AI infrastructure buildout)
// that would otherwise make projected FCF negative for all future years.
func (s *Service) averageCapExAndDA(historicalData *entities.HistoricalFinancialData) (avgDA, avgCapEx float64) {
	recentYears := historicalData.GetRecentYears(5)
	var daSum, capexSum float64
	var count float64

	for _, fd := range recentYears {
		if fd.DepreciationAndAmortization > 0 || fd.CapitalExpenditures > 0 {
			daSum += fd.DepreciationAndAmortization
			capexSum += fd.CapitalExpenditures
			count++
		}
	}

	if count == 0 {
		return 0, 0
	}
	return daSum / count, capexSum / count
}

// isFinancialDataIncomplete checks whether stored financial data is missing
// critical FCF fields (D&A, CapEx, Cash). Returns true if ALL are zero/missing,
// which indicates pre-Phase-1.2 data that should be re-fetched.
// effectiveOI returns the best available operating income (normalized preferred, raw as fallback).
func effectiveOI(fd *entities.FinancialData) float64 {
	if fd.NormalizedOperatingIncome > 0 {
		return fd.NormalizedOperatingIncome
	}
	return fd.OperatingIncome
}

func (s *Service) isFinancialDataIncomplete(data *entities.HistoricalFinancialData) bool {
	latest, _ := data.GetLatestPeriod()
	if latest == nil {
		return true
	}
	return latest.DepreciationAndAmortization == 0 &&
		latest.CapitalExpenditures == 0 &&
		latest.CashAndCashEquivalents == 0
}

// calculateDataFreshnessScore calculates a score from 0-100 based on data age
func (s *Service) calculateDataFreshnessScore(financial *entities.FinancialData, market *entities.MarketData, macro *entities.MacroData) int {
	score := 100

	// Reduce score based on financial data age
	financialAge := time.Since(financial.AsOf)
	if financialAge > 90*24*time.Hour { // More than 90 days
		score -= 30
	} else if financialAge > 30*24*time.Hour { // More than 30 days
		score -= 15
	}

	// Reduce score based on market data age
	marketAge := market.GetDataAge()
	if marketAge > 7*24*time.Hour { // More than 7 days
		score -= 20
	} else if marketAge > 24*time.Hour { // More than 1 day
		score -= 10
	}

	// Reduce score based on macro data age
	macroAge := time.Since(macro.AsOf)
	if macroAge > 30*24*time.Hour { // More than 30 days
		score -= 20
	} else if macroAge > 7*24*time.Hour { // More than 7 days
		score -= 10
	}

	if score < 0 {
		score = 0
	}

	return score
}

// calculateNetWorkingCapitalChange computes the change in net working capital
// between the two most recent annual periods. Positive = cash consumed.
func (s *Service) calculateNetWorkingCapitalChange(
	historicalData *entities.HistoricalFinancialData,
	latest *entities.FinancialData,
) float64 {
	if latest.CurrentAssets <= 0 || latest.CurrentLiabilities <= 0 {
		return 0 // data not available
	}

	latestNWC := latest.CurrentAssets - latest.CurrentLiabilities

	// Find prior period to compute delta
	recentYears := historicalData.GetRecentYears(2)
	if len(recentYears) < 2 {
		return 0 // not enough history for delta
	}

	// recentYears[0] is most recent, [1] is prior (sorted descending by GetRecentYears)
	prior := recentYears[1]
	if prior.CurrentAssets <= 0 || prior.CurrentLiabilities <= 0 {
		return 0
	}

	priorNWC := prior.CurrentAssets - prior.CurrentLiabilities
	return latestNWC - priorNWC
}
