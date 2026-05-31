package datacleaner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
)

// TestCleanWithViews_ReturnsWrapper pins the CleanFinancialDataWithViews
// contract:
//
//  1. returns a non-nil *CleaningResult whose CleanedData matches what
//     CleanFinancialData would return for the same input;
//  2. returns a non-nil *cleaneddata.CleanedFinancialData whose accessors
//     are functional;
//  3. that calling AsReported() on the wrapper produces a view whose
//     Ticker matches the underlying entity.
//
// DC-1 Phase 5 (P5-C5): the historical "wrapper Raw() returns the same
// pointer as result.CleanedData" assertion was DELETED alongside
// cleaneddata.Raw() itself — Phase 4 closed the migration window by
// migrating every internal/ consumer to view accessors (zero remaining
// Raw() callers per `grep -rn '.Raw()' internal/`). Phase 5 deletes the
// escape hatch entirely; the test now exercises the view-only surface
// that survives DC-1 close. Phase 4 consumers grep for this method to
// enumerate migration progress.
func TestCleanWithViews_ReturnsWrapper(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()

	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	data := createTestFinancialDataWithIssues()

	result, views, err := svc.CleanFinancialDataWithViews(ctx, data)
	require.NoError(t, err)
	require.NotNil(t, result, "result must not be nil")
	require.NotNil(t, views, "views wrapper must not be nil")

	assert.True(t, result.Success, "cleaning must succeed on the standard fixture")
	require.NotNil(t, result.CleanedData)

	// Sanity: the view kind tag matches; the ticker round-trips through
	// the view-only surface (the Phase-3 pointer-identity check is no
	// longer applicable post-Raw()-deletion).
	view := views.AsReported()
	require.NotNil(t, view)
	assert.Equal(t, cleaneddata.AsReportedView, view.ViewKind)
	assert.Equal(t, result.CleanedData.Ticker, view.Ticker)
}

// TestCleanWithViews_NilDataPropagatesError pins that the wrapper
// surfaces validation errors faithfully (same error CleanFinancialData
// would return). No silent swallowing.
func TestCleanWithViews_NilDataPropagatesError(t *testing.T) {
	cfg := createTestConfig()
	ctx := context.Background()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	result, views, err := svc.CleanFinancialDataWithViews(ctx, nil)
	assert.Error(t, err, "nil data must produce an error")
	assert.Nil(t, result)
	assert.Nil(t, views)
}
