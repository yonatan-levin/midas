# TESTING.md - Testing Strategy & Guidelines

This document defines the testing strategy, conventions, and guidelines for the Midas DCF Valuation API.

## Testing Philosophy

- **TDD mandatory**: Write failing tests first, then implement
- **Financial correctness**: Property-based testing for all financial calculations
- **Boundary validation**: Test at system boundaries (API inputs, external service responses)
- **Fast feedback**: Unit tests run in < 30s, full suite in < 5 minutes

## Running Tests

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run with coverage report
go test -cover ./...

# Run with detailed coverage profile
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out

# Run a specific package
go test ./internal/services/valuation/...

# Run a specific test
go test -run TestServiceName_MethodName ./internal/services/valuation/

# Run integration tests only
go test ./internal/integration/...

# Run benchmarks
go test -bench=. ./internal/services/valuation/...

# Run with race detector
go test -race ./...

# Run live E2E tests (requires running server + real external APIs)
E2E_LIVE=1 go test ./internal/integration/...
```

## Test Organization

### Directory Structure

```
internal/
  api/
    server.go                  # HTTP server, middleware, routes
    server_test.go             # Server middleware + handler tests (96.2%)
  services/
    valuation/
      service.go              # Implementation
      service_test.go          # Unit tests
      service_bench_test.go    # Benchmarks
      service_perf_test.go     # Performance tests
      service_concurrent_test.go # Concurrency tests
  integration/                 # Cross-service integration tests
    api_routes_test.go         # API endpoint tests
    api_contract_fuzz_test.go  # Contract fuzz testing
    e2e_live_test.go           # Live E2E (gated by E2E_LIVE=1)
    service_test.go            # Service integration tests
    datacleaner_*_test.go      # DataCleaner integration variants
    scheduler_integration_test.go
    test_setup_test.go         # Shared test fixtures
pkg/
  finance/
    dcf/dcf_test.go            # DCF calculation tests
    wacc/wacc_test.go          # WACC unit tests
    wacc/wacc_property_test.go # WACC property-based tests
    growth/growth_test.go      # Growth rate tests
    leases/estimation_test.go  # Lease estimation tests
    leases/performance_test.go # Lease performance tests
