package profile

// Captured pre-Tier-2 + per-phase expected values for the cross-model
// regression suite (tier2_regression_test.go). Each constant pins the
// IntrinsicValuePerShare a representative ticker produces under its
// resolved profile so future refactors of the model math (or the JSON
// calibration rows) surface as test failures rather than silent drift.
//
// Regeneration workflow (spec §8.2, plan Gap #5):
//  1. Build a fresh worktree on master HEAD.
//  2. Run `go test -tags pincapture -run TestCapturePins ./internal/services/valuation/profile/... -v`.
//  3. Paste the printed lines into this file.
//
// One constant per ticker; constants are NOT in pins_test.go because the
// regression suite (also in this package) consumes them via a regular Go
// import path. Cross-package consumers would otherwise hit
// "internal" boundary errors.
//
// Phase ownership:
//
//	P1 (RM-3): ExpectedMXLPrimaryValue.
//	P2 (VAL-1): TBD.
//	P3 (VAL-2): TBD.
//	P4 (VAL-3 P3): ExpectedEQIXPrimaryValue, ExpectedEQIXForwardValue,
//	  ExpectedPLDPrimaryValue, ExpectedPLDForwardValue.
//
// JPM bit-for-bit is NOT pinned via this file — the legacy DDM golden lives
// at internal/services/valuation/models/testdata/golden/jpm_ddm_pre_tier2_output.json
// and is consumed via testhelpers.LoadGoldenJPMPrimaryValue.
const (
	// ExpectedMXLPrimaryValue is the IntrinsicValuePerShare produced by
	// RevenueMultipleModel.Calculate when invoked with the canonical
	// MXL fixture (profile/testhelpers/fixtures.go::BuildMXLModelInput)
	// and the cyclical_trough:standard_growth profile. Captured from
	// the Tier-2-P1 implementation; regenerate via the workflow above
	// if revenue_multiple.go's trailing math changes.
	//
	// The pin asserts the *trailing* IntrinsicValuePerShare (the legacy
	// math) — adding the forward path is additive, so trailing must not
	// drift. The forward value is a separate field (ForwardValue) and is
	// not pinned at the float-equality level in P1 because the calibration
	// of HorizonYears / TerminalMultiple is still evolving.
	ExpectedMXLPrimaryValue = 9.14634146341463 // captured 2026-05-16, MXL @ cyclical_trough:standard_growth

	// ExpectedEQIXPrimaryValue pins the FFO model's IntrinsicValuePerShare
	// for the synthetic data-center REIT (EQIX-ish) under the
	// reit_datacenter:high_growth profile (horizon=5, terminal=28.0).
	// Tier 2 P4 (VAL-3 P3 forward FFO). Re-captured 2026-05-19 against the
	// post-T2-P4-W1 master where industry_multiples.json keys REIT_DATACENTER
	// at 31.0 P/FFO (previous pin 386.84 reflected the pre-T2-P4-W1 default
	// 15× fallback because the bare DATA_CENTER key didn't resolve).
	ExpectedEQIXPrimaryValue = 799.473684210526

	// ExpectedEQIXForwardValue pins the additive forward FFO leg for the
	// same input. Diagnostic-only; the primary value remains trailing.
	// Unchanged across T2-P4-W1 because the forward path uses
	// Profile.TerminalMultiple (28.0) directly, not industry_multiples.json.
	ExpectedEQIXForwardValue = 741.6674862247

	// ExpectedPLDPrimaryValue pins the FFO model's IntrinsicValuePerShare
	// for the synthetic industrial REIT (PLD-ish) under the
	// reit_industrial:standard_growth profile (horizon=3, terminal=22.5).
	// Tier 2 P4 (VAL-3 P3 forward FFO). Re-captured 2026-05-19 against the
	// post-T2-P4-W1 master where industry_multiples.json keys REIT_INDUSTRIAL
	// at 22.5 P/FFO (previous pin 62.84 reflected the pre-T2-P4-W1 default
	// 15× fallback because the bare INDUSTRIAL key didn't resolve).
	ExpectedPLDPrimaryValue = 94.2567567567568

	// ExpectedPLDForwardValue pins the additive forward FFO leg for the
	// PLD input. Diagnostic-only. Unchanged across T2-P4-W1 (same reason
	// as ExpectedEQIXForwardValue).
	ExpectedPLDForwardValue = 85.4184686502213
)
