package sqlite

import (
	"context"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestFinancialDataRepository_PlugFields_PersistenceGap documents the
// DC-1 Phase 0 SQLite persistence gap: the four Other*Assets /
// Other*Liabilities plug fields populated by the SEC parser are NOT round-
// tripped through this repository today because:
//
//  1. internal/infra/database/schema.sql:25-107 does not declare the four
//     columns (other_current_assets, other_non_current_assets,
//     other_current_liabilities, other_non_current_liabilities).
//  2. storeWith's INSERT column list (financial_data_repository.go:56-87) does
//     not enumerate the plug fields, so values supplied to Store are dropped.
//  3. GetLatest / GetHistorical / GetByPeriod SELECT statements
//     (financial_data_repository.go:141+, 202+, 278+ respectively) do not
//     request plug columns, so reads silently return zero.
//
// This is **HARMLESS UNDER THE PHASE 0 INVARIANT** ("no consumer reads the
// new plug fields") — but it is a real persistence gap that Phase 1+ must
// close before any downstream code starts reading the plug fields from a
// cached database row.
//
// Phase 0 scope contract (per the implementation plan's Task 0.6):
//
//   - JSON-blob persistence paths (cache layer) require zero code change —
//     pinned by cache/memory_cache_plug_roundtrip_test.go.
//   - Discrete-column persistence paths (this repository) require a schema
//     migration + INSERT/SELECT extension. Per the plan, this is OUT OF
//     SCOPE for Phase 0 and must be escalated to ARCH.
//
// This test pins the CURRENT BEHAVIOR (plug fields round-trip to zero). When
// Phase 1+ lands the migration + repo changes, this test must FLIP to assert
// the actual stored values survive. The flip is the load-bearing signal that
// the persistence gap was closed; deleting the test silently would lose that
// signal.
//
// DC-1 spec reference:
// docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
func TestFinancialDataRepository_PlugFields_PersistenceGap(t *testing.T) {
	db := setupTestDatabase(t)
	defer cleanupTestDatabase(db)

	repo := NewFinancialDataRepository(db)
	ctx := context.Background()

	const (
		wantOtherCA  = 107_270_000_000.0
		wantOtherNCA = 209_189_000_000.0
		wantOtherCL  = 143_898_000_000.0
		wantOtherNCL = 23_491_000_000.0
	)

	data := &entities.FinancialData{
		Ticker:                     "AAPL",
		CIK:                        "0000320193",
		FilingPeriod:               "2023FY",
		FilingDate:                 time.Date(2023, 11, 3, 0, 0, 0, 0, time.UTC),
		AsOf:                       time.Now(),
		SharesOutstanding:          15_550_061_000,
		DilutedSharesOutstanding:   15_812_547_000,
		OtherCurrentAssets:         wantOtherCA,
		OtherNonCurrentAssets:      wantOtherNCA,
		OtherCurrentLiabilities:    wantOtherCL,
		OtherNonCurrentLiabilities: wantOtherNCL,
	}

	require.NoError(t, repo.Store(ctx, data))

	got, err := repo.GetLatest(ctx, "AAPL")
	require.NoError(t, err)
	require.NotNil(t, got)

	// CURRENT BEHAVIOR (Phase 0): plug fields are not in the INSERT/SELECT
	// column lists, so the stored values are dropped and reads return zero.
	// When Phase 1+ ships the schema migration + INSERT/SELECT extensions,
	// flip these four assertions to the wantOther* constants above. That flip
	// is the load-bearing signal that the persistence gap was closed.
	assert.Equal(t, 0.0, got.OtherCurrentAssets,
		"Phase 0 gap: SQLite does not persist OtherCurrentAssets — flip to wantOtherCA after Phase 1+ migration")
	assert.Equal(t, 0.0, got.OtherNonCurrentAssets,
		"Phase 0 gap: SQLite does not persist OtherNonCurrentAssets — flip to wantOtherNCA after Phase 1+ migration")
	assert.Equal(t, 0.0, got.OtherCurrentLiabilities,
		"Phase 0 gap: SQLite does not persist OtherCurrentLiabilities — flip to wantOtherCL after Phase 1+ migration")
	assert.Equal(t, 0.0, got.OtherNonCurrentLiabilities,
		"Phase 0 gap: SQLite does not persist OtherNonCurrentLiabilities — flip to wantOtherNCL after Phase 1+ migration")

	// Sanity: pre-Phase-0 fields DO persist correctly, so the test is
	// pinning a specific gap (the four plug columns) not a generic
	// repository regression.
	assert.Equal(t, "AAPL", got.Ticker)
	assert.Equal(t, "0000320193", got.CIK)
	assert.Equal(t, "2023FY", got.FilingPeriod)

	// Touch the unused constants so the linter does not flag them; they
	// document the post-Phase-1+ expected values for the flip described above.
	_ = wantOtherCA
	_ = wantOtherNCA
	_ = wantOtherCL
	_ = wantOtherNCL
}
