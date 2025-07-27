package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// BenchmarkAnalyzer provides analysis and reporting for benchmark results
type BenchmarkAnalyzer struct{}

// SummaryReport holds a summarized view of benchmark results
type SummaryReport struct {
	TestName           string         `json:"test_name"`
	Timestamp          time.Time      `json:"timestamp"`
	TotalRequests      int64          `json:"total_requests"`
	SuccessfulRequests int64          `json:"successful_requests"`
	FailedRequests     int64          `json:"failed_requests"`
	ThroughputRPS      float64        `json:"throughput_rps"`
	ErrorRatePercent   float64        `json:"error_rate_percent"`
	AvgLatencyMs       float64        `json:"avg_latency_ms"`
	P95LatencyMs       float64        `json:"p95_latency_ms"`
	SLAEvaluation      *SLAEvaluation `json:"sla_evaluation"`
}

// DetailedReport holds comprehensive benchmark analysis
type DetailedReport struct {
	Summary            string                 `json:"summary"`
	PerformanceMetrics map[string]interface{} `json:"performance_metrics"`
	SLAEvaluation      *SLAEvaluation         `json:"sla_evaluation"`
	BaselineComparison *BaselineComparison    `json:"baseline_comparison,omitempty"`
	Recommendations    []string               `json:"recommendations"`
	TrendAnalysis      *TrendAnalysis         `json:"trend_analysis,omitempty"`
}

// TrendAnalysis holds performance trend analysis
type TrendAnalysis struct {
	Direction       string        `json:"direction"`        // "IMPROVING", "DEGRADING", "STABLE"
	LatencyTrend    float64       `json:"latency_trend"`    // Percentage change per day
	ThroughputTrend float64       `json:"throughput_trend"` // Percentage change per day
	ErrorRateTrend  float64       `json:"error_rate_trend"` // Percentage change per day
	Insights        []string      `json:"insights"`
	AnalysisWindow  time.Duration `json:"analysis_window"`
}

// NewBenchmarkAnalyzer creates a new benchmark analyzer
func NewBenchmarkAnalyzer() *BenchmarkAnalyzer {
	return &BenchmarkAnalyzer{}
}

// EvaluateSLA evaluates benchmark results against SLA thresholds
func (ba *BenchmarkAnalyzer) EvaluateSLA(result BenchmarkResult, sla SLAThresholds) SLAEvaluation {
	return sla.Evaluate(result)
}

// CompareToBaseline compares current results to baseline
func (ba *BenchmarkAnalyzer) CompareToBaseline(baseline, current BenchmarkResult, threshold float64) BaselineComparison {
	return CompareToBaseline(baseline, current, threshold)
}

// GenerateSummaryReport creates a summary report from benchmark results
func (ba *BenchmarkAnalyzer) GenerateSummaryReport(result BenchmarkResult, sla SLAThresholds) *SummaryReport {
	slaEval := ba.EvaluateSLA(result, sla)

	return &SummaryReport{
		TestName:           result.TestName,
		Timestamp:          result.Timestamp,
		TotalRequests:      result.TotalRequests,
		SuccessfulRequests: result.SuccessfulReqs,
		FailedRequests:     result.FailedReqs,
		ThroughputRPS:      result.ThroughputRPS,
		ErrorRatePercent:   result.ErrorRatePercent,
		AvgLatencyMs:       float64(result.AvgLatency.Milliseconds()),
		P95LatencyMs:       float64(result.P95Latency.Milliseconds()),
		SLAEvaluation:      &slaEval,
	}
}

