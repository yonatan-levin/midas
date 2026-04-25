package industry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	configfs "github.com/midas/dcf-valuation-api/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// subIndustryMapping represents a sub-industry classification (e.g., TECH_SAAS within TECH).
type subIndustryMapping struct {
	Name     string `json:"name"`
	Code     string `json:"code"`
	Matchers struct {
		Keywords   []string `json:"keywords"`
		SICCodes   []string `json:"sic_codes"`
		NAICSCodes []string `json:"naics_codes"`
		Patterns   []string `json:"patterns"`
	} `json:"matchers"`

	// Pre-compiled regexes, populated during LoadIndustryCodesConfig.
	// These avoid recompiling on every Classify() call (W-2).
	compiledKeywords []*regexp.Regexp `json:"-"`
	compiledPatterns []*regexp.Regexp `json:"-"`
}

// industryMapping represents a single industry mapping entry from config/datacleaner/industry_codes.json.
// Each entry maps SIC codes, NAICS codes, and company name keywords to an industry code.
type industryMapping struct {
	Name     string `json:"name"`
	Code     string `json:"code"`
	Priority int    `json:"priority"`
	Matchers struct {
		SICCodes   []string `json:"sic_codes"`
		NAICSCodes []string `json:"naics_codes"`
		Keywords   []string `json:"keywords"`
		Patterns   []string `json:"patterns"`
		ExactNames []string `json:"exact_names"`
	} `json:"matchers"`
	SubIndustries []subIndustryMapping `json:"sub_industries"`

	// Pre-compiled regexes, populated during LoadIndustryCodesConfig.
	// These avoid recompiling on every Classify() call (W-2).
	compiledKeywords []*regexp.Regexp `json:"-"`
	compiledPatterns []*regexp.Regexp `json:"-"`
}

// industryCodesConfig represents the full industry_codes.json file structure
type industryCodesConfig struct {
	Version     string            `json:"version"`
	DefaultCode string            `json:"default_code"`
	Mappings    []industryMapping `json:"mappings"`
}

// IndustryClassifier provides enhanced industry classification logic.
// It supports two classification approaches:
//   - Classify(): SIC/NAICS code and company name based classification (Phase 3)
//   - ClassifyIndustry(): Financial heuristic based classification (original)
type IndustryClassifier struct {
	sectorConfigs map[string]*SectorConfig
	codesConfig   *industryCodesConfig // loaded from industry_codes.json
}

// SectorConfig defines industry-specific configuration and thresholds
type SectorConfig struct {
	SectorCode        string                  `json:"sector_code"`        // GICS sector code
	SectorName        string                  `json:"sector_name"`        // Human-readable name
	SubIndustries     []string                `json:"sub_industries"`     // List of sub-industry codes
	RiskProfile       RiskProfile             `json:"risk_profile"`       // Industry risk characteristics
	Thresholds        IndustryThresholds      `json:"thresholds"`         // Industry-specific thresholds
	CommonAdjustments []string                `json:"common_adjustments"` // Typical adjustments for this industry
	KeyMetrics        []string                `json:"key_metrics"`        // Important metrics to monitor
	Characteristics   IndustryCharacteristics `json:"characteristics"`    // Industry-specific traits
}

// RiskProfile defines risk characteristics for an industry
type RiskProfile struct {
	CyclicalityRisk    RiskLevel `json:"cyclicality_risk"`     // Economic cycle sensitivity
	RegulatoryRisk     RiskLevel `json:"regulatory_risk"`      // Regulatory change risk
	TechnologyRisk     RiskLevel `json:"technology_risk"`      // Technology disruption risk
	CompetitiveRisk    RiskLevel `json:"competitive_risk"`     // Competitive pressure risk
	CapitalIntensity   RiskLevel `json:"capital_intensity"`    // Capital requirements
	WorkingCapitalRisk RiskLevel `json:"working_capital_risk"` // Working capital volatility
}

// RiskLevel defines risk intensity levels
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// IndustryThresholds defines industry-specific adjustment thresholds
type IndustryThresholds struct {
	// Asset Adjustment Thresholds
	GoodwillThreshold         float64 `json:"goodwill_threshold"`          // % of total assets
	IntangibleThreshold       float64 `json:"intangible_threshold"`        // % of total assets
	InventoryObsolescenceRate float64 `json:"inventory_obsolescence_rate"` // % haircut for dead inventory
	DeferredTaxThreshold      float64 `json:"deferred_tax_threshold"`      // % of total assets

	// Liability Adjustment Thresholds
	OperatingLeaseRate      float64 `json:"operating_lease_rate"`      // Discount rate for lease capitalization
	PensionFundingThreshold float64 `json:"pension_funding_threshold"` // Underfunding % threshold
	ContingentLiabilityRate float64 `json:"contingent_liability_rate"` // Probability weighting

	// Earnings Adjustment Thresholds
	RestructuringThreshold float64 `json:"restructuring_threshold"` // % of revenue threshold
	StockCompThreshold     float64 `json:"stock_comp_threshold"`    // % of revenue threshold
	LitigationThreshold    float64 `json:"litigation_threshold"`    // % of revenue threshold

	// Quality Score Adjustments
	QualityScoreAdjustment float64 `json:"quality_score_adjustment"` // Industry-specific quality adjustment
	MinimumQualityScore    float64 `json:"minimum_quality_score"`    // Industry minimum quality threshold
}

