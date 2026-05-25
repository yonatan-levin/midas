package datacleaner

import (
	"context"
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/adjustments"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/rules"
)

// StageProcessor defines the interface for processing a pipeline stage
type StageProcessor interface {
	ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.StageResult, error)
}

// PipelineOrchestrator manages the execution of data cleaning stages
type PipelineOrchestrator struct {
	stageProcessors map[entities.PipelineStage]StageProcessor
	config          *PipelineConfig
	rulesEngine     rules.RuleEngine
}

// PipelineConfig holds configuration for pipeline execution
type PipelineConfig struct {
	EnableParallelProcessing bool          `json:"enable_parallel_processing"`
	StageTimeout             time.Duration `json:"stage_timeout"`
	ContinueOnStageFailure   bool          `json:"continue_on_stage_failure"`
}

// NewPipelineOrchestrator creates a new pipeline orchestrator
func NewPipelineOrchestrator(rulesEngine rules.RuleEngine) *PipelineOrchestrator {
	orchestrator := &PipelineOrchestrator{
		stageProcessors: make(map[entities.PipelineStage]StageProcessor),
		config: &PipelineConfig{
			EnableParallelProcessing: false, // Sequential by default for consistency
			StageTimeout:             30 * time.Second,
			ContinueOnStageFailure:   true,
		},
		rulesEngine: rulesEngine,
	}

	// Register default stage processors
	orchestrator.registerDefaultProcessors()

	return orchestrator
}

// registerDefaultProcessors registers the default stage processors
func (po *PipelineOrchestrator) registerDefaultProcessors() {
	po.stageProcessors[entities.StageAssetQuality] = &AssetQualityStageProcessor{
		assetAdjuster: adjustments.NewAssetAdjuster(),
		rulesEngine:   po.rulesEngine,
	}
	// Create mock AI service for pipeline - will be replaced with proper DI
	mockAI := ai.NewMockAIService(&ai.AIServiceConfig{})
	po.stageProcessors[entities.StageLiabilityCompleteness] = &LiabilityCompletenessStageProcessor{
		liabilityAdjuster: adjustments.NewLiabilityAdjuster(mockAI, nil),
		rulesEngine:       po.rulesEngine,
	}
	po.stageProcessors[entities.StageEarningsNormalization] = &EarningsNormalizationStageProcessor{
		earningsAdjuster: adjustments.NewEarningsAdjuster(),
		rulesEngine:      po.rulesEngine,
	}
	po.stageProcessors[entities.StageQualityAssessment] = &QualityAssessmentStageProcessor{
		rulesEngine: po.rulesEngine,
	}
	po.stageProcessors[entities.StageFlagging] = &FlaggingStageProcessor{
		rulesEngine: po.rulesEngine,
	}
}

// RegisterStageProcessor registers a custom stage processor
func (po *PipelineOrchestrator) RegisterStageProcessor(stage entities.PipelineStage, processor StageProcessor) {
	po.stageProcessors[stage] = processor
}