// GenerateDetailedReport creates a comprehensive report
func (ba *BenchmarkAnalyzer) GenerateDetailedReport(result BenchmarkResult, sla SLAThresholds, baseline *BenchmarkResult) *DetailedReport {
	slaEval := ba.EvaluateSLA(result, sla)
	recommendations := ba.GenerateRecommendations(result)

	// Create performance metrics map
	perfMetrics := map[string]interface{}{
		"avg_latency_ms":      float64(result.AvgLatency.Milliseconds()),
		"p95_latency_ms":      float64(result.P95Latency.Milliseconds()),
		"p99_latency_ms":      float64(result.P99Latency.Milliseconds()),
		"min_latency_ms":      float64(result.MinLatency.Milliseconds()),
		"max_latency_ms":      float64(result.MaxLatency.Milliseconds()),
		"throughput_rps":      result.ThroughputRPS,
		"error_rate_percent":  result.ErrorRatePercent,
		"total_requests":      result.TotalRequests,
		"successful_requests": result.SuccessfulReqs,
		"failed_requests":     result.FailedReqs,
		"test_duration_sec":   result.Duration.Seconds(),
	}

	// Generate summary text
	slaStatus := "PASSED"
	if !slaEval.Passed {
		slaStatus = "FAILED"
	}

	summary := fmt.Sprintf(
		"Performance Test: %s - SLA Status: %s\n"+
			"Executed at: %s | Duration: %.1fs\n"+
			"Requests: %d total (%d successful, %d failed)\n"+
			"Throughput: %.1f RPS | Error Rate: %.2f%%\n"+
			"Latency: avg=%.1fms, p95=%.1fms, p99=%.1fms",
		result.TestName, slaStatus,
		result.Timestamp.Format("2006-01-02 15:04:05"),
		result.Duration.Seconds(),
		result.TotalRequests, result.SuccessfulReqs, result.FailedReqs,
		result.ThroughputRPS, result.ErrorRatePercent,
		float64(result.AvgLatency.Milliseconds()),
		float64(result.P95Latency.Milliseconds()),
		float64(result.P99Latency.Milliseconds()),
	)

	report := &DetailedReport{
		Summary:            summary,
		PerformanceMetrics: perfMetrics,
		SLAEvaluation:      &slaEval,
		Recommendations:    recommendations,
	}

	// Add baseline comparison if provided
	if baseline != nil {
		comparison := ba.CompareToBaseline(*baseline, result, 0.20)
		report.BaselineComparison = &comparison
	}

	return report
}

// GenerateRecommendations generates performance improvement recommendations
func (ba *BenchmarkAnalyzer) GenerateRecommendations(result BenchmarkResult) []string {
	var recommendations []string

	// Latency recommendations
	avgLatencyMs := float64(result.AvgLatency.Milliseconds())
	p95LatencyMs := float64(result.P95Latency.Milliseconds())

	if avgLatencyMs > 500 {
		recommendations = append(recommendations,
			"High average latency detected (>500ms). Consider implementing response caching, optimizing database queries, or adding database indexes.")
	}

	if p95LatencyMs > 1000 {
		recommendations = append(recommendations,
			"High P95 latency detected (>1000ms). Investigate slow queries, implement connection pooling, and consider horizontal scaling.")
	}

	if p95LatencyMs > avgLatencyMs*3 {
		recommendations = append(recommendations,
			"High latency variance detected. This indicates inconsistent performance - investigate resource contention and optimize background processes.")
	}

	// Throughput recommendations
	if result.ThroughputRPS < 10 {
		recommendations = append(recommendations,
			"Low throughput detected (<10 RPS). Consider increasing concurrency, optimizing critical path performance, or implementing load balancing.")
	}

	if result.ThroughputRPS < 50 && avgLatencyMs < 200 {
		recommendations = append(recommendations,
			"Good latency but low throughput suggests potential for scaling. Consider increasing worker threads or implementing connection pooling.")
	}

	// Error rate recommendations
	if result.ErrorRatePercent > 1.0 {
		recommendations = append(recommendations,
			"High error rate detected (>1%). Implement better error handling, add request validation, and improve system reliability.")
	}

	if result.ErrorRatePercent > 5.0 {
		recommendations = append(recommendations,
			"Critical error rate detected (>5%). Immediate investigation required - check system health, resource limits, and external dependencies.")
	}

	// Request pattern recommendations
	if result.FailedReqs > 0 && result.ErrorRatePercent < 1.0 {
		recommendations = append(recommendations,
			"Some requests are failing but error rate is low. Implement retry logic with exponential backoff for transient failures.")
	}

	// Performance optimization recommendations
	if avgLatencyMs > 200 && result.ThroughputRPS > 20 {
		recommendations = append(recommendations,
			"Moderate latency with good throughput. Focus on caching frequently accessed data and optimizing compute-intensive operations.")
	}

	// If no specific issues found, provide general recommendations
	if len(recommendations) == 0 {
		recommendations = append(recommendations,
			"Performance metrics look good! Consider establishing this as a baseline and setting up automated performance regression detection.")
	}

	return recommendations
}

