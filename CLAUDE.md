# CLAUDE.md - Midas DCF Valuation API

This file provides guidance for AI assistants (Claude, etc.) working on this codebase.

READING AGENTS.md IS MANDATORY.

## Project Overview

**Midas** is a production-grade REST API for equity valuation using Discounted Cash Flow (DCF) analysis. It fetches real-time financial data from SEC EDGAR, market prices from Yahoo Finance/Finzive, and macroeconomic indicators from FRED, then normalizes, cleans, and uses that data to calculate intrinsic value per share.

- **Module**: `github.com/midas/dcf-valuation-api`
- **Go Version**: 1.23+ (toolchain 1.24.4)
- **Current Version**: v0.9.0-rc1 (MVP)

## Build & Run Commands

```bash
# Build
go build ./cmd/server

# Run locally
go run cmd/server/main.go

# Run tests (all)
go test ./...

# Run tests with coverage
go test -cover ./...

# Run a specific package's tests
go test ./internal/services/valuation/...

# Run integration tests only
go test ./internal/integration/...

# Apply database schema + migrations + demo data
go run ./cmd/migrate -db ./data/midas.db

# Seed a demo API key
go run ./cmd/seed-demo-key -db ./data/midas.db

# Hash an API key (utility)
go run ./cmd/hash-key -key <your-key>

# Docker
docker-compose up -d              # Development
docker-compose -f docker-compose.prod.yml up -d  # Production

# Launch staging (scripts)
./scripts/launch_staging.sh       # Linux/macOS
.\scripts\launch_staging.ps1      # Windows

# Contract fuzz testing
./scripts/contract_fuzz.ps1 -DemoKey '<key>' -ApiBase 'http://localhost:8080' -DbPath './data/midas.db'

# Load testing
go run ./scripts/load_tester.go -url http://localhost:8080 -key <API_KEY> -type single -concurrency 20 -duration 60s -rps 20

# Observability lint guard (Phase S) — fails if request-path code uses singleton loggers
# instead of logctx.Or(ctx, ...). Run before committing service/gateway changes.
# Requires ripgrep: `choco install ripgrep` (Windows) / `brew install ripgrep` / `apt-get install ripgrep`.
./scripts/lint-logs.sh           # Linux/macOS
.\scripts\lint-logs.ps1          # Windows
```

## Architecture

Clean Architecture (Hexagonal / Ports & Adapters) with dependency injection via `uber/fx`.

```
cmd/                    # Entry points (server, migrate, seed-demo-key, hash-key, apply-schema)
internal/
  api/                  # HTTP layer (Gin router, middleware, handlers)
    v1/handlers/        # Request handlers (fair_value, health, auth, performance)
  config/               # Viper-based configuration loading + XBRL/industry/flag configs
  core/                 # Domain layer (no external dependencies)
    entities/           # Domain models (FinancialData, MarketData, MacroData, etc.)
    ports/              # Interface definitions (gateways, repositories, services)
  di/                   # Dependency injection container (fx modules)
  infra/                # Infrastructure adapters
    database/           # SQL schema
    gateways/           # External API clients
      sec/              # SEC EDGAR API client
      market/           # Yahoo Finance + Finzive market data
      macro/            # FRED macro data
    repositories/       # Data persistence
      sqlite/           # SQLite repository implementations
      cache/            # Redis + in-memory cache implementations
    resilience/         # Circuit breaker, retry policies
  services/             # Business logic services
    auth/               # API key authentication
    datacleaner/        # Financial data normalization pipeline
      adjustments/      # Asset, liability, earnings adjusters
      ai/               # AI-powered footnote analysis (optional)
      flagging/         # Risk flag detection
      industry/         # Industry classification
    datafetcher/        # Multi-source data coordination
    metrics/            # Prometheus metrics collection
    ratelimit/          # Rate limiting
    scheduler/          # Background job scheduler
    growth/             # Forward-looking growth estimation service
    valuation/          # Valuation orchestration engine
      models/           # Industry-specific models (DCF, DDM, FFO, Revenue Multiple)
    watchlist/          # Scheduler watchlist management
    alerting/           # Alert configuration
  observability/        # Cross-cutting logger plumbing (added Phase O of observability upgrade)
    logctx/             # Context-scoped logger inject/extract
    calclog/            # Calculation-stage trace emitter (gated by logging.trace_calculations)
  integration/          # Integration tests
pkg/
  finance/              # Shared financial calculation libraries
    dcf/                # DCF calculation
    growth/             # Growth rate estimation
    wacc/               # WACC calculation
    leases/             # Lease estimation
config/                 # Configuration files (YAML, JSON)
  datacleaner/          # Rules, industry codes, XBRL tag mappings, flag conditions
  alerting/             # Alert rules and notification channels
docs/                   # Documentation (OpenAPI spec, integration plans)
scripts/                # Build, deploy, and test scripts
data/                   # SQLite database files (gitignored)
```

