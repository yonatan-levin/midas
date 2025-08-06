package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// YFinanceClient implements Yahoo Finance API client
type YFinanceClient struct {
	httpClient *http.Client
	config     *config.YFinanceConfig
	logger     *zap.Logger
	baseURL    string
}

// NewYFinanceClient creates a new Yahoo Finance client
func NewYFinanceClient(cfg *config.YFinanceConfig, logger *zap.Logger) *YFinanceClient {
	httpClient := &http.Client{
		Timeout: cfg.RequestTimeout,
		Transport: &http.Transport{
			MaxIdleConns:       20,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: false,
		},
	}

	return &YFinanceClient{
		httpClient: httpClient,
		config:     cfg,
		logger:     logger.Named("yfinance-client"),
		baseURL:    cfg.BaseURL,
	}
}

// GetQuote retrieves current quote data for a ticker
func (c *YFinanceClient) GetQuote(ctx context.Context, ticker string) (*ports.YFinanceQuote, error) {
	c.logger.Debug("Fetching quote", zap.String("ticker", ticker))

	// Yahoo Finance v7 API endpoint
	endpoint := fmt.Sprintf("%s/v7/finance/quote", c.baseURL)

	// Build query parameters
	params := url.Values{}
	params.Set("symbols", ticker)
	params.Set("fields", "regularMarketPrice,marketCap,sharesOutstanding,regularMarketVolume,averageDailyVolume3Month,beta,currency,marketState,regularMarketTime")

	url := fmt.Sprintf("%s?%s", endpoint, params.Encode())

	var result *YFinanceQuoteResponse
	var err error

	// Implement retry logic
	for attempt := 0; attempt < c.config.MaxRetries; attempt++ {
		result, err = c.makeQuoteRequest(ctx, url)
		if err == nil {
			break
		}

		if attempt < c.config.MaxRetries-1 {
			backoff := time.Duration(attempt+1) * time.Second
			c.logger.Warn("Quote request failed, retrying",
				zap.String("ticker", ticker),
				zap.Int("attempt", attempt+1),
				zap.Duration("backoff", backoff),
				zap.Error(err))

			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to fetch quote for %s: %w", ticker, err)
	}

	if len(result.QuoteResponse.Result) == 0 {
		return nil, fmt.Errorf("no quote data found for ticker %s", ticker)
	}

	quote := result.QuoteResponse.Result[0]
	c.logger.Debug("Successfully fetched quote",
		zap.String("ticker", ticker),
		zap.Float64("price", quote.RegularMarketPrice))

	return &quote, nil
}

// GetBatchQuotes retrieves quotes for multiple tickers
func (c *YFinanceClient) GetBatchQuotes(ctx context.Context, tickers []string) (map[string]*ports.YFinanceQuote, error) {
	if len(tickers) == 0 {
		return make(map[string]*ports.YFinanceQuote), nil
	}

	c.logger.Debug("Fetching batch quotes", zap.Strings("tickers", tickers))

	// Yahoo Finance supports batch requests
	endpoint := fmt.Sprintf("%s/v7/finance/quote", c.baseURL)

	// Build query parameters
	params := url.Values{}
	params.Set("symbols", strings.Join(tickers, ","))
	params.Set("fields", "regularMarketPrice,marketCap,sharesOutstanding,regularMarketVolume,averageDailyVolume3Month,beta,currency,marketState,regularMarketTime")

	url := fmt.Sprintf("%s?%s", endpoint, params.Encode())

	result, err := c.makeQuoteRequest(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch batch quotes: %w", err)
	}

	quotes := make(map[string]*ports.YFinanceQuote)
	for _, quote := range result.QuoteResponse.Result {
		quotes[quote.Symbol] = &quote
	}

	c.logger.Info("Successfully fetched batch quotes",
		zap.Int("requested", len(tickers)),
		zap.Int("received", len(quotes)))

	return quotes, nil
}

// GetKeyStatistics retrieves key statistics including beta and shares outstanding
func (c *YFinanceClient) GetKeyStatistics(ctx context.Context, ticker string) (*ports.YFinanceKeyStats, error) {
	c.logger.Debug("Fetching key statistics", zap.String("ticker", ticker))

	// Yahoo Finance v10 API endpoint for key statistics
	endpoint := fmt.Sprintf("%s/v10/finance/quoteSummary/%s", c.baseURL, ticker)

	params := url.Values{}
	params.Set("modules", "defaultKeyStatistics,financialData")

	url := fmt.Sprintf("%s?%s", endpoint, params.Encode())

	result, err := c.makeKeyStatsRequest(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch key statistics for %s: %w", ticker, err)
	}

	stats := &ports.YFinanceKeyStats{}

	// Extract data from the response
	if keyStats := result.QuoteSummary.Result[0].DefaultKeyStatistics; keyStats != nil {
		if keyStats.Beta.Raw != nil {
			stats.Beta = *keyStats.Beta.Raw
		}
		if keyStats.SharesOutstanding.Raw != nil {
			stats.SharesOutstanding = *keyStats.SharesOutstanding.Raw
		}
		if keyStats.FloatShares.Raw != nil {
			stats.SharesFloat = *keyStats.FloatShares.Raw
		}
		if keyStats.BookValue.Raw != nil {
			stats.BookValue = *keyStats.BookValue.Raw
		}
		if keyStats.PriceToBook.Raw != nil {
			stats.PriceToBook = *keyStats.PriceToBook.Raw
		}
		if keyStats.EnterpriseValue.Raw != nil {
			stats.EnterpriseValue = *keyStats.EnterpriseValue.Raw
		}
		if keyStats.TotalCash.Raw != nil {
			stats.TotalCash = *keyStats.TotalCash.Raw
		}
		if keyStats.TotalDebt.Raw != nil {
			stats.TotalDebt = *keyStats.TotalDebt.Raw
		}
	}

	if financialData := result.QuoteSummary.Result[0].FinancialData; financialData != nil {
		// nolint:staticcheck // placeholder until detailed price parsing
		if financialData.CurrentPrice.Raw != nil {
		}
	}

	c.logger.Debug("Successfully fetched key statistics",
		zap.String("ticker", ticker),
		zap.Float64("beta", stats.Beta),
		zap.Float64("shares_outstanding", stats.SharesOutstanding))

	return stats, nil
}

// GetHistoricalPrices retrieves historical price data for beta calculation
func (c *YFinanceClient) GetHistoricalPrices(ctx context.Context, ticker string, days int) ([]ports.YFinancePricePoint, error) {
	c.logger.Debug("Fetching historical prices",
		zap.String("ticker", ticker),
		zap.Int("days", days))

	// Calculate time range
	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -days)

	// Yahoo Finance v8 API endpoint for historical data
	endpoint := fmt.Sprintf("%s/v8/finance/chart/%s", c.baseURL, ticker)

	params := url.Values{}
	params.Set("period1", strconv.FormatInt(startTime.Unix(), 10))
	params.Set("period2", strconv.FormatInt(endTime.Unix(), 10))
	params.Set("interval", "1d")
	params.Set("includePrePost", "false")

	url := fmt.Sprintf("%s?%s", endpoint, params.Encode())

	result, err := c.makeHistoricalRequest(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch historical prices for %s: %w", ticker, err)
	}

	if len(result.Chart.Result) == 0 {
		return nil, fmt.Errorf("no historical data found for ticker %s", ticker)
	}

	chartData := result.Chart.Result[0]
	prices := make([]ports.YFinancePricePoint, 0, len(chartData.Timestamp))

	for i, timestamp := range chartData.Timestamp {
		if i >= len(chartData.Indicators.Quote[0].Open) {
			break
		}

		point := ports.YFinancePricePoint{
			Date:   time.Unix(timestamp, 0),
			Open:   chartData.Indicators.Quote[0].Open[i],
			High:   chartData.Indicators.Quote[0].High[i],
			Low:    chartData.Indicators.Quote[0].Low[i],
			Close:  chartData.Indicators.Quote[0].Close[i],
			Volume: chartData.Indicators.Quote[0].Volume[i],
		}
		prices = append(prices, point)
	}

	c.logger.Info("Successfully fetched historical prices",
		zap.String("ticker", ticker),
		zap.Int("days_requested", days),
		zap.Int("points_received", len(prices)))

	return prices, nil
}

