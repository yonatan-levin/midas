package industry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIndustryClassifier_Classify_SubIndustry verifies W-3: sub-industry classification
// returns the more specific code (e.g., TECH_SAAS) when a sub-industry matches.
func TestIndustryClassifier_Classify_SubIndustry(t *testing.T) {
	classifier := newTestClassifier(t)

	tests := []struct {
		name        string
		sicCode     string
		naicsCode   string
		companyName string
		expected    string
	}{
		{
			name:        "SaaS company matches TECH_SAAS sub-industry",
			companyName: "Acme SaaS Platform Inc",
			expected:    "TECH_SAAS",
		},
		{
			name:        "AI company matches TECH_AI sub-industry",
			companyName: "Quantum Artificial Intelligence Labs",
			expected:    "TECH_AI",
		},
		{
			name:        "Biotech company matches HEALTH_BIOTECH sub-industry",
			companyName: "Genesis Biotech Corp",
			expected:    "HEALTH_BIOTECH",
		},
		{
			name:        "Pharmaceutical matches HEALTH_PHARMA sub-industry",
			companyName: "Global Pharmaceutical Holdings",
			expected:    "HEALTH_PHARMA",
		},
		{
			name:        "Generic tech company falls through to TECH parent",
			companyName: "Nondescript Tech Corp",
			expected:    "TECH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := classifier.Classify(context.Background(), tt.sicCode, tt.naicsCode, tt.companyName)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIndustryClassifier_Classify_RegexPreCompiled verifies W-2: regexes are compiled once
// at load time, not on every Classify() call. We check by inspecting the cached slices.
func TestIndustryClassifier_Classify_RegexPreCompiled(t *testing.T) {
	classifier := newTestClassifier(t)

	require.NotNil(t, classifier.codesConfig, "codesConfig must be loaded")
	require.NotEmpty(t, classifier.codesConfig.Mappings, "must have parent mappings")

	// For each mapping with keywords/patterns, the compiled slices must be populated
	// and match the source slice length (entries may be nil for long keywords that
	// use strings.Contains, but the slice itself must exist).
	for _, m := range classifier.codesConfig.Mappings {
		assert.Equal(t, len(m.Matchers.Keywords), len(m.compiledKeywords),
			"compiledKeywords length must match Matchers.Keywords for %s", m.Code)
		assert.Equal(t, len(m.Matchers.Patterns), len(m.compiledPatterns),
			"compiledPatterns length must match Matchers.Patterns for %s", m.Code)

		// Sub-industries must also be pre-compiled
		for _, sub := range m.SubIndustries {
			assert.Equal(t, len(sub.Matchers.Keywords), len(sub.compiledKeywords),
				"sub-industry %s compiledKeywords length mismatch", sub.Code)
			assert.Equal(t, len(sub.Matchers.Patterns), len(sub.compiledPatterns),
				"sub-industry %s compiledPatterns length mismatch", sub.Code)
		}
	}

	// Short keywords (<=3 chars) must have non-nil compiled regexes
	// Long keywords use strings.Contains so their compiled entries are nil
	for _, m := range classifier.codesConfig.Mappings {
		for i, keyword := range m.Matchers.Keywords {
			if len(keyword) <= 3 {
				assert.NotNil(t, m.compiledKeywords[i],
					"short keyword %q in %s must have compiled regex", keyword, m.Code)
			}
		}
	}
}

// TestCompileKeywordRegexes verifies the helper compiles short keywords correctly.
func TestCompileKeywordRegexes(t *testing.T) {
	compiled := compileKeywordRegexes([]string{"ai", "oil", "technology", "bank"})
	require.Len(t, compiled, 4)

	// "ai" is short (<=3) → compiled
	assert.NotNil(t, compiled[0])
	assert.True(t, compiled[0].MatchString("artificial AI company"))
	assert.False(t, compiled[0].MatchString("retail"), "word-boundary must prevent matching inside retail")

	// "oil" is short → compiled
	assert.NotNil(t, compiled[1])
	assert.True(t, compiled[1].MatchString("big OIL corp"))

	// "technology" is long → not compiled (uses strings.Contains)
	assert.Nil(t, compiled[2])

	// "bank" is long → not compiled
	assert.Nil(t, compiled[3])
}
