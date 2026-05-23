package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/config"
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

	gw := NewBundleMacroGateway(tmpDir, ModeRaw, nil, nil)
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

	gw := NewBundleMacroGateway(tmpDir, ModeParsed, nil, nil)
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
	gw := NewBundleMacroGateway(tmpDir, ModeRaw, nil, nil)
	_, err := gw.GetTreasuryRates(context.Background())
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMacroGateway_GetTreasuryRates_ParsedMode_MissingFile_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeParsed, nil, nil)
	_, err := gw.GetTreasuryRates(context.Background())
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMacroGateway_GetTreasuryRates_RawMode_PartialPresence_ToleratedPerSeries(t *testing.T) {
	tmpDir := t.TempDir()
	// Only one series present; rest must be tolerated.
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS10.raw.json", makeFREDObsRaw(t, "4.25"))

	gw := NewBundleMacroGateway(tmpDir, ModeRaw, nil, nil)
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

// TestBundleMacroGateway_GetMarketRiskPremium_ReadsFromConfig pins the
// fix for VERIFIER finding HIGH-1: the gateway must read MRP from the
// supplied *config.Config so replay tracks whatever production value
// is wired (currently 0.05, see internal/config/config.go:490). Pinning
// against a constant inside the gateway risked silent drift if the
// production default ever changed; the constant in this package was
// 0.06 while production was 0.05, which would have produced a 1pp WACC
// drift for any captured bundle.
func TestBundleMacroGateway_GetMarketRiskPremium_ReadsFromConfig(t *testing.T) {
	cases := []struct {
		name string
		mrp  float64
	}{
		{"production default 0.05", 0.05},
		{"override 0.06", 0.06},
		{"override 0.075", 0.075},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Macro: config.MacroConfig{ManualMarketRiskPremium: tc.mrp}}
			gw := NewBundleMacroGateway(t.TempDir(), ModeRaw, cfg, nil)
			got, err := gw.GetMarketRiskPremium(context.Background())
			if err != nil {
				t.Fatalf("GetMarketRiskPremium: %v", err)
			}
			if got != tc.mrp {
				t.Fatalf("MRP: want %v, got %v", tc.mrp, got)
			}
		})
	}
}

// TestBundleMacroGateway_GetMarketRiskPremium_NilConfig_FallsBackToProductionDefault
// guards the no-config path: when callers construct the gateway without
// a config (e.g. in pre-existing tests), the gateway returns the
// production default constant so the prior contract still holds. This
// keeps the constructor signature change non-breaking for the common
// "throwaway test gateway" usage.
func TestBundleMacroGateway_GetMarketRiskPremium_NilConfig_FallsBackToProductionDefault(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeRaw, nil, nil)
	mrp, err := gw.GetMarketRiskPremium(context.Background())
	if err != nil {
		t.Fatalf("GetMarketRiskPremium: %v", err)
	}
	// Production default at internal/config/config.go:490 is 0.05.
	if mrp != defaultMarketRiskPremium {
		t.Fatalf("MRP nil-config fallback: want %v, got %v", defaultMarketRiskPremium, mrp)
	}
	if defaultMarketRiskPremium != 0.05 {
		t.Fatalf("defaultMarketRiskPremium constant must mirror production default 0.05; got %v", defaultMarketRiskPremium)
	}
}

func TestBundleMacroGateway_GetFXRate_FromCcyEqualsToCcy_ReturnsOne(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeRaw, nil, nil)
	rate, err := gw.GetFXRate(context.Background(), "USD", "USD")
	if err != nil {
		t.Fatalf("GetFXRate identity: %v", err)
	}
	if rate != 1.0 {
		t.Fatalf("rate: want 1.0, got %v", rate)
	}
}