// IndustryCharacteristics defines unique traits of an industry
type IndustryCharacteristics struct {
	AssetHeavy            bool     `json:"asset_heavy"`             // High PP&E relative to revenue
	InventoryIntensive    bool     `json:"inventory_intensive"`     // High inventory levels
	IntangibleIntensive   bool     `json:"intangible_intensive"`    // High intangible assets
	RegulatedIndustry     bool     `json:"regulated_industry"`      // Subject to regulatory oversight
	CyclicalEarnings      bool     `json:"cyclical_earnings"`       // Earnings vary with economic cycles
	HighRDIntensity       bool     `json:"high_rd_intensity"`       // High R&D spending
	LongTermContracts     bool     `json:"long_term_contracts"`     // Revenue from long-term contracts
	SeasonalBusiness      bool     `json:"seasonal_business"`       // Seasonal revenue patterns
	HighStockCompensation bool     `json:"high_stock_compensation"` // Above-average stock compensation
	FrequentRestructuring bool     `json:"frequent_restructuring"`  // Regular restructuring activities
	TypicalAdjustments    []string `json:"typical_adjustments"`     // Common adjustment types
}

// DefaultIndustryCodesPath is the default path to the industry codes config file.
// Override via dependency injection for testability.
const DefaultIndustryCodesPath = "./config/datacleaner/industry_codes.json"

// Heuristic classification thresholds shared across the predicate functions in
// this file. Centralized so tuning stays consistent between the tech detector
// and the retail-exclusion guards.
const (
	// retailRnDCeilingPctRevenue — R&D-to-revenue above this excludes retail.
	// Retailers do not run R&D labs; anything above 5% signals tech/growth.
	retailRnDCeilingPctRevenue = 0.05

	// retailSBCCeilingPctRevenue — SBC-to-revenue above this excludes retail.
	// Retailers pay cash, not equity; anything above 5% signals tech/growth.
	retailSBCCeilingPctRevenue = 0.05

	// techSBCFloorPctRevenue — SBC-to-revenue above this signals tech.
	// Kept numerically equal to the retail SBC ceiling so the two heuristics
	// partition cleanly: if SBC excludes retail, it also qualifies tech.
	techSBCFloorPctRevenue = 0.05
)

// NewIndustryClassifier creates a new industry classifier with configurations
// loaded from the embedded config/datacleaner/industry_codes.json (see
// config/configfs). No filesystem I/O in production — safe in any working
// directory.
//
// Tests that need a custom override call LoadIndustryCodesConfig with a path.
func NewIndustryClassifier() *IndustryClassifier {
	classifier := &IndustryClassifier{
		sectorConfigs: make(map[string]*SectorConfig),
	}

	classifier.loadDefaultConfigurations()

	// Gracefully degrade if the embed read fails — keyword-only matching
	// still works. In practice the embed is always present in the binary.
	_ = classifier.loadEmbeddedIndustryCodes()

	return classifier
}

// loadEmbeddedIndustryCodes reads datacleaner/industry_codes.json from the
// compiled-in configfs and populates the classifier's codesConfig. Kept
// private so tests keep using the explicit-path LoadIndustryCodesConfig.
func (ic *IndustryClassifier) loadEmbeddedIndustryCodes() error {
	data, err := configfs.Read("datacleaner/industry_codes.json")
	if err != nil {
		return fmt.Errorf("failed to read embedded industry codes config: %w", err)
	}
	var cfg industryCodesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse industry codes config: %w", err)
	}
	compileCodesConfig(&cfg)
	ic.codesConfig = &cfg
	return nil
}

// LoadIndustryCodesConfig loads the industry_codes.json file for SIC/NAICS/keyword classification.
// Pre-compiles all keyword and pattern regexes at load time so Classify() reuses them
// instead of compiling on every call (W-2).
func (ic *IndustryClassifier) LoadIndustryCodesConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read industry codes config: %w", err)
	}

	var cfg industryCodesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse industry codes config: %w", err)
	}

	compileCodesConfig(&cfg)
	ic.codesConfig = &cfg
	return nil
}

