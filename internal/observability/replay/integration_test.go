package replay

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
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
	// RPL-3p (R3b cleanup): maps.Copy collapses the manual map-walk
	// into a single stdlib call (Go 1.21+).
	maps.Copy(mf.SchemaVersions, CurrentSchemaVersions)
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
	// RPL-3p (R3b cleanup): maps.Copy stdlib helper.
	maps.Copy(mf.SchemaVersions, CurrentSchemaVersions)
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

	// Wrap Replay in a recover to assert no panic. RPL-3p (R3b cleanup):
	// `any` instead of legacy `interface{}`.
	var recovered any
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

// TestReplayFidelity_FreshBundle_ZeroDiffs is the regression pin for the
// QA-identified replay-drift bug (MXL 2026-05-12, 71% DCF drop, 15 fields
// drifting). The bug had three root causes:
//
//  1. SEC submissions endpoint was never snapshotted, so SIC was lost on
//     replay → industry classifier fell back to keyword matching → wrong
//     model selection (revenue_multiple's MFG_SEMI 6.5× became generic 2.0×).
//  2. YFinance earningsTrend endpoint was never snapshotted, so analyst
//     estimates were lost on replay → growth blender flipped from
//     analyst_blend to historical_only.
//  3. Bundle's manifest schema_versions map omitted ValuationResult
//     (alt-model path didn't stamp it), surfacing a false-positive
//     schema_drift entry on every same-SHA replay of an alt-model bundle.
//
// This test seeds a complete 1.1-layout bundle including BOTH new snapshot
// files, runs the engine twice, and asserts zero diffs. If either snapshot
// is removed (regression to the pre-fix state), the engine on the second
// run will see different inputs and the diff will surface.
func TestReplayFidelity_FreshBundle_ZeroDiffs(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)

	// REVIEWER MINOR-1 (debug cycle 2): the test seeds 1.1-only payload
	// files (secSubmissionsParsedFile + analystRawFile) but the manifest
	// declared "1.0". A future bundle-version-tightening guard
	// (e.g., "1.1 manifests MUST contain submissions+analyst files") would
	// have silently passed this test even though the contract was wrong.
	// Bump to "1.1" so the test exercises the actual fresh-bundle layout.
	rewriteManifestBundleVersion(t, bundleDir, "1.1")

	// Add the 1.1-only snapshots that close the QA-identified gaps.
	// Without these, replay would see different inputs than the original
	// capture and surface a non-zero diff.
	seedBundleFile(t, bundleDir, secSubmissionsParsedFile, makeSECSubmissionsParsed(t, "3571"))
	seedBundleFile(t, bundleDir, analystRawFile, makeAnalystRaw(t))

	// First engine run — captures the canonical response into 17-response.json.
	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	if firstResp == nil {
		t.Fatalf("first engine run produced nil response")
	}
	writeResponseFile(t, bundleDir, firstResp)

	// Second engine run via Replay() — should produce zero diffs because
	// the SIC + analyst snapshots feed the same inputs back to the engine.
	res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
	if res.Status == StatusErrored {
		t.Fatalf("Replay returned Errored: %s", res.Error)
	}
	if res.FieldsChanged != 0 {
		t.Fatalf("FieldsChanged: want 0, got %d; floats=%v strings=%v",
			res.FieldsChanged, res.Diffs, res.StringDiffs)
	}
	if len(res.Diffs) != 0 {
		t.Fatalf("Diffs: want empty, got %v", res.Diffs)
	}
	if len(res.StringDiffs) != 0 {
		t.Fatalf("StringDiffs: want empty, got %v", res.StringDiffs)
	}
}

// makeSECSubmissionsParsed produces a minimal SEC submissions parsed-form
// payload — only the SIC field matters for the replay-side reader. Real
// submissions JSON has many more fields (entity name, fiscal year end,
// filing history, etc.) but the gateway decodes into struct{SIC string}.
func makeSECSubmissionsParsed(t *testing.T, sic string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{"sic": sic})
	if err != nil {
		t.Fatalf("marshal submissions parsed: %v", err)
	}
	return body
}

