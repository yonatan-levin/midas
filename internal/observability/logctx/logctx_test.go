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
	// Typed nil (not bare nil) — exercises the nil-safety contract without
	// tripping staticcheck SA1012. The function MUST handle a nil Context
	// because middleware plumbing or missing Inject calls can leave one.
	var nilCtx context.Context //nolint:staticcheck // intentional nil for contract test
	got := logctx.From(nilCtx)
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

// TestLogctx_Or exercises the Or(ctx, fallback) helper under all contract cases.
func TestLogctx_Or(t *testing.T) {
	// Build an observer-backed "injected" logger to track its usage.
	injectedCore, injectedLogs := observer.New(zap.InfoLevel)
	injectedLogger := zap.New(injectedCore)

	// Build an observer-backed "fallback" logger to track its usage.
	fallbackCore, fallbackLogs := observer.New(zap.InfoLevel)
	fallbackLogger := zap.New(fallbackCore)

	// ctx that carries the injected logger.
	ctxWith := logctx.Inject(context.Background(), injectedLogger)

	// ctx that does NOT carry any logger.
	ctxWithout := context.Background()

	// typed nil ctx — exercises the nil-safety contract (staticcheck suppressed per convention).
	var nilCtx context.Context //nolint:staticcheck

	tests := []struct {
		name          string
		ctx           context.Context
		fallback      *zap.Logger
		wantInjected  bool // true → returned logger routes to injectedLogs
		wantFallback  bool // true → returned logger routes to fallbackLogs
		wantNop       bool // true → neither observer should see entries (nop)
		nilReturnSafe bool // returned logger must be non-nil
	}{
		{
			name:         "ctx_with_injected_logger_and_non_nil_fallback_returns_injected",
			ctx:          ctxWith,
			fallback:     fallbackLogger,
			wantInjected: true,
		},
		{
			name:         "ctx_without_logger_and_non_nil_fallback_returns_fallback",
			ctx:          ctxWithout,
			fallback:     fallbackLogger,
			wantFallback: true,
		},
		{
			name:          "ctx_without_logger_and_nil_fallback_returns_nop",
			ctx:           ctxWithout,
			fallback:      nil,
			wantNop:       true,
			nilReturnSafe: true,
		},
		{
			name:         "nil_ctx_and_non_nil_fallback_returns_fallback",
			ctx:          nilCtx,
			fallback:     fallbackLogger,
			wantFallback: true,
		},
		{
			name:          "nil_ctx_and_nil_fallback_returns_nop",
			ctx:           nilCtx,
			fallback:      nil,
			wantNop:       true,
			nilReturnSafe: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Reset observer counters before each sub-test.
			injectedLogs.TakeAll()
			fallbackLogs.TakeAll()

			got := logctx.Or(tc.ctx, tc.fallback)

			// The returned logger must never be nil.
			require.NotNil(t, got, "Or must never return nil")

			// Emit a test entry so we can count where it landed.
			got.Info("probe")

			switch {
			case tc.wantInjected:
				assert.Equal(t, 1, injectedLogs.Len(), "probe should route to injected logger")
				assert.Equal(t, 0, fallbackLogs.Len(), "probe must NOT route to fallback")
			case tc.wantFallback:
				assert.Equal(t, 0, injectedLogs.Len(), "probe must NOT route to injected logger")
				assert.Equal(t, 1, fallbackLogs.Len(), "probe should route to fallback logger")
			case tc.wantNop:
				assert.Equal(t, 0, injectedLogs.Len(), "nop must not route to injected logger")
				assert.Equal(t, 0, fallbackLogs.Len(), "nop must not route to fallback logger")
			}
		})
	}
}
