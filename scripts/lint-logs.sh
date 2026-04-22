#!/usr/bin/env bash
# lint-logs.sh — Phase S observability CI guard (Linux/macOS)
#
# Fails (exit 1) if any request-path service or gateway file still uses a
# singleton receiver-logger call (e.g. s.logger.Info, c.logger.Warn) where
# logctx.Or(ctx, ...) should be used instead.
#
# Scan scope: internal/services/**, internal/infra/gateways/**, internal/api/v1/handlers/**
#
# Whitelist (dirs/files intentionally skipped — future migration phases or
# singleton-only contexts):
#   - internal/services/scheduler/          (background jobs: always singleton)
#   - internal/services/auth/               (Phase T migration)
#   - internal/services/watchlist/          (Phase T migration)
#   - internal/services/alerting/           (Phase T migration)
#   - internal/services/metrics/            (Phase T migration)
#   - internal/services/ratelimit/          (Phase T migration)
#   - internal/services/datacleaner/ai/     (Phase T migration)
#   - internal/services/valuation/models/router.go
#       SelectModel has no ctx parameter — tracked concern for Phase M
#   - internal/services/growth/estimator.go
#       EstimateGrowthRates has no ctx parameter — tracked concern for Phase M
#
# Usage: ./scripts/lint-logs.sh
#        (run from repo root)

set -euo pipefail

# Pattern: receiver-scoped logger calls using single-letter variables in (s,c,r,e,g,h)
PATTERN='(s|c|r|e|g|h)\.logger\.(Info|Warn|Error|Debug)\('

# Directories to scan
SCAN_DIRS=(
    "internal/services"
    "internal/infra/gateways"
    "internal/api/v1/handlers"
)

# Verify rg (ripgrep) is available
if ! command -v rg &>/dev/null; then
    echo "ERROR: ripgrep (rg) is required but not found."
    echo "  Install: brew install ripgrep  OR  apt-get install ripgrep"
    # Exit 2 (not 1) so CI can distinguish "dependency missing" from
    # "lint violation detected" (which is exit 1). Matches lint-logs.ps1.
    exit 2
fi

echo "lint-logs: scanning for unguarded singleton logger calls..."
echo "  Pattern : ${PATTERN}"
echo "  Dirs    : ${SCAN_DIRS[*]}"
echo ""

# Run ripgrep with whitelist globs
# rg exit 0 = matches found, exit 1 = no matches, exit 2 = error
set +e
OUTPUT=$(rg \
    --no-heading \
    --line-number \
    --color never \
    --type go \
    --glob '!internal/services/scheduler/**' \
    --glob '!internal/services/auth/**' \
    --glob '!internal/services/watchlist/**' \
    --glob '!internal/services/alerting/**' \
    --glob '!internal/services/metrics/**' \
    --glob '!internal/services/ratelimit/**' \
    --glob '!internal/services/datacleaner/ai/**' \
    --glob '!internal/services/valuation/models/router.go' \
    --glob '!internal/services/growth/estimator.go' \
    "${PATTERN}" \
    "${SCAN_DIRS[@]}" 2>&1)
RG_EXIT=$?
set -e

if [ "${RG_EXIT}" -eq 0 ]; then
    # rg exit 0 = matches found → FAIL
    echo "FAIL: Found unguarded singleton logger calls that should use logctx.Or(ctx, ...):"
    echo ""
    echo "${OUTPUT}" | while IFS= read -r line; do
        echo "  ${line}"
    done
    echo ""
    echo "Fix: replace <receiver>.logger.<Level>(...) with logctx.Or(ctx, <receiver>.logger).<Level>(...)"
    echo "     or add the file to the whitelist in scripts/lint-logs.sh if it is a background-only path."
    exit 1
elif [ "${RG_EXIT}" -eq 1 ]; then
    # rg exit 1 = no matches → PASS
    echo "OK: No unguarded singleton logger calls found in request-path code."
    exit 0
else
    # rg exit 2 = error
    echo "ERROR: ripgrep encountered an error (exit ${RG_EXIT}):"
    echo "${OUTPUT}"
    exit 2
fi