// ensureCompiled lazily compiles regexes if they haven't been compiled yet.
// Handles test scenarios where codesConfig is set directly without LoadIndustryCodesConfig.
// The check looks at whether ANY mapping with keywords/patterns has compiled slices
// of the expected length — a cheap heuristic that avoids recompiling on every call.
func (ic *IndustryClassifier) ensureCompiled() {
	if ic.codesConfig == nil {
		return
	}
	for i := range ic.codesConfig.Mappings {
		m := &ic.codesConfig.Mappings[i]
		// If keyword/pattern slices don't match the counts, we haven't compiled yet
		if len(m.compiledKeywords) != len(m.Matchers.Keywords) ||
			len(m.compiledPatterns) != len(m.Matchers.Patterns) {
			compileCodesConfig(ic.codesConfig)
			return
		}
	}
}

// compileCodesConfig pre-compiles all keyword and pattern regexes for parent industries
// and sub-industries. Idempotent — safe to call multiple times.
func compileCodesConfig(cfg *industryCodesConfig) {
	for i := range cfg.Mappings {
		m := &cfg.Mappings[i]
		m.compiledKeywords = compileKeywordRegexes(m.Matchers.Keywords)
		m.compiledPatterns = compilePatternRegexes(m.Matchers.Patterns)
		for j := range m.SubIndustries {
			sub := &m.SubIndustries[j]
			sub.compiledKeywords = compileKeywordRegexes(sub.Matchers.Keywords)
			sub.compiledPatterns = compilePatternRegexes(sub.Matchers.Patterns)
		}
	}
}

// compileKeywordRegexes pre-compiles word-boundary regexes for short keywords only.
// Long keywords (> 3 chars) use simple strings.Contains in Classify(), so no regex needed.
// Returns a slice parallel to the input — entries for long keywords are nil.
func compileKeywordRegexes(keywords []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(keywords))
	for i, keyword := range keywords {
		lower := strings.ToLower(keyword)
		if len(lower) <= 3 {
			// Word-boundary regex prevents false positives (e.g., "ai" in "retail")
			re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(lower) + `\b`)
			if err == nil {
				out[i] = re
			}
		}
	}
	return out
}

// compilePatternRegexes pre-compiles full regex patterns.
// Invalid patterns are silently skipped (entry left nil).
func compilePatternRegexes(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		re, err := regexp.Compile("(?i)" + pattern)
		if err == nil {
			out[i] = re
		}
	}
	return out
}

// ClassificationResult is the full classifier output. Surfaced on the
// "industry_classification" calc trace per docs/refactoring/observability-
// upgrade-spec.md §Phase M trace points table (M-1b).
//
// Field semantics:
//   - Sector    : top-level (parent) industry code from industry_codes.json
//     (e.g. "TECH", "FIN", "HEALTH"). Equal to Industry when no
//     sub-industry matched.
//   - Industry  : most-specific industry code emitted (e.g. "TECH_SAAS",
//     "HEALTH_BIOTECH"). Equal to Sector when no sub-industry matched.
//   - SubIndustry: the sub-industry code when one matched (e.g. "TECH_SAAS"),
//     empty string otherwise.
//   - ModelHint : the code used by ModelRouter.SelectModel to pick a
//     valuation model. Currently equal to Industry — kept as a
//     distinct field so the model-routing key can diverge from
//     the surface-level industry label without a breaking change.
//   - SIC / NAICS: caller's input echoed back for trace completeness; empty
//     when the caller passed an empty string.
type ClassificationResult struct {
	Sector      string
	Industry    string
	SubIndustry string
	ModelHint   string
	NAICS       string
	SIC         string
}

