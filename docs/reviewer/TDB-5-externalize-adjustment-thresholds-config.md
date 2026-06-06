# TDB-5 — Externalize datacleaner adjustment thresholds to config

**Status:** OPEN — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P2 — Tier 2 (maintainability).
**Type:** Enhancement / tech-debt.
**Mirrored as GitHub issue:** `[TDB-5]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "Configuration System" (`adjustments/liabilities.go:17,27` + `adjustments/assets.go:14`).

---

## Context

Adjustment thresholds are hardcoded in the adjuster files with standing TODOs:
- `adjustments/liabilities.go:17` "Add configuration for adjustment thresholds"
- `adjustments/liabilities.go:27` "Load configuration from proper source"
- `adjustments/assets.go:14` (same pattern)

Externalizing them (viper / JSON config, per the project convention used for `config/datacleaner/*.json`) makes materiality thresholds tunable without a recompile and auditable in one place.

## Scope / Tasks

| ID | Task | File | Effort |
|---|---|---|---|
| TDB-5.1 | Define a threshold config schema (mirror existing `config/datacleaner/*.json` style) | `config/datacleaner/` | S |
| TDB-5.2 | Load it through the existing config plumbing; inject into adjusters | `internal/config`, adjusters | M |
| TDB-5.3 | Safe defaults if config absent (preserve current behavior) | adjusters | S |
| TDB-5.4 | Tests: default + overridden thresholds | adjuster tests | S |

## Acceptance
- [ ] Thresholds read from config with safe defaults
- [ ] Behavior unchanged when config is absent (regression-safe)
- [ ] Tests cover default + override
