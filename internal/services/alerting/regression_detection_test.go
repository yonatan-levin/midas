package alerting

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestEnhancedRegressionDetectionService_StatisticalRegression tests statistical regression detection
func TestEnhancedRegressionDetectionService_StatisticalRegression(t *testing.T) {
	service := NewEnhancedRegressionDetectionService()
	ctx := context.Background()

	tests := []struct {
		name               string
		baseline           []entities.BenchmarkResult
		current            []entities.BenchmarkResult
		config             entities.RegressionCondition
		expectedRegression bool
		expectedSeverity   entities.AlertSeverity
		description        string
	}{
		{
			name: "no_regression_similar_performance",
			baseline: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 300*time.Millisecond, 25.0, 0.1),
				createTestBenchmarkResult("test2", 310*time.Millisecond, 24.5, 0.2),
				createTestBenchmarkResult("test3", 295*time.Millisecond, 25.5, 0.1),
				createTestBenchmarkResult("test4", 305*time.Millisecond, 24.8, 0.15),
				createTestBenchmarkResult("test5", 290*time.Millisecond, 25.2, 0.1),
			},
			current: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 298*time.Millisecond, 25.1, 0.12),
				createTestBenchmarkResult("test2", 315*time.Millisecond, 24.3, 0.18),
				createTestBenchmarkResult("test3", 302*time.Millisecond, 25.3, 0.11),
				createTestBenchmarkResult("test4", 292*time.Millisecond, 25.0, 0.14),
				createTestBenchmarkResult("test5", 308*time.Millisecond, 24.9, 0.13),
			},
			config: entities.RegressionCondition{
				Method:          "statistical",
				Threshold:       0.20,
				ConfidenceLevel: 0.95,
				StatisticalTest: "t-test",
				MinSampleSize:   5,
			},
			expectedRegression: false,
			expectedSeverity:   entities.SeverityInfo,
			description:        "Should not detect regression with similar performance",
		},
		{
			name: "latency_regression_detected",
			baseline: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 300*time.Millisecond, 25.0, 0.1),
				createTestBenchmarkResult("test2", 310*time.Millisecond, 24.5, 0.2),
				createTestBenchmarkResult("test3", 295*time.Millisecond, 25.5, 0.1),
				createTestBenchmarkResult("test4", 305*time.Millisecond, 24.8, 0.15),
				createTestBenchmarkResult("test5", 290*time.Millisecond, 25.2, 0.1),
			},
			current: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 450*time.Millisecond, 25.1, 0.12), // 50% increase
				createTestBenchmarkResult("test2", 465*time.Millisecond, 24.3, 0.18), // 50% increase
				createTestBenchmarkResult("test3", 442*time.Millisecond, 25.3, 0.11), // 50% increase
				createTestBenchmarkResult("test4", 458*time.Millisecond, 25.0, 0.14), // 50% increase
				createTestBenchmarkResult("test5", 435*time.Millisecond, 24.9, 0.13), // 50% increase
			},
			config: entities.RegressionCondition{
				Method:          "statistical",
				Threshold:       0.20,
				ConfidenceLevel: 0.95,
				StatisticalTest: "t-test",
				MinSampleSize:   5,
			},
			expectedRegression: true,
			expectedSeverity:   entities.SeverityCritical,
			description:        "Should detect significant latency regression",
		},
		{
			name: "throughput_regression_detected",
			baseline: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 300*time.Millisecond, 25.0, 0.1),
				createTestBenchmarkResult("test2", 310*time.Millisecond, 24.5, 0.2),
				createTestBenchmarkResult("test3", 295*time.Millisecond, 25.5, 0.1),
				createTestBenchmarkResult("test4", 305*time.Millisecond, 24.8, 0.15),
				createTestBenchmarkResult("test5", 290*time.Millisecond, 25.2, 0.1),
			},
			current: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 300*time.Millisecond, 15.0, 0.12), // 40% decrease
				createTestBenchmarkResult("test2", 310*time.Millisecond, 14.7, 0.18), // 40% decrease
				createTestBenchmarkResult("test3", 295*time.Millisecond, 15.3, 0.11), // 40% decrease
				createTestBenchmarkResult("test4", 305*time.Millisecond, 14.9, 0.14), // 40% decrease
				createTestBenchmarkResult("test5", 290*time.Millisecond, 15.1, 0.13), // 40% decrease
			},
			config: entities.RegressionCondition{
				Method:          "statistical",
				Threshold:       0.20,
				ConfidenceLevel: 0.95,
				StatisticalTest: "t-test",
				MinSampleSize:   5,
			},
			expectedRegression: true,
			expectedSeverity:   entities.SeverityCritical,
			description:        "Should detect significant throughput regression",
		},
		{
			name: "error_rate_regression_detected",
			baseline: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 300*time.Millisecond, 25.0, 0.1),
				createTestBenchmarkResult("test2", 310*time.Millisecond, 24.5, 0.2),
				createTestBenchmarkResult("test3", 295*time.Millisecond, 25.5, 0.1),
				createTestBenchmarkResult("test4", 305*time.Millisecond, 24.8, 0.15),
				createTestBenchmarkResult("test5", 290*time.Millisecond, 25.2, 0.1),
			},
			current: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 300*time.Millisecond, 25.0, 5.0), // 50x increase
				createTestBenchmarkResult("test2", 310*time.Millisecond, 24.5, 4.8), // 24x increase
				createTestBenchmarkResult("test3", 295*time.Millisecond, 25.5, 5.2), // 52x increase
				createTestBenchmarkResult("test4", 305*time.Millisecond, 24.8, 4.9), // 32x increase
				createTestBenchmarkResult("test5", 290*time.Millisecond, 25.2, 5.1), // 51x increase
			},
			config: entities.RegressionCondition{
				Method:          "statistical",
				Threshold:       0.20,
				ConfidenceLevel: 0.95,
				StatisticalTest: "t-test",
				MinSampleSize:   5,
			},
			expectedRegression: true,
			expectedSeverity:   entities.SeverityCritical,
			description:        "Should detect significant error rate regression",
		},
		{
			name: "insufficient_sample_size",
			baseline: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 300*time.Millisecond, 25.0, 0.1),
				createTestBenchmarkResult("test2", 310*time.Millisecond, 24.5, 0.2),
			},
			current: []entities.BenchmarkResult{
				createTestBenchmarkResult("test1", 450*time.Millisecond, 15.0, 5.0),
				createTestBenchmarkResult("test2", 465*time.Millisecond, 14.3, 4.8),
			},
			config: entities.RegressionCondition{
				Method:          "statistical",
				Threshold:       0.20,
				ConfidenceLevel: 0.95,
				StatisticalTest: "t-test",
				MinSampleSize:   5,
			},
			expectedRegression: false,
			expectedSeverity:   entities.SeverityWarning,
			description:        "Should not perform statistical test with insufficient sample size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.DetectStatisticalRegression(ctx, tt.baseline, tt.current, tt.config)
			require.NoError(t, err, tt.description)
			require.NotNil(t, result, "Result should not be nil")

			assert.Equal(t, tt.expectedRegression, result.HasRegression, tt.description)
			assert.Equal(t, tt.expectedSeverity, result.Severity, "Severity should match expected")
			assert.Equal(t, tt.config.Method, result.Method, "Method should match config")
			assert.Equal(t, tt.config.ConfidenceLevel, result.ConfidenceLevel, "Confidence level should match")

			if tt.expectedRegression {
				assert.NotEmpty(t, result.Recommendations, "Should provide recommendations for regressions")
				assert.Greater(t, len(result.Details), 0, "Should provide detailed analysis")
			}

			// Statistical tests should provide p-value when applicable
			if tt.config.StatisticalTest != "" && len(tt.baseline) >= tt.config.MinSampleSize {
				assert.GreaterOrEqual(t, result.PValue, 0.0, "P-value should be non-negative")
				assert.LessOrEqual(t, result.PValue, 1.0, "P-value should not exceed 1.0")
			}
		})
	}
}

