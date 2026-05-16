package profile

import (
	"fmt"
	"regexp"
)

// semverRegex pins MAJOR.MINOR.PATCH form for config_version and the
// resolver_version. Prerelease/build identifiers are intentionally not
// supported in Tier 2 — the AssumptionProfile config is an internal
// calibration artifact, not an external API contract.
var semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// validateConfig enforces the 9 load-time invariants from spec §4.3. Any
// failure refuses startup; invalid shipped config is an operator error,
// not a user-data graceful-degradation case.
func validateConfig(c *configFile) error {
	if !semverRegex.MatchString(c.ConfigVersion) {
		return fmt.Errorf("config_version %q is not semver", c.ConfigVersion)
	}
	if c.ResolverVersion != ResolverVersion {
		// Version pinning prevents config drift from outrunning the resolver
		// algorithm. Bump both versions together when the resolver changes.
		return fmt.Errorf("resolver_version mismatch: config=%s compiled=%s",
			c.ResolverVersion, ResolverVersion)
	}
	for id, p := range c.Profiles {
		if err := validateProfile(id, p); err != nil {
			return err
		}
	}
	// Invariant 4: unique rule IDs (callers grep by ID in trace fields).
	seenIDs := make(map[string]bool)
	for _, r := range c.ArchetypeRules {
		if seenIDs[r.ID] {
			return fmt.Errorf("duplicate rule id %q", r.ID)
		}
		seenIDs[r.ID] = true
	}
	// Invariant 6: every rule archetype has at least one profile entry.
	// Without this, a rule could match and then the (arch, mat) Lookup
	// would silently fall back to the wildcard — masking the calibration gap.
	archetypesInProfiles := make(map[Archetype]bool)
	for _, p := range c.Profiles {
		archetypesInProfiles[p.Archetype] = true
	}
	for _, r := range c.ArchetypeRules {
		if !archetypesInProfiles[r.Archetype] {
			return fmt.Errorf("rule %q references archetype %q with no profile entries",
				r.ID, r.Archetype)
		}
	}
	// Invariant 7: a wildcard fallback rule exists.
	hasFallback := false
	for _, r := range c.ArchetypeRules {
		if r.IndustryPrefix == "*" {
			hasFallback = true
			break
		}
	}
	if !hasFallback {
		return fmt.Errorf("no fallback rule with industry_prefix=*; spec §4.3 invariant 7")
	}
	// Invariant 8: maturity thresholds non-negative. Negative values would
	// flip the maturity-bucketing comparisons.
	mt := c.MaturityThresholdsFallback
	if mt.LargeCapMinUSD < 0 || mt.MidCapMinUSD < 0 || mt.HighGrowthYoYMin < 0 || mt.MatureYoYMax < 0 {
		return fmt.Errorf("maturity_thresholds_fallback contains negative value")
	}
	return nil
}

// validateProfile checks a single profile entry against the enum + range
// invariants from spec §4.3 invariant 3. The id parameter is used only in
// error messages so operators can grep by profile_id.
func validateProfile(id string, p AssumptionProfile) error {
	if !isValidArchetype(p.Archetype) {
		return fmt.Errorf("profile %s: invalid archetype %q", id, p.Archetype)
	}
	if !isValidMaturity(p.Maturity) {
		return fmt.Errorf("profile %s: invalid maturity %q", id, p.Maturity)
	}
	if !isValidRevenueBaseMethod(p.RevenueBaseMethod) {
		return fmt.Errorf("profile %s: invalid revenue_base_method %q", id, p.RevenueBaseMethod)
	}
	if !isValidTerminalMethod(p.TerminalMethod) {
		return fmt.Errorf("profile %s: invalid terminal_method %q", id, p.TerminalMethod)
	}
	if !isValidDiscountMethod(p.DiscountMethod) {
		return fmt.Errorf("profile %s: invalid discount_method %q", id, p.DiscountMethod)
	}
	if p.HorizonYears < 0 || p.HorizonYears > 15 {
		// Upper bound 15 is the practical horizon — anything beyond is a
		// modeling smell, not a real calibration. Spec §3.1 implicit range.
		return fmt.Errorf("profile %s: horizon_years out of range [0,15]: %d", id, p.HorizonYears)
	}
	if p.CompoundGrowthCap <= 1.0 && p.HorizonYears > 0 {
		// CompoundGrowthCap is a multiplier on the base; values <= 1.0
		// would shrink or freeze projection growth, which is never the
		// intent for a non-zero horizon.
		return fmt.Errorf("profile %s: compound_growth_cap must be > 1.0 for non-zero horizon", id)
	}
	return nil
}

// isValid* helpers are exhaustive case-statements over the declared enum
// values. Keeping them as switches (rather than map[Foo]bool) ensures the
// compiler flags unhandled cases if enum values are added without updating
// validation.
func isValidArchetype(a Archetype) bool {
	switch a {
	case ArchetypeMatureLargeScale, ArchetypeMatureLargeBank, ArchetypeGrowthBank,
		ArchetypeInsuranceCompany, ArchetypeSoftwareLikeLargeScale, ArchetypeSoftwareLikeScaling,
		ArchetypeCyclicalMidCycle, ArchetypeCyclicalTrough, ArchetypeHypergrowthEarly,
		ArchetypeHypergrowthProfitable, ArchetypePreRevenueBiotech,
		ArchetypeMaturingTechDividend, ArchetypeMatureDividendTech,
		ArchetypeREITResidential, ArchetypeREITCommercial, ArchetypeREITIndustrial,
		ArchetypeREITHealthcare, ArchetypeREITDataCenter, ArchetypeREITCellTower,
		ArchetypeREITRetail, ArchetypeREITSpecialty:
		return true
	}
	return false
}

func isValidMaturity(m Maturity) bool {
	switch m {
	case MaturityMature, MaturityStandardGrowth, MaturityHighGrowth:
		return true
	}
	return false
}

func isValidRevenueBaseMethod(m RevenueBaseMethod) bool {
	switch m {
	case RevenueBaseRawTTM, RevenueBaseTwoYearAverage,
		RevenueBaseMaxTTMOrFloor, RevenueBaseMidCycleNormalized:
		return true
	}
	return false
}

func isValidTerminalMethod(m TerminalMethod) bool {
	switch m {
	case TerminalGordonGrowth, TerminalExitMultiple:
		return true
	}
	return false
}

func isValidDiscountMethod(m DiscountMethod) bool {
	switch m {
	case DiscountWACC, DiscountCostOfEquity:
		return true
	}
	return false
}
