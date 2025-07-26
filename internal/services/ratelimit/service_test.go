package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestRateLimiter_AllowRequest(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)

	tests := []struct {
		name          string
		limits        []LimitConfig
		requests      []TestRequest
		wantAllowed   []bool
		wantRemaining []int
		setupCache    func() CacheStore
	}{
		{
			name: "api_key_rate_limit_success",
			limits: []LimitConfig{
				{
					Type:        LimitTypeAPIKey,
					Identifier:  "test-key-123",
					MaxRequests: 5,
					Window:      time.Minute,
				},
			},
			requests: []TestRequest{
				{Identifier: "test-key-123", Type: LimitTypeAPIKey},
				{Identifier: "test-key-123", Type: LimitTypeAPIKey},
				{Identifier: "test-key-123", Type: LimitTypeAPIKey},
			},
			wantAllowed:   []bool{true, true, true},
			wantRemaining: []int{4, 3, 2},
			setupCache:    func() CacheStore { return newMockCacheStore() },
		},
		{
			name: "api_key_rate_limit_exceeded",
			limits: []LimitConfig{
				{
					Type:        LimitTypeAPIKey,
					Identifier:  "test-key-456",
					MaxRequests: 2,
					Window:      time.Minute,
				},
			},
			requests: []TestRequest{
				{Identifier: "test-key-456", Type: LimitTypeAPIKey},
				{Identifier: "test-key-456", Type: LimitTypeAPIKey},
				{Identifier: "test-key-456", Type: LimitTypeAPIKey}, // Should be denied
			},
			wantAllowed:   []bool{true, true, false},
			wantRemaining: []int{1, 0, 0},
			setupCache:    func() CacheStore { return newMockCacheStore() },
		},
		{
			name: "ip_based_rate_limit",
			limits: []LimitConfig{
				{
					Type:        LimitTypeIP,
					Identifier:  "192.168.1.100",
					MaxRequests: 3,
					Window:      time.Minute,
				},
			},
			requests: []TestRequest{
				{Identifier: "192.168.1.100", Type: LimitTypeIP},
				{Identifier: "192.168.1.100", Type: LimitTypeIP},
				{Identifier: "192.168.1.100", Type: LimitTypeIP},
				{Identifier: "192.168.1.100", Type: LimitTypeIP}, // Should be denied
			},
			wantAllowed:   []bool{true, true, true, false},
			wantRemaining: []int{2, 1, 0, 0},
			setupCache:    func() CacheStore { return newMockCacheStore() },
		},
		{
			name: "endpoint_specific_limits",
			limits: []LimitConfig{
				{
					Type:        LimitTypeEndpoint,
					Identifier:  "/api/v1/fair-value",
					MaxRequests: 10,
					Window:      time.Minute,
				},
			},
			requests: []TestRequest{
				{Identifier: "/api/v1/fair-value", Type: LimitTypeEndpoint},
				{Identifier: "/api/v1/fair-value", Type: LimitTypeEndpoint},
			},
			wantAllowed:   []bool{true, true},
			wantRemaining: []int{9, 8},
			setupCache:    func() CacheStore { return newMockCacheStore() },
		},
		{
			name: "multiple_limit_types",
			limits: []LimitConfig{
				{
					Type:        LimitTypeAPIKey,
					Identifier:  "multi-test-key",
					MaxRequests: 5,
					Window:      time.Minute,
				},
				{
					Type:        LimitTypeIP,
					Identifier:  "192.168.1.200",
					MaxRequests: 3,
					Window:      time.Minute,
				},
			},
			requests: []TestRequest{
				{Identifier: "multi-test-key", Type: LimitTypeAPIKey, IPAddress: "192.168.1.200"},
				{Identifier: "multi-test-key", Type: LimitTypeAPIKey, IPAddress: "192.168.1.200"},
				{Identifier: "multi-test-key", Type: LimitTypeAPIKey, IPAddress: "192.168.1.200"},
				{Identifier: "multi-test-key", Type: LimitTypeAPIKey, IPAddress: "192.168.1.200"}, // IP limit exceeded
			},
			wantAllowed:   []bool{true, true, true, false},
			wantRemaining: []int{2, 1, 0, 0}, // Most restrictive remaining count (IP has 3 limit)
			setupCache:    func() CacheStore { return newMockCacheStore() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := tt.setupCache()
			limiter := NewRateLimiter(cache, logger)

			// Configure limits
			for _, limit := range tt.limits {
				err := limiter.SetLimit(ctx, limit)
				require.NoError(t, err)
			}

			// Execute requests and check results
			for i, req := range tt.requests {
				result, err := limiter.AllowRequest(ctx, RateLimitRequest{
					Identifier: req.Identifier,
					Type:       req.Type,
					IPAddress:  req.IPAddress,
					Endpoint:   req.Endpoint,
				})

				require.NoError(t, err)
				assert.Equal(t, tt.wantAllowed[i], result.Allowed,
					"request %d: expected allowed=%v, got=%v", i, tt.wantAllowed[i], result.Allowed)
				assert.Equal(t, tt.wantRemaining[i], result.Remaining,
					"request %d: expected remaining=%d, got=%d", i, tt.wantRemaining[i], result.Remaining)

				if result.Allowed {
					assert.True(t, result.ResetTime.After(time.Now()))
				}
			}
		})
	}
}

