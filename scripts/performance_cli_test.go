package main

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIPerformanceAnalyzer_BasicUsage tests basic CLI functionality
func TestCLIPerformanceAnalyzer_BasicUsage(t *testing.T) {
	analyzer := NewCLIPerformanceAnalyzer()

	// Test configuration parsing
	config := CLIConfig{
		ResultsFile:         "testdata/test-results.json",
		OutputFile:          "testdata/test-analysis.json",
		ReportFile:          "testdata/test-report.md",
		RegressionThreshold: 0.20,
		CreateBaseline:      false,
		Detailed:            false,
	}

	err := analyzer.ValidateConfig(config)
	assert.NoError(t, err, "Should validate basic configuration")

	// Test invalid configuration
	invalidConfig := CLIConfig{
		ResultsFile: "", // Missing required field
	}

	err = analyzer.ValidateConfig(invalidConfig)
	assert.Error(t, err, "Should reject invalid configuration")
}

// TestCLIPerformanceAnalyzer_ResultsProcessing tests processing benchmark results
func TestCLIPerformanceAnalyzer_ResultsProcessing(t *testing.T) {
	analyzer := NewCLIPerformanceAnalyzer()

	// Create test results file
	testResults := []BenchmarkResult{
		{
			TestName:         "single_ticker_baseline",
			Timestamp:        time.Now(),
			Duration:         30 * time.Second,
			AvgLatency:       400 * time.Millisecond,
			P95Latency:       800 * time.Millisecond,
			ThroughputRPS:    25.0,
			ErrorRatePercent: 0.2,
			TotalRequests:    750,
			SuccessfulReqs:   748,
			FailedReqs:       2,
		},
		{
			TestName:         "health_check_performance",
			Timestamp:        time.Now(),
			Duration:         30 * time.Second,
			AvgLatency:       50 * time.Millisecond,
			P95Latency:       100 * time.Millisecond,
			ThroughputRPS:    60.0,
			ErrorRatePercent: 0.0,
			TotalRequests:    1800,
			SuccessfulReqs:   1800,
			FailedReqs:       0,
		},
	}

	// Write test results to file
	resultsFile := "testdata/cli-test-results.json"
	err := SaveMultipleResults(testResults, resultsFile)
	require.NoError(t, err, "Should save test results")
	defer os.Remove(resultsFile)

	// Test loading results
	loadedResults, err := analyzer.LoadResults(resultsFile)
	require.NoError(t, err, "Should load results successfully")
	assert.Len(t, loadedResults, 2, "Should load all results")
	assert.Equal(t, testResults[0].TestName, loadedResults[0].TestName)
}

// TestCLIPerformanceAnalyzer_BaselineComparison tests baseline comparison functionality
func TestCLIPerformanceAnalyzer_BaselineComparison(t *testing.T) {
	analyzer := NewCLIPerformanceAnalyzer()

	// Create baseline results
	baseline := BenchmarkResult{
		TestName:         "single_ticker_baseline",
		AvgLatency:       300 * time.Millisecond,
		P95Latency:       600 * time.Millisecond,
		ThroughputRPS:    30.0,
		ErrorRatePercent: 0.1,
	}

	// Create current results (with regression)
	current := BenchmarkResult{
		TestName:         "single_ticker_baseline",
		AvgLatency:       450 * time.Millisecond, // 50% increase (regression)
		P95Latency:       600 * time.Millisecond,
		ThroughputRPS:    20.0, // 33% decrease (regression)
		ErrorRatePercent: 0.1,
	}

	comparison := analyzer.CompareResults(baseline, current, 0.20)
	require.NotNil(t, comparison, "Should generate comparison")
	assert.True(t, comparison.HasRegression, "Should detect regression")
	assert.NotEmpty(t, comparison.Regressions, "Should have regression details")
}

// TestCLIPerformanceAnalyzer_ReportGeneration tests report generation
func TestCLIPerformanceAnalyzer_ReportGeneration(t *testing.T) {
	analyzer := NewCLIPerformanceAnalyzer()

	results := []BenchmarkResult{
		{
			TestName:         "comprehensive_test",
			Timestamp:        time.Now(),
			Duration:         60 * time.Second,
			AvgLatency:       350 * time.Millisecond,
			P95Latency:       700 * time.Millisecond,
			ThroughputRPS:    28.5,
			ErrorRatePercent: 0.3,
			TotalRequests:    1710,
			SuccessfulReqs:   1705,
			FailedReqs:       5,
		},
	}

	sla := SLAThresholds{
		MaxAvgLatencyMs:     500,
		MaxP95LatencyMs:     1000,
		MinThroughputRPS:    10,
		MaxErrorRatePercent: 1.0,
	}

	report := analyzer.GenerateMarkdownReport(results, sla, nil, false)
	require.NotEmpty(t, report, "Should generate report")

	// Verify report content
	assert.Contains(t, report, "# Performance Test Report", "Should include header")
	assert.Contains(t, report, "comprehensive_test", "Should include test name")
	assert.Contains(t, report, "✅", "Should show SLA compliance")
	assert.Contains(t, report, "28.5 RPS", "Should include throughput")
	assert.Contains(t, report, "350ms", "Should include latency")
}

