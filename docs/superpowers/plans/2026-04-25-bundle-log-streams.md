# Bundle Log Streams Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close QA finding MINOR-1 from the 2026-04-25 QA pass on `feat/observability-narrative` — the artifact bundle currently does NOT contain the `99-narrate.jsonl` and `99-debug-trace.jsonl` files that spec §7.1 / §7.3 promise. The narrate stream is observable in the host process's zap logger output but is not duplicated into the bundle directory, weakening the spec's "self-describing bundle — `cat` one directory for everything" promise.

**Architecture:** Add a `zapcore.Core` wrapper that, when installed by the trace middleware on bundle open, forwards every log entry to the underlying core unchanged AND additionally appends the entry to one or both bundle JSONL files based on entry attributes:

- Any entry with field `event="narrate"` → appended to `<bundle>/99-narrate.jsonl`
- Any entry at Debug level (`zap.DebugLevel`) → appended to `<bundle>/99-debug-trace.jsonl`

Per-request scope: each request's wrapper writes only to that request's bundle directory. The wrapper is installed in trace middleware after the bundle is opened, replacing `logctx.From(ctx)`'s logger via `logctx.Inject(ctx, wrapped)`. On bundle close, the wrapper's `Sync()` flushes any remaining data and closes the file handles.

**Tech Stack:** Go 1.23, `go.uber.org/zap`, `go.uber.org/zap/zapcore`. No new dependencies. Re-uses existing `Bundle` struct and `OpenBundle` lifecycle.

**Worktree:** `C:\Users\Yonatan Levin\Documents\Programming\Projects\FinTech\Strade\midas\.worktrees\feat-observability-narrative`
**Branch:** `feat/observability-narrative` (this will be a 5th commit on top of `41bd91c`).

**Spec sections:**
- `docs/refactoring/observability-narrative-and-artifacts-spec.md` §7.1 (bundle layout — lists `99-narrate.jsonl` and `99-debug-trace.jsonl`)
- `docs/refactoring/observability-narrative-and-artifacts-spec.md` §7.3 (capture mechanics — describes the `zapcore.Core` wrapper mechanism)

---

## Task 1: Add `Bundle.AppendStream` for line-appendable JSONL files

**Files:**
- Modify: `internal/observability/artifact/bundle.go`
- Modify: `internal/observability/artifact/bundle_test.go`

**Rationale:** The `BundleSink` wrapper (Task 2) needs a way to append bytes to a named file inside the bundle directory without going through the existing `Snapshot` / `SnapshotRaw` machinery (which assumes a one-shot per-phase write with manifest registration). JSONL streams need to grow over the request lifetime.

- [ ] **Step 1.1: Add `AppendStream(filename string, line []byte) error` method on `*Bundle`**

  Behavior:
  - No-op when `b == nil` (nil-receiver safety, matches existing methods).
  - No-op when bundle has been closed (check existing `closed` flag).
  - Opens `<bundleDir>/<filename>` in `O_APPEND|O_CREATE|O_WRONLY` mode if not already open; caches the `*os.File` in a per-filename map on `Bundle`.
  - Writes `line` followed by `\n` if `line` doesn't already end in `\n`.
  - Increments `writeErrors` (added in commit `41bd91c`) on any failure and returns the error.
  - Thread-safe: protect the file-handle cache with the existing `mu sync.Mutex`.

  The cache is needed because each request emits ~17 narrate lines and potentially hundreds of Debug lines — re-opening the file on every Append would be slow.

- [ ] **Step 1.2: Extend `Bundle.Close` to flush + close cached stream files**

  After the snapshot worker drains:
  - Lock `mu`.
  - For each cached `*os.File`, call `f.Sync()` then `f.Close()`. Aggregate any errors into `writeErrors`.
  - Clear the cache.

- [ ] **Step 1.3: Add unit test `TestBundle_AppendStream_Persists`**

  In `bundle_test.go`:
  - Open a bundle in a `t.TempDir()`.
  - Call `AppendStream("99-narrate.jsonl", []byte(`{"event":"narrate","phase":"test"}`))` 5 times.
  - Close the bundle.
  - `os.ReadFile` the resulting file; assert 5 lines, each parses as JSON.

