package industry

import (
	"context"
	"os"
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

	// Unambiguous retailer profile (think Target, Macy's):
	//   - High inventory: 22% of total assets
	//   - Tangible assets ~65% of total (stores, warehouses, fixtures)
	//   - Modest intangibles (~5% — brand value only, no acquired IP)
	//   - Zero R&D: retailers do not run R&D labs
	//   - Near-zero stock-based comp (< 1% of revenue): retailers pay cash
	//
	// This profile must NOT trip the isTechnologyCompany guard (no R&D, no SBC,
	// intangibles well below the 15% tech threshold) and must match the retail
	// predicate's inventory + tangibles branch.
	data := &entities.FinancialData{
		TotalAssets:            1_000_000_000, // $1B assets
		Inventory:              220_000_000,   // $220M inventory (22%)
		TangibleAssets:         650_000_000,   // $650M tangible (65%)
		IntangibleAssets:       50_000_000,    // $50M intangibles (5% — brand only)
		Revenue:                2_000_000_000, // $2B revenue
		ResearchAndDevelopment: 0,             // Retailers don't run R&D
		StockBasedCompensation: 10_000_000,    // $10M SBC (0.5% of revenue)
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

// TestIndustryClassifier_Classify_ExactNameMatching tests exact company name matching
// using a manually constructed config (distinct from the production config tests).
func TestIndustryClassifier_Classify_ExactNameMatching(t *testing.T) {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
		codesConfig: &industryCodesConfig{
			Version:     "test",
			DefaultCode: "NA",
			Mappings: []industryMapping{
				{
					Name:     "REIT",
					Code:     "REIT",
					Priority: 95,
					Matchers: struct {
						SICCodes   []string `json:"sic_codes"`
						NAICSCodes []string `json:"naics_codes"`
						Keywords   []string `json:"keywords"`
						Patterns   []string `json:"patterns"`
						ExactNames []string `json:"exact_names"`
					}{
						ExactNames: []string{"American Tower Corp", "Prologis Inc"},
					},
				},
			},
		},
	}

	result, err := classifier.Classify(context.Background(), "", "", "American Tower Corp")
	require.NoError(t, err)
	assert.Equal(t, "REIT", result)

	// Case-insensitive match
	result, err = classifier.Classify(context.Background(), "", "", "american tower corp")
	require.NoError(t, err)
	assert.Equal(t, "REIT", result)

	// No match
	result, err = classifier.Classify(context.Background(), "", "", "Random Company")
	require.NoError(t, err)
	assert.Equal(t, "NA", result)
}

// TestIndustryClassifier_Classify_ShortKeywordBoundary tests word-boundary handling
// for short keywords (<=3 chars) to prevent false matches inside longer words.
func TestIndustryClassifier_Classify_ShortKeywordBoundary(t *testing.T) {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
		codesConfig: &industryCodesConfig{
			Version:     "test",
			DefaultCode: "NA",
			Mappings: []industryMapping{
				{
					Name:     "Technology",
					Code:     "TECH",
					Priority: 100,
					Matchers: struct {
						SICCodes   []string `json:"sic_codes"`
						NAICSCodes []string `json:"naics_codes"`
						Keywords   []string `json:"keywords"`
						Patterns   []string `json:"patterns"`
						ExactNames []string `json:"exact_names"`
					}{
						Keywords: []string{"software", "ai", "cloud"},
					},
				},
			},
		},
	}

	tests := []struct {
		name        string
		companyName string
		expected    string
	}{
		{name: "Long keyword match", companyName: "Acme Software Corp", expected: "TECH"},
		{name: "Short keyword with word boundary", companyName: "OpenAI Inc", expected: "NA"},
		{name: "Short keyword standalone", companyName: "AI Corp", expected: "TECH"},
		{name: "Short keyword inside word should not match", companyName: "Retail Chains Inc", expected: "NA"},
		{name: "Cloud keyword match", companyName: "Big Cloud Solutions", expected: "TECH"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(context.Background(), "", "", tt.companyName)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIndustryClassifier_Classify_PatternMatching tests regex pattern matching
func TestIndustryClassifier_Classify_PatternMatching(t *testing.T) {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
		codesConfig: &industryCodesConfig{
			Version:     "test",
			DefaultCode: "NA",
			Mappings: []industryMapping{
				{
					Name:     "Energy",
					Code:     "ENERGY",
					Priority: 85,
					Matchers: struct {
						SICCodes   []string `json:"sic_codes"`
						NAICSCodes []string `json:"naics_codes"`
						Keywords   []string `json:"keywords"`
						Patterns   []string `json:"patterns"`
						ExactNames []string `json:"exact_names"`
					}{
						Patterns: []string{`oil\s+and\s+gas`, `petroleum`},
					},
				},
			},
		},
	}

	result, err := classifier.Classify(context.Background(), "", "", "Big Oil and Gas Company")
	require.NoError(t, err)
	assert.Equal(t, "ENERGY", result)

	result, err = classifier.Classify(context.Background(), "", "", "National Petroleum Corp")
	require.NoError(t, err)
	assert.Equal(t, "ENERGY", result)
}

// TestIndustryClassifier_Classify_NilCodesConfigError tests that Classify returns an error
// when the codes config is not loaded (direct construction without loading config file).
func TestIndustryClassifier_Classify_NilCodesConfigError(t *testing.T) {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
		codesConfig:   nil, // not loaded
	}

	result, err := classifier.Classify(context.Background(), "7372", "", "")
	assert.Error(t, err)
	assert.Equal(t, "NA", result)
	assert.Contains(t, err.Error(), "config not loaded")
}

// TestIndustryClassifier_Classify_PriorityOrderingDualKeywords tests that higher-priority
// mappings take precedence when both match the same keyword.
func TestIndustryClassifier_Classify_PriorityOrderingDualKeywords(t *testing.T) {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
		codesConfig: &industryCodesConfig{
			Version:     "test",
			DefaultCode: "NA",
			Mappings: []industryMapping{
				{
					Name:     "Generic Tech",
					Code:     "TECH",
					Priority: 50, // Lower priority
					Matchers: struct {
						SICCodes   []string `json:"sic_codes"`
						NAICSCodes []string `json:"naics_codes"`
						Keywords   []string `json:"keywords"`
						Patterns   []string `json:"patterns"`
						ExactNames []string `json:"exact_names"`
					}{
						Keywords: []string{"software"},
					},
				},
				{
					Name:     "SaaS Tech",
					Code:     "TECH_SAAS",
					Priority: 100, // Higher priority
					Matchers: struct {
						SICCodes   []string `json:"sic_codes"`
						NAICSCodes []string `json:"naics_codes"`
						Keywords   []string `json:"keywords"`
						Patterns   []string `json:"patterns"`
						ExactNames []string `json:"exact_names"`
					}{
						Keywords: []string{"software"},
					},
				},
			},
		},
	}

	result, err := classifier.Classify(context.Background(), "", "", "Big Software Inc")
	require.NoError(t, err)
	assert.Equal(t, "TECH_SAAS", result, "higher priority mapping should win")
}

