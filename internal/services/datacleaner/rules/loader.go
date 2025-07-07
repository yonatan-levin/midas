package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// loader implements the RuleLoader interface
type loader struct{}

// NewRuleLoader creates a new rule loader instance
func NewRuleLoader() RuleLoader {
	return &loader{}
}

// LoadFromFile loads rules from a JSON file
func (l *loader) LoadFromFile(path string) (*entities.RulesConfig, error) {
	// Read file contents
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	// Parse JSON
	var config entities.RulesConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse JSON from %s: %w", path, err)
	}

	// Basic validation
	if err := l.validateConfig(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration in %s: %w", path, err)
	}

	return &config, nil
}

// LoadIndustryFromFile loads industry rules from a JSON file
func (l *loader) LoadIndustryFromFile(path string) (*entities.IndustryConfig, error) {
	// Read file contents
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	// Parse JSON
	var config entities.IndustryConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse JSON from %s: %w", path, err)
	}

	// Basic validation
	if err := l.validateIndustryConfig(&config); err != nil {
		return nil, fmt.Errorf("invalid industry configuration in %s: %w", path, err)
	}

	return &config, nil
}

// ValidateSchema validates rules against JSON schema
func (l *loader) ValidateSchema(rules *entities.RulesConfig, schemaPath string) error {
	// Read schema file
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	// Parse schema JSON
	var schema map[string]interface{}
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return fmt.Errorf("failed to parse schema JSON: %w", err)
	}

	// Convert rules to generic interface for validation
	rulesData, err := json.Marshal(rules)
	if err != nil {
		return fmt.Errorf("failed to marshal rules for validation: %w", err)
	}

	var rulesInterface map[string]interface{}
	if err := json.Unmarshal(rulesData, &rulesInterface); err != nil {
		return fmt.Errorf("failed to unmarshal rules for validation: %w", err)
	}

	// Perform basic schema validation
	if err := l.validateAgainstSchema(rulesInterface, schema); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	return nil
}

// Private helper methods

// validateConfig performs basic validation on rules configuration
func (l *loader) validateConfig(config *entities.RulesConfig) error {
	if config.Version == "" {
		return fmt.Errorf("version is required")
	}

	if len(config.Rules) == 0 {
		return fmt.Errorf("at least one rule is required")
	}

	// Validate each rule
	ruleIDs := make(map[string]bool)
	for i, rule := range config.Rules {
		if err := l.validateRuleConfig(&rule); err != nil {
			return fmt.Errorf("rule %d: %w", i, err)
		}

		// Check for duplicate IDs
		if ruleIDs[rule.ID] {
			return fmt.Errorf("duplicate rule ID: %s", rule.ID)
		}
		ruleIDs[rule.ID] = true
	}

	return nil
}

// validateIndustryConfig performs basic validation on industry configuration
func (l *loader) validateIndustryConfig(config *entities.IndustryConfig) error {
	if config.GICSCode == "" {
		return fmt.Errorf("GICS code is required")
	}

	if config.Name == "" {
		return fmt.Errorf("industry name is required")
	}

	// Validate overrides
	for i, override := range config.Overrides {
		if override.RuleID == "" {
			return fmt.Errorf("override %d: rule_id is required", i)
		}
	}

	// Validate special rules
	ruleIDs := make(map[string]bool)
	for i, rule := range config.SpecialRules {
		if err := l.validateRuleConfig(&rule); err != nil {
			return fmt.Errorf("special rule %d: %w", i, err)
		}

		// Check for duplicate IDs in special rules
		if ruleIDs[rule.ID] {
			return fmt.Errorf("duplicate special rule ID: %s", rule.ID)
		}
		ruleIDs[rule.ID] = true
	}

	return nil
}

// validateRuleConfig validates a single rule configuration
func (l *loader) validateRuleConfig(rule *entities.CleaningRule) error {
	if rule.ID == "" {
		return fmt.Errorf("rule ID is required")
	}

	if rule.Name == "" {
		return fmt.Errorf("rule name is required")
	}

	if len(rule.XBRLTags) == 0 {
		return fmt.Errorf("at least one XBRL tag is required")
	}

	// Validate category
	if !l.isValidCategory(rule.Category) {
		return fmt.Errorf("invalid category: %s", rule.Category)
	}

	// Validate adjustment
	if !l.isValidAdjustment(rule.Adjustment) {
		return fmt.Errorf("invalid adjustment: %s", rule.Adjustment)
	}

	// Validate severity
	if !l.isValidSeverity(rule.Severity) {
		return fmt.Errorf("invalid severity: %s", rule.Severity)
	}

	if len(rule.Industry) == 0 {
		return fmt.Errorf("at least one industry specification is required")
	}

	return nil
}

