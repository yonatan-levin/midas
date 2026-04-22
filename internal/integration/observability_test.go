// Package integration contains end-to-end integration tests.
//
// This file verifies Phase M of the observability upgrade:
// a single GET /api/v1/fair-value/AAPL request must produce exactly 12
// structured log entries with event="calc", covering the full DCF calculation
// pipeline from data_fetch through final. All 12 entries must carry the same
// request_id that the server echoes back via the X-Request-ID response header.
package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/api"
	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/config"
	coreEntities "github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/di"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// expectedCalcStages lists the 12 mandatory stage names that the DCF pipeline
// must emit in a single fair-value calculation. The set is validated as a whole
// (order-independent) to tolerate any future re-ordering without breaking the test.
var expectedCalcStages = []string{
	"data_fetch",
	"data_clean_summary",
	"industry_classification",
	"model_selection",
	"growth",
	"wacc",
	"fcf_projection",
	"terminal_value",
	"discount",
	"equity_bridge",
	"cross_check",
	"final",
}

// TestObservability_CalcTraces_ExactlyTwelvePerRequest fires a single
// GET /api/v1/fair-value/AAPL against a fully-wired (but in-memory) server
// and asserts the following invariants about the structured log output:
//
//  1. The HTTP response is 200 OK.
//  2. Exactly 12 log entries carry event="calc".
//  3. The set of stage values across those 12 entries equals expectedCalcStages exactly.
//  4. Every one of the 12 entries carries the same request_id value, and that
//     value matches the X-Request-ID response header.
func TestObservability_CalcTraces_ExactlyTwelvePerRequest(t *testing.T) {
	// Change working directory to the project root so that relative paths like
	// "./config/industry_multiples.json" and "./config/country_risk.json" resolve
	// correctly. The valuation service's NewService constructor loads these files
	// using the hardcoded DefaultIndustryMultiplesPath constant which is relative
	// to the process working directory.
	//
	// Go 1.24 testing.T.Chdir restores the original directory automatically via
	// t.Cleanup, so this does not affect other tests.
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is internal/integration/observability_test.go — two levels up is the project root.
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	t.Chdir(projectRoot)

	// Make the test's dependency on config/industry_multiples.json explicit.
	// Without this file, the valuation service's industryMultiples is nil, the
	// cross_check trace does NOT emit, and the assertion "exactly 12 calc
	// entries" fails with a confusing count=11 message. A FileExists assertion
	// up front turns that into a clear "missing config file" diagnostic.
	require.FileExists(t, filepath.Join(projectRoot, "config", "industry_multiples.json"),
		"cross_check emit depends on this config file")

	// --- 1. Build the observer-backed logger (captures Info+ entries) ---
	// zapcore.InfoLevel: Emitter logs at Info when TraceCalculations=true.
	observerCore, observedLogs := observer.New(zapcore.InfoLevel)
	obsLogger := zap.New(observerCore)

	// --- 2. Build a config with TraceCalculations = true and in-memory SQLite ---
	// We reuse the standard test-config helper and then set TraceCalculations.
	// No Docker / Redis required: the standard helper falls back to an
	// in-memory cache when Redis is unavailable.
	cfg := createTestConfig("redis://127.0.0.1:0") // triggers in-memory cache fallback
	cfg.Logging.TraceCalculations = true
	// Disable external network calls so the test is deterministic.
	cfg.Market.YFinance.Enabled = false
	cfg.Macro.FREDEnabled = false
	cfg.Macro.ManualRiskFreeRate = 0.04
	cfg.Macro.ManualMarketRiskPremium = 0.05

	// --- 3. Wire the full DI graph, replacing the default logger with the observer ---
	var (
		database       *sqlx.DB
		authService    *auth.Service
		valuationSvc   *valuation.Service
		rateLimiter    *ratelimit.RateLimiter
		healthHandler  *handlers.HealthHandler
		metricsService *metrics.Service
		fairValueHdlr  *handlers.FairValueHandler
	)

	app := fxtest.New(t,
		// Override config + logger providers.
		fx.Provide(func() *config.Config { return cfg }),
		// fx.Decorate replaces *zap.Logger for all consumers in this scope,
		// so calclog.Emitter, valuation.Service, datacleaner.Service, etc.
		// all receive the observer-backed logger.
		fx.Decorate(func() *zap.Logger { return obsLogger }),

		// Full application modules — CoreModule provides calclog.NewEmitter
		// (which reads cfg.Logging.TraceCalculations at construction time).
		di.CoreModule,
		di.ServiceModule,
		di.HandlerModule,
		fx.Provide(handlers.NewFairValueHandler),

		// Populate pointers we need to build the HTTP server and seed data.
		fx.Populate(
			&database,
			&authService,
			&valuationSvc,
			&rateLimiter,
			&healthHandler,
			&metricsService,
			&fairValueHdlr,
		),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	// --- 4. Initialise the in-memory database ---
	SetupDatabase(t, database)
	SeedTestData(t, database)

	// --- 5. Build the HTTP router (uses obsLogger so request_id is embedded) ---
	srv := api.NewServer(cfg, obsLogger, valuationSvc, authService, rateLimiter, healthHandler, metricsService)
	router := srv.Engine()

	// --- 6. Create an API key with fair-value permission ---
	ctx := context.Background()
	apiKey, err := authService.CreateKey(ctx, "obs-test-user", []coreEntities.Permission{
		coreEntities.PermissionReadFairValue,
	})
	require.NoError(t, err, "should create API key for observability test")

	// --- 7. Fire the request ---
	w := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "/api/v1/fair-value/AAPL", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", apiKey.Key)

	router.ServeHTTP(w, req)

	// --- 8. Assert HTTP success ---
	require.Equal(t, http.StatusOK, w.Code,
		"expected 200 OK; body: %s", w.Body.String())

	// Capture the request_id the server echoed back.
	responseRequestID := w.Header().Get("X-Request-ID")
	require.NotEmpty(t, responseRequestID, "X-Request-ID must be present in response")

	// --- 9. Filter observer log entries to only "calc" events ---
	// The observer captures ALL Info+ entries, including request/response logs.
	// We filter down to entries where the "event" field == "calc".
	allEntries := observedLogs.All()
	calcEntries := filterCalcEntries(allEntries)

	// Produce a human-readable dump for easier debugging on failure.
	var stagesFound []string
	for _, e := range calcEntries {
		stage := fieldString(e, "stage")
		rid := fieldString(e, "request_id")
		stagesFound = append(stagesFound, fmt.Sprintf("%s(rid=%s)", stage, rid))
	}
	t.Logf("calc entries found (%d): %v", len(calcEntries), stagesFound)

	// --- 10. Assert exactly 12 "calc" entries ---
	require.Len(t, calcEntries, 12,
		"expected exactly 12 calc stage traces; got %d. Entries: %v", len(calcEntries), stagesFound)

	// --- 11. Assert stage set matches expectedCalcStages exactly ---
	var actualStages []string
	for _, e := range calcEntries {
		actualStages = append(actualStages, fieldString(e, "stage"))
	}
	sort.Strings(actualStages)
	expectedSorted := make([]string, len(expectedCalcStages))
	copy(expectedSorted, expectedCalcStages)
	sort.Strings(expectedSorted)
	assert.Equal(t, expectedSorted, actualStages,
		"stage set must exactly match the 12 expected stages")

	// --- 12. Assert all 12 entries carry the same request_id that matches the response header ---
	for i, e := range calcEntries {
		rid := fieldString(e, "request_id")
		assert.Equal(t, responseRequestID, rid,
			"calc entry #%d (stage=%s) must carry request_id=%s; got %s",
			i, fieldString(e, "stage"), responseRequestID, rid)
	}
}

// filterCalcEntries returns only log entries where the structured field
// "event" has the value "calc". This separates calc-stage traces from
// general request/response/middleware log lines.
func filterCalcEntries(entries []observer.LoggedEntry) []observer.LoggedEntry {
	var out []observer.LoggedEntry
	for _, e := range entries {
		if fieldString(e, "event") == "calc" {
			out = append(out, e)
		}
	}
	return out
}

// fieldString searches for a zap.Field with the given key in the entry's
// context and returns its string value. Returns "" when not found.
func fieldString(e observer.LoggedEntry, key string) string {
	for _, f := range e.Context {
		if f.Key == key {
			return f.String
		}
	}
	return ""
}
