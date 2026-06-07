# TDB-2 — A6 (ROU assets) + A7 (excess cash) asset-quality adjusters — DESIGN SPEC

**Status:** APPROVED for implementation (design only — no production code in this doc).
**Issue:** GitHub `#2` / tracker `docs/reviewer/TDB-2-missing-a6-rou-a7-excess-cash-adjusters.md`.
**Decision (human, 2026-06-06):** IMPLEMENT both adjusters. Do NOT remove the dangling rules.
**Author role:** ARCH. **Implementer role:** BACKEND (separate `/execute` pass).
**Worktree:** `.claude/worktrees/tdb-2-a6-a7-adjusters` (own `go.mod`; validate with `GOWORK=off`).
**Engine baseline (verified in code, NOT from stale CLAUDE.md):** `CalculationVersion = "4.6"` (two stamp sites: `internal/services/valuation/service.go:1522` standard-DCF, `:1921` alt-model). `CurrentSchemaVersions["FinancialData"] = 9` (`internal/observability/replay/schema.go:53`; stamped at `internal/services/datacleaner/service.go:306`).

---

## 1. Summary

Two `enabled:true` asset-quality rules in `config/datacleaner/rules.json` —
`right_of_use_assets` (A6, `SEC_Guide_A6`) and `excess_cash` (A7, `SEC_Guide_A7`) —
have **no dispatcher arm** in `ProcessAssetAdjustments`. They fall through the
`default: continue` and are silently skipped. Both being `enabled:true` while doing
nothing is a correctness lie. This spec makes both real, as `Adjuster`-interface
implementations following the canonical DC-1 Phase-2..5 pattern, wires them into the
DC-1 view model (`InvestedCapital()` / EV→Equity bridge), and keeps every load-bearing
invariant green.

**Critical findings that shape the design (read before the design sections):**

1. **A7 is almost a no-op under the current engine.** The DCF FCF path *already*
   excludes the **entire** cash balance from operating net-working-capital
   (`calculateNetWorkingCapitalChange`, BUG-014: `(CurrentAssets − Cash) −
   CurrentLiabilities`), and the EV→Equity bridge *already* adds back the **entire**
   cash balance to equity (`CalculateEquityValueWithDebtLikeClaims`, `+ cash`). So the
   engine *today* treats **all** cash as non-operating and credits 100% of it to
   equity holders — which is the correct standard EV→equity convention. A classical
   Damodaran excess-vs-operating-cash split (excess = cash − operating-cash-need)
   would, if applied to the **bridge**, *reduce* equity value by the operating-cash
   portion — which would be **wrong** (operating cash is still a real asset owned by
   shareholders; it is not consumed). A7's defensible scope is therefore **NOT** the
   EV bridge. See §4 for the resolved A7 design (observability + ROIC-invested-capital,
   not the equity bridge).

2. **A6 (ROU) double-counts against B1 (lease debt) if done naively.** B1
   (`operating_leases`, `ApplyB1OperatingLeases`) capitalizes the operating-lease
   **liability** PV into `InvestedCapital().DebtLikeClaims`, which the EV→Equity bridge
   **subtracts**. The ROU **asset** is the asset-side mirror of that same lease. Under
   the standard treatment, capitalizing a lease adds a debt-like claim (B1, subtract
   in bridge) AND an operating asset (ROU, which *stays in* invested capital because
   the leased asset genuinely produces operating capacity). A6's config text —
   "Exclude ROU assets from invested capital as they inflate total assets but don't add
   operating capacity" — is a **conservative analytical stance** that conflicts with
   the B1 treatment. If A6 excludes ROU from invested capital **AND** B1 already added
   the lease liability to DebtLikeClaims (subtracted from EV), the lease is
   double-penalized. See §3 for the resolved A6 design (overlay scoped to
   `TotalAssets`/invested-capital ONLY, with an explicit B1-overlap guard and a
   "do-not-touch-the-bridge" boundary).

---

## 2. Goals / Non-goals / Constraints

### Goals
- A6 + A7 are real `Adjuster`-interface impls, routed in `ProcessAssetAdjustments`
  (no more `default: continue` skip).
- ROU value is stored on the entity (new field `OperatingLeaseRightOfUseAsset`) by the
  SEC parser, FX-converted for FPI tickers, and surfaced through the view model.
- A6's exclusion is realized at the **view level** (`InvestedCapital()`), not by mutating
  an entity umbrella — matching the A1/B1/B2/B3 OverlayEmitter precedent.
- A7 is honored with a concrete, config-driven, defensible mechanic that does **not**
  mis-value cash-rich firms (see §4 — observability flag + ROIC invested-capital
  exclusion, NOT the equity bridge).
