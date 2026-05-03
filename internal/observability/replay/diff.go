package replay

import (
	"math"
	"sort"
)

// Default float tolerances per spec §5 D4. Two knobs because the relative
// and absolute cases are orthogonal:
//   - relTol binds for non-zero values (e.g. dcf=156.42 -> dcf=156.42 *
//     1.0000000001).
//   - absTol binds for legitimately-zero values (e.g. crp=0.0 for
//     non-ADR tickers; without an absolute floor a 1e-10 drift would
//     pass relative tolerance with the math `|0-1e-10|/max(0,1e-10) =
//     1` which fails relative tolerance — but a future caller might use
//     a different formula and we still want a small floor).
const (
	DefaultFloatRelTol = 1e-9
	DefaultFloatAbsTol = 1e-12
)

// FloatDiff describes one float-field mismatch inside a Result. R2's diff
// path will produce slices of these by walking the response struct;
// R1 exposes the per-pair helper so the bigger-picture diff path can
// build on it.
type FloatDiff struct {
	// Path is a dotted JSON-style locator (e.g. "dcf_value_per_share" or
	// "sanity_check.implied_pe"). R2 fills this in by tracing the field
	// path; R1's CompareFloat doesn't synthesize a Path and the caller
	// supplies one.
	Path string `json:"path"`
	// Old is the bundle's recorded value (from 17-response.json). Renamed
	// "old" rather than "bundle" so the JSON shape (§7) reads naturally.
	Old float64 `json:"old"`
	// New is the value the current code produced.
	New float64 `json:"new"`
	// RelDrift is |new-old|/max(|old|,|new|) when both are non-zero, else 0.
	// Reported even when the diff is within tolerance so the
	// "drifted-within-tolerance" annotation in --verbose mode (R3) has
	// data to work with.
	RelDrift float64 `json:"rel_drift"`
	// AbsDrift is |new-old|. Always reported.
	AbsDrift float64 `json:"abs_drift"`
	// WithinTolerance is true when the pair satisfies CompareFloat at the
	// configured tolerances. Useful to the renderer for color-coding.
	WithinTolerance bool `json:"within_tolerance"`
}

// CompareFloat returns true when two float64 values are equal within the
// supplied tolerances. Implements §5 D4's `EquateApprox` semantics:
//
//	|a-b| <= max(absTol, relTol * max(|a|, |b|))
//
// Special cases:
//   - NaN equality: a NaN never equals any value, including itself. Two
//     NaNs are treated as equal (consistent with cmpopts.EquateNaNs) so a
//     legitimate "no data, NaN-default" field doesn't false-fail.
//   - Inf: same as exact equality (Inf == Inf, -Inf == -Inf, otherwise
//     unequal).
//   - Zero: covered by absTol when at least one operand is zero; otherwise
//     relTol applies normally.
//
// The function is pure and allocation-free, so it is safe to call in a hot
// loop over a Result-shaped struct.
func CompareFloat(a, b, relTol, absTol float64) bool {
	// NaN handling first.
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	// Inf handling: bitwise-exact equality is the only sane comparison.
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return a == b
	}
	abs := math.Abs(a - b)
	if abs <= absTol {
		return true
	}
	maxMag := math.Max(math.Abs(a), math.Abs(b))
	return abs <= relTol*maxMag
}

// FloatDiffOf builds a populated FloatDiff for the supplied path / value
// pair under the given tolerances. The result's WithinTolerance flag
// reflects CompareFloat. Returns the FloatDiff regardless of whether the
// pair is in tolerance — the renderer is responsible for filtering or
// annotating based on WithinTolerance.
//
// Parameter names (bundleVal / currentVal) match the JSON Old/New
// convention used elsewhere in the renderer; we avoid the literal `new`
// because it shadows Go's builtin.
func FloatDiffOf(path string, bundleVal, currentVal, relTol, absTol float64) FloatDiff {
	d := FloatDiff{Path: path, Old: bundleVal, New: currentVal}
	d.AbsDrift = math.Abs(bundleVal - currentVal)
	maxMag := math.Max(math.Abs(bundleVal), math.Abs(currentVal))
	if maxMag > 0 {
		d.RelDrift = d.AbsDrift / maxMag
	}
	d.WithinTolerance = CompareFloat(bundleVal, currentVal, relTol, absTol)
	return d
}

// ResultDiff is the stub Result-level diff produced by the replay
// orchestration layer. R1 defines it so R2 can plug in the engine wiring
// without churning the renderer's contract.
//
// This is intentionally smaller than the eventual Result struct
// (replay.Result lives in output.go and embeds this). Diff is the shape
// of the per-bundle field-level evidence; Result is the per-bundle
// outcome wrapper.
type ResultDiff struct {
	// Floats are the per-field float mismatches outside tolerance. May be
	// empty when all fields match. Sorted by Path for stable output.
	Floats []FloatDiff
	// FloatsWithinTolerance are pairs that drifted but passed —
	// surfaced only via --verbose / "drifted-within-tolerance"
	// annotations. Empty in default text output. Sorted by Path.
	FloatsWithinTolerance []FloatDiff
	// Strings are per-field non-float mismatches (path -> old/new pair).
	// JSON-renderable as separate diff entries.
	Strings []StringDiff
	// FieldsTotal is the number of fields the diff layer compared. Set
	// by R2's diff implementation; R1 leaves it 0.
	FieldsTotal int
}

// StringDiff is the textual analog of FloatDiff for string-, bool-, or
// integer-valued fields where tolerance is irrelevant.
type StringDiff struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// HasMismatch returns true when the diff contains any out-of-tolerance
// float field or any string mismatch. Drifted-within-tolerance entries do
// NOT count as mismatches.
func (d *ResultDiff) HasMismatch() bool {
	if d == nil {
		return false
	}
	return len(d.Floats) > 0 || len(d.Strings) > 0
}

// FieldsChanged returns the number of mismatched fields (out-of-tolerance
// floats + string differences). Drifted-within-tolerance entries are
// excluded. Used by the renderer's "fields=2/47" summary line.
func (d *ResultDiff) FieldsChanged() int {
	if d == nil {
		return 0
	}
	return len(d.Floats) + len(d.Strings)
}

// SortDiffs sorts both slices of d in place by Path for stable output.
// Idempotent — safe to call repeatedly.
func (d *ResultDiff) SortDiffs() {
	if d == nil {
		return
	}
	sort.Slice(d.Floats, func(i, j int) bool { return d.Floats[i].Path < d.Floats[j].Path })
	sort.Slice(d.FloatsWithinTolerance, func(i, j int) bool {
		return d.FloatsWithinTolerance[i].Path < d.FloatsWithinTolerance[j].Path
	})
	sort.Slice(d.Strings, func(i, j int) bool { return d.Strings[i].Path < d.Strings[j].Path })
}
