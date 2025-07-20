package leases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ConfigLoader handles loading and parsing of lease estimation configuration
type ConfigLoader struct {
	configPath string
	config     *EstimationConfig
	lastLoaded time.Time
}

// NewConfigLoader creates a new configuration loader
func NewConfigLoader(configPath string) *ConfigLoader {
	return &ConfigLoader{
		configPath: configPath,
	}
}

// LoadConfig loads the configuration from JSON file
func (cl *ConfigLoader) LoadConfig() (*EstimationConfig, error) {
	// Check if we need to reload (TTL-based or file change detection)
	if cl.shouldReload() {
		if err := cl.loadFromFile(); err != nil {
			return nil, fmt.Errorf("failed to load configuration from %s: %w", cl.configPath, err)
		}
	}
	
	return cl.config, nil
}

// shouldReload determines if configuration should be reloaded
func (cl *ConfigLoader) shouldReload() bool {
	// Always reload if config is nil
	if cl.config == nil {
		return true
	}
	
	// Check if file has been modified since last load
	if fileInfo, err := os.Stat(cl.configPath); err == nil {
		if fileInfo.ModTime().After(cl.lastLoaded) {
			return true
		}
	}
	
	// Check TTL (reload every 5 minutes)
	if time.Since(cl.lastLoaded) > 5*time.Minute {
		return true
	}
	
	return false
}

// loadFromFile loads configuration from the JSON file
func (cl *ConfigLoader) loadFromFile() error {
	// Read the configuration file
	data, err := os.ReadFile(cl.configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	
	// Parse JSON configuration
	var configData map[string]interface{}
	if err := json.Unmarshal(data, &configData); err != nil {
		return fmt.Errorf("failed to parse JSON config: %w", err)
	}
	
	// Extract configuration values
	config := &EstimationConfig{
		// Set defaults first
		DiscountRateMethod:     "incremental_borrowing_rate",
		LeaseTermMethod:        "disclosed_commitments",
		PaymentMethod:          "schedule_extraction",
		DefaultDiscountRate:    0.06,
		DefaultLeaseTermYears:  10,
		DefaultEscalationRate:  0.03,
		MinimumRate:            0.005,
		MaximumRate:            0.25,
		MinimumConfidenceScore: 0.6,
		CacheEnabled:           true,
		CacheTTL:               1 * time.Hour,
		CalculationTimeout:     30 * time.Second,
		IndustryAdjustments:    make(map[string]IndustryAdjustment),
	}
	
	// Parse estimation methods
	if methods, ok := configData["estimation_methods"].(map[string]interface{}); ok {
		if discountRate, ok := methods["discount_rate"].(map[string]interface{}); ok {
			if method, ok := discountRate["primary_method"].(string); ok {
				config.DiscountRateMethod = method
			}
		}
		
		if leaseTerm, ok := methods["lease_term"].(map[string]interface{}); ok {
			if method, ok := leaseTerm["primary_method"].(string); ok {
				config.LeaseTermMethod = method
			}
		}
		
		if payment, ok := methods["payment_schedule"].(map[string]interface{}); ok {
			if method, ok := payment["primary_method"].(string); ok {
				config.PaymentMethod = method
			}
		}
	}
	
	// Parse fallback parameters
	if fallback, ok := configData["fallback_parameters"].(map[string]interface{}); ok {
		if rate, ok := fallback["default_discount_rate"].(float64); ok {
			config.DefaultDiscountRate = rate
		}
		if years, ok := fallback["default_lease_term_years"].(float64); ok {
			config.DefaultLeaseTermYears = int(years)
		}
		if escalation, ok := fallback["default_escalation_rate"].(float64); ok {
			config.DefaultEscalationRate = escalation
		}
	}
	
	// Parse industry defaults
	if industry, ok := configData["industry_defaults"].(map[string]interface{}); ok {
		for industryCode, settings := range industry {
			if settingsMap, ok := settings.(map[string]interface{}); ok {
				adjustment := IndustryAdjustment{}
				
				if adj, ok := settingsMap["discount_rate_adjustment"].(float64); ok {
					adjustment.DiscountRateAdjustment = adj
				}
				if years, ok := settingsMap["typical_lease_term_years"].(float64); ok {
					adjustment.TypicalLeaseTermYears = int(years)
				}
				if escalation, ok := settingsMap["escalation_rate"].(float64); ok {
					adjustment.EscalationRate = escalation
				}
				if threshold, ok := settingsMap["materiality_threshold"].(float64); ok {
					adjustment.MaterialityThreshold = threshold
				}
				
				config.IndustryAdjustments[industryCode] = adjustment
			}
		}
	}
	
	// Parse performance settings
	if performance, ok := configData["performance"].(map[string]interface{}); ok {
		if caching, ok := performance["caching"].(map[string]interface{}); ok {
			if enabled, ok := caching["enabled"].(bool); ok {
				config.CacheEnabled = enabled
			}
			if ttl, ok := caching["cache_ttl_seconds"].(float64); ok {
				config.CacheTTL = time.Duration(ttl) * time.Second
			}
		}
		if rateLimit, ok := performance["rate_limiting"].(map[string]interface{}); ok {
			if timeout, ok := rateLimit["calculation_timeout_seconds"].(float64); ok {
				config.CalculationTimeout = time.Duration(timeout) * time.Second
			}
		}
	}
	
	// Parse validation settings
	if validation, ok := configData["validation"].(map[string]interface{}); ok {
		if quality, ok := validation["data_quality"].(map[string]interface{}); ok {
			if confidence, ok := quality["minimum_confidence_score"].(float64); ok {
				config.MinimumConfidenceScore = confidence
			}
		}
		if reasonableness, ok := validation["reasonableness_checks"].(map[string]interface{}); ok {
			if minRate, ok := reasonableness["minimum_discount_rate"].(float64); ok {
				config.MinimumRate = minRate
			}
			if maxRate, ok := reasonableness["maximum_discount_rate"].(float64); ok {
				config.MaximumRate = maxRate
			}
		}
	}
	
	// Validate the configuration
	if err := cl.validateConfig(config); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}
	
	cl.config = config
	cl.lastLoaded = time.Now()
	
	return nil
}