- Every load-bearing invariant stays green, OR (shadow snapshots) is regenerated with a
  documented, A6/A7-attributable justification.
- `*_Adjuster_Interface_Contract` tests for both; basket/integration parity preserved.
- No `enabled:true`-but-skipped asset rule remains.

### Non-goals (explicitly out of scope)
- **NO change to the EV→Equity bridge cash term.** A7 does not subtract operating cash
  from the bridge (see §1.1 / §4 — that would be a valuation regression).
- **NO B1 (lease-debt) change.** A6 must not alter B1's `DebtLikeClaims` contribution.
- **NO short-term-investments / marketable-securities isolation.** A7's config lists
  `ShortTermInvestments` / `MarketableSecurities`, but the entity has only
  `CashAndCashEquivalents` (ST investments are absorbed into the `OtherCurrentAssets`
  plug; see BUG-014 §5). A7 operates on `CashAndCashEquivalents` only. ST-investment
  isolation is a deferred parser change (out of scope, noted as an open question).
- **NO lease current/noncurrent split.** Untouched (deferred, per existing parser note).
- **NO refactor of the legacy `CalculateNetTangibleAssets` deprecated method.**
- **NO DDM bit-for-bit golden regeneration.** (Banks fire neither A6 nor A7 materially;
  proven in §6.)

### Constraints
- TDD mandatory (RED→GREEN). Coverage target: ≥90% for finance modules, ≥80% overall
  (repo policy, `midas/CLAUDE.md`).
- Adjusters are mutation-FREE `Apply*` methods; dispatcher owns any dual-write. A6 is an
  OverlayEmitter → emits an `OverlaySpec`, NO dispatcher dual-write (mirrors A1).
- Failure isolation: an adjuster must never panic the cleaner pipeline.
- `GOWORK=off` for isolated build/test in the worktree.

---

## 3. A6 — Right-of-Use Assets (ROU) exclusion design

### 3.1 Role: OverlayEmitter (mirror A1 goodwill exactly)
A6 is an **OverlayEmitter**, structurally identical to A1 goodwill exclusion. It emits a
single `OverlaySpec{Field:"TotalAssets", Operation:"subtract", Amount: rouValue,
AmountSemantics: AmountIncremental}` and a `Fired:true` audit `LedgerEntry` carrying NO
`Component`/`DeltaAmount`/`EquityOffset`. It does **NOT** mutate any entity field. The
dispatcher arm does **NOT** call `applyLedgerComponentDeltas` for a meaningful component
(A6 has no component) — it only drains `NativeOverlays`/`NativeLedgerEntries`/`Flags`,
exactly like the A1 arm minus the goodwill-specific overlay.

Rationale: A6's exclusion is an analytical view adjustment (Damodaran "exclude ROU from
invested capital"), not a restatement of the as-filed balance sheet. The as-reported
balance sheet legitimately carries the ROU asset; only the *invested-capital analytical
view* removes it. This is precisely what `InvestedCapital()` is for.

### 3.2 View-model mechanics
`InvestedCapital()` already handles `Field:"TotalAssets"` + `Operation:"subtract"` via
`applyOverlayToView` (it does `v.TotalAssets += signed; v.Goodwill = 0;
v.TangibleAssets = v.TotalAssets − v.OtherIntangibles`). **A6 must NOT reuse the
`TotalAssets` arm verbatim**, because that arm hard-zeroes `Goodwill` (an A1-specific
Damodaran side effect). A6 needs a `TotalAssets` subtraction WITHOUT the goodwill zero.

**Decision:** route A6 through a dedicated overlay `Field` value to keep the two effects
independent. Use `Field:"InvestedCapitalExclusion"` (a new, A6-scoped overlay-field
constant) consumed by a NEW switch arm in `applyOverlayToView` that does ONLY
`v.TotalAssets += signed; v.TangibleAssets = v.TotalAssets − v.Goodwill −
v.OtherIntangibles` (no goodwill zero). This avoids coupling A6 to A1's goodwill
side-effect and avoids accidental double-effects when both A1 and A6 fire on the same
ticker.

> Alternative considered & rejected: reuse `Field:"TotalAssets"`. Rejected because A1's
> arm zeroes Goodwill — a same-ticker A1+A6 fire would zero goodwill twice (idempotent)
> but, more importantly, couples A6's correctness to A1's side-effect ordering. A
> dedicated field is clearer and order-independent.

### 3.3 What value does A6 exclude? — the B1 double-count guard (LOAD-BEARING)
A6 excludes the **ROU asset book value** (`OperatingLeaseRightOfUseAsset`) from invested
capital. The **double-count risk** is with B1, NOT within A6:

