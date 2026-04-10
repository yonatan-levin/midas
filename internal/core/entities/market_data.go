package entities

import (
	"time"
)

// MarketData represents current market pricing and risk metrics for a company
type MarketData struct {
	// Identifiers
	Ticker string    `json:"ticker"`
	AsOf   time.Time `json:"as_of"`

	// Current market metrics
	SharePrice        float64 `json:"share_price"`        // Current stock price
	MarketCap         float64 `json:"market_cap"`         // Share price * shares outstanding
	SharesOutstanding float64 `json:"shares_outstanding"` // Current shares outstanding

	// Risk metrics
	Beta   float64 `json:"beta"`    // 1-year beta
	Beta3Y float64 `json:"beta_3y"` // 3-year beta for stability

	// Volume and liquidity
	AverageVolume float64 `json:"average_volume"` // Average daily trading volume

	// International risk metrics
	DomicileCountry    string  `json:"domicile_country,omitempty"`     // ISO-2 country code (e.g., "US", "CN", "TW")
	CountryRiskPremium float64 `json:"country_risk_premium,omitempty"` // Damodaran-style CRP for the domicile country

	// Data source metadata
	Source      string `json:"source"`       // yfinance, finzive, etc.
	DataQuality string `json:"data_quality"` // high, medium, low
}

// MacroData represents macro-economic data needed for WACC calculation
type MacroData struct {
	AsOf time.Time `json:"as_of" db:"as_of"`

	// Risk-free rate (typically 10-year Treasury)
	RiskFreeRate       float64 `json:"risk_free_rate" db:"risk_free_rate"`       // Current 10-year Treasury yield
	RiskFreeRate3Month float64 `json:"risk_free_rate_3m" db:"risk_free_rate_3m"` // 3-month Treasury for short-term

	// Market risk premium (configurable but typically ~5%)
	MarketRiskPremium float64 `json:"market_risk_premium" db:"market_risk_premium"`

	// Economic indicators
	InflationRate float64 `json:"inflation_rate" db:"inflation_rate"` // Current inflation rate

	// Data source
	Source string `json:"source" db:"source"` // 'fred', 'manual', etc.
}

// IsStale checks if the macro data is stale beyond the given duration
func (m *MacroData) IsStale(maxAge time.Duration) bool {
	return time.Since(m.AsOf) > maxAge
}

// GetEffectiveRiskFreeRate returns the most appropriate risk-free rate
func (m *MacroData) GetEffectiveRiskFreeRate() float64 {
	// Prefer 10-year rate for DCF calculations, fallback to 3-month
	if m.RiskFreeRate > 0 {
		return m.RiskFreeRate
	}
	return m.RiskFreeRate3Month
}

// IsComplete checks if the macro data has all required fields
func (m *MacroData) IsComplete() bool {
	return m.GetEffectiveRiskFreeRate() > 0 && m.MarketRiskPremium > 0
}

// GetWACCInputs returns the inputs needed for WACC calculation
func (m *MacroData) GetWACCInputs() (riskFreeRate, marketRiskPremium float64) {
	return m.GetEffectiveRiskFreeRate(), m.MarketRiskPremium
}

// IsValidBeta checks if beta is within reasonable bounds
func (md *MarketData) IsValidBeta() bool {
	return md.Beta > 0 && md.Beta < 5.0 // Typical range for public companies
}

// GetEffectiveBeta returns the best available beta value
func (md *MarketData) GetEffectiveBeta() float64 {
	if md.Beta > 0 {
		return md.Beta
	}
	if md.Beta3Y > 0 {
		return md.Beta3Y
	}
	return 1.0 // Default beta if none available
}

// IsComplete checks if market data has minimum required fields
func (md *MarketData) IsComplete() bool {
	return md.SharePrice > 0 && md.SharesOutstanding > 0
}

// CalculateMarketValue calculates the market value of equity
func (md *MarketData) CalculateMarketValue() float64 {
	return md.SharePrice * md.SharesOutstanding
}

// GetDataAge returns how old the market data is
func (md *MarketData) GetDataAge() time.Duration {
	return time.Since(md.AsOf)
}

// IsStale checks if the market data is stale beyond the given duration
func (md *MarketData) IsStale(maxAge time.Duration) bool {
	return md.GetDataAge() > maxAge
}
