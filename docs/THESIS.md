# THESIS.md — Product Direction

This file is the **single source of truth for where Midas is going**. All agents (human and AI) should read this to understand scope, current phase, and roadmap before making decisions.

Update this file when: a phase completes, scope changes, or priorities shift.

---

## Mission

Provide **institutional-quality equity valuation through a simple REST API**, combining SEC EDGAR filings, Yahoo Finance market data, and FRED macroeconomic indicators. The engine must handle *any* publicly traded company correctly — growth, value, international, ADRs, REITs, banks, pre-revenue.

## Primary User

**Yonatan Levin** — personal investor using Midas for decision-making across:
- US growth equities (TSLA, NVDA, etc.)
- US value equities (JNJ, PG, etc.)
- International companies, ADRs, emerging markets

Quality bar: **fintech-platform-grade accuracy**, not a personal script.

---

## Current State (as of 2026-04-18)

**Version:** `v0.9.0-rc1` (MVP — feature complete)

**Tech stack:** Go 1.23+, Gin, SQLite/PostgreSQL, Redis (optional), `uber/fx` DI, `zap` logging, clean/hexagonal architecture.

**Phases:**

| Phase | Status | Commit | Key Work |
|-------|--------|--------|----------|
| 0+1: DCF Fundamentals | COMPLETE (2026-04-09) | `49b0afa` | True FCF, growth caps, diluted shares, WACC-terminal guard, equity bridge |
| 2: Multi-Stage Growth | COMPLETE (2026-04-09) | `66ece97` | 7-year projection, analyst blending, ROIC ceiling |
| Data Quality Guardrail | COMPLETE (2026-04-09) | `e5c33c0`, `08cf32e` | Schema migration, stale data cleanup, CapEx smoothing |
| 3: Industry-Aware Models | COMPLETE (2026-04-09) | `7eaa488` | DDM (banks), FFO (REITs), Revenue Multiple (pre-revenue), DCF (default) |
| 4: International + Cross-Checks | COMPLETE (2026-04-10) | `440d204` | Country risk premium, Blume beta, exit-multiple TV, sanity cross-check |
| IFRS / FPI Support | COMPLETE (2026-04-27) | `phase-b-ifrs-fpi-support` tag | TSM, ASML, NVO, AZN, BABA, BIDU, TM, RIO, BHP, NVS, SHEL, BP produce USD per-ADR fair values via IFRS-full XBRL parsing + FRED FX conversion + configured ADR ratios. Response surfaces `currency`, `adr_ratio_applied`, and `current_price` for transparency. See `docs/refactoring/ifrs-foreign-private-issuer-support-spec.md`. |
| Graham-Floor (Tier 1) | COMPLETE (2026-05-11) | `0324057` | Tier 1 polish — VAL-4 (Graham floor metrics), VAL-5, VAL-7, RM-1.A, RM-1.B archived. Engine at `CalculationVersion 4.1`. |
| Tier 2: AssumptionProfile (in flight) | Bootstrap + P0a + P0b SHIPPED (2026-05-16) | `265b9c9` (Bootstrap) + `d2a586e` (P0a) + `2e48fde` (P0b) | Unified `AssumptionProfile` backbone keyed by `(archetype × maturity)` driving DCF/DDM/FFO/RevenueMultiple calibration. Closes RM-3 + VAL-1 + VAL-2 + VAL-3 P3. **Bootstrap** captured pre-Tier-2 baselines (10 bundles + DDM bit-for-bit goldens). **P0a** delivered the full profile package (91.5% coverage). **P0b** wires Tier 2 plumbing into the engine: `config/assumption_profiles.json` embeds; `Bundle.SetAssumptionProfileManifest` writes `08-assumption-profile.json` (schema v1); `service.go::performValuation` builds Facts + calls Resolve + stamps profile onto `ModelInput.Profile`/result/bundle; `NewService` 11th param `profile.Registry`; 11 omitempty fields added to `ModelResult`/`ValuationResult`/`FairValueResponse` (consumers no-op until P1-P4). JPM bit-for-bit DDM invariant intact across all 3 phases. **P1-P4 dispatch next as parallel worktrees** — RM-3 (forward revenue multiple), VAL-1 (DCF archetype-aware horizon + 5 diagnostics), VAL-2 (DDM multi-stage, bit-for-bit-load-bearing), VAL-3 P3 (forward FFO). Will bump engine to `CalculationVersion 4.2` at Tier 2 close. See `docs/refactoring/spec/assumption-profile-spec.md` + `docs/refactoring/implementations/assumption-profile-implementation-plan.md`. |
| DC-1: Datacleaner refactor (in flight) | Phase 0 SHIPPED (2026-05-16) | `1640394` (merge) | Three-view datacleaner architecture — `CleanedFinancialData {AsReported, Restated, InvestedCapital}` with explicit `AdjustmentLedger` + `OverlaySpec` audit trail. Closes the post-clean balance-sheet asymmetry (`Assets ≠ Liabilities + Equity`) and unlocks future features (Altman-Z, P/B, ROE-decomp, distress screens). **Phase 0** added 4 plug fields to `FinancialData` (`OtherCurrentAssets`, `OtherNonCurrentAssets`, `OtherCurrentLiabilities`, `OtherNonCurrentLiabilities`) + `computePlugs` helper in `internal/infra/gateways/sec/plugs.go` populated at end of `parsePeriodData`. Zero behavior change — empirically replay-verified on AAPL + MSFT (timestamp-only drift in `17-response.json`). **Phases 1-4 pending**: component primitive shim → unified `Adjuster` interface (one interface; Restater/Overlay/Hybrid roles from output) → view reconstruction → 13-site consumer migration including `WACCInputs` compile-time boundary and B3 contingent-liability reroute to `DebtLikeClaims`. Damodaran goodwill convention preserved (A1 Overlay excludes from `InvestedCapital`). See `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` + `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-0-implementation-plan.md`. |

