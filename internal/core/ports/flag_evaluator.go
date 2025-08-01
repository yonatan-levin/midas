// Package ports contains interface definitions for flag condition evaluation
package ports

import (
	"context"
	"time"
)

// FlagResult represents the result of flag evaluation
type FlagResult struct {
	// FlagName that was evaluated
	FlagName string
	
	// Triggered indicates if the flag conditions were met
	Triggered bool
	
	// Timestamp of evaluation
	Timestamp time.Time
	
	// Details about the evaluation
	Details string
	
	// Actions that should be executed
	Actions []interface{}
}

// FlagConditionEvaluator defines the interface for evaluating flag conditions
type FlagConditionEvaluator interface {
	// EvaluateFlags evaluates all enabled flags against the provided data
	EvaluateFlags(ctx context.Context, data map[string]interface{}) ([]FlagResult, error)
	
	// EvaluateFlag evaluates a specific flag against the provided data
	EvaluateFlag(ctx context.Context, flagName string, data map[string]interface{}) (*FlagResult, error)
	
	// ExecuteActions executes actions for triggered flags
	ExecuteActions(ctx context.Context, results []FlagResult, data map[string]interface{}) error
}
