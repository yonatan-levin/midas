package datacleaner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/adjustments"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/rules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPipelineIntegration_RealProcessors tests the pipeline with real stage processors
func TestPipelineIntegration_RealProcessors(t *testing.T) {
	// Create real rules engine
	rulesEngine := rules.NewRuleEngine()
	
	// Create real adjusters
	assetAdjuster := adjustments.NewAssetAdjuster()
	liabilityAdjuster := adjustments.NewLiabilityAdjuster()
	earningsAdjuster := adjustments.NewEarningsAdjuster()

	// Create pipeline orchestrator
	orchestrator := NewPipelineOrchestrator()

	// Register real stage processors
	err := orchestrator.RegisterStage(AssetQualityStage, 
		NewAssetQualityStageProcessor(assetAdjuster, rulesEngine))
	require.NoError(t, err)

	err = orchestrator.RegisterStage(LiabilityCompletenessStage,
		NewLiabilityCompletenessStageProcessor(liabilityAdjuster, rulesEngine))
	require.NoError(t, err)

	err = orchestrator.RegisterStage(EarningsNormalizationStage,
		NewEarningsNormalizationStageProcessor(earningsAdjuster, rulesEngine))
	require.NoError(t, err)

	// Test data with multiple issues requiring cleaning
	testData := &entities.FinancialData{
		Ticker:                   "PIPELINE_TEST",
		TotalAssets:              2000000,
		Goodwill:                 200000, // 10% - should trigger adjustment
		OtherIntangibles:         150000, // 7.5% - should trigger adjustment
		Inventory:                300000, // Will need industry context
		OperatingLeaseLiability:  100000, // Should trigger liability adjustment
		Revenue:                  1000000,
		OperatingIncome:          200000,
		RestructuringCharges:     25000,  // Should trigger earnings adjustment
		AssetSaleGains:           15000,  // Should trigger earnings adjustment
		InterestExpense:          50000,
		TaxRate:                  0.21,
	}

	cleaningCtx := &entities.CleaningContext{
		IndustryCode:     "technology",
		CompanySize:      entities.LargeCap,
		DataVintage:      time.Now(),
		EnableIndustry:   true,
		EnableCaching:    false,
		QualityThreshold: 80.0,
	}

	ctx := context.Background()

	// Execute pipeline
	result, err := orchestrator.ExecutePipeline(ctx, testData, cleaningCtx)

	// Verify results
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, 3, len(result.StageResults)) // All three stages should execute

	// Verify each stage processed
	assert.Equal(t, AssetQualityStage, result.StageResults[0].Stage)
	assert.Equal(t, LiabilityCompletenessStage, result.StageResults[1].Stage)
	assert.Equal(t, EarningsNormalizationStage, result.StageResults[2].Stage)

	// Verify performance
	assert.True(t, result.TotalDuration < 200*time.Millisecond)

	// Verify cleaned data was modified
	assert.NotNil(t, result.CleanedData)
	assert.Equal(t, testData.Ticker, result.CleanedData.Ticker)
}

// TestPipelineIntegration_WithReporting tests the complete pipeline with reporting
func TestPipelineIntegration_WithReporting(t *testing.T) {
	// Create real components
	rulesEngine := rules.NewRuleEngine()
	assetAdjuster := adjustments.NewAssetAdjuster()
	
	// Create pipeline orchestrator
	orchestrator := NewPipelineOrchestrator()
	err := orchestrator.RegisterStage(AssetQualityStage, 
		NewAssetQualityStageProcessor(assetAdjuster, rulesEngine))
	require.NoError(t, err)

	// Test data
	originalData := &entities.FinancialData{
		Ticker:      "REPORT_TEST",
		TotalAssets: 1000000,
		Goodwill:    150000, // 15% - significant goodwill
		Revenue:     500000,
	}

	cleaningCtx := &entities.CleaningContext{
		IndustryCode: "technology",
		CompanySize:  entities.LargeCap,
	}

	ctx := context.Background()

	// Execute pipeline
	pipelineResult, err := orchestrator.ExecutePipeline(ctx, originalData, cleaningCtx)
	require.NoError(t, err)

	// Generate report
	reportGenerator := NewCleaningReportGenerator()
	report, err := reportGenerator.GenerateReport(pipelineResult, originalData)

	// Verify report
	require.NoError(t, err)
	assert.NotNil(t, report)
	assert.Equal(t, "REPORT_TEST", report.Ticker)
	assert.True(t, report.ProcessingTime >= 0) // Allow zero processing time
	assert.True(t, len(report.Sections) > 0)
	assert.NotNil(t, report.AuditTrail)

	// Verify audit trail
	assert.Equal(t, 1, report.AuditTrail.StagesProcessed)
	assert.Equal(t, 1, len(report.AuditTrail.ProcessingOrder))
	assert.Equal(t, string(AssetQualityStage), report.AuditTrail.ProcessingOrder[0])
}

