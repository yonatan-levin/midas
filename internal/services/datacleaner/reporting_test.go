package datacleaner

import (
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCleaningReportGenerator tests the main report generation functionality
func TestCleaningReportGenerator_GenerateReport(t *testing.T) {
	tests := []struct {
		name           string
		pipelineResult *PipelineResult
		originalData   *entities.FinancialData
		expectError    bool
		expectSections int
		expectQuality  entities.QualityGrade
	}{
		{
			name: "comprehensive_cleaning_report",
			pipelineResult: &PipelineResult{
				Success:       true,
				TotalDuration: 45 * time.Millisecond,
				StageResults: []StageResult{
					{
						Stage:   AssetQualityStage,
						Success: true,
						Adjustments: []entities.Adjustment{
							{
								Type:        entities.Exclude,
								Amount:      100000,
								FromAccount: "Goodwill",
								Reasoning:   "goodwill_exclusion: Excluded goodwill for asset quality",
								Applied:     true,
							},
						},
						Flags: []entities.Flag{
							{
								Type:        "goodwill_exclusion",
								Severity:    entities.FlagSeverityHigh,
								Amount:      100000,
								Percentage:  10.0,
								Description: "Significant goodwill excluded",
							},
						},
						Duration:     15 * time.Millisecond,
						RulesApplied: 3,
					},
				},
				CleanedData: &entities.FinancialData{
					Ticker:      "AAPL",
					TotalAssets: 900000, // Reduced from 1M due to goodwill exclusion
					Revenue:     500000,
				},
				Summary: PipelineSummary{
					TotalAdjustments:  1,
					TotalFlags:        1,
					TotalRulesApplied: 3,
					StagesProcessed:   1,
				},
			},
			originalData: &entities.FinancialData{
				Ticker:      "AAPL",
				TotalAssets: 1000000,
				Goodwill:    100000,
				Revenue:     500000,
			},
			expectError:    false,
			expectSections: 5,               // Executive Summary, Adjustments, Flags, Quality Assessment, Audit Trail
			expectQuality:  entities.GradeB, // Good quality after cleaning
		},
		{
			name: "no_adjustments_report",
			pipelineResult: &PipelineResult{
				Success:       true,
				TotalDuration: 20 * time.Millisecond,
				StageResults:  []StageResult{},
				CleanedData: &entities.FinancialData{
					Ticker:      "MSFT",
					TotalAssets: 500000,
					Revenue:     300000,
				},
				Summary: PipelineSummary{
					TotalAdjustments:  0,
					TotalFlags:        0,
					TotalRulesApplied: 0,
					StagesProcessed:   0,
				},
			},
			originalData: &entities.FinancialData{
				Ticker:      "MSFT",
				TotalAssets: 500000,
				Revenue:     300000,
			},
			expectError:    false,
			expectSections: 3,               // Fewer sections when no adjustments
			expectQuality:  entities.GradeA, // Excellent - no issues found
		},
		{
			name: "failed_pipeline_report",
			pipelineResult: &PipelineResult{
				Success:       false,
				TotalDuration: 10 * time.Millisecond,
				StageResults: []StageResult{
					{
						Stage:   AssetQualityStage,
						Success: false,
						Errors:  []string{"Data validation failed"},
					},
				},
				CleanedData: nil, // Failed pipeline has no cleaned data
				Summary: PipelineSummary{
					ErrorCount: 1,
				},
			},
			originalData: &entities.FinancialData{
				Ticker: "INVALID",
			},
			expectError:    false,
			expectSections: 4,               // Executive Summary + Quality Assessment + Audit Trail + Error Summary
			expectQuality:  entities.GradeF, // Failed processing
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			generator := NewCleaningReportGenerator()

			// Act
			report, err := generator.GenerateReport(tt.pipelineResult, tt.originalData)

			// Assert
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, report)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, report)
				assert.Equal(t, tt.expectSections, len(report.Sections))
				assert.Equal(t, tt.expectQuality, report.QualityGrade)
				assert.NotEmpty(t, report.GeneratedAt)
				expectedTicker := tt.originalData.Ticker
				if tt.pipelineResult.CleanedData != nil {
					expectedTicker = tt.pipelineResult.CleanedData.Ticker
				}
				assert.Equal(t, expectedTicker, report.Ticker)
			}
		})
	}
}