func TestRateLimiter_BurstHandling(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// Set up burst-friendly limit
	limit := LimitConfig{
		Type:         LimitTypeAPIKey,
		Identifier:   "burst-test-key",
		MaxRequests:  10,
		Window:       time.Second,
		BurstAllowed: true,
		BurstSize:    15, // Allow 15 requests in quick succession
	}

	err := limiter.SetLimit(ctx, limit)
	require.NoError(t, err)

	// Send burst of requests
	allowedCount := 0
	for i := 0; i < 20; i++ {
		result, err := limiter.AllowRequest(ctx, RateLimitRequest{
			Identifier: "burst-test-key",
			Type:       LimitTypeAPIKey,
		})
		require.NoError(t, err)

		if result.Allowed {
			allowedCount++
		}
	}

	// Should allow burst size (15), not just the per-second limit (10)
	assert.Equal(t, 15, allowedCount, "burst handling should allow %d requests", 15)
}

func TestRateLimiter_WindowReset(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// Use very short window for testing
	limit := LimitConfig{
		Type:        LimitTypeAPIKey,
		Identifier:  "reset-test-key",
		MaxRequests: 2,
		Window:      100 * time.Millisecond,
	}

	err := limiter.SetLimit(ctx, limit)
	require.NoError(t, err)

	// Fill the limit
	result1, err := limiter.AllowRequest(ctx, RateLimitRequest{
		Identifier: "reset-test-key",
		Type:       LimitTypeAPIKey,
	})
	require.NoError(t, err)
	assert.True(t, result1.Allowed)

	result2, err := limiter.AllowRequest(ctx, RateLimitRequest{
		Identifier: "reset-test-key",
		Type:       LimitTypeAPIKey,
	})
	require.NoError(t, err)
	assert.True(t, result2.Allowed)

	// Should be blocked now
	result3, err := limiter.AllowRequest(ctx, RateLimitRequest{
		Identifier: "reset-test-key",
		Type:       LimitTypeAPIKey,
	})
	require.NoError(t, err)
	assert.False(t, result3.Allowed)

	// Wait for window to reset
	time.Sleep(150 * time.Millisecond)

	// Should be allowed again
	result4, err := limiter.AllowRequest(ctx, RateLimitRequest{
		Identifier: "reset-test-key",
		Type:       LimitTypeAPIKey,
	})
	require.NoError(t, err)
	assert.True(t, result4.Allowed)
	assert.Equal(t, 1, result4.Remaining)
}

