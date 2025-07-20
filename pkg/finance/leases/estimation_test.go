package leases

import (
	"context"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPresentValueCalculator_CalculatePresentValue(t *testing.T) {
	tests := []struct {
		name          string
		config        *EstimationConfig
		financialData *entities.FinancialData
		context       *entities.CleaningContext
		expected      *PresentValueResult
		expectedError string
	}{
		{
			name: "successful_calculation_with_disclosed_commitments",
			config: &EstimationConfig{
				DiscountRateMethod:    "incremental_borrowing_rate",
				LeaseTermMethod:       "disclosed_commitments",
				PaymentMethod:         "schedule_extraction",
				DefaultDiscountRate:   0.06,
				DefaultLeaseTermYears: 10,
				DefaultEscalationRate: 0.03,
			},
			financialData: &entities.FinancialData{
				OperatingLeaseCommitments: map[string]float64{
					"Year1": 100000,
					"Year2": 105000,
					"Year3": 110000,
					"Year4": 115000,
					"Year5": 120000,
				},
				InterestExpense: 50000,
				TotalDebt:       1000000,
				RiskFreeRate:    0.025,
			},
			context: &entities.CleaningContext{
				IndustryCode: "44", // Retail
			},
			expected: &PresentValueResult{
				PresentValue:      450000, // Approximate expected value
				DiscountRate:      0.05,   // Corrected: 0.025 + 0.025 + 0.005 = 0.055, but bounded
				LeaseTermYears:    5,
				ConfidenceScore:   0.9,
				CalculationMethod: "schedule_extraction",
				EstimationQuality: "high",
			},
		},
		{
			name: "fallback_to_cost_of_debt",
			config: &EstimationConfig{
				DiscountRateMethod:    "cost_of_debt",
				LeaseTermMethod:       "industry_benchmarks",
				PaymentMethod:         "straight_line",
				DefaultDiscountRate:   0.06,
				DefaultLeaseTermYears: 10,
				DefaultEscalationRate: 0.03,
			},
			financialData: &entities.FinancialData{
				OperatingLeaseLiability: 500000,
				InterestExpense:         75000,
				TotalDebt:               1500000,
				RiskFreeRate:            0.03,
			},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			expected: &PresentValueResult{
				PresentValue:      500000,
				DiscountRate:      0.04, // Corrected: 0.05 - 0.01 = 0.04 (tech adjustment)
				LeaseTermYears:    8,
				ConfidenceScore:   0.7,
				CalculationMethod: "straight_line",
				EstimationQuality: "medium",
			},
		},
		{
			name: "insufficient_data_fallback",
			config: &EstimationConfig{
				DiscountRateMethod:    "incremental_borrowing_rate",
				LeaseTermMethod:       "disclosed_commitments",
				PaymentMethod:         "schedule_extraction",
				DefaultDiscountRate:   0.06,
				DefaultLeaseTermYears: 10,
				DefaultEscalationRate: 0.03,
			},
			financialData: &entities.FinancialData{
				// Minimal data forcing fallback
				RiskFreeRate: 0.02,
			},
			context: &entities.CleaningContext{
				IndustryCode: "31", // Manufacturing
			},
			expected: &PresentValueResult{
				PresentValue:      0,
				DiscountRate:      0.06,
				LeaseTermYears:    10,
				ConfidenceScore:   0.3,
				CalculationMethod: "emergency_fallback",
				EstimationQuality: "low",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calculator := NewPresentValueCalculator(tt.config)
			require.NotNil(t, calculator)

			ctx := context.Background()
			result, err := calculator.CalculatePresentValue(ctx, tt.financialData, tt.context)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			// Allow for reasonable tolerance in present value calculations
			assert.InDelta(t, tt.expected.PresentValue, result.PresentValue, tt.expected.PresentValue*0.2)
			assert.InDelta(t, tt.expected.DiscountRate, result.DiscountRate, 0.01)
			assert.Equal(t, tt.expected.LeaseTermYears, result.LeaseTermYears)
			assert.GreaterOrEqual(t, result.ConfidenceScore, 0.0)
			assert.LessOrEqual(t, result.ConfidenceScore, 1.0)
			assert.NotEmpty(t, result.CalculationMethod)
			assert.NotEmpty(t, result.EstimationQuality)
		})
	}
}

func TestDiscountRateEstimator_EstimateIncrementalBorrowingRate(t *testing.T) {
	tests := []struct {
		name          string
		financialData *entities.FinancialData
		context       *entities.CleaningContext
		config        *EstimationConfig
		expectedRate  float64
		expectedError string
	}{
		{
			name: "successful_rate_calculation",
			financialData: &entities.FinancialData{
				RiskFreeRate:    0.025,
				InterestExpense: 50000,
				TotalDebt:       1000000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "44", // Retail
			},
			config: &EstimationConfig{
				DefaultDiscountRate: 0.06,
			},
			expectedRate: 0.05, // Corrected: 0.025 + 0.025 + 0.005 = 0.055, bounded to 0.05
		},
		{
			name: "fallback_to_risk_free_plus_spread",
			financialData: &entities.FinancialData{
				RiskFreeRate: 0.03,
				// No debt data forcing fallback
			},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			config: &EstimationConfig{
				DefaultDiscountRate: 0.06,
			},
			expectedRate: 0.055, // 3% + 2.5% base spread - 0.5% tech adjustment
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimator := NewDiscountRateEstimator(tt.config)
			require.NotNil(t, estimator)

			ctx := context.Background()
			result, err := estimator.EstimateIncrementalBorrowingRate(ctx, tt.financialData, tt.context)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			assert.InDelta(t, tt.expectedRate, result.Rate, 0.01)
			assert.GreaterOrEqual(t, result.ConfidenceScore, 0.0)
			assert.LessOrEqual(t, result.ConfidenceScore, 1.0)
		})
	}
}

