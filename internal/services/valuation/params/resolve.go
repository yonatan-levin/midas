package params

import "fmt"

// This file implements the two-phase resolver (plan §3.4 / §3.5 / §3.7).
//
//	ResolveInputs   — phase 1: precedence merge + WACC-INDEPENDENT invariants.
//	ResolveTerminal — phase 2: terminal-growth resolution against the COMPUTED WACC.
//
// PRIME DIRECTIVE: with empty Overrides the resolved values MUST be byte-identical
// to today's config+profile+literal reads, and ResolveTerminal's auto-derive path
// MUST reproduce service.go::calculateTerminalGrowthRate bit-for-bit when the cap
// equals DefaultTerminalGrowthCap (0.03). The auto-derive arithmetic below is a
// FAITHFUL, VERBATIM port — do not "improve" it.

// JSON knob names used as Provenance keys. These match the options DTO field
// catalog (design §5) so the handler can echo applied_overrides without a second
// mapping table.
const (
	knobTerminalGrowthRate = "terminal_growth_rate"
	knobTerminalGrowthCap  = "terminal_growth_cap"
	knobHorizonYears       = "horizon_years"
	knobStage1Years        = "stage1_years"
	knobStage2Years        = "stage2_years"
	knobStage3Years        = "stage3_years"
	knobMaxGrowthRate      = "max_growth_rate"
	knobMinGrowthRate      = "min_growth_rate"
	knobTerminalMethod     = "terminal_method"
	knobTerminalMultiple   = "terminal_multiple"
	knobTaxRate            = "tax_rate"
	knobBeta               = "beta"
	knobRiskFreeRate       = "risk_free_rate"
	knobGrowthStages       = "growth_stages"
)

// terminalMethodExitMultiple is the enum value selecting the exit-multiple
// terminal model. Mirrors profile.TerminalExitMultiple without importing the
// profile package (the resolver is scalar-only).
const terminalMethodExitMultiple = "exit_multiple"

