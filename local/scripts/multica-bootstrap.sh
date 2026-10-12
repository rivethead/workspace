#!/usr/bin/env bash
# Create the local Multica workspace and, for each laptop, its runner account
# (docs/plan.md: "the daemon logs in as a runner account for that laptop, not as
# the person"). Safe to re-run.
#
#   scripts/multica-bootstrap.sh workspace              the workspace, owned by $SPONSOR_EMAIL
#   scripts/multica-bootstrap.sh runner <person>.<machine>  runner account + token for one laptop
#   scripts/multica-bootstrap.sh remove-runner <person>.<machine>  undo `runner`
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
[ -n "${SPONSOR_EMAIL:-}" ] || { echo "set SPONSOR_EMAIL in local/.env to your own @example.com address" >&2; exit 1; }
SLUG=example
state=.state/multica.env

owner_token() { MC_TOKEN=$(multica_session "$SPONSOR_EMAIL"); export MC_TOKEN; }
workspace_id() {
  mc GET /api/workspaces/ | python3 -c "import sys,json;print(next((w['id'] for w in json.load(sys.stdin) if w['slug']=='$SLUG'),''))"
}

case "${1:-}" in
  workspace)
    owner_token
    id=$(workspace_id)
    if [ -z "$id" ]; then
      id=$(mc POST /api/workspaces/ '{"name":"Example (local)","slug":"'$SLUG'","issue_prefix":"WS"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['id'])")
      echo "created workspace $SLUG ($id)"
    else
      echo "workspace exists: $SLUG ($id)"
    fi
    printf 'MULTICA_WORKSPACE_ID=%s\n' "$id" > "$state"
    ;;
  runner)
    cn="${2:?usage: multica-bootstrap.sh runner <person>.<machine>}"
    dir=.state/laptops/$cn; mkdir -p "$dir"
    if [ -s "$dir/multica-token" ]; then echo "runner exists for $cn"; exit 0; fi
    email="runner-${cn//./-}@example.com"
    owner_token; ws=$(workspace_id); [ -n "$ws" ] || { echo "run: multica-bootstrap.sh workspace" >&2; exit 1; }
    mc POST "/api/workspaces/$ws/members" '{"email":"'"$email"'","role":"member"}' >/dev/null 2>&1 || true
    MC_TOKEN=$(multica_session "$email"); export MC_TOKEN
    for inv in $(mc GET /api/invitations | python3 -c "import sys,json;[print(i['id']) for i in json.load(sys.stdin)]"); do
      mc POST "/api/invitations/$inv/accept" >/dev/null
    done
    mc POST /api/tokens/ '{"name":"daemon '"$cn"'","expires_in_days":90}' \
      | python3 -c "import sys,json;print(json.load(sys.stdin)['token'])" > "$dir/multica-token"
    chmod 600 "$dir/multica-token"
    echo "runner $email joined $SLUG; token in $dir/multica-token"
    ;;
  remove-runner)
    # Undo `runner`: revoke the runner's token, delete the laptop's runtime
    # (its agents stay, without a runtime) and take the runner out of the
    # workspace. Multica cannot delete the user itself; without a workspace or
    # a token it can do nothing.
    cn="${2:?usage: multica-bootstrap.sh remove-runner <person>.<machine>}"
    email="runner-${cn//./-}@example.com"
    token_file=.state/laptops/$cn/multica-token
    if [ -s "$token_file" ]; then
      MC_TOKEN=$(cat "$token_file") MULTICA_WORKSPACE_ID='' mc GET /api/tokens/ 2>/dev/null \
        | python3 -c "import sys,json;d=json.load(sys.stdin);d=d.get('tokens',d) if isinstance(d,dict) else d;[print(t['id']) for t in d]" \
        | while read -r t; do MC_TOKEN=$(cat "$token_file") mc DELETE "/api/tokens/$t" >/dev/null && echo "revoked runner token $t"; done
    fi
    owner_token; ws=$(workspace_id); [ -n "$ws" ] || { echo "no workspace $SLUG"; exit 0; }
    export MULTICA_WORKSPACE_ID=$ws
    for rt in $(mc GET "/api/runtimes/?workspace_id=$ws" | python3 -c "import sys,json;[print(r['id']) for r in json.load(sys.stdin) if r.get('daemon_id')=='$cn']"); do
      # Multica wants the agents it will unbind named, so it never unbinds more.
      bound=$(mc GET /api/agents/ | python3 -c "import sys,json;d=json.load(sys.stdin);d=d.get('agents',d) if isinstance(d,dict) else d;print(json.dumps({'expected_active_agent_ids':[a['id'] for a in d if a.get('runtime_id')=='$rt']}))")
      mc POST "/api/runtimes/$rt/unbind-agents-and-delete" "$bound" >/dev/null && echo "deleted runtime $rt"
    done
    member=$(mc GET "/api/workspaces/$ws/members" | python3 -c "import sys,json;d=json.load(sys.stdin);d=d.get('members',d) if isinstance(d,dict) else d;print(next((m['id'] for m in d if (m.get('email') or m.get('user',{}).get('email'))=='$email'),''))")
    if [ -n "$member" ]; then mc DELETE "/api/workspaces/$ws/members/$member/" >/dev/null && echo "removed $email from $SLUG"; fi
    ;;
  *) sed -n '2,8p' "$0" >&2; exit 1 ;;
esac
