package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// MacroDataRepository implements the MacroDataRepository interface for SQLite
type MacroDataRepository struct {
	db     *sqlx.DB
	logger *zap.Logger
}

// NewMacroDataRepository creates a new SQLite macro data repository
func NewMacroDataRepository(db *sqlx.DB) ports.MacroDataRepository {
	return &MacroDataRepository{
		db:     db,
		logger: zap.L().Named("macro-data-repository"),
	}
}

// Store stores macro-economic data
func (r *MacroDataRepository) Store(ctx context.Context, data *entities.MacroData) error {
	r.logger.Debug("Storing macro data",
		zap.Time("as_of", data.AsOf),
		zap.Float64("risk_free_rate", data.RiskFreeRate),
		zap.Float64("market_risk_premium", data.MarketRiskPremium))

	// First try to update existing record with same timestamp
	updateQuery := `
		UPDATE macro_data SET
			risk_free_rate = ?,
			risk_free_rate_3m = ?,
			market_risk_premium = ?,
			inflation_rate = ?,
			source = ?,
			updated_at = datetime('now')
		WHERE as_of = ?`

	result, err := r.db.ExecContext(ctx, updateQuery,
		data.RiskFreeRate,
		data.RiskFreeRate3Month,
		data.MarketRiskPremium,
		data.InflationRate,
		data.Source,
		data.AsOf,
	)

	if err != nil {
		r.logger.Error("Failed to update macro data", zap.Error(err))
		return fmt.Errorf("failed to update macro data: %w", err)
	}

	// Check if any rows were updated
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		r.logger.Error("Failed to get rows affected", zap.Error(err))
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected > 0 {
		r.logger.Debug("Successfully updated existing macro data",
			zap.Int64("rows_affected", rowsAffected))
		return nil
	}

	// If no rows were updated, insert new record
	insertQuery := `
		INSERT INTO macro_data (
			as_of, risk_free_rate, risk_free_rate_3m, market_risk_premium,
			inflation_rate, source, created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?, datetime('now'), datetime('now')
		)`

	_, err = r.db.ExecContext(ctx, insertQuery,
		data.AsOf,
		data.RiskFreeRate,
		data.RiskFreeRate3Month,
		data.MarketRiskPremium,
		data.InflationRate,
		data.Source,
	)

	if err != nil {
		r.logger.Error("Failed to insert macro data", zap.Error(err))
		return fmt.Errorf("failed to insert macro data: %w", err)
	}

	r.logger.Debug("Successfully inserted new macro data")
	return nil
}

// GetLatest retrieves the most recent macro data
func (r *MacroDataRepository) GetLatest(ctx context.Context) (*entities.MacroData, error) {
	r.logger.Debug("Fetching latest macro data")

	query := `
		SELECT as_of, risk_free_rate, risk_free_rate_3m, market_risk_premium,
			   inflation_rate, source
		FROM macro_data
		ORDER BY as_of DESC
		LIMIT 1`

	var data entities.MacroData
	err := r.db.GetContext(ctx, &data, query)
	if err != nil {
		if err == sql.ErrNoRows {
			r.logger.Debug("No macro data found in database")
			return nil, fmt.Errorf("no macro data found")
		}
		r.logger.Error("Failed to fetch latest macro data", zap.Error(err))
		return nil, fmt.Errorf("failed to fetch latest macro data: %w", err)
	}

	r.logger.Debug("Successfully fetched latest macro data",
		zap.Time("as_of", data.AsOf),
		zap.Float64("risk_free_rate", data.RiskFreeRate))

	return &data, nil
}

// IsStale checks if macro data is stale
func (r *MacroDataRepository) IsStale(ctx context.Context, maxAge time.Duration) (bool, error) {
	r.logger.Debug("Checking if macro data is stale",
		zap.Duration("max_age", maxAge))

	query := `
		SELECT COUNT(*)
		FROM macro_data
		WHERE as_of > datetime('now', '-' || ? || ' seconds')`

	var count int
	err := r.db.GetContext(ctx, &count, query, int(maxAge.Seconds()))
	if err != nil {
		r.logger.Error("Failed to check macro data staleness", zap.Error(err))
		return true, fmt.Errorf("failed to check staleness: %w", err)
	}

	isStale := count == 0
	r.logger.Debug("Macro data staleness check result",
		zap.Bool("is_stale", isStale),
		zap.Int("fresh_count", count))

	return isStale, nil
}

// Additional helper methods for treasury rates integration

// SaveTreasuryRates saves treasury rates data and converts to MacroData
func (r *MacroDataRepository) SaveTreasuryRates(ctx context.Context, rates *entities.TreasuryRates) error {
	r.logger.Debug("Converting treasury rates to macro data")

	// Convert TreasuryRates to MacroData format
	macroData := &entities.MacroData{
		AsOf:               rates.AsOf,
		RiskFreeRate:       rates.GetEffective10Year(),
		RiskFreeRate3Month: rates.Yield3Month,
		Source:             "treasury_conversion",
	}

	// Store as macro data
	return r.Store(ctx, macroData)
}

// UpdateMarketRiskPremium updates just the market risk premium in the latest data
func (r *MacroDataRepository) UpdateMarketRiskPremium(ctx context.Context, premium float64) error {
	r.logger.Debug("Updating market risk premium",
		zap.Float64("premium", premium))

	query := `
		UPDATE macro_data 
		SET market_risk_premium = ?, updated_at = datetime('now')
		WHERE as_of = (SELECT MAX(as_of) FROM macro_data)`

	result, err := r.db.ExecContext(ctx, query, premium)
	if err != nil {
		r.logger.Error("Failed to update market risk premium", zap.Error(err))
		return fmt.Errorf("failed to update market risk premium: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		r.logger.Warn("No macro data found to update market risk premium")
		return fmt.Errorf("no macro data found to update")
	}

	r.logger.Debug("Successfully updated market risk premium")
	return nil
}

// DeleteOldData removes macro data older than specified duration
func (r *MacroDataRepository) DeleteOldData(ctx context.Context, olderThan time.Duration) error {
	r.logger.Debug("Deleting old macro data",
		zap.Duration("older_than", olderThan))

	query := `
		DELETE FROM macro_data
		WHERE as_of < datetime('now', '-' || ? || ' seconds')`

	result, err := r.db.ExecContext(ctx, query, int(olderThan.Seconds()))
	if err != nil {
		r.logger.Error("Failed to delete old macro data", zap.Error(err))
		return fmt.Errorf("failed to delete old macro data: %w", err)
	}

	rowsDeleted, _ := result.RowsAffected()
	r.logger.Info("Successfully deleted old macro data",
		zap.Int64("rows_deleted", rowsDeleted))

	return nil
}
