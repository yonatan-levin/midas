# RM-1.B — Documentation drift after RM-1 bridge reorder

**Status:** OPEN — filed 2026-05-09 alongside the merge of Stream B's bridge-ordering fix (commit `9da6c68`).
**Severity:** NIT. Pure documentation hygiene — neither finding affects runtime behavior.
**Origin:** Stream B small V/R/Q REVIEWER pass (LOW-1 spec ordering + NIT-1 caller comment).
**Blocks:** Nothing.
**Related:** RM-1 (parent spec), `9da6c68` (the reorder commit), RM-1.A (the substantive T7 deferral companion).

---

## Two doc-drift items

### Item 1 — RM-1 spec's "Updated fallback chain" diagram is reversed (LOW-1)

`docs/reviewer/RM-1-revenue-multiple-quarterly-vs-ttm.md:134-141` and `:158` still list the OLD ordering:

```
TTM_4Q → TTM_PRIOR_BRIDGE → ANNUAL_FY → ANNUALIZED_QUARTER → INSUFFICIENT_HISTORY
```

The implementation in `9da6c68` reverses this so partial-year IPOs are properly auditable as `TTM_PRIOR_BRIDGE` rather than silently absorbed into TTM_4Q:

```
TTM_PRIOR_BRIDGE → TTM_4Q → ANNUAL_FY → ANNUALIZED_QUARTER → INSUFFICIENT_HISTORY
```

T9's row in the test scenarios table at line 158 also still lists `TTM_4Q` as the expected source; should be `TTM_PRIOR_BRIDGE`.

**Suggested fix.** Swap the first two entries in the diagram, update T9's expected source, and add a one-line rationale (e.g., "partial-year shape preserved for replay audit"). About 5 lines of edits.

### Item 2 — Stale comment in caller (NIT-1)

`internal/services/valuation/models/revenue_multiple.go:79` reads (paraphrasing the OLD ordering):

```
// fallback chain (TTM_4Q -> TTM_PRIOR_BRIDGE -> ANNUAL_FY -> ...)
```

Should read:

```
// fallback chain (TTM_PRIOR_BRIDGE -> TTM_4Q -> ANNUAL_FY -> ...)
```

One-line edit.

---

## Acceptance for closing this tracker

- [ ] RM-1 spec diagram updated to reflect the new ordering.
- [ ] T9 row in spec test-scenarios table updated.
- [ ] `revenue_multiple.go:79` caller comment updated.

## Out of scope

- The substantive T7 stale-data check — that is tracked separately as RM-1.A.
- Other documentation drift not surfaced by Stream B's V/R/Q pass.
- A repo-wide line-number-in-comments sweep — that anti-pattern is tracked at VAL-4.