// validateConfig validates the loaded configuration
func (cl *ConfigLoader) validateConfig(config *EstimationConfig) error {
	// Validate discount rate bounds
	if config.DefaultDiscountRate < config.MinimumRate || config.DefaultDiscountRate > config.MaximumRate {
		return fmt.Errorf("default discount rate %.3f is outside valid range [%.3f, %.3f]", 
			config.DefaultDiscountRate, config.MinimumRate, config.MaximumRate)
	}
	
	// Validate lease term
	if config.DefaultLeaseTermYears < 1 || config.DefaultLeaseTermYears > 100 {
		return fmt.Errorf("default lease term %d years is outside valid range [1, 100]", 
			config.DefaultLeaseTermYears)
	}
	
	// Validate escalation rate
	if config.DefaultEscalationRate < -0.5 || config.DefaultEscalationRate > 1.0 {
		return fmt.Errorf("default escalation rate %.3f is outside valid range [-0.5, 1.0]", 
			config.DefaultEscalationRate)
	}
	
	// Validate confidence score
	if config.MinimumConfidenceScore < 0.0 || config.MinimumConfidenceScore > 1.0 {
		return fmt.Errorf("minimum confidence score %.3f is outside valid range [0.0, 1.0]", 
			config.MinimumConfidenceScore)
	}
	
	// Validate methods
	validDiscountMethods := map[string]bool{
		"incremental_borrowing_rate": true,
		"cost_of_debt":               true,
		"risk_free_plus_spread":      true,
		"industry_average":           true,
	}
	if !validDiscountMethods[config.DiscountRateMethod] {
		return fmt.Errorf("invalid discount rate method: %s", config.DiscountRateMethod)
	}
	
	validLeaseTermMethods := map[string]bool{
		"disclosed_commitments": true,
		"historical_analysis":   true,
		"industry_benchmarks":   true,
	}
	if !validLeaseTermMethods[config.LeaseTermMethod] {
		return fmt.Errorf("invalid lease term method: %s", config.LeaseTermMethod)
	}
	
	validPaymentMethods := map[string]bool{
		"schedule_extraction": true,
		"straight_line":       true,
		"growth_adjusted":     true,
	}
	if !validPaymentMethods[config.PaymentMethod] {
		return fmt.Errorf("invalid payment method: %s", config.PaymentMethod)
	}
	
	return nil
}

