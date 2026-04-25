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
#   - internal/services/growth/estimator.go
#       1 singleton-logger call (line 130 area) is kept for back-compat with
#       pre-Phase-M callers that still invoke EstimateGrowthRates without ctx.
#       Full migration tracked in docs/reviewer/M1.
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
elif [ "${RG_EXIT}" -ne 1 ]; then
    # rg exit 2 = error
    echo "ERROR: ripgrep encountered an error (exit ${RG_EXIT}):"
    echo "${OUTPUT}"
    exit 2
fi
# rg exit 1 (no matches) → fall through to the second check below.

# ----------------------------------------------------------------------------
# Phase 1 (observability narrative & artifacts spec §6) — Debug-tracer prefix.
# Any Debug("…", …) call inside request-path code (services/, gateways/,
# handlers/) must use the message convention:
#   logger.Debug("trace.<area>.<op>", …)
# Free-form Debug messages are still allowed in non-request-path packages and
# in the legacy whitelist below (which we narrow as files migrate).
#
# Pattern: literal `Debug("` followed by anything that does NOT start with
# "trace.". We negative-look-ahead via PCRE2 (--pcre2).
# ----------------------------------------------------------------------------
echo ""
echo "lint-logs: scanning Debug() messages for trace.<area>.<op> prefix..."
DEBUG_PATTERN='\.Debug\("(?!trace\.)'

# Whitelist of files containing legacy Debug calls predating the convention.
# Drop entries from this list as you migrate them.
set +e
DEBUG_OUTPUT=$(rg \
    --no-heading \
    --line-number \
    --color never \
    --type go \
    --pcre2 \
    --glob '!internal/services/scheduler/**' \
    --glob '!internal/services/auth/**' \
    --glob '!internal/services/watchlist/**' \
    --glob '!internal/services/alerting/**' \
    --glob '!internal/services/metrics/**' \
    --glob '!internal/services/ratelimit/**' \
    --glob '!internal/services/datacleaner/ai/**' \
    --glob '!internal/services/growth/estimator.go' \
    --glob '!internal/services/valuation/service.go' \
    --glob '!internal/services/valuation/models/**' \
    --glob '!internal/infra/gateways/**' \
    "${DEBUG_PATTERN}" \
    "${SCAN_DIRS[@]}" 2>&1)
DEBUG_EXIT=$?
set -e

if [ "${DEBUG_EXIT}" -eq 0 ]; then
    echo "FAIL: Found Debug() calls in request-path code missing trace.<area>.<op> prefix:"
    echo ""
    echo "${DEBUG_OUTPUT}" | while IFS= read -r line; do
        echo "  ${line}"
    done
    echo ""
    echo "Fix: change message to 'trace.<area>.<op>' (e.g. trace.gateway.sec.fetch)"
    echo "     or whitelist the file in scripts/lint-logs.sh."
    exit 1
elif [ "${DEBUG_EXIT}" -eq 1 ]; then
    echo "OK: All non-whitelisted Debug() calls follow trace.<area>.<op> prefix."
else
    echo "ERROR: ripgrep PCRE2 check failed (exit ${DEBUG_EXIT}):"
    echo "${DEBUG_OUTPUT}"
    exit 2
fi

echo "OK: No unguarded singleton logger calls found in request-path code."
exit 0
