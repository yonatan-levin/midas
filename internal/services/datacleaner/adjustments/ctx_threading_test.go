package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestCtxThreading_LiabilityAdjusterReceivesCtx pins the Phase 3 Task 3.9
// signature change: ProcessLiabilityAdjustments now accepts a
// context.Context as its first parameter. Passing a CANCELLED ctx must
// not crash the dispatcher (Phase 3 wires the ctx but does not yet read
// from ctx.Done()); future ctx-aware adjusters can opt into cancellation
// without changing the call site.
//
// Asset and earnings siblings get the same ctx-as-first-parameter shape
// for symmetry — exercised below.
func TestCtxThreading_LiabilityAdjusterReceivesCtx(t *testing.T) {
	la := NewLiabilityAdjuster(nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the test pins "ctx-cancelled does not crash".

	data := &entities.FinancialData{
		Ticker:    "CTXTEST",
		TotalDebt: 100,
	}
	cleaningCtx := &entities.CleaningContext{
		IndustryCode: "TECH",
	}

	// Empty rules slice → nothing executes; we only care that the ctx
	// parameter is accepted on the public signature.
	result := la.ProcessLiabilityAdjustments(ctx, data, nil, cleaningCtx)
	assert.NotNil(t, result, "result must not be nil even with a cancelled ctx")
}

// TestCtxThreading_AssetAdjusterReceivesCtx pins the same signature shape
// on the asset-side dispatcher.
func TestCtxThreading_AssetAdjusterReceivesCtx(t *testing.T) {
	aa := NewAssetAdjuster()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	data := &entities.FinancialData{Ticker: "CTXTEST", TotalAssets: 1000}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "TECH"}

	result := aa.ProcessAssetAdjustments(ctx, data, nil, cleaningCtx)
	assert.NotNil(t, result)
}

// TestCtxThreading_EarningsAdjusterReceivesCtx pins the same signature
// shape on the earnings-side dispatcher.
func TestCtxThreading_EarningsAdjusterReceivesCtx(t *testing.T) {
	ea := NewEarningsAdjuster()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	data := &entities.FinancialData{Ticker: "CTXTEST"}
	cleaningCtx := &entities.CleaningContext{IndustryCode: "TECH"}

	result := ea.ProcessEarningsAdjustments(ctx, data, nil, cleaningCtx)
	assert.NotNil(t, result)
}