// TestCleaningReportGenerator_AuditTrail tests audit trail generation
func TestCleaningReportGenerator_AuditTrail(t *testing.T) {
	generator := NewCleaningReportGenerator()

	pipelineResult := &PipelineResult{
		Success: true,
		StageResults: []StageResult{
			{
				Stage:   AssetQualityStage,
				Success: true,
				Adjustments: []entities.Adjustment{
					{
						ID:          "adj-1",
						Type:        entities.Exclude,
						Amount:      50000,
						FromAccount: "Goodwill",
						Reasoning:   "goodwill_exclusion: Asset quality improvement",
						Applied:     true,
						Timestamp:   time.Now(),
					},
				},
				Duration: 10 * time.Millisecond,
			},
			{
				Stage:   LiabilityCompletenessStage,
				Success: true,
				Adjustments: []entities.Adjustment{
					{
						ID:          "adj-2",
						Type:        entities.TreatAsDebt,
						Amount:      25000,
						FromAccount: "OperatingLease",
						Reasoning:   "operating_lease_obligation: Liability completeness",
						Applied:     true,
						Timestamp:   time.Now(),
					},
				},
				Duration: 15 * time.Millisecond,
			},
		},
	}

	originalData := &entities.FinancialData{
		Ticker:      "TEST",
		TotalAssets: 1000000,
	}

	// Generate report
	report, err := generator.GenerateReport(pipelineResult, originalData)
	require.NoError(t, err)

	// Verify audit trail section
	auditSection := findReportSection(report, "Audit Trail")
	require.NotNil(t, auditSection, "Audit Trail section should be present")

	// Should contain all adjustments in chronological order
	assert.Contains(t, auditSection.Content, "adj-1")
	assert.Contains(t, auditSection.Content, "adj-2")
	assert.Contains(t, auditSection.Content, "goodwill_exclusion")
	assert.Contains(t, auditSection.Content, "operating_lease_obligation")

	// Verify audit trail metadata
	auditTrail := report.AuditTrail
	assert.Equal(t, 2, len(auditTrail.Adjustments))
	assert.Equal(t, 2, auditTrail.StagesProcessed)
	assert.True(t, auditTrail.TotalDuration > 20*time.Millisecond) // Sum of stage durations
}

// TestCleaningReportGenerator_QualityAssessment tests quality scoring and assessment
func TestCleaningReportGenerator_QualityAssessment(t *testing.T) {
	tests := []struct {
		name            string
		flagSeverities  []entities.FlagSeverity
		adjustmentCount int
		expectGrade     entities.QualityGrade
		expectScore     float64 // Approximate range
	}{
		{
			name:            "excellent_quality",
			flagSeverities:  []entities.FlagSeverity{},
			adjustmentCount: 0,
			expectGrade:     entities.GradeA,
			expectScore:     95.0,
		},
		{
			name:            "good_quality_minor_adjustments",
			flagSeverities:  []entities.FlagSeverity{entities.FlagSeverityLow},
			adjustmentCount: 1,
			expectGrade:     entities.GradeB,
			expectScore:     87.0, // 100 - 10 (low flag) - 3 (1 adjustment)
		},
		{
			name:            "fair_quality_moderate_issues",
			flagSeverities:  []entities.FlagSeverity{entities.FlagSeverityMedium, entities.FlagSeverityLow},
			adjustmentCount: 3,
			expectGrade:     entities.GradeC,
			expectScore:     71.0, // 100 - 10 (medium) - 10 (low) - 9 (3 adjustments)
		},
		{
			name:            "poor_quality_significant_issues",
			flagSeverities:  []entities.FlagSeverity{entities.FlagSeverityHigh, entities.FlagSeverityMedium},
			adjustmentCount: 5,
			expectGrade:     entities.GradeD,
			expectScore:     60.0, // 100 - 15 (high) - 10 (medium) - 15 (5 adjustments)
		},
		{
			name:            "failed_quality_critical_issues",
			flagSeverities:  []entities.FlagSeverity{entities.FlagSeverityCritical, entities.FlagSeverityHigh},
			adjustmentCount: 8,
			expectGrade:     entities.GradeF,
			expectScore:     31.0, // 100 - 30 (critical) - 15 (high) - 24 (8 adjustments)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generator := NewCleaningReportGenerator()

			// Create flags and adjustments based on test case
			flags := make([]entities.Flag, len(tt.flagSeverities))
			for i, severity := range tt.flagSeverities {
				flags[i] = entities.Flag{
					Severity: severity,
					Amount:   float64((i + 1) * 10000),
				}
			}

			adjustments := make([]entities.Adjustment, tt.adjustmentCount)
			for i := 0; i < tt.adjustmentCount; i++ {
				adjustments[i] = entities.Adjustment{
					Amount:  float64((i + 1) * 5000),
					Applied: true,
				}
			}

			pipelineResult := &PipelineResult{
				Success: true,
				StageResults: []StageResult{
					{
						Flags:       flags,
						Adjustments: adjustments,
					},
				},
			}

			originalData := &entities.FinancialData{Ticker: "TEST"}

			// Generate report and assess quality
			report, err := generator.GenerateReport(pipelineResult, originalData)
			require.NoError(t, err)

			// Verify quality assessment
			assert.Equal(t, tt.expectGrade, report.QualityGrade)
			assert.InDelta(t, tt.expectScore, report.QualityScore, 10.0, "Quality score should be within ±10 points")
		})
	}
}

