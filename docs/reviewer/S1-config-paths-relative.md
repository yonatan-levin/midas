# S-1: Config File Loading Uses Relative Paths

| Field | Value |
|-------|-------|
| **ID** | S-1 |
| **Severity** | LOW |
| **Status** | Open |
| **Found In** | Phase 3 Code Review |
| **Files** | `classifier.go:123`, `ffo.go:18`, `revenue_multiple.go:18` |

## Description

Config files (`industry_codes.json`, `industry_multiples.json`) are loaded via `os.ReadFile()` with relative paths like `"./config/datacleaner/industry_codes.json"`. This depends on the working directory at startup.

Works fine for `go run cmd/server/main.go` from project root, but fails in Docker containers with different working directories or when deployed as a standalone binary.

## Impact

- Server fails to classify industries in Docker (falls back to defaults silently)
- Not a crash — models degrade gracefully — but loses industry-specific routing

## Recommended Fix

**Option A:** Inject paths via `config.Config` and DI container
**Option B:** Use `embed.FS` to bundle JSON files into the binary
**Option C:** Use environment variable for config base path

Option B is cleanest for a self-contained binary deployment.

## Acceptance Criteria

- [ ] Config files loadable regardless of working directory
- [ ] Tests pass when run from any directory
