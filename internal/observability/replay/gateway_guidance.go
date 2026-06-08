package replay

import (
	"errors"
	"io/fs"
	"time"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
)

// guidanceBundleFile is the canonical bundle filename for the Layer-B Phase-2
// guidance stage, mirroring artifact.Bundle.SetGuidanceResolution
// (b.Snapshot(ctx, "guidance.resolved", "09-guidance.json", stage)).
const guidanceBundleFile = "09-guidance.json"

// BundleGuidanceGateway is the bundle-backed replay implementation of the
// valuation service's guidance source (the Load(cik, asOf) seam). It reads the
// captured 09-guidance.json from the bundle and reconstructs the resolution via
// guidance.LoadFromBundle, preserving the replay hermeticity contract (NF3): the
// engine consumes the captured artifact rather than scanning the live fixture
// directory.
//
// ABSENT-NOT-PANIC (the replay gateway discipline, CLAUDE.md F11): a bundle that
// predates guidance capture has no 09-guidance.json. Per the replay contract
// (gateways run inside datafetcher goroutines where a panic is unrecoverable),
// a missing file is NOT a panic and NOT a hard error — it resolves to Absent so
// the old bundle replays on the absent path, bit-for-bit with its original
// valuation (which also had no guidance). The ONE hard error is a content-hash
// MISMATCH on a captured artifact (a tampered bundle must not silently replay a
// different value) — that propagates from guidance.LoadFromBundle.
//
// asOf is ignored: the bundle already pins the SELECTED artifact (the live
// loader's as-of eligibility / conflict resolution ran at capture time and the
// winner is what 09-guidance.json records). Replay consumes that decision
// verbatim rather than re-deriving it.
type BundleGuidanceGateway struct {
	bundleDir string
}

// NewBundleGuidanceGateway constructs a replay-mode guidance source rooted at
// bundleDir.
func NewBundleGuidanceGateway(bundleDir string) *BundleGuidanceGateway {
	return &BundleGuidanceGateway{bundleDir: bundleDir}
}

// Load reads the captured guidance stage and reconstructs the resolution.
// Missing-file ⇒ Absent (old bundle, absent path). Hash mismatch on a captured
// artifact ⇒ hard error (propagated). asOf is unused (see type doc).
func (g *BundleGuidanceGateway) Load(_ string, _ time.Time) (guidance.Resolution, error) {
	body, err := readBundlePayload(g.bundleDir, guidanceBundleFile)
	if err != nil {
		// Missing 09-guidance.json (old bundle / never captured) ⇒ Absent, NOT
		// an error or panic. readBundlePayload classifies a missing file as
		// ErrBundleMissingPayload (which wraps fs.ErrNotExist); both map to the
		// absent path so the engine replays exactly as it did originally.
		if errors.Is(err, ErrBundleMissingPayload) || errors.Is(err, fs.ErrNotExist) {
			return guidance.Resolution{Absent: true}, nil
		}
		// MEDIUM-7: any OTHER read error (a present-but-unreadable / corrupt
		// payload — permission denied, a directory in the file's place, an I/O
		// fault) MUST propagate, NOT degrade to absent. Silently replaying on the
		// absent path would mask a tampered/broken bundle and break hermeticity:
		// a present guidance stage that cannot be read is a real failure, distinct
		// from "this old bundle never captured guidance".
		return guidance.Resolution{}, err
	}

	// guidance.LoadFromBundle handles the captured-absence envelope (Absent),
	// the captured-hit (verifies the embedded content hash), and a malformed
	// stage (degrades to absent). Only a content-hash MISMATCH on a present
	// captured artifact returns a hard error here.
	return guidance.LoadFromBundle(body)
}
