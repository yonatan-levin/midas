package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	sqliterepo "github.com/midas/dcf-valuation-api/internal/infra/repositories/sqlite"
	"github.com/midas/dcf-valuation-api/internal/services/auth"
)

// --- Constants for the known demo key seeded by the 0001 migration. ---
const (
	// demoRawKey is the plaintext API key stored in the migration comment.
	// Users pass this value in the X-API-Key header.
	demoRawKey = "dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788"

	// demoKeyHash is the SHA-256 hex digest of demoRawKey, stored in the DB.
	demoKeyHash = "07b7dc84a8e720803fe20679742b813baecde27256f57d9bb062069193503802"

	// demoKeyID is the deterministic row ID used in the migration.
	demoKeyID = "demo_key_0001"
)

// allDemoPermissions is the full set of permissions the demo key must have
// after a fresh install or an upgrade migration.
var allDemoPermissions = []entities.Permission{
	entities.PermissionReadFairValue,
	entities.PermissionReadHealth,
	entities.PermissionReadMetrics,
	entities.PermissionManageKeys,
	entities.PermissionAdmin,
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// projectRoot resolves the repository root from this test file's location.
// The file lives at internal/integration/, so two levels up is the root.
func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

// readSQL reads a SQL file relative to the project root and returns its content.
func readSQL(t *testing.T, relPath string) string {
	t.Helper()
	absPath := filepath.Join(projectRoot(), relPath)
	data, err := os.ReadFile(absPath)
	require.NoError(t, err, "failed to read SQL file: %s", absPath)
	return string(data)
}

// openMemoryDB opens an in-memory SQLite database and registers cleanup.
func openMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "failed to open in-memory SQLite")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// applySQL executes a raw SQL string against the given database.
func applySQL(t *testing.T, db *sql.DB, label, sqlText string) {
	t.Helper()
	_, err := db.Exec(sqlText)
	require.NoError(t, err, "failed to apply SQL: %s", label)
}

// bootstrapDB creates a fresh in-memory SQLite DB, applies schema.sql and the
// 0001_seed_demo_key.sql migration, then returns the ready database.
func bootstrapDB(t *testing.T) *sql.DB {
	t.Helper()

	db := openMemoryDB(t)

	schemaSQL := readSQL(t, filepath.Join("internal", "infra", "database", "schema.sql"))
	applySQL(t, db, "schema.sql", schemaSQL)

	migrationSQL := readSQL(t, filepath.Join("migrations", "0001_seed_demo_key.sql"))
	applySQL(t, db, "0001_seed_demo_key.sql", migrationSQL)

	return db
}

