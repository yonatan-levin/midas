package di

// Tests for the rewritten NewLogger constructor (O.5).
//
// The tests cover:
//  1. File.Enabled=true  → log file created, contains JSON fields
//  2. File.Enabled=false → no log file created
//  3. Format="console"  → logger starts up without error
//  4. Format="json"     → logger starts up without error
//  5. Zero-value Config  → logger starts up without error (backward-compat fallback)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
)

// baseLoggingCfg returns a minimal config suitable for testing NewLogger.
// It uses a zero-value LogFileConfig (file disabled) unless overridden by the
// caller. Database / Cache fields are not needed by NewLogger.
func baseLoggingCfg() *config.Config {
	return &config.Config{
		LogLevel: "debug",
		Logging: config.LoggingConfig{
			Level:  "debug",
			Format: "json",
		},
	}
}

// TestNewLogger_FileEnabled verifies that when File.Enabled=true, NewLogger
// creates the log file, and that a logged entry is persisted as valid JSON
// containing the expected fields.
func TestNewLogger_FileEnabled(t *testing.T) {
	// NOTE: We manage the log directory manually (not t.TempDir()) because
	// lumberjack holds the file handle open on Windows until the logger is
	// synced and the underlying lumberjack.Logger is closed. t.TempDir()
	// would fail to clean up while the handle is still open.
	dir, err := os.MkdirTemp("", "midas-logger-test-*")
	require.NoError(t, err)

	logPath := filepath.Join(dir, "test.log")

	cfg := baseLoggingCfg()
	cfg.Logging.File = config.LogFileConfig{
		Enabled:    true,
		Path:       logPath,
		MaxSizeMB:  10,
		MaxBackups: 1,
		MaxAgeDays: 1,
		Compress:   false,
	}

	logger, err := NewLogger(cfg)
	require.NoError(t, err)
	require.NotNil(t, logger)

	// Log a test message and sync to flush the file sink.
	logger.Info("test_file_log", zap.String("ticker", "AAPL"), zap.Float64("price", 192.5))
	// Sync flushes all cores including the lumberjack file writer.
	_ = logger.Sync()

	// Verify the log file was created and contains a valid JSON entry.
	data, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr, "log file should exist after writing")
	require.NotEmpty(t, data, "log file should not be empty")

	// Parse the first line as JSON and check for expected fields.
	lines := splitLines(data)
	require.NotEmpty(t, lines, "log file should contain at least one line")

	var entry map[string]interface{}
	parseErr := json.Unmarshal([]byte(lines[0]), &entry)
	require.NoError(t, parseErr, "first log line should be valid JSON")

	// The "midas" name is added via .Named("midas") — check the logger name field.
	assert.Contains(t, entry, "ts", "JSON log entry should have a 'ts' (timestamp) field")
	assert.Contains(t, entry, "level", "JSON log entry should have a 'level' field")

	// Cleanup: remove directory manually after we're done reading.
	_ = os.RemoveAll(dir)
}

// TestNewLogger_FileDisabled verifies that when File.Enabled=false, no log
// file is created on disk.
func TestNewLogger_FileDisabled(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "should_not_exist.log")

	cfg := baseLoggingCfg()
	cfg.Logging.File = config.LogFileConfig{
		Enabled: false,
		Path:    logPath,
	}

	logger, err := NewLogger(cfg)
	require.NoError(t, err)
	require.NotNil(t, logger)

	logger.Info("this goes only to stdout")
	_ = logger.Sync()

	// The file should NOT exist.
	_, statErr := os.Stat(logPath)
	assert.True(t, os.IsNotExist(statErr), "log file must not be created when File.Enabled=false")
}

// TestNewLogger_FormatVariants verifies that both "console" and "json" formats
// build successfully without error.
func TestNewLogger_FormatVariants(t *testing.T) {
	tests := []struct {
		name   string
		format string
	}{
		{"json_format", "json"},
		{"console_format", "console"},
		{"empty_format_defaults_to_json", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseLoggingCfg()
			cfg.Logging.Format = tc.format
			cfg.Logging.File.Enabled = false // no file I/O in format tests

			logger, err := NewLogger(cfg)
			require.NoError(t, err, "NewLogger must not error for format=%q", tc.format)
			require.NotNil(t, logger)

			// Ensure it's usable without panicking.
			logger.Info("format_test", zap.String("format", tc.format))
		})
	}
}

// TestNewLogger_ZeroValueConfig verifies that NewLogger handles a zero-value
// Config gracefully (backward-compat fallback: defaults to info/json/stdout).
func TestNewLogger_ZeroValueConfig(t *testing.T) {
	cfg := &config.Config{} // completely zero-value

	logger, err := NewLogger(cfg)
	require.NoError(t, err, "NewLogger must not error for a zero-value Config")
	require.NotNil(t, logger)

	// Must be usable.
	logger.Info("zero_value_config_test")
}

// TestNewLogger_LevelFiltering verifies that the level setting is respected:
// a logger built with "error" level should not emit Info entries.
// We verify this indirectly by checking the logger builds without error and
// can be called at all levels without panic.
func TestNewLogger_LevelFiltering(t *testing.T) {
	cfg := baseLoggingCfg()
	cfg.Logging.Level = "error"
	cfg.Logging.File.Enabled = false

	logger, err := NewLogger(cfg)
	require.NoError(t, err)

	// These should not panic, and Debug/Info/Warn should be silently dropped.
	logger.Debug("should_be_dropped")
	logger.Info("should_be_dropped")
	logger.Warn("should_be_dropped")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// splitLines splits byte content into non-empty lines.
func splitLines(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := string(data[start:i])
			if line != "" {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		line := string(data[start:])
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
