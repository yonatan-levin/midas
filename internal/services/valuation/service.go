package valuation

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
	"github.com/midas/dcf-valuation-api/pkg/finance/wacc"
)

// Service provides valuation operations
type Service struct {
	financialRepo ports.FinancialDataRepository
	marketRepo    ports.MarketDataRepository
	macroRepo     ports.MacroDataRepository
	cache         ports.CacheRepository
	logger        *zap.Logger
}

// NewService creates a new valuation service
func NewService(
	financialRepo ports.FinancialDataRepository,
	marketRepo ports.MarketDataRepository,
	macroRepo ports.MacroDataRepository,
	cache ports.CacheRepository,
	logger *zap.Logger,
) *Service {
	return &Service{
		financialRepo: financialRepo,
		marketRepo:    marketRepo,
		macroRepo:     macroRepo,
		cache:         cache,
		logger:        logger,
	}
}

// ValuationResult represents the output of a valuation calculation
type ValuationResult struct {
	Ticker                string    `json:"ticker"`
	CalculatedAt          time.Time `json:"calculated_at"`
	TangibleValuePerShare float64   `json:"tangible_value_per_share"`
	DCFValuePerShare      float64   `json:"dcf_value_per_share"`
	WACC                  float64   `json:"wacc"`
	GrowthRate            float64   `json:"growth_rate"`
	TerminalGrowthRate    float64   `json:"terminal_growth_rate"`
	MarketRiskPremium     float64   `json:"market_risk_premium"`
	EnterpriseValue       float64   `json:"enterprise_value"`
	EquityValue           float64   `json:"equity_value"`
	FinancialDataPeriod   string    `json:"financial_data_period"`
	MarketDataDate        time.Time `json:"market_data_date"`
	DataFreshnessScore    int       `json:"data_freshness_score"`
	CalculationVersion    string    `json:"calculation_version"`
}

// CalculateValuation performs a complete DCF valuation for a ticker
func (s *Service) CalculateValuation(ctx context.Context, ticker string) (*ValuationResult, error) {
	s.logger.Info("Starting valuation calculation", zap.String("ticker", ticker))

	// Check cache first
	cacheKey := fmt.Sprintf("valuation:%s", ticker)
	var cachedResult ValuationResult
	if err := s.cache.Get(ctx, cacheKey, &cachedResult); err == nil {
		s.logger.Info("Returning cached valuation", zap.String("ticker", ticker))
		return &cachedResult, nil
	}

	// Fetch financial data
	historicalData, err := s.financialRepo.GetHistorical(ctx, ticker, 10) // Get up to 10 periods
	if err != nil {
		return nil, fmt.Errorf("failed to fetch financial data: %w", err)
	}

	// Fetch market data
	marketData, err := s.marketRepo.GetLatest(ctx, ticker)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch market data: %w", err)
	}

	// Fetch macro data
	macroData, err := s.macroRepo.GetLatest(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch macro data: %w", err)
	}

	// Calculate valuation
	result, err := s.performValuation(historicalData, marketData, macroData)
	if err != nil {
		return nil, fmt.Errorf("failed to perform valuation: %w", err)
	}

	// Cache the result for 1 hour
	if err := s.cache.Set(ctx, cacheKey, result, 1*time.Hour); err != nil {
		s.logger.Warn("Failed to cache valuation result", zap.Error(err))
	}

	s.logger.Info("Valuation calculation completed", zap.String("ticker", ticker), zap.Float64("dcf_value", result.DCFValuePerShare))
	return result, nil
}

// performValuation executes the valuation calculation logic
func (s *Service) performValuation(
	historicalData *entities.HistoricalFinancialData,
	marketData *entities.MarketData,
	macroData *entities.MacroData,
) (*ValuationResult, error) {

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

	result := &ValuationResult{
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

// calculateCostOfDebt calculates the effective cost of debt
func (s *Service) calculateCostOfDebt(financial *entities.FinancialData) float64 {
	if financial.InterestBearingDebt <= 0 {
		return 0 // No debt
	}

	// Calculate as interest expense / total debt
	return financial.InterestExpense / financial.InterestBearingDebt
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
