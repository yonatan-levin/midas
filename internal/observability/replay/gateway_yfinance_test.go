package replay

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// makeAnalystRaw produces a minimal Yahoo Finance v10 earningsTrend
// envelope matching the production YFinanceEarningsTrendResponse decode
// shape. Mirrors the makeMarketRaw helper in gateway_market_test.go.
func makeAnalystRaw(t *testing.T) []byte {
	t.Helper()
	mkVal := func(v float64) map[string]interface{} {
		return map[string]interface{}{"raw": v, "fmt": ""}
	}
	env := map[string]interface{}{
		"quoteSummary": map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"earningsTrend": map[string]interface{}{
						"trend": []map[string]interface{}{
							{
								"period": "0y",
								"revenueEstimate": map[string]interface{}{
									"avg":              mkVal(100e9),
									"low":              mkVal(95e9),
									"high":             mkVal(110e9),
									"numberOfAnalysts": mkVal(25),
								},
							},
							{
								"period": "+1y",
								"revenueEstimate": map[string]interface{}{
									"avg": mkVal(120e9),
								},
							},
							{
								"period": "+5y",
								"growth": mkVal(0.15),
							},
						},
					},
				},
			},
			"error": nil,
		},
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal analyst raw: %v", err)
	}
	return body
}

func TestBundleYFinanceGateway_GetQuote_RawMode_ReturnsProductionShape(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, marketRawFile, makeMarketRaw(t, "AAPL"))

	gw := NewBundleYFinanceGateway(tmpDir, ModeRaw)
	got, err := gw.GetQuote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if got.Symbol != "AAPL" {
		t.Fatalf("Symbol: want AAPL, got %q", got.Symbol)
	}
	if got.RegularMarketPrice != 190.0 {
		t.Fatalf("RegularMarketPrice: want 190.0, got %v", got.RegularMarketPrice)
	}
}

func TestBundleYFinanceGateway_GetQuote_ParsedMode_DirectUnmarshal(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, marketParsedFile, makeMarketParsed(t, "AAPL"))

	gw := NewBundleYFinanceGateway(tmpDir, ModeParsed)
	got, err := gw.GetQuote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if got.Beta != 1.25 {
		t.Fatalf("Beta: want 1.25, got %v", got.Beta)
	}
}

func TestBundleYFinanceGateway_GetBatchQuotes_StampsRequestedTickers(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, marketRawFile, makeMarketRaw(t, "AAPL"))

	gw := NewBundleYFinanceGateway(tmpDir, ModeRaw)
	got, err := gw.GetBatchQuotes(context.Background(), []string{"AAPL", "MSFT"})
	if err != nil {
		t.Fatalf("GetBatchQuotes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: want 2, got %d", len(got))
	}
	if got["MSFT"].Symbol != "MSFT" {
		t.Fatalf("MSFT.Symbol: got %q", got["MSFT"].Symbol)
	}
	if got["AAPL"].Symbol != "AAPL" {
		t.Fatalf("AAPL.Symbol: got %q", got["AAPL"].Symbol)
	}
}

func TestBundleYFinanceGateway_GetKeyStatistics_NotInBundle_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleYFinanceGateway(t.TempDir(), ModeRaw)
	_, err := gw.GetKeyStatistics(context.Background(), "AAPL")
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

// TestBundleYFinanceGateway_GetAnalystEstimates_FileAbsent_ReturnsErrBundleMissingPayload
// pins the backward-compat path: pre-bundle_version-1.1 bundles do not
// contain the analyst snapshot file, so GetAnalystEstimates must surface
// ErrBundleMissingPayload (the growth blender then falls back to
// historical-only growth). Previously this was the unconditional behavior
// — that has changed; the new behavior is in
// TestBundleYFinanceGateway_GetAnalystEstimates_FilePresent_ReturnsBundledEstimates
// below.
func TestBundleYFinanceGateway_GetAnalystEstimates_FileAbsent_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleYFinanceGateway(t.TempDir(), ModeRaw)
	_, err := gw.GetAnalystEstimates(context.Background(), "AAPL")
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

// TestBundleYFinanceGateway_GetAnalystEstimates_FilePresent_ReturnsBundledEstimates
// pins the new bundle_version-1.1 behavior: when the snapshot file is
// present (captured by the YFinance gateway's makeEarningsTrendRequest tap),
// the replay gateway projects the raw envelope into
// ports.YFinanceAnalystEstimates so the growth blender's `analyst_blend`
// branch matches the original capture. Closes the QA-identified gap that
// caused replay to flip `growth_source` from analyst_blend to
// historical_only on freshly captured bundles.
func TestBundleYFinanceGateway_GetAnalystEstimates_FilePresent_ReturnsBundledEstimates(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, analystRawFile, makeAnalystRaw(t))

	gw := NewBundleYFinanceGateway(tmpDir, ModeRaw)
	got, err := gw.GetAnalystEstimates(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("GetAnalystEstimates: %v", err)
	}
	if got == nil {
		t.Fatalf("GetAnalystEstimates: expected non-nil estimates")
	}
	// Field values mirror makeAnalystRaw's fixture.
	if got.RevenueEstimateCurrentYear != 100e9 {
		t.Fatalf("RevenueEstimateCurrentYear: want 100e9, got %v", got.RevenueEstimateCurrentYear)
	}
	if got.RevenueEstimateNextYear != 120e9 {
		t.Fatalf("RevenueEstimateNextYear: want 120e9, got %v", got.RevenueEstimateNextYear)
	}
	if got.EarningsGrowth5Year != 0.15 {
		t.Fatalf("EarningsGrowth5Year: want 0.15, got %v", got.EarningsGrowth5Year)
	}
	if got.NumberOfAnalysts != 25 {
		t.Fatalf("NumberOfAnalysts: want 25, got %d", got.NumberOfAnalysts)
	}
}

// TestBundleYFinanceGateway_GetAnalystEstimates_ParsedMode_ReturnsBundledEstimates
// mirrors the raw-mode pin for the --from=parsed dispatch path. The
// producer's tap snapshots the same envelope under the .parsed.json
// sibling, so the projector logic is symmetric.
func TestBundleYFinanceGateway_GetAnalystEstimates_ParsedMode_ReturnsBundledEstimates(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, analystParsedFile, makeAnalystRaw(t))

	gw := NewBundleYFinanceGateway(tmpDir, ModeParsed)
	got, err := gw.GetAnalystEstimates(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("GetAnalystEstimates: %v", err)
	}
	if got == nil || got.EarningsGrowth5Year != 0.15 {
		t.Fatalf("parsed-mode projection: got %+v", got)
	}
}

func TestBundleYFinanceGateway_GetHistoricalPrices_NotInBundle_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleYFinanceGateway(t.TempDir(), ModeRaw)
	_, err := gw.GetHistoricalPrices(context.Background(), "AAPL", 252)
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleYFinanceGateway_GetQuote_MissingFile_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleYFinanceGateway(t.TempDir(), ModeRaw)
	_, err := gw.GetQuote(context.Background(), "AAPL")
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

// Compile-time interface conformance.
var _ ports.YFinanceGateway = (*BundleYFinanceGateway)(nil)