// ExecutePipeline executes the complete data cleaning pipeline
func (po *PipelineOrchestrator) ExecutePipeline(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.PipelineResult, error) {
	start := time.Now()

	// Define processing order
	stages := []entities.PipelineStage{
		entities.StageAssetQuality,
		entities.StageLiabilityCompleteness,
		entities.StageEarningsNormalization,
		entities.StageQualityAssessment,
		entities.StageFlagging,
	}

	result := &entities.PipelineResult{
		Success:       true,
		StageResults:  make([]entities.StageResult, 0, len(stages)),
		CleanedData:   data, // Start with original data
		TotalDuration: 0,
	}

	// Choose processing approach based on configuration
	if po.config.EnableParallelProcessing {
		return po.executeParallelPipeline(ctx, stages, result, cleaningCtx)
	}

	// Process each stage sequentially
	for _, stage := range stages {
		stageStart := time.Now()

		processor, exists := po.stageProcessors[stage]
		if !exists {
			// Create a warning for missing processor
			stageResult := entities.StageResult{
				Stage:        stage,
				Success:      false,
				Duration:     time.Since(stageStart),
				RulesApplied: 0,
				Errors:       []string{fmt.Sprintf("no processor registered for stage %s", stage)},
			}
			result.StageResults = append(result.StageResults, stageResult)

			if !po.config.ContinueOnStageFailure {
				result.Success = false
				break
			}
			continue
		}

		// Execute stage with timeout
		stageCtx, cancel := context.WithTimeout(ctx, po.config.StageTimeout)
		stageResult, err := processor.ProcessStage(stageCtx, result.CleanedData, cleaningCtx)
		cancel()

		if err != nil {
			stageResult = &entities.StageResult{
				Stage:        stage,
				Success:      false,
				Duration:     time.Since(stageStart),
				RulesApplied: 0,
				Errors:       []string{err.Error()},
			}
		}

		// Update stage duration if not set
		if stageResult.Duration == 0 {
			stageResult.Duration = time.Since(stageStart)
		}

		result.StageResults = append(result.StageResults, *stageResult)

		// Check if stage failed
		if !stageResult.Success {
			if !po.config.ContinueOnStageFailure {
				result.Success = false
				break
			}
		}
	}

	// Calculate total duration and summary
	result.TotalDuration = time.Since(start)
	result.Summary = po.calculateSummary(result)

	// Check if any stage failed - if so, overall success should be false
	for _, stageResult := range result.StageResults {
		if !stageResult.Success {
			result.Success = false
			break
		}
	}

	return result, nil
}

// calculateSummary calculates pipeline summary statistics
func (po *PipelineOrchestrator) calculateSummary(result *entities.PipelineResult) entities.PipelineSummary {
	summary := entities.PipelineSummary{
		StagesProcessed: len(result.StageResults),
	}

	for _, stageResult := range result.StageResults {
		summary.TotalAdjustments += len(stageResult.Adjustments)
		summary.TotalFlags += len(stageResult.Flags)
		summary.TotalRulesApplied += stageResult.RulesApplied
		summary.ErrorCount += len(stageResult.Errors)
		summary.WarningCount += len(stageResult.Warnings)
	}

	return summary
}

// AssetQualityStageProcessor handles Category A asset quality adjustments
type AssetQualityStageProcessor struct {
	assetAdjuster *adjustments.AssetAdjuster
	rulesEngine   rules.RuleEngine
}

// ProcessStage processes the asset quality stage
func (asp *AssetQualityStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.StageResult, error) {
	start := time.Now()

	// Get asset quality rules
	assetRules := asp.rulesEngine.GetRulesByCategory(entities.AssetQuality)
	if len(assetRules) == 0 {
		return &entities.StageResult{
			Stage:        entities.StageAssetQuality,
			Success:      true,
			Duration:     time.Since(start),
			RulesApplied: 0,
			Warnings:     []string{"no asset quality rules found"},
		}, nil
	}

	// Convert rules to pointers for compatibility
	rulePointers := make([]*entities.CleaningRule, len(assetRules))
	for i := range assetRules {
		rulePointers[i] = &assetRules[i]
	}

	// Apply asset adjustments
	adjustmentResult := asp.assetAdjuster.ProcessAssetAdjustments(ctx, data, rulePointers, cleaningCtx)

	return &entities.StageResult{
		Stage:        entities.StageAssetQuality,
		Success:      true,
		Adjustments:  adjustmentResult.Adjustments,
		Flags:        adjustmentResult.Flags,
		Duration:     time.Since(start),
		RulesApplied: len(assetRules),
	}, nil
}

// LiabilityCompletenessStageProcessor handles Category B liability completeness
type LiabilityCompletenessStageProcessor struct {
	liabilityAdjuster *adjustments.LiabilityAdjuster
	rulesEngine       rules.RuleEngine
}

// ProcessStage processes the liability completeness stage
func (lsp *LiabilityCompletenessStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.StageResult, error) {
	start := time.Now()

	// Get liability rules
	liabilityRules := lsp.rulesEngine.GetRulesByCategory(entities.LiabilityCompleteness)
	if len(liabilityRules) == 0 {
		return &entities.StageResult{
			Stage:        entities.StageLiabilityCompleteness,
			Success:      true,
			Duration:     time.Since(start),
			RulesApplied: 0,
			Warnings:     []string{"no liability rules found"},
		}, nil
	}

	// Convert rules to pointers for compatibility
	rulePointers := make([]*entities.CleaningRule, len(liabilityRules))
	for i := range liabilityRules {
		rulePointers[i] = &liabilityRules[i]
	}

	// Apply liability adjustments
	adjustmentResult := lsp.liabilityAdjuster.ProcessLiabilityAdjustments(ctx, data, rulePointers, cleaningCtx)

	return &entities.StageResult{
		Stage:        entities.StageLiabilityCompleteness,
		Success:      true,
		Adjustments:  adjustmentResult.Adjustments,
		Flags:        adjustmentResult.Flags,
		Duration:     time.Since(start),
		RulesApplied: len(liabilityRules),
	}, nil
}