// Classify determines the industry classification for a company using SIC code,
// NAICS code, and company name. It performs a two-pass classification:
//  1. Parent industry match (TECH, FIN, HEALTH, etc.) — by priority
//  2. Sub-industry refinement within the matched parent (TECH_SAAS, FIN_IB, etc.)
//
// ctx is accepted for future context-aware tracing; it is not used internally but
// allows callers (e.g. valuation.Service) to emit a correlated calc trace after
// this call returns.
//
// Matchers are evaluated in order:
//  1. SIC code (exact or range match)
//  2. NAICS code (prefix match)
//  3. Exact company name match
//  4. Keyword matching on company name (word-boundary for short keywords)
//  5. Regex pattern matching on company name
//
// Returns a ClassificationResult populated with both the parent (Sector) and
// most-specific (Industry) codes plus echoes of the input SIC/NAICS, so callers
// can emit a complete observability trace. On no match the default code ("NA")
// is returned in both Sector and Industry. SIC/NAICS echoes are preserved even
// in the error path so trace completeness survives a missing config.
func (ic *IndustryClassifier) Classify(_ context.Context, sicCode string, naicsCode string, companyName string) (ClassificationResult, error) {
	if ic.codesConfig == nil {
		// Preserve SIC/NAICS echoes on the error path so observability traces
		// stay complete even when the codes config never loaded.
		return ClassificationResult{
			Sector:    "NA",
			Industry:  "NA",
			ModelHint: "NA",
			SIC:       sicCode,
			NAICS:     naicsCode,
		}, fmt.Errorf("industry codes config not loaded")
	}

	// Ensure regexes are compiled — handles the case where tests set codesConfig
	// directly without going through LoadIndustryCodesConfig.
	ic.ensureCompiled()

	lowerName := strings.ToLower(companyName)

	// Pass 1: Find best parent industry by priority
	bestCode := ic.codesConfig.DefaultCode
	bestPriority := -1
	var bestMapping *industryMapping

	for i := range ic.codesConfig.Mappings {
		mapping := &ic.codesConfig.Mappings[i]

		if ic.matchesParent(mapping, sicCode, naicsCode, companyName, lowerName) {
			if mapping.Priority > bestPriority {
				bestCode = mapping.Code
				bestPriority = mapping.Priority
				bestMapping = mapping
			}
		}
	}

	// Default result: no parent match — Sector == Industry == default code.
	result := ClassificationResult{
		Sector:    bestCode,
		Industry:  bestCode,
		ModelHint: bestCode,
		SIC:       sicCode,
		NAICS:     naicsCode,
	}

	// Pass 2: Refine with sub-industry classification within the matched parent (W-3).
	// A sub-industry match returns a more specific code like "TECH_SAAS" instead of "TECH".
	// When matched, Industry/ModelHint are upgraded to the sub-industry code while
	// Sector retains the parent code so callers can surface both.
	if bestMapping != nil {
		if subCode := ic.classifySubIndustry(bestMapping, sicCode, naicsCode, lowerName); subCode != "" {
			result.Industry = subCode
			result.SubIndustry = subCode
			result.ModelHint = subCode
		}
	}

	return result, nil
}

// matchesParent checks if a company matches a parent industry mapping across all matcher types.
// Uses pre-compiled regexes from LoadIndustryCodesConfig to avoid per-call compilation (W-2).
func (ic *IndustryClassifier) matchesParent(mapping *industryMapping, sicCode, naicsCode, companyName, lowerName string) bool {
	// 1. SIC code matching (highest confidence)
	if sicCode != "" && ic.matchSICCode(sicCode, mapping.Matchers.SICCodes) {
		return true
	}

	// 2. NAICS code matching (prefix-based, e.g., "522" matches "52211")
	if naicsCode != "" && ic.matchNAICSCode(naicsCode, mapping.Matchers.NAICSCodes) {
		return true
	}

	// 3. Exact company name matching
	if companyName != "" {
		for _, exactName := range mapping.Matchers.ExactNames {
			if strings.EqualFold(companyName, exactName) {
				return true
			}
		}
	}

	// 4. Keyword matching with pre-compiled regexes for short keywords
	if lowerName != "" && ic.matchKeywords(mapping.Matchers.Keywords, mapping.compiledKeywords, lowerName) {
		return true
	}

	// 5. Regex pattern matching with pre-compiled regexes
	if lowerName != "" && ic.matchPatterns(mapping.compiledPatterns, lowerName) {
		return true
	}

	return false
}

// classifySubIndustry checks if the company matches any sub-industry within the given parent.
// Returns the sub-industry code (e.g., "TECH_SAAS") or empty string if no sub-match.
func (ic *IndustryClassifier) classifySubIndustry(parent *industryMapping, sicCode, naicsCode, lowerName string) string {
	for i := range parent.SubIndustries {
		sub := &parent.SubIndustries[i]

		if sicCode != "" && ic.matchSICCode(sicCode, sub.Matchers.SICCodes) {
			return sub.Code
		}
		if naicsCode != "" && ic.matchNAICSCode(naicsCode, sub.Matchers.NAICSCodes) {
			return sub.Code
		}
		if lowerName != "" && ic.matchKeywords(sub.Matchers.Keywords, sub.compiledKeywords, lowerName) {
			return sub.Code
		}
		if lowerName != "" && ic.matchPatterns(sub.compiledPatterns, lowerName) {
			return sub.Code
		}
	}
	return ""
}

// matchKeywords checks if any keyword matches the company name.
// Uses pre-compiled word-boundary regex for short keywords (<=3 chars),
// and simple strings.Contains for longer keywords.
func (ic *IndustryClassifier) matchKeywords(keywords []string, compiled []*regexp.Regexp, lowerName string) bool {
	for i, keyword := range keywords {
		lowerKeyword := strings.ToLower(keyword)
		if len(lowerKeyword) <= 3 {
			if i < len(compiled) && compiled[i] != nil && compiled[i].MatchString(lowerName) {
				return true
			}
		} else {
			if strings.Contains(lowerName, lowerKeyword) {
				return true
			}
		}
	}
	return false
}

// matchPatterns runs all pre-compiled patterns against the company name.
func (ic *IndustryClassifier) matchPatterns(compiled []*regexp.Regexp, lowerName string) bool {
	for _, re := range compiled {
		if re != nil && re.MatchString(lowerName) {
			return true
		}
	}
	return false
}

