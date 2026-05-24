package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/sec"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// TestDataCleanerRecompute_ShadowMode_TickerBasket runs the FULL cleaner
// pipeline against every available ticker in the DC-1 acceptance basket,
// captures every WARN line emitted by the recomputeUmbrellas shadow shim,
// and writes a per-ticker JSON snapshot to
// internal/integration/testdata/recompute-shadow/<TICKER>.json.
//
// The snapshots are committed to the repo as the Phase 1 → Phase 2 hand-off
// artifact — Phase 2's PR diff against these files surfaces every adjuster
// mutation pattern that needs to migrate to the Adjuster interface.
//
// Recording-not-asserting policy: the test does NOT assert on a specific
// divergence count for any ticker. Known clamp-fired periods (AMD 2023FY /
// KO 2023FY are the live carriers in the artifacts/tier2-baseline/2026-05-15/
// date range — both ship reported_TL=0 because of a parser-side dropout
// flagged in the shadow-analysis report; the Phase 0 closeout's historical
// MXL 2017FY / EQIX 2013Q1 citations fall outside this baseline's date
// range) WILL emit WARN lines — that is the documented Phase 0 behavior
// that Phase 1's shadow mode is built to surface. A maximum-divergence-
// count assertion would require pre-knowledge of every adjuster's
// divergence shape, which is exactly the data Phase 1 produces.
//
// The single load-bearing assertion is `passedCount >= 5` (matches Phase 0's
// floor in datacleaner_plug_invariants_test.go). This catches the all-skip
// silent regression where loadSECRawForTicker fails for every basket member
// (bundle dir moved / fixture-file naming changed) without false-positing on
// individual ticker skips for lack of captured fixtures.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
// Plan: docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md
func TestDataCleanerRecompute_ShadowMode_TickerBasket(t *testing.T) {
	// DC-1 Phase 0+1 acceptance basket per the spec. Tickers without a
	// captured bundle skip via t.Skipf — they are recorded in the test
	// output for Phase 2's analysis but don't fail CI.
	basket := []string{"AAPL", "MSFT", "JNJ", "KO", "F", "AMD", "MXL", "TSM", "BABA", "EQIX"}

	parser := sec.NewParser(zap.NewNop())

	// Resolve the newest baseline date directory under artifacts/tier2-baseline/
	// (lexicographic max on ISO date strings — same pattern as Phase 0's
	// plug-invariant test). Fails loudly if no baseline exists so a future
	// repo-layout change doesn't silently dissolve the basket.
	baselineParent, err := filepath.Abs(filepath.Join("..", "..", "artifacts", "tier2-baseline"))
	require.NoError(t, err, "resolve baseline parent")
	matches, err := filepath.Glob(filepath.Join(baselineParent, "*"))
	require.NoError(t, err, "glob tier2-baseline subdirs")
	var dateDirs []string
	for _, m := range matches {
		if info, statErr := os.Stat(m); statErr == nil && info.IsDir() {
			dateDirs = append(dateDirs, m)
		}
	}
	require.NotEmpty(t, dateDirs, "no baseline date directories under %s — capture a baseline first", baselineParent)
	sort.Strings(dateDirs)
	bundleRoot := dateDirs[len(dateDirs)-1]
	t.Logf("tier2-baseline resolved to %s (newest of %d date dirs)", filepath.Base(bundleRoot), len(dateDirs))

	// Build a real DataCleanerService — the WARN signal MUST come from the
	// recompute call site wired into the production pipeline, not from a
	// direct unit-style call. The test exercises the same path a fair-value
	// request takes.
	cfg := buildShadowTestConfig(t)
	cleanerSvc, err := datacleaner.NewDataCleanerService(cfg, ai.NewMockAIService(&ai.AIServiceConfig{}), nil)
	require.NoError(t, err, "construct DataCleanerService")

	// Ensure the snapshot directory exists. Phase 1's hand-off artifact is
	// the committed JSON files under it; the directory must be present in
	// the worktree (the .gitkeep below pins it).
	snapshotDir, err := filepath.Abs(filepath.Join("testdata", "recompute-shadow"))
	require.NoError(t, err, "resolve snapshot dir")
	require.NoError(t, os.MkdirAll(snapshotDir, 0o755), "create snapshot dir")

	var passedCount atomic.Int32

	for _, ticker := range basket {
		ticker := ticker
		t.Run(ticker, func(t *testing.T) {
			rawBytes, fixturePath, ok := loadSECRawForTicker(t, bundleRoot, ticker)
			if !ok {
				t.Skipf("no captured SEC fixture for %s under %s", ticker, bundleRoot)
				return
			}

			var facts ports.SECCompanyFacts
			require.NoError(t, json.Unmarshal(rawBytes, &facts),
				"%s: unmarshal SEC raw payload at %s", ticker, fixturePath)

			historical, err := parser.ParseFinancialData(context.Background(), &facts)
			require.NoError(t, err, "%s: parser.ParseFinancialData failed", ticker)
			require.NotNil(t, historical, "%s: parser returned nil historical", ticker)
			require.NotEmpty(t, historical.Data, "%s: parser produced no periods", ticker)

			// Per-ticker observer captures every WARN across every period
			// of this ticker's history. Each period gets its own context so
			// future per-period filtering stays available; the recorder
			// itself is shared because the JSON snapshot is per-ticker.
			core, recorded := observer.New(zap.WarnLevel)
			ctx := logctx.Inject(context.Background(), zap.New(core))

			periodsProcessed := 0
			periodsWithDivergence := map[string]bool{}

			// Sorted period walk for deterministic snapshot ordering. We
			// skip malformed period keys (empty string or pseudo-period
			// "0") because (a) they don't reproduce reliably across runs
			// — the SEC parser intermittently produces them for stub
			// filings depending on map iteration order during currency-
			// bucket collapse, and (b) Phase 2's punch list keys on
			// (ticker, period, umbrella) so a malformed period would
			// land in an un-actionable cluster anyway. Letting them
			// through would make the committed snapshots non-deterministic
			// across runs and break the diff-review signal that is the
			// whole point of committing them.
			periods := historical.GetSortedPeriods()
			for _, period := range periods {
				if period == "" || period == "0" {
					continue
				}
				fd := historical.Data[period]
				if fd == nil {
					continue
				}
				// Skip FDs whose own FilingPeriod is malformed for the
				// same reason — the WARN's `period` field flows from
				// fd.FilingPeriod and would carry the malformed value
				// through to the snapshot.
				if fd.FilingPeriod == "" || fd.FilingPeriod == "0" {
					continue
				}
				// Tag the fd's ticker so the WARN lines carry the real
				// identifier (some parsed fds leave Ticker empty). We do
				// NOT mutate any other fields — recomputeUmbrellas's
				// read-only invariant is preserved.
				if fd.Ticker == "" {
					fd.Ticker = ticker
				}

				before := recorded.Len()
				// Run the FULL cleaner pipeline — exercises the wired-in
				// recomputeUmbrellas call site, not a direct invocation.
				_, cleanErr := cleanerSvc.CleanFinancialData(ctx, fd)
				if cleanErr != nil {
					// Some periods are missing required fields (sub-period
					// stubs, currency-bucket collapse edge cases). Skip
					// them — Phase 1's signal is the WARN set, not the
					// cleaner's success rate.
					continue
				}
				periodsProcessed++

				// Mark this period as divergent if recomputeUmbrellas
				// added any WARN entries during this call.
				if recorded.Len() > before {
					periodsWithDivergence[period] = true
				}
			}

			// Collect every divergence WARN into the snapshot. The
			// observer.LoggedEntry shape gives us the structured fields
			// directly; we filter to the recompute message to ignore any
			// other WARN lines emitted by adjusters or rule evaluators.
			snapshot := shadowSnapshot{
				Ticker:                ticker,
				BundleRoot:            relativizeForSnapshot(bundleRoot),
				FixturePath:           relativizeForSnapshot(fixturePath),
				PeriodsProcessed:      periodsProcessed,
				PeriodsWithDivergence: len(periodsWithDivergence),
				Divergences:           []divergenceRecord{},
			}
			for _, e := range recorded.FilterMessage("recomputeUmbrellas: umbrella divergence").All() {
				snapshot.Divergences = append(snapshot.Divergences, divergenceFromEntry(e))
			}
			// Stable ordering for diff-friendly snapshots (period, then umbrella).
			sort.SliceStable(snapshot.Divergences, func(i, j int) bool {
				if snapshot.Divergences[i].Period != snapshot.Divergences[j].Period {
					return snapshot.Divergences[i].Period < snapshot.Divergences[j].Period
				}
				return snapshot.Divergences[i].Umbrella < snapshot.Divergences[j].Umbrella
			})

			writeShadowSnapshot(t, snapshotDir, ticker, snapshot)

			if !t.Failed() {
				passedCount.Add(1)
			}
		})
	}

	// All-skip silent-regression gate (matches Phase 0's floor).
	require.GreaterOrEqual(t, passedCount.Load(), int32(5),
		"basket coverage degraded — fewer than 5 tickers ran through the cleaner successfully (got %d)", passedCount.Load())
}

