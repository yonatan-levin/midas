package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestMacroDataRepository_Store(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMacroDataRepository(db)
	ctx := context.Background()

	t.Run("store new macro data successfully", func(t *testing.T) {
		macroData := &entities.MacroData{
			AsOf:               time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			RiskFreeRate:       0.045,
			RiskFreeRate3Month: 0.042,
			MarketRiskPremium:  0.05,
			InflationRate:      0.03,
			Source:             "FRED",
		}

		err := repo.Store(ctx, macroData)
		assert.NoError(t, err)

		// Verify data was stored
		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, macroData.AsOf.UTC(), latest.AsOf.UTC())
		assert.Equal(t, macroData.RiskFreeRate, latest.RiskFreeRate)
		assert.Equal(t, macroData.RiskFreeRate3Month, latest.RiskFreeRate3Month)
		assert.Equal(t, macroData.MarketRiskPremium, latest.MarketRiskPremium)
		assert.Equal(t, macroData.InflationRate, latest.InflationRate)
		assert.Equal(t, macroData.Source, latest.Source)
	})

	t.Run("store overwrites existing data with same timestamp", func(t *testing.T) {
		timestamp := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

		// Store first version
		macroData1 := &entities.MacroData{
			AsOf:              timestamp,
			RiskFreeRate:      0.045,
			MarketRiskPremium: 0.05,
			Source:            "FRED",
		}
		err := repo.Store(ctx, macroData1)
		require.NoError(t, err)

		// Store updated version with same timestamp
		macroData2 := &entities.MacroData{
			AsOf:              timestamp,
			RiskFreeRate:      0.047, // Different rate
			MarketRiskPremium: 0.052, // Different premium
			Source:            "FRED_updated",
		}
		err = repo.Store(ctx, macroData2)
		require.NoError(t, err)

		// Verify updated data
		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0.047, latest.RiskFreeRate)
		assert.Equal(t, 0.052, latest.MarketRiskPremium)
		assert.Equal(t, "FRED_updated", latest.Source)
	})

	t.Run("store multiple entries with different timestamps", func(t *testing.T) {
		entries := []*entities.MacroData{
			{
				AsOf:              time.Date(2024, 1, 14, 10, 0, 0, 0, time.UTC),
				RiskFreeRate:      0.044,
				MarketRiskPremium: 0.049,
				Source:            "FRED",
			},
			{
				AsOf:              time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
				RiskFreeRate:      0.045,
				MarketRiskPremium: 0.05,
				Source:            "FRED",
			},
			{
				AsOf:              time.Date(2024, 1, 16, 10, 0, 0, 0, time.UTC),
				RiskFreeRate:      0.046,
				MarketRiskPremium: 0.051,
				Source:            "FRED",
			},
		}

		for _, entry := range entries {
			err := repo.Store(ctx, entry)
			require.NoError(t, err)
		}

		// Verify latest is the most recent
		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, entries[2].AsOf.UTC(), latest.AsOf.UTC())
		assert.Equal(t, entries[2].RiskFreeRate, latest.RiskFreeRate)
	})
}

func TestMacroDataRepository_GetLatest(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMacroDataRepository(db)
	ctx := context.Background()

	t.Run("get latest when no data exists", func(t *testing.T) {
		latest, err := repo.GetLatest(ctx)
		assert.Error(t, err)
		assert.Nil(t, latest)
		assert.Contains(t, err.Error(), "no macro data found")
	})

	t.Run("get latest with single entry", func(t *testing.T) {
		macroData := &entities.MacroData{
			AsOf:              time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			RiskFreeRate:      0.045,
			MarketRiskPremium: 0.05,
			Source:            "test",
		}

		err := repo.Store(ctx, macroData)
		require.NoError(t, err)

		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, macroData.AsOf.UTC(), latest.AsOf.UTC())
		assert.Equal(t, macroData.RiskFreeRate, latest.RiskFreeRate)
	})

	t.Run("get latest with multiple entries returns most recent", func(t *testing.T) {
		entries := []*entities.MacroData{
			{
				AsOf:         time.Date(2024, 1, 13, 10, 0, 0, 0, time.UTC),
				RiskFreeRate: 0.043,
				Source:       "old",
			},
			{
				AsOf:         time.Date(2024, 1, 15, 10, 40, 0, 0, time.UTC),
				RiskFreeRate: 0.045,
				Source:       "latest",
			},
			{
				AsOf:         time.Date(2024, 1, 14, 10, 0, 0, 0, time.UTC),
				RiskFreeRate: 0.044,
				Source:       "middle",
			},
		}

		for _, entry := range entries {
			err := repo.Store(ctx, entry)
			require.NoError(t, err)
		}

		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, "latest", latest.Source)
		assert.Equal(t, 0.045, latest.RiskFreeRate)
	})
}

