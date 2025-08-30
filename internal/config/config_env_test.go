package config

import (
	"testing"
)

// Test that environment variables override nested config keys via viper with key replacer
func TestEnvOverridesDatabaseDriverAndPath(t *testing.T) {
	// Ensure clean env
	t.Setenv("DATABASE_DRIVER", "sqlite3")
	t.Setenv("DATABASE_SQLITE_PATH", ":memory:")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.Database.Driver != "sqlite3" {
		t.Fatalf("expected database.driver to be sqlite3, got %q", cfg.Database.Driver)
	}

	if cfg.Database.SQLitePath != ":memory:" {
		t.Fatalf("expected database.sqlite_path to be :memory:, got %q", cfg.Database.SQLitePath)
	}
}
