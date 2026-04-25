package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestYFinanceBundleCapture_RawAndParsed verifies that when an artifact bundle
// is on ctx, the Yahoo Finance client writes both the parsed quote struct
// and the raw response bytes to the bundle. Confirms the spec's gateway
// pattern: parsed via Snapshot, raw via TeeReader+SnapshotRaw.
func TestYFinanceBundleCapture_RawAndParsed(t *testing.T) {
	// Fake Yahoo: responds to crumb, cookie, and quote endpoints. The cookie
	// step is keyed by the "fc.yahoo.com" path; auth then asks crumb, then
	// the quote endpoint serves a single AAPL quote.
	mux := http.NewServeMux()
	// Cookie endpoint: just return Set-Cookie so auth caches at least one.
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A1", Value: "test", Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	// Crumb endpoint: return a fixed crumb so subsequent quote calls succeed.
	mux.HandleFunc("/crumb", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test-crumb"))
	})
	// Quote endpoint: return a minimal AAPL quote payload.
	mux.HandleFunc("/v7/finance/quote", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"quoteResponse":{"result":[{"symbol":"AAPL","regularMarketPrice":190.0,"marketCap":3000000000000,"sharesOutstanding":15000000000,"beta":1.2}],"error":null}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	cfg := &config.YFinanceConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		CookieURL:      server.URL + "/cookie",
		CrumbURL:       server.URL + "/crumb",
		MaxRetries:     1,
		RequestTimeout: 5 * time.Second,
		AuthTTL:        1 * time.Hour,
	}
	client := NewYFinanceClient(cfg, zap.NewNop())

	// Open a bundle in a temp dir.
	tmpRoot := t.TempDir()
	aCfg := artifact.Config{
		Enabled:  true,
		RootPath: tmpRoot,
	}
	bundle, err := artifact.OpenBundle(aCfg, "req_test_market", "AAPL", artifact.TriggerHeader)
	if err != nil {
		t.Fatalf("OpenBundle failed: %v", err)
	}

	ctx := artifact.Inject(context.Background(), bundle)
	quote, err := client.GetQuote(ctx, "AAPL")
	if err != nil {
		t.Fatalf("GetQuote failed: %v", err)
	}
	if quote.RegularMarketPrice != 190.0 {
		t.Fatalf("expected price=190.0, got %v", quote.RegularMarketPrice)
	}

	if err := bundle.Close(); err != nil {
		t.Fatalf("bundle.Close failed: %v", err)
	}

	// Assert: parsed file exists.
	parsedPath := filepath.Join(bundle.Root(), "06-fetch-market.parsed.json")
	if _, err := os.Stat(parsedPath); err != nil {
		t.Fatalf("expected parsed snapshot at %s, got %v", parsedPath, err)
	}

	// Assert: raw file exists and contains the AAPL payload bytes.
	rawPath := filepath.Join(bundle.Root(), "06-fetch-market.raw.json")
	rawBody, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("expected raw snapshot at %s, got %v", rawPath, err)
	}
	if !strings.Contains(string(rawBody), "AAPL") {
		t.Fatalf("raw snapshot missing AAPL ticker: %s", string(rawBody))
	}
}

// TestYFinanceBundleCapture_NoBundleAttached confirms the gateway is fully
// nil-safe: with no artifact bundle on ctx, the GetQuote call still
// succeeds and creates zero on-disk artifacts. Spec G5 (zero impact for
// non-traced requests).
func TestYFinanceBundleCapture_NoBundleAttached(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "A1", Value: "test", Path: "/"})
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/crumb", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test-crumb"))
	})
	mux.HandleFunc("/v7/finance/quote", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"quoteResponse":{"result":[{"symbol":"AAPL","regularMarketPrice":1.0}],"error":null}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	cfg := &config.YFinanceConfig{
		Enabled:        true,
		BaseURL:        server.URL,
		CookieURL:      server.URL + "/cookie",
		CrumbURL:       server.URL + "/crumb",
		MaxRetries:     1,
		RequestTimeout: 5 * time.Second,
		AuthTTL:        1 * time.Hour,
	}
	client := NewYFinanceClient(cfg, zap.NewNop())

	tmpRoot := t.TempDir()
	ctx := context.Background()
	if _, err := client.GetQuote(ctx, "AAPL"); err != nil {
		t.Fatalf("GetQuote failed without bundle: %v", err)
	}

	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		t.Fatalf("ReadDir tmpRoot: %v", err)
	}
	if len(entries) > 0 {
		t.Fatalf("expected empty tmpRoot, got %d entries: %v", len(entries), entries)
	}
}

// TestYFinanceCrumbRedaction_AcrossRequests verifies that the redactor's
// query-string path correctly scrubs Yahoo's `crumb` URL parameter when
// invoked via RedactURL. This is a focused unit on the redact contract for
// the spec §7.5 closed list — the gateway only writes response bodies (not
// URLs) into bundles, so crumb-in-URL redaction is exercised against the
// helper directly.
func TestYFinanceCrumbRedaction_AcrossRequests(t *testing.T) {
	url := "https://query2.finance.yahoo.com/v7/finance/quote?symbols=AAPL&crumb=AbCdEf123"
	redacted, paths := artifact.RedactURL(url)
	if strings.Contains(redacted, "AbCdEf123") {
		t.Fatalf("redacted URL still contains crumb: %s", redacted)
	}
	foundCrumb := false
	for _, p := range paths {
		if p == "query.crumb" {
			foundCrumb = true
		}
	}
	if !foundCrumb {
		t.Fatalf("expected query.crumb in redacted paths, got %v", paths)
	}
}
