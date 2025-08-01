// Package datacleaner provides industry code detection service
package datacleaner

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	
	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// IndustryCodeDetectorService implements the IndustryCodeDetector interface
type IndustryCodeDetectorService struct {
	config         *config.IndustryCodeConfig
	logger         *log.Logger
	compiledRegexs map[string]*regexp.Regexp
}

// NewIndustryCodeDetectorService creates a new industry code detector service
func NewIndustryCodeDetectorService(cfg *config.IndustryCodeConfig, logger *log.Logger) (ports.IndustryCodeDetector, error) {
	// Compile regex patterns
	compiledRegexs := make(map[string]*regexp.Regexp)
	for _, mapping := range cfg.Mappings {
		for _, pattern := range mapping.Matchers.Patterns {
			regex, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("failed to compile pattern '%s' for industry %s: %w", 
					pattern, mapping.Name, err)
			}
			compiledRegexs[pattern] = regex
		}
	}
	
	return &IndustryCodeDetectorService{
		config:         cfg,
		logger:         logger,
		compiledRegexs: compiledRegexs,
	}, nil
}

// DetectIndustryCode detects industry code based on various inputs
func (s *IndustryCodeDetectorService) DetectIndustryCode(ctx context.Context, input ports.IndustryDetectionInput) (*ports.IndustryDetectionResult, error) {
	// Validate input
	if err := s.validateInput(input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	
	// Sort mappings by priority (higher first)
	sortedMappings := make([]config.IndustryMapping, len(s.config.Mappings))
	copy(sortedMappings, s.config.Mappings)
	sort.Slice(sortedMappings, func(i, j int) bool {
		return sortedMappings[i].Priority > sortedMappings[j].Priority
	})
	
	// Try each mapping in priority order
	for _, mapping := range sortedMappings {
		result := s.tryMatchMapping(mapping, input)
		if result != nil {
			s.logger.Printf("Industry detected: %s (code: %s, confidence: %.2f, method: %s)",
				result.Name, result.Code, result.Confidence, result.MatchMethod)
			return result, nil
		}
	}
	
	// No match found, return default
	s.logger.Printf("No industry match found for company '%s', using default code: %s",
		input.CompanyName, s.config.DefaultCode)
	
	return &ports.IndustryDetectionResult{
		Code:        s.config.DefaultCode,
		Name:        "Not Classified",
		Confidence:  0.0,
		MatchMethod: "default",
	}, nil
}

// tryMatchMapping attempts to match input against a single mapping
func (s *IndustryCodeDetectorService) tryMatchMapping(mapping config.IndustryMapping, input ports.IndustryDetectionInput) *ports.IndustryDetectionResult {
	matchers := mapping.Matchers
	
	// Check exact name match (highest confidence)
	if s.matchExactName(matchers.ExactNames, input.CompanyName) {
		return &ports.IndustryDetectionResult{
			Code:        mapping.Code,
			Name:        mapping.Name,
			Confidence:  1.0,
			MatchMethod: "exact_name",
		}
	}
	
	// Check SIC code match (high confidence)
	if input.SICCode != "" && s.matchCode(matchers.SICCodes, input.SICCode) {
		result := &ports.IndustryDetectionResult{
			Code:        mapping.Code,
			Name:        mapping.Name,
			Confidence:  0.95,
			MatchMethod: "sic_code",
		}
		
		// Check for sub-industry match
		if subIndustry := s.matchSubIndustry(mapping.SubIndustries, input); subIndustry != nil {
			result.SubIndustry = subIndustry
		}
		
		return result
	}
	
	// Check NAICS code match (high confidence)
	if input.NAICSCode != "" && s.matchCode(matchers.NAICSCodes, input.NAICSCode) {
		result := &ports.IndustryDetectionResult{
			Code:        mapping.Code,
			Name:        mapping.Name,
			Confidence:  0.95,
			MatchMethod: "naics_code",
		}
		
		// Check for sub-industry match
		if subIndustry := s.matchSubIndustry(mapping.SubIndustries, input); subIndustry != nil {
			result.SubIndustry = subIndustry
		}
		
		return result
	}
	
	// Check pattern match (medium confidence)
	if matchedPattern := s.matchPattern(matchers.Patterns, input); matchedPattern != "" {
		return &ports.IndustryDetectionResult{
			Code:        mapping.Code,
			Name:        mapping.Name,
			Confidence:  0.8,
			MatchMethod: fmt.Sprintf("pattern:%s", matchedPattern),
		}
	}
	
	// Check keyword match (lower confidence)
	if matchedKeywords := s.matchKeywords(matchers.Keywords, input); len(matchedKeywords) > 0 {
		// Calculate confidence based on number of keyword matches
		confidence := 0.4 + (0.1 * float64(len(matchedKeywords)))
		if confidence > 0.7 {
			confidence = 0.7
		}
		
		return &ports.IndustryDetectionResult{
			Code:        mapping.Code,
			Name:        mapping.Name,
			Confidence:  confidence,
			MatchMethod: fmt.Sprintf("keywords:%s", strings.Join(matchedKeywords, ",")),
		}
	}
	
	return nil
}

// matchExactName checks for exact company name match
func (s *IndustryCodeDetectorService) matchExactName(exactNames []string, companyName string) bool {
	normalizedName := s.normalizeString(companyName)
	for _, exactName := range exactNames {
		if s.normalizeString(exactName) == normalizedName {
			return true
		}
	}
	return false
}

// matchCode checks if a code matches any in the list
func (s *IndustryCodeDetectorService) matchCode(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
		// Also check prefix match for hierarchical codes
		if strings.HasPrefix(code, c) {
			return true
		}
	}
	return false
}

