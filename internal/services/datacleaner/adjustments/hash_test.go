package adjustments

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// TestSha256HexPromptCanonical_HandlesUnsupportedContextValues is the
// LOW-1 regression pin. Before the fix the inner json.Marshal error
// was swallowed via `_`; an unsupported type (chan, func, cyclic
// struct) would hash as an empty string, silently colliding with any
// other unsupported value AND with the absent-field case. The fix
// emits a typed `<unsupported:%T>` tag so the canonical-request
// fingerprint stays sensitive to the unsupported value's identity.
func TestSha256HexPromptCanonical_HandlesUnsupportedContextValues(t *testing.T) {
	req := &ai.FootnoteAnalysisRequest{
		Ticker:        "TEST",
		FootnoteText:  "footnote text",
		AnalysisType:  ai.ContingentLiabilityAnalysis,
		PriorityLevel: ai.PriorityNormal,
		Context: map[string]interface{}{
			"good_value": "string-value",
			"bad_value":  make(chan int), // unsupported by encoding/json
		},
	}

	hash := sha256HexPromptCanonical(req)
	require.Len(t, hash, 64, "must produce a 64-char hex digest")

	// Determinism: same unsupported type → same tag → same hash bytes.
	hash2 := sha256HexPromptCanonical(req)
	assert.Equal(t, hash, hash2,
		"identical inputs (including unsupported chan type) must produce identical hashes")

	// Collision prevention: different unsupported types must produce
	// different hashes. Pre-fix code would have produced the same empty
	// hash for both — a silent collision across structurally-distinct inputs.
	req.Context["bad_value"] = make(chan string)
	hash3 := sha256HexPromptCanonical(req)
	assert.NotEqual(t, hash, hash3,
		"different unsupported types (chan int vs chan string) must produce different hashes")

	// Sensitivity to value absence: removing the unsupported field
	// produces a different hash from the typed-tag fallback. Pre-fix
	// code would have collided here too (empty-string Marshal output
	// equals empty-field absent).
	req.Context["bad_value"] = nil
	delete(req.Context, "bad_value")
	hash4 := sha256HexPromptCanonical(req)
	assert.NotEqual(t, hash, hash4,
		"absent unsupported field must produce a different hash than a typed-tag fallback")
}

// TestSha256HexPromptCanonical_DeterministicOnSupportedTypes pins that
// the F.5 fix preserves the determinism invariant for the supported
// types B3 actually uses today (string, float64, int). The Q4
// PromptHash contract depends on this byte-identical determinism;
// the previous TestQ4_AIProvenance_SHA256_Deterministic exercises
// the path end-to-end, but this pin makes the helper-level invariant
// explicit for any future refactor of the canonical struct shape.
func TestSha256HexPromptCanonical_DeterministicOnSupportedTypes(t *testing.T) {
	req1 := &ai.FootnoteAnalysisRequest{
		Ticker:        "ACME",
		FootnoteText:  "Material contingent loss exposure",
		AnalysisType:  ai.ContingentLiabilityAnalysis,
		PriorityLevel: ai.PriorityNormal,
		FilingType:    "10-K",
		Context: map[string]interface{}{
			"industry_code":           "45",
			"total_contingent_amount": 50_000_000.0,
			"revenue":                 1_000_000_000.0,
		},
	}
	// Same structural shape, different Context insertion order — the
	// sort.Strings(keys) call must absorb the insertion-order non-determinism.
	req2 := &ai.FootnoteAnalysisRequest{
		Ticker:        "ACME",
		FootnoteText:  "Material contingent loss exposure",
		AnalysisType:  ai.ContingentLiabilityAnalysis,
		PriorityLevel: ai.PriorityNormal,
		FilingType:    "10-K",
		Context: map[string]interface{}{
			"revenue":                 1_000_000_000.0,
			"industry_code":           "45",
			"total_contingent_amount": 50_000_000.0,
		},
	}
	assert.Equal(t, sha256HexPromptCanonical(req1), sha256HexPromptCanonical(req2),
		"insertion order MUST NOT affect the hash — keys are sorted before serialization")
}
