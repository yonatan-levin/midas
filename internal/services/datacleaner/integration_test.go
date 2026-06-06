package datacleaner

import (
	"context"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/rules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPipelineIntegration_RealProcessors tests the pipeline with real stage processors
func TestPipelineIntegration_RealProcessors(t *testing.T) {
	// Create rules engine
	rulesEngine := rules.NewRuleEngine()
	err := rulesEngine.LoadRules("../../../config/datacleaner/rules.json")
	require.NoError(t, err)

	// Create pipeline orchestrator
	orchestrator := NewPipelineOrchestrator(rulesEngine)

	// Create test context
	cleaningCtx := &entities.CleaningContext{
		IndustryCode:     "technology",
		EnableIndustry:   true,
		QualityThreshold: 80.0,
	}

	// Test data with multiple issues requiring cleaning
	testData := &entities.FinancialData{
		Ticker:                  "PIPELINE_TEST",
		TotalAssets:             2000000,
		Goodwill:                300000, // 15% - should trigger adjustment
		IntangibleAssets:        200000, // 10% - should be flagged
		Inventory:               800000, // High inventory
		OperatingLeaseLiability: 150000, // Should be treated as debt
		Revenue:                 1500000,
		OperatingIncome:         300000,
		InterestExpense:         25000,
		TaxRate:                 0.21,
	}

	ctx := context.Background()

	// Act
	result, err := orchestrator.ExecutePipeline(ctx, testData, cleaningCtx)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, result)

	// Basic pipeline should execute successfully
	assert.True(t, result.Success)
	assert.NotNil(t, result.CleanedData)
	assert.Equal(t, "PIPELINE_TEST", result.CleanedData.Ticker)

	// Generate report
	generator := NewReportGenerator()
	report := generator.GenerateReport(testData.Ticker, result)

	require.NotNil(t, report)
	assert.Equal(t, "PIPELINE_TEST", report.Ticker)
	assert.True(t, len(report.Sections) > 0)

	t.Logf("Pipeline processed %d stages in %v",
		len(result.StageResults), result.TotalDuration)
	t.Logf("Generated report with %d sections", len(report.Sections))
}

// TestPipelineIntegration_SimpleFlow tests basic pipeline flow
func TestPipelineIntegration_SimpleFlow(t *testing.T) {
	// Create rules engine
	rulesEngine := rules.NewRuleEngine()

	// Create pipeline orchestrator
	orchestrator := NewPipelineOrchestrator(rulesEngine)

	// Simple test data
	testData := &entities.FinancialData{
		Ticker:      "SIMPLE_TEST",
		TotalAssets: 1000000,
		Revenue:     500000,
	}

	cleaningCtx := &entities.CleaningContext{
		IndustryCode: "general",
	}

	ctx := context.Background()

	// Act
	result, err := orchestrator.ExecutePipeline(ctx, testData, cleaningCtx)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotNil(t, result.CleanedData)
}

// TODO: TestStageProcessors_ErrorHandling - Requires stage processor implementations
// func TestStageProcessors_ErrorHandling(t *testing.T) { /* Commented out until stage processors are implemented */ }

// TODO: TestPipelineConfiguration - Requires updated PipelineConfig structure
/*
func TestPipelineConfiguration(t *testing.T) {
	tests := []struct {
		name               string
		config             *PipelineConfig
		expectStageFailure bool
	}{
		{
			name: "continue_on_error_true",
			config: &PipelineConfig{
				MaxStageTimeout: 1 * time.Second,
				ContinueOnError: true,
				EnableParallel:  false,
				LogLevel:        "info",
			},
			expectStageFailure: false, // Should continue despite errors
		},
		{
			name: "continue_on_error_false",
			config: &PipelineConfig{
				MaxStageTimeout: 1 * time.Second,
				ContinueOnError: false,
				EnableParallel:  false,
				LogLevel:        "info",
			},
			expectStageFailure: true, // Should fail on first error
		},
		{
			name: "short_timeout",
			config: &PipelineConfig{
				MaxStageTimeout: 1 * time.Millisecond, // Very short timeout
				ContinueOnError: false,
				EnableParallel:  false,
				LogLevel:        "debug",
			},
			expectStageFailure: true, // Should timeout
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator := NewPipelineOrchestratorWithConfig(tt.config)

			// Register a slow processor for timeout test
			if tt.name == "short_timeout" {
				slowProcessor := &mockStageProcessor{
					stage:     AssetQualityStage,
					sleepTime: 100 * time.Millisecond, // Longer than timeout
				}
				err := orchestrator.RegisterStage(AssetQualityStage, slowProcessor)
				require.NoError(t, err)
			} else {
				// Register a failing processor for error handling tests
				failingProcessor := &mockStageProcessor{
					stage: AssetQualityStage,
					err:   assert.AnError,
				}
				err := orchestrator.RegisterStage(AssetQualityStage, failingProcessor)
				require.NoError(t, err)
			}

			data := &entities.FinancialData{Ticker: "CONFIG_TEST"}
			ctx := context.Background()

			result, err := orchestrator.ExecutePipeline(ctx, data, &entities.CleaningContext{})

			if tt.expectStageFailure && !tt.config.ContinueOnError {
				assert.Error(t, err)
			} else if tt.config.ContinueOnError {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.False(t, result.Success) // Should be false due to errors
			}
		})
	}
}
*/

