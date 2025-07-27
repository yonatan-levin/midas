package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// BenchmarkConfig holds the configuration for performance benchmarks
type BenchmarkConfig struct {
	BaseURL       string         `json:"base_url"`
	APIKey        string         `json:"api_key"`
	Scenarios     []TestScenario `json:"scenarios"`
	SLAThresholds SLAThresholds  `json:"sla_thresholds"`
}

// TestScenario defines a specific performance test scenario
type TestScenario struct {
	Name           string        `json:"name"`
	TestType       string        `json:"test_type"` // "single", "bulk", "mixed"
	Duration       time.Duration `json:"duration"`
	Concurrency    int           `json:"concurrency"`
	RequestsPerSec int           `json:"requests_per_sec"`
	Tickers        []string      `json:"tickers"`
}

// SLAThresholds defines the performance SLA requirements
type SLAThresholds struct {
	MaxAvgLatencyMs     int     `json:"max_avg_latency_ms"`
	MaxP95LatencyMs     int     `json:"max_p95_latency_ms"`
	MinThroughputRPS    float64 `json:"min_throughput_rps"`
	MaxErrorRatePercent float64 `json:"max_error_rate_percent"`
}

// BenchmarkResult holds the results of a performance benchmark
type BenchmarkResult struct {
	TestName         string        `json:"test_name"`
	Timestamp        time.Time     `json:"timestamp"`
	Duration         time.Duration `json:"duration"`
	AvgLatency       time.Duration `json:"avg_latency"`
	P95Latency       time.Duration `json:"p95_latency"`
	P99Latency       time.Duration `json:"p99_latency"`
	MinLatency       time.Duration `json:"min_latency"`
	MaxLatency       time.Duration `json:"max_latency"`
	ThroughputRPS    float64       `json:"throughput_rps"`
	ErrorRatePercent float64       `json:"error_rate_percent"`
	TotalRequests    int64         `json:"total_requests"`
	SuccessfulReqs   int64         `json:"successful_requests"`
	FailedReqs       int64         `json:"failed_requests"`
	TestConfig       TestScenario  `json:"test_config"`
}

// SLAEvaluation holds the results of SLA evaluation
type SLAEvaluation struct {
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures"`
}

// BaselineComparison holds the results of baseline comparison
type BaselineComparison struct {
	HasRegression bool                 `json:"has_regression"`
	Regressions   []RegressionDetails  `json:"regressions"`
	Improvements  []ImprovementDetails `json:"improvements"`
}

// RegressionDetails describes a performance regression
type RegressionDetails struct {
	Metric        string  `json:"metric"`
	BaselineVal   float64 `json:"baseline_value"`
	CurrentVal    float64 `json:"current_value"`
	ChangePercent float64 `json:"change_percent"`
	Description   string  `json:"description"`
}

// ImprovementDetails describes a performance improvement
type ImprovementDetails struct {
	Metric        string  `json:"metric"`
	BaselineVal   float64 `json:"baseline_value"`
	CurrentVal    float64 `json:"current_value"`
	ChangePercent float64 `json:"change_percent"`
	Description   string  `json:"description"`
}

// Validate validates the benchmark configuration
func (bc *BenchmarkConfig) Validate() error {
	if bc.BaseURL == "" {
		return fmt.Errorf("base_url is required")
	}

	if strings.TrimSpace(bc.APIKey) == "" {
		return fmt.Errorf("api_key is required")
	}

	if len(bc.Scenarios) == 0 {
		return fmt.Errorf("at least one test scenario is required")
	}

	for i, scenario := range bc.Scenarios {
		if err := scenario.Validate(); err != nil {
			return fmt.Errorf("scenario %d (%s): %w", i, scenario.Name, err)
		}
	}

	return bc.SLAThresholds.Validate()
}

// Validate validates a test scenario
func (ts *TestScenario) Validate() error {
	if ts.Name == "" {
		return fmt.Errorf("scenario name is required")
	}

	if ts.TestType == "" {
		return fmt.Errorf("test_type is required")
	}

	if ts.Duration <= 0 {
		return fmt.Errorf("duration must be positive")
	}

	if ts.Concurrency <= 0 {
		return fmt.Errorf("concurrency must be positive")
	}

	if ts.RequestsPerSec <= 0 {
		return fmt.Errorf("requests_per_sec must be positive")
	}

	if len(ts.Tickers) == 0 {
		return fmt.Errorf("at least one ticker is required")
	}

	return nil
}

// Validate validates SLA thresholds
func (sla *SLAThresholds) Validate() error {
	if sla.MaxAvgLatencyMs <= 0 {
		return fmt.Errorf("max_avg_latency_ms must be positive")
	}

	if sla.MaxP95LatencyMs <= 0 {
		return fmt.Errorf("max_p95_latency_ms must be positive")
	}

	if sla.MinThroughputRPS <= 0 {
		return fmt.Errorf("min_throughput_rps must be positive")
	}

	if sla.MaxErrorRatePercent < 0 || sla.MaxErrorRatePercent > 100 {
		return fmt.Errorf("max_error_rate_percent must be between 0 and 100")
	}

	return nil
}

