package valuation

// Engine-level NF1 guard for the Layer-B Phase-2 NearTermReinvestmentOverride
// seam lives in the valuation package (rather than pkg/finance/dcf) only to keep
// the new override-focused assertions grouped with the rest of the Phase-2
// suite; it exercises pkg/finance/dcf.CalculateDCF directly.

import (
	"math"
	"testing"

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
