# Midas DCF Valuation API — Production Deployment Runbook

Target: **Docker Compose production** (`docker-compose.prod.yml`). Decision recorded in
`docs/refactoring/spec/tdb-6-cloud-deploy-config-spec.md` §1.

> Cross-references (not duplicated here):
> - Build/run basics: `README.md` §Docker, `CLAUDE.md`.
> - API contract: `docs/API_DOCUMENTATION.md`.
> - Config reference: `internal/config/config.go` `setDefaults()`.
> - Env-var contract (host ⇄ container mapping): `docs/refactoring/spec/tdb-6-cloud-deploy-config-spec.md` §4.

---

## 1. Overview & scope

This runbook deploys the production Docker Compose topology:

- `dcf-api` — the valuation API (compose declares `deploy.replicas: 2` with resource limits
  and a rolling-update + rollback policy; see §8 for the Swarm-vs-standalone caveat).
- `traefik` — reverse proxy terminating TLS via Let's Encrypt (ACME http-01).
- `prometheus` + `grafana` — **optional**, behind `--profile monitoring`.

It does **NOT** cover Kubernetes/Helm or a managed-cloud target. Those are future targets that
reuse the **same** env-var contract (spec §4) — non-secret vars become a `ConfigMap`/task-env,
the `# SECRET` rows become a `Secret`/secrets-manager reference. No `internal/config/config.go`
change is needed for any target.

---

## 2. Prerequisites

- **Docker Engine ≥ 24** and **Compose v2**. Verify:
  ```bash
  docker --version
  docker compose version
  ```
- A deploy host with the production `.env` filled in (see §3).
- **Outbound network** from the host to the data sources:
  `data.sec.gov`, `query2.finance.yahoo.com`, and `api.stlouisfed.org` (FRED, only if enabled).
- A **DNS A-record** pointing the `Host()` domain (compose ships `api.dcf-valuation.com` — you
  MUST edit it to your domain, see §7) at the deploy host.
- **Ports 80 and 443 open** inbound (80 for the ACME http-01 challenge, 443 for TLS traffic).
- For the `--profile monitoring` stack: `./monitoring/prometheus.yml` and
  `./monitoring/grafana/provisioning` must exist (they do NOT ship in the repo today — see §12).

---

## 3. Environment setup

```bash
cp config.env.prod.example .env
chmod 600 .env
# Edit .env: fill every required (REQUIRED) and secret (# SECRET) var.
```

Compose automatically reads `./.env` next to `docker-compose.prod.yml` and interpolates the
`${VAR}` references from it. Each required host var that you leave unset expands to an empty
string **silently** (Compose does not error), which can ship a mis-configured container — so
fill them all.

**Host-var ⇄ container-var mapping.** The compose file deliberately exposes short host names
(e.g. `DATABASE_URL`, `REDIS_URL`, `FRED_ENABLED`) and maps them onto the longer names the app
reads inside the container (`DATABASE_POSTGRES_URL`, `CACHE_REDIS_URL`, `MACRO_FRED_ENABLED`).
Your `.env` defines the **host** names; `docker exec <ctr> env` will show the **container**
names. The full mapping table is in spec §4.

The repo's pre-read hook blocks reading `.env` in-tooling, so the host `.env` stays out of any
agent/tooling context.

---

## 4. Database provisioning

> **READ THIS FIRST — driver reality (verified against the code, 2026-06-09).**
> **SQLite is the only WIRED database driver today.** `DATABASE_DRIVER=postgres` is *accepted* by
> config validation (`config.go:658`) and the compose file defaults to it
> (`DATABASE_DRIVER:-postgres`), **but the app does not import a Postgres `database/sql` driver**
> (`lib/pq`/`pgx` are absent from `go.mod` and not blank-imported anywhere). So `NewDatabase`'s
> `sqlx.Connect("postgres", …)` (`internal/di/container.go:427`) fails at boot with
> `sql: unknown driver "postgres" (forgotten import?)`. **You MUST set `DATABASE_DRIVER=sqlite3`**
> in `.env` (overriding the compose default) for the API to boot. Completing Postgres support
> (import a driver + add Postgres migration/seed tooling) is a **code follow-up** — see §12 caveat #5.