// TestIndustryClassifier_LoadIndustryCodesConfig_InvalidJSON tests loading invalid JSON
func TestIndustryClassifier_LoadIndustryCodesConfig_InvalidJSON(t *testing.T) {
	classifier := NewIndustryClassifier()

	tmpFile := t.TempDir() + "/bad_industry_codes.json"
	err := os.WriteFile(tmpFile, []byte("not valid json"), 0644)
	require.NoError(t, err)

	err = classifier.LoadIndustryCodesConfig(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse")
}

// TestIndustryClassifier_isUtilitiesCompany_Qualifying tests the utilities detection
// heuristic directly (tangible > 80% AND inventory < 5%).
// Note: In ClassifyIndustry, isManufacturingCompany is checked before isUtilitiesCompany.
// Since tangible > 60% triggers manufacturing, utilities is currently unreachable
// through ClassifyIndustry. This tests the function in isolation.
func TestIndustryClassifier_isUtilitiesCompany_Qualifying(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		TotalAssets:    10000000000,
		TangibleAssets: 8500000000, // 85% tangible (> 80%)
		Inventory:      100000000,  // 1% inventory (< 5%)
		Revenue:        5000000000,
	}

	assert.True(t, classifier.isUtilitiesCompany(data),
		"should detect utilities with high tangible assets and low inventory")
}

// TestIndustryClassifier_isUtilitiesCompany_ZeroInventory tests utilities detection
// when inventory is exactly zero (common for pure utilities).
func TestIndustryClassifier_isUtilitiesCompany_ZeroInventory(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		TotalAssets:    10000000000,
		TangibleAssets: 9000000000, // 90% tangible
		Inventory:      0,          // no inventory at all
		Revenue:        5000000000,
	}

	assert.True(t, classifier.isUtilitiesCompany(data),
		"zero inventory is classic utilities profile")
}

