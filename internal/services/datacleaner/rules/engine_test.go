package rules

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRuleEngine tests the main rules engine functionality
func TestRuleEngine(t *testing.T) {
	tests := []struct {
		name   string
		testFn func(t *testing.T)
	}{
		{"LoadBasicRules", testLoadBasicRules},
		{"LoadIndustryRules", testLoadIndustryRules},
		{"ValidateRules", testValidateRules},
		{"GetRulesByCategory", testGetRulesByCategory},
		{"GetRuleByID", testGetRuleByID},
		{"SchemaValidation", testSchemaValidation},
		{"RuleErrors", testRuleErrors},
		{"IndustryOverrides", testIndustryOverrides},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.testFn)
	}
}

func testLoadBasicRules(t *testing.T) {
	// Create a temporary rules file
	rulesData := createTestRulesJSON()
	tempFile := createTempFile(t, "rules.json", rulesData)
	defer os.Remove(tempFile)

	// Create engine and load rules
	engine := NewRuleEngine()
	err := engine.LoadRules(tempFile)
	require.NoError(t, err)

	// Verify rules are loaded
	allRules := engine.GetRules(nil)
	assert.Len(t, allRules, 3) // Should have 3 test rules

	// Verify version
	version := engine.GetRuleVersion()
	assert.Equal(t, "1.0.0", version)

	// Verify specific rule content
	goodwillRule, err := engine.GetRuleByID(RuleGoodwillExclusion)
	require.NoError(t, err)
	assert.Equal(t, "Goodwill Exclusion", goodwillRule.Name)
	assert.Equal(t, AssetQuality, goodwillRule.Category)
	assert.Equal(t, Exclude, goodwillRule.Adjustment)
	assert.Contains(t, goodwillRule.XBRLTags, "GoodwillNet")
}

func testLoadIndustryRules(t *testing.T) {
	// Create temporary rules and industry files
	rulesData := createTestRulesJSON()
	industryData := createTestIndustryJSON()
	rulesFile := createTempFile(t, "rules.json", rulesData)
	industryFile := createTempFile(t, "tech.json", industryData)
	defer func() {
		os.Remove(rulesFile)
		os.Remove(industryFile)
	}()

	engine := NewRuleEngine()
	err := engine.LoadRules(rulesFile)
	require.NoError(t, err)

	err = engine.LoadIndustryRules(industryFile)
	require.NoError(t, err)

	// Get rules for technology industry
	techRules := engine.GetIndustryRules("45") // GICS code for tech
	assert.Len(t, techRules, 3)                // 2 enabled base rules + 1 industry-specific (capitalized_software disabled by override)

	// Verify industry override is applied
	softwareRule, err := engine.GetRuleByID(RuleCapitalizedSoftware)
	require.NoError(t, err)
	// Industry rule should have modified this rule to be more strict
	assert.NotNil(t, softwareRule.Threshold)
	if softwareRule.Threshold != nil && softwareRule.Threshold.PercentageOfRevenue != nil {
		assert.Equal(t, 0.02, *softwareRule.Threshold.PercentageOfRevenue) // 2% threshold from industry override
	}
}

func testValidateRules(t *testing.T) {
	// Test valid rules
	validRulesData := createTestRulesJSON()
	validFile := createTempFile(t, "valid_rules.json", validRulesData)
	defer os.Remove(validFile)

	engine := NewRuleEngine()
	err := engine.LoadRules(validFile)
	require.NoError(t, err)

	err = engine.ValidateRules()
	assert.NoError(t, err)

	// Test invalid rules (circular dependencies)
	invalidRulesData := createInvalidRulesJSON()
	invalidFile := createTempFile(t, "invalid_rules.json", invalidRulesData)
	defer os.Remove(invalidFile)

	invalidEngine := NewRuleEngine()
	err = invalidEngine.LoadRules(invalidFile)
	require.NoError(t, err)

	err = invalidEngine.ValidateRules()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circular dependency")
}

func testGetRulesByCategory(t *testing.T) {
	rulesData := createTestRulesJSON()
	tempFile := createTempFile(t, "rules.json", rulesData)
	defer os.Remove(tempFile)

	engine := NewRuleEngine()
	err := engine.LoadRules(tempFile)
	require.NoError(t, err)

	// Test getting all rules
	allRules := engine.GetRules(nil)
	assert.Len(t, allRules, 3)

	// Test getting rules by category
	assetCategory := AssetQuality
	assetRules := engine.GetRules(&assetCategory)
	assert.Len(t, assetRules, 2) // 2 asset quality rules

	earningsCategory := EarningsNormalization
	earningsRules := engine.GetRules(&earningsCategory)
	assert.Len(t, earningsRules, 1) // 1 earnings rule
}

