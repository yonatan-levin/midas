package valuation

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// DeviationThresholdHigh is the upper bound multiplier. DCF-implied multiples
// above 2x the sector median are flagged. Exported for use by other valuation
// models (FFO NAV, DDM P/BV) to keep a single source of truth.
const DeviationThresholdHigh = 2.0

// DeviationThresholdLow is the lower bound multiplier. DCF-implied multiples
// below 0.5x the sector median are flagged. Exported for use by other valuation
// models.
const DeviationThresholdLow = 0.5

// Unexported aliases preserved for backward compatibility within the package.
const (
	deviationThresholdHigh = DeviationThresholdHigh
	deviationThresholdLow  = DeviationThresholdLow
)

// FlagDivergence returns a formatted flag string when the implied multiple
// deviates from the sector median by more than 2x or less than 0.5x.
// Returns ("", false) when the inputs are insufficient or the ratio is within bounds.
//
// This is the single source of truth for "DCF-implied X diverges from sector median"
// messaging used across sanity checks (P/E, EV/EBITDA, P/FCF), NAV cross-check, and P/BV.
func FlagDivergence(label, industry string, implied, sector float64) (string, bool) {
	if implied <= 0 || sector <= 0 {
		return "", false
	}
	ratio := implied / sector
	if ratio > DeviationThresholdHigh {
		return fmt.Sprintf("DCF-implied %s (%.1f) is >2x sector median (%.1f) for %s",
			label, implied, sector, industry), true
	}
	if ratio < DeviationThresholdLow {
		return fmt.Sprintf("DCF-implied %s (%.1f) is <0.5x sector median (%.1f) for %s",
			label, implied, sector, industry), true
	}
	return "", false
}

// CalculateSanityCheck compares DCF-derived valuation against sector median multiples
// (P/E, EV/EBITDA, P/FCF). Returns an entities.SanityCheck that can be attached to
// ValuationResult. IsReasonable is derived from whether any flags fired.
func CalculateSanityCheck(
	dcfEquityValue, enterpriseValue float64,
	eps, ebitda, fcfPerShare float64,
	sharesOutstanding float64,
	industry string,
	sectorPE, sectorEVEBITDA, sectorPFCF float64,
) *entities.SanityCheck {
	check := &entities.SanityCheck{
		SectorMedianPE:       sectorPE,
		SectorMedianEVEBITDA: sectorEVEBITDA,
		SectorMedianPFCF:     sectorPFCF,
		Flags:                []string{},
	}

	if sharesOutstanding <= 0 {
		check.IsReasonable = true
		return check
	}
	dcfValuePerShare := dcfEquityValue / sharesOutstanding

	if eps > 0 {
		check.ImpliedPE = dcfValuePerShare / eps
		if flag, ok := FlagDivergence("P/E", industry, check.ImpliedPE, sectorPE); ok {
			check.Flags = append(check.Flags, flag)
		}
	}

	if ebitda > 0 {
		check.ImpliedEVEBITDA = enterpriseValue / ebitda
		if flag, ok := FlagDivergence("EV/EBITDA", industry, check.ImpliedEVEBITDA, sectorEVEBITDA); ok {
			check.Flags = append(check.Flags, flag)
		}
	}

	if fcfPerShare > 0 {
		check.ImpliedPFCF = dcfValuePerShare / fcfPerShare
		if flag, ok := FlagDivergence("P/FCF", industry, check.ImpliedPFCF, sectorPFCF); ok {
			check.Flags = append(check.Flags, flag)
		}
	}

	check.IsReasonable = len(check.Flags) == 0
	return check
}

// industryMultiplesConfig represents the parsed industry_multiples.json file.
// All consumers (sanity check, FFO, RevenueMultiple) use this single struct —
// the file is loaded ONCE at startup, not per-model.
type industryMultiplesConfig struct {
	EVEBITDAMultiples  map[string]float64 `json:"ev_ebitda_multiples"`
	SectorMedianPE     map[string]float64 `json:"sector_median_pe"`
	SectorMedianPFCF   map[string]float64 `json:"sector_median_pfcf"`
	EVRevenueMultiples map[string]float64 `json:"ev_revenue_multiples"`
	REITPFFOMultiples  map[string]float64 `json:"reit_pffo_multiples"`
	REITCapRates       map[string]float64 `json:"reit_cap_rates"`
}

// LoadIndustryMultiples loads all sector multiples from a single config file read.
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
// Tries exact match first, then longest prefix match, then default.
// Longest-prefix-match avoids nondeterminism from Go's random map iteration order.
func LookupMultiple(multiples map[string]float64, industry string) float64 {
	upper := strings.ToUpper(industry)

	if val, ok := multiples[upper]; ok {
		return val
	}

	bestKey := ""
	bestVal := 0.0
	for code, val := range multiples {
		if code != "default" && strings.HasPrefix(upper, code) && len(code) > len(bestKey) {
			bestKey = code
			bestVal = val
		}
	}
	if bestKey != "" {
		return bestVal
	}

	if val, ok := multiples["default"]; ok {
		return val
	}

	return 0
}

// isDeviationReasonable checks if the ratio between implied and median is within bounds.
func isDeviationReasonable(implied, median float64) bool {
	if median <= 0 || implied <= 0 {
		return true
	}
	ratio := implied / median
	return ratio >= deviationThresholdLow && ratio <= deviationThresholdHigh
}
