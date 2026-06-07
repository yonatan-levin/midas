package valuation

import (
	"context"
	"reflect"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/authority"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
)

// reinvestmentTestService builds a minimal *Service sufficient to call
// applyReinvestmentModel directly (it reads only s.log / s.calcEmitter).
func reinvestmentTestService() *Service {
	return &Service{logger: zap.NewNop()}
}

// reinvestmentProfile is a profile that OPTS INTO the sales_to_capital
// reinvestment model so applyReinvestmentModel engages (rather than taking the
// legacy no-op early return).
func reinvestmentProfile() *profile.ResolvedProfile {
	rp := &profile.ResolvedProfile{}
	rp.ProfileID = "test_scaling:high_growth"
	rp.ReinvestmentMethod = profile.ReinvestmentSalesToCapital
	rp.SalesToCapitalStart = 1.5
	rp.SalesToCapitalTarget = 2.5
	rp.ReinvestmentFadeYears = 5
	rp.MaintenanceCapexFloor = 0.02
	rp.TargetOperatingMargin = 0.30
	rp.MarginConvergenceYears = 5
	return rp
}

// histWithRevenue returns a single-FY history with a positive revenue base so
// TrailingTwelveMonthsRevenue resolves via the ANNUAL_FY path.
func histWithRevenue(revenue float64) *entities.HistoricalFinancialData {
	return &entities.HistoricalFinancialData{
		Ticker: "AMD",
		Data: map[string]*entities.FinancialData{
			"2025FY": {CIK: "0000002488", Revenue: revenue},
		},
	}
}

func baseInputs() dcf.Inputs {
	return dcf.Inputs{
		BaseOperatingIncome: 2e9,
		ProjectionYears:     5,
		WACC:                0.10,
		TerminalGrowthRate:  0.025,
		TaxRate:             0.21,
	}
}

// TestApplyReinvestmentModel_NilAnchors_ByteIdentical is the load-bearing NF1
// proof at the reinvestment seam: applyReinvestmentModel with EMPTY anchors must
// produce a dcf.Inputs byte-identical to the same call with a freshly
// zero-value NearTermAnchors{} — i.e. the anchor path is a strict no-op and
// every guidance-related mutation is gated behind a non-nil anchor pointer.
//
// Concretely we run the reinvestment model twice with the same inputs and an
// empty anchor set, and assert the resulting inputs are DeepEqual AND that no
// NearTermReinvestmentOverride was ever allocated (nil, not empty-map).
func TestApplyReinvestmentModel_NilAnchors_ByteIdentical(t *testing.T) {
	s := reinvestmentTestService()
	rp := reinvestmentProfile()
	hist := histWithRevenue(10e9)
	growth := []float64{0.20, 0.18, 0.15, 0.12, 0.10}

	in1 := baseInputs()
	in1.GrowthRates = growth
	w1 := s.applyReinvestmentModel(context.Background(), &in1, rp, in1.BaseOperatingIncome, hist, growth, authority.NearTermAnchors{})

	in2 := baseInputs()
	in2.GrowthRates = growth
	var empty authority.NearTermAnchors // zero value
	w2 := s.applyReinvestmentModel(context.Background(), &in2, rp, in2.BaseOperatingIncome, hist, growth, empty)

	if !reflect.DeepEqual(in1, in2) {
		t.Fatalf("empty-anchor inputs diverged:\n in1=%+v\n in2=%+v", in1, in2)
	}
	if in1.NearTermReinvestmentOverride != nil {
		t.Fatalf("empty anchors must not allocate NearTermReinvestmentOverride; got %v", in1.NearTermReinvestmentOverride)
	}
	if !reflect.DeepEqual(w1, w2) {
		t.Fatalf("warning lines diverged: %v vs %v", w1, w2)
	}
	// The reinvestment model still engaged (one audit line) — confirming this is
	// the active path, not the legacy early-return.
	if len(w1) != 1 {
		t.Fatalf("expected exactly one reinvestment_model audit line, got %d: %v", len(w1), w1)
	}
}

