// Command replay re-runs a captured artifact bundle through the current
// code and diffs the produced response against the bundle's recorded
// 17-response.json.
//
// Phase R1 (this commit) ships ONLY the manifest-validate and walk
// scaffolding: the binary walks bundles, validates each manifest, checks
// schema drift, and emits per-bundle skeleton-OK rows + an aggregate
// summary. It does NOT yet run the engine — that's R2.
//
// See docs/refactoring/observability-replay-tooling-spec.md for the
// full design.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/midas/dcf-valuation-api/internal/observability/replay"
)

// resolveGitSHA reads the binary's VCS revision from runtime/debug.ReadBuildInfo.
// Populated for builds produced by `go build` from a clean VCS tree
// (Go ≥ 1.18 stamps it automatically). Returns an empty string when
// running under `go test` or `go run` (no VCS stamping in those modes) —
// JSON output preserves the empty string so consumers can distinguish
// "no git info" from "git info matches".
//
// Replaces the previous user-facing --git-sha flag (R1 follow-up #11).
// Operator override is deferred to R2 when it actually has an effect
// (drift detection); registering it as a no-op flag now would be a
// contract leak.
func resolveGitSHA() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// exitFn is overridable in tests so main_test.go can drive Run() and
// inspect exit codes without actually exiting the test process.
//
// In production this points at os.Exit; tests swap in a stub that
// captures the code.
var exitFn = os.Exit

// usageMessage is the short help printed on -h or on a flag-parse error.
// Lives near main so it's obvious what flags exist; keep in sync with
// spec §7.
const usageMessage = `Usage: replay [flags] <path>

  <path>  A bundle directory (containing 00-manifest.json) OR a parent
          directory containing one or more bundles (recursively walked).

Flags (R1 + R2 — full set lands in R3):
  --format string         Output format: text or json (default "text")
  --out string            Output path (default "-" for stdout)
  --allow-schema-drift    Treat schema-version mismatch as a warning instead of an error
  --allow-git-drift       Treat git_sha mismatch as a warning instead of an error
  --quiet                 Suppress per-bundle rows; only print the aggregate summary
  --verbose               Verbose per-field diff output (text mode)
  --from string           Gateway substitution mode: raw or parsed (default "raw")

Exit codes:
  0   All bundles validated and (in R2+) replayed within tolerance
  1   Reserved for R2: at least one bundle's diff exceeded tolerance
  2   Infrastructure failure (missing files, malformed manifest, schema drift without --allow-schema-drift)
`

// flags is the parsed flag set. Hoisted so tests can reset it cleanly.
type flags struct {
	format           string
	out              string
	allowSchemaDrift bool
	allowGitDrift    bool
	quiet            bool
	verbose          bool

	// from selects the gateway substitution mode for replay's bundle
	// gateways: "raw" (decode the captured HTTP-response bytes through
	// the production parser) or "parsed" (json.Unmarshal the
	// post-parse snapshot directly). Defaults to "raw" so a bare
	// `replay <bundle>` invocation runs the symmetric production-parser
	// path that exercises spec D3's invariant.
	from string
}

// parseFlags parses argv (without the program name). Returns the parsed
// flags struct, the positional path argument, and any error. Errors are
// either flag.ErrHelp (the user passed -h) or a usage error.
//
// Splitting parse from Run() lets main_test.go assert flag parsing
// independently of the rest of the orchestration.
func parseFlags(argv []string) (*flags, string, error) {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // suppress flag's stderr; we render usage ourselves

	f := &flags{}
	fs.StringVar(&f.format, "format", "text", "Output format (text|json)")
	fs.StringVar(&f.out, "out", "-", "Output destination (- for stdout)")
	fs.BoolVar(&f.allowSchemaDrift, "allow-schema-drift", false, "Warn instead of refusing on schema_versions mismatch")
	fs.BoolVar(&f.allowGitDrift, "allow-git-drift", false, "Warn instead of refusing on git_sha mismatch (R2)")
	fs.BoolVar(&f.quiet, "quiet", false, "Suppress per-bundle rows; only print the aggregate summary")
	fs.BoolVar(&f.verbose, "verbose", false, "Verbose per-field diff output (text mode)")
	fs.StringVar(&f.from, "from", "raw", "Gateway substitution mode (raw|parsed)")
	// --git-sha was previously registered but had no R1-side effect; it
	// would have been a contract leak (REVIEWER #11). It will return as
	// an operator override in R2 once git-drift detection actually
	// consumes it. R1 populates git_sha_current from
	// runtime/debug.ReadBuildInfo automatically (see resolveGitSHA).

	if err := fs.Parse(argv); err != nil {
		return nil, "", err
	}

	// --quiet and --verbose are mutually exclusive (spec §7).
	if f.quiet && f.verbose {
		return nil, "", fmt.Errorf("--quiet and --verbose are mutually exclusive")
	}

	if f.format != "text" && f.format != "json" {
		return nil, "", fmt.Errorf("--format must be text or json; got %q", f.format)
	}

	if f.from != "raw" && f.from != "parsed" {
		return nil, "", fmt.Errorf("--from must be raw or parsed; got %q", f.from)
	}

	args := fs.Args()
	if len(args) == 0 {
		return nil, "", fmt.Errorf("missing positional <path> argument")
	}
	if len(args) > 1 {
		return nil, "", fmt.Errorf("expected exactly one <path> argument; got %d", len(args))
	}
	return f, args[0], nil
}

