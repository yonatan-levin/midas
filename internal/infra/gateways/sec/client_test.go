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
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// Test that GetTickerCIKMapping correctly parses numeric cik_str and returns uppercase tickers
func TestGetTickerCIKMapping_ParsesNumericCIK(t *testing.T) {
	// Prepare a fake SEC mapping JSON (object keyed by numeric strings)
	sample := map[string]map[string]interface{}{
		"0": {"cik_str": 320193, "ticker": "AAPL", "title": "Apple Inc."},
		"1": {"cik_str": 1309251, "ticker": "MALG", "title": "MICROALLIANCE GROUP INC."},
		"2": {"cik_str": 98677, "ticker": "TR", "title": "TOOTSIE ROLL INDUSTRIES INC"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sample)
	}))
	defer srv.Close()

	cfg := &config.SECConfig{
		BaseURL:          "",
		TickerMappingURL: srv.URL,
		UserAgent:        "midas-test/1.0",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: 100 * time.Millisecond,
	}

	logger := zap.NewNop()
	client := NewClient(cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mapping, err := client.GetTickerCIKMapping(ctx)
	if err != nil {
		t.Fatalf("GetTickerCIKMapping returned error: %v", err)
	}

	if got := mapping["AAPL"]; got != "320193" {
		t.Fatalf("expected AAPL to map to 320193, got %q", got)
	}
	if got := mapping["MALG"]; got != "1309251" {
		t.Fatalf("expected MALG to map to 1309251, got %q", got)
	}
	if got := mapping["TR"]; got != "98677" {
		t.Fatalf("expected TR to map to 98677, got %q", got)
	}
}

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
	// Mock SEC API response with nested taxonomy -> concept structure
	mockResponse := &ports.SECCompanyFacts{
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

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		assert.Equal(t, "/api/xbrl/companyfacts/CIK0000320193.json", r.URL.Path)
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
	assert.Contains(t, facts.Facts, "us-gaap")
	assert.Contains(t, facts.Facts["us-gaap"], "Revenues")
}

func TestClient_GetCompanyFacts_NotFound(t *testing.T) {
	// Create test server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Company not found"))
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
	facts, err := client.GetCompanyFacts(ctx, "0000000001")

	assert.Error(t, err)
	assert.Nil(t, facts)
	assert.Contains(t, err.Error(), "company facts not found (404)")
}

func TestClient_GetCompanyFacts_RateLimit(t *testing.T) {
	// Create test server that returns 429
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("Rate limited"))
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

	assert.Error(t, err)
	assert.Nil(t, facts)
	assert.Contains(t, err.Error(), "rate limited by SEC API (429)")
}

