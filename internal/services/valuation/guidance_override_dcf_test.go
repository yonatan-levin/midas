package valuation

// Engine-level NF1 guard for the Layer-B Phase-2 NearTermReinvestmentOverride
// seam lives in the valuation package (rather than pkg/finance/dcf) only to keep
// the new override-focused assertions grouped with the rest of the Phase-2
// suite; it exercises pkg/finance/dcf.CalculateDCF directly.

import (
	"math"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/authority"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
)

// salesToCapInputs mirrors the canonical Layer-A reinvestment fixture
// (pkg/finance/dcf/reinvestment_test.go::salesToCapBaseInputs) so the override
// is exercised on the active reinvestment path.
func salesToCapInputs() dcf.Inputs {
	return dcf.Inputs{
		BaseOperatingIncome:    1000.0,
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
		MaintenanceCapexFloor:  0.005,
		BaseOperatingMargin:    0.10,
		TargetOperatingMargin:  0.20,
		MarginConvergenceYears: 5,
	}
}

// TestCalculateDCF_NearTermOverride_NilIsByteIdentical pins the engine-level NF1
// invariant: a nil NearTermReinvestmentOverride produces a bit-for-bit identical
// EnterpriseValue + per-year FCF to the same inputs with the field absent. This
// is the engine half of the absent-guidance byte-identity guarantee.
func TestCalculateDCF_NearTermOverride_NilIsByteIdentical(t *testing.T) {
	base := salesToCapInputs()

	withNil := salesToCapInputs()
	withNil.NearTermReinvestmentOverride = nil

	rBase, err := dcf.CalculateDCF(base)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	rNil, err := dcf.CalculateDCF(withNil)
	if err != nil {
		t.Fatalf("nil-override: %v", err)
	}

	if math.Float64bits(rBase.EnterpriseValue) != math.Float64bits(rNil.EnterpriseValue) {
		t.Fatalf("nil override drifted EV: %v vs %v", rBase.EnterpriseValue, rNil.EnterpriseValue)
	}
	for i := range rBase.Projections {
		if math.Float64bits(rBase.Projections[i].FreeCashFlow) != math.Float64bits(rNil.Projections[i].FreeCashFlow) {
			t.Fatalf("year %d FCF drifted: %v vs %v", i+1, rBase.Projections[i].FreeCashFlow, rNil.Projections[i].FreeCashFlow)
		}
	}
}

// TestCalculateDCF_NearTermOverride_EmptyMapIsByteIdentical confirms an
// allocated-but-empty map is also a strict no-op (the year-lookup miss path).
func TestCalculateDCF_NearTermOverride_EmptyMapIsByteIdentical(t *testing.T) {
	base := salesToCapInputs()
	withEmpty := salesToCapInputs()
	withEmpty.NearTermReinvestmentOverride = map[int]float64{}

	rBase, _ := dcf.CalculateDCF(base)
	rEmpty, _ := dcf.CalculateDCF(withEmpty)
	if math.Float64bits(rBase.EnterpriseValue) != math.Float64bits(rEmpty.EnterpriseValue) {
		t.Fatalf("empty-map override drifted EV: %v vs %v", rBase.EnterpriseValue, rEmpty.EnterpriseValue)
	}
}

// TestCalculateDCF_NearTermOverride_ChangesTargetYearOnly confirms a year-1
// override REPLACES year-1 reinvestment (changing year-1 FCF) while leaving
// years 2..N FCF untouched — the structural near-term-only effect.
func TestCalculateDCF_NearTermOverride_ChangesTargetYearOnly(t *testing.T) {
	base, _ := dcf.CalculateDCF(salesToCapInputs())

	withOverride := salesToCapInputs()
	// Force year-1 reinvestment to 0 ⇒ year-1 FCF jumps up by the previously
	// modeled reinvestment amount; later years stay put.
	withOverride.NearTermReinvestmentOverride = map[int]float64{1: 0}
	got, err := dcf.CalculateDCF(withOverride)
	if err != nil {
		t.Fatalf("override: %v", err)
	}

	// Year 1 FCF = NOPAT - 0 = NOPAT; must differ from the modeled FCF.
	if got.Projections[0].FreeCashFlow == base.Projections[0].FreeCashFlow {
		t.Fatal("year-1 override did not change year-1 FCF")
	}
	if got.Projections[0].FreeCashFlow != got.Projections[0].NOPAT {
		t.Fatalf("year-1 FCF should equal NOPAT with zero reinvestment override; got fcf=%v nopat=%v",
			got.Projections[0].FreeCashFlow, got.Projections[0].NOPAT)
	}
	// Years 2..5 FCF must be bit-for-bit identical to the un-overridden run.
	for i := 1; i < len(base.Projections); i++ {
		if math.Float64bits(got.Projections[i].FreeCashFlow) != math.Float64bits(base.Projections[i].FreeCashFlow) {
			t.Fatalf("year %d FCF changed by a year-1 override: %v vs %v",
				i+1, got.Projections[i].FreeCashFlow, base.Projections[i].FreeCashFlow)
		}
	}
}

