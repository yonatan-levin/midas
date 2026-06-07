package guidance

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// dateLayout is the canonical YYYY-MM-DD form for filing_date / period_end.
const dateLayout = "2006-01-02"

// parseFilingDate parses a YYYY-MM-DD string into a UTC time.Time at midnight.
// UTC + midnight makes filing-date eligibility and staleness comparisons in the
// loader total and deterministic (NF2) — independent of the host time zone. An
// empty or malformed string is an error (the validator rejects those up front).
func parseFilingDate(s string) (time.Time, error) {
	t, err := time.ParseInLocation(dateLayout, s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("not a YYYY-MM-DD date: %w", err)
	}
	return t, nil
}

// fiscalYearEnd parses a guidance period like "FY2026" into the last instant of
// that fiscal calendar year (interpreted as the Dec-31 of the named year for
// staleness purposes). It is intentionally coarse: Phase 2's staleness rule
// (§8.3 item 5) only needs to know whether the period the guidance references
// has lapsed relative to as-of. A non-"FYxxxx" period returns ok=false and is
// treated as never-stale (the loader keeps it eligible) — Phase 3 may refine
// quarter-level periods.
//
// Returns (fiscalYearEndExclusiveBoundary, ok). The boundary is Jan-1 of the
// NEXT year (UTC midnight): a period "FY2026" lapses once as-of reaches
// 2027-01-01. Using the exclusive next-year boundary means a valuation dated
// anywhere within FY2026 still consumes FY2026 guidance.
func fiscalYearEnd(period string) (time.Time, bool) {
	p := strings.TrimSpace(strings.ToUpper(period))
	if !strings.HasPrefix(p, "FY") {
		return time.Time{}, false
	}
	yearStr := strings.TrimPrefix(p, "FY")
	year, err := strconv.Atoi(yearStr)
	if err != nil || year < 1900 || year > 9999 {
		return time.Time{}, false
	}
	// Exclusive boundary: first instant of the following calendar year.
	return time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.UTC), true
}
