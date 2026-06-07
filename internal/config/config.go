package config

import (
	"fmt"
	"strings"
	"sync"
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

	// Observability
	Logging LoggingConfig `mapstructure:"logging"`
}

// LoggingConfig holds structured-logging and log-file configuration.
// Environment-specific defaults are applied by applyLoggingEnvironmentDefaults
// after the config file is read, so that explicit user values always win.
type LoggingConfig struct {
	// Level controls the minimum log level emitted (debug|info|warn|error).
	Level string `mapstructure:"level"`

	// Format selects the encoder: "json" for production, "console" for development.
	Format string `mapstructure:"format"`

	// TraceCalculations, when true, emits per-stage DCF calculation entries at
	// Info level (via calclog.Emitter). When false, they are emitted at Debug
	// level and are effectively invisible in production log streams.
	TraceCalculations bool `mapstructure:"trace_calculations"`

	// AccessLogSkipPaths lists endpoint paths that are excluded from the
	// access-log middleware (e.g. health probes, metrics scrape endpoint).
	AccessLogSkipPaths []string `mapstructure:"access_log_skip_paths"`

	// File holds rolling-file sink configuration.
	File LogFileConfig `mapstructure:"file"`

	// Narrate controls the Tier-1 pipeline-phase narrative stream. See
	// docs/refactoring/observability-narrative-and-artifacts-spec.md (§4-§5).
	Narrate NarrateConfig `mapstructure:"narrate"`

	// ArtifactStore controls the Tier-3 per-request bundle on disk. See
	// docs/refactoring/observability-narrative-and-artifacts-spec.md (§7-§8).
	ArtifactStore ArtifactStoreConfig `mapstructure:"artifact_store"`
}

// NarrateConfig governs the Tier-1 narrate stream (one Info line per
// pipeline phase).
type NarrateConfig struct {
	// Enabled is the master switch. When false, narrate.Emitter.Emit is a
	// no-op for every request.
	Enabled bool `mapstructure:"enabled"`

	// SampleRate is a value in [0.0, 1.0]. The decision is made ONCE per
	// request_id at request entry and stuck on the emitter — a request is
	// either fully narrated or fully sampled out, never half-told.
	SampleRate float64 `mapstructure:"sample_rate"`

	// RedactFields lists field keys to drop from emitted lines (e.g.
	// "client_ip_hash") for operators with stricter privacy requirements.
	RedactFields []string `mapstructure:"redact_fields"`
}

// ArtifactStoreConfig governs the Tier-3 artifact bundle (per-request
// directory of raw + parsed payloads on disk).
type ArtifactStoreConfig struct {
	// Enabled is the master switch. Default false in all environments
	// except development.
	Enabled bool `mapstructure:"enabled"`

	// RootPath is the directory under which dated bundle subtrees are
	// created. Default ./artifacts.
	RootPath string `mapstructure:"root_path"`

	// RetentionDays is the maximum age of bundle directories before the
	// reaper sweeps them. 0 disables the age-based sweep.
	RetentionDays int `mapstructure:"retention_days"`

	// MaxTotalBytes is the soft cap for the entire bundle root tree.
	// When exceeded, the reaper evicts oldest bundles first. 0 disables.
	MaxTotalBytes int64 `mapstructure:"max_total_bytes"`

	// QueueSize bounds the per-bundle snapshot queue. Bursty captures will
	// drop snapshots (logged + recorded as bundle outcome=partial) rather
	// than block the request thread. Default 256.
	QueueSize int `mapstructure:"queue_size"`

	// PendingBytesCap is the per-bundle in-memory buffer ceiling for
	// deferred (auto-on-error) bundles introduced in Phase 2.A. Bounds the
	// worst-case heap footprint when many requests are buffering snapshots
	// "just in case" they 5xx. Overflow drops oldest snapshots first.
	// Default 10 MiB; only consulted when Triggers.OnError is true.
	PendingBytesCap int64 `mapstructure:"pending_bytes_cap"`

	// Triggers controls which conditions open a bundle. Phase 1: only
	// Manual is honoured (?trace=1 / X-Midas-Trace). Phase 2.A adds OnError.
	Triggers ArtifactTriggers `mapstructure:"triggers"`
}