// matchSICCode checks if a SIC code matches the mapping's SIC code list.
// Supports both exact codes (e.g., "7372") and ranges (e.g., "2000-3999").
func (ic *IndustryClassifier) matchSICCode(sicCode string, sicCodes []string) bool {
	for _, code := range sicCodes {
		// Handle range format: "2000-3999"
		if strings.Contains(code, "-") {
			parts := strings.SplitN(code, "-", 2)
			if len(parts) == 2 {
				low, errLow := strconv.Atoi(parts[0])
				high, errHigh := strconv.Atoi(parts[1])
				sicInt, errSIC := strconv.Atoi(sicCode)
				if errLow == nil && errHigh == nil && errSIC == nil {
					if sicInt >= low && sicInt <= high {
						return true
					}
				}
			}
			continue
		}

		// Exact match
		if sicCode == code {
			return true
		}
	}
	return false
}

// matchNAICSCode checks if a NAICS code matches via prefix matching.
// For example, NAICS config "522" matches company NAICS "52211".
func (ic *IndustryClassifier) matchNAICSCode(naicsCode string, configCodes []string) bool {
	for _, configCode := range configCodes {
		// Either the config code is a prefix of the company code,
		// or the company code is a prefix of the config code
		if strings.HasPrefix(naicsCode, configCode) || strings.HasPrefix(configCode, naicsCode) {
			return true
		}
	}
	return false
}

// ClassifyIndustry determines the industry classification for a company
func (ic *IndustryClassifier) ClassifyIndustry(ticker string, data *entities.FinancialData) (*SectorConfig, error) {
	// TODO: Implement proper industry classification logic
	// For now, use simple heuristics based on financial characteristics

	if data == nil {
		return nil, fmt.Errorf("financial data is required for industry classification")
	}

	// Technology sector detection runs FIRST.
	// Ordering matters: tech companies (especially fabless semiconductors with acquired IP)
	// can legitimately have 10-30% inventory ratios and >10% intangibles, which otherwise
	// trip the retail heuristic. By checking tech first we ensure R&D/SBC-heavy issuers
	// are caught before the coarser retail balance-sheet signals are evaluated.
	if ic.isTechnologyCompany(ticker, data) {
		return ic.sectorConfigs["45"], nil // Technology sector
	}

	// Retail sector detection
	if ic.isRetailCompany(data) {
		return ic.sectorConfigs["25"], nil // Consumer Discretionary sector
	}

	// Manufacturing sector detection
	if ic.isManufacturingCompany(data) {
		return ic.sectorConfigs["20"], nil // Industrials sector
	}

	// Utilities sector detection
	if ic.isUtilitiesCompany(data) {
		return ic.sectorConfigs["55"], nil // Utilities sector
	}

	// Financial services detection
	if ic.isFinancialCompany(data) {
		// TODO: Add financial sector configuration (sector "40")
		// For now, default to industrials
		return ic.sectorConfigs["20"], nil // Default to industrials
	}

	// Healthcare sector detection
	if ic.isHealthcareCompany(data) {
		// TODO: Add healthcare sector configuration (sector "35")
		// For now, default to industrials
		return ic.sectorConfigs["20"], nil // Default to industrials
	}

	// Default to general industrial classification
	return ic.sectorConfigs["20"], nil
}

// GetSectorConfig returns configuration for a specific sector
func (ic *IndustryClassifier) GetSectorConfig(sectorCode string) (*SectorConfig, bool) {
	config, exists := ic.sectorConfigs[sectorCode]
	return config, exists
}

// GetAllSectorConfigs returns all available sector configurations
func (ic *IndustryClassifier) GetAllSectorConfigs() map[string]*SectorConfig {
	return ic.sectorConfigs
}

