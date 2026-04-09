package growth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateInvestedCapital(t *testing.T) {
	tests := []struct {
		name     string
		equity   float64
		debt     float64
		cash     float64
		expected float64
	}{
		{"standard company", 50000, 30000, 10000, 70000},
		{"no debt", 50000, 0, 10000, 40000},
		{"no cash", 50000, 30000, 0, 80000},
		{"cash exceeds equity+debt floors at zero", 10000, 5000, 20000, 0},
		{"all zero", 0, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateInvestedCapital(tt.equity, tt.debt, tt.cash)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateROIC(t *testing.T) {
	tests := []struct {
		name            string
		nopat           float64
		investedCapital float64
		expected        float64
	}{
		{"standard 15% ROIC", 15000, 100000, 0.15},
		{"high ROIC", 30000, 100000, 0.30},
		{"zero invested capital", 15000, 0, 0},
		{"negative invested capital", 15000, -10000, 0},
		{"zero NOPAT", 0, 100000, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateROIC(tt.nopat, tt.investedCapital)
			assert.InDelta(t, tt.expected, result, 0.001)
		})
	}
}

func TestCalculateSustainableGrowth(t *testing.T) {
	tests := []struct {
		name            string
		nopat           float64
		investedCapital float64
		payoutRatio     float64
		expected        float64
	}{
		{
			name:            "ROIC 15%, payout 40% -> sustainable growth 9%",
			nopat:           15000,
			investedCapital: 100000,
			payoutRatio:     0.4,
			expected:        0.09, // 15% × (1 - 40%) = 9%
		},
		{
			name:            "ROIC 20%, zero payout -> sustainable growth 20%",
			nopat:           20000,
			investedCapital: 100000,
			payoutRatio:     0.0,
			expected:        0.20,
		},
		{
			name:            "ROIC 10%, 100% payout -> zero growth",
			nopat:           10000,
			investedCapital: 100000,
			payoutRatio:     1.0,
			expected:        0.0,
		},
		{
			name:            "zero invested capital -> zero growth",
			nopat:           15000,
			investedCapital: 0,
			payoutRatio:     0.3,
			expected:        0.0,
		},
		{
			name:            "negative payout ratio clamped to 0",
			nopat:           15000,
			investedCapital: 100000,
			payoutRatio:     -0.5,
			expected:        0.15, // payout clamped to 0, so full reinvestment
		},
		{
			name:            "payout ratio > 1 clamped to 1",
			nopat:           15000,
			investedCapital: 100000,
			payoutRatio:     1.5,
			expected:        0.0, // payout clamped to 1, zero reinvestment
		},
		{
			name:            "negative NOPAT -> zero growth",
			nopat:           -5000,
			investedCapital: 100000,
			payoutRatio:     0.3,
			expected:        0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateSustainableGrowth(tt.nopat, tt.investedCapital, tt.payoutRatio)
			assert.InDelta(t, tt.expected, result, 0.001)
		})
	}
}
