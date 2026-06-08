# TDB-5 — Externalize datacleaner adjustment thresholds to config (DESIGN SPEC)

**Issue:** #5 (`[TDB-5]`, yonatan-levin/midas) — mirrors `docs/reviewer/TDB-5-externalize-adjustment-thresholds-config.md`.
**Status:** OPEN — design only (PLAN_AND_CREATE / ROLE: ARCH).
**Type:** Enhancement / tech-debt (maintainability).
**Author:** ARCH, 2026-06-08.
**Worktree:** `worktree-tdb-5-threshold-config` (own `go.mod`; validate with `GOWORK=off`).

Companion implementer plan: `docs/refactoring/implementations/tdb-5-adjustment-thresholds-config-implementation-plan.md`.

---

## 1. Goal

Externalize the **datacleaner adjuster materiality thresholds** that are hardcoded as code
constants today (the standing TODOs at `adjustments/liabilities.go:17,27` +
`adjustments/assets.go:14`) into a single JSON config file
(`config/datacleaner/adjustment_thresholds.json`), loaded through the existing
`internal/config/*_config.go` loader pattern and injected into the three adjusters.

The **load-bearing invariant** is regression safety: **when the config file is absent OR a
key is missing, every threshold falls back to the exact current hardcoded constant**, so
production output stays byte-identical until an operator deliberately provides an override.
This is what makes the change safe to land against the DC-1 bit-for-bit / shadow-snapshot /
basket invariants.

## 2. Non-goals (explicitly out of scope for this first cut)

- **Industry-keyed threshold maps** (`getInventoryThresholdForIndustry`,
  `getLeaseThresholdForIndustry`, `getPensionThresholdForIndustry`,
  `getContingentLiabilityThreshold`). These are richer per-GICS tables; externalizing them is a
  follow-up (see §9 Open Question 1). The TODOs the issue references sit on the **constructors**
  and point at the flat per-adjuster materiality/probability constants, not the per-industry tables.
- **`rule.Threshold.PercentageOfRevenue`-driven thresholds** (C1 restructuring 2%, C3 litigation 1%,
  C7 working-capital 15%, A7 excess-cash). These are **already externalized** via the
  `CleaningRule` config (`config/datacleaner/rules.json`) with a code-constant fallback. They are
  NOT hardcoded-only and are out of scope.
- **Writedown / retention *rates*** (A2 tiered 1/3-0.3-0.2, A4 50%, A5 40%). These are accounting
  treatment magnitudes, not materiality *gates*. They are deliberately deferred (see §9 Open
  Question 2) — the first cut externalizes the **gate thresholds** the TODOs reference, not the
  treatment rates.
- Changing any threshold value, flag taxonomy, reasoning string, or adjuster firing logic.
- A viper/`mapstructure` binding for the new file. We mirror the **direct JSON loader** pattern
  (`LoadFlagConditionsConfig` / `LoadIndustryCodeConfig`), which is `os.ReadFile` +
  `json.Unmarshal` + `Validate()` — NOT the viper `config.yaml` path. (The new file's *path* may be
  surfaced on `DataCleanerConfig`; the file *contents* are loaded by a dedicated loader.)
- No DI/`fx` provider changes beyond what `NewDataCleanerService` already does inline (it already
  loads `flag_conditions.json` inside the constructor with a fallback — we add one more load there).

## 3. Threshold inventory

Enumerated by grepping `assets.go`, `liabilities.go`, `earnings.go`. Column **Externalize Y/N**
states the first-cut decision; deferred items carry a reason.

### 3.1 In-scope — flat per-adjuster materiality / review gates (Class A: hardcoded code constants)