// loadDefaultConfigurations loads default industry configurations
func (ic *IndustryClassifier) loadDefaultConfigurations() {
	// Technology Sector (45)
	ic.sectorConfigs["45"] = &SectorConfig{
		SectorCode:    "45",
		SectorName:    "Information Technology",
		SubIndustries: []string{"451010", "451020", "451030"}, // Software, Hardware, Semiconductors
		RiskProfile: RiskProfile{
			CyclicalityRisk:    RiskMedium,
			RegulatoryRisk:     RiskLow,
			TechnologyRisk:     RiskHigh,
			CompetitiveRisk:    RiskHigh,
			CapitalIntensity:   RiskLow,
			WorkingCapitalRisk: RiskLow,
		},
		Thresholds: IndustryThresholds{
			GoodwillThreshold:         0.15,  // 15% of total assets
			IntangibleThreshold:       0.20,  // 20% of total assets
			InventoryObsolescenceRate: 0.50,  // 50% haircut (high obsolescence)
			DeferredTaxThreshold:      0.05,  // 5% of total assets
			OperatingLeaseRate:        0.055, // 5.5% discount rate
			PensionFundingThreshold:   0.80,  // 80% funding threshold
			ContingentLiabilityRate:   0.40,  // 40% probability weighting (conservative for tech)
			RestructuringThreshold:    0.02,  // 2% of revenue
			StockCompThreshold:        0.08,  // 8% of revenue (high for tech)
			LitigationThreshold:       0.01,  // 1% of revenue
			QualityScoreAdjustment:    -5.0,  // -5 points for tech volatility
			MinimumQualityScore:       65.0,  // 65 minimum quality score
		},
		CommonAdjustments: []string{"stock_compensation", "intangible_writedown", "rd_capitalization"},
		KeyMetrics:        []string{"rd_intensity", "stock_compensation_ratio", "intangible_ratio"},
		Characteristics: IndustryCharacteristics{
			AssetHeavy:            false,
			InventoryIntensive:    false,
			IntangibleIntensive:   true,
			RegulatedIndustry:     false,
			CyclicalEarnings:      true,
			HighRDIntensity:       true,
			LongTermContracts:     false,
			SeasonalBusiness:      false,
			HighStockCompensation: true,
			FrequentRestructuring: false,
			TypicalAdjustments:    []string{"A2", "A3", "C4"}, // Intangibles, R&D, Stock comp
		},
	}

	// Manufacturing/Industrials Sector (20)
	ic.sectorConfigs["20"] = &SectorConfig{
		SectorCode:    "20",
		SectorName:    "Industrials",
		SubIndustries: []string{"201010", "201020", "201030"}, // Aerospace, Machinery, Transportation
		RiskProfile: RiskProfile{
			CyclicalityRisk:    RiskHigh,
			RegulatoryRisk:     RiskMedium,
			TechnologyRisk:     RiskMedium,
			CompetitiveRisk:    RiskMedium,
			CapitalIntensity:   RiskHigh,
			WorkingCapitalRisk: RiskHigh,
		},
		Thresholds: IndustryThresholds{
			GoodwillThreshold:         0.10,  // 10% of total assets
			IntangibleThreshold:       0.08,  // 8% of total assets
			InventoryObsolescenceRate: 0.25,  // 25% haircut
			DeferredTaxThreshold:      0.03,  // 3% of total assets
			OperatingLeaseRate:        0.050, // 5.0% discount rate
			PensionFundingThreshold:   0.85,  // 85% funding threshold
			ContingentLiabilityRate:   0.70,  // 70% probability weighting
			RestructuringThreshold:    0.03,  // 3% of revenue
			StockCompThreshold:        0.03,  // 3% of revenue
			LitigationThreshold:       0.015, // 1.5% of revenue
			QualityScoreAdjustment:    0.0,   // No adjustment (baseline)
			MinimumQualityScore:       70.0,  // 70 minimum quality score
		},
		CommonAdjustments: []string{"inventory_obsolescence", "pension_adjustment", "restructuring"},
		KeyMetrics:        []string{"asset_turnover", "inventory_turnover", "pension_funding_ratio"},
		Characteristics: IndustryCharacteristics{
			AssetHeavy:            true,
			InventoryIntensive:    true,
			IntangibleIntensive:   false,
			RegulatedIndustry:     true,
			CyclicalEarnings:      true,
			HighRDIntensity:       false,
			LongTermContracts:     true,
			SeasonalBusiness:      false,
			HighStockCompensation: false,
			FrequentRestructuring: true,
			TypicalAdjustments:    []string{"A5", "B2", "C1"}, // Inventory, Pensions, Restructuring
		},
	}

	// Consumer Discretionary/Retail Sector (25)
	ic.sectorConfigs["25"] = &SectorConfig{
		SectorCode:    "25",
		SectorName:    "Consumer Discretionary",
		SubIndustries: []string{"255010", "255020", "255030"}, // Retail, Restaurants, Hotels
		RiskProfile: RiskProfile{
			CyclicalityRisk:    RiskHigh,
			RegulatoryRisk:     RiskLow,
			TechnologyRisk:     RiskMedium,
			CompetitiveRisk:    RiskHigh,
			CapitalIntensity:   RiskMedium,
			WorkingCapitalRisk: RiskHigh,
		},
		Thresholds: IndustryThresholds{
			GoodwillThreshold:         0.12,  // 12% of total assets
			IntangibleThreshold:       0.10,  // 10% of total assets
			InventoryObsolescenceRate: 0.40,  // 40% haircut (fashion/seasonal)
			DeferredTaxThreshold:      0.04,  // 4% of total assets
			OperatingLeaseRate:        0.060, // 6.0% discount rate (store leases)
			PensionFundingThreshold:   0.85,  // 85% funding threshold
			ContingentLiabilityRate:   0.65,  // 65% probability weighting
			RestructuringThreshold:    0.025, // 2.5% of revenue
			StockCompThreshold:        0.04,  // 4% of revenue
			LitigationThreshold:       0.02,  // 2% of revenue
			QualityScoreAdjustment:    -3.0,  // -3 points for retail volatility
			MinimumQualityScore:       68.0,  // 68 minimum quality score
		},
		CommonAdjustments: []string{"inventory_obsolescence", "operating_lease_capitalization", "seasonal_adjustment"},
		KeyMetrics:        []string{"inventory_turnover", "same_store_sales", "lease_intensity"},
		Characteristics: IndustryCharacteristics{
			AssetHeavy:            false,
			InventoryIntensive:    true,
			IntangibleIntensive:   false,
			RegulatedIndustry:     false,
			CyclicalEarnings:      true,
			HighRDIntensity:       false,
			LongTermContracts:     false,
			SeasonalBusiness:      true,
			HighStockCompensation: false,
			FrequentRestructuring: true,
			TypicalAdjustments:    []string{"A5", "B1", "C7"}, // Inventory, Leases, Working capital
		},
	}
}

