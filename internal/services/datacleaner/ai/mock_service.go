package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// MockAIService provides a mock implementation for testing and development
// TODO: Replace with actual AI service integration when ready
type MockAIService struct {
	config  *AIServiceConfig
	metrics *AIServiceMetrics
}

// NewMockAIService creates a new mock AI service instance
func NewMockAIService(config *AIServiceConfig) *MockAIService {
	return &MockAIService{
		config: config,
		metrics: &AIServiceMetrics{
			TotalRequests:       0,
			SuccessfulRequests:  0,
			FailedRequests:      0,
			AverageResponseTime: 100 * time.Millisecond, // Simulate fast mock responses
			CacheHitRate:        0.0,
		},
	}
}

// AnalyzeFootnote performs mock analysis on footnote text
func (m *MockAIService) AnalyzeFootnote(ctx context.Context, request *FootnoteAnalysisRequest) (*FootnoteAnalysisResponse, error) {
	startTime := time.Now()
	m.metrics.TotalRequests++

	// Simulate processing delay
	time.Sleep(50 * time.Millisecond)

	// Check for context cancellation
	if err := ctx.Err(); err != nil {
		m.metrics.FailedRequests++
		return nil, err
	}

	// Generate mock response based on analysis type
	response := &FootnoteAnalysisResponse{
		RequestID:         fmt.Sprintf("mock_%d", time.Now().UnixNano()),
		Ticker:            request.Ticker,
		AnalysisType:      request.AnalysisType,
		Confidence:        0.85, // Mock high confidence
		ExtractedData:     make(map[string]interface{}),
		Recommendations:   []string{},
		Flags:             []entities.Flag{},
		ProcessingTime:    time.Since(startTime),
		ResponseTimestamp: time.Now(),
	}

	// Generate mock data based on analysis type
	switch request.AnalysisType {
	case ContingentLiabilityAnalysis:
		response.ExtractedData = m.generateMockContingentLiability(request)
		response.Recommendations = []string{
			"Consider recording 60% probability-weighted liability",
			"Monitor quarterly for changes in litigation status",
			"Disclose range of possible outcomes in footnotes",
		}

	case PensionObligationAnalysis:
		response.ExtractedData = m.generateMockPensionData(request)
		response.Recommendations = []string{
			"Underfunded pension plan requires additional contributions",
			"Consider impact of discount rate changes on obligations",
			"Review actuarial assumptions for reasonableness",
		}

	case OperatingLeaseAnalysis:
		response.ExtractedData = m.generateMockLeaseData(request)
		response.Recommendations = []string{
			"Capitalize operating leases under ASC 842",
			"Use incremental borrowing rate for present value calculation",
			"Consider lease modification impacts",
		}

	case RestructuringAnalysis:
		response.ExtractedData = m.generateMockRestructuringData(request)
		response.Recommendations = []string{
			"Exclude one-time restructuring charges from normalized earnings",
			"Monitor for recurring nature of restructuring activities",
			"Verify cash vs. non-cash components",
		}

	case LitigationAnalysis:
		response.ExtractedData = m.generateMockLitigationData(request)
		response.Recommendations = []string{
			"Assess probability of adverse outcome",
			"Consider settlement likelihood and amounts",
			"Monitor for new developments in legal proceedings",
		}

	case StockCompensationAnalysis:
		response.ExtractedData = m.generateMockStockCompData(request)
		response.Recommendations = []string{
			"Consider dilutive impact on per-share metrics",
			"Analyze vesting schedules and exercise patterns",
			"Review fair value methodology for options",
		}

	default:
		response.ExtractedData["analysis_type"] = "unsupported"
		response.Confidence = 0.0
		response.Recommendations = []string{"Analysis type not supported in mock service"}
	}

	// Generate mock flags if needed
	if response.Confidence > 0.7 {
		flag := entities.Flag{
			ID:             fmt.Sprintf("ai_flag_%d", time.Now().UnixNano()),
			RuleID:         fmt.Sprintf("ai_%s", request.AnalysisType),
			Type:           "ai_analysis",
			Severity:       entities.Info,
			Amount:         0, // Will be set based on extracted data
			Percentage:     0,
			Description:    fmt.Sprintf("AI analysis completed for %s", request.AnalysisType),
			Recommendation: strings.Join(response.Recommendations, "; "),
			Timestamp:      time.Now(),
		}
		response.Flags = append(response.Flags, flag)
	}

	m.metrics.SuccessfulRequests++
	return response, nil
}

// BatchAnalyzeFootnotes processes multiple footnotes in batch
func (m *MockAIService) BatchAnalyzeFootnotes(ctx context.Context, requests []*FootnoteAnalysisRequest) ([]*FootnoteAnalysisResponse, error) {
	responses := make([]*FootnoteAnalysisResponse, 0, len(requests))

	for _, request := range requests {
		response, err := m.AnalyzeFootnote(ctx, request)
		if err != nil {
			return responses, err
		}
		responses = append(responses, response)
	}

	return responses, nil
}

