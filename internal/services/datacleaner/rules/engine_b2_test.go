package rules

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// SR-1 B2 regression tests — LoadIndustryRules used to mutate the SHARED base
// rule set in place: overrides wrote through the *CleaningRule pointers held
// in e.rules, and special rules were injected into the base index. The first
// industry load therefore permanently installed that industry's thresholds /
// severities / enabled-flags for EVERY subsequent ticker in the process
// (order-dependent cleaning results), and industry-only special rules leaked
// into the generic rule set.
//
// Corrected contract pinned here:
//   - the base rule set stays PRISTINE across any number of industry loads;
//   - overrides + special rules are visible only through the per-industry
//     snapshot (GetIndustryRules(gicsCode));
//   - GetRuleByID returns the BASE definition for base rules, and still
//     resolves industry-only special rules (read-only fallback scan) so
//     callers that look up special rules by ID keep working.
//
// See docs/reviewer/archive/SR-1-simplify-and-code-review-candidates.md §B2.
func TestLoadIndustryRules_DoesNotMutateBaseRules(t *testing.T) {
	rulesFile := createTempFile(t, "rules.json", createTestRulesJSON())
	industryFile := createTempFile(t, "tech.json", createTestIndustryJSON())

	engine := NewRuleEngine()
	require.NoError(t, engine.LoadRules(rulesFile))

	// Snapshot the base rule's pre-load state (fixture: enabled, 5% threshold,
	// severity info).
	pre, err := engine.GetRuleByID(entities.RuleCapitalizedSoftware)
	require.NoError(t, err)
	require.True(t, pre.Enabled)
	require.NotNil(t, pre.Threshold)
	require.NotNil(t, pre.Threshold.PercentageOfRevenue)
	require.Equal(t, 0.05, *pre.Threshold.PercentageOfRevenue)
	require.Equal(t, entities.Info, pre.Severity)

	// Load the tech industry overrides (disable capitalized_software, tighten
	// its threshold to 2%, raise severity) — twice, to also pin idempotence.
	require.NoError(t, engine.LoadIndustryRules(industryFile))
	require.NoError(t, engine.LoadIndustryRules(industryFile))

	t.Run("base rule stays pristine after industry load", func(t *testing.T) {
		post, err := engine.GetRuleByID(entities.RuleCapitalizedSoftware)
		require.NoError(t, err)
		assert.True(t, post.Enabled, "industry override must not disable the BASE rule")
		require.NotNil(t, post.Threshold)
		require.NotNil(t, post.Threshold.PercentageOfRevenue)
		assert.Equal(t, 0.05, *post.Threshold.PercentageOfRevenue,
			"industry threshold override must not leak into the BASE rule")
		assert.Equal(t, entities.Info, post.Severity,
			"industry severity override must not leak into the BASE rule")
	})

	t.Run("generic (no-industry) rule set is unaffected", func(t *testing.T) {
		// GetIndustryRules("") is the fallback path every non-45/25 ticker
		// takes in production. (capitalized_software is industry ["45"]-scoped
		// and was never part of this set — its pristine state is pinned via
		// GetRuleByID above.) The generic set must keep the "all"-industry
		// rules enabled and must NOT pick up the tech-only special rule.
		baseSet := engine.GetIndustryRules("")
		ids := map[string]bool{}
		for _, r := range baseSet {
			ids[r.ID] = true
		}
		assert.True(t, ids[entities.RuleGoodwillExclusion],
			"all-industry goodwill rule must remain in the generic rule set")
		assert.True(t, ids[entities.RuleStockCompensation],
			"all-industry stock-compensation rule must remain in the generic rule set")
		assert.False(t, ids["tech_specific_rule"],
			"tech-only special rule must not leak into the generic rule set")
	})

	t.Run("industry snapshot carries the override", func(t *testing.T) {
		techRules := engine.GetIndustryRules("45")
		for _, r := range techRules {
			if r.ID == entities.RuleCapitalizedSoftware {
				t.Fatalf("capitalized_software is disabled by the tech override and must not appear in the enabled tech snapshot")
			}
		}
		var sawSpecial bool
		for _, r := range techRules {
			if r.ID == "tech_specific_rule" {
				sawSpecial = true
			}
		}
		assert.True(t, sawSpecial, "special rule must be present in the tech snapshot")
	})

	t.Run("special rule still resolvable by ID", func(t *testing.T) {
		special, err := engine.GetRuleByID("tech_specific_rule")
		require.NoError(t, err)
		assert.Equal(t, "tech_specific_rule", special.ID)
	})

	t.Run("GetRules excludes industry-only special rules", func(t *testing.T) {
		ids := map[string]bool{}
		for _, r := range engine.GetRules(nil) {
			ids[r.ID] = true
		}
		assert.True(t, ids[entities.RuleCapitalizedSoftware],
			"base capitalized_software must stay enabled in GetRules")
		assert.False(t, ids["tech_specific_rule"],
			"special rule must not be injected into the base index")
	})
}
