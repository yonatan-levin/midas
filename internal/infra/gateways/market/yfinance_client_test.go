package market

import (
	"context"
	"encoding/json"
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

// testMux creates an http.ServeMux that serves auth endpoints (cookie + crumb)
// alongside custom data handlers. All test servers should use this to satisfy
// the cookie+crumb auth flow in YFinanceClient.
func testMux(t *testing.T, dataHandler http.HandlerFunc) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A1", Value: "test-session"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/crumb", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test-crumb"))
	})
	mux.HandleFunc("/", dataHandler)
	return mux
}

// newTestClient creates a YFinanceClient configured to use a test server
// that handles auth + data endpoints.
func newTestClient(t *testing.T, server *httptest.Server) *YFinanceClient {
	t.Helper()
	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		CookieURL:      server.URL + "/cookie",
		CrumbURL:       server.URL + "/crumb",
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		AuthTTL:        6 * time.Hour,
	}
	return NewYFinanceClient(cfg, zap.NewNop())
}

func TestNewYFinanceClient(t *testing.T) {
	cfg := &config.YFinanceConfig{
		BaseURL:        "https://query2.finance.yahoo.com",
		CookieURL:      "https://fc.yahoo.com",
		CrumbURL:       "https://query2.finance.yahoo.com/v1/test/getcrumb",
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		AuthTTL:        6 * time.Hour,
	}
	logger := zap.NewNop()

	client := NewYFinanceClient(cfg, logger)

	assert.NotNil(t, client)
	assert.Equal(t, cfg, client.config)
	assert.NotNil(t, client.httpClient)
	assert.NotNil(t, client.auth)
	assert.NotNil(t, client.logger)
	assert.Equal(t, cfg.BaseURL, client.baseURL)
}

func TestYFinanceClient_GetQuote_Success(t *testing.T) {
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

	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v7/finance/quote") {
			assert.Equal(t, "AAPL", r.URL.Query().Get("symbols"))
			// Verify crumb is present
			assert.NotEmpty(t, r.URL.Query().Get("crumb"), "crumb parameter should be set")
			// Verify cookies are sent
			cookies := r.Cookies()
			assert.NotEmpty(t, cookies, "auth cookies should be sent")

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mockResponse)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server)
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
	mockResponse := &YFinanceQuoteResponse{
		QuoteResponse: struct {
			Result []ports.YFinanceQuote `json:"result"`
			Error  interface{}           `json:"error"`
		}{
			Result: []ports.YFinanceQuote{},
			Error:  nil,
		},
	}

	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()
	quote, err := client.GetQuote(ctx, "INVALID")

	assert.Error(t, err)
	assert.Nil(t, quote)
	assert.Contains(t, err.Error(), "no quote data found")
}

func TestYFinanceClient_GetQuote_AuthRefreshOn401(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v7/finance/quote") {
			callCount++
			if callCount == 1 {
				// First call returns 401
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			// Subsequent calls succeed
			mockResponse := &YFinanceQuoteResponse{
				QuoteResponse: struct {
					Result []ports.YFinanceQuote `json:"result"`
					Error  interface{}           `json:"error"`
				}{
					Result: []ports.YFinanceQuote{
						{Symbol: "AAPL", RegularMarketPrice: 150.00},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mockResponse)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()
	quote, err := client.GetQuote(ctx, "AAPL")

	require.NoError(t, err)
	assert.NotNil(t, quote)
	assert.Equal(t, "AAPL", quote.Symbol)
	assert.GreaterOrEqual(t, callCount, 2, "should have retried after 401")
}

func TestYFinanceClient_GetBatchQuotes_Success(t *testing.T) {
	mockResponse := &YFinanceQuoteResponse{
		QuoteResponse: struct {
			Result []ports.YFinanceQuote `json:"result"`
			Error  interface{}           `json:"error"`
		}{
			Result: []ports.YFinanceQuote{
				{Symbol: "AAPL", RegularMarketPrice: 150.00},
				{Symbol: "MSFT", RegularMarketPrice: 300.00},
			},
			Error: nil,
		},
	}

	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v7/finance/quote") {
			assert.Equal(t, "AAPL,MSFT", r.URL.Query().Get("symbols"))
			assert.NotEmpty(t, r.URL.Query().Get("crumb"))

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mockResponse)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server)
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
		BaseURL:        "https://query2.finance.yahoo.com",
		CookieURL:      "https://fc.yahoo.com",
		CrumbURL:       "https://query2.finance.yahoo.com/v1/test/getcrumb",
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		AuthTTL:        6 * time.Hour,
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

	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v7/finance/quote") {
			assert.Contains(t, r.URL.Query().Get("symbols"), "AAPL")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mockResponse)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.NoError(t, err)
}

