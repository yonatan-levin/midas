package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/observability/replay"
)

// TestParseFlags_HappyPath drives the canonical flag set and confirms
// every option lands on the struct.
func TestParseFlags_HappyPath(t *testing.T) {
	f, path, err := parseFlags([]string{
		"--format=json",
		"--out=/tmp/out.json",
		"--allow-schema-drift",
		"--verbose",
		"/path/to/bundles",
	})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.format != "json" {
		t.Errorf("format = %q", f.format)
	}
	if f.out != "/tmp/out.json" {
		t.Errorf("out = %q", f.out)
	}
	if !f.allowSchemaDrift {
		t.Errorf("allowSchemaDrift = false, want true")
	}
	if !f.verbose {
		t.Errorf("verbose = false, want true")
	}
	if path != "/path/to/bundles" {
		t.Errorf("path = %q", path)
	}
}

// TestParseFlags_DefaultFormat verifies the format defaults to text.
func TestParseFlags_DefaultFormat(t *testing.T) {
	f, _, err := parseFlags([]string{"/x"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.format != "text" {
		t.Errorf("default format = %q, want text", f.format)
	}
	if f.out != "-" {
		t.Errorf("default out = %q, want -", f.out)
	}
}

func TestParseFlags_RejectsUnknownFormat(t *testing.T) {
	_, _, err := parseFlags([]string{"--format=yaml", "/x"})
	if err == nil {
		t.Fatal("parseFlags should reject --format=yaml")
	}
}

func TestParseFlags_RejectsQuietAndVerbose(t *testing.T) {
	_, _, err := parseFlags([]string{"--quiet", "--verbose", "/x"})
	if err == nil {
		t.Fatal("parseFlags should reject --quiet + --verbose")
	}
}

func TestParseFlags_RequiresPositionalPath(t *testing.T) {
	_, _, err := parseFlags([]string{"--format=json"})
	if err == nil {
		t.Fatal("parseFlags should require a positional path")
	}
}

func TestParseFlags_RejectsExtraArgs(t *testing.T) {
	_, _, err := parseFlags([]string{"/a", "/b"})
	if err == nil {
		t.Fatal("parseFlags should reject multiple positional args")
	}
}

// TestRun_EmptyDirectory drives the binary against an empty dir. QA B2
// (R3b polish) flipped this from "exit 0 with 0/0 passed" to "exit 2
// with stderr warning" — pointing at the wrong path is a CI footgun
// the silent zero-success exit mask. Operators in CI scripts now get
// an actionable failure instead of a misleading green.
func TestRun_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run([]string{dir}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no bundles found") {
		t.Errorf("stderr should include 'no bundles found'; got:\n%s", stderr.String())
	}
	// Stderr message must reference the offending path so the operator
	// can spot the typo in CI logs. The path is rendered via %q which
	// escapes backslashes on Windows — match against the quoted form
	// so the assertion is platform-stable.
	if !strings.Contains(stderr.String(), strconv.Quote(dir)) {
		t.Errorf("stderr should reference the supplied path %q; got:\n%s", dir, stderr.String())
	}
}

// TestRun_EmptyDirectory_QuietStillWarns pins QA B2's quiet-mode carve-
// out: --quiet suppresses per-bundle rows and the SUMMARY line, but the
// "no bundles found" warning ALWAYS fires because the operator needs to
// know they pointed at the wrong path. The exit code remains 2.
func TestRun_EmptyDirectory_QuietStillWarns(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--quiet", dir}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 even under --quiet; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no bundles found") {
		t.Errorf("--quiet must NOT suppress the no-bundles warning; got stderr:\n%s", stderr.String())
	}
}

