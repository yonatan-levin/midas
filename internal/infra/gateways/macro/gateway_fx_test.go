package macro

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// fredHandler builds an httptest handler that responds to FRED observation
// queries by reading the series_id query param and looking up its raw value
// in `seriesValues`. Unknown series IDs respond 404. Returning the raw
// FRED-style number (as a string) keeps tests faithful to the real wire
// format and exercises the same parsing path as production.
func fredHandler(t *testing.T, seriesValues map[string]string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seriesID := r.URL.Query().Get("series_id")
		val, ok := seriesValues[seriesID]
		if !ok {
			http.NotFound(w, r)
			return
		}
		resp := FREDResponse{
			RealtimeStart: "2026-04-27",
			RealtimeEnd:   "2026-04-27",
			Observations: []FREDObservation{
				{Date: "2026-04-27", Value: val},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// TestMacroGateway_GetFXRate_SameCcy_ShortCircuits pins the identity case:
// USD→USD must return exactly 1.0 with no FRED call and no log line. This
// matters in production because Phase B9 will call GetFXRate for every
// reporting-currency-to-USD conversion, including the no-op USD case.
func TestMacroGateway_GetFXRate_SameCcy_ShortCircuits(t *testing.T) {
	// Set up an httptest server that fails the test if hit — the identity
	// path must not consult FRED at all.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("FRED must not be called for identity USD->USD lookup; got %s", r.URL.String())
		http.NotFound(w, r)
	}))
	defer server.Close()

	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	cfg := createTestMacroConfig(true, "test_key", server.URL)
	gateway := NewGatewayWithFXRates(cfg, map[string]float64{"USD": 1.0, "TWD": 0.0312}, logger)

	rate, err := gateway.GetFXRate(context.Background(), "USD", "USD")

	require.NoError(t, err)
	assert.Equal(t, 1.0, rate, "identity rate must be exactly 1.0")
	// No log line should be emitted on the identity path (we want quiet
	// short-circuit). Tolerate logger.Named() side-effects but nothing else.
	for _, e := range logs.All() {
		// "macro-gateway" is set by .Named() at construction; treat any
		// log entry beyond that as a regression in the identity path.
		t.Errorf("identity path emitted log entry: msg=%q level=%s", e.Message, e.Level)
	}
}

// TestMacroGateway_GetFXRate_FREDSuccess_TWD_USD pins the inverted-series
// path: DEXTAUS publishes TWD per USD, so the gateway must invert (1/V) to
// produce USD per TWD. With FRED returning 31.4, we expect ≈ 0.03185.
func TestMacroGateway_GetFXRate_FREDSuccess_TWD_USD(t *testing.T) {
	server := httptest.NewServer(fredHandler(t, map[string]string{
		"DEXTAUS": "31.4",
	}))
	defer server.Close()

	cfg := createTestMacroConfig(true, "test_key", server.URL)
	logger := zap.NewNop()
	gateway := NewGatewayWithFXRates(cfg, nil, logger)

	rate, err := gateway.GetFXRate(context.Background(), "TWD", "USD")

	require.NoError(t, err)
	assert.InDelta(t, 1.0/31.4, rate, 1e-6, "TWD->USD must invert FRED's TWD-per-USD value")
}

// TestMacroGateway_GetFXRate_FREDSuccess_EUR_USD_NoInvert pins the
// pass-through path: DEXUSEU already publishes USD per EUR, so the gateway
// must NOT invert. With FRED returning 1.085, we expect 1.085 exactly.
func TestMacroGateway_GetFXRate_FREDSuccess_EUR_USD_NoInvert(t *testing.T) {
	server := httptest.NewServer(fredHandler(t, map[string]string{
		"DEXUSEU": "1.085",
	}))
	defer server.Close()

	cfg := createTestMacroConfig(true, "test_key", server.URL)
	logger := zap.NewNop()
	gateway := NewGatewayWithFXRates(cfg, nil, logger)

	rate, err := gateway.GetFXRate(context.Background(), "EUR", "USD")

	require.NoError(t, err)
	assert.InDelta(t, 1.085, rate, 1e-9, "EUR->USD pass-through must equal FRED's USD-per-EUR value")
}

