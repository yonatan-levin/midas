package valuation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadADRRatios_HappyPath exercises the real, checked-in adr_ratios.json
// so we catch malformed JSON or broken keys at CI time. Asserts the canonical
// pinned tickers (TSM = 5, BABA = 8) so the snapshot file cannot silently
// regress to wrong ratios — a bad ratio here will silently corrupt every
// per-ADR valuation in Phase B10.
func TestLoadADRRatios_HappyPath(t *testing.T) {
	// Repository-relative path: tests run from the package directory, so we
	// walk up to the project root to find config/adr_ratios.json.
	path := filepath.Join("..", "..", "..", "config", "adr_ratios.json")

	ratios, err := LoadADRRatios(path)
	require.NoError(t, err)
	require.NotNil(t, ratios)
	require.NotNil(t, ratios.Ratios)

	// TSM is the canonical regression case (5 ordinary shares per ADR).
	// If this ratio drifts, every TSM valuation under-counts the share
	// base by 5x, inflating per-ADR fair value by ~5x.
	tsm, ok := ratios.Ratios["TSM"]
	require.True(t, ok, "TSM must be present in the ADR-ratio snapshot")
	assert.Equal(t, 5, tsm, "TSM ADR ratio must be pinned at 5")

	// BABA = 8 is the second canonical pin (Alibaba ordinary-to-ADR ratio).
	baba, ok := ratios.Ratios["BABA"]
	require.True(t, ok, "BABA must be present in the ADR-ratio snapshot")
	assert.Equal(t, 8, baba, "BABA ADR ratio must be pinned at 8")

	// We seed at least 13 well-known FPI tickers; if the file shrinks below
	// that, something has been deleted accidentally and Phase B10 coverage
	// for the international cohort silently degrades.
	assert.GreaterOrEqual(t, len(ratios.Ratios), 13,
		"ADR-ratio snapshot must seed at least 13 well-known FPI tickers")
}

// TestLoadADRRatios_MissingFile_ReturnsEmpty pins the non-fatal behavior on a
// missing file: the loader must return an empty (but non-nil) ADRRatios so
// the valuation service can boot in degraded mode (all tickers default to
// 1:1) without a nil-pointer panic.
func TestLoadADRRatios_MissingFile_ReturnsEmpty(t *testing.T) {
	ratios, err := LoadADRRatios("/this/path/definitely/does/not/exist/adr_ratios.json")

	require.NoError(t, err, "missing file must NOT be a hard error")
	require.NotNil(t, ratios)
	require.NotNil(t, ratios.Ratios)
	assert.Empty(t, ratios.Ratios, "missing file must yield an empty ratios map")
}

// TestLoadADRRatios_MalformedJSON_Errors pins fail-fast behavior on broken
// JSON. We cannot silently fall back to an empty map here because that would
// mask configuration drift — operators must see the parse error in logs.
func TestLoadADRRatios_MalformedJSON_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adr_ratios.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"oops"`), 0o600))

	ratios, err := LoadADRRatios(path)

	require.Error(t, err, "malformed JSON must be a hard error")
	assert.Nil(t, ratios, "ratios must be nil on parse error")
}

// TestADRRatios_Get_HappyPath pins the lookup contract: case-insensitive,
// returns the configured ratio for known tickers, and returns 1 for tickers
// that are absent (domestic 10-K filers). Empty-string is also defensively
// mapped to 1 to keep the caller free of pre-validation.
func TestADRRatios_Get_HappyPath(t *testing.T) {
	a := &ADRRatios{
		Ratios: map[string]int{
			"TSM":  5,
			"BABA": 8,
		},
	}

	assert.Equal(t, 5, a.Get("TSM"), "TSM should resolve to 5")
	assert.Equal(t, 5, a.Get("tsm"), "Get must be case-insensitive")
	assert.Equal(t, 8, a.Get("BABA"), "BABA should resolve to 8")
	assert.Equal(t, 1, a.Get("AAPL"), "absent ticker must default to 1 (domestic)")
	assert.Equal(t, 1, a.Get(""), "empty ticker must defensively return 1")
}

// TestADRRatios_Get_NilSafe pins the nil-safety contract. Phase B10 will call
// Get() on the service field without nil-checking; if the loader's degraded
// path returned nil, every Phase-B10 call site would panic. Forcing nil-safe
// Get() lets the upstream loader remain simple.
func TestADRRatios_Get_NilSafe(t *testing.T) {
	var a *ADRRatios
	assert.Equal(t, 1, a.Get("TSM"), "nil receiver must return 1, not panic")
}

// TestADRRatios_Get_ZeroRatioReturnsOne guards against bad config: if a
// reviewer accidentally seeds a 0 ratio, we treat it as missing and return
// 1 rather than dividing by zero downstream. This is a defensive belt-and-
// suspenders check on top of JSON validation.
func TestADRRatios_Get_ZeroRatioReturnsOne(t *testing.T) {
	a := &ADRRatios{
		Ratios: map[string]int{"BAD": 0},
	}
	assert.Equal(t, 1, a.Get("BAD"),
		"zero ratio must be treated as bad config and downgraded to 1")
}
