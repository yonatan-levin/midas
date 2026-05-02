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

// TestBundleMissingPayloadError_UnwrapReturnsSentinel locks the unwrap
// behavior — Unwrap() must yield the sentinel, not nil and not Cause. This
// is what makes errors.Is work uniformly across both bare and rich
// returns.
func TestBundleMissingPayloadError_UnwrapReturnsSentinel(t *testing.T) {
	err := NewBundleMissingPayloadError("/x", "y", fs.ErrNotExist)
	unwrapped := errors.Unwrap(err)
	if unwrapped != ErrBundleMissingPayload {
		t.Fatalf("Unwrap() = %v, want ErrBundleMissingPayload", unwrapped)
	}
}
