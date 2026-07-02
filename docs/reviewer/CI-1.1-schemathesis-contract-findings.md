# CI-1.1 — Schemathesis contract findings (API-hardening backlog)

**Status:** OPEN — filed 2026-07-02, spun out of CI-1 (#20) during the CI-green work.
**Severity:** Medium. Real spec-vs-implementation gaps + one input-validation 500. None
block the default CI (the `Contract Fuzzing` workflow is gated off push/PR — see CI-1), but
they must be burned down before schemathesis can return to the push path.
**GitHub issue:** _to be filed (`/github-tracking`)._

---

## Why this exists

CI-1 fixed the reason schemathesis produced **zero** signal: `contract-fuzz.yml` started the
server with `DATABASE_TYPE` / `DATABASE_PATH`, but `internal/config/config.go` reads
`DATABASE_DRIVER` / `DATABASE_SQLITE_PATH` (viper maps `database.driver` / `database.sqlite_path`).
The server never booted, the health check timed out, and schemathesis never ran.

With the env fixed (server boots, demo key seeded via `cmd/migrate`), schemathesis **does** run
and finds genuine contract gaps. These are an API-hardening effort distinct from CI hygiene, so
CI-1 gates the workflow (nightly / `workflow_dispatch` / PR label `contract`) and files the
findings here rather than papering over `--checks all`.

Reproduced locally (schemathesis v4.22.0) against the booted server with the seeded demo key:

```
python -m schemathesis.cli run http://localhost:8080/docs/openapi.yaml \
  --header "X-API-Key: <demo key>" --checks all
```

## Findings

### F1 (highest) — `POST /api/v1/auth/keys` returns 500 on a validation error
A request body with `permissions: []` (empty) yields **HTTP 500**, not 400. Server log:
`handlers/auth.go:65 failed to create API key … error: "invalid input parameters: permissions cannot be empty"`.
An empty/invalid `permissions` array is a client error → should map to **400 Bad Request**
(RFC 7807), not a server error. Also reproduced with exotic-Unicode `user_id`. This is the only
`not_a_server_error` failure (found in ~732 generated cases; the lighter `TestAPIFuzz_*_No5xx`
integration suite misses it because it does not fuzz the auth-keys body space).
- Repro: `curl -X POST -H 'X-API-Key: <key>' -H 'Content-Type: application/json' -d '{"permissions": [], "user_id": "x"}' http://localhost:8080/api/v1/auth/keys`

### F2 — Unsupported methods return 404, expected 405 (6 occurrences)
E.g. `TRACE /version` → 404 instead of `405 Method Not Allowed`. Gin's default is 404 for
unrouted method+path; enabling `HandleMethodNotAllowed` (or documenting the methods) would align
with the spec's implied 405.

### F3 — Response violates schema (2) + API rejected schema-compliant request (2)
Some responses don't match the declared OpenAPI response schema, and some schema-compliant
generated requests are rejected (API validation stricter than the documented schema). Reconcile
`docs/openapi.yaml` with the handler request/response shapes.

### F4 — Undocumented Content-Type (3)
Responses use a Content-Type not declared for the operation in the spec. Add the produced
content types (or normalize the responses).

## Acceptance for closing
- [ ] F1 fixed: empty/invalid `permissions` → 400 (RFC 7807), with a regression test.
- [ ] F2–F4 resolved (405 for unsupported methods; response/request schema reconciled;
      content-types documented) OR each explicitly accepted with a documented `--exclude-checks`.
- [ ] `contract-fuzz` returns to the push path (drop the gate in `.github/workflows/contract-fuzz.yml`)
      and a clean run is green.

## Out of scope
- The CI-green work itself (that is CI-1 / #20 — done). This tracker is purely the API-hardening
  backlog surfaced by finally running schemathesis for real.