// TestCLIPerformanceAnalyzer_MultipleScenarios tests handling multiple test scenarios
func TestCLIPerformanceAnalyzer_MultipleScenarios(t *testing.T) {
	analyzer := NewCLIPerformanceAnalyzer()

	results := []BenchmarkResult{
		{
			TestName:         "single_ticker_baseline",
			AvgLatency:       400 * time.Millisecond,
			ThroughputRPS:    25.0,
			ErrorRatePercent: 0.2,
		},
		{
			TestName:         "bulk_valuation_test",
			AvgLatency:       800 * time.Millisecond,
			ThroughputRPS:    12.0,
			ErrorRatePercent: 0.5,
		},
		{
			TestName:         "health_check_performance",
			AvgLatency:       50 * time.Millisecond,
			ThroughputRPS:    100.0,
			ErrorRatePercent: 0.0,
		},
	}

	summary := analyzer.GenerateSummaryAnalysis(results)
	require.NotNil(t, summary, "Should generate summary")

	assert.Equal(t, 3, summary.TotalScenarios, "Should count all scenarios")
	assert.Greater(t, summary.OverallThroughput, 0.0, "Should calculate overall throughput")
	assert.GreaterOrEqual(t, summary.AverageLatency, 0.0, "Should calculate average latency")
	assert.Contains(t, summary.ScenarioResults, "single_ticker_baseline", "Should include all scenarios")
}

// TestCLIPerformanceAnalyzer_ErrorHandling tests error handling scenarios
func TestCLIPerformanceAnalyzer_ErrorHandling(t *testing.T) {
	analyzer := NewCLIPerformanceAnalyzer()

	// Test missing results file
	_, err := analyzer.LoadResults("nonexistent-file.json")
	assert.Error(t, err, "Should handle missing results file")

	// Test invalid JSON
	invalidFile := "testdata/invalid-results.json"
	err = os.WriteFile(invalidFile, []byte(`{"invalid": json}`), 0644)
	require.NoError(t, err)
	defer os.Remove(invalidFile)

	_, err = analyzer.LoadResults(invalidFile)
	assert.Error(t, err, "Should handle invalid JSON")
}

// TestCLIConfig_Validation tests CLI configuration validation
func TestCLIConfig_Validation(t *testing.T) {
	tests := []struct {
		name        string
		config      CLIConfig
		expectValid bool
		description string
	}{
		{
			name: "valid_basic_config",
			config: CLIConfig{
				ResultsFile:         "results.json",
				OutputFile:          "analysis.json",
				ReportFile:          "report.md",
				RegressionThreshold: 0.20,
			},
			expectValid: true,
			description: "Should accept valid basic configuration",
		},
		{
			name: "missing_results_file",
			config: CLIConfig{
				OutputFile: "analysis.json",
			},
			expectValid: false,
			description: "Should require results file",
		},
		{
			name: "invalid_threshold",
			config: CLIConfig{
				ResultsFile:         "results.json",
				RegressionThreshold: -0.1, // Invalid negative threshold
			},
			expectValid: false,
			description: "Should reject negative threshold",
		},
		{
			name: "threshold_too_high",
			config: CLIConfig{
				ResultsFile:         "results.json",
				RegressionThreshold: 2.0, // > 100%
			},
			expectValid: false,
			description: "Should reject threshold over 100%",
		},
	}

	analyzer := NewCLIPerformanceAnalyzer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := analyzer.ValidateConfig(tt.config)
			if tt.expectValid {
				assert.NoError(t, err, tt.description)
			} else {
				assert.Error(t, err, tt.description)
			}
		})
	}
}

// TestCLIPerformanceAnalyzer_SLAEvaluation tests SLA evaluation for multiple scenarios
func TestCLIPerformanceAnalyzer_SLAEvaluation(t *testing.T) {
	analyzer := NewCLIPerformanceAnalyzer()

	results := []BenchmarkResult{
		{
			TestName:         "passing_test",
			AvgLatency:       300 * time.Millisecond,
			P95Latency:       600 * time.Millisecond,
			ThroughputRPS:    25.0,
			ErrorRatePercent: 0.1,
		},
		{
			TestName:         "failing_test",
			AvgLatency:       700 * time.Millisecond, // Exceeds 500ms limit
			P95Latency:       600 * time.Millisecond,
			ThroughputRPS:    8.0, // Below 10 RPS limit
			ErrorRatePercent: 2.0, // Above 1% limit
		},
	}

	sla := SLAThresholds{
		MaxAvgLatencyMs:     500,
		MaxP95LatencyMs:     1000,
		MinThroughputRPS:    10,
		MaxErrorRatePercent: 1.0,
	}

	evaluation := analyzer.EvaluateOverallSLA(results, sla)
	require.NotNil(t, evaluation, "Should generate SLA evaluation")

	assert.False(t, evaluation.Passed, "Should fail overall SLA due to failing_test")
	assert.NotEmpty(t, evaluation.Failures, "Should have failure details")
	assert.Equal(t, 1, evaluation.PassingScenarios, "Should count passing scenarios")
	assert.Equal(t, 1, evaluation.FailingScenarios, "Should count failing scenarios")
}
