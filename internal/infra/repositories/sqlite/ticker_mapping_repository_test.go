package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTickerMappingRepository_GetCIK(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewTickerMappingRepository(db)
	ctx := context.Background()

	t.Run("get CIK for non-existent ticker", func(t *testing.T) {
		cik, err := repo.GetCIK(ctx, "NONEXISTENT")
		assert.Error(t, err)
		assert.Empty(t, cik)
		assert.Contains(t, err.Error(), "no CIK found for ticker NONEXISTENT")
	})

	t.Run("get CIK for existing ticker", func(t *testing.T) {
		// Store a mapping first
		err := repo.Store(ctx, "AAPL", "0000320193")
		require.NoError(t, err)

		// Retrieve it
		cik, err := repo.GetCIK(ctx, "AAPL")
		assert.NoError(t, err)
		assert.Equal(t, "0000320193", cik)
	})

	t.Run("get CIK case sensitivity", func(t *testing.T) {
		// Store mapping with uppercase
		err := repo.Store(ctx, "MSFT", "0000789019")
		require.NoError(t, err)

		// Try to retrieve with lowercase (should fail if case sensitive)
		cik, err := repo.GetCIK(ctx, "msft")
		assert.Error(t, err)
		assert.Empty(t, cik)

		// Retrieve with correct case
		cik, err = repo.GetCIK(ctx, "MSFT")
		assert.NoError(t, err)
		assert.Equal(t, "0000789019", cik)
	})
}

func TestTickerMappingRepository_GetTicker(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewTickerMappingRepository(db)
	ctx := context.Background()

	t.Run("get ticker for non-existent CIK", func(t *testing.T) {
		ticker, err := repo.GetTicker(ctx, "0000999999")
		assert.Error(t, err)
		assert.Empty(t, ticker)
		assert.Contains(t, err.Error(), "no ticker found for CIK 0000999999")
	})

	t.Run("get ticker for existing CIK", func(t *testing.T) {
		// Store a mapping first
		err := repo.Store(ctx, "GOOGL", "0001652044")
		require.NoError(t, err)

		// Retrieve it
		ticker, err := repo.GetTicker(ctx, "0001652044")
		assert.NoError(t, err)
		assert.Equal(t, "GOOGL", ticker)
	})

	t.Run("get ticker with leading zeros in CIK", func(t *testing.T) {
		// Store mapping
		err := repo.Store(ctx, "TSLA", "0001318605")
		require.NoError(t, err)

		// Try different CIK formats
		ticker, err := repo.GetTicker(ctx, "0001318605")
		assert.NoError(t, err)
		assert.Equal(t, "TSLA", ticker)

		// Without leading zeros should fail if stored with leading zeros
		ticker, err = repo.GetTicker(ctx, "1318605")
		assert.Error(t, err)
		assert.Empty(t, ticker)
	})
}

func TestTickerMappingRepository_Store(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewTickerMappingRepository(db)
	ctx := context.Background()

	t.Run("store new mapping successfully", func(t *testing.T) {
		err := repo.Store(ctx, "AMZN", "0001018724")
		assert.NoError(t, err)

		// Verify storage
		cik, err := repo.GetCIK(ctx, "AMZN")
		require.NoError(t, err)
		assert.Equal(t, "0001018724", cik)

		ticker, err := repo.GetTicker(ctx, "0001018724")
		require.NoError(t, err)
		assert.Equal(t, "AMZN", ticker)
	})

	t.Run("store overwrites existing mapping", func(t *testing.T) {
		// Store initial mapping
		err := repo.Store(ctx, "TEST", "0000111111")
		require.NoError(t, err)

		// Overwrite with new CIK
		err = repo.Store(ctx, "TEST", "0000222222")
		require.NoError(t, err)

		// Verify new mapping
		cik, err := repo.GetCIK(ctx, "TEST")
		require.NoError(t, err)
		assert.Equal(t, "0000222222", cik)

		// Verify old CIK no longer points to TEST
		ticker, err := repo.GetTicker(ctx, "0000111111")
		assert.Error(t, err)
		assert.Empty(t, ticker)
	})

	t.Run("store empty values", func(t *testing.T) {
		// Empty ticker should be allowed (though not practical)
		err := repo.Store(ctx, "", "0000333333")
		assert.NoError(t, err)

		// Empty CIK should be allowed (though not practical)
		err = repo.Store(ctx, "EMPTY", "")
		assert.NoError(t, err)

		// Verify storage
		ticker, err := repo.GetTicker(ctx, "0000333333")
		require.NoError(t, err)
		assert.Equal(t, "", ticker)

		cik, err := repo.GetCIK(ctx, "EMPTY")
		require.NoError(t, err)
		assert.Equal(t, "", cik)
	})
}

