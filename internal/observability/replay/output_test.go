package replay

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestComputeSummary_Basic walks the canonical mix: passes, fails,
// erroreds, plus durations. SkeletonOK counts as a pass per R1's
// pass-through.
func TestComputeSummary_Basic(t *testing.T) {
	results := []Result{
		{Bundle: "/a", Status: StatusPass, DurationMs: 87},
		{Bundle: "/b", Status: StatusFail, DurationMs: 92},
		{Bundle: "/c", Status: StatusErrored, DurationMs: 4},
		{Bundle: "/d", Status: StatusSkeletonOK, DurationMs: 50},
	}
	s := ComputeSummary(results)
	if s.Total != 4 {
		t.Errorf("Total = %d, want 4", s.Total)
	}
	if s.Passed != 2 {
		t.Errorf("Passed = %d, want 2", s.Passed)
	}
	if s.Failed != 1 {
		t.Errorf("Failed = %d, want 1", s.Failed)
	}
	if s.Errored != 1 {
		t.Errorf("Errored = %d, want 1", s.Errored)
	}
	if s.DurationMs != 87+92+4+50 {
		t.Errorf("DurationMs = %d", s.DurationMs)
	}
}

// TestReport_ExitCode locks the F9 exit-code policy: 0 when clean, 1 when
// any fail, 2 when any errored. Errored dominates fail.
func TestReport_ExitCode(t *testing.T) {
	tests := []struct {
		name string
		s    Summary
		want int
	}{
		{"all_pass", Summary{Total: 5, Passed: 5}, 0},
		{"one_fail", Summary{Total: 5, Passed: 4, Failed: 1}, 1},
		{"one_errored", Summary{Total: 5, Passed: 4, Errored: 1}, 2},
		{"errored_dominates_fail", Summary{Total: 5, Passed: 3, Failed: 1, Errored: 1}, 2},
		{"empty", Summary{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Report{Summary: tt.s}
			got := r.ExitCode()
			if got != tt.want {
				t.Errorf("ExitCode = %d, want %d (summary=%+v)", got, tt.want, tt.s)
			}
		})
	}
}

func TestReport_ExitCode_NilSafe(t *testing.T) {
	var r *Report
	if r.ExitCode() != 2 {
		t.Errorf("nil report should report exit 2 (defensive)")
	}
}

// TestReport_RenderJSON_Skeleton verifies the JSON contract for an R1-
// style skeleton-OK report. Pin matches spec §7 sample at the field level
// (we don't pin the bytes verbatim because indented JSON ordering of
// optional fields can vary across Go versions; instead we round-trip).
func TestReport_RenderJSON_Skeleton(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "a3f8c1e",
		Results: []Result{
			{
				Bundle:        "artifacts/2026-04-25/AAPL/req_01HW8ZQXKR",
				Status:        StatusSkeletonOK,
				Ticker:        "AAPL",
				FieldsTotal:   0,
				FieldsChanged: 0,
				DurationMs:    0,
			},
		},
		Summary: Summary{Total: 1, Passed: 1},
	}
	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	// Round-trip through the JSON decoder so we can assert at the field
	// level and avoid byte-level brittleness across Go versions.
	var got Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\noutput=%s", err, buf.String())
	}
	if got.ReplayVersion != ReplayVersion {
		t.Errorf("ReplayVersion = %q", got.ReplayVersion)
	}
	if got.GitSHACurrent != "a3f8c1e" {
		t.Errorf("GitSHACurrent = %q", got.GitSHACurrent)
	}
	if got.Summary.Total != 1 || got.Summary.Passed != 1 {
		t.Errorf("Summary = %+v", got.Summary)
	}
	if len(got.Results) != 1 {
		t.Fatalf("Results length = %d", len(got.Results))
	}
	r0 := got.Results[0]
	if r0.Bundle != "artifacts/2026-04-25/AAPL/req_01HW8ZQXKR" {
		t.Errorf("Bundle = %q", r0.Bundle)
	}
	if r0.Status != StatusSkeletonOK {
		t.Errorf("Status = %q", r0.Status)
	}
	if r0.Ticker != "AAPL" {
		t.Errorf("Ticker = %q", r0.Ticker)
	}
}

