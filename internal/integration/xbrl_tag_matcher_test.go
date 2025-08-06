// Package integration contains integration tests for XBRL tag matching
package integration

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestXBRLTagMatcherIntegration tests the complete XBRL tag matching flow
func TestXBRLTagMatcherIntegration(t *testing.T) {
	// Setup test configuration
	cfg := setupTestConfig(t)
	logger := log.New(os.Stdout, "TEST: ", log.LstdFlags)

	// Create the service
	matcher := datacleaner.NewXBRLTagMatcherService(cfg, logger)

	t.Run("MatchTags_ValidData_Success", func(t *testing.T) {
		// Prepare test XBRL data
		xbrlData := &entities.XBRLData{
			Namespace: "us-gaap",
			Context: entities.XBRLContext{
				EntityID:   "0000123456",
				PeriodType: "instant",
				EndDate:    time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			},
			Facts: map[string]interface{}{
				"us-gaap:Assets":             "1500000", // In thousands
				"us-gaap:Liabilities":        "900000",
				"us-gaap:StockholdersEquity": "600000",
				"us-gaap:Revenues":           "2000000",
				"us-gaap:NetIncomeLoss":      "150000",
				"dei:EntityRegistrantName":   "Test Company Inc.",
			},
			Units: map[string]string{
				"USD": "iso4217:USD",
			},
		}

		// Execute matching
		ctx := context.Background()
		results, err := matcher.MatchTags(ctx, xbrlData)

		// Assertions
		require.NoError(t, err)
		assert.Len(t, results, 6, "Should match all 6 provided tags")

		// Verify specific matches
		assetMatch := findMatchByField(results, "total_assets")
		require.NotNil(t, assetMatch)
		assert.Equal(t, float64(1500000000), assetMatch.Value) // Multiplied by 1000
		assert.Equal(t, "us-gaap:Assets", assetMatch.OriginalTag)
		assert.Contains(t, assetMatch.TransformationsApplied, "multiply_by_thousand")

		companyMatch := findMatchByField(results, "company_name")
		require.NotNil(t, companyMatch)
		assert.Equal(t, "Test Company Inc.", companyMatch.Value)
	})

	t.Run("MatchTags_AlternativeTags_Success", func(t *testing.T) {
		// Test with alternative tag names
		xbrlData := &entities.XBRLData{
			Namespace: "",
			Facts: map[string]interface{}{
				"TotalAssets":        "1000000",
				"TotalLiabilities":   "600000",
				"ShareholdersEquity": "400000",
				"Revenue":            "1500000",
				"NetIncome":          "200000",
				"CompanyName":        "Alternative Corp",
			},
		}

		ctx := context.Background()
		results, err := matcher.MatchTags(ctx, xbrlData)

		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(results), 6, "Should match all alternative tags")

		// Check that alternative tags were matched with lower confidence
		assetMatch := findMatchByField(results, "total_assets")
		require.NotNil(t, assetMatch)
		assert.Less(t, assetMatch.Confidence, 1.0, "Alternative tag should have lower confidence")
	})

	t.Run("MatchTags_MissingRequiredTags_Error", func(t *testing.T) {
		// XBRL data missing required tags
		xbrlData := &entities.XBRLData{
			Namespace: "us-gaap",
			Facts: map[string]interface{}{
				"us-gaap:Assets": "1000000",
				// Missing: Liabilities, StockholdersEquity, Revenues, NetIncomeLoss, EntityRegistrantName
			},
		}

		ctx := context.Background()
		results, err := matcher.MatchTags(ctx, xbrlData)

		// Should return results but with an error about missing required fields
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing required tags")
		assert.NotEmpty(t, results, "Should still return successful matches")
	})

	t.Run("ValidateMatches_BalanceSheetEquation_Success", func(t *testing.T) {
		// Prepare matched results that satisfy the balance sheet equation
		matches := []entities.MatchResult{
			{
				InternalField: "total_assets",
				Value:         float64(1000000),
			},
			{
				InternalField: "total_liabilities",
				Value:         float64(600000),
			},
			{
				InternalField: "stockholders_equity",
				Value:         float64(400000),
			},
		}

		ctx := context.Background()
		err := matcher.ValidateMatches(ctx, matches)

		assert.NoError(t, err, "Balance sheet should balance")
	})

	t.Run("ValidateMatches_InvalidRange_Error", func(t *testing.T) {
		// Prepare matches with invalid values
		matches := []entities.MatchResult{
			{
				InternalField: "total_assets",
				Value:         float64(-1000), // Negative assets
			},
		}

		ctx := context.Background()
		err := matcher.ValidateMatches(ctx, matches)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Total assets must be positive")
	})

	t.Run("MatchSingleTag_WithTransformations_Success", func(t *testing.T) {
		ctx := context.Background()

		// Test currency symbol removal and decimal conversion
		result, err := matcher.MatchSingleTag(ctx, "us-gaap:Assets", "$1,500,000")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, float64(1500000000), result.Value) // Converted and multiplied
		assert.Contains(t, result.TransformationsApplied, "to_decimal")
		assert.Contains(t, result.TransformationsApplied, "multiply_by_thousand")
	})

	t.Run("GetRequiredTags_ReturnsAllRequired", func(t *testing.T) {
		requiredTags := matcher.GetRequiredTags()

		// Should include primary and alternative tags for required fields
		assert.Contains(t, requiredTags, "us-gaap:Assets")
		assert.Contains(t, requiredTags, "us-gaap:Liabilities")
		assert.Contains(t, requiredTags, "us-gaap:StockholdersEquity")
		assert.Contains(t, requiredTags, "us-gaap:Revenues")
		assert.Contains(t, requiredTags, "us-gaap:NetIncomeLoss")
		assert.Contains(t, requiredTags, "dei:EntityRegistrantName")

		// Should also include alternative tags
		assert.Contains(t, requiredTags, "TotalAssets")
		assert.Contains(t, requiredTags, "CompanyName")
	})
}

