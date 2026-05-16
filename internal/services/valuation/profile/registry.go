package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Registry is the lookup surface for profiles. Designed so a future
// DB-backed implementation can swap in without touching consumers (see
// docs/refactoring/spec/assumption-profile-db-backed-future.md). The Tier 2
// implementation is jsonRegistry, loaded once at fx startup and shared
// across the request path; the interface is safe for concurrent reads.
type Registry interface {
	// Resolve runs the 3-stage resolution algorithm against the supplied
	// Facts. Returns a non-nil *ResolvedProfile on success (including the
	// fallback case — the fallback profile is still a valid resolution);
	// the returned trace carries Source + matched-rule-id + human reason.
	Resolve(facts Facts) (*ResolvedProfile, ResolutionTrace)

	// Lookup returns the profile entry for an exact (archetype, maturity)
	// pair. Used by tests and by integration sites that already know which
	// profile they want without running the resolver.
	Lookup(arch Archetype, mat Maturity) (*AssumptionProfile, bool)

	// ConfigVersion returns the semver of the loaded assumption_profiles.json.
	ConfigVersion() string
	// ConfigHash returns the SHA-256 of the canonicalized config JSON,
	// stamped onto every ResolutionTrace for replay determinism.
	ConfigHash() string
}

// ArchetypeRule is one priority-ordered rule in the resolver's Stage-1
// industry → archetype mapping. Higher Priority fires first; the wildcard
// IndustryPrefix "*" is the unconditional fallback.
type ArchetypeRule struct {
	ID             string    `json:"id"`
	Priority       int       `json:"priority"`
	IndustryPrefix string    `json:"industry_prefix"`
	Archetype      Archetype `json:"archetype"`
	Notes          string    `json:"notes,omitempty"`
}

// MaturityThresholds carries the global fallback size + growth cutoffs for
// the resolver's Stage-2 maturity bucketing. Archetype-specific overrides
// live on AssumptionProfile.SizeThresholds.
type MaturityThresholds struct {
	LargeCapMinUSD    float64 `json:"large_cap_revenue_min_usd"`
	MidCapMinUSD      float64 `json:"mid_cap_revenue_min_usd"`
	HighGrowthYoYMin  float64 `json:"high_growth_revenue_yoy_min"`
	MatureYoYMax      float64 `json:"mature_revenue_yoy_max"`
	TroughOIThreshold float64 `json:"trough_oi_threshold"`
}

// configFile is the on-disk JSON shape parsed by LoadFromJSON. Private to
// the package; the runtime registry exposes a richer indexed structure.
type configFile struct {
	ConfigVersion              string                       `json:"config_version"`
	ResolverVersion            string                       `json:"resolver_version"`
	Profiles                   map[string]AssumptionProfile `json:"profiles"`
	ArchetypeRules             []ArchetypeRule              `json:"archetype_rules"`
	MaturityThresholdsFallback MaturityThresholds           `json:"maturity_thresholds_fallback"`
}

// archetypeMaturityKey is the composite key for the profile lookup map.
// Using a struct key (rather than a concatenated string) avoids ambiguity
// when an archetype name contains a colon.
type archetypeMaturityKey struct {
	Arch Archetype
	Mat  Maturity
}

// jsonRegistry is the concrete JSON-backed Registry implementation. Frozen
// at construction; safe for concurrent reads. Rule matching uses the sorted
// archetypeRules slice — no map iteration, so resolution is deterministic.
type jsonRegistry struct {
	configVersion      string
	configHash         string
	profiles           map[archetypeMaturityKey]*AssumptionProfile
	archetypeRules     []ArchetypeRule
	fallbackProfile    *AssumptionProfile
	maturityThresholds MaturityThresholds
}

// LoadFromJSON loads the registry from assumption_profiles.json on disk.
// Returns an error on any of:
//   - file not readable
//   - JSON malformed
//   - validation failure (unknown archetype, missing fallback, etc.)
//
// The service MUST fail startup on any of these — invalid shipped config is
// an operator error, not user-data graceful-degradation (spec §4.4).
//
// Path-based loading is kept for tests + replay (which read fixtures from
// arbitrary on-disk locations). Production wiring should prefer LoadFromBytes
// + the configfs embed.FS so the binary is hermetic against cwd.
func LoadFromJSON(path string) (Registry, error) {
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return LoadFromBytes(rawBytes, path)
}

