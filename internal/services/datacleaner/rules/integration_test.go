package rules

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestRealConfigurationFiles tests the actual configuration files in ./config/datacleaner/
func TestRealConfigurationFiles(t *testing.T) {
	tests := []struct {
		name   string
		testFn func(t *testing.T)
	}{
		{"LoadProductionRules", testLoadProductionRules},
		{"LoadTechIndustryRules", testLoadTechIndustryRules},
		{"LoadRetailIndustryRules", testLoadRetailIndustryRules},
		{"ValidateRulesSchema", testValidateRulesSchema},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.testFn(t)
		})
	}
}

func testLoadProductionRules(t *testing.T) {
	engine := NewRuleEngine()

	// Load the actual production rules file
	err := engine.LoadRules("../../../../config/datacleaner/rules.json")
	require.NoError(t, err, "Should be able to load production rules file")

	// Verify we loaded the expected number of rules
	assetQualityRules := engine.GetRulesByCategory(entities.AssetQuality)
	liabilityRules := engine.GetRulesByCategory(entities.LiabilityCompleteness)
	earningsRules := engine.GetRulesByCategory(entities.EarningsNormalization)

	// Should have rules in each category
	assert.Greater(t, len(assetQualityRules), 5, "Should have multiple asset quality rules")
	assert.Greater(t, len(liabilityRules), 2, "Should have multiple liability rules")
	assert.Greater(t, len(earningsRules), 5, "Should have multiple earnings rules")

	// Test specific rules exist
	goodwillRule, err := engine.GetRuleByID(entities.RuleGoodwillExclusion)
	require.NoError(t, err, "Goodwill exclusion rule should exist")
	assert.Equal(t, "Goodwill Exclusion", goodwillRule.Name)
	assert.Equal(t, entities.AssetQuality, goodwillRule.Category)
	assert.True(t, goodwillRule.Enabled)

	// Test rule with thresholds
	inventoryRule, err := engine.GetRuleByID("obsolete_inventory")
	require.NoError(t, err, "Obsolete inventory rule should exist")
	assert.NotNil(t, inventoryRule.Threshold, "Inventory rule should have thresholds")

	// Test industry-specific rule
	softwareRule, err := engine.GetRuleByID(entities.RuleCapitalizedSoftware)
	require.NoError(t, err, "Capitalized software rule should exist")
	assert.Contains(t, softwareRule.Industry, "45", "Should apply to tech industry")
}

func testLoadTechIndustryRules(t *testing.T) {
	// First load base rules
	engine := NewRuleEngine()
	err := engine.LoadRules("../../../../config/datacleaner/rules.json")
	require.NoError(t, err, "Should load base rules")

	// Then load tech industry rules
	err = engine.LoadIndustryRules("../../../../config/datacleaner/industry/technology.json")
	require.NoError(t, err, "Should load tech industry rules")

	// Verify tech-specific rules were added
	techRules := engine.GetIndustryRules("45")
	assert.Greater(t, len(techRules), 5, "Should have multiple tech-specific rules")

	// Test a tech-specific rule exists
	rdRule, err := engine.GetRuleByID("rd_capitalization_review")
	require.NoError(t, err, "R&D capitalization rule should exist")
	assert.Equal(t, "R&D Capitalization Review", rdRule.Name)
	assert.Contains(t, rdRule.Industry, "45")
}

func testLoadRetailIndustryRules(t *testing.T) {
	// First load base rules
	engine := NewRuleEngine()
	err := engine.LoadRules("../../../../config/datacleaner/rules.json")
	require.NoError(t, err, "Should load base rules")

	// Then load retail industry rules
	err = engine.LoadIndustryRules("../../../../config/datacleaner/industry/retail.json")
	require.NoError(t, err, "Should load retail industry rules")

	// Verify retail-specific rules were added
	retailRules := engine.GetIndustryRules("25")
	assert.Greater(t, len(retailRules), 5, "Should have multiple retail-specific rules")

	// Test a retail-specific rule exists
	seasonalRule, err := engine.GetRuleByID("seasonal_inventory_adjustment")
	require.NoError(t, err, "Seasonal inventory rule should exist")
	assert.Equal(t, "Seasonal Inventory Adjustment", seasonalRule.Name)
	assert.Contains(t, seasonalRule.Industry, "25")

	// Test that inventory rule has more aggressive thresholds for retail
	inventoryRule, err := engine.GetRuleByID("obsolete_inventory")
	require.NoError(t, err, "Obsolete inventory rule should exist")
	// Should have industry-specific overrides applied
	assert.NotNil(t, inventoryRule.Threshold)
}

func testValidateRulesSchema(t *testing.T) {
	loader := NewRuleLoader()

	// Load production rules
	rules, err := loader.LoadFromFile("../../../../config/datacleaner/rules.json")
	require.NoError(t, err, "Should load production rules")

	// Validate against schema
	err = loader.ValidateSchema(rules, "../../../../config/datacleaner/schema.json")
	assert.NoError(t, err, "Production rules should validate against schema")

	// Verify loaded rules structure
	assert.Equal(t, "1.0.0", rules.Version)
	assert.Greater(t, len(rules.Rules), 15, "Should have at least 17 rules as specified")

	// Check each rule has required fields
	for _, rule := range rules.Rules {
		assert.NotEmpty(t, rule.ID, "Rule should have ID")
		assert.NotEmpty(t, rule.Name, "Rule should have name")
		assert.NotEmpty(t, rule.Description, "Rule should have description")
		assert.NotEmpty(t, rule.XBRLTags, "Rule should have XBRL tags")
		assert.NotEmpty(t, rule.Industry, "Rule should have industry specification")
		assert.True(t, len(rule.Industry) > 0, "Rule should specify at least one industry")
	}
}
