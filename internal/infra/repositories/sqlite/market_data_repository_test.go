package sqlite

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestMarketDataRepository_Store(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMarketDataRepository(db)
	ctx := context.Background()

	// Test data
	marketData := &entities.MarketData{
		Ticker:            "AAPL",
		AsOf:              time.Now(),
		SharePrice:        180.50,
		MarketCap:         2840000000000,
		SharesOutstanding: 15744231000,
		Beta:              1.25,
		Beta3Y:            1.20,
		AverageVolume:     75000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	t.Run("successful store", func(t *testing.T) {
		err := repo.Store(ctx, marketData)
		assert.NoError(t, err)
	})

	t.Run("store multiple entries for same ticker", func(t *testing.T) {
		// Should allow multiple entries for same ticker with different dates
		newData := &entities.MarketData{
			Ticker:            "AAPL",
			AsOf:              time.Now().Add(1 * time.Hour),
			SharePrice:        182.00,
			MarketCap:         2860000000000,
			SharesOutstanding: 15744231000,
			Beta:              1.26,
			Beta3Y:            1.21,
			AverageVolume:     76000000,
			Source:            "yfinance",
			DataQuality:       "good",
		}
		err := repo.Store(ctx, newData)
		assert.NoError(t, err)
	})

	t.Run("nil data should fail", func(t *testing.T) {
		err := repo.Store(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})
}

func TestMarketDataRepository_GetLatest(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMarketDataRepository(db)
	ctx := context.Background()

	// Store test data with different dates
	baseTime := time.Now()
	oldData := &entities.MarketData{
		Ticker:            "AAPL",
		AsOf:              baseTime.Add(-2 * time.Hour),
		SharePrice:        175.00,
		MarketCap:         2750000000000,
		SharesOutstanding: 15744231000,
		Beta:              1.20,
		Beta3Y:            1.15,
		AverageVolume:     70000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	latestData := &entities.MarketData{
		Ticker:            "AAPL",
		AsOf:              baseTime,
		SharePrice:        180.50,
		MarketCap:         2840000000000,
		SharesOutstanding: 15744231000,
		Beta:              1.25,
		Beta3Y:            1.20,
		AverageVolume:     75000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	err := repo.Store(ctx, oldData)
	require.NoError(t, err)
	err = repo.Store(ctx, latestData)
	require.NoError(t, err)

	t.Run("get latest existing data", func(t *testing.T) {
		result, err := repo.GetLatest(ctx, "AAPL")
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "AAPL", result.Ticker)
		assert.Equal(t, 180.50, result.SharePrice)
		assert.Equal(t, 1.25, result.Beta)
		assert.Equal(t, 1.20, result.Beta3Y)
		// Should return the latest entry (baseTime, not baseTime-2h)
		assert.True(t, result.AsOf.After(oldData.AsOf) || result.AsOf.Equal(latestData.AsOf))
	})

	t.Run("get non-existent ticker", func(t *testing.T) {
		result, err := repo.GetLatest(ctx, "NONEXISTENT")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no market data found")
		assert.Nil(t, result)
	})
}

func TestMarketDataRepository_GetBatch(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMarketDataRepository(db)
	ctx := context.Background()

	// Store test data for multiple tickers
	aaplData := &entities.MarketData{
		Ticker:            "AAPL",
		AsOf:              time.Now(),
		SharePrice:        180.50,
		MarketCap:         2840000000000,
		SharesOutstanding: 15744231000,
		Beta:              1.25,
		Beta3Y:            1.20,
		AverageVolume:     75000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	msftData := &entities.MarketData{
		Ticker:            "MSFT",
		AsOf:              time.Now(),
		SharePrice:        350.75,
		MarketCap:         2600000000000,
		SharesOutstanding: 7430000000,
		Beta:              0.95,
		Beta3Y:            0.92,
		AverageVolume:     45000000,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	err := repo.Store(ctx, aaplData)
	require.NoError(t, err)
	err = repo.Store(ctx, msftData)
	require.NoError(t, err)

	t.Run("get batch existing tickers", func(t *testing.T) {
		result, err := repo.GetBatch(ctx, []string{"AAPL", "MSFT"})
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result, 2)

		aaplResult := result["AAPL"]
		assert.NotNil(t, aaplResult)
		assert.Equal(t, "AAPL", aaplResult.Ticker)
		assert.Equal(t, 180.50, aaplResult.SharePrice)

		msftResult := result["MSFT"]
		assert.NotNil(t, msftResult)
		assert.Equal(t, "MSFT", msftResult.Ticker)
		assert.Equal(t, 350.75, msftResult.SharePrice)
	})

	t.Run("get batch with mix of existing and non-existing tickers", func(t *testing.T) {
		result, err := repo.GetBatch(ctx, []string{"AAPL", "NONEXISTENT"})
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result, 1) // Only AAPL should be returned

		aaplResult := result["AAPL"]
		assert.NotNil(t, aaplResult)
		assert.Equal(t, "AAPL", aaplResult.Ticker)
	})

	t.Run("get batch empty tickers", func(t *testing.T) {
		result, err := repo.GetBatch(ctx, []string{})
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result, 0)
	})
}

func TestMarketDataRepository_IsStale(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMarketDataRepository(db)
	ctx := context.Background()

	// Store recent data
	recentData := &entities.MarketData{
		Ticker:            "AAPL",
		AsOf:              time.Now().Add(-10 * time.Minute), // 10 minutes ago
		SharePrice:        180.50,
		MarketCap:         2840000000000,
		SharesOutstanding: 15744231000,
		Beta:              1.25,
		Beta3Y:            1.20,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	err := repo.Store(ctx, recentData)
	require.NoError(t, err)

	t.Run("recent data is not stale", func(t *testing.T) {
		isStale, err := repo.IsStale(ctx, "AAPL", 30*time.Minute)
		assert.NoError(t, err)
		assert.False(t, isStale)
	})

	t.Run("old data is stale", func(t *testing.T) {
		isStale, err := repo.IsStale(ctx, "AAPL", 5*time.Minute)
		assert.NoError(t, err)
		assert.True(t, isStale)
	})

	t.Run("non-existent ticker is stale", func(t *testing.T) {
		isStale, err := repo.IsStale(ctx, "NONEXISTENT", 30*time.Minute)
		assert.NoError(t, err)
		assert.True(t, isStale) // Should return true when no data exists
	})
}

func TestMarketDataRepository_GetLastUpdated(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMarketDataRepository(db)
	ctx := context.Background()

	// Store test data
	beforeStore := time.Now()
	marketData := &entities.MarketData{
		Ticker:            "AAPL",
		AsOf:              time.Now(),
		SharePrice:        180.50,
		MarketCap:         2840000000000,
		SharesOutstanding: 15744231000,
		Beta:              1.25,
		Beta3Y:            1.20,
		Source:            "yfinance",
		DataQuality:       "good",
	}

	err := repo.Store(ctx, marketData)
	require.NoError(t, err)
	afterStore := time.Now()

	t.Run("get last updated for existing ticker", func(t *testing.T) {
		lastUpdated, err := repo.GetLastUpdated(ctx, "AAPL")
		assert.NoError(t, err)

		// Truncate to seconds to match SQLite precision
		beforeStoreSeconds := beforeStore.Truncate(time.Second)
		afterStoreSeconds := afterStore.Truncate(time.Second)

		// Should be between beforeStore and afterStore (at second precision)
		assert.True(t, lastUpdated.After(beforeStoreSeconds) || lastUpdated.Equal(beforeStoreSeconds))
		assert.True(t, lastUpdated.Before(afterStoreSeconds) || lastUpdated.Equal(afterStoreSeconds))
	})

	t.Run("get last updated for non-existent ticker", func(t *testing.T) {
		lastUpdated, err := repo.GetLastUpdated(ctx, "NONEXISTENT")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no market data found")
		assert.True(t, lastUpdated.IsZero())
	})
}
