# TDB-8 — Inventory turnover analysis for obsolescence detection

**Status:** OPEN — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P3 — Tier 3 (enhancement).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-8]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "Inventory Analysis Enhancement" (`flagging/risk_analyzer.go:128`).

---

## Context

`flagging/risk_analyzer.go:128` carries a standing TODO: "Add inventory turnover analysis for better obsolescence detection." The risk analyzer currently flags inventory risk without incorporating turnover, which is the standard signal for obsolescence (slow-moving inventory → write-down risk).

## Scope / Tasks

| ID | Task | File | Effort |
|---|---|---|---|
| TDB-8.1 | Incorporate `InventoryTurnover` into the obsolescence-risk flag logic | `flagging/risk_analyzer.go` | S |
| TDB-8.2 | Test high-turnover (healthy) vs low-turnover (obsolescence-risk) scenarios | `flagging` tests | S |

## Acceptance
- [ ] Turnover incorporated into the risk/obsolescence flag
- [ ] Tests cover high/low-turnover cases
