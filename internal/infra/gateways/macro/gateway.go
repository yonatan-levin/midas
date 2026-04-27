package macro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// fxCacheTTL bounds how long a cached FX rate (FRED-fetched or static-fallback)
// is reused before a refresh is attempted. Six hours matches the FRED H.10
// daily-series update cadence with comfortable headroom: even if the series
// publishes mid-window, we never serve a rate older than the typical bid/ask
// spread on G10 currencies. Phase B9 will revisit this if FX-driven
// valuations exhibit visible staleness in production.
const fxCacheTTL = 6 * time.Hour

// fxCacheEntry is the value half of the fxCache sync.Map. Pairing the rate
// with its insertion time lets reads check expiry without taking a lock; the
// sync.Map handles all concurrency on the keyspace.
type fxCacheEntry struct {
	rate     float64
	storedAt time.Time
}

// Gateway implements the MacroData gateway interface
type Gateway struct {
	config     *config.MacroConfig
	httpClient *http.Client
	logger     *zap.Logger

	// fxRatesToUSD is the static FRED H.10 snapshot loaded from
	// config/fx_rates.json at boot. Used as a fallback when the live FRED
	// API is unavailable (HTTP error, missing API key, malformed response).
	// nil/empty means "no fallback configured" — callers will surface
	// ports.ErrFXRateUnavailable when FRED is also down.
	fxRatesToUSD map[string]float64

	// fxCache memoizes successful FX-rate lookups (both FRED-sourced and
	// static-fallback) keyed by "FROM:TO". sync.Map is preferred over a
	// plain map+RWMutex because the keyspace is bounded (~30 currency pairs
	// max) and reads vastly outnumber writes after warm-up.
	fxCache sync.Map
}

// NewGateway creates a new MacroData gateway. Backward-compat shim — passes
// no static FX-rates snapshot. Production code should use
// NewGatewayWithFXRates so the static fallback survives FRED outages.
func NewGateway(cfg *config.MacroConfig, logger *zap.Logger) ports.MacroDataGateway {
	return NewGatewayWithFXRates(cfg, nil, logger)
}

// NewGatewayWithFXRates constructs a MacroData gateway with an optional
// static FX-rates fallback (USD per 1 unit of foreign currency, keyed by
// ISO-4217 code). When the map is nil or empty, GetFXRate operates in
// FRED-only mode: any FRED failure produces ports.ErrFXRateUnavailable.
//
// The map is consumed read-only — callers retain ownership but must not
// mutate it concurrently. The DI layer (internal/di/container.go) wires this
// from valuation.LoadFXRates.
func NewGatewayWithFXRates(cfg *config.MacroConfig, fxRatesToUSD map[string]float64, logger *zap.Logger) ports.MacroDataGateway {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: false,
		},
	}

	return &Gateway{
		config:       cfg,
		httpClient:   httpClient,
		logger:       logger.Named("macro-gateway"),
		fxRatesToUSD: fxRatesToUSD,
	}
}

// GetTreasuryRates retrieves current Treasury yield curve data.
//
// On success, snapshots the parsed treasury rates into the request bundle
// (when a bundle is on ctx) under fetch.macro/07-fetch-macro.parsed.json.
// FRED raw payloads are captured per-series in getFREDSeries via TeeReader.
func (g *Gateway) GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error) {
	// Tier-2 trace.gateway.macro.fred.fetch entry: log start.
	startFetch := time.Now()
	logctx.Or(ctx, g.logger).Debug("trace.gateway.macro.fred.fetch",
		zap.Bool("fred_enabled", g.config.FREDEnabled && g.config.FREDAPIKey != ""))

	// If FRED API is enabled, try to fetch from FRED first
	if g.config.FREDEnabled && g.config.FREDAPIKey != "" {
		rates, err := g.getTreasuryRatesFromFRED(ctx)
		if err == nil {
			logctx.Or(ctx, g.logger).Info("Successfully fetched treasury rates from FRED")

			// Tier-3 artifact bundle: snapshot the parsed treasury rates.
			if b := artifact.From(ctx); b != nil {
				b.Snapshot(ctx, "fetch.macro", "07-fetch-macro.parsed.json", rates)
				b.AddSchemaVersion("MacroData", 1)
			}
			logctx.Or(ctx, g.logger).Debug("trace.gateway.macro.fred.parse",
				zap.String("provider", "fred"),
				zap.Duration("elapsed", time.Since(startFetch)))

			return rates, nil
		}
		logctx.Or(ctx, g.logger).Warn("Failed to fetch from FRED, falling back to config defaults",
			zap.Error(err))
	}

	// Fallback to manual config settings as per user requirement
	rates := g.getTreasuryRatesFromConfig(ctx)
	if b := artifact.From(ctx); b != nil {
		b.Snapshot(ctx, "fetch.macro", "07-fetch-macro.parsed.json", rates)
		b.AddSchemaVersion("MacroData", 1)
	}
	logctx.Or(ctx, g.logger).Debug("trace.gateway.macro.fred.parse",
		zap.String("provider", "manual_config"),
		zap.Duration("elapsed", time.Since(startFetch)))
	return rates, nil
}

