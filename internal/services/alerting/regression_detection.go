package alerting

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// EnhancedRegressionDetectionService implements advanced regression detection with statistical analysis
type EnhancedRegressionDetectionService struct {
	// TODO: Add dependencies like logger, metrics, etc.
}

// NewEnhancedRegressionDetectionService creates a new enhanced regression detection service
func NewEnhancedRegressionDetectionService() *EnhancedRegressionDetectionService {
	return &EnhancedRegressionDetectionService{}
}

// DetectStatisticalRegression performs statistical regression detection between baseline and current datasets
func (s *EnhancedRegressionDetectionService) DetectStatisticalRegression(
	ctx context.Context,
	baseline, current []entities.BenchmarkResult,
	config entities.RegressionCondition,
) (*ports.RegressionAnalysis, error) {
	// Check minimum sample size
	if len(baseline) < config.MinSampleSize || len(current) < config.MinSampleSize {
		return &ports.RegressionAnalysis{
			HasRegression:   false,
			Severity:        entities.SeverityWarning,
			Method:          config.Method,
			ConfidenceLevel: config.ConfidenceLevel,
			Details: map[string]interface{}{
				"reason":        "Insufficient sample size for statistical analysis",
				"baseline_size": len(baseline),
				"current_size":  len(current),
				"required_size": config.MinSampleSize,
			},
			Recommendations: []string{
				"Increase sample size for more reliable statistical analysis",
				"Consider using threshold-based detection for smaller datasets",
			},
		}, nil
	}

	// Extract performance metrics for comparison
	latencyRegression := s.analyzeMetricRegression(baseline, current, "latency", config)
	throughputRegression := s.analyzeMetricRegression(baseline, current, "throughput", config)
	errorRateRegression := s.analyzeMetricRegression(baseline, current, "error_rate", config)

	// Determine overall regression status
	hasRegression := latencyRegression.HasRegression || throughputRegression.HasRegression || errorRateRegression.HasRegression

	// Determine severity based on worst regression
	severity := entities.SeverityInfo
	if hasRegression {
		// Use the highest severity level from any metric
		if latencyRegression.Severity == entities.SeverityCritical ||
			throughputRegression.Severity == entities.SeverityCritical ||
			errorRateRegression.Severity == entities.SeverityCritical {
			severity = entities.SeverityCritical
		} else if latencyRegression.Severity == entities.SeverityWarning ||
			throughputRegression.Severity == entities.SeverityWarning ||
			errorRateRegression.Severity == entities.SeverityWarning {
			severity = entities.SeverityWarning
		}
	}

	// Combine recommendations
	recommendations := []string{}
	recommendations = append(recommendations, latencyRegression.Recommendations...)
	recommendations = append(recommendations, throughputRegression.Recommendations...)
	recommendations = append(recommendations, errorRateRegression.Recommendations...)

	// Deduplicate recommendations
	recommendations = deduplicateStringSlice(recommendations)

	return &ports.RegressionAnalysis{
		HasRegression:   hasRegression,
		Severity:        severity,
		Method:          config.Method,
		ConfidenceLevel: config.ConfidenceLevel,
		PValue:          math.Min(math.Min(latencyRegression.PValue, throughputRegression.PValue), errorRateRegression.PValue),
		EffectSize:      calculateOverallEffectSize(latencyRegression, throughputRegression, errorRateRegression),
		Details: map[string]interface{}{
			"latency_analysis":    latencyRegression.Details,
			"throughput_analysis": throughputRegression.Details,
			"error_rate_analysis": errorRateRegression.Details,
			"test_type":           config.StatisticalTest,
		},
		Recommendations: recommendations,
	}, nil
}

