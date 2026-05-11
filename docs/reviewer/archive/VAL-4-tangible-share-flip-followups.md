# VAL-4 — tangible_value_per_share PR #2 follow-up NITs

**Status:** OPEN — filed 2026-05-09 alongside the merge of Graham PR #2 (commit `360d677` on `worktree-agent-a2df13bff56a1ddae`).
**Severity:** Trivial. Two non-blocking NITs surfaced by the REVIEWER pass during the V/R/Q gate; both are sweep candidates rather than dedicated fix-effort items.
**Origin:** Stream C V/R/Q gate (2026-05-09), REVIEWER report on `internal/services/valuation/service.go::calculateTangibleValuePerShare`.
**Blocks:** Nothing — Graham PR #2 ships unchanged; these are advisory polish items only.
**Related specs:** `docs/refactoring/graham-floor-metrics-spec.md` §4.5 (the flip itself); CLAUDE.md "Common Gotchas" entry on `tangible_value_per_share` (already updated by `360d677`).

---

## NIT 1 — Comment line-number rot at `service.go:1344`

Current comment in the new `calculateTangibleValuePerShare`:

```go
// calculateTangibleValuePerShare ... matches the DCF-path chain at service.go ~lines 862-873
```

The numeric line-reference rots the moment any line is inserted earlier in the file. `service.go` is already over 1,400 lines and growing; cross-reference-by-line-number has a short half-life.

**Suggested fix.** Replace with a function-name reference that survives refactors:

```go
// calculateTangibleValuePerShare ... uses the same share-resolution chain as performDCFValuation.
```

This is worth filing because it is a **systemic anti-pattern** — comments that cite line numbers across the file are common throughout `service.go` and decay silently. A repo-wide grep (`rg "lines? \d+-\d+|line \d+"`) would likely surface several such hot spots. Closing this NIT could justifiably be paired with a sweep of the broader anti-pattern.

## NIT 2 — Unused declared field in regression-test diagnostic

`service_test.go:1184`: the `expectedShares` struct field on the table-driven test case is **declared but only consumed inside the failure-message format string**, never in an assertion. It compiles cleanly, but a future reader has to read the `t.Errorf` template to discover why the field exists at all — the connection between the field and the assertion is implicit.

**Suggested fix (one of):**

- Add a `// for diagnostic message only` doc comment on the field declaration.
- Surface `expectedShares` in an explicit assertion making the linkage explicit, e.g.:

  ```go
  assert.Equal(t, tt.tangibleAssets/tt.expectedShares, got, ...)
  ```

The second is preferred because it pins the math invariant in addition to the numeric output.

---

## Acceptance for closing this tracker

- [ ] NIT 1 fixed via comment-style swap (no behavior change).
- [ ] NIT 2 fixed via assertion change OR diagnostic comment.
- [ ] If a wider sweep is decided for the line-number anti-pattern, document additional hot spots found via `rg`.

## Out of scope

- Reverting or revising the denominator flip itself — Graham PR #2 is the canonical numeric semantics from v0.10.0 forward.
- Wider refactor of `service.go` — separate effort, beyond the scope of this tracker.
- Backward-compat shims for the 2-5% drift — explicitly rejected in the spec (`graham-floor-metrics-spec.md` §10 R1).

---

## Notes on the V/R/Q cycle that produced this tracker

The Stream C V/R/Q cycle (run in parallel on 2026-05-09) returned:

- VERIFIER: VERIFIED (RED→GREEN proof clean; coverage 100% on touched function, 89.7% package; no scope creep into `graham.go` / `entities` / `CalculationVersion`).
- REVIEWER: APPROVE with these 2 NITs.
- QA: PASS (AAPL/MSFT/JPM all drift in correct direction with magnitudes 0.42-0.94%, below the spec's 1-5% upper-bound band — interpreted as expected envelope, not strict floor; wire shape preserved; sibling per-share fields bit-for-bit identical).

Both NITs are tracked here so the deferral is on the record.
