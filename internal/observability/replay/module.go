package replay

import (
	"os"
	"path/filepath"
	"runtime"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	configfs "github.com/midas/dcf-valuation-api/config"
	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	aiSvc "github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
	"github.com/midas/dcf-valuation-api/internal/services/datafetcher"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// Module returns the fx Option that wires a replay-mode valuation service.
//
// Design note (deviation from plan §3 Stage C composition recipe):
//
// The plan suggests `fx.Options(di.CoreModule, di.ServiceModule, decoratesAbove)`
// using fx.Decorate over the production providers. The pre-flight spike
// (commit 2c4b60c) confirmed `fx.Decorate` composes correctly at fx 1.24.0,
// so that path is technically viable. We deviate to a hand-picked module
// here because `di.CoreModule` provides `*sqlx.DB` (NewDatabase) and
// `*redis.Client` (NewRedisClient) — both side-effecting constructors. Even
// when downstream repositories are decorated away, fx's lazy construction
// only avoids invoking the originals when NOTHING in the app graph asks
// for the concrete types. `di.HandlerModule` and various repos still
// transitively depend on `*sqlx.DB`, so naively pulling in CoreModule
// would force a sqlite handle to open even though no replay code reads
// from it. F11 ("hermeticity: replay must not touch external systems")
// is cleaner with the hand-picked composition: every provider listed
// below is either compute-pure (config/logger/clock) or replay-owned
// (bundle gateways, NotFound repos, no-op metrics).
//
// Keeping this composition tight has a maintenance cost: when a new
// production dependency is added to *valuation.Service's constructor
// signature, this file must be updated. That tradeoff favors hermeticity
// over wide reuse — replay's blast radius is intentionally small.
//
// fx.Decorate IS still used for one composition: the valuation.Clock
// override that binds the engine's clock to manifest.started_at. That
// decorator runs OVER the production wallClock provided by valuation.NewWallClock,
// because the spike proved the decoration semantics work and Stage F's
// cross-year regression test relies on a SECOND fx.Decorate(Clock)
// layered on top of this one (last-Decorate-wins).
//
// Post-construct hook: *valuation.Service.SetYFinanceGateway is called
// in an fx.Invoke so the bundle YFinance gateway flows into the
// service's optional analyst-estimates surface. The plan calls this
// out as Critical Surface #2 — using fx.Decorate for the post-construct
// wiring would not work because SetYFinanceGateway mutates the service
// after construction, not at provider time.
func Module(bundleDir string, opts Options) fx.Option {
	return fx.Options(
		// --------------------------------------------------------------
		// Compute-pure providers — config, logger, calclog emitter.
		// These are forwarded through fx so the production constructors
		// (NewDataCleanerService, NewValuationService, etc.) receive the
		// same shape they receive in production wiring.
		// --------------------------------------------------------------
		fx.Provide(func() (*config.Config, error) { return replayConfig(bundleDir) }),
		fx.Provide(replayLogger),
		fx.Provide(calclog.NewEmitter),

		// --------------------------------------------------------------
		// Bundle gateways — replace production gateways. Each provider
		// is niladic (no fx-resolved deps) so fx's lazy graph walker
		// will not transitively pull in DB/Redis providers.
		// --------------------------------------------------------------
		fx.Provide(func(logger *zap.Logger) ports.SECGateway {
			// opts.Ticker (manifest ticker) is threaded so the SEC gateway's
			// GetTickerCIKMapping returns {ticker: cik} for any bundle —
			// not just the prior hardcoded mega-cap list. VERIFIER MEDIUM-1.
			return NewBundleSECGateway(bundleDir, opts.Mode, opts.Ticker, logger)
		}),
		fx.Provide(func() ports.MarketDataGateway {
			return NewBundleMarketGateway(bundleDir, opts.Mode)
		}),
		fx.Provide(func(cfg *config.Config, logger *zap.Logger) ports.MacroDataGateway {
			// Threaded *config.Config so GetMarketRiskPremium reads
			// cfg.Macro.ManualMarketRiskPremium (matches production
			// gateway.go:140-157). VERIFIER finding HIGH-1.
			//
			// logger is threaded so the RPL-7 raw→parsed fallback WARN
			// line (tracker docs/reviewer/RPL7-raw-mode-macro-per-series-snapshot.md)
			// reaches the operator. Grep with
			// `rg '"phase":"RPL-7-raw-fallback"'`.
			return NewBundleMacroGateway(bundleDir, opts.Mode, cfg, logger)
		}),
		// YFinance is wired post-construct via the fx.Invoke at the bottom;
		// expose it here so the post-construct hook can resolve it.
		fx.Provide(func() ports.YFinanceGateway {
			return NewBundleYFinanceGateway(bundleDir, opts.Mode)
		}),

		// Layer B Phase 2: bundle-backed guidance source. Injected into
		// replayValuationService (below) which calls SetGuidanceSource so the
		// engine reads the captured 09-guidance.json instead of the live fixture
		// directory (NF3). Absent stage ⇒ absent path (old bundles unaffected).
		fx.Provide(func() *BundleGuidanceGateway {
			return NewBundleGuidanceGateway(bundleDir)
		}),

		// --------------------------------------------------------------
		// NotFound repos. Replace production sqlite/cache repos. All
		// niladic — no DB or Redis side effects.
		// --------------------------------------------------------------
		fx.Provide(NewNotFoundFinancialDataRepo),
		fx.Provide(NewNotFoundMarketDataRepo),
		fx.Provide(NewNotFoundMacroDataRepo),
		fx.Provide(NewNotFoundCacheRepo),
		fx.Provide(NewNotFoundTickerMappingRepo),
		fx.Provide(NewPanicWatchlistRepo),

		// --------------------------------------------------------------
		// Auth: replay does not consume auth.Repository, but the production
		// constructor wires *auth.Service via DI. We provide a panic stub
		// only as a sentinel — if a future engine path consults auth.Service
		// we want the panic to surface immediately. handlers.AuthKeyManager
		// is not needed because replay does not construct HTTP handlers.
		// --------------------------------------------------------------
		// (deliberately omitted — *valuation.Service's constructor does not
		// take auth.Service; we never call out to handlers in replay)

		// --------------------------------------------------------------
		// Metrics: no-op service, both as concrete *metrics.Service AND
		// as the ports.MetricsService interface (the production code at
		// container.go:148-153 uses this two-step binding so any consumer
		// expecting either shape resolves correctly).
		// --------------------------------------------------------------
		fx.Provide(replayMetricsService),
		fx.Provide(func(s *metrics.Service) ports.MetricsService { return s }),

		// --------------------------------------------------------------
		// Production constructors that have no I/O dependencies — pulled
		// from di package so any change in their signatures shows up here
		// at compile time.
		// --------------------------------------------------------------
		fx.Provide(replayAIService),
		fx.Provide(replayDataCleanerService),
		fx.Provide(datafetcher.NewDataFetcher),

		// --------------------------------------------------------------
		// Clock provider: bound directly to manifest.started_at via a
		// constructor closure. We DO NOT use fx.Decorate here because
		// fx 1.24.0 disallows multiple decorators on the same type
		// within one composition — and Stage F's
		// TestReplay_CrossYearProducesByteIdenticalOutput needs to layer
		// a SECOND fx.Decorate(Clock) on top of replay.Module to inject
		// a fixture clock for cross-year regression. Decorating once at
		// the module level would block that layering.
		//
		// By exposing Clock as a plain fx.Provide, the test (or any
		// future caller) can attach exactly one fx.Decorate(Clock) on
		// top to override the manifest binding.
		// --------------------------------------------------------------
		fx.Provide(func() valuation.Clock {
			return newManifestClock(opts.ManifestStartedAt)
		}),

		// --------------------------------------------------------------
		// Tier 2 P0b: AssumptionProfile registry. Replay loads the SAME
		// production config — embedded via configfs so the replay binary
		// stays hermetic against cwd. The registry is hermetic by
		// construction (no I/O after load, no time.Now()), so re-resolution
		// against the captured Facts is deterministic. Future P1+ work may
		// swap this for a bundle-snapshot-aware provider that short-circuits
		// to the captured AssumptionProfileManifest's ResolvedSnapshot, but
		// P0b keeps replay and production on the same code path —
		// schema_drift surfaces any config change as a manifest hash
		// mismatch.
		// --------------------------------------------------------------
		fx.Provide(func() (profile.Registry, error) {
			raw, err := configfs.Read("assumption_profiles.json")
			if err != nil {
				return nil, err
			}
			return profile.LoadFromBytes(raw, "assumption_profiles.json:embed")
		}),

		// --------------------------------------------------------------
		// *valuation.Service is the engine entry point. Reuse the production
		// constructor signature so any engine-side change in dependencies
		// surfaces here at compile time. The constructor expects all the
		// providers above and produces the *Service we'll resolve via Replay().
		// --------------------------------------------------------------
		fx.Provide(replayValuationService),

		// --------------------------------------------------------------
		// Bind *valuation.Service to handlers.ValuationCalculator
		// interface — though replay does not construct handlers, the
		// binding is a future-proofing hook so a R3 handler-as-replay-driver
		// can resolve it without re-wiring.
		// --------------------------------------------------------------
		fx.Provide(func(s *valuation.Service) handlers.ValuationCalculator { return s }),

		// --------------------------------------------------------------
		// Post-construct hook: wire YFinanceGateway. Cannot be a Decorate
		// because Service mutates state via SetYFinanceGateway after the
		// constructor returns; fx.Decorate runs at provider time.
		// --------------------------------------------------------------
		fx.Invoke(func(svc *valuation.Service, yfin ports.YFinanceGateway) {
			svc.SetYFinanceGateway(yfin)
		}),

		fx.NopLogger,
	)
}

// replayConfig builds a *config.Config sufficient to drive the production
// *valuation.Service constructor without any external I/O. It has two layers:
//
//  1. BASE: the hand-mirrored production viper defaults from setDefaults() at
//     internal/config/config.go (the RPL-10 fallback, below). The datacleaner
//     reads cfg.DataCleaner.* heavily; the valuation engine reads Valuation.*.
//  2. OVERLAY: if the bundle carries a 00-config.json snapshot (1.2+ bundles,
//     RPL-9), its captured Valuation + Macro fields override the base. For
//     these bundles the snapshot — not the hand-mirror — is the source of truth.
//
// For pre-1.2 bundles (no snapshot file) the base is returned unchanged, so the
// hand-mirror remains the fallback. A present-but-corrupt snapshot fails loudly
// (returns an error) — consistent with replay's strict schema/hash drift
// philosophy: a malformed bundle artifact must not be silently ignored.
//
// SEC / Market / Macro config fields are unused because we supply bundle
// gateways that ignore them; the Macro manual-rate fields ARE overlaid because
// BundleMacroGateway reads cfg.Macro.ManualMarketRiskPremium.
func replayConfig(bundleDir string) (*config.Config, error) {
	base := &config.Config{
		Valuation: config.ValuationConfig{
			// FALLBACK MIRROR (pre-1.2 bundles only). RPL-9 has now
			// LANDED: for 1.2+ bundles the bundle's 00-config.json is the
			// source of truth and the overlay below (after this literal)
			// overrides these defaults. This hand-mirror remains the BASE
			// before the overlay AND the sole config for pre-1.2 bundles
			// that lack the snapshot file.
			//
			// RPL-10 (2026-05-22) fallback-parity discipline still applies
			// to the pre-1.2 path: mirror ALL non-zero production viper
			// defaults from internal/config/config.go:setDefaults() — not
			// just the few currently consumed by replay-reachable code
			// paths. Defense-in-depth: cycles 1+2+3 of the replay-fidelity
			// debug each fixed one instance of "replay-side config field
			// hand-copied wrong from production default" (DCFMaxGrowthRate,
			// DCFMinGrowthRate, DefaultTerminalGrowthCap). For a 1.2+ bundle
			// captured against a non-default production config the snapshot
			// now carries that config faithfully, so such a bundle replays
			// correctly without touching this mirror. For an OLD (pre-1.2)
			// bundle the mirror is all there is, so it must still match
			// production defaults — pinned by
			// TestReplayConfig_MirrorsAllValuationViperDefaults, which fails
			// the moment someone adds a new viper default without mirroring
			// it here.
			//
			// Why the growth caps matter (historical context, cycle 2 / MXL
			// 2026-05-13): they feed *growth.Estimator.MaxGrowthRate /
			// MinGrowthRate via valuation.NewService:88-93 and are consulted
			// in service.go:569 as the terminal-growth fallback when
			// historical CAGR computation errors out. Any divergence
			// silently clips/floors the blended Stage 1 growth rate when
			// |blended| > cap (cascading through the Stage 2 fade and
			// corrupting every projected rate, the `growth_rate` summary,
			// DCF value, etc.), OR substitutes a different terminal-growth
			// rate when the historical fallback fires (sparse / all-negative
			// OI). The prior 0.40 / -0.10 values clipped MXL's blended 0.516
			// down to 0.40 — a ~0.10 drop on every stage and 9 drift fields
			// against a production-captured 17-response.json (which used the
			// 0.50 cap). Regression-pinned by
			// TestReplayFidelity_MXLClassFixture_ZeroDiffs in
			// integration_test.go.
			//
			// Field order below matches config.ValuationConfig struct
			// declaration order at internal/config/config.go:263-286.
			DefaultMarketRiskPremium: 0.05,             // config.go:493
			DefaultTerminalGrowthCap: 0.03,             // config.go:494
			DefaultTaxRate:           0.21,             // config.go:495
			MinDataPointsForGrowth:   2,                // config.go:496
			MaxBulkSize:              50,               // config.go:497
			CacheTTL:                 time.Hour,        // config.go:498
			SlowRequestThreshold:     5 * time.Second,  // config.go:499
			DataFetchTimeout:         10 * time.Second, // config.go:500
			// EnableConcurrentDataFetch has no viper.SetDefault in
			// setDefaults(), so its production default is the zero
			// value (false). Mirroring as false would be redundant but
			// harmless; we elide for clarity that this is the un-set
			// zero default.
			DCFProjectionYears:    5,      // config.go:503
			DCFMaxGrowthRate:      0.50,   // config.go:504
			DCFMinGrowthRate:      -0.30,  // config.go:505
			DCFIterationTolerance: 0.0001, // config.go:506
			DCFMaxIterations:      100,    // config.go:507
		},
		// Mirror viper defaults at config.go:489-490
		// (manual_risk_free_rate=0.045, manual_market_risk_premium=0.05).
		// ManualMarketRiskPremium is threaded into BundleMacroGateway so
		// replay's MRP matches production for bundles captured against
		// the default config (VERIFIER finding HIGH-1, debug cycle 2).
		// ManualRiskFreeRate is NOT consumed by replay-reachable paths
		// today (replay uses the bundle's macro snapshot for the risk-
		// free rate), but RPL-10 mirrors it as defense-in-depth and the
		// parity test guards against future drift.
		Macro: config.MacroConfig{
			ManualRiskFreeRate:      0.045,
			ManualMarketRiskPremium: 0.05,
		},
		DataCleaner: config.DataCleanerConfig{
			// Mirror viper defaults at config.go:510-529. The rules /
			// industry / schema paths point at on-disk JSON files in the
			// repo. Replay's working directory at test time is the
			// internal/observability/replay/ dir, so the config-relative
			// path "./config/datacleaner/rules.json" needs to resolve
			// relative to repo root. We avoid a working-directory
			// dependency by computing an absolute path in the same way
			// internal/config/config.go's defaults do — see
			// resolveDataCleanerConfigPath below.
			Enabled:             true,
			RulesPath:           resolveDataCleanerConfigPath("rules.json"),
			IndustryRulesPath:   resolveDataCleanerConfigPath("industry"),
			SchemaPath:          resolveDataCleanerConfigPath("schema.json"),
			EnableAIIntegration: false,
			MinQualityScore:     60.0,
			HighQualityScore:    85.0,
			EnableRiskFlags:     true,
			CriticalThreshold:   0.3,
			WarningThreshold:    0.15,
			MaxConcurrentRules:  10,
			EnableCaching:       true,
			CacheTTL:            6 * time.Hour,
			EnableIndustryRules: true,
			EnableAuditTrail:    true,
			LogAdjustments:      true,
			LogFlags:            true,
		},
	}

	// RPL-9 overlay: if the bundle carries a 00-config.json snapshot
	// (1.2+ bundles), its captured config is the source of truth and
	// overrides the hand-mirrored base above. A present-but-corrupt
	// snapshot fails loudly — a malformed bundle artifact must not be
	// silently ignored (consistent with replay's strict schema/hash drift
	// philosophy). An absent snapshot (pre-1.2 bundle) leaves the base
	// untouched.
	snap, found, err := artifact.ReadConfigSnapshot(bundleDir)
	if err != nil {
		return nil, err
	}
	// Keep this overlay in sync with the fields on artifact.ConfigSnapshot
	// (internal/observability/artifact/config_snapshot.go). A field captured
	// there but NOT overlaid here is a silent partial-overlay drift —
	// TestReplayConfig_BundleSnapshotOverridesHandMirror asserts all 11 fields
	// precisely to catch that.
	if found {
		base.Valuation.DefaultMarketRiskPremium = snap.Valuation.DefaultMarketRiskPremium
		base.Valuation.DefaultTerminalGrowthCap = snap.Valuation.DefaultTerminalGrowthCap
		base.Valuation.DefaultTaxRate = snap.Valuation.DefaultTaxRate
		base.Valuation.MinDataPointsForGrowth = snap.Valuation.MinDataPointsForGrowth
		base.Valuation.DCFProjectionYears = snap.Valuation.DCFProjectionYears
		base.Valuation.DCFMaxGrowthRate = snap.Valuation.DCFMaxGrowthRate
		base.Valuation.DCFMinGrowthRate = snap.Valuation.DCFMinGrowthRate
		base.Valuation.DCFIterationTolerance = snap.Valuation.DCFIterationTolerance
		base.Valuation.DCFMaxIterations = snap.Valuation.DCFMaxIterations
		base.Macro.ManualRiskFreeRate = snap.Macro.ManualRiskFreeRate
		base.Macro.ManualMarketRiskPremium = snap.Macro.ManualMarketRiskPremium
	}

	return base, nil
}

// resolveDataCleanerConfigPath finds the repo's config/datacleaner/<name>
// file regardless of where the binary is invoked from. The strategy:
// walk up from the package's source file until the first ancestor
// containing go.mod (which marks the repo root). Falls back to the
// relative path when the walk fails — replay tests then surface a
// clear loader error rather than silently mis-loading.
//
// RPL-2j (R3 Stage O.8): the prior fixed-depth walk (4 parents) was
// brittle if module.go ever moved. Anchoring on go.mod is robust
// against directory-depth drift.
func resolveDataCleanerConfigPath(name string) string {
	// Production callers run from repo root, so the relative path works.
	// Tests run from internal/observability/replay/ and need the absolute
	// path; we resolve it via runtime.Caller.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "./config/datacleaner/" + name
	}
	// Walk up from this file until we find a directory containing go.mod.
	// Cap the search at 16 ancestors to avoid an unbounded walk on a
	// pathological filesystem.
	dir := filepath.Dir(thisFile)
	// Go 1.22+ integer range form. RPL-3h (R3b cleanup).
	for range 16 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "config", "datacleaner", name)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return "./config/datacleaner/" + name
}

