# M-2 — `string(rune(statusCode))` produces single-glyph status_code labels

**Status:** RESOLVED 2026-04-25.
**Severity:** HIGH (production observability — silently broke every Prometheus query keyed on `status_code`).
**Pre-existing:** Yes. Bug predates the 2026-04-24 reviewer-cleanup sweep but was invisible until PREX-1 (`78fa1b6`) actually registered the metric on a gatherer. Surfaced during the validation cycle of that sweep.

## Symptom

`http_requests_total{method="GET", endpoint="/api/v1/fair-value/:ticker", status_code="È"}` — the `status_code` label is a single Unicode glyph (U+00C8 `È` for HTTP 200, U+0194 `Ɣ` for 404, etc.), not a decimal string. Every dashboard or alert rule comparing `status_code="200"` returned zero matches in production.

## Root cause

`internal/services/metrics/service.go:360` (commit history shows the line was introduced years ago and never reviewed):

```go
s.httpRequestsTotal.WithLabelValues(method, endpoint, string(rune(statusCode))).Inc()
```

`string(rune(200))` does NOT mean "render the integer as decimal." It means "build a 1-rune string whose codepoint is 200" — i.e. the Latin-1 character `È`. The Go vet rule `go vet -strconv` would have caught this; `golangci-lint` with the default `stringintconv` rule would have caught it; neither was wired into CI.

## Why pre-existing tests didn't catch it

- Unit tests for `RecordHTTPRequest` only asserted that the call did not panic.
- The metric was never on the wire — `promauto.Factory{}` (PREX-1) silently dropped the registration. Grafana / Prometheus saw no series at all, so the *label-value shape* was untestable end-to-end.
- After PREX-1 was fixed, the metric reached the wire — and the bad label values became user-visible.

## Fix

Two-line change at `internal/services/metrics/service.go`:

```go
import (
    "runtime"
    "strconv"  // new
    "time"
    ...
)
...
s.httpRequestsTotal.WithLabelValues(method, endpoint, strconv.Itoa(statusCode)).Inc()
```

## Regression test

`TestRecordHTTPRequest_StatusCodeLabel` in `internal/services/metrics/service_test.go` records three HTTP statuses (200, 404, 500), gathers the registry, and asserts each `status_code` label is the literal decimal string `"200"` / `"404"` / `"500"`. Any rune-encoding regression fails the test with the actual mis-encoded value in the diff.

## Tracked when

- Discovered: 2026-04-24, REVIEWER subagent during the post-sweep validation cycle.
- Filed: 2026-04-25 by ORCHESTRATOR, fixed and resolved in same session.
- Owner: BACKEND.

## Resolution commit

(Pending — committed alongside this doc.)