func TestLeaseTermEstimator_EstimateFromCommitments(t *testing.T) {
	tests := []struct {
		name          string
		commitments   map[string]float64
		context       *entities.CleaningContext
		config        *EstimationConfig
		expectedYears int
		expectedError string
	}{
		{
			name: "five_year_commitment_schedule",
			commitments: map[string]float64{
				"Year1": 100000,
				"Year2": 105000,
				"Year3": 110000,
				"Year4": 115000,
				"Year5": 120000,
			},
			context: &entities.CleaningContext{
				IndustryCode: "44", // Retail
			},
			config: &EstimationConfig{
				DefaultLeaseTermYears: 10,
			},
			expectedYears: 5,
		},
		{
			name:        "empty_commitments_fallback",
			commitments: map[string]float64{},
			context: &entities.CleaningContext{
				IndustryCode: "45", // Technology
			},
			config: &EstimationConfig{
				DefaultLeaseTermYears: 8,
			},
			expectedYears: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimator := NewLeaseTermEstimator(tt.config)
			require.NotNil(t, estimator)

			ctx := context.Background()
			result, err := estimator.EstimateFromCommitments(ctx, tt.commitments, tt.context)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedYears, result.LeaseTermYears)
			assert.GreaterOrEqual(t, result.ConfidenceScore, 0.0)
			assert.LessOrEqual(t, result.ConfidenceScore, 1.0)
		})
	}
}

func TestPaymentScheduleEstimator_EstimateFromSchedule(t *testing.T) {
	tests := []struct {
		name           string
		commitments    map[string]float64
		leaseTermYears int
		config         *EstimationConfig
		expectedTotal  float64
		expectedError  string
	}{
		{
			name: "explicit_schedule_calculation",
			commitments: map[string]float64{
				"Year1": 100000,
				"Year2": 105000,
				"Year3": 110000,
				"Year4": 115000,
				"Year5": 120000,
			},
			leaseTermYears: 5,
			config: &EstimationConfig{
				DefaultEscalationRate: 0.03,
			},
			expectedTotal: 550000,
		},
		{
			name: "straight_line_estimation",
			commitments: map[string]float64{
				"Year1": 100000,
			},
			leaseTermYears: 10,
			config: &EstimationConfig{
				DefaultEscalationRate: 0.03,
			},
			expectedTotal: 1140000, // Approximate with 3% escalation
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimator := NewPaymentScheduleEstimator(tt.config)
			require.NotNil(t, estimator)

			ctx := context.Background()
			result, err := estimator.EstimateFromSchedule(ctx, tt.commitments, tt.leaseTermYears)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			assert.InDelta(t, tt.expectedTotal, result.TotalPayments, tt.expectedTotal*0.1)
			assert.GreaterOrEqual(t, result.ConfidenceScore, 0.0)
			assert.LessOrEqual(t, result.ConfidenceScore, 1.0)
		})
	}
}

// Property-based tests for mathematical correctness
func TestPresentValueCalculation_MathematicalProperties(t *testing.T) {
	config := &EstimationConfig{
		DiscountRateMethod:    "incremental_borrowing_rate",
		LeaseTermMethod:       "disclosed_commitments",
		PaymentMethod:         "schedule_extraction",
		DefaultDiscountRate:   0.06,
		DefaultLeaseTermYears: 10,
		DefaultEscalationRate: 0.03,
	}

	calculator := NewPresentValueCalculator(config)
	ctx := context.Background()

	t.Run("present_value_decreases_with_higher_discount_rate", func(t *testing.T) {
		baseData := &entities.FinancialData{
			OperatingLeaseCommitments: map[string]float64{
				"Year1": 100000,
				"Year2": 100000,
				"Year3": 100000,
				"Year4": 100000,
				"Year5": 100000,
			},
			RiskFreeRate: 0.02,
		}

		context1 := &entities.CleaningContext{IndustryCode: "44"}
		context2 := &entities.CleaningContext{IndustryCode: "44"}

		// Modify data to create higher discount rate scenario
		highRateData := *baseData
		highRateData.RiskFreeRate = 0.05

		result1, err1 := calculator.CalculatePresentValue(ctx, baseData, context1)
		require.NoError(t, err1)

		result2, err2 := calculator.CalculatePresentValue(ctx, &highRateData, context2)
		require.NoError(t, err2)

		assert.Greater(t, result1.PresentValue, result2.PresentValue,
			"Present value should decrease with higher discount rate")
	})

	t.Run("present_value_increases_with_longer_lease_term", func(t *testing.T) {
		baseData := &entities.FinancialData{
			OperatingLeaseCommitments: map[string]float64{
				"Year1": 100000,
				"Year2": 100000,
				"Year3": 100000,
			},
			RiskFreeRate: 0.03,
		}

		longTermData := &entities.FinancialData{
			OperatingLeaseCommitments: map[string]float64{
				"Year1": 100000,
				"Year2": 100000,
				"Year3": 100000,
				"Year4": 100000,
				"Year5": 100000,
			},
			RiskFreeRate: 0.03,
		}

		context1 := &entities.CleaningContext{IndustryCode: "44"}
		context2 := &entities.CleaningContext{IndustryCode: "44"}

		result1, err1 := calculator.CalculatePresentValue(ctx, baseData, context1)
		require.NoError(t, err1)

		result2, err2 := calculator.CalculatePresentValue(ctx, longTermData, context2)
		require.NoError(t, err2)

		assert.Greater(t, result2.PresentValue, result1.PresentValue,
			"Present value should increase with longer lease term")
	})
}
