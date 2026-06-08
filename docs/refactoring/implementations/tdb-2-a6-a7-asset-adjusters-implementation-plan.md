# TDB-2 — A6 (ROU) + A7 (excess cash) — IMPLEMENTER PLAN (RED→GREEN TDD)

**Spec:** `docs/refactoring/spec/tdb-2-a6-a7-asset-adjusters-spec.md` (read first).
**Issue:** `#2`. **Worktree:** `.claude/worktrees/tdb-2-a6-a7-adjusters` (own `go.mod`).
**All commands use `GOWORK=off`** to validate the worktree module in isolation.
**Role:** BACKEND. **Methodology:** TDD — write the failing test, then the code.
**Pre-flight invariant baseline (capture BEFORE any edit):**
```
GOWORK=off go test ./... -count=1 > /tmp/tdb2-baseline.txt 2>&1; echo "baseline exit: $?"
git stash list   # ensure clean tree
git rev-parse HEAD   # record the base commit for the commit template
```

> **Ordering principle:** entity+parser first (data available), then the two Apply
> methods (RED→GREEN, isolated), then dispatcher wiring, then view-model arms, then
> projection meta, then schema bump, then the invariant-proof tests, then the
> integration/basket pass. Each numbered task is a self-contained RED→GREEN unit.

---

## Task 0 — Confirm Q1 (A7 does NOT touch the bridge) is accepted

**BLOCKING.** Do not start Task 1 until the human confirms spec §9 Q1 (recommended
default: A7 is observability/view only; the EV bridge is untouched). If Q1 flips, the A7
design changes materially. Record the decision in the TDB-2 tracker.

---

## Task 1 — Entity field (A6)

**File:** `internal/core/entities/financial_data.go`

Add in the lease block (after `OperatingLeaseLiability`, ~line 79):
```go
// OperatingLeaseRightOfUseAsset is the ASC 842 / IFRS 16 right-of-use asset
// book value (A6, TDB-2). Stored as a PARALLEL informational field: it is the
// asset-side mirror of OperatingLeaseLiability (B1) and is NOT a computePlugs
// component (it remains absorbed in the OtherNonCurrentAssets plug), so the
// TotalAssets == sum(components)+plug invariant is unchanged. Consumed only by
// the A6 adjuster's invested-capital-exclusion overlay.
OperatingLeaseRightOfUseAsset float64 `json:"operating_lease_right_of_use_asset,omitempty"`
```

**Validate:** `GOWORK=off go build ./internal/core/entities/...` (exit 0).

---

## Task 2 — Parser stores ROU (A6) — RED→GREEN

**RED** — `internal/infra/gateways/sec/parser_test.go` (or the existing parser test file):
add a table case asserting that a fixture/synthetic XBRL payload containing
`us-gaap:OperatingLeaseRightOfUseAsset` produces a non-zero
`financialData.OperatingLeaseRightOfUseAsset`. Run it; it FAILS (field never stored).

**GREEN** — `internal/infra/gateways/sec/parser.go`, immediately after the operating-lease
liability `findValue` block (~line 784):
```go
// Right-of-Use assets (ASC 842 / IFRS 16) — A6 (TDB-2). Stored as a parallel
// informational field; deliberately NOT folded into computePlugs (see spec §3.7),
// so the TotalAssets == sum(components)+plug invariant is unchanged.
if val, exists := p.findValue(data, []string{
    "OperatingLeaseRightOfUseAsset",
    "RightOfUseAssets",
    "OperatingLeaseRightOfUseAssetAfterAccumulatedAmortization",
    "RightofuseAssets", // IFRS 16
}); exists {
    financialData.OperatingLeaseRightOfUseAsset = val
}
```
**Do NOT modify `computePlugs`** (spec §3.7). Re-run the parser test → GREEN.

**Validate:** `GOWORK=off go test ./internal/infra/gateways/sec/... -count=1`.

---

## Task 3 — FX conversion for ROU (A6)

**File:** `internal/services/valuation/currency.go`

Add in the asset/lease block (near `fd.OperatingLeaseLiability *= rate`, ~line 242):
```go
fd.OperatingLeaseRightOfUseAsset *= rate
```
Update the file's monetary-fields godoc comment list (top of file) to include the new
field. **RED-optional:** add an FPI-conversion assertion to the existing currency test if
one enumerates monetary fields; otherwise rely on the contract test in Task 4.

