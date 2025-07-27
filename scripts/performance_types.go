package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CLIConfig holds CLI configuration options
type CLIConfig struct {
	ResultsFile         string  `json:"results_file"`
	BaselineFile        string  `json:"baseline_file,omitempty"`
	OutputFile          string  `json:"output_file,omitempty"`
	ReportFile          string  `json:"report_file,omitempty"`
	RegressionThreshold float64 `json:"regression_threshold"`
	CreateBaseline      bool    `json:"create_baseline"`
	Detailed            bool    `json:"detailed"`
}

// CLIPerformanceAnalyzer provides CLI functionality for performance analysis
type CLIPerformanceAnalyzer struct {
	analyzer *BenchmarkAnalyzer
}

// SummaryAnalysis holds overall analysis across multiple scenarios
type SummaryAnalysis struct {
	TotalScenarios    int                        `json:"total_scenarios"`
	OverallThroughput float64                    `json:"overall_throughput"`
	AverageLatency    float64                    `json:"average_latency"`
	ScenarioResults   map[string]ScenarioSummary `json:"scenario_results"`
	SLACompliance     *OverallSLAEvaluation      `json:"sla_compliance"`
}

// ScenarioSummary holds summary for individual scenario
type ScenarioSummary struct {
	TestName         string  `json:"test_name"`
	ThroughputRPS    float64 `json:"throughput_rps"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
	ErrorRatePercent float64 `json:"error_rate_percent"`
	SLAPassed        bool    `json:"sla_passed"`
}

// OverallSLAEvaluation holds overall SLA evaluation across scenarios
type OverallSLAEvaluation struct {
	Passed           bool     `json:"passed"`
	PassingScenarios int      `json:"passing_scenarios"`
	FailingScenarios int      `json:"failing_scenarios"`
	Failures         []string `json:"failures"`
}

// NewCLIPerformanceAnalyzer creates a new CLI performance analyzer
func NewCLIPerformanceAnalyzer() *CLIPerformanceAnalyzer {
	return &CLIPerformanceAnalyzer{
		analyzer: NewBenchmarkAnalyzer(),
	}
}

// ValidateConfig validates CLI configuration
func (cli *CLIPerformanceAnalyzer) ValidateConfig(config CLIConfig) error {
	if config.ResultsFile == "" {
		return fmt.Errorf("results file is required")
	}

	if config.RegressionThreshold < 0 {
		return fmt.Errorf("regression threshold must be non-negative")
	}

	if config.RegressionThreshold > 1.0 {
		return fmt.Errorf("regression threshold must not exceed 100%% (1.0)")
	}

	return nil
}

// LoadResults loads benchmark results from JSON file
func (cli *CLIPerformanceAnalyzer) LoadResults(filename string) ([]BenchmarkResult, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read results file: %w", err)
	}

	var results []BenchmarkResult
	if err := json.Unmarshal(data, &results); err != nil {
		// Try loading single result
		var singleResult BenchmarkResult
		if err2 := json.Unmarshal(data, &singleResult); err2 != nil {
			return nil, fmt.Errorf("failed to parse results JSON: %w", err)
		}
		results = []BenchmarkResult{singleResult}
	}

	return results, nil
}

// CompareResults compares two benchmark results
func (cli *CLIPerformanceAnalyzer) CompareResults(baseline, current BenchmarkResult, threshold float64) *BaselineComparison {
	comparison := CompareToBaseline(baseline, current, threshold)
	return &comparison
}

// GenerateMarkdownReport generates a Markdown report
func (cli *CLIPerformanceAnalyzer) GenerateMarkdownReport(results []BenchmarkResult, sla SLAThresholds, baseline []BenchmarkResult, detailed bool) string {
	var builder strings.Builder

	// Header
	builder.WriteString("# Performance Test Report\n\n")
	builder.WriteString(fmt.Sprintf("**Generated:** %s  \n", time.Now().Format("2006-01-02 15:04:05 UTC")))
	builder.WriteString(fmt.Sprintf("**Test Scenarios:** %d  \n", len(results)))

	// SLA Thresholds
	builder.WriteString("\n## 📊 SLA Thresholds\n\n")
	builder.WriteString(fmt.Sprintf("- **Max Average Latency:** %dms  \n", sla.MaxAvgLatencyMs))
	builder.WriteString(fmt.Sprintf("- **Max P95 Latency:** %dms  \n", sla.MaxP95LatencyMs))
	builder.WriteString(fmt.Sprintf("- **Min Throughput:** %.1f RPS  \n", sla.MinThroughputRPS))
	builder.WriteString(fmt.Sprintf("- **Max Error Rate:** %.1f%%  \n", sla.MaxErrorRatePercent))

	// Overall Status
	overallSLA := cli.EvaluateOverallSLA(results, sla)
	if overallSLA.Passed {
		builder.WriteString("\n## ✅ Overall Status: **PASSED**\n\n")
	} else {
		builder.WriteString("\n## ❌ Overall Status: **FAILED**\n\n")
		builder.WriteString("**SLA Violations:**\n")
		for _, failure := range overallSLA.Failures {
			builder.WriteString(fmt.Sprintf("- %s\n", failure))
		}
		builder.WriteString("\n")
	}

	// Summary Statistics
	summary := cli.GenerateSummaryAnalysis(results)
	builder.WriteString("## 📈 Summary Statistics\n\n")
	builder.WriteString(fmt.Sprintf("- **Overall Throughput:** %.1f RPS  \n", summary.OverallThroughput))
	builder.WriteString(fmt.Sprintf("- **Average Latency:** %.0fms  \n", summary.AverageLatency))
	builder.WriteString(fmt.Sprintf("- **Passing Scenarios:** %d/%d  \n", overallSLA.PassingScenarios, len(results)))

	// Individual Scenario Results
	builder.WriteString("\n## 🎯 Scenario Results\n\n")
	for _, result := range results {
		evaluation := cli.analyzer.EvaluateSLA(result, sla)
		status := "✅ PASSED"
		if !evaluation.Passed {
			status = "❌ FAILED"
		}

		builder.WriteString(fmt.Sprintf("### %s %s\n\n", result.TestName, status))
		builder.WriteString(fmt.Sprintf("- **Throughput:** %.1f RPS  \n", result.ThroughputRPS))
		builder.WriteString(fmt.Sprintf("- **Average Latency:** %.0fms  \n", float64(result.AvgLatency.Milliseconds())))
		builder.WriteString(fmt.Sprintf("- **P95 Latency:** %.0fms  \n", float64(result.P95Latency.Milliseconds())))
		builder.WriteString(fmt.Sprintf("- **Error Rate:** %.2f%%  \n", result.ErrorRatePercent))
		builder.WriteString(fmt.Sprintf("- **Total Requests:** %d (%d successful, %d failed)  \n",
			result.TotalRequests, result.SuccessfulReqs, result.FailedReqs))

		if !evaluation.Passed {
			builder.WriteString("\n**SLA Violations:**\n")
			for _, failure := range evaluation.Failures {
				builder.WriteString(fmt.Sprintf("- %s\n", failure))
			}
		}

		// Include recommendations for failed tests
		if !evaluation.Passed || detailed {
			recommendations := cli.analyzer.GenerateRecommendations(result)
			if len(recommendations) > 0 {
				builder.WriteString("\n**Recommendations:**\n")
				for i, rec := range recommendations {
					builder.WriteString(fmt.Sprintf("%d. %s\n", i+1, rec))
				}
			}
		}

		builder.WriteString("\n")
	}

	// Baseline Comparison (if provided)
	if len(baseline) > 0 && len(results) > 0 {
		builder.WriteString("## 📊 Baseline Comparison\n\n")

		// Find matching scenarios between baseline and current
		baselineMap := make(map[string]BenchmarkResult)
		for _, b := range baseline {
			baselineMap[b.TestName] = b
		}

		hasRegression := false
		for _, current := range results {
			if base, exists := baselineMap[current.TestName]; exists {
				comparison := cli.CompareResults(base, current, 0.20)

				if comparison.HasRegression {
					hasRegression = true
					builder.WriteString(fmt.Sprintf("### ⚠️ %s - Regression Detected\n\n", current.TestName))
					for _, reg := range comparison.Regressions {
						builder.WriteString(fmt.Sprintf("- %s\n", reg.Description))
					}
				} else {
					builder.WriteString(fmt.Sprintf("### ✅ %s - No Regression\n\n", current.TestName))
				}

				if len(comparison.Improvements) > 0 {
					builder.WriteString("**Improvements:**\n")
					for _, imp := range comparison.Improvements {
						builder.WriteString(fmt.Sprintf("- %s\n", imp.Description))
					}
				}
				builder.WriteString("\n")
			}
		}

		if !hasRegression {
			builder.WriteString("**Overall:** ✅ No performance regressions detected\n\n")
		}
	}

	return builder.String()
}

// GenerateSummaryAnalysis generates summary analysis across multiple scenarios
func (cli *CLIPerformanceAnalyzer) GenerateSummaryAnalysis(results []BenchmarkResult) *SummaryAnalysis {
	if len(results) == 0 {
		return &SummaryAnalysis{}
	}

	summary := &SummaryAnalysis{
		TotalScenarios:  len(results),
		ScenarioResults: make(map[string]ScenarioSummary),
	}

	var totalThroughput float64
	var totalLatency float64

	for _, result := range results {
		totalThroughput += result.ThroughputRPS
		totalLatency += float64(result.AvgLatency.Milliseconds())

		summary.ScenarioResults[result.TestName] = ScenarioSummary{
			TestName:         result.TestName,
			ThroughputRPS:    result.ThroughputRPS,
			AvgLatencyMs:     float64(result.AvgLatency.Milliseconds()),
			ErrorRatePercent: result.ErrorRatePercent,
		}
	}

	summary.OverallThroughput = totalThroughput
	summary.AverageLatency = totalLatency / float64(len(results))

	return summary
}

// EvaluateOverallSLA evaluates SLA compliance across multiple scenarios
func (cli *CLIPerformanceAnalyzer) EvaluateOverallSLA(results []BenchmarkResult, sla SLAThresholds) *OverallSLAEvaluation {
	evaluation := &OverallSLAEvaluation{
		Passed: true,
	}

	for _, result := range results {
		slaResult := cli.analyzer.EvaluateSLA(result, sla)

		if slaResult.Passed {
			evaluation.PassingScenarios++
		} else {
			evaluation.FailingScenarios++
			evaluation.Passed = false

			// Add failures with scenario context
			for _, failure := range slaResult.Failures {
				evaluation.Failures = append(evaluation.Failures,
					fmt.Sprintf("%s: %s", result.TestName, failure))
			}
		}
	}

	return evaluation
}

// SaveMultipleResults saves benchmark results to file
func SaveMultipleResults(results []BenchmarkResult, filename string) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal results: %w", err)
	}

	return os.WriteFile(filename, data, 0644)
}