// ResolveInputs performs phase 1: it merges the precedence layers
// (default ← profile ← override) for every WACC-independent knob, records the
// Source of each resolved knob in Provenance, and validates the cross-knob
// invariants that do NOT depend on the computed WACC.
//
// growthRateLen is the length of the growth-rate slice the estimator produced for
// THIS request (needed for the horizon ≤ length invariant); the caller passes it
// after the estimator runs (plan §3.7 ordering).
//
// On the DEFAULT path (empty Overrides), the returned params reproduce today's
// reads exactly and no error is returned. Cross-knob violations return a typed
// *ParamError (mapped to HTTP 422 by the handler).
//
// R2 narrowing (plan §8): the horizon ≤ stage-sum and horizon ≤ growthRateLen
// checks return 422 ONLY when the horizon is request-sourced. When the horizon
// comes from profile/default, the legacy silent-clamp behavior is preserved (clamp
// + a warning note appended to Warnings) so the default path stays byte-identical.
func ResolveInputs(d Defaults, o Overrides, growthRateLen int) (EffectiveValuationParams, error) {
	p := EffectiveValuationParams{
		Provenance: make(map[string]Source),
	}

	// --- Growth-rate bounds (config ← override; no profile source) ---------
	// N1/N2 (carry-forward): a zero config Default*GrowthRate means "no config
	// value"; fall back to the named default constant. Production always populates
	// these from config, but unit tests and edge configs rely on this.
	p.MaxGrowthRate = d.MaxGrowthRate
	if p.MaxGrowthRate == 0 {
		p.MaxGrowthRate = DefaultMaxGrowthRate
	}
	p.Provenance[knobMaxGrowthRate] = SourceDefault
	if o.MaxGrowthRate != nil {
		p.MaxGrowthRate = *o.MaxGrowthRate
		p.Provenance[knobMaxGrowthRate] = SourceRequest
	}

	p.MinGrowthRate = d.MinGrowthRate
	if p.MinGrowthRate == 0 {
		p.MinGrowthRate = DefaultMinGrowthRate
	}
	p.Provenance[knobMinGrowthRate] = SourceDefault
	if o.MinGrowthRate != nil {
		p.MinGrowthRate = *o.MinGrowthRate
		p.Provenance[knobMinGrowthRate] = SourceRequest
	}

	// --- Growth-stage durations (estimator default ← override) -------------
	// N3 (carry-forward): zero Stage1/Stage2 → const default. Stage3 zero is the
	// legitimate legacy 7-year-horizon signal and is used as-is (DefaultStage3Years
	// is itself 0, so no special-casing is needed, but we DO NOT coerce a zero
	// Stage3 to a non-zero default).
	p.Stage1Years = d.Stage1Years
	if p.Stage1Years == 0 {
		p.Stage1Years = DefaultStage1Years
	}
	p.Provenance[knobStage1Years] = SourceDefault
	if o.Stage1Years != nil {
		p.Stage1Years = *o.Stage1Years
		p.Provenance[knobStage1Years] = SourceRequest
	}

	p.Stage2Years = d.Stage2Years
	if p.Stage2Years == 0 {
		p.Stage2Years = DefaultStage2Years
	}
	p.Provenance[knobStage2Years] = SourceDefault
	if o.Stage2Years != nil {
		p.Stage2Years = *o.Stage2Years
		p.Provenance[knobStage2Years] = SourceRequest
	}

	// Stage3: pass d.Stage3Years through verbatim (0 is legitimate). DefaultStage3Years
	// is documented as 0; an explicit override may raise it.
	p.Stage3Years = d.Stage3Years
	p.Provenance[knobStage3Years] = SourceDefault
	if o.Stage3Years != nil {
		p.Stage3Years = *o.Stage3Years
		p.Provenance[knobStage3Years] = SourceRequest
	}

	// --- Terminal growth cap (config ← override; no profile source) --------
	p.TerminalGrowthCap = d.TerminalGrowthCap
	if p.TerminalGrowthCap == 0 {
		p.TerminalGrowthCap = DefaultTerminalGrowthCap
	}
	p.Provenance[knobTerminalGrowthCap] = SourceDefault
	if o.TerminalGrowthCap != nil {
		p.TerminalGrowthCap = *o.TerminalGrowthCap
		p.Provenance[knobTerminalGrowthCap] = SourceRequest
	}

	// --- Explicit terminal growth rate (override only; no profile source) --
	// Auto-derived in ResolveTerminal when not explicit. We record the explicit
	// value + TerminalGrowthExplicit here; the final < WACC invariant is phase 2.
	if o.TerminalGrowthRate != nil {
		p.TerminalGrowthRate = *o.TerminalGrowthRate
		p.TerminalGrowthExplicit = true
		p.Provenance[knobTerminalGrowthRate] = SourceRequest
	}

	// --- Terminal method (const default ← profile ← override) --------------
	p.TerminalMethod = DefaultTerminalMethod
	p.Provenance[knobTerminalMethod] = SourceDefault
	if d.ProfileTerminalMethod != "" {
		// Mirrors the legacy gate at service.go:1126
		// (resolvedProfile.TerminalMethod != "").
		p.TerminalMethod = d.ProfileTerminalMethod
		p.Provenance[knobTerminalMethod] = SourceProfile
	}
	if o.TerminalMethod != nil {
		p.TerminalMethod = *o.TerminalMethod
		p.Provenance[knobTerminalMethod] = SourceRequest
	}

	// --- Terminal multiple (industry lookup ← profile ← override) ----------
	// Default source is the industry EV/EBITDA lookup (0 = none found). A profile
	// value (>0) overrides the industry default; an explicit request override wins.
	p.TerminalMultiple = d.IndustryExitMultiple
	if d.IndustryExitMultiple > 0 {
		p.Provenance[knobTerminalMultiple] = SourceDefault
	}
	if d.ProfileTerminalMultiple > 0 {
		p.TerminalMultiple = d.ProfileTerminalMultiple
		p.Provenance[knobTerminalMultiple] = SourceProfile
	}
	if o.TerminalMultiple != nil {
		p.TerminalMultiple = *o.TerminalMultiple
		p.Provenance[knobTerminalMultiple] = SourceRequest
	}

	// --- WACC inputs + tax (data-source baseline ← override) ---------------
	p.Beta = d.Beta
	if o.Beta != nil {
		p.Beta = *o.Beta
		p.Provenance[knobBeta] = SourceRequest
	}

	p.RiskFreeRate = d.RiskFreeRate
	if o.RiskFreeRate != nil {
		p.RiskFreeRate = *o.RiskFreeRate
		p.Provenance[knobRiskFreeRate] = SourceRequest
	}

	p.MarketRiskPremium = d.MarketRiskPremium
	if o.MarketRiskPremium != nil {
		p.MarketRiskPremium = *o.MarketRiskPremium
		p.Provenance["market_risk_premium"] = SourceRequest
	}

	p.TaxRate = d.TaxRate
	if o.TaxRate != nil {
		p.TaxRate = *o.TaxRate
		p.Provenance[knobTaxRate] = SourceRequest
	}

	// --- Horizon (growth-rate length / profile ← override) -----------------
	// Default source is the legacy growth-rate slice length; a profile value (>0)
	// overrides it (legacy gate service.go:1110); an explicit request override wins.
	p.HorizonYears = growthRateLen
	p.Provenance[knobHorizonYears] = SourceDefault
	if d.ProfileHorizonYears > 0 {
		p.HorizonYears = d.ProfileHorizonYears
		p.Provenance[knobHorizonYears] = SourceProfile
	}
	if o.HorizonYears != nil {
		p.HorizonYears = *o.HorizonYears
		p.Provenance[knobHorizonYears] = SourceRequest
	}

	// =====================================================================
	// Cross-knob invariants (WACC-independent). Terminal-growth < WACC is
	// validated in phase 2 (ResolveTerminal).
	// =====================================================================

	// Staging structural invariants: min ≤ max, stage-sum ≥ 1. Shared with the
	// pre-estimator pre-check (validateStaging) so the two never drift.
	if err := validateStaging(p.MinGrowthRate, p.MaxGrowthRate,
		p.Stage1Years, p.Stage2Years, p.Stage3Years); err != nil {
		return EffectiveValuationParams{}, err
	}

	// Exit-multiple resolvability (placed here, NOT in ResolveTerminal — see the
	// doc comment on this invariant below). When method == "exit_multiple" a
	// terminal multiple must be resolvable from override/profile/industry default.
	if p.TerminalMethod == terminalMethodExitMultiple && p.TerminalMultiple <= 0 {
		return EffectiveValuationParams{}, &ParamError{
			Knob:   knobTerminalMultiple,
			Reason: "required when terminal_method is \"exit_multiple\" and no industry default is available",
			Value:  p.TerminalMultiple,
		}
	}

	// Horizon invariants. R2 narrowing: when horizon is request-sourced, an
	// out-of-range horizon is a hard 422. When horizon comes from profile/default,
	// preserve the legacy silent clamp + WARN (no 422) so the default path stays
	// byte-identical (mirrors the clamp at service.go:1117).
	stageSum := p.Stage1Years + p.Stage2Years + p.Stage3Years
	horizonRequestSourced := p.Provenance[knobHorizonYears] == SourceRequest

	if p.HorizonYears > stageSum {
		if horizonRequestSourced {
			return EffectiveValuationParams{}, &ParamError{
				Knob:   knobHorizonYears,
				Reason: "must be ≤ stage1_years + stage2_years + stage3_years",
				Value:  float64(p.HorizonYears),
				Limit:  float64(stageSum),
			}
		}
		// Profile/default-sourced: clamp + warn (legacy behavior).
		p.Warnings = append(p.Warnings, fmt.Sprintf(
			"horizon_years (%d) exceeds growth-stage sum (%d); clamped to %d",
			p.HorizonYears, stageSum, stageSum))
		p.HorizonYears = stageSum
	}

	// Recompute after a possible stage-sum clamp so the length check uses the
	// (possibly already clamped) horizon. growthRateLen is the post-estimator
	// slice length; horizon must not exceed it.
	if growthRateLen > 0 && p.HorizonYears > growthRateLen {
		if horizonRequestSourced {
			return EffectiveValuationParams{}, &ParamError{
				Knob:   knobHorizonYears,
				Reason: "must be ≤ the number of projected growth rates",
				Value:  float64(p.HorizonYears),
				Limit:  float64(growthRateLen),
			}
		}
		// Profile/default-sourced: clamp + warn (legacy service.go:1117 behavior).
		p.Warnings = append(p.Warnings, fmt.Sprintf(
			"horizon_years (%d) exceeds available growth rates (%d); clamped to %d",
			p.HorizonYears, growthRateLen, growthRateLen))
		p.HorizonYears = growthRateLen
	}

	return p, nil
}

