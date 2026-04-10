package wacc

// Beta adjustment functions for more accurate cost of equity estimation.
// These are pure math functions with no config dependencies.

// BlumeAdjustedBeta applies Blume's mean-reversion adjustment toward the market
// beta of 1.0. Historical betas tend to revert to the market mean over time,
// so this reduces estimation error for extreme beta values.
//
// Formula: adjusted = 0.67 * rawBeta + 0.33 * 1.0
//
// Reference: Blume, Marshall E. "Betas and Their Regression Tendencies" (1975).
func BlumeAdjustedBeta(rawBeta float64) float64 {
	return 0.67*rawBeta + 0.33*1.0
}

// UnleveredBeta removes the effect of financial leverage from the observed beta,
// isolating the business risk (asset beta) from capital structure risk.
//
// Formula (Hamada): unlevered = levered / (1 + (1 - taxRate) * debtEquityRatio)
//
// Parameters:
//   - leveredBeta: observed equity beta (includes leverage effect)
//   - taxRate: corporate tax rate (0 to 1)
//   - debtEquityRatio: total debt / equity market value
func UnleveredBeta(leveredBeta, taxRate, debtEquityRatio float64) float64 {
	return leveredBeta / (1 + (1-taxRate)*debtEquityRatio)
}

// RelleveredBeta applies a target capital structure to an unlevered (asset) beta,
// producing the beta that reflects the target leverage ratio.
//
// Formula (Hamada): relevered = unlevered * (1 + (1 - taxRate) * targetDebtEquityRatio)
//
// Parameters:
//   - unleveredBeta: asset beta without leverage effect
//   - taxRate: corporate tax rate (0 to 1)
//   - targetDebtEquityRatio: target debt / equity ratio
func RelleveredBeta(unleveredBeta, taxRate, targetDebtEquityRatio float64) float64 {
	return unleveredBeta * (1 + (1-taxRate)*targetDebtEquityRatio)
}