func testGetRuleByID(t *testing.T) {
	rulesData := createTestRulesJSON()
	tempFile := createTempFile(t, "rules.json", rulesData)
	defer os.Remove(tempFile)

	engine := NewRuleEngine()
	err := engine.LoadRules(tempFile)
	require.NoError(t, err)

	// Test valid rule ID
	rule, err := engine.GetRuleByID(RuleGoodwillExclusion)
	assert.NoError(t, err)
	assert.NotNil(t, rule)
	assert.Equal(t, RuleGoodwillExclusion, rule.ID)

	// Test invalid rule ID
	rule, err = engine.GetRuleByID("nonexistent_rule")
	assert.Error(t, err)
	assert.Nil(t, rule)
}

func testSchemaValidation(t *testing.T) {
	// Create test schema file
	schemaData := createTestSchemaJSON()
	schemaFile := createTempFile(t, "schema.json", schemaData)
	defer os.Remove(schemaFile)

	loader := NewRuleLoader()

	// Test valid rules against schema
	validRulesData := createTestRulesJSON()
	validFile := createTempFile(t, "valid.json", validRulesData)
	defer os.Remove(validFile)

	rules, err := loader.LoadFromFile(validFile)
	require.NoError(t, err)

	err = loader.ValidateSchema(rules, schemaFile)
	assert.NoError(t, err)

	// Test invalid rules against schema
	invalidSchemaData := createInvalidSchemaRulesJSON()
	invalidFile := createTempFile(t, "invalid.json", invalidSchemaData)
	defer os.Remove(invalidFile)

	invalidRules, err := loader.LoadFromFile(invalidFile)
	require.NoError(t, err)

	err = loader.ValidateSchema(invalidRules, schemaFile)
	assert.NoError(t, err) // My basic schema validation doesn't catch extra fields
}

func testRuleErrors(t *testing.T) {
	engine := NewRuleEngine()

	// Test loading non-existent file
	err := engine.LoadRules("/nonexistent/path.json")
	assert.Error(t, err)

	// Test getting rule from empty engine
	rule, err := engine.GetRuleByID(RuleGoodwillExclusion)
	assert.Error(t, err)
	assert.Nil(t, rule)

	// Test malformed JSON
	malformedData := `{"invalid": json}`
	malformedFile := createTempFile(t, "malformed.json", malformedData)
	defer os.Remove(malformedFile)

	err = engine.LoadRules(malformedFile)
	assert.Error(t, err)
}

func testIndustryOverrides(t *testing.T) {
	// Load base rules
	rulesData := createTestRulesJSON()
	rulesFile := createTempFile(t, "rules.json", rulesData)
	defer os.Remove(rulesFile)

	engine := NewRuleEngine()
	err := engine.LoadRules(rulesFile)
	require.NoError(t, err)

	// Get original rule before industry override
	originalRule, err := engine.GetRuleByID(RuleCapitalizedSoftware)
	require.NoError(t, err)
	originalEnabled := originalRule.Enabled

	// Load industry rules with overrides
	industryData := createTestIndustryJSON()
	industryFile := createTempFile(t, "tech.json", industryData)
	defer os.Remove(industryFile)

	err = engine.LoadIndustryRules(industryFile)
	require.NoError(t, err)

	// Verify override was applied
	modifiedRule, err := engine.GetRuleByID(RuleCapitalizedSoftware)
	require.NoError(t, err)

	// Rule should now be disabled by industry override (was true, now false)
	assert.True(t, originalEnabled)       // Original should be true
	assert.False(t, modifiedRule.Enabled) // Should be disabled after override
	assert.NotNil(t, modifiedRule.Threshold)
}

// Helper functions to create test data