// TestCapExAnchor_GrossAsNet_Year1ReinvestmentEqualsMidpoint pins the MEDIUM-2
// Phase-2 interpretation of a CapEx guidance anchor: the anchored capex MIDPOINT
// is used DIRECTLY as the year-1 NET reinvestment term (the gross-as-net
// approximation). It asserts year-1 FCF == NOPAT − capexMidpoint, so Phase 3
// inherits a pinned contract for the value it must preserve (or deliberately
// change when it introduces the precise gross→net conversion).
//
// This drives the real applyNearTermAnchors seam (CapExYear1 ⇒
// NearTermReinvestmentOverride[1]) followed by CalculateDCF, so the pin covers
// the whole anchor → engine path, not just the engine override read.
func TestCapExAnchor_GrossAsNet_Year1ReinvestmentEqualsMidpoint(t *testing.T) {
	const capexMidpoint = 1.5e9 // e.g. midpoint of [1.4B, 1.6B]

	in := salesToCapInputs()
	cap1 := capexMidpoint
	applyNearTermAnchors(&in, authority.NearTermAnchors{CapExYear1: &cap1})

	// The anchor must land in the year-1 reinvestment override verbatim
	// (gross-as-net: no D&A conversion in Phase 2).
	if got := in.NearTermReinvestmentOverride[1]; got != capexMidpoint {
		t.Fatalf("year-1 reinvestment override = %v, want anchored midpoint %v (gross-as-net)", got, capexMidpoint)
	}

	res, err := dcf.CalculateDCF(in)
	if err != nil {
		t.Fatalf("CalculateDCF: %v", err)
	}
	// Year-1 FCF = NOPAT − reinvestment, and reinvestment == the anchored capex
	// midpoint exactly (the gross-as-net Phase-2 contract Phase 3 inherits).
	wantFCF := res.Projections[0].NOPAT - capexMidpoint
	if math.Float64bits(res.Projections[0].FreeCashFlow) != math.Float64bits(wantFCF) {
		t.Fatalf("year-1 FCF = %v, want NOPAT − capexMidpoint = %v (reinvestment must equal the anchored midpoint)",
			res.Projections[0].FreeCashFlow, wantFCF)
	}
}

// TestCalculateDCF_NearTermMarginOverride_NilIsByteIdentical pins the engine-level
// NF1 invariant for the LOW-1 margin seam: a nil NearTermMarginOverride is a
// strict no-op (bit-for-bit identical EV + per-year FCF).
func TestCalculateDCF_NearTermMarginOverride_NilIsByteIdentical(t *testing.T) {
	base := salesToCapInputs()
	withNil := salesToCapInputs()
	withNil.NearTermMarginOverride = nil

	rBase, err := dcf.CalculateDCF(base)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	rNil, err := dcf.CalculateDCF(withNil)
	if err != nil {
		t.Fatalf("nil margin override: %v", err)
	}
	if math.Float64bits(rBase.EnterpriseValue) != math.Float64bits(rNil.EnterpriseValue) {
		t.Fatalf("nil margin override drifted EV: %v vs %v", rBase.EnterpriseValue, rNil.EnterpriseValue)
	}
	for i := range rBase.Projections {
		if math.Float64bits(rBase.Projections[i].FreeCashFlow) != math.Float64bits(rNil.Projections[i].FreeCashFlow) {
			t.Fatalf("year %d FCF drifted under nil margin override: %v vs %v",
				i+1, rBase.Projections[i].FreeCashFlow, rNil.Projections[i].FreeCashFlow)
		}
	}
}

// TestCalculateDCF_NearTermMarginOverride_ChangesTargetYearOnly confirms a
// year-2 margin override REPLACES the year-2 converged margin (changing year-2
// OperatingIncome/NOPAT/FCF) while leaving every other year bit-for-bit
// untouched — the structural near-term-only effect.
func TestCalculateDCF_NearTermMarginOverride_ChangesTargetYearOnly(t *testing.T) {
	base, _ := dcf.CalculateDCF(salesToCapInputs())

	withOverride := salesToCapInputs()
	// Force year-2 margin to a distinct value well away from the converged path.
	const year2Margin = 0.40
	withOverride.NearTermMarginOverride = map[int]float64{2: year2Margin}
	got, err := dcf.CalculateDCF(withOverride)
	if err != nil {
		t.Fatalf("margin override: %v", err)
	}

	// Year-2 operating income = revenue × overridden margin.
	if got.Projections[1].OperatingIncome == base.Projections[1].OperatingIncome {
		t.Fatal("year-2 margin override did not change year-2 operating income")
	}
	// Every year EXCEPT year 2 must be bit-for-bit identical.
	for i := range base.Projections {
		if i == 1 {
			continue
		}
		if math.Float64bits(got.Projections[i].FreeCashFlow) != math.Float64bits(base.Projections[i].FreeCashFlow) {
			t.Fatalf("year %d FCF changed by a year-2 margin override: %v vs %v",
				i+1, got.Projections[i].FreeCashFlow, base.Projections[i].FreeCashFlow)
		}
	}
}

