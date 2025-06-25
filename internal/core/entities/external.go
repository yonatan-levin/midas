package entities

import (
	"time"
)

// CompanyFactsResponse represents the response from SEC Company Facts API
type CompanyFactsResponse struct {
	CIK         string                 `json:"cik"`
	EntityName  string                 `json:"entityName"`
	Facts       map[string]interface{} `json:"facts"`
	Units       map[string]interface{} `json:"units,omitempty"`
	FactsCount  int                    `json:"factsCount"`
	LastUpdated time.Time              `json:"lastUpdated"`
}

// ConceptResponse represents the response from SEC Company Concepts API
type ConceptResponse struct {
	CIK         string                 `json:"cik"`
	EntityName  string                 `json:"entityName"`
	Tag         string                 `json:"tag"`
	Taxonomy    string                 `json:"taxonomy"`
	Label       string                 `json:"label"`
	Description string                 `json:"description"`
	Units       map[string]interface{} `json:"units"`
	LastUpdated time.Time              `json:"lastUpdated"`
}

// PriceData represents historical price information
type PriceData struct {
	Ticker   string    `json:"ticker"`
	Date     time.Time `json:"date"`
	Open     float64   `json:"open"`
	High     float64   `json:"high"`
	Low      float64   `json:"low"`
	Close    float64   `json:"close"`
	Volume   int64     `json:"volume"`
	AdjClose float64   `json:"adj_close"`
}

// TreasuryRates represents current Treasury yield curve data
type TreasuryRates struct {
	AsOf        time.Time `json:"as_of"`
	Yield1Month float64   `json:"yield_1_month"`
	Yield3Month float64   `json:"yield_3_month"`
	Yield6Month float64   `json:"yield_6_month"`
	Yield1Year  float64   `json:"yield_1_year"`
	Yield2Year  float64   `json:"yield_2_year"`
	Yield5Year  float64   `json:"yield_5_year"`
	Yield10Year float64   `json:"yield_10_year"`
	Yield20Year float64   `json:"yield_20_year"`
	Yield30Year float64   `json:"yield_30_year"`
}

// GetEffective10Year returns the 10-year treasury rate for DCF calculations
func (t *TreasuryRates) GetEffective10Year() float64 {
	// Prefer 10-year, fallback to nearest available
	if t.Yield10Year > 0 {
		return t.Yield10Year
	}
	if t.Yield5Year > 0 {
		return t.Yield5Year
	}
	if t.Yield2Year > 0 {
		return t.Yield2Year
	}
	return 0.03 // Default 3% if no data available
}
