// Package config provides configuration structures for XBRL tag matching
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// XBRLTagConfig represents the configuration for XBRL tag mappings
type XBRLTagConfig struct {
	// Version of the configuration schema
	Version string `json:"version"`
	
	// TagMappings contains the mapping of XBRL tags to internal field names
	TagMappings map[string]XBRLTagMapping `json:"tag_mappings"`
	
	// DefaultNamespace is the default XBRL namespace to use
	DefaultNamespace string `json:"default_namespace"`
	
	// ValidationRules contains rules for validating XBRL data
	ValidationRules []ValidationRule `json:"validation_rules"`
}

// XBRLTagMapping represents a single XBRL tag mapping
type XBRLTagMapping struct {
	// XBRLTag is the XBRL tag name (e.g., "us-gaap:Assets")
	XBRLTag string `json:"xbrl_tag"`
	
	// InternalField is the internal field name to map to
	InternalField string `json:"internal_field"`
	
	// DataType specifies the expected data type
	DataType string `json:"data_type"`
	
	// Required indicates if this tag is required
	Required bool `json:"required"`
	
	// Transformations to apply to the value
	Transformations []string `json:"transformations,omitempty"`
	
	// AlternativeTags are fallback tags to check if primary is not found
	AlternativeTags []string `json:"alternative_tags,omitempty"`
	
	// Context specifies the context requirements (instant, duration, etc.)
	Context string `json:"context,omitempty"`
}

// ValidationRule represents a validation rule for XBRL data
type ValidationRule struct {
	// Name of the validation rule
	Name string `json:"name"`
	
	// Type of validation (range, format, consistency, etc.)
	Type string `json:"type"`
	
	// Field to validate
	Field string `json:"field"`
	
	// Parameters for the validation
	Parameters map[string]interface{} `json:"parameters"`
	
	// ErrorMessage to display on validation failure
	ErrorMessage string `json:"error_message"`
}

// LoadXBRLConfig loads XBRL configuration from a file
func LoadXBRLConfig(configPath string) (*XBRLTagConfig, error) {
	// Check if config path is provided, otherwise use default
	if configPath == "" {
		configPath = os.Getenv("XBRL_CONFIG_PATH")
		if configPath == "" {
			configPath = "config/datacleaner/xbrl_tag_mappings.json"
		}
	}
	
	// Read the configuration file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read XBRL config file: %w", err)
	}
	
	// Parse the JSON configuration
	var config XBRLTagConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse XBRL config: %w", err)
	}
	
	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid XBRL config: %w", err)
	}
	
	return &config, nil
}

// Validate checks if the configuration is valid
func (c *XBRLTagConfig) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("configuration version is required")
	}
	
	if len(c.TagMappings) == 0 {
		return fmt.Errorf("at least one tag mapping is required")
	}
	
	// Validate each tag mapping
	for key, mapping := range c.TagMappings {
		if mapping.XBRLTag == "" {
			return fmt.Errorf("XBRL tag is required for mapping %s", key)
		}
		
		if mapping.InternalField == "" {
			return fmt.Errorf("internal field is required for mapping %s", key)
		}
		
		// Validate data type
		validDataTypes := map[string]bool{
			"string": true,
			"number": true,
			"decimal": true,
			"boolean": true,
			"date": true,
			"duration": true,
		}
		
		if !validDataTypes[mapping.DataType] {
			return fmt.Errorf("invalid data type %s for mapping %s", mapping.DataType, key)
		}
	}
	
	return nil
}

// GetMappingByXBRLTag returns the mapping for a given XBRL tag
func (c *XBRLTagConfig) GetMappingByXBRLTag(xbrlTag string) (*XBRLTagMapping, bool) {
	for _, mapping := range c.TagMappings {
		if mapping.XBRLTag == xbrlTag {
			return &mapping, true
		}
		
		// Check alternative tags
		for _, altTag := range mapping.AlternativeTags {
			if altTag == xbrlTag {
				return &mapping, true
			}
		}
	}
	
	return nil, false
}

// GetMappingByInternalField returns the mapping for a given internal field
func (c *XBRLTagConfig) GetMappingByInternalField(field string) (*XBRLTagMapping, bool) {
	for _, mapping := range c.TagMappings {
		if mapping.InternalField == field {
			return &mapping, true
		}
	}
	
	return nil, false
}

// GetRequiredMappings returns all required tag mappings
func (c *XBRLTagConfig) GetRequiredMappings() []XBRLTagMapping {
	var required []XBRLTagMapping
	
	for _, mapping := range c.TagMappings {
		if mapping.Required {
			required = append(required, mapping)
		}
	}
	
	return required
}
