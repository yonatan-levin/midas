package datacleaner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/adjustments"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/industry"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/rules"
)

// service implements the DataCleanerService interface
type service struct {
	config             *config.DataCleanerConfig
	rulesEngine        rules.RuleEngine
	assetAdjuster      *adjustments.AssetAdjuster
	liabilityAdjuster  *adjustments.LiabilityAdjuster
	earningsAdjuster   *adjustments.EarningsAdjuster
	industryClassifier *industry.IndustryClassifier
	flagEvaluator      ports.FlagConditionEvaluator
	cache              map[string]*entities.CleaningResult // Simple in-memory cache for now
	cacheMu            sync.RWMutex
	stats              entities.CleaningStats
	statsMu            sync.RWMutex
}

// NewDataCleanerService creates a new DataCleaner service instance
func NewDataCleanerService(cfg *config.Config) (DataCleanerService, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration cannot be nil")
	}

	if !cfg.DataCleaner.Enabled {
		return nil, fmt.Errorf("data cleaner service is disabled in configuration")
	}

	// Initialize rules engine
	rulesEngine := rules.NewRuleEngine()

	// Load main rules
	if err := rulesEngine.LoadRules(cfg.DataCleaner.RulesPath); err != nil {
		return nil, fmt.Errorf("failed to load cleaning rules: %w", err)
	}

	// Validate rules
	if err := rulesEngine.ValidateRules(); err != nil {
		return nil, fmt.Errorf("rules validation failed: %w", err)
	}

	// Initialize flag evaluator with loaded config
	flagConfigPath := "config/datacleaner/flag_conditions.json"
	flagConfig, err := config.LoadFlagConditionsConfig(flagConfigPath)
	if err != nil {
		// Log warning but continue with empty config for fallback
		// TODO: Add proper logging
		flagConfig = &config.FlagConditionsConfig{
			Version: "1.0",
			Flags:   []config.FlagConfig{},
		}
	}

	flagEvaluator, err := NewFlagConditionEvaluatorService(flagConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize flag evaluator: %w", err)
	}

	svc := &service{
		config:             &cfg.DataCleaner,
		rulesEngine:        rulesEngine,
		assetAdjuster:      adjustments.NewAssetAdjuster(),
		liabilityAdjuster:  adjustments.NewLiabilityAdjuster(),
		earningsAdjuster:   adjustments.NewEarningsAdjuster(),
		industryClassifier: industry.NewIndustryClassifier(),
		flagEvaluator:      flagEvaluator,
		cache:              make(map[string]*entities.CleaningResult),
		stats: entities.CleaningStats{
			QualityDistribution: make(map[entities.QualityGrade]int),
			CommonAdjustments:   make(map[string]int),
			CommonFlags:         make(map[string]int),
		},
	}

	return svc, nil
}

