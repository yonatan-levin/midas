package market

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

func TestNewGateway(t *testing.T) {
	cfg := &config.MarketConfig{
		YFinance: config.YFinanceConfig{
			Enabled:        true,
			BaseURL:        "https://query1.finance.yahoo.com",
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
		},
		Finzive: config.FinziveConfig{
			Enabled:        false,
			BaseURL:        "https://finzive.com",
			RequestTimeout: 60 * time.Second,
			MaxRetries:     2,
			UserAgent:      "Test Agent",
		},
	}
	logger := zap.NewNop()

	gateway := NewGateway(cfg, logger)

	assert.NotNil(t, gateway)
	assert.NotNil(t, gateway.yfinance)
	assert.Equal(t, cfg, gateway.config)
	assert.NotNil(t, gateway.logger)
}

func TestNewGateway_YFinanceDisabled(t *testing.T) {
	cfg := &config.MarketConfig{
		YFinance: config.YFinanceConfig{
			Enabled:        false,
			BaseURL:        "https://query1.finance.yahoo.com",
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
		},
	}
	logger := zap.NewNop()

	gateway := NewGateway(cfg, logger)

	assert.NotNil(t, gateway)
	assert.Nil(t, gateway.yfinance) // Should be nil when disabled
	assert.Equal(t, cfg, gateway.config)
	assert.NotNil(t, gateway.logger)
}

func TestGateway_CalculateDailyReturns(t *testing.T) {
	gateway := &Gateway{logger: zap.NewNop()}

	tests := []struct {
		name     string
		prices   []ports.YFinancePricePoint
		expected []float64
	}{
		{
			name:     "empty_prices",
			prices:   []ports.YFinancePricePoint{},
			expected: []float64{},
		},
		{
			name: "single_price",
			prices: []ports.YFinancePricePoint{
				{Close: 100.0, Date: time.Now()},
			},
			expected: []float64{},
		},
		{
			name: "two_prices_positive_return",
			prices: []ports.YFinancePricePoint{
				{Close: 100.0, Date: time.Now()},
				{Close: 110.0, Date: time.Now().Add(24 * time.Hour)},
			},
			expected: []float64{0.1}, // 10% return
		},
		{
			name: "two_prices_negative_return",
			prices: []ports.YFinancePricePoint{
				{Close: 100.0, Date: time.Now()},
				{Close: 90.0, Date: time.Now().Add(24 * time.Hour)},
			},
			expected: []float64{-0.1}, // -10% return
		},
		{
			name: "multiple_prices",
			prices: []ports.YFinancePricePoint{
				{Close: 100.0, Date: time.Now()},
				{Close: 105.0, Date: time.Now().Add(24 * time.Hour)},
				{Close: 110.0, Date: time.Now().Add(48 * time.Hour)},
			},
			expected: []float64{0.05, 0.047619047619047616}, // 5%, ~4.76%
		},
		{
			name: "zero_price_handling",
			prices: []ports.YFinancePricePoint{
				{Close: 0.0, Date: time.Now()},
				{Close: 100.0, Date: time.Now().Add(24 * time.Hour)},
			},
			expected: []float64{0.0}, // Should handle zero price gracefully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gateway.calculateDailyReturns(tt.prices)
			assert.InDelta(t, len(tt.expected), len(result), 0, "Length mismatch")
			for i, expected := range tt.expected {
				if i < len(result) {
					assert.InDelta(t, expected, result[i], 0.0001, "Return calculation mismatch at index %d", i)
				}
			}
		})
	}
}

func TestGateway_CalculateMean(t *testing.T) {
	gateway := &Gateway{logger: zap.NewNop()}

	tests := []struct {
		name     string
		values   []float64
		expected float64
	}{
		{
			name:     "empty_slice",
			values:   []float64{},
			expected: 0.0,
		},
		{
			name:     "single_value",
			values:   []float64{5.0},
			expected: 5.0,
		},
		{
			name:     "multiple_positive_values",
			values:   []float64{1.0, 2.0, 3.0, 4.0, 5.0},
			expected: 3.0,
		},
		{
			name:     "mixed_values",
			values:   []float64{-2.0, 0.0, 2.0},
			expected: 0.0,
		},
		{
			name:     "negative_values",
			values:   []float64{-1.0, -2.0, -3.0},
			expected: -2.0,
		},
		{
			name:     "floating_point_precision",
			values:   []float64{0.1, 0.2, 0.3},
			expected: 0.2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gateway.calculateMean(tt.values)
			assert.InDelta(t, tt.expected, result, 0.0001, "Mean calculation mismatch")
		})
	}
}

