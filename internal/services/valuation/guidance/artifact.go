// Package guidance owns the Layer-B Phase-2 guidance-artifact contract: the
// immutable, content-addressed JSON fixture that records forward-looking
// management guidance (CapEx / margin / revenue) extracted from a single SEC
// filing, plus the deterministic loader that resolves at most one artifact per
// (CIK, as-of) valuation.
//
// Design of record: docs/refactoring/spec/layer-b-phase2-guidance-fixture-spec.md
//
// This package is a LEAF in the valuation dependency graph (like
// internal/services/valuation/profile): it MUST NOT import
// internal/services/valuation/models or internal/core/entities. The
// import-boundary guard lives in import_boundary_test.go. Keeping the package
// entity-free preserves the NF3 hermetic-replay contract — the loader touches
// only the fixture filesystem (or, in replay, the captured bundle bytes), never
// any DCF math or domain model.
package guidance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// SchemaVersion is the current guidance-artifact schema version. The loader
// rejects an artifact whose major version differs (forward-compat gate, §8.3 /
// Decision 1). Bump the MINOR for additive optional fields; bump the MAJOR for
// any field-meaning change so old loaders refuse to silently misread.
const SchemaVersion = "1.0.0"

// Status enumerates the artifact-level status (§8.3 item 3, Decision 1). Only
// StatusValidated is eligible to supply a NUMERIC override (§9.3); every other
// status falls through to the Layer-A trajectory but is still captured.
type Status string

const (
	// StatusValidated marks a fully-validated artifact whose envelopes may
	// supply numeric near-term anchors (subject to the §9.3 guardrails).
	StatusValidated Status = "validated"
	// StatusNeedsReview marks an artifact the validator could not fully
	// accept; consumed as qualitative context only, never a numeric anchor.
	StatusNeedsReview Status = "needs_review"
	// StatusRejected marks an artifact the validator rejected; not consumed.
	StatusRejected Status = "rejected"
	// StatusNoGuidanceFound is the mandatory positive "we looked at this
	// filing and found no explicit guidance" record (§8.3 item 3). It is a
	// complete, valid artifact with extraction absent/empty — cacheable and
	// replay-pinnable because the absence is attributed to a specific filing.
	StatusNoGuidanceFound Status = "no_explicit_guidance_found"
)

// Unit enumerates the envelope value unit (§8.6 scale-error defense). Recorded
// so a consumer never silently mixes an absolute-USD CapEx figure with a margin
// fraction.
type Unit string

const (
	// UnitAbsoluteUSD marks an absolute USD figure (e.g. CapEx guidance).
	UnitAbsoluteUSD Unit = "absolute_usd"
	// UnitPct marks a fraction in [0,1] (e.g. an operating-margin guidance).
	UnitPct Unit = "pct"
)

// Provider enumerates the ai_provenance.provider. Phase-2 fixtures are
// hand-authored; Phase 3's extraction tool stamps "anthropic" etc.
const (
	// ProviderHandAuthored is the provider value for a Phase-2 hand-authored
	// fixture.
	ProviderHandAuthored = "hand_authored"
)

// Issuer identifies the company the artifact speaks to.
type Issuer struct {
	Ticker string `json:"ticker"`
	// CIK is the zero-padded 10-digit form (matches SEC / ports.FlexibleCIK),
	// e.g. "0000002488" for AMD.
	CIK string `json:"cik"`
}

// Filing is the immutable identity block (§8.3 item 1). The (Issuer.CIK,
// Filing.Accession) pair is the artifact key — NEVER ticker/date.
type Filing struct {
	// Accession is the immutable identity together with the CIK, e.g.
	// "0000002488-26-000012".
	Accession string `json:"accession"`
	// FormType drives the conflict tie-break (form specificity), e.g.
	// "10-K", "10-Q", "10-K/A".
	FormType string `json:"form_type"`
	// FilingDate (YYYY-MM-DD) drives conflict resolution (newest wins) and
	// the as-of eligibility cutoff.
	FilingDate string `json:"filing_date"`
	// PeriodEnd (YYYY-MM-DD) is the fiscal period the filing reports; drives
	// same-period conflict grouping.
	PeriodEnd string `json:"period_end"`
	// SECURL is a human reference (optional).
	SECURL string `json:"sec_url,omitempty"`
	// SourceDocSHA256 hashes the raw filing text (optional; Phase 3 fills it).
	SourceDocSHA256 string `json:"source_doc_sha256,omitempty"`
}

