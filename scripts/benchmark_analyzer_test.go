package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBenchmarkAnalyzer_SLACompliance tests SLA compliance checking
func TestBenchmarkAnalyzer_SLACompliance(t *testing.T) {
	analyzer := NewBenchmarkAnalyzer()

	sla := SLAThresholds{
		MaxAvgLatencyMs:     500,
		MaxP95LatencyMs:     1000,
		MinThroughputRPS:    10,
		MaxErrorRatePercent: 1.0,
	}

	tests := []struct {
		name            string
		result          BenchmarkResult
		expectCompliant bool
		description     string
	}{
		{
			name: "compliant_result",
			result: BenchmarkResult{
				AvgLatency:       400 * time.Millisecond,
				P95Latency:       850 * time.Millisecond,
				ThroughputRPS:    25.5,
				ErrorRatePercent: 0.2,
			},
			expectCompliant: true,
			description:     "Should pass SLA compliance when all metrics meet requirements",
		},
		{
			name: "non_compliant_latency",
			result: BenchmarkResult{
				AvgLatency:       600 * time.Millisecond, // Exceeds 500ms limit
				P95Latency:       850 * time.Millisecond,
				ThroughputRPS:    25.5,
				ErrorRatePercent: 0.2,
			},
			expectCompliant: false,
			description:     "Should fail SLA compliance when latency exceeds limit",
		},
		{
			name: "non_compliant_throughput",
			result: BenchmarkResult{
				AvgLatency:       400 * time.Millisecond,
				P95Latency:       850 * time.Millisecond,
				ThroughputRPS:    8.5, // Below 10 RPS requirement
				ErrorRatePercent: 0.2,
			},
			expectCompliant: false,
			description:     "Should fail SLA compliance when throughput is too low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluation := analyzer.EvaluateSLA(tt.result, sla)
			assert.Equal(t, tt.expectCompliant, evaluation.Passed, tt.description)

			if !tt.expectCompliant {
				assert.NotEmpty(t, evaluation.Failures, "Should have failure reasons")
			} else {
				assert.Empty(t, evaluation.Failures, "Should have no failures")
			}
		})
	}
}

// TestBenchmarkAnalyzer_BaselineComparison tests baseline comparison logic
func TestBenchmarkAnalyzer_BaselineComparison(t *testing.T) {
	analyzer := NewBenchmarkAnalyzer()

	baseline := BenchmarkResult{
		TestName:         "baseline_test",
		AvgLatency:       300 * time.Millisecond,
		P95Latency:       600 * time.Millisecond,
		ThroughputRPS:    20.0,
		ErrorRatePercent: 0.5,
	}

	tests := []struct {
		name                string
		current             BenchmarkResult
		expectRegression    bool
		expectedImprovement bool
		description         string
	}{
		{
			name: "no_significant_change",
			current: BenchmarkResult{
				AvgLatency:       310 * time.Millisecond, // +3.3% (acceptable)
				P95Latency:       620 * time.Millisecond, // +3.3%
				ThroughputRPS:    19.5,                   // -2.5%
				ErrorRatePercent: 0.6,                    // +20% but still low
			},
			expectRegression:    false,
			expectedImprovement: false,
			description:         "Should not detect regression for small changes",
		},
		{
			name: "performance_regression",
			current: BenchmarkResult{
				AvgLatency:       420 * time.Millisecond, // +40% increase (significant)
				P95Latency:       600 * time.Millisecond,
				ThroughputRPS:    14.0, // -30% decrease
				ErrorRatePercent: 0.5,
			},
			expectRegression:    true,
			expectedImprovement: false,
			description:         "Should detect regression when latency increases significantly",
		},
		{
			name: "performance_improvement",
			current: BenchmarkResult{
				AvgLatency:       200 * time.Millisecond, // -33% improvement
				P95Latency:       400 * time.Millisecond, // -33% improvement
				ThroughputRPS:    30.0,                   // +50% improvement
				ErrorRatePercent: 0.2,                    // -60% improvement
			},
			expectRegression:    false,
			expectedImprovement: true,
			description:         "Should detect improvement when metrics improve significantly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comparison := analyzer.CompareToBaseline(baseline, tt.current, 0.20) // 20% threshold

			assert.Equal(t, tt.expectRegression, comparison.HasRegression, tt.description)

			if tt.expectedImprovement {
				assert.NotEmpty(t, comparison.Improvements, "Should detect improvements")
			}

			if tt.expectRegression {
				assert.NotEmpty(t, comparison.Regressions, "Should have regression details")
			}
		})
	}
}

