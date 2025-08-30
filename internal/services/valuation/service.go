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

// CalculateValuation performs a complete DCF valuation for a ticker
func (s *Service) CalculateValuation(ctx context.Context, ticker string) (*entities.ValuationResult, error) {
	start := time.Now()
	s.logger.Info("Starting valuation calculation", zap.String("ticker", ticker))

	// Check cache first
	cacheKey := fmt.Sprintf("valuation:%s", ticker)
	var cachedResult entities.ValuationResult
	if err := s.cache.Get(ctx, cacheKey, &cachedResult); err == nil {
		s.logger.Info("Returning cached valuation", zap.String("ticker", ticker))
		s.metricsService.RecordValuationRequest(ticker, "single", "cache_hit", time.Since(start))
		return &cachedResult, nil
	}

	// Try to fetch financial data first
	historicalData, err := s.financialRepo.GetHistorical(ctx, ticker, 10) // Get up to 10 periods
	if err != nil || len(historicalData.Data) == 0 {
		s.logger.Info("No historical data in repository, fetching via DataFetcher", zap.String("ticker", ticker))

		// Check if DataFetcher is configured before attempting to use it
		if s.dataFetcher == nil {
			return nil, fmt.Errorf("no historical data found for ticker %s and data fetcher not configured", ticker)
		}

		// Use DataFetcher to fetch and store data
		fetchRequest := &entities.FetchRequest{
			Ticker: ticker,
		}

		_, fetchErr := s.dataFetcher.Fetch(ctx, fetchRequest)
		if fetchErr != nil {
			return nil, fmt.Errorf("failed to fetch data via DataFetcher: %w", fetchErr)
		}

		// DataFetcher should have populated the database, try repository again
		historicalData, err = s.financialRepo.GetHistorical(ctx, ticker, 10)
		if err != nil || len(historicalData.Data) == 0 {
			return nil, fmt.Errorf("failed to fetch financial data: no historical data found for ticker %s", ticker)
		}

		s.logger.Info("Successfully fetched data via DataFetcher",
			zap.String("ticker", ticker),
			zap.Int("periods", len(historicalData.Data)))
	}

	// OPTIMIZATION: Conditionally fetch market and macro data concurrently
	var marketData *entities.MarketData
	var macroData *entities.MacroData

	if s.config.Valuation.EnableConcurrentDataFetch {
		// Concurrent approach for better performance
		type fetchResult struct {
			marketData *entities.MarketData
			macroData  *entities.MacroData
			marketErr  error
			macroErr   error
		}

		resultChan := make(chan fetchResult, 1)

		go func() {
			var result fetchResult

			// Use separate goroutines for truly parallel execution
			marketChan := make(chan struct{})
			macroChan := make(chan struct{})

			go func() {
				defer close(marketChan)
				result.marketData, result.marketErr = s.marketRepo.GetLatest(ctx, ticker)
			}()

			go func() {
				defer close(macroChan)
				result.macroData, result.macroErr = s.macroRepo.GetLatest(ctx)
			}()

			// Wait for both to complete
			<-marketChan
			<-macroChan

			resultChan <- result
		}()

		result := <-resultChan
		marketData = result.marketData
		macroData = result.macroData

		if result.marketErr != nil {
			return nil, fmt.Errorf("failed to fetch market data: %w", result.marketErr)
		}
		if result.macroErr != nil {
			return nil, fmt.Errorf("failed to fetch macro data: %w", result.macroErr)
		}
	} else {
		// Sequential approach (default) for test compatibility
		var err error
		marketData, err = s.marketRepo.GetLatest(ctx, ticker)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch market data: %w", err)
		}

		macroData, err = s.macroRepo.GetLatest(ctx)
		if err != nil {
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

	// Calculate valuation using potentially cleaned data
	result, err := s.performValuation(historicalData, marketData, macroData)
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

	// Cache the result for configurable TTL
	if err := s.cache.Set(ctx, cacheKey, result, s.config.Valuation.CacheTTL); err != nil {
		s.logger.Warn("Failed to cache valuation result", zap.Error(err))
	}

	// Record successful valuation metrics
	s.metricsService.RecordValuationRequest(ticker, "single", "success", time.Since(start))

	s.logger.Info("Valuation calculation completed", zap.String("ticker", ticker), zap.Float64("dcf_value", result.DCFValuePerShare))
	return result, nil
}

// performValuation executes the valuation calculation logic
func (s *Service) performValuation(
	historicalData *entities.HistoricalFinancialData,
	marketData *entities.MarketData,
	macroData *entities.MacroData,
) (*entities.ValuationResult, error) {

	// Validate minimum data requirements
	if !historicalData.HasMinimumData(3) {
		return nil, fmt.Errorf("insufficient financial data: need at least 3 years")
	}

	if !marketData.IsComplete() {
		return nil, fmt.Errorf("incomplete market data")
	}

	if !macroData.IsComplete() {
		return nil, fmt.Errorf("incomplete macro data")
	}

	// Calculate growth rate from historical data
	growthResult, err := historicalData.CalculateAverageGrowthRate(5)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate growth rate: %w", err)
	}

	// Get the latest financial data for asset calculations
	latestFinancialData, latestPeriod := historicalData.GetLatestPeriod()
	if latestFinancialData == nil {
		return nil, fmt.Errorf("no latest financial data available")
	}

	// Calculate tangible value per share
	tangibleValuePerShare := s.calculateTangibleValuePerShare(latestFinancialData, marketData)

	// Calculate WACC
	waccInputs := wacc.Inputs{
		RiskFreeRate:        macroData.GetEffectiveRiskFreeRate(),
		MarketRiskPremium:   macroData.MarketRiskPremium,
		Beta:                marketData.GetEffectiveBeta(),
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

	// Calculate terminal growth rate (conservative approach)
	terminalGrowthRate := s.calculateTerminalGrowthRate(growthResult.GrowthRate)

	// Perform DCF calculation
	dcfInputs := dcf.Inputs{
		BaseOperatingIncome: latestFinancialData.NormalizedOperatingIncome,
		GrowthRate:          growthResult.GrowthRate,
		TerminalGrowthRate:  terminalGrowthRate,
		WACC:                waccResult.WACC,
		ProjectionYears:     5,
		TaxRate:             latestFinancialData.TaxRate,
	}

	dcfResult, err := dcf.CalculateDCF(dcfInputs)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate DCF: %w", err)
	}

	// Record DCF calculation metrics
	s.metricsService.IncDCFCalculations()
	s.metricsService.SetAverageGrowthRate(growthResult.GrowthRate)

	// Calculate per-share values
	sharesOutstanding := marketData.SharesOutstanding
	if sharesOutstanding <= 0 {
		sharesOutstanding = latestFinancialData.SharesOutstanding
	}

	if sharesOutstanding <= 0 {
		return nil, fmt.Errorf("shares outstanding not available")
	}

	dcfValuePerShare := dcfResult.EnterpriseValue / sharesOutstanding

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
		EquityValue:           dcfResult.EnterpriseValue - latestFinancialData.InterestBearingDebt,
		FinancialDataPeriod:   latestPeriod,
		MarketDataDate:        marketData.AsOf,
		DataFreshnessScore:    dataFreshnessScore,
		CalculationVersion:    "1.0",
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

// calculateTerminalGrowthRate calculates a conservative terminal growth rate
func (s *Service) calculateTerminalGrowthRate(historicalCAGR float64) float64 {
	// Conservative approach: min of 3% or half of historical CAGR
	terminalGrowth := historicalCAGR / 2
	maxTerminalGrowth := 0.03 // 3%

	if terminalGrowth > maxTerminalGrowth {
		return maxTerminalGrowth
	}

	if terminalGrowth < 0 {
		return 0.02 // Minimum 2% for inflation
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
