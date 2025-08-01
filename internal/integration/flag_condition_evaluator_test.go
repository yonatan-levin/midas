// Package integration contains integration tests for flag conditions
package integration

import (
	"context"
	"encoding/json"

	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlagConditionEvaluatorIntegration tests the flag condition evaluation system
func TestFlagConditionEvaluatorIntegration(t *testing.T) {
	// Setup test configuration
	cfg := setupFlagTestConfig(t)
	logger := log.New(os.Stdout, "TEST: ", log.LstdFlags)

	// Create the evaluator service
	evaluator, err := datacleaner.NewFlagConditionEvaluatorService(cfg, logger)
	require.NoError(t, err, "Failed to create evaluator service")

	ctx := context.Background()

	t.Run("EvaluateHighLeverageFlag", func(t *testing.T) {
		data := map[string]interface{}{
			"debt_to_equity_ratio":    2.5,
			"interest_coverage_ratio": 2.0,
			"company_name":            "High Leverage Corp",
		}

		results, err := evaluator.EvaluateFlags(ctx, data)

		require.NoError(t, err)

		// Find high leverage flag result
		var highLeverageResult *ports.FlagResult
		for _, result := range results {
			if result.FlagName == "high_leverage_flag" {
				highLeverageResult = &result
				break
			}
		}

		require.NotNil(t, highLeverageResult)
		assert.True(t, highLeverageResult.Triggered)
		assert.Len(t, highLeverageResult.Actions, 2)

		// Execute actions
		err = evaluator.ExecuteActions(ctx, results, data)
		require.NoError(t, err)

		// Check that action was executed
		riskFlags, ok := data["risk_flags"].(map[string]interface{})
		require.True(t, ok)
		assert.True(t, riskFlags["high_leverage"].(bool))
	})

	t.Run("EvaluateRevenueVolatilityFlag", func(t *testing.T) {
		testCases := []struct {
			name      string
			data      map[string]interface{}
			triggered bool
		}{
			{
				name: "High CV triggers flag",
				data: map[string]interface{}{
					"revenue_coefficient_variation": 0.35,
					"revenue_yoy_change":            0.1,
				},
				triggered: true,
			},
			{
				name: "Large decline triggers flag",
				data: map[string]interface{}{
					"revenue_coefficient_variation": 0.2,
					"revenue_yoy_change":            -0.25,
				},
				triggered: true,
			},
			{
				name: "Stable revenue does not trigger",
				data: map[string]interface{}{
					"revenue_coefficient_variation": 0.1,
					"revenue_yoy_change":            0.05,
				},
				triggered: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result, err := evaluator.EvaluateFlag(ctx, "revenue_volatility_flag", tc.data)

				require.NoError(t, err)
				assert.Equal(t, tc.triggered, result.Triggered)
			})
		}
	})

	t.Run("EvaluateMaterialAdjustmentFlag", func(t *testing.T) {
		data := map[string]interface{}{
			"total_adjustments":           1000000,
			"adjustment_to_revenue_ratio": 0.08, // Above 5% threshold
			"total_revenue":               12500000,
		}

		results, err := evaluator.EvaluateFlags(ctx, data)

		require.NoError(t, err)

		// Find material adjustment flag
		var adjustmentResult *ports.FlagResult
		for _, result := range results {
			if result.FlagName == "material_adjustment_flag" {
				adjustmentResult = &result
				break
			}
		}

		require.NotNil(t, adjustmentResult)
		assert.True(t, adjustmentResult.Triggered)

		// Check that it has alert action
		hasAlertAction := false
		for _, action := range adjustmentResult.Actions {
			if flagAction, ok := action.(config.FlagAction); ok && flagAction.Type == "alert" {
				hasAlertAction = true
				break
			}
		}
		assert.True(t, hasAlertAction)
	})

	t.Run("EvaluateIndustrySpecificFlag", func(t *testing.T) {
		data := map[string]interface{}{
			"industry_code":    "TECH",
			"rd_expense_ratio": 0.20,
			"total_rd_expense": 2000000,
			"total_revenue":    10000000,
		}

		result, err := evaluator.EvaluateFlag(ctx, "industry_specific_tech_flag", data)

		require.NoError(t, err)
		assert.True(t, result.Triggered)

		// Execute actions
		err = evaluator.ExecuteActions(ctx, []ports.FlagResult{*result}, data)
		require.NoError(t, err)

		// Check that R&D capitalization was set
		valAdjustments, ok := data["valuation_adjustments"].(map[string]interface{})
		require.True(t, ok)
		assert.True(t, valAdjustments["capitalize_rd"].(bool))
		assert.Equal(t, 5, valAdjustments["rd_amortization_years"])
	})

	t.Run("EvaluateComplexConditionGroups", func(t *testing.T) {
		// Test cash flow quality flag with nested groups
		data := map[string]interface{}{
			"operating_cash_flow":           -1000000,
			"net_income":                    500000,
			"cash_flow_to_net_income_ratio": 0.8,
			"receivables_growth_rate":       0.1,
		}

		result, err := evaluator.EvaluateFlag(ctx, "cash_flow_quality_flag", data)

		require.NoError(t, err)
		assert.True(t, result.Triggered, "First group condition should trigger")

		// Test second group condition
		data2 := map[string]interface{}{
			"operating_cash_flow":           1000000,
			"net_income":                    2500000,
			"cash_flow_to_net_income_ratio": 0.4,  // Less than 0.5
			"receivables_growth_rate":       0.35, // Greater than 0.3
		}

		result2, err := evaluator.EvaluateFlag(ctx, "cash_flow_quality_flag", data2)

		require.NoError(t, err)
		assert.True(t, result2.Triggered, "Second group condition should trigger")
	})

	t.Run("EvaluateExistsCondition", func(t *testing.T) {
		// Test with missing required fields
		data := map[string]interface{}{
			"total_assets": 1000000,
			// Missing total_revenue and net_income
		}

		result, err := evaluator.EvaluateFlag(ctx, "data_completeness_flag", data)

		require.NoError(t, err)
		assert.True(t, result.Triggered)
		assert.Contains(t, result.Details, "exists: false")
	})

	t.Run("EvaluateRegexCondition", func(t *testing.T) {
		testCases := []struct {
			name      string
			notes     string
			triggered bool
		}{
			{
				name:      "Going concern triggers",
				notes:     "There is substantial doubt about the entity's ability to continue as a going concern.",
				triggered: true,
			},
			{
				name:      "Material uncertainty triggers",
				notes:     "Material uncertainty exists regarding future operations.",
				triggered: true,
			},
			{
				name:      "Clean notes don't trigger",
				notes:     "The financial statements present fairly the financial position.",
				triggered: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				data := map[string]interface{}{
					"audit_opinion":         "unqualified",
					"has_material_weakness": false,
					"auditor_notes":         tc.notes,
				}

				result, err := evaluator.EvaluateFlag(ctx, "audit_concern_flag", data)

				require.NoError(t, err)
				assert.Equal(t, tc.triggered, result.Triggered)
			})
		}
	})

	t.Run("EvaluateNullBehavior", func(t *testing.T) {
		// Test with null values
		data := map[string]interface{}{
			// debt_to_equity_ratio is missing
			"interest_coverage_ratio": 1.5,
		}

		result, err := evaluator.EvaluateFlag(ctx, "high_leverage_flag", data)

		require.NoError(t, err)
		assert.False(t, result.Triggered, "Should not trigger with null_behavior='false'")
		assert.Contains(t, result.Details, "is null")
	})

	t.Run("EvaluatePriorityOrder", func(t *testing.T) {
		// Create data that triggers multiple flags
		data := map[string]interface{}{
			"stockholders_equity":     -500000, // Negative equity (priority 95)
			"debt_to_equity_ratio":    -3.0,    // Also high leverage (priority 100)
			"interest_coverage_ratio": 1.0,
			"audit_opinion":           "qualified", // Audit concern (priority 85)
		}

		results, err := evaluator.EvaluateFlags(ctx, data)

		require.NoError(t, err)
		require.GreaterOrEqual(t, len(results), 3)

		// Check that results are in priority order
		for i := 1; i < len(results); i++ {
			prevFlag, _ := cfg.GetFlagByName(results[i-1].FlagName)
			currFlag, _ := cfg.GetFlagByName(results[i].FlagName)
			assert.GreaterOrEqual(t, prevFlag.Priority, currFlag.Priority,
				"Flags should be evaluated in priority order")
		}
	})

	t.Run("GlobalVariableReference", func(t *testing.T) {
		// Test using global variable reference ($min_materiality_threshold)
		data := map[string]interface{}{
			"total_adjustments":           1000000,
			"adjustment_to_revenue_ratio": 0.06, // Above global threshold of 0.05
		}

		result, err := evaluator.EvaluateFlag(ctx, "material_adjustment_flag", data)

		require.NoError(t, err)
		assert.True(t, result.Triggered, "Should trigger when ratio exceeds global threshold")
	})

	t.Run("DisabledFlag", func(t *testing.T) {
		// Create a config with a disabled flag
		cfg := &config.FlagConditionsConfig{
			Version: "1.0.0",
			Flags: []config.FlagConfig{
				{
					Name:        "disabled_flag",
					Description: "This flag is disabled",
					Enabled:     false,
					Priority:    100,
					Conditions: config.ConditionGroup{
						Operator: "AND",
						Conditions: []config.Condition{
							{
								Type:     "boolean",
								Field:    "always_true",
								Operator: "eq",
								Value:    true,
							},
						},
					},
				},
			},
		}

		evaluator, err := datacleaner.NewFlagConditionEvaluatorService(cfg, logger)
		require.NoError(t, err)

		data := map[string]interface{}{
			"always_true": true,
		}

		result, err := evaluator.EvaluateFlag(ctx, "disabled_flag", data)

		require.NoError(t, err)
		assert.False(t, result.Triggered)
		assert.Equal(t, "Flag is disabled", result.Details)
	})
}