// TestReport_RenderJSON_StableShape verifies that two renders of the same
// Report produce byte-identical output (modulo a trailing newline). Pins
// the determinism property R3 will rely on for golden tests.
func TestReport_RenderJSON_StableShape(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results: []Result{
			{Bundle: "/a", Status: StatusSkeletonOK},
			{Bundle: "/b", Status: StatusSkeletonOK},
		},
		Summary: Summary{Total: 2, Passed: 2},
	}
	var buf1, buf2 bytes.Buffer
	if err := r.RenderJSON(&buf1); err != nil {
		t.Fatalf("RenderJSON 1: %v", err)
	}
	if err := r.RenderJSON(&buf2); err != nil {
		t.Fatalf("RenderJSON 2: %v", err)
	}
	if buf1.String() != buf2.String() {
		t.Errorf("RenderJSON output not stable across calls:\nfirst:\n%s\nsecond:\n%s",
			buf1.String(), buf2.String())
	}
}

// TestReport_RenderJSON_SortsResults locks the deterministic sort by
// Bundle path. Important for --workers=1 reproducibility.
func TestReport_RenderJSON_SortsResults(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results: []Result{
			{Bundle: "/z", Status: StatusSkeletonOK},
			{Bundle: "/a", Status: StatusSkeletonOK},
			{Bundle: "/m", Status: StatusSkeletonOK},
		},
		Summary: Summary{Total: 3, Passed: 3},
	}
	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var got Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Results[0].Bundle != "/a" || got.Results[2].Bundle != "/z" {
		t.Errorf("results not sorted: %+v", got.Results)
	}
}

// TestReport_RenderText_BasicLayout pins the text-mode layout per spec
// §7. Substring assertions rather than verbatim because the surface is
// alignment-tolerant — what matters is the user can read it.
func TestReport_RenderText_BasicLayout(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results: []Result{
			{
				Bundle:        "artifacts/2026-04-25/AAPL/req_01HW8ZQXKR",
				Status:        StatusSkeletonOK,
				FieldsChanged: 0,
				FieldsTotal:   0,
				DurationMs:    0,
			},
		},
		Summary: Summary{Total: 1, Passed: 1},
	}
	var buf bytes.Buffer
	if err := r.RenderText(&buf); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "artifacts/2026-04-25/AAPL/req_01HW8ZQXKR") {
		t.Errorf("text output should include bundle path; got:\n%s", out)
	}
	if !strings.Contains(out, "SKELETON_OK") {
		t.Errorf("text output should include status uppercase; got:\n%s", out)
	}
	if !strings.Contains(out, "fields=0/0") {
		t.Errorf("text output should include fields=0/0; got:\n%s", out)
	}
	if !strings.Contains(out, "SUMMARY: 1/1 passed, 0 failed, 0 errored") {
		t.Errorf("text output should include the spec summary line; got:\n%s", out)
	}
}

// TestReport_RenderText_FailWithDiffs renders a fail row with two diffs
// and confirms each diff appears as an indented bullet line.
func TestReport_RenderText_FailWithDiffs(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results: []Result{
			{
				Bundle:        "/x",
				Status:        StatusFail,
				FieldsChanged: 2,
				FieldsTotal:   47,
				Diffs: []FloatDiff{
					{Path: "dcf_value_per_share", Old: 156.42, New: 156.81, RelDrift: 0.0025},
					{Path: "wacc", Old: 0.092, New: 0.094, RelDrift: 0.0217},
				},
			},
		},
		Summary: Summary{Total: 1, Failed: 1},
	}
	var buf bytes.Buffer
	if err := r.RenderText(&buf); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "  - dcf_value_per_share:") {
		t.Errorf("expected indented bullet for dcf_value_per_share diff; got:\n%s", out)
	}
	if !strings.Contains(out, "  - wacc:") {
		t.Errorf("expected indented bullet for wacc diff; got:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL status line; got:\n%s", out)
	}
}

