# DC-1 Phase 0 — Plug Fields and Entity Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add four "Other*" plug fields to `entities.FinancialData` and have the SEC parser compute them so components always sum exactly to their balance-sheet umbrellas, with zero downstream behavior change.

**Architecture:** Plugs are computed in pure Go at the end of `parsePeriodData` (post `findValue` extraction, post multi-currency dominant-currency collapse) by subtracting the sum of known components from each umbrella. Negative residuals are clamped to zero (with a debug log) and any missing umbrella leaves the corresponding plug at 0. This sits entirely inside the SEC parser — no datacleaner code changes, no consumer migration, no new XBRL extraction.

**Tech Stack:** Go 1.23 (toolchain 1.24.4); `go.uber.org/zap` for structured logs; `github.com/leanovate/gopter` for property-based testing; `github.com/stretchr/testify` for asserts. Foreground packages: `internal/core/entities`, `internal/infra/gateways/sec`.

---

## Mode

MODE: PLAN_AND_CREATE
ROLE: ARCH

## Summary

Phase 0 of the DC-1 datacleaner refactor (see `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`). We add the structural primitive that every later phase depends on — the entity-level guarantee that **components sum to umbrellas** — without touching any cleaner, valuation, or consumer code. The four new fields are `OtherCurrentAssets`, `OtherNonCurrentAssets`, `OtherCurrentLiabilities`, `OtherNonCurrentLiabilities`. They are computed once per period at parse time and are pure residual plugs (umbrella − sum(known components)).

**Phase 0 explicitly does NOT:**
- Introduce any view types (`CleanedFinancialData`, `Restated()`, `InvestedCapital()`) — those are Phase 3.
- Change any consumer's read site — all 13 consumer sites in the spec stay as-is until Phase 4.
- Add the `Adjuster` interface, `LedgerEntry`, `OverlaySpec` types — those are Phase 2.
- Add the `recomputeUmbrellas()` shim — that is Phase 1.
- Change DCF, WACC, Graham, NCAV, FFO, DDM, or RevenueMultiple output values for ANY ticker in the basket.
- Add new XBRL extraction tags (the four plugs are pure residuals; no `findValue` lookup of `OtherAssetsCurrent` or similar).
- Change the OpenAPI public contract (`FairValueResponse` does not surface `FinancialData` fields; OpenAPI is untouched).

---

## Requirements

### Functional

1. `entities.FinancialData` gains four `float64` fields with snake_case JSON tags: `other_current_assets`, `other_non_current_assets`, `other_current_liabilities`, `other_non_current_liabilities`.
2. `parsePeriodData` in `internal/infra/gateways/sec/parser.go` populates the four fields at the end of the function (after every existing `findValue`/`sumValues` call, after the missing-fields check, but before the final `return financialData, nil`).
3. Plugs are computed as residuals: `OtherCurrentAssets = max(0, CurrentAssets − (CashAndCashEquivalents + Inventory))`; same shape for the other three (see Task 0.2 for the exact decomposition).
4. When an umbrella is zero or missing (`CurrentAssets == 0`, `TotalAssets == 0`, etc.) the corresponding plug stays at 0 — no negative residuals.
5. A property test asserts the algebraic invariant: for any random `FinancialData` produced by the SEC parser, `umbrella ≥ sum(known components)` and `plug == max(0, umbrella − sum(known components))` for all four pairs.
6. An integration test runs the parser against fixture SEC responses for the 10-ticker basket (AAPL, MSFT, JNJ, KO, F, AMD, MXL, TSM, BABA, EQIX) and asserts plugs are non-negative and umbrellas balance.
7. All existing tests in `./...` stay green — zero behavior change in any consumer.

### Non-functional

- **Zero behavior change in DCF output** for the ticker basket. The four new fields are read by no consumer in Phase 0, so DCF / WACC / Graham / NCAV outputs are bit-for-bit identical to today's master.
- **No performance regression.** The plug computation is four float64 subtractions per period; parser p99 latency must not move (measure: existing `service_bench_test.go` runs of the valuation path are unaffected; SEC parse is microseconds per period).
- **Determinism.** Plugs are pure functions of already-extracted facts; no clock, RNG, or external I/O.
- **Multi-currency safety.** Plugs are computed AFTER `extractFiscalPeriods` resolves the dominant currency and collapses `valuesByCurrency` into `payload.values` (see `parser.go:309-363`), so all four umbrellas and all components are in the same currency before subtraction.

### Constraints

- Must use the existing `findValue`-derived field values on the `*FinancialData` struct — no new XBRL tag list, no new lookups against `data` (the raw concept map).
- Must handle IFRS-full filers (TSM, ASML, BABA, NVO, AZN, SAP) identically to US-GAAP filers, because by the time `parsePeriodData` runs, IFRS-full taxonomy concepts have already been mapped onto US-GAAP-shaped entity fields by the existing `findValue` lists (`Inventory` accepts both `InventoryNet` and `ifrs-full:Inventories`, etc.).
- Must respect the convention in `CLAUDE.md`: structured logging via `zap` only (no `fmt.Println`, no `log`). The parser already holds a named logger (`p.logger`).
- Must follow the project's no-globals rule — the plug helper is a method on `*Parser` or a free function in the same package; no package-level state.

---

## Architecture decisions for Phase 0

### A. Where the plug computation lives

The plug computation lives at the **end of `parsePeriodData`** in `internal/infra/gateways/sec/parser.go`, after:
- All `findValue` and `sumValues` calls (lines 466-832 in current master).
- The `InventoryTurnover` derivation (lines 834-836).
- The `MissingFields` stamp (lines 838-841).
- The `Revenue/OperatingIncome` viability check (lines 843-846).

…and **before** the final `return financialData, nil` at line 848.

This placement ensures:
1. Every component field (`CashAndCashEquivalents`, `Inventory`, `Goodwill`, `OtherIntangibles`, `DeferredTaxAssets`, `OperatingLeaseLiability`) has already been populated.
2. Both umbrellas (`CurrentAssets`, `TotalAssets`, `CurrentLiabilities`, `TotalLiabilities`) have already been populated.
3. We have a `*FinancialData` to mutate (no need to thread the raw `data` map further).
4. We can fail-soft: if `Revenue && OperatingIncome` are both zero we return an error EARLIER, and the plug step never runs — no need to handle that branch.

The plug computation itself goes in a new package-private helper in the SEC package (`computePlugs` in a new file `internal/infra/gateways/sec/plugs.go`) so it stays unit-testable in isolation without spinning up the whole parser.

### B. Plug formulas (the four residuals)

```
non_current_assets = max(0, TotalAssets - CurrentAssets)

OtherCurrentAssets         = max(0, CurrentAssets         - (CashAndCashEquivalents + Inventory))
OtherNonCurrentAssets      = max(0, non_current_assets    - (Goodwill + OtherIntangibles + DeferredTaxAssets))
OtherCurrentLiabilities    = max(0, CurrentLiabilities    - OperatingLeaseLiabilityCurrent)
OtherNonCurrentLiabilities = max(0, TotalLiabilities      - CurrentLiabilities - TotalDebt - OperatingLeaseLiabilityNoncurrent)
```