// rewriteManifestBundleVersion overwrites the bundle_version field on an
// existing manifest. Used by tests that need a fixture written by
// seedFullBundle (which always writes "1.0") to declare a different
// version — typically "1.1" when the test seeds 1.1-only payload files
// alongside.
//
// Implemented as a read-modify-write rather than re-marshalling the
// artifact.Manifest struct so future manifest fields (added without
// changing the test helper) propagate through untouched.
func rewriteManifestBundleVersion(t *testing.T, bundleDir, version string) {
	t.Helper()
	manifestPath := filepath.Join(bundleDir, "00-manifest.json")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest for rewrite: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	raw["bundle_version"] = version
	out, err := json.MarshalIndent(&raw, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, out, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// TestReplayFidelity_FreshBundle_AltModel_ZeroDiffs is the REVIEWER MINOR-2
// regression pin for cycle 1's Gap-3 fix (alt-model paths now stamp the
// ValuationResult bundle snapshot + schema version).
//
// The existing TestReplayFidelity_FreshBundle_ZeroDiffs only exercises the
// DCF path (AAPL SIC 3571 → MFG_TECH → DCF model). Cycle 1's Gap-3 fix
// affects the alt-model paths (revenue_multiple, FFO, DDM) where the
// service emits `15-valuation.json` separately. This test routes through
// the revenue_multiple model via SIC 3674 (semiconductors → MFG_SEMI in
// the SIC→GICS map) so the alt-model snapshot path is exercised.
//
// Pin:
//   - 17-response.json is produced by the engine (calculation_method is
//     alt-model, not "discounted_cash_flow")
//   - Replay round-trip produces FieldsChanged == 0
//
// The bundle's 15-valuation.json existence is implicitly tested: if Gap-3
// regressed and alt-model paths stopped stamping ValuationResult into the
// bundle, the manifest's schema_versions map would omit ValuationResult,
// the round-trip would emit a schema_drift entry, and the assertion below
// (Status != StatusErrored && FieldsChanged == 0) would still pass because
// schema_drift is a separate Result field. We add an explicit assertion on
// the bundle 15-valuation.json file to make that contract load-bearing.
func TestReplayFidelity_FreshBundle_AltModel_ZeroDiffs(t *testing.T) {
	const ticker = "MXLT" // synthetic ticker — alt-model fixture, not the live MXL bundle
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)
	rewriteManifestBundleVersion(t, bundleDir, "1.1")

	// SIC 3674 → MFG_SEMI → revenue_multiple model. The classifier returns
	// MFG_SEMI for SIC 3674; the router picks revenue_multiple when the
	// company has negative operating income (which the AAPL fixture does
	// NOT have — AAPL has $114B OI). We pair the SIC override with a
	// modified SEC payload that DOES have negative OI to force alt-model
	// selection. See seedAltModelSECPayload below.
	seedBundleFile(t, bundleDir, secRawFile, seedAltModelSECPayload(t))
	seedBundleFile(t, bundleDir, secSubmissionsParsedFile, makeSECSubmissionsParsed(t, "3674"))
	seedBundleFile(t, bundleDir, analystRawFile, makeAnalystRaw(t))

	firstResult, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	if firstResp == nil {
		t.Fatalf("first engine run produced nil response")
	}
	writeResponseFile(t, bundleDir, firstResp)

	// Sanity-check: confirm we actually exercised the alt-model path.
	// If the fixture accidentally routes to DCF the test would still
	// pass against zero diffs but wouldn't be pinning what we claim.
	if firstResp.CalculationMethod == "discounted_cash_flow" || firstResp.CalculationMethod == "" {
		t.Fatalf("fixture did not route to alt-model path: calculation_method=%q (want non-DCF)",
			firstResp.CalculationMethod)
	}

	// Seed the bundle's 15-valuation.json from the first engine's result.
	// In production this is written by the engine via b.Snapshot(...) when
	// an artifact bundle is in ctx; runEngineForTest doesn't wire one, so
	// we marshal firstResult here. Pairing this seed with the DiffStages
	// pin below makes a Gap-3 regression observable: when the engine's
	// alt-model path stops calling b.Snapshot, the diff layer flags
	// `stages.15-valuation.json.current_missing` because the bundle side
	// is present but the engine side is empty.
	stageBody, err := json.MarshalIndent(firstResult, "", "  ")
	if err != nil {
		t.Fatalf("marshal 15-valuation.json fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "15-valuation.json"), stageBody, 0o644); err != nil {
		t.Fatalf("write 15-valuation.json fixture: %v", err)
	}

	// Round-trip the bundle. DiffStages is enabled so the engine writes
	// per-stage snapshots into an ephemeral capture bundle, letting us
	// pin Gap-3 via the asymmetric-absence marker.
	res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw, DiffStages: true})

	// Load-bearing Gap-3 pin: the alt-model path MUST snapshot
	// 15-valuation.json alongside the DCF path. Before cycle 1's Gap-3
	// fix this snapshot was emitted only by the DCF path; without this
	// pin, a regression would still satisfy FieldsChanged == 0 because
	// schema_drift is a separate Result field and DiffStages defaults to
	// false. The pin asserts the engine-side capture is NOT marked
	// `current_missing` — i.e., the alt-model path actually produced a
	// 15-valuation.json snapshot for the diff layer to read.
	if res.StageDiffs == nil {
		t.Fatalf("StageDiffs is nil; Options.DiffStages=true should populate it")
	}
	stage := res.StageDiffs["15-valuation.json"]
	for _, sd := range stage.Strings {
		if sd.Path == "stages.15-valuation.json.current_missing" {
			t.Fatalf("alt-model path failed to snapshot 15-valuation.json (Gap-3 regression): %+v", sd)
		}
	}
	if res.Status == StatusErrored {
		t.Fatalf("Replay returned Errored: %s", res.Error)
	}
	if res.FieldsChanged != 0 {
		t.Fatalf("FieldsChanged: want 0, got %d; floats=%v strings=%v",
			res.FieldsChanged, res.Diffs, res.StringDiffs)
	}
	if len(res.Diffs) != 0 {
		t.Fatalf("Diffs: want empty, got %v", res.Diffs)
	}
	if len(res.StringDiffs) != 0 {
		t.Fatalf("StringDiffs: want empty, got %v", res.StringDiffs)
	}
}