// TestCleaningReportGenerator_DTOFormat tests the report DTO formatting
func TestCleaningReportGenerator_DTOFormat(t *testing.T) {
	generator := NewCleaningReportGenerator()

	pipelineResult := &PipelineResult{
		Success:       true,
		TotalDuration: 50 * time.Millisecond,
		StageResults: []StageResult{
			{
				Stage:        AssetQualityStage,
				Success:      true,
				Adjustments:  []entities.Adjustment{{Amount: 1000}},
				Flags:        []entities.Flag{{Severity: entities.FlagSeverityLow}},
				RulesApplied: 2,
			},
		},
		CleanedData: &entities.FinancialData{
			Ticker:      "FORMAT_TEST",
			TotalAssets: 500000,
		},
	}

	originalData := &entities.FinancialData{
		Ticker:      "FORMAT_TEST",
		TotalAssets: 600000,
	}

	report, err := generator.GenerateReport(pipelineResult, originalData)
	require.NoError(t, err)

	// Verify DTO structure
	assert.NotEmpty(t, report.ReportID)
	assert.Equal(t, "FORMAT_TEST", report.Ticker)
	assert.True(t, report.ProcessingTime > 0)
	assert.NotEmpty(t, report.GeneratedAt)
	assert.NotNil(t, report.Summary)
	assert.NotNil(t, report.AuditTrail)
	assert.True(t, len(report.Sections) > 0)

	// Verify summary statistics
	assert.Equal(t, 1, report.Summary.TotalAdjustments)
	assert.Equal(t, 1, report.Summary.TotalFlags)
	assert.Equal(t, 2, report.Summary.RulesApplied)

	// Verify JSON serialization capability
	assert.True(t, report.GeneratedAt.Unix() > 0, "Timestamp should be serializable")
}

// TestCleaningReportGenerator_Performance tests report generation performance
func TestCleaningReportGenerator_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	generator := NewCleaningReportGenerator()

	// Create a large pipeline result with many adjustments and flags
	adjustments := make([]entities.Adjustment, 50)
	flags := make([]entities.Flag, 25)

	for i := 0; i < 50; i++ {
		adjustments[i] = entities.Adjustment{
			Amount:      float64(i * 1000),
			FromAccount: "TestAccount",
			Applied:     true,
		}
	}

	for i := 0; i < 25; i++ {
		flags[i] = entities.Flag{
			Severity: entities.FlagSeverityLow,
			Amount:   float64(i * 500),
		}
	}

	pipelineResult := &PipelineResult{
		Success: true,
		StageResults: []StageResult{
			{Adjustments: adjustments[:25], Flags: flags[:12]},
			{Adjustments: adjustments[25:], Flags: flags[12:]},
		},
		CleanedData: &entities.FinancialData{Ticker: "PERF_TEST"},
	}

	originalData := &entities.FinancialData{Ticker: "PERF_TEST"}

	// Run performance test
	iterations := 100
	start := time.Now()

	for i := 0; i < iterations; i++ {
		report, err := generator.GenerateReport(pipelineResult, originalData)
		require.NoError(t, err)
		require.NotNil(t, report)
	}

	avgDuration := time.Since(start) / time.Duration(iterations)
	t.Logf("Average report generation time: %v", avgDuration)

	// KPI: Report generation should be < 10ms even for large datasets
	assert.True(t, avgDuration < 10*time.Millisecond,
		"Report generation took %v, expected < 10ms", avgDuration)
}

// Helper function to find a specific section in the report
func findReportSection(report *CleaningReport, sectionName string) *ReportSection {
	for _, section := range report.Sections {
		if section.Title == sectionName {
			return &section
		}
	}
	return nil
}

// TestReportSection_Formatting tests individual section formatting
func TestReportSection_Formatting(t *testing.T) {
	generator := NewCleaningReportGenerator()

	// Test executive summary formatting
	summary := ReportSummary{
		TotalAdjustments: 3,
		TotalFlags:       2,
		RulesApplied:     5,
		OriginalAssets:   1000000,
		AdjustedAssets:   950000,
		AdjustmentImpact: -50000,
	}

	section := generator.FormatExecutiveSummary(summary)
	assert.Equal(t, "Executive Summary", section.Title)
	assert.Contains(t, section.Content, "3 adjustments")
	assert.Contains(t, section.Content, "2 flags")
	assert.Contains(t, section.Content, "5 rules")
	assert.Contains(t, section.Content, "$50,000") // Impact formatting

	// Test empty summary handling
	emptySection := generator.FormatExecutiveSummary(ReportSummary{})
	assert.NotEmpty(t, emptySection.Content, "Should handle empty summary gracefully")
}
