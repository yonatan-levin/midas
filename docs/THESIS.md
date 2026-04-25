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

**All planned phases are complete.** The engine is at `CalculationVersion 4.0`.

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
| **Observability narrative + artifact capture (Phase 1)** — Tier-1 narrate stream (one Info line per pipeline phase, 17 closed-enum phases + `outcome` + free-text `notes`), Tier-2 Debug-tracer convention (`trace.<area>.<op>`), Tier-3 per-request artifact bundle (raw + parsed payloads, before/after pipeline snapshots, `99-narrate.jsonl` + `99-debug-trace.jsonl` streams via a `BundleSink` zapcore wrapper, self-describing manifest with schema versions, git SHA, build version, hard-coded redaction). Opt-in via `?trace=1` / `X-Midas-Trace: 1`. 7-day retention, 5 GiB cap, reaper goroutine. Phase 2 (auto-on-error / auto-on-quality-flag / always-on / replay tooling) explicitly deferred — see spec §13. | 2026-04-25 (6 commits, merge `83cbfc7`) | `feat/observability-narrative` · `docs/refactoring/observability-narrative-and-artifacts-spec.md` |

## In Flight

_No initiatives currently in flight._

## Next Candidate Work (Ranked)

No commitment yet — listed for future prioritization:

1. **Accuracy validation** — systematic comparison of Midas valuations against benchmarks (analyst consensus, implied prices). User has flagged this as a gap.
2. **Close the W-4 coverage gap** — bring `models/` to 90%+.
3. **Fix S-1/S-4** — make config loading robust for Docker deployments.
4. **Sector-specific validation sets** — test bank valuations against known bank valuations, REIT valuations against REIT benchmarks, etc.
5. **Observability narrative & artifacts — Phase 2** — auto-trigger bundle capture on errors, on data-quality flags, and an "always-on" debugging knob; replay tooling. Tracked in §13 of `docs/refactoring/observability-narrative-and-artifacts-spec.md`; file individual `docs/reviewer/` items when prioritized.
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
