package di

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	// Ensure sqlite drivers are registered in all build modes (including distroless containers)
	_ "github.com/mattn/go-sqlite3"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/macro"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/market"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/sec"
	"github.com/midas/dcf-valuation-api/internal/infra/repositories/cache"
	"github.com/midas/dcf-valuation-api/internal/infra/repositories/sqlite"
	"github.com/midas/dcf-valuation-api/internal/infra/resilience"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	aiSvc "github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
	"github.com/midas/dcf-valuation-api/internal/services/datafetcher"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/ratelimit"
	"github.com/midas/dcf-valuation-api/internal/services/scheduler"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
	"github.com/midas/dcf-valuation-api/internal/services/watchlist"
)

// RateLimiterCacheAdapter adapts ports.CacheRepository to ratelimit.CacheStore
type RateLimiterCacheAdapter struct {
	cache ports.CacheRepository
}

// Increment implements ratelimit.CacheStore.Increment
func (a *RateLimiterCacheAdapter) Increment(ctx context.Context, key string, window time.Duration) (int, time.Time, error) {
	// Get current value
	var currentCount int
	err := a.cache.Get(ctx, key, &currentCount)
	if err != nil {
		// Key doesn't exist, start with 0
		currentCount = 0
	}

	// Increment
	newCount := currentCount + 1
	resetTime := time.Now().Add(window)

	// Store updated count
	err = a.cache.Set(ctx, key, newCount, window)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to store incremented count: %w", err)
	}

	return newCount, resetTime, nil
}

// Get implements ratelimit.CacheStore.Get
func (a *RateLimiterCacheAdapter) Get(ctx context.Context, key string) (int, time.Time, error) {
	var count int
	err := a.cache.Get(ctx, key, &count)
	if err != nil {
		return 0, time.Time{}, ratelimit.ErrCacheKeyNotFound
	}

	// We can't determine exact reset time from the cache interface
	// Return a reasonable estimate
	resetTime := time.Now().Add(time.Minute)

	return count, resetTime, nil
}

// Set implements ratelimit.CacheStore.Set
func (a *RateLimiterCacheAdapter) Set(ctx context.Context, key string, value int, window time.Duration) error {
	return a.cache.Set(ctx, key, value, window)
}

// Delete implements ratelimit.CacheStore.Delete
func (a *RateLimiterCacheAdapter) Delete(ctx context.Context, key string) error {
	return a.cache.Delete(ctx, key)
}

// CoreModule contains core infrastructure providers (database, cache, gateways)
var CoreModule = fx.Options(
	// Logging Module
	fx.Provide(NewLogger),

	// Observability: calculation-stage emitter (consumed by services in Phase S)
	fx.Provide(calclog.NewEmitter),

	// Observability: artifact-bundle reaper. Started by RegisterHooks at app
	// startup, stopped at shutdown. Idle when ArtifactStore.Enabled=false.
	fx.Provide(NewArtifactReaper),

	// Database Module
	fx.Provide(NewDatabase),

	// Cache/Redis
	fx.Provide(NewRedisClient),

	// Resilience Factories
	fx.Provide(NewCircuitBreakerFactory),
	fx.Provide(NewRetryPolicyFactory),

	// Repositories
	fx.Provide(fx.Annotate(NewFinancialDataRepository, fx.As(new(ports.FinancialDataRepository)))),
	fx.Provide(fx.Annotate(NewMarketDataRepository, fx.As(new(ports.MarketDataRepository)))),
	fx.Provide(fx.Annotate(NewMacroDataRepository, fx.As(new(ports.MacroDataRepository)))),
	fx.Provide(fx.Annotate(NewTickerMappingRepository, fx.As(new(ports.TickerMappingRepository)))),
	fx.Provide(fx.Annotate(NewCacheRepository, fx.As(new(ports.CacheRepository)))),
	fx.Provide(fx.Annotate(NewAuthRepository, fx.As(new(auth.Repository)))),
	fx.Provide(fx.Annotate(NewWatchlistRepository, fx.As(new(ports.WatchlistRepository)))),

	// Gateways
	fx.Provide(fx.Annotate(NewSECGateway, fx.As(new(ports.SECGateway)))),
	fx.Provide(fx.Annotate(NewMarketDataGateway, fx.As(new(ports.MarketDataGateway)))),
	fx.Provide(fx.Annotate(NewMacroDataGateway, fx.As(new(ports.MacroDataGateway)))),
)