// validateAgainstSchema performs basic JSON schema validation
func (l *loader) validateAgainstSchema(data map[string]interface{}, schema map[string]interface{}) error {
	// Check required fields
	required, ok := schema["required"].([]interface{})
	if ok {
		for _, field := range required {
			fieldName, ok := field.(string)
			if !ok {
				continue
			}

			if _, exists := data[fieldName]; !exists {
				return fmt.Errorf("required field '%s' is missing", fieldName)
			}
		}
	}

	// Check properties types
	properties, ok := schema["properties"].(map[string]interface{})
	if ok {
		for fieldName, fieldSchema := range properties {
			if fieldValue, exists := data[fieldName]; exists {
				if err := l.validateFieldType(fieldName, fieldValue, fieldSchema); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// validateFieldType validates a field against its schema definition
func (l *loader) validateFieldType(fieldName string, value interface{}, schema interface{}) error {
	schemaMap, ok := schema.(map[string]interface{})
	if !ok {
		return nil // Skip validation if schema is not a map
	}

	expectedType, ok := schemaMap["type"].(string)
	if !ok {
		return nil // Skip validation if type is not specified
	}

	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("field '%s' must be a string", fieldName)
		}
	case "array":
		if _, ok := value.([]interface{}); !ok {
			return fmt.Errorf("field '%s' must be an array", fieldName)
		}
	case "object":
		if _, ok := value.(map[string]interface{}); !ok {
			return fmt.Errorf("field '%s' must be an object", fieldName)
		}
	case "number":
		switch value.(type) {
		case float64, int, int64:
			// Valid number types
		default:
			return fmt.Errorf("field '%s' must be a number", fieldName)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field '%s' must be a boolean", fieldName)
		}
	}

	return nil
}

// Helper validation functions

func (l *loader) isValidCategory(category entities.RuleCategory) bool {
	switch category {
	case entities.AssetQuality, entities.LiabilityCompleteness, entities.EarningsNormalization:
		return true
	default:
		return false
	}
}

func (l *loader) isValidAdjustment(adjustment entities.AdjustmentType) bool {
	switch adjustment {
	case entities.Exclude, entities.Writedown, entities.Reclassify, entities.TreatAsDebt, entities.FlagForReview:
		return true
	default:
		return false
	}
}

func (l *loader) isValidSeverity(severity entities.FlagSeverity) bool {
	switch severity {
	case entities.Info, entities.Warning, entities.Critical:
		return true
	default:
		return false
	}
}

// Additional helper functions for advanced rule processing

// NormalizeRuleID normalizes a rule ID to a standard format
func NormalizeRuleID(id string) string {
	// Convert to lowercase and replace spaces with underscores
	normalized := strings.ToLower(id)
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, "-", "_")
	return normalized
}

// ValidateXBRLTag checks if an XBRL tag follows proper formatting
func ValidateXBRLTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("XBRL tag cannot be empty")
	}

	// Basic XBRL tag validation - should start with capital letter
	if len(tag) > 0 && (tag[0] < 'A' || tag[0] > 'Z') {
		return fmt.Errorf("XBRL tag '%s' should start with a capital letter", tag)
	}

	return nil
}

// GetRulePriority calculates rule priority based on category and severity
func GetRulePriority(category entities.RuleCategory, severity entities.FlagSeverity) int {
	// Base priority by category
	var basePriority int
	switch category {
	case entities.AssetQuality:
		basePriority = 100
	case entities.LiabilityCompleteness:
		basePriority = 200
	case entities.EarningsNormalization:
		basePriority = 300
	default:
		basePriority = 999
	}

	// Severity modifier
	var severityModifier int
	switch severity {
	case entities.Critical:
		severityModifier = 10
	case entities.Warning:
		severityModifier = 20
	case entities.Info:
		severityModifier = 30
	default:
		severityModifier = 50
	}

	return basePriority + severityModifier
}
