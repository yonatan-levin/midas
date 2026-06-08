package replay

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
)

// seedGuidanceStage writes a captured 09-guidance.json into bundleDir so the
// replay path's BundleGuidanceGateway consumes it (rather than the live fixture
// directory — NF3).
func seedGuidanceStage(t *testing.T, bundleDir string, stage guidance.BundleStage) {
	t.Helper()
	body, err := json.MarshalIndent(stage, "", "  ")
	if err != nil {
		t.Fatalf("marshal guidance stage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, guidanceBundleFile), body, 0o644); err != nil {
		t.Fatalf("write guidance stage: %v", err)
	}
}

// TestReplay_OldBundleWithoutGuidanceStage_ReplaysOnAbsentPath is the B6
// old-bundle guarantee (Decision 8): a bundle captured BEFORE guidance existed
// has no 09-guidance.json. The BundleGuidanceGateway resolves it to Absent (no
// panic, no error) so the engine takes the absent path and replays bit-for-bit
// with its original valuation — preserving every existing baseline bundle.
func TestReplay_OldBundleWithoutGuidanceStage_ReplaysOnAbsentPath(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)
	// Deliberately do NOT seed 09-guidance.json (simulates an old bundle).
	if _, err := os.Stat(filepath.Join(bundleDir, guidanceBundleFile)); !os.IsNotExist(err) {
		t.Fatalf("precondition: bundle must not carry %s", guidanceBundleFile)
	}

	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	if firstResp == nil {
		t.Fatal("first engine run produced nil response")
	}
	writeResponseFile(t, bundleDir, firstResp)

	res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
	if res.Status == StatusErrored {
		t.Fatalf("Replay errored: %s", res.Error)
	}
	if res.Status != StatusPass || res.FieldsChanged != 0 {
		t.Fatalf("old-bundle replay not bit-for-bit: status=%s changed=%d floats=%v strings=%v",
			res.Status, res.FieldsChanged, res.Diffs, res.StringDiffs)
	}
}

// TestReplay_GuidanceConsumingBundle_ReplaysBitForBit is the B6 captured-stage
// guarantee: a bundle that carries a captured 09-guidance.json replays
// bit-for-bit through the BundleGuidanceGateway → guidance.LoadFromBundle seam.
//
// The captured stage here is a no_explicit_guidance_found record — a positive
// "guidance was considered, none applied" fact. It exercises the full capture →
// consume → resolve → re-capture round-trip deterministically (the resolver
// produces no anchor, so the DCF value is identical across both passes, while
// the 09-guidance.json stage is consumed identically on each pass).
func TestReplay_GuidanceConsumingBundle_ReplaysBitForBit(t *testing.T) {
	const ticker = "AAPL"
	const startedAt = "2026-01-15T12:00:00Z"

	bundleDir := seedFullBundle(t, ticker, startedAt)

	absent := &guidance.Artifact{
		SchemaVersion: guidance.SchemaVersion,
		Status:        guidance.StatusNoGuidanceFound,
		Issuer:        guidance.Issuer{Ticker: ticker, CIK: "0000320193"},
		Filing:        guidance.Filing{Accession: "0000320193-26-000010", FormType: "10-Q", FilingDate: "2026-01-10", PeriodEnd: "2025-12-28"},
		Validation:    guidance.Validation{Status: string(guidance.StatusNoGuidanceFound)},
	}
	h, err := guidance.ComputeArtifactSHA256(absent)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	absent.ArtifactSHA256 = h
	res := guidance.Resolution{Artifact: absent, Trace: guidance.LoadTrace{SelectedAccession: absent.Filing.Accession}}
	seedGuidanceStage(t, bundleDir, guidance.NewBundleStage(res, "no_explicit_guidance_found", nil))

	_, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeRaw, nil)
	if firstResp == nil {
		t.Fatal("first engine run produced nil response")
	}
	writeResponseFile(t, bundleDir, firstResp)

	rr := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
	if rr.Status == StatusErrored {
		t.Fatalf("Replay errored: %s", rr.Error)
	}
	if rr.Status != StatusPass || rr.FieldsChanged != 0 {
		t.Fatalf("guidance-consuming replay not bit-for-bit: status=%s changed=%d floats=%v strings=%v",
			rr.Status, rr.FieldsChanged, rr.Diffs, rr.StringDiffs)
	}
}

