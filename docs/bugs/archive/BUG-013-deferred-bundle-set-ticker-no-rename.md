# BUG-013: Deferred-bundle `SetTicker` doesn't update `b.root` — bundles land at `_no-ticker/` instead of `<TICKER>/`

| Field | Value |
|-------|-------|
| **ID** | BUG-013 |
| **Title** | `(*Bundle).SetTicker` on a deferred bundle silently fails because `os.Rename` runs against an on-disk directory that doesn't exist yet — bundle promotes at the original `_no-ticker/req_<id>/` path instead of the intended `<TICKER>/req_<id>/` path |
| **Severity** | MEDIUM (operator-visible, manifest-grep workaround exists) |
| **Status** | Resolved 2026-05-02 (merge `621f805`) |
| **Component** | `internal/observability/artifact/bundle.go::SetTicker` + handler call site `internal/api/v1/handlers/fair_value.go:258` |
| **Reported** | 2026-05-01 (Phase 2.C QA pass; surfaced when `Always=true` made every request hit the path) |
| **Affects** | All three auto-trigger paths: `on_error` (Phase 2.A), `on_quality_flag` (Phase 2.B), `always` (Phase 2.C). Manual `?trace=1` / `X-Midas-Trace` triggers are NOT affected (they use the eager `OpenBundle` path which sets the ticker directory at construction time). |
| **First flagged** | Phase 2.C QA, 2026-05-01. Pre-existed since Phase 2.A but only became visible at-scale when Phase 2.C's always-on knob made every request hit the deferred-bundle path. |

## Summary

When the trace middleware opens a deferred bundle, the on-disk directory does NOT exist yet — `OpenDeferredBundle` only allocates the in-memory `*Bundle` and sets `b.root` to `<root>/<date>/_no-ticker/req_<id>/` (the placeholder pre-ticker path). The handler then calls `bundle.SetTicker("AAPL")` at `internal/api/v1/handlers/fair_value.go:258` which:

1. Computes the new path: `<root>/<date>/AAPL/req_<id>/`
2. Calls `os.Rename(b.root, newPath)`
3. Rename returns ENOENT because `b.root` is an in-memory placeholder, not an on-disk directory yet
4. Increments `writeErrors` (because the rename failed)
5. **Does NOT update `b.root`** — the success-path-only update at `bundle.go:486` is skipped on rename error
6. Updates the manifest's `ticker` field to `"AAPL"` (this part DOES succeed because manifest is in-memory)

Later, when the trace middleware's defer block calls `bundle.Promote(...)`, `Promote` does `os.MkdirAll(b.root, 0o755)` — and `b.root` is still the unchanged `_no-ticker/` placeholder. So the bundle is created on disk at the wrong location.

## Symptoms

Operator inspecting an always-triggered bundle (or any auto-triggered bundle):

```bash
# Expected: bundle under per-ticker partition
$ ls artifacts/2026-05-01/AAPL/
# (empty — no bundles here despite the request having ticker=AAPL)

# Actual: bundle under _no-ticker placeholder
$ ls artifacts/2026-05-01/_no-ticker/
req_1f393a4a-7b8c-4dee-91a5-...

# Manifest IS correctly stamped:
$ cat artifacts/2026-05-01/_no-ticker/req_1f393a4a-.../00-manifest.json | jq .ticker
"AAPL"

# And outcome reflects the spurious writeErrors=1:
$ cat artifacts/2026-05-01/_no-ticker/req_1f393a4a-.../00-manifest.json | jq '{outcome, notes}'
{
  "outcome": "partial",
  "notes": "write_failures=1 queue_drops=0 oversize_lines=0"
}
```

## Impact

For Phase 2.A (`on_error` trigger): every 5xx-triggered bundle lands at `_no-ticker/`. Per-ticker filesystem navigation is broken; operators must `grep -l '"ticker": "AAPL"' artifacts/<date>/_no-ticker/*/00-manifest.json` to find the right bundles for a specific ticker. Workable but awkward.

For Phase 2.B (`on_quality_flag` trigger): same — every flagged-data bundle lands at `_no-ticker/`.

For Phase 2.C (`always` trigger): **THIS IS WHY THE BUG IS NOW THE DOMINANT CASE**. With `Always=true`, every single request lands at `_no-ticker/`. An operator running a debugging session for a single ticker (`AAPL`) sees `_no-ticker/` filled with hundreds of bundles for ALL tickers (the always knob captures everything). Per-ticker isolation is lost entirely.

