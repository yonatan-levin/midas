package valuation

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/midas/dcf-valuation-api/internal/services/datafetcher"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
	"github.com/midas/dcf-valuation-api/pkg/finance/growth"
	"github.com/midas/dcf-valuation-api/pkg/finance/wacc"
)

// Service provides valuation operations
type Service struct {
	financialRepo  ports.FinancialDataRepository
	marketRepo     ports.MarketDataRepository
	macroRepo      ports.MacroDataRepository
	cache          ports.CacheRepository
	dataCleaner    datacleaner.DataCleanerService
	dataFetcher    *datafetcher.DataFetcher
	metricsService ports.MetricsService
	config         *config.Config
	logger         *zap.Logger
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
	return &Service{
		financialRepo:  financialRepo,
		marketRepo:     marketRepo,
		macroRepo:      macroRepo,
		cache:          cache,
		dataCleaner:    dataCleaner,
		dataFetcher:    dataFetcher,
		metricsService: metricsService,
		config:         cfg,
		logger:         logger,
	}
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
	cacheKey := fmt.Sprintf("valuation:%s", ticker)

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
	result, err := s.performValuation(historicalData, marketData, macroData, opts)
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

	// Calculate growth rate from historical data.
	// With < 2 years of data, growth rate calculation will fail — use a conservative default.
	growthResult, err := historicalData.CalculateAverageGrowthRate(5)
	if err != nil {
		// Not enough history for growth calculation — use a conservative default rate
		growthResult = &growth.CalculationResult{
			GrowthRate:  s.config.Valuation.DefaultTerminalGrowthCap, // Use terminal growth cap as conservative estimate
			Method:      "default",
			DataQuality: "low",
			IsReliable:  false,
		}
	}

	// Enforce config-driven growth rate bounds (BUG-010 fix).
	// Only apply when bounds are configured (non-zero); fall back to hardcoded defaults otherwise.
	minGrowth := s.config.Valuation.DCFMinGrowthRate
	maxGrowth := s.config.Valuation.DCFMaxGrowthRate
	if minGrowth == 0 && maxGrowth == 0 {
		minGrowth = -0.3
		maxGrowth = 0.5
	}
	uncappedGrowth := growthResult.GrowthRate
	growthResult.GrowthRate = growth.CapGrowthRateWithBounds(growthResult.GrowthRate, minGrowth, maxGrowth)
	if growthResult.GrowthRate != uncappedGrowth {
		s.logger.Warn("Growth rate capped to configured bounds",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("original", uncappedGrowth),
			zap.Float64("capped", growthResult.GrowthRate),
			zap.Float64("max", maxGrowth),
			zap.Float64("min", minGrowth))
	}

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

	// Calculate WACC
	waccInputs := wacc.Inputs{
		RiskFreeRate:        riskFreeRate,
		MarketRiskPremium:   macroData.MarketRiskPremium,
		Beta:                beta,
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

	// Calculate terminal growth rate (conservative approach, WACC-aware)
	terminalGrowthRate := s.calculateTerminalGrowthRate(growthResult.GrowthRate, waccResult.WACC)

	// Guard: standard DCF requires positive operating income.
	// Companies with negative OI (growth-stage, turnaround) need industry-specific models.
	baseOI := latestFinancialData.NormalizedOperatingIncome
	if baseOI <= 0 {
		baseOI = latestFinancialData.OperatingIncome
	}
	if baseOI <= 0 {
		return nil, fmt.Errorf("%w: company has non-positive operating income (%.2f); standard DCF requires positive operating income", ErrModelNotApplicable, latestFinancialData.NormalizedOperatingIncome)
	}

	// Calculate net working capital change from historical data if available
	nwcChange := s.calculateNetWorkingCapitalChange(historicalData, latestFinancialData)

	// Perform DCF calculation
	dcfInputs := dcf.Inputs{
		BaseOperatingIncome: baseOI,
		GrowthRate:          growthResult.GrowthRate,
		TerminalGrowthRate:  terminalGrowthRate,
		WACC:                waccResult.WACC,
		ProjectionYears:     5,
		TaxRate:             latestFinancialData.TaxRate,
	}

	// Use true FCF when D&A and CapEx data are available
	if latestFinancialData.DepreciationAndAmortization > 0 || latestFinancialData.CapitalExpenditures > 0 {
		dcfInputs.UseTrueFCF = true
		dcfInputs.DepreciationAndAmortization = latestFinancialData.DepreciationAndAmortization
		dcfInputs.CapitalExpenditures = latestFinancialData.CapitalExpenditures
		dcfInputs.NetWorkingCapitalChange = nwcChange
		s.logger.Info("Using true FCF calculation",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("da", latestFinancialData.DepreciationAndAmortization),
			zap.Float64("capex", latestFinancialData.CapitalExpenditures),
			zap.Float64("nwc_change", nwcChange))
	} else {
		s.logger.Info("Falling back to NOPAT-based FCF (D&A/CapEx unavailable)",
			zap.String("ticker", historicalData.Ticker))
	}

	dcfResult, err := dcf.CalculateDCF(dcfInputs)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate DCF: %w", err)
	}

	// Record DCF calculation metrics
	s.metricsService.IncDCFCalculations()
	s.metricsService.SetAverageGrowthRate(growthResult.GrowthRate)

	// Calculate per-share values.
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

	// Equity value bridge: EV - Debt + Cash = Equity Value
	equityValue := dcf.CalculateEquityValue(
		dcfResult.EnterpriseValue,
		latestFinancialData.InterestBearingDebt,
		latestFinancialData.CashAndCashEquivalents,
	)
	dcfValuePerShare := equityValue / sharesOutstanding

	// Calculate data freshness score
	dataFreshnessScore := s.calculateDataFreshnessScore(latestFinancialData, marketData, macroData)

	result := &entities.ValuationResult{
		Ticker:                historicalData.Ticker,
		CalculatedAt:          time.Now(),
		TangibleValuePerShare: tangibleValuePerShare,
		DCFValuePerShare:      dcfValuePerShare,
		WACC:                  waccResult.WACC,
		GrowthRate:            growthResult.GrowthRate,
		TerminalGrowthRate:    terminalGrowthRate,
		MarketRiskPremium:     macroData.MarketRiskPremium,
		EnterpriseValue:       dcfResult.EnterpriseValue,
		EquityValue:           equityValue,
		FinancialDataPeriod:   latestPeriod,
		MarketDataDate:        marketData.AsOf,
		DataFreshnessScore:    dataFreshnessScore,
		CalculationVersion:    "1.1",
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
