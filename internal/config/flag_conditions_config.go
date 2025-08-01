// Package config provides flag conditions configuration
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FlagConditionsConfig represents the configuration for flag conditions
type FlagConditionsConfig struct {
	// Version of the configuration schema
	Version string `json:"version"`
	
	// Flags contains all flag configurations
	Flags []FlagConfig `json:"flags"`
	
	// GlobalVariables for use in conditions
	GlobalVariables map[string]interface{} `json:"global_variables,omitempty"`
}

// FlagConfig represents a single flag configuration
type FlagConfig struct {
	// Name is the unique identifier for the flag
	Name string `json:"name"`
	
	// Description of what this flag represents
	Description string `json:"description"`
	
	// Enabled determines if this flag should be evaluated
	Enabled bool `json:"enabled"`
	
	// Priority for evaluation order (higher priority evaluated first)
	Priority int `json:"priority"`
	
	// Conditions that must be met for the flag to be set
	Conditions ConditionGroup `json:"conditions"`
	
	// Actions to perform when flag is triggered
	Actions []FlagAction `json:"actions,omitempty"`
	
	// Metadata for additional flag information
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ConditionGroup represents a group of conditions with logical operators
type ConditionGroup struct {
	// Operator is the logical operator (AND, OR, NOT)
	Operator string `json:"operator"`
	
	// Conditions in this group
	Conditions []Condition `json:"conditions,omitempty"`
	
	// Groups for nested condition groups
	Groups []ConditionGroup `json:"groups,omitempty"`
}

// Condition represents a single condition to evaluate
type Condition struct {
	// Type of condition (numeric, string, boolean, date, exists, regex)
	Type string `json:"type"`
	
	// Field to evaluate (supports dot notation for nested fields)
	Field string `json:"field"`
	
	// Operator for comparison (eq, ne, gt, lt, gte, lte, contains, matches, in, between)
	Operator string `json:"operator"`
	
	// Value to compare against (can be static or reference to another field)
	Value interface{} `json:"value"`
	
	// CaseSensitive for string comparisons
	CaseSensitive bool `json:"case_sensitive,omitempty"`
	
	// NullBehavior defines how to handle null/missing values (ignore, false, true)
	NullBehavior string `json:"null_behavior,omitempty"`
}

// FlagAction represents an action to take when a flag is triggered
type FlagAction struct {
	// Type of action (set_field, log, alert, transform)
	Type string `json:"type"`
	
	// Parameters for the action
	Parameters map[string]interface{} `json:"parameters"`
}

// LoadFlagConditionsConfig loads flag conditions configuration from a file
func LoadFlagConditionsConfig(configPath string) (*FlagConditionsConfig, error) {
	// Check if config path is provided, otherwise use default
	if configPath == "" {
		configPath = os.Getenv("FLAG_CONDITIONS_CONFIG_PATH")
		if configPath == "" {
			configPath = "config/datacleaner/flag_conditions.json"
		}
	}
	
	// Read the configuration file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read flag conditions config file: %w", err)
	}
	
	// Parse the JSON configuration
	var config FlagConditionsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse flag conditions config: %w", err)
	}
	
	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid flag conditions config: %w", err)
	}
	
	return &config, nil
}

// Validate checks if the configuration is valid
func (c *FlagConditionsConfig) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("configuration version is required")
	}
	
	// Check for duplicate flag names
	flagNames := make(map[string]bool)
	for _, flag := range c.Flags {
		if flag.Name == "" {
			return fmt.Errorf("flag name is required")
		}
		
		if flagNames[flag.Name] {
			return fmt.Errorf("duplicate flag name: %s", flag.Name)
		}
		flagNames[flag.Name] = true
		
		// Validate conditions
		if err := validateConditionGroup(flag.Conditions); err != nil {
			return fmt.Errorf("invalid conditions for flag %s: %w", flag.Name, err)
		}
		
		// Validate actions
		for _, action := range flag.Actions {
			if action.Type == "" {
				return fmt.Errorf("action type is required for flag %s", flag.Name)
			}
		}
	}
	
	return nil
}

// validateConditionGroup validates a condition group recursively
func validateConditionGroup(group ConditionGroup) error {
	validOperators := map[string]bool{
		"AND": true,
		"OR":  true,
		"NOT": true,
	}
	
	if !validOperators[strings.ToUpper(group.Operator)] {
		return fmt.Errorf("invalid operator: %s", group.Operator)
	}
	
	// Must have either conditions or groups
	if len(group.Conditions) == 0 && len(group.Groups) == 0 {
		return fmt.Errorf("condition group must have at least one condition or nested group")
	}
	
	// Validate individual conditions
	for _, condition := range group.Conditions {
		if err := validateCondition(condition); err != nil {
			return err
		}
	}
	
	// Validate nested groups
	for _, nestedGroup := range group.Groups {
		if err := validateConditionGroup(nestedGroup); err != nil {
			return err
		}
	}
	
	return nil
}

// validateCondition validates a single condition
func validateCondition(condition Condition) error {
	validTypes := map[string]bool{
		"numeric": true,
		"string":  true,
		"boolean": true,
		"date":    true,
		"exists":  true,
		"regex":   true,
	}
	
	if !validTypes[condition.Type] {
		return fmt.Errorf("invalid condition type: %s", condition.Type)
	}
	
	if condition.Field == "" {
		return fmt.Errorf("condition field is required")
	}
	
	validOperators := map[string]bool{
		"eq":       true,
		"ne":       true,
		"gt":       true,
		"lt":       true,
		"gte":      true,
		"lte":      true,
		"contains": true,
		"matches":  true,
		"in":       true,
		"between":  true,
	}
	
	if !validOperators[condition.Operator] {
		return fmt.Errorf("invalid operator: %s", condition.Operator)
	}
	
	// Validate null behavior
	if condition.NullBehavior != "" {
		validNullBehaviors := map[string]bool{
			"ignore": true,
			"false":  true,
			"true":   true,
		}
		
		if !validNullBehaviors[condition.NullBehavior] {
			return fmt.Errorf("invalid null behavior: %s", condition.NullBehavior)
		}
	}
	
	return nil
}

// GetFlagByName returns a flag configuration by name
func (c *FlagConditionsConfig) GetFlagByName(name string) (*FlagConfig, bool) {
	for i := range c.Flags {
		if c.Flags[i].Name == name {
			return &c.Flags[i], true
		}
	}
	return nil, false
}

// GetEnabledFlags returns all enabled flags sorted by priority
func (c *FlagConditionsConfig) GetEnabledFlags() []FlagConfig {
	var enabled []FlagConfig
	
	for _, flag := range c.Flags {
		if flag.Enabled {
			enabled = append(enabled, flag)
		}
	}
	
	// Sort by priority (descending)
	for i := 0; i < len(enabled)-1; i++ {
		for j := i + 1; j < len(enabled); j++ {
			if enabled[i].Priority < enabled[j].Priority {
				enabled[i], enabled[j] = enabled[j], enabled[i]
			}
		}
	}
	
	return enabled
}
