#!/usr/bin/env bash
# Run inside a laptop container: call the broker with this container's JWT-SVID.
#   broker-call.sh <audience> <path> <json>
set -euo pipefail
aud="$1" path="$2" body="$3"
tok=$(spire-agent api fetch jwt -audience "$aud" -socketPath /run/spire/sockets/agent.sock | awk '/^token\(/{getline; print $1}')
curl -sS -X POST "http://broker:8443$path" -H "Authorization: Bearer $tok" -H 'content-type: application/json' -d "$body"
