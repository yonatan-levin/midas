package params

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// production-representative Defaults: mirror today's config+estimator reads so an
// empty-override resolution reproduces the legacy path. cap=0.03, stages 3/4/0,
// growth bounds 0.5 / -0.3.
func legacyDefaults() Defaults {
	return Defaults{
		TerminalGrowthCap: DefaultTerminalGrowthCap,
		MaxGrowthRate:     DefaultMaxGrowthRate,
		MinGrowthRate:     DefaultMinGrowthRate,
		Stage1Years:       DefaultStage1Years,
		Stage2Years:       DefaultStage2Years,
		Stage3Years:       DefaultStage3Years,
		Beta:              1.1,
		RiskFreeRate:      0.045,
		MarketRiskPremium: 0.05,
		TaxRate:           0.21,
	}
}

func f64(v float64) *float64 { return &v }
func i(v int) *int           { return &v }
func str(v string) *string   { return &v }

// ---------------------------------------------------------------------------
// Precedence (plan §6: TestResolveInputs_Precedence_PerKnob)
// ---------------------------------------------------------------------------

// TestResolveInputs_Precedence_PerKnob verifies, per knob, that the precedence
// default ← profile ← override resolves to the expected value AND records the
// correct Provenance source. Knobs with no profile source assert config < override.
func TestResolveInputs_Precedence_PerKnob(t *testing.T) {
	t.Run("terminal_method config<profile<override", func(t *testing.T) {
		// default
		d := legacyDefaults()
		p, err := ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, DefaultTerminalMethod, p.TerminalMethod)
		assert.Equal(t, SourceDefault, p.Provenance[knobTerminalMethod])

		// profile
		d.ProfileTerminalMethod = "exit_multiple"
		d.ProfileTerminalMultiple = 12 // make exit_multiple resolvable
		p, err = ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, "exit_multiple", p.TerminalMethod)
		assert.Equal(t, SourceProfile, p.Provenance[knobTerminalMethod])

		// override wins
		p, err = ResolveInputs(d, Overrides{TerminalMethod: str("gordon_growth")}, 7)
		require.NoError(t, err)
		assert.Equal(t, "gordon_growth", p.TerminalMethod)
		assert.Equal(t, SourceRequest, p.Provenance[knobTerminalMethod])
	})

	t.Run("horizon_years config<profile<override", func(t *testing.T) {
		// default → growth-rate length
		d := legacyDefaults()
		p, err := ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, 7, p.HorizonYears)
		assert.Equal(t, SourceDefault, p.Provenance[knobHorizonYears])

		// profile (≤ stage-sum 7 and ≤ len 7, so no clamp)
		d.ProfileHorizonYears = 5
		p, err = ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, 5, p.HorizonYears)
		assert.Equal(t, SourceProfile, p.Provenance[knobHorizonYears])

		// override wins
		p, err = ResolveInputs(d, Overrides{HorizonYears: i(4)}, 7)
		require.NoError(t, err)
		assert.Equal(t, 4, p.HorizonYears)
		assert.Equal(t, SourceRequest, p.Provenance[knobHorizonYears])
	})

	t.Run("terminal_multiple industry<profile<override", func(t *testing.T) {
		d := legacyDefaults()
		d.IndustryExitMultiple = 10
		p, err := ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, 10.0, p.TerminalMultiple)
		assert.Equal(t, SourceDefault, p.Provenance[knobTerminalMultiple])

		d.ProfileTerminalMultiple = 13
		p, err = ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, 13.0, p.TerminalMultiple)
		assert.Equal(t, SourceProfile, p.Provenance[knobTerminalMultiple])

		p, err = ResolveInputs(d, Overrides{TerminalMultiple: f64(15)}, 7)
		require.NoError(t, err)
		assert.Equal(t, 15.0, p.TerminalMultiple)
		assert.Equal(t, SourceRequest, p.Provenance[knobTerminalMultiple])
	})

	// Knobs with NO profile source: assert config < override only.
	t.Run("max_growth_rate config<override (no profile)", func(t *testing.T) {
		d := legacyDefaults()
		p, err := ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, DefaultMaxGrowthRate, p.MaxGrowthRate)
		assert.Equal(t, SourceDefault, p.Provenance[knobMaxGrowthRate])

		p, err = ResolveInputs(d, Overrides{MaxGrowthRate: f64(0.8)}, 7)
		require.NoError(t, err)
		assert.Equal(t, 0.8, p.MaxGrowthRate)
		assert.Equal(t, SourceRequest, p.Provenance[knobMaxGrowthRate])
	})

	t.Run("min_growth_rate config<override (no profile)", func(t *testing.T) {
		d := legacyDefaults()
		p, err := ResolveInputs(d, Overrides{MinGrowthRate: f64(-0.5)}, 7)
		require.NoError(t, err)
		assert.Equal(t, -0.5, p.MinGrowthRate)
		assert.Equal(t, SourceRequest, p.Provenance[knobMinGrowthRate])
	})

	t.Run("beta config<override (no profile)", func(t *testing.T) {
		d := legacyDefaults()
		p, err := ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, 1.1, p.Beta)
		_, present := p.Provenance[knobBeta]
		assert.False(t, present, "untouched data-source knob has no provenance entry")

		p, err = ResolveInputs(d, Overrides{Beta: f64(1.8)}, 7)
		require.NoError(t, err)
		assert.Equal(t, 1.8, p.Beta)
		assert.Equal(t, SourceRequest, p.Provenance[knobBeta])
	})

	t.Run("risk_free_rate / market_risk_premium / tax_rate config<override", func(t *testing.T) {
		d := legacyDefaults()
		p, err := ResolveInputs(d, Overrides{
			RiskFreeRate:      f64(0.06),
			MarketRiskPremium: f64(0.07),
			TaxRate:           f64(0.15),
		}, 7)
		require.NoError(t, err)
		assert.Equal(t, 0.06, p.RiskFreeRate)
		assert.Equal(t, 0.07, p.MarketRiskPremium)
		assert.Equal(t, 0.15, p.TaxRate)
		assert.Equal(t, SourceRequest, p.Provenance[knobRiskFreeRate])
		assert.Equal(t, SourceRequest, p.Provenance["market_risk_premium"])
		assert.Equal(t, SourceRequest, p.Provenance[knobTaxRate])
	})

	t.Run("terminal_growth_cap config<override (no profile)", func(t *testing.T) {
		d := legacyDefaults()
		p, err := ResolveInputs(d, Overrides{}, 7)
		require.NoError(t, err)
		assert.Equal(t, DefaultTerminalGrowthCap, p.TerminalGrowthCap)
		assert.Equal(t, SourceDefault, p.Provenance[knobTerminalGrowthCap])

		p, err = ResolveInputs(d, Overrides{TerminalGrowthCap: f64(0.04)}, 7)
		require.NoError(t, err)
		assert.Equal(t, 0.04, p.TerminalGrowthCap)
		assert.Equal(t, SourceRequest, p.Provenance[knobTerminalGrowthCap])
	})

	t.Run("growth_stages config<override (no profile)", func(t *testing.T) {
		d := legacyDefaults()
		p, err := ResolveInputs(d, Overrides{
			Stage1Years: i(2), Stage2Years: i(3), Stage3Years: i(1),
		}, 6)
		require.NoError(t, err)
		assert.Equal(t, 2, p.Stage1Years)
		assert.Equal(t, 3, p.Stage2Years)
		assert.Equal(t, 1, p.Stage3Years)
		assert.Equal(t, SourceRequest, p.Provenance[knobStage1Years])
		assert.Equal(t, SourceRequest, p.Provenance[knobStage2Years])
		assert.Equal(t, SourceRequest, p.Provenance[knobStage3Years])
	})
}