| Threshold name (config key)            | Adjuster | Current value | File:line                  | Externalize |
|----------------------------------------|----------|---------------|----------------------------|-------------|
| `a1.goodwill_materiality_ratio`        | A1       | `0.05`        | `assets.go:354`            | **Y**       |
| `a1.goodwill_significance_flag_ratio`  | A1       | `0.10`        | `assets.go:400`            | **Y**       |
| `a2.intangible_materiality_ratio`      | A2       | `0.02`        | `assets.go:753`            | **Y**       |
| `a4.dta_materiality_ratio`             | A4       | `0.05`        | `assets.go:892`            | **Y**       |
| `a4.dta_significance_flag_ratio`       | A4       | `0.10`        | `assets.go:932`            | **Y**       |
| `a6.rou_materiality_ratio`             | A6       | `0.05`        | `assets.go:487`            | **Y**       |
| `a6.rou_significance_flag_ratio`       | A6       | `0.10`        | `assets.go:534`            | **Y**       |
| `reviews.rd_capitalization_ratio`      | A-RD     | `0.10`        | `assets.go:1140`           | **Y**       |
| `reviews.capitalized_software_ratio`   | A-SW     | `0.015`       | `assets.go:~1245`          | **Y**       |

Rationale for inclusion: each is a single scalar materiality/review **gate** living inline in an
`Apply*` method, exactly the kind of "tune without recompile / audit in one place" value the issue
targets. Each already has a hardcoded fallback semantics-of-one (the literal), so a config indirection
with a default equal to that literal is behaviour-preserving by construction.

> Note on A6/A-RD/A-SW: A6 ships under TDB-2 with an explicit "thresholds stay as code constants
> (spec §3.5 / Q4)" decision. TDB-5 supersedes that *only* for the externalization mechanism —
> the **default remains 0.05 / 0.10**, so A6 behaviour is unchanged. Including A6/A-RD/A-SW keeps
> the asset-adjuster gate surface consistent (all A-gates read from one place) rather than leaving a
> confusing split where A1/A2/A4 are config-driven but A6 is not. If REVIEWER prefers a tighter
> first cut, A6/A-RD/A-SW can be deferred (see §9 Open Question 3) — A1/A2/A4 are the minimum.

### 3.2 Deferred — industry-keyed threshold tables (Class B)

| Threshold table                          | Adjuster | Default / shape                                  | File:line             | Externalize |
|------------------------------------------|----------|--------------------------------------------------|-----------------------|-------------|
| `getInventoryThresholdForIndustry`       | A5       | map GICS→ratio, default `0.25`                    | `assets.go:1780`      | N (defer)   |
| `getLeaseThresholdForIndustry`           | B1       | switch GICS→ratio, default `0.10`                 | `liabilities.go:1218` | N (defer)   |
| `getPensionThresholdForIndustry`         | B2       | switch GICS→ratio, default `0.03`                 | `liabilities.go:1233` | N (defer)   |
| `getContingentLiabilityThreshold`        | B3       | switch GICS→ratio, default `0.01`                 | `liabilities.go:1246` | N (defer)   |
| `detectInventoryObsolescence` `1.5×`/`<3.0` | A5    | obsolescence multipliers                          | `assets.go:1796-1807` | N (defer)   |

Reason to defer: these are per-GICS tables (richer schema: a default + a code→value map). Bundling
them now bloats the first cut and risks the regression invariant across many GICS branches. They are
a clean **phase 2** once the flat-gate mechanism is proven. (Open Question 1.)

### 3.3 Deferred — treatment rates (not materiality gates)

| Value                              | Adjuster | Current | File:line          | Externalize |
|------------------------------------|----------|---------|--------------------|-------------|
| A2 tiered retention rates          | A2       | `1/3` / `0.3` / `0.2` (by intangible size) | `assets.go:774-779` | N (defer)   |
| A4 valuation-allowance rate        | A4       | `0.50`  | `assets.go:912`    | N (defer)   |
| A5 inventory writedown rate        | A5       | `0.40`  | `assets.go:1043`   | N (defer)   |
| C1 restructuring estimate          | C1       | `0.015` of revenue | `earnings.go:146` | N (defer)   |

Reason to defer: these are accounting-treatment magnitudes, not "is this material enough to act"
gates. They change the *amount* of every fired adjustment, so they carry higher regression blast
radius and a different review story. Out of scope for a "materiality thresholds" first cut.
(Open Question 2.)

