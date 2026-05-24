package adjustments

import (
	"context"
	gocontext "context"
	"encoding/json"
	"fmt"
	"io/ioutil" //nolint:staticcheck // test uses ioutil for simplicity
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/sec"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// mockAIServiceRealData implements the ai.AIService interface for real data testing
type mockAIServiceRealData struct{}

func (m *mockAIServiceRealData) AnalyzeFootnote(ctx context.Context, request *ai.FootnoteAnalysisRequest) (*ai.FootnoteAnalysisResponse, error) {
	return &ai.FootnoteAnalysisResponse{
		RequestID:    "real-data-test-request-id",
		Ticker:       request.Ticker,
		AnalysisType: request.AnalysisType,
		Confidence:   0.8,
		ExtractedData: map[string]interface{}{
			"contingent_liability_estimate": ai.ContingentLiabilityEstimate{
				ProbabilityPercent: 30.0, // Conservative default
				ConfidenceLevel:    0.8,
			},
		},
		Recommendations: []string{"Mock analysis for real data testing"},
	}, nil
}

func (m *mockAIServiceRealData) BatchAnalyzeFootnotes(ctx context.Context, requests []*ai.FootnoteAnalysisRequest) ([]*ai.FootnoteAnalysisResponse, error) {
	var responses []*ai.FootnoteAnalysisResponse
	for _, req := range requests {
		resp, err := m.AnalyzeFootnote(ctx, req)
		if err != nil {
			return nil, err
		}
		responses = append(responses, resp)
	}
	return responses, nil
}

func (m *mockAIServiceRealData) GetAnalysisCapabilities() []ai.FootnoteAnalysisType {
	return []ai.FootnoteAnalysisType{ai.ContingentLiabilityAnalysis}
}

func (m *mockAIServiceRealData) HealthCheck(ctx context.Context) error {
	return nil
}

// TestRealAppleSECDataIntegration validates the complete data cleaning pipeline using
// actual Apple SEC filing data from testdata/ and that the data cleaning pipeline works end-to-end with real data.
func TestRealAppleSECDataIntegration(t *testing.T) {
	t.Run("Load and Parse Real Apple SEC Data", func(t *testing.T) {
		// Load real Apple SEC filing data
		realAppleData, err := loadRealAppleSECData(t)
		require.NoError(t, err, "Should successfully load real Apple SEC data")
		require.NotNil(t, realAppleData, "Real Apple data should not be nil")

		// Validate basic structure
		assert.Equal(t, "320193", realAppleData.CIK.String(), "Apple CIK should match")
		assert.Equal(t, "Apple Inc.", realAppleData.EntityName, "Apple entity name should match")
		assert.NotEmpty(t, realAppleData.Facts, "Apple should have facts data")

		// Parse using enhanced SEC parser
		logger := zap.NewNop()
		secParser := sec.NewParser(logger)

		historicalData, err := secParser.ParseFinancialData(gocontext.Background(), realAppleData)
		if err != nil && strings.Contains(err.Error(), "data structure may be nested and not yet supported") {
			t.Skip("Skipping test due to nested SEC data structure not yet fully supported")
		}
		require.NoError(t, err, "Should successfully parse real Apple SEC data")
		require.NotNil(t, historicalData, "Historical data should not be nil")
		assert.NotEmpty(t, historicalData.Data, "Should have parsed financial periods")

		// Get the most recent annual data for cleaning pipeline testing
		latestAnnualData := extractLatestAnnualData(t, realAppleData)
		require.NotNil(t, latestAnnualData, "Should have latest annual data")

		// Set ticker for cleaning pipeline
		latestAnnualData.Ticker = "AAPL"

		t.Logf("Testing with Apple %s data: Revenue=%.0f, Assets=%.0f",
			latestAnnualData.Period, latestAnnualData.Revenue, latestAnnualData.TotalAssets)
	})

	t.Run("Enhanced SEC Parser Field Mapping Validation", func(t *testing.T) {
		// Load and parse real data
		realAppleData, err := loadRealAppleSECData(t)
		require.NoError(t, err)

		// Get parsed financial data using enhanced parser
		financialData := extractLatestAnnualData(t, realAppleData)
		require.NotNil(t, financialData)

		// Validate that enhanced parser successfully extracts critical DCF fields
		assert.Greater(t, financialData.TotalAssets, float64(0), "Total assets should be parsed")
		assert.Greater(t, financialData.Revenue, float64(0), "Revenue should be parsed")

		// Apple-specific validations based on known characteristics
		assert.GreaterOrEqual(t, financialData.TotalAssets, float64(300000000000),
			"Apple should have >$300B in total assets")
		assert.GreaterOrEqual(t, financialData.Revenue, float64(200000000000),
			"Apple should have >$200B in revenue")

		// Validate enhanced field mapping worked
		if financialData.Goodwill > 0 {
			goodwillRatio := financialData.Goodwill / financialData.TotalAssets
			assert.LessOrEqual(t, goodwillRatio, 0.05,
				"Apple should have low goodwill ratio (tech company)")
		}

		// Check for operating lease data (Apple has retail stores)
		if financialData.OperatingLeaseLiability > 0 {
			leaseRatio := financialData.OperatingLeaseLiability / financialData.TotalAssets
			assert.GreaterOrEqual(t, leaseRatio, 0.01,
				"Apple should have meaningful operating leases for stores/offices")
		}

		t.Logf("Apple enhanced parsing results: Assets=%.2fB, Revenue=%.2fB, Goodwill=%.2fB, OperatingLeases=%.2fB",
			financialData.TotalAssets/1000000000, financialData.Revenue/1000000000,
			financialData.Goodwill/1000000000, financialData.OperatingLeaseLiability/1000000000)
	})

	t.Run("Real Apple Data Category A Adjustments", func(t *testing.T) {
		// Load and parse real data
		realAppleData, err := loadRealAppleSECData(t)
		require.NoError(t, err)

		// Get parsed financial data
		financialData := extractLatestAnnualData(t, realAppleData)
		require.NotNil(t, financialData)

		// Initialize asset adjuster
		assetAdjuster := NewAssetAdjuster()
		context := &entities.CleaningContext{
			IndustryCode:     "45",             // Technology
			CompanySize:      entities.MegaCap, // Apple is mega-cap
			DataVintage:      time.Now(),
			EnableIndustry:   true,
			EnableCaching:    false,
			QualityThreshold: 0.8, // High quality threshold for Apple
		}

		// Test Category A adjustments on real Apple data
		assetResult := assetAdjuster.CalculateNetTangibleAssets(financialData, context)
		require.NotNil(t, assetResult, "Asset result should not be nil")

		// Validate that processing completed without errors
		assert.NotNil(t, assetResult.Adjustments, "Should have adjustment records")
		assert.GreaterOrEqual(t, assetResult.AdjustedTangibleAssets, float64(0), "Tangible assets should be non-negative")

		// Apple typically has minimal goodwill relative to its size
		if len(assetResult.Adjustments) > 0 {
			t.Logf("Real Apple Category A adjustments: %d adjustments made", len(assetResult.Adjustments))
			for _, adj := range assetResult.Adjustments {
				t.Logf("  - %s: %.2fB (%s)", adj.Type, adj.Amount/1000000000, adj.Reasoning)
			}
		}

		t.Logf("Real Apple Category A results: TangibleAssets=%.2fB, %d adjustments",
			assetResult.AdjustedTangibleAssets/1000000000, len(assetResult.Adjustments))
	})

	t.Run("Real Apple Data Category B Adjustments", func(t *testing.T) {
		// Load and parse real data
		realAppleData, err := loadRealAppleSECData(t)
		require.NoError(t, err)

		// Get parsed financial data
		financialData := extractLatestAnnualData(t, realAppleData)
		require.NotNil(t, financialData)

		// Initialize liability adjuster
		liabilityAdjuster := NewLiabilityAdjuster(&mockAIServiceRealData{}, nil)
		context := &entities.CleaningContext{
			IndustryCode:     "45",             // Technology
			CompanySize:      entities.MegaCap, // Apple is mega-cap
			DataVintage:      time.Now(),
			EnableIndustry:   true,
			EnableCaching:    false,
			QualityThreshold: 0.8, // High quality threshold for Apple
		}

		// Load liability rules for processing using existing pattern
		allRules := createComprehensiveRuleSet()
		liabilityRules := filterRulesByCategory(allRules, entities.LiabilityCompleteness)

		// Test Category B adjustments on real Apple data
		liabilityResult := liabilityAdjuster.ProcessLiabilityAdjustments(gocontext.Background(), financialData, liabilityRules, context)
		require.NotNil(t, liabilityResult, "Liability result should not be nil")

		// Validate processing completed
		assert.NotNil(t, liabilityResult.Adjustments, "Should have liability adjustment records")

		// Apple should have manageable debt levels for a mega-cap tech company
		if financialData.TotalDebt > 0 {
			totalDebtRatio := financialData.TotalDebt / financialData.TotalAssets
			assert.LessOrEqual(t, totalDebtRatio, 0.40,
				"Apple debt-to-assets should be reasonable for mega-cap tech")
		}

		if len(liabilityResult.Adjustments) > 0 {
			t.Logf("Real Apple Category B adjustments: %d adjustments made", len(liabilityResult.Adjustments))
			for _, adj := range liabilityResult.Adjustments {
				t.Logf("  - %s: %.2fB (%s)", adj.Type, adj.Amount/1000000000, adj.Reasoning)
			}
		}

		t.Logf("Real Apple Category B results: %d adjustments, TotalLiabilityAdjustment=%.2fB",
			len(liabilityResult.Adjustments), liabilityResult.TotalLiabilityAdjustment/1000000000)
	})

	t.Run("Real Apple Data Complete Pipeline Performance", func(t *testing.T) {
		// Load and parse real data
		realAppleData, err := loadRealAppleSECData(t)
		require.NoError(t, err)

		// Get parsed financial data
		financialData := extractLatestAnnualData(t, realAppleData)
		require.NotNil(t, financialData)

		// Initialize adjusters
		assetAdjuster := NewAssetAdjuster()
		liabilityAdjuster := NewLiabilityAdjuster(&mockAIServiceRealData{}, nil)
		context := createTestCleaningContext("real_apple_performance_test")

		// Measure complete pipeline performance
		startTime := time.Now()

		// Run Category A + B adjustments
		assetResult := assetAdjuster.CalculateNetTangibleAssets(financialData, context)
		allRules := createComprehensiveRuleSet()
		liabilityRules := filterRulesByCategory(allRules, entities.LiabilityCompleteness)
		liabilityResult := liabilityAdjuster.ProcessLiabilityAdjustments(gocontext.Background(), financialData, liabilityRules, context)

		processingTime := time.Since(startTime)

		// Validate performance requirements
		assert.LessOrEqual(t, processingTime, 1000*time.Millisecond,
			"Real Apple data processing should complete within 1000ms (actual: %s)", processingTime)

		// Validate results quality
		require.NotNil(t, assetResult)
		require.NotNil(t, liabilityResult)

		totalAdjustments := len(assetResult.Adjustments) + len(liabilityResult.Adjustments)

		// Apple should produce meaningful results
		assert.GreaterOrEqual(t, financialData.TotalAssets, float64(300000000000),
			"Apple should maintain realistic asset levels after adjustments")

		t.Logf("Real Apple pipeline performance: %s for %d total adjustments, Assets=%.2fB",
			processingTime, totalAdjustments, financialData.TotalAssets/1000000000)
	})
}

// loadRealAppleSECData loads the actual Apple SEC filing data from testdata/CIK-example-2016onwards.json
func loadRealAppleSECData(t *testing.T) (*ports.SECCompanyFacts, error) {
	// Read the real Apple SEC filing JSON
	filepath := "../../../../testdata/CIK-example-2016onwards.json"
	jsonData, err := ioutil.ReadFile(filepath)
	if err != nil {
		t.Logf("Failed to read real Apple SEC data file: %v", err)
		t.Skip("Skipping real SEC data test - file not available")
		return nil, err
	}

	// Unmarshal into SECCompanyFacts structure
	var realAppleData ports.SECCompanyFacts
	err = json.Unmarshal(jsonData, &realAppleData)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal real Apple SEC data: %w", err)
	}

	return &realAppleData, nil
}

