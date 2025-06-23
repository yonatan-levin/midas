package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration settings for the application
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Cache     CacheConfig     `mapstructure:"cache"`
	SEC       SECConfig       `mapstructure:"sec"`
	Market    MarketConfig    `mapstructure:"market"`
	Valuation ValuationConfig `mapstructure:"valuation"`
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
	Driver      string `mapstructure:"driver"` // sqlite or postgres
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

// ValuationConfig holds valuation calculation settings
type ValuationConfig struct {
	DefaultMarketRiskPremium float64 `mapstructure:"default_market_risk_premium"`
	DefaultTerminalGrowthCap float64 `mapstructure:"default_terminal_growth_cap"`
	DefaultTaxRate           float64 `mapstructure:"default_tax_rate"`
	MinDataPointsForGrowth   int     `mapstructure:"min_data_points_for_growth"`
	MaxBulkSize              int     `mapstructure:"max_bulk_size"`
}

// Load loads configuration from environment variables and config files
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	// Set default values
	setDefaults()

	// Enable environment variable support
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
	// Server defaults
	viper.SetDefault("server.port", "8080")
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("server.write_timeout", "30s")
	viper.SetDefault("server.idle_timeout", "120s")

	// Database defaults
	viper.SetDefault("database.driver", "sqlite")
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

	// Valuation defaults
	viper.SetDefault("valuation.default_market_risk_premium", 0.05) // 5%
	viper.SetDefault("valuation.default_terminal_growth_cap", 0.03) // 3%
	viper.SetDefault("valuation.default_tax_rate", 0.21)            // 21%
	viper.SetDefault("valuation.min_data_points_for_growth", 2)
	viper.SetDefault("valuation.max_bulk_size", 50)
}

// validate performs basic validation on the configuration
func validate(config *Config) error {
	if config.Server.Port == "" {
		return fmt.Errorf("server port cannot be empty")
	}

	if config.Database.Driver != "sqlite" && config.Database.Driver != "postgres" {
		return fmt.Errorf("database driver must be 'sqlite' or 'postgres'")
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