// newAuthService wires up a real AuthRepository + auth.Service on top of the
// given *sql.DB, using a test logger. This is the same stack production uses.
func newAuthService(t *testing.T, db *sql.DB) *auth.Service {
	t.Helper()
	repo := sqliterepo.NewAuthRepository(db)
	logger := zaptest.NewLogger(t)
	return auth.NewService(repo, logger)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestBootstrap_DemoKeyHasFullPermissions applies schema + seed migration to a
// fresh DB and verifies that the demo key can be validated and carries all five
// expected permissions. This is the primary regression test for BUG-011.
func TestBootstrap_DemoKeyHasFullPermissions(t *testing.T) {
	db := bootstrapDB(t)
	svc := newAuthService(t, db)

	ctx := context.Background()
	info, err := svc.ValidateKey(ctx, demoRawKey)
	require.NoError(t, err, "demo key validation must succeed after fresh bootstrap")
	require.NotNil(t, info)

	// Verify identity fields
	assert.Equal(t, demoKeyID, info.ID, "key ID must match migration value")
	assert.Equal(t, "demo-user", info.UserID, "user_id must match migration value")
	assert.True(t, info.IsActive, "demo key must be active")

	// Verify all 5 permissions are present
	assert.Len(t, info.Permissions, len(allDemoPermissions),
		"demo key must have exactly %d permissions", len(allDemoPermissions))
	assert.ElementsMatch(t, allDemoPermissions, info.Permissions,
		"demo key permissions must match the expected set")

	// Explicitly check the two critical permissions that were missing in BUG-011
	assertHasPermission(t, info.Permissions, entities.PermissionManageKeys)
	assertHasPermission(t, info.Permissions, entities.PermissionAdmin)
}

// TestBootstrap_UpgradePath simulates an existing database that already has the
// demo key row with only a single permission. Applying the migration must
// upgrade the permissions via the UPDATE fallback (INSERT OR IGNORE skips,
// but the trailing UPDATE applies unconditionally on id='demo_key_0001').
func TestBootstrap_UpgradePath(t *testing.T) {
	db := openMemoryDB(t)

	// Step 1: Apply schema only (no migration yet)
	schemaSQL := readSQL(t, filepath.Join("internal", "infra", "database", "schema.sql"))
	applySQL(t, db, "schema.sql", schemaSQL)

	// Step 2: Insert a "legacy" demo key row with only one permission
	legacyInsert := `
		INSERT INTO api_keys (id, key_hash, user_id, permissions, rate_limit, is_active, created_at, updated_at)
		VALUES (
			'demo_key_0001',
			'07b7dc84a8e720803fe20679742b813baecde27256f57d9bb062069193503802',
			'demo-user',
			'["read:fair_value"]',
			1000,
			1,
			CURRENT_TIMESTAMP,
			CURRENT_TIMESTAMP
		);
	`
	applySQL(t, db, "legacy INSERT", legacyInsert)

	// Sanity check: the key has only 1 permission before migration
	svc := newAuthService(t, db)
	ctx := context.Background()
	before, err := svc.ValidateKey(ctx, demoRawKey)
	require.NoError(t, err)
	assert.Len(t, before.Permissions, 1, "before migration the key must have exactly 1 permission")

	// Step 3: Apply the migration (INSERT OR IGNORE will skip, UPDATE will apply)
	migrationSQL := readSQL(t, filepath.Join("migrations", "0001_seed_demo_key.sql"))
	applySQL(t, db, "0001_seed_demo_key.sql", migrationSQL)

	// Step 4: Verify all 5 permissions are now present
	after, err := svc.ValidateKey(ctx, demoRawKey)
	require.NoError(t, err, "demo key must still be valid after upgrade migration")
	require.NotNil(t, after)

	assert.Len(t, after.Permissions, len(allDemoPermissions),
		"after migration the key must have all %d permissions", len(allDemoPermissions))
	assert.ElementsMatch(t, allDemoPermissions, after.Permissions,
		"permissions after upgrade must match the full expected set")

	// Verify the critical permissions that were missing in the BUG-011 scenario
	assertHasPermission(t, after.Permissions, entities.PermissionManageKeys)
	assertHasPermission(t, after.Permissions, entities.PermissionAdmin)
}

// TestBootstrap_HashMatchesKey is a pure cryptographic sanity check: the
// SHA-256 of the known raw demo key must equal the hash stored in the
// migration SQL. If this fails, the key and migration are out of sync.
func TestBootstrap_HashMatchesKey(t *testing.T) {
	hash := sha256.Sum256([]byte(demoRawKey))
	got := hex.EncodeToString(hash[:])

	assert.Equal(t, demoKeyHash, got,
		"SHA-256 of demo raw key must match the hash embedded in 0001_seed_demo_key.sql")
}

// TestBootstrap_DemoKeyCanCreateNewKeys proves the full bootstrap gap is fixed:
// the demo key has manage:keys, so the auth service can use it to create
// additional API keys. This is the exact workflow that BUG-011 broke.
func TestBootstrap_DemoKeyCanCreateNewKeys(t *testing.T) {
	db := bootstrapDB(t)
	svc := newAuthService(t, db)
	ctx := context.Background()

	// Step 1: Validate the demo key and confirm it carries manage:keys
	demoInfo, err := svc.ValidateKey(ctx, demoRawKey)
	require.NoError(t, err, "demo key must be valid")
	assertHasPermission(t, demoInfo.Permissions, entities.PermissionManageKeys)

	// Step 2: Use the service to create a brand-new API key
	newKey, err := svc.CreateKey(ctx, "new-user", []entities.Permission{
		entities.PermissionReadFairValue,
		entities.PermissionReadHealth,
	})
	require.NoError(t, err, "creating a new key via the auth service must succeed")
	require.NotNil(t, newKey)

	assert.NotEmpty(t, newKey.Key, "newly created key must have a raw value")
	assert.NotEmpty(t, newKey.ID, "newly created key must have an ID")
	assert.Equal(t, "new-user", newKey.UserID)
	assert.True(t, newKey.IsActive)

	// Step 3: Validate the newly created key works end-to-end
	newInfo, err := svc.ValidateKey(ctx, newKey.Key)
	require.NoError(t, err, "newly created key must validate successfully")
	assert.Equal(t, "new-user", newInfo.UserID)
	assert.ElementsMatch(t,
		[]entities.Permission{entities.PermissionReadFairValue, entities.PermissionReadHealth},
		newInfo.Permissions,
		"newly created key must carry exactly the requested permissions",
	)
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

// assertHasPermission checks that a specific permission exists in the slice.
func assertHasPermission(t *testing.T, perms []entities.Permission, want entities.Permission) {
	t.Helper()
	for _, p := range perms {
		if p == want {
			return
		}
	}
	t.Errorf("expected permission %q not found in %v", want, perms)
}