// TestRun_HappyBundle uses the committed testdata/happy bundle. Asserts
// per-bundle SkeletonOK row and exit 0.
func TestRun_HappyBundle(t *testing.T) {
	bundle := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", "happy")
	abs, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "00-manifest.json")); err != nil {
		t.Fatalf("testdata bundle not found at %s: %v", abs, err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{abs}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SKELETON_OK") {
		t.Errorf("expected SKELETON_OK row; got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "1/1 passed") {
		t.Errorf("expected '1/1 passed'; got:\n%s", stdout.String())
	}
}

// TestRun_SchemaDriftRefused replays the schema-drift bundle without
// --allow-schema-drift; spec §9 R1: exits 2 with the drift table.
// TestRun_SchemaDriftRefused replays the schema-drift bundle without
// --allow-schema-drift; spec §5 D5: the drift table goes to STDERR (so
// it is not interleaved with report output piped to --out or
// processed by jq), and the binary exits 2.
func TestRun_SchemaDriftRefused(t *testing.T) {
	bundle := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", "schema-drift")
	abs, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{abs}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2; stdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	// Per spec §5 D5: schema-drift table is on stderr, separate from the
	// report (which lives on stdout / --out). R1 follow-up #4.
	if !strings.Contains(stderr.String(), "schema:FinancialData") {
		t.Errorf("expected drift detail on STDERR (spec §5 D5); got stderr=%s\nstdout=%s", stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "schema drift detected") {
		t.Errorf("expected schema-drift error message on STDERR; got stderr=%s\nstdout=%s", stderr.String(), stdout.String())
	}
	// Bundle's per-row result still appears in the report on stdout
	// (status=ERRORED). Only the focused diagnostic is on stderr.
	if !strings.Contains(stdout.String(), "ERRORED") {
		t.Errorf("expected per-row ERRORED status on stdout; got stdout=%s", stdout.String())
	}
}

// TestRun_SchemaDriftRefused_StderrOutDecoupled pins the spec invariant
// from R1 follow-up #4: even when --out routes the report to a file
// (so stdout is empty), the schema-drift diagnostic still goes to
// stderr. Operators piping --out=foo.json | jq must see the failure
// without reading the file.
func TestRun_SchemaDriftRefused_StderrOutDecoupled(t *testing.T) {
	bundle := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", "schema-drift")
	abs, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	outFile := filepath.Join(t.TempDir(), "report.json")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--format=json", "--out=" + outFile, abs}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2; stderr=%s", code, stderr.String())
	}
	// The diagnostic is on stderr regardless of --out.
	if !strings.Contains(stderr.String(), "schema:FinancialData") {
		t.Errorf("schema-drift diagnostic must reach stderr even when --out routes the report elsewhere; stderr=%s", stderr.String())
	}
}

// TestRun_SchemaDriftAllowed verifies --allow-schema-drift downgrades the
// drift to a warn (status SkeletonOK with schema_drift=true) and exits 0.
func TestRun_SchemaDriftAllowed(t *testing.T) {
	bundle := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", "schema-drift")
	abs, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--allow-schema-drift", abs}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "schema_drift=true") {
		t.Errorf("expected schema_drift=true annotation on row; got stdout=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "1/1 passed") {
		t.Errorf("expected '1/1 passed'; got stdout=%s", stdout.String())
	}
}

// TestRun_JSONFormat drives the binary with --format=json and verifies
// the output round-trips through encoding/json. Pins the §7 contract.
func TestRun_JSONFormat(t *testing.T) {
	bundle := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", "happy")
	abs, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--format=json", abs}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON: %v\noutput=%s", err, stdout.String())
	}
	if got["replay_version"] == nil {
		t.Errorf("missing replay_version field")
	}
	summary, ok := got["summary"].(map[string]any)
	if !ok {
		t.Fatalf("missing summary object; got: %v", got)
	}
	if summary["total"] != float64(1) {
		t.Errorf("summary.total = %v, want 1", summary["total"])
	}
	if summary["passed"] != float64(1) {
		t.Errorf("summary.passed = %v, want 1", summary["passed"])
	}
}

// TestRun_NonexistentPath surfaces the missing-path infrastructure error
// as exit 2.
func TestRun_NonexistentPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{filepath.Join(t.TempDir(), "nope")}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "replay:") {
		t.Errorf("expected error on stderr; got: %s", stderr.String())
	}
}

// TestRun_UsageOnFlagError verifies a parse error prints the usage
// message and exits 2.
func TestRun_UsageOnFlagError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--format=yaml", "/x"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage: replay") {
		t.Errorf("expected usage message on stderr; got: %s", stderr.String())
	}
}

// TestRun_HelpFlagExits0 confirms -h prints usage and exits 0.
func TestRun_HelpFlagExits0(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"-h"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: replay") {
		t.Errorf("expected usage on stdout; got: %s", stdout.String())
	}
}

