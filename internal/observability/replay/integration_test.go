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
	"github.com/midas/dcf-valuation-api/internal/core/ports"
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

// seedFullBundle_ParsedMode writes a fully populated bundle directory
// shaped for the --from=parsed gateway dispatch path. Like
// seedFullBundle but emits the *.parsed.json projections of the
// production parser output (the post-parse domain types) so bundle
// gateways read the snapshot directly without re-running the parser.
//
// Files emitted:
//   - 00-manifest.json (with current schema_versions, identical to raw mode)
//   - 05-fetch-sec.raw.json — STILL needed because BundleSECGateway.
//     GetFinancialDataForTicker reads `secRawFile` unconditionally
//     regardless of Mode. The .parsed.json sibling is consumed only by
//     GetCompanyFacts which is a different code path.
//   - 06-fetch-market.parsed.json (ports.YFinanceQuote) — consumed by
//     BundleYFinanceGateway.GetQuote in ModeParsed.
//   - 07-fetch-macro.parsed.json (entities.TreasuryRates) — consumed
//     by BundleMacroGateway.GetTreasuryRates in ModeParsed.
//
// The shapes come from the production producers:
//   - sec/client.go:184 — b.Snapshot("05-fetch-sec.parsed.json", facts)
//     where facts is *ports.SECCompanyFacts. The replay-side reader in
//     gateway_sec.go:160 unmarshal's into entities.CompanyFactsResponse,
//     which is structurally compatible because both share the {CIK,
//     EntityName, Facts} JSON keys at the top level.
//   - market/yfinance_client.go:151 — b.Snapshot("06-fetch-market.parsed.json", &quote)
//     where quote is ports.YFinanceQuote.
//   - macro/gateway.go:115/131 — b.Snapshot("07-fetch-macro.parsed.json", rates)
//     where rates is *entities.TreasuryRates.
//
// Self-referential limitation (mirror of seedFullBundle): both halves of
// the round-trip test consume the same buildFairValueResponse helper.
// The test asserts replay is deterministic against itself; functional
// "parsed-mode reproduces production exactly" coverage comes from the
// per-gateway unit tests and the cross-year regression test.
//
// R3b plan §3 Stage M.3 — closes the RPL-2b gap that R3a deferred.
func seedFullBundle_ParsedMode(t *testing.T, ticker, manifestStartedAt string) string {
	t.Helper()
	tmpDir := t.TempDir()

	mf := artifact.Manifest{
		BundleVersion:  "1.0",
		RequestID:      "req_integration_parsed_" + ticker,
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

	// SEC: raw is required even in parsed mode (see helper doc-comment).
	seedBundleFile(t, tmpDir, secRawFile, makeMinimalSECRaw(t))

	// Market parsed: ports.YFinanceQuote shape — same fixture values as
	// makeMarketRaw so cross-mode determinism holds.
	quote := ports.YFinanceQuote{
		Symbol:               ticker,
		RegularMarketPrice:   190.0,
		MarketCap:            3.0e12,
		SharesOutstanding:    1.5e10,
		RegularMarketVolume:  5.5e7,
		AverageDailyVolume3M: 6.0e7,
		Beta:                 1.25,
		Currency:             "USD",
		MarketState:          "REGULAR",
		RegularMarketTime:    1700000000,
	}
	mb, err := json.Marshal(&quote)
	if err != nil {
		t.Fatalf("marshal market parsed: %v", err)
	}
	seedBundleFile(t, tmpDir, marketParsedFile, mb)

	// Macro parsed: entities.TreasuryRates shape, values mirror
	// makeFREDObsRaw constants (DGS10=4.25, DGS5=3.75, DGS2=3.50; the
	// gateway divides by 100 so production rate is 0.0425 etc).
	rates := entities.TreasuryRates{
		Yield10Year: 0.0425,
		Yield5Year:  0.0375,
		Yield2Year:  0.0350,
	}
	rb, err := json.Marshal(&rates)
	if err != nil {
		t.Fatalf("marshal macro parsed: %v", err)
	}
	seedBundleFile(t, tmpDir, macroParsedFile, rb)

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
		Module(bundleDir, Options{Mode: mode, ManifestStartedAt: manifestStartedAt, Ticker: ticker}),
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

// TestRoundTrip_ReplaySelfConsistency_ZeroDiffs is the headline R2
// integration test (plan §3 Stage F task F.1).
//
// Strategy: seed a complete bundle, run the engine once to capture the
// canonical response into 17-response.json, then run Replay() — which
// runs the engine a SECOND time and diffs against the captured response.
// Both runs are deterministic (clock pinned to manifest.started_at, no
// network I/O), so the diff is expected to be zero.
//
// Limitation (RPL-2a, R3 Stage M.2 doc): both halves of the round-trip
// use the same buildFairValueResponse helper. A bug in that helper
// would pass this test silently because both sides invoke the same
// buggy projection. Functional "replay reproduces production exactly"
// coverage comes from the cross-year regression test
// (TestReplay_CrossYearProducesByteIdenticalOutput) and the JSON
// golden tests planned for Stage M.1. The honest name for THIS test is
// "ReplaySelfConsistency" — it asserts replay is deterministic against
// itself.
//
// This test exercises:
//   - Stage A bundle gateways (SEC raw → parser, Market raw, Macro raw)
//   - Stage B NotFound repos (cache miss → engine consults gateways)
//   - Stage C fx Module composition (no DB/Redis side effects)
//   - Stage D Replay() orchestrator (manifest read, schema check, engine,
//     response render, response diff)
func TestRoundTrip_ReplaySelfConsistency_ZeroDiffs(t *testing.T) {
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

// TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs is the
// parsed-mode counterpart to TestRoundTrip_ReplaySelfConsistency_
// ZeroDiffs. R3a-BACKEND-2 left this gap because seedFullBundle was
// raw-mode only (RPL-2b deferral note). R3b Stage M.3 adds the
// parsed-mode fixture builder (seedFullBundle_ParsedMode) which closes
// the gap.
//
// Strategy:
//  1. Seed a complete parsed-mode bundle (raw SEC payload still required
//     because BundleSECGateway.GetFinancialDataForTicker reads raw
//     unconditionally; market+macro use *.parsed.json).
//  2. First engine run captures the canonical response under ModeParsed
//     into 17-response.json.
//  3. Second engine run via Replay() with ModeParsed produces zero diffs.
//
// Self-referential limitation (mirrors the raw-mode test): both halves
// use the same buildFairValueResponse helper. The test asserts replay
// is deterministic against itself in parsed mode; "parsed-mode reproduces
// production exactly" coverage comes from the per-gateway unit tests
// (gateway_*_test.go::ParsedMode_*) and the cross-year regression test.
func TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle_ParsedMode(t, ticker, startedAt)

	// First engine run under ModeParsed — captures the canonical
	// response.
	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeParsed, nil)
	if firstResp == nil {
		t.Fatalf("first engine run produced nil response")
	}
	writeResponseFile(t, bundleDir, firstResp)

	// Second engine run via Replay() — should produce zero diffs.
	res := Replay(context.Background(), bundleDir, Options{Mode: ModeParsed})
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

// TestRun_DiffStages_PopulatesStageDiffsField verifies Stage K's
// engine wiring populates Result.StageDiffs when Options.DiffStages is
// true. Setup mirrors the round-trip happy path; the assertion is that
// at least one stage entry is present after the run, since the engine
// writes `13-wacc.json` unconditionally on the DCF path. R3b Stage K.
func TestRun_DiffStages_PopulatesStageDiffsField(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)

	// Capture canonical response so the response-level diff is clean.
	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	writeResponseFile(t, bundleDir, firstResp)

	// Replay with DiffStages enabled.
	res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw, DiffStages: true})
	if res.Status == StatusErrored {
		t.Fatalf("Replay returned Errored: %s", res.Error)
	}
	if res.StageDiffs == nil {
		t.Fatalf("StageDiffs nil with DiffStages=true; want non-nil map")
	}
	// 13-wacc.json is the load-bearing entry — always written by the DCF
	// path (the only model path the AAPL fixture exercises).
	if _, ok := res.StageDiffs["13-wacc.json"]; !ok {
		t.Fatalf("StageDiffs missing 13-wacc.json key; got keys %v", stageDiffKeys(res.StageDiffs))
	}
	// 13-wacc.json should ALSO be in the bundle (because the test
	// fixture's first engine run wrote it via the same Snapshot path).
	// However, our seedFullBundle does NOT write stage files — only
	// raw fetches. So the 13-wacc.json bundle side IS missing, and the
	// diff path emits a `bundle_missing` asymmetric marker. Pin that.
	bw, ok := res.StageDiffs["13-wacc.json"]
	if !ok {
		t.Fatal("13-wacc.json absent from StageDiffs")
	}
	foundMissingMarker := false
	for _, sd := range bw.Strings {
		if sd.Path == "stages.13-wacc.json.bundle_missing" {
			foundMissingMarker = true
			break
		}
	}
	if !foundMissingMarker {
		// The fixture writes raw fetches but not stage snapshots; the
		// engine produces 13-wacc.json on the DCF path. Asymmetric
		// marker is the expected outcome.
		t.Fatalf("expected `bundle_missing` marker for 13-wacc.json (fixture has no stage files); got %+v", bw)
	}
}

// TestRun_DiffStages_DisabledByDefault_ZeroStageDiffs verifies that
// without the flag, Result.StageDiffs is nil. Catches a regression
// where DiffStages defaulted to true and would silently slow every
// replay.
func TestRun_DiffStages_DisabledByDefault_ZeroStageDiffs(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)
	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	writeResponseFile(t, bundleDir, firstResp)

	res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw}) // DiffStages omitted = false
	if res.StageDiffs != nil {
		t.Fatalf("StageDiffs = %+v; want nil with DiffStages omitted", res.StageDiffs)
	}
}

// stageDiffKeys returns sorted keys of a StageDiff map for diagnostic
// printing in test failures.
func stageDiffKeys(m map[string]StageDiff) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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

// scrubTimestamps zeroes the wall-clock-echo fields on a
// *ValuationResult so cross-year comparison can focus on math. The
// scrubbed fields are the WALL-CLOCK echoes — not derived math from
// the wall clock — so zeroing them does not affect any number that
// comparison cares about. This is the test-only helper for the D10
// regression pin; production code never zeros these.
// RPL-2l (R3 Stage O.10): reworded for clarity.
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