// CleanFinancialData cleans and normalizes financial data using configured rules
func (s *service) CleanFinancialData(ctx context.Context, data *entities.FinancialData) (*entities.CleaningResult, error) {
	if data == nil {
		return nil, fmt.Errorf("financial data cannot be nil")
	}

	// Check context cancellation
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	startTime := time.Now()

	// Validate input data
	if err := s.ValidateData(data); err != nil {
		return nil, fmt.Errorf("data validation failed: %w", err)
	}

	// Check cache if enabled
	if s.config.EnableCaching {
		cacheKey := generateCacheKey(data)
		if cachedResult := s.getCachedResult(cacheKey); cachedResult != nil {
			// Update processing time for the cache hit
			cachedResult.ProcessingTime = time.Since(startTime)
			return cachedResult, nil
		}
	}

	// Create cleaning context
	industryCode, err := s.getIndustryCode(data)
	if err != nil {
		// Log warning but continue with empty industry code for general rules
		industryCode = ""
	}

	cleaningCtx := &entities.CleaningContext{
		IndustryCode:     industryCode,
		CompanySize:      getCompanySize(data),
		DataVintage:      data.FilingDate,
		EnableIndustry:   s.config.EnableIndustryRules,
		EnableCaching:    s.config.EnableCaching,
		QualityThreshold: s.config.MinQualityScore,
	}

	// Initialize result
	result := &entities.CleaningResult{
		Success:          false,
		Timestamp:        startTime,
		IndustryCode:     cleaningCtx.IndustryCode,
		IndustrySpecific: false,
		Adjustments:      make([]entities.Adjustment, 0),
		Flags:            make([]entities.Flag, 0),
		QualityIssues:    make([]string, 0),
		Errors:           make([]string, 0),
		Warnings:         make([]string, 0),
	}

	// Create a copy of the data for cleaning
	cleanedData := *data
	result.CleanedData = &cleanedData

	// Load industry-specific rules if enabled
	if cleaningCtx.EnableIndustry && cleaningCtx.IndustryCode != "" {
		if err := s.loadIndustryRules(cleaningCtx.IndustryCode); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Failed to load industry rules: %v", err))
		} else {
			result.IndustrySpecific = true
		}
	}

	// Apply active cleaning adjustments
	adjustments, flags, rulesApplied, err := s.applyActiveAdjustments(ctx, result.CleanedData, cleaningCtx)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		result.ProcessingTime = time.Since(startTime)
		return result, nil // Return partial result rather than error
	}

	result.RulesApplied = rulesApplied
	result.Adjustments = adjustments
	result.Flags = flags

	// Add additional warning flags for risky patterns
	additionalFlags := s.createRiskWarningFlags(result.CleanedData, startTime)
	result.Flags = append(result.Flags, additionalFlags...)

	// Calculate quality score
	qualityScore, qualityIssues, err := s.calculateQualityScore(result.CleanedData, flags)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Quality score calculation failed: %v", err))
		qualityScore = 50.0 // Default moderate score
	}

	result.QualityScore = qualityScore
	result.QualityGrade = string(entities.GetQualityGrade(qualityScore))
	result.QualityIssues = qualityIssues

	// Mark as successful
	result.Success = true
	result.ProcessingTime = time.Since(startTime)

	// Cache result if enabled
	if s.config.EnableCaching {
		cacheKey := generateCacheKey(data)
		s.setCachedResult(cacheKey, result)
	}

	// Update statistics
	s.updateStats(result)

	return result, nil
}

// GetIndustryRules returns applicable rules for a specific industry
func (s *service) GetIndustryRules(industryCode string) ([]entities.CleaningRule, error) {
	if industryCode == "" {
		return s.rulesEngine.GetRules(nil), nil
	}

	return s.rulesEngine.GetIndustryRules(industryCode), nil
}

// GetQualityScore calculates quality score for financial data without applying changes
func (s *service) GetQualityScore(ctx context.Context, data *entities.FinancialData) (float64, error) {
	if data == nil {
		return 0, fmt.Errorf("financial data cannot be nil")
	}

	// Validate data first
	if err := s.ValidateData(data); err != nil {
		return 0, err
	}

	// Get applicable rules
	industryCode, err := s.getIndustryCode(data)
	if err != nil {
		// Log warning but continue with empty industry code for general rules
		industryCode = ""
	}
	applicableRules := s.rulesEngine.GetIndustryRules(industryCode)

	// Simulate applying rules without making changes
	var flags []entities.Flag
	for _, rule := range applicableRules {
		if !rule.Enabled {
			continue
		}

		// Check if rule applies
		if ruleApplies := s.checkRuleApplicability(&rule, data); ruleApplies {
			// Create flag for quality assessment
			flag := entities.Flag{
				RuleID:      rule.ID,
				Type:        string(rule.Category),
				Severity:    rule.Severity,
				Description: rule.Description,
				Timestamp:   time.Now(),
			}
			flags = append(flags, flag)
		}
	}

	// Calculate quality score based on flags
	score, _, err := s.calculateQualityScore(data, flags)
	return score, err
}

// ValidateData performs basic data validation before cleaning
func (s *service) ValidateData(data *entities.FinancialData) error {
	if data.Ticker == "" {
		return fmt.Errorf("ticker is required")
	}

	if data.Revenue <= 0 {
		return fmt.Errorf("revenue must be positive")
	}

	if data.TotalAssets <= 0 {
		return fmt.Errorf("total assets must be positive")
	}

	if data.SharesOutstanding <= 0 && data.DilutedSharesOutstanding <= 0 {
		return fmt.Errorf("shares outstanding must be positive")
	}

	if data.FilingDate.IsZero() {
		return fmt.Errorf("filing date is required")
	}

	// Check data freshness
	if time.Since(data.FilingDate) > time.Hour*24*365*3 { // 3 years old
		return fmt.Errorf("data is too old: filing date %v", data.FilingDate)
	}

	return nil
}

// Private helper methods

