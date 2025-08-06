package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

func TestNewYFinanceClient(t *testing.T) {
	cfg := &config.YFinanceConfig{
		BaseURL:        "https://query1.finance.yahoo.com",
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	logger := zap.NewNop()

	client := NewYFinanceClient(cfg, logger)

	assert.NotNil(t, client)
	assert.Equal(t, cfg, client.config)
	assert.NotNil(t, client.httpClient)
	assert.NotNil(t, client.logger)
	assert.Equal(t, cfg.BaseURL, client.baseURL)
}

func TestYFinanceClient_GetQuote_Success(t *testing.T) {
	// Mock Yahoo Finance API response
	mockResponse := &YFinanceQuoteResponse{
		QuoteResponse: struct {
			Result []ports.YFinanceQuote `json:"result"`
			Error  interface{}           `json:"error"`
		}{
			Result: []ports.YFinanceQuote{
				{
					Symbol:               "AAPL",
					RegularMarketPrice:   150.00,
					MarketCap:            2500000000000,
					SharesOutstanding:    16000000000,
					RegularMarketVolume:  50000000,
					AverageDailyVolume3M: 60000000,
					Beta:                 1.2,
					Currency:             "USD",
					MarketState:          "REGULAR",
					RegularMarketTime:    time.Now().Unix(),
				},
			},
			Error: nil,
		},
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		assert.Equal(t, "/v7/finance/quote", r.URL.Path)
		assert.Equal(t, "AAPL", r.URL.Query().Get("symbols"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	quote, err := client.GetQuote(ctx, "AAPL")

	require.NoError(t, err)
	assert.NotNil(t, quote)
	assert.Equal(t, "AAPL", quote.Symbol)
	assert.Equal(t, 150.00, quote.RegularMarketPrice)
	assert.Equal(t, 2500000000000.0, quote.MarketCap)
	assert.Equal(t, 16000000000.0, quote.SharesOutstanding)
	assert.Equal(t, 1.2, quote.Beta)
}

func TestYFinanceClient_GetQuote_NoResults(t *testing.T) {
	// Mock response with no results
	mockResponse := &YFinanceQuoteResponse{
		QuoteResponse: struct {
			Result []ports.YFinanceQuote `json:"result"`
			Error  interface{}           `json:"error"`
		}{
			Result: []ports.YFinanceQuote{},
			Error:  nil,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	quote, err := client.GetQuote(ctx, "INVALID")

	assert.Error(t, err)
	assert.Nil(t, quote)
	assert.Contains(t, err.Error(), "no quote data found")
}

func TestYFinanceClient_GetBatchQuotes_Success(t *testing.T) {
	mockResponse := &YFinanceQuoteResponse{
		QuoteResponse: struct {
			Result []ports.YFinanceQuote `json:"result"`
			Error  interface{}           `json:"error"`
		}{
			Result: []ports.YFinanceQuote{
				{
					Symbol:             "AAPL",
					RegularMarketPrice: 150.00,
				},
				{
					Symbol:             "MSFT",
					RegularMarketPrice: 300.00,
				},
			},
			Error: nil,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v7/finance/quote", r.URL.Path)
		assert.Equal(t, "AAPL,MSFT", r.URL.Query().Get("symbols"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	quotes, err := client.GetBatchQuotes(ctx, []string{"AAPL", "MSFT"})

	require.NoError(t, err)
	assert.NotNil(t, quotes)
	assert.Len(t, quotes, 2)
	assert.Contains(t, quotes, "AAPL")
	assert.Contains(t, quotes, "MSFT")
	assert.Equal(t, 150.00, quotes["AAPL"].RegularMarketPrice)
	assert.Equal(t, 300.00, quotes["MSFT"].RegularMarketPrice)
}

func TestYFinanceClient_GetBatchQuotes_EmptyTickers(t *testing.T) {
	cfg := &config.YFinanceConfig{
		BaseURL:        "https://query1.finance.yahoo.com",
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	quotes, err := client.GetBatchQuotes(ctx, []string{})

	require.NoError(t, err)
	assert.NotNil(t, quotes)
	assert.Len(t, quotes, 0)
}

func TestYFinanceClient_HealthCheck_Success(t *testing.T) {
	// Mock a proper Yahoo Finance API response for AAPL
	mockResponse := &YFinanceQuoteResponse{
		QuoteResponse: struct {
			Result []ports.YFinanceQuote `json:"result"`
			Error  interface{}           `json:"error"`
		}{
			Result: []ports.YFinanceQuote{
				{
					Symbol:               "AAPL",
					RegularMarketPrice:   150.00,
					MarketCap:            2500000000000,
					SharesOutstanding:    16000000000,
					RegularMarketVolume:  50000000,
					AverageDailyVolume3M: 60000000,
					Beta:                 1.2,
					Currency:             "USD",
					MarketState:          "REGULAR",
					RegularMarketTime:    time.Now().Unix(),
				},
			},
			Error: nil,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate that it's requesting AAPL (health check ticker)
		assert.Contains(t, r.URL.Query().Get("symbols"), "AAPL")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.NoError(t, err)
}

func TestYFinanceClient_HealthCheck_Failure(t *testing.T) {
	// Test when GetQuote fails (e.g., API returns error)
	mockResponse := &YFinanceQuoteResponse{
		QuoteResponse: struct {
			Result []ports.YFinanceQuote `json:"result"`
			Error  interface{}           `json:"error"`
		}{
			Result: []ports.YFinanceQuote{}, // Empty results
			Error:  nil,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no quote data found")
}

func TestYFinanceClient_HealthCheck_APIError(t *testing.T) {
	// Test HTTP error scenarios
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Yahoo Finance API returned status 500")
}

func TestYFinanceClient_HealthCheck_Timeout(t *testing.T) {
	// Test timeout scenarios
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Longer than client timeout
	}))
	defer server.Close()

	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		RequestTimeout: 10 * time.Millisecond, // Very short timeout
		MaxRetries:     1,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// Note: Additional tests for GetKeyStatistics and GetHistoricalPrices
// are omitted due to complex response structures. These would be added
// in a complete test suite with proper mock setup.