### 3.4 Out of scope — already externalized (Class C)

| Value                          | Adjuster | Source today                               | Externalize |
|--------------------------------|----------|--------------------------------------------|-------------|
| C1 restructuring gate `0.02`   | C1       | `rule.Threshold.PercentageOfRevenue` + code default | already done |
| C3 litigation gate `0.01`      | C3       | `rule.Threshold.PercentageOfRevenue` + code default | already done |
| C7 working-capital gate `0.15` | C7       | `rule.Threshold.PercentageOfRevenue` + code default | already done |
| A7 excess-cash op-cash pct     | A7       | `rule.Threshold.PercentageOfRevenue`        | already done |

These read from `CleaningRule` config already; no work.

## 4. Config schema

New file: **`config/datacleaner/adjustment_thresholds.json`**. Mirrors the existing style
(`version` + `description` + grouped nested objects, e.g. `lease_estimation.json`,
`flag_conditions.json`). Flat documented map of threshold name → value, grouped by adjuster.

```jsonc
{
  "version": "1.0.0",
  "description": "Externalized datacleaner adjuster materiality / review gate thresholds (TDB-5). Absent file OR absent key falls back to the in-code default, which equals the pre-TDB-5 hardcoded constant — production behaviour is byte-identical until an override is supplied.",
  "updated_at": "2026-06-08T00:00:00Z",

  "asset": {
    "a1_goodwill": {
      "materiality_ratio": 0.05,        // gate: goodwill/assets must exceed to fire (assets.go:354)
      "significance_flag_ratio": 0.10   // gate: emit significance flag (assets.go:400)
    },
    "a2_intangible": {
      "materiality_ratio": 0.02         // gate (assets.go:753)
    },
    "a4_dta": {
      "materiality_ratio": 0.05,        // gate (assets.go:892)
      "significance_flag_ratio": 0.10   // gate (assets.go:932)
    },
    "a6_right_of_use": {
      "materiality_ratio": 0.05,        // gate (assets.go:487)
      "significance_flag_ratio": 0.10   // gate (assets.go:534)
    },
    "reviews": {
      "rd_capitalization_ratio": 0.10,    // gate (assets.go:1140)
      "capitalized_software_ratio": 0.015 // gate (assets.go:~1245)
    }
  }
}
```

Design notes:
- **Grouped by adjuster** (`asset.a1_goodwill.*`), mirroring `lease_estimation.json`'s nested
  grouping. Keys are snake_case per the existing JSON convention.
- **`version`** field required (matches `Validate()` in both existing loaders).
- Only the **asset** group exists in the first cut (all in-scope gates are A-rules; the
  liability-side B1/B2/B3 gates are industry-keyed and deferred). A future `liability:` /
  `earnings:` group slots in additively without breaking the loader.
- JSON does not support comments; the `//` annotations above are for the spec only. The shipped
  file is plain JSON with a populated `description`.

### 4.1 Schema validation (mirror the `schema.json` convention)

The datacleaner config dir already ships `config/datacleaner/schema.json` (JSON Schema for
`rules.json`). For TDB-5 we mirror the **Go `Validate()` method** convention used by both existing
loaders (`FlagConditionsConfig.Validate()` / `IndustryCodeConfig.Validate()`) rather than a separate
JSON-Schema file — it is the dominant pattern for these per-loader configs and keeps validation next
to the load. `Validate()` enforces:
- `version` non-empty (consistent with both existing loaders).
- every present ratio is in the half-open range `(0, 1]` (a materiality ratio of 0 or > 1 is a
  config error; reject rather than silently mis-gate). Absent keys are **not** an error — they fall
  back to defaults.

A future contributor may add a `adjustment_thresholds.schema.json` if the team wants editor
validation; it is not required for this cut (Open Question 4).

## 5. Loader + injection design

### 5.1 Loader — `internal/config/adjustment_thresholds_config.go`

A direct mirror of `internal/config/flag_conditions_config.go`:

```go
package config

// AdjustmentThresholdsConfig is the externalized datacleaner gate-threshold config (TDB-5).
type AdjustmentThresholdsConfig struct {
    Version     string               `json:"version"`
    Description string               `json:"description,omitempty"`
    Asset       AssetThresholdConfig `json:"asset"`
}

type AssetThresholdConfig struct {
    A1Goodwill   GoodwillThresholds `json:"a1_goodwill"`
    A2Intangible RatioThreshold     `json:"a2_intangible"`
    A4DTA        GoodwillThresholds `json:"a4_dta"`         // reuses {materiality, significance}
    A6RightOfUse GoodwillThresholds `json:"a6_right_of_use"`
    Reviews      ReviewThresholds   `json:"reviews"`
}
// ... GoodwillThresholds{MaterialityRatio, SignificanceFlagRatio *float64}, etc.
// Pointer fields (*float64) distinguish "absent" (nil → use default) from "present 0".

func LoadAdjustmentThresholdsConfig(configPath string) (*AdjustmentThresholdsConfig, error) {
    if configPath == "" {
        configPath = os.Getenv("ADJUSTMENT_THRESHOLDS_CONFIG_PATH")
        if configPath == "" {
            configPath = "config/datacleaner/adjustment_thresholds.json"
        }
    }
    data, err := os.ReadFile(configPath)
    if err != nil { return nil, fmt.Errorf("failed to read adjustment thresholds config file: %w", err) }
    var c AdjustmentThresholdsConfig
    if err := json.Unmarshal(data, &c); err != nil { return nil, fmt.Errorf("failed to parse: %w", err) }
    if err := c.Validate(); err != nil { return nil, fmt.Errorf("invalid: %w", err) }
    return &c, nil
}
```

**Pointer fields (`*float64`)** are the missing-vs-zero discriminator (the same idiom DC-1 uses on
`Facts` and `rule.Threshold.PercentageOfRevenue`). `nil` → "key absent, use default"; non-nil → use
the operator's value. A present `0` is caught by `Validate()` as out-of-range.

### 5.2 The resolved threshold carrier — `adjustments.AssetThresholds`

The adjuster package gets a small **plain-value** resolved struct (no pointers — every field is a
concrete `float64`, already defaulted). The package owns its default constants (the literals that are
hardcoded today) so the package is self-contained and tests need no config:

```go
package adjustments

// AssetThresholds carries the resolved (defaulted) asset-adjuster gate thresholds.
type AssetThresholds struct {
    GoodwillMateriality       float64
    GoodwillSignificanceFlag  float64
    IntangibleMateriality     float64
    DTAMateriality            float64
    DTASignificanceFlag       float64
    ROUMateriality            float64
    ROUSignificanceFlag       float64
    RDCapitalizationReview    float64
    CapitalizedSoftwareReview float64
}

// DefaultAssetThresholds returns the pre-TDB-5 hardcoded constants. This is the
// single source of truth for "behaviour when config is absent".
func DefaultAssetThresholds() AssetThresholds {
    return AssetThresholds{
        GoodwillMateriality: 0.05, GoodwillSignificanceFlag: 0.10,
        IntangibleMateriality: 0.02,
        DTAMateriality: 0.05, DTASignificanceFlag: 0.10,
        ROUMateriality: 0.05, ROUSignificanceFlag: 0.10,
        RDCapitalizationReview: 0.10, CapitalizedSoftwareReview: 0.015,
    }
}
```

### 5.3 Injection — constructor + struct field

`AssetAdjuster` gains a `thresholds AssetThresholds` field (replacing the
`// TODO: Add configuration for adjustment thresholds` comment). The constructor becomes:

```go
type AssetAdjuster struct {
    thresholds AssetThresholds
}

// NewAssetAdjuster constructs with the default (pre-TDB-5) thresholds.
// Existing zero-arg callers (tests, pipeline.go) are unchanged.
func NewAssetAdjuster() *AssetAdjuster {
    return &AssetAdjuster{thresholds: DefaultAssetThresholds()}
}

// NewAssetAdjusterWithThresholds constructs with explicit thresholds (production wiring).
func NewAssetAdjusterWithThresholds(t AssetThresholds) *AssetAdjuster {
    return &AssetAdjuster{thresholds: t}
}
```