**Phases 0-4 + IFRS-FPI + Graham-Floor (Tier 1) complete.** Engine at `CalculationVersion 4.1`. **Tier 2 in flight** (Bootstrap shipped 2026-05-16).

---

## Design Principles

1. **Valuation accuracy over engineering elegance** — frame all suggestions in terms of correctness first.
2. **Institutional approach** — industry-aware models, multi-stage growth, country risk, proper FCF. No shortcuts.
3. **No Monte Carlo** — user has explicitly rejected stochastic simulation as unnecessary.
4. **Graceful degradation** — the engine never fails completely; every layer has a fallback.
5. **Transparency** — every valuation includes quality score, warnings, cleaning adjustments, and sanity-check flags.
6. **Clean architecture** — domain layer (`internal/core/`) has zero external deps; all I/O via ports in `internal/core/ports/`.

---

## Out of Scope

- Monte Carlo / stochastic simulation
- Technical analysis / charting
- Portfolio optimization
- Trade execution
- Real-time streaming data (valuations are point-in-time)
- Front-end UI (API only; clients build their own)

---

## Known Follow-Ups (Tracked, Not Blocking)

Classifier / data-quality items (separate track — `docs/refactoring/industry-classification-unification-spec.md` + `docs/FEEDBACK-LOG.md`):

| ID | Severity | Description |
|----|----------|-------------|
| IC-1 | Architectural | SIC-only industry classification unification — retire the balance-sheet `ClassifyIndustry` heuristic in favor of SIC-based `Classify` everywhere. |
| IC-2 | Data | Owned-store retailers (TGT, HD, COST, LOW) misclassified as Industrials by heuristic — `isRetailCompany` rejects tickers with tangibles > 70% and intangibles < 10%. |
| IC-3 | Data | Some tickers (e.g., AMD) arrive at the heuristic with `ResearchAndDevelopment = 0` despite SEC XBRL having it — `isTechnologyCompany` misses them, fall through to Industrials. XBRL tag extraction investigation required. |

