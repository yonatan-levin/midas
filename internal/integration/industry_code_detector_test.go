// Package integration contains integration tests for industry code detection
package integration

import (
	"context"
	"encoding/json"
	"io"
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

// TestIndustryCodeDetectorIntegration tests the industry code detection system
func TestIndustryCodeDetectorIntegration(t *testing.T) {
	// Setup test configuration
	cfg := setupIndustryTestConfig(t)
	logger := log.New(os.Stdout, "TEST: ", log.LstdFlags)

	// Create the detector service
	detector, err := datacleaner.NewIndustryCodeDetectorService(cfg, logger)
	require.NoError(t, err, "Failed to create detector service")

	ctx := context.Background()

	t.Run("DetectByExactName", func(t *testing.T) {
		input := ports.IndustryDetectionInput{
			CompanyName: "Microsoft Corporation",
		}

		result, err := detector.DetectIndustryCode(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "TECH", result.Code)
		assert.Equal(t, "Technology", result.Name)
		assert.Equal(t, 1.0, result.Confidence)
		assert.Equal(t, "exact_name", result.MatchMethod)
	})

	t.Run("DetectBySICCode", func(t *testing.T) {
		input := ports.IndustryDetectionInput{
			CompanyName: "ABC Software Inc.",
			SICCode:     "7372",
		}

		result, err := detector.DetectIndustryCode(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "TECH", result.Code)
		assert.Equal(t, 0.95, result.Confidence)
		assert.Equal(t, "sic_code", result.MatchMethod)
	})

	t.Run("DetectByNAICSCode", func(t *testing.T) {
		input := ports.IndustryDetectionInput{
			CompanyName: "XYZ Financial Services",
			NAICSCode:   "523110", // Investment Banking
		}

		result, err := detector.DetectIndustryCode(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "FIN", result.Code)
		assert.Equal(t, 0.95, result.Confidence)
		assert.Equal(t, "naics_code", result.MatchMethod)
	})

	t.Run("DetectByKeywords", func(t *testing.T) {
		// Use input that matches keywords but not patterns
		input := ports.IndustryDetectionInput{
			CompanyName: "Generic Corp",
			Description: "We provide cloud-based computing solutions for businesses",
		}

		result, err := detector.DetectIndustryCode(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "TECH", result.Code)
		assert.GreaterOrEqual(t, result.Confidence, 0.5)
		assert.Contains(t, result.MatchMethod, "keywords:")
	})

	t.Run("DetectByPattern", func(t *testing.T) {
		// Use input that matches patterns (has "tech" or "software" as whole words)
		input := ports.IndustryDetectionInput{
			CompanyName: "TechVentures Ltd",
			Description: "We build software products",
		}

		result, err := detector.DetectIndustryCode(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "TECH", result.Code)
		assert.Equal(t, 0.8, result.Confidence)
		assert.Contains(t, result.MatchMethod, "pattern:")
	})

	t.Run("DetectWithSubIndustry", func(t *testing.T) {
		input := ports.IndustryDetectionInput{
			CompanyName: "AI Solutions Inc.",
			Description: "Machine learning and artificial intelligence services",
			NAICSCode:   "5415", // Computer Systems Design
		}

		result, err := detector.DetectIndustryCode(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "TECH", result.Code)
		assert.NotNil(t, result.SubIndustry)
		assert.Equal(t, "TECH_AI", result.SubIndustry.Code)
		assert.Equal(t, "Artificial Intelligence", result.SubIndustry.Name)
	})

	t.Run("DetectMultipleKeywordMatch", func(t *testing.T) {
		input := ports.IndustryDetectionInput{
			Description: "Banking and financial investment services with capital management",
		}

		result, err := detector.DetectIndustryCode(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "FIN", result.Code)
		// Should have higher confidence due to multiple keyword matches
		assert.GreaterOrEqual(t, result.Confidence, 0.6)
	})

	t.Run("NoMatchReturnsDefault", func(t *testing.T) {
		input := ports.IndustryDetectionInput{
			CompanyName: "Unknown Business Type LLC",
			Description: "We do various things",
		}

		result, err := detector.DetectIndustryCode(ctx, input)

		require.NoError(t, err)
		assert.Equal(t, "NA", result.Code)
		assert.Equal(t, "Not Classified", result.Name)
		assert.Equal(t, 0.0, result.Confidence)
		assert.Equal(t, "default", result.MatchMethod)
	})

	t.Run("GetIndustryByCode", func(t *testing.T) {
		// Test valid code
		info, err := detector.GetIndustryByCode("TECH")

		require.NoError(t, err)
		assert.Equal(t, "TECH", info.Code)
		assert.Equal(t, "Technology", info.Name)
		assert.Len(t, info.SubIndustries, 1)

		// Test invalid code
		_, err = detector.GetIndustryByCode("INVALID")
		assert.Error(t, err)
	})

	t.Run("ValidateIndustryCode", func(t *testing.T) {
		// Valid codes
		assert.NoError(t, detector.ValidateIndustryCode("TECH"))
		assert.NoError(t, detector.ValidateIndustryCode("FIN"))
		assert.NoError(t, detector.ValidateIndustryCode("NA"))
		assert.NoError(t, detector.ValidateIndustryCode("TECH_AI"))

		// Invalid code
		assert.Error(t, detector.ValidateIndustryCode("INVALID_CODE"))
	})

	t.Run("EmptyInputError", func(t *testing.T) {
		input := ports.IndustryDetectionInput{}

		_, err := detector.DetectIndustryCode(ctx, input)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one input field must be provided")
	})

	t.Run("NormalizeCompanyNames", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected string
		}{
			{
				input:    "Microsoft Corp.",
				expected: "TECH",
			},
			{
				input:    "MICROSOFT CORPORATION",
				expected: "TECH",
			},
			{
				input:    "Microsoft Inc",
				expected: "TECH",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.input, func(t *testing.T) {
				input := ports.IndustryDetectionInput{
					CompanyName: tc.input,
				}

				result, err := detector.DetectIndustryCode(ctx, input)

				require.NoError(t, err)
				assert.Equal(t, tc.expected, result.Code)
			})
		}
	})
}

