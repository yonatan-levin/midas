package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/sec"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// TestLedger_BasketSnapshot_ClusterPrediction asserts that DC-1 Phase 2's
// native Adjuster pipeline emits the EXPECTED set of AdjusterIDs on
// data.AdjustmentLedger for each ticker in the DC-1 acceptance basket.
//
// Truth source: the committed shadow snapshots at
// internal/integration/testdata/recompute-shadow/<TICKER>.json (filed by
// Phase 1's basket-recording test) plus the cluster-mapping enumerated in
// the Phase 1 shadow analysis report
// (docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md).
// The 7-cluster punch list maps every observed (ticker, period, umbrella)
// divergence to a known cleaner-side adjuster signature. We invert that
// mapping here: per-ticker we know which adjusters MUST have fired (or at
// least been considered — see "Considered, not fired" below) across the
// 12-period bundle.
//
// Why this test is structurally different from
// TestDataCleanerRecompute_ShadowMode_TickerBasket
// ----------------------------------------------------------------------
//
// The Phase 1 recompute-shadow test records WARN-emission shape and commits
// it as a per-ticker JSON snapshot. The committed snapshots are the diff-
// reviewable hand-off contract — Phase 2's PR diffs surface adjuster
// migrations indirectly. This test (Phase 2 PR-4 Task 4.6) is the FIRST
// integration test to read data.AdjustmentLedger directly and assert on
// it. It is the regression sentinel that catches "a future refactor
// silently stopped emitting LedgerEntries for adjuster X on ticker Y"
// without depending on the WARN-line round-trip.
//
// Both tests share the same bundle-loading infrastructure (loadSECRawForTicker)
// and basket (10 tickers under artifacts/tier2-baseline/<newest>/). The
// per-ticker iteration deliberately mirrors the recompute-shadow test so
// they degrade in lockstep when a fixture goes missing.
//
// "Considered, not fired" — what we assert on
// ----------------------------------------------------------------------
//
// Phase 2's canonical Adjuster pattern emits a LedgerEntry for EVERY
// adjuster considered per period — fired entries carry Fired:true with
// Component/DeltaAmount/EquityOffset; skipped entries carry Fired:false
// with SkipReason and optionally SkipMetrics. The AdjusterID is populated
// on every entry regardless of Fired state. So the SET of AdjusterIDs
// observed on data.AdjustmentLedger after CleanFinancialData answers
// "which rules did the cleaner consider for this ticker?", not "which
// fired". This is the right granularity for a structural-regression test:
// a refactor that accidentally drops adjuster X from the dispatcher's
// switch table fails this test even if X would only have skipped on the
// fixture data.
//
// Per-ticker expected AdjusterIDs (the predictionRows table below) are
// derived from the shadow-analysis report's cluster mapping plus the
// rule-applicability predicates at service.go::checkRuleApplicability:
//
//   - B1 operating_lease_capitalization — applies to every ticker with
//     revenue > 0 (service.go:608-610: "Apply to all companies with
//     revenue data"). Expected on ALL 10 basket tickers.
//   - A1 goodwill_exclusion — applies when working.Goodwill > 0
//     (checkRuleApplicability case "goodwill_exclusion"). The shadow-
//     analysis cluster A1-A5 covers AMD/F/KO/MXL on Qx periods.
//     Cross-check: AAPL/MSFT/JNJ/EQIX/BABA/TSM all carry goodwill on
//     captured periods. Expected on ALL 10.
//   - A2 intangible_adjustment — applies when working.OtherIntangibles
//     > 0. Cleaner considers it for every ticker with non-zero
//     intangibles. Most large-cap basket members have intangibles;
//     pure pre-revenue / mature-no-acquisition tickers might not.
//     Expected on the broad basket; we don't pin it because the rule
//     applicability is data-dependent.
//   - A5 obsolete_inventory — applies when Inventory > 0 AND
//     InventoryTurnover < 6.0. Per shadow analysis Cluster A1-A5, this
//     is what drives the paired CA-down/TA-up signature on
//     AMD/F/KO/MXL Qx periods. Expected for AMD/F/KO/MXL at minimum.
//
// We assert a conservative LOWER BOUND (expectedMust) per ticker so
// fixture-side noise (a period where every rule skips) doesn't flap the
// test; the test will fail loudly if the dispatcher drops B1 entirely on
// a captured ticker, which is the regression we care about.
//
// Skip behavior for missing bundles
// ----------------------------------------------------------------------
//
// Mirrors TestDataCleanerRecompute_ShadowMode_TickerBasket: any ticker
// without a captured 05-fetch-sec.raw.json under bundleRoot/<TICKER>/
// emits t.Skipf rather than failing. The passedCount >= 5 floor catches
// the all-skip silent regression where loadSECRawForTicker fails for
// every basket member.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md (§7 Task 4.6)
func TestLedger_BasketSnapshot_ClusterPrediction(t *testing.T) {
	// DC-1 acceptance basket — same shape as Phase 1's shadow-mode test.
	// Order matches the committed shadow snapshots' filename order so
	// failure output reads as a per-ticker diff.
	basket := []string{"AAPL", "MSFT", "JNJ", "KO", "F", "AMD", "MXL", "TSM", "BABA", "EQIX"}

	// Per-ticker expected AdjusterIDs. The lower-bound contract: the
	// observed SET of AdjusterIDs on data.AdjustmentLedger after running
	// CleanFinancialData across every period of the bundle MUST be a
	// superset of expectedMust for this ticker.
	//
	// Source mapping: Phase 1 shadow-analysis report §4 (cluster
	// enumeration) inverted via service.go::checkRuleApplicability. Every
	// ticker that has revenue-bearing periods is required to have at
	// least the B1 entry because the rule applies broadly. Tickers known
	// to have material goodwill across captured periods also get A1.
	//
	// We do NOT include adjusters whose firing depends on per-period
	// state we can't predict from the bundle root alone (e.g. A5 fires
	// only when InventoryTurnover < 6.0; A2 only when intangibles > 0).
	// Asserting on those would create a brittle prediction-vs-cleaner
	// race. The expectedMust set captures the WIDE-applicability
	// adjusters whose absence would indicate a dispatcher regression,
	// not a fixture quirk.
	type predictionRow struct {
		ticker       string
		expectedMust []string
	}
	predictionRows := []predictionRow{
		// US large caps with goodwill on every captured period.
		{ticker: "AAPL", expectedMust: []string{"B1_operating_lease_capitalization"}},
		{ticker: "MSFT", expectedMust: []string{"A1_goodwill_exclusion", "B1_operating_lease_capitalization"}},
		{ticker: "JNJ", expectedMust: []string{"A1_goodwill_exclusion", "B1_operating_lease_capitalization"}},
		{ticker: "KO", expectedMust: []string{"A1_goodwill_exclusion", "B1_operating_lease_capitalization"}},
		// US industrial / discretionary with inventory + goodwill.
		{ticker: "F", expectedMust: []string{"A1_goodwill_exclusion", "B1_operating_lease_capitalization"}},
		{ticker: "AMD", expectedMust: []string{"A1_goodwill_exclusion", "B1_operating_lease_capitalization"}},
		{ticker: "MXL", expectedMust: []string{"A1_goodwill_exclusion", "B1_operating_lease_capitalization"}},
		// ADRs / IFRS filers — B1 still applies. Goodwill presence
		// varies; we don't pin A1 for the FPI cohort because their
		// XBRL goodwill mapping has historically been less reliable
		// (see CLAUDE.md DC-1 corollary).
		{ticker: "TSM", expectedMust: []string{"B1_operating_lease_capitalization"}},
		{ticker: "BABA", expectedMust: []string{"B1_operating_lease_capitalization"}},
		// REIT.
		{ticker: "EQIX", expectedMust: []string{"B1_operating_lease_capitalization"}},
	}
	predictionByTicker := map[string][]string{}
	for _, row := range predictionRows {
		predictionByTicker[row.ticker] = row.expectedMust
	}

	parser := sec.NewParser(zap.NewNop())

	// Resolve the newest baseline-date directory under tier2-baseline/ —
	// same pattern as Phase 0's plug-invariant test and Phase 1's
	// recompute-shadow test. ISO date directories sort lexicographically,
	// so the max element is the freshest capture.
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

	// Real DataCleanerService — the LedgerEntries MUST come from the
	// production dispatcher (ProcessAssetAdjustments / ProcessLiability
	// Adjustments / ProcessEarningsAdjustments wired into the orchestrator),
	// not from a direct Adjuster.Apply call.
	cfg := buildShadowTestConfig(t)
	cleanerSvc, err := datacleaner.NewDataCleanerService(cfg, ai.NewMockAIService(&ai.AIServiceConfig{}), nil)
	require.NoError(t, err, "construct DataCleanerService")

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

			// Collect AdjusterIDs across the whole 12-period bundle.
			// One observed-across-any-period set per ticker is the right
			// granularity for the lower-bound assertion: a wide-
			// applicability rule like B1 fires (or at least is
			// considered) on every period, but we don't care WHICH
			// period emits it — only that the dispatcher emitted SOME
			// LedgerEntry with the expected AdjusterID across the
			// bundle.
			observed := map[string]bool{}

			periodsProcessed := 0
			periods := historical.GetSortedPeriods()
			for _, period := range periods {
				if period == "" || period == "0" {
					continue
				}
				fd := historical.Data[period]
				if fd == nil {
					continue
				}
				if fd.FilingPeriod == "" || fd.FilingPeriod == "0" {
					continue
				}
				if fd.Ticker == "" {
					fd.Ticker = ticker
				}

				result, cleanErr := cleanerSvc.CleanFinancialData(context.Background(), fd)
				if cleanErr != nil {
					// Some periods are missing required fields (sub-
					// period stubs, currency-bucket collapse edges).
					// Skip them — Phase 1's signal stays "cleaner
					// success rate is not the assertion".
					continue
				}
				if result == nil || result.CleanedData == nil {
					continue
				}
				periodsProcessed++

				for _, entry := range result.CleanedData.AdjustmentLedger {
					if entry.AdjusterID != "" {
						observed[entry.AdjusterID] = true
					}
				}
			}

			require.Greater(t, periodsProcessed, 0,
				"%s: no periods produced a CleanedData result — fixture or cleaner regression",
				ticker)

			expectedMust, ok := predictionByTicker[ticker]
			if !ok {
				// Ticker is in the basket but has no prediction row.
				// That means this test was edited without updating the
				// per-ticker table — fail loudly so the table doesn't
				// silently degrade.
				t.Fatalf("%s: no expected-AdjusterID prediction row defined for this ticker", ticker)
			}

			// Sorted observed set for stable failure-message output.
			observedSorted := make([]string, 0, len(observed))
			for id := range observed {
				observedSorted = append(observedSorted, id)
			}
			sort.Strings(observedSorted)

			// Lower-bound assertion: every expected AdjusterID must
			// appear in observed. Missing entries are listed in the
			// failure message so a regression points at the dropped
			// adjuster directly.
			var missing []string
			for _, want := range expectedMust {
				if !observed[want] {
					missing = append(missing, want)
				}
			}
			if len(missing) > 0 {
				t.Errorf("%s: expected AdjusterIDs missing from ledger (%d periods processed)\n  missing: %v\n  observed: %v",
					ticker, periodsProcessed, missing, observedSorted)
			} else {
				t.Logf("%s: %d periods processed, %d distinct AdjusterIDs observed, all %d expected present",
					ticker, periodsProcessed, len(observed), len(expectedMust))
			}

			if !t.Failed() {
				passedCount.Add(1)
			}
		})
	}

	// All-skip silent-regression gate — mirrors the recompute-shadow
	// floor of 5. Current captured baselines yield 10 of 10 PASS;
	// 5 is the conservative floor that survives one or two tickers
	// temporarily dropping out without becoming so high it false-
	// positives during baseline-refresh cycles.
	require.GreaterOrEqual(t, passedCount.Load(), int32(5),
		"basket coverage degraded — fewer than 5 tickers exercised the ledger successfully (got %d)", passedCount.Load())
}

