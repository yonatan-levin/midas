package authority

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
)

// --- fixtures -------------------------------------------------------------

func capexEnvelope(conf float64) *guidance.Envelope {
	return &guidance.Envelope{
		ValueLow: 1.4e9, ValueHigh: 1.6e9, Unit: guidance.UnitAbsoluteUSD, Period: "FY2026",
		Confidence: conf,
		Evidence:   []guidance.Evidence{{Quote: "we expect capex of ~$1.5B", Location: "Item 7"}},
	}
}

func marginEnvelope(conf float64) guidance.Envelope {
	return guidance.Envelope{
		ValueLow: 0.30, ValueHigh: 0.34, Unit: guidance.UnitPct, Period: "FY2026",
		Basis:      &guidance.Basis{GAAPOrNonGAAP: "non_gaap"},
		Confidence: conf,
		Evidence:   []guidance.Evidence{{Quote: "gross margin in the low-30s", Location: "Item 7"}},
	}
}

func revenueEnvelope(conf float64) guidance.Envelope {
	return guidance.Envelope{
		ValueLow: 0.20, ValueHigh: 0.24, Unit: guidance.UnitPct, Period: "FY2026",
		Confidence: conf,
		Evidence:   []guidance.Evidence{{Quote: "revenue growth ~22%", Location: "Item 7"}},
	}
}

func validatedArtifact() *guidance.Artifact {
	return &guidance.Artifact{
		SchemaVersion: guidance.SchemaVersion,
		Status:        guidance.StatusValidated,
		Issuer:        guidance.Issuer{Ticker: "AMD", CIK: "0000002488"},
		Filing:        guidance.Filing{Accession: "0000002488-26-000012", FormType: "10-K", FilingDate: "2026-02-04", PeriodEnd: "2025-12-28"},
		Extraction:    &guidance.Extraction{CapExGuidance: capexEnvelope(0.82)},
		Validation:    guidance.Validation{Status: "validated", Confidence: 0.82},
	}
}

func hit(art *guidance.Artifact, stale bool) guidance.Resolution {
	return guidance.Resolution{
		Artifact: art,
		Trace:    guidance.LoadTrace{SelectedAccession: art.Filing.Accession, Stale: stale},
	}
}

// --- precedence: each level wins in turn --------------------------------

func TestResolve_Guidance_AnchorsCapEx(t *testing.T) {
	in := Input{Loaded: hit(validatedArtifact(), false)}
	res := Resolve(in)

	assert.Equal(t, StatusValidated, res.GuidanceStatus)
	require.NotNil(t, res.Anchors.CapExYear1)
	assert.Equal(t, 1.5e9, *res.Anchors.CapExYear1, "midpoint of [1.4B,1.6B]")

	src, ok := res.Sources[KeyCapExYear1]
	require.True(t, ok)
	assert.Equal(t, SourceGuidance, src.Level)
	assert.Equal(t, 1.5e9, src.Value)
	assert.Contains(t, src.Detail, "accession=0000002488-26-000012")
	assert.Contains(t, src.Detail, "period=FY2026")

	assert.Equal(t, []string{KeyCapExYear1}, res.AnchorsApplied)
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "guidance: capex_year1 anchored")
}

func TestResolve_UserOverride_WinsOverGuidance(t *testing.T) {
	in := Input{
		Loaded:              hit(validatedArtifact(), false),
		UserOverriddenKnobs: map[string]bool{KeyCapExYear1: true},
	}
	res := Resolve(in)

	// Level 1 wins: no guidance anchor is produced; the source records the
	// user override and no anchor flows to the DCF.
	assert.Nil(t, res.Anchors.CapExYear1)
	src := res.Sources[KeyCapExYear1]
	assert.Equal(t, SourceUserOverride, src.Level)
	assert.Empty(t, res.AnchorsApplied)
}

func TestResolve_Absent_FallsThroughToProfile(t *testing.T) {
	in := Input{Loaded: guidance.Resolution{Absent: true}}
	res := Resolve(in)

	assert.Equal(t, StatusAbsent, res.GuidanceStatus)
	assert.True(t, res.Anchors.IsEmpty(), "absent ⇒ no anchors ⇒ Layer A unchanged")
	assert.Empty(t, res.AnchorsApplied)
	assert.Nil(t, res.Captured)
}