- B1 adds the **lease liability PV** to `DebtLikeClaims` → the EV→Equity bridge
  **subtracts** it (reduces equity).
- A6 subtracts the **ROU asset** from `InvestedCapital().TotalAssets` (used by the
  WACC capital-structure denominator path and ROIC, NOT the EV→Equity equity bridge —
  see §3.4 for the exact consumer boundary).

These touch **different** view fields (`DebtLikeClaims` vs `TotalAssets`) and **different**
downstream consumers, so they do **not** arithmetically double-count *in the same term*.
The genuine economic concern is conceptual symmetry: capitalizing a lease should add a
debt-like claim (B1) and keep the operating asset (ROU stays). A6's config stance
("ROU doesn't add operating capacity") deliberately removes the asset — a more
conservative invested-capital base that *raises* ROIC and shrinks the capital base.

**Guard (must ship):** A6's `Apply` records, in the fired `LedgerEntry.SkipMetrics`, both
`rou_value` and `operating_lease_liability` (read from `working.OperatingLeaseLiability`)
so the B1/A6 overlap is observable per-ticker. A6 does **not** read or alter B1's
overlay. The spec explicitly accepts the conservative double-treatment (lease penalized
on the liability side via B1, asset removed via A6) as the **intended** A6 semantics per
the config description, and documents it loudly. If a future decision wants symmetric
treatment (keep ROU when B1 fires), that is a config/spec change, not a silent default.

### 3.4 Consumer boundary (which views read A6's exclusion)
- `InvestedCapital().TotalAssets` reflects the A6 exclusion (overlay applied).
- **EV→Equity equity bridge is UNTOUCHED.** The bridge reads
  `InvestedCapital().DebtLikeClaims` (A6 contributes 0 there), `waccRestated.InterestBearingDebt`
  (`Restated()`, A6 contributes 0), and `latestFinancialData.CashAndCashEquivalents`
  (entity, A6 contributes 0). A6 emits a `TotalAssets`-class overlay, NOT a
  `DebtLikeClaims` overlay, so `dcfValuePerShare` is **bit-for-bit unchanged** by A6.
- **ROIC** (`growth.CalculateInvestedCapital` at `service.go:652`) currently reads
  `roicView.StockholdersEquity + roicView.InterestBearingDebt − cash` from `Restated()`.
  A6's exclusion lives on `InvestedCapital()`, which ROIC does **not** read today.
  **Decision:** do NOT change the ROIC read site in this task (keeps ROIC on
  `Restated()`, byte-identical). A6's exclusion therefore has **no per-share or ROIC
  numeric effect today** — it is realized only in `InvestedCapital().TotalAssets`, which
  is consumed by no production per-share path yet. This is intentional and matches the
  A1 precedent: A1's `InvestedCapital()` goodwill exclusion is likewise only consumed by
  the WACC/bridge path through `DebtLikeClaims`, not through `TotalAssets`.

> Honest assessment: like A1's `TotalAssets` overlay, A6's exclusion is currently a
> **view-level + audit-trail + flag** change with no live per-share consumer. Making it
> *numerically* bite would require migrating a per-share consumer (e.g. ROIC or a future
> asset-based cross-check) to read `InvestedCapital().TotalAssets` — out of scope here and
> called out as an open question (§9 Q3). What ships now: the rule fires, is audited,
> flagged, and view-visible; it no longer lies about being enabled.

### 3.5 Threshold / materiality / sign
- **Sign:** subtract (ROU reduces invested capital).
- **Materiality gate:** fire only when `rouRatio = rou / TotalAssets > 0.05` (5%, mirrors
  A1 goodwill threshold). Below threshold → `Fired:false` skip with
  `SkipMetrics{rou_ratio, threshold}`. Zero/absent ROU → `Fired:false` skip.
- **Flag:** emit one `info`-severity flag (config `severity: info`) when fired and
  `rouRatio >= 0.10`, with `Type:"right_of_use_exclusion"`. Mirror A1's
  `>= 0.10`-gated flag pattern.
- **No config threshold today.** A6's rules.json entry has no `threshold` block; the
  5%/10% gates are code constants matching A1. (Could be promoted to config later — open
  question §9 Q4.)

### 3.6 Entity + parser changes (A6)
- **Entity** (`internal/core/entities/financial_data.go`): add
  `OperatingLeaseRightOfUseAsset float64 \`json:"operating_lease_right_of_use_asset,omitempty"\``
  in the Liability-Completeness / lease block (near `OperatingLeaseLiability`,
  conceptually the asset side of B1).
