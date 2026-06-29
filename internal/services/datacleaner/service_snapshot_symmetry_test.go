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
