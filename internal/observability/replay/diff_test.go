package replay

import (
	"math"
	"testing"
)

// TestCompareFloat_TableDriven covers the canonical pass/fail cases at the
// default tolerances. Each row's comment explains why it matters in the
// fintech-grade context — fail-loud for any future tolerance regression.
func TestCompareFloat_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		a, b float64
		rel  float64
		abs  float64
		want bool
	}{
		// Equal: trivially true.
		{"exact_equal", 156.42, 156.42, DefaultFloatRelTol, DefaultFloatAbsTol, true},
		// Inside default rel tol: ~1e-9 relative drift on a per-share value
		// is the legitimate ULP-level drift we want to absorb.
		{"within_rel_tol", 156.42, 156.42 + 156.42*1e-10, DefaultFloatRelTol, DefaultFloatAbsTol, true},
		// Outside default rel tol: would mask a real math change.
		{"outside_rel_tol", 156.42, 156.42 * 1.0001, DefaultFloatRelTol, DefaultFloatAbsTol, false},
		// Both zero: covered by abs tol path.
		{"both_zero", 0, 0, DefaultFloatRelTol, DefaultFloatAbsTol, true},
		// One zero, drift inside abs tol.
		{"near_zero_inside_abs", 0, 5e-13, DefaultFloatRelTol, DefaultFloatAbsTol, true},
		// One zero, drift outside abs tol.
		{"near_zero_outside_abs", 0, 5e-10, DefaultFloatRelTol, DefaultFloatAbsTol, false},
		// Negative values handled symmetrically.
		{"negative_within", -156.42, -156.42 - 1e-10, DefaultFloatRelTol, DefaultFloatAbsTol, true},
		// NaN equality.
		{"nan_eq_nan", math.NaN(), math.NaN(), DefaultFloatRelTol, DefaultFloatAbsTol, true},
		{"nan_vs_value", math.NaN(), 1.0, DefaultFloatRelTol, DefaultFloatAbsTol, false},
		// Inf equality / disequality.
		{"inf_eq_inf", math.Inf(+1), math.Inf(+1), DefaultFloatRelTol, DefaultFloatAbsTol, true},
		{"inf_vs_neginf", math.Inf(+1), math.Inf(-1), DefaultFloatRelTol, DefaultFloatAbsTol, false},
		{"inf_vs_value", math.Inf(+1), 1.0, DefaultFloatRelTol, DefaultFloatAbsTol, false},
		// Custom tolerances override defaults.
		{"loose_rel", 100, 110, 0.1, 0, true},
		{"strict_abs", 0, 1e-15, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareFloat(tt.a, tt.b, tt.rel, tt.abs)
			if got != tt.want {
				t.Fatalf("CompareFloat(%v, %v, rel=%v, abs=%v) = %v, want %v",
					tt.a, tt.b, tt.rel, tt.abs, got, tt.want)
			}
		})
	}
}

// TestCompareFloat_Symmetric verifies the comparison is order-independent.
// A future implementation that accidentally treats `a` differently from `b`
// (e.g. uses |a-b| / |a| instead of /max) would fail this property test.
func TestCompareFloat_Symmetric(t *testing.T) {
	pairs := [][2]float64{
		{156.42, 156.42 + 1e-10},
		{0, 1e-13},
		{-1e6, -1e6 + 1e-3},
		{math.NaN(), 1.0},
		{math.Inf(+1), 0},
	}
	for _, p := range pairs {
		got1 := CompareFloat(p[0], p[1], DefaultFloatRelTol, DefaultFloatAbsTol)
		got2 := CompareFloat(p[1], p[0], DefaultFloatRelTol, DefaultFloatAbsTol)
		if got1 != got2 {
			t.Errorf("CompareFloat asymmetric for %v vs %v: %v != %v", p[0], p[1], got1, got2)
		}
	}
}

