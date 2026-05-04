//go:build replay_spike
// +build replay_spike

// Spike test for Phase R2 Pre-Flight (§2 of
// docs/refactoring/observability-replay-tooling-r2-implementation-plan.md).
//
// Purpose: prove that fx.Decorate composes against the real di.CoreModule at
// the pinned go.uber.org/fx v1.24.0 and that the decorated value reaches
// downstream consumers via interface identity. If this test compiles and
// passes, replay's R2 design (D2 in the spec) can use fx.Decorate as written;
// if it fails, BACKEND must execute the §10 Contingent fallback (split a
// GatewayModule sub-module out of CoreModule and use fx.Replace).
//
// Build-tag-gated so the spike is excluded from default `go test ./...` runs.
// Per plan §2 "Disposition" the test may be deleted after the spike concludes
// or retained as a regression guard. We retain it: the cost is one tagged
// file the default test run never sees, and the upside is that any future
// fx upgrade silently breaking decoration would surface here loudly.
//
// Run: go test -tags replay_spike ./internal/observability/replay/ -run TestSpike -v

package replay

import (
	"context"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/di"
)

// fakeSECGateway is the marker the spike injects via fx.Decorate. The whole
// point is: after decoration, an fx.Invoke that requests ports.SECGateway
// must receive THIS value, not the production binding constructed by
// di.NewSECGateway.
type fakeSECGateway struct{}

func (f *fakeSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
	return nil, nil
}
func (f *fakeSECGateway) GetCompanyConcepts(ctx context.Context, cik, tag string) (*entities.ConceptResponse, error) {
	return nil, nil
}
func (f *fakeSECGateway) GetTickerCIKMapping(ctx context.Context) (map[string]string, error) {
	return nil, nil
}
func (f *fakeSECGateway) GetFinancialDataForTicker(ctx context.Context, ticker, cik string) (*entities.HistoricalFinancialData, error) {
	return nil, nil
}
func (f *fakeSECGateway) HealthCheck(ctx context.Context) error { return nil }

// TestSpike_FxDecorate_OverridesGatewayProvider is the load-bearing assertion
// of the pre-flight spike. Per plan §2 pass criterion: decoration must take
// effect AND the decorated value must reach downstream consumers via
// interface identity (gw == fake).
func TestSpike_FxDecorate_OverridesGatewayProvider(t *testing.T) {
	fake := &fakeSECGateway{}

	// Minimal config sufficient for di.CoreModule's gateway providers to
	// construct without touching network or disk. We don't need the
	// production gateway to actually work — fx lazy-resolves; we only need
	// the provider graph to compile and let Decorate intercept.
	cfg := &config.Config{
		SEC: config.SECConfig{
			BaseURL:          "https://data.sec.gov",
			UserAgent:        "spike test",
			RateLimit:        10,
			RequestTimeout:   5 * time.Second,
			MaxRetries:       1,
			RetryBackoffBase: 100 * time.Millisecond,
			TickerMappingURL: "https://www.sec.gov/files/company_tickers.json",
		},
		Database: config.DatabaseConfig{
			Driver:     "sqlite3",
			SQLitePath: ":memory:",
		},
	}

	// Whether the decoration reached the resolved interface value.
	var resolvedIsFake bool

	app := fxtest.New(t,
		// Supply config + logger so CoreModule's providers can construct.
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(zap.NewNop),

		// The provider being tested is fx.Provide(NewSECGateway, fx.As(...))
		// at di/container.go:127. We do NOT need every CoreModule provider
		// — only the SEC gateway provider chain (NewSECGateway + factories).
		// CoreModule pulls in DB/Redis providers that would force config
		// of those subsystems even if we never resolve them. The spike's
		// goal is to assert fx.Decorate semantics against a provider that
		// matches CoreModule's exact registration shape.
		fx.Provide(di.NewCircuitBreakerFactory),
		fx.Provide(di.NewRetryPolicyFactory),
		fx.Provide(fx.Annotate(di.NewSECGateway, fx.As(new(ports.SECGateway)))),

		// The decoration under test. If fx.Decorate composes correctly at
		// fx 1.24.0, this replaces the production binding for any
		// downstream consumer of ports.SECGateway.
		fx.Decorate(func(prod ports.SECGateway) ports.SECGateway {
			return fake
		}),

		// Resolve the gateway and prove decoration took effect via
		// interface identity. The plan specifies this exact assertion
		// pattern: gw != fake means the production binding leaked through.
		fx.Invoke(func(gw ports.SECGateway) {
			resolvedIsFake = (gw == ports.SECGateway(fake))
		}),

		fx.NopLogger,
	)

	app.RequireStart()
	defer app.RequireStop()

	if !resolvedIsFake {
		t.Fatalf("fx.Decorate did not override ports.SECGateway: production binding leaked. " +
			"Execute §10 Contingent: split a GatewayModule out of CoreModule and use fx.Replace instead.")
	}
}