// TestIndustryClassifier_isHealthcareCompany_Qualifying tests the healthcare detection
// heuristic directly (R&D > 15% AND stock comp < 5%).
// Note: In ClassifyIndustry, isTechnologyCompany is checked before isHealthcareCompany.
// Since R&D > 10% triggers tech, healthcare is currently unreachable through
// ClassifyIndustry for companies with R&D > 15%. This tests the function in isolation.
func TestIndustryClassifier_isHealthcareCompany_Qualifying(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		Revenue:                1000000000,
		ResearchAndDevelopment: 200000000, // 20% R&D intensity (> 15%)
		StockBasedCompensation: 30000000,  // 3% stock comp (< 5%)
		TotalAssets:            2000000000,
	}

	assert.True(t, classifier.isHealthcareCompany(data),
		"should detect healthcare with high R&D and low stock comp")
}

// TestIndustryClassifier_isHealthcareCompany_TooHighStockComp tests that companies
// with high stock comp are NOT classified as healthcare (they look more like tech).
func TestIndustryClassifier_isHealthcareCompany_TooHighStockComp(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		Revenue:                1000000000,
		ResearchAndDevelopment: 200000000, // 20% R&D intensity
		StockBasedCompensation: 60000000,  // 6% stock comp (> 5%)
		TotalAssets:            2000000000,
	}

	assert.False(t, classifier.isHealthcareCompany(data),
		"high stock comp suggests tech, not healthcare")
}

// TestIndustryClassifier_isHealthcareCompany_LowRD tests that companies with low R&D
// do not qualify as healthcare.
func TestIndustryClassifier_isHealthcareCompany_LowRD(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		Revenue:                1000000000,
		ResearchAndDevelopment: 100000000, // 10% R&D intensity (< 15%)
		StockBasedCompensation: 10000000,
		TotalAssets:            2000000000,
	}

	assert.False(t, classifier.isHealthcareCompany(data),
		"R&D below 15% should not qualify as healthcare")
}

// TestIndustryClassifier_isManufacturingCompany_HighInventory tests the high inventory path
// for manufacturing. Uses inventory > 30% (outside retail 10-30% range) to avoid
// triggering the retail classifier which is checked first.
func TestIndustryClassifier_isManufacturingCompany_HighInventory(t *testing.T) {
	classifier := NewIndustryClassifier()

	// Inventory > 30% of total assets falls outside retail range (10-30%), so retail
	// check returns false. Then manufacturing detects inventory > 15%.
	data := &entities.FinancialData{
		TotalAssets:    2000000000,
		TangibleAssets: 800000000, // 40% tangible (below 60% threshold)
		Inventory:      700000000, // 35% inventory (above 15%, but outside retail 10-30%)
		Revenue:        1500000000,
	}

	sectorConfig, err := classifier.ClassifyIndustry("MFG2", data)
	require.NoError(t, err)
	assert.Equal(t, "20", sectorConfig.SectorCode) // Industrials (manufacturing)
}

// TestIndustryClassifier_isTechnologyCompany_HighIntangibles tests the high intangible assets path
func TestIndustryClassifier_isTechnologyCompany_HighIntangibles(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		Revenue:                1000000000,
		ResearchAndDevelopment: 50000000, // 5% R&D (below 10% threshold)
		StockBasedCompensation: 30000000, // 3% stock comp (below 5% threshold)
		TotalAssets:            2000000000,
		IntangibleAssets:       400000000, // 20% intangibles (above 15% threshold)
	}

	sectorConfig, err := classifier.ClassifyIndustry("INTANGIBLE_CO", data)
	require.NoError(t, err)
	assert.Equal(t, "45", sectorConfig.SectorCode) // Technology (via intangible path)
}

// TestIndustryClassifier_isUtilitiesCompany_NotQualifying tests that high tangible with
// high inventory does NOT classify as utilities
func TestIndustryClassifier_isUtilitiesCompany_NotQualifying(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		TotalAssets:    10000000000,
		TangibleAssets: 8500000000, // 85% tangible (high)
		Inventory:      600000000,  // 6% inventory (above 5% threshold - not utility-like)
		Revenue:        5000000000,
	}

	// High tangible but too much inventory -> not utilities, falls to manufacturing
	sectorConfig, err := classifier.ClassifyIndustry("NOT_UTIL", data)
	require.NoError(t, err)
	// Should be manufacturing (high tangible ratio > 60%)
	assert.Equal(t, "20", sectorConfig.SectorCode)
}

