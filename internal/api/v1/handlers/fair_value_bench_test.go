package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// BenchmarkFairValueResponse_JSONMarshal benchmarks JSON marshaling performance
func BenchmarkFairValueResponse_JSONMarshal(b *testing.B) {
	response := FairValueResponse{
		Ticker:                "AAPL",
		WACC:                  0.092,
		GrowthRate:            0.045,
		TangibleValuePerShare: 24.73,
		DCFValuePerShare:      156.42,
		AsOf:                  time.Now().Format("2006-01-02T15:04:05Z"),
		DataQualityScore:      90.5,
		DataQualityGrade:      "A",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(response)
		if err != nil {
			b.Fatalf("Failed to marshal response: %v", err)
		}
	}
}

// BenchmarkBulkFairValueRequest_JSONUnmarshal benchmarks JSON unmarshaling performance
func BenchmarkBulkFairValueRequest_JSONUnmarshal(b *testing.B) {
	requestJSON := `{
		"tickers": ["AAPL", "MSFT", "GOOGL", "AMZN", "TSLA"],
		"override_beta": 1.2,
		"override_rf": 0.045
	}`

	requestBytes := []byte(requestJSON)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var request BulkFairValueRequest
		err := json.Unmarshal(requestBytes, &request)
		if err != nil {
			b.Fatalf("Failed to unmarshal request: %v", err)
		}

		if len(request.Tickers) != 5 {
			b.Fatalf("Expected 5 tickers, got %d", len(request.Tickers))
		}
	}
}

// BenchmarkTickerValidation benchmarks ticker validation performance
func BenchmarkTickerValidation(b *testing.B) {
	tickers := []string{"AAPL", "MSFT", "GOOGL", "AMZN", "TSLA", "META", "NVDA", "ORCL", "CRM", "ADBE"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ticker := tickers[i%len(tickers)]
		valid := isValidTicker(ticker)
		if !valid {
			b.Fatalf("Expected ticker %s to be valid", ticker)
		}
	}
}

// BenchmarkTickerValidation_InvalidCases benchmarks validation with invalid tickers
func BenchmarkTickerValidation_InvalidCases(b *testing.B) {
	invalidTickers := []string{"", "TOOLONG", "123456", "invalid-ticker", "ticker@"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ticker := invalidTickers[i%len(invalidTickers)]
		valid := isValidTicker(ticker)
		if valid {
			b.Fatalf("Expected ticker %s to be invalid", ticker)
		}
	}
}

// BenchmarkHTTPRequest_Creation benchmarks HTTP request creation overhead
func BenchmarkHTTPRequest_Creation(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/fair-value/AAPL", nil)
		req.Header.Set("X-API-Key", "test-api-key")
		req.Header.Set("Content-Type", "application/json")

		// Verify request is properly created
		if req.Method != http.MethodGet {
			b.Fatalf("Expected GET method, got %s", req.Method)
		}
	}
}

// BenchmarkGinContext_Creation benchmarks Gin context creation
func BenchmarkGinContext_Creation(b *testing.B) {
	gin.SetMode(gin.TestMode)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		// Set common values that would be set by middleware
		c.Set("request_id", "test-request-id")
		c.Header("X-Request-ID", "test-request-id")

		// Verify context is properly created
		if c == nil {
			b.Fatal("Failed to create Gin context")
		}
	}
}

// BenchmarkErrorResponse_Creation benchmarks error response creation
func BenchmarkErrorResponse_Creation(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		errorResponse := ErrorResponse{
			Type:     "https://problems.midas.dev/INVALID_TICKER",
			Title:    "Bad Request",
			Status:   http.StatusBadRequest,
			Detail:   "Invalid ticker format",
			Instance: "/api/v1/fair-value/INVALID",
			Context: map[string]interface{}{
				"ticker": "INVALID",
			},
		}

		// Marshal to JSON to measure complete response creation
		_, err := json.Marshal(errorResponse)
		if err != nil {
			b.Fatalf("Failed to marshal error response: %v", err)
		}
	}
}