### SQLite (the working driver — `DATABASE_DRIVER=sqlite3`)

```bash
# In .env (override the compose default of postgres):
#   DATABASE_DRIVER=sqlite3
#   RUN_MIGRATIONS=true                 # entrypoint runs cmd/migrate against the SQLite file
# Mount a persisted /app/data volume so the DB survives container restarts (see §10).
```

The container entrypoint auto-migrates on boot when `RUN_MIGRATIONS=true` (it runs
`cmd/migrate -db "$DB_PATH"` against the SQLite path). SQLite requires CGO (the
`mattn/go-sqlite3` driver); the production image already builds with it. Back up the `/app/data`
volume per §10.

> **Required compose edits for the SQLite path.** The shipped `docker-compose.prod.yml`
> `dcf-api.environment:` block is **Postgres-oriented** — it passes `DATABASE_POSTGRES_URL` but
> NOT the SQLite path / `RUN_MIGRATIONS`, and declares no data volume. To run SQLite you must add
> to that block: `DATABASE_SQLITE_PATH=/app/data/midas.db` and `RUN_MIGRATIONS=true`, plus a
> persisted volume (`volumes: ["dcf_data:/app/data"]` on `dcf-api` + a top-level `dcf_data:` named
> volume). Until the Postgres driver follow-up (§12 caveat #5) lands, treat these compose edits as
> a **required deploy step** (this runbook does not modify the shipped compose file).

> **Multi-replica caveat.** SQLite is a single-file, single-writer store — it **cannot** be safely
> shared across the compose `deploy.replicas: 2`. Run a **single** API instance on SQLite (do not
> scale `dcf-api` while on SQLite). A true multi-replica deployment needs a shared Postgres, which
> is blocked on the driver follow-up above. (Standalone `docker compose up` runs one replica
> anyway — see §8.)

### Postgres (config-accepted but NOT functional today)

Do **not** use `DATABASE_DRIVER=postgres` in production yet — the app cannot connect (no driver
imported, above), and there is **no Postgres migration/seed tooling** (`cmd/migrate`,
`cmd/seed-demo-key` are both hardcoded SQLite-only: `sql.Open("sqlite3", …)`, `-db` is a *file
path*, not a DSN). When Postgres support lands (driver import + a Postgres-aware migration path),
this section will document: create the DB/user out of band, point `DATABASE_URL` at it, apply
`internal/infra/database/schema.sql` + `migrations/*.sql` via `psql`, and provision API keys per §9.

---

## 5. Build & launch

```bash
# Core stack (dcf-api + traefik):
docker compose -f docker-compose.prod.yml up -d --build

# With monitoring (needs ./monitoring assets — see §12):
docker compose -f docker-compose.prod.yml --profile monitoring up -d

# Status + logs:
docker compose -f docker-compose.prod.yml ps
docker compose -f docker-compose.prod.yml logs -f dcf-api
```

`VERSION` (from `.env`) drives both the build arg and the image tag
(`image: dcf-valuation-api:${VERSION}`). Bump it for each release (see §8 / §11).

---

## 6. Verify

```bash
# Liveness (the compose healthcheck also wgets /health internally):
curl -fsS https://<your-host>/health

# Prometheus exposition:
curl -fsS https://<your-host>/metrics | head

# End-to-end smoke (key provisioned per §9):
curl -fsS -H "X-API-Key: <key>" https://<your-host>/api/v1/fair-value/AAPL | jq .
```

A healthy boot returns `200` on `/health`. If `/health` flaps or the container restarts, check
`docker compose ... logs -f dcf-api` and §12.

---

## 7. TLS (Traefik + Let's Encrypt)

- `ACME_EMAIL` (from `.env`) drives Let's Encrypt registration and expiry notices — an empty
  value fails ACME registration.
- **Edit the router `Host()` label** in `docker-compose.prod.yml` from the shipped
  `api.dcf-valuation.com` to **your** domain. Traefik routes by this label.
- The `letsencrypt` named volume (`dcf_letsencrypt`) persists `acme.json` across restarts.
- The **http-01** challenge requires port **80** reachable from the public internet; certs are
  served on **443**. Both ports must be open (§2).

---

## 8. Scaling & rollout

The compose file declares `deploy.replicas: 2` and an `update_config` block (parallelism 1,
delay 10s, monitor 60s, `failure_action: rollback`, `max_failure_ratio: 0.3`).

> **Caveat (Swarm-only `deploy:` keys).** `docker compose -f … up` (Compose v2 **standalone**)
> **ignores** `deploy.replicas` and `deploy.update_config` — it honours only a subset of the
> `deploy:` block. For a true 2-replica rolling deploy you have two options:
> 1. **Swarm:** `docker stack deploy -c docker-compose.prod.yml dcf` (the `deploy:` block is
>    honoured natively, including `failure_action: rollback`).
> 2. **Standalone scale:** `docker compose -f docker-compose.prod.yml up -d --scale dcf-api=2`
>    — but `--scale` conflicts with the fixed `container_name: dcf-valuation-api-prod`, so you
>    must remove (or template) `container_name` first.

**Rolling image update:** bump `VERSION` in `.env`, then re-run the launch command (§5). Under
Swarm this triggers the `update_config` rolling strategy; under standalone Compose it recreates
the container(s) in place.

---

## 9. Secrets handling

- **Never commit `.env`.** Supply secrets via a secrets manager or the host `.env` only. The
  repo's pre-read hook blocks reading `.env` in-tooling.
- The secret-bearing **env** vars are: `DATABASE_URL` (DB credentials), `FRED_API_KEY`,
  `GRAFANA_PASSWORD`, `REDIS_URL` (only if Redis auth is used), and `DATACLEANER_AI_SERVICE_URL`
  (only if it embeds a token). The committed template carries placeholders for all of them.

- **API keys are DATABASE-backed — there is NO `API_KEY` env var.** Provision keys into the
  production **SQLite** database (the only wired driver — see §4):
  ```bash
  # First key — seeds a demo key into the SQLite DB and prints DEMO_API_KEY=... to stdout
  # (capture it; only the hash is stored, so it is not retrievable later):
  go run ./cmd/seed-demo-key -db ./data/midas.db

  # hash-key does NOT touch the DB — it only PRINTS the SHA-256 hash of a key to stdout:
  go run ./cmd/hash-key -key <your-key>
  ```
  Both CLIs are **SQLite-only** (`-db` is a file path; `sql.Open("sqlite3", …)`). `seed-demo-key`
  inserts; `hash-key` only computes a hash for you to insert by hand. Run them against the same
  SQLite file the API uses (the `/app/data/midas.db` inside the persisted volume — e.g.
  `docker compose exec dcf-api ./seed-demo-key -db /app/data/midas.db`). Distribute the plaintext
  key to API consumers out of band; the DB stores only the hash.
  > **Postgres key provisioning has no tooling today** (both CLIs are SQLite-only). When Postgres
  > support lands (§4), provisioning will insert the `hash-key` digest into the `api_keys` table
  > via `psql`. Tracked with the Postgres driver follow-up (§12 caveat #5).

---

## 10. Backup & restore

- **Named volumes** to back up: `dcf_letsencrypt`, `dcf_prometheus_data`, `dcf_grafana_data`.
- **SQLite** (the working driver — the `/app/data` volume). NOTE: the shipped
  `docker-compose.prod.yml` does **not** declare a data volume — you add one for the SQLite file
  (e.g. `volumes: [dcf_data:/app/data]` on `dcf-api` + a top-level `dcf_data:` named volume), so
  substitute your actual volume name below:
  ```bash
  docker run --rm -v dcf_data:/d -v "$PWD":/b alpine tar czf /b/data.tgz -C /d .   # backup
  docker run --rm -v dcf_data:/d -v "$PWD":/b alpine tar xzf /b/data.tgz -C /d     # restore
  ```
- **Postgres** (only once the driver follow-up lands — §4; the DB would be external, not a compose
  volume): `pg_dump "$DATABASE_URL" > midas-$(date +%F).sql` / `psql "$DATABASE_URL" < dump.sql`.
- **`acme.json`** restore: drop the file back into the `dcf_letsencrypt` volume and `chmod 600`
  it (Traefik refuses a world-readable `acme.json`).

---

## 11. Rollback

- **Automatic** (Swarm only): `update_config.failure_action: rollback` reverts a failed rolling
  update.
- **Manual:** redeploy the previous image tag —
  ```bash
  VERSION=<previous> docker compose -f docker-compose.prod.yml up -d
  ```
- **Database migrations are forward-only.** A code rollback does NOT roll back schema/data
  changes; restore from a backup (§10) if a migration must be undone.

---

## 12. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Boot fails: `sql: unknown driver "postgres"` | `DATABASE_DRIVER=postgres` but no PG driver is imported | set `DATABASE_DRIVER=sqlite3` (§4 caveat #5). |
| Boot fails: "postgres_url is required" | `DATABASE_DRIVER=postgres` + empty `DATABASE_URL` | switch to `sqlite3` (§4); Postgres isn't wired yet. |
| SEC 403 / no financial data | missing/placeholder `SEC_USER_AGENT` | use a real contact email. |
| TLS never issues | port 80 closed / wrong `Host()` label / bad `ACME_EMAIL` | open 80, fix the label/email (§7). |
| WARN "redis unavailable, using in-memory" | `REDIS_URL` empty/unreachable | optional — set it if you want a shared cache. |
| `--profile monitoring` fails to start | missing `./monitoring/prometheus.yml` + grafana provisioning | author them first (see note below). |
| Healthcheck flapping | slow boot / DB unreachable | check `logs -f dcf-api`; raise the compose `start_period`. |
| Only one container despite `replicas: 2` | Compose standalone ignores `deploy.replicas` | use Swarm or `--scale` (§8). |

### Known caveats / follow-ups (documented, not silently fixed)

1. **Missing `monitoring/` assets.** The `--profile monitoring` stack mounts
   `./monitoring/prometheus.yml` and `./monitoring/grafana/provisioning`, which **do not exist**
   in the repo. Before `--profile monitoring up`, author a minimal `prometheus.yml` (scrape
   `dcf-api:8080/metrics`) and any grafana provisioning you need. Authoring the monitoring config
   is **out of scope** for this runbook — tracked as a follow-up (spec §8 OQ#1).
2. **`deploy:` keys are Swarm-only.** `docker compose up` (standalone) ignores
   `deploy.replicas`/`deploy.update_config`. Use `docker stack deploy` (Swarm) or manual
   `--scale` (§8). Not a code defect — a Compose semantics difference.
3. **`SCHEDULER_ENABLED` double-runs across replicas.** With 2 replicas, enabling the scheduler
   on both double-runs background jobs. Keep `SCHEDULER_ENABLED=false` (the default); run the
   scheduler on a single dedicated instance if you need it. Leader election is out of scope.
4. **Dockerfile `HEALTHCHECK` mismatch (latent).** The Dockerfile's
   `HEALTHCHECK CMD /app/dcf-api --health-check` invokes a flag that `cmd/server/main.go` does
   not implement (it would boot a second server process rather than probe health). Production
   relies on the **compose-level** healthcheck (`wget --spider …/health`), which is correct.
   The Dockerfile `HEALTHCHECK` is flagged as a separate code defect (spec §8 OQ#4) — out of
   scope here.
5. **Postgres driver not imported — `DATABASE_DRIVER=postgres` does NOT boot (verified).** No
   `lib/pq`/`pgx` driver is in `go.mod` or blank-imported, so `sqlx.Connect("postgres", …)`
   (`internal/di/container.go:427`) fails with `sql: unknown driver "postgres"`; and the
   `cmd/migrate`/`cmd/seed-demo-key` CLIs are hardcoded SQLite-only. The compose file's
   `DATABASE_DRIVER:-postgres` default is therefore **non-functional** — set `sqlite3` in `.env`
   (§4). Completing Postgres (import a driver + Postgres migration/seed tooling, and realistically
   the only way to safely run the compose `replicas: 2`) is a **code follow-up** — file it as a
   separate issue. This is the single most important operational caveat in this runbook.
