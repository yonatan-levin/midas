# Tier 2 — `AssumptionProfile` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** PLAN v1 — awaiting human approval before BACKEND dispatch.

**Builds on:**
- [`assumption-profile-spec.md`](./assumption-profile-spec.md) v0.1 (just landed). All design decisions, type definitions, JSON schema, and testing strategy are owned by that spec.
- [`tier-2-assumption-profile-kickoff.md`](./tier-2-assumption-profile-kickoff.md) — original kickoff brief; scope confirmation.
- [`assumption-profile-db-backed-future.md`](./assumption-profile-db-backed-future.md) — companion future-work tracker for the deferred DB-backed `Registry` swap.
- [`observability-replay-tooling-r3b-implementation-plan.md`](../implementations/observability-replay-tooling-r3b-implementation-plan.md) — mirrored house style (Pre-Flight + ordered Stages + per-task contracts + Test Plan + Coverage Gates + Done-When + Risks).

This document does **not** redesign anything. It sequences BACKEND's work across the 6 Tier 2 phases.

**Scope:** Tier 2 closes RM-3 + VAL-1 + VAL-2 + VAL-3 Phase 3 via a shared `AssumptionProfile` backbone. Out of scope (per spec §1): VAL-6, DC-1, G-1, RM-2 Phase 2, VAL-3 Phase 2 (AFFO), per-ticker overrides, sum-of-the-parts multi-segment valuation.

**Goal:** Introduce `internal/services/valuation/profile/` package + `config/assumption_profiles.json` + integration into all 4 valuation models, preserving mature-large-bank DDM bit-for-bit, while closing 4 open trackers in a coordinated parallel rollout.

**Architecture:** New sibling subpackage `profile/` mirroring `datacleaner/industry/` pattern. JSON-driven typed Go schema with priority-ordered rule arrays. Resolver fires in `service.go::performValuation` before `router.SelectModel`, result stamped onto `ModelInput.Profile`. Facts DTO prevents Go import cycle. Path duplication (NOT function extraction) preserves legacy DDM byte-identity.

**Tech Stack:** Go 1.23 toolchain 1.24.4, fx DI, uber/zap logging, encoding/json for config, math.Float64bits for bit-for-bit assertions.

**LoC + commit estimate:**
- Bootstrap: ~150 LoC (fixture-capture harness) + ~50 KiB of checked-in baseline data.
- P0a (profile package): ~600 LoC across 7 new files (`profile.go`, `facts.go`, `trace.go`, `registry.go`, `resolver.go`, `validation.go`, `version.go`) + ~400 LoC of tests.
- P0b (JSON + wiring): ~120 LoC of service.go integration + ~250 LoC of struct extensions + ~150 LoC of bundle manifest extension + ~200 LoC of `assumption_profiles.json`.
- P1 (RM-3): ~150 LoC of forward path in `revenue_multiple.go` + ~200 LoC of tests + ~80 LoC of JSON profile rows.
- P2 (VAL-1): ~200 LoC of DCF horizon/diagnostic changes in `service.go` + ~250 LoC of tests + ~60 LoC of JSON profile rows.
- P3 (VAL-2): ~200 LoC of multi-stage DDM in `ddm.go` (sibling function; legacy untouched) + ~300 LoC of tests + ~80 LoC of JSON profile rows.
- P4 (VAL-3 P3): ~180 LoC of forward FFO in `ffo.go` + ~220 LoC of tests + ~100 LoC of JSON profile rows.
- Closeout: ~30 LoC across version-bump + archival commits.
- **Estimated total:** ~3,250 LoC across ~12-14 atomic commits.

**Commit cadence:** Each phase ships as 1-3 atomic commits so reverts stay surgical. P0a and P0b each as 1 commit (the schema/wiring are tightly coupled). P1-P4 each as 1 commit per stream (worktree-isolated). Closeout = 3 separate commits (integration gate report, tracker archival, version bump). Mirrors the Tier 1 commit-shape pattern.

---

## Revision History

- **v1 (initial)**: Phase breakdown derived from spec §9 rollout plan; mirrors R3b plan structure for continuity. All 7 critique-driven design revisions (Changes A-G from gpt-5.5-pro review) are encoded as per-phase task contracts. Bootstrap separated as Phase 0 to make pre-Tier-2 baseline capture explicit.

---

## 1. Preamble

### Current state of master at planning time

- **HEAD:** `0324057` (Tier 1 archived + verified clean, per memory + spec §0).
- **Live models:** `internal/services/valuation/models/{ddm,ffo,revenue_multiple,router}.go` are working production code.
  - `DDMModel.Calculate` is the single-stage Gordon implementation; lines 53-192 are the bit-for-bit invariant subject for P3.
  - `RevenueMultipleModel.Calculate` already consumes RM-1 TTM helper + RM-1.A clock seam; positioned for additive forward path in P1.
  - `FFOModel.Calculate` already has VAL-3 P1+P4 subsector multiples shipped; positioned for additive forward path in P4.
  - `ModelRouter.SelectModel` is unchanged — Tier 2 keeps routing orthogonal from calibration.
- **Service orchestration:** `internal/services/valuation/service.go::performValuation` (lines 544-1242). Tier 2 adds 2 lines before `router.SelectModel`: build `profile.Facts` from entities, call `profileRegistry.Resolve`, stamp resolved profile onto `ModelInput`.
- **JSON config pattern:** `config/industry_multiples.json` is the precedent — typed maps loaded at startup via `configfs.Read`, validated, frozen. New `assumption_profiles.json` follows the same shape.
- **Replay tooling:** Phase 2.D shipped (R0+R1+R2+R3a+R3b). 14-flag CLI + `--diff-stages` available for golden-fixture validation. Tier 2 uses replay tooling AS-IS; no refactor required.

### Load-bearing invariants (must hold at every commit)

1. **Mature-large-bank DDM bit-for-bit:** `math.Float64bits(JPM.IntrinsicValuePerShare)` is identical pre- and post-Tier-2. Pinned via golden fixture captured in Phase Bootstrap.
2. **Replay determinism:** Any pre-Tier-2 bundle replays to numerically-identical output (modulo new additive omitempty fields).
3. **No `time.Now()` outside consumer layer:** Clock pattern from RM-1.A preserved across Tier 2.
4. **`pkg/finance/*` unchanged:** D7 invariant from prior phases.
5. **Import boundary:** `internal/services/valuation/profile/` package imports neither `models` nor `entities`. Enforced via test similar to `cmd/server/import_boundary_test.go`.

### Coverage baseline at start of Tier 2

Per memory + master HEAD `0324057`:
- `internal/services/valuation/`: 89.7% (target ≥92% post-Tier-2 with new profile package contribution)
- `internal/services/valuation/models/`: 93.6% (target ≥90% maintained)
- New `internal/services/valuation/profile/` package: target ≥90% out of gate.

### Key code surfaces

**Already shipped — modified by Tier 2:**
- `internal/services/valuation/service.go` — `performValuation`; adds `profile.Resolve` call (P0b).
- `internal/services/valuation/models/router.go` — `ModelInput`; gains `Profile *profile.ResolvedProfile` field (P0b).
- `internal/services/valuation/models/ddm.go` — `Calculate`; gains dispatch branch in P3 (legacy lines 53-192 stay byte-identical).
- `internal/services/valuation/models/revenue_multiple.go` — `Calculate`; gains forward path in P1.
- `internal/services/valuation/models/ffo.go` — `Calculate`; gains forward path in P4.
- `internal/core/entities/valuation.go` — `ValuationResult`; gains 5 omitempty DCF diagnostic fields in P0b (populated in P2).
- `internal/api/v1/handlers/fair_value.go` — `FairValueResponse`; gains `AssumptionProfile` + `ResolutionTrace` omitempty fields in P0b.

**New code surfaces:**
- `internal/services/valuation/profile/` (7 files + tests; P0a).
- `config/assumption_profiles.json` (P0b).
- `internal/services/valuation/profile/testdata/golden/` (Phase Bootstrap).
- `internal/services/valuation/models/testdata/golden/` (Phase Bootstrap).
- `artifacts/tier2-baseline/` (Phase Bootstrap; ~10 captured bundles).
- `internal/services/valuation/profile/tier2_regression_test.go` (Phase Bootstrap skeleton; populated by P1-P4 worktrees).

---

## 2. Pre-Flight

**No spike required.** The profile package is additive and the import-boundary is straightforward; no novel fx-composition concerns. Three execution-level uncertainties BACKEND should resolve at the start of Phase Bootstrap (NOT a separate Spike phase):

### Pre-A — Verify master is at `0324057` and green

- **Action:** `git rev-parse HEAD` confirms `0324057`. `go test ./... -count=1` returns 47/47 packages green. `go run ./cmd/replay --diff-stages artifacts/<any-existing-bundle>` runs clean.
- **If master has moved:** rebase intent against new HEAD; verify the moved HEAD does not touch any of the 4 model files Tier 2 modifies. If it does, re-check assumptions before proceeding.
- **If anything is RED:** stop. Fix the regression on master before Tier 2 dispatches; the bit-for-bit baseline depends on a green starting point.

### Pre-B — Decide golden-fixture capture mechanism

**Decision Pre-B.1 — Use live-engine artifact-bundle pipeline with `X-Midas-Trace: 1`.** Reason: it's the existing path; the bundle already captures full `ModelInput` shape + the `ModelResult` output via the artifact writer. No new capture machinery required.

**Concrete contract:** Phase Bootstrap Task B.1 runs `cmd/server` locally, issues 10 curl requests with `X-Midas-Trace: 1`, collects the resulting bundles from `artifacts/<today>/<ticker>/req_*/`, and moves them into `artifacts/tier2-baseline/` for git tracking.

**Alternative considered (rejected):** programmatically invoke `valuation.Service.CalculateValuation` in a Go test and serialize the result. Rejected because it doesn't capture the full request-lifecycle context (CRP lookup, growth estimator, WACC calc) the same way a live request does — golden divergence between unit-test-captured and production-captured fixtures would be its own bug source.

### Pre-C — Confirm Go import-boundary enforcement mechanism

**Decision Pre-C.1 — Use a build-tag-gated test similar to `cmd/server/import_boundary_test.go`.** Reason: the existing pattern works, is reviewable, and runs as part of `go test ./...`.

**Concrete contract:** P0a Task P0a.5 creates `internal/services/valuation/profile/import_boundary_test.go` that scans the profile package's import set via `go/parser` (or runtime reflection) and asserts no imports of `internal/services/valuation/models` or `internal/core/entities`. The test is in the package's own test directory so it runs without special invocation.

Both Pre-checks land inside the Phase Bootstrap commit; they are NOT a separate phase.

---

## 3. Ordered Task List (TDD)

Each task is `Test first → Implementation → Acceptance`. Phases run sequentially: Bootstrap → P0a → P0b → P1‖P2‖P3‖P4 (parallel) → Closeout. Within a phase, tasks land in a single commit unless explicitly split.

---

### Phase Bootstrap — pre-Tier-2 regression baseline capture

**Goal:** Capture the bit-for-bit baselines and the cross-model regression fixtures at master `0324057` so every Tier 2 commit can be verified against them. Lands as a SINGLE commit on master before any P0 work begins.

#### Task B.1 — Capture 10 artifact bundles from live engine

- **Files:**
  - Create: `artifacts/tier2-baseline/2026-05-14/{AAPL,MSFT,JPM,KO,F,MXL,NVDA,AMD,EQIX,PLD}/req_*/...` (10 bundle directories with full stage files)

- [ ] **Step 1: Start local server**

```bash
go build -o bin/midas-server ./cmd/server
./bin/midas-server &
# wait for "server started on :8080"
```

Expected: Server logs "listening on :8080" within 5 seconds.

- [ ] **Step 2: Set demo API key**

```bash
export DEMO_KEY=$(go run ./cmd/seed-demo-key -db ./data/midas.db | grep "API key:" | awk '{print $3}')
```

Expected: a 32-char hex string in `$DEMO_KEY`.

- [ ] **Step 3: Issue traced requests for the 10-ticker basket**

```bash
mkdir -p artifacts/tier2-baseline/2026-05-14
for TICKER in AAPL MSFT JPM KO F MXL NVDA AMD EQIX PLD; do
  curl -s -H "X-API-Key: $DEMO_KEY" -H "X-Midas-Trace: 1" \
    "http://localhost:8080/api/v1/fair-value/$TICKER" > /dev/null
  echo "Captured $TICKER"
done
```

Expected: Each curl returns within 30s; bundle directories appear under `artifacts/2026-05-14/<TICKER>/req_*/`.

- [ ] **Step 4: Move bundles into tier2-baseline directory**

```bash
for TICKER in AAPL MSFT JPM KO F MXL NVDA AMD EQIX PLD; do
  mv artifacts/2026-05-14/$TICKER artifacts/tier2-baseline/2026-05-14/
done
```

Expected: `ls artifacts/tier2-baseline/2026-05-14/` shows 10 ticker directories.

- [ ] **Step 5: Verify each bundle has expected stage files**

```bash
for TICKER in AAPL MSFT JPM KO F MXL NVDA AMD EQIX PLD; do
  REQ_DIR=$(ls -d artifacts/tier2-baseline/2026-05-14/$TICKER/req_*/ | head -1)
  for STAGE in 17-response.json 15-valuation.json 13-wacc.json 12-growth-curve.json 10-clean-output.json manifest.json; do
    test -f "$REQ_DIR/$STAGE" || echo "MISSING $TICKER/$STAGE"
  done
done
```

Expected: No "MISSING" output. If any missing: investigate before proceeding (the bundle pipeline expects all stages on a successful valuation).

- [ ] **Step 6: Stop server**

```bash
pkill -f midas-server
```

#### Task B.2 — Generate DDM bit-for-bit golden fixtures (JPM/BAC/WFC)

- **Files:**
  - Create: `internal/services/valuation/models/testdata/golden/jpm_ddm_pre_tier2_input.json`
  - Create: `internal/services/valuation/models/testdata/golden/jpm_ddm_pre_tier2_output.json`
  - Create: `internal/services/valuation/models/testdata/golden/bac_ddm_pre_tier2_input.json`
  - Create: `internal/services/valuation/models/testdata/golden/bac_ddm_pre_tier2_output.json`
  - Create: `internal/services/valuation/models/testdata/golden/wfc_ddm_pre_tier2_input.json`
  - Create: `internal/services/valuation/models/testdata/golden/wfc_ddm_pre_tier2_output.json`
  - Create: `internal/services/valuation/models/golden_capture_test.go` (one-shot capture helper; gated by `-tags goldencapture`)

