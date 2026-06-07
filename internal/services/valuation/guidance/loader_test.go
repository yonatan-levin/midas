package guidance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCIK = "0000002488"

// writeArtifact stamps the content hash and writes the artifact to
// root/<cik>/<accession>.json (the on-disk layout, Decision 2).
func writeArtifact(t *testing.T, root string, a *Artifact) {
	t.Helper()
	h, err := ComputeArtifactSHA256(a)
	require.NoError(t, err)
	a.ArtifactSHA256 = h

	dir := filepath.Join(root, a.Issuer.CIK)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body, err := json.MarshalIndent(a, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, a.Filing.Accession+".json"), body, 0o644))
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := parseFilingDate(s)
	require.NoError(t, err)
	return d
}

func TestLoader_DisabledRoot_AlwaysAbsent(t *testing.T) {
	l := NewLoader("")
	res, err := l.Load(testCIK, time.Now())
	require.NoError(t, err)
	assert.True(t, res.Absent)
	assert.Equal(t, "disabled_root", res.Trace.Reason)
	assert.Nil(t, res.Artifact)
}

func TestLoader_NilReceiver_Absent(t *testing.T) {
	var l *Loader
	res, err := l.Load(testCIK, time.Now())
	require.NoError(t, err)
	assert.True(t, res.Absent)
}

func TestLoader_NoDir_Absent(t *testing.T) {
	l := NewLoader(t.TempDir()) // root exists but no <cik> subdir
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err)
	assert.True(t, res.Absent)
	assert.Equal(t, "no_dir", res.Trace.Reason)
}

func TestLoader_EmptyDir_Absent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, testCIK), 0o755))
	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err)
	assert.True(t, res.Absent)
	assert.Equal(t, "no_eligible_candidate", res.Trace.Reason)
}

func TestLoader_Hit(t *testing.T) {
	root := t.TempDir()
	writeArtifact(t, root, validCapExArtifact())

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err)

	assert.False(t, res.Absent)
	require.NotNil(t, res.Artifact)
	assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession)
	assert.Equal(t, StatusValidated, res.Artifact.Status)
	assert.False(t, res.Trace.Stale)
}

func TestLoader_FilingDateEligibility(t *testing.T) {
	root := t.TempDir()
	writeArtifact(t, root, validCapExArtifact()) // filing_date 2026-02-04

	l := NewLoader(root)
	// as-of BEFORE the filing date ⇒ not yet eligible ⇒ absent.
	res, err := l.Load(testCIK, mustDate(t, "2026-01-15"))
	require.NoError(t, err)
	assert.True(t, res.Absent)
	assert.Equal(t, "no_eligible_candidate", res.Trace.Reason)

	// as-of exactly ON the filing date ⇒ eligible.
	res, err = l.Load(testCIK, mustDate(t, "2026-02-04"))
	require.NoError(t, err)
	assert.False(t, res.Absent)
}

func TestLoader_ConflictResolution_NewestFilingWins(t *testing.T) {
	root := t.TempDir()

	// Two filings speaking to the same FY2026 period: a 10-K then a later 10-Q.
	older := validCapExArtifact() // 10-K, filing_date 2026-02-04
	newer := validCapExArtifact()
	newer.Filing.Accession = "0000002488-26-000050"
	newer.Filing.FormType = "10-Q"
	newer.Filing.FilingDate = "2026-05-01"
	newer.Extraction.CapExGuidance.ValueHigh = 1.7e9 // distinguishable

	writeArtifact(t, root, older)
	writeArtifact(t, root, newer)

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-06-01"))
	require.NoError(t, err)
	require.NotNil(t, res.Artifact)

	// Newest filing_date wins (the 10-Q), even though 10-K is the more specific
	// form — newest filing_date is the FIRST tie-break, applied before form rank.
	assert.Equal(t, "0000002488-26-000050", res.Trace.SelectedAccession)
	assert.Equal(t, []string{"0000002488-26-000012"}, res.Trace.RejectedAccessions)
}

func TestLoader_ConflictResolution_FormRankTieBreak(t *testing.T) {
	root := t.TempDir()

	// Same filing_date → form rank decides: 10-K/A beats 10-K.
	base := validCapExArtifact()
	base.Filing.Accession = "0000002488-26-000012"
	base.Filing.FormType = "10-K"

	amended := validCapExArtifact()
	amended.Filing.Accession = "0000002488-26-000013"
	amended.Filing.FormType = "10-K/A"
	// same filing_date as base.

	writeArtifact(t, root, base)
	writeArtifact(t, root, amended)

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err)
	assert.Equal(t, "0000002488-26-000013", res.Trace.SelectedAccession, "10-K/A outranks 10-K on same filing_date")
}

func TestLoader_ConflictResolution_AccessionLexTieBreak(t *testing.T) {
	root := t.TempDir()

	// Same filing_date AND same form_type → lexicographically-largest accession.
	a := validCapExArtifact()
	a.Filing.Accession = "0000002488-26-000012"
	b := validCapExArtifact()
	b.Filing.Accession = "0000002488-26-000099"

	writeArtifact(t, root, a)
	writeArtifact(t, root, b)

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err)
	assert.Equal(t, "0000002488-26-000099", res.Trace.SelectedAccession, "lex-largest accession wins the final tie-break")
}

