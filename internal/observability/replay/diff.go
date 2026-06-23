package replay

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// init is the Stage O.6 (R3b) reflection guard for
// countFairValueFields. It uses reflect.NumField to count the actual
// struct fields of FairValueResponse + Industry + SanityCheck at
// package load and panics if they disagree with the constant returned
// by countFairValueFields below.
//
// Why panic at init: a field-count drift means goFieldToJSON's
// snake_case mapping is incomplete — a new field would render via
// camelToSnake's best-effort PascalCase→snake_case heuristic, which
// is wrong for most real fields. Failing fast at package load forces
// the maintainer to update countFairValueFields AND goFieldToJSON in
// the same commit as the new struct field.
//
// Panic scope: replay-binary-only. cmd/server does NOT import this
// package — enforced by cmd/server/import_boundary_test.go shipped
// under R3a Stage O.13. A panic here cannot crash production server
// startup. If a future refactor breaks the import boundary, the O.13
// test fails first; the O.6 panic is reachable only after that
// breakage.
func init() {
	responseFields := reflect.TypeFor[handlers.FairValueResponse]().NumField()
	industryFields := reflect.TypeFor[handlers.Industry]().NumField()
	sanityFields := reflect.TypeFor[entities.SanityCheck]().NumField()
	actual := responseFields + industryFields + sanityFields
	expected := countFairValueFields()
	if actual != expected {
		panic(fmt.Sprintf(
			"replay/diff.go: countFairValueFields drift — reflect counted %d fields (response=%d + industry=%d + sanity=%d), constant returns %d. "+
				"Update countFairValueFields and goFieldToJSON to match the new struct shape.",
			actual, responseFields, industryFields, sanityFields, expected,
		))
	}
}

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

// CompareResponse is the Stage G go-cmp-based response walker. It
// produces a ResultDiff by comparing every field of two
// FairValueResponse values using github.com/google/go-cmp under
// EquateApprox(relTol, absTol). Numeric mismatches outside tolerance
// surface as FloatDiff entries; string / bool / int mismatches surface
// as StringDiff entries.
//
// Stage G design note (v0.2 spec, plan §3 Stage G):
//
// This is the FIRST non-test consumer of go-cmp in the repository — the
// Stage G commit promotes the existing transitive dependency to a direct
// import in go.mod. The hand-rolled compareFairValueResponses walker in
// compare.go remains in place because it is exercised by Replay() and
// has different ergonomics (it walks specific field projections rather
// than reflecting over the whole struct). CompareResponse is the
// reflection-based alternative the spec calls for, used by the
// CompareResponse_* tests; future R3 work that adds new optional fields
// to FairValueResponse will benefit from CompareResponse's auto-discovery
// over the manual list compareFairValueResponses maintains.
//
// The two helpers are NOT redundant — they serve different purposes:
//
//   - compareFairValueResponses (compare.go) — used by Replay() during
//     orchestration. Walks specific field projections; faster (no
//     reflection); explicit pin against schema drift (a new field is
//     invisible until added to the walker, surfaced by REVIEWER).
//
//   - CompareResponse (this function) — used by tests + R3 tooling.
//     Walks via reflection through go-cmp; auto-discovers new fields;
//     simpler usage but harder to pin specific drift.
//
// Replay() may be migrated to CompareResponse in R3 once the integration
// tests have shaken out any go-cmp / Approx-tolerance edge cases.
//
// Default tolerances: when relTol == 0 the spec NF1 default
// DefaultFloatRelTol is used; same for absTol.
func CompareResponse(bundle, current *handlers.FairValueResponse, relTol, absTol float64) *ResultDiff {
	if relTol == 0 {
		relTol = DefaultFloatRelTol
	}
	if absTol == 0 {
		absTol = DefaultFloatAbsTol
	}
	d := &ResultDiff{}
	if bundle == nil && current == nil {
		return d
	}
	if bundle == nil || current == nil {
		d.Strings = append(d.Strings, StringDiff{Path: "$root", Old: nilOrType(bundle), New: nilOrType(current)})
		d.FieldsTotal = 1
		return d
	}

	// Use go-cmp with EquateApprox so numeric drift inside tolerance
	// reports as equal (we'd otherwise have to post-filter every diff
	// entry). EquateNaNs treats NaN==NaN so legitimate-NaN fields
	// (sanity_check.implied_pe when EPS is 0) don't false-fail.
	opts := cmp.Options{
		cmpopts.EquateApprox(relTol, absTol),
		cmpopts.EquateNaNs(),
	}

	// Custom reporter to capture per-field paths and old/new values.
	r := &diffReporter{
		floats:                make([]FloatDiff, 0),
		strings:               make([]StringDiff, 0),
		floatsWithinTolerance: make([]FloatDiff, 0),
		relTol:                relTol,
		absTol:                absTol,
	}
	opts = append(opts, cmp.Reporter(r))

	// Run the diff. Side-effect: reporter captures per-field changes.
	// We discard the returned string — the reporter has the structured
	// data we need.
	_ = cmp.Diff(bundle, current, opts...)

	d.Floats = r.floats
	d.Strings = r.strings
	d.FloatsWithinTolerance = r.floatsWithinTolerance
	d.FieldsTotal = countFairValueFields()
	d.SortDiffs()
	return d
}

