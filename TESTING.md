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
  api/v1/handlers/
    fair_value_handler_overrides_test.go  # POST handler + override validation tests (T7-T10)
  services/
    valuation/
      service.go              # Implementation
      service_test.go          # Unit tests
      service_bench_test.go    # Benchmarks
      service_perf_test.go     # Performance tests
      service_concurrent_test.go # Concurrency tests
    valuation/params/
      params_test.go           # params.Overrides + projectOverrides unit tests
      resolve_test.go          # params.Resolve* knob resolution unit tests (Layer-2 invariants)
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

Phase 1 extends the same pattern to the recompute side:
`internal/services/datacleaner/recompute_test.go::TestRecomputeUmbrellas_Property_WellFormedNoDivergence`
(4 properties × 200 iterations, pinned seed `20260517`) asserts that for any
well-formed `FinancialData` where the plug invariant holds, `recomputeUmbrellas`
emits zero WARN lines. The clamp-fired and adjuster-mutated branches are
recorded (NOT asserted) by the integration test at
`internal/integration/datacleaner_recompute_shadow_test.go` for Phase 2's
punch list — committed snapshots under
`internal/integration/testdata/recompute-shadow/<TICKER>.json` are the
Phase 1 → Phase 2 hand-off artifact. The load-bearing
`TestRecomputeUmbrellas_NoMutation` `reflect.DeepEqual` snapshot test pins
the "zero `fd.*` writes" invariant — any commit that breaks it must be
reverted, not adjusted.

**Per-rule adapter test pattern (DC-1 Phase 2 PR-2+).** Every native
`Adjuster` implementation ships a dedicated test file at
`internal/services/datacleaner/adjustments/a<rule>_<name>_adjuster_test.go`
pinning the AdjusterOutput shape across the rule's branches. The conventional
test structure has four parts: (1) `TestA<X><Rule>Adjuster_Adjuster_Interface_Contract`
with subtests for every fired and skipped branch (asserting LedgerEntry
Component / DeltaAmount / EquityOffset / TaxShieldDTA / SkipReason / SkipMetrics
shape per role); (2) `TestAssetAdjuster_ProcessAssetAdjustments_NativeA<X>Emission`
exercising the dispatcher fired path + verifying the dual-write mutation +
`NativeLedgerEntries` / `NativelyEmittedRuleIDs` population; (3) a corresponding
`NativeA<X>SkipPath` test pinning the dispatcher skip behavior; (4) a `*-mutation-free*`
assertion confirming `Apply` does not mutate `working`. The four role flavors
(OverlayEmitter / Restater / Restater+TaxShieldDTA / FlagEmitter) all use this
template — see `a1_goodwill_adjuster_test.go`, `a2_intangible_adjuster_test.go`,
`a4_dta_valuation_allowance_adjuster_test.go`, `a5_inventory_writedown_adjuster_test.go`,
and `a_flag_only_reviews_adjuster_test.go` for the canonical patterns. PR-3
(earnings C1-C7) extends this template to the earnings adjusters at
`c1_restructuring_adjuster_test.go`, `c2_asset_sale_gains_adjuster_test.go`,
`c3_litigation_settlements_adjuster_test.go`, `c4_stock_compensation_adjuster_test.go`,
`c5_derivative_gains_losses_adjuster_test.go`, `c6_capitalized_interest_adjuster_test.go`,
and `c7_working_capital_adjuster_test.go`. C6's `EquityOffset=0` is pinned by
a dedicated subtest in `c6_capitalized_interest_adjuster_test.go` with an
explicit failure message naming "Phase 3 Restated() must NOT add C6 DeltaAmount
to retained earnings" — this is the load-bearing carrier of the
capitalized-interest reclassification semantics and must not regress when
Phase 3 ships the `Restated()` accessor. **PR-4 (liabilities B1/B2/B3)
extends this template to the final category** — see
`b1_operating_leases_adjuster_test.go`, `b2_pension_adjuster_test.go`, and
`b3_contingent_liabilities_adjuster_test.go`. The B3 test pins
`Field:"DebtLikeClaims"` on the emitted OverlaySpec with an explicit
failure message naming the Phase 4 routing intent ("B3 emits
DebtLikeClaims overlay — Phase 4 consumer reads Overlays[Field:DebtLikeClaims],
NOT data.TotalDebt"), mirroring the C6 EquityOffset=0 pin convention.
B3's `AIProvenance` best-effort capture (`ModelName: b3AIModelName`;
`PromptHash`/`SourceDocHash`/`ExtractedSpan` empty per Q4 deferral) is
pinned by a dedicated subtest that asserts the populated fields are
non-zero AND the deferred-hash fields are empty.

