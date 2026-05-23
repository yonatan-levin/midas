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

// TestFFO_Forward_DataCenterREIT_PopulatesForwardLeg verifies the additive
// forward path: with a 5-year horizon at high growth rates and a 28x
// terminal P/FFO multiple, ForwardValue is populated alongside TrailingValue.
//
// Note (post-T2-P4-W1 reconciliation 2026-05-19): the original assertion
// `forward > trailing` was a calibration-dependent invariant that no longer
// holds at the new REIT_DATACENTER trailing multiple of 31x
// (industry_multiples.json v1.3.0 — bare DATA_CENTER key renamed to
// REIT_DATACENTER, which now correctly resolves in FFOModel.getMultiple
// instead of falling back to the 15x default that P4's pins were captured
// against). With trailing P/FFO = 31x and forward terminal = 28x +
// cost-of-equity discount, the trailing path can exceed the forward path;
// that is mathematically correct relative-valuation output and not a
// regression of the forward computation itself. The numeric pin tests
// TestTier2_EQIX_Pin / TestTier2_PLD_Pin (in profile package) lock in the
// exact values; this qualitative test now only asserts the forward leg is
// *populated* (non-zero, distinct from trailing, horizon honored).
func TestFFO_Forward_DataCenterREIT_PopulatesForwardLeg(t *testing.T) {
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	testhelpers.PatchFilingDatesFromAsOf(input)
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
	assert.NotEqual(t, result.TrailingValue, result.ForwardValue,
		"forward path must produce a value distinct from trailing")
	assert.Equal(t, 5, result.HorizonSelected)
	assert.InEpsilon(t, 28.0, result.TerminalMultiple, 1e-9)
}

// TestFFO_NilProfile_FallsThroughToTrailing pins the legacy path: a nil
// profile must leave ForwardValue zero so JSON-omitempty keeps the
// response identical to pre-Tier-2 shape.
func TestFFO_NilProfile_FallsThroughToTrailing(t *testing.T) {
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	testhelpers.PatchFilingDatesFromAsOf(input)
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
	testhelpers.PatchFilingDatesFromAsOf(input)
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
