package replay

import (
	"fmt"
	"strings"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// compareFairValueResponses walks two FairValueResponse values field by
// field, classifying each non-equal field into a FloatDiff (numeric, with
// tolerance check) or StringDiff (everything else).
//
// Stage G replaces this hand-rolled walker with go-cmp + a custom
// reporter. Until then we mirror the production handler's projection at
// fair_value.go:376-399 manually so every public field of FairValueResponse
// is checked. New fields added to FairValueResponse must be added here too
// — failure to do so means that field becomes invisible to drift detection,
// a regression flagged in the spec §11 REVIEWER audit list.
//
// Path naming uses snake_case matching the JSON tags so a diff entry's
// path is directly addressable in jq / structured grep over the bundle's
// 17-response.json.
func compareFairValueResponses(bundle, current *handlers.FairValueResponse, relTol, absTol float64) *ResultDiff {
	d := &ResultDiff{}
	if bundle == nil && current == nil {
		return d
	}
	if bundle == nil || current == nil {
		// One missing entirely — record a single sentinel string diff so
		// the renderer surfaces the situation; a per-field walk would be
		// uninformative.
		d.Strings = append(d.Strings, StringDiff{Path: "$root", Old: stringOrNil(bundle), New: stringOrNil(current)})
		d.FieldsTotal = 1
		return d
	}

	// String-valued fields. Compared by equality; mismatches captured
	// as StringDiff.
	stringFields := []struct {
		path  string
		bv    string
		cv    string
		count bool // count toward FieldsTotal even when absent on both sides
	}{
		{"ticker", bundle.Ticker, current.Ticker, true},
		{"growth_source", bundle.GrowthSource, current.GrowthSource, true},
		{"growth_confidence", bundle.GrowthConfidence, current.GrowthConfidence, true},
		{"as_of", bundle.AsOf, current.AsOf, true},
		{"data_quality_grade", bundle.DataQualityGrade, current.DataQualityGrade, true},
		{"calculation_method", bundle.CalculationMethod, current.CalculationMethod, true},
		{"calculation_version", bundle.CalculationVersion, current.CalculationVersion, true},
		{"currency", bundle.Currency, current.Currency, true},
		// Tier 2 P0b additive fields. omitempty on the wire — pre-Tier-2
		// bundles unmarshal to "" so the comparison is honest about
		// "absent in bundle, present in current" via the StringDiff path.
		{"assumption_profile", bundle.AssumptionProfile, current.AssumptionProfile, true},
		{"dcf_terminal_method", bundle.DCFTerminalMethod, current.DCFTerminalMethod, true},
	}
	for _, f := range stringFields {
		d.FieldsTotal++
		if f.bv != f.cv {
			d.Strings = append(d.Strings, StringDiff{Path: f.path, Old: f.bv, New: f.cv})
		}
	}

	// Float-valued top-level fields.
	floatFields := []struct {
		path string
		bv   float64
		cv   float64
	}{
		{"wacc", bundle.WACC, current.WACC},
		{"growth_rate", bundle.GrowthRate, current.GrowthRate},
		{"tangible_value_per_share", bundle.TangibleValuePerShare, current.TangibleValuePerShare},
		{"dcf_value_per_share", bundle.DCFValuePerShare, current.DCFValuePerShare},
		{"data_quality_score", bundle.DataQualityScore, current.DataQualityScore},
		{"current_price", bundle.CurrentPrice, current.CurrentPrice},
		// Tier 2 P0b additive DCF diagnostics (P2 fills these; P0b ships
		// the schema). omitempty on the wire keeps pre-Tier-2 bundle
		// JSON byte-equal until populated.
		{"dcf_terminal_pct_of_ev", bundle.DCFTerminalPctOfEV, current.DCFTerminalPctOfEV},
		{"dcf_terminal_growth_used", bundle.DCFTerminalGrowthUsed, current.DCFTerminalGrowthUsed},
	}
	for _, f := range floatFields {
		d.FieldsTotal++
		fd := FloatDiffOf(f.path, f.bv, f.cv, relTol, absTol)
		if !fd.WithinTolerance {
			d.Floats = append(d.Floats, fd)
		} else if fd.AbsDrift > 0 {
			// Drifted but within tolerance — surface only via verbose mode.
			d.FloatsWithinTolerance = append(d.FloatsWithinTolerance, fd)
		}
	}

	// Integer-as-float: ADRRatioApplied. Compare exactly (no tolerance).
	d.FieldsTotal++
	if bundle.ADRRatioApplied != current.ADRRatioApplied {
		d.Strings = append(d.Strings, StringDiff{
			Path: "adr_ratio_applied",
			Old:  fmt.Sprintf("%d", bundle.ADRRatioApplied),
			New:  fmt.Sprintf("%d", current.ADRRatioApplied),
		})
	}

	// Tier 2 P0b: DCFHorizonYears (int, exact compare — no tolerance).
	d.FieldsTotal++
	if bundle.DCFHorizonYears != current.DCFHorizonYears {
		d.Strings = append(d.Strings, StringDiff{
			Path: "dcf_horizon_years",
			Old:  fmt.Sprintf("%d", bundle.DCFHorizonYears),
			New:  fmt.Sprintf("%d", current.DCFHorizonYears),
		})
	}

	// GrowthRates slice — compare element-wise. Length mismatch is a
	// single string diff (per-element diff would be noisy).
	d.FieldsTotal++
	if len(bundle.GrowthRates) != len(current.GrowthRates) {
		d.Strings = append(d.Strings, StringDiff{
			Path: "growth_rates.len",
			Old:  fmt.Sprintf("%d", len(bundle.GrowthRates)),
			New:  fmt.Sprintf("%d", len(current.GrowthRates)),
		})
	} else {
		for i := range bundle.GrowthRates {
			d.FieldsTotal++
			path := fmt.Sprintf("growth_rates[%d]", i)
			fd := FloatDiffOf(path, bundle.GrowthRates[i], current.GrowthRates[i], relTol, absTol)
			if !fd.WithinTolerance {
				d.Floats = append(d.Floats, fd)
			} else if fd.AbsDrift > 0 {
				d.FloatsWithinTolerance = append(d.FloatsWithinTolerance, fd)
			}
		}
	}

	// Tier 2 P2 (closes T2-P0b-1): DCFPerYearPV slice — compare element-wise
	// just like GrowthRates. P0b declared the field; P2 populates it. Without
	// this walker extension, drift in the per-year explicit-period PVs would
	// silently bypass Replay() regression detection, masking issues like
	// growth-curve misindexing or off-by-one horizon handling. omitempty on
	// the wire means pre-Tier-2 bundles unmarshal to a nil slice — that
	// matches a nil current slice as length=0 here, so no false positives.
	d.FieldsTotal++
	if len(bundle.DCFPerYearPV) != len(current.DCFPerYearPV) {
		d.Strings = append(d.Strings, StringDiff{
			Path: "dcf_per_year_pv.len",
			Old:  fmt.Sprintf("%d", len(bundle.DCFPerYearPV)),
			New:  fmt.Sprintf("%d", len(current.DCFPerYearPV)),
		})
	} else {
		for i := range bundle.DCFPerYearPV {
			d.FieldsTotal++
			path := fmt.Sprintf("dcf_per_year_pv[%d]", i)
			fd := FloatDiffOf(path, bundle.DCFPerYearPV[i], current.DCFPerYearPV[i], relTol, absTol)
			if !fd.WithinTolerance {
				d.Floats = append(d.Floats, fd)
			} else if fd.AbsDrift > 0 {
				d.FloatsWithinTolerance = append(d.FloatsWithinTolerance, fd)
			}
		}
	}

	// Warnings slice — compare element-wise as strings.
	d.FieldsTotal++
	if len(bundle.Warnings) != len(current.Warnings) {
		d.Strings = append(d.Strings, StringDiff{
			Path: "warnings.len",
			Old:  fmt.Sprintf("%d", len(bundle.Warnings)),
			New:  fmt.Sprintf("%d", len(current.Warnings)),
		})
	} else {
		// Only flag the first per-index difference; equal-set permutations
		// (different ordering) would produce false positives without a
		// set-comparison helper. R3 may upgrade to set-based comparison.
		for i := range bundle.Warnings {
			d.FieldsTotal++
			if bundle.Warnings[i] != current.Warnings[i] {
				d.Strings = append(d.Strings, StringDiff{
					Path: fmt.Sprintf("warnings[%d]", i),
					Old:  bundle.Warnings[i],
					New:  current.Warnings[i],
				})
			}
		}
	}

	// SanityCheck nested struct.
	compareSanityCheck(bundle.SanityCheck, current.SanityCheck, relTol, absTol, d)

	// Industry nested struct.
	compareIndustry(bundle.Industry, current.Industry, d)

	return d
}

// compareSanityCheck recurses into the optional SanityCheck struct and
// appends any per-field diffs to d.
func compareSanityCheck(bundle, current *entities.SanityCheck, relTol, absTol float64, d *ResultDiff) {
	if bundle == nil && current == nil {
		return
	}
	d.FieldsTotal++
	if bundle == nil || current == nil {
		d.Strings = append(d.Strings, StringDiff{
			Path: "sanity_check",
			Old:  stringOrNilStruct(bundle),
			New:  stringOrNilStruct(current),
		})
		return
	}
	floats := []struct {
		path string
		bv   float64
		cv   float64
	}{
		{"sanity_check.implied_pe", bundle.ImpliedPE, current.ImpliedPE},
		{"sanity_check.implied_ev_ebitda", bundle.ImpliedEVEBITDA, current.ImpliedEVEBITDA},
		{"sanity_check.implied_pfcf", bundle.ImpliedPFCF, current.ImpliedPFCF},
		{"sanity_check.sector_median_pe", bundle.SectorMedianPE, current.SectorMedianPE},
		{"sanity_check.sector_median_ev_ebitda", bundle.SectorMedianEVEBITDA, current.SectorMedianEVEBITDA},
		{"sanity_check.sector_median_pfcf", bundle.SectorMedianPFCF, current.SectorMedianPFCF},
	}
	for _, f := range floats {
		d.FieldsTotal++
		fd := FloatDiffOf(f.path, f.bv, f.cv, relTol, absTol)
		if !fd.WithinTolerance {
			d.Floats = append(d.Floats, fd)
		} else if fd.AbsDrift > 0 {
			d.FloatsWithinTolerance = append(d.FloatsWithinTolerance, fd)
		}
	}
	d.FieldsTotal++
	if bundle.IsReasonable != current.IsReasonable {
		d.Strings = append(d.Strings, StringDiff{
			Path: "sanity_check.is_reasonable",
			Old:  fmt.Sprintf("%t", bundle.IsReasonable),
			New:  fmt.Sprintf("%t", current.IsReasonable),
		})
	}
	// Flags slice: compare element-wise after asserting equal length.
	d.FieldsTotal++
	if len(bundle.Flags) != len(current.Flags) {
		d.Strings = append(d.Strings, StringDiff{
			Path: "sanity_check.flags.len",
			Old:  fmt.Sprintf("%d", len(bundle.Flags)),
			New:  fmt.Sprintf("%d", len(current.Flags)),
		})
	} else {
		for i := range bundle.Flags {
			d.FieldsTotal++
			if bundle.Flags[i] != current.Flags[i] {
				d.Strings = append(d.Strings, StringDiff{
					Path: fmt.Sprintf("sanity_check.flags[%d]", i),
					Old:  bundle.Flags[i],
					New:  current.Flags[i],
				})
			}
		}
	}
}

// compareIndustry recurses into the optional Industry struct.
func compareIndustry(bundle, current *handlers.Industry, d *ResultDiff) {
	if bundle == nil && current == nil {
		return
	}
	d.FieldsTotal++
	if bundle == nil || current == nil {
		d.Strings = append(d.Strings, StringDiff{
			Path: "industry",
			Old:  stringOrNilStruct(bundle),
			New:  stringOrNilStruct(current),
		})
		return
	}
	pairs := []struct {
		path string
		bv   string
		cv   string
	}{
		{"industry.sic_code", bundle.SICCode, current.SICCode},
		{"industry.sic", bundle.SIC, current.SIC},
		{"industry.heuristic_code", bundle.HeuristicCode, current.HeuristicCode},
		{"industry.heuristic_name", bundle.HeuristicName, current.HeuristicName},
	}
	for _, f := range pairs {
		d.FieldsTotal++
		if f.bv != f.cv {
			d.Strings = append(d.Strings, StringDiff{Path: f.path, Old: f.bv, New: f.cv})
		}
	}
	d.FieldsTotal++
	if bundle.Match != current.Match {
		d.Strings = append(d.Strings, StringDiff{
			Path: "industry.match",
			Old:  fmt.Sprintf("%t", bundle.Match),
			New:  fmt.Sprintf("%t", current.Match),
		})
	}
}

// stringOrNil renders a nilable pointer as "nil" or its short type name —
// just enough for a diagnostic StringDiff entry.
func stringOrNil(p interface{}) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", p)
}

// stringOrNilStruct mirrors stringOrNil but accepts pointer-to-struct
// inputs, returning "nil" for the typed-nil case (which would hide
// behind interface{} otherwise).
func stringOrNilStruct(p interface{}) string {
	if p == nil {
		return "nil"
	}
	v := fmt.Sprintf("%v", p)
	if strings.Contains(v, "<nil>") {
		return "nil"
	}
	return "present"
}