func TestResolve_NoGuidanceFound_FallsThrough(t *testing.T) {
	art := validatedArtifact()
	art.Status = guidance.StatusNoGuidanceFound
	art.Extraction = nil
	in := Input{Loaded: hit(art, false)}
	res := Resolve(in)

	assert.Equal(t, StatusNoGuidanceFound, res.GuidanceStatus)
	assert.True(t, res.Anchors.IsEmpty())
	require.NotNil(t, res.Captured, "the absence record is still captured for the bundle")
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "no_explicit_guidance_found")
}

// --- §9.3 guardrails ------------------------------------------------------

func TestResolve_LowConfidence_NotAnchored(t *testing.T) {
	art := validatedArtifact()
	art.Extraction.CapExGuidance = capexEnvelope(0.50) // below default 0.70
	in := Input{Loaded: hit(art, false)}
	res := Resolve(in)

	assert.Equal(t, StatusValidated, res.GuidanceStatus, "artifact is validated; the ENVELOPE is low-confidence")
	assert.Nil(t, res.Anchors.CapExYear1, "low-confidence envelope ⇒ no anchor")
	assert.Empty(t, res.AnchorsApplied)
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "NOT anchored")
}

func TestResolve_NonValidatedStatus_ContextOnly(t *testing.T) {
	for _, status := range []guidance.Status{guidance.StatusNeedsReview, guidance.StatusRejected} {
		t.Run(string(status), func(t *testing.T) {
			art := validatedArtifact()
			art.Status = status
			in := Input{Loaded: hit(art, false)}
			res := Resolve(in)

			assert.Equal(t, StatusLowConfidence, res.GuidanceStatus)
			assert.True(t, res.Anchors.IsEmpty(), "non-validated ⇒ context only, no numeric anchor (laundering rejected)")
			require.Len(t, res.Warnings, 1)
			assert.Contains(t, res.Warnings[0], "context only")
		})
	}
}

func TestResolve_Stale_CapturedNotAnchored(t *testing.T) {
	in := Input{Loaded: hit(validatedArtifact(), true)} // stale=true
	res := Resolve(in)

	assert.Equal(t, StatusStale, res.GuidanceStatus)
	assert.True(t, res.Anchors.IsEmpty(), "stale ⇒ captured, not anchored")
	require.NotNil(t, res.Captured)
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "stale")
}

func TestResolve_MissingEvidence_NotAnchored_LaunderingRejected(t *testing.T) {
	art := validatedArtifact()
	// High confidence but NO evidence — the §9.3 numeric guardrail must still
	// refuse the anchor (defense-in-depth even if the loader were bypassed).
	art.Extraction.CapExGuidance.Confidence = 0.95
	art.Extraction.CapExGuidance.Evidence = nil
	in := Input{Loaded: hit(art, false)}
	res := Resolve(in)

	assert.Nil(t, res.Anchors.CapExYear1, "no evidence ⇒ no numeric anchor")
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "NOT anchored")
}

func TestResolve_VagueProse_ContextOnly(t *testing.T) {
	// A "vague bullish prose, no value" fixture is modeled as a validated
	// artifact with NO extraction envelopes — there is nothing numeric to
	// anchor, so the resolver produces no anchor and no source entry.
	art := validatedArtifact()
	art.Extraction = &guidance.Extraction{} // empty: no capex/margin/revenue
	in := Input{Loaded: hit(art, false)}
	res := Resolve(in)

	assert.Equal(t, StatusValidated, res.GuidanceStatus)
	assert.True(t, res.Anchors.IsEmpty(), "vague prose contributes context only, never a number")
	assert.Empty(t, res.Sources)
}

// --- §9.3 dominance guardrail (near-term-only, structural) ---------------

func TestRefuseAnchorIndex_YearThreePlus(t *testing.T) {
	assert.False(t, RefuseAnchorIndex(0), "year 1 (index 0) allowed")
	assert.False(t, RefuseAnchorIndex(1), "year 2 (index 1) allowed")
	assert.True(t, RefuseAnchorIndex(2), "year 3 (index 2) HARD-REFUSED")
	assert.True(t, RefuseAnchorIndex(5), "year 6 (index 5) HARD-REFUSED")
}

