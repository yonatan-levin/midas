package ai

import (
	"context"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// FootnoteAnalysisRequest represents a request for AI-powered footnote analysis
type FootnoteAnalysisRequest struct {
	Ticker           string                 `json:"ticker"`
	FilingType       string                 `json:"filing_type"`       // 10-K, 10-Q, etc.
	FootnoteText     string                 `json:"footnote_text"`     // Raw footnote content
	AnalysisType     FootnoteAnalysisType   `json:"analysis_type"`     // Type of analysis needed
	Context          map[string]interface{} `json:"context"`           // Additional context data
	PriorityLevel    PriorityLevel          `json:"priority_level"`    // Processing priority
	RequestTimestamp time.Time              `json:"request_timestamp"`
}

// FootnoteAnalysisResponse represents the AI service response
type FootnoteAnalysisResponse struct {
	RequestID        string                 `json:"request_id"`
	Ticker           string                 `json:"ticker"`
	AnalysisType     FootnoteAnalysisType   `json:"analysis_type"`
	Confidence       float64                `json:"confidence"`        // 0.0 to 1.0
	ExtractedData    map[string]interface{} `json:"extracted_data"`    // Structured data extracted
	Recommendations  []string               `json:"recommendations"`   // AI recommendations
	Flags            []entities.Flag        `json:"flags"`             // Generated flags
	ProcessingTime   time.Duration          `json:"processing_time"`
	ResponseTimestamp time.Time             `json:"response_timestamp"`
	Error            string                 `json:"error,omitempty"`   // Error message if any
}

// FootnoteAnalysisType defines the type of analysis to perform
type FootnoteAnalysisType string

const (
	// Liability Analysis Types
	ContingentLiabilityAnalysis FootnoteAnalysisType = "contingent_liability"
	PensionObligationAnalysis   FootnoteAnalysisType = "pension_obligation"
	OperatingLeaseAnalysis      FootnoteAnalysisType = "operating_lease"
	DebtCovenantAnalysis        FootnoteAnalysisType = "debt_covenant"
	
	// Asset Analysis Types
	IntangibleValuationAnalysis FootnoteAnalysisType = "intangible_valuation"
	InventoryObsolescenceAnalysis FootnoteAnalysisType = "inventory_obsolescence"
	DeferredTaxAnalysis         FootnoteAnalysisType = "deferred_tax"
	
	// Earnings Analysis Types
	RestructuringAnalysis       FootnoteAnalysisType = "restructuring"
	LitigationAnalysis          FootnoteAnalysisType = "litigation"
	StockCompensationAnalysis   FootnoteAnalysisType = "stock_compensation"
	DerivativeAnalysis          FootnoteAnalysisType = "derivative"
	
	// General Analysis
	ComprehensiveAnalysis       FootnoteAnalysisType = "comprehensive"
)

// PriorityLevel defines processing priority for AI requests
type PriorityLevel string

const (
	PriorityLow    PriorityLevel = "low"
	PriorityNormal PriorityLevel = "normal"
	PriorityHigh   PriorityLevel = "high"
	PriorityCritical PriorityLevel = "critical"
)

// AIService defines the interface for AI-powered footnote analysis
type AIService interface {
	// AnalyzeFootnote performs AI analysis on footnote text
	AnalyzeFootnote(ctx context.Context, request *FootnoteAnalysisRequest) (*FootnoteAnalysisResponse, error)
	
	// BatchAnalyzeFootnotes processes multiple footnotes in batch
	BatchAnalyzeFootnotes(ctx context.Context, requests []*FootnoteAnalysisRequest) ([]*FootnoteAnalysisResponse, error)
	
	// GetAnalysisCapabilities returns supported analysis types
	GetAnalysisCapabilities() []FootnoteAnalysisType
	
	// HealthCheck verifies AI service availability
	HealthCheck(ctx context.Context) error
}

// ContingentLiabilityEstimate represents AI-extracted contingent liability data
type ContingentLiabilityEstimate struct {
	LiabilityType        string  `json:"liability_type"`        // "litigation", "warranty", "environmental", etc.
	EstimatedAmount      float64 `json:"estimated_amount"`      // AI-estimated amount
	ProbabilityRange     string  `json:"probability_range"`     // "remote", "reasonably possible", "probable"
	ProbabilityPercent   float64 `json:"probability_percent"`   // Numeric probability (0-100)
	ConfidenceLevel      float64 `json:"confidence_level"`      // AI confidence in estimate
	SupportingEvidence   []string `json:"supporting_evidence"`  // Text evidence from footnotes
	RecommendedTreatment string  `json:"recommended_treatment"` // "record", "disclose", "ignore"
}

// PensionObligationData represents AI-extracted pension data
type PensionObligationData struct {
	PlanType             string  `json:"plan_type"`             // "defined_benefit", "defined_contribution"
	ProjectedBenefit     float64 `json:"projected_benefit"`     // PBO amount
	PlanAssets           float64 `json:"plan_assets"`           // Fair value of plan assets
	FundingStatus        string  `json:"funding_status"`        // "overfunded", "underfunded", "fully_funded"
	UnfundedAmount       float64 `json:"unfunded_amount"`       // Shortfall amount
	DiscountRate         float64 `json:"discount_rate"`         // Discount rate used
	ExpectedReturn       float64 `json:"expected_return"`       // Expected return on assets
	ServiceCost          float64 `json:"service_cost"`          // Annual service cost
	ConfidenceLevel      float64 `json:"confidence_level"`      // AI confidence
}

// OperatingLeaseData represents AI-extracted lease data
type OperatingLeaseData struct {
	TotalCommitments     float64   `json:"total_commitments"`     // Total lease commitments
	YearlyCommitments    []float64 `json:"yearly_commitments"`    // Year-by-year breakdown
	WeightedAverageRate  float64   `json:"weighted_average_rate"` // Implicit rate or estimate
	WeightedAverageTerm  float64   `json:"weighted_average_term"` // Average lease term in years
	PresentValue         float64   `json:"present_value"`         // NPV of lease obligations
	ConfidenceLevel      float64   `json:"confidence_level"`      // AI confidence
}

// RestructuringData represents AI-extracted restructuring information
type RestructuringData struct {
	ChargeType           string  `json:"charge_type"`           // "severance", "facility", "integration", etc.
	TotalCharge          float64 `json:"total_charge"`          // Total restructuring charge
	CashPortion          float64 `json:"cash_portion"`          // Cash component
	NonCashPortion       float64 `json:"non_cash_portion"`      // Non-cash component
	RecurringNature      bool    `json:"recurring_nature"`      // Is this recurring?
	ExpectedCompletion   string  `json:"expected_completion"`   // Timeline for completion
	BusinessRationale    string  `json:"business_rationale"`    // Reason for restructuring
	ConfidenceLevel      float64 `json:"confidence_level"`      // AI confidence
}

// AIServiceConfig represents configuration for AI service integration
type AIServiceConfig struct {
	ServiceType          string        `json:"service_type"`          // "openai", "anthropic", "custom", etc.
	APIEndpoint          string        `json:"api_endpoint"`          // Service endpoint URL
	APIKey               string        `json:"api_key"`               // Authentication key
	Model                string        `json:"model"`                 // Model to use (e.g., "gpt-4", "claude-3")
	MaxTokens            int           `json:"max_tokens"`            // Maximum tokens per request
	Temperature          float64       `json:"temperature"`           // Model temperature setting
	TimeoutSeconds       int           `json:"timeout_seconds"`       // Request timeout
	RetryAttempts        int           `json:"retry_attempts"`        // Number of retry attempts
	RateLimitPerMinute   int           `json:"rate_limit_per_minute"` // Rate limiting
	EnableBatchMode      bool          `json:"enable_batch_mode"`     // Enable batch processing
	CacheResults         bool          `json:"cache_results"`         // Cache AI responses
	CacheTTLHours        int           `json:"cache_ttl_hours"`       // Cache time-to-live
}

// AIServiceMetrics represents metrics for AI service usage
type AIServiceMetrics struct {
	TotalRequests        int64         `json:"total_requests"`
	SuccessfulRequests   int64         `json:"successful_requests"`
	FailedRequests       int64         `json:"failed_requests"`
	AverageResponseTime  time.Duration `json:"average_response_time"`
	TotalTokensUsed      int64         `json:"total_tokens_used"`
	TotalCostUSD         float64       `json:"total_cost_usd"`
	CacheHitRate         float64       `json:"cache_hit_rate"`
	LastRequestTime      time.Time     `json:"last_request_time"`
}