func TestGateway_CalculateVariance(t *testing.T) {
	gateway := &Gateway{logger: zap.NewNop()}

	tests := []struct {
		name     string
		values   []float64
		expected float64
	}{
		{
			name:     "empty_slice",
			values:   []float64{},
			expected: 0.0,
		},
		{
			name:     "single_value",
			values:   []float64{5.0},
			expected: math.NaN(), // Implementation divides by n-1, causing NaN for single value
		},
		{
			name:     "identical_values",
			values:   []float64{3.0, 3.0, 3.0, 3.0},
			expected: 0.0,
		},
		{
			name:     "simple_values",
			values:   []float64{1.0, 2.0, 3.0},
			expected: 1.0, // Sample variance
		},
		{
			name:     "negative_values",
			values:   []float64{-1.0, 0.0, 1.0},
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gateway.calculateVariance(tt.values)
			if math.IsNaN(tt.expected) {
				assert.True(t, math.IsNaN(result), "Expected NaN but got %f", result)
			} else {
				assert.InDelta(t, tt.expected, result, 0.0001, "Variance calculation mismatch")
			}
		})
	}
}

func TestGateway_CalculateCovariance(t *testing.T) {
	gateway := &Gateway{logger: zap.NewNop()}

	tests := []struct {
		name     string
		x        []float64
		y        []float64
		expected float64
	}{
		{
			name:     "empty_slices",
			x:        []float64{},
			y:        []float64{},
			expected: 0.0,
		},
		{
			name:     "mismatched_lengths",
			x:        []float64{1.0, 2.0},
			y:        []float64{1.0},
			expected: 0.0,
		},
		{
			name:     "single_values",
			x:        []float64{5.0},
			y:        []float64{10.0},
			expected: math.NaN(), // Implementation divides by n-1, causing NaN for single value
		},
		{
			name:     "positive_correlation",
			x:        []float64{1.0, 2.0, 3.0},
			y:        []float64{2.0, 4.0, 6.0},
			expected: 2.0, // Perfect positive correlation
		},
		{
			name:     "negative_correlation",
			x:        []float64{1.0, 2.0, 3.0},
			y:        []float64{6.0, 4.0, 2.0},
			expected: -2.0, // Perfect negative correlation
		},
		{
			name:     "zero_correlation",
			x:        []float64{1.0, 2.0, 3.0},
			y:        []float64{5.0, 5.0, 5.0},
			expected: 0.0, // No correlation (y is constant)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gateway.calculateCovariance(tt.x, tt.y)
			if math.IsNaN(tt.expected) {
				assert.True(t, math.IsNaN(result), "Expected NaN but got %f", result)
			} else {
				assert.InDelta(t, tt.expected, result, 0.0001, "Covariance calculation mismatch")
			}
		})
	}
}

func TestGateway_AssessDataQuality(t *testing.T) {
	gateway := &Gateway{logger: zap.NewNop()}

	tests := []struct {
		name     string
		quote    *ports.YFinanceQuote
		expected string
	}{
		{
			name: "high_quality_all_fields_present",
			quote: &ports.YFinanceQuote{
				RegularMarketPrice:   100.0,
				SharesOutstanding:    1000000,
				Beta:                 1.2,
				MarketCap:            100000000,
				AverageDailyVolume3M: 50000,
				RegularMarketTime:    time.Now().Unix(),
			},
			expected: "high",
		},
		{
			name: "high_quality_one_missing",
			quote: &ports.YFinanceQuote{
				RegularMarketPrice:   100.0,
				SharesOutstanding:    1000000,
				Beta:                 0.0, // Missing (1 out of 5 missing = 80% = "high")
				MarketCap:            100000000,
				AverageDailyVolume3M: 50000,
				RegularMarketTime:    time.Now().Unix(),
			},
			expected: "high", // 4/5 = 0.8 which is >= 0.8, so "high"
		},
		{
			name: "low_quality_multiple_missing",
			quote: &ports.YFinanceQuote{
				RegularMarketPrice:   100.0,
				SharesOutstanding:    0.0, // Missing
				Beta:                 0.0, // Missing
				MarketCap:            0.0, // Missing
				AverageDailyVolume3M: 50000,
				RegularMarketTime:    time.Now().Unix(),
			},
			expected: "low",
		},
		{
			name: "low_quality_all_missing",
			quote: &ports.YFinanceQuote{
				RegularMarketPrice:   0.0,
				SharesOutstanding:    0.0,
				Beta:                 0.0,
				MarketCap:            0.0,
				AverageDailyVolume3M: 0.0,
				RegularMarketTime:    time.Now().Unix(),
			},
			expected: "low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gateway.assessDataQuality(tt.quote)
			assert.Equal(t, tt.expected, result, "Data quality assessment mismatch")
		})
	}
}

