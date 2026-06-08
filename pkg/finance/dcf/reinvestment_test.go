package dcf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Layer A — reinvestment / operating-leverage model tests.
//
// Spec: docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md
// §5 (model), §7 (guardrails), §11.1 (golden tests). These prove the unified
// reinvestment term lets FCF cross negative→positive WITHIN the explicit horizon
// for a reinvestment-heavy firm, the implied capex-intensity declines, the
// maintenance-capex floor clamps, terminal reinvestment is consistency-derived,
// and the legacy_proportional opt-out stays bit-for-bit.

// salesToCapBaseInputs is the canonical Layer-A fixture. Hand-computed FCF
// series under sales_to_capital: [-276.5714, +279.4667, +902.7506, +2072.553,
// +2711.804] — crosses positive at year 2, every year ≥ the prior. Implied
// capex-intensity (reinvest/revenue) is monotonically declining. (See the
// per-year arithmetic in the spec-derived comment in the cross-positive test.)
func salesToCapBaseInputs() Inputs {
	return Inputs{
		BaseOperatingIncome:    1000.0, // = BaseRevenue × BaseOperatingMargin (10000 × 0.10)
		GrowthRates:            []float64{0.20, 0.20, 0.20, 0.10, 0.10},
		TerminalGrowthRate:     0.03,
		WACC:                   0.10,
		TaxRate:                0.20,
		ProjectionYears:        5,
		ReinvestmentMethod:     "sales_to_capital",
		BaseRevenue:            10000.0,
		SalesToCapitalStart:    1.0,
		SalesToCapitalTarget:   3.0,
		ReinvestmentFadeYears:  5,
		MaintenanceCapexFloor:  0.005, // low enough that it never binds in this fixture
		BaseOperatingMargin:    0.10,
		TargetOperatingMargin:  0.20,
		MarginConvergenceYears: 5,
	}
}

