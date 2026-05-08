package replay

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// ReplayVersion is the semver-compatible version embedded in JSON output.
// Bump on shape changes (renames, removals, type changes). Additive fields
// don't bump it. Spec §6 D6: JSON shape is a stable contract.
const ReplayVersion = "0.1"

// Status is the per-bundle outcome state. Stringified as the JSON
// "status" field; renderers also use it for the text-mode column.
type Status string

const (
	// StatusSkeletonOK is the R1-only status: replay walked the bundle
	// and validated the manifest, but did not run the engine. R2's PASS /
	// FAIL statuses replace this once the engine wiring lands.
	StatusSkeletonOK Status = "skeleton_ok"
	// StatusPass means the bundle replayed and produced zero
	// out-of-tolerance diffs (R2+).
	StatusPass Status = "pass"
	// StatusFail means the bundle replayed and produced at least one
	// out-of-tolerance diff (R2+).
	StatusFail Status = "fail"
	// StatusErrored means infrastructure failure (missing payloads,
	// schema drift without --allow-schema-drift, malformed manifest).
	// Maps to exit code 2.
	StatusErrored Status = "errored"
	// StatusWarn means schema or git drift was detected but the user
	// passed --allow-schema-drift / --allow-git-drift, so replay
	// continued. Reported alongside the underlying status (drift +
	// pass).
	StatusWarn Status = "warn"
)

// Result is the per-bundle outcome record. Both text and JSON renderers
// consume slices of these. Field names mirror the JSON contract in spec
// §7 — adding a field is fine; renaming bumps replay's version.
type Result struct {
	// Bundle is the absolute or repo-relative path to the bundle dir.
	// Stable identifier; always emitted.
	Bundle string `json:"bundle"`
	// Status is the outcome enum (see constants above).
	Status Status `json:"status"`
	// Ticker is read from the manifest. Surfaced redundantly in the JSON
	// row so consumers don't need a second join.
	Ticker string `json:"ticker,omitempty"`
	// FieldsTotal is the number of fields the diff layer compared. R1
	// leaves it 0; R2 fills it in.
	FieldsTotal int `json:"fields_total"`
	// FieldsChanged is the number of fields out of tolerance. Always
	// reported even when 0.
	FieldsChanged int `json:"fields_changed"`
	// SchemaDrift is true when the bundle's schema_versions disagree
	// with CurrentSchemaVersions. May coexist with a Pass status when
	// --allow-schema-drift is set.
	SchemaDrift bool `json:"schema_drift"`
	// GitDrift is true when the bundle's git_sha disagrees with the
	// running binary's. R2 wires this in; R1 always emits false.
	GitDrift bool `json:"git_drift"`
	// DurationMs is the wall-clock time the replay took for this
	// bundle. R1 emits 0 because no replay actually ran.
	DurationMs int64 `json:"duration_ms"`
	// Diffs are the out-of-tolerance per-field mismatches. R1 leaves
	// this nil; R2 populates it.
	Diffs []FloatDiff `json:"diffs,omitempty"`
	// StringDiffs are non-float field mismatches.
	StringDiffs []StringDiff `json:"string_diffs,omitempty"`
	// DriftedWithinTolerance lists drift entries that passed because
	// they were within configured tolerance. Default text mode hides
	// these; --verbose / JSON output always emit them.
	DriftedWithinTolerance []FloatDiff `json:"drifted_within_tolerance,omitempty"`
	// SchemaDriftEntries enumerates the per-entity drift detected. Only
	// populated when SchemaDrift is true.
	SchemaDriftEntries []SchemaDriftEntry `json:"schema_drift_entries,omitempty"`
	// StageDiffs carries Stage K's per-stage diff records, keyed by the
	// stage filename (e.g. "13-wacc.json"). Populated only when
	// Options.DiffStages is true; absent from JSON output (omitempty)
	// otherwise. Each StageDiff carries Floats / Strings /
	// DriftedWithinTolerance slices mirroring Result's response-level
	// diff fields. Spec §7 + R3b plan §3 Stage K.
	StageDiffs map[string]StageDiff `json:"stage_diffs,omitempty"`
	// Error carries the error message for an Errored Result. Stable
	// shape; the underlying error type is not promised.
	Error string `json:"error,omitempty"`

	// errSentinel is the underlying typed error so callers can
	// errors.Is(result.Err(), replay.ErrBundleMissingPayload). Not
	// serialized to JSON — the Error string is the stable contract.
	// Exposed via Result.Err() so tests can match sentinels without
	// string parsing.
	errSentinel error `json:"-"`
}

