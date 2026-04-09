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
	"github.com/midas/dcf-valuation-api/internal/services/auth"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupAuthTestDB creates an in-memory SQLite *sql.DB with the full schema.
// AuthRepository uses *sql.DB (standard library), not *sqlx.DB, so we cannot
// reuse the shared setupTestDatabase helper.
func setupAuthTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "opening in-memory sqlite")

	// Reuse the package-level helper that already knows how to locate the
	// schema file via runtime.Caller and disable foreign key constraints.
	schema, err := loadSchemaFileForTests()
	require.NoError(t, err, "loading schema file")

	_, err = db.Exec(schema)
	require.NoError(t, err, "applying schema to test database")

	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newTestAPIKey builds a minimal, valid APIKey entity suitable for insertion.
func newTestAPIKey(id, keyHash, userID string, active bool) *entities.APIKey {
	now := time.Now().UTC().Truncate(time.Second)
	return &entities.APIKey{
		ID:      id,
		KeyHash: keyHash,
		UserID:  userID,
		Permissions: []entities.Permission{
			entities.PermissionReadFairValue,
			entities.PermissionReadHealth,
		},
		RateLimit:  1000,
		IsActive:   active,
		CreatedAt:  now,
		UpdatedAt:  now,
		UsageCount: 0,
	}
}

