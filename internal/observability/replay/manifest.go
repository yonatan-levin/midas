package replay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// ManifestFileName is the canonical filename of the bundle's self-describing
// index, mirrored from internal/observability/artifact for read-side use so
// the replay package does not depend on artifact's writer plumbing.
const ManifestFileName = "00-manifest.json"

// SupportedBundleVersions enumerates the bundle_version values this replay
// build accepts. Mirrors NG7 (replay is forward-compatible from "1.0";
// earlier formats do not exist) — adding `1.x` minor revisions is fine
// because the manifest schema is documented as additive-only. A wholly-new
// major version (e.g. "2.0") would change file naming or required fields,
// so refuse it explicitly until the parser is updated.
var SupportedBundleVersions = map[string]bool{
	"1.0": true,
}

// ReadManifest opens 00-manifest.json under bundleDir, parses it, and
// validates the minimum required fields. The returned *artifact.Manifest is
// the same struct production stamps; replay consumes it read-only.
//
// Validation policy:
//   - Missing file → wrapped fs error (caller can errors.Is os.ErrNotExist).
//   - Malformed JSON → wrapped json error.
//   - Unsupported bundle_version → returns ErrUnsupportedBundleVersion.
//   - Missing required identity fields (request_id / ticker / started_at)
//     → returns a "manifest invalid" error. These are required by the bundle
//     contract; replay cannot proceed without them.
//
// Optional fields (notes, finished_at, build_version) are tolerated when
// missing.
func ReadManifest(bundleDir string) (*artifact.Manifest, error) {
	path := filepath.Join(bundleDir, ManifestFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		// Wrap so callers can distinguish "missing manifest" from "bad
		// manifest content" via errors.Is(err, os.ErrNotExist).
		return nil, fmt.Errorf("replay: read manifest %s: %w", path, err)
	}
	var mf artifact.Manifest
	if err := json.Unmarshal(body, &mf); err != nil {
		return nil, fmt.Errorf("replay: parse manifest %s: %w", path, err)
	}
	if err := validateManifest(&mf); err != nil {
		return nil, fmt.Errorf("replay: invalid manifest %s: %w", path, err)
	}
	return &mf, nil
}

// ErrUnsupportedBundleVersion is returned by ReadManifest when the manifest's
// bundle_version is not in SupportedBundleVersions. Callers may match via
// errors.Is.
var ErrUnsupportedBundleVersion = fmt.Errorf("replay: unsupported bundle_version")

// validateManifest enforces the minimum-required-fields contract. The
// reasoning behind each required field:
//   - bundle_version: pins the parser; without it the bundle is opaque.
//   - request_id: replay re-injects this into ctx (D7 / F7); cannot proceed
//     blind.
//   - ticker: identifies the valuation target — required by every gateway.
//   - started_at: math-affecting per D10 (clock binding); replay cannot
//     pin determinism without it.
//
// Other fields (outcome, phases_recorded, schema_versions) are validated
// downstream when their consumers run; pre-validating them here would force
// a single point of churn whenever the upstream schema evolves.
func validateManifest(mf *artifact.Manifest) error {
	if mf.BundleVersion == "" {
		return fmt.Errorf("missing bundle_version")
	}
	if !SupportedBundleVersions[mf.BundleVersion] {
		return fmt.Errorf("%w: %q", ErrUnsupportedBundleVersion, mf.BundleVersion)
	}
	if mf.RequestID == "" {
		return fmt.Errorf("missing request_id")
	}
	if mf.Ticker == "" {
		return fmt.Errorf("missing ticker")
	}
	if mf.StartedAt == "" {
		return fmt.Errorf("missing started_at")
	}
	return nil
}
