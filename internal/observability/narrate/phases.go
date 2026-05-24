// Package narrate provides the Tier-1 observability stream: one Info-level
// log line per pipeline phase that, when filtered by event=narrate, reads
// top-to-bottom as the story of one request.
//
// See docs/refactoring/spec/observability-narrative-and-artifacts-spec.md (§5)
// for the closed-enum phase taxonomy and §4 for the standard-fields contract.
package narrate

// Phase is the closed-enum identifier for one of the pipeline phases the
// narrate stream describes. Adding a new phase here is a deliberate API change:
// downstream log consumers (dashboards, grep playbooks) treat the set as
// versioned. Update the spec, the consumers, and the closed-set test in
// phases_test.go in lockstep.
type Phase string

// The phases that compose a complete fair-value request narrative.
// Order in this block matches the natural execution order of a successful
// request and the file-prefix numbering used inside an artifact bundle.
const (
	// PhaseRequestReceived fires from the trace middleware right after the
	// request_id has been assigned/echoed and before any handler logic.
	PhaseRequestReceived Phase = "request.received"

	// PhaseAuthResolved fires from the auth middleware after the API-key
	// validation succeeded (or failed); carries key_id + permission count.
	PhaseAuthResolved Phase = "auth.resolved"

	// PhaseRateLimitChecked fires from the rate-limit middleware after the
	// allow/deny decision; carries bucket + remaining/limit counters.
	PhaseRateLimitChecked Phase = "ratelimit.checked"

	// PhaseHandlerEntry fires from the fair-value handler immediately after
	// request parsing; carries any ValuationOptions overrides applied.
	PhaseHandlerEntry Phase = "handler.entry"

	// PhaseCacheLookup fires before the data-fetch step; outcome=ok on hit,
	// skipped on miss.
	PhaseCacheLookup Phase = "cache.lookup"

	// PhaseFetchFanout summarises the multi-source coordinator run; emits
	// once after all per-source emissions, never per source.
	PhaseFetchFanout Phase = "fetch.fanout"

	// PhaseFetchSEC fires from the datafetcher coordinator after the SEC
	// gateway returned (success, fallback, or error).
	PhaseFetchSEC Phase = "fetch.sec"

	// PhaseFetchMarket fires after the market gateway returned. The
	// "provider" field distinguishes Yahoo vs Finzive.
	PhaseFetchMarket Phase = "fetch.market"

	// PhaseFetchMacro fires after the macro gateway returned. The
	// "provider" field distinguishes FRED vs manual fallback.
	PhaseFetchMacro Phase = "fetch.macro"

	// PhaseCleanNormalized fires after the data-cleaner pipeline completes;
	// summarises rules_applied, adjustments_made, flags_raised.
	PhaseCleanNormalized Phase = "clean.normalized"

	// PhaseClassifyIndustry fires after both the SIC-based and heuristic
	// classifiers ran; carries both labels and the match flag.
	PhaseClassifyIndustry Phase = "classify.industry"

	// PhaseGrowthEstimated fires after the growth estimator returns the
	// multi-stage growth curve.
	PhaseGrowthEstimated Phase = "growth.estimated"

	// PhaseWACCComputed fires after WACC is computed; carries cost-of-equity,
	// cost-of-debt, weights, and final WACC.
	PhaseWACCComputed Phase = "wacc.computed"

	// PhaseModelSelected fires from the valuation router after it picks an
	// industry-specific model (DCF, DDM, FFO, Revenue Multiple).
	PhaseModelSelected Phase = "model.selected"

	// PhaseValuationComputed fires after the valuation engine returns the
	// fair-value-per-share number (or an error).
	PhaseValuationComputed Phase = "valuation.computed"

	// PhaseFXConvert is emitted by the valuation service after FX-converting
	// reporting-currency financials to USD (Phase B9 of IFRS-FPI plan,
	// docs/refactoring/archive/ifrs-foreign-private-issuer-support-spec.md).
	// Outcome=ok with currencies_converted + periods_converted on success;
	// outcome=error with currencies_failed when one or more FX pairs are
	// unresolved by FRED + static config.
	PhaseFXConvert Phase = "fx.convert"

	// PhaseADRRatioApplied is emitted when the valuation service has divided
	// ordinary-share counts by the depositary's ADR ratio so per-share output
	// matches the listed ADR price (Phase B10 of IFRS-FPI plan). Skipped for
	// domestic filers (ratio=1, no-op).
	PhaseADRRatioApplied Phase = "adr_ratio.applied"

	// PhaseCrosscheckEvaluated fires after the implied-multiples sanity check.
	PhaseCrosscheckEvaluated Phase = "crosscheck.evaluated"

	// PhaseResponseSent fires from the trace middleware on response (deferred
	// emission); carries final status, body bytes, total_elapsed_ms.
	PhaseResponseSent Phase = "response.sent"
)

// Outcome is the closed-enum status of a phase. A phase outcome is independent
// of the request outcome — for example, a request can succeed (HTTP 200) with
// outcome=fallback on fetch.market.
type Outcome string

const (
	// OutcomeOK — phase did its job using the primary path.
	OutcomeOK Outcome = "ok"

	// OutcomeFallback — primary path failed, secondary path succeeded; result
	// is usable. Example: Yahoo cookie expired, switched to Finzive.
	OutcomeFallback Outcome = "fallback"

	// OutcomePartial — phase produced a result but had to fill gaps. Example:
	// FY2019 missing in SEC filings, extrapolated linearly.
	OutcomePartial Outcome = "partial"

	// OutcomeSkipped — phase was a no-op by design (cache miss, ratelimit
	// bypass, etc).
	OutcomeSkipped Outcome = "skipped"

	// OutcomeError — phase failed; downstream phases may emit fallback/partial
	// to recover. Note: outcome=error does NOT mean the request failed —
	// only response.sent with status>=500 means that.
	OutcomeError Outcome = "error"
)

// allPhases returns the immutable closed set of phase identifiers. Used by
// the closed-set test in narrate_test.go to detect accidental string drift
// (someone renaming a constant value but forgetting to update the consumer).
//
// New phases MUST be appended here AND added as a constant above. The
// closed-set test pins the count at 19 phases (was 17 pre-Phase-B; B9 added
// PhaseFXConvert and B10 added PhaseADRRatioApplied) — bump the test
// assertion when adding a 20th.
func allPhases() []Phase {
	return []Phase{
		PhaseRequestReceived,
		PhaseAuthResolved,
		PhaseRateLimitChecked,
		PhaseHandlerEntry,
		PhaseCacheLookup,
		PhaseFetchFanout,
		PhaseFetchSEC,
		PhaseFetchMarket,
		PhaseFetchMacro,
		PhaseCleanNormalized,
		PhaseClassifyIndustry,
		PhaseGrowthEstimated,
		PhaseWACCComputed,
		PhaseModelSelected,
		PhaseValuationComputed,
		PhaseFXConvert,
		PhaseADRRatioApplied,
		PhaseCrosscheckEvaluated,
		PhaseResponseSent,
	}
}

// allOutcomes returns the immutable closed set of outcome identifiers.
func allOutcomes() []Outcome {
	return []Outcome{
		OutcomeOK,
		OutcomeFallback,
		OutcomePartial,
		OutcomeSkipped,
		OutcomeError,
	}
}