func TestClient_GetCompanyFacts_WithRetry(t *testing.T) {
	requestCount := 0
	mockResponse := &ports.SECCompanyFacts{
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
								Fy:    2023,
								Fp:    "FY",
								Filed: "2023-11-03",
							},
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
			_, _ = w.Write([]byte("Server error"))
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
		_ = json.NewEncoder(w).Encode(mockMapping)
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
		_ = json.NewEncoder(w).Encode(mockMapping)
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
			Facts: map[string]map[string]ports.SECFactGroup{
				"us-gaap": {
					"Revenues": {
						Label:       "Revenues",
						Description: "Revenue from operations",
						Units: map[string][]ports.SECFact{
							"USD": {{End: "2023-09-30", Val: 383285000000, Fy: 2023, Fp: "FY", Filed: "2023-11-03"}},
						},
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
			Facts: map[string]map[string]ports.SECFactGroup{
				"us-gaap": {
					"Revenues": {
						Label:       "Revenues",
						Description: "Revenue from operations",
						Units: map[string][]ports.SECFact{
							"USD": {{End: "2023-09-30", Val: 383285000000, Fy: 2023, Fp: "FY", Filed: "2023-11-03"}},
						},
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

// ---------------------------------------------------------------------------
// makeRequest error path tests
// ---------------------------------------------------------------------------

// TestClient_MakeRequest_InvalidJSON verifies error handling when SEC returns
// syntactically invalid JSON that cannot be decoded.
func TestClient_MakeRequest_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyFacts(context.Background(), "320193")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode SEC response")
}

// TestClient_MakeRequest_MissingEntityName verifies error when the JSON response
// has a valid CIK but an empty entity name.
func TestClient_MakeRequest_MissingEntityName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cik":320193,"entityName":"","facts":{"us-gaap":{"Assets":{"label":"Assets","units":{"USD":[]}}}}}`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyFacts(context.Background(), "320193")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing entity name")
}

// TestClient_MakeRequest_EmptyFacts verifies error when the JSON response has no facts.
func TestClient_MakeRequest_EmptyFacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cik":320193,"entityName":"Apple Inc.","facts":{}}`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyFacts(context.Background(), "320193")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no facts found")
}

// TestClient_MakeRequest_DefaultStatusCode verifies the default branch in
// the status code switch (an unexpected HTTP status like 418).
func TestClient_MakeRequest_DefaultStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 418
		_, _ = w.Write([]byte("I'm a teapot"))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyFacts(context.Background(), "320193")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 418")
}

// ---------------------------------------------------------------------------
// makeConceptRequest error path tests
// ---------------------------------------------------------------------------

// TestClient_MakeConceptRequest_RateLimit429 verifies handling of SEC 429 responses.
func TestClient_MakeConceptRequest_RateLimit429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyConcepts(context.Background(), "320193", "Revenues")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rate limited by SEC API (429)")
}

// TestClient_MakeConceptRequest_ServerError verifies handling of 5xx server errors.
func TestClient_MakeConceptRequest_ServerError(t *testing.T) {
	statusCodes := []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	}

	for _, statusCode := range statusCodes {
		t.Run(fmt.Sprintf("status_%d", statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))
			defer server.Close()

			cfg := &config.SECConfig{
				BaseURL:          server.URL,
				UserAgent:        "Test",
				RateLimit:        10,
				RequestTimeout:   5 * time.Second,
				MaxRetries:       1,
				RetryBackoffBase: time.Millisecond,
			}
			client := NewClient(cfg, zap.NewNop())

			_, err := client.GetCompanyConcepts(context.Background(), "320193", "Revenues")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "SEC API server error")
		})
	}
}

// TestClient_MakeConceptRequest_DefaultStatus verifies the default status branch.
func TestClient_MakeConceptRequest_DefaultStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("teapot"))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyConcepts(context.Background(), "320193", "Revenues")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 418")
}

// TestClient_MakeConceptRequest_InvalidJSON verifies error on malformed JSON.
func TestClient_MakeConceptRequest_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyConcepts(context.Background(), "320193", "Revenues")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode SEC concept response")
}

// TestClient_MakeConceptRequest_MissingCIK verifies error when CIK is empty in response.
func TestClient_MakeConceptRequest_MissingCIK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cik":"","entityName":"Apple","tag":"Revenues","units":{}}`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyConcepts(context.Background(), "320193", "Revenues")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing CIK")
}

// TestClient_MakeConceptRequest_MissingTag verifies error when tag is empty in response.
func TestClient_MakeConceptRequest_MissingTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cik":"320193","entityName":"Apple","tag":"","units":{}}`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyConcepts(context.Background(), "320193", "Revenues")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing tag")
}

// ---------------------------------------------------------------------------
// makeTickerMappingRequest error path tests
// ---------------------------------------------------------------------------

// TestClient_MakeTickerMappingRequest_InvalidJSON verifies error on malformed JSON.
func TestClient_MakeTickerMappingRequest_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`broken json{`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		TickerMappingURL: server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetTickerCIKMapping(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode ticker mapping")
}

// TestClient_MakeTickerMappingRequest_DefaultStatus verifies the default status branch.
func TestClient_MakeTickerMappingRequest_DefaultStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("teapot"))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		TickerMappingURL: server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetTickerCIKMapping(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 418")
}

