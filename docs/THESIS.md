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

These are known gaps from Phase 4 review. Scheduled for post-MVP cleanup.

| ID | Severity | Description |
|----|----------|-------------|
| S-1 | Structural | Config file paths are relative to working directory (fragile in Docker) |
| S-4 | Structural | Model constructors perform I/O (`os.ReadFile`) |
| IC-1 | Architectural | SIC-only industry classification unification — retire the balance-sheet `ClassifyIndustry` heuristic in favor of SIC-based `Classify` everywhere. Tracked in `docs/refactoring/industry-classification-unification-spec.md`. |
| IC-2 | Data | Owned-store retailers (TGT, HD, COST, LOW) misclassified as Industrials by heuristic — `isRetailCompany` rejects tickers with tangibles > 70% and intangibles < 10%. See `docs/FEEDBACK-LOG.md` 2026-04-24. |
| IC-3 | Data | Some tickers (e.g., AMD) arrive at the heuristic with `ResearchAndDevelopment = 0` despite SEC XBRL having it — `isTechnologyCompany` misses them, fall through to Industrials. XBRL tag extraction investigation required. See `docs/FEEDBACK-LOG.md` 2026-04-24. |

**Full tracking:** `docs/reviewer/` for S-*; `docs/FEEDBACK-LOG.md` for IC-*.

W-1, W-2, W-3, W-4, and S-5 were resolved in commit `4d46142`; the corresponding files in `docs/reviewer/` are retained as historical records.

---

## Infrastructure Constraints

- **Local-only project** — no GitHub remote, no issue tracker. Work is tracked in `docs/reviewer/`, `docs/bugs/`, and daily logs.
- **Windows dev environment** — user is on Windows 11; some E2E tests are gated behind `E2E_LIVE=1`.
- **SEC User-Agent** — must include contact email; 10 req/sec hard limit (SEC policy).

---

## Next Candidate Work (Ranked)

No commitment yet — listed for future prioritization:

1. **Accuracy validation** — systematic comparison of Midas valuations against benchmarks (analyst consensus, implied prices). User has flagged this as a gap.
2. **Close the W-4 coverage gap** — bring `models/` to 90%+.
3. **Fix S-1/S-4** — make config loading robust for Docker deployments.
4. **Sector-specific validation sets** — test bank valuations against known bank valuations, REIT valuations against REIT benchmarks, etc.

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
