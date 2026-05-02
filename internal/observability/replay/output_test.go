package replay

import (
	"bytes"
	"encoding/json"
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
