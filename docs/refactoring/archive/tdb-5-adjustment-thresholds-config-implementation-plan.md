# TDB-5 — Adjustment thresholds config: IMPLEMENTER PLAN

**Issue:** #5 (`[TDB-5]`). **Spec:** `docs/refactoring/spec/tdb-5-adjustment-thresholds-config-spec.md`.
**Worktree:** `worktree-tdb-5-threshold-config` (own `go.mod`). **All validation uses `GOWORK=off`.**
**Mode:** TDD (RED → GREEN), small commits. Design is settled in the spec; this is the build order.

> Scope reminder (spec §2/§3): externalize the **flat asset-adjuster materiality/review gate
> constants** (A1/A2/A4/A6/A-RD/A-SW). Industry-keyed tables, treatment rates, and
> `rule.Threshold`-driven gates are OUT of scope. The load-bearing invariant is **default ==
> legacy constant ⇒ byte-identical behaviour**.

---

## 0. Pre-flight (read-only, no commit)

```bash
cd <worktree>
GOWORK=off go build ./... && GOWORK=off go test ./internal/services/datacleaner/... -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/ && echo "shadow clean"
```
Confirm baseline green before any edit. Re-confirm the 9 literal sites in spec §5.3 still match
(line numbers may have drifted; grep `threshold := 0.05` / `0.02` / `0.10` / `0.015` and the A6
`const threshold = 0.05` in `assets.go`).

---

## 1. Task ladder (RED → GREEN)

### Task 1 — Default table + resolved carrier (RED→GREEN, adjustments pkg)

**Files (new):**
- `internal/services/datacleaner/adjustments/asset_thresholds.go`
- `internal/services/datacleaner/adjustments/asset_thresholds_test.go`

