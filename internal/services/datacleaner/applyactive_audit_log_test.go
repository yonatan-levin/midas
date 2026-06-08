package datacleaner

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
)

// TestApplyActiveAdjustments_EmitsAuditLogPerFiredAdjustment pins TDB-4 Task A:
// applyActiveAdjustments emits exactly one request-scoped
// "trace.datacleaner.adjustment" Debug line per FIRED adjuster, carrying the
// projected entities.Adjustment fields (ticker, rule_id, category, type,
// amount). The line count must equal the length of the returned
// allAdjustments projection (one entry == one fired adjuster). The observer is
// injected via logctx so the emit point's logctx.From(ctx) resolves to it.
func TestApplyActiveAdjustments_EmitsAuditLogPerFiredAdjustment(t *testing.T) {
	cfg := createTestConfig()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// The inner *service is what carries applyActiveAdjustments; the public
	// constructor returns the DataCleanerService interface, so assert to the
	// concrete type to reach the unexported method.
	s, ok := svc.(*service)
	require.True(t, ok, "NewDataCleanerService must return the concrete *service")

	core, observed := observer.New(zap.DebugLevel)
	ctx := logctx.Inject(context.Background(), zap.New(core))

	data := createTestFinancialDataWithIssues()
	cleaningCtx := newTestCleaningContext()

	adjustments, _, _, err := s.applyActiveAdjustments(ctx, data, cleaningCtx)
	require.NoError(t, err)
	require.NotEmpty(t, adjustments,
		"the with-issues fixture must fire at least one adjuster for this test to be meaningful")

	logged := observed.FilterMessage("trace.datacleaner.adjustment").All()
	assert.Len(t, logged, len(adjustments),
		"exactly one trace.datacleaner.adjustment line per fired adjuster (== len(allAdjustments))")

	// Every audit line must carry the projected fields and the correct ticker.
	for _, e := range logged {
		fields := e.ContextMap()
		assert.Equal(t, "TEST", fields["ticker"], "ticker must be data.Ticker")
		assert.Contains(t, fields, "rule_id")
		assert.Contains(t, fields, "category")
		assert.Contains(t, fields, "type")
		assert.Contains(t, fields, "amount")
		assert.Contains(t, fields, "percentage")
		assert.Contains(t, fields, "from_account")
		assert.Contains(t, fields, "to_account")
		assert.NotEmpty(t, fields["rule_id"], "audit line rule_id must be non-empty")
	}

	// Field-level cross-check: the set of (rule_id, category, type, amount)
	// tuples in the log must match the projection element-for-element.
	type key struct {
		ruleID   string
		category string
		typ      string
		amount   float64
	}
	want := make(map[key]int)
	for _, adj := range adjustments {
		want[key{adj.RuleID, string(adj.Category), string(adj.Type), adj.Amount}]++
	}
	got := make(map[key]int)
	for _, e := range logged {
		f := e.ContextMap()
		got[key{
			ruleID:   f["rule_id"].(string),
			category: f["category"].(string),
			typ:      f["type"].(string),
			amount:   f["amount"].(float64),
		}]++
	}
	assert.Equal(t, want, got,
		"each audit line's (rule_id, category, type, amount) must match an allAdjustments element")

	// The optional summary line ships with the correct fired_count.
	summary := observed.FilterMessage("trace.datacleaner.adjustments_summary").All()
	require.Len(t, summary, 1, "exactly one summary line per clean call")
	assert.Equal(t, int64(len(adjustments)), summary[0].ContextMap()["fired_count"],
		"summary fired_count must equal the number of fired adjusters")
}