// TestIndustryCodeBackwardCompatibility tests the backward compatibility function
func TestIndustryCodeBackwardCompatibility(t *testing.T) {
	cfg := setupIndustryTestConfig(t)
	logger := log.New(io.Discard, "", 0)

	detector, err := datacleaner.NewIndustryCodeDetectorService(cfg, logger)
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("ValidInput", func(t *testing.T) {
		input := ports.IndustryDetectionInput{
			CompanyName: "Bank of America Corporation",
		}

		code := datacleaner.IndustryCodeWithDefaultNA(detector, ctx, input)

		assert.Equal(t, "FIN", code)
	})

	t.Run("ErrorReturnsNA", func(t *testing.T) {
		// Empty input will cause error
		input := ports.IndustryDetectionInput{}

		code := datacleaner.IndustryCodeWithDefaultNA(detector, ctx, input)

		assert.Equal(t, "NA", code)
	})
}

// TestIndustryConfigValidation tests configuration validation
func TestIndustryConfigValidation(t *testing.T) {
	t.Run("ValidConfig", func(t *testing.T) {
		cfg := &config.IndustryCodeConfig{
			Version:     "1.0.0",
			DefaultCode: "NA",
			Mappings: []config.IndustryMapping{
				{
					Name: "Test Industry",
					Code: "TEST",
					Matchers: config.IndustryMatchers{
						Keywords: []string{"test"},
					},
				},
			},
		}

		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("MissingVersion", func(t *testing.T) {
		cfg := &config.IndustryCodeConfig{
			DefaultCode: "NA",
			Mappings:    []config.IndustryMapping{},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "configuration version is required")
	})

	t.Run("MissingDefaultCode", func(t *testing.T) {
		cfg := &config.IndustryCodeConfig{
			Version:  "1.0.0",
			Mappings: []config.IndustryMapping{},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "default code is required")
	})

	t.Run("NoMappings", func(t *testing.T) {
		cfg := &config.IndustryCodeConfig{
			Version:     "1.0.0",
			DefaultCode: "NA",
			Mappings:    []config.IndustryMapping{},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one industry mapping is required")
	})

	t.Run("DuplicateCode", func(t *testing.T) {
		cfg := &config.IndustryCodeConfig{
			Version:     "1.0.0",
			DefaultCode: "NA",
			Mappings: []config.IndustryMapping{
				{
					Name: "Industry 1",
					Code: "TEST",
					Matchers: config.IndustryMatchers{
						Keywords: []string{"test"},
					},
				},
				{
					Name: "Industry 2",
					Code: "TEST", // Duplicate
					Matchers: config.IndustryMatchers{
						Keywords: []string{"test2"},
					},
				},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate industry code")
	})

	t.Run("NoMatchers", func(t *testing.T) {
		cfg := &config.IndustryCodeConfig{
			Version:     "1.0.0",
			DefaultCode: "NA",
			Mappings: []config.IndustryMapping{
				{
					Name:     "Test Industry",
					Code:     "TEST",
					Matchers: config.IndustryMatchers{}, // No matchers
				},
			},
		}

		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one matcher is required")
	})
}

// TestIndustryConfigLoading tests configuration file loading
func TestIndustryConfigLoading(t *testing.T) {
	t.Run("LoadFromFile", func(t *testing.T) {
		// Create temporary config file
		configData := createIndustryTestConfigJSON()
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "industry_codes.json")

		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Load configuration
		cfg, err := config.LoadIndustryCodeConfig(configPath)

		require.NoError(t, err)
		assert.Equal(t, "1.0.0", cfg.Version)
		assert.Equal(t, "NA", cfg.DefaultCode)
		assert.GreaterOrEqual(t, len(cfg.Mappings), 3)
	})

	t.Run("LoadFromEnvironment", func(t *testing.T) {
		// Create config file
		configData := createIndustryTestConfigJSON()
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "env_industry_codes.json")
		err := os.WriteFile(configPath, []byte(configData), 0644)
		require.NoError(t, err)

		// Set environment variable
		require.NoError(t, os.Setenv("INDUSTRY_CODE_CONFIG_PATH", configPath))
		defer func() { _ = os.Unsetenv("INDUSTRY_CODE_CONFIG_PATH") }()

		// Load without specifying path
		cfg, err := config.LoadIndustryCodeConfig("")

		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.Equal(t, "NA", cfg.DefaultCode)
	})
}

// Helper Functions

// setupIndustryTestConfig creates a test configuration
func setupIndustryTestConfig(t *testing.T) *config.IndustryCodeConfig {
	configJSON := createIndustryTestConfigJSON()

	var cfg config.IndustryCodeConfig
	err := json.Unmarshal([]byte(configJSON), &cfg)
	require.NoError(t, err)

	return &cfg
}

// createIndustryTestConfigJSON returns a minimal test configuration
func createIndustryTestConfigJSON() string {
	return `{
		"version": "1.0.0",
		"default_code": "NA",
		"mappings": [
			{
				"name": "Technology",
				"code": "TECH",
				"priority": 100,
				"matchers": {
					"sic_codes": ["7371", "7372"],
					"naics_codes": ["5112", "5415"],
					"keywords": ["software", "technology", "cloud"],
					"patterns": ["\\b(tech|software)\\b"],
					"exact_names": ["Microsoft Corporation", "Microsoft Corp.", "Microsoft Inc"]
				},
				"sub_industries": [
					{
						"name": "Artificial Intelligence",
						"code": "TECH_AI",
						"matchers": {
							"keywords": ["artificial intelligence", "machine learning"]
						}
					}
				]
			},
			{
				"name": "Financial Services",
				"code": "FIN",
				"priority": 90,
				"matchers": {
					"naics_codes": ["523"],
					"keywords": ["bank", "financial", "investment"],
					"exact_names": ["Bank of America Corporation"]
				}
			},
			{
				"name": "Healthcare",
				"code": "HEALTH",
				"priority": 85,
				"matchers": {
					"keywords": ["healthcare", "medical", "pharmaceutical"]
				}
			}
		],
		"validation_rules": []
	}`
}
