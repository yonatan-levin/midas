package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// R3 integration tests for parallel dispatch (Stage I.2), filter flags
// (Stage J), and timing instrumentation (Stage L.3).
//
// All tests use the same `00-manifest.json`-only fixtures the R1+R2
// suite uses (via cpManifest in main_test.go). Schema-drift bundles are
// what they are; happy bundles report SKELETON_OK. The tests assert
// orchestration behavior — parallelism, ordering, filtering, timing —
// without requiring the full engine path.

// TestRun_Workers_DeterministicStdoutAcrossWorkerCounts pins the v2
// plan §3 Decision I.3: rendering sorts by bundle path, so stdout is
// byte-identical between --workers=1 and --workers=N for the same
// bundle set. This is the headline determinism guarantee under
// parallelism.
func TestRun_Workers_DeterministicStdoutAcrossWorkerCounts(t *testing.T) {
	root := t.TempDir()
	// Build a 5-bundle tree. Use happy-only fixtures so all 5 succeed
	// uniformly (mixed pass/fail would still be deterministic but the
	// noise complicates the byte-equality assertion when the diagnostic
	// includes line offsets).
	for _, name := range []string{"req_a", "req_b", "req_c", "req_d", "req_e"} {
		cpManifest(t, "happy", filepath.Join(root, name))
	}

	runWith := func(workers string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--format=json", "--workers=" + workers, root}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("--workers=%s: exit=%d stderr=%s", workers, code, stderr.String())
		}
		// Strip the timing fields before comparison: walk_duration_ms
		// and replay_duration_ms are wall-clock and will naturally
		// differ between runs (and between worker counts). The shape
		// guarantee is what the renderer sorts by — Bundle paths and
		// per-result content — not the timing.
		return stripTimingFields(t, stdout.String())
	}

	a := runWith("1")
	b := runWith("4")
	if a != b {
		t.Fatalf("stdout differs between --workers=1 and --workers=4 (after stripping timing fields)\n--workers=1:\n%s\n--workers=4:\n%s", a, b)
	}
}

// TestRun_Workers_InvalidValueRejected confirms the validation gate
// fires on infrastructure failure (exit 2 per spec §9).
func TestRun_Workers_InvalidValueRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--workers=0", t.TempDir()}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--workers must be >= 1") {
		t.Errorf("expected --workers validation error on stderr; got %q", stderr.String())
	}
}

// TestRun_FilterTicker_OnlyMatchingBundlesReplayed exercises Stage J.1.
// Three bundles, two named AAPL (the canonical happy ticker), one
// named MSFT after manifest mutation; only AAPL bundles should appear
// in the result.
func TestRun_FilterTicker_OnlyMatchingBundlesReplayed(t *testing.T) {
	root := t.TempDir()
	cpManifest(t, "happy", filepath.Join(root, "AAPL", "req_a"))
	cpManifest(t, "happy", filepath.Join(root, "AAPL", "req_b"))
	// Mutate the third bundle's ticker to MSFT so the filter excludes it.
	mutateManifest(t, filepath.Join(root, "MSFT", "req_c"), "happy", func(m map[string]any) {
		m["ticker"] = "MSFT"
	})

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--format=json", "--filter-ticker=AAPL", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}

	report := decodeReport(t, stdout.Bytes())
	results, _ := report["results"].([]any)
	if len(results) != 2 {
		t.Errorf("expected 2 AAPL results; got %d (%v)", len(results), results)
	}
	for _, r := range results {
		row := r.(map[string]any)
		if row["ticker"] != "AAPL" {
			t.Errorf("filter leaked non-AAPL ticker: %v", row["ticker"])
		}
	}
}