// Basis records the accounting basis of an envelope value. gaap_or_non_gaap
// MUST be present for a margin envelope (never silently mix, §8.6); the
// validator enforces it.
type Basis struct {
	GrossOrNet            string `json:"gross_or_net,omitempty"`
	CashOrAccrual         string `json:"cash_or_accrual,omitempty"`
	GAAPOrNonGAAP         string `json:"gaap_or_non_gaap,omitempty"`
	ConsolidatedOrSegment string `json:"consolidated_or_segment,omitempty"`
}

// Evidence is a single forward-looking supporting quote. A NUMERIC override
// requires >=1 evidence quote (§9.3); the validator enforces it.
type Evidence struct {
	Quote    string `json:"quote"`
	Location string `json:"location"`
}

// Envelope is the shared shape for every guidance kind (CapEx / margin /
// revenue). value_low <= value_high is an invariant; for a point estimate
// value_low == value_high.
type Envelope struct {
	// ValueLow is the low end of the guidance range. POINTER (HIGH-2): a
	// numeric envelope MUST carry an EXPLICIT value — an omitted JSON field
	// unmarshals to nil (not 0), so ValidateStructural can reject the
	// silent-zero anchoring the §9.3 "explicit value required" guardrail
	// forbids. A float64 field would coerce a missing value_low to 0 and pass.
	ValueLow *float64 `json:"value_low"`
	// ValueHigh is the high end. Invariant: *ValueLow <= *ValueHigh. POINTER for
	// the same explicit-value reason as ValueLow (HIGH-2).
	ValueHigh *float64 `json:"value_high"`
	// Unit defends against scale errors (§8.6).
	Unit Unit `json:"unit"`
	// Period is the explicit fiscal period, e.g. "FY2026". Ambiguous/empty
	// makes the envelope invalid (§8.6 period-ambiguity rule).
	Period string `json:"period"`
	// Basis is recorded; gaap_or_non_gaap required for margin envelopes.
	Basis *Basis `json:"basis,omitempty"`
	// Confidence is the validator-computed per-envelope confidence in [0,1]
	// (§8.3 item 6).
	Confidence float64 `json:"confidence"`
	// Evidence is required-if-numeric: >=1 forward-looking quote (§9.3).
	Evidence []Evidence `json:"evidence,omitempty"`
}

// Midpoint returns (*ValueLow + *ValueHigh) / 2 — the Decision-6 anchor value.
// It assumes both bounds are present (ValidateStructural rejects a numeric
// envelope with a nil bound, HIGH-2); a nil bound is treated as 0 defensively so
// the function stays total even if called on an unvalidated envelope.
func (e Envelope) Midpoint() float64 {
	var lo, hi float64
	if e.ValueLow != nil {
		lo = *e.ValueLow
	}
	if e.ValueHigh != nil {
		hi = *e.ValueHigh
	}
	return (lo + hi) / 2
}

// Float returns a pointer to v. It is the canonical helper for authoring
// Envelope.ValueLow/ValueHigh (now *float64, HIGH-2) in fixtures and tests
// without a one-off local variable per value.
func Float(v float64) *float64 { return &v }

// Extraction holds the three guidance envelopes. Absent/empty when
// status == no_explicit_guidance_found.
type Extraction struct {
	// CapExGuidance is the (optional) single CapEx envelope.
	CapExGuidance *Envelope `json:"capex_guidance,omitempty"`
	// MarginGuidance is the (possibly empty) list of margin envelopes.
	MarginGuidance []Envelope `json:"margin_guidance,omitempty"`
	// RevenueGuidance is the (possibly empty) list of revenue envelopes.
	RevenueGuidance []Envelope `json:"revenue_guidance,omitempty"`
}

// SourceSelection records the provenance of the extracted text region. Absent
// on a no_explicit_guidance_found record.
type SourceSelection struct {
	Sections           []string `json:"sections,omitempty"`
	SelectedTextSHA256 string   `json:"selected_text_sha256,omitempty"`
}

// AIProvenance mirrors midas's existing entities.AIProvenance shape and the
// internal/services/datacleaner/adjustments/hash.go canonical-hashing
// discipline. For a hand-authored Phase-2 fixture provider == "hand_authored"
// and the hashes are computed over the fixture inputs. The guidance
// AIProvenance is recorded for audit/replay; it never DRIVES a value in Phase 2
// (the value comes from Extraction).
type AIProvenance struct {
	Provider             string  `json:"provider"`
	ModelName            string  `json:"model_name"`
	ModelVersion         string  `json:"model_version"`
	Temperature          float64 `json:"temperature"`
	PromptSHA256         string  `json:"prompt_sha256,omitempty"`
	SchemaSHA256         string  `json:"schema_sha256,omitempty"`
	RawResponseSHA256    string  `json:"raw_response_sha256,omitempty"`
	ExtractionCodeGitSHA string  `json:"extraction_code_git_sha,omitempty"`
}

