package valuation

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// deviationThresholdHigh is the upper bound multiplier. DCF-implied multiples
// above 2x the sector median are flagged.
const deviationThresholdHigh = 2.0

// deviationThresholdLow is the lower bound multiplier. DCF-implied multiples
// below 0.5x the sector median are flagged.
const deviationThresholdLow = 0.5

// CalculateSanityCheck compares DCF-derived valuation against sector median multiples.
// Returns an entities.SanityCheck that can be attached to ValuationResult.
//
// Parameters:
//   - dcfEquityValue: total equity value from DCF model
//   - enterpriseValue: total enterprise value from DCF model
//   - eps: earnings per share (net income / shares outstanding)
//   - ebitda: EBITDA for the most recent period
//   - sharesOutstanding: diluted shares outstanding
//   - industry: industry code for sector median lookup
//   - sectorPE: sector median P/E ratio (from config)
//   - sectorEVEBITDA: sector median EV/EBITDA ratio (from config)
func CalculateSanityCheck(
	dcfEquityValue, enterpriseValue float64,
	eps, ebitda float64,
	sharesOutstanding float64,
	industry string,
	sectorPE, sectorEVEBITDA float64,
) *entities.SanityCheck {
	check := &entities.SanityCheck{
		SectorMedianPE:       sectorPE,
		SectorMedianEVEBITDA: sectorEVEBITDA,
		IsReasonable:         true,
		Flags:                []string{},
	}

	// Calculate implied P/E: DCF value per share / EPS
	if eps > 0 {
		dcfValuePerShare := dcfEquityValue / sharesOutstanding
		check.ImpliedPE = dcfValuePerShare / eps

		// Flag if implied P/E deviates significantly from sector median
		if sectorPE > 0 {
			ratio := check.ImpliedPE / sectorPE
			if ratio > deviationThresholdHigh {
				check.IsReasonable = false
				check.Flags = append(check.Flags,
					fmt.Sprintf("DCF-implied P/E (%.1f) is >2x sector median (%.1f) for %s",
						check.ImpliedPE, sectorPE, industry))
			} else if ratio < deviationThresholdLow {
				check.IsReasonable = false
				check.Flags = append(check.Flags,
					fmt.Sprintf("DCF-implied P/E (%.1f) is <0.5x sector median (%.1f) for %s",
						check.ImpliedPE, sectorPE, industry))
			}
		}
	}

	// Calculate implied EV/EBITDA: Enterprise Value / EBITDA
	if ebitda > 0 {
		check.ImpliedEVEBITDA = enterpriseValue / ebitda

		// Flag if implied EV/EBITDA deviates significantly from sector median
		if sectorEVEBITDA > 0 {
			ratio := check.ImpliedEVEBITDA / sectorEVEBITDA
			if ratio > deviationThresholdHigh {
				check.IsReasonable = false
				check.Flags = append(check.Flags,
					fmt.Sprintf("DCF-implied EV/EBITDA (%.1f) is >2x sector median (%.1f) for %s",
						check.ImpliedEVEBITDA, sectorEVEBITDA, industry))
			} else if ratio < deviationThresholdLow {
				check.IsReasonable = false
				check.Flags = append(check.Flags,
					fmt.Sprintf("DCF-implied EV/EBITDA (%.1f) is <0.5x sector median (%.1f) for %s",
						check.ImpliedEVEBITDA, sectorEVEBITDA, industry))
			}
		}
	}

	return check
}

// industryMultiplesConfig represents the parsed industry_multiples.json file,
// extended with EV/EBITDA and P/E multiples for sanity cross-checks.
type industryMultiplesConfig struct {
	EVEBITDAMultiples map[string]float64 `json:"ev_ebitda_multiples"`
	SectorMedianPE    map[string]float64 `json:"sector_median_pe"`
}

// LoadIndustryMultiples loads EV/EBITDA and P/E multiples from the config file.
func LoadIndustryMultiples(path string) (*industryMultiplesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read industry multiples config: %w", err)
	}

	var cfg industryMultiplesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse industry multiples config: %w", err)
	}

	return &cfg, nil
}

// LookupMultiple finds the appropriate multiple for an industry code.
// Tries exact match first, then prefix match, then default.
func LookupMultiple(multiples map[string]float64, industry string) float64 {
	upper := strings.ToUpper(industry)

	// Exact match
	if val, ok := multiples[upper]; ok {
		return val
	}

	// Prefix match (e.g., "TECH_SAAS" -> "TECH")
	for code, val := range multiples {
		if strings.HasPrefix(upper, code) && code != "default" {
			return val
		}
	}

	// Default fallback
	if val, ok := multiples["default"]; ok {
		return val
	}

	return 0
}

// isDeviationReasonable checks if the ratio between implied and median is within bounds.
func isDeviationReasonable(implied, median float64) bool {
	if median <= 0 || implied <= 0 {
		return true // Can't evaluate, assume ok
	}
	ratio := implied / median
	return ratio >= deviationThresholdLow && ratio <= deviationThresholdHigh
}
