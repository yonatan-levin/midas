package artifact_test

// Phase 2.C — auto-on-always trigger unit tests for the artifact.Trigger
// vocabulary.
//
// Phase 2.C adds a single new trigger constant (TriggerAlways = "always")
// that the trace middleware uses as the lowest-precedence catch-all when
// the operator has flipped logging.artifact_store.triggers.always=true.
// The bundle itself gains no new state for Phase 2.C — the Trigger value is
// stamped onto the manifest at Promote-time exactly as the other auto-
// triggers (on_error, on_quality_flag) already do. So this file holds a
// single constant pin; the behavioural surface lives in the trace middleware
// tests where the precedence ladder is exercised.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestTrigger_AlwaysConstant_Defined pins the wire value of the new
// trigger. Manifest tooling and ops dashboards grep for "always"; a typo
// in a future refactor would silently break that contract.
//
// Mirrors TestTrigger_OnErrorConstant_Defined / TestTrigger_OnQualityFlag
// ConstantDefined exactly so a single pattern covers all four trigger
// constants.
func TestTrigger_AlwaysConstant_Defined(t *testing.T) {
	assert.Equal(t, artifact.Trigger("always"), artifact.TriggerAlways,
		"TriggerAlways wire value must remain always")
}
