package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// HTTPResponse holds the response from an HTTP request
type HTTPResponse struct {
	StatusCode int           `json:"status_code"`
	Body       string        `json:"body"`
	Duration   time.Duration `json:"duration"`
	Size       int           `json:"size"`
}

// BenchmarkExecutor executes performance benchmarks against the API
type BenchmarkExecutor struct {
	config     BenchmarkConfig
	httpClient *http.Client
}

// RequestMetrics tracks metrics for a single request
type RequestMetrics struct {
	StatusCode int
	Duration   time.Duration
	Size       int
	Error      error
}

// NewBenchmarkExecutor creates a new benchmark executor
func NewBenchmarkExecutor(config BenchmarkConfig) *BenchmarkExecutor {
	return &BenchmarkExecutor{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

// MakeRequest makes a single HTTP request and measures performance
func (be *BenchmarkExecutor) MakeRequest(ctx context.Context, method, endpoint string, body []byte) (*HTTPResponse, error) {
	url := be.config.BaseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication header
	if be.config.APIKey != "" {
		req.Header.Set("X-API-Key", be.config.APIKey)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Measure request duration
	start := time.Now()
	resp, err := be.httpClient.Do(req)
	duration := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return &HTTPResponse{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		Duration:   duration,
		Size:       len(respBody),
	}, nil
}

// RunScenario executes a complete test scenario and returns results
func (be *BenchmarkExecutor) RunScenario(ctx context.Context, scenario TestScenario) (*BenchmarkResult, error) {
	if err := scenario.Validate(); err != nil {
		return nil, fmt.Errorf("invalid scenario: %w", err)
	}

	result := &BenchmarkResult{
		TestName:   scenario.Name,
		Timestamp:  time.Now(),
		Duration:   scenario.Duration,
		TestConfig: scenario,
	}

	// Create context with test duration timeout
	testCtx, cancel := context.WithTimeout(ctx, scenario.Duration+10*time.Second)
	defer cancel()

	// Execute based on test type
	var metrics []RequestMetrics
	var err error

	switch scenario.TestType {
	case "single":
		metrics, err = be.runSingleTickerTest(testCtx, scenario)
	case "bulk":
		metrics, err = be.runBulkTest(testCtx, scenario)
	case "health":
		metrics, err = be.runHealthTest(testCtx, scenario)
	case "cache":
		metrics, err = be.runCacheTest(testCtx, scenario)
	case "error":
		metrics, err = be.runErrorTest(testCtx, scenario)
	case "cold_start":
		metrics, err = be.runColdStartTest(testCtx, scenario)
	case "mixed":
		metrics, err = be.runMixedTest(testCtx, scenario)
	default:
		return nil, fmt.Errorf("unsupported test type: %s", scenario.TestType)
	}

	if err != nil {
		return nil, fmt.Errorf("scenario execution failed: %w", err)
	}

	// Calculate results from metrics
	be.calculateResults(result, metrics)

	return result, nil
}

// runSingleTickerTest executes single ticker valuation requests
func (be *BenchmarkExecutor) runSingleTickerTest(ctx context.Context, scenario TestScenario) ([]RequestMetrics, error) {
	return be.runLoadTest(ctx, scenario, func() string {
		ticker := scenario.Tickers[0] // Use first ticker for single tests
		return fmt.Sprintf("/api/v1/fair-value/%s", ticker)
	})
}

// runBulkTest executes bulk valuation requests
func (be *BenchmarkExecutor) runBulkTest(ctx context.Context, scenario TestScenario) ([]RequestMetrics, error) {
	requestBody := map[string][]string{
		"tickers": scenario.Tickers,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bulk request: %w", err)
	}

	return be.runLoadTestWithBody(ctx, scenario, "/api/v1/bulk", bodyBytes)
}

// runHealthTest executes health check requests
func (be *BenchmarkExecutor) runHealthTest(ctx context.Context, scenario TestScenario) ([]RequestMetrics, error) {
	return be.runLoadTest(ctx, scenario, func() string {
		return "/api/v1/health"
	})
}

// runCacheTest executes cache performance tests (repeated requests)
func (be *BenchmarkExecutor) runCacheTest(ctx context.Context, scenario TestScenario) ([]RequestMetrics, error) {
	tickerIndex := 0
	return be.runLoadTest(ctx, scenario, func() string {
		ticker := scenario.Tickers[tickerIndex%len(scenario.Tickers)]
		tickerIndex++
		return fmt.Sprintf("/api/v1/fair-value/%s", ticker)
	})
}

// runErrorTest executes error handling tests
func (be *BenchmarkExecutor) runErrorTest(ctx context.Context, scenario TestScenario) ([]RequestMetrics, error) {
	tickerIndex := 0
	return be.runLoadTest(ctx, scenario, func() string {
		ticker := scenario.Tickers[tickerIndex%len(scenario.Tickers)]
		tickerIndex++
		return fmt.Sprintf("/api/v1/fair-value/%s", ticker)
	})
}

// runColdStartTest executes cold start performance tests
func (be *BenchmarkExecutor) runColdStartTest(ctx context.Context, scenario TestScenario) ([]RequestMetrics, error) {
	// For cold start, we make a request, wait, then make another to test cold start behavior
	metrics := make([]RequestMetrics, 0)

	ticker := scenario.Tickers[0]
	endpoint := fmt.Sprintf("/api/v1/fair-value/%s", ticker)

	// First request (cold start)
	resp, err := be.MakeRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		metrics = append(metrics, RequestMetrics{
			StatusCode: 0,
			Duration:   0,
			Size:       0,
			Error:      err,
		})
	} else {
		metrics = append(metrics, RequestMetrics{
			StatusCode: resp.StatusCode,
			Duration:   resp.Duration,
			Size:       resp.Size,
			Error:      nil,
		})
	}

	// Wait for cache to potentially expire
	time.Sleep(1 * time.Second)

	// Run normal load test for the rest of the duration
	loadMetrics, err := be.runLoadTest(ctx, scenario, func() string {
		return endpoint
	})
	if err != nil {
		return metrics, err
	}

	metrics = append(metrics, loadMetrics...)
	return metrics, nil
}

// runMixedTest executes mixed workload tests
func (be *BenchmarkExecutor) runMixedTest(ctx context.Context, scenario TestScenario) ([]RequestMetrics, error) {
	tickerIndex := 0
	return be.runLoadTest(ctx, scenario, func() string {
		ticker := scenario.Tickers[tickerIndex%len(scenario.Tickers)]
		tickerIndex++
		return fmt.Sprintf("/api/v1/fair-value/%s", ticker)
	})
}

// runLoadTest executes a load test with the specified endpoint generator
func (be *BenchmarkExecutor) runLoadTest(ctx context.Context, scenario TestScenario, endpointGen func() string) ([]RequestMetrics, error) {
	return be.runLoadTestWithBody(ctx, scenario, "", nil, endpointGen)
}

// runLoadTestWithBody executes a load test with request body (for bulk requests)
func (be *BenchmarkExecutor) runLoadTestWithBody(ctx context.Context, scenario TestScenario, fixedEndpoint string, body []byte, endpointGenOpt ...func() string) ([]RequestMetrics, error) {
	var endpointGen func() string
	if len(endpointGenOpt) > 0 {
		endpointGen = endpointGenOpt[0]
	}

	metrics := make([]RequestMetrics, 0)
	metricsMutex := &sync.Mutex{}

	// Create worker pool for concurrency
	workers := scenario.Concurrency
	requestQueue := make(chan struct{}, 1000) // Buffer for requests

	// Rate limiter
	ticker := time.NewTicker(time.Second / time.Duration(scenario.RequestsPerSec))
	defer ticker.Stop()

	// Test timeout
	testCtx, cancel := context.WithTimeout(ctx, scenario.Duration)
	defer cancel()

	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-testCtx.Done():
					return
				case <-requestQueue:
					var endpoint string
					method := "GET"
					var reqBody []byte

					if fixedEndpoint != "" {
						endpoint = fixedEndpoint
						method = "POST"
						reqBody = body
					} else if endpointGen != nil {
						endpoint = endpointGen()
					}

					resp, err := be.MakeRequest(testCtx, method, endpoint, reqBody)

					metric := RequestMetrics{
						Error: err,
					}

					if resp != nil {
						metric.StatusCode = resp.StatusCode
						metric.Duration = resp.Duration
						metric.Size = resp.Size
					}

					metricsMutex.Lock()
					metrics = append(metrics, metric)
					metricsMutex.Unlock()
				}
			}
		}()
	}

	// Generate requests at specified rate
	requestGenerator := make(chan struct{})
	go func() {
		defer close(requestGenerator)
		for {
			select {
			case <-testCtx.Done():
				return
			case <-ticker.C:
				select {
				case requestQueue <- struct{}{}:
				case <-testCtx.Done():
					return
				default:
					// Queue full, skip this request
				}
			}
		}
	}()

	// Wait for test duration
	<-testCtx.Done()

	// Wait for request generator to finish, then close queue
	<-requestGenerator
	close(requestQueue)

	// Wait for remaining requests to complete (with timeout)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Force timeout if workers don't finish
	}

	return metrics, nil
}

