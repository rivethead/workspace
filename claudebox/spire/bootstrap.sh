#!/usr/bin/env bash
# One-time SPIRE setup for claudebox: start the server, join the agent with a fresh
# token, and register one SPIFFE ID per compose service. Safe to re-run.
set -euo pipefail
cd "$(dirname "$0")/.."

TD=claudebox.local
NODE_ID="spiffe://$TD/node/docker-desktop"
srv() { docker compose exec -T spire-server /opt/spire/bin/spire-server "$@"; }

docker compose up -d spire-server
for i in $(seq 60); do srv healthcheck >/dev/null 2>&1 && break; sleep 1; done
srv healthcheck >/dev/null || { docker compose logs --tail 20 spire-server; exit 1; }

# The agent reuses the SVID in its spire-agent-data volume. Only when it can't (first run,
# or that volume was wiped) join it again with a fresh one-time token.
agent_ok() { docker compose exec -T spire-agent /opt/spire/bin/spire-agent healthcheck -socketPath /run/spire/sockets/agent.sock >/dev/null 2>&1; }
docker compose up -d spire-agent
sleep 5
if ! agent_ok; then
    token=$(srv token generate -spiffeID "$NODE_ID" -ttl 600 | awk '/Token:/{print $2}')
    SPIRE_JOIN_TOKEN="$token" docker compose up -d --force-recreate spire-agent
    for i in $(seq 30); do agent_ok && break; sleep 1; done
fi
agent_ok || { docker compose logs --tail 20 spire-agent; exit 1; }
echo "spire-agent: healthy" 

# Workload identities: the docker attestor matches the compose service label.
for svc in claudebox claudebox-server multica-daemon; do
    id="spiffe://$TD/claudebox/$svc"
    if srv entry show -spiffeID "$id" 2>/dev/null | grep -q "Entry ID"; then
        echo "exists: $id"
    else
        srv entry create -parentID "$NODE_ID" -spiffeID "$id" \
            -selector "docker:label:com.docker.compose.project:claudebox" \
            -selector "docker:label:com.docker.compose.service:$svc" >/dev/null
        echo "created: $id"
    fi
done
