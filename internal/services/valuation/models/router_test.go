package models

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func testLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func newTestRouter() *ModelRouter {
	logger := testLogger()
	allModels := []ValuationModel{
		NewDDMModel(logger),
		NewFFOModelWithMultiple(DefaultPFFOMultiple, logger),
		NewRevenueMultipleModelWithMultiples(map[string]float64{"default": DefaultEVRevenueMultiple}, logger),
		NewMultiStageDCFModel(logger),
	}
	// nil calcEmitter — no calc traces in unit tests; they only fire in integration tests.
	return NewModelRouter(allModels, logger, nil)
}

// TestModelRouter_SelectModel tests all routing scenarios
func TestModelRouter_SelectModel(t *testing.T) {
	tests := []struct {
		name          string
		industry      string
		financials    *entities.FinancialData
		expectedModel string
	}{
		{
			name:     "financial industry routes to DDM",
			industry: "FIN",
			financials: &entities.FinancialData{
				OperatingIncome: 1000,
			},
			expectedModel: "ddm",
		},
		{
			name:     "financial sub-industry routes to DDM",
			industry: "FIN_IB",
			financials: &entities.FinancialData{
				OperatingIncome: 1000,
			},
			expectedModel: "ddm",
		},
		{
			name:     "REIT routes to FFO",
			industry: "REIT",
			financials: &entities.FinancialData{
				OperatingIncome: 500,
			},
			expectedModel: "ffo",
		},
		{
			name:     "RESTATE (real estate) routes to FFO",
			industry: "RESTATE",
			financials: &entities.FinancialData{
				OperatingIncome: 500,
			},
			expectedModel: "ffo",
		},
		{
			name:     "negative OI routes to revenue multiple",
			industry: "TECH",
			financials: &entities.FinancialData{
				OperatingIncome:           -100,
				NormalizedOperatingIncome: -50,
			},
			expectedModel: "revenue_multiple",
		},
		{
			name:     "zero OI routes to revenue multiple",
			industry: "HEALTH",
			financials: &entities.FinancialData{
				OperatingIncome:           0,
				NormalizedOperatingIncome: 0,
			},
			expectedModel: "revenue_multiple",
		},
		{
			name:     "positive OI tech routes to DCF",
			industry: "TECH",
			financials: &entities.FinancialData{
				OperatingIncome:           1000,
				NormalizedOperatingIncome: 900,
			},
			expectedModel: "multi_stage_dcf",
		},
		{
			name:     "positive OI default industry routes to DCF",
			industry: "MFG",
			financials: &entities.FinancialData{
				OperatingIncome: 2000,
			},
			expectedModel: "multi_stage_dcf",
		},
		{
			name:     "empty industry with positive OI routes to DCF",
			industry: "",
			financials: &entities.FinancialData{
				OperatingIncome: 1000,
			},
			expectedModel: "multi_stage_dcf",
		},
		{
			name:          "nil financials routes to DCF (no OI check possible)",
			industry:      "TECH",
			financials:    nil,
			expectedModel: "multi_stage_dcf",
		},
		{
			name:     "FIN with negative OI still routes to DDM (industry takes priority)",
			industry: "FIN",
			financials: &entities.FinancialData{
				OperatingIncome: -100,
			},
			expectedModel: "ddm",
		},
		{
			name:     "case insensitive industry matching",
			industry: "fin",
			financials: &entities.FinancialData{
				OperatingIncome: 1000,
			},
			expectedModel: "ddm",
		},
		{
			name:     "case insensitive REIT matching",
			industry: "reit",
			financials: &entities.FinancialData{
				OperatingIncome: 500,
			},
			expectedModel: "ffo",
		},
		{
			name:     "normalized OI positive but raw OI negative still routes to DCF",
			industry: "TECH",
			financials: &entities.FinancialData{
				OperatingIncome:           -100,
				NormalizedOperatingIncome: 500,
			},
			expectedModel: "multi_stage_dcf",
		},
	}

	router := newTestRouter()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := router.SelectModel(context.Background(), tt.industry, tt.financials)
			require.NotNil(t, model, "model should not be nil")
			assert.Equal(t, tt.expectedModel, model.ModelType())
		})
	}
}

// TestModelRouter_SelectModel_NoModels tests behavior when no models are registered
func TestModelRouter_SelectModel_NoModels(t *testing.T) {
	router := NewModelRouter([]ValuationModel{}, testLogger(), nil)

	model := router.SelectModel(context.Background(), "TECH", &entities.FinancialData{
		OperatingIncome: 1000,
	})

	assert.Nil(t, model, "should return nil when no models are registered")
}

// TestModelRouter_FindModel tests the internal model lookup
func TestModelRouter_FindModel(t *testing.T) {
	router := newTestRouter()

	tests := []struct {
		name      string
		modelType string
		found     bool
	}{
		{"find DCF model", "multi_stage_dcf", true},
		{"find DDM model", "ddm", true},
		{"find FFO model", "ffo", true},
		{"find revenue multiple model", "revenue_multiple", true},
		{"non-existent model", "discounted_dividends_v2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := router.findModel(tt.modelType)
			if tt.found {
				require.NotNil(t, model)
				assert.Equal(t, tt.modelType, model.ModelType())
			} else {
				assert.Nil(t, model)
			}
		})
	}
}

// TestMultiStageDCFModel_Calculate_ReturnsError verifies that calling Calculate on the
// DCF model marker returns an error, since the real DCF logic lives in the service layer.
func TestMultiStageDCFModel_Calculate_ReturnsError(t *testing.T) {
	model := NewMultiStageDCFModel(testLogger())

	result, err := model.Calculate(context.Background(), &ModelInput{})
	assert.Error(t, err, "DCF model Calculate should return error")
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "should not be called directly")
}

// TestMultiStageDCFModel_ModelType verifies the model type identifier
func TestMultiStageDCFModel_ModelType(t *testing.T) {
	model := NewMultiStageDCFModel(testLogger())
	assert.Equal(t, "multi_stage_dcf", model.ModelType())
}
