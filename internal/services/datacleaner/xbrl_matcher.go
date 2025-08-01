// Package datacleaner provides XBRL tag matching service implementation
package datacleaner

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// XBRLTagMatcherService implements the XBRLTagMatcher interface
type XBRLTagMatcherService struct {
	config         *config.XBRLTagConfig
	logger         *log.Logger
	compiledRegexs map[string]*regexp.Regexp
}

// NewXBRLTagMatcherService creates a new XBRL tag matcher service
func NewXBRLTagMatcherService(cfg *config.XBRLTagConfig, logger *log.Logger) ports.XBRLTagMatcher {
	return &XBRLTagMatcherService{
		config:         cfg,
		logger:         logger,
		compiledRegexs: make(map[string]*regexp.Regexp),
	}
}

// MatchTags matches XBRL tags to internal fields based on configuration
func (s *XBRLTagMatcherService) MatchTags(ctx context.Context, xbrlData *entities.XBRLData) ([]entities.MatchResult, error) {
	if xbrlData == nil {
		return nil, fmt.Errorf("xbrlData cannot be nil")
	}
	
	var results []entities.MatchResult
	
	// Process each fact in the XBRL data
	for tag, value := range xbrlData.Facts {
		// Normalize the tag (handle namespaces)
		normalizedTag := s.normalizeTag(tag, xbrlData.Namespace)
		
		// Try to match the tag
		result, err := s.MatchSingleTag(ctx, normalizedTag, value)
		if err != nil {
			s.logger.Printf("Warning: failed to match tag %s: %v", tag, err)
			continue
		}
		
		if result != nil {
			results = append(results, *result)
		}
	}
	
	// Check for required tags
	if err := s.checkRequiredTags(results); err != nil {
		return results, fmt.Errorf("missing required tags: %w", err)
	}
	
	return results, nil
}

// MatchSingleTag matches a single XBRL tag to an internal field
func (s *XBRLTagMatcherService) MatchSingleTag(ctx context.Context, xbrlTag string, value interface{}) (*entities.MatchResult, error) {
	// Look up the mapping in configuration
	mapping, found := s.config.GetMappingByXBRLTag(xbrlTag)
	if !found {
		// Tag not found in configuration, this might be expected
		return nil, nil
	}
	
	// Transform the value based on configuration
	transformedValue, transformations, err := s.applyTransformations(value, mapping.Transformations)
	if err != nil {
		return nil, fmt.Errorf("failed to apply transformations: %w", err)
	}
	
	// Validate data type
	if err := s.validateDataType(transformedValue, mapping.DataType); err != nil {
		return nil, fmt.Errorf("data type validation failed: %w", err)
	}
	
	// Calculate confidence score
	confidence := s.calculateConfidence(xbrlTag, mapping, transformations)
	
	return &entities.MatchResult{
		InternalField:          mapping.InternalField,
		Value:                  transformedValue,
		OriginalTag:            xbrlTag,
		Confidence:             confidence,
		TransformationsApplied: transformations,
	}, nil
}

// ValidateMatches validates the matched results against rules
func (s *XBRLTagMatcherService) ValidateMatches(ctx context.Context, matches []entities.MatchResult) error {
	// Create a map for easy lookup
	matchMap := make(map[string]interface{})
	for _, match := range matches {
		matchMap[match.InternalField] = match.Value
	}
	
	// Apply validation rules
	for _, rule := range s.config.ValidationRules {
		if err := s.applyValidationRule(rule, matchMap); err != nil {
			return fmt.Errorf("validation rule '%s' failed: %w", rule.Name, err)
		}
	}
	
	return nil
}

// GetRequiredTags returns the list of required XBRL tags
func (s *XBRLTagMatcherService) GetRequiredTags() []string {
	var tags []string
	
	for _, mapping := range s.config.GetRequiredMappings() {
		tags = append(tags, mapping.XBRLTag)
		// Include alternative tags as well
		tags = append(tags, mapping.AlternativeTags...)
	}
	
	return tags
}

