package datacleaner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestDataCleanerService tests the main data cleaning service functionality
func TestDataCleanerService(t *testing.T) {
	tests := []struct {
		name   string
		testFn func(t *testing.T)
	}{
		{"BasicCleaning", testBasicCleaning},
		{"IndustryCleaning", testIndustryCleaning},
		{"QualityScoring", testQualityScoring},
		{"RiskFlagging", testRiskFlagging},
		{"AuditTrail", testAuditTrail},
		{"ErrorHandling", testErrorHandling},
		{"Caching", testCaching},
		{"ConcurrentCleaning", testConcurrentCleaning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.testFn(t)
		})
	}
}

func testBasicCleaning(t *testing.T) {
	// Create test configuration
	cfg := createTestConfig()
	ctx := context.Background()

	// Create service
	service, err := NewDataCleanerService(cfg)
	require.NoError(t, err, "Should create service without error")

	// Create test financial data with known cleaning issues
	data := createTestFinancialDataWithIssues()

	// Clean the data
	result, err := service.CleanFinancialData(ctx, data)
	require.NoError(t, err, "Should clean data without error")

	// Verify cleaning was applied
	assert.True(t, result.Success, "Cleaning should succeed")
	assert.Greater(t, result.RulesApplied, 0, "Should apply at least one rule")
	assert.Greater(t, len(result.Adjustments), 0, "Should make adjustments")
	assert.Greater(t, result.QualityScore, 0.0, "Should calculate quality score")
	assert.GreaterOrEqual(t, result.ProcessingTime, time.Duration(0), "Should track processing time")

	// Verify specific adjustments for known issues
	hasGoodwillAdjustment := false
	for _, adj := range result.Adjustments {
		if adj.RuleID == "goodwill_exclusion" {
			hasGoodwillAdjustment = true
			assert.Equal(t, "exclude", string(adj.Type))
			assert.Greater(t, adj.Amount, 0.0)
		}
	}
	assert.True(t, hasGoodwillAdjustment, "Should exclude goodwill")

	// Verify cleaned data doesn't contain excluded items
	assert.NotEqual(t, data, result.CleanedData, "Data should be modified")
}

func testIndustryCleaning(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()

	service, err := NewDataCleanerService(cfg)
	require.NoError(t, err)

	// Test technology industry cleaning
	techData := createTestTechCompanyData()
	// Industry code will be detected from company data

	result, err := service.CleanFinancialData(ctx, techData)
	require.NoError(t, err)

	// Should apply tech-specific rules
	hasRDCapitalizationFlag := false
	for _, flag := range result.Flags {
		if flag.RuleID == "rd_capitalization_review" {
			hasRDCapitalizationFlag = true
			assert.Equal(t, "critical", string(flag.Severity))
		}
	}
	assert.True(t, hasRDCapitalizationFlag, "Should flag R&D capitalization for tech companies")

	// Test retail industry cleaning
	retailData := createTestRetailCompanyData()
	// Industry code will be detected from company data

	retailResult, err := service.CleanFinancialData(ctx, retailData)
	require.NoError(t, err)

	// Should apply retail-specific inventory rules
	hasInventoryAdjustment := false
	for _, adj := range retailResult.Adjustments {
		if adj.RuleID == "obsolete_inventory" {
			hasInventoryAdjustment = true
			assert.Greater(t, adj.Amount, 0.0)
		}
	}
	assert.True(t, hasInventoryAdjustment, "Should adjust inventory for retail companies")
}

func testQualityScoring(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()

	service, err := NewDataCleanerService(cfg)
	require.NoError(t, err)

	// Test high-quality data
	cleanData := createTestCleanFinancialData()
	result, err := service.CleanFinancialData(ctx, cleanData)
	require.NoError(t, err)

	assert.Greater(t, result.QualityScore, 85.0, "Clean data should have high quality score")
	assert.Equal(t, 0, len(result.Flags), "Clean data should have no flags")

	// Test low-quality data with multiple issues
	problematicData := createTestProblematicFinancialData()
	problematicResult, err := service.CleanFinancialData(ctx, problematicData)
	require.NoError(t, err)

	assert.Less(t, problematicResult.QualityScore, 70.0, "Problematic data should have lower quality score")
	assert.Greater(t, len(problematicResult.Flags), 1, "Should flag multiple issues")
}

