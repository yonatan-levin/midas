package replay

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Stage M.1 (R3b plan §3 Stage M.1) — JSON contract golden tests.
//
// Strategy: each test programmatically constructs a Report representing
// a specific output shape, renders it via RenderJSON, and compares
// byte-for-byte against a checked-in fixture under
// `testdata/golden/<name>.json`. On mismatch, the test surfaces a
// readable diff and reminds the operator how to regenerate.
//
// Maintenance flow: when JSON shape evolves intentionally (e.g. a new
// Result field is added), regenerate the goldens via:
//
//   UPDATE_GOLDEN=1 go test -run TestRenderJSON_GoldenFixture ./internal/observability/replay/
//
// The harness writes the rendered bytes back into testdata/golden/
// when UPDATE_GOLDEN=1 is set. Without that env var, the tests are
// strict assertions and fail on any byte-level drift.
//
// Time-sensitive scrubbing: Report carries no time-derived JSON field
// today (Verbose/GeneratedAtUTC are renderer-only, not serialized).
// Per-Result DurationMs is set explicitly by the fixture builders here
// so the values are deterministic. No runtime scrubber required.
//
// Concurrency note: Stage M.1 tests do NOT call t.Parallel because
// they may interact with the gitSHAResolver package var (per RPL-2e).
// Today's fixtures populate Report.GitSHACurrent directly so the
// resolver is not consulted, but the constraint is documented for
// future maintainers.

// goldenDir is the directory holding the checked-in golden fixtures.
// Relative paths inside this package work for both `go test` and
// `go test ./...` invocations.
const goldenDir = "testdata/golden"

// updateGolden returns true when the operator has set UPDATE_GOLDEN=1
// in the env, signaling intent to regenerate fixtures rather than
// assert against them.
func updateGolden() bool {
	return os.Getenv("UPDATE_GOLDEN") == "1"
}

// assertGolden renders r to JSON via RenderJSON, then either compares
// against testdata/golden/<name>.json byte-for-byte (default) or
// regenerates the file when UPDATE_GOLDEN=1 is set.
//
// Failure messages include the regeneration hint so operators don't
// need to remember the env-var convention.
func assertGolden(t *testing.T, r *Report, name string) {
	t.Helper()

	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	got := buf.Bytes()

	path := filepath.Join(goldenDir, name)

	if updateGolden() {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatalf("mkdir testdata/golden: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("regenerated golden fixture %s (%d bytes)", path, len(got))
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\nTo create the fixture for the first time, run:\n  UPDATE_GOLDEN=1 go test -run TestRenderJSON_GoldenFixture ./internal/observability/replay/", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("JSON drift vs %s\n\nGot:\n%s\n\nWant:\n%s\n\nTo update goldens after a deliberate JSON-shape change, run:\n  UPDATE_GOLDEN=1 go test -run TestRenderJSON_GoldenFixture ./internal/observability/replay/",
			path, string(got), string(want))
	}
}

// fixturePassOneBundle builds a Report with one passing bundle, default
// shape (no diffs / no schema drift / no stage diffs).
func fixturePassOneBundle() *Report {
	results := []Result{
		{
			Bundle:        "/fixtures/AAPL/req_pass",
			Status:        StatusPass,
			Ticker:        "AAPL",
			FieldsTotal:   36,
			FieldsChanged: 0,
			SchemaDrift:   false,
			GitDrift:      false,
			DurationMs:    87,
		},
	}
	return &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test-build",
		Summary:       ComputeSummary(results),
		Results:       results,
	}
}

// fixtureFailOneBundle builds a Report with a single failing bundle and
// one float diff outside tolerance.
func fixtureFailOneBundle() *Report {
	results := []Result{
		{
			Bundle:        "/fixtures/AMD/req_fail",
			Status:        StatusFail,
			Ticker:        "AMD",
			FieldsTotal:   36,
			FieldsChanged: 1,
			DurationMs:    92,
			Diffs: []FloatDiff{
				{Path: "dcf_value_per_share", Old: 156.42, New: 156.81, RelDrift: 0.0025, AbsDrift: 0.39, WithinTolerance: false},
			},
		},
	}
	return &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test-build",
		Summary:       ComputeSummary(results),
		Results:       results,
	}
}

