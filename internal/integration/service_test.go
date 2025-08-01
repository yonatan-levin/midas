package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
)

// TestE2E_FairValue_SingleTicker tests the complete flow for a single ticker
// This is a failing test that will drive our implementation (TDD)
func TestE2E_FairValue_SingleTicker(t *testing.T) {
	// Step 1: Setup real test environment with testcontainers
	testEnv := SetupTestEnvironment(t)
	defer testEnv.Cleanup()

	tests := []struct {
		name           string
		ticker         string
		expectedCode   int
		expectDCF      bool
		expectTangible bool
	}{
		{
			name:           "AAPL - Valid ticker should return positive DCF",
			ticker:         "AAPL",
			expectedCode:   http.StatusOK,
			expectDCF:      true,
			expectTangible: true,
		},
		{
			name:         "INVALID - Invalid ticker should return error",
			ticker:       "INVALID123",
			expectedCode: http.StatusBadRequest,
			expectDCF:    false,
		},
		{
			name:         "Empty ticker should return validation error",
			ticker:       "",
			expectedCode: http.StatusBadRequest,
			expectDCF:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Step 2: Make request to endpoint using real router with DI
			w := httptest.NewRecorder()
			req, err := http.NewRequest("GET", fmt.Sprintf("/api/v1/fair-value/%s", tt.ticker), nil)
			require.NoError(t, err)

			// Step 3: Execute request through real infrastructure
			testEnv.Router.ServeHTTP(w, req)

			// Step 4: Assert response code - this will drive implementation
			// Initially expected to fail and guide development
			assert.Equal(t, tt.expectedCode, w.Code, "HTTP status code should match expected")

			if tt.expectedCode == http.StatusOK {
				// Step 5: Parse response and validate structure
				var response handlers.FairValueResponse
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err, "Response should be valid JSON")

				// Step 6: Assert core business logic requirements
				assert.Equal(t, tt.ticker, response.Ticker, "Ticker should match request")
				assert.NotEmpty(t, response.AsOf, "AsOf timestamp should be populated")

				if tt.expectDCF {
					assert.Greater(t, response.DCFValuePerShare, 0.0, "DCF value should be positive for valid tickers")
					assert.Greater(t, response.WACC, 0.0, "WACC should be positive")
					assert.NotZero(t, response.GrowthRate, "Growth rate should be calculated")
				}

				if tt.expectTangible {
					// Tangible value can be negative (more debt than assets), but should be calculated
					assert.NotZero(t, response.TangibleValuePerShare, "Tangible value should be calculated")
				}

				// TODO: Add golden master validation
				// Compare against testdata/AAPL_golden_master.json for regression testing
			}
		})
	}
}

// TestE2E_FairValue_BulkRequest tests the bulk endpoint with multiple tickers
func TestE2E_FairValue_BulkRequest(t *testing.T) {
	// Step 1: Setup test data
	bulkRequest := handlers.BulkFairValueRequest{
		Tickers: []string{"AAPL", "MSFT", "GOOGL"},
	}

	requestBody, err := json.Marshal(bulkRequest)
	require.NoError(t, err)

	// Step 2: Setup test environment
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// TODO: Wire real DI container here
	var fairValueHandler *handlers.FairValueHandler = nil

	v1 := router.Group("/api/v1")
	v1.POST("/fair-value/bulk", fairValueHandler.GetBulkFairValue)

	// Step 3: Make bulk request
	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", "/api/v1/fair-value/bulk", bytes.NewBuffer(requestBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	// Step 4: Execute request
	router.ServeHTTP(w, req)

	// Step 5: This should initially fail - driving TDD implementation
	assert.Equal(t, http.StatusOK, w.Code, "Bulk request should succeed")

	if w.Code == http.StatusOK {
		var response handlers.BulkFairValueResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		// Step 6: Validate bulk response structure
		assert.Len(t, response.Results, 3, "Should return results for all 3 tickers")
		assert.Equal(t, 3, response.Summary.TotalRequested, "Summary should show 3 requested")

		// Step 7: Validate individual ticker results
		for _, result := range response.Results {
			assert.Contains(t, []string{"AAPL", "MSFT", "GOOGL"}, result.Ticker)
			assert.Greater(t, result.DCFValuePerShare, 0.0, "Each ticker should have positive DCF")
		}
	}
}

