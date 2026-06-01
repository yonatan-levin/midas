# BUG-012: Artifact bundle drops/oversize-lines surface only postmortem in manifest notes; no runtime Warn

| Field | Value |
|-------|-------|
| **ID** | BUG-012 |
| **Title** | `*Bundle` increments `dropped` / `writeErrors` / `oversizeLines` silently — operator only learns of an incomplete bundle by reading `00-manifest.json` after the fact |
| **Severity** | MINOR (operator-visibility) |
| **Status** | Fixed (pending HUMAN acceptance) — 2026-05-31 |
| **Component** | `internal/observability/artifact/bundle.go` |
| **Reported** | 2026-04-29 (Phase 2.B QA pass) |
| **Affects** | Phase 2.A on_error trigger AND Phase 2.B on_quality_flag trigger; does NOT affect manual `?trace=1` correctness |
| **First flagged** | Phase 2.A REVIEWER round 1 as follow-up B-4 (2026-04-27); re-surfaced by Phase 2.B QA (2026-04-29). The inline TODO that acknowledged the gap lived in `runWorker` at `internal/observability/artifact/bundle.go:1165-1168` (NOT the stale 935-941 reference used elsewhere in this doc); it was retired by the fix and replaced with the `maybeWarnWriteErr()` call. |

## Summary

The artifact bundle worker and the deferred-mode buffer both increment internal counters silently when they have to drop content:

- `dropped atomic.Int64` — snapshot dropped because the worker queue was full (256-job default).
- `writeErrors atomic.Int64` — `os.WriteFile` (or `os.OpenFile` for the deferred-flush append path) failed.
- `oversizeLines atomic.Int64` — a single log line exceeded `MaxStreamLineBytes` (256 KiB).
- `pendingBytes` overflow — oldest snapshot evicted FIFO to make room (deferred mode only).

These counters are formatted into the bundle's `00-manifest.json` `notes` field at `Close()` time as `"write_failures=N queue_drops=M oversize_lines=O"`, with `outcome` degraded to `"partial"`. Operators only discover the incomplete bundle when they open it and inspect the manifest.

## Impact

For the auto-triggers (on_error and on_quality_flag), this is the worst-case discovery moment: an operator opens the bundle expecting full forensic context for a 5xx incident or a flagged data-quality run, finds the manifest annotated with `oversize_lines=12 queue_drops=3`, and now has to figure out (a) which lines were dropped, (b) whether the missing context was the diagnostic context, (c) whether to bump the cap and re-run.

A runtime Warn line at drop-time would let operators tail the host log and see drops as they happen — they could correlate with the upstream noisy debug call that pushed the buffer over cap (e.g., a giant SEC payload logged at Debug) BEFORE the bundle is finalized.

## Why deferred from Phase 2.A

The fix requires changing the `Bundle` constructor signature to accept a `*zap.Logger` (or take it via context). The artifact package is currently logger-free by design — keeps the package domain-free and avoids circular imports. Adding a logger crosses that boundary and touches:

- `OpenBundle` and `OpenDeferredBundle` signatures (both constructors)
- Every test that constructs bundles directly (~30 call sites across `bundle_test.go`, `bundle_deferred_test.go`, `bundle_quality_flag_test.go`, `zap_core_test.go`)
- `internal/api/middleware/trace.go` (passes the logger through)
- `internal/api/server.go` wiring

Deliberate scope deferral. The `runWorker` TODO at `internal/observability/artifact/bundle.go:1165-1168` documented the gap and noted the intended approach (the earlier "935-941" reference was stale).

## Recommended fix

Two viable shapes:

### Option A — Constructor parameter
```go
func OpenDeferredBundle(cfg Config, requestID, ticker string, trigger Trigger, logger *zap.Logger) (*Bundle, error)
func OpenBundle(cfg Config, requestID, ticker string, trigger Trigger, logger *zap.Logger) (*Bundle, error)
```
Pro: explicit, no hidden state. Con: every existing call site must update.

### Option B — Functional option (preferred)
```go
type BundleOption func(*Bundle)
func WithLogger(l *zap.Logger) BundleOption { ... }

func OpenDeferredBundle(cfg Config, requestID, ticker string, trigger Trigger, opts ...BundleOption) (*Bundle, error)
```
Pro: backward-compatible (existing call sites compile unchanged); logger is opt-in. Con: nil-logger fallback path adds a tiny conditional.

### At-most-once gating
Whatever shape, the Warn must be at-most-once per bundle (per counter, or single combined Warn) to avoid log spam. Pattern:

```go
type Bundle struct {
    ...
    warnedDrop atomic.Bool
    warnedOversize atomic.Bool
    warnedWriteErr atomic.Bool
}

func (b *Bundle) maybeWarnDrop() {
    if b.logger == nil || !b.warnedDrop.CompareAndSwap(false, true) {
        return
    }
    b.logger.Warn("artifact.bundle.drops",
        zap.String("request_id", b.requestID),
        zap.String("trigger", string(b.manifest.Trigger())),
    )
}
```

Subsequent drops increment counters silently as today; the first one fires the Warn.

### Tests required
- `TestBundle_FirstDropEmitsWarn` — load up the queue past cap, assert Warn fires exactly once (use `zaptest.NewObserver`).
- `TestBundle_SecondDropDoesNotEmitDuplicateWarn` — at-most-once behavior.
- `TestBundle_OversizeLineEmitsWarn` — single 1 MiB line, Warn fires.
- `TestBundle_NilLoggerNoOp` — backward-compat with existing call sites that pass no logger.

## Estimated cost

~80-120 LoC across 4-6 files. Recommend a dedicated commit (`feat(observability): runtime Warn on bundle buffer drops`) that can be reviewed on its own merits, NOT bundled with a feature commit.

## Acceptance criteria

- An operator with `LOGGING_ARTIFACT_STORE_TRIGGERS_ON_ERROR=true` on a busy server sees a host-log Warn within seconds of a snapshot drop, identifying the request_id and the cause (queue/bytes/oversize).
- Existing tests still pass without modification (Option B) OR with a single mechanical update (Option A).
- The manifest `notes` field still records the full counter values at Close time (current behavior unchanged).

## Resolution (as shipped)

Implemented **Option B (functional option)** on branch `fix/bug-012-bundle-warn-on-drop` (2026-05-31), purely additive — no drop/eviction/promote/close logic changed; every counter `.Add(1)` is preserved and the `Close()` manifest `notes`/`outcome="partial"` accounting is untouched.

- **Seam:** `type BundleOption func(*Bundle)` + `WithLogger(*zap.Logger)`; both `OpenBundle` / `OpenDeferredBundle` are now variadic (`opts ...BundleOption`), so the ~30 existing call sites compile unchanged. Nil logger → every warn is a no-op (back-compat identical to pre-fix).
- **At-most-once:** three `atomic.Bool` gates (`warnedDrop` / `warnedOversize` / `warnedWriteErr`), each helper short-circuits `if b.logger == nil || !b.warned*.CompareAndSwap(false, true) { return }` (nil-check BEFORE the CAS, so a nil-logger bundle never burns its gate). The FIRST event of each kind emits; later ones stay silent (counters still increment).
- **Lock-free helpers:** `maybeWarnDrop` / `maybeWarnOversize` / `maybeWarnWriteErr` read only the immutable `b.requestID` / `b.trigger` (a new immutable field set at construction), the atomic gate, and `b.logger`. They MUST NOT touch `b.manifest` / `b.mu` / `b.pendingMu` because they fire from inside `pendingMu`-held eviction loops and from the worker goroutine — verified race-clean.
- **Cause granularity (closes the acceptance-criterion "queue/bytes/oversize" intent + review follow-ups):**
  - `artifact.bundle.snapshot_dropped` carries `reason` = `queue_full` (worker queue / deferred count-cap saturated) **or** `bytes_overflow` (deferred `PendingBytesCap` exceeded / single payload bigger than the cap).
  - `artifact.bundle.oversize_line` carries `max_stream_line_bytes`.
  - `artifact.bundle.write_error` carries `site` naming the failing call (`worker_write`, `promote_stream_open/write/close`, `setticker_mkdir/rename/stream_close`, `appendstream_open/write/newline`, `open_config_snapshot`, `promote_config_snapshot`, `close_stream_sync/close`).
  - Every warn carries `request_id` + `trigger` for host-log correlation.
- **Wiring:** `TraceMiddleware(cfgN, cfgA, logger *zap.Logger)` threads the **plain singleton** `s.logger` (NOT the BundleSink-wrapped request logger, which would re-enter `AppendStream`) into both constructors via `WithLogger`.
- **Tests** (`bundle_warn_test.go`, `zaptest/observer`): the 4 spec-required tests + `TestBundle_WriteErrorEmitsWarn` (asserts `site=worker_write`) + `TestBundle_QueueFullDropEmitsQueueFullReason` (pins the `queue_full` vs `bytes_overflow` reason split). All pass under `-race`; the artifact package's own pre-existing concurrency tests stay green.

## Cross-references

- Phase 2.A REVIEWER round 1 finding B-4 (2026-04-27)
- Phase 2.B QA (2026-04-29) Finding #2 — re-surfaced
- Inline TODO at `internal/observability/artifact/bundle.go:1165-1168` (the "935-941" reference cited at the top of this doc was stale; the real TODO lived in `runWorker` and was retired by the fix)