**RED:** write `TestDefaultAssetThresholds_EqualLegacyConstants` asserting each field of
`DefaultAssetThresholds()` equals the documented literal (0.05/0.10/0.02/0.05/0.10/0.05/0.10/0.10/0.015).
Compiles-fails (type doesn't exist yet).

**GREEN:** add `type AssetThresholds struct { … }` (9 plain `float64` fields, spec §5.2) +
`DefaultAssetThresholds() AssetThresholds` returning the literals. Test passes.

**Validate:** `GOWORK=off go test ./internal/services/datacleaner/adjustments/ -run AssetThresholds -count=1`.

---

### Task 2 — Inject into `AssetAdjuster`, swap the 9 literals (RED→GREEN)

**Files:** `internal/services/datacleaner/adjustments/assets.go`,
`internal/services/datacleaner/adjustments/asset_thresholds_test.go`.

**RED:**
- `TestAssetAdjuster_DefaultGate_Unchanged` — build an A1 fixture at goodwill/assets `= 0.05` (skip)
  and `= 0.0501` (fire) using `NewAssetAdjuster()`; assert fire/skip matches today.
- `TestAssetAdjuster_OverrideGate` — `NewAssetAdjusterWithThresholds(AssetThresholds{GoodwillMateriality:0.20, …default rest})`; a ticker at 0.10 goodwill ratio that fired under default now SKIPs.
  (Build the rest of the struct from `DefaultAssetThresholds()` and override one field.)

**GREEN:**
1. `AssetAdjuster` struct: replace `// TODO: Add configuration for adjustment thresholds` with
   `thresholds AssetThresholds`.
2. `NewAssetAdjuster()` → `return &AssetAdjuster{thresholds: DefaultAssetThresholds()}`.
3. Add `NewAssetAdjusterWithThresholds(t AssetThresholds) *AssetAdjuster`.
4. Swap each literal (spec §5.3 list): A1 `0.05`/`0.10`, A2 `0.02`, A4 `0.05`/`0.10`,
   A6 `0.05`/`0.10` (drop the `const`), A-RD `0.10`, A-SW `0.015` → `aa.thresholds.<Field>`.
   **Leave reasoning strings, SkipMetrics keys, and flag taxonomy untouched** — only the comparison
   operand changes.

**Validate:** `GOWORK=off go test ./internal/services/datacleaner/adjustments/ -count=1` (the full
adjuster suite — ~40 existing tests must stay green because `NewAssetAdjuster()` is unchanged in
behaviour).

> Watch-out: A6's gate was `const threshold = 0.05`. Converting to a struct-field read removes the
> `const`; ensure the comparison stays `rouRatio <= threshold` / `>= significanceFlag` with identical
> operators. A6/A-RD/A-SW thresholds are still 0.05/0.10/0.10/0.015 by default — no behaviour change.

---

### Task 3 — Loader + Validate + Resolver (RED→GREEN, config pkg)

**Files (new):**
- `internal/config/adjustment_thresholds_config.go`
- `internal/config/adjustment_thresholds_config_test.go`

**RED:**
- `TestLoadAdjustmentThresholdsConfig_Absent_UsesDefaults` — load a non-existent path → error;
  `ResolveAssetThresholds(adjustments.DefaultAssetThresholds(), nil)` deep-equals the default.
- `TestLoadAdjustmentThresholdsConfig_Validate` — empty `version` rejected; ratio `0` and `1.5`
  rejected; a config omitting `a2_intangible` accepted (absent key OK).
- `TestResolveAssetThresholds_PartialOverride` — config sets only `asset.a1_goodwill.materiality_ratio = 0.2`;
  result has `GoodwillMateriality == 0.2`, every other field == default.

**GREEN:** implement per spec §5.1/§5.4:
- structs with **pointer** `*float64` fields (missing-vs-zero), `json` tags matching the schema
  (`asset.a1_goodwill.materiality_ratio`, …).
- `LoadAdjustmentThresholdsConfig(path)` mirroring `LoadFlagConditionsConfig` (path → env
  `ADJUSTMENT_THRESHOLDS_CONFIG_PATH` → `config/datacleaner/adjustment_thresholds.json`).
- `(*AdjustmentThresholdsConfig) Validate()` — version non-empty; every **present** ratio in `(0,1]`.
- `ResolveAssetThresholds(def adjustments.AssetThresholds, cfg *AdjustmentThresholdsConfig) adjustments.AssetThresholds`
  — copy `def`, overwrite each field only when its config pointer is non-nil.

> Import edge: `config` importing `adjustments` for the `AssetThresholds` type — verify it does not
> create a cycle (`adjustments` must NOT import `config`; it does not today). If a cycle appears,
> move `ResolveAssetThresholds` into `service.go` (which imports both) and keep `config` returning
> only the parsed pointer struct. Decide by compiling; document the choice in the commit.

**Validate:** `GOWORK=off go test ./internal/config/ -run AdjustmentThresholds -count=1`.

---

### Task 4 — Wire into `NewDataCleanerService` + config field (GREEN)

**Files:** `internal/services/datacleaner/service.go`, `internal/config/config.go`.

1. `config.go` `DataCleanerConfig`: add `ThresholdsPath string mapstructure:"thresholds_path"`
   (optional; empty → loader default path).
2. `service.go` `NewDataCleanerService`: after the flag-config block, add the warn-and-fallback load
   (spec §5.5): start from `adjustments.DefaultAssetThresholds()`, attempt
   `config.LoadAdjustmentThresholdsConfig(cfg.DataCleaner.ThresholdsPath)`, and on success resolve;
   on error keep defaults (non-fatal, mirroring the flag-config fallback at service.go:67-74).
3. `svc.assetAdjuster = adjustments.NewAssetAdjusterWithThresholds(assetThresholds)`.
4. `pipeline.go` test harness: **no change** — keep `NewAssetAdjuster()` (defaults).

**Validate:** `GOWORK=off go test ./internal/services/datacleaner/... -count=1`.

---

### Task 5 — Ship the config file + docs (GREEN)

**Files (new/edit):**
- `config/datacleaner/adjustment_thresholds.json` — spec §4 shape, values **byte-equal to defaults**,
  `version: "1.0.0"`, populated `description` (states "absent/absent-key → default → byte-identical").
- Update `docs/reviewer/archive/TDB-5-externalize-adjustment-thresholds-config.md` — link spec + plan,
  Status OPEN (done in this handoff).

> Optional (Open Question 4): if the team prefers no shipped file, skip the JSON and rely on the
> missing-file fallback. The plan ships it for discoverability; either is regression-safe.

---

## 2. Final validation (must all pass before PR)

```bash
cd <worktree>
GOWORK=off go build ./...
GOWORK=off go vet ./internal/services/datacleaner/... ./internal/config/...
GOWORK=off go test ./... -count=1            # full suite, EXIT 0
```

**Named invariants — must stay green (run explicitly):**
```bash
GOWORK=off go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1
GOWORK=off go test ./internal/services/datacleaner/ -run TestRecomputeUmbrellas_NoMutation -count=1
GOWORK=off go test ./internal/services/datacleaner/adjustments/ -run TestOrchestrator_LedgerOrdering -count=1
GOWORK=off go test ./internal/integration/ -run 'TestLedger_BasketSnapshot_(ClusterPrediction|T2BS3_RestatedReconstruction)' -count=1
```

**Shadow gate (must remain unchanged — exit 0):**
```bash
git diff --quiet internal/integration/testdata/recompute-shadow/ && echo "shadow snapshots byte-identical"
```
If the shadow diff is non-empty, the change altered a gate on a basket ticker — STOP and investigate
(a default drifted, or a literal was swapped to the wrong field). Do **not** regenerate snapshots.

No `CalculationVersion` bump (4.4) and no `SchemaVersion` bump (FinancialData 9) — behaviour is
preserved; this is config plumbing only.

## 3. Regression-safe default mechanism (recap — the binding constraint)

1. `DefaultAssetThresholds()` literals == the 9 pre-TDB-5 constants (pinned by Task 1 test).
2. `NewAssetAdjuster()` (used by ALL existing tests + `pipeline.go`) yields defaults — no test edits.
3. `NewDataCleanerService` load is warn-and-fallback — missing/invalid file → defaults.
4. `ResolveAssetThresholds` overwrites only non-nil keys — partial config can't zero a gate.
5. Shipped `adjustment_thresholds.json` values == defaults — no shipped override changes any gate.
6. The override test writes a **temp** file (never committed) to prove the wiring is live.

## 4. Commit template

```
feat(datacleaner): externalize asset-adjuster materiality thresholds to config (#5)

Replace the hardcoded A1/A2/A4/A6/A-RD/A-SW gate constants
(adjustments/assets.go) with an injected AssetThresholds struct loaded from
config/datacleaner/adjustment_thresholds.json via the existing
LoadFlagConditionsConfig-style loader. Defaults equal the pre-TDB-5
constants, so absent/partial/invalid config yields byte-identical behaviour
(DDM bit-for-bit, recompute-shadow, and basket invariants unchanged).

Closes the TODOs at adjustments/assets.go:14 (and the constructor TODOs at
adjustments/liabilities.go:17,27 for the in-scope asset gates; industry-keyed
B-rule tables deferred per spec §9 Q1).

Spec:  docs/refactoring/spec/tdb-5-adjustment-thresholds-config-spec.md
Plan:  docs/refactoring/implementations/tdb-5-adjustment-thresholds-config-implementation-plan.md

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

(Split into per-task commits if preferred: Task 1+2 = adjuster, Task 3 = loader, Task 4+5 = wiring/config.)

## 5. Hand-off notes for the implementer

- Keep `NewAssetAdjuster()` zero-arg — do NOT change the ~40 test call sites.
- The liability/earnings constructors (`NewLiabilityAdjuster`, `NewEarningsAdjuster`) are **not**
  touched in this cut (their in-scope gates are industry-keyed / already-externalized). Their
  constructor TODOs stay until the deferred follow-up.
- Touch only the comparison operand at each of the 9 sites; do not reflow surrounding code, reasoning
  strings, or SkipMetrics — that minimizes the shadow-diff blast radius to zero.
- If REVIEWER opts for the minimal cut (A1/A2/A4 only), drop A6/A-RD/A-SW fields from the struct and
  leave those literals — the rest of the plan is unchanged.
