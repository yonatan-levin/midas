# TDB-6 — Cloud Deployment Config: Implementer Plan

**Spec:** `docs/refactoring/spec/tdb-6-cloud-deploy-config-spec.md`.
**Tracker:** `docs/reviewer/archive/TDB-6-cloud-deployment-config-variables.md`.
**Issue:** TDB-6 / GitHub #6.
**Nature:** Docs + env-template ONLY. No Go code, no compose change, no Dockerfile change.
**Decision:** Docker Compose production (recorded in spec §1).

---

## 1. Files to create / edit

| Action | Path | Notes |
|---|---|---|
| CREATE | `config.env.prod.example` | Production host-`.env` template. Placeholders only. ⊇ all compose `${VAR}`. |
| CREATE | `docs/operations/deployment-runbook.md` | New dir `docs/operations/`. Operator runbook. |
| EDIT | `docs/reviewer/archive/TDB-6-cloud-deployment-config-variables.md` | Record decision; link spec+plan; check "target chosen"; advance Status. |
| (already created) | `docs/refactoring/spec/tdb-6-cloud-deploy-config-spec.md` | The spec. |
| (this file) | `docs/refactoring/implementations/tdb-6-cloud-deploy-config-implementation-plan.md` | This plan. |

No other files. Specifically: do **not** touch `internal/config/config.go`,
`docker-compose.prod.yml`, `Dockerfile`, `docker-entrypoint.sh`, or any `.go`/test/migration.

---

## 2. Pre-flight (read-only)

1. Confirm the compose `${VAR}` set from `docker-compose.prod.yml` (the 12 host vars in
   spec §4.1). This is the must-cover set.
2. Confirm the config env-var contract from `internal/config/config.go` `setDefaults()` +
   `mapstructure` tags (spec §4.2/§4.3). Do not re-read `config.env.example` (blocked by the
   pre-read hook).
3. Note: API-key auth is DB-backed (no `API_KEY` env var) — `cmd/seed-demo-key/main.go`,
   `cmd/hash-key/main.go`.

---

## 3. Exact content

### 3.1 `config.env.prod.example`

Flat `KEY=value` host-`.env`. Use this literal content (placeholders only):

