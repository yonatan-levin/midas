package valuation

import (
	"context"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
)

// stageEmitter bundles the per-request emission context (ctx + the service's
// calcEmitter) so the repeated `if s.calcEmitter != nil { s.calcEmitter.Emit(...) }`
// ritual collapses to one call. Emit-only; carries NO math. Behavior is
// byte-identical: calc(stage, fields...) is exactly the old guarded Emit.
type stageEmitter struct {
	ctx     context.Context
	emitter *calclog.Emitter // may be nil — calc() guards it, matching today
}

// newStageEmitter binds the request ctx to the service's calcEmitter so calc()
// reproduces the legacy guarded emission without a per-site nil check.
func (s *Service) newStageEmitter(ctx context.Context) stageEmitter {
	return stageEmitter{ctx: ctx, emitter: s.calcEmitter}
}

// calc replays the legacy `if s.calcEmitter != nil { s.calcEmitter.Emit(ctx, stage, fields...) }`.
// The variadic field list keeps every per-stage field expression at the call
// site (several are computed inside the old guard), so this is a pure
// guard-collapse with no field movement.
func (se stageEmitter) calc(stage string, fields ...zap.Field) {
	if se.emitter != nil {
		se.emitter.Emit(se.ctx, stage, fields...)
	}
}
