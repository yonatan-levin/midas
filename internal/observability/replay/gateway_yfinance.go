package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// analystRawFile / analystParsedFile mirror the snapshot filenames stamped
// by the YFinance gateway's makeEarningsTrendRequest tap. Added to the
// bundle layout in bundle_version 1.1; pre-1.1 bundles do not contain
// these files and the replay-side reader returns ErrBundleMissingPayload
// (the production growth blender then falls back to historical-only).
const (
	analystRawFile    = "06-fetch-market-analyst.raw.json"
	analystParsedFile = "06-fetch-market-analyst.parsed.json"
)

// rawAnalystEnvelope mirrors the shape of market.YFinanceEarningsTrendResponse
// without depending on the production market package. The replay package
// intentionally avoids importing infra/gateways/market to keep the
// dependency direction one-way (replay reads, never depends on gateway
// internals).
type rawAnalystEnvelope struct {
	QuoteSummary struct {
		Result []struct {
			EarningsTrend *struct {
				Trend []struct {
					Period           string         `json:"period"`
					RevenueEstimate  *analystEstSet `json:"revenueEstimate"`
					EarningsEstimate *analystEstSet `json:"earningsEstimate"`
					Growth           *analystValue  `json:"growth"`
				} `json:"trend"`
			} `json:"earningsTrend"`
		} `json:"result"`
	} `json:"quoteSummary"`
}

type analystEstSet struct {
	Avg              *analystValue `json:"avg"`
	Low              *analystValue `json:"low"`
	High             *analystValue `json:"high"`
	NumberOfAnalysts *analystValue `json:"numberOfAnalysts"`
}

type analystValue struct {
	Raw *float64 `json:"raw"`
	Fmt string   `json:"fmt"`
}

// projectAnalystEstimates extracts ports.YFinanceAnalystEstimates from the
// raw envelope by walking the Trend[] entries. Logic mirrors the
// production projector in market.YFinanceClient.GetAnalystEstimates
// (yfinance_client.go:587-615): pick "0y" (current year), "+1y" (next year)
// and "+5y" (long-term growth) entries.
func projectAnalystEstimates(env *rawAnalystEnvelope) *ports.YFinanceAnalystEstimates {
	if env == nil || len(env.QuoteSummary.Result) == 0 || env.QuoteSummary.Result[0].EarningsTrend == nil {
		return nil
	}
	estimates := &ports.YFinanceAnalystEstimates{}
	for _, entry := range env.QuoteSummary.Result[0].EarningsTrend.Trend {
		switch entry.Period {
		case "0y":
			if entry.RevenueEstimate != nil {
				if entry.RevenueEstimate.Avg != nil && entry.RevenueEstimate.Avg.Raw != nil {
					estimates.RevenueEstimateCurrentYear = *entry.RevenueEstimate.Avg.Raw
				}
				if entry.RevenueEstimate.Low != nil && entry.RevenueEstimate.Low.Raw != nil {
					estimates.RevenueEstimateLow = *entry.RevenueEstimate.Low.Raw
				}
				if entry.RevenueEstimate.High != nil && entry.RevenueEstimate.High.Raw != nil {
					estimates.RevenueEstimateHigh = *entry.RevenueEstimate.High.Raw
				}
				if entry.RevenueEstimate.NumberOfAnalysts != nil && entry.RevenueEstimate.NumberOfAnalysts.Raw != nil {
					estimates.NumberOfAnalysts = int(*entry.RevenueEstimate.NumberOfAnalysts.Raw)
				}
			}
		case "+1y":
			if entry.RevenueEstimate != nil && entry.RevenueEstimate.Avg != nil && entry.RevenueEstimate.Avg.Raw != nil {
				estimates.RevenueEstimateNextYear = *entry.RevenueEstimate.Avg.Raw
			}
		case "+5y":
			if entry.Growth != nil && entry.Growth.Raw != nil {
				estimates.EarningsGrowth5Year = *entry.Growth.Raw
			}
		}
	}
	return estimates
}

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

// GetAnalystEstimates returns the bundled analyst estimates when present.
// Resolution depends on the gateway's Mode:
//
//   - ModeRaw    → reads `06-fetch-market-analyst.raw.json` (the raw Yahoo
//     v10 quoteSummary?modules=earningsTrend response envelope) and runs
//     projectAnalystEstimates to extract ports.YFinanceAnalystEstimates.
//     This mirrors production's market.YFinanceClient.GetAnalystEstimates
//     projection so a `--from=raw` replay exercises the same path.
//   - ModeParsed → reads `06-fetch-market-analyst.parsed.json` (the same
//     raw envelope — production snapshots the envelope via Snapshot, not
//     a pre-projected struct) and runs the same projector. The two modes
//     are deliberately symmetric because the producer captures envelope
//     bytes in BOTH cases.
//
// Backward compat: pre-bundle_version-1.1 bundles do not contain these
// files. Missing-file errors propagate as ErrBundleMissingPayload —
// the growth estimator's blender treats that as a signal to fall back
// to historical-only growth (same path production runs when YFinance
// throttles the v10 endpoint), so replay reproduces the captured
// degraded behavior rather than diverging.
func (g *BundleYFinanceGateway) GetAnalystEstimates(ctx context.Context, ticker string) (*ports.YFinanceAnalystEstimates, error) {
	atomic.AddUint64(&g.callsCount, 1)

	// Pick the per-mode filename. Both files contain the same envelope
	// shape (Yahoo's raw v10 response) because the producer's tap
	// snapshots the envelope identically for raw and parsed sinks.
	var filename string
	switch g.mode {
	case ModeRaw:
		filename = analystRawFile
	case ModeParsed:
		filename = analystParsedFile
	default:
		return nil, fmt.Errorf("replay: BundleYFinanceGateway: unknown mode %d", g.mode)
	}

	body, err := readBundlePayload(g.bundleDir, filename)
	if err != nil {
		// Preserve the ErrBundleMissingPayload sentinel — callers use
		// errors.Is to detect "no analyst data, fall back to historical".
		if errors.Is(err, ErrBundleMissingPayload) {
			return nil, err
		}
		return nil, fmt.Errorf("replay: BundleYFinanceGateway GetAnalystEstimates: %w", err)
	}

	var env rawAnalystEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("replay: BundleYFinanceGateway GetAnalystEstimates: parse %s: %w", filename, err)
	}
	estimates := projectAnalystEstimates(&env)
	// nil is a legal return (no analyst data available) — production
	// returns (nil, nil) in the same case. Keep that contract.
	return estimates, nil
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
