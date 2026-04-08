package sec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

func createTestSECConfig(baseURL string) *config.SECConfig {
	return &config.SECConfig{
		BaseURL:          baseURL,
		TickerMappingURL: baseURL + "/company_tickers.json",
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
}

func TestNewGateway(t *testing.T) {
	t.Run("creates gateway successfully", func(t *testing.T) {
		cfg := createTestSECConfig("https://data.sec.gov/api/xbrl")
		logger := zap.NewNop()

		gateway := NewGateway(cfg, logger)

		assert.NotNil(t, gateway)
		assert.IsType(t, &Gateway{}, gateway)
	})
}

func TestGateway_GetCompanyFacts(t *testing.T) {
	// Mock successful SEC response with nested taxonomy -> concept structure
	mockCompanyFacts := &ports.SECCompanyFacts{
		CIK:        "320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"us-gaap": {
				"Revenues": {
					Label:       "Revenues",
					Description: "Revenue from operations",
					Units: map[string][]ports.SECFact{
						"USD": {
							{
								End:   "2023-09-30",
								Val:   383285000000,
								Accn:  "0000320193-23-000106",
								Fy:    2023,
								Fp:    "FY",
								Form:  "10-K",
								Filed: "2023-11-03",
								Frame: "CY2023Q3I",
							},
						},
					},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		assert.Equal(t, "/api/xbrl/companyfacts/CIK0000320193.json", r.URL.Path)
		assert.Equal(t, "Test User Agent", r.Header.Get("User-Agent"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockCompanyFacts)
	}))
	defer server.Close()

	t.Run("fetches company facts successfully", func(t *testing.T) {
		cfg := createTestSECConfig(server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		financialData, err := gateway.GetCompanyFacts(ctx, "0000320193")

		require.NoError(t, err)
		assert.NotNil(t, financialData)
		assert.Equal(t, "320193", financialData.CIK)
		assert.Equal(t, "Apple Inc.", financialData.EntityName)
		assert.Greater(t, len(financialData.Facts), 0)
	})

	t.Run("handles server error", func(t *testing.T) {
		errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer errorServer.Close()

		cfg := createTestSECConfig(errorServer.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		financialData, err := gateway.GetCompanyFacts(ctx, "0000320193")

		assert.Error(t, err)
		assert.Nil(t, financialData)
	})
}

func TestGateway_GetCompanyConcepts(t *testing.T) {
	// Mock SEC concept response
	mockConceptResponse := &entities.ConceptResponse{
		CIK:         "320193",
		Tag:         "Revenues",
		EntityName:  "Apple Inc.",
		Taxonomy:    "us-gaap",
		Label:       "Revenues",
		Description: "Revenue from operations",
		Units: map[string]interface{}{
			"USD": []interface{}{
				map[string]interface{}{
					"end":   "2023-09-30",
					"val":   383285000000.0,
					"accn":  "0000320193-23-000106",
					"fy":    2023.0,
					"fp":    "FY",
					"form":  "10-K",
					"filed": "2023-11-03",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		assert.Equal(t, "/api/xbrl/companyconcept/CIK0000320193/us-gaap/Revenues.json", r.URL.Path)
		assert.Equal(t, "Test User Agent", r.Header.Get("User-Agent"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockConceptResponse)
	}))
	defer server.Close()

	t.Run("fetches company concepts successfully", func(t *testing.T) {
		cfg := createTestSECConfig(server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		conceptResponse, err := gateway.GetCompanyConcepts(ctx, "0000320193", "Revenues")

		require.NoError(t, err)
		assert.NotNil(t, conceptResponse)
		assert.Equal(t, "320193", conceptResponse.CIK)
		assert.Equal(t, "Revenues", conceptResponse.Tag)
		assert.Equal(t, "Apple Inc.", conceptResponse.EntityName)
	})

	t.Run("handles 404 error", func(t *testing.T) {
		notFoundServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer notFoundServer.Close()

		cfg := createTestSECConfig(notFoundServer.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		conceptResponse, err := gateway.GetCompanyConcepts(ctx, "0000320193", "Revenues")

		assert.Error(t, err)
		assert.Nil(t, conceptResponse)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestGateway_GetTickerCIKMapping(t *testing.T) {
	// Mock ticker mapping response
	mockMapping := map[string]interface{}{
		"0": map[string]interface{}{
			"cik_str": "320193",
			"ticker":  "AAPL",
			"title":   "Apple Inc.",
		},
		"1": map[string]interface{}{
			"cik_str": "789019",
			"ticker":  "MSFT",
			"title":   "Microsoft Corp",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		assert.Equal(t, "/company_tickers.json", r.URL.Path)
		assert.Equal(t, "Test User Agent", r.Header.Get("User-Agent"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockMapping)
	}))
	defer server.Close()

	t.Run("fetches ticker mapping successfully", func(t *testing.T) {
		cfg := createTestSECConfig(server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		mapping, err := gateway.GetTickerCIKMapping(ctx)

		require.NoError(t, err)
		assert.NotNil(t, mapping)
		assert.Equal(t, "320193", mapping["AAPL"])
		assert.Equal(t, "789019", mapping["MSFT"])
		assert.Len(t, mapping, 2)
	})

	t.Run("handles empty mapping", func(t *testing.T) {
		emptyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		}))
		defer emptyServer.Close()

		cfg := createTestSECConfig(emptyServer.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		mapping, err := gateway.GetTickerCIKMapping(ctx)

		require.NoError(t, err)
		assert.NotNil(t, mapping)
		assert.Len(t, mapping, 0)
	})
}

func TestGateway_HealthCheck(t *testing.T) {
	t.Run("passes health check with working server", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return simple successful response for ticker mapping
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		}))
		defer server.Close()

		cfg := createTestSECConfig(server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		err := gateway.HealthCheck(ctx)

		assert.NoError(t, err)
	})

	t.Run("fails health check with broken server", func(t *testing.T) {
		brokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer brokenServer.Close()

		cfg := createTestSECConfig(brokenServer.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		err := gateway.HealthCheck(ctx)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "health check failed")
	})
}

// Note: convertCIKToTicker is a private method and tested indirectly through GetCompanyFacts

func TestGateway_ContextCancellation(t *testing.T) {
	t.Run("respects context cancellation", func(t *testing.T) {
		// Create slow server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		}))
		defer server.Close()

		cfg := createTestSECConfig(server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := gateway.GetCompanyFacts(ctx, "0000320193")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context")
	})

	t.Run("respects context timeout", func(t *testing.T) {
		// Create slow server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		}))
		defer server.Close()

		cfg := createTestSECConfig(server.URL)
		cfg.RequestTimeout = 50 * time.Millisecond // Short timeout
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		_, err := gateway.GetCompanyFacts(ctx, "0000320193")
		assert.Error(t, err)
		// Should timeout due to client timeout
	})
}

func TestGateway_RateLimiting(t *testing.T) {
	t.Run("respects rate limiting", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		}))
		defer server.Close()

		cfg := createTestSECConfig(server.URL)
		cfg.RateLimit = 1 // Very low rate limit
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()

		// Make multiple rapid requests
		start := time.Now()
		for i := 0; i < 3; i++ {
			_, _ = gateway.GetTickerCIKMapping(ctx)
		}
		duration := time.Since(start)

		// Should take at least 2 seconds due to rate limiting (1 req/sec, 3 requests)
		assert.Greater(t, duration, 1*time.Second)
		assert.Equal(t, 3, requestCount)
	})
}

func TestGateway_RetryLogic(t *testing.T) {
	t.Run("retries on server error", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount++
			if requestCount < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// Succeed on third attempt
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		}))
		defer server.Close()

		cfg := createTestSECConfig(server.URL)
		cfg.MaxRetries = 3
		cfg.RetryBackoffBase = 10 * time.Millisecond // Fast retry for test
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		mapping, err := gateway.GetTickerCIKMapping(ctx)

		assert.NoError(t, err)
		assert.NotNil(t, mapping)
		assert.Equal(t, 3, requestCount) // Should have retried and succeeded
	})

	t.Run("fails after max retries", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		cfg := createTestSECConfig(server.URL)
		cfg.MaxRetries = 2
		cfg.RetryBackoffBase = 10 * time.Millisecond // Fast retry for test
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		mapping, err := gateway.GetTickerCIKMapping(ctx)

		assert.Error(t, err)
		assert.Nil(t, mapping)
		assert.Equal(t, 2, requestCount) // Should have made exactly 2 attempts
	})
}