**Component selection rationale (matches today's entity surface):**

| Plug | Subtracted components (already in `FinancialData`) | Excluded from plug |
|---|---|---|
| `OtherCurrentAssets` | `CashAndCashEquivalents`, `Inventory` | Accounts-receivable (not in entity), prepaid (not in entity) → land in plug |
| `OtherNonCurrentAssets` | `Goodwill`, `OtherIntangibles`, `DeferredTaxAssets` | PP&E (not in entity), long-term investments (not in entity) → land in plug |
| `OtherCurrentLiabilities` | `OperatingLeaseLiabilityCurrent` | Accounts payable, accrued, current portion of debt → land in plug |
| `OtherNonCurrentLiabilities` | `CurrentLiabilities`, `TotalDebt`, `OperatingLeaseLiabilityNoncurrent` | Pension, OPEB, deferred tax liab → land in plug |

The rule is **conservative**: plug only what's not already a typed component on `FinancialData`. As Phases 1-3 add typed components (PP&E, A/R, A/P, pension, etc.), they get subtracted from the corresponding plug. This keeps the plug shape stable across the refactor.

For `OtherNonCurrentLiabilities` we derive `NonCurrentLiabilities = TotalLiabilities − CurrentLiabilities` inline (the entity doesn't carry `NonCurrentLiabilities` as a field, and adding it is out of scope for Phase 0). `TotalDebt` is the sum of current and non-current debt today (see `parser.go:728-753`), so subtracting all of it from `NonCurrentLiabilities` slightly over-subtracts for filers with material short-term debt. This is acceptable for Phase 0 because (a) the plug is allowed to go to zero via the `max(0, …)` clamp, (b) the asymmetry is documented in the property test's `WithinDelta` tolerance, and (c) Phase 1's `recomputeUmbrellas` shadow-mode will quantify the residual.

### C. Negative-residual handling

If `umbrella < sum(components)` for any pair, the raw residual is negative. This can legitimately happen for two reasons:

1. **Filer data quality** — the umbrella is mis-tagged or the component is over-stated (e.g., a filer reports `Goodwill` larger than `TotalAssets − CurrentAssets` because the umbrella tag is missing and we fell back to a partial extraction).
2. **Currency disambiguation lag** — a multi-currency period where the dominant-currency collapse picked the wrong bucket (extremely rare; Phase B post-launch hotfix at `parser.go:309-363` mitigates this).

In both cases the correct Phase 0 behavior is to **clamp to zero** and emit a `Debug` log line tagged with `cik`, `period`, the plug field name, and the negative residual amount. This:
- Preserves the invariant "plug ≥ 0" so downstream property tests don't fail on data-quality edge cases.
- Surfaces the anomaly via structured logging so operators can investigate.
- Stays out of the `Warnings` field on `FinancialData` — Phase 0 promises zero behavior change.

The clamp is **silent at INFO level** but visible at DEBUG. We do NOT emit a `WARN` because plug clamping is expected for some filers and would create alert noise. Phase 1's shadow-mode shim will tally these and report aggregate counts.

### D. Multi-currency interaction

`extractFiscalPeriods` at `parser.go:278-366` resolves the dominant currency and collapses `valuesByCurrency` into `payload.values` BEFORE `parsePeriodData` is called per period. By the time we reach the end of `parsePeriodData`, every field on the in-flight `*FinancialData` is denominated in `payload.currency` (which becomes `financialData.ReportingCurrency`).

Therefore plug arithmetic is currency-coherent by construction. We do NOT need to FX-convert anything or check `ReportingCurrency` inside `computePlugs`. Phase B9 of the IFRS-FPI plan will later convert the entire `*FinancialData` to USD via `convertFinancialsToUSD` in `internal/services/valuation/currency.go`; the plug fields will be in the list of monetary fields converted at that step (Task 0.4 covers this).

### E. JSON / Go naming conventions

Following the existing entity surface (`current_assets`, `total_assets`, `deferred_tax_assets`):

```go
OtherCurrentAssets         float64 `json:"other_current_assets"`
OtherNonCurrentAssets      float64 `json:"other_non_current_assets"`
OtherCurrentLiabilities    float64 `json:"other_current_liabilities"`
OtherNonCurrentLiabilities float64 `json:"other_non_current_liabilities"`
```

- Go: PascalCase, matches `OperatingLeaseLiabilityCurrent` / `OperatingLeaseLiabilityNoncurrent`.
- JSON: snake_case, matches `current_assets` / `deferred_tax_assets`.
- No `omitempty` — these are computed plugs that legitimately can be zero, and we want them to round-trip through cached SQL/JSON deterministically. (Compare `TotalLiabilities` which uses `omitempty` because zero means "not parsed".)

### F. Persistence implications

The `financial_data` SQL table stores serialized `*FinancialData` as JSON in some code paths and as discrete columns in others. The plug fields:

- **Phase 0 of this plan does NOT add new columns** to the SQL schema. Phase 3 of the parent spec adds an `adjustment_ledger` JSON column; that is the canonical place for the schema migration.
- For Phase 0, the four plug fields ride along inside whichever JSON blob the row already uses (typically the `data` column on `historical_financial_data` or in the in-memory cache). They round-trip via Go's `encoding/json` package automatically.
- Repositories that store individual columns (rare — only `internal/infra/repositories/sqlite/financial_data_repository.go` if it exists) need verification in Task 0.6.

### G. What we explicitly do NOT pre-commit to

The plan must NOT lock in design decisions that future phases need flexibility on:

1. **Plug taxonomy granularity.** Phases 1-3 may discover that splitting `OtherNonCurrentAssets` into `OtherNonCurrentAssets_PPE`, `OtherNonCurrentAssets_LongTermInvestments`, etc. is necessary. The Phase 0 shape stays at four plugs; future granularity is additive (new typed components get subtracted from the existing plug field).
2. **Negative-residual policy.** We clamp to zero in Phase 0 with a Debug log. Phase 1's shadow-mode shim may upgrade this to a `Warnings` entry or a tracked counter — leave that flexibility open.
3. **Plug field exposure in OpenAPI.** The four fields are entity-internal in Phase 0. If the spec eventually adds them to a public response, that's a separate PR — Phase 0 ships zero-OpenAPI-change.
4. **DeepCopy implementation.** Phase 3 introduces `working := asReported.DeepCopy()`. The four plug fields are plain `float64`, so any structural copy (e.g., `*data` value copy) trivially handles them. No DeepCopy method exists in Phase 0; we don't introduce one.

---

## File Structure

Files created or modified by this plan:

| Path | Change | Responsibility |
|---|---|---|
| `internal/core/entities/financial_data.go` | Modify | Add 4 plug fields to `FinancialData` struct |
| `internal/infra/gateways/sec/plugs.go` | Create | `computePlugs(*entities.FinancialData, *zap.Logger)` pure helper |
| `internal/infra/gateways/sec/plugs_test.go` | Create | Unit tests for `computePlugs` (table-driven + property-based) |
| `internal/infra/gateways/sec/parser.go` | Modify | Call `computePlugs(financialData, p.logger)` at end of `parsePeriodData` |
| `internal/infra/gateways/sec/parser_test.go` | Modify | Extend existing AAPL fixture test to assert plug invariants |
| `internal/integration/datacleaner_baseline_test.go` | Modify | Add ticker-basket plug invariant assertion (or new test file if cleaner) |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` | Modify (optional) | Append Phase 0 completion note to Change log |

---

## API Contracts

Phase 0 has **NO API contract changes**. The four new entity fields are internal-only:

- `FinancialData` is NOT exposed as a public response schema in `docs/openapi.yaml`. The public response shape is `FairValueResponse` (at `docs/openapi.yaml:241`), which projects a subset of valuation outputs — none of the four plug fields are surfaced.
- Internal Go callers that import `internal/core/entities` will see four new fields on the struct. This is backwards-compatible: Go allows reading non-existent fields as zero, and all existing literal/struct-tag construction sites continue to compile (the new fields default to `0.0`).
- JSON marshaling of `FinancialData` (used by the artifact-bundle subsystem and the in-memory cache) gains four extra keys. Existing replay bundles WITHOUT these keys decode cleanly with the new struct (the new fields land at zero). New bundles WITH the keys decode into older binaries that lack the fields — JSON ignores unknown keys by default.
- The replay bundle's `10-clean-input.json` and `10-clean-output.json` artifacts will start containing the four new keys after Phase 0 ships. The replay tooling at `internal/observability/replay/` uses `--allow-schema-drift` (R3a Stage I.0) to accept this; no replay binary change is required.

**OpenAPI Task: SKIPPED.** The original brief listed Task 0.5 as updating `docs/openapi.yaml`, but inspection of the file confirms `FinancialData` has no schema component there — the entity is internal. No OpenAPI edit is needed.

---

## Module Descriptions

### `internal/core/entities/financial_data.go`

Plain-Go entity struct. Carries the SEC-parsed and cleaner-mutated financial primitives. Phase 0 adds four `float64` plug fields. The entity is layer-pure: no imports from `internal/services/*` or `internal/infra/*`. JSON tags drive (a) persistence serialization, (b) artifact-bundle serialization, (c) `FairValueResponse` projection. No methods on the new fields — pure data.

### `internal/infra/gateways/sec/plugs.go` (new)

A small package-private helper file containing one exported-within-package function:

```
func computePlugs(fd *entities.FinancialData, logger *zap.Logger)
```

Pure-Go arithmetic. Reads the existing component / umbrella fields off `fd`, computes the four residuals, clamps negatives to zero, writes the four results back to `fd`. Emits a single `Debug` log line per clamped plug with the field name and the raw (negative) residual, tagged with `cik` and `period` from `fd`.

The function takes the logger explicitly (rather than relying on a package-level singleton, per the project's no-globals rule). `parsePeriodData` passes `p.logger` when calling it.

### `internal/infra/gateways/sec/plugs_test.go` (new)

Two test groups:

1. **Table-driven unit tests** covering: typical filer (positive plugs), zero-umbrella case (all plugs zero), negative-residual case (plug clamped, debug log emitted), IFRS-full-shape filer (TSM-like component decomposition).
2. **`gopter` property tests** asserting the invariant: for any random `FinancialData` with valid (`umbrella ≥ component`) inputs, the post-`computePlugs` state satisfies `umbrella == sum(components) + plug` within float tolerance.

### `internal/infra/gateways/sec/parser.go`

One-line insertion at the end of `parsePeriodData` (line 847 in current master, immediately before `return financialData, nil`):

```go
computePlugs(financialData, p.logger)
```

Plus one comment block explaining the placement decision (post-extraction, post-currency-collapse, pre-return).

### `internal/integration/datacleaner_baseline_test.go` (or new file)

One integration test that walks the 10-ticker basket fixture bundles, invokes the full SEC parser path, and asserts for each parsed period:
- All four plug fields are `≥ 0`.
- `CurrentAssets ≥ CashAndCashEquivalents + Inventory` (the components fit under the umbrella).
- `TotalAssets ≥ CurrentAssets` (non-current is non-negative).
- For periods where `TotalLiabilities > 0`: `TotalLiabilities ≥ CurrentLiabilities`.

---

## Tasks by Agent

Phase 0 is **BACKEND-only**. No FRONTEND, UX_UI, or new QA work beyond the test asserts BACKEND writes.

### BACKEND: 6 tasks (one entity edit, one helper + test, one parser edit, one integration test, one doc update, one verification commit).

---

### Task 0.1: Extend `entities.FinancialData` with four plug fields

**Files:**
- Modify: `internal/core/entities/financial_data.go:101-103`

**Steps:**

- [ ] **Step 1: Write the failing test**

Open `internal/core/entities/financial_data.go` and confirm the current shape of the working-capital block (lines 101-103). Then add a new test file `internal/core/entities/financial_data_plugs_test.go`:

```go
package entities

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinancialData_PlugFields_JSONRoundtrip pins the four plug fields' JSON
// tags so the replay bundle / cache layer can round-trip them deterministically.
// DC-1 Phase 0 — see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
func TestFinancialData_PlugFields_JSONRoundtrip(t *testing.T) {
	fd := FinancialData{
		OtherCurrentAssets:         12_345.0,
		OtherNonCurrentAssets:      67_890.0,
		OtherCurrentLiabilities:    1_111.0,
		OtherNonCurrentLiabilities: 2_222.0,
	}

	raw, err := json.Marshal(fd)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"other_current_assets":12345`)
	assert.Contains(t, string(raw), `"other_non_current_assets":67890`)
	assert.Contains(t, string(raw), `"other_current_liabilities":1111`)
	assert.Contains(t, string(raw), `"other_non_current_liabilities":2222`)

	var decoded FinancialData
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, fd.OtherCurrentAssets, decoded.OtherCurrentAssets)
	assert.Equal(t, fd.OtherNonCurrentAssets, decoded.OtherNonCurrentAssets)
	assert.Equal(t, fd.OtherCurrentLiabilities, decoded.OtherCurrentLiabilities)
	assert.Equal(t, fd.OtherNonCurrentLiabilities, decoded.OtherNonCurrentLiabilities)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -run TestFinancialData_PlugFields_JSONRoundtrip ./internal/core/entities/
```

Expected: FAIL with `unknown field OtherCurrentAssets in struct literal of type FinancialData` (compile error).

- [ ] **Step 3: Add the four fields to `FinancialData`**

Edit `internal/core/entities/financial_data.go`. Locate the existing working-capital block at lines 101-103:

```go
	// Working capital components (for delta WC calculation)
	CurrentAssets      float64 `json:"current_assets"`
	CurrentLiabilities float64 `json:"current_liabilities"`
```

Replace it with:

```go
	// Working capital components (for delta WC calculation)
	CurrentAssets      float64 `json:"current_assets"`
	CurrentLiabilities float64 `json:"current_liabilities"`

	// Plug fields — DC-1 Phase 0 (see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md).
	// Computed at the end of the SEC parser as residuals so that:
	//   CurrentAssets    == CashAndCashEquivalents + Inventory + OtherCurrentAssets
	//   TotalAssets      == CurrentAssets + Goodwill + OtherIntangibles + DeferredTaxAssets + OtherNonCurrentAssets
	//   CurrentLiab      == OperatingLeaseLiabilityCurrent + OtherCurrentLiabilities
	//   TotalLiab        == CurrentLiab + TotalDebt + OperatingLeaseLiabilityNoncurrent + OtherNonCurrentLiabilities
	// All four are >= 0 by construction (negative residuals clamped with a Debug log).
	// Phase 1+ uses these to enforce components-sum-to-umbrellas in the cleaner.
	OtherCurrentAssets         float64 `json:"other_current_assets"`
	OtherNonCurrentAssets      float64 `json:"other_non_current_assets"`
	OtherCurrentLiabilities    float64 `json:"other_current_liabilities"`
	OtherNonCurrentLiabilities float64 `json:"other_non_current_liabilities"`
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -run TestFinancialData_PlugFields_JSONRoundtrip ./internal/core/entities/
```

Expected: PASS.

- [ ] **Step 5: Run full entity package tests to confirm no collateral breakage**

```bash
go test ./internal/core/entities/...
```

Expected: PASS, no test failures.

- [ ] **Step 6: Commit**

```bash
git add internal/core/entities/financial_data.go internal/core/entities/financial_data_plugs_test.go
git commit -m "feat(entities): add four plug fields to FinancialData (DC-1 Phase 0)

Adds OtherCurrentAssets, OtherNonCurrentAssets, OtherCurrentLiabilities,
OtherNonCurrentLiabilities as the structural primitive that DC-1 Phases 1-4
depend on. JSON roundtrip pinned by financial_data_plugs_test.go. Plug
computation lands in a follow-up commit (SEC parser).

Refs: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md"
```

---

### Task 0.2: Implement `computePlugs` helper in SEC package

**Files:**
- Create: `internal/infra/gateways/sec/plugs.go`
- Create: `internal/infra/gateways/sec/plugs_test.go`

**Steps:**

- [ ] **Step 1: Write the failing test (table-driven unit tests)**

Create `internal/infra/gateways/sec/plugs_test.go`:

```go
package sec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestComputePlugs_TypicalFiler pins the happy-path: a US-GAAP filer with
// all components populated yields strictly-non-negative plug residuals that
// satisfy the components-sum-to-umbrellas invariant.
func TestComputePlugs_TypicalFiler(t *testing.T) {
	fd := &entities.FinancialData{
		CIK:                               "0000320193",
		FilingPeriod:                      "2023FY",
		TotalAssets:                       352_755.0,
		CurrentAssets:                     143_566.0,
		CurrentLiabilities:                145_308.0,
		TotalLiabilities:                  290_437.0,
		CashAndCashEquivalents:            29_965.0,
		Inventory:                         6_331.0,
		Goodwill:                          0.0,
		OtherIntangibles:                  0.0,
		DeferredTaxAssets:                 0.0,
		TotalDebt:                         111_088.0,
		OperatingLeaseLiabilityCurrent:    1_410.0,
		OperatingLeaseLiabilityNoncurrent: 10_550.0,
	}

	computePlugs(fd, zap.NewNop())

	// OtherCurrentAssets = 143566 - (29965 + 6331) = 107270
	assert.InDelta(t, 107_270.0, fd.OtherCurrentAssets, 0.01)
	// non_current_assets = 352755 - 143566 = 209189; minus (0+0+0) = 209189
	assert.InDelta(t, 209_189.0, fd.OtherNonCurrentAssets, 0.01)
	// OtherCurrentLiabilities = 145308 - 1410 = 143898
	assert.InDelta(t, 143_898.0, fd.OtherCurrentLiabilities, 0.01)
	// OtherNonCurrentLiabilities = 290437 - 145308 - 111088 - 10550 = 23491
	assert.InDelta(t, 23_491.0, fd.OtherNonCurrentLiabilities, 0.01)
}

// TestComputePlugs_ZeroUmbrellas verifies that missing umbrellas leave all
// plugs at zero (no negative residuals leaking from arithmetic on zero).
func TestComputePlugs_ZeroUmbrellas(t *testing.T) {
	fd := &entities.FinancialData{
		CIK:          "0000000000",
		FilingPeriod: "2024Q1",
		// All fields default to zero.
	}

	computePlugs(fd, zap.NewNop())

	assert.Equal(t, 0.0, fd.OtherCurrentAssets)
	assert.Equal(t, 0.0, fd.OtherNonCurrentAssets)
	assert.Equal(t, 0.0, fd.OtherCurrentLiabilities)
	assert.Equal(t, 0.0, fd.OtherNonCurrentLiabilities)
}

// TestComputePlugs_NegativeResidualClampedAndLogged verifies the safety net:
// when sum(components) > umbrella (data-quality edge case), the plug clamps
// to zero and a Debug log line is emitted.
func TestComputePlugs_NegativeResidualClampedAndLogged(t *testing.T) {
	core, recorded := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	fd := &entities.FinancialData{
		CIK:                    "0001234567",
		FilingPeriod:           "2025Q2",
		CurrentAssets:          100.0,
		CashAndCashEquivalents: 80.0,
		Inventory:              50.0, // 80 + 50 = 130 > 100 → negative residual
	}

	computePlugs(fd, logger)

	assert.Equal(t, 0.0, fd.OtherCurrentAssets, "negative residual must clamp to zero")

	// Exactly one debug log line for the clamped field.
	entries := recorded.FilterMessage("plug residual clamped to zero").All()
	assert.Len(t, entries, 1)
	ctxMap := entries[0].ContextMap()
	assert.Equal(t, "0001234567", ctxMap["cik"])
	assert.Equal(t, "2025Q2", ctxMap["period"])
	assert.Equal(t, "OtherCurrentAssets", ctxMap["plug_field"])
	// raw residual = 100 - 130 = -30
	assert.InDelta(t, -30.0, ctxMap["raw_residual"].(float64), 0.01)
}

// TestComputePlugs_IFRSFullFiler_TSM mimics the TSM-style decomposition
// (large goodwill, intangibles, multi-currency had already collapsed before
// the call). Just confirms IFRS-shaped data flows through identically.
func TestComputePlugs_IFRSFullFiler_TSM(t *testing.T) {
	fd := &entities.FinancialData{
		CIK:                    "0001046179",
		FilingPeriod:           "2024FY",
		ReportingCurrency:      "TWD",
		TotalAssets:            6_000_000.0,
		CurrentAssets:          2_000_000.0,
		CurrentLiabilities:     1_000_000.0,
		TotalLiabilities:       3_000_000.0,
		CashAndCashEquivalents: 1_500_000.0,
		Inventory:              250_000.0,
		Goodwill:               50_000.0,
		OtherIntangibles:       30_000.0,
		DeferredTaxAssets:      20_000.0,
		TotalDebt:              900_000.0,
	}

	computePlugs(fd, zap.NewNop())

	// Plug invariant: umbrella == sum(known components) + plug.
	got := fd.CashAndCashEquivalents + fd.Inventory + fd.OtherCurrentAssets
	assert.InDelta(t, fd.CurrentAssets, got, 0.01)

	gotNCA := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets + fd.OtherNonCurrentAssets
	assert.InDelta(t, fd.TotalAssets-fd.CurrentAssets, gotNCA, 0.01)

	assert.GreaterOrEqual(t, fd.OtherCurrentAssets, 0.0)
	assert.GreaterOrEqual(t, fd.OtherNonCurrentAssets, 0.0)
	assert.GreaterOrEqual(t, fd.OtherCurrentLiabilities, 0.0)
	assert.GreaterOrEqual(t, fd.OtherNonCurrentLiabilities, 0.0)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -run TestComputePlugs ./internal/infra/gateways/sec/
```

Expected: FAIL with `undefined: computePlugs` (compile error).

- [ ] **Step 3: Implement `computePlugs`**

Create `internal/infra/gateways/sec/plugs.go`:

```go
package sec

import (
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// computePlugs fills the four "Other*" plug fields on fd as residuals between
// each balance-sheet umbrella and the sum of its known typed components.
//
// Invariant after this call (for every (umbrella, components, plug) triple):
//
//   umbrella == sum(known_components) + plug,  with plug >= 0
//
// When sum(known_components) > umbrella the raw residual is negative; we clamp
// to zero (preserving the >= 0 invariant) and emit a Debug log line so the
// data-quality anomaly is observable without polluting Warnings.
//
// computePlugs assumes fd's monetary fields are already currency-coherent —
// i.e., extractFiscalPeriods has already collapsed the per-currency value
// buckets via the dominant-currency resolution at parser.go:309-363. The
// caller in parsePeriodData satisfies this by construction.
//
// DC-1 Phase 0 — see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md.
func computePlugs(fd *entities.FinancialData, logger *zap.Logger) {
	if fd == nil {
		return
	}

	// Plug 1: OtherCurrentAssets = CurrentAssets - (Cash + Inventory)
	currentAssetsComponents := fd.CashAndCashEquivalents + fd.Inventory
	fd.OtherCurrentAssets = clampPlug(
		"OtherCurrentAssets",
		fd.CurrentAssets,
		currentAssetsComponents,
		fd,
		logger,
	)

	// Plug 2: OtherNonCurrentAssets = (TotalAssets - CurrentAssets) - (Goodwill + Intangibles + DTA)
	nonCurrentAssetsUmbrella := fd.TotalAssets - fd.CurrentAssets
	if nonCurrentAssetsUmbrella < 0 {
		nonCurrentAssetsUmbrella = 0
	}
	nonCurrentAssetsComponents := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets
	fd.OtherNonCurrentAssets = clampPlug(
		"OtherNonCurrentAssets",
		nonCurrentAssetsUmbrella,
		nonCurrentAssetsComponents,
		fd,
		logger,
	)

	// Plug 3: OtherCurrentLiabilities = CurrentLiabilities - OperatingLeaseLiabilityCurrent
	currentLiabComponents := fd.OperatingLeaseLiabilityCurrent
	fd.OtherCurrentLiabilities = clampPlug(
		"OtherCurrentLiabilities",
		fd.CurrentLiabilities,
		currentLiabComponents,
		fd,
		logger,
	)

	// Plug 4: OtherNonCurrentLiabilities = (TotalLiabilities - CurrentLiabilities) - (TotalDebt + OpLeaseNoncurrent)
	// Note: TotalDebt aggregates current + noncurrent debt today (parser.go:728-753),
	// so subtracting all of it from non-current-liabilities slightly over-subtracts
	// for filers with material short-term debt. The max(0,…) clamp absorbs this;
	// Phase 1's recomputeUmbrellas shadow-mode will quantify the residual.
	nonCurrentLiabUmbrella := fd.TotalLiabilities - fd.CurrentLiabilities
	if nonCurrentLiabUmbrella < 0 {
		nonCurrentLiabUmbrella = 0
	}
	nonCurrentLiabComponents := fd.TotalDebt + fd.OperatingLeaseLiabilityNoncurrent
	fd.OtherNonCurrentLiabilities = clampPlug(
		"OtherNonCurrentLiabilities",
		nonCurrentLiabUmbrella,
		nonCurrentLiabComponents,
		fd,
		logger,
	)
}

// clampPlug returns max(0, umbrella - components), emitting a Debug log line
// (tagged with cik/period/plug_field/raw_residual) when the raw residual is
// negative. Logger may be nil — in that case the clamp still happens silently.
func clampPlug(plugField string, umbrella, components float64, fd *entities.FinancialData, logger *zap.Logger) float64 {
	raw := umbrella - components
	if raw >= 0 {
		return raw
	}
	if logger != nil {
		logger.Debug("plug residual clamped to zero",
			zap.String("cik", fd.CIK),
			zap.String("period", fd.FilingPeriod),
			zap.String("plug_field", plugField),
			zap.Float64("umbrella", umbrella),
			zap.Float64("components", components),
			zap.Float64("raw_residual", raw),
		)
	}
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -run TestComputePlugs ./internal/infra/gateways/sec/
```

Expected: PASS (all four `TestComputePlugs_*` subtests green).

- [ ] **Step 5: Commit**

```bash
git add internal/infra/gateways/sec/plugs.go internal/infra/gateways/sec/plugs_test.go
git commit -m "feat(sec): add computePlugs helper for component-residual fields (DC-1 Phase 0)

Pure-Go helper that fills FinancialData's four Other* plug fields as
residuals (umbrella - sum(components)). Clamps negative residuals to zero
and emits a Debug log line per clamp so data-quality anomalies are
observable without polluting Warnings.

Table-driven coverage:
- Typical US-GAAP filer (positive plugs, invariant holds)
- Zero-umbrella case (all plugs zero)
- Negative-residual clamping + log assertion
- IFRS-full filer shape (TSM-like)

Refs: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md"
```

---

### Task 0.3: Wire `computePlugs` into `parsePeriodData`

**Files:**
- Modify: `internal/infra/gateways/sec/parser.go:843-848`

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `internal/infra/gateways/sec/parser_test.go` (do not modify the existing `TestParser_ParseFinancialData_Success` test — add a new one at the end of the file):

```go
// TestParser_ParseFinancialData_ComputesPlugs verifies that parsePeriodData
// fills the four Other* plug fields after extracting components and umbrellas.
// DC-1 Phase 0 — see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md.
func TestParser_ParseFinancialData_ComputesPlugs(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// Minimal AAPL-shaped fixture: enough fields to exercise each plug.
	facts := &ports.SECCompanyFacts{
		CIK:        "0000320193",
		EntityName: "Apple Inc.",
		Facts: map[string]map[string]ports.SECFactGroup{
			"us-gaap": {
				"Revenues":                              factGroupUSD(383_285_000_000, 2023),
				"OperatingIncomeLoss":                   factGroupUSD(114_301_000_000, 2023),
				"Assets":                                factGroupUSD(352_755_000_000, 2023),
				"AssetsCurrent":                         factGroupUSD(143_566_000_000, 2023),
				"LiabilitiesCurrent":                    factGroupUSD(145_308_000_000, 2023),
				"Liabilities":                           factGroupUSD(290_437_000_000, 2023),
				"CashAndCashEquivalentsAtCarryingValue": factGroupUSD(29_965_000_000, 2023),
				"InventoryNet":                          factGroupUSD(6_331_000_000, 2023),
				"LongTermDebt":                          factGroupUSD(111_088_000_000, 2023),
				"OperatingLeaseLiabilityCurrent":        factGroupUSD(1_410_000_000, 2023),
				"OperatingLeaseLiabilityNoncurrent":     factGroupUSD(10_550_000_000, 2023),
				"CommonStockSharesOutstanding":          factGroupShares(15_550_061_000, 2023),
			},
		},
	}

	historical, err := parser.ParseFinancialData(context.Background(), facts)
	require.NoError(t, err)
	require.NotEmpty(t, historical.Data)

	fd := historical.Data["2023FY"]
	require.NotNil(t, fd)

	// Plug invariant: umbrella == sum(known components) + plug.
	assert.InDelta(t, fd.CurrentAssets,
		fd.CashAndCashEquivalents+fd.Inventory+fd.OtherCurrentAssets, 1.0)
	assert.InDelta(t, fd.TotalAssets-fd.CurrentAssets,
		fd.Goodwill+fd.OtherIntangibles+fd.DeferredTaxAssets+fd.OtherNonCurrentAssets, 1.0)
	assert.InDelta(t, fd.CurrentLiabilities,
		fd.OperatingLeaseLiabilityCurrent+fd.OtherCurrentLiabilities, 1.0)
	assert.InDelta(t, fd.TotalLiabilities-fd.CurrentLiabilities,
		fd.TotalDebt+fd.OperatingLeaseLiabilityNoncurrent+fd.OtherNonCurrentLiabilities, 1.0)

	// All four plugs must be non-negative.
	assert.GreaterOrEqual(t, fd.OtherCurrentAssets, 0.0)
	assert.GreaterOrEqual(t, fd.OtherNonCurrentAssets, 0.0)
	assert.GreaterOrEqual(t, fd.OtherCurrentLiabilities, 0.0)
	assert.GreaterOrEqual(t, fd.OtherNonCurrentLiabilities, 0.0)
}

// factGroupUSD is a small helper to keep the AAPL fixture readable.
func factGroupUSD(val float64, fy int) ports.SECFactGroup {
	return ports.SECFactGroup{
		Units: map[string][]ports.SECFact{
			"USD": {{End: "2023-09-30", Val: val, Fy: fy, Fp: "FY", Form: "10-K", Filed: "2023-11-03"}},
		},
	}
}

// factGroupShares is a small helper for dimensionless share-count facts.
func factGroupShares(val float64, fy int) ports.SECFactGroup {
	return ports.SECFactGroup{
		Units: map[string][]ports.SECFact{
			"shares": {{End: "2023-09-30", Val: val, Fy: fy, Fp: "FY", Form: "10-K", Filed: "2023-11-03"}},
		},
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -run TestParser_ParseFinancialData_ComputesPlugs ./internal/infra/gateways/sec/
```

Expected: FAIL — plug fields are all zero (computePlugs not yet wired in).

- [ ] **Step 3: Wire computePlugs into parsePeriodData**

Edit `internal/infra/gateways/sec/parser.go`. Locate the end of `parsePeriodData` (around line 843-848):

```go
	// Validate that we have minimum required data
	if financialData.Revenue <= 0 && financialData.OperatingIncome <= 0 {
		return nil, fmt.Errorf("insufficient data: no revenue or operating income")
	}

	return financialData, nil
}
```

Replace with:

```go
	// Validate that we have minimum required data
	if financialData.Revenue <= 0 && financialData.OperatingIncome <= 0 {
		return nil, fmt.Errorf("insufficient data: no revenue or operating income")
	}

	// DC-1 Phase 0: fill the four Other* plug fields as residuals so components
	// sum to umbrellas. Runs after every findValue/sumValues call and after
	// the missing-fields stamp; runs before return so callers see a balanced
	// FinancialData. computePlugs assumes currency coherence — guaranteed by
	// extractFiscalPeriods's dominant-currency collapse (parser.go:309-363).
	// See docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md.
	computePlugs(financialData, p.logger)

	return financialData, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -run TestParser_ParseFinancialData_ComputesPlugs ./internal/infra/gateways/sec/
```

Expected: PASS.

- [ ] **Step 5: Run all SEC parser tests to confirm no collateral breakage**

```bash
go test ./internal/infra/gateways/sec/...
```

Expected: PASS, all existing tests (including `TestParser_ParseFinancialData_Success`) still green.

- [ ] **Step 6: Commit**

```bash
git add internal/infra/gateways/sec/parser.go internal/infra/gateways/sec/parser_test.go
git commit -m "feat(sec): wire computePlugs into parsePeriodData (DC-1 Phase 0)

Calls computePlugs at the end of parsePeriodData so every FinancialData
emerging from the SEC parser satisfies the component-sum-to-umbrella
invariant. Placement is post-extraction, post-currency-collapse, pre-return
— before this point the entity fields aren't fully populated; after this
point the entity must round-trip with balanced plugs.

Zero consumer behavior change — the four plug fields are read by nobody
in Phase 0. Phase 1's recomputeUmbrellas shim will start consuming them.

Refs: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md"
```

---

### Task 0.4: Property test — components sum to umbrellas for any valid input

**Files:**
- Modify: `internal/infra/gateways/sec/plugs_test.go` (append new property test)

**Steps:**

- [ ] **Step 1: Write the failing test**

Append to `internal/infra/gateways/sec/plugs_test.go`:

```go
import (
	// Add these to the existing import block at the top of the file:
	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

// TestComputePlugs_Property_ComponentsSumToUmbrellas is the load-bearing
// invariant for DC-1 Phase 0: for any FinancialData with non-negative inputs,
// after computePlugs runs, the four (umbrella, components, plug) triples
// must satisfy `umbrella == sum(components) + plug` within float tolerance,
// and all four plugs must be >= 0.
//
// We generate inputs that respect the "umbrella >= sum(components)" precondition
// because that's the well-formed case; the negative-residual case is covered
// by TestComputePlugs_NegativeResidualClampedAndLogged in this file.
func TestComputePlugs_Property_ComponentsSumToUmbrellas(t *testing.T) {
	params := gopter.DefaultTestParameters()
	params.Rng.Seed(20260516)
	params.MinSuccessfulTests = 200

	properties := gopter.NewProperties(params)

	properties.Property("plug invariant holds for current assets", prop.ForAll(
		func(cash, inventory, slack float64) bool {
			currentAssets := cash + inventory + slack
			fd := &entities.FinancialData{
				CIK:                    "FUZZ",
				FilingPeriod:           "2024FY",
				CurrentAssets:          currentAssets,
				CashAndCashEquivalents: cash,
				Inventory:              inventory,
			}
			computePlugs(fd, zap.NewNop())
			got := fd.CashAndCashEquivalents + fd.Inventory + fd.OtherCurrentAssets
			return fd.OtherCurrentAssets >= 0 &&
				approxEqual(fd.CurrentAssets, got, 1e-6)
		},
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
	))

	properties.Property("plug invariant holds for non-current assets", prop.ForAll(
		func(currentAssets, goodwill, intangibles, dta, slack float64) bool {
			totalAssets := currentAssets + goodwill + intangibles + dta + slack
			fd := &entities.FinancialData{
				CIK:               "FUZZ",
				FilingPeriod:      "2024FY",
				TotalAssets:       totalAssets,
				CurrentAssets:     currentAssets,
				Goodwill:          goodwill,
				OtherIntangibles:  intangibles,
				DeferredTaxAssets: dta,
			}
			computePlugs(fd, zap.NewNop())
			gotNCA := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets + fd.OtherNonCurrentAssets
			return fd.OtherNonCurrentAssets >= 0 &&
				approxEqual(fd.TotalAssets-fd.CurrentAssets, gotNCA, 1e-6)
		},
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
	))

	properties.Property("plug invariant holds for current liabilities", prop.ForAll(
		func(opLeaseCurrent, slack float64) bool {
			currentLiab := opLeaseCurrent + slack
			fd := &entities.FinancialData{
				CIK:                            "FUZZ",
				FilingPeriod:                   "2024FY",
				CurrentLiabilities:             currentLiab,
				OperatingLeaseLiabilityCurrent: opLeaseCurrent,
			}
			computePlugs(fd, zap.NewNop())
			got := fd.OperatingLeaseLiabilityCurrent + fd.OtherCurrentLiabilities
			return fd.OtherCurrentLiabilities >= 0 &&
				approxEqual(fd.CurrentLiabilities, got, 1e-6)
		},
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
	))

	properties.Property("plug invariant holds for non-current liabilities", prop.ForAll(
		func(currentLiab, totalDebt, opLeaseNoncurrent, slack float64) bool {
			totalLiab := currentLiab + totalDebt + opLeaseNoncurrent + slack
			fd := &entities.FinancialData{
				CIK:                               "FUZZ",
				FilingPeriod:                      "2024FY",
				TotalLiabilities:                  totalLiab,
				CurrentLiabilities:                currentLiab,
				TotalDebt:                         totalDebt,
				OperatingLeaseLiabilityNoncurrent: opLeaseNoncurrent,
			}
			computePlugs(fd, zap.NewNop())
			gotNCL := fd.TotalDebt + fd.OperatingLeaseLiabilityNoncurrent + fd.OtherNonCurrentLiabilities
			return fd.OtherNonCurrentLiabilities >= 0 &&
				approxEqual(fd.TotalLiabilities-fd.CurrentLiabilities, gotNCL, 1e-6)
		},
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
		gen.Float64Range(0, 1e12),
	))

	properties.TestingRun(t)
}

// approxEqual returns true when |a - b| <= max(absTol, relTol * max(|a|, |b|)).
// Used to absorb float64 accumulation error in large-value plug arithmetic.
func approxEqual(a, b, relTol float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	scale := a
	if scale < 0 {
		scale = -scale
	}
	if b > scale {
		scale = b
	}
	if -b > scale {
		scale = -b
	}
	tol := relTol * scale
	if tol < 1e-3 {
		tol = 1e-3
	}
	return diff <= tol
}
```

- [ ] **Step 2: Run test to verify it fails or passes**

```bash
go test -run TestComputePlugs_Property_ComponentsSumToUmbrellas ./internal/infra/gateways/sec/
```

Expected: PASS (because `computePlugs` is already implemented in Task 0.2 — this test exercises the invariant across 200 randomized inputs as a regression guard, not a RED-phase failing test).

If it fails, the most likely causes are: (a) `approxEqual` tolerance too tight for `1e12` magnitude inputs — relax `relTol` to `1e-9`; (b) the gopter generator producing values that overflow to `+Inf` — cap the range to `1e15` instead of unbounded. Fix and re-run.

- [ ] **Step 3: Commit**

```bash
git add internal/infra/gateways/sec/plugs_test.go
git commit -m "test(sec): property-based regression for plug invariant (DC-1 Phase 0)

Gopter-based property tests with 200 randomized inputs per invariant.
Asserts: for any FinancialData where umbrella >= sum(components), after
computePlugs runs, plug >= 0 AND umbrella == sum(components) + plug within
float tolerance.

This is the load-bearing invariant gate for Phase 0 → Phase 1 transition
per the spec's 'Plug values match (umbrella - sum) across ticker basket'
acceptance criterion.

Refs: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md"
```

---

### Task 0.5: Integration test — plug invariants across the 10-ticker basket

**Files:**
- Modify: `internal/integration/datacleaner_baseline_test.go` (append a new test function — do not modify existing tests in that file).

**Note on fixture availability:** The 10-ticker basket bundles live under `artifacts/2026-04-25/` (or whichever recent date the baseline references). If the bundle layout has shifted, fall back to invoking the existing parser fixtures the test suite already uses (search for `*.json` testdata under `internal/infra/gateways/sec/testdata/` if it exists, or construct in-memory `SECCompanyFacts` via the existing test helpers). The invariant assertions below are bundle-shape-agnostic.

**Steps:**

- [ ] **Step 1: Locate baseline test conventions**

Read `internal/integration/datacleaner_baseline_test.go` to learn how the existing tests load fixture data and whether they iterate over a ticker basket. Use the same fixture-loading idiom for the new test.

```bash
go test -list '.*' ./internal/integration/ | head -30
```

This lists test names; pattern-match on `Datacleaner*` to find the existing convention.

- [ ] **Step 2: Write the failing test**

Append to `internal/integration/datacleaner_baseline_test.go` (or create a new file `internal/integration/datacleaner_plug_invariants_test.go` if the baseline file is large):

```go
// TestDatacleaner_PlugInvariants_TickerBasket asserts that the SEC parser's
// post-Phase-0 plug computation produces a balanced FinancialData for every
// ticker in the DC-1 acceptance basket. Closes the Phase 0 → Phase 1 gate
// per the spec: 'Plug values match (umbrella - sum) across ticker basket.'
//
// Fixture loading mirrors the existing TestDatacleaner_Baseline_* tests in
// this file — if the baseline tests have moved to a different fixture
// strategy, update this test to match.
func TestDatacleaner_PlugInvariants_TickerBasket(t *testing.T) {
	basket := []string{"AAPL", "MSFT", "JNJ", "KO", "F", "AMD", "MXL", "TSM", "BABA", "EQIX"}

	for _, ticker := range basket {
		t.Run(ticker, func(t *testing.T) {
			historical := loadHistoricalFixture(t, ticker) // existing test helper in this package
			if historical == nil {
				t.Skipf("no fixture for %s — basket coverage will be tightened in Phase 1", ticker)
				return
			}

			for period, fd := range historical.Data {
				if fd == nil {
					continue
				}

				// All four plugs must be non-negative.
				assert.GreaterOrEqual(t, fd.OtherCurrentAssets, 0.0,
					"%s %s: OtherCurrentAssets negative", ticker, period)
				assert.GreaterOrEqual(t, fd.OtherNonCurrentAssets, 0.0,
					"%s %s: OtherNonCurrentAssets negative", ticker, period)
				assert.GreaterOrEqual(t, fd.OtherCurrentLiabilities, 0.0,
					"%s %s: OtherCurrentLiabilities negative", ticker, period)
				assert.GreaterOrEqual(t, fd.OtherNonCurrentLiabilities, 0.0,
					"%s %s: OtherNonCurrentLiabilities negative", ticker, period)

				// Plug invariant: umbrella == sum(known components) + plug.
				// Use a relative tolerance because IFRS-full filers can carry
				// trillions in local-currency units (TWD, JPY).
				tol := plugTolerance(fd.CurrentAssets)
				if fd.CurrentAssets > 0 {
					sumCA := fd.CashAndCashEquivalents + fd.Inventory + fd.OtherCurrentAssets
					assert.InDelta(t, fd.CurrentAssets, sumCA, tol,
						"%s %s: CurrentAssets plug invariant", ticker, period)
				}

				if fd.TotalAssets > 0 && fd.TotalAssets > fd.CurrentAssets {
					nca := fd.TotalAssets - fd.CurrentAssets
					sumNCA := fd.Goodwill + fd.OtherIntangibles + fd.DeferredTaxAssets + fd.OtherNonCurrentAssets
					assert.InDelta(t, nca, sumNCA, plugTolerance(nca),
						"%s %s: NonCurrentAssets plug invariant", ticker, period)
				}

				if fd.CurrentLiabilities > 0 {
					sumCL := fd.OperatingLeaseLiabilityCurrent + fd.OtherCurrentLiabilities
					assert.InDelta(t, fd.CurrentLiabilities, sumCL, plugTolerance(fd.CurrentLiabilities),
						"%s %s: CurrentLiabilities plug invariant", ticker, period)
				}

				if fd.TotalLiabilities > 0 && fd.TotalLiabilities > fd.CurrentLiabilities {
					ncl := fd.TotalLiabilities - fd.CurrentLiabilities
					sumNCL := fd.TotalDebt + fd.OperatingLeaseLiabilityNoncurrent + fd.OtherNonCurrentLiabilities
					assert.InDelta(t, ncl, sumNCL, plugTolerance(ncl),
						"%s %s: NonCurrentLiabilities plug invariant", ticker, period)
				}
			}
		})
	}
}

// plugTolerance returns max(1.0, value * 1e-9). For large IFRS-full filer
// magnitudes (1e12+ TWD) the absolute floor of 1.0 isn't enough; the relative
// term takes over. For small magnitudes (US$1M and under) the floor catches
// float64 accumulation error from a chain of subtractions.
func plugTolerance(value float64) float64 {
	if value < 0 {
		value = -value
	}
	tol := value * 1e-9
	if tol < 1.0 {
		tol = 1.0
	}
	return tol
}
```

- [ ] **Step 3: Run test to verify it passes (with skips for missing fixtures)**

```bash
go test -run TestDatacleaner_PlugInvariants_TickerBasket -v ./internal/integration/
```

Expected: PASS for every ticker with a fixture; SKIP for any ticker whose fixture is not yet captured. The `t.Skipf` line ensures CI is not blocked on missing fixtures — Phase 1's shadow-mode work will tighten this and require all 10.

**If any ticker FAILS:** the failure is signal, not noise. Either (a) the SEC parser is mis-extracting a component (file a bug under `docs/bugs/`), or (b) the plug formula needs an additional known-component subtractor. Investigate before proceeding; the spec's Phase 0 → Phase 1 gate explicitly requires green basket coverage.

- [ ] **Step 4: Commit**

```bash
git add internal/integration/datacleaner_baseline_test.go
git commit -m "test(integration): plug-invariant assertions across 10-ticker basket (DC-1 Phase 0)

Asserts every period's plug fields are non-negative AND each (umbrella,
components, plug) triple satisfies the algebraic invariant within float
tolerance. Tolerance is relative (1e-9 × magnitude, floor 1.0) so IFRS-full
filers carrying trillions in local-currency units (TSM/TWD, BABA/CNY) don't
trigger false negatives.

Closes the Phase 0 → Phase 1 transition gate per the spec.

Refs: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md"
```

---

### Task 0.6: Verify repository / persistence layer round-trips the four plug fields

**Files:**
- Inspect: `internal/infra/repositories/sqlite/financial_data_repository.go` (if exists)
- Inspect: `internal/infra/repositories/cache/*.go` (in-memory + Redis cache impls)

**Steps:**

- [ ] **Step 1: Locate persistence layers that touch `FinancialData`**

```bash
go test -list 'Test.*Financial.*Repo.*' ./internal/infra/repositories/...
```

And:

```
Grep for: \bFinancialData\b inside internal/infra/repositories/
```

(Use the project's Grep tool, not raw `grep`.)

- [ ] **Step 2: For each persistence path, confirm round-trip behavior**

If the repository serializes `FinancialData` as a JSON blob into a single column: zero code change required — JSON tags already cover the new fields, and existing rows decode cleanly with the new struct (missing keys → zero values).

If the repository stores discrete columns (one per field): add `other_current_assets`, `other_non_current_assets`, `other_current_liabilities`, `other_non_current_liabilities` columns to the table, plus a forward-compatible migration. **If this is the case, escalate to ARCH before proceeding — schema migrations need a separate review pass and were not in the Phase 0 scope as written.**

If the repository uses an ORM with struct tags: confirm the new fields' tags align with the ORM's expectations (likely none needed — `json:"…"` covers both `encoding/json` and many ORMs).

- [ ] **Step 3: Add a regression test pinning the round-trip**

If a JSON-blob persistence path is in use, add a test asserting that a `FinancialData` with non-zero plug fields round-trips through `Save → Load` with the four fields preserved. Place the test alongside the existing repository tests.

- [ ] **Step 4: Commit (only if changes were needed)**

If no code change was required, skip the commit — the verification is recorded in this plan's checkbox.

If a test or migration was added:

```bash
git add <paths>
git commit -m "test(repo): pin FinancialData plug-field round-trip (DC-1 Phase 0)

Verifies that the new Other*Assets / Other*Liabilities plug fields survive
the persistence layer's JSON-blob serialization without truncation.

Refs: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md"
```

---

### Task 0.7: Full-test-suite verification + final commit

**Steps:**

- [ ] **Step 1: Run the full test suite with race detector**

```bash
go test -race ./...
```

Expected: ALL PASS. Phase 0 ships zero behavior change, so every existing test stays green.

- [ ] **Step 2: Run the coverage report on touched packages**

```bash
go test -cover ./internal/core/entities/... ./internal/infra/gateways/sec/...
```

Expected: coverage stays ≥ existing baseline for both packages. The new `plugs.go` file has unit + property + integration coverage, so the SEC package coverage should increase, not decrease.

- [ ] **Step 3: Run the observability lint guards**

Per `CLAUDE.md`:

```bash
./scripts/lint-logs.sh                       # Linux/macOS
.\scripts\lint-logs.ps1                      # Windows (PowerShell)
./scripts/lint-prometheus-registers.sh       # Linux/macOS
.\scripts\lint-prometheus-registers.ps1      # Windows
```

Expected: PASS. The new `computePlugs` is not request-path code (it runs in the SEC gateway, which uses the fx-provided singleton logger — that's the documented exception per `CLAUDE.md` "request-path logs via `logctx.From(ctx)`" rule). The Prometheus lint is unrelated.

- [ ] **Step 4: Spot-check one replay bundle**

Pick any recent bundle from `artifacts/` and inspect its `10-clean-input.json` after re-running the parser. The four new keys should appear in the JSON; existing fields are unchanged.

```bash
# Pick any bundle and dump the financial data section:
# (Use Read tool against artifacts/<date>/<ticker>/req_*/10-clean-input.json)
```

Expected: four new keys present with non-negative values; all pre-Phase-0 keys unchanged.

- [ ] **Step 5: Commit the spec update (Task 0.8 below) and tag the phase**

After Task 0.8 (spec + ARCHITECTURE update) lands in the same series of commits, the Phase 0 work is complete. No tag is required — the merge commit message references the spec.

---

### Task 0.8: Documentation updates (ARCHITECTURE.md, CLAUDE.md, TESTING.md, spec changelog)

**Files:**
- Modify: `ARCHITECTURE.md`
- Modify: `CLAUDE.md` (Important Files table entry for `financial_data.go`)
- Modify: `TESTING.md` (add property-test pattern note)
- Modify: `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` (append to Change log)
- Modify: `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` (status update only)

**Steps:**

- [ ] **Step 1: Update ARCHITECTURE.md**

Add a paragraph to ARCHITECTURE.md (after the existing description of `FinancialData`) noting:

> `FinancialData` now carries four plug fields (`OtherCurrentAssets`, `OtherNonCurrentAssets`, `OtherCurrentLiabilities`, `OtherNonCurrentLiabilities`) populated by the SEC parser as residuals between umbrella totals and the sum of known components. This is the structural foundation for the DC-1 datacleaner refactor (`docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`). Phase 0 (the entity + parser plug fill) ships with zero downstream behavior change; subsequent phases use the plug fields to enforce components-sum-to-umbrellas through the cleaner pipeline.

- [ ] **Step 2: Update CLAUDE.md "Important Files" table**

Locate the row for `internal/core/entities/financial_data.go` if one exists, or add one. Suggested entry (keeping the table format):

```
| `internal/core/entities/financial_data.go` | Domain entity for company financials. Plug fields (`other_current_assets`, etc.) added in DC-1 Phase 0; populated by SEC parser. |
```

If the file isn't already in the table, append it under the relevant section.

- [ ] **Step 3: Update TESTING.md**

Add a short subsection to TESTING.md under "Test Categories" or "Property-based testing":

> ### Plug-invariant property tests (DC-1 Phase 0+)
>
> The SEC parser fills four "Other*" plug fields on `FinancialData` so components always sum to umbrellas. The invariant is pinned by a `gopter`-based property test at `internal/infra/gateways/sec/plugs_test.go` (200 randomized inputs per invariant, seeded for reproducibility) and a basket-level integration test at `internal/integration/datacleaner_baseline_test.go` (10 tickers covering US-GAAP + IFRS-full filers). When extending the cleaner to add new typed components (e.g., PP&E, A/R), update the relevant plug formula in `internal/infra/gateways/sec/plugs.go` to subtract the new component so the plug shrinks correspondingly.

- [ ] **Step 4: Update the spec's Change log**

Append a row to the table at the bottom of `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`:

```
| 2026-05-16 | Phase 0 SHIPPED: four plug fields added to `FinancialData`; SEC parser fills them via `computePlugs` at end of `parsePeriodData`; property test + ticker-basket integration test pin the components-sum-to-umbrellas invariant. Zero downstream behavior change — no consumer reads the plug fields in Phase 0. Phase 1 (`recomputeUmbrellas` shadow shim) is now unblocked. |
```

- [ ] **Step 5: Update the reviewer tracker (status only)**

Edit `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` to bump the status header from `OPEN` to `IN PROGRESS — Phase 0 shipped 2026-05-16` and append a short paragraph at the end noting which phase is now unblocked.

Do NOT archive the tracker — DC-1 closes only after Phase 4. Per the spec: "DC-1 tracker archived to `docs/reviewer/archive/`" is in the Phase 4 acceptance checklist.

- [ ] **Step 6: Commit the docs**

```bash
git add ARCHITECTURE.md CLAUDE.md TESTING.md \
        docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md \
        docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md
git commit -m "docs: DC-1 Phase 0 shipped — plug-field entity extension + parser fill

- ARCHITECTURE.md: note FinancialData plug fields and link to spec.
- CLAUDE.md: add financial_data.go to Important Files table with plug context.
- TESTING.md: document plug-invariant property-test pattern.
- spec changelog: Phase 0 SHIPPED row.
- DC-1 tracker: status bumped to IN PROGRESS.

Refs: docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md"
```

---

## Spec Updates

The plan modifies these spec/doc files (concrete diffs are in Task 0.8):

| File | Change |
|---|---|
| `ARCHITECTURE.md` | Add paragraph documenting plug fields and spec reference |
| `CLAUDE.md` | Add `financial_data.go` row to Important Files table |
| `TESTING.md` | Add subsection on plug-invariant property tests |
| `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` | Append "Phase 0 SHIPPED" row to Change log |
| `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` | Status bump to IN PROGRESS |

**`docs/openapi.yaml` is intentionally NOT modified** — `FinancialData` is internal-only and not referenced from any OpenAPI schema component.

---

## Tests

**Phase 0 testing strategy:**

| Test | Location | Type | What it pins |
|---|---|---|---|
| `TestFinancialData_PlugFields_JSONRoundtrip` | `internal/core/entities/financial_data_plugs_test.go` | Unit | JSON tag fidelity for the four plug fields |
| `TestComputePlugs_TypicalFiler` | `internal/infra/gateways/sec/plugs_test.go` | Unit | Happy-path arithmetic for a US-GAAP filer |
| `TestComputePlugs_ZeroUmbrellas` | same | Unit | All plugs stay zero when umbrellas are zero |
| `TestComputePlugs_NegativeResidualClampedAndLogged` | same | Unit | Negative residuals clamp to zero AND emit Debug log |
| `TestComputePlugs_IFRSFullFiler_TSM` | same | Unit | IFRS-shaped filer arithmetic |
| `TestComputePlugs_Property_ComponentsSumToUmbrellas` | same | Property (gopter) | Algebraic invariant across 200 fuzz inputs |
| `TestParser_ParseFinancialData_ComputesPlugs` | `internal/infra/gateways/sec/parser_test.go` | Integration | End-to-end parse → plug fill for AAPL fixture |
| `TestDatacleaner_PlugInvariants_TickerBasket` | `internal/integration/datacleaner_baseline_test.go` | Integration | Plug invariants hold for 10-ticker basket |

**Critical edge cases that MUST have tests:**

1. **Zero umbrellas** — covered by `TestComputePlugs_ZeroUmbrellas`. A period with no balance-sheet data (e.g., a partial filing) must not produce negative or NaN plugs.
2. **Negative residuals** — covered by `TestComputePlugs_NegativeResidualClampedAndLogged`. Data-quality anomalies (component > umbrella) must clamp gracefully.
3. **IFRS-full filers** — covered by `TestComputePlugs_IFRSFullFiler_TSM` (unit) + the basket integration test (TSM, BABA in the basket). Currency-denominated facts must already be collapsed by the dominant-currency resolution before `computePlugs` runs.
4. **Large magnitudes** — covered by the property test (generates up to 1e12 values) and the basket test's `plugTolerance` (relative tolerance for trillion-magnitude TWD filers).
5. **Float accumulation error** — the `approxEqual` / `InDelta` tolerances absorb this; the property test exercises it across 200 inputs.

**Tests we intentionally do NOT add in Phase 0:**

- Consumer behavior tests (DCF, WACC, Graham diffs) — Phase 0 promises zero consumer change, but those tests would be testing inertia, not behavior. The existing valuation test suite stays green; that's the proof.
- Multi-currency boundary tests — those belong in the SEC parser's `extractFiscalPeriods` test suite which already exists.
- DeepCopy / view-reconstruction tests — those land in Phase 3.

---

## Implementation Roadmap (suggested order)

1. **Task 0.1** — extend the entity. Smallest possible blast radius; sets up the field surface for everything below. ~15 minutes.
2. **Task 0.2** — implement `computePlugs` with table-driven + edge-case unit tests. Pure-Go, isolated package. ~45 minutes.
3. **Task 0.3** — wire it into `parsePeriodData`. One-line insertion + one new parser test. ~20 minutes.
4. **Task 0.4** — gopter property test. Pure regression hardening. ~30 minutes.
5. **Task 0.5** — basket integration test. Depends on understanding the existing fixture-loading idiom in `datacleaner_baseline_test.go`. ~45 minutes.
6. **Task 0.6** — repository / persistence audit. Read-only walk-through unless an unexpected schema is discovered. ~20 minutes (assuming JSON-blob persistence — escalate if not).
7. **Task 0.7** — full-suite verification. Race tests, coverage, lint guards. ~15 minutes (machine time dominates).
8. **Task 0.8** — documentation. ARCHITECTURE, CLAUDE, TESTING, spec changelog, tracker status. ~30 minutes.

**Total: ~3.5 hours focused work.** The spec's "2-3 days" estimate accommodates context-loading, code review back-and-forth, and the cycle through the bit-for-bit basket validation. Allow the slack.

---

## Potential Challenges

### Risk 1: An existing test asserts the absence of the four new JSON keys

**Likelihood:** Low. The project uses `testify/assert.Equal` and struct comparisons; few tests do exhaustive JSON byte-for-byte comparisons.

**Mitigation:** Task 0.7's `go test -race ./...` catches this. If found, the offending test was over-specified — relax it to `assert.JSONEq` with only the fields it actually cares about. Do NOT remove the new plug fields from the entity to satisfy a brittle test.

### Risk 2: The IFRS-full filer basket trips the integration test

**Likelihood:** Medium. TSM/BABA reporting practices have surprised us before (multi-currency, component-level debt). If a filer's umbrella tag is missing AND a component is present, the umbrella is zero but the component is positive → the plug computation may produce a non-obvious shape.

**Mitigation:** The plug formula handles this correctly today — when `CurrentAssets == 0`, both the umbrella and the components reference the same baseline (0), so the plug stays at 0 (with a Debug log if components are positive). The integration test guards against value-positive components landing in a missing-umbrella plug. If a real ticker fails, the failure surfaces a parser bug (missing umbrella extraction), not a plug bug.

### Risk 3: Replay bundle diff fires on existing bundles

**Likelihood:** High — replay's `--diff-stages` will detect the four new JSON keys in `10-clean-input.json` / `10-clean-output.json`. The R3a `--allow-schema-drift` flag exists exactly for this case.

**Mitigation:** Replay golden-bundle regeneration is in Phase 4 of the parent spec, not Phase 0. Phase 0 ships behind `--allow-schema-drift`. Document this in Task 0.8's CLAUDE.md update.

### Risk 4: Test fixtures for the 10-ticker basket are out-of-date

**Likelihood:** Medium. The basket coverage is tightened over time; some tickers may have stale or missing fixtures.

**Mitigation:** Task 0.5's `t.Skipf` allows missing fixtures to skip without blocking CI. Track missing tickers as a follow-up under the DC-1 tracker — Phase 1's shadow-mode work requires full basket coverage.

### Risk 5: `parser.go`'s comment block becomes unwieldy

**Likelihood:** Low. The wiring is a single line + a comment block.

**Mitigation:** Keep the comment block ≤ 8 lines. The full rationale lives in `plugs.go`; `parser.go` only needs the placement justification.

---

## GitHub Issue Update

- Issue: N/A (no GitHub issue tied to DC-1; tracked via `docs/reviewer/DC-1-…md`)
- Status: not updated
- Actions taken: none
- Proposed update: when Phase 0 commits land, bump the tracker status as described in Task 0.8 Step 5.

---

## Acceptance Criteria

Phase 0 is complete when ALL of the following are true:

1. `entities.FinancialData` has the four new plug fields with the documented JSON tags.
2. `internal/infra/gateways/sec/plugs.go` exists with `computePlugs` implementation matching the documented formulas.
3. `parsePeriodData` calls `computePlugs(financialData, p.logger)` at the documented placement (end of function, pre-return).
4. All eight tests listed in the Tests section pass on `go test -race ./...`.
5. `go test -cover ./internal/core/entities/... ./internal/infra/gateways/sec/...` shows coverage ≥ pre-Phase-0 baseline.
6. `./scripts/lint-logs.{sh,ps1}` and `./scripts/lint-prometheus-registers.{sh,ps1}` pass.
7. ARCHITECTURE.md, CLAUDE.md, TESTING.md, the spec changelog, and the DC-1 reviewer tracker are updated per Task 0.8.
8. Manual replay-bundle spot-check (Task 0.7 Step 4) confirms the four keys appear in `10-clean-input.json` with non-negative values.
9. No DCF / WACC / Graham / NCAV / FFO / DDM / RevenueMultiple output value changes for any ticker in the basket (verified by the existing valuation test suite remaining green and by replay-bundle diff against pre-Phase-0 master, ignoring the four new keys).
10. The DC-1 tracker status is bumped to `IN PROGRESS — Phase 0 shipped`.

---

## Phases 1-4 Architectural Commentary

These are scoping notes for future ARCH cycles — Phase 0's plan does NOT pre-commit to any of the design choices below.

### Phase 1 — `recomputeUmbrellas` shadow shim (3-5 days)

Phase 1 adds `recomputeUmbrellas(*entities.FinancialData)` called at the end of the datacleaner pipeline. In **shadow mode**: it computes what the umbrella SHOULD be (sum of components + plug) and compares against the actual mutated umbrella, logging a `WARN` on divergence. Behavior is unchanged — the warning is observability only.

**Phase 0 unblocks Phase 1 by:**
- Providing the plug fields that `recomputeUmbrellas` needs as the "buffer" term for components-not-yet-typed.
- Establishing the invariant `umbrella == sum(components) + plug` so the shadow-mode shim has a well-defined "expected" value.

**Don't pre-commit to in Phase 0:**
- The exact WARN format or whether shadow warnings flow into `Warnings` on `FinancialData`.
- Whether shadow mode runs once per pipeline or after every adjuster — likely once at the end, but Phase 1 may decide otherwise.
- The threshold above which shadow warnings escalate to a quality-flag artifact-bundle trigger.

### Phase 2 — `Adjuster` interface + `AdjustmentLedger` (5-7 days)

Phase 2 introduces the unified `Adjuster` interface and the `AdjusterOutput { LedgerEntries, Overlays, Flags }` shape. All 14 existing adjusters (A1-A5, B1-B3, C1-C7) are refactored to implement it. The pipeline collects ledger + overlays IN PARALLEL with the existing mutation pattern — both code paths run, but only the existing mutation drives behavior. The ledger is observability-only in Phase 2.

**Phase 0 unblocks Phase 2 by:**
- Ensuring component fields are populated and balanced post-parse, so adjusters that mutate components don't immediately desync umbrellas.
- Establishing the plug as the "buffer" for unknown components — Phase 2's adjusters can adjust typed components without worrying about the plug term, which absorbs the residual.

**Don't pre-commit to in Phase 0:**
- The `AmountSemantics` enum values (`Incremental` / `Replacement` / `Delta`) — those are Phase 2 design decisions.
- The shape of the `AIProvenance` struct for B3 contingent liabilities — depends on the AI module's existing surface.
- Whether the `Adjuster` interface's `Apply` method takes `*entities.FinancialData` or a richer `WorkingCopy` wrapper.

### Phase 3 — `CleanedFinancialData` + view reconstruction (5-7 days)

Phase 3 adds the triple-view output (`AsReported`, `Restated()`, `InvestedCapital()`) and stops the cleaner from mutating the input pointer. SQLite gains an `adjustment_ledger` JSON column. Replay bundles gain `11-restated.json`, `12-investedcapital.json`, `13-cleaner-audit.json`. Consumers still read `cleaned.Restated()` for byte-for-byte parity with today's output.

**Phase 0 unblocks Phase 3 by:**
- Establishing the entity shape that survives DeepCopy — the four plug `float64`s are trivially copyable.
- Pinning the components-sum-to-umbrellas invariant so `Restated()`'s recompute step has a well-defined contract.

**Don't pre-commit to in Phase 0:**
- The DeepCopy implementation (value copy vs deep struct copy vs slice/map clone for `OperatingLeaseCommitments` map and `MissingFields` slice).
- The SQL migration ordering (`adjustment_ledger` column add can be backfilled or shipped with cache invalidation).
- The exact `Restated()` cache semantics — likely first-access lazy, but Phase 3 may pre-compute eagerly for perf reasons.

### Phase 4 — Consumer migration + WACC boundary (7-10 days)

Phase 4 migrates all 13 consumer sites to read the correct view, introduces the `WACCInputs` compile-time type to gate the InvestedCapital boundary, and ships the B3 routing correction (contingent liabilities flow to `DebtLikeClaims` instead of `TotalDebt`). This is the FIRST phase with consumer-visible behavior changes — WACC weights shift for filers with material contingents (TSM, AMD, possibly BABA). Replay golden bundles regenerate.

**Phase 0 unblocks Phase 4 by:**
- Nothing directly — Phase 4 is downstream of Phase 3.
- Indirectly: the plug shape ensures Graham's NCAV consumer (which Phase 4 migrates to `AsReported`) reads a balanced balance sheet without the current `TotalLiabilities` derivation fallback.

**Don't pre-commit to in Phase 0:**
- The `WACCInputs` struct's exact fields — `CapitalStructureDebt` vs `MarketValueOfDebt` naming is Phase 4's call.
- The optional `IncludeDebtLikeClaimsInCapitalStructure` knob — out of scope for Phase 4 per the spec's Open Questions.
- Bundle layout naming (`11-restated.json` vs `11-cleaned-restated.json`) — Phase 4 finalizes.

---

## Spec Issues Found

After reading the spec and the supporting files thoroughly, I found **two minor issues** that should be flagged for follow-up (NOT silently corrected in Phase 0):

### Issue 1: Spec mentions `rapid.Check` in the property test snippet; project uses `gopter`

The spec's testing-strategy section uses `rapid.Check(t, func(t *rapid.T) {…})` style:

```go
// from spec line 458:
rapid.Check(t, func(t *rapid.T) {
    fd := genRandomFinancialData(t)
    recomputeUmbrellas(&fd)
    epsilon := 0.01
    require.InDelta(t, fd.TotalAssets, fd.TotalLiabilities + fd.StockholdersEquity, epsilon)
})
```

But the project's `go.mod` declares `github.com/leanovate/gopter v0.2.11` and the existing property test at `pkg/finance/wacc/wacc_property_test.go` uses gopter. Adding a `rapid` dependency for this one test would be inconsistent with the project's convention.

**Resolution adopted in this plan:** Phase 0's property test uses `gopter` (matching the project convention). The spec's snippet is illustrative only — the substance (assert the invariant) is preserved. If the spec authors want to switch the project to `rapid` globally, that's a separate decision and not Phase 0's call.

### Issue 2: Spec's Phase 0 plug formula omits `DeferredTaxAssets` from the current-assets plug

The spec's Section "Data model — Entity extensions" describes:

> `OtherCurrentAssets` — `float64` — plug: `CurrentAssets − (Cash + Inventory + known_current_components)`

…and `T1` property test text:

> `CurrentAssets ≈ Cash + Inventory + DeferredTaxAssets_current_if_any + OtherCurrentAssets`

But `entities.FinancialData.DeferredTaxAssets` is a SINGLE field (total DTA, gross — see `financial_data.go:66`), with no current vs non-current split. The SEC parser at `parser.go:765-772` extracts a single `DeferredTaxAssets` value via `findValue([DeferredTaxAssetsNet, DeferredIncomeTaxAssetsNet, ifrs-full:DeferredTaxAssets])` — these XBRL tags are themselves taxonomy-agnostic re: current vs non-current.

**Resolution adopted in this plan:** Phase 0 subtracts `DeferredTaxAssets` from the NON-current plug only (treating the full DTA as non-current, which is the GAAP-default classification after ASU 2015-17). The current-asset plug does NOT subtract DTA. The brief's snippet ("…DTA_current_if_any…") becomes a no-op when DTA-current isn't a typed field.

If a future phase adds `DeferredTaxAssetsCurrent` as a typed component, the formulas adjust: subtract it from the current-assets plug, and reduce the non-current plug by the same amount. This is exactly the additive shape the plug invariant is designed for.

**Both issues are non-blocking for Phase 0.** The plan implements the spec's intent (components sum to umbrellas) without introducing the `rapid` dependency or the DTA-current ambiguity. Flag both for spec maintenance after Phase 0 ships.

---

## Assumptions and Open Questions

### Assumptions used

1. The 10-ticker basket fixture (`AAPL, MSFT, JNJ, KO, F, AMD, MXL, TSM, BABA, EQIX`) is loadable via the existing test helpers in `internal/integration/datacleaner_baseline_test.go`. If the helper is named differently (`loadHistoricalFixture` is my best guess from spec context), Task 0.5 Step 2 uses the actual helper name.
2. `FinancialData` is persisted as a JSON blob in all repository implementations, not as discrete columns. Task 0.6 verifies this; if false, escalate.
3. No production consumer accesses the four plug fields. Phase 0's "zero behavior change" promise depends on this being true. Grep at Task 0.1 Step 3 confirms (the field names don't appear in the repo today).
4. The `lint-logs.sh` guard only flags request-path code. `computePlugs` runs in the SEC gateway (a non-request-path layer that uses the singleton logger), so it's exempt. Confirmed by `CLAUDE.md` "request-path logs via `logctx.From(ctx)`" rule which scopes the lint to request handlers.

### Blocking questions

None. The plan is self-contained.

### Non-blocking questions

1. Should the negative-residual clamp threshold be configurable (per `config/datacleaner/*.json`)? Phase 0 hard-codes "clamp at zero, log at Debug." Future phases may want a threshold like "log at Warn if |residual| > 1% of umbrella." Out of scope for Phase 0.
2. Should the four plug fields appear in `MissingFields` when they're effectively buffer terms? Phase 0 says NO — they're computed values, not missing inputs. If a future feature needs the "could not balance" signal, it should be a new field or a quality-score input.
3. Should `OtherCurrentLiabilities` subtract `OperatingLeaseLiability` (the umbrella) instead of `OperatingLeaseLiabilityCurrent` (the current portion)? Phase 0 follows the typed-field convention: subtract the specific current portion, leave the total umbrella for the non-current plug. If a filer only publishes the umbrella `OperatingLeaseLiability` without splitting, the current-portion field stays at zero and the umbrella value flows into the parser's `OperatingLeaseLiability` (non-typed for current/noncurrent split). Phase 0 absorbs this asymmetry into the plug; Phase 1's shadow shim quantifies the residual.

### Decisions needed before implementation

None. All design choices are recorded in the Architecture Decisions section above.

---

## Next Steps

After Phase 0 ships:

1. **Phase 1 ARCH cycle** — produce an implementer plan for `recomputeUmbrellas` shadow shim. Read this plan + the spec's Phase 1 row + the warnings logged by `computePlugs` over a week of production traffic to inform threshold tuning.
2. **DC-1 tracker** — keep open; status `IN PROGRESS — Phase 0 shipped, Phase 1 next`.
3. **CLAUDE.md "Common Gotchas"** — defer until Phase 4 ships (per spec acceptance criteria #7).

---

HANDOFF_TO: BACKEND
