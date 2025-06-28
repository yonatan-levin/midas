package macro

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
)

func createTestMacroConfig(fredEnabled bool, fredAPIKey string, fredBaseURL string) *config.MacroConfig {
	return &config.MacroConfig{
		FREDEnabled:             fredEnabled,
		FREDAPIKey:              fredAPIKey,
		FREDBaseURL:             fredBaseURL,
		ManualRiskFreeRate:      0.045, // 4.5%
		ManualMarketRiskPremium: 0.05,  // 5%
	}
}

func TestNewGateway(t *testing.T) {
	t.Run("creates gateway successfully", func(t *testing.T) {
		cfg := createTestMacroConfig(true, "test_key", "https://api.stlouisfed.org/fred")
		logger := zap.NewNop()

		gateway := NewGateway(cfg, logger)

		assert.NotNil(t, gateway)
		assert.IsType(t, &Gateway{}, gateway)
	})
}

func TestGateway_GetTreasuryRates_ConfigFallback(t *testing.T) {
	t.Run("uses config fallback when FRED disabled", func(t *testing.T) {
		cfg := createTestMacroConfig(false, "", "")
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		rates, err := gateway.GetTreasuryRates(ctx)

		require.NoError(t, err)
		assert.NotNil(t, rates)
		assert.Equal(t, cfg.ManualRiskFreeRate, rates.Yield10Year)
		assert.Equal(t, cfg.ManualRiskFreeRate*0.6, rates.Yield3Month) // Interpolated
		assert.WithinDuration(t, time.Now().UTC(), rates.AsOf, 1*time.Minute)
	})

	t.Run("uses config fallback when FRED key missing", func(t *testing.T) {
		cfg := createTestMacroConfig(true, "", "https://api.stlouisfed.org/fred")
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		rates, err := gateway.GetTreasuryRates(ctx)

		require.NoError(t, err)
		assert.NotNil(t, rates)
		assert.Equal(t, cfg.ManualRiskFreeRate, rates.Yield10Year)
	})
}

