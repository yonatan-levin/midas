# RPL-9 — Bundle manifest doesn't snapshot production config → replay-side mirrors by hand-copy (brittle)

**Status:** OPEN — filed 2026-05-14 as the durable fix for the bug class that produced debug cycles 1+2+3.
**Severity:** MEDIUM (architectural debt) — no current symptom, but the same bug class will recur until this is closed.
**Origin:** Identified during debug cycle 2 root-cause analysis; forward-referenced in `internal/observability/replay/module.go` inline comment.

## The problem class

Cycle 2 of the replay-fidelity debug found that `replayConfig.DCFMaxGrowthRate` was hardcoded to `0.40` while production's viper default was `0.50`. The 0.10 cap divergence cascaded into a 9-field drift on every MXL replay.

Cycle 3 found a **latent twin**: `replayConfig.DefaultTerminalGrowthCap` was hardcoded to `0` (unset) while production was `0.03`. Latent until a sparse-historical bundle hits the `CalculateAverageGrowthRate` error fallback.

**Both bugs share the same root cause**: replay-side `replayConfig()` mirrors production's viper defaults by hand-copy. If a future production config change (e.g., bumping `DefaultTaxRate` from 0.21 to 0.22, or `DCFProjectionYears` from 5 to 7) doesn't also update `replay/module.go`, replay-side will silently diverge for any bundle captured under the new production config.

This bug class will keep recurring until the manifest snapshots the resolved config the request ran under.

## Durable fix

**Stamp the resolved config into the bundle manifest at capture time; replay reads it back instead of hand-copying defaults.**

Concretely:
1. At capture time, after viper resolves the effective config, write the relevant subset (at minimum `ValuationConfig`) to `02-handler-options.json` (already exists as a partial snapshot) OR a new dedicated file like `00-config.json`.
2. The replay-side `replayConfig()` reads the bundle's saved config instead of hardcoded defaults.
3. Backward-compat: bundles captured before this fix use today's hand-copied fallback (preserves existing replay behavior for old bundles).

After this lands, the entire class of "replay-side and production-side config diverge" bugs becomes impossible by construction.

## Scope

The minimum viable subset to snapshot:
- All `Valuation.*` fields used downstream of `valuation.Service` (DCFMaxGrowthRate, DCFMinGrowthRate, DefaultTerminalGrowthCap, DefaultTaxRate, DefaultMarketRiskPremium, DCFProjectionYears, DCFIterationTolerance, DCFMaxIterations, MinDataPointsForGrowth)
- `Macro.ManualRiskFreeRate`, `Macro.ManualMarketRiskPremium` (used as live fallbacks when FRED is unavailable)
- Potentially `DataCleaner.*` thresholds that affect cleaning rules

Out of scope for this tracker (these are runtime-only, not algorithmic): server timeouts, cache TTLs, log levels.

## Acceptance criteria

- [ ] Capture path writes the effective valuation config to the bundle.
- [ ] Replay path reads the bundle's config (with fallback to current hand-copy for old bundles).
- [ ] Golden test: introduce a config divergence between replay/module.go and production viper defaults; verify replay STILL produces zero-diff because the bundle's saved config overrides.
- [ ] Bundle version bumped 1.1 → 1.2.
- [ ] Old 1.0 + 1.1 bundles continue to replay (with the hand-copy fallback).
- [ ] Inline comment in `replay/module.go` cycle-2 block removed (the manifest now carries the source of truth; no more hand-mirror discipline needed).

## Estimated effort

Medium — ~1-2 days. Touches capture-side artifact writer + replay-side gateway + new test that asserts capture-replay config parity.

## Traceability

- Class-of-bug forward-referenced in `internal/observability/replay/module.go` cycle-2 inline comment (currently says "(tracker filing pending)" — will be updated to `(RPL-9)` at this filing).
- Two instances of this bug class fixed by hand:
  - Cycle 2 (`96501c8`): DCFMaxGrowthRate / DCFMinGrowthRate
  - Cycle 3 (`e1496c9`): DefaultTerminalGrowthCap
- Related: RPL-10 (defense-in-depth — until RPL-9 lands, replay should mirror ALL production defaults with a golden parity test).
