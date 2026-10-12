#!/usr/bin/env bash
# Create an agent in the local Multica workspace, bound to a laptop's runtime,
# and register its runs with SPIRE on that laptop. Until the provisioner exists
# (stage L5), this stands in for it. Safe to re-run.
#
#   scripts/agent.sh <agent> <person>.<machine> "<instructions>"
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
name="${1:?usage: agent.sh <agent> <person>.<machine> <instructions>}" cn="${2:?}" instructions="${3:-}"
set -a; . .state/multica.env; set +a
# Multica lets only a runtime's owner bind agents to it; the laptop's runner
# account owns the laptop's runtime, so it creates the agents placed there.
MC_TOKEN=$(multica_session "runner-${cn//./-}@example.com"); export MC_TOKEN

runtime=$(mc GET /api/runtimes/ | python3 -c "import sys,json;print(next((r['id'] for r in json.load(sys.stdin) if r.get('daemon_id')=='$cn' and r['provider']=='claude'),''))")
[ -n "$runtime" ] || { echo "no Claude runtime for $cn: is the laptop up?" >&2; exit 1; }
id=$(mc GET /api/agents/ | python3 -c "import sys,json;d=json.load(sys.stdin);d=d.get('agents',d) if isinstance(d,dict) else d;print(next((a['id'] for a in d if a['name']=='$name'),''))")
if [ -z "$id" ]; then
  body=$(python3 -c "import json,sys;print(json.dumps({'name':sys.argv[1],'instructions':sys.argv[2],'runtime_id':sys.argv[3],'visibility':'workspace'}))" "$name" "$instructions" "$runtime")
  id=$(mc POST /api/agents/ "$body" | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])")
  echo "created agent $name ($id) on runtime $runtime"
else
  echo "agent exists: $name ($id)"
fi
scripts/laptop.sh agent "$cn" "$name"
