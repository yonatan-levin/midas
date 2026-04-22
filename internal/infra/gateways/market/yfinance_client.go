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
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// errUnauthorized is a sentinel used to detect 401 responses for auth refresh.
type errUnauthorized struct {
	msg string
}

func (e *errUnauthorized) Error() string { return e.msg }

// YFinanceClient implements Yahoo Finance API client
type YFinanceClient struct {
	httpClient *http.Client
	config     *config.YFinanceConfig
	auth       *YFinanceAuth
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

	// Determine auth URLs: use config values, fall back to sensible defaults
	cookieURL := cfg.CookieURL
	if cookieURL == "" {
		cookieURL = "https://fc.yahoo.com"
	}
	crumbURL := cfg.CrumbURL
	if crumbURL == "" {
		crumbURL = "https://query2.finance.yahoo.com/v1/test/getcrumb"
	}
	authTTL := cfg.AuthTTL
	if authTTL <= 0 {
		authTTL = 6 * time.Hour
	}

	// The auth manager uses its own HTTP client without the short request timeout,
	// since auth fetches are separate from data requests.
	authHTTPClient := &http.Client{
		Timeout: 30 * time.Second,
		// Do NOT follow redirects automatically — we need to capture cookies from
		// the initial response before any redirects.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	auth := NewYFinanceAuth(authHTTPClient, cookieURL, crumbURL, authTTL, logger)

	return &YFinanceClient{
		httpClient: httpClient,
		config:     cfg,
		auth:       auth,
		logger:     logger.Named("yfinance-client"),
		baseURL:    cfg.BaseURL,
	}
}

// GetQuote retrieves current quote data for a ticker
func (c *YFinanceClient) GetQuote(ctx context.Context, ticker string) (*ports.YFinanceQuote, error) {
	logctx.Or(ctx, c.logger).Debug("Fetching quote", zap.String("ticker", ticker))

	// Yahoo Finance v7 API endpoint
	endpoint := fmt.Sprintf("%s/v7/finance/quote", c.baseURL)

	// Build query parameters
	params := url.Values{}
	params.Set("symbols", ticker)
	params.Set("fields", "regularMarketPrice,marketCap,sharesOutstanding,regularMarketVolume,averageDailyVolume3Month,beta,currency,marketState,regularMarketTime")

	url := fmt.Sprintf("%s?%s", endpoint, params.Encode())

	var result *YFinanceQuoteResponse
	var err error

	// Implement retry logic with auth refresh on 401
	for attempt := 0; attempt < c.config.MaxRetries; attempt++ {
		result, err = c.makeQuoteRequest(ctx, url)
		if err == nil {
			break
		}

		// If we got a 401, invalidate auth so the next attempt uses fresh credentials
		if _, ok := err.(*errUnauthorized); ok {
			logctx.Or(ctx, c.logger).Warn("Got 401 from Yahoo Finance, refreshing auth",
				zap.String("ticker", ticker),
				zap.Int("attempt", attempt+1))
			c.auth.Invalidate()
		}

		if attempt < c.config.MaxRetries-1 {
			backoff := time.Duration(attempt+1) * time.Second
			logctx.Or(ctx, c.logger).Warn("Quote request failed, retrying",
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
	logctx.Or(ctx, c.logger).Debug("Successfully fetched quote",
		zap.String("ticker", ticker),
		zap.Float64("price", quote.RegularMarketPrice))

	return &quote, nil
}

// GetBatchQuotes retrieves quotes for multiple tickers
func (c *YFinanceClient) GetBatchQuotes(ctx context.Context, tickers []string) (map[string]*ports.YFinanceQuote, error) {
	if len(tickers) == 0 {
		return make(map[string]*ports.YFinanceQuote), nil
	}

	logctx.Or(ctx, c.logger).Debug("Fetching batch quotes", zap.Strings("tickers", tickers))

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

	logctx.Or(ctx, c.logger).Info("Successfully fetched batch quotes",
		zap.Int("requested", len(tickers)),
		zap.Int("received", len(quotes)))

	return quotes, nil
}

// GetKeyStatistics retrieves key statistics including beta and shares outstanding
func (c *YFinanceClient) GetKeyStatistics(ctx context.Context, ticker string) (*ports.YFinanceKeyStats, error) {
	logctx.Or(ctx, c.logger).Debug("Fetching key statistics", zap.String("ticker", ticker))

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

	// Extract data from the response.
	// Each field is a *YFinanceValue which may be nil when Yahoo omits it,
	// so we must nil-check the outer pointer before accessing .Raw.
	if keyStats := result.QuoteSummary.Result[0].DefaultKeyStatistics; keyStats != nil {
		if keyStats.Beta != nil && keyStats.Beta.Raw != nil {
			stats.Beta = *keyStats.Beta.Raw
		}
		if keyStats.SharesOutstanding != nil && keyStats.SharesOutstanding.Raw != nil {
			stats.SharesOutstanding = *keyStats.SharesOutstanding.Raw
		}
		if keyStats.FloatShares != nil && keyStats.FloatShares.Raw != nil {
			stats.SharesFloat = *keyStats.FloatShares.Raw
		}
		if keyStats.BookValue != nil && keyStats.BookValue.Raw != nil {
			stats.BookValue = *keyStats.BookValue.Raw
		}
		if keyStats.PriceToBook != nil && keyStats.PriceToBook.Raw != nil {
			stats.PriceToBook = *keyStats.PriceToBook.Raw
		}
		if keyStats.EnterpriseValue != nil && keyStats.EnterpriseValue.Raw != nil {
			stats.EnterpriseValue = *keyStats.EnterpriseValue.Raw
		}
		if keyStats.TotalCash != nil && keyStats.TotalCash.Raw != nil {
			stats.TotalCash = *keyStats.TotalCash.Raw
		}
		if keyStats.TotalDebt != nil && keyStats.TotalDebt.Raw != nil {
			stats.TotalDebt = *keyStats.TotalDebt.Raw
		}
	}

	if financialData := result.QuoteSummary.Result[0].FinancialData; financialData != nil {
		if financialData.CurrentPrice != nil && financialData.CurrentPrice.Raw != nil {
			// Price data available from financial data module
			_ = *financialData.CurrentPrice.Raw
		}
	}

	logctx.Or(ctx, c.logger).Debug("Successfully fetched key statistics",
		zap.String("ticker", ticker),
		zap.Float64("beta", stats.Beta),
		zap.Float64("shares_outstanding", stats.SharesOutstanding))

	return stats, nil
}

// GetHistoricalPrices retrieves historical price data for beta calculation
func (c *YFinanceClient) GetHistoricalPrices(ctx context.Context, ticker string, days int) ([]ports.YFinancePricePoint, error) {
	logctx.Or(ctx, c.logger).Debug("Fetching historical prices",
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

	logctx.Or(ctx, c.logger).Info("Successfully fetched historical prices",
		zap.String("ticker", ticker),
		zap.Int("days_requested", days),
		zap.Int("points_received", len(prices)))

	return prices, nil
}

// makeQuoteRequest executes a quote request with cookie+crumb auth.
func (c *YFinanceClient) makeQuoteRequest(ctx context.Context, reqURL string) (*YFinanceQuoteResponse, error) {
	// Ensure we have valid auth credentials
	if err := c.auth.EnsureAuth(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate with Yahoo Finance: %w", err)
	}

	// Append crumb parameter to URL
	reqURL = c.appendCrumb(reqURL)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errUnauthorized{msg: fmt.Sprintf("yahoo finance API returned 401: %s", string(body))}
	}

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

// makeKeyStatsRequest executes a key statistics request with cookie+crumb auth.
func (c *YFinanceClient) makeKeyStatsRequest(ctx context.Context, reqURL string) (*YFinanceKeyStatsResponse, error) {
	if err := c.auth.EnsureAuth(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate with Yahoo Finance: %w", err)
	}

	reqURL = c.appendCrumb(reqURL)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errUnauthorized{msg: fmt.Sprintf("yahoo finance API returned 401: %s", string(body))}
	}

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

// makeHistoricalRequest executes a historical data request with cookie+crumb auth.
func (c *YFinanceClient) makeHistoricalRequest(ctx context.Context, reqURL string) (*YFinanceHistoricalResponse, error) {
	if err := c.auth.EnsureAuth(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate with Yahoo Finance: %w", err)
	}

	reqURL = c.appendCrumb(reqURL)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		return nil, &errUnauthorized{msg: fmt.Sprintf("yahoo finance API returned 401: %s", string(body))}
	}

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

// applyAuth sets the auth headers (cookies and User-Agent) on a request.
func (c *YFinanceClient) applyAuth(req *http.Request) {
	c.auth.ApplyCookies(req)
	req.Header.Set("User-Agent", yahooUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://finance.yahoo.com")
}

// appendCrumb adds the crumb query parameter to a URL.
func (c *YFinanceClient) appendCrumb(reqURL string) string {
	crumb := c.auth.GetCrumb()
	if crumb == "" {
		return reqURL
	}
	separator := "&"
	if !strings.Contains(reqURL, "?") {
		separator = "?"
	}
	return reqURL + separator + "crumb=" + url.QueryEscape(crumb)
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

// YFinanceEarningsTrendResponse represents the earningsTrend module response
type YFinanceEarningsTrendResponse struct {
	QuoteSummary struct {
		Result []struct {
			EarningsTrend *struct {
				Trend []struct {
					Period          string `json:"period"` // "0q", "+1q", "0y", "+1y", "+5y"
					RevenueEstimate *struct {
						Avg              *YFinanceValue `json:"avg"`
						Low              *YFinanceValue `json:"low"`
						High             *YFinanceValue `json:"high"`
						NumberOfAnalysts *YFinanceValue `json:"numberOfAnalysts"`
					} `json:"revenueEstimate"`
					EarningsEstimate *struct {
						Avg              *YFinanceValue `json:"avg"`
						NumberOfAnalysts *YFinanceValue `json:"numberOfAnalysts"`
					} `json:"earningsEstimate"`
					Growth *YFinanceValue `json:"growth"`
				} `json:"trend"`
			} `json:"earningsTrend"`
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

// GetAnalystEstimates retrieves analyst consensus growth estimates from the earningsTrend module.
// Returns nil (not error) when no analyst data is available (micro-caps, foreign tickers).
func (c *YFinanceClient) GetAnalystEstimates(ctx context.Context, ticker string) (*ports.YFinanceAnalystEstimates, error) {
	logctx.Or(ctx, c.logger).Debug("Fetching analyst estimates", zap.String("ticker", ticker))

	endpoint := fmt.Sprintf("%s/v10/finance/quoteSummary/%s", c.baseURL, ticker)

	params := url.Values{}
	params.Set("modules", "earningsTrend")

	reqURL := fmt.Sprintf("%s?%s", endpoint, params.Encode())

	// Reuse makeKeyStatsRequest but decode into EarningsTrend response
	resp, err := c.makeEarningsTrendRequest(ctx, reqURL)
	if err != nil {
		logctx.Or(ctx, c.logger).Warn("Failed to fetch analyst estimates, will use historical growth only",
			zap.String("ticker", ticker), zap.Error(err))
		return nil, nil // Graceful degradation — not an error
	}

	if len(resp.QuoteSummary.Result) == 0 || resp.QuoteSummary.Result[0].EarningsTrend == nil {
		return nil, nil // No analyst data available
	}

	trend := resp.QuoteSummary.Result[0].EarningsTrend.Trend
	estimates := &ports.YFinanceAnalystEstimates{}

	for _, entry := range trend {
		switch entry.Period {
		case "0y": // Current year
			if entry.RevenueEstimate != nil {
				if entry.RevenueEstimate.Avg != nil && entry.RevenueEstimate.Avg.Raw != nil {
					estimates.RevenueEstimateCurrentYear = *entry.RevenueEstimate.Avg.Raw
				}
				if entry.RevenueEstimate.Low != nil && entry.RevenueEstimate.Low.Raw != nil {
					estimates.RevenueEstimateLow = *entry.RevenueEstimate.Low.Raw
				}
				if entry.RevenueEstimate.High != nil && entry.RevenueEstimate.High.Raw != nil {
					estimates.RevenueEstimateHigh = *entry.RevenueEstimate.High.Raw
				}
				if entry.RevenueEstimate.NumberOfAnalysts != nil && entry.RevenueEstimate.NumberOfAnalysts.Raw != nil {
					estimates.NumberOfAnalysts = int(*entry.RevenueEstimate.NumberOfAnalysts.Raw)
				}
			}
		case "+1y": // Next year
			if entry.RevenueEstimate != nil && entry.RevenueEstimate.Avg != nil && entry.RevenueEstimate.Avg.Raw != nil {
				estimates.RevenueEstimateNextYear = *entry.RevenueEstimate.Avg.Raw
			}
		case "+5y": // 5-year growth estimate
			if entry.Growth != nil && entry.Growth.Raw != nil {
				estimates.EarningsGrowth5Year = *entry.Growth.Raw
			}
		}
	}

	logctx.Or(ctx, c.logger).Debug("Successfully fetched analyst estimates",
		zap.String("ticker", ticker),
		zap.Int("analysts", estimates.NumberOfAnalysts),
		zap.Float64("5y_growth", estimates.EarningsGrowth5Year))

	return estimates, nil
}

// makeEarningsTrendRequest executes an earningsTrend request with cookie+crumb auth.
func (c *YFinanceClient) makeEarningsTrendRequest(ctx context.Context, reqURL string) (*YFinanceEarningsTrendResponse, error) {
	if err := c.auth.EnsureAuth(ctx); err != nil {
		return nil, fmt.Errorf("failed to authenticate with Yahoo Finance: %w", err)
	}

	reqURL = c.appendCrumb(reqURL)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo finance API returned status %d", resp.StatusCode)
	}

	var result YFinanceEarningsTrendResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
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