func TestRateLimiter_GetUsage(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	limit := LimitConfig{
		Type:        LimitTypeAPIKey,
		Identifier:  "usage-test-key",
		MaxRequests: 5,
		Window:      time.Minute,
	}

	err := limiter.SetLimit(ctx, limit)
	require.NoError(t, err)

	// Make some requests
	for i := 0; i < 3; i++ {
		_, err := limiter.AllowRequest(ctx, RateLimitRequest{
			Identifier: "usage-test-key",
			Type:       LimitTypeAPIKey,
		})
		require.NoError(t, err)
	}

	// Check usage
	usage, err := limiter.GetUsage(ctx, "usage-test-key", LimitTypeAPIKey)
	require.NoError(t, err)
	assert.Equal(t, 3, usage.RequestsUsed)
	assert.Equal(t, 5, usage.MaxRequests)
	assert.Equal(t, 2, usage.Remaining)
}

func TestRateLimiter_SetLimit(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	t.Run("valid_config", func(t *testing.T) {
		config := LimitConfig{
			Type:        LimitTypeAPIKey,
			Identifier:  "test-key",
			MaxRequests: 100,
			Window:      time.Hour,
			Description: "Test limit",
		}

		err := limiter.SetLimit(ctx, config)
		assert.NoError(t, err)

		// Verify the limit was stored
		limits := limiter.GetLimits()
		key := "limit:api_key:test-key"
		stored, exists := limits[key]
		assert.True(t, exists)
		assert.Equal(t, config.Type, stored.Type)
		assert.Equal(t, config.Identifier, stored.Identifier)
		assert.Equal(t, config.MaxRequests, stored.MaxRequests)
	})

	t.Run("invalid_config_empty_type", func(t *testing.T) {
		config := LimitConfig{
			Identifier:  "test-key",
			MaxRequests: 100,
			Window:      time.Hour,
		}

		err := limiter.SetLimit(ctx, config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "limit type cannot be empty")
	})

	t.Run("invalid_config_empty_identifier", func(t *testing.T) {
		config := LimitConfig{
			Type:        LimitTypeAPIKey,
			MaxRequests: 100,
			Window:      time.Hour,
		}

		err := limiter.SetLimit(ctx, config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "identifier cannot be empty")
	})

	t.Run("invalid_config_zero_max_requests", func(t *testing.T) {
		config := LimitConfig{
			Type:        LimitTypeAPIKey,
			Identifier:  "test-key",
			MaxRequests: 0,
			Window:      time.Hour,
		}

		err := limiter.SetLimit(ctx, config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "max requests must be positive")
	})

	t.Run("invalid_config_zero_window", func(t *testing.T) {
		config := LimitConfig{
			Type:        LimitTypeAPIKey,
			Identifier:  "test-key",
			MaxRequests: 100,
			Window:      0,
		}

		err := limiter.SetLimit(ctx, config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "window must be positive")
	})

	t.Run("invalid_burst_config", func(t *testing.T) {
		config := LimitConfig{
			Type:         LimitTypeAPIKey,
			Identifier:   "test-key",
			MaxRequests:  100,
			Window:       time.Hour,
			BurstAllowed: true,
			BurstSize:    50, // Less than MaxRequests
		}

		err := limiter.SetLimit(ctx, config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "burst size must be greater than max requests")
	})
}

func TestRateLimiter_GetUsage_EdgeCases(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	t.Run("no_limit_configured", func(t *testing.T) {
		usage, err := limiter.GetUsage(ctx, "non-existent", LimitTypeAPIKey)
		assert.Error(t, err)
		assert.Nil(t, usage)
		assert.Contains(t, err.Error(), "no rate limit configured")
	})

	t.Run("cache_key_not_found", func(t *testing.T) {
		// Set up a limit but don't make any requests
		config := LimitConfig{
			Type:        LimitTypeAPIKey,
			Identifier:  "unused-key",
			MaxRequests: 10,
			Window:      time.Hour,
		}
		err := limiter.SetLimit(ctx, config)
		require.NoError(t, err)

		usage, err := limiter.GetUsage(ctx, "unused-key", LimitTypeAPIKey)
		assert.NoError(t, err)
		assert.Equal(t, 0, usage.RequestsUsed)
		assert.Equal(t, 10, usage.MaxRequests)
		assert.Equal(t, 10, usage.Remaining)
	})
}

