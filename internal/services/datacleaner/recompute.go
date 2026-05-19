package datacleaner

import (
	"context"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// divergenceTolerance is the absolute USD threshold above which the recomputed
// umbrella is considered to diverge from the cleaner's reported value. A WARN
// log fires only when |recomputed - reported| > divergenceTolerance.
//
// Why 1.0 USD (absolute, NOT relative): every cleaner-side mutation today
// subtracts at least dollars from a balance-sheet umbrella (the smallest A2
// intangible writedown still moves TotalAssets by thousands), so a $1 absolute
// tolerance never false-triggers on float64 accumulation noise while staying
// tight enough to surface every real adjuster mutation. A relative tolerance
// would mask exactly the divergences Phase 2 needs to fix — A1 goodwill
// exclusion drives 45% of MXL's TotalAssets delta, and any relative tolerance
// large enough to absorb float64 noise across IFRS-full-filer magnitudes would
// also swallow the goodwill-exclusion signal.
//
// See implementation plan §C for the full rationale:
//
//	docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md
const divergenceTolerance = 1.0

// recomputeUmbrellas (shadow-mode, DC-1 Phase 1) reads fd and, for each of the
// four balance-sheet umbrellas (CurrentAssets, TotalAssets, CurrentLiabilities,
// TotalLiabilities), recomputes
//
//	umbrella = sum(known_components) + plug
//
// and emits a single structured WARN log line per umbrella whenever the
// recomputed value diverges from the cleaner's mutated value beyond
// divergenceTolerance.
//
// MUST NOT mutate fd. Phase 1 of DC-1 ships this as observability only — the
// recomputed value is computed, logged, and discarded. The cleaner's existing
// mutated umbrella remains the canonical value for every downstream consumer.
//
// The function uses logctx.From(ctx) to obtain the request-scoped logger so
// each WARN line inherits request_id (and user_id / key_id post-auth). The
// nil-context path is intentionally safe — logctx.From(nil) returns
// zap.NewNop() so unit tests don't need to thread a real context.
//
// Spec: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
// Plan: docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-implementation-plan.md
//
// The recompute formulas mirror computePlugs (internal/infra/gateways/sec/plugs.go)
// byte-for-byte. In well-formed Phase 0 state (no cleaner mutation), the
// recomputed value equals the reported value exactly. Any cleaner-side
// mutation that breaks umbrella == sum(components) + plug produces a
// divergence — which is precisely the Phase 2 punch-list signal.
func recomputeUmbrellas(ctx context.Context, fd *entities.FinancialData) {
	if fd == nil {
		return
	}
	logger := logctx.From(ctx)

	// --- CurrentAssets = Cash + Inventory + OtherCurrentAssets ---
	recomputedCA := fd.CashAndCashEquivalents + fd.Inventory + fd.OtherCurrentAssets
	emitIfDiverged(logger, fd, "CurrentAssets", fd.CurrentAssets, recomputedCA, fd.OtherCurrentAssets)

	// --- TotalAssets = NonCurrentAssets_recomputed + CurrentAssets ---
	// NonCurrentAssets components: Goodwill + OtherIntangibles + DeferredTaxAssets + OtherNonCurrentAssets.
	// computePlugs clamped the umbrella to >= 0 before computing the residual,
	// but the recompute side here just sums components + plug + CurrentAssets;
	// the divergence signal is what we want to surface.
	nonCurrentAssets := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets + fd.OtherNonCurrentAssets
	recomputedTA := nonCurrentAssets + fd.CurrentAssets
	emitIfDiverged(logger, fd, "TotalAssets", fd.TotalAssets, recomputedTA, fd.OtherNonCurrentAssets)

	// --- CurrentLiabilities = OperatingLeaseLiabilityCurrent + OtherCurrentLiabilities ---
	// In production today the lease-current field is always zero (parser only
	// fills the umbrella OperatingLeaseLiability) so the plug absorbs the
	// entire current-liabilities umbrella. The invariant still holds.
	recomputedCL := fd.OperatingLeaseLiabilityCurrent + fd.OtherCurrentLiabilities
	emitIfDiverged(logger, fd, "CurrentLiabilities", fd.CurrentLiabilities, recomputedCL, fd.OtherCurrentLiabilities)

	// --- TotalLiabilities = NonCurrentLiabilities_recomputed + CurrentLiabilities ---
	// NonCurrentLiabilities components: TotalDebt + OperatingLeaseLiabilityNoncurrent + OtherNonCurrentLiabilities.
	// Today's cleaner mutates fd.TotalDebt via liabilities.go:87-88 (B1/B2/B3 add
	// to TotalDebt) but does NOT touch fd.TotalLiabilities. A B1 lease
	// capitalization of $254M therefore produces recomputedTL = reportedTL + 254M,
	// surfacing as a divergence that Phase 2 resolves by routing B1 through an
	// Overlay rather than into TotalDebt directly.
	nonCurrentLiab := fd.TotalDebt + fd.OperatingLeaseLiabilityNoncurrent + fd.OtherNonCurrentLiabilities
	recomputedTL := nonCurrentLiab + fd.CurrentLiabilities
	emitIfDiverged(logger, fd, "TotalLiabilities", fd.TotalLiabilities, recomputedTL, fd.OtherNonCurrentLiabilities)
}

// emitIfDiverged fires a single WARN log line when |recomputed - reported| > divergenceTolerance.
//
// The clamp_suspected field is set true when recomputed > reported AND the
// plug is exactly zero — the Phase 0 clamp fingerprint (sum(components) >
// umbrella → plug clamped to 0 by clampPlug). Phase 2's shadow-analysis
// tooling filters on this field to separate known Phase 0 clamp-fired periods
// (MXL 2017FY, EQIX 2013Q1) from the genuine cleaner-mutation punch list.
//
// All structured field names are part of the contract Phase 2's analysis
// tooling depends on (see implementation plan §D). Do NOT rename without
// updating the analysis report side.
func emitIfDiverged(logger *zap.Logger, fd *entities.FinancialData, umbrella string, reported, recomputed, plug float64) {
	delta := recomputed - reported

	// Branchless absolute-value compare against the tolerance. We avoid math.Abs
	// to keep this leaf function math-package-free; the tolerance is tiny and
	// the comparison is in the hot path.
	absDelta := delta
	if absDelta < 0 {
		absDelta = -absDelta
	}
	if absDelta <= divergenceTolerance {
		return
	}

	// clamp_suspected: the Phase 0 clamp zero'd the plug because
	// sum(components) > umbrella. The recompute will then produce
	// recomputed > reported because the components sum exceeds the reported
	// umbrella WITHOUT the plug absorbing the excess.
	clampSuspected := recomputed > reported && plug == 0

	logger.Warn("recomputeUmbrellas: umbrella divergence",
		zap.String("ticker", fd.Ticker),
		zap.String("period", fd.FilingPeriod),
		zap.String("cik", fd.CIK),
		zap.String("umbrella", umbrella),
		zap.Float64("reported", reported),
		zap.Float64("recomputed", recomputed),
		zap.Float64("delta", delta),
		zap.Float64("plug", plug),
		zap.Bool("clamp_suspected", clampSuspected),
		zap.String("phase", "DC-1-P1-shadow"),
	)
}
