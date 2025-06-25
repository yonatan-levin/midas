package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestRetryConfig_DefaultValues(t *testing.T) {
	config := DefaultRetryConfig()

	assert.Equal(t, 3, config.MaxAttempts)
	assert.Equal(t, 100*time.Millisecond, config.BaseDelay)
	assert.Equal(t, BackoffExponential, config.Strategy)
	assert.Equal(t, 5*time.Second, config.MaxDelay)
	assert.Equal(t, true, config.Jitter)
}

func TestRetry_SuccessfulOperation(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		Strategy:    BackoffExponential,
		MaxDelay:    1 * time.Second,
		Jitter:      false,
	}

	retry := NewRetryPolicy(config, zap.NewNop())
	ctx := context.Background()

	t.Run("succeeds on first attempt", func(t *testing.T) {
		callCount := 0
		operation := func() error {
			callCount++
			return nil
		}

		err := retry.Execute(ctx, operation)

		assert.NoError(t, err)
		assert.Equal(t, 1, callCount)
	})
}

func TestRetry_FailureAndRetry(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		Strategy:    BackoffExponential,
		MaxDelay:    1 * time.Second,
		Jitter:      false,
	}

	retry := NewRetryPolicy(config, zap.NewNop())
	ctx := context.Background()

	t.Run("succeeds on second attempt", func(t *testing.T) {
		callCount := 0
		operation := func() error {
			callCount++
			if callCount == 1 {
				return errors.New("temporary error")
			}
			return nil
		}

		err := retry.Execute(ctx, operation)

		assert.NoError(t, err)
		assert.Equal(t, 2, callCount)
	})

	t.Run("fails after max attempts", func(t *testing.T) {
		callCount := 0
		operation := func() error {
			callCount++
			return errors.New("persistent error")
		}

		err := retry.Execute(ctx, operation)

		assert.Error(t, err)
		assert.Equal(t, 3, callCount) // Should try max attempts
		assert.Contains(t, err.Error(), "persistent error")
	})
}

func TestRetry_ExponentialBackoff(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   50 * time.Millisecond,
		Strategy:    BackoffExponential,
		MaxDelay:    1 * time.Second,
		Jitter:      false,
	}

	retry := NewRetryPolicy(config, zap.NewNop())
	ctx := context.Background()

	t.Run("delays increase exponentially", func(t *testing.T) {
		callTimes := make([]time.Time, 0)
		operation := func() error {
			callTimes = append(callTimes, time.Now())
			return errors.New("test error")
		}

		start := time.Now()
		err := retry.Execute(ctx, operation)

		assert.Error(t, err)
		assert.Len(t, callTimes, 3)

		// Check delays between calls (approximate due to timing)
		delay1 := callTimes[1].Sub(callTimes[0])
		delay2 := callTimes[2].Sub(callTimes[1])

		assert.Greater(t, delay1, 40*time.Millisecond)
		assert.Less(t, delay1, 60*time.Millisecond)

		assert.Greater(t, delay2, 90*time.Millisecond)
		assert.Less(t, delay2, 110*time.Millisecond)

		totalTime := time.Since(start)
		assert.Greater(t, totalTime, 140*time.Millisecond) // Should have some delay
	})
}

func TestRetry_ContextCancellation(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   100 * time.Millisecond,
		Strategy:    BackoffExponential,
		MaxDelay:    1 * time.Second,
		Jitter:      false,
	}

	retry := NewRetryPolicy(config, zap.NewNop())

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		callCount := 0
		operation := func() error {
			callCount++
			if callCount == 2 {
				cancel() // Cancel on second call
			}
			return errors.New("test error")
		}

		err := retry.Execute(ctx, operation)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
		assert.LessOrEqual(t, callCount, 2) // Should stop early
	})
}

func TestRetry_WithCircuitBreaker(t *testing.T) {
	retryConfig := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		Strategy:    BackoffExponential,
		MaxDelay:    1 * time.Second,
		Jitter:      false,
	}

	cbConfig := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      2,
		FailureTimeout:   100 * time.Millisecond,
		SuccessThreshold: 1,
		RequestTimeout:   1 * time.Second,
		ResetTimeout:     60 * time.Second,
	}

	retry := NewRetryPolicy(retryConfig, zap.NewNop())
	cb := NewCircuitBreaker(cbConfig, zap.NewNop())
	ctx := context.Background()

	t.Run("works together with circuit breaker", func(t *testing.T) {
		callCount := 0
		operation := func() error {
			callCount++
			return errors.New("test error")
		}

		// First, execute through circuit breaker with retry
		err := retry.Execute(ctx, func() error {
			return cb.Execute(ctx, operation)
		})

		assert.Error(t, err)
		// Circuit breaker opens after 2 failures (MaxFailures: 2), so third retry is rejected
		assert.Equal(t, 2, callCount) // Should call 2 times before circuit opens

		// Verify circuit breaker is now open
		assert.Equal(t, "OPEN", cb.State())

		// Reset for next test
		callCount = 0

		// Now retry should fail quickly due to open circuit
		err = retry.Execute(ctx, func() error {
			return cb.Execute(ctx, operation)
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circuit breaker is open")
		// When circuit is open, all retry attempts are rejected immediately
		assert.Equal(t, 0, callCount) // No calls should be made to the operation
	})
}
