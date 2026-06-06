package entities

import (
	"fmt"
	"sort"
	"time"
)

// Source identifiers returned by TrailingTwelveMonthsOperatingIncome. The
// string values mirror the revenue helper's vocabulary (TrailingTwelveMonthsRevenue)
// and are part of this helper's public contract — replay tooling and downstream
// dashboards key off them, so do not rename without coordinating with consumers
// (see docs/bugs/BUG-015-dcf-quarterly-operating-income-base.md).
const (
	oiSourceTTM4Q             = "TTM_4Q"
	oiSourceTTMPriorBridge    = "TTM_PRIOR_BRIDGE"
	oiSourceAnnualFY          = "ANNUAL_FY"
	oiSourceAnnualizedQuarter = "ANNUALIZED_QUARTER"
	oiSourceInsufficient      = "INSUFFICIENT_HISTORY"
)

// periodEffectiveOI returns the per-period operating-income metric the DCF base
// uses. It mirrors service.go::effectiveOI exactly: NormalizedOperatingIncome
// when positive, else the raw OperatingIncome. Keeping the two in lockstep is
// load-bearing — the TTM helper MUST sum the same metric the engine reads for
// an FY-latest period so FY-latest tickers stay bit-for-bit (BUG-015 §4).
func periodEffectiveOI(d *FinancialData) float64 {
	if d == nil {
		return 0
	}
	if d.NormalizedOperatingIncome > 0 {
		return d.NormalizedOperatingIncome
	}
	return d.OperatingIncome
}

// TrailingTwelveMonthsOperatingIncome returns a best-effort trailing-twelve-
// months (TTM) operating-income figure for use as the standard-DCF base
// (BUG-015). It walks the same documented fallback chain as
// TrailingTwelveMonthsRevenue so callers can take the result at face value for
// the headline number while still inspecting `source` and `warning` for
// audit / replay purposes.
//
// Fallback chain (applied in order):
//
//  1. TTM_PRIOR_BRIDGE    — partial-year case (latest year has 1-3 quarters,
//     prior year supplies the missing quarters at the same calendar position).
//     Runs FIRST so the partial-year shape is preserved in the source string.
//  2. TTM_4Q              — sum of the 4 most recent contiguous quarters,
//     possibly crossing a fiscal-year boundary. Gold-standard for full-year
//     shapes; warning is empty.
//  3. ANNUAL_FY           — most recent fiscal-year filing when no usable
//     quarterly window exists. For an FY-latest ticker the DCF base is the FY
//     value UNCHANGED (no annualization), preserving FY-latest bit-for-bit.
//  4. ANNUALIZED_QUARTER  — naive scaling of the available quarters
//     (4*Q1, 2*(Q1+Q2), or (4/3)*(Q1+Q2+Q3)). Lossy: ignores seasonality.
//     Warning emitted.
//  5. INSUFFICIENT_HISTORY — no operating income at all; returns (0, …) and
//     lets the caller decide whether to fail.
//
// The summed metric is periodEffectiveOI (NormalizedOperatingIncome with a
// raw-OperatingIncome fallback), matching service.go::effectiveOI per period.
//
// The signature is stable: replay tooling pattern-matches `source` strings.
// Adding a new source string requires updating the spec and consumers;
// renaming an existing one is a breaking change.
func (h *HistoricalFinancialData) TrailingTwelveMonthsOperatingIncome() (oi float64, source string, warning string) {
	if h == nil || len(h.Data) == 0 {
		return 0, oiSourceInsufficient, "operating_income_base: insufficient operating-income history"
	}

	// 1) TTM_PRIOR_BRIDGE — partial-year + prior-year corresponding quarters.
	if v, ok := h.ttmPriorBridgeOperatingIncome(); ok {
		return v, oiSourceTTMPriorBridge,
			"operating_income_base: partial-year TTM bridged with prior-year quarters (handles seasonality)"
	}

	// 2) TTM_4Q — four most recent contiguous quarters, summed.
	if v, ok := h.ttmFourQuartersOperatingIncome(); ok {
		return v, oiSourceTTM4Q, ""
	}

	// 3) ANNUAL_FY — fall back to the latest fiscal-year filing. For an FY-latest
	// ticker this is the same effective OI the engine read pre-fix, returned
	// unchanged (no annualization).
	if v, period, date, ok := h.latestAnnualOperatingIncome(); ok {
		if len(h.GetQuarterlyPeriods()) == 0 {
			return v, oiSourceAnnualFY, ""
		}
		return v, oiSourceAnnualFY,
			fmt.Sprintf("operating_income_base: TTM unavailable, used latest FY ($%.0f dated %s)",
				v, date.Format("2006-01-02")) + fmt.Sprintf(" [%s]", period)
	}

	// 4) ANNUALIZED_QUARTER — scale up 1-3 available quarters of the latest year.
	if v, ok := h.annualizedQuarterOperatingIncome(); ok {
		return v, oiSourceAnnualizedQuarter,
			"operating_income_base: annualized single-quarter operating income (4× extrapolation, ignores seasonality)"
	}

	// 5) INSUFFICIENT_HISTORY — no usable operating income at all.
	return 0, oiSourceInsufficient, "operating_income_base: insufficient operating-income history"
}

