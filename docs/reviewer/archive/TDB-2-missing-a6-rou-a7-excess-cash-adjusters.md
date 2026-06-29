# TDB-2 — A6 (ROU assets) and A7 (excess cash) adjusters are enabled in config but unimplemented

**Status:** IMPLEMENTED 2026-06-07 (branch `worktree-tdb-2-a6-a7-adjusters`) — A6 + A7 OverlayEmitters ship; full validation cycle green (VERIFIER VERIFIED · REVIEWER APPROVE_WITH_NITS · QA PASS); `go test ./... -count=1` = 48/48 ok; shadow snapshots byte-identical; DDM bit-for-bit green; `dcf_value_per_share` bit-for-bit unchanged. **Discovered follow-up (NOT a TDB-2 blocker):** the cleaner audit trail (`ValuationResult.CleaningAdjustments`) is not wired into the HTTP `FairValueResponse` — a PRE-EXISTING gap shared by A1/B1/B2/B3; A6/A7 entries reach the internal result + ledger + cleaner logs + replay bundles but NOT the public API. See "Discovered follow-up" below.
**Priority:** P1 — Tier 1 (valuation correctness).
**Type:** Correctness gap / dead config (rule promises behavior the engine never performs).
**Mirrored as GitHub issue:** `[TDB-2]` (yonatan-levin/midas).
**Origin:** 2026-06-06 investigation (residue **R2**). Also resolves the dormant catalog item "Adjuster test coverage (A6/A7)" — you cannot test adjusters that do not exist.
**Related:** TDB-1 (sibling parser/adjuster gap), `config/datacleaner/rules.json`.

---

## DECISION (2026-06-07)

- **TDB-2.0 — DECIDED:** **IMPLEMENT both** A6 + A7 as `Adjuster`-interface impls (human
  decision; do NOT remove the rules).
- **Spec:** `docs/refactoring/spec/tdb-2-a6-a7-asset-adjusters-spec.md`
- **Implementer plan:** `docs/refactoring/implementations/tdb-2-a6-a7-asset-adjusters-implementation-plan.md`
- **Worktree:** `.claude/worktrees/tdb-2-a6-a7-adjusters` (own `go.mod`; `GOWORK=off`).

