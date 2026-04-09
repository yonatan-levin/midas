package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// WatchlistRepository implements the WatchlistRepository interface using SQLite
type WatchlistRepository struct {
	db *sql.DB
}

// NewWatchlistRepository creates a new SQLite-based watchlist repository
func NewWatchlistRepository(db *sql.DB) ports.WatchlistRepository {
	return &WatchlistRepository{db: db}
}

// GetActiveWatchlist retrieves all active tickers from the watchlist
func (r *WatchlistRepository) GetActiveWatchlist(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error) {
	if filter == nil {
		filter = &entities.WatchlistFilter{}
	}

	// Override is_active to true for this method
	isActive := true
	filter.IsActive = &isActive

	return r.GetAll(ctx, filter)
}

// GetAll retrieves all watchlist entries with optional filtering
func (r *WatchlistRepository) GetAll(ctx context.Context, filter *entities.WatchlistFilter) ([]*entities.WatchlistEntry, error) {
	query := `
		SELECT id, ticker, is_active, priority, added_reason, last_fetched_at, 
		       fetch_failures, max_failures, created_at, updated_at
		FROM scheduler_watchlist
		WHERE 1=1`

	args := []interface{}{}
	argIndex := 1

	// Apply filters
	if filter != nil {
		if filter.IsActive != nil {
			query += fmt.Sprintf(" AND is_active = ?%d", argIndex)
			args = append(args, *filter.IsActive)
			argIndex++
		}

		if filter.Priority != nil {
			query += fmt.Sprintf(" AND priority = ?%d", argIndex)
			args = append(args, int(*filter.Priority))
			argIndex++
		}

		if filter.MaxFailures != nil {
			query += fmt.Sprintf(" AND fetch_failures <= ?%d", argIndex)
			args = append(args, *filter.MaxFailures)
			argIndex++
		}
	}

	// Order by priority (high to low), then by ticker
	query += " ORDER BY priority ASC, ticker ASC"

	// Apply limit and offset
	if filter != nil {
		if filter.Limit > 0 {
			query += fmt.Sprintf(" LIMIT ?%d", argIndex)
			args = append(args, filter.Limit)
			argIndex++
		}

		if filter.Offset > 0 {
			query += fmt.Sprintf(" OFFSET ?%d", argIndex)
			args = append(args, filter.Offset)
		}
	}

	// Replace numbered placeholders with ? for SQLite
	for i := len(args); i >= 1; i-- {
		query = strings.Replace(query, fmt.Sprintf("?%d", i), "?", 1)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query watchlist: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []*entities.WatchlistEntry
	for rows.Next() {
		entry := &entities.WatchlistEntry{}
		var lastFetchedAt sql.NullTime

		err := rows.Scan(
			&entry.ID,
			&entry.Ticker,
			&entry.IsActive,
			&entry.Priority,
			&entry.AddedReason,
			&lastFetchedAt,
			&entry.FetchFailures,
			&entry.MaxFailures,
			&entry.CreatedAt,
			&entry.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan watchlist entry: %w", err)
		}

		if lastFetchedAt.Valid {
			entry.LastFetchedAt = &lastFetchedAt.Time
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// GetByTicker retrieves a watchlist entry by ticker symbol
func (r *WatchlistRepository) GetByTicker(ctx context.Context, ticker string) (*entities.WatchlistEntry, error) {
	query := `
		SELECT id, ticker, is_active, priority, added_reason, last_fetched_at, 
		       fetch_failures, max_failures, created_at, updated_at
		FROM scheduler_watchlist
		WHERE ticker = ?`

	entry := &entities.WatchlistEntry{}
	var lastFetchedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, ticker).Scan(
		&entry.ID,
		&entry.Ticker,
		&entry.IsActive,
		&entry.Priority,
		&entry.AddedReason,
		&lastFetchedAt,
		&entry.FetchFailures,
		&entry.MaxFailures,
		&entry.CreatedAt,
		&entry.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil // Not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get watchlist entry for %s: %w", ticker, err)
	}

	if lastFetchedAt.Valid {
		entry.LastFetchedAt = &lastFetchedAt.Time
	}

	return entry, nil
}

// Add adds a new ticker to the watchlist
func (r *WatchlistRepository) Add(ctx context.Context, entry *entities.WatchlistEntry) error {
	query := `
		INSERT INTO scheduler_watchlist (ticker, is_active, priority, added_reason, max_failures)
		VALUES (?, ?, ?, ?, ?)`

	// Set defaults if not provided
	if entry.Priority == 0 {
		entry.Priority = int(entities.MediumPriority)
	}
	if entry.MaxFailures == 0 {
		entry.MaxFailures = 5
	}
	if entry.AddedReason == "" {
		entry.AddedReason = "manual"
	}

	_, err := r.db.ExecContext(ctx, query,
		entry.Ticker,
		entry.IsActive,
		entry.Priority,
		entry.AddedReason,
		entry.MaxFailures,
	)

	if err != nil {
		return fmt.Errorf("failed to add watchlist entry for %s: %w", entry.Ticker, err)
	}

	return nil
}

// Update updates an existing watchlist entry
func (r *WatchlistRepository) Update(ctx context.Context, ticker string, updates *entities.UpdateWatchlistEntryRequest) error {
	setParts := []string{}
	args := []interface{}{}

	if updates.IsActive != nil {
		setParts = append(setParts, "is_active = ?")
		args = append(args, *updates.IsActive)
	}

	if updates.Priority != nil {
		setParts = append(setParts, "priority = ?")
		args = append(args, int(*updates.Priority))
	}

	if updates.MaxFailures != nil {
		setParts = append(setParts, "max_failures = ?")
		args = append(args, *updates.MaxFailures)
	}

	if len(setParts) == 0 {
		return nil // Nothing to update
	}

	// Always update the updated_at field
	setParts = append(setParts, "updated_at = CURRENT_TIMESTAMP")

	query := fmt.Sprintf(`
		UPDATE scheduler_watchlist 
		SET %s
		WHERE ticker = ?`,
		strings.Join(setParts, ", "))

	args = append(args, ticker)

	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update watchlist entry for %s: %w", ticker, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for %s: %w", ticker, err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("watchlist entry not found for ticker %s", ticker)
	}

	return nil
}

// Remove removes a ticker from the watchlist
func (r *WatchlistRepository) Remove(ctx context.Context, ticker string) error {
	query := `DELETE FROM scheduler_watchlist WHERE ticker = ?`

	result, err := r.db.ExecContext(ctx, query, ticker)
	if err != nil {
		return fmt.Errorf("failed to remove watchlist entry for %s: %w", ticker, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for %s: %w", ticker, err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("watchlist entry not found for ticker %s", ticker)
	}

	return nil
}

// RecordSuccess updates the last fetched time and resets failure count
func (r *WatchlistRepository) RecordSuccess(ctx context.Context, ticker string, fetchedAt time.Time) error {
	query := `
		UPDATE scheduler_watchlist 
		SET last_fetched_at = ?, fetch_failures = 0, updated_at = CURRENT_TIMESTAMP
		WHERE ticker = ?`

	result, err := r.db.ExecContext(ctx, query, fetchedAt, ticker)
	if err != nil {
		return fmt.Errorf("failed to record success for %s: %w", ticker, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for %s: %w", ticker, err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("watchlist entry not found for ticker %s", ticker)
	}

	return nil
}

// RecordFailure increments the failure count and optionally disables the entry
func (r *WatchlistRepository) RecordFailure(ctx context.Context, ticker string) error {
	query := `
		UPDATE scheduler_watchlist 
		SET fetch_failures = fetch_failures + 1,
		    is_active = CASE 
		        WHEN fetch_failures + 1 >= max_failures THEN 0 
		        ELSE is_active 
		    END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE ticker = ?`

	result, err := r.db.ExecContext(ctx, query, ticker)
	if err != nil {
		return fmt.Errorf("failed to record failure for %s: %w", ticker, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for %s: %w", ticker, err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("watchlist entry not found for ticker %s", ticker)
	}

	return nil
}

// GetStats retrieves statistics about the watchlist
func (r *WatchlistRepository) GetStats(ctx context.Context) (*entities.WatchlistStats, error) {
	// COALESCE prevents NULL when the table is empty (SUM over 0 rows = NULL in SQLite)
	query := `
		SELECT
		    COUNT(*) as total_entries,
		    COALESCE(SUM(CASE WHEN is_active = 1 THEN 1 ELSE 0 END), 0) as active_entries,
		    COALESCE(SUM(CASE WHEN is_active = 0 THEN 1 ELSE 0 END), 0) as inactive_entries,
		    COALESCE(SUM(CASE WHEN priority = 1 THEN 1 ELSE 0 END), 0) as high_priority,
		    COALESCE(SUM(CASE WHEN priority = 2 THEN 1 ELSE 0 END), 0) as medium_priority,
		    COALESCE(SUM(CASE WHEN priority = 3 THEN 1 ELSE 0 END), 0) as low_priority,
		    COALESCE(SUM(CASE WHEN fetch_failures > 0 AND updated_at > datetime('now', '-1 day') THEN 1 ELSE 0 END), 0) as recent_failures
		FROM scheduler_watchlist`

	stats := &entities.WatchlistStats{}
	err := r.db.QueryRowContext(ctx, query).Scan(
		&stats.TotalEntries,
		&stats.ActiveEntries,
		&stats.InactiveEntries,
		&stats.HighPriority,
		&stats.MediumPriority,
		&stats.LowPriority,
		&stats.RecentFailures,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get watchlist stats: %w", err)
	}

	return stats, nil
}

// BulkUpdateFailures updates failure counts for multiple tickers
func (r *WatchlistRepository) BulkUpdateFailures(ctx context.Context, failures map[string]bool) error {
	if len(failures) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	successQuery := `
		UPDATE scheduler_watchlist 
		SET last_fetched_at = CURRENT_TIMESTAMP, fetch_failures = 0, updated_at = CURRENT_TIMESTAMP
		WHERE ticker = ?`

	failureQuery := `
		UPDATE scheduler_watchlist 
		SET fetch_failures = fetch_failures + 1,
		    is_active = CASE 
		        WHEN fetch_failures + 1 >= max_failures THEN 0 
		        ELSE is_active 
		    END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE ticker = ?`

	for ticker, success := range failures {
		var query string
		if success {
			query = successQuery
		} else {
			query = failureQuery
		}

		_, err := tx.ExecContext(ctx, query, ticker)
		if err != nil {
			return fmt.Errorf("failed to update failure status for %s: %w", ticker, err)
		}
	}

	return tx.Commit()
}
