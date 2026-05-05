# Observability Replay Tooling — Phase R2 Implementation Plan

**Status:** R2 SHIPPED on master (merge `e4d2fb2`, 2026-05-05). All Stages (Pre-Flight + D1.1 + A.1 + A.2 + A.3 + A.4 + A.6 + B + C + D + E + F + G) executed per plan across 3 BACKEND dispatches; A.5 (Finzive) skipped per plan §4 (Finzive not wired in production). Validated through 4 review gates (VERIFIER × 2, REVIEWER, QA). 15 advisory follow-ups deferred to R3, tracked at `docs/reviewer/RPL2-r2-followups.md`. This plan is now historical — kept for traceability.

---

## Revision History

- **v2 (2026-05-04 — revision pass)**: Tightened Surface #3 (macro raw-mode parser extraction is now a pre-approved Stage A task, not a >30-LoC fallback). Tightened Surface #4 (cross-year regression test now uses Clock-injected fixture clocks at 2026 vs 2027 — replaces the weak "two consecutive replays" assertion). Other 3 Critical Surfaces accepted as-is from v1.
- **v1 (initial)**: Stage breakdown, spike protocol, gateway contracts, test plan, coverage gates.

---

## Implementation Outcome (post-shipment record)

| Stage | Result | Commit(s) |
|-------|--------|-----------|
| Pre-Flight `fx.Decorate` spike | PASSED at fx v1.24.0; §10 Contingent (GatewayModule split) NOT triggered | `2c4b60c` (BACKEND-1) |
| D1.1 `BuildIndustryFromResult` export | DONE (rename-only) | `1bd7947` (BACKEND-1) |
| A.6 `macro.ParseFREDSeries` extraction | DONE (~75 LoC pure function + 150 LoC tests at 100% file coverage) | `985603e` (BACKEND-1) |
| A.1 `BundleSECGateway` | DONE | `c98c061` (BACKEND-2) |
| A.2/A.3/A.4 Market/Macro/YFinance gateways | DONE (grouped) | `54c1f76` (BACKEND-2) |
| A.5 Finzive | SKIPPED (not wired in production) | n/a |
| B side-effect stubs | DONE | `c90b3af` (BACKEND-2) |
| C `replay.Module` fx composition | DONE — **deviation: hand-picked `fx.Provide` instead of `fx.Decorate` over CoreModule** for F11 hermeticity (transitive `*sqlx.DB`/`*redis.Client` pulls). Documented in 60-line package comment. | `8aef33a` (BACKEND-2) |
| D `Replay()` orchestrator + comparator | DONE | `9302411` (BACKEND-2) |
| E `--from` CLI flag + R3 deferral guard | DONE | `dcd4dd7` (BACKEND-2) |
| F round-trip + cross-year integration tests | DONE — both pass `-count=10 -race` | `8434989` (BACKEND-2) |
| G `go-cmp` direct import + CompareResponse | DONE — `go.mod` adds only this one direct-promotion | `a09cd53` (BACKEND-2) |
| Coverage sweep on stubs | DONE (replay 84.5%, cmd/replay 81.4%) | `edc4680` (BACKEND-2) |
| VERIFIER cycle 1 — HIGH-1 fix | Threaded `*config.Config` into `BundleMacroGateway` for MRP (was hardcoded 0.06; now reads `cfg.Macro.ManualMarketRiskPremium` with 0.05 fallback matching production default) | `4945a01` (BACKEND-3) |
| VERIFIER cycle 1 — MEDIUM-1 fix | Threaded `manifest.Ticker` into `BundleSECGateway` (replaces hardcoded 8-ticker map; now supports any captured ticker including FPI/ADR set) | `a8d58e7` (BACKEND-3) |
| VERIFIER cycle 1 — MEDIUM-2 fix | `TestModule_DoesNotConstructDB` now asserts against real `*sqlx.DB` (previously used local placeholder type) | `a1ba463` (BACKEND-3) |
| VERIFIER cycle 1 — LOW-1 fix | Cover git-drift branch in `Replay()` via injectable `gitSHAResolver` package-var seam | `8d1e8f4` (BACKEND-3) |
| VERIFIER cycle 1 — LOW-2 fix | Removed dead `Mode.String()` method (`camelToSnake` retained — has real caller at `diff.go:373`) | `6d485c3` (BACKEND-3) |

**5 BACKEND deviations beyond the original plan**, all defensible per the gate reviews:
1. Stage C hand-picked module (vs `fx.Decorate(CoreModule)` in spec D2) — for F11 hermeticity
2. Cross-year test `scrubTimestamps` of 3 timestamp fields before `reflect.DeepEqual` — those fields ARE the clock's value
3. `BundleMacroGateway.GetMarketRiskPremium` returns config value, not `ErrBundleMissingPayload` — coordinator treats MRP error as fatal
4. `BundleSECGateway.GetTickerCIKMapping` extracts CIK from raw payload + threaded ticker — engine consumes mapping for every replay
5. Coverage 84.5% / 81.4% (below 90% / 80% spec target) — gaps in defensive `if err != nil` branches accepted by VERIFIER

---

## 1. Preamble

**Status:** PLAN v2 — awaiting human approval before BACKEND dispatch.

**Builds on:** [`observability-replay-tooling-spec.md`](./observability-replay-tooling-spec.md) (v0.2). All design decisions, ADRs, CLI contract, files-touched table, and testing strategy are owned by that spec. This document does **not** redesign anything; it sequences BACKEND's work.

**Scope:** Phase **R2 only** — gateway substitution + engine wiring producing a real `*entities.ValuationResult` for a single bundle, diffed against `17-response.json`. R3 (parallel batch, `--diff-stages`, `--filter-*`, `--verbose`, `--workers`, perf benches) is explicitly out of scope and will be planned in a follow-up dispatch after R2 ships.

**Phase R0 + R1 already merged.** Confirmed live in the repo:
- `internal/observability/replay/{errors,manifest,schema,diff,walk,output,duration}.go` plus tests (90.7% coverage).
- `internal/services/valuation/clock.go` — `Clock` interface + `wallClock{}` (D10) + `*Service.SetClock`.
- `internal/di/container.go` — `fx.Provide(valuation.NewWallClock)` + `svc.SetClock(clock)` in `NewValuationService`.
- `cmd/replay/main.go` — flag-parsing skeleton (R1 subset: `--format`, `--out`, `--allow-schema-drift`, `--allow-git-drift`, `--quiet`, `--verbose`).
- `internal/api/v1/handlers/fair_value.go` — D1 helper exports are **not yet done**; `buildIndustryFromResult` is still lowercase (verified `grep '^func [A-Z]\w+' fair_value.go`).

**LoC + commit estimate (R2 only):**
- Pre-flight spike: ~80 LoC (test only, throwaway), 1 commit.
- D1 helper export: ~10 LoC (rename), 1 commit.
- **Stage A.6 macro per-series parser extraction (v2 redirect): ~50–80 LoC including tests, 1 commit.** Pre-approved precondition for `BundleMacroGateway`'s raw-mode symmetry; see §4.3 OQ2 and §3 Stage A.
- Stage A–E (gateway stubs, side-effect stubs, fx Module, orchestrator, CLI flag wiring): ~900 LoC including tests, 4–6 commits.
- Stage F (round-trip integration test): ~250 LoC, 1 commit.
- Stage G (`go-cmp` direct-import in `diff.go` + `go.mod` tidy): ~40 LoC + go.mod diff, 1 commit.
- **Estimated total:** ~1,250–1,300 LoC, 9–11 commits, contingent on the spike outcome (a fallback `GatewayModule` split adds ~40 LoC and 1 commit).

**Commit cadence:** Each stage is a separate commit so any individual step can be reverted in isolation, mirroring the R0+R1 cadence.

---

## 2. Pre-Flight Spike Protocol — `fx.Decorate` Composition

**Non-negotiable.** This is BACKEND's first task in R2. **No gateway-stub code may be written before this spike concludes.** Spec §14 row 1 calls for a "≤ 1 day spike"; this section specifies the exact protocol.

### Why this exists

The replay design hinges on D2: `replay.Module` overrides production gateway providers via `fx.Decorate` over `CoreModule + ServiceModule`. If `fx.Decorate` does not compose at the pinned `go.uber.org/fx v1.24.0` against our existing provider topology, the entire R2 file layout shifts (a `GatewayModule` sub-module split in `internal/di/container.go` becomes mandatory). Discovering that after writing 5 gateway stubs costs a re-write; discovering it before costs a 1-day spike.

### Spike location

`internal/observability/replay/spike_test.go` (build-tag-free, but kept tiny so it can be deleted after the spike concludes — see "Disposition" below).

### Spike code (pseudocode — BACKEND fills in the exact types)

```go
//go:build replay_spike
// +build replay_spike

package replay

import (
    "context"
    "testing"

    "go.uber.org/fx"
    "go.uber.org/fx/fxtest"

    "github.com/midas/dcf-valuation-api/internal/core/entities"
    "github.com/midas/dcf-valuation-api/internal/core/ports"
    "github.com/midas/dcf-valuation-api/internal/di"
)

type fakeSECGateway struct{ called bool }

func (f *fakeSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
    f.called = true
    return &entities.CompanyFactsResponse{}, nil
}
// ... satisfy the rest of ports.SECGateway with no-op stubs ...

func TestSpike_FxDecorate_OverridesGatewayProvider(t *testing.T) {
    fake := &fakeSECGateway{}

    app := fxtest.New(t,
        di.CoreModule,
        // Decorate over the production SEC gateway provider with our fake.
        fx.Decorate(func(prod ports.SECGateway) ports.SECGateway {
            return fake
        }),
        // Resolve the gateway and prove the decoration took effect.
        fx.Invoke(func(gw ports.SECGateway) {
            if gw != fake {
                t.Fatalf("expected decorated SECGateway to be the fake; got %T (production binding leaked)", gw)
            }
        }),
    )
    app.RequireStart()
    app.RequireStop()
}
```

