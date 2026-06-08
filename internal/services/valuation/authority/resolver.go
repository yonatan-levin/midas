// Package authority implements the §9 assumption-authority hierarchy: the
// single deterministic place where, per near-term assumption, midas decides
// WHICH source supplied the final value (user override → guidance → profile →
// historical → default) and enforces the §9.3 anti-"assumption-laundering"
// guardrails.
//
// It is the guidance analogue of internal/services/valuation/params — the one
// authoritative source-decision path. It imports guidance (the artifact
// contract) and profile (the Layer-A trajectory), and is imported only by the
// valuation service seam (valuation.resolveGuidance). It contains NO filesystem
// access and NO DCF math; it is a pure function of its inputs, so it is
// replay-safe and bit-for-bit deterministic (NF2).
//
// Design of record: docs/refactoring/spec/layer-b-phase2-guidance-fixture-spec.md
// (Decision 3, Decision 6, §9, §9.3).
package authority

import (
	"fmt"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// DefaultConfidenceThreshold is the §9.3 minimum per-envelope confidence a
// guidance numeric anchor must clear. Phase 2 uses a single constant (default
// 0.70); calibration is a Phase-3 concern (open question — non-blocking).
const DefaultConfidenceThreshold = 0.70

// MaxAnchorYearIndex is the §9.3 dominance guardrail: a guidance anchor may
// touch AT MOST year 1 and year 2 (indices 0 and 1). The resolver hard-refuses
// any anchor at index >= 2, so guidance can never steer year 3+ or the
// terminal — the structural guarantee that "AI guidance does not dominate
// intrinsic value."
const MaxAnchorYearIndex = 1

// Source enumerates the §9 precedence levels (highest authority first).
type Source string

const (
	SourceUserOverride Source = "user_override"
	SourceGuidance     Source = "guidance"
	SourceProfile      Source = "profile"
	SourceHistorical   Source = "historical"
	SourceDefault      Source = "default"
)

// Assumption-key constants for the Sources map / bundle anchors_applied. Phase
// 2 steers only the near-term (year-1) knobs.
const (
	KeyCapExYear1           = "capex_year1"
	KeyOperatingMarginYear1 = "operating_margin_year1"
	KeyRevenueGrowthYear1   = "revenue_growth_year1"
)

// GuidanceStatus values surfaced on Resolution.GuidanceStatus (for the bundle
// envelope + diagnostics).
const (
	StatusValidated       = "validated"
	StatusNoGuidanceFound = "no_explicit_guidance_found"
	StatusStale           = "stale"
	StatusAbsent          = "absent"
	// StatusLowConfidence marks a VALIDATED artifact whose envelope confidence
	// fell below the §9.3 anchor threshold — the artifact itself is fine, the
	// numeric value is just not anchor-eligible. Distinct from a non-validated
	// artifact (LOW-3): a needs_review / rejected artifact surfaces its OWN
	// status (StatusNeedsReview / StatusRejected) so a high-confidence-but-
	// rejected artifact never reads as "low_confidence".
	StatusLowConfidence = "low_confidence"
	// StatusNeedsReview / StatusRejected mirror the underlying guidance artifact
	// statuses verbatim (LOW-3). Both fall through to the Layer-A trajectory
	// (no numeric anchor) but the diagnostic tag is honest about WHY.
	StatusNeedsReview = "needs_review"
	StatusRejected    = "rejected"
)

// AssumptionSource records the final source + value provenance for one
// assumption (the diagnostic block, Decision 3 / Decision 4).
type AssumptionSource struct {
	Level  Source
	Value  float64
	Detail string
}

// NearTermAnchors carries only what Phase 2 actually steers: the year-1
// (optionally year-2) near-term DCF reinvestment-model inputs. A nil pointer
// means "no anchor for this knob" ⇒ Layer A runs unchanged for it (NF1).
type NearTermAnchors struct {
	CapExYear1           *float64
	OperatingMarginYear1 *float64
	RevenueGrowthYear1   *float64

	CapExYear2           *float64
	OperatingMarginYear2 *float64
	RevenueGrowthYear2   *float64
}

// IsEmpty reports whether no anchor at all is present. When true,
// applyReinvestmentModel must be byte-identical to Layer A (NF1).
func (a NearTermAnchors) IsEmpty() bool {
	return a.CapExYear1 == nil && a.OperatingMarginYear1 == nil && a.RevenueGrowthYear1 == nil &&
		a.CapExYear2 == nil && a.OperatingMarginYear2 == nil && a.RevenueGrowthYear2 == nil
}

// Resolution is the resolver output.
type Resolution struct {
	// Sources maps each touched assumption key to its final source + value.
	Sources map[string]AssumptionSource
	// Anchors are the near-term DCF inputs the reinvestment model consumes.
	Anchors NearTermAnchors
	// Warnings are the human-readable source tags appended to result.Warnings
	// (RM-1 / Layer-A convention).
	Warnings []string
	// Captured is the artifact captured into the bundle (the selected artifact
	// or the absent record); nil when there was nothing to capture.
	Captured *guidance.Artifact
	// GuidanceStatus is the machine-stable outcome tag.
	GuidanceStatus string
	// AnchorsApplied names the knobs actually anchored from guidance (for the
	// bundle envelope). Deterministic order (§NF2): capex, margin, revenue.
	AnchorsApplied []string
}

// Input bundles everything the resolver needs. ResolvedProfile is the Layer-A
// profile (may be nil on test paths). UserOverriddenKnobs is the set of
// guidance-steerable knob keys the request explicitly set (Phase 2: always
// empty — params has no per-near-term-knob override yet — but modeled so the
// level-1 precedence is structurally correct and future-proof). ConfidenceThreshold
// <= 0 falls back to DefaultConfidenceThreshold.
type Input struct {
	Loaded              guidance.Resolution
	Profile             *profile.ResolvedProfile
	UserOverriddenKnobs map[string]bool
	ConfidenceThreshold float64
}

// Resolve applies the §9 precedence + §9.3 guardrails and returns the
// near-term anchors plus the per-assumption source map.
//
// Precedence per assumption (highest first): user_override → guidance →
// profile → historical → default. In Phase 2 the resolver only PRODUCES a
// guidance-sourced anchor when guidance clears every §9.3 guardrail; otherwise
// it records that the assumption falls through to the profile (the default for
// the vast majority) and produces NO anchor — which keeps the DCF byte-identical
// to Layer A (NF1).
func Resolve(in Input) Resolution {
	threshold := in.ConfidenceThreshold
	if threshold <= 0 {
		threshold = DefaultConfidenceThreshold
	}

	res := Resolution{
		Sources:        map[string]AssumptionSource{},
		GuidanceStatus: StatusAbsent,
	}

	loaded := in.Loaded
	art := loaded.Artifact

	// Absence / positive no-guidance / nil artifact ⇒ no anchors; fall through
	// to the profile for every near-term knob. The artifact (if any) is still
	// captured so the bundle records "we considered guidance and none applied".
	if loaded.Absent || art == nil {
		res.GuidanceStatus = StatusAbsent
		res.Captured = art
		return res
	}

	res.Captured = art

	if art.Status == guidance.StatusNoGuidanceFound {
		res.GuidanceStatus = StatusNoGuidanceFound
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("guidance: no_explicit_guidance_found for accession %s — using Layer A trajectory", art.Filing.Accession))
		return res
	}

	// §9.3: ONLY a validated artifact is eligible to supply a numeric override.
	// A needs_review / rejected artifact contributes qualitative context only
	// (never a number) — Phase 2 records the fall-through and produces no anchor.
	// LOW-3: surface the underlying artifact status verbatim rather than
	// flattening every non-validated status to "low_confidence" — a
	// high-confidence-but-rejected artifact is NOT low-confidence, and the
	// diagnostic tag must not say it is.
	if art.Status != guidance.StatusValidated {
		res.GuidanceStatus = string(art.Status)
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("guidance: artifact status=%s (not validated) for accession %s — context only, no numeric anchor",
				art.Status, art.Filing.Accession))
		return res
	}

	// §8.3 item 5: a stale artifact (its referenced period has lapsed relative
	// to as-of) is captured but NOT consumed for a numeric override.
	if loaded.Trace.Stale {
		res.GuidanceStatus = StatusStale
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("guidance: artifact accession %s is stale (referenced period lapsed) — captured, not anchored",
				art.Filing.Accession))
		return res
	}

	res.GuidanceStatus = StatusValidated

	if art.Extraction == nil {
		// Validated but no extraction ⇒ nothing to anchor.
		return res
	}

	// Apply each guidance kind to its near-term (year-1) knob, subject to the
	// §9.3 numeric-eligibility guardrails (confidence, explicit value, evidence)
	// AND the level-1 user-override precedence (a request that set the same knob
	// wins over guidance).
	applyCapEx(&res, in, art, threshold)
	applyMargin(&res, in, art, threshold)
	applyRevenue(&res, in, art, threshold)

	return res
}

