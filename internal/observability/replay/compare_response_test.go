package replay

import (
	"testing"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
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

// TestCompareFairValueResponses_DCFPerYearPV_LengthMismatch verifies that
// the hand-rolled walker (compareFairValueResponses) catches DCFPerYearPV
// length drift. Closes T2-P0b-1 — before this test, drift in this field
// could silently bypass Replay() regression because the walker did not
// enumerate it.
func TestCompareFairValueResponses_DCFPerYearPV_LengthMismatch(t *testing.T) {
	a := &handlers.FairValueResponse{
		Ticker:       "AAPL",
		Currency:     "USD",
		DCFPerYearPV: []float64{10.0, 20.0, 30.0}, // 3-year horizon
	}
	b := *a
	b.DCFPerYearPV = []float64{10.0, 20.0, 30.0, 40.0} // 4-year horizon (drift)

	d := compareFairValueResponses(a, &b, 1e-9, 1e-12)
	if !d.HasMismatch() {
		t.Fatalf("length drift in DCFPerYearPV must surface a mismatch")
	}
	found := false
	for _, s := range d.Strings {
		if s.Path == "dcf_per_year_pv.len" && s.Old == "3" && s.New == "4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dcf_per_year_pv.len StringDiff; got strings=%v", d.Strings)
	}
}

