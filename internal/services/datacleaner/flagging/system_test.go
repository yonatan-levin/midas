package flagging

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestFlaggingSystem_CalculateQualityScore(t *testing.T) {
	tests := []struct {
		name           string
		financialData  *entities.FinancialData
		flags          []entities.Flag
		expectedScore  float64
		expectedGrade  string
		expectedIssues []string
	}{
		{
			name:          "high quality company - minimal issues",
			financialData: createHighQualityFinancialData(),
			flags: []entities.Flag{
				createMinorFlag("A1", "goodwill_concentration"),
			},
			expectedScore:  94.5,
			expectedGrade:  "A+",
			expectedIssues: []string{"Minor goodwill concentration"},
		},
		{
			name:          "poor quality company - multiple severe issues",
			financialData: createPoorQualityFinancialData(),
			flags: []entities.Flag{
				createCriticalFlag("A1", "goodwill_concentration"),
				createHighFlag("A5", "dead_inventory"),
				createMediumFlag("B1", "lease_liability"),
			},
			expectedScore: 0.0,
			expectedGrade: "F+",
			expectedIssues: []string{
				"Excessive goodwill concentration",
				"Significant dead inventory detected",
				"Off-balance sheet lease obligations",
			},
		},
		{
			name:          "medium quality company - balanced profile",
			financialData: createMediumQualityFinancialData(),
			flags: []entities.Flag{
				createMediumFlag("A2", "intangibles"),
				createLowFlag("C1", "restructuring"),
			},
			expectedScore: 76.0,
			expectedGrade: "B+",
			expectedIssues: []string{
				"Significant intangible assets",
				"Recurring restructuring charges",
			},
		},
		{
			name:           "excellent company - no major issues",
			financialData:  createExcellentFinancialData(),
			flags:          []entities.Flag{},
			expectedScore:  95.0,
			expectedGrade:  "A+",
			expectedIssues: []string{},
		},
	}

	system := NewFlaggingSystem()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := system.CalculateQualityScore(tt.financialData, tt.flags)

			assert.InDelta(t, tt.expectedScore, result.QualityScore, 5.0, "Quality score should be within range")
			assert.Equal(t, tt.expectedGrade, result.QualityGrade, "Quality grade should match")

			if len(tt.expectedIssues) > 0 {
				assert.NotEmpty(t, result.QualityIssues, "Should have quality issues")
				for _, expectedIssue := range tt.expectedIssues {
					assert.Contains(t, result.QualityIssues, expectedIssue, "Should contain expected issue")
				}
			} else {
				assert.Empty(t, result.QualityIssues, "Should have no quality issues")
			}
		})
	}
}

func TestFlaggingSystem_AnalyzeRisks(t *testing.T) {
	tests := []struct {
		name          string
		financialData *entities.FinancialData
		context       *entities.CleaningContext
		expectedFlags int
		expectedTypes []string
	}{
		{
			name:          "goodwill concentration risk",
			financialData: createGoodwillRiskData(),
			context:       createDefaultContext(),
			expectedFlags: 2,
			expectedTypes: []string{"goodwill_concentration", "intangible_risk"},
		},
		{
			name:          "inventory obsolescence risk",
			financialData: createInventoryRiskData(),
			context:       createRetailContext(),
			expectedFlags: 1,
			expectedTypes: []string{"inventory_obsolescence"},
		},
		{
			name:          "leverage concern",
			financialData: createHighDebtData(),
			context:       createDefaultContext(),
			expectedFlags: 1,
			expectedTypes: []string{"leverage_concern"},
		},
		{
			name:          "clean company - no flags",
			financialData: createCleanFinancialData(),
			context:       createDefaultContext(),
			expectedFlags: 0,
			expectedTypes: []string{},
		},
	}

	system := NewFlaggingSystem()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := system.AnalyzeRisks(tt.financialData, tt.context)

			assert.Len(t, flags, tt.expectedFlags, "Should generate expected number of flags")

			if tt.expectedFlags > 0 {
				flagTypes := make([]string, len(flags))
				for i, flag := range flags {
					flagTypes[i] = flag.Type
				}

				for _, expectedType := range tt.expectedTypes {
					assert.Contains(t, flagTypes, expectedType, "Should contain expected flag type")
				}
			}
		})
	}
}