func TestGateway_GetTreasuryRates_FREDSuccess(t *testing.T) {
	// Create mock FRED server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate request
		assert.Contains(t, r.URL.Query().Get("series_id"), "DGS") // Should request treasury series
		assert.Equal(t, "test_api_key", r.URL.Query().Get("api_key"))
		assert.Equal(t, "json", r.URL.Query().Get("file_type"))

		// Mock FRED response
		response := FREDResponse{
			RealtimeStart: "2024-01-15",
			RealtimeEnd:   "2024-01-15",
			Observations: []FREDObservation{
				{
					RealtimeStart: "2024-01-15",
					RealtimeEnd:   "2024-01-15",
					Date:          "2024-01-15",
					Value:         "4.50", // 4.5% in FRED format
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	t.Run("fetches from FRED successfully", func(t *testing.T) {
		cfg := createTestMacroConfig(true, "test_api_key", server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		rates, err := gateway.GetTreasuryRates(ctx)

		require.NoError(t, err)
		assert.NotNil(t, rates)
		// Should have converted FRED percentage to decimal
		assert.Greater(t, rates.Yield10Year, 0.0)
		assert.WithinDuration(t, time.Now().UTC(), rates.AsOf, 1*time.Minute)
	})
}

func TestGateway_GetTreasuryRates_FREDFailure(t *testing.T) {
	// Create failing FRED server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	t.Run("falls back to config when FRED fails", func(t *testing.T) {
		cfg := createTestMacroConfig(true, "test_api_key", server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		rates, err := gateway.GetTreasuryRates(ctx)

		require.NoError(t, err)
		assert.NotNil(t, rates)
		// Should use config fallback values
		assert.Equal(t, cfg.ManualRiskFreeRate, rates.Yield10Year)
		assert.Equal(t, cfg.ManualRiskFreeRate*0.6, rates.Yield3Month)
	})
}

func TestGateway_GetTreasuryRates_FREDInvalidData(t *testing.T) {
	// Create FRED server with invalid data
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := FREDResponse{
			RealtimeStart: "2024-01-15",
			RealtimeEnd:   "2024-01-15",
			Observations: []FREDObservation{
				{
					RealtimeStart: "2024-01-15",
					RealtimeEnd:   "2024-01-15",
					Date:          "2024-01-15",
					Value:         ".", // FRED uses "." for missing data
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	t.Run("falls back to config when FRED returns invalid data", func(t *testing.T) {
		cfg := createTestMacroConfig(true, "test_api_key", server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		rates, err := gateway.GetTreasuryRates(ctx)

		require.NoError(t, err)
		assert.NotNil(t, rates)
		// Should use config fallback values
		assert.Equal(t, cfg.ManualRiskFreeRate, rates.Yield10Year)
	})
}

func TestGateway_GetMarketRiskPremium(t *testing.T) {
	t.Run("returns config-based market risk premium", func(t *testing.T) {
		cfg := createTestMacroConfig(false, "", "")
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		mrp, err := gateway.GetMarketRiskPremium(ctx)

		require.NoError(t, err)
		assert.Equal(t, cfg.ManualMarketRiskPremium, mrp)
	})

	t.Run("returns config fallback even with FRED enabled", func(t *testing.T) {
		cfg := createTestMacroConfig(true, "test_key", "https://api.stlouisfed.org/fred")
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		mrp, err := gateway.GetMarketRiskPremium(ctx)

		require.NoError(t, err)
		assert.Equal(t, cfg.ManualMarketRiskPremium, mrp)
	})
}

func TestGateway_HealthCheck(t *testing.T) {
	t.Run("passes health check when FRED disabled", func(t *testing.T) {
		cfg := createTestMacroConfig(false, "", "")
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		err := gateway.HealthCheck(ctx)

		assert.NoError(t, err)
	})

	t.Run("passes health check even when FRED fails", func(t *testing.T) {
		// Create failing FRED server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		cfg := createTestMacroConfig(true, "test_key", server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()
		err := gateway.HealthCheck(ctx)

		// Should pass because config fallback is available
		assert.NoError(t, err)
	})
}

func TestGateway_getTreasuryRatesFromConfig(t *testing.T) {
	t.Run("generates interpolated rates from config", func(t *testing.T) {
		cfg := createTestMacroConfig(false, "", "")
		cfg.ManualRiskFreeRate = 0.04 // 4%
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger).(*Gateway)

		rates := gateway.getTreasuryRatesFromConfig()

		require.NotNil(t, rates)
		assert.Equal(t, 0.04, rates.Yield10Year)                  // Base rate
		assert.Equal(t, 0.04*0.5, rates.Yield1Month)              // 50% of base
		assert.Equal(t, 0.04*0.6, rates.Yield3Month)              // 60% of base
		assert.InDelta(t, 0.04*1.05, rates.Yield20Year, 0.000001) // 105% of base
		assert.Equal(t, 0.04*1.1, rates.Yield30Year)              // 110% of base
		assert.WithinDuration(t, time.Now().UTC(), rates.AsOf, 1*time.Minute)
	})

	t.Run("handles zero risk-free rate", func(t *testing.T) {
		cfg := createTestMacroConfig(false, "", "")
		cfg.ManualRiskFreeRate = 0.0 // 0%
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger).(*Gateway)

		rates := gateway.getTreasuryRatesFromConfig()

		require.NotNil(t, rates)
		assert.Equal(t, 0.0, rates.Yield10Year)
		assert.Equal(t, 0.0, rates.Yield1Month)
		assert.Equal(t, 0.0, rates.Yield3Month)
		assert.Equal(t, 0.0, rates.Yield20Year)
		assert.Equal(t, 0.0, rates.Yield30Year)
	})

	t.Run("handles high risk-free rate", func(t *testing.T) {
		cfg := createTestMacroConfig(false, "", "")
		cfg.ManualRiskFreeRate = 0.15 // 15%
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger).(*Gateway)

		rates := gateway.getTreasuryRatesFromConfig()

		require.NotNil(t, rates)
		assert.Equal(t, 0.15, rates.Yield10Year)
		assert.Equal(t, 0.15*0.5, rates.Yield1Month)
		assert.Equal(t, 0.15*1.1, rates.Yield30Year)
	})
}

func TestGateway_getFREDSeries(t *testing.T) {
	// Create mock FRED server with different response scenarios
	testCases := []struct {
		name          string
		fredResponse  interface{}
		statusCode    int
		expectedError bool
		expectedValue float64
	}{
		{
			name: "successful response",
			fredResponse: FREDResponse{
				Observations: []FREDObservation{
					{Value: "4.50"},
				},
			},
			statusCode:    http.StatusOK,
			expectedError: false,
			expectedValue: 4.50,
		},
		{
			name: "missing data (dot value)",
			fredResponse: FREDResponse{
				Observations: []FREDObservation{
					{Value: "."},
				},
			},
			statusCode:    http.StatusOK,
			expectedError: true,
		},
		{
			name: "no observations",
			fredResponse: FREDResponse{
				Observations: []FREDObservation{},
			},
			statusCode:    http.StatusOK,
			expectedError: true,
		},
		{
			name:          "server error",
			fredResponse:  nil,
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				if tc.fredResponse != nil {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(tc.fredResponse)
				}
			}))
			defer server.Close()

			cfg := createTestMacroConfig(true, "test_key", server.URL)
			logger := zap.NewNop()
			gateway := NewGateway(cfg, logger).(*Gateway)

			ctx := context.Background()
			value, err := gateway.getFREDSeries(ctx, "DGS10")

			if tc.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedValue, value)
			}
		})
	}
}

