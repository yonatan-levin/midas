package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// marketRawFile and marketParsedFile are the canonical bundle filenames for
// the market-fetch phase. They mirror the producer in
// internal/infra/gateways/market/yfinance_client.go (Snapshot/SnapshotRaw
// calls). marketParsedFile carries a serialized ports.YFinanceQuote (the
// production code calls b.Snapshot(ctx, "fetch.market", "06-fetch-market.parsed.json", &quote)).
const (
	marketRawFile    = "06-fetch-market.raw.json"
	marketParsedFile = "06-fetch-market.parsed.json"
)

// rawQuoteEnvelope mirrors the production YFinanceQuoteResponse shape so
// raw-mode reads of 06-fetch-market.raw.json (which is the unwrapped Yahoo
// Finance API JSON, not just the inner quote) decode the same way the
// production parser does. Spec D3 invariant: raw-mode replay must invoke
// the production parsing code path; we satisfy that by mirroring the same
// json shape and the same first-result extraction.
type rawQuoteEnvelope struct {
	QuoteResponse struct {
		Result []ports.YFinanceQuote `json:"result"`
	} `json:"quoteResponse"`
}

// BundleMarketGateway is the bundle-backed replay implementation of
// ports.MarketDataGateway. Like BundleSECGateway it serves quote data from
// a captured bundle directory in raw or parsed mode.
//
// YFinance secondary surface — design note (per plan §3 A.2 trade-off):
// the production *market.Gateway exposes a *YFinanceClient field that the
// valuation service casts to via interface{}.(*market.Gateway) in the DI
// container (container.go:668). Replay cannot supply that concrete type
// without constructing a real YFinance client. Instead, we expose a
// SEPARATE BundleYFinanceGateway (in gateway_yfinance.go) that satisfies
// ports.YFinanceGateway directly, and the replay fx Module wires it via
// *valuation.Service.SetYFinanceGateway(gw) in a post-construct fx.Invoke
// hook (Stage C). Service's existing YFinance plumbing is post-construct
// (SetYFinanceGateway is exported), so this composition works without any
// production change.
//
// Goroutine-safety: same contract as BundleSECGateway. Immutable fields,
// atomic call counter, no internal mutex.
type BundleMarketGateway struct {
	bundleDir string
	mode      Mode

	callsCount uint64
}

// NewBundleMarketGateway constructs a replay-mode market data gateway over
// the supplied bundle directory.
func NewBundleMarketGateway(bundleDir string, mode Mode) *BundleMarketGateway {
	return &BundleMarketGateway{
		bundleDir: bundleDir,
		mode:      mode,
	}
}

// CallsCount returns the total number of method invocations across all
// exported methods. Test-only.
func (g *BundleMarketGateway) CallsCount() uint64 {
	return atomic.LoadUint64(&g.callsCount)
}

// GetQuote returns the bundled quote re-shaped as *entities.MarketData,
// mirroring the production market.Gateway.getMarketDataFromYFinance
// conversion at gateway.go:228-244 (Ticker, SharePrice, MarketCap,
// SharesOutstanding, Beta, AverageVolume, AsOf, Source="yfinance",
// DataQuality). Only the headline fields the engine consumes are
// stamped — the production helper assessDataQuality is replicated as a
// trivial constant ("good") since replay does not have access to the
// quote object beyond its bundle-captured state.
//
// In ModeRaw the gateway decodes the wrapped Yahoo Finance API JSON
// (quoteResponse.result[0]) — the same shape production decodes via
// json.Decoder over the live response body.
//
// In ModeParsed the bundle stamped the inner ports.YFinanceQuote directly
// via b.Snapshot(ctx, "fetch.market", "06-fetch-market.parsed.json", &quote);
// we unmarshal that and re-shape into MarketData.
func (g *BundleMarketGateway) GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error) {
	atomic.AddUint64(&g.callsCount, 1)

	quote, err := g.readQuote(g.mode)
	if err != nil {
		return nil, err
	}
	return quoteToMarketData(ticker, quote), nil
}

// GetQuotes is the batch variant. Replay bundles capture a single ticker
// per request, so the captured quote is returned for every requested
// ticker (callers expecting per-ticker uniqueness are out of replay's
// hermeticity contract — use multi-bundle replay for fan-out).
func (g *BundleMarketGateway) GetQuotes(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	atomic.AddUint64(&g.callsCount, 1)

	quote, err := g.readQuote(g.mode)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*entities.MarketData, len(tickers))
	for _, t := range tickers {
		out[t] = quoteToMarketData(t, quote)
	}
	return out, nil
}