// ServiceModule contains business logic services
var ServiceModule = fx.Options(
	// Services
	fx.Provide(NewAuthService),
	fx.Provide(NewDataCleanerService),

	// Data fetcher service
	fx.Provide(NewDataFetcher),

	// Watchlist service for scheduler
	fx.Provide(NewWatchlistService),

	// Optional AI service provider (config-gated)
	fx.Provide(NewAIService),

	// Metrics Service - concrete type
	fx.Provide(NewMetricsService), // returns *metrics.Service

	// Bind concrete to interface without constructing anything new
	fx.Provide(
		func(s *metrics.Service) ports.MetricsService { return s },
	),

	fx.Provide(NewValuationService),
	fx.Provide(NewRateLimiterService),

	// Bind *valuation.Service to handlers.ValuationCalculator interface
	// so the FairValueHandler can receive it via DI.
	fx.Provide(
		func(s *valuation.Service) handlers.ValuationCalculator { return s },
	),

	// Bind *auth.Service to handlers.AuthKeyManager interface
	// so the AuthHandler can receive it via DI.
	fx.Provide(
		func(s *auth.Service) handlers.AuthKeyManager { return s },
	),

	// Scheduler service (disabled by default, uses watchlist)
	fx.Provide(NewSchedulerService),
)

// HandlerModule contains HTTP handlers
var HandlerModule = fx.Options(
	// Handler Module
	fx.Provide(NewHealthHandler),

	// Lifecycle hooks
	fx.Invoke(RegisterHooks),
)

// Module contains all dependency injection providers for the application
var Module = fx.Options(
	CoreModule,
	ServiceModule,
	HandlerModule,
)

// Container holds the dependency injection container
type Container struct {
	app *fx.App
}

// NewContainer creates a new dependency injection container
func NewContainer() *Container {
	app := fx.New(
		// Configuration Module (not included in Module as it's app-specific)
		fx.Provide(config.Load),

		// Include the shared Module with all providers
		Module,

		// Disable fx logs in production
		fx.NopLogger,
	)

	return &Container{app: app}
}

// Start starts the dependency injection container
func (c *Container) Start(ctx context.Context) error {
	return c.app.Start(ctx)
}

// Stop stops the dependency injection container
func (c *Container) Stop(ctx context.Context) error {
	return c.app.Stop(ctx)
}

// Dependency Providers