## Key Conventions

### Code Style
- **No globals** - All state managed through DI container
- **Interface-first** - All external dependencies defined as interfaces in `internal/core/ports/`
- **Structured logging** - Use `go.uber.org/zap` exclusively, never `log` or `fmt.Println`
- **Request-path logs via `logctx.From(ctx)`** - Any log line emitted during an HTTP request must go through `internal/observability/logctx.From(ctx)` so it inherits `request_id` (and `user_id`/`key_id` post-auth). Reserve the fx-provided singleton `*zap.Logger` for startup, shutdown, scheduler, and other non-request contexts.
- **Error wrapping** - Use `fmt.Errorf("context: %w", err)` for error chains
- **Context propagation** - All service/repository methods accept `context.Context` as first parameter
- **RFC 7807** - Error responses follow Problem Details format

### Testing
- **TDD mandatory** - Write tests before implementation
- **Table-driven tests** - Use `[]struct{name string; ...}` pattern for test cases
- **Coverage target** - >= 90% for critical finance modules, >= 80% overall
- **Property-based testing** - Use `gopter` for financial calculation correctness
- **Test naming** - `TestServiceName_MethodName_Scenario`
- **Mocks** - Use `testify/mock` for interface mocking
- **Integration tests** - Located in `internal/integration/`, use `testcontainers-go` where needed

### Configuration
- Viper-based: reads from `config/config.yaml`, then env vars (e.g., `DATABASE_DRIVER`)
- Env var mapping: nested keys use `_` separator (`database.driver` -> `DATABASE_DRIVER`)
- Secrets: never committed; use env vars or vault references
- Feature flags: `SCHEDULER_ENABLED`, `DATACLEANER_ENABLE_AI_INTEGRATION`, `ENABLE_SWAGGER`, `ENABLE_PPROF`

### API Design
- All protected endpoints require `X-API-Key` header
- Permission-based access control (`read:fair_value`, `read:health`, `read:metrics`, `manage:keys`, `admin`)
- Rate limiting on all endpoints (API key-based or IP-based fallback)
- Security headers on all responses (HSTS, CSP, X-Frame-Options)

### Database
- SQLite3 (default) or PostgreSQL
- Schema in `internal/infra/database/schema.sql`
- Migrations via `cmd/migrate`
- Repository pattern for all data access

## Important Files

