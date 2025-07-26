package datacleaner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/adjustments"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/rules"
)

// PipelineStage represents a processing stage in the data cleaning pipeline
type PipelineStage string

// Pipeline stage constants - these define the sequential processing stages
const (
	AssetQualityStage          PipelineStage = "asset_quality"          // Category A: Asset quality adjustments
	LiabilityCompletenessStage PipelineStage = "liability_completeness" // Category B: Liability completeness
	EarningsNormalizationStage PipelineStage = "earnings_normalization" // Category C: Earnings normalization
)

// StageProcessor defines the interface for processing a pipeline stage
type StageProcessor interface {
	ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*StageResult, error)
}

// StageResult represents the result of processing a single pipeline stage
type StageResult struct {
	Stage        PipelineStage         `json:"stage"`
	Success      bool                  `json:"success"`
	Adjustments  []entities.Adjustment `json:"adjustments"`
	Flags        []entities.Flag       `json:"flags"`
	Duration     time.Duration         `json:"duration"`
	RulesApplied int                   `json:"rules_applied"`
	Errors       []string              `json:"errors,omitempty"`
	Warnings     []string              `json:"warnings,omitempty"`
}

// PipelineResult represents the complete result of pipeline execution
type PipelineResult struct {
	Success       bool                    `json:"success"`
	StageResults  []StageResult           `json:"stage_results"`
	TotalDuration time.Duration           `json:"total_duration"`
	CleanedData   *entities.FinancialData `json:"cleaned_data"`
	Summary       PipelineSummary         `json:"summary"`
}

// PipelineSummary provides aggregate statistics from pipeline execution
type PipelineSummary struct {
	TotalAdjustments  int `json:"total_adjustments"`
	TotalFlags        int `json:"total_flags"`
	TotalRulesApplied int `json:"total_rules_applied"`
	StagesProcessed   int `json:"stages_processed"`
	ErrorCount        int `json:"error_count"`
	WarningCount      int `json:"warning_count"`
}

// PipelineOrchestrator manages the execution of the multi-stage data cleaning pipeline
type PipelineOrchestrator struct {
	processors map[PipelineStage]StageProcessor
	mu         sync.RWMutex
	config     *PipelineConfig
}

// PipelineConfig holds configuration for pipeline execution
type PipelineConfig struct {
	MaxStageTimeout time.Duration `json:"max_stage_timeout"`
	ContinueOnError bool          `json:"continue_on_error"`
	EnableParallel  bool          `json:"enable_parallel"`
	LogLevel        string        `json:"log_level"`
}

// NewPipelineOrchestrator creates a new pipeline orchestrator instance
func NewPipelineOrchestrator() *PipelineOrchestrator {
	return &PipelineOrchestrator{
		processors: make(map[PipelineStage]StageProcessor),
		config: &PipelineConfig{
			MaxStageTimeout: 30 * time.Second,
			ContinueOnError: false,
			EnableParallel:  false, // Sequential for now to maintain data consistency
			LogLevel:        "info",
		},
	}
}

// NewPipelineOrchestratorWithConfig creates a new pipeline orchestrator with custom config
func NewPipelineOrchestratorWithConfig(config *PipelineConfig) *PipelineOrchestrator {
	return &PipelineOrchestrator{
		processors: make(map[PipelineStage]StageProcessor),
		config:     config,
	}
}

// RegisterStage registers a stage processor for a specific pipeline stage
func (po *PipelineOrchestrator) RegisterStage(stage PipelineStage, processor StageProcessor) error {
	po.mu.Lock()
	defer po.mu.Unlock()

	if _, exists := po.processors[stage]; exists {
		return fmt.Errorf("stage %s is already registered", stage)
	}

	if processor == nil {
		return fmt.Errorf("processor cannot be nil for stage %s", stage)
	}

	po.processors[stage] = processor
	return nil
}

// UnregisterStage removes a stage processor
func (po *PipelineOrchestrator) UnregisterStage(stage PipelineStage) {
	po.mu.Lock()
	defer po.mu.Unlock()
	delete(po.processors, stage)
}

