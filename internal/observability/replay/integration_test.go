package replay

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// seedFullBundle writes a fully populated bundle directory containing:
//   - 00-manifest.json (with current schema_versions)
//   - 05-fetch-sec.raw.json (minimal AAPL company facts)
//   - 06-fetch-market.raw.json (minimal Yahoo quote envelope)
//   - 07-fetch-macro-DGS10.raw.json + DGS5 + DGS2 (for treasury rates)
//
// Returns the bundle directory.
//
// The bundle is rich enough that replay.Replay can drive
// *valuation.Service end-to-end. 17-response.json is intentionally NOT
// written here — the test captures the engine's first run as the
// canonical response and writes it before the second replay.
func seedFullBundle(t *testing.T, ticker, manifestStartedAt string) string {
	t.Helper()
	tmpDir := t.TempDir()

	mf := artifact.Manifest{
		BundleVersion:  "1.0",
		RequestID:      "req_integration_" + ticker,
		Ticker:         ticker,
		Trigger:        "header",
		StartedAt:      manifestStartedAt,
		Outcome:        "ok",
		SchemaVersions: map[string]int{},
	}
	for k, v := range CurrentSchemaVersions {
		mf.SchemaVersions[k] = v
	}
	body, err := json.MarshalIndent(&mf, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "00-manifest.json"), body, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	seedBundleFile(t, tmpDir, secRawFile, makeMinimalSECRaw(t))
	seedBundleFile(t, tmpDir, marketRawFile, makeMarketRaw(t, ticker))
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS10.raw.json", makeFREDObsRaw(t, "4.25"))
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS5.raw.json", makeFREDObsRaw(t, "3.75"))
	seedBundleFile(t, tmpDir, "07-fetch-macro-DGS2.raw.json", makeFREDObsRaw(t, "3.50"))

	return tmpDir
}

// runEngineForTest drives *valuation.Service to completion for a given
// bundle and returns both the *entities.ValuationResult and the
// rendered FairValueResponse. Used by integration tests to capture the
// engine's output without a full Replay.
func runEngineForTest(t *testing.T, bundleDir, ticker, manifestStartedAt string, mode Mode, clockOverride valuation.Clock) (*entities.ValuationResult, *handlers.FairValueResponse) {
	t.Helper()

	var svc *valuation.Service
	options := []fx.Option{
		Module(bundleDir, Options{Mode: mode, ManifestStartedAt: manifestStartedAt}),
		fx.Populate(&svc),
		fx.NopLogger,
	}
	if clockOverride != nil {
		options = append(options, fx.Decorate(func(_ valuation.Clock) valuation.Clock {
			return clockOverride
		}))
	}
	app := fxtest.New(t, options...)
	app.RequireStart()
	defer app.RequireStop()

	result, err := svc.CalculateValuation(context.Background(), ticker, nil)
	if err != nil {
		t.Fatalf("CalculateValuation: %v", err)
	}
	return result, buildFairValueResponse(ticker, result)
}

// writeResponseFile JSON-marshals resp and writes it to
// 17-response.json under bundleDir. Mirrors the production handler's
// b.Snapshot(...) of &response.
func writeResponseFile(t *testing.T, bundleDir string, resp *handlers.FairValueResponse) {
	t.Helper()
	body, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, responseFile), body, 0o644); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

// TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs is the headline R2
// integration test (plan §3 Stage F task F.1).
//
// Strategy: seed a complete bundle, run the engine once to capture the
// canonical response into 17-response.json, then run Replay() — which
// runs the engine a SECOND time and diffs against the captured response.
// Both runs are deterministic (clock pinned to manifest.started_at, no
// network I/O), so the diff is expected to be zero.
//
// This test exercises:
//   - Stage A bundle gateways (SEC raw → parser, Market raw, Macro raw)
//   - Stage B NotFound repos (cache miss → engine consults gateways)
//   - Stage C fx Module composition (no DB/Redis side effects)
//   - Stage D Replay() orchestrator (manifest read, schema check, engine,
//     response render, response diff)
func TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)

	// First engine run — captures the canonical response.
	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	if firstResp == nil {
		t.Fatalf("first engine run produced nil response")
	}
	writeResponseFile(t, bundleDir, firstResp)

	// Second engine run via the public Replay() entry — this is the
	// round-trip the spec calls out. Should produce zero diffs.
	res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
	if res.Status == StatusErrored {
		t.Fatalf("Replay returned Errored: %s", res.Error)
	}
	if res.Status != StatusPass {
		t.Fatalf("Status: want pass, got %s; floats=%v strings=%v", res.Status, res.Diffs, res.StringDiffs)
	}
	if res.FieldsChanged != 0 {
		t.Fatalf("FieldsChanged: want 0, got %d; floats=%v strings=%v", res.FieldsChanged, res.Diffs, res.StringDiffs)
	}
}

// TestRoundTrip_MutatedResponse_FlagsDiff verifies that mutating the
// canonical response after capture causes Replay to surface a diff.
func TestRoundTrip_MutatedResponse_FlagsDiff(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)

	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	// Mutate the DCFValuePerShare by 5% before writing — outside default tolerance.
	firstResp.DCFValuePerShare *= 1.05
	if firstResp.DCFValuePerShare == 0 {
		// Engine produced 0; mutate to a clearly-nonzero number so the
		// drift is detectable.
		firstResp.DCFValuePerShare = 100.0
	}
	writeResponseFile(t, bundleDir, firstResp)

	res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
	if res.Status == StatusErrored {
		t.Fatalf("Replay returned Errored: %s", res.Error)
	}
	if res.Status != StatusFail {
		t.Fatalf("Status: want fail (mutated dcf), got %s", res.Status)
	}
	// At least one diff entry must reference dcf_value_per_share.
	found := false
	for _, fd := range res.Diffs {
		if fd.Path == "dcf_value_per_share" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dcf_value_per_share Float diff; got %+v", res.Diffs)
	}
}

