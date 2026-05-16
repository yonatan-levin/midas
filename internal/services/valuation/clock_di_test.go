package valuation

import (
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/midas/dcf-valuation-api/internal/services/datafetcher"
)

// TestService_SetClock_OverridesDefault is the post-construction injection
// path that the DI container calls into. Pins that SetClock is honored —
// without this, the dead-binding bug (Clock provider with no consumer)
// would re-emerge silently.
func TestService_SetClock_OverridesDefault(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	t0 := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)
	service.SetClock(fixedClock{t: t0})

	if got := service.clock.Now(); !got.Equal(t0) {
		t.Fatalf("after SetClock(fixedClock{t0}), clock.Now() = %v; want %v", got, t0)
	}
}

// TestService_SetClock_NilIsNoOp pins the defensive contract: passing nil
// must not nil-out s.clock (which would panic the next time s.clock.Now()
// is called).
func TestService_SetClock_NilIsNoOp(t *testing.T) {
	service, _, _, _, _, _ := createTestService()

	// Set a known clock, then attempt to clear it with nil.
	t0 := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	service.SetClock(fixedClock{t: t0})

	service.SetClock(nil)

	// Clock should still be the fixed one — nil was silently ignored.
	if got := service.clock.Now(); !got.Equal(t0) {
		t.Fatalf("SetClock(nil) overwrote the previously-set clock; clock.Now() = %v, want %v", got, t0)
	}
}

// newServiceForFXDecorateTest mirrors di.NewValuationService's signature
// in a minimal, self-contained form so this test can drive an fx.Decorate
// flow without importing internal/di (which would create a circular
// import). The behavior under test is identical: the constructor must
// accept a Clock parameter and call svc.SetClock(clock).
//
// If the production constructor regresses (loses its Clock parameter),
// fix BOTH this helper and di.NewValuationService.
func newServiceForFXDecorateTest(
	financialRepo ports.FinancialDataRepository,
	marketRepo ports.MarketDataRepository,
	macroRepo ports.MacroDataRepository,
	cache ports.CacheRepository,
	dataCleaner datacleaner.DataCleanerService,
	dataFetcher *datafetcher.DataFetcher,
	metricsService ports.MetricsService,
	cfg *config.Config,
	logger *zap.Logger,
	calcEmitter *calclog.Emitter,
	clock Clock,
) *Service {
	svc := NewService(
		financialRepo,
		marketRepo,
		macroRepo,
		cache,
		dataCleaner,
		dataFetcher,
		metricsService,
		cfg,
		logger,
		calcEmitter,
		nil, // profileRegistry — Tier 2 P0b; clock-DI tests don't exercise the profile path.
	)
	svc.SetClock(clock)
	return svc
}

// TestService_FXDecorateFlowsIntoClock pins dispatch item #1: an
// fx.Decorate over valuation.Clock must flow into *Service.clock so
// Phase R2's replay binary can override the seam at the fx layer rather
// than reaching into the constructed service. Before the fix, the
// fx.Provide(NewWallClock) provider had no consumer (NewValuationService
// didn't take a Clock parameter); a Decorate over it would silently no-op.
//
// This test is the fx-layer integration check. Unit-level coverage of
// SetClock is provided by TestService_SetClock_OverridesDefault above;
// this one drives the actual fx graph (Provide + Decorate + Populate)
// that R2 will use.
func TestService_FXDecorateFlowsIntoClock(t *testing.T) {
	t0 := time.Date(2030, 4, 15, 12, 0, 0, 0, time.UTC)

	var svc *Service

	app := fxtest.New(t,
		fx.Provide(
			func() ports.FinancialDataRepository { return &MockFinancialDataRepository{} },
			func() ports.MarketDataRepository { return &MockMarketDataRepository{} },
			func() ports.MacroDataRepository { return &MockMacroDataRepository{} },
			func() ports.CacheRepository { return &MockCacheRepository{} },
			func() datacleaner.DataCleanerService { return &MockDataCleanerService{} },
			func() *datafetcher.DataFetcher { return nil },
			func() ports.MetricsService { return &MockMetricsService{} },
			func() *config.Config {
				return &config.Config{
					Valuation: config.ValuationConfig{
						CacheTTL:             time.Hour,
						SlowRequestThreshold: 500 * time.Millisecond,
						DataFetchTimeout:     30 * time.Second,
					},
				}
			},
			func() *zap.Logger { return zap.NewNop() },
			func() *calclog.Emitter { return nil },
			NewWallClock,                // production Clock provider
			newServiceForFXDecorateTest, // constructor that consumes Clock
		),
		// Replay (R2) decorates Clock with a manifest-bound clock. Mirror
		// that pattern; if NewValuationService is regressed to drop the
		// Clock parameter, this Decorate becomes a silent no-op and svc.clock
		// is the production wallClock — the assertion below catches it.
		fx.Decorate(func(Clock) Clock {
			return fixedClock{t: t0}
		}),
		fx.Populate(&svc),
	)
	app.RequireStart()
	defer app.RequireStop()

	if svc == nil {
		t.Fatal("fx did not populate *valuation.Service")
	}

	if got := svc.clock.Now(); !got.Equal(t0) {
		t.Fatalf("fx.Decorate(Clock) did NOT flow into *Service.clock: clock.Now() = %v, want %v.\nLikely cause: NewValuationService regressed to drop its Clock parameter, so the Decorate layer has no consumer and is silently dropped.",
			got, t0)
	}
}