// NewLogger creates a structured logger using the LoggingConfig from the
// application configuration. It builds a zapcore.Tee that always writes to
// stdout and optionally writes to a rolling log file (lumberjack) when
// cfg.Logging.File.Enabled is true.
//
// Format decisions:
//   - "console" → coloured, human-readable output (development)
//   - "json"    → structured JSON output (staging / production)
//
// The file sink always uses JSON regardless of the stdout format so that
// log-processing pipelines can parse it reliably.
//
// Backward compatibility: if cfg.Logging.Level is empty (e.g. the Config was
// constructed manually in a test without going through Load()), the function
// falls back to cfg.LogLevel, and then to "info".
func NewLogger(cfg *config.Config) (*zap.Logger, error) {
	// Resolve effective log level, applying backward-compat fallback chain.
	levelStr := cfg.Logging.Level
	if levelStr == "" {
		levelStr = cfg.LogLevel
	}
	if levelStr == "" {
		levelStr = "info"
	}

	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(levelStr)); err != nil {
		// Unknown level string — default to info rather than failing startup.
		zapLevel = zapcore.InfoLevel
	}
	levelEnabler := zap.NewAtomicLevelAt(zapLevel)

	// Resolve effective format, defaulting to JSON when unset.
	format := cfg.Logging.Format
	if format == "" {
		format = "json"
	}

	// Build the stdout encoder according to the configured format.
	var stdoutEncoder zapcore.Encoder
	if format == "console" {
		// Human-readable, coloured console output for development.
		encCfg := zap.NewDevelopmentEncoderConfig()
		encCfg.TimeKey = "ts"
		encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		stdoutEncoder = zapcore.NewConsoleEncoder(encCfg)
	} else {
		// Structured JSON output for staging and production.
		encCfg := zap.NewProductionEncoderConfig()
		encCfg.TimeKey = "ts"
		encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
		stdoutEncoder = zapcore.NewJSONEncoder(encCfg)
	}

	// Stdout core — always present.
	stdoutCore := zapcore.NewCore(
		stdoutEncoder,
		zapcore.Lock(os.Stdout),
		levelEnabler,
	)

	// Optional file core backed by lumberjack for automatic log rotation.
	var cores []zapcore.Core
	cores = append(cores, stdoutCore)

	if cfg.Logging.File.Enabled {
		// M-1e: proactively verify the log file's parent directory is writable
		// and the path itself can be opened for append. lumberjack.Logger lazily
		// fails on first write — silently dropping log lines — so operators
		// enabling file logging on a misconfigured path get zero signal that
		// their file logs are being lost. Probe-and-warn instead, then fall
		// back cleanly to stdout-only.
		logDir := filepath.Dir(cfg.Logging.File.Path)
		if mkErr := os.MkdirAll(logDir, 0o755); mkErr != nil {
			// Directory cannot be created (nonexistent drive on Windows,
			// permission denied on Linux, etc.). Emit a one-line warning to
			// the stdout core and skip the file core entirely.
			stdoutOnly := zap.New(stdoutCore, zap.AddCaller())
			stdoutOnly.Warn("logging.file.enabled=true but log directory is unwritable; falling back to stdout-only",
				zap.String("path", cfg.Logging.File.Path),
				zap.String("dir", logDir),
				zap.Error(mkErr),
			)
		} else if probeErr := probeWritable(cfg.Logging.File.Path); probeErr != nil {
			// Directory exists, but the file path itself can't be opened for
			// append (e.g. ACL denies the current user). Same fallback path:
			// warn once on stdout and skip the file core.
			stdoutOnly := zap.New(stdoutCore, zap.AddCaller())
			stdoutOnly.Warn("logging.file.enabled=true but log file path is unwritable; falling back to stdout-only",
				zap.String("path", cfg.Logging.File.Path),
				zap.Error(probeErr),
			)
		} else {
			// Probe succeeded — register the lumberjack-backed file core.
			fileWriter := &lumberjack.Logger{
				Filename:   cfg.Logging.File.Path,
				MaxSize:    cfg.Logging.File.MaxSizeMB,
				MaxBackups: cfg.Logging.File.MaxBackups,
				MaxAge:     cfg.Logging.File.MaxAgeDays,
				Compress:   cfg.Logging.File.Compress,
			}

			// The file sink always uses JSON so log-ingestion pipelines can parse it,
			// even when the stdout format is set to "console".
			jsonEncCfg := zap.NewProductionEncoderConfig()
			jsonEncCfg.TimeKey = "ts"
			jsonEncCfg.EncodeTime = zapcore.ISO8601TimeEncoder
			fileCore := zapcore.NewCore(
				zapcore.NewJSONEncoder(jsonEncCfg),
				zapcore.AddSync(fileWriter),
				levelEnabler,
			)
			cores = append(cores, fileCore)
		}
	}

	// Tee all cores together.
	core := zapcore.NewTee(cores...)

	// Build the final logger with caller info always enabled and stacktraces
	// only at Error level and above to avoid noise in normal operation.
	logger := zap.New(core,
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
	).Named("midas")

	return logger, nil
}

// probeWritable opens the path with O_CREATE|O_APPEND|O_WRONLY so a permission
// error or "can't create file" error surfaces synchronously (here, at logger
// construction) rather than at lumberjack's first asynchronous write. Closes
// the handle immediately on success; no log content is written by the probe.
//
// Used by NewLogger to validate the configured `logging.file.path` before
// registering the file core (M-1e).
func probeWritable(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// mapDatabaseDriver maps configuration driver names to actual registered driver names
func mapDatabaseDriver(configDriver string) string {
	switch configDriver {
	case "sqlite3":
		return "sqlite3"
	case "moderncsqlite":
		// Backward compatibility: route modernc to sqlite3 now that we standardize on mattn
		return "sqlite3"
	case "sqlite":
		// Backward compatibility: map legacy logical name to sqlite3
		return "sqlite3"
	default:
		return configDriver // postgres, etc. remain unchanged
	}
}

// NewDatabase creates a database connection
func NewDatabase(cfg *config.Config, logger *zap.Logger) (*sqlx.DB, error) {
	var dsn string

	if cfg.Database.Driver == "sqlite3" || cfg.Database.Driver == "sqlite" {
		dsn = cfg.Database.SQLitePath
	} else {
		dsn = cfg.Database.PostgresURL
	}

	// Map driver name to actual registered driver
	actualDriver := mapDatabaseDriver(cfg.Database.Driver)

	logger.Info("Connecting to database",
		zap.String("driver", cfg.Database.Driver),
		zap.String("actual_driver", actualDriver),
		zap.String("dsn", dsn))

	db, err := sqlx.Connect(actualDriver, dsn)
	if err != nil {
		return nil, err
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.Database.MaxOpenConn)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConn)
	db.SetConnMaxLifetime(30 * time.Minute) // Default 30 minutes

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, err
	}

	logger.Info("Database connection established")
	return db, nil
}