// TestCompareFairValueResponses_DCFPerYearPV_ElementDrift verifies the
// walker catches per-element drift in DCFPerYearPV. Same closure rationale
// as the length test — per-element checks are what catches off-by-one
// horizon indexing bugs that don't change slice length.
func TestCompareFairValueResponses_DCFPerYearPV_ElementDrift(t *testing.T) {
	a := &handlers.FairValueResponse{
		Ticker:       "AAPL",
		Currency:     "USD",
		DCFPerYearPV: []float64{10.0, 20.0, 30.0},
	}
	b := *a
	b.DCFPerYearPV = []float64{10.0, 22.0, 30.0} // year-2 PV drifted 10%

	d := compareFairValueResponses(a, &b, 1e-9, 1e-12)
	if !d.HasMismatch() {
		t.Fatalf("element-level drift in DCFPerYearPV must surface a mismatch")
	}
	found := false
	for _, fd := range d.Floats {
		if fd.Path == "dcf_per_year_pv[1]" && !fd.WithinTolerance {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dcf_per_year_pv[1] FloatDiff outside tolerance; got floats=%v", d.Floats)
	}
}

// TestCompareFairValueResponses_DCFPerYearPV_BothEmpty_NoFalsePositive
// confirms that pre-Tier-2 bundles (which marshal to nil because of
// omitempty) versus an unpopulated current snapshot don't produce a false
// positive — both sides have len=0 so the walker should see no drift.
func TestCompareFairValueResponses_DCFPerYearPV_BothEmpty_NoFalsePositive(t *testing.T) {
	a := &handlers.FairValueResponse{Ticker: "AAPL", Currency: "USD"} // DCFPerYearPV is nil
	b := *a

	d := compareFairValueResponses(a, &b, 1e-9, 1e-12)
	for _, s := range d.Strings {
		if s.Path == "dcf_per_year_pv.len" {
			t.Fatalf("nil/nil DCFPerYearPV must not produce a length diff; got %v", s)
		}
	}
}

// TestCompareFairValueResponses_ResolutionTrace_BothNil_NoFalsePositive
// confirms that pre-Tier-2 bundles (where ResolutionTrace marshals to
// nil under omitempty) compared against an equally-unpopulated current
// snapshot produce zero diffs at the resolution_trace path. Closes
// T2-P4-W2 item 12 nil-vs-nil case.
func TestCompareFairValueResponses_ResolutionTrace_BothNil_NoFalsePositive(t *testing.T) {
	a := &handlers.FairValueResponse{Ticker: "AAPL", Currency: "USD"} // ResolutionTrace is nil
	b := *a

	d := compareFairValueResponses(a, &b, 1e-9, 1e-12)
	for _, s := range d.Strings {
		if s.Path == "resolution_trace" || hasResolutionTracePathPrefix(s.Path) {
			t.Fatalf("nil/nil ResolutionTrace must not produce a diff; got %v", s)
		}
	}
}

// TestCompareFairValueResponses_ResolutionTrace_NilVsPopulated surfaces a
// single sentinel StringDiff at the "resolution_trace" path when one side
// has the field populated and the other does not. This is the canonical
// "schema gap" signal: a pre-Tier-2 bundle replayed against current code
// that resolves a profile triggers exactly this case. Per-field flooding
// is intentionally suppressed.
func TestCompareFairValueResponses_ResolutionTrace_NilVsPopulated(t *testing.T) {
	a := &handlers.FairValueResponse{Ticker: "AAPL", Currency: "USD"} // ResolutionTrace nil
	b := *a
	b.ResolutionTrace = &profile.ResolutionTrace{
		ProfileID:       "mature_large_bank:mature",
		Source:          profile.SourceExplicit,
		ResolverVersion: "1",
		ConfigVersion:   "1.0.0",
	}

	d := compareFairValueResponses(a, &b, 1e-9, 1e-12)
	if !d.HasMismatch() {
		t.Fatalf("nil-vs-populated ResolutionTrace must surface a mismatch")
	}
	sentinel := false
	perFieldNoise := 0
	for _, s := range d.Strings {
		if s.Path == "resolution_trace" {
			sentinel = true
			if s.Old != "nil" || s.New != "present" {
				t.Fatalf("expected old=nil/new=present; got %+v", s)
			}
		}
		if hasResolutionTracePathPrefix(s.Path) && s.Path != "resolution_trace" {
			perFieldNoise++
		}
	}
	if !sentinel {
		t.Fatalf("expected resolution_trace sentinel StringDiff; got strings=%v", d.Strings)
	}
	if perFieldNoise > 0 {
		t.Fatalf("nil-vs-populated should NOT emit per-field diffs; got %d", perFieldNoise)
	}
}

// TestCompareFairValueResponses_ResolutionTrace_PopulatedVsPopulated_NoDrift
// confirms that two identical populated traces produce zero diffs across
// every walked field — the "no false positive on the happy path" pin.
func TestCompareFairValueResponses_ResolutionTrace_PopulatedVsPopulated_NoDrift(t *testing.T) {
	rt := &profile.ResolutionTrace{
		ProfileID:       "mature_large_bank:mature",
		Source:          profile.SourceExplicit,
		ResolverVersion: "1",
		ConfigVersion:   "1.0.0",
		ConfigHash:      "abc123",
		MatchedRuleID:   "fin_generic",
		FallbackReason:  "",
		MissingFacts:    nil,
		HumanReason:     "matched FIN prefix at priority 100",
	}
	a := &handlers.FairValueResponse{Ticker: "JPM", Currency: "USD", ResolutionTrace: rt}
	bRT := *rt
	b := *a
	b.ResolutionTrace = &bRT

	d := compareFairValueResponses(a, &b, 1e-9, 1e-12)
	for _, s := range d.Strings {
		if hasResolutionTracePathPrefix(s.Path) {
			t.Fatalf("identical traces must not diff; got %+v", s)
		}
	}
}

// TestCompareFairValueResponses_ResolutionTrace_PerFieldDrift exercises
// every field of ResolutionTrace independently. Each row mutates exactly
// one field and asserts the corresponding StringDiff path surfaces. Drift
// in any of these would be a genuine regression worth surfacing in
// Replay() output — that's the whole reason this walker exists.
func TestCompareFairValueResponses_ResolutionTrace_PerFieldDrift(t *testing.T) {
	base := profile.ResolutionTrace{
		ProfileID:       "mature_large_bank:mature",
		Source:          profile.SourceExplicit,
		ResolverVersion: "1",
		ConfigVersion:   "1.0.0",
		ConfigHash:      "abc123",
		MatchedRuleID:   "fin_generic",
		FallbackReason:  "",
		HumanReason:     "matched",
	}
	cases := []struct {
		name     string
		mutate   func(*profile.ResolutionTrace)
		wantPath string
		wantOld  string
		wantNew  string
	}{
		{
			name:     "profile_id",
			mutate:   func(r *profile.ResolutionTrace) { r.ProfileID = "growth_bank:mature" },
			wantPath: "resolution_trace.profile_id",
			wantOld:  "mature_large_bank:mature",
			wantNew:  "growth_bank:mature",
		},
		{
			name:     "source",
			mutate:   func(r *profile.ResolutionTrace) { r.Source = profile.SourceFallback },
			wantPath: "resolution_trace.source",
			wantOld:  "explicit",
			wantNew:  "fallback",
		},
		{
			name:     "resolver_version",
			mutate:   func(r *profile.ResolutionTrace) { r.ResolverVersion = "2" },
			wantPath: "resolution_trace.resolver_version",
			wantOld:  "1",
			wantNew:  "2",
		},
		{
			name:     "config_version",
			mutate:   func(r *profile.ResolutionTrace) { r.ConfigVersion = "1.1.0" },
			wantPath: "resolution_trace.config_version",
			wantOld:  "1.0.0",
			wantNew:  "1.1.0",
		},
		{
			name:     "config_hash",
			mutate:   func(r *profile.ResolutionTrace) { r.ConfigHash = "def456" },
			wantPath: "resolution_trace.config_hash",
			wantOld:  "abc123",
			wantNew:  "def456",
		},
		{
			name:     "matched_rule_id",
			mutate:   func(r *profile.ResolutionTrace) { r.MatchedRuleID = "insurance" },
			wantPath: "resolution_trace.matched_rule_id",
			wantOld:  "fin_generic",
			wantNew:  "insurance",
		},
		{
			name:     "fallback_reason",
			mutate:   func(r *profile.ResolutionTrace) { r.FallbackReason = "no rule matched" },
			wantPath: "resolution_trace.fallback_reason",
			wantOld:  "",
			wantNew:  "no rule matched",
		},
		{
			name:     "human_reason",
			mutate:   func(r *profile.ResolutionTrace) { r.HumanReason = "matched at priority 50" },
			wantPath: "resolution_trace.human_reason",
			wantOld:  "matched",
			wantNew:  "matched at priority 50",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aRT := base
			bRT := base
			tc.mutate(&bRT)
			a := &handlers.FairValueResponse{Ticker: "JPM", Currency: "USD", ResolutionTrace: &aRT}
			b := &handlers.FairValueResponse{Ticker: "JPM", Currency: "USD", ResolutionTrace: &bRT}

			d := compareFairValueResponses(a, b, 1e-9, 1e-12)
			if !d.HasMismatch() {
				t.Fatalf("mutation of %s must surface a diff", tc.name)
			}
			found := false
			for _, s := range d.Strings {
				if s.Path == tc.wantPath && s.Old == tc.wantOld && s.New == tc.wantNew {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected path=%s old=%q new=%q; got strings=%v",
					tc.wantPath, tc.wantOld, tc.wantNew, d.Strings)
			}
		})
	}
}

