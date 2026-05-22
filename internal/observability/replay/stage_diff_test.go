package replay

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestStageDiffInventory_HasExpectedStages pins the canonical Stage K
// inventory. Catches accidental ordering or additions — the inventory
// is part of the contract a future replay-CLI consumer will rely on.
func TestStageDiffInventory_HasExpectedStages(t *testing.T) {
	want := []string{
		"10-clean-output.json",
		"12-growth-curve.json",
		"13-wacc.json",
		"15-valuation.json",
	}
	if !reflect.DeepEqual(StageDiffInventory, want) {
		t.Fatalf("StageDiffInventory drift:\n got: %v\nwant: %v", StageDiffInventory, want)
	}
}

// TestStageDiff_BothFilesAbsent_NoDiff verifies the common case where
// neither side has the stage file (e.g. non-DCF model paths skip
// 15-valuation.json entirely). Should produce an empty StageDiff so the
// renderer and JSON output don't surface false-positives.
func TestStageDiff_BothFilesAbsent_NoDiff(t *testing.T) {
	dir := t.TempDir()
	got := diffStage(dir, "15-valuation.json", nil, 0, 0)
	if len(got.Floats) != 0 || len(got.Strings) != 0 || len(got.DriftedWithinTolerance) != 0 {
		t.Fatalf("expected empty StageDiff for both-absent; got %+v", got)
	}
	if got.HasMismatch() {
		t.Fatalf("HasMismatch=true on empty StageDiff; want false")
	}
}

// TestStageDiff_FileAbsentInBundle_RecordedAsAsymmetric verifies the
// `bundle_missing` marker fires when the bundle lacks a stage file but
// the engine produced one. Operator should see this asymmetry without
// being buried in synthetic per-field "current_only" entries.
func TestStageDiff_FileAbsentInBundle_RecordedAsAsymmetric(t *testing.T) {
	dir := t.TempDir()
	current := []byte(`{"wacc": 0.1}`)
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Strings) != 1 {
		t.Fatalf("expected exactly 1 StringDiff (bundle_missing); got %d: %+v", len(got.Strings), got.Strings)
	}
	want := "stages.13-wacc.json.bundle_missing"
	if got.Strings[0].Path != want {
		t.Fatalf("path: got %q, want %q", got.Strings[0].Path, want)
	}
}

// TestStageDiff_FileAbsentInCurrent_RecordedAsAsymmetric verifies the
// inverse — bundle has the file but the engine didn't capture one
// (e.g. engine refactor stopped writing the snapshot). Operator should
// see the asymmetry surfaced explicitly.
func TestStageDiff_FileAbsentInCurrent_RecordedAsAsymmetric(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "13-wacc.json")
	if err := os.WriteFile(bundlePath, []byte(`{"wacc": 0.1}`), 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	got := diffStage(dir, "13-wacc.json", nil, 0, 0)
	if len(got.Strings) != 1 {
		t.Fatalf("expected exactly 1 StringDiff (current_missing); got %d: %+v", len(got.Strings), got.Strings)
	}
	want := "stages.13-wacc.json.current_missing"
	if got.Strings[0].Path != want {
		t.Fatalf("path: got %q, want %q", got.Strings[0].Path, want)
	}
}

// TestStageDiff_FloatFieldDriftWithinTolerance verifies that float
// drift on the order of ULP-level rounding lands in DriftedWithinTolerance
// — not in Floats — under the default tolerances. Catches a regression
// where the walker would mis-classify any non-zero drift as a hard fail.
func TestStageDiff_FloatFieldDriftWithinTolerance(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "13-wacc.json")
	if err := os.WriteFile(bundlePath, []byte(`{"cost_of_equity": 0.118}`), 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	current := []byte(`{"cost_of_equity": 0.11800000001}`) // ~8.5e-11 relative drift, well within DefaultFloatRelTol=1e-9
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Floats) != 0 {
		t.Fatalf("expected 0 hard Floats for within-tol drift; got %+v", got.Floats)
	}
	if len(got.DriftedWithinTolerance) != 1 {
		t.Fatalf("expected 1 DriftedWithinTolerance entry; got %d: %+v", len(got.DriftedWithinTolerance), got.DriftedWithinTolerance)
	}
	want := "stages.13-wacc.json.cost_of_equity"
	if got.DriftedWithinTolerance[0].Path != want {
		t.Fatalf("path: got %q, want %q", got.DriftedWithinTolerance[0].Path, want)
	}
}