// TestReport_RenderText_VerboseShowsWithinTolerance ensures
// drifted-within-tolerance entries appear ONLY in verbose mode. This pins
// the "tilde prefix" convention so R3's --verbose flag stays
// backward-compatible.
func TestReport_RenderText_VerboseShowsWithinTolerance(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results: []Result{
			{
				Bundle: "/x",
				Status: StatusPass,
				DriftedWithinTolerance: []FloatDiff{
					{Path: "growth_rate", Old: 0.05, New: 0.05000001, RelDrift: 2e-7},
				},
			},
		},
		Summary: Summary{Total: 1, Passed: 1},
	}

	t.Run("non_verbose_hides_tolerance_drift", func(t *testing.T) {
		r.Verbose = false
		var buf bytes.Buffer
		if err := r.RenderText(&buf); err != nil {
			t.Fatalf("RenderText: %v", err)
		}
		if strings.Contains(buf.String(), "growth_rate") {
			t.Errorf("non-verbose should hide drifted-within-tolerance; got:\n%s", buf.String())
		}
	})
	t.Run("verbose_shows_tolerance_drift", func(t *testing.T) {
		r.Verbose = true
		var buf bytes.Buffer
		if err := r.RenderText(&buf); err != nil {
			t.Fatalf("RenderText: %v", err)
		}
		if !strings.Contains(buf.String(), "growth_rate") {
			t.Errorf("verbose should show drifted-within-tolerance; got:\n%s", buf.String())
		}
		if !strings.Contains(buf.String(), "  ~ ") {
			t.Errorf("expected tilde-prefix marker for tolerance drift; got:\n%s", buf.String())
		}
	})
}

// TestReport_RenderText_ErroredHasErrorLine confirms an Errored Result's
// Error string surfaces under the row in non-verbose text output. Critical
// for the schema-drift acceptance test where the user needs to see WHY
// the replay refused without --allow-schema-drift.
func TestReport_RenderText_ErroredHasErrorLine(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results: []Result{
			{
				Bundle: "/x",
				Status: StatusErrored,
				Error:  "schema drift detected (use --allow-schema-drift to proceed)",
			},
		},
		Summary: Summary{Total: 1, Errored: 1},
	}
	var buf bytes.Buffer
	if err := r.RenderText(&buf); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if !strings.Contains(buf.String(), "ERROR: schema drift detected") {
		t.Errorf("expected ERROR line for Errored result; got:\n%s", buf.String())
	}
}

// TestReport_RenderText_SchemaDriftRows verifies the schema-drift table
// is emitted beneath the bundle row when SchemaDrift=true.
func TestReport_RenderText_SchemaDriftRows(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results: []Result{
			{
				Bundle:      "/x",
				Status:      StatusErrored,
				SchemaDrift: true,
				SchemaDriftEntries: []SchemaDriftEntry{
					{Entity: "FinancialData", BundleVersion: 7, CurrentVersion: 8},
					{Entity: "NewEntity", CurrentVersion: 1, MissingFromBundle: true},
				},
				Error: "schema drift detected",
			},
		},
		Summary: Summary{Total: 1, Errored: 1},
	}
	var buf bytes.Buffer
	if err := r.RenderText(&buf); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "schema:FinancialData") {
		t.Errorf("expected schema:FinancialData drift row; got:\n%s", out)
	}
	if !strings.Contains(out, "bundle=7 current=8") {
		t.Errorf("expected version mismatch row; got:\n%s", out)
	}
	if !strings.Contains(out, "(not stamped in bundle)") {
		t.Errorf("expected MissingFromBundle annotation; got:\n%s", out)
	}
}

// TestReport_RenderJSON_NilDefensive guards against a nil Report panic at
// the call site.
func TestReport_RenderJSON_NilDefensive(t *testing.T) {
	var r *Report
	var buf bytes.Buffer
	err := r.RenderJSON(&buf)
	if err == nil {
		t.Errorf("RenderJSON on nil report should error")
	}
}

// TestReport_RenderText_NilDefensive same for text mode.
func TestReport_RenderText_NilDefensive(t *testing.T) {
	var r *Report
	var buf bytes.Buffer
	err := r.RenderText(&buf)
	if err == nil {
		t.Errorf("RenderText on nil report should error")
	}
}

// failingWriter implements io.Writer and returns an error after a
// configurable number of bytes. Used to cover the error-return branches in
// the renderers without mocking. After writeBudget bytes are written,
// further calls return errFailingWriter.
type failingWriter struct {
	written     int
	writeBudget int
}

var errFailingWriter = errFakeWrite("failing writer")

type errFakeWrite string

func (e errFakeWrite) Error() string { return string(e) }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.written >= w.writeBudget {
		return 0, errFailingWriter
	}
	remaining := w.writeBudget - w.written
	if len(p) <= remaining {
		w.written += len(p)
		return len(p), nil
	}
	w.written = w.writeBudget
	return remaining, errFailingWriter
}

