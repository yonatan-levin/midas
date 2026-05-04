package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// makeFREDObsRaw produces a valid single-observation FRED API response body
// for the supplied series ID and percentage value. Mirrors the real FRED
// payload shape (see internal/infra/gateways/macro/gateway.go:541-553).
func makeFREDObsRaw(t *testing.T, value string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"realtime_start": "2025-01-15",
		"realtime_end":   "2025-01-15",
		"observations": []map[string]interface{}{
			{
				"realtime_start": "2025-01-15",
				"realtime_end":   "2025-01-15",
				"date":           "2025-01-15",
				"value":          value,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal FRED raw: %v", err)
	}
	return body
}

func TestBundleMacroGateway_GetTreasuryRates_RawMode_ParsesProductionBytes(t *testing.T) {
	tmpDir := t.TempDir()

	// Seed three series files at different rates; rest are absent (per-series
	// tolerance — production warns + continues).
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS10.raw.json", makeFREDObsRaw(t, "4.25"))
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS5.raw.json", makeFREDObsRaw(t, "3.75"))
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS2.raw.json", makeFREDObsRaw(t, "3.50"))

	gw := NewBundleMacroGateway(tmpDir, ModeRaw)
	got, err := gw.GetTreasuryRates(context.Background())
	if err != nil {
		t.Fatalf("GetTreasuryRates: %v", err)
	}
	// FRED returns percentages; production divides by 100 to express decimals.
	if got.Yield10Year != 0.0425 {
		t.Fatalf("Yield10Year: want 0.0425, got %v", got.Yield10Year)
	}
	if got.Yield5Year != 0.0375 {
		t.Fatalf("Yield5Year: want 0.0375, got %v", got.Yield5Year)
	}
	if got.Yield2Year != 0.0350 {
		t.Fatalf("Yield2Year: want 0.0350, got %v", got.Yield2Year)
	}
}

func TestBundleMacroGateway_GetTreasuryRates_ParsedMode_DirectUnmarshal(t *testing.T) {
	tmpDir := t.TempDir()

	rates := entities.TreasuryRates{
		Yield10Year: 0.0425,
		Yield5Year:  0.0375,
	}
	body, err := json.Marshal(&rates)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	seedBundleFile(t, tmpDir, macroParsedFile, body)

	gw := NewBundleMacroGateway(tmpDir, ModeParsed)
	got, err := gw.GetTreasuryRates(context.Background())
	if err != nil {
		t.Fatalf("GetTreasuryRates: %v", err)
	}
	if got.Yield10Year != 0.0425 {
		t.Fatalf("Yield10Year: want 0.0425, got %v", got.Yield10Year)
	}
}

func TestBundleMacroGateway_GetTreasuryRates_RawMode_AllSeriesMissing_ReturnsErrBundleMissingPayload(t *testing.T) {
	tmpDir := t.TempDir() // no files
	gw := NewBundleMacroGateway(tmpDir, ModeRaw)
	_, err := gw.GetTreasuryRates(context.Background())
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMacroGateway_GetTreasuryRates_ParsedMode_MissingFile_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeParsed)
	_, err := gw.GetTreasuryRates(context.Background())
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMacroGateway_GetTreasuryRates_RawMode_PartialPresence_ToleratedPerSeries(t *testing.T) {
	tmpDir := t.TempDir()
	// Only one series present; rest must be tolerated.
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS10.raw.json", makeFREDObsRaw(t, "4.25"))

	gw := NewBundleMacroGateway(tmpDir, ModeRaw)
	got, err := gw.GetTreasuryRates(context.Background())
	if err != nil {
		t.Fatalf("GetTreasuryRates: %v", err)
	}
	if got.Yield10Year != 0.0425 {
		t.Fatalf("Yield10Year: want 0.0425, got %v", got.Yield10Year)
	}
	if got.Yield5Year != 0 {
		t.Fatalf("Yield5Year: want 0 (absent), got %v", got.Yield5Year)
	}
}

func TestBundleMacroGateway_GetMarketRiskPremium_NotInBundle_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeRaw)
	_, err := gw.GetMarketRiskPremium(context.Background())
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMacroGateway_GetFXRate_FromCcyEqualsToCcy_ReturnsOne(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeRaw)
	rate, err := gw.GetFXRate(context.Background(), "USD", "USD")
	if err != nil {
		t.Fatalf("GetFXRate identity: %v", err)
	}
	if rate != 1.0 {
		t.Fatalf("rate: want 1.0, got %v", rate)
	}
}

func TestBundleMacroGateway_GetFXRate_NonIdentity_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeRaw)
	_, err := gw.GetFXRate(context.Background(), "TWD", "USD")
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMacroGateway_HealthCheck_AlwaysOK(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeRaw)
	if err := gw.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestBundleMacroGateway_ConcurrentGetTreasuryRates_RaceFree(t *testing.T) {
	tmpDir := t.TempDir()
	for seriesID := range macroSeriesMap {
		seedBundleFile(t, tmpDir, fmt.Sprintf("07-fetch-macro-%s.raw.json", seriesID), makeFREDObsRaw(t, "4.0"))
	}

	gw := NewBundleMacroGateway(tmpDir, ModeRaw)

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := gw.GetTreasuryRates(context.Background()); err != nil {
				t.Errorf("concurrent GetTreasuryRates: %v", err)
			}
		}()
	}
	wg.Wait()
	if gw.CallsCount() != N {
		t.Fatalf("CallsCount: want %d, got %d", N, gw.CallsCount())
	}
}

// Compile-time interface conformance.
var _ ports.MacroDataGateway = (*BundleMacroGateway)(nil)