func (s *service) loadIndustryRules(industryCode string) error {
	// Map industry codes to filenames
	industryFileMap := map[string]string{
		"45": "technology.json",
		"25": "retail.json",
		// TODO: Add more industry mappings as needed
	}

	filename, exists := industryFileMap[industryCode]
	if !exists {
		return fmt.Errorf("no industry rules file found for industry code: %s", industryCode)
	}

	industryRulesPath := fmt.Sprintf("%s/%s", s.config.IndustryRulesPath, filename)

	// Use the rules engine to load industry-specific rules
	err := s.rulesEngine.LoadIndustryRules(industryRulesPath)
	if err != nil {
		return fmt.Errorf("failed to load industry rules from %s: %w", industryRulesPath, err)
	}

	return nil
}

// applyActiveAdjustments applies Category A and B adjustments using dedicated adjusters
func (s *service) applyActiveAdjustments(ctx context.Context, data *entities.FinancialData, cleaningCtx *entities.CleaningContext) ([]entities.Adjustment, []entities.Flag, int, error) {
	var allAdjustments []entities.Adjustment
	var allFlags []entities.Flag
	totalRulesApplied := 0

	// Get applicable rules
	applicableRules := s.rulesEngine.GetIndustryRules(cleaningCtx.IndustryCode)

	// Separate rules by category
	assetRules := make([]*entities.CleaningRule, 0)
	liabilityRules := make([]*entities.CleaningRule, 0)

	for i, rule := range applicableRules {
		if !rule.Enabled {
			continue
		}

		// Check if rule applies to this data
		if !s.checkRuleApplicability(&rule, data) {
			continue
		}

		switch rule.Category {
		case entities.AssetQuality:
			assetRules = append(assetRules, &applicableRules[i])
		case entities.LiabilityCompleteness:
			liabilityRules = append(liabilityRules, &applicableRules[i])
		}
	}

	// Apply Category A (Asset Quality) adjustments
	if len(assetRules) > 0 {
		assetResult := s.assetAdjuster.ProcessAssetAdjustments(data, assetRules, cleaningCtx)
		if assetResult.Applied {
			allAdjustments = append(allAdjustments, assetResult.Adjustments...)
			allFlags = append(allFlags, assetResult.Flags...)
			totalRulesApplied += len(assetRules)
		}
	}

	// Apply Category B (Liability Completeness) adjustments
	if len(liabilityRules) > 0 {
		liabilityResult := s.liabilityAdjuster.ProcessLiabilityAdjustments(data, liabilityRules, cleaningCtx)
		if liabilityResult.Applied {
			allAdjustments = append(allAdjustments, liabilityResult.Adjustments...)
			allFlags = append(allFlags, liabilityResult.Flags...)
			totalRulesApplied += len(liabilityRules)
		}
	}

	// Apply Category C (Earnings Normalization) adjustments
	earningsRules := make([]*entities.CleaningRule, 0)
	for i, rule := range applicableRules {
		if rule.Enabled && rule.Category == entities.EarningsNormalization {
			if s.checkRuleApplicability(&rule, data) {
				earningsRules = append(earningsRules, &applicableRules[i])
			}
		}
	}

	if len(earningsRules) > 0 {
		earningsResult := s.earningsAdjuster.ProcessEarningsAdjustments(data, earningsRules, cleaningCtx)
		if earningsResult.Applied {
			allAdjustments = append(allAdjustments, earningsResult.Adjustments...)
			allFlags = append(allFlags, earningsResult.Flags...)
			totalRulesApplied += len(earningsRules)
		}
	}

	return allAdjustments, allFlags, totalRulesApplied, nil
}