// calculateResults computes benchmark results from request metrics
func (be *BenchmarkExecutor) calculateResults(result *BenchmarkResult, metrics []RequestMetrics) {
	if len(metrics) == 0 {
		return
	}

	var totalDuration time.Duration
	var successfulReqs int64
	var failedReqs int64
	var totalSize int64
	var latencies []time.Duration

	for _, metric := range metrics {
		if metric.Error != nil || metric.StatusCode >= 400 {
			failedReqs++
		} else {
			successfulReqs++
			totalDuration += metric.Duration
			totalSize += int64(metric.Size)
			latencies = append(latencies, metric.Duration)
		}
	}

	result.TotalRequests = int64(len(metrics))
	result.SuccessfulReqs = successfulReqs
	result.FailedReqs = failedReqs

	if successfulReqs > 0 {
		result.AvgLatency = totalDuration / time.Duration(successfulReqs)

		// Calculate percentiles
		sort.Slice(latencies, func(i, j int) bool {
			return latencies[i] < latencies[j]
		})

		if len(latencies) > 0 {
			result.MinLatency = latencies[0]
			result.MaxLatency = latencies[len(latencies)-1]

			p95Index := int(float64(len(latencies)) * 0.95)
			if p95Index >= len(latencies) {
				p95Index = len(latencies) - 1
			}
			result.P95Latency = latencies[p95Index]

			p99Index := int(float64(len(latencies)) * 0.99)
			if p99Index >= len(latencies) {
				p99Index = len(latencies) - 1
			}
			result.P99Latency = latencies[p99Index]
		}
	}

	// Calculate throughput (requests per second)
	result.ThroughputRPS = float64(result.TotalRequests) / result.Duration.Seconds()

	// Calculate error rate
	if result.TotalRequests > 0 {
		result.ErrorRatePercent = float64(result.FailedReqs) / float64(result.TotalRequests) * 100
	}
}