// NewRedisClient creates a Redis client
func NewRedisClient(cfg *config.Config, logger *zap.Logger) (*redis.Client, error) {
	logger.Info("Connecting to Redis",
		zap.String("url", cfg.Cache.RedisURL))

	// Parse Redis URL to extract host/port
	opts, err := redis.ParseURL(cfg.Cache.RedisURL)
	if err != nil {
		logger.Warn("Failed to parse Redis URL, will use memory cache",
			zap.Error(err))
		return nil, nil
	}

	// Configure connection settings
	opts.MaxRetries = 3
	opts.DialTimeout = 5 * time.Second
	opts.ReadTimeout = 3 * time.Second
	opts.WriteTimeout = 3 * time.Second
	opts.PoolSize = 10
	opts.MinIdleConns = 5

	client := redis.NewClient(opts)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		logger.Warn("Redis connection failed, will use memory cache",
			zap.Error(err))
		return nil, nil // Return nil to use memory cache fallback
	}

	logger.Info("Redis connection established")
	return client, nil
}

// CircuitBreakerFactory creates circuit breakers for different services
type CircuitBreakerFactory struct {
	logger *zap.Logger
}

func NewCircuitBreakerFactory(logger *zap.Logger) *CircuitBreakerFactory {
	return &CircuitBreakerFactory{logger: logger}
}

func (f *CircuitBreakerFactory) CreateSECCircuitBreaker() ports.CircuitBreaker {
	config := resilience.CircuitBreakerConfig{
		Name:             "sec_api",
		MaxFailures:      3,
		FailureTimeout:   30 * time.Second,
		SuccessThreshold: 2,
		RequestTimeout:   15 * time.Second,
		ResetTimeout:     60 * time.Second,
	}
	return resilience.NewCircuitBreaker(config, f.logger)
}

func (f *CircuitBreakerFactory) CreateMarketDataCircuitBreaker() ports.CircuitBreaker {
	config := resilience.CircuitBreakerConfig{
		Name:             "market_data_api",
		MaxFailures:      5,
		FailureTimeout:   15 * time.Second,
		SuccessThreshold: 3,
		RequestTimeout:   10 * time.Second,
		ResetTimeout:     30 * time.Second,
	}
	return resilience.NewCircuitBreaker(config, f.logger)
}

// RetryPolicyFactory creates retry policies for different services
type RetryPolicyFactory struct {
	logger *zap.Logger
}

func NewRetryPolicyFactory(logger *zap.Logger) *RetryPolicyFactory {
	return &RetryPolicyFactory{logger: logger}
}

func (f *RetryPolicyFactory) CreateSECRetryPolicy() ports.RetryPolicy {
	config := resilience.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		Strategy:    resilience.BackoffExponential,
		Jitter:      true,
	}
	return resilience.NewRetryPolicy(config, f.logger)
}

func (f *RetryPolicyFactory) CreateMarketDataRetryPolicy() ports.RetryPolicy {
	config := resilience.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   200 * time.Millisecond,
		MaxDelay:    2 * time.Second,
		Strategy:    resilience.BackoffLinear,
		Jitter:      true,
	}
	return resilience.NewRetryPolicy(config, f.logger)
}

// Repository Providers

func NewFinancialDataRepository(db *sqlx.DB) ports.FinancialDataRepository {
	return sqlite.NewFinancialDataRepository(db)
}

func NewMarketDataRepository(db *sqlx.DB) ports.MarketDataRepository {
	return sqlite.NewMarketDataRepository(db)
}

func NewMacroDataRepository(db *sqlx.DB) ports.MacroDataRepository {
	return sqlite.NewMacroDataRepository(db)
}

