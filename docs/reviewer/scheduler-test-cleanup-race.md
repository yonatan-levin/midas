# SCHED-1 — `TestSchedulerService_ErrorHandling` race: goroutine logs after `*testing.T` scope

**Status:** OPEN — filed 2026-05-16 by QA gate of Worktree B of DC-1 Phase 0 (master `4612f77`).
**Severity:** MINOR (test-only; production `scheduler.Service.Start` works correctly under normal operation).
**Origin:** QA discovered while running `go test -count=1 ./...` as the load-bearing zero-regression check for DC-1 Phase 0. The scheduler package panics partway through the suite; running `go test ./internal/services/scheduler/` in isolation PASSES.
**Not caused by DC-1.** This is a pre-existing race in the scheduler test cleanup ordering. Filed here so the project keeps a record once the symptom surfaces again.

---

## Symptom

```
panic: Log in goroutine after TestSchedulerService_ErrorHandling has completed: ...
    at internal/services/scheduler/scheduler.go:56
```

Fires only when the FULL suite runs sequentially (`go test ./...`). The scheduler test itself passes when run alone.

## Reproduction

```bash
# Triggers the race (full-suite ordering):
go test -count=1 ./...

# Does NOT trigger (isolated):
go test -count=1 ./internal/services/scheduler/
go test -count=1 -run TestSchedulerService_ErrorHandling ./internal/services/scheduler/
```

The race fires intermittently — observed on Windows during DC-1 Phase 0 QA, may be flaky on other platforms depending on goroutine scheduling.

## Root cause hypothesis

`internal/services/scheduler/scheduler.go:50-56` (the `Start()` function) launches a goroutine that defers a logger call. The test's setup invokes `Start()` and the test's cleanup (`t.Cleanup` or implicit teardown via `*testing.T` going out of scope) does NOT explicitly drain the goroutine before returning.

When the testing harness moves on to the next test/package, the scheduler goroutine wakes up, hits its deferred logger call, and discovers that the `*zap.Logger` it captured belongs to a now-terminated test scope. Go's `testing` package detects the late `Log` call and panics.

Concrete spots to inspect:
- `internal/services/scheduler/scheduler.go:50-56` — the `Start()` goroutine launch + deferred logger.
- Scheduler test file (search for `TestSchedulerService_ErrorHandling`) — the missing `<-stopCh` drain in test cleanup.
- Any `t.Cleanup(func() { svc.Stop() })` registration — is it present, and does `Stop()` wait for the goroutine to exit before returning?

## Proposed fix (options, pick one)

1. **Test-side drain (smallest change):** in the scheduler test setup, register a `t.Cleanup` that calls `svc.Stop()` AND awaits a signal channel confirming the goroutine has exited. The test's lifecycle then strictly contains the goroutine.

2. **Production-side ctx-respecting deferred call:** wrap the deferred logger call in `scheduler.go:50-56` with a context-cancel check:
   ```go
   defer func() {
       select {
       case <-ctx.Done():
           return  // test already torn down; suppress late log
       default:
           logger.Info("scheduler stopped", ...)
       }
   }()
   ```
   Production behavior is unchanged because the context is not cancelled during normal shutdown.

3. **Inject a fresh logger per goroutine + drop the zap-shared one** — most invasive; only if (1) and (2) introduce other bugs.

Option (1) is the lowest-blast-radius fix and matches Go testing idiom (tests own their goroutine lifetimes).

## Out of scope

- This is **not a DC-1 concern.** The scheduler package is orthogonal to the cleaner refactor. Filed here only because DC-1 Phase 0 QA was the discovery vector.
- Not a Tier 2 concern either. Scheduler is a side-track.
- Track and fix on a dedicated scheduler-cleanup branch; do not bundle with cleaner or valuation work.

## Acceptance for closing

- [ ] Reproduction script in a CI job (`go test -count=N ./...` for some N high enough to surface the race deterministically).
- [ ] Fix lands per one of the three options above.
- [ ] Repro script becomes green across 100 sequential runs.
- [ ] CLAUDE.md "Common Gotchas" or `agents/rules/` updated if the fix changes scheduler test conventions.