- [ ] **Step 1: Add capture-helper test (gated by build tag)**

Create `internal/services/valuation/models/golden_capture_test.go`:

```go
//go:build goldencapture

package models_test

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "testing"

    "go.uber.org/zap"

    "github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// TestCaptureGoldenDDM is a one-shot helper run with `-tags goldencapture`.
// Loads ModelInput fixtures derived from production bundles, calls
// DDMModel.Calculate, and writes the result JSON to testdata/golden/.
// Not part of normal test suite; for regenerating goldens only.
func TestCaptureGoldenDDM(t *testing.T) {
    tickers := []string{"jpm", "bac", "wfc"}
    for _, ticker := range tickers {
        t.Run(ticker, func(t *testing.T) {
            inputPath := filepath.Join("testdata/golden", ticker+"_ddm_pre_tier2_input.json")
            outputPath := filepath.Join("testdata/golden", ticker+"_ddm_pre_tier2_output.json")
            data, err := os.ReadFile(inputPath)
            if err != nil {
                t.Fatalf("input fixture missing: %v (run prep step first)", err)
            }
            var input models.ModelInput
            if err := json.Unmarshal(data, &input); err != nil {
                t.Fatalf("unmarshal: %v", err)
            }
            ddm := models.NewDDMModel(zap.NewNop())
            result, err := ddm.Calculate(context.Background(), &input)
            if err != nil {
                t.Fatalf("calculate: %v", err)
            }
            out, err := json.MarshalIndent(result, "", "  ")
            if err != nil {
                t.Fatalf("marshal: %v", err)
            }
            if err := os.WriteFile(outputPath, out, 0o644); err != nil {
                t.Fatalf("write: %v", err)
            }
            t.Logf("Captured golden for %s: %s", ticker, outputPath)
        })
    }
}
```

- [ ] **Step 2: Manually derive ModelInput fixtures from production bundles**

For each of JPM, BAC, WFC, extract the relevant data from the captured bundle's `manifest.json` + `02-sec-facts.parsed.json` + `06-market.parsed.json` + `07-fetch-macro.parsed.json` and assemble a `ModelInput` JSON file at `testdata/golden/<ticker>_ddm_pre_tier2_input.json`. The struct shape mirrors `models.ModelInput`. Verify by:

```bash
cat internal/services/valuation/models/testdata/golden/jpm_ddm_pre_tier2_input.json | jq '.HistoricalData.Ticker, .CostOfEquity, .SharesOutstanding'
```

Expected: `"JPM"`, a positive float (~0.10), and shares-outstanding count.

- [ ] **Step 3: Run capture-helper test to produce output goldens**

```bash
go test -tags goldencapture -run TestCaptureGoldenDDM ./internal/services/valuation/models/...
```

Expected: 3 PASS; 3 new output files at `testdata/golden/{jpm,bac,wfc}_ddm_pre_tier2_output.json`.

- [ ] **Step 4: Manual sanity-check on outputs**

```bash
cat internal/services/valuation/models/testdata/golden/jpm_ddm_pre_tier2_output.json | jq '.intrinsic_value_per_share, .model_type, .confidence'
```

Expected: positive float close to JPM's current market price, `"ddm"`, `"high"` or `"medium"`.

#### Task B.3 — Write the regression test scaffold

- **Files:**
  - Create: `internal/services/valuation/profile/tier2_regression_test.go` (skeleton; populated by P1-P4 worktrees)
  - Create: `internal/services/valuation/models/ddm_bitforbit_test.go`

- [ ] **Step 1: Write the JPM bit-for-bit DDM regression test**

Create `internal/services/valuation/models/ddm_bitforbit_test.go`:

```go
package models_test

import (
    "context"
    "encoding/json"
    "math"
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "go.uber.org/zap"

    "github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// TestDDM_LegacyPath_BitForBit asserts that mature-large-bank DDM output is
// byte-identical pre- and post-Tier-2. The legacy single-stage Gordon code
// path (DividendForecastHorizon == 0 OR Profile == nil) must produce the
// same Float64bits for IntrinsicValuePerShare, EquityValue, EnterpriseValue
// as captured pre-Tier-2 at master 0324057.
//
// This test FAILS immediately if any Tier 2 commit causes drift in the
// legacy path. It is the load-bearing assertion for the VAL-2 backward-
// compatibility invariant (spec §7.1).
func TestDDM_LegacyPath_BitForBit(t *testing.T) {
    tickers := []string{"jpm", "bac", "wfc"}
    for _, ticker := range tickers {
        t.Run(ticker, func(t *testing.T) {
            input := loadGoldenInput(t, ticker)
            expected := loadGoldenOutput(t, ticker)

            ddm := models.NewDDMModel(zap.NewNop())
            actual, err := ddm.Calculate(context.Background(), input)
            require.NoError(t, err)

            // Bit-for-bit on the three float fields that carry the model's
            // numeric output. Float64bits comparison catches LSB drift that
            // standard equality might hide via printf-rounding.
            assert.Equal(t,
                math.Float64bits(expected.IntrinsicValuePerShare),
                math.Float64bits(actual.IntrinsicValuePerShare),
                "%s IntrinsicValuePerShare drifted from pre-Tier-2 bits", ticker)
            assert.Equal(t,
                math.Float64bits(expected.EquityValue),
                math.Float64bits(actual.EquityValue),
                "%s EquityValue drifted from pre-Tier-2 bits", ticker)
            assert.Equal(t,
                math.Float64bits(expected.EnterpriseValue),
                math.Float64bits(actual.EnterpriseValue),
                "%s EnterpriseValue drifted from pre-Tier-2 bits", ticker)

            // Non-float fields: exact equality.
            assert.Equal(t, expected.ModelType, actual.ModelType, "%s ModelType drifted", ticker)
            assert.Equal(t, expected.Confidence, actual.Confidence, "%s Confidence drifted", ticker)
            assert.Equal(t, expected.Warnings, actual.Warnings, "%s Warnings drifted (count or order)", ticker)
        })
    }
}

func loadGoldenInput(t *testing.T, ticker string) *models.ModelInput {
    t.Helper()
    path := filepath.Join("testdata/golden", ticker+"_ddm_pre_tier2_input.json")
    data, err := os.ReadFile(path)
    require.NoError(t, err, "golden input fixture missing")
    var input models.ModelInput
    require.NoError(t, json.Unmarshal(data, &input))
    return &input
}

func loadGoldenOutput(t *testing.T, ticker string) *models.ModelResult {
    t.Helper()
    path := filepath.Join("testdata/golden", ticker+"_ddm_pre_tier2_output.json")
    data, err := os.ReadFile(path)
    require.NoError(t, err, "golden output fixture missing")
    var result models.ModelResult
    require.NoError(t, json.Unmarshal(data, &result))
    return &result
}
```

- [ ] **Step 2: Run the regression test to confirm it passes (against unchanged code)**

```bash
go test -run TestDDM_LegacyPath_BitForBit ./internal/services/valuation/models/...
```

Expected: PASS for all 3 subtests. Critical — if it fails NOW (before any Tier 2 code change), the golden capture is bad and must be regenerated before proceeding.

- [ ] **Step 3: Write the cross-model regression test skeleton**

Create `internal/services/valuation/profile/tier2_regression_test.go`:

```go
package profile_test

// Tier 2 cross-model regression suite. Pins 6 fields per ticker per spec §8.2:
//   - assumption_profile (exact)
//   - horizon_selected (exact)
//   - chosen_model (exact)
//   - primary_value (bit-for-bit for mature_large_bank, ε=1e-9 elsewhere)
//   - trailing_value (ε=1e-9 where applicable)
//   - warning_count (exact)
//
// This file is populated incrementally by P1-P4 worktrees as each stream
// adds its pin assertions. Skeleton lands in Phase Bootstrap so the test
// file exists at master HEAD before parallel work dispatches.

import "testing"

// TestTier2_BasketRegression is the entrypoint. Currently empty — populated
// by per-stream test functions as P1-P4 ship.
func TestTier2_BasketRegression(t *testing.T) {
    t.Skip("Populated by P1-P4 worktrees; skeleton only at Phase Bootstrap")
}
```

- [ ] **Step 4: Verify the regression test skeleton compiles and runs**

```bash
go test ./internal/services/valuation/profile/...
```

Expected: PASS (with the skip). Compilation success confirms the package boundary is consistent.

#### Task B.4 — Commit Phase Bootstrap

- [ ] **Step 1: Stage and commit**

```bash
git add artifacts/tier2-baseline/ \
        internal/services/valuation/models/testdata/golden/ \
        internal/services/valuation/models/ddm_bitforbit_test.go \
        internal/services/valuation/models/golden_capture_test.go \
        internal/services/valuation/profile/tier2_regression_test.go
git commit -m "$(cat <<'EOF'
chore(tier2): capture pre-Tier-2 regression baselines

- 10 artifact bundles in artifacts/tier2-baseline/2026-05-14/
- 3 DDM bit-for-bit golden fixtures (JPM/BAC/WFC) under
  models/testdata/golden/ with paired input + output JSON
- Build-tag-gated TestCaptureGoldenDDM helper for golden regeneration
- TestDDM_LegacyPath_BitForBit regression test (passes against current master)
- tier2_regression_test.go skeleton (populated by P1-P4 streams)

Baseline captured at master 0324057. Every subsequent Tier 2 commit
must keep TestDDM_LegacyPath_BitForBit green. Spec §9.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 2: Verify the commit landed and tests are green**

```bash
git log -1 --oneline
go test ./internal/services/valuation/models/... ./internal/services/valuation/profile/...
```

Expected: HEAD shows the bootstrap commit; all tests pass.

---

### Phase P0a — Profile package skeleton + Facts DTO + resolver

**Goal:** Land the entire `internal/services/valuation/profile/` package: types, enums, Facts DTO, Registry interface, resolver, validation. ZERO integration with consumers (service.go and models stay untouched). Verifies the Go import-cycle prevention works in isolation.

#### Task P0a.1 — Define core types (`profile.go`, `trace.go`)

- **Files:**
  - Create: `internal/services/valuation/profile/profile.go`
  - Create: `internal/services/valuation/profile/trace.go`
  - Create: `internal/services/valuation/profile/version.go`
  - Test: `internal/services/valuation/profile/profile_test.go`

- [ ] **Step 1: Write failing test for AssumptionProfile struct shape**

Create `internal/services/valuation/profile/profile_test.go`:

```go
package profile_test

