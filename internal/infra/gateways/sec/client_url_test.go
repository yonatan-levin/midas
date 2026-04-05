package sec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/config"
	"go.uber.org/zap"
)

func Test_formatCIK(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"789019", "CIK0000789019", true},
		{"0000789019", "CIK0000789019", true},
		{"MSFT", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, err := formatCIK(tc.in)
		if tc.ok && err != nil {
			t.Fatalf("formatCIK(%q) unexpected error: %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("formatCIK(%q) expected error, got none", tc.in)
		}
		if got != tc.want {
			t.Fatalf("formatCIK(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func Test_secXBRLURL_Variants(t *testing.T) {
	variants := []string{
		"https://data.sec.gov",
		"https://data.sec.gov/",
		"https://data.sec.gov/api",
		"https://data.sec.gov/api/",
		"https://data.sec.gov/api/xbrl",
		"https://data.sec.gov/api/xbrl/",
	}

	for _, base := range variants {
		cfg := &config.SECConfig{BaseURL: base, RateLimit: 10, RequestTimeout: 5 * time.Second}
		logger := zap.NewNop()
		c := NewClient(cfg, logger)
		u, err := c.secXBRLURL("companyfacts", "CIK0000789019.json")
		if err != nil {
			t.Fatalf("secXBRLURL error for base %q: %v", base, err)
		}
		want := "https://data.sec.gov/api/xbrl/companyfacts/CIK0000789019.json"
		if u != want {
			t.Fatalf("base %q -> %q, want %q", base, u, want)
		}
	}
}

func TestClient_GetCompanyFacts_BuildsCorrectPath(t *testing.T) {
	// Mock SEC server asserting the request path
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/api/xbrl/companyfacts/CIK0000789019.json"
		if r.URL.Path != wantPath {
			t.Fatalf("unexpected path: got %q want %q", r.URL.Path, wantPath)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cik":789019,"entityName":"MICROSOFT CORPORATION","facts":{"us-gaap":{"Assets":{"label":"Assets","description":"","units":{"USD":[]}}}}}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	cfg := &config.SECConfig{BaseURL: srv.URL, UserAgent: "test ua", RateLimit: 10, RequestTimeout: 5 * time.Second, MaxRetries: 1}
	logger := zap.NewNop()
	c := NewClient(cfg, logger)
	// Use server client to avoid TLS issues
	c.httpClient = srv.Client()

	ctx := context.Background()
	_, err := c.GetCompanyFacts(ctx, "789019")
	if err != nil {
		t.Fatalf("GetCompanyFacts error: %v", err)
	}
}

func TestClient_GetCompanyConcepts_BuildsCorrectPath(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/api/xbrl/companyconcept/CIK0000789019/us-gaap/Assets.json"
		if r.URL.Path != wantPath {
			t.Fatalf("unexpected path: got %q want %q", r.URL.Path, wantPath)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cik":"0000789019","entityName":"MICROSOFT CORPORATION","tag":"Assets","units":{}}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	cfg := &config.SECConfig{BaseURL: srv.URL, UserAgent: "test ua", RateLimit: 10, RequestTimeout: 5 * time.Second, MaxRetries: 1}
	logger := zap.NewNop()
	c := NewClient(cfg, logger)
	c.httpClient = srv.Client()

	ctx := context.Background()
	_, err := c.GetCompanyConcepts(ctx, "789019", "Assets")
	if err != nil {
		t.Fatalf("GetCompanyConcepts error: %v", err)
	}
}
