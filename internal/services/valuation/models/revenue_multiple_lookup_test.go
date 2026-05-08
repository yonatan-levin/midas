package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRevenueMultipleModel_GetMultiple_RM2P1Buckets pins the RM-2 Phase 1
// sector buckets that close the silent-MFG-1.5x understatement reported in
// docs/reviewer/RM-2-sector-multiple-coverage-gaps.md.
//
// The classifier emits these codes (MFG_SEMI, FIN_BANK, FIN_INSURANCE) via
// the new sub-industry refinements added to config/datacleaner/industry_codes.json
// — without the matching entries in config/industry_multiples.json the
// longest-prefix-match would silently fall back to the parent (MFG=1.5x,
// FIN=2.5x) or the default (2.0x). This test exercises the embedded config
// path and asserts each bucket returns its calibrated value.
//
// Lives in revenue_multiple_lookup_test.go to avoid colliding with concurrent
// edits to revenue_multiple_test.go (Stream B is touching the algorithm body
// in parallel; this file only adds new assertions).
func TestRevenueMultipleModel_GetMultiple_RM2P1Buckets(t *testing.T) {
	// NewRevenueMultipleModel reads the embedded industry_multiples.json so the
	// values exercised here are the production config, not a fixture.
	model := NewRevenueMultipleModel(testLogger())
	require.NotNil(t, model)

	tests := []struct {
		name     string
		industry string
		expected float64
	}{
		{
			name:     "MFG_SEMI returns 6.5x (was MFG default 1.5x — RM-2 P1 closes the semi gap)",
			industry: "MFG_SEMI",
			expected: 6.5,
		},
		{
			name:     "FIN_BANK returns 2.0x (slightly below FIN parent 2.5x)",
			industry: "FIN_BANK",
			expected: 2.0,
		},
		{
			name:     "FIN_INSURANCE returns 1.0x (insurance trades below banks)",
			industry: "FIN_INSURANCE",
			expected: 1.0,
		},
		{
			name:     "MFG parent stays at 1.5x — sub-industry add must not regress parent",
			industry: "MFG",
			expected: 1.5,
		},
		{
			name:     "FIN parent stays at 2.5x — sub-industry add must not regress parent",
			industry: "FIN",
			expected: 2.5,
		},
		{
			name:     "MFG_AEROSPACE (unmapped sub) longest-prefix-matches MFG parent",
			industry: "MFG_AEROSPACE",
			expected: 1.5,
		},
		{
			name:     "FIN_FINTECH (unmapped sub) longest-prefix-matches FIN parent",
			industry: "FIN_FINTECH",
			expected: 2.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.getMultiple(tt.industry)
			assert.InDelta(t, tt.expected, got, 0.0001,
				"getMultiple(%q) = %v; want %v", tt.industry, got, tt.expected)
		})
	}
}
