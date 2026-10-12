#!/usr/bin/env bash
# Start ClaudeBox's background services.
#
#   scripts/start.sh               API server (claudebox-server)
#   scripts/start.sh --multica     plus the Multica daemon (needs MULTICA_TOKEN in .env)
#   scripts/start.sh --spire       plus SPIFFE identities (SPIRE server and agent)
#   scripts/start.sh --all         all of the above
#   scripts/start.sh --build       rebuild the image first (combine with any of the above)
#
# Interactive Claude sessions don't need anything started: use scripts/use.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

MULTICA=0 SPIRE=0 BUILD=0
for arg in "$@"; do
    case "$arg" in
        --multica) MULTICA=1 ;;
        --spire)   SPIRE=1 ;;
        --all)     MULTICA=1; SPIRE=1 ;;
        --build)   BUILD=1 ;;
        -h|--help) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown option: $arg (try --help)" >&2; exit 2 ;;
    esac
done

env_value() { grep -E "^$1=" .env 2>/dev/null | tail -1 | cut -d= -f2-; }

[ -f .env ] || { echo "No .env yet: cp .env.example .env, then set your token (see ONBOARDING.md)" >&2; exit 1; }
[ -n "$(env_value CLAUDE_CODE_OAUTH_TOKEN)$(env_value ANTHROPIC_API_KEY)" ] \
    || { echo "Set CLAUDE_CODE_OAUTH_TOKEN (from 'claude setup-token') in .env" >&2; exit 1; }
if [ $MULTICA -eq 1 ] && [ -z "$(env_value MULTICA_TOKEN)" ]; then
    echo "--multica needs MULTICA_TOKEN in .env" >&2; exit 1
fi

if [ $BUILD -eq 1 ] || ! docker image inspect claudebox >/dev/null 2>&1; then
    echo "Building the image..."
    docker compose build claudebox
fi

# SPIRE first, so the other services fetch their SVIDs when they start.
if [ $SPIRE -eq 1 ]; then
    echo "Starting SPIRE..."
    ./spire/bootstrap.sh 2>&1 | grep -vE '^ *(Container|Network|Volume) ' || true
fi

services=(claudebox-server)
[ $MULTICA -eq 1 ] && services+=(multica-daemon)
echo "Starting ${services[*]}..."
docker compose up -d "${services[@]}" 2>&1 | grep -vE '^ *(Container|Volume|Network) ' || true

PORT=$(env_value CLAUDEBOX_PORT); PORT=${PORT:-3210}
for _ in $(seq 30); do curl -fsS "localhost:$PORT/health" >/dev/null 2>&1 && break; sleep 1; done

echo
exec "$(dirname "$0")/status.sh"
