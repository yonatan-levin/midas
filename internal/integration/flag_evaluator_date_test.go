// Package integration contains integration tests for the flag-evaluator date condition (TDB-10 item 4).
package integration

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dateFlagConfig builds a single-condition flag of type "date" so the date arm of
// evaluateCondition (reachable via EvaluateFlag) is exercised without touching shipped config.
func dateFlagConfig(operator string, value interface{}) *config.FlagConditionsConfig {
	return &config.FlagConditionsConfig{
		Version: "1.0.0",
		Flags: []config.FlagConfig{
			{
				Name:     "date_flag",
				Enabled:  true,
				Priority: 100,
				Conditions: config.ConditionGroup{
					Operator: "AND",
					Conditions: []config.Condition{
						{
							Type:     "date",
							Field:    "report_date",
							Operator: operator,
							Value:    value,
						},
					},
				},
			},
		},
	}
}

// TestEvaluateDateCondition_AbsoluteOperators pins the absolute date operators (TDB-10 item 4).
func TestEvaluateDateCondition_AbsoluteOperators(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	ctx := context.Background()

	ref := "2024-06-15"
	fieldEarly := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	fieldLate := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	fieldEq := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)

	testCases := []struct {
		name      string
		operator  string
		value     interface{}
		field     interface{}
		triggered bool
	}{
		{name: "before true", operator: "before", value: ref, field: fieldEarly, triggered: true},
		{name: "before false", operator: "before", value: ref, field: fieldLate, triggered: false},
		{name: "after true", operator: "after", value: ref, field: fieldLate, triggered: true},
		{name: "after false", operator: "after", value: ref, field: fieldEarly, triggered: false},
		{name: "eq true", operator: "eq", value: ref, field: fieldEq, triggered: true},
		{name: "eq false", operator: "eq", value: ref, field: fieldEarly, triggered: false},
		{name: "ne true", operator: "ne", value: ref, field: fieldEarly, triggered: true},
		{name: "ne false", operator: "ne", value: ref, field: fieldEq, triggered: false},
		{name: "before with string field date", operator: "before", value: ref, field: "2024-01-01", triggered: true},
		{name: "between true", operator: "between", value: []interface{}{"2024-01-01", "2024-12-31"}, field: fieldEq, triggered: true},
		{name: "between false (outside)", operator: "between", value: []interface{}{"2024-07-01", "2024-12-31"}, field: fieldEq, triggered: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := dateFlagConfig(tc.operator, tc.value)
			evaluator, err := datacleaner.NewFlagConditionEvaluatorService(cfg, logger)
			require.NoError(t, err)

			result, err := evaluator.EvaluateFlag(ctx, "date_flag", map[string]interface{}{
				"report_date": tc.field,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.triggered, result.Triggered, "details: %s", result.Details)
		})
	}
}

// TestEvaluateDateCondition_NonDateAndUnsupported pins the lenient error paths.
func TestEvaluateDateCondition_NonDateAndUnsupported(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	ctx := context.Background()

	t.Run("non-date field value", func(t *testing.T) {
		cfg := dateFlagConfig("before", "2024-06-15")
		evaluator, err := datacleaner.NewFlagConditionEvaluatorService(cfg, logger)
		require.NoError(t, err)

		result, err := evaluator.EvaluateFlag(ctx, "date_flag", map[string]interface{}{
			"report_date": "not-a-date",
		})
		require.NoError(t, err)
		assert.False(t, result.Triggered)
		assert.Contains(t, result.Details, "is not a date")
	})

	t.Run("unsupported date operator", func(t *testing.T) {
		cfg := dateFlagConfig("older_than_days", float64(30))
		evaluator, err := datacleaner.NewFlagConditionEvaluatorService(cfg, logger)
		require.NoError(t, err)

		result, err := evaluator.EvaluateFlag(ctx, "date_flag", map[string]interface{}{
			"report_date": time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		})
		require.NoError(t, err)
		assert.False(t, result.Triggered)
		assert.Contains(t, result.Details, "unsupported date operator")
	})
}