// buildShadowTestConfig produces the minimal config needed to construct a
// real DataCleanerService for the shadow basket test. Paths resolve relative
// to internal/integration/ (where this test runs) into the repo's config/.
func buildShadowTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		DataCleaner: config.DataCleanerConfig{
			RulesPath:           filepath.FromSlash("../../config/datacleaner/rules.json"),
			IndustryRulesPath:   filepath.FromSlash("../../config/datacleaner/industry"),
			SchemaPath:          filepath.FromSlash("../../config/datacleaner/schema.json"),
			Enabled:             true,
			EnableAIIntegration: false,
			MinQualityScore:     60.0,
			HighQualityScore:    85.0,
			EnableRiskFlags:     true,
			CriticalThreshold:   0.3,
			WarningThreshold:    0.15,
			MaxConcurrentRules:  10,
			EnableCaching:       false, // disable to ensure every period runs through the pipeline
			CacheTTL:            time.Hour * 6,
			EnableIndustryRules: true,
			EnableAuditTrail:    true,
			LogAdjustments:      true,
			LogFlags:            true,
		},
	}
}

// shadowSnapshot is the per-ticker JSON committed to testdata/recompute-shadow/.
// Schema is part of the Phase 1 → Phase 2 hand-off contract; renaming fields
// breaks Phase 2's analysis tooling.
type shadowSnapshot struct {
	Ticker                string             `json:"ticker"`
	BundleRoot            string             `json:"bundle_root"`
	FixturePath           string             `json:"fixture_path"`
	PeriodsProcessed      int                `json:"periods_processed"`
	PeriodsWithDivergence int                `json:"periods_with_divergence"`
	Divergences           []divergenceRecord `json:"divergences"`
}