// Err returns the typed sentinel error attached to an Errored Result, or
// nil when no sentinel was recorded. Use errors.Is on the return to
// match a specific class (e.g. ErrBundleMissingPayload) without parsing
// the .Error string.
func (r *Result) Err() error {
	if r == nil {
		return nil
	}
	return r.errSentinel
}

// Summary is the aggregate row at the bottom of every replay invocation.
// Renderers append it to the per-bundle stream.
//
// Timing fields (R3 Stage L.3 — v2 plan Addition #4):
//   - DurationMs: cumulative per-bundle replay duration (sum of Result.DurationMs).
//     Pre-existing field; preserves R2 contract.
//   - WalkDurationMs: wall-clock time WalkBundles took to enumerate the bundle
//     tree. Single batch-level measurement (one WalkBundles call covers the run).
//   - ReplayDurationMs: wall-clock time the dispatcher spent running per-bundle
//     replays. Under --workers > 1 this is the wall clock of the bounded pool
//     (start of dispatch to last worker complete), NOT cumulative CPU time.
//
// The walk/replay split makes Surface #2's scale ceiling observable rather
// than debated: a future operator reporting "replay is slow on 10k bundles"
// has data to pinpoint walk vs replay as the bottleneck without spelunking
// source.
type Summary struct {
	Total            int   `json:"total"`
	Passed           int   `json:"passed"`
	Failed           int   `json:"failed"`
	Errored          int   `json:"errored"`
	DurationMs       int64 `json:"duration_ms"`
	WalkDurationMs   int64 `json:"walk_duration_ms"`
	ReplayDurationMs int64 `json:"replay_duration_ms"`
}

// Report bundles per-bundle Results plus a Summary into a single
// renderable shape. The JSON renderer emits exactly this struct (with
// replay_version + git_sha_current up top per spec §7).
//
// git_sha_current is intentionally NOT omitempty: spec §7 sample at
// L515-554 shows the field always populated. The cmd/replay binary
// resolves it from runtime/debug.ReadBuildInfo at startup so the JSON
// contract field is always present (an empty string indicates an
// unstamped/test-binary build, which is information itself).
type Report struct {
	ReplayVersion  string   `json:"replay_version"`
	GitSHACurrent  string   `json:"git_sha_current"`
	Summary        Summary  `json:"summary"`
	Results        []Result `json:"results"`
	Verbose        bool     `json:"-"` // renderer flag, not serialized
	GeneratedAtUTC string   `json:"-"` // renderer-only; pinned for golden tests
}

// ComputeSummary walks results and produces the aggregate counts. Pure
// function — every call is deterministic over the same input slice.
//
// Counting policy (mirrors spec F9 / §7 exit codes):
//   - StatusPass and StatusSkeletonOK count as "passed".
//   - StatusFail counts as "failed".
//   - StatusErrored counts as "errored".
//   - StatusWarn alone does not change the count — it's an annotation
//     accompanying one of the other statuses. The orchestration layer
//     emits Pass + SchemaDrift=true rather than a bare Warn.
func ComputeSummary(results []Result) Summary {
	s := Summary{Total: len(results)}
	for _, r := range results {
		switch r.Status {
		case StatusPass, StatusSkeletonOK:
			s.Passed++
		case StatusFail:
			s.Failed++
		case StatusErrored:
			s.Errored++
		}
		s.DurationMs += r.DurationMs
	}
	return s
}

// ExitCode returns the process-level exit code per spec F9. Centralized
// here so cmd/replay/main.go only needs to construct a Report and call
// .ExitCode() — keeping the policy near the data.
func (r *Report) ExitCode() int {
	if r == nil {
		// Defensive: a nil Report from an unexpected error path should
		// fall through to "infrastructure failure".
		return 2
	}
	if r.Summary.Errored > 0 {
		return 2
	}
	if r.Summary.Failed > 0 {
		return 1
	}
	return 0
}

// RenderJSON emits the Report as a single, indented JSON object on w.
// JSON shape is the stable contract (spec §6 D6); tests pin it via
// golden assertions in output_test.go.
//
// Field-omission rules:
//   - Fields tagged with `,omitempty` are dropped when zero/empty (e.g.
//     Result.Diffs is omitted entirely when no diffs exist).
//   - Fields without `,omitempty` always serialize, even when nil/zero.
//     For Report.Results specifically: callers that want an empty array
//     in --quiet mode MUST pass []Result{} (not nil), otherwise Go's
//     encoder emits `null`. cmd/replay/main.go does this in the --quiet
//     branch.
func (r *Report) RenderJSON(w io.Writer) error {
	if r == nil {
		return fmt.Errorf("replay: nil report")
	}
	// Sort results for deterministic JSON output. Idempotent.
	sort.Slice(r.Results, func(i, j int) bool { return r.Results[i].Bundle < r.Results[j].Bundle })

	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("replay: marshal report: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("replay: write report: %w", err)
	}
	// Trailing newline so downstream pipelines (jq, sh redirects) get
	// well-formed text output.
	_, err = io.WriteString(w, "\n")
	return err
}