**Validate:** `GOWORK=off go test ./internal/services/valuation/... -run Currency -count=1`.

---

## Task 4 — A6 `ApplyA6RightOfUseAssets` — RED→GREEN

**RED** — `internal/services/datacleaner/adjustments/a6_right_of_use_adjuster_test.go`
(new file). Mirror `TestA1*_Adjuster_Interface_Contract`. Table cases:
- no ROU (`OperatingLeaseRightOfUseAsset == 0`) → `Fired:false`, SkipReason set, no overlay.
- ROU below 5% of TotalAssets → `Fired:false`, `SkipMetrics{rou_ratio, threshold}`, no overlay.
- ROU ≥ 5% → `Fired:true` audit LedgerEntry (empty Component/DeltaAmount), exactly one
  `OverlaySpec{OverlayID: adjusterIDA6RightOfUseExclusion, Field:"InvestedCapitalExclusion",
  Operation:"subtract", Amount: rou, AmountSemantics: AmountIncremental}`,
  `SkipMetrics{rou_value, operating_lease_liability}` on the fired entry (B1-overlap guard).
- ROU ≥ 10% → also one `info`-severity Flag `Type:"right_of_use_exclusion"`.
- mutation-free assertion: `reflect.DeepEqual(workingBefore, workingAfter)` (Apply never
  mutates `working`).

**GREEN** — `internal/services/datacleaner/adjustments/assets.go`:
1. Add constants near the existing AdjusterID block (~line 27):
   ```go
   adjusterIDA6RightOfUseExclusion = "A6_right_of_use_exclusion"
   adjusterIDA7ExcessCash          = "A7_excess_cash"
   ```
2. Add the method (mirror `ApplyA1Goodwill`):
   ```go
   func (aa *AssetAdjuster) ApplyA6RightOfUseAssets(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
       _ = ctx; _ = cleaningCtx
       now := time.Now()
       rou := working.OperatingLeaseRightOfUseAsset
       if rou <= 0 {
           return AdjusterOutput{LedgerEntries: []entities.LedgerEntry{{
               Timestamp: now, AdjusterID: adjusterIDA6RightOfUseExclusion, RuleID: rule.ID,
               Fired: false, Reasoning: "No right-of-use assets present to exclude",
               SkipReason: "No right-of-use assets present to exclude"}}}, nil
       }
       rouRatio := rou / working.TotalAssets
       const threshold = 0.05
       if rouRatio <= threshold {
           return AdjusterOutput{LedgerEntries: []entities.LedgerEntry{{
               Timestamp: now, AdjusterID: adjusterIDA6RightOfUseExclusion, RuleID: rule.ID,
               Fired: false, Reasoning: "ROU ratio below 5% threshold",
               SkipReason: fmt.Sprintf("ROU ratio %.1f%% below threshold %.1f%%", rouRatio*100, threshold*100),
               SkipMetrics: map[string]float64{"rou_ratio": rouRatio, "threshold": threshold}}}}, nil
       }
       overlay := entities.OverlaySpec{
           OverlayID: adjusterIDA6RightOfUseExclusion, RuleID: rule.ID,
           Field: "InvestedCapitalExclusion", Operation: "subtract", Amount: rou,
           AmountSemantics: entities.AmountIncremental,
           Reasoning: fmt.Sprintf("right_of_use_assets: Excluded %.0f ROU assets (%.1f%% of assets) from invested capital per A6 rule", rou, rouRatio*100),
       }
       out := AdjusterOutput{
           LedgerEntries: []entities.LedgerEntry{{
               Timestamp: now, AdjusterID: adjusterIDA6RightOfUseExclusion, RuleID: rule.ID,
               Fired: true, Reasoning: "A6 right-of-use exclusion overlay emitted",
               // B1-overlap guard (spec §3.3): record both magnitudes so the
               // ROU-asset vs lease-liability overlap is observable per ticker.
               SkipMetrics: map[string]float64{
                   "rou_value": rou, "operating_lease_liability": working.OperatingLeaseLiability}}},
           Overlays: []entities.OverlaySpec{overlay},
       }
       if rouRatio >= 0.10 {
           out.Flags = append(out.Flags, entities.Flag{
               ID: fmt.Sprintf("rou-flag-%d", now.UnixNano()), RuleID: rule.ID,
               Type: "right_of_use_exclusion", Severity: entities.FlagSeverityLow,
               Amount: rou, Percentage: rouRatio * 100,
               Description: fmt.Sprintf("Excluded significant ROU assets (%.1f%% of assets) from invested capital", rouRatio*100),
               Recommendation: "Verify lease accounting; ROU assets inflate total assets without adding operating capacity",
               Timestamp: now})
       }
       return out, nil
   }
   ```
   (Severity: config `severity:info`. Map to the closest existing constant —
   `entities.FlagSeverityLow` or `entities.Warning`; confirm the taxonomy and match the
   config intent. If an `Info`-level constant exists, use it.)
