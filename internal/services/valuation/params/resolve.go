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
	// Stage-year knobs echo the NESTED wire path the request used
	// (ValuationOverrides.growth_stages.stageN_years) so applied_overrides mirrors
	// the request shape and the Layer-1 validation messages. MEDIUM-2: previously
	// flat (stageN_years), which did not match the request's nested field.
	knobStage1Years       = "growth_stages.stage1_years"
	knobStage2Years       = "growth_stages.stage2_years"
	knobStage3Years       = "growth_stages.stage3_years"
	knobMaxGrowthRate     = "max_growth_rate"
	knobMinGrowthRate     = "min_growth_rate"
	knobTerminalMethod    = "terminal_method"
	knobTerminalMultiple  = "terminal_multiple"
	knobTaxRate           = "tax_rate"
	knobBeta              = "beta"
	knobRiskFreeRate      = "risk_free_rate"
	knobMarketRiskPremium = "market_risk_premium"
	knobGrowthStages      = "growth_stages"

	// knobWACC names the computed WACC in a ParamError when the resolved CAPM/WACC
	// inputs drive the cost of capital to a non-positive value (e.g. an extreme
	// negative-beta + high-MRP + zero-rf combo). WACC is not itself an override knob —
	// it is derived from beta/risk_free_rate/market_risk_premium/tax_rate — so this
	// is the most actionable label to surface ("your inputs produced WACC ≤ 0").
	knobWACC = "wacc"
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
		p.Provenance[knobMarketRiskPremium] = SourceRequest
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
				Knob:     knobHorizonYears,
				Reason:   "must be ≤ stage1_years + stage2_years + stage3_years",
				Value:    float64(p.HorizonYears),
				Limit:    float64(stageSum),
				HasLimit: true,
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
				Knob:     knobHorizonYears,
				Reason:   "must be ≤ the number of projected growth rates",
				Value:    float64(p.HorizonYears),
				Limit:    float64(growthRateLen),
				HasLimit: true,
			}
		}
		// Profile/default-sourced: clamp + warn (legacy service.go:1117 behavior).
		p.Warnings = append(p.Warnings, fmt.Sprintf(
			"horizon_years (%d) exceeds available growth rates (%d); clamped to %d",
			p.HorizonYears, growthRateLen, growthRateLen))
		p.HorizonYears = growthRateLen
	}

	// Engine-horizon ceiling (DYNAMIC engine constraint). The effective horizon must
	// not exceed MaxDCFProjectionYears (== dcf.validateInputs' ProjectionYears > 50
	// rail). Even with horizon_years OMITTED, request-sourced growth_stages can drive
	// the default-sourced horizon (== growthRateLen == stage-sum) above the ceiling —
	// dcf.validateInputs would then reject it with an UNTYPED error → HTTP 500. We are
	// the COMPLETE gatekeeper, so attribute the excess to the request and 422 here:
	//   - horizon_years request-sourced → knob horizon_years
	//   - else any stage knob request-sourced → knob growth_stages (the request input
	//     that grew the horizon past the rail)
	// A purely profile/default-sourced horizon over the ceiling is clamped + warned
	// (the default path never produces > 50, so this clamp never fires there).
	if p.HorizonYears > MaxDCFProjectionYears {
		stageRequestSourced := p.Provenance[knobStage1Years] == SourceRequest ||
			p.Provenance[knobStage2Years] == SourceRequest ||
			p.Provenance[knobStage3Years] == SourceRequest
		switch {
		case horizonRequestSourced:
			return EffectiveValuationParams{}, &ParamError{
				Knob:     knobHorizonYears,
				Reason:   fmt.Sprintf("must be ≤ %d (the maximum DCF projection horizon)", MaxDCFProjectionYears),
				Value:    float64(p.HorizonYears),
				Limit:    float64(MaxDCFProjectionYears),
				HasLimit: true,
			}
		case stageRequestSourced:
			return EffectiveValuationParams{}, &ParamError{
				Knob:     knobGrowthStages,
				Reason:   fmt.Sprintf("stage1_years + stage2_years + stage3_years drive the horizon above the maximum DCF projection horizon (%d)", MaxDCFProjectionYears),
				Value:    float64(p.HorizonYears),
				Limit:    float64(MaxDCFProjectionYears),
				HasLimit: true,
			}
		default:
			// Profile/default-sourced: clamp + warn. Unreachable on the default path.
			p.Warnings = append(p.Warnings, fmt.Sprintf(
				"horizon_years (%d) exceeds the maximum DCF projection horizon (%d); clamped to %d",
				p.HorizonYears, MaxDCFProjectionYears, MaxDCFProjectionYears))
			p.HorizonYears = MaxDCFProjectionYears
		}
	}

	return p, nil
}

