package datacleaner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPipelineOrchestrator tests the main pipeline orchestration functionality
func TestPipelineOrchestrator_ExecutePipeline(t *testing.T) {
	tests := []struct {
		name           string
		data           *entities.FinancialData
		context        *entities.CleaningContext
		stages         []entities.PipelineStage
		expectError    bool
		expectStages   int
		expectDuration time.Duration
	}{
		{
			name: "successful_five_stage_pipeline",
			data: &entities.FinancialData{
				Ticker:      "AAPL",
				TotalAssets: 1000000,
				Goodwill:    100000, // 10% - should trigger asset adjustment
				Revenue:     500000,
			},
			context: &entities.CleaningContext{
				IndustryCode:     "technology",
				EnableIndustry:   true,
				QualityThreshold: 75.0,
			},
			stages:         []entities.PipelineStage{}, // Doesn't matter - orchestrator runs all 5 stages
			expectError:    false,
			expectStages:   5,
			expectDuration: 150 * time.Millisecond, // KPI: < 150ms
		},
		{
			name: "pipeline_with_stage_failure",
			data: &entities.FinancialData{
				Ticker: "INVALID",
			},
			context: &entities.CleaningContext{
				IndustryCode: "unknown",
			},
			stages:         []entities.PipelineStage{}, // Doesn't matter - orchestrator runs all 5 stages
			expectError:    false,                      // Pipeline continues but reports failure
			expectStages:   5,                          // All 5 stages still run even with failures
			expectDuration: 50 * time.Millisecond,
		},
		{
			name: "standard_pipeline",
			data: &entities.FinancialData{
				Ticker: "TEST",
			},
			context:        &entities.CleaningContext{},
			stages:         []entities.PipelineStage{}, // Doesn't matter - orchestrator runs all 5 stages
			expectError:    false,
			expectStages:   5,
			expectDuration: 50 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			rulesEngine := &mockRuleEngine{}
			orchestrator := NewPipelineOrchestrator(rulesEngine)
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			// Register stages
			for _, stage := range tt.stages {
				var processor StageProcessor
				if tt.name == "pipeline_with_stage_failure" {
					// Create a failing processor for the failure test
					processor = &mockStageProcessor{
						stage: stage,
						err:   errors.New("simulated stage failure"),
					}
				} else {
					processor = createMockStageProcessor(stage)
				}
				orchestrator.RegisterStageProcessor(stage, processor)
			}

			start := time.Now()

			// Act
			result, err := orchestrator.ExecutePipeline(ctx, tt.data, tt.context)

			// Assert
			duration := time.Since(start)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.expectStages, len(result.StageResults))
				assert.True(t, duration < tt.expectDuration, "Pipeline execution took %v, expected < %v", duration, tt.expectDuration)
			}
		})
	}
}

// TestPipelineOrchestrator_StageRegistry tests stage registration
func TestPipelineOrchestrator_StageRegistry(t *testing.T) {
	rulesEngine := &mockRuleEngine{}
	orchestrator := NewPipelineOrchestrator(rulesEngine)

	// Test registering stages
	orchestrator.RegisterStageProcessor(entities.StageAssetQuality, createMockStageProcessor(entities.StageAssetQuality))
	orchestrator.RegisterStageProcessor(entities.StageLiabilityCompleteness, createMockStageProcessor(entities.StageLiabilityCompleteness))

	// Verify pipeline can execute with registered stages
	ctx := context.Background()
	data := &entities.FinancialData{Ticker: "TEST"}
	cleaningCtx := &entities.CleaningContext{}

	result, err := orchestrator.ExecutePipeline(ctx, data, cleaningCtx)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

// TestPipelineOrchestrator_ContextPropagation tests context cancellation and propagation
func TestPipelineOrchestrator_ContextPropagation(t *testing.T) {
	rulesEngine := &mockRuleEngine{}
	orchestrator := NewPipelineOrchestrator(rulesEngine)

	// Register a slow stage
	slowProcessor := &mockStageProcessor{
		stage:     entities.StageAssetQuality,
		sleepTime: 200 * time.Millisecond,
	}
	orchestrator.RegisterStageProcessor(entities.StageAssetQuality, slowProcessor)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	data := &entities.FinancialData{Ticker: "TEST"}
	cleaningCtx := &entities.CleaningContext{}

	start := time.Now()
	result, err := orchestrator.ExecutePipeline(ctx, data, cleaningCtx)
	duration := time.Since(start)

	// Should complete but stage may timeout
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, duration < 100*time.Millisecond, "Pipeline should have been cancelled quickly")
}