3. Add the adapter struct + constructor + `var _ Adjuster` assertion + `Name()`/`Apply()`
   mirroring `a1GoodwillAdjuster` (`a6RightOfUseAdjuster`).

**Validate:** `GOWORK=off go test ./internal/services/datacleaner/adjustments/ -run A6 -count=1`.

---

## Task 5 — A7 `ApplyA7ExcessCash` — RED→GREEN

**RED** — `internal/services/datacleaner/adjustments/a7_excess_cash_adjuster_test.go`
(new file). Table cases:
- no cash (`CashAndCashEquivalents == 0`) → `Fired:false`, SkipReason, no overlay.
- `Revenue <= 0` → all cash is excess (`excessCash == Cash`), `Fired:true`, overlay
  `Amount == Cash`.
- `Revenue > 0`, threshold present (10%): `excessCash == max(0, Cash − 0.10*Revenue)`;
  fire only when `excessCash > 0`. Verify the math on a concrete fixture
  (Cash=$50B, Revenue=$100B → operatingNeed=$10B → excess=$40B).
- `Cash <= operatingNeed` → `excessCash == 0` → `Fired:false` skip.
- mutation-free assertion (`reflect.DeepEqual` before/after).
- threshold sourced from `rule.Threshold.PercentageOfRevenue` (pass a rule with the
  pointer set; and a case with `rule.Threshold == nil` → all-cash-excess default).

**GREEN** — `internal/services/datacleaner/adjustments/assets.go`:
```go
func (aa *AssetAdjuster) ApplyA7ExcessCash(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error) {
    _ = ctx; _ = cleaningCtx
    now := time.Now()
    cash := working.CashAndCashEquivalents
    if cash <= 0 {
        return AdjusterOutput{LedgerEntries: []entities.LedgerEntry{{
            Timestamp: now, AdjusterID: adjusterIDA7ExcessCash, RuleID: rule.ID,
            Fired: false, Reasoning: "No cash present to assess",
            SkipReason: "No cash present to assess"}}}, nil
    }
    // Operating-cash floor as a % of revenue (config-driven). When the
    // threshold is absent OR revenue is non-positive, treat ALL cash as
    // excess (engine's existing "all cash non-operating" stance; safe default).
    operatingCashPct := 0.0 // default → all cash excess
    if rule.Threshold != nil && rule.Threshold.PercentageOfRevenue != nil {
        operatingCashPct = *rule.Threshold.PercentageOfRevenue
    }
    operatingCashNeed := 0.0
    if working.Revenue > 0 && operatingCashPct > 0 {
        operatingCashNeed = operatingCashPct * working.Revenue
    }
    excessCash := cash - operatingCashNeed
    if excessCash < 0 { excessCash = 0 }
    if excessCash <= 0 {
        return AdjusterOutput{LedgerEntries: []entities.LedgerEntry{{
            Timestamp: now, AdjusterID: adjusterIDA7ExcessCash, RuleID: rule.ID,
            Fired: false, Reasoning: "All cash within operating-cash need; no excess",
            SkipReason: fmt.Sprintf("Cash %.0f <= operating need %.0f", cash, operatingCashNeed),
            SkipMetrics: map[string]float64{"cash": cash, "operating_cash_need": operatingCashNeed, "operating_cash_pct": operatingCashPct}}}}, nil
    }
    overlay := entities.OverlaySpec{
        OverlayID: adjusterIDA7ExcessCash, RuleID: rule.ID,
        Field: "ExcessCash", Operation: "identify", Amount: excessCash,
        AmountSemantics: entities.AmountReplacement, // sets view.ExcessCash = amount
        Reasoning: fmt.Sprintf("excess_cash: Identified %.0f excess cash (cash %.0f - operating need %.0f at %.0f%% of revenue) as non-operating per A7 rule", excessCash, cash, operatingCashNeed, operatingCashPct*100),
    }
    out := AdjusterOutput{
        LedgerEntries: []entities.LedgerEntry{{
            Timestamp: now, AdjusterID: adjusterIDA7ExcessCash, RuleID: rule.ID,
            Fired: true, Reasoning: "A7 excess-cash identification overlay emitted",
            SkipMetrics: map[string]float64{"cash": cash, "operating_cash_need": operatingCashNeed, "excess_cash": excessCash}}},
        Overlays: []entities.OverlaySpec{overlay},
    }
    out.Flags = append(out.Flags, entities.Flag{
        ID: fmt.Sprintf("excess-cash-flag-%d", now.UnixNano()), RuleID: rule.ID,
        Type: "excess_cash", Severity: entities.FlagSeverityLow,
        Amount: excessCash, Description: fmt.Sprintf("Identified %.0f non-operating (excess) cash", excessCash),
        Recommendation: "Excess cash is non-operating; consider in capital-allocation analysis",
        Timestamp: now})
    return out, nil
}
```
Add the `a7ExcessCashAdjuster` adapter + constructor + `var _ Adjuster` + `Name()`/`Apply()`.