func TestRateLimiter_RemoveLimit(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// Set up a limit
	config := LimitConfig{
		Type:        LimitTypeAPIKey,
		Identifier:  "remove-test",
		MaxRequests: 5,
		Window:      time.Minute,
	}
	err := limiter.SetLimit(ctx, config)
	require.NoError(t, err)

	// Make a request to create cache entry
	_, err = limiter.AllowRequest(ctx, RateLimitRequest{
		Identifier: "remove-test",
		Type:       LimitTypeAPIKey,
	})
	require.NoError(t, err)

	// Remove the limit
	err = limiter.RemoveLimit(ctx, LimitTypeAPIKey, "remove-test")
	assert.NoError(t, err)

	// Verify limit is removed
	limits := limiter.GetLimits()
	key := "limit:api_key:remove-test"
	_, exists := limits[key]
	assert.False(t, exists)
}

func TestRateLimiter_GetLimits(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// Initially empty
	limits := limiter.GetLimits()
	assert.Empty(t, limits)

	// Add some limits
	configs := []LimitConfig{
		{Type: LimitTypeAPIKey, Identifier: "key1", MaxRequests: 10, Window: time.Minute},
		{Type: LimitTypeIP, Identifier: "192.168.1.1", MaxRequests: 20, Window: time.Hour},
	}

	for _, config := range configs {
		err := limiter.SetLimit(ctx, config)
		require.NoError(t, err)
	}

	limits = limiter.GetLimits()
	assert.Len(t, limits, 2)

	// Verify it returns a copy (can't modify original)
	originalLen := len(limiter.limits)
	delete(limits, "limit:api_key:key1")       // Modify returned map
	assert.Len(t, limiter.limits, originalLen) // Original should be unchanged
}

