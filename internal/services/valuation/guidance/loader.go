package guidance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// canonicalCIK is the ONLY accepted CIK form: the zero-padded 10-digit string
// (matches SEC / ports.FlexibleCIK and the on-disk fixture directory layout,
// Decision 2). Validating against this BEFORE joining the CIK onto Root closes
// the MEDIUM-1 path-traversal vector — a CIK is an attacker-influenceable key
// once Phase 3 derives it from request/SEC data, so a payload like "../../etc"
// or "0000002488/../.." must never reach filepath.Join. The pattern admits no
// path separators, no dots, and no "..": a matching string cannot escape Root.
var canonicalCIK = regexp.MustCompile(`^[0-9]{10}$`)

// ErrContentHashMismatch is the ONE hard error the loader raises on a present
// artifact (F2 / NF4): the recomputed artifact_sha256 does not match the
// embedded value, meaning the on-disk artifact was tampered with or corrupted.
// Silently consuming a mismatched artifact would violate immutability, so this
// is NOT degraded to the absent path — it propagates to the caller.
var ErrContentHashMismatch = errors.New("guidance: artifact content-hash mismatch")

// Resolution is the loader's output: at most one selected artifact, or a
// first-class absent record. Trace carries the deterministic selection audit
// (chosen + rejected accessions) for the bundle capture (Decision 8).
type Resolution struct {
	// Artifact is the selected artifact (a hit OR a positive
	// no_explicit_guidance_found record). nil when Absent.
	Artifact *Artifact
	// Absent is true when no eligible artifact was found at all (no fixture
	// dir, empty dir, disabled root, or every candidate filtered out). The
	// production default (empty Root) always yields Absent — the NF1 no-op.
	Absent bool
	// Trace records the deterministic selection for the bundle/diagnostics.
	Trace LoadTrace
}

// LoadTrace is the deterministic selection audit captured into 09-guidance.json.
type LoadTrace struct {
	// SelectedAccession is the accession of the chosen artifact ("" when Absent).
	SelectedAccession string `json:"selected_accession,omitempty"`
	// RejectedAccessions lists the SAME-period_end candidates that competed with
	// the winner and lost (the genuine conflict group, LOW-4), sorted for
	// determinism (NF2). Eligible filings from a different period_end never
	// competed and are deliberately excluded.
	RejectedAccessions []string `json:"rejected_accessions,omitempty"`
	// Stale is true when the selected artifact's newest guidance period has
	// lapsed relative to as-of (still captured, not consumed for a numeric
	// override — §8.3 item 5).
	Stale bool `json:"stale,omitempty"`
	// Reason is a short machine-stable tag for why the resolution landed
	// where it did (e.g. "disabled_root", "no_dir", "no_eligible_candidate",
	// "selected", "no_explicit_guidance_found").
	Reason string `json:"reason,omitempty"`
}

// Loader resolves, for a (CIK, as-of) valuation, at most one guidance artifact
// from a content-addressed fixture directory. It is the ONLY code that touches
// the fixture filesystem (or, via LoadFromBundle, the captured bundle bytes).
//
// Root is the fixture directory root (e.g. testdata/guidance). Empty Root means
// guidance is DISABLED — every Load returns Absent immediately. Production wires
// an empty Root in Phase 2 (the NF1 byte-identity guarantee); Phase 3 flips the
// default to the real directory.
//
// The loader is request-scoped and holds no cross-request mutable state, so it
// is safe to construct once and share (it performs read-only filesystem scans).
type Loader struct {
	Root string
}

// NewLoader constructs a Loader rooted at root. An empty root disables guidance.
func NewLoader(root string) *Loader {
	return &Loader{Root: root}
}