// TestE2E_FairValue_WithOverrides tests the API with beta and risk-free rate overrides
func TestE2E_FairValue_WithOverrides(t *testing.T) {
	tests := []struct {
		name         string
		ticker       string
		overrideBeta *float64
		overrideRF   *float64
		expectedCode int
	}{
		{
			name:         "AAPL with beta override",
			ticker:       "AAPL",
			overrideBeta: func() *float64 { v := 1.5; return &v }(),
			expectedCode: http.StatusOK,
		},
		{
			name:         "AAPL with risk-free rate override",
			ticker:       "AAPL",
			overrideRF:   func() *float64 { v := 0.03; return &v }(),
			expectedCode: http.StatusOK,
		},
		{
			name:         "AAPL with both overrides",
			ticker:       "AAPL",
			overrideBeta: func() *float64 { v := 1.2; return &v }(),
			overrideRF:   func() *float64 { v := 0.025; return &v }(),
			expectedCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TODO: Implement parameter override testing
			// This test should drive implementation of query parameter handling

			gin.SetMode(gin.TestMode)
			router := gin.New()

			// TODO: Wire real DI container
			var fairValueHandler *handlers.FairValueHandler = nil

			v1 := router.Group("/api/v1")
			v1.GET("/fair-value/:ticker", fairValueHandler.GetFairValue)

			// Build query parameters
			queryParams := ""
			if tt.overrideBeta != nil {
				queryParams += fmt.Sprintf("override_beta=%.3f", *tt.overrideBeta)
			}
			if tt.overrideRF != nil {
				if queryParams != "" {
					queryParams += "&"
				}
				queryParams += fmt.Sprintf("override_rf=%.3f", *tt.overrideRF)
			}

			url := fmt.Sprintf("/api/v1/fair-value/%s", tt.ticker)
			if queryParams != "" {
				url += "?" + queryParams
			}

			w := httptest.NewRecorder()
			req, err := http.NewRequest("GET", url, nil)
			require.NoError(t, err)

			router.ServeHTTP(w, req)

			// This will fail initially, driving implementation
			assert.Equal(t, tt.expectedCode, w.Code)

			if w.Code == http.StatusOK {
				var response handlers.FairValueResponse
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)

				// TODO: Validate that overrides were actually applied to WACC calculation
				assert.Greater(t, response.DCFValuePerShare, 0.0)
			}
		})
	}
}

// TestE2E_FairValue_GoldenMasterRegression tests against known good outputs
func TestE2E_FairValue_GoldenMasterRegression(t *testing.T) {
	// TODO: Implement golden master testing for regression prevention
	// This should load testdata/AAPL_golden_master.json and compare outputs

	t.Skip("TODO: Implement golden master testing - Step 3 of Task 2.5.2")

	// Expected structure:
	// 1. Load golden master data from testdata/
	// 2. Run API call with same inputs
	// 3. Compare outputs (allowing for small tolerances due to data updates)
	// 4. Fail if significant deviation detected
}

// TestE2E_FairValue_ErrorHandling tests error scenarios and RFC 7807 compliance
func TestE2E_FairValue_ErrorHandling(t *testing.T) {
	// TODO: Test error scenarios as per project rules (RFC 7807 error format)

	t.Skip("TODO: Implement error handling tests - part of Step 4")

	// Test cases to implement:
	// 1. Missing ticker
	// 2. Invalid ticker format
	// 3. External API failures (SEC, market data)
	// 4. Database connection issues
	// 5. Invalid override parameters
}

func TestE2E_CompleteServicePipeline_AAPL(t *testing.T) {
	// Setup test environment with mock SEC server
	mockSECServer := SetupMockSECServer(t)
	defer mockSECServer.Close()

	// Setup test container with mock SEC URL
	tc := SetupTestEnvironmentWithMockSEC(t, mockSECServer.URL)
	defer tc.Cleanup()

	// Test the COMPLETE service pipeline flow:
	// 1. HTTP request to our API
	// 2. DataFetcher.Fetch() -> calls DataCoordinator
	// 3. DataCoordinator -> calls SEC Gateway
	// 4. SEC Gateway -> calls SEC Client (gets mocked response)
	// 5. SEC Parser parses the JSON
	// 6. DataCleaner.CleanFinancialData() processes it
	// 7. Valuation service calculates DCF
	// 8. Response returned

	// Make real HTTP request to our API
	req := httptest.NewRequest("GET", "/api/v1/fair-value/AAPL", nil)
	w := httptest.NewRecorder()
	tc.Router.ServeHTTP(w, req)

	// Verify complete pipeline worked
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify the real services processed real data
	assert.Contains(t, response, "dcf_value_per_share")
	assert.Contains(t, response, "tangible_value_per_share")
	assert.Greater(t, response["dcf_value_per_share"].(float64), 0.0)

	t.Logf("✅ COMPLETE SERVICE PIPELINE TEST PASSED - DCF: $%.2f",
		response["dcf_value_per_share"].(float64))
}

