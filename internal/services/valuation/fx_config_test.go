package valuation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadFXRates_HappyPath exercises the real, checked-in fx_rates.json so
// we catch malformed JSON or broken keys at CI time. Asserts the canonical
// pinned currencies (USD = 1.0 exactly, TWD strictly between 0 and 1) so the
// snapshot file cannot silently regress to nonsense values.
func TestLoadFXRates_HappyPath(t *testing.T) {
	// Repository-relative path: tests run from the package directory, so we
	// walk up to the project root to find config/fx_rates.json.
	path := filepath.Join("..", "..", "..", "config", "fx_rates.json")

	rates, err := LoadFXRates(path)
	require.NoError(t, err)
	require.NotNil(t, rates)
	require.NotNil(t, rates.RatesToUSD)

	// USD is the anchor — must be exactly 1.0 so cross-rate math is symmetric.
	usd, ok := rates.RatesToUSD["USD"]
	require.True(t, ok, "USD must be present in the static FX snapshot")
	assert.Equal(t, 1.0, usd, "USD must be pinned at 1.0 exactly")

	// TWD is the canonical IFRS-FPI test case (TSMC reports in TWD). We don't
	// assert an exact value because the snapshot is refreshed periodically;
	// instead we range-check it: 1 TWD should be a small fraction of 1 USD.
	twd, ok := rates.RatesToUSD["TWD"]
	require.True(t, ok, "TWD must be present for IFRS-FPI fallback")
	assert.Greater(t, twd, 0.0, "TWD rate must be positive")
	assert.Less(t, twd, 1.0, "1 TWD must be < 1 USD")
}

// TestLoadFXRates_MissingFile_ReturnsEmpty pins the non-fatal behavior on a
// missing file: the loader must return an empty FXRates so the macro gateway
// can degrade gracefully (FRED-only mode) without a nil-pointer panic.
func TestLoadFXRates_MissingFile_ReturnsEmpty(t *testing.T) {
	rates, err := LoadFXRates("/this/path/definitely/does/not/exist/fx_rates.json")

	require.NoError(t, err, "missing file must NOT be a hard error")
	require.NotNil(t, rates)
	require.NotNil(t, rates.RatesToUSD)
	assert.Empty(t, rates.RatesToUSD, "missing file must yield an empty rates map")
}

// TestLoadFXRates_MalformedJSON_Errors pins the fail-fast behavior on broken
// JSON. We cannot silently fall back to an empty map here because that would
// mask configuration drift — operators must see the parse error in logs.
func TestLoadFXRates_MalformedJSON_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fx_rates.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"oops"`), 0o600))

	rates, err := LoadFXRates(path)

	require.Error(t, err, "malformed JSON must be a hard error")
	assert.Nil(t, rates, "rates must be nil on parse error")
}