// applyCapEx anchors capex_year1 from the (single) capex envelope when it
// clears the §9.3 guardrails. CapEx is recorded as an absolute-USD anchor; the
// reinvestment seam converts it into a year-1 reinvestment override.
func applyCapEx(res *Resolution, in Input, art *guidance.Artifact, threshold float64) {
	env := art.Extraction.CapExGuidance
	if env == nil {
		return
	}
	if in.UserOverriddenKnobs[KeyCapExYear1] {
		// Level 1 wins: the request explicitly set this knob; guidance defers.
		res.Sources[KeyCapExYear1] = AssumptionSource{Level: SourceUserOverride, Detail: "request override"}
		return
	}
	if !numericEligible(*env, threshold) {
		res.Warnings = append(res.Warnings, guardrailWarning(KeyCapExYear1, art, *env, threshold))
		return
	}
	m := env.Midpoint()
	anchorYear1(res, KeyCapExYear1, &res.Anchors.CapExYear1, m, art, *env)

	// MEDIUM-6: disclose the Phase-2 gross-as-net approximation on the CapEx
	// anchor's provenance. The engine substitutes this GROSS `absolute_usd` capex
	// figure DIRECTLY for the NET reinvestment term (no D&A-bearing gross→net
	// conversion until Phase 3). A consumer seeing `capex_year1 source=guidance`
	// would otherwise assume true CapEx semantics — so we append the marker to
	// the Sources.Detail (the structured diagnostic) AND the just-emitted anchor
	// warning (the operator-visible signal), in place, to avoid a second warning
	// line. This is capex-specific; margin/revenue anchors do not carry it.
	src := res.Sources[KeyCapExYear1]
	src.Detail += " " + grossAsNetMarker
	res.Sources[KeyCapExYear1] = src
	if n := len(res.Warnings); n > 0 {
		res.Warnings[n-1] += " [" + grossAsNetMarker + "]"
	}
}

