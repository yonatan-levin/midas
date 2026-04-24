# MVP Readiness Checklist (v0.9.0-rc1)

- [X] All tests green (go test ./...) on CI and dev
- [X] Integration coverage >= 90% (internal/integration)
- [X] Contract fuzzing: no 5xx on invalid inputs
- [X] p95 < 300ms at 20 RPS for 60s (dev baseline) – see docs/PERF_BASELINE.md
- [X] Auth & permissions enforced on protected routes
- [X] Metrics & health endpoints respond (with auth for detailed)
- [X] Config via env/Viper; no hard-coded secrets
- [X] Rate limiting active with graceful fallback
- [X] Tag v0.9.0-rc1 and accept only bug-fix PRs

Notes
- Live E2E gated by E2E_LIVE=1 and skipped on Windows; CI runs non-Windows.
- Redis test dependency is OS-specific; Windows tests use in-memory fallback.

---

## Archived (verified 2026-04-23)

**Classification:** OBSOLETE / COMPLETED

**Reason:** All 9 checklist items are checked; the v0.9.0-rc1 tag was cut and the MVP shipped. Subsequent work (Phases 0-4 of the valuation-engine upgrade) has happened on top of this MVP baseline. The checklist has served its gate function.

**Superseded by:** `docs/THESIS.md` (tracks current version and phase status for the whole project).

**Evidence inspected:**
- `docs/THESIS.md` lines 26-40 list v0.9.0-rc1 as the MVP baseline with phases 0-4 complete on top.
- All checklist items (tests green, integration coverage ≥90%, contract fuzzing, p95<300ms, auth enforced, metrics/health, Viper config, rate limit, tag v0.9.0-rc1) have verifiable counterparts still in the codebase.
