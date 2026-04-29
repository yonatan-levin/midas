package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jmoiron/sqlx"
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

func TestProbe_FlagCount(t *testing.T) {
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
	cfg := createTestConfig("redis://127.0.0.1:0")
	cfg.Logging.TraceCalculations = true
	cfg.Logging.Narrate.Enabled = true
	cfg.Logging.Narrate.SampleRate = 1.0
	cfg.Logging.ArtifactStore.Enabled = true
	cfg.Logging.ArtifactStore.RootPath = t.TempDir()
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
		di.CoreModule, di.ServiceModule, di.HandlerModule,
		fx.Provide(handlers.NewFairValueHandler),
		fx.Populate(&database, &authService, &valuationSvc, &rateLimiter, &healthHandler, &metricsService),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	SetupDatabase(t, database)
	SeedTestData(t, database)

	srv := api.NewServer(cfg, obsLogger, valuationSvc, authService, rateLimiter, healthHandler, metricsService)
	router := srv.Engine()
	ctx := context.Background()
	apiKey, err := authService.CreateKey(ctx, "probe", []coreEntities.Permission{coreEntities.PermissionReadFairValue})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/fair-value/AAPL?trace=1", nil)
	req.Header.Set("X-API-Key", apiKey.Key)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	for _, e := range observedLogs.All() {
		if e.Message == "narrate" {
			fields := e.ContextMap()
			if phase, _ := fields["phase"].(string); phase == "clean.normalized" {
				t.Logf("clean.normalized fields: %+v", fields)
			}
		}
	}
}
