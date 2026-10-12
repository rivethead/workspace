#!/usr/bin/env bash
# Stage L4 behaviour, end to end with real identities: the launcher on
# a laptop asks the broker whether it may start runs (ARCHITECTURE.md D50).
#
#   scripts/smoke-l4.sh [<person>.<machine>]    a laptop that is up (default alex.mac)
set -uo pipefail
cd "$(dirname "$0")/.."
cn="${1:-alex.mac}"
fail=0
ok()  { echo "  ok    $1"; }
bad() { echo "  FAIL  $1: $2"; fail=1; }
project=laptop-${cn//./-}
# Calls come from a throwaway container with the launcher service's labels: the
# selectors SPIRE gives the launcher identity to (scripts/laptop.sh). The real
# launcher image has no curl.
call() {
  docker run --rm -i --entrypoint bash --network workspace-edge \
    --label "com.docker.compose.project=$project" --label com.docker.compose.service=launcher \
    -v "${project}_spire-agent-socket:/run/spire/sockets:ro" claudebox -s -- "$@" < laptop/broker-call.sh 2>&1
}
expect() { local name="$1" want="$2" out; shift 2; out=$(call "$@"); [[ "$out" == *"$want"* ]] && ok "$name" || bad "$name" "$out"; }

cs() { docker compose exec -T credential-service /credential-service "$@"; }
rules=.state/broker/rules.json
cp "$rules" "$rules.bak" 2>/dev/null || true
trap 'mv "$rules.bak" "$rules" 2>/dev/null; cs start smoke-ada smoke >/dev/null 2>&1' EXIT
cat > "$rules" <<JSON
{"agents": {
  "smoke-ada":   {"status": "active", "placement": "$cn"},
  "smoke-grace": {"status": "active", "placement": "someone-else.mac"},
  "smoke-old":   {"status": "retired", "placement": "$cn"}
}}
JSON

expect "an agent placed on this laptop may start"      '"allowed":true'      broker /v1/launcher/check '{"agent":"smoke-ada"}'
expect "an agent placed on another laptop is refused"  'wrong_placement'     broker /v1/launcher/check '{"agent":"smoke-grace"}'
expect "a retired agent is refused"                     'not_active'          broker /v1/launcher/check '{"agent":"smoke-old"}'
expect "an unknown agent is refused"                    'unknown_agent'       broker /v1/launcher/check '{"agent":"nobody"}'
expect "a token for another audience is refused"        'InvalidAudience'     assignments /v1/launcher/check '{"agent":"smoke-ada"}'
expect "a launcher cannot get GitHub tokens"            'wrong_caller'        broker /v1/github/token '{"repository":"example-org/workspace-sandbox"}'
# A laptop reaches only the workspace's edge, never its private services.
for host in postgres:5432 mailpit:8025 credential-service:8090 spire-oidc:8008 broker:15000 broker-admin:15000 tornjak-backend:10000 tornjak-api:10000; do
  if docker run --rm --network workspace-edge --entrypoint bash claudebox -c "exec 3<>/dev/tcp/${host%:*}/${host#*:}" 2>/dev/null; then
    bad "a laptop cannot reach $host" "connected"
  else
    ok "a laptop cannot reach $host"
  fi
done
cs stop smoke-ada smoke "stop switch test" >/dev/null
expect "a stopped agent is refused at once"             '"reason":"stopped"'  broker /v1/launcher/check '{"agent":"smoke-ada"}'
cs start smoke-ada smoke >/dev/null
expect "a started agent is allowed again"               '"allowed":true'      broker /v1/launcher/check '{"agent":"smoke-ada"}'
exit $fail