- [ ] **Step 1.4: Add unit test `TestBundle_AppendStream_NilSafe`**

  Calls `var b *Bundle; err := b.AppendStream("foo", []byte("x"))`. Asserts `err == nil` (no panic, no error — same contract as other nil-receiver methods).

- [ ] **Step 1.5: Add unit test `TestBundle_AppendStream_AfterClose_NoOps`**

  Opens bundle, closes it, then calls `AppendStream`. Asserts no error and no file written (or file unchanged from pre-close state).

- [ ] **Step 1.6: Run unit tests + coverage**

  ```bash
  cd C:\Users\Yonatan Levin\Documents\Programming\Projects\FinTech\Strade\midas\.worktrees\feat-observability-narrative
  go test -cover -run TestBundle ./internal/observability/artifact/
  ```
  Expected: PASS, coverage ≥ 91% (current 91.7%).

---

## Task 2: Implement the `BundleSink` `zapcore.Core` wrapper

**Files:**
- New: `internal/observability/artifact/zap_core.go`
- New: `internal/observability/artifact/zap_core_test.go`

**Rationale:** The wrapper must (a) forward every entry to the underlying core unchanged so existing zap output is preserved, and (b) tee `event=narrate` entries to `99-narrate.jsonl` and Debug-level entries to `99-debug-trace.jsonl`.

- [ ] **Step 2.1: Define `BundleSink` struct**

  ```go
  // BundleSink is a zapcore.Core wrapper that forwards every log entry to the
  // wrapped core unchanged, while also teeing entries with event="narrate" to
  // <bundle>/99-narrate.jsonl and entries at Debug level to
  // <bundle>/99-debug-trace.jsonl.
  //
  // Zero impact when bundle is nil (forwards verbatim).
  type BundleSink struct {
      zapcore.Core
      bundle  *Bundle
      encoder zapcore.Encoder // JSON encoder for JSONL serialization, owned by the sink
      fields  []zapcore.Field // accumulated fields from With() calls
  }

  func NewBundleSink(wrapped zapcore.Core, bundle *Bundle) zapcore.Core { ... }
  ```

- [ ] **Step 2.2: Implement `Check`**

  Required by `zapcore.Core` interface. Forward to wrapped:
  ```go
  func (s *BundleSink) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
      if s.Enabled(ent.Level) {
          return ce.AddCore(ent, s)
      }
      return ce
  }
  ```

- [ ] **Step 2.3: Implement `Write`**

  Required by `zapcore.Core` interface. The wrapped core receives the entry first; then we tee to bundle if applicable.

  ```go
  func (s *BundleSink) Write(ent zapcore.Entry, fields []zapcore.Field) error {
      if err := s.Core.Write(ent, fields); err != nil {
          return err
      }
      if s.bundle == nil {
          return nil
      }

      isNarrate := false
      for _, f := range fields {
          if f.Key == "event" && f.String == "narrate" {
              isNarrate = true
              break
          }
      }
      // Also check accumulated fields from With() (rare for event=narrate, common for request_id).
      if !isNarrate {
          for _, f := range s.fields {
              if f.Key == "event" && f.String == "narrate" {
                  isNarrate = true
                  break
              }
          }
      }

      if !isNarrate && ent.Level > zapcore.DebugLevel {
          return nil // not narrate, not debug — nothing to tee
      }

      // Encode to JSON. Use the sink's owned encoder so we don't fight the wrapped core's encoder choice.
      buf, err := s.encoder.EncodeEntry(ent, append(s.fields, fields...))
      if err != nil {
          return nil // encoder errors are best-effort for the sink — don't fail the wrapped write
      }
      defer buf.Free()

      if isNarrate {
          _ = s.bundle.AppendStream("99-narrate.jsonl", buf.Bytes())
      }
      if ent.Level == zapcore.DebugLevel {
          _ = s.bundle.AppendStream("99-debug-trace.jsonl", buf.Bytes())
      }
      return nil
  }
  ```