// TestMacroGateway_GetFXRate_FallsBackToConfig pins the FRED-down path.
// With FRED returning 500 errors and the static config carrying
// TWD=0.0312 USD-per-TWD, GetFXRate must return 0.0312 successfully and
// emit an INFO log line tagged source=static_config so operators can see
// that fallback was active.
func TestMacroGateway_GetFXRate_FallsBackToConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := createTestMacroConfig(true, "test_key", server.URL)

	// Use observer so we can assert on the fallback log line.
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	gateway := NewGatewayWithFXRates(cfg, map[string]float64{"TWD": 0.0312}, logger)

	// Inject the observer logger via context so the request-path log goes
	// through logctx.Or(ctx, ...) and we capture it.
	ctx := logctx.Inject(context.Background(), logger)
	rate, err := gateway.GetFXRate(ctx, "TWD", "USD")

	require.NoError(t, err)
	assert.InDelta(t, 0.0312, rate, 1e-9)

	// Verify the fallback log line carried the expected fields.
	fallbackEntries := logs.FilterMessageSnippet("fx.fallback").All()
	require.NotEmpty(t, fallbackEntries, "must emit gateway.macro.fx.fallback log on static fallback")
	entry := fallbackEntries[0]
	fields := entry.ContextMap()
	assert.Equal(t, "static_config", fields["source"], "log must tag source=static_config")
	assert.Equal(t, "TWD", fields["from"])
	assert.Equal(t, "USD", fields["to"])
	assert.NotEmpty(t, fields["reason"], "log must include FRED failure reason")
}

// TestMacroGateway_GetFXRate_CrossViaUSD_EUR_JPY pins the cross-currency
// path. The gateway must compose two USD-leg lookups and divide. We mock
// FRED so that EUR->USD = 1.085 (DEXUSEU pass-through with raw 1.085) and
// JPY->USD = 0.00665 (DEXJPUS inverted from raw 1/0.00665 ≈ 150.376).
// Expected EUR->JPY = 1.085 / 0.00665 ≈ 163.16 JPY per EUR.
func TestMacroGateway_GetFXRate_CrossViaUSD_EUR_JPY(t *testing.T) {
	// DEXJPUS raw value chosen so 1/raw == 0.00665 (gateway inverts).
	jpyPerUSD := 1.0 / 0.00665 // ≈ 150.376

	server := httptest.NewServer(fredHandler(t, map[string]string{
		"DEXUSEU": "1.085",
		"DEXJPUS": floatStr(jpyPerUSD),
	}))
	defer server.Close()

	cfg := createTestMacroConfig(true, "test_key", server.URL)
	logger := zap.NewNop()
	gateway := NewGatewayWithFXRates(cfg, nil, logger)

	rate, err := gateway.GetFXRate(context.Background(), "EUR", "JPY")

	require.NoError(t, err)
	expected := 1.085 / 0.00665 // ≈ 163.16
	assert.InDelta(t, expected, rate, 0.01, "EUR->JPY must cross via USD: EUR/USD divided by JPY/USD")
}

// TestMacroGateway_GetFXRate_UnknownCurrency_ReturnsSentinel pins the
// failure path: when FRED has no series for the requested pair AND the
// static config also lacks the currency, callers must see
// ports.ErrFXRateUnavailable wrapped in the returned error so Phase B9 can
// classify it as a fatal-but-recoverable conversion failure.
func TestMacroGateway_GetFXRate_UnknownCurrency_ReturnsSentinel(t *testing.T) {
	// FRED handler that 404s on every series — simulates "no such series"
	// for the unknown XYZ currency.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := createTestMacroConfig(true, "test_key", server.URL)
	logger := zap.NewNop()
	// Static config covers TWD only; XYZ is intentionally absent.
	gateway := NewGatewayWithFXRates(cfg, map[string]float64{"TWD": 0.0312}, logger)

	rate, err := gateway.GetFXRate(context.Background(), "XYZ", "USD")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrFXRateUnavailable),
		"error must wrap ports.ErrFXRateUnavailable, got: %v", err)
	assert.Equal(t, 0.0, rate, "rate must be zero on failure")
}

// TestMacroGateway_GetFXRate_CacheHit verifies that successive calls for the
// same pair don't re-hit FRED. This isn't in the required test list but
// pins behavior we rely on for performance and FRED rate-limit politeness.
func TestMacroGateway_GetFXRate_CacheHit(t *testing.T) {
	var fredCallCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fredCallCount++
		resp := FREDResponse{
			Observations: []FREDObservation{{Date: "2026-04-27", Value: "31.4"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := createTestMacroConfig(true, "test_key", server.URL)
	gateway := NewGatewayWithFXRates(cfg, nil, zap.NewNop())

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := gateway.GetFXRate(ctx, "TWD", "USD")
		require.NoError(t, err)
	}
	assert.Equal(t, 1, fredCallCount, "FRED should be called exactly once; subsequent reads must hit cache")
}

// floatStr formats a float64 in FRED's decimal-string wire format. Uses
// strconv.FormatFloat with 'f' verb and -1 precision so we get the shortest
// representation that round-trips back to v exactly — perfect for tests
// where the gateway re-parses the value via strconv.ParseFloat.
func floatStr(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
