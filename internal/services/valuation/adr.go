package valuation

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DefaultCountryRiskConfigPath is the default path to the country risk config file.
const DefaultCountryRiskConfigPath = "./config/country_risk.json"

// knownADRCountries maps well-known ADR tickers to their domicile country ISO-2 codes.
// ADRs that file with SEC (20-F) already flow through the existing pipeline;
// this mapping adds the country context needed for country risk premium lookup.
//
// TODO: Enrich this from SEC EDGAR entity metadata (country field) when available.
var knownADRCountries = map[string]string{
	"TSM":  "TW", // Taiwan Semiconductor
	"NVO":  "DK", // Novo Nordisk
	"BABA": "CN", // Alibaba
	"JD":   "CN", // JD.com
	"PDD":  "CN", // PDD Holdings
	"BIDU": "CN", // Baidu
	"NIO":  "CN", // NIO
	"LI":   "CN", // Li Auto
	"XPEV": "CN", // XPeng
	"SE":   "SG", // Sea Limited
	"GRAB": "SG", // Grab Holdings
	"MELI": "AR", // MercadoLibre
	"NU":   "BR", // Nu Holdings
	"VALE": "BR", // Vale
	"PBR":  "BR", // Petrobras
	"ITUB": "BR", // Itau Unibanco
	"INFY": "IN", // Infosys
	"WIT":  "IN", // Wipro
	"HDB":  "IN", // HDFC Bank
	"TM":   "JP", // Toyota
	"SNY":  "FR", // Sanofi
	"SAP":  "DE", // SAP
	"SHOP": "CA", // Shopify
	"TD":   "CA", // Toronto-Dominion
	"RY":   "CA", // Royal Bank of Canada
	"BP":   "UK", // BP plc
	"SHEL": "UK", // Shell plc
	"AZN":  "UK", // AstraZeneca
	"UL":   "UK", // Unilever
	"ASML": "NL", // ASML Holding
	"KB":   "KR", // KB Financial
}

// GetCountryForTicker returns the ISO-2 country code for a given ticker.
// Returns "US" for tickers not in the ADR map (assumes domestic US listing).
func GetCountryForTicker(ticker string) string {
	upper := strings.ToUpper(ticker)
	if country, ok := knownADRCountries[upper]; ok {
		return country
	}
	return "US"
}

// countryRiskConfig represents the parsed country_risk.json file.
type countryRiskConfig struct {
	CountryRiskPremiums map[string]float64 `json:"country_risk_premiums"`
}

// LoadCountryRiskPremiums loads the country risk premium map from the config file.
// Returns a map of ISO-2 country code -> CRP value.
func LoadCountryRiskPremiums(path string) (map[string]float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read country risk config: %w", err)
	}

	var cfg countryRiskConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse country risk config: %w", err)
	}

	return cfg.CountryRiskPremiums, nil
}

// GetCountryRiskPremium looks up the CRP for a given country code.
// Falls back to the "default" entry if the country is not in the map.
// Returns 0 if neither the country nor default is found.
func GetCountryRiskPremium(premiums map[string]float64, countryCode string) float64 {
	upper := strings.ToUpper(countryCode)

	if crp, ok := premiums[upper]; ok {
		return crp
	}
	if defaultCRP, ok := premiums["default"]; ok {
		return defaultCRP
	}
	return 0
}
