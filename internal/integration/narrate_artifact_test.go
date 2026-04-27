// Package integration contains end-to-end integration tests.
//
// This file verifies Phase 1 of docs/refactoring/observability-narrative-and-
// artifacts-spec.md: a single ?trace=1 request must produce the full Tier-1
// narrate stream (every emitted phase) AND a Tier-3 artifact bundle on disk
// with one file per phase plus a manifest. A request WITHOUT ?trace=1 must
// still produce the narrate stream but NO bundle.
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// requiredNarratePhases lists the phases that MUST appear when an end-to-end
// happy-path AAPL valuation runs against the in-memory test fixture. The
// list is a subset of the 17-phase taxonomy in spec §5 — phases that depend
// on configuration not exercised in this test (fetch.* fan-out fires only
// when DataFetcher actually runs, which it does NOT for the seeded-AAPL
// path) are intentionally excluded. The test asserts the listed phases are
// PRESENT, not that the set is exhaustive, so future phases can be added
// without breaking the assertion.
var requiredNarratePhases = []string{
	"request.received",
	"auth.resolved",
	"ratelimit.checked",
	"handler.entry",
	"cache.lookup",
	"clean.normalized",
	"classify.industry",
	"growth.estimated",
	"wacc.computed",
	"model.selected",
	"valuation.computed",
	"crosscheck.evaluated",
	"response.sent",
}

