package datacleaner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// SR-1 B1 regression tests — the configured flag system was inert in
// production and carried a latent nil-logger panic:
//
//  1. NewFlagConditionEvaluatorService(cfg, nil) stored a nil *log.Logger and
//     evaluateSingleFlag called s.logger.Printf unguarded on any triggered
//     flag → nil-pointer panic the moment a flag could actually fire.
//  2. The `exists`-type condition was structurally unreachable for ABSENT
//     fields: evaluateCondition's null short-circuit returned before the
//     `case "exists"` branch, so data_completeness_flag (which wants to fire
//     on MISSING fields) could never detect absence — and for PRESENT fields
//     the branch ignored the configured operator/value entirely (inverted
//     semantics).
//  3. The shipped config (snake_case fields + the unsupported
//     "${TotalAssets * goodwill_threshold}" expression syntax) never matched
//     the PascalCase dataMap built by createRiskWarningFlags, so NO configured
//     flag could trigger; production always fell back to hardcoded flags.
//
// See docs/reviewer/SR-1-simplify-and-code-review-candidates.md §B1.

// alwaysTriggerConfig returns a minimal config whose single flag triggers on
// any dataMap that carries always_true=true. Includes a log action so the
// triggered path exercises the logger.
func alwaysTriggerConfig() *config.FlagConditionsConfig {
	return &config.FlagConditionsConfig{
		Version: "1.0.0",
		Flags: []config.FlagConfig{
			{
				Name:     "always_trigger",
				Enabled:  true,
				Priority: 100,
				Conditions: config.ConditionGroup{
					Operator: "AND",
					Conditions: []config.Condition{
						{Type: "boolean", Field: "always_true", Operator: "eq", Value: true},
					},
				},
				Actions: []config.FlagAction{
					{Type: "log", Parameters: map[string]interface{}{"level": "warning", "message": "fired"}},
				},
			},
		},
	}
}

// TestFlagEvaluator_NilLogger_NoPanicOnTriggeredFlag pins the nil-logger
// hardening: the production wiring at service.go constructs the evaluator
// with a nil logger, so a triggered flag must NOT dereference it.
func TestFlagEvaluator_NilLogger_NoPanicOnTriggeredFlag(t *testing.T) {
	evaluator, err := NewFlagConditionEvaluatorService(alwaysTriggerConfig(), nil)
	require.NoError(t, err)

	// Pre-fix this panics inside evaluateSingleFlag's s.logger.Printf.
	results, err := evaluator.EvaluateFlags(context.Background(), map[string]interface{}{
		"always_true": true,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Triggered, "the always_true flag should trigger")
}

// TestFlagEvaluator_ExistsCondition_Semantics pins the corrected exists-type
// behavior: the condition compares actual presence against the configured
// expected value (operator eq/ne), and absence is handled BY the exists
// branch rather than being swallowed by the null short-circuit.
func TestFlagEvaluator_ExistsCondition_Semantics(t *testing.T) {
	existsCfg := func(expected interface{}, operator string) *config.FlagConditionsConfig {
		return &config.FlagConditionsConfig{
			Version: "1.0.0",
			Flags: []config.FlagConfig{
				{
					Name:     "exists_flag",
					Enabled:  true,
					Priority: 10,
					Conditions: config.ConditionGroup{
						Operator: "AND",
						Conditions: []config.Condition{
							{Type: "exists", Field: "watched_field", Operator: operator, Value: expected},
						},
					},
				},
			},
		}
	}

	cases := []struct {
		name      string
		expected  interface{}
		operator  string
		data      map[string]interface{}
		triggered bool
	}{
		{
			// The data_completeness_flag shape: fire when the field is MISSING.
			name:      "expect-absent on absent field triggers",
			expected:  false,
			operator:  "eq",
			data:      map[string]interface{}{"other": 1.0},
			triggered: true,
		},
		{
			name:      "expect-absent on present field does not trigger",
			expected:  false,
			operator:  "eq",
			data:      map[string]interface{}{"watched_field": 42.0},
			triggered: false,
		},
		{
			name:      "expect-present on present field triggers",
			expected:  true,
			operator:  "eq",
			data:      map[string]interface{}{"watched_field": 42.0},
			triggered: true,
		},
		{
			name:      "expect-present on absent field does not trigger",
			expected:  true,
			operator:  "eq",
			data:      map[string]interface{}{"other": 1.0},
			triggered: false,
		},
		{
			name:      "ne inverts: not-absent on present field triggers",
			expected:  false,
			operator:  "ne",
			data:      map[string]interface{}{"watched_field": 42.0},
			triggered: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evaluator, err := NewFlagConditionEvaluatorService(existsCfg(tc.expected, tc.operator), nil)
			require.NoError(t, err)

			result, err := evaluator.EvaluateFlag(context.Background(), "exists_flag", tc.data)
			require.NoError(t, err)
			assert.Equal(t, tc.triggered, result.Triggered, "details: %s", result.Details)
		})
	}
}

