package metrics

import (
	"testing"
	"time"

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
 