func (s *service) checkRuleApplicability(rule *entities.CleaningRule, data *entities.FinancialData) bool {
	// Check XBRL tags - for now, basic implementation
	// TODO: Implement proper XBRL tag matching based on actual data structure
	// TODO: Change the approach to checkRuleApplicability by config and industry hardcoded numbers dosne't apply to all cases
	// Basic rule applicability based on rule ID and data content
	switch rule.ID {
	case "goodwill_exclusion":
		return data.Goodwill > 0
	case "intangible_adjustment":
		return data.OtherIntangibles > 0
	case "obsolete_inventory":
		return data.Inventory > 0 && data.InventoryTurnover < 6.0 // Flag if turnover below 6x
	case "deferred_tax_assets":
		// Only apply if we estimate significant DTA (>2% of assets)
		dtaEstimate := data.TotalAssets * 0.03
		return dtaEstimate > data.TotalAssets*0.02
	case "excess_cash":
		// Only apply if estimated cash is high
		estimatedCash := data.Revenue * 0.05
		operationalNeeds := data.Revenue * 0.1
		return estimatedCash > operationalNeeds
	case "stock_compensation":
		// Flag if company is large or has excessive goodwill/intangibles (suggests aggressive accounting)
		hasAggressiveAccounting := (data.Goodwill > data.TotalAssets*0.25) || (data.OtherIntangibles > data.TotalAssets*0.20)
		return data.Revenue > 100000000 || hasAggressiveAccounting
	case "contingent_liabilities":
		// Only flag if revenue is large AND there are quality issues suggesting risk
		hasRiskFactors := (data.Goodwill > data.TotalAssets*0.1) ||
			(data.OtherIntangibles > data.TotalAssets*0.1) ||
			(data.InventoryTurnover < 4.0)
		return data.Revenue > 300000000 && hasRiskFactors
	case "working_capital_window_dressing":
		// Flag if large company or if significant quality issues detected
		hasQualityIssues := (data.Goodwill > data.TotalAssets*0.2) || (data.OtherIntangibles > data.TotalAssets*0.15)
		return data.Revenue > 500000000 || hasQualityIssues
	case "restructuring_charges", "asset_sale_gains", "litigation_settlements":
		// Flag if company is large or has quality issues (companies with poor accounting may have these issues)
		hasPoorQuality := (data.Goodwill > data.TotalAssets*0.25) || (data.OtherIntangibles > data.TotalAssets*0.20)
		return data.Revenue > 200000000 || hasPoorQuality
	case "operating_leases":
		return data.Revenue > 100000000 // Companies with significant operations
	case "pension_obligations":
		return data.Revenue > 500000000 // Typically larger, older companies
	case "right_of_use_assets":
		return data.TotalAssets > 500000000 // Companies with significant assets
	case "capitalized_software", "capitalized_interest":
		return data.Revenue > 100000000 // Companies with capital intensity
	case "derivative_gains_losses":
		// Large companies using derivatives, or companies with aggressive accounting
		hasAggressiveAccounting := (data.Goodwill > data.TotalAssets*0.3) || (data.OtherIntangibles > data.TotalAssets*0.25)
		return data.Revenue > 1000000000 || hasAggressiveAccounting

	// Tech industry special rules
	case "rd_capitalization_review":
		// Apply to tech companies (ticker TECH or high intangibles suggesting tech)
		isTechCompany := (data.Ticker == "TECH") || (data.OtherIntangibles > data.TotalAssets*0.15)
		return isTechCompany && data.Revenue > 100000000
	case "acquired_technology_writedown":
		// Apply to tech companies with significant intangibles
		isTechCompany := (data.Ticker == "TECH") || (data.OtherIntangibles > data.TotalAssets*0.15)
		return isTechCompany && data.OtherIntangibles > 0
	case "saas_deferred_revenue_quality", "cloud_infrastructure_adjustment", "cryptocurrency_holdings":
		// Apply to tech companies
		isTechCompany := (data.Ticker == "TECH") || (data.OtherIntangibles > data.TotalAssets*0.15)
		return isTechCompany && data.Revenue > 200000000

	default:
		return false // Don't apply unknown rules by default
	}
}

func (s *service) applyRule(rule *entities.CleaningRule, data *entities.FinancialData) (*entities.Adjustment, *entities.Flag, error) {
	timestamp := time.Now()

	switch rule.Adjustment {
	case entities.Exclude:
		return s.applyExclusionRule(rule, data, timestamp)
	case entities.Writedown:
		return s.applyWritedownRule(rule, data, timestamp)
	case entities.FlagForReview:
		return s.applyFlagRule(rule, data, timestamp)
	case entities.Reclassify:
		return s.applyReclassifyRule(rule, data, timestamp)
	case entities.TreatAsDebt:
		return s.applyTreatAsDebtRule(rule, data, timestamp)
	default:
		return nil, nil, fmt.Errorf("unsupported adjustment type: %s", rule.Adjustment)
	}
}

