//go:build cli

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// main function for CLI usage
func main() {
	var config CLIConfig

	// Define CLI flags
	flag.StringVar(&config.ResultsFile, "results", "", "Path to benchmark results JSON file (required)")
	flag.StringVar(&config.BaselineFile, "baseline", "", "Path to baseline results JSON file")
	flag.StringVar(&config.OutputFile, "output", "", "Path to output analysis JSON file")
	flag.StringVar(&config.ReportFile, "report", "", "Path to output report Markdown file")
	flag.Float64Var(&config.RegressionThreshold, "threshold", 0.20, "Regression detection threshold (0.0-1.0)")
	flag.BoolVar(&config.CreateBaseline, "create-baseline", false, "Create baseline from current results")
	flag.BoolVar(&config.Detailed, "detailed", false, "Generate detailed analysis report")

	flag.Parse()

	// Validate required flags
	if config.ResultsFile == "" {
		fmt.Fprintf(os.Stderr, "Error: -results flag is required\n")
		flag.Usage()
		os.Exit(1)
	}

	// Create analyzer
	analyzer := NewCLIPerformanceAnalyzer()

	// Validate configuration
	if err := analyzer.ValidateConfig(config); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// Load results
	results, err := analyzer.LoadResults(config.ResultsFile)
	if err != nil {
		log.Fatalf("Failed to load results: %v", err)
	}

	// Load baseline if provided
	var baseline []BenchmarkResult
	if config.BaselineFile != "" {
		baseline, err = analyzer.LoadResults(config.BaselineFile)
		if err != nil {
			log.Printf("Warning: Failed to load baseline: %v", err)
		}
	}

	// Use default SLA thresholds (matching config/performance/benchmark_config.json)
	sla := SLAThresholds{
		MaxAvgLatencyMs:     500,
		MaxP95LatencyMs:     1000,
		MinThroughputRPS:    10,
		MaxErrorRatePercent: 1.0,
	}

	// Generate analysis
	analysis := struct {
		Results       []BenchmarkResult     `json:"results"`
		Baseline      []BenchmarkResult     `json:"baseline,omitempty"`
		Summary       *SummaryAnalysis      `json:"summary"`
		SLA           *OverallSLAEvaluation `json:"sla_evaluation"`
		HasRegression bool                  `json:"has_regression"`
		Timestamp     time.Time             `json:"timestamp"`
	}{
		Results:   results,
		Baseline:  baseline,
		Summary:   analyzer.GenerateSummaryAnalysis(results),
		SLA:       analyzer.EvaluateOverallSLA(results, sla),
		Timestamp: time.Now(),
	}

	// Check for regressions if baseline provided
	if len(baseline) > 0 {
		baselineMap := make(map[string]BenchmarkResult)
		for _, b := range baseline {
			baselineMap[b.TestName] = b
		}

		for _, current := range results {
			if base, exists := baselineMap[current.TestName]; exists {
				comparison := analyzer.CompareResults(base, current, config.RegressionThreshold)
				if comparison.HasRegression {
					analysis.HasRegression = true
					break
				}
			}
		}
	}

	// Save analysis output
	if config.OutputFile != "" {
		analysisData, err := json.MarshalIndent(analysis, "", "  ")
		if err != nil {
			log.Fatalf("Failed to marshal analysis: %v", err)
		}

		// Ensure output directory exists
		if err := os.MkdirAll(filepath.Dir(config.OutputFile), 0755); err != nil {
			log.Fatalf("Failed to create output directory: %v", err)
		}

		if err := os.WriteFile(config.OutputFile, analysisData, 0644); err != nil {
			log.Fatalf("Failed to write analysis file: %v", err)
		}

		fmt.Printf("Analysis saved to: %s\n", config.OutputFile)
	}

	// Generate and save report
	if config.ReportFile != "" {
		report := analyzer.GenerateMarkdownReport(results, sla, baseline, config.Detailed)

		// Ensure report directory exists
		if err := os.MkdirAll(filepath.Dir(config.ReportFile), 0755); err != nil {
			log.Fatalf("Failed to create report directory: %v", err)
		}

		if err := os.WriteFile(config.ReportFile, []byte(report), 0644); err != nil {
			log.Fatalf("Failed to write report file: %v", err)
		}

		fmt.Printf("Report saved to: %s\n", config.ReportFile)
	}

	// Output summary to stdout
	fmt.Printf("\n=== Performance Analysis Summary ===\n")
	fmt.Printf("Scenarios: %d\n", len(results))
	fmt.Printf("SLA Compliance: %s\n", map[bool]string{true: "PASSED", false: "FAILED"}[analysis.SLA.Passed])
	fmt.Printf("Passing/Failing: %d/%d\n", analysis.SLA.PassingScenarios, analysis.SLA.FailingScenarios)

	if analysis.HasRegression {
		fmt.Printf("Performance Regression: DETECTED\n")
	} else if len(baseline) > 0 {
		fmt.Printf("Performance Regression: None\n")
	}

	// Exit with appropriate code
	if !analysis.SLA.Passed || analysis.HasRegression {
		os.Exit(1)
	}
}
