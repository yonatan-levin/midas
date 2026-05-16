package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"go.uber.org/fx"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// responseFile is the canonical filename for the bundle-recorded
// FairValueResponse. Mirrors the producer in
// internal/api/v1/handlers/fair_value.go:413
// (b.Snapshot(c.Request.Context(), "response.sent", "17-response.json", &response)).
const responseFile = "17-response.json"

// Replay re-runs a single bundle through the current code and produces a
// per-bundle Result describing the outcome (pass / fail / errored).
//
// Steps (mirroring spec §6 architecture diagram):
//
//  1. Read 00-manifest.json + validate minimum-required fields.
//  2. Detect schema-version drift; refuse unless opts.AllowSchemaDrift.
//  3. Detect git-SHA drift; refuse unless opts.AllowGitDrift.
//  4. Construct an fx.App from replay.Module(bundleDir, opts) and resolve
//     *valuation.Service.
//  5. Invoke svc.CalculateValuation(ctx, manifest.Ticker, nil) — opts not
//     yet wired in R2 (the bundle's 02-handler-options.json is read in R3).
//  6. Render the *entities.ValuationResult through handlers.BuildIndustryFromResult
//     into a FairValueResponse-shaped struct mirroring the production handler
//     at fair_value.go:376-399.
//  7. Read the bundled 17-response.json and diff against the freshly
//     reconstructed response. Float fields use the package's CompareFloat
//     helper at default tolerances (R2 — Stage G upgrades to go-cmp); string
//     fields compare for equality.
//  8. Stop the fx app cleanly. Return the populated Result.
//
// Errors at any stage are surfaced as a Result with Status=StatusErrored,
// not bubbled back to the caller — replay's CLI orchestrator (cmd/replay)
// wants per-bundle outcomes, not a fail-fast walk.
//
// The function is NOT goroutine-safe at the bundle level — the same
// bundleDir should not be replayed concurrently because some side effects
// (in-process metrics counters in *metrics.Service) accumulate. R3's
// per-bundle parallelism uses one fx.App per worker which is safe.
func Replay(ctx context.Context, bundleDir string, opts Options) Result {
	res := Result{Bundle: bundleDir, Status: StatusErrored}
	startWall := time.Now()
	defer func() {
		res.DurationMs = time.Since(startWall).Milliseconds()
	}()

	// Step 1: manifest read.
	mf, err := ReadManifest(bundleDir)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Ticker = mf.Ticker
	opts.ManifestStartedAt = mf.StartedAt
	// Thread the manifest ticker into Options so BundleSECGateway can
	// emit a {ticker: cik} mapping for arbitrary bundles, not just a
	// hardcoded mega-cap list. VERIFIER finding MEDIUM-1.
	opts.Ticker = mf.Ticker

	// Step 2: schema-version drift.
	driftReport := CompareManifestSchemas(mf)
	if driftReport.HasDrift() {
		res.SchemaDrift = true
		res.SchemaDriftEntries = driftReport.Entries
		if !opts.AllowSchemaDrift {
			res.Error = "schema drift detected (use --allow-schema-drift to proceed)"
			return res
		}
	}

	// Step 3: git-SHA drift. F6 invariant: empty bundle git_sha is
	// "unknown" (not drift); only fire when both have non-empty values
	// that disagree.
	currentSHA := gitSHAResolver()
	if mf.GitSHA != "" && currentSHA != "" && mf.GitSHA != currentSHA {
		res.GitDrift = true
		if !opts.AllowGitDrift {
			res.Error = fmt.Sprintf("git_sha drift: bundle=%s current=%s (use --allow-git-drift to proceed)", mf.GitSHA, currentSHA)
			return res
		}
	}

	// Step 4-5: build the fx app, resolve the service, run the engine.
	// When opts.DiffStages is set, stagesDir is a per-call temp directory
	// that the engine's `b.Snapshot(...)` calls write into; runEngine
	// removes it before returning bytes through stagesBytes. Empty
	// otherwise. Spec D7 invariant ("replay produces no bundles of
	// bundles") is preserved because stagesDir is ephemeral.
	current, stagesBytes, runErr := runEngine(ctx, bundleDir, opts, mf.Ticker)
	if runErr != nil {
		res.Error = runErr.Error()
		// Pass-through: errors.Is(...,replay.ErrBundleMissingPayload) on
		// the returned Result.Err lets the integration test assert the
		// F11 invariant. Surface a sentinel-aware wrapper here so callers
		// can match without string-parsing.
		res.errSentinel = runErr
		return res
	}

	// Step 6: render *entities.ValuationResult into a response shape.
	currentResp := buildFairValueResponse(mf.Ticker, current)

	// Step 7: read the bundle's recorded response and diff.
	bundlePath := filepath.Join(bundleDir, responseFile)
	bundleBody, readErr := os.ReadFile(bundlePath)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			res.Error = NewBundleMissingPayloadError(bundleDir, responseFile, readErr).Error()
			res.errSentinel = NewBundleMissingPayloadError(bundleDir, responseFile, readErr)
			return res
		}
		res.Error = fmt.Errorf("read recorded response: %w", readErr).Error()
		return res
	}
	var bundleResp handlers.FairValueResponse
	if err := json.Unmarshal(bundleBody, &bundleResp); err != nil {
		res.Error = fmt.Errorf("unmarshal recorded response: %w", err).Error()
		return res
	}
	// Resolve tolerances: zero is sentinel for "use default" so a caller
	// that doesn't set Options.FloatRelTol gets the historical contract.
	// R3 Stage L.2.
	relTol := opts.FloatRelTol
	if relTol == 0 {
		relTol = DefaultFloatRelTol
	}
	absTol := opts.FloatAbsTol
	if absTol == 0 {
		absTol = DefaultFloatAbsTol
	}
	diff := compareFairValueResponses(&bundleResp, currentResp, relTol, absTol)
	diff.SortDiffs()

	res.FieldsTotal = diff.FieldsTotal
	res.FieldsChanged = diff.FieldsChanged()
	res.Diffs = diff.Floats
	res.StringDiffs = diff.Strings
	res.DriftedWithinTolerance = diff.FloatsWithinTolerance

	// Stage K: per-stage JSON diff. When opts.DiffStages is true, walk the
	// canonical stage inventory and diff each pair (bundle-recorded vs
	// engine-produced). A stage diff at any inventory entry that
	// HasMismatch() promotes the overall result to StatusFail even if
	// the response-level diff was clean — because a stage-level drift IS
	// a regression signal even when the final per-share value happens to
	// round identically. Drifted-within-tolerance entries do NOT promote.
	stageMismatch := false
	if opts.DiffStages {
		res.StageDiffs = make(map[string]StageDiff, len(StageDiffInventory))
		for _, stageFile := range StageDiffInventory {
			sd := diffStage(bundleDir, stageFile, stagesBytes[stageFile], relTol, absTol)
			res.StageDiffs[stageFile] = sd
			if sd.HasMismatch() {
				stageMismatch = true
			}
		}
	}

	if diff.HasMismatch() || stageMismatch {
		res.Status = StatusFail
	} else {
		res.Status = StatusPass
	}
	return res
}

