package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func TestFinancialDataRepository_Store(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	// Test data
	financialData := &entities.FinancialData{
		Ticker:                    "AAPL",
		CIK:                       "0000320193",
		FilingPeriod:              "2023Q4",
		FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		AsOf:                      time.Now(),
		OperatingIncome:           123450000000,
		NormalizedOperatingIncome: 120000000000,
		Revenue:                   383930000000,
		InterestExpense:           3490000000,
		TaxRate:                   0.21,
		TotalAssets:               381190000000,
		TangibleAssets:            350000000000,
		Goodwill:                  0,
		OtherIntangibles:          31190000000,
		TotalDebt:                 0,
		InterestBearingDebt:       0,
		SharesOutstanding:         15744231000,
		DilutedSharesOutstanding:  15744231000,
		HasNormalizedData:         true,
	}

	t.Run("successful store", func(t *testing.T) {
		err := repo.Store(ctx, financialData)
		assert.NoError(t, err)
	})

	t.Run("duplicate ticker and period should fail", func(t *testing.T) {
		err := repo.Store(ctx, financialData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "UNIQUE constraint failed")
	})

	t.Run("nil data should fail", func(t *testing.T) {
		err := repo.Store(ctx, nil)
		assert.Error(t, err)
	})
}

func TestFinancialDataRepository_GetLatest(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	// Store test data
	financialData := &entities.FinancialData{
		Ticker:                    "AAPL",
		CIK:                       "0000320193",
		FilingPeriod:              "2023Q4",
		FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		AsOf:                      time.Now(),
		OperatingIncome:           123450000000,
		NormalizedOperatingIncome: 120000000000,
		Revenue:                   383930000000,
		SharesOutstanding:         15744231000,
		HasNormalizedData:         true,
	}

	err := repo.Store(ctx, financialData)
	require.NoError(t, err)

	t.Run("get existing data", func(t *testing.T) {
		result, err := repo.GetLatest(ctx, "AAPL")
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "AAPL", result.Ticker)
		assert.Equal(t, "2023Q4", result.FilingPeriod)
		assert.Equal(t, float64(123450000000), result.OperatingIncome)
	})

	t.Run("get non-existent ticker", func(t *testing.T) {
		result, err := repo.GetLatest(ctx, "NONEXISTENT")
		assert.Error(t, err)
		assert.True(t, err == sql.ErrNoRows || err.Error() == "no financial data found for ticker NONEXISTENT")
		assert.Nil(t, result)
	})
}