The test must:
1. Compile cleanly against `go.uber.org/fx v1.24.0` (the version pinned in `go.mod` — verify with `go list -m go.uber.org/fx` before running).
2. Invoke `fx.Decorate` over the **real** `di.CoreModule` (not a synthetic mini-module) so any provider-graph cycle or grouped-provider rejection surfaces.
3. Assert via the resolved interface identity (`gw != fake`) that the decorated value reaches downstream consumers — not just that decoration silently succeeds.

### Pass criterion

Both must hold:
- `go test -tags replay_spike ./internal/observability/replay/ -run TestSpike_FxDecorate_OverridesGatewayProvider -v` exits 0.
- The output contains the fake's callsite identity assertion succeeding (no `production binding leaked`).

### Fail criterion + fallback path

If the spike fails (compile error, runtime error, decoration silently no-ops), BACKEND **must** stop and execute the contingent fallback before proceeding to gateway-stub implementation:

#### Fallback: `GatewayModule` sub-module split

Per spec §10 "Contingent — only if `fx.Decorate` is insufficient." Concrete partition:

| Action | File | Detail |
|---|---|---|
| Extract a new `GatewayModule` `fx.Options` block | `internal/di/container.go` | Move the three lines `fx.Provide(fx.Annotate(NewSECGateway, fx.As(new(ports.SECGateway))))` / `NewMarketDataGateway` / `NewMacroDataGateway` (lines 127–129) out of `CoreModule` into a new `GatewayModule` sibling. |
| Update `Module = fx.Options(CoreModule, GatewayModule, ServiceModule, HandlerModule)` | `internal/di/container.go` | Recompose so production wiring is byte-identical. |
| Update `cmd/server/main.go` if it imports `CoreModule` directly | check | Likely still imports `di.Module`, so no change; verify before committing. |
| Replay's `replay.Module` uses `fx.Replace(...)` over `GatewayModule`'s providers | `internal/observability/replay/module.go` | Spec §10 explicitly calls out this contingency. |

Behavior change: **none in production** — composing `CoreModule + GatewayModule` produces the same provider graph as the original `CoreModule`. CI test suite (existing) must still pass after the split. The split is its own commit before any gateway-stub code lands.

### Decision gate

BACKEND must report spike outcome to HUMAN before proceeding to Stage A:
- **PASS:** "spike confirms `fx.Decorate` overrides `ports.SECGateway` at fx 1.24.0; proceeding with §6 D2 design as written."
- **FAIL:** "spike failed at <line>: <error>; executing the `GatewayModule` split as the §10 Contingent fallback. Will report when split lands."

If FAIL, the split commit lands first, the spike test is updated to use `fx.Replace` against `GatewayModule`, and the spike must re-run and pass before Stage A begins.

### Disposition of the spike test

After the spike concludes, the spike test file may either:
1. Be deleted (preferred — it served its purpose, and an integration test in Stage F covers the real path).
2. Be retained behind the `replay_spike` build tag indefinitely as a regression guard against an fx upgrade silently breaking decoration.

BACKEND's choice; either is acceptable. The `replay_spike` build tag keeps it out of the default `go test ./...` run regardless.

---

## 3. Ordered Task List (TDD)

Each task is `Test first → Implementation → Acceptance`. BACKEND must respect dependency order: **Stages A through E run sequentially**; within Stage A the five gateway stubs MAY be implemented in parallel after the spike resolves.

### Pre-flight (already covered in §2)

**Task PF.1 — `fx.Decorate` spike + decision gate.** Must complete before any Stage A task. Pass/fail/fallback all defined in §2.

### D1 Pre-cursor

**Task D1.1 — Export response-construction helpers in `internal/api/v1/handlers/fair_value.go`.**

- **File:** `internal/api/v1/handlers/fair_value.go`
- **Test first:** Update `internal/api/v1/handlers/fair_value_test.go` (or add a new `*_test.go`) with a test that imports the package and calls the exported function name. The test compile-fails BEFORE the rename, compile-succeeds after. Suggested test: `TestBuildIndustryFromResult_ExportedSurface` constructs an `entities.ValuationResult{IndustrySIC: "TECH"}` and asserts the returned `*Industry` is non-nil and has SIC populated.
- **Implementation:** Rename `buildIndustryFromResult` → `BuildIndustryFromResult` (line 108). **No logic changes.** Same parameter list, same return type, same body. The Industry struct (line 49) is already exported.
- **Acceptance:**
  - `git diff master..HEAD -- internal/api/v1/handlers/fair_value.go` shows only the function-name capitalization change and any internal call-sites updated to match (line 385: `buildIndustryFromResult(result)` → `BuildIndustryFromResult(result)`).
  - `go build ./...` succeeds.
  - `go test ./internal/api/v1/handlers/...` passes (existing tests must not regress).
  - REVIEWER (per spec §11 D1 Finding 1) verifies the diff is rename-only.

**Why this is a pre-cursor and not a Stage E task:** The replay orchestrator in `internal/observability/replay/replay.go` (Stage D) imports `handlers.BuildIndustryFromResult` to rebuild `FairValueResponse` after `*Service.CalculateValuation` returns. Without the export, Stage D won't compile. Land this immediately after the spike, before Stage A starts.

### Stage A — Bundle Gateway Stubs (after spike + D1.1)

These tasks satisfy F3, F11, and the §6 "Critical abstractions" `replay.BundleGateway` contract. Each task is structured identically: failing test → implementation → acceptance.

Per-gateway contract details (raw-mode vs parsed-mode payload paths, error contract, goroutine-safety) are specified in §4 below — BACKEND should treat §4 as the authoritative implementation contract for Stage A.

**Task ordering inside Stage A (v2):**

- **Task A.6 (macro per-series parser extraction) is a HARD PRECONDITION for Task A.3 (`BundleMacroGateway`).** A.6 must complete and merge before A.3 is implemented. See §4.3 Open Question 2 for rationale; the short version is that raw-mode symmetry across SEC/Yahoo/Macro is a spec D3 invariant, and macro's parser today lives inline in `getFREDSeries` — extracting it cleanly is preferable to a >30-LoC carve-out fallback.
- Tasks A.1, A.2, A.4 (SEC, Market, YFinance bundle gateways) have no inter-dependency; they MAY be implemented in parallel after the spike resolves and D1.1 lands.
- Task A.5 is a documented skip (Finzive not wired in production).

#### Task A.1 — `BundleSECGateway` (`internal/observability/replay/gateway_sec.go`)

- **Test first:** `gateway_sec_test.go` with these test cases (table-driven where natural):
  - `TestBundleSECGateway_GetCompanyFacts_RawMode_ParsesProductionBytes` — seeds a `testdata/bundles/happy-aapl/05-fetch-sec.raw.json` with a real captured AAPL response (committed fixture from `artifacts/<date>/AAPL/req_<id>/05-fetch-sec.raw.json` redacted to <50 KiB), constructs the gateway with `Mode=ModeRaw`, asserts the returned `*entities.CompanyFactsResponse.CIK == "320193"` and the parsed Facts map is non-empty.
  - `TestBundleSECGateway_GetCompanyFacts_ParsedMode_DirectUnmarshal` — same fixture but the test reads `05-fetch-sec.parsed.json`; `Mode=ModeParsed`; asserts the same CIK with no production parser invocation (pin via a counter or the absence of parser-side narrate emissions).
  - `TestBundleSECGateway_GetCompanyFacts_MissingFile_ReturnsErrBundleMissingPayload` — empty bundle dir; asserts `errors.Is(err, replay.ErrBundleMissingPayload)` AND `errors.As(err, &replay.BundleMissingPayloadError{}) // RelativePath == "05-fetch-sec.raw.json"`.
  - `TestBundleSECGateway_GetTickerCIKMapping_NotInBundle_ReturnsErrBundleMissingPayload` — bundles do not record this endpoint; the gateway must return `ErrBundleMissingPayload` cleanly (NOT panic; NOT live HTTP).
  - `TestBundleSECGateway_HealthCheck_AlwaysOK` — replay never tests live health; HealthCheck returns nil.
  - `TestBundleSECGateway_ConcurrentGetCompanyFacts_RaceFree` — call `GetCompanyFacts` 100x in parallel via `t.Parallel()` + `sync.WaitGroup`; run under `-race` (CI flag); asserts no data race.