// RenderText emits the human-readable text report on w. Layout mirrors
// spec §7's text sample: one line per bundle, then a SUMMARY line.
//
// Format rules (stable contract; tests pin via golden files):
//   - Each result row:    <bundle>   <STATUS>  fields=<changed>/<total>  duration=<ms>ms
//   - Drift rows are emitted as "  - <path>: <old> -> <new> (...)" beneath
//     the bundle row when the result has diffs.
//   - Summary line:        SUMMARY: <pass>/<total> passed, <fail> failed,
//     <err> errored, total duration=<ms>ms
func (r *Report) RenderText(w io.Writer) error {
	if r == nil {
		return fmt.Errorf("replay: nil report")
	}
	sort.Slice(r.Results, func(i, j int) bool { return r.Results[i].Bundle < r.Results[j].Bundle })

	for _, res := range r.Results {
		if err := writeResultRow(w, &res, r.Verbose); err != nil {
			return err
		}
	}

	// Aggregate footer.
	_, err := fmt.Fprintf(w, "\nSUMMARY: %d/%d passed, %d failed, %d errored, total duration=%dms\n",
		r.Summary.Passed, r.Summary.Total, r.Summary.Failed, r.Summary.Errored, r.Summary.DurationMs)
	return err
}

// writeResultRow renders a single Result row. Extracted so the test suite
// can pin individual rows without driving the full Report layout.
func writeResultRow(w io.Writer, res *Result, verbose bool) error {
	statusUpper := strings.ToUpper(string(res.Status))
	if _, err := fmt.Fprintf(w, "%s   %s   fields=%d/%d   duration=%dms",
		res.Bundle, statusUpper, res.FieldsChanged, res.FieldsTotal, res.DurationMs); err != nil {
		return err
	}
	if res.SchemaDrift {
		if _, err := io.WriteString(w, "   schema_drift=true"); err != nil {
			return err
		}
	}
	if res.GitDrift {
		if _, err := io.WriteString(w, "   git_drift=true"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}

	// Errored Results carry an error message — surface it on the next
	// indented line so users see the specific failure without --verbose.
	if res.Status == StatusErrored && res.Error != "" {
		if _, err := fmt.Fprintf(w, "  ERROR: %s\n", res.Error); err != nil {
			return err
		}
	}

	// Schema drift rows beneath the bundle line, always.
	if res.SchemaDrift {
		for _, e := range res.SchemaDriftEntries {
			if _, err := fmt.Fprintf(w, "  - schema:%s  bundle=%d current=%d", e.Entity, e.BundleVersion, e.CurrentVersion); err != nil {
				return err
			}
			if e.MissingFromCurrent {
				if _, err := io.WriteString(w, " (unknown to current code)"); err != nil {
					return err
				}
			}
			if e.MissingFromBundle {
				if _, err := io.WriteString(w, " (not stamped in bundle)"); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
	}

	// Float diff rows: always emit when status is Fail; emit
	// drifted-within-tolerance only in verbose mode.
	for _, d := range res.Diffs {
		if _, err := fmt.Fprintf(w, "  - %s: %v -> %v (rel_drift=%.6f)\n", d.Path, d.Old, d.New, d.RelDrift); err != nil {
			return err
		}
	}
	for _, d := range res.StringDiffs {
		if _, err := fmt.Fprintf(w, "  - %s: %q -> %q\n", d.Path, d.Old, d.New); err != nil {
			return err
		}
	}
	if verbose {
		for _, d := range res.DriftedWithinTolerance {
			if _, err := fmt.Fprintf(w, "  ~ %s: %v -> %v (within tolerance, rel_drift=%.6e)\n", d.Path, d.Old, d.New, d.RelDrift); err != nil {
				return err
			}
		}
	}
	return nil
}

// FormatTimestamp formats t as the ISO-8601-ish string used in JSON
// output. Centralized here so renderers can refer to a single rule.
// Currently unused by the JSON path (Go's default time.Time marshalling
// is sufficient) but exposed in case R3 needs it for a per-result
// completed_at field.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
