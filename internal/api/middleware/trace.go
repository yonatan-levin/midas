// Package middleware holds Gin middleware that doesn't belong to the
// monolithic server.go file. The trace middleware is the entry point for
// the Tier-1 narrate stream and the Tier-3 artifact bundle: it decides per
// request whether tracing is on, opens the bundle if so, attaches the
// narrate emitter + bundle to ctx, and finalises both on response.
//
// See docs/refactoring/observability-narrative-and-artifacts-spec.md (§8).
package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
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
		trigger, traceFlag := detectTraceTrigger(c)
		var bundle *artifact.Bundle
		if traceFlag && cfgA.Enabled {
			// Ticker is unknown at this point — handler will SetTicker
			// after URL parsing.
			b, err := artifact.OpenBundle(cfgA, requestID, "", trigger)
			if err == nil {
				bundle = b
				emitter.WithPayloadRoot(b.Root())
			}
		}

		// Attach both to the request context so downstream middleware/handlers
		// can pull them via narrate.From / artifact.From.
		ctx := narrate.Inject(c.Request.Context(), emitter)
		if bundle != nil {
			ctx = artifact.Inject(ctx, bundle)
		}
		c.Request = c.Request.WithContext(ctx)

		// Stash the trigger flag on the gin context so commit 3's
		// request.received emission can carry trace_enabled+reason without
		// re-parsing the URL/headers.
		c.Set("trace_flag", traceFlag)
		c.Set("trace_trigger", string(trigger))
		c.Set("trace_enabled", traceFlag && cfgA.Enabled)
		if traceFlag && !cfgA.Enabled {
			c.Set("trace_reason", "disabled")
		}

		// Run the rest of the middleware chain + handler.
		c.Next()

		// Close the bundle at request end, finalising 00-manifest.json.
		// Close is idempotent and nil-safe.
		_ = bundle.Close()
	}
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
