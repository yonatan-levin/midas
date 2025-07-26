package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
)

// AuthRepository implements the auth.Repository interface using SQLite
type AuthRepository struct {
	db *sql.DB
}

// NewAuthRepository creates a new SQLite auth repository
func NewAuthRepository(db *sql.DB) *AuthRepository {
	return &AuthRepository{
		db: db,
	}
}

// GetKeyByHash retrieves an API key by its hash
func (r *AuthRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entities.APIKey, error) {
	query := `
		SELECT id, key_hash, user_id, permissions, rate_limit, expires_at, 
		       is_active, created_at, updated_at, last_used_at, usage_count
		FROM api_keys 
		WHERE key_hash = ? AND is_active = 1
	`

	var key entities.APIKey
	var permissionsJSON string
	var expiresAt, lastUsedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, keyHash).Scan(
		&key.ID,
		&key.KeyHash,
		&key.UserID,
		&permissionsJSON,
		&key.RateLimit,
		&expiresAt,
		&key.IsActive,
		&key.CreatedAt,
		&key.UpdatedAt,
		&lastUsedAt,
		&key.UsageCount,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, auth.ErrKeyNotFound
		}
		return nil, fmt.Errorf("failed to query API key: %w", err)
	}

	// Parse permissions JSON
	var permissions []string
	if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
		return nil, fmt.Errorf("failed to parse permissions: %w", err)
	}

	// Convert string permissions to Permission type
	key.Permissions = make([]entities.Permission, len(permissions))
	for i, p := range permissions {
		key.Permissions[i] = entities.Permission(p)
	}

	// Handle nullable timestamps
	if expiresAt.Valid {
		key.ExpiresAt = &expiresAt.Time
	}
	if lastUsedAt.Valid {
		key.LastUsedAt = &lastUsedAt.Time
	}

	return &key, nil
}

// CreateKey creates a new API key in the database
func (r *AuthRepository) CreateKey(ctx context.Context, key *entities.APIKey) error {
	// Convert permissions to JSON
	permissionsJSON, err := json.Marshal(key.Permissions)
	if err != nil {
		return fmt.Errorf("failed to marshal permissions: %w", err)
	}

	query := `
		INSERT INTO api_keys (
			id, key_hash, user_id, permissions, rate_limit, expires_at,
			is_active, created_at, updated_at, usage_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	var expiresAt interface{}
	if key.ExpiresAt != nil {
		expiresAt = key.ExpiresAt
	}

	_, err = r.db.ExecContext(ctx, query,
		key.ID,
		key.KeyHash,
		key.UserID,
		string(permissionsJSON),
		key.RateLimit,
		expiresAt,
		key.IsActive,
		key.CreatedAt,
		key.UpdatedAt,
		key.UsageCount,
	)

	if err != nil {
		return fmt.Errorf("failed to create API key: %w", err)
	}

	return nil
}

// UpdateKeyStatus updates the active status of an API key
func (r *AuthRepository) UpdateKeyStatus(ctx context.Context, keyID string, isActive bool) error {
	query := `
		UPDATE api_keys 
		SET is_active = ?, updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`

	result, err := r.db.ExecContext(ctx, query, isActive, keyID)
	if err != nil {
		return fmt.Errorf("failed to update key status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return auth.ErrKeyNotFound
	}

	return nil
}

// UpdateKeyExpiration updates the expiration time of an API key
func (r *AuthRepository) UpdateKeyExpiration(ctx context.Context, keyID string, expiresAt time.Time) error {
	query := `
		UPDATE api_keys 
		SET expires_at = ?, updated_at = CURRENT_TIMESTAMP 
		WHERE id = ?
	`

	result, err := r.db.ExecContext(ctx, query, expiresAt, keyID)
	if err != nil {
		return fmt.Errorf("failed to update key expiration: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return auth.ErrKeyNotFound
	}

	return nil
}

// RecordUsage records API key usage for monitoring and rate limiting
func (r *AuthRepository) RecordUsage(ctx context.Context, usage *entities.APIKeyUsage) error {
	query := `
		INSERT INTO api_key_usage (
			id, api_key_id, endpoint, timestamp, response_status,
			response_time_ms, user_agent, ip_address
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.ExecContext(ctx, query,
		usage.ID,
		usage.APIKeyID,
		usage.Endpoint,
		usage.Timestamp,
		usage.ResponseStatus,
		usage.ResponseTimeMs,
		usage.UserAgent,
		usage.IPAddress,
	)

	if err != nil {
		return fmt.Errorf("failed to record usage: %w", err)
	}

	return nil
}

// GetUsageStats returns usage statistics for an API key since a given time
func (r *AuthRepository) GetUsageStats(ctx context.Context, keyID string, since time.Time) (*entities.UsageStats, error) {
	// Get total requests and average response time
	statsQuery := `
		SELECT 
			COUNT(*) as total_requests,
			AVG(response_time_ms) as avg_response_ms,
			MAX(timestamp) as last_activity
		FROM api_key_usage 
		WHERE api_key_id = ? AND timestamp >= ?
	`

	var stats entities.UsageStats
	var avgResponseMs sql.NullFloat64
	var lastActivity sql.NullTime

	err := r.db.QueryRowContext(ctx, statsQuery, keyID, since).Scan(
		&stats.TotalRequests,
		&avgResponseMs,
		&lastActivity,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get usage stats: %w", err)
	}

	if avgResponseMs.Valid {
		stats.AverageResponseMs = int(avgResponseMs.Float64)
	}

	if lastActivity.Valid {
		stats.LastActivityAt = &lastActivity.Time
	}

	// Get error rate
	errorQuery := `
		SELECT COUNT(*) 
		FROM api_key_usage 
		WHERE api_key_id = ? AND timestamp >= ? AND response_status >= 400
	`

	var errorCount int64
	err = r.db.QueryRowContext(ctx, errorQuery, keyID, since).Scan(&errorCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get error count: %w", err)
	}

	if stats.TotalRequests > 0 {
		stats.ErrorRate = float64(errorCount) / float64(stats.TotalRequests)
	}

	// Get most used endpoint
	endpointQuery := `
		SELECT endpoint 
		FROM api_key_usage 
		WHERE api_key_id = ? AND timestamp >= ?
		GROUP BY endpoint 
		ORDER BY COUNT(*) DESC 
		LIMIT 1
	`

	err = r.db.QueryRowContext(ctx, endpointQuery, keyID, since).Scan(&stats.MostUsedEndpoint)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to get most used endpoint: %w", err)
	}

	// Calculate requests per hour
	hours := time.Since(since).Hours()
	if hours > 0 {
		stats.RequestsPerHour = int64(float64(stats.TotalRequests) / hours)
	}

	return &stats, nil
}

