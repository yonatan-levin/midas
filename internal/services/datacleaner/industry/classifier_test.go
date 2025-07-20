package industry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestNewIndustryClassifier(t *testing.T) {
	classifier := NewIndustryClassifier()
	assert.NotNil(t, classifier)
	assert.NotEmpty(t, classifier.sectorConfigs)

	// Verify default configurations are loaded
	techConfig, exists := classifier.GetSectorConfig("45")
	assert.True(t, exists)
	assert.Equal(t, "Information Technology", techConfig.SectorName)

	industrialsConfig, exists := classifier.GetSectorConfig("20")
	assert.True(t, exists)
	assert.Equal(t, "Industrials", industrialsConfig.SectorName)

	retailConfig, exists := classifier.GetSectorConfig("25")
	assert.True(t, exists)
	assert.Equal(t, "Consumer Discretionary", retailConfig.SectorName)
}

func TestIndustryClassifier_ClassifyIndustry_Technology(t *testing.T) {
	classifier := NewIndustryClassifier()

	tests := []struct {
		name           string
		ticker         string
		data           *entities.FinancialData
		expectedSector string
	}{
		{
			name:   "high_rd_intensity_tech",
			ticker: "TECH",
			data: &entities.FinancialData{
				Revenue:                1000000000, // $1B revenue
				ResearchAndDevelopment: 150000000,  // $150M R&D (15% intensity)
				TotalAssets:            2000000000, // $2B assets
				IntangibleAssets:       400000000,  // $400M intangibles (20%)
				StockBasedCompensation: 80000000,   // $80M stock comp (8%)
			},
			expectedSector: "45", // Technology
		},
		{
			name:   "known_tech_ticker",
			ticker: "AAPL",
			data: &entities.FinancialData{
				Revenue:     1000000000,
				TotalAssets: 2000000000,
			},
			expectedSector: "45", // Technology
		},
		{
			name:   "high_stock_compensation",
			ticker: "STARTUP",
			data: &entities.FinancialData{
				Revenue:                1000000000, // $1B revenue
				StockBasedCompensation: 60000000,   // $60M stock comp (6% intensity)
				TotalAssets:            2000000000,
				IntangibleAssets:       100000000, // $100M intangibles (5%)
			},
			expectedSector: "45", // Technology
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sectorConfig, err := classifier.ClassifyIndustry(tt.ticker, tt.data)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedSector, sectorConfig.SectorCode)
		})
	}
}

func TestIndustryClassifier_ClassifyIndustry_Manufacturing(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		TotalAssets:    2000000000, // $2B assets
		TangibleAssets: 1400000000, // $1.4B tangible (70% - capital intensive)
		Inventory:      400000000,  // $400M inventory (20%)
		Revenue:        1500000000, // $1.5B revenue
	}

	sectorConfig, err := classifier.ClassifyIndustry("MFG", data)
	require.NoError(t, err)
	assert.Equal(t, "20", sectorConfig.SectorCode) // Industrials
	assert.Equal(t, "Industrials", sectorConfig.SectorName)
}

func TestIndustryClassifier_ClassifyIndustry_Retail(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		TotalAssets:      1000000000, // $1B assets
		TangibleAssets:   600000000,  // $600M tangible (60% - asset-light)
		IntangibleAssets: 200000000,  // $200M intangibles (20% - brand value)
		Inventory:        200000000,  // $200M inventory (20%)
		Revenue:          2000000000, // $2B revenue
	}

	sectorConfig, err := classifier.ClassifyIndustry("RETAIL", data)
	require.NoError(t, err)
	assert.Equal(t, "25", sectorConfig.SectorCode) // Consumer Discretionary
	assert.Equal(t, "Consumer Discretionary", sectorConfig.SectorName)
}

func TestIndustryClassifier_ClassifyIndustry_Financial(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		TotalAssets:    10000000000, // $10B assets
		TangibleAssets: 2000000000,  // $2B tangible (20% - mostly financial assets)
		Inventory:      0,           // No inventory
		TotalDebt:      3000000000,  // $3B debt (30% leverage)
		Revenue:        1000000000,  // $1B revenue
	}

	sectorConfig, err := classifier.ClassifyIndustry("BANK", data)
	require.NoError(t, err)
	// Financial companies should be detected, but since we don't have financial sector config,
	// it defaults to industrials (sector "20")
	assert.NotNil(t, sectorConfig)
	assert.Equal(t, "20", sectorConfig.SectorCode) // Defaults to industrials
}

func TestIndustryClassifier_ClassifyIndustry_Healthcare(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		Revenue:                1000000000, // $1B revenue
		ResearchAndDevelopment: 200000000,  // $200M R&D (20% intensity - high)
		StockBasedCompensation: 30000000,   // $30M stock comp (3% - lower than tech)
		TotalAssets:            2000000000, // $2B assets
	}

	sectorConfig, err := classifier.ClassifyIndustry("PHARMA", data)
	require.NoError(t, err)
	// Should classify as healthcare (but we don't have healthcare config yet, so defaults to industrials)
	assert.NotNil(t, sectorConfig)
}