// replayLogger returns a zap.NewNop logger so replay never emits log lines
// to stdout/stderr. Production binds zap.Logger via NewLogger which sets
// up file rotation + JSON encoding — none of which is appropriate during
// a deterministic replay.
//
// Returning a Nop logger does NOT silence the request-scoped logger that
// production gateways use via logctx.Or(ctx, ...): replay does not inject
// a request-scoped logger via logctx, so logctx.Or falls through to this
// Nop logger, which is what we want.
func replayLogger() *zap.Logger {
	return zap.NewNop()
}

// replayMetricsService bridges the no-op metrics into the *metrics.Service
// concrete type the production constructor expects. Returns the *typed*
// concrete because the production binding `func(s *metrics.Service)
// ports.MetricsService { return s }` requires the concrete type.
//
// Trade-off: the production *metrics.Service constructor (NewMetricsService)
// builds Prometheus collectors. We don't want that side effect in replay
// — but replay's *valuation.Service constructor takes *metrics.Service
// (concrete), not the interface. We pass the production *metrics.Service
// here because (a) it's stateless w.r.t. external systems (Prometheus
// collectors live in-process; no /metrics endpoint is served by replay),
// (b) the engine consults a few Record* methods which are safe to invoke
// even without an HTTP scrape endpoint. The stub `noOpMetricsService`
// satisfies the interface but NOT the concrete *metrics.Service type;
// using the production constructor here is the simplest path that
// preserves source-compatibility with the production engine.
//
// RPL-2g (R3 Stage O.5): under --workers > 1 the replay binary runs N
// concurrent Replay() invocations, each of which constructs its own
// *metrics.Service via this provider. Stage I.0's audit confirmed that
// metrics.NewService allocates a FRESH per-service *prometheus.Registry
// (NOT prometheus.DefaultRegisterer) — see internal/services/metrics/
// service.go:107-109. Each parallel worker therefore gets its own
// registry; there is no shared state and no race on collector
// registration. The defense-in-depth lint at scripts/lint-prometheus-
// registers.{sh,ps1} (Stage I.0) prevents future regressions that
// would reintroduce DefaultRegisterer use.
func replayMetricsService(logger *zap.Logger) *metrics.Service {
	return metrics.NewService(logger)
}