// TestE2E_CompleteServicePipeline_RealFlow tests the complete service pipeline
// using real services and mock SEC data for AAPL
func TestE2E_CompleteServicePipeline_RealFlow(t *testing.T) {
	// Step 1: Setup mock SEC server with real Apple data
	mockSECServer := SetupMockSECServer(t)
	defer mockSECServer.Close()

	// Step 2: Setup test environment with mock SEC URL
	tc := SetupTestEnvironmentWithMockSEC(t, mockSECServer.URL)
	defer tc.Cleanup()

	t.Log("🧪 Testing complete service pipeline flow...")
	t.Logf("Mock SEC server: %s", mockSECServer.URL)

	// Step 3: Test AAPL with mock SEC data - should flow through complete pipeline:
	// HTTP Request → DataFetcher → SEC Gateway → SEC Client (mock) → SEC Parser →
	// DataCleaner → Valuation Service → DCF Calculation → HTTP Response
	t.Run("AAPL_Complete_Pipeline", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/fair-value/AAPL", nil)
		w := httptest.NewRecorder()
		tc.Router.ServeHTTP(w, req)

		// Verify HTTP response
		assert.Equal(t, http.StatusOK, w.Code, "Expected successful response")

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err, "Failed to parse response JSON")

		// Verify response structure (API contract)
		assert.Contains(t, response, "ticker", "Response should contain ticker")
		assert.Contains(t, response, "dcf_value_per_share", "Response should contain DCF value")
		assert.Contains(t, response, "tangible_value_per_share", "Response should contain tangible value")
		assert.Contains(t, response, "wacc", "Response should contain WACC")
		assert.Contains(t, response, "growth_rate", "Response should contain growth rate")
		assert.Contains(t, response, "as_of", "Response should contain as_of timestamp")

		// Verify business logic - DCF should be positive and reasonable
		dcfValue := response["dcf_value_per_share"].(float64)
		tangibleValue := response["tangible_value_per_share"].(float64)
		wacc := response["wacc"].(float64)
		growthRate := response["growth_rate"].(float64)

		assert.Greater(t, dcfValue, 0.0, "DCF value should be positive")
		assert.Less(t, dcfValue, 1000.0, "DCF value should be reasonable (< $1000)")
		assert.Greater(t, tangibleValue, 0.0, "Tangible value should be positive")
		assert.Greater(t, wacc, 0.0, "WACC should be positive")
		assert.Greater(t, wacc, 0.01, "WACC should be > 1%")
		assert.Less(t, wacc, 0.50, "WACC should be < 50%")
		assert.Greater(t, growthRate, -0.20, "Growth rate should be > -20%")
		assert.Less(t, growthRate, 1.0, "Growth rate should be < 100%")

		// Verify data quality (if included)
		if dataQuality, exists := response["data_quality_score"]; exists {
			qualityScore := dataQuality.(float64)
			assert.GreaterOrEqual(t, qualityScore, 50.0, "Data quality should be >= 50")
		}

		t.Logf("✅ COMPLETE PIPELINE SUCCESS:")
		t.Logf("   Ticker: %s", response["ticker"])
		t.Logf("   DCF Value: $%.2f", dcfValue)
		t.Logf("   Tangible Value: $%.2f", tangibleValue)
		t.Logf("   WACC: %.2f%%", wacc*100)
		t.Logf("   Growth Rate: %.2f%%", growthRate*100)
		if dataQuality, exists := response["data_quality_score"]; exists {
			t.Logf("   Data Quality: %.1f", dataQuality)
		}
	})

	// Step 4: Test real SEC API fallback with MSFT
	t.Run("MSFT_Real_SEC_API_Fallback", func(t *testing.T) {
		// This should hit the real SEC API since mock only serves AAPL
		req := httptest.NewRequest("GET", "/api/v1/fair-value/MSFT", nil)
		w := httptest.NewRecorder()
		tc.Router.ServeHTTP(w, req)

		// MSFT might fail with real SEC API due to rate limits or network issues
		// So we accept both success and specific failure cases
		if w.Code == http.StatusOK {
			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, "MSFT", response["ticker"])
			assert.Greater(t, response["dcf_value_per_share"].(float64), 0.0)
			t.Logf("✅ MSFT Real SEC API Success: DCF $%.2f", response["dcf_value_per_share"].(float64))
		} else {
			// Log the failure for debugging but don't fail test
			t.Logf("ℹ️ MSFT Real SEC API failed (expected in CI): %d - %s", w.Code, w.Body.String())
		}
	})

	// Step 5: Test error handling
	t.Run("Invalid_Ticker_Error_Handling", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/fair-value/INVALID123", nil)
		w := httptest.NewRecorder()
		tc.Router.ServeHTTP(w, req)

		// Should return error for invalid ticker
		assert.NotEqual(t, http.StatusOK, w.Code, "Invalid ticker should not return success")

		if w.Code != http.StatusOK {
			t.Logf("✅ Invalid ticker error handling: %d - %s", w.Code, w.Body.String())
		}
	})
}