func TestLoader_Staleness_PeriodLapsed(t *testing.T) {
	root := t.TempDir()
	writeArtifact(t, root, validCapExArtifact()) // capex period FY2026

	l := NewLoader(root)
	// as-of in 2027 ⇒ FY2026 has lapsed ⇒ stale flag set (still captured).
	res, err := l.Load(testCIK, mustDate(t, "2027-03-01"))
	require.NoError(t, err)
	require.NotNil(t, res.Artifact)
	assert.True(t, res.Trace.Stale, "FY2026 lapsed by 2027 ⇒ stale")

	// as-of within FY2026 ⇒ not stale.
	res, err = l.Load(testCIK, mustDate(t, "2026-06-01"))
	require.NoError(t, err)
	assert.False(t, res.Trace.Stale)
}

func TestLoader_NoGuidanceFound_IsFirstClass(t *testing.T) {
	root := t.TempDir()
	writeArtifact(t, root, absentArtifact())

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-06-01"))
	require.NoError(t, err)

	// A positive "we looked, found nothing" record: Artifact present, status
	// no_explicit_guidance_found. The resolver (B3) treats it as absent.
	require.NotNil(t, res.Artifact)
	assert.Equal(t, StatusNoGuidanceFound, res.Artifact.Status)
	assert.Equal(t, "no_explicit_guidance_found", res.Trace.Reason)
}

func TestLoader_HashMismatch_HardError(t *testing.T) {
	root := t.TempDir()
	a := validCapExArtifact()
	a.ArtifactSHA256 = "deadbeef" // deliberately wrong — do NOT recompute.
	dir := filepath.Join(root, a.Issuer.CIK)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body, err := json.MarshalIndent(a, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, a.Filing.Accession+".json"), body, 0o644))

	l := NewLoader(root)
	_, err = l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrContentHashMismatch), "tampered artifact hard-errors")
}

func TestLoader_MalformedFile_DegradesToSkip(t *testing.T) {
	root := t.TempDir()
	// A valid artifact + a junk file in the same dir: junk is skipped, the
	// valid one still resolves (NF4 — never abort on a malformed sibling).
	writeArtifact(t, root, validCapExArtifact())
	dir := filepath.Join(root, testCIK)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0000002488-26-999999.json"), []byte("{not json"), 0o644))

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err, "malformed sibling must not abort the load")
	require.NotNil(t, res.Artifact)
	assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession)
}

func TestLoader_StructurallyInvalidFile_Skipped(t *testing.T) {
	root := t.TempDir()
	writeArtifact(t, root, validCapExArtifact())

	// A structurally-invalid artifact (value_low > value_high) written with a
	// VALID self-hash (so it is not a hash mismatch — it is a structural skip).
	bad := validCapExArtifact()
	bad.Filing.Accession = "0000002488-26-000077"
	bad.Extraction.CapExGuidance.ValueLow = 9e9 // > value_high
	writeArtifact(t, root, bad)

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err)
	require.NotNil(t, res.Artifact)
	assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession, "structurally-invalid sibling skipped")
}

func TestLoadFromBundle_AbsentPayload(t *testing.T) {
	res, err := LoadFromBundle(nil)
	require.NoError(t, err)
	assert.True(t, res.Absent)

	res, err = LoadFromBundle([]byte(""))
	require.NoError(t, err)
	assert.True(t, res.Absent)
}

func TestLoadFromBundle_CapturedHit_RoundTrips(t *testing.T) {
	a := mustHash(t, validCapExArtifact())
	res := Resolution{Artifact: a, Trace: LoadTrace{SelectedAccession: a.Filing.Accession}}
	stage := NewBundleStage(res, "validated", []string{"capex_year1"})

	raw, err := json.Marshal(stage)
	require.NoError(t, err)

	got, err := LoadFromBundle(raw)
	require.NoError(t, err)
	require.NotNil(t, got.Artifact)
	assert.Equal(t, a.Filing.Accession, got.Trace.SelectedAccession)
	assert.Equal(t, a.ArtifactSHA256, got.Artifact.ArtifactSHA256)
}

func TestLoadFromBundle_CapturedAbsence(t *testing.T) {
	res := Resolution{Absent: true, Trace: LoadTrace{Reason: "absent"}}
	stage := NewBundleStage(res, "absent", nil)
	raw, err := json.Marshal(stage)
	require.NoError(t, err)

	got, err := LoadFromBundle(raw)
	require.NoError(t, err)
	assert.True(t, got.Absent)
	assert.Nil(t, got.Artifact)
}

func TestLoadFromBundle_TamperedArtifact_HardError(t *testing.T) {
	a := mustHash(t, validCapExArtifact())
	a.Extraction.CapExGuidance.ValueHigh = 9.9e9 // mutate AFTER hashing → mismatch
	res := Resolution{Artifact: a}
	stage := NewBundleStage(res, "validated", nil)
	raw, err := json.Marshal(stage)
	require.NoError(t, err)

	_, err = LoadFromBundle(raw)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrContentHashMismatch))
}

func TestLoadFromBundle_MalformedStage_DegradesToAbsent(t *testing.T) {
	got, err := LoadFromBundle([]byte("{not json"))
	require.NoError(t, err)
	assert.True(t, got.Absent)
	assert.Equal(t, "bundle_parse_error", got.Trace.Reason)
}