**Sweep of 2026-04-24/25** closed all open reviewer items across two sessions:
- 2026-04-24 (12 items): Q-1/Q-2/Q-3/Y-2 (landed earlier as `a7626f0`), D-1, D-2, B-2, S-1, S-4, V4.1 (N1–N11), PREX-1, M-1a, M-1f.
- 2026-04-25 (4 items + post-validation hotfix): M-1b (richer `industry_classification` trace), M-1c (raw `exit_multiple_tv` on terminal_value), M-1d (MinorityInterest + PreferredEquity end-to-end including SQLite persistence), M-1e (NewLogger probe-and-warn). Hotfix `fb01061` closed the validation-cycle BLOCKERs (persistence layer + service-level test).

`docs/reviewer/` now contains only `archive/`. Next time an issue surfaces, file a new doc there.

**Full tracking:** `docs/reviewer/archive/` for resolved history, `docs/FEEDBACK-LOG.md` for IC-*.

W-1..W-5 and S-2/S-3/S-5 were resolved in earlier commits (`4d46142`, `01f4db0`); the corresponding files in `docs/reviewer/archive/` are retained as historical records.

---

## Infrastructure Constraints

- **Local-only project** — no GitHub remote, no issue tracker. Work is tracked in `docs/reviewer/`, `docs/bugs/`, and daily logs.
- **Windows dev environment** — user is on Windows 11; some E2E tests are gated behind `E2E_LIVE=1`.
- **SEC User-Agent** — must include contact email; 10 req/sec hard limit (SEC policy).

---

## Recently Completed

