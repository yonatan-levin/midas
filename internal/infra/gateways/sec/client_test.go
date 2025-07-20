package sec

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

func TestNewClient(t *testing.T) {
	cfg := &config.SECConfig{
		BaseURL:          "https://data.sec.gov/api/xbrl",
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()

	client := NewClient(cfg, logger)

	assert.NotNil(t, client)
	assert.Equal(t, cfg, client.config)
	assert.NotNil(t, client.httpClient)
	assert.NotNil(t, client.rateLimiter)
	assert.NotNil(t, client.logger)
}

func TestClient_GetCompanyFacts_Success(t *testing.T) {
	// Mock SEC API response
	mockResponse := &ports.SECCompanyFacts{
		CIK:        "320193",
		EntityName: "Apple Inc.",
		Facts: map[string]ports.SECFactGroup{
			"us-gaap:Revenues": {
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
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		assert.Equal(t, "/companyfacts/CIK0000320193.json", r.URL.Path)
		assert.Equal(t, "Test User Agent", r.Header.Get("User-Agent"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		// Encode with error handling
		if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
			t.Errorf("Failed to encode mock response: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx := context.Background()
	facts, err := client.GetCompanyFacts(ctx, "0000320193")

	require.NoError(t, err)
	assert.NotNil(t, facts)
	assert.Equal(t, "320193", facts.CIK.String())
	assert.Equal(t, "Apple Inc.", facts.EntityName)
	assert.Len(t, facts.Facts, 1)
	assert.Contains(t, facts.Facts, "us-gaap:Revenues")
}

func TestClient_GetCompanyFacts_NotFound(t *testing.T) {
	// Create test server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Company not found"))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx := context.Background()
	facts, err := client.GetCompanyFacts(ctx, "invalid")

	assert.Error(t, err)
	assert.Nil(t, facts)
	assert.Contains(t, err.Error(), "company facts not found (404)")
}

func TestClient_GetCompanyFacts_RateLimit(t *testing.T) {
	// Create test server that returns 429
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Rate limited"))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Millisecond, // Short backoff for testing
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx := context.Background()
	facts, err := client.GetCompanyFacts(ctx, "test")

	assert.Error(t, err)
	assert.Nil(t, facts)
	assert.Contains(t, err.Error(), "rate limited by SEC API (429)")
}

func TestClient_GetCompanyFacts_WithRetry(t *testing.T) {
	requestCount := 0
	mockResponse := &ports.SECCompanyFacts{
		CIK:        "320193",
		EntityName: "Apple Inc.",
		Facts: map[string]ports.SECFactGroup{
			"us-gaap:Revenues": {
				Label:       "Revenues",
				Description: "Revenue from operations",
				Units: map[string][]ports.SECFact{
					"USD": {
						{
							End:   "2023-09-30",
							Val:   383285000000,
							Fy:    2023,
							Fp:    "FY",
							Filed: "2023-11-03",
						},
					},
				},
			},
		},
	}

	// Create test server that fails first 2 requests then succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Server error"))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		// Encode with error handling
		if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
			t.Errorf("Failed to encode mock response: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Millisecond, // Short backoff for testing
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx := context.Background()
	facts, err := client.GetCompanyFacts(ctx, "0000320193")

	require.NoError(t, err)
	assert.NotNil(t, facts)
	assert.Equal(t, 3, requestCount) // Should have retried twice
}

func TestClient_GetTickerCIKMapping_Success(t *testing.T) {
	mockMapping := map[string]interface{}{
		"0": map[string]interface{}{
			"cik_str": "320193",
			"ticker":  "AAPL",
			"title":   "Apple Inc.",
		},
		"1": map[string]interface{}{
			"cik_str": "789019",
			"ticker":  "MSFT",
			"title":   "MICROSOFT CORP",
		},
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Test User Agent", r.Header.Get("User-Agent"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockMapping)
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          "https://data.sec.gov/api/xbrl",
		TickerMappingURL: server.URL, // Point to our mock server
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx := context.Background()
	mapping, err := client.GetTickerCIKMapping(ctx)

	require.NoError(t, err)
	assert.NotNil(t, mapping)
	assert.Equal(t, "320193", mapping["AAPL"])
	assert.Equal(t, "789019", mapping["MSFT"])
	assert.Len(t, mapping, 2)
}

func TestClient_HealthCheck_Success(t *testing.T) {
	// Create test server that mocks the ticker mapping endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock the ticker mapping response that HealthCheck calls
		mockMapping := map[string]interface{}{
			"0": map[string]interface{}{
				"cik_str": "320193",
				"ticker":  "AAPL",
				"title":   "Apple Inc.",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockMapping)
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          "https://data.sec.gov/api/xbrl",
		TickerMappingURL: server.URL, // Point to our mock server
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.NoError(t, err)
}

func TestClient_HealthCheck_Failure(t *testing.T) {
	// Create test server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          "https://data.sec.gov/api/xbrl",
		TickerMappingURL: server.URL, // Point to our mock server that returns error
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.Error(t, err)
}

func TestClient_RateLimiting(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockResponse := &ports.SECCompanyFacts{
			CIK:        "320193",
			EntityName: "Apple Inc.",
			Facts: map[string]ports.SECFactGroup{
				"us-gaap:Revenues": {
					Label:       "Revenues",
					Description: "Revenue from operations",
					Units: map[string][]ports.SECFact{
						"USD": {{End: "2023-09-30", Val: 383285000000, Fy: 2023, Fp: "FY", Filed: "2023-11-03"}},
					},
				},
			},
		}
			// Encode with error handling
			if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
				t.Errorf("Failed to encode mock response: %v", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test User Agent",
		RateLimit:        1, // Very restrictive rate limit
		RequestTimeout:   30 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx := context.Background()

	// First request should succeed
	start := time.Now()
	_, err := client.GetCompanyFacts(ctx, "0000320193")
	assert.NoError(t, err)

	// Second request should be rate limited (should take at least 1 second)
	_, err = client.GetCompanyFacts(ctx, "0000320193")
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, time.Second)
}

func TestClient_ContextCancellation(t *testing.T) {
	// Create test server with delay
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		mockResponse := &ports.SECCompanyFacts{
			CIK:        "320193",
			EntityName: "Apple Inc.",
			Facts: map[string]ports.SECFactGroup{
				"us-gaap:Revenues": {
					Label:       "Revenues",
					Description: "Revenue from operations",
					Units: map[string][]ports.SECFact{
						"USD": {{End: "2023-09-30", Val: 383285000000, Fy: 2023, Fp: "FY", Filed: "2023-11-03"}},
					},
				},
			},
		}
		// Encode with error handling
		if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
			t.Errorf("Failed to encode mock response: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	// Create context that cancels quickly
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.GetCompanyFacts(ctx, "0000320193")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}