**Basket-snapshot ledger integration test (DC-1 Phase 2 PR-4 Task 4.6).**
`internal/integration/datacleaner_ledger_basket_test.go::TestLedger_BasketSnapshot_ClusterPrediction`
is the first integration test to read `data.AdjustmentLedger` directly
and assert on per-ticker expected AdjusterID sets. The test runs the
production cleaner pipeline (`CleanFinancialData`) against every period
of every basket ticker bundle under
`artifacts/tier2-baseline/<newest-date>/<TICKER>/` and asserts that the
observed-across-all-periods SET of `AdjusterID` values on
`data.AdjustmentLedger` is a SUPERSET of a conservative per-ticker
lower-bound table (the `predictionRows` table). Truth source: the
committed shadow snapshots at
`internal/integration/testdata/recompute-shadow/<TICKER>.json` (filed
by Phase 1's basket-recording test) plus the 7-cluster mapping in
`docs/refactoring/archive/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md`,
inverted via `service.go::checkRuleApplicability`. The assertion
granularity is "considered, not fired" — the AdjusterID is populated
on every LedgerEntry regardless of `Fired` state, so the test catches
the regression "a future refactor silently stopped emitting LedgerEntries
for adjuster X on ticker Y" without depending on per-period firing
state we can't predict from the bundle root alone. Skip behavior for
missing bundles + `passedCount >= 5` floor mirror
`TestDataCleanerRecompute_ShadowMode_TickerBasket` so the two tests
degrade in lockstep.

**FlagEmitter test convention (DC-1 Phase 2 PR-2 Task 2.5).** Flag-only reviews
(R&D capitalization, capitalized software) emit `Fired:false` LedgerEntries on
EVERY path including the "review fired its flag" path — `SkipReason="flag-only
review; no balance-sheet adjustment"` + populated `AdjusterOutput.Flags` slice
IS the firing signal. The convention is pinned by subtests that assert
`Fired:false` AND `len(out.Flags) > 0` on the fired path, distinguishing this
shape from both the standard skip path (Fired:false + Flags empty) and the
Restater fired path (Fired:true + Component/DeltaAmount populated).

**Firing-signal regression-pin pattern (DC-1 Phase 5 P5-C3 follow-up).** When
the orchestrator's firing predicate is rewritten (e.g., legacy `.Applied` bool
→ native `nativeFired(LedgerEntries, Overlays, Flags)` helper), the regression
test MUST exercise the path where outer applicability passes but the inner
`Apply*` skips — because skip paths emit `Fired:false` diagnostic LedgerEntries
that a naive `len(NativeLedgerEntries) > 0` predicate over-counts.
`TestApplyActiveAdjustments_FiringSignalParity_A1ApplicableButSkipped` at
`internal/services/datacleaner/applyactive_firingsignal_parity_test.go` is the
canonical example: Goodwill at 3% of TotalAssets passes A1's `data.Goodwill > 0`
applicability check but skips at the 5% materiality threshold, emitting one
`Fired:false` LedgerEntry. The test asserts `result.RulesApplied == 0` AND the
diagnostic LedgerEntry survives on `data.AdjustmentLedger` — proving the
firing-signal correctly filters skip diagnostics WITHOUT losing the
observability surface. Pattern applies any time a `len(slice) > 0` predicate
is the firing signal and the slice can contain diagnostic non-firing entries.

**Ignore-guard pattern (DC-1 Phase 5 P5-C1 follow-up).** When a model contract
states "model X must IGNORE input field Y" (e.g., `router.go` ModelInput godoc:
"FFO does not consume input.DebtLikeClaims"), pin the contract with a
`math.Float64bits` equality assertion across DIFFERENT non-zero values of the
ignored field. `TestFFO_IgnoresDebtLikeClaims` at
`internal/services/valuation/models/ffo_phase5_debtlikeclaims_test.go` is the
canonical example: runs FFO with `DebtLikeClaims=0` and `DebtLikeClaims=$250M`
against the same fixture; asserts Float64bits equality on
`IntrinsicValuePerShare`, `EquityValue`, AND `EnterpriseValue`. A future edit
that accidentally adds a `DebtLikeClaims` term to FFO's EV bridge would fail
the third assertion immediately. Pattern applies any time a load-bearing
"model ignores X" contract exists in production code without a regression test —
the bit-equality assertion is the strongest possible guard because ANY arithmetic
involving the field would surface as bit drift, even if the resulting value
rounds back to the original via float coincidence.

**Byte-identity acceptance method (request-valuation-overrides T9/T10).** The
contract "empty POST body produces a response byte-identical to GET for the same
ticker" is pinned by `TestPostFairValue_EmptyBody_EqualsGET` at
`internal/api/v1/handlers/fair_value_handler_overrides_test.go`. The test runs
both handlers against the same mock service, marshals both responses to JSON, and
asserts `bytes.Equal` on the two byte slices — not `reflect.DeepEqual`, not
string equality, but actual byte identity. Any code path that sets a new field in
`buildFairValueResponse` without gating on nil/zero would fail this test
immediately. The companion test `TestPostFairValue_WithOverrides_AppliedOverridesPresent`
asserts `applied_overrides` is populated when overrides are supplied and exactly
matches the set of overrides that were provided (no phantom fields, no missing
fields). New handler features that touch `FairValueResponse` or
`buildFairValueResponse` MUST maintain both of these tests.

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