// --- multi-knob + midpoint correctness -----------------------------------

func TestResolve_AllThreeKnobs_MidpointsCorrect(t *testing.T) {
	art := validatedArtifact()
	art.Extraction = &guidance.Extraction{
		CapExGuidance:   capexEnvelope(0.80),
		MarginGuidance:  []guidance.Envelope{marginEnvelope(0.80)},
		RevenueGuidance: []guidance.Envelope{revenueEnvelope(0.80)},
	}
	in := Input{Loaded: hit(art, false)}
	res := Resolve(in)

	require.NotNil(t, res.Anchors.CapExYear1)
	require.NotNil(t, res.Anchors.OperatingMarginYear1)
	require.NotNil(t, res.Anchors.RevenueGrowthYear1)
	assert.Equal(t, 1.5e9, *res.Anchors.CapExYear1)
	assert.InDelta(t, 0.32, *res.Anchors.OperatingMarginYear1, 1e-12, "midpoint of [0.30,0.34]")
	assert.InDelta(t, 0.22, *res.Anchors.RevenueGrowthYear1, 1e-12, "midpoint of [0.20,0.24]")

	// Deterministic anchors-applied order: capex, margin, revenue (NF2).
	assert.Equal(t, []string{KeyCapExYear1, KeyOperatingMarginYear1, KeyRevenueGrowthYear1}, res.AnchorsApplied)
}

func TestResolve_UserOverride_MarginAndRevenue(t *testing.T) {
	art := validatedArtifact()
	art.Extraction = &guidance.Extraction{
		CapExGuidance:   capexEnvelope(0.80),
		MarginGuidance:  []guidance.Envelope{marginEnvelope(0.80)},
		RevenueGuidance: []guidance.Envelope{revenueEnvelope(0.80)},
	}
	in := Input{
		Loaded: hit(art, false),
		UserOverriddenKnobs: map[string]bool{
			KeyOperatingMarginYear1: true,
			KeyRevenueGrowthYear1:   true,
		},
	}
	res := Resolve(in)

	// CapEx still anchors from guidance; margin + revenue defer to the request.
	require.NotNil(t, res.Anchors.CapExYear1)
	assert.Nil(t, res.Anchors.OperatingMarginYear1)
	assert.Nil(t, res.Anchors.RevenueGrowthYear1)
	assert.Equal(t, SourceUserOverride, res.Sources[KeyOperatingMarginYear1].Level)
	assert.Equal(t, SourceUserOverride, res.Sources[KeyRevenueGrowthYear1].Level)
	assert.Equal(t, []string{KeyCapExYear1}, res.AnchorsApplied)
}

func TestNumericEligible_EmptyQuoteRejected(t *testing.T) {
	env := guidance.Envelope{
		ValueLow: 1, ValueHigh: 2, Unit: guidance.UnitAbsoluteUSD, Period: "FY2026",
		Confidence: 0.95,
		Evidence:   []guidance.Evidence{{Quote: "", Location: "x"}}, // present but empty
	}
	assert.False(t, numericEligible(env, DefaultConfidenceThreshold), "empty-quote evidence does not satisfy §9.3")
}

func TestFormatValue_UnknownUnit(t *testing.T) {
	// Defensive default branch: an unknown unit renders via %g rather than
	// panicking — keeps the warning string total.
	assert.Equal(t, "1.5", formatValue(guidance.Unit("furlongs"), 1.5))
}

func TestResolve_ConfidenceThresholdOverride(t *testing.T) {
	art := validatedArtifact()
	art.Extraction.CapExGuidance = capexEnvelope(0.60)

	// Default threshold 0.70 ⇒ not anchored.
	res := Resolve(Input{Loaded: hit(art, false)})
	assert.Nil(t, res.Anchors.CapExYear1)

	// A lower threshold (0.50) ⇒ anchored.
	res = Resolve(Input{Loaded: hit(art, false), ConfidenceThreshold: 0.50})
	require.NotNil(t, res.Anchors.CapExYear1)
}
