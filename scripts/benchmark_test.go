package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBenchmarkConfig tests the benchmark configuration loading and validation
func TestBenchmarkConfig_LoadAndValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      BenchmarkConfig
		expectValid bool
		description string
	}{
		{
			name: "valid_baseline_config",
			config: BenchmarkConfig{
				BaseURL: "http://localhost:8080",
				APIKey:  "test-api-key-123",
				Scenarios: []TestScenario{
					{
						Name:           "single_ticker",
						TestType:       "single",
						Duration:       30 * time.Second,
						Concurrency:    5,
						RequestsPerSec: 10,
						Tickers:        []string{"AAPL"},
					},
				},
				SLAThresholds: SLAThresholds{
					MaxAvgLatencyMs:     500,  // 500ms as per user requirement
					MaxP95LatencyMs:     1000, // Conservative p95
					MinThroughputRPS:    10,   // 10 RPS baseline
					MaxErrorRatePercent: 1.0,  // 1% error rate
				},
			},
			expectValid: true,
			description: "Should accept valid baseline configuration",
		},
		{
			name: "invalid_negative_concurrency",
			config: BenchmarkConfig{
				BaseURL: "http://localhost:8080",
				APIKey:  "test-key",
				Scenarios: []TestScenario{
					{
						Name:        "invalid_test",
						Concurrency: -1, // Invalid
						Duration:    30 * time.Second,
					},
				},
			},
			expectValid: false,
			description: "Should reject negative concurrency",
		},
		{
			name: "invalid_empty_api_key",
			config: BenchmarkConfig{
				BaseURL: "http://localhost:8080",
				APIKey:  "", // Empty API key
				Scenarios: []TestScenario{
					{
						Name:        "test",
						Concurrency: 1,
						Duration:    10 * time.Second,
					},
				},
			},
			expectValid: false,
			description: "Should require API key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectValid {
				assert.NoError(t, err, tt.description)
			} else {
				assert.Error(t, err, tt.description)
			}
		})
	}
}

// TestSLAEvaluation tests SLA threshold evaluation logic
func TestSLAEvaluation_ValidateResults(t *testing.T) {
	sla := SLAThresholds{
		MaxAvgLatencyMs:     500,
		MaxP95LatencyMs:     1000,
		MinThroughputRPS:    10,
		MaxErrorRatePercent: 1.0,
	}

	tests := []struct {
		name        string
		result      BenchmarkResult
		expectPass  bool
		description string
	}{
		{
			name: "passing_result",
			result: BenchmarkResult{
				AvgLatency:       400 * time.Millisecond, // Under 500ms
				P95Latency:       800 * time.Millisecond, // Under 1000ms
				ThroughputRPS:    25.5,                   // Above 10 RPS
				ErrorRatePercent: 0.1,                    // Under 1%
			},
			expectPass:  true,
			description: "Should pass when all metrics meet SLA",
		},
		{
			name: "failing_latency",
			result: BenchmarkResult{
				AvgLatency:       600 * time.Millisecond, // Over 500ms limit
				P95Latency:       800 * time.Millisecond,
				ThroughputRPS:    25.5,
				ErrorRatePercent: 0.1,
			},
			expectPass:  false,
			description: "Should fail when average latency exceeds SLA",
		},
		{
			name: "failing_error_rate",
			result: BenchmarkResult{
				AvgLatency:       400 * time.Millisecond,
				P95Latency:       800 * time.Millisecond,
				ThroughputRPS:    25.5,
				ErrorRatePercent: 2.5, // Over 1% limit
			},
			expectPass:  false,
			description: "Should fail when error rate exceeds SLA",
		},
		{
			name: "failing_throughput",
			result: BenchmarkResult{
				AvgLatency:       400 * time.Millisecond,
				P95Latency:       800 * time.Millisecond,
				ThroughputRPS:    5.0, // Under 10 RPS requirement
				ErrorRatePercent: 0.1,
			},
			expectPass:  false,
			description: "Should fail when throughput below SLA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluation := sla.Evaluate(tt.result)
			if tt.expectPass {
				assert.True(t, evaluation.Passed, tt.description)
				assert.Empty(t, evaluation.Failures, "Should have no SLA failures")
			} else {
				assert.False(t, evaluation.Passed, tt.description)
				assert.NotEmpty(t, evaluation.Failures, "Should have SLA failures")
			}
		})
	}
}

// TestBaselineComparison tests performance baseline comparison logic
func TestBaselineComparison_DetectRegression(t *testing.T) {
	baseline := BenchmarkResult{
		AvgLatency:       400 * time.Millisecond,
		P95Latency:       800 * time.Millisecond,
		ThroughputRPS:    25.0,
		ErrorRatePercent: 0.1,
	}

	tests := []struct {
		name             string
		current          BenchmarkResult
		expectRegression bool
		description      string
	}{
		{
			name: "no_regression",
			current: BenchmarkResult{
				AvgLatency:       420 * time.Millisecond, // +5% increase (acceptable)
				P95Latency:       840 * time.Millisecond, // +5% increase
				ThroughputRPS:    24.0,                   // -4% decrease (acceptable)
				ErrorRatePercent: 0.2,                    // Doubled but still low
			},
			expectRegression: false,
			description:      "Should not detect regression for small changes",
		},
		{
			name: "latency_regression",
			current: BenchmarkResult{
				AvgLatency:       520 * time.Millisecond, // +30% increase (significant)
				P95Latency:       800 * time.Millisecond,
				ThroughputRPS:    25.0,
				ErrorRatePercent: 0.1,
			},
			expectRegression: true,
			description:      "Should detect regression when latency increases significantly",
		},
		{
			name: "throughput_regression",
			current: BenchmarkResult{
				AvgLatency:       400 * time.Millisecond,
				P95Latency:       800 * time.Millisecond,
				ThroughputRPS:    18.0, // -28% decrease (significant)
				ErrorRatePercent: 0.1,
			},
			expectRegression: true,
			description:      "Should detect regression when throughput drops significantly",
		},
	}

	// 20% regression threshold
	regressionThreshold := 0.20

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comparison := CompareToBaseline(baseline, tt.current, regressionThreshold)
			if tt.expectRegression {
				assert.True(t, comparison.HasRegression, tt.description)
				assert.NotEmpty(t, comparison.Regressions, "Should have regression details")
			} else {
				assert.False(t, comparison.HasRegression, tt.description)
			}
		})
	}
}

