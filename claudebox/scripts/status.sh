#!/usr/bin/env bash
# Show what's running and whether each part is healthy.
#
#   scripts/status.sh
set -uo pipefail
cd "$(dirname "$0")/.."

env_value() { grep -E "^$1=" .env 2>/dev/null | tail -1 | cut -d= -f2-; }
running() { docker compose ps --status running -q "$1" 2>/dev/null | grep -q .; }
line() { printf "  %-18s %s\n" "$1" "$2"; }

PORT=$(env_value CLAUDEBOX_PORT); PORT=${PORT:-3210}

echo "ClaudeBox"
if docker image inspect claudebox >/dev/null 2>&1; then
    line "image" "$(docker run --rm --entrypoint claude claudebox --version 2>/dev/null | head -1), $(docker run --rm --entrypoint appa claudebox --version 2>/dev/null)"
else
    line "image" "not built (scripts/start.sh builds it)"
fi

if running claudebox-server; then
    health=$(curl -fsS -m 3 "localhost:$PORT/health" 2>/dev/null || echo "not answering")
    logs=$(docker compose logs claudebox-server 2>&1)
    appa=$(grep -q "openappa: protected" <<< "$logs" && echo "OpenAPPA on" || echo "OpenAPPA line missing")
    line "API server" "http://localhost:$PORT  $health  ($appa)"
else
    line "API server" "stopped"
fi

if running multica-daemon; then
    st=$(docker compose exec -T multica-daemon multica daemon status 2>/dev/null | awk -F': *' '/^Daemon:/{print $2}')
    line "Multica daemon" "${st:-unknown}  (server: $(env_value MULTICA_SERVER_URL))"
else
    line "Multica daemon" "stopped (optional: scripts/start.sh --multica)"
fi

if running spire-agent; then
    line "SPIRE" "running, trust domain claudebox.local"
else
    line "SPIRE" "stopped (optional: scripts/start.sh --spire)"
fi

echo
echo 'Use it:  scripts/session.sh [folder]        interactive Claude on a folder (default: current)'
echo '         scripts/use.sh [folder] -p "..."   one-shot prompt'
echo '         scripts/use.sh [folder] bash       shell inside the box'