// seedAltModelSECPayload produces a minimal SEC raw payload shaped to route
// through the alt-model (revenue_multiple) path. Differences from
// makeMinimalSECRaw:
//   - OperatingIncomeLoss is NEGATIVE (forces "negative OI → revenue_multiple"
//     selection in the model router); the standard DCF model returns
//     ErrModelNotApplicable.
//   - Revenue is still positive so revenue_multiple has a base to multiply.
//
// All values are small (millions) rather than billions so the test fixture
// isn't confused with a real-company payload. CIK 999999 is a sentinel that
// will not collide with any real filer.
func seedAltModelSECPayload(t *testing.T) []byte {
	t.Helper()
	facts := map[string]interface{}{
		"cik":        999999,
		"entityName": "Alt-Model Test Fixture Inc.",
		"facts": map[string]interface{}{
			"us-gaap": map[string]interface{}{
				"Revenues": map[string]interface{}{
					"label":       "Revenues",
					"description": "Aggregate revenue",
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   500e6,
								"end":   "2023-12-31",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2024-02-15",
								"accn":  "0000999999-23-000001",
								"frame": "CY2023",
							},
						},
					},
				},
				"OperatingIncomeLoss": map[string]interface{}{
					"label":       "Operating Income (Loss)",
					"description": "Negative — forces alt-model selection",
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   -20e6,
								"end":   "2023-12-31",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2024-02-15",
								"accn":  "0000999999-23-000001",
								"frame": "CY2023",
							},
						},
					},
				},
				"Assets": map[string]interface{}{
					"label": "Assets",
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   600e6,
								"end":   "2023-12-31",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2024-02-15",
								"accn":  "0000999999-23-000001",
								"frame": "CY2023",
							},
						},
					},
				},
				"Liabilities": map[string]interface{}{
					"label": "Liabilities",
					"units": map[string]interface{}{
						"USD": []interface{}{
							map[string]interface{}{
								"val":   300e6,
								"end":   "2023-12-31",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2024-02-15",
								"accn":  "0000999999-23-000001",
								"frame": "CY2023",
							},
						},
					},
				},
			},
			"dei": map[string]interface{}{
				"EntityCommonStockSharesOutstanding": map[string]interface{}{
					"label": "Shares outstanding",
					"units": map[string]interface{}{
						"shares": []interface{}{
							map[string]interface{}{
								"val":   80e6,
								"end":   "2023-12-31",
								"fy":    2023,
								"fp":    "FY",
								"form":  "10-K",
								"filed": "2024-02-15",
								"accn":  "0000999999-23-000001",
								"frame": "CY2023Q4I",
							},
						},
					},
				},
			},
		},
	}
	body, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("marshal alt-model raw fixture: %v", err)
	}
	return body
}