// TestBenchmarkAnalyzer_ReportGeneration tests report generation
func TestBenchmarkAnalyzer_ReportGeneration(t *testing.T) {
	analyzer := NewBenchmarkAnalyzer()

	result := BenchmarkResult{
		TestName:         "sample_test",
		Timestamp:        time.Now(),
		Duration:         60 * time.Second,
		AvgLatency:       450 * time.Millisecond,
		P95Latency:       800 * time.Millisecond,
		P99Latency:       1200 * time.Millisecond,
		MinLatency:       200 * time.Millisecond,
		MaxLatency:       2000 * time.Millisecond,
		ThroughputRPS:    18.5,
		ErrorRatePercent: 0.8,
		TotalRequests:    1110,
		SuccessfulReqs:   1101,
		FailedReqs:       9,
	}

	sla := SLAThresholds{
		MaxAvgLatencyMs:     500,
		MaxP95LatencyMs:     1000,
		MinThroughputRPS:    10,
		MaxErrorRatePercent: 1.0,
	}

	// Test summary report generation
	report := analyzer.GenerateSummaryReport(result, sla)
	require.NotNil(t, report, "Should generate summary report")

	assert.Equal(t, result.TestName, report.TestName)
	assert.Equal(t, result.TotalRequests, report.TotalRequests)
	assert.Equal(t, result.SuccessfulReqs, report.SuccessfulRequests)
	assert.Equal(t, result.FailedReqs, report.FailedRequests)
	assert.Equal(t, result.ThroughputRPS, report.ThroughputRPS)
	assert.Equal(t, result.ErrorRatePercent, report.ErrorRatePercent)

	// Verify SLA evaluation is included
	assert.NotNil(t, report.SLAEvaluation, "Should include SLA evaluation")
	assert.True(t, report.SLAEvaluation.Passed, "Should pass SLA (all metrics within limits)")

	// Test detailed report generation
	detailedReport := analyzer.GenerateDetailedReport(result, sla, nil)
	require.NotNil(t, detailedReport, "Should generate detailed report")

	assert.Contains(t, detailedReport.Summary, result.TestName, "Should include test name in summary")
	assert.Contains(t, detailedReport.Summary, "PASSED", "Should indicate SLA compliance")
	assert.NotEmpty(t, detailedReport.PerformanceMetrics, "Should include performance metrics")
	assert.NotEmpty(t, detailedReport.Recommendations, "Should include recommendations")
}

// TestBenchmarkAnalyzer_PerformanceRecommendations tests recommendation generation
func TestBenchmarkAnalyzer_PerformanceRecommendations(t *testing.T) {
	analyzer := NewBenchmarkAnalyzer()

	tests := []struct {
		name                    string
		result                  BenchmarkResult
		expectedRecommendations []string
		description             string
	}{
		{
			name: "high_latency_scenario",
			result: BenchmarkResult{
				AvgLatency:       800 * time.Millisecond, // High latency
				P95Latency:       1500 * time.Millisecond,
				ThroughputRPS:    15.0,
				ErrorRatePercent: 0.1,
			},
			expectedRecommendations: []string{
				"high average latency",
				"caching",
				"optimizing database",
			},
			description: "Should recommend latency optimization for high latency",
		},
		{
			name: "low_throughput_scenario",
			result: BenchmarkResult{
				AvgLatency:       200 * time.Millisecond,
				P95Latency:       400 * time.Millisecond,
				ThroughputRPS:    5.0, // Low throughput
				ErrorRatePercent: 0.1,
			},
			expectedRecommendations: []string{
				"low throughput",
				"concurrency",
				"load balancing",
			},
			description: "Should recommend throughput optimization for low throughput",
		},
		{
			name: "high_error_rate_scenario",
			result: BenchmarkResult{
				AvgLatency:       300 * time.Millisecond,
				P95Latency:       600 * time.Millisecond,
				ThroughputRPS:    20.0,
				ErrorRatePercent: 5.0, // High error rate
			},
			expectedRecommendations: []string{
				"error handling",
				"reliability",
				"validation",
			},
			description: "Should recommend error handling improvements for high error rate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations := analyzer.GenerateRecommendations(tt.result)
			require.NotEmpty(t, recommendations, tt.description)



			// Check that expected recommendation types are present
			for _, expected := range tt.expectedRecommendations {
				found := false
				for _, rec := range recommendations {
					if contains(rec, expected) {
						found = true
						break
					}
				}
				assert.True(t, found, fmt.Sprintf("Should include %s recommendation (looking for: %s)", expected, expected))
			}
		})
	}
}

// TestBenchmarkAnalyzer_TrendAnalysis tests trend analysis functionality
func TestBenchmarkAnalyzer_TrendAnalysis(t *testing.T) {
	analyzer := NewBenchmarkAnalyzer()

	// Create historical results showing performance degradation
	results := []BenchmarkResult{
		{
			TestName:         "test_run_1",
			Timestamp:        time.Now().Add(-72 * time.Hour),
			AvgLatency:       300 * time.Millisecond,
			ThroughputRPS:    25.0,
			ErrorRatePercent: 0.1,
		},
		{
			TestName:         "test_run_2",
			Timestamp:        time.Now().Add(-48 * time.Hour),
			AvgLatency:       350 * time.Millisecond,
			ThroughputRPS:    22.0,
			ErrorRatePercent: 0.2,
		},
		{
			TestName:         "test_run_3",
			Timestamp:        time.Now().Add(-24 * time.Hour),
			AvgLatency:       400 * time.Millisecond,
			ThroughputRPS:    20.0,
			ErrorRatePercent: 0.5,
		},
		{
			TestName:         "test_run_4",
			Timestamp:        time.Now(),
			AvgLatency:       450 * time.Millisecond,
			ThroughputRPS:    18.0,
			ErrorRatePercent: 0.8,
		},
	}

	trend := analyzer.AnalyzeTrend(results)
	require.NotNil(t, trend, "Should analyze trend")

	// Should detect degrading performance
	assert.Equal(t, "DEGRADING", trend.Direction, "Should detect degrading trend")
	assert.Greater(t, trend.LatencyTrend, 0.0, "Should show increasing latency trend")
	assert.Less(t, trend.ThroughputTrend, 0.0, "Should show decreasing throughput trend")
	assert.Greater(t, trend.ErrorRateTrend, 0.0, "Should show increasing error rate trend")
	assert.NotEmpty(t, trend.Insights, "Should provide trend insights")
}

// Helper function to check if a string contains a substring (case-insensitive)
func contains(str, substr string) bool {
	return strings.Contains(strings.ToLower(str), strings.ToLower(substr))
}