// EarningsNormalizationStageProcessor handles Category C earnings normalization
type EarningsNormalizationStageProcessor struct {
	earningsAdjuster *adjustments.EarningsAdjuster
	rulesEngine      rules.RuleEngine
}

// ProcessStage processes the earnings normalization stage
func (esp *EarningsNormalizationStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.StageResult, error) {
	start := time.Now()

	// Get earnings rules
	earningsRules := esp.rulesEngine.GetRulesByCategory(entities.EarningsNormalization)
	if len(earningsRules) == 0 {
		return &entities.StageResult{
			Stage:        entities.StageEarningsNormalization,
			Success:      true,
			Duration:     time.Since(start),
			RulesApplied: 0,
			Warnings:     []string{"no earnings rules found"},
		}, nil
	}

	// Convert rules to pointers for compatibility
	rulePointers := make([]*entities.CleaningRule, len(earningsRules))
	for i := range earningsRules {
		rulePointers[i] = &earningsRules[i]
	}

	// Apply earnings adjustments
	adjustmentResult := esp.earningsAdjuster.ProcessEarningsAdjustments(ctx, data, rulePointers, cleaningCtx)

	return &entities.StageResult{
		Stage:        entities.StageEarningsNormalization,
		Success:      true, // Stage is successful even if no adjustments needed
		Adjustments:  adjustmentResult.Adjustments,
		Flags:        adjustmentResult.Flags,
		Duration:     time.Since(start),
		RulesApplied: len(earningsRules),
	}, nil
}

// QualityAssessmentStageProcessor handles data quality assessment
type QualityAssessmentStageProcessor struct {
	rulesEngine rules.RuleEngine
}

// ProcessStage processes the quality assessment stage
func (qsp *QualityAssessmentStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.StageResult, error) {
	start := time.Now()

	// TODO: Define QualityAssessment rule category in entities
	// For now, return a basic successful result
	return &entities.StageResult{
		Stage:        entities.StageQualityAssessment,
		Success:      true,
		Duration:     time.Since(start),
		RulesApplied: 0,
		Warnings:     []string{"quality assessment not yet implemented"},
	}, nil
}

// FlaggingStageProcessor handles risk flagging
type FlaggingStageProcessor struct {
	rulesEngine rules.RuleEngine
}

// ProcessStage processes the flagging stage
func (fsp *FlaggingStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*entities.StageResult, error) {
	start := time.Now()

	// TODO: Define Flagging rule category in entities
	// For now, return a basic successful result
	return &entities.StageResult{
		Stage:        entities.StageFlagging,
		Success:      true,
		Duration:     time.Since(start),
		RulesApplied: 0,
		Warnings:     []string{"flagging not yet implemented"},
	}, nil
}