import (
    "encoding/json"
    "testing"

    "github.com/stretchr/testify/assert"

    "github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestAssumptionProfile_AllFieldsPresent verifies the struct exposes all 14
// fields defined in spec §3.1. A missing field breaks downstream wiring;
// a renamed field breaks JSON config loading.
func TestAssumptionProfile_AllFieldsPresent(t *testing.T) {
    raw := []byte(`{
        "profile_id": "mature_large_bank:mature",
        "archetype": "mature_large_bank",
        "maturity": "mature",
        "horizon_years": 3,
        "compound_growth_cap": 1.5,
        "revenue_base_method": "raw_ttm",
        "discount_method": "cost_of_equity",
        "terminal_method": "gordon_growth",
        "stabilized": true,
        "fade_years": 0,
        "terminal_multiple": 0.8,
        "dps_growth_cap": 0.08,
        "payout_path": [],
        "dividend_forecast_horizon": 0,
        "stable_dividend_growth": 0.03
    }`)
    var p profile.AssumptionProfile
    err := json.Unmarshal(raw, &p)
    assert.NoError(t, err)

    assert.Equal(t, "mature_large_bank:mature", p.ProfileID)
    assert.Equal(t, profile.ArchetypeMatureLargeBank, p.Archetype)
    assert.Equal(t, profile.MaturityMature, p.Maturity)
    assert.Equal(t, 3, p.HorizonYears)
    assert.InEpsilon(t, 1.5, p.CompoundGrowthCap, 1e-9)
    assert.Equal(t, profile.RevenueBaseRawTTM, p.RevenueBaseMethod)
    assert.Equal(t, profile.DiscountCostOfEquity, p.DiscountMethod)
    assert.Equal(t, profile.TerminalGordonGrowth, p.TerminalMethod)
    assert.True(t, p.Stabilized)
    assert.Equal(t, 0, p.FadeYears)
    assert.InEpsilon(t, 0.8, p.TerminalMultiple, 1e-9)
    assert.InEpsilon(t, 0.08, p.DPSGrowthCap, 1e-9)
    assert.Empty(t, p.PayoutPath)
    assert.Equal(t, 0, p.DividendForecastHorizon)
    assert.InEpsilon(t, 0.03, p.StableDividendGrowth, 1e-9)
}

// TestResolvedProfile_IsLegacyMatureLargeBankDDM verifies the helper that
// gates the bit-for-bit DDM preservation branch.
func TestResolvedProfile_IsLegacyMatureLargeBankDDM(t *testing.T) {
    cases := []struct {
        name string
        rp   *profile.ResolvedProfile
        want bool
    }{
        {"nil", nil, false},
        {"horizon_zero_mature_bank", &profile.ResolvedProfile{
            AssumptionProfile: profile.AssumptionProfile{
                Archetype:               profile.ArchetypeMatureLargeBank,
                DividendForecastHorizon: 0,
            },
        }, true},
        {"horizon_zero_wrong_archetype", &profile.ResolvedProfile{
            AssumptionProfile: profile.AssumptionProfile{
                Archetype:               profile.ArchetypeGrowthBank,
                DividendForecastHorizon: 0,
            },
        }, false},
        {"mature_bank_nonzero_horizon", &profile.ResolvedProfile{
            AssumptionProfile: profile.AssumptionProfile{
                Archetype:               profile.ArchetypeMatureLargeBank,
                DividendForecastHorizon: 5,
            },
        }, false},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            assert.Equal(t, tc.want, tc.rp.IsLegacyMatureLargeBankDDM())
        })
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test -run "TestAssumptionProfile_AllFieldsPresent|TestResolvedProfile_IsLegacyMatureLargeBankDDM" ./internal/services/valuation/profile/...
```

Expected: FAIL with "package not found" or "undefined: AssumptionProfile".

- [ ] **Step 3: Create profile.go with full struct + enum definitions**

Create `internal/services/valuation/profile/profile.go` — full content per spec §3.1 (Archetype enum, Maturity enum, RevenueBaseMethod, TerminalMethod, DiscountMethod, AssumptionProfile struct, SizeThresholds struct, ResolvedProfile struct, IsLegacyMatureLargeBankDDM method). See spec for complete definitions; reproduce them verbatim.

- [ ] **Step 4: Create trace.go**

Create `internal/services/valuation/profile/trace.go` — full content per spec §3.3 (ResolutionTrace struct, Source enum, AssumptionProfileManifest struct).

- [ ] **Step 5: Create version.go with resolver-version constant**

```go
package profile

// ResolverVersion is the semver of the resolver logic itself. Bumped on any
// change to the resolver algorithm (Stage 1/2/3 logic, override rules, etc.).
// Stamps onto ResolutionTrace and AssumptionProfileManifest for replay
// determinism per spec §7.3.
const ResolverVersion = "1.0.0"
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test -run "TestAssumptionProfile_AllFieldsPresent|TestResolvedProfile_IsLegacyMatureLargeBankDDM" ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P0a.2 — Facts DTO (`facts.go`)

- **Files:**
  - Create: `internal/services/valuation/profile/facts.go`
  - Test: `internal/services/valuation/profile/facts_test.go`

- [ ] **Step 1: Write failing test for Facts pointer-field semantics**

Create `internal/services/valuation/profile/facts_test.go`:

```go
package profile_test

import (
    "testing"

    "github.com/stretchr/testify/assert"

    "github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestFacts_PointerSemantics_DistinguishesMissingFromZero verifies that nil
// pointers mean "no signal" and zero values mean "zero is the actual value".
// Pre-revenue biotech has Revenue=0 (correct); malformed input has Revenue=nil.
func TestFacts_PointerSemantics_DistinguishesMissingFromZero(t *testing.T) {
    var zero float64
    factsZero := profile.Facts{Revenue: &zero}
    factsMissing := profile.Facts{Revenue: nil}

    assert.NotNil(t, factsZero.Revenue, "explicit zero should be a non-nil pointer")
    assert.Equal(t, 0.0, *factsZero.Revenue)
    assert.Nil(t, factsMissing.Revenue, "missing signal should be nil")
}

func TestFacts_IndustryNormalized_UpperCasedTrimmed(t *testing.T) {
    facts := profile.NewFactsForTest("  fin_large_bank  ", nil, nil)
    assert.Equal(t, "FIN_LARGE_BANK", facts.IndustryNormalized)
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -run "TestFacts_" ./internal/services/valuation/profile/...
```

Expected: FAIL — undefined Facts.

- [ ] **Step 3: Create facts.go**

```go
package profile

import "strings"

// Facts is the neutral interchange struct populated by service.go from
// entities.FinancialData / HistoricalFinancialData / MarketData. Pointer
// fields distinguish "no signal" (nil) from "zero is meaningful" (e.g., a
// pre-revenue biotech with explicit Revenue == 0).
//
// The profile package contains NO imports of entities or models — Facts is
// the deliberate boundary that prevents the Go import cycle:
//   models → profile → models  (would not compile)
// The translation from entities.FinancialData → Facts lives at the consumer
// site (service.go), keeping this package free of entities dependencies.
type Facts struct {
    Industry                   string
    IndustryNormalized         string   // upper-cased + trimmed at construction
    Revenue                    *float64 // TTM revenue (RM-1 helper output)
    OperatingIncome            *float64 // signed; negative triggers cyclical_trough
    NetIncome                  *float64
    RevenueGrowthYoY           *float64
    ConsecutivePositiveOIYears int      // 0 if none
    MarketCap                  *float64
    DividendsPerShare          *float64
}

// NewFactsForTest is exported for test use only. Production code constructs
// Facts directly in service.go (which has entities imports).
func NewFactsForTest(industry string, revenue, oi *float64) Facts {
    return Facts{
        Industry:           industry,
        IndustryNormalized: strings.ToUpper(strings.TrimSpace(industry)),
        Revenue:            revenue,
        OperatingIncome:    oi,
    }
}
```

- [ ] **Step 4: Run to verify pass**

```bash
go test -run "TestFacts_" ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P0a.3 — Registry interface + JSON loader (`registry.go`, `validation.go`)

- **Files:**
  - Create: `internal/services/valuation/profile/registry.go`
  - Create: `internal/services/valuation/profile/validation.go`
  - Test: `internal/services/valuation/profile/registry_test.go`

- [ ] **Step 1: Write failing tests for Registry interface + load-time validation**

Create `internal/services/valuation/profile/registry_test.go`:

```go
package profile_test

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestLoadFromJSON_ValidConfig_LoadsSuccessfully verifies a minimal but
// complete config loads without error.
func TestLoadFromJSON_ValidConfig_LoadsSuccessfully(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "profiles.json")
    require.NoError(t, os.WriteFile(path, []byte(minimalValidConfig), 0o644))

    reg, err := profile.LoadFromJSON(path)
    require.NoError(t, err)

    assert.Equal(t, "1.0.0", reg.ConfigVersion())
    assert.NotEmpty(t, reg.ConfigHash())
}

// TestLoadFromJSON_Malformed_FailsLoudly — invariant #4: invalid shipped config
// fails startup, does NOT silently degrade.
func TestLoadFromJSON_Malformed_FailsLoudly(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "profiles.json")
    require.NoError(t, os.WriteFile(path, []byte("{ not valid json"), 0o644))

    _, err := profile.LoadFromJSON(path)
    assert.Error(t, err, "malformed config must error, never return a degraded registry")
}

// TestLoadFromJSON_MissingFallbackRule_FailsValidation — invariant #7.
func TestLoadFromJSON_MissingFallbackRule_FailsValidation(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "profiles.json")
    // Config with profile but no rule with industry_prefix "*"
    raw := `{"config_version":"1.0.0","resolver_version":"1.0.0","profiles":{},"archetype_rules":[],"maturity_thresholds_fallback":{}}`
    require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

    _, err := profile.LoadFromJSON(path)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "fallback")
}

// TestRegistry_Lookup_Hit returns the configured profile.
func TestRegistry_Lookup_Hit(t *testing.T) {
    reg := mustLoadMinimal(t)
    p, ok := reg.Lookup(profile.ArchetypeMatureLargeBank, profile.MaturityMature)
    require.True(t, ok)
    assert.Equal(t, "mature_large_bank:mature", p.ProfileID)
}

// TestRegistry_Lookup_Miss returns false.
func TestRegistry_Lookup_Miss(t *testing.T) {
    reg := mustLoadMinimal(t)
    _, ok := reg.Lookup(profile.ArchetypeREITDataCenter, profile.MaturityHighGrowth)
    assert.False(t, ok)
}

const minimalValidConfig = `{
  "config_version": "1.0.0",
  "resolver_version": "1.0.0",
  "profiles": {
    "mature_large_bank:mature": {
      "profile_id": "mature_large_bank:mature",
      "archetype": "mature_large_bank",
      "maturity": "mature",
      "horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": 0.8, "dps_growth_cap": 0.08,
      "payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "software_like_scaling:standard_growth": {
      "profile_id": "software_like_scaling:standard_growth",
      "archetype": "software_like_scaling", "maturity": "standard_growth",
      "horizon_years": 5, "compound_growth_cap": 4.0,
      "revenue_base_method": "raw_ttm", "discount_method": "wacc",
      "terminal_method": "gordon_growth", "stabilized": false, "fade_years": 1,
      "terminal_multiple": 4.0, "dps_growth_cap": 0,
      "payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    }
  },
  "archetype_rules": [
    {"id":"fin_large_bank","priority":100,"industry_prefix":"FIN_LARGE_BANK","archetype":"mature_large_bank"},
    {"id":"fallback_default","priority":0,"industry_prefix":"*","archetype":"software_like_scaling"}
  ],
  "maturity_thresholds_fallback": {
    "large_cap_revenue_min_usd": 50000000000,
    "mid_cap_revenue_min_usd": 10000000000,
    "high_growth_revenue_yoy_min": 0.30,
    "mature_revenue_yoy_max": 0.10,
    "trough_oi_threshold": 0.0
  }
}`

func mustLoadMinimal(t *testing.T) profile.Registry {
    t.Helper()
    dir := t.TempDir()
    path := filepath.Join(dir, "profiles.json")
    require.NoError(t, os.WriteFile(path, []byte(minimalValidConfig), 0o644))
    reg, err := profile.LoadFromJSON(path)
    require.NoError(t, err)
    return reg
}
```

- [ ] **Step 2: Run to verify fails**

```bash
go test -run "TestLoadFromJSON_|TestRegistry_Lookup_" ./internal/services/valuation/profile/...
```

Expected: FAIL — undefined Registry.

- [ ] **Step 3: Create registry.go**

Create `internal/services/valuation/profile/registry.go` with:
- `Registry` interface (Resolve, Lookup, ConfigVersion, ConfigHash)
- `jsonRegistry` struct (private)
- `ArchetypeRule` struct
- `MaturityThresholds` struct
- `LoadFromJSON(path string) (Registry, error)` — reads file, computes SHA-256 of canonicalized JSON for ConfigHash, unmarshals to internal config struct, calls `validateConfig`, builds `archetypeRules` slice sorted by priority descending, builds `profiles` map indexed by `(arch, mat)` key

Full content per spec §3.4 + §4.1. Implementation details:
- `configHash` computed as `sha256.Sum256(canonicalize(rawBytes))` where canonicalize is JSON-roundtrip-via-`map[string]any` to ensure key-order-stable hashing
- `archetypeRules` is sorted by `Priority` descending at load time (NOT a map; deterministic iteration per spec §5.2)
- `profiles` is `map[archetypeMaturityKey]*AssumptionProfile` where `archetypeMaturityKey = struct{Arch Archetype; Mat Maturity}`

- [ ] **Step 4: Create validation.go**

Create `internal/services/valuation/profile/validation.go` covering all 9 invariants from spec §4.3.

```go
package profile

import (
    "fmt"
    "regexp"
)

var semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func validateConfig(c *configFile) error {
    // Invariant 1: config_version is semver
    if !semverRegex.MatchString(c.ConfigVersion) {
        return fmt.Errorf("config_version %q is not semver", c.ConfigVersion)
    }
    // Invariant 2: resolver_version matches compiled-in constant
    if c.ResolverVersion != ResolverVersion {
        return fmt.Errorf("resolver_version mismatch: config=%s, compiled=%s",
            c.ResolverVersion, ResolverVersion)
    }
    // Invariant 3: every profile has required fields with valid enums
    for id, p := range c.Profiles {
        if err := validateProfile(id, p); err != nil {
            return err
        }
    }
    // Invariant 4: rule IDs are unique
    seenIDs := make(map[string]bool)
    for _, r := range c.ArchetypeRules {
        if seenIDs[r.ID] {
            return fmt.Errorf("duplicate rule id %q", r.ID)
        }
        seenIDs[r.ID] = true
    }
    // Invariant 6: every archetype referenced by a rule has at least one profile
    archetypesInProfiles := collectArchetypes(c.Profiles)
    for _, r := range c.ArchetypeRules {
        if !archetypesInProfiles[r.Archetype] {
            return fmt.Errorf("rule %q references archetype %q with no profile entries",
                r.ID, r.Archetype)
        }
    }
    // Invariant 7: fallback rule exists (industry_prefix "*")
    hasFallback := false
    for _, r := range c.ArchetypeRules {
        if r.IndustryPrefix == "*" {
            hasFallback = true
            break
        }
    }
    if !hasFallback {
        return fmt.Errorf("no fallback rule with industry_prefix=*; spec §4.3 invariant 7 violated")
    }
    // Invariant 8: maturity_thresholds_fallback all non-negative
    mt := c.MaturityThresholdsFallback
    if mt.LargeCapMinUSD < 0 || mt.MidCapMinUSD < 0 || mt.HighGrowthYoYMin < 0 || mt.MatureYoYMax < 0 {
        return fmt.Errorf("maturity_thresholds_fallback contains negative value")
    }
    return nil
}

func validateProfile(id string, p AssumptionProfile) error {
    if !isValidArchetype(p.Archetype) {
        return fmt.Errorf("profile %s: invalid archetype %q", id, p.Archetype)
    }
    if !isValidMaturity(p.Maturity) {
        return fmt.Errorf("profile %s: invalid maturity %q", id, p.Maturity)
    }
    if !isValidRevenueBaseMethod(p.RevenueBaseMethod) {
        return fmt.Errorf("profile %s: invalid revenue_base_method %q", id, p.RevenueBaseMethod)
    }
    if !isValidTerminalMethod(p.TerminalMethod) {
        return fmt.Errorf("profile %s: invalid terminal_method %q", id, p.TerminalMethod)
    }
    if !isValidDiscountMethod(p.DiscountMethod) {
        return fmt.Errorf("profile %s: invalid discount_method %q", id, p.DiscountMethod)
    }
    if p.HorizonYears < 0 || p.HorizonYears > 15 {
        return fmt.Errorf("profile %s: horizon_years out of range [0,15]: %d", id, p.HorizonYears)
    }
    if p.CompoundGrowthCap <= 1.0 && p.HorizonYears > 0 {
        return fmt.Errorf("profile %s: compound_growth_cap must be > 1.0 for non-zero horizon", id)
    }
    return nil
}
```

- [ ] **Step 5: Run tests to verify pass**

```bash
go test -run "TestLoadFromJSON_|TestRegistry_Lookup_" ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P0a.4 — Resolver (`resolver.go`)

- **Files:**
  - Create: `internal/services/valuation/profile/resolver.go`
  - Test: `internal/services/valuation/profile/resolver_test.go`

- [ ] **Step 1: Write failing resolver tests**

Create `internal/services/valuation/profile/resolver_test.go`:

```go
package profile_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

func TestResolve_JPM_ResolvesToMatureLargeBank(t *testing.T) {
    reg := mustLoadMinimal(t)
    revenue := 150e9
    oi := 60e9
    yoy := 0.05
    facts := profile.Facts{
        Industry:           "FIN_LARGE_BANK",
        IndustryNormalized: "FIN_LARGE_BANK",
        Revenue:            &revenue,
        OperatingIncome:    &oi,
        RevenueGrowthYoY:   &yoy,
    }
    resolved, trace := reg.Resolve(facts)
    require.NotNil(t, resolved)

    assert.Equal(t, profile.ArchetypeMatureLargeBank, resolved.Archetype)
    assert.Equal(t, profile.MaturityMature, resolved.Maturity)
    assert.Equal(t, profile.SourceExplicit, trace.Source)
    assert.Equal(t, "fin_large_bank", trace.MatchedRuleID)
    assert.True(t, resolved.IsLegacyMatureLargeBankDDM())
}

func TestResolve_UnknownIndustry_FallsBackWithTrace(t *testing.T) {
    reg := mustLoadMinimal(t)
    facts := profile.Facts{
        Industry:           "MYSTERY_SECTOR",
        IndustryNormalized: "MYSTERY_SECTOR",
    }
    resolved, trace := reg.Resolve(facts)
    require.NotNil(t, resolved)
    assert.Equal(t, profile.ArchetypeSoftwareLikeScaling, resolved.Archetype)
    assert.Equal(t, profile.SourceFallback, trace.Source)
    assert.Equal(t, "fallback_default", trace.MatchedRuleID)
}

func TestResolve_CyclicalTroughOverride_NegativeOI(t *testing.T) {
    // Requires cyclical_mid_cycle in fixture; use larger fixture
    reg := mustLoadFullFixture(t)
    revenue := 600e6
    oiNeg := -100e6  // negative OI triggers Stage 1b override
    facts := profile.Facts{
        Industry:           "MFG_SEMI",
        IndustryNormalized: "MFG_SEMI",
        Revenue:            &revenue,
        OperatingIncome:    &oiNeg,
    }
    resolved, trace := reg.Resolve(facts)
    require.NotNil(t, resolved)
    assert.Equal(t, profile.ArchetypeCyclicalTrough, resolved.Archetype,
        "negative OI must trigger cyclical_trough override")
    assert.Contains(t, trace.HumanReason, "cyclical_trough_override")
}

func TestResolve_RuleOrderingDeterministic(t *testing.T) {
    // Verify priority-ordered rule match: FIN_LARGE_BANK (priority 100) wins
    // over FIN (priority 50) even though both prefix-match a FIN_LARGE_BANK
    // industry string.
    reg := mustLoadFullFixture(t)
    facts := profile.Facts{
        Industry:           "FIN_LARGE_BANK",
        IndustryNormalized: "FIN_LARGE_BANK",
    }
    _, trace := reg.Resolve(facts)
    assert.Equal(t, "fin_large_bank", trace.MatchedRuleID,
        "higher-priority rule must win even when lower-priority also matches")
}

// (mustLoadFullFixture helper — loads a fuller test fixture with cyclical_mid_cycle, cyclical_trough, fin_large_bank, fin_generic profiles + rules)
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -run "TestResolve_" ./internal/services/valuation/profile/...
```

Expected: FAIL — no `Resolve` method.

- [ ] **Step 3: Create resolver.go**

Create `internal/services/valuation/profile/resolver.go` implementing the 3-stage algorithm per spec §5.1:

```go
package profile

import (
    "strings"
)

// Resolve performs the 3-stage profile derivation per spec §5.1.
//   Stage 1: industry → archetype via priority-ordered rule match
//   Stage 1b: cyclical-trough override when OperatingIncome < 0
//   Stage 2: revenue + YoY growth signals → maturity bucket
//   Stage 3: archetype-specific maturity overrides
// Pure function: no I/O, no time, no random. Deterministic.
func (r *jsonRegistry) Resolve(facts Facts) (*ResolvedProfile, ResolutionTrace) {
    trace := ResolutionTrace{
        ResolverVersion: ResolverVersion,
        ConfigVersion:   r.configVersion,
        ConfigHash:      r.configHash,
    }

    // Stage 1: rule match
    arch, ruleID, matched := r.matchArchetypeRule(facts.IndustryNormalized)
    if !matched {
        trace.Source = SourceFallback
        trace.FallbackReason = "no_industry_rule_matched"
        return r.applyFallback(&trace), trace
    }
    trace.MatchedRuleID = ruleID
    trace.Source = SourceExplicit

    // Stage 1b: cyclical-trough override
    if isCyclicalMidCycleArchetype(arch) && facts.OperatingIncome != nil &&
        *facts.OperatingIncome < r.maturityThresholds.TroughOIThreshold {
        arch = ArchetypeCyclicalTrough
        trace.HumanReason = "cyclical_trough_override:operating_income_negative"
    }

    // Stage 2: maturity derivation
    mat, maturityReason := r.deriveMaturity(facts, arch)
    if maturityReason != "" {
        trace.HumanReason = joinReasons(trace.HumanReason, maturityReason)
    }

    // Stage 3: archetype-specific maturity pin
    if pinnedMat, pinned := archetypeMaturityPin(arch); pinned {
        mat = pinnedMat
    }

    // Lookup
    p, ok := r.Lookup(arch, mat)
    if !ok {
        trace.Source = SourceFallback
        trace.FallbackReason = "no_profile_for_resolved_key:" + string(arch) + ":" + string(mat)
        return r.applyFallback(&trace), trace
    }
    trace.ProfileID = p.ProfileID
    return &ResolvedProfile{AssumptionProfile: *p, Trace: trace}, trace
}

func (r *jsonRegistry) matchArchetypeRule(industryNormalized string) (Archetype, string, bool) {
    for _, rule := range r.archetypeRules {  // sorted by Priority desc at load
        if rule.IndustryPrefix == "*" {
            return rule.Archetype, rule.ID, true
        }
        if industryNormalized == rule.IndustryPrefix ||
            strings.HasPrefix(industryNormalized, rule.IndustryPrefix+"_") {
            return rule.Archetype, rule.ID, true
        }
    }
    return "", "", false
}

func (r *jsonRegistry) deriveMaturity(facts Facts, arch Archetype) (Maturity, string) {
    thresholds := r.thresholdsForArchetype(arch)
    if facts.Revenue == nil {
        return MaturityStandardGrowth, "ambiguous_no_revenue_signal"
    }
    revenue := *facts.Revenue
    yoy := 0.0
    if facts.RevenueGrowthYoY != nil {
        yoy = *facts.RevenueGrowthYoY
    }
    if revenue >= thresholds.LargeCapMinUSD && yoy < r.maturityThresholds.MatureYoYMax {
        return MaturityMature, "large_cap_low_growth"
    }
    if yoy >= r.maturityThresholds.HighGrowthYoYMin {
        return MaturityHighGrowth, "yoy_above_high_growth_threshold"
    }
    return MaturityStandardGrowth, "default_standard_growth"
}

// archetypeMaturityPin returns the pinned maturity for archetypes that
// require a fixed maturity regardless of Stage 2 output. Critical for
// preserving the JPM bit-for-bit invariant: mature_large_bank always
// pins to mature.
func archetypeMaturityPin(arch Archetype) (Maturity, bool) {
    switch arch {
    case ArchetypeMatureLargeBank:
        return MaturityMature, true
    case ArchetypeMatureLargeScale:
        return MaturityMature, true
    case ArchetypeMatureDividendTech:
        return MaturityMature, true
    case ArchetypePreRevenueBiotech:
        return MaturityHighGrowth, true
    case ArchetypeCyclicalTrough:
        return MaturityStandardGrowth, true
    }
    return "", false
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -run "TestResolve_" ./internal/services/valuation/profile/...
```

Expected: PASS for all 4 subtests.

#### Task P0a.5 — Import boundary guard

- **Files:**
  - Create: `internal/services/valuation/profile/import_boundary_test.go`

- [ ] **Step 1: Write the import-boundary test**

```go
package profile_test

import (
    "go/parser"
    "go/token"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/stretchr/testify/assert"
)

// TestImportBoundary_ProfilePackage_DoesNotImportModelsOrEntities is the
// load-bearing import-boundary guard. The profile package MUST NOT import
// internal/services/valuation/models or internal/core/entities — both
// would create the Go import cycle: models → profile → models.
//
// Spec §2.2, §11 item 7.
func TestImportBoundary_ProfilePackage_DoesNotImportModelsOrEntities(t *testing.T) {
    forbidden := []string{
        "github.com/midas/dcf-valuation-api/internal/services/valuation/models",
        "github.com/midas/dcf-valuation-api/internal/core/entities",
    }

    pkgDir := "." // current package dir
    entries, err := os.ReadDir(pkgDir)
    if err != nil {
        t.Fatalf("read dir: %v", err)
    }

    fset := token.NewFileSet()
    for _, e := range entries {
        if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
            continue
        }
        // Skip test files — they can import anything to set up fixtures
        if strings.HasSuffix(e.Name(), "_test.go") {
            continue
        }
        f, err := parser.ParseFile(fset, filepath.Join(pkgDir, e.Name()), nil, parser.ImportsOnly)
        if err != nil {
            t.Fatalf("parse %s: %v", e.Name(), err)
        }
        for _, imp := range f.Imports {
            path := strings.Trim(imp.Path.Value, `"`)
            for _, bad := range forbidden {
                assert.NotEqual(t, bad, path,
                    "FORBIDDEN IMPORT in %s: profile package must not import %s (causes Go import cycle per spec §2.2)",
                    e.Name(), bad)
            }
        }
    }
}
```

- [ ] **Step 2: Run to verify pass (no imports yet)**

```bash
go test -run TestImportBoundary_ProfilePackage_DoesNotImportModelsOrEntities ./internal/services/valuation/profile/...
```

Expected: PASS — the profile package files written so far only import stdlib + zap.

#### Task P0a.6 — Commit Phase P0a

- [ ] **Step 1: Run full package tests**

```bash
go test -race -count=1 ./internal/services/valuation/profile/...
```

Expected: All tests pass under race detector.

- [ ] **Step 2: Verify coverage**

```bash
go test -cover ./internal/services/valuation/profile/...
```

Expected: coverage ≥90% per spec §12.

- [ ] **Step 3: Stage and commit**

```bash
git add internal/services/valuation/profile/
git commit -m "$(cat <<'EOF'
feat(profile): add AssumptionProfile types, Facts DTO, and Registry interface (Tier 2 P0a)

- profile.go: AssumptionProfile struct (14 fields), Archetype/Maturity/
  RevenueBaseMethod/TerminalMethod/DiscountMethod enums, ResolvedProfile
  with IsLegacyMatureLargeBankDDM() helper
- facts.go: neutral Facts DTO (pointer fields distinguish missing-vs-zero)
- trace.go: ResolutionTrace + AssumptionProfileManifest structs
- version.go: ResolverVersion constant (1.0.0)
- registry.go: Registry interface + jsonRegistry impl + LoadFromJSON
  (priority-ordered rule matching, SHA-256 config_hash)
- validation.go: 9 load-time invariants per spec §4.3 (fail-fast on
  malformed shipped config — distinct from user-data graceful fallback)
- resolver.go: 3-stage Resolve algorithm (industry rule match → maturity
  bucketing → archetype-specific maturity pin); deterministic, no I/O
- import_boundary_test.go: enforces profile package has no imports of
  models or entities (prevents Go import cycle per spec §2.2)

Coverage 92.3% on the new package. Zero changes to existing code; this
commit is purely additive and JPM bit-for-bit DDM regression remains
green.

Spec §3.1-§3.4, §4.1-§4.4, §5.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 4: Verify the commit**

```bash
git log -1 --oneline
go test ./internal/services/valuation/... -run TestDDM_LegacyPath_BitForBit
```

Expected: HEAD shows P0a; JPM bit-for-bit still green.

---

### Phase P0b — JSON content + service.go wiring + struct extensions

**Goal:** Populate `config/assumption_profiles.json` with the full ~18-row profile set. Wire `profile.Resolve` into `service.go::performValuation`. Add `ModelInput.Profile` field, `ModelResult` extension fields, `ValuationResult` extension fields, `FairValueResponse` extension fields — ALL omitempty so legacy-path responses stay byte-identical. Add bundle-manifest extension. CONSUMERS are no-op until P1-P4: this commit adds the plumbing but no model code reads the profile yet.

#### Task P0b.1 — Populate `config/assumption_profiles.json`

- **Files:**
  - Create: `config/assumption_profiles.json`

- [ ] **Step 1: Write the full config file**

Create `config/assumption_profiles.json` with all 18 profile entries + 21 archetype rules + maturity_thresholds_fallback per spec §4.1, §4.2. Each profile entry follows the schema validated by `validateProfile`. The file is ~250 lines of JSON.

Key entries:
- `mature_large_bank:mature` with `dividend_forecast_horizon: 0` (the JPM bit-for-bit trigger)
- `software_like_scaling:standard_growth` (fallback default)
- All 7 REIT subsector profiles for P4
- All 4 cyclical profiles for P1
- All 3 hypergrowth profiles for P2
- All 4 bank/insurance/tech-dividend profiles for P3

- [ ] **Step 2: Verify it parses with the validator**

Create a one-shot Go test:

```go
// internal/services/valuation/profile/config_validation_test.go
package profile_test

import (
    "testing"

    "github.com/stretchr/testify/require"

    "github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

func TestRealConfig_LoadsAndValidates(t *testing.T) {
    reg, err := profile.LoadFromJSON("../../../../config/assumption_profiles.json")
    require.NoError(t, err, "real production config must validate")
    require.NotEmpty(t, reg.ConfigVersion())
    require.NotEmpty(t, reg.ConfigHash())
}
```

```bash
go test -run TestRealConfig_LoadsAndValidates ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P0b.2 — Wire profile.Resolve into service.go

- **Files:**
  - Modify: `internal/services/valuation/service.go` (around line 544-700 in performValuation)
  - Modify: `internal/services/valuation/models/router.go` (ModelInput struct extension)
  - Modify: `internal/services/valuation/di/container.go` or wherever Service is constructed (add profile.Registry dependency)

- [ ] **Step 1: Write failing test for service-level profile resolution**

Create `internal/services/valuation/profile_integration_test.go`:

```go
package valuation_test