func TestFlaggingSystem_GenerateRecommendations(t *testing.T) {
	tests := []struct {
		name                    string
		flags                   []entities.Flag
		financialData           *entities.FinancialData
		expectedRecommendations int
		expectedKeywords        []string
	}{
		{
			name: "goodwill impairment recommendation",
			flags: []entities.Flag{
				createCriticalFlag("A1", "goodwill_concentration"),
			},
			financialData:           createGoodwillRiskData(),
			expectedRecommendations: 1,
			expectedKeywords:        []string{"impairment", "writedown", "goodwill"},
		},
		{
			name: "inventory liquidation recommendation",
			flags: []entities.Flag{
				createHighFlag("A5", "dead_inventory"),
			},
			financialData:           createInventoryRiskData(),
			expectedRecommendations: 1,
			expectedKeywords:        []string{"liquidation", "writedown", "inventory"},
		},
		{
			name: "multiple recommendations",
			flags: []entities.Flag{
				createMediumFlag("A2", "intangibles"),
				createLowFlag("C1", "restructuring"),
			},
			financialData:           createMediumQualityFinancialData(),
			expectedRecommendations: 2,
			expectedKeywords:        []string{"amortization", "normalize"},
		},
	}

	system := NewFlaggingSystem()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations := system.GenerateRecommendations(tt.flags, tt.financialData)

			assert.Len(t, recommendations, tt.expectedRecommendations, "Should generate expected number of recommendations")

			if tt.expectedRecommendations > 0 {
				allText := ""
				for _, rec := range recommendations {
					allText += rec.Description + " " + rec.Action
				}

				for _, keyword := range tt.expectedKeywords {
					assert.Contains(t, strings.ToLower(allText), strings.ToLower(keyword), "Recommendations should contain expected keyword")
				}
			}
		})
	}
}

func TestIndustryAnalyzer_GetIndustryThresholds(t *testing.T) {
	tests := []struct {
		name         string
		industryCode string
		expected     map[string]float64
	}{
		{
			name:         "technology sector",
			industryCode: "45", // Technology GICS
			expected: map[string]float64{
				"goodwill_threshold":   0.00,  // Goodwill has no real value
				"intangible_threshold": 0.25, // 25% for tech IP
				"inventory_threshold":  0.05, // 5% for tech (low inventory)
			},
		},
		{
			name:         "retail sector",
			industryCode: "25", // Consumer Discretionary
			expected: map[string]float64{
				"goodwill_threshold":   0.00, // Goodwill has no real value
				"intangible_threshold": 0.10, // 10% for retail
				"inventory_threshold":  0.30, // 30% for retail (updated from 40%)
			},
		},
		{
			name:         "default/unknown sector",
			industryCode: "99", // Unknown
			expected: map[string]float64{
				"goodwill_threshold":   0.00, // Goodwill has no real value
				"intangible_threshold": 0.15, // 15% default
				"inventory_threshold":  0.25, // 25% default
			},
		},
	}

	analyzer := NewIndustryAnalyzer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thresholds := analyzer.GetIndustryThresholds(tt.industryCode)

			for key, expected := range tt.expected {
				actual, exists := thresholds[key]
				assert.True(t, exists, "Threshold %s should exist", key)
				assert.Equal(t, expected, actual, "Threshold %s should match expected value", key)
			}
		})
	}
}