func TestGateway_ContextCancellation(t *testing.T) {
	t.Run("respects context cancellation", func(t *testing.T) {
		// Create slow FRED server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			response := FREDResponse{
				Observations: []FREDObservation{{Value: "4.50"}},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := createTestMacroConfig(true, "test_key", server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// Should still succeed due to fallback, but test that context is respected
		rates, err := gateway.GetTreasuryRates(ctx)
		assert.NoError(t, err) // Falls back to config
		assert.NotNil(t, rates)
	})

	t.Run("respects context timeout", func(t *testing.T) {
		// Create slow FRED server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			response := FREDResponse{
				Observations: []FREDObservation{{Value: "4.50"}},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := createTestMacroConfig(true, "test_key", server.URL)
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		// Should fall back to config when FRED times out
		rates, err := gateway.GetTreasuryRates(ctx)
		assert.NoError(t, err) // Falls back to config
		assert.NotNil(t, rates)
		assert.Equal(t, cfg.ManualRiskFreeRate, rates.Yield10Year)
	})
}

func TestGateway_EdgeCases(t *testing.T) {
	t.Run("handles empty config values gracefully", func(t *testing.T) {
		cfg := &config.MacroConfig{
			FREDEnabled:             false,
			ManualRiskFreeRate:      0.0, // Zero values
			ManualMarketRiskPremium: 0.0,
		}
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()

		rates, err := gateway.GetTreasuryRates(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, rates)
		assert.Equal(t, 0.0, rates.Yield10Year)

		mrp, err := gateway.GetMarketRiskPremium(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0.0, mrp)
	})

	t.Run("handles negative config values", func(t *testing.T) {
		cfg := &config.MacroConfig{
			FREDEnabled:             false,
			ManualRiskFreeRate:      -0.01, // Negative rates (possible in some economies)
			ManualMarketRiskPremium: -0.02,
		}
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()

		rates, err := gateway.GetTreasuryRates(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, rates)
		assert.Equal(t, -0.01, rates.Yield10Year)

		mrp, err := gateway.GetMarketRiskPremium(ctx)
		assert.NoError(t, err)
		assert.Equal(t, -0.02, mrp)
	})

	t.Run("handles very high config values", func(t *testing.T) {
		cfg := &config.MacroConfig{
			FREDEnabled:             false,
			ManualRiskFreeRate:      1.0, // 100% rate
			ManualMarketRiskPremium: 0.5, // 50% premium
		}
		logger := zap.NewNop()
		gateway := NewGateway(cfg, logger)

		ctx := context.Background()

		rates, err := gateway.GetTreasuryRates(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, rates)
		assert.Equal(t, 1.0, rates.Yield10Year)

		mrp, err := gateway.GetMarketRiskPremium(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0.5, mrp)
	})
}