```

### Test Categories

| Category | Location | Purpose | Run Condition |
|----------|----------|---------|---------------|
| **Unit** | `*_test.go` alongside code | Test single functions/methods in isolation | Always |
| **Integration** | `internal/integration/` | Test service interactions, API routes | Always |
| **E2E Live** | `internal/integration/e2e_live_test.go` | Test with real external APIs | `E2E_LIVE=1` |
| **Contract Fuzz** | `internal/integration/api_contract_fuzz_test.go` | Fuzz API with invalid inputs | Always |
| **Benchmark** | `*_bench_test.go` | Performance regression detection | On demand |
| **Performance** | `*_perf_test.go` | Load/stress testing | On demand |
| **Property** | `*_property_test.go` | Mathematical correctness | Always |

## Coverage Targets

| Module | Target | Current |
|--------|--------|---------|
| `internal/api` | >= 90% | ~96.2% |
| `pkg/finance/dcf` | >= 90% | ~96.6% |
| `pkg/finance/growth` | >= 90% | ~91.6% |
| `pkg/finance/wacc` | >= 85% | ~98.5% |
| `internal/services/valuation` | >= 90% | ~91.2% |
| `internal/services/valuation/models` | >= 90% | ~91.5% |
| `internal/services/growth` | >= 90% | ~97.1% |
| `internal/services/datafetcher` | >= 80% | ~95.3% |
| `internal/services/metrics` | >= 85% | ~96.8% |
| `internal/services/datacleaner/industry` | >= 80% | ~97.2% |
| `internal/infra/gateways/sec` | >= 90% | ~93.3% |
| `internal/infra/gateways/market` | >= 80% | ~82.0% |
| `internal/api/v1/handlers` | >= 90% | ~94.3% |
| `internal/services/datacleaner` | >= 80% | ~38.9% |
| Overall | >= 80% | ~85%+ |

## Test Patterns

### 1. Table-Driven Tests (Standard Pattern)

Used for testing multiple scenarios of the same function:

```go
func TestCalculateWACC_Scenarios(t *testing.T) {
    tests := []struct {
        name     string
        input    WACCInput
        expected float64
        wantErr  bool
    }{
        {
            name:     "standard company with moderate leverage",
            input:    WACCInput{CostOfEquity: 0.10, CostOfDebt: 0.05, ...},
            expected: 0.085,
        },
        {
            name:     "zero equity value returns error",
            input:    WACCInput{EquityValue: 0},
            wantErr:  true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := CalculateWACC(tt.input)
            if tt.wantErr {
                assert.Error(t, err)
                return
            }
            assert.NoError(t, err)
            assert.InDelta(t, tt.expected, result, 0.001)
        })
    }
}
```

### 2. Property-Based Tests (Financial Math)

Used with `gopter` for mathematical invariants:

```go
func TestWACC_Properties(t *testing.T) {
    properties := gopter.NewProperties(nil)

    properties.Property("WACC is between cost of equity and cost of debt", prop.ForAll(
        func(ke, kd float64, ratio float64) bool {
            wacc := ratio*ke + (1-ratio)*kd
            return wacc >= min(ke, kd) && wacc <= max(ke, kd)
        },
        gen.Float64Range(0.01, 0.30),  // Cost of equity: 1-30%
        gen.Float64Range(0.01, 0.15),  // Cost of debt: 1-15%
        gen.Float64Range(0.0, 1.0),    // Equity ratio: 0-100%
    ))

    properties.TestingRun(t)
}
```

**Plug-invariant property tests (DC-1 Phase 0+).** The same `gopter` pattern pins
the cleaner's `components-sum-to-umbrellas` invariant across randomized
`FinancialData` inputs. See
`internal/infra/gateways/sec/plugs_test.go::TestComputePlugs_Property_*`
(4 properties × 200 iterations, pinned seed `20260516`, well-formed branch
exercised by construction; clamp-fired branch covered by a separate
`TestComputePlugs_NegativeResidualClampedAndLogged` table test and by the
integration basket test at
`internal/integration/datacleaner_plug_invariants_test.go`). Spec:
`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`
(Testing strategy → T1).

### 3. Mock-Based Tests (Service Layer)

Using `testify/mock` for interface dependencies:

```go
type MockSECGateway struct {
    mock.Mock
}

func (m *MockSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
    args := m.Called(ctx, cik)
    if args.Get(0) == nil {
        return nil, args.Error(1)
    }
    return args.Get(0).(*entities.CompanyFactsResponse), args.Error(1)
}