func TestRiskAnalyzer_AssessGoodwillRisk(t *testing.T) {
	tests := []struct {
		name              string
		goodwill          float64
		totalAssets       float64
		industryThreshold float64
		expectedSeverity  entities.FlagSeverity
		expectedFlag      bool
	}{
		{
			name:              "excessive goodwill - critical",
			goodwill:          500.0, // 50% of assets
			totalAssets:       1000.0,
			industryThreshold: 0.20, // 20% threshold
			expectedSeverity:  entities.FlagSeverityCritical,
			expectedFlag:      true,
		},
		{
			name:              "moderate goodwill - medium",
			goodwill:          250.0, // 25% of assets
			totalAssets:       1000.0,
			industryThreshold: 0.20, // 20% threshold
			expectedSeverity:  entities.FlagSeverityMedium,
			expectedFlag:      true,
		},
		{
			name:              "acceptable goodwill - no flag",
			goodwill:          150.0, // 15% of assets
			totalAssets:       1000.0,
			industryThreshold: 0.20, // 20% threshold
			expectedSeverity:  "",
			expectedFlag:      false,
		},
		{
			name:              "no goodwill - no flag",
			goodwill:          0.0,
			totalAssets:       1000.0,
			industryThreshold: 0.20,
			expectedSeverity:  "",
			expectedFlag:      false,
		},
	}

	analyzer := NewRiskAnalyzer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := analyzer.AssessGoodwillRisk(tt.goodwill, tt.totalAssets, tt.industryThreshold)

			if tt.expectedFlag {
				require.NotNil(t, flag, "Should generate a flag")
				assert.Equal(t, tt.expectedSeverity, flag.Severity, "Severity should match")
				assert.Equal(t, "goodwill_concentration", flag.Type, "Flag type should be goodwill_concentration")
				assert.Greater(t, flag.Percentage, 0.0, "Should have percentage value")
			} else {
				assert.Nil(t, flag, "Should not generate a flag")
			}
		})
	}
}

// Helper functions for creating test data

func createHighQualityFinancialData() *entities.FinancialData {
	return &entities.FinancialData{
		// Low goodwill, strong balance sheet
		Goodwill:         100.0, // 10% of assets
		TotalAssets:      1000.0,
		OtherIntangibles: 50.0,   // 5% of assets - reasonable intangibles
		Inventory:        50.0,   // 5% of assets - low inventory
		TotalDebt:        200.0,  // 20% debt ratio
		Revenue:          1200.0, // Strong revenue
		// Other fields as needed...
	}
}

func createPoorQualityFinancialData() *entities.FinancialData {
	return &entities.FinancialData{
		// High goodwill, weak balance sheet
		Goodwill:         600.0, // 60% of assets - excessive
		TotalAssets:      1000.0,
		OtherIntangibles: 100.0, // 10% of assets - high intangibles
		Inventory:        300.0, // 30% of assets - high inventory
		TotalDebt:        700.0, // 70% debt ratio - high leverage
		Revenue:          500.0, // Weak revenue relative to assets
		// Other fields as needed...
	}
}

func createMediumQualityFinancialData() *entities.FinancialData {
	return &entities.FinancialData{
		// Moderate metrics
		Goodwill:         250.0, // 25% of assets
		TotalAssets:      1000.0,
		OtherIntangibles: 75.0,  // 7.5% of assets - moderate intangibles
		Inventory:        150.0, // 15% of assets
		TotalDebt:        400.0, // 40% debt ratio
		Revenue:          800.0, // Reasonable revenue
		// Other fields as needed...
	}
}

func createExcellentFinancialData() *entities.FinancialData {
	return &entities.FinancialData{
		// Minimal goodwill, strong metrics
		Goodwill:         50.0, // 5% of assets
		TotalAssets:      1000.0,
		OtherIntangibles: 30.0,   // 3% of assets - minimal intangibles
		Inventory:        30.0,   // 3% of assets - very low
		TotalDebt:        100.0,  // 10% debt ratio - low leverage
		Revenue:          1500.0, // Excellent revenue
		// Other fields as needed...
	}
}

