// rpl4d_coverage_close_test.go — RPL-4d coverage close.
//
// Test-only additions to lift package coverage above the 90% gate while
// also pinning the per-function ≥90% targets named in
// docs/reviewer/RPL4-r3b-followups.md §D. The bulk of the per-function
// work lives next to its production siblings (output_test.go,
// stage_diff_test.go). This file collects the cheap-helper unit tests
// (stringOrNil / stringOrNilStruct / camelToSnake / nilOrType / Err)
// and the comparator branch fixtures (compareSanityCheck /
// compareIndustry) needed to push package coverage from ~83.9% to ≥90%.
//
// Rule: nothing in this file changes production behavior. Each test
// either exercises a defensive helper directly or feeds the production
// comparator a focused input pair to cover a specific branch.
package replay

import (
	"errors"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestStringOrNil_BasicCases pins the diagnostic helper used inside
// asymmetric-nil StringDiff entries. Two cases: nil → "nil", non-nil →
// "%T" stringification (the concrete type name).
func TestStringOrNil_BasicCases(t *testing.T) {
	if got := stringOrNil(nil); got != "nil" {
		t.Errorf("stringOrNil(nil) = %q, want %q", got, "nil")
	}
	// Non-nil — returns the concrete type. Use a typed pointer so the
	// stringified output is deterministic across Go versions.
	var p *entities.SanityCheck = &entities.SanityCheck{}
	got := stringOrNil(p)
	if got == "nil" || got == "" {
		t.Errorf("stringOrNil(non-nil) = %q, expected concrete type name", got)
	}
}

// TestStringOrNilStruct_BasicCases pins the struct-pointer variant.
// nil-interface → "nil"; typed-nil pointer → "nil" (via the <nil>
// fmt-sprintf inspection); non-nil → "present".
func TestStringOrNilStruct_BasicCases(t *testing.T) {
	if got := stringOrNilStruct(nil); got != "nil" {
		t.Errorf("stringOrNilStruct(nil) = %q, want %q", got, "nil")
	}
	// Typed-nil pointer (interface holds a non-nil type but a nil value).
	// fmt.Sprintf renders this as "<nil>", which the helper catches.
	var typedNil *entities.SanityCheck
	if got := stringOrNilStruct(typedNil); got != "nil" {
		t.Errorf("stringOrNilStruct(typed nil) = %q, want %q", got, "nil")
	}
	// Non-nil → "present".
	if got := stringOrNilStruct(&entities.SanityCheck{ImpliedPE: 22.0}); got != "present" {
		t.Errorf("stringOrNilStruct(non-nil struct) = %q, want %q", got, "present")
	}
}

// TestCamelToSnake_HandlesCommonCases pins the best-effort
// CamelCase → snake_case fallback used by diff.go's struct walker when
// the goFieldToJSON map doesn't contain a field.
func TestCamelToSnake_HandlesCommonCases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// The heuristic inserts `_` before an uppercase letter when either
		// (a) the previous char is lowercase OR (b) the next char is
		// lowercase. So WACC → "wacc" (no underscores; pure adjacent-
		// uppercase run with no terminating lowercase), DCFValuePerShare
		// → "dcf_value_per_share" (the 'V' in 'Value' triggers underscore
		// because 'a' follows it; same for the 'P' in 'Per' and the 'S'
		// in 'Share'). SICCode → "sic_code" by the same rule applied at
		// the C-C-o boundary.
		{"DCFValuePerShare", "dcf_value_per_share"},
		{"WACC", "wacc"},
		{"GrowthRate", "growth_rate"},
		{"Currency", "currency"},
		{"SICCode", "sic_code"},
		{"a", "a"}, // single lowercase.
		{"A", "a"}, // single uppercase.
		{"", ""},   // empty.
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := camelToSnake(tc.in)
			if got != tc.want {
				t.Errorf("camelToSnake(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStructFieldToJSON_FallbackToCamelToSnake pins the unmapped-name
// branch of structFieldToJSON. Mapped names return the curated JSON tag;
// unmapped names fall back to the best-effort camelToSnake heuristic.
// This is the maintenance hot-spot the diff.go comment calls out — any
// future FairValueResponse field added without a goFieldToJSON entry
// silently routes through the heuristic, which the operator should
// know is the failure mode.
func TestStructFieldToJSON_FallbackToCamelToSnake(t *testing.T) {
	// Mapped name — returns the curated tag verbatim.
	if got := structFieldToJSON("WACC"); got != "wacc" {
		t.Errorf("structFieldToJSON(WACC) = %q, want %q (curated)", got, "wacc")
	}
	// Unmapped name — falls back to camelToSnake. Use a synthetic field
	// name unlikely to ever land in goFieldToJSON.
	if got := structFieldToJSON("XyzNewField"); got != "xyz_new_field" {
		t.Errorf("structFieldToJSON(XyzNewField) = %q, want %q (camelToSnake fallback)", got, "xyz_new_field")
	}
}

// TestNilOrType_BasicCases pins diff.go's nilOrType helper. nil → "nil",
// non-nil → "%T".
func TestNilOrType_BasicCases(t *testing.T) {
	if got := nilOrType(nil); got != "nil" {
		t.Errorf("nilOrType(nil) = %q", got)
	}
	if got := nilOrType(&handlers.FairValueResponse{}); got == "nil" || got == "" {
		t.Errorf("nilOrType(non-nil) = %q; expected concrete type", got)
	}
}

// TestResult_Err_NilReceiver_ReturnsNil pins Result.Err()'s nil-safe
// behaviour: calling .Err() on a nil *Result returns nil (not a panic).
// Mirrors the existing TestReport_ExitCode_NilSafe pattern for the Err
// surface.
func TestResult_Err_NilReceiver_ReturnsNil(t *testing.T) {
	var r *Result
	if got := r.Err(); got != nil {
		t.Errorf("nil-receiver Err() = %v, want nil", got)
	}
}

// TestResult_Err_ReturnsAttachedSentinel pins the typed-error retrieval
// path: when errSentinel is set, .Err() returns it and errors.Is matches.
// The sentinel is package-private — set via the same code path Replay
// uses on the error branches.
func TestResult_Err_ReturnsAttachedSentinel(t *testing.T) {
	sentinel := errors.New("test-sentinel")
	r := &Result{errSentinel: sentinel}
	if got := r.Err(); !errors.Is(got, sentinel) {
		t.Errorf("Err() = %v, want errors.Is(%v) == true", got, sentinel)
	}
	// And the empty-sentinel case returns nil.
	empty := &Result{}
	if got := empty.Err(); got != nil {
		t.Errorf("empty.Err() = %v, want nil", got)
	}
}

// TestCompareSanityCheck_AsymmetricNil covers the bundle-nil-current-nonnil
// branch of compareSanityCheck (compare.go:206). This branch is reached
// via the hand-rolled compareFairValueResponses walker, NOT via
// CompareResponse — the latter uses go-cmp reflection and routes through
// a different code path (diffReporter). Calling the hand-rolled walker
// directly is the only way to exercise the stringOrNilStruct helper from
// here.
func TestCompareSanityCheck_AsymmetricNil(t *testing.T) {
	a := &handlers.FairValueResponse{Ticker: "AAPL", Currency: "USD"}
	b := *a
	b.SanityCheck = &entities.SanityCheck{ImpliedPE: 18.0}
	d := compareFairValueResponses(a, &b, 0, 0)
	var found bool
	for _, s := range d.Strings {
		if s.Path == "sanity_check" {
			found = true
			if s.Old != "nil" {
				t.Errorf("asymmetric-nil Old = %q, want \"nil\"", s.Old)
			}
			if s.New != "present" {
				t.Errorf("asymmetric-nil New = %q, want \"present\"", s.New)
			}
		}
	}
	if !found {
		t.Errorf("expected sanity_check StringDiff for asymmetric-nil; got %+v", d.Strings)
	}
}

// TestCompareSanityCheck_IsReasonable_And_Flags covers the
// IsReasonable boolean diff branch AND the Flags-slice-length-mismatch
// branch AND the per-element Flags diff branch — the three branches of
// compareSanityCheck not covered by the basic float-drift test.
func TestCompareSanityCheck_IsReasonable_And_Flags(t *testing.T) {
	t.Run("is_reasonable_flips", func(t *testing.T) {
		a := &handlers.FairValueResponse{
			Ticker:      "AAPL",
			Currency:    "USD",
			SanityCheck: &entities.SanityCheck{IsReasonable: true, Flags: []string{"low_growth"}},
		}
		b := *a
		bSC := *a.SanityCheck
		bSC.IsReasonable = false // bool flip; everything else identical.
		b.SanityCheck = &bSC
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "sanity_check.is_reasonable" {
				found = true
				if s.Old != "true" || s.New != "false" {
					t.Errorf("is_reasonable diff: got %+v, want true→false", s)
				}
			}
		}
		if !found {
			t.Errorf("expected sanity_check.is_reasonable StringDiff; got %+v", d.Strings)
		}
	})

	t.Run("flags_length_mismatch", func(t *testing.T) {
		a := &handlers.FairValueResponse{
			Ticker:      "AAPL",
			Currency:    "USD",
			SanityCheck: &entities.SanityCheck{Flags: []string{"a", "b"}},
		}
		b := *a
		bSC := *a.SanityCheck
		bSC.Flags = []string{"a"} // different length.
		b.SanityCheck = &bSC
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "sanity_check.flags.len" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected sanity_check.flags.len StringDiff; got %+v", d.Strings)
		}
	})

	t.Run("flags_per_element_diff", func(t *testing.T) {
		a := &handlers.FairValueResponse{
			Ticker:      "AAPL",
			Currency:    "USD",
			SanityCheck: &entities.SanityCheck{Flags: []string{"low_growth", "high_wacc"}},
		}
		b := *a
		bSC := *a.SanityCheck
		bSC.Flags = []string{"low_growth", "low_wacc"} // element [1] differs.
		b.SanityCheck = &bSC
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "sanity_check.flags[1]" {
				found = true
				if s.Old != "high_wacc" || s.New != "low_wacc" {
					t.Errorf("flags[1] diff: got %+v, want high_wacc→low_wacc", s)
				}
			}
		}
		if !found {
			t.Errorf("expected sanity_check.flags[1] StringDiff; got %+v", d.Strings)
		}
	})
}