func TestValuationService_CalculateFairValue(t *testing.T) {
    mockSEC := new(MockSECGateway)
    mockSEC.On("GetCompanyFacts", mock.Anything, "0000320193").
        Return(testFixtures.AppleCompanyFacts(), nil)

    // ... test logic
    mockSEC.AssertExpectations(t)
}
```

### 4. Integration Tests (Cross-Service)

Testing full service pipelines with real (but local) dependencies:

```go
func TestFullValuationPipeline(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }

    // Setup in-memory DB, seed test data
    db := setupTestDB(t)
    defer db.Close()

    // Wire real services with test config
    svc := setupValuationService(t, db)

    result, err := svc.CalculateFairValue(ctx, "AAPL")
    require.NoError(t, err)
    assert.Greater(t, result.DCFValuePerShare, 0.0)
    assert.NotEmpty(t, result.DataQualityGrade)
}
```

### 5. Contract Fuzz Tests

Ensuring API doesn't return 5xx on any input:

```go
func TestAPIContract_NoServerErrors(t *testing.T) {
    server := setupTestServer(t)

    // Fuzz with random tickers
    for _, ticker := range fuzzTickers {
        resp := server.GET("/api/v1/fair-value/" + ticker)
        assert.NotEqual(t, 500, resp.Code, "5xx on ticker: %s", ticker)
    }
}
```

### 6. Benchmark Tests

Performance regression detection:

```go
func BenchmarkValuationService_CalculateFairValue(b *testing.B) {
    svc := setupBenchService(b)
    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _ = svc.CalculateFairValue(ctx, "AAPL")
    }
}
```

## Test Naming Convention

```
Test{ServiceName}_{MethodName}_{Scenario}
```

Examples:
- `TestValuationService_CalculateFairValue_Success`
- `TestValuationService_CalculateFairValue_MissingMarketData`
- `TestDataCleaner_Clean_AppliesAssetAdjustments`
- `TestWACC_Calculate_ZeroEquityReturnsError`

## Test Assertions

- Use `testify/assert` for non-fatal assertions (test continues)
- Use `testify/require` for fatal assertions (test stops)
- Use `assert.InDelta(t, expected, actual, delta)` for floating-point comparisons
- Default delta for financial values: `0.001` (0.1%)
- Default delta for percentages: `0.0001` (0.01%)

## Test Fixtures

Shared test fixtures are in `internal/integration/test_setup_test.go` and individual `*_test.go` files. Common patterns:

- `setupTestDB(t)` - Create in-memory SQLite with schema
- `setupTestServer(t)` - Create test HTTP server with all middleware
- `testFixtures.Apple*()` - Pre-built test data for Apple (AAPL)

## Special Test Considerations

### Windows
- E2E live tests are skipped on Windows (gated by `E2E_LIVE=1` which is CI-only)
- Redis tests use in-memory fallback on Windows
- Some file path tests may need `filepath.Join` awareness

### CGO
- SQLite driver (`mattn/go-sqlite3`) requires CGO enabled
- Tests using real SQLite need `CGO_ENABLED=1`
- For pure-Go testing, mock the repository interfaces

### External Dependencies
- SEC API tests use recorded HTTP responses (no live calls in unit tests)
- Yahoo Finance tests use mock responses
- FRED tests use manual config values
- Live tests (E2E_LIVE=1) hit real APIs and are rate-limited

### Testcontainers
- `testcontainers-go` is available for Docker-based integration tests
- Used for Redis and PostgreSQL when needed
- Skipped when Docker is not available

## Performance Testing

### Load Testing

```bash
# Run the built-in load tester
go run ./scripts/load_tester.go \
    -url http://localhost:8080 \
    -key <API_KEY> \
    -type single \
    -concurrency 20 \
    -duration 60s \
    -rps 20 \
    -output performance/results/baseline.json
```

### Performance Targets

| Metric | Target |
|--------|--------|
| p95 latency | < 300ms at 20 RPS |
| Error rate | < 1% |
| Throughput | >= 20 RPS sustained |

### Contract Fuzz Testing (Schemathesis)

```bash
# Install Schemathesis
pip install schemathesis

# Run fuzz testing against OpenAPI spec
schemathesis run http://localhost:8080/docs/openapi.yaml \
    --header "X-API-Key: <key>" \
    --checks all
```

## CI/CD Integration

Tests are expected to pass in CI with:
- `go test ./...` (all non-live tests)
- `go test -race ./...` (race condition detection)
- `go test -cover ./...` (coverage reporting)

Live E2E tests run separately with `E2E_LIVE=1` on non-Windows CI runners.

## Adding New Tests

1. **New feature**: Write failing tests first (TDD), then implement
2. **Bug fix**: Write a test that reproduces the bug, then fix it
3. **New service**: Add unit tests in the same package + integration tests in `internal/integration/`
4. **New financial calculation**: Add property-based tests with `gopter` for mathematical invariants
5. **New API endpoint**: Add contract tests in `internal/integration/api_routes_test.go`
