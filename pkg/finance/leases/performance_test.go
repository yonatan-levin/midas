package leases

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/assert"
)

// TestCacheKey tests the CacheKey struct and its methods
func TestCacheKey_String(t *testing.T) {
	key := CacheKey{
		Ticker:     "AAPL",
		FilingDate: time.Date(2023, 9, 30, 0, 0, 0, 0, time.UTC),
		ConfigHash: "abcdef1234567890",
		DataHash:   "1234567890abcdef",
	}

	expected := "lease_pv_AAPL_2023-09-30_abcdef12_12345678"
	assert.Equal(t, expected, key.String())
}

// TestCacheEntry tests the CacheEntry struct and its methods
func TestCacheEntry_IsExpired(t *testing.T) {
	t.Run("not_expired", func(t *testing.T) {
		entry := &CacheEntry{
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}
		assert.False(t, entry.IsExpired())
	})

	t.Run("expired", func(t *testing.T) {
		entry := &CacheEntry{
			ExpiresAt: time.Now().Add(-1 * time.Hour),
		}
		assert.True(t, entry.IsExpired())
	})
}

// TestLeaseCalculationCache tests the cache implementation
func TestLeaseCalculationCache(t *testing.T) {
	t.Run("new_cache", func(t *testing.T) {
		cache := NewLeaseCalculationCache(1*time.Hour, 100)
		assert.NotNil(t, cache)

		stats := cache.GetStats()
		assert.Equal(t, 0, stats.Size)
		assert.Equal(t, 100, stats.MaxSize)
		assert.Equal(t, int64(0), stats.HitCount)
		assert.Equal(t, int64(0), stats.MissCount)
	})

	t.Run("set_and_get", func(t *testing.T) {
		cache := NewLeaseCalculationCache(1*time.Hour, 100)

		key := &CacheKey{
			Ticker:     "TEST",
			FilingDate: time.Now(),
			ConfigHash: "config1234567890abcdef",
			DataHash:   "data1234567890abcdef",
		}

		result := &PresentValueResult{
			PresentValue: 1000000.0,
		}

		cache.Set(key, result)
		retrievedResult, found := cache.Get(key)

		assert.True(t, found)
		assert.Equal(t, 1000000.0, retrievedResult.PresentValue)
	})

	t.Run("cache_miss", func(t *testing.T) {
		cache := NewLeaseCalculationCache(1*time.Hour, 100)

		key := &CacheKey{
			Ticker:     "MISS",
			FilingDate: time.Now(),
			ConfigHash: "config1234567890abcdef",
			DataHash:   "data1234567890abcdef",
		}

		result, found := cache.Get(key)
		assert.False(t, found)
		assert.Nil(t, result)
	})

	t.Run("cache_clear", func(t *testing.T) {
		cache := NewLeaseCalculationCache(1*time.Hour, 100)

		key := &CacheKey{
			Ticker:     "CLEAR",
			FilingDate: time.Now(),
			ConfigHash: "config1234567890abcdef",
			DataHash:   "data1234567890abcdef",
		}
		result := &PresentValueResult{PresentValue: 1000.0}
		cache.Set(key, result)

		cache.Clear()

		stats := cache.GetStats()
		assert.Equal(t, 0, stats.Size)
	})
}

// TestCircuitBreakerState tests the circuit breaker state enum
func TestCircuitBreakerState_String(t *testing.T) {
	assert.Equal(t, "closed", StateClosed.String())
	assert.Equal(t, "open", StateOpen.String())
	assert.Equal(t, "half_open", StateHalfOpen.String())
}

// TestCircuitBreaker tests the circuit breaker implementation
func TestCircuitBreaker(t *testing.T) {
	t.Run("new_circuit_breaker", func(t *testing.T) {
		config := &CircuitBreakerConfig{
			FailureThreshold: 3,
			RecoveryTimeout:  30 * time.Second,
			HalfOpenMaxCalls: 2,
			CallTimeout:      5 * time.Second,
		}

		cb := NewCircuitBreaker(config)
		assert.NotNil(t, cb)
		assert.Equal(t, StateClosed, cb.GetState())
	})

	t.Run("successful_calls", func(t *testing.T) {
		config := &CircuitBreakerConfig{
			FailureThreshold: 3,
			RecoveryTimeout:  30 * time.Second,
			HalfOpenMaxCalls: 2,
			CallTimeout:      5 * time.Second,
		}

		cb := NewCircuitBreaker(config)
		ctx := context.Background()

		successfulCall := func(ctx context.Context) (*PresentValueResult, error) {
			return &PresentValueResult{PresentValue: 1000.0}, nil
		}

		result, err := cb.Call(ctx, successfulCall)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 1000.0, result.PresentValue)

		assert.Equal(t, StateClosed, cb.GetState())
	})

	t.Run("circuit_opens_on_failures", func(t *testing.T) {
		config := &CircuitBreakerConfig{
			FailureThreshold: 2, // Lower threshold for faster testing
			RecoveryTimeout:  30 * time.Second,
			HalfOpenMaxCalls: 2,
			CallTimeout:      5 * time.Second,
		}

		cb := NewCircuitBreaker(config)
		ctx := context.Background()

		failingCall := func(ctx context.Context) (*PresentValueResult, error) {
			return nil, errors.New("calculation failed")
		}

		// Make failing calls to trigger open state
		for i := 0; i < 2; i++ {
			result, err := cb.Call(ctx, failingCall)
			assert.Error(t, err)
			assert.Nil(t, result)
		}

		assert.Equal(t, StateOpen, cb.GetState())

		// Next call should be rejected
		result, err := cb.Call(ctx, failingCall)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "circuit breaker is open")
	})

	t.Run("reset", func(t *testing.T) {
		config := &CircuitBreakerConfig{
			FailureThreshold: 1,
			RecoveryTimeout:  30 * time.Second,
			HalfOpenMaxCalls: 2,
			CallTimeout:      5 * time.Second,
		}

		cb := NewCircuitBreaker(config)
		ctx := context.Background()

		failingCall := func(ctx context.Context) (*PresentValueResult, error) {
			return nil, errors.New("calculation failed")
		}

		// Trigger open state
		_, _ = cb.Call(ctx, failingCall)
		assert.Equal(t, StateOpen, cb.GetState())

		// Reset should close it
		cb.Reset()
		assert.Equal(t, StateClosed, cb.GetState())
	})
}

