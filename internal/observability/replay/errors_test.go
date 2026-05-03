package replay

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// TestErrBundleMissingPayload_IsSentinelMatchable pins the public contract:
// callers can match on the package-level sentinel via errors.Is, regardless
// of whether the underlying error is the bare sentinel or the rich struct.
// R2's coordinator-goroutine error path depends on this.
func TestErrBundleMissingPayload_IsSentinelMatchable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"bare sentinel", ErrBundleMissingPayload, true},
		{"rich struct, no cause", NewBundleMissingPayloadError("/bundles/x", "05-fetch-sec.raw.json", nil), true},
		{"rich struct, fs cause", NewBundleMissingPayloadError("/bundles/x", "05-fetch-sec.raw.json", fs.ErrNotExist), true},
		{"unrelated error", errors.New("network down"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errors.Is(tt.err, ErrBundleMissingPayload)
			if got != tt.want {
				t.Fatalf("errors.Is(%v, ErrBundleMissingPayload) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestBundleMissingPayloadError_ErrorString verifies the human-readable
// format includes both bundle and relative paths. This is informational —
// the format is documented as stable but is not a machine contract; callers
// match via errors.Is.
func TestBundleMissingPayloadError_ErrorString(t *testing.T) {
	t.Run("without cause", func(t *testing.T) {
		err := NewBundleMissingPayloadError("/tmp/bundle", "05-fetch-sec.raw.json", nil)
		msg := err.Error()
		// Both pieces of context must appear.
		if !strings.Contains(msg, "/tmp/bundle") {
			t.Errorf("Error() should include bundle path; got: %s", msg)
		}
		if !strings.Contains(msg, "05-fetch-sec.raw.json") {
			t.Errorf("Error() should include relative path; got: %s", msg)
		}
	})

	t.Run("with cause", func(t *testing.T) {
		err := NewBundleMissingPayloadError("/tmp/bundle", "05-fetch-sec.raw.json", fs.ErrNotExist)
		msg := err.Error()
		if !strings.Contains(msg, "file does not exist") {
			t.Errorf("Error() should include cause; got: %s", msg)
		}
	})
}

// TestBundleMissingPayloadError_AsExtractsFields confirms callers can use
// errors.As to recover the rich struct and inspect BundlePath /
// RelativePath, which is how text/JSON renderers will format the
// diagnostic in batch output.
func TestBundleMissingPayloadError_AsExtractsFields(t *testing.T) {
	original := NewBundleMissingPayloadError("/bundles/AAPL/req_x", "06-fetch-market.raw.json", nil)
	// Wrap once to simulate the gateway -> coordinator -> orchestrator path.
	wrapped := errors.Join(errors.New("coordinator: child gateway failed"), original)

	var bmp *BundleMissingPayloadError
	if !errors.As(wrapped, &bmp) {
		t.Fatalf("errors.As should recover the rich struct from a wrapped error")
	}
	if bmp.BundlePath != "/bundles/AAPL/req_x" {
		t.Errorf("BundlePath = %q, want %q", bmp.BundlePath, "/bundles/AAPL/req_x")
	}
	if bmp.RelativePath != "06-fetch-market.raw.json" {
		t.Errorf("RelativePath = %q, want %q", bmp.RelativePath, "06-fetch-market.raw.json")
	}
}

// TestBundleMissingPayloadError_UnwrapReturnsCause locks the corrected
// unwrap contract: Unwrap() yields the underlying os/io error (Cause) so
// errors.Is(err, fs.ErrNotExist) succeeds. The sentinel-match path is now
// served by Is(), not Unwrap, per the Go errors invariant that an error's
// chain must terminate at the underlying root cause.
//
// Before fix (R1 follow-up #2): Unwrap returned the package sentinel,
// breaking errors.Is(err, fs.ErrNotExist) even when Cause = fs.ErrNotExist.
// After fix: Unwrap → Cause; Is(target) handles the sentinel match.
func TestBundleMissingPayloadError_UnwrapReturnsCause(t *testing.T) {
	t.Run("with cause", func(t *testing.T) {
		err := NewBundleMissingPayloadError("/x", "y", fs.ErrNotExist)
		unwrapped := errors.Unwrap(err)
		if unwrapped != fs.ErrNotExist {
			t.Fatalf("Unwrap() = %v, want fs.ErrNotExist", unwrapped)
		}
	})
	t.Run("without cause", func(t *testing.T) {
		err := NewBundleMissingPayloadError("/x", "y", nil)
		unwrapped := errors.Unwrap(err)
		if unwrapped != nil {
			t.Fatalf("Unwrap() = %v, want nil for no-cause case", unwrapped)
		}
	})
}

// TestBundleMissingPayloadError_IsMatchesCause is the new contract: a
// rich BundleMissingPayloadError wrapping fs.ErrNotExist must satisfy
// BOTH errors.Is(err, ErrBundleMissingPayload) (via the Is method) AND
// errors.Is(err, fs.ErrNotExist) (via Unwrap → Cause).
//
// This is the canonical idiom for a sentinel-class error that also wraps
// a stdlib error: Is(target) handles the package-internal sentinel
// matching; Unwrap exposes the underlying root cause for stdlib matching.
// The previous implementation broke the second leg by returning the
// sentinel from Unwrap, so callers that wanted to special-case
// "file-not-found" errors uniformly across the codebase couldn't.
func TestBundleMissingPayloadError_IsMatchesCause(t *testing.T) {
	err := NewBundleMissingPayloadError("/bundles/x", "05-fetch-sec.raw.json", fs.ErrNotExist)

	if !errors.Is(err, ErrBundleMissingPayload) {
		t.Errorf("errors.Is(err, ErrBundleMissingPayload) should be true (sentinel match via Is method)")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) should be true (cause match via Unwrap chain) — the fix routes Unwrap through Cause so stdlib sentinels are reachable")
	}
}