// shippedFlagConfig loads the real production config so the tests below
// catch shipped-config drift (field vocabulary, expression syntax) — the
// pre-fix config used "${TotalAssets * goodwill_threshold}" which the
// evaluator cannot parse, making the flag permanently dead.
func shippedFlagConfig(t *testing.T) *config.FlagConditionsConfig {
	t.Helper()
	cfg, err := config.LoadFlagConditionsConfig("../../../config/datacleaner/flag_conditions.json")
	require.NoError(t, err, "shipped flag_conditions.json must load and validate")
	return cfg
}

// highGoodwillFinancials builds a FinancialData whose goodwill is 45% of
// total assets — far above the 25% goodwill_threshold global — so the
// shipped excessive_goodwill_warning flag must trigger once the config and
// dataMap actually speak the same vocabulary.
func highGoodwillFinancials() *entities.FinancialData {
	return &entities.FinancialData{
		Ticker:                   "GOODW",
		Revenue:                  1_000_000_000,
		TotalAssets:              2_000_000_000,
		Goodwill:                 900_000_000, // 45% of assets
		OtherIntangibles:         100_000_000, // 5% — below the 20% intangibles threshold
		NetIncome:                50_000_000,
		StockholdersEquity:       800_000_000,
		OperatingCashFlow:        120_000_000,
		TotalDebt:                400_000_000,
		OperatingIncome:          90_000_000,
		InterestExpense:          10_000_000,
		SharesOutstanding:        100_000_000,
		DilutedSharesOutstanding: 100_000_000,
		FilingDate:               time.Now().AddDate(0, -3, 0),
	}
}

// TestShippedConfig_GoodwillFlag_TriggersOnEnrichedDataMap is the end-to-end
// B1 pin: shipped config + the production dataMap builder must produce a
// triggered excessive_goodwill_warning for a high-goodwill company. Pre-fix
// this fails on BOTH sides (PascalCase-only dataMap, ${} expression).
func TestShippedConfig_GoodwillFlag_TriggersOnEnrichedDataMap(t *testing.T) {
	cfg := shippedFlagConfig(t)
	evaluator, err := NewFlagConditionEvaluatorService(cfg, nil)
	require.NoError(t, err)

	dataMap := buildFlagEvaluationData(highGoodwillFinancials())

	results, err := evaluator.EvaluateFlags(context.Background(), dataMap)
	require.NoError(t, err)

	triggered := map[string]bool{}
	for _, r := range results {
		triggered[r.FlagName] = r.Triggered
	}

	assert.True(t, triggered["excessive_goodwill_warning"],
		"goodwill at 45%% of assets must trigger the shipped goodwill flag; results: %#v", triggered)
	assert.False(t, triggered["excessive_intangibles_warning"],
		"intangibles at 5%% of assets must NOT trigger the 20%% intangibles flag")
	assert.False(t, triggered["negative_equity_flag"],
		"positive equity must not trigger the negative-equity flag")
	assert.False(t, triggered["data_completeness_flag"],
		"all completeness fields are present — flag must stay quiet")
}

// TestShippedConfig_NegativeEquityFlag_Triggers pins a second live shipped
// flag through the same production dataMap path.
func TestShippedConfig_NegativeEquityFlag_Triggers(t *testing.T) {
	cfg := shippedFlagConfig(t)
	evaluator, err := NewFlagConditionEvaluatorService(cfg, nil)
	require.NoError(t, err)

	fd := highGoodwillFinancials()
	fd.Goodwill = 100_000_000 // below threshold so only equity fires
	fd.StockholdersEquity = -250_000_000

	results, err := evaluator.EvaluateFlags(context.Background(), buildFlagEvaluationData(fd))
	require.NoError(t, err)

	var negEquity bool
	for _, r := range results {
		if r.FlagName == "negative_equity_flag" {
			negEquity = r.Triggered
		}
	}
	assert.True(t, negEquity, "negative stockholders_equity must trigger the shipped flag")
}

// TestCreateRiskWarningFlags_UsesConfiguredFlags pins the service-level
// behavior: with the shipped config working, createRiskWarningFlags returns
// the configured flag (RuleID = flag name) instead of only ever reaching the
// hardcoded fallback.
func TestCreateRiskWarningFlags_UsesConfiguredFlags(t *testing.T) {
	svc := newTestServiceWithShippedFlags(t)

	flags := svc.createRiskWarningFlags(context.Background(), highGoodwillFinancials(), time.Now())
	require.NotEmpty(t, flags)

	var found bool
	for _, f := range flags {
		if f.RuleID == "excessive_goodwill_warning" {
			found = true
			assert.Equal(t, "risk_warning", f.Type)
		}
	}
	assert.True(t, found, "configured goodwill flag should surface through createRiskWarningFlags; got %#v", flags)
}

// newTestServiceWithShippedFlags builds a *service wired with the SHIPPED
// flag config (and an empty rules engine — not needed for these tests).
func newTestServiceWithShippedFlags(t *testing.T) *service {
	t.Helper()
	cfg := shippedFlagConfig(t)
	evaluator, err := NewFlagConditionEvaluatorService(cfg, nil)
	require.NoError(t, err)

	return &service{
		config:        &config.DataCleanerConfig{Enabled: true},
		flagEvaluator: evaluator,
		cache:         map[string]*entities.CleaningResult{},
	}
}