// TestReplayFidelity_MXLClassFixture_ZeroDiffs is the MAJOR-1 regression
// pin: a synthetic bundle that mimics MXL's data shape — alt-model path
// (revenue_multiple via SIC 3674) AND a blended Stage 1 growth rate that
// EXCEEDS DCFMaxGrowthRate so the cap-divergence between replay's prior
// 0.40 cap and production's 0.50 cap is exercised.
//
// Debug cycle 2 root cause: the replay-side replayConfig hardcoded
// DCFMaxGrowthRate=0.40 / DCFMinGrowthRate=-0.10 while production viper
// defaults are 0.50 / -0.30 (internal/config/config.go:504-505). For
// tickers with very high historical CAGR (MXL: 176%) the blender
// produces a Stage 1 rate > 0.40 that production caps at 0.50 but replay
// caps at 0.40 — a 0.10 absolute drop that cascades through the Stage 2
// fade interpolation, producing 9 drift fields (growth_rate, all 7
// growth_rates, data_freshness_score).
//
// Math (MXL bundle 2026-05-13):
//   - historical_cagr = 1.76 (multi-year OI ramp)
//   - analyst Y1/Y2 derived analystGrowthRate ~= 0.20
//   - 10 analysts → 80% analyst + 20% historical weights
//   - blended = 0.80*0.20 + 0.20*1.76 = 0.16 + 0.352 = 0.512
//   - sustainable_growth_rate = 0 → no blend-down
//   - production cap: stage1Rate = min(0.512, 0.50) = 0.50 ✓ (captured)
//   - prior replay cap: stage1Rate = min(0.512, 0.40) = 0.40 ✗ (-0.10 drift)
//
// Note on fixture vs. live MXL: the numerical inputs below (50M → 150M → 450M
// revenue, sign-alternating OI, 80M shares, sentinel CIK 999999, etc.) are NOT
// MXL's actual values — only the structural signature is preserved (positive
// multi-period OI ramp with a negative latest period to force revenue_multiple
// selection, 10 analysts to drive the 80/20 weighting, no "+5y" analyst entry
// so the blender derives growth from Y1→Y2, and a pre-cap blended rate well
// above 0.50). That structure is what triggers the cap divergence; specific
// magnitudes are tuned for fixture compactness, not bundle parity.
//
// Fix: replayConfig now uses 0.50 / -0.30. This test asserts the fix:
// a captured bundle whose math hits the cap should now round-trip with
// FieldsChanged == 0 instead of the prior 9.
//
// The fixture seeds:
//   - Multi-period SEC payload with high CAGR (50M → 150M → 450M revenue;
//     -20M → -10M → 50M OI alternating sign so revenue_multiple is selected)
//   - SIC 3674 → MFG_SEMI → revenue_multiple model
//   - Analyst data with NO "+5y" entry (forces Y1→Y2 revenue derivation,
//     matching MXL's actual data shape)
func TestReplayFidelity_MXLClassFixture_ZeroDiffs(t *testing.T) {
	const ticker = "MXLT" // synthetic — NOT the live MXL bundle
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)
	rewriteManifestBundleVersion(t, bundleDir, "1.1")

	// SEC payload with multi-year OI ramp producing high CAGR. Pair with
	// negative operating income for the LATEST period so revenue_multiple
	// is selected (not DCF).
	seedBundleFile(t, bundleDir, secRawFile, seedMXLClassSECPayload(t))
	seedBundleFile(t, bundleDir, secSubmissionsParsedFile, makeSECSubmissionsParsed(t, "3674"))
	// Analyst data with NO "+5y" entry — forces the blender to derive
	// analystGrowthRate from (Y2-Y1)/Y1, matching MXL's actual shape.
	seedBundleFile(t, bundleDir, analystRawFile, makeAnalystRawNoFiveYearGrowth(t))

	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	if firstResp == nil {
		t.Fatalf("first engine run produced nil response")
	}
	writeResponseFile(t, bundleDir, firstResp)

	// Confirm we actually exercised the alt-model + cap-fired path.
	if firstResp.CalculationMethod == "discounted_cash_flow" || firstResp.CalculationMethod == "" {
		t.Fatalf("fixture did not route to alt-model path: calculation_method=%q (want non-DCF)",
			firstResp.CalculationMethod)
	}
	// growth_rate is the averaged Stage1+Stage2 rate. The cap-firing
	// signature is Stage 1 == DCFMaxGrowthRate (0.50). We can't assert
	// growth_rate directly without re-implementing the average, but we
	// CAN assert the first element of growth_rates equals 0.50 (which
	// signals the cap fired AND the fix made it 0.50 not 0.40).
	if len(firstResp.GrowthRates) == 0 {
		t.Fatalf("growth_rates is empty; cannot assert cap-firing pattern")
	}
	if firstResp.GrowthRates[0] < 0.49 || firstResp.GrowthRates[0] > 0.51 {
		t.Fatalf("growth_rates[0] = %v; want ~0.50 (cap-firing signature). "+
			"If <0.49 the cap is set lower than production's 0.50 (regression of debug cycle 2 fix). "+
			"If >0.51 the cap didn't fire — fixture inputs may be too small.",
			firstResp.GrowthRates[0])
	}

	// Round-trip and assert zero diffs.
	res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
	if res.Status == StatusErrored {
		t.Fatalf("Replay returned Errored: %s", res.Error)
	}
	if res.FieldsChanged != 0 {
		t.Fatalf("MAJOR-1 regression: FieldsChanged: want 0, got %d; floats=%v strings=%v. "+
			"The replay-side DCFMaxGrowthRate likely diverges from production's 0.50 again.",
			res.FieldsChanged, res.Diffs, res.StringDiffs)
	}
}

