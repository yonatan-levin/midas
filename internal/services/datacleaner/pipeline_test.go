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
		stages         []PipelineStage
		expectError    bool
		expectStages   int
		expectDuration time.Duration
	}{
		{
			name: "successful_three_stage_pipeline",
			data: &entities.FinancialData{
				Ticker:      "AAPL",
				TotalAssets: 1000000,
				Goodwill:    100000, // 10% - should trigger asset adjustment
				Revenue:     500000,
			},
			context: &entities.CleaningContext{
				IndustryCode:     "technology",
				CompanySize:      entities.LargeCap,
				EnableIndustry:   true,
				QualityThreshold: 75.0,
			},
			stages:         []PipelineStage{AssetQualityStage, LiabilityCompletenessStage, EarningsNormalizationStage},
			expectError:    false,
			expectStages:   3,
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
			stages:         []PipelineStage{AssetQualityStage},
			expectError:    true,
			expectStages:   0,
			expectDuration: 50 * time.Millisecond,
		},
		{
			name: "empty_pipeline",
			data: &entities.FinancialData{
				Ticker: "TEST",
			},
			context:        &entities.CleaningContext{},
			stages:         []PipelineStage{},
			expectError:    false,
			expectStages:   0,
			expectDuration: 10 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			orchestrator := NewPipelineOrchestrator()
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
				err := orchestrator.RegisterStage(stage, processor)
				require.NoError(t, err)
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

// TestPipelineOrchestrator_StageRegistry tests stage registration and retrieval
func TestPipelineOrchestrator_StageRegistry(t *testing.T) {
	orchestrator := NewPipelineOrchestrator()

	// Test registering stages
	err := orchestrator.RegisterStage(AssetQualityStage, createMockStageProcessor(AssetQualityStage))
	assert.NoError(t, err)

	err = orchestrator.RegisterStage(LiabilityCompletenessStage, createMockStageProcessor(LiabilityCompletenessStage))
	assert.NoError(t, err)

	// Test duplicate registration
	err = orchestrator.RegisterStage(AssetQualityStage, createMockStageProcessor(AssetQualityStage))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")

	// Test getting registered stages
	stages := orchestrator.GetRegisteredStages()
	assert.Len(t, stages, 2)
	assert.Contains(t, stages, AssetQualityStage)
	assert.Contains(t, stages, LiabilityCompletenessStage)
}

// TestPipelineOrchestrator_ContextPropagation tests context cancellation and propagation
func TestPipelineOrchestrator_ContextPropagation(t *testing.T) {
	orchestrator := NewPipelineOrchestrator()

	// Register a slow stage
	slowProcessor := &mockStageProcessor{
		stage:     AssetQualityStage,
		sleepTime: 200 * time.Millisecond,
	}
	err := orchestrator.RegisterStage(AssetQualityStage, slowProcessor)
	require.NoError(t, err)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	data := &entities.FinancialData{Ticker: "TEST"}
	cleaningCtx := &entities.CleaningContext{}

	start := time.Now()
	result, err := orchestrator.ExecutePipeline(ctx, data, cleaningCtx)
	duration := time.Since(start)

	// Should return context timeout error
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	assert.Nil(t, result)
	assert.True(t, duration < 100*time.Millisecond, "Pipeline should have been cancelled quickly")
}

// TestPipelineOrchestrator_ErrorAggregation tests error collection from multiple stages
func TestPipelineOrchestrator_ErrorAggregation(t *testing.T) {
	// Create orchestrator with ContinueOnError=true to collect all errors
	config := &PipelineConfig{
		MaxStageTimeout: 30 * time.Second,
		ContinueOnError: true, // Continue processing to collect all errors
		EnableParallel:  false,
		LogLevel:        "info",
	}
	orchestrator := NewPipelineOrchestratorWithConfig(config)

	// Register failing processors
	failingProcessor1 := &mockStageProcessor{
		stage: AssetQualityStage,
		err:   errors.New("asset stage failed"),
	}
	failingProcessor2 := &mockStageProcessor{
		stage: LiabilityCompletenessStage,
		err:   errors.New("liability stage failed"),
	}

	err := orchestrator.RegisterStage(AssetQualityStage, failingProcessor1)
	require.NoError(t, err)
	err = orchestrator.RegisterStage(LiabilityCompletenessStage, failingProcessor2)
	require.NoError(t, err)

	ctx := context.Background()
	data := &entities.FinancialData{Ticker: "TEST"}
	cleaningCtx := &entities.CleaningContext{}

	result, err := orchestrator.ExecutePipeline(ctx, data, cleaningCtx)

	// With ContinueOnError=true, we should get a result but it should contain errors
	assert.NoError(t, err) // Pipeline should complete but with failed stages
	assert.NotNil(t, result)
	assert.False(t, result.Success)              // Overall success should be false
	assert.Equal(t, 2, len(result.StageResults)) // Both stages should be processed

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

	orchestrator := NewPipelineOrchestrator()

	// Register all three stages
	stages := []PipelineStage{AssetQualityStage, LiabilityCompletenessStage, EarningsNormalizationStage}
	for _, stage := range stages {
		err := orchestrator.RegisterStage(stage, createMockStageProcessor(stage))
		require.NoError(t, err)
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
		CompanySize:      entities.LargeCap,
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

// Mock stage processor for testing
type mockStageProcessor struct {
	stage       PipelineStage
	sleepTime   time.Duration
	err         error
	adjustments []entities.Adjustment
	flags       []entities.Flag
}

func (m *mockStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*StageResult, error) {
	if m.sleepTime > 0 {
		select {
		case <-time.After(m.sleepTime):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if m.err != nil {
		return nil, m.err
	}

	return &StageResult{
		Stage:        m.stage,
		Success:      true,
		Adjustments:  m.adjustments,
		Flags:        m.flags,
		Duration:     m.sleepTime,
		RulesApplied: 1,
	}, nil
}

// Helper function to create mock stage processors
func createMockStageProcessor(stage PipelineStage) StageProcessor {
	adjustments := []entities.Adjustment{}
	flags := []entities.Flag{}

	switch stage {
	case AssetQualityStage:
		adjustments = []entities.Adjustment{
			{
				Type:        entities.Exclude,
				Amount:      50000,
				Reasoning:   "Goodwill writedown for asset quality",
				FromAccount: "Goodwill",
				Applied:     true,
			},
		}
	case LiabilityCompletenessStage:
		adjustments = []entities.Adjustment{
			{
				Type:        entities.TreatAsDebt,
				Amount:      75000,
				Reasoning:   "Operating lease capitalization",
				FromAccount: "OperatingLease",
				Applied:     true,
			},
		}
	case EarningsNormalizationStage:
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
	expectedStages := []PipelineStage{
		AssetQualityStage,
		LiabilityCompletenessStage,
		EarningsNormalizationStage,
	}

	for _, stage := range expectedStages {
		assert.NotEmpty(t, string(stage), "Stage constant should not be empty")
	}

	// Ensure stages are unique
	stageMap := make(map[PipelineStage]bool)
	for _, stage := range expectedStages {
		assert.False(t, stageMap[stage], "Stage %s should be unique", stage)
		stageMap[stage] = true
	}
}
