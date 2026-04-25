package macro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// Gateway implements the MacroData gateway interface
type Gateway struct {
	config     *config.MacroConfig
	httpClient *http.Client
	logger     *zap.Logger
}

// NewGateway creates a new MacroData gateway
func NewGateway(cfg *config.MacroConfig, logger *zap.Logger) ports.MacroDataGateway {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: false,
		},
	}

	return &Gateway{
		config:     cfg,
		httpClient: httpClient,
		logger:     logger.Named("macro-gateway"),
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