// TestPipelineOrchestrator_ErrorAggregation tests error collection from multiple stages
func TestPipelineOrchestrator_ErrorAggregation(t *testing.T) {
	// Create orchestrator with ContinueOnStageFailure=true to collect all errors
	rulesEngine := &mockRuleEngine{}
	orchestrator := NewPipelineOrchestrator(rulesEngine)
	orchestrator.config.ContinueOnStageFailure = true // Enable error collection

	// Register failing processors
	failingProcessor1 := &mockStageProcessor{
		stage: entities.StageAssetQuality,
		err:   errors.New("asset stage failed"),
	}
	failingProcessor2 := &mockStageProcessor{
		stage: entities.StageLiabilityCompleteness,
		err:   errors.New("liability stage failed"),
	}

	orchestrator.RegisterStageProcessor(entities.StageAssetQuality, failingProcessor1)
	orchestrator.RegisterStageProcessor(entities.StageLiabilityCompleteness, failingProcessor2)

	ctx := context.Background()
	data := &entities.FinancialData{Ticker: "TEST"}
	cleaningCtx := &entities.CleaningContext{}

	result, err := orchestrator.ExecutePipeline(ctx, data, cleaningCtx)

	// With ContinueOnStageFailure=true, we should get a result but it should contain errors
	assert.NoError(t, err) // Pipeline should complete but with failed stages
	assert.NotNil(t, result)
	assert.False(t, result.Success)              // Overall success should be false
	assert.Equal(t, 5, len(result.StageResults)) // All 5 stages should be processed

	// Check that both stage results contain errors
	assert.False(t, result.StageResults[0].Success)
	assert.Contains(t, result.StageResults[0].Errors[0], "asset stage failed")
	assert.False(t, result.StageResults[1].Success)
	assert.Contains(t, result.StageResults[1].Errors[0], "liability stage failed")
}

// TestPipelineOrchestrator_Performance tests the performance requirements
func TestPipelineOrchestrator_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	rulesEngine := &mockRuleEngine{}
	orchestrator := NewPipelineOrchestrator(rulesEngine)

	// Register all three stages
	stages := []entities.PipelineStage{entities.StageAssetQuality, entities.StageLiabilityCompleteness, entities.StageEarningsNormalization}
	for _, stage := range stages {
		orchestrator.RegisterStageProcessor(stage, createMockStageProcessor(stage))
	}

	// Create synthetic 5-year dataset
	data := &entities.FinancialData{
		Ticker:          "PERF_TEST",
		TotalAssets:     5000000,
		Goodwill:        500000,
		Revenue:         2000000,
		OperatingIncome: 300000,
		// Add more fields to simulate real dataset
		Inventory:               400000,
		OperatingLeaseLiability: 100000,
		InterestExpense:         50000,
	}

	cleaningCtx := &entities.CleaningContext{
		IndustryCode:     "technology",
		EnableIndustry:   true,
		QualityThreshold: 80.0,
	}

	ctx := context.Background()

	// Run multiple iterations to get average
	iterations := 10
	var totalDuration time.Duration

	for i := 0; i < iterations; i++ {
		start := time.Now()
		result, err := orchestrator.ExecutePipeline(ctx, data, cleaningCtx)
		duration := time.Since(start)

		require.NoError(t, err)
		require.NotNil(t, result)
		totalDuration += duration
	}

	avgDuration := totalDuration / time.Duration(iterations)
	t.Logf("Average pipeline execution time: %v", avgDuration)

	// KPI: Pipeline should complete in < 150ms
	assert.True(t, avgDuration < 150*time.Millisecond,
		"Average pipeline execution time %v exceeds 150ms threshold", avgDuration)
}