// TestCompareIndustry_FieldByFieldDiffs covers the per-field comparison
// branches of compareIndustry, which today's tests don't exercise. Each
// subtest flips exactly one of (SICCode, SIC, HeuristicCode,
// HeuristicName, Match) to confirm the right diff path emits.
func TestCompareIndustry_FieldByFieldDiffs(t *testing.T) {
	base := &handlers.Industry{
		SICCode:       "7372",
		SIC:           "TECH_SAAS",
		HeuristicCode: "TECH",
		HeuristicName: "Technology",
		Match:         true,
	}
	cases := []struct {
		name     string
		mutate   func(*handlers.Industry)
		wantPath string
		wantOld  string
		wantNew  string
	}{
		{"sic_code", func(i *handlers.Industry) { i.SICCode = "7370" }, "industry.sic_code", "7372", "7370"},
		{"sic", func(i *handlers.Industry) { i.SIC = "TECH" }, "industry.sic", "TECH_SAAS", "TECH"},
		{"heuristic_code", func(i *handlers.Industry) { i.HeuristicCode = "FIN" }, "industry.heuristic_code", "TECH", "FIN"},
		{"heuristic_name", func(i *handlers.Industry) { i.HeuristicName = "Financials" }, "industry.heuristic_name", "Technology", "Financials"},
		{"match", func(i *handlers.Industry) { i.Match = false }, "industry.match", "true", "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &handlers.FairValueResponse{Ticker: "AAPL", Currency: "USD", Industry: &handlers.Industry{}}
			*a.Industry = *base
			b := *a
			bInd := *a.Industry
			tc.mutate(&bInd)
			b.Industry = &bInd
			d := compareFairValueResponses(a, &b, 0, 0)
			var found bool
			for _, s := range d.Strings {
				if s.Path == tc.wantPath {
					found = true
					if s.Old != tc.wantOld || s.New != tc.wantNew {
						t.Errorf("%s diff: got %+v, want %s→%s", tc.wantPath, s, tc.wantOld, tc.wantNew)
					}
				}
			}
			if !found {
				t.Errorf("expected %s StringDiff; got %+v", tc.wantPath, d.Strings)
			}
		})
	}
}