// GetMarketRiskPremium retrieves the market risk premium
func (g *Gateway) GetMarketRiskPremium(ctx context.Context) (float64, error) {
	logctx.Or(ctx, g.logger).Debug("Getting market risk premium")

	// If FRED API is enabled, could potentially fetch historical market data
	// nolint:staticcheck // placeholder until FRED integration is implemented
	if g.config.FREDEnabled && g.config.FREDAPIKey != "" {
		// TODO: Implement sophisticated MRP calculation from FRED data
		// For now, fall back to config default
	}

	// Use config-based default
	mrp := g.config.ManualMarketRiskPremium
	logctx.Or(ctx, g.logger).Debug("Using config-based market risk premium",
		zap.Float64("market_risk_premium", mrp))

	return mrp, nil
}

// HealthCheck performs a health check on the macro data gateway
func (g *Gateway) HealthCheck(ctx context.Context) error {
	logctx.Or(ctx, g.logger).Debug("Performing macro data gateway health check")

	// If FRED is enabled, test the connection
	if g.config.FREDEnabled && g.config.FREDAPIKey != "" {
		_, err := g.getTreasuryRatesFromFRED(ctx)
		if err != nil {
			logctx.Or(ctx, g.logger).Warn("FRED API health check failed, but config fallback available",
				zap.Error(err))
			// Don't fail health check if config fallback is available
		}
	}

	// Always pass health check since config fallback is always available
	logctx.Or(ctx, g.logger).Debug("Macro data gateway health check passed")
	return nil
}