// ---------------------------------------------------------------------------
// Per-invariant 422 (plan §6)
// ---------------------------------------------------------------------------

func assertParamErrorKnob(t *testing.T, err error, wantKnob string) {
	t.Helper()
	require.Error(t, err)
	var pe *ParamError
	require.True(t, errors.As(err, &pe), "expected *ParamError, got %T: %v", err, err)
	assert.Equal(t, wantKnob, pe.Knob)
}

func TestResolveInputs_Invariant_MinGreaterThanMax_Returns422(t *testing.T) {
	d := legacyDefaults()
	_, err := ResolveInputs(d, Overrides{MinGrowthRate: f64(0.6), MaxGrowthRate: f64(0.5)}, 7)
	assertParamErrorKnob(t, err, knobMinGrowthRate)
}

func TestResolveInputs_Invariant_StageSumBelowOne_Returns422(t *testing.T) {
	d := legacyDefaults()
	_, err := ResolveInputs(d, Overrides{
		Stage1Years: i(0), Stage2Years: i(0), Stage3Years: i(0),
	}, 7)
	assertParamErrorKnob(t, err, knobGrowthStages)
}

func TestResolveInputs_Invariant_HorizonExceedsStageSum_Returns422(t *testing.T) {
	d := legacyDefaults() // stage sum = 3+4+0 = 7
	// Request-sourced horizon of 8 > stage-sum 7. growthRateLen large enough that
	// the stage-sum check fires first.
	_, err := ResolveInputs(d, Overrides{HorizonYears: i(8)}, 20)
	assertParamErrorKnob(t, err, knobHorizonYears)
}

