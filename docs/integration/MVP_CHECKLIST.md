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