// seedMXLClassSECPayload produces a multi-period SEC payload with a
// high-CAGR operating-income ramp so the historical CAGR exceeds the
// growth cap and the blender's output triggers the cap-firing path.
//
// Shape:
//   - 3 fiscal periods (2021FY, 2022FY, 2023FY)
//   - OI ramp: 10M → 50M → 200M (per-period growth 400%/300% — Yields
//     CAGR substantially above 0.50)
//   - Latest-period OI is set NEGATIVE (-20M) to force revenue_multiple
//     model selection; the older periods retain positive OI so
//     CalculateAverageGrowthRate has values to consume.
//
// The 4th period (latest, -20M OI) is the one the model router consults
// via GetLatestPeriod; the CAGR calculator skips negative values
// (see entities/financial_data.go:724) so it operates on the 3 positive
// periods.
func seedMXLClassSECPayload(t *testing.T) []byte {
	t.Helper()
	mkFact := func(val float64, end string, fy int, accn string) map[string]interface{} {
		return map[string]interface{}{
			"val":   val,
			"end":   end,
			"fy":    fy,
			"fp":    "FY",
			"form":  "10-K",
			"filed": end, // approximation; filed date is unused by the test path
			"accn":  accn,
			"frame": "CY" + end[:4],
		}
	}
	revenues := []interface{}{
		mkFact(80e6, "2021-12-31", 2021, "0000999998-21-000001"),
		mkFact(200e6, "2022-12-31", 2022, "0000999998-22-000001"),
		mkFact(400e6, "2023-12-31", 2023, "0000999998-23-000001"),
		// 2024 ("latest") — keeps revenue positive for revenue_multiple but
		// the model selection uses OperatingIncomeLoss sign below.
		mkFact(500e6, "2024-12-31", 2024, "0000999998-24-000001"),
	}
	ois := []interface{}{
		mkFact(10e6, "2021-12-31", 2021, "0000999998-21-000001"),
		mkFact(50e6, "2022-12-31", 2022, "0000999998-22-000001"),
		mkFact(200e6, "2023-12-31", 2023, "0000999998-23-000001"),
		// 2024 ("latest") — NEGATIVE OI forces alt-model
		mkFact(-20e6, "2024-12-31", 2024, "0000999998-24-000001"),
	}
	assetsList := []interface{}{
		mkFact(600e6, "2024-12-31", 2024, "0000999998-24-000001"),
	}
	liabilitiesList := []interface{}{
		mkFact(300e6, "2024-12-31", 2024, "0000999998-24-000001"),
	}
	sharesList := []interface{}{
		map[string]interface{}{
			"val":   80e6,
			"end":   "2024-12-31",
			"fy":    2024,
			"fp":    "FY",
			"form":  "10-K",
			"filed": "2024-12-31",
			"accn":  "0000999998-24-000001",
			"frame": "CY2024Q4I",
		},
	}
	facts := map[string]interface{}{
		"cik":        999998,
		"entityName": "MXL-Class High-CAGR Alt-Model Fixture",
		"facts": map[string]interface{}{
			"us-gaap": map[string]interface{}{
				"Revenues": map[string]interface{}{
					"label": "Revenues",
					"units": map[string]interface{}{"USD": revenues},
				},
				"OperatingIncomeLoss": map[string]interface{}{
					"label": "Operating Income (Loss)",
					"units": map[string]interface{}{"USD": ois},
				},
				"Assets": map[string]interface{}{
					"label": "Assets",
					"units": map[string]interface{}{"USD": assetsList},
				},
				"Liabilities": map[string]interface{}{
					"label": "Liabilities",
					"units": map[string]interface{}{"USD": liabilitiesList},
				},
			},
			"dei": map[string]interface{}{
				"EntityCommonStockSharesOutstanding": map[string]interface{}{
					"label": "Shares outstanding",
					"units": map[string]interface{}{"shares": sharesList},
				},
			},
		},
	}
	body, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("marshal MXL-class raw fixture: %v", err)
	}
	return body
}