| Initiative | Completed | Branch / Spec |
|------------|-----------|---------------|
| **Observability upgrade** — request correlation via context-scoped logger, file logging in local dev only, 12-stage DCF calc tracing, docker-compose cleanup | 2026-04-23 (all 5 phases) | `feat/observability` · `docs/refactoring/observability-upgrade-spec.md` |
| **Observability narrative + artifact capture (Phase 1)** — Tier-1 narrate stream (one Info line per pipeline phase, 17 closed-enum phases + `outcome` + free-text `notes`), Tier-2 Debug-tracer convention (`trace.<area>.<op>`), Tier-3 per-request artifact bundle (raw + parsed payloads, before/after pipeline snapshots, `99-narrate.jsonl` + `99-debug-trace.jsonl` streams via a `BundleSink` zapcore wrapper, self-describing manifest with schema versions, git SHA, build version, hard-coded redaction). Opt-in via `?trace=1` / `X-Midas-Trace: 1`. 7-day retention, 5 GiB cap, reaper goroutine. | 2026-04-25 (6 commits, merge `83cbfc7`) | `feat/observability-narrative` · `docs/refactoring/observability-narrative-and-artifacts-spec.md` |
| **Observability narrative + artifact capture (Phase 2.A — auto-on-error)** — adds a server-driven trigger that opens an artifact bundle for any request returning HTTP >= 500 even without `?trace=1`. New config knobs `logging.artifact_store.triggers.on_error` (default `false`) and `logging.artifact_store.pending_bytes_cap` (default 10 MiB). New exported constant `artifact.MaxStreamLineBytes = 256 KiB` enforces a per-line cap on the BundleSink stream so one giant Debug payload can't blow the buffer. Mechanism: when on_error is enabled, every request opens a deferred `*Bundle` that buffers Snapshot/AppendStream calls in memory, then either `Promote()`s to disk on 5xx or dissolves with zero side effects on 2xx/3xx/4xx. Manual `?trace=1` / `X-Midas-Trace` still wins for the manifest's `trigger` field. | 2026-04-27 (5 commits, merge `48a9578`) | `feat/observability-narrative-phase2` · `docs/refactoring/observability-narrative-and-artifacts-spec.md` |
| **Observability narrative + artifact capture (Phase 2.B — auto-on-quality-flag)** — adds a second server-driven trigger that opens a bundle when the data cleaner raises 1+ flags at-or-above a configurable severity. New config knob `logging.artifact_store.triggers.quality_flag_threshold` (default `""` = disabled, accepts `info`/`low`/`warning`/`medium`/`high`/`critical` with case+whitespace normalisation at config-load and a startup Warn on unknown values via new `internal/config/artifact_triggers.go`). New `entities.KnownFlagSeverities` canonical slice in core. New `artifact.TriggerOnQualityFlag` constant + Bundle methods (`RecordQualityFlagCount`/`QualityFlagCount`/`QualityFlagThreshold`). Cleaner hook in `CleanFinancialData` runs on BOTH cache-hit AND cache-miss paths via shared helper — the cache-hit path was REVIEWER's HIGH that would have silently bypassed the trigger for repeat-ticker requests. New `trace.bundle.promoted` Info line emitted on any auto-trigger Promote success so operators can `grep host.log | jq '.artifact_path'`. Precedence ladder: manual > on_quality_flag > on_error; Promote called exactly once per request. | 2026-04-29 (10 commits, merge `fa89aa2`) | `feat/observability-narrative-phase2b` · `docs/refactoring/observability-narrative-and-artifacts-spec.md` |
| **Observability narrative + artifact capture (Phase 2.C — always-on knob)** — adds a third server-driven trigger that bundles EVERY request regardless of error/flag state, gated by `logging.artifact_store.triggers.always` (default `false`). Intended for sustained debugging sessions ("flip on for an hour, flip off when done"). New `artifact.TriggerAlways = "always"` constant. Precedence ladder updated to **manual > on_quality_flag > on_error > always** (always sits at the BOTTOM as the catch-all — a 5xx under `always=true` records `trigger=on_error` so postmortem readers see the more diagnostic label). Two operator-UX hardening fixes shipped alongside: (1) `trace.bundle.promoted` Info line is SUPPRESSED for `trigger=always` to avoid host-log flood (would have emitted 6,000 lines/min at 100 req/s), still emitted for `on_error`/`on_quality_flag`. (2) New boot Warn `config.artifact_store.always_on_active` fires when knob is set, ensuring an operator who flipped it and forgot gets a loud reminder at next deploy. `ValidateArtifactTriggers` refactored to drop early returns so both Phase 2.B threshold-typo Warn and Phase 2.C always-on Warn can fire on the same boot. Phase 2.D (replay tooling) remains deferred — see spec §13. | 2026-05-01 (4 commits, merge `6e3ad8f`) | `feat/observability-narrative-phase2c` · `docs/refactoring/observability-narrative-and-artifacts-spec.md` |
| **Observability narrative + artifact capture (Phase 2.D — replay tooling, COMPLETE)** — `cmd/replay/main.go` re-runs captured artifact bundles through the current valuation engine and diffs the produced response against the saved `17-response.json`. Hermetic by construction (no production DB / Redis / metrics / scheduler / external API). 14-flag CLI: `--format`, `--out`, `--allow-schema-drift`, `--allow-git-drift`, `--quiet`, `--verbose`, `--from=raw\|parsed`, `--workers`, `--filter-ticker`, `--filter-since`, `--float-rel-tol`, `--float-abs-tol`, `--diff-stages`. Stage K's `--diff-stages` reads bundle JSON files (`10-clean-output.json`, `12-growth-curve.json`, `13-wacc.json`, `15-valuation.json`) directly via `os.ReadFile` and diffs against an ephemeral in-memory snapshot of the current engine's output (D7 invariant: no bundles of bundles). Stage O.6 `init()` reflection guard panics at package load if `countFairValueFields() = 36` disagrees with `reflect.TypeFor[FairValueResponse + Industry + SanityCheck]().NumField()` — catches future struct-shape drift before any runtime code runs. Per-instance `*prometheus.Registry` isolation enforced by `lint-prometheus-registers.{sh,ps1}` CI guard. `cmd/server` ↔ `replay` import-boundary CI guard (`cmd/server/import_boundary_test.go`) confines replay-package init() panic safety. Performance: NF2 single-bundle ≤200ms / NF3 100-bundle batch ≤30s, both with 3× CI slack — measured local NF2 3.5ms / NF3-seq 329ms / NF3-par 87ms (orders of magnitude under SLA). 4 sub-merges. | 2026-05-03 → 2026-05-09 (45 commits across 4 merges: R0+R1 `8a9878f` 2026-05-03, R2 `e4d2fb2` 2026-05-05, R3a `011d78c` 2026-05-06, R3b `0741958` 2026-05-09) | `docs/refactoring/observability-replay-tooling-spec.md` v0.5 (carved out from §13 of the parent narrative spec during R0+R1 dispatch) |

