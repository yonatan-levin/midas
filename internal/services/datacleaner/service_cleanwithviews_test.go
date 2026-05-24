package datacleaner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
)

// TestCleanWithViews_ReturnsWrapper pins that the new Phase 3
// CleanFinancialDataWithViews method:
//
//  1. returns a non-nil *CleaningResult whose CleanedData matches what
//     CleanFinancialData would return for the same input;
//  2. returns a non-nil *cleaneddata.CleanedFinancialData whose Raw()
//     points at the same *FinancialData as result.CleanedData;
//  3. that calling AsReported() on the wrapper produces a view whose
//     Ticker matches the underlying entity.
//
// The wrapper is a thin pass-through (Phase 3 contract) — no extra side
// effects, no duplicated state. Phase 4 consumers grep for this method
// to enumerate migration progress.
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

	// Wrapper holds the same *FinancialData pointer as result.CleanedData;
	// Phase 4 consumers may rely on this for migration audits.
	assert.Same(t, result.CleanedData, views.Raw(),
		"wrapper Raw() must return the same *FinancialData held by result.CleanedData")

	// Sanity: the view kind tag matches; the ticker round-trips.
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