func NewTickerMappingRepository(db *sqlx.DB) ports.TickerMappingRepository {
	return sqlite.NewTickerMappingRepository(db)
}

func NewCacheRepository(redisClient *redis.Client, logger *zap.Logger) ports.CacheRepository {
	if redisClient != nil {
		logger.Info("Using Redis cache repository")
		return cache.NewRedisCacheRepository(redisClient)
	}

	logger.Info("Redis not available, using memory cache repository")
	return cache.NewMemoryCacheRepository()
}

func NewAuthRepository(db *sqlx.DB) auth.Repository {
	return sqlite.NewAuthRepository(db.DB)
}

// Gateway Providers

func NewSECGateway(
	cfg *config.Config,
	cbFactory *CircuitBreakerFactory,
	retryFactory *RetryPolicyFactory,
	logger *zap.Logger,
) ports.SECGateway {
	return sec.NewGateway(&cfg.SEC, logger)
}

func NewMarketDataGateway(
	cfg *config.Config,
	cbFactory *CircuitBreakerFactory,
	retryFactory *RetryPolicyFactory,
	logger *zap.Logger,
) ports.MarketDataGateway {
	return market.NewGateway(&cfg.Market, logger)
}

// NewMacroDataGateway creates a macro data gateway
func NewMacroDataGateway(
	cfg *config.Config,
	logger *zap.Logger,
) ports.MacroDataGateway {
	return macro.NewGateway(&cfg.Macro, logger)
}

// Service Providers

func NewAuthService(repository auth.Repository, logger *zap.Logger) *auth.Service {
	return auth.NewService(repository, logger)
}

func NewRateLimiterService(cache ports.CacheRepository, logger *zap.Logger) *ratelimit.RateLimiter {
	// Create a rate limiter cache store adapter
	cacheStore := &RateLimiterCacheAdapter{cache: cache}
	limiter := ratelimit.NewRateLimiter(cacheStore, logger)

	// Set up default rate limits
	ctx := context.Background()
	if err := limiter.SetDefaultLimits(ctx); err != nil {
		logger.Warn("Failed to set default rate limits", zap.Error(err))
	}

	return limiter
}

func NewDataCleanerService(cfg *config.Config, logger *zap.Logger, aiSvc aiSvc.AIService, calcEmitter *calclog.Emitter) (datacleaner.DataCleanerService, error) {
	return datacleaner.NewDataCleanerService(cfg, aiSvc, calcEmitter)
}

func NewValuationService(
	financialRepo ports.FinancialDataRepository,
	marketRepo ports.MarketDataRepository,
	macroRepo ports.MacroDataRepository,
	cache ports.CacheRepository,
	dataCleaner datacleaner.DataCleanerService,
	dataFetcher *datafetcher.DataFetcher,
	marketGateway ports.MarketDataGateway,
	metricsService *metrics.Service,
	cfg *config.Config,
	logger *zap.Logger,
	calcEmitter *calclog.Emitter,
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
	)

	// Wire YFinanceGateway for analyst consensus estimates.
	// The market gateway wraps a YFinanceClient that implements YFinanceGateway.
	if gw, ok := marketGateway.(*market.Gateway); ok && gw.YFinanceClient() != nil {
		svc.SetYFinanceGateway(gw.YFinanceClient())
		logger.Info("YFinanceGateway wired for analyst consensus estimates")
	}

	return svc
}

// NewAIService creates the AI service based on configuration with logger injection.
func NewAIService(cfg *config.Config, logger *zap.Logger) aiSvc.AIService {
	return aiSvc.BuildAIServiceWithLogger(&cfg.DataCleaner, logger)
}

// NewWatchlistRepository creates a new watchlist repository
func NewWatchlistRepository(db *sqlx.DB) ports.WatchlistRepository {
	return sqlite.NewWatchlistRepository(db.DB)
}

// NewWatchlistService creates a new watchlist service
func NewWatchlistService(repo ports.WatchlistRepository, logger *zap.Logger) *watchlist.Service {
	return watchlist.NewService(repo, logger)
}

// NewSchedulerService provides a scheduler configured from app config. It is disabled by default
// and starts only when scheduler.enabled=true in configuration.
type SchedulerParams struct {
	fx.In
	Lifecycle    fx.Lifecycle
	Logger       *zap.Logger
	Fetcher      *datafetcher.DataFetcher
	WatchlistSvc *watchlist.Service
	Cfg          *config.Config
}

