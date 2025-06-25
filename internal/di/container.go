package di

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/infra/repositories/cache"
	"github.com/midas/dcf-valuation-api/internal/infra/repositories/sqlite"
	"github.com/midas/dcf-valuation-api/internal/infra/resilience"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// Container holds the dependency injection container
type Container struct {
	app *fx.App
}

// NewContainer creates a new dependency injection container
func NewContainer() *Container {
	app := fx.New(
		// Configuration Module
		fx.Provide(config.Load),

		// Logging Module
		fx.Provide(NewLogger),

		// Database Module
		fx.Provide(NewDatabase),

		// Redis Module
		fx.Provide(NewRedisClient),

		// Resilience Module
		fx.Provide(NewCircuitBreakerFactory),
		fx.Provide(NewRetryPolicyFactory),

		// Repository Module
		fx.Provide(NewFinancialDataRepository),
		fx.Provide(NewMarketDataRepository),
		fx.Provide(NewMacroDataRepository),
		fx.Provide(NewCacheRepository),

		// Gateway Module
		fx.Provide(NewSECGateway),
		fx.Provide(NewMarketDataGateway),
		fx.Provide(NewMacroDataGateway),

		// Service Module
		fx.Provide(NewValuationService),

		// Lifecycle hooks
		fx.Invoke(RegisterHooks),

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

// NewLogger creates a new structured logger
func NewLogger(cfg *config.Config) (*zap.Logger, error) {
	// Use development logger for now, can be configured later
	return zap.NewDevelopment()
}

// NewDatabase creates a database connection
func NewDatabase(cfg *config.Config, logger *zap.Logger) (*sqlx.DB, error) {
	var dsn string

	if cfg.Database.Driver == "sqlite" {
		dsn = cfg.Database.SQLitePath
	} else {
		dsn = cfg.Database.PostgresURL
	}

	logger.Info("Connecting to database",
		zap.String("driver", cfg.Database.Driver),
		zap.String("dsn", dsn))

	db, err := sqlx.Connect(cfg.Database.Driver, dsn)
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
	// TODO: Implement macro data repository
	return nil // sqlite.NewMacroDataRepository(db)
}

func NewCacheRepository(redisClient *redis.Client, logger *zap.Logger) ports.CacheRepository {
	if redisClient != nil {
		logger.Info("Using Redis cache repository")
		return cache.NewRedisCacheRepository(redisClient)
	}

	logger.Info("Redis not available, using memory cache repository")
	return cache.NewMemoryCacheRepository()
}

// Gateway Providers

func NewSECGateway(
	cfg *config.Config,
	cbFactory *CircuitBreakerFactory,
	retryFactory *RetryPolicyFactory,
	logger *zap.Logger,
) ports.SECGateway {
	// TODO: SEC Gateway needs to implement GetCompanyConcepts method
	return nil // sec.NewGateway(&cfg.SEC, logger)
}

func NewMarketDataGateway(
	cfg *config.Config,
	cbFactory *CircuitBreakerFactory,
	retryFactory *RetryPolicyFactory,
	logger *zap.Logger,
) ports.MarketDataGateway {
	// TODO: Market Gateway needs to implement GetHistoricalPrices method
	return nil // market.NewGateway(&cfg.Market, logger)
}

// NewMacroDataGateway creates a macro data gateway
func NewMacroDataGateway(
	cfg *config.Config,
	logger *zap.Logger,
) ports.MacroDataGateway {
	// TODO: Implement macro data gateway
	return nil
}

// Service Providers

func NewValuationService(
	financialRepo ports.FinancialDataRepository,
	marketRepo ports.MarketDataRepository,
	macroRepo ports.MacroDataRepository,
	cache ports.CacheRepository,
	logger *zap.Logger,
) *valuation.Service {
	return valuation.NewService(financialRepo, marketRepo, macroRepo, cache, logger)
}

// RegisterHooks registers application lifecycle hooks
func RegisterHooks(
	lifecycle fx.Lifecycle,
	db *sqlx.DB,
	redisClient *redis.Client,
	logger *zap.Logger,
) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			logger.Info("Application starting...")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("Application stopping...")

			// Close database connection
			if db != nil {
				if err := db.Close(); err != nil {
					logger.Error("Failed to close database", zap.Error(err))
				}
			}

			// Close Redis connection
			if redisClient != nil {
				if err := redisClient.Close(); err != nil {
					logger.Error("Failed to close Redis", zap.Error(err))
				}
			}

			return nil
		},
	})
}