// TestReport_RenderText_HandlesWriteErrors covers the error-return paths
// in writeResultRow and RenderText. Without this, lots of `if err :=
// ...; err != nil { return err }` lines are dead-coverage despite being
// the only safe behavior on a broken pipe.
func TestReport_RenderText_HandlesWriteErrors(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Verbose:       true,
		Results: []Result{
			{
				Bundle:        "/x",
				Status:        StatusFail,
				FieldsChanged: 1,
				FieldsTotal:   2,
				SchemaDrift:   true,
				GitDrift:      true,
				SchemaDriftEntries: []SchemaDriftEntry{
					{Entity: "FinancialData", BundleVersion: 7, CurrentVersion: 8},
					{Entity: "X", BundleVersion: 1, MissingFromCurrent: true},
					{Entity: "Y", CurrentVersion: 1, MissingFromBundle: true},
				},
				Diffs:                  []FloatDiff{{Path: "wacc", Old: 0.092, New: 0.094}},
				StringDiffs:            []StringDiff{{Path: "industry.sic", Old: "TECH", New: "TECH_SAAS"}},
				DriftedWithinTolerance: []FloatDiff{{Path: "growth_rate", Old: 0.05, New: 0.0500001}},
				Error:                  "ignored when not Errored",
			},
		},
		Summary: Summary{Total: 1, Failed: 1},
	}
	// Force RenderText to return at every possible byte boundary so each
	// internal Fprintf error path is hit at least once.
	for budget := 0; budget < 200; budget += 8 {
		w := &failingWriter{writeBudget: budget}
		_ = r.RenderText(w) // ignore err — we just want coverage
	}
}

// TestReport_RenderJSON_HandlesWriteError covers the JSON renderer's
// io.Writer error branches.
func TestReport_RenderJSON_HandlesWriteError(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results:       []Result{{Bundle: "/x", Status: StatusPass}},
		Summary:       Summary{Total: 1, Passed: 1},
	}
	w := &failingWriter{writeBudget: 0}
	if err := r.RenderJSON(w); err == nil {
		t.Errorf("expected error from failing writer")
	}
}

// TestReport_RenderText_ErroredWithEmptyError covers the branch where
// Status=Errored but Error is empty (no ERROR: line emitted).
func TestReport_RenderText_ErroredWithEmptyError(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results:       []Result{{Bundle: "/x", Status: StatusErrored}},
		Summary:       Summary{Total: 1, Errored: 1},
	}
	var buf bytes.Buffer
	if err := r.RenderText(&buf); err != nil {
		t.Fatalf("RenderText: %v", err)
	}
	if strings.Contains(buf.String(), "ERROR:") {
		t.Errorf("empty Error string should not produce ERROR: line; got:\n%s", buf.String())
	}
}

// TestReport_RenderJSON_AllKeysSnakeCase pins the JSON contract from
// spec §6 D6 (R1 follow-up #3): every JSON object key emitted by the
// renderer must be snake_case. The schema_drift_entries[] array
// previously emitted PascalCase keys (Entity, BundleVersion, ...) because
// the SchemaDriftEntry struct lacked json: tags. This test traverses
// every nested object and rejects any key with a capital letter.
//
// The regex pattern matches a JSON key like "FooBar":, distinguishing
// keys from string values that contain capitals.
func TestReport_RenderJSON_AllKeysSnakeCase(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "deadbeef",
		Results: []Result{
			{
				Bundle:        "/x",
				Status:        StatusErrored,
				Ticker:        "AAPL",
				FieldsTotal:   10,
				FieldsChanged: 2,
				SchemaDrift:   true,
				GitDrift:      true,
				DurationMs:    100,
				Diffs: []FloatDiff{
					{Path: "wacc", Old: 0.092, New: 0.094, RelDrift: 0.0217, AbsDrift: 0.002, WithinTolerance: false},
				},
				StringDiffs: []StringDiff{
					{Path: "industry.sic", Old: "TECH", New: "TECH_SAAS"},
				},
				DriftedWithinTolerance: []FloatDiff{
					{Path: "growth_rate", Old: 0.05, New: 0.05000001},
				},
				SchemaDriftEntries: []SchemaDriftEntry{
					{Entity: "FinancialData", BundleVersion: 7, CurrentVersion: 8},
					{Entity: "NewEntity", CurrentVersion: 1, MissingFromBundle: true},
					{Entity: "DroppedEntity", BundleVersion: 3, MissingFromCurrent: true},
				},
				Error: "test error",
			},
		},
		Summary: Summary{Total: 1, Errored: 1, DurationMs: 100},
	}

	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	// Match any JSON key (a quoted identifier followed by a colon) that
	// starts with an uppercase letter or contains an uppercase letter.
	// snake_case keys (e.g. "fields_total":) won't match. Stable contract:
	// no PascalCase keys anywhere in the output.
	violators := regexp.MustCompile(`"[A-Z][A-Za-z0-9_]*":`).FindAllString(buf.String(), -1)
	if len(violators) > 0 {
		t.Fatalf("RenderJSON emitted PascalCase keys (R1 follow-up #3): %v\nFull output:\n%s",
			violators, buf.String())
	}

	// Sanity: the snake_case versions should be present.
	out := buf.String()
	for _, expected := range []string{`"entity":`, `"bundle_version":`, `"current_version":`, `"missing_from_current":`, `"missing_from_bundle":`} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected SchemaDriftEntry key %s in output; got:\n%s", expected, out)
		}
	}
}

