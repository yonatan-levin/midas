package handlers

// fair_value_validation.go — Layer-1 static range + enum validation for the
// ValuationOverrides DTO (T7).
//
// These are "fat-finger rails": cheap, per-knob checks that catch unit errors
// (e.g., supplying 50 instead of 0.50) and enum typos before any valuation
// work is done.  They are intentionally wide so legitimate economic values are
// never rejected — negatives are allowed on every knob where §3/D6 of the
// design spec says they are real (terminal growth, growth rates, beta, tax,
// risk-free).  MRP is floored at 0 because a negative equity risk premium is
// economically unsound, not just unusual.
//
// Cross-knob invariants (terminal < WACC, min ≤ max, horizon ≤ stage-sum,
// exit_multiple resolvable) live in the resolver (Layer 2, T8) and are NOT
// checked here.
//
// The format of the returned *ErrorResponse follows the §4.3 shape that T6
// already uses for INVALID_OVERRIDE errors:
//
//	detail: "<field> (<value>) out of range [<min>, <max>]"
//	code:   "INVALID_OVERRIDE"
//	context.knob: "<field>"

import (
	"fmt"
)

// ── Range sentinels (transcribed verbatim from design §5) ─────────────────
//
// These constants exist so the bounds are in one place and tests can cross-
// check them directly rather than duplicating magic numbers.

const (
	// Terminal growth rate (and cap) bounds: real-terms contraction → modest growth
	terminalGrowthRateMin = -0.20
	terminalGrowthRateMax = 0.50

	// Horizon years: at least one year, no more than 50
	horizonYearsMin = 1
	horizonYearsMax = 50

	// Per-stage years: a stage can be zeroed out (skipped) or run up to 50 years
	stageYearsMin = 0
	stageYearsMax = 50

	// Growth rate bounds: floors at -1.0 (100% contraction) so that the revenue
	// base can shrink to zero but not go negative; ceil at 10× (1000% CAGR) to
	// catch obvious unit errors while permitting high-growth scenarios
	growthRateMin = -1.0
	growthRateMax = 10.0

	// Terminal multiple: EV/EBITDA-class multiples; floor 0 (theoretical), cap 100
	terminalMultipleMin = 0.0
	terminalMultipleMax = 100.0

	// Tax rate: negative effective rates are real (NOLs, credits); above 1.0 is
	// always a unit error
	taxRateMin = -0.5
	taxRateMax = 1.0

	// Beta: negative beta is real (gold, inverse-ETF proxies); beyond ±5 is noise
	betaMin = -5.0
	betaMax = 5.0

	// Risk-free rate: negative nominal rates have occurred (EUR/JPY/CHF) and must
	// not be rejected; 25% is a generous upper rail
	riskFreeRateMin = -0.05
	riskFreeRateMax = 0.25

	// Market risk premium: must be ≥ 0 (a negative ERP is nonsensical);
	// 30% is the upper rail (well above any observed historical premium)
	marketRiskPremiumMin = 0.0
	marketRiskPremiumMax = 0.30
)

// terminalMethodValues is the exhaustive set of allowed terminal_method values.
var terminalMethodValues = map[string]struct{}{
	"gordon_growth": {},
	"exit_multiple": {},
}

