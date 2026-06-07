package guidance

import (
	"encoding/json"
	"fmt"
)

// BundleStage is the on-disk shape of the 09-guidance.json replay-bundle stage
// (Decision 8). On a hit it carries the selected Artifact verbatim (including
// its artifact_sha256) plus a small resolution envelope; on absent / no-guidance
// it carries only the envelope with a status, so "no guidance applied" is a
// captured fact a replay can re-derive identically (NF3).
type BundleStage struct {
	Resolution ResolutionEnvelope `json:"resolution"`
	// Artifact is the selected artifact verbatim. nil on the absent /
	// no_explicit_guidance_found-without-artifact path.
	Artifact *Artifact `json:"artifact,omitempty"`
}

// ResolutionEnvelope is the small diagnostic header captured alongside the
// artifact in 09-guidance.json. Deterministic by construction — every field is
// derived from the (sorted) loader trace + resolver outcome (NF2).
type ResolutionEnvelope struct {
	// Status mirrors the guidance outcome: "validated" | "stale" | "absent" |
	// "no_explicit_guidance_found" | "low_confidence".
	Status string `json:"status"`
	// SelectedAccession is the chosen artifact's accession ("" when absent).
	SelectedAccession string `json:"selected_accession,omitempty"`
	// RejectedAccessions lists eligible-but-not-chosen candidates (sorted).
	RejectedAccessions []string `json:"rejected_accessions,omitempty"`
	// AnchorsApplied names the near-term knobs the resolver anchored, e.g.
	// ["capex_year1"]. Empty when nothing was anchored.
	AnchorsApplied []string `json:"anchors_applied,omitempty"`
	// Stale records the §8.3-item-5 staleness flag.
	Stale bool `json:"stale,omitempty"`
}

// NewBundleStage assembles the capture payload for 09-guidance.json from a
// resolution, the resolver status, and the anchors the resolver actually
// applied. A nil artifact (absent) is captured as an artifact-less envelope so
// replay re-derives the absent path.
func NewBundleStage(res Resolution, status string, anchorsApplied []string) BundleStage {
	env := ResolutionEnvelope{
		Status:             status,
		SelectedAccession:  res.Trace.SelectedAccession,
		RejectedAccessions: res.Trace.RejectedAccessions,
		AnchorsApplied:     anchorsApplied,
		Stale:              res.Trace.Stale,
	}
	return BundleStage{Resolution: env, Artifact: res.Artifact}
}

// unmarshalArtifact unmarshals a single artifact payload, returning a wrapped
// ErrInvalidArtifact on a malformed body so the loader can classify it as a
// skip (NF4) rather than a hard error.
func unmarshalArtifact(body []byte) (*Artifact, error) {
	var a Artifact
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("%w: unmarshal: %v", ErrInvalidArtifact, err)
	}
	return &a, nil
}

// unmarshalBundleStage unmarshals a 09-guidance.json payload.
func unmarshalBundleStage(body []byte) (*BundleStage, error) {
	var s BundleStage
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("guidance: unmarshal bundle stage: %w", err)
	}
	return &s, nil
}
