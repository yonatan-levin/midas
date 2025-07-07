package rules

import (
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// RuleEngine defines the interface for the rules processing engine
type RuleEngine interface {
	// LoadRules loads rules from configuration
	LoadRules(configPath string) error
	// LoadIndustryRules loads industry-specific rule overrides
	LoadIndustryRules(industryPath string) error
	// GetRules returns all loaded rules, optionally filtered by category
	GetRules(category *entities.RuleCategory) []entities.CleaningRule
	// GetIndustryRules returns rules for a specific industry
	GetIndustryRules(industry string) []entities.CleaningRule
	// GetRulesByCategory returns all enabled rules for a specific category
	GetRulesByCategory(category entities.RuleCategory) []entities.CleaningRule
	// ValidateRules validates loaded rules for consistency
	ValidateRules() error
	// GetRuleByID returns a specific rule by ID
	GetRuleByID(id string) (*entities.CleaningRule, error)
	// GetRuleVersion returns the version of loaded rules
	GetRuleVersion() string
}

// RuleLoader defines the interface for loading rules from external sources
type RuleLoader interface {
	// LoadFromFile loads rules from a JSON file
	LoadFromFile(path string) (*entities.RulesConfig, error)
	// LoadIndustryFromFile loads industry rules from a JSON file
	LoadIndustryFromFile(path string) (*entities.IndustryConfig, error)
	// ValidateSchema validates rules against JSON schema
	ValidateSchema(rules *entities.RulesConfig, schemaPath string) error
}
