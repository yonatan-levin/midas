# TDB-6 — Cloud Deployment Configuration Spec

**Issue:** TDB-6 / GitHub #6 — "Add cloud deployment configuration variables".
**Type:** Deployment-config + docs (NO code change).
**Status:** SPEC — ready for implementation.
**Author role:** ARCH.
**Tracker:** `docs/reviewer/TDB-6-cloud-deployment-config-variables.md`.
**Implementer plan:** `docs/refactoring/implementations/tdb-6-cloud-deploy-config-implementation-plan.md`.

---

## 1. Recorded decision

The ticket was filed **BLOCKED ON DECISION** because "add cloud deployment config" is
meaningless without a target platform (Docker Compose prod vs Kubernetes/Helm vs a
specific managed cloud all imply different artifacts).

> **DECISION (2026-06-09): the deployment target is Docker Compose production.**

### Rationale

- **Lowest friction.** `docker-compose.prod.yml` **already exists and is substantial** —
  it defines `dcf-api` (2 replicas, resource limits, rolling-update + rollback), `traefik`
  (TLS via Let's Encrypt), and a `monitoring` profile (`prometheus` + `grafana`). The ONLY
  missing pieces are (a) a documented production env-var template and (b) an operator
  runbook. TDB-6 closes exactly those two gaps.
- **Reversible.** Choosing Compose now does not foreclose anything: the env-var contract
  this spec freezes is the same contract a future K8s `ConfigMap`/`Secret`, a Helm
  `values.yaml`, or a managed-cloud task definition would consume. No code or config knob
  is platform-specific.
- **Extensible.** A future Kubernetes/Helm or managed-cloud target builds **on top of the
  same env-var contract** (§4). When that target is chosen, the work is "translate the §4
  table into a `ConfigMap` + `Secret` (or task-definition env block)" — the contract itself
  is already validated against the running app's `config.go`.

### Future-target note (for the record)

A future K8s/Helm or managed-cloud deployment SHOULD reuse the §4 env-var contract verbatim:
- non-secret vars → `ConfigMap` / task-def `environment`;
- secret vars (the `# SECRET` rows) → `Secret` / secrets-manager references, never inlined;
- the `${VAR}` host-`.env` interpolation that Compose performs becomes the orchestrator's
  native variable injection.
No change to `internal/config/config.go` is required for any of these targets — the app reads
env vars through Viper's `AutomaticEnv` + `.`→`_` key replacer regardless of who sets them.

---

## 2. Problem statement (the concrete gap)

`docker-compose.prod.yml` interpolates a set of host `${VAR}` values from the operator's
`.env` file (Compose reads `./.env` next to the compose file automatically). **There is no
template documenting these host vars, and no `docs/operations/` runbook.** An operator
cloning the repo today has to reverse-engineer the required environment from the compose
file and `config.go`. A missing host var silently expands to an empty string at deploy time
(Compose does not error on an undefined `${VAR}` — it substitutes `""`), which can ship a
mis-configured production container (e.g. empty `DATABASE_POSTGRES_URL` → Postgres-driver
validation failure at boot, or empty `SEC_USER_AGENT` → SEC EDGAR 403s).

**TDB-6 deliverables:**
1. `config.env.prod.example` — a production env-var template (host `.env` shape), placeholders only.
2. `docs/operations/deployment-runbook.md` — an operator-runnable runbook (new directory).

---

## 3. Scope & constraints

### In scope
- A production env-var template enumerating every host `${VAR}` the prod compose file
  expands **plus** every `config.go` env var a prod deploy should set/override, grouped and
  annotated.
- An operator deployment runbook covering prerequisites → env setup → build/launch → verify
  → TLS → scaling/rollout → secrets → backup/restore → rollback → troubleshooting.
- Updating the TDB-6 tracker with the decision + acceptance check.

### Out of scope (NON-goals)
- **No Go code change. No behavior change.** This is a docs + env-template change only.
  Therefore every load-bearing invariant (DDM bit-for-bit, recompute-shadow byte-identity,
  ledger-basket, CalcVersion 4.7, SchemaVersion 9) is **trivially unaffected** — no source,
  test, migration, or runtime config default is touched.
- **No real secrets.** The template ships placeholders ONLY; secret rows are annotated
  `# SECRET — supply via secrets manager / host .env, never commit`. (The post-edit hook
  scans edited files for secret patterns; the template must contain none.)
- No new compose service, no Dockerfile change, no Helm/K8s manifest (future targets).
- No creation of the referenced-but-absent `monitoring/prometheus.yml` /
  `monitoring/grafana/provisioning` assets (tracked as an OPEN QUESTION in §8 — the
  `--profile monitoring` stack needs them, but authoring monitoring config is a separate
  artifact; the runbook documents the requirement and the operator action).

### Doc-style constraints
- Match the existing `docs/` style (the `docs/refactoring/spec/*` and `docs/operations`-style
  prose: H2 sections, fenced command blocks, tables for contracts).
- **Cross-reference, don't duplicate.** `docs/API_DOCUMENTATION.md` §"Deployment" already
  points at `docker-compose*.yml` / `scripts/` / `Dockerfile`; the README §"Docker" already
  shows `docker-compose -f docker-compose.prod.yml up -d`. The runbook is the **new canonical
  operational doc** and links back to these rather than restating the API contract or config
  reference.

---

## 4. The complete env-var contract

This table is the **source of truth** for the template. It was derived by cross-checking
**every** `${VAR}` the prod compose file expands against **every** env-var-mapped setting in
`internal/config/config.go` (`setDefaults()` + the `mapstructure` tags; Viper maps nested
keys `a.b.c` → `A_B_C`).

> **Two name spaces.** The prod compose file reads **host** `${VAR}` names (e.g. `DATABASE_URL`,
> `REDIS_URL`, `FRED_ENABLED`, `MANUAL_RISK_FREE_RATE`) and maps them onto the **container**
> env var the app actually reads (e.g. `DATABASE_POSTGRES_URL`, `CACHE_REDIS_URL`,
> `MACRO_FRED_ENABLED`, `MACRO_MANUAL_RISK_FREE_RATE`). The template `config.env.prod.example`
> uses the **host** names (the ones the operator's `.env` must define), because that is the
> file Compose interpolates. The "Container var (config.go)" column records the app-side name
> for traceability.

Legend — **Req?**: ✔ required for a healthy prod boot, ○ optional/has-safe-default.
**Secret?**: 🔒 must come from a secrets manager / host `.env`, never committed.

### 4.1 Host vars the prod compose file interpolates (the must-document set)

> **CORRECTION (REVIEWER, 2026-06-09) — driver reality.** Although the compose default is
> `DATABASE_DRIVER=postgres`, **Postgres does not actually work today**: no `lib/pq`/`pgx`
> driver is in `go.mod` or blank-imported, so `sqlx.Connect("postgres", …)`
> (`internal/di/container.go:427`) fails at boot with `sql: unknown driver "postgres"`; and
> `cmd/migrate`/`cmd/seed-demo-key` are hardcoded SQLite-only. **`sqlite3` is the only wired
> driver** — the template ships `DATABASE_DRIVER=sqlite3` and the runbook (§4 + §12 caveat #5)
> documents the Postgres gap as a code follow-up. The `DATABASE_DRIVER` / `DATABASE_URL` rows
> below describe the compose *contract*; the operator override is `sqlite3`.

| Host `${VAR}` (template) | Group | Container var (config.go) | Compose default | Req? | Secret? | Prod placeholder / note |
|---|---|---|---|---|---|---|
| `BUILD_DATE` | Build | (build arg `BUILD_DATE`) | `now` | ○ | | ISO date stamped into the image label, e.g. `2026-06-09`. |
| `VERSION` | Build | (build arg + image tag `VERSION`) | `latest` | ○ | | Release tag, e.g. `v0.9.0-rc1`. Drives `image: dcf-valuation-api:${VERSION}`. |
| `DATABASE_DRIVER` | Database | `database.driver` | `postgres` | ○ | | `postgres` (compose default) or `sqlite3`. Must be one of those two (config `validate()`). |
| `DATABASE_URL` | Database | `database.postgres_url` | (none) | ✔ (postgres) | 🔒 | `postgres://user:__password__@host:5432/midas?sslmode=require`. **Required when driver=postgres** (config `validate()` rejects empty). |
| `REDIS_URL` | Cache | `cache.redis_url` | (none → app default `redis://localhost:6379`) | ○ | 🔒-if-auth | `redis://redis-host:6379` or `rediss://:__password__@host:6379`. Empty ⇒ in-memory fallback (Redis is optional). |
| `SEC_USER_AGENT` | SEC | `sec.user_agent` | (none → app default `Midas DCF API admin@example.com`) | ✔ | | `"Your Company Name admin@yourdomain.com"`. SEC EDGAR **requires** a real contact email or returns 403. |
| `FRED_ENABLED` | Macro (FRED) | `macro.fred_enabled` | `false` | ○ | | `true` to use live FRED macro data; `false` uses manual rates below. |
| `FRED_API_KEY` | Macro (FRED) | `macro.fred_api_key` | (none) | ✔-if-FRED | 🔒 | `__set_in_secrets_manager__`. Required only when `FRED_ENABLED=true`. |
| `MANUAL_RISK_FREE_RATE` | Macro (FRED) | `macro.manual_risk_free_rate` | `0.045` | ○ | | Fallback 10y-Treasury proxy used when FRED disabled. `0.045` = 4.5%. |
| `MANUAL_MARKET_RISK_PREMIUM` | Macro (FRED) | `macro.manual_market_risk_premium` | `0.05` | ○ | | Fallback equity risk premium. `0.05` = 5%. |
| `ACME_EMAIL` | TLS (Traefik) | (Traefik flag, not app) | (none) | ✔ | | `admin@yourdomain.com`. Let's Encrypt registration/expiry email; empty ⇒ ACME registration fails. |
| `GRAFANA_PASSWORD` | Monitoring (Grafana) | (Grafana env, not app) | (none) | ✔-if-monitoring | 🔒 | `__set_in_secrets_manager__`. Grafana admin password; only needed with `--profile monitoring`. |

### 4.2 Container vars the prod compose file pins to literals (document for override awareness)

These are set to fixed values directly in the compose `environment:` block (no `${VAR}`),
so they need **no host var** — but the template documents them (commented, optional override
hooks) so operators understand the production posture and can lift them to host vars later.

| Container var (config.go) | Group | Compose literal | config.go default | Note |
|---|---|---|---|---|
| `ENVIRONMENT` | Application | `production` | `development` | Drives logging env defaults (json format, file sink off, info level). |
| `LOG_LEVEL` | Application | `info` | `debug` | Legacy level field; `LOGGING_LEVEL` overrides. |
| `PORT` | Application | `8080` | `8080` | App listen port (also `SERVER_PORT`). |
| `GIN_MODE` | Application | `release` | (Gin, not Viper) | `release` disables Gin debug logging. |
| `DATABASE_MAX_OPEN_CONN` | Database | `50` | `25` | Pool size raised for prod. |
| `DATABASE_MAX_IDLE_CONN` | Database | `10` | `10` | |
| `SEC_RATE_LIMIT` | SEC | `10` | `10` | SEC EDGAR cap (10 req/s). Do not raise. |
| `DATACLEANER_ENABLED` | DataCleaner | `true` | `true` | |
| `DATACLEANER_RULES_PATH` | DataCleaner | `/app/config/datacleaner/rules.json` | `./config/...` | Container-absolute path (config mounted at `/app/config`). |
| `DATACLEANER_INDUSTRY_RULES_PATH` | DataCleaner | `/app/config/datacleaner/industry` | `./config/...` | |
| `DATACLEANER_MIN_QUALITY_SCORE` | DataCleaner | `60.0` | `60.0` | |
| `DATACLEANER_ENABLE_CACHING` | DataCleaner | `true` | `true` | |
| `DATACLEANER_CACHE_TTL` | DataCleaner | `6h` | `6h` | |
| `LOGGING_ARTIFACT_STORE_ENABLED` | Observability | `true` | `false` (staging/prod) | Prod opts INTO the artifact store… |
| `LOGGING_ARTIFACT_STORE_TRIGGERS_ON_ERROR` | Observability | `true` | `false` | …only flushing bundles on 5xx (postmortem capture). |

### 4.3 Additional config vars a prod deploy SHOULD consider (not in compose today)

These are env-var-mapped settings from `config.go` that a production operator may want to
set/override but which the current compose file leaves at default. The template lists them
as **commented optional** lines so operators can opt in without re-reading `config.go`.

| Container var (config.go) | Group | config.go default | Why it matters in prod |
|---|---|---|---|
| `ENABLE_SWAGGER` | Auth/Secrets-adjacent | `false` | Keep **false** in prod (no interactive API explorer on a public surface). |
| `ENABLE_PPROF` | Auth/Secrets-adjacent | `false` | Keep **false** in prod (pprof endpoints are a profiling/DoS surface). |
| `SCHEDULER_ENABLED` | Application | `false` | Set `true` only if this instance owns the background watchlist scheduler (avoid double-run across the 2 replicas — see §8 OPEN QUESTION). |
| `DATACLEANER_ENABLE_AI_INTEGRATION` | DataCleaner | `false` | Keep **false** unless an AI footnote service (`DATACLEANER_AI_SERVICE_URL`) is wired; on ⇒ external calls per request. |
| `DATACLEANER_AI_SERVICE_URL` | DataCleaner | `""` | Only when AI integration is on. May carry an internal URL/token → treat as 🔒. |
| `LOGGING_LEVEL` | Observability | `info` (prod) | Override to `debug` only for transient diagnosis. |
| `LOGGING_ARTIFACT_STORE_ROOT_PATH` | Observability | `./artifacts` | If artifact store is on, point at a sized, mounted volume (the compose service does NOT mount one today — see §8). |
| `LOGGING_ARTIFACT_STORE_MAX_TOTAL_BYTES` | Observability | `5 GiB` | Cap the postmortem bundle disk footprint. |
| `MACRO_FRED_BASE_URL` | Macro (FRED) | `https://api.stlouisfed.org/fred` | Rarely overridden; document for completeness. |
| `VALUATION_GUIDANCE_ROOT` | Application | `""` (disabled) | **Leave empty in prod** — Layer-B guidance is fixture-only (NF1: empty ⇒ byte-identical to the 4.7 engine). |

> **Auth/secrets posture (important — no env var for API keys).** Midas API-key auth is
> **database-backed**, not env-var-backed. Keys are provisioned into the DB via
> `cmd/seed-demo-key` / `cmd/hash-key` (see `cmd/seed-demo-key/main.go`). There is therefore
> **no `API_KEY` env var** to put in the template. The runbook's "secrets handling" section
> documents the real key-provisioning path (seed/hash CLIs against the prod DB). The
> secret-bearing **env** vars are limited to: `DATABASE_URL` (DB credentials), `FRED_API_KEY`,
> `GRAFANA_PASSWORD`, `REDIS_URL` (if Redis auth used), and `DATACLEANER_AI_SERVICE_URL` (if
> it embeds a token).

---

## 5. Template design — `config.env.prod.example`

A flat `KEY=value` host-`.env` file (the file Compose interpolates), structured to mirror §4:

- **Header comment block:** what the file is, how Compose consumes it (copy to `.env` next to
  `docker-compose.prod.yml`), the placeholders-only / never-commit rule, and a pointer to the
  runbook.
- **Grouped sections** with `# ===== <GROUP> =====` banners, in this order:
  Application → Database → Cache → SEC → Macro (FRED) → DataCleaner → Observability →
  Auth/Secrets → TLS (Traefik) → Monitoring (Grafana) → Build.
- **Every host `${VAR}` from §4.1 is present** (template ⊇ compose `${VAR}` set — the
  no-silent-empty-env guarantee). §4.2 literals appear as commented "pinned in compose;
  uncomment to override" lines; §4.3 vars appear as commented "optional" lines.
- **Each line carries a one-line comment**: what it is + the config.go default (if any) +
  required-vs-optional.
- **Placeholders only.** Examples: `SEC_USER_AGENT="Your Company contact@example.com"`,
  `FRED_API_KEY=__set_in_secrets_manager__`,
  `DATABASE_URL=postgres://user:__password__@host:5432/midas?sslmode=require`,
  `GRAFANA_PASSWORD=__set_in_secrets_manager__`.
- **Secret rows annotated:** `# SECRET — supply via secrets manager / host .env, never commit`
  immediately above each 🔒 var.

The full literal content is specified in the implementer plan (§3.1).

---

## 6. Runbook design — `docs/operations/deployment-runbook.md`

New directory `docs/operations/`. Operator-runnable, real commands, Compose-prod-specific.
Section outline:

1. **Overview & scope** — what this deploys (the prod compose topology), what it does NOT
   (no K8s); link to README §Docker and `docs/API_DOCUMENTATION.md` §Deployment.
2. **Prerequisites** — Docker Engine ≥ 24 + Compose v2 (`docker compose version`); a host
   with the prod `.env`; outbound access to SEC EDGAR / Yahoo / FRED; DNS A-record for the
   `Host()` domain; ports 80/443 open for ACME + TLS.
3. **Environment setup** — `cp config.env.prod.example .env`; fill every ✔ and 🔒 var;
   `chmod 600 .env`; the pre-read hook blocks reading `.env` in-repo (so it stays out of
   tooling). Note the host-var ⇄ container-var mapping (§4 note).
4. **Database provisioning** — Postgres path (default): create DB/user, run
   `cmd/migrate` against `DATABASE_URL` (entrypoint's `RUN_MIGRATIONS` is **SQLite-only** —
   does NOT migrate Postgres); SQLite path (alt): `DATABASE_DRIVER=sqlite3` +
   `RUN_MIGRATIONS=true` + a persisted `/app/data` volume.
5. **Build & launch** — `docker compose -f docker-compose.prod.yml up -d --build`; the
   `--profile monitoring` for prometheus/grafana; `docker compose ... ps` / `logs -f`.
6. **Verify** — `/health` (the compose healthcheck wgets it; `curl -f https://<host>/health`);
   `/metrics` (Prometheus exposition); a smoke `GET /api/v1/fair-value/AAPL` with a real
   `X-API-Key` (provisioned per §9).
7. **TLS (Traefik + Let's Encrypt)** — `ACME_EMAIL` drives registration; the `Host()` label
   must match the public DNS name (compose ships `api.dcf-valuation.com` — operators MUST
   edit it to their domain); `letsencrypt` named volume persists `acme.json`; the http-01
   challenge needs port 80 reachable.
8. **Scaling & rollout** — the compose `deploy.replicas: 2` + `update_config`
   (parallelism 1, monitor 60s, `failure_action: rollback`); note Compose-standalone vs
   Swarm semantics for `deploy:` (see §8 OPEN QUESTION); rolling-image update via
   `VERSION` bump + `up -d`.
9. **Secrets handling** — never commit `.env`; secrets-manager / host-`.env` only; the
   pre-read hook blocks `.env`; **API-key provisioning** (the DB-backed path:
   `cmd/seed-demo-key` for a first key, `cmd/hash-key` to hash an externally-generated key
   and insert it) — NOT an env var.
10. **Backup & restore** — the named volumes (`dcf_letsencrypt`, `dcf_prometheus_data`,
    `dcf_grafana_data`); Postgres `pg_dump`/`pg_restore` (DB is the external Postgres, not a
    volume) vs SQLite (`/app/data` volume `docker run --rm -v … tar`); `acme.json` restore note.
11. **Rollback** — automatic (`update_config.failure_action: rollback`) + manual
    (`VERSION=<prev> docker compose ... up -d`); DB-migration rollback caveat (migrations are
    forward-only — restore from backup).
12. **Troubleshooting** — table: empty-`${VAR}` symptom → fix; Postgres-validation boot
    failure (empty `DATABASE_URL`); SEC 403 (missing `SEC_USER_AGENT`); ACME failure
    (port 80 / `ACME_EMAIL` / wrong `Host()`); Redis-down (falls back to in-memory, WARN);
    healthcheck flapping; monitoring profile fails to start (missing `monitoring/` assets).

Full content outline in the implementer plan (§3.2).

---

## 7. No-code / no-secret statement

- **No-code:** this change touches only Markdown (`docs/refactoring/spec/`,
  `docs/refactoring/implementations/`, `docs/operations/`, `docs/reviewer/`) and a new env
  **template** (`config.env.prod.example`). No `.go` file, test, migration, Dockerfile,
  compose file, or runtime config default is modified. Behavior is unchanged ⇒ all
  load-bearing invariants (DDM bit-for-bit, recompute-shadow byte-identity, ledger-basket,
  CalcVersion 4.7, SchemaVersion["FinancialData"]=9) are **trivially unaffected**.
  `go build ./...` and `go test ./...` remain a no-op sanity check (expected exit 0, nothing
  changed).
- **No-secret:** the template carries **placeholders only** (`__set_in_secrets_manager__`,
  `__password__`, `Your Company contact@example.com`). No real key, token, password, or
  connection string is committed. The post-edit hook's secret scan must pass clean on every
  new file.

---

## 8. Open questions & recommendations

1. **Missing `monitoring/` assets.** The prod compose `--profile monitoring` mounts
   `./monitoring/prometheus.yml` and `./monitoring/grafana/provisioning`, which **do not
   exist** in the repo. **Recommendation:** the runbook documents that the monitoring profile
   requires these files and tells the operator to author a minimal `prometheus.yml` (scrape
   `dcf-api:8080/metrics`) before `--profile monitoring up`. Authoring the actual monitoring
   config is **out of TDB-6 scope** (separate config artifact) — flag for a follow-up
   (suggest TDB-6.3 or a new ticket). Do NOT silently create them in this docs-only change.
2. **`deploy:` keys under Compose-standalone.** `replicas`, `update_config`, and
   `restart_policy` in the `deploy:` block are **honoured by Docker Swarm**, but
   `docker compose -f … up` (Compose v2 standalone) **ignores `replicas`/`update_config`**
   (it respects only a subset). **Recommendation:** the runbook states this explicitly — for
   true 2-replica rolling deploys the operator runs `docker stack deploy` (Swarm) OR scales
   manually with `docker compose up --scale dcf-api=2` (note: `--scale` conflicts with the
   fixed `container_name`, so the operator must remove `container_name` for scaling). Document
   the limitation; do not change the compose file in this ticket.
3. **`SCHEDULER_ENABLED` across replicas.** With 2 replicas, enabling the scheduler on both
   double-runs background jobs. **Recommendation:** keep `SCHEDULER_ENABLED=false` by default
   (current behavior); document that the scheduler should run on a single dedicated instance
   if needed. Out of scope to solve leader-election here.
4. **Dockerfile `HEALTHCHECK` mismatch (latent, pre-existing).** The Dockerfile's
   `HEALTHCHECK CMD /app/dcf-api --health-check` invokes a flag that
   **`cmd/server/main.go` does not implement** — it would boot a second server process rather
   than probe health. The **compose-level** healthcheck (`wget --spider …/health`) is correct
   and is what prod uses. **Recommendation:** the runbook relies on the compose healthcheck;
   flag the Dockerfile `HEALTHCHECK` as a separate code defect (follow-up ticket) — out of
   TDB-6 (no-code) scope.
5. **Host-var vs container-var naming drift.** The compose file deliberately exposes shorter
   host names (`DATABASE_URL`, `REDIS_URL`, `FRED_ENABLED`) that differ from the container
   names the app reads. **Recommendation (taken):** the template uses the host names (what the
   operator's `.env` needs) and the §4 table records the container mapping; the runbook calls
   out the mapping so operators aren't surprised when `docker exec env` shows different names.

---

## 9. Acceptance criteria

- [ ] **Decision recorded** (Docker Compose prod) in this spec + the tracker.
- [ ] `config.env.prod.example` exists, **⊇ every `${VAR}` the prod compose file expands**
      (§4.1: `BUILD_DATE`, `VERSION`, `DATABASE_DRIVER`, `DATABASE_URL`, `REDIS_URL`,
      `SEC_USER_AGENT`, `FRED_ENABLED`, `FRED_API_KEY`, `MANUAL_RISK_FREE_RATE`,
      `MANUAL_MARKET_RISK_PREMIUM`, `ACME_EMAIL`, `GRAFANA_PASSWORD`), grouped, each with a
      one-line comment, secret rows annotated.
- [ ] Template contains **placeholders only — no real secret** (post-edit secret scan clean).
- [ ] `docs/operations/deployment-runbook.md` exists with all 12 §6 sections, operator-runnable
      commands, and cross-references (not duplicates) of README / API docs.
- [ ] `go build ./...` exit 0 (sanity — no code changed).
- [ ] Tracker `docs/reviewer/TDB-6-...md` updated: decision recorded, spec+plan linked,
      "target chosen" acceptance box checked, Status advanced.