// TestXBRLTagMatcherDataTypes tests various data type conversions
func TestXBRLTagMatcherDataTypes(t *testing.T) {
	cfg := setupTestConfig(t)
	logger := log.New(io.Discard, "", 0) // Silent logger for tests
	matcher := datacleaner.NewXBRLTagMatcherService(cfg, logger)

	ctx := context.Background()

	testCases := []struct {
		name          string
		tag           string
		value         interface{}
		expectedValue interface{}
		expectError   bool
	}{
		{
			name:          "String value",
			tag:           "dei:EntityRegistrantName",
			value:         "  Test Company  ",
			expectedValue: "Test Company",
			expectError:   false,
		},
		{
			name:          "Integer to decimal",
			tag:           "us-gaap:Assets",
			value:         1500,
			expectedValue: float64(1500000), // Multiplied by 1000
			expectError:   false,
		},
		{
			name:          "Float value",
			tag:           "us-gaap:Assets",
			value:         1500.5,
			expectedValue: float64(1500500), // Multiplied by 1000
			expectError:   false,
		},
		{
			name:          "String with currency symbol",
			tag:           "us-gaap:Assets",
			value:         "$1,500,000.50",
			expectedValue: float64(1500000500), // Parsed and multiplied
			expectError:   false,
		},
		{
			name:          "Invalid numeric string",
			tag:           "us-gaap:Assets",
			value:         "not a number",
			expectedValue: nil,
			expectError:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := matcher.MatchSingleTag(ctx, tc.tag, tc.value)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, tc.expectedValue, result.Value)
			}
		})
	}
}

