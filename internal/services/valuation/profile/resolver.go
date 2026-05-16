package profile

import "strings"

// Resolve performs the 3-stage profile derivation per spec §5.1.
//
//	Stage 1:  industry → archetype via priority-ordered rule match
//	Stage 1b: cyclical-trough override when OperatingIncome < TroughOIThreshold
//	Stage 2:  revenue + YoY growth signals → maturity bucket
//	Stage 3:  archetype-specific maturity pin (overrides Stage 2 for archetypes
//	          whose semantics fix the maturity regardless of size signals)
//
// Pure function: no I/O, no time, no randomness. Determinism is a hard
// requirement for replay (spec §7.3). The matched rule's ID is stamped on
// the returned trace so audit consumers can grep by rule_id.
func (r *jsonRegistry) Resolve(facts Facts) (*ResolvedProfile, ResolutionTrace) {
	trace := ResolutionTrace{
		ResolverVersion: ResolverVersion,
		ConfigVersion:   r.configVersion,
		ConfigHash:      r.configHash,
	}

	// Stage 1: industry rule match. matched=false only when archetype_rules
	// is empty; an empty ruleset is rejected at load time only when no
	// wildcard exists, so in practice a real config always matches.
	arch, ruleID, matched, isWildcard := r.matchArchetypeRule(facts.IndustryNormalized)
	if !matched {
		trace.Source = SourceFallback
		trace.FallbackReason = "no_industry_rule_matched"
		return r.applyFallback(&trace), trace
	}
	trace.MatchedRuleID = ruleID
	if isWildcard {
		// The wildcard rule matched: industry was unknown to the explicit
		// ruleset. Source is fallback even though Stage 1 succeeded so audit
		// consumers can filter on "no real rule fired."
		trace.Source = SourceFallback
		trace.FallbackReason = "wildcard_rule_matched"
	} else {
		trace.Source = SourceExplicit
	}

	// Stage 1b: cyclical-trough override. A cyclical_mid_cycle archetype
	// with negative operating income flips to cyclical_trough so trough
	// calibrations (different terminal_method, longer fade) apply.
	if isCyclicalMidCycleArchetype(arch) && facts.OperatingIncome != nil &&
		*facts.OperatingIncome < r.maturityThresholds.TroughOIThreshold {
		arch = ArchetypeCyclicalTrough
		trace.HumanReason = "cyclical_trough_override:operating_income_negative"
	}

	// Stage 2: maturity derivation from size + YoY growth signals.
	mat, maturityReason := r.deriveMaturity(facts, arch)
	if maturityReason != "" {
		trace.HumanReason = joinReasons(trace.HumanReason, maturityReason)
	}

	// Stage 3: archetype-specific maturity pin. Mature-large-bank pins to
	// mature regardless of size signals — this is load-bearing for the
	// JPM bit-for-bit invariant (a JPM with hypothetical 30% YoY growth
	// must still resolve to mature_large_bank:mature for legacy path).
	if pinnedMat, pinned := archetypeMaturityPin(arch); pinned {
		mat = pinnedMat
	}

	// Lookup the (arch, mat) profile. A miss here means the config has a
	// gap — a rule matches an archetype but no profile entry exists for
	// the resolved maturity. validateConfig requires each rule archetype
	// to have at least one profile entry, but not for the SPECIFIC
	// maturity Stage 2/3 produces — so a fallback is the safe behavior.
	p, ok := r.Lookup(arch, mat)
	if !ok {
		trace.Source = SourceFallback
		trace.FallbackReason = "no_profile_for_resolved_key:" + string(arch) + ":" + string(mat)
		return r.applyFallback(&trace), trace
	}
	trace.ProfileID = p.ProfileID
	return &ResolvedProfile{AssumptionProfile: *p, Trace: trace}, trace
}

// matchArchetypeRule iterates the priority-sorted rule slice and returns
// the first match. Returns (archetype, ruleID, matched, isWildcard).
// isWildcard distinguishes the "industry hit the explicit fallback" case
// from a real industry rule firing — used to set Source correctly.
func (r *jsonRegistry) matchArchetypeRule(industryNormalized string) (Archetype, string, bool, bool) {
	for _, rule := range r.archetypeRules { // sorted desc by Priority at load
		if rule.IndustryPrefix == "*" {
			return rule.Archetype, rule.ID, true, true
		}
		// Prefix semantics: an exact-equal match counts, OR the industry
		// has a trailing underscore-prefix segment (so "FIN" matches
		// "FIN_LARGE_BANK" but does not match "FINANCE_TECH").
		if industryNormalized == rule.IndustryPrefix ||
			strings.HasPrefix(industryNormalized, rule.IndustryPrefix+"_") {
			return rule.Archetype, rule.ID, true, false
		}
	}
	return "", "", false, false
}

// deriveMaturity buckets the company into mature/standard/high based on
// revenue size + YoY growth. Returns the chosen maturity plus a short
// human-readable reason string for the trace's HumanReason field.
func (r *jsonRegistry) deriveMaturity(facts Facts, arch Archetype) (Maturity, string) {
	thresholds := r.thresholdsForArchetype(arch)
	if facts.Revenue == nil {
		// No revenue signal: default to standard_growth (the safest middle
		// bucket). HumanReason flags the ambiguity for audit.
		return MaturityStandardGrowth, "ambiguous_no_revenue_signal"
	}
	revenue := *facts.Revenue
	yoy := 0.0
	if facts.RevenueGrowthYoY != nil {
		yoy = *facts.RevenueGrowthYoY
	}
	// Large + slow-growing ⇒ mature. The two-signal check prevents a
	// $50B+ company growing 40% YoY (rare but real) from being miscast.
	if revenue >= thresholds.LargeCapMinUSD && yoy < r.maturityThresholds.MatureYoYMax {
		return MaturityMature, "large_cap_low_growth"
	}
	if yoy >= r.maturityThresholds.HighGrowthYoYMin {
		return MaturityHighGrowth, "yoy_above_high_growth_threshold"
	}
	return MaturityStandardGrowth, "default_standard_growth"
}

// archetypeMaturityPin returns the pinned maturity for archetypes that
// require a fixed maturity regardless of Stage-2 output. CRITICAL: the
// JPM bit-for-bit invariant depends on mature_large_bank pinning maturity=mature
// even when threshold drift would suggest otherwise.
func archetypeMaturityPin(arch Archetype) (Maturity, bool) {
	switch arch {
	case ArchetypeMatureLargeBank, ArchetypeMatureLargeScale, ArchetypeMatureDividendTech:
		return MaturityMature, true
	case ArchetypePreRevenueBiotech, ArchetypeHypergrowthEarly:
		return MaturityHighGrowth, true
	case ArchetypeCyclicalTrough:
		// Trough is by definition a temporary state with depressed
		// fundamentals; standard_growth is the recovery-path calibration.
		return MaturityStandardGrowth, true
	}
	return "", false
}

// isCyclicalMidCycleArchetype is the predicate for Stage-1b trough
// override. Kept as a function so future cyclical variants can join the
// set without touching the call site.
func isCyclicalMidCycleArchetype(arch Archetype) bool {
	return arch == ArchetypeCyclicalMidCycle
}

// joinReasons concatenates two HumanReason fragments with a separator,
// handling empty strings idempotently. The separator is deliberately
// human-readable; consumers that need structured access should read
// MatchedRuleID / FallbackReason directly.
func joinReasons(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}
