// Package replay implements the offline-bundle replay tooling described in
// docs/refactoring/archive/observability-replay-tooling-spec.md.
//
// Phase R1 (this commit) provides:
//   - manifest read + validate
//   - schema-version drift detection
//   - bundle directory walk
//   - float-tolerant diff helpers
//   - text + JSON output renderers
//   - extended duration parser (Go std + 'd' for days)
//   - the ErrBundleMissingPayload sentinel (used by R2 gateway stubs)
//
// Phase R2 will add: BundleSEC/Market/Macro gateways, fx Module composition,
// and the Replay() entry point that actually exercises *valuation.Service.
package replay

import (
	"errors"
	"fmt"
)

// ErrBundleMissingPayload is the sentinel returned (NOT panicked) by every
// bundle-gateway implementation when asked for a payload not present on
// disk. Required by F11 of the spec: gateways run inside
// internal/services/datafetcher/coordinator goroutines, and a child-goroutine
// panic is not recovered by main(), so the binary would crash. Returning a
// structured error keeps replay hermetic and lets the orchestration layer
// surface an "errored" Result (exit code 2).
//
// This sentinel is defined in R1 even though only R2's gateway stubs raise
// it, so the R2 implementation can `errors.Is(err, replay.ErrBundleMissingPayload)`
// without a circular import or a sentinel-relocation churn commit.
//
// The wrapping struct (BundleMissingPayloadError) carries enough context
// (bundle path + missing relative path) for the user-facing diagnostic;
// errors.Is unwraps to the package-level sentinel so callers can match on
// the type-class without depending on the exact string.
var ErrBundleMissingPayload = errors.New("replay: bundle missing required payload file")

// BundleMissingPayloadError is the rich error type returned by gateway stubs
// when a required payload file is absent. Use errors.As to extract the
// fields, or errors.Is(err, ErrBundleMissingPayload) for sentinel match.
type BundleMissingPayloadError struct {
	// BundlePath is the absolute path of the bundle directory the gateway
	// was loaded against. Useful for "which bundle was bad" diagnostics in
	// batch runs.
	BundlePath string
	// RelativePath is the file the gateway tried to read, relative to
	// BundlePath (e.g. "05-fetch-sec.raw.json"). Stable across platforms —
	// always uses forward slashes so the diagnostic matches the spec's
	// canonical naming.
	RelativePath string
	// Cause is the underlying os/io error if any, or nil if the gateway
	// detected the absence via a stat that returned os.ErrNotExist. Kept
	// optional so simpler call sites don't have to invent one.
	Cause error
}

// Error renders a single-line diagnostic suitable for stderr or a JSON
// "error" field. Format is stable but informational — callers should match
// via errors.Is, not string parsing.
func (e *BundleMissingPayloadError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("replay: bundle %q missing payload %q: %v", e.BundlePath, e.RelativePath, e.Cause)
	}
	return fmt.Sprintf("replay: bundle %q missing payload %q", e.BundlePath, e.RelativePath)
}

// Is reports whether target matches the package-level sentinel. This
// keeps errors.Is(err, ErrBundleMissingPayload) working regardless of
// whether the caller returned the bare sentinel or the rich struct.
//
// Implementing Is alongside Unwrap is the canonical Go idiom for a
// "sentinel-class error that also wraps a stdlib error": Is handles the
// package-internal sentinel match; Unwrap exposes Cause so stdlib
// sentinels (fs.ErrNotExist, io.ErrUnexpectedEOF, ...) remain reachable
// through the standard error chain.
func (e *BundleMissingPayloadError) Is(target error) bool {
	return target == ErrBundleMissingPayload
}

// Unwrap returns the underlying os/io error (Cause) so errors.Is matches
// stdlib sentinels like fs.ErrNotExist. Returns nil when no Cause was
// supplied, terminating the chain cleanly. The package-internal sentinel
// match is served by Is, not Unwrap.
//
// Before R1 follow-up #2 this returned ErrBundleMissingPayload — that
// shadowed Cause and broke errors.Is(err, fs.ErrNotExist) even when the
// caller wrapped a real fs.ErrNotExist. The fix splits the two
// responsibilities cleanly.
func (e *BundleMissingPayloadError) Unwrap() error {
	return e.Cause
}

// NewBundleMissingPayloadError is a small constructor so call sites stay
// terse. Either argument may be empty in tests — only RelativePath is
// strictly required for the error to be diagnostically useful, so we
// don't enforce non-empty here.
func NewBundleMissingPayloadError(bundlePath, relativePath string, cause error) *BundleMissingPayloadError {
	return &BundleMissingPayloadError{
		BundlePath:   bundlePath,
		RelativePath: relativePath,
		Cause:        cause,
	}
}