// diffReporter is a custom cmp.Reporter that captures per-field paths
// in dotted JSON-style form (e.g. "sanity_check.implied_pe") and
// classifies each leaf-level mismatch into FloatDiff vs StringDiff.
//
// cmp invokes PushStep on each level of the path (struct field, map
// key, slice index, etc.) and Report at every leaf-level comparison.
// We accumulate the path stack and emit a diff entry only when Report
// indicates a non-equal pair.
type diffReporter struct {
	path                  cmp.Path
	floats                []FloatDiff
	strings               []StringDiff
	floatsWithinTolerance []FloatDiff
	relTol                float64
	absTol                float64
}

func (r *diffReporter) PushStep(s cmp.PathStep) { r.path = append(r.path, s) }
func (r *diffReporter) PopStep()                { r.path = r.path[:len(r.path)-1] }

func (r *diffReporter) Report(rs cmp.Result) {
	if rs.Equal() {
		return
	}
	vx, vy := r.path.Last().Values()
	if !vx.IsValid() || !vy.IsValid() {
		return
	}
	pathStr := jsonPath(r.path)
	switch vx.Kind() {
	case reflect.Float32, reflect.Float64:
		bv := vx.Float()
		cv := vy.Float()
		fd := FloatDiffOf(pathStr, bv, cv, r.relTol, r.absTol)
		if !fd.WithinTolerance {
			r.floats = append(r.floats, fd)
		} else if fd.AbsDrift > 0 {
			r.floatsWithinTolerance = append(r.floatsWithinTolerance, fd)
		}
	case reflect.String:
		r.strings = append(r.strings, StringDiff{
			Path: pathStr,
			Old:  vx.String(),
			New:  vy.String(),
		})
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		r.strings = append(r.strings, StringDiff{
			Path: pathStr,
			Old:  fmt.Sprintf("%d", vx.Int()),
			New:  fmt.Sprintf("%d", vy.Int()),
		})
	case reflect.Bool:
		r.strings = append(r.strings, StringDiff{
			Path: pathStr,
			Old:  fmt.Sprintf("%t", vx.Bool()),
			New:  fmt.Sprintf("%t", vy.Bool()),
		})
	default:
		// Slice / struct / pointer mismatches surface here. cmp's walker
		// drills further into composite types, so by the time Report
		// fires for a leaf we already have a primitive kind. Anything
		// else is a typed-nil edge — emit a generic StringDiff so the
		// path is still surfaced.
		r.strings = append(r.strings, StringDiff{
			Path: pathStr,
			Old:  fmt.Sprintf("%v", vx.Interface()),
			New:  fmt.Sprintf("%v", vy.Interface()),
		})
	}
}