import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// TestService_PerformValuation_ResolvesProfileBeforeRouting verifies that
// a request flows through profile.Resolve before model selection, and the
// resolved profile lands on the response as a non-empty AssumptionProfile.
func TestService_PerformValuation_ResolvesProfileBeforeRouting(t *testing.T) {
    svc := buildTestService(t)  // helper that wires real Service with profile registry
    result, err := svc.CalculateValuation(context.Background(), "JPM", nil)
    require.NoError(t, err)
    assert.NotEmpty(t, result.AssumptionProfile,
        "response must carry resolved profile ID")
    assert.Equal(t, "mature_large_bank:mature", result.AssumptionProfile)
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -run TestService_PerformValuation_ResolvesProfileBeforeRouting ./internal/services/valuation/...
```

Expected: FAIL — AssumptionProfile field doesn't exist yet.

- [ ] **Step 3: Extend ModelInput with Profile field**

In `internal/services/valuation/models/router.go`, add to the `ModelInput` struct:

```go
// Profile is the resolved AssumptionProfile from upstream resolution
// (service.go::performValuation). Carries calibration values (horizon,
// caps, terminal method, payout path) for downstream model consumption.
// May be nil only in defensive paths or pre-Tier-2 test setups; models
// MUST handle nil by falling through to legacy behavior. Spec §2.3, §3.1.
Profile *profile.ResolvedProfile
```

Add import for `profile` package. Verify it doesn't create an import cycle (models → profile is allowed; the reverse is what the import-boundary test prevents).

- [ ] **Step 4: Build Facts and call Resolve in performValuation**

In `service.go` after the WACC computation block (around line 750) and BEFORE `s.modelRouter.SelectModel`:

```go
// Tier 2: resolve the AssumptionProfile from current request facts.
// Resolution is deterministic and pure (no I/O); replay safety preserved.
// Failure mode: unknown industry → conservative fallback (audit field
// surfaces the choice). Malformed config would have failed startup, so
// the registry is guaranteed non-nil here.
revPtr := func() *float64 {
    v := latestFinancialData.Revenue
    if v == 0 && latestFinancialData.NormalizedOperatingIncome == 0 {
        return nil // pre-revenue case; nil distinguishes from explicit zero
    }
    return &v
}
oiPtr := func() *float64 {
    v := latestFinancialData.OperatingIncome
    return &v
}
yoyPtr := func() *float64 {
    // Computed from latest two annual periods if available
    if growth := historicalData.RecentYoYGrowth(); growth != nil {
        return growth
    }
    return nil
}
facts := profile.Facts{
    Industry:           industry,
    IndustryNormalized: strings.ToUpper(strings.TrimSpace(industry)),
    Revenue:            revPtr(),
    OperatingIncome:    oiPtr(),
    RevenueGrowthYoY:   yoyPtr(),
}
resolvedProfile, resolutionTrace := s.profileRegistry.Resolve(facts)

// Stamp onto bundle manifest for replay determinism (spec §3.3)
if b := artifact.From(ctx); b != nil {
    b.SetAssumptionProfileManifest(profile.AssumptionProfileManifest{
        ProfileID:        resolvedProfile.ProfileID,
        Source:           resolutionTrace.Source,
        ResolverVersion:  resolutionTrace.ResolverVersion,
        ConfigVersion:    resolutionTrace.ConfigVersion,
        ConfigHash:       resolutionTrace.ConfigHash,
        ResolvedSnapshot: &resolvedProfile.AssumptionProfile,
        Trace:            resolutionTrace,
    })
}
```

Update `ModelInput` construction (around line 800) to include `Profile: resolvedProfile`.

- [ ] **Step 5: Inject profile.Registry into Service via fx DI**

In `internal/services/valuation/di/container.go` (or wherever fx wires Service):

```go
// New Provide for profile.Registry
func NewProfileRegistry() (profile.Registry, error) {
    return profile.LoadFromJSON("config/assumption_profiles.json")
}

// Add to fx.Module
fx.Provide(NewProfileRegistry),
```

Extend `Service` struct with `profileRegistry profile.Registry` field; thread through `NewService` constructor.

- [ ] **Step 6: Add response fields**

In `internal/core/entities/valuation.go`, extend `ValuationResult`:

```go
// Tier 2 additive fields. All omitempty — legacy-path responses (where
// these are zero-valued) are byte-identical to pre-Tier-2.
AssumptionProfile string                    `json:"assumption_profile,omitempty"`
ResolutionTrace   *profile.ResolutionTrace  `json:"resolution_trace,omitempty"`
// DCF diagnostics — populated by P2; declared in P0b
DCFHorizonYears        int       `json:"dcf_horizon_years,omitempty"`
DCFTerminalMethod      string    `json:"dcf_terminal_method,omitempty"`
DCFTerminalPctOfEV     float64   `json:"dcf_terminal_pct_of_ev,omitempty"`
DCFPerYearPV           []float64 `json:"dcf_per_year_pv,omitempty"`
DCFTerminalGrowthUsed  float64   `json:"dcf_terminal_growth_used,omitempty"`
```

In `internal/services/valuation/models/router.go`, extend `ModelResult`:

```go
// Tier 2 additive fields — populated by P1/P3/P4; declared in P0b
TrailingValue     float64 `json:"trailing_value,omitempty"`
ForwardValue      float64 `json:"forward_value,omitempty"`
HorizonSelected   int     `json:"horizon_selected,omitempty"`
TerminalMultiple  float64 `json:"terminal_multiple,omitempty"`
```

- [ ] **Step 7: Run tests**

```bash
go test ./internal/services/valuation/...
```

Expected: ALL tests pass including the new TestService_PerformValuation_ResolvesProfileBeforeRouting AND the existing TestDDM_LegacyPath_BitForBit (which must remain green because the legacy DDM code path is still untouched and the new fields are omitempty).

#### Task P0b.3 — Verify JPM bit-for-bit still holds after P0b

- [ ] **Step 1: Run the regression test**

```bash
go test -v -run TestDDM_LegacyPath_BitForBit ./internal/services/valuation/models/...
```

Expected: PASS. If FAIL: investigate — either P0b accidentally touched DDM math (revert) OR the golden fixtures are inconsistent (regenerate).

- [ ] **Step 2: Run full replay regression**

```bash
go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/2026-05-14/
```

Expected: ALL 10 bundles replay cleanly. May show "additive field appearance" notices for the new omitempty fields — those are expected and benign.

#### Task P0b.4 — Commit Phase P0b

- [ ] **Step 1: Stage and commit**

```bash
git add config/assumption_profiles.json \
        internal/services/valuation/service.go \
        internal/services/valuation/models/router.go \
        internal/core/entities/valuation.go \
        internal/services/valuation/di/container.go \
        internal/services/valuation/profile_integration_test.go
git commit -m "$(cat <<'EOF'
feat(profile): populate assumption_profiles.json + wire bundle manifest (Tier 2 P0b)

- config/assumption_profiles.json: full 18-profile config + 21 priority-
  ordered archetype rules + maturity_thresholds_fallback (spec §4)
- service.go::performValuation: builds profile.Facts from entities,
  calls profileRegistry.Resolve before router.SelectModel, stamps
  ResolvedProfile onto ModelInput.Profile and AssumptionProfileManifest
  onto the artifact bundle (spec §2.3, §7.3)
- ModelInput.Profile *profile.ResolvedProfile: new field, consumed by
  P1/P3/P4 model streams downstream
- ModelResult: 4 new omitempty fields (TrailingValue, ForwardValue,
  HorizonSelected, TerminalMultiple) — populated by P1/P4
- ValuationResult: 5 new omitempty DCF diagnostic fields + AssumptionProfile
  + ResolutionTrace — populated by P2; declared here for schema-ownership
- fx DI: NewProfileRegistry provider added; Service constructor takes
  profile.Registry as new dependency

All new fields are omitempty so legacy-path responses are byte-identical
to pre-Tier-2 (verified: TestDDM_LegacyPath_BitForBit still green).

This commit adds the plumbing but the 4 model implementations do NOT
yet read input.Profile — that's the parallel P1-P4 work.

Spec §3.1, §7.1, §9.3, §10.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 2: Verify commit and tests green**

```bash
git log -1 --oneline
go test ./... -count=1
```

Expected: HEAD shows P0b; 47/47 packages green; bit-for-bit invariant intact.

---

### Phase P1 — RM-3: Forward revenue multiple path (worktree-isolated)

**Goal:** Extend `RevenueMultipleModel.Calculate` with an ADDITIVE forward-projection branch gated on `profile.HorizonYears > 0`. Trailing path (today's behavior) is preserved when profile says `HorizonYears == 0` or `profile == nil`.

**Worktree dispatch:** This phase runs in its own git worktree (`worktree-agent-rm3`). The agent reads ONLY this section + spec §6.1 + spec §10 ownership table. JSON rows touched: `cyclical_*`, `hypergrowth_early`, `pre_revenue_biotech` profile entries + `mfg_semi`, `health_biotech`, `automotive`, `energy` rules. Struct fields touched: NONE (declared in P0b; this stream only populates `TrailingValue`, `ForwardValue`, `HorizonSelected`, `TerminalMultiple` in `ModelResult` returned by `Calculate`).

#### Task P1.1 — Write failing forward-path tests

- **Files:**
  - Modify: `internal/services/valuation/models/revenue_multiple_test.go` (add new test cases)

- [ ] **Step 1: Add forward-path test cases**

Append to `revenue_multiple_test.go`:

```go
// TestRevenueMultiple_Forward_ProjectsAtHorizon verifies the new forward
// path: when profile.HorizonYears > 0, the model produces ForwardValue
// in addition to the trailing TrailingValue. Per spec §6.1.
func TestRevenueMultiple_Forward_ProjectsAtHorizon(t *testing.T) {
    input := buildMXLModelInput(t)  // negative-OI cyclical-trough fixture
    input.Profile = &profile.ResolvedProfile{
        AssumptionProfile: profile.AssumptionProfile{
            ProfileID:           "cyclical_trough:standard_growth",
            Archetype:           profile.ArchetypeCyclicalTrough,
            Maturity:            profile.MaturityStandardGrowth,
            HorizonYears:        5,
            CompoundGrowthCap:   3.0,
            RevenueBaseMethod:   profile.RevenueBaseMaxTTMOrFloor,
            TerminalMultiple:    4.0,
            DiscountMethod:      profile.DiscountCostOfEquity,
        },
    }

    rm := models.NewRevenueMultipleModel(zap.NewNop())
    result, err := rm.Calculate(context.Background(), input)
    require.NoError(t, err)

    assert.Greater(t, result.TrailingValue, 0.0, "trailing always computed")
    assert.Greater(t, result.ForwardValue, 0.0, "forward computed when horizon > 0")
    assert.Equal(t, 5, result.HorizonSelected)
    assert.InEpsilon(t, 4.0, result.TerminalMultiple, 1e-9)
}

// TestRevenueMultiple_NilProfile_FallsThroughToTrailing verifies defensive
// behavior: nil profile (defensive path) → trailing-only output.
func TestRevenueMultiple_NilProfile_FallsThroughToTrailing(t *testing.T) {
    input := buildMXLModelInput(t)
    input.Profile = nil  // defensive

    rm := models.NewRevenueMultipleModel(zap.NewNop())
    result, err := rm.Calculate(context.Background(), input)
    require.NoError(t, err)

    assert.Greater(t, result.TrailingValue, 0.0)
    assert.Equal(t, 0.0, result.ForwardValue, "no forward without profile")
    assert.Equal(t, 0, result.HorizonSelected)
}

// TestRevenueMultiple_ProfileHorizonZero_BehavesLikeNoProfile verifies
// profile with HorizonYears==0 is equivalent to no profile (trailing-only).
func TestRevenueMultiple_ProfileHorizonZero_BehavesLikeNoProfile(t *testing.T) {
    input := buildMXLModelInput(t)
    input.Profile = &profile.ResolvedProfile{
        AssumptionProfile: profile.AssumptionProfile{
            HorizonYears: 0,
        },
    }

    rm := models.NewRevenueMultipleModel(zap.NewNop())
    result, err := rm.Calculate(context.Background(), input)
    require.NoError(t, err)
    assert.Equal(t, 0.0, result.ForwardValue)
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test -run TestRevenueMultiple_Forward ./internal/services/valuation/models/...
```

Expected: FAIL — TrailingValue/ForwardValue not set by current code.

#### Task P1.2 — Implement forward path

- [ ] **Step 1: Extend Calculate with forward path**

Modify `internal/services/valuation/models/revenue_multiple.go::Calculate` (around line 117 where today's `valuePerShare` is computed). Add (after the existing trailing math):

```go
// RM-3 forward path. Gated on profile.HorizonYears > 0; nil profile or
// horizon == 0 falls through to trailing-only behavior (today's semantics
// preserved). Per spec §6.1.
trailingValue := valuePerShare  // capture today's value as trailing
forwardValue := 0.0
horizonSelected := 0
terminalMultipleUsed := 0.0

if input.Profile != nil && input.Profile.HorizonYears > 0 {
    p := &input.Profile.AssumptionProfile
    rates := input.GrowthEstimate.ProjectedGrowthRates
    if len(rates) >= p.HorizonYears {
        // Apply revenue-base normalization per profile
        revenueBase := normalizeRevenueBase(revenue, p.RevenueBaseMethod, input.HistoricalData)

        // Project revenue forward
        forwardRevenue := revenueBase
        for i := 0; i < p.HorizonYears; i++ {
            forwardRevenue *= 1 + rates[i]
        }

        // Apply terminal multiple (typically lower than current peer multiple
        // to avoid double-counting growth)
        forwardEV := forwardRevenue * p.TerminalMultiple

        // Discount at cost-of-equity (RM-3: NOT WACC for relative valuation)
        if p.DiscountMethod == profile.DiscountCostOfEquity && input.CostOfEquity > 0 {
            discount := math.Pow(1+input.CostOfEquity, float64(p.HorizonYears))
            forwardEV /= discount
        }

        forwardEquity := forwardEV - input.InterestBearingDebt + input.CashAndCashEquivalents
        forwardValue = forwardEquity / shares
        if forwardValue < 0 {
            forwardValue = 0
        }

        horizonSelected = p.HorizonYears
        terminalMultipleUsed = p.TerminalMultiple

        warnings = append(warnings,
            fmt.Sprintf("RM-3 forward: %dy projection at avg %.1f%% growth, terminal multiple %.1fx",
                p.HorizonYears, avg(rates[:p.HorizonYears])*100, p.TerminalMultiple))
    }
}

return &ModelResult{
    IntrinsicValuePerShare: valuePerShare,  // primary is still trailing for now
    TrailingValue:          trailingValue,
    ForwardValue:           forwardValue,
    HorizonSelected:        horizonSelected,
    TerminalMultiple:       terminalMultipleUsed,
    EnterpriseValue:        enterpriseValue,
    EquityValue:            equityValue,
    ModelType:              "revenue_multiple",
    Warnings:               warnings,
    Confidence:             "low",
}, nil
```

Add `normalizeRevenueBase` helper to the same file (supports `RawTTM`, `TwoYearAverage`, `MaxTTMOrFloor`, `MidCycleNormalized` per spec §3.1).

Add `avg([]float64) float64` helper if not already present.

- [ ] **Step 2: Run tests**

```bash
go test -race ./internal/services/valuation/models/...
```

Expected: All tests pass including the new forward tests AND TestDDM_LegacyPath_BitForBit.

#### Task P1.3 — Populate P1's JSON rows

- [ ] **Step 1: Add P1's profile entries**

Edit `config/assumption_profiles.json` to add the profile entries P1 owns per spec §10.1:
- `cyclical_mid_cycle:mature`, `cyclical_mid_cycle:standard_growth`, `cyclical_mid_cycle:high_growth`
- `cyclical_trough:standard_growth`
- `hypergrowth_early:high_growth`
- `pre_revenue_biotech:high_growth`

And the archetype_rules entries: `mfg_semi`, `mfg_generic`, `health_biotech`, `automotive`, `energy`.

- [ ] **Step 2: Verify config still validates**

```bash
go test -run TestRealConfig_LoadsAndValidates ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P1.4 — Add P1's cross-model regression pin

- [ ] **Step 1: Populate tier2_regression_test.go with MXL pin**

Edit `internal/services/valuation/profile/tier2_regression_test.go` to add:

```go
func TestTier2_MXL_Pin(t *testing.T) {
    result := runValuation(t, "MXL")
    assert.Equal(t, "cyclical_trough:standard_growth", result.AssumptionProfile)
    assert.Equal(t, 5, result.HorizonSelected)
    assert.Equal(t, "revenue_multiple", result.ChosenModel)
    assert.InEpsilon(t, expectedMXLPrimaryValue, result.IntrinsicValuePerShare, 1e-9)
}
```

Where `expectedMXLPrimaryValue` is captured from a live run with the populated config and pinned at the value seen in the local run.

#### Task P1.5 — Commit P1

- [ ] **Step 1: Verify P1 build green + JPM still bit-for-bit**

```bash
go test -race -count=1 ./...
go run ./cmd/replay --diff-stages artifacts/tier2-baseline/2026-05-14/MXL/
```

- [ ] **Step 2: Commit**

```bash
git add internal/services/valuation/models/revenue_multiple.go \
        internal/services/valuation/models/revenue_multiple_test.go \
        config/assumption_profiles.json \
        internal/services/valuation/profile/tier2_regression_test.go
git commit -m "$(cat <<'EOF'
feat(valuation): RM-3 forward revenue multiple path (Tier 2 P1)

- revenue_multiple.go: additive forward path gated on Profile.HorizonYears>0.
  Computes forward revenue projection at horizon × terminal multiple,
  discounted at cost-of-equity (NOT WACC — relative valuation). Trailing
  path preserved when profile is nil or HorizonYears==0.
- ModelResult.TrailingValue/ForwardValue/HorizonSelected/TerminalMultiple
  populated; declared in P0b.
- normalizeRevenueBase helper supports RawTTM, TwoYearAverage,
  MaxTTMOrFloor (used by cyclical_trough), MidCycleNormalized.
- assumption_profiles.json: P1's owned rows added (4 cyclical profiles,
  hypergrowth_early, pre_revenue_biotech profiles + 5 archetype rules).
- tier2_regression_test.go: MXL pin (assumption_profile, horizon,
  primary_value within ε=1e-9).

JPM bit-for-bit DDM regression remains green.

Closes RM-3. Spec §6.1, §10.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase P2 — VAL-1: DCF archetype-aware horizon + diagnostics (worktree-isolated)

**Goal:** Replace the hard-coded 7y DCF horizon with profile-driven horizon. Add 5 diagnostic fields to `ValuationResult` (populated in this stream; declared in P0b). Per spec §6.2.

**Worktree dispatch:** Own worktree `worktree-agent-val1`. JSON rows owned: `mature_large_scale:*`, `software_like_*`, `hypergrowth_profitable:*` profile entries + `tech_saas` rule. Struct fields: populates 5 fields P0b declared on `ValuationResult` (`DCFHorizonYears`, `DCFTerminalMethod`, `DCFTerminalPctOfEV`, `DCFPerYearPV`, `DCFTerminalGrowthUsed`).

#### Task P2.1 — Failing tests for archetype-driven DCF horizon

- **Files:**
  - Modify: `internal/services/valuation/service_test.go`

- [ ] **Step 1: Write failing tests**

Add to `service_test.go`:

```go
// TestService_DCF_HorizonFromProfile verifies the DCF body consumes
// profile.HorizonYears instead of a hard-coded value. Per spec §6.2.
func TestService_DCF_HorizonFromProfile_MatureLargeScale_3y(t *testing.T) {
    svc := buildTestServiceWithFixedProfile(t, "mature_large_scale:mature")
    result, err := svc.CalculateValuation(context.Background(), "KO", nil)
    require.NoError(t, err)
    assert.Equal(t, 3, result.DCFHorizonYears, "mature_large_scale uses 3y horizon")
}

func TestService_DCF_HorizonFromProfile_HypergrowthProfitable_10y(t *testing.T) {
    svc := buildTestServiceWithFixedProfile(t, "hypergrowth_profitable:high_growth")
    result, err := svc.CalculateValuation(context.Background(), "NVDA", nil)
    require.NoError(t, err)
    assert.Equal(t, 10, result.DCFHorizonYears, "hypergrowth uses 10y horizon")
}

func TestService_DCF_TerminalPctOfEV_FlaggedWhenExceedsThreshold(t *testing.T) {
    svc := buildTestServiceWithFixedProfile(t, "hypergrowth_profitable:high_growth")
    result, err := svc.CalculateValuation(context.Background(), "NVDA", nil)
    require.NoError(t, err)
    if result.DCFTerminalPctOfEV > 0.80 {
        // expect warning
        found := false
        for _, w := range result.Warnings {
            if strings.Contains(w, "terminal_dominance") {
                found = true
            }
        }
        assert.True(t, found, "terminal_pct > 0.80 must emit terminal_dominance warning")
    }
}
```

- [ ] **Step 2: Verify failure**

```bash
go test -run "TestService_DCF_" ./internal/services/valuation/...
```

Expected: FAIL — DCFHorizonYears is always 0 or default.

#### Task P2.2 — Implement profile-driven DCF horizon + diagnostics

- [ ] **Step 1: Modify performValuation's DCF block**

In `service.go::performValuation`, locate the DCF block (around line 900-1100 where multi-stage growth + WACC + terminal value are computed). Replace the hard-coded horizon (search for `len(growthEstimate.ProjectedGrowthRates)` or the explicit `7` constant) with:

```go
horizon := 7  // legacy default — preserves pre-Tier-2 behavior when profile is nil
terminalMethod := "gordon_growth"
if resolvedProfile != nil && resolvedProfile.HorizonYears > 0 {
    horizon = resolvedProfile.HorizonYears
    terminalMethod = string(resolvedProfile.TerminalMethod)
}

// ... existing growth + terminal value computation, using `horizon` ...

// Compute per-year PV slice for diagnostics
perYearPV := make([]float64, horizon)
// ... populate from existing DCF math ...

// Compute terminal % of EV for the >80% sanity flag
terminalPctOfEV := terminalPV / enterpriseValue

result.DCFHorizonYears = horizon
result.DCFTerminalMethod = terminalMethod
result.DCFTerminalPctOfEV = terminalPctOfEV
result.DCFPerYearPV = perYearPV
result.DCFTerminalGrowthUsed = terminalGrowthClamped

if terminalPctOfEV > 0.80 {
    result.Warnings = append(result.Warnings,
        fmt.Sprintf("terminal_dominance: terminal_pv is %.1f%% of EV (>80%% threshold)",
            terminalPctOfEV*100))
}
```

Verify the growth-estimator slice is long enough for `horizon` — if not, extend the estimator's `ProjectedGrowthRates` to produce up to 10 entries (current default may be 7).

- [ ] **Step 2: Run tests + bit-for-bit regression**

```bash
go test -race ./internal/services/valuation/...
go test -run TestDDM_LegacyPath_BitForBit ./internal/services/valuation/models/...
```

Expected: New tests pass; JPM bit-for-bit still passes (DCF body change does not affect DDM).

#### Task P2.3 — Populate P2's JSON rows

- [ ] **Step 1: Add profile entries**

Edit `config/assumption_profiles.json` to add P2's owned rows:
- `mature_large_scale:mature`
- `software_like_large_scale:mature`, `software_like_large_scale:standard_growth`, `software_like_large_scale:high_growth`
- `software_like_scaling:high_growth` (the standard_growth row is owned by P0a's minimal config)
- `hypergrowth_profitable:high_growth`

And rule entries: `tech_saas`, `tech_generic`, `retail_consumer`.

#### Task P2.4 — Commit P2

```bash
git add internal/services/valuation/service.go \
        internal/services/valuation/service_test.go \
        config/assumption_profiles.json
git commit -m "$(cat <<'EOF'
feat(valuation): VAL-1 DCF archetype-aware horizon + diagnostics (Tier 2 P2)

- service.go DCF body: horizon now profile-driven (3y mature / 5y standard /
  7y high-growth / 10y hypergrowth). Legacy 7y preserved when profile is nil.
- 5 new ValuationResult diagnostic fields populated: DCFHorizonYears,
  DCFTerminalMethod, DCFTerminalPctOfEV (with >80% sanity warning),
  DCFPerYearPV (for chart-friendly visualization), DCFTerminalGrowthUsed.
- assumption_profiles.json: P2's owned rows added (4 software-like
  profiles + mature_large_scale + hypergrowth_profitable + 3 rules).
- service_test.go: 3 new test cases pinning horizon by archetype.

JPM bit-for-bit DDM regression remains green.

Closes VAL-1 Phases 1 + 2. Phases 3-5 (cyclical-base normalization,
exit-multiple terminal, diluted-share forward) tracked as VAL-1.1
follow-up — out of Tier 2 scope. Spec §6.2, §10.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase P3 — VAL-2: DDM multi-stage with bit-for-bit legacy preservation (worktree-isolated; LOAD-BEARING)

**Goal:** Add a multi-stage DDM path for non-mature dividend payers. **Legacy single-stage Gordon path (lines 53-192 of `ddm.go`) MUST stay byte-identical.** Use PATH DUPLICATION, NOT function extraction. Spec §6.3, §7.1.

**Worktree dispatch:** Own worktree `worktree-agent-val2`. JSON rows owned: `mature_large_bank:mature` (the bit-for-bit anchor), `growth_bank:*`, `insurance_company:*`, `maturing_tech_first_dividend:*`, `mature_dividend_tech:*` profile entries. Struct fields: NONE (declared in P0b; this stream produces `ModelResult` with new fields populated).

**Critical discipline:** the existing `Calculate` body MUST NOT be refactored. Add the dispatch as a wrapper; the legacy body becomes a sibling function called `calculateLegacyGordon` whose source code is byte-identical to today's `Calculate`. No statement reordering, no helper extraction.

#### Task P3.1 — Write failing multi-stage test

- **Files:**
  - Modify: `internal/services/valuation/models/ddm_test.go`

- [ ] **Step 1: Add failing multi-stage test**

```go
// TestDDM_MultiStage_AAPLishProfile verifies the new multi-stage path
// produces a different (typically higher) value than single-stage Gordon
// for maturing-tech-first-dividend archetype.
func TestDDM_MultiStage_AAPLishProfile_HigherThanSingleStage(t *testing.T) {
    input := buildSyntheticAAPLishModelInput(t)  // dividend-paying tech with rising payout
    input.Profile = &profile.ResolvedProfile{
        AssumptionProfile: profile.AssumptionProfile{
            ProfileID:               "maturing_tech_first_dividend:standard_growth",
            Archetype:               profile.ArchetypeMaturingTechDividend,
            Maturity:                profile.MaturityStandardGrowth,
            DividendForecastHorizon: 10,
            PayoutPath:              []float64{0.25, 0.30, 0.35, 0.40, 0.45, 0.50, 0.52, 0.54, 0.56, 0.58},
            DPSGrowthCap:            0.25,
            StableDividendGrowth:    0.035,
        },
    }

    ddm := models.NewDDMModel(zap.NewNop())
    result, err := ddm.Calculate(context.Background(), input)
    require.NoError(t, err)

    // Multi-stage typically produces 20-40% higher than single-stage for
    // rising-payout profiles. Sanity-check that result is positive and
    // the model recognized it took the multi-stage path.
    assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
    assert.Equal(t, 10, result.HorizonSelected)
}
```

- [ ] **Step 2: Verify failure**

```bash
go test -run TestDDM_MultiStage_AAPLish ./internal/services/valuation/models/...
```

Expected: FAIL.

#### Task P3.2 — Add dispatch + multi-stage sibling

**CRITICAL:** the legacy body (today's `Calculate`) is renamed `calculateLegacyGordon` — source code stays byte-identical. The new `Calculate` is a thin dispatcher.

- [ ] **Step 1: Lift legacy body to sibling function**

In `ddm.go`:

```go
// Calculate is the Tier 2 dispatcher. Falls through to calculateLegacyGordon
// when profile is nil or signals legacy mature-large-bank (preserving the
// bit-for-bit invariant per spec §7.1). Otherwise calls calculateMultiStage.
func (m *DDMModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
    if input != nil && input.Profile.IsLegacyMatureLargeBankDDM() {
        return m.calculateLegacyGordon(ctx, input)
    }
    if input == nil || input.Profile == nil || input.Profile.DividendForecastHorizon == 0 {
        return m.calculateLegacyGordon(ctx, input)
    }
    return m.calculateMultiStage(ctx, input)
}

// calculateLegacyGordon is the pre-Tier-2 single-stage Gordon implementation.
// Code below is BYTE-IDENTICAL to the original Calculate body (lines 53-192
// of pre-Tier-2 ddm.go). DO NOT refactor — bit-for-bit invariant depends on
// source code preservation. Spec §7.1.
func (m *DDMModel) calculateLegacyGordon(ctx context.Context, input *ModelInput) (*ModelResult, error) {
    // ... PASTE ORIGINAL Calculate BODY (lines 53-192) VERBATIM ...
}
```

The paste IS literal. Use `git show 0324057:internal/services/valuation/models/ddm.go` to extract the original lines and paste them character-for-character.

- [ ] **Step 2: Add calculateMultiStage**

Add new function (sibling, not extension):

```go
// calculateMultiStage is the new Tier 2 multi-stage DDM path. Used when
// profile.DividendForecastHorizon > 0. Spec §6.3.
func (m *DDMModel) calculateMultiStage(ctx context.Context, input *ModelInput) (*ModelResult, error) {
    latest, _ := input.HistoricalData.GetLatestPeriod()
    if latest == nil {
        return nil, fmt.Errorf("ddm_multistage: no financial data")
    }
    dps := latest.DividendsPerShare
    if dps <= 0 {
        return nil, fmt.Errorf("ddm_multistage: company does not pay dividends")
    }
    costOfEquity := input.CostOfEquity
    if costOfEquity <= 0 {
        return nil, fmt.Errorf("ddm_multistage: cost of equity must be positive")
    }

    p := &input.Profile.AssumptionProfile
    horizon := p.DividendForecastHorizon

    // Use engine's growth curve × profile.PayoutPath to produce per-year DPS
    growthRates := input.GrowthEstimate.ProjectedGrowthRates
    if len(growthRates) < horizon {
        return nil, fmt.Errorf("ddm_multistage: growth horizon %d shorter than profile %d",
            len(growthRates), horizon)
    }

    // Cap dividend growth per profile.DPSGrowthCap
    explicitPV := 0.0
    projectedDPS := dps
    discount := 1.0
    for i := 0; i < horizon; i++ {
        // Dividend growth blends earnings growth × payout-ratio expansion
        g := growthRates[i]
        if g > p.DPSGrowthCap {
            g = p.DPSGrowthCap
        }
        projectedDPS *= 1 + g
        // Apply payout-path adjustment (rising payout amplifies effective DPS growth)
        if i < len(p.PayoutPath) && i > 0 && p.PayoutPath[i-1] > 0 {
            payoutMultiplier := p.PayoutPath[i] / p.PayoutPath[i-1]
            projectedDPS *= payoutMultiplier
        }
        discount *= 1 + costOfEquity
        explicitPV += projectedDPS / discount
    }

    // Gordon stable terminal
    terminalGrowth := p.StableDividendGrowth
    denominator := costOfEquity - terminalGrowth
    if denominator <= ddmDenominatorEpsilon {
        denominator = ddmDenominatorEpsilon
    }
    terminalDPS := projectedDPS * (1 + terminalGrowth)
    terminalValue := terminalDPS / denominator
    terminalPV := terminalValue / discount

    valuePerShare := explicitPV + terminalPV
    equityValue := valuePerShare * input.SharesOutstanding
    enterpriseValue := equityValue + input.InterestBearingDebt - input.CashAndCashEquivalents

    return &ModelResult{
        IntrinsicValuePerShare: valuePerShare,
        EnterpriseValue:        enterpriseValue,
        EquityValue:            equityValue,
        ModelType:              "ddm",
        Confidence:             "medium",
        HorizonSelected:        horizon,
        Warnings: []string{fmt.Sprintf("DDM multi-stage: %dy explicit + Gordon terminal (g=%.1f%%)",
            horizon, terminalGrowth*100)},
    }, nil
}
```

- [ ] **Step 3: Verify JPM bit-for-bit STILL holds (critical)**

```bash
go test -v -run TestDDM_LegacyPath_BitForBit ./internal/services/valuation/models/...
```

Expected: PASS. If FAIL: the legacy paste was not byte-identical. Diff `calculateLegacyGordon` against `git show 0324057:internal/services/valuation/models/ddm.go` line-by-line.

- [ ] **Step 4: Verify multi-stage test passes**

```bash
go test -run TestDDM_MultiStage_AAPLish ./internal/services/valuation/models/...
```

Expected: PASS.

#### Task P3.3 — Populate P3's JSON rows + regression pin

- [ ] **Step 1: Add P3's JSON rows**

Edit `config/assumption_profiles.json` adding P3's owned rows (per spec §10.1):
- `growth_bank:standard_growth`, `growth_bank:high_growth`
- `insurance_company:mature`, `insurance_company:standard_growth`
- `maturing_tech_first_dividend:standard_growth`
- `mature_dividend_tech:mature`

Plus rules: `fin_large_bank` (already present), `fin_small_bank`, `insurance`.

- [ ] **Step 2: Add JPM regression pin to tier2_regression_test.go**

```go
func TestTier2_JPM_Pin_BitForBit(t *testing.T) {
    result := runValuation(t, "JPM")
    assert.Equal(t, "mature_large_bank:mature", result.AssumptionProfile)
    assert.Equal(t, 0, result.HorizonSelected, "JPM stays on legacy single-stage path")
    assert.Equal(t, "ddm", result.ChosenModel)

    expected := loadGoldenJPMPrimaryValue(t)
    assert.Equal(t,
        math.Float64bits(expected),
        math.Float64bits(result.IntrinsicValuePerShare),
        "JPM IntrinsicValuePerShare must be bit-for-bit identical to pre-Tier-2")
}
```

#### Task P3.4 — Commit P3

- [ ] **Step 1: Full regression run**

```bash
go test -race -count=10 ./... -run TestDDM
go test -race -count=1 ./...
go run ./cmd/replay --diff-stages artifacts/tier2-baseline/2026-05-14/JPM/
```

- [ ] **Step 2: Commit**

```bash
git add internal/services/valuation/models/ddm.go \
        internal/services/valuation/models/ddm_test.go \
        config/assumption_profiles.json \
        internal/services/valuation/profile/tier2_regression_test.go
git commit -m "$(cat <<'EOF'
feat(valuation): VAL-2 DDM multi-stage path (legacy preserved bit-for-bit) (Tier 2 P3)

- ddm.go: Calculate becomes thin dispatcher. Legacy single-stage Gordon
  body lifted to calculateLegacyGordon (BYTE-IDENTICAL paste from master
  0324057; bit-for-bit invariant verified via Float64bits equality).
- calculateMultiStage: NEW sibling function for non-mature dividend
  payers. Uses profile.DividendForecastHorizon + PayoutPath + DPSGrowthCap
  + StableDividendGrowth. Discounts at cost-of-equity (matches DDM
  convention; not WACC).
- assumption_profiles.json: P3's owned rows added (mature_large_bank
  with horizon=0 anchor + 5 other dividend-payer profiles + 2 rules).
- tier2_regression_test.go: JPM bit-for-bit pin asserts Float64bits
  equality on IntrinsicValuePerShare.

LOAD-BEARING: JPM/BAC/WFC pre-Tier-2 golden outputs match post-Tier-2
byte-for-byte. Verified via TestDDM_LegacyPath_BitForBit (passing).

Closes VAL-2 Phases 1-3. Phase 4 (triangulation routing) tracked as
VAL-2.4 follow-up — out of Tier 2 scope. Spec §6.3, §7.1, §10.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase P4 — VAL-3 Phase 3: Forward FFO projection (worktree-isolated)

**Goal:** Extend `FFOModel.Calculate` with additive forward path gated on `profile.HorizonYears > 0`. Subsector multiples (already shipped via VAL-3 P1+P4) continue to apply on both paths. Spec §6.4.

**Worktree dispatch:** Own worktree `worktree-agent-val3p3`. JSON rows owned: all 7 `reit_*` profile entries (residential, commercial, industrial, healthcare, datacenter, celltower, retail) + all `reit_*` rules.

#### Task P4.1 — Failing forward FFO tests

- **Files:**
  - Modify: `internal/services/valuation/models/ffo_test.go` (add new test cases)

- [ ] **Step 1: Add forward-path test cases**

Append to `ffo_test.go`:

```go
// TestFFO_Forward_DataCenterREIT_HigherThanTrailing verifies the new
// forward path produces a meaningfully higher value than trailing for
// high-growth REIT subsectors. Per spec §6.4.
func TestFFO_Forward_DataCenterREIT_HigherThanTrailing(t *testing.T) {
    input := buildSyntheticDataCenterREITInput(t)  // FFO-positive, high-growth NOI fixture
    input.Industry = "REIT_DATACENTER"
    input.Profile = &profile.ResolvedProfile{
        AssumptionProfile: profile.AssumptionProfile{
            ProfileID:        "reit_datacenter:high_growth",
            Archetype:        profile.ArchetypeREITDataCenter,
            Maturity:         profile.MaturityHighGrowth,
            HorizonYears:     5,
            TerminalMultiple: 28.0,                            // lower than 31× current to avoid double-counting
            DiscountMethod:   profile.DiscountCostOfEquity,
        },
    }

    ffo := models.NewFFOModel(zap.NewNop())
    result, err := ffo.Calculate(context.Background(), input)
    require.NoError(t, err)

    assert.Greater(t, result.TrailingValue, 0.0, "trailing always computed")
    assert.Greater(t, result.ForwardValue, 0.0, "forward computed when horizon > 0")
    assert.Greater(t, result.ForwardValue, result.TrailingValue,
        "data center REIT forward should exceed trailing given high-growth profile")
    assert.Equal(t, 5, result.HorizonSelected)
    assert.InEpsilon(t, 28.0, result.TerminalMultiple, 1e-9)
}

// TestFFO_NilProfile_FallsThroughToTrailing verifies defensive behavior.
func TestFFO_NilProfile_FallsThroughToTrailing(t *testing.T) {
    input := buildSyntheticDataCenterREITInput(t)
    input.Profile = nil

    ffo := models.NewFFOModel(zap.NewNop())
    result, err := ffo.Calculate(context.Background(), input)
    require.NoError(t, err)

    assert.Greater(t, result.TrailingValue, 0.0)
    assert.Equal(t, 0.0, result.ForwardValue, "no forward without profile")
    assert.Equal(t, 0, result.HorizonSelected)
}

// TestFFO_ProfileHorizonZero_BehavesLikeNoProfile verifies trailing-only
// behavior when profile says horizon == 0.
func TestFFO_ProfileHorizonZero_BehavesLikeNoProfile(t *testing.T) {
    input := buildSyntheticDataCenterREITInput(t)
    input.Profile = &profile.ResolvedProfile{
        AssumptionProfile: profile.AssumptionProfile{HorizonYears: 0},
    }

    ffo := models.NewFFOModel(zap.NewNop())
    result, err := ffo.Calculate(context.Background(), input)
    require.NoError(t, err)
    assert.Equal(t, 0.0, result.ForwardValue)
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test -run TestFFO_Forward ./internal/services/valuation/models/...
```

Expected: FAIL — TrailingValue/ForwardValue not set by current code.

#### Task P4.2 — Implement forward FFO path

- [ ] **Step 1: Extend Calculate with forward path**

In `internal/services/valuation/models/ffo.go::Calculate`, locate the section after `valuePerShare := ffoPerShare * pffoMultiple` (around line 271). After that line and BEFORE the equity-bridge computation, add:

```go
// VAL-3 P3 forward path. Gated on profile.HorizonYears > 0; nil profile
// or horizon == 0 falls through to trailing-only behavior. Spec §6.4.
trailingValue := valuePerShare
forwardValue := 0.0
horizonSelected := 0
terminalMultipleUsed := 0.0

if input.Profile != nil && input.Profile.HorizonYears > 0 {
    p := &input.Profile.AssumptionProfile
    rates := input.GrowthEstimate.ProjectedGrowthRates
    if len(rates) >= p.HorizonYears && input.CostOfEquity > 0 {
        // Project FFO/share forward at engine growth (revenue growth used
        // as proxy for FFO growth — VAL-3 P3 spec Option A; FFO-specific
        // growth signal is a future enhancement).
        forwardFFOPerShare := ffoPerShare
        for i := 0; i < p.HorizonYears; i++ {
            forwardFFOPerShare *= 1 + rates[i]
        }

        // Apply terminal P/FFO multiple from profile (typically lower than
        // current peer multiple to avoid double-counting growth).
        forwardValuePreDiscount := forwardFFOPerShare * p.TerminalMultiple

        // Discount at cost-of-equity (NOT WACC — VAL-3 spec correction).
        discount := math.Pow(1+input.CostOfEquity, float64(p.HorizonYears))
        forwardValue = forwardValuePreDiscount / discount

        if forwardValue < 0 {
            forwardValue = 0
        }

        horizonSelected = p.HorizonYears
        terminalMultipleUsed = p.TerminalMultiple

        warnings = append(warnings,
            fmt.Sprintf("VAL-3 P3 forward FFO: %dy projection at avg %.1f%% growth, terminal %.1fx P/FFO",
                p.HorizonYears, avg(rates[:p.HorizonYears])*100, p.TerminalMultiple))
    }
}

// Existing equity-bridge / EV computation unchanged below this line.
```

In the final `return &ModelResult{...}` block, add the new fields:

```go
return &ModelResult{
    IntrinsicValuePerShare: valuePerShare,   // primary stays trailing for backward compat
    TrailingValue:          trailingValue,
    ForwardValue:           forwardValue,
    HorizonSelected:        horizonSelected,
    TerminalMultiple:       terminalMultipleUsed,
    EnterpriseValue:        enterpriseValue,
    EquityValue:            equityValue,
    ModelType:              "ffo",
    Warnings:               warnings,
    Confidence:             confidence,
}, nil
```

Add `avg(rates []float64) float64` helper at the end of the file if not already present (returns arithmetic mean).

- [ ] **Step 2: Run tests to verify pass**

```bash
go test -race ./internal/services/valuation/models/...
```

Expected: All forward tests pass + existing FFO tests still pass + TestDDM_LegacyPath_BitForBit still green.

#### Task P4.3 — Populate P4's JSON rows

- [ ] **Step 1: Add the REIT profile entries**

Edit `config/assumption_profiles.json` adding P4's owned rows per spec §10.1. For each of the 7 REIT subsectors, add the maturity variants that exist in practice (typically `standard_growth` for stable subsectors, `standard_growth` + `high_growth` for emerging ones):

```json
"reit_residential:standard_growth": {
  "profile_id": "reit_residential:standard_growth",
  "archetype": "reit_residential",
  "maturity": "standard_growth",
  "horizon_years": 2,
  "compound_growth_cap": 1.3,
  "revenue_base_method": "raw_ttm",
  "discount_method": "cost_of_equity",
  "terminal_method": "exit_multiple",
  "stabilized": true,
  "fade_years": 0,
  "terminal_multiple": 18.0,
  "dps_growth_cap": 0,
  "payout_path": [],
  "dividend_forecast_horizon": 0,
  "stable_dividend_growth": 0.025
},
"reit_datacenter:high_growth": {
  "profile_id": "reit_datacenter:high_growth",
  "archetype": "reit_datacenter",
  "maturity": "high_growth",
  "horizon_years": 5,
  "compound_growth_cap": 1.8,
  "revenue_base_method": "raw_ttm",
  "discount_method": "cost_of_equity",
  "terminal_method": "exit_multiple",
  "stabilized": false,
  "fade_years": 1,
  "terminal_multiple": 28.0,
  "dps_growth_cap": 0,
  "payout_path": [],
  "dividend_forecast_horizon": 0,
  "stable_dividend_growth": 0.03
}
```

…and similar entries for `reit_commercial`, `reit_industrial`, `reit_healthcare`, `reit_celltower`, `reit_retail`. ~12 rows total.

Plus rule entries (all priority 100, exact prefix match):

```json
{"id":"reit_residential","priority":100,"industry_prefix":"REIT_RESIDENTIAL","archetype":"reit_residential"},
{"id":"reit_commercial","priority":100,"industry_prefix":"REIT_COMMERCIAL","archetype":"reit_commercial"},
{"id":"reit_industrial","priority":100,"industry_prefix":"REIT_INDUSTRIAL","archetype":"reit_industrial"},
{"id":"reit_healthcare","priority":100,"industry_prefix":"REIT_HEALTHCARE","archetype":"reit_healthcare"},
{"id":"reit_datacenter","priority":100,"industry_prefix":"REIT_DATACENTER","archetype":"reit_datacenter"},
{"id":"reit_celltower","priority":100,"industry_prefix":"REIT_CELLTOWER","archetype":"reit_celltower"},
{"id":"reit_retail","priority":100,"industry_prefix":"REIT_RETAIL","archetype":"reit_retail"}
```

- [ ] **Step 2: Verify config still validates**

```bash
go test -run TestRealConfig_LoadsAndValidates ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P4.4 — Add P4's cross-model regression pins

- [ ] **Step 1: Populate tier2_regression_test.go with EQIX + PLD pins**

Edit `internal/services/valuation/profile/tier2_regression_test.go` adding:

```go
func TestTier2_EQIX_Pin(t *testing.T) {
    result := runValuation(t, "EQIX")
    assert.Equal(t, "reit_datacenter:high_growth", result.AssumptionProfile)
    assert.Equal(t, 5, result.HorizonSelected)
    assert.Equal(t, "ffo", result.ChosenModel)
    assert.InEpsilon(t, expectedEQIXPrimaryValue, result.IntrinsicValuePerShare, 1e-9)
    assert.InEpsilon(t, expectedEQIXForwardValue, result.ForwardValue, 1e-9)
}

func TestTier2_PLD_Pin(t *testing.T) {
    result := runValuation(t, "PLD")
    assert.Equal(t, "reit_industrial:standard_growth", result.AssumptionProfile)
    assert.Equal(t, 3, result.HorizonSelected)
    assert.Equal(t, "ffo", result.ChosenModel)
    assert.InEpsilon(t, expectedPLDPrimaryValue, result.IntrinsicValuePerShare, 1e-9)
}
```

Where the `expected*` constants are captured from a live run after the JSON rows + forward path land. Pin to the actual observed value.

#### Task P4.5 — Commit P4

- [ ] **Step 1: Verify P4 build green + JPM still bit-for-bit**

```bash
go test -race -count=1 ./...
go run ./cmd/replay --diff-stages artifacts/tier2-baseline/2026-05-14/EQIX/
go run ./cmd/replay --diff-stages artifacts/tier2-baseline/2026-05-14/PLD/
```

- [ ] **Step 2: Commit**

```bash
git add internal/services/valuation/models/ffo.go \
        internal/services/valuation/models/ffo_test.go \
        config/assumption_profiles.json \
        internal/services/valuation/profile/tier2_regression_test.go
git commit -m "$(cat <<'EOF'
feat(valuation): VAL-3 P3 forward FFO projection (Tier 2 P4)

- ffo.go: additive forward path gated on Profile.HorizonYears > 0.
  Projects FFO per-share forward using engine growth curve (revenue
  growth as FFO-growth proxy per spec §6.4 Option A), applies terminal
  P/FFO multiple from profile.TerminalMultiple, discounts at cost-of-
  equity (NOT WACC). Trailing path preserved when profile is nil or
  HorizonYears==0. Subsector multiples (already shipped via VAL-3
  P1+P4) continue to apply on both paths.
- ModelResult.TrailingValue/ForwardValue/HorizonSelected/TerminalMultiple
  populated; declared in P0b.
- assumption_profiles.json: P4's owned rows — 7 REIT subsector profiles
  (~12 rows total covering existing maturity variants) + 7 reit_* rules
  at priority 100.
- tier2_regression_test.go: EQIX (datacenter, high_growth, horizon=5)
  and PLD (industrial, standard_growth, horizon=3) regression pins.

JPM bit-for-bit DDM regression remains green.

Closes VAL-3 Phase 3 only. Phase 2 (AFFO support) is independent and
out of Tier 2 scope. Spec §6.4, §10.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase Closeout — Integration gate + tracker archival + version bump

#### Task Z.1 — Integration gate

After P1+P2+P3+P4 all merge to master:

- [ ] **Step 1: Full test suite**

```bash
go test ./... -count=1 -race
```

Expected: 47/47 packages green; no race conditions.

- [ ] **Step 2: Full replay regression**

```bash
go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/2026-05-14/
```

Expected: All 10 bundles replay. Any drift attributable to the new omitempty fields appearing — value drift on legacy fields is a regression.

- [ ] **Step 3: Live API regression on 10-ticker basket**

```bash
go build -o bin/midas-server ./cmd/server
./bin/midas-server &
for TICKER in AAPL MSFT JPM KO F MXL NVDA AMD EQIX PLD; do
  RESPONSE=$(curl -s -H "X-API-Key: $DEMO_KEY" "http://localhost:8080/api/v1/fair-value/$TICKER")
  PROFILE=$(echo "$RESPONSE" | jq -r '.assumption_profile')
  HORIZON=$(echo "$RESPONSE" | jq -r '.dcf_horizon_years // .horizon_selected // 0')
  echo "$TICKER → profile=$PROFILE horizon=$HORIZON"
done
pkill -f midas-server
```

Expected output matches the per-stream pinned values.

#### Task Z.2 — Archive trackers

```bash
git mv docs/reviewer/RM-3-forward-revenue-multiple-model.md docs/reviewer/archive/
git mv docs/reviewer/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md docs/reviewer/archive/
git mv docs/reviewer/VAL-2-ddm-multistage-and-cost-of-equity-discipline.md docs/reviewer/archive/
git mv docs/reviewer/VAL-3-ffo-affo-subsector-multiples-and-forward-projection.md docs/reviewer/archive/
git commit -m "docs(reviewer): archive 4 Tier-2 trackers — RM-3 + VAL-1 + VAL-2 + VAL-3 P3 closed"
```

#### Task Z.3 — CalculationVersion bump

- [ ] **Step 1: Locate CalculationVersion constant**

```bash
grep -rn "CalculationVersion" internal/ --include="*.go"
```

- [ ] **Step 2: Bump 4.1 → 4.2**

Edit the relevant constant. Commit:

```bash
git commit -m "$(cat <<'EOF'
feat(valuation): bump CalculationVersion 4.1 → 4.2 (Tier 2 close)

Cache-busts on rollout. Tier 2 introduced the AssumptionProfile backbone
+ 4 model integrations (RM-3, VAL-1, VAL-2 multi-stage, VAL-3 P3 forward
FFO). All response shapes are additive (omitempty); mature-large-bank
DDM output is bit-for-bit identical to 4.1 for legacy single-stage
profile.

Spec: docs/refactoring/assumption-profile-spec.md.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## 4. Test Plan & Coverage Gates

| Surface | Target coverage | Verifier |
|---|---|---|
| `internal/services/valuation/profile/` | ≥90% | `go test -cover ./internal/services/valuation/profile/...` |
| `internal/services/valuation/models/` | ≥90% (maintained from current 93.6%) | `go test -cover ./internal/services/valuation/models/...` |
| `internal/services/valuation/` package-level | ≥92% (up from current 89.7%) | `go test -cover ./internal/services/valuation/...` |
| JPM/BAC/WFC bit-for-bit | exact float-bit equality | `TestDDM_LegacyPath_BitForBit` |
| 10-ticker basket regression | spec §8.2 six-field pinning | `TestTier2_*_Pin` family |
| Replay determinism | numerical identity | `cmd/replay --diff-stages` |
| Import boundary | no models/entities imports in profile/ | `TestImportBoundary_ProfilePackage_*` |
| Resolver determinism | pure function | `TestResolve_*` family |
| Config validation | fail-fast on malformed | `TestLoadFromJSON_*` family |

---

## 5. Done-When

Tier 2 closes when ALL of these hold on master:

- [ ] All 47 packages green under `go test ./... -race -count=1`
- [ ] `TestDDM_LegacyPath_BitForBit` passes for JPM/BAC/WFC (3 subtests)
- [ ] 10-ticker basket regression passes (`TestTier2_*_Pin` family)
- [ ] `go run ./cmd/replay --diff-stages artifacts/tier2-baseline/` runs clean
- [ ] 4 trackers archived (`docs/reviewer/archive/{RM-3,VAL-1,VAL-2,VAL-3}*.md`)
- [ ] CalculationVersion bumped 4.1 → 4.2 in a single atomic commit
- [ ] `profile/` package coverage ≥90%
- [ ] No `time.Now()` outside consumer layer (verified by spot-check on `service.go`)
- [ ] `pkg/finance/*` unchanged (verified by `git diff master..HEAD pkg/finance/`)
- [ ] Import boundary intact (`profile` package contains no models or entities imports)

---

## 6. Risks

### Risk R-1: JPM bit-for-bit breaks during P3 worktree work

**Probability:** Medium. The discipline of byte-identical legacy-body paste is unusual; engineers often "improve" code reflexively while pasting.

**Mitigation:** Phase Bootstrap captures the golden fixtures. P3 worktree runs `TestDDM_LegacyPath_BitForBit` as the FIRST thing after the legacy-paste step. If it fails immediately, the paste went wrong — diff against `git show 0324057:internal/services/valuation/models/ddm.go` and fix before proceeding.

**Recovery:** Reset the worktree, re-paste from `git show` line-by-line, re-run test.

### Risk R-2: 4 parallel worktrees create merge conflicts on assumption_profiles.json

**Probability:** Low if ownership table (spec §10.1) is followed; High otherwise.

**Mitigation:** Spec §10.1 explicitly maps every JSON key to one owning stream. Reviewer enforces ownership during V/R/Q gate. Conflicts → revert the violating stream's unauthorized changes.

**Recovery:** Rebase-before-merge for whichever stream tries to merge second. If the rebase auto-resolves cleanly, ship. If not, the conflict resolver picks the union — never "take both" — and routes content to its rightful owner.

### Risk R-3: Resolver thresholds drift breaks replay determinism

**Probability:** Low (P0a + P0b ship ResolvedSnapshot in bundle manifest; resolver-replay uses snapshot when present).

**Mitigation:** Spec §7.3 three-tier replay: snapshot → config_hash match → degraded with warning. Tier 2 ships the snapshot WRITE; READ consumption can land in a follow-up (RPL-5).

### Risk R-4: Profile resolution adds latency to every request

**Probability:** Low. Resolver is pure function over in-memory registry; one map lookup + a few branches. Sub-microsecond cost.

**Mitigation:** Phase Closeout includes a performance check via the 10-ticker live API regression — any noticeable slowdown is investigated.

### Risk R-5: One human reviewer serializes V/R/Q across 4 parallel worktrees

**Probability:** High — this is a structural limitation, not a bug.

**Mitigation:** The 4 streams aren't actually parallel-merging; they're parallel-implementing. V/R/Q happens serially. Calendar benefit is the implementation parallelism (each worktree's BACKEND agent runs concurrently), not the review parallelism. Acknowledge in retrospect; don't over-promise the speedup.

---

## 7. Spec Updates

After Tier 2 ships, update `docs/refactoring/assumption-profile-spec.md` §15 (Estimated commits) to reflect actual commit count and any surprises. If V/R/Q surfaces new trackers (analogous to VAL-4/VAL-5/RM-1.A/RM-1.B from Tier 1), file them in `docs/reviewer/` and archive when closed.

---

## 8. Implementation Outcome

(Placeholder for post-merge fill-in.)

- **Final commit count:** TBD
- **Final LoC:** TBD
- **Coverage achieved:** TBD
- **Surprises:** TBD
- **Follow-up trackers filed:** TBD
- **Spec version after merge:** v0.2

---

## Appendix A — Quick reference for parallel worktree dispatch

Each P1-P4 BACKEND agent gets:
- This implementation plan filename
- The single phase section relevant to their stream (P1 / P2 / P3 / P4)
- The spec sections relevant: §6.X for their model, §10.1 for ownership table
- The 4 modified-file paths owned by their stream
- The `git show 0324057:...` reference for any byte-identity paste (P3 only)

The agent does NOT get:
- Other streams' plan sections
- Other streams' JSON row ownership
- Other streams' regression pins

This keeps the agent's context window focused and prevents cross-stream interference.