// TestApplyNearTermAnchors_RevenueAnchor_ExtendsShortSlice is the MEDIUM-4 pin:
// a revenue-growth anchor must actually MOVE year-1 even when RevenueGrowthRates
// is empty or shorter than year 1 — previously the resolver recorded a guidance
// source + warning but the DCF input was silently UNCHANGED (the write was
// skipped because yearIndex >= len(slice)). The fix extends the slice to
// yearIndex+1, seeding any missing years from GrowthRates[i] (else GrowthRate),
// then writes the anchor.
func TestApplyNearTermAnchors_RevenueAnchor_ExtendsShortSlice(t *testing.T) {
	t.Run("empty RevenueGrowthRates ⇒ extended + year-1 set", func(t *testing.T) {
		in := salesToCapInputs()
		in.RevenueGrowthRates = nil // empty: the silent no-op case
		in.GrowthRates = []float64{0.20, 0.18, 0.15, 0.12, 0.10}
		in.GrowthRate = 0.05

		g := 0.35
		applyNearTermAnchors(&in, authority.NearTermAnchors{RevenueGrowthYear1: &g})

		if len(in.RevenueGrowthRates) < 1 {
			t.Fatalf("revenue anchor must extend RevenueGrowthRates to at least length 1; got len %d", len(in.RevenueGrowthRates))
		}
		if in.RevenueGrowthRates[0] != g {
			t.Fatalf("year-1 revenue anchor not applied to extended slice: got %v want %v", in.RevenueGrowthRates[0], g)
		}
	})

	t.Run("year-2 anchor seeds missing year-1 from GrowthRates", func(t *testing.T) {
		in := salesToCapInputs()
		in.RevenueGrowthRates = nil
		in.GrowthRates = []float64{0.20, 0.18, 0.15, 0.12, 0.10}
		in.GrowthRate = 0.05

		g2 := 0.30
		applyNearTermAnchors(&in, authority.NearTermAnchors{RevenueGrowthYear2: &g2})

		if len(in.RevenueGrowthRates) < 2 {
			t.Fatalf("year-2 anchor must extend RevenueGrowthRates to length >= 2; got %d", len(in.RevenueGrowthRates))
		}
		// Year-2 anchor lands at index 1; the seeded year-1 (index 0) comes from
		// GrowthRates[0], NOT a zero.
		if in.RevenueGrowthRates[1] != g2 {
			t.Fatalf("year-2 anchor not applied: got %v want %v", in.RevenueGrowthRates[1], g2)
		}
		if in.RevenueGrowthRates[0] != 0.20 {
			t.Fatalf("seeded year-1 must come from GrowthRates[0]=0.20, not zero; got %v", in.RevenueGrowthRates[0])
		}
	})

	t.Run("empty slice + no GrowthRates ⇒ seeds from scalar GrowthRate", func(t *testing.T) {
		in := salesToCapInputs()
		in.RevenueGrowthRates = nil
		in.GrowthRates = nil
		in.GrowthRate = 0.07

		g2 := 0.33
		applyNearTermAnchors(&in, authority.NearTermAnchors{RevenueGrowthYear2: &g2})

		if len(in.RevenueGrowthRates) < 2 {
			t.Fatalf("expected slice extended to >= 2; got %d", len(in.RevenueGrowthRates))
		}
		if in.RevenueGrowthRates[0] != 0.07 {
			t.Fatalf("seeded year-1 must fall back to scalar GrowthRate=0.07; got %v", in.RevenueGrowthRates[0])
		}
		if in.RevenueGrowthRates[1] != g2 {
			t.Fatalf("year-2 anchor not applied: got %v", in.RevenueGrowthRates[1])
		}
	})
}