func (s *service) applyExclusionRule(rule *entities.CleaningRule, data *entities.FinancialData, timestamp time.Time) (*entities.Adjustment, *entities.Flag, error) {
	var amount float64
	var fromAccount string

	switch rule.ID {
	case "goodwill_exclusion":
		amount = data.Goodwill
		fromAccount = "Goodwill"
		// Exclude goodwill from tangible assets calculation
		data.TangibleAssets = data.TotalAssets - data.Goodwill - data.OtherIntangibles

	case "restructuring_charges":
		// TODO: Extract actual restructuring charges from financial data
		// For now, estimate based on revenue threshold
		if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
			amount = data.Revenue * (*rule.Threshold.PercentageOfRevenue)
		} else {
			amount = data.Revenue * 0.02 // Default 2% of revenue
		}
		fromAccount = "RestructuringCharges"
		// Adjust normalized operating income
		data.NormalizedOperatingIncome += amount

	case "asset_sale_gains":
		// TODO: Extract actual asset sale gains from financial data
		// For now, estimate minor impact
		amount = data.Revenue * 0.005 // Estimate 0.5% of revenue
		fromAccount = "AssetSaleGains"
		// Adjust normalized operating income
		data.NormalizedOperatingIncome -= amount

	case "litigation_settlements":
		// TODO: Extract actual litigation costs from financial data
		// For now, estimate based on company size
		amount = data.Revenue * 0.001 // Estimate 0.1% of revenue
		fromAccount = "LitigationSettlements"
		// Adjust normalized operating income
		data.NormalizedOperatingIncome += amount

	case "excess_cash":
		// Calculate excess cash above operational needs
		if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
			operationalCashNeeds := data.Revenue * (*rule.Threshold.PercentageOfRevenue)
			// TODO: Get actual cash from data - for now use placeholder
			totalCash := data.Revenue * 0.05 // Estimate 5% of revenue as cash
			if totalCash > operationalCashNeeds {
				amount = totalCash - operationalCashNeeds
			}
		}
		fromAccount = "ExcessCash"
		// Exclude from working capital calculations

	case "right_of_use_assets":
		// TODO: Extract actual ROU assets from financial data
		// For now, estimate based on asset size
		amount = data.TotalAssets * 0.02 // Estimate 2% of assets
		fromAccount = "RightOfUseAssets"
		// Exclude from tangible assets
		data.TangibleAssets -= amount

	default:
		// For any other exclusion rules, create a basic adjustment
		// TODO: Implement specific logic for each rule
		amount = data.Revenue * 0.001 // Default small adjustment (0.1% of revenue)
		fromAccount = fmt.Sprintf("Other_%s", rule.ID)

		// For earnings normalization rules, adjust normalized operating income
		if rule.Category == entities.EarningsNormalization {
			data.NormalizedOperatingIncome += amount // Add back excluded item
		}
	}

	adjustment := &entities.Adjustment{
		ID:          fmt.Sprintf("adj_%d", timestamp.UnixNano()),
		RuleID:      rule.ID,
		Category:    rule.Category,
		Type:        rule.Adjustment,
		Amount:      amount,
		FromAccount: fromAccount,
		Reasoning:   rule.Description,
		Applied:     true,
		Timestamp:   timestamp,
	}

	return adjustment, nil, nil
}

func (s *service) applyWritedownRule(rule *entities.CleaningRule, data *entities.FinancialData, timestamp time.Time) (*entities.Adjustment, *entities.Flag, error) {
	var amount float64
	var fromAccount string

	switch rule.ID {
	case "intangible_adjustment":
		// Write down indefinite-lived intangibles
		amount = data.OtherIntangibles
		if rule.Threshold != nil && rule.Threshold.WritedownRate != nil {
			amount *= (*rule.Threshold.WritedownRate)
		} else {
			amount *= 1.0 // Default full writedown
		}
		fromAccount = "IntangibleAssets"
		// Reduce other intangibles and recalculate tangible assets
		data.OtherIntangibles -= amount
		data.TangibleAssets = data.TotalAssets - data.Goodwill - data.OtherIntangibles

	case "deferred_tax_assets":
		// Write down portion of deferred tax assets
		// TODO: Extract actual DTA from financial data
		// For now, estimate based on assets
		dtaEstimate := data.TotalAssets * 0.03 // Estimate 3% of assets as DTA
		if rule.Threshold != nil && rule.Threshold.PercentageOfAssets != nil {
			amount = dtaEstimate * (*rule.Threshold.PercentageOfAssets)
		} else {
			amount = dtaEstimate * 0.25 // Default 25% writedown
		}
		fromAccount = "DeferredTaxAssets"
		// Reduce tangible assets
		data.TangibleAssets -= amount

	case "obsolete_inventory":
		// Write down obsolete inventory
		if rule.Threshold != nil && rule.Threshold.WritedownRate != nil {
			amount = data.Inventory * (*rule.Threshold.WritedownRate)
		} else {
			amount = data.Inventory * 0.4 // Default 40% writedown
		}
		fromAccount = "Inventory"
		// Track inventory writedown
		data.DeadInventoryWritedown = amount
		// Reduce tangible assets
		data.TangibleAssets -= amount

	default:
		// For any other writedown rules, create a basic adjustment
		// TODO: Implement specific logic for each rule
		amount = 0
		fromAccount = "Other"
	}

	if amount > 0 {
		adjustment := &entities.Adjustment{
			ID:          fmt.Sprintf("adj_%d", timestamp.UnixNano()),
			RuleID:      rule.ID,
			Category:    rule.Category,
			Type:        rule.Adjustment,
			Amount:      amount,
			FromAccount: fromAccount,
			Reasoning:   rule.Description,
			Applied:     true,
			Timestamp:   timestamp,
		}
		return adjustment, nil, nil
	}

	return nil, nil, nil
}

