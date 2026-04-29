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

// TestFinancialDataRepository_MinorityInterestPreferredEquity_RoundTrip pins
// the M-1d-fix follow-through: minority_interest and preferred_equity must
// survive Store → GetLatest, Store → GetHistorical, and Store → GetByPeriod.
//
// Pre-fix, the SQLite schema did not carry the two columns and the INSERT/
// SELECT lists in storeWith / GetLatest / GetHistorical / GetByPeriod silently
// dropped both fields. Cached reads zeroed them, making the equity_bridge
// trace lie and per-share regress to pre-M-1d behavior on the warm-cache path.
func TestFinancialDataRepository_MinorityInterestPreferredEquity_RoundTrip(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	// BRK-A-shape values: meaningful MI, modest preferred.
	const wantMI = 250_000_000.0
	const wantPE = 75_000_000.0

	data := &entities.FinancialData{
		Ticker:                   "BRKA",
		CIK:                      "0001067983",
		FilingPeriod:             "2023Q4",
		FilingDate:               time.Date(2024, 2, 24, 0, 0, 0, 0, time.UTC),
		AsOf:                     time.Now(),
		OperatingIncome:          1000.0,
		Revenue:                  10000.0,
		MinorityInterest:         wantMI,
		PreferredEquity:          wantPE,
		SharesOutstanding:        100,
		DilutedSharesOutstanding: 100,
	}

	require.NoError(t, repo.Store(ctx, data))

	t.Run("GetLatest preserves both fields", func(t *testing.T) {
		got, err := repo.GetLatest(ctx, "BRKA")
		require.NoError(t, err)
		assert.Equal(t, wantMI, got.MinorityInterest, "minority_interest must survive Store→GetLatest")
		assert.Equal(t, wantPE, got.PreferredEquity, "preferred_equity must survive Store→GetLatest")
	})

	t.Run("GetByPeriod preserves both fields", func(t *testing.T) {
		got, err := repo.GetByPeriod(ctx, "BRKA", "2023Q4")
		require.NoError(t, err)
		assert.Equal(t, wantMI, got.MinorityInterest, "minority_interest must survive Store→GetByPeriod")
		assert.Equal(t, wantPE, got.PreferredEquity, "preferred_equity must survive Store→GetByPeriod")
	})

	t.Run("GetHistorical preserves both fields", func(t *testing.T) {
		hist, err := repo.GetHistorical(ctx, "BRKA", 5)
		require.NoError(t, err)
		require.NotNil(t, hist)
		require.Len(t, hist.Data, 1, "expected exactly one period in the historical map")
		period, ok := hist.Data["2023Q4"]
		require.True(t, ok, "2023Q4 missing from GetHistorical result")
		assert.Equal(t, wantMI, period.MinorityInterest, "minority_interest must survive Store→GetHistorical")
		assert.Equal(t, wantPE, period.PreferredEquity, "preferred_equity must survive Store→GetHistorical")
	})
}