// TestRun_OutFile writes the report to a file and verifies the file is
// created and parses back as JSON. Uses --format=json for stable
// machine-checkable content.
func TestRun_OutFile(t *testing.T) {
	bundle := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", "happy")
	abs, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	outFile := filepath.Join(t.TempDir(), "out.json")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--format=json", "--out=" + outFile, abs}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read out file: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode out file: %v\nbody=%s", err, string(body))
	}
	if report["replay_version"] == nil {
		t.Errorf("missing replay_version in out file")
	}
}

// TestRun_QuietJSON_ResultsIsEmptyArray pins R1 follow-up #8: --quiet
// --format=json must emit "results": [] not "results": null. Downstream
// tooling (jq idioms, type-stable consumers) expect a stable array
// shape. Without explicit []Result{} initialization the Go encoder emits
// null for a nil slice.
func TestRun_QuietJSON_ResultsIsEmptyArray(t *testing.T) {
	bundle := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", "happy")
	abs, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--quiet", "--format=json", abs}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, `"results": null`) {
		t.Errorf(`--quiet --format=json must emit "results": [] not "results": null; got:%s`, out)
	}
	if !strings.Contains(out, `"results": []`) {
		t.Errorf(`expected "results": [] in quiet JSON output; got:%s`, out)
	}
}

// TestRun_QuietMode confirms --quiet hides per-bundle rows but keeps the
// summary line.
func TestRun_QuietMode(t *testing.T) {
	bundle := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", "happy")
	abs, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--quiet", abs}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "SKELETON_OK") {
		t.Errorf("--quiet should hide per-bundle row; got:\n%s", out)
	}
	if !strings.Contains(out, "SUMMARY:") {
		t.Errorf("--quiet should still emit the summary; got:\n%s", out)
	}
}