- **Implementation:**
  - File: `internal/observability/replay/gateway_sec.go`.
  - Implement `ports.SECGateway` (verify the four method signatures in `internal/core/ports/gateways.go:88-94`).
  - Constructor: `NewBundleSECGateway(bundleDir string, mode Mode) *BundleSECGateway`.
  - `Mode` is a new top-level type in `internal/observability/replay/types.go` or co-located in `gateway_sec.go` (BACKEND's call): `type Mode int; const (ModeRaw Mode = iota; ModeParsed)`.
  - In `ModeRaw`, `GetCompanyFacts` reads `<bundleDir>/05-fetch-sec.raw.json`, then invokes the production parser (`internal/infra/gateways/sec/parser.go::ParseCompanyFacts` or whatever the canonical entry point is — confirm via grep before wiring). Cite spec §5 D3.
  - In `ModeParsed`, `GetCompanyFacts` reads `<bundleDir>/05-fetch-sec.parsed.json` and `json.Unmarshal`s directly into `*entities.CompanyFactsResponse`.
  - Every "missing payload" path returns `replay.NewBundleMissingPayloadError(bundleDir, "05-fetch-sec.raw.json", err)` wrapped via `errors.Is`-friendly indirection (already wired in `errors.go`).
  - Goroutine-safety: gateway may be called from `datafetcher.coordinator:181-196` via `go func()`. The struct holds only the `bundleDir string` + `mode Mode` (both immutable post-construction); `os.ReadFile` is concurrency-safe, so no mutex is required. Document this in a struct-level comment so future maintainers don't add mutable cache state.
- **Acceptance:**
  - `go test ./internal/observability/replay/ -run TestBundleSECGateway -race -count=10` passes.
  - File-level coverage ≥ 90% (per §6).

#### Task A.2 — `BundleMarketGateway` (`internal/observability/replay/gateway_market.go`)

- **Test first:** Mirror A.1's structure. Tests:
  - `TestBundleMarketGateway_GetQuote_RawMode_ParsesProductionBytes` — fixture `06-fetch-market.raw.json`; assert returned `*entities.MarketData.Ticker == "AAPL"`.
  - `TestBundleMarketGateway_GetQuote_ParsedMode_DirectUnmarshal`.
  - `TestBundleMarketGateway_GetQuote_MissingFile_ReturnsErrBundleMissingPayload`.
  - `TestBundleMarketGateway_GetQuotes_RaceFree` (called from goroutines in coordinator).
  - `TestBundleMarketGateway_GetHistoricalPrices_NotInBundle_ReturnsErrBundleMissingPayload` — historical prices are not snapshotted in the bundle today; the stub must return `ErrBundleMissingPayload`.
  - `TestBundleMarketGateway_HealthCheck_AlwaysOK`.
- **Implementation:**
  - File: `internal/observability/replay/gateway_market.go`.
  - Implement `ports.MarketDataGateway` (verify signatures `internal/core/ports/gateways.go:97-102`).
  - Constructor: `NewBundleMarketGateway(bundleDir string, mode Mode) *BundleMarketGateway`.
  - Same raw/parsed dispatch pattern as A.1.
  - **YFinance secondary surface:** Production wires `*market.Gateway` (concrete) and the valuation service does an `interface{}.(*market.Gateway)` cast at `container.go:668` to reach `YFinanceClient()`. Replay's `BundleMarketGateway` must EITHER:
    1. Implement `ports.YFinanceGateway` directly AND be passed separately to `*Service.SetYFinanceGateway(...)` via the replay module's post-construct hook (preferred — clean abstraction; see Stage C).
    2. Emit a `*market.Gateway` concrete instance — rejected because constructing one requires real YFinance config.
  - **Document this trade-off in `gateway_market.go`'s package comment.** The replay path supplies a separate `BundleYFinanceGateway` (Task A.4) and the module wires it post-construct — Service's existing `SetYFinanceGateway` already supports this lifecycle.
- **Acceptance:** Same as A.1.

#### Task A.3 — `BundleMacroGateway` (`internal/observability/replay/gateway_macro.go`)

- **Test first:**
  - `TestBundleMacroGateway_GetTreasuryRates_RawMode_ParsesProductionBytes` — fixture is **multiple files** following the production layout `07-fetch-macro-<seriesID>.raw.json` (per `internal/infra/gateways/macro/gateway.go:290`). The gateway must read whichever file matches the FRED series the production code requests (e.g. `DGS10` for 10-year treasury). For `ModeParsed`, the fixture is single `07-fetch-macro.parsed.json`.
  - `TestBundleMacroGateway_GetMarketRiskPremium_ParsedMode_DirectUnmarshal`.
  - `TestBundleMacroGateway_GetFXRate_FromCcyEqualsToCcy_ReturnsOne` — short-circuit per `gateways.go:118`.
  - `TestBundleMacroGateway_GetFXRate_NotInBundle_ReturnsErrBundleMissingPayload`.
  - `TestBundleMacroGateway_HealthCheck_AlwaysOK`.
  - Concurrency test mirroring A.1.
- **Implementation:**
  - File: `internal/observability/replay/gateway_macro.go`.
  - Implement `ports.MacroDataGateway` (signatures `gateways.go:105-121`).
  - **Hard precondition: Task A.6 must have merged.** A.6 extracts the FRED per-series parser into a pure function in `internal/infra/gateways/macro/parser.go` (capitalized export, e.g. `ParseFREDSeries(seriesID string, body []byte) (float64, error)`). `BundleMacroGateway` imports that function for raw-mode dispatch.
  - Raw-mode handling for `GetTreasuryRates`: read each FRED series ID expected by production (`DGS1MO`, `DGS3MO`, `DGS6MO`, `DGS1`, `DGS2`, `DGS5`, `DGS10`, `DGS20`, `DGS30` — see `internal/infra/gateways/macro/gateway.go:180-190` `seriesMap`), load the corresponding `<bundleDir>/07-fetch-macro-<seriesID>.raw.json` file, dispatch each to the extracted `ParseFREDSeries(seriesID, body)`. Missing files for individual series are tolerated (the production gateway already logs and continues per `gateway.go:200-204`); a missing file is NOT `ErrBundleMissingPayload` unless ALL series files are absent. Reassemble into `*entities.TreasuryRates` using the same `seriesMap` mapping the production code uses.
  - Parsed-mode handling: `json.Unmarshal` `<bundleDir>/07-fetch-macro.parsed.json` directly into `*entities.TreasuryRates`.
  - `GetFXRate`: identity short-circuit per `gateways.go:118` is unchanged; for non-identity pairs the bundle does not capture FX rates today, so return `ErrBundleMissingPayload` cleanly (or, if Stage F's integration test reveals the engine path consults this, BACKEND surfaces and HUMAN re-scopes — flag explicitly).
- **Acceptance:** Same as A.1, plus: file-level coverage for `BundleMacroGateway` ≥ 90%, raw-mode tests exercise the extracted `ParseFREDSeries` (NOT a private re-implementation).

#### Task A.4 — `BundleYFinanceGateway` (in `gateway_market.go` or co-located file)

- **Test first:**
  - `TestBundleYFinanceGateway_GetAnalystEstimates_ParsedMode` — the bundle today does NOT capture YFinance analyst estimates as a separate file (verify by grepping `internal/infra/gateways/market/yfinance_client.go` for snapshot calls). If absent, this test must assert `ErrBundleMissingPayload` and the production engine path must tolerate that error gracefully (it likely already does because YFinance is best-effort).
  - `TestBundleYFinanceGateway_GetQuote_DelegatesToMarketBundle` — for the methods that overlap with `MarketDataGateway`, the YFinance bundle reads the same `06-fetch-market.*.json` fixture.
- **Implementation:**
  - Implement `ports.YFinanceGateway` (signatures `gateways.go:170-185`).
  - **Critical contract decision:** if the production bundle does not capture YFinance-specific endpoints (`GetKeyStatistics`, `GetAnalystEstimates`, `GetHistoricalPrices` — beta calc), the stub returns `ErrBundleMissingPayload` and the production code's existing fallback path handles it. This is consistent with F11's hermeticity invariant. BACKEND must verify this fallback exists by reading `internal/services/valuation/service.go` for `errors.Is(...,ErrBundleMissingPayload)`-compatible handling — if the engine treats a YFinance miss as fatal, replay reports `errored` (exit 2), NOT crash. Document the verified behavior in the gateway's doc-comment.
- **Acceptance:** Same as A.1.

#### Task A.5 — Finzive bundle gateway: NOT REQUIRED for R2

`ports.FinziveGateway` is defined but not wired in production (`grep FinziveGateway internal/` returns only the port definition). The market gateway has `// TODO: Initialize Finzive client when implemented` at `internal/infra/gateways/market/gateway.go:33-36`. R2 does not need a Finzive bundle stub because the engine path never consults a `FinziveGateway` interface. **Skip this task.** Document the reason in `module.go`'s package comment so a future Finzive enablement is forced to add the stub at the same time.

If R3 or a later phase wires Finzive, the Finzive bundle stub joins the file layout per spec §6 alongside the others. Out of scope here.

#### Task A.6 — Macro per-series parser extraction (PRECONDITION for A.3)

**Pre-approved by HUMAN as part of the v2 redirect.** This task lands BEFORE Task A.3 and unblocks raw-mode symmetry across SEC / Market / Macro per spec D3 ("`--from=raw` exercises the gateway parser"). Without this extraction, `BundleMacroGateway` would have to either re-implement the FRED parsing logic (drift risk: production parser changes silently desynchronize from replay's parser) or fall through to `ModeParsed`-only behavior (asymmetric raw-mode semantics: a user running `--from=raw` to verify "did my parser+math change" gets incomplete coverage without knowing).

- **File (new):** `internal/infra/gateways/macro/parser.go`
- **File (modified):** `internal/infra/gateways/macro/gateway.go` — `getFREDSeries` is split: the HTTP fetch + Tee/snapshot wiring stays inside the method; the JSON-decode + observation-validation + float-parse logic moves into the new pure function.
- **Test first:** `internal/infra/gateways/macro/parser_test.go` — table-driven tests for the extracted function:
  - `TestParseFREDSeries_HappyPath_ReturnsFloat` — canned FRED response body with one observation, value `"4.25"`; assert returned `(4.25, nil)`.
  - `TestParseFREDSeries_NoObservations_ReturnsError` — body with empty `observations` array; assert error contains `"no observations found for series"`.
  - `TestParseFREDSeries_DotValue_ReturnsError` — observation with `value == "."` (FRED's "no data" sentinel); assert error contains `"no valid data for series"`.
  - `TestParseFREDSeries_MalformedFloat_ReturnsError` — observation with `value == "abc"`; assert wrapped `strconv.ParseFloat` error.
  - `TestParseFREDSeries_MalformedJSON_ReturnsError` — body `[]byte("{not json")`; assert decode error.
  - `TestParseFREDSeries_MultipleObservations_UsesFirst` — body with two observations (real FRED responses sort `desc`); assert the function uses `Observations[0]` consistent with current production behavior at `gateway.go:299`.
- **Implementation contract:**
  - **Signature (mandatory):** `func ParseFREDSeries(seriesID string, body []byte) (float64, error)` — pure function, no `*Gateway` receiver, no implicit dependencies on gateway state. The `seriesID` parameter is used only for error messages so callers see which series failed.
  - The function takes the raw bytes that today flow into `json.NewDecoder(body).Decode(&fredResponse)` at `gateway.go:278` and returns the same `float64` that today flows out at `gateway.go:309`.
  - Public surface of `*Gateway` (constructor names, public method signatures on `ports.MacroDataGateway`, behavior for all callers) MUST be unchanged. This is an internal refactor.
  - `getFREDSeries` after extraction reads as: HTTP fetch → TeeReader + bundle snapshot wiring (unchanged) → `ParseFREDSeries(seriesID, capturedBytes)` → return.
- **Why a pure function and not a method on a stateless struct:** the spec D3 requirement is that the replay's bundle gateway "exercises the gateway parser" — a free function with the canonical signature is the simplest way to make that semantically obvious in `BundleMacroGateway.GetTreasuryRates`'s call site. Future maintainers see `macro.ParseFREDSeries(...)` and immediately know it's the production parser, not a replay-local re-implementation.
- **Behavior preservation check:** the existing macro gateway tests (`internal/infra/gateways/macro/*_test.go` if present, otherwise the integration coverage in `internal/integration/`) MUST continue to pass with zero changes. If any existing test references the inlined parsing logic by line number or via a private helper, BACKEND adjusts the test to call through `getFREDSeries` (the public path) — never via direct invocation of the extracted parser, since that would couple unrelated tests to the new function.
- **Acceptance:**
  - `parser_test.go` passes with ≥ 90% coverage of `parser.go`.
  - All pre-existing macro gateway tests pass without modification (or with only error-message-substring updates if the wrapping changed).
  - `git diff master..HEAD -- internal/infra/gateways/macro/` shows ONLY: (a) new `parser.go`, (b) new `parser_test.go`, (c) `gateway.go` `getFREDSeries` body shrunk by ~25 LoC and replaced with a `ParseFREDSeries` call. The public surface of `gateway.go` is unchanged.
  - Manual smoke: a real production replay (against any captured bundle) produces byte-identical treasury rates compared to a build before the extraction (verified by REVIEWER via response diff if needed).

**Coupling note for BACKEND:** the FRED parser's logic is small (~20–25 LoC of decode + validate + parse). If during implementation BACKEND finds the function would need to take additional dependencies (e.g., a logger, a config flag, anything beyond `seriesID` + `body`), STOP and surface to HUMAN — that signals the parser is more entangled than the read suggests, and the extraction may need narrower scope. The signature `(string, []byte) -> (float64, error)` is the budget.

### Stage B — Side-Effect Stubs

#### Task B.1 — `internal/observability/replay/stubs.go`

- **Test first:** `stubs_test.go` per spec §12:
  - `TestNotFoundCacheRepo_Get_AlwaysReturnsCacheMiss` — table-driven, multiple keys, all return the established "cache miss" sentinel (mirror what `internal/infra/repositories/cache/memory.go` returns; cite the exact error in the test).
  - `TestNotFoundCacheRepo_Set_NoOp` — call Set with arbitrary value; assert no error and a subsequent Get still misses.
  - `TestNotFoundFinancialDataRepo_*` / `MarketData` / `Macro` — all return their "not found" sentinel.
  - `TestNoOpMetricsService_*Counter` — every emission method is a no-op; recording stub increments an internal counter we can assert on (per spec §5 D8 "recording stub").
  - `TestPanicAuthRepo_GetByKey_Panics` — `defer recover(); call; assert recovered`.
  - `TestPanicWatchlistRepo_*_Panics`.
- **Implementation:**
  - All stubs in `stubs.go` so REVIEWER can audit hermeticity in one file.
  - Provide constructors (`NewNotFoundCacheRepo()`, etc.) so the fx Module wires them via `fx.Provide` rather than literal struct values.
  - Auth + Watchlist stubs panic (per F11: they sit OUTSIDE the goroutine path, so panic-on-call catches genuine wiring drift). Document this on the stub types.
  - `*sql.DB` and `*redis.Client` providers: replay's fx.App excludes these entirely. The replay Module must **not** include providers that need them (verified in Stage C).
- **Acceptance:**
  - All tests pass.
  - Coverage of `stubs.go` ≥ 90%.

### Stage C — fx Module Composition

#### Task C.1 — `internal/observability/replay/module.go`

- **Test first:** `module_test.go`:
  - `TestModule_OverridesAllGateways` — build an `fxtest.App` with `replay.Module(...)`, resolve each `ports.*Gateway`, assert each is the bundle-backed type (NOT the production type).
  - `TestModule_OverridesAllRepos` — same for `FinancialData`, `MarketData`, `Macro`, `Cache`, `Auth`, `Watchlist`, `MetricsService`.
  - `TestModule_DoesNotConstructDB` — assert the fx graph does NOT include `*sqlx.DB` (no provider for it). Verify by attempting `fx.Invoke(func(db *sqlx.DB) {})` and asserting fx returns a "missing dependency" error.
  - `TestModule_DoesNotConstructRedis` — same.
  - `TestModule_OverridesClock_BindsToManifestStartedAt` — construct with a manifest where `started_at = "2025-01-01T00:00:00Z"`; resolve the `valuation.Clock`; assert `Clock.Now() == 2025-01-01T00:00:00Z`.
  - `TestModule_PostConstructHook_WiresYFinanceGateway` — the test resolves `*valuation.Service`; asserts via reflection or an exported test-only getter that `svc.YFinanceGateway == BundleYFinanceGateway`. (If no test-only getter exists, add one in `service_test.go` build-tag-guarded — last resort.)
- **Implementation:**
  - File: `internal/observability/replay/module.go`.
  - Export `Module(bundleDir string, opts Options) fx.Option`. Returns an `fx.Options(...)` composition:
    1. `fx.Decorate(func(ports.SECGateway) ports.SECGateway { return NewBundleSECGateway(bundleDir, opts.Mode) })` — ditto for Market and Macro. (If the spike forced a fallback, replace with `fx.Replace(...)` over `GatewayModule`.)
    2. `fx.Decorate(func(ports.FinancialDataRepository) ports.FinancialDataRepository { return NewNotFoundFinancialDataRepo() })` — and similar for MarketData, Macro, Cache, Auth, Watchlist.
    3. `fx.Decorate(func(*metrics.Service) *metrics.Service { return ... })` — replay binds a no-op metrics service (or, per the existing two-step binding at `container.go:148-153`, decorate the `ports.MetricsService` interface binding instead).
    4. `fx.Decorate(func(valuation.Clock) valuation.Clock { return &manifestClock{at: opts.ManifestStartedAt} })` — the clock binding flows through `NewValuationService → svc.SetClock(clock)` per `container.go:664`. **Confirmed this works because of R0/R1's wiring** — no further production change needed.
    5. `fx.Invoke(func(svc *valuation.Service, yfin ports.YFinanceGateway) { svc.SetYFinanceGateway(yfin) })` — post-construct hook to wire the `BundleYFinanceGateway` (since Service's YFinance plumbing is post-construct, not constructor-injected).
  - Composition: `fx.Options(di.CoreModule, di.ServiceModule, decoratesAbove, fx.NopLogger)`.
  - **Do NOT include `di.HandlerModule`** (D2: no Gin, no lifecycle hooks, no disk).
  - The `Options` struct mirrors `replay.Options` from §6 Critical abstractions; for R2 only `Mode` and `ManifestStartedAt` are consumed. R3 fields (`FloatRelTol`, `AllowSchemaDrift`, etc.) live on the same struct but are read by `Replay()` (Stage D) directly, not by `Module`.
- **Acceptance:**
  - `module_test.go` passes.
  - `module.go` itself has ≥ 90% coverage (most lines are fx wiring; tests resolve every override path).

### Stage D — Orchestration

#### Task D.1 — `internal/observability/replay/replay.go` (the `Replay()` entry point)

- **Test first:** `replay_test.go` — these are unit tests against synthetic bundles in `testdata/`. The full round-trip integration test is Stage F.
  - `TestReplay_HappyPath_NoFieldDiffs` — synthetic bundle with manifest + raw payloads + `17-response.json`; the engine produces a `*ValuationResult` that, when re-rendered through `BuildIndustryFromResult` + `FairValueResponse{...}`, matches `17-response.json` exactly. Assert `Result.Status == "pass"`.
  - `TestReplay_MutatedResponse_FlagsExactlyOneFieldDiff` — same bundle but with `17-response.json`'s `dcf_value_per_share` shifted by 1%; assert `Result.Status == "fail"` and the diff list contains exactly one float-diff entry at path `dcf_value_per_share`.
  - `TestReplay_MissingPayload_ReturnsErroredResult` — bundle with `05-fetch-sec.raw.json` deleted; assert `Result.Status == "errored"` and `errors.Is(result.Err, replay.ErrBundleMissingPayload)`.
  - `TestReplay_SchemaDrift_RefusedByDefault` — manifest with `schema_versions: {FinancialData: 999}`; assert error returned and `Result.Status == "errored"` and the error message names the drifted entity.
  - `TestReplay_SchemaDrift_AllowedWithFlag` — same bundle but `Options{AllowSchemaDrift: true}`; assert `Result.Status == "pass"` and `Result.SchemaDrift == true`.
  - `TestReplay_GitDrift_RefusedByDefault` / `_AllowedWithFlag` — same pattern.
- **Implementation:**
  - Export `Replay(ctx context.Context, bundleDir string, opts Options) (Result, error)`.
  - Steps (mirrors §6 architecture diagram):
    1. `ReadManifest(bundleDir)` (existing R1 code).
    2. Validate `schema_versions` via `CompareManifestSchemas` (existing R1 code); refuse if drift unless `opts.AllowSchemaDrift`.
    3. Validate `git_sha` (manifest's SHA vs current build's `resolveGitSHA()`); refuse if drift unless `opts.AllowGitDrift`. Empty manifest SHA is "unknown" not "drift" per F6.
    4. Build `fx.New(replay.Module(bundleDir, opts), fx.NopLogger)`. App start.
    5. Resolve `*valuation.Service` via `fx.Populate` or an `fx.Invoke` hook.
    6. Inject `manifest.RequestID` into ctx via `logctx.Inject` (or whatever the canonical request-id plumbing is — verify by reading `internal/observability/logctx/`). Per D7 / F7.
    7. Call `svc.CalculateValuation(ctx, manifest.Ticker, /*opts from 02-handler-options.json*/ nil)`. The opts come from `02-handler-options.json` if present; nil fallback.
    8. Render the result via the **exported** `BuildIndustryFromResult` (D1.1 dependency) into a `FairValueResponse` struct, exactly as the production handler does at `fair_value.go:368-392`.
    9. Read `17-response.json` from the bundle into a `FairValueResponse` shape; diff field-by-field via `internal/observability/replay/diff.go` helpers (Stage G upgrades these to use `go-cmp`).
    10. Build the `Result{}` and stop the app.
  - **Determinism:** verify per §11 REVIEWER: replay does NOT call `uuid.NewString()` (manifest's request_id is authoritative); replay does NOT emit narrate or calclog (the request-path emitters are no-op'd via the metrics decoration in Stage C).
- **Acceptance:**
  - All `replay_test.go` tests pass.
  - `replay.go` coverage ≥ 90%.
  - `grep 'time\.Now\(\)' internal/services/valuation/service.go` returns zero hits in non-test files (REVIEWER §11 D7/D10 audit).

### Stage E — CLI Flag Wiring (R2-Scoped)

#### Task E.1 — Add `--from=raw|parsed` to `cmd/replay/main.go`

- **Test first:** Update `cmd/replay/main_test.go` with:
  - `TestParseFlags_FromRaw_Default` — argv without `--from`; asserts `flags.from == "raw"`.
  - `TestParseFlags_FromParsed_Explicit` — argv `--from=parsed`; asserts `flags.from == "parsed"`.
  - `TestParseFlags_FromInvalid_Errors` — argv `--from=cleaned`; asserts error containing `"--from must be raw or parsed"`.
- **Implementation:**
  - Add `--from` flag to `parseFlags()` (per `cmd/replay/main.go:91-120`):
    ```go
    fs.StringVar(&f.from, "from", "raw", "Gateway substitution mode (raw|parsed)")
    ```
  - Add validation block alongside the existing `--format` check at line 117:
    ```go
    if f.from != "raw" && f.from != "parsed" {
        return nil, "", fmt.Errorf("--from must be raw or parsed; got %q", f.from)
    }
    ```
  - Update `flags` struct (line 76-83) to add `from string`.
  - In the existing `Run()` orchestration (read the rest of `main.go` to identify the spot — likely the per-bundle loop), translate `f.from` to `replay.Mode` and pass into `replay.Options`.
  - Update `usageMessage` (line 58-73) to document the new flag.
- **R3 flags explicitly NOT in this task:** `--workers`, `--filter-ticker`, `--filter-since`, `--diff-stages`, `--float-rel-tol`, `--float-abs-tol`. Do not register them. Doing so would create a CLI contract leak (per the prior R1 follow-up #11 "no-op flag" rule).
- **Acceptance:**
  - `go test ./cmd/replay/...` passes.
  - `cmd/replay/` coverage ≥ 80%.
  - Running `go run ./cmd/replay --from=parsed <real-bundle>` exercises the parsed path end-to-end (manual smoke).

### Stage F — Round-Trip Integration Test

#### Task F.1 — `internal/observability/replay/integration_test.go`

This is the headline test for R2. **Per spec §9 R2 acceptance: "the round-trip integration test that produces a real bundle in-memory and replays it."**

- **Tests:**
  1. `TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs`:
     - Construct an `artifact.Bundle` via `artifact.OpenBundle(...)` against a `t.TempDir()` config (`Enabled: true`, `RootPath: tmp`).
     - Run a synthetic but realistic engine flow: stub the SEC/Market/Macro gateways to return canned data; invoke a small driver that mimics what the real handler does — captures snapshots via `b.Snapshot(...)` for each phase (`05-fetch-sec.raw.json`, `06-fetch-market.raw.json`, `07-fetch-macro.parsed.json`, `10-clean-input.json`, etc.), then calls `svc.CalculateValuation`, snapshots the response into `17-response.json`, and `b.Close()` finalizes the manifest.
     - Confirm the bundle directory is fully populated.
     - Call `replay.Replay(ctx, bundleDir, Options{Mode: ModeRaw})`.
     - Assert `result.Status == "pass"` and `result.FieldsChanged == 0`.
  2. `TestRoundTrip_MutatedResponse_FlagsDiff` — same setup, then mutate `17-response.json`'s `dcf_value_per_share` by 1% on disk, replay, assert `Status == "fail"` with exactly one diff at `dcf_value_per_share`.
  3. `TestRoundTrip_MissingRawSEC_ReturnsErroredViaCoordinatorGoroutine` — **CRITICAL per F11.** Same setup, then `os.Remove(<bundleDir>/05-fetch-sec.raw.json)`, replay. Assert:
     - `result.Status == "errored"`.
     - `errors.Is(result.Err, replay.ErrBundleMissingPayload)`.
     - The replay process exits cleanly (i.e., no Go panic is raised — the test wraps `replay.Replay` in a `defer recover()` and asserts `recovered == nil`).
     - The error path runs through the **real** `internal/services/datafetcher/coordinator.go` goroutines (assertion: `service.go`'s engine path is invoked, NOT a synchronous mock; verify via the call having `~10ms+` wall time consistent with goroutine setup). This is the regression guard for F11 / spec §14 row 4.
  4. `TestReplay_CrossYearProducesByteIdenticalOutput` — **D10 regression pin (revised in v2).** This test verifies that `valuation.Clock` injection makes replay outputs invariant to the wall-clock year — the load-bearing reason D10 exists. The previous fallback ("two consecutive replays produce byte-identical output") was rejected: that assertion would pass even if D10's Clock seam was completely broken, because both replays would consult the same `time.Now()` at the same moment. The whole point of D10 — pinning `service.go:245` `time.Now().Year()` for periodKey via `s.clock.Now()` — is to ensure cross-year replay produces *consistent* results, not *consecutive-call-deterministic* results.

     **Test mechanism (mandatory):**
     - Construct a synthetic bundle with `manifest.started_at = "2026-01-15T12:00:00Z"` (any in-2026 timestamp is fine).
     - Run replay 1 with the binary's `valuation.Clock` overridden via `fx.Decorate` to a fixed clock returning `2026-06-01T12:00:00Z` (mid-2026). The override is layered ON TOP of `replay.Module`'s manifest-bound clock decoration so the test can pin a specific calendar year independent of the manifest's value. Capture `result1`.
     - Run replay 2 with the binary's `valuation.Clock` overridden via `fx.Decorate` to a fixed clock returning `2027-06-01T12:00:00Z` (mid-2027). Capture `result2`.
     - Assert the two replays produce byte-identical `*entities.ValuationResult` outputs. Compare via `cmp.Diff(result1, result2)` (or `reflect.DeepEqual` if `cmp` would surface harmless diffs in unexported fields); empty diff is required.
     - The test name follows the convention used elsewhere in `internal/observability/replay/`: `TestReplay_CrossYearProducesByteIdenticalOutput`. If a different convention emerges in adjacent test files (e.g. `TestReplay_<Behavior>` vs `TestReplay_<Setup>_<Expectation>`), align to that — the key is consistency, not the specific shape.

     **Failure mode this test catches:** if a regression in `service.go` reintroduces a `time.Now()` call (or if a new wall-clock leak appears anywhere in the engine path), this test will produce a non-empty diff between the two replays and fail loudly. Today's R0 code (post-D10 commit) passes this test by construction; this test is a regression pin for D10's invariant. It also catches future drift: if a maintainer adds a new `time.Now()` call in a service-layer file under `internal/services/valuation/` that isn't routed through `s.clock.Now()`, this test surfaces it the moment the year-boundary case is exercised.

     **Relationship to existing tests:**
     - `internal/services/valuation/clock_freshness_test.go` (added in cycle-2 commit `60c8572`) tests the freshness-score computation under a fixture clock — that surface tests `freshness` math against a synthetic `Clock` value. The new test exercises the *whole replay pipeline* under fixture clocks — gateway → cleaner → growth → valuation → response render. They are complementary, not duplicate. Reference `clock_freshness_test.go` in the test's doc comment so a future maintainer who sees both knows the partition.

     **File:** `internal/observability/replay/integration_test.go` (this file does not exist yet — it is created as part of Stage F per Task F.1; this test goes in alongside the other three round-trip tests).

     **Test-only `valuation.Clock` decoration mechanism:** the test uses `fx.Decorate` over `valuation.Clock` AFTER `replay.Module(...)` has been applied. Composition order:
     ```
     fx.New(
         replay.Module(bundleDir, opts),                        // binds Clock to manifest.started_at
         fx.Decorate(func(valuation.Clock) valuation.Clock {    // overrides with fixture clock
             return &fixtureClock{at: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
         }),
         fx.NopLogger,
     )
     ```
     `fx.Decorate` applies in declaration order, so the second decoration wins — this is a documented fx behavior and the spike protocol in §2 already verifies decoration composition works at fx 1.24.0. If the spike forces the fallback `GatewayModule` path, the same `fx.Decorate(Clock)` mechanism still applies (Clock is provided in `CoreModule` independent of any gateway split).

     **Caveat:** if a future Go version's float math becomes slightly non-deterministic across years (highly unlikely — Go's `math` package is bit-exact across compilers and architectures), this test would false-fail. See §8 Risks for handling.
- **Implementation:**
  - File: `internal/observability/replay/integration_test.go`.
  - Test fixtures live in `internal/observability/replay/testdata/integration/` for static parts; the dynamic parts (in-memory bundles) are built per-test via `t.TempDir()`.
  - Use `_test.go` build tag conditionals only if the test is too slow for default `go test ./...` runs. R2's headline test should run in default mode (it's the whole point); R3 perf benches are the candidates for `_bench_test.go` exclusion.
- **Acceptance:**
  - `go test ./internal/observability/replay/ -run TestRoundTrip -race -count=10` passes deterministically.
  - The four tests collectively exercise the full §11 REVIEWER audit list.

### Stage G — `go-cmp` Direct Import

#### Task G.1 — Promote `github.com/google/go-cmp` from transitive to direct in `internal/observability/replay/diff.go`

- **Test first:** `diff_test.go` already exists. Add tests that EXERCISE the new `go-cmp`-based response walker:
  - `TestCompareResponse_NoDiffs` — two identical `FairValueResponse{}` values; assert empty `ResultDiff`.
  - `TestCompareResponse_FloatFieldOutsideTolerance` — two responses differing in `dcf_value_per_share` by 5%; assert one `FloatDiff` entry with the correct path and `WithinTolerance == false`.
  - `TestCompareResponse_FloatFieldWithinTolerance` — same fields differ by 1e-10 (under default `1e-9` rel tol); assert it lands in `FloatsWithinTolerance` slice (NOT `Floats`).
  - `TestCompareResponse_StringFieldDiff` — `growth_source` differs; assert one `StringDiff` entry.
  - `TestCompareResponse_NestedStruct_SanityCheck` — `SanityCheck.ImpliedPE` differs; assert path `sanity_check.implied_pe`.
- **Implementation:**
  - Add `"github.com/google/go-cmp/cmp"` and `"github.com/google/go-cmp/cmp/cmpopts"` imports at the top of `diff.go`. **This is the first non-test file to import `go-cmp` in this repo** — promotes it from transitive to direct after `go mod tidy`.
  - Implement `CompareResponse(bundle, current *FairValueResponse, relTol, absTol float64) *ResultDiff`:
    - Use `cmp.Diff(bundle, current, cmpopts.EquateApprox(relTol, absTol), cmpopts.EquateNaNs())` to walk the structure.
    - For each `cmp.Reporter`-emitted path, classify into `FloatDiff` (numeric) vs `StringDiff` (everything else).
    - Use a custom `cmp.Reporter` to capture per-field paths in dotted JSON-style locator format (e.g. `"sanity_check.implied_pe"`).
  - Replace any hand-rolled deep-walk in the existing R1 `diff.go` with the `go-cmp` path where appropriate. The existing `CompareFloat`, `FloatDiffOf`, `ResultDiff`, `StringDiff`, `HasMismatch`, `FieldsChanged`, `SortDiffs` symbols stay as-is (they are exported and may be called by `Replay()` or `output.go`).
- **`go.mod` update:**
  - Run `go mod tidy`.
  - The diff should show `github.com/google/go-cmp v<x.y.z>` moving from the indirect block to the direct block. **No other module-level changes.**
- **Acceptance:**
  - `git diff master..HEAD -- go.mod` shows ONLY the `go-cmp` promotion (per spec NF1).
  - `git diff master..HEAD -- go.sum` shows ONLY checksum changes for `go-cmp` (no new transitive deps).
  - `go test ./internal/observability/replay/ -run TestCompareResponse` passes.
  - Coverage of `diff.go` ≥ 90% (new function-level expectation).

---

## 4. Bundle Gateway Implementation Details

This section is the authoritative contract for Stage A's five gateways. BACKEND should treat the table as a checklist; deviations require HUMAN approval.

### 4.1 Common contract (applies to ALL bundle gateways)

| Aspect | Contract |
|---|---|
| Constructor | `NewBundle<X>Gateway(bundleDir string, mode Mode) *Bundle<X>Gateway` |
| Mode type | `type Mode int; const (ModeRaw Mode = iota; ModeParsed)` — defined once at package scope (suggest `gateway.go` or `types.go`) |
| Goroutine-safety | All exported methods are safe to call concurrently. Struct fields are immutable post-construction. No internal mutexes; rely on `os.ReadFile`'s thread-safety. |
| Missing payload | Return `replay.NewBundleMissingPayloadError(bundleDir, "<canonical-relative-path>", causeOrNil)`. **NEVER panic.** This is a hard F11 invariant — `internal/services/datafetcher/coordinator.go:181-196` runs gateway calls in goroutines under `sync.WaitGroup`, and a panic in a child goroutine is NOT recovered by `cmd/replay/main.go`'s top-level `recover()`. |
| Other errors | Wrap with `fmt.Errorf("replay: <gateway> <method>: %w", err)`. |
| Health check | `HealthCheck(ctx) error` always returns `nil`. Replay does not exercise health probes. |

### 4.2 Per-gateway specifics

| Gateway | Interface to satisfy | Raw-mode payload path | Parsed-mode payload path | Production parser entry point |
|---|---|---|---|---|
| `BundleSECGateway` | `ports.SECGateway` (`gateways.go:88-94`) | `<bundleDir>/05-fetch-sec.raw.json` | `<bundleDir>/05-fetch-sec.parsed.json` | `internal/infra/gateways/sec/parser.go` (BACKEND verifies the canonical entry — likely `ParseCompanyFacts` or similar; cite in implementation comment) |
| `BundleMarketGateway` | `ports.MarketDataGateway` (`gateways.go:97-102`) | `<bundleDir>/06-fetch-market.raw.json` | `<bundleDir>/06-fetch-market.parsed.json` | `internal/infra/gateways/market/yfinance_client.go` (BACKEND verifies parser entry) |
| `BundleMacroGateway` | `ports.MacroDataGateway` (`gateways.go:105-121`) | `<bundleDir>/07-fetch-macro-<seriesID>.raw.json` (multiple files; one per FRED series) | `<bundleDir>/07-fetch-macro.parsed.json` (single aggregated file) | `internal/infra/gateways/macro/gateway.go` (BACKEND verifies; the per-series-id dispatch is the trickiest part of Stage A) |
| `BundleYFinanceGateway` | `ports.YFinanceGateway` (`gateways.go:170-185`) | Reads same `06-fetch-market.raw.json` for overlapping methods (Quote); returns `ErrBundleMissingPayload` for endpoints not in the bundle (`AnalystEstimates`, `KeyStatistics`) | Reads `06-fetch-market.parsed.json` analogously | Reuse `BundleMarketGateway`'s parser dispatch where overlap exists |
| Finzive | `ports.FinziveGateway` | **N/A — out of scope for R2** (not wired in production today) | N/A | N/A |

### 4.3 Open implementation questions for BACKEND to resolve during Stage A

These are details that depend on the production parser's structure; surface results to HUMAN if any of these block work for >2 hours:

1. **SEC parser entry point:** Is `parser.go::ParseCompanyFacts(body []byte) (*entities.CompanyFactsResponse, error)` exposed as a pure function, or is it tangled into `client.go`'s HTTP path? If the latter, factoring it out is a small additive change (≤30 LoC) that should be its own commit before Stage A starts.

2. **Macro per-series dispatch:** RESOLVED in v2 — Task A.6 (macro per-series parser extraction) is now a pre-approved Stage A precondition for A.3. There is no fallback to `ModeParsed`-only for macro: raw-mode symmetry across SEC / Market / Macro is a spec D3 invariant ("`--from=raw` exercises the gateway parser"). The `>30-LoC fallback` clause from v1 is removed.

   Reading `internal/infra/gateways/macro/gateway.go` confirms the parser is currently inline in `getFREDSeries` (lines 277–309) and is well-bounded: ~20–25 LoC of `json.Decode` + observation validation + `strconv.ParseFloat`. It does not depend on `*Gateway` state — every input it consumes (the response body) and every output it produces (the parsed float64) flows through method-local variables. The extraction is mechanical.

   The bundle layout `07-fetch-macro-<seriesID>.raw.json` (per `gateway.go:290`) maps one file per FRED series. The replay path in `BundleMacroGateway.GetTreasuryRates` walks the same `seriesMap` the production code uses (`gateway.go:180-190`), reads each `<seriesID>.raw.json` from the bundle, and dispatches each through the extracted `ParseFREDSeries(seriesID, body)`. The same per-series tolerance the production gateway already applies (skip a series on parse error, log a warn, continue) is preserved.

   **If during A.6 implementation BACKEND discovers the parser is already a pure exported function** (it is not today, verified at v2 authoring time), the task simplifies to "rename and re-export" rather than "extract" — the budget shrinks but the precondition still holds. Surface that case explicitly in the A.6 commit message; it does not change the order of operations.

3. **YFinance fallback semantics:** Does `*valuation.Service` treat a missing `YFinanceClient` as fatal or best-effort? If best-effort, `BundleYFinanceGateway` returning `ErrBundleMissingPayload` for analyst-estimates is fine. If fatal, R2's bundle fixtures must include analyst-estimate snapshots (which don't exist today — the production code at `yfinance_client.go:151` only snapshots the quote, not analyst estimates). Verify before writing tests.

---

## 5. Test Plan

Authoritative file-by-file test inventory for R2 — derived from spec §12 with R2-applicable rows highlighted.

### 5.1 New test files

| File | Test name | Assertion |
|---|---|---|
| `gateway_sec_test.go` | `TestBundleSECGateway_GetCompanyFacts_RawMode_ParsesProductionBytes` | Production parser invoked on raw bytes; result CIK + Facts populated |
| `gateway_sec_test.go` | `TestBundleSECGateway_GetCompanyFacts_ParsedMode_DirectUnmarshal` | Parser bypassed; struct populated from `*.parsed.json` |
| `gateway_sec_test.go` | `TestBundleSECGateway_GetCompanyFacts_MissingFile_ReturnsErrBundleMissingPayload` | `errors.Is(err, ErrBundleMissingPayload)` AND no panic |
| `gateway_sec_test.go` | `TestBundleSECGateway_ConcurrentGetCompanyFacts_RaceFree` | 100x parallel calls under `-race`; no race |
| `gateway_sec_test.go` | `TestBundleSECGateway_HealthCheck_AlwaysOK` | nil return |
| `gateway_market_test.go` | mirror SEC tests for `MarketDataGateway` interface | as above |
| `gateway_market_test.go` | `TestBundleYFinanceGateway_*` (co-located if file naming is shared) | per-method coverage of `ports.YFinanceGateway` |
| `gateway_macro_test.go` | mirror SEC tests for `MacroDataGateway` interface | as above |
| `gateway_macro_test.go` | `TestBundleMacroGateway_GetFXRate_FromCcyEqualsToCcy_ReturnsOne` | `GetFXRate(ctx, "USD", "USD") == 1.0` (short-circuit per `gateways.go:118`) |
| `stubs_test.go` | `TestNotFoundCacheRepo_Get_AlwaysReturnsCacheMiss` | every Get is a miss |
| `stubs_test.go` | `TestNotFoundFinancial/Market/Macro_Repo_NotFound` | sentinel errors |
| `stubs_test.go` | `TestNoOpMetricsService_Counters_NoEffect` | no panics, no side effects |
| `stubs_test.go` | `TestPanicAuthRepo_GetByKey_Panics` | `recover()` catches expected panic |
| `stubs_test.go` | `TestPanicWatchlistRepo_*_Panics` | `recover()` catches expected panic |
| `module_test.go` | `TestModule_OverridesAllGateways` | post-`fx.New`, every gateway resolves to bundle-backed impl |
| `module_test.go` | `TestModule_OverridesAllRepos` | every repo resolves to NotFound stub |
| `module_test.go` | `TestModule_DoesNotConstructDB` | `fx.Invoke(func(*sqlx.DB){})` errors with missing-dep |
| `module_test.go` | `TestModule_DoesNotConstructRedis` | `fx.Invoke(func(*redis.Client){})` errors |
| `module_test.go` | `TestModule_OverridesClock_BindsToManifestStartedAt` | resolved Clock returns manifest's started_at |
| `module_test.go` | `TestModule_PostConstructHook_WiresYFinanceGateway` | `*valuation.Service` carries the bundle's YFinance gateway |
| `replay_test.go` | `TestReplay_HappyPath_NoFieldDiffs` | synthetic bundle → zero diffs |
| `replay_test.go` | `TestReplay_MutatedResponse_FlagsExactlyOneFieldDiff` | one mutated float → one diff entry |
| `replay_test.go` | `TestReplay_MissingPayload_ReturnsErroredResult` | F11 invariant; `errored` not panic |
| `replay_test.go` | `TestReplay_SchemaDrift_RefusedByDefault` | exit-2 path |
| `replay_test.go` | `TestReplay_SchemaDrift_AllowedWithFlag` | warn-and-proceed path |
| `replay_test.go` | `TestReplay_GitDrift_RefusedByDefault` | exit-2 path |
| `replay_test.go` | `TestReplay_GitDrift_AllowedWithFlag` | warn-and-proceed path |
| `integration_test.go` | `TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs` | **HEADLINE TEST** — produce bundle in-memory + replay = pass |
| `integration_test.go` | `TestRoundTrip_MutatedResponse_FlagsDiff` | mutate response on disk; assert FAIL |
| `integration_test.go` | `TestRoundTrip_MissingRawSEC_ReturnsErroredViaCoordinatorGoroutine` | **F11 regression guard** — exits cleanly through coordinator goroutines |
| `integration_test.go` | `TestReplay_CrossYearProducesByteIdenticalOutput` | **D10 regression pin** — same bundle replayed under `valuation.Clock` fixtures at 2026 vs 2027 = byte-identical `*entities.ValuationResult` (revised in v2; replaces the weaker "two consecutive replays" assertion) |
| `cmd/replay/main_test.go` | `TestParseFlags_FromRaw_Default` | `--from` defaults to "raw" |
| `cmd/replay/main_test.go` | `TestParseFlags_FromParsed_Explicit` | `--from=parsed` accepted |
| `cmd/replay/main_test.go` | `TestParseFlags_FromInvalid_Errors` | `--from=cleaned` rejected |
| `diff_test.go` (extended) | `TestCompareResponse_NoDiffs` | identical responses → empty diff |
| `diff_test.go` (extended) | `TestCompareResponse_FloatFieldOutsideTolerance` | path + WithinTolerance correct |
| `diff_test.go` (extended) | `TestCompareResponse_FloatFieldWithinTolerance` | lands in `FloatsWithinTolerance` |
| `diff_test.go` (extended) | `TestCompareResponse_StringFieldDiff` | string mismatch surfaces |
| `diff_test.go` (extended) | `TestCompareResponse_NestedStruct_SanityCheck` | nested-path locator correct |

### 5.2 R3 tests EXPLICITLY OUT OF SCOPE for R2

These belong to the future R3 dispatch:

- `TestReplay_BatchOver100Bundles_DeterministicWithWorkers1` — batch + parallelism.
- `TestReplay_FilterTicker_OnlyMatchingBundles` — `--filter-ticker`.
- `TestReplay_FilterSince_HonorsExtendedDuration` — `--filter-since`.
- `TestReplay_DiffStages_SurfacesPerStageDrift` — `--diff-stages`.
- `TestReplay_DriftedWithinTolerance_VisibleOnlyWithVerbose` — `--verbose`.
- Performance benches NF2 (≤200ms/bundle) and NF3 (≤30s/100 bundles).

---

## 6. Coverage Gates

| Path | Threshold | Source |
|---|---|---|
| `internal/observability/replay/` | ≥ 90% | spec NF6; current R0+R1 baseline 90.7% must not regress |
| `cmd/replay/` | ≥ 80% | spec NF6 |
| `internal/services/valuation/` | no regression vs current 89.1% | implicit; R2 only adds an `interface{}` post-construct hook call site |
| **New per-file expectations** | | |
| `gateway_sec.go` | ≥ 90% | per-file gate |
| `gateway_market.go` | ≥ 90% | per-file gate |
| `gateway_macro.go` | ≥ 90% | per-file gate |
| `stubs.go` | ≥ 90% | per-file gate (panic stubs trivially covered via `recover()` tests) |
| `module.go` | ≥ 90% | per-file gate |
| `replay.go` | ≥ 90% | per-file gate |
| `diff.go` (after Stage G) | ≥ 90% | per-file gate (Stage G adds `CompareResponse`) |

Verification command: `go test ./internal/observability/replay/... ./cmd/replay/... -coverprofile=cov.out && go tool cover -func=cov.out` — inspect each new file's row, ensure ≥ threshold.

---

## 7. Done-When Checklist

BACKEND uses this to determine R2 is ready for VERIFIER hand-off.

- [ ] §2 spike resolved (PASS or fallback executed); commit message documents outcome
- [ ] D1.1 (`BuildIndustryFromResult` rename) merged as a clean rename diff (REVIEWER spec §11 audit)
- [ ] All §3 Stage A tasks complete; gateway tests pass with `-race -count=10`
- [ ] All §3 Stage B tasks complete; stub tests pass
- [ ] All §3 Stage C tasks complete; module tests pass
- [ ] All §3 Stage D tasks complete; orchestration tests pass
- [ ] All §3 Stage E tasks complete; CLI flag tests pass
- [ ] All §3 Stage F tasks complete; the **headline round-trip test** (`TestRoundTrip_ProduceBundleThenReplay_ZeroDiffs`) passes with `-count=10 -race`
- [ ] **D10 cross-year regression pin** (`TestReplay_CrossYearProducesByteIdenticalOutput`) passes with `-count=10 -race` — fixture-clock replays at 2026 vs 2027 produce byte-identical `*entities.ValuationResult` (v2 redirect; replaces v1's weaker "two consecutive replays" assertion)
- [ ] **Macro parser extraction** (Task A.6) merged before Task A.3 — `internal/infra/gateways/macro/parser.go::ParseFREDSeries` exported as a pure function; `getFREDSeries` in `gateway.go` calls through it; pre-existing macro tests pass without behavioral change (v2 redirect)
- [ ] §3 Stage G complete; `diff.go` uses `go-cmp` for response comparison
- [ ] §6 coverage gates met for every file
- [ ] `go test ./... -race` full repo green
- [ ] `go vet ./...` clean
- [ ] `git diff master..HEAD -- pkg/finance/` is empty (D7 v1.1 / NF4 invariant)
- [ ] `git diff master..HEAD -- go.mod` shows ONLY the `go-cmp` direct-promotion (NF1)
- [ ] `git diff master..HEAD -- go.sum` shows ONLY checksum updates for `go-cmp` (no new transitive deps)
- [ ] `grep 'time\.Now\(\)' internal/services/valuation/service.go` returns zero hits in non-test files (D7/D10 audit; should already be true post-R0 but re-verify)
- [ ] Manual smoke: `go run ./cmd/replay --from=raw <real-bundle-from-artifacts/>` produces a PASS row
- [ ] Manual smoke: `go run ./cmd/replay --from=parsed <real-bundle>` produces the same PASS
- [ ] `replay artifacts/<UTC-date>/AAPL/req_<id>/` against a bundle produced by today's server returns PASS within tolerance (the §9 R2 acceptance criterion)
- [ ] `internal/observability/replay/spike_test.go` either deleted OR retained behind `replay_spike` build tag (no impact on default test run)

---

## 8. Risks & How to Handle (R2-Specific)

Spec §14 covers all design risks; the table below is R2-execution-specific.

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| `fx.Decorate` doesn't compose at fx 1.24.0 | Low | Medium — forces Contingent path | §2 spike protocol catches this in ≤ 1 day; fallback `GatewayModule` split adds 1 commit + ~40 LoC |
| Production gateway parser drift (bundle bytes captured at older revision than current parser expects) | Medium | High — round-trip test could fail through no fault of replay | Pin the integration test's bundle to current `git_sha`: produce the bundle in-memory at test time, then replay immediately. Do NOT use a checked-in bundle as the integration source. (Static fixtures in `testdata/` ARE acceptable for unit tests; the round-trip is an integration test.) |
| `datafetcher.coordinator` goroutine timing introduces ordering assumptions that break under `-race` | Medium | Medium | All bundle gateways must be stateless post-construction (verified per §4.1). All tests run with `-race`. The headline integration test runs with `-count=10` to surface flakes. |
| `go-cmp` direct-promotion drags in transitive deps | Low | Low | `go-cmp` is already pinned in `go.sum` via testify. `go mod tidy` should produce a minimal diff. Done-When checklist verifies. If diff includes other modules, BACKEND stops and reports to HUMAN. |
| YFinance/Finzive surfaces in production code change between plan and implementation | Low | Medium | Stage A.4 + A.5 verification step (read production code immediately before implementing). If Finzive becomes wired between plan and implementation, R2 grows by one gateway stub — flag to HUMAN before proceeding. |
| Cross-year regression test (`TestReplay_CrossYearProducesByteIdenticalOutput`) becomes a hard CI gate | Accepted | — | v2 redirect makes D10's cross-year invariant a CI-enforced regression pin. Any future change that reintroduces an unrouted `time.Now()` in the engine path will produce a non-empty diff between the 2026 and 2027 fixture-clock replays and fail the test. This is the desired behavior — flagging here for awareness so reviewers don't treat a future failure as flakiness. |
| Future Go version introduces non-deterministic float math across years | Very Low | Low | Go's `math` package is bit-exact across compilers, architectures, and (historically) versions. If a Go upgrade ever broke this property the cross-year test would false-fail; mitigation would be to relax the byte-identical assertion to `cmp.Diff` with `cmpopts.EquateApprox` at a vanishingly small tolerance. Flag for awareness; no preemptive change. |
| Manifest's `request_id` re-injection collides with existing `logctx.Inject` semantics | Low | Low | BACKEND verifies `internal/observability/logctx/` API before wiring. If the API is already idempotent on re-inject, no work; if not, document and add a small wrapper in `replay.go`. |
| Macro gateway raw-mode parser extraction (Task A.6) reveals hidden `*Gateway` coupling | Low | Medium | v2 redirect pre-approves the extraction (§4.3 OQ2 RESOLVED). The signature `ParseFREDSeries(seriesID string, body []byte) (float64, error)` is the budget. If during implementation BACKEND finds the function would need additional dependencies (logger, config, gateway state), STOP and surface to HUMAN — that signals the parser is more entangled than the v2 read suggests. Read of `gateway.go:277-309` at v2 authoring time confirms the logic is purely byte-level (`json.Decode` + observation validation + `strconv.ParseFloat`); a coupling surprise is not expected. |

---

## 9. Spec Updates Needed Post-R2

Forward-looking; **do not apply during R2 implementation.** Enumerated for the post-R2 docs-update dispatch.

- **`docs/refactoring/observability-replay-tooling-spec.md`:**
  - Append a Change Log row: `2026-MM-DD | v0.3 — R2 SHIPPED as <merge-sha>. Pre-flight fx.Decorate spike: <PASS or FALLBACK>. Macro per-series parser extracted to internal/infra/gateways/macro/parser.go::ParseFREDSeries as a Stage A precondition for raw-mode symmetry across SEC/Market/Macro (D3 invariant). Cross-year regression pinned via TestReplay_CrossYearProducesByteIdenticalOutput using fx.Decorate-injected fixture clocks at 2026 vs 2027 (D10 invariant). ...`
  - Update §1 Status: `DESIGN — awaiting human approval...` → `DESIGN — R0+R1 SHIPPED, R2 SHIPPED, R3 PLANNED`.
  - Update §9 Phase R2 entry: Status SHIPPED + commit SHA.
  - **Note for the post-R2 docs-update dispatch:** the macro per-series parser extraction was an R2 precondition required for raw-mode symmetry. Production behavior of `internal/infra/gateways/macro/gateway.go`'s public surface is unchanged; only the parsing logic moved out of `getFREDSeries` into a pure exported function. Reviewers verifying "no production behavior change in R2" should expect the macro gateway diff to show the inline parsing logic replaced by a `ParseFREDSeries(seriesID, body)` call.

- **`AGENTS.md` Tier 4 table** (per spec §16):
  - Update the planned row to `Phase 2.D R2 SHIPPED [date] as <merge-sha>` once R2 lands.

- **`CLAUDE.md`:**
  - Add to Build & Run section:
    ```bash
    # Replay a captured artifact bundle (Phase 2.D R2)
    go run ./cmd/replay --from=raw artifacts/<UTC-date>/<TICKER>/req_<id>/

    # Replay using parsed-mode (skip gateway parsers; isolate engine math)
    go run ./cmd/replay --from=parsed artifacts/<UTC-date>/<TICKER>/req_<id>/
    ```
  - Add to Architecture section's `cmd/` line: `replay`.
  - Add to "Important Files" table:
    | `cmd/replay/main.go` | Replay CLI: re-runs captured bundles through the current code |
    | `internal/observability/replay/` | Replay core: bundle gateways, fx module, diff, walk |

- **`docs/THESIS.md`:**
  - Move "Replay tooling (observability Phase 2.D)" Planned/In-Progress → Completed Phases (R2 partial; R3 still Planned).

- **`docs/refactoring/observability-narrative-and-artifacts-spec.md`:**
  - Update §13.D: `DESIGN` → `R2 SHIPPED [date] as <merge-sha>; R3 PLANNED`.

None of these are applied as part of R2 implementation; they are the next dispatch's job once the merge SHA exists.

---

## Appendix — Reference Index

| Need | Path |
|---|---|
| Canonical R2 design | `docs/refactoring/observability-replay-tooling-spec.md` (v0.2) |
| Existing R0+R1 replay package | `internal/observability/replay/` |
| Gateway interfaces to satisfy | `internal/core/ports/gateways.go` |
| Goroutine-safety call site | `internal/services/datafetcher/coordinator.go:181-196` |
| fx provider topology | `internal/di/container.go:96-179` |
| Clock production binding | `internal/services/valuation/clock.go` + `internal/di/container.go:162` |
| Engine entry point | `internal/services/valuation/service.go::CalculateValuation` |
| Production handler that builds `FairValueResponse` | `internal/api/v1/handlers/fair_value.go:368-415` |
| Bundle producer surface | `internal/observability/artifact/bundle.go::OpenBundle` |
| Manifest schema | `internal/observability/artifact/manifest.go` |
| Bundle phase filenames | spec §1 (canonical layout); concrete producers cited via grep at `internal/infra/gateways/*/client.go` |

---

**End of R2 Implementation Plan.**
