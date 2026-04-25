package industry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIndustryClassifier_Classify_SICCodeMatching tests SIC code based classification
func TestIndustryClassifier_Classify_SICCodeMatching(t *testing.T) {
	classifier := newTestClassifier(t)

	tests := []struct {
		name     string
		sicCode  string
		expected string
	}{
		{
			name:     "exact SIC code match for technology",
			sicCode:  "7372",
			expected: "TECH",
		},
		{
			name:     "exact SIC code match for financial services",
			sicCode:  "6020",
			expected: "FIN",
		},
		{
			name:     "exact SIC code match for healthcare",
			sicCode:  "8062",
			expected: "HEALTH",
		},
		{
			name:     "exact SIC code match for retail",
			sicCode:  "5311",
			expected: "RETAIL",
		},
		{
			name:     "exact SIC code match for energy",
			sicCode:  "1311",
			expected: "ENERGY",
		},
		{
			name:     "SIC range match for manufacturing (2000-3999)",
			sicCode:  "2500",
			expected: "MFG",
		},
		{
			name:     "SIC range match for manufacturing overlaps consumer goods",
			sicCode:  "2050",
			expected: "MFG", // MFG (priority 75) has broader range "2000-3999" and higher priority than CONS (priority 50)
		},
		{
			name:     "SIC range match for manufacturing upper bound",
			sicCode:  "3500",
			expected: "MFG",
		},
		{
			name:     "SIC range match for real estate",
			sicCode:  "6510",
			expected: "RESTATE",
		},
		{
			name:     "no match returns default code",
			sicCode:  "9999",
			expected: "NA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(context.Background(), tt.sicCode, "", "")
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Industry)
		})
	}
}

// TestIndustryClassifier_Classify_NAICSCodeMatching tests NAICS prefix matching
func TestIndustryClassifier_Classify_NAICSCodeMatching(t *testing.T) {
	classifier := newTestClassifier(t)

	tests := []struct {
		name      string
		naicsCode string
		expected  string
	}{
		{
			name:      "NAICS prefix match for financial services",
			naicsCode: "52211",
			expected:  "FIN",
		},
		{
			name:      "NAICS prefix match for healthcare",
			naicsCode: "62100",
			expected:  "HEALTH",
		},
		{
			name:      "NAICS prefix match for manufacturing",
			naicsCode: "31000",
			expected:  "MFG",
		},
		{
			name:      "NAICS prefix match for retail",
			naicsCode: "44100",
			expected:  "RETAIL",
		},
		{
			name:      "NAICS prefix match for tech (5415)",
			naicsCode: "541511",
			expected:  "TECH",
		},
		{
			name:      "no NAICS match returns default",
			naicsCode: "99999",
			expected:  "NA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(context.Background(), "", tt.naicsCode, "")
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Industry)
		})
	}
}

// TestIndustryClassifier_Classify_KeywordMatching tests company name keyword matching
func TestIndustryClassifier_Classify_KeywordMatching(t *testing.T) {
	classifier := newTestClassifier(t)

	tests := []struct {
		name        string
		companyName string
		expected    string
	}{
		{
			name:        "keyword match for bank",
			companyName: "First National Bank Corp",
			expected:    "FIN",
		},
		{
			name:        "keyword match for software",
			companyName: "Enterprise Software Solutions Inc",
			expected:    "TECH",
		},
		{
			// W-3: Sub-industry classification is now active — "pharmaceutical" matches the
			// HEALTH_PHARMA sub-industry, returning the more specific code instead of parent HEALTH.
			name:        "keyword match for pharmaceutical (sub-industry)",
			companyName: "Global Pharmaceutical Holdings",
			expected:    "HEALTH_PHARMA",
		},
		{
			name:        "keyword match for retail store",
			companyName: "Big Box Retail Store Chain",
			expected:    "RETAIL",
		},
		{
			name:        "keyword match for oil company",
			companyName: "Pacific Oil and Gas Corporation",
			expected:    "ENERGY",
		},
		{
			name:        "keyword match for REIT",
			companyName: "Pacific REIT Properties Trust",
			expected:    "RESTATE",
		},
		{
			name:        "exact name match for known company",
			companyName: "JPMorgan Chase & Co.",
			expected:    "FIN",
		},
		{
			name:        "exact name match case insensitive",
			companyName: "jpmorgan chase & co.",
			expected:    "FIN",
		},
		{
			name:        "no match returns default",
			companyName: "Mysterious Unknown Corp",
			expected:    "NA",
		},
		{
			name:        "empty name returns default",
			companyName: "",
			expected:    "NA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(context.Background(), "", "", tt.companyName)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Industry)
		})
	}
}

