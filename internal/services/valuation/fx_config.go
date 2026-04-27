package valuation

import (
	"encoding/json"
	"fmt"
	"os"
)

// DefaultFXRatesConfigPath is the default path to the FX rates config file.
// Mirrors DefaultCountryRiskConfigPath. The path is process-relative because
// the binary is invoked from the project root in dev/staging/prod.
const DefaultFXRatesConfigPath = "./config/fx_rates.json"

// FXRates is the deserialized form of config/fx_rates.json. It captures a
// manually-curated FRED H.10 snapshot used as a fallback when the live FRED
// daily-FX series are unavailable. Only RatesToUSD is consumed by the macro
// gateway (Phase B7); AsOf and Source are preserved so logs and audit trails
// can attribute the value to a specific snapshot date.
//
// All rates express USD per 1 unit of the foreign currency (e.g.
// TWD: 0.0312 means 1 TWD = 0.0312 USD). USD itself is pinned at 1.0 to keep
// cross-currency lookups symmetric and to avoid a special case in callers.
type FXRates struct {
	AsOf       string             `json:"as_of"`
	Source     string             `json:"source"`
	RatesToUSD map[string]float64 `json:"rates_to_usd"`
}

// LoadFXRates loads the static FX-rate snapshot from a JSON file. Behavior:
//
//   - Missing file is non-fatal — returns an empty (but non-nil) FXRates so
//     the caller can continue running. The macro gateway treats an empty map
//     as "no static fallback available", which causes ErrFXRateUnavailable
//     to be surfaced when FRED is also down.
//   - Malformed JSON IS fatal — returns a non-nil error. Silently ignoring
//     parse failures would mask configuration drift and degrade FX behavior
//     in production without operator awareness.
//
// Mirrors LoadCountryRiskPremiums in adr.go.
func LoadFXRates(path string) (*FXRates, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		// Missing file is non-fatal — return empty struct so downstream
		// code does not need to nil-check. The DI layer logs a warning.
		if os.IsNotExist(err) {
			return &FXRates{RatesToUSD: map[string]float64{}}, nil
		}
		return nil, fmt.Errorf("failed to read FX rates config: %w", err)
	}

	var rates FXRates
	if err := json.Unmarshal(data, &rates); err != nil {
		return nil, fmt.Errorf("failed to parse FX rates config: %w", err)
	}

	// Defensive: ensure the map is non-nil even if the JSON had no
	// "rates_to_usd" key. Callers iterate this map directly.
	if rates.RatesToUSD == nil {
		rates.RatesToUSD = map[string]float64{}
	}

	return &rates, nil
}
