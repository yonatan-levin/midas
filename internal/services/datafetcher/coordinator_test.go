package datafetcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDataCoordinator_CoordinateFetch tests the coordination functionality directly
func TestDataCoordinator_CoordinateFetch(t *testing.T) {
	tests := []struct {
		name           string
		request        *entities.FetchRequest
		setupMocks     func(*mockSECGateway, *mockMarketDataGateway, *mockMacroDataGateway)
		expectError    bool
		expectSources  int
		expectErrors   int
		expectWarnings int
	}{
		{
			name: "successful_concurrent_coordination",
			request: &entities.FetchRequest{
				Ticker:      "COORD_TEST",
				CIK:         "1234567890",
				DataSources: []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
			},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway) {
				sec.companyFacts = &entities.CompanyFactsResponse{
					CIK:        "1234567890",
					EntityName: "Coordination Test Corp",
					Facts: map[string]interface{}{
						"Assets": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   2000000000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
						"Revenues": map[string]interface{}{
							"units": map[string]interface{}{
								"USD": []interface{}{
									map[string]interface{}{
										"val":   1500000000,
										"fy":    2023,
										"form":  "10-K",
										"end":   "2023-09-30",
										"frame": "CY2023Q3",
									},
								},
							},
						},
					},
				}
				market.marketData = &entities.MarketData{
					Ticker:            "COORD_TEST",
					SharePrice:        150.0,
					MarketCap:         5000000000,
					SharesOutstanding: 33333333,
					Beta:              1.2,
				}
				macro.macroData = &entities.MacroData{
					RiskFreeRate:      0.045,
					MarketRiskPremium: 0.055,
					AsOf:              time.Now(),
					Source:            "coordinator_test",
				}
			},
			expectError:    false,
			expectSources:  3,
			expectErrors:   0,
			expectWarnings: 0,
		},
		{
			name: "partial_failure_coordination",
			request: &entities.FetchRequest{
				Ticker:      "PARTIAL_FAIL",
				DataSources: []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
			},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway) {
				sec.err = errors.New("SEC service temporarily unavailable")
				market.marketData = &entities.MarketData{
					Ticker:     "PARTIAL_FAIL",
					SharePrice: 95.0,
					Beta:       0.9,
				}
				macro.macroData = &entities.MacroData{
					RiskFreeRate:      0.04,
					MarketRiskPremium: 0.05,
				}
			},
			expectError:    false, // Coordination should not error, but collect the errors
			expectSources:  3,
			expectErrors:   1, // SEC error should be collected
			expectWarnings: 0,
		},
		{
			name: "context_cancellation_handling",
			request: &entities.FetchRequest{
				Ticker:      "CANCELLED",
				DataSources: []entities.DataSource{entities.SECSource, entities.MarketSource},
			},
			setupMocks: func(sec *mockSECGateway, market *mockMarketDataGateway, macro *mockMacroDataGateway) {
				// Setup will be cancelled before completion
				sec.companyFacts = &entities.CompanyFactsResponse{
					CIK:        "CANCELLED",
					EntityName: "Cancelled Test",
					Facts:      map[string]interface{}{},
				}
			},
			expectError:    false, // Should handle cancellation gracefully
			expectSources:  2,
			expectErrors:   2, // Both sources should error due to cancellation
			expectWarnings: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			secGateway := &mockSECGateway{}
			marketGateway := &mockMarketDataGateway{}
			macroGateway := &mockMacroDataGateway{}

			if tt.setupMocks != nil {
				tt.setupMocks(secGateway, marketGateway, macroGateway)
			}

			config := &DataFetcherConfig{
				ConcurrentFetching: true,
				MaxRetries:         3,
				TimeoutDuration:    30 * time.Second,
			}

			coordinator := NewDataCoordinator(config, secGateway, marketGateway, macroGateway)

			ctx := context.Background()
			if tt.name == "context_cancellation_handling" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1*time.Millisecond)
				defer cancel()
			}

			start := time.Now()

			// Act
			result, err := coordinator.CoordinateFetch(ctx, tt.request)

			// Assert
			duration := time.Since(start)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Equal(t, tt.expectSources, len(result.SourceMetadata))
			assert.Equal(t, tt.expectErrors, len(result.Errors))
			assert.Equal(t, tt.expectWarnings, len(result.Warnings))

			// Verify source metadata is populated
			for _, source := range tt.request.DataSources {
				metadata, exists := result.SourceMetadata[source]
				assert.True(t, exists, "Source metadata should exist for %s", source)
				assert.Greater(t, metadata.Duration, time.Duration(0), "Duration should be tracked for %s", source)
				assert.NotZero(t, metadata.StatusCode, "Status code should be set for %s", source)
			}

			// Verify concurrent execution is faster than sequential
			if len(tt.request.DataSources) > 1 && tt.name != "context_cancellation_handling" {
				maxSequentialTime := time.Duration(len(tt.request.DataSources)) * 50 * time.Millisecond
				assert.True(t, duration < maxSequentialTime,
					"Concurrent coordination should be faster than sequential: %v vs max %v", duration, maxSequentialTime)
			}

			// Reset mock state
			secGateway.err = nil
			marketGateway.err = nil
			macroGateway.err = nil
		})
	}
}