### Design summary (full rationale in the spec)
- **A6 (ROU)** — OverlayEmitter (mirrors A1 goodwill). Emits
  `OverlaySpec{Field:"InvestedCapitalExclusion", Operation:"subtract", Amount:ROU}`,
  fires at ROU/TotalAssets > 5%, `info` flag at ≥10%. New entity field
  `OperatingLeaseRightOfUseAsset` (parser stores it, currency FX-converts it; kept OUT of
  `computePlugs` as a parallel field → shadow snapshots untouched). Excludes ROU from
  `InvestedCapital().TotalAssets` ONLY — **does NOT touch the EV→Equity bridge**; B1 lease
  debt stays on `DebtLikeClaims` (different field/consumer → no arithmetic double-count;
  B1-overlap recorded on the fired LedgerEntry's SkipMetrics).
- **A7 (excess cash)** — OverlayEmitter. `excessCash = max(0, Cash − pct*Revenue)` with
  `pct` from the rule's `Threshold.PercentageOfRevenue` (config `0.1`); all-cash-excess
  when Revenue≤0. Emits `OverlaySpec{Field:"ExcessCash"}` consumed by the audit-trail
  projection + a new `InvestedCapital().ExcessCash` view field. **Deliberately does NOT
  alter the EV→Equity bridge** — the engine already (correctly) credits the full cash
  balance to equity and nets all cash out of operating NWC (BUG-014); subtracting
  operating cash from the bridge would be a valuation regression.
- **Invariants:** `dcf_value_per_share`/`EquityValue`/`EnterpriseValue` bit-for-bit
  unchanged (no bridge input touched). DDM bit-for-bit green (banks skip A6; A7 can't
  reach DDM math). Shadow snapshots byte-identical (HARD gate). Basket golden updated
  ONLY for A6/A7-attributable additions.
- **Versions:** SchemaVersion `FinancialData` **9 → 10** (new ROU field, atomic bump).
  CalculationVersion stays **4.6** (no per-share numeric change; verified live value is
  4.6, not the stale "4.4" in CLAUDE.md).
- **Blocking open question for execution:** confirm A7 stays observability/view-only and
  does NOT touch the EV bridge (spec §9 Q1; recommended default: YES).

---

## Context

The DC-1 Phase-2 refactor migrated A1/A2/A4/A5 + the RD/CapSW flag-only reviews to the `Adjuster` interface, but **never built A6 (right-of-use assets) or A7 (excess cash)**. Their config rules are live:
- `right_of_use_assets` (A6) — `rules.json` `enabled:true`
- `excess_cash` (A7) — `rules.json` `enabled:true`

The asset dispatcher (`adjustments/assets.go`) has no switch arm for either, so both fall to `default: continue` and are **silently skipped**. The ROU value isn't even stored on the entity; the cash value *is* parsed (`CashAndCashEquivalents`, `sec/parser.go:635`) but never consumed by an excess-cash adjustment.

## Why it matters

- **Excess cash (A7)** feeds the EV→Equity bridge — an unadjusted cash balance mis-states equity value for cash-rich companies.
- **ROU assets (A6)** feed invested capital — relevant to operating-lease-heavy industries (retail, airlines).
- Both rules being `enabled:true` while doing nothing is a correctness *lie* in the config — either honor it or remove it.

## Scope / Tasks (decision-first)

| ID | Task | Effort |
|---|---|---|
| TDB-2.0 | **Decision:** implement A6/A7, or disable/remove the dangling rules | — |
| TDB-2.1 | If implementing: `Adjuster`-interface impls for A6 + A7 (`adjustments/assets.go` + new files) | M |
| TDB-2.2 | Route them in the asset dispatcher (replace the silent skip) | S |
| TDB-2.3 | `*_Adjuster_Interface_Contract` tests for each | S |
| TDB-2.4 | If removing instead: delete the rules + note in config changelog | XS |

## Acceptance
- [x] Decision recorded (implement — see DECISION above)
- [x] If implemented: adjusters fire, dispatcher routes them, contract tests pass (`TestA6RightOfUseAdjuster_*`, `TestA7ExcessCashAdjuster_*`, cleaneddata view + projection tests)
- [x] No silent `enabled:true`-but-skipped rules remain (QA audited all 17 config rules vs dispatcher arms — all routed)
- [x] Load-bearing invariants stay green (DDM bit-for-bit; shadow byte-identical; ledger basket incl. T2-BS-3; `dcf_value_per_share` unchanged)

## Discovered follow-up (pre-existing, broader than TDB-2)
- **Wire `cleaning_adjustments` into the HTTP API.** `entities.ValuationResult.CleaningAdjustments` (populated by A1/A6/A7/B-rules via `adjustmentsFromLedger`) is NOT mapped into `FairValueResponse` by `buildFairValueResponse` (`internal/api/v1/handlers/fair_value.go`); the response struct has no `cleaning_adjustments` field. So the cleaner audit trail is internal-only today (visible in `ValuationResult`, the ledger, cleaner logs, and replay bundles). Exposing it would surface all adjuster activity (not just A6/A7) to API consumers. Pre-existing; flagged by the TDB-2 REVIEWER 2026-06-07. Candidate new backlog item. **→ CLOSED as TDB-11 / GitHub #11** (merged `220bf6e`; `cleaning_adjustments` now ships omitempty/fired-only).

## REVIEWER NITs — CLOSED 2026-06-28 (branch `chore/tdb-part2-followups`)

The two APPROVE_WITH_NITS items from the 2026-06-07 review are now closed:
- **NIT (a) — `TotalAssets<=0` guard in A6.** `ApplyA6RightOfUseAssets` (`internal/services/datacleaner/adjustments/assets.go`) now skips (Fired:false, no overlay) when `working.TotalAssets <= 0`, before computing `rou / TotalAssets`. Without it, `rou>0 && TotalAssets<=0` yields a `+Inf` ratio that clears the materiality gate and fires A6 with a nonsense ratio. Regression subtest added to `TestA6RightOfUseAdjuster_Adjuster_Interface_Contract` (`ta ∈ {0, -1e6}`). A1 goodwill shares the identical unguarded divide on `working.TotalAssets` (line ~123) — left as-is, **out of this NIT's scope**, noted inline at the A6 guard.
- **NIT (b) — broaden the A6/A7 DDM-invariance test.** `TestA6A7_DDMBanks_DoNotFire` is now table-driven over **JPM/BAC/WFC** (the three tickers `TestDDM_LegacyPath_BitForBit` pins) and additionally asserts A7's structural guarantee: a bank's cash exceeds the 10%-of-revenue floor so A7 *does* fire, but its overlay can only target the view-only `ExcessCash` field, which no DDM input (or EV→Equity bridge term) reads.

Validation: `GOWORK=off go test ./internal/services/datacleaner/adjustments/` green; `TestDDM_LegacyPath_BitForBit` green; full `go build ./...` clean. No CalculationVersion/SchemaVersion change (behavior-preserving guard + test-only broadening).
