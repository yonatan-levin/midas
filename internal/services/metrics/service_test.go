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
		service.RecordValuationError("data_fetch_error")
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
	// GetRegistry returns prometheus.DefaultRegisterer regardless of how the
	// service was constructed, so a custom-registry service is fine here.
	service := NewServiceWithRegistry(logger, prometheus.NewRegistry())

	registry := service.GetRegistry()
	assert.NotNil(t, registry)
}

// TestMetricsService_RegistryReceivesMetrics pins PREX-1: NewService must
// register Midas metrics on the service-owned registry so promhttp.HandlerFor
// (wired in server.go) can surface them on /metrics. Pre-fix, promauto.Factory{}
// (zero value, nil registerer) silently dropped every registration and the
// /metrics endpoint returned only Go runtime data.
//
// We assert by gathering directly from the service's registry rather than
// DefaultGatherer — Midas deliberately avoids the global registerer to keep
// metric names (e.g. "go_info") from colliding with the standard Go
// collectors.
func TestMetricsService_RegistryReceivesMetrics(t *testing.T) {
	logger := zap.NewNop()
	svc := NewService(logger)
	svc.RecordHTTPRequest("GET", "/api/v1/fair-value/:ticker", 200, 100*time.Millisecond, 1024)

	families, err := svc.GetRegistry().Gather()
	if err != nil {
		t.Fatalf("registry.Gather error: %v", err)
	}

	found := false
	for _, f := range families {
		if f.GetName() == "http_requests_total" {
			found = true
			break
		}
	}
	assert.True(t, found, "http_requests_total must be registered on the service registry")
}

// TestMetricsService_OwnedRegistry_HasGoRuntimeMetrics pins Task C of the
// 2026-04-25 follow-through: when NewService allocates its own registry,
// the standard Go runtime + process collectors must be registered alongside
// Midas-specific metrics so /metrics surfaces go_goroutines, go_memstats_*,
// process_cpu_seconds_total, etc. Pre-fix (PREX-1's first cut), the service-
// owned registry held only Midas series and operators lost runtime visibility.
//
// Caller-supplied registries are intentionally NOT augmented (test isolation),
// so this test must construct via NewService (the no-custom-registry path).
func TestMetricsService_OwnedRegistry_HasGoRuntimeMetrics(t *testing.T) {
	logger := zap.NewNop()
	svc := NewService(logger)

	families, err := svc.GetRegistry().Gather()
	if err != nil {
		t.Fatalf("registry.Gather error: %v", err)
	}

	want := map[string]bool{
		"go_goroutines":             false,
		"go_memstats_alloc_bytes":   false,
		"process_cpu_seconds_total": false,
	}
	for _, f := range families {
		if _, ok := want[f.GetName()]; ok {
			want[f.GetName()] = true
		}
	}
	for name, seen := range want {
		assert.True(t, seen, "owned registry must surface %q from the standard Go/process collector", name)
	}
}

// TestMetricsService_CallerSuppliedRegistry_NoGoCollector pins the inverse:
// when a caller passes their own registry, the service must NOT auto-register
// runtime collectors — the caller owns the policy, and tests rely on this for
// strict isolation. This guarantees we never accidentally pollute a test
// registry with go_goroutines / go_memstats_* series.
func TestMetricsService_CallerSuppliedRegistry_NoGoCollector(t *testing.T) {
	logger := zap.NewNop()
	customReg := prometheus.NewRegistry()
	svc := NewServiceWithRegistry(logger, customReg)

	families, err := svc.GetRegistry().Gather()
	if err != nil {
		t.Fatalf("registry.Gather error: %v", err)
	}
	for _, f := range families {
		switch f.GetName() {
		case "go_goroutines", "go_memstats_alloc_bytes", "process_cpu_seconds_total":
			t.Errorf("caller-supplied registry must NOT contain %q (Midas should not augment caller registries)", f.GetName())
		}
	}
}

// TestRecordHTTPRequest_StatusCodeLabel pins M2: the status_code label on
// http_requests_total must be the decimal string ("200"), not the
// single-rune encoding (string(rune(200)) == "È", U+00C8) that the
// pre-fix code produced. The bug was invisible until PREX-1 actually
// registered the metric on a gatherer; once /metrics surfaced the label,
// any Grafana panel or Prometheus rule keyed on status_code="200" was
// silently broken.
func TestRecordHTTPRequest_StatusCodeLabel(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	svc := NewServiceWithRegistry(logger, registry)

	svc.RecordHTTPRequest("GET", "/api/v1/fair-value/:ticker", 200, 100*time.Millisecond, 1024)
	svc.RecordHTTPRequest("GET", "/api/v1/fair-value/:ticker", 404, 100*time.Millisecond, 1024)
	svc.RecordHTTPRequest("POST", "/api/v1/fair-value/bulk", 500, 100*time.Millisecond, 1024)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("registry.Gather error: %v", err)
	}

	want := map[string]bool{"200": false, "404": false, "500": false}
	for _, f := range families {
		if f.GetName() != "http_requests_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() != "status_code" {
					continue
				}
				val := lp.GetValue()
				if _, ok := want[val]; ok {
					want[val] = true
				} else {
					t.Errorf("unexpected status_code label value %q (len=%d) — must be a decimal numeric string, not a single-rune encoding",
						val, len(val))
				}
			}
		}
	}
	for code, seen := range want {
		assert.True(t, seen, "status_code=%q must appear as a decimal label on http_requests_total", code)
	}
}

// TestRecordAdjustment pins TDB-4: datacleaner_adjustments_total registers on
// the service-owned registry and increments once per RecordAdjustment call for
// the {rule_id,category,type} label set.
func TestRecordAdjustment(t *testing.T) {
	logger := zap.NewNop()
	registry := prometheus.NewRegistry()
	svc := NewServiceWithRegistry(logger, registry)

	assert.NotPanics(t, func() {
		svc.RecordAdjustment("A1", "asset_quality", "exclude")
		svc.RecordAdjustment("A1", "asset_quality", "exclude")
		svc.RecordAdjustment("B1", "liability_completeness", "treat_as_debt")
	})

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("registry.Gather error: %v", err)
	}

	type key struct{ ruleID, category, typ string }
	got := map[key]int{}
	found := false
	for _, f := range families {
		if f.GetName() != "datacleaner_adjustments_total" {
			continue
		}
		found = true
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			got[key{labels["rule_id"], labels["category"], labels["type"]}] = int(m.GetCounter().GetValue())
		}
	}

	assert.True(t, found, "datacleaner_adjustments_total must be registered on the service registry")
	assert.Equal(t, 2, got[key{"A1", "asset_quality", "exclude"}],
		"two A1 increments must aggregate on the same label set")
	assert.Equal(t, 1, got[key{"B1", "liability_completeness", "treat_as_debt"}])
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
