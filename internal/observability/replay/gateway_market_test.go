package replay

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// makeMarketRaw produces a minimal Yahoo Finance v7 quote envelope JSON
// matching the production YFinanceQuoteResponse decode shape.
func makeMarketRaw(t *testing.T, ticker string) []byte {
	t.Helper()
	env := map[string]interface{}{
		"quoteResponse": map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"symbol":                   ticker,
					"regularMarketPrice":       190.0,
					"marketCap":                3.0e12,
					"sharesOutstanding":        1.5e10,
					"regularMarketVolume":      5.5e7,
					"averageDailyVolume3Month": 6.0e7,
					"beta":                     1.25,
					"currency":                 "USD",
					"marketState":              "REGULAR",
					"regularMarketTime":        int64(1700000000),
				},
			},
			"error": nil,
		},
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal market raw: %v", err)
	}
	return body
}

// makeMarketParsed produces a minimal ports.YFinanceQuote JSON body — the
// shape produced by yfinance_client.go's b.Snapshot(...) of `&quote`.
func makeMarketParsed(t *testing.T, ticker string) []byte {
	t.Helper()
	q := ports.YFinanceQuote{
		Symbol:               ticker,
		RegularMarketPrice:   190.0,
		MarketCap:            3.0e12,
		SharesOutstanding:    1.5e10,
		RegularMarketVolume:  5.5e7,
		AverageDailyVolume3M: 6.0e7,
		Beta:                 1.25,
		Currency:             "USD",
		MarketState:          "REGULAR",
		RegularMarketTime:    1700000000,
	}
	body, err := json.Marshal(&q)
	if err != nil {
		t.Fatalf("marshal market parsed: %v", err)
	}
	return body
}

func TestBundleMarketGateway_GetQuote_RawMode_ParsesProductionBytes(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, marketRawFile, makeMarketRaw(t, "AAPL"))

	gw := NewBundleMarketGateway(tmpDir, ModeRaw)
	got, err := gw.GetQuote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if got.Ticker != "AAPL" {
		t.Fatalf("Ticker: want AAPL, got %q", got.Ticker)
	}
	if got.SharePrice != 190.0 {
		t.Fatalf("SharePrice: want 190.0, got %v", got.SharePrice)
	}
	if got.Beta != 1.25 {
		t.Fatalf("Beta: want 1.25, got %v", got.Beta)
	}
	if got.Source != "yfinance" {
		t.Fatalf("Source: want yfinance, got %q", got.Source)
	}
}

func TestBundleMarketGateway_GetQuote_ParsedMode_DirectUnmarshal(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, marketParsedFile, makeMarketParsed(t, "AAPL"))

	gw := NewBundleMarketGateway(tmpDir, ModeParsed)
	got, err := gw.GetQuote(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if got.SharePrice != 190.0 {
		t.Fatalf("SharePrice: want 190.0, got %v", got.SharePrice)
	}
}

func TestBundleMarketGateway_GetQuote_MissingFile_ReturnsErrBundleMissingPayload(t *testing.T) {
	tmpDir := t.TempDir()
	gw := NewBundleMarketGateway(tmpDir, ModeRaw)
	_, err := gw.GetQuote(context.Background(), "AAPL")
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMarketGateway_GetQuotes_BatchSpansAllRequestedTickers(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, marketRawFile, makeMarketRaw(t, "AAPL"))

	gw := NewBundleMarketGateway(tmpDir, ModeRaw)
	got, err := gw.GetQuotes(context.Background(), []string{"AAPL", "MSFT"})
	if err != nil {
		t.Fatalf("GetQuotes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: want 2, got %d", len(got))
	}
	for _, ticker := range []string{"AAPL", "MSFT"} {
		md, ok := got[ticker]
		if !ok {
			t.Fatalf("missing ticker in result: %s", ticker)
		}
		if md.Ticker != ticker {
			t.Fatalf("Ticker mismatch in %s: got %q", ticker, md.Ticker)
		}
	}
}

func TestBundleMarketGateway_GetHistoricalPrices_NotInBundle_ReturnsErrBundleMissingPayload(t *testing.T) {
	gw := NewBundleMarketGateway(t.TempDir(), ModeRaw)
	_, err := gw.GetHistoricalPrices(context.Background(), "AAPL", time.Now().AddDate(-1, 0, 0), time.Now())
	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Fatalf("expected ErrBundleMissingPayload; got %v", err)
	}
}

func TestBundleMarketGateway_HealthCheck_AlwaysOK(t *testing.T) {
	gw := NewBundleMarketGateway(t.TempDir(), ModeRaw)
	if err := gw.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestBundleMarketGateway_ConcurrentGetQuote_RaceFree(t *testing.T) {
	tmpDir := t.TempDir()
	seedBundleFile(t, tmpDir, marketRawFile, makeMarketRaw(t, "AAPL"))

	gw := NewBundleMarketGateway(tmpDir, ModeRaw)

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := gw.GetQuote(context.Background(), "AAPL"); err != nil {
				t.Errorf("concurrent GetQuote: %v", err)
			}
		}()
	}
	wg.Wait()
	if gw.CallsCount() != N {
		t.Fatalf("CallsCount: want %d, got %d", N, gw.CallsCount())
	}
}

// Compile-time interface conformance.
var _ ports.MarketDataGateway = (*BundleMarketGateway)(nil)
