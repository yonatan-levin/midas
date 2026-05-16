package profile

// ResolutionTrace is the structured audit trail for a single resolution
// outcome (spec §3.3). Replaces an originally-proposed single route_reason
// string with a typed record so downstream consumers (FairValueResponse,
// artifact bundle manifest, replay) can introspect what fired and why
// without parsing free-form text.
type ResolutionTrace struct {
	ProfileID       string   `json:"profile_id"`
	Source          Source   `json:"source"`
	ResolverVersion string   `json:"resolver_version"`
	ConfigVersion   string   `json:"config_version"`
	ConfigHash      string   `json:"config_hash,omitempty"`
	MatchedRuleID   string   `json:"matched_rule_id,omitempty"`
	FallbackReason  string   `json:"fallback_reason,omitempty"`
	MissingFacts    []string `json:"missing_facts,omitempty"`
	HumanReason     string   `json:"human_reason,omitempty"`
}

// Source classifies how a profile was selected. Lands as a string enum in
// the bundle manifest + FairValueResponse so audit consumers can filter on
// "show me everything that fell back to the default."
type Source string

const (
	// SourceExplicit means an industry rule matched (non-wildcard) and the
	// resolved (archetype, maturity) had a profile entry.
	SourceExplicit Source = "explicit"
	// SourceInferred means the rule match was partial (e.g. industry was
	// known but maturity was derived from defaults vs. archetype-specific
	// thresholds). Reserved for future use; today maturity inference is
	// folded into SourceExplicit + HumanReason.
	SourceInferred Source = "inferred"
	// SourceFallback means no non-wildcard industry rule matched, or the
	// resolved (archetype, maturity) had no profile entry; the conservative
	// fallback profile (industry_prefix "*") was applied instead.
	SourceFallback Source = "fallback"
)

// AssumptionProfileManifest is the bundle-manifest extension carrying the
// full resolved profile + audit trail (spec §3.3, §7.3). Lands in the
// replay bundle alongside existing manifest fields like clock_seed and
// git_sha. Two consumption modes:
//   - ResolvedSnapshot present → replay uses it directly for perfect
//     determinism regardless of config drift.
//   - ResolvedSnapshot nil → replay re-resolves; mismatched ConfigHash
//     triggers a non_reproducible_replay warning.
type AssumptionProfileManifest struct {
	ProfileID        string             `json:"profile_id"`
	Source           Source             `json:"source"`
	ResolverVersion  string             `json:"resolver_version"`
	ConfigVersion    string             `json:"config_version"`
	ConfigHash       string             `json:"config_hash"`
	ResolvedSnapshot *AssumptionProfile `json:"resolved_snapshot,omitempty"`
	Trace            ResolutionTrace    `json:"trace"`
}