// getTreasuryRatesFromFRED fetches treasury rates from FRED API
func (g *Gateway) getTreasuryRatesFromFRED(ctx context.Context) (*entities.TreasuryRates, error) {
	// FRED series IDs for Treasury yields
	seriesMap := map[string]string{
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

	treasuryRates := &entities.TreasuryRates{
		AsOf: time.Now().UTC(),
	}

	// Fetch each series from FRED
	for seriesID, fieldName := range seriesMap {
		value, err := g.getFREDSeries(ctx, seriesID)
		if err != nil {
			logctx.Or(ctx, g.logger).Warn("Failed to fetch FRED series",
				zap.String("series_id", seriesID),
				zap.Error(err))
			continue
		}

		// Convert percentage to decimal (FRED returns percentages)
		rate := value / 100.0

		// Set the appropriate field using reflection-like approach
		switch fieldName {
		case "yield_1_month":
			treasuryRates.Yield1Month = rate
		case "yield_3_month":
			treasuryRates.Yield3Month = rate
		case "yield_6_month":
			treasuryRates.Yield6Month = rate
		case "yield_1_year":
			treasuryRates.Yield1Year = rate
		case "yield_2_year":
			treasuryRates.Yield2Year = rate
		case "yield_5_year":
			treasuryRates.Yield5Year = rate
		case "yield_10_year":
			treasuryRates.Yield10Year = rate
		case "yield_20_year":
			treasuryRates.Yield20Year = rate
		case "yield_30_year":
			treasuryRates.Yield30Year = rate
		}
	}

	// Validate that we got at least some data
	if treasuryRates.Yield10Year == 0 && treasuryRates.Yield5Year == 0 {
		return nil, fmt.Errorf("no valid treasury rates fetched from FRED")
	}

	return treasuryRates, nil
}

// getFREDSeries fetches a single series from FRED API.
//
// When an artifact bundle is on ctx, the raw FRED response body is captured
// under fetch.macro/07-fetch-macro-<seriesID>.raw.json after JSON-key
// redaction. The FRED `api_key` URL query parameter is the secret to scrub —
// it travels in the URL, not the body, so RedactJSONBytes alone won't see
// it; the secret is also recorded against the manifest's redactions list
// via a synthetic redaction path "query.api_key".
func (g *Gateway) getFREDSeries(ctx context.Context, seriesID string) (float64, error) {
	url := fmt.Sprintf("%s/series/observations?series_id=%s&api_key=%s&file_type=json&limit=1&sort_order=desc",
		g.config.FREDBaseURL, seriesID, g.config.FREDAPIKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create FRED request: %w", err)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to execute FRED request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("FRED API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Tier-3 artifact bundle: TeeReader the response body so reads dual-stream
	// into both the JSON decoder AND a per-request raw-bytes buffer.
	body := resp.Body
	var rawBuf *bytes.Buffer
	if b := artifact.From(ctx); b != nil {
		rawBuf = &bytes.Buffer{}
		body = io.NopCloser(io.TeeReader(resp.Body, rawBuf))
	}

	var fredResponse FREDResponse
	if err := json.NewDecoder(body).Decode(&fredResponse); err != nil {
		return 0, fmt.Errorf("failed to decode FRED response: %w", err)
	}

	// Push the captured raw bytes into the bundle. Body redaction strips any
	// secret-looking JSON keys; we ALSO record "query.api_key" in the manifest
	// redaction set since the FRED key travels in the URL (not visible to
	// RedactJSONBytes which only sees the response body).
	if rawBuf != nil {
		raw, redacted := artifact.RedactJSONBytes(rawBuf.Bytes())
		if b := artifact.From(ctx); b != nil {
			redacted = append(redacted, "query.api_key")
			fname := fmt.Sprintf("07-fetch-macro-%s.raw.json", seriesID)
			b.SnapshotRaw(ctx, "fetch.macro", fname, raw, redacted)
		}
	}

	if len(fredResponse.Observations) == 0 {
		return 0, fmt.Errorf("no observations found for series %s", seriesID)
	}

	observation := fredResponse.Observations[0]
	if observation.Value == "." {
		return 0, fmt.Errorf("no valid data for series %s", seriesID)
	}

	value, err := strconv.ParseFloat(observation.Value, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse value for series %s: %w", seriesID, err)
	}

	return value, nil
}

// getTreasuryRatesFromConfig returns treasury rates using config defaults.
// ctx is threaded through from the calling public method so logs inherit request correlation.
func (g *Gateway) getTreasuryRatesFromConfig(ctx context.Context) *entities.TreasuryRates {
	logctx.Or(ctx, g.logger).Info("Using config-based treasury rates fallback",
		zap.Float64("manual_risk_free_rate", g.config.ManualRiskFreeRate))

	// Use the manual risk-free rate for 10-year treasury and interpolate others
	baseRate := g.config.ManualRiskFreeRate

	return &entities.TreasuryRates{
		AsOf:        time.Now().UTC(),
		Yield1Month: baseRate * 0.5, // Typically lower than 10-year
		Yield3Month: baseRate * 0.6,
		Yield6Month: baseRate * 0.7,
		Yield1Year:  baseRate * 0.8,
		Yield2Year:  math.Round(baseRate*0.90*10000) / 10000,
		Yield5Year:  math.Round(baseRate*0.95*10000) / 10000,
		Yield10Year: baseRate,                                // Base rate represents 10-year
		Yield20Year: math.Round(baseRate*1.05*10000) / 10000, // Typically slightly higher
		Yield30Year: math.Round(baseRate*1.10*10000) / 10000,
	}
}

// GetFXRate returns the spot exchange rate fromCcy→toCcy: how many units of
// toCcy you get for one unit of fromCcy. Lookup order:
//
//  1. Identity (from == to) → 1.0 with no I/O.
//  2. In-process cache (6-hour TTL) → cheap repeat reads.
//  3. FRED daily-FX series — preferred when the API key is configured. The
//     fredSeriesFor table maps direct USD pairs to a FRED series ID and an
//     "invert" flag that reflects the direction the series publishes (some
//     are USD-per-X, others X-per-USD).
//  4. Cross-via-USD recursion — for any non-USD pair, GetFXRate(from, USD)
//     and GetFXRate(to, USD) are computed and divided. This re-uses the
//     cache and keeps the FRED series table small (only X→USD pairs).
//  5. Static config fallback — config/fx_rates.json snapshot, consulted
//     whenever FRED is unavailable. Logged at INFO with source:
//     "static_config" so operators can see when fallback is active.
//
// Returns ports.ErrFXRateUnavailable when no source can satisfy the request.
// Phase B9 of the IFRS-FPI spec is the only consumer (TODO: wire from
// valuation.convertFinancialsToUSD when that lands).
func (g *Gateway) GetFXRate(ctx context.Context, fromCcy, toCcy string) (float64, error) {
	from := strings.ToUpper(strings.TrimSpace(fromCcy))
	to := strings.ToUpper(strings.TrimSpace(toCcy))

	// Identity short-circuit. No log, no cache — keeps health-check noise
	// down when callers normalize "USD reporting" tickers through this path.
	if from == to {
		return 1.0, nil
	}

	cacheKey := from + ":" + to

	// Cache check. We tolerate stale entries up to fxCacheTTL — for spot FX
	// on G10 currencies the typical bid/ask move is well under 1% in 6h,
	// which is acceptable for valuation work.
	if v, ok := g.fxCache.Load(cacheKey); ok {
		if entry, ok := v.(fxCacheEntry); ok && time.Since(entry.storedAt) < fxCacheTTL {
			return entry.rate, nil
		}
	}

	// Cross-via-USD path: when neither side is USD we route through the
	// per-USD pairs (which is what FRED publishes). This recursion is bounded
	// to two levels because each recursive call has at least one USD leg.
	if from != "USD" && to != "USD" {
		fromToUSD, err := g.GetFXRate(ctx, from, "USD")
		if err != nil {
			return 0, fmt.Errorf("cross-rate %s->%s: %w", from, to, err)
		}
		toToUSD, err := g.GetFXRate(ctx, to, "USD")
		if err != nil {
			return 0, fmt.Errorf("cross-rate %s->%s: %w", from, to, err)
		}
		if toToUSD == 0 {
			return 0, fmt.Errorf("cross-rate %s->%s: divisor is zero: %w", from, to, ports.ErrFXRateUnavailable)
		}
		rate := fromToUSD / toToUSD
		g.fxCache.Store(cacheKey, fxCacheEntry{rate: rate, storedAt: time.Now()})
		return rate, nil
	}

	// Direct USD pair. Try FRED first, then fall back to static config.
	rate, err := g.getFXRateFromFRED(ctx, from, to)
	if err == nil {
		g.fxCache.Store(cacheKey, fxCacheEntry{rate: rate, storedAt: time.Now()})
		return rate, nil
	}

	// FRED failed — log the fallback transition at INFO so operators see it.
	// The reason field captures the underlying FRED error so on-call can
	// distinguish "FRED is down" from "we never had an API key".
	rate, fallbackErr := g.getFXRateFromStaticConfig(from, to)
	if fallbackErr != nil {
		// Both sources unavailable. Multi-%w (Go 1.20+) so callers can
		// errors.Is against either the FRED error chain or
		// ports.ErrFXRateUnavailable (which fallbackErr carries).
		return 0, fmt.Errorf("fred error: %w; static fallback: %w", err, fallbackErr)
	}

	logctx.Or(ctx, g.logger).Info("gateway.macro.fx.fallback",
		zap.String("from", from),
		zap.String("to", to),
		zap.Float64("rate", rate),
		zap.String("source", "static_config"),
		zap.String("reason", err.Error()),
	)

	g.fxCache.Store(cacheKey, fxCacheEntry{rate: rate, storedAt: time.Now()})
	return rate, nil
}

// getFXRateFromFRED fetches a direct USD-pair FX rate from FRED. Returns an
// error if FRED is not configured (no API key) or if the underlying HTTP
// request / parse fails. Callers fall back to the static config on any error.
//
// Precondition: exactly one of {fromCcy, toCcy} is "USD". Cross-USD pairs
// are handled by GetFXRate before we get here.
func (g *Gateway) getFXRateFromFRED(ctx context.Context, fromCcy, toCcy string) (float64, error) {
	// Skip FRED entirely when not configured. This is not "fred returned an
	// error" — it is "fred is not even an option" — so we synthesize an error
	// so the caller can transition to the static fallback uniformly.
	if !g.config.FREDEnabled || g.config.FREDAPIKey == "" {
		return 0, errors.New("FRED API not configured")
	}

	seriesID, invert, ok := fredSeriesFor(fromCcy, toCcy)
	if !ok {
		// No mapping for this pair — surface as a sentinel-wrapped error so
		// upstream callers can decide whether the static fallback covers it.
		return 0, fmt.Errorf("no FRED series for %s->%s: %w", fromCcy, toCcy, ports.ErrFXRateUnavailable)
	}

	rawValue, err := g.getFREDSeries(ctx, seriesID)
	if err != nil {
		return 0, fmt.Errorf("FRED series %s: %w", seriesID, err)
	}

	if rawValue == 0 {
		return 0, fmt.Errorf("FRED series %s returned zero", seriesID)
	}

	if invert {
		// FRED publishes fromCcy-per-USD; we want USD-per-fromCcy (or the
		// equivalent toCcy direction). The series direction is fixed by FRED
		// metadata; the invert flag in fredSeriesFor encodes which way to go.
		return 1.0 / rawValue, nil
	}

	// Series already publishes the direction we want.
	return rawValue, nil
}

// getFXRateFromStaticConfig resolves an FX rate from the manually-curated
// FRED H.10 snapshot. RatesToUSD entries express USD per 1 unit of the
// foreign currency, so:
//   - X→USD: rate = RatesToUSD[X]
//   - USD→X: rate = 1 / RatesToUSD[X]
//
// Cross-USD pairs are not handled here — GetFXRate's cross-via-USD path
// recurses into this function twice and divides.
//
// Returns ports.ErrFXRateUnavailable when the requested currency is missing
// from the snapshot.
func (g *Gateway) getFXRateFromStaticConfig(fromCcy, toCcy string) (float64, error) {
	if len(g.fxRatesToUSD) == 0 {
		return 0, fmt.Errorf("static FX config empty: %w", ports.ErrFXRateUnavailable)
	}

	switch {
	case toCcy == "USD":
		rate, ok := g.fxRatesToUSD[fromCcy]
		if !ok {
			return 0, fmt.Errorf("no static FX rate for %s: %w", fromCcy, ports.ErrFXRateUnavailable)
		}
		return rate, nil
	case fromCcy == "USD":
		rate, ok := g.fxRatesToUSD[toCcy]
		if !ok || rate == 0 {
			return 0, fmt.Errorf("no static FX rate for %s: %w", toCcy, ports.ErrFXRateUnavailable)
		}
		return 1.0 / rate, nil
	default:
		// Defensive: GetFXRate routes cross-USD pairs through recursion, so
		// this branch should be unreachable. Treat as unavailable rather
		// than panic so the caller can degrade gracefully.
		return 0, fmt.Errorf("static FX config does not handle cross pairs %s->%s: %w", fromCcy, toCcy, ports.ErrFXRateUnavailable)
	}
}

// fredSeriesFor maps a direct USD currency pair to its FRED daily-FX series
// ID and whether the series value must be inverted before use. FRED publishes
// some pairs as "foreign per USD" (DEXTAUS = TWD per USD → invert=true) and
// others as "USD per foreign" (DEXUSEU = USD per EUR → invert=false). The
// table is the single source of truth for that direction; gateway logic stays
// agnostic.
//
// Initial coverage matches the 13 currencies in config/fx_rates.json so FRED
// and static fallback have the same reach. Add new pairs by extending both
// this table AND the config snapshot together.
func fredSeriesFor(fromCcy, toCcy string) (seriesID string, invert bool, ok bool) {
	type entry struct {
		series string
		invert bool
	}

	// X → USD direction. USD → X is handled by inverting the result; we keep
	// the table compact to one direction per currency.
	xToUSD := map[string]entry{
		"TWD": {"DEXTAUS", true},  // FRED: TWD per USD → invert
		"EUR": {"DEXUSEU", false}, // FRED: USD per EUR → no invert
		"JPY": {"DEXJPUS", true},  // FRED: JPY per USD → invert
		"GBP": {"DEXUSUK", false}, // FRED: USD per GBP → no invert
		"HKD": {"DEXHKUS", true},  // FRED: HKD per USD → invert
		"CNY": {"DEXCHUS", true},  // FRED: CNY per USD → invert
		"KRW": {"DEXKOUS", true},  // FRED: KRW per USD → invert
		"CHF": {"DEXSZUS", true},  // FRED: CHF per USD → invert
		"CAD": {"DEXCAUS", true},  // FRED: CAD per USD → invert
		"AUD": {"DEXUSAL", false}, // FRED: USD per AUD → no invert
		"INR": {"DEXINUS", true},  // FRED: INR per USD → invert
		"BRL": {"DEXBZUS", true},  // FRED: BRL per USD → invert
		"DKK": {"DEXDNUS", true},  // FRED: DKK per USD → invert
	}

	switch {
	case toCcy == "USD":
		if e, found := xToUSD[fromCcy]; found {
			return e.series, e.invert, true
		}
	case fromCcy == "USD":
		// USD → X: look up the X → USD entry and flip the invert flag.
		// If X→USD doesn't need inverting (USD-per-X), then USD→X DOES need
		// inverting, and vice versa.
		if e, found := xToUSD[toCcy]; found {
			return e.series, !e.invert, true
		}
	}
	return "", false, false
}

// FREDResponse represents the response structure from FRED API
type FREDResponse struct {
	RealtimeStart string            `json:"realtime_start"`
	RealtimeEnd   string            `json:"realtime_end"`
	Observations  []FREDObservation `json:"observations"`
}

// FREDObservation represents a single observation from FRED
type FREDObservation struct {
	RealtimeStart string `json:"realtime_start"`
	RealtimeEnd   string `json:"realtime_end"`
	Date          string `json:"date"`
	Value         string `json:"value"`
}
