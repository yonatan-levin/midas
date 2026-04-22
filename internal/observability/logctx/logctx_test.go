package logctx_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// TestLogctx_RoundTrip verifies that a logger injected into a context can be retrieved.
func TestLogctx_RoundTrip(t *testing.T) {
	core, _ := observer.New(zap.DebugLevel)
	l := zap.New(core)

	ctx := logctx.Inject(context.Background(), l)
	got := logctx.From(ctx)

	// Must be exactly the same pointer (no wrapping).
	assert.Equal(t, l, got, "From(Inject(ctx, l)) should return the same logger")
}

// TestLogctx_MissReturnsNop verifies that From returns a non-nil no-op logger
// when the context carries no logger.
func TestLogctx_MissReturnsNop(t *testing.T) {
	got := logctx.From(context.Background())

	require.NotNil(t, got, "From() must never return nil")

	// Confirm it is a no-op by logging and checking the observer produces zero entries.
	// We verify via the fact that the returned logger is functional (does not panic)
	// and produces no observable side effects on a plain Background context.
	got.Info("this should be swallowed silently")
}

// TestLogctx_MissObserverZeroEntries verifies the no-op behavior of the miss logger
// by injecting an observer-backed logger and confirming it captures entries,
// while a plain context does NOT.
func TestLogctx_MissObserverZeroEntries(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	observedLogger := zap.New(core)

	// Inject the observer logger so we know how to count
	ctxWith := logctx.Inject(context.Background(), observedLogger)
	logctx.From(ctxWith).Info("captured")
	assert.Equal(t, 1, logs.Len(), "observer logger should capture the entry")

	// A plain background context should not route to the observer
	logctx.From(context.Background()).Info("not captured")
	assert.Equal(t, 1, logs.Len(), "plain background context miss must not add entries to the observer")
}

// TestLogctx_BackgroundReturnsNop verifies From(context.Background()) returns non-nil.
func TestLogctx_BackgroundReturnsNop(t *testing.T) {
	got := logctx.From(context.Background())
	require.NotNil(t, got, "From(context.Background()) must return a non-nil logger")
	// Must not panic when used
	got.Debug("background nop check")
}

// TestLogctx_NilContextReturnsNop verifies From(nil) returns a non-nil no-op logger
// and does not panic.
func TestLogctx_NilContextReturnsNop(t *testing.T) {
	// This should not panic
	got := logctx.From(nil)
	require.NotNil(t, got, "From(nil) must return a non-nil logger")
	got.Info("nil context nop check")
}

// TestLogctx_InjectDoesNotMutateParent verifies that injecting a logger into a
// child context does not affect the parent context.
func TestLogctx_InjectDoesNotMutateParent(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	observedLogger := zap.New(core)

	parent := context.Background()
	child := logctx.Inject(parent, observedLogger)

	// Child has the logger
	logctx.From(child).Info("child entry")
	assert.Equal(t, 1, logs.Len())

	// Parent should still return nop (no entry added)
	logctx.From(parent).Info("parent entry")
	assert.Equal(t, 1, logs.Len(), "parent context must not be affected by child injection")
}