// ArtifactTriggers enumerates the per-request conditions that open a
// bundle. Phase 1 supports only Manual; Phase 2.A added OnError; Phase 2.B
// adds QualityFlagThreshold; Phase 2.C adds Always (see spec §13.C).
type ArtifactTriggers struct {
	// Manual = ?trace=1 query OR X-Midas-Trace: 1 header.
	Manual bool `mapstructure:"manual"`

	// OnError = auto-trigger when HTTP status >=500. The trace middleware
	// opens a deferred bundle for every request and only flushes to disk
	// if the response code crosses the 5xx threshold; non-erroring requests
	// pay only the in-memory buffer cost (capped by PendingBytesCap).
	OnError bool `mapstructure:"on_error"`

	// QualityFlagThreshold = auto-trigger when the data cleaner raises
	// one or more flags at or above the named severity (Phase 2.B).
	// Valid values are FlagSeverity vocabulary (info / low / warning /
	// medium / high / critical); empty string disables the trigger.
	// Off by default to keep the trigger opt-in and protect operators
	// who haven't sized disk for the expected flag-volume.
	//
	// Precedence at request-end: manual > on_quality_flag > on_error > always.
	// A request that satisfies both quality_flag and on_error promotes
	// with on_quality_flag because the flag list is more diagnostic than
	// the bare 5xx signal.
	QualityFlagThreshold string `mapstructure:"quality_flag_threshold"`

	// Always = auto-trigger for EVERY request regardless of status or
	// flag count (Phase 2.C; spec §13.C). Intended for sustained
	// debugging sessions — "flip on for an hour, flip off when done".
	// Off by default. When on alongside other triggers, sits at the
	// BOTTOM of the precedence ladder: a 5xx request still records
	// trigger=on_error in its manifest, a flagging request still
	// records trigger=on_quality_flag — only requests that fire NO
	// other trigger get trigger=always. This is intentional so operators
	// reading bundles can tell which ones are interesting (errors / data
	// issues) vs which are noise from the debugging session.
	//
	// CAUTION: enabling this without sizing the disk for the expected
	// request volume will fill the bundle root's max_total_bytes cap
	// fast and force the reaper to evict aggressively. Recommend
	// pairing with a tightened MaxTotalBytes (or a per-request rate
	// limit upstream) when using in production.
	Always bool `mapstructure:"always"`
}

