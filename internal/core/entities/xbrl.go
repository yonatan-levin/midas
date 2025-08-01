// Package entities contains domain entities for XBRL tag matching
package entities

import (
	"time"
)

// XBRLData represents parsed XBRL data
type XBRLData struct {
	// Namespace of the XBRL document
	Namespace string
	
	// Context information (period, entity, etc.)
	Context XBRLContext
	
	// Facts contains the XBRL facts/values
	Facts map[string]interface{}
	
	// Units contains unit information for numeric values
	Units map[string]string
	
	// Footnotes contains additional notes
	Footnotes map[string]string
}

// XBRLContext represents the context of XBRL data
type XBRLContext struct {
	// Entity identifier
	EntityID string
	
	// Period type (instant or duration)
	PeriodType string
	
	// Start date for duration contexts
	StartDate *time.Time
	
	// End date for duration contexts or instant date
	EndDate time.Time
	
	// Segments for dimensional information
	Segments map[string]string
}

// MatchResult represents the result of tag matching
type MatchResult struct {
	// InternalField that was matched
	InternalField string
	
	// Value extracted from XBRL
	Value interface{}
	
	// OriginalTag that was matched
	OriginalTag string
	
	// Confidence score (0-1)
	Confidence float64
	
	// TransformationsApplied during matching
	TransformationsApplied []string
}