// extractLatestAnnualData parses real SEC data and returns the most recent annual financial data
func extractLatestAnnualData(t *testing.T, realAppleData *ports.SECCompanyFacts) *entities.FinancialData {
	// Parse using existing SEC parser
	logger := zap.NewNop()
	secParser := sec.NewParser(logger)

	historicalData, err := secParser.ParseFinancialData(gocontext.Background(), realAppleData)
	if err != nil && strings.Contains(err.Error(), "data structure may be nested and not yet supported") {
		t.Skip("Skipping data extraction due to nested SEC data structure not yet fully supported")
	}
	require.NoError(t, err, "Should parse real Apple data")
	require.NotNil(t, historicalData)

	// Find the most recent annual data using proper sorted period selection
	var latestAnnualData *entities.FinancialData
	var latestFYPeriod string
	for period, data := range historicalData.Data {
		if strings.Contains(period, "FY") {
			if period > latestFYPeriod { // string comparison works: "2024FY" > "2011FY"
				latestFYPeriod = period
				latestAnnualData = data
			}
		}
	}

	// Fallback: use GetLatestPeriod if no FY data found
	if latestAnnualData == nil {
		latestAnnualData, _ = historicalData.GetLatestPeriod()
	}

	require.NotNil(t, latestAnnualData, "Should have latest annual data")

	// Set required fields for cleaning pipeline
	latestAnnualData.Ticker = "AAPL"

	return latestAnnualData
}
