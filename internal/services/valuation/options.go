package valuation

// ValuationOptions holds optional overrides for valuation parameters.
// Nil pointer fields indicate "use default from data sources".
type ValuationOptions struct {
	// OverrideBeta overrides the market-data beta used in WACC calculation.
	OverrideBeta *float64

	// OverrideRiskFree overrides the macro-data risk-free rate used in WACC calculation.
	OverrideRiskFree *float64
}