// LoadFromBytes parses + validates the registry from in-memory bytes. Used
// by production wiring with the configfs embed.FS contents so the binary
// is hermetic against process cwd; matches the country_risk / industry_
// multiples conventions for tests. label is included in error messages for
// operator debuggability (e.g. "assumption_profiles.json:embed").
func LoadFromBytes(rawBytes []byte, label string) (Registry, error) {
	var cfg configFile
	if err := json.Unmarshal(rawBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validate %s: %w", label, err)
	}

	// Sort archetype_rules by descending priority so the resolver iterates
	// in deterministic match order. SliceStable preserves the input order
	// among equal priorities — useful only for diagnostic predictability;
	// the validation rejects ambiguous priority ties when they would mask
	// actual mismatches.
	rules := make([]ArchetypeRule, len(cfg.ArchetypeRules))
	copy(rules, cfg.ArchetypeRules)
	sort.SliceStable(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})

	// Index profiles by (archetype, maturity). Force ProfileID to match the
	// map key so callers can always trust ResolvedProfile.ProfileID even if
	// the JSON entry's internal profile_id field drifted from its key.
	idx := make(map[archetypeMaturityKey]*AssumptionProfile, len(cfg.Profiles))
	for id, p := range cfg.Profiles {
		profileCopy := p
		profileCopy.ProfileID = id
		idx[archetypeMaturityKey{Arch: p.Archetype, Mat: p.Maturity}] = &profileCopy
	}

	// Locate the fallback profile (industry_prefix "*"). Used by Resolve to
	// return a non-nil profile even on resolution failure so consumers never
	// have to nil-check the resolved value.
	fallback := selectFallbackProfile(rules, idx)
	if fallback == nil {
		// validateConfig already requires hasFallback, so reaching this
		// branch means the fallback archetype has no profile entries —
		// a separate invariant (spec §4.3 invariant 6).
		return nil, fmt.Errorf("validate %s: fallback rule archetype has no profile entries", label)
	}

	// SHA-256 of the canonicalized JSON gives a stable hash even when the
	// source file has cosmetic whitespace variation between captures. Both
	// captures parse to the same cfg struct and remarshal identically.
	canonical, _ := json.Marshal(&cfg) // rawBytes already parsed successfully
	sum := sha256.Sum256(canonical)

	return &jsonRegistry{
		configVersion:      cfg.ConfigVersion,
		configHash:         hex.EncodeToString(sum[:]),
		profiles:           idx,
		archetypeRules:     rules,
		fallbackProfile:    fallback,
		maturityThresholds: cfg.MaturityThresholdsFallback,
	}, nil
}

// selectFallbackProfile finds the wildcard-rule archetype and returns any
// profile entry for it. Returns nil when the wildcard archetype has no
// profile entries (caller must surface as a validation error).
func selectFallbackProfile(rules []ArchetypeRule, idx map[archetypeMaturityKey]*AssumptionProfile) *AssumptionProfile {
	var fallbackArch Archetype
	for _, rule := range rules {
		if rule.IndustryPrefix == "*" {
			fallbackArch = rule.Archetype
			break
		}
	}
	if fallbackArch == "" {
		return nil
	}
	// Prefer MaturityStandardGrowth for the fallback when available; it's
	// the most defensible default for an unknown industry. Otherwise pick
	// any maturity-variant deterministically.
	if p, ok := idx[archetypeMaturityKey{Arch: fallbackArch, Mat: MaturityStandardGrowth}]; ok {
		return p
	}
	// Deterministic scan by sorted maturity for stable fallback selection.
	for _, mat := range []Maturity{MaturityMature, MaturityHighGrowth} {
		if p, ok := idx[archetypeMaturityKey{Arch: fallbackArch, Mat: mat}]; ok {
			return p
		}
	}
	return nil
}

// ConfigVersion returns the semver of the loaded assumption_profiles.json.
func (r *jsonRegistry) ConfigVersion() string { return r.configVersion }

// ConfigHash returns the SHA-256 hex-encoded hash of the canonicalized
// loaded config, stamped onto every ResolutionTrace.
func (r *jsonRegistry) ConfigHash() string { return r.configHash }

// Lookup returns the profile entry for an exact (archetype, maturity)
// pair. Resolver consumers use Resolve; Lookup is exported for tests and
// for sites that already know which profile they want.
func (r *jsonRegistry) Lookup(arch Archetype, mat Maturity) (*AssumptionProfile, bool) {
	p, ok := r.profiles[archetypeMaturityKey{Arch: arch, Mat: mat}]
	return p, ok
}

// applyFallback returns the conservative fallback profile as a
// ResolvedProfile, copying it together with the supplied trace. Callers
// must have already stamped the trace's Source and FallbackReason.
func (r *jsonRegistry) applyFallback(trace *ResolutionTrace) *ResolvedProfile {
	if r.fallbackProfile == nil {
		return nil
	}
	trace.ProfileID = r.fallbackProfile.ProfileID
	return &ResolvedProfile{
		AssumptionProfile: *r.fallbackProfile,
		Trace:             *trace,
	}
}

// thresholdsForArchetype returns the size thresholds for the given
// archetype, using the archetype-specific overrides on any profile entry
// when set, otherwise the global fallback. Iterates profiles map; the
// first profile entry with non-nil SizeThresholds wins (entries within an
// archetype are expected to share thresholds, so order is irrelevant).
func (r *jsonRegistry) thresholdsForArchetype(arch Archetype) SizeThresholds {
	for k, p := range r.profiles {
		if k.Arch == arch && p.SizeThresholds != nil {
			return *p.SizeThresholds
		}
	}
	return SizeThresholds{
		LargeCapMinUSD: r.maturityThresholds.LargeCapMinUSD,
		MidCapMinUSD:   r.maturityThresholds.MidCapMinUSD,
	}
}