// replayAIService returns a nil-safe AI service — datacleaner consumes it
// as an interface and skips AI integration when nil-implementations'
// methods do nothing. Production wires aiSvc.BuildAIServiceWithLogger;
// the same builder works here with a zero config.
func replayAIService(cfg *config.Config, logger *zap.Logger) aiSvc.AIService {
	return aiSvc.BuildAIServiceWithLogger(&cfg.DataCleaner, logger)
}

// replayDataCleanerService constructs the production datacleaner with
// the same arguments di.NewDataCleanerService does. Re-exported here
// instead of importing di to avoid pulling di's transitive sqlite/Redis
// imports into the replay package.
//
// TDB-4: the adjustment counter (WithAdjustmentMetrics) is deliberately NOT
// wired here. Replay never scrapes /metrics, so injecting a recorder would add
// graph surface for zero observable benefit; leaving it nil keeps the replay
// composition minimal and the hermeticity argument trivially obvious (the
// audit log still no-ops because replay injects no request-scoped logger via
// logctx).
func replayDataCleanerService(cfg *config.Config, logger *zap.Logger, ai aiSvc.AIService, calc *calclog.Emitter) (datacleaner.DataCleanerService, error) {
	return datacleaner.NewDataCleanerService(cfg, ai, calc)
}

