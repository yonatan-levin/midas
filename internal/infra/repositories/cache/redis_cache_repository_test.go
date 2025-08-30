package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestRedisClient creates an in-memory Redis client for testing using miniredis
func setupTestRedisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis, func()) {
	// Create an in-memory Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err, "Failed to start miniredis")

	// Create Redis client connected to miniredis
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	// Cleanup function to close the server
	cleanup := func() {
		_ = client.Close()
		mr.Close()
	}

	return client, mr, cleanup
}

type TestStruct struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestRedisCacheRepository_Set_Get(t *testing.T) {
	client, mr, cleanup := setupTestRedisClient(t)
	defer cleanup()

	repo := NewRedisCacheRepository(client)
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
		value := TestStruct{Name: "test", Value: 42}

		err := repo.Set(ctx, key, value, 1*time.Hour)
		assert.NoError(t, err)

		var result TestStruct
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value, result)
	})

	t.Run("set with TTL expiration", func(t *testing.T) {
		key := "test_ttl"
		value := "expires soon"

		err := repo.Set(ctx, key, value, 100*time.Millisecond)
		assert.NoError(t, err)

		// Should exist immediately
		var result string
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, value, result)

		// Advance time in miniredis to trigger expiration
		mr.FastForward(150 * time.Millisecond)

		// Should not exist after expiration
		err = repo.Get(ctx, key, &result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("get non-existent key", func(t *testing.T) {
		var result string
		err := repo.Get(ctx, "non_existent", &result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestRedisCacheRepository_Delete(t *testing.T) {
	client, _, cleanup := setupTestRedisClient(t)
	defer cleanup()

	repo := NewRedisCacheRepository(client)
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

func TestRedisCacheRepository_Exists(t *testing.T) {
	client, _, cleanup := setupTestRedisClient(t)
	defer cleanup()

	repo := NewRedisCacheRepository(client)
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
}

func TestRedisCacheRepository_SetNX(t *testing.T) {
	client, _, cleanup := setupTestRedisClient(t)
	defer cleanup()

	repo := NewRedisCacheRepository(client)
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
}

func TestRedisCacheRepository_GetKeys(t *testing.T) {
	client, _, cleanup := setupTestRedisClient(t)
	defer cleanup()

	repo := NewRedisCacheRepository(client)
	ctx := context.Background()

	// Set up test data
	testKeys := []string{
		"user:123",
		"user:456",
		"session:abc",
		"session:def",
	}

	for _, key := range testKeys {
		err := repo.Set(ctx, key, "test_value", 1*time.Hour)
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

	t.Run("get keys with non-matching pattern", func(t *testing.T) {
		keys, err := repo.GetKeys(ctx, "nonexistent:*")
		assert.NoError(t, err)
		assert.Len(t, keys, 0)
	})
}

func TestRedisCacheRepository_DeletePattern(t *testing.T) {
	client, _, cleanup := setupTestRedisClient(t)
	defer cleanup()

	repo := NewRedisCacheRepository(client)
	ctx := context.Background()

	// Set up test data
	testKeys := []string{
		"user:123",
		"user:456",
		"session:abc",
		"session:def",
	}

	for _, key := range testKeys {
		err := repo.Set(ctx, key, "test_value", 1*time.Hour)
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

		// Verify session keys still exist
		keys, err = repo.GetKeys(ctx, "session:*")
		assert.NoError(t, err)
		assert.Len(t, keys, 2)
	})

	t.Run("delete pattern with no matches", func(t *testing.T) {
		err := repo.DeletePattern(ctx, "nonexistent:*")
		assert.NoError(t, err) // Should not error when no keys match
	})
}

func TestRedisCacheRepository_Integration(t *testing.T) {
	client, _, cleanup := setupTestRedisClient(t)
	defer cleanup()

	repo := NewRedisCacheRepository(client)
	ctx := context.Background()

	t.Run("complete workflow", func(t *testing.T) {
		// Store a complex object
		data := TestStruct{Name: "integration_test", Value: 999}
		key := "integration_test"

		// Set the data
		err := repo.Set(ctx, key, data, 1*time.Hour)
		assert.NoError(t, err)

		// Check if it exists
		exists, err := repo.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)

		// Get the data back
		var result TestStruct
		err = repo.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, data, result)

		// Use SetNX (should fail)
		success, err := repo.SetNX(ctx, key, TestStruct{Name: "new", Value: 0}, 1*time.Hour)
		assert.NoError(t, err)
		assert.False(t, success)

		// Delete the data
		err = repo.Delete(ctx, key)
		assert.NoError(t, err)

		// Verify it's gone
		exists, err = repo.Exists(ctx, key)
		assert.NoError(t, err)
		assert.False(t, exists)
	})
}
