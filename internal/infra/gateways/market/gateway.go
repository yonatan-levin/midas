package market

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// Gateway implements the market data gateway interface with fallback sources
type Gateway struct {
	yfinance *YFinanceClient
	// Add Finzive client when implemented
	// finzive  *FinziveClient
	config *config.MarketConfig
	logger *zap.Logger
}

// NewGateway creates a new market data gateway
func NewGateway(cfg *config.MarketConfig, logger *zap.Logger) *Gateway {
	var yfinance *YFinanceClient
	if cfg.YFinance.Enabled {
		yfinance = NewYFinanceClient(&cfg.YFinance, logger)
	}

	// TODO: Initialize Finzive client when implemented
	// var finzive *FinziveClient
	// if cfg.Finzive.Enabled {
	//     finzive = NewFinziveClient(&cfg.Finzive, logger)
	// }

	return &Gateway{
		yfinance: yfinance,
		config:   cfg,
		logger:   logger.Named("market-gateway"),
	}
}

// GetMarketData retrieves current market data for a ticker with fallback
func (g *Gateway) GetMarketData(ctx context.Context, ticker string) (*entities.MarketData, error) {
	g.logger.Debug("Fetching market data", zap.String("ticker", ticker))

	// Try yfinance first
	if g.yfinance != nil {
		marketData, err := g.getMarketDataFromYFinance(ctx, ticker)
		if err == nil {
			g.logger.Debug("Successfully fetched market data from yfinance",
				zap.String("ticker", ticker))
			return marketData, nil
		}
		g.logger.Warn("Failed to fetch from yfinance, trying fallback",
			zap.String("ticker", ticker),
			zap.Error(err))
	}

	// TODO: Try Finzive as fallback
	// if g.finzive != nil {
	//     return g.getMarketDataFromFinzive(ctx, ticker)
	// }

	return nil, fmt.Errorf("failed to fetch market data for %s from all sources", ticker)
}

// GetQuote implements MarketDataGateway interface - alias for GetMarketData
func (g *Gateway) GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error) {
	return g.GetMarketData(ctx, ticker)
}

// GetQuotes implements MarketDataGateway interface - alias for GetBatchMarketData
func (g *Gateway) GetQuotes(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	return g.GetBatchMarketData(ctx, tickers)
}

// GetHistoricalPrices retrieves historical price data for a ticker
func (g *Gateway) GetHistoricalPrices(ctx context.Context, ticker string, startDate, endDate time.Time) ([]*entities.PriceData, error) {
	g.logger.Debug("Fetching historical prices",
		zap.String("ticker", ticker),
		zap.Time("start_date", startDate),
		zap.Time("end_date", endDate))

	if g.yfinance == nil {
		return nil, fmt.Errorf("yfinance client not available")
	}

	// Calculate number of days for the request
	days := int(endDate.Sub(startDate).Hours() / 24)
	if days <= 0 {
		return nil, fmt.Errorf("invalid date range: end date must be after start date")
	}

	// Get historical data from yfinance
	pricePoints, err := g.yfinance.GetHistoricalPrices(ctx, ticker, days)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical prices from yfinance: %w", err)
	}

	// Convert to entities.PriceData format
	result := make([]*entities.PriceData, 0, len(pricePoints))
	for _, point := range pricePoints {
		// Filter by date range
		if point.Date.Before(startDate) || point.Date.After(endDate) {
			continue
		}

		priceData := &entities.PriceData{
			Ticker:   ticker,
			Date:     point.Date,
			Open:     point.Open,
			High:     point.High,
			Low:      point.Low,
			Close:    point.Close,
			Volume:   int64(point.Volume),
			AdjClose: point.Close, // yfinance Close is already adjusted
		}
		result = append(result, priceData)
	}

	g.logger.Debug("Successfully fetched historical prices",
		zap.String("ticker", ticker),
		zap.Int("price_points", len(result)))

	return result, nil
}