| File | Purpose |
|------|---------|
| `cmd/server/main.go` | Application entry point |
| `internal/di/container.go` | DI container wiring |
| `internal/api/server.go` | HTTP server, routes, middleware |
| `internal/config/config.go` | Configuration structs and loading |
| `internal/core/ports/gateways.go` | External service interfaces |
| `internal/core/ports/repositories.go` | Data storage interfaces |
| `internal/services/valuation/service.go` | DCF valuation orchestration |
| `internal/services/valuation/errors.go` | Sentinel errors (ErrTickerNotFound, ErrInsufficientData, ErrModelNotApplicable) |
| `internal/services/valuation/options.go` | ValuationOptions (override beta/risk-free rate) |
| `internal/services/valuation/models/router.go` | Industry-aware model selection (DDM, FFO, Revenue Multiple, DCF) |
| `internal/services/valuation/adr.go` | ADR ticker-to-country mapping + country risk premium lookup |
| `internal/services/valuation/crosscheck.go` | Sanity check: DCF-implied multiples vs sector medians |
| `internal/services/valuation/graham.go` | Graham-school asset-floor diagnostics (current_assets/share, NCAV/share, Graham buy-below floor, discount %). See `docs/refactoring/graham-floor-metrics-spec.md`. |
| `internal/services/growth/estimator.go` | Multi-stage growth estimation with analyst/historical blending |
| `internal/services/datafetcher/coordinator.go` | Multi-source data fetching |
| `internal/services/datacleaner/service.go` | Financial data normalization |
| `internal/services/datacleaner/industry/classifier.go` | Dual classifier: SIC-based `Classify` (model router) + balance-sheet `ClassifyIndustry` (cleaning rules) |
| `internal/services/datacleaner/industry/classifier_regressions_test.go` | AMD retail-misclassification regression pins (semi basket + sentinel branches) |
| `internal/api/v1/handlers/fair_value.go` | Fair-value handler; owns `FairValueResponse`, `Industry` struct, the canonical SIC→GICS mapping, and `BuildIndustryFromResult` (exported for replay tooling) |
| `cmd/replay/main.go` | Replay CLI (Phase 2.D): re-runs captured artifact bundles through the current valuation engine and diffs against the saved `17-response.json` |
| `internal/observability/replay/` | Replay core: bundle gateways (SEC/Market/Macro/YFinance), `replay.Module` fx composition with hand-picked providers (avoids transitive sqlx/redis), `Replay()` orchestrator, manifest-bound clock binding, schema/git drift detection |
| `internal/infra/gateways/macro/parser.go` | `ParseFREDSeries` pure function (extracted in Phase 2.D R2 Stage A.6) — used by both production `gateway.go` and `BundleMacroGateway` raw-mode |
| `internal/infra/gateways/market/yfinance_auth.go` | Yahoo Finance cookie+crumb auth manager |
| `internal/infra/database/schema.sql` | Database schema |
| `config/country_risk.json` | Damodaran country risk premiums (30+ countries) |
| `config/industry_multiples.json` | Sector median P/E, EV/EBITDA, EV/Revenue, P/FFO multiples |
| `config/datacleaner/industry_codes.json` | SIC/NAICS/keyword → industry-label mappings; source of truth for what `Classify` emits |
| `docs/openapi.yaml` | OpenAPI 3.0 specification |
| `docs/refactoring/valuation-engine-upgrade-spec.md` | Full upgrade spec: multi-stage growth, industry models, international |
| `docs/refactoring/industry-classification-unification-spec.md` | Planned SIC-only classification refactor (heuristic retirement) |
| `docs/superpowers/specs/2026-04-23-industry-in-response-design.md` | Design spec for the `industry` response field (dual SIC + heuristic with Match flag) |
| `docs/reviewer/` | Tracked follow-up items from code review (D1, D2, B2, W1-W5, S1-S5) |
| `config.env.example` | Environment variable template |

## External Data Sources

| Source | Purpose | Rate Limit |
|--------|---------|------------|
| SEC EDGAR (`data.sec.gov`) | Company financial filings (10-K, 10-Q) | 10 req/sec |
| Yahoo Finance (`query2.finance.yahoo.com`) | Market prices, beta, volume | Cookie+crumb auth, 3 retries |
| Finzive | Fallback market/financial data | Scraper (be polite) |
| FRED (`api.stlouisfed.org`) | Treasury rates, macro indicators | Requires API key |

## Common Gotchas

- Redis is **optional** - the app falls back to in-memory cache if Redis is unavailable
- Windows tests skip some E2E tests (gated by `E2E_LIVE=1`)
- SEC API requires a valid `User-Agent` header with contact email
- SEC EDGAR inconsistently serializes the `cik` field: numeric for some filers (e.g. AAPL: `320193`), zero-padded quoted string for others (e.g. XRTX: `"0001729214"`). Handled by `ports.FlexibleCIK` — do NOT change `SECCompanyFacts.CIK` back to `json.Number` or decode will fail for affected tickers
- **Foreign private issuers (20-F filers) — fully supported as of Phase B of the IFRS-FPI plan** (`docs/refactoring/ifrs-foreign-private-issuer-support-spec.md`). Tickers like TSM (Taiwan Semiconductor), ASML, NVO, AZN, SAP, BABA, BIDU, TM, RIO, BHP, NVS, SHEL, BP all produce real USD per-ADR fair values. The pipeline:
  1. `sec/parser.go` reads any ISO-4217 currency unit and IFRS-full taxonomy concepts; stamps `FinancialData.ReportingCurrency` with the source currency (e.g., TWD).
  2. `valuation/currency.go: convertFinancialsToUSD` FX-converts every monetary field via `MacroDataGateway.GetFXRate` (FRED daily series, falling back to `config/fx_rates.json` when FRED is unavailable). After this step `ReportingCurrency = "USD"`.
  3. `valuation/currency.go: applyADRRatio` divides ordinary-share counts by the configured ratio in `config/adr_ratios.json` (TSM=5, BABA=8, …). Yahoo's reported sharesOutstanding is cross-checked; >10% deviation logs a WARN.
  4. `FairValueResponse` carries `currency: "USD"`, `adr_ratio_applied: <ratio>`, and `current_price: <USD>` for transparency. `current_price` is the live per-share quote captured from Yahoo/Finzive at calculation time, in the same per-share basis as `dcf_value_per_share` (per-ADR for ADRs), so consumers can compute the `(dcf - price) / price` discount without a second quote lookup.

  **The `ports.ErrForeignPrivateIssuer` 422 still ships for**: (a) tickers using genuinely-unmapped taxonomies (JGAAP, K-IFRS, ifrs-smes), (b) currencies with no FRED series AND not present in `config/fx_rates.json`. Both are config-extensible — see `sec/parser.go: findValue` for taxonomy coverage and `internal/infra/gateways/macro/gateway.go: fredSeriesFor` for currency coverage.

  **For new ADR tickers**: verify the depositary ratio against the prospectus before adding to `config/adr_ratios.json` — a wrong ratio silently corrupts per-share values.

  **Distinct from `INSUFFICIENT_DATA`**: clinical-stage biotechs / pre-revenue US companies with `us-gaap` present but no Revenue/OperatingIncome → `HTTP 422 INSUFFICIENT_DATA` (wraps `ports.ErrCompanyFactsNotFound` → `valuation.ErrInsufficientData`, same path as a genuine SEC 404 from `sec/client.go`). The handler **must check FPI first** because it is the more specific case; falling through to INSUFFICIENT_DATA would mask the actionable diagnostic.