// jsonPath converts a cmp.Path stack into a dotted snake_case path that
// matches the JSON tags on FairValueResponse fields, so a diff entry's
// path is directly grep-able in 17-response.json.
//
// Limitations: cmp's StructField step exposes the Go field name (e.g.
// "DCFValuePerShare"), not the JSON tag. We map the common Go-name →
// JSON-tag pairs for FairValueResponse here; new fields require an
// entry. For struct types other than FairValueResponse + nested types
// (Industry, SanityCheck), the Go field name is used unchanged.
func jsonPath(p cmp.Path) string {
	parts := make([]string, 0, len(p))
	for _, step := range p {
		switch s := step.(type) {
		case cmp.StructField:
			parts = append(parts, structFieldToJSON(s.Name()))
		case cmp.SliceIndex:
			xi, _ := s.SplitKeys()
			parts[len(parts)-1] = fmt.Sprintf("%s[%d]", parts[len(parts)-1], xi)
		case cmp.MapIndex:
			parts[len(parts)-1] = fmt.Sprintf("%s[%v]", parts[len(parts)-1], s.Key())
		}
	}
	// First step is always the top-level type (no name) — drop it if blank.
	out := strings.Join(parts, ".")
	out = strings.TrimPrefix(out, ".")
	return out
}

// structFieldToJSON converts a Go struct field name into its
// snake_case JSON tag name. Only the fields used by FairValueResponse
// + its nested types are mapped here; unmapped names fall through to a
// best-effort lowercase conversion. This is the maintenance hot-spot
// for Stage G — adding a new FairValueResponse field requires an entry
// here.
func structFieldToJSON(name string) string {
	if mapped, ok := goFieldToJSON[name]; ok {
		return mapped
	}
	// Best-effort: convert PascalCase → snake_case.
	return camelToSnake(name)
}

// goFieldToJSON maps Go field names to their JSON snake_case forms for
// the FairValueResponse struct + its nested types. Sourced from the
// `json:` tags in internal/api/v1/handlers/fair_value.go and
// internal/core/entities/valuation.go.
var goFieldToJSON = map[string]string{
	// FairValueResponse
	"Ticker":                "ticker",
	"WACC":                  "wacc",
	"GrowthRate":            "growth_rate",
	"GrowthRates":           "growth_rates",
	"GrowthSource":          "growth_source",
	"GrowthConfidence":      "growth_confidence",
	"TangibleValuePerShare": "tangible_value_per_share",
	"DCFValuePerShare":      "dcf_value_per_share",
	"PFFOValuePerShare":     "pffo_value_per_share",  // VAL-3 Phase 2
	"PAFFOValuePerShare":    "paffo_value_per_share", // VAL-3 Phase 2
	"AsOf":                  "as_of",
	"DataQualityScore":      "data_quality_score",
	"DataQualityGrade":      "data_quality_grade",
	"CalculationMethod":     "calculation_method",
	"CalculationVersion":    "calculation_version",
	"Warnings":              "warnings",
	"SanityCheck":           "sanity_check",
	"Industry":              "industry",
	"Currency":              "currency",
	"ADRRatioApplied":       "adr_ratio_applied",
	"CurrentPrice":          "current_price",
	// Tier 2 P0b additive fields — declared on FairValueResponse with
	// omitempty. Empty values drop on the wire so pre-Tier-2 captures stay
	// byte-equal; populated values must diff correctly here. P2 fills the
	// DCF diagnostic fields; P0b only declares the schema.
	"AssumptionProfile":     "assumption_profile",
	"ResolutionTrace":       "resolution_trace",
	"DCFHorizonYears":       "dcf_horizon_years",
	"DCFTerminalMethod":     "dcf_terminal_method",
	"DCFTerminalPctOfEV":    "dcf_terminal_pct_of_ev",
	"DCFPerYearPV":          "dcf_per_year_pv",
	"DCFTerminalGrowthUsed": "dcf_terminal_growth_used",
	// VAL-1 Phase 3: dcf_base_normalization records cyclical-base
	// normalization ("latest"|"3y_mean"). Omitempty — absent on non-cyclical
	// paths. Added in the same commit as the FairValueResponse field to keep
	// this map and the field count in sync (the init() drift guard enforces it).
	"DCFBaseNormalization": "dcf_base_normalization",
	// T10: applied_overrides echoes request-sourced knobs. Omitempty — absent
	// on default GET and POST{} paths. Added in the same commit as the struct
	// field to keep this map and the field count in sync.
	"AppliedOverrides": "applied_overrides",
	// Layer-B Phase-2: assumption_sources records per-assumption authority
	// provenance. Omitempty — absent on the default (no-guidance) path. Added in
	// the same commit as the struct field to keep this map and the field count
	// in sync (the init() drift guard enforces it).
	"AssumptionSources": "assumption_sources",
	// TDB-11: cleaning_adjustments surfaces the datacleaner audit trail.
	// Omitempty — absent when no adjuster fired. Added in the same commit as the
	// struct field to keep this map and the field count in sync (the init() drift
	// guard enforces it).
	"CleaningAdjustments": "cleaning_adjustments",
	// SanityCheck
	"ImpliedPE":            "implied_pe",
	"SectorMedianPE":       "sector_median_pe",
	"ImpliedEVEBITDA":      "implied_ev_ebitda",
	"SectorMedianEVEBITDA": "sector_median_ev_ebitda",
	"ImpliedPFCF":          "implied_pfcf",
	"SectorMedianPFCF":     "sector_median_pfcf",
	"IsReasonable":         "is_reasonable",
	"Flags":                "flags",
	// Industry
	"SICCode":       "sic_code",
	"SIC":           "sic",
	"HeuristicCode": "heuristic_code",
	"HeuristicName": "heuristic_name",
	"Match":         "match",
	// ResolutionTrace (closes T2-P4-W2 item 12 — kept here so a future
	// migration of Replay() to the reflection-based CompareResponse walker
	// gets the same dotted snake_case paths the hand-rolled walker emits).
	"ProfileID":       "profile_id",
	"Source":          "source",
	"ResolverVersion": "resolver_version",
	"ConfigVersion":   "config_version",
	"ConfigHash":      "config_hash",
	"MatchedRuleID":   "matched_rule_id",
	"FallbackReason":  "fallback_reason",
	"MissingFacts":    "missing_facts",
	"HumanReason":     "human_reason",
}

