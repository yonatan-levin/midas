# 📋 **TODO TASKS CATALOG**
**DCF Valuation API - Comprehensive Task List**

**Project**: DCF Valuation API (Go)  
**Catalog Date**: January 19, 2025  
**Last Verified**: June 9, 2026 — **TODO burn-down COMPLETE** (TDB-1…TDB-12 / GitHub #1–#12 all shipped + closed; see §2026-06-08/09 — TDB BURN-DOWN COMPLETE near the top)  
**Purpose**: Complete inventory of all TODO comments and pending tasks  

---

> # ✅ CURRENT STATUS (2026-06-09): ALL CATALOGUED TODOs ARE CLOSED
> **Read this — not the snapshot tables below.** Every item this catalog tracked has been resolved.
> The sections that follow (including the **2026-06-05** and **2026-06-06** verdict tables that still
> read "OPEN" / "PARTIAL") are a **chronological work-in-progress LOG**, kept oldest-first for
> provenance. Those OPEN/PARTIAL markers are **historical** and now carry inline `→ ✅ TDB-N`
> resolutions in the verdict column.
>
> **The only work that remains — and it was NEVER part of this catalog's burn-down:**
> - a small **test-infra backlog** (broader integration tests, edge-case/error tests, E2E
>   testcontainers, k6 perf baseline) — tracked in `docs/THESIS.md` → "Next Candidate Work";
> - **GitHub #13** — the Postgres driver isn't wired (`DATABASE_DRIVER=postgres` doesn't boot).
>
> Authoritative roll-up: jump to **§2026-06-08/09 — TDB BURN-DOWN COMPLETE** below, or read
> `docs/reviewer/todo-burndown-final-closeout.md`.

---

## 🎯 **EXECUTIVE SUMMARY**

This document catalogs all TODO comments found throughout the codebase, organized by priority and implementation phase. These tasks represent technical debt, feature enhancements, and architectural improvements needed for production readiness.

**Total TODO Items**: 35 identified (updated from 32)  
**High Priority**: 8 items  
**Medium Priority**: 18 items (updated from 15)  
**Low Priority**: 9 items  

> ⚠️ The counts above are the original January-2025 tally and are **superseded** by the
> 2026-06-05 verification pass below. The HIGH-priority block was already fully completed;
> the live-code re-check reclassified several MEDIUM/LOW items as **DONE** or **PARTIAL**.

---

## 🕘 **2026-06-05 VERIFICATION PASS — HISTORICAL SNAPSHOT** (every OPEN/PARTIAL below was since CLOSED — see the `→ ✅ TDB-N` in each verdict)

Every pending item was re-checked against the live codebase (post DC-1 Phases 0–5 datacleaner
refactor, engine `CalculationVersion 4.4`). Legend: **DONE** = shipped; **PARTIAL** = infra
shipped but a residual TODO remains; **OPEN** = TODO still present (line numbers refreshed).
**(Snapshot as of 2026-06-05 — the `→ ✅ TDB-N` suffixes in the Verdict column record how each was later closed.)**

| Item | Verdict | Evidence (2026-06-05) |
|------|---------|-----------------------|
| Context Management — "use proper context from caller" | **DONE** | DC-1 Phase 3 followup threaded `ctx`; godoc at `adjustments/liabilities.go:524-529` documents the retired `context.Background()` TODO |
| Test Data Enhancement — problematic patterns / contingent liabilities | **DONE** | `createTestProblematicFinancialData` + `createTestRiskyFinancialData` at `datacleaner/service_test.go:487+` (contingent, litigation, restructuring, derivatives, missing fields) |
| API docs — fair-value endpoint | **DONE** | swagger `@Summary`/`@Router` at `handlers/fair_value.go:287,303,501,514` |
| Migration + seed *tooling* | **DONE** | `cmd/migrate/main.go` + `cmd/seed-demo-key/main.go` both exist |
| AI footnote analysis | **PARTIAL → ✅ TDB-3** | B3 AI path shipped (DC-1 Phase 3); TDB-3 made the AI-*failed* fallback use the industry heuristic (not flat 0.40) — the heuristic is the deliberate fallback, not a gap |
| Adjuster test coverage (A3 / A6 / A7) | **PARTIAL → ✅ TDB-2** | A3 already tested; A6 (ROU) + A7 (excess-cash) adjusters implemented + tested by TDB-2. (Broader integration/edge-case tests = the test-infra backlog, NOT a burn-down item) |
| `launch_staging.sh` migration/seed wiring | **OPEN → ✅ DONE 2026-06-06** | wired `go run ./cmd/migrate` + `./cmd/seed-demo-key` |
| Financial Data Extraction (9 sites) | **OPEN → ✅ TDB-7 + TDB-1** | the host `applyRule` chain was dead code (deleted by TDB-7); the real extraction now lives in the SEC parser (TDB-1) |
| Company Size Classification | **OPEN → ✅ TDB-7** | dead code (producer-only, zero consumers) — deleted |
| Industry Mapping Expansion | **OPEN → ✅ TDB-9** | documented defer (classifier emits only `{45,20,25}`; bare TODO → tracked reference) |
| Generic Rule Implementation (×2) | **OPEN → ✅ TDB-7** | inside the deleted `applyRule` chain (zero callers) |
| API docs — health / performance / server | **OPEN → ✅ DONE 2026-06-06** | swaggo on health/metrics; perf handler deleted (dead code); server-info already present |
| Inventory turnover analysis | **OPEN → ✅ TDB-8** | turnover refines the obsolescence-flag severity |
| Monitoring & Observability (×2) | **OPEN → ✅ TDB-4** | `datacleaner_adjustments_total` counter + `trace.datacleaner.adjustment` audit log |
| Configuration System (thresholds / source) | **OPEN → ✅ TDB-5** | 9 asset gates externalized to `config/datacleaner/adjustment_thresholds.json` (defaults==constants) |
| Cloud deployment config variables | **OPEN → ✅ TDB-6** | Docker Compose prod env template + `docs/operations/` runbook |

---

## 🕘 **2026-06-06 EXECUTION & INVESTIGATION PASS — HISTORICAL** (superseded by the 2026-06-08/09 burn-down below)

Acted on the 2026-06-05 OPEN/PARTIAL items (this section **supersedes** the matching rows above).
**Engine is now `CalculationVersion 4.6`** — it moved again since 2026-06-05, reinforcing that
2025-era TODOs must be re-validated, not blindly executed. Work done in worktree
`worktree-api-docs-swagger`.

### Executed (shipped this pass)
| Item | Action | Evidence |
|------|--------|----------|
| API docs — health & metrics | **DONE** | swaggo annotations on `DetailedHealthCheck` (`/api/v1/health/detailed`) + `GetMetrics` (`/api/v1/metrics`); `docs/swagger.{json,yaml}` regenerated (additive) |
| API docs — server entry point | **ALREADY DONE** | general `@title`/`@BasePath`/`@securityDefinitions` block already at `cmd/server/main.go:25-42` |
| API docs — performance dashboard | **REMOVED (dead code)** | `PerformanceHandler` was wired to no route (only its own integration test registered routes); deleted `handlers/performance.go`, `handlers/performance_test.go`, `integration/performance_monitoring_test.go` |
| `launch_staging.sh` migrate/seed wiring | **DONE** | wired `go run ./cmd/migrate` + `./cmd/seed-demo-key` (DB_PATH-overridable) |

### Investigated → CLOSED (TODO obsolete; the project moved past it)
| Item | Verdict | Finding |
|------|---------|---------|
| Financial Data Extraction (9 sites) | **CLOSED — dead code** | The `applyRule`→`apply{Exclusion,Writedown,Reclassify,TreatAsDebt,Flag}Rule` chain (`service.go:712-1047`) has **zero callers** (`nolint:unused`). 7/9 values are now extracted by DC-1 adjusters (A4 DTA, B1 lease, B2 pension, C1/C2/C3/C6). Genuine residue re-filed below. |
| Company Size Classification | **CLOSED — obsolete/orphaned** | `getCompanySize` (`service.go:1160`) is producer-only: stamped at `service.go:164`, read by **zero** production consumers. Market cap isn't available to the datacleaner (`entities.MarketData.MarketCap`, not `FinancialData`); profile `Maturity` is revenue-based and independent. |

### Re-filed — the real, narrowly-scoped residue (NOT the obsolete TODOs above)
- **R1 — Parser gap (HIGH): ✅ DONE 2026-06-06 (TDB-1 / issue #1, branch `worktree-tdb-1-parser-extraction`).** `parsePeriodData` now populates `RestructuringCharges` / `LitigationSettlements` / `CapitalizedInterest` via `findValue` us-gaap + ifrs-full candidate lists, in reporting currency, normalized to positive add-backs (`math.Abs` — e.g. JNJ tags `LitigationSettlementExpense` as −379M). `GainLossRelatedToLitigationSettlement` and the cash-flow `InterestPaidCapitalized` deliberately excluded (inverted-gain / wrong-measure). C1 now uses the real value (not the 1.5% guess); C3/C6 fire. Full suite green; shadow byte-identical. Operator live-replay verification deferred (non-blocking).
- **R2 — Missing adjusters (MEDIUM):** config rules `right_of_use_assets` (A6) and `excess_cash` (A7) are `enabled:true` but have no adjuster; the asset dispatcher silently skips them (`adjustments/assets.go` `default: continue`). Implement A6/A7 or remove the dangling rules.
- **R3 — Dead-code cleanup (LOW): ✅ DONE 2026-06-06 (TDB-7 / issue #7, branch `worktree-tdb-7-dead-code-cleanup`).** Deleted the unreferenced `applyRule` chain (~354 lines incl. the 5 `apply*Rule` helpers), the orphaned `getCompanySize` + `CleaningContext.CompanySize` field + `entities.CompanySize` type/enum + dead `company_size_classification` flag rule (and its now-orphaned `high/mid_revenue_threshold` vars) + unpopulated `profile.Facts.MarketCap`, and `alerting.IntegrationService` (+ its test). Zero behavior change; full suite green (47/47); invariants byte-identical.

---

## 🏁 **2026-06-08/09 — TDB BURN-DOWN COMPLETE** (supersedes ALL remaining OPEN/PARTIAL rows above)

All twelve prioritized backlog issues (**TDB-1 … TDB-12 / GitHub #1–#12**) are SHIPPED to local
master (`3d6cadf`, not pushed) and CLOSED on GitHub. Engine is now `CalculationVersion 4.7` (Layer A).
Per-item trackers archived under `docs/reviewer/archive/TDB-*.md`. Full roll-up:
`docs/reviewer/todo-burndown-final-closeout.md`.

| Issue | Catalog row it closes | Disposition | Merge |
|---|---|---|---|
| **TDB-1** | R1 — parser nonrecurring extraction (HIGH) | DONE | `21fb60f` |
| **TDB-2** | R2 — A6 ROU + A7 excess-cash adjusters (MED) + the A6/A7 test-coverage rows | DONE | `b82035c` |
| **TDB-3** | "Replace with AI-powered footnote analysis" (PARTIAL) | RESOLVED — AI-failed fallback uses the industry heuristic (not flat 0.40) | `0ff62a6` |
| **TDB-4** | Monitoring & Observability (×2) — `liabilities.go:641-642` | DONE — `logctx` audit log + Prometheus counter | `034f9bc` |
| **TDB-5** | Configuration System (thresholds) — `liabilities.go:17,27` + `assets.go:14` | DONE — 9 asset gates externalized; defaults==constants | `b328463` |
| **TDB-6** | Cloud deployment config variables | DONE — Docker Compose prod env template + `docs/operations/` runbook | `3d6cadf` |
| **TDB-7** | R3 — dead-code cleanup; also CLOSES "Financial Data Extraction (9 sites)" + "Company Size" + "Generic Rule Implementation (×2)" (all inside the deleted `applyRule` chain) | DONE | `18f4ec6` |
| **TDB-8** | Inventory turnover analysis — `flagging/risk_analyzer.go:128` | DONE | `39fb1ef` |
| **TDB-9** | Industry Mapping Expansion — `service.go` `loadIndustryRules` | RESOLVED — **documented defer** (classifier emits only `{45,20,25}`; bare TODO → tracked reference) | `4eb27d7` |
| **TDB-10** | Residual XBRL-matcher / flag-evaluator sub-TODOs | DONE — 4 IMPLEMENT + 3 DE-SCOPE; zero bare TODOs | `5b432ac` |
| **TDB-11** | Expose `cleaning_adjustments` on the fair-value API | DONE | `220bf6e` |
| **TDB-12** | SEC parser populates contingent-liability fields → B3 fires | DONE | `d9cf8b1` |

**Catalog reconciliation:** every 2026-06-05 **OPEN** row is now closed — Industry Mapping → TDB-9,
Inventory turnover → TDB-8, Monitoring & Observability → TDB-4, Configuration System → TDB-5, Cloud
deployment → TDB-6; "Generic Rule Implementation (×2)" (`service.go:794,867`) + "Financial Data
Extraction (9 sites)" were inside the `applyRule` chain DELETED by TDB-7. The 2026-06-05 **PARTIAL**
rows are resolved — AI footnote → TDB-3, A6/A7 adjuster + tests → TDB-2.

**Validation (cumulative):** `go build`/`go vet` exit 0; `go test ./... -count=1` = **50/50 ok, 0 FAIL**;
load-bearing invariants byte-identical (DDM bit-for-bit, recompute-shadow exit 0, ledger basket);
live API verified (AAPL/JPM/KO 200, `calc_version 4.7`, TDB-4/11 + counter three-way consistent).

**Discovered follow-up (NOT a TDB item):** GitHub **#13** — `DATABASE_DRIVER=postgres` doesn't boot
(no PG driver imported; CLIs SQLite-only). Surfaced by REVIEWER in TDB-6; a real code defect for a
future session. The TDB-6 template defaults `sqlite3`.

---

> # ⚠️ **EVERYTHING BELOW IS THE ORIGINAL JAN-2025 CATALOG — HISTORICAL, NOT CURRENT STATUS**
> The `[ ]` / `[x]` checkboxes in the sections below were **NOT** reconciled when the burn-down
> closed; they are a 2025-era snapshot kept for provenance. **The authoritative current status is
> the three reconciliation passes ABOVE** (§2026-06-05, §2026-06-06, §2026-06-08/09 BURN-DOWN
> COMPLETE). Do **not** read a `[ ]` below as open work — cross-reference it against the passes above
> first. Per-item `→ TDB-N` / `→ done` pointers have been added inline below where a legacy item maps
> to a closed pass. **The only genuinely-still-open residual** (never part of the burn-down) is the
> small test-infra/quality backlog called out at the end of the LOW-PRIORITY section and tracked in
> `docs/THESIS.md` → "Next Candidate Work" (test coverage → 90%, edge-case/error tests, E2E
> testcontainers, k6 perf baseline) — plus GitHub **#13** (Postgres driver).

---

## 🔥 **HIGH PRIORITY TODOS** (Phase 3B-3D Implementation) — _historical; all ✅ DONE (2025-01)_

### **Category C: Earnings Normalization** ✅ **COMPLETED**
**Location**: `internal/services/datacleaner/adjustments/earnings.go`
- [x] **Add Category C (Earnings Normalization) adjuster** ✅ **COMPLETED 2025-01-19**
- **Impact**: Critical for Phase 3B completion
- **Effort**: 90 minutes (Step 5 of Phase 3B)

### **XBRL Tag Matching System**
**Location**: `internal/services/datacleaner/service.go:369-370`
- [X] **Implement proper XBRL tag matching based on actual data structure**
- [X] **Change the approach to checkRuleApplicability by config - hardcoded numbers don't apply to all cases**
- **Impact**: Core business logic accuracy
- **Effort**: 60 minutes

### **Industry Code Detection System**
**Location**: `internal/services/datacleaner/service.go:844`
- [X] **Rethink industry code function - not maintainable, hardcoded IndustryCodes not a good idea**
- **Related**: Add industry code field to FinancialData entity (from PHASE_3_INTEGRATION.md:385)
- **Impact**: Industry-specific rule accuracy
- **Effort**: 45 minutes

### **Flag Conditions Configuration**
**Location**: `internal/services/datacleaner/service.go:890,906`
- [X] **Consolidate flag conditions in a configurable system** (2 instances)
- **Impact**: Maintainability and flexibility
- **Effort**: 30 minutes

### **Phase 3B-3D High Priority Tasks Completion Notes**

**✅ XBRL Tag Matching System (Completed 2025-01-31)**
- Created comprehensive XBRL tag configuration system in `internal/config/xbrl_config.go`
- Implemented XBRLTagMatcherService with full transformation support (multiply_by_thousand, to_decimal, etc.)
- Added configurable tag mappings with alternative tags support in `config/datacleaner/xbrl_tag_mappings.json`
- Includes validation rules for balance sheet equation, assets positivity, and revenue range checks
- Full integration test suite in `internal/integration/xbrl_tag_matcher_test.go`

**✅ Industry Code Detection System (Completed 2025-01-31)**
- Developed flexible industry code detection service in `internal/services/datacleaner/industry_detector.go`
- Created comprehensive industry mapping configuration in `config/datacleaner/industry_codes.json`
- Supports multiple detection methods: exact name, SIC codes, NAICS codes, keywords, and regex patterns
- Includes sub-industry classification (e.g., TECH_AI, FIN_IB)
- Priority-based matching with confidence scoring
- Full integration test suite in `internal/integration/industry_code_detector_test.go`

**✅ Flag Conditions Configuration (Completed 2025-01-31)**
- Built complete flag condition evaluation system in `internal/services/datacleaner/flag_evaluator.go`
- Created flexible condition configuration supporting AND/OR/NOT operators and nested groups
- Implemented multiple condition types: numeric, string, boolean, date, exists, regex
- Added configurable actions: set_field, log, alert, transform
- Global variables support for reusable thresholds
- Full integration test suite in `internal/integration/flag_condition_evaluator_test.go`

---

## ⚠️ **MEDIUM PRIORITY TODOS** (Technical Debt & Enhancements)

### **Phase 2.5 MVP Infrastructure** 🆕 — ✅ **migrate/seed wiring DONE (2026-06-06)**
**Location**: `scripts/launch_staging.sh`
- [x] **Add migration command when available** ✅ wired `go run ./cmd/migrate -db "$DB_PATH"` (2026-06-06)
- [x] **Add seed script when SQL seed is created** ✅ wired `go run ./cmd/seed-demo-key -db "$DB_PATH"` (2026-06-06)
- [x] **Add cloud deployment configuration variables** (from Phase 2.5.1) — ✅ **closed by TDB-6** (Docker Compose prod env template `config.env.prod.example` + `docs/operations/deployment-runbook.md`)
- **Impact**: MVP deployment readiness
- **Effort**: 1 hour

### **Financial Data Extraction Improvements** — ❌ **CLOSED — DEAD CODE (2026-06-06)** _(applyRule chain has zero callers; 7/9 superseded by DC-1 adjusters; genuine residue re-filed as R1/R2/R3 in the 2026-06-06 pass)_
**Location**: `internal/services/datacleaner/service.go`
> ✅ The legacy `applyRule` rule path carrying all 9 TODOs below was DELETED under
> **TDB-7 / issue #7** (2026-06-06, branch `worktree-tdb-7-dead-code-cleanup`) — it had zero
> callers. The line numbers below are historical (the code no longer exists). The genuine
> residue (real restructuring / litigation / capitalized-interest extraction) is re-filed as
> **R1 / TDB-1 / issue #1** in the parser (`internal/infra/gateways/sec/parser.go`), NOT the
> cleaner.
> ✅ **All 9 boxes below are CLOSED** — the host `applyRule` chain was deleted as dead code by **TDB-7**, and the genuine residue (real restructuring / litigation / capitalized-interest extraction) was implemented in the SEC parser by **TDB-1**. The boxes are kept (ticked) for provenance.
- [x] **Extract actual restructuring charges from financial data** (was Line 744) — ✅ TDB-7 (dead code) + TDB-1 (real parser extraction)
- [x] **Extract actual asset sale gains from financial data** (was Line 756) — ✅ TDB-7 (dead code)
- [x] **Extract actual litigation costs from financial data** (was Line 764) — ✅ TDB-7 + TDB-1
- [x] **Get actual cash from data - placeholder currently used** (was Line 775) — ✅ TDB-7 (dead code; live excess-cash is A7 via TDB-2)
- [x] **Extract actual ROU assets from financial data** (was Line 785) — ✅ TDB-7 (dead code; live ROU is A6 via TDB-2)
- [x] **Extract actual DTA from financial data** (was Line 840) — ✅ TDB-7 (dead code; live DTA is A4)
- [x] **Extract actual capitalized interest from financial data** (was Line 961) — ✅ TDB-7 + TDB-1
- [x] **Extract actual operating lease liability from financial data** (was Line 1008) — ✅ TDB-7 (dead code; live lease is B1)
- [x] **Extract actual pension underfunding from financial data** (was Line 1017) — ✅ TDB-7 (dead code; live pension is B2)
- **Impact**: Data accuracy and business logic precision
- **Effort**: 2-3 hours total

### **AI Integration Structure** — ⚠️ **PARTIAL (2026-06-05)**
**Location**: `internal/services/datacleaner/adjustments/liabilities.go`
- [x] **Integrate AI service for footnote analysis for precise probability estimates** ✅ DC-1 Phase 3 shipped the B3 contingent-liability AI path (`analyzeContingentLiabilityWithAI` + SHA-256 provenance)
- [x] **Replace with AI-powered footnote analysis for more precise estimates** — ✅ **closed by TDB-3** (the B3 AI path already exists from DC-1 Phase 3; TDB-3 made the AI-*failed* fallback use the industry heuristic instead of a flat 0.40 — the heuristic is the deliberate fallback, not a gap)
- **Impact**: Advanced analytics capability
- **Effort**: Phase 3B Step 6 (45 minutes)

### **Company Size Classification** — ❌ **CLOSED — OBSOLETE/ORPHANED (2026-06-06)**
**Location**: `internal/services/datacleaner/service.go:1160` _(`getCompanySize`)_
- [x] ~~**Implement proper company size classification based on market cap**~~ — producer-only (`service.go:164`), read by zero production consumers; market cap unavailable to the datacleaner. **Dead code removed under TDB-7 / issue #7 (2026-06-06).**
- **Impact**: Better risk assessment
- **Effort**: 30 minutes

### **Industry Mapping Expansion**
**Location**: `internal/services/datacleaner/service.go:459` _(was :260; refreshed 2026-06-05)_
- [x] **Add more industry mappings as needed** — ✅ **closed by TDB-9** (documented defer: `loadIndustryRules` maps a GICS sector → override file; the live classifier emits only `{45,20,25}`; the bare TODO was replaced with a tracked, criteria-based reference)
- **Impact**: Broader industry coverage
- **Effort**: 15 minutes per industry

---

## 📊 **LOW PRIORITY TODOS** (Future Enhancements)

### **Test Coverage Expansion** — ⚠️ **PARTIAL (2026-06-05)**
**Location**: `internal/services/datacleaner/adjustments/` (per-adjuster `*_adjuster_test.go`)
> DC-1 Phase 2 rebuilt the adjusters behind the `Adjuster` interface; all 16 canonical adjusters
> now ship `*_Adjuster_Interface_Contract` tests. The legacy function names below predate that refactor.
- [x] **Add tests for ProcessRightOfUseAssetAdjustment (A6)** — ✅ **closed by TDB-2** (A6 ROU adjuster implemented + tested)
- [x] **Add tests for ProcessExcessCashAdjustment (A7)** — ✅ **closed by TDB-2** (A7 excess-cash adjuster implemented + tested)
- [x] **Add tests for ProcessCapitalizedSoftwareAdjustment (A3)** ✅ `TestACapitalizedSoftwareReviewAdjuster_Adjuster_Interface_Contract`
- [ ] **Add integration tests with multiple adjustments** — partially covered by `internal/integration/datacleaner_ledger_basket_test.go`. **⚠️ GENUINELY OPEN (out of burn-down scope)** — future test-infra backlog.
- [ ] **Add error handling and edge cases tests** — **⚠️ GENUINELY OPEN (out of burn-down scope)** — future test-infra backlog.
- **Impact**: Test coverage improvement (currently 77-83% → 90% target = the W-4 gap in THESIS "Next Candidate Work")
- **Effort**: 2-3 hours total

### **Test Data Enhancement** — ✅ **DONE (2026-06-05)**
**Location**: `internal/services/datacleaner/service_test.go:487+`
- [x] **Add more problematic patterns** ✅ `createTestProblematicFinancialData` / `createTestRiskyFinancialData`
- [x] **Add contingent liabilities, aggressive accounting, etc.** ✅ fixtures set `ContingentLiabilities`, `LitigationLiabilities`, `RestructuringCharges`, `StockBasedCompensation`, `DerivativeGainsLosses`, missing-field lists
- **Impact**: Better test scenarios
- **Effort**: 30 minutes

### **Inventory Analysis Enhancement**
**Location**: `internal/services/datacleaner/flagging/risk_analyzer.go:128` _(was `system_test.go:395`; refreshed 2026-06-05)_
- [x] **Add inventory turnover data for better analysis** — ✅ **closed by TDB-8** (turnover refines the obsolescence-flag severity; escalate <2.0×, de-escalate ≥4.0×)
- **Impact**: Improved inventory obsolescence detection
- **Effort**: 20 minutes

### **Monitoring & Observability**
**Location**: `internal/services/datacleaner/adjustments/liabilities.go:641-642` _(was :199-200; refreshed 2026-06-05)_
- [x] **Add monitoring metrics for calculation performance** — ✅ **closed by TDB-4** (`datacleaner_adjustments_total{rule_id,category,type}` Prometheus counter)
- [x] **Log calculation details for audit trail** — ✅ **closed by TDB-4** (`trace.datacleaner.adjustment` per-fired-adjustment logctx audit line)
- **Impact**: Production monitoring
- **Effort**: 45 minutes

### **Configuration System**
**Location**: `internal/services/datacleaner/adjustments/liabilities.go:17,27` + `adjustments/assets.go:14` _(refreshed 2026-06-05)_
- [x] **Add configuration for adjustment thresholds** — ✅ **closed by TDB-5** (9 asset gates externalized to `config/datacleaner/adjustment_thresholds.json`; defaults==constants)
- [x] **Load configuration from proper source** — ✅ **closed by TDB-5** (loader `adjustment_thresholds_config.go` + warn-and-fallback wiring)
- **Impact**: Operational flexibility
- **Effort**: 30 minutes

### **Context Management** — ✅ **DONE (2026-06-05)**
**Location**: `internal/services/datacleaner/adjustments/liabilities.go` (ctx threaded in DC-1 Phase 3 followup)
- [x] **Use proper context from caller** ✅ `ProcessOperatingLeaseAdjustment(ctx, …)` and sibling adjusters now take `ctx`; the retired `context.Background()` TODO is documented at `liabilities.go:524-529`
- **Impact**: Better error handling and cancellation
- **Effort**: 15 minutes

### **Generic Rule Implementation**
**Location**: `internal/services/datacleaner/service.go:794,867` _(was :519,591; refreshed 2026-06-05)_
- [x] **Implement specific logic for each rule** (2 instances) — ✅ **closed by TDB-7** (these lived inside the `applyRule` chain at `service.go:794,867`, deleted as dead code — zero callers)
- **Impact**: Complete rule coverage
- **Effort**: 1-2 hours

---

### **Proper API documentation** — ✅ **DONE / RESOLVED (2026-06-06)**
**Location**: `internal/api/v1/handlers/health.go`
- [x] **API documentation for the health/metrics endpoints** ✅ swaggo `@Summary`/`@Router` added to `DetailedHealthCheck` + `GetMetrics` (2026-06-06)
**Location**: `internal/api/v1/handlers/performance.go`
- [x] **Performance dashboard docs** ✅ **N/A — handler deleted** (wired to no route; dead code removed 2026-06-06)
**Location**: `internal/api/v1/handlers/fair_value.go`
- [x] **Add proper API documentation for the fair value endpoint** ✅ swagger `@Summary`/`@Router` at lines 287, 303, 501, 514
**Location**: `cmd/server/main.go`
- [x] **API documentation for the server entry point** ✅ general `@title`/`@BasePath`/`@securityDefinitions` already at `cmd/server/main.go:25-42`

## 📈 **PRIORITY MATRIX**

### **Phase 3B Implementation (Next 3 hours)**
1. ✅ **Category C Earnings Normalization** - Critical for phase completion ✅ **COMPLETED 2025-01-19**
2. ✅ **AI Integration Structure** - Required for phase completion ✅ **COMPLETED 2025-01-19**
3. [x] **XBRL Tag Matching** — ✅ **COMPLETED 2025-01-31** (see the XBRL Tag Matching System notes above)

### **Phase 2.5 MVP Implementation (Current)** 🆕
1. **Staging Infrastructure** - Scripts and configuration for local deployment
2. **Database Migrations** - Schema setup and demo data seeding
3. **API Documentation** - Swagger/OpenAPI generation

### **Technical Debt Resolution (Next Sprint)**
1. **Industry Code Detection System** - Architecture improvement
2. **Financial Data Extraction** - Data accuracy enhancement
3. **Flag Conditions Configuration** - Maintainability improvement

### **Future Enhancements (Backlog)**
1. **Test Coverage Expansion** - Quality assurance
2. **Monitoring & Observability** - Production readiness
3. **Configuration System** - Operational flexibility

---

## 🎯 **IMPLEMENTATION STRATEGY**

### **Immediate Actions (Phase 3B)**
- Focus on high-priority TODOs that block phase completion
- ✅ **Category C earnings normalization adjuster** ✅ **COMPLETED 2025-01-19**
- ✅ **AI service integration structure** ✅ **COMPLETED 2025-01-19**
- [x] **Improve XBRL tag matching system** — ✅ done 2025-01-31

### **Current Actions (Phase 2.5)** 🆕
- [x] **Complete staging infrastructure setup** — ✅ `launch_staging.sh`/`stop_staging.sh` + migrate/seed wiring (2026-06-06)
- [x] **Implement database migrations and seeding** — ✅ `cmd/migrate` + `cmd/seed-demo-key` (wired 2026-06-06)
- [ ] **Add E2E tests with testcontainers** — **⚠️ GENUINELY OPEN (never in burn-down scope)** — future test-infra backlog
- [ ] **Performance baseline with k6** — **⚠️ GENUINELY OPEN** — partial baseline at `docs/integration/PERF_BASELINE.md`; k6 harness is future backlog

### **Technical Debt Sprint**
- Refactor industry code detection system
- Implement proper financial data extraction
- Create configurable flag conditions system

### **Quality Improvement Sprint**
- Expand test coverage to meet 90%+ target
- Add comprehensive error handling
- Implement monitoring and observability

---

## 📋 **TRACKING NOTES**

**Documentation Sources:**
- Direct code analysis of TODO comments
- PHASE_3_INTEGRATION.md priority items
- Test coverage improvement requirements
- Architecture improvement needs
- Phase 2.5 MVP requirements 🆕

**Update Frequency**: This catalog should be updated after each major implementation phase to reflect completed items and newly identified tasks.

**Completion Tracking**: Mark items as ✅ when completed and add completion date for audit trail.

---

## 🎯 **RECENT COMPLETIONS**

### **Phase 2.5 Task 2.5.1 Progress** 🆕 **PARTIAL COMPLETION 2025-01-28**
- [x] **Created launch_staging.sh script** - Single-command launch for local staging
- [x] **Created stop_staging.sh script** - Clean shutdown of staging environment
- [x] **Updated README.md** - Added Quick Start documentation
- [x] **Database migrations** ✅ `cmd/migrate/main.go` exists (verified 2026-06-05) — `launch_staging.sh` wiring still TODO
- [x] **Demo data seeding** ✅ `cmd/seed-demo-key/main.go` exists (verified 2026-06-05) — `launch_staging.sh` wiring still TODO
- **Impact**: Simplified local development and testing workflow
- **Technical Details**:
  - Scripts handle .env creation from config.env.example
  - Docker Compose integration for Redis
  - Health check verification built-in
  - Cross-platform support (Windows/Linux/macOS)

### **Test Fixes** ✅ **COMPLETED 2025-01-19**
- [x] **Fixed TestAssetAdjuster_ProcessAssetAdjustments_ActiveWorkflow** - Resolved floating point precision issues in intangible asset retention rate calculations
- [x] **Fixed TestDataCleanerService** - Updated test expectations to account for new earnings normalization functionality
- **Impact**: All tests now passing, ensuring code quality and reliability
- **Technical Details**:
  - Implemented tiered retention rates for intangible assets (33.3% for >$300k, 30% for $200k-$299k, 20% for <$200k)
  - Enhanced test data with earnings normalization fields to trigger proper flag generation
  - Updated quality scoring expectations to reflect new business logic

### **Category C Earnings Normalization** ✅ **COMPLETED 2025-01-19**
- [x] **Implemented ProcessEarningsAdjustments** - Complete Category C earnings normalization system
- [x] **Added all earnings adjustment rules** - Restructuring charges, asset sales, litigation, stock compensation, derivatives, capitalized interest, working capital
- **Impact**: Critical Phase 3B completion requirement
- **Technical Details**:
  - Implemented comprehensive earnings normalization with 7 adjustment types
  - Added proper threshold checking and materiality assessment
  - Integrated with existing cleaning pipeline and quality scoring

### **AI Integration Structure** ✅ **COMPLETED 2025-01-19**
- [x] **Created AI service interfaces** - Complete interface definitions for footnote analysis
- [x] **Implemented mock AI service** - Full mock implementation for testing
- [x] **Added AI integration points** - Ready for actual AI service integration
- **Impact**: Advanced analytics capability foundation
- **Technical Details**:
  - Defined FootnoteAnalysisRequest/Response structures
  - Implemented AIService interface with mock implementation
  - Added configuration support for AI service integration

---

*This catalog represents a comprehensive inventory of all identified TODO items as of January 28, 2025, last reconciled against the live codebase on June 5, 2026 (see the **2026-06-05 Verification Pass** near the top). Items are prioritized based on business impact, implementation phase requirements, and technical debt severity.*
