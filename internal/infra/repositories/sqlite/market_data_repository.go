package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// MarketDataRepository implements the MarketDataRepository interface for SQLite
type MarketDataRepository struct {
	db *sqlx.DB
}

// NewMarketDataRepository creates a new SQLite market data repository
func NewMarketDataRepository(db *sqlx.DB) ports.MarketDataRepository {
	return &MarketDataRepository{
		db: db,
	}
}

// Store stores market data for a company
func (r *MarketDataRepository) Store(ctx context.Context, data *entities.MarketData) error {
	if data == nil {
		return fmt.Errorf("market data cannot be nil")
	}

	query := `
		INSERT INTO market_data (
			ticker, as_of_date, share_price, market_cap, shares_outstanding,
			beta, beta_3_year, average_volume, source, data_quality,
			created_at, updated_at
		) VALUES (
			:ticker, :as_of_date, :share_price, :market_cap, :shares_outstanding,
			:beta, :beta_3_year, :average_volume, :source, :data_quality,
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`

	args := map[string]interface{}{
		"ticker":             data.Ticker,
		"as_of_date":         data.AsOf,
		"share_price":        data.SharePrice,
		"market_cap":         data.MarketCap,
		"shares_outstanding": data.SharesOutstanding,
		"beta":               data.Beta,
		"beta_3_year":        data.Beta3Y,
		"average_volume":     data.AverageVolume,
		"source":             data.Source,
		"data_quality":       data.DataQuality,
	}

	_, err := r.db.NamedExecContext(ctx, query, args)
	if err != nil {
		return fmt.Errorf("failed to store market data: %w", err)
	}

	return nil
}

// GetLatest retrieves the most recent market data for a ticker
func (r *MarketDataRepository) GetLatest(ctx context.Context, ticker string) (*entities.MarketData, error) {
	query := `
		SELECT 
			ticker, as_of_date, share_price, market_cap, shares_outstanding,
			beta, beta_3_year, average_volume, source, data_quality
		FROM market_data 
		WHERE ticker = ? 
		ORDER BY as_of_date DESC, created_at DESC
		LIMIT 1`

	var data entities.MarketData
	err := r.db.QueryRowxContext(ctx, query, ticker).Scan(
		&data.Ticker, &data.AsOf, &data.SharePrice, &data.MarketCap, &data.SharesOutstanding,
		&data.Beta, &data.Beta3Y, &data.AverageVolume, &data.Source, &data.DataQuality,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no market data found for ticker %s", ticker)
		}
		return nil, fmt.Errorf("failed to get latest market data: %w", err)
	}

	return &data, nil
}

// GetBatch retrieves market data for multiple tickers
func (r *MarketDataRepository) GetBatch(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	if len(tickers) == 0 {
		return make(map[string]*entities.MarketData), nil
	}

	// Build query with IN clause
	query := `
		SELECT DISTINCT 
			ticker, as_of_date, share_price, market_cap, shares_outstanding,
			beta, beta_3_year, average_volume, source, data_quality
		FROM market_data md1
		WHERE ticker IN (?` + strings.Repeat(",?", len(tickers)-1) + `)
		AND as_of_date = (
			SELECT MAX(as_of_date) 
			FROM market_data md2 
			WHERE md2.ticker = md1.ticker
		)`

	// Convert tickers to interface{} slice
	args := make([]interface{}, len(tickers))
	for i, ticker := range tickers {
		args[i] = ticker
	}

	rows, err := r.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query batch market data: %w", err)
	}
	defer rows.Close()

	result := make(map[string]*entities.MarketData)
	for rows.Next() {
		var data entities.MarketData
		err := rows.Scan(
			&data.Ticker, &data.AsOf, &data.SharePrice, &data.MarketCap, &data.SharesOutstanding,
			&data.Beta, &data.Beta3Y, &data.AverageVolume, &data.Source, &data.DataQuality,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan market data: %w", err)
		}

		result[data.Ticker] = &data
	}

	return result, nil
}

// IsStale checks if market data is stale for a ticker
func (r *MarketDataRepository) IsStale(ctx context.Context, ticker string, maxAge time.Duration) (bool, error) {
	latestData, err := r.GetLatest(ctx, ticker)
	if err != nil {
		// If no data exists, consider it stale
		return true, nil
	}

	// Check if the actual market data (as_of_date) is older than maxAge
	return time.Since(latestData.AsOf) > maxAge, nil
}

// GetLastUpdated returns when the data was last updated for a ticker
func (r *MarketDataRepository) GetLastUpdated(ctx context.Context, ticker string) (time.Time, error) {
	query := `
		SELECT updated_at 
		FROM market_data 
		WHERE ticker = ? 
		ORDER BY updated_at DESC 
		LIMIT 1`

	var updatedAt time.Time
	err := r.db.QueryRowxContext(ctx, query, ticker).Scan(&updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, fmt.Errorf("no market data found for ticker %s", ticker)
		}
		return time.Time{}, fmt.Errorf("failed to get last updated time: %w", err)
	}

	return updatedAt, nil
}