func TestGateway_GetMarketData_NoClients(t *testing.T) {
	gateway := &Gateway{
		yfinance: nil, // No clients available
		logger:   zap.NewNop(),
	}

	ctx := context.Background()
	_, err := gateway.GetMarketData(ctx, "AAPL")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch market data for AAPL from all sources")
}

func TestGateway_GetQuote_Alias(t *testing.T) {
	gateway := &Gateway{
		yfinance: nil,
		logger:   zap.NewNop(),
	}

	ctx := context.Background()
	_, err := gateway.GetQuote(ctx, "AAPL")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch market data for AAPL from all sources")
}

func TestGateway_GetBatchMarketData_EmptySlice(t *testing.T) {
	gateway := &Gateway{
		yfinance: nil,
		logger:   zap.NewNop(),
	}

	ctx := context.Background()
	result, err := gateway.GetBatchMarketData(ctx, []string{})
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestGateway_GetHistoricalPrices_NoClient(t *testing.T) {
	gateway := &Gateway{
		yfinance: nil,
		logger:   zap.NewNop(),
	}

	ctx := context.Background()
	startDate := time.Now().AddDate(0, 0, -30)
	endDate := time.Now()

	_, err := gateway.GetHistoricalPrices(ctx, "AAPL", startDate, endDate)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "yfinance client not available")
}

func TestGateway_GetHistoricalPrices_InvalidDateRange(t *testing.T) {
	cfg := &config.MarketConfig{
		YFinance: config.YFinanceConfig{Enabled: true},
	}
	gateway := NewGateway(cfg, zap.NewNop())

	ctx := context.Background()
	startDate := time.Now()
	endDate := time.Now().AddDate(0, 0, -30) // End before start

	_, err := gateway.GetHistoricalPrices(ctx, "AAPL", startDate, endDate)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid date range: end date must be after start date")
}

func TestGateway_GetBeta_NoClients(t *testing.T) {
	gateway := &Gateway{
		yfinance: nil,
		logger:   zap.NewNop(),
	}

	ctx := context.Background()
	_, err := gateway.GetBeta(ctx, "AAPL")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to determine beta for AAPL")
}

func TestGateway_GetSharePrice_NoClients(t *testing.T) {
	gateway := &Gateway{
		yfinance: nil,
		logger:   zap.NewNop(),
	}

	ctx := context.Background()
	_, err := gateway.GetSharePrice(ctx, "AAPL")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch market data for AAPL from all sources")
}

func TestGateway_GetSharesOutstanding_NoClients(t *testing.T) {
	gateway := &Gateway{
		yfinance: nil,
		logger:   zap.NewNop(),
	}

	ctx := context.Background()
	_, err := gateway.GetSharesOutstanding(ctx, "AAPL")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch market data for AAPL from all sources")
}

func TestGateway_HealthCheck_NoClients(t *testing.T) {
	gateway := &Gateway{
		yfinance: nil,
		logger:   zap.NewNop(),
	}

	ctx := context.Background()
	err := gateway.HealthCheck(ctx)
	assert.NoError(t, err) // Should pass when no clients are configured
}

// Test edge cases for beta calculation helper functions
func TestGateway_BetaCalculationEdgeCases(t *testing.T) {
	gateway := &Gateway{logger: zap.NewNop()}

	t.Run("insufficient_data_for_beta", func(t *testing.T) {
		// Test case where we don't have enough data points
		stockReturns := []float64{0.01, 0.02}    // Only 2 points
		marketReturns := []float64{0.015, 0.025} // Only 2 points

		// This should be handled gracefully in the actual beta calculation
		covariance := gateway.calculateCovariance(stockReturns, marketReturns)
		variance := gateway.calculateVariance(marketReturns)

		assert.False(t, math.IsNaN(covariance), "Covariance should not be NaN")
		assert.False(t, math.IsNaN(variance), "Variance should not be NaN")
	})

	t.Run("zero_market_variance", func(t *testing.T) {
		// Test case where market has zero variance (constant prices)
		marketReturns := []float64{0.0, 0.0, 0.0, 0.0}
		variance := gateway.calculateVariance(marketReturns)
		assert.Equal(t, 0.0, variance)
	})
}

