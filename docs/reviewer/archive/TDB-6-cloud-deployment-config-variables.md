# TDB-6 — Add cloud deployment configuration variables

**Status:** IMPLEMENTED — `config.env.prod.example` + `docs/operations/deployment-runbook.md` created (2026-06-09; docs + env template only, no code). VERIFIER VERIFIED; QA PASS; REVIEWER APPROVE_WITH_NITS → both MAJOR doc-accuracy defects FIXED (Postgres migration/key-provisioning commands; the deeper finding that `DATABASE_DRIVER=postgres` doesn't boot at all — no PG driver imported → template now defaults `sqlite3` + runbook §4/§12-caveat#5 document the gap). NITs folded (GIN_MODE inert; `dcf_data` operator-created).
**Priority:** P2 — Tier 2 (deployment readiness).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-6]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "Cloud deployment configuration variables" (Phase 2.5.1), confirmed still OPEN (no cloud config found in `scripts/` or `config/`).

**Spec:** `docs/refactoring/spec/tdb-6-cloud-deploy-config-spec.md`.
**Implementer plan:** `docs/refactoring/implementations/tdb-6-cloud-deploy-config-implementation-plan.md`.

---

## DECISION (2026-06-09): Docker Compose production

The TDB-6.0 blocker is resolved. **Target = Docker Compose production** (`docker-compose.prod.yml`).

- **Why:** lowest-friction — the prod compose file (dcf-api ×2 + traefik/TLS + prometheus/grafana
  `monitoring` profile) **already exists and is substantial**; the only gaps are a documented
  prod env-var template and an operations runbook, which TDB-6 now closes. Reversible and
  extensible — the env-var contract frozen in the spec (§4) is the same contract a future
  K8s/Helm `ConfigMap`+`Secret` or managed-cloud task definition would consume; no code is
  platform-specific.
- **Future-target note:** a K8s/Helm or managed-cloud target reuses the §4 env-var contract
  verbatim (non-secret → ConfigMap/task-env; `# SECRET` rows → Secret/secrets-manager). No
  `internal/config/config.go` change needed for any target.
- **Scope is docs + an env template only — no Go/compose/Dockerfile change, no behavior change.**

---

## Context

The staging launch path (`scripts/launch_staging.sh`, now wired for local migrate/seed) and the config tree carry **no cloud-deployment variables**. This was always a placeholder item.

**Blocker:** the scope is meaningless without a target platform. The work differs substantially between Docker Compose prod (`docker-compose.prod.yml` already exists), Kubernetes (needs manifests/Helm), or a specific managed cloud (env-var contract + secrets manager).

## Scope / Tasks

| ID | Task | Effort |
|---|---|---|
| TDB-6.0 | **Decision:** choose the deployment target | — |
| TDB-6.1 | Add the cloud config variables to the appropriate template/config for that target | M |
| TDB-6.2 | Document in a deployment runbook (`docs/operations/`) | S |

## Acceptance
- [x] Target deployment platform chosen (decision recorded) — Docker Compose production, 2026-06-09.
- [x] Cloud config variables added for that target — `config.env.prod.example` (per implementer plan §3.1; ⊇ all 12 compose `${VAR}`, placeholders only).
- [x] Deployment runbook updated — `docs/operations/deployment-runbook.md` (per implementer plan §3.2; all 12 sections, 4 ARCH caveats documented).

## Follow-ups surfaced during design + review (file separately as CODE issues)
- **Postgres driver not imported → `DATABASE_DRIVER=postgres` does NOT boot (HIGH — found by REVIEWER).**
  No `lib/pq`/`pgx` in `go.mod` or blank-imported; `sqlx.Connect("postgres", …)`
  (`internal/di/container.go:427`) fails with `sql: unknown driver "postgres"`. Both `cmd/migrate`
  and `cmd/seed-demo-key` are hardcoded SQLite-only. The compose default of `postgres` is therefore
  non-functional, and SQLite (the only wired driver) can't be safely shared across the compose
  `replicas: 2`. Completing Postgres = import a driver + add Postgres migration/seed tooling +
  (optionally) make the shipped compose SQLite-or-Postgres switchable. Runbook §4 + §12 caveat #5.
- `monitoring/prometheus.yml` + grafana provisioning assets are referenced by the prod compose
  `--profile monitoring` but do NOT exist (spec §8 OQ#1).
- Dockerfile `HEALTHCHECK CMD /app/dcf-api --health-check` invokes a flag `cmd/server/main.go`
  does not implement — latent code defect; the compose-level `wget /health` healthcheck is the
  correct one used in prod (spec §8 OQ#4).
- The shipped `docker-compose.prod.yml` `dcf-api.environment:` block is Postgres-oriented (passes
  `DATABASE_POSTGRES_URL`, not the SQLite path / `RUN_MIGRATIONS`, no data volume) → running the
  working SQLite path needs operator compose edits (runbook §4).
