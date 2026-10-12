#!/usr/bin/env bash
# Delete the whole local workspace: every simulated laptop and its runs, the
# workspace services and their data, and local/.state and local/.env. After
# this, the machine is as it was before onboarding step 6 (or the tutorial).
# The Docker images stay, so the next start is fast. Nothing outside local/
# and the workspace's Docker projects is touched.
#
#   scripts/reset.sh             ask, then delete
#   scripts/reset.sh --yes       delete without asking
#   scripts/reset.sh --dry-run   show what would be deleted
set -euo pipefail
mode="${1:-ask}"
case "$mode" in ask|--yes|--dry-run) ;; *) sed -n '2,10p' "$0" >&2; exit 1 ;; esac
cd "$(dirname "$0")/.."

# The laptops: from their files, and from Docker (in case the files are gone).
projects=$( {
  for d in .state/laptops/*/; do [ -d "$d" ] && b=$(basename "$d") && echo "laptop-${b//./-}"; done
  docker ps -a --filter "label=com.docker.compose.project" --format '{{.Label "com.docker.compose.project"}}' | grep '^laptop-' || true
  docker volume ls -q | sed -n 's/^\(laptop-[a-z0-9-]*\)_.*/\1/p'
} | sort -u)

containers=$( {
  docker ps -aq --filter "label=com.docker.compose.project=workspace-local"
  docker ps -aq --filter "label=workspace.laptop"
  for p in $projects; do docker ps -aq --filter "label=com.docker.compose.project=$p"; done
} | sort -u)
volumes=$( {
  docker volume ls -q | grep '^workspace-local_' || true
  for p in $projects; do docker volume ls -q | grep "^${p}_" || true; done
} | sort -u)
networks=$( {
  docker network ls --format '{{.Name}}' | grep -E '^(workspace-local|workspace-edge)$' || true
  for p in $projects; do docker network ls --format '{{.Name}}' | grep "^${p}_" || true; done
} | sort -u)
files=$(ls -d .state .env 2>/dev/null || true)

echo "This deletes the local workspace:"
echo "  laptops:    ${projects:-none}" | tr '\n' ' '; echo
echo "  containers: $(echo "$containers" | grep -c . || true)"
echo "  volumes:    $(echo "$volumes" | grep -c . || true) (databases, SPIRE, agent sessions, work folders)"
echo "  networks:   $(echo "$networks" | tr '\n' ' ')"
echo "  files:      $(echo "$files" | sed 's|^|local/|' | tr '\n' ' ')(certificates, tokens, rules, Claude token)"
if ls .state/github/*.pem >/dev/null 2>&1; then
  echo "  note: agents' GitHub App keys are in local/.state/github. Delete those Apps on GitHub too."
fi

if [ "$mode" = --dry-run ]; then
  [ -z "$volumes" ] || { echo "volumes:"; echo "$volumes" | sed 's/^/  /'; }
  exit 0
fi
if [ "$mode" = ask ]; then
  printf 'Type "reset" to delete all of this: '
  read -r answer
  [ "$answer" = reset ] || { echo "nothing deleted"; exit 1; }
fi

echo "$containers" | xargs -r docker rm -f >/dev/null
echo "$volumes" | xargs -r docker volume rm >/dev/null
echo "$networks" | xargs -r docker network rm >/dev/null
rm -rf .state .env
echo "deleted. Start again with onboarding step 6 or the tutorial's lesson 2."
