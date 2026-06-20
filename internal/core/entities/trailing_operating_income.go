package entities

// Source identifiers returned by TrailingTwelveMonthsOperatingIncome. The
// string values mirror the revenue helper's vocabulary and are part of this
// helper's public contract — replay tooling and downstream dashboards key off
// them, so do not rename without coordinating with consumers
// (see docs/bugs/BUG-015-dcf-quarterly-operating-income-base.md).
// Aliases of the unified ttmSource* set in trailing_ttm.go (SR-1 A6).
const (
	oiSourceTTM4Q             = ttmSourceTTM4Q
	oiSourceTTMPriorBridge    = ttmSourceTTMPriorBridge
	oiSourceAnnualFY          = ttmSourceAnnualFY
	oiSourceAnnualizedQuarter = ttmSourceAnnualizedQuarter
	oiSourceInsufficient      = ttmSourceInsufficient
)

// periodEffectiveOI returns the per-period operating-income metric the DCF base
// uses. It mirrors service.go::effectiveOI exactly: NormalizedOperatingIncome
// when positive, else the raw OperatingIncome. Keeping the two in lockstep is
// load-bearing — the TTM helper MUST sum the same metric the engine reads for
// an FY-latest period so FY-latest tickers stay bit-for-bit (BUG-015 §4).
// Nil-safe (returns 0), per the trailing_ttm.go metric contract.
func periodEffectiveOI(d *FinancialData) float64 {
	if d == nil {
		return 0
	}
	if d.NormalizedOperatingIncome > 0 {
		return d.NormalizedOperatingIncome
	}
	return d.OperatingIncome
}

// oiTTMSpec parameterizes the shared trailing-twelve-months chain
// (trailing_ttm.go) for the effective-operating-income metric. SR-1 A6
// unified the previously duplicated revenue/OI chains; fallback order, source
// identifiers and warning strings are byte-identical to the pre-unification
// behavior (pinned by trailing_operating_income_test.go, assertions
// unchanged).
var oiTTMSpec = ttmSpec{
	metric:           periodEffectiveOI,
	labelPrefix:      "operating_income_base",
	insufficientNoun: "operating-income",
	annualizedNoun:   "single-quarter operating income",
}

// TrailingTwelveMonthsOperatingIncome returns a best-effort trailing-twelve-
// months (TTM) operating-income figure for use as the standard-DCF base
// (BUG-015). It walks the same documented fallback chain as
// TrailingTwelveMonthsRevenue (see trailing_ttm.go for the chain contract):
//
//	TTM_PRIOR_BRIDGE → TTM_4Q → ANNUAL_FY → ANNUALIZED_QUARTER → INSUFFICIENT_HISTORY
//
// For an FY-latest ticker the DCF base is the FY value UNCHANGED (no
// annualization), preserving FY-latest bit-for-bit. The summed metric is
// periodEffectiveOI (NormalizedOperatingIncome with a raw-OperatingIncome
// fallback), matching service.go::effectiveOI per period. The signature is
// stable: replay tooling pattern-matches `source` strings — adding a source
// requires a spec update; renaming one is a breaking change.
func (h *HistoricalFinancialData) TrailingTwelveMonthsOperatingIncome() (oi float64, source string, warning string) {
	return h.trailingTwelveMonths(oiTTMSpec)
}