- [ ] **Step 2.4: Implement `With`**

  Required by `zapcore.Core` interface. Accumulate fields for later write-time evaluation:

  ```go
  func (s *BundleSink) With(fields []zapcore.Field) zapcore.Core {
      return &BundleSink{
          Core:    s.Core.With(fields),
          bundle:  s.bundle,
          encoder: s.encoder.Clone(),
          fields:  append(append([]zapcore.Field{}, s.fields...), fields...),
      }
  }
  ```

- [ ] **Step 2.5: Implement `Sync`**

  Forward to the wrapped core. Bundle stream files are flushed on bundle close (Task 1.2), not on every Sync.

- [ ] **Step 2.6: Add unit test `TestBundleSink_TeesNarrateLines`**

  - Construct `zaptest/observer.New` for the wrapped core.
  - Open a real `Bundle` in a `t.TempDir()`.
  - Wrap with `NewBundleSink`.
  - Build a logger from the wrapper.
  - Emit 3 lines via `logger.Info("foo", zap.String("event", "narrate"), zap.String("phase", "test"))`.
  - Assert: observer saw 3 entries (forwarding works); `99-narrate.jsonl` in bundle has 3 lines.

- [ ] **Step 2.7: Add unit test `TestBundleSink_TeesDebugLines`**

  Same setup. Emit 5 Debug lines without `event=narrate`. Assert: observer saw 5; `99-debug-trace.jsonl` has 5 lines; `99-narrate.jsonl` does not exist.

- [ ] **Step 2.8: Add unit test `TestBundleSink_NilBundleIsTransparent`**

  `NewBundleSink(wrapped, nil)`. Emit 10 entries of various levels. Assert: observer saw all 10; no files created anywhere.

- [ ] **Step 2.9: Add unit test `TestBundleSink_PreservesWithFields`**

  - Wrap, then call `.With(zap.String("request_id", "req_X"))` to get a sub-logger.
  - Emit a narrate line on the sub-logger.
  - Assert: the JSONL line in the bundle includes `request_id=req_X` (proving With-fields propagate to the tee).

- [ ] **Step 2.10: Run unit tests + coverage**

  ```bash
  go test -cover -run "TestBundleSink|TestBundle_" ./internal/observability/artifact/
  ```
  Expected: PASS, package coverage ≥ 91%.

---

## Task 3: Wire `BundleSink` into the trace middleware

**Files:**
- Modify: `internal/api/middleware/trace.go`
- Modify: `internal/api/middleware/trace_test.go`

**Rationale:** The wrapper must be installed **after** the bundle is opened (so it has a non-nil bundle to tee into) but **before** any narrate emission happens (so all 17 phases get captured). The `request.received` narrate line should land in the bundle JSONL.

- [ ] **Step 3.1: Inspect existing trace middleware lifecycle**

  Read `internal/api/middleware/trace.go` end-to-end. Identify:
  - Where `OpenBundle` is called (the success path post-fix from `41bd91c`).
  - Where the request-scoped logger is currently set on context (likely via `logctx.Inject`).
  - Where the bundle is closed (response path).

- [ ] **Step 3.2: Install `BundleSink` wrapper after successful bundle open**

  Sketch:

  ```go
  // After bundle, openErr := artifact.OpenBundle(...) returns successfully:
  if bundle != nil {
      base := logctx.From(ctx)                                    // current request-scoped logger
      wrapped := base.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
          return artifact.NewBundleSink(c, bundle)
      }))
      ctx = logctx.Inject(ctx, wrapped)
      c.Request = c.Request.WithContext(ctx)
  }
  ```

  Important: the wrapper must wrap the request-scoped logger (which already carries `request_id`), not the singleton — otherwise the JSONL lines won't carry `request_id` and won't correlate.

- [ ] **Step 3.3: Confirm bundle close happens AFTER the response has been written**

  This is critical — if `bundle.Close` runs while in-flight `Write` calls are still pending, JSONL lines emitted during late response handling will be lost. The existing close path on `41bd91c` should already happen after `c.Next()`, but verify.