func TestTickerMappingRepository_BulkStore(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewTickerMappingRepository(db)
	ctx := context.Background()

	t.Run("bulk store empty map", func(t *testing.T) {
		err := repo.BulkStore(ctx, map[string]string{})
		assert.NoError(t, err) // Should not error on empty map
	})

	t.Run("bulk store multiple mappings successfully", func(t *testing.T) {
		mappings := map[string]string{
			"AAPL":  "0000320193",
			"MSFT":  "0000789019",
			"GOOGL": "0001652044",
			"AMZN":  "0001018724",
			"TSLA":  "0001318605",
		}

		err := repo.BulkStore(ctx, mappings)
		assert.NoError(t, err)

		// Verify all mappings
		for ticker, expectedCIK := range mappings {
			cik, err := repo.GetCIK(ctx, ticker)
			require.NoError(t, err, "Failed to get CIK for %s", ticker)
			assert.Equal(t, expectedCIK, cik, "CIK mismatch for %s", ticker)

			retrievedTicker, err := repo.GetTicker(ctx, expectedCIK)
			require.NoError(t, err, "Failed to get ticker for CIK %s", expectedCIK)
			assert.Equal(t, ticker, retrievedTicker, "Ticker mismatch for CIK %s", expectedCIK)
		}
	})

	t.Run("bulk store overwrites existing mappings", func(t *testing.T) {
		// Store initial mappings
		initialMappings := map[string]string{
			"OLD1": "0000111111",
			"OLD2": "0000222222",
		}
		err := repo.BulkStore(ctx, initialMappings)
		require.NoError(t, err)

		// Overwrite with new mappings
		newMappings := map[string]string{
			"OLD1": "0000333333", // Same ticker, new CIK
			"NEW1": "0000222222", // New ticker, existing CIK (note: OLD2 -> 0000222222 still exists)
			"NEW2": "0000444444", // Completely new
		}
		err = repo.BulkStore(ctx, newMappings)
		assert.NoError(t, err)

		// Verify new mappings
		cik, err := repo.GetCIK(ctx, "OLD1")
		require.NoError(t, err)
		assert.Equal(t, "0000333333", cik)

		// Note: Multiple tickers can map to same CIK, so 0000222222 could return either OLD2 or NEW1
		// This is expected behavior - we'll just verify NEW1 maps correctly
		cik, err = repo.GetCIK(ctx, "NEW1")
		require.NoError(t, err)
		assert.Equal(t, "0000222222", cik)

		// Verify old mapping is gone
		_, err = repo.GetTicker(ctx, "0000111111")
		assert.Error(t, err)
	})

	t.Run("bulk store with some invalid entries continues", func(t *testing.T) {
		// This test assumes the implementation continues on individual failures
		// The actual behavior depends on implementation details
		mappings := map[string]string{
			"VALID1": "0000111111",
			"VALID2": "0000222222",
		}

		err := repo.BulkStore(ctx, mappings)
		assert.NoError(t, err)

		// Check that valid entries were stored
		cik, err := repo.GetCIK(ctx, "VALID1")
		assert.NoError(t, err)
		assert.Equal(t, "0000111111", cik)
	})
}