// TODO: TestReportGenerator_EdgeCases - Requires updated report generator structure
// /*
// func TestReportGenerator_EdgeCases(t *testing.T) {
// 	generator := NewCleaningReportGenerator()

// 	tests := []struct {
// 		name           string
// 		pipelineResult *PipelineResult
// 		originalData   *entities.FinancialData
// 		expectError    bool
// 	}{
// 		{
// 			name:           "nil_pipeline_result",
// 			pipelineResult: nil,
// 			originalData:   &entities.FinancialData{Ticker: "TEST"},
// 			expectError:    true,
// 		},
// 		{
// 			name:           "nil_original_data",
// 			pipelineResult: &PipelineResult{Success: true},
// 			originalData:   nil,
// 			expectError:    true,
// 		},
// 		{
// 			name: "empty_stage_results",
// 			pipelineResult: &PipelineResult{
// 				Success:      true,
// 				StageResults: []StageResult{},
// 				CleanedData:  &entities.FinancialData{Ticker: "EMPTY"},
// 			},
// 			originalData: &entities.FinancialData{Ticker: "EMPTY"},
// 			expectError:  false,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			report, err := generator.GenerateReport(tt.pipelineResult, tt.originalData)

// 			if tt.expectError {
// 				assert.Error(t, err)
// 				assert.Nil(t, report)
// 			} else {
// 				assert.NoError(t, err)
// 				assert.NotNil(t, report)
// 			}
// 		})
// 	}
// }

// // TestReportGenerator_Configuration tests different report configurations
// func TestReportGenerator_Configuration(t *testing.T) {
// 	configs := []*ReportConfig{
// 		{
// 			IncludeDetailedAuditTrail: false, // Should not include audit trail section
// 			FormatCurrency:            false, // Should not format as currency
// 			TimestampFormat:           time.RFC822,
// 			IncludeMetadata:           false,
// 		},
// 		{
// 			IncludeDetailedAuditTrail: true,
// 			FormatCurrency:            true,
// 			TimestampFormat:           time.RFC3339,
// 			IncludeMetadata:           true,
// 		},
// 	}

// 	for i, config := range configs {
// 		t.Run(fmt.Sprintf("config_%d", i), func(t *testing.T) {
// 			generator := NewCleaningReportGeneratorWithConfig(config)

// 			pipelineResult := &PipelineResult{
// 				Success: true,
// 				StageResults: []StageResult{
// 					{
// 						Stage: AssetQualityStage,
// 						Adjustments: []entities.Adjustment{
// 							{Amount: 1000, Applied: true},
// 						},
// 					},
// 				},
// 				CleanedData: &entities.FinancialData{Ticker: "CONFIG_TEST"},
// 			}

// 			originalData := &entities.FinancialData{Ticker: "CONFIG_TEST"}

// 			report, err := generator.GenerateReport(pipelineResult, originalData)
// 			require.NoError(t, err)

// 			// Verify configuration effects
// 			if config.IncludeDetailedAuditTrail {
// 				auditSection := findReportSection(report, "Audit Trail")
// 				assert.NotNil(t, auditSection)
// 			}

// 			if config.IncludeMetadata {
// 				assert.NotNil(t, report.Metadata)
// 			} else {
// 				assert.Nil(t, report.Metadata)
// 			}
// 		})
// 	}
// }

// // TestCurrencyFormatting tests various currency formatting scenarios
// func TestCurrencyFormatting(t *testing.T) {
// 	generator := NewCleaningReportGenerator()

// 	tests := []struct {
// 		amount   float64
// 		expected string
// 	}{
// 		{0, "$0"},
// 		{100, "$100"},
// 		{999, "$999"},
// 		{1000, "$1,000"},
// 		{1500.50, "$1,500.50"},
// 		{1000000, "$1,000,000"},
// 		{-1000, "-$1,000"},
// 		{-1500.75, "-$1,500.75"},
// 	}

// 	for _, tt := range tests {
// 		t.Run(fmt.Sprintf("amount_%.2f", tt.amount), func(t *testing.T) {
// 			result := generator.formatCurrency(tt.amount)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }

// // TestAddThousandSeparators tests the thousand separator helper function
// func TestAddThousandSeparators(t *testing.T) {
// 	generator := NewCleaningReportGenerator()

// 	tests := []struct {
// 		input    string
// 		expected string
// 	}{
// 		{"100", "100"},
// 		{"999", "999"},
// 		{"1000", "1,000"},
// 		{"1500", "1,500"},
// 		{"1000000", "1,000,000"},
// 		{"1500.50", "1,500.50"},
// 		{"999.99", "999.99"},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.input, func(t *testing.T) {
// 			result := generator.addThousandSeparators(tt.input)
// 			assert.Equal(t, tt.expected, result)
// 		})
// 	}
// }
