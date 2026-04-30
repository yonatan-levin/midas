// Package middleware holds Gin middleware that doesn't belong to the
// monolithic server.go file. The trace middleware is the entry point for
// the Tier-1 narrate stream and the Tier-3 artifact bundle: it decides per
// request whether tracing is on, opens the bundle if so, attaches the
// narrate emitter + bundle to ctx, and finalises both on response.
//
// See docs/refactoring/observability-narrative-and-artifacts-spec.md (§8).
package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/observability/narrate"
)

// traceHeaderName is the canonical opt-in header. Hard-coded — adding to
// config would just expose footguns.
const traceHeaderName = "X-Midas-Trace"

// traceQueryParam is the canonical opt-in query string.
const traceQueryParam = "trace"

// TraceMiddleware constructs the trace middleware. It must be registered
// AFTER requestIDMiddleware because the bundle directory uses request_id
// as its name. (The spec §10 line "wire trace middleware before requestID"
// refers to wiring it before downstream handler code, not before requestID.)
//
// Behaviour:
//   - Always constructs a narrate.Emitter from cfgN and attaches it to ctx.
//     Sampling decision is fixed at construction so half-told stories are
//     impossible.
//   - When the trace flag is set AND cfgA.Enabled, opens an artifact.Bundle
//     and attaches it to ctx.
//   - When the trace flag is set but cfgA.Enabled is false, the bundle is
//     not opened. The narrate stream still emits trace_enabled=false with
//     reason=disabled (added in commit 3).
//   - On response: the bundle is Close()'d, finalising the manifest.
//
// Phase 1 commit 1 leaves the actual narrate.Emit calls (request.received,
// response.sent) to commit 3. This commit just establishes the wiring so
// subsequent commits can fill in emissions.
func TraceMiddleware(cfgN narrate.Config, cfgA artifact.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// requestID is set by the canonical requestIDMiddleware which runs
		// BEFORE this middleware in the chain. We read it from the gin
		// context (the singleton key set in server.go).
		rid, _ := c.Get("request_id")
		requestID, _ := rid.(string)

		// Construct the narrate emitter unconditionally — it's near-free when
		// disabled (a single struct alloc) and lets the entire request-path
		// code call narrate.From(ctx).Emit without nil checks.
		emitter := narrate.NewEmitter(cfgN, requestID)

		// Decide whether to open a bundle.
		//
		// Precedence (spec §13 + Phase 2.A/2.B briefs):
		//   1. Manual flag (?trace=1 / X-Midas-Trace) — open eager bundle
		//      with trigger=header/query. Always wins so debug sessions
		//      stay attributable to the operator who flipped the flag.
		//   2. Any auto-trigger configured (on_error OR on_quality_flag) —
		//      open ONE deferred bundle. The bundle is in-memory only;
		//      trace middleware decides at request-end (post-c.Next) which
		//      auto-trigger to Promote with based on bundle state, or
		//      Close()-without-flush if no auto-trigger fires.
		//   3. Otherwise — no bundle.
		//
		// Phase 2.B note: a single deferred bundle covers BOTH auto-
		// triggers — we never open two bundles for the same request. The
		// per-trigger decision happens at Promote-time in the post-c.Next()
		// defer below.
		trigger, traceFlag := detectTraceTrigger(c)
		// autoTriggerActive is true when ANY auto-trigger is configured
		// (on_error, on_quality_flag, and/or always). Used to decide
		// whether to open a deferred bundle even when no manual flag is
		// present. Phase 2.C adds the `Always` clause: when the operator
		// has flipped the always-on knob, every request must open a
		// deferred bundle so it can be promoted at request-end as the
		// catch-all fallback in the precedence ladder.
		autoTriggerActive := cfgA.Triggers.OnError ||
			cfgA.Triggers.QualityFlagThreshold != "" ||
			cfgA.Triggers.Always
		var bundle *artifact.Bundle
		// deferredBundle tracks whether we opened in deferred mode. Used
		// post-c.Next to decide between Promote() and Close()-without-flush.
		// Manual triggers bypass this flag (they always promote == eager).
		var deferredBundle bool
		// openErr is captured so we can both surface the failure to log
		// readers (via Warn) and downgrade trace_enabled with an
		// explanatory reason on the narrate stream. Silent swallow makes
		// "?trace=1 returns no bundle" un-debuggable.
		var openErr error
		switch {
		case traceFlag && cfgA.Enabled:
			// Ticker is unknown at this point — handler will SetTicker
			// after URL parsing.
			b, err := artifact.OpenBundle(cfgA, requestID, "", trigger)
			if err != nil {
				// Disk-full / permission / malformed config — emit a Warn so
				// operators can find the cause in logs. We use the request
				// context's logger via logctx so the line carries request_id.
				openErr = err
				logctx.From(c.Request.Context()).Warn("trace.bundle.open_failed",
					zap.String("request_id", requestID),
					zap.String("trigger", string(trigger)),
					zap.Error(err),
				)
			} else {
				bundle = b
				emitter.WithPayloadRoot(b.Root())
			}
		case cfgA.Enabled && autoTriggerActive:
			// No manual flag, but at least one auto-trigger is on — open a
			// deferred bundle. The speculative trigger value stamped on the
			// manifest is TriggerOnError; Promote() at request-end will
			// overwrite it with whichever auto-trigger actually fired
			// (on_quality_flag if quality count > 0, otherwise on_error if
			// status >= 500). If neither fires, Close() drops the buffers
			// and nothing lands on disk.
			//
			// OpenDeferredBundle does NOT mkdir or spawn a goroutine, so
			// non-firing requests pay only buffer-allocation cost.
			b, err := artifact.OpenDeferredBundle(cfgA, requestID, "", artifact.TriggerOnError)
			if err != nil {
				// Same Warn shape as the eager path so log readers don't
				// have to learn two log lines for the same class of failure.
				openErr = err
				logctx.From(c.Request.Context()).Warn("trace.bundle.open_failed",
					zap.String("request_id", requestID),
					zap.String("trigger", string(artifact.TriggerOnError)),
					zap.Error(err),
				)
			} else {
				bundle = b
				deferredBundle = true
				// Intentionally NOT calling WithPayloadRoot here: the bundle
				// directory does not exist on disk yet and may never exist.
				// payload_ref fields would point to a non-existent path,
				// which is more confusing than absent. WithPayloadRoot is
				// invoked at Promote()-time below if/when we promote.
			}
		}

		// Attach both to the request context so downstream middleware/handlers
		// can pull them via narrate.From / artifact.From.
		ctx := narrate.Inject(c.Request.Context(), emitter)
		if bundle != nil {
			ctx = artifact.Inject(ctx, bundle)

			// Tee narrate + Debug zap entries into the bundle's JSONL streams
			// (spec §7.1 + §7.3). We wrap the request-scoped logger (which
			// already carries request_id baked into its core's With-state)
			// — wrapping the singleton would lose that correlation in the
			// host log stream. The wrapper is transparent for non-narrate
			// Info+ entries so existing log output is unaffected.
			//
			// request_id is passed as a baseline field directly to
			// NewBundleSink (NOT via post-wrap .With(...)). zap's Core.Write
			// only receives call-site fields, so the sink would otherwise be
			// blind to the request_id already baked into the wrapped core's
			// internal state. Going through .With() to re-attach it would
			// also re-apply request_id to the wrapped core, producing a
			// duplicate "request_id" field on every host log line (zap's
			// JSON encoder does NOT dedupe duplicate keys; see REVIEWER
			// finding 2026-04-25). Passing baseFields to the sink injects
			// the correlation field into the sink's encoder context only.
			base := logctx.From(c.Request.Context())
			wrapped := base.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
				return artifact.NewBundleSink(c, bundle, zap.String("request_id", requestID))
			}))
			ctx = logctx.Inject(ctx, wrapped)
		}
		c.Request = c.Request.WithContext(ctx)

		// Compute trace_enabled — the bundle must have actually opened, not
		// just been requested. open_failed flips trace_enabled to false even
		// though traceFlag && cfgA.Enabled were both true.
		traceEnabled := traceFlag && cfgA.Enabled && openErr == nil

		// Stash the trigger flag on the gin context for downstream consumers.
		c.Set("trace_flag", traceFlag)
		c.Set("trace_trigger", string(trigger))
		c.Set("trace_enabled", traceEnabled)
		traceReason := ""
		switch {
		case traceFlag && !cfgA.Enabled:
			traceReason = "disabled"
		case traceFlag && cfgA.Enabled && openErr != nil:
			// Bundle creation failed — narrate readers see the reason
			// without having to cross-reference the Warn line.
			traceReason = "open_failed"
		}
		if traceReason != "" {
			c.Set("trace_reason", traceReason)
		}

		// Tier-1 narrate: request.received. First line of the per-request
		// story; carries the method+path+client_ip_hash so log readers know
		// what request they are inspecting and whether bundling is on.
		reqStart := time.Now()
		notes := ""
		if traceReason != "" {
			notes = "trace_reason=" + traceReason
		}
		emitter.Emit(c.Request.Context(), narrate.PhaseRequestReceived, narrate.OutcomeOK, notes,
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("client_ip_hash", hashIP(c.ClientIP())),
			zap.Bool("trace_enabled", traceEnabled),
		)

		// Wrap the post-c.Next() block in a defer so the bundle is finalised
		// even when the handler chain panics (REVIEWER HIGH-4). Pre-fix:
		// if a panic propagated up through c.Next() — possible whenever a
		// recovery middleware is registered OUTSIDE trace, or when no
		// recovery middleware is registered at all — the bundle was never
		// promoted and never closed, leaking the deferred-mode buffers and
		// (worse) skipping the auto-on-error capture for the very requests
		// that most need post-mortem visibility.
		//
		// We `recover()` at the top of the defer so we can:
		//   - Force respOutcome=error on panic even when c.Writer.Status()
		//     hasn't been set to 500 yet (e.g. when no outer recovery exists).
		//   - Re-panic at the END of the defer so any outer recovery
		//     middleware still catches it with the original value. The stack
		//     trace gets one extra frame at the re-panic site, which is an
		//     acceptable cost for guaranteed bundle finalisation.
		defer func() {
			rec := recover()
			panicked := rec != nil

			// Tier-1 narrate: response.sent. Final line of the per-request
			// story. Outcome=error on panic OR when the response status is
			// >=500 per spec §4. We treat panic as error even before any
			// recovery middleware translates it into a 500 because the
			// auto-on-error trigger should fire on un-recovered panics too.
			respOutcome := narrate.OutcomeOK
			if panicked || c.Writer.Status() >= 500 {
				respOutcome = narrate.OutcomeError
			}

			// Phase 2.A/2.B: deferred-bundle promote/discard decision.
			//
			// MUST run BEFORE we emit response.sent so that:
			//   - Promoted bundles include the response.sent narrate line in
			//     their 99-narrate.jsonl (the buffered request.received line +
			//     the freshly-emitted response.sent both end up on disk).
			//   - Promoting now lets us include artifact_path in response.sent
			//     pointing at the now-on-disk bundle directory.
			//
			// Manual triggers always opened in eager mode (deferredBundle=false)
			// and reach this block as no-op. Only auto-triggered (deferred)
			// bundles take the Promote/Close branch.
			//
			// Precedence at promote-time (highest to lowest):
			//   1. on_quality_flag — at least one cleaner flag at-or-above the
			//      configured severity threshold. Wins because the flag list
			//      points at WHY the upstream data was suspicious.
			//   2. on_error — response status >= 500 (or panic). Last-resort
			//      capture for failures the cleaner didn't catch.
			//   3. always (Phase 2.C) — operator flipped the always-on knob
			//      for a debugging session. Lowest precedence: only fires when
			//      no other trigger does, so operators can tell at a glance
			//      from the manifest's trigger field which bundles are "noise"
			//      (always) vs "interesting" (on_error / on_quality_flag).
			//      When always is the only configured trigger, EVERY request
			//      lands here and gets bundled.
			//
			// We evaluate all three ONCE, choose the highest-precedence
			// trigger, and call Promote EXACTLY once. The bundle's Promote is
			// idempotent so a bug here would be self-healing for correctness
			// but would still muddy the manifest's trigger field.
			//
			// promoteSucceeded gates the artifact_path field on response.sent
			// below. It stays false for the dissolve path (no auto-trigger
			// fired), for the Promote-failure path (mkdir error), and for
			// non-deferred bundles (where the field is N/A — eager bundles
			// take the `!deferredBundle` branch of the gate). It only flips
			// true when Promote returns nil, signalling the bundle directory
			// is on disk.
			promoteSucceeded := false
			if deferredBundle && bundle != nil {
				// Decide which auto-trigger (if any) fired per the precedence
				// ladder above. Switch order = precedence order (highest first):
				// on_quality_flag > on_error > always. Adding always as the
				// LOWEST-precedence case is the entire promote-time delta of
				// Phase 2.C — every other line in this block is unchanged.
				var autoTrigger artifact.Trigger
				switch {
				case cfgA.Triggers.QualityFlagThreshold != "" && bundle.QualityFlagCount() > 0:
					autoTrigger = artifact.TriggerOnQualityFlag
				case cfgA.Triggers.OnError && respOutcome == narrate.OutcomeError:
					autoTrigger = artifact.TriggerOnError
				case cfgA.Triggers.Always:
					// Catch-all: no other trigger fired but the operator
					// asked for every-request capture. Stamps trigger=always
					// on the manifest so postmortem readers can tell the
					// bundle from a "this 5xx'd" or "this had bad data"
					// capture at a glance.
					autoTrigger = artifact.TriggerAlways
				}

				if autoTrigger != "" {
					if perr := bundle.Promote(autoTrigger); perr != nil {
						// Promote failed (mkdir error). Same Warn shape as the
						// open-time failure so log readers don't have to learn
						// two log lines. promoteSucceeded stays false so the
						// response.sent line below omits artifact_path — it
						// would otherwise point at a directory that does not
						// exist on disk, misleading log readers (QA finding,
						// MINOR-NEW 2026-04-26).
						logctx.From(c.Request.Context()).Warn("trace.bundle.promote_failed",
							zap.String("request_id", requestID),
							zap.String("trigger", string(autoTrigger)),
							zap.Error(perr),
						)
					} else {
						promoteSucceeded = true
						// Wire payload_root NOW so response.sent (and any
						// downstream narrate lines if there are any) carry
						// resolvable payload_ref values. Pre-promote narrate
						// lines were buffered without payload_root; that's OK
						// because their payload_ref fields would have pointed
						// at a nonexistent path anyway.
						emitter.WithPayloadRoot(bundle.Root())

						// REVIEWER MEDIUM-2: emit a host-log Info line so
						// operators tailing the host log stream can see WHICH
						// requests created bundles today and WHY, without
						// having to walk the artifacts directory or grep
						// 99-narrate.jsonl files inside each bundle. Symmetrical
						// shape with trace.bundle.promote_failed (Warn) above
						// — operators only need to learn one field set.
						//
						// Goes through logctx so it inherits request_id +
						// any auth fields baked into the request-scoped
						// logger (per CLAUDE.md "request-path logs via
						// logctx.From(ctx)" rule).
						//
						// REVIEWER HIGH-F (Phase 2.C follow-up): suppress this
						// line for the always path. The line's contract is
						// "operators tailing the host log can see WHICH
						// requests created bundles and WHY" — but with
						// always=true EVERY request creates a bundle for the
						// SAME trigger, so the line stops carrying signal and
						// becomes pure noise (6,000 Info/min at 100 rps).
						// Worse, the contract inverts: every request created
						// a bundle, so the line tells operators nothing they
						// don't already know from flipping the knob. The
						// other auto-triggers (on_error, on_quality_flag) stay
						// rare-by-construction and keep emitting the line.
						if autoTrigger != artifact.TriggerAlways {
							logctx.From(c.Request.Context()).Info("trace.bundle.promoted",
								zap.String("request_id", requestID),
								zap.String("trigger", string(autoTrigger)),
								zap.String("artifact_path", bundle.Root()),
							)
						}
					}
				}
				// If autoTrigger stayed empty we DON'T call Promote: the
				// deferred bundle is dropped via Close() below, releasing
				// all in-memory state.
			}

			respFields := []zap.Field{
				zap.Int("status", c.Writer.Status()),
				zap.Int("body_bytes", maxInt(c.Writer.Size(), 0)),
				zap.Int64("total_elapsed_ms", time.Since(reqStart).Milliseconds()),
			}
			// artifact_path is added for eager bundles (which always have an
			// on-disk directory) AND for deferred bundles whose Promote()
			// succeeded. Skipped for deferred-but-not-promoted bundles (status
			// <500 / no panic — directory was never created) AND for deferred
			// bundles whose Promote() failed (mkdir error — directory does not
			// and will never exist on disk). Emitting the path in either of
			// the latter cases would mislead log readers into chasing a path
			// that isn't there. The trace.bundle.promote_failed Warn line
			// above is the operator's correlation point when we omit it here.
			if bundle != nil && (!deferredBundle || promoteSucceeded) {
				respFields = append(respFields, zap.String("artifact_path", bundle.Root()))
			}
			emitter.Emit(c.Request.Context(), narrate.PhaseResponseSent, respOutcome, "", respFields...)

			// Set the bundle's outcome so the manifest reflects the request result.
			// SetOutcome and Close are nil-receiver no-ops, so we drop the
			// `if bundle != nil` guards for consistency (REVIEWER nit).
			if respOutcome == narrate.OutcomeError {
				bundle.SetOutcome("error")
			}

			// Close the bundle at request end, finalising 00-manifest.json.
			// Close is idempotent and nil-safe. For deferred-but-unpromoted
			// bundles, Close() drops the in-memory buffers without touching
			// disk — see Bundle.Close()'s deferred branch.
			_ = bundle.Close()

			// Re-raise the panic AFTER bundle finalisation so any outer
			// recovery middleware (or the runtime's default panic handler)
			// still observes it. We deliberately do NOT swallow it — that
			// would mask the bug from operators and from gin's own recovery.
			if panicked {
				panic(rec)
			}
		}()

		// Run the rest of the middleware chain + handler. Any panic from
		// here propagates through the defer above, which captures it,
		// finalises the bundle, then re-raises.
		c.Next()
	}
}

// hashIP returns the first 8 hex chars of a SHA-256 hash of the client IP.
// This gives us a stable per-IP identifier in narrate lines without storing
// the raw IP address (which is PII in many jurisdictions). Truncating to 8
// chars trades collision resistance (1 in ~4 billion) for log brevity —
// adequate for "did this storm of requests come from one IP" triage.
func hashIP(ip string) string {
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:4])
}

// maxInt is a local helper to avoid importing math just for two lines. The
// builtin `max` exists in Go 1.21+ but using a local for clarity.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// detectTraceTrigger returns the trigger source (header > query) and whether
// any opt-in flag is present. Header takes precedence per spec §8 — the
// header is the recommended UI for shell users, the query string is for
// browser experiments.
func detectTraceTrigger(c *gin.Context) (artifact.Trigger, bool) {
	if h := c.GetHeader(traceHeaderName); isTruthy(h) {
		return artifact.TriggerHeader, true
	}
	if q := c.Query(traceQueryParam); isTruthy(q) {
		return artifact.TriggerQuery, true
	}
	return "", false
}

// isTruthy normalises common opt-in spellings: "1", "true", "yes", "on".
// Empty string is false. Strict — anything else is treated as off so a
// typo doesn't quietly enable tracing in production.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