// GetRegisteredStages returns a list of all registered pipeline stages
func (po *PipelineOrchestrator) GetRegisteredStages() []PipelineStage {
	po.mu.RLock()
	defer po.mu.RUnlock()

	stages := make([]PipelineStage, 0, len(po.processors))
	for stage := range po.processors {
		stages = append(stages, stage)
	}
	return stages
}

// ExecutePipeline executes the complete data cleaning pipeline
func (po *PipelineOrchestrator) ExecutePipeline(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*PipelineResult, error) {
	if data == nil {
		return nil, fmt.Errorf("financial data cannot be nil")
	}
	if cleaningCtx == nil {
		return nil, fmt.Errorf("cleaning context cannot be nil")
	}

	startTime := time.Now()

	// Create a copy of the data to avoid modifying the original
	cleanedData := *data

	// Initialize pipeline result
	result := &PipelineResult{
		Success:      true,
		StageResults: make([]StageResult, 0),
		CleanedData:  &cleanedData,
		Summary: PipelineSummary{
			StagesProcessed: 0,
		},
	}

	// Define the processing order for stages
	stageOrder := []PipelineStage{
		AssetQualityStage,
		LiabilityCompletenessStage,
		EarningsNormalizationStage,
	}

	// Execute stages in sequence
	var allErrors []string
	po.mu.RLock()
	defer po.mu.RUnlock()

	for _, stage := range stageOrder {
		processor, exists := po.processors[stage]
		if !exists {
			// Skip stages that aren't registered
			continue
		}

		// Check for context cancellation before each stage
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("pipeline cancelled: %w", err)
		}

		// Execute the stage with timeout
		stageCtx, cancel := context.WithTimeout(ctx, po.config.MaxStageTimeout)
		stageResult, err := po.executeStage(stageCtx, stage, processor, &cleanedData, cleaningCtx)
		cancel()

		if err != nil {
			stageResult = &StageResult{
				Stage:    stage,
				Success:  false,
				Duration: time.Since(startTime),
				Errors:   []string{err.Error()},
			}
			allErrors = append(allErrors, fmt.Sprintf("stage %s failed: %v", stage, err))

			if !po.config.ContinueOnError {
				result.Success = false
				result.TotalDuration = time.Since(startTime)
				return nil, fmt.Errorf("pipeline failed at stage %s: %w", stage, err)
			}
		}

		// Add stage result to pipeline result
		result.StageResults = append(result.StageResults, *stageResult)
		result.Summary.StagesProcessed++

		// Aggregate summary statistics
		result.Summary.TotalAdjustments += len(stageResult.Adjustments)
		result.Summary.TotalFlags += len(stageResult.Flags)
		result.Summary.TotalRulesApplied += stageResult.RulesApplied
		result.Summary.ErrorCount += len(stageResult.Errors)
		result.Summary.WarningCount += len(stageResult.Warnings)
	}

	// Calculate total duration
	result.TotalDuration = time.Since(startTime)

	// Set overall success based on any errors encountered
	if len(allErrors) > 0 {
		result.Success = false
		if !po.config.ContinueOnError {
			return nil, fmt.Errorf("pipeline execution failed: %s", strings.Join(allErrors, "; "))
		}
		// If ContinueOnError is true, return the result with Success=false but no error
	}

	return result, nil
}

// executeStage executes a single pipeline stage with proper error handling and timing
func (po *PipelineOrchestrator) executeStage(ctx context.Context, stage PipelineStage, processor StageProcessor, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*StageResult, error) {
	stageStart := time.Now()

	// Execute the stage processor
	result, err := processor.ProcessStage(ctx, data, cleaningCtx)
	if err != nil {
		return nil, fmt.Errorf("stage execution failed: %w", err)
	}

	// Ensure result has proper timing information
	if result != nil {
		result.Duration = time.Since(stageStart)
	}

	return result, nil
}

// AssetQualityStageProcessor implements StageProcessor for asset quality adjustments
type AssetQualityStageProcessor struct {
	adjuster    *adjustments.AssetAdjuster
	rulesEngine rules.RuleEngine
}