// TestCompareIndustry_AsymmetricNil covers the bundle-nil-current-nonnil
// branch of compareIndustry, mirroring the SanityCheck asymmetric-nil
// test. Also routes through compareFairValueResponses (hand-rolled walker)
// rather than CompareResponse (go-cmp reflection).
func TestCompareIndustry_AsymmetricNil(t *testing.T) {
	a := &handlers.FairValueResponse{Ticker: "AAPL", Currency: "USD"}
	b := *a
	b.Industry = &handlers.Industry{SICCode: "7372", SIC: "TECH_SAAS"}
	d := compareFairValueResponses(a, &b, 0, 0)
	var found bool
	for _, s := range d.Strings {
		if s.Path == "industry" {
			found = true
			if s.Old != "nil" || s.New != "present" {
				t.Errorf("industry asymmetric-nil diff: got %+v, want nil→present", s)
			}
		}
	}
	if !found {
		t.Errorf("expected industry StringDiff for asymmetric-nil; got %+v", d.Strings)
	}
}

// TestCompareFairValueResponses_TypicalDriftPaths walks the hand-rolled
// walker through its non-nil, non-trivial branches: string fields,
// float fields, growth_rates length + per-element drift, warnings
// length + per-element drift, ADRRatioApplied / DCFHorizonYears int
// diffs, DCFPerYearPV length + per-element drift. Each subtest pins a
// specific branch of compareFairValueResponses (84.5% pre-test) without
// duplicating production behaviour — the walker is the source of truth
// and these tests verify the diff surface it emits.
func TestCompareFairValueResponses_TypicalDriftPaths(t *testing.T) {
	t.Run("string_field_diff", func(t *testing.T) {
		a := &handlers.FairValueResponse{Ticker: "AAPL", GrowthSource: "blended_v1"}
		b := *a
		b.GrowthSource = "blended_v2"
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "growth_source" && s.Old == "blended_v1" && s.New == "blended_v2" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected growth_source StringDiff; got %+v", d.Strings)
		}
	})

	t.Run("float_field_outside_tolerance", func(t *testing.T) {
		a := &handlers.FairValueResponse{WACC: 0.092}
		b := *a
		b.WACC = 0.092 * 1.05 // 5% drift
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, f := range d.Floats {
			if f.Path == "wacc" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected wacc Float diff; got %+v", d.Floats)
		}
	})

	t.Run("adr_ratio_applied_diff", func(t *testing.T) {
		a := &handlers.FairValueResponse{ADRRatioApplied: 5}
		b := *a
		b.ADRRatioApplied = 8
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "adr_ratio_applied" && s.Old == "5" && s.New == "8" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected adr_ratio_applied StringDiff; got %+v", d.Strings)
		}
	})

	t.Run("dcf_horizon_years_diff", func(t *testing.T) {
		a := &handlers.FairValueResponse{DCFHorizonYears: 5}
		b := *a
		b.DCFHorizonYears = 7
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "dcf_horizon_years" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected dcf_horizon_years StringDiff; got %+v", d.Strings)
		}
	})

	t.Run("growth_rates_length_mismatch", func(t *testing.T) {
		a := &handlers.FairValueResponse{GrowthRates: []float64{0.10, 0.08, 0.06}}
		b := *a
		b.GrowthRates = []float64{0.10, 0.08}
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "growth_rates.len" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected growth_rates.len StringDiff; got %+v", d.Strings)
		}
	})

	t.Run("growth_rates_per_element_drift", func(t *testing.T) {
		a := &handlers.FairValueResponse{GrowthRates: []float64{0.10, 0.08}}
		b := *a
		b.GrowthRates = []float64{0.10, 0.084} // [1] differs by 5%.
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, f := range d.Floats {
			if f.Path == "growth_rates[1]" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected growth_rates[1] Float diff; got %+v", d.Floats)
		}
	})

	t.Run("dcf_per_year_pv_length_mismatch", func(t *testing.T) {
		a := &handlers.FairValueResponse{DCFPerYearPV: []float64{10, 20, 30}}
		b := *a
		b.DCFPerYearPV = []float64{10, 20}
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "dcf_per_year_pv.len" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected dcf_per_year_pv.len StringDiff; got %+v", d.Strings)
		}
	})

	t.Run("dcf_per_year_pv_per_element_drift", func(t *testing.T) {
		a := &handlers.FairValueResponse{DCFPerYearPV: []float64{10, 20}}
		b := *a
		b.DCFPerYearPV = []float64{10, 21} // [1] differs by 5%.
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, f := range d.Floats {
			if f.Path == "dcf_per_year_pv[1]" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected dcf_per_year_pv[1] Float diff; got %+v", d.Floats)
		}
	})

	t.Run("warnings_length_mismatch", func(t *testing.T) {
		a := &handlers.FairValueResponse{Warnings: []string{"x", "y"}}
		b := *a
		b.Warnings = []string{"x"}
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "warnings.len" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected warnings.len StringDiff; got %+v", d.Strings)
		}
	})

	t.Run("warnings_per_element_diff", func(t *testing.T) {
		a := &handlers.FairValueResponse{Warnings: []string{"x", "y"}}
		b := *a
		b.Warnings = []string{"x", "z"} // [1] differs.
		d := compareFairValueResponses(a, &b, 0, 0)
		var found bool
		for _, s := range d.Strings {
			if s.Path == "warnings[1]" && s.Old == "y" && s.New == "z" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected warnings[1] StringDiff; got %+v", d.Strings)
		}
	})
}

