package datacleaner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestCleanFinancialData_SnapshotSymmetry_RevenueZeroTicker_RPL8 is the
// regression pin for RPL-8 (#25).
//
// The bug: CleanFinancialData writes the INPUT snapshot 10-clean-input.json,
// then calls ValidateData, which rejects Revenue<=0. Banks/insurers
// legitimately carry Revenue=0, so they early-return AFTER the input snapshot
// but BEFORE the OUTPUT snapshot block — leaving the bundle missing
// 10-clean-output.json + 10-clean-trace.json + the FinancialData schema stamp,
// which forced replay to need --allow-schema-drift.
//
// The fix makes the output snapshot + schema stamp SYMMETRIC across the
// validation-fail early return. This test drives a real eager artifact bundle
// through CleanFinancialData and asserts:
//
//   - Case 1 (the bug): a bank-shaped Revenue=0 ticker (ValidateData fails) now
//     produces 10-clean-output.json + 10-clean-trace.json AND stamps
//     FinancialData in the manifest schema_versions.
//   - Case 2 (control): a normal Revenue>0 ticker (happy path) produces the
//     same files — the happy path is unchanged.
//
// Pre-fix evidence: Case 1 fails — 10-clean-output.json is absent and
// manifest.SchemaVersions["FinancialData"] is unset (0). Case 2 passes pre-fix
// (the happy path always wrote the output snapshot).
func TestCleanFinancialData_SnapshotSymmetry_RevenueZeroTicker_RPL8(t *testing.T) {
	const schemaFinancialData = 10 // current FinancialData bundle schema version

	tests := []struct {
		name         string
		ticker       string
		data         *entities.FinancialData
		expectResult bool // happy path returns a non-nil result; validation-fail returns (nil, err)
	}{
		{
			name:         "revenue_zero_bank_validation_fails",
			ticker:       "BANKZERO",
			data:         createBankZeroRevenueData(),
			expectResult: false,
		},
		{
			name:         "normal_revenue_happy_path",
			ticker:       "NORMAL",
			data:         createTestFinancialDataWithIssues(),
			expectResult: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := createTestConfig()
			svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
			require.NoError(t, err)

			// Open a real EAGER artifact bundle (Enabled, real on-disk root) and
			// inject it into ctx, exactly as the trace middleware does in a
			// ?trace=1 request.
			root := t.TempDir()
			b, err := artifact.OpenBundle(
				artifact.Config{Enabled: true, RootPath: root},
				"rid-rpl8-"+tc.name, tc.ticker, artifact.TriggerQuery,
			)
			require.NoError(t, err)
			require.NotNil(t, b)
			ctx := artifact.Inject(context.Background(), b)

			_, cleanErr := svc.CleanFinancialData(ctx, tc.data)
			if tc.expectResult {
				require.NoError(t, cleanErr, "happy path must succeed")
			} else {
				require.Error(t, cleanErr, "Revenue=0 must fail ValidateData and return an error")
			}

			// Close flushes the worker queue + writes the manifest.
			require.NoError(t, b.Close())

			bundleRoot := b.Root()

			// Both the input AND output snapshots must be present — symmetry.
			assertBundleFileExists(t, bundleRoot, "10-clean-input.json")
			assertBundleFileExists(t, bundleRoot, "10-clean-output.json")
			assertBundleFileExists(t, bundleRoot, "10-clean-trace.json")

			// The FinancialData schema stamp must be recorded in the manifest so
			// replay does not need --allow-schema-drift.
			mfBody, err := os.ReadFile(filepath.Join(bundleRoot, "00-manifest.json"))
			require.NoError(t, err)
			var mf artifact.Manifest
			require.NoError(t, json.Unmarshal(mfBody, &mf))
			assert.Equal(t, schemaFinancialData, mf.SchemaVersions["FinancialData"],
				"FinancialData schema version must be stamped on every snapshot-emitting path")
		})
	}
}

