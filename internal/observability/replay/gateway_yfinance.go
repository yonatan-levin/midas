package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// BundleYFinanceGateway is the bundle-backed replay implementation of
// ports.YFinanceGateway. Wired separately from BundleMarketGateway because
// production's *valuation.Service consumes a YFinanceGateway through
// post-construct injection (Service.SetYFinanceGateway), not through the
// MarketDataGateway interface.
//
// The bundle's `06-fetch-market.{raw,parsed}.json` files are the only
// market data captured today. Quote endpoints (GetQuote/GetBatchQuotes)
// reuse those via the same shared decode logic in gateway_market.go.
// Endpoints whose snapshots are NOT captured by production
// (GetKeyStatistics, GetAnalystEstimates, GetHistoricalPrices) return
// ErrBundleMissingPayload — the engine path treats YFinance's analyst
// estimates and key-stats lookups as best-effort (production allows them
// to fail silently and proceeds with historical-only growth + the
// quote-derived beta), so replay tolerating those misses preserves
// production semantics. See plan §3 OQ3 for the verification of this
// behavior.
type BundleYFinanceGateway struct {
	bundleDir string
	mode      Mode

	callsCount uint64
}

// NewBundleYFinanceGateway constructs a YFinance bundle gateway. The same
// bundleDir + mode the BundleMarketGateway is constructed with — both
// gateways read the same captured bytes for overlapping endpoints.
func NewBundleYFinanceGateway(bundleDir string, mode Mode) *BundleYFinanceGateway {
	return &BundleYFinanceGateway{
		bundleDir: bundleDir,
		mode:      mode,
	}
}

// CallsCount is test-only telemetry, mirroring the other bundle gateways.
func (g *BundleYFinanceGateway) CallsCount() uint64 {
	return atomic.LoadUint64(&g.callsCount)
}

// GetQuote returns the bundled YFinanceQuote. Reads the same files as
// BundleMarketGateway (06-fetch-market.{raw,parsed}.json) — this method
// is the lower-level surface the valuation service consults via
// SetYFinanceGateway, not via MarketDataGateway.
func (g *BundleYFinanceGateway) GetQuote(ctx context.Context, ticker string) (*ports.YFinanceQuote, error) {
	atomic.AddUint64(&g.callsCount, 1)
	return readBundleQuote(g.bundleDir, g.mode)
}

// GetBatchQuotes returns the captured quote keyed by every requested
// ticker, mirroring BundleMarketGateway.GetQuotes. The engine's growth
// estimator never invokes batch quotes today; this is here purely for
// interface conformance.
func (g *BundleYFinanceGateway) GetBatchQuotes(ctx context.Context, tickers []string) (map[string]*ports.YFinanceQuote, error) {
	atomic.AddUint64(&g.callsCount, 1)
	quote, err := readBundleQuote(g.bundleDir, g.mode)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*ports.YFinanceQuote, len(tickers))
	for _, t := range tickers {
		// Stamp Symbol so callers that round-trip the map see the right
		// ticker; production sets the field server-side before returning.
		// RPL-2m (R3 Stage O.11): variable renamed from `copy` to `dup`
		// so it no longer shadows the built-in `copy` function.
		dup := *quote
		dup.Symbol = t
		out[t] = &dup
	}
	return out, nil
}

// GetKeyStatistics returns ErrBundleMissingPayload — bundles do not capture
// the v10 quoteSummary endpoint today. Production reaches this method only
// via the Beta / SharesOutstanding fallback in
// market.Gateway.getMarketDataFromYFinance; that fallback handles errors
// gracefully (just leaves the field at its previous value), so replay
// returning the missing-payload sentinel produces the same downstream
// shape as a live network failure.
func (g *BundleYFinanceGateway) GetKeyStatistics(ctx context.Context, ticker string) (*ports.YFinanceKeyStats, error) {
	atomic.AddUint64(&g.callsCount, 1)
	return nil, NewBundleMissingPayloadError(g.bundleDir, "06-fetch-market-keystats.raw.json", nil)
}

// GetHistoricalPrices returns ErrBundleMissingPayload — historical chart
// data is not captured. The engine's only consumer is
// market.Gateway.calculateBetaFromHistoricalData, a tertiary beta fallback
// that production tolerates failing.
func (g *BundleYFinanceGateway) GetHistoricalPrices(ctx context.Context, ticker string, days int) ([]ports.YFinancePricePoint, error) {
	atomic.AddUint64(&g.callsCount, 1)
	return nil, NewBundleMissingPayloadError(g.bundleDir, "06-fetch-market-history.raw.json", nil)
}

// GetAnalystEstimates returns ErrBundleMissingPayload — earnings-trend
// data is not captured. The growth estimator's blend logic (services/growth/
// estimator.go) treats analyst estimates as optional; when this method
// errors it falls back to historical-only growth, which is the same
// degraded path production runs when YFinance throttles the v10 endpoint.
func (g *BundleYFinanceGateway) GetAnalystEstimates(ctx context.Context, ticker string) (*ports.YFinanceAnalystEstimates, error) {
	atomic.AddUint64(&g.callsCount, 1)
	return nil, NewBundleMissingPayloadError(g.bundleDir, "06-fetch-market-analyst.raw.json", nil)
}

// readBundleQuote is shared with BundleMarketGateway: both gateways read
// the same captured bytes. Centralized so the raw/parsed decode is
// identical at the byte level — drift between the two would silently
// produce two different MarketData stamps for the same captured quote.
func readBundleQuote(bundleDir string, mode Mode) (*ports.YFinanceQuote, error) {
	switch mode {
	case ModeRaw:
		body, err := readBundlePayload(bundleDir, marketRawFile)
		if err != nil {
			return nil, err
		}
		var env rawQuoteEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("replay: BundleYFinanceGateway: parse raw payload: %w", err)
		}
		if len(env.QuoteResponse.Result) == 0 {
			return nil, fmt.Errorf("replay: BundleYFinanceGateway: raw payload has no quoteResponse.result")
		}
		quote := env.QuoteResponse.Result[0]
		return &quote, nil

	case ModeParsed:
		body, err := readBundlePayload(bundleDir, marketParsedFile)
		if err != nil {
			return nil, err
		}
		var quote ports.YFinanceQuote
		if err := json.Unmarshal(body, &quote); err != nil {
			return nil, fmt.Errorf("replay: BundleYFinanceGateway: parse parsed payload: %w", err)
		}
		return &quote, nil

	default:
		return nil, fmt.Errorf("replay: BundleYFinanceGateway: unknown mode %d", mode)
	}
}
