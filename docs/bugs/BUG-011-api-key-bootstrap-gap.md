# BUG-011: API key bootstrap gap - no path to manage:keys permission

| Field | Value |
|-------|-------|
| **ID** | BUG-011 |
| **Title** | No bootstrap path for `manage:keys` permission; demo key hidden after migration |
| **Severity** | HIGH |
| **Status** | Resolved (2026-04-05) |
| **Component** | Auth Service / seed-demo-key / Migrations |
| **Reported** | 2026-04-05 |

## Summary

Three related issues prevent users from properly creating and obtaining API keys:

1. **Hidden demo key**: `cmd/migrate` applies `0001_seed_demo_key.sql` which inserts a deterministic demo key, but the raw key is only in a SQL comment — never printed to stdout. Users have no idea what their key is after running migrate.

2. **Chicken-and-egg privilege gap**: The `POST /api/v1/auth/keys` endpoint requires `manage:keys` permission (`server.go:251`), but both key-creation paths only grant `read:fair_value`:
   - `migrations/0001_seed_demo_key.sql` inserts `'["read:fair_value"]'`
   - `cmd/seed-demo-key/main.go:60` uses `[]entities.Permission{entities.PermissionReadFairValue}`
   
   Result: the auth/keys endpoint is permanently unreachable — a dead endpoint.

3. **Silent duplicate keys**: Running `seed-demo-key` after `migrate` silently creates a second key without warning.

## Steps to Reproduce

```bash
# 1. Apply schema + migrations
go run ./cmd/migrate -db ./data/midas.db
# Output: "Applied migration: 0001_seed_demo_key.sql" — no key printed

# 2. Try to create a key via API using the seeded demo key
curl -X POST http://localhost:8080/api/v1/auth/keys \
  -H "X-API-Key: dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"test","permissions":["read:fair_value"]}'
# Returns: 403 Insufficient permissions (key lacks manage:keys)

# 3. Run seed-demo-key to get a printed key
go run ./cmd/seed-demo-key -db ./data/midas.db
# Creates a new key with only read:fair_value — still can't hit auth endpoint
```

## Root Cause

| File | Line | Issue |
|------|------|-------|
| `migrations/0001_seed_demo_key.sql` | 8 | Permissions hardcoded to `["read:fair_value"]` only |
| `cmd/seed-demo-key/main.go` | 60 | Only grants `PermissionReadFairValue` |
| `cmd/migrate/main.go` | 70 | Does not print the demo key after applying seed migration |
| `internal/api/server.go` | 251 | Auth group requires `PermissionManageKeys` — no bootstrap |

## Proposed Fix

1. **`cmd/seed-demo-key/main.go`**: Grant all necessary permissions (`read:fair_value`, `read:health`, `read:metrics`, `manage:keys`, `admin:all`)
2. **`migrations/0001_seed_demo_key.sql`**: Update permissions JSON to include `manage:keys` and `admin:all`; re-compute SHA-256 hash for the demo key
3. **`cmd/migrate/main.go`**: After applying the seed migration, print the demo key so users can see it

## Acceptance Criteria

- [ ] `go run ./cmd/migrate` prints the demo API key to stdout after seeding
- [ ] The seeded demo key has `manage:keys` permission (at minimum)
- [ ] `POST /api/v1/auth/keys` succeeds with the demo key
- [ ] `go run ./cmd/seed-demo-key` creates a key with full permissions
- [ ] Existing `read:fair_value` functionality is not regressed
- [ ] Auth service tests pass
- [ ] Hash in migration matches the raw key (verified via `cmd/hash-key`)