// CleanupExpiredUsage removes old usage records (for maintenance)
func (r *AuthRepository) CleanupExpiredUsage(ctx context.Context, olderThan time.Time) error {
	query := `DELETE FROM api_key_usage WHERE timestamp < ?`

	result, err := r.db.ExecContext(ctx, query, olderThan)
	if err != nil {
		return fmt.Errorf("failed to cleanup expired usage: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	// Log the cleanup result (could be added as a parameter or injected logger)
	_ = rowsAffected // For now, just acknowledge we got the count

	return nil
}

// GetActiveKeys returns all active API keys (for administrative purposes)
func (r *AuthRepository) GetActiveKeys(ctx context.Context) ([]*entities.APIKey, error) {
	query := `
		SELECT id, key_hash, user_id, permissions, rate_limit, expires_at,
		       is_active, created_at, updated_at, last_used_at, usage_count
		FROM api_keys 
		WHERE is_active = 1
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query active keys: %w", err)
	}
	defer rows.Close()

	var keys []*entities.APIKey

	for rows.Next() {
		var key entities.APIKey
		var permissionsJSON string
		var expiresAt, lastUsedAt sql.NullTime

		err := rows.Scan(
			&key.ID,
			&key.KeyHash,
			&key.UserID,
			&permissionsJSON,
			&key.RateLimit,
			&expiresAt,
			&key.IsActive,
			&key.CreatedAt,
			&key.UpdatedAt,
			&lastUsedAt,
			&key.UsageCount,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan API key: %w", err)
		}

		// Parse permissions
		var permissions []string
		if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
			return nil, fmt.Errorf("failed to parse permissions: %w", err)
		}

		key.Permissions = make([]entities.Permission, len(permissions))
		for i, p := range permissions {
			key.Permissions[i] = entities.Permission(p)
		}

		// Handle nullable timestamps
		if expiresAt.Valid {
			key.ExpiresAt = &expiresAt.Time
		}
		if lastUsedAt.Valid {
			key.LastUsedAt = &lastUsedAt.Time
		}

		keys = append(keys, &key)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over keys: %w", err)
	}

	return keys, nil
}
