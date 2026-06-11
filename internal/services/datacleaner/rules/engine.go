package rules

import (
	"fmt"
	"sync"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// engine implements the RuleEngine interface
type engine struct {
	rules         map[string]*entities.CleaningRule  // Rules indexed by ID
	industryRules map[string][]entities.CleaningRule // Rules indexed by GICS code
	rulesConfig   *entities.RulesConfig              // Original loaded configuration
	loader        RuleLoader                         // Rule loader instance
	mu            sync.RWMutex                       // Thread-safe access
}

// NewRuleEngine creates a new rules engine instance
func NewRuleEngine() RuleEngine {
	return &engine{
		rules:         make(map[string]*entities.CleaningRule),
		industryRules: make(map[string][]entities.CleaningRule),
		loader:        NewRuleLoader(),
	}
}

// LoadRules loads rules from configuration file
func (e *engine) LoadRules(configPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Load rules from file
	config, err := e.loader.LoadFromFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to load rules from %s: %w", configPath, err)
	}

	// Store the configuration
	e.rulesConfig = config

	// Clear existing rules
	e.rules = make(map[string]*entities.CleaningRule)

	// Index rules by ID
	for i := range config.Rules {
		rule := &config.Rules[i]
		e.rules[rule.ID] = rule
	}

	return nil
}

// LoadIndustryRules loads industry-specific rule overrides.
//
// SR-1 B2 fix: overrides and special rules are applied to a per-industry
// SNAPSHOT built from value copies of the base rules — the base rule set
// (e.rules) is NEVER mutated. Pre-fix, the overrides wrote through the shared
// *CleaningRule pointers and special rules were injected into the base index,
// so the first industry load (this method runs per-clean for 45/25 tickers)
// permanently installed that industry's thresholds / severities / enabled
// flags into every subsequent ticker's rule set, making cleaning results
// order-dependent across the process lifetime.
//
// Re-loading the same industry replaces its snapshot wholesale, so repeated
// loads are idempotent against the pristine base.
func (e *engine) LoadIndustryRules(industryPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Load industry configuration
	industryConfig, err := e.loader.LoadIndustryFromFile(industryPath)
	if err != nil {
		return fmt.Errorf("failed to load industry rules from %s: %w", industryPath, err)
	}

	// Index overrides by rule ID for the copy loop below.
	overrides := make(map[string]entities.IndustryRuleOverride, len(industryConfig.Overrides))
	for _, override := range industryConfig.Overrides {
		overrides[override.RuleID] = override
	}

	// Build the industry snapshot from VALUE COPIES of the base rules,
	// applying overrides to the copies only.
	industryRules := make([]entities.CleaningRule, 0, len(e.rules)+len(industryConfig.SpecialRules))
	for _, rule := range e.rules {
		ruleCopy := *rule // base stays pristine; the copy carries the overrides
		if override, exists := overrides[ruleCopy.ID]; exists {
			if override.Enabled != nil {
				ruleCopy.Enabled = *override.Enabled
			}
			if override.Threshold != nil {
				ruleCopy.Threshold = override.Threshold
			}
			if override.Severity != nil {
				ruleCopy.Severity = *override.Severity
			}
		}
		industryRules = append(industryRules, ruleCopy)
	}

	// Special industry rules live ONLY in the snapshot — they are not added
	// to the base index (GetRuleByID resolves them via its snapshot fallback).
	industryRules = append(industryRules, industryConfig.SpecialRules...)

	// Store industry-specific rules
	e.industryRules[industryConfig.GICSCode] = industryRules

	return nil
}

// GetRules returns all loaded rules, optionally filtered by category
func (e *engine) GetRules(category *entities.RuleCategory) []entities.CleaningRule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []entities.CleaningRule

	for _, rule := range e.rules {
		// Filter by category if specified
		if category != nil && rule.Category != *category {
			continue
		}

		// Only include enabled rules
		if rule.Enabled {
			result = append(result, *rule)
		}
	}

	return result
}

