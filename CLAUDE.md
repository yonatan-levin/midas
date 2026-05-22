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

# Prometheus singleton-registerer lint guard (Phase 2.D R3a Stage I.0) — fails if
# replay-package code references prometheus.DefaultRegisterer (must use per-instance
# registries to keep replay binary hermetic under parallel --workers).
./scripts/lint-prometheus-registers.sh   # Linux/macOS
.\scripts\lint-prometheus-registers.ps1  # Windows

# Replay tooling (Phase 2.D — replays captured artifact bundles through current code
# and diffs against saved 17-response.json). All R0–R3 SHIPPED on master.

# Single-bundle replay (default --from=raw)
go run ./cmd/replay artifacts/2026-05-06/MXL/req_c01bec94-9c3c-46f6-afad-9458672c8534/

# Parallel batch replay across a watchlist of bundles (R3a)
go run ./cmd/replay --workers=4 --format=json artifacts/2026-04-25/

# Filter to a specific ticker or recent bundles only (R3a)
go run ./cmd/replay --filter-ticker=AAPL --filter-since=7d artifacts/

# Tunable float tolerances (R3a — defaults: rel 1e-9, abs 1e-12; --float-rel-tol=0
# means "use default", NOT exact-match)
go run ./cmd/replay --float-rel-tol=1e-6 --float-abs-tol=1e-9 artifacts/

# Per-stage diff detail (intermediate-stage drift inspection — R3b). Diffs the bundle's
# saved 10-clean-output.json / 12-growth-curve.json / 13-wacc.json / 15-valuation.json
# against what the engine produces today. --verbose renders per-stage diff sections
# beneath each bundle row.
go run ./cmd/replay --diff-stages --verbose --from=parsed artifacts/2026-05-06/MXL/req_c01bec94-9c3c-46f6-afad-9458672c8534/

# Regenerate JSON contract golden fixtures (R3b Stage M.1) — only run after a
# deliberate change to the JSON output shape. Then `git diff` to verify.
UPDATE_GOLDEN=1 go test -run TestRenderJSON_GoldenFixture ./internal/observability/replay/

