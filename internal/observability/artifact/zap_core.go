package artifact

import (
	"go.uber.org/zap/zapcore"
)

// BundleSink is a zapcore.Core wrapper that forwards every log entry to the
// wrapped core unchanged, while also teeing entries with event="narrate"
// to <bundle>/99-narrate.jsonl and entries at Debug level to
// <bundle>/99-debug-trace.jsonl. Spec §7.1 + §7.3.
//
// The wrapper is installed by the trace middleware after a successful bundle
// open and replaces the request-scoped logger via logctx.Inject. This is
// what makes the bundle directory self-describing: a reader can `cat
// 99-narrate.jsonl` and see the full per-request story without having to
// grep the host process's log stream.
//
// Zero impact when bundle is nil: in that case the wrapper degenerates to a
// pass-through (the trace middleware never installs the wrapper for non-
// trace requests, but the nil-safety keeps unit tests cheap to write).
type BundleSink struct {
	zapcore.Core
	bundle *Bundle
	// encoder is owned by the sink so JSONL serialisation is independent
	// of whatever encoder the wrapped core uses (production may be JSON,
	// tests typically use the observer.New core which has no encoder).
	encoder zapcore.Encoder
	// fields accumulates the With() chain — zap calls With(...) on the
	// core, and at Write-time the Core gets ONLY the call-site fields, not
	// the With-chain. We keep our own copy so JSONL lines carry request_id
	// and other contextual fields the request-scoped logger added.
	fields []zapcore.Field
}

// NewBundleSink wraps the given core with bundle-tee behaviour. The wrapped
// core's Sync() is called when the wrapper's Sync() is called. Bundle stream
// files are flushed + closed by Bundle.Close(), not by Sync().
func NewBundleSink(wrapped zapcore.Core, bundle *Bundle) zapcore.Core {
	return &BundleSink{
		Core:    wrapped,
		bundle:  bundle,
		encoder: newJSONEncoder(),
	}
}

// newJSONEncoder builds the JSONL encoder used to serialise teed entries.
// Kept package-private so callers can't accidentally mismatch field naming.
// Uses zap's production encoder config so the keys match what the host
// process's stdout zap.Logger emits — a reader correlating the host log
// stream with 99-narrate.jsonl sees identical structure on both sides.
func newJSONEncoder() zapcore.Encoder {
	cfg := zapcore.EncoderConfig{
		MessageKey:     "msg",
		LevelKey:       "level",
		TimeKey:        "ts",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	return zapcore.NewJSONEncoder(cfg)
}

// Check tells zap whether this core wants the entry. We delegate to
// Enabled() (which the embedded Core implements) so the filter mirrors the
// wrapped core's level configuration.
func (s *BundleSink) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if s.Enabled(ent.Level) {
		return ce.AddCore(ent, s)
	}
	return ce
}

// Write forwards the entry to the wrapped core unchanged, then optionally
// tees the entry to one or both bundle JSONL streams.
//
// Tee rules (combined with the level check below):
//   - Entry has field event="narrate" (in either With-fields or call-site
//     fields) — tee to 99-narrate.jsonl regardless of level.
//   - Entry is at Debug level — tee to 99-debug-trace.jsonl.
//
// Both can fire on the same entry (a Debug narrate entry would land in
// both files), which is desirable: a reader of the debug stream wants every
// debug line, and a reader of the narrate stream wants every narrate line.
//
// The forwarding write happens FIRST so the wrapped core's behaviour is
// preserved even if the tee fails. Tee errors are intentionally swallowed
// (best-effort) and accounted for via Bundle.writeErrors so Close() can
// downgrade outcome to "partial" without breaking the host log pipeline.
func (s *BundleSink) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	// Forwarding write — preserves the host process's existing log output.
	if err := s.Core.Write(ent, fields); err != nil {
		return err
	}
	if s.bundle == nil {
		return nil
	}

	// Detect event=narrate. We check the call-site fields first (cheaper +
	// more common) then fall back to the accumulated With-fields (rare for
	// event=narrate — the narrate emitter sets it per-call).
	isNarrate := hasNarrateField(fields) || hasNarrateField(s.fields)

	// Early-out: only narrate entries OR Debug-level entries get teed.
	// In zap, Debug is the lowest level (DebugLevel=-1, InfoLevel=0,
	// WarnLevel=1, ErrorLevel=2, ...) so `ent.Level > zapcore.DebugLevel`
	// is true for Info and above. The check correctly LETS narrate entries
	// at Info+ pass through (because isNarrate short-circuits the early
	// return) while skipping non-narrate Info+ entries (which we don't
	// want to tee to either file). Do not "fix" this to use < or != without
	// re-validating against zap's level constants — getting it wrong drops
	// every Warn/Error narrate entry from the bundle.
	if !isNarrate && ent.Level > zapcore.DebugLevel {
		return nil
	}

	// Encode using the sink's owned JSON encoder. We pass the union of
	// With-fields + call-site fields so the JSONL line carries
	// request_id (from With) AND phase (from the call site).
	all := make([]zapcore.Field, 0, len(s.fields)+len(fields))
	all = append(all, s.fields...)
	all = append(all, fields...)

	buf, err := s.encoder.EncodeEntry(ent, all)
	if err != nil {
		// Encoder errors are best-effort for the sink — never fail the
		// wrapped write. Account via writeErrors so the manifest reflects
		// the loss.
		s.bundle.writeErrors.Add(1)
		return nil
	}
	defer buf.Free()

	if isNarrate {
		// AppendStream is nil-safe + closed-safe + tracks its own
		// writeErrors, so we can swallow the error here.
		_ = s.bundle.AppendStream("99-narrate.jsonl", buf.Bytes())
	}
	if ent.Level == zapcore.DebugLevel {
		_ = s.bundle.AppendStream("99-debug-trace.jsonl", buf.Bytes())
	}
	return nil
}

// With returns a derived sink whose accumulated fields include the new
// fields. Both the wrapped core and the encoder are cloned so the original
// sink is unaffected — this is the standard zapcore.Core contract.
func (s *BundleSink) With(fields []zapcore.Field) zapcore.Core {
	combined := make([]zapcore.Field, 0, len(s.fields)+len(fields))
	combined = append(combined, s.fields...)
	combined = append(combined, fields...)
	return &BundleSink{
		Core:    s.Core.With(fields),
		bundle:  s.bundle,
		encoder: s.encoder.Clone(),
		fields:  combined,
	}
}

// Sync forwards to the wrapped core. Bundle stream files are flushed on
// Bundle.Close(), not on every Sync() — narrate / debug entries are
// per-line writes that benefit from OS-level buffering, and forcing a
// flush per Sync would defeat that benefit without correctness gain.
func (s *BundleSink) Sync() error {
	return s.Core.Sync()
}

// hasNarrateField is the predicate the BundleSink uses to identify narrate
// log entries. Kept private because the only legitimate caller is the
// sink itself.
func hasNarrateField(fields []zapcore.Field) bool {
	for _, f := range fields {
		if f.Key == "event" && f.Type == zapcore.StringType && f.String == "narrate" {
			return true
		}
	}
	return false
}