**Validate:** `GOWORK=off go test ./internal/services/datacleaner/adjustments/ -run A7 -count=1`.

---

## Task 6 — Dispatcher arms (both) — GREEN

**File:** `internal/services/datacleaner/adjustments/assets.go`, in the
`ProcessAssetAdjustments` `switch rule.ID` (~line 1064), add the two arms BEFORE the
`default`:
```go
case "right_of_use_assets":
    out, err := aa.ApplyA6RightOfUseAssets(applyCtx, data, rule, cleaningCtx)
    if err != nil { continue }
    allFlags = append(allFlags, out.Flags...)
    nativeLedger = append(nativeLedger, out.LedgerEntries...)
    nativeOverlays = append(nativeOverlays, out.Overlays...)
    nativelyEmittedRuleIDs[rule.ID] = true
    continue // OverlayEmitter — no component delta, no tangible recompute
case "excess_cash":
    out, err := aa.ApplyA7ExcessCash(applyCtx, data, rule, cleaningCtx)
    if err != nil { continue }
    allFlags = append(allFlags, out.Flags...)
    nativeLedger = append(nativeLedger, out.LedgerEntries...)
    nativeOverlays = append(nativeOverlays, out.Overlays...)
    nativelyEmittedRuleIDs[rule.ID] = true
    continue
```
**Note:** both `continue` (like the RD/CapSW arms) so the post-switch tangible recompute
does not fire (neither emits a Restater component delta). No change to
`assetArmTriggersTangibleRecompute` or the orchestrator.

**Validate:** `GOWORK=off go test ./internal/services/datacleaner/... -count=1`.

---

## Task 7 — View model arms — RED→GREEN

**RED** — `internal/services/datacleaner/cleaneddata/invested_capital_test.go`: add cases
asserting:
- an `OverlaySpec{Field:"InvestedCapitalExclusion", Operation:"subtract", Amount: R}`
  reduces `InvestedCapital().TotalAssets` by R, recomputes `TangibleAssets`, and does NOT
  zero `Goodwill`.
- an `OverlaySpec{Field:"ExcessCash", AmountSemantics: AmountReplacement, Amount: E}`
  sets `InvestedCapital().ExcessCash == E` and leaves `TotalAssets`/`DebtLikeClaims`
  untouched.
- `AsReported()`/`Restated()` have `ExcessCash == 0` (identity).