// TestStageDiff_FloatFieldDriftOutsideTolerance verifies the converse:
// 5% drift on a float field surfaces as a Floats entry that the renderer
// will treat as a real diff.
func TestStageDiff_FloatFieldDriftOutsideTolerance(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "13-wacc.json")
	if err := os.WriteFile(bundlePath, []byte(`{"cost_of_equity": 0.118}`), 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	current := []byte(`{"cost_of_equity": 0.124}`) // ~5% drift
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Floats) != 1 {
		t.Fatalf("expected 1 Floats entry; got %d: %+v", len(got.Floats), got.Floats)
	}
	want := "stages.13-wacc.json.cost_of_equity"
	if got.Floats[0].Path != want {
		t.Fatalf("path: got %q, want %q", got.Floats[0].Path, want)
	}
	if !got.HasMismatch() {
		t.Fatalf("HasMismatch=false on outside-tol diff; want true")
	}
}

// TestStageDiff_NestedFieldPath_Renders verifies the dotted-path joining
// for nested objects. Operators rely on this path being grep-able
// against the source 13-wacc.json file.
func TestStageDiff_NestedFieldPath_Renders(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "13-wacc.json")
	bundleBytes := []byte(`{"result": {"cost_of_debt": {"after_tax": 0.045}}}`)
	if err := os.WriteFile(bundlePath, bundleBytes, 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	current := []byte(`{"result": {"cost_of_debt": {"after_tax": 0.046}}}`)
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Floats) != 1 {
		t.Fatalf("expected 1 Floats entry; got %+v", got)
	}
	want := "stages.13-wacc.json.result.cost_of_debt.after_tax"
	if got.Floats[0].Path != want {
		t.Fatalf("path: got %q, want %q", got.Floats[0].Path, want)
	}
}

// TestStageDiff_StringFieldChange_Diffs verifies string-typed fields
// (e.g. growth_source, model_selection.chosen) surface as StringDiff,
// NOT silently ignored. Stage K is a regression-detection tool — string
// drift is as important as float drift.
func TestStageDiff_StringFieldChange_Diffs(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "14-model-selection.json")
	if err := os.WriteFile(bundlePath, []byte(`{"chosen": "multi_stage_dcf"}`), 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	current := []byte(`{"chosen": "ddm"}`)
	// Reuse an in-inventory filename for the path; the comparator does
	// not gate by inventory.
	got := diffStage(dir, "14-model-selection.json", current, 0, 0)
	if len(got.Strings) != 1 {
		t.Fatalf("expected 1 StringDiff; got %d: %+v", len(got.Strings), got.Strings)
	}
	if got.Strings[0].Old != "multi_stage_dcf" || got.Strings[0].New != "ddm" {
		t.Fatalf("StringDiff old/new: got %+v", got.Strings[0])
	}
}

// TestStageDiff_NewFieldOnCurrent_RecordedAsCurrentOnly verifies that
// when the engine adds a field that the bundle's saved JSON did not
// have, the diff records it as `*.current_only` rather than failing.
// This is the additive-evolution path the spec calls out.
func TestStageDiff_NewFieldOnCurrent_RecordedAsCurrentOnly(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "13-wacc.json")
	if err := os.WriteFile(bundlePath, []byte(`{"wacc": 0.1}`), 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	current := []byte(`{"wacc": 0.1, "newfield": "v"}`)
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Strings) != 1 {
		t.Fatalf("expected 1 StringDiff for current_only; got %+v", got.Strings)
	}
	want := "stages.13-wacc.json.newfield.current_only"
	if got.Strings[0].Path != want {
		t.Fatalf("path: got %q, want %q", got.Strings[0].Path, want)
	}
}

// TestStageDiff_MalformedBundleJSON_RecordedAsParseError verifies the
// defensive fallback when the bundle's saved JSON is corrupt. The diff
// surface returns a single StringDiff at .bundle_parse_error — the
// operator sees the failure rather than a silent zero-diff.
func TestStageDiff_MalformedBundleJSON_RecordedAsParseError(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "13-wacc.json")
	if err := os.WriteFile(bundlePath, []byte(`{not valid json`), 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	current := []byte(`{"wacc": 0.1}`)
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Strings) != 1 {
		t.Fatalf("expected 1 parse-error StringDiff; got %+v", got.Strings)
	}
	want := "stages.13-wacc.json.bundle_parse_error"
	if got.Strings[0].Path != want {
		t.Fatalf("path: got %q, want %q", got.Strings[0].Path, want)
	}
}