// fixtureErroredOneBundle builds a Report with a single errored bundle
// (e.g. ErrBundleMissingPayload).
func fixtureErroredOneBundle() *Report {
	results := []Result{
		{
			Bundle:     "/fixtures/TSM/req_errored",
			Status:     StatusErrored,
			Ticker:     "TSM",
			DurationMs: 14,
			Error:      "replay: bundle missing payload: 05-fetch-sec.raw.json",
		},
	}
	return &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test-build",
		Summary:       ComputeSummary(results),
		Results:       results,
	}
}

// fixtureWithDriftedWithinTolerance builds a Report with a passing
// bundle that has a single drifted-within-tolerance entry. Pins the
// existing legacy field so the JSON contract reads consistently.
func fixtureWithDriftedWithinTolerance() *Report {
	results := []Result{
		{
			Bundle:        "/fixtures/MSFT/req_pass_with_tol",
			Status:        StatusPass,
			Ticker:        "MSFT",
			FieldsTotal:   36,
			FieldsChanged: 0,
			DurationMs:    101,
			DriftedWithinTolerance: []FloatDiff{
				{Path: "growth_rate", Old: 0.05, New: 0.050000000001, RelDrift: 2e-11, AbsDrift: 1e-12, WithinTolerance: true},
			},
		},
	}
	return &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test-build",
		Summary:       ComputeSummary(results),
		Results:       results,
	}
}

// fixtureWithStageDiffs builds a Report with a passing bundle that
// carries a populated stage_diffs map (Stage K's new field). One stage
// has an outside-tolerance float diff; another has a within-tolerance
// drift; a third is empty (omitted from output by the renderer's
// omitempty contract).
func fixtureWithStageDiffs() *Report {
	results := []Result{
		{
			Bundle:        "/fixtures/AMD/req_stage_diffs",
			Status:        StatusFail,
			Ticker:        "AMD",
			FieldsTotal:   36,
			FieldsChanged: 0,
			DurationMs:    104,
			StageDiffs: map[string]StageDiff{
				"13-wacc.json": {
					Floats: []FloatDiff{
						{Path: "stages.13-wacc.json.cost_of_equity", Old: 0.118, New: 0.121, RelDrift: 0.0254, AbsDrift: 0.003, WithinTolerance: false},
					},
				},
				"15-valuation.json": {
					DriftedWithinTolerance: []FloatDiff{
						{Path: "stages.15-valuation.json.dcf_value_per_share", Old: 156.42, New: 156.42 + 1e-10, RelDrift: 6.4e-13, AbsDrift: 1e-10, WithinTolerance: true},
					},
				},
			},
		},
	}
	return &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test-build",
		Summary:       ComputeSummary(results),
		Results:       results,
	}
}

// fixtureMixedWithWorkers4 builds a Report with three bundles
// (pass + fail + errored) that emulates a `--workers=4` run.
// Pins the deterministic-sort behavior (results sorted by Bundle path)
// AND the walk/replay timing fields populated by the dispatcher.
func fixtureMixedWithWorkers4() *Report {
	results := []Result{
		// Intentionally NOT in sorted order — RenderJSON's sort.Slice
		// must produce identical output regardless of input order.
		{
			Bundle: "/fixtures/AMD/req_3", Status: StatusErrored, Ticker: "AMD",
			DurationMs: 12,
			Error:      "replay: bundle missing payload: 05-fetch-sec.raw.json",
		},
		{
			Bundle: "/fixtures/AAPL/req_1", Status: StatusPass, Ticker: "AAPL",
			FieldsTotal: 36, FieldsChanged: 0, DurationMs: 87,
		},
		{
			Bundle: "/fixtures/MSFT/req_2", Status: StatusFail, Ticker: "MSFT",
			FieldsTotal: 36, FieldsChanged: 1, DurationMs: 95,
			Diffs: []FloatDiff{
				{Path: "wacc", Old: 0.092, New: 0.094, RelDrift: 0.0217, AbsDrift: 0.002, WithinTolerance: false},
			},
		},
	}
	summary := ComputeSummary(results)
	// Mimic the dispatcher's walk/replay split: cumulative DurationMs
	// stays as-is; ReplayDurationMs is the wall-clock the parallel pool
	// observed (smaller than DurationMs because workers ran concurrently).
	summary.WalkDurationMs = 5
	summary.ReplayDurationMs = 56
	return &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test-build",
		Summary:       summary,
		Results:       results,
	}
}

