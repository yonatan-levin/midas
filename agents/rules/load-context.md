---
alwaysApply: true
---
# Load Context Skill (Smart Router)

When invoked with `@load-context {path}`, intelligently detect the type of path and load all relevant documentation and context.

## Purpose

Universal context loader that handles any path in the Midas Go monolithic codebase - services, internal layers, infrastructure, shared packages, testing, and more. Automatically detects the category and loads appropriate documentation.

## Supported Paths

### Monolithic Layers (Shorthand or Full Path)

| Input | Resolves To | Description |
|-------|-------------|-------------|
| `api` | `internal/api/` | HTTP Layer (Gin routes, middleware, handlers) |
| `core` | `internal/core/` | Domain layer (entities, interface ports) |
| `infra` | `internal/infra/` | Infrastructure adapters (DB, repositories, external gateways) |
| `di` | `internal/di/` | Dependency injection container (uber/fx) |
| `config` | `internal/config/` & `config/` | Viper configuration loading and static configs |
| `cmd` | `cmd/` | Entry points (server, migrations, seeds, utilities) |

### Internal Services

| Input | Resolves To | Description |
|-------|-------------|-------------|
| `valuation` | `internal/services/valuation/` | Core DCF valuation orchestration engine |
| `datacleaner`| `internal/services/datacleaner/` | Financial data normalization pipeline |
| `datafetcher`| `internal/services/datafetcher/` | Multi-source data fetching (SEC, Market, Macro) |
| `growth` | `internal/services/growth/` | Forward-looking growth estimation |
| `auth` | `internal/services/auth/` | API key authentication and management |
| `scheduler` | `internal/services/scheduler/` | Background job scheduler |

### Infrastructure Sub-Components

| Input | Resolves To | Description |
|-------|-------------|-------------|
| `sec` | `internal/infra/gateways/sec/` | SEC EDGAR API client |
| `market` | `internal/infra/gateways/market/`| Yahoo Finance / Finzive market data clients |
| `db` or `database` | `internal/infra/database/`| SQLite/PostgreSQL schema and setup |

### Packages & Shared Logic

| Input | Resolves To | Description |
|-------|-------------|-------------|
| `finance` | `pkg/finance/` | Shared financial calculation logic (DCF, WACC, growth) |
| `observability` | `internal/observability/`| Cross-cutting logger plumbing (`logctx`, `calclog`) |

### Infrastructure & Deploy

| Input | Resolves To | Description |
|-------|-------------|-------------|
| `docker` or `compose` | Root `docker-compose*.yml` | Docker Compose stack configuration |

### Testing & Scripts

| Input | Resolves To | Description |
|-------|-------------|-------------|
| `scripts` | `scripts/` | Build, deploy, testing, and utility scripts |
| `test` or `integration` | `internal/integration/` | Integration tests |

### Documentation

| Input | Resolves To | Description |
|-------|-------------|-------------|
| `docs` | `docs/` | Project docs (architecture, bugs, refactoring, superpowers) |

## Automatic Actions

### Step 1: Detect Category

Analyze the input path and determine category:

```
Input -> Category Detection:
+-- internal/api/* or api -> API
+-- internal/core/* or core -> CORE
+-- internal/infra/* or infra -> INFRA
+-- internal/services/* -> SERVICE
+-- internal/di/* or di -> DI
+-- pkg/finance/* or finance -> FINANCE
+-- docker or compose -> DOCKER
+-- scripts/* -> SCRIPTS
+-- internal/integration/* or integration -> TESTS
+-- docs/* -> DOCS
+-- (file path) -> SINGLE_FILE
```

### Step 2: Memory-Aware Context

Before loading docs, check memory:
1. `user-memory-search_nodes` for "{path} patterns"
2. `user-memory-search_nodes` for "recent {path} issues"
3. Include learnings in context

### Step 3: Load Documentation Based on Category

#### For SERVICE category (e.g., valuation, datacleaner):
Read using Read tool:
- `internal/services/{service}/*.go` - Check main service structs
- `internal/core/ports/` - Check related interfaces
- `internal/integration/` - Check related integration tests

#### For API category:
Read using Read tool:
- `internal/api/server.go` - Route mapping and middleware
- `internal/api/v1/handlers/*.go` - HTTP endpoints and DTOs
- `docs/openapi.yaml` (if needed for API spec)

#### For CORE category:
Read using Read tool:
- `internal/core/entities/*.go` - Domain models
- `internal/core/ports/*.go` - Interfaces (gateways, repositories, services)

#### For INFRA category:
Read using Read tool:
- `internal/infra/database/schema.sql` - Database schema
- `internal/infra/gateways/{gateway}/*.go` - API clients (sec, market, macro)
- `internal/infra/repositories/{impl}/*.go` - Repository implementation

#### For FINANCE category:
Read using Read tool:
- `pkg/finance/{package}/*.go` - Pure calculation logic (dcf, wacc, leases)

#### For DOCKER category:
Read using Read tool:
- `docker-compose.yml` - Main service stack
- `docker-compose.prod.yml` - Production stack overrides
- `Dockerfile`

#### For SCRIPTS category:
List all scripts in `scripts/` and describe their purpose based on filename.
Key scripts include: 
- `launch_staging.sh/.ps1`
- `contract_fuzz.ps1`
- `lint-logs.sh/.ps1`
- `load_tester.go`

#### For TESTS category:
Read using Read tool:
- `internal/integration/*.go` - Integration tests using testcontainers-go

#### For DOCS category:
List all documentation files in `docs/` and summarize topics.
Key checking areas:
- `docs/refactoring/`
- `docs/superpowers/specs/`
- `docs/bugs/`

#### For SINGLE_FILE category:
Read the specific file and provide context about its role in the codebase.

### Step 4: Store in Memory

Use `user-memory-create_entities` to store:
- Path loaded
- Category detected
- Key patterns discovered

## Required Output Format

```
## Context Loaded: {path}

### Category: {API | CORE | INFRA | SERVICE | DI | FINANCE | DOCKER | SCRIPTS | TESTS | DOCS | SINGLE_FILE}

### Overview
{brief description based on loaded documentation}

### Key Information
{category-specific information}

### Structure
{path}/
+-- {relevant files/folders}
+-- ...

### Related Memories
{any relevant past context from memory}

### Context Stored in Memory
```

## Composability

This skill can be chained with:
- `@preflight` - run before this for full task context
- `@research {topic}` - if unknowns were identified

## Shorthand Reference

Quick reference for common shortcuts:

| Shorthand | Full Path |
|-----------|-----------|
| `api` | `internal/api` |
| `core` | `internal/core` |
| `infra` | `internal/infra` |
| `valuation` | `internal/services/valuation` |
| `datacleaner` | `internal/services/datacleaner` |
| `datafetcher` | `internal/services/datafetcher` |
| `auth` | `internal/services/auth` |
| `finance` | `pkg/finance` |
| `di` | `internal/di` |
| `config` | `internal/config` and `config` |
| `docker` / `compose` | Root compose yml files |
| `scripts` | `scripts` |
| `docs` | `docs` |
