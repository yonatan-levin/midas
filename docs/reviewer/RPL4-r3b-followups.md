# RPL-4 — Phase 2.D R3b deferred items (post-merge follow-ups)

**Status:** OPEN — filed 2026-05-09 as R3b's post-merge cleanup. R3b shipped on master via merge `0741958` (2026-05-09). Phase 2.D is COMPLETE; R3b's V/R/Q gate cycle returned zero MAJOR/BLOCKER findings. The 4 items below are MINOR-or-lower and were explicitly deferred per the merge-commit body. R3b's plan §10 outcome table marks each as "deferred to RPL-4."
**Severity:** Mixed — 1 MINOR (spec/sample divergence, documentation call), 2 MINOR (cross-platform polish + cleaner-team item), 1 documented-residual (coverage gap with explicit accept-clause from plan §6).
**Origin:** Consolidated from R3b's V/R/Q gate-cycle reports:
- VERIFIER (verdict: VERIFIED WITH NOTES) — coverage residuals
- REVIEWER (verdict: APPROVED WITH NOTES) — section-order vs spec sample
- QA (verdict: PASS WITH NOTES) — `as_of` nondeterminism + path normalization

## Context

R3b's cycle was the cleanest of any Phase 2.D dispatch:
- All 7 plan stages shipped in their planned order
- Three iterative cleanup commits (`257ff5c`, `a5f08f3`, `b7a9bdc`) closed the gopls iterative-diagnostic surfacing pattern with an explicit cutoff
- One V/R/Q-polish commit (`573e517`) folded six high-value findings (REVIEWER #2-#4 + QA M3/D1/B2) into a single pre-merge follow-up
- Zero MAJOR/BLOCKER findings across all 3 gates
- Master's parallel-divergent 10 commits had zero file overlap with R3b's surface

The 4 items below are what's left. None gate Phase 2.D's COMPLETE status; they are scheduled as Phase 2.E candidates per their natural fit.

---

## Section A — Spec / sample documentation divergence (MINOR)

### RPL-4a — Stage L.1 section-order vs spec §7 sample

**Severity:** MINOR (spec/sample divergence; documentation-only resolution preferred).
**Origin:** REVIEWER cycle 1 finding #1.
**Location:** `internal/observability/replay/output.go:347-353` (writeResultRow's section-emission order) + `docs/refactoring/observability-replay-tooling-spec.md` v0.4 §7 sample at L498-510.

**Issue:** Spec §7's verbose-mode sample places the `Stage diffs:` section BEFORE `Response diffs:`, with an explicit `"Response diffs:"` header. Implementation flips the order: response-level diff lines render FIRST (no header), then `Stage diffs:` section follows. `TestRenderText_VerboseTrue_EmitsBothResponseAndStageDiffs` (committed in `b87b3b7`) pins the as-shipped order as a contract.

**REVIEWER's recommendation:** Treat as another acceptable deviation. Implementation is internally consistent (response-first matches the existing render order for non-verbose mode where stage_diffs don't apply). Update spec v0.5's §7 sample to reflect the shipped order rather than flipping the implementation.

**Fix path A (recommended):** Spec v0.5 (post-shipment HUMAN dispatch per plan §9) updates §7 sample text to match implementation:
```
Response diffs:
  - dcf_value_per_share: 156.42 -> 156.81 (rel_drift +0.25%)
  - wacc: 0.092 -> 0.094 (rel_drift +2.17%)
Stage diffs:
  13-wacc.json:
    - cost_of_equity: 0.118 -> 0.121 (rel_drift +2.54%)
  ...
```
Plus append a Deviation #8 row to the merge-time outcome record noting the spec-sample update.

**Fix path B (alternative):** Code-side flip in `writeResultRow` to match spec sample. Requires:
- Reorder the section emission in `writeResultRow`
- Add `"Response diffs:"` header above the existing diff lines
- Update `TestRenderText_VerboseTrue_EmitsBothResponseAndStageDiffs` assertion
- Regenerate `internal/observability/replay/testdata/golden/json_with_drifted_within_tolerance.json` AND any other text-render goldens
- Estimated effort: 30-45 minutes including golden regeneration

**Estimated effort (path A):** 5 minutes (one-line spec edit during the post-shipment dispatch).

---

## Section B — Cross-platform polish (MINOR)

### RPL-4b — Windows backslash path normalization in JSON output

**Severity:** MINOR.
**Origin:** QA cycle 1 finding M2.
**Location:** `internal/observability/replay/output.go` JSON rendering of `Result.Bundle` field.

**Issue:** On Windows, the `bundle` field in JSON output uses native backslash-escaped paths:
```json
{"bundle": "C:\\Users\\YONATA~1\\...\\req_e7547aa4..."}
```
Most JSON consumers handle this fine; some shell pipelines (e.g., `jq`-piped-to-`xargs`-piped-to-Linux-tools) don't, and operators copy-pasting the path into a Unix-style command get cryptic failures.

**Fix:** Normalize path separators to `/` for JSON-mode output specifically (preserve native separators for text-mode where operators see them visually):
- In `RenderJSON` (or wherever `Result.Bundle` is marshaled): convert `\\` → `/` before serialization.
- Use `filepath.ToSlash(res.Bundle)` from stdlib — purpose-built for this.
- Add a test case asserting JSON output's `bundle` field uses forward slashes regardless of host OS.

**Estimated effort:** 15 minutes including test.

---

## Section C — Cleaner-team item (out of replay scope) (MINOR)

### RPL-4c — Cleaner's `as_of` field nondeterminism across runs

**Severity:** MINOR (data-quality issue surfaced by R3b's diff path; root cause is OUTSIDE R3b's scope — cleaner-team backlog candidate).
**Origin:** QA cycle 1 finding M1.
**Location:** Cleaner pipeline — `internal/services/datacleaner/...`.
**Surfaced by:** R3b's `--diff-stages --from=parsed` against the same MXL bundle across 3 runs. The cleaner's `as_of` field appeared in 1 of 3 runs and was absent in the other 2.

**Root cause:** Cleaner picks `as_of` based on "today's most recent FY data point" using the wall-clock at run time. Across replay runs done minutes apart, the FY boundary can shift, causing the field to flip on/off.

**Why this is a problem:** The whole point of replay is reproducibility — running R3b's CLI against the same bundle should produce the same output. Per Phase 2.D D10 invariant ("manifestClock pins engine to capture-time"), the engine itself IS pinned to the bundle's manifest timestamp. The cleaner's `as_of` derivation evidently slipped past that pin.

**Fix path:** Investigation in the cleaner pipeline:
1. Find the code path computing `as_of` from "today's most recent FY data point."
2. Confirm whether it consults `time.Now()` directly OR consults the engine's bound `Clock` interface (the same one Stage D10 wired manifestClock into).
3. If it bypasses the Clock, route it through the Clock so replay's manifestClock pin propagates.

**Fix path is NOT R3b scope** — the cleaner is `internal/services/datacleaner/`, not the replay tooling. R3b correctly observes and reports the drift; the cleaner team owns the fix.

**Estimated effort (cleaner team):** 30-60 minutes once the as_of derivation site is located.

---

## Section D — Documented coverage residuals (Plan §6 escape clause)

### RPL-4d — `internal/observability/replay/` package coverage 82.5% (gate ≥90%)

**Severity:** Documented residual (plan §6 explicit accept-clause).
**Origin:** VERIFIER cycle 1 + plan §6 acknowledgment.
**Locations:**
- `internal/observability/replay/output.go::writeStageDiffSection` 70.8% (per-file gate ≥95%)
- `internal/observability/replay/output.go::RenderJSON` 90.9% (per-file gate ≥95%)
- `internal/observability/replay/stage_diff.go::walkSlice` 0%
- `internal/observability/replay/stage_diff.go::genericEqual` 0%
- `internal/observability/replay/stage_diff.go::diffStage` 83.3%

**Issue:** Coverage falls below the plan's targets. **All uncovered branches are defensive:**
- `writeStageDiffSection` / `RenderJSON` — `if err != nil` write-error paths against `io.WriteString` / `fmt.Fprintf`. Closing requires injecting a write-failing `io.Writer` into the renderer test.
- `walkSlice` / `genericEqual` — defensive code with no production reach. Stage files 10/12/13/15 are scalar maps (per the bundle taxonomy), never arrays; `genericEqual` is the catch-all kind fallback for type drift the production code never produces.
- `diffStage` — defensive read-error and parse-error branches against `os.ReadFile` / `json.Unmarshal`.

**Why this is acceptable per plan §6:** The plan's coverage section explicitly accepts residual gaps "concentrated in defensive branches with no production reach." VERIFIER's cycle-1 report independently confirmed the gap is in defensive code, not in untested business logic.

**Fix paths (if a future contributor wants to close the gap):**
- For `writeStageDiffSection` / `RenderJSON`: add a `failingWriter` test fixture that returns `io.ErrShortWrite` after N bytes; assert the renderer propagates the error correctly.
- For `walkSlice` / `genericEqual`: synthesize a JSON shape that contains arrays at the top level (e.g., `[{"a": 1}, {"a": 2}]` instead of `{"items": [...]}`) and feed it through `diffStage` directly. Note: not exercising production behavior, but lifts coverage.
- For `diffStage`'s read-error branch: pass a non-existent-but-not-ErrNotExist path (e.g., a directory where a file is expected) to trigger an `os.PathError` other than `os.ErrNotExist`.

**Estimated effort:** 30-45 minutes for a complete close. NOT a Phase 2.D blocker.

---

## Why deferred to RPL-4

R3b's V/R/Q cycle had a tight cadence with three back-to-back cleanup commits (`a5f08f3`, `b7a9bdc`, `573e517`) before merge. Continuing to iterate on cosmetic findings would have dragged Phase 2.D's COMPLETE milestone past the 2026-05-09 mark with diminishing returns. The 4 items above are well-scoped Phase 2.E candidates:
- **RPL-4a** is a 5-minute spec edit during the post-shipment HUMAN dispatch
- **RPL-4b** is 15 minutes of cross-platform polish
- **RPL-4c** is a cleaner-team item, not R3b's responsibility (replay is observing, not causing)
- **RPL-4d** is explicitly accepted by plan §6's escape clause

Phase 2.D is COMPLETE as of merge `0741958` (2026-05-09). Filing this tracker preserves the open items so they're not lost across context boundaries.

## Acceptance criteria

- [ ] **RPL-4a** — Spec v0.5 §7 sample updated to match shipped section order (HUMAN post-shipment dispatch); OR code flipped to match spec sample (Phase 2.E).
- [ ] **RPL-4b** — `filepath.ToSlash` normalization applied to `Result.Bundle` in JSON output + test (Phase 2.E).
- [ ] **RPL-4c** — Cleaner team investigates and routes `as_of` derivation through the bound `Clock` interface (Phase 2.E or later — owned by cleaner team, not replay).
- [ ] **RPL-4d** — Either (a) close the coverage gap with `failingWriter` + array-shape fixtures (Phase 2.E), OR (b) keep the documented residual as-is per plan §6 escape clause (no action). Default: (b).

## Traceability

- **Filed by:** Phase 2.D R3b post-merge cleanup (2026-05-09) consolidating findings from VERIFIER cycle 1 + REVIEWER cycle 1 + QA cycle 1
- **R3b merge:** `0741958` (2026-05-09)
- **Spec it relates to:** `docs/refactoring/observability-replay-tooling-spec.md` v0.4 (will bump to v0.5 in post-shipment HUMAN dispatch)
- **Implementation plan:** `docs/refactoring/observability-replay-tooling-r3b-implementation-plan.md` v1 (now historical, all 7 stages SHIPPED per §10)
- **Prior follow-up files:** `RPL1-replay-walk-and-output-r3-followups.md` (R0+R1, all items folded into R3 plan v2), `RPL2-r2-followups.md` (R2, all items folded into R3 plan v2), `RPL3-r3a-followups.md` (R3a, all items folded into R3b plan v1 — should be marked RESOLVED in the post-shipment HUMAN dispatch)
- **R3b worktree branch:** `worktree-agent-a927bf55184a27f2a` — preserved for git-blame archeology
