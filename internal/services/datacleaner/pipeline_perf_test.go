package datacleaner

import (
	"context"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/rules"
)

// BenchmarkPipelineExecution_Sequential measures current sequential performance
func BenchmarkPipelineExecution_Sequential(b *testing.B) {
	// Create test financial data
	financialData := createTestFinancialDataForCleaning()

	// Create cleaning context
	cleaningCtx := &entities.CleaningContext{
		IndustryCode:     "334413", // Technology
		CompanySize:      "large",
		DataVintage:      time.Now().Add(-30 * 24 * time.Hour),
		EnableIndustry:   true,
		EnableCaching:    false, // Disable caching for consistent benchmarks
		QualityThreshold: 0.7,
	}

	// Create rules engine
	rulesEngine := rules.NewRuleEngine()

	// Create pipeline orchestrator with sequential processing
	orchestrator := NewPipelineOrchestrator(rulesEngine)
	orchestrator.config.EnableParallelProcessing = false

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create a copy for each iteration to avoid side effects
		testData := *financialData

		result, err := orchestrator.ExecutePipeline(ctx, &testData, cleaningCtx)
		if err != nil {
			b.Fatalf("Pipeline execution failed: %v", err)
		}

		// Prevent optimization
		_ = result
	}
}

// BenchmarkPipelineExecution_Parallel measures parallel performance
func BenchmarkPipelineExecution_Parallel(b *testing.B) {
	// Create test financial data
	financialData := createTestFinancialDataForCleaning()

	// Create cleaning context
	cleaningCtx := &entities.CleaningContext{
		IndustryCode:     "334413", // Technology
		CompanySize:      "large",
		DataVintage:      time.Now().Add(-30 * 24 * time.Hour),
		EnableIndustry:   true,
		EnableCaching:    false, // Disable caching for consistent benchmarks
		QualityThreshold: 0.7,
	}

	// Create rules engine
	rulesEngine := rules.NewRuleEngine()

	// Create pipeline orchestrator with parallel processing enabled
	orchestrator := NewPipelineOrchestrator(rulesEngine)
	orchestrator.config.EnableParallelProcessing = true // Enable parallel processing

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create a copy for each iteration to avoid side effects
		testData := *financialData

		result, err := orchestrator.ExecutePipeline(ctx, &testData, cleaningCtx)
		if err != nil {
			b.Fatalf("Pipeline execution failed: %v", err)
		}

		// Prevent optimization
		_ = result
	}
}

// BenchmarkStageProcessing_Individual measures individual stage performance
func BenchmarkStageProcessing_Individual(b *testing.B) {
	financialData := createTestFinancialDataForCleaning()
	cleaningCtx := &entities.CleaningContext{
		IndustryCode:     "334413",
		CompanySize:      "large",
		DataVintage:      time.Now().Add(-30 * 24 * time.Hour),
		EnableIndustry:   true,
		EnableCaching:    false,
		QualityThreshold: 0.7,
	}

	rulesEngine := rules.NewRuleEngine()
	orchestrator := NewPipelineOrchestrator(rulesEngine)
	ctx := context.Background()

	// Benchmark individual stages to understand their relative costs
	stages := []entities.PipelineStage{
		entities.StageAssetQuality,
		entities.StageLiabilityCompleteness,
		entities.StageEarningsNormalization,
		entities.StageQualityAssessment,
		entities.StageFlagging,
	}

	for _, stage := range stages {
		processor, exists := orchestrator.stageProcessors[stage]
		if !exists {
			continue
		}

		b.Run(string(stage), func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				testData := *financialData

				result, err := processor.ProcessStage(ctx, &testData, cleaningCtx)
				if err != nil {
					b.Fatalf("Stage %s failed: %v", stage, err)
				}

				_ = result // Prevent optimization
			}
		})
	}
}

// Helper function to create consistent test data
func createTestFinancialDataForCleaning() *entities.FinancialData {
	return &entities.FinancialData{
		Ticker:                    "AAPL",
		IndustryCode:              "334413",
		CIK:                       "0000320193",
		AsOf:                      time.Now(),
		OperatingIncome:           123500000000,
		NormalizedOperatingIncome: 120000000000,
		Revenue:                   383932000000,
		ResearchAndDevelopment:    29915000000,
		InterestExpense:           3920000000,
		TaxRate:                   0.20,

		// Asset values for asset quality stage
		TotalAssets:        381190000000,
		TangibleAssets:     350000000000,
		Goodwill:           25000000000,
		OtherIntangibles:   6190000000,
		IntangibleAssets:   31190000000,
		DeferredTaxAssets:  15000000000,
		ValuationAllowance: 500000000,

		// Liability values for liability completeness stage
		TotalDebt:               110000000000,
		InterestBearingDebt:     110000000000,
		OperatingLeaseLiability: 5000000000,
		PensionLiabilities:      2000000000,

		// Earnings normalization values
		RestructuringCharges:   500000000,
		AssetSaleGains:         200000000,
		LitigationSettlements:  100000000,
		StockBasedCompensation: 15000000000,

		// Inventory for dead inventory analysis
		Inventory:         6331000000,
		InventoryTurnover: 33.2,
		CostOfGoodsSold:   210000000000,

		// Share information
		SharesOutstanding:        15744231000,
		DilutedSharesOutstanding: 15812547000,

		// Metadata
		FilingPeriod:      "2023FY",
		FilingDate:        time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		HasNormalizedData: false, // Will be set to true after normalization
	}
}