// matchPattern checks if input matches any regex pattern
func (s *IndustryCodeDetectorService) matchPattern(patterns []string, input ports.IndustryDetectionInput) string {
	searchText := s.combineSearchText(input)
	
	for _, pattern := range patterns {
		if regex, ok := s.compiledRegexs[pattern]; ok {
			if regex.MatchString(searchText) {
				return pattern
			}
		}
	}
	
	return ""
}

// matchKeywords checks for keyword matches
func (s *IndustryCodeDetectorService) matchKeywords(keywords []string, input ports.IndustryDetectionInput) []string {
	searchText := s.normalizeString(s.combineSearchText(input))
	var matched []string
	
	for _, keyword := range keywords {
		normalizedKeyword := s.normalizeString(keyword)
		if strings.Contains(searchText, normalizedKeyword) {
			matched = append(matched, keyword)
		}
	}
	
	return matched
}

// matchSubIndustry tries to match sub-industries
func (s *IndustryCodeDetectorService) matchSubIndustry(subIndustries []config.SubIndustryMapping, input ports.IndustryDetectionInput) *ports.SubIndustryInfo {
	for _, sub := range subIndustries {
		// Use similar matching logic as main industry
		if s.matchExactName(sub.Matchers.ExactNames, input.CompanyName) ||
		   s.matchCode(sub.Matchers.SICCodes, input.SICCode) ||
		   s.matchCode(sub.Matchers.NAICSCodes, input.NAICSCode) ||
		   len(s.matchKeywords(sub.Matchers.Keywords, input)) > 0 {
			return &ports.SubIndustryInfo{
				Code: sub.Code,
				Name: sub.Name,
			}
		}
	}
	
	return nil
}

// GetIndustryByCode retrieves industry information by code
func (s *IndustryCodeDetectorService) GetIndustryByCode(code string) (*ports.IndustryInfo, error) {
	mapping, found := s.config.GetMappingByCode(code)
	if !found {
		return nil, fmt.Errorf("industry code not found: %s", code)
	}
	
	info := &ports.IndustryInfo{
		Code: mapping.Code,
		Name: mapping.Name,
	}
	
	// Add sub-industries
	for _, sub := range mapping.SubIndustries {
		info.SubIndustries = append(info.SubIndustries, ports.SubIndustryInfo{
			Code: sub.Code,
			Name: sub.Name,
		})
	}
	
	return info, nil
}

// ValidateIndustryCode validates if a code is valid
func (s *IndustryCodeDetectorService) ValidateIndustryCode(code string) error {
	// Check if it's the default code
	if code == s.config.DefaultCode {
		return nil
	}
	
	// Check if it exists in mappings
	for _, mapping := range s.config.Mappings {
		if mapping.Code == code {
			return nil
		}
		
		// Check sub-industries
		for _, sub := range mapping.SubIndustries {
			if sub.Code == code {
				return nil
			}
		}
	}
	
	return fmt.Errorf("invalid industry code: %s", code)
}

// validateInput validates the detection input
func (s *IndustryCodeDetectorService) validateInput(input ports.IndustryDetectionInput) error {
	// At least one field should be provided
	if input.CompanyName == "" && 
	   input.Description == "" && 
	   input.SICCode == "" && 
	   input.NAICSCode == "" {
		return fmt.Errorf("at least one input field must be provided")
	}
	
	return nil
}

// normalizeString normalizes a string for comparison
func (s *IndustryCodeDetectorService) normalizeString(str string) string {
	// Convert to lowercase and trim spaces
	normalized := strings.ToLower(strings.TrimSpace(str))
	
	// Remove common suffixes
	suffixes := []string{" inc.", " inc", " corp.", " corp", " llc", " ltd.", " ltd", " plc"}
	for _, suffix := range suffixes {
		normalized = strings.TrimSuffix(normalized, suffix)
	}
	
	return strings.TrimSpace(normalized)
}

// combineSearchText combines all searchable text from input
func (s *IndustryCodeDetectorService) combineSearchText(input ports.IndustryDetectionInput) string {
	parts := []string{}
	
	if input.CompanyName != "" {
		parts = append(parts, input.CompanyName)
	}
	
	if input.Description != "" {
		parts = append(parts, input.Description)
	}
	
	return strings.Join(parts, " ")
}

// IndustryCodeWithDefaultNA is a helper function for backward compatibility
func IndustryCodeWithDefaultNA(detector ports.IndustryCodeDetector, ctx context.Context, input ports.IndustryDetectionInput) string {
	result, err := detector.DetectIndustryCode(ctx, input)
	if err != nil {
		return "NA"
	}
	
	return result.Code
}