// grossAsNetMarker is the provenance marker disclosing that a CapEx guidance
// anchor's gross `absolute_usd` figure is consumed AS the net reinvestment term
// in Phase 2 (MEDIUM-6). Replay tooling / dashboards key off this string; do NOT
// rename without coordinating with consumers.
const grossAsNetMarker = "gross_capex_as_net_reinvestment_approx=true"

// applyMargin anchors operating_margin_year1 from the first ELIGIBLE margin
// envelope (MEDIUM-3) — i.e. the first that clears the §9.3 guardrails, scanning
// in stored (deterministic) order. A leading low-confidence envelope must NOT
// suppress a later anchorable one; only if NO envelope is eligible does the
// resolver record a single fall-through warning.
func applyMargin(res *Resolution, in Input, art *guidance.Artifact, threshold float64) {
	if len(art.Extraction.MarginGuidance) == 0 {
		return
	}
	if in.UserOverriddenKnobs[KeyOperatingMarginYear1] {
		res.Sources[KeyOperatingMarginYear1] = AssumptionSource{Level: SourceUserOverride, Detail: "request override"}
		return
	}
	env, ok := firstEligible(art.Extraction.MarginGuidance, threshold)
	if !ok {
		// None eligible: one fall-through warning keyed to the first envelope
		// (the representative candidate) rather than the whole list.
		res.Warnings = append(res.Warnings,
			guardrailWarning(KeyOperatingMarginYear1, art, art.Extraction.MarginGuidance[0], threshold))
		return
	}
	anchorYear1(res, KeyOperatingMarginYear1, &res.Anchors.OperatingMarginYear1, env.Midpoint(), art, env)
}

// applyRevenue anchors revenue_growth_year1 from the first ELIGIBLE revenue
// envelope (MEDIUM-3 — same first-eligible semantics as applyMargin). Phase 2
// anchors the year-1 GROWTH RATE (staying inside the existing RevenueGrowthRates
// seam); revenue-LEVEL anchoring is a Phase-3 refinement (non-blocking open
// question).
func applyRevenue(res *Resolution, in Input, art *guidance.Artifact, threshold float64) {
	if len(art.Extraction.RevenueGuidance) == 0 {
		return
	}
	if in.UserOverriddenKnobs[KeyRevenueGrowthYear1] {
		res.Sources[KeyRevenueGrowthYear1] = AssumptionSource{Level: SourceUserOverride, Detail: "request override"}
		return
	}
	env, ok := firstEligible(art.Extraction.RevenueGuidance, threshold)
	if !ok {
		res.Warnings = append(res.Warnings,
			guardrailWarning(KeyRevenueGrowthYear1, art, art.Extraction.RevenueGuidance[0], threshold))
		return
	}
	anchorYear1(res, KeyRevenueGrowthYear1, &res.Anchors.RevenueGrowthYear1, env.Midpoint(), art, env)
}