// TestApplyReinvestmentModel_CapExAnchor_OverridesYear1Only confirms a CapEx
// year-1 anchor sets the per-year reinvestment override for year 1 ONLY (and
// not years >=2), and the growth slice / margins are untouched.
func TestApplyReinvestmentModel_CapExAnchor_OverridesYear1Only(t *testing.T) {
	s := reinvestmentTestService()
	rp := reinvestmentProfile()
	hist := histWithRevenue(10e9)
	growth := []float64{0.20, 0.18, 0.15, 0.12, 0.10}

	capex := 1.5e9
	anchors := authority.NearTermAnchors{CapExYear1: &capex}

	in := baseInputs()
	in.GrowthRates = growth
	s.applyReinvestmentModel(context.Background(), &in, rp, in.BaseOperatingIncome, hist, growth, anchors)

	if in.NearTermReinvestmentOverride == nil {
		t.Fatal("CapEx anchor must allocate a per-year reinvestment override")
	}
	if got, ok := in.NearTermReinvestmentOverride[1]; !ok || got != capex {
		t.Fatalf("year-1 reinvestment override = (%v,%v), want %v", got, ok, capex)
	}
	if _, ok := in.NearTermReinvestmentOverride[2]; ok {
		t.Fatal("CapEx year-1 anchor must NOT touch year 2")
	}
	if _, ok := in.NearTermReinvestmentOverride[3]; ok {
		t.Fatal("guidance must never anchor year 3+ (§9.3)")
	}
}

// TestApplyReinvestmentModel_RevenueAnchor_ClonesSlice confirms a revenue-growth
// anchor overrides RevenueGrowthRates[0] WITHOUT mutating the caller's slice
// (the anchor clones before writing).
func TestApplyReinvestmentModel_RevenueAnchor_ClonesSlice(t *testing.T) {
	s := reinvestmentTestService()
	rp := reinvestmentProfile()
	hist := histWithRevenue(10e9)
	growth := []float64{0.20, 0.18, 0.15, 0.12, 0.10}

	g := 0.35
	anchors := authority.NearTermAnchors{RevenueGrowthYear1: &g}

	in := baseInputs()
	in.GrowthRates = growth
	s.applyReinvestmentModel(context.Background(), &in, rp, in.BaseOperatingIncome, hist, growth, anchors)

	if in.RevenueGrowthRates[0] != g {
		t.Fatalf("year-1 growth anchor not applied: %v", in.RevenueGrowthRates[0])
	}
	// The caller's original slice must be untouched (clone-on-write).
	if growth[0] != 0.20 {
		t.Fatalf("caller growth slice mutated in place: %v", growth[0])
	}
}

// TestResolveGuidance_EmptyRoot_NoOp is the NF1 proof at the service seam: with
// an empty GuidanceRoot the resolver returns the absent resolution — no
// artifact, no anchors, no sources, no warnings — so the downstream DCF anchor
// step is a strict no-op (and the captured bundle records absence).
func TestResolveGuidance_EmptyRoot_NoOp(t *testing.T) {
	s := &Service{logger: zap.NewNop(), guidanceSource: guidance.NewLoader("")}

	asOf := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	res, err := s.resolveGuidance(context.Background(), "0000002488", asOf, nil, nil)
	if err != nil {
		t.Fatalf("resolveGuidance returned error on empty root: %v", err)
	}
	if !res.Anchors.IsEmpty() {
		t.Fatal("empty root must produce no anchors")
	}
	if len(res.Sources) != 0 {
		t.Fatalf("empty root must produce no sources, got %v", res.Sources)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("empty root must produce no warnings, got %v", res.Warnings)
	}
	if res.GuidanceStatus != authority.StatusAbsent {
		t.Fatalf("empty root status = %q, want absent", res.GuidanceStatus)
	}
}

// TestStampAssumptionSources_EmptyIsNoOp proves the response stamp is a strict
// no-op for an empty Sources map (so default-path responses omit the field).
func TestStampAssumptionSources_EmptyIsNoOp(t *testing.T) {
	r := &entities.ValuationResult{Ticker: "AMD"}
	stampAssumptionSources(r, nil)
	stampAssumptionSources(r, map[string]authority.AssumptionSource{})
	if r.AssumptionSources != nil {
		t.Fatalf("empty sources must leave AssumptionSources nil, got %v", r.AssumptionSources)
	}

	stampAssumptionSources(r, map[string]authority.AssumptionSource{
		"capex_year1": {Level: authority.SourceGuidance, Value: 1.5e9, Detail: "x"},
	})
	if got := r.AssumptionSources["capex_year1"].Source; got != "guidance" {
		t.Fatalf("stamped source = %q, want guidance", got)
	}
}