For Phase 1 (manual `?trace=1` / `X-Midas-Trace`): NOT affected. Manual triggers use the eager `OpenBundle` path which creates the on-disk directory at construction time — the `os.Rename` in `SetTicker` succeeds against a real directory.

Secondary symptom: every auto-triggered bundle has `outcome="partial"` because the failed rename increments `writeErrors`, even though no actual data was lost. This conflates "bundle was incomplete" (real reason for partial) with "ticker rename failed" (cosmetic). Postmortem readers misinterpret the bundle as data-incomplete when it isn't.

## Why deferred from Phase 2.A

Not deferred — it was never noticed. Phase 2.A's QA exercised the `on_error` path with a synthetic NoRoute handler that has no ticker (so `SetTicker` was never called and the bug couldn't fire). Phase 2.B's QA exercised the `on_quality_flag` path with a real AAPL request through the integration test, but the test asserted only that the manifest had `trigger="on_quality_flag"` and didn't check the on-disk PATH. Phase 2.C's QA was the first to inspect a real on-disk bundle by hand at a stable path, found the bundle under `_no-ticker/`, and surfaced the bug.

## Recommended fix

Two viable shapes:

### Option A — Update `b.root` directly when in deferred mode (preferred)

In `SetTicker`, check `b.deferred.Load()`. When deferred (no on-disk directory yet), skip the `os.Rename` entirely and update `b.root` directly to the new path. `Promote` will then `MkdirAll` at the correct path. Eager-mode behavior unchanged (still uses `os.Rename` against the existing directory).

```go
func (b *Bundle) SetTicker(ticker string) {
    // ... existing setup ...

    newRoot := filepath.Join(b.config.RootPath, dateDir, sanitizedTicker, "req_"+b.requestID)

    if b.deferred.Load() {
        // Deferred mode: no on-disk directory exists yet. Just update
        // the in-memory path; Promote() will MkdirAll at the new location.
        b.mu.Lock()
        b.root = newRoot
        b.mu.Unlock()
        b.manifest.SetTicker(ticker)
        return
    }

    // Eager mode: directory exists on disk; rename it.
    if err := os.Rename(b.root, newRoot); err != nil {
        // ... existing error path (writeErrors++) ...
        return
    }
    b.mu.Lock()
    b.root = newRoot
    b.mu.Unlock()
    b.manifest.SetTicker(ticker)
}
```

Pro: minimal diff, clean separation of concerns, preserves eager-mode behavior exactly.
Con: introduces a deferred-vs-eager branch in `SetTicker` (small but real new conditional).

### Option B — Defer ticker storage, apply at Promote time

Store the intended ticker on the Bundle (`b.intendedTicker`). `SetTicker` only updates this field + the manifest. `Promote` consults `b.intendedTicker` and computes the correct path before MkdirAll.

Pro: completely eliminates the rename path for deferred bundles.
Con: bigger refactor; adds a new field; changes the Promote contract. More moving parts.

**Recommended: Option A** — cleaner, smaller, doesn't change the Promote contract.

## Tests required