// GetHistoricalPrices returns ErrBundleMissingPayload — historical-prices
// snapshots are not captured by today's bundle producers
// (yfinance_client.go only snapshots the GetQuote response). The valuation
// engine's beta-fallback path (market.Gateway.calculateBetaFromHistoricalData)
// is reached only when a quote's Beta is invalid AND key-stats fall back
// also fail; in replay that path's error is non-fatal — beta degrades to
// the default and the engine continues.
func (g *BundleMarketGateway) GetHistoricalPrices(ctx context.Context, ticker string, startDate, endDate time.Time) ([]*entities.PriceData, error) {
	atomic.AddUint64(&g.callsCount, 1)
	return nil, NewBundleMissingPayloadError(g.bundleDir, "06-fetch-market-historical.raw.json", nil)
}

// HealthCheck always succeeds in replay (see BundleSECGateway.HealthCheck).
func (g *BundleMarketGateway) HealthCheck(ctx context.Context) error {
	atomic.AddUint64(&g.callsCount, 1)
	return nil
}

// readQuote dispatches to the correct mode and returns the captured
// ports.YFinanceQuote. Used by GetQuote / GetQuotes / shared with
// BundleYFinanceGateway via package scope.
func (g *BundleMarketGateway) readQuote(mode Mode) (*ports.YFinanceQuote, error) {
	switch mode {
	case ModeRaw:
		body, err := readBundlePayload(g.bundleDir, marketRawFile)
		if err != nil {
			return nil, err
		}
		var env rawQuoteEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("replay: BundleMarketGateway: parse raw payload: %w", err)
		}
		if len(env.QuoteResponse.Result) == 0 {
			return nil, fmt.Errorf("replay: BundleMarketGateway: raw payload has no quoteResponse.result")
		}
		quote := env.QuoteResponse.Result[0]
		return &quote, nil

	case ModeParsed:
		body, err := readBundlePayload(g.bundleDir, marketParsedFile)
		if err != nil {
			return nil, err
		}
		var quote ports.YFinanceQuote
		if err := json.Unmarshal(body, &quote); err != nil {
			return nil, fmt.Errorf("replay: BundleMarketGateway: parse parsed payload: %w", err)
		}
		return &quote, nil

	default:
		return nil, fmt.Errorf("replay: BundleMarketGateway: unknown mode %d", mode)
	}
}

// quoteToMarketData mirrors the production market.Gateway helper at
// gateway.go:234-244. Replay does NOT call back into key-stats fallbacks
// (those are not in the bundle); when Beta or SharesOutstanding is zero
// the engine handles it via its existing degraded-data paths.
//
// DataQuality is hard-coded to "good" because the production helper
// assessDataQuality consults the quote's averageDailyVolume3M and
// regularMarketVolume — both already on the captured quote — but the
// helper itself is unexported. Replicating it here would couple replay to
// the helper's heuristics; for R2 we accept "good" as the constant since
// the engine consumes DataQuality only as a freshness annotation, not for
// math. R3 may revisit if a diff surfaces.
func quoteToMarketData(ticker string, quote *ports.YFinanceQuote) *entities.MarketData {
	asOf := time.Unix(quote.RegularMarketTime, 0).UTC()
	return &entities.MarketData{
		Ticker:            ticker,
		SharePrice:        quote.RegularMarketPrice,
		MarketCap:         quote.MarketCap,
		SharesOutstanding: quote.SharesOutstanding,
		Beta:              quote.Beta,
		AverageVolume:     quote.AverageDailyVolume3M,
		AsOf:              asOf,
		Source:            "yfinance",
		DataQuality:       "good",
	}
}

// readBundlePayload is the shared file-read helper used by all bundle
// gateways. Centralized so the missing-file → ErrBundleMissingPayload
// classification is uniform; non-missing fs errors are wrapped with
// gateway-agnostic context.
func readBundlePayload(bundleDir, relativePath string) ([]byte, error) {
	full := filepath.Join(bundleDir, relativePath)
	body, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, NewBundleMissingPayloadError(bundleDir, relativePath, err)
		}
		return nil, fmt.Errorf("replay: read %s: %w", relativePath, err)
	}
	return body, nil
}