func TestResolveInputs_Invariant_HorizonExceedsGrowthLen_Returns422(t *testing.T) {
	d := legacyDefaults()
	// Request-sourced horizon 6 ≤ stage-sum 7 (passes stage check) but > growth len 5.
	_, err := ResolveInputs(d, Overrides{HorizonYears: i(6)}, 5)
	assertParamErrorKnob(t, err, knobHorizonYears)
}

// R2 nuance: a PROFILE-sourced horizon exceeding length CLAMPS + WARNs, never 422
// (preserves default-path byte-identity, mirrors service.go:1117).
func TestResolveInputs_ProfileHorizonExceedsLen_ClampsNot422(t *testing.T) {
	d := legacyDefaults()
	d.ProfileHorizonYears = 10 // > stage-sum 7 and > growth len 5
	p, err := ResolveInputs(d, Overrides{}, 5)
	require.NoError(t, err, "profile-sourced horizon must clamp, not 422")
	assert.Equal(t, 5, p.HorizonYears, "clamped to growth-rate length")
	require.NotEmpty(t, p.Warnings, "a clamp warning must be recorded")
}

// Exit-multiple resolvability is enforced in ResolveInputs (it is WACC-independent —
// it depends only on method + multiple + industry/profile default, all known at
// phase 1). See the note in resolve.go. The plan permitted placing it in either
// ResolveInputs or ResolveTerminal; we chose ResolveInputs and pin it here.
func TestResolveInputs_Invariant_ExitMultipleUnresolvable_Returns422(t *testing.T) {
	d := legacyDefaults() // IndustryExitMultiple = 0, no profile multiple
	_, err := ResolveInputs(d, Overrides{TerminalMethod: str("exit_multiple")}, 7)
	assertParamErrorKnob(t, err, knobTerminalMultiple)
}

func TestResolveInputs_ExitMultipleResolvableFromOverride_OK(t *testing.T) {
	d := legacyDefaults()
	p, err := ResolveInputs(d, Overrides{
		TerminalMethod:   str("exit_multiple"),
		TerminalMultiple: f64(14),
	}, 7)
	require.NoError(t, err)
	assert.Equal(t, "exit_multiple", p.TerminalMethod)
	assert.Equal(t, 14.0, p.TerminalMultiple)
}

// ---------------------------------------------------------------------------
// ResolveTerminal invariants
// ---------------------------------------------------------------------------

func TestResolveTerminal_Invariant_TerminalGrowthGEWACC_Returns422(t *testing.T) {
	p := EffectiveValuationParams{
		TerminalGrowthRate:     0.12,
		TerminalGrowthExplicit: true,
		Provenance:             map[string]Source{knobTerminalGrowthRate: SourceRequest},
	}
	err := ResolveTerminal(&p, 0.094, 0.10)
	assertParamErrorKnob(t, err, knobTerminalGrowthRate)
}