// ResolveTerminal performs phase 2: it finalizes the terminal growth rate against
// the COMPUTED WACC, mutating p in place.
//
//   - Explicit path (p.TerminalGrowthExplicit): use p.TerminalGrowthRate as-is;
//     assert it is strictly < computedWACC (else 422). The cap is NOT applied on
//     the explicit path (design §5 / §8 R8). A value within 1% of WACC (but still
//     below it) appends a near-WACC advisory to p.Warnings — it is NOT an error.
//   - Auto-derive path: a FAITHFUL, VERBATIM port of
//     service.go::calculateTerminalGrowthRate, using p.TerminalGrowthCap as the cap,
//     DefaultTerminalGrowthFloor as the ≤0 floor, DefaultTerminalWACCSpread as the
//     spread, and DefaultTerminalGrowthDegenWACCFloor as the post-spread degenerate
//     floor. Byte-identical to the legacy function when p.TerminalGrowthCap == 0.03.
//
// Provenance for terminal_growth_rate is set to SourceDefault on the auto-derive
// path (it was set to SourceRequest in ResolveInputs on the explicit path).
func ResolveTerminal(p *EffectiveValuationParams, computedWACC, historicalCAGR float64) error {
	if p.Provenance == nil {
		p.Provenance = make(map[string]Source)
	}

	if p.TerminalGrowthExplicit {
		// Hard invariant: explicit terminal growth must be strictly below WACC to
		// keep the Gordon perpetuity finite. Applies regardless of cap.
		if computedWACC > 0 && p.TerminalGrowthRate >= computedWACC {
			return &ParamError{
				Knob:   knobTerminalGrowthRate,
				Reason: "must be strictly less than WACC",
				Value:  p.TerminalGrowthRate,
				Limit:  computedWACC,
			}
		}
		// Soft advisory: within 1% of WACC (but still below). Surfaced to the caller.
		if computedWACC > 0 && computedWACC-p.TerminalGrowthRate < 0.01 {
			p.Warnings = append(p.Warnings, fmt.Sprintf(
				"terminal_growth_rate (%g) is within 1%% of WACC (%g); terminal value is highly sensitive",
				p.TerminalGrowthRate, computedWACC))
		}
		return nil
	}

	// --- Auto-derive path: VERBATIM port of calculateTerminalGrowthRate -----
	// (service.go:1721-1745). The ONLY substitution is the cap literal 0.03 →
	// p.TerminalGrowthCap; every other literal is replaced by its named constant
	// of the SAME numeric value, so the result is byte-identical when cap == 0.03.
	terminalGrowth := historicalCAGR / 2
	maxTerminalGrowth := p.TerminalGrowthCap // was: 0.03

	if terminalGrowth > maxTerminalGrowth {
		terminalGrowth = maxTerminalGrowth
	}

	if terminalGrowth <= 0 {
		terminalGrowth = DefaultTerminalGrowthFloor // was: 0.02
	}

	// Ensure terminal growth stays at least DefaultTerminalWACCSpread below WACC.
	if computedWACC > 0 && terminalGrowth > computedWACC-DefaultTerminalWACCSpread {
		terminalGrowth = computedWACC - DefaultTerminalWACCSpread
		if terminalGrowth < DefaultTerminalGrowthDegenWACCFloor { // was: 0.01
			terminalGrowth = DefaultTerminalGrowthDegenWACCFloor
		}
	}

	p.TerminalGrowthRate = terminalGrowth
	p.Provenance[knobTerminalGrowthRate] = SourceDefault
	return nil
}