func TestYFinanceClient_HealthCheck_Failure(t *testing.T) {
	mockResponse := &YFinanceQuoteResponse{
		QuoteResponse: struct {
			Result []ports.YFinanceQuote `json:"result"`
			Error  interface{}           `json:"error"`
		}{
			Result: []ports.YFinanceQuote{},
			Error:  nil,
		},
	}

	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no quote data found")
}

func TestYFinanceClient_HealthCheck_APIError(t *testing.T) {
	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "yahoo finance api returned status 500")
}

func TestYFinanceClient_HealthCheck_Timeout(t *testing.T) {
	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Longer than client timeout
	}))
	defer server.Close()

	cfg := &config.YFinanceConfig{
		BaseURL:        server.URL,
		CookieURL:      server.URL + "/cookie",
		CrumbURL:       server.URL + "/crumb",
		RequestTimeout: 10 * time.Millisecond, // Very short timeout
		MaxRetries:     1,
		AuthTTL:        6 * time.Hour,
	}
	logger := zap.NewNop()
	client := NewYFinanceClient(cfg, logger)

	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.Error(t, err)
}

func TestYFinanceClient_AppendCrumb(t *testing.T) {
	server := newAuthTestServer(t, "my-crumb")
	defer server.Close()

	client := newTestClient(t, server)
	// Ensure auth to get the crumb
	_ = client.auth.EnsureAuth(context.Background())

	tests := []struct {
		name     string
		inputURL string
		contains string
	}{
		{
			name:     "url_with_existing_params",
			inputURL: "https://example.com/api?symbols=AAPL",
			contains: "&crumb=",
		},
		{
			name:     "url_without_params",
			inputURL: "https://example.com/api",
			contains: "?crumb=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.appendCrumb(tt.inputURL)
			assert.Contains(t, result, tt.contains)
			assert.Contains(t, result, "my-crumb")
		})
	}
}

func TestYFinanceClient_ApplyAuth_SetsHeaders(t *testing.T) {
	server := newAuthTestServer(t, "header-crumb")
	defer server.Close()

	client := newTestClient(t, server)
	_ = client.auth.EnsureAuth(context.Background())

	req, _ := http.NewRequest("GET", "https://example.com/test", nil)
	client.applyAuth(req)

	assert.Equal(t, yahooUserAgent, req.Header.Get("User-Agent"))
	assert.Equal(t, "application/json", req.Header.Get("Accept"))
	assert.Equal(t, "https://finance.yahoo.com", req.Header.Get("Referer"))
	assert.NotEmpty(t, req.Cookies(), "should have auth cookies applied")
}

