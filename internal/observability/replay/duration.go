package replay

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDurationExtended extends Go's time.ParseDuration with a single
// addition: a `d` suffix interpreted as 24-hour days. So `7d` = 168h, `30d`
// = 720h. All other Go-standard units (`ns`/`us`/`µs`/`ms`/`s`/`m`/`h`) work
// unchanged via delegation.
//
// Why not weeks/months/years: spec §6 explicitly rejects them.
//   - week ambiguity is small (always 7d) but the abbreviation `w` collides
//     with no Go unit, so the temptation to add it is real;
//   - month and year require a calendar reference (variable length);
//   - the watchlist-regression workflow only needs day-grain freshness
//     ("--filter-since 7d means bundles from the last week").
//
// Adding only `d` keeps the parser hermetic: pure arithmetic, no time zone,
// no calendar.
//
// Used by --filter-since (Phase R3). Defined in R1 because the duration
// arithmetic and validation are pure-package helpers and putting them in
// place now means R3 only needs to wire the flag.
//
// Format rules:
//   - Single, simple form: `<number>d` where number is a (possibly
//     fractional) decimal integer. Examples: `7d`, `0.5d`, `30d`.
//   - The `d` form is mutually exclusive with Go-standard compound forms
//     (we do NOT accept `1d12h` or `2d30m`). Mixed input is a parse error
//     to keep the contract obvious.
//   - Empty input returns an error rather than 0 to catch flag-default
//     accidents at the call site.
//   - Whitespace is rejected (e.g. `7 d` is invalid). Stay strict so the
//     flag value cannot silently become a valid duration via copy-paste
//     drift.
func ParseDurationExtended(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("replay: duration is empty")
	}
	if strings.ContainsAny(s, " \t") {
		return 0, fmt.Errorf("replay: duration %q must not contain whitespace", s)
	}

	// Detect the days suffix. We accept ONLY `<num>d` exactly — no compound
	// forms (e.g. `1d2h`) because that would conflict with Go's time-unit
	// grammar and force us to rewrite the parser. Single-suffix is enough
	// for --filter-since.
	//
	// Note: Go's std time-unit suffixes (ns/us/µs/ms/s/m/h) never end in
	// "d", so a HasSuffix(s, "d") check is unambiguous against the
	// current Go grammar. If a future Go release adds a unit ending in
	// "d", the test suite (which exercises every standard unit) will
	// catch it before this branch silently misroutes input.
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		// Pre-validate that the prefix is a numeric literal BEFORE
		// delegating to time.ParseDuration. Without this guard, an input
		// like "invalid" (which ends in `d`) gets trimmed to "invali" and
		// rehydrated as "invalih", surfacing a confusing
		// `time: invalid duration "invalih"` — a string the operator
		// never typed. Falling through to the standard time.ParseDuration
		// branch produces a clean error against the original input
		// instead. (QA cycle 1, R3 minor #2.)
		if _, err := strconv.ParseFloat(numStr, 64); err != nil {
			// Non-numeric prefix → not actually a days form. Let the
			// standard parser handle it; it will error against `s`
			// verbatim.
			d, err := time.ParseDuration(s)
			if err != nil {
				return 0, fmt.Errorf("replay: invalid duration %q: %w", s, err)
			}
			return d, nil
		}
		// time.ParseDuration accepts decimal floats, so reuse its number
		// parser by treating the days value as hours and multiplying.
		// Going through ParseDuration also gives consistent error
		// messages.
		hoursDur, err := time.ParseDuration(numStr + "h")
		if err != nil {
			return 0, fmt.Errorf("replay: invalid duration %q: %w", s, err)
		}
		return hoursDur * 24, nil
	}

	// Reject explicitly-unsupported units that Go also rejects but with
	// less readable error messages. Catching them here gives the operator a
	// hint rather than a generic "missing unit" failure.
	for _, badSuffix := range []string{"w", "wk", "mo", "y", "yr", "days"} {
		if strings.HasSuffix(strings.ToLower(s), badSuffix) {
			return 0, fmt.Errorf("replay: unsupported unit in %q (only Go-std units plus 'd' for days are accepted)", s)
		}
	}

	// Everything else delegates to Go's standard parser.
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("replay: invalid duration %q: %w", s, err)
	}
	return d, nil
}
