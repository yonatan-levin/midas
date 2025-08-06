package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// setupTestDatabase creates an in-memory SQLite database with the full schema loaded
// This ensures all tests run against the same database structure as production
func setupTestDatabase(t *testing.T) *sqlx.DB {
	// Create in-memory SQLite database for testing
	db, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	// Load the actual schema file
	schema, err := loadSchemaFileForTests()
	require.NoError(t, err, "Failed to load schema file")

	// Apply the schema to the test database
	_, err = db.Exec(schema)
	require.NoError(t, err, "Failed to apply schema to test database")

	return db
}

// loadSchemaFile reads the database schema from the schema.sql file
func loadSchemaFile() (string, error) {
	// Get the current file's directory
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("unable to get current file path")
	}

	// Navigate from internal/infra/repositories/sqlite to internal/infra/database
	currentDir := filepath.Dir(filename)
	schemaPath := filepath.Join(currentDir, "..", "..", "database", "schema.sql")

	// Read the schema file
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return "", fmt.Errorf("failed to read schema file at %s: %w", schemaPath, err)
	}

	return string(schemaBytes), nil
}

// loadSchemaFileForTests reads the database schema and modifies it for test usage
func loadSchemaFileForTests() (string, error) {
	schema, err := loadSchemaFile()
	if err != nil {
		return "", err
	}

	// Replace the FK pragma to disable foreign key constraints for tests
	// This simplifies test data setup by not requiring complete referential data
	modifiedSchema := strings.Replace(schema, "PRAGMA foreign_keys = ON;", "PRAGMA foreign_keys = OFF;", 1)

	return modifiedSchema, nil
}

// cleanupTestDatabase closes the database connection
// This is a helper for defer cleanup in tests
func cleanupTestDatabase(db *sqlx.DB) {
	if db != nil {
		_ = db.Close() // nolint:errcheck
	}
}