**GREEN:**
1. `internal/services/datacleaner/cleaneddata/view.go` — add to `FinancialDataView`:
   ```go
   // ExcessCash is the A7-identified non-operating cash. InvestedCapital-only;
   // zero on AsReported/Restated. Informational — does NOT feed the EV→Equity
   // bridge (spec §4.1). (Add to identityCopy? NO — it is overlay-derived only.)
   ExcessCash float64
   ```
   (Do NOT add `ExcessCash` to `identityCopy` — it is populated only by the overlay arm.
   `TestIdentityCopy_CoversEveryViewField` likely enumerates view fields; if it fails,
   add `ExcessCash` to that test's EXEMPT set with a comment "overlay-derived, never
   identity-copied" — mirror how `DebtLikeClaims` is handled.)
2. `internal/services/datacleaner/cleaneddata/invested_capital.go` — in
   `applyOverlayToView`'s incremental switch, add:
   ```go
   case "InvestedCapitalExclusion":
       // A6 ROU exclusion. Subtract from TotalAssets WITHOUT zeroing Goodwill
       // (distinct from the A1 "TotalAssets" arm which is goodwill-specific).
       v.TotalAssets += signed
       v.TangibleAssets = v.TotalAssets - v.Goodwill - v.OtherIntangibles
   ```
   and in the replacement switch (`applyReplacement`), add:
   ```go
   case "ExcessCash":
       v.ExcessCash = o.Amount
   ```
   (A7 uses `AmountReplacement`, so it routes through `applyReplacement`.)

**Validate:** `GOWORK=off go test ./internal/services/datacleaner/cleaneddata/ -count=1`.

---

## Task 8 — Projection meta rows — RED→GREEN

**RED** — extend the projection test
(`internal/services/datacleaner/adjustment_projection_test.go`): assert that a ledger +
overlay set containing a fired A6 and a fired A7 produces two `entities.Adjustment`
records (Category `AssetQuality`, Type `Exclude`, correct `FromAccount`, Amount from the
overlay, reasoning from the overlay).

**GREEN** — `internal/services/datacleaner/adjustment_projection.go`, add to
`perRuleAdjustmentMeta` (~line 112):
```go
"A6_right_of_use_exclusion": {
    Category: entities.AssetQuality, Type: entities.Exclude,
    FromAccount: "OperatingLeaseRightOfUseAsset", ToAccount: "",
    PercentageMode: percentageAbsent, AmountSource: amountOverlayAmount},
"A7_excess_cash": {
    Category: entities.AssetQuality, Type: entities.Exclude,
    FromAccount: "CashAndCashEquivalents", ToAccount: "",
    PercentageMode: percentageAbsent, AmountSource: amountOverlayAmount},
```

**Validate:** `GOWORK=off go test ./internal/services/datacleaner/ -run Projection -count=1`.

---

## Task 9 — SchemaVersion bump 9 → 10 (atomic)

**Files (same commit):**
- `internal/observability/replay/schema.go:53` — `"FinancialData": 10,` (update the godoc
  block above it noting the TDB-2 ROU field addition).
- `internal/services/datacleaner/service.go:306` — `b.AddSchemaVersion("FinancialData", 10)`.

**Validate:** the replay schema round-trip test should pass (producer subset of
`CurrentSchemaVersions`). `GOWORK=off go test ./internal/observability/replay/... -count=1`.

---

## Task 10 — Invariant-proof tests (DDM + shadow + per-share)

**10a — DDM unaffected:** add
`internal/services/valuation/models/ddm_tdb2_invariance_test.go` (or extend
`ddm_bitforbit_test.go` siblings): for JPM/BAC/WFC fixtures run the full cleaner + assert
(a) no `Fired:true` A6/A7 ledger entry, OR (b) if one fires, the DDM
`IntrinsicValuePerShare`/`EquityValue`/`EnterpriseValue` bits are still
`math.Float64bits`-equal to the pre-change goldens. Keep `TestDDM_LegacyPath_BitForBit`
GREEN (do NOT regenerate goldens).

**10b — Shadow byte-identity (GATE, do not skip):**
```
GOWORK=off go test ./internal/integration/... -run TestDataCleanerRecompute_ShadowMode_TickerBasket -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/ ; echo "shadow-diff exit (MUST be 0): $?"
```
If exit != 0: **STOP**. A6/A7 should mutate nothing the shim observes (spec §3.7). A
non-zero diff means an unintended plug/recompute interaction — diagnose and fix; do NOT
regenerate snapshots to mask it.

**10c — Per-share bit-for-bit:** run the valuation service test for AAPL + a lease-heavy
ticker (e.g. F) and confirm `dcf_value_per_share`/`EquityValue`/`EnterpriseValue`
unchanged vs. `/tmp/tdb2-baseline.txt`.

---

## Task 11 — Basket / integration pass (controlled golden update)

```
GOWORK=off go test ./internal/integration/... -run TestLedger_BasketSnapshot -count=1
git diff internal/integration/datacleaner_ledger_basket_test.go internal/integration/testdata/  # inspect
```
- If the basket snapshot diffs, confirm EVERY added line is an `A6_*`/`A7_*` ledger entry
  or `InvestedCapitalExclusion`/`ExcessCash` overlay on a ticker that genuinely carries
  ROU / excess cash. Update the golden ONLY for those additions.
- Confirm `TestLedger_BasketSnapshot_T2BS3_RestatedReconstruction` AMD $9,679M / KO
  $60,912M `Restated().TotalLiabilities` are byte-identical (A6/A7 touch assets/overlays,
  not liabilities) — these MUST NOT move.
- `TestOrchestrator_LedgerOrdering`, `TestApplyActiveAdjustments_*FiringSignal*`,
  `TestPreStateCapture_*` must stay GREEN.

---

## Task 12 — Full validation set (must all be green)

```
GOWORK=off go build ./...                                  # exit 0
GOWORK=off go vet ./...                                    # exit 0
GOWORK=off go test ./... -count=1                          # exit 0, 0 FAIL
# Named invariants:
GOWORK=off go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1
GOWORK=off go test ./internal/services/datacleaner/ -run TestRecomputeUmbrellas_NoMutation -count=1
GOWORK=off go test ./internal/integration/ -run 'TestLedger_BasketSnapshot|TestDataCleanerRecompute_ShadowMode' -count=1
git diff --quiet internal/integration/testdata/recompute-shadow/ ; echo "shadow exit: $?"   # MUST be 0
# No silent enabled:true skip remains (audit):
GOWORK=off go test ./internal/services/datacleaner/adjustments/ -run 'A6|A7' -count=1
```
Optional live confirmation (operator, requires running server): `GET
/api/v1/fair-value/AAPL` should now show A6/A7 entries in `cleaning_adjustments` while
`dcf_value_per_share` matches the pre-change value.

---

## Commit ladder (suggested — small, reviewable, each green)

1. `feat(datacleaner): store ROU asset on entity + parser + FX (TDB-2 A6 prep) (#2)` — Tasks 1-3.
2. `feat(datacleaner): A6 right-of-use exclusion adjuster + dispatcher arm (#2)` — Tasks 4, 6 (A6 half).
3. `feat(datacleaner): A7 excess-cash adjuster + dispatcher arm (#2)` — Tasks 5, 6 (A7 half).
4. `feat(cleaneddata): InvestedCapital ROU exclusion + ExcessCash view arms (#2)` — Task 7.
5. `feat(datacleaner): A6/A7 adjustment-projection meta rows (#2)` — Task 8.
6. `chore(replay): bump FinancialData schema 9->10 for ROU field (#2)` — Task 9.
7. `test(tdb-2): DDM/shadow/per-share invariance + basket golden update (#2)` — Tasks 10-11.

### Commit message template
```
feat(datacleaner): <subject> (#2)

TDB-2 — make the A6 right_of_use_assets / A7 excess_cash rules real
(previously enabled:true but silently skipped at the dispatcher default).

<what + why; reference spec §section>

Invariants: DDM bit-for-bit green; shadow snapshots byte-identical
(git diff --quiet exits 0); dcf_value_per_share unchanged.

Spec:  docs/refactoring/spec/tdb-2-a6-a7-asset-adjusters-spec.md
Plan:  docs/refactoring/implementations/tdb-2-a6-a7-asset-adjusters-implementation-plan.md

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

---

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| ROU double-counts B1 lease debt | A6 overlays `InvestedCapitalExclusion` (TotalAssets-class), B1 overlays `DebtLikeClaims` — different fields/consumers; bridge untouched (spec §3.3/§3.4). Guard metrics on the fired entry make overlap observable. |
| A7 mis-values cash-rich firms | A7 does NOT touch the EV bridge or NWC (spec §4.1/§4.6) — observability/view only. Per-share bit-for-bit proven in Task 10c. |
| Shadow snapshots drift unexpectedly | Task 10b is a hard gate (`git diff --quiet` MUST be 0). A6/A7 mutate no entity field; ROU is a parallel field outside the plug. |
| DDM banks move | Task 10a proves banks skip A6 (ROU<5%) and A7 overlays can't reach DDM math. |
| Projection omits A6/A7 from audit trail | Mandatory meta rows (Task 8) + projection test (Task 8 RED). |
| Severity constant mismatch (`info`) | Confirm the `FlagSeverity` taxonomy; map config `info` to the existing constant; pin in the contract test. |
| `IdentityCopy_CoversEveryViewField` fails on new `ExcessCash` | Add to the test's EXEMPT set (overlay-derived), mirroring `DebtLikeClaims`. |
