package models

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// MultiStageDCFModel is a marker model representing the existing multi-stage DCF calculation.
//
// Unlike DDM, FFO, and Revenue Multiple models which are self-contained, the DCF model's
// calculation logic lives in the valuation service's performValuation method. This model
// serves as a routing target so the ModelRouter can identify "use the standard DCF path".
//
// The Calculate method is intentionally a no-op that returns an error — callers should
// check ModelType() and use the service's DCF path directly.
type MultiStageDCFModel struct {
	logger *zap.Logger
}

// NewMultiStageDCFModel creates a new multi-stage DCF model marker.
func NewMultiStageDCFModel(logger *zap.Logger) *MultiStageDCFModel {
	return &MultiStageDCFModel{
		logger: logger.Named("dcf-model"),
	}
}

// ModelType returns the model identifier.
func (m *MultiStageDCFModel) ModelType() string {
	return "multi_stage_dcf"
}

// SupportsIndustry returns true for all industries — DCF is the universal default.
func (m *MultiStageDCFModel) SupportsIndustry(industry string) bool {
	return true
}

// Calculate is a no-op for the DCF model. The actual DCF calculation is performed
// by the valuation service's performValuation method. This exists only to satisfy
// the ValuationModel interface.
//
// The valuation service checks ModelType() == "multi_stage_dcf" and routes to
// the existing DCF code path instead of calling this method.
func (m *MultiStageDCFModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
	return nil, fmt.Errorf("dcf_model: Calculate should not be called directly; use valuation service DCF path")
}
