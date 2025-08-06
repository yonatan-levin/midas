package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// TickerMappingRepository implements the TickerMappingRepository interface for SQLite
type TickerMappingRepository struct {
	db     *sqlx.DB
	logger *zap.Logger
}

// NewTickerMappingRepository creates a new SQLite ticker mapping repository
func NewTickerMappingRepository(db *sqlx.DB) ports.TickerMappingRepository {
	return &TickerMappingRepository{
		db:     db,
		logger: zap.L().Named("ticker-mapping-repository"),
	}
}

// GetCIK retrieves the CIK for a ticker symbol
func (r *TickerMappingRepository) GetCIK(ctx context.Context, ticker string) (string, error) {
	r.logger.Debug("Getting CIK for ticker", zap.String("ticker", ticker))

	query := `SELECT cik FROM ticker_mapping WHERE ticker = ? LIMIT 1`

	var cik string
	err := r.db.GetContext(ctx, &cik, query, ticker)
	if err != nil {
		if err == sql.ErrNoRows {
			r.logger.Debug("No CIK found for ticker", zap.String("ticker", ticker))
			return "", fmt.Errorf("no CIK found for ticker %s", ticker)
		}
		r.logger.Error("Failed to get CIK for ticker",
			zap.String("ticker", ticker),
			zap.Error(err))
		return "", fmt.Errorf("failed to get CIK for ticker %s: %w", ticker, err)
	}

	r.logger.Debug("Successfully retrieved CIK",
		zap.String("ticker", ticker),
		zap.String("cik", cik))

	return cik, nil
}

// GetTicker retrieves the ticker for a CIK
func (r *TickerMappingRepository) GetTicker(ctx context.Context, cik string) (string, error) {
	r.logger.Debug("Getting ticker for CIK", zap.String("cik", cik))

	query := `SELECT ticker FROM ticker_mapping WHERE cik = ? LIMIT 1`

	var ticker string
	err := r.db.GetContext(ctx, &ticker, query, cik)
	if err != nil {
		if err == sql.ErrNoRows {
			r.logger.Debug("No ticker found for CIK", zap.String("cik", cik))
			return "", fmt.Errorf("no ticker found for CIK %s", cik)
		}
		r.logger.Error("Failed to get ticker for CIK",
			zap.String("cik", cik),
			zap.Error(err))
		return "", fmt.Errorf("failed to get ticker for CIK %s: %w", cik, err)
	}

	r.logger.Debug("Successfully retrieved ticker",
		zap.String("cik", cik),
		zap.String("ticker", ticker))

	return ticker, nil
}

// Store stores a ticker-to-CIK mapping
func (r *TickerMappingRepository) Store(ctx context.Context, ticker, cik string) error {
	r.logger.Debug("Storing ticker-CIK mapping",
		zap.String("ticker", ticker),
		zap.String("cik", cik))

	query := `
		INSERT OR REPLACE INTO ticker_mapping (
			ticker, cik, created_at, updated_at
		) VALUES (
			?, ?, datetime('now'), datetime('now')
		)`

	_, err := r.db.ExecContext(ctx, query, ticker, cik)
	if err != nil {
		r.logger.Error("Failed to store ticker-CIK mapping",
			zap.String("ticker", ticker),
			zap.String("cik", cik),
			zap.Error(err))
		return fmt.Errorf("failed to store mapping for %s->%s: %w", ticker, cik, err)
	}

	r.logger.Debug("Successfully stored ticker-CIK mapping")
	return nil
}