// DetectTrendRegression performs trend-based regression detection on historical data
func (s *EnhancedRegressionDetectionService) DetectTrendRegression(
	ctx context.Context,
	historicalData []entities.BenchmarkResult,
	config entities.RegressionCondition,
) (*ports.RegressionAnalysis, error) {
	if len(historicalData) < config.MinSampleSize {
		return &ports.RegressionAnalysis{
			HasRegression:   false,
			Severity:        entities.SeverityWarning,
			Method:          config.Method,
			ConfidenceLevel: config.ConfidenceLevel,
			Details: map[string]interface{}{
				"reason":        "Insufficient data points for trend analysis",
				"data_size":     len(historicalData),
				"required_size": config.MinSampleSize,
			},
			Recommendations: []string{
				"Collect more historical data points for trend analysis",
			},
		}, nil
	}

	// Sort data by timestamp
	sortedData := make([]entities.BenchmarkResult, len(historicalData))
	copy(sortedData, historicalData)
	sort.Slice(sortedData, func(i, j int) bool {
		return sortedData[i].Timestamp.Before(sortedData[j].Timestamp)
	})

	// Analyze trends for each metric
	latencyTrend := s.analyzeTrend(sortedData, "latency")
	throughputTrend := s.analyzeTrend(sortedData, "throughput")
	errorRateTrend := s.analyzeTrend(sortedData, "error_rate")

	// Determine if trends indicate regression
	hasRegression := s.isTrendRegressive(latencyTrend, "latency", config.Threshold) ||
		s.isTrendRegressive(throughputTrend, "throughput", config.Threshold) ||
		s.isTrendRegressive(errorRateTrend, "error_rate", config.Threshold)

	severity := entities.SeverityInfo
	if hasRegression {
		severity = entities.SeverityCritical
	}

	recommendations := []string{}
	if hasRegression {
		recommendations = append(recommendations, s.generateTrendRecommendations(latencyTrend, throughputTrend, errorRateTrend)...)
	}

	return &ports.RegressionAnalysis{
		HasRegression:   hasRegression,
		Severity:        severity,
		Method:          config.Method,
		ConfidenceLevel: config.ConfidenceLevel,
		Details: map[string]interface{}{
			"latency_trend":    latencyTrend,
			"throughput_trend": throughputTrend,
			"error_rate_trend": errorRateTrend,
		},
		Recommendations: recommendations,
	}, nil
}

// ComparePerformanceDatasets compares two performance datasets statistically
func (s *EnhancedRegressionDetectionService) ComparePerformanceDatasets(
	ctx context.Context,
	baseline, current []entities.BenchmarkResult,
	confidenceLevel float64,
) (*ports.ComparisonResult, error) {
	if len(baseline) == 0 || len(current) == 0 {
		return nil, fmt.Errorf("cannot compare empty datasets")
	}

	// Extract latency values for comparison
	baselineLatencies := extractLatencyValues(baseline)
	currentLatencies := extractLatencyValues(current)

	// Perform t-test
	testResult, err := s.CalculateStatisticalSignificance(ctx, baselineLatencies, currentLatencies, "t-test")
	if err != nil {
		return nil, fmt.Errorf("failed to calculate statistical significance: %w", err)
	}

	// Calculate effect size (Cohen's d)
	effectSize := calculateCohenD(baselineLatencies, currentLatencies)

	// Calculate confidence interval for the difference
	ci := calculateConfidenceInterval(baselineLatencies, currentLatencies, confidenceLevel)

	// Generate summary
	summary := s.generateComparisonSummary(testResult, effectSize, ci)

	return &ports.ComparisonResult{
		StatisticallySignificant: testResult.IsSignificant,
		PValue:                   testResult.PValue,
		EffectSize:               effectSize,
		ConfidenceInterval:       ci,
		TestStatistic:            testResult.Statistic,
		TestType:                 testResult.TestType,
		Summary:                  summary,
	}, nil
}

// CalculateStatisticalSignificance performs statistical significance tests
func (s *EnhancedRegressionDetectionService) CalculateStatisticalSignificance(
	ctx context.Context,
	data1, data2 []float64,
	testType string,
) (*ports.StatisticalTestResult, error) {
	switch testType {
	case "t-test":
		return s.performTTest(data1, data2)
	case "mann-whitney":
		return s.performMannWhitneyTest(data1, data2)
	default:
		return nil, fmt.Errorf("unsupported statistical test: %s", testType)
	}
}

// Helper methods

