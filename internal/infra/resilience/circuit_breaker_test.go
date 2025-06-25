package resilience

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func createTestCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	logger := zap.NewNop()
	return NewCircuitBreaker(config, logger).(*CircuitBreaker)
}

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	t.Run("default config has sensible values", func(t *testing.T) {
		config := DefaultCircuitBreakerConfig("test")

		assert.Equal(t, "test", config.Name)
		assert.Equal(t, 5, config.MaxFailures)
		assert.Equal(t, 30*time.Second, config.FailureTimeout)
		assert.Equal(t, 3, config.SuccessThreshold)
		assert.Equal(t, 10*time.Second, config.RequestTimeout)
		assert.Equal(t, 60*time.Second, config.ResetTimeout)
	})
}

func TestCircuitBreaker_InitialState(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      3,
		FailureTimeout:   30 * time.Second,
		SuccessThreshold: 2,
		RequestTimeout:   5 * time.Second,
		ResetTimeout:     60 * time.Second,
	}

	cb := createTestCircuitBreaker(config)

	t.Run("starts in closed state", func(t *testing.T) {
		assert.Equal(t, "CLOSED", cb.State())
	})

	t.Run("allows requests initially", func(t *testing.T) {
		assert.True(t, cb.allowRequest())
	})
}

func TestCircuitBreaker_ExecuteSuccess(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      3,
		FailureTimeout:   30 * time.Second,
		SuccessThreshold: 2,
		RequestTimeout:   5 * time.Second,
		ResetTimeout:     60 * time.Second,
	}

	cb := createTestCircuitBreaker(config)
	ctx := context.Background()

	t.Run("successful execution", func(t *testing.T) {
		callCount := 0
		fn := func() error {
			callCount++
			return nil
		}

		err := cb.Execute(ctx, fn)

		assert.NoError(t, err)
		assert.Equal(t, 1, callCount)
		assert.Equal(t, "CLOSED", cb.State())
	})

	t.Run("multiple successful executions", func(t *testing.T) {
		callCount := 0
		fn := func() error {
			callCount++
			return nil
		}

		for i := 0; i < 5; i++ {
			err := cb.Execute(ctx, fn)
			assert.NoError(t, err)
		}

		assert.Equal(t, 5, callCount)
		assert.Equal(t, "CLOSED", cb.State())
	})
}

func TestCircuitBreaker_ExecuteFailure(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      3,
		FailureTimeout:   30 * time.Second,
		SuccessThreshold: 2,
		RequestTimeout:   5 * time.Second,
		ResetTimeout:     60 * time.Second,
	}

	t.Run("single failure keeps circuit closed", func(t *testing.T) {
		cb := createTestCircuitBreaker(config) // Fresh instance for test isolation
		ctx := context.Background()

		fn := func() error {
			return errors.New("test error")
		}

		err := cb.Execute(ctx, fn)

		assert.Error(t, err)
		assert.Equal(t, "CLOSED", cb.State())
	})

	t.Run("failures below threshold keep circuit closed", func(t *testing.T) {
		cb := createTestCircuitBreaker(config) // Fresh instance for test isolation
		ctx := context.Background()

		fn := func() error {
			return errors.New("test error")
		}

		// Fail twice (below threshold of 3)
		for i := 0; i < 2; i++ {
			err := cb.Execute(ctx, fn)
			assert.Error(t, err)
			assert.Equal(t, "CLOSED", cb.State())
		}
	})

	t.Run("failures at threshold open circuit", func(t *testing.T) {
		cb := createTestCircuitBreaker(config) // Fresh instance for test isolation
		ctx := context.Background()

		fn := func() error {
			return errors.New("test error")
		}

		// Execute 3 failures to reach threshold
		for i := 0; i < 3; i++ {
			err := cb.Execute(ctx, fn)
			assert.Error(t, err)
		}

		assert.Equal(t, "OPEN", cb.State())
	})
}