// Industry classification helper methods
// TODO: Replace with more sophisticated classification logic using external data sources

// isTechnologyCompany detects technology companies based on financial characteristics
func (ic *IndustryClassifier) isTechnologyCompany(ticker string, data *entities.FinancialData) bool {
	// High R&D intensity
	if data.Revenue > 0 && data.ResearchAndDevelopment > 0 {
		rdIntensity := data.ResearchAndDevelopment / data.Revenue
		if rdIntensity > 0.10 { // >10% R&D intensity
			return true
		}
	}

	// High stock-based compensation
	if data.Revenue > 0 && data.StockBasedCompensation > 0 {
		stockCompRatio := data.StockBasedCompensation / data.Revenue
		if stockCompRatio > techSBCFloorPctRevenue {
			return true
		}
	}

	// High intangible assets
	if data.TotalAssets > 0 && data.IntangibleAssets > 0 {
		intangibleRatio := data.IntangibleAssets / data.TotalAssets
		if intangibleRatio > 0.15 { // >15% intangible assets
			return true
		}
	}

	// Known technology tickers (temporary heuristic)
	techTickers := []string{"AAPL", "MSFT", "GOOGL", "GOOG", "AMZN", "META", "TSLA", "NVDA", "ORCL", "CRM"}
	for _, techTicker := range techTickers {
		if strings.EqualFold(ticker, techTicker) {
			return true
		}
	}

	return false
}

// isManufacturingCompany detects manufacturing companies
func (ic *IndustryClassifier) isManufacturingCompany(data *entities.FinancialData) bool {
	// High tangible assets (proxy for PP&E) relative to total assets
	if data.TotalAssets > 0 && data.TangibleAssets > 0 {
		tangibleRatio := data.TangibleAssets / data.TotalAssets
		if tangibleRatio > 0.60 { // >60% tangible assets (capital intensive)
			return true
		}
	}

	// Significant inventory levels
	if data.TotalAssets > 0 && data.Inventory > 0 {
		inventoryRatio := data.Inventory / data.TotalAssets
		if inventoryRatio > 0.15 { // >15% inventory
			return true
		}
	}

	return false
}

// isRetailCompany detects retail companies.
//
// Early-return guards run before any balance-sheet matching so that tech
// companies with moderate inventory + substantial intangibles (e.g. fabless
// semiconductors post-acquisition) can never match the retail profile:
//   - Retailers do not run >5% R&D-to-revenue. Tech does (AMD ~25%).
//   - Retailers do not have tech-level SBC-to-revenue. Tech does (AMD ~8%).
func (ic *IndustryClassifier) isRetailCompany(data *entities.FinancialData) bool {
	// R&D guard: retailers do not run R&D labs — anything above the ceiling
	// signals tech/growth and excludes retail classification.
	if data.Revenue > 0 && data.ResearchAndDevelopment > 0 {
		if data.ResearchAndDevelopment/data.Revenue > retailRnDCeilingPctRevenue {
			return false
		}
	}

	// SBC guard: retailers pay cash, not equity — SBC above the ceiling signals
	// a tech/growth issuer, never a traditional retailer.
	if data.Revenue > 0 && data.StockBasedCompensation > 0 {
		if data.StockBasedCompensation/data.Revenue > retailSBCCeilingPctRevenue {
			return false
		}
	}

	// High inventory turnover characteristics
	if data.TotalAssets > 0 && data.Inventory > 0 {
		inventoryRatio := data.Inventory / data.TotalAssets
		// Retail typically has moderate inventory levels (10-30%)
		if inventoryRatio > 0.10 && inventoryRatio < 0.30 {
			// Asset-light model (high intangible ratio indicates brand value)
			if data.IntangibleAssets > 0 {
				intangibleRatio := data.IntangibleAssets / data.TotalAssets
				if intangibleRatio > 0.10 { // >10% intangibles (brand value)
					return true
				}
			}
			// Or moderate tangible asset ratio
			if data.TangibleAssets > 0 {
				tangibleRatio := data.TangibleAssets / data.TotalAssets
				if tangibleRatio < 0.70 { // <70% tangible assets (asset-light)
					return true
				}
			}
		}
	}

	return false
}