// GetBatchMarketData retrieves market data for multiple tickers
func (g *Gateway) GetBatchMarketData(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	if len(tickers) == 0 {
		return make(map[string]*entities.MarketData), nil
	}

	g.logger.Info("Fetching batch market data",
		zap.Strings("tickers", tickers),
		zap.Int("count", len(tickers)))

	results := make(map[string]*entities.MarketData)

	// Try batch request with yfinance first
	if g.yfinance != nil {
		batchResults, err := g.getBatchMarketDataFromYFinance(ctx, tickers)
		if err == nil {
			for ticker, data := range batchResults {
				results[ticker] = data
			}
		} else {
			g.logger.Warn("Batch request failed, falling back to individual requests",
				zap.Error(err))
		}
	}

	// For any tickers that failed batch request, try individual requests
	failedTickers := make([]string, 0)
	for _, ticker := range tickers {
		if _, exists := results[ticker]; !exists {
			failedTickers = append(failedTickers, ticker)
		}
	}

	if len(failedTickers) > 0 {
		g.logger.Debug("Fetching individual data for failed tickers",
			zap.Strings("failed_tickers", failedTickers))

		for _, ticker := range failedTickers {
			if data, err := g.GetMarketData(ctx, ticker); err == nil {
				results[ticker] = data
			} else {
				g.logger.Warn("Failed to fetch market data for ticker",
					zap.String("ticker", ticker),
					zap.Error(err))
			}
		}
	}

	g.logger.Info("Completed batch market data fetch",
		zap.Int("requested", len(tickers)),
		zap.Int("successful", len(results)))

	return results, nil
}

// GetBeta retrieves beta for a ticker
func (g *Gateway) GetBeta(ctx context.Context, ticker string) (float64, error) {
	// Try to get beta from market data first
	marketData, err := g.GetMarketData(ctx, ticker)
	if err == nil && marketData.Beta > 0 {
		return marketData.Beta, nil
	}

	// Fallback: calculate beta from historical prices
	if g.yfinance != nil {
		return g.calculateBetaFromHistoricalData(ctx, ticker)
	}

	return 0, fmt.Errorf("unable to determine beta for %s", ticker)
}

// GetSharePrice retrieves current share price for a ticker
func (g *Gateway) GetSharePrice(ctx context.Context, ticker string) (float64, error) {
	marketData, err := g.GetMarketData(ctx, ticker)
	if err != nil {
		return 0, err
	}
	return marketData.SharePrice, nil
}

// GetSharesOutstanding retrieves shares outstanding for a ticker
func (g *Gateway) GetSharesOutstanding(ctx context.Context, ticker string) (float64, error) {
	marketData, err := g.GetMarketData(ctx, ticker)
	if err != nil {
		return 0, err
	}
	return marketData.SharesOutstanding, nil
}

// getMarketDataFromYFinance fetches market data using Yahoo Finance
func (g *Gateway) getMarketDataFromYFinance(ctx context.Context, ticker string) (*entities.MarketData, error) {
	quote, err := g.yfinance.GetQuote(ctx, ticker)
	if err != nil {
		return nil, err
	}

	marketData := &entities.MarketData{
		Ticker:            ticker,
		SharePrice:        quote.RegularMarketPrice,
		MarketCap:         quote.MarketCap,
		SharesOutstanding: quote.SharesOutstanding,
		Beta:              quote.Beta,
		AverageVolume:     quote.AverageDailyVolume3M,
		AsOf:              time.Unix(quote.RegularMarketTime, 0),
		Source:            "yfinance",
		DataQuality:       g.assessDataQuality(quote),
	}

	// If beta is missing or invalid, try to get it from key statistics
	if marketData.Beta <= 0 {
		if keyStats, err := g.yfinance.GetKeyStatistics(ctx, ticker); err == nil {
			if keyStats.Beta > 0 {
				marketData.Beta = keyStats.Beta
			} else if keyStats.Beta3Year > 0 {
				marketData.Beta = keyStats.Beta3Year
			}
		}
	}

	// If shares outstanding is missing, try key statistics
	if marketData.SharesOutstanding <= 0 {
		if keyStats, err := g.yfinance.GetKeyStatistics(ctx, ticker); err == nil {
			if keyStats.SharesOutstanding > 0 {
				marketData.SharesOutstanding = keyStats.SharesOutstanding
			}
		}
	}

	return marketData, nil
}

// getBatchMarketDataFromYFinance fetches batch market data using Yahoo Finance
func (g *Gateway) getBatchMarketDataFromYFinance(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	quotes, err := g.yfinance.GetBatchQuotes(ctx, tickers)
	if err != nil {
		return nil, err
	}

	results := make(map[string]*entities.MarketData)
	for ticker, quote := range quotes {
		marketData := &entities.MarketData{
			Ticker:            ticker,
			SharePrice:        quote.RegularMarketPrice,
			MarketCap:         quote.MarketCap,
			SharesOutstanding: quote.SharesOutstanding,
			Beta:              quote.Beta,
			AverageVolume:     quote.AverageDailyVolume3M,
			AsOf:              time.Unix(quote.RegularMarketTime, 0),
			Source:            "yfinance",
			DataQuality:       g.assessDataQuality(quote),
		}
		results[ticker] = marketData
	}

	return results, nil
}