// TestBenchmarkResults_JSONSerialization tests that results can be saved and loaded properly
func TestBenchmarkResults_JSONSerialization(t *testing.T) {
	originalResult := BenchmarkResult{
		TestName:         "single_ticker_test",
		Timestamp:        time.Now(),
		Duration:         60 * time.Second,
		AvgLatency:       450 * time.Millisecond,
		P95Latency:       900 * time.Millisecond,
		P99Latency:       1200 * time.Millisecond,
		MinLatency:       100 * time.Millisecond,
		MaxLatency:       2000 * time.Millisecond,
		ThroughputRPS:    22.5,
		ErrorRatePercent: 0.3,
		TotalRequests:    1350,
		SuccessfulReqs:   1346,
		FailedReqs:       4,
		TestConfig: TestScenario{
			Name:           "single_ticker",
			TestType:       "single",
			Duration:       60 * time.Second,
			Concurrency:    10,
			RequestsPerSec: 20,
			Tickers:        []string{"AAPL"},
		},
	}

	// Test JSON marshaling
	jsonData, err := json.MarshalIndent(originalResult, "", "  ")
	require.NoError(t, err, "Should marshal benchmark result to JSON")

	// Test JSON unmarshaling
	var loadedResult BenchmarkResult
	err = json.Unmarshal(jsonData, &loadedResult)
	require.NoError(t, err, "Should unmarshal benchmark result from JSON")

	// Verify data integrity
	assert.Equal(t, originalResult.TestName, loadedResult.TestName)
	assert.Equal(t, originalResult.AvgLatency, loadedResult.AvgLatency)
	assert.Equal(t, originalResult.ThroughputRPS, loadedResult.ThroughputRPS)
	assert.Equal(t, originalResult.ErrorRatePercent, loadedResult.ErrorRatePercent)
	assert.Equal(t, originalResult.TestConfig.Name, loadedResult.TestConfig.Name)
}

// TestConfigFileLoading tests loading configuration from JSON files
func TestConfigFile_LoadFromJSON(t *testing.T) {
	// Create temporary config file
	tmpFile, err := os.CreateTemp("", "benchmark_config_*.json")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	testConfig := BenchmarkConfig{
		BaseURL: "http://localhost:8080",
		APIKey:  "test-key-123",
		Scenarios: []TestScenario{
			{
				Name:           "integration_test",
				TestType:       "single",
				Duration:       30 * time.Second,
				Concurrency:    5,
				RequestsPerSec: 15,
				Tickers:        []string{"AAPL", "MSFT"},
			},
		},
		SLAThresholds: SLAThresholds{
			MaxAvgLatencyMs:     500,
			MaxP95LatencyMs:     1000,
			MinThroughputRPS:    10,
			MaxErrorRatePercent: 1.0,
		},
	}

	// Write config to file
	configData, err := json.MarshalIndent(testConfig, "", "  ")
	require.NoError(t, err)

	_, err = tmpFile.Write(configData)
	require.NoError(t, err)
	tmpFile.Close()

	// Test loading config from file
	loadedConfig, err := LoadBenchmarkConfig(tmpFile.Name())
	require.NoError(t, err, "Should load config from JSON file")

	assert.Equal(t, testConfig.BaseURL, loadedConfig.BaseURL)
	assert.Equal(t, testConfig.APIKey, loadedConfig.APIKey)
	assert.Len(t, loadedConfig.Scenarios, 1)
	assert.Equal(t, testConfig.Scenarios[0].Name, loadedConfig.Scenarios[0].Name)
	assert.Equal(t, testConfig.SLAThresholds.MaxAvgLatencyMs, loadedConfig.SLAThresholds.MaxAvgLatencyMs)
}

// TestAPIKeyValidation tests API key format validation
func TestAPIKey_Validation(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		expectValid bool
		description string
	}{
		{
			name:        "valid_test_key",
			apiKey:      "test-api-key-12345",
			expectValid: true,
			description: "Should accept properly formatted test API key",
		},
		{
			name:        "empty_key",
			apiKey:      "",
			expectValid: false,
			description: "Should reject empty API key",
		},
		{
			name:        "whitespace_key",
			apiKey:      "   ",
			expectValid: false,
			description: "Should reject whitespace-only API key",
		},
		{
			name:        "minimum_length_key",
			apiKey:      "test-key-1",
			expectValid: true,
			description: "Should accept minimum length API key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := ValidateAPIKey(tt.apiKey)
			assert.Equal(t, tt.expectValid, isValid, tt.description)
		})
	}
}