// TestValidateInputs_NearTermOverride_RejectsOutOfPrefixYear is the MEDIUM-5
// pin: dcf.Inputs is EXPORTED, so a direct caller could pass a
// NearTermReinvestmentOverride / NearTermMarginOverride keyed at year 3+ (or
// year 0), bypassing the service-seam §9.3 near-term-prefix refusal. The engine
// must enforce its OWN contract: validateInputs rejects any override year < 1 or
// > 2 (only years 1 and 2 are valid near-term anchors).
func TestValidateInputs_NearTermOverride_RejectsOutOfPrefixYear(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(in *Inputs)
		wantError bool
	}{
		{"reinvestment year 1 ok", func(in *Inputs) { in.NearTermReinvestmentOverride = map[int]float64{1: 100} }, false},
		{"reinvestment year 2 ok", func(in *Inputs) { in.NearTermReinvestmentOverride = map[int]float64{2: 100} }, false},
		{"reinvestment year 3 rejected", func(in *Inputs) { in.NearTermReinvestmentOverride = map[int]float64{3: 100} }, true},
		{"reinvestment year 0 rejected", func(in *Inputs) { in.NearTermReinvestmentOverride = map[int]float64{0: 100} }, true},
		{"margin year 2 ok", func(in *Inputs) { in.NearTermMarginOverride = map[int]float64{2: 0.3} }, false},
		{"margin year 3 rejected", func(in *Inputs) { in.NearTermMarginOverride = map[int]float64{3: 0.3} }, true},
		{"margin year 0 rejected", func(in *Inputs) { in.NearTermMarginOverride = map[int]float64{0: 0.3} }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := salesToCapBaseInputs()
			tc.mutate(&in)
			_, err := CalculateDCF(in)
			if tc.wantError {
				require.Error(t, err, "an out-of-prefix near-term override must be rejected by the engine")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCalculateDCF_SalesToCapital_CrossesPositiveInWindow(t *testing.T) {
	// Per-year hand arithmetic (revenue grows at GrowthRates; margin converges
	// 0.10→0.20 linearly over 5y; sales-to-capital rises 1.0→3.0 over 5y):
	//   y1 rev=12000 m=0.12 nopat=1152.0  reinv=2000/1.4=1428.571  fcf=-276.571
	//   y2 rev=14400 m=0.14 nopat=1612.8  reinv=2400/1.8=1333.333  fcf=+279.467
	//   y3 rev=17280 m=0.16 nopat=2211.84 reinv=2880/2.2=1309.091  fcf=+902.749
	//   y4 rev=19008 m=0.18 nopat=2737.15 reinv=1728/2.6= 664.615  fcf=+2072.536
	//   y5 rev=20908.8 m=0.20 nopat=3345.408 reinv=1900.8/3=633.6  fcf=+2711.808
	result, err := CalculateDCF(salesToCapBaseInputs())
	require.NoError(t, err)
	require.Len(t, result.Projections, 5)

	wantFCF := []float64{-276.5714, 279.4667, 902.7491, 2072.5363, 2711.808}
	for i, w := range wantFCF {
		assert.InDelta(t, w, result.Projections[i].FreeCashFlow, 0.05,
			"year %d FCF", i+1)
	}

	// §5.7(1): FCF transitions negative→positive BEFORE the terminal year.
	require.Negative(t, result.Projections[0].FreeCashFlow, "year 1 starts negative")
	crossed := false
	for _, p := range result.Projections {
		if p.FreeCashFlow > 0 {
			crossed = true
			break
		}
	}
	assert.True(t, crossed, "FCF must cross positive within the explicit horizon")

	// §5.7(1): no year is MORE negative than the prior (the legacy sign-lock
	// failure was each year strictly more negative).
	for i := 1; i < len(result.Projections); i++ {
		assert.GreaterOrEqual(t, result.Projections[i].FreeCashFlow, result.Projections[i-1].FreeCashFlow,
			"year %d FCF must not be more negative than year %d", i+1, i)
	}

	assert.Positive(t, result.EnterpriseValue, "EV positive")
}

func TestCalculateDCF_SalesToCapital_CapexIntensityDeclines(t *testing.T) {
	// §5.7(2) / §11.1(2): implied reinvestment intensity (reinvest_t / revenue_t)
	// is monotonically non-increasing toward the mature norm.
	in := salesToCapBaseInputs()
	result, err := CalculateDCF(in)
	require.NoError(t, err)

	// Reconstruct revenue_t to derive the intensity from FCF: reinvest = nopat - fcf.
	rev := in.BaseRevenue
	prevIntensity := 1e18
	for i, p := range result.Projections {
		g := in.GrowthRates[i]
		rev *= (1 + g)
		reinvest := p.NOPAT - p.FreeCashFlow
		intensity := reinvest / rev
		assert.LessOrEqual(t, intensity, prevIntensity+1e-9,
			"year %d reinvestment intensity %.5f must be ≤ prior %.5f", i+1, intensity, prevIntensity)
		prevIntensity = intensity
	}
}

func TestCalculateDCF_SalesToCapital_TerminalConsistency(t *testing.T) {
	// §7.3: terminal reinvestment derived from terminal growth + matured
	// sales-to-capital (NOT the final explicit-year FCF × (1+g)). Damodaran
	// stable-growth FCFF — reinvestment funds the NEXT period's growth off the
	// perpetuity base, so RR = g/ROIC exactly. With the base fixture:
	//   rev6 = 20908.8 × 1.03 = 21536.064
	//   nopat6 = 21536.064 × 0.20 × 0.80 = 3445.770
	//   reinv6 = 21536.064 × 0.03 / 3.0 = 215.361   (pure g/ROIC; the §7.1 floor is
	//                                                 NOT applied at the terminal)
	//   terminalFCF = 3445.770 − 215.361 = 3230.410
	//   gordonTV = 3230.410 / (0.10 − 0.03) = 46148.71
	result, err := CalculateDCF(salesToCapBaseInputs())
	require.NoError(t, err)

	assert.InDelta(t, 46148.71, result.GordonTV, 1.0, "Gordon TV must use consistency-derived terminal FCF")
	// Gordon-only path (no exit multiple): TerminalValueNominal == GordonTV.
	assert.InDelta(t, result.GordonTV, result.TerminalValueNominal, 1e-6)

	// The implied terminal reinvestment rate equals terminal_growth / terminal_ROIC
	// by construction. ROIC_terminal = after-tax target margin × S2C target =
	// 0.20×0.80×3.0 = 0.48 ⇒ RR = 0.03/0.48 = 0.0625. Verify the terminal FCF the
	// Gordon TV implies reflects RR=0.0625 of terminal NOPAT.
	terminalNOPAT := 21536.064 * 0.20 * 0.80
	impliedTerminalFCF := result.GordonTV * (result.WACC - result.TerminalGrowthRate)
	impliedRR := 1 - impliedTerminalFCF/terminalNOPAT
	assert.InDelta(t, 0.0625, impliedRR, 1e-3, "terminal reinvestment rate must equal g/ROIC")
}

func TestCalculateDCF_MaintenanceCapexFloor_Clamps(t *testing.T) {
	// §7.1: reinvestment may not fall below MaintenanceCapexFloor × Revenue.
	// Use a high floor (0.30 of revenue) so the modeled (small) late-year
	// reinvestment is clamped up — proving the floor binds and warns.
	in := salesToCapBaseInputs()
	in.MaintenanceCapexFloor = 0.30
	result, err := CalculateDCF(in)
	require.NoError(t, err)

	rev := in.BaseRevenue
	for i, p := range result.Projections {
		rev *= (1 + in.GrowthRates[i])
		reinvest := p.NOPAT - p.FreeCashFlow
		assert.GreaterOrEqual(t, reinvest, 0.30*rev-1e-6,
			"year %d reinvestment must respect the maintenance floor", i+1)
	}

	hasFloorWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "maintenance_capex_floor") {
			hasFloorWarning = true
		}
	}
	assert.True(t, hasFloorWarning, "a maintenance_capex_floor clamp warning must be emitted, got %v", result.Warnings)
}

func TestCalculateDCF_DecliningCapexIntensity_CrossesPositive(t *testing.T) {
	// §5.4 fallback path. NetReinvestIntensity declines 0.18 → 0.04 over 5y.
	//   reinvest_t = intensity_t × revenue_t ; FCF = NOPAT − reinvest.
	in := salesToCapBaseInputs()
	in.ReinvestmentMethod = "declining_capex_intensity"
	in.CapExIntensityStart = 0.18
	in.CapExIntensityMature = 0.04
	result, err := CalculateDCF(in)
	require.NoError(t, err)

	// Intensity declines by construction; assert reinvest_t/revenue_t falls.
	rev := in.BaseRevenue
	prev := 1e18
	for i, p := range result.Projections {
		rev *= (1 + in.GrowthRates[i])
		intensity := (p.NOPAT - p.FreeCashFlow) / rev
		assert.LessOrEqual(t, intensity, prev+1e-9, "year %d intensity declines", i+1)
		prev = intensity
	}
	// Late years turn positive as intensity falls below the NOPAT margin.
	assert.Positive(t, result.Projections[4].FreeCashFlow, "year 5 FCF positive once intensity matures")
}

func TestCalculateDCF_LegacyProportional_BitForBit(t *testing.T) {
	// §11.1(6) / §13: the legacy_proportional opt-out (and the empty-method
	// default) must reproduce the pre-Layer-A result EXACTLY, even when the new
	// Layer-A fields are populated. Run a true-FCF fixture twice — once with the
	// new fields absent, once with them present but method=legacy_proportional —
	// and assert byte-identical projections + EV.
	legacyBaseline := Inputs{
		BaseOperatingIncome:         1000.0,
		GrowthRate:                  0.10,
		TerminalGrowthRate:          0.025,
		WACC:                        0.10,
		TaxRate:                     0.25,
		ProjectionYears:             5,
		UseTrueFCF:                  true,
		DepreciationAndAmortization: 200.0,
		CapitalExpenditures:         300.0,
		NetWorkingCapitalChange:     50.0,
	}
	baseline, err := CalculateDCF(legacyBaseline)
	require.NoError(t, err)

	optOut := legacyBaseline
	optOut.ReinvestmentMethod = "legacy_proportional"
	// Populate the Layer-A fields with non-trivial values that MUST be ignored.
	optOut.BaseRevenue = 8000
	optOut.SalesToCapitalStart = 1.5
	optOut.SalesToCapitalTarget = 4.0
	optOut.ReinvestmentFadeYears = 7
	optOut.MaintenanceCapexFloor = 0.03
	optOut.BaseOperatingMargin = 0.125
	optOut.TargetOperatingMargin = 0.30
	optOut.MarginConvergenceYears = 7
	got, err := CalculateDCF(optOut)
	require.NoError(t, err)

	require.Len(t, got.Projections, len(baseline.Projections))
	for i := range baseline.Projections {
		assert.Equal(t, baseline.Projections[i].FreeCashFlow, got.Projections[i].FreeCashFlow,
			"year %d FCF must be bit-for-bit identical under legacy_proportional", i+1)
		assert.Equal(t, baseline.Projections[i].NOPAT, got.Projections[i].NOPAT)
		assert.Equal(t, baseline.Projections[i].OperatingIncome, got.Projections[i].OperatingIncome)
	}
	assert.Equal(t, baseline.EnterpriseValue, got.EnterpriseValue, "EV bit-for-bit")
	assert.Equal(t, baseline.TerminalValueNominal, got.TerminalValueNominal, "terminal nominal bit-for-bit")
}

func TestCalculateDCF_ReinvestmentModel_FallsBackWhenNoRevenue(t *testing.T) {
	// Guard: a non-legacy method with BaseRevenue == 0 must NOT engage the new
	// path (it has no revenue series to project) — it falls through to the
	// legacy projection so the engine never divides by a missing base.
	in := salesToCapBaseInputs()
	in.BaseRevenue = 0 // missing revenue base
	result, err := CalculateDCF(in)
	require.NoError(t, err)
	// With no true-FCF inputs and no revenue, legacy path yields FCF == NOPAT.
	for i, p := range result.Projections {
		assert.Equal(t, p.NOPAT, p.FreeCashFlow, "year %d falls back to NOPAT when revenue base missing", i+1)
	}
}