// replayValuationService constructs *valuation.Service with the production
// constructor and applies the Clock injection. Mirrors di.NewValuationService
// at internal/di/container.go:630 but skips the auth-key handler binding
// (replay never constructs handlers).
func replayValuationService(
	financialRepo ports.FinancialDataRepository,
	marketRepo ports.MarketDataRepository,
	macroRepo ports.MacroDataRepository,
	cache ports.CacheRepository,
	dataCleaner datacleaner.DataCleanerService,
	dataFetcher *datafetcher.DataFetcher,
	marketGateway ports.MarketDataGateway,
	macroGateway ports.MacroDataGateway,
	metricsService *metrics.Service,
	cfg *config.Config,
	logger *zap.Logger,
	calcEmitter *calclog.Emitter,
	clock valuation.Clock,
	profileRegistry profile.Registry,
	guidanceGateway *BundleGuidanceGateway,
) *valuation.Service {
	svc := valuation.NewService(
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
		profileRegistry,
	)
	svc.SetClock(clock)
	// RPL-2k (R3 Stage O.9): macroGateway comes through fx.Provide which
	// never produces nil. The defensive check was dead code.
	svc.SetMacroGateway(macroGateway)
	// Layer B Phase 2: replace the production guidance loader (which would scan
	// the live fixture directory) with a bundle-backed source reading the
	// captured 09-guidance.json (NF3 hermeticity). An old bundle without the
	// stage resolves to Absent ⇒ the absent path ⇒ bit-for-bit with the
	// original valuation.
	svc.SetGuidanceSource(guidanceGateway)
	// YFinanceGateway is wired in the fx.Invoke hook after this constructor
	// returns; doing it here would require an extra parameter the production
	// constructor doesn't have, so we keep the post-construct hook for
	// consistency with the di package wiring.
	//
	// marketGateway is consumed transitively by other fx providers in
	// replay.Module (the datafetcher coordinator routes market reads
	// through ports.MarketDataGateway). The underscore here is the
	// explicit "intentionally unused at THIS site" marker so a future
	// maintainer doesn't delete the parameter and break the fx dependency
	// graph downstream. RPL-3l (R3b cleanup).
	_ = marketGateway
	return svc
}

// _ = time.Now — placeholder so the time import (used by replayClock and
// its callers in this package) is never accidentally pruned by goimports
// when the rest of the file's references vary. RPL-2c (R3 Stage O.1)
// removed the sibling authsvc.NewService sentinel — the auth package was
// never wired from this module.
var _ = time.Now
