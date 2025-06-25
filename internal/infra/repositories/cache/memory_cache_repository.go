package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// cacheItem represents an item in the memory cache
type cacheItem struct {
	Data      []byte
	ExpiresAt time.Time
}

// MemoryCacheRepository implements the CacheRepository interface using in-memory storage
type MemoryCacheRepository struct {
	mu    sync.RWMutex
	items map[string]*cacheItem
}

// NewMemoryCacheRepository creates a new memory cache repository
func NewMemoryCacheRepository() ports.CacheRepository {
	cache := &MemoryCacheRepository{
		items: make(map[string]*cacheItem),
	}

	// Start cleanup goroutine
	go cache.cleanup()

	return cache
}

// cleanup removes expired items periodically
func (r *MemoryCacheRepository) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		r.mu.Lock()
		now := time.Now()
		for key, item := range r.items {
			if now.After(item.ExpiresAt) {
				delete(r.items, key)
			}
		}
		r.mu.Unlock()
	}
}

// isExpired checks if an item has expired
func (r *MemoryCacheRepository) isExpired(item *cacheItem) bool {
	return time.Now().After(item.ExpiresAt)
}

// Set stores a value in cache with TTL
func (r *MemoryCacheRepository) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Calculate expiration time - if TTL is 0, set to far future (never expires)
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	} else {
		// Set to a far future date to effectively never expire
		expiresAt = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	}

	r.items[key] = &cacheItem{
		Data:      data,
		ExpiresAt: expiresAt,
	}

	return nil
}

// Get retrieves a value from cache
func (r *MemoryCacheRepository) Get(ctx context.Context, key string, dest interface{}) error {
	r.mu.RLock()
	item, exists := r.items[key]
	r.mu.RUnlock()

	if !exists {
		return fmt.Errorf("cache key not found: %s", key)
	}

	if r.isExpired(item) {
		// Remove expired item
		r.mu.Lock()
		delete(r.items, key)
		r.mu.Unlock()
		return fmt.Errorf("cache key not found: %s", key)
	}

	err := json.Unmarshal(item.Data, dest)
	if err != nil {
		return fmt.Errorf("failed to unmarshal cached value: %w", err)
	}

	return nil
}

// Delete removes a value from cache
func (r *MemoryCacheRepository) Delete(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.items, key)
	return nil
}

// Exists checks if a key exists in cache
func (r *MemoryCacheRepository) Exists(ctx context.Context, key string) (bool, error) {
	r.mu.RLock()
	item, exists := r.items[key]
	r.mu.RUnlock()

	if !exists {
		return false, nil
	}

	if r.isExpired(item) {
		// Remove expired item
		r.mu.Lock()
		delete(r.items, key)
		r.mu.Unlock()
		return false, nil
	}

	return true, nil
}

// SetNX sets a value only if key doesn't exist (for locking)
func (r *MemoryCacheRepository) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("failed to marshal value: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if key already exists and is not expired
	if item, exists := r.items[key]; exists && !r.isExpired(item) {
		return false, nil // Key already exists
	}

	// Set the new value
	r.items[key] = &cacheItem{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}

	return true, nil
}

// GetKeys returns all keys matching a pattern (simple glob-style pattern matching)
func (r *MemoryCacheRepository) GetKeys(ctx context.Context, pattern string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matchingKeys []string
	now := time.Now()

	for key, item := range r.items {
		// Skip expired items
		if now.After(item.ExpiresAt) {
			continue
		}

		// Simple pattern matching (supports * wildcard)
		if r.matchPattern(key, pattern) {
			matchingKeys = append(matchingKeys, key)
		}
	}

	return matchingKeys, nil
}

// matchPattern performs simple glob-style pattern matching
func (r *MemoryCacheRepository) matchPattern(key, pattern string) bool {
	// Use filepath.Match for glob-style pattern matching
	matched, err := filepath.Match(pattern, key)
	if err != nil {
		// Fallback to simple contains check if pattern is invalid
		return strings.Contains(key, strings.ReplaceAll(pattern, "*", ""))
	}
	return matched
}

// DeletePattern deletes all keys matching a pattern
func (r *MemoryCacheRepository) DeletePattern(ctx context.Context, pattern string) error {
	keys, err := r.GetKeys(ctx, pattern)
	if err != nil {
		return fmt.Errorf("failed to get keys for deletion: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, key := range keys {
		delete(r.items, key)
	}

	return nil
}
