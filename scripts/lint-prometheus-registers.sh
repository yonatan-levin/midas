#!/usr/bin/env bash
# lint-prometheus-registers.sh — R3 Stage I.0 (v2 Addition #1) CI guard
#
# Fails (exit 1) if any Go file outside the allowlist registers Prometheus
# collectors against the process-global DefaultRegisterer (or a promauto
# constructor variant that uses it). The per-instance-registry pattern
# established by PREX-1 (internal/services/metrics/service.go) is the only
# acceptable shape; this lint prevents silent reintroduction of the global
# registry hazard.
#
# Hazards detected:
#   - prometheus.MustRegister(...)          — global side-effect registration
#   - prometheus.Register(...)              — same, returns error variant
#   - prometheus.DefaultRegisterer          — direct reference to the global
#   - promauto.NewCounter / NewGauge / ...  — promauto top-level constructors
#                                             default to DefaultRegisterer
#
# Safe (NOT flagged):
#   - registry.MustRegister(...)            — registers on a service-owned
#                                             *prometheus.Registry instance
#   - promauto.With(registry).NewXXX(...)   — explicit per-instance scope
#
# Allowlist: paths that may legitimately contain the patterns above (e.g.,
# inline comments documenting why DefaultRegisterer is avoided). Add to the
# array below ONLY with PR-level rationale.
#
# Exit codes:
#   0 — clean (no stray registrations outside allowlist)
#   1 — stray registration found
#   2 — ripgrep not installed
#
# Usage: ./scripts/lint-prometheus-registers.sh
#        (run from repo root)

set -euo pipefail

# Patterns to flag. Each is wrapped in word-boundary-ish anchors via the
# rg invocation below so we don't over-match (e.g. promauto.NewSomething
# is fine if it's a method like promauto.With(...).NewCounter — that
# survives because rg matches the literal substring "promauto.New").
PATTERNS=(
    'prometheus\.MustRegister\('
    'prometheus\.Register\('
    'prometheus\.DefaultRegisterer'
    'promauto\.New[A-Z]'
)

# Allowlist: paths where the patterns are acceptable. Each is a literal
# path relative to repo root. The metrics service file is the ONLY place
# we register collectors — every entry there uses promauto.With(registry),
# never the global. The two service.go / service_test.go entries below
# are inline comments documenting why DefaultRegisterer is avoided.
#
# internal/observability/replay/module.go is allowlisted for an audit
# doc-comment only (Stage I.0): the comment block at line 309 references
# `prometheus.DefaultRegisterer` to explain why metrics.NewService allocates
# a fresh per-service registry instead. The replay module wires
# *metrics.Service via metrics.NewService — verified by the same Stage I.0
# audit — so allowlisting only documents the avoidance, never an actual
# global registration.
ALLOWLIST=(
    'internal/services/metrics/service.go'
    'internal/services/metrics/service_test.go'
    'internal/observability/replay/module.go'
)

# Verify rg (ripgrep) is available
if ! command -v rg &>/dev/null; then
    echo "ERROR: ripgrep (rg) is required but not found."
    echo "  Install: brew install ripgrep  OR  apt-get install ripgrep  OR  choco install ripgrep"
    exit 2
fi

echo "lint-prometheus-registers: scanning for stray DefaultRegisterer registrations..."

# Build a single regex from the patterns (rg supports alternation via |).
PATTERN_OR="$(IFS='|'; echo "${PATTERNS[*]}")"

# Run rg without filtering allowlist; we'll filter in shell below so we can
# emit a precise error message.
set +e
OUTPUT=$(rg \
    --no-heading \
    --line-number \
    --color never \
    --type go \
    "${PATTERN_OR}" \
    . 2>&1)
RG_EXIT=$?
set -e

if [ "${RG_EXIT}" -eq 1 ]; then
    # rg exit 1 = no matches anywhere. Allowlist is for matches that DO
    # occur but are acceptable; zero matches is the cleanest possible state.
    echo "OK: No prometheus DefaultRegisterer references found anywhere."
    exit 0
elif [ "${RG_EXIT}" -ne 0 ]; then
    echo "ERROR: ripgrep encountered an error (exit ${RG_EXIT}):"
    echo "${OUTPUT}"
    exit 2
fi

# Filter out allowlisted paths. ripgrep output format is `path:line:match`.
STRAY=""
while IFS= read -r line; do
    # Extract path before the first ':' (Windows paths use forward slashes
    # under rg by default).
    path="${line%%:*}"
    # Normalize leading "./" if present (rg sometimes emits with prefix).
    path="${path#./}"
    allowed=0
    for ok in "${ALLOWLIST[@]}"; do
        if [ "${path}" = "${ok}" ]; then
            allowed=1
            break
        fi
    done
    if [ "${allowed}" -eq 0 ]; then
        STRAY+="${line}"$'\n'
    fi
done <<< "${OUTPUT}"

if [ -n "${STRAY}" ]; then
    echo "FAIL: Stray prometheus DefaultRegisterer references found outside allowlist:"
    echo ""
    echo -n "${STRAY}" | while IFS= read -r line; do
        echo "  ${line}"
    done
    echo ""
    echo "Fix: register collectors on a service-owned *prometheus.Registry via"
    echo "     promauto.With(registry).NewXXX(...) or registry.MustRegister(...)."
    echo "     Avoid prometheus.DefaultRegisterer / promauto.NewXXX — they leak"
    echo "     into the process-global registry and break per-instance hermeticity"
    echo "     (the property R3 Stage I parallel replay relies on)."
    echo ""
    echo "     If a new path legitimately needs to be allowlisted, add it to the"
    echo "     ALLOWLIST array in this script with PR-level rationale."
    exit 1
fi

echo "OK: All prometheus references confined to allowlist."
exit 0