// TestReport_RenderJSON_QuietProducesEmptyArray pins R1 follow-up #8:
// --quiet output's results field must serialize as `[]`, not `null`.
// The orchestration code clones the report and clears Results; without
// initialization to []Result{}, Go's JSON encoder emits `null` because
// the slice is nil. Downstream tooling generally handles `[]` better
// than `null` (jq idioms, type-stable consumers).
func TestReport_RenderJSON_QuietProducesEmptyArray(t *testing.T) {
	// Mimic the cmd/replay --quiet flow: clone and zero the Results slice
	// to []Result{} (the post-fix shape).
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results:       []Result{},
		Summary:       Summary{Total: 1, Passed: 1},
	}
	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `"results": null`) {
		t.Errorf("--quiet --format=json must emit results: [] not results: null; got:\n%s", out)
	}
	if !strings.Contains(out, `"results": []`) {
		t.Errorf("expected results: [] in quiet JSON output; got:\n%s", out)
	}
}

// TestFormatTimestamp_StableUTC verifies UTC normalization of timestamp
// formatting (used by future R3 per-result completed_at fields, but
// covered now to keep the helper honest).
func TestFormatTimestamp_StableUTC(t *testing.T) {
	parsed, err := time.Parse(time.RFC3339Nano, "2026-04-25T12:34:56.789Z")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	out := FormatTimestamp(parsed)
	if !strings.Contains(out, "2026-04-25T12:34:56") {
		t.Errorf("FormatTimestamp = %q", out)
	}
}

// stageDiffsFixture builds a Result populated with one passing stage,
// one stage with a hard float diff, and one stage with a within-
// tolerance drift. Used by the Stage L.1 verbose-render tests.
func stageDiffsFixture() *Result {
	return &Result{
		Bundle: "/x",
		Status: StatusFail,
		StageDiffs: map[string]StageDiff{
			"13-wacc.json": {
				Floats: []FloatDiff{
					{Path: "stages.13-wacc.json.cost_of_equity", Old: 0.118, New: 0.121, RelDrift: 0.0254},
				},
			},
			"15-valuation.json": {
				DriftedWithinTolerance: []FloatDiff{
					{Path: "stages.15-valuation.json.dcf_value_per_share", Old: 156.42, New: 156.42 + 1e-9, RelDrift: 1e-11},
				},
			},
			// 12-growth-curve.json with empty diff — should be skipped
			// from output entirely.
			"12-growth-curve.json": {},
		},
	}
}

// TestRenderText_VerboseFalse_OmitsStageDiffsSection pins the negative
// contract — the "Stage diffs:" header must NOT appear in non-verbose
// mode. Stage L.1.
func TestRenderText_VerboseFalse_OmitsStageDiffsSection(t *testing.T) {
	res := stageDiffsFixture()
	var buf bytes.Buffer
	if err := writeResultRow(&buf, res, false); err != nil {
		t.Fatalf("writeResultRow: %v", err)
	}
	if strings.Contains(buf.String(), "Stage diffs:") {
		t.Errorf("non-verbose must not emit Stage diffs section; got:\n%s", buf.String())
	}
}