// validateOverrides performs Layer-1 static range and enum checks on the
// ValuationOverrides DTO.  It returns nil when the overrides are valid, or a
// pointer to an ErrorResponse (HTTP 422, code INVALID_OVERRIDE) on the first
// failing knob.
//
// Only SET knobs (non-nil pointers) are checked — nil means "use the default"
// and is always valid here.  Cross-knob invariants are NOT checked here.
func validateOverrides(o *ValuationOverrides) *ErrorResponse {
	if o == nil {
		return nil
	}

	// ── Float-range knobs ─────────────────────────────────────────────────

	if o.TerminalGrowthRate != nil {
		v := *o.TerminalGrowthRate
		if v < terminalGrowthRateMin || v > terminalGrowthRateMax {
			return overrideRangeError("terminal_growth_rate", v,
				terminalGrowthRateMin, terminalGrowthRateMax)
		}
	}

	if o.TerminalGrowthCap != nil {
		v := *o.TerminalGrowthCap
		if v < terminalGrowthRateMin || v > terminalGrowthRateMax {
			return overrideRangeError("terminal_growth_cap", v,
				terminalGrowthRateMin, terminalGrowthRateMax)
		}
	}

	if o.MaxGrowthRate != nil {
		v := *o.MaxGrowthRate
		if v < growthRateMin || v > growthRateMax {
			return overrideRangeError("max_growth_rate", v,
				growthRateMin, growthRateMax)
		}
	}

	if o.MinGrowthRate != nil {
		v := *o.MinGrowthRate
		if v < growthRateMin || v > growthRateMax {
			return overrideRangeError("min_growth_rate", v,
				growthRateMin, growthRateMax)
		}
	}

	if o.TerminalMultiple != nil {
		v := *o.TerminalMultiple
		if v < terminalMultipleMin || v > terminalMultipleMax {
			return overrideRangeError("terminal_multiple", v,
				terminalMultipleMin, terminalMultipleMax)
		}
	}

	if o.TaxRate != nil {
		v := *o.TaxRate
		if v < taxRateMin || v > taxRateMax {
			return overrideRangeError("tax_rate", v, taxRateMin, taxRateMax)
		}
	}

	if o.Beta != nil {
		v := *o.Beta
		if v < betaMin || v > betaMax {
			return overrideRangeError("beta", v, betaMin, betaMax)
		}
	}

	if o.RiskFreeRate != nil {
		v := *o.RiskFreeRate
		if v < riskFreeRateMin || v > riskFreeRateMax {
			return overrideRangeError("risk_free_rate", v,
				riskFreeRateMin, riskFreeRateMax)
		}
	}

	if o.MarketRiskPremium != nil {
		v := *o.MarketRiskPremium
		if v < marketRiskPremiumMin || v > marketRiskPremiumMax {
			return overrideRangeError("market_risk_premium", v,
				marketRiskPremiumMin, marketRiskPremiumMax)
		}
	}

	// ── Integer-range knobs ──────────────────────────────────────────────

	if o.HorizonYears != nil {
		v := *o.HorizonYears
		if v < horizonYearsMin || v > horizonYearsMax {
			return overrideIntRangeError("horizon_years", v,
				horizonYearsMin, horizonYearsMax)
		}
	}

	if o.GrowthStages != nil {
		gs := o.GrowthStages
		if gs.Stage1Years != nil {
			v := *gs.Stage1Years
			if v < stageYearsMin || v > stageYearsMax {
				return overrideIntRangeError("growth_stages.stage1_years", v,
					stageYearsMin, stageYearsMax)
			}
		}
		if gs.Stage2Years != nil {
			v := *gs.Stage2Years
			if v < stageYearsMin || v > stageYearsMax {
				return overrideIntRangeError("growth_stages.stage2_years", v,
					stageYearsMin, stageYearsMax)
			}
		}
		if gs.Stage3Years != nil {
			v := *gs.Stage3Years
			if v < stageYearsMin || v > stageYearsMax {
				return overrideIntRangeError("growth_stages.stage3_years", v,
					stageYearsMin, stageYearsMax)
			}
		}
	}

	// ── Enum knobs ───────────────────────────────────────────────────────

	if o.TerminalMethod != nil {
		if _, ok := terminalMethodValues[*o.TerminalMethod]; !ok {
			return overrideEnumError("terminal_method", *o.TerminalMethod,
				[]string{"gordon_growth", "exit_multiple"})
		}
	}

	return nil
}

// ── Error builders ────────────────────────────────────────────────────────
//
// These helpers produce an *ErrorResponse (not a gin.Context write) so the
// caller (handler) can decide when and how to send it.  Decoupling the builder
// from the transport makes validateOverrides reusable in both the bulk handler
// (T7 wiring) and the future POST single-ticker handler (T9).

// overrideRangeError builds an INVALID_OVERRIDE 422 ErrorResponse for a
// float-range violation.  The detail field follows the canonical format:
// "<knob> (<value>) out of range [<min>, <max>]".
func overrideRangeError(knob string, value, min, max float64) *ErrorResponse {
	return &ErrorResponse{
		Type:   "https://problems.midas.dev/INVALID_OVERRIDE",
		Title:  "Invalid valuation override",
		Status: 422,
		Detail: fmt.Sprintf("%s (%.4g) out of range [%.4g, %.4g]", knob, value, min, max),
		Code:   "INVALID_OVERRIDE",
		Context: map[string]interface{}{
			"knob": knob,
		},
	}
}

// overrideIntRangeError builds an INVALID_OVERRIDE 422 ErrorResponse for an
// integer-range violation.
func overrideIntRangeError(knob string, value, min, max int) *ErrorResponse {
	return &ErrorResponse{
		Type:   "https://problems.midas.dev/INVALID_OVERRIDE",
		Title:  "Invalid valuation override",
		Status: 422,
		Detail: fmt.Sprintf("%s (%d) out of range [%d, %d]", knob, value, min, max),
		Code:   "INVALID_OVERRIDE",
		Context: map[string]interface{}{
			"knob": knob,
		},
	}
}

// overrideEnumError builds an INVALID_OVERRIDE 422 ErrorResponse for an
// enum-membership violation.
func overrideEnumError(knob, value string, allowed []string) *ErrorResponse {
	return &ErrorResponse{
		Type:   "https://problems.midas.dev/INVALID_OVERRIDE",
		Title:  "Invalid valuation override",
		Status: 422,
		Detail: fmt.Sprintf("%s (%q) is not a valid value; allowed: %v", knob, value, allowed),
		Code:   "INVALID_OVERRIDE",
		Context: map[string]interface{}{
			"knob": knob,
		},
	}
}