```dotenv
# =============================================================================
# Midas DCF Valuation API — PRODUCTION environment template
# =============================================================================
# Target: Docker Compose production (docker-compose.prod.yml). See the runbook:
#   docs/operations/deployment-runbook.md
#
# HOW THIS IS CONSUMED:
#   Copy this file to `.env` NEXT TO docker-compose.prod.yml on the deploy host:
#     cp config.env.prod.example .env  &&  chmod 600 .env
#   Compose interpolates ${VAR} in docker-compose.prod.yml from this `.env`.
#
# RULES:
#   * PLACEHOLDERS ONLY in this committed template — NEVER put a real secret here.
#   * Lines marked "# SECRET" must be supplied via a secrets manager or the host
#     .env at deploy time, and the host .env must NEVER be committed.
#   * An UNDEFINED ${VAR} expands to "" silently at deploy — set every required (✔)
#     var below or risk a mis-configured container.
#
# NAME SPACES: this file uses the HOST var names the compose file interpolates
#   (e.g. DATABASE_URL). The app inside the container reads a different name
#   (e.g. DATABASE_POSTGRES_URL); the compose file does the mapping. See the
#   env-var contract table in docs/refactoring/spec/tdb-6-cloud-deploy-config-spec.md §4.
# =============================================================================

# ===== Application =====
# These are pinned to literals in docker-compose.prod.yml (ENVIRONMENT=production,
# LOG_LEVEL=info, PORT=8080, GIN_MODE=release). Uncomment to override the pinned value.
#ENVIRONMENT=production          # config default: development. Drives prod logging defaults.
#LOG_LEVEL=info                  # config default: debug. Legacy level field.
#PORT=8080                       # config default: 8080. App listen port.
#SCHEDULER_ENABLED=false         # config default: false. Set true on ONE instance only (replicas double-run).
#ENABLE_SWAGGER=false            # config default: false. Keep false in prod (no public API explorer).
#ENABLE_PPROF=false              # config default: false. Keep false in prod (profiling/DoS surface).

# ===== Database =====
DATABASE_DRIVER=postgres                 # compose default: postgres. Must be 'postgres' or 'sqlite3'. (optional)
# SECRET — supply via secrets manager / host .env, never commit
DATABASE_URL=postgres://user:__password__@host:5432/midas?sslmode=require   # REQUIRED when driver=postgres. (✔)
#DATABASE_MAX_OPEN_CONN=50       # pinned to 50 in compose (config default 25).
#DATABASE_MAX_IDLE_CONN=10       # pinned to 10 in compose (config default 10).

# ===== Cache (Redis — OPTIONAL; empty => in-memory fallback) =====
# SECRET — supply via secrets manager / host .env if Redis requires auth, never commit
REDIS_URL=redis://redis-host:6379        # optional. Use rediss://:__password__@host:6379 for TLS+auth. (○)

# ===== SEC EDGAR =====
SEC_USER_AGENT="Your Company Name admin@yourdomain.com"   # REQUIRED. Real contact email or SEC returns 403. (✔)
#SEC_RATE_LIMIT=10               # pinned to 10 in compose (SEC cap; do NOT raise).

# ===== Macro data (FRED) =====
FRED_ENABLED=false                       # compose default: false. true => live FRED macro data. (○)
# SECRET — supply via secrets manager / host .env, never commit
FRED_API_KEY=__set_in_secrets_manager__  # REQUIRED only when FRED_ENABLED=true. (✔-if-FRED)
MANUAL_RISK_FREE_RATE=0.045              # fallback risk-free rate (4.5%) when FRED disabled. (○)
MANUAL_MARKET_RISK_PREMIUM=0.05          # fallback equity risk premium (5%) when FRED disabled. (○)
#MACRO_FRED_BASE_URL=https://api.stlouisfed.org/fred   # rarely overridden.

# ===== DataCleaner =====
# All pinned to literals in compose (DATACLEANER_ENABLED=true, rules paths at /app/config/...,
# MIN_QUALITY_SCORE=60.0, ENABLE_CACHING=true, CACHE_TTL=6h). Override hooks below.
#DATACLEANER_ENABLE_AI_INTEGRATION=false # config default: false. Keep false unless an AI service is wired.
# SECRET — only if the AI service URL embeds a token; supply via secrets manager
#DATACLEANER_AI_SERVICE_URL=             # config default: "". Only with AI integration on.

# ===== Observability =====
# Prod opts INTO the artifact store, flushing bundles only on 5xx (postmortem capture).
# Both pinned to literals in compose; override hooks below.
#LOGGING_LEVEL=info                       # prod default: info. Set debug only for transient diagnosis.
#LOGGING_ARTIFACT_STORE_ROOT_PATH=./artifacts   # if on, point at a sized, mounted volume (compose mounts none today).
#LOGGING_ARTIFACT_STORE_MAX_TOTAL_BYTES=5368709120   # 5 GiB cap on postmortem bundle disk.

# ===== Auth / Secrets =====
# NOTE: Midas API-key auth is DATABASE-backed — there is NO API_KEY env var.
# Provision keys with cmd/seed-demo-key (first key) or cmd/hash-key (insert an
# externally-generated key). See the runbook §"Secrets handling".

# ===== TLS (Traefik + Let's Encrypt) =====
ACME_EMAIL=admin@yourdomain.com          # REQUIRED. Let's Encrypt registration/expiry email. (✔)
# NOTE: edit the Host(`api.dcf-valuation.com`) label in docker-compose.prod.yml to YOUR domain.

# ===== Monitoring (Grafana — only with `--profile monitoring`) =====
# SECRET — supply via secrets manager / host .env, never commit
GRAFANA_PASSWORD=__set_in_secrets_manager__   # REQUIRED only with the monitoring profile. (✔-if-monitoring)

# ===== Build =====
BUILD_DATE=2026-06-09            # compose default: now. ISO date stamped into the image label. (○)
VERSION=v0.9.0-rc1               # compose default: latest. Release tag; drives the image tag. (○)
```

