package entities

import (
	"time"
)

// BenchmarkResult represents a performance benchmark result (moved from scripts for clean architecture)
type BenchmarkResult struct {
	TestName          string        `json:"test_name"`
	Timestamp         time.Time     `json:"timestamp"`
	Duration          time.Duration `json:"duration"`
	AvgLatency        time.Duration `json:"avg_latency"`
	P95Latency        time.Duration `json:"p95_latency"`
	ThroughputRPS     float64       `json:"throughput_rps"`
	ErrorRatePercent  float64       `json:"error_rate_percent"`
	TotalRequests     int           `json:"total_requests"`
	SuccessfulReqs    int           `json:"successful_reqs"`
	FailedReqs        int           `json:"failed_reqs"`
}

// AlertSeverity represents the severity level of an alert
type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "info"
	SeverityWarning  AlertSeverity = "warning"
	SeverityCritical AlertSeverity = "critical"
)

// AlertStatus represents the current status of an alert
type AlertStatus string

const (
	StatusActive       AlertStatus = "active"
	StatusAcknowledged AlertStatus = "acknowledged"
	StatusResolved     AlertStatus = "resolved"
	StatusSuppressed   AlertStatus = "suppressed"
)

// NotificationChannel represents a notification delivery channel
type NotificationChannel struct {
	ID        string                 `json:"id" yaml:"id"`
	Name      string                 `json:"name" yaml:"name"`
	Type      string                 `json:"type" yaml:"type"` // email, slack, webhook, pagerduty
	Config    map[string]interface{} `json:"config" yaml:"config"`
	Enabled   bool                   `json:"enabled" yaml:"enabled"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// AlertRule defines conditions for triggering alerts
type AlertRule struct {
	ID          string        `json:"id" yaml:"id"`
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description" yaml:"description"`
	Severity    AlertSeverity `json:"severity" yaml:"severity"`
	Enabled     bool          `json:"enabled" yaml:"enabled"`

	// Conditions for triggering the alert
	Conditions AlertConditions `json:"conditions" yaml:"conditions"`

	// Notification settings
	Channels []string `json:"channels" yaml:"channels"` // Channel IDs

	// Escalation settings
	EscalationPolicy *EscalationPolicy `json:"escalation_policy,omitempty" yaml:"escalation_policy,omitempty"`

	// Suppression settings
	SuppressionWindows []SuppressionWindow `json:"suppression_windows,omitempty" yaml:"suppression_windows,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AlertConditions defines the conditions that trigger an alert
type AlertConditions struct {
	// Performance thresholds
	LatencyThreshold    *ThresholdCondition `json:"latency_threshold,omitempty" yaml:"latency_threshold,omitempty"`
	ThroughputThreshold *ThresholdCondition `json:"throughput_threshold,omitempty" yaml:"throughput_threshold,omitempty"`
	ErrorRateThreshold  *ThresholdCondition `json:"error_rate_threshold,omitempty" yaml:"error_rate_threshold,omitempty"`

	// Regression detection
	RegressionDetection *RegressionCondition `json:"regression_detection,omitempty" yaml:"regression_detection,omitempty"`

	// Anomaly detection
	AnomalyDetection *AnomalyCondition `json:"anomaly_detection,omitempty" yaml:"anomaly_detection,omitempty"`

	// Trend analysis
	TrendAnalysis *TrendCondition `json:"trend_analysis,omitempty" yaml:"trend_analysis,omitempty"`
}

// ThresholdCondition represents a simple threshold-based condition
type ThresholdCondition struct {
	Operator    string  `json:"operator" yaml:"operator"` // gt, gte, lt, lte, eq, ne
	Value       float64 `json:"value" yaml:"value"`
	Duration    string  `json:"duration" yaml:"duration"`       // Duration threshold must be exceeded
	Consecutive int     `json:"consecutive" yaml:"consecutive"` // Consecutive violations required
}

// RegressionCondition represents conditions for regression detection
type RegressionCondition struct {
	Method          string  `json:"method" yaml:"method"`                     // statistical, threshold, trend
	Threshold       float64 `json:"threshold" yaml:"threshold"`               // Percentage change threshold
	ConfidenceLevel float64 `json:"confidence_level" yaml:"confidence_level"` // Statistical confidence level
	BaselinePeriod  string  `json:"baseline_period" yaml:"baseline_period"`   // Period for baseline calculation
	MinSampleSize   int     `json:"min_sample_size" yaml:"min_sample_size"`   // Minimum samples for statistical tests
	StatisticalTest string  `json:"statistical_test" yaml:"statistical_test"` // t-test, mann-whitney, etc.
}

// AnomalyCondition represents conditions for anomaly detection
type AnomalyCondition struct {
	Method        string  `json:"method" yaml:"method"`                 // zscore, iqr, isolation_forest
	Sensitivity   float64 `json:"sensitivity" yaml:"sensitivity"`       // Sensitivity level (1-10)
	WindowSize    int     `json:"window_size" yaml:"window_size"`       // Rolling window size for analysis
	MinDeviations float64 `json:"min_deviations" yaml:"min_deviations"` // Minimum standard deviations for anomaly
}

// TrendCondition represents conditions for trend analysis
type TrendCondition struct {
	Direction       string  `json:"direction" yaml:"direction"`               // increasing, decreasing, stable
	MinSlope        float64 `json:"min_slope" yaml:"min_slope"`               // Minimum slope for trend detection
	WindowSize      int     `json:"window_size" yaml:"window_size"`           // Window size for trend calculation
	ConfidenceLevel float64 `json:"confidence_level" yaml:"confidence_level"` // Confidence level for trend significance
}

// EscalationPolicy defines how alerts should be escalated
type EscalationPolicy struct {
	ID             string           `json:"id" yaml:"id"`
	Name           string           `json:"name" yaml:"name"`
	Steps          []EscalationStep `json:"steps" yaml:"steps"`
	MaxEscalations int              `json:"max_escalations" yaml:"max_escalations"`
}

// EscalationStep represents a step in the escalation process
type EscalationStep struct {
	Level    int      `json:"level" yaml:"level"`
	Delay    string   `json:"delay" yaml:"delay"`       // Duration before escalation
	Channels []string `json:"channels" yaml:"channels"` // Channel IDs for this escalation level
}

// SuppressionWindow defines when alerts should be suppressed
type SuppressionWindow struct {
	StartTime string   `json:"start_time" yaml:"start_time"` // Time in format "15:04"
	EndTime   string   `json:"end_time" yaml:"end_time"`     // Time in format "15:04"
	Days      []string `json:"days" yaml:"days"`             // Days of week (monday, tuesday, etc.)
	Timezone  string   `json:"timezone" yaml:"timezone"`     // Timezone for suppression window
}

// PerformanceAlert represents an active or historical alert
type PerformanceAlert struct {
	ID          string        `json:"id"`
	RuleID      string        `json:"rule_id"`
	RuleName    string        `json:"rule_name"`
	Severity    AlertSeverity `json:"severity"`
	Status      AlertStatus   `json:"status"`
	Message     string        `json:"message"`
	Description string        `json:"description"`

	// Context about the performance issue
	Context AlertContext `json:"context"`

	// Timing information
	TriggeredAt    time.Time  `json:"triggered_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`

	// Escalation tracking
	EscalationLevel int        `json:"escalation_level"`
	LastEscalatedAt *time.Time `json:"last_escalated_at,omitempty"`

	// Notification tracking
	NotificationsSent []NotificationLog `json:"notifications_sent"`

	// Metadata
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AlertContext provides context about the performance issue
type AlertContext struct {
	TestScenario     string  `json:"test_scenario"`
	Metric           string  `json:"metric"`
	CurrentValue     float64 `json:"current_value"`
	ThresholdValue   float64 `json:"threshold_value,omitempty"`
	BaselineValue    float64 `json:"baseline_value,omitempty"`
	PercentageChange float64 `json:"percentage_change,omitempty"`

	// Statistical context
	ConfidenceLevel float64 `json:"confidence_level,omitempty"`
	PValue          float64 `json:"p_value,omitempty"`

	// Performance data
	PerformanceData map[string]interface{} `json:"performance_data,omitempty"`

	// Recommendations
	Recommendations []string `json:"recommendations,omitempty"`
}

// NotificationLog tracks sent notifications
type NotificationLog struct {
	ID          string     `json:"id"`
	ChannelID   string     `json:"channel_id"`
	ChannelType string     `json:"channel_type"`
	Status      string     `json:"status"` // sent, failed, pending
	Error       string     `json:"error,omitempty"`
	SentAt      time.Time  `json:"sent_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`

	// Message content
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body,omitempty"`

	// Retry information
	RetryCount  int        `json:"retry_count"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
}

// TrendAnalysis represents performance trend analysis results
type TrendAnalysis struct {
	ID           string `json:"id"`
	TestScenario string `json:"test_scenario"`
	Metric       string `json:"metric"`

	// Trend statistics
	Slope     float64 `json:"slope"`
	Intercept float64 `json:"intercept"`
	RSquared  float64 `json:"r_squared"`
	PValue    float64 `json:"p_value"`

	// Trend interpretation
	Direction    string `json:"direction"`    // improving, degrading, stable
	Significance string `json:"significance"` // significant, not_significant

	// Data points
	DataPoints []TrendDataPoint `json:"data_points"`

	// Time range
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`

	// Forecast
	Forecast []ForecastPoint `json:"forecast,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// TrendDataPoint represents a single data point in trend analysis
type TrendDataPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// ForecastPoint represents a forecasted performance value
type ForecastPoint struct {
	Timestamp       time.Time `json:"timestamp"`
	PredictedValue  float64   `json:"predicted_value"`
	ConfidenceLower float64   `json:"confidence_lower"`
	ConfidenceUpper float64   `json:"confidence_upper"`
}

// AnomalyDetectionResult represents the result of anomaly detection
type AnomalyDetectionResult struct {
	ID           string `json:"id"`
	TestScenario string `json:"test_scenario"`
	Metric       string `json:"metric"`

	// Anomaly information
	IsAnomaly bool          `json:"is_anomaly"`
	Severity  AlertSeverity `json:"severity"`
	Score     float64       `json:"score"` // Anomaly score (higher = more anomalous)

	// Statistical details
	Value         float64 `json:"value"`
	ExpectedValue float64 `json:"expected_value"`
	Deviation     float64 `json:"deviation"`
	ZScore        float64 `json:"z_score,omitempty"`

	// Context
	WindowData []float64 `json:"window_data,omitempty"`
	Method     string    `json:"method"`

	DetectedAt time.Time `json:"detected_at"`
}
