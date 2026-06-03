# Future Work — DB-backed `AssumptionProfile` Registry

**Status:** DEFERRED — interface ships in Tier 2; concrete DB-backed implementation tracked here for if/when it becomes needed.
**Filed:** 2026-05-14 alongside Tier 2 spec landing.
**Companion:** `docs/refactoring/archive/assumption-profile-spec.md`

---

## Context

Tier 2 introduced the `profile.Registry` interface in `internal/services/valuation/profile/registry.go` precisely so a future DB-backed implementation can swap in without touching consumers. The Tier 2 implementation is JSON-backed (`jsonRegistry` reads `config/assumption_profiles.json` at startup). This document tracks the deferred work for a DB-backed alternative.

---

## When this work becomes worth doing

Trigger any of these conditions:

1. **Multi-tenant tuning** — multiple customers (e.g., different fund managers, different research desks) want different caps/horizons applied to the same ticker. JSON-config-as-source-of-truth doesn't scale to this.
2. **Real-time analyst tuning** — analysts want to retune caps via an admin UI without filing a PR + waiting for a service restart.
3. **Row-level audit history** — git-blame on the JSON file is sufficient for "who changed what when," but a row-level audit table (analyst, timestamp, prior/new values, reason) would be more discoverable and queryable.
4. **A/B-test on profile values** — comparing two cap regimes head-to-head against the same set of bundles requires a registry that can serve different profile values for different request cohorts.

None of these apply to Midas today (single-tenant, personal investment use, no analyst pool). If any of them become real, this tracker activates.

---

## Implementation sketch

### Storage layer

A `assumption_profiles` table + companion `archetype_rules` table:

```sql
CREATE TABLE assumption_profiles (
    profile_id              TEXT PRIMARY KEY,    -- e.g. "mature_large_bank:mature"
    archetype               TEXT NOT NULL,
    maturity                TEXT NOT NULL,
    horizon_years           INTEGER NOT NULL,
    compound_growth_cap     REAL NOT NULL,
    revenue_base_method     TEXT NOT NULL,
    discount_method         TEXT NOT NULL,
    terminal_method         TEXT NOT NULL,
    stabilized              INTEGER NOT NULL,     -- 0 or 1
    fade_years              INTEGER NOT NULL,
    terminal_multiple       REAL NOT NULL,
    dps_growth_cap          REAL NOT NULL,
    payout_path             TEXT,                 -- JSON-encoded []float64
    dividend_forecast_horizon INTEGER NOT NULL,
    stable_dividend_growth  REAL NOT NULL,
    large_cap_min_usd       REAL,                 -- nullable; null = use global fallback
    mid_cap_min_usd         REAL,
    config_version          TEXT NOT NULL,        -- semver; bumped on any row change
    updated_at              TIMESTAMP NOT NULL,
    updated_by              TEXT NOT NULL         -- audit
);

CREATE TABLE archetype_rules (
    rule_id                 TEXT PRIMARY KEY,
    priority                INTEGER NOT NULL,
    industry_prefix         TEXT NOT NULL,
    archetype               TEXT NOT NULL,
    notes                   TEXT,
    config_version          TEXT NOT NULL,
    updated_at              TIMESTAMP NOT NULL,
    updated_by              TEXT NOT NULL
);

CREATE TABLE assumption_profile_audit (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type             TEXT NOT NULL,        -- "profile" | "rule"
    entity_id               TEXT NOT NULL,
    field_changed           TEXT NOT NULL,
    old_value               TEXT,
    new_value               TEXT,
    changed_at              TIMESTAMP NOT NULL,
    changed_by              TEXT NOT NULL,
    reason                  TEXT
);
```

### Registry implementation

```go
// internal/services/valuation/profile/db_registry.go (future)
type dbRegistry struct {
    db              *sqlx.DB
    cache           atomic.Value  // *profileCache; refreshed via SIGHUP or polling
    refreshInterval time.Duration
    configHashFn    func(profiles []AssumptionProfile, rules []ArchetypeRule) string
}

func NewDBRegistry(db *sqlx.DB, refreshInterval time.Duration) (Registry, error) {
    r := &dbRegistry{db: db, refreshInterval: refreshInterval, configHashFn: defaultConfigHash}
    if err := r.refresh(context.Background()); err != nil {
        return nil, fmt.Errorf("initial profile load: %w", err)
    }
    go r.refreshLoop()
    return r, nil
}

// Resolve, Lookup, ConfigVersion, ConfigHash implementations read from the
// atomic cache — same hot-path performance as jsonRegistry.
```

### Trade-offs vs JSON-backed

| Axis | JSON (Tier 2) | DB-backed (future) |
|---|---|---|
| Tuning workflow | Edit file + PR review + restart | UPDATE in admin UI; auto-refresh |
| Multi-tenancy | One table for one engine | Tenant-keyed rows possible |
| Audit history | Git blame on JSON | Dedicated audit table with reason |
| A/B testing | Not possible | Cohort-keyed profile serving |
| Operational dependency | None beyond the binary | SQLite or PostgreSQL |
| Replay determinism | Config hash + ResolvedSnapshot | Same patterns apply; queries become point-in-time |
| Startup failure mode | File malformed → fail loud | Table empty → fail loud |
| Complexity | ~150 lines of loader + validation | ~300-500 lines including refresh logic, audit triggers |

---

## Migration path (if/when this activates)

1. Existing `jsonRegistry` implementation stays — kept as the embedded default for single-tenant deployments
2. Add `dbRegistry` as a second `Registry` impl behind a config flag (`profile.backend: "json" | "db"`)
3. Add a one-time migration that reads `config/assumption_profiles.json` and seeds the DB tables
4. Replay tooling already supports `ResolvedSnapshot` capture; replay determinism preserved by construction
5. Existing `Facts` DTO and `Resolve()` contract unchanged — only the `Registry` impl swaps

No consumer code changes required. This was the explicit design intent when the interface was introduced in Tier 2.

---

## What is NOT being deferred

- The interface itself (ships in Tier 2)
- The `ResolvedSnapshot` bundle-manifest field (ships in Tier 2 P0b)
- The `ConfigHash` discipline (ships in Tier 2 P0a)
- Validation invariants (ships in Tier 2; portable across both implementations)

These ship now because they're load-bearing for replay determinism and audit visibility regardless of the storage backend. The deferred work is purely the swap-in of a DB-backed `Registry` implementation.

---

## Out of scope for this tracker

- The admin UI for analyst tuning (separate frontend work)
- Multi-tenant authentication/authorization (orthogonal concern)
- A/B-test cohort serving infrastructure (different system)
- Cross-region replication of the profile DB (operational concern, not architectural)

---

## Acceptance criteria (when this activates)

- [ ] `dbRegistry` ships with full `Registry` interface compliance
- [ ] Migration script reads existing JSON config and seeds DB tables
- [ ] Audit table populated on every row change
- [ ] Replay tooling continues to work end-to-end (config hash discipline preserved)
- [ ] Coverage ≥90% on `db_registry.go`
- [ ] Documented operational runbook for the DB backend (backup, restore, schema migration)
- [ ] Performance benchmark: DB-backed `Resolve()` p99 latency ≤2× the JSON-backed `Resolve()` p99