func testRiskFlagging(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()

	service, err := NewDataCleanerService(cfg)
	require.NoError(t, err)

	// Create data with various risk factors
	riskyData := createTestRiskyFinancialData()

	result, err := service.CleanFinancialData(ctx, riskyData)
	require.NoError(t, err)

	// Should flag various risks
	flagSeverities := make(map[string]int)
	for _, flag := range result.Flags {
		flagSeverities[string(flag.Severity)]++

		// Verify flag structure
		assert.NotEmpty(t, flag.ID, "Flag should have ID")
		assert.NotEmpty(t, flag.RuleID, "Flag should reference rule")
		assert.NotEmpty(t, flag.Description, "Flag should have description")
		assert.Greater(t, flag.Amount, 0.0, "Flag should have amount")
		assert.NotZero(t, flag.Timestamp, "Flag should have timestamp")
	}

	// Should have flags of different severities
	assert.Greater(t, flagSeverities["critical"], 0, "Should have critical flags")
	assert.Greater(t, flagSeverities["warning"], 0, "Should have warning flags")
}

func testAuditTrail(t *testing.T) {
	cfg := createTestConfig()
	cfg.DataCleaner.EnableAuditTrail = true
	ctx := context.Background()

	service, err := NewDataCleanerService(cfg)
	require.NoError(t, err)

	data := createTestFinancialDataWithIssues()
	result, err := service.CleanFinancialData(ctx, data)
	require.NoError(t, err)

	// Should have complete audit trail
	assert.Greater(t, len(result.Adjustments), 0, "Should have adjustments")

	for _, adj := range result.Adjustments {
		assert.NotEmpty(t, adj.ID, "Adjustment should have unique ID")
		assert.NotEmpty(t, adj.RuleID, "Adjustment should reference rule")
		assert.NotEmpty(t, adj.Category, "Adjustment should have category")
		assert.NotEmpty(t, adj.Reasoning, "Adjustment should have reasoning")
		assert.NotZero(t, adj.Timestamp, "Adjustment should have timestamp")
		assert.True(t, adj.Applied, "Adjustment should be marked as applied")
	}

	// Should have processing metadata
	assert.GreaterOrEqual(t, result.ProcessingTime, time.Duration(0), "Should track processing time")
	assert.True(t, result.Success, "Should track success status")
}

func testErrorHandling(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()

	service, err := NewDataCleanerService(cfg)
	require.NoError(t, err)

	// Test with nil data
	result, err := service.CleanFinancialData(ctx, nil)
	assert.Error(t, err, "Should error with nil data")
	assert.Nil(t, result, "Should return nil result on error")

	// Test with incomplete data
	incompleteData := &entities.FinancialData{
		Ticker: "TEST",
		CIK:    "0001234567",
		AsOf:   time.Now(),
		// Missing critical financial data (revenue, assets, etc.)
	}

	result, err = service.CleanFinancialData(ctx, incompleteData)
	assert.Error(t, err, "Should error with incomplete data")

	// Test context cancellation
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel() // Cancel immediately

	data := createTestFinancialDataWithIssues()
	result, err = service.CleanFinancialData(cancelCtx, data)
	assert.Error(t, err, "Should error with cancelled context")
}

func testCaching(t *testing.T) {
	cfg := createTestConfig()
	cfg.DataCleaner.EnableCaching = true
	ctx := context.Background()

	service, err := NewDataCleanerService(cfg)
	require.NoError(t, err)

	data := createTestFinancialDataWithIssues()

	// First cleaning
	result1, err := service.CleanFinancialData(ctx, data)
	require.NoError(t, err)
	time1 := result1.ProcessingTime

	// Second cleaning of same data should be faster (cached)
	result2, err := service.CleanFinancialData(ctx, data)
	require.NoError(t, err)
	time2 := result2.ProcessingTime

	// Results should be identical
	assert.Equal(t, result1.QualityScore, result2.QualityScore)
	assert.Equal(t, len(result1.Adjustments), len(result2.Adjustments))
	assert.Equal(t, len(result1.Flags), len(result2.Flags))

	// Second call should be faster or equal (if both are very fast)
	assert.LessOrEqual(t, time2, time1, "Cached call should be faster or equal")
}