- [ ] **Step 3.4: Add middleware test `TestTrace_BundleSinkInstalledAndCaptures`**

  - Set up gin test recorder.
  - Configure `cfgA.Enabled = true`.
  - Hit a handler that emits one narrate line and one Debug line via `logctx.From(c.Request.Context())`.
  - Use `?trace=1` to open a bundle.
  - After response, find the bundle dir (use the same pattern as the integration test).
  - Assert `99-narrate.jsonl` has the narrate line.
  - Assert `99-debug-trace.jsonl` has the debug line.
  - Assert the underlying observer also saw both (forwarding still works).

- [ ] **Step 3.5: Add middleware test `TestTrace_NoBundleSink_WhenDisabled`**

  Configure `cfgA.Enabled = false`. Confirm:
  - No bundle dir created.
  - Logger from `logctx.From(ctx)` is NOT wrapped (test by inspecting the core type if exposed, OR by emitting and confirming no JSONL anywhere).

- [ ] **Step 3.6: Run middleware tests + coverage**

  ```bash
  go test -cover ./internal/api/middleware/
  ```
  Expected: PASS, coverage ≥ 92.6% (current).

---

## Task 4: Extend integration test to assert bundle JSONL files

**Files:**
- Modify: `internal/integration/narrate_artifact_test.go`

**Rationale:** QA explicitly recommended pinning the new behavior at the integration layer so a regression silently dropping the JSONL writer would fail a test.

- [ ] **Step 4.1: Locate the existing TestNarrateArtifact_TraceOn_EmitsStreamAndBundle test**

  Read the test's bundle-discovery logic (the `findBundleDir` helper or equivalent).

- [ ] **Step 4.2: Add assertions on `99-narrate.jsonl`**

  After the test verifies the manifest:
  ```go
  narrateStream := filepath.Join(bundleDir, "99-narrate.jsonl")
  data, err := os.ReadFile(narrateStream)
  require.NoError(t, err, "99-narrate.jsonl must exist in opened bundle")

  lines := strings.Split(strings.TrimSpace(string(data)), "\n")
  require.GreaterOrEqual(t, len(lines), 13, "narrate stream should have at least 13 phase entries (matches the 13 phases the seeded test path actually exercises)")

  for i, line := range lines {
      var entry map[string]interface{}
      require.NoError(t, json.Unmarshal([]byte(line), &entry), "line %d not valid JSON: %s", i, line)
      require.Equal(t, "narrate", entry["event"], "line %d missing event=narrate", i)
      require.Equal(t, requestID, entry["request_id"], "line %d has wrong request_id", i)
  }
  ```

- [ ] **Step 4.3: Confirm `99-debug-trace.jsonl` semantics**

  If the test runs at `info` level, the file should NOT exist (no debug entries to capture). If it runs at `debug`, it should exist with at least one entry.

  Inspect the test's logger setup. If level is info, assert `_, err := os.Stat(debugStream); err != nil`. If debug, assert it exists and has ≥ 1 line.

- [ ] **Step 4.4: Run integration test**

  ```bash
  go test -v -run TestNarrateArtifact ./internal/integration/
  ```
  Expected: PASS.

---

## Task 5: Update the spec to document the implementation

**Files:**
- Modify: `docs/refactoring/observability-narrative-and-artifacts-spec.md`

- [ ] **Step 5.1: Add Change Log entry**

  Append to the Change Log table at the bottom of the spec:

  ```
  | 2026-04-25 | v0.2 — §7.1 + §7.3 closed: 99-narrate.jsonl and 99-debug-trace.jsonl now written into bundles via a BundleSink zapcore.Core wrapper installed by trace middleware. Closes QA-2026-04-25 MINOR-1. |
  ```

- [ ] **Step 5.2: Update the Status header**

  Change line 4 from `Status: DESIGN — Phase 1 scoped` to `Status: PHASE 1 IMPLEMENTED 2026-04-25 — see commits 666d275, e463b3e, af6c314, 41bd91c, <new commit>; Phase 2 (auto-triggers) deferred — see §13.`

---

## Task 6: Build, test, lint, commit