// normalizeTag normalizes an XBRL tag by handling namespaces
func (s *XBRLTagMatcherService) normalizeTag(tag, namespace string) string {
	// Remove namespace prefix if present
	parts := strings.Split(tag, ":")
	if len(parts) == 2 {
		return parts[1]
	}
	
	// If no namespace in tag, prepend the default namespace
	if namespace != "" && !strings.Contains(tag, ":") {
		return fmt.Sprintf("%s:%s", namespace, tag)
	}
	
	return tag
}

// applyTransformations applies configured transformations to a value
func (s *XBRLTagMatcherService) applyTransformations(value interface{}, transformations []string) (interface{}, []string, error) {
	applied := []string{}
	result := value
	
	for _, transform := range transformations {
		var err error
		var wasApplied bool
		
		switch transform {
		case "multiply_by_thousand":
			result, wasApplied, err = s.transformMultiplyByThousand(result)
		case "negate":
			result, wasApplied, err = s.transformNegate(result)
		case "to_decimal":
			result, wasApplied, err = s.transformToDecimal(result)
		case "trim":
			result, wasApplied, err = s.transformTrim(result)
		case "remove_currency_symbol":
			result, wasApplied, err = s.transformRemoveCurrencySymbol(result)
		default:
			s.logger.Printf("Unknown transformation: %s", transform)
			continue
		}
		
		if err != nil {
			return nil, applied, fmt.Errorf("transformation '%s' failed: %w", transform, err)
		}
		
		if wasApplied {
			applied = append(applied, transform)
		}
	}
	
	return result, applied, nil
}

// transformMultiplyByThousand multiplies numeric values by 1000
func (s *XBRLTagMatcherService) transformMultiplyByThousand(value interface{}) (interface{}, bool, error) {
	switch v := value.(type) {
	case float64:
		return v * 1000, true, nil
	case int:
		return float64(v) * 1000, true, nil
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f * 1000, true, nil
		}
	}
	return value, false, nil
}

// transformNegate negates numeric values
func (s *XBRLTagMatcherService) transformNegate(value interface{}) (interface{}, bool, error) {
	switch v := value.(type) {
	case float64:
		return -v, true, nil
	case int:
		return -v, true, nil
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return -f, true, nil
		}
	}
	return value, false, nil
}

// transformToDecimal converts values to decimal/float64
func (s *XBRLTagMatcherService) transformToDecimal(value interface{}) (interface{}, bool, error) {
	switch v := value.(type) {
	case float64:
		return v, false, nil // Already decimal
	case int:
		return float64(v), true, nil
	case string:
		// Remove commas and spaces
		cleaned := strings.ReplaceAll(strings.TrimSpace(v), ",", "")
		if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
			return f, true, nil
		}
		return nil, false, fmt.Errorf("cannot convert string '%s' to decimal", v)
	default:
		return nil, false, fmt.Errorf("unsupported type for decimal conversion: %T", v)
	}
}

// transformTrim trims whitespace from string values
func (s *XBRLTagMatcherService) transformTrim(value interface{}) (interface{}, bool, error) {
	if str, ok := value.(string); ok {
		trimmed := strings.TrimSpace(str)
		return trimmed, trimmed != str, nil
	}
	return value, false, nil
}

// transformRemoveCurrencySymbol removes currency symbols from values
func (s *XBRLTagMatcherService) transformRemoveCurrencySymbol(value interface{}) (interface{}, bool, error) {
	if str, ok := value.(string); ok {
		// Remove common currency symbols
		cleaned := strings.TrimSpace(str)
		symbols := []string{"$", "€", "£", "¥", "₹", "¢"}
		
		for _, symbol := range symbols {
			cleaned = strings.ReplaceAll(cleaned, symbol, "")
		}
		
		cleaned = strings.TrimSpace(cleaned)
		return cleaned, cleaned != str, nil
	}
	return value, false, nil
}

// validateDataType validates that a value matches the expected data type
func (s *XBRLTagMatcherService) validateDataType(value interface{}, expectedType string) error {
	switch expectedType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case "number", "decimal":
		switch value.(type) {
		case float64, float32, int, int32, int64:
			// Valid numeric types
		default:
			return fmt.Errorf("expected numeric type, got %T", value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", value)
		}
	case "date", "duration":
		// TODO: Add date/duration validation
	}
	
	return nil
}

