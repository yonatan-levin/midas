package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration settings for the application
type Config struct {
	// Application settings
	Version       string `mapstructure:"version"`
	Environment   string `mapstructure:"environment"`
	BuildTime     string `mapstructure:"build_time"`
	GitCommit     string `mapstructure:"git_commit"`
	LogLevel      string `mapstructure:"log_level"`
	Port          string `mapstructure:"port"`
	EnableSwagger bool   `mapstructure:"enable_swagger"`
	EnablePprof   bool   `mapstructure:"enable_pprof"`

	// Component configurations
	Server      ServerConfig      `mapstructure:"server"`
	Database    DatabaseConfig    `mapstructure:"database"`
	Cache       CacheConfig       `mapstructure:"cache"`
	SEC         SECConfig         `mapstructure:"sec"`
	Market      MarketConfig      `mapstructure:"market"`
	Macro       MacroConfig       `mapstructure:"macro"`
	Valuation   ValuationConfig   `mapstructure:"valuation"`
	DataCleaner DataCleanerConfig `mapstructure:"datacleaner"`
	Scheduler   SchedulerConfig   `mapstructure:"scheduler"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Port         string        `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`
}

// DatabaseConfig holds database connection settings
type DatabaseConfig struct {
	Driver      string `mapstructure:"driver"` // sqlite3 or postgres
	SQLitePath  string `mapstructure:"sqlite_path"`
	PostgresURL string `mapstructure:"postgres_url"`
	MaxOpenConn int    `mapstructure:"max_open_conn"`
	MaxIdleConn int    `mapstructure:"max_idle_conn"`
}

// CacheConfig holds Redis cache configuration
type CacheConfig struct {
	RedisURL           string        `mapstructure:"redis_url"`
	SECFilingsTTL      time.Duration `mapstructure:"sec_filings_ttl"`
	MarketDataTTL      time.Duration `mapstructure:"market_data_ttl"`
	MacroDataTTL       time.Duration `mapstructure:"macro_data_ttl"`
	ValuationResultTTL time.Duration `mapstructure:"valuation_result_ttl"`
	DefaultTTL         time.Duration `mapstructure:"default_ttl"`
}

// SECConfig holds SEC API configuration
type SECConfig struct {
	BaseURL          string        `mapstructure:"base_url"`
	TickerMappingURL string        `mapstructure:"ticker_mapping_url"`
	UserAgent        string        `mapstructure:"user_agent"`
	RateLimit        int           `mapstructure:"rate_limit"` // requests per second
	RequestTimeout   time.Duration `mapstructure:"request_timeout"`
	MaxRetries       int           `mapstructure:"max_retries"`
	RetryBackoffBase time.Duration `mapstructure:"retry_backoff_base"`
}

// MarketConfig holds market data source configuration
type MarketConfig struct {
	YFinance YFinanceConfig `mapstructure:"yfinance"`
	Finzive  FinziveConfig  `mapstructure:"finzive"`
}

// YFinanceConfig holds yfinance-style API configuration
type YFinanceConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	BaseURL        string        `mapstructure:"base_url"`
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
	MaxRetries     int           `mapstructure:"max_retries"`
}

// FinziveConfig holds Finzive scraper configuration
type FinziveConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	BaseURL        string        `mapstructure:"base_url"`
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
	MaxRetries     int           `mapstructure:"max_retries"`
	UserAgent      string        `mapstructure:"user_agent"`
}

// MacroConfig holds macro data configuration
type MacroConfig struct {
	FREDEnabled             bool    `mapstructure:"fred_enabled"`
	FREDAPIKey              string  `mapstructure:"fred_api_key"`
	FREDBaseURL             string  `mapstructure:"fred_base_url"`
	ManualRiskFreeRate      float64 `mapstructure:"manual_risk_free_rate"`
	ManualMarketRiskPremium float64 `mapstructure:"manual_market_risk_premium"`
}

// ValuationConfig holds valuation calculation settings
type ValuationConfig struct {
	DefaultMarketRiskPremium float64 `mapstructure:"default_market_risk_premium"`
	DefaultTerminalGrowthCap float64 `mapstructure:"default_terminal_growth_cap"`
	DefaultTaxRate           float64 `mapstructure:"default_tax_rate"`
	MinDataPointsForGrowth   int     `mapstructure:"min_data_points_for_growth"`
	MaxBulkSize              int     `mapstructure:"max_bulk_size"`

	// Cache settings
	CacheTTL time.Duration `mapstructure:"cache_ttl"` // TTL for valuation results cache

	// Performance thresholds
	SlowRequestThreshold time.Duration `mapstructure:"slow_request_threshold"` // Threshold for logging slow requests
	DataFetchTimeout     time.Duration `mapstructure:"data_fetch_timeout"`     // Timeout for slow data fetch warnings

	// Performance optimizations
	EnableConcurrentDataFetch bool `mapstructure:"enable_concurrent_data_fetch"` // Enable concurrent market/macro data fetching

	// DCF calculation specific settings
	DCFProjectionYears    int     `mapstructure:"dcf_projection_years"`    // Number of explicit forecast years
	DCFMaxGrowthRate      float64 `mapstructure:"dcf_max_growth_rate"`     // Maximum allowed growth rate
	DCFMinGrowthRate      float64 `mapstructure:"dcf_min_growth_rate"`     // Minimum allowed growth rate
	DCFIterationTolerance float64 `mapstructure:"dcf_iteration_tolerance"` // Tolerance for implied growth calculations
	DCFMaxIterations      int     `mapstructure:"dcf_max_iterations"`      // Max iterations for implied growth calculations
}

// SchedulerConfig holds scheduler configuration
type SchedulerConfig struct {
	Enabled        bool          `mapstructure:"enabled"`         // Enable/disable scheduler service
	Interval       time.Duration `mapstructure:"interval"`        // Interval between scheduler runs
	MaxConcurrency int           `mapstructure:"max_concurrency"` // Maximum concurrent jobs
}

// DataCleanerConfig holds data cleaning configuration
type DataCleanerConfig struct {
	// Rules configuration paths
	RulesPath         string `mapstructure:"rules_path"`          // Path to main rules JSON file
	IndustryRulesPath string `mapstructure:"industry_rules_path"` // Path to industry rules directory
	SchemaPath        string `mapstructure:"schema_path"`         // Path to JSON schema file

	// Processing configuration
	Enabled             bool          `mapstructure:"enabled"`               // Enable/disable data cleaning
	EnableAIIntegration bool          `mapstructure:"enable_ai_integration"` // Enable AI service integration
	AIServiceURL        string        `mapstructure:"ai_service_url"`        // External AI service URL for footnote parsing
	AIServiceTimeout    time.Duration `mapstructure:"ai_service_timeout"`    // AI service request timeout

	// Quality scoring thresholds
	MinQualityScore  float64 `mapstructure:"min_quality_score"`  // Minimum acceptable quality score (0-100)
	HighQualityScore float64 `mapstructure:"high_quality_score"` // High quality threshold (0-100)

	// Risk flagging configuration
	EnableRiskFlags   bool    `mapstructure:"enable_risk_flags"`  // Enable automated risk flagging
	CriticalThreshold float64 `mapstructure:"critical_threshold"` // Critical risk threshold
	WarningThreshold  float64 `mapstructure:"warning_threshold"`  // Warning risk threshold

	// Performance settings
	MaxConcurrentRules int           `mapstructure:"max_concurrent_rules"` // Max concurrent rule processing
	EnableCaching      bool          `mapstructure:"enable_caching"`       // Enable cleaning result caching
	CacheTTL           time.Duration `mapstructure:"cache_ttl"`            // Cache TTL for cleaning results

	// Industry classification
	DefaultIndustry     string `mapstructure:"default_industry"`      // Default GICS code when industry unknown
	EnableIndustryRules bool   `mapstructure:"enable_industry_rules"` // Enable industry-specific rules

	// Audit and logging
	EnableAuditTrail bool `mapstructure:"enable_audit_trail"` // Enable detailed audit trail
	LogAdjustments   bool `mapstructure:"log_adjustments"`    // Log all adjustments made
	LogFlags         bool `mapstructure:"log_flags"`          // Log all flags raised
}

// Load loads configuration from environment variables and config files
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	// Set default values
	setDefaults()

	// Enable environment variable support (map nested keys like database.driver -> DATABASE_DRIVER)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Try to read config file (optional)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Config file not found, continue with env vars and defaults
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate configuration
	if err := validate(&config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

// setDefaults sets default configuration values
func setDefaults() {
	// Application defaults
	viper.SetDefault("port", "8080")
	viper.SetDefault("environment", "development")
	viper.SetDefault("log_level", "debug")
	viper.SetDefault("enable_swagger", false)
	viper.SetDefault("enable_pprof", false)

	// Server defaults
	viper.SetDefault("server.port", "8080")
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("server.write_timeout", "30s")
	viper.SetDefault("server.idle_timeout", "120s")

	// Database defaults
	viper.SetDefault("database.driver", "sqlite3")
	viper.SetDefault("database.sqlite_path", "./data/midas.db")
	viper.SetDefault("database.max_open_conn", 25)
	viper.SetDefault("database.max_idle_conn", 10)

	// Cache defaults
	viper.SetDefault("cache.redis_url", "redis://localhost:6379")
	viper.SetDefault("cache.sec_filings_ttl", "48h")
	viper.SetDefault("cache.market_data_ttl", "15m")
	viper.SetDefault("cache.macro_data_ttl", "4h")
	viper.SetDefault("cache.valuation_result_ttl", "1h")
	viper.SetDefault("cache.default_ttl", "30m")

	// SEC API defaults
	viper.SetDefault("sec.base_url", "https://data.sec.gov/api/xbrl")
	viper.SetDefault("sec.ticker_mapping_url", "https://www.sec.gov/files/company_tickers.json")
	viper.SetDefault("sec.user_agent", "Midas DCF API admin@example.com")
	viper.SetDefault("sec.rate_limit", 10) // requests per second
	viper.SetDefault("sec.request_timeout", "30s")
	viper.SetDefault("sec.max_retries", 3)
	viper.SetDefault("sec.retry_backoff_base", "1s")

	// Market data defaults
	viper.SetDefault("market.yfinance.enabled", true)
	viper.SetDefault("market.yfinance.base_url", "https://query1.finance.yahoo.com")
	viper.SetDefault("market.yfinance.request_timeout", "30s")
	viper.SetDefault("market.yfinance.max_retries", 3)

	viper.SetDefault("market.finzive.enabled", true)
	viper.SetDefault("market.finzive.base_url", "https://finzive.com")
	viper.SetDefault("market.finzive.request_timeout", "60s")
	viper.SetDefault("market.finzive.max_retries", 2)
	viper.SetDefault("market.finzive.user_agent", "Mozilla/5.0 (compatible; Midas/1.0)")

	// Macro data defaults
	viper.SetDefault("macro.fred_enabled", false)
	viper.SetDefault("macro.fred_base_url", "https://api.stlouisfed.org/fred")
	viper.SetDefault("macro.manual_risk_free_rate", 0.045)     // 4.5% - 10-year Treasury approximation
	viper.SetDefault("macro.manual_market_risk_premium", 0.05) // 5% - Standard market risk premium

	// Valuation defaults
	viper.SetDefault("valuation.default_market_risk_premium", 0.05) // 5%
	viper.SetDefault("valuation.default_terminal_growth_cap", 0.03) // 3%
	viper.SetDefault("valuation.default_tax_rate", 0.21)            // 21%
	viper.SetDefault("valuation.min_data_points_for_growth", 2)
	viper.SetDefault("valuation.max_bulk_size", 50)
	viper.SetDefault("valuation.cache_ttl", "1h")              // 1 hour cache TTL for valuation results
	viper.SetDefault("valuation.slow_request_threshold", "5s") // Log slow requests after 5 seconds
	viper.SetDefault("valuation.data_fetch_timeout", "10s")    // Warn about slow data fetch after 10 seconds

	// DCF calculation defaults
	viper.SetDefault("valuation.dcf_projection_years", 5)         // 5-year explicit forecast
	viper.SetDefault("valuation.dcf_max_growth_rate", 0.5)        // 50% max growth
	viper.SetDefault("valuation.dcf_min_growth_rate", -0.3)       // -30% min growth
	viper.SetDefault("valuation.dcf_iteration_tolerance", 0.0001) // 0.01% tolerance
	viper.SetDefault("valuation.dcf_max_iterations", 100)         // 100 max iterations

	// DataCleaner defaults
	viper.SetDefault("datacleaner.rules_path", "./config/datacleaner/rules.json")
	viper.SetDefault("datacleaner.industry_rules_path", "./config/datacleaner/industry")
	viper.SetDefault("datacleaner.schema_path", "./config/datacleaner/schema.json")
	viper.SetDefault("datacleaner.enabled", true)
	viper.SetDefault("datacleaner.enable_ai_integration", false) // Disabled by default until AI service is ready
	viper.SetDefault("datacleaner.ai_service_url", "")
	viper.SetDefault("datacleaner.ai_service_timeout", "30s")
	viper.SetDefault("datacleaner.min_quality_score", 60.0)  // 60% minimum quality
	viper.SetDefault("datacleaner.high_quality_score", 85.0) // 85% high quality
	viper.SetDefault("datacleaner.enable_risk_flags", true)
	viper.SetDefault("datacleaner.critical_threshold", 0.3)  // 30% critical threshold
	viper.SetDefault("datacleaner.warning_threshold", 0.15)  // 15% warning threshold
	viper.SetDefault("datacleaner.max_concurrent_rules", 10) // Process 10 rules concurrently
	viper.SetDefault("datacleaner.enable_caching", true)
	viper.SetDefault("datacleaner.cache_ttl", "6h")      // Cache cleaning results for 6 hours
	viper.SetDefault("datacleaner.default_industry", "") // No default industry
	viper.SetDefault("datacleaner.enable_industry_rules", true)
	viper.SetDefault("datacleaner.enable_audit_trail", true) // Enable full audit trail
	viper.SetDefault("datacleaner.log_adjustments", true)    // Log all adjustments
	viper.SetDefault("datacleaner.log_flags", true)          // Log all flags

	// Scheduler defaults
	viper.SetDefault("scheduler.enabled", false)     // Disabled by default
	viper.SetDefault("scheduler.interval", "24h")    // Run daily by default
	viper.SetDefault("scheduler.max_concurrency", 2) // Maximum 2 concurrent jobs
}

// validate performs basic validation on the configuration
func validate(config *Config) error {
	if config.Server.Port == "" {
		return fmt.Errorf("server port cannot be empty")
	}

	if config.Database.Driver != "sqlite3" && config.Database.Driver != "postgres" {
		return fmt.Errorf("database driver must be 'sqlite3' or 'postgres'")
	}

	if config.Database.Driver == "postgres" && config.Database.PostgresURL == "" {
		return fmt.Errorf("postgres_url is required when using postgres driver")
	}

	if config.SEC.RateLimit <= 0 {
		return fmt.Errorf("SEC rate limit must be positive")
	}

	if config.Valuation.MaxBulkSize <= 0 || config.Valuation.MaxBulkSize > 100 {
		return fmt.Errorf("max_bulk_size must be between 1 and 100")
	}

	return nil
}