// TestCompareFairValueResponses_NilArgs covers compareFairValueResponses's
// own bundle-nil-or-current-nil branch at compare.go:30-37. Both-nil
// returns an empty diff; one-nil emits a single $root StringDiff via
// stringOrNil.
func TestCompareFairValueResponses_NilArgs(t *testing.T) {
	t.Run("both_nil_empty_diff", func(t *testing.T) {
		d := compareFairValueResponses(nil, nil, 0, 0)
		if d == nil {
			t.Fatalf("expected non-nil ResultDiff even for both-nil; got nil")
		}
		if len(d.Floats) != 0 || len(d.Strings) != 0 {
			t.Errorf("both-nil: expected empty diff; got %+v", d)
		}
	})
	t.Run("bundle_nil_current_nonnil", func(t *testing.T) {
		b := &handlers.FairValueResponse{Ticker: "AAPL"}
		d := compareFairValueResponses(nil, b, 0, 0)
		if len(d.Strings) != 1 {
			t.Fatalf("expected 1 $root StringDiff; got %+v", d.Strings)
		}
		if d.Strings[0].Path != "$root" {
			t.Errorf("path = %q, want $root", d.Strings[0].Path)
		}
		// Note: stringOrNil uses interface-nil semantics, not typed-nil.
		// A typed-nil *handlers.FairValueResponse passed into the helper
		// has a non-nil interface{} (it carries the type), so the helper
		// returns the concrete type name rather than "nil". This is the
		// production behaviour — the test pins it explicitly.
		if d.Strings[0].Old != "*handlers.FairValueResponse" {
			t.Errorf("Old = %q, want \"*handlers.FairValueResponse\"", d.Strings[0].Old)
		}
		if d.Strings[0].New != "*handlers.FairValueResponse" {
			t.Errorf("New = %q, want \"*handlers.FairValueResponse\"", d.Strings[0].New)
		}
	})
	t.Run("bundle_nonnil_current_nil", func(t *testing.T) {
		a := &handlers.FairValueResponse{Ticker: "AAPL"}
		d := compareFairValueResponses(a, nil, 0, 0)
		if len(d.Strings) != 1 {
			t.Fatalf("expected 1 $root StringDiff; got %+v", d.Strings)
		}
		// Same typed-nil-vs-interface-nil note as above; both sides
		// stringify to the concrete type name.
		if d.Strings[0].New != "*handlers.FairValueResponse" {
			t.Errorf("New = %q, want \"*handlers.FairValueResponse\"", d.Strings[0].New)
		}
	})
}