// TestCompareFairValueResponses_ResolutionTrace_MissingFactsDrift walks
// the MissingFacts slice element-wise + length, mirroring how the walker
// handles Warnings and SanityCheck.Flags. Length drift surfaces a sentinel
// .len StringDiff; equal-length per-index drift surfaces per-element diffs.
func TestCompareFairValueResponses_ResolutionTrace_MissingFactsDrift(t *testing.T) {
	t.Run("length_drift", func(t *testing.T) {
		a := &handlers.FairValueResponse{
			Ticker:   "AAPL",
			Currency: "USD",
			ResolutionTrace: &profile.ResolutionTrace{
				ProfileID:    "mature_dividend_tech:mature",
				Source:       profile.SourceExplicit,
				MissingFacts: []string{"dividend_per_share"},
			},
		}
		bRT := *a.ResolutionTrace
		bRT.MissingFacts = []string{"dividend_per_share", "payout_ratio"}
		b := *a
		b.ResolutionTrace = &bRT

		d := compareFairValueResponses(a, &b, 1e-9, 1e-12)
		if !d.HasMismatch() {
			t.Fatalf("missing_facts length drift must surface a mismatch")
		}
		found := false
		for _, s := range d.Strings {
			if s.Path == "resolution_trace.missing_facts.len" && s.Old == "1" && s.New == "2" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected resolution_trace.missing_facts.len StringDiff; got %v", d.Strings)
		}
	})

	t.Run("element_drift", func(t *testing.T) {
		a := &handlers.FairValueResponse{
			Ticker:   "AAPL",
			Currency: "USD",
			ResolutionTrace: &profile.ResolutionTrace{
				ProfileID:    "mature_dividend_tech:mature",
				Source:       profile.SourceExplicit,
				MissingFacts: []string{"dividend_per_share", "payout_ratio"},
			},
		}
		bRT := *a.ResolutionTrace
		bRT.MissingFacts = []string{"dividend_per_share", "book_value_per_share"} // index 1 changed
		b := *a
		b.ResolutionTrace = &bRT

		d := compareFairValueResponses(a, &b, 1e-9, 1e-12)
		if !d.HasMismatch() {
			t.Fatalf("missing_facts element drift must surface a mismatch")
		}
		found := false
		for _, s := range d.Strings {
			if s.Path == "resolution_trace.missing_facts[1]" &&
				s.Old == "payout_ratio" && s.New == "book_value_per_share" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected resolution_trace.missing_facts[1] StringDiff; got %v", d.Strings)
		}
	})
}

// hasResolutionTracePathPrefix returns true for any path under the
// resolution_trace umbrella. Used by tests to assert the walker emits
// (or doesn't emit) diffs anywhere under that namespace.
func hasResolutionTracePathPrefix(path string) bool {
	const p = "resolution_trace"
	if path == p {
		return true
	}
	if len(path) > len(p) && path[:len(p)] == p && (path[len(p)] == '.' || path[len(p)] == '[') {
		return true
	}
	return false
}