// runEngine constructs the fx app, resolves *valuation.Service, and
// invokes CalculateValuation. Returns the engine output, a per-stage-
// filename map of captured snapshot bytes (populated only when
// opts.DiffStages is true; nil otherwise), and any error. The fx app
// is started + stopped within this function — its lifetime does not
// extend past Replay's return.
//
// Stage K snapshot capture (D7 invariant — "replay produces no bundles
// of bundles"):
//
// When opts.DiffStages is true, runEngine creates an ephemeral temp
// directory, opens an artifact.Bundle pointed at it, injects the
// bundle into ctx so the engine's `b := artifact.From(ctx)` lookup
// returns it, and reads back the captured stage files from the temp
// directory before removing it. The temp directory's lifetime is
// scoped to this function — it never persists past the return.
//
// Hermeticity preserved: the temp directory lives under os.TempDir()
// (NOT under the production artifact root); the bundle's worker
// goroutine flushes synchronously via Close() before we read the
// files; RemoveAll cleans up unconditionally via defer.
func runEngine(ctx context.Context, bundleDir string, opts Options, ticker string) (*entities.ValuationResult, map[string][]byte, error) {
	var svc *valuation.Service
	app := fx.New(
		Module(bundleDir, opts),
		fx.Populate(&svc),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		return nil, nil, fmt.Errorf("replay: fx app build: %w", err)
	}
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		return nil, nil, fmt.Errorf("replay: fx app start: %w", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = app.Stop(stopCtx)
	}()

	if svc == nil {
		return nil, nil, fmt.Errorf("replay: *valuation.Service was not populated")
	}

	// Optional snapshot capture for Stage K. When enabled, an ephemeral
	// bundle is injected into ctx; the engine's per-stage `b.Snapshot`
	// calls land in stagesDir which we read + remove before return.
	stageBytes, cleanup, ctxOut, snapErr := openStageCapture(ctx, opts, ticker)
	if snapErr != nil {
		return nil, nil, snapErr
	}
	defer cleanup()

	result, err := svc.CalculateValuation(ctxOut, ticker, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("replay: CalculateValuation %s: %w", ticker, err)
	}

	// Drain captured stage snapshots (no-op when DiffStages is false).
	captured := drainStageBytes(stageBytes, opts)
	return result, captured, nil
}