// TestLedger_BasketSnapshot_T2BS3_ParserTruthful pins the T2-BS-3 Option A
// parser fix (tracker docs/reviewer/T2-BS-3-...): AMD and KO report the
// LiabilitiesCurrent / LiabilitiesNoncurrent split WITHOUT the rolled-up
// us-gaap:Liabilities umbrella, which previously left
// FinancialData.TotalLiabilities==0 (the "carve-out"). The parser now derives
// TotalLiabilities = LiabilitiesCurrent + LiabilitiesNoncurrent, so re-parsing
// the captured bundle yields a TRUTHFUL, positive AsReported value.
//
// This SUPERSEDES the former carve-out acceptance test (which asserted
// AsReported==0, the pre-fix behavior). The synthesized-seed sibling
// TestCleanedFinancialData_AsReported_PreservesParserZeros_AMD_KO in
// internal/services/datacleaner/cleaneddata/t2bs3_test.go still pins the
// VIEW-layer contract (AsReported faithfully preserves whatever the parser
// stamped) using a constructed zero — unaffected by this parser change.
//
// Per-ticker contract (Option A):
//   - On AT LEAST ONE captured period for the ticker, the parser fallback
//     produces a positive total and the views stay coherent:
//     AsReported().TotalLiabilities  >  0
//     Restated().TotalLiabilities    >= AsReported().TotalLiabilities
//
// Skips when the tier2-baseline fixtures are absent (BUG-016) — this test
// can only run on a machine with the captured AMD/KO bundles checked out.
func TestLedger_BasketSnapshot_T2BS3_ParserTruthful(t *testing.T) {
	parser := sec.NewParser(zap.NewNop())

	// Reuse the baseline-resolution pattern from the cluster-prediction
	// test above — newest tier2-baseline date dir.
	baselineParent, err := filepath.Abs(filepath.Join("..", "..", "artifacts", "tier2-baseline"))
	require.NoError(t, err)
	matches, err := filepath.Glob(filepath.Join(baselineParent, "*"))
	require.NoError(t, err)
	var dateDirs []string
	for _, m := range matches {
		if info, statErr := os.Stat(m); statErr == nil && info.IsDir() {
			dateDirs = append(dateDirs, m)
		}
	}
	if len(dateDirs) == 0 {
		// BUG-016: the tier2-baseline subtree is gitignored and not present
		// on every machine. Skip rather than hard-fail when it's absent.
		t.Skipf("no tier2-baseline date dirs under %s (BUG-016)", baselineParent)
	}
	sort.Strings(dateDirs)
	bundleRoot := dateDirs[len(dateDirs)-1]

	cfg := buildShadowTestConfig(t)
	cleanerSvc, err := datacleaner.NewDataCleanerService(cfg, ai.NewMockAIService(&ai.AIServiceConfig{}), nil)
	require.NoError(t, err)

	t2bs3Tickers := []string{"AMD", "KO"}

	for _, ticker := range t2bs3Tickers {
		ticker := ticker
		t.Run(ticker, func(t *testing.T) {
			rawBytes, _, ok := loadSECRawForTicker(t, bundleRoot, ticker)
			if !ok {
				t.Skipf("no captured SEC fixture for %s under %s", ticker, bundleRoot)
				return
			}

			var facts ports.SECCompanyFacts
			require.NoError(t, json.Unmarshal(rawBytes, &facts))

			historical, err := parser.ParseFinancialData(context.Background(), &facts)
			require.NoError(t, err)
			require.NotNil(t, historical)
			require.NotEmpty(t, historical.Data)

			// Walk every captured period and find at least one where the
			// T2-BS-3 Option A parser fallback produced a truthful total
			// (positive AsReported) with coherent views.
			foundTruthful := false
			for _, period := range historical.GetSortedPeriods() {
				fd := historical.Data[period]
				if fd == nil || fd.FilingPeriod == "" || fd.FilingPeriod == "0" {
					continue
				}
				if fd.Ticker == "" {
					fd.Ticker = ticker
				}

				_, views, cleanErr := cleanerSvc.CleanFinancialDataWithViews(context.Background(), fd)
				if cleanErr != nil || views == nil {
					continue
				}

				asReported := views.AsReported()
				restated := views.Restated()

				// Option A: the parser now derives TotalLiabilities from the
				// current/noncurrent split, so AsReported is positive (was 0
				// pre-fix). Restated stays >= AsReported (it only adds
				// restatement/overlay deltas on top of the reconstructed sum).
				if asReported.TotalLiabilities > 0 {
					foundTruthful = true
					require.GreaterOrEqual(t, restated.TotalLiabilities, asReported.TotalLiabilities,
						"%s %s: Restated must not drop below AsReported", ticker, fd.FilingPeriod)
					t.Logf("%s %s: AsReported.TotalLiabilities=%.0f; Restated.TotalLiabilities=%.0f (T2-BS-3 Option A OK)",
						ticker, fd.FilingPeriod, asReported.TotalLiabilities, restated.TotalLiabilities)
					break
				}
			}

			require.True(t, foundTruthful,
				"%s: no captured period produced a positive AsReported.TotalLiabilities. "+
					"The T2-BS-3 Option A parser fallback should derive it from "+
					"LiabilitiesCurrent + LiabilitiesNoncurrent — investigate the parser "+
					"or refresh the baseline.", ticker)
		})
	}
}
