#!/usr/bin/env bash
# Check that every local service answers. Exit 1 if one does not.
set -uo pipefail
cd "$(dirname "$0")/.."
set -a; [ -f .env ] && . ./.env; set +a

fail=0
check() { if eval "$2" >/dev/null 2>&1; then echo "  ok    $1"; else echo "  FAIL  $1"; fail=1; fi; }

check "postgres: multica, spire, assignments, broker databases" \
  "[ \$(docker compose exec -T postgres psql -U workspace -d multica -Atc \"select count(*) from pg_database where datname in ('multica','spire','assignments','broker')\") = 4 ]"
check "multica api /readyz" "curl -fsS http://localhost:${MULTICA_API_PORT:-8080}/readyz"
check "multica web" "curl -fsS -o /dev/null http://localhost:${MULTICA_WEB_PORT:-3000}/"
check "tornjak api" "curl -fsS -o /dev/null http://localhost:${TORNJAK_API_PORT:-10000}/"
check "tornjak ui" "curl -fsS -o /dev/null http://localhost:${TORNJAK_PORT:-3001}/"
check "tornjak api is read-only" "[ \$(curl -s -o /dev/null -w '%{http_code}' -X POST -d '{}' http://localhost:${TORNJAK_API_PORT:-10000}/api/v1/spire/entries) = 403 ]"
check "broker ui" "curl -fsS -o /dev/null http://localhost:${BROKER_ADMIN_PORT:-15000}/ui/"
check "broker ui is read-only" "[ \$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H 'content-type: application/json' -d '{\"resources\":[]}' http://localhost:${BROKER_ADMIN_PORT:-15000}/api/config/resources/mcp.target) = 403 ]"
check "mailpit api" "curl -fsS http://localhost:${MAILPIT_PORT:-8025}/api/v1/info"
check "spire server healthy" "docker compose exec -T spire-server /opt/spire/bin/spire-server healthcheck"
check "node CA is a CA" "openssl x509 -in .state/node-ca/ca.pem -noout -text | grep -q CA:TRUE"
exit $fail