// TestEnhancedRegressionDetectionService_TrendRegression tests trend-based regression detection
func TestEnhancedRegressionDetectionService_TrendRegression(t *testing.T) {
	service := NewEnhancedRegressionDetectionService()
	ctx := context.Background()

	tests := []struct {
		name               string
		historicalData     []entities.BenchmarkResult
		config             entities.RegressionCondition
		expectedRegression bool
		description        string
	}{
		{
			name: "improving_trend_no_regression",
			historicalData: []entities.BenchmarkResult{
				createTestBenchmarkResultWithTime("test", 350*time.Millisecond, 20.0, 0.2, time.Now().Add(-10*time.Hour)),
				createTestBenchmarkResultWithTime("test", 340*time.Millisecond, 21.0, 0.18, time.Now().Add(-8*time.Hour)),
				createTestBenchmarkResultWithTime("test", 330*time.Millisecond, 22.0, 0.16, time.Now().Add(-6*time.Hour)),
				createTestBenchmarkResultWithTime("test", 320*time.Millisecond, 23.0, 0.14, time.Now().Add(-4*time.Hour)),
				createTestBenchmarkResultWithTime("test", 310*time.Millisecond, 24.0, 0.12, time.Now().Add(-2*time.Hour)),
				createTestBenchmarkResultWithTime("test", 300*time.Millisecond, 25.0, 0.10, time.Now()),
			},
			config: entities.RegressionCondition{
				Method:          "trend",
				Threshold:       0.15,
				ConfidenceLevel: 0.95,
				MinSampleSize:   5,
			},
			expectedRegression: false,
			description:        "Should not detect regression in improving performance trend",
		},
		{
			name: "degrading_trend_regression",
			historicalData: []entities.BenchmarkResult{
				createTestBenchmarkResultWithTime("test", 300*time.Millisecond, 25.0, 0.10, time.Now().Add(-10*time.Hour)),
				createTestBenchmarkResultWithTime("test", 320*time.Millisecond, 23.5, 0.12, time.Now().Add(-8*time.Hour)),
				createTestBenchmarkResultWithTime("test", 350*time.Millisecond, 22.0, 0.15, time.Now().Add(-6*time.Hour)),
				createTestBenchmarkResultWithTime("test", 380*time.Millisecond, 20.5, 0.18, time.Now().Add(-4*time.Hour)),
				createTestBenchmarkResultWithTime("test", 420*time.Millisecond, 19.0, 0.22, time.Now().Add(-2*time.Hour)),
				createTestBenchmarkResultWithTime("test", 450*time.Millisecond, 17.5, 0.25, time.Now()),
			},
			config: entities.RegressionCondition{
				Method:          "trend",
				Threshold:       0.15,
				ConfidenceLevel: 0.95,
				MinSampleSize:   5,
			},
			expectedRegression: true,
			description:        "Should detect regression in degrading performance trend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.DetectTrendRegression(ctx, tt.historicalData, tt.config)
			require.NoError(t, err, tt.description)
			require.NotNil(t, result, "Result should not be nil")

			assert.Equal(t, tt.expectedRegression, result.HasRegression, tt.description)
			assert.Equal(t, tt.config.Method, result.Method, "Method should match config")
		})
	}
}

