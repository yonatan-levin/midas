package valuation

import "github.com/midas/dcf-valuation-api/internal/services/valuation/params"

// ValuationOptions holds optional overrides for valuation parameters.
// Nil pointer fields indicate "use default from data sources".
type ValuationOptions struct {
	// OverrideBeta overrides the market-data beta used in WACC calculation.
	// Deprecated: prefer Overrides.Beta; kept for back-compat with existing
	// callers (GET query params, legacy bulk fields). The service entry point
	// normalizes this into Overrides.Beta when Overrides.Beta is nil.
	OverrideBeta *float64

	// OverrideRiskFree overrides the macro-data risk-free rate used in WACC calculation.
	// Deprecated: prefer Overrides.RiskFreeRate; kept for back-compat with existing
	// callers. The service entry point normalizes this into Overrides.RiskFreeRate
	// when Overrides.RiskFreeRate is nil.
	OverrideRiskFree *float64

	// Overrides is the unified per-request override carrier consumed by the params
	// resolver (T4 wires the reads). When a request sets this, the resolver applies
	// the full precedence stack (config defaults < AssumptionProfile < request override)
	// across all valuation knobs. Any non-nil field here causes the cache to be
	// bypassed (read + write), matching the behavior of the legacy OverrideBeta /
	// OverrideRiskFree fields.
	//
	// Back-compat: the legacy OverrideBeta / OverrideRiskFree fields are normalized
	// into Overrides.Beta / Overrides.RiskFreeRate at the CalculateValuation entry
	// so T4 can read a single canonical source.
	Overrides params.Overrides
}

// hasAnyOverride reports whether opts carries any per-request override — either
// the legacy scalar pointers or any field in the unified Overrides carrier.
// Used by CalculateValuation to decide whether to bypass the cache.
func (o *ValuationOptions) hasAnyOverride() bool {
	if o == nil {
		return false
	}
	if o.OverrideBeta != nil || o.OverrideRiskFree != nil {
		return true
	}
	ov := o.Overrides
	// Any non-nil pointer field in Overrides means the request explicitly set a knob.
	return ov.Beta != nil ||
		ov.RiskFreeRate != nil ||
		ov.MarketRiskPremium != nil ||
		ov.TaxRate != nil ||
		ov.TerminalGrowthRate != nil ||
		ov.TerminalGrowthCap != nil ||
		ov.HorizonYears != nil ||
		ov.Stage1Years != nil ||
		ov.Stage2Years != nil ||
		ov.Stage3Years != nil ||
		ov.MaxGrowthRate != nil ||
		ov.MinGrowthRate != nil ||
		ov.TerminalMethod != nil ||
		ov.TerminalMultiple != nil
}
