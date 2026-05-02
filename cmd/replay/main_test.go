package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestRun_EmptyDirectory drives the binary against an empty dir; expected
// behavior per spec §9 R1: "0/0 passed", exit 0.
func TestRun_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run([]string{dir}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "0/0 passed") {
		t.Errorf("stdout should include '0/0 passed'; got:\n%s", stdout.String())
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
	// The drift table is printed in the report (stdout, not stderr).
	if !strings.Contains(stdout.String(), "schema:FinancialData") {
		t.Errorf("expected drift detail in output; got stdout=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "schema drift detected") {
		t.Errorf("expected schema-drift error message; got stdout=%s", stdout.String())
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