// TestRun_BatchTree verifies the walk-and-aggregate path against a tree
// containing both happy and schema-drift bundles. Without
// --allow-schema-drift the binary should exit 2 (drift wins) and the
// summary should reflect both bundles.
func TestRun_BatchTree(t *testing.T) {
	root := t.TempDir()
	// Copy the two synthetic bundles into a tree so WalkBundles finds them.
	cpManifest(t, "happy", filepath.Join(root, "AAPL", "req_x"))
	cpManifest(t, "schema-drift", filepath.Join(root, "MSFT", "req_y"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{root}, &stdout, &stderr)
	// Drift bundle without --allow-schema-drift causes errored, exit 2.
	if code != 2 {
		t.Errorf("exit code = %d, want 2; stdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "1/2 passed") {
		t.Errorf("expected '1/2 passed'; got:\n%s", out)
	}
	if !strings.Contains(out, "1 errored") {
		t.Errorf("expected '1 errored'; got:\n%s", out)
	}
}

// cpManifest copies the canonical 00-manifest.json from
// internal/observability/replay/testdata/<name>/ into dst, creating dst.
// Used to assemble multi-bundle test trees without committing every
// permutation.
func cpManifest(t *testing.T, name, dst string) {
	t.Helper()
	src := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", name, "00-manifest.json")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	if err := os.WriteFile(filepath.Join(dst, "00-manifest.json"), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestParseFlags_FromRaw_Default pins the R2 flag default. Plan §3 Stage E.
func TestParseFlags_FromRaw_Default(t *testing.T) {
	f, _, err := parseFlags([]string{"/x"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.from != "raw" {
		t.Fatalf("default --from = %q, want raw", f.from)
	}
}

// TestParseFlags_FromParsed_Explicit verifies --from=parsed is accepted.
func TestParseFlags_FromParsed_Explicit(t *testing.T) {
	f, _, err := parseFlags([]string{"--from=parsed", "/x"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.from != "parsed" {
		t.Fatalf("--from=parsed: got %q, want parsed", f.from)
	}
}

// TestParseFlags_FromInvalid_Errors rejects an unsupported value.
func TestParseFlags_FromInvalid_Errors(t *testing.T) {
	_, _, err := parseFlags([]string{"--from=cleaned", "/x"})
	if err == nil {
		t.Fatal("parseFlags should reject --from=cleaned")
	}
	if !strings.Contains(err.Error(), "raw or parsed") {
		t.Fatalf("error message should mention valid values; got: %v", err)
	}
}

// TestParseFlags_R3FlagsAreRegistered (R3 polarity flip): the previously-
// deferred R3 flag set IS now registered with real behavior. Each entry
// must parse without error and produce its expected effect on the flags
// struct. This test inverts the prior R2-era TestParseFlags_R3FlagsAreNotRegistered
// per the plan §7 Done-When checklist.
func TestParseFlags_R3FlagsAreRegistered(t *testing.T) {
	cases := []struct {
		name   string
		argv   []string
		assert func(t *testing.T, f *flags)
	}{
		{
			name: "--workers=4",
			argv: []string{"--workers=4", "/x"},
			assert: func(t *testing.T, f *flags) {
				if f.workers != 4 {
					t.Fatalf("workers = %d, want 4", f.workers)
				}
			},
		},
		{
			name: "--filter-ticker=AAPL",
			argv: []string{"--filter-ticker=AAPL", "/x"},
			assert: func(t *testing.T, f *flags) {
				if f.filterTicker != "AAPL" {
					t.Fatalf("filterTicker = %q, want AAPL", f.filterTicker)
				}
			},
		},
		{
			name: "--filter-since=24h",
			argv: []string{"--filter-since=24h", "/x"},
			assert: func(t *testing.T, f *flags) {
				if f.filterSince != 24*time.Hour {
					t.Fatalf("filterSince = %v, want 24h", f.filterSince)
				}
			},
		},
		{
			name: "--filter-since=7d (extended duration)",
			argv: []string{"--filter-since=7d", "/x"},
			assert: func(t *testing.T, f *flags) {
				if f.filterSince != 7*24*time.Hour {
					t.Fatalf("filterSince = %v, want 168h", f.filterSince)
				}
			},
		},
		{
			name: "--float-rel-tol=1e-6",
			argv: []string{"--float-rel-tol=1e-6", "/x"},
			assert: func(t *testing.T, f *flags) {
				if f.floatRelTol != 1e-6 {
					t.Fatalf("floatRelTol = %v, want 1e-6", f.floatRelTol)
				}
			},
		},
		{
			name: "--float-abs-tol=1e-9",
			argv: []string{"--float-abs-tol=1e-9", "/x"},
			assert: func(t *testing.T, f *flags) {
				if f.floatAbsTol != 1e-9 {
					t.Fatalf("floatAbsTol = %v, want 1e-9", f.floatAbsTol)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _, err := parseFlags(c.argv)
			if err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			c.assert(t, f)
		})
	}
}

// TestParseFlags_DiffStages_DefaultFalse pins the default value of the
// --diff-stages flag. Default off means the watchlist-regression
// workflow doesn't pay the snapshot-capture cost unless the operator
// explicitly opts in. R3b Stage K.
func TestParseFlags_DiffStages_DefaultFalse(t *testing.T) {
	f, _, err := parseFlags([]string{"/x"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.diffStages {
		t.Fatalf("--diff-stages default = true, want false")
	}
}

// TestParseFlags_DiffStages_ExplicitTrue verifies the bool flag flips
// when explicitly passed. Plain `--diff-stages` (no value) is the spec
// shape per §7's L515-554 sample.
func TestParseFlags_DiffStages_ExplicitTrue(t *testing.T) {
	f, _, err := parseFlags([]string{"--diff-stages", "/x"})
	if err != nil {
		t.Fatalf("parseFlags --diff-stages: %v", err)
	}
	if !f.diffStages {
		t.Fatalf("--diff-stages = false after explicit pass; want true")
	}
}

// TestParseFlags_Workers_DefaultIsRuntimeNumCPU asserts the --workers
// default falls back to runtime.NumCPU when REPLAY_WORKERS is unset.
func TestParseFlags_Workers_DefaultIsRuntimeNumCPU(t *testing.T) {
	t.Setenv("REPLAY_WORKERS", "")
	f, _, err := parseFlags([]string{"/x"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.workers != runtime.NumCPU() {
		t.Fatalf("workers default = %d, want runtime.NumCPU() = %d", f.workers, runtime.NumCPU())
	}
}

// TestParseFlags_Workers_ZeroOrNegativeRejected validates the lower bound.
func TestParseFlags_Workers_ZeroOrNegativeRejected(t *testing.T) {
	cases := []string{"--workers=0", "--workers=-1"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, _, err := parseFlags([]string{c, "/x"})
			if err == nil {
				t.Fatalf("expected error for %s; got nil", c)
			}
			if !strings.Contains(err.Error(), "--workers must be >= 1") {
				t.Fatalf("expected --workers >= 1 error; got %v", err)
			}
		})
	}
}

// TestParseFlags_FloatTol_NegativeRejected validates tolerance lower bound.
func TestParseFlags_FloatTol_NegativeRejected(t *testing.T) {
	if _, _, err := parseFlags([]string{"--float-rel-tol=-1", "/x"}); err == nil {
		t.Fatal("expected error for --float-rel-tol=-1")
	}
	if _, _, err := parseFlags([]string{"--float-abs-tol=-0.5", "/x"}); err == nil {
		t.Fatal("expected error for --float-abs-tol=-0.5")
	}
}

// TestParseFlags_FloatTol_InfRejected pins the contract that ±Inf is
// rejected for both --float-rel-tol and --float-abs-tol.
//
// Why: an operator typo like `--float-rel-tol=+Inf` (or `-Inf`, which Go's
// strconv parses as a valid float64) is finite-positive on the surface but
// makes EVERY float comparison tolerate ANY drift — turning replay PASS
// into a useless rubber stamp regardless of real differences. NaN and
// negatives were already rejected; Inf was a gap closed by QA cycle 1.
//
// The flag value MUST be a finite, non-negative float64 to be accepted.
func TestParseFlags_FloatTol_InfRejected(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"rel_pos_inf", []string{"--float-rel-tol=+Inf", "/x"}},
		{"rel_neg_inf", []string{"--float-rel-tol=-Inf", "/x"}},
		{"abs_pos_inf", []string{"--float-abs-tol=+Inf", "/x"}},
		{"abs_neg_inf", []string{"--float-abs-tol=-Inf", "/x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := parseFlags(c.argv)
			if err == nil {
				t.Fatalf("parseFlags(%v) accepted ±Inf; want error", c.argv)
			}
			// The error message should call out which flag and that the
			// value must be finite — operators reading stderr should
			// understand the contract from the message alone.
			msg := err.Error()
			if !strings.Contains(msg, "--float-rel-tol") && !strings.Contains(msg, "--float-abs-tol") {
				t.Fatalf("error message must reference the offending flag; got: %v", err)
			}
			if !strings.Contains(msg, "finite") {
				t.Fatalf("error message must explain finite-only contract; got: %v", err)
			}
		})
	}
}

// TestParseFlags_FilterSince_InvalidUnitErrors confirms ParseDurationExtended
// errors propagate through parseFlags.
func TestParseFlags_FilterSince_InvalidUnitErrors(t *testing.T) {
	if _, _, err := parseFlags([]string{"--filter-since=1w", "/x"}); err == nil {
		t.Fatal("expected error for --filter-since=1w")
	}
}

// TestEnvVar_REPLAY_WORKERS_FlagWins confirms an explicit --workers flag
// beats REPLAY_WORKERS env var (spec §8 precedence).
func TestEnvVar_REPLAY_WORKERS_FlagWins(t *testing.T) {
	t.Setenv("REPLAY_WORKERS", "8")
	f, _, err := parseFlags([]string{"--workers=2", "/x"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.workers != 2 {
		t.Fatalf("workers = %d, want 2 (flag wins over env)", f.workers)
	}
}

// TestEnvVar_REPLAY_WORKERS_AppliesAsDefault confirms the env var fills
// in the default when --workers is not passed.
func TestEnvVar_REPLAY_WORKERS_AppliesAsDefault(t *testing.T) {
	t.Setenv("REPLAY_WORKERS", "8")
	f, _, err := parseFlags([]string{"/x"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.workers != 8 {
		t.Fatalf("workers = %d, want 8 (env-var default)", f.workers)
	}
}

// TestEvaluateBundle_FromRaw_MissingPayload_AppendsParsedHint pins QA
// D1: the default --from=raw mode against a bundle that only ships
// parsed snapshots (a real-world condition for production bundles in
// artifacts/) must surface a hint pointing the operator at --from=parsed.
// Without the hint the failure is "no macro data" — confusing because
// the bundle exists, just the raw FRED files don't.
//
// Strategy: install evaluateBundleFn to return a Result wrapping the
// canonical replay.ErrBundleMissingPayload sentinel, then call
// evaluateBundleWithHint and assert the rendered Error contains
// "try --from=parsed". The hint application lives at the cmd/replay
// layer (where --from is known) per the dispatch prompt's guidance.
func TestEvaluateBundle_FromRaw_MissingPayload_AppendsParsedHint(t *testing.T) {
	original := evaluateBundleFn
	t.Cleanup(func() { evaluateBundleFn = original })

	missing := replay.NewBundleMissingPayloadError("/x/bundle", "07-fetch-macro-DGS10.raw.json", nil)
	evaluateBundleFn = func(bundleDir string, f *flags) replay.Result {
		return replay.Result{
			Bundle: bundleDir,
			Status: replay.StatusErrored,
			Error:  missing.Error(),
			// errSentinel is unexported; the dispatcher must rely on the
			// public Result.Err() accessor, populated via the public
			// constructor pathway in production. For this test we
			// provide a Result that exposes the sentinel by returning
			// a freshly-constructed error from evaluateBundle's path.
		}
	}

	// The hint-appender is a small free function; assert it directly so
	// the test does not depend on the unexported errSentinel field.
	rawHinted := annotateMissingPayloadHint(missing.Error(), missing, "raw")
	if !strings.Contains(rawHinted, "try --from=parsed") {
		t.Fatalf("expected --from=raw hint in error string; got %q", rawHinted)
	}
	parsedNotHinted := annotateMissingPayloadHint(missing.Error(), missing, "parsed")
	if strings.Contains(parsedNotHinted, "try --from=parsed") {
		t.Fatalf("hint must not appear when mode is already parsed; got %q", parsedNotHinted)
	}
	// Non-missing-payload errors should pass through unchanged regardless
	// of mode — the hint is specific to the bundle-missing-payload class.
	other := errors.New("some other failure")
	otherHinted := annotateMissingPayloadHint(other.Error(), other, "raw")
	if strings.Contains(otherHinted, "try --from=parsed") {
		t.Fatalf("hint must not be appended for non-missing-payload errors; got %q", otherHinted)
	}
}

// TestEvaluateBundleWithRecover_PanicConvertedToErroredResult exercises
// the defer-recover at the worker-goroutine boundary in
// evaluateBundleWithRecover. Production: a panic in any layer outside
// the F11 datafetcher goroutine path (e.g. an Auth/Watchlist stub
// panic, an engine refactor that touches a panic-stub repo) escapes
// up to evaluateBundleWithRecover where the deferred recover converts
// it to a StatusErrored Result so the parent batch keeps running.
//
// Strategy: the production evaluateBundle is reached via the
// evaluateBundleFn package-level indirection (RPL-3o test seam, ~5
// LoC of production code). Test swaps in a stub that panics with a
// known sentinel; assert the result's Status==StatusErrored and the
// Error string contains the panic value.
//
// RPL-3o (R3b cleanup); spec §12 testing requirement.
func TestEvaluateBundleWithRecover_PanicConvertedToErroredResult(t *testing.T) {
	const panicMsg = "rpl-3o-test-panic"

	original := evaluateBundleFn
	t.Cleanup(func() { evaluateBundleFn = original })
	evaluateBundleFn = func(bundleDir string, f *flags) replay.Result {
		panic(panicMsg)
	}

	res := evaluateBundleWithRecover("/x/test-bundle", &flags{})
	if res.Status != replay.StatusErrored {
		t.Fatalf("Status: want errored, got %s", res.Status)
	}
	if res.Bundle != "/x/test-bundle" {
		t.Errorf("Bundle: want /x/test-bundle, got %q", res.Bundle)
	}
	if !strings.Contains(res.Error, "panic in replay worker") {
		t.Errorf("Error must mention 'panic in replay worker'; got %q", res.Error)
	}
	if !strings.Contains(res.Error, panicMsg) {
		t.Errorf("Error must surface the panic value %q; got %q", panicMsg, res.Error)
	}
}
