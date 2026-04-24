package metrics

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

// TestNormalizeEndpoint tests the endpoint normalization function
func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		fullPath string
		expected string
	}{
		{
			name:     "empty_path",
			fullPath: "",
			expected: "unknown",
		},
		{
			name:     "root_path",
			fullPath: "/",
			expected: "root",
		},
		{
			name:     "health_endpoint",
			fullPath: "/health",
			expected: "health",
		},
		{
			name:     "ready_endpoint",
			fullPath: "/ready",
			expected: "ready",
		},
		{
			name:     "version_endpoint",
			fullPath: "/version",
			expected: "version",
		},
		{
			name:     "fair_value_ticker",
			fullPath: "/api/v1/fair-value/:ticker",
			expected: "/api/v1/fair-value/:ticker",
		},
		{
			name:     "fair_value_bulk",
			fullPath: "/api/v1/fair-value/bulk",
			expected: "/api/v1/fair-value/bulk",
		},
		{
			name:     "detailed_health",
			fullPath: "/api/v1/health/detailed",
			expected: "/api/v1/health/detailed",
		},
		{
			name:     "api_metrics",
			fullPath: "/api/v1/metrics",
			expected: "/api/v1/metrics",
		},
		{
			name:     "prometheus_metrics",
			fullPath: "/metrics",
			expected: "/metrics",
		},
		{
			name:     "unknown_short_path",
			fullPath: "/unknown/path",
			expected: "/unknown/path",
		},
		{
			name:     "very_long_path",
			fullPath: "/very/long/path/that/exceeds/one/hundred/characters/and/should/be/normalized/to/prevent/cardinality/issues",
			expected: "long_path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeEndpoint(tt.fullPath)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestValuationMetricsWrapper tests the valuation metrics wrapper
func TestValuationMetricsWrapper(t *testing.T) {
	t.Run("new_wrapper_creation", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())

		wrapper := NewValuationMetricsWrapper(service, logger)
		assert.NotNil(t, wrapper)
		assert.Equal(t, service, wrapper.metricsService)
		assert.Equal(t, logger, wrapper.logger)
	})

	t.Run("successful_operation_tracking", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())
		wrapper := NewValuationMetricsWrapper(service, logger)

		executed := false
		operation := func() error {
			executed = true
			return nil
		}

		err := wrapper.WrapValuationOperation("AAPL", "fair_value", operation)

		assert.NoError(t, err)
		assert.True(t, executed)
	})

	t.Run("error_operation_tracking", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())
		wrapper := NewValuationMetricsWrapper(service, logger)

		testError := errors.New("valuation failed")
		operation := func() error {
			return testError
		}

		err := wrapper.WrapValuationOperation("MSFT", "dcf", operation)

		assert.Error(t, err)
		assert.Equal(t, testError, err)
	})
}