// ─────────────────────────────────────────────────────────────────────
// Integration tests: Full Gateway → YFinanceClient → httptest.Server
// ─────────────────────────────────────────────────────────────────────

// fullMockServer creates an httptest.Server that handles all Yahoo Finance
// endpoints: auth (cookie+crumb), quote (v7), key stats (v10), and
// historical chart (v8). Pass quoteOverride / keyStatsOverride to customise
// per-test responses; nil means use sensible defaults.
func fullMockServer(t *testing.T, opts *mockServerOpts) *httptest.Server {
	t.Helper()
	if opts == nil {
		opts = &mockServerOpts{}
	}

	mux := http.NewServeMux()

	// Auth endpoints
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A1", Value: "s"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/crumb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("test-crumb"))
	})

	// v7 — quote
	mux.HandleFunc("/v7/finance/quote", func(w http.ResponseWriter, r *http.Request) {
		symbols := r.URL.Query().Get("symbols")
		tickers := strings.Split(symbols, ",")

		var results []ports.YFinanceQuote
		for _, tk := range tickers {
			tk = strings.TrimSpace(tk)
			q := defaultQuote(tk)
			if opts.quoteOverride != nil {
				if custom, ok := opts.quoteOverride[tk]; ok {
					q = custom
				}
			}
			results = append(results, q)
		}

		resp := YFinanceQuoteResponse{}
		resp.QuoteResponse.Result = results
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// v10 — key stats
	mux.HandleFunc("/v10/finance/quoteSummary/", func(w http.ResponseWriter, r *http.Request) {
		beta := 1.15
		shares := 15e9
		if opts.keyStatsBeta != nil {
			beta = *opts.keyStatsBeta
		}
		if opts.keyStatsShares != nil {
			shares = *opts.keyStatsShares
		}

		resp := YFinanceKeyStatsResponse{}
		resp.QuoteSummary.Result = append(resp.QuoteSummary.Result, struct {
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
		}{
			DefaultKeyStatistics: &struct {
				Beta              *YFinanceValue `json:"beta"`
				SharesOutstanding *YFinanceValue `json:"sharesOutstanding"`
				FloatShares       *YFinanceValue `json:"floatShares"`
				BookValue         *YFinanceValue `json:"bookValue"`
				PriceToBook       *YFinanceValue `json:"priceToBook"`
				EnterpriseValue   *YFinanceValue `json:"enterpriseValue"`
				TotalCash         *YFinanceValue `json:"totalCash"`
				TotalDebt         *YFinanceValue `json:"totalDebt"`
			}{
				Beta:              &YFinanceValue{Raw: &beta},
				SharesOutstanding: &YFinanceValue{Raw: &shares},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// v8 — historical chart
	mux.HandleFunc("/v8/finance/chart/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		ticker := parts[len(parts)-1]
		numDays := 60

		now := time.Now()
		timestamps := make([]int64, numDays)
		opens := make([]float64, numDays)
		highs := make([]float64, numDays)
		lows := make([]float64, numDays)
		closes := make([]float64, numDays)
		volumes := make([]float64, numDays)

		basePrice := 100.0
		if ticker == "^GSPC" {
			basePrice = 4500.0
		}

		for i := 0; i < numDays; i++ {
			timestamps[i] = now.AddDate(0, 0, -(numDays - i)).Unix()
			p := basePrice + float64(i)*0.5
			opens[i] = p
			highs[i] = p + 1
			lows[i] = p - 1
			closes[i] = p + 0.25
			volumes[i] = 1e6
		}

		resp := YFinanceHistoricalResponse{}
		resp.Chart.Result = append(resp.Chart.Result, struct {
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
		}{
			Meta: struct {
				Currency string `json:"currency"`
				Symbol   string `json:"symbol"`
			}{Currency: "USD", Symbol: ticker},
			Timestamp: timestamps,
			Indicators: struct {
				Quote []struct {
					Open   []float64 `json:"open"`
					High   []float64 `json:"high"`
					Low    []float64 `json:"low"`
					Close  []float64 `json:"close"`
					Volume []float64 `json:"volume"`
				} `json:"quote"`
			}{
				Quote: []struct {
					Open   []float64 `json:"open"`
					High   []float64 `json:"high"`
					Low    []float64 `json:"low"`
					Close  []float64 `json:"close"`
					Volume []float64 `json:"volume"`
				}{{Open: opens, High: highs, Low: lows, Close: closes, Volume: volumes}},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	return httptest.NewServer(mux)
}

type mockServerOpts struct {
	quoteOverride  map[string]ports.YFinanceQuote // per-ticker quote overrides
	keyStatsBeta   *float64
	keyStatsShares *float64
}

func defaultQuote(ticker string) ports.YFinanceQuote {
	return ports.YFinanceQuote{
		Symbol:               ticker,
		RegularMarketPrice:   150.0,
		MarketCap:            2.5e12,
		SharesOutstanding:    15e9,
		RegularMarketVolume:  50e6,
		AverageDailyVolume3M: 60e6,
		Beta:                 1.2,
		Currency:             "USD",
		MarketState:          "REGULAR",
		RegularMarketTime:    time.Now().Unix(),
	}
}

// newGatewayWithMockServer creates a Gateway backed by a real YFinanceClient
// pointing at the given mock server.
func newGatewayWithMockServer(t *testing.T, server *httptest.Server) *Gateway {
	t.Helper()
	cfg := &config.MarketConfig{
		YFinance: config.YFinanceConfig{
			Enabled:        true,
			BaseURL:        server.URL,
			CookieURL:      server.URL + "/cookie",
			CrumbURL:       server.URL + "/crumb",
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			AuthTTL:        6 * time.Hour,
		},
	}
	return NewGateway(cfg, zap.NewNop())
}

// ─── Gateway integration tests ───────────────────────────────────────

func TestGateway_GetMarketData_Success(t *testing.T) {
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	md, err := gw.GetMarketData(context.Background(), "AAPL")

	require.NoError(t, err)
	assert.Equal(t, "AAPL", md.Ticker)
	assert.InDelta(t, 150.0, md.SharePrice, 0.01)
	assert.InDelta(t, 2.5e12, md.MarketCap, 1.0)
	assert.InDelta(t, 15e9, md.SharesOutstanding, 1.0)
	assert.InDelta(t, 1.2, md.Beta, 0.01)
	assert.Equal(t, "yfinance", md.Source)
	assert.NotEmpty(t, md.DataQuality)
}

func TestGateway_GetMarketData_BetaFallbackToKeyStats(t *testing.T) {
	ksBeta := 1.35
	server := fullMockServer(t, &mockServerOpts{
		quoteOverride: map[string]ports.YFinanceQuote{
			"AAPL": {
				Symbol:             "AAPL",
				RegularMarketPrice: 150.0,
				MarketCap:          2.5e12,
				SharesOutstanding:  15e9,
				Beta:               0, // Missing beta → should fallback to key stats
				RegularMarketTime:  time.Now().Unix(),
			},
		},
		keyStatsBeta: &ksBeta,
	})
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	md, err := gw.GetMarketData(context.Background(), "AAPL")

	require.NoError(t, err)
	assert.InDelta(t, 1.35, md.Beta, 0.01, "beta should come from key stats fallback")
}

func TestGateway_GetBatchMarketData_Success(t *testing.T) {
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	results, err := gw.GetBatchMarketData(context.Background(), []string{"AAPL", "MSFT"})

	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Contains(t, results, "AAPL")
	assert.Contains(t, results, "MSFT")
	assert.InDelta(t, 150.0, results["AAPL"].SharePrice, 0.01)
	assert.Equal(t, "yfinance", results["AAPL"].Source)
}

func TestGateway_GetQuotes_Success(t *testing.T) {
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	results, err := gw.GetQuotes(context.Background(), []string{"GOOGL"})

	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Contains(t, results, "GOOGL")
}

func TestGateway_GetHistoricalPrices_Success(t *testing.T) {
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	start := time.Now().AddDate(0, 0, -30)
	end := time.Now()

	prices, err := gw.GetHistoricalPrices(context.Background(), "AAPL", start, end)

	require.NoError(t, err)
	assert.NotEmpty(t, prices)
	for _, p := range prices {
		assert.Equal(t, "AAPL", p.Ticker)
		assert.Greater(t, p.Close, 0.0)
		assert.True(t, !p.Date.Before(start) && !p.Date.After(end.Add(24*time.Hour)),
			"price date should be within requested range")
	}
}

func TestGateway_HealthCheck_WithClient(t *testing.T) {
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	err := gw.HealthCheck(context.Background())

	assert.NoError(t, err)
}

func TestGateway_GetBeta_Success(t *testing.T) {
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	beta, err := gw.GetBeta(context.Background(), "AAPL")

	require.NoError(t, err)
	assert.InDelta(t, 1.2, beta, 0.01)
}

func TestGateway_GetSharePrice_Success(t *testing.T) {
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	price, err := gw.GetSharePrice(context.Background(), "AAPL")

	require.NoError(t, err)
	assert.InDelta(t, 150.0, price, 0.01)
}

func TestGateway_GetSharesOutstanding_Success(t *testing.T) {
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	shares, err := gw.GetSharesOutstanding(context.Background(), "AAPL")

	require.NoError(t, err)
	assert.InDelta(t, 15e9, shares, 1.0)
}

func TestGateway_CalculateBetaFromHistoricalData(t *testing.T) {
	// The mock server provides 60 days of prices for both ticker and ^GSPC,
	// which satisfies the >30 data point requirement in calculateBetaFromHistoricalData.
	server := fullMockServer(t, nil)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	beta, err := gw.calculateBetaFromHistoricalData(context.Background(), "AAPL")

	require.NoError(t, err)
	// Mock data has different base prices (100 vs 4500) but both increase linearly,
	// so beta will be positive and finite.
	assert.False(t, math.IsNaN(beta), "beta should not be NaN")
	assert.False(t, math.IsInf(beta, 0), "beta should not be Inf")
}

func TestGateway_GetBeta_FallbackToHistoricalCalc(t *testing.T) {
	// Quote returns beta=0, so gateway should fallback to historical calculation
	server := fullMockServer(t, &mockServerOpts{
		quoteOverride: map[string]ports.YFinanceQuote{
			"AAPL": {
				Symbol:             "AAPL",
				RegularMarketPrice: 150.0,
				MarketCap:          2.5e12,
				SharesOutstanding:  15e9,
				Beta:               0, // Missing beta
				RegularMarketTime:  time.Now().Unix(),
			},
		},
		keyStatsBeta: func() *float64 { v := 0.0; return &v }(), // Key stats also returns 0
	})
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	beta, err := gw.GetBeta(context.Background(), "AAPL")

	// Beta from key stats is 0, but market data returns 0 beta → should fall through
	// to calculateBetaFromHistoricalData
	require.NoError(t, err)
	assert.Greater(t, beta, 0.0, "beta should be calculated from historical data")
}

func TestGateway_GetBatchMarketData_FallbackToIndividual(t *testing.T) {
	// Server that fails batch but succeeds on individual requests
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A1", Value: "s"})
	})
	mux.HandleFunc("/crumb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("c"))
	})
	mux.HandleFunc("/v7/finance/quote", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		symbols := r.URL.Query().Get("symbols")
		if strings.Contains(symbols, ",") {
			// Batch request → fail to trigger fallback
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Individual request → succeed
		resp := YFinanceQuoteResponse{}
		resp.QuoteResponse.Result = []ports.YFinanceQuote{defaultQuote(symbols)}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	// Key stats endpoint for the beta/shares fallback in getMarketDataFromYFinance
	mux.HandleFunc("/v10/finance/quoteSummary/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // not needed for this test
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	results, err := gw.GetBatchMarketData(context.Background(), []string{"AAPL", "MSFT"})

	require.NoError(t, err)
	assert.Len(t, results, 2, "both tickers should succeed via individual fallback")
	assert.Contains(t, results, "AAPL")
	assert.Contains(t, results, "MSFT")
}

func TestGateway_HealthCheck_WithClientError(t *testing.T) {
	// Server that fails the health check quote request
	mux := http.NewServeMux()
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A1", Value: "s"})
	})
	mux.HandleFunc("/crumb", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("c"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		// Return empty quote results → health check fails with "no quote data found"
		resp := YFinanceQuoteResponse{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	err := gw.HealthCheck(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "health check failed")
}

func TestGateway_GetMarketData_SharesFallbackToKeyStats(t *testing.T) {
	ksShares := 16e9
	server := fullMockServer(t, &mockServerOpts{
		quoteOverride: map[string]ports.YFinanceQuote{
			"AAPL": {
				Symbol:             "AAPL",
				RegularMarketPrice: 150.0,
				MarketCap:          2.5e12,
				SharesOutstanding:  0, // Missing → fallback
				Beta:               1.2,
				RegularMarketTime:  time.Now().Unix(),
			},
		},
		keyStatsShares: &ksShares,
	})
	defer server.Close()

	gw := newGatewayWithMockServer(t, server)
	md, err := gw.GetMarketData(context.Background(), "AAPL")

	require.NoError(t, err)
	assert.InDelta(t, 16e9, md.SharesOutstanding, 1.0, "shares should come from key stats fallback")
}
