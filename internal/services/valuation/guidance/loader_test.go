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
	newer.Extraction.CapExGuidance.ValueHigh = Float(1.7e9) // distinguishable

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

// TestLoader_RejectedAccessions_SamePeriodOnly is the LOW-4 scope pin:
// rejected_accessions must list ONLY the candidates that actually COMPETED with
// the winner — i.e. shared the winner's period_end — not every eligible
// candidate from unrelated periods. A different-period filing never "lost" to
// the winner; including it overstates conflict.
func TestLoader_RejectedAccessions_SamePeriodOnly(t *testing.T) {
	root := t.TempDir()

	// Winner group: two filings on period_end 2026-12-26 (FY2027). The newer
	// 10-Q wins; the same-period 10-K is the genuine rejected competitor.
	winner := validCapExArtifact()
	winner.Filing.Accession = "0000002488-27-000040"
	winner.Filing.FormType = "10-Q"
	winner.Filing.FilingDate = "2027-04-28"
	winner.Filing.PeriodEnd = "2026-12-26"
	winner.Extraction.CapExGuidance.Period = "FY2027"

	sameperiodLoser := validCapExArtifact()
	sameperiodLoser.Filing.Accession = "0000002488-27-000005"
	sameperiodLoser.Filing.FormType = "10-K"
	sameperiodLoser.Filing.FilingDate = "2027-02-03"
	sameperiodLoser.Filing.PeriodEnd = "2026-12-26" // SAME period as the winner
	sameperiodLoser.Extraction.CapExGuidance.Period = "FY2027"

	// Unrelated group: an older filing on a DIFFERENT period_end (2025-12-28,
	// FY2026). It is eligible but never competed with the FY2027 winner, so it
	// must NOT appear in rejected_accessions.
	otherPeriod := validCapExArtifact() // accession 0000002488-26-000012, period_end 2025-12-28

	writeArtifact(t, root, winner)
	writeArtifact(t, root, sameperiodLoser)
	writeArtifact(t, root, otherPeriod)

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2027-06-01"))
	require.NoError(t, err)
	require.NotNil(t, res.Artifact)

	assert.Equal(t, "0000002488-27-000040", res.Trace.SelectedAccession, "newest filing wins")
	// ONLY the same-period_end competitor is a rejected accession; the unrelated
	// FY2026 filing competed in no conflict and is excluded (LOW-4).
	assert.Equal(t, []string{"0000002488-27-000005"}, res.Trace.RejectedAccessions,
		"rejected_accessions lists only same-period competitors, not unrelated eligible filings")
	assert.NotContains(t, res.Trace.RejectedAccessions, "0000002488-26-000012",
		"a different-period eligible filing never competed and must be excluded")
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

// TestIsStale_NewestPeriodGoverns is the MEDIUM-1 pin: staleness is decided by
// the NEWEST referenced guidance period, not the oldest. An artifact that
// references BOTH a lapsed period (FY2024) and a still-current period (FY2026)
// must NOT be stale when as-of falls within FY2026 — the current period is the
// one that matters. The prior implementation returned stale as soon as ANY
// included period lapsed, which would wrongly discard live guidance.
func TestIsStale_NewestPeriodGoverns(t *testing.T) {
	multi := &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusValidated,
		Issuer:        Issuer{Ticker: "AMD", CIK: testCIK},
		Filing:        Filing{Accession: "0000002488-26-000111", FormType: "10-K", FilingDate: "2026-02-04", PeriodEnd: "2025-12-28"},
		Extraction: &Extraction{
			// A lapsed FY2024 capex envelope AND a still-current FY2026 revenue
			// envelope. The newest period (FY2026) governs staleness.
			CapExGuidance: &Envelope{
				ValueLow: Float(0.9e9), ValueHigh: Float(1.1e9), Unit: UnitAbsoluteUSD, Period: "FY2024",
				Basis:      &Basis{GrossOrNet: "gross", CashOrAccrual: "cash", GAAPOrNonGAAP: "gaap"},
				Confidence: 0.80,
				Evidence:   []Evidence{{Quote: "fiscal 2024 capex ~$1.0B", Location: "Item 7"}},
			},
			RevenueGuidance: []Envelope{{
				ValueLow: Float(0.20), ValueHigh: Float(0.24), Unit: UnitPct, Period: "FY2026",
				Confidence: 0.80,
				Evidence:   []Evidence{{Quote: "fiscal 2026 revenue growth ~22%", Location: "Item 7"}},
			}},
		},
		Validation: Validation{Status: string(StatusValidated), Confidence: 0.80},
	}

	// as-of within FY2026: the newest period has NOT lapsed ⇒ not stale (even
	// though the FY2024 envelope lapsed long ago).
	assert.False(t, isStale(multi, mustDate(t, "2026-06-01")),
		"newest period FY2026 is current ⇒ artifact is NOT stale despite a lapsed FY2024 envelope (MEDIUM-1)")

	// as-of after FY2026 has lapsed: the newest period is now stale.
	assert.True(t, isStale(multi, mustDate(t, "2027-06-01")),
		"once the newest period FY2026 lapses, the artifact is stale")
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

// TestLoader_TamperedAndStructurallyInvalid_HardErrors is the HIGH-4 pin:
// content-hash verification must run BEFORE structural validation. A tampered
// artifact that is ALSO structurally invalid must HARD-ERROR as a content-hash
// mismatch — not be silently SKIPPED as a structural failure (which would let a
// tampered immutable artifact slip through the immutability guarantee).
//
// We author a structurally-INVALID artifact (value_low > value_high) and stamp a
// DELIBERATELY WRONG artifact_sha256 (not recomputed), so it is both tampered
// AND invalid. With hash-first ordering the load hard-errors; with the prior
// structural-first ordering it would have degraded to skip ⇒ Absent.
func TestLoader_TamperedAndStructurallyInvalid_HardErrors(t *testing.T) {
	root := t.TempDir()

	a := validCapExArtifact()
	a.Extraction.CapExGuidance.ValueLow = Float(9e9) // structurally invalid (> value_high)
	a.ArtifactSHA256 = "deadbeef"                    // tampered: NOT the recomputed hash

	dir := filepath.Join(root, a.Issuer.CIK)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body, err := json.MarshalIndent(a, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, a.Filing.Accession+".json"), body, 0o644))

	l := NewLoader(root)
	_, err = l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.Error(t, err, "a tampered-AND-invalid artifact must hard-error on the hash, not silently skip")
	assert.True(t, errors.Is(err, ErrContentHashMismatch),
		"content-hash verification must run before structural validation (HIGH-4)")
}