func NewSchedulerService(p SchedulerParams) *scheduler.Service {
	// Create watchlist-based ingestion job
	ingestionJob := datafetcher.NewIngestionJob(
		p.Fetcher,      // BulkFetcher
		p.WatchlistSvc, // WatchlistProvider
		p.WatchlistSvc, // FetchResultRecorder (same service implements both)
		p.Logger,
	)

	// Scheduler configuration from app config
	sched := scheduler.New(scheduler.Config{
		Enabled:        p.Cfg.Scheduler.Enabled,
		Interval:       p.Cfg.Scheduler.Interval,
		MaxConcurrency: p.Cfg.Scheduler.MaxConcurrency,
	}, p.Logger, ingestionJob)

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// Start scheduler if enabled
			sched.Start(ctx)
			return nil
		},
	})
	return sched
}

// NewDataFetcher creates a new DataFetcher service
func NewDataFetcher(
	secGateway ports.SECGateway,
	marketGateway ports.MarketDataGateway,
	macroGateway ports.MacroDataGateway,
	cache ports.CacheRepository,
) *datafetcher.DataFetcher {
	return datafetcher.NewDataFetcher(
		secGateway,
		marketGateway,
		macroGateway,
		cache,
	)
}

// Handler Providers

func NewHealthHandler(
	logger *zap.Logger,
	db *sqlx.DB,
	redis *redis.Client,
	cache ports.CacheRepository,
	rateLimiter *ratelimit.RateLimiter,
	secGateway ports.SECGateway,
	marketGateway ports.MarketDataGateway,
	macroGateway ports.MacroDataGateway,
	metricsService *metrics.Service,
) *handlers.HealthHandler {
	return handlers.NewHealthHandler(
		logger,
		db,
		redis,
		cache,
		rateLimiter,
		secGateway,
		marketGateway,
		macroGateway,
		metricsService,
	)
}

// NewMetricsService creates a new Prometheus metrics service
func NewMetricsService(logger *zap.Logger) *metrics.Service {
	return metrics.NewService(logger)
}

// RegisterHooksParams defines the parameters for RegisterHooks
type RegisterHooksParams struct {
	fx.In
	Lifecycle      fx.Lifecycle
	DB             *sqlx.DB
	Logger         *zap.Logger
	RedisClient    *redis.Client `optional:"true"`
	ArtifactReaper *artifact.Reaper
}

// RegisterHooks registers application lifecycle hooks
func RegisterHooks(params RegisterHooksParams) {
	params.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			params.Logger.Info("Application starting...")
			// Start the bundle reaper. No-op when ArtifactStore.Enabled=false.
			// Use a long-lived context so the goroutine survives until OnStop.
			//nolint:contextcheck // intentional fresh context for background daemon
			params.ArtifactReaper.Start(context.Background())
			return nil
		},
		OnStop: func(ctx context.Context) error {
			params.Logger.Info("Application stopping...")

			// Stop reaper goroutine (idempotent + nil-safe).
			params.ArtifactReaper.Stop()

			// Close database connection
			if params.DB != nil {
				if err := params.DB.Close(); err != nil {
					params.Logger.Error("Failed to close database", zap.Error(err))
				}
			}

			// Close Redis connection
			if params.RedisClient != nil {
				if err := params.RedisClient.Close(); err != nil {
					params.Logger.Error("Failed to close Redis", zap.Error(err))
				}
			}

			return nil
		},
	})
}

// NewArtifactReaper builds the bundle reaper from app config. The reaper is
// idle (no goroutine) when ArtifactStore.Enabled is false; Start is called
// from RegisterHooks at app startup so it begins sweeping immediately.
func NewArtifactReaper(cfg *config.Config) *artifact.Reaper {
	return artifact.NewReaper(artifact.Config{
		Enabled:       cfg.Logging.ArtifactStore.Enabled,
		RootPath:      cfg.Logging.ArtifactStore.RootPath,
		RetentionDays: cfg.Logging.ArtifactStore.RetentionDays,
		MaxTotalBytes: cfg.Logging.ArtifactStore.MaxTotalBytes,
		QueueSize:     cfg.Logging.ArtifactStore.QueueSize,
	})
}