// Load resolves the guidance artifact for (cik, asOf). It is deterministic for a
// given (cik, asOf, on-disk fixture set): same inputs ⇒ same artifact or same
// absence (F1, NF2).
//
// Algorithm (Decision 2 / Decision 5):
//  1. Root == "" ⇒ Absent immediately (the production no-op).
//  2. List Root/<cik>/ in sorted order (NF2). No subdir / empty ⇒ Absent.
//  3. Parse + structurally-validate each file; recompute and verify
//     artifact_sha256 (HARD error on mismatch — F2). A structurally-invalid or
//     unreadable file is SKIPPED (degrade, not abort — NF4).
//  4. Filter to eligible candidates: filing_date <= asOf.
//  5. Group by period_end and apply conflict resolution (newest filing_date →
//     form-rank → accession-lex). Select the single newest-filing winner across
//     all groups. If the winner is no_explicit_guidance_found, return it as a
//     positive absence record. If no eligible candidate, return Absent.
//  6. Mark the winner stale (Trace.Stale) when its newest guidance period has
//     lapsed relative to asOf — captured, but the resolver will not consume a
//     numeric override from a stale artifact.
//
// A returned ErrContentHashMismatch is the only hard error; every other failure
// degrades to Absent so a malformed fixture never aborts a valuation (NF4). The
// caller (resolveGuidance) records a Warnings entry in that case.
func (l *Loader) Load(cik string, asOf time.Time) (Resolution, error) {
	if l == nil || l.Root == "" {
		return Resolution{Absent: true, Trace: LoadTrace{Reason: "disabled_root"}}, nil
	}

	// MEDIUM-1 path-traversal guard: reject any CIK that is not the canonical
	// zero-padded 10-digit form BEFORE it is joined onto Root. A non-canonical
	// CIK (separators, "..", non-digits) degrades to absence — it never reads a
	// directory outside Root and never raises a hard error (NF4 discipline). In
	// Phase 2 this is unreachable in production (empty Root short-circuits above),
	// but it closes the latent vector for when Phase 3 sets a live root.
	if !canonicalCIK.MatchString(cik) {
		return Resolution{Absent: true, Trace: LoadTrace{Reason: "invalid_cik"}}, nil
	}

	dir := filepath.Join(l.Root, cik)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No directory for this CIK (the common case) ⇒ first-class absence.
		// Any other read error also degrades to absent (NF4) — a valuation
		// must never fail because the fixture tree is unreadable.
		return Resolution{Absent: true, Trace: LoadTrace{Reason: "no_dir"}}, nil
	}

	// Sorted scan (NF2): collect candidate filenames deterministically.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var candidates []*Artifact
	for _, name := range names {
		path := filepath.Join(dir, name)
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			// Unreadable individual file: skip (degrade, NF4).
			continue
		}
		art, parseErr := parseAndVerify(body)
		if parseErr != nil {
			if errors.Is(parseErr, ErrContentHashMismatch) {
				// The one hard error: a present artifact whose content-hash
				// fails verification must NOT be silently dropped.
				return Resolution{}, fmt.Errorf("guidance: %s: %w", name, parseErr)
			}
			// Structural / schema / unmarshal failures degrade to skip (NF4).
			continue
		}
		candidates = append(candidates, art)
	}

	if len(candidates) == 0 {
		return Resolution{Absent: true, Trace: LoadTrace{Reason: "no_eligible_candidate"}}, nil
	}

	return selectArtifact(candidates, asOf), nil
}

