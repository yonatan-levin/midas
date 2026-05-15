# Tier 2 — `AssumptionProfile` Implementer Plan (BACKEND-consumable)

> **For agentic workers:** This is the executable plan BACKEND consumes. Each phase is self-sufficient — a worktree-isolated BACKEND agent reads ONLY this file + the spec + existing source code. No flip-page references to other phases. Steps use checkbox (`- [ ]`) syntax for tracking. REQUIRED SUB-SKILL: `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans`.

**Status:** PLAN v1 — awaiting human approval before BACKEND dispatch.

**Builds on:**
- [`../spec/assumption-profile-spec.md`](../spec/assumption-profile-spec.md) v0.1 — design spec; the *what* and *why*. ALL design decisions, type definitions, JSON schema, and testing strategy are owned by that spec. **DO NOT REDESIGN.**
- [`../spec/assumption-profile-implementation-plan.md`](../spec/assumption-profile-implementation-plan.md) — multi-phase rollout plan (~1,150 lines); structural scaffold for this implementer plan. Has known gaps that THIS plan fills.
- [`../spec/tier-2-assumption-profile-kickoff.md`](../spec/tier-2-assumption-profile-kickoff.md) — scope confirmation
- [`../spec/assumption-profile-db-backed-future.md`](../spec/assumption-profile-db-backed-future.md) — companion future-work tracker
- [`observability-replay-tooling-r3b-implementation-plan.md`](observability-replay-tooling-r3b-implementation-plan.md) — mirrored house style

**Scope:** Tier 2 closes RM-3 + VAL-1 + VAL-2 + VAL-3 Phase 3 via a shared `AssumptionProfile` backbone keyed by `(Archetype × Maturity)`.

**Goal:** Introduce `internal/services/valuation/profile/` package + `config/assumption_profiles.json` + integration into all 4 valuation models, preserving mature-large-bank DDM bit-for-bit (verified via `math.Float64bits` equality on JPM/BAC/WFC), via 4 worktree-isolated BACKEND streams executing in parallel after P0a/P0b ship.

**Tech Stack:** Go 1.23 toolchain 1.24.4, fx DI, uber/zap logging, encoding/json for config, math.Float64bits for bit-for-bit assertions, sha256 for config_hash.

**LoC + commit estimate:**
- Bootstrap: ~250 LoC (test helpers + fixture-capture harness) + ~50 KiB of checked-in baseline data
- P0a (profile package): ~600 LoC across 7 new files + ~400 LoC of tests
- P0b (JSON + wiring + Bundle.SetAssumptionProfileManifest + YoY helper): ~450 LoC of integration + ~250 LoC of JSON + ~80 LoC of new Bundle method + ~30 LoC of YoY helper
- Pre-P2 (growth estimator slice extension): ~20 LoC of config change + ~30 LoC of tests
- P1 (RM-3): ~180 LoC + ~250 LoC of tests + ~80 LoC of JSON profile rows
- P2 (VAL-1): ~230 LoC + ~280 LoC of tests + ~60 LoC of JSON profile rows
- P3 (VAL-2): ~220 LoC (multi-stage; legacy untouched) + ~320 LoC of tests + ~80 LoC of JSON profile rows
- P4 (VAL-3 P3): ~200 LoC + ~240 LoC of tests + ~100 LoC of JSON profile rows
- Closeout: ~30 LoC across 3 commits
- **Estimated total:** ~3,800 LoC across ~13-15 atomic commits

**Commit cadence:** Each phase ships as 1-3 atomic commits so reverts stay surgical. P0a, P0b, Pre-P2, P1, P2, P3, P4 each as 1 commit per stream (worktree-isolated). Closeout = 3 separate commits (integration gate report, tracker archival, CalculationVersion bump).

---

## Revision History

- **v1 (initial)**: Implementer plan derived from spec/-tier rollout plan + 5 critical gap fixes (helper definitions, Bundle method, YoY computation, growth estimator slice, test-pin workflow). Mirrors R3b plan structure. Each phase is self-sufficient for worktree dispatch.

---

## 1. Preamble

### Current state of master at planning time

- **HEAD:** `0324057` (Tier 1 archived + verified clean).
- **Live models** (subject to per-phase modifications):
  - `internal/services/valuation/models/ddm.go` — single-stage Gordon; lines 53-192 are the bit-for-bit invariant subject for P3. **DO NOT REFACTOR THIS BODY**; P3 uses path duplication, not function extraction.
  - `internal/services/valuation/models/revenue_multiple.go` — already consumes RM-1 TTM helper + RM-1.A clock seam; positioned for additive forward path in P1.
  - `internal/services/valuation/models/ffo.go` — already has VAL-3 P1+P4 subsector multiples shipped; positioned for additive forward path in P4.
  - `internal/services/valuation/models/router.go` — defines `ModelInput`, `ModelResult`, `ModelRouter`; UNCHANGED routing logic — Tier 2 keeps routing orthogonal from calibration.
- **Service orchestration:** `internal/services/valuation/service.go::performValuation` (line 544+). Tier 2 inserts profile resolution before `s.modelRouter.SelectModel`.
- **Entity layer:** `internal/core/entities/financial_data.go` (FinancialData struct), `internal/core/entities/valuation.go` (ValuationResult struct), `internal/core/entities/historical_financial.go` (HistoricalFinancialData wrapper — verify exact name when implementing).
- **Bundle layer:** `internal/observability/artifact/bundle.go` (the `Bundle` type with `Snapshot`, `AddSchemaVersion` methods — P0b extends with `SetAssumptionProfileManifest`).
- **Growth estimator:** `internal/services/growth/estimator.go` — produces `ProjectedGrowthRates` slice; Pre-P2 extends max length to support horizon=10.

### Load-bearing invariants (must hold at every commit)

1. **Mature-large-bank DDM bit-for-bit:** `math.Float64bits(JPM.IntrinsicValuePerShare)` is identical pre- and post-Tier-2. Pinned via golden fixture captured in Phase Bootstrap. Failure of `TestDDM_LegacyPath_BitForBit` is a hard stop.
2. **Replay determinism:** Any pre-Tier-2 bundle replays to numerically-identical output (modulo new additive omitempty fields).
3. **No `time.Now()` outside consumer layer:** Clock pattern from RM-1.A preserved across Tier 2.
4. **`pkg/finance/*` unchanged:** D7 invariant from prior phases.
5. **Import boundary:** `internal/services/valuation/profile/` package imports neither `models` nor `entities`. Enforced via package test.

### Key code surfaces

**Already shipped — modified by Tier 2:**
- `internal/services/valuation/service.go` — `performValuation`; P0b adds `profile.Resolve` call
- `internal/services/valuation/models/router.go` — `ModelInput`; P0b adds `Profile *profile.ResolvedProfile` field
- `internal/services/valuation/models/ddm.go` — `Calculate`; P3 adds dispatch (legacy lines stay byte-identical)
- `internal/services/valuation/models/revenue_multiple.go` — `Calculate`; P1 adds forward path
- `internal/services/valuation/models/ffo.go` — `Calculate`; P4 adds forward path
- `internal/core/entities/valuation.go` — `ValuationResult`; P0b adds 5 omitempty DCF diagnostic fields + AssumptionProfile + ResolutionTrace
- `internal/observability/artifact/bundle.go` — `Bundle`; P0b adds `SetAssumptionProfileManifest` method
- `internal/services/growth/estimator.go` — Pre-P2 bumps slice-length cap from 7 to 10
- `internal/services/valuation/di/container.go` (or wherever fx wiring lives) — P0b adds `NewProfileRegistry` provider

**New code surfaces:**
- `internal/services/valuation/profile/` (7 files + tests — P0a)
- `config/assumption_profiles.json` (P0b)
- `internal/services/valuation/profile/testhelpers/` package + `testhelpers_test.go` — defined in Bootstrap; consumed by P1-P4
- `internal/services/valuation/profile/testdata/golden/` — Bootstrap captures
- `internal/services/valuation/models/testdata/golden/` — Bootstrap captures DDM bit-for-bit fixtures
- `internal/services/valuation/models/util.go` — `avg([]float64) float64` helper (NEW; P1 creates)
- `artifacts/tier2-baseline/` — Bootstrap captures 10 replay bundles
- `internal/services/valuation/profile/tier2_regression_test.go` — Bootstrap creates skeleton; populated by P1-P4

---

## 2. Pre-Flight

**No spike required.** Profile package is additive; no novel fx-composition concerns. Three execution-level uncertainties BACKEND resolves at the start of Phase Bootstrap:

### Pre-A — Verify master is at `0324057` and green

- **Action:** `git rev-parse HEAD` confirms `0324057`. `go test ./... -count=1` returns 47/47 packages green. `go run ./cmd/replay --diff-stages artifacts/<any-existing-bundle>` runs clean.
- **If master has moved:** rebase intent against new HEAD; verify the moved HEAD does not touch any of the 4 model files Tier 2 modifies. If it does, re-check assumptions before proceeding.
- **If anything is RED:** stop. Fix the regression on master before Tier 2 dispatches.

### Pre-B — Decide golden-fixture capture mechanism

**Decision Pre-B.1 — Use live-engine artifact-bundle pipeline with `X-Midas-Trace: 1`.** Already-existing path. Bootstrap Task B.1 runs `cmd/server` locally, issues 10 curl requests with `X-Midas-Trace: 1`, collects the resulting bundles, moves them into `artifacts/tier2-baseline/` for git tracking.

### Pre-C — Confirm Go import-boundary enforcement mechanism

**Decision Pre-C.1 — Use a `go/parser`-based test in the profile package's own `_test.go`.** Mirrors `cmd/server/import_boundary_test.go` pattern. P0a Task 5 creates `internal/services/valuation/profile/import_boundary_test.go` that scans every `*.go` file in the package and asserts no imports of `internal/services/valuation/models` or `internal/core/entities`.

---

## 3. Ordered Task List (TDD)

Each task is `Test first → Implementation → Acceptance`. Phases run sequentially: Bootstrap → P0a → P0b → Pre-P2 → (P1‖P2‖P3‖P4) → Closeout. Within a phase, tasks land in a single commit unless explicitly split.

---

### Phase Bootstrap — pre-Tier-2 regression baseline capture + test helpers

**Goal:** Capture bit-for-bit baselines, cross-model regression fixtures, AND define ALL test helpers used by subsequent phases. Lands as a SINGLE commit on master before any P0 work begins.

**Why test helpers ship in Bootstrap:** P1, P2, P3, P4 dispatch into separate worktrees and need consistent helper definitions. Defining them once in Bootstrap (under the new `testhelpers` package) means each worktree imports the same symbols.

#### Task B.1 — Capture 10 artifact bundles from live engine

- **Files:**
  - Create: `artifacts/tier2-baseline/2026-05-14/{AAPL,MSFT,JPM,KO,F,MXL,NVDA,AMD,EQIX,PLD}/req_*/...`

- [ ] **Step 1: Start local server**

```bash
go build -o bin/midas-server ./cmd/server
./bin/midas-server &
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

- [ ] **Step 4: Move bundles into tier2-baseline directory**

```bash
for TICKER in AAPL MSFT JPM KO F MXL NVDA AMD EQIX PLD; do
  mv artifacts/2026-05-14/$TICKER artifacts/tier2-baseline/2026-05-14/
done
```

- [ ] **Step 5: Verify all bundles have expected stage files**

```bash
for TICKER in AAPL MSFT JPM KO F MXL NVDA AMD EQIX PLD; do
  REQ_DIR=$(ls -d artifacts/tier2-baseline/2026-05-14/$TICKER/req_*/ | head -1)
  for STAGE in 17-response.json 15-valuation.json 13-wacc.json 12-growth-curve.json 10-clean-output.json manifest.json; do
    test -f "$REQ_DIR/$STAGE" || echo "MISSING $TICKER/$STAGE"
  done
done
```

Expected: No "MISSING" output.

- [ ] **Step 6: Stop server**

```bash
pkill -f midas-server
```

#### Task B.2 — Generate DDM bit-for-bit golden fixtures (JPM/BAC/WFC)

- **Files:**
  - Create: `internal/services/valuation/models/testdata/golden/{jpm,bac,wfc}_ddm_pre_tier2_input.json`
  - Create: `internal/services/valuation/models/testdata/golden/{jpm,bac,wfc}_ddm_pre_tier2_output.json`
  - Create: `internal/services/valuation/models/golden_capture_test.go` (build-tag-gated helper)

- [ ] **Step 1: Write capture-helper test (gated by `-tags goldencapture`)**

Create `internal/services/valuation/models/golden_capture_test.go`:

```go
//go:build goldencapture

package models_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// TestCaptureGoldenDDM is a one-shot helper run with `-tags goldencapture`.
// Reads ModelInput fixtures derived from production bundles, calls
// DDMModel.Calculate, writes the result JSON to testdata/golden/.
// Not part of normal test suite — for regenerating goldens only.
func TestCaptureGoldenDDM(t *testing.T) {
	tickers := []string{"jpm", "bac", "wfc"}
	for _, ticker := range tickers {
		t.Run(ticker, func(t *testing.T) {
			inputPath := filepath.Join("testdata/golden", ticker+"_ddm_pre_tier2_input.json")
			outputPath := filepath.Join("testdata/golden", ticker+"_ddm_pre_tier2_output.json")
			data, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatalf("input fixture missing at %s: %v (manually create from bundle first)", inputPath, err)
			}
			var input models.ModelInput
			if err := json.Unmarshal(data, &input); err != nil {
				t.Fatalf("unmarshal input: %v", err)
			}
			ddm := models.NewDDMModel(zap.NewNop())
			result, err := ddm.Calculate(context.Background(), &input)
			if err != nil {
				t.Fatalf("DDM calculate: %v", err)
			}
			out, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				t.Fatalf("marshal output: %v", err)
			}
			if err := os.WriteFile(outputPath, out, 0o644); err != nil {
				t.Fatalf("write output: %v", err)
			}
			fmt.Printf("Captured golden for %s: %s (intrinsic=%.6f)\n",
				ticker, outputPath, result.IntrinsicValuePerShare)
		})
	}
}
```

- [ ] **Step 2: Derive ModelInput fixtures from production bundles**

For each of JPM, BAC, WFC, manually assemble a `ModelInput` JSON file at `testdata/golden/<ticker>_ddm_pre_tier2_input.json` from the bundle's `02-sec-facts.parsed.json` + `06-market.parsed.json` + `07-fetch-macro.parsed.json`. The struct shape mirrors `models.ModelInput` (search `internal/services/valuation/models/router.go` for the struct definition).

Verify:

```bash
cat internal/services/valuation/models/testdata/golden/jpm_ddm_pre_tier2_input.json | jq '.HistoricalData.Ticker, .CostOfEquity, .SharesOutstanding'
```

Expected: `"JPM"`, positive float ~0.10, positive share count.

- [ ] **Step 3: Run capture-helper test to produce output goldens**

```bash
go test -tags goldencapture -run TestCaptureGoldenDDM ./internal/services/valuation/models/...
```

Expected: 3 PASS; 3 new output files; printed `intrinsic=X.XXXXXX` for each ticker.

- [ ] **Step 4: Sanity-check captured outputs**

```bash
for TICKER in jpm bac wfc; do
  jq '.intrinsic_value_per_share, .model_type, .confidence' \
    internal/services/valuation/models/testdata/golden/${TICKER}_ddm_pre_tier2_output.json