func testConcurrentCleaning(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()

	service, err := NewDataCleanerService(cfg)
	require.NoError(t, err)

	// Test concurrent cleaning of different companies
	companies := []string{"AAPL", "MSFT", "GOOGL", "TSLA", "AMZN"}
	results := make(chan entities.CleaningResult, len(companies))
	errors := make(chan error, len(companies))

	// Start concurrent cleaning
	for _, ticker := range companies {
		go func(t string) {
			data := createTestFinancialDataForTicker(t)
			result, err := service.CleanFinancialData(ctx, data)
			if err != nil {
				errors <- err
			} else {
				results <- *result
			}
		}(ticker)
	}

	// Collect results
	var cleaningResults []entities.CleaningResult
	for i := 0; i < len(companies); i++ {
		select {
		case result := <-results:
			cleaningResults = append(cleaningResults, result)
		case err := <-errors:
			t.Errorf("Concurrent cleaning failed: %v", err)
		case <-time.After(30 * time.Second):
			t.Errorf("Concurrent cleaning timed out")
		}
	}

	// Verify all companies were processed
	assert.Equal(t, len(companies), len(cleaningResults), "Should process all companies")

	// Verify each result is valid
	for _, result := range cleaningResults {
		assert.True(t, result.Success, "Each cleaning should succeed")
		assert.Greater(t, result.QualityScore, 0.0, "Each should have quality score")
	}
}

// Helper functions for test data creation

func createTestConfig() *config.Config {
	return &config.Config{
		DataCleaner: config.DataCleanerConfig{
			RulesPath:           "../../../config/datacleaner/rules.json",
			IndustryRulesPath:   "../../../config/datacleaner/industry",
			SchemaPath:          "../../../config/datacleaner/schema.json",
			Enabled:             true,
			EnableAIIntegration: false,
			MinQualityScore:     60.0,
			HighQualityScore:    85.0,
			EnableRiskFlags:     true,
			CriticalThreshold:   0.3,
			WarningThreshold:    0.15,
			MaxConcurrentRules:  10,
			EnableCaching:       true,
			CacheTTL:            time.Hour * 6,
			EnableIndustryRules: true,
			EnableAuditTrail:    true,
			LogAdjustments:      true,
			LogFlags:            true,
		},
	}
}

func createTestFinancialDataWithIssues() *entities.FinancialData {
	return &entities.FinancialData{
		// Company identification
		Ticker: "TEST",
		CIK:    "1234567",
		AsOf:   time.Now().AddDate(0, -3, 0), // 3 months ago

		// Income Statement items
		Revenue:                   500000000, // $500M revenue
		OperatingIncome:           100000000, // $100M operating income
		NormalizedOperatingIncome: 95000000,  // $95M normalized (with adjustments)
		InterestExpense:           10000000,  // $10M interest expense
		TaxRate:                   0.25,      // 25% tax rate

		// Balance Sheet items with known cleaning issues
		TotalAssets:         1000000000, // $1B
		TangibleAssets:      650000000,  // $650M (calculated after adjustments)
		Goodwill:            200000000,  // $200M goodwill (should be excluded)
		OtherIntangibles:    150000000,  // $150M intangibles (may need writedown)
		TotalDebt:           300000000,  // $300M total debt
		InterestBearingDebt: 280000000,  // $280M interest-bearing debt

		// Inventory analysis
		Inventory:         100000000, // $100M inventory
		InventoryTurnover: 6.0,       // 6x turnover

		// Share information
		SharesOutstanding:        100000000, // 100M shares
		DilutedSharesOutstanding: 105000000, // 105M diluted shares

		// Filing metadata
		FilingPeriod: "2024Q3",
		FilingDate:   time.Now().AddDate(0, -3, 0), // 3 months ago

		// Data quality flags
		HasNormalizedData: true,
		MissingFields:     []string{},
	}
}

func createTestTechCompanyData() *entities.FinancialData {
	data := createTestFinancialDataWithIssues()
	data.Ticker = "TECH"
	data.CIK = "1234568"

	// Add tech-specific issues that should trigger R&D and intangible flags
	data.OtherIntangibles = 300000000                                              // Higher intangibles typical for tech (30% of assets)
	data.TangibleAssets = data.TotalAssets - data.Goodwill - data.OtherIntangibles // Recalculate tangible assets
	data.ResearchAndDevelopment = 50000000                                         // $50M R&D that should be reviewed for capitalization
	// Set industry context - this will be detected by the service
	data.Ticker = "AAPL" // Use a known tech ticker to trigger industry detection

	return data
}

func createTestRetailCompanyData() *entities.FinancialData {
	data := createTestFinancialDataWithIssues()
	data.Ticker = "RETAIL"
	data.CIK = "1234569"

	// Add retail-specific issues that should trigger inventory adjustments
	data.Inventory = 400000000   // Much higher inventory typical for retail (40% of assets)
	data.InventoryTurnover = 2.5 // Lower turnover that should trigger obsolescence concerns
	data.Goodwill = 150000000    // Some goodwill from acquisitions
	// Set industry context - use a known retail ticker
	data.Ticker = "WMT" // Use Walmart ticker to trigger retail industry detection

	return data
}

