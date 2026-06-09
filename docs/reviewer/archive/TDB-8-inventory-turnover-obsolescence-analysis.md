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

**Status:** IMPLEMENTED 2026-06-08 (branch `worktree-tdb-8-inventory-turnover`) — `InventoryTurnover` (= Revenue/Inventory) now refines the inventory-obsolescence flag severity (escalate when <2.0×, de-escalate when ≥4.0×, unchanged when 0/unreported). Gates run: full suite 50/50, shadow byte-identical, DDM bit-for-bit. REVIEWER APPROVE_WITH_NITS (MAJOR comment-accuracy fix applied: the metric is Revenue/Inventory, NOT the textbook COGS/Inventory; thresholds match the codebase's existing `constants.InventoryTurnoverThreshold=2.0`). QA PASS. Flag-only — no per-share/regression impact.

## Acceptance
- [x] Turnover incorporated into the risk/obsolescence flag (escalate <2.0×, de-escalate ≥4.0×; unchanged when unreported)
- [x] Tests cover high/low-turnover cases (+ regression-safe turnover==0 + below-threshold)

## Deferred follow-up NITs (REVIEWER 2026-06-08, non-blocking, noted in-code)
- Make the turnover cutoffs industry-aware via `GetIndustryThresholds` (a structurally low-turnover industry like heavy equipment is over-escalated today); reconcile the 4.0× de-escalate boundary with `industry_analyzer.go:213`'s `<4.0` retail low-turnover heuristic.
- Add helper cap (already-Critical) / floor (already-Low) + exact-boundary (2.0/4.0) tests.
- Note the FY-vs-Q turnover asymmetry (an FY snapshot's Revenue/Inventory runs ~4× a single quarter's) — see `docs/reviewer/DC-1-FY-enable-predicate-investigation.md`.