// ---------------------------------------------------------------------------
// Tests for Gateway wrapper methods: ParseFinancialData, NormalizeFinancialData,
// and GetFinancialDataForTicker. These delegate to the internal parser so we
// exercise the full gateway -> parser path.
// ---------------------------------------------------------------------------

func TestGateway_ParseFinancialData_Success(t *testing.T) {
	cfg := createTestSECConfig("https://data.sec.gov/api/xbrl")
	logger := zap.NewNop()
	gateway := NewGateway(cfg, logger)

	// Build a rich SECCompanyFacts payload covering multiple XBRL tags
	facts := &ports.SECCompanyFacts{
		CIK:        "320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"us-gaap": {
				"Revenues": makeUSDFactGroup("Revenues", 383285000000, 2023, "FY"),
				"OperatingIncomeLoss": makeUSDFactGroup(
					"Operating Income", 114301000000, 2023, "FY"),
				"Assets": makeUSDFactGroup("Total Assets", 352755000000, 2023, "FY"),
			},
		},
	}

	ctx := context.Background()
	historical, err := gateway.ParseFinancialData(ctx, facts)

	require.NoError(t, err)
	assert.NotNil(t, historical)
	assert.Contains(t, historical.Data, "2023FY")
	assert.Equal(t, 383285000000.0, historical.Data["2023FY"].Revenue)
}