func createGoodwillRiskData() *entities.FinancialData {
	return &entities.FinancialData{
		Goodwill:         400.0, // 40% of assets - high risk
		TotalAssets:      1000.0,
		OtherIntangibles: 200.0, // 20% of assets - also high intangibles for second flag
		Revenue:          800.0, // Include revenue to avoid structural penalty
		// Other fields...
	}
}

func createInventoryRiskData() *entities.FinancialData {
	return &entities.FinancialData{
		Inventory:         350.0, // 35% of assets - retail risk
		TotalAssets:       1000.0,
		Revenue:           1000.0, // Include revenue
		InventoryTurnover: 2.0,    // Low turnover indicating obsolescence risk
		// TODO: Add inventory turnover data for better analysis
		// Other fields...
	}
}

func createHighDebtData() *entities.FinancialData {
	return &entities.FinancialData{
		TotalDebt:   800.0, // 80% of assets - high leverage
		TotalAssets: 1000.0,
		Revenue:     900.0, // Include revenue
		// Other fields...
	}
}

func createCleanFinancialData() *entities.FinancialData {
	return &entities.FinancialData{
		// Clean metrics across the board
		Goodwill:         0.0, // 5% minimal goodwill
		TotalAssets:      1000.0,
		OtherIntangibles: 40.0,   // 4% minimal intangibles
		Inventory:        80.0,   // 8% reasonable inventory
		TotalDebt:        150.0,  // 15% conservative debt
		Revenue:          1100.0, // Strong revenue
		// Other fields...
	}
}

func createDefaultContext() *entities.CleaningContext {
	return &entities.CleaningContext{
		IndustryCode:     "20", // Default industry
		CompanySize:      entities.LargeCap,
		DataVintage:      time.Now(),
		EnableIndustry:   true,
		EnableCaching:    true,
		QualityThreshold: 70.0,
	}
}

func createRetailContext() *entities.CleaningContext {
	return &entities.CleaningContext{
		IndustryCode:     "25", // Consumer Discretionary (Retail)
		CompanySize:      entities.MidCap,
		DataVintage:      time.Now(),
		EnableIndustry:   true,
		EnableCaching:    true,
		QualityThreshold: 70.0,
	}
}

func createMinorFlag(ruleID, flagType string) entities.Flag {
	return entities.Flag{
		ID:          "flag-001",
		RuleID:      ruleID,
		Type:        flagType,
		Severity:    entities.FlagSeverityLow,
		Amount:      100.0,
		Percentage:  10.0,
		Description: "Minor issue detected",
		Timestamp:   time.Now(),
	}
}

func createMediumFlag(ruleID, flagType string) entities.Flag {
	return entities.Flag{
		ID:          "flag-002",
		RuleID:      ruleID,
		Type:        flagType,
		Severity:    entities.FlagSeverityMedium,
		Amount:      250.0,
		Percentage:  25.0,
		Description: "Medium issue detected",
		Timestamp:   time.Now(),
	}
}

func createHighFlag(ruleID, flagType string) entities.Flag {
	return entities.Flag{
		ID:          "flag-003",
		RuleID:      ruleID,
		Type:        flagType,
		Severity:    entities.FlagSeverityHigh,
		Amount:      400.0,
		Percentage:  40.0,
		Description: "High severity issue detected",
		Timestamp:   time.Now(),
	}
}

func createLowFlag(ruleID, flagType string) entities.Flag {
	return entities.Flag{
		ID:          "flag-001",
		RuleID:      ruleID,
		Type:        flagType,
		Severity:    entities.FlagSeverityLow,
		Amount:      50.0,
		Percentage:  5.0,
		Description: "Low severity issue detected",
		Timestamp:   time.Now(),
	}
}

func createCriticalFlag(ruleID, flagType string) entities.Flag {
	return entities.Flag{
		ID:          "flag-004",
		RuleID:      ruleID,
		Type:        flagType,
		Severity:    entities.FlagSeverityCritical,
		Amount:      600.0,
		Percentage:  60.0,
		Description: "Critical issue detected",
		Timestamp:   time.Now(),
	}
}