- [ ] **Step 6.1: Full build + tests**

  ```bash
  cd C:\Users\Yonatan Levin\Documents\Programming\Projects\FinTech\Strade\midas\.worktrees\feat-observability-narrative
  go build ./...
  go test ./...
  go test -race ./internal/observability/... ./internal/api/middleware/... ./internal/integration/
  go test -cover ./internal/observability/narrate/... ./internal/observability/artifact/... ./internal/api/middleware/...
  ```
  Expected: all green; narrate ≥ 95%, artifact ≥ 90%, middleware ≥ 90%.

- [ ] **Step 6.2: Lint script (if ripgrep is installed)**

  ```bash
  ./scripts/lint-logs.sh   # Linux/macOS
  .\scripts\lint-logs.ps1  # Windows
  ```
  Expected: PASS (zero new findings).

- [ ] **Step 6.3: Commit**

  ```bash
  git add internal/observability/artifact/zap_core.go \
          internal/observability/artifact/zap_core_test.go \
          internal/observability/artifact/bundle.go \
          internal/observability/artifact/bundle_test.go \
          internal/api/middleware/trace.go \
          internal/api/middleware/trace_test.go \
          internal/integration/narrate_artifact_test.go \
          docs/refactoring/observability-narrative-and-artifacts-spec.md \
          docs/superpowers/plans/2026-04-25-bundle-log-streams.md \
          docs/reviewer/G1-growth-blend-weights-coarse.md

  git commit -m "$(cat <<'EOF'
  feat(observability-narrative): commit 5 - 99-*.jsonl streams in bundles + G1 follow-up

  Closes QA finding MINOR-1 from the 2026-04-25 QA pass on this branch.

  Spec §7.1 listed `99-narrate.jsonl` and `99-debug-trace.jsonl` as bundle files,
  but neither was being written. Now:

  - artifact.BundleSink: a zapcore.Core wrapper that forwards entries to the
    underlying core unchanged AND tees event=narrate entries to
    `<bundle>/99-narrate.jsonl` plus Debug-level entries to
    `<bundle>/99-debug-trace.jsonl`.
  - artifact.Bundle: gains `AppendStream` for line-appendable JSONL writes
    with cached file handles, flushed + closed on Bundle.Close.
  - api/middleware/trace: installs the BundleSink wrapper on the
    request-scoped logger after a successful bundle open, so all 17 narrate
    phases land in the bundle's JSONL stream.
  - integration test extended to assert the JSONL file exists with the
    correct line count and request_id correlation.
  - Spec updated: §7.1 / §7.3 marked implemented.
  - docs/reviewer/G1-growth-blend-weights-coarse.md: filed QA finding
    MINOR-2 as a tracked follow-up (growth blend weights are coarse 0.5/0.5
    because growth.Result doesn't expose blend math).

  Coverage: narrate <%>, artifact <%>, middleware <%>.

  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

  Replace `<%>` with the actual numbers from the test run.

- [ ] **Step 6.4: Verify clean tree + commit landed**

  ```bash
  git status
  git log --oneline -6
  ```
  Expected:
  - `nothing to commit, working tree clean`
  - 5 commits ahead of master tip

---

## Acceptance criteria (for the executing agent to confirm)

- [ ] `99-narrate.jsonl` written into bundle when bundle is open and `event=narrate` lines emitted
- [ ] `99-debug-trace.jsonl` written when Debug-level lines emitted
- [ ] All entries in JSONL files share the request_id of the request that opened the bundle
- [ ] Wrapper is transparent to non-bundle requests (no JSONL files anywhere)
- [ ] Existing zap output unaffected (forwarding still works)
- [ ] All tests pass; coverage gates met
- [ ] Spec change-log + status updated to reflect implementation
- [ ] G-1 reviewer note filed for MINOR-2

## Out of scope

- The G-1 follow-up (growth blend weights) is documented in `docs/reviewer/G1-growth-blend-weights-coarse.md` but NOT implemented in this commit. Filed for a future Phase 2-class change.
- Any change to `pkg/finance/*` (v1.1 D7 invariant).
- Any change to the Phase 2 deferred work (auto-on-error, auto-on-quality-flag, always-on, replay tooling).
