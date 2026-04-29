package valuation

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DefaultADRRatiosConfigPath is the default path to the ADR-ratios config file.
// Mirrors DefaultFXRatesConfigPath / DefaultCountryRiskConfigPath. The path is
// process-relative because the binary is invoked from the project root in
// dev/staging/prod.
const DefaultADRRatiosConfigPath = "./config/adr_ratios.json"

// ADRRatios is the deserialized form of config/adr_ratios.json. Each entry in
// Ratios is the number of ORDINARY (foreign-listed) shares that one ADR
// represents. The ratio matters for FPIs whose SEC filings report ordinary
// shares while market price feeds quote the ADR — naïvely multiplying ordinary
// shares by ADR price over-counts the share base by the ratio.
//
// Tickers absent from the map have an implicit ratio of 1 — the caller looks
// up via Get(), which returns 1 for unknown tickers without erroring. AsOf
// and Source are preserved for log/audit attribution but are not consumed
// programmatically.
type ADRRatios struct {
	AsOf   string         `json:"as_of"`
	Source string         `json:"source"`
	Ratios map[string]int `json:"ratios"`
}

// LoadADRRatios loads the ADR-to-ordinary-share ratio table from a JSON file.
// Behavior:
//
//   - Missing file is non-fatal — returns an empty (but non-nil) ADRRatios so
//     the valuation service can boot in degraded mode (every ticker treated
//     as 1:1) without a nil-pointer panic. The caller is expected to log a
//     warning for operator visibility.
//   - Malformed JSON IS fatal — returns a non-nil error. Silently ignoring
//     parse failures would mask configuration drift and silently corrupt
//     per-ADR valuations in production.
//
// Mirrors LoadFXRates in fx_config.go and LoadCountryRiskPremiums in adr.go.
func LoadADRRatios(path string) (*ADRRatios, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		// Missing file is non-fatal — return empty struct so downstream code
		// does not need to nil-check. The DI layer logs a warning.
		if os.IsNotExist(err) {
			return &ADRRatios{Ratios: map[string]int{}}, nil
		}
		return nil, fmt.Errorf("failed to read ADR ratios config: %w", err)
	}

	var ratios ADRRatios
	if err := json.Unmarshal(data, &ratios); err != nil {
		return nil, fmt.Errorf("failed to parse ADR ratios config: %w", err)
	}

	// Defensive: ensure the map is non-nil even if the JSON had no "ratios"
	// key. Callers iterate (and Get() reads from) this map directly.
	if ratios.Ratios == nil {
		ratios.Ratios = map[string]int{}
	}

	return &ratios, nil
}

// Get returns the ordinary-shares-per-ADR ratio for the given ticker. Lookup
// is case-insensitive (the key is uppercased on lookup). Returns 1 in three
// defensive cases:
//
//  1. Receiver is nil — keeps Phase B10 callers free of nil checks.
//  2. Ticker is absent from the map — domestic 10-K filers effectively have
//     a 1:1 ratio.
//  3. The configured ratio is <= 0 — treated as bad config and downgraded
//     to 1 to guarantee a positive divisor downstream.
//
// The contract guarantees: Get always returns a positive int.
func (a *ADRRatios) Get(ticker string) int {
	if a == nil || a.Ratios == nil {
		return 1
	}
	if r, ok := a.Ratios[strings.ToUpper(ticker)]; ok && r > 0 {
		return r
	}
	return 1
}