// validateStaging enforces the WACC-independent structural invariants shared by
// ResolveInputs and the pre-estimator pre-check (plan §3.7): min ≤ max growth, and
// stage-sum ≥ 1. Keeping this in ONE place ensures the pre-check and the resolver
// never drift. The horizon-vs-length invariant is NOT here — it needs the
// post-estimator growth-rate length and lives in ResolveInputs.
func validateStaging(minGrowth, maxGrowth float64, stage1, stage2, stage3 int) error {
	if minGrowth > maxGrowth {
		return &ParamError{
			Knob:   knobMinGrowthRate,
			Reason: "must be ≤ max_growth_rate",
			Value:  minGrowth,
			Limit:  maxGrowth,
		}
	}

	if stage1+stage2+stage3 < 1 {
		return &ParamError{
			Knob:   knobGrowthStages,
			Reason: "stage1_years + stage2_years + stage3_years must be ≥ 1",
			Value:  float64(stage1 + stage2 + stage3),
			Limit:  1,
		}
	}

	return nil
}

// ValidateEstimatorConfig is the cheap pre-estimator structural pre-check the
// service calls (when overrides are present) BEFORE building a per-request
// estimator, so the estimator never runs with an invalid staging config (plan
// §3.7 subtlety). It resolves the same min/max/stage knobs ResolveInputs does
// (applying the N1–N3 zero-sentinel fallbacks) and runs validateStaging. It does
// NOT touch WACC or horizon. ResolveInputs re-asserts the same invariants
// idempotently after the estimator runs.
func ValidateEstimatorConfig(d Defaults, o Overrides) error {
	maxGrowth := d.MaxGrowthRate
	if maxGrowth == 0 {
		maxGrowth = DefaultMaxGrowthRate
	}
	if o.MaxGrowthRate != nil {
		maxGrowth = *o.MaxGrowthRate
	}

	minGrowth := d.MinGrowthRate
	if minGrowth == 0 {
		minGrowth = DefaultMinGrowthRate
	}
	if o.MinGrowthRate != nil {
		minGrowth = *o.MinGrowthRate
	}

	stage1 := d.Stage1Years
	if stage1 == 0 {
		stage1 = DefaultStage1Years
	}
	if o.Stage1Years != nil {
		stage1 = *o.Stage1Years
	}

	stage2 := d.Stage2Years
	if stage2 == 0 {
		stage2 = DefaultStage2Years
	}
	if o.Stage2Years != nil {
		stage2 = *o.Stage2Years
	}

	stage3 := d.Stage3Years
	if o.Stage3Years != nil {
		stage3 = *o.Stage3Years
	}

	return validateStaging(minGrowth, maxGrowth, stage1, stage2, stage3)
}
