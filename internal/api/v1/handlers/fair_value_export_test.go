package handlers_test

// External-package test file. Compiles only against the handlers package's
// EXPORTED surface — so it fails to build until BuildIndustryFromResult
// (and the Industry struct it returns) are exported.
//
// Pinned for Phase R2 D1.1 of the observability replay tooling
// (docs/refactoring/archive/observability-replay-tooling-r2-implementation-plan.md
// §3 Task D1.1). The replay orchestration layer in
// internal/observability/replay/replay.go will import this exported helper
// to rebuild a FairValueResponse-equivalent shape from
// *entities.ValuationResult, exactly as the production handler does. Without
// the export, replay.go cannot compile.
//
// Design intent: the rename is logic-free. REVIEWER (per plan §3 D1.1
// acceptance) audits `git diff master..HEAD -- fair_value.go` shows ONLY a
// function-name capitalization change plus the two internal call-sites.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestBuildIndustryFromResult_ExportedSurface compiles only when
// handlers.BuildIndustryFromResult is exported AND the Industry struct it
// returns is exported with at least the SIC field. Mirrors plan §3 D1.1
// suggested test: construct an entities.ValuationResult with IndustrySIC
// populated, assert the returned *Industry is non-nil and carries the SIC.
func TestBuildIndustryFromResult_ExportedSurface(t *testing.T) {
	in := &entities.ValuationResult{IndustrySIC: "TECH"}

	// The very name of the call is the load-bearing assertion: if the
	// rename was not applied, this call refers to an unexported symbol
	// and the test file fails to build. The behavior assertions below
	// pin that the rename was logic-free (same body, same return shape).
	got := handlers.BuildIndustryFromResult(in)

	assert.NotNil(t, got, "BuildIndustryFromResult should return a non-nil *Industry when IndustrySIC is populated")
	assert.Equal(t, "TECH", got.SIC, "exported helper must surface IndustrySIC unchanged")
	// Match flag should be false: TECH (parent) only matches GICS "45" per
	// the sicToGICS table; with HeuristicCode empty, matchSICToGICS returns
	// false. Pinning this guards against an accidental logic shift inside
	// the rename.
	assert.False(t, got.Match, "Match must be false when HeuristicCode is empty (rename must not alter logic)")
}