// TestApplyActiveAdjustments_NoAuditLogWhenNoneFire pins that a fixture that
// fires no adjuster produces zero per-adjustment audit lines (and exactly one
// summary line with fired_count=0).
func TestApplyActiveAdjustments_NoAuditLogWhenNoneFire(t *testing.T) {
	cfg := createTestConfig()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)
	s, ok := svc.(*service)
	require.True(t, ok)

	core, observed := observer.New(zap.DebugLevel)
	ctx := logctx.Inject(context.Background(), zap.New(core))

	// Pristine balance sheet: no goodwill, no intangibles, no inventory, no
	// DTA, no leases, no pension, no contingents, no restructuring — every
	// A/B/C adjuster skips on applicability.
	data := &entities.FinancialData{
		Ticker:            "PRISTINE",
		ReportingCurrency: "USD",
		FilingPeriod:      "2024FY",
		FilingDate:        time.Now(),
		TotalAssets:       1_000_000_000,
		SharesOutstanding: 100_000_000,
		Revenue:           500_000_000,
		NetIncome:         50_000_000,
	}
	cleaningCtx := newTestCleaningContext()

	adjustments, _, _, err := s.applyActiveAdjustments(ctx, data, cleaningCtx)
	require.NoError(t, err)
	require.Empty(t, adjustments, "pristine fixture must fire no adjuster")

	assert.Equal(t, 0, observed.FilterMessage("trace.datacleaner.adjustment").Len(),
		"no per-adjustment audit line when nothing fires")

	summary := observed.FilterMessage("trace.datacleaner.adjustments_summary").All()
	require.Len(t, summary, 1, "summary line emitted even when zero fired (operability)")
	assert.Equal(t, int64(0), summary[0].ContextMap()["fired_count"])
}

// TestApplyActiveAdjustments_IncrementsAdjustmentCounter pins TDB-4 Task B:
// when an AdjustmentMetrics recorder is injected, the orchestrator increments
// datacleaner_adjustments_total once per fired adjuster with the
// {rule_id,category,type} labels of that adjustment.
func TestApplyActiveAdjustments_IncrementsAdjustmentCounter(t *testing.T) {
	cfg := createTestConfig()
	registry := prometheus.NewRegistry()
	ms := metrics.NewServiceWithRegistry(zap.NewNop(), registry)
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil,
		WithAdjustmentMetrics(ms))
	require.NoError(t, err)
	s, ok := svc.(*service)
	require.True(t, ok)

	ctx := context.Background()
	data := createTestFinancialDataWithIssues()
	cleaningCtx := newTestCleaningContext()

	adjustments, _, _, err := s.applyActiveAdjustments(ctx, data, cleaningCtx)
	require.NoError(t, err)
	require.NotEmpty(t, adjustments)

	// Gather the service-owned registry (NEVER DefaultRegisterer) and sum the
	// datacleaner_adjustments_total samples — the total must equal the number
	// of fired adjusters, and each (rule_id, category, type) label set must
	// match an allAdjustments element.
	families, err := registry.Gather()
	require.NoError(t, err)

	type key struct{ ruleID, category, typ string }
	want := make(map[key]int)
	for _, adj := range adjustments {
		want[key{adj.RuleID, string(adj.Category), string(adj.Type)}]++
	}

	got := make(map[key]int)
	var total int
	foundFamily := false
	for _, f := range families {
		if f.GetName() != "datacleaner_adjustments_total" {
			continue
		}
		foundFamily = true
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			v := int(m.GetCounter().GetValue())
			got[key{labels["rule_id"], labels["category"], labels["type"]}] += v
			total += v
		}
	}
	assert.True(t, foundFamily, "datacleaner_adjustments_total must be registered on the service registry")
	assert.Equal(t, len(adjustments), total,
		"datacleaner_adjustments_total increments once per fired adjuster")
	assert.Equal(t, want, got,
		"each counter label set {rule_id,category,type} must match an allAdjustments element")
}

// TestService_AdjustmentMetrics_NilSafe pins that the default 3-arg
// constructor (no WithAdjustmentMetrics option) leaves the recorder nil and
// the orchestrator runs without panicking — the metrics increment is
// nil-guarded.
func TestService_AdjustmentMetrics_NilSafe(t *testing.T) {
	cfg := createTestConfig()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)
	s, ok := svc.(*service)
	require.True(t, ok)
	require.Nil(t, s.adjMetrics, "no option → nil recorder")

	ctx := context.Background()
	data := createTestFinancialDataWithIssues()
	cleaningCtx := newTestCleaningContext()

	assert.NotPanics(t, func() {
		_, _, _, aerr := s.applyActiveAdjustments(ctx, data, cleaningCtx)
		require.NoError(t, aerr)
	}, "nil metrics recorder must not panic")
}

// newTestCleaningContext builds a minimal CleaningContext for the
// applyActiveAdjustments call sites in this file. IndustryCode is empty so the
// generic (non-industry-specific) rule set applies — the same set the
// with-issues fixture in service_test.go exercises.
func newTestCleaningContext() *entities.CleaningContext {
	return &entities.CleaningContext{
		IndustryCode: "",
		DataVintage:  time.Now(),
	}
}