// TestIndustryClassifier_isFinancialCompany_NotQualifying tests that a company with
// high tangible assets is NOT classified as financial
func TestIndustryClassifier_isFinancialCompany_NotQualifying(t *testing.T) {
	classifier := NewIndustryClassifier()

	data := &entities.FinancialData{
		TotalAssets:    10000000000,
		TangibleAssets: 5000000000, // 50% tangible (above 30% threshold)
		Inventory:      0,
		TotalDebt:      3000000000,
		Revenue:        1000000000,
	}

	// High tangible ratio -> not financial, falls to default
	sectorConfig, err := classifier.ClassifyIndustry("NOT_FIN", data)
	require.NoError(t, err)
	// With 50% tangible and no other triggers, defaults to industrials
	assert.Equal(t, "20", sectorConfig.SectorCode)
}

// TestIndustryClassifier_ApplyIndustrySpecificThresholds_AllRuleTypes tests applying
// thresholds for all supported rule types
func TestIndustryClassifier_ApplyIndustrySpecificThresholds_AllRuleTypes(t *testing.T) {
	classifier := NewIndustryClassifier()

	// Get technology sector config for its thresholds
	techConfig, exists := classifier.GetSectorConfig("45")
	require.True(t, exists)

	// Create rules for all supported types
	rules := []*entities.CleaningRule{
		{ID: "goodwill_exclusion", Category: entities.AssetQuality, Enabled: true},
		{ID: "intangible_writedown", Category: entities.AssetQuality, Enabled: true},
		{ID: "inventory_obsolescence", Category: entities.AssetQuality, Enabled: true},
		{ID: "deferred_tax_adjustment", Category: entities.AssetQuality, Enabled: true},
		{ID: "restructuring_charges", Category: entities.EarningsNormalization, Enabled: true},
		{ID: "stock_compensation", Category: entities.EarningsNormalization, Enabled: true},
		{ID: "litigation_settlements", Category: entities.EarningsNormalization, Enabled: true},
	}

	adjustedRules := classifier.ApplyIndustrySpecificThresholds(rules, techConfig)

	// Verify each rule type got its threshold applied
	for _, rule := range adjustedRules {
		assert.NotNil(t, rule.Threshold, "rule %s should have threshold", rule.ID)

		switch rule.ID {
		case "goodwill_exclusion":
			assert.NotNil(t, rule.Threshold.PercentageOfAssets)
			assert.Equal(t, techConfig.Thresholds.GoodwillThreshold, *rule.Threshold.PercentageOfAssets)
		case "intangible_writedown":
			assert.NotNil(t, rule.Threshold.PercentageOfAssets)
			assert.Equal(t, techConfig.Thresholds.IntangibleThreshold, *rule.Threshold.PercentageOfAssets)
		case "inventory_obsolescence":
			assert.NotNil(t, rule.Threshold.WritedownRate)
			assert.Equal(t, techConfig.Thresholds.InventoryObsolescenceRate, *rule.Threshold.WritedownRate)
		case "deferred_tax_adjustment":
			assert.NotNil(t, rule.Threshold.PercentageOfAssets)
			assert.Equal(t, techConfig.Thresholds.DeferredTaxThreshold, *rule.Threshold.PercentageOfAssets)
		case "restructuring_charges":
			assert.NotNil(t, rule.Threshold.PercentageOfRevenue)
			assert.Equal(t, techConfig.Thresholds.RestructuringThreshold, *rule.Threshold.PercentageOfRevenue)
		case "stock_compensation":
			assert.NotNil(t, rule.Threshold.PercentageOfRevenue)
			assert.Equal(t, techConfig.Thresholds.StockCompThreshold, *rule.Threshold.PercentageOfRevenue)
		case "litigation_settlements":
			assert.NotNil(t, rule.Threshold.PercentageOfRevenue)
			assert.Equal(t, techConfig.Thresholds.LitigationThreshold, *rule.Threshold.PercentageOfRevenue)
		}
	}
}

// TestIndustryClassifier_ApplyIndustrySpecificThresholds_NilThreshold tests that the function
// initializes a nil Threshold field on rules before applying sector-specific values.
func TestIndustryClassifier_ApplyIndustrySpecificThresholds_NilThreshold(t *testing.T) {
	classifier := NewIndustryClassifier()

	techConfig, exists := classifier.GetSectorConfig("45")
	require.True(t, exists)

	// Rule with nil Threshold — the function should initialize it
	rules := []*entities.CleaningRule{
		{ID: "goodwill_exclusion", Category: entities.AssetQuality, Enabled: true, Threshold: nil},
	}

	adjustedRules := classifier.ApplyIndustrySpecificThresholds(rules, techConfig)

	require.Len(t, adjustedRules, 1)
	assert.NotNil(t, adjustedRules[0].Threshold)
	assert.NotNil(t, adjustedRules[0].Threshold.PercentageOfAssets)
}
