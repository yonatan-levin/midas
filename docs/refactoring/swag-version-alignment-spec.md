# swag CLI / library version alignment

**Status:** Tracked, not blocking. Hotfix applied 2026-04-30 (commit `<TBD-hotfix>`).
**Owner:** unassigned.
**Trigger to act:** see "When to upgrade" below.

---

## Problem

`docs/docs.go`, `docs/swagger.json`, and `docs/swagger.yaml` are auto-generated from Go source annotations by the `swag` CLI (`github.com/swaggo/swag/cmd/swag`). The runtime application also imports the `github.com/swaggo/swag` library to register `SwaggerInfo` at init time and serve the Swagger UI under `/swagger/*`.

The CLI version that emits `docs.go` and the library version pinned in `go.mod` must agree on the `swag.Spec` struct shape. They currently do not:

| Layer | Version | Spec fields it knows about |
|---|---|---|
| `swag` CLI installed locally | v1.16.4 | Includes `LeftDelim` / `RightDelim` (added in v1.16.x) |
| `swag` library pinned in `go.mod` | v1.8.12 | Does **not** include `LeftDelim` / `RightDelim` |

When the CLI regenerated `docs.go` during the `e881d27` `/docs-update` pass, it emitted those two fields. The pinned library couldn't compile them, breaking `go build ./...` with:

```
docs\docs.go:525:2: unknown field LeftDelim in struct literal of type "github.com/swaggo/swag".Spec
docs\docs.go:526:2: unknown field RightDelim in struct literal of type "github.com/swaggo/swag".Spec
```

The runtime server binary kept building because `cmd/server/main.go:22` has the docs import commented out (`// _ "github.com/midas/dcf-valuation-api/docs"`), so the `docs` package was never compiled in normal flows. The break only surfaced under whole-module commands: `go build ./...`, `go vet ./...`, `go test ./...`.

## Hotfix applied (Option A)

Stripped `LeftDelim` and `RightDelim` from `docs/docs.go` lines 525-526, restoring the v1.8.12-compatible struct literal that earlier `swag init` runs had produced. Two-line patch, no dependency churn, build immediately green.

**Caveat:** Any future `swag init` run will re-emit those fields. The next person who regenerates the swagger artifacts will re-introduce the build break. This spec exists so that person knows what to do.

## Future durable fix (Option B)

Bump `github.com/swaggo/swag` in `go.mod` to a version ≥ v1.16 that includes `LeftDelim` / `RightDelim` on the `Spec` struct. After that, the CLI and library agree, and `swag init` produces compilable artifacts directly.

### Steps when ready

1. Read the swag changelog from v1.8.12 → current major to surface any breaking changes (e.g., behavior of `--parseInternal`, default delimiter handling, generated import paths). Particular attention to: the `internal_api_v1_handlers.X` rename behavior we hit during the 2026-04-30 regen, which would land permanently if `--parseInternal` becomes default.
2. Bump:
   ```bash
   go get github.com/swaggo/swag@latest    # or pin a specific version
   go mod tidy
   ```
3. Regenerate:
   ```bash
   swag init -g cmd/server/main.go -o docs/
   ```
4. Verify:
   ```bash
   go build ./...
   go vet ./...
   go test ./... -count=1 -short
   ```
5. **Live verify** the Swagger UI: `cmd/server/main.go:22` has the docs import commented out today; uncomment it temporarily, run the server, hit `/swagger/index.html`, confirm the response schema renders the new `current_price`, `currency`, `adr_ratio_applied`, and FPI 422 error code correctly. Re-comment if the user wants to keep the import out.
6. Inspect the diff carefully — schema definition keys may rename (e.g., `handlers.FairValueResponse` → `internal_api_v1_handlers.FairValueResponse`). If they do, document it in the commit message because any external tooling that referenced the old names will need updating.

### Acceptance criteria

- `go build ./...` exit 0 with the swag library bumped + `docs.go` regenerated
- `swag.Spec` struct literal in `docs.go` includes `LeftDelim`/`RightDelim` cleanly (no need to strip)
- Live `/swagger/index.html` (with the docs import temporarily uncommented) renders the FairValueResponse schema with all current transparency fields

## When to upgrade

Pick whichever fires first:

- **Forced**: Someone runs `swag init` for legitimate reasons (e.g., adds a new endpoint, changes annotations) and re-introduces the build break. Cheapest moment to do the upgrade is right when the break recurs.
- **Pulled by need**: A swag feature added in v1.9-v1.16 (e.g., better generic-type support, OpenAPI 3.x output, new annotation directives) becomes useful. Upgrade is now justified by feature gain rather than tech debt cleanup alone.
- **CI-driven**: If a future CI pipeline runs `go build ./...` or `go vet ./...` whole-module (which today's local-only workflow doesn't), the strip-fix is no longer enough — the upgrade becomes the only durable answer.

If none of those fire for ≥ 6 months, this spec can be marked stale and dropped. The pinned v1.8.12 has been adequate for the project's swagger surface for a long time, and there's no acute pain.

## Why this matters

The break is asymmetric: silent at runtime (the docs import is commented out), loud at module level. That's the worst kind of regression — it masquerades as "everything works" if you only check the running app. The 2026-04-30 verification cycle caught it specifically because `VERIFIER` ran `go build ./...` (whole-module) rather than the targeted package builds the implementation phase used. Lesson reinforces a broader pattern:

> When regenerating tooling artifacts (`swag init`, `protoc`, `mockgen`, `sqlc generate`), the right verification is the broadest one — `go build ./...`, not package-targeted commands. Targeted commands skip the very files you regenerated.

## Related

- `cmd/server/main.go` — line 22 has `// _ "github.com/midas/dcf-valuation-api/docs"` commented out. If swagger UI gets re-enabled, uncomment.
- `docs/docs.go` — current state has `LeftDelim`/`RightDelim` stripped to match v1.8.12 library
- `go.mod` — pins `github.com/swaggo/swag v1.8.12`