// Evaluate evaluates a benchmark result against SLA thresholds
func (sla *SLAThresholds) Evaluate(result BenchmarkResult) SLAEvaluation {
	var failures []string

	// Check average latency
	avgLatencyMs := float64(result.AvgLatency.Milliseconds())
	if avgLatencyMs > float64(sla.MaxAvgLatencyMs) {
		failures = append(failures, fmt.Sprintf(
			"Average latency %.1fms exceeds SLA limit of %dms",
			avgLatencyMs, sla.MaxAvgLatencyMs))
	}

	// Check P95 latency
	p95LatencyMs := float64(result.P95Latency.Milliseconds())
	if p95LatencyMs > float64(sla.MaxP95LatencyMs) {
		failures = append(failures, fmt.Sprintf(
			"P95 latency %.1fms exceeds SLA limit of %dms",
			p95LatencyMs, sla.MaxP95LatencyMs))
	}

	// Check throughput
	if result.ThroughputRPS < sla.MinThroughputRPS {
		failures = append(failures, fmt.Sprintf(
			"Throughput %.1f RPS below SLA minimum of %.1f RPS",
			result.ThroughputRPS, sla.MinThroughputRPS))
	}

	// Check error rate
	if result.ErrorRatePercent > sla.MaxErrorRatePercent {
		failures = append(failures, fmt.Sprintf(
			"Error rate %.2f%% exceeds SLA limit of %.2f%%",
			result.ErrorRatePercent, sla.MaxErrorRatePercent))
	}

	return SLAEvaluation{
		Passed:   len(failures) == 0,
		Failures: failures,
	}
}

// CompareToBaseline compares current results to baseline and detects regressions
func CompareToBaseline(baseline, current BenchmarkResult, threshold float64) BaselineComparison {
	var regressions []RegressionDetails
	var improvements []ImprovementDetails

	// Compare average latency (higher is worse)
	latencyChange := calculatePercentChange(
		float64(baseline.AvgLatency.Milliseconds()),
		float64(current.AvgLatency.Milliseconds()))

	if latencyChange > threshold {
		regressions = append(regressions, RegressionDetails{
			Metric:        "avg_latency",
			BaselineVal:   float64(baseline.AvgLatency.Milliseconds()),
			CurrentVal:    float64(current.AvgLatency.Milliseconds()),
			ChangePercent: latencyChange,
			Description:   fmt.Sprintf("Average latency increased by %.1f%%", latencyChange*100),
		})
	} else if latencyChange < -threshold {
		improvements = append(improvements, ImprovementDetails{
			Metric:        "avg_latency",
			BaselineVal:   float64(baseline.AvgLatency.Milliseconds()),
			CurrentVal:    float64(current.AvgLatency.Milliseconds()),
			ChangePercent: latencyChange,
			Description:   fmt.Sprintf("Average latency improved by %.1f%%", math.Abs(latencyChange*100)),
		})
	}

	// Compare throughput (lower is worse)
	throughputChange := calculatePercentChange(baseline.ThroughputRPS, current.ThroughputRPS)

	if throughputChange < -threshold {
		regressions = append(regressions, RegressionDetails{
			Metric:        "throughput",
			BaselineVal:   baseline.ThroughputRPS,
			CurrentVal:    current.ThroughputRPS,
			ChangePercent: throughputChange,
			Description:   fmt.Sprintf("Throughput decreased by %.1f%%", math.Abs(throughputChange*100)),
		})
	} else if throughputChange > threshold {
		improvements = append(improvements, ImprovementDetails{
			Metric:        "throughput",
			BaselineVal:   baseline.ThroughputRPS,
			CurrentVal:    current.ThroughputRPS,
			ChangePercent: throughputChange,
			Description:   fmt.Sprintf("Throughput improved by %.1f%%", throughputChange*100),
		})
	}

	// Compare error rate (higher is worse)
	errorRateChange := calculatePercentChange(baseline.ErrorRatePercent, current.ErrorRatePercent)

	// Only flag error rate regression if both the percentage change is significant AND the absolute error rate is concerning
	if errorRateChange > threshold && current.ErrorRatePercent > 1.0 { // Only flag if error rate is > 1% (meaningful)
		regressions = append(regressions, RegressionDetails{
			Metric:        "error_rate",
			BaselineVal:   baseline.ErrorRatePercent,
			CurrentVal:    current.ErrorRatePercent,
			ChangePercent: errorRateChange,
			Description:   fmt.Sprintf("Error rate increased by %.1f%%", errorRateChange*100),
		})
	}

	return BaselineComparison{
		HasRegression: len(regressions) > 0,
		Regressions:   regressions,
		Improvements:  improvements,
	}
}

// calculatePercentChange calculates the percentage change from baseline to current
func calculatePercentChange(baseline, current float64) float64 {
	if baseline == 0 {
		if current == 0 {
			return 0
		}
		return 1.0 // 100% change
	}
	return (current - baseline) / baseline
}

// LoadBenchmarkConfig loads benchmark configuration from a JSON file
func LoadBenchmarkConfig(filename string) (*BenchmarkConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config BenchmarkConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &config, nil
}

// ValidateAPIKey validates API key format and content
func ValidateAPIKey(apiKey string) bool {
	trimmed := strings.TrimSpace(apiKey)
	return trimmed != "" && len(trimmed) >= 5 // Minimum reasonable API key length
}

// SaveBenchmarkResult saves benchmark results to a JSON file
func SaveBenchmarkResult(result BenchmarkResult, filename string) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write result file: %w", err)
	}

	return nil
}

// LoadBenchmarkResult loads benchmark results from a JSON file
func LoadBenchmarkResult(filename string) (*BenchmarkResult, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read result file: %w", err)
	}

	var result BenchmarkResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse result JSON: %w", err)
	}

	return &result, nil
}
