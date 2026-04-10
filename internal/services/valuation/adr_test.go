package valuation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetCountryForTicker(t *testing.T) {
	tests := []struct {
		name     string
		ticker   string
		expected string
	}{
		{name: "TSM maps to Taiwan", ticker: "TSM", expected: "TW"},
		{name: "BABA maps to China", ticker: "BABA", expected: "CN"},
		{name: "NVO maps to Denmark", ticker: "NVO", expected: "DK"},
		{name: "ASML maps to Netherlands", ticker: "ASML", expected: "NL"},
		{name: "SHOP maps to Canada", ticker: "SHOP", expected: "CA"},
		{name: "INFY maps to India", ticker: "INFY", expected: "IN"},
		{name: "VALE maps to Brazil", ticker: "VALE", expected: "BR"},
		{name: "AZN maps to UK", ticker: "AZN", expected: "UK"},
		{name: "Unknown ticker defaults to US", ticker: "AAPL", expected: "US"},
		{name: "Another unknown defaults to US", ticker: "MSFT", expected: "US"},
		{name: "Case insensitive lookup", ticker: "tsm", expected: "TW"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetCountryForTicker(tt.ticker)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetCountryRiskPremium(t *testing.T) {
	premiums := map[string]float64{
		"US":      0.0,
		"CN":      0.025,
		"BR":      0.035,
		"TW":      0.01,
		"default": 0.02,
	}

	tests := []struct {
		name        string
		countryCode string
		expected    float64
	}{
		{name: "US has zero CRP", countryCode: "US", expected: 0.0},
		{name: "China CRP", countryCode: "CN", expected: 0.025},
		{name: "Brazil CRP", countryCode: "BR", expected: 0.035},
		{name: "Taiwan CRP", countryCode: "TW", expected: 0.01},
		{name: "Unknown country falls back to default", countryCode: "XX", expected: 0.02},
		{name: "Case insensitive", countryCode: "cn", expected: 0.025},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetCountryRiskPremium(premiums, tt.countryCode)
			assert.InDelta(t, tt.expected, result, 0.0001)
		})
	}
}

func TestGetCountryRiskPremium_EmptyMap(t *testing.T) {
	// When the premiums map is empty, should return 0
	result := GetCountryRiskPremium(map[string]float64{}, "CN")
	assert.Equal(t, 0.0, result)
}

func TestGetCountryRiskPremium_NoDefault(t *testing.T) {
	// When the premiums map has entries but no default, unknown countries get 0
	premiums := map[string]float64{
		"US": 0.0,
		"CN": 0.025,
	}
	result := GetCountryRiskPremium(premiums, "XX")
	assert.Equal(t, 0.0, result)
}
