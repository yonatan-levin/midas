package models

import (
	"encoding/json"
	"fmt"

	configfs "github.com/midas/dcf-valuation-api/config"
)

// RM-2 Phase 2: SIC-driven Damodaran sector EV/Sales lookup. This file owns the
// pure lookup primitive plus the two embedded-config loaders. It is the primary
// source for the revenue_multiple model's EV/Revenue multiple; the Phase 1
// industry_multiples.json buckets remain the additive fallback (see
// revenue_multiple.go::resolveMultiple).

// lookupDamodaranMultiple resolves a raw SEC SIC code to a Damodaran EV/Sales
// multiple via the crosswalk. It returns ok=false (and zero values) when:
//   - sic is empty,
//   - sic is not present in sicToDamodaran, or
//   - the mapped industry name is missing from the damodaran table (a dangling
//     crosswalk entry — the CI integrity gate prevents this at build time, but
//     this runtime guard keeps the lookup safe rather than panicking).
//
// Pure: no clock, no I/O. Returns the resolved Damodaran industry name so
// callers can build provenance strings and warnings.
func lookupDamodaranMultiple(
	sic string,
	sicToDamodaran map[string]string,
	damodaran map[string]float64,
) (multiple float64, industry string, ok bool) {
	if sic == "" || len(sicToDamodaran) == 0 || len(damodaran) == 0 {
		return 0, "", false
	}
	name, mapped := sicToDamodaran[sic]
	if !mapped {
		return 0, "", false
	}
	value, present := damodaran[name]
	if !present {
		// Dangling crosswalk entry: SIC maps to an industry name absent from
		// the table. Degrade to ok=false so the caller falls back to Phase 1.
		return 0, "", false
	}
	return value, name, true
}

// loadDamodaranMultiples loads the Damodaran industry -> EV/Sales table from the
// embedded damodaran_sector_multiples.json, returning the map and the dataset
// date. Mirrors loadEVRevenueMultiples' embedded-read + error-wrap pattern.
func loadDamodaranMultiples() (map[string]float64, string, error) {
	data, err := configfs.Read("damodaran_sector_multiples.json")
	if err != nil {
		return nil, "", fmt.Errorf("failed to read embedded damodaran multiples config: %w", err)
	}
	var cfg struct {
		DatasetDate string             `json:"dataset_date"`
		Industries  map[string]float64 `json:"industries"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, "", fmt.Errorf("failed to parse damodaran multiples config: %w", err)
	}
	return cfg.Industries, cfg.DatasetDate, nil
}

// loadSICToDamodaran loads the SIC -> Damodaran-industry crosswalk from the
// embedded sic_to_damodaran.json.
func loadSICToDamodaran() (map[string]string, error) {
	data, err := configfs.Read("sic_to_damodaran.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded sic-to-damodaran crosswalk: %w", err)
	}
	var cfg struct {
		Map map[string]string `json:"map"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse sic-to-damodaran crosswalk: %w", err)
	}
	return cfg.Map, nil
}