func TestYFinanceClient_GetKeyStatistics_Success(t *testing.T) {
	beta := 1.25
	sharesOut := 15000000000.0
	floatShares := 14500000000.0
	bookVal := 4.15
	ptb := 36.14
	ev := 2800000000000.0
	cash := 62000000000.0
	debt := 120000000000.0

	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v10/finance/quoteSummary/") {
			assert.Contains(t, r.URL.Query().Get("modules"), "defaultKeyStatistics")
			assert.NotEmpty(t, r.URL.Query().Get("crumb"))

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
					SharesOutstanding: &YFinanceValue{Raw: &sharesOut},
					FloatShares:       &YFinanceValue{Raw: &floatShares},
					BookValue:         &YFinanceValue{Raw: &bookVal},
					PriceToBook:       &YFinanceValue{Raw: &ptb},
					EnterpriseValue:   &YFinanceValue{Raw: &ev},
					TotalCash:         &YFinanceValue{Raw: &cash},
					TotalDebt:         &YFinanceValue{Raw: &debt},
				},
				FinancialData: &struct {
					CurrentPrice *YFinanceValue `json:"currentPrice"`
				}{
					CurrentPrice: &YFinanceValue{Raw: ptrFloat(150.0)},
				},
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	stats, err := client.GetKeyStatistics(context.Background(), "AAPL")

	require.NoError(t, err)
	assert.InDelta(t, 1.25, stats.Beta, 0.001)
	assert.InDelta(t, 15000000000.0, stats.SharesOutstanding, 1.0)
	assert.InDelta(t, 14500000000.0, stats.SharesFloat, 1.0)
	assert.InDelta(t, 4.15, stats.BookValue, 0.01)
	assert.InDelta(t, 36.14, stats.PriceToBook, 0.01)
	assert.InDelta(t, 2800000000000.0, stats.EnterpriseValue, 1.0)
	assert.InDelta(t, 62000000000.0, stats.TotalCash, 1.0)
	assert.InDelta(t, 120000000000.0, stats.TotalDebt, 1.0)
}

func TestYFinanceClient_GetKeyStatistics_Error(t *testing.T) {
	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	stats, err := client.GetKeyStatistics(context.Background(), "BAD")

	assert.Error(t, err)
	assert.Nil(t, stats)
	assert.Contains(t, err.Error(), "failed to fetch key statistics")
}

func TestYFinanceClient_GetHistoricalPrices_Success(t *testing.T) {
	now := time.Now()
	timestamps := []int64{
		now.AddDate(0, 0, -3).Unix(),
		now.AddDate(0, 0, -2).Unix(),
		now.AddDate(0, 0, -1).Unix(),
	}

	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v8/finance/chart/") {
			assert.Contains(t, r.URL.Path, "AAPL")
			assert.NotEmpty(t, r.URL.Query().Get("period1"))
			assert.NotEmpty(t, r.URL.Query().Get("period2"))
			assert.NotEmpty(t, r.URL.Query().Get("crumb"))

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
				}{Currency: "USD", Symbol: "AAPL"},
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
					}{{
						Open:   []float64{148.0, 150.0, 151.0},
						High:   []float64{152.0, 153.0, 154.0},
						Low:    []float64{147.0, 149.0, 150.0},
						Close:  []float64{150.0, 151.0, 153.0},
						Volume: []float64{50e6, 45e6, 55e6},
					}},
				},
			})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	prices, err := client.GetHistoricalPrices(context.Background(), "AAPL", 30)

	require.NoError(t, err)
	assert.Len(t, prices, 3)
	assert.InDelta(t, 150.0, prices[0].Close, 0.01)
	assert.InDelta(t, 151.0, prices[1].Close, 0.01)
	assert.InDelta(t, 153.0, prices[2].Close, 0.01)
	assert.InDelta(t, 50e6, prices[0].Volume, 1.0)
}

func TestYFinanceClient_GetHistoricalPrices_NoData(t *testing.T) {
	server := httptest.NewServer(testMux(t, func(w http.ResponseWriter, r *http.Request) {
		resp := YFinanceHistoricalResponse{} // empty chart.result
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	prices, err := client.GetHistoricalPrices(context.Background(), "INVALID", 30)

	assert.Error(t, err)
	assert.Nil(t, prices)
	assert.Contains(t, err.Error(), "no historical data found")
}

func TestErrUnauthorized_Error(t *testing.T) {
	err := &errUnauthorized{msg: "test unauthorized message"}
	assert.Equal(t, "test unauthorized message", err.Error())
}

// ptrFloat is a helper to create *float64 inline.
func ptrFloat(v float64) *float64 { return &v }