**Backward-compatibility decision:** keep `NewAssetAdjuster()` zero-arg with default thresholds so
the ~40 existing test call sites and `pipeline.go` compile unchanged. Add a sibling
`...WithThresholds` constructor for the production path. This mirrors the codebase's existing
`WithAI(...)` additive-builder convention on `LiabilityAdjuster` and avoids touching dozens of test
files. (Alternative — a variadic option — is heavier; rejected for KISS.)

Each `Apply*` method swaps its literal for the field:
- `assets.go:354` `threshold := 0.05` → `threshold := aa.thresholds.GoodwillMateriality`
- `assets.go:400` `if goodwillRatio >= 0.10` → `>= aa.thresholds.GoodwillSignificanceFlag`
- `assets.go:753` `threshold := 0.02` → `aa.thresholds.IntangibleMateriality`
- `assets.go:892` `threshold := 0.05` → `aa.thresholds.DTAMateriality`
- `assets.go:932` `if dtaRatio >= 0.10` → `>= aa.thresholds.DTASignificanceFlag`
- `assets.go:487` A6 `const threshold = 0.05` → `aa.thresholds.ROUMateriality` (drops `const`)
- `assets.go:534` A6 `if rouRatio >= 0.10` → `>= aa.thresholds.ROUSignificanceFlag`
- `assets.go:1140` A-RD `threshold := 0.10` → `aa.thresholds.RDCapitalizationReview`
- `assets.go:~1245` A-SW `threshold := 0.015` → `aa.thresholds.CapitalizedSoftwareReview`

### 5.4 Resolver — config → resolved struct (default fallback)

A pure function in `internal/config` (or a small `adjustments` helper fed the config struct) maps
the pointer-field config onto the plain resolved struct, substituting the default for every `nil`:

```go
// ResolveAssetThresholds applies cfg over the defaults. cfg may be nil (→ all defaults).
func ResolveAssetThresholds(def adjustments.AssetThresholds, cfg *AdjustmentThresholdsConfig) adjustments.AssetThresholds {
    out := def
    if cfg == nil { return out }
    if v := cfg.Asset.A1Goodwill.MaterialityRatio; v != nil { out.GoodwillMateriality = *v }
    // ... one guarded assignment per key ...
    return out
}
```

> Import-direction note: to avoid `config → adjustments` coupling if undesirable, the resolver can
> live **in the `adjustments` package** taking the already-loaded `*config.AdjustmentThresholdsConfig`,
> OR the resolved `AssetThresholds` can be built in `service.go` (which already imports both). The plan
> picks the lowest-coupling option that compiles; either keeps the default-table inside `adjustments`.

### 5.5 Wiring — `NewDataCleanerService` (service.go)

`NewDataCleanerService` already loads `flag_conditions.json` inline with a **warn-and-fallback**
pattern (service.go:65-74). We add the same pattern for thresholds, then resolve:

```go
// TDB-5: load externalized adjuster thresholds; absent/invalid file → defaults (behaviour-preserving).
assetThresholds := adjustments.DefaultAssetThresholds()
if thrCfg, err := config.LoadAdjustmentThresholdsConfig(cfg.DataCleaner.ThresholdsPath); err == nil {
    assetThresholds = config.ResolveAssetThresholds(assetThresholds, thrCfg)
} // else: missing/invalid file is non-fatal — keep defaults (same stance as flag_conditions fallback)

svc := &service{
    assetAdjuster: adjustments.NewAssetAdjusterWithThresholds(assetThresholds),
    ...
}
```

