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
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"time"

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

Flags:
  --format string         Output format: text or json (default "text")
  --out string            Output path (default "-" for stdout)
  --allow-schema-drift    Treat schema-version mismatch as a warning instead of an error
  --allow-git-drift       Treat git_sha mismatch as a warning instead of an error
  --quiet                 Suppress per-bundle rows; only print the aggregate summary
  --verbose               Verbose per-field diff output (text mode)
  --from string           Gateway substitution mode: raw or parsed (default "raw")
  --workers int           Parallel replay workers (default runtime.NumCPU(); env REPLAY_WORKERS)
  --filter-ticker string  Replay only bundles whose manifest ticker == this string (exact-case)
  --filter-since string   Replay only bundles whose manifest started_at is within this duration of now (e.g. 7d, 24h)
  --diff-stages           Diff intermediate-stage JSON files (10-clean-output, 12-growth-curve, 13-wacc, 15-valuation) in addition to the response-level diff
  --float-rel-tol float   Relative tolerance for float diffs (default 1e-9; 0 means use default, NOT exact-match)
  --float-abs-tol float   Absolute tolerance for float diffs (default 1e-12; 0 means use default, NOT exact-match)

Exit codes:
  0   All bundles validated and replayed within tolerance
  1   At least one bundle's diff exceeded tolerance
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

	// workers is the parallel-dispatch fan-out for the per-bundle
	// replay loop. Default is runtime.NumCPU(); REPLAY_WORKERS env var
	// overrides the default but is itself overridden by an explicit
	// --workers flag. Validation: must be >= 1.
	workers int

	// filterTicker, when non-empty, restricts the replay to bundles
	// whose manifest.ticker exactly equals this string. Case-sensitive
	// (tickers are conventionally uppercase in the system; mismatches
	// are intentionally a no-op rather than a fuzzy-matched surprise).
	filterTicker string

	// filterSinceRaw is the user-supplied --filter-since string. It is
	// parsed via replay.ParseDurationExtended (which accepts the Go
	// stdlib syntax plus the "d" days unit) into filterSince after
	// flag.Parse returns. A bundle is included iff manifest.started_at
	// is within filterSince of time.Now() — boundary inclusive.
	filterSinceRaw string
	filterSince    time.Duration

	// diffStages enables per-stage JSON diff (Stage K). When true, the
	// orchestrator captures the engine's intermediate snapshots
	// (10-clean-output.json, 12-growth-curve.json, 13-wacc.json,
	// 15-valuation.json) into an ephemeral bundle and diffs them against
	// the bundle's recorded versions. Off by default to keep the
	// watchlist-regression workflow as fast as the response-only diff.
	diffStages bool

	// floatRelTol / floatAbsTol override the default tolerances used by
	// the diff layer. Defaults map to replay.DefaultFloatRelTol /
	// replay.DefaultFloatAbsTol. Negative or NaN values are rejected at
	// parse time.
	floatRelTol float64
	floatAbsTol float64
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

	// R3 flags. --workers default resolution: REPLAY_WORKERS env var if
	// set and parseable; otherwise runtime.NumCPU(). The CLI flag, when
	// passed explicitly, beats both. We register the flag with the env-
	// derived default so `flag.Parse` still treats an explicit
	// --workers=N as an override (flag's IntVar has no notion of
	// "explicitly set"; the default-precedence chain matches spec §8).
	defaultWorkers := defaultWorkerCount()
	fs.IntVar(&f.workers, "workers", defaultWorkers, "Parallel replay workers (default runtime.NumCPU(); env REPLAY_WORKERS)")
	fs.StringVar(&f.filterTicker, "filter-ticker", "", "Replay only bundles whose manifest ticker == this string (exact-case)")
	fs.StringVar(&f.filterSinceRaw, "filter-since", "", "Replay only bundles whose manifest started_at is within this duration of now (e.g. 7d, 24h)")
	fs.BoolVar(&f.diffStages, "diff-stages", false, "Diff intermediate-stage JSON files in addition to the response-level diff (Stage K, Phase 2.D R3b)")
	fs.Float64Var(&f.floatRelTol, "float-rel-tol", replay.DefaultFloatRelTol, "Relative tolerance for float diffs")
	fs.Float64Var(&f.floatAbsTol, "float-abs-tol", replay.DefaultFloatAbsTol, "Absolute tolerance for float diffs")

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

	if f.workers < 1 {
		return nil, "", fmt.Errorf("--workers must be >= 1; got %d", f.workers)
	}

	// Validate float tolerances. The flag value must be a finite,
	// non-negative float64. Three failure modes to reject:
	//   - negative (e.g. `--float-rel-tol=-0.01`): silently flips
	//     comparisons; nonsensical.
	//   - NaN (e.g. `--float-rel-tol=NaN`): every comparison fails the
	//     ordering check; equally nonsensical.
	//   - ±Inf (e.g. `--float-rel-tol=+Inf`): every comparison falls
	//     within tolerance, turning replay PASS into a rubber stamp
	//     regardless of real drift. This is the operator-typo class bug
	//     QA cycle 1 caught before it could mask a real regression.
	// Use math.IsInf(_, 0) to catch BOTH +Inf and -Inf in a single check.
	if math.IsNaN(f.floatRelTol) || math.IsInf(f.floatRelTol, 0) || f.floatRelTol < 0 {
		return nil, "", fmt.Errorf("--float-rel-tol must be a finite, non-negative number; got %v", f.floatRelTol)
	}
	if math.IsNaN(f.floatAbsTol) || math.IsInf(f.floatAbsTol, 0) || f.floatAbsTol < 0 {
		return nil, "", fmt.Errorf("--float-abs-tol must be a finite, non-negative number; got %v", f.floatAbsTol)
	}

	// Parse --filter-since via replay.ParseDurationExtended (handles the
	// "d" days unit on top of Go's stdlib syntax). Empty string disables
	// the filter (filterSince stays at the zero value).
	if f.filterSinceRaw != "" {
		d, err := replay.ParseDurationExtended(f.filterSinceRaw)
		if err != nil {
			return nil, "", fmt.Errorf("--filter-since: %w", err)
		}
		f.filterSince = d
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

// defaultWorkerCount resolves the --workers default. Precedence per
// spec §8: explicit --workers flag (caller's responsibility) beats
// REPLAY_WORKERS env var beats runtime.NumCPU(). This helper handles
// only the env-vs-NumCPU half; the flag-vs-this half is driven by the
// flag library treating an explicit argument as an override.
//
// Malformed / non-positive REPLAY_WORKERS values fall back to NumCPU
// silently — env-var typos shouldn't produce a hard error at flag-set
// construction time.
func defaultWorkerCount() int {
	if raw, ok := os.LookupEnv("REPLAY_WORKERS"); ok {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 {
			return n
		}
	}
	return runtime.NumCPU()
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

	walkStart := time.Now()
	bundles, err := replay.WalkBundles(path)
	if err != nil {
		fmt.Fprintf(stderr, "replay: %v\n", err)
		return 2
	}
	walkDuration := time.Since(walkStart)

	// Apply --filter-ticker / --filter-since AFTER the walk and BEFORE
	// the dispatch. Filtering is policy that doesn't belong in WalkBundles
	// (which is intentionally narrow); doing it here also lets us avoid
	// constructing fx apps for filtered-out bundles.
	bundles = applyFilters(bundles, f, stderr)

	// Build the report. Per spec §7 the JSON contract is stable; we
	// populate it with the per-bundle Results from a parallel dispatcher
	// when --workers > 1, or sequentially otherwise.
	report := &replay.Report{
		ReplayVersion: replay.ReplayVersion,
		GitSHACurrent: resolveGitSHA(),
		Verbose:       f.verbose,
	}

	replayStart := time.Now()
	report.Results = dispatchReplay(bundles, f)
	replayDuration := time.Since(replayStart)

	// Sort by bundle path so output ordering is deterministic regardless
	// of worker-completion order. RenderText/RenderJSON also sort, but
	// pinning the order here keeps ComputeSummary's per-result iteration
	// stable too.
	sort.Slice(report.Results, func(i, j int) bool {
		return report.Results[i].Bundle < report.Results[j].Bundle
	})

	report.Summary = replay.ComputeSummary(report.Results)
	report.Summary.WalkDurationMs = walkDuration.Milliseconds()
	report.Summary.ReplayDurationMs = replayDuration.Milliseconds()

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

// dispatchReplay runs evaluateBundle for every bundle either sequentially
// (--workers == 1) or in a bounded goroutine pool (--workers > 1).
//
// Determinism: result ordering is NOT preserved by the parallel path; the
// caller sorts by Bundle after collection. RenderText / RenderJSON also
// sort, so stdout is byte-identical between --workers=1 and --workers=N
// for the same bundle set.
//
// Hermeticity (F11): each goroutine constructs its own replay.Module fx
// app via Replay() — no shared mutable state across workers. Bundle
// gateway structs are immutable post-construction (R2 invariant).
//
// Panic recovery: each worker has a defer-recover that converts a panic
// into a StatusErrored Result with a "panic in replay worker" diagnostic.
// This catches an Auth/Watchlist stub panic (allowed per F11; sits OUTSIDE
// the F11 goroutine path) without crashing the whole batch.
func dispatchReplay(bundles []string, f *flags) []replay.Result {
	if len(bundles) == 0 {
		return nil
	}

	// Sequential fast path: --workers=1 preserves R0+R1+R2 behavior
	// bit-for-bit. Removes any pool-construction overhead for trivial
	// runs and keeps the deterministic-stdout property (which the
	// post-collect sort would also enforce, but the explicit branch is
	// simpler to reason about).
	if f.workers <= 1 {
		out := make([]replay.Result, 0, len(bundles))
		for _, b := range bundles {
			out = append(out, evaluateBundleWithRecover(b, f))
		}
		return out
	}

	// Bounded goroutine pool. Capacity = f.workers; a buffered channel of
	// the same capacity acts as the semaphore. Each worker reads one job,
	// runs evaluateBundle, writes its Result into a per-index slot. We
	// avoid an unbounded results channel because the input size is known
	// (len(bundles)) — direct slot-write is simpler than a fan-in
	// goroutine.
	results := make([]replay.Result, len(bundles))
	sem := make(chan struct{}, f.workers)
	var wg sync.WaitGroup

	for i, b := range bundles {
		// Go 1.23.0 (per go.mod): per-iteration loop semantics are in
		// effect, so a closure capturing i/b sees fresh values without
		// the historical `i, b := i, b` shadow. RPL-3g (R3b cleanup).
		wg.Add(1)
		sem <- struct{}{} // acquire slot
		go func() {
			defer wg.Done()
			defer func() { <-sem }() // release slot
			results[i] = evaluateBundleWithRecover(b, f)
		}()
	}
	wg.Wait()
	return results
}

// evaluateBundleWithRecover wraps evaluateBundle with a defer-recover so
// a panic in any layer of the engine path (Auth/Watchlist stubs, an
// engine refactor that accidentally touches a panic-stub repo, etc.)
// becomes a StatusErrored Result instead of crashing the binary mid-batch.
//
// F11 invariant unchanged: bundle gateways still return
// replay.ErrBundleMissingPayload (NOT panic) on missing files because
// they sit on coordinator goroutines. This recover is a defense-in-depth
// for layers OUTSIDE the F11 boundary.
func evaluateBundleWithRecover(bundleDir string, f *flags) (res replay.Result) {
	defer func() {
		if r := recover(); r != nil {
			res = replay.Result{
				Bundle: bundleDir,
				Status: replay.StatusErrored,
				Error:  fmt.Sprintf("panic in replay worker: %v", r),
			}
		}
	}()
	return evaluateBundleFn(bundleDir, f)
}

// evaluateBundleFn is the package-level indirection so tests can
// install a panic stub and exercise evaluateBundleWithRecover's
// `defer recover()` path. Production wires it to evaluateBundle.
//
// Test-only seam (RPL-3o): main_test.go's
// TestEvaluateBundleWithRecover_PanicConvertedToErroredResult swaps
// this var with a panicking stub, calls evaluateBundleWithRecover,
// and asserts the recover converted the panic to a StatusErrored
// Result without crashing the binary. The seam is 1 line of
// production code (the var declaration) — the alternative would be
// an unreachable test-only branch inside evaluateBundle, which is
// dirtier.
var evaluateBundleFn = evaluateBundle

// applyFilters filters the bundle list by --filter-ticker and
// --filter-since. Both filters peek the manifest (cheap; <1 KiB read);
// failed manifest reads are propagated through to evaluateBundle which
// surfaces them as a StatusErrored Result — we don't want a malformed
// manifest to silently disappear from the report just because a filter
// couldn't read it.
func applyFilters(bundles []string, f *flags, stderr io.Writer) []string {
	if f.filterTicker == "" && f.filterSince == 0 {
		return bundles
	}
	out := make([]string, 0, len(bundles))
	now := time.Now()
	for _, b := range bundles {
		mf, err := replay.ReadManifest(b)
		if err != nil {
			// Couldn't read manifest — keep the bundle so evaluateBundle
			// surfaces the failure as StatusErrored. Matches the principle
			// "filters narrow the inclusion set; they don't suppress
			// errors".
			out = append(out, b)
			continue
		}
		if f.filterTicker != "" && mf.Ticker != f.filterTicker {
			continue
		}
		if f.filterSince > 0 && mf.StartedAt != "" {
			// Boundary inclusive: a bundle exactly at now-filterSince is
			// included (Sub <= filterSince). A malformed StartedAt is
			// surfaced as a stderr WARN and the bundle passes through —
			// we don't want a manifest-corruption case to silently
			// vanish from the report.
			started, err := time.Parse(time.RFC3339Nano, mf.StartedAt)
			if err != nil {
				started, err = time.Parse(time.RFC3339, mf.StartedAt)
			}
			if err != nil {
				fmt.Fprintf(stderr, "replay: %s: malformed manifest started_at %q: %v (bundle kept)\n", b, mf.StartedAt, err)
			} else if now.Sub(started) > f.filterSince {
				continue
			}
		}
		out = append(out, b)
	}
	return out
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
		FloatRelTol:      f.floatRelTol,
		FloatAbsTol:      f.floatAbsTol,
		DiffStages:       f.diffStages,
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
