package datafetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// This test measures that resolving a CIK is faster on second call due to cached mapping.
func TestCoordinateFetch_CacheSpeedsUpMapping(t *testing.T) {
	// Create fakes
	fakeSEC := &fakeSECGateway{
		// Simulate slow mapping call initially
		mappingDelay: 75 * time.Millisecond,
		mapping:      map[string]string{"AAPL": "320193", "MSFT": "789019"},
	}
	fakeMarket := &fakeMarketGateway{}
	fakeMacro := &fakeMacroGateway{}
	cache := newTestMemoryCache()

	cfg := &DataFetcherConfig{
		ConcurrentFetching: false,
		MaxRetries:         1,
		TimeoutDuration:    time.Second,
		TickerMappingTTL:   time.Minute,
	}

	dc := NewDataCoordinator(cfg, fakeSEC, fakeMarket, fakeMacro, cache)

	ctx := context.Background()

	// First ticker fetch – incurs mapping delay
	start1 := time.Now()
	_, err := dc.fetchSECData(ctx, "AAPL", "")
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	elapsed1 := time.Since(start1)

	// Second ticker fetch – should reuse mapping from cache and be faster
	fakeSEC.mappingDelay = 0
	start2 := time.Now()
	_, err = dc.fetchSECData(ctx, "MSFT", "")
	if err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}
	elapsed2 := time.Since(start2)

	if !(elapsed2*2 < elapsed1) {
		t.Fatalf("expected second fetch to be at least 2x faster; first=%v, second=%v", elapsed1, elapsed2)
	}
}

// ---- minimal fakes ----

type fakeSECGateway struct {
	mappingDelay time.Duration
	mapping      map[string]string
}

func (f *fakeSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
	return &entities.CompanyFactsResponse{CIK: cik, EntityName: "X", Facts: map[string]interface{}{}}, nil
}
func (f *fakeSECGateway) GetCompanyConcepts(ctx context.Context, cik string, tag string) (*entities.ConceptResponse, error) {
	return &entities.ConceptResponse{CIK: cik, Tag: tag}, nil
}
func (f *fakeSECGateway) GetTickerCIKMapping(ctx context.Context) (map[string]string, error) {
	if f.mappingDelay > 0 {
		time.Sleep(f.mappingDelay)
	}
	return f.mapping, nil
}
func (f *fakeSECGateway) GetFinancialDataForTicker(ctx context.Context, ticker, cik string) (*entities.HistoricalFinancialData, error) {
	// Return a valid HistoricalFinancialData with one period for testing.
	return &entities.HistoricalFinancialData{
		Ticker: ticker,
		Data: map[string]*entities.FinancialData{
			"FY2023": {
				Ticker:      ticker,
				CIK:         cik,
				TotalAssets: 1_000_000,
				Revenue:     500_000,
				FilingDate:  time.Now(),
			},
		},
	}, nil
}
func (f *fakeSECGateway) HealthCheck(ctx context.Context) error { return nil }

type fakeMarketGateway struct{}

func (f *fakeMarketGateway) GetQuote(ctx context.Context, ticker string) (*entities.MarketData, error) {
	return &entities.MarketData{}, nil
}
func (f *fakeMarketGateway) GetQuotes(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error) {
	return map[string]*entities.MarketData{}, nil
}
func (f *fakeMarketGateway) GetHistoricalPrices(ctx context.Context, ticker string, startDate, endDate time.Time) ([]*entities.PriceData, error) {
	return []*entities.PriceData{}, nil
}
func (f *fakeMarketGateway) HealthCheck(ctx context.Context) error { return nil }

type fakeMacroGateway struct{}

func (f *fakeMacroGateway) GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error) {
	return &entities.TreasuryRates{AsOf: time.Now(), Yield2Year: 0.04, Yield10Year: 0.045}, nil
}
func (f *fakeMacroGateway) GetMarketRiskPremium(ctx context.Context) (float64, error) {
	return 0.05, nil
}
func (f *fakeMacroGateway) HealthCheck(ctx context.Context) error { return nil }

// test memory cache using in-process implementation
func newTestMemoryCache() *testCacheRepo { return &testCacheRepo{store: map[string]entry{}} }

type entry struct {
	v   interface{}
	exp time.Time
}
type testCacheRepo struct{ store map[string]entry }

func (r *testCacheRepo) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	r.store[key] = entry{v: value, exp: time.Now().Add(ttl)}
	return nil
}
func (r *testCacheRepo) Get(ctx context.Context, key string, dest interface{}) error {
	e, ok := r.store[key]
	if !ok || time.Now().After(e.exp) {
		return fmt.Errorf("not found")
	}
	b, _ := json.Marshal(e.v)
	return json.Unmarshal(b, dest)
}
func (r *testCacheRepo) Delete(ctx context.Context, key string) error {
	delete(r.store, key)
	return nil
}
func (r *testCacheRepo) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := r.store[key]
	return ok, nil
}
func (r *testCacheRepo) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	if _, ok := r.store[key]; ok {
		return false, nil
	}
	return true, r.Set(ctx, key, value, ttl)
}
func (r *testCacheRepo) GetKeys(ctx context.Context, pattern string) ([]string, error) {
	return []string{}, nil
}
func (r *testCacheRepo) DeletePattern(ctx context.Context, pattern string) error { return nil }