// firstEligible returns the first envelope (in stored order, deterministic) that
// clears the §9.3 numeric guardrails, or ok=false if none do (MEDIUM-3).
func firstEligible(envs []guidance.Envelope, threshold float64) (guidance.Envelope, bool) {
	for _, env := range envs {
		if numericEligible(env, threshold) {
			return env, true
		}
	}
	return guidance.Envelope{}, false
}

// anchorYear1 writes the midpoint anchor into the year-1 slot (index 0), records
// the source, appends the human-readable tag, and notes the knob in
// AnchorsApplied. It is the ONLY place a year-1 anchor is set. Year-1 is index
// 0, which is always within the §9.3 near-term prefix (<= MaxAnchorYearIndex);
// the HARD refusal of any out-of-prefix index lives in RefuseAnchorIndex, shared
// with the reinvestment seam's post-anchor assertion and exercised by tests.
func anchorYear1(res *Resolution, key string, slot **float64, midpoint float64, art *guidance.Artifact, env guidance.Envelope) {
	v := midpoint
	*slot = &v
	res.Sources[key] = AssumptionSource{
		Level:  SourceGuidance,
		Value:  midpoint,
		Detail: anchorDetail(art, env, midpoint),
	}
	res.AnchorsApplied = append(res.AnchorsApplied, key)
	res.Warnings = append(res.Warnings, anchorWarning(key, art, env, midpoint))
}

// RefuseAnchorIndex reports whether an anchor at the given year index must be
// HARD-REFUSED per the §9.3 dominance guardrail. Any index beyond
// MaxAnchorYearIndex (year 3+, i.e. index >= 2) is refused so guidance can
// never steer past the near-term prefix. Exposed so the reinvestment seam's
// post-anchor assertion and tests can share the single definition.
func RefuseAnchorIndex(yearIndex int) bool {
	return yearIndex > MaxAnchorYearIndex
}

// numericEligible enforces the §9.3 numeric-override guardrails on a single
// envelope: confidence >= threshold AND an explicit value (low/high finite,
// which the struct guarantees) AND >=1 accepted evidence quote. The
// structural validator already guarantees evidence-for-numeric, but the
// resolver re-checks defensively so a future bypass of the loader still cannot
// launder a numeric anchor through a value with no evidence.
func numericEligible(env guidance.Envelope, threshold float64) bool {
	if env.Confidence < threshold {
		return false
	}
	if len(env.Evidence) == 0 {
		return false
	}
	for _, ev := range env.Evidence {
		if ev.Quote == "" {
			return false
		}
	}
	return true
}

// anchorDetail renders the per-assumption Sources detail string (Decision 4).
func anchorDetail(art *guidance.Artifact, env guidance.Envelope, midpoint float64) string {
	return fmt.Sprintf("accession=%s period=%s conf=%.2f midpoint=%s",
		art.Filing.Accession, env.Period, env.Confidence, formatValue(env.Unit, midpoint))
}

// anchorWarning renders the human-readable source tag appended to
// result.Warnings (mirrors the RM-1 / Layer-A convention, Decision 4).
func anchorWarning(key string, art *guidance.Artifact, env guidance.Envelope, midpoint float64) string {
	return fmt.Sprintf("guidance: %s anchored from accession %s (%s, conf=%.2f, midpoint %s)",
		key, art.Filing.Accession, env.Period, env.Confidence, formatValue(env.Unit, midpoint))
}

// guardrailWarning renders the fall-through tag when an envelope fails the
// §9.3 numeric guardrails (low confidence / missing evidence).
func guardrailWarning(key string, art *guidance.Artifact, env guidance.Envelope, threshold float64) string {
	return fmt.Sprintf("guidance: %s NOT anchored — accession %s envelope conf=%.2f < threshold %.2f or insufficient evidence; using Layer A",
		key, art.Filing.Accession, env.Confidence, threshold)
}

// formatValue renders a value with a unit-aware suffix for the detail/warning
// strings (USD billions for absolute, percent for pct).
func formatValue(unit guidance.Unit, v float64) string {
	switch unit {
	case guidance.UnitPct:
		return fmt.Sprintf("%.1f%%", v*100)
	case guidance.UnitAbsoluteUSD:
		return fmt.Sprintf("$%.2fB", v/1e9)
	default:
		return fmt.Sprintf("%g", v)
	}
}
