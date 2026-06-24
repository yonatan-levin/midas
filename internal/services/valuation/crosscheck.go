package valuation

import (
	"encoding/json"
	"fmt"

	configfs "github.com/midas/dcf-valuation-api/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/thresholds"
)

// DeviationThresholdHigh re-exports thresholds.DeviationHigh so external
// callers that read valuation.DeviationThresholdHigh keep compiling.
// New code should import the thresholds package directly.
const DeviationThresholdHigh = thresholds.DeviationHigh

// DeviationThresholdLow re-exports thresholds.DeviationLow (see above).
const DeviationThresholdLow = thresholds.DeviationLow

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
		// VAL-1 Phase 4 (benign circularity note): when the resolved profile's
		// terminal_method == "exit_multiple", the DCF terminal value is BLENDED
		// 50/50 with a sector EV/EBITDA multiple, so enterpriseValue is partly
		// DEFINED by that sector multiple. The implied EV/EBITDA below is therefore
		// pulled TOWARD the sector median — i.e. the circularity makes a SPURIOUS
		// divergence flag LESS likely, never more (the safe direction). We do NOT
		// de-circularize here: the crosscheck remains a sanity heuristic, and a
		// blended terminal that converges toward the sector median is exactly the
		// reconciliation the exit-multiple terminal intends. Pinned by
		// TestService_Crosscheck_ExitMultipleProfile_NoSpuriousEVEBITDAFlag.
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

// LoadIndustryMultiples parses the embedded industry_multiples.json. The
// path parameter is deprecated and ignored — kept so existing call sites
// that still pass a path keep compiling. Pass "" for new call sites.
func LoadIndustryMultiples(_ string) (*industryMultiplesConfig, error) {
	data, err := configfs.Read("industry_multiples.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded industry multiples config: %w", err)
	}

	var cfg industryMultiplesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse industry multiples config: %w", err)
	}

	return &cfg, nil
}

// LookupMultiple finds the appropriate multiple for an industry code: exact
// match, then longest prefix match at an underscore boundary (the W-4
// deterministic core, shared via models.LookupByLongestPrefix — SR-1 A10),
// then the "default" key, then 0.
func LookupMultiple(multiples map[string]float64, industry string) float64 {
	if val, ok := models.LookupByLongestPrefix(multiples, industry); ok {
		return val
	}
	if val, ok := multiples["default"]; ok {
		return val
	}
	return 0
}