// TestClassifyValuationError tests the error classification function
func TestClassifyValuationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil_error",
			err:      nil,
			expected: "none",
		},
		{
			name:     "data_fetch_error",
			err:      errors.New("failed to fetch data from API"),
			expected: "data_fetch_error",
		},
		{
			name:     "parsing_error",
			err:      errors.New("failed to parse json response"),
			expected: "parsing_error",
		},
		{
			name:     "cache_error",
			err:      errors.New("cache connection failed"),
			expected: "cache_error",
		},
		{
			name:     "timeout_error",
			err:      errors.New("operation timeout exceeded"),
			expected: "timeout_error",
		},
		{
			name:     "context_deadline_error",
			err:      errors.New("context deadline exceeded"),
			expected: "timeout_error",
		},
		{
			name:     "validation_error",
			err:      errors.New("validation failed for input"),
			expected: "validation_error",
		},
		{
			name:     "calculation_error",
			err:      errors.New("math calculation overflow"),
			expected: "calculation_error",
		},
		{
			name:     "divide_by_zero_error",
			err:      errors.New("divide by zero in WACC calculation"),
			expected: "calculation_error",
		},
		{
			name:     "database_error",
			err:      errors.New("database connection lost"),
			expected: "database_error",
		},
		{
			name:     "sql_error",
			err:      errors.New("sql query failed"),
			expected: "database_error",
		},
		{
			name:     "rate_limit_error",
			err:      errors.New("rate limit exceeded"),
			expected: "rate_limit_error",
		},
		{
			name:     "auth_error",
			err:      errors.New("unauthorized access"),
			expected: "auth_error",
		},
		{
			name:     "unknown_error",
			err:      errors.New("something went wrong"),
			expected: "unknown_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyValuationError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestContains tests the contains helper function
func TestContains(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		substrings []string
		expected   bool
	}{
		{
			name:       "single_match",
			target:     "hello world",
			substrings: []string{"world"},
			expected:   true,
		},
		{
			name:       "multiple_substrings_one_match",
			target:     "error in fetch operation",
			substrings: []string{"fetch", "parse", "validate"},
			expected:   true,
		},
		{
			name:       "no_match",
			target:     "hello world",
			substrings: []string{"foo", "bar"},
			expected:   false,
		},
		{
			name:       "empty_target",
			target:     "",
			substrings: []string{"test"},
			expected:   false,
		},
		{
			name:       "empty_substring",
			target:     "hello",
			substrings: []string{""},
			expected:   true,
		},
		{
			name:       "substring_longer_than_target",
			target:     "hi",
			substrings: []string{"hello"},
			expected:   false,
		},
		{
			name:       "exact_match",
			target:     "test",
			substrings: []string{"test"},
			expected:   true,
		},
		{
			name:       "partial_match",
			target:     "testing",
			substrings: []string{"test"},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contains(tt.target, tt.substrings...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestDataFetchMetricsWrapper tests the data fetch metrics wrapper
func TestDataFetchMetricsWrapper(t *testing.T) {
	t.Run("new_wrapper_creation", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())

		wrapper := NewDataFetchMetricsWrapper(service, logger)
		assert.NotNil(t, wrapper)
		assert.Equal(t, service, wrapper.metricsService)
		assert.Equal(t, logger, wrapper.logger)
	})

	t.Run("wrap_sec_fetch_success", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())
		wrapper := NewDataFetchMetricsWrapper(service, logger)

		executed := false
		operation := func() error {
			executed = true
			return nil
		}

		err := wrapper.WrapSECFetch("/company/CIK123/facts.json", operation)

		assert.NoError(t, err)
		assert.True(t, executed)
	})

	t.Run("wrap_sec_fetch_error", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())
		wrapper := NewDataFetchMetricsWrapper(service, logger)

		testError := errors.New("SEC API error")
		operation := func() error {
			return testError
		}

		err := wrapper.WrapSECFetch("/company/invalid", operation)

		assert.Error(t, err)
		assert.Equal(t, testError, err)
	})

	t.Run("wrap_market_fetch", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())
		wrapper := NewDataFetchMetricsWrapper(service, logger)

		operation := func() error {
			return nil
		}

		err := wrapper.WrapMarketFetch("yahoo_finance", operation)
		assert.NoError(t, err)
	})

	t.Run("wrap_macro_fetch", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())
		wrapper := NewDataFetchMetricsWrapper(service, logger)

		operation := func() error {
			return nil
		}

		err := wrapper.WrapMacroFetch("treasury_api", operation)
		assert.NoError(t, err)
	})

	t.Run("record_data_fetch_timing", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())
		wrapper := NewDataFetchMetricsWrapper(service, logger)

		// Test normal timing
		wrapper.RecordDataFetchTiming("sec", "AAPL", 100*time.Millisecond)

		// Test slow timing (should trigger warning log)
		wrapper.RecordDataFetchTiming("market", "SLOW", 15*time.Second)
	})
}

// TestMiddlewareComponents tests individual middleware components
func TestMiddlewareComponents(t *testing.T) {
	t.Run("valuation_wrapper_creation", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		service := NewServiceWithRegistry(logger, prometheus.NewRegistry())

		wrapper := NewValuationMetricsWrapper(service, logger)
		assert.NotNil(t, wrapper)

		fetchWrapper := NewDataFetchMetricsWrapper(service, logger)
		assert.NotNil(t, fetchWrapper)
	})

	t.Run("error_classification_coverage", func(t *testing.T) {
		// Test various error types
		errors := []error{
			errors.New("fetch timeout"),
			errors.New("json unmarshal error"),
			errors.New("cache miss"),
			errors.New("invalid validation"),
			errors.New("database sql error"),
			errors.New("throttle rate limit"),
			errors.New("permission unauthorized"),
			errors.New("weird error type"),
		}

		for _, err := range errors {
			result := classifyValuationError(err)
			assert.NotEmpty(t, result)
		}
	})
}
