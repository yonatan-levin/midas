package params

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Named-constant parity tests (plan §3.3 prime directive)
//
// Each assertion here MUST equal the literal/default it replaces in existing
// code. If a test fails it means a constant was accidentally changed during
// relocation — revert the constant, never the test.
// ---------------------------------------------------------------------------

// TestDefaultConstants_EqualVerifiedSourceValues asserts every named default
// constant equals the literal or Viper default it replaces.
//
// Verified against live code:
//   - DefaultTerminalGrowthCap   → service.go:1726   maxTerminalGrowth := 0.03
//   - DefaultTerminalGrowthFloor → service.go:1733   terminalGrowth = 0.02
//   - DefaultTerminalWACCSpread  → service.go:1737   wacc-0.02
//   - DefaultStage1Years         → growth/estimator.go:40  Stage1Years: 3
//   - DefaultStage2Years         → growth/estimator.go:41  Stage2Years: 4
//   - DefaultStage3Years         → growth/estimator.go:42  Stage3Years: 0
//   - DefaultMaxGrowthRate       → config.go:566  SetDefault("valuation.dcf_max_growth_rate", 0.5)
//     also growth/estimator.go:37  MaxGrowthRate: 0.5
//   - DefaultMinGrowthRate       → config.go:567  SetDefault("valuation.dcf_min_growth_rate", -0.3)
//     also growth/estimator.go:38  MinGrowthRate: -0.3
//   - DefaultTerminalMethod      → service.go:1109   terminalMethodLabel := "gordon_growth"
//     also profile/profile.go:67  TerminalGordonGrowth = "gordon_growth"
func TestDefaultConstants_EqualVerifiedSourceValues(t *testing.T) {
	t.Run("DefaultTerminalGrowthCap", func(t *testing.T) {
		assert.Equal(t, 0.03, DefaultTerminalGrowthCap,
			"must match maxTerminalGrowth := 0.03 at service.go:1726")
	})

	t.Run("DefaultTerminalGrowthFloor", func(t *testing.T) {
		assert.Equal(t, 0.02, DefaultTerminalGrowthFloor,
			"must match inline 0.02 inflation floor at service.go:1733")
	})

	t.Run("DefaultTerminalWACCSpread", func(t *testing.T) {
		assert.Equal(t, 0.02, DefaultTerminalWACCSpread,
			"must match wacc-0.02 guard at service.go:1737")
	})

	t.Run("DefaultTerminalGrowthDegenWACCFloor", func(t *testing.T) {
		assert.Equal(t, 0.01, DefaultTerminalGrowthDegenWACCFloor,
			"must match the inner post-WACC-spread floor `if terminalGrowth < 0.01` at service.go:1739")
	})

	t.Run("DegenWACCFloor_DistinctFromGrowthFloor", func(t *testing.T) {
		// Carry-forward I1: the post-WACC-spread degenerate floor (0.01) must NOT
		// be merged with the ≤0 inflation floor (0.02). Using 0.02 for the inner
		// branch would break byte-identity in low-WACC rows.
		assert.NotEqual(t, DefaultTerminalGrowthFloor, DefaultTerminalGrowthDegenWACCFloor,
			"the degenerate-WACC floor (0.01) and the inflation floor (0.02) must stay distinct")
	})

	t.Run("DefaultStage1Years", func(t *testing.T) {
		assert.Equal(t, 3, DefaultStage1Years,
			"must match DefaultEstimatorConfig().Stage1Years at growth/estimator.go:40")
	})

	t.Run("DefaultStage2Years", func(t *testing.T) {
		assert.Equal(t, 4, DefaultStage2Years,
			"must match DefaultEstimatorConfig().Stage2Years at growth/estimator.go:41")
	})

	t.Run("DefaultStage3Years", func(t *testing.T) {
		assert.Equal(t, 0, DefaultStage3Years,
			"must match DefaultEstimatorConfig().Stage3Years (legacy 7-year horizon signal) at growth/estimator.go:42")
	})

	t.Run("DefaultMaxGrowthRate", func(t *testing.T) {
		assert.Equal(t, 0.5, DefaultMaxGrowthRate,
			"must match viper.SetDefault(\"valuation.dcf_max_growth_rate\", 0.5) at config.go:566")
	})

	t.Run("DefaultMinGrowthRate", func(t *testing.T) {
		assert.Equal(t, -0.3, DefaultMinGrowthRate,
			"must match viper.SetDefault(\"valuation.dcf_min_growth_rate\", -0.3) at config.go:567")
	})

	t.Run("DefaultTerminalMethod", func(t *testing.T) {
		assert.Equal(t, "gordon_growth", DefaultTerminalMethod,
			"must match terminalMethodLabel := \"gordon_growth\" at service.go:1109")
	})
}

