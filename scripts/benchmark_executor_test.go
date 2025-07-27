package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBenchmarkExecutor_HTTPClient tests the HTTP client implementation
func TestBenchmarkExecutor_HTTPClient(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify authentication
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "test-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}

		// Simulate API delay
		time.Sleep(100 * time.Millisecond)

		// Return mock valuation response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"ticker": "AAPL",
			"wacc": 0.08,
			"growth_rate": 0.05,
			"tangible_value_per_share": 45.67,
			"dcf_value_per_share": 123.45,
			"as_of": "2024-01-15T10:30:00Z"
		}`))
	}))
	defer server.Close()

	executor := NewBenchmarkExecutor(BenchmarkConfig{
		BaseURL: server.URL,
		APIKey:  "test-api-key",
	})

	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
		description    string
	}{
		{
			name:           "successful_api_call",
			endpoint:       "/api/v1/fair-value/AAPL",
			expectedStatus: 200,
			description:    "Should successfully call API with authentication",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			response, err := executor.MakeRequest(ctx, "GET", tt.endpoint, nil)
			require.NoError(t, err, tt.description)
			assert.Equal(t, tt.expectedStatus, response.StatusCode, tt.description)
			assert.Greater(t, response.Duration.Milliseconds(), int64(95), "Should measure request duration")
			assert.NotEmpty(t, response.Body, "Should return response body")
		})
	}
}

// TestBenchmarkExecutor_AuthenticationHandling tests API key authentication
func TestBenchmarkExecutor_AuthenticationHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "missing api key"}`))
			return
		}

		if apiKey != "valid-key" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "invalid api key"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	tests := []struct {
		name           string
		apiKey         string
		expectedStatus int
		description    string
	}{
		{
			name:           "valid_api_key",
			apiKey:         "valid-key",
			expectedStatus: 200,
			description:    "Should accept valid API key",
		},
		{
			name:           "invalid_api_key",
			apiKey:         "invalid-key",
			expectedStatus: 401,
			description:    "Should reject invalid API key",
		},
		{
			name:           "missing_api_key",
			apiKey:         "",
			expectedStatus: 401,
			description:    "Should reject missing API key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := NewBenchmarkExecutor(BenchmarkConfig{
				BaseURL: server.URL,
				APIKey:  tt.apiKey,
			})

			ctx := context.Background()
			response, err := executor.MakeRequest(ctx, "GET", "/api/v1/health", nil)
			require.NoError(t, err, tt.description)
			assert.Equal(t, tt.expectedStatus, response.StatusCode, tt.description)
		})
	}
}

// TestScenarioExecution tests running specific test scenarios
func TestScenarioExecution_SingleTicker(t *testing.T) {
	// Track request count
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		// Simulate varying response times
		delay := time.Duration(requestCount%3) * 50 * time.Millisecond
		time.Sleep(delay)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"ticker": "AAPL",
			"dcf_value_per_share": 123.45
		}`))
	}))
	defer server.Close()

	executor := NewBenchmarkExecutor(BenchmarkConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	scenario := TestScenario{
		Name:           "test_scenario",
		TestType:       "single",
		Duration:       2 * time.Second, // Short test
		Concurrency:    3,
		RequestsPerSec: 10,
		Tickers:        []string{"AAPL"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := executor.RunScenario(ctx, scenario)
	require.NoError(t, err, "Should execute scenario successfully")

	// Verify results
	assert.Equal(t, scenario.Name, result.TestName)
	assert.Greater(t, result.TotalRequests, int64(0), "Should have made requests")
	assert.Greater(t, result.SuccessfulReqs, int64(0), "Should have successful requests")
	assert.GreaterOrEqual(t, result.SuccessfulReqs, result.TotalRequests-result.FailedReqs, "Request count should be consistent")
	assert.Greater(t, result.AvgLatency.Milliseconds(), int64(0), "Should measure latency")
	assert.Greater(t, result.ThroughputRPS, 0.0, "Should calculate throughput")
	assert.GreaterOrEqual(t, result.ErrorRatePercent, 0.0, "Error rate should be non-negative")
	assert.LessOrEqual(t, result.ErrorRatePercent, 100.0, "Error rate should not exceed 100%")

	// Verify the server received requests
	assert.Greater(t, requestCount, 0, "Server should have received requests")
}

// TestScenarioExecution_BulkRequests tests bulk API endpoint performance
func TestScenarioExecution_BulkRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/bulk" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		// Simulate bulk processing delay
		time.Sleep(200 * time.Millisecond)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[
			{"ticker": "AAPL", "dcf_value_per_share": 123.45},
			{"ticker": "MSFT", "dcf_value_per_share": 234.56},
			{"ticker": "GOOGL", "dcf_value_per_share": 345.67}
		]`))
	}))
	defer server.Close()

	executor := NewBenchmarkExecutor(BenchmarkConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	scenario := TestScenario{
		Name:           "bulk_test",
		TestType:       "bulk",
		Duration:       1 * time.Second,
		Concurrency:    2,
		RequestsPerSec: 5,
		Tickers:        []string{"AAPL", "MSFT", "GOOGL"},
	}

	ctx := context.Background()
	result, err := executor.RunScenario(ctx, scenario)
	require.NoError(t, err, "Should execute bulk scenario successfully")

	assert.Equal(t, "bulk_test", result.TestName)
	assert.Greater(t, result.TotalRequests, int64(0), "Should have made bulk requests")
	assert.Greater(t, result.AvgLatency.Milliseconds(), int64(150), "Bulk requests should take longer")
}

