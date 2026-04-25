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
		trigger, traceFlag := detectTraceTrigger(c)
		var bundle *artifact.Bundle
		// openErr is captured so we can both surface the failure to log
		// readers (via Warn) and downgrade trace_enabled with an
		// explanatory reason on the narrate stream. Silent swallow makes
		// "?trace=1 returns no bundle" un-debuggable.
		var openErr error
		if traceFlag && cfgA.Enabled {
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
		}

		// Attach both to the request context so downstream middleware/handlers
		// can pull them via narrate.From / artifact.From.
		ctx := narrate.Inject(c.Request.Context(), emitter)
		if bundle != nil {
			ctx = artifact.Inject(ctx, bundle)
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

		// Run the rest of the middleware chain + handler.
		c.Next()

		// Tier-1 narrate: response.sent. Final line of the per-request story.
		// Outcome=error when the response status is >=500 per spec §4.
		respOutcome := narrate.OutcomeOK
		if c.Writer.Status() >= 500 {
			respOutcome = narrate.OutcomeError
		}
		respFields := []zap.Field{
			zap.Int("status", c.Writer.Status()),
			zap.Int("body_bytes", maxInt(c.Writer.Size(), 0)),
			zap.Int64("total_elapsed_ms", time.Since(reqStart).Milliseconds()),
		}
		if bundle != nil {
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
		// Close is idempotent and nil-safe.
		_ = bundle.Close()
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
