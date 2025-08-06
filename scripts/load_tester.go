package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// LoadTestConfig holds configuration for the load test
type LoadTestConfig struct {
	BaseURL        string
	APIKey         string
	Concurrency    int
	Duration       time.Duration
	RequestsPerSec int
	TestEndpoints  []string
	TestTickers    []string
}

// LoadTestResult holds the results of a load test
type LoadTestResult struct {
	TotalRequests     int64         `json:"total_requests"`
	SuccessfulReqs    int64         `json:"successful_requests"`
	FailedReqs        int64         `json:"failed_requests"`
	AverageLatency    time.Duration `json:"average_latency"`
	MinLatency        time.Duration `json:"min_latency"`
	MaxLatency        time.Duration `json:"max_latency"`
	RequestsPerSecond float64       `json:"requests_per_second"`
	ErrorRate         float64       `json:"error_rate"`
	TestDuration      time.Duration `json:"test_duration"`
}

// RequestMetric holds metrics for a single request
type RequestMetric struct {
	URL        string
	StatusCode int
	Latency    time.Duration
	Error      error
	Timestamp  time.Time
}

func main() {
	// Parse command line flags
	var (
		baseURL     = flag.String("url", "http://localhost:8080", "Base URL of the API")
		apiKey      = flag.String("key", "", "API key for authentication")
		concurrency = flag.Int("concurrency", 10, "Number of concurrent connections")
		duration    = flag.Duration("duration", 60*time.Second, "Duration of the load test")
		rps         = flag.Int("rps", 10, "Requests per second")
		testType    = flag.String("type", "single", "Test type: single, bulk, mixed")
		outputFile  = flag.String("output", "", "Output file for results (JSON)")
	)
	flag.Parse()

	if *apiKey == "" {
		log.Fatal("API key is required. Use -key flag")
	}

	config := &LoadTestConfig{
		BaseURL:        *baseURL,
		APIKey:         *apiKey,
		Concurrency:    *concurrency,
		Duration:       *duration,
		RequestsPerSec: *rps,
		TestTickers:    getTestTickers(*testType),
		TestEndpoints:  getTestEndpoints(*testType),
	}

	fmt.Printf("Starting load test with configuration:\n")
	fmt.Printf("  Base URL: %s\n", config.BaseURL)
	fmt.Printf("  Concurrency: %d\n", config.Concurrency)
	fmt.Printf("  Duration: %v\n", config.Duration)
	fmt.Printf("  Target RPS: %d\n", config.RequestsPerSec)
	fmt.Printf("  Test Type: %s\n", *testType)
	fmt.Printf("  Test Tickers: %v\n", config.TestTickers)
	fmt.Println()

	// Run the load test
	result := runLoadTest(config)

	// Print results
	printResults(result)

	// Save results to file if specified
	if *outputFile != "" {
		saveResults(result, *outputFile)
	}
}

func runLoadTest(config *LoadTestConfig) *LoadTestResult {
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	// Channels for coordinating workers
	requestChan := make(chan RequestTask, config.Concurrency*2)
	resultChan := make(chan RequestMetric, config.Concurrency*2)

	// Metrics tracking
	var (
		totalRequests  int64
		successfulReqs int64
		failedReqs     int64
		totalLatency   int64
		minLatency     = int64(time.Hour)
		maxLatency     int64
	)

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			worker(ctx, workerID, config, requestChan, resultChan)
		}(i)
	}

	// Start result collector
	collectorDone := make(chan bool)
	go func() {
		for metric := range resultChan {
			atomic.AddInt64(&totalRequests, 1)

			if metric.Error != nil || metric.StatusCode >= 400 {
				atomic.AddInt64(&failedReqs, 1)
			} else {
				atomic.AddInt64(&successfulReqs, 1)
			}

			// Update latency statistics
			latencyNanos := metric.Latency.Nanoseconds()
			atomic.AddInt64(&totalLatency, latencyNanos)

			// Update min/max latency atomically
			for {
				currentMin := atomic.LoadInt64(&minLatency)
				if latencyNanos >= currentMin || atomic.CompareAndSwapInt64(&minLatency, currentMin, latencyNanos) {
					break
				}
			}

			for {
				currentMax := atomic.LoadInt64(&maxLatency)
				if latencyNanos <= currentMax || atomic.CompareAndSwapInt64(&maxLatency, currentMax, latencyNanos) {
					break
				}
			}
		}
		collectorDone <- true
	}()

	// Generate requests at the specified rate
	startTime := time.Now()
	ticker := time.NewTicker(time.Second / time.Duration(config.RequestsPerSec))
	defer ticker.Stop()

	requestIndex := 0
	for {
		select {
		case <-ctx.Done():
			close(requestChan)
			goto done
		case <-ticker.C:
			// Round-robin through test endpoints and tickers
			endpoint := config.TestEndpoints[requestIndex%len(config.TestEndpoints)]
			ticker := config.TestTickers[requestIndex%len(config.TestTickers)]

			task := RequestTask{
				URL:    fmt.Sprintf("%s%s", config.BaseURL, fmt.Sprintf(endpoint, ticker)),
				Method: "GET",
			}

			select {
			case requestChan <- task:
				requestIndex++
			default:
				// Channel full, skip this request
			}
		}
	}