// TestClient_MakeTickerMappingRequest_RateLimit429 verifies handling of 429 on
// the ticker mapping endpoint.
func TestClient_MakeTickerMappingRequest_RateLimit429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		TickerMappingURL: server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetTickerCIKMapping(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rate limited by SEC API (429)")
}

// TestClient_MakeTickerMappingRequest_SkipsEmptyTicker verifies entries with empty
// ticker strings are silently skipped in the mapping.
func TestClient_MakeTickerMappingRequest_SkipsEmptyTicker(t *testing.T) {
	sample := map[string]map[string]interface{}{
		"0": {"cik_str": "320193", "ticker": "AAPL", "title": "Apple Inc."},
		"1": {"cik_str": "789019", "ticker": "", "title": "No Ticker Inc."}, // empty ticker
		"2": {"cik_str": "", "ticker": "NOCIK", "title": "No CIK Inc."},     // empty CIK
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sample)
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		TickerMappingURL: server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	mapping, err := client.GetTickerCIKMapping(context.Background())
	require.NoError(t, err)

	// Only AAPL should be present; empty ticker and empty CIK entries skipped
	assert.Equal(t, "320193", mapping["AAPL"])
	assert.Len(t, mapping, 1)
}

// TestClient_GetCompanyConcepts_InvalidCIK verifies error handling for non-numeric CIK.
func TestClient_GetCompanyConcepts_InvalidCIK(t *testing.T) {
	cfg := &config.SECConfig{
		BaseURL:          "https://data.sec.gov/api/xbrl",
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyConcepts(context.Background(), "INVALID", "Revenues")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid CIK")
}

// TestClient_GetCompanyFacts_InvalidCIK verifies error handling for non-numeric CIK.
func TestClient_GetCompanyFacts_InvalidCIK(t *testing.T) {
	cfg := &config.SECConfig{
		BaseURL:          "https://data.sec.gov/api/xbrl",
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	_, err := client.GetCompanyFacts(context.Background(), "BADCIK")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid CIK")
}

// ---------------------------------------------------------------------------
// GetCompanySIC tests — Item 3: Extract SIC code from SEC submissions endpoint
// ---------------------------------------------------------------------------

// TestClient_GetCompanySIC_Success verifies successful SIC code extraction
// from the SEC submissions endpoint response.
func TestClient_GetCompanySIC_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/submissions/CIK0000320193.json")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cik":"320193","entityType":"operating","sic":"3571","sicDescription":"ELECTRONIC COMPUTERS","name":"Apple Inc."}`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	sic, err := client.GetCompanySIC(context.Background(), "320193")
	require.NoError(t, err)
	assert.Equal(t, "3571", sic)
}

// TestClient_GetCompanySIC_NotFound verifies graceful handling when submissions
// endpoint returns 404 (e.g., unknown CIK).
func TestClient_GetCompanySIC_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	sic, err := client.GetCompanySIC(context.Background(), "9999999")
	assert.Error(t, err)
	assert.Empty(t, sic)
}

// TestClient_GetCompanySIC_NoSICField verifies behavior when the submissions response
// is valid JSON but lacks the "sic" field.
func TestClient_GetCompanySIC_NoSICField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cik":"320193","entityType":"operating","name":"Test Corp"}`))
	}))
	defer server.Close()

	cfg := &config.SECConfig{
		BaseURL:          server.URL,
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	sic, err := client.GetCompanySIC(context.Background(), "320193")
	assert.NoError(t, err)
	assert.Empty(t, sic, "should return empty string when SIC is not in response")
}

// TestClient_GetCompanySIC_InvalidCIK verifies error handling for invalid CIK format.
func TestClient_GetCompanySIC_InvalidCIK(t *testing.T) {
	cfg := &config.SECConfig{
		BaseURL:          "https://data.sec.gov",
		UserAgent:        "Test",
		RateLimit:        10,
		RequestTimeout:   5 * time.Second,
		MaxRetries:       1,
		RetryBackoffBase: time.Millisecond,
	}
	client := NewClient(cfg, zap.NewNop())

	sic, err := client.GetCompanySIC(context.Background(), "BADCIK")
	assert.Error(t, err)
	assert.Empty(t, sic)
}
