package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNewService(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	assert.NotNil(t, service)
	assert.NotNil(t, service.logger)
	assert.False(t, service.startTime.IsZero())

	// Verify metrics are initialized
	assert.NotNil(t, service.httpRequestsTotal)
	assert.NotNil(t, service.valuationRequestsTotal)
	assert.NotNil(t, service.dcfCalculationsTotal)
}

func TestHTTPMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Test HTTP request recording - should not panic
	assert.NotPanics(t, func() {
		service.RecordHTTPRequest("GET", "/api/v1/fair-value/AAPL", 200, 100*time.Millisecond, 1024)
	})

	// Test in-flight requests - should not panic
	assert.NotPanics(t, func() {
		service.IncHTTPRequestsInFlight()
		service.DecHTTPRequestsInFlight()
	})
}

func TestValuationMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Test valuation request recording
	assert.NotPanics(t, func() {
		service.RecordValuationRequest("AAPL", "single", "success", 50*time.Millisecond)
	})

	// Test calculation counters
	assert.NotPanics(t, func() {
		service.IncDCFCalculations()
		service.IncWACCCalculations()
	})

	// Test error recording
	assert.NotPanics(t, func() {
		service.RecordValuationError("AAPL", "data_fetch_error")
	})
}

func TestDataSourceMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Test API metrics recording - should not panic
	assert.NotPanics(t, func() {
		service.RecordSECAPIRequest("companyfacts", "success")
		service.RecordMarketAPIRequest("yfinance", "success")
		service.RecordMacroAPIRequest("fred", "success")
		service.RecordDataFetch("sec", "AAPL", 500*time.Millisecond)
		service.RecordDataCleaning("AAPL", "technology", 100*time.Millisecond)
	})
}

func TestCacheMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Test cache metrics - should not panic
	assert.NotPanics(t, func() {
		service.RecordCacheRequest("redis", "get", "hit")
		service.SetCacheHitRatio("redis", 0.85)
		service.SetCacheSize("redis", 1000)
	})
}

func TestRateLimitMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Test rate limit metrics - should not panic
	assert.NotPanics(t, func() {
		service.RecordRateLimitRequest("api_key", "test_key_123")
		service.RecordRateLimitReject("api_key", "quota_exceeded")
	})
}

func TestBusinessMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Test business metrics - should not panic
	assert.NotPanics(t, func() {
		service.SetUniqueTickersServed(50)
		service.SetAverageWACC(0.08)
		service.SetAverageGrowthRate(0.05)
		service.RecordDataQuality("AAPL", "technology", 0.95)
	})
}

func TestDatabaseMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Test database metrics - should not panic
	assert.NotPanics(t, func() {
		service.UpdateDBStats(5, 10, 15)
		service.RecordDBQuery("SELECT", "financial_data", 10*time.Millisecond)
	})
}

func TestSystemMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Test system metrics update - should not panic
	assert.NotPanics(t, func() {
		service.UpdateSystemMetrics()
	})
}

func TestHealthCheck(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	err := service.HealthCheck()
	assert.NoError(t, err)
}

func TestGetRegistry(t *testing.T) {
	logger := zap.NewNop()
	service := NewService(logger) // Use default service to test global registry

	registry := service.GetRegistry()
	assert.NotNil(t, registry)
}

// Benchmark test for metrics recording performance
func BenchmarkRecordHTTPRequest(b *testing.B) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.RecordHTTPRequest("GET", "/api/v1/fair-value/AAPL", 200, 100*time.Millisecond, 1024)
	}
}

func BenchmarkRecordValuationRequest(b *testing.B) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.RecordValuationRequest("AAPL", "single", "success", 50*time.Millisecond)
	}
}

func BenchmarkUpdateSystemMetrics(b *testing.B) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.UpdateSystemMetrics()
	}
}

// TestService_GetterFunctions_ZeroState verifies all getter functions return
// sensible defaults when no requests have been recorded yet. This covers the
// zero-division guard branches in GetAverageResponseTime and GetErrorRate.
func TestService_GetterFunctions_ZeroState(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	assert.Equal(t, int64(0), service.GetTotalRequests(), "no requests recorded yet")
	assert.Equal(t, 0, service.GetActiveConnections(), "no active connections yet")
	assert.Equal(t, 0.0, service.GetAverageResponseTime(), "zero requests means zero avg response time")
	assert.Equal(t, 0.0, service.GetErrorRate(), "zero requests means zero error rate")
	assert.Equal(t, 0.0, service.GetCacheHitRate(), "no cache hit rate set yet")
	assert.Equal(t, int64(0), service.GetTotalValuations(), "no valuations recorded yet")
	assert.Equal(t, int64(0), service.GetSuccessfulValuations(), "no successful valuations yet")
	assert.Equal(t, int64(0), service.GetFailedValuations(), "no failed valuations yet")
	assert.Equal(t, 0.0, service.GetAverageWACC(), "no WACC set yet")
	assert.Equal(t, 0.0, service.GetAverageGrowthRate(), "no growth rate set yet")
	assert.Equal(t, int64(0), service.GetUniqueTickersServed(), "no tickers served yet")
}

