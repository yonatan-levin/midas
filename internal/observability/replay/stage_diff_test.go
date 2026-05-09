package replay

import (
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