// executeParallelPipeline executes stages with parallelization for independent operations
func (po *PipelineOrchestrator) executeParallelPipeline(ctx context.Context, stages []entities.PipelineStage, result *entities.PipelineResult, cleaningCtx *entities.CleaningContext) (*entities.PipelineResult, error) {
	// Phase 1: Execute independent stages in parallel
	// Asset Quality, Liability Completeness, and Earnings Normalization can run concurrently
	// since they modify different parts of the financial data
	independentStages := []entities.PipelineStage{
		entities.StageAssetQuality,
		entities.StageLiabilityCompleteness,
		entities.StageEarningsNormalization,
	}

	// Phase 2: Execute dependent stages sequentially
	// Quality Assessment and Flagging depend on results from Phase 1
	dependentStages := []entities.PipelineStage{
		entities.StageQualityAssessment,
		entities.StageFlagging,
	}

	// Execute Phase 1: Independent stages in parallel
	phase1Results, err := po.executeStagesInParallel(ctx, independentStages, result.CleanedData, cleaningCtx)
	if err != nil {
		result.Success = false
		return result, err
	}

	// Merge parallel results
	result.StageResults = append(result.StageResults, phase1Results...)

	// Check if any critical stage failed
	for _, stageResult := range phase1Results {
		if !stageResult.Success && !po.config.ContinueOnStageFailure {
			result.Success = false
			return result, nil
		}
	}

	// Execute Phase 2: Dependent stages sequentially
	for _, stage := range dependentStages {
		stageStart := time.Now()

		processor, exists := po.stageProcessors[stage]
		if !exists {
			stageResult := entities.StageResult{
				Stage:        stage,
				Success:      false,
				Duration:     time.Since(stageStart),
				RulesApplied: 0,
				Errors:       []string{fmt.Sprintf("no processor registered for stage %s", stage)},
			}
			result.StageResults = append(result.StageResults, stageResult)

			if !po.config.ContinueOnStageFailure {
				result.Success = false
				break
			}
			continue
		}

		// Execute stage with timeout
		stageCtx, cancel := context.WithTimeout(ctx, po.config.StageTimeout)
		stageResult, err := processor.ProcessStage(stageCtx, result.CleanedData, cleaningCtx)
		cancel()

		if err != nil {
			stageResult = &entities.StageResult{
				Stage:        stage,
				Success:      false,
				Duration:     time.Since(stageStart),
				RulesApplied: 0,
				Errors:       []string{err.Error()},
			}
		}

		// Update stage duration if not set
		if stageResult.Duration == 0 {
			stageResult.Duration = time.Since(stageStart)
		}

		result.StageResults = append(result.StageResults, *stageResult)

		// Check if stage failed
		if !stageResult.Success && !po.config.ContinueOnStageFailure {
			result.Success = false
			break
		}
	}

	return result, nil
}

// executeStagesInParallel executes multiple stages concurrently and returns their results
func (po *PipelineOrchestrator) executeStagesInParallel(ctx context.Context, stages []entities.PipelineStage, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) ([]entities.StageResult, error) {
	type stageExecution struct {
		stage  entities.PipelineStage
		result *entities.StageResult
		err    error
	}

	// Create buffered channel for results
	resultChan := make(chan stageExecution, len(stages))

	// Launch goroutines for each stage
	for _, stage := range stages {
		go func(s entities.PipelineStage) {
			stageStart := time.Now()

			processor, exists := po.stageProcessors[s]
			if !exists {
				resultChan <- stageExecution{
					stage: s,
					result: &entities.StageResult{
						Stage:        s,
						Success:      false,
						Duration:     time.Since(stageStart),
						RulesApplied: 0,
						Errors:       []string{fmt.Sprintf("no processor registered for stage %s", s)},
					},
					err: nil,
				}
				return
			}

			// Execute stage with timeout
			stageCtx, cancel := context.WithTimeout(ctx, po.config.StageTimeout)
			defer cancel()

			// Create a copy of data for this stage to avoid race conditions
			stageCopyData := *data
			stageResult, err := processor.ProcessStage(stageCtx, &stageCopyData, cleaningCtx)

			if err != nil {
				stageResult = &entities.StageResult{
					Stage:        s,
					Success:      false,
					Duration:     time.Since(stageStart),
					RulesApplied: 0,
					Errors:       []string{err.Error()},
				}
			}

			// Update stage duration if not set
			if stageResult.Duration == 0 {
				stageResult.Duration = time.Since(stageStart)
			}

			resultChan <- stageExecution{
				stage:  s,
				result: stageResult,
				err:    err,
			}
		}(stage)
	}

	// Collect results
	results := make([]entities.StageResult, 0, len(stages))
	for i := 0; i < len(stages); i++ {
		execution := <-resultChan
		results = append(results, *execution.result)

		// Apply stage modifications back to the original data
		// Note: This is a simplified merge - in production, you might need
		// more sophisticated conflict resolution if stages modify overlapping fields
		if execution.result.Success {
			// TODO: Implement proper data merging logic based on stage type
			// For now, we assume stages don't conflict
			_ = execution.result // Acknowledge successful execution
		}
	}

	return results, nil
}
