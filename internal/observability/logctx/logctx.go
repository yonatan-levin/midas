// Package logctx provides helpers for propagating a zap.Logger through a
// context.Context. This is the foundation layer for per-request structured
// logging — callers inject a request-scoped logger at the entry point and
// retrieve it deep in the call stack without explicit parameter threading.
package logctx

import (
	"context"

	"go.uber.org/zap"
)

// loggerKey is an unexported struct type used as the context key for the
// stored logger. Using a private struct type (rather than a plain string)
// prevents any external package from accidentally colliding with the same key.
type loggerKey struct{}

// Inject stores l in ctx and returns the derived child context.
// The original ctx is unchanged; the logger is only visible to the returned
// child and its descendants.
func Inject(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// From retrieves the *zap.Logger stored by Inject.
//
// Safe to call with a nil or empty context — it returns zap.NewNop() in those
// cases so callers never need to guard against a nil logger.
func From(ctx context.Context) *zap.Logger {
	// Guard against nil context to avoid a nil-pointer dereference in Value().
	if ctx == nil {
		return zap.NewNop()
	}

	if l, ok := ctx.Value(loggerKey{}).(*zap.Logger); ok && l != nil {
		return l
	}

	// Return a no-op logger when no logger is present in the context.
	// This keeps callers free of nil-checks and ensures observability is
	// opt-in: code that hasn't been wired yet simply produces no output.
	return zap.NewNop()
}

// Or returns the logger injected into ctx if present; otherwise it returns
// the provided fallback. This lets service methods that can be invoked from
// BOTH the HTTP request path (where ctx carries a request-scoped logger) and
// the scheduler / startup path (where it doesn't) emit structured logs in
// both cases — correlated when called from a request, falling back to the
// singleton logger otherwise.
//
// Intentionally does NOT delegate to From(ctx): From returns a freshly
// allocated nop logger on miss, which Or would then discard in favour of the
// caller's fallback. Inlining the lookup avoids that allocation on every call
// on the hot path and keeps the fallback branch load-bearing — a future
// refactor that rewrites this as `if l := From(ctx); ... ; return fallback`
// would silently break the semantic (From's nop would preempt the fallback).
func Or(ctx context.Context, fallback *zap.Logger) *zap.Logger {
	// If the context carries a logger, prefer it.
	if ctx != nil {
		if v := ctx.Value(loggerKey{}); v != nil {
			if l, ok := v.(*zap.Logger); ok && l != nil {
				return l
			}
		}
	}

	// Fall back; if fallback is also nil, return a nop so callers never panic.
	if fallback == nil {
		return zap.NewNop()
	}
	return fallback
}