// Validation is the artifact-level validator verdict. Confidence here is the
// ARTIFACT-level confidence; per-envelope confidence lives inside each
// Envelope.
type Validation struct {
	Status                    string   `json:"status"`
	Confidence                float64  `json:"confidence"`
	Warnings                  []string `json:"warnings,omitempty"`
	NormalizationRulesVersion string   `json:"normalization_rules_version"`
	ValidatorVersion          string   `json:"validator_version"`
}

// Artifact is the top-level guidance-artifact contract (Decision 1). It is
// immutable and content-addressed: ArtifactSHA256 is the SHA-256 over the
// canonical serialization of every field EXCEPT ArtifactSHA256 itself.
type Artifact struct {
	SchemaVersion   string           `json:"schema_version"`
	Status          Status           `json:"status"`
	Issuer          Issuer           `json:"issuer"`
	Filing          Filing           `json:"filing"`
	SourceSelection *SourceSelection `json:"source_selection,omitempty"`
	Extraction      *Extraction      `json:"extraction,omitempty"`
	AIProvenance    *AIProvenance    `json:"ai_provenance,omitempty"`
	Validation      Validation       `json:"validation"`
	// ArtifactSHA256 is the content-address (hex). Excluded from the hash
	// preimage by ComputeArtifactSHA256.
	ArtifactSHA256 string `json:"artifact_sha256"`
}

// ComputeArtifactSHA256 returns the SHA-256 hex digest of the artifact's
// canonical serialization, computed over EVERY field except ArtifactSHA256
// itself (the content-address cannot include itself).
//
// Determinism (NF2) follows the hash.go discipline exactly:
//   - No wall-clock participates (the artifact carries no time fields in its
//     hash preimage — dates are strings the author supplies, not Now()).
//   - encoding/json sorts struct fields in declaration order and map keys
//     alphabetically; the canonical struct below pins field order so two
//     structurally-equal artifacts always hash identically regardless of how
//     the source JSON was key-ordered.
//   - Validation.Warnings / Evidence / envelope slices are serialized in
//     their stored order; the author controls that order and it is preserved
//     verbatim on round-trip, so the hash is stable across load/save.
//
// The hash is taken over the same Go types the loader unmarshals into, so a
// recomputed hash on load matches the embedded value byte-for-byte (F2). A
// mismatch is the loader's one hard error.
func ComputeArtifactSHA256(a *Artifact) (string, error) {
	if a == nil {
		return "", fmt.Errorf("guidance: ComputeArtifactSHA256: nil artifact")
	}

	// Copy with the content-address field zeroed so it never participates in
	// its own preimage. A value copy is sufficient — the pointer fields are
	// shared but never mutated here.
	preimage := *a
	preimage.ArtifactSHA256 = ""

	// encoding/json on this struct CANNOT fail for the scalar + slice + map
	// shape the Artifact uses; a regression in the stdlib that broke that
	// invariant should surface LOUDLY rather than silently hash an empty
	// string (mirrors hash.go's panic-on-outer-Marshal-error rationale).
	buf, err := json.Marshal(&preimage)
	if err != nil {
		return "", fmt.Errorf("guidance: ComputeArtifactSHA256: marshal: %w", err)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// sha256HexString is the small helper used by fixtures/tests and the
// AIProvenance hash fields to hash an arbitrary string deterministically (same
// rule as hash.go::sha256Hex). Exported so the B7 fixture-hash test helper can
// compute synthetic source/prompt hashes without a prod tool.
func sha256HexString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SHA256Hex returns the SHA-256 hex digest of s. Exposed for fixture authoring
// (B7) and AIProvenance hash computation; deterministic and wall-clock-free.
func SHA256Hex(s string) string {
	return sha256HexString(s)
}

// SHA256HexStrings hashes a sorted, newline-joined serialization of the input
// strings — used to fingerprint an ordered set deterministically (e.g. a list
// of selected sections). Sorting makes the digest order-insensitive, matching
// the hash.go sorted-key discipline for set-like inputs.
func SHA256HexStrings(parts []string) string {
	cp := make([]string, len(parts))
	copy(cp, parts)
	sort.Strings(cp)
	joined := ""
	for i, p := range cp {
		if i > 0 {
			joined += "\n"
		}
		joined += p
	}
	return sha256HexString(joined)
}