// makeAnalystRawNoFiveYearGrowth produces an analyst envelope WITHOUT the
// "+5y" trend entry — forces the growth blender to derive analystGrowthRate
// from the Y1→Y2 revenue ratio (matches MXL's actual shape; see the
// captured 06-fetch-market-analyst.raw.json from req_390b3380... — its +5y
// entry is missing because Yahoo did not publish a 5-year forecast for MXL).
//
// 10 analysts → high-confidence weights (80% analyst / 20% historical).
// Y1=120M / Y2=132M → analyst growth = (132-120)/120 = 0.10.
func makeAnalystRawNoFiveYearGrowth(t *testing.T) []byte {
	t.Helper()
	mkVal := func(v float64) map[string]interface{} {
		return map[string]interface{}{"raw": v, "fmt": ""}
	}
	env := map[string]interface{}{
		"quoteSummary": map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"earningsTrend": map[string]interface{}{
						"trend": []map[string]interface{}{
							{
								"period": "0y",
								"revenueEstimate": map[string]interface{}{
									"avg":              mkVal(120e6),
									"low":              mkVal(115e6),
									"high":             mkVal(125e6),
									"numberOfAnalysts": mkVal(10),
								},
							},
							{
								"period": "+1y",
								"revenueEstimate": map[string]interface{}{
									"avg": mkVal(132e6),
								},
							},
							// NO +5y entry — matches MXL's captured shape.
						},
					},
				},
			},
			"error": nil,
		},
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal analyst raw (no +5y): %v", err)
	}
	return body
}
