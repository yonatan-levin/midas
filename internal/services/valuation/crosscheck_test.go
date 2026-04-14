package valuation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateSanityCheck(t *testing.T) {
	tests := []struct {
		name              string
		dcfEquityValue    float64
		enterpriseValue   float64
		eps               float64
		ebitda            float64
		sharesOutstanding float64
		industry          string
		sectorPE          float64
		sectorEVEBITDA    float64
		wantReasonable    bool
		wantFlagCount     int
	}{
		{
			name:              "Reasonable valuation — multiples within range",
			dcfEquityValue:    1000.0,
			enterpriseValue:   1200.0,
			eps:               5.0,
			ebitda:            100.0,
			sharesOutstanding: 100.0,
			industry:          "TECH",
			sectorPE:          20.0,  // implied P/E = (1000/100)/5 = 2.0 -> ratio 2/20 = 0.1 < 0.5
			sectorEVEBITDA:    12.0,  // implied EV/EBITDA = 1200/100 = 12.0 -> ratio 1.0
			wantReasonable:    false, // P/E is way below sector
			wantFlagCount:     1,     // only P/E flagged
		},
		{
			name:              "Both multiples within sector norms",
			dcfEquityValue:    10000.0,
			enterpriseValue:   12000.0,
			eps:               5.0,
			ebitda:            1000.0,
			sharesOutstanding: 100.0,
			industry:          "TECH",
			sectorPE:          25.0, // implied P/E = (10000/100)/5 = 20.0 -> ratio 20/25 = 0.8
			sectorEVEBITDA:    12.0, // implied EV/EBITDA = 12000/1000 = 12.0 -> ratio 1.0
			wantReasonable:    true,
			wantFlagCount:     0,
		},
		{
			name:              "P/E way above sector median (overvalued by DCF)",
			dcfEquityValue:    50000.0,
			enterpriseValue:   55000.0,
			eps:               5.0,
			ebitda:            1000.0,
			sharesOutstanding: 100.0,
			industry:          "MFG",
			sectorPE:          16.0, // implied P/E = (50000/100)/5 = 100.0 -> ratio 100/16 = 6.25
			sectorEVEBITDA:    10.0, // implied EV/EBITDA = 55000/1000 = 55 -> ratio 5.5
			wantReasonable:    false,
			wantFlagCount:     2, // both P/E and EV/EBITDA flagged
		},
		{
			name:              "EV/EBITDA below sector (undervalued by DCF)",
			dcfEquityValue:    5000.0,
			enterpriseValue:   2000.0,
			eps:               5.0,
			ebitda:            1000.0,
			sharesOutstanding: 100.0,
			industry:          "ENERGY",
			sectorPE:          10.0, // implied P/E = (5000/100)/5 = 10.0 -> ratio 1.0
			sectorEVEBITDA:    6.0,  // implied EV/EBITDA = 2000/1000 = 2.0 -> ratio 0.33
			wantReasonable:    false,
			wantFlagCount:     1, // only EV/EBITDA flagged
		},
		{
			name:              "Zero EPS skips P/E check",
			dcfEquityValue:    10000.0,
			enterpriseValue:   12000.0,
			eps:               0.0,
			ebitda:            1000.0,
			sharesOutstanding: 100.0,
			industry:          "TECH",
			sectorPE:          25.0,
			sectorEVEBITDA:    12.0,
			wantReasonable:    true,
			wantFlagCount:     0,
		},
		{
			name:              "Zero EBITDA skips EV/EBITDA check",
			dcfEquityValue:    10000.0,
			enterpriseValue:   12000.0,
			eps:               5.0,
			ebitda:            0.0,
			sharesOutstanding: 100.0,
			industry:          "TECH",
			sectorPE:          25.0, // implied P/E = 20.0 -> ratio 0.8
			sectorEVEBITDA:    12.0,
			wantReasonable:    true,
			wantFlagCount:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateSanityCheck(
				tt.dcfEquityValue, tt.enterpriseValue,
				tt.eps, tt.ebitda,
				tt.sharesOutstanding,
				tt.industry,
				tt.sectorPE, tt.sectorEVEBITDA,
			)

			assert.Equal(t, tt.wantReasonable, result.IsReasonable,
				"IsReasonable mismatch")
			assert.Len(t, result.Flags, tt.wantFlagCount,
				"Flag count mismatch: %v", result.Flags)
		})
	}
}

func TestLookupMultiple(t *testing.T) {
	multiples := map[string]float64{
		"TECH":      18.0,
		"TECH_SAAS": 22.0,
		"FIN":       10.0,
		"ENERGY":    6.0,
		"default":   12.0,
	}

	tests := []struct {
		name     string
		industry string
		expected float64
	}{
		{name: "Exact match TECH", industry: "TECH", expected: 18.0},
		{name: "Exact match FIN", industry: "FIN", expected: 10.0},
		{name: "Exact match TECH_SAAS", industry: "TECH_SAAS", expected: 22.0},
		{name: "Prefix match TECH_HARDWARE", industry: "TECH_HARDWARE", expected: 18.0},
		{name: "Longest prefix match TECH_SAAS_CLOUD prefers TECH_SAAS over TECH", industry: "TECH_SAAS_CLOUD", expected: 22.0},
		{name: "Unknown falls back to default", industry: "UNKNOWN", expected: 12.0},
		{name: "Case insensitive", industry: "tech", expected: 18.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := LookupMultiple(multiples, tt.industry)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

func TestIsDeviationReasonable(t *testing.T) {
	tests := []struct {
		name     string
		implied  float64
		median   float64
		expected bool
	}{
		{name: "Ratio 1.0 is reasonable", implied: 20.0, median: 20.0, expected: true},
		{name: "Ratio 0.5 (boundary) is reasonable", implied: 10.0, median: 20.0, expected: true},
		{name: "Ratio 2.0 (boundary) is reasonable", implied: 40.0, median: 20.0, expected: true},
		{name: "Ratio 2.1 is unreasonable", implied: 42.0, median: 20.0, expected: false},
		{name: "Ratio 0.4 is unreasonable", implied: 8.0, median: 20.0, expected: false},
		{name: "Zero median is always reasonable", implied: 20.0, median: 0.0, expected: true},
		{name: "Zero implied is always reasonable", implied: 0.0, median: 20.0, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDeviationReasonable(tt.implied, tt.median)
			assert.Equal(t, tt.expected, result)
		})
	}
}