func TestIndustryClassifier_ApplyIndustrySpecificThresholds(t *testing.T) {
	classifier := NewIndustryClassifier()

	// Get technology sector config
	techConfig, exists := classifier.GetSectorConfig("45")
	require.True(t, exists)

	// Create sample rules
	rules := []*entities.CleaningRule{
		{
			ID:       "goodwill_exclusion",
			Category: entities.AssetQuality,
			Enabled:  true,
		},
		{
			ID:       "stock_compensation",
			Category: entities.EarningsNormalization,
			Enabled:  true,
		},
		{
			ID:       "inventory_obsolescence",
			Category: entities.AssetQuality,
			Enabled:  true,
		},
	}

	// Apply industry-specific thresholds
	adjustedRules := classifier.ApplyIndustrySpecificThresholds(rules, techConfig)

	// Verify thresholds were applied
	for _, rule := range adjustedRules {
		assert.NotNil(t, rule.Threshold)

		switch rule.ID {
		case "goodwill_exclusion":
			assert.NotNil(t, rule.Threshold.PercentageOfAssets)
			assert.Equal(t, 0.15, *rule.Threshold.PercentageOfAssets) // Tech threshold
		case "stock_compensation":
			assert.NotNil(t, rule.Threshold.PercentageOfRevenue)
			assert.Equal(t, 0.08, *rule.Threshold.PercentageOfRevenue) // Tech threshold (8%)
		case "inventory_obsolescence":
			assert.NotNil(t, rule.Threshold.WritedownRate)
			assert.Equal(t, 0.50, *rule.Threshold.WritedownRate) // Tech threshold (50%)
		}
	}
}

func TestIndustryClassifier_GetAllSectorConfigs(t *testing.T) {
	classifier := NewIndustryClassifier()

	allConfigs := classifier.GetAllSectorConfigs()
	assert.NotEmpty(t, allConfigs)

	// Verify we have the expected sectors
	expectedSectors := []string{"45", "20", "25"}
	for _, sectorCode := range expectedSectors {
		config, exists := allConfigs[sectorCode]
		assert.True(t, exists, "Sector %s should exist", sectorCode)
		assert.NotEmpty(t, config.SectorName)
		assert.NotEmpty(t, config.CommonAdjustments)
		assert.NotEmpty(t, config.KeyMetrics)
	}
}

func TestIndustryClassifier_SectorConfigValidation(t *testing.T) {
	classifier := NewIndustryClassifier()

	// Test technology sector configuration
	techConfig, exists := classifier.GetSectorConfig("45")
	require.True(t, exists)

	// Verify risk profile
	assert.Equal(t, RiskHigh, techConfig.RiskProfile.TechnologyRisk)
	assert.Equal(t, RiskHigh, techConfig.RiskProfile.CompetitiveRisk)
	assert.Equal(t, RiskLow, techConfig.RiskProfile.CapitalIntensity)

	// Verify thresholds
	assert.Equal(t, 0.15, techConfig.Thresholds.GoodwillThreshold)
	assert.Equal(t, 0.08, techConfig.Thresholds.StockCompThreshold)        // High for tech
	assert.Equal(t, 0.50, techConfig.Thresholds.InventoryObsolescenceRate) // High obsolescence

	// Verify characteristics
	assert.False(t, techConfig.Characteristics.AssetHeavy)
	assert.True(t, techConfig.Characteristics.IntangibleIntensive)
	assert.True(t, techConfig.Characteristics.HighRDIntensity)
	assert.True(t, techConfig.Characteristics.HighStockCompensation)

	// Test industrials sector configuration
	industrialsConfig, exists := classifier.GetSectorConfig("20")
	require.True(t, exists)

	// Verify risk profile
	assert.Equal(t, RiskHigh, industrialsConfig.RiskProfile.CyclicalityRisk)
	assert.Equal(t, RiskHigh, industrialsConfig.RiskProfile.CapitalIntensity)

	// Verify characteristics
	assert.True(t, industrialsConfig.Characteristics.AssetHeavy)
	assert.True(t, industrialsConfig.Characteristics.InventoryIntensive)
	assert.True(t, industrialsConfig.Characteristics.FrequentRestructuring)
}

func TestIndustryClassifier_ErrorHandling(t *testing.T) {
	classifier := NewIndustryClassifier()

	// Test with nil data
	sectorConfig, err := classifier.ClassifyIndustry("TEST", nil)
	assert.Error(t, err)
	assert.Nil(t, sectorConfig)
	assert.Contains(t, err.Error(), "financial data is required")

	// Test with non-existent sector
	_, exists := classifier.GetSectorConfig("99")
	assert.False(t, exists)

	// Test ApplyIndustrySpecificThresholds with nil config
	rules := []*entities.CleaningRule{{ID: "test", Enabled: true}}
	adjustedRules := classifier.ApplyIndustrySpecificThresholds(rules, nil)
	assert.Equal(t, rules, adjustedRules) // Should return original rules unchanged
}