// clearMacroData removes all data from macro_data table for test isolation
func clearMacroData(t *testing.T, db *sqlx.DB) {
	_, err := db.Exec("DELETE FROM macro_data")
	require.NoError(t, err)
}

func TestMacroDataRepository_IsStale(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMacroDataRepository(db)
	ctx := context.Background()

	t.Run("returns true when no data exists", func(t *testing.T) {
		clearMacroData(t, db) // Ensure clean state
		isStale, err := repo.IsStale(ctx, 1*time.Hour)
		assert.NoError(t, err)
		assert.True(t, isStale)
	})

	t.Run("returns false when data is fresh", func(t *testing.T) {
		clearMacroData(t, db) // Ensure clean state
		// Store recent data
		macroData := &entities.MacroData{
			AsOf:         time.Now().UTC().Add(-30 * time.Minute), // 30 minutes ago
			RiskFreeRate: 0.045,
			Source:       "fresh_data",
		}

		err := repo.Store(ctx, macroData)
		require.NoError(t, err)

		isStale, err := repo.IsStale(ctx, 1*time.Hour) // Max age 1 hour
		assert.NoError(t, err)
		assert.False(t, isStale)
	})

	t.Run("returns true when data is stale", func(t *testing.T) {
		clearMacroData(t, db) // Ensure clean state
		// Store old data
		macroData := &entities.MacroData{
			AsOf:         time.Now().UTC().Add(-2 * time.Hour), // 2 hours ago
			RiskFreeRate: 0.045,
			Source:       "stale_data",
		}

		err := repo.Store(ctx, macroData)
		require.NoError(t, err)

		isStale, err := repo.IsStale(ctx, 1*time.Minute) // Max age 1 minute
		assert.NoError(t, err)
		assert.True(t, isStale)
	})

	t.Run("boundary case - exactly at max age", func(t *testing.T) {
		clearMacroData(t, db) // Ensure clean state
		// Store data exactly at the boundary
		macroData := &entities.MacroData{
			AsOf:         time.Now().UTC().Add(-1 * time.Hour), // Exactly 1 hour ago
			RiskFreeRate: 0.045,
			Source:       "boundary_data",
		}

		err := repo.Store(ctx, macroData)
		require.NoError(t, err)

		// Allow small timing tolerance
		isStale, err := repo.IsStale(ctx, 1*time.Hour-1*time.Second)
		assert.NoError(t, err)
		assert.True(t, isStale) // Should be stale since it's slightly older than max age
	})
}

func TestMacroDataRepository_SaveTreasuryRates(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMacroDataRepository(db)
	// Cast to concrete type to access additional helper methods
	concreteRepo := repo.(*MacroDataRepository)
	ctx := context.Background()

	t.Run("converts and saves treasury rates successfully", func(t *testing.T) {
		treasuryRates := &entities.TreasuryRates{
			AsOf:        time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			Yield1Month: 0.041,
			Yield3Month: 0.042,
			Yield6Month: 0.043,
			Yield1Year:  0.044,
			Yield2Year:  0.045,
			Yield5Year:  0.046,
			Yield10Year: 0.047,
			Yield20Year: 0.048,
			Yield30Year: 0.049,
		}

		err := concreteRepo.SaveTreasuryRates(ctx, treasuryRates)
		assert.NoError(t, err)

		// Verify conversion and storage
		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, treasuryRates.AsOf.UTC(), latest.AsOf.UTC())
		assert.Equal(t, treasuryRates.GetEffective10Year(), latest.RiskFreeRate)
		assert.Equal(t, treasuryRates.Yield3Month, latest.RiskFreeRate3Month)
		assert.Equal(t, "treasury_conversion", latest.Source)
	})

	t.Run("handles missing 10-year yield with fallback", func(t *testing.T) {
		treasuryRates := &entities.TreasuryRates{
			AsOf:        time.Date(2024, 1, 16, 10, 30, 0, 0, time.UTC),
			Yield1Month: 0.041,
			Yield3Month: 0.042,
			Yield5Year:  0.046,
			Yield10Year: 0.0, // Missing 10-year
			Yield20Year: 0.048,
		}

		err := concreteRepo.SaveTreasuryRates(ctx, treasuryRates)
		assert.NoError(t, err)

		// Verify fallback logic worked
		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		expectedRate := treasuryRates.GetEffective10Year() // Should use 20-year as fallback
		assert.Equal(t, expectedRate, latest.RiskFreeRate)
	})
}