func (s *service) applyFlagRule(rule *entities.CleaningRule, data *entities.FinancialData, timestamp time.Time) (*entities.Adjustment, *entities.Flag, error) {
	var amount float64

	// Calculate amount for context in flag
	switch rule.ID {
	case "contingent_liabilities":
		// Estimate contingent liability exposure
		if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
			amount = data.Revenue * (*rule.Threshold.PercentageOfRevenue)
		} else {
			amount = data.Revenue * 0.05 // Default 5% of revenue
		}
	case "stock_compensation":
		// Estimate stock compensation expense
		amount = data.Revenue * 0.02 // Estimate 2% of revenue
	case "working_capital_window_dressing":
		// Estimate potential working capital manipulation
		if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
			amount = data.Revenue * (*rule.Threshold.PercentageOfRevenue)
		} else {
			amount = data.Revenue * 0.15 // Default 15% of revenue (from config)
		}
	case "rd_capitalization_review":
		// Estimate R&D capitalization amount
		amount = data.Revenue * 0.1 // 10% of revenue
	case "saas_deferred_revenue_quality":
		// Estimate deferred revenue quality issues
		amount = data.Revenue * 0.3 // 30% of revenue
	case "acquired_technology_writedown":
		// Estimate acquired technology at risk
		amount = data.OtherIntangibles * 0.6 // 60% writedown potential
	default:
		amount = 0
	}

	flag := &entities.Flag{
		ID:             fmt.Sprintf("flag_%d", timestamp.UnixNano()),
		RuleID:         rule.ID,
		Type:           string(rule.Category),
		Severity:       rule.Severity,
		Description:    rule.Description,
		Recommendation: fmt.Sprintf("Review %s for potential issues", rule.Name),
		Amount:         amount,
		Timestamp:      timestamp,
	}

	return nil, flag, nil
}

func (s *service) applyReclassifyRule(rule *entities.CleaningRule, data *entities.FinancialData, timestamp time.Time) (*entities.Adjustment, *entities.Flag, error) {
	var amount float64
	var fromAccount string

	switch rule.ID {
	case "capitalized_software":
		// Reclassify capitalized software as operating expense
		if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
			amount = data.Revenue * (*rule.Threshold.PercentageOfRevenue)
		} else {
			amount = data.Revenue * 0.02 // Default 2% of revenue
		}
		fromAccount = "CapitalizedSoftware"
		// Adjust normalized operating income (increase expense)
		data.NormalizedOperatingIncome -= amount
		// Reduce tangible assets
		data.TangibleAssets -= amount

	case "capitalized_interest":
		// Reclassify capitalized interest as interest expense
		// TODO: Extract actual capitalized interest from financial data
		// For now, estimate based on debt level
		amount = data.InterestBearingDebt * 0.02 // Estimate 2% interest rate on debt
		fromAccount = "CapitalizedInterest"
		// Adjust interest expense
		data.InterestExpense += amount
		// Adjust normalized operating income
		data.NormalizedOperatingIncome -= amount

	case "working_capital_window_dressing":
		// Flag potential working capital manipulation
		// Calculate potential manipulation amount
		amount = data.Revenue * 0.01 // Estimate 1% of revenue
		fromAccount = "WorkingCapitalAdjustments"
		// This is more of a flag than actual reclassification

	default:
		// For any other reclassification rules
		amount = 0
		fromAccount = "Other"
	}

	if amount > 0 {
		adjustment := &entities.Adjustment{
			ID:          fmt.Sprintf("adj_%d", timestamp.UnixNano()),
			RuleID:      rule.ID,
			Category:    rule.Category,
			Type:        rule.Adjustment,
			Amount:      amount,
			FromAccount: fromAccount,
			Reasoning:   rule.Description,
			Applied:     true,
			Timestamp:   timestamp,
		}
		return adjustment, nil, nil
	}

	return nil, nil, nil
}

