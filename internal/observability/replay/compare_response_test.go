package replay

import (
	"testing"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// Tests for the Stage G go-cmp-based CompareResponse walker. Plan §3
// Stage G enumerates these as the diff_test.go (extended) cases — kept
// in a dedicated file because diff.go's existing test file already has
// extensive coverage of the FloatDiff / ResultDiff primitives this
// builds on.

func TestCompareResponse_NoDiffs(t *testing.T) {
	a := &handlers.FairValueResponse{
		Ticker:           "AAPL",
		WACC:             0.092,
		GrowthRate:       0.045,
		DCFValuePerShare: 156.42,
		Currency:         "USD",
	}
	b := *a
	d := CompareResponse(a, &b, 0, 0)
	if d.HasMismatch() {
		t.Fatalf("HasMismatch: want false; floats=%v strings=%v", d.Floats, d.Strings)
	}
}

func TestCompareResponse_FloatFieldOutsideTolerance(t *testing.T) {
	a := &handlers.FairValueResponse{Ticker: "AAPL", DCFValuePerShare: 156.42, Currency: "USD"}
	b := *a
	b.DCFValuePerShare = 156.42 * 1.05 // 5% drift

	d := CompareResponse(a, &b, 0, 0)
	if !d.HasMismatch() {
		t.Fatalf("HasMismatch: want true; got false")
	}
	found := false
	for _, fd := range d.Floats {
		if fd.Path == "dcf_value_per_share" && !fd.WithinTolerance {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dcf_value_per_share Float diff outside tolerance; got %v", d.Floats)
	}
}

func TestCompareResponse_FloatFieldWithinTolerance(t *testing.T) {
	a := &handlers.FairValueResponse{Ticker: "AAPL", DCFValuePerShare: 156.42, Currency: "USD"}
	b := *a
	// 1e-10 drift — well below default 1e-9 relative tolerance.
	b.DCFValuePerShare = 156.42 + 1e-10

	d := CompareResponse(a, &b, 0, 0)
	if d.HasMismatch() {
		t.Fatalf("HasMismatch: want false (within tolerance); floats=%v", d.Floats)
	}
	// EquateApprox makes these equal at the cmp.Diff level, so the
	// reporter never sees them — FloatsWithinTolerance may be empty
	// because cmp considers them equal. This is the documented
	// trade-off of using EquateApprox vs a manual walk: drifted-but-
	// in-tolerance fields are silent in EquateApprox-mode. The hand-
	// rolled compareFairValueResponses (in compare.go) is the path
	// that surfaces them for verbose mode.
	_ = d
}

func TestCompareResponse_StringFieldDiff(t *testing.T) {
	a := &handlers.FairValueResponse{Ticker: "AAPL", GrowthSource: "analyst_blend", Currency: "USD"}
	b := *a
	b.GrowthSource = "historical_only"

	d := CompareResponse(a, &b, 0, 0)
	if !d.HasMismatch() {
		t.Fatalf("HasMismatch: want true on string diff")
	}
	found := false
	for _, s := range d.Strings {
		if s.Path == "growth_source" && s.Old == "analyst_blend" && s.New == "historical_only" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected growth_source StringDiff; got %v", d.Strings)
	}
}

func TestCompareResponse_NestedStruct_SanityCheck(t *testing.T) {
	a := &handlers.FairValueResponse{
		Ticker:   "AAPL",
		Currency: "USD",
		SanityCheck: &entities.SanityCheck{
			ImpliedPE: 20.0,
		},
	}
	b := *a
	bSC := *a.SanityCheck
	bSC.ImpliedPE = 22.0 // 10% drift, outside tolerance
	b.SanityCheck = &bSC

	d := CompareResponse(a, &b, 0, 0)
	if !d.HasMismatch() {
		t.Fatalf("HasMismatch: want true on nested-struct drift")
	}
	found := false
	for _, fd := range d.Floats {
		if fd.Path == "sanity_check.implied_pe" && !fd.WithinTolerance {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected sanity_check.implied_pe Float diff; got %v", d.Floats)
	}
}

func TestCompareResponse_NilArgs(t *testing.T) {
	d := CompareResponse(nil, nil, 0, 0)
	if d.HasMismatch() {
		t.Fatalf("nil/nil: want no mismatch; got %v", d)
	}

	// One nil, one non-nil: should surface a $root-level diff.
	a := &handlers.FairValueResponse{Ticker: "AAPL"}
	d2 := CompareResponse(a, nil, 0, 0)
	if !d2.HasMismatch() {
		t.Fatalf("a/nil: expected mismatch")
	}
}
