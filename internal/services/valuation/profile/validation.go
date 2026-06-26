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

// validateConfig enforces the 10 load-time invariants from spec §4.3. Any
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
	// VAL-3 Phase 3 (C1): terminal_multiple range guard. The exit-multiple
	// forward path (DDM multi-stage, FFO/AFFO projection) multiplies the
	// projected base by TerminalMultiple, so a typo'd 0 or negative value
	// silently zeroes the forward leg with no error (fail-open). Bound it to
	// (0, terminalMultipleCeiling] only when a non-zero horizon actually
	// engages the forward path — horizon==0 profiles never read it. REIT
	// terminal P/FFO multiples top out ~31x today, so 50 is a generous sanity
	// ceiling that still catches a misplaced decimal (e.g. 240 instead of 24).
	if p.HorizonYears > 0 {
		if p.TerminalMultiple <= 0 {
			return fmt.Errorf("profile %s: terminal_multiple must be > 0 when horizon_years > 0: %v",
				id, p.TerminalMultiple)
		}
		if p.TerminalMultiple > terminalMultipleCeiling {
			return fmt.Errorf("profile %s: terminal_multiple out of sane range (0,%.0f]: %v",
				id, terminalMultipleCeiling, p.TerminalMultiple)
		}
	}
	// Invariant 10 (T2-P4-W2 item 4): when both DividendForecastHorizon and
	// PayoutPath are populated, their lengths MUST match. The runtime guard
	// in models/ddm.go:342 (`i < len(p.PayoutPath)`) silently truncates the
	// payout-multiplier effect on mismatch; this load-time check promotes
	// the silent truncation to fail-fast at startup per spec §4.4 fail-loud
	// philosophy. Predicate requires BOTH conditions so that legacy single-
	// stage profiles (horizon=0, empty path) and multi-stage profiles that
	// intentionally defer payout-path population (horizon>0, empty path)
	// remain valid; only populated-but-mismatched configs are rejected.
	if p.DividendForecastHorizon > 0 && len(p.PayoutPath) > 0 &&
		len(p.PayoutPath) != p.DividendForecastHorizon {
		return fmt.Errorf("profile %s: payout_path length %d must equal dividend_forecast_horizon %d",
			id, len(p.PayoutPath), p.DividendForecastHorizon)
	}
	// Layer A — reinvestment / margin invariants (spec §6.3). Only enforced for
	// profiles that opt into a non-legacy method; legacy/empty profiles ignore
	// these fields entirely so pre-Layer-A config stays valid unchanged.
	if err := validateReinvestmentFields(id, p); err != nil {
		return err
	}
	// VAL-1 Phase 5 — diluted-share-forward clamp ceiling. 0 is valid (means
	// "use the code default"); a rate above 100%/yr is never a real calibration
	// (a typo guard, mirroring reinvestmentFieldCeiling). The boolean gate itself
	// needs no validation. Enforced regardless of the flag so a stray ceiling
	// fails loud even before a profile is enabled.
	if p.MaxAnnualDilutionRate < 0 || p.MaxAnnualDilutionRate > 1 {
		return fmt.Errorf("profile %s: max_annual_dilution_rate out of range [0,1]: %.4f", id, p.MaxAnnualDilutionRate)
	}
	return nil
}

// terminalMultipleCeiling is the upper sanity bound on a profile's exit
// terminal_multiple (VAL-3 Phase 3 C1). REIT terminal P/FFO multiples top out
// near 31x today; 50 still catches a misplaced decimal (240 vs 24) without
// rejecting any real calibration.
const terminalMultipleCeiling = 50.0

// marginCeiling is the hard validation cap on the converged operating-margin
// target — no archetype may converge above it (spec §7.3 "margin expansion is
// capped by archetype/industry"). 60% pre-tax operating margin is already beyond
// any real public-company sector norm; it is a guardrail, not a calibration.
const marginCeiling = 0.60

// reinvestmentFieldCeiling bounds the maintenance-capex floor and capex-intensity
// fractions so a typo (e.g. 30 instead of 0.30) fails loud rather than producing
// nonsense FCF.
const reinvestmentFieldCeiling = 0.50