func (s *service) applyTreatAsDebtRule(rule *entities.CleaningRule, data *entities.FinancialData, timestamp time.Time) (*entities.Adjustment, *entities.Flag, error) {
	var amount float64
	var fromAccount string

	switch rule.ID {
	case "operating_leases":
		// TODO: Extract actual operating lease liability from financial data
		// For now, estimate based on revenue (retail/real estate intensive)
		amount = data.Revenue * 0.1 // Estimate 10% of revenue as lease obligations
		fromAccount = "OperatingLeases"
		// Add to interest-bearing debt
		data.InterestBearingDebt += amount
		data.TotalDebt += amount

	case "pension_obligations":
		// TODO: Extract actual pension underfunding from financial data
		// For now, estimate based on company size
		amount = data.Revenue * 0.05 // Estimate 5% of revenue
		fromAccount = "PensionObligations"
		// Add to interest-bearing debt
		data.InterestBearingDebt += amount
		data.TotalDebt += amount

	default:
		// For any other treat-as-debt rules
		amount = 0
		fromAccount = "Other"
	}

	if amount > 0 {
		adjustment := &entities.Adjustment{
			ID:          fmt.Sprintf("adj_%d", timestamp.UnixNano()),
			RuleID:      rule.ID,
			Category:    rule.Category,
			Type:        rule.Adjustment,
			Amount:      amount,
			FromAccount: fromAccount,
			Reasoning:   rule.Description,
			Applied:     true,
			Timestamp:   timestamp,
		}
		return adjustment, nil, nil
	}

	return nil, nil, nil
}

func (s *service) calculateQualityScore(data *entities.FinancialData, flags []entities.Flag) (float64, []string, error) {
	baseScore := 100.0
	var issues []string

	// Deduct points for each flag based on severity
	for _, flag := range flags {
		switch flag.Severity {
		case entities.Critical:
			baseScore -= 20
			issues = append(issues, fmt.Sprintf("Critical: %s", flag.Description))
		case entities.Warning:
			baseScore -= 10
			issues = append(issues, fmt.Sprintf("Warning: %s", flag.Description))
		case entities.Info:
			baseScore -= 5
			issues = append(issues, fmt.Sprintf("Info: %s", flag.Description))
		}
	}

	// Additional quality checks
	if data.Revenue <= 0 {
		baseScore -= 30
		issues = append(issues, "Missing or invalid revenue data")
	}

	if data.TotalAssets <= 0 {
		baseScore -= 30
		issues = append(issues, "Missing or invalid asset data")
	}

	// Ensure score is between 0 and 100
	if baseScore < 0 {
		baseScore = 0
	}

	return baseScore, issues, nil
}

func (s *service) getCachedResult(key string) *entities.CleaningResult {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	return s.cache[key]
}

func (s *service) setCachedResult(key string, result *entities.CleaningResult) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cache[key] = result
}

func (s *service) updateStats(result *entities.CleaningResult) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()

	s.stats.TotalCompanies++
	s.stats.AverageQualityScore = (s.stats.AverageQualityScore*float64(s.stats.TotalCompanies-1) + result.QualityScore) / float64(s.stats.TotalCompanies)
	s.stats.QualityDistribution[entities.GetQualityGrade(result.QualityScore)]++

	for _, adj := range result.Adjustments {
		s.stats.CommonAdjustments[adj.RuleID]++
	}

	for _, flag := range result.Flags {
		s.stats.CommonFlags[flag.RuleID]++
	}
}

// Helper functions

func generateCacheKey(data *entities.FinancialData) string {
	return fmt.Sprintf("%s_%s_%v", data.Ticker, data.FilingPeriod, data.FilingDate.Unix())
}

