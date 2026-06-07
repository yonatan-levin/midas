package replay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
)

func guidanceFixtureArtifact(t *testing.T) *guidance.Artifact {
	t.Helper()
	a := &guidance.Artifact{
		SchemaVersion: guidance.SchemaVersion,
		Status:        guidance.StatusValidated,
		Issuer:        guidance.Issuer{Ticker: "AMD", CIK: "0000002488"},
		Filing:        guidance.Filing{Accession: "0000002488-26-000012", FormType: "10-K", FilingDate: "2026-02-04", PeriodEnd: "2025-12-28"},
		Validation:    guidance.Validation{Status: "validated", Confidence: 0.82},
	}
	h, err := guidance.ComputeArtifactSHA256(a)
	require.NoError(t, err)
	a.ArtifactSHA256 = h
	return a
}

func writeGuidanceStage(t *testing.T, dir string, stage guidance.BundleStage) {
	t.Helper()
	body, err := json.MarshalIndent(stage, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, guidanceBundleFile), body, 0o644))
}

// TestBundleGuidanceGateway_MissingFile_Absent is the absent-not-panic contract
// (CLAUDE.md F11): a bundle without 09-guidance.json (an old bundle predating
// guidance capture) resolves to Absent — no panic, no error — so it replays on
// the absent path bit-for-bit with its original valuation.
func TestBundleGuidanceGateway_MissingFile_Absent(t *testing.T) {
	gw := NewBundleGuidanceGateway(t.TempDir()) // empty dir, no stage
	res, err := gw.Load("0000002488", time.Now())
	require.NoError(t, err)
	assert.True(t, res.Absent)
	assert.Nil(t, res.Artifact)
}

// TestBundleGuidanceGateway_CapturedHit_RoundTrips confirms a captured hit
// reconstructs the selected artifact via guidance.LoadFromBundle.
func TestBundleGuidanceGateway_CapturedHit_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	art := guidanceFixtureArtifact(t)
	res := guidance.Resolution{Artifact: art, Trace: guidance.LoadTrace{SelectedAccession: art.Filing.Accession}}
	writeGuidanceStage(t, dir, guidance.NewBundleStage(res, "validated", []string{"capex_year1"}))

	gw := NewBundleGuidanceGateway(dir)
	got, err := gw.Load("0000002488", time.Now())
	require.NoError(t, err)
	require.NotNil(t, got.Artifact)
	assert.Equal(t, art.Filing.Accession, got.Trace.SelectedAccession)
	assert.Equal(t, art.ArtifactSHA256, got.Artifact.ArtifactSHA256)
}

// TestBundleGuidanceGateway_CapturedAbsence_Absent confirms a captured absence
// envelope replays as Absent.
func TestBundleGuidanceGateway_CapturedAbsence_Absent(t *testing.T) {
	dir := t.TempDir()
	writeGuidanceStage(t, dir, guidance.NewBundleStage(guidance.Resolution{Absent: true}, "absent", nil))

	gw := NewBundleGuidanceGateway(dir)
	got, err := gw.Load("0000002488", time.Now())
	require.NoError(t, err)
	assert.True(t, got.Absent)
}

// TestBundleGuidanceGateway_TamperedArtifact_HardError confirms the ONE hard
// error survives the replay seam: a captured artifact whose content hash no
// longer verifies must not silently replay a different value.
func TestBundleGuidanceGateway_TamperedArtifact_HardError(t *testing.T) {
	dir := t.TempDir()
	art := guidanceFixtureArtifact(t)
	art.Issuer.Ticker = "TAMPERED" // mutate AFTER hashing
	res := guidance.Resolution{Artifact: art}
	writeGuidanceStage(t, dir, guidance.NewBundleStage(res, "validated", nil))

	gw := NewBundleGuidanceGateway(dir)
	_, err := gw.Load("0000002488", time.Now())
	require.Error(t, err)
	assert.True(t, errors.Is(err, guidance.ErrContentHashMismatch))
}

// TestBundleGuidanceGateway_MalformedStage_Absent confirms a corrupt stage
// degrades to Absent (replay reproduces absence, does not crash).
func TestBundleGuidanceGateway_MalformedStage_Absent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, guidanceBundleFile), []byte("{not json"), 0o644))

	gw := NewBundleGuidanceGateway(dir)
	got, err := gw.Load("0000002488", time.Now())
	require.NoError(t, err)
	assert.True(t, got.Absent)
}
