package guidance

import (
	"errors"
	"fmt"
)

// Validation sentinels. Callers (the loader, the resolver) use errors.Is to
// classify a structural failure vs. a content-hash mismatch. A structural
// failure degrades to the absent path with a Warnings entry (NF4); a
// content-hash mismatch is the one hard error (F2) and lives in loader.go.
var (
	// ErrUnknownSchemaMajor is returned when the artifact's schema_version
	// major differs from the loader's supported major (forward-compat gate).
	ErrUnknownSchemaMajor = errors.New("guidance: unknown schema major version")
	// ErrInvalidArtifact is the umbrella for any structural validation
	// failure. Wrapped errors carry a specific field-level reason.
	ErrInvalidArtifact = errors.New("guidance: invalid artifact")
)

// supportedSchemaMajor is the schema major version this loader understands.
// Derived from SchemaVersion ("1.0.0" → "1"); an artifact with a different
// major is refused (Decision 1 forward-compat gate). Kept as a literal rather
// than parsed from SchemaVersion so a deliberate major bump is a conscious
// two-line edit (constant + this value), not an accidental follow-on.
const supportedSchemaMajor = "1"

// ValidateStructural enforces the structural contract of an artifact (Decision
// 1 / §8.6 / §9.3). It is PURE — no I/O, no clock — so it is replay-safe and
// cheap to call on every load.
//
// Rules enforced:
//   - schema_version present with a supported MAJOR (ErrUnknownSchemaMajor on
//     a different major so an old loader never silently misreads a v2 shape);
//   - status is one of the known enum values;
//   - issuer.ticker / issuer.cik present;
//   - filing.accession / form_type / filing_date / period_end present (the
//     absence record still attributes itself to a specific filing, §8.3 item 3);
//   - for a non-absent status, every present envelope satisfies envelope rules
//     (value_low <= value_high, unit known, period non-empty, margin basis
//     present, evidence required when the envelope is numeric);
//   - for status == no_explicit_guidance_found, extraction MUST be absent/empty
//     (the absence record carries no numbers).
//
// It does NOT recompute the content hash — that is the loader's hard-error
// responsibility (F2) and requires the embedded value to compare against.
func ValidateStructural(a *Artifact) error {
	if a == nil {
		return fmt.Errorf("%w: nil artifact", ErrInvalidArtifact)
	}

	if a.SchemaVersion == "" {
		return fmt.Errorf("%w: schema_version is required", ErrInvalidArtifact)
	}
	if major := schemaMajor(a.SchemaVersion); major != supportedSchemaMajor {
		return fmt.Errorf("%w: schema_version=%q (supported major=%q)",
			ErrUnknownSchemaMajor, a.SchemaVersion, supportedSchemaMajor)
	}

	if !knownStatus(a.Status) {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidArtifact, a.Status)
	}

	if a.Issuer.Ticker == "" {
		return fmt.Errorf("%w: issuer.ticker is required", ErrInvalidArtifact)
	}
	if a.Issuer.CIK == "" {
		return fmt.Errorf("%w: issuer.cik is required", ErrInvalidArtifact)
	}

	if a.Filing.Accession == "" {
		return fmt.Errorf("%w: filing.accession is required", ErrInvalidArtifact)
	}
	if a.Filing.FormType == "" {
		return fmt.Errorf("%w: filing.form_type is required", ErrInvalidArtifact)
	}
	if a.Filing.FilingDate == "" {
		return fmt.Errorf("%w: filing.filing_date is required", ErrInvalidArtifact)
	}
	if a.Filing.PeriodEnd == "" {
		return fmt.Errorf("%w: filing.period_end is required", ErrInvalidArtifact)
	}
	// Dates must parse to a canonical YYYY-MM-DD (so eligibility + staleness
	// comparisons in the loader are total and deterministic — NF2).
	if _, err := parseFilingDate(a.Filing.FilingDate); err != nil {
		return fmt.Errorf("%w: filing.filing_date %q: %v", ErrInvalidArtifact, a.Filing.FilingDate, err)
	}
	if _, err := parseFilingDate(a.Filing.PeriodEnd); err != nil {
		return fmt.Errorf("%w: filing.period_end %q: %v", ErrInvalidArtifact, a.Filing.PeriodEnd, err)
	}

	// The absence record must carry NO extraction (it is a positive
	// "we looked, found nothing"). A present, non-empty extraction with this
	// status is a contradiction.
	if a.Status == StatusNoGuidanceFound {
		if a.Extraction != nil && !a.Extraction.isEmpty() {
			return fmt.Errorf("%w: status=no_explicit_guidance_found must not carry extraction", ErrInvalidArtifact)
		}
		return nil
	}

	if a.Extraction == nil {
		return nil // a non-absent artifact with no extraction supplies no anchor; not a structural error
	}

	if a.Extraction.CapExGuidance != nil {
		if err := validateEnvelope("capex_guidance", *a.Extraction.CapExGuidance); err != nil {
			return err
		}
	}
	for i := range a.Extraction.MarginGuidance {
		if err := validateEnvelope(fmt.Sprintf("margin_guidance[%d]", i), a.Extraction.MarginGuidance[i]); err != nil {
			return err
		}
	}
	for i := range a.Extraction.RevenueGuidance {
		if err := validateEnvelope(fmt.Sprintf("revenue_guidance[%d]", i), a.Extraction.RevenueGuidance[i]); err != nil {
			return err
		}
	}
	return nil
}

