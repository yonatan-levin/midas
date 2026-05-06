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
	current, runErr := runEngine(ctx, bundleDir, opts, mf.Ticker)
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

	if diff.HasMismatch() {
		res.Status = StatusFail
	} else {
		res.Status = StatusPass
	}
	return res
}

// runEngine constructs the fx app, resolves *valuation.Service, and
// invokes CalculateValuation. Returns the engine output and any error.
// The fx app is started + stopped within this function — its lifetime
// does not extend past Replay's return.
func runEngine(ctx context.Context, bundleDir string, opts Options, ticker string) (*entities.ValuationResult, error) {
	var svc *valuation.Service
	app := fx.New(
		Module(bundleDir, opts),
		fx.Populate(&svc),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		return nil, fmt.Errorf("replay: fx app build: %w", err)
	}
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		return nil, fmt.Errorf("replay: fx app start: %w", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = app.Stop(stopCtx)
	}()

	if svc == nil {
		return nil, fmt.Errorf("replay: *valuation.Service was not populated")
	}

	result, err := svc.CalculateValuation(ctx, ticker, nil)
	if err != nil {
		return nil, fmt.Errorf("replay: CalculateValuation %s: %w", ticker, err)
	}
	return result, nil
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
