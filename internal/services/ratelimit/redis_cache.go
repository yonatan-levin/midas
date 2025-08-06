package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisCacheStore implements CacheStore using Redis with atomic operations
type RedisCacheStore struct {
	client *redis.Client
	logger *zap.Logger
}

// NewRedisCacheStore creates a new Redis-based cache store
func NewRedisCacheStore(client *redis.Client, logger *zap.Logger) *RedisCacheStore {
	return &RedisCacheStore{
		client: client,
		logger: logger,
	}
}

// Increment atomically increments a counter and sets expiration if it's a new key
func (r *RedisCacheStore) Increment(ctx context.Context, key string, window time.Duration) (int, time.Time, error) {
	// Use Redis pipeline for atomic operations
	pipe := r.client.Pipeline()

	// Increment the counter
	incrCmd := pipe.Incr(ctx, key)

	// Set expiration only if this is the first increment (TTL will be -1 for new keys)
	ttlCmd := pipe.TTL(ctx, key)

	// Execute pipeline
	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to execute Redis pipeline: %w", err)
	}

	// Get the incremented value
	count, err := incrCmd.Result()
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to get increment result: %w", err)
	}

	// Check if we need to set expiration (for new keys)
	ttl, err := ttlCmd.Result()
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to get TTL: %w", err)
	}

	var resetTime time.Time

	// If TTL is -1, this is a new key without expiration
	if ttl == -1 {
		err = r.client.Expire(ctx, key, window).Err()
		if err != nil {
			r.logger.Warn("Failed to set expiration", zap.String("key", key), zap.Error(err))
			// Don't fail the request, just log the warning
		}
		resetTime = time.Now().Add(window)
	} else {
		// Calculate reset time based on current TTL
		resetTime = time.Now().Add(ttl)
	}

	r.logger.Debug("Rate limit counter incremented",
		zap.String("key", key),
		zap.Int64("count", count),
		zap.Time("reset_time", resetTime),
	)

	return int(count), resetTime, nil
}

// Get retrieves the current count and reset time for a key
func (r *RedisCacheStore) Get(ctx context.Context, key string) (int, time.Time, error) {
	// Use pipeline to get both value and TTL atomically
	pipe := r.client.Pipeline()

	getCmd := pipe.Get(ctx, key)
	ttlCmd := pipe.TTL(ctx, key)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to execute Redis pipeline: %w", err)
	}

	// Get the count
	countStr, err := getCmd.Result()
	if err != nil {
		if err == redis.Nil {
			return 0, time.Time{}, ErrCacheKeyNotFound
		}
		return 0, time.Time{}, fmt.Errorf("failed to get value: %w", err)
	}

	count, err := strconv.Atoi(countStr)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to parse count: %w", err)
	}

	// Get the TTL
	ttl, err := ttlCmd.Result()
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to get TTL: %w", err)
	}

	var resetTime time.Time
	if ttl > 0 {
		resetTime = time.Now().Add(ttl)
	} else {
		// Key exists but has no expiration, or is about to expire
		resetTime = time.Now().Add(time.Minute) // Default fallback
	}

	return count, resetTime, nil
}

// Set sets a value with expiration
func (r *RedisCacheStore) Set(ctx context.Context, key string, value int, window time.Duration) error {
	err := r.client.Set(ctx, key, value, window).Err()
	if err != nil {
		return fmt.Errorf("failed to set key: %w", err)
	}

	r.logger.Debug("Rate limit counter set",
		zap.String("key", key),
		zap.Int("value", value),
		zap.Duration("window", window),
	)

	return nil
}

// Delete removes a key from the cache
func (r *RedisCacheStore) Delete(ctx context.Context, key string) error {
	result := r.client.Del(ctx, key)

	err := result.Err()
	if err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}

	deleted := result.Val()
	if deleted == 0 {
		return ErrCacheKeyNotFound
	}

	r.logger.Debug("Rate limit counter deleted", zap.String("key", key))
	return nil
}