// TestCleanFinancialData_SnapshotSymmetry_CacheHit_RPL8 is the regression pin
// for the CACHE-HIT leg of RPL-8 (#25), found+fixed by REVIEWER in 5276272.
//
// The bug: with enable_caching ON (the default), CleanFinancialData writes the
// INPUT snapshot 10-clean-input.json, then on a warm cache returns the cached
// result BEFORE the output snapshot block — leaving the cache-hit bundle missing
// 10-clean-output.json + 10-clean-trace.json + the FinancialData schema stamp,
// the same drift the validation-fail early return suffered. The fix routes the
// cache-hit return through snapshotCleanOutput so the output snapshot + schema
// stamp stay symmetric, and removed a now-redundant standalone
// recordQualityFlagCount (RecordQualityFlagCount ADDS — not idempotent — so
// double-calling would double-count and could spuriously trip the
// auto-on-quality-flag trigger).
//
// This test calls CleanFinancialData twice against the same service instance
// with the SAME input (a normal Revenue>0 ticker — the cache hit is the point,
// not the revenue), injecting a SEPARATE eager bundle for each call so the
// second (cache-hit) bundle can be asserted on in isolation. It asserts:
//
//   - the second call IS a cache hit (it returns the exact pointer the first
//     call cached — getCachedResult hands back the stored *CleaningResult);
//   - the cache-hit bundle has 10-clean-output.json + 10-clean-trace.json and
//     stamps FinancialData=10 in the manifest (the symmetry the fix restores);
//   - the cache-hit bundle's QualityFlagCount is NOT doubled — it equals the
//     first (cache-miss) call's count, proving the redundant
//     recordQualityFlagCount removal.
//
// Pre-fix evidence (teeth): commenting out the cache-hit snapshotCleanOutput
// call makes 10-clean-output.json absent and FinancialData unstamped (0) on the
// second bundle, failing this test.
func TestCleanFinancialData_SnapshotSymmetry_CacheHit_RPL8(t *testing.T) {
	const schemaFinancialData = 10 // current FinancialData bundle schema version

	cfg := createTestConfig()
	// The whole point of this test is the cache-HIT early return, so caching
	// must be ON. createTestConfig() enables it; assert the prerequisite so a
	// future config change can't silently turn this into a two-cache-miss test.
	require.True(t, cfg.DataCleaner.EnableCaching,
		"test prerequisite: DataCleaner.EnableCaching must be true to exercise the cache-hit path")
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// One FinancialData reused across both calls. generateCacheKey hashes
	// (Ticker, FilingPeriod, FilingDate.Unix()), so identical input lands on the
	// same cache entry. Revenue>0 normal ticker — drives the happy path on call
	// one (which populates the cache) so call two is a clean cache hit.
	data := createTestFinancialDataWithIssues()

	// runOnce opens a fresh EAGER bundle, runs CleanFinancialData with it on ctx,
	// closes the bundle (flushing the worker queue + writing the manifest), and
	// returns the bundle + the cleaning result. The bundle is kept (not just its
	// root) so the test can read QualityFlagCount() — that count lives on the
	// in-memory bundle (atomic.Int64), not in the persisted manifest.
	runOnce := func(rid string) (*artifact.Bundle, *entities.CleaningResult) {
		root := t.TempDir()
		b, err := artifact.OpenBundle(
			artifact.Config{Enabled: true, RootPath: root},
			rid, data.Ticker, artifact.TriggerQuery,
		)
		require.NoError(t, err)
		require.NotNil(t, b)

		ctx := artifact.Inject(context.Background(), b)
		result, cleanErr := svc.CleanFinancialData(ctx, data)
		require.NoError(t, cleanErr)
		require.NotNil(t, result)

		require.NoError(t, b.Close())
		return b, result
	}

	// First call — cache MISS. Walks the full pipeline and populates the cache.
	b1, r1 := runOnce("rid-rpl8-cache-miss")

	// Second call — cache HIT. The fix routes this early return through
	// snapshotCleanOutput.
	b2, r2 := runOnce("rid-rpl8-cache-hit")

	// Prove the second call really was a cache hit. SR-1 B6 returns a shallow
	// COPY of the cached *CleaningResult (to avoid racing the shared pointer on
	// ProcessingTime), so the outer pointers differ — but the copy shares the
	// inner CleanedData pointer, which a fresh full-pipeline run would have
	// re-allocated. Same CleanedData pointer ⇒ cache HIT.
	require.Same(t, r1.CleanedData, r2.CleanedData,
		"second call must reuse the cached CleanedData (cache HIT); a re-run would allocate a new one")

	root2 := b2.Root()

	// The cache-hit bundle must carry BOTH the input AND output snapshots — the
	// symmetry the fix restores.
	assertBundleFileExists(t, root2, "10-clean-input.json")
	assertBundleFileExists(t, root2, "10-clean-output.json")
	assertBundleFileExists(t, root2, "10-clean-trace.json")

	// The FinancialData schema stamp must be recorded on the cache-hit path too,
	// so replay does not need --allow-schema-drift on a warm-cache bundle.
	mfBody, err := os.ReadFile(filepath.Join(root2, "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, schemaFinancialData, mf.SchemaVersions["FinancialData"],
		"FinancialData schema version must be stamped on the cache-hit snapshot path")

	// The cache-hit bundle's quality-flag count must NOT be doubled: it must
	// equal the cache-miss count, proving snapshotCleanOutput records the count
	// exactly once (the standalone recordQualityFlagCount that used to live on
	// the cache-hit path was removed because RecordQualityFlagCount ADDS, so
	// calling both would double-count). This holds whether or not the fixture
	// raises qualifying flags — equality is the load-bearing property, not the
	// magnitude — so it stays robust without a brittle exact-count assertion.
	assert.Equal(t, b1.QualityFlagCount(), b2.QualityFlagCount(),
		"cache-hit bundle quality-flag count must equal the cache-miss count (not doubled); got first=%d second=%d",
		b1.QualityFlagCount(), b2.QualityFlagCount())
}

// assertBundleFileExists fails the test if name is missing under bundleRoot.
func assertBundleFileExists(t *testing.T, bundleRoot, name string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(bundleRoot, name))
	require.NoErrorf(t, err, "expected bundle file %s to be written", name)
}

// createBankZeroRevenueData returns a bank-shaped FinancialData with Revenue=0,
// which ValidateData rejects (Revenue must be positive). All OTHER required
// fields are valid so the early return is driven specifically by the
// zero-revenue branch — the exact shape banks/insurers carry.
func createBankZeroRevenueData() *entities.FinancialData {
	return &entities.FinancialData{
		Ticker: "BANKZERO",
		CIK:    "0000019617", // JPM-like
		AsOf:   time.Now().AddDate(0, -3, 0),

		// Revenue=0 is the bug trigger — banks report no "Revenue" us-gaap tag.
		Revenue:         0,
		OperatingIncome: 50000000000,

		// Valid balance sheet so only the Revenue<=0 branch fires.
		TotalAssets:         3000000000000,
		TotalDebt:           400000000000,
		InterestBearingDebt: 380000000000,

		SharesOutstanding:        2900000000,
		DilutedSharesOutstanding: 2950000000,

		FilingPeriod: "2024Q3",
		FilingDate:   time.Now().AddDate(0, -3, 0),
	}
}