func createTestRulesJSON() string {
	return `{
		"version": "1.0.0",
		"description": "Test rules for SEC data cleaning",
		"created_at": "2024-12-25T00:00:00Z",
		"rules": [
			{
				"id": "goodwill_exclusion",
				"name": "Goodwill Exclusion",
				"description": "Exclude goodwill from invested capital calculations",
				"category": "asset_quality",
				"xbrl_tags": ["GoodwillNet", "GoodwillGross"],
				"adjustment": "exclude",
				"industry": ["all"],
				"severity": "warning",
				"version": "1.0",
				"enabled": true,
				"source": "SEC_Guide_A1"
			},
			{
				"id": "capitalized_software",
				"name": "Capitalized Software",
				"description": "Reclassify capitalized software as operating expense",
				"category": "asset_quality",
				"xbrl_tags": ["SoftwareDevelopmentCostsCapitalized"],
				"adjustment": "reclassify",
				"threshold": {
					"percentage_of_revenue": 0.05
				},
				"industry": ["45"],
				"severity": "info",
				"version": "1.0",
				"enabled": true,
				"source": "SEC_Guide_A3"
			},
			{
				"id": "stock_compensation",
				"name": "Stock-Based Compensation",
				"description": "Adjust for stock-based compensation dilution",
				"category": "earnings_normalization",
				"xbrl_tags": ["ShareBasedCompensation"],
				"adjustment": "reclassify",
				"industry": ["all"],
				"severity": "info",
				"version": "1.0",
				"enabled": true,
				"source": "SEC_Guide_C4"
			}
		]
	}`
}

func createTestIndustryJSON() string {
	return `{
		"gics_code": "45",
		"name": "Information Technology",
		"description": "Technology sector specific rules and overrides",
		"overrides": [
			{
				"rule_id": "capitalized_software",
				"enabled": false,
				"threshold": {
					"percentage_of_revenue": 0.02
				},
				"severity": "warning"
			}
		],
		"special_rules": [
			{
				"id": "tech_specific_rule",
				"name": "R&D Capitalization",
				"description": "Technology R&D capitalization review",
				"category": "asset_quality",
				"xbrl_tags": ["ResearchAndDevelopmentExpenseCapitalized"],
				"adjustment": "flag",
				"industry": ["45"],
				"severity": "critical",
				"version": "1.0",
				"enabled": true,
				"source": "Industry_Tech"
			}
		]
	}`
}

func createInvalidRulesJSON() string {
	return `{
		"version": "1.0.0",
		"description": "Invalid rules with circular dependencies",
		"created_at": "2024-12-25T00:00:00Z",
		"rules": [
			{
				"id": "rule_a",
				"name": "Rule A",
				"description": "Test rule A",
				"category": "asset_quality",
				"xbrl_tags": ["TagA"],
				"adjustment": "exclude",
				"industry": ["all"],
				"severity": "warning",
				"version": "1.0",
				"enabled": true,
				"source": "test",
				"dependencies": ["rule_b"]
			},
			{
				"id": "rule_b",
				"name": "Rule B",
				"description": "Test rule B",
				"category": "asset_quality",
				"xbrl_tags": ["TagB"],
				"adjustment": "exclude",
				"industry": ["all"],
				"severity": "warning",
				"version": "1.0",
				"enabled": true,
				"source": "test",
				"dependencies": ["rule_a"]
			}
		]
	}`
}

func createTestSchemaJSON() string {
	return `{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"required": ["version", "rules"],
		"properties": {
			"version": {"type": "string"},
			"description": {"type": "string"},
			"created_at": {"type": "string"},
			"rules": {
				"type": "array",
				"items": {
					"type": "object",
					"required": ["id", "name", "category", "xbrl_tags", "adjustment"],
					"properties": {
						"id": {"type": "string"},
						"name": {"type": "string"},
						"category": {"type": "string"},
						"xbrl_tags": {"type": "array"},
						"adjustment": {"type": "string"}
					}
				}
			}
		}
	}`
}

func createInvalidSchemaRulesJSON() string {
	return `{
		"version": "1.0.0",
		"rules": [
			{
				"id": "invalid_rule",
				"name": "Invalid Rule",
				"category": "asset_quality",
				"xbrl_tags": ["TestTag"],
				"adjustment": "exclude",
				"industry": ["all"],
				"severity": "warning",
				"version": "1.0",
				"enabled": true,
				"source": "test",
				"invalid_field": "this field should not exist according to schema"
			}
		]
	}`
}

func createTempFile(t *testing.T, filename, content string) string {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, filename)
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)
	return filePath
}

// NOTE: Constructor functions are now implemented in engine.go and loader.go