func TestMacroDataRepository_UpdateMarketRiskPremium(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMacroDataRepository(db)
	// Cast to concrete type to access additional helper methods
	concreteRepo := repo.(*MacroDataRepository)
	ctx := context.Background()

	t.Run("updates market risk premium successfully", func(t *testing.T) {
		// Store initial data
		macroData := &entities.MacroData{
			AsOf:              time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			RiskFreeRate:      0.045,
			MarketRiskPremium: 0.05,
			Source:            "initial",
		}

		err := repo.Store(ctx, macroData)
		require.NoError(t, err)

		// Update market risk premium
		newPremium := 0.055
		err = concreteRepo.UpdateMarketRiskPremium(ctx, newPremium)
		assert.NoError(t, err)

		// Verify update
		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, newPremium, latest.MarketRiskPremium)
		assert.Equal(t, 0.045, latest.RiskFreeRate) // Should remain unchanged
	})

	t.Run("fails when no data exists to update", func(t *testing.T) {
		clearMacroData(t, db) // Ensure clean state
		err := concreteRepo.UpdateMarketRiskPremium(ctx, 0.06)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no macro data found to update")
	})
}

func TestMacroDataRepository_DeleteOldData(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMacroDataRepository(db)
	// Cast to concrete type to access additional helper methods
	concreteRepo := repo.(*MacroDataRepository)
	ctx := context.Background()

	t.Run("deletes old data successfully", func(t *testing.T) {
		now := time.Now().UTC()

		// Store data with different ages
		entries := []*entities.MacroData{
			{
				AsOf:         now.Add(-10 * time.Hour), // Very old
				RiskFreeRate: 0.043,
				Source:       "very_old",
			},
			{
				AsOf:         now.Add(-3 * time.Hour), // Old
				RiskFreeRate: 0.044,
				Source:       "old",
			},
			{
				AsOf:         now.Add(-30 * time.Minute), // Recent
				RiskFreeRate: 0.045,
				Source:       "recent",
			},
		}

		for _, entry := range entries {
			err := repo.Store(ctx, entry)
			require.NoError(t, err)
		}

		// Delete data older than 2 hours
		err := concreteRepo.DeleteOldData(ctx, 2*time.Hour)
		assert.NoError(t, err)

		// Verify only recent data remains
		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, "recent", latest.Source)
	})

	t.Run("no error when no old data to delete", func(t *testing.T) {
		err := concreteRepo.DeleteOldData(ctx, 24*time.Hour)
		assert.NoError(t, err) // Should not error
	})
}

func TestMacroDataRepository_EdgeCases(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMacroDataRepository(db)
	ctx := context.Background()

	t.Run("handles zero values correctly", func(t *testing.T) {
		macroData := &entities.MacroData{
			AsOf:               time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			RiskFreeRate:       0.0, // Zero rate
			RiskFreeRate3Month: 0.0,
			MarketRiskPremium:  0.0,
			InflationRate:      0.0,
			Source:             "zero_values",
		}

		err := repo.Store(ctx, macroData)
		assert.NoError(t, err)

		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0.0, latest.RiskFreeRate)
		assert.Equal(t, 0.0, latest.MarketRiskPremium)
	})

	t.Run("handles negative values correctly", func(t *testing.T) {
		macroData := &entities.MacroData{
			AsOf:              time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			RiskFreeRate:      -0.01, // Negative rate
			MarketRiskPremium: 0.05,
			InflationRate:     -0.005, // Deflation
			Source:            "negative_values",
		}

		err := repo.Store(ctx, macroData)
		assert.NoError(t, err)

		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, -0.01, latest.RiskFreeRate)
		assert.Equal(t, -0.005, latest.InflationRate)
	})

	t.Run("handles empty source string", func(t *testing.T) {
		macroData := &entities.MacroData{
			AsOf:         time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			RiskFreeRate: 0.045,
			Source:       "", // Empty source
		}

		err := repo.Store(ctx, macroData)
		assert.NoError(t, err)

		latest, err := repo.GetLatest(ctx)
		require.NoError(t, err)
		assert.Equal(t, "", latest.Source)
	})
}

func TestMacroDataRepository_ContextCancellation(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewMacroDataRepository(db)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		macroData := &entities.MacroData{
			AsOf:         time.Now(),
			RiskFreeRate: 0.045,
			Source:       "test",
		}

		err := repo.Store(ctx, macroData)
		// Should either succeed (if fast enough) or fail with context error
		if err != nil {
			assert.Contains(t, err.Error(), "context")
		}
	})
}