// AnalyzeTrend analyzes performance trends across multiple results
func (ba *BenchmarkAnalyzer) AnalyzeTrend(results []BenchmarkResult) *TrendAnalysis {
	if len(results) < 2 {
		return &TrendAnalysis{
			Direction: "INSUFFICIENT_DATA",
			Insights:  []string{"Need at least 2 data points for trend analysis"},
		}
	}

	// Sort results by timestamp
	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.Before(results[j].Timestamp)
	})

	oldest := results[0]
	newest := results[len(results)-1]
	timeWindow := newest.Timestamp.Sub(oldest.Timestamp)

	if timeWindow.Hours() < 1 {
		return &TrendAnalysis{
			Direction: "INSUFFICIENT_TIMESPAN",
			Insights:  []string{"Need longer time window for meaningful trend analysis"},
		}
	}

	// Calculate daily trend rates
	days := timeWindow.Hours() / 24
	if days == 0 {
		days = 1 // Avoid division by zero
	}

	// Latency trend (increase is bad)
	latencyChange := (float64(newest.AvgLatency.Milliseconds()) - float64(oldest.AvgLatency.Milliseconds())) / float64(oldest.AvgLatency.Milliseconds())
	latencyTrendPerDay := latencyChange / days

	// Throughput trend (increase is good)
	throughputChange := (newest.ThroughputRPS - oldest.ThroughputRPS) / oldest.ThroughputRPS
	throughputTrendPerDay := throughputChange / days

	// Error rate trend (increase is bad)
	var errorRateTrendPerDay float64
	if oldest.ErrorRatePercent > 0 {
		errorRateChange := (newest.ErrorRatePercent - oldest.ErrorRatePercent) / oldest.ErrorRatePercent
		errorRateTrendPerDay = errorRateChange / days
	} else if newest.ErrorRatePercent > 0 {
		errorRateTrendPerDay = 1.0 / days // Significant increase from 0
	}

	// Determine overall direction
	direction := "STABLE"
	var insights []string

	degradingCount := 0
	improvingCount := 0

	// Check each metric for significant trends (>10% change per week)
	weeklyThreshold := 0.10 / 7 // 10% per week = ~1.4% per day

	if latencyTrendPerDay > weeklyThreshold {
		degradingCount++
		insights = append(insights, fmt.Sprintf("Latency increasing by %.1f%% per day", latencyTrendPerDay*100))
	} else if latencyTrendPerDay < -weeklyThreshold {
		improvingCount++
		insights = append(insights, fmt.Sprintf("Latency improving by %.1f%% per day", math.Abs(latencyTrendPerDay*100)))
	}

	if throughputTrendPerDay > weeklyThreshold {
		improvingCount++
		insights = append(insights, fmt.Sprintf("Throughput increasing by %.1f%% per day", throughputTrendPerDay*100))
	} else if throughputTrendPerDay < -weeklyThreshold {
		degradingCount++
		insights = append(insights, fmt.Sprintf("Throughput decreasing by %.1f%% per day", math.Abs(throughputTrendPerDay*100)))
	}

	if math.Abs(errorRateTrendPerDay) > weeklyThreshold && newest.ErrorRatePercent > 0.1 {
		if errorRateTrendPerDay > 0 {
			degradingCount++
			insights = append(insights, fmt.Sprintf("Error rate increasing by %.1f%% per day", errorRateTrendPerDay*100))
		} else {
			improvingCount++
			insights = append(insights, fmt.Sprintf("Error rate decreasing by %.1f%% per day", math.Abs(errorRateTrendPerDay*100)))
		}
	}

	// Determine overall direction
	if degradingCount > improvingCount {
		direction = "DEGRADING"
	} else if improvingCount > degradingCount {
		direction = "IMPROVING"
	}

	// Add summary insights
	if direction == "DEGRADING" {
		insights = append(insights, "Performance is degrading over time. Investigation and optimization recommended.")
	} else if direction == "IMPROVING" {
		insights = append(insights, "Performance is improving over time. Continue current optimization efforts.")
	} else {
		insights = append(insights, "Performance is stable with no significant trends detected.")
	}

	return &TrendAnalysis{
		Direction:       direction,
		LatencyTrend:    latencyTrendPerDay,
		ThroughputTrend: throughputTrendPerDay,
		ErrorRateTrend:  errorRateTrendPerDay,
		Insights:        insights,
		AnalysisWindow:  timeWindow,
	}
}