// An explicit terminal_growth_rate within the minimum WACC spread (but still below
// WACC) is now a HARD 422 (Layer-2, Part C-1), not a soft warning — the engine's
// Gordon denominator would otherwise be numerically unstable. This catches it as a
// typed *ParamError before CalculateDCF runs (never a 500).
func TestResolveTerminal_ExplicitNearWACC_Returns422(t *testing.T) {
	p := EffectiveValuationParams{
		TerminalGrowthRate:     0.089, // 0.5% below WACC 0.094 → inside the 1% spread
		TerminalGrowthExplicit: true,
		Provenance:             map[string]Source{knobTerminalGrowthRate: SourceRequest},
	}
	err := ResolveTerminal(&p, 0.094, 0.10)
	assertParamErrorKnob(t, err, knobTerminalGrowthRate)
}

// A value comfortably beyond MinTerminalWACCSpread below WACC is accepted as-is.
// WACC 0.10, spread 0.01 → boundary 0.09; 0.08 is clearly outside the guard.
func TestResolveTerminal_ExplicitBeyondSpread_OK(t *testing.T) {
	p := EffectiveValuationParams{
		TerminalGrowthRate:     0.08, // 0.02 below WACC 0.10 (> 0.01 spread)
		TerminalGrowthExplicit: true,
		Provenance:             map[string]Source{knobTerminalGrowthRate: SourceRequest},
	}
	err := ResolveTerminal(&p, 0.10, 0.10)
	require.NoError(t, err)
	assert.Equal(t, 0.08, p.TerminalGrowthRate, "explicit value used as-is")
}

func TestResolveTerminal_ExplicitComfortablyBelowWACC_NoWarn(t *testing.T) {
	p := EffectiveValuationParams{
		TerminalGrowthRate:     0.02,
		TerminalGrowthExplicit: true,
		Provenance:             map[string]Source{knobTerminalGrowthRate: SourceRequest},
	}
	err := ResolveTerminal(&p, 0.094, 0.10)
	require.NoError(t, err)
	assert.Empty(t, p.Warnings)
}

// Explicit path must NOT apply the cap (design §5 / §8 R8): a value above the cap
// but below WACC is honored as-is.
func TestResolveTerminal_ExplicitAboveCap_NotClamped(t *testing.T) {
	p := EffectiveValuationParams{
		TerminalGrowthRate:     0.05, // above the 0.03 cap, below WACC 0.10
		TerminalGrowthExplicit: true,
		TerminalGrowthCap:      0.03,
		Provenance:             map[string]Source{knobTerminalGrowthRate: SourceRequest},
	}
	err := ResolveTerminal(&p, 0.10, 0.04)
	require.NoError(t, err)
	assert.Equal(t, 0.05, p.TerminalGrowthRate, "cap must NOT apply on explicit path")
}

// ---------------------------------------------------------------------------
// BYTE-IDENTITY: ResolveTerminal auto-derive == legacy calculateTerminalGrowthRate
// ---------------------------------------------------------------------------

// legacyCalculateTerminalGrowthRate is an INLINE replica of the production
// function service.go::calculateTerminalGrowthRate (lines 1721-1745). We cannot
// import the valuation package (import cycle: valuation → params), so the
// reference implementation is duplicated here verbatim and used as the oracle.
func legacyCalculateTerminalGrowthRate(historicalCAGR, wacc float64) float64 {
	terminalGrowth := historicalCAGR / 2
	maxTerminalGrowth := 0.03 // 3%

	if terminalGrowth > maxTerminalGrowth {
		terminalGrowth = maxTerminalGrowth
	}

	if terminalGrowth <= 0 {
		terminalGrowth = 0.02
	}

	if wacc > 0 && terminalGrowth > wacc-0.02 {
		terminalGrowth = wacc - 0.02
		if terminalGrowth < 0.01 {
			terminalGrowth = 0.01
		}
	}

	return terminalGrowth
}