// TestService_GetterFunctions_AfterRecording verifies all getter functions return
// correct computed values after recording HTTP requests, valuations, and cache state.
// This covers the non-zero branches of GetAverageResponseTime and GetErrorRate.
func TestService_GetterFunctions_AfterRecording(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Record 3 HTTP requests: 2 successful (200), 1 error (500).
	// Each with 100ms duration so totalResponseTime = 300ms.
	service.RecordHTTPRequest("GET", "/api/v1/fair-value/AAPL", 200, 100*time.Millisecond, 512)
	service.RecordHTTPRequest("GET", "/api/v1/fair-value/MSFT", 200, 100*time.Millisecond, 256)
	service.RecordHTTPRequest("GET", "/api/v1/fair-value/GOOG", 500, 100*time.Millisecond, 128)

	// Record valuations: 2 successful for different tickers, 1 failed.
	service.RecordValuationRequest("AAPL", "single", "success", 50*time.Millisecond)
	service.RecordValuationRequest("MSFT", "single", "success", 60*time.Millisecond)
	service.RecordValuationRequest("GOOG", "single", "error", 70*time.Millisecond)

	// Set cache hit rate via the setter that updates internal state.
	service.SetCacheHitRatio("memory", 0.75)

	// Verify HTTP-based getters
	assert.Equal(t, int64(3), service.GetTotalRequests())
	assert.Equal(t, 0, service.GetActiveConnections(), "no in-flight tracking via RecordHTTPRequest")

	// Average response time: each request is 100ms = 100.0ms in state.
	// Total = 300ms, count = 3, average = 100ms.
	expectedAvgResponseTime := 100.0 // milliseconds
	assert.InDelta(t, expectedAvgResponseTime, service.GetAverageResponseTime(), 0.01)

	// Error rate: 1 error out of 3 total requests = 0.333...
	expectedErrorRate := 1.0 / 3.0
	assert.InDelta(t, expectedErrorRate, service.GetErrorRate(), 0.01)

	// Cache hit rate should reflect the last SetCacheHitRatio call
	assert.InDelta(t, 0.75, service.GetCacheHitRate(), 0.001)

	// Valuation getters: 3 total, 2 success, 1 error
	assert.Equal(t, int64(3), service.GetTotalValuations())
	assert.Equal(t, int64(2), service.GetSuccessfulValuations())
	assert.Equal(t, int64(1), service.GetFailedValuations())

	// WACC and growth rate remain at default because we did not set them via state.
	// The state.averageWACC is only updated externally; the SetAverageWACC method
	// only updates the Prometheus gauge, not the internal state.
	assert.Equal(t, 0.0, service.GetAverageWACC())
	assert.Equal(t, 0.0, service.GetAverageGrowthRate())

	// Unique tickers: AAPL, MSFT, GOOG = 3 unique tickers from RecordValuationRequest.
	assert.Equal(t, int64(3), service.GetUniqueTickersServed())
}

// TestHTTPMetricsMiddleware_RecordsMetrics verifies the Gin middleware records
// HTTP request metrics and returns the correct response from the downstream handler.
func TestHTTPMetricsMiddleware_RecordsMetrics(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	service := NewServiceWithRegistry(logger, registry)

	// Set gin to test mode to suppress debug output
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(HTTPMetricsMiddleware(service, logger))

	// Register a simple test handler
	router.GET("/api/v1/fair-value/:ticker", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ticker": c.Param("ticker")})
	})

	// Perform a test HTTP request
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fair-value/AAPL", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Verify the handler responded correctly
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify the middleware updated internal metrics state
	assert.Equal(t, int64(1), service.GetTotalRequests(), "middleware should have recorded 1 request")
	assert.True(t, service.GetAverageResponseTime() >= 0, "response time should be non-negative")
	assert.Equal(t, 0.0, service.GetErrorRate(), "200 response should not count as an error")
}
