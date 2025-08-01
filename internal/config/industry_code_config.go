// Package config provides industry code configuration
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// IndustryCodeConfig represents the configuration for industry code mappings
type IndustryCodeConfig struct {
	// Version of the configuration schema
	Version string `json:"version"`
	
	// DefaultCode is returned when no match is found
	DefaultCode string `json:"default_code"`
	
	// Mappings contains the industry code mappings
	Mappings []IndustryMapping `json:"mappings"`
	
	// ValidationRules for industry codes
	ValidationRules []IndustryValidationRule `json:"validation_rules"`
}

// IndustryMapping represents a single industry code mapping
type IndustryMapping struct {
	// Name of the industry
	Name string `json:"name"`
	
	// Code is the industry code to use
	Code string `json:"code"`
	
	// Priority for matching (higher priority checked first)
	Priority int `json:"priority"`
	
	// Matchers define how to identify this industry
	Matchers IndustryMatchers `json:"matchers"`
	
	// SubIndustries for more specific classifications
	SubIndustries []SubIndustryMapping `json:"sub_industries,omitempty"`
}

// IndustryMatchers contains different ways to match an industry
type IndustryMatchers struct {
	// SICCodes that map to this industry
	SICCodes []string `json:"sic_codes,omitempty"`
	
	// NAICSCodes that map to this industry
	NAICSCodes []string `json:"naics_codes,omitempty"`
	
	// Keywords to search in company description
	Keywords []string `json:"keywords,omitempty"`
	
	// Patterns for regex matching
	Patterns []string `json:"patterns,omitempty"`
	
	// ExactNames for exact company name matching
	ExactNames []string `json:"exact_names,omitempty"`
}

// SubIndustryMapping represents a sub-industry classification
type SubIndustryMapping struct {
	Name     string           `json:"name"`
	Code     string           `json:"code"`
	Matchers IndustryMatchers `json:"matchers"`
}

// IndustryValidationRule represents a validation rule for industry codes
type IndustryValidationRule struct {
	Name         string                 `json:"name"`
	Type         string                 `json:"type"`
	Parameters   map[string]interface{} `json:"parameters"`
	ErrorMessage string                 `json:"error_message"`
}

// LoadIndustryCodeConfig loads industry code configuration from a file
func LoadIndustryCodeConfig(configPath string) (*IndustryCodeConfig, error) {
	// Check if config path is provided, otherwise use default
	if configPath == "" {
		configPath = os.Getenv("INDUSTRY_CODE_CONFIG_PATH")
		if configPath == "" {
			configPath = "config/datacleaner/industry_codes.json"
		}
	}
	
	// Read the configuration file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read industry code config file: %w", err)
	}
	
	// Parse the JSON configuration
	var config IndustryCodeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse industry code config: %w", err)
	}
	
	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid industry code config: %w", err)
	}
	
	return &config, nil
}

// Validate checks if the configuration is valid
func (c *IndustryCodeConfig) Validate() error {
	if c.Version == "" {
		return fmt.Errorf("configuration version is required")
	}
	
	if c.DefaultCode == "" {
		return fmt.Errorf("default code is required")
	}
	
	if len(c.Mappings) == 0 {
		return fmt.Errorf("at least one industry mapping is required")
	}
	
	// Check for duplicate codes
	codeMap := make(map[string]bool)
	for _, mapping := range c.Mappings {
		if mapping.Code == "" {
			return fmt.Errorf("industry code is required for %s", mapping.Name)
		}
		
		if codeMap[mapping.Code] {
			return fmt.Errorf("duplicate industry code: %s", mapping.Code)
		}
		codeMap[mapping.Code] = true
		
		// Validate matchers
		if !mapping.Matchers.HasAnyMatcher() {
			return fmt.Errorf("at least one matcher is required for industry %s", mapping.Name)
		}
	}
	
	return nil
}

// HasAnyMatcher checks if at least one matcher is defined
func (m *IndustryMatchers) HasAnyMatcher() bool {
	return len(m.SICCodes) > 0 || 
	       len(m.NAICSCodes) > 0 || 
	       len(m.Keywords) > 0 || 
	       len(m.Patterns) > 0 || 
	       len(m.ExactNames) > 0
}

// GetMappingByCode returns the mapping for a given industry code
func (c *IndustryCodeConfig) GetMappingByCode(code string) (*IndustryMapping, bool) {
	for i := range c.Mappings {
		if c.Mappings[i].Code == code {
			return &c.Mappings[i], true
		}
	}
	return nil, false
}
