// Package datacleaner — shared flexible date coercion helpers (TDB-10 / #10).
// Used by the flag evaluator's date-condition evaluation. Lenient by default:
// a small fixed layout list, no new dependency.
package datacleaner

import (
	"time"
)

// dateLayouts is the small fixed set of layouts accepted by parseFlexibleDate. Kept short and
// documented on purpose — these cover the shapes seen in XBRL/financial contexts. Do not grow
// this list speculatively; add a layout only when a real input requires it.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02",
	"01/02/2006",
	"--01-02", // XBRL dei:CurrentFiscalYearEndDate, e.g. "--09-28"
}

// parseFlexibleDate attempts a small fixed set of layouts. Returns ok=false if none parse.
func parseFlexibleDate(s string) (time.Time, bool) {
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// coerceTime accepts a time.Time directly or a parseable date string.
func coerceTime(v interface{}) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		return parseFlexibleDate(x)
	default:
		return time.Time{}, false
	}
}
