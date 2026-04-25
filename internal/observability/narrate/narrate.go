package narrate

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// Config controls narrate emission behaviour. Mirrors the
// logging.narrate.* keys in config/config.yaml so the DI container can pass
// them straight through.
type Config struct {
	// Enabled is the master switch. When false, every Emit call is a no-op
	// regardless of the per-request sample decision.
	Enabled bool

	// SampleRate is a value in [0.0, 1.0]. Sampling decision is made ONCE per
	// request_id at request entry and stuck on the emitter. A request is
	// either fully narrated (every phase) or fully sampled out (no phases) —
	// never half-told.
	SampleRate float64

	// RedactFields are field keys to drop entirely from emitted lines. Used
	// for fields the operator considers PII (e.g. client_ip_hash).
	RedactFields []string
}

// Emitter is the request-scoped writer for the narrate stream. Construct one
// per HTTP request via NewEmitter, attach it to the request context with
// Inject, and retrieve it from any service via From.
//
// Standard fields automatically appended to every Emit call:
//   - event=narrate (filterable: rg event=narrate gives the entire stream)
//   - request_id (inherited from logctx.From(ctx))
//   - ticker (set via WithTicker once the handler parses the URL)
//
// All Emit calls are level=Info. Tier-2 Debug-tracer lines are NOT emitted
// through this type — they go directly through logctx.From(ctx).Debug with
// the documented "trace.<area>.<op>" message convention.
type Emitter struct {
	cfg          Config
	sampledOut   bool
	redactSet    map[string]struct{}
	mu           sync.RWMutex
	ticker       string
	payloadRoot  string // optional artifact-bundle directory; empty when tracing off
	requestStart time.Time
}

// NewEmitter constructs a request-scoped narrate.Emitter. The sample decision
// is made here, deterministically, by hashing requestID against SampleRate so
// the same request always yields the same in/out decision (no per-line jitter).
//
// requestID may be empty in early-startup paths; in that case sample_rate=1.0
// will still emit and sample_rate<1.0 falls through to a stable hash of the
// empty string (always sampled in or always out).
func NewEmitter(cfg Config, requestID string) *Emitter {
	e := &Emitter{
		cfg:          cfg,
		requestStart: time.Now(),
	}

	// Build the redact set once.
	if len(cfg.RedactFields) > 0 {
		e.redactSet = make(map[string]struct{}, len(cfg.RedactFields))
		for _, f := range cfg.RedactFields {
			e.redactSet[f] = struct{}{}
		}
	}

	// Sample decision: hash request_id mod 100 and compare against
	// SampleRate*100. Sample_rate=1.0 always passes; 0.0 always fails.
	// This is intentionally cheap (FNV-32) — narrate sampling is statistical
	// triage, not security gating.
	if cfg.Enabled {
		switch {
		case cfg.SampleRate <= 0:
			e.sampledOut = true
		case cfg.SampleRate >= 1:
			e.sampledOut = false
		default:
			h := fnv.New32a()
			_, _ = h.Write([]byte(requestID))
			bucket := float64(h.Sum32()%10000) / 10000.0
			e.sampledOut = bucket >= cfg.SampleRate
		}
	} else {
		e.sampledOut = true
	}

	return e
}

// WithTicker stamps the ticker on the emitter so subsequent Emit calls carry
// it as a standard field. Safe to call multiple times — last call wins.
// Called by the fair-value handler after URL parsing.
func (e *Emitter) WithTicker(ticker string) {
	e.mu.Lock()
	e.ticker = ticker
	e.mu.Unlock()
}

// WithPayloadRoot records the on-disk artifact-bundle root for this request.
// When set, Emit calls that pass a payload_ref field will resolve it relative
// to this root, letting the narrate line carry a path to the per-phase file.
// No-op when artifact tracing is disabled for the request.
func (e *Emitter) WithPayloadRoot(root string) {
	e.mu.Lock()
	e.payloadRoot = root
	e.mu.Unlock()
}

// PayloadRoot returns the artifact-bundle directory recorded for this
// emitter, or empty if tracing is off. Lets handlers/services check whether
// to bother marshalling snapshots.
func (e *Emitter) PayloadRoot() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.payloadRoot
}

// Sampled reports whether this emitter will actually emit. Useful for callers
// that want to skip expensive field materialisation when the request was
// sampled out.
func (e *Emitter) Sampled() bool {
	return !e.sampledOut
}

// Emit writes one narrate line at Info level through the request's context
// logger. The phase, outcome, and notes parameters are required; additional
// zap fields can be passed for phase-specific detail (see spec §5).
//
// Behaviour:
//   - No-op when narrate is disabled or the request was sampled out.
//   - No-op when ctx carries no logger (logctx miss); inheriting nop logger
//     from logctx.From means the line is silently discarded.
//   - notes may be empty; it is only appended when non-empty.
//   - elapsed_ms can be passed as a regular zap.Int64 field by the caller.
func (e *Emitter) Emit(ctx context.Context, phase Phase, outcome Outcome, notes string, fields ...zap.Field) {
	// Cheap early-return guards keep the hot path free of allocations
	// when narrate is off OR this request was sampled out.
	if e == nil || e.sampledOut {
		return
	}

	l := logctx.From(ctx)

	e.mu.RLock()
	ticker := e.ticker
	e.mu.RUnlock()

	// Standard fields first; per-call fields appended after so callers can
	// override only by emitting an additional field with the same key (zap
	// keeps later fields in JSON encoder).
	std := make([]zap.Field, 0, 4+len(fields))
	std = append(std,
		zap.String("event", "narrate"),
		zap.String("phase", string(phase)),
		zap.String("outcome", string(outcome)),
	)
	if ticker != "" {
		std = append(std, zap.String("ticker", ticker))
	}
	if notes != "" {
		std = append(std, zap.String("notes", notes))
	}

	// Apply field-level redaction: drop any field whose key is in redactSet.
	// We do this after the standard fields so callers can never accidentally
	// redact the standard ones (event, phase, outcome, ticker, notes).
	if len(e.redactSet) == 0 {
		std = append(std, fields...)
	} else {
		for _, f := range fields {
			if _, drop := e.redactSet[f.Key]; drop {
				continue
			}
			std = append(std, f)
		}
	}

	// All narrate lines are Info level. The message is "narrate" so a
	// human reading raw output can spot the stream at a glance; the
	// event=narrate field is what programmatic filters key on.
	l.Info("narrate", std...)
}

// emitterKey is the unexported context key for storing the request-scoped
// Emitter. Using a private struct prevents collisions with any other package.
type emitterKey struct{}

// Inject returns a child context carrying e. The original ctx is unchanged.
func Inject(ctx context.Context, e *Emitter) context.Context {
	return context.WithValue(ctx, emitterKey{}, e)
}

// From retrieves the Emitter stored in ctx, or returns a nil-safe disabled
// emitter when none is present. The returned emitter is always safe to call
// Emit on — it is either the live request emitter or a no-op stand-in.
func From(ctx context.Context) *Emitter {
	if ctx == nil {
		return disabledEmitter()
	}
	if v, ok := ctx.Value(emitterKey{}).(*Emitter); ok && v != nil {
		return v
	}
	return disabledEmitter()
}

// disabledEmitter returns a singleton no-op emitter. Cached at package init
// so From() never allocates on a context-miss.
//
//nolint:gochecknoglobals // intentional process-wide nop singleton; immutable
var disabledEmitter = func() func() *Emitter {
	nop := &Emitter{sampledOut: true}
	return func() *Emitter { return nop }
}()
