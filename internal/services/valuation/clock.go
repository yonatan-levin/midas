package valuation

import "time"

// Clock is a narrow seam used by *Service to read the wall clock.
// Production binds it to wallClock (time.Now). Replay binds it to a
// manifest-bound clock that returns the bundle's started_at so cross-year
// regression replays do not silently produce different valuations purely
// because the calendar year changed.
//
// Introduced by Phase R0 of the observability replay-tooling spec
// (docs/refactoring/archive/observability-replay-tooling-spec.md §5 D10).
// Production behavior is byte-identical to a direct time.Now() call.
type Clock interface {
	Now() time.Time
}

// wallClock is the production Clock implementation. It delegates to time.Now()
// so observable behavior is bit-for-bit identical to the pre-injection code.
type wallClock struct{}

// Now returns the current wall-clock time.
func (wallClock) Now() time.Time { return time.Now() }

// NewWallClock returns the production Clock binding. Wired by fx in
// internal/di/container.go.
func NewWallClock() Clock { return wallClock{} }