func TestGateway_ParseFinancialData_NilFacts(t *testing.T) {
	cfg := createTestSECConfig("https://data.sec.gov/api/xbrl")
	logger := zap.NewNop()
	gateway := NewGateway(cfg, logger)

	ctx := context.Background()
	historical, err := gateway.ParseFinancialData(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, historical)
	assert.Contains(t, err.Error(), "facts cannot be nil")
}

func TestGateway_NormalizeFinancialData_Success(t *testing.T) {
	cfg := createTestSECConfig("https://data.sec.gov/api/xbrl")
	logger := zap.NewNop()
	gateway := NewGateway(cfg, logger)

	data := &entities.FinancialData{
		Ticker:           "AAPL",
		OperatingIncome:  100000000,
		TotalAssets:      300000000,
		Goodwill:         10000000,
		OtherIntangibles: 5000000,
		TaxRate:          0.21,
		FilingPeriod:     "2023FY",
		FilingDate:       time.Now(),
		AsOf:             time.Now(),
	}

	ctx := context.Background()
	normalized, err := gateway.NormalizeFinancialData(ctx, data)

	require.NoError(t, err)
	assert.True(t, normalized.HasNormalizedData)
	assert.Equal(t, 285000000.0, normalized.TangibleAssets) // 300M - 10M - 5M
}

func TestGateway_NormalizeFinancialData_NilData(t *testing.T) {
	cfg := createTestSECConfig("https://data.sec.gov/api/xbrl")
	logger := zap.NewNop()
	gateway := NewGateway(cfg, logger)

	ctx := context.Background()
	normalized, err := gateway.NormalizeFinancialData(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, normalized)
	assert.Contains(t, err.Error(), "data cannot be nil")
}