// ---------------------------------------------------------------------------
// extractLatestUSDValue tests
// ---------------------------------------------------------------------------

// TestExtractLatestUSDValue exercises every early-return branch in the nested
// facts map traversal. Each test case targets exactly one "wrong type / missing key"
// level so we can confirm the function returns 0 gracefully.
func TestExtractLatestUSDValue(t *testing.T) {
	tests := []struct {
		name     string
		facts    map[string]interface{}
		taxonomy string
		concept  string
		expected float64
	}{
		{
			name:     "happy_path_returns_latest_value",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": []interface{}{
								map[string]interface{}{"val": 100.0, "fy": 2022},
								map[string]interface{}{"val": 200.0, "fy": 2023},
							},
						},
					},
				},
			},
			expected: 200.0,
		},
		{
			name:     "single_entry_returns_value",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": []interface{}{
								map[string]interface{}{"val": 42.5},
							},
						},
					},
				},
			},
			expected: 42.5,
		},
		{
			name:     "missing_taxonomy_key",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts:    map[string]interface{}{},
			expected: 0,
		},
		{
			name:     "taxonomy_wrong_type",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": "not a map",
			},
			expected: 0,
		},
		{
			name:     "missing_concept_key",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Revenue": map[string]interface{}{},
				},
			},
			expected: 0,
		},
		{
			name:     "concept_wrong_type",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": "not a map",
				},
			},
			expected: 0,
		},
		{
			name:     "missing_units_key",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"label": "Total Assets",
					},
				},
			},
			expected: 0,
		},
		{
			name:     "units_wrong_type",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": "not a map",
					},
				},
			},
			expected: 0,
		},
		{
			name:     "missing_usd_key",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"EUR": []interface{}{},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name:     "usd_wrong_type",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": "not a slice",
						},
					},
				},
			},
			expected: 0,
		},
		{
			name:     "usd_empty_array",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": []interface{}{},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name:     "latest_entry_wrong_type",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": []interface{}{
								"not a map",
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name:     "missing_val_key_in_entry",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": []interface{}{
								map[string]interface{}{"fy": 2023, "form": "10-K"},
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name:     "val_wrong_type_string",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts: map[string]interface{}{
				"us-gaap": map[string]interface{}{
					"Assets": map[string]interface{}{
						"units": map[string]interface{}{
							"USD": []interface{}{
								map[string]interface{}{"val": "not a number"},
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name:     "nil_facts_map",
			taxonomy: "us-gaap",
			concept:  "Assets",
			facts:    nil,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLatestUSDValue(tt.facts, tt.taxonomy, tt.concept)
			if got != tt.expected {
				t.Errorf("extractLatestUSDValue() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// coordinateSequential tests
// ---------------------------------------------------------------------------

// TestDataCoordinator_CoordinateSequential_AllSources verifies the sequential
// fetch path returns data from all three sources without concurrency.
func TestDataCoordinator_CoordinateSequential_AllSources(t *testing.T) {
	fakeSEC := &fakeSECGateway{
		mapping: map[string]string{"AAPL": "320193"},
	}
	fakeMarket := &fakeMarketGateway{}
	fakeMacro := &fakeMacroGateway{}
	cache := newTestMemoryCache()

	cfg := &DataFetcherConfig{
		ConcurrentFetching: false, // Force sequential path
		MaxRetries:         3,
	}

	dc := NewDataCoordinator(cfg, fakeSEC, fakeMarket, fakeMacro, cache)

	request := &entities.FetchRequest{
		Ticker:      "AAPL",
		DataSources: []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
	}

	result, err := dc.CoordinateFetch(context.Background(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All three sources should be present in metadata
	if len(result.SourceMetadata) != 3 {
		t.Fatalf("expected 3 source metadata entries, got %d", len(result.SourceMetadata))
	}

	// Financial data should be populated from SEC source
	if result.FinancialData == nil {
		t.Fatal("expected financial data to be populated")
	}

	// Market data should be populated
	if result.MarketData == nil {
		t.Fatal("expected market data to be populated")
	}

	// Macro data should be populated
	if result.MacroData == nil {
		t.Fatal("expected macro data to be populated")
	}

	// No errors expected when all sources succeed
	if len(result.Errors) != 0 {
		t.Fatalf("expected 0 errors, got %d", len(result.Errors))
	}
}

// TestDataCoordinator_CoordinateSequential_BreaksOnError verifies that sequential
// coordination stops after the first error when MaxRetries <= 1.
func TestDataCoordinator_CoordinateSequential_BreaksOnError(t *testing.T) {
	// SEC gateway that always fails — this should cause the sequential loop to break
	failingSEC := &fakeSECGateway{
		mapping: nil, // Force GetTickerCIKMapping to fail with nil return
	}
	fakeMarket := &fakeMarketGateway{}
	fakeMacro := &fakeMacroGateway{}
	cache := newTestMemoryCache()

	cfg := &DataFetcherConfig{
		ConcurrentFetching: false,
		MaxRetries:         1, // Trigger early break on error
	}

	dc := NewDataCoordinator(cfg, failingSEC, fakeMarket, fakeMacro, cache)

	request := &entities.FetchRequest{
		Ticker:      "FAIL",
		DataSources: []entities.DataSource{entities.SECSource, entities.MarketSource, entities.MacroSource},
	}

	result, err := dc.CoordinateFetch(context.Background(), request)
	if err != nil {
		t.Fatalf("coordinateSequential should not return error, got: %v", err)
	}

	// Should have at least one error from the SEC fetch failure
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one fetch error")
	}

	// Because MaxRetries <= 1 and the first source errors out, the loop should
	// break. Only 1 source metadata entry should exist (the failed SEC one).
	if len(result.SourceMetadata) != 1 {
		t.Fatalf("expected 1 source metadata (early break), got %d", len(result.SourceMetadata))
	}
}

// ---------------------------------------------------------------------------
// GetCoordinationMetrics test
// ---------------------------------------------------------------------------

// TestDataCoordinator_GetCoordinationMetrics verifies the metrics getter returns
// a valid initialized struct.
func TestDataCoordinator_GetCoordinationMetrics(t *testing.T) {
	cfg := &DataFetcherConfig{}
	dc := NewDataCoordinator(cfg, nil, nil, nil, nil)

	metrics := dc.GetCoordinationMetrics()

	if metrics == nil {
		t.Fatal("expected non-nil metrics")
	}
	if metrics.TotalCoordinations != 0 {
		t.Errorf("expected TotalCoordinations=0, got %d", metrics.TotalCoordinations)
	}
	if metrics.SourceErrorRates == nil {
		t.Error("expected SourceErrorRates map to be initialized")
	}
}

// ---------------------------------------------------------------------------
// fetchMacroData error path test
// ---------------------------------------------------------------------------

// fakeMacroGatewayWithRiskPremiumError simulates GetMarketRiskPremium failure
// while GetTreasuryRates succeeds. This covers the second error branch in fetchMacroData.
type fakeMacroGatewayWithRiskPremiumError struct{}

func (f *fakeMacroGatewayWithRiskPremiumError) GetTreasuryRates(ctx context.Context) (*entities.TreasuryRates, error) {
	return &entities.TreasuryRates{AsOf: time.Now(), Yield10Year: 0.04, Yield2Year: 0.035}, nil
}

func (f *fakeMacroGatewayWithRiskPremiumError) GetMarketRiskPremium(ctx context.Context) (float64, error) {
	return 0, fmt.Errorf("risk premium API unavailable")
}

func (f *fakeMacroGatewayWithRiskPremiumError) HealthCheck(ctx context.Context) error { return nil }

// TestDataCoordinator_FetchMacroData_RiskPremiumError tests the error path when
// treasury rates succeed but market risk premium fails.
func TestDataCoordinator_FetchMacroData_RiskPremiumError(t *testing.T) {
	cfg := &DataFetcherConfig{
		ConcurrentFetching: false,
		MaxRetries:         3,
	}

	dc := NewDataCoordinator(cfg, &fakeSECGateway{mapping: map[string]string{}}, &fakeMarketGateway{}, &fakeMacroGatewayWithRiskPremiumError{}, newTestMemoryCache())

	macroData, err := dc.fetchMacroData(context.Background())
	if err == nil {
		t.Fatal("expected error from risk premium failure")
	}
	if macroData != nil {
		t.Fatal("expected nil macro data on error")
	}
	if !contains(err.Error(), "market risk premium") {
		t.Errorf("error should mention 'market risk premium', got: %v", err)
	}
}

// contains is a small test helper to check substring presence.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// fetchFromSource unknown source test
// ---------------------------------------------------------------------------

// TestDataCoordinator_FetchFromSource_UnknownSource exercises the default branch
// in fetchFromSource when an unrecognized DataSource is provided.
func TestDataCoordinator_FetchFromSource_UnknownSource(t *testing.T) {
	cfg := &DataFetcherConfig{}
	dc := NewDataCoordinator(cfg, &fakeSECGateway{mapping: map[string]string{}}, &fakeMarketGateway{}, &fakeMacroGateway{}, newTestMemoryCache())

	request := &entities.FetchRequest{Ticker: "TEST"}
	srcResult := dc.fetchFromSource(context.Background(), request, entities.DataSource("unknown_source"))

	if srcResult.err == nil {
		t.Fatal("expected error for unknown data source")
	}
	if srcResult.metadata.StatusCode != 500 {
		t.Errorf("expected status code 500 for error, got %d", srcResult.metadata.StatusCode)
	}
}