// TestErrorHandling tests how the executor handles API errors
func TestErrorHandling_APIErrors(t *testing.T) {
	errorCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errorCount++

		// Return errors for first few requests, then success
		if errorCount <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "internal server error"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ticker": "AAPL", "dcf_value_per_share": 123.45}`))
	}))
	defer server.Close()

	executor := NewBenchmarkExecutor(BenchmarkConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	scenario := TestScenario{
		Name:           "error_test",
		TestType:       "error",
		Duration:       1 * time.Second,
		Concurrency:    1,
		RequestsPerSec: 5,
		Tickers:        []string{"AAPL"},
	}

	ctx := context.Background()
	result, err := executor.RunScenario(ctx, scenario)
	require.NoError(t, err, "Should handle API errors gracefully")

	assert.Greater(t, result.TotalRequests, int64(2), "Should have made multiple requests")
	assert.Greater(t, result.FailedReqs, int64(0), "Should have recorded failed requests")
	assert.Greater(t, result.ErrorRatePercent, 0.0, "Should calculate error rate")
	assert.Less(t, result.ErrorRatePercent, 100.0, "Should have some successful requests")
}

// TestConcurrencyHandling tests concurrent request execution
func TestConcurrencyHandling_ParallelRequests(t *testing.T) {
	concurrentRequests := 0
	maxConcurrent := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		concurrentRequests++
		if concurrentRequests > maxConcurrent {
			maxConcurrent = concurrentRequests
		}

		// Hold request open to test concurrency
		time.Sleep(100 * time.Millisecond)

		concurrentRequests--

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ticker": "AAPL", "dcf_value_per_share": 123.45}`))
	}))
	defer server.Close()

	executor := NewBenchmarkExecutor(BenchmarkConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	scenario := TestScenario{
		Name:           "concurrency_test",
		TestType:       "single",
		Duration:       500 * time.Millisecond,
		Concurrency:    5, // 5 concurrent workers
		RequestsPerSec: 20,
		Tickers:        []string{"AAPL"},
	}

	ctx := context.Background()
	result, err := executor.RunScenario(ctx, scenario)
	require.NoError(t, err, "Should handle concurrent requests")

	assert.Greater(t, result.TotalRequests, int64(3), "Should have made multiple requests")
	assert.Greater(t, maxConcurrent, 1, "Should have processed requests concurrently")
	assert.LessOrEqual(t, maxConcurrent, 5, "Should not exceed concurrency limit")
}

// TestLatencyMeasurement tests accurate latency measurement
func TestLatencyMeasurement_Accuracy(t *testing.T) {
	expectedDelay := 150 * time.Millisecond

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(expectedDelay)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	executor := NewBenchmarkExecutor(BenchmarkConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	ctx := context.Background()
	response, err := executor.MakeRequest(ctx, "GET", "/test", nil)
	require.NoError(t, err, "Should make request successfully")

	// Verify latency measurement accuracy (within 20ms tolerance)
	tolerance := 20 * time.Millisecond
	assert.Greater(t, response.Duration, expectedDelay-tolerance, "Measured latency should be close to actual delay")
	assert.Less(t, response.Duration, expectedDelay+tolerance, "Measured latency should be close to actual delay")
}
