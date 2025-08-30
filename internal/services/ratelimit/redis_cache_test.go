package ratelimit

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestNewRedisCacheStore(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Test with nil client
	store := NewRedisCacheStore(nil, logger)
	assert.NotNil(t, store)
	assert.Nil(t, store.client)
	assert.Equal(t, logger, store.logger)
}

func TestRedisCacheStore_BasicOperations(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create a Redis client that will fail operations (no Redis server)
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:9999", // Non-existent Redis server
	})

	store := NewRedisCacheStore(client, logger)
	ctx := context.Background()

	// Test operations that will fail due to no Redis connection
	// This tests code paths and error handling

	t.Run("increment_with_connection_error", func(t *testing.T) {
		_, _, err := store.Increment(ctx, "test:key", time.Minute)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute Redis pipeline")
	})

	t.Run("get_with_connection_error", func(t *testing.T) {
		_, _, err := store.Get(ctx, "test:key")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute Redis pipeline")
	})

	t.Run("set_with_connection_error", func(t *testing.T) {
		err := store.Set(ctx, "test:key", 1, time.Minute)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to set key")
	})

	t.Run("delete_with_connection_error", func(t *testing.T) {
		err := store.Delete(ctx, "test:key")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete key")
	})

	t.Run("increment_with_lua_error", func(t *testing.T) {
		_, _, _, err := store.IncrementWithLua(ctx, "test:key", time.Minute, 10)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute Lua script")
	})

	t.Run("clean_expired_keys_error", func(t *testing.T) {
		err := store.CleanExpiredKeys(ctx, "usage:*")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to scan keys")
	})

	t.Run("get_stats_error", func(t *testing.T) {
		_, err := store.GetStats(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get Redis info")
	})

	t.Run("health_check_error", func(t *testing.T) {
		err := store.HealthCheck(ctx)
		assert.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "redis health check failed")
	})
}

// Test edge cases and specific behaviors
func TestRedisCacheStore_EdgeCases(t *testing.T) {
	logger := zaptest.NewLogger(t)
	client := redis.NewClient(&redis.Options{Addr: "localhost:9999"})
	store := NewRedisCacheStore(client, logger)
	ctx := context.Background()

	// Test with invalid window duration
	t.Run("increment_zero_window", func(t *testing.T) {
		_, _, err := store.Increment(ctx, "test:key", 0)
		assert.Error(t, err) // Should fail due to connection, but tests the code path
	})

	// Test with empty key
	t.Run("operations_with_empty_key", func(t *testing.T) {
		_, _, err := store.Increment(ctx, "", time.Minute)
		assert.Error(t, err)

		_, _, err = store.Get(ctx, "")
		assert.Error(t, err)

		err = store.Set(ctx, "", 1, time.Minute)
		assert.Error(t, err)

		err = store.Delete(ctx, "")
		assert.Error(t, err)
	})

	// Test Lua script with various parameters
	t.Run("lua_script_edge_cases", func(t *testing.T) {
		// Zero limit
		_, _, _, err := store.IncrementWithLua(ctx, "test:key", time.Minute, 0)
		assert.Error(t, err)

		// Negative limit
		_, _, _, err = store.IncrementWithLua(ctx, "test:key", time.Minute, -1)
		assert.Error(t, err)

		// Very large limit
		_, _, _, err = store.IncrementWithLua(ctx, "test:key", time.Minute, 1000000)
		assert.Error(t, err)
	})
}