// Run executes the parsed CLI against the supplied I/O. Extracted so
// main_test.go can drive the binary's logic with fake stdout/stderr.
//
// Returns an exit code suitable for os.Exit. Always non-blocking;
// safe to call repeatedly in tests.
func Run(argv []string, stdout, stderr io.Writer) int {
	f, path, err := parseFlags(argv)
	if err != nil {
		// flag.ErrHelp is the user explicitly requesting help — not an
		// error condition. Print usage to stdout and exit 0.
		if err == flag.ErrHelp {
			fmt.Fprint(stdout, usageMessage)
			return 0
		}
		fmt.Fprintf(stderr, "replay: %v\n\n%s", err, usageMessage)
		return 2
	}

	bundles, err := replay.WalkBundles(path)
	if err != nil {
		fmt.Fprintf(stderr, "replay: %v\n", err)
		return 2
	}

	// Build the report by walking each bundle and producing a Result.
	// Phase R1: the only outcome is SkeletonOK or Errored. R2 adds
	// Pass/Fail.
	report := &replay.Report{
		ReplayVersion: replay.ReplayVersion,
		GitSHACurrent: resolveGitSHA(),
		Verbose:       f.verbose,
	}

	for _, b := range bundles {
		res := evaluateBundle(b, f)
		report.Results = append(report.Results, res)
	}
	report.Summary = replay.ComputeSummary(report.Results)

	// Render to the configured destination.
	out, closeFn, err := openOutput(f.out, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "replay: open output %q: %v\n", f.out, err)
		return 2
	}
	defer closeFn()

	if f.quiet {
		// Quiet mode: skip per-bundle rows; show only the aggregate.
		// We still emit it via the renderer to keep the format
		// consistent — clear Results to an empty (non-nil) slice so the
		// JSON encoder emits "results": [] rather than "results": null.
		// The latter is a regression: downstream tooling (jq idioms,
		// type-stable consumers) expects a stable array shape. Pinned by
		// TestRun_QuietJSON_ResultsIsEmptyArray. R1 follow-up #8.
		quietReport := *report
		quietReport.Results = []replay.Result{}
		if err := renderReport(&quietReport, f.format, out); err != nil {
			fmt.Fprintf(stderr, "replay: render: %v\n", err)
			return 2
		}
	} else {
		if err := renderReport(report, f.format, out); err != nil {
			fmt.Fprintf(stderr, "replay: render: %v\n", err)
			return 2
		}
	}

	// Spec §5 D5: when schema drift is detected and --allow-schema-drift
	// was NOT passed, the mismatched table goes to stderr (separate from
	// the report on stdout / --out). Operators piping --out=foo.json to
	// jq must still see WHY replay refused without reading the file.
	// R1 follow-up #4.
	if !f.allowSchemaDrift {
		writeSchemaDriftDiagnostic(stderr, report.Results)
	}

	return report.ExitCode()
}

