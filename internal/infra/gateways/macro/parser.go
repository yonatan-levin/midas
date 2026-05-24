package macro

// Pure FRED per-series parser, extracted in Phase R2 Stage A.6 of the
// observability replay tooling
// (docs/refactoring/archive/observability-replay-tooling-r2-implementation-plan.md
// §3 Task A.6).
//
// Why a pure free function and not a method on *Gateway:
//   - Spec D3 invariant: replay's `--from=raw` must "exercise the gateway
//     parser", not a replay-local re-implementation.
//     `BundleMacroGateway.GetTreasuryRates` (Stage A.3) walks each FRED
//     series file in the bundle and dispatches to this function. A free
//     function with the canonical signature makes that semantically obvious
//     at the call site (`macro.ParseFREDSeries(seriesID, body)` reads as
//     "I am invoking the production parser") and forecloses any silent
//     drift between production and replay parsing logic.
//   - Behavior preservation: every input this function consumes (the raw
//     response body) and every output it produces (the parsed float64)
//     flowed through method-local variables in the pre-extraction
//     getFREDSeries. There is no *Gateway state coupling — the extraction
//     is mechanical.
//
// Behavior (pinned by parser_test.go):
//   - Decodes the body as a FREDResponse JSON object.
//   - Returns the first observation's Value parsed via strconv.ParseFloat.
//   - "no observations found for series <id>" when Observations is empty.
//   - "no valid data for series <id>" when Observations[0].Value == "."
//     (FRED's missing-data sentinel).
//   - Wraps strconv.ParseFloat errors with "failed to parse value for
//     series <id>: <cause>".
//   - Wraps json.Decode errors with "failed to decode FRED response: <cause>".
//
// The seriesID parameter is consumed only for error messages so callers can
// identify which series failed in a multi-series fan-out (per-series tolerance
// in the production gateway: skip a failing series, continue with the rest).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// ParseFREDSeries consumes a raw FRED API response body and returns the
// numeric value of the first observation. seriesID is used only for error
// messages.
//
// Pre-extraction (gateway.go:277-309), the same logic ran inline inside
// (*Gateway).getFREDSeries after the HTTP fetch + TeeReader wiring. The
// extraction preserves byte-for-byte behavior; existing macro gateway
// integration tests pass without modification.
func ParseFREDSeries(seriesID string, body []byte) (float64, error) {
	var fredResponse FREDResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&fredResponse); err != nil {
		return 0, fmt.Errorf("failed to decode FRED response: %w", err)
	}

	if len(fredResponse.Observations) == 0 {
		return 0, fmt.Errorf("no observations found for series %s", seriesID)
	}

	observation := fredResponse.Observations[0]
	if observation.Value == "." {
		return 0, fmt.Errorf("no valid data for series %s", seriesID)
	}

	value, err := strconv.ParseFloat(observation.Value, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse value for series %s: %w", seriesID, err)
	}

	return value, nil
}