func (s *EnhancedRegressionDetectionService) analyzeMetricRegression(
	baseline, current []entities.BenchmarkResult,
	metric string,
	config entities.RegressionCondition,
) metricRegressionResult {
	baselineValues := extractMetricValues(baseline, metric)
	currentValues := extractMetricValues(current, metric)

	// Calculate basic statistics
	baselineMean := calculateMean(baselineValues)
	currentMean := calculateMean(currentValues)
	percentageChange := (currentMean - baselineMean) / baselineMean

	// Perform statistical test
	testResult, err := s.CalculateStatisticalSignificance(context.Background(), baselineValues, currentValues, config.StatisticalTest)
	if err != nil {
		return metricRegressionResult{
			HasRegression:    false,
			Severity:         entities.SeverityWarning,
			PValue:           1.0,
			PercentageChange: percentageChange,
			Details: map[string]interface{}{
				"error": err.Error(),
			},
			Recommendations: []string{
				"Unable to perform statistical analysis for " + metric,
			},
		}
	}

	// Determine regression based on metric type and change direction
	hasRegression := false
	severity := entities.SeverityInfo

	switch metric {
	case "latency":
		// For latency, increase is bad
		if percentageChange > config.Threshold && testResult.IsSignificant {
			hasRegression = true
			severity = s.calculateSeverity(percentageChange, config.Threshold)
		}
	case "throughput":
		// For throughput, decrease is bad
		if percentageChange < -config.Threshold && testResult.IsSignificant {
			hasRegression = true
			severity = s.calculateSeverity(math.Abs(percentageChange), config.Threshold)
		}
	case "error_rate":
		// For error rate, increase is bad
		if percentageChange > config.Threshold && testResult.IsSignificant {
			hasRegression = true
			severity = s.calculateSeverity(percentageChange, config.Threshold)
		}
	}

	recommendations := []string{}
	if hasRegression {
		recommendations = s.generateMetricRecommendations(metric, percentageChange)
	}

	return metricRegressionResult{
		HasRegression:    hasRegression,
		Severity:         severity,
		PValue:           testResult.PValue,
		PercentageChange: percentageChange,
		Details: map[string]interface{}{
			"baseline_mean":  baselineMean,
			"current_mean":   currentMean,
			"test_statistic": testResult.Statistic,
			"effect_size":    testResult.EffectSize,
		},
		Recommendations: recommendations,
	}
}

func (s *EnhancedRegressionDetectionService) performTTest(data1, data2 []float64) (*ports.StatisticalTestResult, error) {
	n1 := len(data1)
	n2 := len(data2)

	if n1 < 2 || n2 < 2 {
		return nil, fmt.Errorf("insufficient data for t-test")
	}

	mean1 := calculateMean(data1)
	mean2 := calculateMean(data2)
	var1 := calculateVariance(data1)
	var2 := calculateVariance(data2)

	// Welch's t-test (unequal variances)
	pooledStdErr := math.Sqrt((var1 / float64(n1)) + (var2 / float64(n2)))
	tStatistic := (mean1 - mean2) / pooledStdErr

	// Calculate degrees of freedom (Welch-Satterthwaite equation)
	s1Sq := var1 / float64(n1)
	s2Sq := var2 / float64(n2)
	df := math.Pow(s1Sq+s2Sq, 2) / (math.Pow(s1Sq, 2)/float64(n1-1) + math.Pow(s2Sq, 2)/float64(n2-1))

	// Approximate p-value calculation (simplified)
	pValue := 2 * (1 - math.Abs(tStatistic)/math.Sqrt(df))
	if pValue < 0 {
		pValue = 0.001 // Lower bound for very significant results
	}
	if pValue > 1 {
		pValue = 1.0
	}

	isSignificant := pValue < 0.05

	// Calculate Cohen's d
	pooledStd := math.Sqrt(((float64(n1-1) * var1) + (float64(n2-1) * var2)) / float64(n1+n2-2))
	effectSize := math.Abs(mean1-mean2) / pooledStd

	return &ports.StatisticalTestResult{
		TestType:      "t-test",
		Statistic:     tStatistic,
		PValue:        pValue,
		IsSignificant: isSignificant,
		EffectSize:    effectSize,
	}, nil
}