// TestStageDiff_TopLevelArrayPayload_ExercisesWalkSlice synthesizes a
// JSON payload whose root is an ARRAY (rather than the production-typical
// scalar map). Production stage files 10/12/13/15 are scalar maps so
// walkSlice gets zero production reach for top-level arrays, but the
// walker handles the case defensively. Tests both the equal-length per-
// element-recursion path AND the length-mismatch path (the StringDiff at
// `<path>.length` plus the truncated prefix recursion).
//
// RPL-4d coverage close — Section D, walkSlice 0% → covered.
func TestStageDiff_TopLevelArrayPayload_ExercisesWalkSlice(t *testing.T) {
	t.Run("equal_length_array_drift", func(t *testing.T) {
		dir := t.TempDir()
		bundlePath := filepath.Join(dir, "12-growth-curve.json")
		if err := os.WriteFile(bundlePath, []byte(`[{"year":1,"rate":0.10},{"year":2,"rate":0.08}]`), 0o644); err != nil {
			t.Fatalf("seed bundle: %v", err)
		}
		current := []byte(`[{"year":1,"rate":0.10},{"year":2,"rate":0.09}]`)
		got := diffStage(dir, "12-growth-curve.json", current, 0, 0)
		if len(got.Floats) != 1 {
			t.Fatalf("expected 1 Floats entry from arr[1].rate drift; got %+v", got)
		}
		// Path uses bracket-indexed segment per walkSlice's contract.
		want := "stages.12-growth-curve.json.[1].rate"
		if got.Floats[0].Path != want {
			t.Fatalf("path: got %q, want %q", got.Floats[0].Path, want)
		}
	})

	t.Run("length_mismatch_records_length_diff_plus_prefix", func(t *testing.T) {
		dir := t.TempDir()
		bundlePath := filepath.Join(dir, "12-growth-curve.json")
		// Bundle has 3 elements, current has 2 — drift on the matched
		// prefix should still surface in addition to the length diff.
		if err := os.WriteFile(bundlePath, []byte(`[{"v":0.1},{"v":0.2},{"v":0.3}]`), 0o644); err != nil {
			t.Fatalf("seed bundle: %v", err)
		}
		current := []byte(`[{"v":0.1},{"v":0.25}]`)
		got := diffStage(dir, "12-growth-curve.json", current, 0, 0)
		// Expect: 1 StringDiff at .length + 1 Floats at [1].v
		var sawLength bool
		for _, s := range got.Strings {
			if s.Path == "stages.12-growth-curve.json.length" {
				sawLength = true
				if s.Old != "3" || s.New != "2" {
					t.Errorf("length diff old/new: got %+v, want 3/2", s)
				}
			}
		}
		if !sawLength {
			t.Errorf("expected length StringDiff at stages.12-growth-curve.json.length; got Strings=%+v", got.Strings)
		}
		if len(got.Floats) != 1 || got.Floats[0].Path != "stages.12-growth-curve.json.[1].v" {
			t.Errorf("expected per-element float drift on prefix [1].v; got Floats=%+v", got.Floats)
		}
	})
}

// TestStageDiff_BoolAndNilLeaves_ExerciseSwitchBranches drives the bool
// and nil branches of stageWalker.walk that previously had no test
// coverage. These cases are minor coverage residuals on `walk` (60%) that
// lift cheaply via the same diffStage entry point.
func TestStageDiff_BoolAndNilLeaves_ExerciseSwitchBranches(t *testing.T) {
	t.Run("bool_change", func(t *testing.T) {
		dir := t.TempDir()
		bundlePath := filepath.Join(dir, "13-wacc.json")
		if err := os.WriteFile(bundlePath, []byte(`{"is_adr": false, "is_fpi": true}`), 0o644); err != nil {
			t.Fatalf("seed bundle: %v", err)
		}
		current := []byte(`{"is_adr": true, "is_fpi": true}`)
		got := diffStage(dir, "13-wacc.json", current, 0, 0)
		if len(got.Strings) != 1 {
			t.Fatalf("expected 1 StringDiff for changed bool; got %+v", got.Strings)
		}
		if got.Strings[0].Old != "false" || got.Strings[0].New != "true" {
			t.Errorf("bool diff old/new: got %+v", got.Strings[0])
		}
	})

	t.Run("nil_equals_nil_no_diff", func(t *testing.T) {
		dir := t.TempDir()
		bundlePath := filepath.Join(dir, "13-wacc.json")
		// Both sides have explicit null at the same key — no drift expected.
		if err := os.WriteFile(bundlePath, []byte(`{"override": null, "wacc": 0.1}`), 0o644); err != nil {
			t.Fatalf("seed bundle: %v", err)
		}
		current := []byte(`{"override": null, "wacc": 0.1}`)
		got := diffStage(dir, "13-wacc.json", current, 0, 0)
		if len(got.Floats) != 0 || len(got.Strings) != 0 || len(got.DriftedWithinTolerance) != 0 {
			t.Fatalf("expected no diffs for both-null leaves; got %+v", got)
		}
	})
}

