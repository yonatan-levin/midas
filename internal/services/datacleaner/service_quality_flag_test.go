package datacleaner

// Phase 2.B — auto-on-quality-flag trigger tests for the data cleaner's
// post-clean hook. These pin the behaviour of countQualifyingFlags and the
// service.go hook that calls bundle.RecordQualityFlagCount when a deferred
// bundle is on ctx.
//
// Severity ranking semantics: the cleaner's flag taxonomy uses the
// FlagSeverity vocabulary defined in core/entities/data_cleaning.go, with
// two parallel value sets (low/medium/high/critical and info/warning/critical)
// that alias on "critical". The threshold compare must rank both value sets
// consistently so an operator setting threshold="warning" gets the
// equivalent of threshold="medium" without surprises.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestCountQualifyingFlags_RanksSeveritiesConsistently pins the rank
// comparator. The two parallel vocabularies must collapse to the same
// numeric ranks so the threshold check is deterministic regardless of
// which vocabulary the cleaner emitted for any given flag.
func TestCountQualifyingFlags_RanksSeveritiesConsistently(t *testing.T) {
	flags := []entities.Flag{
		{Severity: entities.FlagSeverityLow},
		{Severity: entities.Info}, // ranks the same as Low
		{Severity: entities.FlagSeverityMedium},
		{Severity: entities.Warning}, // ranks the same as Medium
		{Severity: entities.FlagSeverityHigh},
		{Severity: entities.FlagSeverityCritical},
		{Severity: entities.Critical}, // alias of FlagSeverityCritical
	}

	cases := []struct {
		threshold string
		want      int
	}{
		{"info", 7},     // everything qualifies (lowest rank)
		{"low", 7},      // info == low rank, all qualify
		{"warning", 5},  // medium+, high+, critical (incl alias) — 2+1+2=5
		{"medium", 5},   // alias of warning
		{"high", 3},     // high + 2x critical
		{"critical", 2}, // both critical instances
	}

	for _, tc := range cases {
		t.Run(tc.threshold, func(t *testing.T) {
			got := countQualifyingFlags(flags, tc.threshold)
			assert.Equal(t, tc.want, got,
				"threshold=%q must qualify %d flags out of %d", tc.threshold, tc.want, len(flags))
		})
	}
}

// TestCountQualifyingFlags_EmptyThresholdDisables — empty threshold means
// the trigger is off. The counter must return 0 regardless of how many
// flags are present so the middleware never fires Promote(on_quality_flag).
func TestCountQualifyingFlags_EmptyThresholdDisables(t *testing.T) {
	flags := []entities.Flag{
		{Severity: entities.FlagSeverityCritical},
		{Severity: entities.FlagSeverityCritical},
	}
	assert.Equal(t, 0, countQualifyingFlags(flags, ""),
		"empty threshold disables the trigger and must short-circuit to 0")
}

// TestCountQualifyingFlags_UnknownThresholdNeverFires — a typo in the
// config (e.g. "warnng") must NOT silently behave like the lowest threshold;
// it must short-circuit to 0 so misconfiguration is loud rather than
// surprising. We accept the loss of diagnostic data over the surprise of
// unexpected disk I/O.
func TestCountQualifyingFlags_UnknownThresholdNeverFires(t *testing.T) {
	flags := []entities.Flag{
		{Severity: entities.FlagSeverityCritical},
	}
	assert.Equal(t, 0, countQualifyingFlags(flags, "warnng"),
		"unknown threshold value must be treated as disabled")
}

// TestCountQualifyingFlags_EmptySeverityDoesNotQualify — a flag with no
// severity field set must not contribute to the count regardless of
// threshold, since "no rank" is unranked.
func TestCountQualifyingFlags_EmptySeverityDoesNotQualify(t *testing.T) {
	flags := []entities.Flag{
		{Severity: ""}, // unranked
		{Severity: entities.FlagSeverityCritical},
	}
	assert.Equal(t, 1, countQualifyingFlags(flags, "info"),
		"unranked flags must not qualify even at the lowest threshold")
}

// TestCountQualifyingFlags_NoFlags pins the empty-input contract: empty
// slice yields zero count regardless of threshold.
func TestCountQualifyingFlags_NoFlags(t *testing.T) {
	assert.Equal(t, 0, countQualifyingFlags(nil, "info"))
	assert.Equal(t, 0, countQualifyingFlags([]entities.Flag{}, "critical"))
}

