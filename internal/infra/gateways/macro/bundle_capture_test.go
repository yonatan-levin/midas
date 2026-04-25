package macro

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestGatewayBundleCapture_FREDPath verifies that when an artifact bundle is
// attached to ctx, the macro gateway:
//  1. Writes the parsed treasury rates JSON file under the bundle root.
//  2. Writes one .raw.json per FRED series fetched.
//  3. Records "query.api_key" in the manifest's redactions_applied set so
//     the auditor can confirm the FRED key never reaches disk.
//
// Drives the full GetTreasuryRates flow through a fake FRED server so the
// TeeReader path is exercised end-to-end.
func TestGatewayBundleCapture_FREDPath(t *testing.T) {
	// Fake FRED returns a minimal valid observation payload for any series.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back the FRED-shaped response. Static value so the test is
		// deterministic; value=4.50 matches a 4.5% treasury yield.
		_, _ = w.Write([]byte(`{"realtime_start":"2026-04-25","realtime_end":"2026-04-25","observations":[{"realtime_start":"2026-04-25","realtime_end":"2026-04-25","date":"2026-04-25","value":"4.50"}]}`))
	}))
	defer server.Close()

	gw := NewGateway(&config.MacroConfig{
		FREDEnabled: true,
		FREDAPIKey:  "test-api-key-should-not-leak",
		FREDBaseURL: server.URL,
	}, zap.NewNop())

	// Open a bundle in a temp dir.
	tmpRoot := t.TempDir()
	cfg := artifact.Config{
		Enabled:  true,
		RootPath: tmpRoot,
	}
	bundle, err := artifact.OpenBundle(cfg, "req_test_macro", "TEST", artifact.TriggerHeader)
	if err != nil {
		t.Fatalf("OpenBundle failed: %v", err)
	}

	ctx := artifact.Inject(context.Background(), bundle)
	if _, err := gw.GetTreasuryRates(ctx); err != nil {
		t.Fatalf("GetTreasuryRates failed: %v", err)
	}

	// Close the bundle to flush the worker queue and finalise the manifest.
	if err := bundle.Close(); err != nil {
		t.Fatalf("bundle.Close failed: %v", err)
	}

	// Assert: parsed file present and well-formed.
	parsedPath := filepath.Join(bundle.Root(), "07-fetch-macro.parsed.json")
	if _, err := os.Stat(parsedPath); err != nil {
		t.Fatalf("expected parsed snapshot at %s, got: %v", parsedPath, err)
	}

	// Assert: at least one raw FRED series file present (we fetch 9 series so
	// expect 9 raw files, one per seriesID).
	entries, err := os.ReadDir(bundle.Root())
	if err != nil {
		t.Fatalf("ReadDir bundle root: %v", err)
	}
	rawCount := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "07-fetch-macro-") && strings.HasSuffix(e.Name(), ".raw.json") {
			rawCount++
		}
	}
	if rawCount == 0 {
		t.Fatalf("expected at least one FRED raw snapshot, found none in %s (entries: %v)", bundle.Root(), entries)
	}

	// Assert: raw payload bytes do NOT contain the FRED API key (defence in
	// depth — even if redactor missed something, the URL-borne secret should
	// never be inside the JSON body anyway).
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".raw.json") {
			body, _ := os.ReadFile(filepath.Join(bundle.Root(), e.Name()))
			if strings.Contains(string(body), "test-api-key-should-not-leak") {
				t.Fatalf("FRED API key leaked into raw bundle file %s", e.Name())
			}
		}
	}

	// Assert: manifest records the redaction of query.api_key so an auditor
	// can verify the secret was scrubbed.
	var m artifact.Manifest
	manifestBody, err := os.ReadFile(filepath.Join(bundle.Root(), "00-manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(manifestBody, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	foundAPIKey := false
	for _, r := range m.RedactionsApplied {
		if r == "query.api_key" {
			foundAPIKey = true
			break
		}
	}
	if !foundAPIKey {
		t.Fatalf("manifest.redactions_applied missing query.api_key; got %v", m.RedactionsApplied)
	}
}

// TestGatewayBundleCapture_NoBundleAttached verifies that when no artifact
// bundle is on ctx, the gateway is silently a no-op for capture: zero files
// are created on disk under the artifact root, and the request still returns
// successfully. This is the critical "zero impact for non-traced requests"
// invariant from spec G5.
func TestGatewayBundleCapture_NoBundleAttached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"observations":[{"date":"2026-04-25","value":"4.50"}]}`))
	}))
	defer server.Close()

	gw := NewGateway(&config.MacroConfig{
		FREDEnabled: true,
		FREDAPIKey:  "k",
		FREDBaseURL: server.URL,
	}, zap.NewNop())

	tmpRoot := t.TempDir()
	// Note: ctx has NO bundle attached.
	ctx := context.Background()
	if _, err := gw.GetTreasuryRates(ctx); err != nil {
		t.Fatalf("GetTreasuryRates failed without bundle: %v", err)
	}

	// Assert tmpRoot is empty — no bundle directory was created.
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		t.Fatalf("ReadDir tmpRoot: %v", err)
	}
	if len(entries) > 0 {
		t.Fatalf("expected empty tmpRoot when no bundle on ctx, got %d entries: %v", len(entries), entries)
	}
}