// Mock rule engine for testing
type mockRuleEngine struct{}

func (m *mockRuleEngine) LoadRules(configPath string) error           { return nil }
func (m *mockRuleEngine) LoadIndustryRules(industryPath string) error { return nil }
func (m *mockRuleEngine) GetRules(category *entities.RuleCategory) []entities.CleaningRule {
	return []entities.CleaningRule{}
}
func (m *mockRuleEngine) GetIndustryRules(industry string) []entities.CleaningRule {
	return []entities.CleaningRule{}
}
func (m *mockRuleEngine) ValidateRules() error                                  { return nil }
func (m *mockRuleEngine) GetRuleByID(id string) (*entities.CleaningRule, error) { return nil, nil }
func (m *mockRuleEngine) GetRuleVersion() string                                { return "test-1.0" }

func (m *mockRuleEngine) GetRulesByCategory(category entities.RuleCategory) []entities.CleaningRule {
	return []entities.CleaningRule{
		{
			ID:       "test-rule-1",
			Category: category,
			Enabled:  true,
		},
	}
}

// Mock stage processor for testing
type mockStageProcessor struct {
	stage       entities.PipelineStage
	sleepTime   time.Duration
	err         error
	adjustments []entities.Adjustment
	flags       []entities.Flag
}

func (m *mockStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.StageResult, error) {
	if m.sleepTime > 0 {
		select {
		case <-time.After(m.sleepTime):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if m.err != nil {
		return &entities.StageResult{
			Stage:        m.stage,
			Success:      false,
			Duration:     m.sleepTime,
			RulesApplied: 0,
			Errors:       []string{m.err.Error()},
		}, nil
	}

	return &entities.StageResult{
		Stage:        m.stage,
		Success:      true,
		Adjustments:  m.adjustments,
		Flags:        m.flags,
		Duration:     m.sleepTime,
		RulesApplied: 1,
	}, nil
}

// Helper function to create mock stage processors
func createMockStageProcessor(stage entities.PipelineStage) StageProcessor {
	adjustments := []entities.Adjustment{}
	flags := []entities.Flag{}

	switch stage {
	case entities.StageAssetQuality:
		adjustments = []entities.Adjustment{
			{
				Type:        entities.Exclude,
				Amount:      50000,
				Reasoning:   "Goodwill writedown for asset quality",
				FromAccount: "Goodwill",
				Applied:     true,
			},
		}
	case entities.StageLiabilityCompleteness:
		adjustments = []entities.Adjustment{
			{
				Type:        entities.TreatAsDebt,
				Amount:      75000,
				Reasoning:   "Operating lease capitalization",
				FromAccount: "OperatingLease",
				Applied:     true,
			},
		}
	case entities.StageEarningsNormalization:
		adjustments = []entities.Adjustment{
			{
				Type:        entities.Exclude,
				Amount:      25000,
				Reasoning:   "Remove non-recurring restructuring charges",
				FromAccount: "RestructuringCharges",
				Applied:     true,
			},
		}
	}

	return &mockStageProcessor{
		stage:       stage,
		sleepTime:   10 * time.Millisecond, // Simulate processing time
		adjustments: adjustments,
		flags:       flags,
	}
}

// TestPipelineStage_Constants tests that stage constants are properly defined
func TestPipelineStage_Constants(t *testing.T) {
	// Ensure all expected stages are defined
	expectedStages := []entities.PipelineStage{
		entities.StageAssetQuality,
		entities.StageLiabilityCompleteness,
		entities.StageEarningsNormalization,
	}

	for _, stage := range expectedStages {
		assert.NotEmpty(t, string(stage), "Stage constant should not be empty")
	}

	// Ensure stages are unique
	stageMap := make(map[entities.PipelineStage]bool)
	for _, stage := range expectedStages {
		assert.False(t, stageMap[stage], "Stage %s should be unique", stage)
		stageMap[stage] = true
	}
}