// IncrementWithLua uses a Lua script for more advanced rate limiting with sliding window
func (r *RedisCacheStore) IncrementWithLua(ctx context.Context, key string, window time.Duration, maxRequests int) (int, time.Time, bool, error) {
	// Lua script for sliding window rate limiting
	script := `
		local key = KEYS[1]
		local window = tonumber(ARGV[1])
		local limit = tonumber(ARGV[2])
		local now = tonumber(ARGV[3])
		
		-- Remove expired entries
		redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
		
		-- Count current requests
		local current = redis.call('ZCARD', key)
		
		if current < limit then
			-- Add current request
			redis.call('ZADD', key, now, now)
			redis.call('EXPIRE', key, window)
			return {current + 1, 1} -- {count, allowed}
		else
			return {current, 0} -- {count, not allowed}
		end
	`

	now := time.Now().Unix()
	windowSeconds := int64(window.Seconds())

	result, err := r.client.Eval(ctx, script, []string{key}, windowSeconds, maxRequests, now).Result()
	if err != nil {
		return 0, time.Time{}, false, fmt.Errorf("failed to execute Lua script: %w", err)
	}

	// Parse result
	resultArray, ok := result.([]interface{})
	if !ok || len(resultArray) != 2 {
		return 0, time.Time{}, false, fmt.Errorf("unexpected Lua script result format")
	}

	count, ok := resultArray[0].(int64)
	if !ok {
		return 0, time.Time{}, false, fmt.Errorf("failed to parse count from Lua result")
	}

	allowed, ok := resultArray[1].(int64)
	if !ok {
		return 0, time.Time{}, false, fmt.Errorf("failed to parse allowed from Lua result")
	}

	resetTime := time.Now().Add(window)

	return int(count), resetTime, allowed == 1, nil
}

// CleanExpiredKeys removes expired rate limit keys (for maintenance)
func (r *RedisCacheStore) CleanExpiredKeys(ctx context.Context, pattern string) error {
	// Scan for keys matching the pattern
	iter := r.client.Scan(ctx, 0, pattern, 1000).Iterator()

	var keysToCheck []string
	for iter.Next(ctx) {
		keysToCheck = append(keysToCheck, iter.Val())
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan keys: %w", err)
	}

	// Check TTL for each key and remove if expired
	if len(keysToCheck) > 0 {
		pipe := r.client.Pipeline()

		for _, key := range keysToCheck {
			pipe.TTL(ctx, key)
		}

		results, err := pipe.Exec(ctx)
		if err != nil {
			return fmt.Errorf("failed to check TTLs: %w", err)
		}

		var expiredKeys []string
		for i, result := range results {
			ttlCmd, ok := result.(*redis.DurationCmd)
			if !ok {
				continue
			}

			ttl, err := ttlCmd.Result()
			if err != nil {
				continue
			}

			// If TTL is -2, key doesn't exist
			// If TTL is -1, key exists but has no expiration (shouldn't happen for rate limits)
			if ttl == -2*time.Second {
				expiredKeys = append(expiredKeys, keysToCheck[i])
			}
		}

		// Delete expired keys in batches
		if len(expiredKeys) > 0 {
			err := r.client.Del(ctx, expiredKeys...).Err()
			if err != nil {
				r.logger.Warn("Failed to delete expired keys", zap.Error(err))
			} else {
				r.logger.Info("Cleaned up expired rate limit keys", zap.Int("count", len(expiredKeys)))
			}
		}
	}

	return nil
}

// GetStats returns statistics about the Redis cache
func (r *RedisCacheStore) GetStats(ctx context.Context) (map[string]interface{}, error) {
	info, err := r.client.Info(ctx, "memory", "stats").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get Redis info: %w", err)
	}

	// Parse relevant stats from info string
	stats := map[string]interface{}{
		"redis_info": info,
		"timestamp":  time.Now(),
	}

	// Add key count for rate limit keys
	keyCount, err := r.client.Eval(ctx, `
		local keys = redis.call('KEYS', 'usage:*')
		return #keys
	`, []string{}).Result()

	if err == nil {
		stats["rate_limit_keys"] = keyCount
	}

	return stats, nil
}

// HealthCheck verifies Redis connectivity
func (r *RedisCacheStore) HealthCheck(ctx context.Context) error {
	// Simple ping to verify connectivity
	err := r.client.Ping(ctx).Err()
	if err != nil {
		return fmt.Errorf("redis health check failed: %w", err)
	}

	// Test basic operations
	testKey := "healthcheck:ratelimit:" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// Set a test value
	err = r.client.Set(ctx, testKey, "test", time.Second).Err()
	if err != nil {
		return fmt.Errorf("redis set operation failed: %w", err)
	}

	// Get the test value
	val, err := r.client.Get(ctx, testKey).Result()
	if err != nil {
		return fmt.Errorf("redis get operation failed: %w", err)
	}

	if val != "test" {
		return fmt.Errorf("redis returned unexpected value: %s", val)
	}

	// Clean up test key
	r.client.Del(ctx, testKey)

	return nil
}