// BenchmarkStringOperations_TickerProcessing benchmarks string operations on tickers
func BenchmarkStringOperations_TickerProcessing(b *testing.B) {
	tickers := []string{"aapl", "msft", "googl", "amzn", "tsla"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ticker := tickers[i%len(tickers)]

		// Simulate the ticker processing operations
		upperTicker := strings.ToUpper(ticker)
		valid := isValidTicker(upperTicker)

		if !valid {
			b.Fatalf("Expected ticker %s to be valid after processing", upperTicker)
		}
	}
}

// BenchmarkMemoryAllocation_ResponseStructs benchmarks memory allocation for response structs
func BenchmarkMemoryAllocation_ResponseStructs(b *testing.B) {
	// Force GC before measurement
	runtime.GC()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		responses := make([]FairValueResponse, 5)

		for j := 0; j < 5; j++ {
			responses[j] = FairValueResponse{
				Ticker:                "AAPL",
				WACC:                  0.092,
				GrowthRate:            0.045,
				TangibleValuePerShare: 24.73,
				DCFValuePerShare:      156.42,
				AsOf:                  time.Now().Format("2006-01-02T15:04:05Z"),
				DataQualityScore:      90.5,
				DataQualityGrade:      "A",
			}
		}

		// Create bulk response
		bulkResponse := BulkFairValueResponse{
			Results: responses,
			Summary: BulkSummary{
				TotalRequested: 5,
				Successful:     5,
				Failed:         0,
			},
		}

		// Marshal to simulate real usage
		_, err := json.Marshal(bulkResponse)
		if err != nil {
			b.Fatalf("Failed to marshal bulk response: %v", err)
		}

		// Force GC every 10 iterations to measure peak memory usage
		if i%10 == 0 {
			runtime.GC()
		}
	}
}

// BenchmarkJSONProcessing_BulkRequest benchmarks complete JSON processing pipeline
func BenchmarkJSONProcessing_BulkRequest(b *testing.B) {
	requestJSON := `{
		"tickers": ["AAPL", "MSFT", "GOOGL", "AMZN", "TSLA", "META", "NVDA", "ORCL", "CRM", "ADBE"]
	}`

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Unmarshal request
		var request BulkFairValueRequest
		err := json.Unmarshal([]byte(requestJSON), &request)
		if err != nil {
			b.Fatalf("Failed to unmarshal request: %v", err)
		}

		// Process tickers (simulate validation and processing)
		results := make([]FairValueResponse, 0, len(request.Tickers))
		successful := 0

		for _, ticker := range request.Tickers {
			upperTicker := strings.ToUpper(ticker)
			if isValidTicker(upperTicker) {
				response := FairValueResponse{
					Ticker:                upperTicker,
					WACC:                  0.092,
					GrowthRate:            0.045,
					TangibleValuePerShare: 24.73,
					DCFValuePerShare:      156.42,
					AsOf:                  time.Now().Format("2006-01-02T15:04:05Z"),
					DataQualityScore:      90.5,
					DataQualityGrade:      "A",
				}
				results = append(results, response)
				successful++
			}
		}

		// Create final response
		bulkResponse := BulkFairValueResponse{
			Results: results,
			Summary: BulkSummary{
				TotalRequested: len(request.Tickers),
				Successful:     successful,
				Failed:         len(request.Tickers) - successful,
			},
		}

		// Marshal final response
		_, err = json.Marshal(bulkResponse)
		if err != nil {
			b.Fatalf("Failed to marshal bulk response: %v", err)
		}
	}
}

// BenchmarkConcurrent_JSONProcessing benchmarks concurrent JSON processing
func BenchmarkConcurrent_JSONProcessing(b *testing.B) {
	requestJSON := `{
		"tickers": ["AAPL", "MSFT", "GOOGL", "AMZN", "TSLA"]
	}`

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var request BulkFairValueRequest
			err := json.Unmarshal([]byte(requestJSON), &request)
			if err != nil {
				b.Fatalf("Failed to unmarshal request: %v", err)
			}

			if len(request.Tickers) != 5 {
				b.Fatalf("Expected 5 tickers, got %d", len(request.Tickers))
			}
		}
	})
}