// TestCleanService_RecordsQualityFlagCount_WhenBundleOnContext — end-to-end
// pin: when a deferred bundle is on ctx with a configured threshold AND
// the cleaner produces qualifying flags, the bundle's QualityFlagCount
// reflects the count post-clean. This is the contract the trace middleware
// relies on at promote-time.
//
// We use a synthetic FinancialData crafted so the cleaner's rules produce
// at least one warning-level flag (excessive goodwill rule fires when
// goodwill > 25% of total assets — see service.go::createHardcodedRiskFlags).
func TestCleanService_RecordsQualityFlagCount_WhenBundleOnContext(t *testing.T) {
	cfg := createTestConfig()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// Construct a deferred bundle with a configured threshold and attach to ctx.
	bundleCfg := artifact.Config{
		Enabled:  true,
		RootPath: t.TempDir(),
		Triggers: artifact.TriggerConfig{
			QualityFlagThreshold: "warning",
		},
	}
	b, err := artifact.OpenDeferredBundle(bundleCfg, "rid-cleaner-pin", "TEST", artifact.TriggerOnQualityFlag)
	require.NoError(t, err)
	require.NotNil(t, b)
	t.Cleanup(func() { _ = b.Close() })

	ctx := artifact.Inject(context.Background(), b)

	// Data with goodwill > 25% of assets — triggers excessive_goodwill_warning
	// (severity=warning) in createHardcodedRiskFlags.
	data := &entities.FinancialData{
		Ticker:                   "TEST1", // Test ticker maps to general rules
		Revenue:                  500_000_000,
		TotalAssets:              1_000_000_000,
		Goodwill:                 400_000_000, // 40% — triggers warning
		OtherIntangibles:         300_000_000, // 30% — also triggers warning
		SharesOutstanding:        100_000_000,
		DilutedSharesOutstanding: 100_000_000,
		FilingPeriod:             "2024Q3",
		FilingDate:               time.Now().AddDate(0, -3, 0),
		HasNormalizedData:        true,
	}

	result, err := svc.CleanFinancialData(ctx, data)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Flags, "test data must produce at least one cleaner flag")

	// Bundle must reflect the count of qualifying flags. We don't pin an
	// exact number (the rules engine may add or remove flags as it evolves),
	// but the count must be non-zero AND match the locally computed total.
	wantCount := countQualifyingFlags(result.Flags, "warning")
	require.Greater(t, wantCount, 0,
		"test data must produce at least one warning-or-above flag for this assertion to be meaningful")
	assert.EqualValues(t, wantCount, b.QualityFlagCount(),
		"bundle's QualityFlagCount must equal the cleaner's qualifying-flag count")
}

// TestCleanService_NoOpWhenBundleAbsent — the cleaner must not panic and
// must not allocate when no bundle is on ctx (the dominant production path
// when the trigger is disabled). This guards against a regression that
// adds an unguarded call to the bundle API.
func TestCleanService_NoOpWhenBundleAbsent(t *testing.T) {
	cfg := createTestConfig()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// No bundle injection — ctx carries no artifact.Bundle.
	data := &entities.FinancialData{
		Ticker:                   "TEST1",
		Revenue:                  500_000_000,
		TotalAssets:              1_000_000_000,
		Goodwill:                 400_000_000,
		SharesOutstanding:        100_000_000,
		DilutedSharesOutstanding: 100_000_000,
		FilingPeriod:             "2024Q3",
		FilingDate:               time.Now().AddDate(0, -3, 0),
		HasNormalizedData:        true,
	}

	// Must not panic and must return a result.
	result, err := svc.CleanFinancialData(context.Background(), data)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// TestCleanService_NoOpWhenThresholdEmpty — bundle is on ctx but the
// configured threshold is empty (default). The cleaner must still run
// successfully and the bundle's QualityFlagCount must remain 0 — calling
// RecordQualityFlagCount with the count would be wasted work since the
// middleware's promote check ignores it when threshold is empty.
func TestCleanService_NoOpWhenThresholdEmpty(t *testing.T) {
	cfg := createTestConfig()
	svc, err := NewDataCleanerService(cfg, &mockAIServiceDataCleaner{}, nil)
	require.NoError(t, err)

	// Bundle is on ctx but with NO threshold configured.
	bundleCfg := artifact.Config{Enabled: true, RootPath: t.TempDir()}
	b, err := artifact.OpenDeferredBundle(bundleCfg, "rid-empty-thr", "TEST", artifact.TriggerOnError)
	require.NoError(t, err)
	require.NotNil(t, b)
	t.Cleanup(func() { _ = b.Close() })

	ctx := artifact.Inject(context.Background(), b)

	data := &entities.FinancialData{
		Ticker:                   "TEST1",
		Revenue:                  500_000_000,
		TotalAssets:              1_000_000_000,
		Goodwill:                 400_000_000,
		SharesOutstanding:        100_000_000,
		DilutedSharesOutstanding: 100_000_000,
		FilingPeriod:             "2024Q3",
		FilingDate:               time.Now().AddDate(0, -3, 0),
		HasNormalizedData:        true,
	}

	_, err = svc.CleanFinancialData(ctx, data)
	require.NoError(t, err)

	assert.Equal(t, int64(0), b.QualityFlagCount(),
		"empty-threshold bundle must keep count at zero (no recording)")
}