// LoadFromBundle reconstructs a Resolution from a captured 09-guidance.json
// payload (the replay seam — NF3). The bundle stage embeds the selected
// artifact verbatim plus a small resolution envelope; here we re-derive the
// Resolution from the embedded artifact + envelope so replay consumes the
// captured artifact rather than scanning the live fixture directory.
//
// An empty/absent payload (an old bundle that predates guidance capture, or a
// captured absence) yields Absent — matching the production no-op so the replay
// stays bit-for-bit with the original valuation (which also had no guidance).
//
// A content-hash mismatch on a captured artifact is still a hard error: a
// tampered bundle must not silently replay a different value.
func LoadFromBundle(raw []byte) (Resolution, error) {
	if len(raw) == 0 {
		return Resolution{Absent: true, Trace: LoadTrace{Reason: "bundle_absent"}}, nil
	}

	stage, err := unmarshalBundleStage(raw)
	if err != nil {
		// A malformed bundle stage degrades to absent (NF4) — replay should
		// reproduce the absent path, not crash.
		return Resolution{Absent: true, Trace: LoadTrace{Reason: "bundle_parse_error"}}, nil
	}

	// A captured absence (status "absent" / no embedded artifact) ⇒ Absent.
	if stage.Artifact == nil {
		return Resolution{
			Absent: true,
			Trace: LoadTrace{
				Reason:            stage.Resolution.Status,
				SelectedAccession: stage.Resolution.SelectedAccession,
			},
		}, nil
	}

	// Verify the captured artifact's content hash (immutability across capture).
	if err := verifyArtifactHash(stage.Artifact); err != nil {
		return Resolution{}, fmt.Errorf("guidance: bundle artifact: %w", err)
	}

	// A captured no_explicit_guidance_found record replays as a positive
	// absence record (Artifact present, but the resolver treats it as absent).
	return Resolution{
		Artifact: stage.Artifact,
		Trace: LoadTrace{
			SelectedAccession:  stage.Resolution.SelectedAccession,
			RejectedAccessions: stage.Resolution.RejectedAccessions,
			Stale:              stage.Resolution.Stale,
			Reason:             stage.Resolution.Status,
		},
	}, nil
}

// parseAndVerify unmarshals, structurally-validates, and content-hash-verifies
// a single artifact payload. A content-hash mismatch returns
// ErrContentHashMismatch (hard); every other failure returns a wrapped
// ErrInvalidArtifact-class error the caller treats as skip.
func parseAndVerify(body []byte) (*Artifact, error) {
	art, err := unmarshalArtifact(body)
	if err != nil {
		return nil, err
	}
	if err := ValidateStructural(art); err != nil {
		return nil, err
	}
	if err := verifyArtifactHash(art); err != nil {
		return nil, err
	}
	return art, nil
}

// verifyArtifactHash recomputes artifact_sha256 and hard-errors on mismatch.
// An EMPTY embedded hash is tolerated as "unsigned fixture" ONLY would be a
// silent-tamper hole, so we require the embedded hash to be present AND correct.
func verifyArtifactHash(a *Artifact) error {
	want := a.ArtifactSHA256
	got, err := ComputeArtifactSHA256(a)
	if err != nil {
		return err
	}
	if want == "" || want != got {
		return fmt.Errorf("%w: embedded=%q recomputed=%q", ErrContentHashMismatch, want, got)
	}
	return nil
}