func TestFinancialDataRepository_GetHistorical(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	// Store multiple periods of data
	periods := []string{"2021Q4", "2022Q4", "2023Q4"}
	for i, period := range periods {
		financialData := &entities.FinancialData{
			Ticker:                    "AAPL",
			CIK:                       "0000320193",
			FilingPeriod:              period,
			FilingDate:                time.Date(2022+i, 1, 15, 0, 0, 0, 0, time.UTC),
			AsOf:                      time.Now(),
			OperatingIncome:           float64(100000000000 + i*10000000000),
			NormalizedOperatingIncome: float64(95000000000 + i*10000000000),
			Revenue:                   float64(300000000000 + i*50000000000),
			SharesOutstanding:         15744231000,
			HasNormalizedData:         true,
		}
		err := repo.Store(ctx, financialData)
		require.NoError(t, err)
	}

	t.Run("get historical data with limit", func(t *testing.T) {
		result, err := repo.GetHistorical(ctx, "AAPL", 2)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "AAPL", result.Ticker)
		assert.Len(t, result.Data, 2)

		// Should contain the expected periods
		assert.Contains(t, result.Data, "2023Q4")
		assert.Contains(t, result.Data, "2022Q4")
	})

	t.Run("get all historical data", func(t *testing.T) {
		result, err := repo.GetHistorical(ctx, "AAPL", 10)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Len(t, result.Data, 3)
	})

	t.Run("get historical for non-existent ticker", func(t *testing.T) {
		result, err := repo.GetHistorical(ctx, "NONEXISTENT", 5)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestFinancialDataRepository_GetByPeriod(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	// Store test data
	financialData := &entities.FinancialData{
		Ticker:                    "AAPL",
		CIK:                       "0000320193",
		FilingPeriod:              "2023Q4",
		FilingDate:                time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		AsOf:                      time.Now(),
		OperatingIncome:           123450000000,
		NormalizedOperatingIncome: 120000000000,
		Revenue:                   383930000000,
		SharesOutstanding:         15744231000,
		HasNormalizedData:         true,
	}

	err := repo.Store(ctx, financialData)
	require.NoError(t, err)

	t.Run("get specific period", func(t *testing.T) {
		result, err := repo.GetByPeriod(ctx, "AAPL", "2023Q4")
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "AAPL", result.Ticker)
		assert.Equal(t, "2023Q4", result.FilingPeriod)
	})

	t.Run("get non-existent period", func(t *testing.T) {
		result, err := repo.GetByPeriod(ctx, "AAPL", "2020Q1")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestFinancialDataRepository_GetLastUpdated(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	// Store test data
	financialData := &entities.FinancialData{
		Ticker:            "AAPL",
		CIK:               "0000320193",
		FilingPeriod:      "2023Q4",
		FilingDate:        time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		AsOf:              time.Now(),
		OperatingIncome:   123450000000,
		SharesOutstanding: 15744231000,
		HasNormalizedData: true,
	}

	err := repo.Store(ctx, financialData)
	require.NoError(t, err)

	t.Run("get last updated time", func(t *testing.T) {
		lastUpdated, err := repo.GetLastUpdated(ctx, "AAPL")
		assert.NoError(t, err)
		assert.WithinDuration(t, time.Now(), lastUpdated, 5*time.Second)
	})

	t.Run("get last updated for non-existent ticker", func(t *testing.T) {
		_, err := repo.GetLastUpdated(ctx, "NONEXISTENT")
		assert.Error(t, err)
	})
}

func TestFinancialDataRepository_StoreHistorical(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	// Create historical data
	periods := []*entities.FinancialData{
		{
			Ticker:            "AAPL",
			CIK:               "0000320193",
			FilingPeriod:      "2021Q4",
			FilingDate:        time.Date(2022, 1, 15, 0, 0, 0, 0, time.UTC),
			AsOf:              time.Now(),
			OperatingIncome:   110000000000,
			Revenue:           365000000000,
			SharesOutstanding: 16200000000,
			HasNormalizedData: true,
		},
		{
			Ticker:            "AAPL",
			CIK:               "0000320193",
			FilingPeriod:      "2022Q4",
			FilingDate:        time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
			AsOf:              time.Now(),
			OperatingIncome:   119000000000,
			Revenue:           394000000000,
			SharesOutstanding: 15900000000,
			HasNormalizedData: true,
		},
	}

	historicalData := &entities.HistoricalFinancialData{
		Ticker: "AAPL",
		Data: map[string]*entities.FinancialData{
			"2021Q4": periods[0],
			"2022Q4": periods[1],
		},
	}

	t.Run("store historical data", func(t *testing.T) {
		err := repo.StoreHistorical(ctx, historicalData)
		assert.NoError(t, err)

		// Verify data was stored
		result, err := repo.GetHistorical(ctx, "AAPL", 10)
		assert.NoError(t, err)
		assert.Len(t, result.Data, 2)
	})

	t.Run("store nil historical data should fail", func(t *testing.T) {
		err := repo.StoreHistorical(ctx, nil)
		assert.Error(t, err)
	})

	t.Run("store empty periods should fail", func(t *testing.T) {
		emptyData := &entities.HistoricalFinancialData{
			Ticker: "AAPL",
			Data:   map[string]*entities.FinancialData{},
		}
		err := repo.StoreHistorical(ctx, emptyData)
		assert.Error(t, err)
	})
}
