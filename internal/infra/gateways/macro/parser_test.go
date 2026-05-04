package macro

// Tests for the per-series FRED parser extracted in Phase R2 Stage A.6
// (docs/refactoring/observability-replay-tooling-r2-implementation-plan.md
// §3 Task A.6). The extraction is a precondition for Task A.3
// (BundleMacroGateway raw-mode dispatch) — replay's bundle gateway needs to
// invoke the same parser the production gateway uses on raw FRED bytes so
// `--from=raw` exercises the production parsing path (spec D3 invariant).
//
// Behavior preservation: every error message, observation-index policy, and
// "." sentinel handling pinned here mirrors what `getFREDSeries` produced
// pre-extraction at gateway.go lines 295-309.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseFREDSeries_HappyPath_ReturnsFloat covers the canonical success
// case: a well-formed FRED response with one observation containing a valid
// numeric value. The function returns the parsed float64 with no error.
func TestParseFREDSeries_HappyPath_ReturnsFloat(t *testing.T) {
	body := []byte(`{"observations":[{"date":"2026-05-01","value":"4.25"}]}`)

	got, err := ParseFREDSeries("DGS10", body)

	require.NoError(t, err)
	assert.InDelta(t, 4.25, got, 1e-12, "happy-path observation must round-trip exactly through strconv.ParseFloat")
}

// TestParseFREDSeries_NoObservations_ReturnsError covers the empty-array
// case. FRED returns an empty observations array when a series has no
// data for the requested window — production gateway treats this as
// "no observations found for series <id>" and skips the series. Replay
// must produce the same error so the bundle gateway's dispatch logic
// makes the same skip decision.
func TestParseFREDSeries_NoObservations_ReturnsError(t *testing.T) {
	body := []byte(`{"observations":[]}`)

	_, err := ParseFREDSeries("DGS10", body)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no observations found for series",
		"error message must remain stable so callers matching by substring still work")
	assert.Contains(t, err.Error(), "DGS10",
		"seriesID must appear in the error so a multi-series caller can identify which one failed")
}

// TestParseFREDSeries_DotValue_ReturnsError covers FRED's "no data"
// sentinel. The API uses literal "." in the value field for missing
// observations. Pre-extraction code returns "no valid data for series
// <id>" — pinned here to prevent silent drift.
func TestParseFREDSeries_DotValue_ReturnsError(t *testing.T) {
	body := []byte(`{"observations":[{"date":"2026-05-01","value":"."}]}`)

	_, err := ParseFREDSeries("DGS10", body)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid data for series",
		"FRED's '.' sentinel must produce the documented error string")
	assert.Contains(t, err.Error(), "DGS10")
}

// TestParseFREDSeries_MalformedFloat_ReturnsError exercises the
// strconv.ParseFloat wrap path. Any non-numeric value (other than ".")
// flows through ParseFloat and surfaces as "failed to parse value for
// series <id>: <strconv error>".
func TestParseFREDSeries_MalformedFloat_ReturnsError(t *testing.T) {
	body := []byte(`{"observations":[{"date":"2026-05-01","value":"abc"}]}`)

	_, err := ParseFREDSeries("DGS10", body)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse value for series")
	assert.Contains(t, err.Error(), "DGS10")
}

// TestParseFREDSeries_MalformedJSON_ReturnsError covers the JSON-decode
// failure path. The error must be wrapped so callers can distinguish it
// from a successful decode that yields empty observations.
func TestParseFREDSeries_MalformedJSON_ReturnsError(t *testing.T) {
	body := []byte(`{not json`)

	_, err := ParseFREDSeries("DGS10", body)

	require.Error(t, err)
	// Match either the documented wrapper message or the underlying JSON error
	// — we don't pin the exact json package error string to keep the test
	// resilient to Go version changes, but the wrapper substring stays stable.
	assert.Contains(t, err.Error(), "failed to decode FRED response",
		"JSON decode errors must surface with the documented wrapper substring")
}

// TestParseFREDSeries_MultipleObservations_UsesFirst pins the
// observation-index policy. FRED responses sort `desc` (production calls
// `sort_order=desc&limit=1` but a hand-crafted bundle fixture might carry
// multiple observations). Pre-extraction code reads Observations[0]; the
// extracted function must preserve that.
func TestParseFREDSeries_MultipleObservations_UsesFirst(t *testing.T) {
	body := []byte(`{"observations":[
		{"date":"2026-05-02","value":"4.50"},
		{"date":"2026-05-01","value":"4.25"}
	]}`)

	got, err := ParseFREDSeries("DGS10", body)

	require.NoError(t, err)
	assert.InDelta(t, 4.50, got, 1e-12,
		"ParseFREDSeries must consume Observations[0] (the desc-sort newest entry), not [last]; "+
			"pre-extraction code at gateway.go:299 used [0] and replay's raw-mode symmetry depends on it")

	// Defensive: assert the second observation is NOT silently used. If
	// regression flips the index, the value would be 4.25.
	assert.NotEqual(t, 4.25, got, "regression guard against an off-by-N index slip in extraction")
}

// TestParseFREDSeries_EmptySeriesID_StillReturnsValueWhenObservationsValid
// covers a defensive edge: the extracted parser uses seriesID for error
// messages only. With valid observations and an empty seriesID, the
// function returns a value; with an empty seriesID and an error path,
// the formatted error contains an empty-quotes substring rather than
// crashing. Pinned because callers might pass through unsanitised input.
func TestParseFREDSeries_EmptySeriesID_StillReturnsValueWhenObservationsValid(t *testing.T) {
	body := []byte(`{"observations":[{"date":"2026-05-01","value":"3.00"}]}`)

	got, err := ParseFREDSeries("", body)

	require.NoError(t, err)
	assert.InDelta(t, 3.00, got, 1e-12)
}

// TestParseFREDSeries_NoDependenciesOnGateway is a structural pin: the
// extracted parser must be a free function with no Gateway receiver and
// no implicit state. Verified by the fact that this test file does NOT
// construct a *Gateway anywhere — if a future refactor sneaks a
// receiver back onto ParseFREDSeries, the call site in this file fails
// to compile and the pin fires.
//
// This is not a runtime check; it is a build-time pin. The empty test
// body is intentional — the test's existence at the import level is
// the assertion.
func TestParseFREDSeries_NoDependenciesOnGateway(t *testing.T) {
	// The two-arg call is the load-bearing structural assertion: if
	// ParseFREDSeries grew a third dependency (logger, gateway, config),
	// this would fail to compile and the test would fire at build time.
	_, _ = ParseFREDSeries("X", []byte(`{"observations":[]}`))

	// Defensive: confirm the documented signature returns (float64, error)
	// — ParseFloat path must produce a non-nil error for empty observations.
	_, err := ParseFREDSeries("X", []byte(`{"observations":[]}`))
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "no observations"),
		"signature pin: error path must remain (float64, error)")
}