done:
	// Wait for all workers to finish
	wg.Wait()
	close(resultChan)
	<-collectorDone

	endTime := time.Now()
	testDuration := endTime.Sub(startTime)

	// Calculate final metrics
	totalReqs := atomic.LoadInt64(&totalRequests)
	successReqs := atomic.LoadInt64(&successfulReqs)
	failReqs := atomic.LoadInt64(&failedReqs)
	avgLatency := time.Duration(0)
	if totalReqs > 0 {
		avgLatency = time.Duration(atomic.LoadInt64(&totalLatency) / totalReqs)
	}

	result := &LoadTestResult{
		TotalRequests:     totalReqs,
		SuccessfulReqs:    successReqs,
		FailedReqs:        failReqs,
		AverageLatency:    avgLatency,
		MinLatency:        time.Duration(atomic.LoadInt64(&minLatency)),
		MaxLatency:        time.Duration(atomic.LoadInt64(&maxLatency)),
		RequestsPerSecond: float64(totalReqs) / testDuration.Seconds(),
		ErrorRate:         float64(failReqs) / float64(totalReqs) * 100,
		TestDuration:      testDuration,
	}

	return result
}

// RequestTask represents a request to be made
type RequestTask struct {
	URL    string
	Method string
	Body   []byte
}

func worker(ctx context.Context, workerID int, config *LoadTestConfig, requestChan <-chan RequestTask, resultChan chan<- RequestMetric) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-requestChan:
			if !ok {
				return
			}

			// Execute the request
			metric := executeRequest(client, task, config.APIKey)

			select {
			case resultChan <- metric:
			case <-ctx.Done():
				return
			}
		}
	}
}

func executeRequest(client *http.Client, task RequestTask, apiKey string) RequestMetric {
	startTime := time.Now()

	var req *http.Request
	var err error

	if task.Body != nil {
		req, err = http.NewRequest(task.Method, task.URL, bytes.NewReader(task.Body))
	} else {
		req, err = http.NewRequest(task.Method, task.URL, nil)
	}

	metric := RequestMetric{
		URL:       task.URL,
		Timestamp: startTime,
	}

	if err != nil {
		metric.Error = err
		metric.Latency = time.Since(startTime)
		return metric
	}

	// Add headers
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		metric.Error = err
		metric.Latency = time.Since(startTime)
		return metric
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response to ensure complete processing
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		metric.Error = err
	}

	metric.StatusCode = resp.StatusCode
	metric.Latency = time.Since(startTime)

	return metric
}

func getTestTickers(testType string) []string {
	switch testType {
	case "single":
		return []string{"AAPL"}
	case "bulk":
		return []string{"AAPL", "GOOGL", "MSFT", "AMZN", "TSLA"}
	case "mixed":
		return []string{
			"AAPL", "GOOGL", "MSFT", "AMZN", "TSLA",
			"META", "NVDA", "AMD", "INTC", "ORCL",
			"CRM", "ADBE", "NOW", "SNOW", "PLTR",
		}
	default:
		return []string{"AAPL"}
	}
}

func getTestEndpoints(testType string) []string {
	switch testType {
	case "single":
		return []string{"/api/v1/fair-value/%s"}
	case "bulk":
		return []string{"/api/v1/bulk"}
	case "mixed":
		return []string{
			"/api/v1/fair-value/%s",
			"/api/v1/health/detailed",
			"/api/v1/metrics",
		}
	default:
		return []string{"/api/v1/fair-value/%s"}
	}
}

func printResults(result *LoadTestResult) {
	fmt.Println("Load Test Results:")
	fmt.Println("==================")
	fmt.Printf("Total Requests:     %d\n", result.TotalRequests)
	fmt.Printf("Successful:         %d\n", result.SuccessfulReqs)
	fmt.Printf("Failed:             %d\n", result.FailedReqs)
	fmt.Printf("Success Rate:       %.2f%%\n", float64(result.SuccessfulReqs)/float64(result.TotalRequests)*100)
	fmt.Printf("Error Rate:         %.2f%%\n", result.ErrorRate)
	fmt.Printf("Average Latency:    %v\n", result.AverageLatency)
	fmt.Printf("Min Latency:        %v\n", result.MinLatency)
	fmt.Printf("Max Latency:        %v\n", result.MaxLatency)
	fmt.Printf("Requests/Second:    %.2f\n", result.RequestsPerSecond)
	fmt.Printf("Test Duration:      %v\n", result.TestDuration)
	fmt.Println()

	// Performance assessment
	fmt.Println("Performance Assessment:")
	if result.ErrorRate > 5 {
		fmt.Println("⚠️  High error rate detected")
	}
	if result.AverageLatency > 1*time.Second {
		fmt.Println("⚠️  High average latency")
	}
	if result.RequestsPerSecond < 10 {
		fmt.Println("⚠️  Low throughput")
	}
	if result.ErrorRate < 1 && result.AverageLatency < 500*time.Millisecond && result.RequestsPerSecond > 50 {
		fmt.Println("✅ Good performance metrics")
	}
}

func saveResults(result *LoadTestResult, filename string) {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal results: %v", err)
		return
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("Failed to write results to file: %v", err)
		return
	}

	fmt.Printf("Results saved to %s\n", filename)
}
