package replay

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// makeMinimalSECRaw produces a minimal but valid ports.SECCompanyFacts JSON
// body sufficient to drive sec.Parser.ParseFinancialData past
// extractFiscalPeriods. The fixture stamps:
//   - cik = 320193 (AAPL, captured numeric per FlexibleCIK contract)
//   - one us-gaap concept (Revenues) with one fact in USD
//   - one dei share-count concept so per-share math has a denominator
//
// Uses one period (2023FY, 10-K) so parsePeriodData succeeds.
func makeMinimalSECRaw(t *testing.T) []byte {
	t.Helper()
	facts := map[string]interface{}{
		"cik":        320193,
		"entityName": "Apple Inc.",
		"facts": map[string]interface{}{
			"us-gaap": map[string]interface{}{
				"Revenues": map[string]interface{}{
					"label":       "Revenues",
					"description": "Aggregate revenue",
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   383285000000.0,
								"end":   "2023-09-30",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2023-11-03",
								"accn":  "0000320193-23-000106",
								"frame": "CY2023",
							},
						},
					},
				},
				"OperatingIncomeLoss": map[string]interface{}{
					"label":       "Operating Income (Loss)",
					"description": "Operating income",
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   114301000000.0,
								"end":   "2023-09-30",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2023-11-03",
								"accn":  "0000320193-23-000106",
								"frame": "CY2023",
							},
						},
					},
				},
				"Assets": map[string]interface{}{
					"label":       "Assets",
					"description": "Total Assets",
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   352755000000.0,
								"end":   "2023-09-30",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2023-11-03",
								"accn":  "0000320193-23-000106",
								"frame": "CY2023",
							},
						},
					},
				},
				"Liabilities": map[string]interface{}{
					"label":       "Liabilities",
					"description": "Total Liabilities",
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   290437000000.0,
								"end":   "2023-09-30",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2023-11-03",
								"accn":  "0000320193-23-000106",
								"frame": "CY2023",
							},
						},
					},
				},
			},
			"dei": map[string]interface{}{
				"EntityCommonStockSharesOutstanding": map[string]interface{}{
					"label":       "Common Stock Shares Outstanding",
					"description": "Shares outstanding",
					"units": map[string]interface{}{
						"shares": []interface{}{
							map[string]interface{}{
								"val":   15634232000.0,
								"end":   "2023-09-30",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2023-11-03",
								"accn":  "0000320193-23-000106",
								"frame": "CY2023Q3I",
							},
						},
					},
				},
			},
		},
	}
	body, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("marshal raw fixture: %v", err)
	}
	return body
}

// makeMinimalSECParsed produces a minimal entities.CompanyFactsResponse JSON
// body for ModeParsed tests. Captures only the fields the test asserts on;
// production's snapshot is richer but the test does not need that.
func makeMinimalSECParsed(t *testing.T) []byte {
	t.Helper()
	resp := entities.CompanyFactsResponse{
		CIK:        "320193",
		EntityName: "Apple Inc.",
		Facts:      map[string]interface{}{},
		FactsCount: 5,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal parsed fixture: %v", err)
	}
	return body
}

// seedBundleFile writes a single file under tmpDir, creating any required
// parent directories. Returns the bundle directory path. Used by every
// test in this file to set up minimal fixture trees.
func seedBundleFile(t *testing.T, tmpDir, name string, content []byte) string {
	t.Helper()
	full := filepath.Join(tmpDir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return tmpDir
}

func TestBundleSECGateway_GetCompanyFacts_RawMode_ParsesProductionBytes(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, secRawFile, makeMinimalSECRaw(t))

	gw := NewBundleSECGateway(tmpDir, ModeRaw, "AAPL", zap.NewNop())
	got, err := gw.GetCompanyFacts(context.Background(), "320193")
	if err != nil {
		t.Fatalf("GetCompanyFacts: %v", err)
	}
	if got == nil {
		t.Fatalf("expected non-nil response")
	}
	if got.CIK != "320193" {
		t.Fatalf("CIK: want 320193, got %q", got.CIK)
	}
	if got.EntityName != "Apple Inc." {
		t.Fatalf("EntityName: want Apple Inc., got %q", got.EntityName)
	}
	if len(got.Facts) == 0 {
		t.Fatalf("expected non-empty Facts map; got empty")
	}
	if got.FactsCount == 0 {
		t.Fatalf("expected FactsCount > 0; got 0")
	}
}

func TestBundleSECGateway_GetCompanyFacts_ParsedMode_DirectUnmarshal(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, secParsedFile, makeMinimalSECParsed(t))

	gw := NewBundleSECGateway(tmpDir, ModeParsed, "AAPL", zap.NewNop())
	got, err := gw.GetCompanyFacts(context.Background(), "320193")
	if err != nil {
		t.Fatalf("GetCompanyFacts: %v", err)
	}
	if got.CIK != "320193" {
		t.Fatalf("CIK: want 320193, got %q", got.CIK)
	}
	if got.EntityName != "Apple Inc." {
		t.Fatalf("EntityName: want Apple Inc., got %q", got.EntityName)
	}
}

func TestBundleSECGateway_GetCompanyFacts_MissingFile_ReturnsErrBundleMissingPayload(t *testing.T) {
	tmpDir := t.TempDir() // no fixture seeded
	gw := NewBundleSECGateway(tmpDir, ModeRaw, "AAPL", zap.NewNop())
	_, err := gw.GetCompanyFacts(context.Background(), "320193")
	if err == nil {
		t.Fatalf("expected error; got nil")
	}
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v (type %T)", err, err)
	}
	var rich *BundleMissingPayloadError
	if !errors.As(err, &rich) {
		t.Fatalf("expected *BundleMissingPayloadError; got %T", err)
	}
	if rich.RelativePath != secRawFile {
		t.Fatalf("RelativePath: want %q, got %q", secRawFile, rich.RelativePath)
	}
}

