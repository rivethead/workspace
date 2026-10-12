#!/usr/bin/env bash
# The threat tests (docs/threat-model.md): one check for each threat that the
# local workspace can show, named by its threat ID.
#
#   scripts/smoke-threats.sh [<person>.<machine>]    a laptop that is up (default alex.mac)
#
# Result kinds, as in claudebox/tests/run.sh:
#   ok     the control works
#   FAIL   the control does not work; the script exits 1
#   known  an open threat that is still present. Reported, but not a failure.
#          When one starts to pass, the script says so: update docs/threat-model.md.
#   skip   not possible here
set -uo pipefail
cd "$(dirname "$0")/.."
cn="${1:-alex.mac}"
project=laptop-${cn//./-}
TD=workspace.local

PASS=0 FAIL=0 KNOWN=0 SKIP=0 FIXED=0
pass()  { echo "  ok     $1"; PASS=$((PASS + 1)); }
fail()  { echo "  FAIL   $1"; [ -n "${2:-}" ] && echo "$2" | tail -5 | sed 's/^/         /'; FAIL=$((FAIL + 1)); }
skip()  { echo "  skip   $1 ($2)"; SKIP=$((SKIP + 1)); }
# known <threat> <description> <threat-present: 0|1>
known() {
  if [ "$3" = "1" ]; then
    echo "  known  $2 [$1]"; KNOWN=$((KNOWN + 1))
  else
    echo "  ok     $2 [$1 looks fixed: update docs/threat-model.md]"; FIXED=$((FIXED + 1)); PASS=$((PASS + 1))
  fi
}
# check <threat> <description> <command>: ok when the command succeeds.
check() { if eval "$3" >/dev/null 2>&1; then pass "$2 [$1]"; else fail "$2 [$1]"; fi; }
section() { echo; echo "== $1"; }

ws()  { docker compose -f docker-compose.yml "$@"; }
srv() { ws exec -T spire-server /opt/spire/bin/spire-server "$@"; }
cs()  { ws exec -T credential-service /credential-service "$@"; }
code() { curl -s -o /dev/null -w '%{http_code}' -m 5 "$@"; }

docker volume inspect "${project}_spire-agent-socket" >/dev/null 2>&1 \
  || { echo "laptop $cn is not up: run scripts/laptop.sh up $cn" >&2; exit 1; }

# ---------------------------------------------------------------------------
section "Identity (STRIDE: spoofing)"
# Two throwaway entries: one placed on this laptop, one on another laptop.
here_id="spiffe://$TD/agent/smoke-threat"
away_id="spiffe://$TD/agent/smoke-threat-away"
entry_ids=()
cleanup() {
  for id in "${entry_ids[@]}"; do srv entry delete -entryID "$id" >/dev/null 2>&1; done
  cs start smoke-threat smoke-threats >/dev/null 2>&1
}
trap cleanup EXIT
new_entry() {
  local id="$1" parent="$2" agent="$3"
  srv entry create -parentID "$parent" -spiffeID "$id" \
    -selector "docker:label:workspace.agent:$agent" -selector "docker:label:workspace.laptop:$cn" \
    | awk '/^Entry ID/{print $4}'
}
entry_ids+=("$(new_entry "$here_id" "spiffe://$TD/spire/agent/x509pop/$cn" smoke-threat)")
entry_ids+=("$(new_entry "$away_id" "spiffe://$TD/spire/agent/x509pop/someone-else.mac" smoke-threat-away)")
# A container that the launcher did not start, with an agent's labels.
svid_for() {
  docker run --rm --label "workspace.agent=$1" --label "workspace.laptop=$cn" \
    -v "${project}_spire-agent-socket:/run/spire/sockets:ro" --entrypoint spire-agent claudebox \
    api fetch jwt -audience broker -socketPath /run/spire/sockets/agent.sock -timeout 3s 2>&1
}
got=""
for _ in $(seq 10); do   # the SPIRE agent picks up new entries within a few seconds
  got=$(svid_for smoke-threat)
  [[ "$got" == *"$here_id"* ]] && break
  sleep 2
done
known TM-S1 "a container that the launcher did not start gets an agent's identity" \
  "$([[ "$got" == *"$here_id"* ]] && echo 1 || echo 0)"
got=$(svid_for smoke-threat-away)
[[ "$got" != *"$away_id"* ]] && pass "an agent placed on another laptop gets no identity here [TM-S1]" \
  || fail "an agent placed on another laptop gets no identity here [TM-S1]" "$got"
mode=$(python3 -c 'import os,sys;print(oct(os.stat(sys.argv[1]).st_mode & 0o777))' ".state/laptops/$cn/agent.key" 2>/dev/null)
[ "$mode" = "0o600" ] && pass "the machine key is readable by its owner only [TM-S2]" \
  || fail "the machine key is readable by its owner only [TM-S2]" "mode $mode"

# ---------------------------------------------------------------------------
section "Tornjak (STRIDE: tampering)"
api="http://localhost:${TORNJAK_API_PORT:-10000}"
check TM-T6 "Tornjak's API still answers reads" "[ \$(code $api/api/v1/spire/entries) = 200 ]"
check TM-T6 "Tornjak's API refuses to create entries" \
  "[ \$(code -X POST -H 'content-type: application/json' -d '{}' $api/api/v1/spire/entries) = 403 ]"
check TM-T6 "Tornjak's API refuses to delete entries" "[ \$(code -X DELETE $api/api/v1/spire/entries) = 403 ]"
check TM-T6 "Tornjak's API refuses to ban laptops" "[ \$(code -X POST $api/api/v1/spire/agents/ban) = 403 ]"
origin=$(curl -s -D - -o /dev/null -m 5 -H 'Origin: https://attacker.example' "$api/api/v1/spire/entries" | tr -d '\r' | awk -F': ' 'tolower($1)=="access-control-allow-origin"{print $2}')
[ "$origin" = "http://localhost:${TORNJAK_PORT:-3001}" ] && pass "other web pages cannot read Tornjak's answers [TM-T6]" \
  || fail "other web pages cannot read Tornjak's answers [TM-T6]" "Access-Control-Allow-Origin: $origin"
check TM-T6 "the Tornjak backend has no port on the host" "! ws port tornjak-backend 10000"

# ---------------------------------------------------------------------------
section "Broker (STRIDE: tampering, information disclosure)"
check TM-T2 "the credential service cannot change its rules" \
  "[ \"\$(docker inspect \$(ws ps -q credential-service) --format '{{range .Mounts}}{{if eq .Destination \"/rules\"}}{{.RW}}{{end}}{{end}}')\" = false ]"
known TM-T2 "the broker accepts rules that have no signature" \
  "$(python3 -c 'import json,sys;print(0 if "signature" in json.load(open(sys.argv[1])) else 1)' .state/broker/rules.json 2>/dev/null || echo 1)"
check TM-T7 "the broker's admin API is read-only" \
  "[ \$(code -X PUT -H 'content-type: application/json' -d '{\"resources\":[]}' http://localhost:${BROKER_ADMIN_PORT:-15000}/api/config/resources/mcp.target) = 403 ]"
# Count token-like strings only; never print them.
tokens=$( { ws logs --no-color broker credential-service; docker logs "$(docker ps -q --filter "label=com.docker.compose.project=$project" --filter label=com.docker.compose.service=launcher)" ; } 2>&1 \
  | grep -cE 'eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}|gh[sopu]_[A-Za-z0-9]{20,}|sk-ant-[A-Za-z0-9_-]{10,}|mat_[A-Za-z0-9]{10,}|mul_[A-Za-z0-9]{10,}')
[ "$tokens" = 0 ] && pass "no tokens in the broker, credential service or launcher logs [TM-I4]" \
  || fail "no tokens in the broker, credential service or launcher logs [TM-I4]" "$tokens lines look like tokens"

# ---------------------------------------------------------------------------
section "Stop switch (STRIDE: repudiation)"
cs stop smoke-threat jane "threat test" >/dev/null && cs start smoke-threat max >/dev/null
history=$(cs history smoke-threat 2>&1 | tail -2)
[[ "$history" == *$'\tstop\tjane\tthreat test'* && "$history" == *$'\tstart\tmax\t'* ]] \
  && pass "the history shows who stopped the agent and who started it again [TM-R1]" \
  || fail "the history shows who stopped the agent and who started it again [TM-R1]" "$history"
check TM-R1 "nobody can start an agent without a name" "! cs start smoke-threat"
check TM-R1 "nobody can change the history" \
  "! ws exec -T postgres psql -U workspace -d broker -v ON_ERROR_STOP=1 -c \"update agent_stop_events set by = 'x' where agent = 'smoke-threat'\""

# ---------------------------------------------------------------------------
section "Runs (STRIDE: information disclosure)"
out=$(docker run --rm --network workspace-edge --entrypoint curl claudebox -s -o /dev/null -w '%{http_code}' -m 5 https://example.com 2>/dev/null)
known TM-I2 "a run can connect to any address on the internet" "$([ "$out" = 200 ] && echo 1 || echo 0)"
# The launcher passes the sponsor's token to each run as a variable (docker run
# -e, pinned by the launcher's tests), where printenv shows it. Never print it.
launcher=$(docker ps -q --filter "label=com.docker.compose.project=$project" --filter label=com.docker.compose.service=launcher)
has_token=$(docker inspect "$launcher" --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | grep -c '^CLAUDE_CODE_OAUTH_TOKEN=.')
known TM-I1 "commands in a run can read the sponsor's Claude token" "$([ "${has_token:-0}" -gt 0 ] && echo 1 || echo 0)"

# ---------------------------------------------------------------------------
section "This computer (LINDDUN: data disclosure)"
if command -v fdesetup >/dev/null; then
  check TM-DD2 "the disk is encrypted (FileVault)" "fdesetup status | grep -q 'FileVault is On'"
else
  skip "the disk is encrypted [TM-DD2]" "check BitLocker or LUKS yourself"
fi

# ---------------------------------------------------------------------------
section "This repository (STRIDE: elevation of privilege)"
check TM-E3 "no workflow runs agents' pull requests with secrets (pull_request_target, workflow_run)" \
  "! grep -rqE 'pull_request_target|workflow_run' ../.github/workflows"
check TM-E4 "a person owns CODEOWNERS and the workflows" "grep -qE '^/\\.github/[[:space:]]+@' ../.github/CODEOWNERS"

echo
echo "$PASS passed, $FAIL failed, $KNOWN known threats, $SKIP skipped"
[ "$FIXED" -gt 0 ] && echo "$FIXED known threat(s) now pass: update docs/threat-model.md"
[ "$FAIL" -eq 0 ]