> Implementer: keep the comments terse and the secret annotations exactly on the line above
> each 🔒 var so the post-edit secret-scan context is unambiguous. The values above are
> deliberately placeholders — do not substitute anything real.

### 3.2 `docs/operations/deployment-runbook.md`

Author the 12 sections from spec §6. Content skeleton (fill with real commands):

````markdown
# Midas DCF Valuation API — Production Deployment Runbook

Target: **Docker Compose production** (`docker-compose.prod.yml`). Decision recorded in
`docs/refactoring/spec/tdb-6-cloud-deploy-config-spec.md` §1.

> Cross-references (not duplicated here):
> - Build/run basics: `README.md` §Docker, `CLAUDE.md`.
> - API contract: `docs/API_DOCUMENTATION.md`.
> - Config reference: `internal/config/config.go` `setDefaults()`.
> - Env-var contract: spec §4.

## 1. Overview & scope
What this deploys (dcf-api ×2 + traefik + optional prometheus/grafana). What it does NOT
(no K8s/Helm — a future target reuses the same env-var contract).

## 2. Prerequisites
- Docker Engine ≥ 24, Compose v2: `docker compose version`.
- A deploy host with the prod `.env` (see §3).
- Outbound network to data.sec.gov, query2.finance.yahoo.com, api.stlouisfed.org.
- DNS A-record pointing the `Host()` domain at the host; ports 80 + 443 open (ACME + TLS).

## 3. Environment setup
```bash
cp config.env.prod.example .env
chmod 600 .env
# Edit .env: fill every required (✔) and secret (🔒) var.
```
Host-var ⇄ container-var mapping note (spec §4). The pre-read hook blocks reading `.env`.

## 4. Database provisioning
**Postgres (default):**
```bash
# Create DB + user out of band, then migrate the schema against DATABASE_URL:
go run ./cmd/migrate -db "$DATABASE_URL"   # or run the migrate binary inside a one-off container
```
Note: the container entrypoint's `RUN_MIGRATIONS` path is **SQLite-only** and does NOT
migrate Postgres. **SQLite (alt):** set `DATABASE_DRIVER=sqlite3`, `RUN_MIGRATIONS=true`,
and mount a persisted `/app/data` volume.

## 5. Build & launch
```bash
docker compose -f docker-compose.prod.yml up -d --build
# With monitoring (needs ./monitoring assets — see §12 troubleshooting):
docker compose -f docker-compose.prod.yml --profile monitoring up -d
docker compose -f docker-compose.prod.yml ps
docker compose -f docker-compose.prod.yml logs -f dcf-api
```

## 6. Verify
```bash
curl -fsS https://<your-host>/health           # compose healthcheck also wgets /health
curl -fsS https://<your-host>/metrics | head   # Prometheus exposition
curl -fsS -H "X-API-Key: <key>" https://<your-host>/api/v1/fair-value/AAPL | jq .
```

## 7. TLS (Traefik + Let's Encrypt)
`ACME_EMAIL` drives registration. Edit the `Host()` router label in the compose file to your
domain. The `letsencrypt` named volume persists `acme.json`. http-01 challenge needs port 80
reachable.

## 8. Scaling & rollout
The `deploy.replicas: 2` + `update_config` (parallelism 1, monitor 60s,
`failure_action: rollback`). **Caveat:** `docker compose up` (standalone) ignores
`replicas`/`update_config` — use `docker stack deploy` (Swarm) for true rolling 2-replica,
or `--scale dcf-api=2` (remove `container_name` first). Rolling update: bump `VERSION`, re-run
`up -d`.

## 9. Secrets handling
- Never commit `.env`; secrets-manager / host-`.env` only; pre-read hook blocks `.env`.
- **API keys are DB-backed (no env var):**
  ```bash
  go run ./cmd/seed-demo-key -db <db>         # first key (prints DEMO_API_KEY=...)
  go run ./cmd/hash-key -key <your-key>       # hash an externally-generated key to insert
  ```

