package artifact_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
)

// TestBundle_SetGuidanceResolution_WritesJSON pins the Layer-B Phase-2 bundle
// extension (Decision 8): SetGuidanceResolution writes 09-guidance.json carrying
// the resolution envelope + (on a hit) the selected artifact verbatim, and
// registers the GuidanceResolution schema version so replay can version-gate.
func TestBundle_SetGuidanceResolution_WritesJSON(t *testing.T) {
	root := t.TempDir()
	cfg := artifact.Config{Enabled: true, RootPath: root}

	b, err := artifact.OpenBundle(cfg, "rid-guidance", "AMD", artifact.TriggerQuery)
	require.NoError(t, err)
	require.NotNil(t, b)

	art := &guidance.Artifact{
		SchemaVersion: guidance.SchemaVersion,
		Status:        guidance.StatusValidated,
		Issuer:        guidance.Issuer{Ticker: "AMD", CIK: "0000002488"},
		Filing:        guidance.Filing{Accession: "0000002488-26-000012", FormType: "10-K", FilingDate: "2026-02-04", PeriodEnd: "2025-12-28"},
		Validation:    guidance.Validation{Status: "validated", Confidence: 0.82},
	}
	h, err := guidance.ComputeArtifactSHA256(art)
	require.NoError(t, err)
	art.ArtifactSHA256 = h

	res := guidance.Resolution{Artifact: art, Trace: guidance.LoadTrace{SelectedAccession: art.Filing.Accession}}
	stage := guidance.NewBundleStage(res, "validated", []string{"capex_year1"})

	b.SetGuidanceResolution(context.Background(), stage)
	require.NoError(t, b.Close())

	body, err := os.ReadFile(filepath.Join(b.Root(), "09-guidance.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"selected_accession": "0000002488-26-000012"`)
	assert.Contains(t, string(body), `"capex_year1"`)
	assert.Contains(t, string(body), art.ArtifactSHA256)

	// The captured stage must round-trip through guidance.LoadFromBundle (the
	// replay seam) to the same resolution.
	loaded, err := guidance.LoadFromBundle(body)
	require.NoError(t, err)
	require.NotNil(t, loaded.Artifact)
	assert.Equal(t, art.Filing.Accession, loaded.Trace.SelectedAccession)

	// Schema version registered.
	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(mfBody, &mf))
	assert.Equal(t, 1, mf.SchemaVersions["GuidanceResolution"], "GuidanceResolution schema_version must be 1")
}

// TestBundle_SetGuidanceResolution_NilSafe — nil receiver must be a no-op so
// service.go can call it through artifact.From(ctx) without nil-checking.
func TestBundle_SetGuidanceResolution_NilSafe(t *testing.T) {
	var b *artifact.Bundle
	// Must not panic.
	b.SetGuidanceResolution(context.Background(), guidance.BundleStage{
		Resolution: guidance.ResolutionEnvelope{Status: "absent"},
	})
}