// ResolveTerminal performs phase 2: it finalizes the terminal growth rate against
// the COMPUTED WACC, mutating p in place.
//
//   - Explicit path (p.TerminalGrowthExplicit): use p.TerminalGrowthRate as-is;
//     assert it stays at least MinTerminalWACCSpread (1%) below computedWACC (else a
//     typed 422). A value WITHIN that spread of WACC (including ≥ WACC) is a HARD
//     error, NOT an advisory — the resolver is the complete gatekeeper for this
//     DYNAMIC constraint so the engine's denominator guard never fires from the
//     override path. The cap is NOT applied on the explicit path (design §5 / §8 R8).
//   - Auto-derive path: a FAITHFUL, VERBATIM port of
//     service.go::calculateTerminalGrowthRate, using p.TerminalGrowthCap as the cap,
//     DefaultTerminalGrowthFloor as the ≤0 floor, DefaultTerminalWACCSpread as the
//     spread, and DefaultTerminalGrowthDegenWACCFloor as the post-spread degenerate
//     floor. Byte-identical to the legacy function when p.TerminalGrowthCap == 0.03.
//     A final spread gate (HIGH-1) upgrades the rare low-WACC (0 < WACC < 0.02) case —
//     where even the degenerate floor leaves (WACC − g) < MinTerminalWACCSpread — into
//     a typed 422 rather than letting it 500 in the engine. Unreachable on the default
//     path (real-ticker WACC ≥ 0.02), so byte-identity is preserved.
//
// Provenance for terminal_growth_rate is set to SourceDefault on the auto-derive
// path (it was set to SourceRequest in ResolveInputs on the explicit path).
func ResolveTerminal(p *EffectiveValuationParams, computedWACC, historicalCAGR float64) error {
	if p.Provenance == nil {
		p.Provenance = make(map[string]Source)
	}

	// Non-positive WACC guard (Layer-2, post-WACC). An extreme CAPM-input combo
	// (e.g. a large negative beta with a high MRP and zero/negative risk-free rate)
	// can drive the resolved cost of capital to ≤ 0, which makes the DCF discount
	// factors and the Gordon denominator undefined. The engine's dcf/wacc
	// validateInputs would reject this with an UNTYPED error → HTTP 500; we catch it
	// HERE as a typed *ParamError → 422 so the caller gets an actionable message.
	// computedWACC == 0 also trips this (a zero discount rate is degenerate).
	if computedWACC <= 0 {
		return &ParamError{
			Knob:   knobWACC,
			Reason: "resolved cost of capital is non-positive; check beta / market_risk_premium / risk_free_rate overrides",
			Value:  computedWACC,
		}
	}

	if p.TerminalGrowthExplicit {
		// Hard invariant: an explicit terminal growth must stay at least
		// MinTerminalWACCSpread below WACC to keep the Gordon perpetuity denominator
		// (WACC − g) numerically stable. This GENERALIZES the older "must be strictly
		// < WACC" check (the >= WACC case is the subset where the gap is ≤ 0) and uses
		// the SAME spread the DCF engine's denominator guard (dcf.MinWACCTerminalSpread)
		// enforces, so the resolver and engine agree. Catching it here means the engine
		// guard never fires from the override path; a 500 there would now indicate a
		// real internal bug. Applies regardless of cap (design §5 / §8 R8).
		if p.TerminalGrowthRate > computedWACC-MinTerminalWACCSpread {
			return &ParamError{
				Knob:     knobTerminalGrowthRate,
				Reason:   fmt.Sprintf("must be at least %g below WACC", MinTerminalWACCSpread),
				Value:    p.TerminalGrowthRate,
				Limit:    computedWACC - MinTerminalWACCSpread,
				HasLimit: true,
			}
		}
		return nil
	}

	// --- Auto-derive path: port of calculateTerminalGrowthRate (service.go) ---
	// The legacy order was: cap → ≤0 floor → WACC-spread clamp. MEDIUM-1 fix: apply
	// the FLOOR first, then the CAP, then re-enforce the CAP after the WACC-spread
	// adjustment. This makes an explicit low/negative terminal_growth_cap actually
	// bind (the old order let the 0.02 ≤0-floor overshoot a cap of e.g. 0.01).
	//
	// BYTE-IDENTITY for the default cap (0.03): before the WACC-spread step, swapping
	// floor↔cap leaves the result unchanged for cap=0.03 (the cap branch only fires
	// when terminalGrowth > 0, where the ≤0 floor is a no-op; the floor branch only
	// fires when terminalGrowth ≤ 0, where 0.02 < 0.03 so the cap is a no-op). The
	// WACC-spread clamp only ever LOWERS terminalGrowth (to wacc−spread ≤ cap), and
	// the degenerate floor raises it to at most 0.01 — both ≤ 0.03 — so the trailing
	// re-cap is a no-op at cap=0.03. Pinned by the byte-identity terminal test grid.
	terminalGrowth := historicalCAGR / 2
	growthCap := p.TerminalGrowthCap // was the literal 0.03 (named to avoid the builtin cap)

	// Floor first: a ≤0 auto-derived rate inflates to the floor ("viable businesses
	// grow at least with prices").
	if terminalGrowth <= 0 {
		terminalGrowth = DefaultTerminalGrowthFloor // 0.02
	}

	// Cap second: honor an explicit (possibly low/negative) cap over the floor.
	if terminalGrowth > growthCap {
		terminalGrowth = growthCap
	}

	// Ensure terminal growth stays at least DefaultTerminalWACCSpread below WACC.
	if computedWACC > 0 && terminalGrowth > computedWACC-DefaultTerminalWACCSpread {
		terminalGrowth = computedWACC - DefaultTerminalWACCSpread
		if terminalGrowth < DefaultTerminalGrowthDegenWACCFloor { // 0.01
			terminalGrowth = DefaultTerminalGrowthDegenWACCFloor
		}
	}

	// Re-enforce the cap: the WACC-spread/degenerate-floor adjustment above can raise
	// terminalGrowth back above an explicit low/negative cap (e.g. growthCap = -0.01
	// with the 0.01 degenerate floor). The cap is a hard ceiling, so clamp once more.
	// No-op for the default cap (0.03), preserving byte-identity.
	if terminalGrowth > growthCap {
		terminalGrowth = growthCap
	}

	// Final DYNAMIC-constraint gate (HIGH-1): in a low-WACC regime (0 < WACC < 0.02)
	// the degenerate floor (0.01) can leave terminalGrowth too close to — or above —
	// WACC, so (WACC − terminalGrowth) < MinTerminalWACCSpread. dcf.validateInputs
	// would then reject it with an UNTYPED error → HTTP 500. We are the COMPLETE
	// gatekeeper for this constraint, so generalize the explicit-path spread 422 to
	// the auto-derive path and surface a typed *ParamError → 422 instead. The
	// default path never produces WACC < 0.02 for real tickers, so this is unreachable
	// on the default path (pinned by the byte-identity terminal grid). The earlier
	// computedWACC ≤ 0 guard handles the non-positive case; this covers (0, 0.02).
	if computedWACC > 0 && terminalGrowth > computedWACC-MinTerminalWACCSpread {
		return &ParamError{
			Knob:     knobTerminalGrowthRate,
			Reason:   "auto-derived terminal growth cannot stay at least 1% below WACC at this WACC; set terminal_growth_rate or adjust the CAPM inputs",
			Value:    terminalGrowth,
			Limit:    computedWACC - MinTerminalWACCSpread,
			HasLimit: true,
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
			Knob:     knobMinGrowthRate,
			Reason:   "must be ≤ max_growth_rate",
			Value:    minGrowth,
			Limit:    maxGrowth,
			HasLimit: true,
		}
	}

	if stage1+stage2+stage3 < 1 {
		return &ParamError{
			Knob:     knobGrowthStages,
			Reason:   "stage1_years + stage2_years + stage3_years must be ≥ 1",
			Value:    float64(stage1 + stage2 + stage3),
			Limit:    1,
			HasLimit: true,
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