## In Flight

_No initiatives currently in flight._

## Next Candidate Work (Ranked)

No commitment yet — listed for future prioritization:

1. **Accuracy validation** — systematic comparison of Midas valuations against benchmarks (analyst consensus, implied prices). User has flagged this as a gap.
2. **Close the W-4 coverage gap** — bring `models/` to 90%+.
3. **Fix S-1/S-4** — make config loading robust for Docker deployments.
4. **Sector-specific validation sets** — test bank valuations against known bank valuations, REIT valuations against REIT benchmarks, etc.
5. **BUG-012 + RPL-4** — BUG-012 (runtime Warn on artifact-bundle buffer drops, inherited from Phase 2.A as B-4) and RPL-4 (Phase 2.D R3b's 4 deferred items: spec L.1 sample-section-order documentation call, Windows backslash JSON path normalization, cleaner `as_of` nondeterminism — out of replay scope, replay-package coverage residual) tracked in `docs/bugs/` and `docs/reviewer/RPL4-r3b-followups.md`.
6. **Close G-1 follow-up** — `growth.estimated` narrate phase emits coarse `analyst_weight=0.5/historical_weight=0.5` because `growth.Result` doesn't expose the actual blend math. Fix described in `docs/reviewer/G1-growth-blend-weights-coarse.md`.

---

## How to Apply This File

- **Before starting a new feature**: check whether it fits the Mission and isn't in Out of Scope.
- **Before architectural changes**: verify they align with Design Principles (esp. #1, #6).
- **When prioritizing**: use Known Follow-Ups and Next Candidate Work as the queue; don't invent new scope without user confirmation.

---

## Change Log

| Date | Change |
|------|--------|
| 2026-04-18 | Initial file. Promoted content from `.claude/.../memory/project_upgrade_status.md`, `project_midas_overview.md`, `user_role.md`. |
| 2026-04-23 | Added IC-1/IC-2/IC-3 follow-ups tracking industry-classification unification and two live-QA data gaps (owned-store retail misclassification, missing R&D for some semiconductor filings). Context: AMD retail-misclassification hotfix + Industry-in-response feature. |
| 2026-04-25 | M-1 sweep closed (M-1a..f + post-validation hotfix `fb01061`); `docs/reviewer/` is now empty of open items. Drafted `docs/refactoring/observability-narrative-and-artifacts-spec.md` (Tier-1 narrate / Tier-2 Debug-tracer / Tier-3 artifact bundle) as next In-Flight initiative; Phase 1 scoped (manual-trigger only), Phase 2 (auto-triggers) explicitly deferred. Schema migration `0006_add_minority_interest_preferred_equity.sql` landed alongside the M-1d equity-bridge fix. |
| 2026-04-25 | **Phase 1 of observability narrative + artifact capture merged to master** as `83cbfc7` (preceded by 6 feature commits on `feat/observability-narrative`). Full BACKEND → VERIFIER → REVIEWER → QA validation cycle ran (REVIEWER round 1 caught BLOCKER + 2 HIGH, fixed in `41bd91c`; REVIEWER round 2 caught 1 HIGH duplicate `request_id` in host stream, fixed in `1e1c6fc`; QA round 2 returned READY TO MERGE). Coverage gates met (narrate 96.4%, artifact 90.2%, middleware 93.7%). G-1 follow-up filed for `growth.estimated` weight precision. In Flight section emptied; Phase 2 deferred work surfaced as Next Candidate Work item #5. |
| 2026-04-27 | **Phase 2.A (auto-on-error trigger) of observability narrative + artifact capture merged to master** as `48a9578` (5 commits on `feat/observability-narrative-phase2`: `f937286`, `f892448`, `b27b317`, `2bca707`, `8dab2e6`). Full BACKEND → VERIFIER → REVIEWER → QA validation cycle ran with one fix-up round (REVIEWER round 1 caught 4 HIGH — Promote stream-flush truncation, Close/Promote race goroutine leak, no per-line size cap on BundleSink, panic skips Promote — all fixed in `f892448` + `b27b317`; VERIFIER round 2 caught a test-harness HIGH — pre-existing `TestSetTicker_RenameFailureCountedAsWriteError` leaked a worker goroutine that tripped the new goleak test under `-count=3` — fixed in `2bca707`; QA APPROVED-FOR-MERGE with 1 MINOR — `artifact_path` populated on `response.sent` even when Promote failed — fixed in `8dab2e6`). Coverage gates met (artifact 90.5%, middleware 95.1%). Race-detector clean under `-count=3`. Item #5 in Next Candidate Work narrowed to 2.B / 2.C / 2.D. |
| 2026-04-29 | **Phase 2.B (auto-on-quality-flag trigger) of observability narrative + artifact capture merged to master** as `fa89aa2` (10 commits on `feat/observability-narrative-phase2b`: `c4d0194`, `1e38103`, `d90b378`, `02beb0d`, `f8d38af`, `afe75a1`, `6d2e669`, `7180422`, `75aeb17`, `e01fd6d`). Full BACKEND → VERIFIER → REVIEWER → QA validation cycle ran with one fix-up round (REVIEWER round 1 returned APPROVE-WITH-MINOR but flagged 1 HIGH — cleaner cache-hit short-circuit silently bypassed the trigger for repeat-ticker requests — plus 2 MEDIUM — threshold typos silently disable, missing `trace.bundle.promoted` operator-greppable Info line — and 2 LOW — `severityRank` future-proofing test, dead-path comment; all fixed in `afe75a1` + `6d2e669` + `7180422` + `75aeb17`; VERIFIER round 2 returned VERIFIED with no findings; QA APPROVED-FOR-MERGE with 2 MINOR — missing dedicated panic test for on_quality_flag fixed in `e01fd6d`, runtime-Warn-on-drops gap filed as BUG-012 for follow-up). Coverage gates met (artifact 90.8%, middleware 95.5%, datacleaner 40.8%). Race-detector clean under `-count=3`. Item #5 in Next Candidate Work narrowed to 2.C / 2.D. New canonical `entities.KnownFlagSeverities` slice in core entities, wired into both runtime validation (`internal/config/artifact_triggers.go::ValidateArtifactTriggers`) and the severity-rank exhaustiveness test. |
| 2026-05-01 | **Phase 2.C (always-on knob) of observability narrative + artifact capture merged to master** as `6e3ad8f` (4 commits on `feat/observability-narrative-phase2c`: `4d9d5f9`, `b1ab077`, `f4423e6`, `c9e6201`). Full BACKEND → VERIFIER → REVIEWER → QA validation cycle ran with one fix-up round (REVIEWER round 1 returned APPROVE-WITH-MINOR but flagged 2 HIGH — `trace.bundle.promoted` Info-line floods host log when `always=true`, no boot-time Warn that always-on is active — plus 2 MEDIUM — operator-facing DoS caveat lives in source comments only, missing dedicated panic test for `always` trigger — and 3 LOW; all 4 in-scope fixes shipped in `b1ab077` + `f4423e6` + `c9e6201`; QA APPROVED-FOR-MERGE with 1 MEDIUM + 3 LOW; the MEDIUM is a pre-existing deferred-bundle SetTicker bug that affects all three auto-triggers — Phase 2.C made it the dominant case because `always=true` hits the path on every request; filed as BUG-013 for dedicated commit). Coverage gates met (artifact 90.8%, middleware 95.6%, config 50.3%). Race-detector clean under `-count=3`. Item #5 in Next Candidate Work narrowed to 2.D only. `ValidateArtifactTriggers` refactored to drop early returns so both Phase 2.B threshold-typo Warn and Phase 2.C always-on Warn can fire on the same boot. |
| 2026-05-02 | **BUG-013 (deferred-bundle `SetTicker` no-op on `b.root`) closed** as merge `621f805` on master (2 commits on `fix/bug013-deferred-bundle-set-ticker`: `f86d067` operator-UX fix + `021e362` REVIEWER follow-up for the latent `b.root` race + TOCTOU). With Phase 2.C's always-on knob shipped 2026-05-01, every auto-triggered bundle was landing under `_no-ticker/<request_id>/` with `outcome="partial"` (spurious `writeErrors=1` from the failed `os.Rename` against a not-yet-existent on-disk dir). Option A from the BUG-013 spec applied: in deferred mode `SetTicker` skips `os.Rename` and updates `b.root` in memory; `Promote()` `MkdirAll`s at the correct per-ticker path. REVIEWER round 1 caught HIGH-A (Promote read `b.root` without holding `b.mu` — pre-existing race surface that BUG-013 fix made writable; snapshot under `b.mu` mirroring `runWorker` pattern) + MEDIUM-A (TOCTOU between `b.deferred.Load()` and `b.mu.Lock()` in `SetTicker`; re-check-after-lock under `pendingMu→b.mu` chosen against REVIEWER's suggested reverse direction to honor `bufferStream`'s documented prohibition). QA performed independent lock-order audit (10 `b.mu` sites, 5 `pendingMu` sites; ZERO `b.mu→pendingMu` nestings anywhere; exactly two new `pendingMu→b.mu` nestings). 150 race-detector iterations clean (`-race -count=3`). Live disk inspection: bundle landed at `artifacts/<date>/AAPL/req_<id>/` with manifest `outcome="ok"` and no `notes` field. Coverage 90.3% (above 90% gate; 0.5pp dip from the deliberately non-deterministic race-test branches). BUG-013 archived to `docs/bugs/archive/`. Item #5 in Next Candidate Work narrowed: only BUG-012 + Phase 2.D remain pending. |
| 2026-05-09 | **Phase 2.D (replay tooling) ALL R0–R3 SHIPPED. Phase 2.D = COMPLETE.** Promoted Phase 2.D from Next Candidate Work item #5 to Recently Completed table as one consolidated row covering all 4 sub-merges (R0+R1 `8a9878f` 2026-05-03 with 8 commits — `valuation.Clock` injection + replay skeleton + manifest validation + schema-drift detection + bundle walk + diff helpers + ErrBundleMissingPayload sentinel + text/JSON renderers; R2 `e4d2fb2` 2026-05-05 with 17 commits — bundle gateways for SEC/Market/Macro/YFinance + side-effect stubs + `replay.Module` fx composition + `Replay()` orchestrator + `--from=raw\|parsed`; R3a `011d78c` 2026-05-06 with 11 commits — Pre-Flight parallel-fx.App spike + `--workers` + `--filter-ticker`/`--filter-since` + `--float-rel-tol`/`--float-abs-tol` + walk/replay timing + Stage O sweep + `cmd/server` import-boundary CI guard; R3b `0741958` 2026-05-09 with 11 commits — `--diff-stages` engine wiring + `stage_diff.go` + verbose stage-diff text render + `seedFullBundle_ParsedMode` + 6 JSON contract golden fixtures + perf benches NF2/NF3 + `init()` reflection guard for `countFairValueFields` + V/R/Q polish). Item #5 in Next Candidate Work updated to drop Phase 2.D mention; only BUG-012 + RPL-4 (R3b's 4 deferred items) remain on item #5. R3b's V/R/Q gate cycle returned zero MAJOR/BLOCKER findings — the cleanest gate cycle of any Phase 2.D dispatch. RPL-3 marked RESOLVED with per-RPL-3 commit-SHA mapping. RPL-4 filed at `docs/reviewer/RPL4-r3b-followups.md` (4 deferred items: spec/sample documentation call, Windows backslash JSON path normalization, cleaner as_of nondeterminism, documented coverage residual per spec §6 escape clause). Spec bumped v0.4 → v0.5; AGENTS.md Tier 4 entry #17 updated; CLAUDE.md Build & Run gained replay CLI examples + Phase 2.D = COMPLETE noted in the "Replay tooling is hermetic by construction" gotcha. |