// newTestUsage builds a minimal APIKeyUsage record.
func newTestUsage(id, keyID, endpoint string, status int, ts time.Time) *entities.APIKeyUsage {
	return &entities.APIKeyUsage{
		ID:             id,
		APIKeyID:       keyID,
		Endpoint:       endpoint,
		Timestamp:      ts,
		ResponseStatus: status,
		ResponseTimeMs: 50,
		UserAgent:      "test-agent",
		IPAddress:      "127.0.0.1",
	}
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestAuthRepository_NewAuthRepository(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	require.NotNil(t, repo, "NewAuthRepository should return a non-nil repo")
}

// ---------------------------------------------------------------------------
// CreateKey
// ---------------------------------------------------------------------------

func TestAuthRepository_CreateKey_HappyPath(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	key := newTestAPIKey("key-1", "hash-abc", "user-1", true)
	err := repo.CreateKey(ctx, key)
	assert.NoError(t, err, "CreateKey should insert without error")
}

func TestAuthRepository_CreateKey_DuplicateHash(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	key := newTestAPIKey("key-1", "hash-dup", "user-1", true)
	require.NoError(t, repo.CreateKey(ctx, key))

	// Second insert with same hash should fail (UNIQUE constraint).
	dup := newTestAPIKey("key-2", "hash-dup", "user-2", true)
	err := repo.CreateKey(ctx, dup)
	assert.Error(t, err, "duplicate key_hash should violate UNIQUE constraint")
}

// ---------------------------------------------------------------------------
// GetKeyByHash
// ---------------------------------------------------------------------------

func TestAuthRepository_GetKeyByHash_HappyPath(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	key := newTestAPIKey("key-get-1", "hash-get-1", "user-get", true)
	require.NoError(t, repo.CreateKey(ctx, key))

	got, err := repo.GetKeyByHash(ctx, "hash-get-1")
	require.NoError(t, err)

	assert.Equal(t, key.ID, got.ID)
	assert.Equal(t, key.KeyHash, got.KeyHash)
	assert.Equal(t, key.UserID, got.UserID)
	assert.Equal(t, key.RateLimit, got.RateLimit)
	assert.True(t, got.IsActive)

	// Permissions should be deserialized from JSON correctly.
	require.Len(t, got.Permissions, 2)
	assert.Equal(t, entities.PermissionReadFairValue, got.Permissions[0])
	assert.Equal(t, entities.PermissionReadHealth, got.Permissions[1])
}

func TestAuthRepository_GetKeyByHash_NotFound(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	_, err := repo.GetKeyByHash(ctx, "nonexistent-hash")
	assert.ErrorIs(t, err, auth.ErrKeyNotFound,
		"should return ErrKeyNotFound for missing key")
}

func TestAuthRepository_GetKeyByHash_InactiveKey(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	// Insert an inactive key.
	key := newTestAPIKey("key-inactive", "hash-inactive", "user-inactive", false)
	require.NoError(t, repo.CreateKey(ctx, key))

	// Query filters on is_active = 1, so inactive key should not be returned.
	_, err := repo.GetKeyByHash(ctx, "hash-inactive")
	assert.ErrorIs(t, err, auth.ErrKeyNotFound,
		"inactive key should not be found by GetKeyByHash")
}

// ---------------------------------------------------------------------------
// UpdateKeyStatus
// ---------------------------------------------------------------------------

func TestAuthRepository_UpdateKeyStatus_Deactivate(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	key := newTestAPIKey("key-status-1", "hash-status-1", "user-s", true)
	require.NoError(t, repo.CreateKey(ctx, key))

	// Key should be retrievable before deactivation.
	_, err := repo.GetKeyByHash(ctx, "hash-status-1")
	require.NoError(t, err)

	// Deactivate.
	err = repo.UpdateKeyStatus(ctx, "key-status-1", false)
	assert.NoError(t, err)

	// After deactivation, GetKeyByHash should no longer find it.
	_, err = repo.GetKeyByHash(ctx, "hash-status-1")
	assert.ErrorIs(t, err, auth.ErrKeyNotFound,
		"deactivated key should not be returned")
}

func TestAuthRepository_UpdateKeyStatus_Reactivate(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	// Start inactive.
	key := newTestAPIKey("key-react", "hash-react", "user-r", false)
	require.NoError(t, repo.CreateKey(ctx, key))

	// Reactivate.
	err := repo.UpdateKeyStatus(ctx, "key-react", true)
	assert.NoError(t, err)

	got, err := repo.GetKeyByHash(ctx, "hash-react")
	require.NoError(t, err)
	assert.True(t, got.IsActive)
}

func TestAuthRepository_UpdateKeyStatus_NotFound(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	err := repo.UpdateKeyStatus(ctx, "nonexistent-id", false)
	assert.ErrorIs(t, err, auth.ErrKeyNotFound,
		"should return ErrKeyNotFound when no rows match")
}

// ---------------------------------------------------------------------------
// UpdateKeyExpiration
// ---------------------------------------------------------------------------

func TestAuthRepository_UpdateKeyExpiration_HappyPath(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	key := newTestAPIKey("key-exp-1", "hash-exp-1", "user-e", true)
	require.NoError(t, repo.CreateKey(ctx, key))

	expiration := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	err := repo.UpdateKeyExpiration(ctx, "key-exp-1", expiration)
	assert.NoError(t, err)

	// Verify expiration was persisted by fetching the key.
	got, err := repo.GetKeyByHash(ctx, "hash-exp-1")
	require.NoError(t, err)
	require.NotNil(t, got.ExpiresAt, "ExpiresAt should be set after update")
	// SQLite may lose sub-second precision; compare truncated to seconds.
	assert.WithinDuration(t, expiration, *got.ExpiresAt, time.Second)
}

func TestAuthRepository_UpdateKeyExpiration_NotFound(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	err := repo.UpdateKeyExpiration(ctx, "nonexistent-id", time.Now())
	assert.ErrorIs(t, err, auth.ErrKeyNotFound,
		"should return ErrKeyNotFound when no rows match")
}

// ---------------------------------------------------------------------------
// RecordUsage
// ---------------------------------------------------------------------------

func TestAuthRepository_RecordUsage_HappyPath(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	// Create the parent key first (FK exists in schema, though we disable
	// enforcement for tests; still good practice).
	key := newTestAPIKey("key-usage-1", "hash-usage-1", "user-u", true)
	require.NoError(t, repo.CreateKey(ctx, key))

	usage := newTestUsage("usage-1", "key-usage-1", "/api/v1/fair-value", 200, time.Now().UTC())
	err := repo.RecordUsage(ctx, usage)
	assert.NoError(t, err, "RecordUsage should insert without error")
}

// ---------------------------------------------------------------------------
// GetUsageStats
// ---------------------------------------------------------------------------

func TestAuthRepository_GetUsageStats_Aggregation(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	key := newTestAPIKey("key-stats", "hash-stats", "user-stats", true)
	require.NoError(t, repo.CreateKey(ctx, key))

	since := time.Now().UTC().Add(-1 * time.Hour)

	// Record 3 usages: 2 success (200), 1 error (500).
	require.NoError(t, repo.RecordUsage(ctx, newTestUsage(
		"u1", "key-stats", "/api/v1/fair-value", 200, time.Now().UTC(),
	)))
	require.NoError(t, repo.RecordUsage(ctx, newTestUsage(
		"u2", "key-stats", "/api/v1/fair-value", 200, time.Now().UTC(),
	)))
	require.NoError(t, repo.RecordUsage(ctx, newTestUsage(
		"u3", "key-stats", "/api/v1/health", 500, time.Now().UTC(),
	)))

	stats, err := repo.GetUsageStats(ctx, "key-stats", since)
	require.NoError(t, err)

	assert.Equal(t, int64(3), stats.TotalRequests, "should count all 3 requests")

	// Error rate: 1 error out of 3 requests => ~0.333
	assert.InDelta(t, 1.0/3.0, stats.ErrorRate, 0.01,
		"error rate should be ~33%")

	// Most used endpoint: /api/v1/fair-value appears twice.
	assert.Equal(t, "/api/v1/fair-value", stats.MostUsedEndpoint)

	// LastActivityAt should be non-nil.
	assert.NotNil(t, stats.LastActivityAt)
}

func TestAuthRepository_GetUsageStats_NoUsage(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	key := newTestAPIKey("key-empty", "hash-empty", "user-empty", true)
	require.NoError(t, repo.CreateKey(ctx, key))

	since := time.Now().UTC().Add(-1 * time.Hour)
	stats, err := repo.GetUsageStats(ctx, "key-empty", since)
	require.NoError(t, err)

	assert.Equal(t, int64(0), stats.TotalRequests)
	assert.Equal(t, float64(0), stats.ErrorRate)
}

// ---------------------------------------------------------------------------
// CleanupExpiredUsage
// ---------------------------------------------------------------------------

func TestAuthRepository_CleanupExpiredUsage(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	key := newTestAPIKey("key-cleanup", "hash-cleanup", "user-cleanup", true)
	require.NoError(t, repo.CreateKey(ctx, key))

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour) // 2 days ago
	recent := now.Add(-1 * time.Hour)

	// Insert old and recent usage records.
	require.NoError(t, repo.RecordUsage(ctx,
		newTestUsage("old-1", "key-cleanup", "/old", 200, old)))
	require.NoError(t, repo.RecordUsage(ctx,
		newTestUsage("old-2", "key-cleanup", "/old", 200, old.Add(time.Minute))))
	require.NoError(t, repo.RecordUsage(ctx,
		newTestUsage("new-1", "key-cleanup", "/new", 200, recent)))

	// Cleanup records older than 24 hours.
	cutoff := now.Add(-24 * time.Hour)
	err := repo.CleanupExpiredUsage(ctx, cutoff)
	require.NoError(t, err)

	// Verify: only the recent record should remain.
	var remaining int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM api_key_usage WHERE api_key_id = ?", "key-cleanup",
	).Scan(&remaining)
	require.NoError(t, err)
	assert.Equal(t, 1, remaining,
		"only the recent usage record should survive cleanup")
}