// GetDefaultConfig returns a default configuration for testing or fallback
func GetDefaultConfig() *EstimationConfig {
	return &EstimationConfig{
		DiscountRateMethod:     "incremental_borrowing_rate",
		LeaseTermMethod:        "disclosed_commitments",
		PaymentMethod:          "schedule_extraction",
		DefaultDiscountRate:    0.06,
		DefaultLeaseTermYears:  10,
		DefaultEscalationRate:  0.03,
		MinimumRate:            0.005,
		MaximumRate:            0.25,
		MinimumConfidenceScore: 0.6,
		CacheEnabled:           true,
		CacheTTL:               1 * time.Hour,
		CalculationTimeout:     30 * time.Second,
		IndustryAdjustments: map[string]IndustryAdjustment{
			"retail": {
				DiscountRateAdjustment: 0.005,
				TypicalLeaseTermYears:  12,
				EscalationRate:         0.035,
				MaterialityThreshold:   0.15,
			},
			"technology": {
				DiscountRateAdjustment: -0.01,
				TypicalLeaseTermYears:  8,
				EscalationRate:         0.025,
				MaterialityThreshold:   0.08,
			},
			"manufacturing": {
				DiscountRateAdjustment: 0.002,
				TypicalLeaseTermYears:  15,
				EscalationRate:         0.03,
				MaterialityThreshold:   0.12,
			},
			"financial": {
				DiscountRateAdjustment: -0.005,
				TypicalLeaseTermYears:  10,
				EscalationRate:         0.02,
				MaterialityThreshold:   0.05,
			},
		},
	}
}

// LoadConfigFromPath loads configuration from a specific path
func LoadConfigFromPath(configPath string) (*EstimationConfig, error) {
	// Ensure the path exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file does not exist: %s", configPath)
	}
	
	loader := NewConfigLoader(configPath)
	return loader.LoadConfig()
}

// LoadConfigFromProjectRoot loads configuration from the project root config directory
func LoadConfigFromProjectRoot() (*EstimationConfig, error) {
	// Find the project root by looking for go.mod
	projectRoot, err := findProjectRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to find project root: %w", err)
	}
	
	configPath := filepath.Join(projectRoot, "config", "datacleaner", "lease_estimation.json")
	return LoadConfigFromPath(configPath)
}

// findProjectRoot finds the project root directory by looking for go.mod
func findProjectRoot() (string, error) {
	// Start from current directory and walk up
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	
	for {
		// Check if go.mod exists in current directory
		goModPath := filepath.Join(currentDir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return currentDir, nil
		}
		
		// Move up one directory
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			// Reached root directory
			break
		}
		currentDir = parentDir
	}
	
	return "", fmt.Errorf("could not find project root (go.mod not found)")
}

// ConfigValidator provides validation utilities for configuration
type ConfigValidator struct {
	config *EstimationConfig
}

// NewConfigValidator creates a new configuration validator
func NewConfigValidator(config *EstimationConfig) *ConfigValidator {
	return &ConfigValidator{
		config: config,
	}
}

// ValidateForIndustry validates configuration for a specific industry
func (cv *ConfigValidator) ValidateForIndustry(industryCode string) error {
	// Check if industry-specific adjustments are available
	if _, exists := cv.config.IndustryAdjustments[industryCode]; !exists {
		// TODO: Log warning about missing industry-specific configuration
		return nil // Not an error, just use defaults
	}
	
	return nil
}

// ValidateCalculationInputs validates inputs for a specific calculation
func (cv *ConfigValidator) ValidateCalculationInputs(discountRate float64, leaseTermYears int, escalationRate float64) error {
	// Validate discount rate
	if discountRate < cv.config.MinimumRate || discountRate > cv.config.MaximumRate {
		return fmt.Errorf("discount rate %.3f is outside valid range [%.3f, %.3f]", 
			discountRate, cv.config.MinimumRate, cv.config.MaximumRate)
	}
	
	// Validate lease term
	if leaseTermYears < 1 || leaseTermYears > 100 {
		return fmt.Errorf("lease term %d years is outside valid range [1, 100]", leaseTermYears)
	}
	
	// Validate escalation rate
	if escalationRate < -0.5 || escalationRate > 1.0 {
		return fmt.Errorf("escalation rate %.3f is outside valid range [-0.5, 1.0]", escalationRate)
	}
	
	return nil
}

// GetIndustryAdjustment returns industry-specific adjustment parameters
func (cv *ConfigValidator) GetIndustryAdjustment(industryCode string) IndustryAdjustment {
	if adjustment, exists := cv.config.IndustryAdjustments[industryCode]; exists {
		return adjustment
	}
	
	// Return default adjustment
	return IndustryAdjustment{
		DiscountRateAdjustment: 0.0,
		TypicalLeaseTermYears:  cv.config.DefaultLeaseTermYears,
		EscalationRate:         cv.config.DefaultEscalationRate,
		MaterialityThreshold:   0.10, // 10% default threshold
	}
}