// buildFairValueResponse mirrors the production handler at
// internal/api/v1/handlers/fair_value.go:376-399. Kept as a free function
// in the replay package so a future change to the handler's projection
// (e.g. a new field) shows up here as a behavior diff against the
// bundle's recorded response — the response diff is the regression
// signal.
//
// The CalculatedAt -> AsOf string conversion uses the same RFC3339-ish
// pattern the handler uses ("2006-01-02T15:04:05Z").
func buildFairValueResponse(ticker string, result *entities.ValuationResult) *handlers.FairValueResponse {
	if result == nil {
		return nil
	}
	resp := &handlers.FairValueResponse{
		Ticker:                ticker,
		WACC:                  result.WACC,
		GrowthRate:            result.GrowthRate,
		GrowthRates:           result.GrowthRates,
		GrowthSource:          result.GrowthSource,
		GrowthConfidence:      result.GrowthConfidence,
		TangibleValuePerShare: result.TangibleValuePerShare,
		DCFValuePerShare:      result.DCFValuePerShare,
		AsOf:                  result.CalculatedAt.Format("2006-01-02T15:04:05Z"),
		DataQualityScore:      result.DataQualityScore,
		DataQualityGrade:      string(result.DataQualityGrade),
		CalculationMethod:     result.CalculationMethod,
		CalculationVersion:    result.CalculationVersion,
		Warnings:              result.Warnings,
		SanityCheck:           result.SanityCheck,
		Industry:              handlers.BuildIndustryFromResult(result),
		Currency:              currencyOrUSD(result.ReportingCurrency),
		ADRRatioApplied:       result.ADRRatioApplied,
		CurrentPrice:          result.CurrentPrice,
		// Tier 2 P0b: mirror the production handler so replay diffs surface
		// any drift in the AssumptionProfile + DCF diagnostic fields. The
		// handler at internal/api/v1/handlers/fair_value.go copies these
		// from the ValuationResult; replay must do the same or it would
		// hide regressions on fields the production wire exposes.
		AssumptionProfile:     result.AssumptionProfile,
		ResolutionTrace:       result.ResolutionTrace,
		DCFHorizonYears:       result.DCFHorizonYears,
		DCFTerminalMethod:     result.DCFTerminalMethod,
		DCFTerminalPctOfEV:    result.DCFTerminalPctOfEV,
		DCFPerYearPV:          result.DCFPerYearPV,
		DCFTerminalGrowthUsed: result.DCFTerminalGrowthUsed,
	}
	return resp
}

// currencyOrUSD mirrors the handler-side helper. Kept as a free function
// here — the handler's version is unexported and importing the handlers
// package's internal helpers via reflection would be brittle.
func currencyOrUSD(s string) string {
	if s == "" {
		return "USD"
	}
	return s
}

// gitSHAResolver returns the binary's VCS revision; package-level var so
// tests can inject a deterministic resolver (VERIFIER finding LOW-1).
// Default: resolveGitSHA, which reads runtime/debug.ReadBuildInfo and
// returns the vcs.revision setting. Empty when running under
// `go test` / `go run` (no VCS stamping). Mirrors cmd/replay/main.go's
// helper of the same name.
//
// Thread-safety note (RPL-2e, R3 Stage O.3): this is a package-level
// var by design. Tests overriding it MUST NOT call t.Parallel() — a
// concurrent test could read while another writes. The current test
// usage is sequential and verified safe; documenting this constraint
// is preferred over the higher-cost refactor (passing a resolver
// closure through Options) per the project's "pragmatic, not dogmatic"
// stance on globals. If a future test needs t.Parallel(), promote
// the seam to Options.GitSHAResolver instead.
var gitSHAResolver = resolveGitSHA

// resolveGitSHA is the production git-SHA resolver. Kept exported only
// at the package-internal level (lowercase) so the package-var seam
// above can default to it without exposing a new API.
func resolveGitSHA() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// _ keeps the artifact import live — Replay does not currently consume
// artifact APIs at the orchestration layer (the bundle gateways do), but
// future code (e.g. R3's per-stage diff against intermediate snapshots)
// likely will, and the import-block stability matters for review hygiene.
var _ = artifact.ManifestVersion