`DataCleanerConfig` gains one optional field `ThresholdsPath string mapstructure:"thresholds_path"`
(empty → loader's default path `config/datacleaner/adjustment_thresholds.json`). `pipeline.go`'s
test-harness construction keeps `NewAssetAdjuster()` (defaults) — no change needed there.

## 6. The default-preserves-behaviour guarantee (load-bearing)

This is the binding constraint. Three independent layers guarantee byte-identical output when no
override is supplied:

1. **Default == constant.** `DefaultAssetThresholds()` returns exactly the literals hardcoded
   today (0.05 / 0.10 / 0.02 / …). A pinned unit test asserts each field equals its documented
   constant, so a future edit that drifts a default fails CI.
2. **Absent file → defaults.** `NewDataCleanerService`'s load is warn-and-fallback: a missing or
   invalid `adjustment_thresholds.json` yields `DefaultAssetThresholds()` unchanged. Tests and the
   shadow/basket suites do **not** ship an override file, so they run on defaults.
3. **Absent key → default.** `ResolveAssetThresholds` only overwrites a field when the config
   pointer is non-nil; every other field keeps its default. A partial config cannot silently zero a
   gate.

**Why DC-1 invariants stay green:** A1/A2/A4/A6 gate at the same ratios → the same rules fire on the
same tickers with the same amounts → `TestDDM_LegacyPath_BitForBit`, `TestRecomputeUmbrellas_NoMutation`,
`TestOrchestrator_LedgerOrdering`, `TestLedger_BasketSnapshot_ClusterPrediction`,
`TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction`, and the recompute-shadow snapshots are
untouched. The **shadow gate stays exit 0** because no shipped config alters any gate, so
`recomputeUmbrellas` observes identical post-clean data and the snapshot bytes are unchanged
(`git diff --quiet internal/integration/testdata/recompute-shadow/`).

**Critical test-hygiene rule:** the regression-safe path requires that **no existing test, and no
shipped production config, supplies an override that changes a gate.** The shipped
`adjustment_thresholds.json` (if we ship one for documentation) MUST contain values byte-equal to
the defaults; the override test uses a **temp file** it writes itself and never commits.

## 7. Test strategy

| Test | Location | Asserts |
|------|----------|---------|
| `TestDefaultAssetThresholds_EqualLegacyConstants` | `adjustments/asset_thresholds_test.go` | each `DefaultAssetThresholds()` field == its documented literal (regression pin) |
| `TestLoadAdjustmentThresholdsConfig_Absent_UsesDefaults` | `config/adjustment_thresholds_config_test.go` | missing path → loader returns error; `ResolveAssetThresholds(def, nil) == def` |
| `TestLoadAdjustmentThresholdsConfig_Validate` | same | empty version rejected; ratio `0` / `>1` rejected; absent key accepted |
| `TestResolveAssetThresholds_PartialOverride` | same | a config with only `a1_goodwill.materiality_ratio` overrides only that field; all others stay default |
| `TestAssetAdjuster_DefaultGate_Unchanged` | `adjustments/asset_thresholds_test.go` | `NewAssetAdjuster()` fires A1 at exactly 0.05 (boundary: 0.05 skip, 0.0501 fire) — proves no behaviour drift |
| `TestAssetAdjuster_OverrideGate` | same | `NewAssetAdjusterWithThresholds({GoodwillMateriality:0.20,...})` makes a ticker that fired at 0.05 now skip — proves the wiring is live |
| existing DC-1 invariants | unchanged | all stay green (no override in the suite) — the regression proof |

Coverage: the new loader + resolver + the modified `Apply*` gates are covered by the above. Per
`CLAUDE.md` the finance-critical modules target ≥90% / ≥80% overall; the new code is small and fully
exercised by default + override + validation + boundary cases.

**Edge cases that MUST have tests:** absent file; present-but-empty `{}` (version validation fails →
fallback); partial override (one key); out-of-range value rejected; boundary gate (ratio exactly at
threshold uses the existing `<=` / `>=` semantics unchanged).

## 8. Self-review (SOLID / KISS / Clean Architecture)

- **No circular deps:** `adjustments` owns its default table; `config` owns the loader/parsed shape;
  `service.go` (which already imports both) composes them. The resolver lives where it avoids a new
  edge — either in `adjustments` (consuming the `config` struct) or in `service.go`.
- **SRP:** loader parses + validates; resolver merges-over-defaults; adjuster applies. One concern each.
- **OCP:** adding a future `liability` group or a deferred industry table is additive (new struct
  fields + new default-table entries), no edit to existing `Apply*` gates beyond reading a new field.
- **KISS:** flat scalar map, pointer-vs-zero defaulting, additive constructor — no viper binding, no
  reflection, no new fx provider. Smallest change that satisfies the issue.
- **Testability:** every threshold path is reachable from a unit test with no live config.
- **Regression safety:** §6 — three independent layers + a pinned default==constant test.

## 9. Open questions (with recommendations)

1. **Industry-keyed tables (Class B) — externalize now or defer?**
   *Recommendation: DEFER.* They are a distinct, richer schema (per-GICS maps) with a wider
   regression surface. Ship the flat-gate mechanism first; a follow-up TDB issue can extend the same
   loader with a `industry_overrides` group. The issue's TODOs sit on the constructors / flat gates,
   not the per-industry helpers.

2. **Treatment rates (A4 50% / A5 40% / A2 tiers / C1 1.5%) — include?**
   *Recommendation: DEFER (out of scope).* These change adjustment *amounts*, not materiality
   *gates*; they belong to a separate "adjustment magnitudes" config with its own review story.

3. **A6 / A-RD / A-SW — include in the first cut or just A1/A2/A4?**
   *Recommendation: INCLUDE all asset gates.* Keeps the A-rule gate surface consistent (one place to
   read every asset gate) and the marginal cost is tiny. A6's TDB-2 "stay as constants" note is
   honoured because the **default is unchanged**. If REVIEWER wants the absolute minimum, A1/A2/A4
   alone satisfy the issue and A6/A-RD/A-SW move to the deferred list — flag this in the PR.

4. **Ship a populated `adjustment_thresholds.json` or leave it absent (defaults-only)?**
   *Recommendation: SHIP a defaults-valued file* (values byte-equal to `DefaultAssetThresholds`) so
   the config surface is discoverable and documented, AND so the "absent file" path and the
   "present file == defaults" path are both exercised. The file must never diverge from defaults
   without a deliberate decision; the `description` field states this. (If the team prefers the file
   absent so missing-file fallback is the only runtime path, that is also safe — the loader handles
   both.)

5. **Resolver location — `adjustments` vs `config` vs `service.go`?**
   *Recommendation:* put `ResolveAssetThresholds` wherever it adds **no new import edge**. The plan
   resolves this concretely at implementation time (likely `config` package taking the parsed struct +
   the `adjustments.AssetThresholds` default, returning the merged struct — `config` does not import
   `adjustments` today, so if that edge is unwanted, resolve in `service.go`).

## 10. Acceptance criteria

- [ ] `config/datacleaner/adjustment_thresholds.json` exists, mirrors the existing JSON style, carries
      `version` + `description`, values byte-equal to the pre-TDB-5 constants.
- [ ] `internal/config/adjustment_thresholds_config.go` loads via the `LoadFlagConditionsConfig`
      pattern (path → env → default), with `Validate()` (version required; ratios in `(0,1]`).
- [ ] `AssetAdjuster` reads each in-scope gate from an injected `AssetThresholds` field; the literals
      at the 9 sites in §5.3 are gone.
- [ ] `NewAssetAdjuster()` stays zero-arg and yields default thresholds; existing call sites compile
      unchanged; `NewAssetAdjusterWithThresholds(...)` exists for production wiring.
- [ ] `NewDataCleanerService` loads the config warn-and-fallback and wires resolved thresholds.
- [ ] Absent / invalid / partial config → defaults; a pinned test proves default == legacy constant.
- [ ] Override test proves the wiring is live (a changed gate changes firing).
- [ ] Full `GOWORK=off go test ./... -count=1` exits 0; all named DC-1 invariants green; the
      recompute-shadow gate (`git diff --quiet internal/integration/testdata/recompute-shadow/`)
      exits 0.