// TestLoader_UnknownSchemaMajor_SkipsWithoutHashError is the HIGH-4 companion:
// an artifact with an UNSUPPORTED schema major is a forward-compat SKIP, not a
// tamper — so the schema-major gate must run BEFORE the hash check, and an
// unknown-major artifact must degrade to skip even if its hash would not verify
// against THIS loader's struct shape.
func TestLoader_UnknownSchemaMajor_SkipsWithoutHashError(t *testing.T) {
	root := t.TempDir()
	// A valid artifact so the load still resolves SOMETHING (proves the unknown-
	// major sibling was skipped, not hard-errored).
	writeArtifact(t, root, validCapExArtifact())

	future := validCapExArtifact()
	future.Filing.Accession = "0000002488-26-000088"
	future.SchemaVersion = "2.0.0" // unsupported major
	// Deliberately leave a non-matching hash (do NOT recompute) — the major gate
	// must short-circuit before the hash check, so this must NOT hard-error.
	future.ArtifactSHA256 = "deadbeef"
	dir := filepath.Join(root, future.Issuer.CIK)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body, err := json.MarshalIndent(future, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, future.Filing.Accession+".json"), body, 0o644))

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err, "an unknown-major sibling must degrade to skip, never hard-error on the hash")
	require.NotNil(t, res.Artifact)
	assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession)
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
	bad.Extraction.CapExGuidance.ValueLow = Float(9e9) // > value_high
	writeArtifact(t, root, bad)

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err)
	require.NotNil(t, res.Artifact)
	assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession, "structurally-invalid sibling skipped")
}

// TestLoader_IssuerCIKMustMatchDir is the MEDIUM-2 pin: a hash-valid artifact
// that is physically placed under the WRONG CIK directory (its issuer.cik does
// not match the directory it sits in) must be SKIPPED — never applied to the
// wrong issuer. A misplaced (but otherwise self-consistent) artifact under
// 0000002488/ that claims issuer.cik 0000000320 would silently corrupt AMD's
// valuation if consumed.
func TestLoader_IssuerCIKMustMatchDir(t *testing.T) {
	root := t.TempDir()

	// A valid AMD artifact under the AMD dir (the legitimate hit).
	writeArtifact(t, root, validCapExArtifact())

	// A self-consistent artifact whose issuer.cik is a DIFFERENT company, written
	// (by filename) into the AMD directory. writeArtifact uses a.Issuer.CIK for
	// the dir, so we write it manually into the AMD dir to model the misplacement.
	misplaced := validCapExArtifact()
	misplaced.Filing.Accession = "0000000320-26-000001"
	misplaced.Issuer.Ticker = "AAPL"
	misplaced.Issuer.CIK = "0000000320" // NOT the AMD dir it will live in
	h, err := ComputeArtifactSHA256(misplaced)
	require.NoError(t, err)
	misplaced.ArtifactSHA256 = h // hash is VALID — only the location is wrong

	amdDir := filepath.Join(root, testCIK)
	require.NoError(t, os.MkdirAll(amdDir, 0o755))
	body, err := json.MarshalIndent(misplaced, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(amdDir, misplaced.Filing.Accession+".json"), body, 0o644))

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err, "a misplaced hash-valid artifact must skip, not hard-error")
	require.NotNil(t, res.Artifact)
	assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession,
		"the misplaced foreign-CIK artifact must be skipped; only the legitimate AMD artifact resolves (MEDIUM-2)")
	assert.NotContains(t, res.Trace.RejectedAccessions, "0000000320-26-000001",
		"a skipped (wrong-CIK) artifact never enters the candidate set")
}