// TestDataCoordinator_SequentialVsConcurrent tests coordination mode differences
func TestDataCoordinator_SequentialVsConcurrent(t *testing.T) {
	// Setup
	secGateway := &mockSECGateway{
		companyFacts: &entities.CompanyFactsResponse{
			CIK:        "MODE_TEST",
			EntityName: "Mode Test Corp",
			Facts:      map[string]interface{}{},
		},
	}
	marketGateway := &mockMarketDataGateway{
		marketData: &entities.MarketData{
			Ticker:     "MODE_TEST",
			SharePrice: 100.0,
			Beta:       1.0,
		},
	}
	macroGateway := &mockMacroDataGateway{
		macroData: &entities.MacroData{
			RiskFreeRate:      0.045,
			MarketRiskPremium: 0.05,
		},
	}

	request := &entities.FetchRequest{
		Ticker:      "MODE_TEST",
		DataSources: []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
	}

	// Test concurrent mode
	t.Run("concurrent_mode", func(t *testing.T) {
		config := &DataFetcherConfig{
			ConcurrentFetching: true,
			MaxRetries:         3,
			TimeoutDuration:    30 * time.Second,
		}

		coordinator := NewDataCoordinator(config, secGateway, marketGateway, macroGateway)
		ctx := context.Background()

		start := time.Now()
		result, err := coordinator.CoordinateFetch(ctx, request)
		concurrentDuration := time.Since(start)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 3, len(result.SourceMetadata))

		// Store concurrent duration for comparison
		require.True(t, concurrentDuration > 0)
	})

	// Test sequential mode
	t.Run("sequential_mode", func(t *testing.T) {
		config := &DataFetcherConfig{
			ConcurrentFetching: false,
			MaxRetries:         3,
			TimeoutDuration:    30 * time.Second,
		}

		coordinator := NewDataCoordinator(config, secGateway, marketGateway, macroGateway)
		ctx := context.Background()

		start := time.Now()
		result, err := coordinator.CoordinateFetch(ctx, request)
		sequentialDuration := time.Since(start)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 3, len(result.SourceMetadata))

		// Sequential should generally take longer (though with mocks it might not be significant)
		require.True(t, sequentialDuration > 0)
	})
}

// TestDataCoordinator_ErrorAggregation tests error collection and aggregation
func TestDataCoordinator_ErrorAggregation(t *testing.T) {
	secGateway := &mockSECGateway{
		err: errors.New("SEC rate limit exceeded"),
	}
	marketGateway := &mockMarketDataGateway{
		err: errors.New("Market data provider down"),
	}
	macroGateway := &mockMacroDataGateway{
		err: errors.New("Treasury API unavailable"),
	}

	config := &DataFetcherConfig{
		ConcurrentFetching: true,
		MaxRetries:         1, // Fail fast for this test
		TimeoutDuration:    5 * time.Second,
	}

	coordinator := NewDataCoordinator(config, secGateway, marketGateway, macroGateway)
	ctx := context.Background()

	request := &entities.FetchRequest{
		Ticker:      "ERROR_TEST",
		DataSources: []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
	}

	result, err := coordinator.CoordinateFetch(ctx, request)

	assert.NoError(t, err) // Coordination itself should not error
	assert.NotNil(t, result)
	assert.Equal(t, 3, len(result.SourceMetadata)) // All sources attempted
	assert.Equal(t, 3, len(result.Errors))         // All errors collected

	// Verify error details
	errorMessages := make(map[entities.DataSource]string)
	for _, fetchError := range result.Errors {
		errorMessages[fetchError.Source] = fetchError.Message
	}

	assert.Contains(t, errorMessages[entities.SECSource], "SEC rate limit exceeded")
	assert.Contains(t, errorMessages[entities.MarketSource], "Market data provider down")
	assert.Contains(t, errorMessages[entities.MacroSource], "Treasury API unavailable")

	// Verify no data was fetched due to errors
	assert.Nil(t, result.FinancialData)
	assert.Nil(t, result.MarketData)
	assert.Nil(t, result.MacroData)
}

// TestDataCoordinator_NILRequest tests error handling for invalid requests
func TestDataCoordinator_NILRequest(t *testing.T) {
	config := &DataFetcherConfig{}
	coordinator := NewDataCoordinator(config, nil, nil, nil)

	result, err := coordinator.CoordinateFetch(context.Background(), nil)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "fetch request cannot be nil")
}

// TestDataCoordinator_DefaultDataSources tests default source selection
func TestDataCoordinator_DefaultDataSources(t *testing.T) {
	secGateway := &mockSECGateway{
		companyFacts: &entities.CompanyFactsResponse{
			CIK:        "DEFAULT_TEST",
			EntityName: "Default Test Corp",
			Facts:      map[string]interface{}{},
		},
	}
	marketGateway := &mockMarketDataGateway{
		marketData: &entities.MarketData{
			Ticker:     "DEFAULT_TEST",
			SharePrice: 75.0,
			Beta:       0.8,
		},
	}
	macroGateway := &mockMacroDataGateway{
		macroData: &entities.MacroData{
			RiskFreeRate:      0.03,
			MarketRiskPremium: 0.055,
		},
	}

	config := &DataFetcherConfig{
		ConcurrentFetching: true,
		MaxRetries:         3,
		TimeoutDuration:    30 * time.Second,
	}

	coordinator := NewDataCoordinator(config, secGateway, marketGateway, macroGateway)
	ctx := context.Background()

	request := &entities.FetchRequest{
		Ticker:      "DEFAULT_TEST",
		DataSources: []entities.DataSource{}, // Empty - should default to all sources
	}

	result, err := coordinator.CoordinateFetch(ctx, request)

	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should have fetched from all three default sources
	assert.Equal(t, 3, len(result.SourceMetadata))

	expectedSources := []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource}
	for _, source := range expectedSources {
		_, exists := result.SourceMetadata[source]
		assert.True(t, exists, "Should have metadata for default source %s", source)
	}

	// Should have data from all sources
	assert.NotNil(t, result.FinancialData)
	assert.NotNil(t, result.MarketData)
	assert.NotNil(t, result.MacroData)
}