// validateEnvelope enforces the per-envelope structural rules. kind is the
// JSON-ish field path used in the error for operator legibility.
func validateEnvelope(kind string, e Envelope) error {
	if e.ValueLow > e.ValueHigh {
		return fmt.Errorf("%w: %s value_low (%g) > value_high (%g)", ErrInvalidArtifact, kind, e.ValueLow, e.ValueHigh)
	}
	if e.Unit != UnitAbsoluteUSD && e.Unit != UnitPct {
		return fmt.Errorf("%w: %s unknown unit %q", ErrInvalidArtifact, kind, e.Unit)
	}
	if e.Period == "" {
		// §8.6 period-ambiguity rule: an empty period makes the envelope
		// unconsumable. Surface it structurally so the fixture author fixes
		// it rather than the loader silently dropping the envelope.
		return fmt.Errorf("%w: %s period is required (empty period is ambiguous, §8.6)", ErrInvalidArtifact, kind)
	}
	// gaap_or_non_gaap MUST be present for a margin envelope (§8.6 — never
	// silently mix GAAP and non-GAAP margins). Identified by kind prefix.
	if isMarginKind(kind) {
		if e.Basis == nil || e.Basis.GAAPOrNonGAAP == "" {
			return fmt.Errorf("%w: %s basis.gaap_or_non_gaap is required for a margin envelope (§8.6)", ErrInvalidArtifact, kind)
		}
	}
	// evidence-required-for-numeric (§9.3): an envelope carrying an explicit
	// numeric value MUST cite >=1 forward-looking evidence quote. Every
	// envelope that reaches here is numeric by construction (it has value_low/
	// value_high floats), so evidence is unconditionally required.
	if len(e.Evidence) == 0 {
		return fmt.Errorf("%w: %s requires >=1 evidence quote for a numeric value (§9.3)", ErrInvalidArtifact, kind)
	}
	for j := range e.Evidence {
		if e.Evidence[j].Quote == "" {
			return fmt.Errorf("%w: %s evidence[%d].quote is empty", ErrInvalidArtifact, kind, j)
		}
	}
	return nil
}

// isEmpty reports whether the extraction carries no envelopes at all. Used to
// validate the absence record and to detect a no-op extraction.
func (x *Extraction) isEmpty() bool {
	if x == nil {
		return true
	}
	return x.CapExGuidance == nil && len(x.MarginGuidance) == 0 && len(x.RevenueGuidance) == 0
}

// knownStatus reports whether s is one of the four enumerated statuses.
func knownStatus(s Status) bool {
	switch s {
	case StatusValidated, StatusNeedsReview, StatusRejected, StatusNoGuidanceFound:
		return true
	default:
		return false
	}
}

// isMarginKind reports whether a kind path names a margin envelope.
func isMarginKind(kind string) bool {
	const prefix = "margin_guidance"
	return len(kind) >= len(prefix) && kind[:len(prefix)] == prefix
}

// schemaMajor extracts the major component of a semver string ("1.0.0" → "1").
// A string with no '.' is returned as-is so a bare "1" still works.
func schemaMajor(v string) string {
	for i := 0; i < len(v); i++ {
		if v[i] == '.' {
			return v[:i]
		}
	}
	return v
}