func TestCircuitBreaker_OpenState(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      2,
		FailureTimeout:   100 * time.Millisecond,
		SuccessThreshold: 2, // Changed from 1 to 2 to allow testing HALF_OPEN state
		RequestTimeout:   5 * time.Second,
		ResetTimeout:     60 * time.Second,
	}

	cb := createTestCircuitBreaker(config)
	ctx := context.Background()

	// Open the circuit by causing failures
	fn := func() error {
		return errors.New("test error")
	}

	for i := 0; i < 2; i++ {
		_ = cb.Execute(ctx, fn)
	}

	require.Equal(t, "OPEN", cb.State())

	t.Run("rejects requests when open", func(t *testing.T) {
		callCount := 0
		fn := func() error {
			callCount++
			return nil
		}

		err := cb.Execute(ctx, fn)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "circuit breaker is open")
		assert.Equal(t, 0, callCount) // Function should not be called
	})

	t.Run("transitions to half-open after timeout", func(t *testing.T) {
		// Create a fresh circuit breaker for this sub-test to ensure isolation
		cbFresh := createTestCircuitBreaker(config)
		ctxFresh := context.Background()

		// Open the circuit by causing failures
		fnFail := func() error {
			return errors.New("test error")
		}

		for i := 0; i < 2; i++ {
			_ = cbFresh.Execute(ctxFresh, fnFail)
		}

		require.Equal(t, "OPEN", cbFresh.State())

		// Wait for failure timeout
		time.Sleep(150 * time.Millisecond)

		assert.True(t, cbFresh.allowRequest()) // Should allow request now

		callCount := 0
		fn := func() error {
			callCount++
			return nil
		}

		err := cbFresh.Execute(ctxFresh, fn)

		assert.NoError(t, err)
		assert.Equal(t, 1, callCount)
		assert.Equal(t, "HALF_OPEN", cbFresh.State())
	})
}

func TestCircuitBreaker_HalfOpenState(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      2,
		FailureTimeout:   50 * time.Millisecond,
		SuccessThreshold: 2,
		RequestTimeout:   5 * time.Second,
		ResetTimeout:     60 * time.Second,
	}

	cb := createTestCircuitBreaker(config)
	ctx := context.Background()

	// Open the circuit
	fn := func() error {
		return errors.New("test error")
	}

	for i := 0; i < 2; i++ {
		_ = cb.Execute(ctx, fn)
	}

	// Wait and execute successful call to get to half-open
	time.Sleep(60 * time.Millisecond)
	successFn := func() error { return nil }
	_ = cb.Execute(ctx, successFn)

	require.Equal(t, "HALF_OPEN", cb.State())

	t.Run("failure in half-open returns to open", func(t *testing.T) {
		fn := func() error {
			return errors.New("test error")
		}

		err := cb.Execute(ctx, fn)

		assert.Error(t, err)
		assert.Equal(t, "OPEN", cb.State())
	})

	// Reset to half-open state
	time.Sleep(60 * time.Millisecond)
	_ = cb.Execute(ctx, successFn)
	require.Equal(t, "HALF_OPEN", cb.State())

	t.Run("enough successes close circuit", func(t *testing.T) {
		fn := func() error {
			return nil
		}

		// Need 2 successes (SuccessThreshold)
		err := cb.Execute(ctx, fn)
		assert.NoError(t, err)
		assert.Equal(t, "CLOSED", cb.State())
	})
}

func TestCircuitBreaker_Timeout(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      5,
		FailureTimeout:   30 * time.Second,
		SuccessThreshold: 2,
		RequestTimeout:   100 * time.Millisecond,
		ResetTimeout:     60 * time.Second,
	}

	cb := createTestCircuitBreaker(config)
	ctx := context.Background()

	t.Run("times out slow function", func(t *testing.T) {
		fn := func() error {
			time.Sleep(200 * time.Millisecond) // Longer than timeout
			return nil
		}

		start := time.Now()
		err := cb.Execute(ctx, fn)
		duration := time.Since(start)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deadline exceeded")
		assert.Less(t, duration, 150*time.Millisecond) // Should timeout quickly
	})

	t.Run("fast function completes normally", func(t *testing.T) {
		fn := func() error {
			time.Sleep(50 * time.Millisecond) // Faster than timeout
			return nil
		}

		err := cb.Execute(ctx, fn)

		assert.NoError(t, err)
	})
}

