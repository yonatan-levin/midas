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