- `TestSetTicker_DeferredBundle_UpdatesRootInMemory` — open deferred bundle with empty ticker, call `SetTicker("AAPL")`, assert `b.Root()` ends in `/AAPL/req_<id>` (NOT `/_no-ticker/`).
- `TestSetTicker_DeferredBundle_PromoteCreatesAtTickerPath` — open deferred bundle, SetTicker, Promote, assert on-disk directory is at `<date>/AAPL/req_<id>/` not `<date>/_no-ticker/req_<id>/`.
- `TestSetTicker_DeferredBundle_NoSpuriousWriteError` — same as above; assert `b.WriteErrors() == 0` after SetTicker (the rename didn't fail because it was never attempted in deferred mode).
- `TestSetTicker_EagerBundle_StillUsesRename` — open eager bundle (so directory exists), SetTicker, assert `os.Rename` was called and the directory moved on disk. Regression pin for the eager path.
- `TestSetTicker_EagerBundle_RenameFailureCountedAsWriteError` — existing test (`TestSetTicker_RenameFailureCountedAsWriteError`) — verify still passes; the rename-failure error path on eager bundles is unchanged.
- Integration: extend `TestNarrate_OnQualityFlagAutoBundle`, `TestNarrate_OnErrorAutoBundle`, `TestNarrate_AlwaysAutoBundle` to assert the bundle directory ends in `/AAPL/` (or whatever ticker was used) — currently they only check the manifest.

## Estimated cost

~25-40 LoC across `internal/observability/artifact/bundle.go` (the deferred-mode branch in SetTicker) + `bundle_test.go` (~4 new unit tests) + 3 integration test extensions. Single dedicated commit.

## Acceptance criteria

- After fix, an operator running `LOGGING_ARTIFACT_STORE_TRIGGERS_ALWAYS=true` and querying `/api/v1/fair-value/AAPL` finds the bundle at `artifacts/<date>/AAPL/req_<id>/` (NOT under `_no-ticker/`).
- Manifest `outcome="ok"` (NOT `"partial"`) for bundles that have no real write failures — the spurious `writeErrors=1` from the failed rename is gone.
- Manual `?trace=1` flow remains unchanged (regression pin holds).
- All existing tests pass without modification (Option A is backward-compatible for the eager path).

## Cross-references

- Phase 2.C QA finding §1 (2026-05-01) — first surfaced
- Phase 2.A and Phase 2.B both have this bug latently (manual-flow tests passed; auto-trigger tests didn't inspect on-disk paths)
- Related code: `internal/observability/artifact/bundle.go::SetTicker` (lines ~424-490 in the Phase 2.C branch), `internal/api/v1/handlers/fair_value.go:258` (call site)
- Spec: `docs/refactoring/observability-narrative-and-artifacts-spec.md` §7 (manifest layout assumes per-ticker partitioning)

---

## Resolution (2026-05-02)

**Merge:** `621f805` on master (`merge: fix/bug013-deferred-bundle-set-ticker — BUG-013 + latent b.root race fix (2 commits)`).

**Approach:** Option A from the Recommended Fix section above — `SetTicker` branches on `b.deferred.Load()`. In deferred mode, skip `os.Rename` (the on-disk dir doesn't exist yet) and update `b.root` in memory directly; `Promote()` later `MkdirAll`s at the correct per-ticker path. Eager-mode behavior unchanged (`os.Rename` still runs against the existing directory; pre-existing `TestSetTicker_RenameFailureCountedAsWriteError` regression pin still passes).

**Commits:**
- `f86d067` — operator-UX fix (Option A). 4 new unit tests + 1 eager regression pin + 3 integration test extensions (one per auto-trigger).
- `021e362` — REVIEWER follow-up addressing two latent concurrency surfaces uncovered while reviewing `f86d067`:
  - **HIGH-A**: `Promote` read `b.root` without holding `b.mu`. Pre-existing race surface latent since Phase 2.A; `f86d067` made it writable by adding an under-lock write in deferred-mode `SetTicker`. Fix: snapshot `b.root` under `b.mu` in `Promote` (mirrors `runWorker` pattern).
  - **MEDIUM-A**: TOCTOU window in `SetTicker` between `b.deferred.Load()` and `b.mu.Lock()`. A concurrent `Promote` could flip `deferred` between the check and the lock. Fix: re-check-after-lock under `pendingMu→b.mu` ordering (chosen against REVIEWER's suggested reverse direction to honor `bufferStream`'s documented prohibition on `b.mu→pendingMu` nesting). 3 new race-detector tests (`TestDeferredBundle_SetTickerPromoteRace_NoDataRace`, `TestSetTicker_DeferredBundle_AfterPromoteFallsThroughToEager`, `TestSetTicker_DeferredBundle_CloseDuringTOCTOURace`).

**QA evidence:**
- Independent lock-order audit: 10 `b.mu.Lock` sites + 5 `pendingMu.Lock` sites enumerated; ZERO `b.mu→pendingMu` nestings anywhere in `bundle.go`; exactly two NEW `pendingMu→b.mu` nestings introduced (the HIGH-A `Promote` snapshot + the MEDIUM-A `SetTicker` re-check).
- 150 race-detector iterations clean (`-race -count=3` across the new race tests).
- Live disk inspection: bundle landed at `artifacts/<date>/AAPL/req_<id>/` with manifest `outcome="ok"` and no `notes` field — the spurious `write_failures=1` from the pre-fix failed rename is gone. The visible disappearance of the bug.
- Coverage: 90.3% (above 90% gate; 0.5pp dip from baseline because the MEDIUM-A re-check tests fire non-deterministically across runs by design).
- All Phase 1 + 2.A + 2.B + 2.C regressions pass.

**Acceptance criteria from spec — all met:**
- ✅ Operator running `LOGGING_ARTIFACT_STORE_TRIGGERS_ALWAYS=true` and querying `/api/v1/fair-value/AAPL` finds the bundle at `artifacts/<date>/AAPL/req_<id>/` (NOT under `_no-ticker/`).
- ✅ Manifest `outcome="ok"` for bundles with no real write failures.
- ✅ Manual `?trace=1` flow unchanged (regression pin holds).
- ✅ All existing tests pass (Option A is backward-compatible for the eager path).