# Performance benchmarks (R3b Stage N) — NF2 single-bundle ≤200ms / NF3 100-bundle
# batch ≤30s, both with 3× CI slack. Bench-gated; the synthetic 100-bundle corpus
# generator only fires when -bench is invoked, NOT on default `go test ./...`.
go test -bench=BenchmarkReplay -benchtime=10x ./internal/observability/replay/
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
| `internal/services/valuation/profile/` | **Tier 2 AssumptionProfile package** (P0a SHIPPED 2026-05-16): full type system — `AssumptionProfile` struct (14 fields) + 21 `Archetype` constants + 3 `Maturity` + `RevenueBaseMethod`/`TerminalMethod`/`DiscountMethod` enums + `ResolvedProfile.IsLegacyMatureLargeBankDDM()` predicate (gates the bit-for-bit DDM legacy path) + `Facts` DTO (pointer-field missing-vs-zero) + `ResolutionTrace`/`AssumptionProfileManifest` + `Registry` interface + `jsonRegistry` impl with SHA-256 `config_hash` + 9 load-time validation invariants (fail-loud on malformed shipped config; spec §4.4) + pure 3-stage `Resolve()` algorithm (industry-rule match → cyclical-trough override on negative OI → maturity bucketing → archetype-specific pin). Wildcard-matched rules report `Source = SourceFallback` per spec §3.3 intent. Import-boundary test (`import_boundary_test.go`) enforces zero imports of `models`/`entities`. Profile resolution fires in `service.go::performValuation` before `router.SelectModel` (wiring lands in P0b); resolved profile stamped onto `ModelInput.Profile`. |
| `internal/services/valuation/profile/testhelpers/` | Tier 2 cross-package test helpers consumed by P1-P4 worktrees: `BuildMXLModelInput`, `BuildSyntheticAAPLishModelInput`, `BuildSyntheticDataCenterREITInput`, `MustLoadFullFixture`, `LoadGoldenJPMPrimaryValue`, plus `t.Skip` stubs for service-level helpers that P2 wires up. |
| `internal/services/valuation/models/testdata/golden/` | Pre-Tier-2 DDM bit-for-bit golden fixtures (JPM/BAC/WFC × input+output). **DO NOT regenerate** without updating `TestDDM_LegacyPath_BitForBit` and the surrounding governance — these pin the load-bearing mature-large-bank DDM invariant. |
| `internal/services/valuation/models/ddm_bitforbit_test.go` | **LOAD-BEARING regression test** for Tier 2 — uses `math.Float64bits` equality on JPM/BAC/WFC `IntrinsicValuePerShare`/`EquityValue`/`EnterpriseValue` fields. Any Tier-2 commit that fails this test = revert; **never update goldens to make it pass**. |
| `artifacts/tier2-baseline/` | 10-ticker replay baseline captured pre-Tier-2 at master `0324057`. Used by `cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/` for per-PR regression validation. JPM bundle requires `--allow-schema-drift` due to missing `10-clean-output.json` (cleaner-side bug T2-BS-2). |
| `internal/services/growth/estimator.go` | Multi-stage growth estimation with analyst/historical blending |
| `internal/services/datafetcher/coordinator.go` | Multi-source data fetching |
| `internal/services/datacleaner/service.go` | Financial data normalization |
| `internal/services/datacleaner/industry/classifier.go` | Dual classifier: SIC-based `Classify` (model router) + balance-sheet `ClassifyIndustry` (cleaning rules) |
| `internal/services/datacleaner/industry/classifier_regressions_test.go` | AMD retail-misclassification regression pins (semi basket + sentinel branches) |
| `internal/api/v1/handlers/fair_value.go` | Fair-value handler; owns `FairValueResponse`, `Industry` struct, the canonical SIC→GICS mapping, and `BuildIndustryFromResult` (exported for replay tooling) |
| `cmd/replay/main.go` | Replay CLI (Phase 2.D): re-runs captured artifact bundles through the current valuation engine and diffs against the saved `17-response.json` |
| `internal/observability/replay/` | Replay core: bundle gateways (SEC/Market/Macro/YFinance), `replay.Module` fx composition with hand-picked providers (avoids transitive sqlx/redis), `Replay()` orchestrator, manifest-bound clock binding, schema/git drift detection |
| `internal/infra/gateways/macro/parser.go` | `ParseFREDSeries` pure function (extracted in Phase 2.D R2 Stage A.6) — used by both production `gateway.go` and `BundleMacroGateway` raw-mode |
| `internal/infra/gateways/sec/plugs.go` | **DC-1 Phase 0 SHIPPED 2026-05-16** (merge `1640394`): `computePlugs` pure helper that fills the 4 `Other*` plug fields on `FinancialData` at the end of `parsePeriodData` as residuals (`umbrella − sum(known_components)`, clamped to ≥0 with Debug log on negative residual). Phase 0 invariant: populated but **NO consumer reads them yet** — Phases 1-4 of DC-1 (component primitive shim → Adjuster interface + ledger → CleanedFinancialData view reconstruction → consumer migration) consume them. Spec: `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`. Plan: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-0-implementation-plan.md`. |
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
- **`sicToGICS` map in `fair_value.go` keys MUST match `config/datacleaner/industry_codes.json`** `code` fields one-to-one. The classifier emits labels like `FIN` (not `FINL`), plus sub-industry codes `TECH_SAAS`, `HEALTH_BIOTECH`, `FIN_BANK`, etc., and REIT subsector codes in `REIT_*` prefixed form (`REIT_DATACENTER`, `REIT_INDUSTRIAL`, `REIT_RETAIL`, `REIT_HEALTHCARE`, `REIT_RESIDENTIAL`, `REIT_OFFICE`, `REIT_CELLTOWER`, `REIT_SPECIALTY`) per T2-P4-W1 reconciliation (merge `be92a79` 2026-05-19). A mismatch silently demotes entire sectors to `match: false`. Add new top-level labels to both the map and `matchSICToGICS`'s normalization logic simultaneously. REIT subsectors get full-code exact-match entries in `sicToGICS` because `"REIT"` itself is NOT a key — parent-strip fallback can't catch them, so each subsector needs its own explicit entry
- **Replay tooling (Phase 2.D — ALL R0–R3 SHIPPED, COMPLETE as of merge `0741958` 2026-05-09) is hermetic by construction** — `cmd/replay` and `internal/observability/replay/` MUST NOT touch the production database, Redis cache, metrics shipper, scheduler, or external APIs. The `replay.Module` hand-picks `fx.Provide` lines rather than wrapping `di.CoreModule` precisely because CoreModule transitively pulls `*sqlx.DB` and `*redis.Client` constructors which would side-effect even when downstream consumers are decorated away. **Bundle gateways MUST return `replay.ErrBundleMissingPayload` (NOT panic)** on missing files because `internal/services/datafetcher/coordinator.go:181-196` runs gateway calls inside parallel goroutines under `sync.WaitGroup` — a child-goroutine panic would not be recovered by the replay binary. Auth/Watchlist stubs DO panic (different layer; not on the goroutine path). The `cmd/server` ↔ `replay` import-boundary CI guard at `cmd/server/import_boundary_test.go` (R3a Stage O.13) keeps Stage O.6's `init()` reflection panic confined to the replay binary. **Stage K's `--diff-stages` reads bundle JSON files via `os.ReadFile` directly** (rather than re-deriving from entities) so future struct-shape evolution doesn't break diff path. The "current" snapshot for diff comparison uses an ephemeral `os.MkdirTemp` bundle with `defer os.RemoveAll`; never persists (preserves D7 "no bundles of bundles" invariant). When adding a new replay surface, preserve all three invariants (F11 hermeticity, ErrBundleMissingPayload-not-panic, ephemeral-snapshot-only)
- **Graham-floor diagnostic fields (`current_assets_per_share`, `ncav_per_share`, `graham_floor_per_share`, `graham_discount_pct`)** are computed in `internal/services/valuation/graham.go` and stamped onto `ValuationResult` from BOTH the DCF path and the alt-model path in `service.go`. All four are **omitted from the JSON response** when `TotalLiabilities` cannot be resolved (see `resolveTotalLiabilities` fallback chain) — a warning string `"graham_floor: insufficient balance-sheet data..."` or `"graham_floor: derived total_liabilities from balance-sheet identity..."` is appended to `result.Warnings` instead. `graham_discount_pct` uses `*float64 + omitempty` deliberately: nil distinguishes "floor==0, ratio undefined" from `&0.0` (price exactly equals the floor). The derivation fallback (`TotalAssets − StockholdersEquity`) emits a WARN log naming the ticker so operators can correlate against the cleaner asymmetry tracked in `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md`. Do NOT add a config flag to suppress these warnings — they are a load-bearing data-quality signal
- **`tangible_value_per_share` denominator changed from market-basic to diluted shares in v0.10.0 (Graham floor metrics PR #2).** Cached pre-v0.10.0 values may be ~2-5% higher; expect drift on first recompute. Priority chain mirrors DCF: diluted → market.basic → financial.basic. See `calculateTangibleValuePerShare` in `internal/services/valuation/service.go` and the regression pin in `service_test.go: TestService_calculateTangibleValuePerShare_DilutedDenominator`
- **DC-1 datacleaner refactor — Phase 0 SHIPPED 2026-05-16, merge `1640394`.** The 4 plug fields on `FinancialData` (`OtherCurrentAssets`, `OtherNonCurrentAssets`, `OtherCurrentLiabilities`, `OtherNonCurrentLiabilities`) are populated by the SEC parser via `computePlugs` at `internal/infra/gateways/sec/plugs.go` but **no consumer reads them yet** as of Phase 0. Zero behavior change in DCF / WACC / Graham / EV-bridge outputs — empirically replay-verified on AAPL + MSFT. **Important corollary**: today's parser only populates the umbrella `OperatingLeaseLiability` (not the `*.Current` / `*.Noncurrent` split fields — they're fallbacks for the umbrella), so in production `OtherCurrentLiabilities` absorbs the entire CurrentLiabilities umbrella and `OtherNonCurrentLiabilities` absorbs everything except `TotalDebt`. Lease-split decomposition is deferred to Phase 1+. Math invariants in the spec hold by construction regardless. The Graham-floor diagnostic (above) cites DC-1 as the cleaner-asymmetry tracker; that asymmetry is what Phases 1-4 close. Phases 2-4 (Adjuster interface + AdjustmentLedger, CleanedFinancialData with `AsReported`/`Restated`/`InvestedCapital` views, consumer migration) follow per `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`.
- **DC-1 datacleaner refactor — Phase 1 SHIPPED 2026-05-19, merge `2d916a7`.** `internal/services/datacleaner/recompute.go::recomputeUmbrellas` runs at the end of the cleaner pipeline (between `createRiskWarningFlags` and `calculateQualityScore`) as a shadow-mode observer — emits WARN lines tagged `"phase":"DC-1-P1-shadow"` for every umbrella divergence, but does NOT mutate `*FinancialData` (the load-bearing invariant is pinned by `TestRecomputeUmbrellas_NoMutation`'s `reflect.DeepEqual` snapshot). Phase 2's `Adjuster` interface refactor consumes the divergence enumeration captured in `internal/integration/testdata/recompute-shadow/<TICKER>.json` — see the shadow-analysis report at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md` for the 7-cluster Phase 2 punch list. To grep production logs: `rg '"phase":"DC-1-P1-shadow"'`. To re-derive the shadow analysis locally: `go test ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1`.
  - **Phase 2 PR-1 SHIPPED 2026-05-21 (branch `dc1-phase-2-pr-1-clean`).** Introduces `Adjuster` interface at `internal/services/datacleaner/adjustments/adjuster.go` and the `LedgerEntry`/`OverlaySpec`/`AdjustmentLedger`/`AmountSemantics`/`AIProvenance` entities at `internal/core/entities/adjustment_ledger.go`. Appends `AdjustmentLedger []LedgerEntry` and `Overlays []OverlaySpec` fields onto `entities.FinancialData`. Adds an orchestrator scaffolding shim at `service.go::applyActiveAdjustments` that mechanically maps the legacy `entities.Adjustment` shape to `LedgerEntry` records after each `Process*Adjustments` call; the shim's three contiguous branches (assets / liabilities / earnings) are deletion order — PR-2 deletes the asset branch when A-rules implement `Adjuster.Apply` natively, PR-3 deletes earnings, PR-4 deletes liabilities. **PR-1 invariant: opt-in observability only — no production consumer reads `data.AdjustmentLedger` or `data.Overlays`** (matches Phase 0 plug-field discipline). The existing dual-write mutations (`data.TotalAssets -= X`, `data.TotalDebt += Y`, etc.) remain unchanged; downstream DCF / WACC / Graham / EV-bridge outputs are bit-for-bit unchanged in PR-1. Phase 3 introduces `CleanedFinancialData.Restated()`/`.InvestedCapital()` accessors as the first ledger/overlay consumers. **`recomputeUmbrellas` WARN line now additively carries `recent_adjusters: []string`** (last 5 AdjusterIDs from `fd.AdjustmentLedger`) per Q1 resolution 2026-05-21 — semantics unchanged, only an additive structured field. The load-bearing `TestRecomputeUmbrellas_NoMutation` invariant is preserved (the helper `lastNAdjusterIDs` is a pure slice-reader; no fd write). Plan: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`.
  - **Phase 2 PR-3 SHIPPED 2026-05-22 (branch `dc1-phase-2-pr-3`, pending merge — stacked on PR-2).** All 7 Category C earnings adjusters now implement the `Adjuster` interface natively, and the earnings-side branch of PR-1's `shimLedgerEntriesFromLegacyExcluding` shim is deleted. **8 commits** on branch `dc1-phase-2-pr-3` (stacked on PR-2 tip `2e8f83b`): Task 3.1 C1 restructuring_charges (Restater) `b1af6b1`; Task 3.2 C2 asset_sale_gains (Restater) `e621320`; Task 3.3 C3 litigation_settlements (Restater) `988a371`; Task 3.5 C5 derivative_gains_losses (Restater, branch-divergent sign) `5654464`; Task 3.6 C6 capitalized_interest (Restater, EquityOffset=0 special case) `5610d51`; Task 3.4 C4 stock_compensation (FlagEmitter — plan-vs-code disagreement, see below) `79b78bd`; Task 3.7 C7 working_capital_window_dressing (FlagEmitter) `75afa8b`; Task 3.8 delete earnings-side shim branch `4af3c33`. **Two role flavors used (already locked by PR-2):** **(1) Restater** — C1 `LedgerEntry{Component:"NormalizedOperatingIncome", DeltaAmount:+restructuringAmount, EquityOffset:+restructuringAmount}`; C2 same shape with `-data.AssetSaleGains`; C3 same shape with `+data.LitigationSettlements`; C5 `Component:"NormalizedOperatingIncome"` with branch-divergent signed `DeltaAmount = -DerivativeGainsLosses` (gain-path NEGATIVE, loss-path POSITIVE — `adjustmentAmount` carries the sign through both legacy mutation sites at `:313/:316`; ONE LedgerEntry per fire); C6 `LedgerEntry{Component:"InterestExpense", DeltaAmount:+CapitalizedInterest, EquityOffset:0, Type:Reclassify}` — **LOAD-BEARING special case**: capitalized interest reclassifies between income-statement line items (operating expense → interest expense), does NOT flow to retained earnings, so Phase 3's `Restated()` accessor MUST NOT add C6's DeltaAmount to equity. **(2) FlagEmitter convention** — C4 stock_compensation emits `Fired:false` LedgerEntry on every path with the populated `AdjusterOutput.Flags` slice (dilution flag) as the firing signal, plus an `entities.Adjustment{Type:Reclassify}` from the legacy translator; C7 working_capital_window_dressing emits `Fired:false` LedgerEntry + WC Flag on fire — no balance-sheet mutation in either case. **C4 plan-vs-code disagreement (documented & resolved):** The Phase 2 implementation plan §7 Task 3.1-3.7 row described C4 as "same pattern as C1" implying Restater. The actual legacy code at `ProcessStockCompensationAdjustment` (`earnings.go:244-295`) does NOT mutate `data.NormalizedOperatingIncome` — it only emits an `entities.Adjustment{Type:Reclassify, FromAccount:"StockBasedCompensation", ToAccount:"OperatingExpenses"}` and a dilution Flag. Trust-the-code precedent (set by PR-2 Task 2.5's flag-only reviews) won: C4 ships as FlagEmitter. The PR-3 handoff doc TL;DR pre-flagged this, and the C4 commit body documents the deviation explicitly so REVIEWER knows the call was deliberate. **Canonical Phase 2 pattern preserved unchanged across all 7 C-rule migrations:** mutation-FREE `Apply*` methods on `EarningsAdjuster` (read `working`, return `AdjusterOutput`); the dispatcher `ProcessEarningsAdjustments` switch in `earnings.go` owns the dual-write (capture original* → call Apply* → translate to legacy `*AdjustmentResult` via per-rule translator → mutate `data.NormalizedOperatingIncome ±=` or `data.InterestExpense +=` → drain `AdjusterOutput.LedgerEntries`/`Overlays`/`Flags` into result's `Native*` slices + add the RuleID to `NativelyEmittedRuleIDs`). C5 dispatcher reads the SIGNED `DeltaAmount` off the native LedgerEntry directly because the translator absolute-magnitudes for the legacy `Adjustment.Amount` field — so branch-identity sign would be lost via the translator path. **Earnings-side legacy shim branch DELETED (Task 3.8 mirrors PR-2's asset-side deletion at Task 2.6).** Native C-rule `LedgerEntries`/`Overlays` continue to land on `data.AdjustmentLedger`/`data.Overlays` via the `NativeLedgerEntries`/`NativeOverlays` append immediately above the deleted block, preserving the load-bearing asset → liability → earnings ordering invariant pinned by `TestOrchestrator_LedgerOrdering`. **Liability-side shim branch + the helpers `shimLedgerEntriesFromLegacy` and `shimLedgerEntriesFromLegacyExcluding` are PRESERVED** for PR-4 (which deletes them alongside B-rule migration). **`CurrentSchemaVersions["FinancialData"]` stays at 8** — PR-3 doesn't bump because it ships the SAME structural envelope PR-2 introduced (LedgerEntries + Overlays + Flags); PR-3 just populates more of it. **Predicted snapshot drift = ZERO for PR-3** (per implementation plan §4 row C — earnings adjusters touch income-statement fields not visible to `recomputeUmbrellas`'s balance-sheet umbrella math). Empirically confirmed: shadow snapshots at `internal/integration/testdata/recompute-shadow/<TICKER>.json` byte-identical across all 8 PR-3 commits (`git diff --quiet` exit 0). **Load-bearing invariants stayed GREEN throughout all 8 PR-3 commits:** `TestDDM_LegacyPath_BitForBit` (jpm/bac/wfc — trivially preserved; DDM doesn't read earnings-normalization fields), `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering` (asset → liability → earnings partition; C-rule entries appear in the earnings partition), `TestDataCleanerRecompute_ShadowMode_TickerBasket` + shadow-snapshot byte-identity. **Translator-string discipline (PR-2 Task 2.1 NIT carry-through):** C3/C5/C6 add a `Revenue > 0` percentage guard that diverges from legacy's NaN/+Inf on Revenue=0 tickers — an intentional defensive guard, documented in those commits; otherwise legacy `Reasoning` strings are preserved byte-identically across the C-rule migrations where practical. **PR-4 (B1/B2/B3 liability adjusters) is next** — the highest-risk PR in the Phase 2 stack because B3 routes to `OverlaySpec.Field:"DebtLikeClaims"` (NOT `TotalDebt`) per the substantive accuracy correction in the spec, B3's `AIProvenance` capture (empty hashes per Q4), and the orchestrator-level `data.TotalDebt += result.Amount` absorption at `liabilities.go:87-88` (Option α — keep wrapper, move mutation into wrapper). PR-4 also deletes the liability-side shim branch AND both shim helpers AND adds the basket snapshot integration test (Task 4.6) AND lands the T2-BS-3 disposition documentation (Task 4.7). Then Phase 3 (view reconstruction) and Phase 4 (13-site consumer migration). Plan: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`. PR-4 handoff: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-pr-4-handoff.md`.
  - **Phase 2 PR-2 SHIPPED 2026-05-22 (branch `dc1-phase-2-pr-2`, pending merge).** All 6 Category A adjusters now implement the `Adjuster` interface natively, and the asset-side branch of PR-1's `shimLedgerEntriesFromLegacyExcluding` shim is deleted. Four role flavors emerged: **(1) OverlayEmitter** — A1 goodwill_exclusion at `internal/services/datacleaner/adjustments/assets.go` emits `OverlaySpec{Field:"TotalAssets", Operation:"subtract", Amount:originalGoodwill}` with empty `Component`/`DeltaAmount`/`EquityOffset` on its LedgerEntry; **(2) Restater** — A2 intangible_writedown emits `LedgerEntry{Component:"OtherIntangibles", DeltaAmount:-writedown, EquityOffset:-writedown}`; A4 deferred_tax_assets emits `LedgerEntry{Component:"DeferredTaxAssets", DeltaAmount:-allowance, EquityOffset:-allowance}` with TaxShieldDTA=0 (A4 IS the DTA reduction itself, separate shield would double-count); **(3) Restater + TaxShieldDTA** — A5 obsolete_inventory emits `LedgerEntry{Component:"Inventory", DeltaAmount:-writedown, EquityOffset:-writedown, TaxShieldDTA: writedown × working.EffectiveTaxRate}` when EffectiveTaxRate > 0 (else TaxShieldDTA=0); A5 is the first PR-2 adjuster to populate TaxShieldDTA; **(4) FlagEmitter convention ("Fired:false-with-Flags")** — the 2 flag-only reviews `RDCapitalizationReview` and `CapitalizedSoftwareReview` never mutate the balance sheet, so every emitted LedgerEntry carries `Fired:false` at all times; the populated `AdjusterOutput.Flags` slice IS the firing signal. **Canonical pattern (locked across all 6 migrations):** `Apply*` methods on `AssetAdjuster` are mutation-FREE pure functions (read `working`, return `AdjusterOutput`); the dispatcher `ProcessAssetAdjustments` switch in `assets.go` owns the dual-write (capture original* → call Apply* → translate to legacy `*AdjustmentResult` via per-rule translator → mutate `data.X` → drain `AdjusterOutput.LedgerEntries`/`Overlays`/`Flags` into result's `Native*` slices + add the RuleID to `NativelyEmittedRuleIDs`). Phase 3 deletes the dual-write at the single dispatcher call site, not at N adjuster Apply methods. **`CurrentSchemaVersions["FinancialData"]` bumped 7→8 atomic with Task 2.1** (`internal/observability/replay/schema.go`) per the `feedback_schema_version_atomic_bump` MEMORY rule — the bump fires in the FIRST POPULATING PR so replay drift stays diagnostic. Baseline refresh of `artifacts/tier2-baseline/2026-05-19/` deferred to a follow-up commit (requires live API access); replay against current code now reports `schema:FinancialData bundle=7 current=8` as the intended diagnostic signal. **Per-rule translators** (`a1AdjusterOutputToLegacyResult`, `a2AdjusterOutputToLegacyResult`, `a4AdjusterOutputToLegacyResult`, `a5AdjusterOutputToLegacyResult`, plus 2 flag-only translators) exist by design; extraction to a generic helper was deferred (per-rule structure justified by role differences — Restater reads `LedgerEntry.DeltaAmount` magnitude, OverlayEmitter reads `OverlaySpec.Amount`, flag-only translators always return `Applied:false`). **A-FY-NULL investigation closed at HIGH-confidence "not a bug for Phase 2"** — the Phase 1 shadow-analysis Cluster A-FY-NULL finding (FY periods of AMD/F/KO/MXL don't emit the paired CA-down/TA-up fingerprint produced on Qx periods) is rooted in SEC XBRL's `fp=FY` Revenue facts carrying ANNUAL magnitudes while `fp=Qx` carries QUARTERLY, so `InventoryTurnover` lands ~4× higher on FY than Qx, and A5's `detectInventoryObsolescence` threshold (<3.0) is implicitly quarterly-tuned. Tracker `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` is OPEN until a Phase 4+ heuristic-review subtask titled "FY-aware annualization for quarterly-tuned heuristics" addresses it. **Q-resolution status carried through:** Q1 (recompute WARN `recent_adjusters` enrichment) SHIPPED in PR-1; Q2 (A2 TaxShieldDTA) DEFERRED to Phase 3 to preserve A2's dual-write bit-for-bit contract — pinned by `TestA2IntangibleAdjuster_Adjuster_Interface_Contract/fired_path_TaxShieldDTA_stays_zero_per_Q2_deferral`; Q3 (A-FY-NULL tracker) SHIPPED in PR-2 Task 2.7; Q4 (AIProvenance hash fields) DEFERRED to Phase 3. **Code-quality NIT to acknowledge:** the A1 fired-path `Adjustment.Reasoning` string drifted slightly from legacy ("byte-identical" claim in Task 2.1's commit message was over-stated; the drift is intentional improvement that adds the amount value but the commit message overpromised). A2/A4/A5/flag-only reviews preserve legacy Reasoning strings byte-identically. No consumer is broken; documented here for transparency. **What stays untouched in PR-2:** `internal/services/datacleaner/recompute.go` (Phase 1's regression sentinel); `internal/services/valuation/*` (Tier 2 territory); `internal/infra/gateways/sec/parser.go` (T2-BS-3 stays as Option B carve-out); `liabilities.go` + `earnings.go` (PR-4 + PR-3). PR-3 (earnings adjusters C1-C7) is next, then PR-4 (liability adjusters B1/B2/B3 + final shim deletion + T2-BS-3 docs + CLAUDE.md Phase 2 closing gotcha). Plan: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`. PR-3 handoff: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-pr-3-handoff.md`.
- **Tier 2 bit-for-bit DDM invariant is LOAD-BEARING.** `TestDDM_LegacyPath_BitForBit` (in `internal/services/valuation/models/ddm_bitforbit_test.go`) asserts `math.Float64bits` equality on JPM/BAC/WFC DDM outputs against pre-Tier-2 goldens at `testdata/golden/{jpm,bac,wfc}_ddm_pre_tier2_{input,output}.json`. **Any change to mature-large-bank DDM math that fails this test must be REVERTED — do NOT update the goldens to make it pass.** The fixtures use patched DPS values (JPM=$4.80, BAC=$1.00, WFC=$1.40) from public-record FY2024 data because the production cleaner emits `DividendsPerShare=0` for FIN-prefix tickers (pre-existing bug T2-BS-1). Regenerating goldens via `go test -tags goldencapture` ONLY regenerates outputs from existing inputs — inputs themselves must NOT be re-derived from a live bundle (would produce DPS=0 and silently break the invariant). The `artifacts/tier2-baseline/` directory contains the 10-ticker replay baseline used by `cmd/replay --diff-stages` for cross-Tier-2-phase regression validation.
- **T2-P4-W1 classifier prefix reconciliation SHIPPED 2026-05-19** (merge `be92a79`): classifier now emits `REIT_*` prefixed subsector codes (REIT_DATACENTER, REIT_INDUSTRIAL, REIT_RETAIL, REIT_HEALTHCARE, REIT_RESIDENTIAL, REIT_OFFICE, REIT_CELLTOWER, REIT_SPECIALTY) so Tier 2 archetype rules in `config/assumption_profiles.json` will fire against real REIT requests once P4 merges. Pre-fix the classifier emitted bare codes (DATA_CENTER, INDUSTRIAL, RETAIL_REIT, …) while archetype rules used the prefixed convention — every REIT subsector would have fallen through to the `software_like_scaling:standard_growth` wildcard fallback after P4 merge. Fix is config-driven: rename in `config/datacleaner/industry_codes.json` flows directly to classifier emission. Downstream consumers updated atomically: `config/industry_multiples.json` keys (v1.3.0), `models/router.go::reitIndustrySet` + defensive `strings.HasPrefix("REIT_")` fallback in `isREITIndustry`, FFO subsector tables (longest-prefix-match unchanged), `handlers/fair_value.go::sicToGICS` (full-code exact-match — `"REIT"` is not a key). FIN side unchanged on master: classifier emits `FIN_INSURANCE` + `FIN_BANK` (no large/small split); `FIN_BANK` matches existing `fin_generic` (FIN prefix) → `mature_large_bank` archetype → preserves JPM bit-for-bit. P3 must coordinate when introducing `fin_small_bank`/`fin_large_bank` rules (3 options enumerated in tracker). Tracker `docs/reviewer/T2-P4-W1-classifier-prefix-mismatch.md` stays OPEN until Tier 2 Closeout validates the deferred acceptance rows (live API regression on EQIX+PLD + replay regression on `artifacts/tier2-baseline/` both need P4 merged to exercise REIT-specific rules end-to-end). **P1-P4 worktrees still pending rebase + merge onto fixed master** — see `git worktree list` for branches `tier2-p1`, `tier2-p2`, `tier2-p3`, `tier2-p4`.
