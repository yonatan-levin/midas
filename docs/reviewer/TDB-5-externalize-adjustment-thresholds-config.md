# TDB-5 — Externalize datacleaner adjustment thresholds to config

**Status:** IMPLEMENTED 2026-06-08 (branch `worktree-tdb-5-threshold-config`) — VERIFIER VERIFIED (full suite 50/0, shadow exit 0, DDM bit-for-bit, default==constant pin, override proves config flows); REVIEWER APPROVE_WITH_NITS (the catastrophic zero-gate path proven impossible across 3 layers); QA PASS. Filed 2026-06-06 (TODO-catalog burn-down pass); design authored 2026-06-08.
**Priority:** P2 — Tier 2 (maintainability).
**Type:** Enhancement / tech-debt.
**Mirrored as GitHub issue:** `[TDB-5]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "Configuration System" (`adjustments/liabilities.go:17,27` + `adjustments/assets.go:14`).

**Design artifacts (2026-06-08, ROLE: ARCH):**
- Spec: `docs/refactoring/spec/tdb-5-adjustment-thresholds-config-spec.md`
- Implementer plan: `docs/refactoring/implementations/tdb-5-adjustment-thresholds-config-implementation-plan.md`

**Scope decision (see spec §2/§3/§9):** first cut externalizes the **flat asset-adjuster
materiality/review gate constants** (A1 5%/10%, A2 2%, A4 5%/10%, A6 5%/10%, A-RD 10%, A-SW 1.5%)
into `config/datacleaner/adjustment_thresholds.json` via the existing `LoadFlagConditionsConfig`
loader pattern, injected through `NewAssetAdjusterWithThresholds`. **Deferred:** industry-keyed
B-rule tables (inventory/lease/pension/contingent), treatment rates (A2 tiers / A4 50% / A5 40%),
and already-externalized `rule.Threshold`-driven gates (C1/C3/C7/A7). Load-bearing invariant:
**default == legacy constant ⇒ byte-identical behaviour** when config is absent/partial.

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
- [x] Thresholds read from config with safe defaults
- [x] Behavior unchanged when config is absent (regression-safe)
- [x] Tests cover default + override

## Implementation status (2026-06-08 — VERIFIED / APPROVE_WITH_NITS / PASS)

Implemented per the plan on `worktree-tdb-5-threshold-config`:

- **New:** `internal/services/datacleaner/adjustments/asset_thresholds.go` — `AssetThresholds`
  struct (9 fields) + `DefaultAssetThresholds()` returning the pre-TDB-5 literals. Pinned by
  `TestDefaultAssetThresholds_EqualLegacyConstants` (default == legacy constant).
- **New:** `internal/config/adjustment_thresholds_config.go` — `LoadAdjustmentThresholdsConfig`
  (path → `ADJUSTMENT_THRESHOLDS_CONFIG_PATH` env → default path) with `*float64` pointer fields
  (missing-vs-zero) and `Validate()` (version required; present ratios in `(0,1]`).
- **New:** `internal/services/datacleaner/adjustment_thresholds_resolve.go` —
  `ResolveAssetThresholds(def, cfg)` overwrites only non-nil config keys. Placed in `datacleaner`
  (not `config`) because `adjustments` transitively imports `config`, so a `config→adjustments`
  edge would close an import cycle.
- **Modified:** `adjustments/assets.go` — `AssetAdjuster` gains a `thresholds` field; the 9 gate
  literals (A1/A2/A4/A6/A-RD/A-SW) now read `aa.thresholds.*`; `NewAssetAdjuster()` stays zero-arg
  (defaults — 44 callers unchanged); added `NewAssetAdjusterWithThresholds(...)`. The
  `assets.go:14` TODO is removed.
- **Modified:** `service.go` (warn-and-fallback load → resolved thresholds wired into the
  production `AssetAdjuster`) + `config.go` (`ThresholdsPath` field on `DataCleanerConfig`).
- **Modified:** `adjustments/liabilities.go:17` TODO replaced with a follow-up note (the B-rule
  industry-keyed gates are deferred per spec §3.2). `liabilities.go:32` lease-config TODO left as-is
  (unrelated to TDB-5 gate thresholds).
- **New:** `config/datacleaner/adjustment_thresholds.json` — values byte-equal to defaults; pinned
  by `TestShippedConfig_ResolvesToDefaults`.

**Deferred (follow-up):** industry-keyed B-rule tables (A5/B1/B2/B3) and treatment rates
(A4 50% / A5 40% / A2 tiers / C1 1.5%) — out of scope per spec §3.2/§3.3.

**Validation (VERIFIER, independent re-run):** `GOWORK=off go build/vet ./...` exit 0; full
`go test ./... -count=1` exit 0 (50 packages, 0 FAIL); shadow gate
(`git diff --quiet internal/integration/testdata/recompute-shadow/`) exit 0; DDM bit-for-bit /
recompute-no-mutation / ledger-ordering / basket (incl. T2-BS-3 AMD/KO) invariants green;
override test confirms a non-default threshold flips a real gate decision (injection is live).

## Deferred follow-up NITs (REVIEWER 2026-06-08, non-blocking)
- `service.go`'s threshold load is "warn-and-fallback" but the fallback branch logs nothing on a
  missing/invalid override file (a faithful copy of the adjacent `flag_conditions.json` load, which
  carries its own standing `// TODO: Add proper logging`). Add a `zap` WARN so an operator who ships
  a malformed override learns it was silently dropped to defaults — fold into the existing logging
  TODO, not a TDB-5 defect.
- Deprecated doc-only path `CalculateNetTangibleAssets` (`assets.go:1675-1729`) still has hardcoded
  `0.05`/`0.10` review literals — out of scope (not `Apply*` gates, no balance-sheet mutation),
  intentionally untouched.