// TestResolveTerminal_EmptyOverride_MatchesCalculateTerminalGrowthRate proves the
// auto-derive path is byte-identical to the legacy function across a representative
// (historicalCAGR, wacc) grid, using math.Float64bits equality. The cap is left at
// DefaultTerminalGrowthCap (0.03) — the only condition under which byte-identity is
// guaranteed.
//
// The grid deliberately exercises:
//   - the cap branch (CAGR high → terminalGrowth > 0.03)
//   - the ≤0 inflation floor (CAGR ≤ 0 → 0.02)
//   - the WACC-spread clamp (terminalGrowth > wacc-0.02)
//   - the degenerate 0.01 floor (WACC < 0.02 so wacc-0.02 < 0.01, and WACC slightly
//     above 0.02 so wacc-0.02 lands between 0.01 and 0.02)
//
// The grid uses only POSITIVE WACCs: ResolveTerminal now returns a typed 422 for a
// non-positive WACC (Part C-2) BEFORE reaching the auto-derive arithmetic, and a
// production WACC is always positive (the dcf/wacc engine validators already reject
// WACC ≤ 0). The legacy oracle's wacc==0 "skip the spread clamp" branch is therefore
// unreachable through ResolveTerminal and is covered separately by the
// non-positive-WACC 422 test.
func TestResolveTerminal_EmptyOverride_MatchesCalculateTerminalGrowthRate(t *testing.T) {
	cagrs := []float64{-0.50, -0.10, 0.0, 0.005, 0.02, 0.04, 0.06, 0.10, 0.30, 1.00}
	waccs := []float64{0.005, 0.01, 0.015, 0.02, 0.025, 0.03, 0.05, 0.08, 0.094, 0.12, 0.20}

	for _, cagr := range cagrs {
		for _, wacc := range waccs {
			want := legacyCalculateTerminalGrowthRate(cagr, wacc)

			p := EffectiveValuationParams{
				TerminalGrowthCap: DefaultTerminalGrowthCap, // 0.03 — required for parity
				Provenance:        map[string]Source{},
			}
			err := ResolveTerminal(&p, wacc, cagr)
			require.NoError(t, err)
			got := p.TerminalGrowthRate

			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("byte-identity FAIL at cagr=%g wacc=%g: got %v (bits %x), want %v (bits %x)",
					cagr, wacc, got, math.Float64bits(got), want, math.Float64bits(want))
			}
			// Auto-derive path tags provenance as default.
			assert.Equal(t, SourceDefault, p.Provenance[knobTerminalGrowthRate])
		}
	}
}

// ---------------------------------------------------------------------------
// Part C-2: non-positive WACC → typed 422 (not an engine 500)
// ---------------------------------------------------------------------------

// TestResolveTerminal_NonPositiveWACC_Returns422 pins that a non-positive computed
// WACC (the degenerate output of an extreme CAPM-input override combo) is caught as
// a typed *ParamError on knob "wacc" BEFORE the auto-derive arithmetic or the engine
// runs — so the caller gets a 422, never an engine 500.
func TestResolveTerminal_NonPositiveWACC_Returns422(t *testing.T) {
	for _, wacc := range []float64{0.0, -0.01, -0.10} {
		t.Run("", func(t *testing.T) {
			// Auto-derive path (no explicit terminal growth).
			p := EffectiveValuationParams{
				TerminalGrowthCap: DefaultTerminalGrowthCap,
				Provenance:        map[string]Source{},
			}
			err := ResolveTerminal(&p, wacc, 0.10)
			assertParamErrorKnob(t, err, knobWACC)

			// Explicit path: the non-positive-WACC guard fires before the
			// terminal-growth spread check.
			pe := EffectiveValuationParams{
				TerminalGrowthRate:     0.01,
				TerminalGrowthExplicit: true,
				Provenance:             map[string]Source{knobTerminalGrowthRate: SourceRequest},
			}
			err = ResolveTerminal(&pe, wacc, 0.10)
			assertParamErrorKnob(t, err, knobWACC)
		})
	}
}

