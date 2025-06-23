package sec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
)

func TestNewGateway(t *testing.T) {
	cfg := &config.SECConfig{
		BaseURL:          "https://data.sec.gov/api/xbrl",
		UserAgent:        "Test User Agent",
		RateLimit:        10,
		RequestTimeout:   30 * time.Second,
		MaxRetries:       3,
		RetryBackoffBase: time.Second,
	}
	logger := zap.NewNop()

	gateway := NewGateway(cfg, logger)

	assert.NotNil(t, gateway)
	assert.NotNil(t, gateway.client)
	assert.NotNil(t, gateway.parser)
	assert.NotNil(t, gateway.logger)
}

// Note: Additional unit tests for Gateway methods require interface abstraction
// of Client and Parser for proper mocking. This would be implemented in
// future refactoring to support better testability.
//
// Integration tests can be added that test the full workflow with actual
// SEC API calls, but should be marked with build tags for optional execution.
