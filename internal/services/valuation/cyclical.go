package valuation

import "github.com/midas/dcf-valuation-api/internal/core/entities"

// normalizeCyclicalBaseOI implements VAL-1 Phase 3 cyclical-base normalization.
// For a cyclical archetype, a trough-year base operating income makes the
// projected rebound look aggressive, so the base is floored at the mean
// effective operating income over the most recent FY periods:
//
//	normalizedOI = max(baseOI, mean(effectiveOI over up to 3 most recent FY periods))
//
// The 3-year window is FY-only (via GetRecentYears) so a single quarter is
// never blended into the mean, and the function is pure + clock-free.
//
// The returned method is "3y_mean" when the FY mean was the binding floor
// (the base was raised), else "latest". When fewer than 2 FY periods are
// available the mean is not meaningful, so the function is a conservative
// no-op returning (baseOI, "latest").
//
// Per-period effective OI mirrors effectiveOI's precedence
// (NormalizedOperatingIncome when >0, else OperatingIncome) so the floor is
// comparable to baseOI. hist is never mutated.
func normalizeCyclicalBaseOI(baseOI float64, hist *entities.HistoricalFinancialData) (float64, string) {
	if hist == nil {
		return baseOI, "latest"
	}

	fyPeriods := hist.GetRecentYears(3)
	if len(fyPeriods) < 2 {
		// A single FY period yields max(latest, latest)==latest anyway; the
		// explicit guard keeps the reported method honest.
		return baseOI, "latest"
	}

	var sum float64
	for _, fd := range fyPeriods {
		sum += effectiveOIOfPeriod(fd)
	}
	mean := sum / float64(len(fyPeriods))

	if mean > baseOI {
		return mean, "3y_mean"
	}
	return baseOI, "latest"
}

// effectiveOIOfPeriod returns a single FY period's effective operating income,
// mirroring effectiveOI's NormalizedOperatingIncome-over-OperatingIncome
// precedence but reading the raw entity (GetRecentYears returns *FinancialData).
func effectiveOIOfPeriod(fd *entities.FinancialData) float64 {
	if fd.NormalizedOperatingIncome > 0 {
		return fd.NormalizedOperatingIncome
	}
	return fd.OperatingIncome
}