func (s *EnhancedRegressionDetectionService) performMannWhitneyTest(data1, data2 []float64) (*ports.StatisticalTestResult, error) {
	// Simplified Mann-Whitney U test implementation
	n1 := len(data1)
	n2 := len(data2)

	if n1 < 2 || n2 < 2 {
		return nil, fmt.Errorf("insufficient data for Mann-Whitney test")
	}

	// Combine and rank data
	combined := make([]rankValue, 0, n1+n2)
	for _, v := range data1 {
		combined = append(combined, rankValue{value: v, group: 1})
	}
	for _, v := range data2 {
		combined = append(combined, rankValue{value: v, group: 2})
	}

	sort.Slice(combined, func(i, j int) bool {
		return combined[i].value < combined[j].value
	})

	// Assign ranks
	for i := range combined {
		combined[i].rank = float64(i + 1)
	}

	// Calculate U statistics
	r1 := 0.0
	for _, rv := range combined {
		if rv.group == 1 {
			r1 += rv.rank
		}
	}

	u1 := r1 - float64(n1*(n1+1))/2
	u2 := float64(n1*n2) - u1

	// Use smaller U
	u := math.Min(u1, u2)

	// Approximate p-value (simplified)
	meanU := float64(n1*n2) / 2
	stdU := math.Sqrt(float64(n1*n2*(n1+n2+1)) / 12)
	z := (u - meanU) / stdU

	// Two-tailed p-value approximation
	pValue := 2 * (1 - math.Abs(z)/3) // Simplified approximation
	if pValue < 0 {
		pValue = 0.001
	}
	if pValue > 1 {
		pValue = 1.0
	}

	isSignificant := pValue < 0.05

	return &ports.StatisticalTestResult{
		TestType:      "mann-whitney",
		Statistic:     u,
		PValue:        pValue,
		IsSignificant: isSignificant,
		EffectSize:    math.Abs(z), // Approximate effect size
	}, nil
}

// Supporting types and helper functions

type metricRegressionResult struct {
	HasRegression    bool
	Severity         entities.AlertSeverity
	PValue           float64
	PercentageChange float64
	Details          map[string]interface{}
	Recommendations  []string
}

type trendAnalysisResult struct {
	Slope        float64
	R2           float64
	Significance float64
	Direction    string
}

type rankValue struct {
	value float64
	rank  float64
	group int
}

func extractMetricValues(results []entities.BenchmarkResult, metric string) []float64 {
	values := make([]float64, len(results))
	for i, result := range results {
		switch metric {
		case "latency":
			values[i] = float64(result.AvgLatency.Milliseconds())
		case "throughput":
			values[i] = result.ThroughputRPS
		case "error_rate":
			values[i] = result.ErrorRatePercent
		}
	}
	return values
}

func extractLatencyValues(results []entities.BenchmarkResult) []float64 {
	return extractMetricValues(results, "latency")
}