// TestLoader_MaliciousCIK_CannotEscapeRoot is the MEDIUM-1 path-traversal guard.
// A CIK is an attacker-influenced key (Phase 3 will derive it from request /
// SEC data), so the loader MUST refuse anything that is not the canonical
// zero-padded 10-digit form BEFORE joining it onto Root. A traversal payload
// like "../../etc" or "0000002488/../.." must resolve to absence (never read a
// file outside Root, never error in a way that leaks the path).
func TestLoader_MaliciousCIK_CannotEscapeRoot(t *testing.T) {
	// Lay out a tree where a traversal payload would land on a directory that
	// HOLDS loadable .json artifacts, so an un-sanitized join is observable:
	//
	//	<base>/secrets/<accession>.json   ← escape target (loadable artifacts)
	//	<base>/fixtures/                   ← Root
	//
	// A payload like "../secrets" joined onto Root (<base>/fixtures) resolves to
	// <base>/secrets — a directory full of .json artifacts the loader would read
	// if it did not validate the CIK. A correct loader refuses every non-canonical
	// CIK and resolves to absence.
	base := t.TempDir()
	root := filepath.Join(base, "fixtures")
	require.NoError(t, os.MkdirAll(root, 0o755))
	// Plant loadable artifacts in the escape target directory.
	writeArtifact(t, base, validCapExArtifact()) // writes base/<cik>/<acc>.json

	l := NewLoader(root)
	for _, badCIK := range []string{
		"../../etc",
		"0000002488/../..",
		"..",
		"../" + testCIK,            // lands on base/<cik> (the planted escape target)
		`..\` + testCIK,            // backslash-separated traversal (Windows)
		"0000002488/../0000002488", // contains separators ⇒ not the canonical form
		"abcdefghij",               // 10 chars but non-numeric
		"000000248",                // 9 digits
		"00000024888",              // 11 digits
		"",                         // empty
	} {
		t.Run(badCIK, func(t *testing.T) {
			res, err := l.Load(badCIK, mustDate(t, "2026-03-01"))
			require.NoError(t, err, "a malicious CIK must degrade to absence, never a hard error")
			assert.True(t, res.Absent, "malicious CIK must not resolve any artifact")
			assert.Nil(t, res.Artifact, "malicious CIK must not read outside Root")
		})
	}
}

// TestLoader_CanonicalCIK_StillResolves pins that the MEDIUM-1 sanitization does
// NOT regress the happy path: a canonical zero-padded 10-digit CIK still loads.
func TestLoader_CanonicalCIK_StillResolves(t *testing.T) {
	root := t.TempDir()
	writeArtifact(t, root, validCapExArtifact())

	l := NewLoader(root)
	res, err := l.Load(testCIK, mustDate(t, "2026-03-01"))
	require.NoError(t, err)
	require.NotNil(t, res.Artifact)
	assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession)
}

// TestLoader_UnpaddedCIK_NormalizesAndResolves is the regression pin for the
// live-run defect (2026-06-08): SEC serializes AMD's CIK as the un-padded "2488"
// (see ports.FlexibleCIK / CLAUDE.md) but the fixture directory is the
// zero-padded "0000002488". The loader MUST normalize the incoming CIK before
// lookup; the prior reject-only `^[0-9]{10}$` guard turned every un-padded
// production CIK into a false "absent", so a fixture was never consumed live even
// though every unit test (which used the padded form) passed.
func TestLoader_UnpaddedCIK_NormalizesAndResolves(t *testing.T) {
	root := t.TempDir()
	writeArtifact(t, root, validCapExArtifact()) // issuer.cik "0000002488"

	l := NewLoader(root)
	for _, cik := range []string{"2488", "0000002488", "002488"} {
		res, err := l.Load(cik, mustDate(t, "2026-03-01"))
		require.NoError(t, err, "cik=%q", cik)
		assert.Falsef(t, res.Absent, "cik=%q must normalize to 0000002488 and resolve", cik)
		require.NotNilf(t, res.Artifact, "cik=%q", cik)
		assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession)
	}

	// A non-numeric / traversal payload still degrades to absence (MEDIUM-1).
	for _, bad := range []string{"../../etc", "0000002488/../..", "abc", "", "12345678901"} {
		res, err := l.Load(bad, mustDate(t, "2026-03-01"))
		require.NoError(t, err, "cik=%q", bad)
		assert.Truef(t, res.Absent, "cik=%q must be rejected to absence", bad)
	}
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
	a.Extraction.CapExGuidance.ValueHigh = Float(9.9e9) // mutate AFTER hashing → mismatch
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
