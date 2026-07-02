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
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	//
	// t.Cleanup runs after the test body returns, at which point Sync() has
	// flushed and the file handle has been released — so cleanup is safe
	// even if an assertion mid-test causes an early bail-out.
	dir, err := os.MkdirTemp("", "midas-logger-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

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

	var entry map[string]any
	parseErr := json.Unmarshal([]byte(lines[0]), &entry)
	require.NoError(t, parseErr, "first log line should be valid JSON")

	assert.Contains(t, entry, "ts", "JSON log entry should have a 'ts' (timestamp) field")
	assert.Contains(t, entry, "level", "JSON log entry should have a 'level' field")
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

// TestNewLogger_FileSinkProbeFailure pins M-1e: when logging.file.enabled=true
// but the configured path's parent directory cannot be created (permission
// denied / nonexistent drive / etc.), NewLogger must:
//   - return successfully (no error) so the server keeps running on stdout-only
//   - log a single warning to stdout containing "falling back to stdout-only"
//     plus the failing path, so operators get a clear signal that file logs
//     are being lost
//   - NOT create the configured log file on disk (the file core must be skipped
//     entirely, not registered with a lumberjack writer that would lazily fail)
//
// Without the probe, lumberjack would silently drop every log line at first
// write — operators would see stdout logs flowing and assume the file sink was
// healthy too. The probe surfaces the misconfiguration synchronously.
func TestNewLogger_FileSinkProbeFailure(t *testing.T) {
	// Choose a path that is guaranteed-unwritable on the host platform.
	// On Windows, a path inside a nonexistent drive (Z:) produces a
	// directory-creation failure on MkdirAll. On Linux, "/proc/1/nope/x.log"
	// fails because /proc/1 is a kernel-managed directory that rejects
	// arbitrary subdirectory creation even for root.
	var unwritablePath, wantPathSubstr string
	switch runtime.GOOS {
	case "windows":
		unwritablePath = `Z:\midas-m1e-probe-fail\test.log`
		wantPathSubstr = "midas-m1e-probe-fail"
	default:
		unwritablePath = "/proc/1/nope/test.log"
		wantPathSubstr = "/proc/1/nope"
	}

	cfg := baseLoggingCfg()
	cfg.Logging.File.Enabled = true
	cfg.Logging.File.Path = unwritablePath
	cfg.Logging.File.MaxSizeMB = 1
	cfg.Logging.File.MaxBackups = 1
	cfg.Logging.File.MaxAgeDays = 1

	// Capture stdout so we can assert the fallback warning was emitted there.
	// NewLogger writes the warning to os.Stdout via the pre-built stdoutCore;
	// swapping os.Stdout for a pipe captures that output for the duration of
	// the call, then restores the original.
	oldStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	require.NoError(t, pipeErr)
	os.Stdout = w

	logger, lerr := NewLogger(cfg)

	// Close the writer before reading, otherwise io.ReadAll blocks.
	require.NoError(t, w.Close())
	os.Stdout = oldStdout

	require.NoError(t, lerr, "NewLogger must not return an error on probe failure — the server must stay up")
	require.NotNil(t, logger)

	captured, _ := io.ReadAll(r)
	capturedStr := string(captured)

	assert.Contains(t, capturedStr, "falling back to stdout-only",
		"expected fallback warning in stdout, got: %s", capturedStr)
	// Match a per-OS substring of the failing path (the two OSes use different
	// unwritable paths; the Windows-only literal fails on Linux CI — CI-1 / #20).
	assert.Contains(t, capturedStr, wantPathSubstr,
		"warning must include the failing path. captured: %s", capturedStr)

	// Sanity: the configured log file must NOT exist (probe rejected creation).
	// If it does exist, the probe didn't run / wasn't honored.
	if _, statErr := os.Stat(unwritablePath); statErr == nil {
		// Best-effort cleanup if it somehow got created.
		_ = os.Remove(unwritablePath)
		t.Fatalf("log file unexpectedly created at %s — probe must have failed silently", unwritablePath)
	}
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