// GetIndustryRules returns rules for a specific industry
func (e *engine) GetIndustryRules(industry string) []entities.CleaningRule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Return industry-specific rules if available
	if industry != "" {
		if rules, exists := e.industryRules[industry]; exists {
			// Filter for enabled rules only
			var enabledRules []entities.CleaningRule
			for _, rule := range rules {
				if rule.Enabled {
					enabledRules = append(enabledRules, rule)
				}
			}
			return enabledRules
		}
	}

	// Fallback to general rules applicable to this industry
	var result []entities.CleaningRule
	for _, rule := range e.rules {
		if rule.Enabled {
			// If no industry specified, return all general rules (those marked as "all" industry)
			if industry == "" {
				if e.isRuleApplicableToIndustry(rule, "all") || len(rule.Industry) == 0 {
					result = append(result, *rule)
				}
			} else {
				// For specific industry, check applicability
				if e.isRuleApplicableToIndustry(rule, industry) {
					result = append(result, *rule)
				}
			}
		}
	}

	return result
}

// GetRulesByCategory returns all enabled rules for a specific category
func (e *engine) GetRulesByCategory(category entities.RuleCategory) []entities.CleaningRule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []entities.CleaningRule

	// Check base rules
	for _, rule := range e.rules {
		if rule.Enabled && rule.Category == category {
			result = append(result, *rule)
		}
	}

	// Check industry-specific rules
	for _, industryRules := range e.industryRules {
		for _, rule := range industryRules {
			if rule.Enabled && rule.Category == category {
				// Avoid duplicates by checking if rule is already in result
				isDuplicate := false
				for _, existingRule := range result {
					if existingRule.ID == rule.ID {
						isDuplicate = true
						break
					}
				}
				if !isDuplicate {
					result = append(result, rule)
				}
			}
		}
	}

	return result
}

// ValidateRules validates loaded rules for consistency
func (e *engine) ValidateRules() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.rules) == 0 {
		return fmt.Errorf("no rules loaded")
	}

	// Check for circular dependencies
	if err := e.validateDependencies(); err != nil {
		return err
	}

	// Validate individual rules
	for id, rule := range e.rules {
		if err := e.validateRule(rule); err != nil {
			return fmt.Errorf("invalid rule %s: %w", id, err)
		}
	}

	return nil
}

// GetRuleByID returns a specific rule by ID.
//
// Base rules take precedence and are returned in their PRISTINE (un-overridden)
// form — industry overrides live only in the per-industry snapshots (SR-1 B2).
// Industry-only special rules are resolved via a read-only fallback scan over
// the snapshots so callers that look special rules up by ID keep working.
// Special-rule IDs are unique across the shipped industry files; if two
// industries ever declare the same special ID, the first snapshot scanned wins
// (map order) — callers needing industry-specific resolution should read
// GetIndustryRules(gicsCode) instead.
func (e *engine) GetRuleByID(id string) (*entities.CleaningRule, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if rule, exists := e.rules[id]; exists {
		// Return a copy to prevent modification
		ruleCopy := *rule
		return &ruleCopy, nil
	}

	// Fallback: industry-only special rules live in the snapshots.
	for _, industryRules := range e.industryRules {
		for i := range industryRules {
			if industryRules[i].ID == id {
				ruleCopy := industryRules[i]
				return &ruleCopy, nil
			}
		}
	}

	return nil, fmt.Errorf("rule with ID %s not found", id)
}

// GetRuleVersion returns the version of loaded rules
func (e *engine) GetRuleVersion() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.rulesConfig == nil {
		return ""
	}

	return e.rulesConfig.Version
}

// Private helper methods

// isRuleApplicableToIndustry checks if a rule applies to a specific industry
func (e *engine) isRuleApplicableToIndustry(rule *entities.CleaningRule, industry string) bool {
	for _, applicableIndustry := range rule.Industry {
		if applicableIndustry == "all" || applicableIndustry == industry {
			return true
		}
	}
	return false
}

// validateDependencies checks for circular dependencies in rules
func (e *engine) validateDependencies() error {
	// Use DFS to detect cycles
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	for ruleID := range e.rules {
		if !visited[ruleID] {
			if e.hasCyclicDependency(ruleID, visited, recStack) {
				return fmt.Errorf("circular dependency detected involving rule %s", ruleID)
			}
		}
	}

	return nil
}