- SQLite driver is `github.com/mattn/go-sqlite3` (requires CGO)
- The `config.env.example` file is blocked by a pre-read hook; get config info from `internal/config/config.go` defaults instead
- **Two parallel industry classifiers today** — `Classify(sic, …)` drives the valuation model router and is the canonical label; `ClassifyIndustry(ticker, data)` is a balance-sheet heuristic that drives industry-specific datacleaner rules. They can disagree on the same ticker. The `FairValueResponse.Industry` field surfaces both plus a `match` flag. Do NOT refactor `getIndustryCode` in `datacleaner/service.go` to prefer SIC — that's tracked as the unification refactor in `docs/refactoring/industry-classification-unification-spec.md` and is out of scope for incidental changes
- **`sicToGICS` map in `fair_value.go` keys MUST match `config/datacleaner/industry_codes.json`** `code` fields one-to-one. The classifier emits labels like `FIN` (not `FINL`), plus sub-industry codes `TECH_SAAS`, `HEALTH_BIOTECH`, etc. A mismatch silently demotes entire sectors to `match: false`. Add new top-level labels to both the map and `matchSICToGICS`'s normalization logic simultaneously
- **Replay tooling (Phase 2.D R0+R1+R2) is hermetic by construction** — `cmd/replay` and `internal/observability/replay/` MUST NOT touch the production database, Redis cache, metrics shipper, scheduler, or external APIs. The `replay.Module` hand-picks `fx.Provide` lines rather than wrapping `di.CoreModule` precisely because CoreModule transitively pulls `*sqlx.DB` and `*redis.Client` constructors which would side-effect even when downstream consumers are decorated away. **Bundle gateways MUST return `replay.ErrBundleMissingPayload` (NOT panic)** on missing files because `internal/services/datafetcher/coordinator.go:181-196` runs gateway calls inside parallel goroutines under `sync.WaitGroup` — a child-goroutine panic would not be recovered by the replay binary. Auth/Watchlist stubs DO panic (different layer; not on the goroutine path). When adding a new replay surface, preserve both invariants
- **Graham-floor diagnostic fields (`current_assets_per_share`, `ncav_per_share`, `graham_floor_per_share`, `graham_discount_pct`)** are computed in `internal/services/valuation/graham.go` and stamped onto `ValuationResult` from BOTH the DCF path and the alt-model path in `service.go`. All four are **omitted from the JSON response** when `TotalLiabilities` cannot be resolved (see `resolveTotalLiabilities` fallback chain) — a warning string `"graham_floor: insufficient balance-sheet data..."` or `"graham_floor: derived total_liabilities from balance-sheet identity..."` is appended to `result.Warnings` instead. `graham_discount_pct` uses `*float64 + omitempty` deliberately: nil distinguishes "floor==0, ratio undefined" from `&0.0` (price exactly equals the floor). The derivation fallback (`TotalAssets − StockholdersEquity`) emits a WARN log naming the ticker so operators can correlate against the cleaner asymmetry tracked in `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`. Do NOT add a config flag to suppress these warnings — they are a load-bearing data-quality signal
