# PREX-1 — Custom Prometheus metrics are never registered with the default registry

**Status:** Pre-existing bug (predates `feat/observability` — `git diff master..HEAD -- internal/services/metrics/` shows 0 lines). Discovered during Phase M/U QA validation, 2026-04-23.
**Severity:** Major (operational visibility) but NOT a blocker for the observability merge because it's unrelated to that work.

## Symptom

`curl http://localhost:8080/metrics` returns the standard Go runtime + process metrics, but **none of the 28 Midas-specific metrics** defined in `internal/services/metrics/service.go` appear — not `http_requests_total`, not `midas_valuations_total`, not `midas_sec_api_requests_total`, etc. Grafana / Prometheus dashboards wired to scrape these labels silently get zero data.

## Root cause

`internal/services/metrics/service.go:107` (identical at master HEAD and at `feat/observability` HEAD):

```go
var factory promauto.Factory
if customRegistry != nil {
    factory = promauto.With(customRegistry)
} else {
    factory = promauto.Factory{}   // <-- BUG
}
```

`promauto.Factory{}` is the zero value: its unexported `r prometheus.Registerer` field is `nil`. Every `factory.NewCounterVec(...)` call checks `if f.r != nil` before registering; when `r` is nil, the metric is *created* (so runtime updates don't crash) but is *never added to any registry*. `promhttp.Handler()` reads from `prometheus.DefaultGatherer`, which never sees these metrics.

## Why unit tests don't catch it

All existing metrics-service tests pass a `customRegistry` (via `NewServiceWithRegistry`), which hits the `promauto.With(customRegistry)` branch. The production path (`NewService` → `customRegistry == nil` → `promauto.Factory{}`) is never exercised by tests.

## Fix

Replace the zero-value fallback with the default registerer:

```go
} else {
    factory = promauto.With(prometheus.DefaultRegisterer)
}
```

## Required test

Add `TestMetricsService_DefaultRegistryRegistration`:

```go
func TestMetricsService_DefaultRegistryRegistration(t *testing.T) {
    svc := metrics.NewService(zap.NewNop())
    svc.RecordHTTPRequest("GET", "/api/v1/fair-value/:ticker", 200, 100*time.Millisecond, 1024)

    families, err := prometheus.DefaultGatherer.Gather()
    require.NoError(t, err)

    found := false
    for _, f := range families {
        if f.GetName() == "http_requests_total" {
            found = true
            break
        }
    }
    require.True(t, found, "http_requests_total must be registered on the default gatherer")
}
```

Note: running this test is mildly tricky because `prometheus.DefaultRegisterer` is a package-level global. Serial test execution within the metrics package is sufficient; if parallel tests are added later, the test should lock or use a dedicated registry.

## Tracked when

- Discovered: Phase M/U QA validation by the QA subagent, 2026-04-23.
- Filed by: controlling agent (main thread) after confirming the bug is pre-existing.
- Owner: BACKEND.
