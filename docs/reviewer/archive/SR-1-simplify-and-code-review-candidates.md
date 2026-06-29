# SR-1 — Codebase-wide /simplify and /code-review candidates

- **Status: CLOSED 2026-06-29 (archived).** Part A COMPLETE (A1–A11; A7 finished 2026-06-27 via #24). Part B fully dispositioned (see the "Part B disposition" block below): B1–B4 + B5/B6/B8/B9/B11 FIXED, B12 + B13-nits DOCUMENTED, B7/B10/bulk-errgroup DEFERRED to **GitHub #29**. No remaining open SR-1 item. Detailed history retained below.
- **Status (historical):** SIMPLIFY COMPLETE 2026-06-13 — Part-A items landed on `claude/dreamy-sanderson-4a1812`, **EXCEPT A7 which shipped only a partial `classifyTickerIndustry` extraction (`8615e5b`); the full A7 split was completed later — 2026-06-27, GitHub #24 — see the A7 section's STATUS line.** **B1–B4** FIXED 2026-06-11 (VERIFIER: all four VERIFIED; REVIEWER: B2/B3/B4 APPROVE, B1 APPROVE_WITH_NITS closed). **Simplify commits:** A1+A2+A4+A5b+A11 (batch-1 deletions) + A1-sweep `e06c328`; A6 `41748ef` (TTM unification); A10 `7d2180c` (longest-prefix lookup core); A9 `2e4f585` (duplicate AI+industry type sets); A3 `e8f9c27` (16 adapter structs + Adjuster interface, ~670 net lines); A7 `8615e5b` (classifyTickerIndustry extraction — pre/post replay differential byte-identical); A8 `5034b0f` (GET/POST error ladder + success tail). **Conventional testing GREEN:** `go build ./...` + `go vet ./...` clean; `go test ./... -count=1` 48/48 packages ok; `lint-logs` + `lint-prometheus-registers` reproduced via ripgrep (rg not installed locally) — both pass, zero new violations. **Live API testing PASS:** server booted (FRED-disabled, real SEC+Yahoo), AAPL/AMD/EQIX/JPM + bulk + POST{}==GET byte-identical + full error ladder (400 INVALID_TICKER, 404 TICKER_NOT_FOUND, GET-400/POST-422 override asymmetry) — JPM live dcf `324.610461699142` == 2026-06-06 baseline byte-for-byte; no panics/500s across 1653 log lines. **Adversarial review CLEAN** — an 11-agent multi-lens workflow (4 dimension reviewers + 7 adversarial verifiers) over all seven refactor commits returned `overall: clean` for every cluster; the 7 confirmed findings are all severity-`low` *positive certifications* (dead-code-deletion safety, no wrong-method rewiring, no recoverable coverage loss — only deleted-adapter `Name()` assertions removed with `AdjusterID` still pinned via production-output fields, A10 byte-for-byte semantics + W-4 determinism preserved). Zero defects, zero blocking findings.
- **Pre-existing (NOT SR-1) follow-up surfaced during live testing:** FIN-prefix banks (JPM) return an empty `data_quality_score`/`data_quality_grade` on the DDM→DCF fallback path (DDM can't value due to the documented T2-BS-1 cleaner DPS=0 bug). Byte-identical to baseline, so not introduced here — but the fallback path not stamping the score is worth a separate fix.
- **B1 DISCLOSURE (REVIEWER MEDIUM, closed):** reviving the configured flag system is a *wire-visible* behavior change — flags with no hardcoded equivalent (`high_leverage_flag`, `negative_equity_flag`, `cash_flow_quality_flag`) can now fire, each warning-severity trigger deducts 10 points from `data_quality_score` (and shifts `data_quality_grade`) on the FairValueResponse for genuinely risky shapes. Pinned by `TestShippedConfig_HighLeverageFlag_FiresAndDeductsQualityScore`. `industry_specific_tech_flag` stays intentionally dormant (`industry_code` sourced from the unpopulated entity field — documented on `buildFlagEvaluationData`). Replay-baseline drift triage (B1 quality-score + B3 tech-classification effects vs `artifacts/tier2-baseline/2026-06-06/`) is folded into the live-testing phase.
- **Date:** 2026-06-10
- **Author:** full-codebase read (learn-codebase pass over all ~210 production Go files, ~62.6K lines, plus shipped configs)
- **Scope:** what is worth running `/simplify` on (behavior-preserving cleanups) and what is worth running `/code-review` on (suspected correctness defects). Each item carries evidence (file:line), a risk note, and a verification step.

---

## 0. Load-bearing invariants any cleanup MUST respect

Before acting on anything below, the executor must keep these green (they are pinned by tests and documented in CLAUDE.md):

1. `TestDDM_LegacyPath_BitForBit` — JPM/BAC/WFC `math.Float64bits` equality. Never regenerate goldens.
2. Recompute-shadow byte-identity — `git diff --quiet internal/integration/testdata/recompute-shadow/`.
3. `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_*`.
4. POST `{}` == GET byte-identity (`TestPostFairValue_EmptyBody_EqualsGET`).
5. Replay field-count guard — any `FairValueResponse` field change must update `internal/observability/replay/diff.go` (`goFieldToJSON` + `countFairValueFields`) or every replay test panics at init.
6. TTM source strings (`TTM_4Q`, `TTM_PRIOR_BRIDGE`, …) and warning prefixes (`revenue_base:`, `operating_income_base:`, `reinvestment_model:`, `guidance:`) are public contract — preserve verbatim across refactors.
7. NF1 (guidance): production `GuidanceRoot=""` ⇒ byte-identical to the 4.7 engine.
8. `lint-logs` / `lint-prometheus-registers` guards (request-path logs via `logctx`, per-instance Prometheus registry).

---

## Part A — `/simplify` candidates (quality, behavior-preserving)

Ranked by (lines removed × risk⁻¹). Items A1–A5 are near-zero-risk deletions/merges; A6+ are structural.

### A1. Dead subsystem: `PipelineOrchestrator` (~530 lines, test-only)
- **Evidence:** `internal/services/datacleaner/pipeline.go`. Zero production callers — `NewPipelineOrchestrator`/`ExecutePipeline` are referenced only by `pipeline_test.go`, `pipeline_perf_test.go`, `integration_test.go` (grep-verified). The production cleaner path is `service.go::applyActiveAdjustments`, which does not use stages.
- **Additional smells inside it:** constructs a `MockAIService` in non-test code ([pipeline.go:58](internal/services/datacleaner/pipeline.go)); the parallel path runs stages on *copies* of the data and then **discards their mutations** (merge is a `TODO`, [pipeline.go:521-528](internal/services/datacleaner/pipeline.go)) — it is broken by design, not just unused.
- **Action:** delete the orchestrator + its 5 stage processors + their tests, or (if the stage abstraction is wanted someday) move to a spec. `QualityAssessmentStageProcessor` and `FlaggingStageProcessor` are stubs returning "not yet implemented" anyway.
- **Risk:** none on production behavior. Verification: `go build ./... && go test ./...`.

### A2. Dead subsystem: `internal/services/alerting/` (~1,300 lines)
- **Evidence:** `configuration.go` + `regression_detection.go` are never constructed anywhere in production (grep: the only non-test mention of `alerting.` is a *comment* in `flag_evaluator.go`). The supporting port file `internal/core/ports/alerting.go` (292 lines: `AlertRepository`, `RegressionDetectionService`, `TrendAnalysisService`, `AnomalyDetectionService`, `AlertService`, `NotificationService`, `PrometheusExporter`, `ConfigurationLoader` + ~10 supporting structs) and `internal/core/entities/alerting.go` (287 lines) exist only to serve it. `config/alerting/` YAML rides along.
- **Action:** delete or quarantine to a spec. If perf-regression alerting is a future goal, the scripts/benchmark stack already duplicates much of it.
- **Risk:** none. Verification: build + tests.

### A3. Dead interface plumbing: the 16 Adjuster adapter structs (~500 lines)
- **Evidence:** `adjustments/assets.go`, `liabilities.go`, `earnings.go` each define per-rule adapter structs (`a1GoodwillAdjuster` … `c7WorkingCapitalAdjuster`) + exported constructors (`NewA1GoodwillAdjuster` …) + compile-time pins. Production dispatchers call the `Apply*` methods **directly**; the adapters' only callers are unit tests (grep-verified). The `Adjuster` interface itself (`adjustments/adjuster.go`) has no production consumer.
- **Action:** delete the adapter structs/constructors; keep the `Apply*` methods and `AdjusterOutput`. Tests that construct adapters switch to calling `Apply*` directly (mechanical).
- **Risk:** low. The DC-1 Phase 2 plan envisioned dispatching *through* the interface; that never happened and Phase 5 closed DC-1. If the interface is kept deliberately, document that; today it is ~500 lines of ceremony per-rule that every new adjuster copies.
- **Verification:** full suite + DDM bit-for-bit + shadow byte-identity.

### A4. Orphaned validators/reporting (CLAUDE.md-acknowledged): `xbrl_matcher.go` (525), `reporting.go` (547), `flag_evaluator.ExecuteActions` chain, deprecated `CalculateNetTangibleAssets` (~95)
- **Evidence:** TDB-10 already established `XBRLTagMatcherService` is orphaned and `ExecuteActions` is dead. `reporting.go`'s generator only feeds `reporting_test.go`; `ValuationResult.CleaningReport` is never populated (`service.go` comment: "would need the full report structure"). `CalculateNetTangibleAssets` is marked DEPRECATED with only test callers. Ports `xbrl.go`, `flag_evaluator.go` (the `ExecuteActions` member), `industry_detector.go` + `IndustryCodeDetectorService` (uses stdlib `log`, violating the zap rule; integration-test-only) are in the same class.
- **Action:** one deletion PR for the whole orphan class, or an explicit "kept as future surface" note per file. Today they cost reading time and carry stdlib-log convention violations.
- **Risk:** none on production behavior.

### A5. Constructed-but-never-used resilience layer
- **Evidence:** `internal/di/container.go:581-597` injects `*CircuitBreakerFactory` and `*RetryPolicyFactory` into `NewSECGateway`/`NewMarketDataGateway` and both **ignore them**. No caller of `CreateSECCircuitBreaker`/`CreateMarketDataRetryPolicy` etc. exists outside the factory definitions. The gateways implement their own inline retry loops instead (`sec/client.go`). `internal/infra/resilience/` is therefore live code with dead wiring.
- **Action (pick one):** (a) wire the policies into the gateways (a real feature — the inline retry in `sec/client.go` triplicates the same loop 3×), or (b) delete the factories + unused params. Either ends the current "looks resilient, isn't" state.
- **Risk:** (b) is zero-risk; (a) is a behavior change → goes through /plan, not /simplify.

### A6. The TTM clone: `trailing_operating_income.go` vs `financial_data.go` (~320 duplicated lines)
- **Evidence:** `TrailingTwelveMonthsOperatingIncome` + its four helpers are a near-verbatim copy of the revenue chain (`ttmFourQuarters*`, `ttmPriorBridge*`, `latestAnnual*`, `annualizedQuarter*`), differing only in the metric extractor (`periodEffectiveOI` vs `.Revenue`) and label strings.
- **Action:** extract one generic `trailingTwelveMonths(h, metric func(*FinancialData) float64, labels ttmLabels)` used by both. Keep the *exact* source strings and warning text (public contract, BUG-015/RM-1).
- **Risk:** medium-low — this code feeds the DCF base; FY-latest bit-for-bit behavior must be preserved. Strong existing test coverage (`trailing_revenue_test.go`, `trailing_operating_income_test.go`) makes this a good guarded refactor.
- **Verification:** those two test files + `service_test.go` BUG-015 pins + replay on `artifacts/tier2-baseline/2026-06-06/`.

### A7. `performValuation` is ~1,200 lines; ~40% is observability boilerplate
- **STATUS: DONE 2026-06-27 (GitHub #24, branch `refactor/sr1-a7-performvaluation`).** The earlier "all Part-A landed" header was inaccurate for A7 — only a partial `classifyTickerIndustry` extraction (`8615e5b`) had shipped; the full split was never done. Now completed as a verified **pure behavior-preserving move**: `performValuation` (~1,180 lines) is a thin orchestrator delegating to `resolveValuationInputs` → `runDCF` → `assembleResult` (with `computeWACC` called inside `resolveValuationInputs` at the original line position), threading state through a single `valuationCtx` carrier; the 7 `calcEmitter` guard-blocks collapse through a `stageEmitter.calc(stage, ...zap.Field)` helper (narrate/artifact pairs left inline by design — heterogeneous payloads). Commit ladder: `427512b` stageEmitter → `a09a88d` valuationCtx → `f81ecbe` resolveValuationInputs → `9297bdb` computeWACC → `494c576` runDCF → `3129899` assembleResult → `97d90fa` godoc → `bf57d89` (REVIEWER nit: drop write-only `denomShares` carrier field). REVIEWER: **APPROVE_WITH_NITS** (normalized multiset diff = zero math/content drift; `clock.Now()`=3 both sides; metrics 1× each; 12 `Warnings` appends in identical order; carrier write/read graph has no staleness; `runDCF (result, done, error)` early-return contract correct). Load-bearing pins all GREEN with assertions unchanged (`TestDDM_LegacyPath_BitForBit`, `TestPostFairValue_EmptyBody_EqualsGET`, BUG-015, narrate/artifact emission order, replay field-count guard); live API smoke green (AAPL/JPM/AMD 200, bad-ticker 404, POST{}==GET byte-equal). No CalcVersion bump (no behavior/API change). Plan: `docs/reviewer/archive/SR-1-A7-performvaluation-refactor-plan.md` (archived 2026-06-29 — A7 complete).
- **Evidence:** `internal/services/valuation/service.go:648-1833`. Twelve repeated blocks of the shape `if s.calcEmitter != nil { emit } ; narrate.From(ctx).Emit(...) ; if b := artifact.From(ctx); b != nil { b.Snapshot(...) }`.
- **Action:** extract a small `stageEmitter` helper struct (holds ctx, ticker, calcEmitter) with methods per stage, and split the function into phases (`resolveInputs`, `computeWACC`, `runDCF`, `assembleResult`). The math ordering must not move — only the emission plumbing.
- **Risk:** medium. This is the hottest file in the repo and byte-identity comments are anchored to it. Recommend doing AFTER A1–A5 land, with replay verification per commit. Do not reorder any read of `restatedViewOr`/`asReportedViewOr`.

### A8. GET/POST/bulk handler triplication in `fair_value.go`
- **Evidence:** `GetFairValue` and `PostFairValue` duplicate ~120 lines each of narrate/bundle/error-classification; `classifyBulkError` re-encodes the same FPI→INSUFFICIENT→MODEL_NOT_APPLICABLE→404→500 ladder a third time.
- **Action:** extract `(h *FairValueHandler) respondValuationError(c, ticker, err)` and a shared "run valuation + emit + snapshot" helper. The RFC-7807 shapes already share `sendError`/`paramErrorResponse`, so this is low-risk consolidation.
- **Verification:** the large handler test suite (`fair_value_*_test.go`) is the safety net; POST{}==GET pin must stay green.

### A9. Duplicate type definitions across packages
- `entities/ai_analysis.go` ↔ `datacleaner/ai/interfaces.go`: `FootnoteAnalysisRequest/Response`, `ContingentLiabilityEstimate`, `PensionObligationData`, `OperatingLeaseData`, `RestructuringData`, `AIServiceConfig`, `AIServiceMetrics`, `AIService` — two whole parallel copies. Production uses the `ai` package set; the entities set appears consumer-less.
- `entities/industry.go` ↔ `datacleaner/industry/classifier.go`: `SectorConfig`, `RiskProfile`, `IndustryThresholds`, `IndustryCharacteristics`, `RiskLevel` duplicated.
- `entities/data_cleaning.go` legacy enum aliases (`RuleCategoryAssetQuality` = `AssetQuality`, `AdjustmentTypeExclusion` = `Exclude`, `Info/Warning/Critical` vs `FlagSeverity*`) — two vocabularies for the same wire values, which forced `quality_flag_severity.go`'s rank-collapse shim.
- **Action:** pick one home per type, alias-deprecate the other; for severities, a follow-up that migrates emit sites to the modern vocabulary would let the legacy aliases retire.
- **Risk:** low-medium (wire values must not change — only Go-side names).

### A10. Longest-prefix-match lookup implemented three times
- **Evidence:** `valuation/crosscheck.go::LookupMultiple`, `models/ffo.go::lookupSubsectorValue`, `models/revenue_multiple.go::getMultiple` — same algorithm (exact → longest `code+"_"` prefix → `default`).
- **Action:** one shared helper (e.g. in `models/util.go` or a tiny `lookup` package). W-4 determinism comment moves with it.
- **Risk:** low; pinned by existing model tests + pins.go values.

### A11. Smaller mechanical cleanups (bundle into one /simplify pass)
| Item | Evidence | Note |
|---|---|---|
| O(n²) bubble sorts | `entities/financial_data.go::GetRecentYears`, `CalculateAverageGrowthRate` | `sort.Slice` |
| Duplicated latest-year scan | `ttmPriorBridge*` vs `annualizedQuarter*` (×2 files) | folds into A6 |
| Hamada unlever→relever no-op | `service.go:1124-1131` — unlever then relever at the *same* D/E and tax rate is exact identity; `beta` always == `blumeBeta` | either delete the ladder or implement target-structure relevering (the latter is a behavior change → plan) |
| `extractLatestUSDValue` dead | `datafetcher/coordinator.go:405` | no callers |
| `GetCoordinationMetrics` stub | `coordinator.go:455` returns zeros | delete or implement |
| Knob fan-out | adding one override knob touches ~7 hand-maintained lists (`params.Overrides`, DTO, `projectOverrides`, `hasAnyOverride`, `anyBulkOverride`, `RequestOverrides` switch, Layer-1 validator) | consider a single knob-descriptor table; at minimum add a reflection test pinning the lists to the same length |
| FFO constructor sprawl | `NewFFOModel` / `WithConfig` / `WithTables` / `WithMultiple` (4 constructors, 2 embed reads) | collapse to functional options; `loadFFOConfig`+`loadFFOSubsectorTables` parse the same embed twice |
| `valuation` config loaders read CWD | `LoadCountryRiskPremiums`, `LoadADRRatios`, `LoadFXRates` use `os.ReadFile("./config/...")` while profiles/multiples/industry-codes use embedded `configfs` | migrate to configfs for cwd-hermeticity (replay module already hand-mirrors these) |
| stdlib `log` usage | `flag_evaluator.go`, `industry_detector.go` | violates "zap exclusively"; see also B1 |
| `BenchmarkResult` struct in `entities/alerting.go` | gofmt-odd alignment, only consumer is scripts/orphans | rides with A2 |

---

## Part B — `/code-review` candidates (suspected correctness defects)

Each item is phrased as a claim + the verification a reviewer should run. None were fixed in this pass.

> ### Part B disposition — CLOSED 2026-06-29 (GitHub #27, branch `chore/sr1-partb-burndown`)
> Every B-item now has a terminal disposition. **B1–B4** were FIXED earlier (2026-06-11). The 2026-06-29 burndown (#27) resolved the rest:
>
> **FIXED (5):**
> - **B5** — dropped the unbounded `ticker` label from the 3 legacy valuation metrics (`fc38ef5`). Live-verified: `/metrics` emits `dcf_valuation_requests_total{request_type,status}` with no ticker dimension.
> - **B6** — cache-hit path returns a shallow copy instead of mutating the shared cached `*CleaningResult` (`203724b`); pinned by `TestCleanFinancialData_CacheHit_NoSharedMutation` (`-race`).
> - **B8** — `RiskFreeRate3Month` now stores `Yield3Month` not `Yield2Year` (`9a998d8`).
> - **B9** — config-driven CORS (`CORSAllowedOrigins`; wildcard ⇒ credentials OFF, incl. a literal-`"*"` guard) + truthful `/ready` DB+cache probe returning 503 when down (`2ec0043`, `9acdb3e`). Live-verified: `/ready` real DB ping; wildcard CORS emits no `Allow-Credentials`.
> - **B11** — replaced the per-request usage-recording goroutine with a bounded 4-worker / 256-buffer pool, non-blocking drop-on-full (`157df5c`, `9acdb3e`); pinned by `usage_recorder_test.go` (`-race`).
>
> **ALREADY RESOLVED:** B13 swagger `CalculationVersion` example is `4.10` (VAL-1 reconciliation).
>
> **DOCUMENTED (no code change, intentional):**
> - **B12** — Year-2 `NearTermAnchors` fields are intentional Layer-B Phase-3 forward-compat; documented on the struct godoc (`resolver.go`).
> - **B13 nits** — `isValidTicker` rejecting `BRK.B`/`BF.B` is a product decision (document if revisited); the `Industry` FIN/HEALTH→sector-`20` TODO is benign given TDB-9's documented "only 45/20/25 emitted" reality; `bulkArtifactSubject` long pseudo-tickers are a cosmetic FS-name concern; `applyADRRatio` DPS asymmetry stays tracked on the IFRS-FPI tracker.
>
> **DEFERRED — own tracked follow-up (behavior/wire-change risk; NOT safe in a burndown batch):**
> - **B7** — `evaluateRuleThreshold` honoring configured thresholds changes *cleaning applicability* → recompute-shadow / DDM byte-identity risk; needs a dedicated plan + golden review.
> - **B10** — growth fields carrying USD in rate-named/JSON-tagged fields is a *wire-contract* change; needs consumer coordination.
> - **B13 bulk-errgroup** — a latency enhancement (parallelize the ≤10-ticker bulk loop), not a correctness defect.
>
> Validation: `go build`/`go vet` clean; full suite 47 ok packages; B6/B11 `-race` green; live API smoke green (valuation 200, `/ready` real check, CORS no-credentials, no panics). Only pre-existing failures remain (`T2BS3_ParserTruthful`, shadow `BUG-016` — both fail on master). The B7/B10/bulk defers are re-homed in **GitHub #29** — so SR-1 has no remaining open item and is archived.

### B1 (HIGH). The config-driven risk-flag system is inert in production — and panics if it ever wakes up
Three stacked defects in `datacleaner/flag_evaluator.go` + `service.go` + `config/datacleaner/flag_conditions.json`:
1. **Field-name mismatch:** `createRiskWarningFlags` feeds the evaluator PascalCase keys (`TotalAssets`, `Goodwill`, … [service.go:926-933](internal/services/datacleaner/service.go)) while most configured flags reference snake_case fields (`total_assets`, `stockholders_equity`, `debt_to_equity_ratio`) that are never in the map ⇒ conditions resolve false.
2. **Unsupported expression syntax:** the two flags that *do* use PascalCase (`excessive_goodwill_warning`, `excessive_intangibles_warning`) compare against `"${TotalAssets * goodwill_threshold}"` — the evaluator only supports `$fieldRef` ([flag_evaluator.go:271-282](internal/services/datacleaner/flag_evaluator.go)), so the comparison silently returns `false, "invalid numeric comparison"`. Net effect: **no configured flag can ever trigger**; production always falls through to `createHardcodedRiskFlags`.
3. **Latent nil-logger panic:** the evaluator is constructed with a nil `*log.Logger` ([service.go:103](internal/services/datacleaner/service.go)) and calls `s.logger.Printf` unguarded when a flag triggers or errors ([flag_evaluator.go:82,136](internal/services/datacleaner/flag_evaluator.go)). The moment anyone "fixes" the config, every triggering request panics.
4. **Bonus:** the `exists`-type condition is structurally unreachable for absent fields — absence short-circuits in `evaluateCondition` before the `case "exists"` branch, so `data_completeness_flag` (which wants to fire on MISSING fields) can never fire.
- **Verify:** unit test feeding the shipped config + a PascalCase dataMap; assert zero triggers; then inject a trivially-true flag and watch the nil-deref.
- **Disposition options:** (a) fix field names + add expression support + real logger; or (b) accept the hardcoded fallback as the contract and delete the config layer (then this becomes a /simplify item). Either way the current state is misleading.

### B2 (HIGH). Rules-engine industry overrides leak across tickers (shared-state mutation)
- **Claim:** `rules/engine.go::LoadIndustryRules` mutates the *base* rules map in place when applying overrides ([engine.go:66-79](internal/services/datacleaner/rules/engine.go)): `rule.Enabled = *override.Enabled`, `rule.Threshold = override.Threshold`, `rule.Severity = *override.Severity` write through shared `*CleaningRule` pointers. `loadIndustryRules` runs per-clean ([datacleaner/service.go:242-248](internal/services/datacleaner/service.go)), so after the first "45" (tech) ticker is cleaned, the technology threshold/severity overrides are permanently installed in the base rule set used by **every subsequent ticker** of every industry (the `GetIndustryRules` fallback path reads the same mutated map). Special rules are also added to the base index permanently.
- **Impact:** order-dependent cleaning results across a process lifetime; an A5/A1 threshold for an industrials ticker can differ depending on whether a tech ticker was cleaned first. Recompute-shadow fixtures wouldn't catch this (single-ticker runs).
- **Verify:** integration test — clean a "45" ticker, then a no-industry ticker, assert the second sees pristine base thresholds. Compare with a fresh engine.
- **Fix direction:** build the industry rule set as a *copy* (override applied to the copy), never mutating `e.rules`.

### B3 (HIGH). SEC parser never populates six fields that downstream logic depends on
- **Claim:** `parsePeriodData` extracts ~33 fields but has **no extraction** for `ResearchAndDevelopment`, `StockBasedCompensation`, `AssetSaleGains`, `DerivativeGainsLosses`, `CostOfGoodsSold`, `EffectiveTaxRate` (grep-verified: no non-test assignment exists anywhere). Consequences:
  - C2 (asset-sale gains), C4 (SBC dilution flag), C5 (derivative marks) **can never fire** in production — the same gap class TDB-1 closed for C1/C3/C6 and TDB-12 closed for B3.
  - A2/A5 `TaxShieldDTA` is always 0 in production (`EffectiveTaxRate` never set), making the Q2-shipped tax-shield plumbing production-dead.
  - The balance-sheet classifier's R&D/SBC tech heuristics ([industry/classifier.go:718-752](internal/services/datacleaner/industry/classifier.go)) run on permanently-zero inputs; tech detection silently degrades to the intangibles ratio + a hardcoded ticker list — which is why AMD needed regression pins.
- **Verify:** replay a basket bundle and grep `10-clean-input.json` for the six fields (expect all zero); confirm against the live SEC facts that the tags exist (e.g. `us-gaap:ResearchAndDevelopmentExpense`, `ShareBasedCompensation`).
- **Fix direction:** TDB-1-pattern parser extraction spec (candidate "TDB-13"). Note `GetSupportedConcepts` already lists `CostOfGoodsAndServicesSold` — advertised but unparsed.

### B4 (MEDIUM-HIGH). `TotalAssets` falls back to `AssetsCurrent` (first-hit list mixes umbrella and component tags)
- **Claim:** [parser.go:648-656](internal/infra/gateways/sec/parser.go) uses `findValue(data, {"Assets","AssetsCurrent","AssetsNoncurrent"})` — for a filer missing the `Assets` umbrella, `TotalAssets` silently becomes *current assets only*, understating the balance sheet and corrupting every ratio gate (A1/A2/A4 materiality, plugs, Graham). The right fallback is `AssetsCurrent + AssetsNoncurrent` (a `sumValues` pair), mirroring the TSM debt-components pattern.
- **Verify:** unit test with facts lacking `Assets` but carrying both components; check live for any basket filer with that shape.

### B5 (MEDIUM). Prometheus ticker-label cardinality on the legacy valuation metrics
- **Claim:** `metrics/service.go` registers `dcf_valuation_requests_total`, `dcf_valuation_duration_seconds`, `dcf_valuation_errors_total` with a **`ticker` label** ([service.go:169-192](internal/services/metrics/service.go)) — unbounded series growth, and directly contradicts the TDB-4 rule the same file documents for `datacleaner_adjustments_total` ("NEVER a ticker label"). A watchlist of thousands of tickers → thousands of series × status × type.
- **Verify:** `curl /metrics` after a few bulk requests; count series. Fix: drop `ticker` (keep it in logs), or hash-bucket.

### B6 (MEDIUM). Shared-pointer mutation on the cleaner's cache hits (data race + audit skew)
- **Claim:** `datacleaner/service.go` caches `*entities.CleaningResult` and on hit does `cachedResult.ProcessingTime = time.Since(startTime)` ([service.go:183](internal/services/datacleaner/service.go)) — a write to a shared object visible to concurrent requests (race detector should flag under parallel load). Additionally `CleanFinancialDataWithViews` wraps the *cached* `CleanedData` pointer in new views while `keepLatestCleanedSlot` installs the same pointer into the request's `historicalData` — any future consumer mutating that entity would corrupt the cache for all readers. The cache is also unbounded (no eviction; `DataCleanerConfig.CacheTTL` exists but is never consulted).
- **Verify:** `go test -race` with two concurrent `CleanFinancialData` calls on the same key; check memory growth on a long watchlist run.

### B7 (MEDIUM). `evaluateRuleThreshold` ignores most configured thresholds
- **Claim:** [datacleaner/service.go:729-790](internal/services/datacleaner/service.go) — the `PercentageOfRevenue` branch for the generic case returns `data.Revenue > 10_000_000` (ignores the configured value); the `deferred_tax_assets` branch computes `estimatedDTA := TotalAssets * 0.03` and compares *that estimate's own ratio* (always exactly 3%) against the threshold instead of using the actual `data.DeferredTaxAssets`; `working_capital_window_dressing` keys off `Revenue > 50M`. The config promises thresholds the gate doesn't honor — adjacent to (but not covered by) TDB-5, which externalized the *adjuster-side* gates only.
- **Verify:** table test: configure `deferred_tax_assets` threshold 0.05 with real DTA at 10% — applicability check uses the bogus 3% estimate.

### B8 (MEDIUM). Treasury field mislabel: 2-year yield stored as 3-month rate
- **Claim:** `datafetcher/coordinator.go::fetchMacroData` sets `RiskFreeRate3Month: treasuryRates.Yield2Year` ([coordinator.go:391](internal/services/datafetcher/coordinator.go)). `GetEffectiveRiskFreeRate` falls back to this field when the 10-year is zero — in that degraded case WACC uses a mislabeled 2-year rate. Small numeric impact, real semantic bug.

### B9 (MEDIUM). CORS: wildcard origin with `AllowCredentials: true`
- **Claim:** [api/server.go:243-250](internal/api/server.go) — `AllowOrigins: ["*"]` + `AllowCredentials: true` is a spec-violating combination (browsers refuse it; some proxies normalize it dangerously). The `TODO: Configure appropriately for production` acknowledges this. Should be config-driven origins (the TDB-6 runbook would carry the env var).
- Also adjacent: `readinessCheck` returns hardcoded `ok` for database/external/cache ([server.go:759-770](internal/api/server.go)) — a lying readiness probe in a runbook'd deployment (TDB-6 documents compose healthchecks that hit `/health`).

### B10 (LOW-MEDIUM). Growth estimate mislabeled fields on the wire
- **Claim:** `growth/estimator.go::blendGrowthRate` assigns absolute USD revenue *estimates* into `AnalystRevenueGrowthY1/Y2` ([estimator.go:209-210](internal/services/growth/estimator.go)) — fields named and JSON-tagged as growth rates carry e.g. `4.5e11`. Consumers/dashboards reading `analyst_revenue_growth_y1` get dollars, not a rate.
- **Verify:** inspect a bundle's `12-growth-curve.json` for a covered ticker.

### B11 (LOW). Auth middleware spawns an unbounded goroutine per authenticated request
- [server.go:673-688](internal/api/server.go) — usage recording is fire-and-forget per request with a fresh 5s-timeout context; under load this is unbounded goroutine growth (each holding a DB write). A small worker pool / buffered channel is the standard shape. Also `ResponseStatus/ResponseTimeMs` recorded as 0 by design — accepted tradeoff, but the comment should ride to the schema so analysts don't trust those columns.

### B12 (LOW). Authority resolver: Year-2 anchor fields are consumable but never producible
- `authority.NearTermAnchors` carries `CapExYear2/OperatingMarginYear2/RevenueGrowthYear2`, and `applyNearTermAnchors` consumes them — but `authority.Resolve` only ever sets Year-1 (`anchorYear1` is the sole setter). Either intentional Phase-3 forward-compat (then document it on the struct) or a missed §12.4 requirement (guidance fixtures can carry FY+1 envelopes that today silently anchor nothing).
- **Verify:** fixture with a two-period margin guidance — assert whether year-2 was expected to anchor per the Layer-B spec.

### B13 (LOW). Misc verification-worthy nits
| Item | Evidence |
|---|---|
| `FairValueResponse.CalculationVersion` swagger example still `4.6` | [fair_value.go:192](internal/api/v1/handlers/fair_value.go) — engine stamps 4.7 |
| Bulk endpoint processes ≤10 tickers strictly serially | `GetBulkFairValue` loop — latency = Σ tickers; a bounded `errgroup` would cut p95 (cache-respecting) |
| `Industry` heuristic detection of FIN/HEALTH falls back to sector "20" with a TODO | [classifier.go:550-562](internal/services/datacleaner/industry/classifier.go) — interacts with TDB-9's "only 45/20/25 emitted" reality; fine today, but the TODO contradicts the TDB-9 documented-defer and should be reconciled |
| `bulkArtifactSubject` can build very long pseudo-tickers (`BULK_A_B_…`×10) used as directory names | minor FS-name length concern only |
| `isValidTicker` rejects valid share-class tickers (`BRK.B`, `BF.B`) | product decision; document if intentional |
| `applyADRRatio` divides `SharesOutstanding` for **all periods** but DPS asymmetry is documented as known (B11+) | keep on the IFRS-FPI tracker |
| `MetricsService` port is a ~25-method fat interface | interface-segregation cleanup opportunity when touched next |

---

## Part C — Suggested execution order

1. **`/code-review` first on B1–B4** (flag system, rules-engine leak, parser field gaps, TotalAssets fallback) — these change *valuation-relevant behavior* when fixed, so they should land before structural refactors that would make bisection harder. Each is small and independently testable.
2. **`/simplify` batch 1 (deletions): A1 + A2 + A4 + A5(b) + A11-dead-items** — pure removals, one PR, full suite + replay green. Expected net: **−4,500 to −5,000 lines** of production code with zero behavior change.
3. **`/simplify` batch 2 (merges): A3, A6, A9, A10** — guarded by the invariant list in §0.
4. **`/simplify` batch 3 (structural): A7, A8** — last, with per-commit replay verification on the 4.7 baseline.
5. B5–B13 fold into normal backlog (some are one-liners; B5 needs an ops decision about dashboard continuity).

## Part D — What NOT to touch

- The DDM legacy/multi-stage **path duplication** (`calculateLegacyGordon` vs `calculateMultiStage`) is *intentional* per CLAUDE.md — do not unify.
- `params.ResolveTerminal`'s auto-derive arithmetic and `calculateTerminalGrowthRate` are deliberate twins (regression pin) — keep both, keep in sync.
- `recompute.go` and `cleaneddata/restate.go` deliberately duplicate the umbrella math (observer vs view) — documented decision.
- `MinTerminalWACCSpread`/`MaxDCFProjectionYears` duplication between `params` and `pkg/finance/dcf` is documented leaf-purity; a drift *test* is welcome, an import is not.
- The TDB-9 industry-mapping defer, the TDB-5 deferred industry-keyed B-rule tables, and the Phase-3 guidance items are tracked elsewhere — out of scope here.