done
```

Expected: positive float per ticker (close to current market price), `"ddm"`, `"high"` or `"medium"`.

#### Task B.3 — Create the `testhelpers` package (consumed by P1-P4)

**This Task fills Gap #1 from the critique.** All helper functions referenced across phases are defined ONCE here.

- **Files:**
  - Create: `internal/services/valuation/profile/testhelpers/testhelpers.go` (EXPORTED package; cross-package helpers)
  - Create: `internal/services/valuation/profile/testhelpers/fixtures.go` (synthetic ModelInput builders)
  - Create: `internal/services/valuation/profile/testhelpers/profile_registry.go` (Registry fixture loaders)
  - Create: `internal/services/valuation/profile/testhelpers/service.go` (test Service builders)

- [ ] **Step 1: Create `testhelpers/fixtures.go` — synthetic ModelInput builders**

```go
// Package testhelpers provides shared test fixtures and helpers for Tier 2
// AssumptionProfile work. Helpers are defined ONCE here and consumed by
// every phase (P1/P2/P3/P4) so each worktree-isolated BACKEND agent uses
// identical fixtures.
package testhelpers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// BuildMXLModelInput returns a ModelInput approximating MXL (negative-OI
// cyclical-trough semi) for P1 RM-3 testing. Synthetic but representative
// of the trough shape: revenue ~$560M TTM, OI ~-$50M, negative growth.
func BuildMXLModelInput(t *testing.T) *models.ModelInput {
	t.Helper()
	latest := &entities.FinancialData{
		Ticker:                    "MXL",
		Revenue:                   560_000_000,
		OperatingIncome:           -50_000_000,
		NormalizedOperatingIncome: -50_000_000,
		NetIncome:                 -75_000_000,
		InterestBearingDebt:       151_000_000,
		CashAndCashEquivalents:    61_000_000,
		StockholdersEquity:        300_000_000,
		TaxRate:                   0.21,
		FilingDate:                time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	historical := buildHistoricalFromLatest(latest, []float64{560e6, 800e6, 1200e6, 950e6, 600e6})
	return &models.ModelInput{
		HistoricalData:         historical,
		MarketData:             buildMarketData("MXL", 80.0, 1.5),
		MacroData:              buildMacroData(0.04, 0.06),
		GrowthEstimate:         buildGrowthEstimate([]float64{0.50, 0.50, 0.41, 0.33, 0.25, 0.16, 0.08}, 0.03, "high"),
		Industry:               "MFG_SEMI",
		WACC:                   0.19,
		CostOfEquity:           0.21,
		TaxRate:                0.21,
		SharesOutstanding:      82_000_000,
		InterestBearingDebt:    151_000_000,
		CashAndCashEquivalents: 61_000_000,
		Now:                    func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	}
}

// BuildSyntheticAAPLishModelInput returns a ModelInput shaped like a
// maturing-tech-first-dividend ticker (AAPL-ish). Used by P3 to test the
// multi-stage DDM path with rising payout.
func BuildSyntheticAAPLishModelInput(t *testing.T) *models.ModelInput {
	t.Helper()
	latest := &entities.FinancialData{
		Ticker:                    "SYNTH_AAPLISH",
		Revenue:                   390_000_000_000,
		OperatingIncome:           115_000_000_000,
		NormalizedOperatingIncome: 115_000_000_000,
		NetIncome:                 95_000_000_000,
		DividendsPerShare:         0.95,
		InterestBearingDebt:       110_000_000_000,
		CashAndCashEquivalents:    65_000_000_000,
		StockholdersEquity:        62_000_000_000,
		TaxRate:                   0.16,
		FilingDate:                time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	historical := buildHistoricalFromLatest(latest, []float64{390e9, 380e9, 365e9, 350e9, 320e9})
	historical.AnnualPeriods[0].DividendsPerShare = 0.95
	historical.AnnualPeriods[1].DividendsPerShare = 0.88
	historical.AnnualPeriods[2].DividendsPerShare = 0.80
	historical.AnnualPeriods[3].DividendsPerShare = 0.74
	historical.AnnualPeriods[4].DividendsPerShare = 0.66
	return &models.ModelInput{
		HistoricalData:         historical,
		MarketData:             buildMarketData("SYNTH_AAPLISH", 190.0, 1.25),
		MacroData:              buildMacroData(0.04, 0.06),
		GrowthEstimate:         buildGrowthEstimate([]float64{0.08, 0.07, 0.06, 0.05, 0.05, 0.04, 0.04, 0.03, 0.03, 0.03}, 0.03, "high"),
		Industry:               "TECH",
		WACC:                   0.10,
		CostOfEquity:           0.11,
		TaxRate:                0.16,
		SharesOutstanding:      15_500_000_000,
		InterestBearingDebt:    110_000_000_000,
		CashAndCashEquivalents: 65_000_000_000,
		Now:                    func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	}
}

// BuildSyntheticDataCenterREITInput returns a ModelInput shaped like a
// data-center REIT (EQIX-ish) for P4 forward-FFO testing.
func BuildSyntheticDataCenterREITInput(t *testing.T) *models.ModelInput {
	t.Helper()
	latest := &entities.FinancialData{
		Ticker:                      "SYNTH_DCREIT",
		Revenue:                     8_000_000_000,
		OperatingIncome:             1_400_000_000,
		NetIncome:                   600_000_000,
		DepreciationAndAmortization: 1_900_000_000,
		GainOnPropertySales:         50_000_000,
		InterestBearingDebt:         16_000_000_000,
		CashAndCashEquivalents:      2_000_000_000,
		StockholdersEquity:          9_000_000_000,
		TaxRate:                     0.21,
		FilingDate:                  time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	historical := buildHistoricalFromLatest(latest, []float64{8e9, 7.2e9, 6.5e9, 5.9e9, 5.3e9})
	return &models.ModelInput{
		HistoricalData:         historical,
		MarketData:             buildMarketData("SYNTH_DCREIT", 800.0, 0.85),
		MacroData:              buildMacroData(0.04, 0.06),
		GrowthEstimate:         buildGrowthEstimate([]float64{0.12, 0.11, 0.10, 0.08, 0.07, 0.05, 0.04}, 0.03, "high"),
		Industry:               "REIT_DATACENTER",
		WACC:                   0.08,
		CostOfEquity:           0.09,
		TaxRate:                0.21,
		SharesOutstanding:      95_000_000,
		InterestBearingDebt:    16_000_000_000,
		CashAndCashEquivalents: 2_000_000_000,
		Now:                    func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	}
}

func buildHistoricalFromLatest(latest *entities.FinancialData, revenueHistory []float64) *entities.HistoricalFinancialData {
	periods := make([]*entities.FinancialData, len(revenueHistory))
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, rev := range revenueHistory {
		p := *latest
		p.Revenue = rev
		p.AsOf = now.AddDate(-i, 0, 0)
		p.FilingDate = p.AsOf.AddDate(0, 3, 0)
		periods[i] = &p
	}
	return &entities.HistoricalFinancialData{
		Ticker:        latest.Ticker,
		AnnualPeriods: periods,
	}
}

func buildMarketData(ticker string, price, beta float64) *entities.MarketData {
	return &entities.MarketData{
		Ticker:            ticker,
		Price:             price,
		Beta:              beta,
		SharesOutstanding: 0, // populated by caller via ModelInput.SharesOutstanding
	}
}

func buildMacroData(rf, erp float64) *entities.MacroData {
	return &entities.MacroData{
		RiskFreeRate:      rf,
		MarketRiskPremium: erp,
	}
}

func buildGrowthEstimate(rates []float64, terminal float64, confidence string) *entities.GrowthEstimate {
	return &entities.GrowthEstimate{
		ProjectedGrowthRates: rates,
		TerminalGrowthRate:   terminal,
		Confidence:           confidence,
	}
}
```

- [ ] **Step 2: Create `testhelpers/profile_registry.go` — Registry fixture loaders**

```go
package testhelpers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// MustLoadFullFixture loads a fuller test fixture with cyclical_mid_cycle,
// cyclical_trough, fin_large_bank, fin_generic profiles + rules. Suitable
// for resolver tests that need a richer rule set than the minimal config.
func MustLoadFullFixture(t *testing.T) profile.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	require.NoError(t, os.WriteFile(path, []byte(fullFixtureConfig), 0o644))
	reg, err := profile.LoadFromJSON(path)
	require.NoError(t, err)
	return reg
}

const fullFixtureConfig = `{
  "config_version": "1.0.0",
  "resolver_version": "1.0.0",
  "profiles": {
    "mature_large_bank:mature": {
      "profile_id": "mature_large_bank:mature",
      "archetype": "mature_large_bank", "maturity": "mature",
      "horizon_years": 3, "compound_growth_cap": 1.5,
      "revenue_base_method": "raw_ttm", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": true, "fade_years": 0,
      "terminal_multiple": 0.8, "dps_growth_cap": 0.08,
      "payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "cyclical_mid_cycle:standard_growth": {
      "profile_id": "cyclical_mid_cycle:standard_growth",
      "archetype": "cyclical_mid_cycle", "maturity": "standard_growth",
      "horizon_years": 5, "compound_growth_cap": 2.0,
      "revenue_base_method": "two_year_average", "discount_method": "cost_of_equity",
      "terminal_method": "gordon_growth", "stabilized": false, "fade_years": 1,
      "terminal_multiple": 3.0, "dps_growth_cap": 0,
      "payout_path": [], "dividend_forecast_horizon": 0,
      "stable_dividend_growth": 0.03
    },
    "cyclical_trough:standard_growth": {
      "profile_id": "cyclical_trough:standard_growth",
      "archetype": "cyclical_trough", "maturity": "standard_growth",
      "horizon_years": 5, "compound_growth_cap": 3.0,
      "revenue_base_method": "max_ttm_or_floor", "discount_method": "cost_of_equity",
      "terminal_method": "exit_multiple", "stabilized": false, "fade_years": 2,
      "terminal_multiple": 4.0, "dps_growth_cap": 0,
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
    {"id":"fin_generic","priority":50,"industry_prefix":"FIN","archetype":"mature_large_bank"},
    {"id":"mfg_semi","priority":90,"industry_prefix":"MFG_SEMI","archetype":"cyclical_mid_cycle"},
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
```

- [ ] **Step 3: Create `testhelpers/service.go` — Test Service builders**

```go
package testhelpers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// BuildTestService constructs a fully-wired Service backed by the full
// fixture profile.Registry. Used by integration tests that need to
// exercise the full valuation lifecycle (service.CalculateValuation).
//
// Implementation detail: the test Service is built by re-using midas's
// existing test-service builder (search for `buildTestServiceForTesting`
// or similar in `internal/services/valuation/service_test.go`); this
// helper wraps it with the profile.Registry injection.
func BuildTestService(t *testing.T) *valuation.Service {
	t.Helper()
	reg := MustLoadFullFixture(t)
	// NOTE: this helper requires the existing test-service builder.
	// Search `internal/services/valuation/` for the existing pattern;
	// if no builder exists, this helper is implemented by replicating
	// the fx.New(...) pattern from di/container.go but with mock repos.
	return buildServiceWithRegistry(t, reg)
}

// BuildTestServiceWithFixedProfile constructs a Service that resolves
// EVERY ticker to the given profileID. Used by P2 to test DCF
// archetype-aware horizon without relying on the full resolver chain.
func BuildTestServiceWithFixedProfile(t *testing.T, profileID string) *valuation.Service {
	t.Helper()
	// Stub registry returns the named profile for any Resolve call.
	stubReg := &stubFixedRegistry{profileID: profileID, full: MustLoadFullFixture(t)}
	return buildServiceWithRegistry(t, stubReg)
}

// RunValuation runs a full CalculateValuation against the test Service
// and returns the result for assertion. ticker MUST be one of the
// 10-ticker basket; the corresponding artifact bundle pre-populates
// the data repositories via test fixtures.
func RunValuation(t *testing.T, ticker string) *entities.ValuationResult {
	t.Helper()
	svc := BuildTestService(t)
	result, err := svc.CalculateValuation(context.Background(), ticker, nil)
	require.NoError(t, err)
	return result
}

// LoadGoldenJPMPrimaryValue returns the pre-Tier-2 captured
// IntrinsicValuePerShare for JPM, for bit-for-bit comparison in P3
// regression tests.
func LoadGoldenJPMPrimaryValue(t *testing.T) float64 {
	t.Helper()
	// Reads internal/services/valuation/models/testdata/golden/
	//      jpm_ddm_pre_tier2_output.json captured by Task B.2
	return loadGoldenPrimary(t, "jpm")
}

// (helpers buildServiceWithRegistry, stubFixedRegistry, loadGoldenPrimary
// are implementation details — define them inline in this file)
```

The `buildServiceWithRegistry` helper requires inspecting `internal/services/valuation/di/container.go` to mirror the fx.New pattern. BACKEND completes this inline based on the existing wiring conventions.

- [ ] **Step 4: Run tests to verify helpers compile**

```bash
go build ./internal/services/valuation/profile/testhelpers/...
```

Expected: clean compile.

#### Task B.4 — Write the bit-for-bit regression test (uses helpers)

- **Files:**
  - Create: `internal/services/valuation/models/ddm_bitforbit_test.go`
  - Create: `internal/services/valuation/profile/tier2_regression_test.go` (skeleton)

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

// TestDDM_LegacyPath_BitForBit asserts mature-large-bank DDM output is
// byte-identical pre- and post-Tier-2. Legacy single-stage Gordon path
// (DividendForecastHorizon == 0 OR Profile == nil) must produce the
// same math.Float64bits for IntrinsicValuePerShare, EquityValue,
// EnterpriseValue as captured pre-Tier-2 at master 0324057.
//
// This test FAILS immediately if any Tier 2 commit causes drift in
// the legacy path. Load-bearing assertion for VAL-2 backward-compat
// (spec §7.1).
func TestDDM_LegacyPath_BitForBit(t *testing.T) {
	tickers := []string{"jpm", "bac", "wfc"}
	for _, ticker := range tickers {
		t.Run(ticker, func(t *testing.T) {
			input := loadGoldenInput(t, ticker)
			expected := loadGoldenOutput(t, ticker)

			ddm := models.NewDDMModel(zap.NewNop())
			actual, err := ddm.Calculate(context.Background(), input)
			require.NoError(t, err)

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

			assert.Equal(t, expected.ModelType, actual.ModelType)
			assert.Equal(t, expected.Confidence, actual.Confidence)
			assert.Equal(t, expected.Warnings, actual.Warnings)
		})
	}
}

func loadGoldenInput(t *testing.T, ticker string) *models.ModelInput {
	t.Helper()
	path := filepath.Join("testdata/golden", ticker+"_ddm_pre_tier2_input.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "golden input fixture missing — run Task B.2 first")
	var input models.ModelInput
	require.NoError(t, json.Unmarshal(data, &input))
	return &input
}

func loadGoldenOutput(t *testing.T, ticker string) *models.ModelResult {
	t.Helper()
	path := filepath.Join("testdata/golden", ticker+"_ddm_pre_tier2_output.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "golden output fixture missing — run Task B.2 first")
	var result models.ModelResult
	require.NoError(t, json.Unmarshal(data, &result))
	return &result
}
```

- [ ] **Step 2: Run regression test to verify it passes against unchanged code**

```bash
go test -run TestDDM_LegacyPath_BitForBit ./internal/services/valuation/models/...
```

Expected: PASS for all 3 subtests. If FAIL: golden capture is bad; regenerate Task B.2.

- [ ] **Step 3: Write the cross-model regression skeleton**

Create `internal/services/valuation/profile/tier2_regression_test.go`:

```go
package profile_test

// Tier 2 cross-model regression suite. Pins 6 fields per ticker per
// spec §8.2:
//   - assumption_profile (exact)
//   - horizon_selected (exact)
//   - chosen_model (exact)
//   - primary_value (bit-for-bit for mature_large_bank, ε=1e-9 elsewhere)
//   - trailing_value (ε=1e-9 where applicable)
//   - warning_count (exact)
//
// Populated incrementally by P1-P4 worktrees. Skeleton lands in
// Phase Bootstrap so the file exists at master HEAD before parallel
// work dispatches.

import "testing"

func TestTier2_BasketRegression(t *testing.T) {
	t.Skip("Populated by P1-P4 worktrees; skeleton only at Phase Bootstrap")
}
```

#### Task B.5 — Commit Phase Bootstrap

- [ ] **Step 1: Stage and commit**

```bash
git add artifacts/tier2-baseline/ \
        internal/services/valuation/models/testdata/golden/ \
        internal/services/valuation/models/golden_capture_test.go \
        internal/services/valuation/models/ddm_bitforbit_test.go \
        internal/services/valuation/profile/testhelpers/ \
        internal/services/valuation/profile/tier2_regression_test.go
git commit -m "$(cat <<'EOF'
chore(tier2): capture pre-Tier-2 baselines + test helpers (Phase Bootstrap)

- 10 artifact bundles captured at master 0324057 in artifacts/tier2-baseline/
- 3 DDM bit-for-bit golden fixtures (JPM/BAC/WFC) under
  internal/services/valuation/models/testdata/golden/ with paired
  input + output JSON
- Build-tag-gated TestCaptureGoldenDDM helper for golden regeneration
- TestDDM_LegacyPath_BitForBit regression test (passes against current master)
- internal/services/valuation/profile/testhelpers/ package with all
  test fixtures + builders consumed by P1-P4: BuildMXLModelInput,
  BuildSyntheticAAPLishModelInput, BuildSyntheticDataCenterREITInput,
  BuildTestService, BuildTestServiceWithFixedProfile, RunValuation,
  LoadGoldenJPMPrimaryValue, MustLoadFullFixture
- tier2_regression_test.go skeleton (populated by P1-P4 streams)

Baseline captured at master 0324057. Every subsequent Tier 2 commit
must keep TestDDM_LegacyPath_BitForBit green. Spec §9.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase P0a — Profile package skeleton + Facts DTO + resolver

**Goal:** Land entire `internal/services/valuation/profile/` package: types, enums, Facts DTO, Registry interface, resolver, validation, import-boundary guard. ZERO integration with consumers — service.go and models stay untouched. Verifies the Go import-cycle prevention works in isolation.

**Worktree dispatch:** This phase runs in its own git worktree (`worktree-agent-p0a`).

#### Task P0a.1 — Define core types (`profile.go`, `trace.go`, `version.go`)

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

- [ ] **Step 2: Run to verify failure**

```bash
go test -run "TestAssumptionProfile_AllFieldsPresent|TestResolvedProfile_IsLegacyMatureLargeBankDDM" ./internal/services/valuation/profile/...
```

Expected: FAIL — undefined types.

- [ ] **Step 3: Create profile.go**

Create `internal/services/valuation/profile/profile.go` with the full content per spec §3.1: `Archetype` enum (21 constants), `Maturity` enum (3 constants), `RevenueBaseMethod`/`TerminalMethod`/`DiscountMethod` enums, `AssumptionProfile` struct (14 fields, see spec §3.1 for exact JSON tags + field types), `SizeThresholds` struct, `ResolvedProfile` struct with embedded AssumptionProfile + Trace ResolutionTrace, and the `IsLegacyMatureLargeBankDDM()` method (returns `r != nil && r.DividendForecastHorizon == 0 && r.Archetype == ArchetypeMatureLargeBank`).

- [ ] **Step 4: Create trace.go**

Create `internal/services/valuation/profile/trace.go` with `ResolutionTrace` struct (ProfileID, Source, ResolverVersion, ConfigVersion, ConfigHash, MatchedRuleID, FallbackReason, MissingFacts, HumanReason fields per spec §3.3), `Source` enum (SourceExplicit, SourceInferred, SourceFallback), `AssumptionProfileManifest` struct (with optional `ResolvedSnapshot *AssumptionProfile`).

- [ ] **Step 5: Create version.go**

```go
package profile

// ResolverVersion is the semver of the resolver logic itself. Bumped on
// any change to the resolver algorithm (Stage 1/2/3 logic, override
// rules, etc.). Stamps onto ResolutionTrace and AssumptionProfileManifest
// for replay determinism per spec §7.3.
const ResolverVersion = "1.0.0"
```

- [ ] **Step 6: Run tests to verify pass**

```bash
go test -run "TestAssumptionProfile_AllFieldsPresent|TestResolvedProfile_IsLegacyMatureLargeBankDDM" ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P0a.2 — Facts DTO (`facts.go`)

- **Files:**
  - Create: `internal/services/valuation/profile/facts.go`
  - Test: `internal/services/valuation/profile/facts_test.go`

- [ ] **Step 1: Write failing test**

```go
package profile_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

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

Expected: FAIL.

- [ ] **Step 3: Create facts.go**

```go
package profile

import "strings"

// Facts is the neutral interchange struct populated by service.go from
// entities.FinancialData / HistoricalFinancialData / MarketData. Pointer
// fields distinguish "no signal" (nil) from "zero is meaningful".
//
// The profile package contains NO imports of entities or models — Facts
// is the deliberate boundary preventing the Go import cycle:
//   models → profile → models  (forbidden)
// Translation from entities.FinancialData → Facts lives at the consumer
// site (service.go), keeping profile/ free of entities dependencies.
type Facts struct {
	Industry                   string
	IndustryNormalized         string
	Revenue                    *float64
	OperatingIncome            *float64
	NetIncome                  *float64
	RevenueGrowthYoY           *float64
	ConsecutivePositiveOIYears int
	MarketCap                  *float64
	DividendsPerShare          *float64
}

// NewFactsForTest is exported for test use only. Production code
// constructs Facts directly in service.go (which has entities imports).
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

- [ ] **Step 1: Write failing tests**

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

func TestLoadFromJSON_ValidConfig_LoadsSuccessfully(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	require.NoError(t, os.WriteFile(path, []byte(minimalValidConfig), 0o644))

	reg, err := profile.LoadFromJSON(path)
	require.NoError(t, err)

	assert.Equal(t, "1.0.0", reg.ConfigVersion())
	assert.NotEmpty(t, reg.ConfigHash())
}

func TestLoadFromJSON_Malformed_FailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	require.NoError(t, os.WriteFile(path, []byte("{ not valid json"), 0o644))

	_, err := profile.LoadFromJSON(path)
	assert.Error(t, err, "malformed config must error, never silently degrade")
}

func TestLoadFromJSON_MissingFallbackRule_FailsValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	raw := `{"config_version":"1.0.0","resolver_version":"1.0.0","profiles":{},"archetype_rules":[],"maturity_thresholds_fallback":{}}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

	_, err := profile.LoadFromJSON(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fallback")
}

func TestRegistry_Lookup_Hit(t *testing.T) {
	reg := mustLoadMinimal(t)
	p, ok := reg.Lookup(profile.ArchetypeMatureLargeBank, profile.MaturityMature)
	require.True(t, ok)
	assert.Equal(t, "mature_large_bank:mature", p.ProfileID)
}

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
      "archetype": "mature_large_bank", "maturity": "mature",
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

- [ ] **Step 2: Run to verify failure**

```bash
go test -run "TestLoadFromJSON_|TestRegistry_Lookup_" ./internal/services/valuation/profile/...
```

Expected: FAIL.

- [ ] **Step 3: Create registry.go**

Create `internal/services/valuation/profile/registry.go` with:

```go
package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Registry is the lookup surface for profiles. Designed so a future DB-backed
// implementation can swap in without touching consumers. See companion doc:
// docs/refactoring/spec/assumption-profile-db-backed-future.md.
type Registry interface {
	Resolve(facts Facts) (*ResolvedProfile, ResolutionTrace)
	Lookup(arch Archetype, mat Maturity) (*AssumptionProfile, bool)
	ConfigVersion() string
	ConfigHash() string
}

// ArchetypeRule is one priority-ordered rule in the resolver's Stage-1 match.
type ArchetypeRule struct {
	ID             string    `json:"id"`
	Priority       int       `json:"priority"`
	IndustryPrefix string    `json:"industry_prefix"`
	Archetype      Archetype `json:"archetype"`
	Notes          string    `json:"notes,omitempty"`
}

// MaturityThresholds carries the global fallback size + growth cutoffs for
// the resolver's Stage-2 maturity bucketing. Archetype-specific overrides
// live on AssumptionProfile.SizeThresholds.
type MaturityThresholds struct {
	LargeCapMinUSD       float64 `json:"large_cap_revenue_min_usd"`
	MidCapMinUSD         float64 `json:"mid_cap_revenue_min_usd"`
	HighGrowthYoYMin     float64 `json:"high_growth_revenue_yoy_min"`
	MatureYoYMax         float64 `json:"mature_revenue_yoy_max"`
	TroughOIThreshold    float64 `json:"trough_oi_threshold"`
}

// configFile is the on-disk JSON shape parsed by LoadFromJSON.
type configFile struct {
	ConfigVersion              string                       `json:"config_version"`
	ResolverVersion            string                       `json:"resolver_version"`
	Profiles                   map[string]AssumptionProfile `json:"profiles"`
	ArchetypeRules             []ArchetypeRule              `json:"archetype_rules"`
	MaturityThresholdsFallback MaturityThresholds           `json:"maturity_thresholds_fallback"`
}

type archetypeMaturityKey struct {
	Arch Archetype
	Mat  Maturity
}

// jsonRegistry is the concrete JSON-backed Registry implementation. Frozen
// at construction; safe for concurrent reads. Rule matching uses the sorted
// archetypeRules slice — no map iteration.
type jsonRegistry struct {
	configVersion      string
	configHash         string
	profiles           map[archetypeMaturityKey]*AssumptionProfile
	archetypeRules     []ArchetypeRule
	fallbackProfile    *AssumptionProfile
	maturityThresholds MaturityThresholds
}

// LoadFromJSON loads the registry from assumption_profiles.json. Returns
// error on any of:
//   - file not readable
//   - JSON malformed
//   - validation failure (unknown archetype, missing fallback, etc.)
// Service MUST fail startup on any of these — invalid shipped config is an
// operator error, not user-data graceful-degradation.
func LoadFromJSON(path string) (Registry, error) {
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg configFile
	if err := json.Unmarshal(rawBytes, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}

	// Build sorted archetype_rules (descending priority).
	rules := make([]ArchetypeRule, len(cfg.ArchetypeRules))
	copy(rules, cfg.ArchetypeRules)
	sort.SliceStable(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})

	// Index profiles by (archetype, maturity).
	idx := make(map[archetypeMaturityKey]*AssumptionProfile, len(cfg.Profiles))
	for id, p := range cfg.Profiles {
		profileCopy := p
		profileCopy.ProfileID = id
		idx[archetypeMaturityKey{Arch: p.Archetype, Mat: p.Maturity}] = &profileCopy
	}

	// Locate fallback profile (rule with industry_prefix "*").
	var fallback *AssumptionProfile
	for _, rule := range rules {
		if rule.IndustryPrefix == "*" {
			// Pick any maturity-variant of the fallback archetype.
			for k, p := range idx {
				if k.Arch == rule.Archetype {
					fallback = p
					break
				}
			}
			break
		}
	}
	if fallback == nil {
		return nil, fmt.Errorf("validate: fallback rule references archetype %s but no profile exists for it",
			rules[len(rules)-1].Archetype)
	}

	// Compute config hash (canonicalized JSON for stable hashing).
	canonical, _ := json.Marshal(&cfg) // ignore error; rawBytes already parsed successfully
	sum := sha256.Sum256(canonical)

	return &jsonRegistry{
		configVersion:      cfg.ConfigVersion,
		configHash:         hex.EncodeToString(sum[:]),
		profiles:           idx,
		archetypeRules:     rules,
		fallbackProfile:    fallback,
		maturityThresholds: cfg.MaturityThresholdsFallback,
	}, nil
}

func (r *jsonRegistry) ConfigVersion() string { return r.configVersion }
func (r *jsonRegistry) ConfigHash() string    { return r.configHash }

func (r *jsonRegistry) Lookup(arch Archetype, mat Maturity) (*AssumptionProfile, bool) {
	p, ok := r.profiles[archetypeMaturityKey{Arch: arch, Mat: mat}]
	return p, ok
}

// applyFallback returns the conservative fallback profile, copying it into a
// ResolvedProfile with the supplied trace.
func (r *jsonRegistry) applyFallback(trace *ResolutionTrace) *ResolvedProfile {
	if r.fallbackProfile == nil {
		return nil
	}
	trace.ProfileID = r.fallbackProfile.ProfileID
	return &ResolvedProfile{
		AssumptionProfile: *r.fallbackProfile,
		Trace:             *trace,
	}
}

// thresholdsForArchetype returns the size thresholds for the given archetype,
// using the archetype-specific overrides if set, otherwise the global fallback.
func (r *jsonRegistry) thresholdsForArchetype(arch Archetype) SizeThresholds {
	for k, p := range r.profiles {
		if k.Arch == arch && p.SizeThresholds != nil {
			return *p.SizeThresholds
		}
	}
	return SizeThresholds{
		LargeCapMinUSD: r.maturityThresholds.LargeCapMinUSD,
		MidCapMinUSD:   r.maturityThresholds.MidCapMinUSD,
	}
}
```

- [ ] **Step 4: Create validation.go**

```go
package profile

import (
	"fmt"
	"regexp"
)

var semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func validateConfig(c *configFile) error {
	if !semverRegex.MatchString(c.ConfigVersion) {
		return fmt.Errorf("config_version %q is not semver", c.ConfigVersion)
	}
	if c.ResolverVersion != ResolverVersion {
		return fmt.Errorf("resolver_version mismatch: config=%s compiled=%s",
			c.ResolverVersion, ResolverVersion)
	}
	for id, p := range c.Profiles {
		if err := validateProfile(id, p); err != nil {
			return err
		}
	}
	seenIDs := make(map[string]bool)
	for _, r := range c.ArchetypeRules {
		if seenIDs[r.ID] {
			return fmt.Errorf("duplicate rule id %q", r.ID)
		}
		seenIDs[r.ID] = true
	}
	archetypesInProfiles := make(map[Archetype]bool)
	for _, p := range c.Profiles {
		archetypesInProfiles[p.Archetype] = true
	}
	for _, r := range c.ArchetypeRules {
		if !archetypesInProfiles[r.Archetype] {
			return fmt.Errorf("rule %q references archetype %q with no profile entries",
				r.ID, r.Archetype)
		}
	}
	hasFallback := false
	for _, r := range c.ArchetypeRules {
		if r.IndustryPrefix == "*" {
			hasFallback = true
			break
		}
	}
	if !hasFallback {
		return fmt.Errorf("no fallback rule with industry_prefix=*; spec §4.3 invariant 7")
	}
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

// isValid* helpers are exhaustive case-statements over the declared enum
// values. BACKEND fills these from profile.go's enum constants.
func isValidArchetype(a Archetype) bool {
	switch a {
	case ArchetypeMatureLargeScale, ArchetypeMatureLargeBank, ArchetypeGrowthBank,
		ArchetypeInsuranceCompany, ArchetypeSoftwareLikeLargeScale, ArchetypeSoftwareLikeScaling,
		ArchetypeCyclicalMidCycle, ArchetypeCyclicalTrough, ArchetypeHypergrowthEarly,
		ArchetypeHypergrowthProfitable, ArchetypePreRevenueBiotech,
		ArchetypeMaturingTechDividend, ArchetypeMatureDividendTech,
		ArchetypeREITResidential, ArchetypeREITCommercial, ArchetypeREITIndustrial,
		ArchetypeREITHealthcare, ArchetypeREITDataCenter, ArchetypeREITCellTower,
		ArchetypeREITRetail, ArchetypeREITSpecialty:
		return true
	}
	return false
}

func isValidMaturity(m Maturity) bool {
	switch m {
	case MaturityMature, MaturityStandardGrowth, MaturityHighGrowth:
		return true
	}
	return false
}

func isValidRevenueBaseMethod(m RevenueBaseMethod) bool {
	switch m {
	case RevenueBaseRawTTM, RevenueBaseTwoYearAverage,
		RevenueBaseMaxTTMOrFloor, RevenueBaseMidCycleNormalized:
		return true
	}
	return false
}

func isValidTerminalMethod(m TerminalMethod) bool {
	switch m {
	case TerminalGordonGrowth, TerminalExitMultiple:
		return true
	}
	return false
}

func isValidDiscountMethod(m DiscountMethod) bool {
	switch m {
	case DiscountWACC, DiscountCostOfEquity:
		return true
	}
	return false
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
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile/testhelpers"
)

func TestResolve_JPM_ResolvesToMatureLargeBank(t *testing.T) {
	reg := testhelpers.MustLoadFullFixture(t)
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
	reg := testhelpers.MustLoadFullFixture(t)
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
	reg := testhelpers.MustLoadFullFixture(t)
	revenue := 600e6
	oiNeg := -100e6
	facts := profile.Facts{
		Industry:           "MFG_SEMI",
		IndustryNormalized: "MFG_SEMI",
		Revenue:            &revenue,
		OperatingIncome:    &oiNeg,
	}
	resolved, trace := reg.Resolve(facts)
	require.NotNil(t, resolved)
	assert.Equal(t, profile.ArchetypeCyclicalTrough, resolved.Archetype)
	assert.Contains(t, trace.HumanReason, "cyclical_trough_override")
}

func TestResolve_RuleOrderingDeterministic(t *testing.T) {
	reg := testhelpers.MustLoadFullFixture(t)
	facts := profile.Facts{
		Industry:           "FIN_LARGE_BANK",
		IndustryNormalized: "FIN_LARGE_BANK",
	}
	_, trace := reg.Resolve(facts)
	assert.Equal(t, "fin_large_bank", trace.MatchedRuleID,
		"higher-priority rule must win over fin_generic")
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -run "TestResolve_" ./internal/services/valuation/profile/...
```

Expected: FAIL — no `Resolve` method.

- [ ] **Step 3: Create resolver.go**

```go
package profile

import "strings"

// Resolve performs the 3-stage profile derivation per spec §5.1.
//   Stage 1:  industry → archetype via priority-ordered rule match
//   Stage 1b: cyclical-trough override when OperatingIncome < 0
//   Stage 2:  revenue + YoY growth signals → maturity bucket
//   Stage 3:  archetype-specific maturity overrides
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
	for _, rule := range r.archetypeRules { // sorted desc by Priority at load
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
// require a fixed maturity regardless of Stage 2 output. Critical: the
// JPM bit-for-bit invariant depends on mature_large_bank pinning maturity=mature
// even if threshold drift would suggest otherwise.
func archetypeMaturityPin(arch Archetype) (Maturity, bool) {
	switch arch {
	case ArchetypeMatureLargeBank, ArchetypeMatureLargeScale, ArchetypeMatureDividendTech:
		return MaturityMature, true
	case ArchetypePreRevenueBiotech, ArchetypeHypergrowthEarly:
		return MaturityHighGrowth, true
	case ArchetypeCyclicalTrough:
		return MaturityStandardGrowth, true
	}
	return "", false
}

func isCyclicalMidCycleArchetype(arch Archetype) bool {
	return arch == ArchetypeCyclicalMidCycle
}

func joinReasons(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
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
// internal/services/valuation/models or internal/core/entities — either
// would create the Go import cycle: models → profile → models.
// Spec §2.2, §11 item 7.
func TestImportBoundary_ProfilePackage_DoesNotImportModelsOrEntities(t *testing.T) {
	forbidden := []string{
		"github.com/midas/dcf-valuation-api/internal/services/valuation/models",
		"github.com/midas/dcf-valuation-api/internal/core/entities",
	}

	pkgDir := "."
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
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
					"FORBIDDEN IMPORT in %s: profile package must not import %s",
					e.Name(), bad)
			}
		}
	}
}
```

- [ ] **Step 2: Run to verify pass**

```bash
go test -run TestImportBoundary_ProfilePackage_DoesNotImportModelsOrEntities ./internal/services/valuation/profile/...
```

Expected: PASS (the profile files written so far import only stdlib).

#### Task P0a.6 — Commit Phase P0a

- [ ] **Step 1: Verify full package coverage**

```bash
go test -race -count=1 -cover ./internal/services/valuation/profile/...
```

Expected: All tests pass; coverage ≥90%.

- [ ] **Step 2: Commit**

```bash
git add internal/services/valuation/profile/
git commit -m "$(cat <<'EOF'
feat(profile): add AssumptionProfile types, Facts DTO, Registry interface (Tier 2 P0a)

- profile.go: AssumptionProfile struct (14 fields), Archetype/Maturity/
  RevenueBaseMethod/TerminalMethod/DiscountMethod enums, ResolvedProfile
  with IsLegacyMatureLargeBankDDM() helper
- facts.go: neutral Facts DTO (pointer fields distinguish missing-vs-zero)
- trace.go: ResolutionTrace + AssumptionProfileManifest structs
- version.go: ResolverVersion constant (1.0.0)
- registry.go: Registry interface + jsonRegistry impl + LoadFromJSON
  with SHA-256 config_hash + priority-ordered rule matching (no map iter)
- validation.go: 9 load-time invariants — fail-fast on malformed shipped
  config (distinct from user-data graceful fallback per spec §4.4)
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

- [ ] **Step 3: Verify regression**

```bash
go test -run TestDDM_LegacyPath_BitForBit ./internal/services/valuation/models/...
```

Expected: PASS — legacy DDM untouched.

---

### Phase P0b — JSON content + bundle manifest + service.go wiring + entity helper

**Goal:** Populate `config/assumption_profiles.json`. Add `Bundle.SetAssumptionProfileManifest` method. Add `HistoricalFinancialData.RecentYoYGrowth() *float64` method. Wire `profile.Resolve` into `service.go`. Add `ModelInput.Profile` field. Extend `ModelResult`/`ValuationResult`/`FairValueResponse` with omitempty fields. ALL consumers are no-op until P1-P4.

**Worktree dispatch:** This phase runs in its own git worktree (`worktree-agent-p0b`).

#### Task P0b.1 — Add `Bundle.SetAssumptionProfileManifest` method (Gap #2)

- **Files:**
  - Modify: `internal/observability/artifact/bundle.go`
  - Test: `internal/observability/artifact/bundle_test.go`

- [ ] **Step 1: Write failing test**

Add to `internal/observability/artifact/bundle_test.go`:

```go
func TestBundle_SetAssumptionProfileManifest_WritesJSON(t *testing.T) {
	bundle, dir := newTestBundle(t)
	defer bundle.Close()

	manifest := profile.AssumptionProfileManifest{
		ProfileID:       "mature_large_bank:mature",
		Source:          profile.SourceExplicit,
		ResolverVersion: "1.0.0",
		ConfigVersion:   "1.0.0",
		ConfigHash:      "abcdef0123",
	}
	bundle.SetAssumptionProfileManifest(context.Background(), manifest)

	require.NoError(t, bundle.Close())

	path := filepath.Join(dir, "08-assumption-profile.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"profile_id":"mature_large_bank:mature"`)
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -run TestBundle_SetAssumptionProfileManifest ./internal/observability/artifact/...
```

Expected: FAIL — method undefined.

- [ ] **Step 3: Add the method to bundle.go**

Search `internal/observability/artifact/bundle.go` for the `func (b *Bundle) Snapshot(` method signature (the existing snapshot-writer convention). Add a sibling method:

```go
// SetAssumptionProfileManifest writes the resolved AssumptionProfile +
// resolution trace to the bundle as 08-assumption-profile.json. Used by
// service.go::performValuation to stamp profile-resolution audit data
// into the bundle for replay determinism (spec §3.3, §7.3).
//
// Idempotent: replacing a previously-written manifest overwrites the file.
func (b *Bundle) SetAssumptionProfileManifest(ctx context.Context, manifest profile.AssumptionProfileManifest) {
	if b == nil {
		return
	}
	b.Snapshot(ctx, "assumption.profile.resolved", "08-assumption-profile.json", manifest)
	b.AddSchemaVersion("AssumptionProfileManifest", 1)
}
```

Note: this method delegates to existing `Snapshot` to reuse the file-writing + error-handling path. The schema version is added so future bundle consumers can version-gate.

- [ ] **Step 4: Run to verify pass**

```bash
go test -run TestBundle_SetAssumptionProfileManifest ./internal/observability/artifact/...
```

Expected: PASS.

#### Task P0b.2 — Add `HistoricalFinancialData.RecentYoYGrowth()` method (Gap #3)

- **Files:**
  - Modify: `internal/core/entities/historical_financial.go` (verify exact filename — search for `type HistoricalFinancialData struct`)
  - Test: existing `internal/core/entities/financial_data_test.go` (add a new test case)

- [ ] **Step 1: Write failing test**

Add to the appropriate `_test.go`:

```go
func TestHistoricalFinancialData_RecentYoYGrowth(t *testing.T) {
	cases := []struct {
		name        string
		periods     []*entities.FinancialData
		wantNil     bool
		wantValue   float64
		wantEpsilon float64
	}{
		{
			name: "two_periods_positive_growth",
			periods: []*entities.FinancialData{
				{Revenue: 110_000_000}, // most recent
				{Revenue: 100_000_000},
			},
			wantValue: 0.10, wantEpsilon: 1e-9,
		},
		{
			name: "two_periods_negative_growth",
			periods: []*entities.FinancialData{
				{Revenue: 90_000_000},
				{Revenue: 100_000_000},
			},
			wantValue: -0.10, wantEpsilon: 1e-9,
		},
		{
			name: "one_period_insufficient",
			periods: []*entities.FinancialData{
				{Revenue: 100_000_000},
			},
			wantNil: true,
		},
		{
			name: "zero_prior_revenue",
			periods: []*entities.FinancialData{
				{Revenue: 100_000_000},
				{Revenue: 0},
			},
			wantNil: true, // cannot compute growth from zero base
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &entities.HistoricalFinancialData{AnnualPeriods: tc.periods}
			yoy := h.RecentYoYGrowth()
			if tc.wantNil {
				assert.Nil(t, yoy)
			} else {
				require.NotNil(t, yoy)
				assert.InEpsilon(t, tc.wantValue, *yoy, tc.wantEpsilon)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -run TestHistoricalFinancialData_RecentYoYGrowth ./internal/core/entities/...
```

Expected: FAIL — method undefined.

- [ ] **Step 3: Add the method**

In `internal/core/entities/historical_financial.go` (or wherever `HistoricalFinancialData` lives), add:

```go
// RecentYoYGrowth returns the year-over-year revenue growth between the
// two most recent annual periods. Returns nil if insufficient periods
// (< 2) or if the prior period's revenue is zero (cannot compute growth
// from zero base; this is data-quality issue, not actual zero growth).
//
// Used by service.go::performValuation to populate profile.Facts.RevenueGrowthYoY
// for the resolver's Stage-2 maturity bucketing (per spec §5.1).
func (h *HistoricalFinancialData) RecentYoYGrowth() *float64 {
	if h == nil || len(h.AnnualPeriods) < 2 {
		return nil
	}
	latest := h.AnnualPeriods[0]
	prior := h.AnnualPeriods[1]
	if latest == nil || prior == nil || prior.Revenue == 0 {
		return nil
	}
	yoy := (latest.Revenue - prior.Revenue) / prior.Revenue
	return &yoy
}
```

- [ ] **Step 4: Run to verify pass**

```bash
go test -run TestHistoricalFinancialData_RecentYoYGrowth ./internal/core/entities/...
```

Expected: PASS for all 4 subtests.

#### Task P0b.3 — Populate `config/assumption_profiles.json`

- **Files:**
  - Create: `config/assumption_profiles.json`
  - Test: `internal/services/valuation/profile/config_validation_test.go`

- [ ] **Step 1: Write the production config**

Create `config/assumption_profiles.json` with the full profile catalog. Use the structure from spec §4.1 + §4.2. The file contains:
- `config_version: "1.0.0"`
- `resolver_version: "1.0.0"` (must match `profile.ResolverVersion`)
- `profiles` map with ~18 entries
- `archetype_rules` array (priority-ordered)
- `maturity_thresholds_fallback` object

Initial config ships with just the rows P0a/P0b need:
- `mature_large_bank:mature` (JPM bit-for-bit anchor — `dividend_forecast_horizon: 0`)
- `software_like_scaling:standard_growth` (fallback default)
- Plus the rule `fin_large_bank` and `fallback_default`

P1, P2, P3, P4 each add their owned rows per spec §10.1.

- [ ] **Step 2: Add validation test**

Create `internal/services/valuation/profile/config_validation_test.go`:

```go
package profile_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

func TestRealConfig_LoadsAndValidates(t *testing.T) {
	reg, err := profile.LoadFromJSON("../../../../config/assumption_profiles.json")
	require.NoError(t, err, "production config must validate")
	require.NotEmpty(t, reg.ConfigVersion())
	require.NotEmpty(t, reg.ConfigHash())
}
```

- [ ] **Step 3: Run to verify pass**

```bash
go test -run TestRealConfig_LoadsAndValidates ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P0b.4 — Wire profile.Resolve into service.go + update NewService signature

- **Files:**
  - Modify: `internal/services/valuation/service.go`
  - Modify: `internal/services/valuation/models/router.go` (add `Profile` field)
  - Modify: `internal/core/entities/valuation.go` (extend `ValuationResult`)
  - Modify: `internal/api/v1/handlers/fair_value.go` (extend `FairValueResponse`)
  - Modify: `internal/services/valuation/di/container.go` (or wherever Service is constructed)

- [ ] **Step 1: Add `Profile` field to ModelInput**

In `internal/services/valuation/models/router.go`, locate the `ModelInput` struct and add:

```go
// Profile is the resolved AssumptionProfile from upstream resolution
// (service.go::performValuation). Carries calibration values (horizon,
// caps, terminal method, payout path) for downstream model consumption.
// May be nil only in defensive/test paths; models MUST handle nil by
// falling through to legacy behavior. Spec §2.3, §3.1.
Profile *profile.ResolvedProfile
```

Add the import for `profile` package. Verify build:

```bash
go build ./internal/services/valuation/models/...
```

Expected: clean compile.

- [ ] **Step 2: Extend ModelResult with omitempty fields**

In the same file, extend `ModelResult`:

```go
// Tier 2 additive fields. All omitempty — when zero-valued (legacy path)
// these are omitted from JSON, preserving byte equality with pre-Tier-2
// responses on the legacy DDM path. Populated by P1/P3/P4.
TrailingValue    float64 `json:"trailing_value,omitempty"`
ForwardValue     float64 `json:"forward_value,omitempty"`
HorizonSelected  int     `json:"horizon_selected,omitempty"`
TerminalMultiple float64 `json:"terminal_multiple,omitempty"`
```

- [ ] **Step 3: Extend ValuationResult**

In `internal/core/entities/valuation.go`, locate `type ValuationResult struct` and add:

```go
// Tier 2 additive fields. All omitempty — legacy responses byte-identical.
AssumptionProfile string                    `json:"assumption_profile,omitempty"`
ResolutionTrace   *profile.ResolutionTrace  `json:"resolution_trace,omitempty"`
// DCF diagnostics — populated by P2; declared here for P0b schema-ownership
DCFHorizonYears       int       `json:"dcf_horizon_years,omitempty"`
DCFTerminalMethod     string    `json:"dcf_terminal_method,omitempty"`
DCFTerminalPctOfEV    float64   `json:"dcf_terminal_pct_of_ev,omitempty"`
DCFPerYearPV          []float64 `json:"dcf_per_year_pv,omitempty"`
DCFTerminalGrowthUsed float64   `json:"dcf_terminal_growth_used,omitempty"`
```

Add the import for `profile` package. Verify that `entities` does not have a circular import with `profile`. (It doesn't — `profile` is the package without entities imports; `entities` importing `profile` is the OPPOSITE direction and is allowed if needed. But to be safe, consider keeping `ResolutionTrace` opaque: store the struct value, not the pointer-from-another-package. If you hit a cycle, fall back to `ResolutionTrace map[string]any` with `,omitempty` and let the consumer marshal/unmarshal.)

**Decision P0b.4.a — Cycle prevention:** If `entities` cannot import `profile` (verify by attempting the build), use `map[string]any` for the trace JSON field. The structured fields are populated via the existing `json:"..."` tags when service.go marshals the trace struct to map.

- [ ] **Step 4: Update NewService signature**

Search `internal/services/valuation/service.go` for `func NewService(`. The current signature has ~12 parameters. Add `profileRegistry profile.Registry` as the 13th parameter. The full updated signature:

```go
func NewService(
	financialRepo ports.FinancialDataRepository,
	marketRepo ports.MarketDataRepository,
	macroRepo ports.MacroDataRepository,
	cache ports.CacheRepository,
	dataCleaner datacleaner.DataCleanerService,
	dataFetcher *datafetcher.DataFetcher,
	metricsService ports.MetricsService,
	cfg *config.Config,
	logger *zap.Logger,
	calcEmitter *calclog.Emitter,
	profileRegistry profile.Registry,  // NEW Tier 2 P0b parameter
) *Service {
```

Add `profileRegistry profile.Registry` to the `Service` struct definition. In the `NewService` body, assign `profileRegistry: profileRegistry` to the returned `&Service{...}` literal.

- [ ] **Step 5: Add fx.Provide for the Registry**

Search `internal/services/valuation/di/container.go` (or wherever fx wiring lives — may also be in `cmd/server/main.go`) for the existing `fx.Provide` block. Add:

```go
fx.Provide(func() (profile.Registry, error) {
	return profile.LoadFromJSON("config/assumption_profiles.json")
}),
```

This blocks service startup if the config is malformed (per spec §4.4 fail-fast invariant).

- [ ] **Step 6: Wire profile resolution in performValuation**

Search `internal/services/valuation/service.go::performValuation` for the comment `// Calculate WACC (with CRP for international companies)` — this is the anchor before profile resolution fires.

Insert AFTER the WACC computation block and BEFORE `model := s.modelRouter.SelectModel(...)`:

```go
// Tier 2: resolve the AssumptionProfile from current request facts.
// Pure deterministic resolution; replay determinism preserved.
// Failure mode: unknown industry → conservative fallback (audit field
// surfaces the choice). Malformed config would have failed startup.
revenuePtr := func() *float64 {
	if latestFinancialData.Revenue == 0 && latestFinancialData.NormalizedOperatingIncome == 0 {
		return nil // pre-revenue case; nil distinguishes from explicit zero
	}
	v := latestFinancialData.Revenue
	return &v
}
oiPtr := func() *float64 {
	v := latestFinancialData.OperatingIncome
	return &v
}
facts := profile.Facts{
	Industry:           industry,
	IndustryNormalized: strings.ToUpper(strings.TrimSpace(industry)),
	Revenue:            revenuePtr(),
	OperatingIncome:    oiPtr(),
	RevenueGrowthYoY:   historicalData.RecentYoYGrowth(),
}
resolvedProfile, resolutionTrace := s.profileRegistry.Resolve(facts)

// Stamp profile manifest onto the bundle for replay determinism.
if b := artifact.From(ctx); b != nil {
	b.SetAssumptionProfileManifest(ctx, profile.AssumptionProfileManifest{
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

Then in the subsequent `ModelInput` construction (search for `modelInput := &models.ModelInput{`), add `Profile: resolvedProfile,` to the literal.

After `result := ...` (the model.Calculate call returns the result), stamp the trace onto the response:

```go
result.AssumptionProfile = resolvedProfile.ProfileID
result.ResolutionTrace = &resolutionTrace
```

Add `"strings"` import if not already present.

- [ ] **Step 7: Update existing tests that construct Service**

Search for `NewService(` callers in `_test.go` files. Each must be updated to pass a `profile.Registry` argument. Use `testhelpers.MustLoadFullFixture(t)` from Phase Bootstrap.

- [ ] **Step 8: Run full test suite**

```bash
go test ./... -count=1
```

Expected: All tests pass including:
- `TestDDM_LegacyPath_BitForBit` — still bit-for-bit (no DDM math touched)
- `TestRealConfig_LoadsAndValidates` — production config validates
- All existing valuation tests (with updated Service construction)

#### Task P0b.5 — Verify JPM bit-for-bit + replay regression

- [ ] **Step 1: Run bit-for-bit regression**

```bash
go test -v -run TestDDM_LegacyPath_BitForBit ./internal/services/valuation/models/...
```

Expected: PASS. If FAIL: P0b accidentally touched DDM math; revert and bisect.

- [ ] **Step 2: Run replay regression**

```bash
go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/2026-05-14/
```

Expected: All 10 bundles replay cleanly (additive field appearances are benign).

#### Task P0b.6 — Commit Phase P0b

- [ ] **Step 1: Stage and commit**

```bash
git add config/assumption_profiles.json \
        internal/services/valuation/service.go \
        internal/services/valuation/models/router.go \
        internal/core/entities/valuation.go \
        internal/core/entities/historical_financial.go \
        internal/core/entities/financial_data_test.go \
        internal/services/valuation/di/container.go \
        internal/observability/artifact/bundle.go \
        internal/observability/artifact/bundle_test.go \
        internal/services/valuation/profile/config_validation_test.go
git commit -m "$(cat <<'EOF'
feat(profile): populate assumption_profiles.json + wire bundle manifest (Tier 2 P0b)

- config/assumption_profiles.json: initial config with mature_large_bank
  (the JPM bit-for-bit anchor; horizon=0) + software_like_scaling fallback.
  P1/P2/P3/P4 each add their owned rows downstream per spec §10.1.
- bundle.go: new Bundle.SetAssumptionProfileManifest(ctx, manifest)
  method writes 08-assumption-profile.json with full resolved profile +
  trace for replay determinism (spec §3.3, §7.3).
- historical_financial.go: new RecentYoYGrowth() *float64 method
  computing YoY between two most recent annual periods. Returns nil
  on insufficient data or zero prior revenue. Used to populate
  Facts.RevenueGrowthYoY in the resolver call.
- service.go::performValuation: builds profile.Facts from entities,
  calls profileRegistry.Resolve before router.SelectModel, stamps
  ResolvedProfile onto ModelInput.Profile, writes manifest to artifact
  bundle, sets AssumptionProfile + ResolutionTrace on ValuationResult.
- NewService signature: adds profileRegistry profile.Registry as 13th
  parameter. fx.Provide loads from config/assumption_profiles.json at
  startup; malformed config fails service construction.
- ModelInput: new Profile *profile.ResolvedProfile field.
- ModelResult: 4 new omitempty fields (TrailingValue, ForwardValue,
  HorizonSelected, TerminalMultiple) — populated by P1/P4.
- ValuationResult: 5 omitempty DCF diagnostic fields + AssumptionProfile
  + ResolutionTrace — populated by P2; declared here for ownership.

All new fields omitempty. Legacy mature-large-bank DDM response is
byte-identical to pre-Tier-2 (verified: TestDDM_LegacyPath_BitForBit
still green).

Spec §3.1, §3.3, §7.1, §9.3, §10.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase Pre-P2 — Growth estimator slice extension (Gap #4)

**Goal:** Bump the `ProjectedGrowthRates` slice-length cap from 7 to 10 to support `hypergrowth_profitable` archetype's horizon=10. Single commit; verifies the existing growth estimator still produces correct values when asked for longer horizons.

**Worktree dispatch:** This phase runs in its own git worktree (`worktree-agent-pre-p2`), or folded into the same worktree as P2 since they're tightly coupled.

#### Task PreP2.1 — Locate the slice-length cap

- [ ] **Step 1: Search for the cap constant**

```bash
grep -rn "ProjectedGrowthRates" internal/services/growth/
grep -rn "make(\[\]float64" internal/services/growth/
grep -rn "7" internal/services/growth/estimator.go | grep -v "//"
```

Locate the variable / constant that determines how many growth rates the estimator produces. Likely candidates: `DefaultEstimatorConfig.MaxProjectionYears`, `numStages`, or similar. Read `internal/services/growth/estimator.go` to confirm.

- [ ] **Step 2: Write failing test**

Add to `internal/services/growth/estimator_test.go`:

```go
func TestEstimator_ProducesAtLeast10Stages_WhenConfigured(t *testing.T) {
	cfg := growth.DefaultEstimatorConfig()
	cfg.MaxProjectionYears = 10 // NEW: was probably 7

	est := growth.NewEstimator(cfg, zap.NewNop(), nil)
	result := est.EstimateGrowthRates(
		context.Background(), "TEST",
		nil, // no analyst data
		&growthpkg.CalculationResult{GrowthRate: 0.30, Method: "historical", IsReliable: true},
		0.10, // sustainable growth
	)
	assert.GreaterOrEqual(t, len(result.ProjectedGrowthRates), 10,
		"hypergrowth_profitable archetype needs at least 10 growth stages")
}
```

- [ ] **Step 3: Run to verify failure**

Expected: FAIL — current estimator caps at 7 stages.

- [ ] **Step 4: Implement the change**

In `internal/services/growth/estimator.go` (and `config.go` if applicable):
- Bump the cap constant from 7 to 10 (or make it config-driven if not already)
- Ensure the fade curve still produces sensible values at year 8/9/10 (extend the linear/exponential fade to fill in additional years)

The exact code change depends on the existing implementation. The minimum: change `make([]float64, 7)` (or equivalent) to `make([]float64, cfg.MaxProjectionYears)` and ensure the fade loop iterates accordingly.

- [ ] **Step 5: Run to verify pass**

```bash
go test -run TestEstimator_ProducesAtLeast10Stages_WhenConfigured ./internal/services/growth/...
```

Expected: PASS.

- [ ] **Step 6: Verify existing tests still pass**

```bash
go test -race ./internal/services/growth/...
```

Expected: All existing growth tests still pass (the change is additive).

#### Task PreP2.2 — Commit Pre-P2

- [ ] **Step 1: Commit**

```bash
git add internal/services/growth/estimator.go \
        internal/services/growth/estimator_test.go
git commit -m "$(cat <<'EOF'
feat(growth): extend ProjectedGrowthRates cap from 7 to 10 stages (Tier 2 Pre-P2)

Required by P2's hypergrowth_profitable archetype which uses horizon=10.
Additive change: cap moves from 7 → 10, fade curve extends to fill years
8-10 with linearly-decelerating growth.

Existing growth tests unchanged; no behavior regression for ticker
profiles using horizon ≤ 7.

JPM bit-for-bit DDM regression remains green.

Spec §6.2.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase P1 — RM-3: Forward revenue multiple path (worktree-isolated)

**Goal:** Extend `RevenueMultipleModel.Calculate` with an ADDITIVE forward-projection branch gated on `profile.HorizonYears > 0`. Trailing path (today's behavior) preserved when profile is nil or horizon=0.

**Worktree dispatch:** Own worktree `worktree-agent-rm3`. JSON rows owned: `cyclical_*`, `hypergrowth_early`, `pre_revenue_biotech` profile entries + `mfg_semi`, `health_biotech`, `automotive`, `energy` rules. Struct fields: NONE (declared in P0b).

**Reads:** This phase + spec §6.1 + spec §10.1.

#### Task P1.1 — Create `models/util.go` with `avg` helper

- **Files:**
  - Create: `internal/services/valuation/models/util.go`

- [ ] **Step 1: Create the file**

```go
package models

// avg returns the arithmetic mean of the given slice. Returns 0 if empty.
// Used by RM-3 (P1) and VAL-3 P3 (P4) forward-projection warnings.
func avg(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
```

#### Task P1.2 — Write failing forward-path tests

- **Files:**
  - Modify: `internal/services/valuation/models/revenue_multiple_test.go`

- [ ] **Step 1: Add forward-path tests**

```go
func TestRevenueMultiple_Forward_ProjectsAtHorizon(t *testing.T) {
	input := testhelpers.BuildMXLModelInput(t)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:         "cyclical_trough:standard_growth",
			Archetype:         profile.ArchetypeCyclicalTrough,
			Maturity:          profile.MaturityStandardGrowth,
			HorizonYears:      5,
			CompoundGrowthCap: 3.0,
			RevenueBaseMethod: profile.RevenueBaseMaxTTMOrFloor,
			TerminalMultiple:  4.0,
			DiscountMethod:    profile.DiscountCostOfEquity,
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

func TestRevenueMultiple_NilProfile_FallsThroughToTrailing(t *testing.T) {
	input := testhelpers.BuildMXLModelInput(t)
	input.Profile = nil

	rm := models.NewRevenueMultipleModel(zap.NewNop())
	result, err := rm.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.TrailingValue, 0.0)
	assert.Equal(t, 0.0, result.ForwardValue)
	assert.Equal(t, 0, result.HorizonSelected)
}

func TestRevenueMultiple_ProfileHorizonZero_BehavesLikeNoProfile(t *testing.T) {
	input := testhelpers.BuildMXLModelInput(t)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{HorizonYears: 0},
	}

	rm := models.NewRevenueMultipleModel(zap.NewNop())
	result, err := rm.Calculate(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0.0, result.ForwardValue)
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -run TestRevenueMultiple_Forward ./internal/services/valuation/models/...
```

Expected: FAIL.

#### Task P1.3 — Implement forward path

- [ ] **Step 1: Extend Calculate**

In `internal/services/valuation/models/revenue_multiple.go`, find the line `valuePerShare := equityValue / shares` (search anchor: `valuePerShare := equityValue`). After the existing trailing math computation but BEFORE the final `return &ModelResult{...}`, add:

```go
// RM-3 forward path. Gated on profile.HorizonYears > 0; nil profile or
// horizon == 0 falls through to trailing-only behavior. Spec §6.1.
trailingValue := valuePerShare
forwardValue := 0.0
horizonSelected := 0
terminalMultipleUsed := 0.0

if input.Profile != nil && input.Profile.HorizonYears > 0 {
	p := &input.Profile.AssumptionProfile
	rates := input.GrowthEstimate.ProjectedGrowthRates
	if len(rates) >= p.HorizonYears && input.CostOfEquity > 0 {
		// Apply revenue-base normalization per profile
		revenueBase := normalizeRevenueBase(revenue, p.RevenueBaseMethod, input.HistoricalData)

		// Project revenue forward
		forwardRevenue := revenueBase
		for i := 0; i < p.HorizonYears; i++ {
			forwardRevenue *= 1 + rates[i]
		}

		// Apply terminal multiple
		forwardEV := forwardRevenue * p.TerminalMultiple

		// Discount at cost-of-equity (NOT WACC — relative valuation per RM-3 spec correction)
		if p.DiscountMethod == profile.DiscountCostOfEquity {
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
			fmt.Sprintf("RM-3 forward: %dy projection at avg %.1f%% growth, terminal %.1fx",
				p.HorizonYears, avg(rates[:p.HorizonYears])*100, p.TerminalMultiple))
	}
}
```

Modify the final return to populate the new fields:

```go
return &ModelResult{
	IntrinsicValuePerShare: valuePerShare,
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

Add `normalizeRevenueBase` helper at the bottom of `revenue_multiple.go`:

```go
// normalizeRevenueBase applies the profile-specified normalization to the
// revenue input. Per spec §3.1 RevenueBaseMethod enum:
//   - raw_ttm: use the TTM helper output as-is (default)
//   - two_year_average: avg of most recent + prior year
//   - max_ttm_or_floor: max(TTM, 5y revenue mean) — for cyclical trough
//   - mid_cycle_normalized: 5y revenue mean (mid-cycle estimate)
func normalizeRevenueBase(ttm float64, method profile.RevenueBaseMethod, hist *entities.HistoricalFinancialData) float64 {
	switch method {
	case profile.RevenueBaseTwoYearAverage:
		if hist == nil || len(hist.AnnualPeriods) < 2 {
			return ttm
		}
		return (hist.AnnualPeriods[0].Revenue + hist.AnnualPeriods[1].Revenue) / 2
	case profile.RevenueBaseMaxTTMOrFloor:
		floor := meanRecentRevenue(hist, 5)
		if floor > ttm {
			return floor
		}
		return ttm
	case profile.RevenueBaseMidCycleNormalized:
		return meanRecentRevenue(hist, 5)
	default: // RevenueBaseRawTTM
		return ttm
	}
}

func meanRecentRevenue(hist *entities.HistoricalFinancialData, years int) float64 {
	if hist == nil || len(hist.AnnualPeriods) == 0 {
		return 0
	}
	n := years
	if len(hist.AnnualPeriods) < n {
		n = len(hist.AnnualPeriods)
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += hist.AnnualPeriods[i].Revenue
	}
	return sum / float64(n)
}
```

- [ ] **Step 2: Add `math` import if not present**

Verify the import statement at the top of `revenue_multiple.go` includes `"math"`.

- [ ] **Step 3: Run tests**

```bash
go test -race ./internal/services/valuation/models/...
```

Expected: All forward tests pass + existing tests pass + `TestDDM_LegacyPath_BitForBit` still green.

#### Task P1.4 — Populate P1's JSON rows

- [ ] **Step 1: Edit config/assumption_profiles.json**

Add P1's owned profile entries (per spec §10.1):
- `cyclical_mid_cycle:mature`, `cyclical_mid_cycle:standard_growth`, `cyclical_mid_cycle:high_growth`
- `cyclical_trough:standard_growth`
- `hypergrowth_early:high_growth`
- `pre_revenue_biotech:high_growth`

Each row has all 14 fields per spec §3.1. Suggested values per spec §3.4 + kickoff brief:

```json
"cyclical_trough:standard_growth": {
	"profile_id": "cyclical_trough:standard_growth",
	"archetype": "cyclical_trough",
	"maturity": "standard_growth",
	"horizon_years": 5,
	"compound_growth_cap": 3.0,
	"revenue_base_method": "max_ttm_or_floor",
	"discount_method": "cost_of_equity",
	"terminal_method": "exit_multiple",
	"stabilized": false,
	"fade_years": 2,
	"terminal_multiple": 4.0,
	"dps_growth_cap": 0,
	"payout_path": [],
	"dividend_forecast_horizon": 0,
	"stable_dividend_growth": 0.03
}
```

Plus archetype_rules entries:

```json
{"id":"mfg_semi","priority":90,"industry_prefix":"MFG_SEMI","archetype":"cyclical_mid_cycle"},
{"id":"mfg_generic","priority":50,"industry_prefix":"MFG","archetype":"cyclical_mid_cycle"},
{"id":"health_biotech","priority":90,"industry_prefix":"HEALTH_BIOTECH","archetype":"pre_revenue_biotech"},
{"id":"automotive","priority":80,"industry_prefix":"AUTOMOTIVE","archetype":"cyclical_mid_cycle"},
{"id":"energy","priority":80,"industry_prefix":"ENERGY","archetype":"cyclical_mid_cycle"}
```

- [ ] **Step 2: Verify config still validates**

```bash
go test -run TestRealConfig_LoadsAndValidates ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P1.5 — Add P1's regression pin (use pincapture pattern; Gap #5)

- **Files:**
  - Create: `internal/services/valuation/profile/pin_capture_test.go` (build-tag-gated)
  - Modify: `internal/services/valuation/profile/tier2_regression_test.go`
  - Create: `internal/services/valuation/profile/pins.go` (constants written by pincapture)

- [ ] **Step 1: Write the pin-capture helper (build-tag-gated)**

Create `internal/services/valuation/profile/pin_capture_test.go`:

```go
//go:build pincapture

package profile_test

import (
	"fmt"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile/testhelpers"
)

// TestCapturePins is a one-shot helper run with `-tags pincapture` to
// print actual values for the Tier 2 regression pins. The agent pastes
// the printed values into pins.go.
//
// Workflow:
//   1. Run: `go test -tags pincapture -run TestCapturePins ./internal/services/valuation/profile/... -v`
//   2. Copy printed lines into internal/services/valuation/profile/pins.go
//   3. Re-run TestTier2_*_Pin tests (without -tags) to verify they pass against the pinned values
func TestCapturePins(t *testing.T) {
	tickers := []string{"MXL", "JPM", "EQIX", "PLD"}
	for _, ticker := range tickers {
		result := testhelpers.RunValuation(t, ticker)
		fmt.Printf("expected%sPrimaryValue   = %.15g\n", ticker, result.IntrinsicValuePerShare)
		// For models with TrailingValue/ForwardValue populated:
		// fmt.Printf("expected%sForwardValue   = %.15g\n", ticker, result.ForwardValue)
	}
}
```

- [ ] **Step 2: Run pin-capture to get actual values**

After P1 implementation is complete:

```bash
go test -tags pincapture -run TestCapturePins ./internal/services/valuation/profile/... -v
```

Expected: prints lines like `expectedMXLPrimaryValue   = 107.234567890123`.

- [ ] **Step 3: Create pins.go with the captured values**

Create `internal/services/valuation/profile/pins.go` (NOT a `_test.go` file so it can be imported by the regression test):

```go
package profile

// Captured pre-Tier-2 expected values for the cross-model regression
// suite. Regenerate by running `go test -tags pincapture` per the
// TestCapturePins helper. Spec §8.2.
const (
	ExpectedMXLPrimaryValue  = 107.234567890123 // CAPTURED VALUE — paste from pincapture run
	ExpectedJPMPrimaryValue  = 198.456789012345 // CAPTURED VALUE
	ExpectedEQIXPrimaryValue = 845.678901234567 // CAPTURED VALUE (populated by P4)
	ExpectedPLDPrimaryValue  = 125.890123456789 // CAPTURED VALUE (populated by P4)
)
```

- [ ] **Step 4: Add P1's pin to tier2_regression_test.go**

```go
func TestTier2_MXL_Pin(t *testing.T) {
	result := testhelpers.RunValuation(t, "MXL")
	assert.Equal(t, "cyclical_trough:standard_growth", result.AssumptionProfile)
	assert.Equal(t, "revenue_multiple", result.ChosenModel)
	assert.InEpsilon(t, profile.ExpectedMXLPrimaryValue, result.IntrinsicValuePerShare, 1e-9)
}
```

- [ ] **Step 5: Verify pin passes**

```bash
go test -run TestTier2_MXL_Pin ./internal/services/valuation/profile/...
```

Expected: PASS.

#### Task P1.6 — Commit P1

- [ ] **Step 1: Full regression run**

```bash
go test -race -count=1 ./...
go run ./cmd/replay --diff-stages artifacts/tier2-baseline/2026-05-14/MXL/
```

- [ ] **Step 2: Commit**

```bash
git add internal/services/valuation/models/revenue_multiple.go \
        internal/services/valuation/models/revenue_multiple_test.go \
        internal/services/valuation/models/util.go \
        config/assumption_profiles.json \
        internal/services/valuation/profile/pin_capture_test.go \
        internal/services/valuation/profile/pins.go \
        internal/services/valuation/profile/tier2_regression_test.go
git commit -m "$(cat <<'EOF'
feat(valuation): RM-3 forward revenue multiple path (Tier 2 P1)

- revenue_multiple.go: additive forward path gated on Profile.HorizonYears>0.
  Computes forward revenue projection × terminal multiple discounted at
  cost-of-equity (NOT WACC — relative valuation). Trailing preserved
  when profile is nil or HorizonYears==0.
- normalizeRevenueBase + meanRecentRevenue helpers support 4 base methods:
  raw_ttm, two_year_average, max_ttm_or_floor (cyclical trough),
  mid_cycle_normalized.
- models/util.go: avg() helper (package-scoped; reused by P4).
- assumption_profiles.json: P1's owned rows added (4 cyclical profiles +
  hypergrowth_early + pre_revenue_biotech + 5 archetype rules).
- pin_capture_test.go: build-tag-gated TestCapturePins helper. Run with
  `-tags pincapture` to regenerate pins.go.
- pins.go: ExpectedMXLPrimaryValue constant for regression pinning.
- tier2_regression_test.go: TestTier2_MXL_Pin asserts profile + model +
  primary value within ε=1e-9.

JPM bit-for-bit DDM regression remains green.

Closes RM-3. Spec §6.1, §10.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase P2 — VAL-1: DCF archetype-aware horizon + diagnostics (worktree-isolated)

**Goal:** Replace hard-coded 7y DCF horizon with profile-driven horizon. Add 5 diagnostic fields to `ValuationResult`. Per spec §6.2.

**Worktree dispatch:** Own worktree `worktree-agent-val1`. JSON rows owned: `mature_large_scale`, `software_like_large_scale:*`, `software_like_scaling:high_growth`, `hypergrowth_profitable:*` profiles + `tech_saas` rule. Struct fields: populates 5 fields P0b declared on `ValuationResult`.

**Reads:** This phase + spec §6.2 + spec §10.1.

#### Task P2.1 — Failing tests for archetype-driven horizon

- [ ] **Step 1: Add to service_test.go**

```go
func TestService_DCF_HorizonFromProfile_MatureLargeScale_3y(t *testing.T) {
	svc := testhelpers.BuildTestServiceWithFixedProfile(t, "mature_large_scale:mature")
	result, err := svc.CalculateValuation(context.Background(), "KO", nil)
	require.NoError(t, err)
	assert.Equal(t, 3, result.DCFHorizonYears)
}

func TestService_DCF_HorizonFromProfile_HypergrowthProfitable_10y(t *testing.T) {
	svc := testhelpers.BuildTestServiceWithFixedProfile(t, "hypergrowth_profitable:high_growth")
	result, err := svc.CalculateValuation(context.Background(), "NVDA", nil)
	require.NoError(t, err)
	assert.Equal(t, 10, result.DCFHorizonYears)
}

func TestService_DCF_TerminalPctOfEV_FlaggedWhenExceedsThreshold(t *testing.T) {
	svc := testhelpers.BuildTestServiceWithFixedProfile(t, "hypergrowth_profitable:high_growth")
	result, err := svc.CalculateValuation(context.Background(), "NVDA", nil)
	require.NoError(t, err)
	if result.DCFTerminalPctOfEV > 0.80 {
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

- [ ] **Step 2: Run to verify failure**

```bash
go test -run TestService_DCF_ ./internal/services/valuation/...
```

Expected: FAIL — `DCFHorizonYears` always 0.

#### Task P2.2 — Implement profile-driven horizon + 5 diagnostic fields

- [ ] **Step 1: Modify DCF block in performValuation**

Search `internal/services/valuation/service.go::performValuation` for the comment `// Multi-stage growth curve` or the variable `terminalGrowth` (the existing DCF body's anchors). The hard-coded horizon is likely `len(growthEstimate.ProjectedGrowthRates)` or an explicit `7`.

Replace:

```go
// Tier 2 VAL-1: DCF horizon driven by AssumptionProfile.
// Defaults to legacy 7y when profile is nil (defensive).
horizon := 7
terminalMethod := "gordon_growth"
if resolvedProfile != nil && resolvedProfile.HorizonYears > 0 {
	horizon = resolvedProfile.HorizonYears
	terminalMethod = string(resolvedProfile.TerminalMethod)
}

// Per-year PV slice for diagnostics
perYearPV := make([]float64, horizon)
for i := 0; i < horizon; i++ {
	// ... existing per-year PV computation, indexed by i ...
	perYearPV[i] = yearPV
}

// Compute terminal PV via existing terminal-value logic
// ... existing terminal computation ...

terminalPctOfEV := terminalPV / enterpriseValue

// Stamp diagnostics onto result
result.DCFHorizonYears = horizon
result.DCFTerminalMethod = terminalMethod
result.DCFTerminalPctOfEV = terminalPctOfEV
result.DCFPerYearPV = perYearPV
result.DCFTerminalGrowthUsed = terminalGrowthClamped

// >80% terminal-dominance warning
if terminalPctOfEV > 0.80 {
	result.Warnings = append(result.Warnings,
		fmt.Sprintf("terminal_dominance: terminal_pv is %.1f%% of EV (>80%% threshold; consider longer horizon)",
			terminalPctOfEV*100))
}
```

- [ ] **Step 2: Run tests**

```bash
go test -race ./internal/services/valuation/...
```

Expected: New tests pass + existing tests pass + `TestDDM_LegacyPath_BitForBit` still green (no DDM code touched).

#### Task P2.3 — Populate P2's JSON rows

Edit `config/assumption_profiles.json` adding P2's owned rows per spec §10.1:
- `mature_large_scale:mature`
- `software_like_large_scale:mature`, `software_like_large_scale:standard_growth`, `software_like_large_scale:high_growth`
- `software_like_scaling:high_growth` (the standard_growth row is owned by P0a/P0b)
- `hypergrowth_profitable:high_growth`

Plus rules: `tech_saas`, `tech_generic`, `retail_consumer`.

#### Task P2.4 — Commit P2

- [ ] **Commit**

```bash
git add internal/services/valuation/service.go \
        internal/services/valuation/service_test.go \
        config/assumption_profiles.json
git commit -m "$(cat <<'EOF'
feat(valuation): VAL-1 DCF archetype-aware horizon + diagnostics (Tier 2 P2)

- service.go DCF body: horizon now profile-driven (3y mature / 5y standard /
  7y high-growth / 10y hypergrowth). Legacy 7y preserved when profile nil.
- 5 new ValuationResult diagnostic fields populated: DCFHorizonYears,
  DCFTerminalMethod, DCFTerminalPctOfEV (with >80% sanity warning),
  DCFPerYearPV (chart-friendly), DCFTerminalGrowthUsed.
- assumption_profiles.json: P2's owned rows added.

JPM bit-for-bit DDM regression remains green.

Closes VAL-1 Phases 1+2. Phases 3-5 (cyclical-base, exit-multiple
terminal, diluted-share forward) tracked as VAL-1.1 follow-up.
Spec §6.2, §10.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase P3 — VAL-2: DDM multi-stage with bit-for-bit legacy preservation (worktree-isolated; LOAD-BEARING)

**Goal:** Add multi-stage DDM path for non-mature dividend payers. **Legacy single-stage Gordon path MUST stay byte-identical.** Use PATH DUPLICATION, NOT function extraction. Spec §6.3, §7.1.

**Worktree dispatch:** Own worktree `worktree-agent-val2`. JSON rows owned: `mature_large_bank:mature` (bit-for-bit anchor; already in P0b), `growth_bank:*`, `insurance_company:*`, `maturing_tech_first_dividend:*`, `mature_dividend_tech:*` profiles + `fin_small_bank`, `insurance` rules.

**CRITICAL discipline:** Existing `Calculate` body MUST NOT be refactored. Add dispatch as a wrapper; legacy body becomes `calculateLegacyGordon` whose source code is BYTE-IDENTICAL to today's `Calculate`. No reordering. No closure extraction.

**Reads:** This phase + spec §6.3 + spec §7.1 + spec §10.1.

#### Task P3.1 — Capture legacy DDM source from master HEAD `0324057`

- [ ] **Step 1: Extract the byte-identical reference**

```bash
git show 0324057:internal/services/valuation/models/ddm.go > /tmp/ddm-pre-tier2.go
diff /tmp/ddm-pre-tier2.go internal/services/valuation/models/ddm.go
```

Expected: identical (we're still at master 0324057 or close to it). If they differ: the file moved or was modified; investigate before proceeding.

#### Task P3.2 — Write failing multi-stage test

- [ ] **Step 1: Add to ddm_test.go**

```go
func TestDDM_MultiStage_AAPLishProfile_HigherThanSingleStage(t *testing.T) {
	input := testhelpers.BuildSyntheticAAPLishModelInput(t)
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

	assert.Greater(t, result.IntrinsicValuePerShare, 0.0)
	assert.Equal(t, 10, result.HorizonSelected)
}
```

- [ ] **Step 2: Run to verify failure**

Expected: FAIL.

#### Task P3.3 — Add dispatcher + sibling multi-stage function

**CRITICAL: do not refactor the existing body. Lift the existing `Calculate` content into `calculateLegacyGordon` as a verbatim paste.**

- [ ] **Step 1: Rename existing Calculate to calculateLegacyGordon and add dispatcher**

In `internal/services/valuation/models/ddm.go`:

1. Rename the existing `func (m *DDMModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error)` to `func (m *DDMModel) calculateLegacyGordon(ctx context.Context, input *ModelInput) (*ModelResult, error)` — change ONLY the function name; the body stays byte-identical.

2. Add a NEW `Calculate` method as the dispatcher:

```go
// Calculate is the Tier 2 dispatcher. Routes legacy mature-large-bank
// requests to the verbatim-preserved single-stage Gordon path; routes
// multi-stage profiles to calculateMultiStage. Spec §7.1.
func (m *DDMModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
	// Defensive: nil profile or legacy-mature-bank → legacy path
	if input != nil && input.Profile.IsLegacyMatureLargeBankDDM() {
		return m.calculateLegacyGordon(ctx, input)
	}
	if input == nil || input.Profile == nil || input.Profile.DividendForecastHorizon == 0 {
		return m.calculateLegacyGordon(ctx, input)
	}
	return m.calculateMultiStage(ctx, input)
}
```

- [ ] **Step 2: Add calculateMultiStage as a sibling function**

```go
// calculateMultiStage is the Tier 2 multi-stage DDM path for non-mature
// dividend payers. Used when profile.DividendForecastHorizon > 0.
// Spec §6.3.
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

	growthRates := input.GrowthEstimate.ProjectedGrowthRates
	if len(growthRates) < horizon {
		return nil, fmt.Errorf("ddm_multistage: growth horizon %d shorter than profile %d",
			len(growthRates), horizon)
	}

	explicitPV := 0.0
	projectedDPS := dps
	discount := 1.0
	for i := 0; i < horizon; i++ {
		g := growthRates[i]
		if g > p.DPSGrowthCap && p.DPSGrowthCap > 0 {
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

- [ ] **Step 3: Verify JPM bit-for-bit holds (CRITICAL)**

```bash
go test -v -run TestDDM_LegacyPath_BitForBit ./internal/services/valuation/models/...
```

Expected: PASS. If FAIL: the legacy body was modified during the rename. Compare:

```bash
git show 0324057:internal/services/valuation/models/ddm.go | sed -n '/^func (m \*DDMModel) Calculate/,/^}/p' > /tmp/legacy-body.go
grep -A 200 "func (m \*DDMModel) calculateLegacyGordon" internal/services/valuation/models/ddm.go | head -200 > /tmp/new-body.go
diff /tmp/legacy-body.go /tmp/new-body.go
```

The diff must show ONLY the function-name change; everything else identical.

- [ ] **Step 4: Run multi-stage test**

```bash
go test -run TestDDM_MultiStage_AAPLish ./internal/services/valuation/models/...
```

Expected: PASS.

#### Task P3.4 — Populate P3's JSON rows + regression pin

- [ ] **Step 1: Add to config/assumption_profiles.json**

P3's owned rows per spec §10.1:
- `growth_bank:standard_growth`, `growth_bank:high_growth`
- `insurance_company:mature`, `insurance_company:standard_growth`
- `maturing_tech_first_dividend:standard_growth`
- `mature_dividend_tech:mature`

Plus rules: `fin_small_bank`, `insurance`.

- [ ] **Step 2: Add JPM regression pin**

In `internal/services/valuation/profile/tier2_regression_test.go`:

```go
func TestTier2_JPM_Pin_BitForBit(t *testing.T) {
	result := testhelpers.RunValuation(t, "JPM")
	assert.Equal(t, "mature_large_bank:mature", result.AssumptionProfile)
	assert.Equal(t, "ddm", result.ChosenModel)

	expected := testhelpers.LoadGoldenJPMPrimaryValue(t)
	assert.Equal(t,
		math.Float64bits(expected),
		math.Float64bits(result.IntrinsicValuePerShare),
		"JPM IntrinsicValuePerShare must be bit-for-bit identical to pre-Tier-2")
}
```

#### Task P3.5 — Commit P3

```bash
git add internal/services/valuation/models/ddm.go \
        internal/services/valuation/models/ddm_test.go \
        config/assumption_profiles.json \
        internal/services/valuation/profile/tier2_regression_test.go
git commit -m "$(cat <<'EOF'
feat(valuation): VAL-2 DDM multi-stage path (legacy preserved bit-for-bit) (Tier 2 P3)

- ddm.go: Calculate becomes thin dispatcher. Legacy single-stage Gordon
  body renamed to calculateLegacyGordon (BYTE-IDENTICAL — verified via
  git show 0324057 diff; only function-name change).
- calculateMultiStage: NEW sibling function for non-mature dividend
  payers. Consumes profile.DividendForecastHorizon + PayoutPath +
  DPSGrowthCap + StableDividendGrowth. Discounts at cost-of-equity.
- assumption_profiles.json: P3's owned rows added.
- tier2_regression_test.go: TestTier2_JPM_Pin_BitForBit asserts
  Float64bits equality on IntrinsicValuePerShare.

LOAD-BEARING INVARIANT VERIFIED: JPM/BAC/WFC pre-Tier-2 golden outputs
match post-Tier-2 byte-for-byte (TestDDM_LegacyPath_BitForBit green).

Closes VAL-2 Phases 1-3. Spec §6.3, §7.1, §10.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase P4 — VAL-3 P3: Forward FFO projection (worktree-isolated)

**Goal:** Extend `FFOModel.Calculate` with additive forward path gated on `profile.HorizonYears > 0`. Subsector multiples (VAL-3 P1+P4 already shipped) continue to apply on both paths. Spec §6.4.

**Worktree dispatch:** Own worktree `worktree-agent-val3p3`. JSON rows owned: all 7 `reit_*` profile entries + all `reit_*` rules per spec §10.1.

**Reads:** This phase + spec §6.4 + spec §10.1.

#### Task P4.1 — Failing forward FFO tests

```go
func TestFFO_Forward_DataCenterREIT_HigherThanTrailing(t *testing.T) {
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:        "reit_datacenter:high_growth",
			Archetype:        profile.ArchetypeREITDataCenter,
			Maturity:         profile.MaturityHighGrowth,
			HorizonYears:     5,
			TerminalMultiple: 28.0,
			DiscountMethod:   profile.DiscountCostOfEquity,
		},
	}

	ffo := models.NewFFOModel(zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)

	assert.Greater(t, result.TrailingValue, 0.0)
	assert.Greater(t, result.ForwardValue, 0.0)
	assert.Greater(t, result.ForwardValue, result.TrailingValue,
		"data center REIT forward should exceed trailing given high-growth profile")
	assert.Equal(t, 5, result.HorizonSelected)
}

func TestFFO_NilProfile_FallsThroughToTrailing(t *testing.T) {
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	input.Profile = nil
	ffo := models.NewFFOModel(zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0.0, result.ForwardValue)
}

func TestFFO_ProfileHorizonZero_BehavesLikeNoProfile(t *testing.T) {
	input := testhelpers.BuildSyntheticDataCenterREITInput(t)
	input.Profile = &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{HorizonYears: 0},
	}
	ffo := models.NewFFOModel(zap.NewNop())
	result, err := ffo.Calculate(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, 0.0, result.ForwardValue)
}
```

#### Task P4.2 — Implement forward FFO path

In `internal/services/valuation/models/ffo.go::Calculate`, search for `valuePerShare := ffoPerShare * pffoMultiple`. After that line and BEFORE the equity-bridge computation, add:

```go
// VAL-3 P3 forward path. Gated on profile.HorizonYears > 0. Spec §6.4.
trailingValue := valuePerShare
forwardValue := 0.0
horizonSelected := 0
terminalMultipleUsed := 0.0

if input.Profile != nil && input.Profile.HorizonYears > 0 {
	p := &input.Profile.AssumptionProfile
	rates := input.GrowthEstimate.ProjectedGrowthRates
	if len(rates) >= p.HorizonYears && input.CostOfEquity > 0 {
		// Project FFO/share forward using engine growth (revenue growth as
		// FFO-growth proxy per spec §6.4 Option A).
		forwardFFOPerShare := ffoPerShare
		for i := 0; i < p.HorizonYears; i++ {
			forwardFFOPerShare *= 1 + rates[i]
		}

		// Apply terminal P/FFO multiple
		forwardValuePreDiscount := forwardFFOPerShare * p.TerminalMultiple

		// Discount at cost-of-equity (NOT WACC — VAL-3 spec correction)
		discount := math.Pow(1+input.CostOfEquity, float64(p.HorizonYears))
		forwardValue = forwardValuePreDiscount / discount

		if forwardValue < 0 {
			forwardValue = 0
		}

		horizonSelected = p.HorizonYears
		terminalMultipleUsed = p.TerminalMultiple

		warnings = append(warnings,
			fmt.Sprintf("VAL-3 P3 forward FFO: %dy at avg %.1f%% growth, terminal %.1fx P/FFO",
				p.HorizonYears, avg(rates[:p.HorizonYears])*100, p.TerminalMultiple))
	}
}
```

Modify the final return:

```go
return &ModelResult{
	IntrinsicValuePerShare: valuePerShare,
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

Add `"math"` to imports.

#### Task P4.3 — Populate JSON rows

Add 7 REIT subsector profiles to `config/assumption_profiles.json` (residential, commercial, industrial, healthcare, datacenter, celltower, retail) — each with the maturity variants that exist in practice (typically `standard_growth` for stable subsectors, `standard_growth` + `high_growth` for emerging ones). ~12 rows total. Plus 7 `reit_*` rules at priority 100.

#### Task P4.4 — Regression pins

Run `go test -tags pincapture` to capture `ExpectedEQIXPrimaryValue` and `ExpectedPLDPrimaryValue`. Add to `pins.go`. Add `TestTier2_EQIX_Pin` and `TestTier2_PLD_Pin` to `tier2_regression_test.go`.

#### Task P4.5 — Commit P4

```bash
git add internal/services/valuation/models/ffo.go \
        internal/services/valuation/models/ffo_test.go \
        config/assumption_profiles.json \
        internal/services/valuation/profile/pins.go \
        internal/services/valuation/profile/tier2_regression_test.go
git commit -m "$(cat <<'EOF'
feat(valuation): VAL-3 P3 forward FFO projection (Tier 2 P4)

- ffo.go: additive forward path gated on Profile.HorizonYears > 0.
  Projects FFO per-share forward using engine growth curve, applies
  terminal P/FFO multiple, discounts at cost-of-equity. Trailing
  preserved when profile is nil or HorizonYears==0.
- assumption_profiles.json: 7 REIT subsector profiles (~12 rows) +
  7 reit_* rules at priority 100.
- pins.go: ExpectedEQIXPrimaryValue + ExpectedPLDPrimaryValue.
- tier2_regression_test.go: EQIX (datacenter, high_growth, horizon=5)
  and PLD (industrial, standard_growth, horizon=3) pins.

JPM bit-for-bit DDM regression remains green.

Closes VAL-3 Phase 3 only. Spec §6.4, §10.1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Phase Closeout — Integration gate + tracker archival + version bump

After P1+P2+P3+P4 all merge to master:

#### Task Z.1 — Integration gate

```bash
go test ./... -count=1 -race
go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/2026-05-14/

# Live API regression on 10-ticker basket
go build -o bin/midas-server ./cmd/server
./bin/midas-server &
for TICKER in AAPL MSFT JPM KO F MXL NVDA AMD EQIX PLD; do
  curl -s -H "X-API-Key: $DEMO_KEY" "http://localhost:8080/api/v1/fair-value/$TICKER" | \
    jq -r "\"$TICKER → profile=\(.assumption_profile) horizon=\(.dcf_horizon_years // .horizon_selected // 0)\""
done
pkill -f midas-server
```

Expected: 47/47 packages green, replay clean, live API matches per-stream pinned values.

#### Task Z.2 — Archive trackers

```bash
git mv docs/reviewer/RM-3-forward-revenue-multiple-model.md docs/reviewer/archive/
git mv docs/reviewer/VAL-1-dcf-model-archetype-aware-horizon-and-normalization.md docs/reviewer/archive/
git mv docs/reviewer/VAL-2-ddm-multistage-and-cost-of-equity-discipline.md docs/reviewer/archive/
git mv docs/reviewer/VAL-3-ffo-affo-subsector-multiples-and-forward-projection.md docs/reviewer/archive/
git commit -m "docs(reviewer): archive 4 Tier-2 trackers — RM-3 + VAL-1 + VAL-2 + VAL-3 P3 closed"
```

#### Task Z.3 — CalculationVersion bump

```bash
grep -rn "CalculationVersion" internal/ --include="*.go"
# Edit the constant from 4.1 → 4.2
git commit -m "feat(valuation): bump CalculationVersion 4.1 → 4.2 (Tier 2 close)"
```

---

## 4. Test Plan & Coverage Gates

| Surface | Target | Verifier |
|---|---|---|
| `internal/services/valuation/profile/` | ≥90% | `go test -cover ./internal/services/valuation/profile/...` |
| `internal/services/valuation/models/` | ≥90% (maintained from 93.6%) | `go test -cover ./internal/services/valuation/models/...` |
| `internal/services/valuation/` package-level | ≥92% (up from 89.7%) | `go test -cover ./internal/services/valuation/...` |
| JPM/BAC/WFC bit-for-bit | exact `Float64bits` equality | `TestDDM_LegacyPath_BitForBit` |
| 10-ticker basket regression | 6-field pinning per spec §8.2 | `TestTier2_*_Pin` family |
| Replay determinism | numerical identity | `cmd/replay --diff-stages` |
| Import boundary | no models/entities in profile/ | `TestImportBoundary_*` |
| Resolver determinism | pure function | `TestResolve_*` |
| Config validation | fail-fast on malformed | `TestLoadFromJSON_*` |

---

## 5. Done-When

- [ ] All 47 packages green under `go test ./... -race -count=1`
- [ ] `TestDDM_LegacyPath_BitForBit` passes for JPM/BAC/WFC
- [ ] 10-ticker basket regression passes (`TestTier2_*_Pin` family)
- [ ] `cmd/replay --diff-stages artifacts/tier2-baseline/` runs clean
- [ ] 4 trackers archived
- [ ] CalculationVersion 4.1 → 4.2
- [ ] `profile/` package coverage ≥90%
- [ ] No `time.Now()` outside consumer layer
- [ ] `pkg/finance/*` unchanged
- [ ] Import boundary intact (profile package has no models/entities imports)

---

## 6. Risks

### R-1: JPM bit-for-bit breaks during P3

**Probability:** Medium. The byte-identical legacy-body discipline is unusual.

**Mitigation:** Bootstrap captures golden fixtures. P3 verifies AFTER the rename via the bit-for-bit test. If fails: `diff` against `git show 0324057:...` to locate the unintended change.

### R-2: 4 parallel worktrees create merge conflicts on assumption_profiles.json

**Probability:** Low with ownership table; High otherwise.

**Mitigation:** Spec §10.1 maps every JSON key to one owning stream. Rebase-before-merge for whichever stream merges second.

### R-3: NewService signature change cascades through test setup

**Probability:** Medium — `NewService` is constructed in ~5-8 test files.

**Mitigation:** P0b explicitly searches `_test.go` callers and updates them all in the same commit. The `testhelpers.BuildTestService` helper insulates downstream tests from future signature changes.

### R-4: Growth estimator slice extension breaks existing tests

**Probability:** Low — change is additive.

**Mitigation:** Pre-P2 runs full `go test ./internal/services/growth/...` before commit.

### R-5: One human reviewer serializes V/R/Q across 4 parallel worktrees

**Probability:** High — structural limitation.

**Mitigation:** Implementation is parallel; review is serial. Calendar benefit is real but smaller than 4× — closer to 2-3×. Acknowledge in retrospect.

---

## 7. Spec Updates

After Tier 2 ships: update `docs/refactoring/spec/assumption-profile-spec.md` §15 (commit estimates) with actuals. File follow-up trackers in `docs/reviewer/` for any V/R/Q-surfaced issues.

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

Each P1-P4 BACKEND agent receives:
- This implementation plan filename
- The single phase section relevant to their stream
- Spec §6.X (their model section) + §10.1 (ownership table)
- The 4 modified-file paths owned by their stream
- For P3 only: the `git show 0324057:...` command for byte-identical paste

Agents do NOT receive:
- Other streams' plan sections
- Other streams' JSON row ownership
- Other streams' regression pins

This keeps context focused and prevents cross-stream interference.