// makeQuoteRequest executes a quote request
func (c *YFinanceClient) makeQuoteRequest(ctx context.Context, url string) (*YFinanceQuoteResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Midas/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yahoo finance API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result YFinanceQuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// makeKeyStatsRequest executes a key statistics request
func (c *YFinanceClient) makeKeyStatsRequest(ctx context.Context, url string) (*YFinanceKeyStatsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Midas/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yahoo finance API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result YFinanceKeyStatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// makeHistoricalRequest executes a historical data request
func (c *YFinanceClient) makeHistoricalRequest(ctx context.Context, url string) (*YFinanceHistoricalResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Midas/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yahoo finance API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result YFinanceHistoricalResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// Response structures for Yahoo Finance API

type YFinanceQuoteResponse struct {
	QuoteResponse struct {
		Result []ports.YFinanceQuote `json:"result"`
		Error  interface{}           `json:"error"`
	} `json:"quoteResponse"`
}

type YFinanceKeyStatsResponse struct {
	QuoteSummary struct {
		Result []struct {
			DefaultKeyStatistics *struct {
				Beta              *YFinanceValue `json:"beta"`
				SharesOutstanding *YFinanceValue `json:"sharesOutstanding"`
				FloatShares       *YFinanceValue `json:"floatShares"`
				BookValue         *YFinanceValue `json:"bookValue"`
				PriceToBook       *YFinanceValue `json:"priceToBook"`
				EnterpriseValue   *YFinanceValue `json:"enterpriseValue"`
				TotalCash         *YFinanceValue `json:"totalCash"`
				TotalDebt         *YFinanceValue `json:"totalDebt"`
			} `json:"defaultKeyStatistics"`
			FinancialData *struct {
				CurrentPrice *YFinanceValue `json:"currentPrice"`
			} `json:"financialData"`
		} `json:"result"`
		Error interface{} `json:"error"`
	} `json:"quoteSummary"`
}

type YFinanceHistoricalResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency string `json:"currency"`
				Symbol   string `json:"symbol"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []float64 `json:"open"`
					High   []float64 `json:"high"`
					Low    []float64 `json:"low"`
					Close  []float64 `json:"close"`
					Volume []float64 `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error interface{} `json:"error"`
	} `json:"chart"`
}

type YFinanceValue struct {
	Raw *float64 `json:"raw"`
	Fmt string   `json:"fmt"`
}

// HealthCheck performs a health check on the Yahoo Finance API
func (c *YFinanceClient) HealthCheck(ctx context.Context) error {
	// Try to fetch a quote for a well-known ticker
	_, err := c.GetQuote(ctx, "AAPL")
	if err != nil {
		return fmt.Errorf("yahoo finance API health check failed: %w", err)
	}
	return nil
}
