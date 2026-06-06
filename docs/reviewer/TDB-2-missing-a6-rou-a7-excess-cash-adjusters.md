# TDB-2 — A6 (ROU assets) and A7 (excess cash) adjusters are enabled in config but unimplemented

**Status:** OPEN — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P1 — Tier 1 (valuation correctness).
**Type:** Correctness gap / dead config (rule promises behavior the engine never performs).
**Mirrored as GitHub issue:** `[TDB-2]` (yonatan-levin/midas).
**Origin:** 2026-06-06 investigation (residue **R2**). Also resolves the dormant catalog item "Adjuster test coverage (A6/A7)" — you cannot test adjusters that do not exist.
**Related:** TDB-1 (sibling parser/adjuster gap), `config/datacleaner/rules.json`.

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
- [ ] Decision recorded (implement vs remove)
- [ ] If implemented: adjusters fire, dispatcher routes them, contract tests pass
- [ ] No silent `enabled:true`-but-skipped rules remain
- [ ] Load-bearing invariants stay green