// stageCaptureContext is the per-call state for Stage K's snapshot
// capture. Holds the ephemeral *artifact.Bundle, its temp directory,
// and the context that downstream engine code reads via
// `artifact.From(ctx)`. Returned from openStageCapture as a struct so
// Replay's caller can defer cleanup() without juggling individual
// pointers.
type stageCaptureState struct {
	bundle  *artifact.Bundle
	rootDir string
}

// openStageCapture opens an ephemeral artifact bundle when
// opts.DiffStages is true so the engine's `b.Snapshot(...)` calls
// land somewhere readable. When opts.DiffStages is false (the common
// case), this is a no-op that returns ctx unchanged.
//
// Returned values:
//   - state: holds the bundle pointer + root dir; nil when DiffStages
//     is false.
//   - cleanup: idempotent function the caller defers. Closes the
//     bundle (waits for the worker to drain) and removes the temp dir.
//   - ctxOut: the (possibly bundle-injected) context to pass downstream.
//   - err: only non-nil when bundle construction itself fails (rare;
//     mostly an OS error case).
func openStageCapture(ctx context.Context, opts Options, ticker string) (*stageCaptureState, func(), context.Context, error) {
	if !opts.DiffStages {
		return nil, func() {}, ctx, nil
	}

	rootDir, err := os.MkdirTemp("", "replay-stages-*")
	if err != nil {
		return nil, func() {}, ctx, fmt.Errorf("replay: open stage capture: %w", err)
	}

	cfg := artifact.Config{
		Enabled:  true,
		RootPath: rootDir,
	}
	requestID := fmt.Sprintf("replay-%s-%d", ticker, time.Now().UnixNano())
	// TriggerHeader is the manual-diagnostic trigger; the closest match
	// for replay's "produce snapshots for offline diff". Manifest stamp
	// is academic since the temp-dir bundle is removed before return —
	// no operator ever inspects it.
	b, err := artifact.OpenBundle(cfg, requestID, ticker, artifact.TriggerHeader)
	if err != nil {
		_ = os.RemoveAll(rootDir)
		return nil, func() {}, ctx, fmt.Errorf("replay: open stage bundle: %w", err)
	}
	if b == nil {
		// OpenBundle returns nil when cfg.Enabled is false — defensive
		// fallback in case the API contract changes.
		_ = os.RemoveAll(rootDir)
		return nil, func() {}, ctx, fmt.Errorf("replay: stage bundle was nil")
	}

	state := &stageCaptureState{bundle: b, rootDir: rootDir}
	cleanup := func() {
		// Close drains the worker goroutine and synchronously flushes
		// pending snapshots to disk. ALL reads in drainStageBytes
		// happen AFTER cleanup is deferred but BEFORE the deferred
		// fires, so we cannot call cleanup eagerly here. Caller is
		// responsible for ordering: drain first, then defer fires.
		_ = b.Close()
		_ = os.RemoveAll(rootDir)
	}
	ctxOut := artifact.Inject(ctx, b)
	return state, cleanup, ctxOut, nil
}

// drainStageBytes reads the captured stage files back from the
// ephemeral bundle's root directory. Returns nil when state is nil
// (DiffStages was false). Errors on individual files are non-fatal —
// they manifest downstream as `current_missing` asymmetric markers
// because diffStage observes the empty bytes.
//
// IMPORTANT: this MUST be called after b.Close() drains the worker, so
// the saved files are guaranteed to be on disk. The caller's deferred
// cleanup() also calls Close() — that's a no-op redundancy because
// Bundle.Close is idempotent. We close eagerly here so the read sees
// the post-flush files; the deferred close handles the directory
// removal afterward.
func drainStageBytes(state *stageCaptureState, opts Options) map[string][]byte {
	if state == nil || !opts.DiffStages {
		return nil
	}
	// Eager close so the worker has drained before we read. Idempotent
	// per Bundle.Close's contract.
	_ = state.bundle.Close()

	out := make(map[string][]byte, len(StageDiffInventory))
	// Bundle.Root() returns the per-request directory inside rootDir;
	// stage files land directly under that root.
	bundleRoot := state.bundle.Root()
	for _, stageFile := range StageDiffInventory {
		body, err := os.ReadFile(filepath.Join(bundleRoot, stageFile))
		if err != nil {
			// Missing file is the common case (engine's calculation
			// path skipped this stage — e.g. non-DCF model paths
			// don't write 15-valuation.json). Leave the entry absent
			// from `out` so diffStage's `currentAbsent` branch fires
			// and emits the asymmetric marker.
			continue
		}
		out[stageFile] = body
	}
	return out
}
