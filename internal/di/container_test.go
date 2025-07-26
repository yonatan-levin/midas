package di

import (
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/midas/dcf-valuation-api/internal/config"
)

// TestNewDatabase_SQLiteConnection tests that SQLite database connection works with correct driver name
func TestNewDatabase_SQLiteConnection(t *testing.T) {
	logger := zaptest.NewLogger(t)

	testCases := []struct {
		name          string
		driverName    string
		sqlitePath    string
		shouldSucceed bool
		description   string
	}{
		{
			name:          "sqlite3_driver_with_memory_db",
			driverName:    "sqlite3",
			sqlitePath:    ":memory:",
			shouldSucceed: true,
			description:   "Should work with sqlite3 driver name and memory database",
		},
		{
			name:          "sqlite_driver_should_now_work",
			driverName:    "sqlite",
			sqlitePath:    ":memory:",
			shouldSucceed: true,
			description:   "Should work with sqlite driver name (gets mapped to sqlite3)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				Database: config.DatabaseConfig{
					Driver:     tc.driverName,
					SQLitePath: tc.sqlitePath,
				},
			}

			db, err := NewDatabase(cfg, logger)

			if tc.shouldSucceed {
				require.NoError(t, err, tc.description)
				require.NotNil(t, db, "Database connection should not be nil")

				// Test basic connectivity
				err = db.Ping()
				assert.NoError(t, err, "Should be able to ping the database")

				// Clean up
				err = db.Close()
				assert.NoError(t, err, "Should be able to close the database")
			} else {
				require.Error(t, err, tc.description)
				assert.Contains(t, err.Error(), "unknown driver", "Should contain unknown driver error")
			}
		})
	}
}

// TestDatabaseDriverMapping tests the driver name mapping functionality
func TestDatabaseDriverMapping(t *testing.T) {
	testCases := []struct {
		name           string
		inputDriver    string
		expectedDriver string
		description    string
	}{
		{
			name:           "sqlite_maps_to_sqlite3",
			inputDriver:    "sqlite",
			expectedDriver: "sqlite3",
			description:    "sqlite should map to sqlite3 for compatibility",
		},
		{
			name:           "postgres_unchanged",
			inputDriver:    "postgres",
			expectedDriver: "postgres",
			description:    "postgres should remain unchanged",
		},
		{
			name:           "sqlite3_unchanged",
			inputDriver:    "sqlite3",
			expectedDriver: "sqlite3",
			description:    "sqlite3 should remain unchanged",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualDriver := mapDatabaseDriver(tc.inputDriver)
			assert.Equal(t, tc.expectedDriver, actualDriver, tc.description)
		})
	}
}

func TestCircuitBreakerFactory_CreateSECCircuitBreaker(t *testing.T) {
	logger := zap.NewNop()
	factory := &CircuitBreakerFactory{logger: logger}

	t.Run("creates SEC circuit breaker with correct config", func(t *testing.T) {
		cb := factory.CreateSECCircuitBreaker()

		assert.NotNil(t, cb)
		assert.Equal(t, "CLOSED", cb.State())
	})
}

func TestCircuitBreakerFactory_CreateMarketDataCircuitBreaker(t *testing.T) {
	logger := zap.NewNop()
	factory := &CircuitBreakerFactory{logger: logger}

	t.Run("creates market data circuit breaker with correct config", func(t *testing.T) {
		cb := factory.CreateMarketDataCircuitBreaker()

		assert.NotNil(t, cb)
		assert.Equal(t, "CLOSED", cb.State())
	})
}

func TestRetryPolicyFactory_CreateSECRetryPolicy(t *testing.T) {
	logger := zap.NewNop()
	factory := &RetryPolicyFactory{logger: logger}

	t.Run("creates SEC retry policy", func(t *testing.T) {
		policy := factory.CreateSECRetryPolicy()

		assert.NotNil(t, policy)
	})
}

func TestRetryPolicyFactory_CreateMarketDataRetryPolicy(t *testing.T) {
	logger := zap.NewNop()
	factory := &RetryPolicyFactory{logger: logger}

	t.Run("creates market data retry policy", func(t *testing.T) {
		policy := factory.CreateMarketDataRetryPolicy()

		assert.NotNil(t, policy)
	})
}

func TestContainer_Creation(t *testing.T) {
	t.Run("creates container successfully", func(t *testing.T) {
		container := NewContainer()

		assert.NotNil(t, container)
		assert.NotNil(t, container.app)
	})
}

// Integration test would require full DI setup
func TestFactories_Integration(t *testing.T) {
	t.Run("factory types exist", func(t *testing.T) {
		logger := zap.NewNop()

		// Test that all factory types exist and can be created
		cbFactory := &CircuitBreakerFactory{logger: logger}
		retryFactory := &RetryPolicyFactory{logger: logger}

		require.NotNil(t, cbFactory)
		require.NotNil(t, retryFactory)
	})
}
