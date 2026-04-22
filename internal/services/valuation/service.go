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
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
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
	calcEmitter        *calclog.Emitter // emits the 12 DCF stage traces (Phase M)
}

// log returns the appropriate logger for the current execution context.
// When ctx carries a request-scoped logger (injected by logctx middleware), that
// logger is returned so all downstream log lines inherit request_id / user_id /
// key_id. When the service is called from the scheduler or startup (plain
// context.Background()), the fx singleton logger is returned as fallback so
// structured logging is never silenced.
func (s *Service) log(ctx context.Context) *zap.Logger {
	return logctx.Or(ctx, s.logger)
}

// NewService creates a new valuation service.
// calcEmitter is injected by the DI container and gates the 12 calculation stage
// traces (Phase M). Pass nil only in tests that do not need calc tracing.
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
	calcEmitter *calclog.Emitter,
) *Service {
	// Build growth estimator from valuation config.
	// Pass calcEmitter so EstimateGrowthRates can emit stage-5 "growth" trace.
	estimatorCfg := growthsvc.DefaultEstimatorConfig()
	if cfg.Valuation.DCFMaxGrowthRate > 0 {
		estimatorCfg.MaxGrowthRate = cfg.Valuation.DCFMaxGrowthRate
	}
	if cfg.Valuation.DCFMinGrowthRate != 0 {
		estimatorCfg.MinGrowthRate = cfg.Valuation.DCFMinGrowthRate
	}

	// Initialize industry classifier for SIC/NAICS-based model selection.
	// ctx is added to Classify (M.1) so the valuation service can emit stage-3
	// "industry_classification" trace after the call returns.
	classifier := industry.NewIndustryClassifier()

	// Initialize model router with all available valuation models.
	// Order matters: more specific models are listed first.
	// Pass calcEmitter so SelectModel emits stage-4 "model_selection" trace.
	allModels := []models.ValuationModel{
		models.NewDDMModel(logger),
		models.NewFFOModel(models.DefaultIndustryMultiplesPath, logger),
		models.NewRevenueMultipleModel(models.DefaultIndustryMultiplesPath, logger),
		models.NewMultiStageDCFModel(logger),
	}
	router := models.NewModelRouter(allModels, logger, calcEmitter)

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
		growthEstimator:    growthsvc.NewEstimator(estimatorCfg, logger, calcEmitter),
		metricsService:     metricsService,
		modelRouter:        router,
		industryClassifier: classifier,
		countryRiskMap:     crpMap,
		industryMultiples:  indMultiples,
		config:             cfg,
		logger:             logger,
		calcEmitter:        calcEmitter,
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
	s.log(ctx).Info("Starting valuation calculation", zap.String("ticker", ticker))

	// Skip cache for requests with overrides — they represent ad-hoc user queries
	// that should not pollute (or be served from) the default-parameter cache.
	hasOverrides := opts != nil && (opts.OverrideBeta != nil || opts.OverrideRiskFree != nil)
	cacheKey := fmt.Sprintf("valuation:v4:%s", ticker)

	if !hasOverrides {
		var cachedResult entities.ValuationResult
		if err := s.cache.Get(ctx, cacheKey, &cachedResult); err == nil {
			s.log(ctx).Info("Returning cached valuation", zap.String("ticker", ticker))
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
		s.log(ctx).Info("No historical data in repository, fetching via DataFetcher", zap.String("ticker", ticker))

		if s.dataFetcher == nil {
			return nil, fmt.Errorf("%w: no historical data and data fetcher not configured for %s", ErrTickerNotFound, ticker)
		}

		fetchResult, fetchErr := s.dataFetcher.Fetch(ctx, &entities.FetchRequest{Ticker: ticker})
		if fetchErr != nil {
			// Defensive: if a direct fetch error ever wraps ErrCompanyFactsNotFound
			// (SEC CIK resolved but XBRL missing), classify as insufficient data
			// rather than a generic 500. In the current flow CoordinateFetch
			// never returns non-nil, so this branch is primarily future-proofing.
			if errors.Is(fetchErr, ports.ErrCompanyFactsNotFound) {
				return nil, fmt.Errorf("%w: no US-GAAP XBRL facts currently available for %s via SEC EDGAR", ErrInsufficientData, ticker)
			}
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
			// No data came back from any source. Distinguish "ticker truly
			// unknown" (404) from "CIK resolved but SEC has no US-GAAP XBRL"
			// (422), because the latter is common for foreign private issuers
			// (20-F filers like Canadian pharmas) and deserves a clearer
			// diagnostic than "ticker not found".
			if hasCompanyFactsNotFoundError(fetchResult.Errors) {
				return nil, fmt.Errorf("%w: no US-GAAP XBRL facts currently available for %s via SEC EDGAR", ErrInsufficientData, ticker)
			}
			return nil, fmt.Errorf("%w: DataFetcher returned no financial data for %s", ErrTickerNotFound, ticker)
		}

		// Use market and macro data from FetchResult if available
		marketData = fetchResult.MarketData
		macroData = fetchResult.MacroData

		s.log(ctx).Info("Successfully fetched data via DataFetcher",
			zap.String("ticker", ticker),
			zap.Int("periods", len(historicalData.Data)),
			zap.Bool("has_market_data", marketData != nil),
			zap.Bool("has_macro_data", macroData != nil))
	} else if s.isFinancialDataIncomplete(historicalData) && s.dataFetcher != nil {
		// Data exists in repo but is incomplete (missing FCF fields like D&A, CapEx, Cash).
		// Re-fetch from SEC to get complete data, then persist the update.
		s.log(ctx).Info("Stored financial data incomplete (missing FCF fields), re-fetching from SEC",
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
					s.log(ctx).Warn("Failed to persist re-fetched financial data",
						zap.String("ticker", ticker), zap.Error(storeErr))
				} else {
					s.log(ctx).Info("Persisted complete financial data to repository",
						zap.String("ticker", ticker),
						zap.Int("periods", len(historicalData.Data)))
				}
			}
		} else {
			s.log(ctx).Warn("Re-fetch failed or returned no data, using incomplete stored data with NOPAT fallback",
				zap.String("ticker", ticker))
		}
	}

	// Fill in market and macro data from repositories if not already set by DataFetcher
	if marketData == nil {
		marketData, err = s.marketRepo.GetLatest(ctx, ticker)
		if err != nil {
			s.log(ctx).Warn("Failed to fetch market data from repository", zap.Error(err), zap.String("ticker", ticker))
			return nil, fmt.Errorf("failed to fetch market data: %w", err)
		}
	}
	if macroData == nil {
		macroData, err = s.macroRepo.GetLatest(ctx)
		if err != nil {
			s.log(ctx).Warn("Failed to fetch macro data from repository", zap.Error(err), zap.String("ticker", ticker))
			return nil, fmt.Errorf("failed to fetch macro data: %w", err)
		}
	}

	// Stage 1 — "data_fetch" calc trace: emit which data sources were consulted and
	// whether they succeeded. This always fires regardless of whether data came from
	// the repository or the DataFetcher, giving operators full acquisition visibility.
	if s.calcEmitter != nil {
		sourcesTried := []string{"financial_repo", "market_repo", "macro_repo"}
		sourcesOk := []string{}
		if len(historicalData.Data) > 0 {
			sourcesOk = append(sourcesOk, "financial_repo")
		}
		if marketData != nil {
			sourcesOk = append(sourcesOk, "market_repo")
		}
		if macroData != nil {
			sourcesOk = append(sourcesOk, "macro_repo")
		}
		s.calcEmitter.Emit(ctx, "data_fetch",
			zap.String("ticker", ticker),
			zap.Strings("sources_tried", sourcesTried),
			zap.Strings("sources_ok", sourcesOk),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	}

	// Apply data cleaning if service is available
	var cleaningResult *entities.CleaningResult
	if s.dataCleaner != nil {
		latest, latestPeriod := historicalData.GetLatestPeriod()
		if latest != nil {
			var err error
			cleaningResult, err = s.dataCleaner.CleanFinancialData(ctx, latest)
			if err != nil {
				s.log(ctx).Warn("Data cleaning failed, using original data",
					zap.Error(err),
					zap.String("ticker", ticker))
			} else {
				s.log(ctx).Info("Data cleaning applied successfully",
					zap.String("ticker", ticker),
					zap.Float64("quality_score", cleaningResult.QualityScore))
				// Update historical data with cleaned data
				historicalData.Data[latestPeriod] = cleaningResult.CleanedData
			}
		}
	} else {
		s.log(ctx).Info("DataCleaner service not available, using original data", zap.String("ticker", ticker))
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
			s.log(ctx).Warn("Failed to cache valuation result", zap.Error(err))
		}
	}

	// Record successful valuation metrics
	s.metricsService.RecordValuationRequest(ticker, "single", "success", time.Since(start))

	s.log(ctx).Info("Valuation calculation completed", zap.String("ticker", ticker), zap.Float64("dcf_value", result.DCFValuePerShare))

	// Stage 12 — "final" calc trace: emit the top-line DCF result so operators can
	// verify the intrinsic value output and its quality metadata end-to-end.
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "final",
			zap.String("ticker", ticker),
			zap.Float64("dcf_per_share", result.DCFValuePerShare),
			zap.Float64("tangible_per_share", result.TangibleValuePerShare),
			zap.String("method", result.CalculationMethod),
			zap.String("version", result.CalculationVersion),
			zap.Float64("quality_score", result.DataQualityScore),
			zap.Int("warnings_count", len(result.Warnings)),
		)
	}

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

	// Fetch analyst consensus estimates with caching (optional, degrades gracefully).
	// Cached separately with 7-day TTL since analyst estimates change infrequently.
	analystData := s.getAnalystEstimates(ctx, historicalData.Ticker)

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

	// Produce multi-stage growth estimate (analyst + historical blend).
	// ctx is passed through so EstimateGrowthRates can emit stage-5 "growth" trace.
	growthEstimate := s.growthEstimator.EstimateGrowthRates(ctx, analystData, historicalGrowth, sustainableGrowth)

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
	// Track each beta stage so we can include all values in the stage-6 "wacc" trace.
	rawBeta := beta
	blumeBeta := wacc.BlumeAdjustedBeta(beta)
	beta = blumeBeta
	unleveredBeta := blumeBeta // default: same as blume if no relever conditions met
	releveredBeta := blumeBeta // default: same as blume if no relever conditions met

	// Unlever beta to remove capital structure effect, then relever at current D/E.
	// For a single company this is near-identity, but it normalizes extreme D/E betas
	// and prepares the pipeline for industry-average beta comparison.
	marketEquity := marketData.CalculateMarketValue()
	if marketEquity > 0 && latestFinancialData.InterestBearingDebt > 0 {
		debtEquityRatio := latestFinancialData.InterestBearingDebt / marketEquity
		unleveredBeta = wacc.UnleveredBeta(blumeBeta, latestFinancialData.TaxRate, debtEquityRatio)
		releveredBeta = wacc.RelleveredBeta(unleveredBeta, latestFinancialData.TaxRate, debtEquityRatio)
		beta = releveredBeta
	}

	s.log(ctx).Debug("Beta adjustments applied",
		zap.String("ticker", historicalData.Ticker),
		zap.Float64("raw_beta", rawBeta),
		zap.Float64("adjusted_beta", beta))

	// Phase 4: Look up country risk premium for international / ADR companies.
	// US-domiciled companies get CRP = 0, so the formula is backward-compatible.
	countryCode := GetCountryForTicker(historicalData.Ticker)
	countryRiskPremium := GetCountryRiskPremium(s.countryRiskMap, countryCode)
	if countryRiskPremium > 0 {
		s.log(ctx).Info("Country risk premium applied",
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

	// Stage 6 — "wacc" calc trace: emit every WACC component so operators can audit
	// the cost-of-capital build-up (beta ladder, risk premiums, debt cost, final rate).
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "wacc",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("rf", riskFreeRate),
			zap.Float64("beta_raw", rawBeta),
			zap.Float64("beta_blume", blumeBeta),
			zap.Float64("beta_unlevered", unleveredBeta),
			zap.Float64("beta_relevered", releveredBeta),
			zap.Float64("erp", macroData.MarketRiskPremium),
			zap.Float64("crp", countryRiskPremium),
			zap.Float64("tax_rate", latestFinancialData.TaxRate),
			zap.Float64("cost_of_debt", waccResult.CostOfDebtAfterTax),
			zap.Float64("wacc", waccResult.WACC),
		)
	}

	// Use terminal growth from the growth estimate, with WACC safety guard
	terminalGrowthRate := s.calculateTerminalGrowthRate(growthEstimate.SummaryGrowthRate(), waccResult.WACC)
	growthEstimate.TerminalGrowthRate = terminalGrowthRate

	// --- Phase 3: Industry-aware model selection ---
	// Classify industry using IndustryClassifier (SIC/NAICS/keyword matching).
	// The IndustryCode on the financial data may already be populated from upstream.
	industryCode := latestFinancialData.IndustryCode
	if industryCode == "" && s.industryClassifier != nil {
		// Use SIC code and company name from SEC data for classification.
		// Falls back to ticker if company name unavailable.
		companyName := historicalData.CompanyName
		if companyName == "" {
			companyName = historicalData.Ticker
		}
		// ctx is passed through for future context-aware tracing inside Classify.
		classified, classifyErr := s.industryClassifier.Classify(ctx, historicalData.SICCode, "", companyName)
		if classifyErr == nil && classified != "" && classified != "NA" {
			industryCode = classified
		}
		s.log(ctx).Debug("Industry classification result",
			zap.String("ticker", historicalData.Ticker),
			zap.String("industry_code", industryCode))
	}

	// Stage 3 — "industry_classification" calc trace: always emitted from here (not
	// inside Classify) so only the valuation pipeline fires it once per valuation —
	// avoids double-emission if Classify is also called from the datacleaner path.
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "industry_classification",
			zap.String("ticker", historicalData.Ticker),
			zap.String("sic", historicalData.SICCode),
			zap.String("naics", ""),
			zap.String("sector", industryCode),
			zap.String("industry", industryCode),
			zap.String("model_hint", industryCode),
		)
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

	// Select the appropriate valuation model based on industry and financials.
	// ctx is passed through so SelectModel emits stage-4 "model_selection" trace.
	selectedModel := s.modelRouter.SelectModel(ctx, industryCode, latestFinancialData)
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
			s.log(ctx).Info("Falling back to standard DCF after alternative model failure",
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
		s.log(ctx).Info("Using true FCF calculation (smoothed over available periods)",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("avg_da", avgDA),
			zap.Float64("avg_capex", avgCapEx),
			zap.Float64("nwc_change", nwcChange))
	} else {
		s.log(ctx).Info("Falling back to NOPAT-based FCF (D&A/CapEx unavailable)",
			zap.String("ticker", historicalData.Ticker))
	}
	usingNOPATFallback := !dcfInputs.UseTrueFCF

	// Phase 4: Wire exit-multiple terminal value from industry config.
	// When available, DCF averages Gordon Growth TV with exit-multiple TV to reduce model risk.
	if s.industryMultiples != nil && s.industryMultiples.EVEBITDAMultiples != nil {
		exitMultiple := LookupMultiple(s.industryMultiples.EVEBITDAMultiples, industryCode)
		if exitMultiple > 0 {
			dcfInputs.ExitMultiple = exitMultiple
			s.log(ctx).Debug("Exit multiple wired for terminal value averaging",
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

	// Stage 7 — "fcf_projection" calc trace: emit per-year growth rates and FCF
	// projections so operators can audit the explicit forecast period.
	if s.calcEmitter != nil {
		fcfSeries := make([]float64, len(dcfResult.Projections))
		for i, p := range dcfResult.Projections {
			fcfSeries[i] = p.FreeCashFlow
		}
		s.calcEmitter.Emit(ctx, "fcf_projection",
			zap.String("ticker", historicalData.Ticker),
			zap.Int("years", dcfResult.ProjectionYears),
			zap.Float64s("growth_rates", growthEstimate.ProjectedGrowthRates),
			zap.Float64s("fcf_series", fcfSeries),
		)
	}

	// Stage 8 — "terminal_value" calc trace: emit terminal-value build-up so operators
	// can see the Gordon Growth vs. exit-multiple averaging outcome.
	if s.calcEmitter != nil {
		// gordonTV = terminalYearFCF * (1 + tg) / (wacc - tg) before exit-multiple averaging.
		// Re-derive it because dcf.Result only exposes the averaged TerminalValueNominal.
		gordonTV := 0.0
		if waccResult.WACC > terminalGrowthRate {
			gordonTV = dcfResult.TerminalYearFCF * (1 + terminalGrowthRate) / (waccResult.WACC - terminalGrowthRate)
		}
		s.calcEmitter.Emit(ctx, "terminal_value",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("gordon_tv", gordonTV),
			zap.Float64("exit_multiple_tv", dcfResult.TerminalValueNominal-gordonTV), // zero when only Gordon used
			zap.Float64("averaged_tv", dcfResult.TerminalValueNominal),
			zap.Float64("terminal_growth", terminalGrowthRate),
		)
	}

	// Stage 9 — "discount" calc trace: emit the PV of explicit period and terminal
	// value to show how enterprise value is assembled from discounted cash flows.
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "discount",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("pv_explicit", dcfResult.ExplicitPeriodValue),
			zap.Float64("pv_terminal", dcfResult.TerminalValue),
			zap.Float64("enterprise_value", dcfResult.EnterpriseValue),
		)
	}

	// Equity value bridge: EV - Debt + Cash = Equity Value
	equityValue := dcf.CalculateEquityValue(
		dcfResult.EnterpriseValue,
		latestFinancialData.InterestBearingDebt,
		latestFinancialData.CashAndCashEquivalents,
	)
	dcfValuePerShare := equityValue / sharesOutstanding

	// Stage 10 — "equity_bridge" calc trace: emit the bridge from enterprise value to
	// per-share intrinsic value so operators can audit the equity conversion step.
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "equity_bridge",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("cash", latestFinancialData.CashAndCashEquivalents),
			zap.Float64("debt", latestFinancialData.InterestBearingDebt),
			zap.Float64("minority_interest", 0), // TODO: add minority interest to FinancialData entity
			zap.Float64("preferred", 0),         // TODO: add preferred equity to FinancialData entity
			zap.Float64("equity_value", equityValue),
			zap.Float64("diluted_shares", sharesOutstanding),
			zap.Float64("per_share", dcfValuePerShare),
		)
	}

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
	// Uses EPS, EBITDA, and FCF from financials to compute implied P/E, EV/EBITDA,
	// and P/FCF, then compares against sector medians.
	if s.industryMultiples != nil {
		eps := 0.0
		if latestFinancialData.NetIncome > 0 && sharesOutstanding > 0 {
			eps = latestFinancialData.NetIncome / sharesOutstanding
		}
		ebitda := latestFinancialData.OperatingIncome + latestFinancialData.DepreciationAndAmortization

		// Calculate FCF per share for P/FCF cross-check.
		// FCF = NetIncome + D&A - CapEx (simplified owner earnings approach).
		fcfPerShare := 0.0
		fcf := latestFinancialData.NetIncome + latestFinancialData.DepreciationAndAmortization - latestFinancialData.CapitalExpenditures
		if fcf > 0 && sharesOutstanding > 0 {
			fcfPerShare = fcf / sharesOutstanding
		}

		sectorPE := LookupMultiple(s.industryMultiples.SectorMedianPE, industryCode)
		sectorEVEBITDA := LookupMultiple(s.industryMultiples.EVEBITDAMultiples, industryCode)
		sectorPFCF := LookupMultiple(s.industryMultiples.SectorMedianPFCF, industryCode)

		sanity := CalculateSanityCheck(
			equityValue, dcfResult.EnterpriseValue,
			eps, ebitda, fcfPerShare,
			sharesOutstanding,
			industryCode,
			sectorPE, sectorEVEBITDA, sectorPFCF,
		)
		result.SanityCheck = sanity

		// Stage 11 — "cross_check" calc trace: emit implied multiples vs. sector medians
		// so operators can see whether the DCF output is in a reasonable range.
		// Only emitted when industryMultiples is configured and the check actually ran.
		if s.calcEmitter != nil {
			s.calcEmitter.Emit(ctx, "cross_check",
				zap.String("ticker", historicalData.Ticker),
				zap.Float64("implied_pe", sanity.ImpliedPE),
				zap.Float64("implied_ev_ebitda", sanity.ImpliedEVEBITDA),
				zap.Float64("sector_median_pe", sanity.SectorMedianPE),
				zap.Float64("sector_median_ev_ebitda", sanity.SectorMedianEVEBITDA),
				zap.Strings("flags", sanity.Flags),
			)
		}

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

	s.log(ctx).Info("Using alternative valuation model",
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
		s.log(ctx).Warn("Primary valuation model failed, attempting fallback",
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

// analystEstimateCacheTTL defines the cache duration for analyst consensus estimates.
// 7 days is appropriate because analyst estimate revisions are infrequent (typically
// quarterly around earnings), and Yahoo Finance rate limits make aggressive fetching costly.
const analystEstimateCacheTTL = 168 * time.Hour // 7 days

// analystEstimateCachePrefix is the versioned cache key prefix for analyst estimates.
const analystEstimateCachePrefix = "analyst:v1:"

// getAnalystEstimates returns analyst consensus estimates for a ticker, using a 7-day
// cache to reduce external API calls. Returns nil when the gateway is not configured
// or when both cache and live fetch fail (graceful degradation).
func (s *Service) getAnalystEstimates(ctx context.Context, ticker string) *ports.YFinanceAnalystEstimates {
	if s.yfinanceGateway == nil {
		return nil
	}

	cacheKey := analystEstimateCachePrefix + ticker

	// Try cache first
	var cached ports.YFinanceAnalystEstimates
	if err := s.cache.Get(ctx, cacheKey, &cached); err == nil {
		s.log(ctx).Debug("Analyst estimates cache hit", zap.String("ticker", ticker))
		return &cached
	}

	// Cache miss — fetch from Yahoo Finance
	estimates, err := s.yfinanceGateway.GetAnalystEstimates(ctx, ticker)
	if err != nil || estimates == nil {
		s.log(ctx).Debug("Failed to fetch analyst estimates, proceeding without",
			zap.String("ticker", ticker), zap.Error(err))
		return nil
	}

	// Store in cache for future requests
	if cacheErr := s.cache.Set(ctx, cacheKey, estimates, analystEstimateCacheTTL); cacheErr != nil {
		s.log(ctx).Warn("Failed to cache analyst estimates",
			zap.String("ticker", ticker), zap.Error(cacheErr))
	}

	s.log(ctx).Debug("Analyst estimates fetched and cached",
		zap.String("ticker", ticker),
		zap.Int("analysts", estimates.NumberOfAnalysts))

	return estimates
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