// TestStageDiff_ReadError_OnDirectoryAsFile pins the defensive
// branch in diffStage where os.ReadFile returns an error that is NOT
// os.ErrNotExist (e.g. EISDIR on Linux / "is a directory" on Windows when
// the named stage file is actually a directory). The bundleErr != nil
// path emits a StringDiff at `<file>.read_error` rather than treating it
// as an asymmetric absence.
//
// RPL-4d coverage close — Section D, diffStage 86.7% → higher.
func TestStageDiff_ReadError_OnDirectoryAsFile(t *testing.T) {
	dir := t.TempDir()
	// Create a DIRECTORY at the path where we'd expect the stage file.
	// os.ReadFile will return a non-NotExist error (EISDIR on POSIX,
	// ERROR_ACCESS_DENIED / "is a directory" on Windows).
	stageDirPath := filepath.Join(dir, "13-wacc.json")
	if err := os.Mkdir(stageDirPath, 0o755); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	current := []byte(`{"wacc": 0.1}`)
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Strings) != 1 {
		t.Fatalf("expected 1 StringDiff for read_error; got %+v", got.Strings)
	}
	want := "stages.13-wacc.json.read_error"
	if got.Strings[0].Path != want {
		t.Errorf("path: got %q, want %q", got.Strings[0].Path, want)
	}
	if got.Strings[0].Old == "" {
		t.Errorf("expected non-empty Old (error.Error()); got empty")
	}
	if got.Strings[0].New != "" {
		t.Errorf("expected empty New for read_error marker; got %q", got.Strings[0].New)
	}
}

// TestStageDiff_MalformedCurrentJSON_RecordedAsParseError mirrors the
// existing bundle-side parse-error test for the CURRENT side, lifting
// diffStage's remaining uncovered branch (line 189-196). Not strictly
// part of RPL-4d but co-located here because it sits on the same code
// path the read-error test exercises.
func TestStageDiff_MalformedCurrentJSON_RecordedAsParseError(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "13-wacc.json")
	if err := os.WriteFile(bundlePath, []byte(`{"wacc": 0.1}`), 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	current := []byte(`{not valid json`)
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Strings) != 1 {
		t.Fatalf("expected 1 parse-error StringDiff; got %+v", got.Strings)
	}
	want := "stages.13-wacc.json.current_parse_error"
	if got.Strings[0].Path != want {
		t.Errorf("path: got %q, want %q", got.Strings[0].Path, want)
	}
}

