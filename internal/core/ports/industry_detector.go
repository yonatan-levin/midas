// Package ports contains interface definitions for industry code detection
package ports

import (
	"context"
)

// IndustryDetectionInput contains input data for industry detection
type IndustryDetectionInput struct {
	// CompanyName for exact matching
	CompanyName string
	
	// Description of the company's business
	Description string
	
	// SICCode if available
	SICCode string
	
	// NAICSCode if available
	NAICSCode string
	
	// Additional metadata
	Metadata map[string]interface{}
}

// IndustryDetectionResult contains the detection result
type IndustryDetectionResult struct {
	// Code is the detected industry code
	Code string
	
	// Name of the industry
	Name string
	
	// Confidence score (0-1)
	Confidence float64
	
	// MatchMethod describes how the match was made
	MatchMethod string
	
	// SubIndustry if applicable
	SubIndustry *SubIndustryInfo
}

// IndustryInfo contains information about an industry
type IndustryInfo struct {
	Code          string
	Name          string
	SubIndustries []SubIndustryInfo
}

// SubIndustryInfo contains sub-industry information
type SubIndustryInfo struct {
	Code string
	Name string
}

// IndustryCodeDetector defines the interface for industry code detection
type IndustryCodeDetector interface {
	// DetectIndustryCode detects industry code based on various inputs
	DetectIndustryCode(ctx context.Context, input IndustryDetectionInput) (*IndustryDetectionResult, error)
	
	// GetIndustryByCode retrieves industry information by code
	GetIndustryByCode(code string) (*IndustryInfo, error)
	
	// ValidateIndustryCode validates if a code is valid
	ValidateIndustryCode(code string) error
}
