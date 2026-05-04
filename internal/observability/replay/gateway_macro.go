package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/macro"
)

// macroParsedFile is the canonical aggregated bundle filename for the
// macro-fetch phase (parsed mode). It mirrors macro.Gateway.GetTreasuryRates
// at gateway.go:115 (b.Snapshot(ctx, "fetch.macro", "07-fetch-macro.parsed.json", rates)).
const macroParsedFile = "07-fetch-macro.parsed.json"

// macroSeriesMap mirrors the production macro.Gateway.getTreasuryRatesFromFRED
// seriesMap exactly (see internal/infra/gateways/macro/gateway.go:181-191).
// Re-declared here so replay's raw-mode dispatch walks the same FRED series
// IDs the production code does. Drift between this map and the production
// one is caught in two layers:
//
//   - the raw-mode round-trip integration test (Stage F) replays a bundle
//     produced by the production gateway; if production stamped a series ID
//     replay's map omits, the rate value silently falls to zero in the
//     reconstructed TreasuryRates and the response diff fails.
//   - reviewers verify the two maps stay in lockstep on any FRED-series
//     change (see plan §4 OQ2).
var macroSeriesMap = map[string]string{
	"DGS1MO": "yield_1_month",
	"DGS3MO": "yield_3_month",
	"DGS6MO": "yield_6_month",
	"DGS1":   "yield_1_year",
	"DGS2":   "yield_2_year",
	"DGS5":   "yield_5_year",
	"DGS10":  "yield_10_year",
	"DGS20":  "yield_20_year",
	"DGS30":  "yield_30_year",
}

// BundleMacroGateway is the bundle-backed replay implementation of
// ports.MacroDataGateway. ModeRaw walks every FRED series file
// (07-fetch-macro-<seriesID>.raw.json) and dispatches each through the
// extracted production parser macro.ParseFREDSeries (Stage A.6 made this
// extraction; before that, the parsing logic was inline in
// (*Gateway).getFREDSeries — replay would have had to duplicate it,
// risking silent drift). ModeParsed reads a single aggregated
// 07-fetch-macro.parsed.json and unmarshal's into *entities.TreasuryRates.
//
// FRED returns rates as percentages; the production gateway divides by
// 100.0 before stamping the field (gateway.go:208 `rate := value / 100.0`).
// Replay applies the same conversion so the resulting TreasuryRates is
// byte-identical regardless of mode.
//
// Per-series tolerance: production tolerates a missing or unparseable
// series (gateway.go:200-204 logs a warn and continues). Replay does the
// same; a missing-file for ONE series leaves that field zero and the loop
// continues. ErrBundleMissingPayload is returned only when EVERY series
// file is absent — the engine cannot proceed without any treasury rate.
type BundleMacroGateway struct {
	bundleDir string
	mode      Mode

	callsCount uint64
}

// NewBundleMacroGateway constructs a replay-mode macro gateway.
func NewBundleMacroGateway(bundleDir string, mode Mode) *BundleMacroGateway {
	return &BundleMacroGateway{
		bundleDir: bundleDir,
		mode:      mode,
	}
}

// CallsCount is test-only telemetry.
func (g *BundleMacroGateway) CallsCount() uint64 {
	return atomic.LoadUint64(&g.callsCount)
}

// GetTreasuryRates returns the bundled treasury yield curve. Behavior
// depends on the gateway's Mode (see type doc).
func (g *BundleMacroGateway) GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error) {
	atomic.AddUint64(&g.callsCount, 1)

	switch g.mode {
	case ModeRaw:
		// Walk each FRED series file and dispatch through the production
		// parser. Per-series misses are tolerated; missing ALL files is
		// an error.
		rates := &entities.TreasuryRates{
			AsOf: time.Now().UTC(),
		}
		var present int
		for seriesID, fieldName := range macroSeriesMap {
			fname := fmt.Sprintf("07-fetch-macro-%s.raw.json", seriesID)
			body, err := readBundlePayload(g.bundleDir, fname)
			if err != nil {
				// Missing-file: tolerate per series. Real parse errors
				// also tolerated here (production logs+continues).
				continue
			}
			value, parseErr := macro.ParseFREDSeries(seriesID, body)
			if parseErr != nil {
				continue
			}
			present++
			rate := value / 100.0
			assignTreasuryField(rates, fieldName, rate)
		}
		if present == 0 {
			// No FRED files at all — fall through to ErrBundleMissingPayload
			// so callers can detect a fully-absent macro phase distinctly
			// from "FRED captured but every series happened to be empty".
			return nil, NewBundleMissingPayloadError(g.bundleDir, "07-fetch-macro-*.raw.json", nil)
		}
		return rates, nil

	case ModeParsed:
		body, err := readBundlePayload(g.bundleDir, macroParsedFile)
		if err != nil {
			return nil, err
		}
		var rates entities.TreasuryRates
		if err := json.Unmarshal(body, &rates); err != nil {
			return nil, fmt.Errorf("replay: BundleMacroGateway: parse parsed payload: %w", err)
		}
		return &rates, nil

	default:
		return nil, fmt.Errorf("replay: BundleMacroGateway: unknown mode %d", g.mode)
	}
}