// TestApplyNearTermAnchors_OperatingMarginYear1_NearTermOnly is the HIGH-1
// regression pin: a year-1 operating-margin anchor must touch ONLY year 1 via
// the per-year margin override seam — it must NOT shift the base→target
// convergence curve (which would move years 3+) nor raise TargetOperatingMargin
// (which would leak guidance into the TERMINAL NOPAT, violating §9.3 "year 1–2
// only, never dominates intrinsic value").
//
// It runs the engine with and without a year-1 margin anchor and asserts:
//   - year 1 OperatingIncome/FCF MOVED (the anchor took effect), and equals the
//     anchored margin × year-1 revenue;
//   - years 3..N FCF are bit-for-bit identical to the no-anchor run;
//   - the terminal value (and thus EnterpriseValue − ExplicitPeriodValue) is
//     bit-for-bit identical — proving the anchor did not perturb Target/terminal.
func TestApplyNearTermAnchors_OperatingMarginYear1_NearTermOnly(t *testing.T) {
	base, err := dcf.CalculateDCF(salesToCapInputs())
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	in := salesToCapInputs()
	// A year-1 margin distinct from the converged year-1 value so the anchor is
	// observable. The base margin is 0.10 and target 0.20 over 5 convergence
	// years, so the un-anchored year-1 margin is 0.12; pick 0.18.
	margin1 := 0.18
	applyNearTermAnchors(&in, authority.NearTermAnchors{OperatingMarginYear1: &margin1})

	// The anchor must consume the per-year margin seam (year 1), NOT BaseOperatingMargin.
	if got, ok := in.NearTermMarginOverride[1]; !ok || got != margin1 {
		t.Fatalf("year-1 margin anchor must write NearTermMarginOverride[1]=%v; got (%v,%v)", margin1, got, ok)
	}
	// BaseOperatingMargin / TargetOperatingMargin must be left at the model values
	// (the HIGH-1 bug raised Target; this asserts it is untouched).
	if in.BaseOperatingMargin != 0.10 {
		t.Fatalf("year-1 anchor must NOT shift BaseOperatingMargin; got %v", in.BaseOperatingMargin)
	}
	if in.TargetOperatingMargin != 0.20 {
		t.Fatalf("year-1 anchor must NOT raise TargetOperatingMargin (terminal leak); got %v", in.TargetOperatingMargin)
	}

	got, err := dcf.CalculateDCF(in)
	if err != nil {
		t.Fatalf("anchored: %v", err)
	}

	// Year 1 moved.
	if got.Projections[0].OperatingIncome == base.Projections[0].OperatingIncome {
		t.Fatal("year-1 margin anchor did not change year-1 operating income")
	}
	// Years 3..N FCF unchanged (the convergence curve was NOT shifted).
	for i := 2; i < len(base.Projections); i++ {
		if math.Float64bits(got.Projections[i].FreeCashFlow) != math.Float64bits(base.Projections[i].FreeCashFlow) {
			t.Fatalf("year %d FCF changed by a year-1 margin anchor — convergence curve was shifted (HIGH-1): %v vs %v",
				i+1, got.Projections[i].FreeCashFlow, base.Projections[i].FreeCashFlow)
		}
	}
	// Terminal value bit-for-bit identical (Target/terminal NOPAT untouched).
	if math.Float64bits(got.TerminalValue) != math.Float64bits(base.TerminalValue) {
		t.Fatalf("year-1 margin anchor leaked into the terminal value (HIGH-1): %v vs %v",
			got.TerminalValue, base.TerminalValue)
	}
}

// TestApplyNearTermAnchors_OperatingMarginYear2_Consumed is the LOW-1 wiring pin:
// a NearTermAnchors.OperatingMarginYear2 must flow into the engine's per-year
// margin override (it was previously silently ignored). It also re-asserts the
// near-term-prefix invariant holds (no panic) and that an empty anchor set
// leaves the inputs untouched.
func TestApplyNearTermAnchors_OperatingMarginYear2_Consumed(t *testing.T) {
	// Empty anchors ⇒ no margin override allocated (strict no-op).
	in := salesToCapInputs()
	applyNearTermAnchors(&in, authority.NearTermAnchors{})
	if in.NearTermMarginOverride != nil {
		t.Fatalf("empty anchors must not allocate a margin override; got %v", in.NearTermMarginOverride)
	}

	// Year-2 margin anchor ⇒ override[2] populated with the anchor value.
	margin2 := 0.27
	in2 := salesToCapInputs()
	applyNearTermAnchors(&in2, authority.NearTermAnchors{OperatingMarginYear2: &margin2})
	got, ok := in2.NearTermMarginOverride[2]
	if !ok {
		t.Fatal("OperatingMarginYear2 anchor was not consumed (no override[2] written) — LOW-1 regression")
	}
	if got != margin2 {
		t.Fatalf("override[2] = %v, want %v", got, margin2)
	}
	// The year-2 anchor must NOT touch year 1 / the convergence start.
	if _, ok := in2.NearTermMarginOverride[1]; ok {
		t.Fatal("year-2 margin anchor must not write a year-1 override")
	}
}