func createTestCleanFinancialData() *entities.FinancialData {
	return &entities.FinancialData{
		// Company identification
		Ticker: "CLEAN",
		CIK:    "1234570",
		AsOf:   time.Now().AddDate(0, -1, 0), // 1 month ago

		// Income Statement items (clean, no adjustments needed)
		Revenue:                   500000000, // $500M revenue
		OperatingIncome:           100000000, // $100M operating income
		NormalizedOperatingIncome: 100000000, // Same as operating income (clean)
		InterestExpense:           5000000,   // $5M interest expense
		TaxRate:                   0.25,      // 25% tax rate

		// Balance Sheet items (clean, minimal adjustments needed)
		TotalAssets:         1000000000, // $1B
		TangibleAssets:      990000000,  // $990M (most assets are tangible)
		Goodwill:            0,          // No goodwill
		OtherIntangibles:    10000000,   // Minimal intangibles (1% - below adjustment threshold)
		TotalDebt:           200000000,  // $200M total debt
		InterestBearingDebt: 200000000,  // $200M interest-bearing debt

		// Inventory analysis (good turnover)
		Inventory:         50000000, // $50M inventory
		InventoryTurnover: 10.0,     // High turnover (good)

		// Share information
		SharesOutstanding:        100000000, // 100M shares
		DilutedSharesOutstanding: 100000000, // 100M diluted shares (no dilution)

		// Filing metadata
		FilingPeriod: "2024Q3",
		FilingDate:   time.Now().AddDate(0, -1, 0), // 1 month ago

		// Data quality flags
		HasNormalizedData: false, // No normalization needed
		MissingFields:     []string{},
	}
}

func createTestProblematicFinancialData() *entities.FinancialData {
	data := createTestFinancialDataWithIssues()
	data.Ticker = "PROBLEM"
	data.CIK = "1234571"

	// Add multiple problematic issues that should significantly lower quality score
	data.Goodwill = 400000000                                                      // 40% of assets - very high
	data.OtherIntangibles = 300000000                                              // 30% of assets - very high
	data.Inventory = 200000000                                                     // Suspicious inventory growth
	data.InventoryTurnover = 2.0                                                   // Poor inventory turnover
	data.TangibleAssets = data.TotalAssets - data.Goodwill - data.OtherIntangibles // Recalculate

	// Add severe quality issues to lower the score
	data.ContingentLiabilities = 50000000     // $50M contingent liabilities (10% of revenue) - critical
	data.LitigationLiabilities = 25000000     // $25M litigation liabilities
	data.WorkingCapitalAdjustment = 100000000 // $100M working capital adjustment (20% of revenue) - critical
	data.RestructuringCharges = 25000000      // $25M restructuring (5% of revenue) - high
	data.StockBasedCompensation = 60000000    // $60M stock compensation (12% of revenue) - very high
	data.AssetSaleGains = 20000000            // $20M asset sale gains
	data.DerivativeGainsLosses = -15000000    // $15M derivative losses

	// Add data quality issues
	data.MissingFields = []string{"CashFlow", "CapEx", "WorkingCapital"} // Missing critical fields

	return data
}

func createTestRiskyFinancialData() *entities.FinancialData {
	data := createTestFinancialDataWithIssues()
	data.Ticker = "RISKY"
	data.CIK = "1234572"

	// Add various risk factors that should trigger flags
	data.Goodwill = 350000000                                                      // High goodwill (35% of assets)
	data.OtherIntangibles = 250000000                                              // High intangibles (25% of assets)
	data.TangibleAssets = data.TotalAssets - data.Goodwill - data.OtherIntangibles // Recalculate
	data.InventoryTurnover = 1.5                                                   // Very poor inventory turnover

	// Add critical risk factors to trigger critical flags
	data.ContingentLiabilities = 30000000    // $30M contingent liabilities (6% of revenue) - triggers critical flag
	data.LitigationLiabilities = 15000000    // $15M litigation liabilities
	data.WorkingCapitalAdjustment = 80000000 // $80M working capital adjustment (16% of revenue) - triggers critical flag

	// Add earnings normalization fields to trigger earnings flags
	data.RestructuringCharges = 15000000   // $15M restructuring (3% of revenue)
	data.StockBasedCompensation = 40000000 // $40M stock compensation (8% of revenue)
	data.AssetSaleGains = 10000000         // $10M asset sale gains

	return data
}

func createTestFinancialDataForTicker(ticker string) *entities.FinancialData {
	data := createTestFinancialDataWithIssues()
	data.Ticker = ticker
	data.CIK = "000" + ticker // Simple CIK based on ticker
	return data
}