func TestBundleMacroGateway_GetFXRate_NonIdentity_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeRaw, nil, nil)
	_, err := gw.GetFXRate(context.Background(), "TWD", "USD")
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMacroGateway_HealthCheck_AlwaysOK(t *testing.T) {
	gw := NewBundleMacroGateway(t.TempDir(), ModeRaw, nil, nil)
	if err := gw.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestBundleMacroGateway_ConcurrentGetTreasuryRates_RaceFree(t *testing.T) {
	tmpDir := t.TempDir()
	for seriesID := range macroSeriesMap {
		seedBundleFile(t, tmpDir, fmt.Sprintf("07-fetch-macro-%s.raw.json", seriesID), makeFREDObsRaw(t, "4.0"))
	}

	gw := NewBundleMacroGateway(tmpDir, ModeRaw, nil, nil)

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

// TestBundleMacroGateway_GetTreasuryRates_RawMode_FallsBackToParsed_RPL7 pins
// the RPL-7 contract (tracker docs/reviewer/RPL7-raw-mode-macro-per-series-snapshot.md):
// when ModeRaw is requested but no per-FRED-series 07-fetch-macro-<seriesID>.raw.json
// files exist in the bundle, the gateway transparently falls back to the
// aggregated 07-fetch-macro.parsed.json payload instead of erroring. This
// matches the production capture path which only ever writes the parsed
// payload (internal/infra/gateways/macro/gateway.go:115-132).
//
// The contract has three observable surfaces:
//   1. GetTreasuryRates returns the bundled rates with no error.
//   2. FellBackToParsed() reports true after the call.
//   3. A structured WARN with phase="RPL-7-raw-fallback" was emitted
//      (verified via an in-memory observer logger).
func TestBundleMacroGateway_GetTreasuryRates_RawMode_FallsBackToParsed_RPL7(t *testing.T) {
	tmpDir := t.TempDir()
	// Seed ONLY the aggregated parsed payload — the production capture shape.
	rates := entities.TreasuryRates{
		Yield10Year: 0.0425,
		Yield5Year:  0.0375,
		Yield2Year:  0.0350,
	}
	body, err := json.Marshal(&rates)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	seedBundleFile(t, tmpDir, macroParsedFile, body)

	// Wire an observer logger so we can assert the structured WARN fired.
	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	gw := NewBundleMacroGateway(tmpDir, ModeRaw, nil, logger)
	got, err := gw.GetTreasuryRates(context.Background())
	if err != nil {
		t.Fatalf("GetTreasuryRates: %v (expected fallback to parsed, not error)", err)
	}
	if got.Yield10Year != 0.0425 {
		t.Fatalf("Yield10Year: want 0.0425, got %v", got.Yield10Year)
	}
	if got.Yield5Year != 0.0375 {
		t.Fatalf("Yield5Year: want 0.0375, got %v", got.Yield5Year)
	}
	if !gw.FellBackToParsed() {
		t.Fatalf("FellBackToParsed: want true after raw-mode fallback fires")
	}
	// Verify the structured WARN was emitted with the grep-friendly phase key.
	entries := observed.FilterField(zap.String("phase", "RPL-7-raw-fallback")).All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 WARN with phase=RPL-7-raw-fallback; got %d (all entries: %+v)", len(entries), observed.All())
	}
	if entries[0].Level != zapcore.WarnLevel {
		t.Fatalf("expected WARN level; got %v", entries[0].Level)
	}
}

// TestBundleMacroGateway_GetTreasuryRates_RawMode_BothMissing_ReturnsErrBundleMissingPayload
// pins the RPL-7 fallback's failure mode: when BOTH the per-series files
// AND the aggregated parsed payload are absent, the original
// ErrBundleMissingPayload contract still holds. Callers using
// errors.Is(err, ErrBundleMissingPayload) MUST continue to match.
func TestBundleMacroGateway_GetTreasuryRates_RawMode_BothMissing_ReturnsErrBundleMissingPayload(t *testing.T) {
	tmpDir := t.TempDir() // no files at all
	gw := NewBundleMacroGateway(tmpDir, ModeRaw, nil, nil)
	_, err := gw.GetTreasuryRates(context.Background())
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload when both per-series + parsed are absent; got %v", err)
	}
	if gw.FellBackToParsed() {
		t.Fatalf("FellBackToParsed: want false when the fallback itself errored")
	}
}

// TestBundleMacroGateway_FellBackToParsed_StaysFalseWhenPerSeriesFilesPresent
// pins the inverse: when per-series files ARE present, the fallback does
// NOT fire and FellBackToParsed stays false. Guards against accidentally
// flipping the flag on the happy path.
func TestBundleMacroGateway_FellBackToParsed_StaysFalseWhenPerSeriesFilesPresent(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS10.raw.json", makeFREDObsRaw(t, "4.25"))
	gw := NewBundleMacroGateway(tmpDir, ModeRaw, nil, nil)
	if _, err := gw.GetTreasuryRates(context.Background()); err != nil {
		t.Fatalf("GetTreasuryRates: %v", err)
	}
	if gw.FellBackToParsed() {
		t.Fatalf("FellBackToParsed: want false when per-series files satisfied the request")
	}
}

// Compile-time interface conformance.
var _ ports.MacroDataGateway = (*BundleMacroGateway)(nil)