// TestXBRLConfigurationLoading tests configuration loading and validation
func TestXBRLConfigurationLoading(t *testing.T) {
	t.Run("LoadValidConfig_Success", func(t *testing.T) {
		// Create a temporary config file
		configData := createTestConfigJSON()
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "xbrl_config.json")

		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Load the configuration
		cfg, err := config.LoadXBRLConfig(configPath)

		require.NoError(t, err)
		assert.Equal(t, "1.0.0", cfg.Version)
		assert.NotEmpty(t, cfg.TagMappings)
		assert.NotEmpty(t, cfg.ValidationRules)
	})

	t.Run("LoadInvalidConfig_Error", func(t *testing.T) {
		// Create invalid config (missing required fields)
		invalidConfig := `{
			"version": "",
			"tag_mappings": {}
		}`

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "invalid_config.json")
		err := os.WriteFile(configPath, []byte(invalidConfig), 0644)
		require.NoError(t, err)

		// Try to load the configuration
		_, err = config.LoadXBRLConfig(configPath)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "configuration version is required")
	})

	t.Run("LoadFromEnvironmentVariable", func(t *testing.T) {
		// Create config file
		configData := createTestConfigJSON()
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "env_config.json")
		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Set environment variable
		require.NoError(t, os.Setenv("XBRL_CONFIG_PATH", configPath))
		defer func() { _ = os.Unsetenv("XBRL_CONFIG_PATH") }()

		// Load without specifying path
		cfg, err := config.LoadXBRLConfig("")

		require.NoError(t, err)
		assert.NotNil(t, cfg)
	})
}

// Helper Functions

// setupTestConfig creates a test configuration
func setupTestConfig(t *testing.T) *config.XBRLTagConfig {
	configJSON := createTestConfigJSON()

	var cfg config.XBRLTagConfig
	err := json.Unmarshal([]byte(configJSON), &cfg)
	require.NoError(t, err)

	return &cfg
}

// createTestConfigJSON returns a test configuration as JSON string
func createTestConfigJSON() string {
	return `{
		"version": "1.0.0",
		"default_namespace": "us-gaap",
		"tag_mappings": {
			"assets": {
				"xbrl_tag": "us-gaap:Assets",
				"internal_field": "total_assets",
				"data_type": "decimal",
				"required": true,
				"transformations": ["remove_currency_symbol", "to_decimal", "multiply_by_thousand"],
				"alternative_tags": ["TotalAssets"],
				"context": "instant"
			},
			"liabilities": {
				"xbrl_tag": "us-gaap:Liabilities",
				"internal_field": "total_liabilities",
				"data_type": "decimal",
				"required": true,
				"transformations": ["remove_currency_symbol", "to_decimal", "multiply_by_thousand"],
				"alternative_tags": ["TotalLiabilities"],
				"context": "instant"
			},
			"equity": {
				"xbrl_tag": "us-gaap:StockholdersEquity",
				"internal_field": "stockholders_equity",
				"data_type": "decimal",
				"required": true,
				"transformations": ["remove_currency_symbol", "to_decimal", "multiply_by_thousand"],
				"alternative_tags": ["ShareholdersEquity"],
				"context": "instant"
			},
			"revenue": {
				"xbrl_tag": "us-gaap:Revenues",
				"internal_field": "total_revenue",
				"data_type": "decimal",
				"required": true,
				"transformations": ["remove_currency_symbol", "to_decimal", "multiply_by_thousand"],
				"alternative_tags": ["Revenue"],
				"context": "duration"
			},
			"net_income": {
				"xbrl_tag": "us-gaap:NetIncomeLoss",
				"internal_field": "net_income",
				"data_type": "decimal",
				"required": true,
				"transformations": ["remove_currency_symbol", "to_decimal", "multiply_by_thousand"],
				"alternative_tags": ["NetIncome"],
				"context": "duration"
			},
			"company_name": {
				"xbrl_tag": "dei:EntityRegistrantName",
				"internal_field": "company_name",
				"data_type": "string",
				"required": true,
				"transformations": ["trim"],
				"alternative_tags": ["CompanyName"],
				"context": "instant"
			}
		},
		"validation_rules": [
			{
				"name": "balance_sheet_equation",
				"type": "consistency",
				"field": "total_assets",
				"parameters": {
					"equation": "total_assets = total_liabilities + stockholders_equity",
					"tolerance": 0.01
				},
				"error_message": "Balance sheet does not balance"
			},
			{
				"name": "assets_positive",
				"type": "range",
				"field": "total_assets",
				"parameters": {
					"min": 0
				},
				"error_message": "Total assets must be positive"
			}
		]
	}`
}

// findMatchByField finds a match result by internal field name
func findMatchByField(results []entities.MatchResult, field string) *entities.MatchResult {
	for _, result := range results {
		if result.InternalField == field {
			return &result
		}
	}
	return nil
}