// selectArtifact applies eligibility (filing_date <= asOf), conflict resolution
// (Decision 2), and staleness to a non-empty candidate set, returning the
// resolved Resolution with a deterministic trace.
func selectArtifact(candidates []*Artifact, asOf time.Time) Resolution {
	// Eligibility: filing_date <= asOf. Validation guarantees parseable dates.
	eligible := make([]*Artifact, 0, len(candidates))
	for _, c := range candidates {
		fd, _ := parseFilingDate(c.Filing.FilingDate)
		if !fd.After(asOf) {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		return Resolution{Absent: true, Trace: LoadTrace{Reason: "no_eligible_candidate"}}
	}

	// Total-order the eligible set by the conflict-resolution ranking so the
	// FIRST element is the unambiguous winner across all period_end groups:
	//   (a) newest filing_date, (b) most-specific form_type, (c) lex-largest
	//   accession. Sorting the whole eligible set (rather than per-period) is
	//   equivalent: the global winner is the per-period winner of the newest
	//   period group, and the same total order picks it deterministically.
	sort.SliceStable(eligible, func(i, j int) bool {
		return moreAuthoritative(eligible[i], eligible[j])
	})
	winner := eligible[0]

	// LOW-4: rejected_accessions lists ONLY the candidates that actually
	// COMPETED with the winner — i.e. shared the winner's period_end. Eligible
	// filings from a DIFFERENT period_end never entered the winner's conflict
	// group (conflict resolution is per-period, Decision 2), so listing them
	// overstates conflict. Restrict to same-period competitors; the set stays
	// deterministic via the sort below.
	rejected := make([]string, 0, len(eligible)-1)
	for _, c := range eligible[1:] {
		if c.Filing.PeriodEnd == winner.Filing.PeriodEnd {
			rejected = append(rejected, c.Filing.Accession)
		}
	}
	sort.Strings(rejected)

	stale := isStale(winner, asOf)

	// A no_explicit_guidance_found winner is a positive absence record.
	reason := "selected"
	if winner.Status == StatusNoGuidanceFound {
		reason = string(StatusNoGuidanceFound)
	}

	return Resolution{
		Artifact: winner,
		Trace: LoadTrace{
			SelectedAccession:  winner.Filing.Accession,
			RejectedAccessions: rejected,
			Stale:              stale,
			Reason:             reason,
		},
	}
}

// formTypeRank ranks form specificity for the conflict tie-break (Decision 2):
// 10-K/A > 10-Q/A > 10-K > 10-Q > (anything else). Higher rank wins.
func formTypeRank(form string) int {
	switch strings.ToUpper(strings.TrimSpace(form)) {
	case "10-K/A":
		return 4
	case "10-Q/A":
		return 3
	case "10-K":
		return 2
	case "10-Q":
		return 1
	default:
		return 0
	}
}

// moreAuthoritative reports whether a should sort before b under the
// deterministic conflict-resolution total order (Decision 2): newest
// filing_date, then most-specific form_type, then lexicographically-largest
// accession. The accession tie-break guarantees a total order (no two distinct
// artifacts compare equal — accessions are unique on disk by filename).
func moreAuthoritative(a, b *Artifact) bool {
	fa, _ := parseFilingDate(a.Filing.FilingDate)
	fb, _ := parseFilingDate(b.Filing.FilingDate)
	if !fa.Equal(fb) {
		return fa.After(fb) // newer filing_date wins
	}
	ra, rb := formTypeRank(a.Filing.FormType), formTypeRank(b.Filing.FormType)
	if ra != rb {
		return ra > rb // more-specific form wins
	}
	// lexicographically-LARGEST accession wins → sort it first.
	return a.Filing.Accession > b.Filing.Accession
}

// isStale reports whether the winner's newest guidance period has lapsed
// relative to asOf (§8.3 item 5). A period parsed to a fiscal-year boundary
// <= asOf is stale. A no_explicit_guidance_found record carries no period and
// is never "stale" (it is already an absence). An unparseable / quarter-level
// period is treated as never-stale in Phase 2 (Phase 3 refines).
func isStale(a *Artifact, asOf time.Time) bool {
	if a.Status == StatusNoGuidanceFound || a.Extraction == nil {
		return false
	}
	for _, p := range a.Extraction.allPeriods() {
		boundary, ok := fiscalYearEnd(p)
		if !ok {
			continue
		}
		// boundary is the first instant of the year AFTER the fiscal year.
		// The period has lapsed once asOf reaches (or passes) that boundary.
		if !asOf.Before(boundary) {
			return true
		}
	}
	return false
}

// allPeriods returns every period string referenced by the extraction's
// envelopes (deduped order-preserving — for staleness any lapsed period
// suffices, so order does not affect the boolean result).
func (x *Extraction) allPeriods() []string {
	if x == nil {
		return nil
	}
	var out []string
	if x.CapExGuidance != nil {
		out = append(out, x.CapExGuidance.Period)
	}
	for _, e := range x.MarginGuidance {
		out = append(out, e.Period)
	}
	for _, e := range x.RevenueGuidance {
		out = append(out, e.Period)
	}
	return out
}