// GetAnalysisCapabilities returns supported analysis types
func (m *MockAIService) GetAnalysisCapabilities() []FootnoteAnalysisType {
	return []FootnoteAnalysisType{
		ContingentLiabilityAnalysis,
		PensionObligationAnalysis,
		OperatingLeaseAnalysis,
		RestructuringAnalysis,
		LitigationAnalysis,
		StockCompensationAnalysis,
		DerivativeAnalysis,
		IntangibleValuationAnalysis,
		InventoryObsolescenceAnalysis,
		DeferredTaxAnalysis,
		ComprehensiveAnalysis,
	}
}

// HealthCheck verifies AI service availability
func (m *MockAIService) HealthCheck(ctx context.Context) error {
	// Mock service is always healthy
	return nil
}

// GetMetrics returns current service metrics
func (m *MockAIService) GetMetrics() *AIServiceMetrics {
	return m.metrics
}

// generateMockContingentLiability creates mock contingent liability data
func (m *MockAIService) generateMockContingentLiability(request *FootnoteAnalysisRequest) map[string]interface{} {
	return map[string]interface{}{
		"contingent_liability": ContingentLiabilityEstimate{
			LiabilityType:        "litigation",
			EstimatedAmount:      50000000, // $50M estimated
			ProbabilityRange:     "reasonably possible",
			ProbabilityPercent:   60.0,
			ConfidenceLevel:      0.85,
			SupportingEvidence:   []string{"ongoing patent litigation", "similar case precedents"},
			RecommendedTreatment: "record",
		},
	}
}

// generateMockPensionData creates mock pension obligation data
func (m *MockAIService) generateMockPensionData(request *FootnoteAnalysisRequest) map[string]interface{} {
	return map[string]interface{}{
		"pension_obligation": PensionObligationData{
			PlanType:         "defined_benefit",
			ProjectedBenefit: 500000000, // $500M PBO
			PlanAssets:       450000000, // $450M assets
			FundingStatus:    "underfunded",
			UnfundedAmount:   50000000, // $50M shortfall
			DiscountRate:     0.045,    // 4.5%
			ExpectedReturn:   0.065,    // 6.5%
			ServiceCost:      25000000, // $25M annual
			ConfidenceLevel:  0.90,
		},
	}
}

// generateMockLeaseData creates mock operating lease data
func (m *MockAIService) generateMockLeaseData(request *FootnoteAnalysisRequest) map[string]interface{} {
	return map[string]interface{}{
		"operating_lease": OperatingLeaseData{
			TotalCommitments:    200000000, // $200M total
			YearlyCommitments:   []float64{40000000, 35000000, 30000000, 25000000, 20000000}, // 5-year breakdown
			WeightedAverageRate: 0.055,     // 5.5%
			WeightedAverageTerm: 4.2,       // 4.2 years
			PresentValue:        175000000, // $175M NPV
			ConfidenceLevel:     0.88,
		},
	}
}

// generateMockRestructuringData creates mock restructuring data
func (m *MockAIService) generateMockRestructuringData(request *FootnoteAnalysisRequest) map[string]interface{} {
	return map[string]interface{}{
		"restructuring": RestructuringData{
			ChargeType:         "facility_closure",
			TotalCharge:        75000000, // $75M total
			CashPortion:        60000000, // $60M cash
			NonCashPortion:     15000000, // $15M non-cash
			RecurringNature:    false,
			ExpectedCompletion: "Q2 2025",
			BusinessRationale:  "Consolidation of manufacturing facilities",
			ConfidenceLevel:    0.92,
		},
	}
}

// generateMockLitigationData creates mock litigation data
func (m *MockAIService) generateMockLitigationData(request *FootnoteAnalysisRequest) map[string]interface{} {
	return map[string]interface{}{
		"litigation": map[string]interface{}{
			"case_type":           "patent_infringement",
			"estimated_exposure":  100000000, // $100M potential exposure
			"probability_adverse": 0.40,      // 40% chance of adverse outcome
			"settlement_range":    []float64{30000000, 80000000}, // $30M-$80M settlement range
			"confidence_level":    0.75,
		},
	}
}

// generateMockStockCompData creates mock stock compensation data
func (m *MockAIService) generateMockStockCompData(request *FootnoteAnalysisRequest) map[string]interface{} {
	return map[string]interface{}{
		"stock_compensation": map[string]interface{}{
			"total_expense":       120000000, // $120M total expense
			"options_expense":     80000000,  // $80M from options
			"rsu_expense":         40000000,  // $40M from RSUs
			"dilution_impact":     0.025,     // 2.5% dilution
			"vesting_schedule":    "4_year_graded",
			"fair_value_method":   "black_scholes",
			"confidence_level":    0.95,
		},
	}
}
