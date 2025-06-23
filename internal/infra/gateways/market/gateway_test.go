package market

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
)

func TestNewGateway(t *testing.T) {
	cfg := &config.MarketConfig{
		YFinance: config.YFinanceConfig{
			Enabled:        true,
			BaseURL:        "https://query1.finance.yahoo.com",
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
		},
		Finzive: config.FinziveConfig{
			Enabled:        false,
			BaseURL:        "https://finzive.com",
			RequestTimeout: 60 * time.Second,
			MaxRetries:     2,
			UserAgent:      "Test Agent",
		},
	}
	logger := zap.NewNop()

	gateway := NewGateway(cfg, logger)

	assert.NotNil(t, gateway)
	assert.NotNil(t, gateway.yfinance)
	assert.Equal(t, cfg, gateway.config)
	assert.NotNil(t, gateway.logger)
}

func TestNewGateway_YFinanceDisabled(t *testing.T) {
	cfg := &config.MarketConfig{
		YFinance: config.YFinanceConfig{
			Enabled:        false,
			BaseURL:        "https://query1.finance.yahoo.com",
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
		},
	}
	logger := zap.NewNop()

	gateway := NewGateway(cfg, logger)

	assert.NotNil(t, gateway)
	assert.Nil(t, gateway.yfinance) // Should be nil when disabled
	assert.Equal(t, cfg, gateway.config)
	assert.NotNil(t, gateway.logger)
}

// Note: Additional unit tests for Gateway methods would require either:
// 1. Interface abstraction of YFinanceClient for proper mocking
// 2. Integration tests with actual API calls (marked with build tags)
// 3. Test fixtures with mock HTTP servers
//
// The current implementation uses concrete types which makes unit testing
// challenging without refactoring the architecture to use dependency injection
// with interfaces.