// TestGenericEqual_DirectUnitContract exercises the genericEqual
// fallback comparator directly. It is unreachable from diffStage
// because sameJSONKind already returns false for non-canonical JSON
// kinds, which short-circuits walk into a StringDiff before reaching
// the default branch that would call genericEqual. Direct unit testing
// keeps the defensive helper honest — if a future refactor lands a
// non-canonical kind in walk's switch (e.g. json.Number from a custom
// decoder), genericEqual stays the documented fallback.
//
// RPL-4d coverage close — Section D, genericEqual 0% → covered.
func TestGenericEqual_DirectUnitContract(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"equal_ints", 42, 42, true},
		{"unequal_ints", 42, 43, false},
		{"equal_int_and_int64_stringify_same", int(7), int64(7), true},
		{"different_types_same_stringification", "abc", []byte("abc"), false},
		{"both_nil", nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := genericEqual(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("genericEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestStageWalker_WalkUnknownKind_FallsBackThroughGenericEqual lifts
// coverage on stageWalker.walk's default-case branch. We construct a
// walker manually (it's package-private; same-package test scope makes
// this legal) and call walk() with two `int` values. sameJSONKind
// returns false for non-canonical types, so walk's type-mismatch
// branch fires and records a StringDiff — exercising the early-return
// path on type drift.
func TestStageWalker_WalkUnknownKind_RecordsStringDiff(t *testing.T) {
	w := &stageWalker{stageFile: "13-wacc.json", relTol: DefaultFloatRelTol, absTol: DefaultFloatAbsTol}
	// Two `int` values — sameJSONKind returns false, walk emits a
	// StringDiff at the field path.
	w.walk("custom.path", 42, 43)
	if len(w.strings) != 1 {
		t.Fatalf("expected 1 StringDiff for int-kind drift; got %+v", w.strings)
	}
	want := "stages.13-wacc.json.custom.path"
	if w.strings[0].Path != want {
		t.Errorf("path: got %q, want %q", w.strings[0].Path, want)
	}
}

// TestStageWalker_PathFor_EmptyFieldPath pins the contract of pathFor("")
// which returns just `stages.<stageFile>` — the root path. Used when
// walk() emits a top-level diff (e.g. type mismatch at the root).
// pathFor is at 66.7%; this lifts the empty-fieldPath branch.
func TestStageWalker_PathFor_EmptyFieldPath(t *testing.T) {
	w := &stageWalker{stageFile: "13-wacc.json"}
	if got := w.pathFor(""); got != "stages.13-wacc.json" {
		t.Errorf("pathFor(\"\") = %q, want stages.13-wacc.json", got)
	}
	if got := w.pathFor("field.sub"); got != "stages.13-wacc.json.field.sub" {
		t.Errorf("pathFor(non-empty) = %q", got)
	}
}

// TestStageDiff_TypeDriftAtRoot_RecordsRootStringDiff exercises the
// type-mismatched root path of walk: bundle is an object, current is an
// array. The walker should emit a single StringDiff at the root path
// (`stages.<file>`) without recursing.
func TestStageDiff_TypeDriftAtRoot_RecordsRootStringDiff(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "13-wacc.json")
	// Object on bundle, array on current — type mismatch at root.
	if err := os.WriteFile(bundlePath, []byte(`{"wacc": 0.1}`), 0o644); err != nil {
		t.Fatalf("seed bundle: %v", err)
	}
	current := []byte(`[{"wacc": 0.1}]`)
	got := diffStage(dir, "13-wacc.json", current, 0, 0)
	if len(got.Strings) != 1 {
		t.Fatalf("expected 1 root-level StringDiff for type drift; got %+v", got.Strings)
	}
	want := "stages.13-wacc.json"
	if got.Strings[0].Path != want {
		t.Errorf("path: got %q, want %q (object-vs-array type drift should emit at root)", got.Strings[0].Path, want)
	}
}

// TestStageWalker_CompareFloat_NaNVsNaN_NoDiff lifts the
// compareFloat NaN-equal-NaN branch which the existing
// diff_test.go::TestFloatDiffOf_NaN tests cover at the FloatDiffOf
// layer, but not via the stageWalker.compareFloat entrypoint.
//
// RPL-4d coverage close — incidental cleanup adjacent to walkSlice / walk.
func TestStageWalker_CompareFloat_NaNVsNaN_NoDiff(t *testing.T) {
	w := &stageWalker{stageFile: "13-wacc.json", relTol: DefaultFloatRelTol, absTol: DefaultFloatAbsTol}
	// math.NaN() vs math.NaN() — compareFloat must short-circuit return
	// without appending to floats / driftedWithin.
	nan1, nan2 := math.NaN(), math.NaN()
	w.compareFloat("cost_of_equity", nan1, nan2)
	if len(w.floats) != 0 || len(w.driftedWithin) != 0 {
		t.Errorf("NaN-vs-NaN should yield no diff; got floats=%v drifted=%v", w.floats, w.driftedWithin)
	}
}

// TestStageDiff_Empty_ReturnsTrueForZeroValueAndFalseOtherwise pins the
// helper's contract used by output.go::writeStageDiffSection. REVIEWER
// R3b #4 — a future replay-CLI consumer wanting to filter "non-empty
// stage diffs" relies on this helper to keep the predicate consistent
// with the rendering site.
func TestStageDiff_Empty_ReturnsTrueForZeroValueAndFalseOtherwise(t *testing.T) {
	if !(StageDiff{}).Empty() {
		t.Errorf("zero-value StageDiff.Empty() = false; want true")
	}
	nonEmpty := StageDiff{Strings: []StringDiff{{Path: "x"}}}
	if nonEmpty.Empty() {
		t.Errorf("StageDiff with one Strings entry .Empty() = true; want false")
	}
	onlyDrifted := StageDiff{DriftedWithinTolerance: []FloatDiff{{Path: "y"}}}
	if onlyDrifted.Empty() {
		t.Errorf("StageDiff with only DriftedWithinTolerance .Empty() = true; want false")
	}
	onlyFloats := StageDiff{Floats: []FloatDiff{{Path: "z"}}}
	if onlyFloats.Empty() {
		t.Errorf("StageDiff with only Floats entry .Empty() = true; want false")
	}
}