// calculateConfidence calculates a confidence score for a match
func (s *XBRLTagMatcherService) calculateConfidence(tag string, mapping *config.XBRLTagMapping, transformations []string) float64 {
	confidence := 1.0
	
	// Reduce confidence if alternative tag was used
	isPrimaryTag := mapping.XBRLTag == tag
	if !isPrimaryTag {
		confidence *= 0.8
	}
	
	// Reduce confidence for each transformation applied
	confidence *= (1.0 - 0.05*float64(len(transformations)))
	
	// Ensure confidence stays in valid range
	if confidence < 0.1 {
		confidence = 0.1
	}
	
	return confidence
}

// checkRequiredTags checks if all required tags are present in the results
func (s *XBRLTagMatcherService) checkRequiredTags(results []entities.MatchResult) error {
	// Create a set of matched internal fields
	matchedFields := make(map[string]bool)
	for _, result := range results {
		matchedFields[result.InternalField] = true
	}
	
	// Check each required mapping
	var missingFields []string
	for _, mapping := range s.config.GetRequiredMappings() {
		if !matchedFields[mapping.InternalField] {
			missingFields = append(missingFields, mapping.InternalField)
		}
	}
	
	if len(missingFields) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missingFields, ", "))
	}
	
	return nil
}

// applyValidationRule applies a single validation rule
func (s *XBRLTagMatcherService) applyValidationRule(rule config.ValidationRule, data map[string]interface{}) error {
	value, exists := data[rule.Field]
	if !exists && rule.Type != "required" {
		// Field doesn't exist, skip validation unless it's a required field check
		return nil
	}
	
	switch rule.Type {
	case "required":
		if !exists {
			return fmt.Errorf(rule.ErrorMessage)
		}
	case "range":
		return s.validateRange(value, rule.Parameters, rule.ErrorMessage)
	case "format":
		return s.validateFormat(value, rule.Parameters, rule.ErrorMessage)
	case "consistency":
		return s.validateConsistency(data, rule.Parameters, rule.ErrorMessage)
	default:
		s.logger.Printf("Unknown validation type: %s", rule.Type)
	}
	
	return nil
}

// validateRange validates numeric range constraints
func (s *XBRLTagMatcherService) validateRange(value interface{}, params map[string]interface{}, errorMsg string) error {
	numValue, err := s.toFloat64(value)
	if err != nil {
		return fmt.Errorf("cannot validate range for non-numeric value: %w", err)
	}
	
	if minVal, ok := params["min"].(float64); ok && numValue < minVal {
		return fmt.Errorf(errorMsg)
	}
	
	if maxVal, ok := params["max"].(float64); ok && numValue > maxVal {
		return fmt.Errorf(errorMsg)
	}
	
	return nil
}

// validateFormat validates string format constraints
func (s *XBRLTagMatcherService) validateFormat(value interface{}, params map[string]interface{}, errorMsg string) error {
	strValue, ok := value.(string)
	if !ok {
		return fmt.Errorf("format validation requires string value, got %T", value)
	}
	
	if pattern, ok := params["pattern"].(string); ok {
		// TODO: Implement regex pattern matching
		_ = pattern
		_ = strValue
	}
	
	return nil
}

// validateConsistency validates consistency between multiple fields
func (s *XBRLTagMatcherService) validateConsistency(data map[string]interface{}, params map[string]interface{}, errorMsg string) error {
	// TODO: Implement consistency checks (e.g., assets = liabilities + equity)
	return nil
}

// toFloat64 converts various numeric types to float64
func (s *XBRLTagMatcherService) toFloat64(value interface{}) (float64, error) {
	v := reflect.ValueOf(value)
	v = reflect.Indirect(v)
	
	switch v.Kind() {
	case reflect.Float64:
		return v.Float(), nil
	case reflect.Float32:
		return v.Float(), nil
	case reflect.Int, reflect.Int32, reflect.Int64:
		return float64(v.Int()), nil
	case reflect.String:
		return strconv.ParseFloat(v.String(), 64)
	default:
		return 0, fmt.Errorf("cannot convert %v to float64", v.Type())
	}
}
