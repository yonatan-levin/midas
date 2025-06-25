package cache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestData struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestMemoryCacheRepository_Set_Get(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	t.Run("set and get string value", func(t *testing.T) {
		key := "test_string"
		value := "hello world"

		err := repo.Set(ctx, key, value, 1*time.Hour)
		assert.NoError(t, err)

		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value, result)
	})

	t.Run("set and get struct value", func(t *testing.T) {
		key := "test_struct"
		value := TestData{Name: "test", Value: 42}

		err := repo.Set(ctx, key, value, 1*time.Hour)
		assert.NoError(t, err)

		var result TestData
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value, result)
	})

	t.Run("set and get with zero TTL (no expiration)", func(t *testing.T) {
		key := "test_no_ttl"
		value := "never expires"

		err := repo.Set(ctx, key, value, 0)
		assert.NoError(t, err)

		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value, result)
	})

	t.Run("get non-existent key", func(t *testing.T) {
		var result string
		err := repo.Get(ctx, "non_existent", &result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("overwrite existing key", func(t *testing.T) {
		key := "test_overwrite"
		value1 := "first value"
		value2 := "second value"

		err := repo.Set(ctx, key, value1, 1*time.Hour)
		assert.NoError(t, err)

		err = repo.Set(ctx, key, value2, 1*time.Hour)
		assert.NoError(t, err)

		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value2, result)
	})
}

func TestMemoryCacheRepository_TTL_Expiration(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	t.Run("item expires after TTL", func(t *testing.T) {
		key := "test_expiration"
		value := "expires soon"

		err := repo.Set(ctx, key, value, 100*time.Millisecond)
		assert.NoError(t, err)

		// Should exist immediately
		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value, result)

		// Wait for expiration
		time.Sleep(150 * time.Millisecond)

		// Should not exist after expiration
		err = repo.Get(ctx, key, &result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("zero TTL means no expiration", func(t *testing.T) {
		key := "test_no_expiration"
		value := "never expires"

		err := repo.Set(ctx, key, value, 0)
		assert.NoError(t, err)

		// Wait a bit
		time.Sleep(50 * time.Millisecond)

		// Should still exist
		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value, result)
	})
}

func TestMemoryCacheRepository_Delete(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	t.Run("delete existing key", func(t *testing.T) {
		key := "test_delete"
		value := "to be deleted"

		// Set the value
		err := repo.Set(ctx, key, value, 1*time.Hour)
		assert.NoError(t, err)

		// Verify it exists
		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)

		// Delete it
		err = repo.Delete(ctx, key)
		assert.NoError(t, err)

		// Verify it's gone
		err = repo.Get(ctx, key, &result)
		assert.Error(t, err)
	})

	t.Run("delete non-existent key", func(t *testing.T) {
		err := repo.Delete(ctx, "non_existent")
		assert.NoError(t, err) // Should not error when deleting non-existent key
	})
}

func TestMemoryCacheRepository_Exists(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	t.Run("check existing key", func(t *testing.T) {
		key := "test_exists"
		value := "exists"

		// Set the value
		err := repo.Set(ctx, key, value, 1*time.Hour)
		assert.NoError(t, err)

		// Check existence
		exists, err := repo.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("check non-existent key", func(t *testing.T) {
		exists, err := repo.Exists(ctx, "non_existent")
		assert.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("check expired key", func(t *testing.T) {
		key := "test_expired"
		value := "will expire"

		// Set with short TTL
		err := repo.Set(ctx, key, value, 50*time.Millisecond)
		assert.NoError(t, err)

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		// Should not exist
		exists, err := repo.Exists(ctx, key)
		assert.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestMemoryCacheRepository_SetNX(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	t.Run("set new key with SetNX", func(t *testing.T) {
		key := "test_setnx_new"
		value := "new value"

		success, err := repo.SetNX(ctx, key, value, 1*time.Hour)
		assert.NoError(t, err)
		assert.True(t, success)

		// Verify it was set
		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value, result)
	})

	t.Run("set existing key with SetNX should fail", func(t *testing.T) {
		key := "test_setnx_existing"
		value1 := "first value"
		value2 := "second value"

		// Set initial value
		err := repo.Set(ctx, key, value1, 1*time.Hour)
		assert.NoError(t, err)

		// Try to set with SetNX (should fail)
		success, err := repo.SetNX(ctx, key, value2, 1*time.Hour)
		assert.NoError(t, err)
		assert.False(t, success)

		// Verify original value is unchanged
		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value1, result)
	})

	t.Run("set expired key with SetNX should succeed", func(t *testing.T) {
		key := "test_setnx_expired"
		value1 := "first value"
		value2 := "second value"

		// Set with short TTL
		err := repo.Set(ctx, key, value1, 50*time.Millisecond)
		assert.NoError(t, err)

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		// SetNX should succeed since key expired
		success, err := repo.SetNX(ctx, key, value2, 1*time.Hour)
		assert.NoError(t, err)
		assert.True(t, success)

		// Verify new value was set
		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value2, result)
	})
}

func TestMemoryCacheRepository_GetKeys(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	// Set up test data
	testKeys := map[string]string{
		"user:123":    "user data 1",
		"user:456":    "user data 2",
		"session:abc": "session data 1",
		"session:def": "session data 2",
		"config:xyz":  "config data",
	}

	for key, value := range testKeys {
		err := repo.Set(ctx, key, value, 1*time.Hour)
		require.NoError(t, err)
	}

	t.Run("get keys with wildcard pattern", func(t *testing.T) {
		keys, err := repo.GetKeys(ctx, "user:*")
		assert.NoError(t, err)
		assert.Len(t, keys, 2)
		assert.Contains(t, keys, "user:123")
		assert.Contains(t, keys, "user:456")
	})

	t.Run("get keys with different pattern", func(t *testing.T) {
		keys, err := repo.GetKeys(ctx, "session:*")
		assert.NoError(t, err)
		assert.Len(t, keys, 2)
		assert.Contains(t, keys, "session:abc")
		assert.Contains(t, keys, "session:def")
	})

	t.Run("get all keys with *", func(t *testing.T) {
		keys, err := repo.GetKeys(ctx, "*")
		assert.NoError(t, err)
		assert.Len(t, keys, 5)
	})

	t.Run("get keys with exact match", func(t *testing.T) {
		keys, err := repo.GetKeys(ctx, "config:xyz")
		assert.NoError(t, err)
		assert.Len(t, keys, 1)
		assert.Contains(t, keys, "config:xyz")
	})

	t.Run("get keys with non-matching pattern", func(t *testing.T) {
		keys, err := repo.GetKeys(ctx, "nonexistent:*")
		assert.NoError(t, err)
		assert.Len(t, keys, 0)
	})
}

func TestMemoryCacheRepository_DeletePattern(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	// Set up test data
	testKeys := map[string]string{
		"user:123":    "user data 1",
		"user:456":    "user data 2",
		"session:abc": "session data 1",
		"session:def": "session data 2",
		"config:xyz":  "config data",
	}

	for key, value := range testKeys {
		err := repo.Set(ctx, key, value, 1*time.Hour)
		require.NoError(t, err)
	}

	t.Run("delete keys by pattern", func(t *testing.T) {
		// Delete all user keys
		err := repo.DeletePattern(ctx, "user:*")
		assert.NoError(t, err)

		// Verify user keys are gone
		keys, err := repo.GetKeys(ctx, "user:*")
		assert.NoError(t, err)
		assert.Len(t, keys, 0)

		// Verify other keys still exist
		keys, err = repo.GetKeys(ctx, "session:*")
		assert.NoError(t, err)
		assert.Len(t, keys, 2)

		keys, err = repo.GetKeys(ctx, "config:*")
		assert.NoError(t, err)
		assert.Len(t, keys, 1)
	})

	t.Run("delete pattern with no matches", func(t *testing.T) {
		err := repo.DeletePattern(ctx, "nonexistent:*")
		assert.NoError(t, err) // Should not error when no keys match
	})
}

func TestMemoryCacheRepository_Concurrency(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	t.Run("concurrent read/write operations", func(t *testing.T) {
		const numGoroutines = 100
		const numOperations = 10

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// Start multiple goroutines performing concurrent operations
		for i := 0; i < numGoroutines; i++ {
			go func(id int) {
				defer wg.Done()

				for j := 0; j < numOperations; j++ {
					key := fmt.Sprintf("concurrent:%d:%d", id, j)
					value := fmt.Sprintf("value_%d_%d", id, j)

					// Set value
					err := repo.Set(ctx, key, value, 1*time.Hour)
					assert.NoError(t, err)

					// Get value
					var result string
					err = repo.Get(ctx, key, &result)
					assert.NoError(t, err)
					assert.Equal(t, value, result)

					// Check existence
					exists, err := repo.Exists(ctx, key)
					assert.NoError(t, err)
					assert.True(t, exists)

					// Delete value
					err = repo.Delete(ctx, key)
					assert.NoError(t, err)
				}
			}(i)
		}

		wg.Wait()
	})

	t.Run("concurrent SetNX operations", func(t *testing.T) {
		const numGoroutines = 10
		key := "lock_key"
		value := "lock_value"

		var wg sync.WaitGroup
		var successCount int32
		var mu sync.Mutex

		wg.Add(numGoroutines)

		// Multiple goroutines try to acquire the same lock
		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()

				success, err := repo.SetNX(ctx, key, value, 1*time.Hour)
				assert.NoError(t, err)

				if success {
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			}()
		}

		wg.Wait()

		// Only one goroutine should have succeeded
		assert.Equal(t, int32(1), successCount)

		// Verify the lock exists
		exists, err := repo.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)
	})
}

func TestMemoryCacheRepository_Cleanup(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	t.Run("cleanup removes expired items", func(t *testing.T) {
		// Set items with different TTLs
		err := repo.Set(ctx, "short_ttl", "expires soon", 50*time.Millisecond)
		assert.NoError(t, err)

		err = repo.Set(ctx, "long_ttl", "expires later", 1*time.Hour)
		assert.NoError(t, err)

		err = repo.Set(ctx, "no_ttl", "never expires", 0)
		assert.NoError(t, err)

		// Wait for short TTL to expire
		time.Sleep(100 * time.Millisecond)

		// Get all keys to trigger cleanup
		keys, err := repo.GetKeys(ctx, "*")
		assert.NoError(t, err)

		// Should only have non-expired items
		assert.Len(t, keys, 2)
		assert.Contains(t, keys, "long_ttl")
		assert.Contains(t, keys, "no_ttl")
		assert.NotContains(t, keys, "short_ttl")
	})
}