// TestEnhancedRegressionDetectionService_StatisticalSignificance tests statistical significance calculation
func TestEnhancedRegressionDetectionService_StatisticalSignificance(t *testing.T) {
	service := NewEnhancedRegressionDetectionService()
	ctx := context.Background()

	tests := []struct {
		name                string
		data1               []float64
		data2               []float64
		testType            string
		expectedSignificant bool
		description         string
	}{
		{
			name:                "t_test_no_difference",
			data1:               []float64{10, 11, 9, 12, 8, 13, 7, 14, 6, 15},
			data2:               []float64{9.5, 11.5, 8.5, 12.5, 7.5, 13.5, 6.5, 14.5, 5.5, 15.5},
			testType:            "t-test",
			expectedSignificant: false,
			description:         "Should not find significant difference between similar datasets",
		},
		{
			name:                "t_test_significant_difference",
			data1:               []float64{10, 11, 9, 12, 8, 13, 7, 14, 6, 15},
			data2:               []float64{20, 21, 19, 22, 18, 23, 17, 24, 16, 25},
			testType:            "t-test",
			expectedSignificant: true,
			description:         "Should find significant difference between different datasets",
		},
		{
			name:                "mann_whitney_test",
			data1:               []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			data2:               []float64{11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
			testType:            "mann-whitney",
			expectedSignificant: true,
			description:         "Should detect significant difference with Mann-Whitney U test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.CalculateStatisticalSignificance(ctx, tt.data1, tt.data2, tt.testType)
			require.NoError(t, err, tt.description)
			require.NotNil(t, result, "Result should not be nil")

			assert.Equal(t, tt.testType, result.TestType, "Test type should match")
			assert.Equal(t, tt.expectedSignificant, result.IsSignificant, tt.description)
			assert.GreaterOrEqual(t, result.PValue, 0.0, "P-value should be non-negative")
			assert.LessOrEqual(t, result.PValue, 1.0, "P-value should not exceed 1.0")

			if tt.expectedSignificant {
				assert.Less(t, result.PValue, 0.05, "Significant result should have p-value < 0.05")
			} else {
				assert.GreaterOrEqual(t, result.PValue, 0.05, "Non-significant result should have p-value >= 0.05")
			}
		})
	}
}