// validateReinvestmentFields enforces the Layer-A range invariants. Enum validity
// is always checked (a garbage method string is a config error); numeric ranges
// are checked only for the method that actually consumes them.
func validateReinvestmentFields(id string, p AssumptionProfile) error {
	if !isValidReinvestmentMethod(p.ReinvestmentMethod) {
		return fmt.Errorf("profile %s: invalid reinvestment_method %q", id, p.ReinvestmentMethod)
	}
	if !isValidBaseMarginMethod(p.BaseMarginMethod) {
		return fmt.Errorf("profile %s: invalid base_margin_method %q", id, p.BaseMarginMethod)
	}
	// Legacy / empty: the Layer-A fields are ignored; nothing else to check.
	if p.ReinvestmentMethod == "" || p.ReinvestmentMethod == ReinvestmentLegacyProportional {
		return nil
	}
	if p.ReinvestmentFadeYears < 0 || p.ReinvestmentFadeYears > 15 {
		return fmt.Errorf("profile %s: reinvestment_fade_years out of range [0,15]: %d", id, p.ReinvestmentFadeYears)
	}
	if p.MarginConvergenceYears < 0 || p.MarginConvergenceYears > 15 {
		return fmt.Errorf("profile %s: margin_convergence_years out of range [0,15]: %d", id, p.MarginConvergenceYears)
	}
	if p.MaintenanceCapexFloor < 0 || p.MaintenanceCapexFloor > reinvestmentFieldCeiling {
		return fmt.Errorf("profile %s: maintenance_capex_floor out of range [0,%.2f]: %.4f", id, reinvestmentFieldCeiling, p.MaintenanceCapexFloor)
	}
	if p.TargetOperatingMargin <= 0 || p.TargetOperatingMargin > marginCeiling {
		return fmt.Errorf("profile %s: target_operating_margin out of range (0,%.2f]: %.4f", id, marginCeiling, p.TargetOperatingMargin)
	}
	// Cross-field sanity: the maintenance floor (net reinvestment as a fraction of
	// revenue) must stay below the steady-state operating margin. A floor at/above
	// the margin means reinvestment ≥ operating income in the late/flat years,
	// which forces non-positive FCF and is an economically-incoherent calibration
	// (catches typos like floor=0.30 with margin=0.20). The terminal year is exempt
	// from the floor entirely (§7.3), so this guards the explicit window.
	if p.MaintenanceCapexFloor >= p.TargetOperatingMargin {
		return fmt.Errorf("profile %s: maintenance_capex_floor (%.4f) must be < target_operating_margin (%.4f)",
			id, p.MaintenanceCapexFloor, p.TargetOperatingMargin)
	}
	switch p.ReinvestmentMethod {
	case ReinvestmentSalesToCapital:
		if p.SalesToCapitalStart <= 0 {
			return fmt.Errorf("profile %s: sales_to_capital_start must be > 0 for sales_to_capital", id)
		}
		if p.SalesToCapitalTarget < p.SalesToCapitalStart {
			return fmt.Errorf("profile %s: sales_to_capital_target (%.3f) must be ≥ sales_to_capital_start (%.3f)",
				id, p.SalesToCapitalTarget, p.SalesToCapitalStart)
		}
	case ReinvestmentDecliningCapexIntensity:
		if p.CapExIntensityStart <= 0 || p.CapExIntensityStart > reinvestmentFieldCeiling {
			return fmt.Errorf("profile %s: capex_intensity_start out of range (0,%.2f]: %.4f", id, reinvestmentFieldCeiling, p.CapExIntensityStart)
		}
		if p.CapExIntensityMature < 0 || p.CapExIntensityMature > p.CapExIntensityStart {
			return fmt.Errorf("profile %s: capex_intensity_mature (%.4f) must be in [0, capex_intensity_start=%.4f]",
				id, p.CapExIntensityMature, p.CapExIntensityStart)
		}
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
		ArchetypeREITResidential, ArchetypeREITOffice, ArchetypeREITIndustrial,
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

// isValidReinvestmentMethod accepts the empty string (a pre-Layer-A profile,
// treated as legacy_proportional) plus the three declared methods.
func isValidReinvestmentMethod(m ReinvestmentMethod) bool {
	switch m {
	case "", ReinvestmentLegacyProportional, ReinvestmentSalesToCapital, ReinvestmentDecliningCapexIntensity:
		return true
	}
	return false
}

// isValidBaseMarginMethod accepts the empty string (defaults to ttm) plus the
// three declared base-margin methods.
func isValidBaseMarginMethod(m BaseMarginMethod) bool {
	switch m {
	case "", BaseMarginTTM, BaseMarginTwoYearAvg, BaseMarginMidCycle:
		return true
	}
	return false
}