// isUtilitiesCompany detects utilities companies
func (ic *IndustryClassifier) isUtilitiesCompany(data *entities.FinancialData) bool {
	// Very high tangible assets (capital intensive)
	if data.TotalAssets > 0 && data.TangibleAssets > 0 {
		tangibleRatio := data.TangibleAssets / data.TotalAssets
		if tangibleRatio > 0.80 { // >80% tangible assets (very capital intensive)
			// Low inventory (utilities don't hold much inventory)
			inventoryRatio := 0.0
			if data.Inventory > 0 {
				inventoryRatio = data.Inventory / data.TotalAssets
			}
			if inventoryRatio < 0.05 { // <5% inventory
				return true
			}
		}
	}

	return false
}

// isFinancialCompany detects financial services companies
func (ic *IndustryClassifier) isFinancialCompany(data *entities.FinancialData) bool {
	// Financial companies have different balance sheet structure
	// Low tangible assets, low inventory, high debt levels

	if data.TotalAssets > 0 {
		// Low tangible assets (mostly financial assets)
		tangibleRatio := 0.0
		if data.TangibleAssets > 0 {
			tangibleRatio = data.TangibleAssets / data.TotalAssets
		}

		// Low inventory
		inventoryRatio := 0.0
		if data.Inventory > 0 {
			inventoryRatio = data.Inventory / data.TotalAssets
		}

		// High debt levels (financial leverage)
		debtRatio := 0.0
		if data.TotalDebt > 0 {
			debtRatio = data.TotalDebt / data.TotalAssets
		}

		if tangibleRatio < 0.30 && inventoryRatio < 0.02 && debtRatio > 0.20 {
			return true
		}
	}

	return false
}

// isHealthcareCompany detects healthcare companies
func (ic *IndustryClassifier) isHealthcareCompany(data *entities.FinancialData) bool {
	// High R&D intensity (similar to tech but different characteristics)
	if data.Revenue > 0 && data.ResearchAndDevelopment > 0 {
		rdIntensity := data.ResearchAndDevelopment / data.Revenue
		if rdIntensity > 0.15 { // >15% R&D intensity (higher than tech)
			// Lower stock compensation than tech
			stockCompRatio := 0.0
			if data.StockBasedCompensation > 0 {
				stockCompRatio = data.StockBasedCompensation / data.Revenue
			}
			if stockCompRatio < 0.05 { // <5% stock compensation
				return true
			}
		}
	}

	return false
}

// ApplyIndustrySpecificThresholds applies industry-specific thresholds to cleaning rules
func (ic *IndustryClassifier) ApplyIndustrySpecificThresholds(rules []*entities.CleaningRule, sectorConfig *SectorConfig) []*entities.CleaningRule {
	if sectorConfig == nil {
		return rules
	}

	adjustedRules := make([]*entities.CleaningRule, len(rules))
	copy(adjustedRules, rules)

	for i, rule := range adjustedRules {
		if rule.Threshold == nil {
			rule.Threshold = &entities.ThresholdConfig{}
		}

		// Apply industry-specific thresholds based on rule ID
		switch rule.ID {
		case "goodwill_exclusion":
			rule.Threshold.PercentageOfAssets = &sectorConfig.Thresholds.GoodwillThreshold
		case "intangible_writedown":
			rule.Threshold.PercentageOfAssets = &sectorConfig.Thresholds.IntangibleThreshold
		case "inventory_obsolescence":
			rule.Threshold.WritedownRate = &sectorConfig.Thresholds.InventoryObsolescenceRate
		case "deferred_tax_adjustment":
			rule.Threshold.PercentageOfAssets = &sectorConfig.Thresholds.DeferredTaxThreshold
		case "restructuring_charges":
			rule.Threshold.PercentageOfRevenue = &sectorConfig.Thresholds.RestructuringThreshold
		case "stock_compensation":
			rule.Threshold.PercentageOfRevenue = &sectorConfig.Thresholds.StockCompThreshold
		case "litigation_settlements":
			rule.Threshold.PercentageOfRevenue = &sectorConfig.Thresholds.LitigationThreshold
			// TODO: Add support for additional threshold types in ThresholdConfig entity
			// - DiscountRate for operating lease capitalization
			// - FundingRatio for pension adjustments
			// - ProbabilityWeight for contingent liabilities
		}

		adjustedRules[i] = rule
	}

	return adjustedRules
}