// TestEnhancedRegressionDetectionService_CompareDatasets tests dataset comparison functionality
func TestEnhancedRegressionDetectionService_CompareDatasets(t *testing.T) {
	service := NewEnhancedRegressionDetectionService()
	ctx := context.Background()

	baseline := []entities.BenchmarkResult{
		createTestBenchmarkResult("test1", 300*time.Millisecond, 25.0, 0.1),
		createTestBenchmarkResult("test2", 310*time.Millisecond, 24.5, 0.2),
		createTestBenchmarkResult("test3", 295*time.Millisecond, 25.5, 0.1),
	}

	current := []entities.BenchmarkResult{
		createTestBenchmarkResult("test1", 450*time.Millisecond, 25.0, 0.1),
		createTestBenchmarkResult("test2", 465*time.Millisecond, 24.5, 0.2),
		createTestBenchmarkResult("test3", 442*time.Millisecond, 25.5, 0.1),
	}

	result, err := service.ComparePerformanceDatasets(ctx, baseline, current, 0.95)
	require.NoError(t, err, "Should compare datasets successfully")
	require.NotNil(t, result, "Result should not be nil")

	assert.NotEmpty(t, result.TestType, "Should specify test type")
	assert.GreaterOrEqual(t, result.PValue, 0.0, "P-value should be non-negative")
	assert.LessOrEqual(t, result.PValue, 1.0, "P-value should not exceed 1.0")
	assert.NotEmpty(t, result.Summary, "Should provide summary")
}

// Helper functions for creating test data

func createTestBenchmarkResult(testName string, latency time.Duration, throughput float64, errorRate float64) entities.BenchmarkResult {
	return entities.BenchmarkResult{
		TestName:         testName,
		Timestamp:        time.Now(),
		Duration:         60 * time.Second,
		AvgLatency:       latency,
		P95Latency:       latency + (latency / 4), // P95 is typically 25% higher
		ThroughputRPS:    throughput,
		ErrorRatePercent: errorRate,
		TotalRequests:    int(throughput * 60), // throughput * duration
		SuccessfulReqs:   int(throughput * 60 * (1 - errorRate/100)),
		FailedReqs:       int(throughput * 60 * (errorRate / 100)),
	}
}

func createTestBenchmarkResultWithTime(testName string, latency time.Duration, throughput float64, errorRate float64, timestamp time.Time) entities.BenchmarkResult {
	result := createTestBenchmarkResult(testName, latency, throughput, errorRate)
	result.Timestamp = timestamp
	return result
}