// writeSchemaDriftDiagnostic emits a focused, stderr-bound diagnostic for
// every bundle that was refused due to schema drift. Mirrors the per-row
// drift entries the text renderer would have emitted on stdout, but
// scoped to only the refused bundles so operators see the actionable
// detail even when --format=json is in use (where stdout is machine-
// parseable and not human-friendly) or when --out routes the report to a
// file.
//
// The emitted lines mirror writeResultRow's drift-detail format so a
// human eyeballing both streams sees consistent text.
func writeSchemaDriftDiagnostic(w io.Writer, results []replay.Result) {
	for _, r := range results {
		if r.Status != replay.StatusErrored || !r.SchemaDrift {
			continue
		}
		fmt.Fprintf(w, "replay: %s: %s\n", r.Bundle, r.Error)
		for _, e := range r.SchemaDriftEntries {
			fmt.Fprintf(w, "  - schema:%s  bundle=%d current=%d", e.Entity, e.BundleVersion, e.CurrentVersion)
			if e.MissingFromCurrent {
				fmt.Fprint(w, " (unknown to current code)")
			}
			if e.MissingFromBundle {
				fmt.Fprint(w, " (not stamped in bundle)")
			}
			fmt.Fprintln(w)
		}
	}
}

// evaluateBundle delegates to replay.Replay which runs the engine through
// the bundle gateways and diffs the result against 17-response.json.
//
// Skeleton-only fallback (preserves R1 behavior): bundles whose only
// content is 00-manifest.json (i.e. fixtures captured before R2 shipped
// or bundles produced by a server config that disabled artifact capture
// after manifest emission) are surfaced as StatusSkeletonOK rather than
// StatusErrored on engine failure. Detection is a presence-check on the
// canonical SEC raw/parsed file — the only required input the engine
// needs to produce a valuation. Without it, the engine has nothing to
// run, and "errored" misclassifies what is actually a manifest-only
// drift-detection bundle.
//
// Mode mapping: --from=raw → replay.ModeRaw (default); --from=parsed →
// replay.ModeParsed.
func evaluateBundle(bundleDir string, f *flags) replay.Result {
	if isSkeletonOnly(bundleDir) {
		return evaluateSkeletonBundle(bundleDir, f)
	}
	mode := replay.ModeRaw
	if f.from == "parsed" {
		mode = replay.ModeParsed
	}
	return replay.Replay(context.Background(), bundleDir, replay.Options{
		Mode:             mode,
		AllowSchemaDrift: f.allowSchemaDrift,
		AllowGitDrift:    f.allowGitDrift,
	})
}

// isSkeletonOnly reports whether the bundle directory contains only
// 00-manifest.json (no SEC, market, macro, or response payloads). When
// true, evaluateBundle uses the R1 SkeletonOK path; when false the full
// engine replay runs.
//
// Heuristic: check for the SEC raw/parsed file presence as a proxy for
// "the bundle has data to replay". The SEC fetch is the engine's
// foundational input — without it, every other gateway is downstream
// of an empty financial-data branch and the engine cannot proceed.
func isSkeletonOnly(bundleDir string) bool {
	for _, name := range []string{"05-fetch-sec.raw.json", "05-fetch-sec.parsed.json"} {
		if _, err := os.Stat(filepath.Join(bundleDir, name)); err == nil {
			return false
		}
	}
	return true
}

// evaluateSkeletonBundle preserves R1's manifest-only walk: read
// manifest, validate schema, return StatusSkeletonOK or StatusErrored
// for drift-without-flag.
func evaluateSkeletonBundle(bundleDir string, f *flags) replay.Result {
	res := replay.Result{
		Bundle: bundleDir,
		Status: replay.StatusSkeletonOK,
	}
	mf, err := replay.ReadManifest(bundleDir)
	if err != nil {
		res.Status = replay.StatusErrored
		res.Error = err.Error()
		return res
	}
	res.Ticker = mf.Ticker

	driftReport := replay.CompareManifestSchemas(mf)
	if driftReport.HasDrift() {
		res.SchemaDrift = true
		res.SchemaDriftEntries = driftReport.Entries
		if !f.allowSchemaDrift {
			res.Status = replay.StatusErrored
			res.Error = "schema drift detected (use --allow-schema-drift to proceed)"
			return res
		}
	}
	return res
}

// renderReport dispatches to the chosen format.
func renderReport(r *replay.Report, format string, out io.Writer) error {
	switch format {
	case "json":
		return r.RenderJSON(out)
	default:
		return r.RenderText(out)
	}
}

// openOutput resolves the --out path. "-" routes to the supplied default
// writer (typically os.Stdout). A real path opens a file for write,
// truncating. Returns the writer plus a close-fn the caller defers.
func openOutput(path string, def io.Writer) (io.Writer, func(), error) {
	if path == "" || path == "-" {
		return def, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { _ = f.Close() }, nil
}

func main() {
	code := Run(os.Args[1:], os.Stdout, os.Stderr)
	exitFn(code)
}