// TestRun_FilterTicker_NoMatch_EmptyResultsExitZero pins the v2 plan
// "0/0 passed = exit 0" semantics for filters that exclude everything.
func TestRun_FilterTicker_NoMatch_EmptyResultsExitZero(t *testing.T) {
	root := t.TempDir()
	cpManifest(t, "happy", filepath.Join(root, "req_a"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--filter-ticker=NEVER", root}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("filter excluding everything must exit 0; got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "0/0 passed") {
		t.Errorf("expected 0/0 passed; got %s", stdout.String())
	}
}

// TestRun_FilterTicker_CaseSensitive — the filter is intentionally
// case-sensitive (tickers are uppercase by convention; lowercase typos
// should silently no-op).
func TestRun_FilterTicker_CaseSensitive(t *testing.T) {
	root := t.TempDir()
	cpManifest(t, "happy", filepath.Join(root, "req_a"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--filter-ticker=aapl", root}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "0/0 passed") {
		t.Errorf("lowercase aapl should NOT match AAPL; got %s", stdout.String())
	}
}

// TestRun_FilterSince_OnlyRecentBundles drives Stage J.2: bundles
// older than --filter-since are excluded.
func TestRun_FilterSince_OnlyRecentBundles(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	// Bundle 1: 1 hour ago (within 7d filter).
	mutateManifest(t, filepath.Join(root, "recent_1h"), "happy", func(m map[string]any) {
		m["started_at"] = now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	})
	// Bundle 2: 5 days ago (within 7d filter).
	mutateManifest(t, filepath.Join(root, "recent_5d"), "happy", func(m map[string]any) {
		m["started_at"] = now.Add(-5 * 24 * time.Hour).UTC().Format(time.RFC3339)
	})
	// Bundle 3: 30 days ago (outside 7d filter).
	mutateManifest(t, filepath.Join(root, "old_30d"), "happy", func(m map[string]any) {
		m["started_at"] = now.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	})

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--format=json", "--filter-since=7d", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}

	report := decodeReport(t, stdout.Bytes())
	results, _ := report["results"].([]any)
	if len(results) != 2 {
		t.Errorf("expected 2 recent results; got %d (%v)", len(results), results)
	}
	// All retained results must be the within-window bundles.
	for _, r := range results {
		row := r.(map[string]any)
		bundle := row["bundle"].(string)
		if strings.Contains(bundle, "old_30d") {
			t.Errorf("filter leaked the 30d-old bundle: %v", bundle)
		}
	}
}

// TestRun_FilterSince_DaysSyntaxAccepted pins ParseDurationExtended
// integration — `7d` MUST resolve to 7×24h, not error.
func TestRun_FilterSince_DaysSyntaxAccepted(t *testing.T) {
	root := t.TempDir()
	mutateManifest(t, filepath.Join(root, "req_a"), "happy", func(m map[string]any) {
		m["started_at"] = time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	})

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--filter-since=7d", root}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("--filter-since=7d should be accepted; exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1/1 passed") {
		t.Errorf("expected 1/1 passed (1h-old bundle within 7d); got %s", stdout.String())
	}
}

// TestRun_Summary_HasWalkAndReplayDurations pins Stage L.3 (v2
// Addition #4): the rendered JSON includes both timing fields.
func TestRun_Summary_HasWalkAndReplayDurations(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		cpManifest(t, "happy", filepath.Join(root, name))
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--format=json", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}

	report := decodeReport(t, stdout.Bytes())
	summary, ok := report["summary"].(map[string]any)
	if !ok {
		t.Fatalf("missing summary; got: %v", report)
	}
	if _, ok := summary["walk_duration_ms"]; !ok {
		t.Errorf("summary missing walk_duration_ms key")
	}
	if _, ok := summary["replay_duration_ms"]; !ok {
		t.Errorf("summary missing replay_duration_ms key")
	}
	// Both values must be >= 0; on tiny fixtures they may round to 0
	// (millisecond resolution). The shape guarantee is the contract,
	// not the magnitude. Document why: a sub-millisecond walk on a
	// 3-bundle fixture can legitimately be zero.
	walk := summary["walk_duration_ms"].(float64)
	rep := summary["replay_duration_ms"].(float64)
	if walk < 0 {
		t.Errorf("walk_duration_ms = %v, want >= 0", walk)
	}
	if rep < 0 {
		t.Errorf("replay_duration_ms = %v, want >= 0", rep)
	}
}

// TestRun_FilterSince_MalformedManifestStartedAt_BundleKept pins the
// "filters narrow inclusion; they don't suppress errors" invariant.
// A manifest with a bad started_at must NOT silently disappear.
func TestRun_FilterSince_MalformedManifestStartedAt_BundleKept(t *testing.T) {
	root := t.TempDir()
	mutateManifest(t, filepath.Join(root, "bad_time"), "happy", func(m map[string]any) {
		m["started_at"] = "not-a-timestamp"
	})

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--filter-since=7d", root}, &stdout, &stderr)
	// The bundle is kept; happy fixture passes; exit 0.
	if code != 0 {
		t.Errorf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "malformed manifest started_at") {
		t.Errorf("expected stderr WARN about malformed started_at; got %s", stderr.String())
	}
}

// --- Helpers ---

// mutateManifest reads the canonical 00-manifest.json from
// internal/observability/replay/testdata/<src>/, applies mutator, and
// writes it to dst/00-manifest.json. Used to construct synthetic
// per-test manifests with controlled ticker / started_at.
func mutateManifest(t *testing.T, dst, src string, mutator func(map[string]any)) {
	t.Helper()
	srcPath := filepath.Join("..", "..", "internal", "observability", "replay", "testdata", src, "00-manifest.json")
	body, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read %s: %v", srcPath, err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mutator(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	if err := os.WriteFile(filepath.Join(dst, "00-manifest.json"), out, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// decodeReport unmarshals the JSON report into a generic map for
// shape-only assertions. Used because the cmd package can't import
// the replay package's Report type without an import cycle.
func decodeReport(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, string(body))
	}
	return m
}

// stripTimingFields removes the three timing fields from a JSON
// report: summary.walk_duration_ms, summary.replay_duration_ms, and
// each result's duration_ms. Used by determinism tests where the
// shape is what we're pinning, not the wall-clock magnitude.
func stripTimingFields(t *testing.T, body string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, body)
	}
	if summary, ok := m["summary"].(map[string]any); ok {
		delete(summary, "walk_duration_ms")
		delete(summary, "replay_duration_ms")
		delete(summary, "duration_ms")
	}
	if results, ok := m["results"].([]any); ok {
		for _, r := range results {
			if row, ok := r.(map[string]any); ok {
				delete(row, "duration_ms")
			}
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}