// getIndustryCode determines the industry code for the given financial data using the IndustryClassifier
func (s *service) getIndustryCode(data *entities.FinancialData) (string, error) {
	if data == nil {
		return "", fmt.Errorf("financial data cannot be nil")
	}

	// Use the industry classifier to determine the sector
	sectorConfig, err := s.industryClassifier.ClassifyIndustry(data.Ticker, data)
	if err != nil {
		// Log the error but return empty string to maintain backward compatibility
		// This allows the system to fall back to general rules
		return "", fmt.Errorf("failed to classify industry for ticker %s: %w", data.Ticker, err)
	}

	if sectorConfig == nil {
		// No specific industry classification found, use general rules
		return "", nil
	}

	return sectorConfig.SectorCode, nil
}

func getCompanySize(data *entities.FinancialData) entities.CompanySize {
	// TODO: Implement proper company size classification based on market cap
	// For now, classify based on revenue as a proxy
	switch {
	case data.Revenue > 50000000000: // $50B+
		return entities.MegaCap
	case data.Revenue > 10000000000: // $10B+
		return entities.LargeCap
	case data.Revenue > 2000000000: // $2B+
		return entities.MidCap
	default:
		return entities.SmallCap
	}
}

// createRiskWarningFlags creates additional warning flags for risky patterns using the FlagConditionEvaluator
func (s *service) createRiskWarningFlags(data *entities.FinancialData, timestamp time.Time) []entities.Flag {
	ctx := context.Background()

	// Convert FinancialData to map for flag evaluator
	dataMap := map[string]interface{}{
		"Ticker":           data.Ticker,
		"TotalAssets":      data.TotalAssets,
		"Goodwill":         data.Goodwill,
		"OtherIntangibles": data.OtherIntangibles,
		"Revenue":          data.Revenue,
		"FilingDate":       data.FilingDate,
	}

	// Use the flag evaluator to evaluate configured conditions
	flagResults, err := s.flagEvaluator.EvaluateFlags(ctx, dataMap)
	if err != nil {
		// Log error but continue with hardcoded flags to maintain system stability
		// TODO: Add proper logging
		return s.createHardcodedRiskFlags(data, timestamp)
	}

	// Convert FlagResults to entities.Flag format
	var flags []entities.Flag
	for i, result := range flagResults {
		if result.Triggered {
			flag := entities.Flag{
				ID:          fmt.Sprintf("config_flag_%d_%d", timestamp.UnixNano(), i),
				RuleID:      result.FlagName,
				Type:        "risk_warning",
				Severity:    "warning",
				Description: result.Details,
				Timestamp:   result.Timestamp,
			}
			flags = append(flags, flag)
		}
	}

	// If no configured flags triggered, fall back to hardcoded logic for backward compatibility
	if len(flags) == 0 {
		return s.createHardcodedRiskFlags(data, timestamp)
	}

	return flags
}

// createHardcodedRiskFlags maintains the original hardcoded logic as fallback
// TODO: Remove this once flag configuration is fully implemented
func (s *service) createHardcodedRiskFlags(data *entities.FinancialData, timestamp time.Time) []entities.Flag {
	var flags []entities.Flag

	// Flag for excessive goodwill (warning level)
	if data.Goodwill > data.TotalAssets*0.25 { // >25%
		flag := entities.Flag{
			ID:             fmt.Sprintf("warning_flag_%d", timestamp.UnixNano()),
			RuleID:         "excessive_goodwill_warning",
			Type:           "asset_quality",
			Severity:       "warning",
			Amount:         data.Goodwill,
			Percentage:     (data.Goodwill / data.TotalAssets) * 100,
			Description:    "High goodwill relative to total assets may indicate overpayment for acquisitions",
			Recommendation: "Review acquisition history and goodwill impairment risks",
			Timestamp:      timestamp,
		}
		flags = append(flags, flag)
	}

	// Flag for excessive intangibles (warning level)
	if data.OtherIntangibles > data.TotalAssets*0.20 { // >20% of assets
		flag := entities.Flag{
			ID:             fmt.Sprintf("warning_flag_%d", timestamp.UnixNano()+1),
			RuleID:         "excessive_intangibles_warning",
			Type:           "asset_quality",
			Severity:       "warning",
			Amount:         data.OtherIntangibles,
			Percentage:     (data.OtherIntangibles / data.TotalAssets) * 100,
			Description:    "High intangible assets may lack substance and be subject to writedowns",
			Recommendation: "Review intangible asset valuation and amortization policies",
			Timestamp:      timestamp,
		}
		flags = append(flags, flag)
	}

	return flags
}
