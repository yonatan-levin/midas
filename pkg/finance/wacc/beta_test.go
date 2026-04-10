package wacc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBlumeAdjustedBeta(t *testing.T) {
	tests := []struct {
		name     string
		rawBeta  float64
		expected float64
	}{
		{
			name:     "Market beta (1.0) stays at 1.0",
			rawBeta:  1.0,
			expected: 1.0, // 0.67*1.0 + 0.33 = 1.0
		},
		{
			name:     "High beta (1.5) regresses toward 1.0",
			rawBeta:  1.5,
			expected: 0.67*1.5 + 0.33, // 1.335
		},
		{
			name:     "Low beta (0.5) regresses toward 1.0",
			rawBeta:  0.5,
			expected: 0.67*0.5 + 0.33, // 0.665
		},
		{
			name:     "Very high beta (2.5)",
			rawBeta:  2.5,
			expected: 0.67*2.5 + 0.33, // 2.005
		},
		{
			name:     "Zero beta",
			rawBeta:  0.0,
			expected: 0.33, // 0.67*0 + 0.33
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BlumeAdjustedBeta(tt.rawBeta)
			assert.InDelta(t, tt.expected, result, 0.0001,
				"BlumeAdjustedBeta(%v) should be %v", tt.rawBeta, tt.expected)
		})
	}
}

func TestBlumeAdjustedBeta_Properties(t *testing.T) {
	// Property: Blume adjustment always moves beta toward 1.0
	t.Run("High betas are reduced", func(t *testing.T) {
		for _, beta := range []float64{1.1, 1.5, 2.0, 3.0} {
			adjusted := BlumeAdjustedBeta(beta)
			assert.Less(t, adjusted, beta,
				"Adjusted beta should be less than raw beta when raw > 1.0")
		}
	})

	t.Run("Low betas are increased", func(t *testing.T) {
		for _, beta := range []float64{0.1, 0.3, 0.5, 0.9} {
			adjusted := BlumeAdjustedBeta(beta)
			assert.Greater(t, adjusted, beta,
				"Adjusted beta should be greater than raw beta when raw < 1.0")
		}
	})
}

func TestUnleveredBeta(t *testing.T) {
	tests := []struct {
		name            string
		leveredBeta     float64
		taxRate         float64
		debtEquityRatio float64
		expected        float64
	}{
		{
			name:            "No debt (D/E = 0) returns same beta",
			leveredBeta:     1.2,
			taxRate:         0.21,
			debtEquityRatio: 0.0,
			expected:        1.2, // 1.2 / (1 + 0) = 1.2
		},
		{
			name:            "Moderate leverage (D/E = 0.5)",
			leveredBeta:     1.2,
			taxRate:         0.21,
			debtEquityRatio: 0.5,
			expected:        1.2 / (1 + 0.79*0.5), // 1.2 / 1.395 = 0.8602
		},
		{
			name:            "High leverage (D/E = 2.0)",
			leveredBeta:     1.5,
			taxRate:         0.25,
			debtEquityRatio: 2.0,
			expected:        1.5 / (1 + 0.75*2.0), // 1.5 / 2.5 = 0.6
		},
		{
			name:            "Zero tax rate",
			leveredBeta:     1.0,
			taxRate:         0.0,
			debtEquityRatio: 1.0,
			expected:        1.0 / (1 + 1.0*1.0), // 1.0 / 2.0 = 0.5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := UnleveredBeta(tt.leveredBeta, tt.taxRate, tt.debtEquityRatio)
			assert.InDelta(t, tt.expected, result, 0.0001,
				"UnleveredBeta(%v, %v, %v)", tt.leveredBeta, tt.taxRate, tt.debtEquityRatio)
		})
	}
}

func TestRelleveredBeta(t *testing.T) {
	tests := []struct {
		name                  string
		unleveredBeta         float64
		taxRate               float64
		targetDebtEquityRatio float64
		expected              float64
	}{
		{
			name:                  "No target leverage returns same beta",
			unleveredBeta:         0.8,
			taxRate:               0.21,
			targetDebtEquityRatio: 0.0,
			expected:              0.8, // 0.8 * (1 + 0) = 0.8
		},
		{
			name:                  "Moderate target leverage (D/E = 0.5)",
			unleveredBeta:         0.8,
			taxRate:               0.21,
			targetDebtEquityRatio: 0.5,
			expected:              0.8 * (1 + 0.79*0.5), // 0.8 * 1.395 = 1.116
		},
		{
			name:                  "High target leverage (D/E = 2.0)",
			unleveredBeta:         0.6,
			taxRate:               0.25,
			targetDebtEquityRatio: 2.0,
			expected:              0.6 * (1 + 0.75*2.0), // 0.6 * 2.5 = 1.5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RelleveredBeta(tt.unleveredBeta, tt.taxRate, tt.targetDebtEquityRatio)
			assert.InDelta(t, tt.expected, result, 0.0001,
				"RelleveredBeta(%v, %v, %v)", tt.unleveredBeta, tt.taxRate, tt.targetDebtEquityRatio)
		})
	}
}

func TestUnleverReleverRoundtrip(t *testing.T) {
	// Property: unlevering then re-levering with the same D/E ratio should return
	// the original beta (within floating point tolerance).
	leveredBeta := 1.3
	taxRate := 0.21
	debtEquityRatio := 0.6

	unlevered := UnleveredBeta(leveredBeta, taxRate, debtEquityRatio)
	relevered := RelleveredBeta(unlevered, taxRate, debtEquityRatio)

	assert.InDelta(t, leveredBeta, relevered, 0.0001,
		"Unlever then re-lever with same D/E should produce the original beta")
}