- **Parser** (`internal/infra/gateways/sec/parser.go`): add a `findValue` block right
  after the operating-lease-liability block (~line 784):
  ```
  // Right-of-Use assets (ASC 842 / IFRS 16) — A6 (TDB-2). Stored as a
  // PARALLEL informational field; deliberately NOT subtracted from the
  // computePlugs OtherNonCurrentAssets residual (see §3.7), so the
  // TotalAssets == sum(components)+plug invariant is unchanged.
  if val, exists := p.findValue(data, []string{
      "OperatingLeaseRightOfUseAsset",
      "RightOfUseAssets",
      "OperatingLeaseRightOfUseAssetAfterAccumulatedAmortization",
      // IFRS 16 equivalent
      "RightofuseAssets",
  }); exists {
      financialData.OperatingLeaseRightOfUseAsset = val
  }
  ```
  `us-gaap:OperatingLeaseRightOfUseAsset` is already in `GetSupportedConcepts` (line
  1001) but never stored — this closes that gap.
- **FX** (`internal/services/valuation/currency.go`): add
  `fd.OperatingLeaseRightOfUseAsset *= rate` to the asset block (near the lease
  liability conversions at ~line 242), so FPI tickers (TSM, ASML, …) get a USD ROU value.

### 3.7 Plug / shadow-snapshot interaction (LOAD-BEARING — read carefully)
Today the ROU asset is absorbed into the `OtherNonCurrentAssets` **plug** (Plug 2 in
`computePlugs`), because ROU is not a typed component. **Decision: leave `computePlugs`
and `recomputeUmbrellas` UNCHANGED.** The new `OperatingLeaseRightOfUseAsset` field is a
**parallel informational field** — it is NOT added to `nonCurrentAssetsComponents` and NOT
subtracted from the `OtherNonCurrentAssets` residual. Consequences:
- `TotalAssets == sum(typed components) + plug` invariant is **unchanged** (ROU still
  lives inside the `OtherNonCurrentAssets` plug; the dedicated field is a duplicate
  *read-only mirror* used solely for A6's overlay amount).
- `recomputeUmbrellas` shadow output is **unchanged by the entity-field addition itself**
  (the shim sums typed components + plug; the new field is in neither).
- **A6 fires an overlay but mutates NO entity field**, so it does NOT change any value the
  shadow shim observes. → **Shadow snapshots are NOT affected by A6.** No regeneration
  for A6.

> Why not carve ROU out of the plug into a real component? Because that WOULD change the
> `OtherNonCurrentAssets` residual on every ticker with ROU and force a shadow-snapshot
> regeneration + a `Restated()` recompute change — a much larger blast radius for zero
> per-share benefit (A6's overlay only needs the *amount*, which the parallel field
> supplies). Promoting ROU to a typed component is deferred (open question §9 Q5).

---

## 4. A7 — Excess cash design (resolved to NOT touch the equity bridge)

### 4.1 The trap (why the obvious design is wrong)
The tracker says A7 "feeds the EV→Equity bridge". The naive reading — subtract
*operating* cash from the bridge's `+ cash` term so only *excess* cash is credited — is a
**valuation regression**. Operating cash is a real asset owned by shareholders; under the
standard EV→equity bridge, the *entire* cash balance is added back (the engine does this
today, correctly). "Excess vs operating" cash matters for **enterprise-value
normalization and return metrics** (you exclude operating cash from invested capital so
ROIC isn't diluted), NOT for the equity bridge. Subtracting operating cash from the
bridge would understate equity for every cash-rich firm — the exact failure mode BUG-014
fixed in the *other* direction.

### 4.2 Resolved A7 mechanic
A7 is an **OverlayEmitter** that emits a single `OverlaySpec{Field:"ExcessCash",
Operation:"identify", Amount: excessCash, AmountSemantics: AmountIncremental}` plus a
`Fired:true` audit `LedgerEntry` and one `info` flag. The overlay is consumed ONLY by:
- **Observability / audit trail** (`adjustmentsFromLedger` projection →
  `ValuationResult.CleaningAdjustments`) — the INTERNAL audit carrier truthfully records
  "A7 identified $X excess cash". This is the primary deliverable: stop lying about
  `enabled:true`. **NOTE (REVIEWER, 2026-06-07):** `CleaningAdjustments` is NOT currently
  wired into the HTTP `FairValueResponse` (`buildFairValueResponse` does not map it; the
  response struct has no `cleaning_adjustments` field) — a PRE-EXISTING gap shared by
  A1/B1/B2/B3, not introduced by TDB-2. The A6/A7 audit therefore reaches the internal
  result, the ledger, the cleaner logs, and replay bundles, but NOT the public API
  response yet. Exposing `cleaning_adjustments` on the API is a separate follow-up that
  also covers A1/B-rules (see the tracker).
- **`InvestedCapital()`** — a NEW `Field:"ExcessCash"` arm that records excess cash on a
  new `FinancialDataView.ExcessCash` field (informational; see §4.5). It does **NOT**
  alter `TotalAssets`, `DebtLikeClaims`, or any bridge term.

**A7 makes NO change to any per-share value today.** Like A6, it is a fire + audit + flag
+ view-field change. This is the honest, defensible scope: it honors the rule (no more
silent skip) without mis-valuing cash-rich firms.

### 4.3 Excess-cash formula (config-driven, Damodaran operating-cash floor)
```
operatingCashNeed = max(0, operatingCashPct * Revenue)     // operatingCashPct from config
excessCash        = max(0, CashAndCashEquivalents − operatingCashNeed)
```
- `operatingCashPct` is read from the rule's `Threshold.PercentageOfRevenue` (already
  present in `config/datacleaner/rules.json`: `"percentage_of_revenue": 0.1` → 10% of
  revenue is treated as operating cash; everything above is excess). When the threshold
  is absent or `Revenue <= 0`, A7 treats **all** cash as excess (`excessCash = Cash`) —
  consistent with the engine's existing "all cash is non-operating" stance and a safe
  default.
- **Materiality gate:** fire only when `excessCash > 0` AND
  `excessCash / max(Revenue, 1) ` is finite. Zero/absent cash → `Fired:false` skip.
- **Sign:** the excess is a positive magnitude recorded on the overlay; no field is
  reduced (it is non-operating-cash *identification*, not removal from the bridge).

### 4.4 Why config threshold over a hard-coded constant
A7 already carries `percentage_of_revenue: 0.1` in config — wiring it makes the rule
configurable per the project's "externalize tenant/runtime config" convention and avoids
a magic number. The formula and default (all-cash-excess when `Revenue<=0`) are stated so
a wrong threshold can't silently mis-classify.

### 4.5 Entity / parser / view (A7)
- **No new entity field.** A7 reads `CashAndCashEquivalents` (already parsed at
  `parser.go:635`) and `Revenue` (already parsed). No parser change for A7.
- **No FX change for A7** (cash + revenue are already FX-converted in `currency.go`).
- **View:** add `FinancialDataView.ExcessCash float64` and a `Field:"ExcessCash"` arm in
  `applyOverlayToView` (replacement-semantics: set `v.ExcessCash = o.Amount`). Identity
  on `AsReported`/`Restated` (zero). Informational only.

### 4.6 A7 does NOT change NWC or the bridge — proof
- `calculateNetWorkingCapitalChange` already subtracts the **full** cash balance on both
  periods → A7 changes nothing there.
- `CalculateEquityValueWithDebtLikeClaims` adds the **full** cash balance → A7 emits no
  `cash`-affecting or `DebtLikeClaims`-affecting overlay → bridge unchanged.
- Therefore `dcf_value_per_share`, `EquityValue`, `EnterpriseValue` are **bit-for-bit
  unchanged** by A7.

---

## 5. Dispatcher wiring (both)

Add two arms to the `switch rule.ID` in `ProcessAssetAdjustments`
(`internal/services/datacleaner/adjustments/assets.go:1064`), mirroring the A1 arm
(OverlayEmitter — drain natives, NO meaningful `applyLedgerComponentDeltas`, NO tangible
recompute since neither emits a component delta):

```
case "right_of_use_assets":
    out, err := aa.ApplyA6RightOfUseAssets(applyCtx, data, rule, cleaningCtx)
    if err != nil { continue }
    allFlags = append(allFlags, out.Flags...)
    nativeLedger = append(nativeLedger, out.LedgerEntries...)
    nativeOverlays = append(nativeOverlays, out.Overlays...)
    nativelyEmittedRuleIDs[rule.ID] = true
    continue   // OverlayEmitter — no component delta, no tangible recompute

case "excess_cash":
    out, err := aa.ApplyA7ExcessCash(applyCtx, data, rule, cleaningCtx)
    if err != nil { continue }
    allFlags = append(allFlags, out.Flags...)
    nativeLedger = append(nativeLedger, out.LedgerEntries...)
    nativeOverlays = append(nativeOverlays, out.Overlays...)
    nativelyEmittedRuleIDs[rule.ID] = true
    continue
```

Both arms `continue` (like the FlagEmitter arms) because neither produces a Restater
component delta — the post-switch tangible recompute must NOT fire. The orchestrator
(`applyActiveAdjustments`) already drains `NativeOverlays` onto `data.Overlays` and
computes the firing signal via `nativeFired(...)` generically — no orchestrator change
needed (overlays count as a fire signal).

Add two per-rule adapter structs (`a6RightOfUseAdjuster`, `a7ExcessCashAdjuster`) with
`Name()`/`Apply()` delegating to the new `Apply*` methods + compile-time
`var _ Adjuster = ...` assertions, mirroring `a1GoodwillAdjuster`. Add the AdjusterID
constants:
```
adjusterIDA6RightOfUseExclusion = "A6_right_of_use_exclusion"
adjusterIDA7ExcessCash          = "A7_excess_cash"
```

### 5.1 Adjustment projection meta rows (both)
Add two rows to `perRuleAdjustmentMeta`
(`internal/services/datacleaner/adjustment_projection.go`), OverlayEmitter family
(`AmountSource: amountOverlayAmount`, `PercentageMode: percentageAbsent`, reasoning
sourced from the overlay via `reasoningFromOverlay`):
```
"A6_right_of_use_exclusion": {Category: AssetQuality, Type: Exclude,
    FromAccount: "OperatingLeaseRightOfUseAsset", ToAccount: "",
    PercentageMode: percentageAbsent, AmountSource: amountOverlayAmount},
"A7_excess_cash": {Category: AssetQuality, Type: Exclude,
    FromAccount: "CashAndCashEquivalents", ToAccount: "",
    PercentageMode: percentageAbsent, AmountSource: amountOverlayAmount},
```
Without these rows the projection silently skips the overlays and the public
`CleaningAdjustments` audit trail would omit A6/A7 (the basket-parity test would catch a
*regression* but not a silent omission of a never-before-emitted rule — so the rows are
mandatory, not optional).

---

## 6. Invariant strategy (verdict per invariant)

| Invariant | Verdict | Why |
|---|---|---|
| `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) | **byte-identical, no change** | Banks: ROU ≈ 0 relative to assets (no material operating leases on bank balance sheets) → A6 skip path (below 5%). A7 fires only as an overlay that does NOT touch DDM's `IntrinsicValuePerShare`/`EquityValue`/`EnterpriseValue` (dividend-derived; bridge `+DebtLikeClaims` only). **Proof obligation in the plan:** add `TestDDM_*_UnaffectedByA6A7` asserting the JPM/BAC/WFC fixtures produce no A6/A7 fired overlay AND `Float64bits` equality holds. If a bank fixture *does* carry material ROU, the fixture is patched-DPS synthetic data (per CLAUDE.md) and A6 firing there still cannot move DDM output (A6 overlays `TotalAssets`-class, not DDM inputs). |
| `TestRecomputeUmbrellas_NoMutation` | **byte-identical, no change** | A6/A7 are mutation-free; neither writes `data.*`. The new ROU field is parallel (not in the shim's component sum). |
| Shadow snapshots (`testdata/recompute-shadow/*.json`) | **byte-identical, NO regeneration** | A6/A7 mutate no entity field; the new ROU field is NOT in `computePlugs`/`recomputeUmbrellas` (§3.7). **Plan must assert `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0** after the change. If it does NOT, STOP — it means an unintended plug/recompute interaction crept in; do not regenerate to paper over it. |
| `TestOrchestrator_LedgerOrdering` (asset→liability→earnings) | **green** | A6/A7 entries append in asset-category order via the existing native drain. |
| `TestLedger_BasketSnapshot_ClusterPrediction` / `_T2BS3_RestatedReconstruction` | **green; may need golden update IF A6/A7 fire on a basket ticker** | A6/A7 add new `Overlays`/`AdjustmentLedger` entries on tickers that fire (e.g. AAPL/F may carry material ROU). The basket snapshot pins ledger shape. **Plan must run the basket test, inspect the diff, and update the golden ONLY for A6/A7-attributable additions** (new `A6_*`/`A7_*` ledger entries + overlays), never for pre-existing-rule drift. T2-BS-3 AMD $9.679B / KO $60.912B `Restated().TotalLiabilities` must stay byte-identical (A6/A7 touch assets/overlays, not liabilities). |
| `dcf_value_per_share` / `EquityValue` / `EnterpriseValue` (any ticker) | **byte-identical** | Neither A6 nor A7 emits a `DebtLikeClaims`/`cash`/`debt` overlay or mutates a bridge input (§3.4, §4.6). |
| `tangible_value_per_share` | **byte-identical** | Reads `AsReported()` (parser-verbatim); A6/A7 don't mutate the entity. |
| Plug invariants (`datacleaner_plug_invariants_test.go`) | **green** | `computePlugs` unchanged; ROU not in the residual. |

**DDM-firing proof method (plan task):** instrument the DDM bit-for-bit fixtures to assert
`for entry in ledger: entry.AdjusterID not in {A6,A7} OR entry.Fired == false`. If any
bank fixture fires A6/A7, prove the DDM output bits are still equal (they must be, by
construction). This is the §6 guarantee made testable.

---

## 7. CalculationVersion / SchemaVersion decision

- **SchemaVersion `FinancialData` 9 → 10 (REQUIRED).** Adding the
  `OperatingLeaseRightOfUseAsset` JSON field changes the serialized shape of
  `FinancialData`. Bump `CurrentSchemaVersions["FinancialData"]` in
  `internal/observability/replay/schema.go:53` **atomically** with the stamp site
  `b.AddSchemaVersion("FinancialData", 10)` at
  `internal/services/datacleaner/service.go:306`, per the `feedback_schema_version_atomic_bump`
  convention (the bump lands in the PR that POPULATES the field — i.e. this one). The
  field is `omitempty`, so bundles without ROU are unaffected; replay drift on
  ROU-carrying tickers is expected → use `--allow-schema-drift` on the first sweep.
- **CalculationVersion 4.6 → UNCHANGED (NO bump).** A6 and A7 are **API-behavior-preserving
  for every per-share value** (§3.4, §4.6 — bit-for-bit `dcf_value_per_share`). They add
  audit-trail (`CleaningAdjustments`) + flags + view fields, none of which feed a
  numeric per-share output today. Per the engine convention (4.4→4.6 bumps each
  corresponded to a per-share-numeric change), there is no per-share numeric change → no
  CalcVersion bump. **The public response DOES gain new `CleaningAdjustments` entries and
  flags for A6/A7-firing tickers** — this is an *additive audit-trail* change, not a
  valuation change. If REVIEWER deems the new audit entries a "calculation-visible"
  change warranting a stamp, bump to `4.7` (open question §9 Q2 — recommended default:
  NO bump).

---

## 8. Behavior-change analysis (which tickers move, expected direction)

| Surface | Effect | Tickers most affected |
|---|---|---|
| `dcf_value_per_share`, `EquityValue`, `EnterpriseValue` | **none (bit-for-bit)** | all |
| `CleaningAdjustments` (public audit array) | **+1 entry** when A6 fires (`Type:exclude`, `FromAccount:OperatingLeaseRightOfUseAsset`) and/or **+1** when A7 fires (`FromAccount:CashAndCashEquivalents`) | A6: lease-heavy retail/airlines/restaurants (e.g. F, AAPL has material ROU); A7: cash-rich tech (AAPL, MSFT, GOOG) |
| `Flags` | **+1 `info` flag** per fired rule | same as above |
| `InvestedCapital().TotalAssets` (view) | **lower** by ROU when A6 fires | lease-heavy |
| `InvestedCapital().ExcessCash` (new view field) | **populated** when A7 fires | cash-rich |
| Replay bundles | new `operating_lease_right_of_use_asset` field on FinancialData JSON | all ROU-carrying |

No ticker's fair value moves. The product change is a richer, truthful INTERNAL audit
trail (`ValuationResult.CleaningAdjustments`) + ledger entries + flags + view fields on
lease-heavy and cash-rich names, plus a new entity/view field on FinancialData. This is
visible in the internal result, cleaner logs, and replay bundles — but NOT yet on the
public HTTP `FairValueResponse` (see the §4.2 NOTE: wiring `cleaning_adjustments` into the
API is a separate, pre-existing follow-up that also covers A1/B-rules).

---

## 9. Open questions (need human decision before / during execution)

- **Q1 (A7 scope — recommended default chosen):** Confirm A7 stays an
  **observability/view** adjuster and does **NOT** alter the EV→Equity bridge cash term.
  *Recommended default: YES, do not touch the bridge* (subtracting operating cash from
  the bridge is a valuation regression, §4.1). **This is the most important decision.**
- **Q2 (CalcVersion):** A6/A7 add audit entries + flags but no per-share numeric change.
  Bump `4.6 → 4.7` or leave at `4.6`? *Recommended default: leave at 4.6* (no per-share
  numeric change). Bump only if REVIEWER wants the new audit entries version-stamped.
- **Q3 (A6 numeric bite):** Should A6's exclusion be made *numerically* live by migrating
  ROIC (or a new asset-based cross-check) to read `InvestedCapital().TotalAssets`?
  *Recommended default: NO, out of scope* (matches A1, which is also view-only on
  `TotalAssets`). Track as a follow-up.
- **Q4 (A6 thresholds in config):** A6 has no `threshold` block; the 5%/10% gates are
  code constants (mirroring A1). Promote to config? *Recommended default: code constants
  now* (consistency with A1); promote later if other thresholds move to config.
- **Q5 (ROU as typed component):** Promote ROU from the `OtherNonCurrentAssets` plug to a
  typed component (changing `computePlugs`/`Restated()`/shadow snapshots)?
  *Recommended default: NO, keep parallel field* (zero per-share benefit, large blast
  radius, §3.7). Defer.
- **Q6 (ST investments / marketable securities for A7):** The config lists
  `ShortTermInvestments`/`MarketableSecurities` but the entity has only
  `CashAndCashEquivalents` (ST invest absorbed into the `OtherCurrentAssets` plug). A7
  uses cash only. *Recommended default: cash-only now*; isolating ST investments is a
  deferred parser change.

---

## 10. Acceptance criteria (testable)

- [ ] `right_of_use_assets` and `excess_cash` are routed in `ProcessAssetAdjustments`
      (no `default: continue` skip); each has a per-rule adapter + AdjusterID + Apply
      method + `*_Adjuster_Interface_Contract` test.
- [ ] Entity has `OperatingLeaseRightOfUseAsset`; parser stores it (verified on a
      ROU-carrying fixture); `currency.go` FX-converts it.
- [ ] A6 emits an `InvestedCapitalExclusion`-field overlay; `InvestedCapital().TotalAssets`
      is reduced by the ROU value when fired; `Goodwill` is NOT zeroed by A6.
- [ ] A7 emits an `ExcessCash`-field overlay using the config
      `percentage_of_revenue` floor; `FinancialDataView.ExcessCash` is populated when
      fired; bridge terms unchanged.
- [ ] `dcf_value_per_share`, `EquityValue`, `EnterpriseValue` are **bit-for-bit**
      unchanged on a representative set (AAPL + a lease-heavy ticker) vs. pre-change.
- [ ] `TestDDM_LegacyPath_BitForBit` green; new `TestDDM_*_UnaffectedByA6A7` proves
      banks don't move.
- [ ] `git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0
      (shadow snapshots byte-identical — A6/A7 mutate nothing the shim observes).
- [ ] Basket snapshot updated ONLY for A6/A7-attributable ledger/overlay additions;
      T2-BS-3 AMD/KO `Restated().TotalLiabilities` byte-identical.
- [ ] `CleaningAdjustments` audit array gains A6/A7 entries on firing tickers (projection
      meta rows present).
- [ ] `SchemaVersion FinancialData` bumped 9 → 10 at both the producer
      (`datacleaner/service.go:306`) and `CurrentSchemaVersions` (`replay/schema.go:53`).
- [ ] `GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./...`
      all green in the worktree.
- [ ] No `enabled:true`-but-skipped asset rule remains (grep audit).

---

## 11. References (load-bearing files)

- Dispatcher + canonical pattern: `internal/services/datacleaner/adjustments/assets.go`
  (`ProcessAssetAdjustments` switch `:1064`; A1 `ApplyA1Goodwill` `:319`).
- Adjuster interface: `internal/services/datacleaner/adjustments/adjuster.go`.
- Ledger/overlay entities + roles: `internal/core/entities/adjustment_ledger.go`.
- View model: `internal/services/datacleaner/cleaneddata/{view.go,asreported.go,restate.go,invested_capital.go,cleaned.go}`.
- Plug residuals: `internal/infra/gateways/sec/plugs.go`; shadow shim:
  `internal/services/datacleaner/recompute.go`.
- Entity: `internal/core/entities/financial_data.go` (cash `:180`; lease block `:77-89`).
- Parser: `internal/infra/gateways/sec/parser.go` (lease block `:774`; cash `:626`;
  `GetSupportedConcepts` ROU `:1001`; `computePlugs` call `:854`).
- FX: `internal/services/valuation/currency.go` (asset block `:220`, lease block `:242`).
- EV→Equity bridge: `internal/services/valuation/service.go` (`:1436-1459`);
  NWC: `:2384-2468`; ROIC `:650-657`; CalcVersion stamps `:1522`, `:1921`.
- DCF equity-value: `pkg/finance/dcf/dcf.go` (`CalculateEquityValueWithDebtLikeClaims:238`).
- Projection: `internal/services/datacleaner/adjustment_projection.go`
  (`perRuleAdjustmentMeta:112`).
- Firing signal: `internal/services/datacleaner/firing_signal.go`.
- Orchestrator native drain: `internal/services/datacleaner/service.go`
  (`applyActiveAdjustments:479`; schema stamp `:306`).
- Config rules: `config/datacleaner/rules.json` (A6 `:93-104`, A7 `:106-120`).
- Schema registry: `internal/observability/replay/schema.go` (`:53`).
