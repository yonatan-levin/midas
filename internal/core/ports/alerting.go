package ports

import (
	"context"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// AlertRepository defines the interface for alert data persistence
type AlertRepository interface {
	// Alert CRUD operations
	CreateAlert(ctx context.Context, alert *entities.PerformanceAlert) error
	GetAlert(ctx context.Context, id string) (*entities.PerformanceAlert, error)
	UpdateAlert(ctx context.Context, alert *entities.PerformanceAlert) error
	DeleteAlert(ctx context.Context, id string) error

	// Alert queries
	ListActiveAlerts(ctx context.Context) ([]*entities.PerformanceAlert, error)
	ListAlertsByRule(ctx context.Context, ruleID string) ([]*entities.PerformanceAlert, error)
	ListAlertsByStatus(ctx context.Context, status entities.AlertStatus) ([]*entities.PerformanceAlert, error)
	ListAlertsInTimeRange(ctx context.Context, start, end time.Time) ([]*entities.PerformanceAlert, error)

	// Alert rule CRUD operations
	CreateAlertRule(ctx context.Context, rule *entities.AlertRule) error
	GetAlertRule(ctx context.Context, id string) (*entities.AlertRule, error)
	UpdateAlertRule(ctx context.Context, rule *entities.AlertRule) error
	DeleteAlertRule(ctx context.Context, id string) error
	ListAlertRules(ctx context.Context, enabled bool) ([]*entities.AlertRule, error)

	// Notification channel CRUD operations
	CreateNotificationChannel(ctx context.Context, channel *entities.NotificationChannel) error
	GetNotificationChannel(ctx context.Context, id string) (*entities.NotificationChannel, error)
	UpdateNotificationChannel(ctx context.Context, channel *entities.NotificationChannel) error
	DeleteNotificationChannel(ctx context.Context, id string) error
	ListNotificationChannels(ctx context.Context, enabled bool) ([]*entities.NotificationChannel, error)

	// Trend analysis operations
	SaveTrendAnalysis(ctx context.Context, analysis *entities.TrendAnalysis) error
	GetTrendAnalysis(ctx context.Context, id string) (*entities.TrendAnalysis, error)
	ListTrendAnalyses(ctx context.Context, scenario string, metric string, limit int) ([]*entities.TrendAnalysis, error)

	// Anomaly detection operations
	SaveAnomalyDetectionResult(ctx context.Context, result *entities.AnomalyDetectionResult) error
	GetAnomalyDetectionResult(ctx context.Context, id string) (*entities.AnomalyDetectionResult, error)
	ListAnomalies(ctx context.Context, scenario string, metric string, limit int) ([]*entities.AnomalyDetectionResult, error)
}

// RegressionDetectionService defines the interface for enhanced regression detection
type RegressionDetectionService interface {
	// Statistical regression detection
	DetectStatisticalRegression(ctx context.Context, baseline, current []entities.BenchmarkResult, config entities.RegressionCondition) (*RegressionAnalysis, error)

	// Trend-based regression detection
	DetectTrendRegression(ctx context.Context, historicalData []entities.BenchmarkResult, config entities.RegressionCondition) (*RegressionAnalysis, error)

	// Comparative analysis between datasets
	ComparePerformanceDatasets(ctx context.Context, baseline, current []entities.BenchmarkResult, confidenceLevel float64) (*ComparisonResult, error)

	// Calculate statistical significance
	CalculateStatisticalSignificance(ctx context.Context, data1, data2 []float64, testType string) (*StatisticalTestResult, error)
}

// TrendAnalysisService defines the interface for performance trend analysis
type TrendAnalysisService interface {
	// Analyze performance trends
	AnalyzeTrend(ctx context.Context, data []entities.BenchmarkResult, metric string, windowSize int) (*entities.TrendAnalysis, error)

	// Forecast future performance
	ForecastPerformance(ctx context.Context, historicalData []entities.BenchmarkResult, metric string, forecastPeriods int) ([]entities.ForecastPoint, error)

	// Detect seasonal patterns
	DetectSeasonality(ctx context.Context, data []entities.BenchmarkResult, metric string) (*SeasonalityResult, error)

	// Calculate trend strength and direction
	CalculateTrendStrength(ctx context.Context, data []float64) (*TrendStrengthResult, error)
}

// AnomalyDetectionService defines the interface for anomaly detection
type AnomalyDetectionService interface {
	// Detect anomalies using various methods
	DetectAnomalies(ctx context.Context, data []entities.BenchmarkResult, metric string, config entities.AnomalyCondition) ([]*entities.AnomalyDetectionResult, error)

	// Z-score based anomaly detection
	DetectZScoreAnomalies(ctx context.Context, data []float64, threshold float64) ([]AnomalyPoint, error)

	// IQR based anomaly detection
	DetectIQRAnomalies(ctx context.Context, data []float64, multiplier float64) ([]AnomalyPoint, error)

	// Rolling window anomaly detection
	DetectRollingAnomalies(ctx context.Context, data []float64, windowSize int, sensitivity float64) ([]AnomalyPoint, error)
}

// AlertService defines the interface for alert management
type AlertService interface {
	// Evaluate alert rules against performance data
	EvaluateAlertRules(ctx context.Context, performanceData []entities.BenchmarkResult) ([]*entities.PerformanceAlert, error)

	// Create and manage alerts
	CreateAlert(ctx context.Context, ruleID string, context entities.AlertContext) (*entities.PerformanceAlert, error)
	AcknowledgeAlert(ctx context.Context, alertID string, acknowledgedBy string) error
	ResolveAlert(ctx context.Context, alertID string, resolvedBy string) error

	// Alert rule management
	ValidateAlertRule(ctx context.Context, rule *entities.AlertRule) error
	TestAlertRule(ctx context.Context, rule *entities.AlertRule, testData []entities.BenchmarkResult) (*AlertRuleTestResult, error)

	// Escalation management
	ProcessEscalations(ctx context.Context) error
	EscalateAlert(ctx context.Context, alertID string) error

	// Suppression management
	SuppressAlert(ctx context.Context, alertID string, duration time.Duration, reason string) error
	CheckSuppressionWindow(ctx context.Context, rule *entities.AlertRule, timestamp time.Time) (bool, error)
}

// NotificationService defines the interface for sending notifications
type NotificationService interface {
	// Send notifications through various channels
	SendNotification(ctx context.Context, alert *entities.PerformanceAlert, channel *entities.NotificationChannel) (*entities.NotificationLog, error)

	// Send bulk notifications
	SendBulkNotifications(ctx context.Context, alert *entities.PerformanceAlert, channels []*entities.NotificationChannel) ([]*entities.NotificationLog, error)

	// Retry failed notifications
	RetryFailedNotifications(ctx context.Context) error

	// Test notification channels
	TestNotificationChannel(ctx context.Context, channel *entities.NotificationChannel) error

	// Template management
	RenderNotificationTemplate(ctx context.Context, alert *entities.PerformanceAlert, templateType string) (string, string, error) // subject, body, error
}

// NotificationChannel defines the interface for specific notification channels
type NotificationChannel interface {
	// Send notification through this channel
	Send(ctx context.Context, subject, body string, config map[string]interface{}) error

	// Test the channel configuration
	Test(ctx context.Context, config map[string]interface{}) error

	// Get channel type
	GetType() string

	// Validate channel configuration
	ValidateConfig(config map[string]interface{}) error
}

// PrometheusExporter defines the interface for exporting metrics to Prometheus
type PrometheusExporter interface {
	// Export performance metrics
	ExportPerformanceMetrics(ctx context.Context, results []entities.BenchmarkResult) error

	// Export alert metrics
	ExportAlertMetrics(ctx context.Context, alerts []*entities.PerformanceAlert) error

	// Export trend analysis metrics
	ExportTrendMetrics(ctx context.Context, trends []*entities.TrendAnalysis) error

	// Register custom metrics
	RegisterCustomMetric(name, help string, labels []string) error

	// Update metric values
	UpdateMetric(name string, value float64, labels map[string]string) error
}

// ConfigurationLoader defines the interface for loading alert configurations
type ConfigurationLoader interface {
	// Load alert rules from configuration
	LoadAlertRules(ctx context.Context, configPath string) ([]*entities.AlertRule, error)

	// Load notification channels from configuration
	LoadNotificationChannels(ctx context.Context, configPath string) ([]*entities.NotificationChannel, error)

	// Load escalation policies from configuration
	LoadEscalationPolicies(ctx context.Context, configPath string) ([]*entities.EscalationPolicy, error)

	// Validate configuration files
	ValidateConfiguration(ctx context.Context, configPath string) error

	// Watch for configuration changes
	WatchConfiguration(ctx context.Context, configPath string) (<-chan ConfigurationChange, error)
}

// Supporting types for interfaces

// RegressionAnalysis represents the result of regression detection
type RegressionAnalysis struct {
	HasRegression   bool                   `json:"has_regression"`
	Severity        entities.AlertSeverity `json:"severity"`
	Method          string                 `json:"method"`
	ConfidenceLevel float64                `json:"confidence_level"`
	PValue          float64                `json:"p_value,omitempty"`
	EffectSize      float64                `json:"effect_size,omitempty"`
	Details         map[string]interface{} `json:"details"`
	Recommendations []string               `json:"recommendations"`
}

// ComparisonResult represents the result of comparing two performance datasets
type ComparisonResult struct {
	StatisticallySignificant bool               `json:"statistically_significant"`
	PValue                   float64            `json:"p_value"`
	EffectSize               float64            `json:"effect_size"`
	ConfidenceInterval       ConfidenceInterval `json:"confidence_interval"`
	TestStatistic            float64            `json:"test_statistic"`
	TestType                 string             `json:"test_type"`
	Summary                  string             `json:"summary"`
}

// StatisticalTestResult represents the result of a statistical test
type StatisticalTestResult struct {
	TestType      string         `json:"test_type"`
	Statistic     float64        `json:"statistic"`
	PValue        float64        `json:"p_value"`
	IsSignificant bool           `json:"is_significant"`
	EffectSize    float64        `json:"effect_size,omitempty"`
	PowerAnalysis *PowerAnalysis `json:"power_analysis,omitempty"`
}

// ConfidenceInterval represents a statistical confidence interval
type ConfidenceInterval struct {
	Lower      float64 `json:"lower"`
	Upper      float64 `json:"upper"`
	Confidence float64 `json:"confidence"`
}

// PowerAnalysis represents statistical power analysis results
type PowerAnalysis struct {
	Power      float64 `json:"power"`
	SampleSize int     `json:"sample_size"`
	EffectSize float64 `json:"effect_size"`
	Alpha      float64 `json:"alpha"`
}

// SeasonalityResult represents the result of seasonality detection
type SeasonalityResult struct {
	HasSeasonality    bool      `json:"has_seasonality"`
	Period            int       `json:"period,omitempty"`
	Strength          float64   `json:"strength,omitempty"`
	SeasonalFactors   []float64 `json:"seasonal_factors,omitempty"`
	TrendComponent    []float64 `json:"trend_component,omitempty"`
	ResidualComponent []float64 `json:"residual_component,omitempty"`
}

// TrendStrengthResult represents the result of trend strength calculation
type TrendStrengthResult struct {
	Direction    string  `json:"direction"`    // increasing, decreasing, stable
	Strength     float64 `json:"strength"`     // 0-1 scale
	Significance float64 `json:"significance"` // p-value
	R2           float64 `json:"r2"`           // coefficient of determination
	Slope        float64 `json:"slope"`
	Intercept    float64 `json:"intercept"`
}

// AnomalyPoint represents a single anomaly detection result
type AnomalyPoint struct {
	Index     int                    `json:"index"`
	Value     float64                `json:"value"`
	Expected  float64                `json:"expected,omitempty"`
	Score     float64                `json:"score"`
	Severity  entities.AlertSeverity `json:"severity"`
	Timestamp time.Time              `json:"timestamp,omitempty"`
}

// AlertRuleTestResult represents the result of testing an alert rule
type AlertRuleTestResult struct {
	Triggered       bool                       `json:"triggered"`
	TriggerCount    int                        `json:"trigger_count"`
	TestData        []entities.BenchmarkResult `json:"test_data"`
	Violations      []AlertViolation           `json:"violations,omitempty"`
	Recommendations []string                   `json:"recommendations,omitempty"`
}

// AlertViolation represents a single alert rule violation
type AlertViolation struct {
	Timestamp time.Time              `json:"timestamp"`
	Metric    string                 `json:"metric"`
	Value     float64                `json:"value"`
	Threshold float64                `json:"threshold,omitempty"`
	Condition string                 `json:"condition"`
	Severity  entities.AlertSeverity `json:"severity"`
}

// ConfigurationChange represents a change in configuration
type ConfigurationChange struct {
	Type      string    `json:"type"` // created, updated, deleted
	Path      string    `json:"path"`
	Timestamp time.Time `json:"timestamp"`
	Content   []byte    `json:"content,omitempty"`
}
