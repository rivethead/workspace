#!/usr/bin/env bash
# Run one sponsor's laptop on this machine: ClaudeBox with a SPIRE agent attested
# by its machine certificate and a Multica daemon signed in as its runner account.
#
#   scripts/laptop.sh up <person>.<machine>      enrol (first time) and start
#   scripts/laptop.sh down <person>.<machine>    stop (keeps the enrolment; `up` starts it again)
#   scripts/laptop.sh remove <person>.<machine>  undo `up`: containers, volumes, SPIRE, Multica, files
#   scripts/laptop.sh agent <person>.<machine> <agent>    register an agent's runs on this laptop
#   scripts/laptop.sh compose <person>.<machine> <args>   any docker compose command
#
# The sponsor's Claude Team seat token goes in .state/laptops/<cn>/claude.env as
# CLAUDE_CODE_OAUTH_TOKEN=... (from `claude setup-token`). It never leaves the laptop.
set -euo pipefail
cd "$(dirname "$0")/.."
here=$PWD
cmd="${1:-}"; cn="${2:-}"
[[ "$cn" =~ ^[a-z0-9-]+\.[a-z0-9-]+$ ]] || { sed -n '2,11p' "$0" >&2; exit 1; }
shift 2 || true
dir=$here/.state/laptops/$cn
project=laptop-${cn//./-}
TD=workspace.local

compose() {
  set -a
  # shellcheck disable=SC1091
  . "$here/.state/multica.env"
  [ -f "$dir/claude.env" ] && . "$dir/claude.env"
  MULTICA_TOKEN=$(cat "$dir/multica-token")
  LAPTOP_DIR=$dir LAPTOP_NAME=$cn COMPOSE_PROJECT_NAME=$project
  # Runs use the exact image the daemon runs, by ID (pinned, as in D44).
  LAUNCHER_IMAGE=$(docker image inspect claudebox --format '{{.Id}}' 2>/dev/null || echo claudebox)
  set +a
  docker compose -p "$project" -f "$here/../claudebox/docker-compose.yml" -f "$here/laptop/compose.yml" --profile multica "$@"
}
srv() { docker compose -f "$here/docker-compose.yml" exec -T spire-server /opt/spire/bin/spire-server "$@"; }

# entry <spiffe-id> <selector>...: create a registration entry under this laptop once.
entry() {
  local id="$1"; shift
  if srv entry show -spiffeID "$id" 2>/dev/null | grep -q "Entry ID"; then return; fi
  local sel=(); for s in "$@"; do sel+=(-selector "$s"); done
  srv entry create -parentID "spiffe://$TD/spire/agent/x509pop/$cn" -spiffeID "$id" "${sel[@]}" >/dev/null
  echo "registered $id"
}

case "$cmd" in
  up)
    mkdir -p "$dir"
    if [ ! -f "$dir/agent.pem" ]; then
      scripts/node-ca.sh issue "$cn" >/dev/null
      cp ".state/machines/$cn/agent.pem" ".state/machines/$cn/agent.key" "$dir/"
      echo "enrolled $cn (machine certificate, 90 days)"
    fi
    srv bundle show > "$dir/bundle.pem"
    sed "s/__CN__/$cn/" laptop/agent.conf.tmpl > "$dir/agent.conf"
    [ -f .state/multica.env ] || scripts/multica-bootstrap.sh workspace
    scripts/multica-bootstrap.sh runner "$cn"
    [ -f "$dir/claude.env" ] || echo "note: no $dir/claude.env yet; runs cannot call Claude until it exists" >&2
    # The launcher container holds the laptop's launcher identity (docs/plan.md, SPIRE).
    entry "spiffe://$TD/launcher/$cn" \
      "docker:label:com.docker.compose.project:$project" "docker:label:com.docker.compose.service:launcher"
    compose up -d spire-agent
    for _ in $(seq 30); do
      compose exec -T spire-agent /opt/spire/bin/spire-agent healthcheck -socketPath /run/spire/sockets/agent.sock >/dev/null 2>&1 && break
      sleep 1
    done
    compose build claudebox launcher >/dev/null
    compose up -d launcher multica-daemon
    echo "laptop $cn is up (compose project $project)"
    ;;
  agent)
    name="${1:?usage: laptop.sh agent <person>.<machine> <agent>}"
    # The launcher labels each run with the agent and the laptop; SPIRE gives
    # the run the agent's identity from those labels.
    entry "spiffe://$TD/agent/$name" "docker:label:workspace.agent:$name" "docker:label:workspace.laptop:$cn"
    ;;
  down) compose down ;;
  remove)
    # Everything `up` and `agent` made for this laptop, found by name and label,
    # so it also works when the laptop's files are already gone.
    # The daemon, launcher and SPIRE agent, then any runs (which have no compose labels).
    docker ps -aq --filter "label=com.docker.compose.project=$project" | xargs -r docker rm -f >/dev/null
    docker ps -aq --filter "label=workspace.laptop=$cn" | xargs -r docker rm -f >/dev/null
    docker volume ls -q | { grep "^${project}_" || true; } | xargs -r docker volume rm >/dev/null
    docker network ls -q --filter "label=com.docker.compose.project=$project" | xargs -r docker network rm >/dev/null
    echo "removed $project containers, volumes and networks"
    # SPIRE: the entries under this laptop, then the laptop itself.
    parent="spiffe://$TD/spire/agent/x509pop/$cn"
    for id in $(srv entry show -parentID "$parent" 2>/dev/null | awk '/^Entry ID/{print $4}'); do
      srv entry delete -entryID "$id" >/dev/null && echo "deleted SPIRE entry $id"
    done
    srv agent evict -spiffeID "$parent" >/dev/null 2>&1 && echo "evicted $parent" || true
    [ -f .state/multica.env ] && scripts/multica-bootstrap.sh remove-runner "$cn"
    rm -rf "$dir" ".state/machines/$cn" ".state/sessions/runner-${cn//./-}@example.com"
    echo "removed $cn's files"
    placed=$(python3 -c "import json,sys;print(' '.join(n for n,a in json.load(open(sys.argv[1]))['agents'].items() if a.get('placement')==sys.argv[2]))" .state/broker/rules.json "$cn" 2>/dev/null || true)
    [ -z "$placed" ] || echo "note: .state/broker/rules.json still places these agents on $cn: $placed (edit it by hand)"
    ;;
  compose) compose "$@" ;;
  *) sed -n '2,11p' "$0" >&2; exit 1 ;;
esac