// LogFileConfig controls the rolling log-file sink backed by lumberjack.
type LogFileConfig struct {
	// Enabled enables the file sink alongside the stdout sink (NewTee).
	Enabled bool `mapstructure:"enabled"`

	// Path is the absolute or relative path to the log file.
	// The directory is created automatically by lumberjack if it doesn't exist.
	Path string `mapstructure:"path"`

	// MaxSizeMB is the maximum size in megabytes before the file is rotated.
	MaxSizeMB int `mapstructure:"max_size_mb"`

	// MaxBackups is the number of old log files to retain.
	MaxBackups int `mapstructure:"max_backups"`

	// MaxAgeDays is the maximum number of days to retain old log files.
	MaxAgeDays int `mapstructure:"max_age_days"`

	// Compress determines whether rotated log files are gzip-compressed.
	Compress bool `mapstructure:"compress"`
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
	CookieURL      string        `mapstructure:"cookie_url"`
	CrumbURL       string        `mapstructure:"crumb_url"`
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
	MaxRetries     int           `mapstructure:"max_retries"`
	AuthTTL        time.Duration `mapstructure:"auth_ttl"`
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

	// GuidanceRoot is the directory root for Layer-B Phase-2 guidance-artifact
	// fixtures (internal/services/valuation/guidance.Loader). Empty (the
	// PRODUCTION DEFAULT) disables guidance entirely — every valuation takes the
	// absent path and is byte-identical to the Layer-A 4.7 engine (NF1). Phase 3
	// flips this to the real directory once an extraction tool produces
	// artifacts. Fixtures live under testdata/guidance for Phase 2.
	GuidanceRoot string `mapstructure:"guidance_root"`
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

	// Set base default values (environment-agnostic)
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

	// Apply environment-specific logging defaults AFTER reading the config file.
	// SetDefault has the lowest priority in Viper (below explicit config values
	// and env-var overrides), so user-supplied values always win.
	applyLoggingEnvironmentDefaults()

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Backward compatibility: if the new Logging.Level is empty (e.g. the caller
	// constructed a Config manually without filling Logging), fall back to the
	// legacy LogLevel field so the logger always has a usable level.
	if config.Logging.Level == "" && config.LogLevel != "" {
		config.Logging.Level = config.LogLevel
	}

	// Phase 2.B post-launch (REVIEWER MEDIUM-1): canonicalise operator-set
	// auto-trigger thresholds (lowercase + trim) so case/whitespace typos
	// in env vars or YAML resolve to the same comparison vocabulary the
	// runtime uses. The matching warn-on-unknown step lives in
	// ValidateArtifactTriggers, called from server boot where the singleton
	// *zap.Logger is in scope.
	normalizeArtifactTriggers(&config.Logging.ArtifactStore.Triggers)

	// Validate configuration
	if err := validate(&config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

// loadDefaultsMu serialises LoadDefaults() calls that briefly mutate the
// global viper singleton. Without the mutex, two concurrent LoadDefaults()
// calls could race on viper.Reset() and corrupt each other's snapshot.
//
// RPL-10 (2026-05-22).
var loadDefaultsMu sync.Mutex

// LoadDefaults returns a *Config populated with every viper.SetDefault that
// production applies via setDefaults() and the "development" branch of
// applyLoggingEnvironmentDefaults(), without reading any config file or
// environment variables and without running validate().
//
// This is the canonical "what does production look like with nothing on
// disk and no env overrides?" snapshot. It exists so callers (notably
// internal/observability/replay/module.go's
// TestReplayConfig_MirrorsAllValuationViperDefaults parity test) can
// compare a hand-mirrored config against the source-of-truth defaults
// without having to know how viper is wired.
//
// Implementation: briefly mutates the global viper singleton because
// setDefaults() and applyLoggingEnvironmentDefaults() are written
// against viper.SetDefault. We bracket the call with viper.Reset() on
// entry and exit, holding loadDefaultsMu so parallel calls do not race.
// Production Load() does its own viper setup at the top of its body, so
// a leaked default from LoadDefaults() would be overwritten anyway —
// but the defensive Reset is cheap and obvious.
//
// Contract: callers must NOT interleave LoadDefaults() with arbitrary
// viper mutation in the same goroutine. Tests that touch viper already
// viper.Reset() at start (see config_logging_test.go's resetViper).
//
// RPL-10 (2026-05-22). The alternative — parameterising setDefaults to
// take a *viper.Viper — would have been a cleaner long-term shape but a
// much larger diff (~80 viper.SetDefault call sites to rewrite) for a
// stopgap fix that becomes obsolete once RPL-9 lands the manifest-config
// snapshot. The Reset+mutex pattern keeps the production code path
// untouched.
func LoadDefaults() (*Config, error) {
	loadDefaultsMu.Lock()
	defer loadDefaultsMu.Unlock()

	viper.Reset()
	defer viper.Reset()

	setDefaults()
	applyLoggingEnvironmentDefaults()

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal defaults: %w", err)
	}

	// Backward-compat: mirror Load()'s fallback so callers see the same
	// shape they'd see from a real Load() with no config file present.
	if cfg.Logging.Level == "" && cfg.LogLevel != "" {
		cfg.Logging.Level = cfg.LogLevel
	}

	return &cfg, nil
}

// setDefaults sets base (environment-agnostic) default configuration values.
// Environment-specific logging defaults are applied separately by
// applyLoggingEnvironmentDefaults after the config file has been read.
func setDefaults() {
	// Application defaults
	viper.SetDefault("port", "8080")
	viper.SetDefault("environment", "development")
	viper.SetDefault("log_level", "debug")
	viper.SetDefault("enable_swagger", false)
	viper.SetDefault("enable_pprof", false)

	// Logging file defaults (always apply regardless of environment)
	viper.SetDefault("logging.file.path", "./logs/midas.log")
	viper.SetDefault("logging.file.max_size_mb", 100)
	viper.SetDefault("logging.file.max_backups", 10)
	viper.SetDefault("logging.file.max_age_days", 14)
	viper.SetDefault("logging.file.compress", true)

	// Access log skip paths: health probes and metrics scrape endpoint should
	// not pollute access logs with high-frequency noise.
	viper.SetDefault("logging.access_log_skip_paths", []string{"/metrics", "/health", "/ready"})

	// Narrate defaults: stream is on by default, full sampling. Per-environment
	// override (dev=on, staging/prod=on) handled in applyLoggingEnvironmentDefaults.
	viper.SetDefault("logging.narrate.sample_rate", 1.0)
	viper.SetDefault("logging.narrate.redact_fields", []string{})

	// Artifact-store defaults. Master Enabled flag is set per-environment
	// (default off in staging/prod) by applyLoggingEnvironmentDefaults so dev
	// gets bundles automatically while prod is opt-in.
	viper.SetDefault("logging.artifact_store.root_path", "./artifacts")
	viper.SetDefault("logging.artifact_store.retention_days", 7)
	viper.SetDefault("logging.artifact_store.max_total_bytes", int64(5)*int64(1<<30)) // 5 GiB
	viper.SetDefault("logging.artifact_store.queue_size", 256)
	// 10 MiB per-bundle in-memory buffer cap for deferred (auto-on-error)
	// bundles. Sized roughly 2x the worst-case Phase 1 happy-path bundle so
	// realistic requests rarely overflow.
	viper.SetDefault("logging.artifact_store.pending_bytes_cap", int64(10)*int64(1<<20))
	viper.SetDefault("logging.artifact_store.triggers.manual", true)
	// Phase 2.A — auto-on-error: OFF by default everywhere. Operators opt
	// in via env var (LOGGING_ARTIFACT_STORE_TRIGGERS_ON_ERROR=true) once
	// they've sized disk for the expected 5xx volume.
	viper.SetDefault("logging.artifact_store.triggers.on_error", false)
	// Phase 2.B — auto-on-quality-flag: empty string = OFF by default.
	// Operators opt in via env var (LOGGING_ARTIFACT_STORE_TRIGGERS_
	// QUALITY_FLAG_THRESHOLD=warning, etc.) once they've sized disk for
	// the expected flag volume.
	viper.SetDefault("logging.artifact_store.triggers.quality_flag_threshold", "")
	// Phase 2.C — always-on knob: OFF by default everywhere. Operators opt
	// in via env var (LOGGING_ARTIFACT_STORE_TRIGGERS_ALWAYS=true) for the
	// duration of a debugging session. Shipping with this on by default
	// would fill the 5 GiB max_total_bytes cap fast in any environment
	// with non-trivial request volume.
	viper.SetDefault("logging.artifact_store.triggers.always", false)

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
	viper.SetDefault("market.yfinance.base_url", "https://query2.finance.yahoo.com")
	viper.SetDefault("market.yfinance.cookie_url", "https://fc.yahoo.com")
	viper.SetDefault("market.yfinance.crumb_url", "https://query2.finance.yahoo.com/v1/test/getcrumb")
	viper.SetDefault("market.yfinance.request_timeout", "30s")
	viper.SetDefault("market.yfinance.max_retries", 3)
	viper.SetDefault("market.yfinance.auth_ttl", "6h")

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

	// Layer B Phase 2: guidance-artifact fixture root. Empty = disabled (the
	// production default ⇒ absent path ⇒ byte-identical to the 4.7 engine, NF1).
	viper.SetDefault("valuation.guidance_root", "")

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

// applyLoggingEnvironmentDefaults sets environment-specific logging defaults.
//
// This is called AFTER viper.ReadInConfig so that values from the config file
// are already loaded. Because we use viper.SetDefault, these values have the
// lowest priority — explicit config-file keys and env-var overrides win.
//
// Rules:
//
//	development → format=console, file.enabled=true, level=debug, trace_calculations=true
//	staging     → format=json,    file.enabled=false, level=info,  trace_calculations=false
//	production  → format=json,    file.enabled=false, level=info,  trace_calculations=false
func applyLoggingEnvironmentDefaults() {
	env := viper.GetString("environment")

	switch env {
	case "staging", "production":
		viper.SetDefault("logging.format", "json")
		viper.SetDefault("logging.file.enabled", false)
		viper.SetDefault("logging.level", "info")
		viper.SetDefault("logging.trace_calculations", false)
		// Narrate stream is on in all environments by default; bundle store
		// is OFF in staging/prod to avoid surprise disk usage in production.
		viper.SetDefault("logging.narrate.enabled", true)
		viper.SetDefault("logging.artifact_store.enabled", false)
	default:
		// "development" and any unrecognised environment fall through to
		// developer-friendly defaults: coloured console output with file sink.
		viper.SetDefault("logging.format", "console")
		viper.SetDefault("logging.file.enabled", true)
		viper.SetDefault("logging.level", "debug")
		viper.SetDefault("logging.trace_calculations", true)
		viper.SetDefault("logging.narrate.enabled", true)
		viper.SetDefault("logging.artifact_store.enabled", true)
	}
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
