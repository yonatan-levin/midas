package entities

import (
	"fmt"
	"sort"
	"time"
)

// This file is the SHARED trailing-twelve-months fallback chain behind both
// TrailingTwelveMonthsRevenue and TrailingTwelveMonthsOperatingIncome
// (SR-1 A6). Before the unification the two public helpers carried ~320
// duplicated lines of chain logic differing only in the per-period metric
// extractor and the label strings; the duplication had already required the
// BUG-015 fix to be hand-mirrored once.
//
// CONTRACT (unchanged by the unification — pinned by trailing_revenue_test.go
// and trailing_operating_income_test.go with assertions untouched):
//
//   - The source identifiers (TTM_PRIOR_BRIDGE → TTM_4Q → ANNUAL_FY →
//     ANNUALIZED_QUARTER → INSUFFICIENT_HISTORY) and every warning string are
//     PUBLIC CONTRACT — replay tooling and dashboards key off them. Do not
//     rename or reformat without coordinating with consumers (RM-1 / BUG-015).
//   - The chain order is load-bearing: the prior-year bridge runs FIRST so
//     partial-year IPO shapes surface as TTM_PRIOR_BRIDGE instead of being
//     silently absorbed into TTM_4Q; ANNUAL_FY returns the FY value UNCHANGED
//     (no annualization) so FY-latest tickers stay bit-for-bit (BUG-015 §4).
//   - Metric extractors MUST tolerate a nil *FinancialData (return 0) — the
//     unified guards treat metric(d) <= 0 as "unusable period".

// Unified TTM source identifiers. The per-metric alias blocks in
// financial_data.go (revenueSource*) and trailing_operating_income.go
// (oiSource*) point here so existing tests and call sites are untouched.
const (
	ttmSourceTTM4Q             = "TTM_4Q"
	ttmSourceTTMPriorBridge    = "TTM_PRIOR_BRIDGE"
	ttmSourceAnnualFY          = "ANNUAL_FY"
	ttmSourceAnnualizedQuarter = "ANNUALIZED_QUARTER"
	ttmSourceInsufficient      = "INSUFFICIENT_HISTORY"
)

// ttmSpec parameterizes the shared chain for one metric.
type ttmSpec struct {
	// metric returns the per-period value to sum. MUST handle a nil
	// *FinancialData by returning 0 (the chain treats <= 0 as unusable).
	metric func(*FinancialData) float64

	// labelPrefix is the warning-string namespace: "revenue_base" or
	// "operating_income_base".
	labelPrefix string

	// insufficientNoun completes "insufficient %s history": "revenue" or
	// "operating-income".
	insufficientNoun string

	// annualizedNoun completes "annualized %s (4× extrapolation, …)":
	// "single-quarter revenue" or "single-quarter operating income".
	annualizedNoun string
}

// trailingTwelveMonths walks the documented fallback chain for spec's metric.
// Each branch reproduces the pre-unification per-metric strings verbatim.
func (h *HistoricalFinancialData) trailingTwelveMonths(spec ttmSpec) (value float64, source string, warning string) {
	if h == nil || len(h.Data) == 0 {
		return 0, ttmSourceInsufficient,
			fmt.Sprintf("%s: insufficient %s history", spec.labelPrefix, spec.insufficientNoun)
	}

	// 1) TTM_PRIOR_BRIDGE — partial-year + prior-year corresponding quarters.
	if v, ok := h.ttmPriorBridgeOf(spec.metric); ok {
		return v, ttmSourceTTMPriorBridge,
			fmt.Sprintf("%s: partial-year TTM bridged with prior-year quarters (handles seasonality)", spec.labelPrefix)
	}

	// 2) TTM_4Q — four most recent contiguous quarters, summed.
	if v, ok := h.ttmFourQuartersOf(spec.metric); ok {
		return v, ttmSourceTTM4Q, ""
	}

	// 3) ANNUAL_FY — latest fiscal-year filing, value UNCHANGED (BUG-015:
	// FY-latest tickers stay bit-for-bit). Clean (no warning) when TTM was
	// structurally unattempted (no quarters at all); annotated when TTM was
	// attempted but failed.
	if v, period, date, ok := h.latestAnnualOf(spec.metric); ok {
		if len(h.GetQuarterlyPeriods()) == 0 {
			return v, ttmSourceAnnualFY, ""
		}
		return v, ttmSourceAnnualFY,
			fmt.Sprintf("%s: TTM unavailable, used latest FY ($%.0f dated %s)",
				spec.labelPrefix, v, date.Format("2006-01-02")) + fmt.Sprintf(" [%s]", period)
	}

	// 4) ANNUALIZED_QUARTER — lossy scaling of 1-3 quarters.
	if v, ok := h.annualizedQuarterOf(spec.metric); ok {
		return v, ttmSourceAnnualizedQuarter,
			fmt.Sprintf("%s: annualized %s (4× extrapolation, ignores seasonality)", spec.labelPrefix, spec.annualizedNoun)
	}

	// 5) INSUFFICIENT_HISTORY.
	return 0, ttmSourceInsufficient,
		fmt.Sprintf("%s: insufficient %s history", spec.labelPrefix, spec.insufficientNoun)
}