// validatedCapExStage builds a captured 09-guidance.json stage carrying a
// VALIDATED, anchor-eligible CapEx artifact for the given ticker/CIK. The
// confidence (0.82) clears the default 0.70 threshold and the evidence quote
// satisfies the §9.3 numeric guardrail, so the resolver produces a real
// capex_year1 anchor (unlike the no_explicit_guidance_found stage used above).
func validatedCapExStage(t *testing.T, ticker, cik string) guidance.BundleStage {
	t.Helper()
	art := &guidance.Artifact{
		SchemaVersion: guidance.SchemaVersion,
		Status:        guidance.StatusValidated,
		Issuer:        guidance.Issuer{Ticker: ticker, CIK: cik},
		Filing: guidance.Filing{
			Accession: cik + "-26-000077", FormType: "10-K",
			FilingDate: "2025-11-01", PeriodEnd: "2025-09-30",
		},
		Extraction: &guidance.Extraction{
			CapExGuidance: &guidance.Envelope{
				ValueLow: 1.4e9, ValueHigh: 1.6e9, Unit: guidance.UnitAbsoluteUSD, Period: "FY2026",
				Basis:      &guidance.Basis{GrossOrNet: "gross", CashOrAccrual: "cash", GAAPOrNonGAAP: "gaap"},
				Confidence: 0.82,
				Evidence: []guidance.Evidence{{
					Quote: "we expect capital expenditures of approximately $1.5 billion in fiscal 2026", Location: "Item 7",
				}},
			},
		},
		Validation: guidance.Validation{
			Status: string(guidance.StatusValidated), Confidence: 0.82,
			NormalizationRulesVersion: "1.0.0", ValidatorVersion: "fixture-1.0.0",
		},
	}
	h, err := guidance.ComputeArtifactSHA256(art)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	art.ArtifactSHA256 = h
	res := guidance.Resolution{Artifact: art, Trace: guidance.LoadTrace{SelectedAccession: art.Filing.Accession}}
	return guidance.NewBundleStage(res, "validated", []string{"capex_year1"})
}

// dcfWarnings reports whether the engine emitted a guidance capex-anchor warning
// tag — the observable signal that the validated fixture actually moved a value
// (the resolver appends a "guidance: capex_year1 anchored ..." tag only when the
// anchor fires).
func hasCapExAnchorWarning(warnings []string) bool {
	for _, w := range warnings {
		if strings.Contains(w, "guidance: capex_year1 anchored") {
			return true
		}
	}
	return false
}

// TestReplay_ValidatedGuidanceConsumingBundle_ReplaysBitForBit is the LOW-2
// value-moving replay pin. Unlike the no_explicit_guidance_found sibling above,
// the captured 09-guidance.json here carries a VALIDATED, anchor-eligible CapEx
// artifact, so the engine applies a year-1 reinvestment anchor (the value-moving
// path). It then asserts the bundle replays bit-for-bit through
// cmd/replay's Replay() in --from=parsed mode (FieldsChanged=0), pinning the full
// capture → consume → re-derive round-trip on an artifact that actually changes
// the valuation — not merely the absent path.
func TestReplay_ValidatedGuidanceConsumingBundle_ReplaysBitForBit(t *testing.T) {
	const ticker = "AAPL"
	const cik = "0000320193"
	const startedAt = "2026-01-15T12:00:00Z"

	// Baseline (absent path): an identical bundle WITHOUT the guidance stage. Its
	// DCF value is the un-anchored Layer-A value; the anchored run must differ
	// from it, proving the validated artifact actually moves the valuation.
	absentBundle := seedFullBundle_ParsedMode(t, ticker, startedAt)
	absentResult, _ := runEngineForTest(t, absentBundle, ticker, startedAt, ModeParsed, nil)
	if absentResult == nil {
		t.Fatal("absent-path engine run produced nil result")
	}

	bundleDir := seedFullBundle_ParsedMode(t, ticker, startedAt)
	seedGuidanceStage(t, bundleDir, validatedCapExStage(t, ticker, cik))

	// First engine run consumes the captured validated stage; capture the
	// canonical response.
	result, firstResp := runEngineForTest(t, bundleDir, ticker, startedAt, ModeParsed, nil)
	if firstResp == nil {
		t.Fatal("first engine run produced nil response")
	}

	// Value-moving evidence #1 (structural): the engine applied the capex anchor.
	// The warning tag is the resolver's observable "anchor fired" signal, and the
	// assumption_sources map records capex_year1 ⇒ guidance. If absent, the chosen
	// bundle/profile did not engage the reinvestment model — fail loudly so the
	// fixture is fixed rather than silently passing on the absent path.
	if !hasCapExAnchorWarning(result.Warnings) {
		t.Fatalf("validated capex fixture did not anchor (no capex_year1 warning tag) — "+
			"the bundle/profile must engage the reinvestment model for a value-moving replay test; warnings=%v",
			result.Warnings)
	}
	if _, ok := result.AssumptionSources["capex_year1"]; !ok {
		t.Fatalf("validated capex fixture did not record assumption_sources[capex_year1]; sources=%v",
			result.AssumptionSources)
	}

	// Value-moving evidence #2 (numeric): the anchored DCF value differs from the
	// absent-path Layer-A value. This is the pin that the artifact MOVED a value.
	if math.Float64bits(result.DCFValuePerShare) == math.Float64bits(absentResult.DCFValuePerShare) {
		t.Fatalf("validated guidance anchor did not move the DCF value (anchored=%v absent=%v)",
			result.DCFValuePerShare, absentResult.DCFValuePerShare)
	}

	writeResponseFile(t, bundleDir, firstResp)

	// Replay re-runs the engine against the SAME captured stage and diffs against
	// the captured response. The anchor is re-applied identically, so the replay
	// is bit-for-bit despite the value having moved relative to the absent path.
	rr := Replay(context.Background(), bundleDir, Options{Mode: ModeParsed})
	if rr.Status == StatusErrored {
		t.Fatalf("Replay errored: %s", rr.Error)
	}
	if rr.Status != StatusPass || rr.FieldsChanged != 0 {
		t.Fatalf("validated-guidance replay not bit-for-bit: status=%s changed=%d floats=%v strings=%v",
			rr.Status, rr.FieldsChanged, rr.Diffs, rr.StringDiffs)
	}
}