// TestRenderText_VerboseTrue_EmitsStageDiffsSection verifies the
// section header AND per-field rows appear under verbose. Stage L.1.
func TestRenderText_VerboseTrue_EmitsStageDiffsSection(t *testing.T) {
	res := stageDiffsFixture()
	var buf bytes.Buffer
	if err := writeResultRow(&buf, res, true); err != nil {
		t.Fatalf("writeResultRow: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Stage diffs:") {
		t.Errorf("verbose must emit Stage diffs section; got:\n%s", out)
	}
	// Sorted by stage filename — 13- precedes 15-.
	idx13 := strings.Index(out, "13-wacc.json:")
	idx15 := strings.Index(out, "15-valuation.json:")
	if idx13 < 0 || idx15 < 0 {
		t.Fatalf("expected both 13- and 15- stage headers; got:\n%s", out)
	}
	if idx13 >= idx15 {
		t.Errorf("expected 13-wacc.json BEFORE 15-valuation.json (sorted); got:\n%s", out)
	}
	// Field path must be stripped of the "stages.<file>." prefix.
	if !strings.Contains(out, "      - cost_of_equity:") {
		t.Errorf("expected stripped field path; got:\n%s", out)
	}
	// Within-tolerance entry uses tilde marker even inside Stage diffs.
	if !strings.Contains(out, "      ~ dcf_value_per_share:") {
		t.Errorf("expected tilde-marked within-tolerance line; got:\n%s", out)
	}
	// Empty stage (12-growth-curve.json) must be skipped.
	if strings.Contains(out, "12-growth-curve.json:") {
		t.Errorf("empty stage should be skipped; got:\n%s", out)
	}
}

// TestRenderText_VerboseTrue_EmitsBothResponseAndStageDiffs verifies
// that when a Result has BOTH response-level diffs AND stage diffs,
// both render and the response-level diffs precede the Stage diffs
// section (stable order, pinned by this test). Stage L.1.
func TestRenderText_VerboseTrue_EmitsBothResponseAndStageDiffs(t *testing.T) {
	res := &Result{
		Bundle: "/x",
		Status: StatusFail,
		Diffs: []FloatDiff{
			{Path: "wacc", Old: 0.092, New: 0.094, RelDrift: 0.0217},
		},
		StageDiffs: map[string]StageDiff{
			"13-wacc.json": {
				Floats: []FloatDiff{
					{Path: "stages.13-wacc.json.cost_of_equity", Old: 0.118, New: 0.121, RelDrift: 0.0254},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := writeResultRow(&buf, res, true); err != nil {
		t.Fatalf("writeResultRow: %v", err)
	}
	out := buf.String()
	respIdx := strings.Index(out, "  - wacc:")
	stageIdx := strings.Index(out, "  Stage diffs:")
	if respIdx < 0 || stageIdx < 0 {
		t.Fatalf("expected both response and stage diff sections; got:\n%s", out)
	}
	if respIdx >= stageIdx {
		t.Errorf("response-level diffs must precede Stage diffs section; got:\n%s", out)
	}
}

// TestRenderJSON_VerboseFlag_StageDiffsAlwaysIncluded verifies that
// JSON output is byte-identical regardless of the verbose flag — JSON
// emits everything because consumers post-filter via jq. Stage L.1.
func TestRenderJSON_VerboseFlag_StageDiffsAlwaysIncluded(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Results:       []Result{*stageDiffsFixture()},
		Summary:       Summary{Total: 1, Failed: 1},
	}

	var nonVerbose, verbose bytes.Buffer
	r.Verbose = false
	if err := r.RenderJSON(&nonVerbose); err != nil {
		t.Fatalf("RenderJSON non-verbose: %v", err)
	}
	r.Verbose = true
	if err := r.RenderJSON(&verbose); err != nil {
		t.Fatalf("RenderJSON verbose: %v", err)
	}
	if !bytes.Equal(nonVerbose.Bytes(), verbose.Bytes()) {
		t.Errorf("JSON output must be byte-identical regardless of verbose:\nnon-verbose:\n%s\nverbose:\n%s",
			nonVerbose.String(), verbose.String())
	}
	// Both must carry the stage_diffs key.
	if !bytes.Contains(nonVerbose.Bytes(), []byte(`"stage_diffs"`)) {
		t.Errorf("JSON must include stage_diffs even in non-verbose; got:\n%s", nonVerbose.String())
	}
}

// TestRenderText_HandlesWriteErrors_WithStageDiffs sweeps the write-budget
// space WITH StageDiffs populated so each error-return branch in
// writeStageDiffSection (the "Stage diffs:" header, the per-stage
// filename header, and each of the Floats / Strings / DriftedWithinTolerance
// per-line writes) is exercised. The pre-existing
// TestReport_RenderText_HandlesWriteErrors fixture has no StageDiffs, so
// writeStageDiffSection is never reached — RPL-4d coverage residual.
//
// Step=1 (not 8 like the older sweep) so every byte boundary is a
// candidate failure point; cheap relative to the total run.
func TestRenderText_HandlesWriteErrors_WithStageDiffs(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		Verbose:       true,
		Results: []Result{
			{
				Bundle: "/x",
				Status: StatusFail,
				StageDiffs: map[string]StageDiff{
					"13-wacc.json": {
						Floats: []FloatDiff{
							{Path: "stages.13-wacc.json.cost_of_equity", Old: 0.118, New: 0.121, RelDrift: 0.0254},
						},
						Strings: []StringDiff{
							{Path: "stages.13-wacc.json.model.chosen", Old: "old", New: "new"},
						},
						DriftedWithinTolerance: []FloatDiff{
							{Path: "stages.13-wacc.json.growth_rate", Old: 0.05, New: 0.0500001, RelDrift: 2e-7},
						},
					},
					"15-valuation.json": {
						Floats: []FloatDiff{
							{Path: "stages.15-valuation.json.dcf_value_per_share", Old: 156.42, New: 156.81, RelDrift: 0.0025},
						},
					},
				},
			},
		},
		Summary: Summary{Total: 1, Failed: 1},
	}
	// Sweep byte budget over the entire output surface so every internal
	// Fprintf / io.WriteString return-error path inside
	// writeStageDiffSection (and the upstream writeResultRow call site)
	// is hit at least once.
	for budget := 0; budget < 600; budget++ {
		w := &failingWriter{writeBudget: budget}
		_ = r.RenderText(w) // ignore err — we just want coverage
	}
}

// TestRenderJSON_HandlesWriteError_TrailingNewline pins RenderJSON's
// second error-return branch: io.WriteString(w, "\n") after the JSON
// body has been written successfully. The existing
// TestReport_RenderJSON_HandlesWriteError covers the first branch
// (w.Write(body) fails) with budget=0; this covers the trailing-newline
// branch by sweeping the budget into the byte-range where Write succeeds
// but the subsequent newline write fails.
func TestRenderJSON_HandlesWriteError_TrailingNewline(t *testing.T) {
	r := &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "deadbeef",
		Results:       []Result{{Bundle: "/x", Status: StatusPass}},
		Summary:       Summary{Total: 1, Passed: 1},
	}
	// First measure the body size so we know where the boundary is.
	var sizing bytes.Buffer
	if err := r.RenderJSON(&sizing); err != nil {
		t.Fatalf("RenderJSON sizing: %v", err)
	}
	bodyLen := sizing.Len() - 1 // subtract trailing newline
	// Budget = bodyLen: Write succeeds, WriteString("\n") fails. Other
	// budgets nearby may catch the partial-write branch. Sweep the
	// immediate neighborhood for robustness.
	for budget := bodyLen - 2; budget <= bodyLen+1; budget++ {
		w := &failingWriter{writeBudget: budget}
		_ = r.RenderJSON(w)
	}
}

// TestRenderJSON_BundlePathUsesForwardSlash pins RPL-4b: the JSON
// contract's "bundle" field uses forward-slash separators on all
// platforms so Linux shell pipelines piping bundle paths through
// `jq '.results[].bundle' | xargs ...` handle Windows-captured bundles
// correctly.
//
// Strategy: feed a Result with a Windows-style backslash path,
// render JSON, and assert the marshaled string contains no
// backslashes in the bundle field. Use a fixed input — the test
// runs cross-platform (filepath.ToSlash on Linux is a no-op for
// already-forward-slash paths, so a backslash input is the only
// reliable way to assert the normalization fires).
//
// The TEXT-mode renderer keeps native separators (operators see them
// visually; native is fine there). This test does NOT assert text-mode
// behavior — see writeResultRow for the unchanged text path.
func TestRenderJSON_BundlePathUsesForwardSlash(t *testing.T) {
	winPath := `C:\Users\op\artifacts\2026-05-09\MXL\req_a293c059`
	results := []Result{
		{
			Bundle:        winPath,
			Status:        StatusPass,
			Ticker:        "MXL",
			FieldsTotal:   36,
			FieldsChanged: 0,
			DurationMs:    87,
		},
	}
	r := &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test",
		Summary:       ComputeSummary(results),
		Results:       results,
	}

	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	// Decode the JSON and check the actual field value — string-grep on
	// the rendered output is brittle because Go's encoder escapes
	// backslashes as "\\". Decoding gives us the unescaped value.
	var got struct {
		Results []struct {
			Bundle string `json:"bundle"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v\nrendered:\n%s", err, buf.String())
	}
	if len(got.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(got.Results))
	}
	if strings.ContainsRune(got.Results[0].Bundle, '\\') {
		t.Errorf("JSON bundle must use forward-slash separators on all platforms; got %q", got.Results[0].Bundle)
	}
	// Spot-check the expected normalized form.
	want := "C:/Users/op/artifacts/2026-05-09/MXL/req_a293c059"
	if got.Results[0].Bundle != want {
		t.Errorf("bundle = %q, want %q", got.Results[0].Bundle, want)
	}

	// Input Report.Results.Bundle must NOT have been mutated — the text
	// renderer downstream may want the native form. This is the
	// "per-call copy" invariant the implementation comment promises.
	if r.Results[0].Bundle != winPath {
		t.Errorf("RenderJSON must not mutate input Report.Results; got %q, want %q",
			r.Results[0].Bundle, winPath)
	}
}


// TestRenderJSON_RPL7_OptionC_ErroredEmitsFieldsChangedMinusOne pins the
// RPL-7 Option C contract: when Result.Status == StatusErrored, the
// JSON output must emit fields_changed: -1 so CI scripts and operators
// cannot false-positive on "0 changes" while the run actually errored.
//
// Non-errored statuses (pass / fail / skeleton_ok / warn) must continue
// to emit the raw FieldsChanged count. The in-memory Result is NOT
// mutated — only the rendered JSON copy receives the sentinel.
func TestRenderJSON_RPL7_OptionC_ErroredEmitsFieldsChangedMinusOne(t *testing.T) {
	results := []Result{
		{
			Bundle:        "/fixtures/RPL7/req_errored",
			Status:        StatusErrored,
			Ticker:        "RPL7",
			FieldsTotal:   0,
			FieldsChanged: 0, // in-memory zero — JSON output must show -1
			DurationMs:    7,
			Error:         "replay: bundle missing payload: 05-fetch-sec.raw.json",
		},
		{
			Bundle:        "/fixtures/RPL7/req_pass",
			Status:        StatusPass,
			Ticker:        "RPL7",
			FieldsTotal:   36,
			FieldsChanged: 0, // in-memory zero — JSON output must STAY 0
			DurationMs:    11,
		},
		{
			Bundle:        "/fixtures/RPL7/req_fail",
			Status:        StatusFail,
			Ticker:        "RPL7",
			FieldsTotal:   36,
			FieldsChanged: 3, // raw count must survive into JSON
			DurationMs:    13,
		},
	}
	r := &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test-build",
		Summary:       ComputeSummary(results),
		Results:       results,
	}

	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}

	var got struct {
		Results []struct {
			Bundle        string `json:"bundle"`
			Status        string `json:"status"`
			FieldsChanged int    `json:"fields_changed"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v\nrendered:\n%s", err, buf.String())
	}
	want := map[string]int{
		"/fixtures/RPL7/req_errored": -1,
		"/fixtures/RPL7/req_pass":    0,
		"/fixtures/RPL7/req_fail":    3,
	}
	for _, row := range got.Results {
		w, ok := want[row.Bundle]
		if !ok {
			t.Errorf("unexpected bundle in output: %s", row.Bundle)
			continue
		}
		if row.FieldsChanged != w {
			t.Errorf("bundle=%s status=%s: fields_changed = %d, want %d", row.Bundle, row.Status, row.FieldsChanged, w)
		}
	}

	// Input Report.Results.FieldsChanged must NOT have been mutated — the
	// sentinel exists only at the JSON boundary. Downstream consumers
	// reading res.FieldsChanged in-memory continue to see the raw count
	// (integration tests in this package assert against 0 in the errored
	// case).
	for _, res := range r.Results {
		if res.Status == StatusErrored && res.FieldsChanged != 0 {
			t.Errorf("RenderJSON must not mutate input Result.FieldsChanged; errored row now %d, want 0", res.FieldsChanged)
		}
	}
}