// hasCyclicDependency performs DFS to detect circular dependencies
func (e *engine) hasCyclicDependency(ruleID string, visited, recStack map[string]bool) bool {
	visited[ruleID] = true
	recStack[ruleID] = true

	rule, exists := e.rules[ruleID]
	if !exists {
		return false
	}

	// Check all dependencies
	for _, depID := range rule.Dependencies {
		if !visited[depID] {
			if e.hasCyclicDependency(depID, visited, recStack) {
				return true
			}
		} else if recStack[depID] {
			return true // Back edge found - circular dependency
		}
	}

	recStack[ruleID] = false
	return false
}

// validateRule validates a single rule for correctness
func (e *engine) validateRule(rule *entities.CleaningRule) error {
	// Check required fields
	if rule.ID == "" {
		return fmt.Errorf("rule ID cannot be empty")
	}

	if rule.Name == "" {
		return fmt.Errorf("rule name cannot be empty")
	}

	if len(rule.XBRLTags) == 0 {
		return fmt.Errorf("rule must have at least one XBRL tag")
	}

	// Validate category
	switch rule.Category {
	case entities.AssetQuality, entities.LiabilityCompleteness, entities.EarningsNormalization:
		// Valid categories
	default:
		return fmt.Errorf("invalid rule category: %s", rule.Category)
	}

	// Validate adjustment type
	switch rule.Adjustment {
	case entities.Exclude, entities.Writedown, entities.Reclassify, entities.TreatAsDebt, entities.FlagForReview:
		// Valid adjustment types
	default:
		return fmt.Errorf("invalid adjustment type: %s", rule.Adjustment)
	}

	// Validate severity
	switch rule.Severity {
	case entities.Info, entities.Warning, entities.Critical:
		// Valid severities
	default:
		return fmt.Errorf("invalid severity: %s", rule.Severity)
	}

	// Validate threshold values if present
	if rule.Threshold != nil {
		if err := e.validateThreshold(rule.Threshold); err != nil {
			return fmt.Errorf("invalid threshold: %w", err)
		}
	}

	// Check that all dependencies exist
	for _, depID := range rule.Dependencies {
		if _, exists := e.rules[depID]; !exists {
			return fmt.Errorf("dependency rule %s not found", depID)
		}
	}

	return nil
}

// validateThreshold validates threshold configuration
func (e *engine) validateThreshold(threshold *entities.ThresholdConfig) error {
	// Check percentage thresholds are valid (0-100%)
	if threshold.PercentageOfRevenue != nil {
		if *threshold.PercentageOfRevenue < 0 || *threshold.PercentageOfRevenue > 1 {
			return fmt.Errorf("percentage_of_revenue must be between 0 and 1")
		}
	}

	if threshold.PercentageOfAssets != nil {
		if *threshold.PercentageOfAssets < 0 || *threshold.PercentageOfAssets > 1 {
			return fmt.Errorf("percentage_of_assets must be between 0 and 1")
		}
	}

	if threshold.PercentageOfEquity != nil {
		if *threshold.PercentageOfEquity < 0 || *threshold.PercentageOfEquity > 1 {
			return fmt.Errorf("percentage_of_equity must be between 0 and 1")
		}
	}

	// Check growth and decline ratios
	if threshold.GrowthMultiple != nil && *threshold.GrowthMultiple < 1 {
		return fmt.Errorf("growth_multiple must be >= 1")
	}

	if threshold.TurnoverDecline != nil {
		if *threshold.TurnoverDecline < 0 || *threshold.TurnoverDecline > 1 {
			return fmt.Errorf("turnover_decline must be between 0 and 1")
		}
	}

	if threshold.WritedownRate != nil {
		if *threshold.WritedownRate < 0 || *threshold.WritedownRate > 1 {
			return fmt.Errorf("writedown_rate must be between 0 and 1")
		}
	}

	// Check age constraints
	if threshold.AgeInYears != nil && *threshold.AgeInYears < 0 {
		return fmt.Errorf("age_in_years must be >= 0")
	}

	return nil
}