// ttmFourQuartersOf returns the metric summed over the 4 most recent quarters
// when they form a contiguous span (no gaps, distinct quarters; may cross a
// fiscal-year boundary). Returns (0, false) when contiguity fails or any
// quarter has a non-positive metric (a zero quarter would silently shrink the
// TTM and re-introduce the BUG-015 base understatement).
func (h *HistoricalFinancialData) ttmFourQuartersOf(metric func(*FinancialData) float64) (float64, bool) {
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
		v := metric(quarterly[p])
		if v <= 0 {
			return 0, false
		}
		sum += v
	}
	return sum, true
}

// ttmPriorBridgeOf handles the partial-year (IPO) case: the latest year has
// 1-3 quarters forming a contiguous Q1-start run, and the prior year supplies
// the missing quarters at the same calendar position. Declines (false) for
// full-year shapes — TTM_4Q owns those — and whenever the bridge cannot be
// built cleanly (gaps, missing or non-positive prior-year quarters).
func (h *HistoricalFinancialData) ttmPriorBridgeOf(metric func(*FinancialData) float64) (float64, bool) {
	quarterly := h.GetQuarterlyPeriods()
	if len(quarterly) == 0 {
		return 0, false
	}

	latestYear := latestQuarterYear(quarterly)
	if latestYear == 0 {
		return 0, false
	}

	// Collect the latest year's quarters in ascending Q order.
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

	// 1-3 quarters forming a contiguous Q1-start run, each with a usable metric.
	if len(current) < 1 || len(current) > 3 {
		return 0, false
	}
	for i, q := range current {
		if q.sub != i+1 {
			return 0, false
		}
		if metric(q.data) <= 0 {
			return 0, false
		}
	}

	var sum float64
	for _, q := range current {
		sum += metric(q.data)
	}
	priorYear := latestYear - 1
	for sub := len(current) + 1; sub <= 4; sub++ {
		key := fmt.Sprintf("%dQ%d", priorYear, sub)
		d, ok := quarterly[key]
		if !ok {
			return 0, false
		}
		v := metric(d)
		if v <= 0 {
			return 0, false
		}
		sum += v
	}
	return sum, true
}

// latestAnnualOf returns the most recent FY filing's metric, period key and
// filing date, walking backward past FY periods with a non-positive metric.
// Period ordering uses the (year, sub) key — robust when FilingDate is zero
// in test fixtures.
func (h *HistoricalFinancialData) latestAnnualOf(metric func(*FinancialData) float64) (float64, string, time.Time, bool) {
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
		v := metric(d)
		if v <= 0 {
			continue
		}
		return v, periods[i], d.FilingDate, true
	}
	return 0, "", time.Time{}, false
}

// annualizedQuarterOf is the lossy last-resort tier: scales 1-3 contiguous
// Q1-start quarters of the latest year to a synthetic 12 months (4×Q1,
// 2×(Q1+Q2), or (4/3)×(Q1+Q2+Q3)). When Q1 of the latest year is absent it
// falls back to 4× the latest single positive quarter (stub-period filers).
// Callers MUST surface the warning string the chain driver emits.
func (h *HistoricalFinancialData) annualizedQuarterOf(metric func(*FinancialData) float64) (float64, bool) {
	quarterly := h.GetQuarterlyPeriods()
	if len(quarterly) == 0 {
		return 0, false
	}

	latestYear := latestQuarterYear(quarterly)
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
		v := metric(d)
		if v <= 0 {
			break
		}
		sum += v
		count++
	}

	// No Q1 in the latest year → 4× the latest single positive quarter.
	if count == 0 {
		var latestSub int
		var latestVal float64
		for p, d := range quarterly {
			y, s := parsePeriodKey(p)
			if y != latestYear || s < 1 || s > 4 {
				continue
			}
			v := metric(d)
			if v <= 0 {
				continue
			}
			if s > latestSub {
				latestSub = s
				latestVal = v
			}
		}
		if latestSub == 0 {
			return 0, false
		}
		return latestVal * 4, true
	}

	switch count {
	case 1:
		return sum * 4, true
	case 2:
		return sum * 2, true
	case 3:
		return sum * 4.0 / 3.0, true
	default:
		// count == 4 should have been picked up by ttmFourQuartersOf;
		// returning the raw sum is the safe behavior anyway.
		return sum, true
	}
}

// latestQuarterYear returns the most recent year carrying at least one
// quarter, or 0 when none qualifies. Shared by the bridge and annualized
// tiers (was duplicated 4× pre-unification).
func latestQuarterYear(quarterly map[string]*FinancialData) int {
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
	return latestYear
}

// metricRevenue is the revenue extractor for the shared chain. Nil-safe.
func metricRevenue(d *FinancialData) float64 {
	if d == nil {
		return 0
	}
	return d.Revenue
}