// TestIndustryClassifier_Classify_PriorityOrdering tests that higher-priority matches win
func TestIndustryClassifier_Classify_PriorityOrdering(t *testing.T) {
	classifier := newTestClassifier(t)

	// SIC match should win over keyword match when both match different industries
	// SIC 7372 = TECH (priority 100), keyword "bank" = FIN (priority 90)
	result, err := classifier.Classify(context.Background(), "7372", "", "Some Bank Software")
	require.NoError(t, err)
	assert.Equal(t, "TECH", result.Industry, "SIC match for TECH (priority 100) should win over keyword match for FIN")
}

// TestIndustryClassifier_Classify_SICFallbackToNAICS tests that NAICS is used when SIC doesn't match
func TestIndustryClassifier_Classify_SICFallbackToNAICS(t *testing.T) {
	classifier := newTestClassifier(t)

	// SIC code doesn't match, but NAICS does
	result, err := classifier.Classify(context.Background(), "9999", "52211", "")
	require.NoError(t, err)
	assert.Equal(t, "FIN", result.Industry, "Should fall back to NAICS matching when SIC doesn't match")
}

// TestIndustryClassifier_Classify_ConfigNotLoaded tests error when config is not loaded
func TestIndustryClassifier_Classify_ConfigNotLoaded(t *testing.T) {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
		codesConfig:   nil,
	}

	result, err := classifier.Classify(context.Background(), "7372", "", "")
	assert.Error(t, err)
	assert.Equal(t, "NA", result.Industry)
	assert.Contains(t, err.Error(), "not loaded")
}

