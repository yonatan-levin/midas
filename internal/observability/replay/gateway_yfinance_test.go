package replay

import (
	"context"
	"errors"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

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

func TestBundleYFinanceGateway_GetAnalystEstimates_NotInBundle_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleYFinanceGateway(t.TempDir(), ModeRaw)
	_, err := gw.GetAnalystEstimates(context.Background(), "AAPL")
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
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
