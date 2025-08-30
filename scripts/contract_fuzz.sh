#!/usr/bin/env bash
set -euo pipefail

# Local contract fuzzing using Schemathesis against the running server
# Requires: schemathesis installed (pip install schemathesis)

API_BASE="${API_BASE:-http://localhost:8080}"
OPENAPI_URL="${OPENAPI_URL:-$API_BASE/docs/openapi.yaml}"

if ! command -v schemathesis >/dev/null 2>&1; then
  echo "ERROR: schemathesis is not installed. Run: pip install schemathesis" >&2
  exit 1
fi

if [[ -z "${DEMO_KEY:-}" ]]; then
  echo "ERROR: DEMO_KEY environment variable is required (raw key)." >&2
  exit 1
fi

echo "🔎 Verifying OpenAPI at $OPENAPI_URL"
curl -fsS "$OPENAPI_URL" >/dev/null

echo "🚀 Running Schemathesis fuzzing..."
schemathesis run "$OPENAPI_URL" \
  --checks all \
  --stateful=none \
  --headers "X-API-Key: $DEMO_KEY"

echo "✅ Schemathesis completed"


