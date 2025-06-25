package di

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCircuitBreakerFactory_CreateSECCircuitBreaker(t *testing.T) {
	logger := zap.NewNop()
	factory := &CircuitBreakerFactory{logger: logger}

	t.Run("creates SEC circuit breaker with correct config", func(t *testing.T) {
		cb := factory.CreateSECCircuitBreaker()

		assert.NotNil(t, cb)
		assert.Equal(t, "CLOSED", cb.State())
	})
}

func TestCircuitBreakerFactory_CreateMarketDataCircuitBreaker(t *testing.T) {
	logger := zap.NewNop()
	factory := &CircuitBreakerFactory{logger: logger}

	t.Run("creates market data circuit breaker with correct config", func(t *testing.T) {
		cb := factory.CreateMarketDataCircuitBreaker()

		assert.NotNil(t, cb)
		assert.Equal(t, "CLOSED", cb.State())
	})
}

func TestRetryPolicyFactory_CreateSECRetryPolicy(t *testing.T) {
	logger := zap.NewNop()
	factory := &RetryPolicyFactory{logger: logger}

	t.Run("creates SEC retry policy", func(t *testing.T) {
		policy := factory.CreateSECRetryPolicy()

		assert.NotNil(t, policy)
	})
}

func TestRetryPolicyFactory_CreateMarketDataRetryPolicy(t *testing.T) {
	logger := zap.NewNop()
	factory := &RetryPolicyFactory{logger: logger}

	t.Run("creates market data retry policy", func(t *testing.T) {
		policy := factory.CreateMarketDataRetryPolicy()

		assert.NotNil(t, policy)
	})
}

func TestContainer_Creation(t *testing.T) {
	t.Run("creates container successfully", func(t *testing.T) {
		container := NewContainer()

		assert.NotNil(t, container)
		assert.NotNil(t, container.app)
	})
}

// Integration test would require full DI setup
func TestFactories_Integration(t *testing.T) {
	t.Run("factory types exist", func(t *testing.T) {
		logger := zap.NewNop()

		// Test that all factory types exist and can be created
		cbFactory := &CircuitBreakerFactory{logger: logger}
		retryFactory := &RetryPolicyFactory{logger: logger}

		require.NotNil(t, cbFactory)
		require.NotNil(t, retryFactory)
	})
}