## 10. Backup & restore
- Named volumes: `dcf_letsencrypt`, `dcf_prometheus_data`, `dcf_grafana_data`.
- Postgres: `pg_dump` / `pg_restore` against `DATABASE_URL` (DB is external, not a volume).
- SQLite: snapshot the `/app/data` volume (`docker run --rm -v dcf_data:/d -v $PWD:/b alpine tar czf /b/data.tgz -C /d .`).
- `acme.json` restore: drop into the `dcf_letsencrypt` volume (chmod 600).

## 11. Rollback
- Automatic: `update_config.failure_action: rollback` (Swarm).
- Manual: `VERSION=<previous> docker compose -f docker-compose.prod.yml up -d`.
- DB migrations are forward-only — roll back data by restoring a backup (§10).

## 12. Troubleshooting
| Symptom | Likely cause | Fix |
|---|---|---|
| Boot fails: "postgres_url is required" | empty `DATABASE_URL` | set it in `.env` (✔). |
| SEC 403 / no financial data | missing/placeholder `SEC_USER_AGENT` | real contact email. |
| TLS never issues | port 80 closed / wrong `Host()` / bad `ACME_EMAIL` | open 80, fix label/email. |
| WARN "redis unavailable, using in-memory" | `REDIS_URL` empty/unreachable | optional — set if you want shared cache. |
| `--profile monitoring` fails | missing `./monitoring/prometheus.yml` + grafana provisioning | author them first (see spec §8 OQ#1). |
| Healthcheck flapping | slow boot / DB unreachable | check `logs -f dcf-api`, raise `start_period`. |
````

---

## 4. Validation

1. **Template ⊇ compose `${VAR}` set** — grep the compose file for `${...}` and confirm each
   host var appears in the template:
   ```bash
   # all ${VAR} the compose file expands:
   grep -oE '\$\{[A-Z_]+' docker-compose.prod.yml | sort -u
   # confirm each is a key in the template:
   grep -oE '^[A-Z_]+=' config.env.prod.example | sort -u
   ```
   Every compose host var (`BUILD_DATE`, `VERSION`, `DATABASE_DRIVER`, `DATABASE_URL`,
   `REDIS_URL`, `SEC_USER_AGENT`, `FRED_ENABLED`, `FRED_API_KEY`, `MANUAL_RISK_FREE_RATE`,
   `MANUAL_MARKET_RISK_PREMIUM`, `ACME_EMAIL`, `GRAFANA_PASSWORD`) MUST be present
   (uncommented for the host-supplied set).
2. **No real secrets** — visually confirm only `__…__` / `Your Company …` placeholders; the
   post-edit hook secret-scan must pass clean.
3. **Sanity build** — `go build ./...` exits 0 (nothing changed; pure safety net).
4. **Docs render** — verify Markdown tables/fences render (no broken code blocks); links
   resolve to real paths.
5. **Tracker** — confirm the decision + checked box + Status are present.

---

## 5. Commit template

```text
docs(deploy): TDB-6 production env template + deployment runbook (#6)

Records the deployment-target decision (Docker Compose production) and closes the
TDB-6 gap: no documented prod env-var template, no operations runbook.

- add config.env.prod.example — host .env template; ⊇ every ${VAR} the prod
  compose file interpolates; placeholders only, secret rows annotated.
- add docs/operations/deployment-runbook.md — operator-runnable Compose-prod
  runbook (prereqs → env → DB → build/launch → verify → TLS → scale → secrets →
  backup/restore → rollback → troubleshooting).
- spec + implementer plan under docs/refactoring/{spec,implementations}/.
- tracker: decision recorded, "target chosen" acceptance checked.

No code / compose / Dockerfile change; no behavior change ⇒ all invariants
unaffected. No secret committed (placeholders only).

Refs #6 (TDB-6).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

---

## 6. Out of scope (follow-ups to file separately)

- `monitoring/prometheus.yml` + grafana provisioning assets (spec §8 OQ#1).
- Dockerfile `HEALTHCHECK --health-check` flag mismatch (spec §8 OQ#4 — code defect).
- K8s/Helm or managed-cloud manifests (future target; reuse this env-var contract).