// ---------------------------------------------------------------------------
// GetActiveKeys
// ---------------------------------------------------------------------------

func TestAuthRepository_GetActiveKeys(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	// Create 2 active + 1 inactive keys.
	require.NoError(t, repo.CreateKey(ctx,
		newTestAPIKey("active-1", "hash-a1", "user-a", true)))
	require.NoError(t, repo.CreateKey(ctx,
		newTestAPIKey("active-2", "hash-a2", "user-b", true)))
	require.NoError(t, repo.CreateKey(ctx,
		newTestAPIKey("inactive-1", "hash-i1", "user-c", false)))

	keys, err := repo.GetActiveKeys(ctx)
	require.NoError(t, err)

	assert.Len(t, keys, 2, "should return only active keys")

	// All returned keys must be active and have properly deserialized permissions.
	for _, k := range keys {
		assert.True(t, k.IsActive, "returned key should be active")
		assert.Len(t, k.Permissions, 2, "permissions should be deserialized")
	}
}

func TestAuthRepository_GetActiveKeys_Empty(t *testing.T) {
	db := setupAuthTestDB(t)
	repo := NewAuthRepository(db)
	ctx := context.Background()

	keys, err := repo.GetActiveKeys(ctx)
	require.NoError(t, err)
	assert.Empty(t, keys, "should return empty slice when no keys exist")
}
