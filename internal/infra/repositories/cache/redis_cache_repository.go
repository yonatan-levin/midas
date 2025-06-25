package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// RedisCacheRepository implements the CacheRepository interface using Redis
type RedisCacheRepository struct {
	client *redis.Client
}

// NewRedisCacheRepository creates a new Redis cache repository
func NewRedisCacheRepository(client *redis.Client) ports.CacheRepository {
	return &RedisCacheRepository{
		client: client,
	}
}

// Set stores a value in cache with TTL
func (r *RedisCacheRepository) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	err = r.client.Set(ctx, key, data, ttl).Err()
	if err != nil {
		return fmt.Errorf("failed to set cache value: %w", err)
	}

	return nil
}

// Get retrieves a value from cache
func (r *RedisCacheRepository) Get(ctx context.Context, key string, dest interface{}) error {
	data, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("cache key not found: %s", key)
		}
		return fmt.Errorf("failed to get cache value: %w", err)
	}

	err = json.Unmarshal([]byte(data), dest)
	if err != nil {
		return fmt.Errorf("failed to unmarshal cached value: %w", err)
	}

	return nil
}

// Delete removes a value from cache
func (r *RedisCacheRepository) Delete(ctx context.Context, key string) error {
	err := r.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to delete cache key: %w", err)
	}

	return nil
}

// Exists checks if a key exists in cache
func (r *RedisCacheRepository) Exists(ctx context.Context, key string) (bool, error) {
	result, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check cache key existence: %w", err)
	}

	return result > 0, nil
}

// SetNX sets a value only if key doesn't exist (for locking)
func (r *RedisCacheRepository) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("failed to marshal value: %w", err)
	}

	result, err := r.client.SetNX(ctx, key, data, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("failed to set cache value with NX: %w", err)
	}

	return result, nil
}

// GetKeys returns all keys matching a pattern
func (r *RedisCacheRepository) GetKeys(ctx context.Context, pattern string) ([]string, error) {
	keys, err := r.client.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get keys by pattern: %w", err)
	}

	return keys, nil
}

// DeletePattern deletes all keys matching a pattern
func (r *RedisCacheRepository) DeletePattern(ctx context.Context, pattern string) error {
	keys, err := r.GetKeys(ctx, pattern)
	if err != nil {
		return fmt.Errorf("failed to get keys for deletion: %w", err)
	}

	if len(keys) == 0 {
		return nil // No keys to delete
	}

	err = r.client.Del(ctx, keys...).Err()
	if err != nil {
		return fmt.Errorf("failed to delete keys by pattern: %w", err)
	}

	return nil
}
