// Package ports contains interface definitions for XBRL tag matching
package ports

import (
	"context"
	
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// XBRLTagMatcher defines the interface for XBRL tag matching
type XBRLTagMatcher interface {
	// MatchTags matches XBRL tags to internal fields based on configuration
	MatchTags(ctx context.Context, xbrlData *entities.XBRLData) ([]entities.MatchResult, error)
	
	// MatchSingleTag matches a single XBRL tag to an internal field
	MatchSingleTag(ctx context.Context, xbrlTag string, value interface{}) (*entities.MatchResult, error)
	
	// ValidateMatches validates the matched results against rules
	ValidateMatches(ctx context.Context, matches []entities.MatchResult) error
	
	// GetRequiredTags returns the list of required XBRL tags
	GetRequiredTags() []string
}