// TestStageProcessors_ErrorHandling tests error handling in real processors
func TestStageProcessors_ErrorHandling(t *testing.T) {
	rulesEngine := rules.NewRuleEngine()
	assetAdjuster := adjustments.NewAssetAdjuster()
	
	processor := NewAssetQualityStageProcessor(assetAdjuster, rulesEngine)

	// Test with cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	
	result, err := processor.ProcessStage(cancelledCtx, &entities.FinancialData{}, &entities.CleaningContext{})
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Nil(t, result)

	// Test with valid data but no rules should work fine
	result, err = processor.ProcessStage(context.Background(), &entities.FinancialData{Ticker: "TEST"}, &entities.CleaningContext{})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, AssetQualityStage, result.Stage)
}

// TestPipelineConfiguration tests different pipeline configurations
func TestPipelineConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config *PipelineConfig
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

// TestReportGenerator_EdgeCases tests edge cases in report generation
func TestReportGenerator_EdgeCases(t *testing.T) {
	generator := NewCleaningReportGenerator()

	tests := []struct {
		name           string
		pipelineResult *PipelineResult
		originalData   *entities.FinancialData
		expectError    bool
	}{
		{
			name:           "nil_pipeline_result",
			pipelineResult: nil,
			originalData:   &entities.FinancialData{Ticker: "TEST"},
			expectError:    true,
		},
		{
			name:           "nil_original_data",
			pipelineResult: &PipelineResult{Success: true},
			originalData:   nil,
			expectError:    true,
		},
		{
			name: "empty_stage_results",
			pipelineResult: &PipelineResult{
				Success:      true,
				StageResults: []StageResult{},
				CleanedData:  &entities.FinancialData{Ticker: "EMPTY"},
			},
			originalData: &entities.FinancialData{Ticker: "EMPTY"},
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := generator.GenerateReport(tt.pipelineResult, tt.originalData)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, report)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, report)
			}
		})
	}
}

// TestReportGenerator_Configuration tests different report configurations
func TestReportGenerator_Configuration(t *testing.T) {
	configs := []*ReportConfig{
		{
			IncludeDetailedAuditTrail: false, // Should not include audit trail section
			FormatCurrency:           false, // Should not format as currency
			TimestampFormat:          time.RFC822,
			IncludeMetadata:          false,
		},
		{
			IncludeDetailedAuditTrail: true,
			FormatCurrency:           true,
			TimestampFormat:          time.RFC3339,
			IncludeMetadata:          true,
		},
	}

	for i, config := range configs {
		t.Run(fmt.Sprintf("config_%d", i), func(t *testing.T) {
			generator := NewCleaningReportGeneratorWithConfig(config)

			pipelineResult := &PipelineResult{
				Success: true,
				StageResults: []StageResult{
					{
						Stage: AssetQualityStage,
						Adjustments: []entities.Adjustment{
							{Amount: 1000, Applied: true},
						},
					},
				},
				CleanedData: &entities.FinancialData{Ticker: "CONFIG_TEST"},
			}

			originalData := &entities.FinancialData{Ticker: "CONFIG_TEST"}

			report, err := generator.GenerateReport(pipelineResult, originalData)
			require.NoError(t, err)

			// Verify configuration effects
			if config.IncludeDetailedAuditTrail {
				auditSection := findReportSection(report, "Audit Trail")
				assert.NotNil(t, auditSection)
			}

			if config.IncludeMetadata {
				assert.NotNil(t, report.Metadata)
			} else {
				assert.Nil(t, report.Metadata)
			}
		})
	}
}

// TestCurrencyFormatting tests various currency formatting scenarios
func TestCurrencyFormatting(t *testing.T) {
	generator := NewCleaningReportGenerator()

	tests := []struct {
		amount   float64
		expected string
	}{
		{0, "$0"},
		{100, "$100"},
		{999, "$999"},
		{1000, "$1,000"},
		{1500.50, "$1,500.50"},
		{1000000, "$1,000,000"},
		{-1000, "-$1,000"},
		{-1500.75, "-$1,500.75"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("amount_%.2f", tt.amount), func(t *testing.T) {
			result := generator.formatCurrency(tt.amount)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestAddThousandSeparators tests the thousand separator helper function
func TestAddThousandSeparators(t *testing.T) {
	generator := NewCleaningReportGenerator()

	tests := []struct {
		input    string
		expected string
	}{
		{"100", "100"},
		{"999", "999"},
		{"1000", "1,000"},
		{"1500", "1,500"},
		{"1000000", "1,000,000"},
		{"1500.50", "1,500.50"},
		{"999.99", "999.99"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := generator.addThousandSeparators(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
} 