// ---------------------------------------------------------------------------
// Source enum sanity
// ---------------------------------------------------------------------------

func TestSource_Values(t *testing.T) {
	assert.Equal(t, Source("default"), SourceDefault)
	assert.Equal(t, Source("profile"), SourceProfile)
	assert.Equal(t, Source("request"), SourceRequest)
}

// ---------------------------------------------------------------------------
// ParamError tests
// ---------------------------------------------------------------------------

// TestParamError_Error_WithLimit confirms the Error() string includes the knob,
// value, reason, and limit when a limit is provided (HasLimit set).
func TestParamError_Error_WithLimit(t *testing.T) {
	err := &ParamError{
		Knob:     "terminal_growth_rate",
		Reason:   "must be strictly less than WACC",
		Value:    0.12,
		Limit:    0.094,
		HasLimit: true,
	}
	msg := err.Error()
	assert.Contains(t, msg, "terminal_growth_rate")
	assert.Contains(t, msg, "0.12")
	assert.Contains(t, msg, "must be strictly less than WACC")
	assert.Contains(t, msg, "0.094")
}

// TestParamError_Error_WithoutLimit confirms the Error() string is clean when
// no limit is applicable (e.g. structural or enum errors) — HasLimit unset.
func TestParamError_Error_WithoutLimit(t *testing.T) {
	err := &ParamError{
		Knob:   "terminal_method",
		Reason: "must be \"gordon_growth\" or \"exit_multiple\"",
		Value:  0,
	}
	msg := err.Error()
	assert.Contains(t, msg, "terminal_method")
	assert.NotContains(t, msg, "limit=", "an unset HasLimit must suppress the limit clause")
}

// TestParamError_Error_ZeroLimit confirms a REAL zero limit (HasLimit set, Limit
// exactly 0) is surfaced rather than dropped — the LOW sentinel fix. Before
// HasLimit, `Limit != 0` silently lost a legitimate zero threshold (e.g.
// min_growth > max_growth with max == 0).
func TestParamError_Error_ZeroLimit(t *testing.T) {
	err := &ParamError{
		Knob:     "min_growth_rate",
		Reason:   "must be ≤ max_growth_rate",
		Value:    0.5,
		Limit:    0,
		HasLimit: true,
	}
	msg := err.Error()
	assert.Contains(t, msg, "limit=0", "a real zero limit must be surfaced, not dropped")
}

// TestParamError_ImplementsError confirms *ParamError satisfies the error interface.
func TestParamError_ImplementsError(t *testing.T) {
	var err error = &ParamError{Knob: "beta", Reason: "out of range", Value: 99}
	require.NotNil(t, err)
	assert.NotEmpty(t, err.Error())
}

// TestParamError_ErrorsAs confirms errors.As can unwrap a *ParamError from a
// wrapped error chain, which is the handler's detection pattern.
func TestParamError_ErrorsAs_UnwrapsFromChain(t *testing.T) {
	inner := &ParamError{Knob: "min_growth_rate", Reason: "exceeds max_growth_rate", Value: 0.6, Limit: 0.5}
	wrapped := fmt.Errorf("resolver: %w", inner)

	var pe *ParamError
	require.True(t, errors.As(wrapped, &pe), "errors.As must find *ParamError in the chain")
	assert.Equal(t, "min_growth_rate", pe.Knob)
	assert.Equal(t, 0.6, pe.Value)
}

// TestIsParamError confirms the convenience helper returns true iff the error
// chain contains a *ParamError.
func TestIsParamError_TrueAndFalse(t *testing.T) {
	pe := &ParamError{Knob: "horizon_years", Reason: "exceeds stage sum", Value: 10}

	assert.True(t, IsParamError(pe), "direct *ParamError must be detected")
	assert.True(t, IsParamError(fmt.Errorf("wrap: %w", pe)), "wrapped *ParamError must be detected")
	assert.False(t, IsParamError(errors.New("unrelated error")), "unrelated error must not be detected")
	assert.False(t, IsParamError(nil), "nil error must not be detected")
}

// ---------------------------------------------------------------------------
// EffectiveValuationParams zero-value sanity
// ---------------------------------------------------------------------------

// TestEffectiveValuationParams_ZeroValue confirms the struct can be created with
// zero values and that the Provenance map starts nil (populated by Resolve*).
func TestEffectiveValuationParams_ZeroValue(t *testing.T) {
	var p EffectiveValuationParams
	assert.Nil(t, p.Provenance, "Provenance must be nil on zero-value struct (populated by Resolve*)")
	assert.False(t, p.TerminalGrowthExplicit)
}
