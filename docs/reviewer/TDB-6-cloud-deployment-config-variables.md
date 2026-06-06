# TDB-6 — Add cloud deployment configuration variables

**Status:** OPEN (BLOCKED ON DECISION) — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P2 — Tier 2 (deployment readiness; under-specified until a target is chosen).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-6]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — catalog "Cloud deployment configuration variables" (Phase 2.5.1), confirmed still OPEN (no cloud config found in `scripts/` or `config/`).

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
- [ ] Target deployment platform chosen (decision recorded)
- [ ] Cloud config variables added for that target
- [ ] Deployment runbook updated