// TestRoundTrip_MissingRawSEC_ReturnsErroredViaCoordinatorGoroutine is
// the F11 invariant regression guard. Same setup as the happy round-trip,
// then we delete 05-fetch-sec.raw.json to simulate a damaged bundle. The
// engine path must surface an Errored Result through the coordinator
// goroutines WITHOUT a panic.
func TestRoundTrip_MissingRawSEC_ReturnsErroredViaCoordinatorGoroutine(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)

	// Capture a response so the missing-sec error path doesn't get
	// short-circuited by missing 17-response.json (the engine error
	// fires before the response read).
	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	writeResponseFile(t, bundleDir, firstResp)

	// Now delete the SEC raw payload. The engine's coordinator goroutine
	// should consult BundleSECGateway, get ErrBundleMissingPayload, and
	// surface that as a clean error — NOT a panic.
	if err := os.Remove(filepath.Join(bundleDir, secRawFile)); err != nil {
		t.Fatalf("remove SEC raw: %v", err)
	}

	// Wrap Replay in a recover to assert no panic.
	var recovered interface{}
	var res Result
	func() {
		defer func() { recovered = recover() }()
		res = Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
	}()
	if recovered != nil {
		t.Fatalf("Replay panicked through coordinator goroutine: %v", recovered)
	}
	if res.Status != StatusErrored {
		t.Fatalf("Status: want errored, got %s", res.Status)
	}
	// The error message must mention missing payload OR ticker not found
	// (the engine's error wrapping). Both are acceptable — the load-bearing
	// assertion is "no panic", which we already checked.
	if res.Error == "" {
		t.Fatalf("Error: expected non-empty diagnostic; got empty")
	}
	// Sentinel match isn't required because the engine's error chain
	// uses fmt.Errorf without %w in its DataFetcher path. The .Error
	// string is the stable contract for now; R3's error-chain audit
	// (RPL-1 follow-up) may tighten this.
	_ = errors.Is
}

// fixedClock is the fixture clock used by the cross-year regression test.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// TestReplay_CrossYearProducesByteIdenticalOutput is the D10 regression
// pin. Same bundle replayed under fx.Decorate-injected fixture clocks
// at 2026-06-01 vs 2027-06-01 must produce byte-identical math outputs.
//
// What gets compared:
//   - All engine MATH outputs (WACC, DCF value, growth rates, sanity
//     check multiples, freshness score, etc.) MUST be byte-identical.
//     A divergence means an unrouted time.Now() leaked into a math path.
//   - The calculated_at + financial_data_as_of + market_data_as_of
//     timestamp fields are EXPECTED to differ — they're literally the
//     Clock's read echoed back. Comparing them would assert the OPPOSITE
//     of what D10 pins (D10 says "the clock is injected"; if those fields
//     didn't differ, the Clock seam isn't actually being read).
//
// Mechanism: run the engine twice on the same bundle, each time with a
// different fixture clock injected via fx.Decorate over replay.Module.
// Decorating Clock once on top of Module's plain Provide is allowed by
// fx 1.24.0; layering two Decorates on the same type is not (which is
// why Module uses fx.Provide + the test uses fx.Decorate).
//
// Failure mode this test catches: a maintainer adds `time.Now()` in
// (e.g.) the WACC computation. With clock=2026 the call returns 2026
// stamps and the WACC rounding hashes differently than with clock=2027.
// reflect.DeepEqual on the math fields surfaces the divergence.
func TestReplay_CrossYearProducesByteIdenticalOutput(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)

	clock2026 := fixedClock{at: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
	clock2027 := fixedClock{at: time.Date(2027, 6, 1, 12, 0, 0, 0, time.UTC)}

	result2026, _ := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, clock2026)
	result2027, _ := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, clock2027)

	// Scrub the clock-derived timestamp fields BEFORE comparing — these
	// are EXPECTED to differ (they ARE the clock's value). The math
	// fields are what D10 protects.
	scrubTimestamps(result2026)
	scrubTimestamps(result2027)

	if !reflect.DeepEqual(result2026, result2027) {
		diffFields := diffValuationResults(result2026, result2027)
		t.Fatalf("cross-year math drift — D10 regression: engine path has an unrouted time.Now() leak.\nDiffering fields:\n%s", diffFields)
	}
}

// scrubTimestamps zeroes the wall-clock-derived fields on a
// *ValuationResult so cross-year comparison can focus on math. This is
// the test-only helper for the D10 regression pin — production code
// never zeros these.
func scrubTimestamps(r *entities.ValuationResult) {
	if r == nil {
		return
	}
	zero := time.Time{}
	r.CalculatedAt = zero
	r.FinancialDataAsOf = zero
	r.MarketDataAsOf = zero
}

// diffValuationResults compares two *entities.ValuationResult and returns
// a human-readable list of fields that differ. Used by the cross-year
// regression test to pinpoint the leak rather than emit a generic failure.
func diffValuationResults(a, b *entities.ValuationResult) string {
	if a == nil || b == nil {
		return "one operand is nil"
	}
	bA, _ := json.MarshalIndent(a, "", "  ")
	bB, _ := json.MarshalIndent(b, "", "  ")
	if string(bA) == string(bB) {
		return "(byte-identical when JSON-rendered; field-level reflect mismatch likely on unexported fields)"
	}
	// Walk top-level fields with a single-pass print.
	return string(bA) + "\n---vs---\n" + string(bB)
}