// ---------------------------------------------------------------------------
// MEDIUM-1: terminal_growth_cap is honored over the ≤0 inflation floor
// ---------------------------------------------------------------------------

// TestResolveTerminal_AutoDerive_CapHonoredOverFloor pins the MEDIUM-1 fix: the
// auto-derive path applies the ≤0 inflation FLOOR first, then the CAP, then re-caps
// after the WACC-spread adjustment — so an explicit low/negative terminal_growth_cap
// binds instead of being silently overshot by the 0.02 floor.
func TestResolveTerminal_AutoDerive_CapHonoredOverFloor(t *testing.T) {
	tests := []struct {
		name   string
		cap    float64
		cagr   float64
		wacc   float64
		wantTG float64
	}{
		{
			// Non-positive CAGR → floor 0.02, but cap 0.01 must bind below it.
			// Pre-fix bug: the floor produced 0.02, ABOVE the 0.01 cap.
			name:   "low cap 0.01 with non-positive CAGR binds below floor",
			cap:    0.01,
			cagr:   -0.10,
			wacc:   0.10,
			wantTG: 0.01,
		},
		{
			// Positive CAGR/2 = 0.05 > cap 0.01 → cap binds. Floor is a no-op.
			name:   "low cap 0.01 with positive CAGR binds",
			cap:    0.01,
			cagr:   0.10,
			wacc:   0.10,
			wantTG: 0.01,
		},
		{
			// Negative cap -0.01: floor lifts to 0.02, cap drops to -0.01, WACC-spread
			// raises to wacc-0.02=0.08 then degenerate-floor min is 0.01, re-cap drops
			// back to -0.01. The negative cap is a hard ceiling and must be honored.
			name:   "negative cap -0.01 is honored as a hard ceiling",
			cap:    -0.01,
			cagr:   -0.10,
			wacc:   0.10,
			wantTG: -0.01,
		},
		{
			// Default cap 0.03 with non-positive CAGR: floor 0.02 (< cap), unchanged
			// by the reorder — the byte-identity case re-asserted inline here.
			name:   "default cap 0.03 with non-positive CAGR resolves to floor",
			cap:    0.03,
			cagr:   -0.10,
			wacc:   0.10,
			wantTG: 0.02,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := EffectiveValuationParams{
				TerminalGrowthCap: tt.cap,
				Provenance:        map[string]Source{},
			}
			err := ResolveTerminal(&p, tt.wacc, tt.cagr)
			require.NoError(t, err)
			assert.InDelta(t, tt.wantTG, p.TerminalGrowthRate, 1e-12)
		})
	}
}

// ---------------------------------------------------------------------------
// BYTE-IDENTITY / default-path: ResolveInputs with empty overrides
// ---------------------------------------------------------------------------

// TestResolveInputs_EmptyOverrides_MatchesLegacyDefaults asserts that an
// empty-override resolution against production-representative Defaults reproduces
// today's reads exactly (the engine's default path).
func TestResolveInputs_EmptyOverrides_MatchesLegacyDefaults(t *testing.T) {
	d := legacyDefaults()
	growthLen := 7 // legacy: horizon falls through to growth-rate slice length
	p, err := ResolveInputs(d, Overrides{}, growthLen)
	require.NoError(t, err)

	assert.Equal(t, DefaultTerminalGrowthCap, p.TerminalGrowthCap)
	assert.Equal(t, DefaultMaxGrowthRate, p.MaxGrowthRate)
	assert.Equal(t, DefaultMinGrowthRate, p.MinGrowthRate)
	assert.Equal(t, DefaultStage1Years, p.Stage1Years)
	assert.Equal(t, DefaultStage2Years, p.Stage2Years)
	assert.Equal(t, DefaultStage3Years, p.Stage3Years)
	assert.Equal(t, DefaultTerminalMethod, p.TerminalMethod)
	assert.Equal(t, growthLen, p.HorizonYears, "default horizon = growth-rate length")
	assert.False(t, p.TerminalGrowthExplicit, "no explicit terminal growth on default path")
	assert.Equal(t, 1.1, p.Beta)
	assert.Equal(t, 0.045, p.RiskFreeRate)
	assert.Equal(t, 0.05, p.MarketRiskPremium)
	assert.Equal(t, 0.21, p.TaxRate)
	assert.Empty(t, p.Warnings, "clean default path raises no warnings")
}

