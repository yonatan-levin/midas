package cleaneddata

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestCleanedFinancialData_AsReported_PreservesParserZeros_AMD_KO is the
// T2-BS-3 carve-out acceptance pin. The Phase 2 parser dropout for AMD/KO
// leaves *FinancialData.TotalLiabilities == 0 while the component-level
// fields carry truthful values that reconstruct to ~$9B.
//
// The expected behavior, per spec §4.6 (AsReported) and §4.4 (Restated):
//
//   - AsReported().TotalLiabilities == 0  (preserves the parser-stamped value)
//   - Restated().TotalLiabilities  > 0   (component-sum recompute reflects truth)
//
// The test synthesizes the AMD/KO parser-dropout shape rather than
// loading the live shadow snapshot fixture; the fixture encodes
// observability snapshots from the recompute shim, not raw
// *FinancialData values, so a synthetic seed keeps the test
// deterministic without coupling to artifact-format evolution.
//
// Phase 3 → Phase 4 gate acceptance criterion (spec §10 item 4): the
// extended basket-integration test asserts the same property on the
// real AMD + KO fixtures.
func TestCleanedFinancialData_AsReported_PreservesParserZeros_AMD_KO(t *testing.T) {
	// Synthesize the T2-BS-3 carve-out: TotalLiabilities=0 alongside
	// real component values that sum to a positive recomputed total.
	// Numbers approximate AMD's 2023Q2 shape from the shadow snapshot
	// (TotalLiabilities=0; reconstruction=$9,679M).
	raw := &entities.FinancialData{
		Ticker:                            "AMD",
		FilingPeriod:                      "2023Q2",
		TotalLiabilities:                  0, // parser dropout (T2-BS-3)
		CurrentLiabilities:                4_000_000_000,
		OperatingLeaseLiabilityCurrent:    0,
		OtherCurrentLiabilities:           4_000_000_000,
		TotalDebt:                         2_500_000_000,
		OperatingLeaseLiabilityNoncurrent: 0,
		OtherNonCurrentLiabilities:        3_179_000_000,
		// Asset side seeded for completeness (not asserted on this test).
		CashAndCashEquivalents: 5_000_000_000,
		Inventory:              2_000_000_000,
		OtherCurrentAssets:     500_000_000,
		CurrentAssets:          7_500_000_000,
		Goodwill:               24_000_000_000,
		OtherIntangibles:       18_000_000_000,
		DeferredTaxAssets:      100_000_000,
		OtherNonCurrentAssets:  10_000_000_000,
		TotalAssets:            59_600_000_000,
	}

	c := New(raw, raw)
	asReported := c.AsReported()
	restated := c.Restated()
	require.NotNil(t, asReported)
	require.NotNil(t, restated)

	// AsReported: parser-stamped zero stays zero (T2-BS-3 Option B carve-out).
	assert.Equal(t, 0.0, asReported.TotalLiabilities,
		"AsReported MUST preserve parser-stamped TotalLiabilities=0 for T2-BS-3 carve-out tickers")

	// Restated: component-sum reconstruction surfaces the truthful total.
	//   CurrentLiabilities(4_000_000_000) +
	//   TotalDebt(2_500_000_000) +
	//   OperatingLeaseLiabilityNoncurrent(0) +
	//   OtherNonCurrentLiabilities(3_179_000_000) = 9_679_000_000
	assert.Equal(t, 9_679_000_000.0, restated.TotalLiabilities,
		"Restated MUST reconstruct TotalLiabilities from sum(components) for T2-BS-3 tickers")

	// CurrentLiabilities ALSO recomputes from components in Restated; for
	// this fixture (no Restater touched it) it matches the parser-stamped
	// 4B because OperatingLeaseLiabilityCurrent + OtherCurrentLiabilities
	// = 0 + 4_000_000_000 = 4_000_000_000.
	assert.Equal(t, 4_000_000_000.0, restated.CurrentLiabilities,
		"Restated.CurrentLiabilities = OpLeaseLiabCurrent + OtherCurrentLiabilities")
}