// divergenceRecord mirrors the structured fields on the recompute WARN line.
// Field names match implementation plan §D exactly.
type divergenceRecord struct {
	Period         string  `json:"period"`
	Umbrella       string  `json:"umbrella"`
	Reported       float64 `json:"reported"`
	Recomputed     float64 `json:"recomputed"`
	Delta          float64 `json:"delta"`
	Plug           float64 `json:"plug"`
	ClampSuspected bool    `json:"clamp_suspected"`
}

// divergenceFromEntry extracts the recompute WARN fields from a single
// observer.LoggedEntry. Defensive on each cast — a missing field falls
// through as zero/empty so a future log-shape change surfaces in the diff
// rather than panicking the snapshot generator.
//
// Monetary float fields are quantized to whole dollars (roundDollar) before
// landing in the snapshot. Without quantization the cleaner's adjuster
// ordering occasionally swaps the last bit of accumulated arithmetic
// (`387402066.6666667` vs `387402066.6666666`), producing a non-stable
// diff that defeats the entire point of committing the snapshots. The
// divergence signal Phase 2 needs is measured in millions of dollars; a
// 1 USD snapshot precision is well below any actionable threshold.
func divergenceFromEntry(e observer.LoggedEntry) divergenceRecord {
	ctx := e.ContextMap()
	rec := divergenceRecord{}
	if v, ok := ctx["period"].(string); ok {
		rec.Period = v
	}
	if v, ok := ctx["umbrella"].(string); ok {
		rec.Umbrella = v
	}
	if v, ok := ctx["reported"].(float64); ok {
		rec.Reported = roundDollar(v)
	}
	if v, ok := ctx["recomputed"].(float64); ok {
		rec.Recomputed = roundDollar(v)
	}
	if v, ok := ctx["delta"].(float64); ok {
		rec.Delta = roundDollar(v)
	}
	if v, ok := ctx["plug"].(float64); ok {
		rec.Plug = roundDollar(v)
	}
	if v, ok := ctx["clamp_suspected"].(bool); ok {
		rec.ClampSuspected = v
	}
	return rec
}

// roundDollar quantizes a float USD value to the nearest whole dollar so
// committed snapshot diffs absorb cleaner-side float64 accumulation noise
// (typically a single ULP, ~$0.000001 for billion-dollar magnitudes).
// Phase 2's punch list operates on million-dollar magnitudes; whole-dollar
// snapshot precision is two orders of magnitude tighter than the smallest
// actionable signal.
func roundDollar(v float64) float64 {
	return math.Round(v)
}

// writeShadowSnapshot serializes the per-ticker snapshot to JSON with
// stable formatting (2-space indent + trailing newline) so the committed
// files diff cleanly across runs.
//
// Writes to <path>.tmp first, then renames atomically to <path>. The
// atomic-write pattern means a ctrl-C (or test-process kill) part-way
// through serialization cannot leave a half-written snapshot on disk —
// either the previous committed snapshot remains, or the new one is fully
// in place. Important because the committed snapshots are the Phase 1 →
// Phase 2 hand-off contract; a truncated file would silently break Phase 2
// reviewer diff-review.
func writeShadowSnapshot(t *testing.T, dir, ticker string, snap shadowSnapshot) {
	t.Helper()
	out, err := json.MarshalIndent(snap, "", "  ")
	require.NoError(t, err, "%s: marshal snapshot", ticker)
	out = append(out, '\n')
	path := filepath.Join(dir, ticker+".json")
	tmpPath := path + ".tmp"
	require.NoError(t, os.WriteFile(tmpPath, out, 0o644), "%s: write snapshot tmp to %s", ticker, tmpPath)
	require.NoError(t, os.Rename(tmpPath, path), "%s: rename snapshot tmp to %s", ticker, path)
}

// relativizeForSnapshot strips the absolute prefix from a path so the
// committed snapshot is portable across developer machines and CI workers
// (no /home/runner/... or C:/Users/... leakage). Falls through to the
// original path if it can't be relativized.
func relativizeForSnapshot(abs string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return abs
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	rel, err := filepath.Rel(repoRoot, abs)
	if err != nil {
		return abs
	}
	// Normalize separators so snapshots are identical on Windows and Unix.
	return filepath.ToSlash(rel)
}

// Compile-time: ensure the fmt import stays used even if a future edit
// removes the only %-format string. (Phase 0 followed the same pattern.)
var _ = fmt.Sprintf