func TestRateLimiter_AlignTimeWindow(t *testing.T) {
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	baseTime := time.Date(2023, 5, 15, 14, 35, 42, 123456789, time.UTC)

	tests := []struct {
		name     string
		window   time.Duration
		expected time.Time
	}{
		{
			name:     "daily_window",
			window:   24 * time.Hour,
			expected: time.Date(2023, 5, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "hourly_window",
			window:   time.Hour,
			expected: time.Date(2023, 5, 15, 14, 0, 0, 0, time.UTC),
		},
		{
			name:     "minute_window",
			window:   time.Minute,
			expected: time.Date(2023, 5, 15, 14, 35, 0, 0, time.UTC),
		},
		{
			name:     "sub_minute_window",
			window:   30 * time.Second,
			expected: time.Unix((baseTime.Unix()/30)*30, 0),
		},
		{
			name:     "very_short_window",
			window:   time.Millisecond,
			expected: time.Unix(baseTime.Unix(), 0), // Aligns to second boundary
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := limiter.alignTimeWindow(baseTime, tt.window)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRateLimiter_GetCacheKey(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// Set up a limit to ensure consistent key generation
	config := LimitConfig{
		Type:        LimitTypeAPIKey,
		Identifier:  "test-key",
		MaxRequests: 10,
		Window:      time.Minute,
	}
	err := limiter.SetLimit(ctx, config)
	require.NoError(t, err)

	key1 := limiter.getCacheKey(LimitTypeAPIKey, "test-key")
	key2 := limiter.getCacheKey(LimitTypeAPIKey, "test-key")

	// Keys should be consistent within the same time window
	assert.Equal(t, key1, key2)
	assert.Contains(t, key1, "usage:api_key:test-key:")
}

func TestRateLimiter_GetApplicableLimits(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// Set up various limits
	limits := []LimitConfig{
		{Type: LimitTypeAPIKey, Identifier: "test-key", MaxRequests: 10, Window: time.Minute},
		{Type: LimitTypeIP, Identifier: "192.168.1.1", MaxRequests: 20, Window: time.Minute},
		{Type: LimitTypeEndpoint, Identifier: "/api/v1/test", MaxRequests: 30, Window: time.Minute},
		{Type: LimitTypeGlobal, Identifier: "global", MaxRequests: 100, Window: time.Minute},
	}

	for _, limit := range limits {
		err := limiter.SetLimit(ctx, limit)
		require.NoError(t, err)
	}

	tests := []struct {
		name          string
		request       RateLimitRequest
		expectedCount int
		expectedTypes []LimitType
	}{
		{
			name: "all_limits_apply",
			request: RateLimitRequest{
				Identifier: "test-key",
				Type:       LimitTypeAPIKey,
				IPAddress:  "192.168.1.1",
				Endpoint:   "/api/v1/test",
			},
			expectedCount: 4,
			expectedTypes: []LimitType{LimitTypeAPIKey, LimitTypeIP, LimitTypeEndpoint, LimitTypeGlobal},
		},
		{
			name: "only_global_applies",
			request: RateLimitRequest{
				Identifier: "unknown-key",
				Type:       LimitTypeAPIKey,
				IPAddress:  "192.168.1.2",
				Endpoint:   "/api/v1/other",
			},
			expectedCount: 1,
			expectedTypes: []LimitType{LimitTypeGlobal},
		},
		{
			name: "api_key_and_global",
			request: RateLimitRequest{
				Identifier: "test-key",
				Type:       LimitTypeAPIKey,
			},
			expectedCount: 2,
			expectedTypes: []LimitType{LimitTypeAPIKey, LimitTypeGlobal},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applicable := limiter.getApplicableLimits(tt.request)
			assert.Len(t, applicable, tt.expectedCount)

			foundTypes := make(map[LimitType]bool)
			for _, limit := range applicable {
				foundTypes[limit.Type] = true
			}

			for _, expectedType := range tt.expectedTypes {
				assert.True(t, foundTypes[expectedType], "Expected limit type %s not found", expectedType)
			}
		})
	}
}

func TestRateLimiter_CleanExpiredEntries(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// This is mostly a placeholder method, but test it doesn't error
	err := limiter.CleanExpiredEntries(ctx)
	assert.NoError(t, err)
}

func TestRateLimiter_GetRateLimitHeaders(t *testing.T) {
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	t.Run("nil_result", func(t *testing.T) {
		headers := limiter.GetRateLimitHeaders(nil)
		assert.Empty(t, headers)
	})

	t.Run("valid_result", func(t *testing.T) {
		result := &RateLimitResult{
			Allowed:    true,
			Remaining:  5,
			ResetTime:  time.Unix(1684123456, 0),
			LimitType:  LimitTypeAPIKey,
			Identifier: "test-key",
		}

		headers := limiter.GetRateLimitHeaders(result)
		assert.Contains(t, headers, "X-RateLimit-Limit")
		assert.Contains(t, headers, "X-RateLimit-Remaining")
		assert.Contains(t, headers, "X-RateLimit-Reset")
		assert.Equal(t, "5", headers["X-RateLimit-Remaining"])
		assert.Equal(t, "1684123456", headers["X-RateLimit-Reset"])
	})

	t.Run("with_retry_after", func(t *testing.T) {
		result := &RateLimitResult{
			Allowed:    false,
			Remaining:  0,
			ResetTime:  time.Now().Add(30 * time.Second),
			RetryAfter: 30 * time.Second,
			LimitType:  LimitTypeAPIKey,
			Identifier: "test-key",
		}

		headers := limiter.GetRateLimitHeaders(result)
		assert.Contains(t, headers, "Retry-After")
		assert.Equal(t, "30", headers["Retry-After"])
	})
}

func TestRateLimiter_SetDefaultLimits(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	err := limiter.SetDefaultLimits(ctx)
	assert.NoError(t, err)

	limits := limiter.GetLimits()
	assert.Len(t, limits, 3) // Global, fair-value endpoint, health endpoint

	// Check specific limits exist
	globalKey := "limit:global:global"
	fairValueKey := "limit:endpoint:/api/v1/fair-value"
	healthKey := "limit:endpoint:/api/v1/health"

	assert.Contains(t, limits, globalKey)
	assert.Contains(t, limits, fairValueKey)
	assert.Contains(t, limits, healthKey)

	// Verify configurations
	globalLimit := limits[globalKey]
	assert.Equal(t, 1000, globalLimit.MaxRequests)
	assert.Equal(t, time.Minute, globalLimit.Window)

	fairValueLimit := limits[fairValueKey]
	assert.Equal(t, 60, fairValueLimit.MaxRequests)
	assert.Equal(t, time.Minute, fairValueLimit.Window)
}

func TestRateLimiter_NoLimitsConfigured(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// Make request without any limits configured
	result, err := limiter.AllowRequest(ctx, RateLimitRequest{
		Identifier: "test-key",
		Type:       LimitTypeAPIKey,
	})

	assert.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.Equal(t, 999, result.Remaining) // Default fallback
}

func TestRateLimiter_GlobalLimitOnly(t *testing.T) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	cache := newMockCacheStore()
	limiter := NewRateLimiter(cache, logger)

	// Set only global limit
	err := limiter.SetLimit(ctx, LimitConfig{
		Type:        LimitTypeGlobal,
		Identifier:  "global",
		MaxRequests: 5,
		Window:      time.Minute,
	})
	require.NoError(t, err)

	// Test that any request is subject to global limit
	for i := 0; i < 6; i++ {
		result, err := limiter.AllowRequest(ctx, RateLimitRequest{
			Identifier: "any-key",
			Type:       LimitTypeAPIKey,
		})
		require.NoError(t, err)

		if i < 5 {
			assert.True(t, result.Allowed, "Request %d should be allowed", i)
		} else {
			assert.False(t, result.Allowed, "Request %d should be denied", i)
		}
	}
}

// Test helper types and functions

type TestRequest struct {
	Identifier string
	Type       LimitType
	IPAddress  string
	Endpoint   string
}

// Mock cache store for testing
type mockCacheStore struct {
	data map[string]CacheEntry
}

type CacheEntry struct {
	Value     int
	ExpiresAt time.Time
}

func newMockCacheStore() *mockCacheStore {
	return &mockCacheStore{
		data: make(map[string]CacheEntry),
	}
}

func (m *mockCacheStore) Increment(ctx context.Context, key string, window time.Duration) (int, time.Time, error) {
	now := time.Now()

	// Clean expired entries
	if entry, exists := m.data[key]; exists && entry.ExpiresAt.Before(now) {
		delete(m.data, key)
	}

	// Get or create entry
	entry, exists := m.data[key]
	if !exists {
		entry = CacheEntry{
			Value:     0,
			ExpiresAt: now.Add(window),
		}
	}

	// Increment
	entry.Value++
	m.data[key] = entry

	return entry.Value, entry.ExpiresAt, nil
}

func (m *mockCacheStore) Get(ctx context.Context, key string) (int, time.Time, error) {
	entry, exists := m.data[key]
	if !exists {
		return 0, time.Time{}, ErrCacheKeyNotFound
	}

	if entry.ExpiresAt.Before(time.Now()) {
		delete(m.data, key)
		return 0, time.Time{}, ErrCacheKeyNotFound
	}

	return entry.Value, entry.ExpiresAt, nil
}

func (m *mockCacheStore) Set(ctx context.Context, key string, value int, window time.Duration) error {
	m.data[key] = CacheEntry{
		Value:     value,
		ExpiresAt: time.Now().Add(window),
	}
	return nil
}

func (m *mockCacheStore) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}