// ---------------------------------------------------------------------------
// N1–N3 zero-sentinel pins (carry-forward)
// ---------------------------------------------------------------------------

// TestResolveInputs_ZeroSentinelFallbacks pins the defensive zero→const fallbacks
// the carry-forward N1–N3 requires: a zero Defaults.MinGrowthRate /
// .MaxGrowthRate / .Stage{1,2}Years resolves to the corresponding Default*
// constant. Stage3 zero is the legitimate legacy signal and stays 0.
func TestResolveInputs_ZeroSentinelFallbacks(t *testing.T) {
	// All-zero Defaults except enough to keep staging valid via fallbacks.
	d := Defaults{} // every field zero
	p, err := ResolveInputs(d, Overrides{}, 7)
	require.NoError(t, err)

	assert.Equal(t, DefaultMinGrowthRate, p.MinGrowthRate, "zero MinGrowthRate → DefaultMinGrowthRate")
	assert.Equal(t, DefaultMaxGrowthRate, p.MaxGrowthRate, "zero MaxGrowthRate → DefaultMaxGrowthRate")
	assert.Equal(t, DefaultStage1Years, p.Stage1Years, "zero Stage1Years → DefaultStage1Years")
	assert.Equal(t, DefaultStage2Years, p.Stage2Years, "zero Stage2Years → DefaultStage2Years")
	assert.Equal(t, DefaultStage3Years, p.Stage3Years, "zero Stage3Years stays 0 (legacy signal)")
	assert.Equal(t, DefaultTerminalGrowthCap, p.TerminalGrowthCap, "zero TerminalGrowthCap → DefaultTerminalGrowthCap")
}

// TestResolveInputs_ZeroMinGrowth_ResolvesToDefault is the explicit pin named in
// the carry-forward: Defaults.MinGrowthRate == 0 → resolved MinGrowthRate ==
// DefaultMinGrowthRate.
func TestResolveInputs_ZeroMinGrowth_ResolvesToDefault(t *testing.T) {
	d := legacyDefaults()
	d.MinGrowthRate = 0
	p, err := ResolveInputs(d, Overrides{}, 7)
	require.NoError(t, err)
	assert.Equal(t, DefaultMinGrowthRate, p.MinGrowthRate)
}

// ---------------------------------------------------------------------------
// validateStaging / ValidateEstimatorConfig parity
// ---------------------------------------------------------------------------

// TestValidateEstimatorConfig_MirrorsResolveInputs confirms the pre-estimator
// pre-check and ResolveInputs agree on the staging invariants (they share
// validateStaging, so they must never disagree).
func TestValidateEstimatorConfig_MirrorsResolveInputs(t *testing.T) {
	d := legacyDefaults()

	// Valid config: both pass.
	require.NoError(t, ValidateEstimatorConfig(d, Overrides{}))
	_, err := ResolveInputs(d, Overrides{}, 7)
	require.NoError(t, err)

	// min>max: both reject with the same knob.
	bad := Overrides{MinGrowthRate: f64(0.6), MaxGrowthRate: f64(0.5)}
	assertParamErrorKnob(t, ValidateEstimatorConfig(d, bad), knobMinGrowthRate)
	_, err = ResolveInputs(d, bad, 7)
	assertParamErrorKnob(t, err, knobMinGrowthRate)

	// stage-sum<1: both reject with the same knob.
	badStage := Overrides{Stage1Years: i(0), Stage2Years: i(0), Stage3Years: i(0)}
	assertParamErrorKnob(t, ValidateEstimatorConfig(d, badStage), knobGrowthStages)
	_, err = ResolveInputs(d, badStage, 7)
	assertParamErrorKnob(t, err, knobGrowthStages)
}