// BulkStore stores multiple ticker-to-CIK mappings
func (r *TickerMappingRepository) BulkStore(ctx context.Context, mappings map[string]string) error {
	if len(mappings) == 0 {
		r.logger.Debug("No mappings to store")
		return nil
	}

	r.logger.Info("Bulk storing ticker-CIK mappings",
		zap.Int("count", len(mappings)))

	// Start transaction for bulk operation
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Prepare statement for efficiency
	stmt, err := tx.PreparexContext(ctx, `
		INSERT OR REPLACE INTO ticker_mapping (
			ticker, cik, created_at, updated_at
		) VALUES (
			?, ?, datetime('now'), datetime('now')
		)`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	// Execute bulk insert
	count := 0
	for ticker, cik := range mappings {
		_, err := stmt.ExecContext(ctx, ticker, cik)
		if err != nil {
			r.logger.Error("Failed to insert mapping in bulk",
				zap.String("ticker", ticker),
				zap.String("cik", cik),
				zap.Error(err))
			// Continue with other mappings rather than failing completely
			continue
		}
		count++
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	r.logger.Info("Successfully bulk stored ticker-CIK mappings",
		zap.Int("stored", count),
		zap.Int("total", len(mappings)))

	return nil
}

// GetAllMappings retrieves all ticker-to-CIK mappings
func (r *TickerMappingRepository) GetAllMappings(ctx context.Context) (map[string]string, error) {
	r.logger.Debug("Fetching all ticker-CIK mappings")

	query := `SELECT ticker, cik FROM ticker_mapping ORDER BY ticker`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		r.logger.Error("Failed to query all mappings", zap.Error(err))
		return nil, fmt.Errorf("failed to query all mappings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	mappings := make(map[string]string)
	for rows.Next() {
		var ticker, cik string
		if err := rows.Scan(&ticker, &cik); err != nil {
			r.logger.Error("Failed to scan mapping row", zap.Error(err))
			continue
		}
		mappings[ticker] = cik
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating mapping rows", zap.Error(err))
		return nil, fmt.Errorf("error iterating mappings: %w", err)
	}

	r.logger.Debug("Successfully fetched all mappings",
		zap.Int("count", len(mappings)))

	return mappings, nil
}

// LoadFromSEC loads ticker mappings from SEC data
func (r *TickerMappingRepository) LoadFromSEC(ctx context.Context) error {
	r.logger.Info("Loading ticker mappings from SEC")

	// TODO: This should integrate with the SEC gateway to fetch latest mappings
	// For now, this is a placeholder that indicates the integration point
	r.logger.Warn("LoadFromSEC not yet implemented - requires SEC gateway integration")

	return fmt.Errorf("LoadFromSEC not yet implemented - requires SEC gateway integration")
}

// Additional helper methods

// Count returns the total number of ticker mappings
func (r *TickerMappingRepository) Count(ctx context.Context) (int, error) {
	r.logger.Debug("Counting ticker mappings")

	query := `SELECT COUNT(*) FROM ticker_mapping`

	var count int
	err := r.db.GetContext(ctx, &count, query)
	if err != nil {
		r.logger.Error("Failed to count mappings", zap.Error(err))
		return 0, fmt.Errorf("failed to count mappings: %w", err)
	}

	r.logger.Debug("Successfully counted mappings", zap.Int("count", count))
	return count, nil
}

// DeleteMapping removes a specific ticker-CIK mapping
func (r *TickerMappingRepository) DeleteMapping(ctx context.Context, ticker string) error {
	r.logger.Debug("Deleting ticker mapping", zap.String("ticker", ticker))

	query := `DELETE FROM ticker_mapping WHERE ticker = ?`

	result, err := r.db.ExecContext(ctx, query, ticker)
	if err != nil {
		r.logger.Error("Failed to delete mapping",
			zap.String("ticker", ticker),
			zap.Error(err))
		return fmt.Errorf("failed to delete mapping for %s: %w", ticker, err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		r.logger.Warn("No mapping found to delete", zap.String("ticker", ticker))
		return fmt.Errorf("no mapping found for ticker %s", ticker)
	}

	r.logger.Debug("Successfully deleted ticker mapping")
	return nil
}

// ClearAll removes all ticker mappings (useful for refresh operations)
func (r *TickerMappingRepository) ClearAll(ctx context.Context) error {
	r.logger.Info("Clearing all ticker mappings")

	query := `DELETE FROM ticker_mapping`

	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		r.logger.Error("Failed to clear all mappings", zap.Error(err))
		return fmt.Errorf("failed to clear all mappings: %w", err)
	}

	rowsDeleted, _ := result.RowsAffected()
	r.logger.Info("Successfully cleared all ticker mappings",
		zap.Int64("deleted", rowsDeleted))

	return nil
}