// ttmFourQuartersOperatingIncome returns the sum of the 4 most recent quarters
// when they form a contiguous span (no gaps, distinct quarters). Quarters may
// cross a fiscal-year boundary. Returns (0, false) when the contiguity check
// fails or any quarter has non-positive effective OI (a zero/negative quarter
// would silently shrink the TTM, which would re-introduce the BUG-015 base
// understatement).
func (h *HistoricalFinancialData) ttmFourQuartersOperatingIncome() (float64, bool) {
	quarterly := h.GetQuarterlyPeriods()
	if len(quarterly) < 4 {
		return 0, false
	}

	periods := make([]string, 0, len(quarterly))
	for p := range quarterly {
		periods = append(periods, p)
	}
	sort.Slice(periods, func(i, j int) bool {
		yi, si := parsePeriodKey(periods[i])
		yj, sj := parsePeriodKey(periods[j])
		if yi != yj {
			return yi < yj
		}
		return si < sj
	})

	last4 := periods[len(periods)-4:]
	if !quartersAreContiguous(last4) {
		return 0, false
	}

	var sum float64
	for _, p := range last4 {
		v := periodEffectiveOI(quarterly[p])
		if v <= 0 {
			return 0, false
		}
		sum += v
	}
	return sum, true
}

// ttmPriorBridgeOperatingIncome handles the partial-year case: the latest year
// has 1-3 quarters and the prior year supplies the (4-N) missing quarters at
// the same calendar position. Mirrors ttmPriorBridgeRevenue.
func (h *HistoricalFinancialData) ttmPriorBridgeOperatingIncome() (float64, bool) {
	quarterly := h.GetQuarterlyPeriods()
	if len(quarterly) == 0 {
		return 0, false
	}

	latestYear := 0
	for p := range quarterly {
		y, s := parsePeriodKey(p)
		if s < 1 || s > 4 {
			continue
		}
		if y > latestYear {
			latestYear = y
		}
	}
	if latestYear == 0 {
		return 0, false
	}

	type qEntry struct {
		sub  int
		data *FinancialData
	}
	var current []qEntry
	for p, d := range quarterly {
		y, s := parsePeriodKey(p)
		if y == latestYear && s >= 1 && s <= 4 {
			current = append(current, qEntry{sub: s, data: d})
		}
	}
	sort.Slice(current, func(i, j int) bool { return current[i].sub < current[j].sub })

	// Bridge only applies when the latest year has 1-3 quarters forming a
	// contiguous Q1-start run.
	if len(current) < 1 || len(current) > 3 {
		return 0, false
	}
	for i, q := range current {
		if q.sub != i+1 {
			return 0, false
		}
		if periodEffectiveOI(q.data) <= 0 {
			return 0, false
		}
	}

	var sum float64
	for _, q := range current {
		sum += periodEffectiveOI(q.data)
	}
	priorYear := latestYear - 1
	for sub := len(current) + 1; sub <= 4; sub++ {
		key := fmt.Sprintf("%dQ%d", priorYear, sub)
		d, ok := quarterly[key]
		if !ok {
			return 0, false
		}
		v := periodEffectiveOI(d)
		if v <= 0 {
			return 0, false
		}
		sum += v
	}
	return sum, true
}

// latestAnnualOperatingIncome returns the most recent fiscal-year filing's
// effective OI, the period key, and the filing date. Mirrors
// latestAnnualRevenue.
func (h *HistoricalFinancialData) latestAnnualOperatingIncome() (float64, string, time.Time, bool) {
	annual := h.GetAnnualPeriods()
	if len(annual) == 0 {
		return 0, "", time.Time{}, false
	}

	periods := make([]string, 0, len(annual))
	for p := range annual {
		periods = append(periods, p)
	}
	sort.Slice(periods, func(i, j int) bool {
		yi, si := parsePeriodKey(periods[i])
		yj, sj := parsePeriodKey(periods[j])
		if yi != yj {
			return yi < yj
		}
		return si < sj
	})

	for i := len(periods) - 1; i >= 0; i-- {
		d := annual[periods[i]]
		v := periodEffectiveOI(d)
		if v <= 0 {
			continue
		}
		return v, periods[i], d.FilingDate, true
	}
	return 0, "", time.Time{}, false
}

// annualizedQuarterOperatingIncome is the lossy third-tier fallback. It scales
// the available 1-3 contiguous quarters of the latest year up to a synthetic 12
// months: 4*Q1, 2*(Q1+Q2), or (4/3)*(Q1+Q2+Q3). Ignores seasonality — callers
// MUST surface the warning string emitted by the parent helper. Mirrors
// annualizedQuarterRevenue.
func (h *HistoricalFinancialData) annualizedQuarterOperatingIncome() (float64, bool) {
	quarterly := h.GetQuarterlyPeriods()
	if len(quarterly) == 0 {
		return 0, false
	}

	latestYear := 0
	for p := range quarterly {
		y, s := parsePeriodKey(p)
		if s < 1 || s > 4 {
			continue
		}
		if y > latestYear {
			latestYear = y
		}
	}
	if latestYear == 0 {
		return 0, false
	}

	var sum float64
	count := 0
	for sub := 1; sub <= 4; sub++ {
		key := fmt.Sprintf("%dQ%d", latestYear, sub)
		d, ok := quarterly[key]
		if !ok {
			break
		}
		v := periodEffectiveOI(d)
		if v <= 0 {
			break
		}
		sum += v
		count++
	}

	// No Q1 in the latest year → 4× the latest single positive quarter.
	if count == 0 {
		var latestSub int
		var latestData *FinancialData
		for p, d := range quarterly {
			y, s := parsePeriodKey(p)
			if y != latestYear || s < 1 || s > 4 || periodEffectiveOI(d) <= 0 {
				continue
			}
			if s > latestSub {
				latestSub = s
				latestData = d
			}
		}
		if latestData == nil {
			return 0, false
		}
		return periodEffectiveOI(latestData) * 4, true
	}

	switch count {
	case 1:
		return sum * 4, true
	case 2:
		return sum * 2, true
	case 3:
		return sum * 4.0 / 3.0, true
	default:
		// count == 4 should have been picked up by ttmFourQuartersOperatingIncome.
		return sum, true
	}
}