// TestRenderJSON_GoldenFixture_PassOneBundle — fixture #1 of 6.
func TestRenderJSON_GoldenFixture_PassOneBundle(t *testing.T) {
	assertGolden(t, fixturePassOneBundle(), "json_pass_one_bundle.json")
}

// TestRenderJSON_GoldenFixture_FailOneBundle — fixture #2 of 6.
func TestRenderJSON_GoldenFixture_FailOneBundle(t *testing.T) {
	assertGolden(t, fixtureFailOneBundle(), "json_fail_one_bundle.json")
}

// TestRenderJSON_GoldenFixture_ErroredOneBundle — fixture #3 of 6.
func TestRenderJSON_GoldenFixture_ErroredOneBundle(t *testing.T) {
	assertGolden(t, fixtureErroredOneBundle(), "json_errored_one_bundle.json")
}

// TestRenderJSON_GoldenFixture_WithDriftedWithinTolerance — fixture #4 of 6.
func TestRenderJSON_GoldenFixture_WithDriftedWithinTolerance(t *testing.T) {
	assertGolden(t, fixtureWithDriftedWithinTolerance(), "json_with_drifted_within_tolerance.json")
}

// TestRenderJSON_GoldenFixture_WithStageDiffs — fixture #5 of 6.
func TestRenderJSON_GoldenFixture_WithStageDiffs(t *testing.T) {
	assertGolden(t, fixtureWithStageDiffs(), "json_with_stage_diffs.json")
}

// TestRenderJSON_GoldenFixture_MixedWithWorkers4 — fixture #6 of 6.
func TestRenderJSON_GoldenFixture_MixedWithWorkers4(t *testing.T) {
	assertGolden(t, fixtureMixedWithWorkers4(), "json_mixed_with_workers_4.json")
}

// fixtureWindowsBundlePath builds a Report with a Windows-style backslash
// bundle path. The golden fixture pins the post-ToSlash normalized form
// emitted by RenderJSON (RPL-4b — Fix 3 of the 2026-05-11 UX dispatch).
//
// Strategy: a Linux operator running `jq '.results[].bundle' | xargs ...`
// against a JSON report captured on Windows must NOT see backslashes in
// the bundle field. The fixture's input has a backslash path; the golden
// JSON file has forward slashes — the diff between them IS the
// normalization contract.
func fixtureWindowsBundlePath() *Report {
	results := []Result{
		{
			Bundle:        `C:\Users\op\artifacts\2026-05-09\MXL\req_a293c059`,
			Status:        StatusPass,
			Ticker:        "MXL",
			FieldsTotal:   36,
			FieldsChanged: 0,
			DurationMs:    87,
		},
	}
	return &Report{
		ReplayVersion: ReplayVersion,
		GitSHACurrent: "test-build",
		Summary:       ComputeSummary(results),
		Results:       results,
	}
}

// TestRenderJSON_GoldenFixture_WindowsBundlePath — fixture #7 of 7.
// Pins the RPL-4b Windows-path normalization: the JSON "bundle" field
// must use forward slashes regardless of the input separator.
func TestRenderJSON_GoldenFixture_WindowsBundlePath(t *testing.T) {
	assertGolden(t, fixtureWindowsBundlePath(), "json_windows_bundle_path.json")
}