// TestIndustryClassifier_matchSICCode tests SIC code matching with ranges
func TestIndustryClassifier_matchSICCode(t *testing.T) {
	classifier := NewIndustryClassifier()

	tests := []struct {
		name     string
		sicCode  string
		codes    []string
		expected bool
	}{
		{
			name:     "exact match",
			sicCode:  "7372",
			codes:    []string{"7370", "7371", "7372"},
			expected: true,
		},
		{
			name:     "range match inside",
			sicCode:  "2500",
			codes:    []string{"2000-3999"},
			expected: true,
		},
		{
			name:     "range match lower bound",
			sicCode:  "2000",
			codes:    []string{"2000-3999"},
			expected: true,
		},
		{
			name:     "range match upper bound",
			sicCode:  "3999",
			codes:    []string{"2000-3999"},
			expected: true,
		},
		{
			name:     "range no match below",
			sicCode:  "1999",
			codes:    []string{"2000-3999"},
			expected: false,
		},
		{
			name:     "range no match above",
			sicCode:  "4000",
			codes:    []string{"2000-3999"},
			expected: false,
		},
		{
			name:     "no match",
			sicCode:  "9999",
			codes:    []string{"7370", "7371", "7372"},
			expected: false,
		},
		{
			name:     "empty codes list",
			sicCode:  "7372",
			codes:    []string{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifier.matchSICCode(tt.sicCode, tt.codes)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestClassify_ReturnsClassificationResult pins the M-1b contract: Classify
// returns a ClassificationResult populated with sector / industry / sub-industry
// /  model_hint and echoes of the SIC + NAICS inputs. The "industry_classification"
// calc trace in valuation.Service depends on every field being populated.
func TestClassify_ReturnsClassificationResult(t *testing.T) {
	classifier := newTestClassifier(t)

	t.Run("parent-only match keeps Sector == Industry and SubIndustry empty", func(t *testing.T) {
		// SIC 6020 maps to FIN parent and has no sub-industry matcher in the
		// production config — exercises the no-sub-industry branch.
		result, err := classifier.Classify(context.Background(), "6020", "", "")
		require.NoError(t, err)

		assert.Equal(t, "FIN", result.Sector, "Sector must be the parent code")
		assert.Equal(t, "FIN", result.Industry, "Industry must equal Sector when no sub-industry matched")
		assert.Empty(t, result.SubIndustry, "SubIndustry must be empty when only parent matched")
		assert.Equal(t, "FIN", result.ModelHint, "ModelHint must equal Industry — model router keys on it")
		assert.Equal(t, "6020", result.SIC, "SIC echo")
		assert.Equal(t, "", result.NAICS, "NAICS echo (empty input)")
	})

	t.Run("sub-industry match diverges Sector from Industry and populates SubIndustry", func(t *testing.T) {
		// "Global Pharmaceutical Holdings" matches the HEALTH parent (via the
		// "pharmaceutical" parent keyword) AND the HEALTH_PHARMA sub-industry
		// (via the same keyword in the sub matchers). Sector should stay
		// "HEALTH" while Industry/ModelHint upgrade to "HEALTH_PHARMA".
		// Production-config sub-industries are addressable via parent + sub
		// keyword overlap — the classifier requires a parent match before
		// consulting sub-industries, so a sub-only SIC alone won't trigger
		// the refinement path.
		result, err := classifier.Classify(context.Background(), "", "", "Global Pharmaceutical Holdings")
		require.NoError(t, err)

		assert.Equal(t, "HEALTH", result.Sector, "Sector must remain the parent code on sub-industry match")
		assert.Equal(t, "HEALTH_PHARMA", result.Industry, "Industry must be the most-specific sub-industry code")
		assert.Equal(t, "HEALTH_PHARMA", result.SubIndustry, "SubIndustry must be populated when matched")
		assert.NotEqual(t, result.Sector, result.Industry, "Sector and Industry must diverge on sub-industry match")
		assert.Equal(t, "HEALTH_PHARMA", result.ModelHint, "ModelHint tracks the sub-industry — preserves model-routing semantics")
		assert.Equal(t, "", result.SIC, "SIC echo (empty input)")
	})

	t.Run("SIC and NAICS inputs are echoed for trace completeness", func(t *testing.T) {
		result, err := classifier.Classify(context.Background(), "7372", "541511", "")
		require.NoError(t, err)

		assert.Equal(t, "7372", result.SIC, "SIC echoed verbatim")
		assert.Equal(t, "541511", result.NAICS, "NAICS echoed verbatim")
	})

	t.Run("nil codes config still echoes SIC/NAICS so trace stays useful on error", func(t *testing.T) {
		// On the error path we still need SIC/NAICS in the result so the
		// observability trace can record what input the caller asked about.
		broken := &IndustryClassifier{
			sectorConfigs: make(map[string]*SectorConfig),
			codesConfig:   nil,
		}
		result, err := broken.Classify(context.Background(), "7372", "541511", "")

		require.Error(t, err)
		assert.Equal(t, "NA", result.Sector, "Sector falls back to NA on error")
		assert.Equal(t, "NA", result.Industry, "Industry falls back to NA on error")
		assert.Empty(t, result.SubIndustry, "SubIndustry empty on error path")
		assert.Equal(t, "NA", result.ModelHint, "ModelHint falls back to NA on error")
		assert.Equal(t, "7372", result.SIC, "SIC echo preserved on error path")
		assert.Equal(t, "541511", result.NAICS, "NAICS echo preserved on error path")
	})
}

// newTestClassifier creates a classifier with the production industry_codes.json config loaded.
func newTestClassifier(t *testing.T) *IndustryClassifier {
	t.Helper()

	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
	}
	classifier.loadDefaultConfigurations()

	// Load the real config file (path relative to this test file location)
	// The test runs from the package directory, so we need to traverse up to project root
	configPaths := []string{
		"../../../../config/datacleaner/industry_codes.json",
		"./config/datacleaner/industry_codes.json",
	}

	var loaded bool
	for _, path := range configPaths {
		if err := classifier.LoadIndustryCodesConfig(path); err == nil {
			loaded = true
			break
		}
	}

	if !loaded {
		t.Fatal("Failed to load industry_codes.json from any expected path")
	}

	return classifier
}