// camelToSnake is a fallback for fields not in goFieldToJSON. Best-effort
// only — accuracy degrades on adjacent-uppercase runs (e.g. WACC → wacc
// instead of w_a_c_c — desirable in this case but the heuristic is
// imperfect for other cases).
func camelToSnake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			// Only insert underscore if the previous char was lowercase
			// or if the next char is lowercase. Avoids "URL" → "u_r_l".
			prevLower := name[i-1] >= 'a' && name[i-1] <= 'z'
			nextLower := i+1 < len(name) && name[i+1] >= 'a' && name[i+1] <= 'z'
			if prevLower || nextLower {
				b.WriteRune('_')
			}
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// nilOrType is a small helper used by CompareResponse for nil-vs-non-nil
// reporting. Mirrors compare.go's stringOrNil but lives in diff.go to
// keep the go-cmp consumer self-contained.
func nilOrType(p any) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", p)
}

// countFairValueFields returns the number of fields the Stage G
// CompareResponse walker considers, used to populate
// ResultDiff.FieldsTotal. Pinned to the reflection-counted struct
// fields of FairValueResponse + Industry + SanityCheck — the init()
// guard above asserts the constant and reflection agree at package
// load time.
//
// Current: 36 (FairValueResponse — 30 pre-T10 + AppliedOverrides +
// AssumptionSources + CleaningAdjustments + PFFOValuePerShare +
// PAFFOValuePerShare [VAL-3 Phase 2] + DCFBaseNormalization [VAL-1
// Phase 3]) + 5 (Industry) + 8 (SanityCheck) = 49.
//
// When a future commit extends FairValueResponse, Industry, or
// SanityCheck:
//  1. The init() guard panics on the next package load with the
//     expected vs actual counts.
//  2. Update this constant to match the new reflection count.
//  3. Add an entry to goFieldToJSON for the new field's snake_case
//     name (otherwise camelToSnake's best-effort conversion runs).
func countFairValueFields() int {
	// FairValueResponse: 36 top-level public fields (30 pre-T10 + AppliedOverrides
	// + AssumptionSources [Layer-B Phase-2] + CleaningAdjustments [TDB-11]
	// + PFFOValuePerShare + PAFFOValuePerShare [VAL-3 Phase 2]
	// + DCFBaseNormalization [VAL-1 Phase 3]).
	// Industry: 5 fields.
	// SanityCheck: 8 fields.
	return 36 + 5 + 8
}