// FormatReport formats a detailed report as human-readable text
func (ba *BenchmarkAnalyzer) FormatReport(report *DetailedReport) string {
	var builder strings.Builder

	builder.WriteString("=== PERFORMANCE BENCHMARK REPORT ===\n\n")
	builder.WriteString(report.Summary)
	builder.WriteString("\n\n")

	// SLA Status
	builder.WriteString("=== SLA COMPLIANCE ===\n")
	if report.SLAEvaluation.Passed {
		builder.WriteString("✅ PASSED - All metrics meet SLA requirements\n")
	} else {
		builder.WriteString("❌ FAILED - SLA violations detected:\n")
		for _, failure := range report.SLAEvaluation.Failures {
			builder.WriteString(fmt.Sprintf("  - %s\n", failure))
		}
	}
	builder.WriteString("\n")

	// Baseline Comparison
	if report.BaselineComparison != nil {
		builder.WriteString("=== BASELINE COMPARISON ===\n")
		if report.BaselineComparison.HasRegression {
			builder.WriteString("⚠️ REGRESSION DETECTED:\n")
			for _, reg := range report.BaselineComparison.Regressions {
				builder.WriteString(fmt.Sprintf("  - %s\n", reg.Description))
			}
		} else {
			builder.WriteString("✅ No performance regressions detected\n")
		}

		if len(report.BaselineComparison.Improvements) > 0 {
			builder.WriteString("🚀 IMPROVEMENTS DETECTED:\n")
			for _, imp := range report.BaselineComparison.Improvements {
				builder.WriteString(fmt.Sprintf("  - %s\n", imp.Description))
			}
		}
		builder.WriteString("\n")
	}

	// Recommendations
	if len(report.Recommendations) > 0 {
		builder.WriteString("=== RECOMMENDATIONS ===\n")
		for i, rec := range report.Recommendations {
			builder.WriteString(fmt.Sprintf("%d. %s\n", i+1, rec))
		}
		builder.WriteString("\n")
	}

	// Trend Analysis
	if report.TrendAnalysis != nil {
		builder.WriteString("=== TREND ANALYSIS ===\n")
		builder.WriteString(fmt.Sprintf("Direction: %s\n", report.TrendAnalysis.Direction))
		for _, insight := range report.TrendAnalysis.Insights {
			builder.WriteString(fmt.Sprintf("  - %s\n", insight))
		}
		builder.WriteString("\n")
	}

	return builder.String()
}
