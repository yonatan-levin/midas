package valuation

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/authority"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// resolveGuidance is the Layer-B Phase-2 consumption seam (spec §"resolveGuidance"):
// the thin service-level orchestrator that runs the guidance loader, applies the
// §9 assumption-authority resolver, captures the resolution into the replay
// bundle (09-guidance.json), and returns the resolution for the DCF anchor seam.
//
// It runs ONCE per valuation, AFTER profile resolution and BEFORE DCF-input
// construction. On the production default (empty GuidanceRoot) the loader
// returns Absent immediately, the resolver produces NO anchors, and the only
// observable effect is the captured "absent" bundle stage — the DCF math is
// byte-identical to the Layer-A 4.7 engine (NF1).
//
// Failure isolation (NF4): a malformed/absent fixture degrades to the absent
// path with a Warnings entry inside the resolver; the ONLY hard error is a
// content-hash MISMATCH on a present artifact (F2), which propagates so a
// tampered fixture is never silently consumed.
func (s *Service) resolveGuidance(
	ctx context.Context,
	cik string,
	asOf time.Time,
	resolvedProfile *profile.ResolvedProfile,
	userOverriddenKnobs map[string]bool,
) (authority.Resolution, error) {
	// guidanceSource is never nil after NewService, but guard for any
	// hand-constructed Service in tests so this never panics.
	if s.guidanceSource == nil {
		return authority.Resolution{GuidanceStatus: authority.StatusAbsent}, nil
	}

	loaded, err := s.guidanceSource.Load(cik, asOf)
	if err != nil {
		// The loader's ONE hard error (content-hash mismatch on a present
		// artifact) propagates — a tampered fixture must fail the valuation,
		// not silently fall through (F2). Production never reaches this branch
		// (empty root ⇒ no files ⇒ no hash to verify).
		s.log(ctx).Error("guidance loader hard error; failing valuation rather than silently consuming",
			zap.String("cik", cik), zap.Error(err))
		return authority.Resolution{}, err
	}

	res := authority.Resolve(authority.Input{
		Loaded:              loaded,
		Profile:             resolvedProfile,
		UserOverriddenKnobs: userOverriddenKnobs,
	})

	// Capture the resolution into the replay bundle (Decision 8). On a hit the
	// selected artifact travels verbatim; on absent / no-guidance the envelope
	// alone is captured so "no guidance applied" is a replayable fact (NF3).
	// Nil-safe via the bundle's own nil-receiver guard.
	stage := guidance.NewBundleStage(loaded, res.GuidanceStatus, res.AnchorsApplied)
	artifact.From(ctx).SetGuidanceResolution(ctx, stage)

	if res.GuidanceStatus != authority.StatusAbsent || len(res.AnchorsApplied) > 0 {
		s.log(ctx).Debug("guidance resolved",
			zap.String("cik", cik),
			zap.String("guidance_status", res.GuidanceStatus),
			zap.Strings("anchors_applied", res.AnchorsApplied),
		)
	}

	return res, nil
}

// stampAssumptionSources projects authority.Resolution.Sources onto the
// response result (Decision 4). Called from BOTH the DCF path and the alt-model
// path. It is a strict no-op when no non-default source fired (the map stays
// nil), so default-path responses keep the assumption_sources key omitted (NF1).
func stampAssumptionSources(result *entities.ValuationResult, sources map[string]authority.AssumptionSource) {
	if len(sources) == 0 {
		return
	}
	if result.AssumptionSources == nil {
		result.AssumptionSources = make(map[string]entities.AssumptionSourceValue, len(sources))
	}
	for key, src := range sources {
		result.AssumptionSources[key] = entities.AssumptionSourceValue{
			Source: string(src.Level),
			Detail: src.Detail,
		}
	}
}