func TestGateway_GetFinancialDataForTicker_Success(t *testing.T) {
	// Build a complete SECCompanyFacts response that the mock server will return.
	// This exercises the full gateway flow: fetch -> parse -> normalize -> set ticker.
	mockFacts := &ports.SECCompanyFacts{
		CIK:        "320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"us-gaap": {
				"Revenues":            makeUSDFactGroup("Revenues", 383285000000, 2023, "FY"),
				"OperatingIncomeLoss": makeUSDFactGroup("Operating Income", 114301000000, 2023, "FY"),
				"Assets":              makeUSDFactGroup("Total Assets", 352755000000, 2023, "FY"),
				"Goodwill":            makeUSDFactGroup("Goodwill", 5000000000, 2023, "FY"),
				"LongTermDebt":        makeUSDFactGroup("Long-Term Debt", 100000000000, 2023, "FY"),
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockFacts)
	}))
	defer server.Close()

	cfg := createTestSECConfig(server.URL)
	cfg.MaxRetries = 1
	cfg.RetryBackoffBase = time.Millisecond
	logger := zap.NewNop()
	gateway := NewGateway(cfg, logger)

	ctx := context.Background()
	historical, err := gateway.GetFinancialDataForTicker(ctx, "AAPL", "0000320193")

	require.NoError(t, err)
	assert.NotNil(t, historical)

	// Verify the ticker is set on the top-level struct and on each period entry
	assert.Equal(t, "AAPL", historical.Ticker)
	for _, data := range historical.Data {
		assert.Equal(t, "AAPL", data.Ticker)
		assert.True(t, data.HasNormalizedData, "each period should be normalized")
	}
}

func TestGateway_GetFinancialDataForTicker_FetchError(t *testing.T) {
	// Server always returns 500 so the fetch step fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := createTestSECConfig(server.URL)
	cfg.MaxRetries = 1
	cfg.RetryBackoffBase = time.Millisecond
	logger := zap.NewNop()
	gateway := NewGateway(cfg, logger)

	ctx := context.Background()
	historical, err := gateway.GetFinancialDataForTicker(ctx, "AAPL", "0000320193")

	assert.Error(t, err)
	assert.Nil(t, historical)
	assert.Contains(t, err.Error(), "failed to get company facts")
}

func TestGateway_GetFinancialDataForTicker_ParseError(t *testing.T) {
	// Return valid JSON but with no recognized financial concepts so parsing fails
	mockFacts := &ports.SECCompanyFacts{
		CIK:        "320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"other": {
				"UnknownConcept": makeUSDFactGroup("Unknown", 100, 2023, "FY"),
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockFacts)
	}))
	defer server.Close()

	cfg := createTestSECConfig(server.URL)
	cfg.MaxRetries = 1
	cfg.RetryBackoffBase = time.Millisecond
	logger := zap.NewNop()
	gateway := NewGateway(cfg, logger)

	ctx := context.Background()
	historical, err := gateway.GetFinancialDataForTicker(ctx, "AAPL", "0000320193")

	assert.Error(t, err)
	assert.Nil(t, historical)
	assert.Contains(t, err.Error(), "failed to parse financial data")
}

// makeUSDFactGroup is a helper that creates a SECFactGroup with a single USD fact
// at the given fiscal year and period. Reduces boilerplate in test data setup.
func makeUSDFactGroup(label string, val float64, fy int, fp string) ports.SECFactGroup {
	return ports.SECFactGroup{
		Label:       label,
		Description: label + " description",
		Units: map[string][]ports.SECFact{
			"USD": {
				{
					End:   "2023-09-30",
					Val:   val,
					Accn:  "0000320193-23-000106",
					Fy:    fy,
					Fp:    fp,
					Form:  "10-K",
					Filed: "2023-11-03",
					Frame: fmt.Sprintf("CY%dQ3I", fy),
				},
			},
		},
	}
}