// calculateBetaFromHistoricalData calculates beta using historical price data
func (g *Gateway) calculateBetaFromHistoricalData(ctx context.Context, ticker string) (float64, error) {
	// Get 1 year of historical data for the stock
	stockPrices, err := g.yfinance.GetHistoricalPrices(ctx, ticker, 252) // ~1 year trading days
	if err != nil {
		return 0, err
	}

	// Get market data (S&P 500 as proxy)
	marketPrices, err := g.yfinance.GetHistoricalPrices(ctx, "^GSPC", 252)
	if err != nil {
		return 0, err
	}

	if len(stockPrices) < 30 || len(marketPrices) < 30 {
		return 0, fmt.Errorf("insufficient historical data for beta calculation")
	}

	// Calculate daily returns
	stockReturns := g.calculateDailyReturns(stockPrices)
	marketReturns := g.calculateDailyReturns(marketPrices)

	// Align the data (use the shorter series)
	minLen := len(stockReturns)
	if len(marketReturns) < minLen {
		minLen = len(marketReturns)
	}

	if minLen < 20 {
		return 0, fmt.Errorf("insufficient aligned data for beta calculation")
	}

	// Calculate beta using covariance and variance
	stockReturns = stockReturns[:minLen]
	marketReturns = marketReturns[:minLen]

	covariance := g.calculateCovariance(stockReturns, marketReturns)
	marketVariance := g.calculateVariance(marketReturns)

	if marketVariance == 0 {
		return 1.0, nil // Default beta if market variance is zero
	}

	beta := covariance / marketVariance

	// Sanity check: beta should typically be between -3 and 3
	if beta < -3 || beta > 3 {
		g.logger.Warn("Calculated beta is outside normal range",
			zap.String("ticker", ticker),
			zap.Float64("beta", beta))
	}

	return beta, nil
}

// calculateDailyReturns calculates daily returns from price data
func (g *Gateway) calculateDailyReturns(prices []ports.YFinancePricePoint) []float64 {
	if len(prices) < 2 {
		return []float64{}
	}

	returns := make([]float64, len(prices)-1)
	for i := 1; i < len(prices); i++ {
		if prices[i-1].Close > 0 {
			returns[i-1] = (prices[i].Close - prices[i-1].Close) / prices[i-1].Close
		}
	}
	return returns
}

// calculateCovariance calculates covariance between two return series
func (g *Gateway) calculateCovariance(x, y []float64) float64 {
	if len(x) != len(y) || len(x) == 0 {
		return 0
	}

	meanX := g.calculateMean(x)
	meanY := g.calculateMean(y)

	sum := 0.0
	for i := 0; i < len(x); i++ {
		sum += (x[i] - meanX) * (y[i] - meanY)
	}

	return sum / float64(len(x)-1)
}

// calculateVariance calculates variance of a return series
func (g *Gateway) calculateVariance(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}

	mean := g.calculateMean(x)
	sum := 0.0
	for _, val := range x {
		sum += math.Pow(val-mean, 2)
	}

	return sum / float64(len(x)-1)
}

// calculateMean calculates the mean of a series
func (g *Gateway) calculateMean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}

	sum := 0.0
	for _, val := range x {
		sum += val
	}

	return sum / float64(len(x))
}

// assessDataQuality assesses the quality of market data
func (g *Gateway) assessDataQuality(quote *ports.YFinanceQuote) string {
	missing := 0
	total := 5

	if quote.RegularMarketPrice <= 0 {
		missing++
	}
	if quote.SharesOutstanding <= 0 {
		missing++
	}
	if quote.Beta <= 0 {
		missing++
	}
	if quote.MarketCap <= 0 {
		missing++
	}
	if quote.AverageDailyVolume3M <= 0 {
		missing++
	}

	qualityScore := float64(total-missing) / float64(total)

	if qualityScore >= 0.8 {
		return "high"
	} else if qualityScore >= 0.6 {
		return "medium"
	} else {
		return "low"
	}
}

// HealthCheck performs a health check on the market data gateway
func (g *Gateway) HealthCheck(ctx context.Context) error {
	if g.yfinance != nil {
		if err := g.yfinance.HealthCheck(ctx); err != nil {
			return fmt.Errorf("yfinance health check failed: %w", err)
		}
	}

	// TODO: Add Finzive health check when implemented

	return nil
}