// TestFlagConfigValidation tests configuration validation
func TestFlagConfigValidation(t *testing.T) {
	t.Run("ValidConfig", func(t *testing.T) {
		cfg := &config.FlagConditionsConfig{
			Version: "1.0.0",
			Flags: []config.FlagConfig{
				{
					Name:        "test_flag",
					Description: "Test flag",
					Enabled:     true,
					Priority:    100,
					Conditions: config.ConditionGroup{
						Operator: "AND",
						Conditions: []config.Condition{
							{
								Type:     "numeric",
								Field:    "value",
								Operator: "gt",
								Value:    0,
							},
						},
					},
				},
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("MissingVersion", func(t *testing.T) {
		cfg := &config.FlagConditionsConfig{
			Flags: []config.FlagConfig{},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "configuration version is required")
	})

	t.Run("DuplicateFlagName", func(t *testing.T) {
		cfg := &config.FlagConditionsConfig{
			Version: "1.0.0",
			Flags: []config.FlagConfig{
				{
					Name: "duplicate",
					Conditions: config.ConditionGroup{
						Operator:   "AND",
						Conditions: []config.Condition{{Type: "exists", Field: "test", Operator: "eq", Value: true}},
					},
				},
				{
					Name: "duplicate",
					Conditions: config.ConditionGroup{
						Operator:   "AND",
						Conditions: []config.Condition{{Type: "exists", Field: "test", Operator: "eq", Value: true}},
					},
				},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate flag name")
	})

	t.Run("InvalidOperator", func(t *testing.T) {
		cfg := &config.FlagConditionsConfig{
			Version: "1.0.0",
			Flags: []config.FlagConfig{
				{
					Name: "test",
					Conditions: config.ConditionGroup{
						Operator:   "INVALID",
						Conditions: []config.Condition{{Type: "exists", Field: "test", Operator: "eq", Value: true}},
					},
				},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid operator")
	})

	t.Run("EmptyConditionGroup", func(t *testing.T) {
		cfg := &config.FlagConditionsConfig{
			Version: "1.0.0",
			Flags: []config.FlagConfig{
				{
					Name: "test",
					Conditions: config.ConditionGroup{
						Operator: "AND",
						// No conditions or groups
					},
				},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must have at least one condition or nested group")
	})
}

// TestFlagConfigLoading tests configuration file loading
func TestFlagConfigLoading(t *testing.T) {
	t.Run("LoadFromFile", func(t *testing.T) {
		// Create temporary config file
		configData := createFlagTestConfigJSON()
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "flag_conditions.json")

		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Load configuration
		cfg, err := config.LoadFlagConditionsConfig(configPath)

		require.NoError(t, err)
		assert.Equal(t, "1.0.0", cfg.Version)
		assert.NotEmpty(t, cfg.Flags)
		assert.NotEmpty(t, cfg.GlobalVariables)
	})

	t.Run("LoadFromEnvironment", func(t *testing.T) {
		// Create config file
		configData := createFlagTestConfigJSON()
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "env_flag_conditions.json")
		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Set environment variable
		os.Setenv("FLAG_CONDITIONS_CONFIG_PATH", configPath)
		defer os.Unsetenv("FLAG_CONDITIONS_CONFIG_PATH")

		// Load without specifying path
		cfg, err := config.LoadFlagConditionsConfig("")

		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.Equal(t, "1.0.0", cfg.Version)
	})
}

// Helper Functions

// setupFlagTestConfig creates a test configuration
func setupFlagTestConfig(t *testing.T) *config.FlagConditionsConfig {
	configJSON := createFlagTestConfigJSON()

	var cfg config.FlagConditionsConfig
	err := json.Unmarshal([]byte(configJSON), &cfg)
	require.NoError(t, err)

	return &cfg
}

// createFlagTestConfigJSON returns a test configuration as JSON string
func createFlagTestConfigJSON() string {
	return `{
		"version": "1.0.0",
		"global_variables": {
			"min_materiality_threshold": 0.05,
			"high_risk_threshold": 0.10
		},
		"flags": [
			{
				"name": "high_leverage_flag",
				"description": "Flag for companies with high leverage ratios",
				"enabled": true,
				"priority": 100,
				"conditions": {
					"operator": "AND",
					"conditions": [
						{
							"type": "numeric",
							"field": "debt_to_equity_ratio",
							"operator": "gt",
							"value": 2.0,
							"null_behavior": "false"
						},
						{
							"type": "numeric",
							"field": "interest_coverage_ratio",
							"operator": "lt",
							"value": 2.5,
							"null_behavior": "false"
						}
					]
				},
				"actions": [
					{
						"type": "set_field",
						"parameters": {
							"field": "risk_flags.high_leverage",
							"value": true
						}
					},
					{
						"type": "log",
						"parameters": {
							"level": "warning",
							"message": "High leverage detected for company"
						}
					}
				]
			},
			{
				"name": "revenue_volatility_flag",
				"description": "Flag for companies with volatile revenue patterns",
				"enabled": true,
				"priority": 90,
				"conditions": {
					"operator": "OR",
					"conditions": [
						{
							"type": "numeric",
							"field": "revenue_coefficient_variation",
							"operator": "gt",
							"value": 0.3,
							"null_behavior": "false"
						},
						{
							"type": "numeric",
							"field": "revenue_yoy_change",
							"operator": "lt",
							"value": -0.2,
							"null_behavior": "false"
						}
					]
				},
				"actions": [
					{
						"type": "set_field",
						"parameters": {
							"field": "risk_flags.revenue_volatility",
							"value": true
						}
					}
				]
			},
			{
				"name": "material_adjustment_flag",
				"description": "Flag for material adjustments that exceed threshold",
				"enabled": true,
				"priority": 85,
				"conditions": {
					"operator": "AND",
					"conditions": [
						{
							"type": "numeric",
							"field": "total_adjustments",
							"operator": "ne",
							"value": 0
						},
						{
							"type": "numeric",
							"field": "adjustment_to_revenue_ratio",
							"operator": "gt",
							"value": "$min_materiality_threshold",
							"null_behavior": "false"
						}
					]
				},
				"actions": [
					{
						"type": "set_field",
						"parameters": {
							"field": "requires_review.material_adjustments",
							"value": true
						}
					},
					{
						"type": "alert",
						"parameters": {
							"type": "email",
							"recipient": "review-team@company.com",
							"subject": "Material adjustments detected"
						}
					}
				]
			},
			{
				"name": "industry_specific_tech_flag",
				"description": "Special handling for technology companies",
				"enabled": true,
				"priority": 80,
				"conditions": {
					"operator": "AND",
					"conditions": [
						{
							"type": "string",
							"field": "industry_code",
							"operator": "eq",
							"value": "TECH",
							"case_sensitive": false
						},
						{
							"type": "numeric",
							"field": "rd_expense_ratio",
							"operator": "gt",
							"value": 0.15,
							"null_behavior": "false"
						}
					]
				},
				"actions": [
					{
						"type": "set_field",
						"parameters": {
							"field": "valuation_adjustments.capitalize_rd",
							"value": true
						}
					},
					{
						"type": "set_field",
						"parameters": {
							"field": "valuation_adjustments.rd_amortization_years",
							"value": 5
						}
					}
				]
			},
			{
				"name": "cash_flow_quality_flag",
				"description": "Flag for poor cash flow quality",
				"enabled": true,
				"priority": 75,
				"conditions": {
					"operator": "OR",
					"groups": [
						{
							"operator": "AND",
							"conditions": [
								{
									"type": "numeric",
									"field": "operating_cash_flow",
									"operator": "lt",
									"value": 0,
									"null_behavior": "false"
								},
								{
									"type": "numeric",
									"field": "net_income",
									"operator": "gt",
									"value": 0,
									"null_behavior": "false"
								}
							]
						},
						{
							"operator": "AND",
							"conditions": [
								{
									"type": "numeric",
									"field": "cash_flow_to_net_income_ratio",
									"operator": "lt",
									"value": 0.5,
									"null_behavior": "false"
								},
								{
									"type": "numeric",
									"field": "receivables_growth_rate",
									"operator": "gt",
									"value": 0.3,
									"null_behavior": "false"
								}
							]
						}
					]
				},
				"actions": [
					{
						"type": "set_field",
						"parameters": {
							"field": "quality_flags.poor_cash_flow_quality",
							"value": true
						}
					}
				]
			},
			{
				"name": "data_completeness_flag",
				"description": "Flag for incomplete financial data",
				"enabled": true,
				"priority": 70,
				"conditions": {
					"operator": "OR",
					"conditions": [
						{
							"type": "exists",
							"field": "total_assets",
							"operator": "eq",
							"value": false
						},
						{
							"type": "exists",
							"field": "total_revenue",
							"operator": "eq",
							"value": false
						},
						{
							"type": "exists",
							"field": "net_income",
							"operator": "eq",
							"value": false
						}
					]
				},
				"actions": [
					{
						"type": "set_field",
						"parameters": {
							"field": "data_quality.incomplete_financials",
							"value": true
						}
					},
					{
						"type": "log",
						"parameters": {
							"level": "warning",
							"message": "Incomplete financial data detected"
						}
					}
				]
			},
			{
				"name": "audit_concern_flag",
				"description": "Flag for audit-related concerns",
				"enabled": true,
				"priority": 85,
				"conditions": {
					"operator": "OR",
					"conditions": [
						{
							"type": "string",
							"field": "audit_opinion",
							"operator": "ne",
							"value": "unqualified",
							"case_sensitive": false,
							"null_behavior": "false"
						},
						{
							"type": "boolean",
							"field": "has_material_weakness",
							"operator": "eq",
							"value": true,
							"null_behavior": "false"
						},
						{
							"type": "regex",
							"field": "auditor_notes",
							"operator": "matches",
							"value": "(?i)(going concern|material uncertainty|significant doubt)",
							"null_behavior": "ignore"
						}
					]
				},
				"actions": [
					{
						"type": "set_field",
						"parameters": {
							"field": "audit_flags.has_concerns",
							"value": true
						}
					},
					{
						"type": "alert",
						"parameters": {
							"type": "dashboard",
							"priority": "high"
						}
					}
				]
			},
			{
				"name": "negative_equity_flag",
				"description": "Flag for companies with negative equity",
				"enabled": true,
				"priority": 95,
				"conditions": {
					"operator": "AND",
					"conditions": [
						{
							"type": "numeric",
							"field": "stockholders_equity",
							"operator": "lt",
							"value": 0,
							"null_behavior": "false"
						}
					]
				},
				"actions": [
					{
						"type": "set_field",
						"parameters": {
							"field": "risk_flags.negative_equity",
							"value": true
						}
					},
					{
						"type": "log",
						"parameters": {
							"level": "error",
							"message": "Negative equity detected - requires special valuation approach"
						}
					}
				]
			}
		]
	}`
}
