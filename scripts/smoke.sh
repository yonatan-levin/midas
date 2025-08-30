#!/usr/bin/env bash
set -euo pipefail

# Simple smoke test: create an API key, then call fair-value for AAPL
# Requirements: server running on localhost:8080

API_BASE="${API_BASE:-http://localhost:8080}"

create_key() {
  # Prefer admin route when ADMIN_KEY is provided
  if [[ -n "${ADMIN_KEY:-}" ]]; then
    echo "Creating demo key via admin route..."
    resp=$(curl -sS -X POST "$API_BASE/api/v1/auth/keys" \
      -H "X-API-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
      -d '{"user_id":"demo","permissions":["read:fair_value"]}')
    echo "$resp" | jq -r '.key'
    return
  fi

  # Fallback: create key directly in DB via CLI
  if command -v go >/dev/null 2>&1; then
    echo "Creating demo key via CLI (cmd/seed-demo-key)..."
    out=$(go run ./cmd/seed-demo-key -db "${DB_PATH:-./data/midas.db}")
    echo "$out" | sed -n 's/^DEMO_API_KEY=//p'
    return
  fi

  echo "ERROR: neither ADMIN_KEY set nor Go toolchain available for CLI fallback." >&2
  exit 1
}

main() {
  echo "🔎 Health: $API_BASE/health"
  curl -sS "$API_BASE/health" | jq .

  if [[ -z "${DEMO_KEY:-}" ]]; then
    DEMO_KEY=$(create_key)
  fi
  echo "Using DEMO_KEY=${DEMO_KEY:0:8}..."

  echo "📈 GET /api/v1/fair-value/AAPL"
  curl -sS "$API_BASE/api/v1/fair-value/AAPL" -H "X-API-Key: $DEMO_KEY" | jq .
}

main "$@"


