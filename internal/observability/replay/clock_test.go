package replay

import (
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestNewManifestClock_HappyPath_ReturnsPinnedInstant pins the contract
// that a valid RFC3339Nano started_at produces a Clock whose Now() returns
// exactly that instant.
func TestNewManifestClock_HappyPath_ReturnsPinnedInstant(t *testing.T) {
	want := "2026-04-25T12:34:56Z"
	clock := newManifestClock(want)
	got := clock.Now()
	parsed, _ := time.Parse(time.RFC3339Nano, want)
	if !got.Equal(parsed) {
		t.Errorf("Now() = %v, want %v", got, parsed)
	}
}

// TestNewManifestClock_MalformedTimestamp_LogsWARN_AndFallsBack covers
// RPL-2n (R3 Stage O.12): malformed started_at MUST emit a WARN line
// rather than silently fall back to wall-clock. Cross-year regression
// invariant cannot hold under the fallback, so the operator must know.
func TestNewManifestClock_MalformedTimestamp_LogsWARN_AndFallsBack(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	originalLogger := clockLogger
	clockLogger = zap.New(core)
	defer func() { clockLogger = originalLogger }()

	clock := newManifestClock("not-a-timestamp")

	// Fallback returns a wall clock — Now() should be close to time.Now.
	// We don't assert exact equality (wall clock); just non-zero.
	if clock.Now().IsZero() {
		t.Errorf("fallback clock Now() was zero; expected wall-clock instant")
	}
	// And a WARN line MUST have fired.
	logs := recorded.All()
	if len(logs) == 0 {
		t.Fatal("expected WARN log on malformed started_at; got none")
	}
	found := false
	for _, l := range logs {
		if l.Level == zap.WarnLevel && strings.Contains(l.Message, "malformed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected WARN about malformed started_at; got: %v", logs)
	}
}

// TestNewManifestClock_EmptyTimestamp_LogsWARN_AndFallsBack covers
// the empty-string branch — also a corruption case worth surfacing.
func TestNewManifestClock_EmptyTimestamp_LogsWARN_AndFallsBack(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	originalLogger := clockLogger
	clockLogger = zap.New(core)
	defer func() { clockLogger = originalLogger }()

	clock := newManifestClock("")

	if clock.Now().IsZero() {
		t.Errorf("fallback clock Now() was zero; expected wall-clock instant")
	}
	logs := recorded.All()
	found := false
	for _, l := range logs {
		if l.Level == zap.WarnLevel && strings.Contains(l.Message, "empty") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected WARN about empty started_at; got: %v", logs)
	}
}