// TestCacheKeyGenerator tests cache key generation
func TestCacheKeyGenerator(t *testing.T) {
	t.Run("consistent_keys", func(t *testing.T) {
		generator := NewCacheKeyGenerator("1.0.0")

		data := &entities.FinancialData{
			Ticker:                  "AAPL",
			FilingDate:              time.Date(2023, 9, 30, 0, 0, 0, 0, time.UTC),
			OperatingLeaseLiability: 1000000.0,
			Revenue:                 100000000.0,
		}

		context := &entities.CleaningContext{}
		config := &EstimationConfig{
			DiscountRateMethod:  "risk_free_plus_spread",
			DefaultDiscountRate: 0.06,
		}

		key1 := generator.GenerateKey(data, context, config)
		key2 := generator.GenerateKey(data, context, config)

		assert.Equal(t, key1.Ticker, key2.Ticker)
		assert.Equal(t, key1.ConfigHash, key2.ConfigHash)
		assert.Equal(t, key1.DataHash, key2.DataHash)
	})

	t.Run("different_data_different_keys", func(t *testing.T) {
		generator := NewCacheKeyGenerator("1.0.0")

		data1 := &entities.FinancialData{
			Ticker:                  "AAPL",
			OperatingLeaseLiability: 1000000.0,
		}

		data2 := &entities.FinancialData{
			Ticker:                  "AAPL",
			OperatingLeaseLiability: 2000000.0, // Different
		}

		context := &entities.CleaningContext{}
		config := &EstimationConfig{DefaultDiscountRate: 0.06}

		key1 := generator.GenerateKey(data1, context, config)
		key2 := generator.GenerateKey(data2, context, config)

		assert.NotEqual(t, key1.DataHash, key2.DataHash)
	})
}

// TestPerformanceOptimizedCalculator tests the main calculator
func TestPerformanceOptimizedCalculator(t *testing.T) {
	config := &EstimationConfig{
		DiscountRateMethod:  "risk_free_plus_spread",
		DefaultDiscountRate: 0.06,
		CacheEnabled:        true,
		CacheTTL:            1 * time.Hour,
		CalculationTimeout:  30 * time.Second,
	}

	t.Run("new_calculator", func(t *testing.T) {
		calc := NewPerformanceOptimizedCalculator(config)
		assert.NotNil(t, calc)
		assert.NotNil(t, calc.calculator)
		assert.NotNil(t, calc.cache)
		assert.NotNil(t, calc.circuitBreaker)
		assert.NotNil(t, calc.keyGenerator)
	})

	t.Run("calculator_without_cache", func(t *testing.T) {
		configNoCache := *config
		configNoCache.CacheEnabled = false

		calc := NewPerformanceOptimizedCalculator(&configNoCache)
		assert.NotNil(t, calc)
		assert.Nil(t, calc.cache)

		stats := calc.GetCacheStats()
		assert.Nil(t, stats)
	})

	t.Run("get_stats", func(t *testing.T) {
		calc := NewPerformanceOptimizedCalculator(config)

		cacheStats := calc.GetCacheStats()
		assert.NotNil(t, cacheStats)

		cbStats := calc.GetCircuitBreakerStats()
		assert.Equal(t, StateClosed, cbStats.State)
	})

	t.Run("clear_cache", func(t *testing.T) {
		calc := NewPerformanceOptimizedCalculator(config)
		calc.ClearCache() // Should not panic

		stats := calc.GetCacheStats()
		assert.NotNil(t, stats)
	})

	t.Run("reset_circuit_breaker", func(t *testing.T) {
		calc := NewPerformanceOptimizedCalculator(config)
		calc.ResetCircuitBreaker() // Should not panic

		stats := calc.GetCircuitBreakerStats()
		assert.Equal(t, StateClosed, stats.State)
	})
}
