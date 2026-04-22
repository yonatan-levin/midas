// Package calclog provides structured logging helpers for financial calculation
// stages. Calculation tracing is controlled by the TraceCalculations config
// flag: when enabled, entries are emitted at Info level so they appear in
// production log streams; when disabled they drop to Debug so they only show
// up during local development.
//
// # Design
//
// The central type is Emitter, a thin struct that is wired through the fx DI
// container. Phase S callers (valuation engine, growth estimator, etc.) will
// receive *Emitter via constructor injection and call Emit() at each discrete
// calculation stage. There are no package-level globals — all state lives in
// the Emitter struct.
package calclog

import (
	"context"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// Emitter emits structured log entries at discrete calculation stages.
// It is intended to be provided by the DI container and injected into services
// that perform multi-step financial calculations (DCF, WACC, growth blend, etc.).
type Emitter struct {
	// traceInfo controls whether Emit logs at Info (true) or Debug (false).
	// Mapped from config.Logging.TraceCalculations.
	traceInfo bool
}

// NewEmitter creates an Emitter configured from the application's LoggingConfig.
// This constructor is registered with fx.Provide so that any service needing
// calc-level tracing can declare a *calclog.Emitter dependency.
func NewEmitter(cfg *config.Config) *Emitter {
	return &Emitter{
		traceInfo: cfg.Logging.TraceCalculations,
	}
}

// Emit logs a calculation stage event through the logger stored in ctx.
//
// Every entry always carries two standard fields:
//   - "stage" — identifies the calculation phase (e.g., "dcf_wacc", "growth_blend").
//   - "event" — fixed string "calc", allowing easy log-stream filtering.
//
// Additional domain-specific fields are appended after the standard fields.
//
// Behaviour:
//   - If stage is empty, it falls back to "unknown" (no panic).
//   - If ctx carries no logger (logctx miss), the nop logger is used — the
//     entry is silently discarded and no panic occurs.
//   - Level: Info when traceInfo=true, Debug when traceInfo=false.
func (e *Emitter) Emit(ctx context.Context, stage string, fields ...zap.Field) {
	// Guard: replace empty stage with a sentinel value rather than panicking.
	if stage == "" {
		stage = "unknown"
	}

	// Resolve the logger from context; falls back to nop if not present.
	l := logctx.From(ctx)

	// Build the complete field slice: standard fields first, then caller fields.
	// Pre-allocating the slice avoids an extra allocation per call.
	allFields := make([]zap.Field, 0, 2+len(fields))
	allFields = append(allFields, zap.String("stage", stage), zap.String("event", "calc"))
	allFields = append(allFields, fields...)

	// Level is determined by the TraceCalculations config flag.
	if e.traceInfo {
		l.Info("calc", allFields...)
	} else {
		l.Debug("calc", allFields...)
	}
}
