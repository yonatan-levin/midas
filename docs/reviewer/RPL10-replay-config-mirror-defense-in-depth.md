# RPL-10 — Defense-in-depth: `replayConfig` should mirror ALL production viper defaults with golden parity test

**Status:** OPEN — filed 2026-05-14 as a stopgap until RPL-9 (manifest-config-snapshot) lands.
**Severity:** LOW — no current symptom; all currently-zero fields are unconsumed by replay code paths.
**Origin:** VERIFIER cycle 2 of the replay-fidelity debug — config audit finding.

## The setup

`internal/observability/replay/module.go: replayConfig()` constructs a `*config.Config` for replay's fx wiring. It hand-mirrors production viper defaults from `internal/config/config.go: setDefaults()`.

As of the debug cycle 3 fix (`e1496c9`), the following Valuation fields are now correctly mirrored:
- `DCFMaxGrowthRate: 0.50`
- `DCFMinGrowthRate: -0.30`
- `DefaultTerminalGrowthCap: 0.03`

But **10+ other Valuation fields remain zero-valued** in replayConfig despite production having non-zero viper defaults:

| Field | Production default | Replay value | Currently consumed in replay? |
|-------|-------------------|--------------|-------------------------------|
| `DCFProjectionYears` | 5 | 0 (unset) | NO |
| `DefaultTaxRate` | 0.21 | 0 (unset) | NO |
| `DefaultMarketRiskPremium` | 0.05 | 0 (unset) | NO |
| `DCFIterationTolerance` | 0.0001 | 0 (unset) | NO |
| `DCFMaxIterations` | 100 | 0 (unset) | NO |
| `MinDataPointsForGrowth` | 2 | 0 (unset) | NO |
| `MaxBulkSize` | 50 | 0 (unset) | NO |
| `CacheTTL` | 1h | 0 (unset) | YES (no-op via NotFoundCacheRepo) |
| `SlowRequestThreshold` | 5s | 0 (unset) | NO |
| `DataFetchTimeout` | 10s | 0 (unset) | NO |
| `Macro.ManualRiskFreeRate` | 0.045 | 0 (unset) | NO (replay uses bundle's macro data) |

**None are currently consumed by replay-reachable code paths** (verified by grep). But they create the same trap that produced cycles 1+2+3: if a future engine change starts reading any of them, replay would silently diverge.

## The class of bug

Cycles 1+2+3 of the replay-fidelity debug each fixed one or two instances of "replay-side config field hand-copied wrong from production default." Three cycles produced fixes for `DCFMaxGrowthRate`, `DCFMinGrowthRate`, and `DefaultTerminalGrowthCap`. The other 10+ unset fields are LATENT instances of the same pattern.

## Fix

Two layers:

### Layer 1 — Mirror all production defaults in replayConfig (defense in depth)

For each field in `internal/config/config.go: setDefaults()`'s viper key under `valuation.*`, ensure `replayConfig.Valuation` has the same value. Even if no current consumer reads it, mirroring closes the door.

### Layer 2 — Golden parity test

Add a unit test that uses reflection (or struct-tag comparison) to assert every field in `config.ValuationConfig` matches between `replayConfig().Valuation` and a freshly-viper-defaulted Config. The test catches future drift the moment someone adds a new viper default without mirroring it.

```go
func TestReplayConfig_MirrorsAllValuationViperDefaults(t *testing.T) {
    // Build a fresh viper config with all setDefaults applied
    productionDefaults := config.LoadDefaults()  // or equivalent
    replayCfg := replayConfig()
    
    // For each field in ValuationConfig, assert equality
    if !reflect.DeepEqual(productionDefaults.Valuation, replayCfg.Valuation) {
        diff := cmp.Diff(productionDefaults.Valuation, replayCfg.Valuation)
        t.Fatalf("replayConfig.Valuation drifts from production viper defaults:\n%s", diff)
    }
}
```

This test would have caught all three cycle 1/2/3 bugs at compile-time.

## This becomes obsolete when RPL-9 lands

RPL-9 (bundle manifest snapshots resolved config) is the durable fix. RPL-10 is the stopgap mirror parity assertion. Once RPL-9 lands, the bundle carries the canonical config and replayConfig becomes a fallback for old bundles only — at which point the parity test gets repurposed to assert `old-bundle-fallback ↔ production defaults` parity, which is a smaller surface.

## Acceptance criteria

- [ ] All non-zero production `Valuation.*` viper defaults appear in `replayConfig()`.
- [ ] Golden parity test added; fails if any future production default drifts from replay mirror.
- [ ] (Optional) Same parity coverage for `Macro.*` fields that affect replay paths.

## Estimated effort

Small — ~30 min. Mostly mechanical mirror + one new test.

## Traceability

- Filed by: VERIFIER cycle 2 of replay-fidelity debug (2026-05-14) during the config-drift audit.
- Related fixed instances: cycles 1+2+3 of the debug, commits `4290266`, `96501c8`, `e1496c9`.
- Made obsolete by: RPL-9 (manifest-config-snapshot, the durable fix).
- Audit table reference: see VERIFIER cycle 2's findings table in the debug session output.