// TestNarrateArtifact_TraceOn_EmitsStreamAndBundle verifies that
// GET /api/v1/fair-value/AAPL?trace=1 produces:
//
//  1. The full Tier-1 narrate stream (event=narrate) covering the required
//     phases, all sharing the same request_id.
//  2. A Tier-3 bundle directory under the artifacts root containing
//     00-manifest.json plus the per-phase snapshot files.
//  3. A manifest whose phases_recorded[] cross-references the bundled files.
func TestNarrateArtifact_TraceOn_EmitsStreamAndBundle(t *testing.T) {
	// Resolve project root the same way observability_test.go does, so
	// hardcoded relative config paths inside the valuation service still
	// load correctly.
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	srcMultiples := filepath.Join(projectRoot, "config", "industry_multiples.json")
	require.FileExists(t, srcMultiples, "cross_check emit depends on this config file")
	require.NoError(t, os.MkdirAll("./config", 0o755))
	t.Cleanup(func() { _ = os.RemoveAll("./config") })
	multiplesBytes, err := os.ReadFile(srcMultiples)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile("./config/industry_multiples.json", multiplesBytes, 0o644))

	// Build observer-backed logger so we can inspect emitted log lines.
	observerCore, observedLogs := observer.New(zapcore.InfoLevel)
	obsLogger := zap.New(observerCore)

	// Per-test artifact root so files are cleaned up automatically.
	artifactRoot := t.TempDir()

	cfg := createTestConfig("redis://127.0.0.1:0")
	cfg.Logging.TraceCalculations = true
	cfg.Logging.Narrate.Enabled = true
	cfg.Logging.Narrate.SampleRate = 1.0
	cfg.Logging.ArtifactStore.Enabled = true
	cfg.Logging.ArtifactStore.RootPath = artifactRoot
	cfg.Logging.ArtifactStore.QueueSize = 256
	cfg.Market.YFinance.Enabled = false
	cfg.Macro.FREDEnabled = false
	cfg.Macro.ManualRiskFreeRate = 0.04
	cfg.Macro.ManualMarketRiskPremium = 0.05
	cfg.DataCleaner.RulesPath = filepath.Join(projectRoot, "config", "datacleaner", "rules.json")
	cfg.DataCleaner.IndustryRulesPath = filepath.Join(projectRoot, "config", "datacleaner", "industry")

	var (
		database       *sqlx.DB
		authService    *auth.Service
		valuationSvc   *valuation.Service
		rateLimiter    *ratelimit.RateLimiter
		healthHandler  *handlers.HealthHandler
		metricsService *metrics.Service
	)

	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Decorate(func() *zap.Logger { return obsLogger }),
		di.CoreModule,
		di.ServiceModule,
		di.HandlerModule,
		fx.Provide(handlers.NewFairValueHandler),
		fx.Populate(
			&database,
			&authService,
			&valuationSvc,
			&rateLimiter,
			&healthHandler,
			&metricsService,
		),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	SetupDatabase(t, database)
	SeedTestData(t, database)

	srv := api.NewServer(cfg, obsLogger, valuationSvc, authService, rateLimiter, healthHandler, metricsService)
	router := srv.Engine()

	ctx := context.Background()
	apiKey, err := authService.CreateKey(ctx, "narrate-test-user", []coreEntities.Permission{
		coreEntities.PermissionReadFairValue,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "/api/v1/fair-value/AAPL?trace=1", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", apiKey.Key)

	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	requestID := w.Header().Get("X-Request-ID")
	require.NotEmpty(t, requestID, "X-Request-ID must be present")

	// --- Assert (1): narrate stream covers the required phases, all
	//     sharing the same request_id. ---
	narrateEntries := filterByEvent(observedLogs.All(), "narrate")
	require.NotEmpty(t, narrateEntries, "expected at least one narrate log line")

	phasesSeen := make(map[string]bool)
	for _, e := range narrateEntries {
		phase := fieldString(e, "phase")
		phasesSeen[phase] = true
		rid := fieldString(e, "request_id")
		assert.Equal(t, requestID, rid,
			"narrate entry phase=%s must carry request_id=%s; got %s", phase, requestID, rid)
	}

	for _, p := range requiredNarratePhases {
		assert.True(t, phasesSeen[p], "narrate stream missing required phase=%s; saw=%v", p, phasesSeen)
	}

	// --- Assert (2): bundle directory created with the expected per-phase
	//     files. The directory layout is artifacts/<UTC-date>/AAPL/req_<id>/. ---
	bundleDir := findBundleDir(t, artifactRoot, "AAPL", requestID)
	require.NotEmpty(t, bundleDir, "expected a bundle directory for request %s under %s", requestID, artifactRoot)

	// Spot-check that several per-phase snapshot files landed on disk. We
	// don't insist on every single 17-phase file because some require
	// configuration paths not exercised by the test fixture (e.g. fetch.sec
	// raw bytes), but the core pipeline outputs MUST be present.
	expectedFiles := []string{
		"00-manifest.json",
		"02-handler-options.json",
		"10-clean-input.json",
		"10-clean-output.json",
		"11-classify.json",
		"12-growth-curve.json",
		"13-wacc.json",
		"14-model-selection.json",
		"15-valuation.json",
		"16-crosscheck.json",
		"17-response.json",
	}
	for _, name := range expectedFiles {
		path := filepath.Join(bundleDir, name)
		_, err := os.Stat(path)
		assert.NoError(t, err, "expected bundle file %s in %s", name, bundleDir)
	}

	// --- Assert (3): manifest references the bundled files via
	//     phases_recorded[] entries. ---
	var m artifact.Manifest
	manifestBody, err := os.ReadFile(filepath.Join(bundleDir, "00-manifest.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(manifestBody, &m))
	assert.Equal(t, requestID, m.RequestID)
	assert.Equal(t, "AAPL", m.Ticker)
	assert.NotEmpty(t, m.PhasesRecorded, "manifest must list the recorded phases")

	// Build a set of all files referenced by the manifest and verify each
	// referenced file actually exists on disk.
	for _, ph := range m.PhasesRecorded {
		for _, f := range ph.Files {
			path := filepath.Join(bundleDir, f)
			_, err := os.Stat(path)
			assert.NoError(t, err, "manifest references missing file: %s", path)
		}
	}

	// --- Assert (4): 99-narrate.jsonl contains every emitted narrate phase,
	//     each tagged with the request_id. Pins QA-2026-04-25 MINOR-1 fix:
	//     spec §7.1 + §7.3 promise this file in the bundle and the
	//     BundleSink (commit 5) writes it.
	narrateStream := filepath.Join(bundleDir, "99-narrate.jsonl")
	streamBody, err := os.ReadFile(narrateStream)
	require.NoError(t, err, "99-narrate.jsonl must exist in opened bundle")

	streamLines := strings.Split(strings.TrimSpace(string(streamBody)), "\n")
	require.GreaterOrEqual(t, len(streamLines), len(requiredNarratePhases),
		"narrate stream must have at least one entry per required phase; got %d lines, need >= %d",
		len(streamLines), len(requiredNarratePhases))

	for i, line := range streamLines {
		var entry map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(line), &entry),
			"line %d not valid JSON: %s", i, line)
		assert.Equal(t, "narrate", entry["event"],
			"line %d missing event=narrate", i)
		assert.Equal(t, requestID, entry["request_id"],
			"line %d carries wrong request_id", i)
	}

	// --- Assert (5): 99-debug-trace.jsonl semantics depend on the test
	//     logger's level. The observerCore is constructed at zapcore.InfoLevel
	//     above, so Debug entries are filtered out at the wrapped-core level
	//     BEFORE the BundleSink's Write is invoked — which means the file is
	//     never created. If a future change raises the observer to DebugLevel,
	//     this assertion must flip to require.FileExists + GreaterOrEqual(1).
	debugStream := filepath.Join(bundleDir, "99-debug-trace.jsonl")
	_, err = os.Stat(debugStream)
	assert.True(t, os.IsNotExist(err),
		"99-debug-trace.jsonl must NOT exist when test logger runs at InfoLevel")
}

// TestNarrateArtifact_TraceOff_NoBundleCreated verifies the spec G5 invariant
// for the non-traced path: the same request without ?trace=1 still produces
// narrate lines (the stream is unconditional once narrate.enabled=true) but
// MUST NOT create any bundle directory under the artifact root.
func TestNarrateArtifact_TraceOff_NoBundleCreated(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	srcMultiples := filepath.Join(projectRoot, "config", "industry_multiples.json")
	require.FileExists(t, srcMultiples)
	require.NoError(t, os.MkdirAll("./config", 0o755))
	t.Cleanup(func() { _ = os.RemoveAll("./config") })
	multiplesBytes, err := os.ReadFile(srcMultiples)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile("./config/industry_multiples.json", multiplesBytes, 0o644))

	observerCore, observedLogs := observer.New(zapcore.InfoLevel)
	obsLogger := zap.New(observerCore)

	artifactRoot := t.TempDir()

	cfg := createTestConfig("redis://127.0.0.1:0")
	cfg.Logging.TraceCalculations = true
	cfg.Logging.Narrate.Enabled = true
	cfg.Logging.Narrate.SampleRate = 1.0
	cfg.Logging.ArtifactStore.Enabled = true
	cfg.Logging.ArtifactStore.RootPath = artifactRoot
	cfg.Market.YFinance.Enabled = false
	cfg.Macro.FREDEnabled = false
	cfg.Macro.ManualRiskFreeRate = 0.04
	cfg.Macro.ManualMarketRiskPremium = 0.05
	cfg.DataCleaner.RulesPath = filepath.Join(projectRoot, "config", "datacleaner", "rules.json")
	cfg.DataCleaner.IndustryRulesPath = filepath.Join(projectRoot, "config", "datacleaner", "industry")

	var (
		database       *sqlx.DB
		authService    *auth.Service
		valuationSvc   *valuation.Service
		rateLimiter    *ratelimit.RateLimiter
		healthHandler  *handlers.HealthHandler
		metricsService *metrics.Service
	)

	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Decorate(func() *zap.Logger { return obsLogger }),
		di.CoreModule,
		di.ServiceModule,
		di.HandlerModule,
		fx.Provide(handlers.NewFairValueHandler),
		fx.Populate(
			&database,
			&authService,
			&valuationSvc,
			&rateLimiter,
			&healthHandler,
			&metricsService,
		),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	SetupDatabase(t, database)
	SeedTestData(t, database)

	srv := api.NewServer(cfg, obsLogger, valuationSvc, authService, rateLimiter, healthHandler, metricsService)
	router := srv.Engine()

	ctx := context.Background()
	apiKey, err := authService.CreateKey(ctx, "narrate-off-test-user", []coreEntities.Permission{
		coreEntities.PermissionReadFairValue,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	// Note: NO ?trace=1 query string and NO X-Midas-Trace header.
	req, err := http.NewRequest(http.MethodGet, "/api/v1/fair-value/AAPL", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", apiKey.Key)

	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Narrate lines still flow because narrate.enabled = true. The bundle
	// did NOT open, so no payload_ref fields are populated.
	narrateEntries := filterByEvent(observedLogs.All(), "narrate")
	require.NotEmpty(t, narrateEntries, "narrate stream should still emit when trace flag absent")

	// The artifact root must contain ZERO entries — no date partition was
	// even created because OpenBundle was never called.
	entries, err := os.ReadDir(artifactRoot)
	require.NoError(t, err)
	assert.Empty(t, entries, "artifact root must remain empty when ?trace=1 is absent; got %v", entries)
}

// TestNarrate_OnErrorAutoBundle pins Phase 2.A end-to-end: with
// logging.artifact_store.triggers.on_error=true and a request that errors
// (HTTP status >=500), the bundle MUST land on disk WITHOUT a manual
// ?trace=1 flag, the manifest's trigger MUST be "on_error", and
// 99-narrate.jsonl MUST contain the full per-request narrate stream — both
// pre-trigger lines (request.received, classify.industry, etc., buffered in
// memory) and the response.sent line emitted post-promote.
//
// We force a 500 by returning an unknown ticker (TICKER_NOT_FOUND_AUTO),
// which goes through the same valuation orchestration path but lands on the
// 404 handler. To keep the test focused on the on-error trigger we instead
// mount a synthetic 5xx-erroring handler under a side-router after wiring
// the same trace middleware. This isolates the on_error trigger from
// fair-value handler quirks while still exercising the full middleware
// chain (trace -> security -> metrics -> recovery -> handler).
func TestNarrate_OnErrorAutoBundle(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	srcMultiples := filepath.Join(projectRoot, "config", "industry_multiples.json")
	require.FileExists(t, srcMultiples)
	require.NoError(t, os.MkdirAll("./config", 0o755))
	t.Cleanup(func() { _ = os.RemoveAll("./config") })
	multiplesBytes, err := os.ReadFile(srcMultiples)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile("./config/industry_multiples.json", multiplesBytes, 0o644))

	observerCore, observedLogs := observer.New(zapcore.InfoLevel)
	obsLogger := zap.New(observerCore)

	artifactRoot := t.TempDir()

	cfg := createTestConfig("redis://127.0.0.1:0")
	cfg.Logging.TraceCalculations = true
	cfg.Logging.Narrate.Enabled = true
	cfg.Logging.Narrate.SampleRate = 1.0
	cfg.Logging.ArtifactStore.Enabled = true
	cfg.Logging.ArtifactStore.RootPath = artifactRoot
	cfg.Logging.ArtifactStore.QueueSize = 256
	// Phase 2.A — auto-on-error trigger ENABLED. No manual flag will be sent.
	cfg.Logging.ArtifactStore.Triggers.OnError = true
	cfg.Market.YFinance.Enabled = false
	cfg.Macro.FREDEnabled = false
	cfg.Macro.ManualRiskFreeRate = 0.04
	cfg.Macro.ManualMarketRiskPremium = 0.05
	cfg.DataCleaner.RulesPath = filepath.Join(projectRoot, "config", "datacleaner", "rules.json")
	cfg.DataCleaner.IndustryRulesPath = filepath.Join(projectRoot, "config", "datacleaner", "industry")

	var (
		database       *sqlx.DB
		authService    *auth.Service
		valuationSvc   *valuation.Service
		rateLimiter    *ratelimit.RateLimiter
		healthHandler  *handlers.HealthHandler
		metricsService *metrics.Service
	)

	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Decorate(func() *zap.Logger { return obsLogger }),
		di.CoreModule,
		di.ServiceModule,
		di.HandlerModule,
		fx.Provide(handlers.NewFairValueHandler),
		fx.Populate(
			&database,
			&authService,
			&valuationSvc,
			&rateLimiter,
			&healthHandler,
			&metricsService,
		),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	SetupDatabase(t, database)
	SeedTestData(t, database)

	srv := api.NewServer(cfg, obsLogger, valuationSvc, authService, rateLimiter, healthHandler, metricsService)
	router := srv.Engine()

	ctx := context.Background()
	apiKey, err := authService.CreateKey(ctx, "narrate-onerror-user", []coreEntities.Permission{
		coreEntities.PermissionReadFairValue,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	// Request a non-existent ticker — the valuation pipeline emits the same
	// upstream narrate phases (request.received, auth.resolved, etc.), then
	// the handler responds with 404. To force a 500, we hit a path that
	// doesn't match any route. Gin's default 404 handler returns 404 not 500,
	// so we instead trigger a 500 by sending a malformed bulk body to the
	// existing fair-value bulk endpoint, which the handler 500s on if the
	// service errors. Simplest approach that doesn't require touching the
	// production handlers: hit a non-existent ticker that the handler maps
	// to 500 via a panic — but we don't want to test panics.
	//
	// Cleanest: ZZZ-NONEXISTENT triggers the valuation service's
	// ErrTickerNotFound which maps to 404, so that won't 500.
	// We force a 500 by overriding gin's NoMethod handler to abort with 500.
	router.NoRoute(func(c *gin.Context) {
		c.AbortWithStatus(http.StatusInternalServerError)
	})
	// NB: NO ?trace=1, NO X-Midas-Trace — pin the on_error auto-trigger.
	req, err := http.NewRequest(http.MethodGet, "/api/v1/this-route-does-not-exist", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", apiKey.Key)

	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())

	requestID := w.Header().Get("X-Request-ID")
	require.NotEmpty(t, requestID, "X-Request-ID must be present")

	// --- Assert (1): a bundle was created on disk under the on_error trigger.
	bundleDir := findBundleDir(t, artifactRoot, "_no-ticker", requestID)
	require.NotEmpty(t, bundleDir, "expected a bundle directory for request %s under %s after on_error auto-trigger", requestID, artifactRoot)

	// --- Assert (2): manifest.trigger == "on_error" (NOT "header"/"query").
	mfBody, err := os.ReadFile(filepath.Join(bundleDir, "00-manifest.json"))
	require.NoError(t, err)
	var m artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &m))
	assert.Equal(t, "on_error", m.Trigger,
		"auto-triggered bundle must carry trigger=on_error in its manifest")
	assert.Equal(t, requestID, m.RequestID)

	// --- Assert (3): the bundle's narrate stream (99-narrate.jsonl) contains
	// the full per-request story — both buffered (pre-trigger) lines like
	// request.received AND the post-promote line response.sent. A reader of
	// the bundle must be able to reconstruct the request's story without
	// having to grep the host log stream.
	narrateStream := filepath.Join(bundleDir, "99-narrate.jsonl")
	streamBody, err := os.ReadFile(narrateStream)
	require.NoError(t, err, "99-narrate.jsonl must exist in promoted deferred bundle")

	streamLines := strings.Split(strings.TrimSpace(string(streamBody)), "\n")
	require.GreaterOrEqual(t, len(streamLines), 2,
		"narrate stream must have at least request.received + response.sent; got %d lines", len(streamLines))

	// Parse each line and verify request_id correlation.
	var sawRequestReceived, sawResponseSent bool
	for i, line := range streamLines {
		var entry map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(line), &entry),
			"line %d not valid JSON: %s", i, line)
		assert.Equal(t, "narrate", entry["event"], "line %d missing event=narrate", i)
		assert.Equal(t, requestID, entry["request_id"], "line %d wrong request_id", i)
		switch entry["phase"] {
		case "request.received":
			sawRequestReceived = true
		case "response.sent":
			sawResponseSent = true
		}
	}
	assert.True(t, sawRequestReceived,
		"buffered request.received line must survive the deferred->promoted transition")
	assert.True(t, sawResponseSent,
		"post-promote response.sent line must land in the same stream file")

	// --- Assert (4): the host log stream still got the same narrate lines
	// (the BundleSink is a tee, not a replacement). request_id correlation
	// is the contract that lets log readers cross-reference both surfaces.
	narrateEntries := filterByEvent(observedLogs.All(), "narrate")
	require.NotEmpty(t, narrateEntries, "host log stream must still receive narrate lines")
	hostHasResponseSent := false
	for _, e := range narrateEntries {
		if fieldString(e, "phase") == "response.sent" && fieldString(e, "request_id") == requestID {
			hostHasResponseSent = true
			break
		}
	}
	assert.True(t, hostHasResponseSent,
		"host log stream must contain response.sent for the same request_id")
}