func TestBundleSECGateway_GetTickerCIKMapping_NotInBundle_ReturnsErrBundleMissingPayload(t *testing.T) {
	tmpDir := t.TempDir()
	gw := NewBundleSECGateway(tmpDir, ModeRaw, "AAPL", zap.NewNop())
	_, err := gw.GetTickerCIKMapping(context.Background())
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

// TestBundleSECGateway_GetTickerCIKMapping_ArbitraryTicker pins the fix
// for VERIFIER finding MEDIUM-1: the mapping must work for ANY bundle
// ticker, not just a hardcoded 8-element list. The previous
// implementation returned a fixed map keyed by AAPL/MSFT/GOOG/GOOGL/AMZN/
// NVDA/TSLA/META so any other ticker (e.g. BABA, AMD, JPM) would silently
// fall through coordinator.go:342 and fail the engine with a
// "ticker not found" error during replay.
//
// Fix: the constructor now takes the manifest ticker; the mapping
// returned is exactly {ticker: cik}, working for arbitrary bundles.
func TestBundleSECGateway_GetTickerCIKMapping_ArbitraryTicker(t *testing.T) {
	cases := []struct {
		ticker string
	}{
		{"BABA"},
		{"AMD"},
		{"JPM"},
		{"TSM"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.ticker, func(t *testing.T) {
			tmpDir := t.TempDir()
			seedBundleFile(t, tmpDir, secRawFile, makeMinimalSECRaw(t))

			gw := NewBundleSECGateway(tmpDir, ModeRaw, tc.ticker, zap.NewNop())
			mapping, err := gw.GetTickerCIKMapping(context.Background())
			if err != nil {
				t.Fatalf("GetTickerCIKMapping: %v", err)
			}
			cik, ok := mapping[tc.ticker]
			if !ok {
				t.Fatalf("expected %s in mapping; got %v", tc.ticker, mapping)
			}
			// SEC fixture stamps CIK 320193 (AAPL); the bundle is
			// single-ticker so we mirror that captured CIK back regardless
			// of what the manifest ticker is. The load-bearing assertion
			// is "the manifest ticker is the key", not the value.
			if cik != "320193" {
				t.Fatalf("CIK for %s: want 320193, got %q", tc.ticker, cik)
			}
		})
	}
}

func TestBundleSECGateway_GetCompanyConcepts_NotInBundle_ReturnsErrBundleMissingPayload(t *testing.T) {
	tmpDir := t.TempDir()
	gw := NewBundleSECGateway(tmpDir, ModeRaw, "AAPL", zap.NewNop())
	_, err := gw.GetCompanyConcepts(context.Background(), "320193", "Revenues")
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleSECGateway_HealthCheck_AlwaysOK(t *testing.T) {
	gw := NewBundleSECGateway(t.TempDir(), ModeRaw, "AAPL", zap.NewNop())
	if err := gw.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestBundleSECGateway_GetFinancialDataForTicker_RawMode_ProducesHistorical(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, secRawFile, makeMinimalSECRaw(t))

	gw := NewBundleSECGateway(tmpDir, ModeRaw, "AAPL", zap.NewNop())
	got, err := gw.GetFinancialDataForTicker(context.Background(), "AAPL", "320193")
	if err != nil {
		t.Fatalf("GetFinancialDataForTicker: %v", err)
	}
	if got == nil {
		t.Fatalf("expected non-nil HistoricalFinancialData")
	}
	if got.Ticker != "AAPL" {
		t.Fatalf("Ticker: want AAPL, got %q", got.Ticker)
	}
	if len(got.Data) == 0 {
		t.Fatalf("expected at least one period in Data; got 0")
	}
}

// TestBundleSECGateway_ConcurrentGetCompanyFacts_RaceFree exercises the
// goroutine-safety contract: the production datafetcher coordinator
// invokes gateway methods inside go-func() workers, so a stateful gateway
// would race. Run under -race to surface any.
func TestBundleSECGateway_ConcurrentGetCompanyFacts_RaceFree(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, secRawFile, makeMinimalSECRaw(t))

	gw := NewBundleSECGateway(tmpDir, ModeRaw, "AAPL", zap.NewNop())

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := gw.GetCompanyFacts(context.Background(), "320193"); err != nil {
				t.Errorf("concurrent GetCompanyFacts: %v", err)
			}
		}()
	}
	wg.Wait()
	if gw.CallsCount() != N {
		t.Fatalf("CallsCount: want %d, got %d", N, gw.CallsCount())
	}
}

func TestBundleSECGateway_GetCompanyFacts_RawMode_MalformedJSON_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, secRawFile, []byte("{not-json"))

	gw := NewBundleSECGateway(tmpDir, ModeRaw, "AAPL", zap.NewNop())
	_, err := gw.GetCompanyFacts(context.Background(), "320193")
	if err == nil {
		t.Fatalf("expected error; got nil")
	}
	// errors.Is should NOT match ErrBundleMissingPayload here — this is a
	// real parse error, not a missing-file error.
	if errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("malformed JSON misclassified as missing payload: %v", err)
	}
}

// Sanity check: ports.SECGateway interface is satisfied at compile time.
var _ ports.SECGateway = (*BundleSECGateway)(nil)