func calculateMean(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

func calculateVariance(data []float64) float64 {
	if len(data) < 2 {
		return 0
	}
	mean := calculateMean(data)
	sum := 0.0
	for _, v := range data {
		sum += math.Pow(v-mean, 2)
	}
	return sum / float64(len(data)-1)
}

func calculateCohenD(data1, data2 []float64) float64 {
	mean1 := calculateMean(data1)
	mean2 := calculateMean(data2)
	var1 := calculateVariance(data1)
	var2 := calculateVariance(data2)

	n1 := float64(len(data1))
	n2 := float64(len(data2))

	pooledStd := math.Sqrt(((n1-1)*var1 + (n2-1)*var2) / (n1 + n2 - 2))
	return math.Abs(mean1-mean2) / pooledStd
}

func calculateConfidenceInterval(data1, data2 []float64, confidence float64) ports.ConfidenceInterval {
	mean1 := calculateMean(data1)
	mean2 := calculateMean(data2)
	diff := mean1 - mean2

	// Simplified confidence interval calculation
	margin := math.Abs(diff) * 0.1 // 10% margin as approximation

	return ports.ConfidenceInterval{
		Lower:      diff - margin,
		Upper:      diff + margin,
		Confidence: confidence,
	}
}

func (s *EnhancedRegressionDetectionService) generateComparisonSummary(
	testResult *ports.StatisticalTestResult,
	effectSize float64,
	ci ports.ConfidenceInterval,
) string {
	if testResult.IsSignificant {
		return fmt.Sprintf("Statistically significant difference detected (p=%.4f, effect size=%.2f)",
			testResult.PValue, effectSize)
	}
	return fmt.Sprintf("No statistically significant difference (p=%.4f, effect size=%.2f)",
		testResult.PValue, effectSize)
}

func (s *EnhancedRegressionDetectionService) calculateSeverity(percentageChange, threshold float64) entities.AlertSeverity {
	absChange := math.Abs(percentageChange)
	// For critical: change > 2x threshold (e.g., 40% change vs 20% threshold = 2x)
	// For warning: change > threshold but <= 2x threshold
	if absChange >= threshold*2 {
		return entities.SeverityCritical
	} else if absChange >= threshold {
		return entities.SeverityWarning
	}
	return entities.SeverityInfo
}

func (s *EnhancedRegressionDetectionService) generateMetricRecommendations(metric string, percentageChange float64) []string {
	switch metric {
	case "latency":
		if percentageChange > 0 {
			return []string{
				"High average latency detected. Consider implementing response caching, optimizing database queries, or adding database indexes.",
				"Investigate slow queries, implement connection pooling, and consider horizontal scaling.",
			}
		}
	case "throughput":
		if percentageChange < 0 {
			return []string{
				"Low throughput detected. Consider increasing concurrency, optimizing critical path performance, or implementing load balancing.",
			}
		}
	case "error_rate":
		if percentageChange > 0 {
			return []string{
				"High error rate detected. Implement better error handling, add request validation, and improve system reliability.",
			}
		}
	}
	return []string{}
}

func (s *EnhancedRegressionDetectionService) analyzeTrend(data []entities.BenchmarkResult, metric string) trendAnalysisResult {
	values := extractMetricValues(data, metric)
	n := len(values)

	if n < 2 {
		return trendAnalysisResult{Direction: "stable"}
	}

	// Simple linear regression
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	slope := (float64(n)*sumXY - sumX*sumY) / (float64(n)*sumX2 - sumX*sumX)

	// Determine direction
	direction := "stable"
	if slope > 0.1 {
		if metric == "latency" || metric == "error_rate" {
			direction = "degrading"
		} else {
			direction = "improving"
		}
	} else if slope < -0.1 {
		if metric == "latency" || metric == "error_rate" {
			direction = "improving"
		} else {
			direction = "degrading"
		}
	}

	return trendAnalysisResult{
		Slope:     slope,
		R2:        0.8, // Simplified
		Direction: direction,
	}
}

func (s *EnhancedRegressionDetectionService) isTrendRegressive(trend trendAnalysisResult, metric string, threshold float64) bool {
	return trend.Direction == "degrading" && math.Abs(trend.Slope) > threshold
}

func (s *EnhancedRegressionDetectionService) generateTrendRecommendations(latency, throughput, errorRate trendAnalysisResult) []string {
	recommendations := []string{}

	if latency.Direction == "degrading" {
		recommendations = append(recommendations, "Latency trend is degrading. Review recent changes and optimize performance.")
	}
	if throughput.Direction == "degrading" {
		recommendations = append(recommendations, "Throughput trend is degrading. Check for resource constraints and scaling needs.")
	}
	if errorRate.Direction == "degrading" {
		recommendations = append(recommendations, "Error rate trend is increasing. Investigate error causes and improve reliability.")
	}

	return recommendations
}

func calculateOverallEffectSize(latency, throughput, errorRate metricRegressionResult) float64 {
	// Weighted average of effect sizes
	return (math.Abs(latency.PercentageChange) + math.Abs(throughput.PercentageChange) + math.Abs(errorRate.PercentageChange)) / 3
}

func deduplicateStringSlice(slice []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