// filterByEvent returns the subset of log entries whose structured "event"
// field matches eventName. Used to slice narrate lines out of the mixed
// access-log + narrate stream.
func filterByEvent(entries []observer.LoggedEntry, eventName string) []observer.LoggedEntry {
	out := make([]observer.LoggedEntry, 0, len(entries))
	for _, e := range entries {
		if fieldString(e, "event") == eventName {
			out = append(out, e)
		}
	}
	return out
}

// findBundleDir walks the artifact root looking for the per-request bundle
// directory. The path layout is <root>/<UTC-date>/<TICKER-OR-_no-ticker>/req_<safe-id>/.
// Returns the first directory whose name matches "req_" + sanitized requestID.
//
// Note: trace middleware opens the bundle BEFORE the handler parses the URL
// ticker, so the directory is initially created under "_no-ticker/". The
// handler then calls SetTicker, which renames the directory to <TICKER>/. If
// a request fails before reaching the handler (auth, ratelimit, malformed
// URL), the directory stays at "_no-ticker/". The ticker argument is ignored
// for the directory lookup since this helper just walks for "req_<id>"; the
// caller may want to verify the parent dir name separately.
func findBundleDir(t *testing.T, root, _ /* ticker */, requestID string) string {
	t.Helper()
	want := "req_" + sanitiseID(requestID)
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && info.Name() == want {
			found = path
		}
		return nil
	})
	require.NoError(t, err)
	return found
}

// sanitiseID mirrors the package-internal safeRequestID() so the test can
// predict the on-disk directory name from the X-Request-ID header value.
// Kept narrowly scoped to UUID-like inputs (only colon/slash etc are
// replaced; UUID hyphens stay).
func sanitiseID(id string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return r.Replace(id)
}
