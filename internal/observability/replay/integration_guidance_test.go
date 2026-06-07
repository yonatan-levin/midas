package replay

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