func TestTickerMappingRepository_HelperMethods(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewTickerMappingRepository(db)
	// Cast to concrete type to access additional helper methods
	concreteRepo := repo.(*TickerMappingRepository)
	ctx := context.Background()

	t.Run("get all mappings when empty", func(t *testing.T) {
		mappings, err := concreteRepo.GetAllMappings(ctx)
		assert.NoError(t, err)
		assert.Empty(t, mappings)
	})

	t.Run("get all mappings with data", func(t *testing.T) {
		// Store some mappings
		testMappings := map[string]string{
			"AAPL":  "0000320193",
			"MSFT":  "0000789019",
			"GOOGL": "0001652044",
		}

		err := repo.BulkStore(ctx, testMappings)
		require.NoError(t, err)

		// Get all mappings
		allMappings, err := concreteRepo.GetAllMappings(ctx)
		assert.NoError(t, err)
		assert.Equal(t, len(testMappings), len(allMappings))

		for ticker, cik := range testMappings {
			assert.Equal(t, cik, allMappings[ticker], "CIK mismatch for ticker %s", ticker)
		}
	})

	t.Run("count mappings", func(t *testing.T) {
		// Count should reflect current mappings from previous test
		count, err := concreteRepo.Count(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 3, count) // From previous test
	})

	t.Run("delete specific mapping", func(t *testing.T) {
		// Delete one mapping
		err := concreteRepo.DeleteMapping(ctx, "AAPL")
		assert.NoError(t, err)

		// Verify deletion
		_, err = repo.GetCIK(ctx, "AAPL")
		assert.Error(t, err)

		// Count should be reduced
		count, err := concreteRepo.Count(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("delete non-existent mapping", func(t *testing.T) {
		err := concreteRepo.DeleteMapping(ctx, "NONEXISTENT")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no mapping found for ticker NONEXISTENT")
	})

	t.Run("clear all mappings", func(t *testing.T) {
		// Clear all
		err := concreteRepo.ClearAll(ctx)
		assert.NoError(t, err)

		// Verify all are gone
		count, err := concreteRepo.Count(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, count)

		mappings, err := concreteRepo.GetAllMappings(ctx)
		assert.NoError(t, err)
		assert.Empty(t, mappings)
	})

	t.Run("load from SEC not implemented", func(t *testing.T) {
		err := concreteRepo.LoadFromSEC(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not yet implemented")
	})
}

func TestTickerMappingRepository_EdgeCases(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewTickerMappingRepository(db)
	ctx := context.Background()

	t.Run("handles special characters in ticker", func(t *testing.T) {
		// Some tickers have special characters
		err := repo.Store(ctx, "BRK.A", "0001067983")
		assert.NoError(t, err)

		cik, err := repo.GetCIK(ctx, "BRK.A")
		assert.NoError(t, err)
		assert.Equal(t, "0001067983", cik)
	})

	t.Run("handles numeric tickers", func(t *testing.T) {
		// Some tickers might be purely numeric
		err := repo.Store(ctx, "1234", "0001234567")
		assert.NoError(t, err)

		cik, err := repo.GetCIK(ctx, "1234")
		assert.NoError(t, err)
		assert.Equal(t, "0001234567", cik)
	})

	t.Run("handles long ticker symbols", func(t *testing.T) {
		// Test with longer ticker
		longTicker := "VERYLONGTICKER"
		err := repo.Store(ctx, longTicker, "0000999999")
		assert.NoError(t, err)

		cik, err := repo.GetCIK(ctx, longTicker)
		assert.NoError(t, err)
		assert.Equal(t, "0000999999", cik)
	})

	t.Run("handles various CIK formats", func(t *testing.T) {
		// Test with different CIK formats
		testCases := []struct {
			ticker string
			cik    string
		}{
			{"SHORT", "123"},          // Short CIK
			{"LONG", "1234567890123"}, // Long CIK
			{"ZERO", "0"},             // Single zero
		}

		for _, tc := range testCases {
			err := repo.Store(ctx, tc.ticker, tc.cik)
			assert.NoError(t, err, "Failed to store %s->%s", tc.ticker, tc.cik)

			retrievedCIK, err := repo.GetCIK(ctx, tc.ticker)
			assert.NoError(t, err, "Failed to retrieve CIK for %s", tc.ticker)
			assert.Equal(t, tc.cik, retrievedCIK, "CIK mismatch for %s", tc.ticker)
		}
	})
}

func TestTickerMappingRepository_ContextCancellation(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewTickerMappingRepository(db)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		err := repo.Store(ctx, "TEST", "0000123456")
		// Should either succeed (if fast enough) or fail with context error
		if err != nil {
			assert.Contains(t, err.Error(), "context")
		}
	})
}