// GetMarketRiskPremium returns ErrBundleMissingPayload — bundles do not
// capture an MRP file because production reads it from configuration
// (config.Macro.ManualMarketRiskPremium), not from FRED. The valuation
// service path SHOULD reach this method through macro.Gateway.GetMarketRiskPremium
// which always succeeds via the config default; replay returns
// ErrBundleMissingPayload to surface the contract gap loudly so a future
// FRED-MRP wiring forces the bundle producer to capture it.
//
// Practical note: if an existing engine path treats MRP fetch failure as
// fatal during replay, the round-trip integration test (Stage F) will
// surface that and we revisit. Today's production code returns
// (cfg.ManualMarketRiskPremium, nil) — never errors — so the engine path
// has no error-handling for this method to exercise; replay returning the
// sentinel is functionally equivalent to "engine consumes the wrong
// number" which the response diff will detect.
func (g *BundleMacroGateway) GetMarketRiskPremium(ctx context.Context) (float64, error) {
	atomic.AddUint64(&g.callsCount, 1)
	return 0, NewBundleMissingPayloadError(g.bundleDir, "07-fetch-macro-mrp.json", nil)
}

// GetFXRate honors the identity short-circuit per ports.MacroDataGateway
// docstring (gateways.go:118): fromCcy == toCcy returns 1.0 with no I/O.
// All other pairs return ErrBundleMissingPayload — bundles do not capture
// FX rates today.
//
// Engine path: the FX gateway is exercised in valuation/currency.go
// convertFinancialsToUSD only when a period's ReportingCurrency != "USD".
// For domestic US filers (the vast majority of test bundles), only the
// identity short-circuit fires, so replay produces the same result as
// production. For ADR / FPI bundles where FX is needed, the round-trip
// integration test (Stage F) will surface the gap if the engine treats it
// as fatal.
func (g *BundleMacroGateway) GetFXRate(ctx context.Context, fromCcy, toCcy string) (float64, error) {
	atomic.AddUint64(&g.callsCount, 1)
	if fromCcy == toCcy {
		return 1.0, nil
	}
	return 0, NewBundleMissingPayloadError(g.bundleDir, fmt.Sprintf("07-fetch-fx-%s-%s.json", fromCcy, toCcy), nil)
}

// HealthCheck always succeeds in replay.
func (g *BundleMacroGateway) HealthCheck(ctx context.Context) error {
	atomic.AddUint64(&g.callsCount, 1)
	return nil
}

// assignTreasuryField mirrors the production switch at gateway.go:211-230,
// stamping the parsed FRED value into the appropriate TreasuryRates field
// using the same fieldName tags.
func assignTreasuryField(rates *entities.TreasuryRates, fieldName string, rate float64) {
	switch fieldName {
	case "yield_1_month":
		rates.Yield1Month = rate
	case "yield_3_month":
		rates.Yield3Month = rate
	case "yield_6_month":
		rates.Yield6Month = rate
	case "yield_1_year":
		rates.Yield1Year = rate
	case "yield_2_year":
		rates.Yield2Year = rate
	case "yield_5_year":
		rates.Yield5Year = rate
	case "yield_10_year":
		rates.Yield10Year = rate
	case "yield_20_year":
		rates.Yield20Year = rate
	case "yield_30_year":
		rates.Yield30Year = rate
	}
}