// NewAssetQualityStageProcessor creates a new asset quality stage processor
func NewAssetQualityStageProcessor(adjuster *adjustments.AssetAdjuster, rulesEngine rules.RuleEngine) *AssetQualityStageProcessor {
	return &AssetQualityStageProcessor{
		adjuster:    adjuster,
		rulesEngine: rulesEngine,
	}
}

// ProcessStage implements the StageProcessor interface for asset quality
func (asp *AssetQualityStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*StageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()

	// Get applicable asset quality rules
	rules := asp.rulesEngine.GetRulesByCategory(entities.AssetQuality)
	assetRules := make([]*entities.CleaningRule, 0)

	for i, rule := range rules {
		if rule.Enabled {
			assetRules = append(assetRules, &rules[i])
		}
	}

	// Apply asset adjustments
	result := asp.adjuster.ProcessAssetAdjustments(data, assetRules, cleaningCtx)

	return &StageResult{
		Stage:        AssetQualityStage,
		Success:      result.Applied,
		Adjustments:  result.Adjustments,
		Flags:        result.Flags,
		Duration:     time.Since(start),
		RulesApplied: len(assetRules),
	}, nil
}

// LiabilityCompletenessStageProcessor implements StageProcessor for liability completeness
type LiabilityCompletenessStageProcessor struct {
	adjuster    *adjustments.LiabilityAdjuster
	rulesEngine rules.RuleEngine
}

// NewLiabilityCompletenessStageProcessor creates a new liability completeness stage processor
func NewLiabilityCompletenessStageProcessor(adjuster *adjustments.LiabilityAdjuster, rulesEngine rules.RuleEngine) *LiabilityCompletenessStageProcessor {
	return &LiabilityCompletenessStageProcessor{
		adjuster:    adjuster,
		rulesEngine: rulesEngine,
	}
}

// ProcessStage implements the StageProcessor interface for liability completeness
func (lsp *LiabilityCompletenessStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*StageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()

	// Get applicable liability completeness rules
	rules := lsp.rulesEngine.GetRulesByCategory(entities.LiabilityCompleteness)
	liabilityRules := make([]*entities.CleaningRule, 0)

	for i, rule := range rules {
		if rule.Enabled {
			liabilityRules = append(liabilityRules, &rules[i])
		}
	}

	// Apply liability adjustments
	result := lsp.adjuster.ProcessLiabilityAdjustments(data, liabilityRules, cleaningCtx)

	return &StageResult{
		Stage:        LiabilityCompletenessStage,
		Success:      result.Applied,
		Adjustments:  result.Adjustments,
		Flags:        result.Flags,
		Duration:     time.Since(start),
		RulesApplied: len(liabilityRules),
	}, nil
}

// EarningsNormalizationStageProcessor implements StageProcessor for earnings normalization
type EarningsNormalizationStageProcessor struct {
	adjuster    *adjustments.EarningsAdjuster
	rulesEngine rules.RuleEngine
}

// NewEarningsNormalizationStageProcessor creates a new earnings normalization stage processor
func NewEarningsNormalizationStageProcessor(adjuster *adjustments.EarningsAdjuster, rulesEngine rules.RuleEngine) *EarningsNormalizationStageProcessor {
	return &EarningsNormalizationStageProcessor{
		adjuster:    adjuster,
		rulesEngine: rulesEngine,
	}
}

// ProcessStage implements the StageProcessor interface for earnings normalization
func (esp *EarningsNormalizationStageProcessor) ProcessStage(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) (*StageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()

	// Get applicable earnings normalization rules
	rules := esp.rulesEngine.GetRulesByCategory(entities.EarningsNormalization)
	earningsRules := make([]*entities.CleaningRule, 0)

	for i, rule := range rules {
		if rule.Enabled {
			earningsRules = append(earningsRules, &rules[i])
		}
	}

	// Apply earnings adjustments
	result := esp.adjuster.ProcessEarningsAdjustments(data, earningsRules, cleaningCtx)

	return &StageResult{
		Stage:        EarningsNormalizationStage,
		Success:      result.Applied,
		Adjustments:  result.Adjustments,
		Flags:        result.Flags,
		Duration:     time.Since(start),
		RulesApplied: len(earningsRules),
	}, nil
}