// TestFinancialDataRepository_ReportingCurrency_RoundTrip pins the IFRS-FPI
// Phase B5 contract: ReportingCurrency must survive Store → GetLatest /
// GetHistorical / GetByPeriod, and an unset field must default to "USD"
// rather than empty string so downstream FX conversion (Phase B9) can rely
// on a non-empty value.
//
// Two scenarios:
//   1. TSM-shape: ReportingCurrency="TWD" → must round-trip as "TWD".
//   2. Domestic-shape: ReportingCurrency="" → must round-trip as "USD"
//      (column NOT NULL DEFAULT 'USD' + storeWith short-circuit).
func TestFinancialDataRepository_ReportingCurrency_RoundTrip(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	t.Run("TSM_TWD_explicit", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:                   "TSM",
			CIK:                      "0001046179",
			FilingPeriod:             "2024FY",
			FilingDate:               time.Date(2025, 4, 17, 0, 0, 0, 0, time.UTC),
			AsOf:                     time.Now(),
			OperatingIncome:          1321714000000,
			Revenue:                  2894308000000,
			ReportingCurrency:        "TWD",
			SharesOutstanding:        25932733242,
			DilutedSharesOutstanding: 25932733242,
		}
		require.NoError(t, repo.Store(ctx, data))

		got, err := repo.GetLatest(ctx, "TSM")
		require.NoError(t, err)
		assert.Equal(t, "TWD", got.ReportingCurrency,
			"reporting_currency must survive Store→GetLatest")

		hist, err := repo.GetHistorical(ctx, "TSM", 5)
		require.NoError(t, err)
		require.Contains(t, hist.Data, "2024FY")
		assert.Equal(t, "TWD", hist.Data["2024FY"].ReportingCurrency,
			"reporting_currency must survive Store→GetHistorical")

		byPeriod, err := repo.GetByPeriod(ctx, "TSM", "2024FY")
		require.NoError(t, err)
		assert.Equal(t, "TWD", byPeriod.ReportingCurrency,
			"reporting_currency must survive Store→GetByPeriod")
	})

	t.Run("AAPL_unset_defaults_to_USD", func(t *testing.T) {
		data := &entities.FinancialData{
			Ticker:          "AAPL",
			CIK:             "0000320193",
			FilingPeriod:    "2024FY",
			FilingDate:      time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC),
			AsOf:            time.Now(),
			OperatingIncome: 114000000000,
			Revenue:         391000000000,
			// ReportingCurrency intentionally unset — domestic 10-K path.
			SharesOutstanding:        15400000000,
			DilutedSharesOutstanding: 15400000000,
		}
		require.NoError(t, repo.Store(ctx, data))

		got, err := repo.GetLatest(ctx, "AAPL")
		require.NoError(t, err)
		assert.Equal(t, "USD", got.ReportingCurrency,
			"unset reporting_currency must default to USD on read; got %q", got.ReportingCurrency)
		assert.NotEqual(t, "", got.ReportingCurrency,
			"empty string would break Phase B9 FX conversion lookups")
	})
}

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

// TestFinancialDataRepository_MissingFieldsJSON exercises the JSON unmarshal
// branch for missing_fields, which is skipped when data has no missing fields.
func TestFinancialDataRepository_MissingFieldsJSON(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	// Store data WITH non-empty missing fields
	data := &entities.FinancialData{
		Ticker:            "AAPL",
		CIK:               "0000320193",
		FilingPeriod:      "2023Q4",
		FilingDate:        time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		AsOf:              time.Now(),
		OperatingIncome:   123450000000,
		Revenue:           383930000000,
		SharesOutstanding: 15744231000,
		HasNormalizedData: true,
		MissingFields:     []string{"tax_rate", "interest_expense", "goodwill"},
	}
	err := repo.Store(ctx, data)
	require.NoError(t, err)

	// Also store a second period for GetHistorical
	data2 := &entities.FinancialData{
		Ticker:            "AAPL",
		CIK:               "0000320193",
		FilingPeriod:      "2022Q4",
		FilingDate:        time.Date(2023, 1, 15, 0, 0, 0, 0, time.UTC),
		AsOf:              time.Now(),
		Revenue:           300000000000,
		SharesOutstanding: 15744231000,
		MissingFields:     []string{"beta"},
	}
	err = repo.Store(ctx, data2)
	require.NoError(t, err)

	t.Run("GetLatest_unmarshals_missing_fields", func(t *testing.T) {
		result, err := repo.GetLatest(ctx, "AAPL")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"tax_rate", "interest_expense", "goodwill"}, result.MissingFields)
	})

	t.Run("GetHistorical_unmarshals_missing_fields", func(t *testing.T) {
		result, err := repo.GetHistorical(ctx, "AAPL", 10)
		require.NoError(t, err)
		assert.Contains(t, result.Data["2023Q4"].MissingFields, "tax_rate")
		assert.Contains(t, result.Data["2022Q4"].MissingFields, "beta")
	})

	t.Run("GetByPeriod_unmarshals_missing_fields", func(t *testing.T) {
		result, err := repo.GetByPeriod(ctx, "AAPL", "2023Q4")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"tax_rate", "interest_expense", "goodwill"}, result.MissingFields)
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