// TestFloatDiffOf_PopulatesAllFields locks the FloatDiff shape — every
// field downstream renderers expect (Path, Old, New, RelDrift, AbsDrift,
// WithinTolerance) is filled.
func TestFloatDiffOf_PopulatesAllFields(t *testing.T) {
	d := FloatDiffOf("dcf_value_per_share", 156.42, 156.81, DefaultFloatRelTol, DefaultFloatAbsTol)
	if d.Path != "dcf_value_per_share" {
		t.Errorf("Path = %q", d.Path)
	}
	if d.Old != 156.42 {
		t.Errorf("Old = %v", d.Old)
	}
	if d.New != 156.81 {
		t.Errorf("New = %v", d.New)
	}
	if d.AbsDrift == 0 {
		t.Errorf("AbsDrift should be >0 for a 0.39 absolute change; got %v", d.AbsDrift)
	}
	if d.RelDrift == 0 {
		t.Errorf("RelDrift should be >0; got %v", d.RelDrift)
	}
	if d.WithinTolerance {
		t.Errorf("0.39 absolute drift should NOT be within default tolerance")
	}
}

// TestFloatDiffOf_ZeroPair verifies a (0, 0) pair reports zero drift and
// WithinTolerance true. Important because the response struct has many
// legitimately-zero fields (CRP for non-ADR tickers, etc.).
func TestFloatDiffOf_ZeroPair(t *testing.T) {
	d := FloatDiffOf("crp", 0, 0, DefaultFloatRelTol, DefaultFloatAbsTol)
	if d.AbsDrift != 0 || d.RelDrift != 0 {
		t.Errorf("zero pair should have zero drift; got abs=%v rel=%v", d.AbsDrift, d.RelDrift)
	}
	if !d.WithinTolerance {
		t.Errorf("(0, 0) should be within tolerance")
	}
}

// TestResultDiff_HasMismatch_NilSafe verifies the orchestration layer can
// call HasMismatch on a nil ResultDiff without panicking — useful for the
// "infrastructure error before diff produced" path.
func TestResultDiff_HasMismatch_NilSafe(t *testing.T) {
	var d *ResultDiff
	if d.HasMismatch() {
		t.Errorf("nil ResultDiff should report no mismatch")
	}
	if d.FieldsChanged() != 0 {
		t.Errorf("nil ResultDiff should report 0 fields changed")
	}
	d.SortDiffs() // must not panic
}

// TestResultDiff_HasMismatchAndFieldsChanged tests the canonical case: a
// few floats and one string mismatch.
func TestResultDiff_HasMismatchAndFieldsChanged(t *testing.T) {
	d := &ResultDiff{
		Floats: []FloatDiff{
			{Path: "wacc", Old: 0.092, New: 0.094},
			{Path: "dcf_value_per_share", Old: 156.42, New: 156.81},
		},
		FloatsWithinTolerance: []FloatDiff{
			{Path: "growth_rate", Old: 0.05, New: 0.05000001},
		},
		Strings: []StringDiff{
			{Path: "industry.sic", Old: "TECH", New: "TECH_SAAS"},
		},
		FieldsTotal: 47,
	}
	if !d.HasMismatch() {
		t.Errorf("expected HasMismatch true")
	}
	if d.FieldsChanged() != 3 {
		t.Errorf("FieldsChanged = %d, want 3 (2 floats + 1 string; tolerance entries excluded)", d.FieldsChanged())
	}
}

// TestResultDiff_SortDiffs_Stable confirms repeated SortDiffs calls
// preserve order. Important because the orchestration layer may call this
// before each render to be safe.
func TestResultDiff_SortDiffs_Stable(t *testing.T) {
	d := &ResultDiff{
		Floats: []FloatDiff{
			{Path: "z"}, {Path: "a"}, {Path: "m"},
		},
		Strings: []StringDiff{
			{Path: "industry.sic"}, {Path: "currency"},
		},
	}
	d.SortDiffs()
	d.SortDiffs() // second call must be idempotent
	if d.Floats[0].Path != "a" || d.Floats[2].Path != "z" {
		t.Errorf("Floats not sorted: %+v", d.Floats)
	}
	if d.Strings[0].Path != "currency" {
		t.Errorf("Strings not sorted: %+v", d.Strings)
	}
}
