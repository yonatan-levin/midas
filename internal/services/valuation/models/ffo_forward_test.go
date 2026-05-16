package models_test

// Tier 2 P4 (VAL-3 Phase 3) — forward FFO projection tests. The forward
// path is gated on profile.HorizonYears > 0; nil profile or zero horizon
// MUST behave as legacy trailing-only. Spec §6.4.
//
// Tests live in the external models_test package so they can import the
// shared testhelpers fixtures (which themselves import models).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile/testhelpers"
)

// stampFilingDates back-fills FilingDate on every period of the fixture's
// HistoricalFinancialData using AsOf so FFOModel.GetLatestPeriod can pick
// the newest row. The shared synthetic builders deliberately only stamp
// AsOf (mirroring real entity construction). The testhelpers package
// is treated as read-only by P4 — patch here instead.
func stampFilingDates(input *models.ModelInput) {
	if input == nil || input.HistoricalData == nil {
		return
	}
	for _, period := range input.HistoricalData.Data {
		if period == nil {
			continue
		}
		if period.FilingDate.IsZero() {
			period.FilingDate = period.AsOf
		}
	}
}

// TestFFO_Forward_DataCenterREIT_HigherThanTrailing verifies the additive
// forward path: with a 5-year horizon at high growth rates and a 28x
// terminal P/FFO multiple, ForwardValue must exceed TrailingValue for a
// data-center REIT shape (EQIX-ish synthetic).
func TestFFO_Forward_DataCenterREIT_HigherThanTrailing(t *testing.T) {
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	stampFilingDates(input)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:        "reit_datacenter:high_growth",
			Archetype:        profile.ArchetypeREITDataCenter,
			Maturity:         profile.MaturityHighGrowth,
			HorizonYears:     5,
			TerminalMultiple: 28.0,
			DiscountMethod:   profile.DiscountCostOfEquity,
		},
	}

	ffo := models.NewFFOModel(zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.TrailingValue, 0.0)
	assert.Greater(t, result.ForwardValue, 0.0)
	assert.Greater(t, result.ForwardValue, result.TrailingValue,
		"data center REIT forward should exceed trailing given high-growth profile")
	assert.Equal(t, 5, result.HorizonSelected)
	assert.InEpsilon(t, 28.0, result.TerminalMultiple, 1e-9)
}

// TestFFO_NilProfile_FallsThroughToTrailing pins the legacy path: a nil
// profile must leave ForwardValue zero so JSON-omitempty keeps the
// response identical to pre-Tier-2 shape.
func TestFFO_NilProfile_FallsThroughToTrailing(t *testing.T) {
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	stampFilingDates(input)
	input.Profile = nil

	ffo := models.NewFFOModel(zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
	assert.Equal(t, 0.0, result.ForwardValue)
	assert.Equal(t, 0, result.HorizonSelected)
	assert.Equal(t, 0.0, result.TerminalMultiple)
}

// TestFFO_ProfileHorizonZero_BehavesLikeNoProfile pins the second gate:
// a non-nil profile with HorizonYears==0 must not engage the forward
// path. Without this, the legacy bit-for-bit guarantee would degrade.
func TestFFO_ProfileHorizonZero_BehavesLikeNoProfile(t *testing.T) {
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	stampFilingDates(input)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{HorizonYears: 0},
	}

	ffo := models.NewFFOModel(zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
	assert.Equal(t, 0.0, result.ForwardValue)
	assert.Equal(t, 0, result.HorizonSelected)
}