func TestCircuitBreaker_PanicRecovery(t *testing.T) {
	config := DefaultCircuitBreakerConfig("test")
	cb := createTestCircuitBreaker(config)
	ctx := context.Background()

	t.Run("recovers from panic", func(t *testing.T) {
		fn := func() error {
			panic("test panic")
		}

		err := cb.Execute(ctx, fn)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "function panicked")
	})
}

func TestCircuitBreaker_Reset(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      2,
		FailureTimeout:   30 * time.Second,
		SuccessThreshold: 2,
		RequestTimeout:   5 * time.Second,
		ResetTimeout:     60 * time.Second,
	}

	cb := createTestCircuitBreaker(config)
	ctx := context.Background()

	// Open the circuit
	fn := func() error {
		return errors.New("test error")
	}

	for i := 0; i < 2; i++ {
		_ = cb.Execute(ctx, fn)
	}

	require.Equal(t, "OPEN", cb.State())

	t.Run("manual reset returns to closed state", func(t *testing.T) {
		cb.Reset()

		assert.Equal(t, "CLOSED", cb.State())

		// Should allow requests again
		successFn := func() error { return nil }
		err := cb.Execute(ctx, successFn)
		assert.NoError(t, err)
	})
}

func TestCircuitBreaker_Concurrency(t *testing.T) {
	config := CircuitBreakerConfig{
		Name:             "test",
		MaxFailures:      10,
		FailureTimeout:   30 * time.Second,
		SuccessThreshold: 5,
		RequestTimeout:   1 * time.Second,
		ResetTimeout:     60 * time.Second,
	}

	cb := createTestCircuitBreaker(config)
	ctx := context.Background()

	t.Run("concurrent successful executions", func(t *testing.T) {
		const numGoroutines = 50
		const numExecutions = 10

		var wg sync.WaitGroup
		var successCount, errorCount int64
		var mu sync.Mutex

		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()

				for j := 0; j < numExecutions; j++ {
					fn := func() error {
						time.Sleep(1 * time.Millisecond)
						return nil
					}

					err := cb.Execute(ctx, fn)

					mu.Lock()
					if err != nil {
						errorCount++
					} else {
						successCount++
					}
					mu.Unlock()
				}
			}()
		}

		wg.Wait()

		// All should succeed since we're not hitting failure threshold
		assert.Equal(t, int64(numGoroutines*numExecutions), successCount)
		assert.Equal(t, int64(0), errorCount)
		assert.Equal(t, "CLOSED", cb.State())
	})

	t.Run("concurrent executions with some failures", func(t *testing.T) {
		const numGoroutines = 20

		var wg sync.WaitGroup
		var executionCount int64
		var mu sync.Mutex

		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(id int) {
				defer wg.Done()

				fn := func() error {
					if id%5 == 0 { // 20% failure rate
						return errors.New("test error")
					}
					return nil
				}

				err := cb.Execute(ctx, fn)

				mu.Lock()
				executionCount++
				mu.Unlock()

				// Error is expected for some calls
				_ = err
			}(i)
		}

		wg.Wait()

		assert.Equal(t, int64(numGoroutines), executionCount)
		// Circuit should still be closed since we're below threshold
		assert.Equal(t, "CLOSED", cb.State())
	})
}

func TestCircuitBreaker_States(t *testing.T) {
	t.Run("state string representation", func(t *testing.T) {
		assert.Equal(t, "CLOSED", StateClosed.String())
		assert.Equal(t, "OPEN", StateOpen.String())
		assert.Equal(t, "HALF_OPEN", StateHalfOpen.String())
		assert.Equal(t, "UNKNOWN", State(999).String())
	})
}

func TestCircuitBreaker_ContextCancellation(t *testing.T) {
	config := DefaultCircuitBreakerConfig("test")
	cb := createTestCircuitBreaker(config)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		fn := func() error {
			time.Sleep(100 * time.Millisecond)
			return nil
		}

		// Cancel context immediately
		cancel()

		err := cb.Execute(ctx, fn)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})

	t.Run("respects context timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		fn := func() error {
			time.Sleep(100 * time.Millisecond)
			return nil
		}

		err := cb.Execute(ctx, fn)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deadline exceeded")
	})
}
