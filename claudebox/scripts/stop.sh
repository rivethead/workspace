#!/usr/bin/env bash
# Stop all ClaudeBox services (API server, Multica daemon, SPIRE).
# Volumes are kept: your settings, OpenAPPA policy, Multica login and SPIRE identities survive.
#
#   scripts/stop.sh
#
# To also delete all of that, run: docker compose --profile multica down -v
set -euo pipefail
cd "$(dirname "$0")/.."

docker compose --profile multica down 2>&1 | grep -vE '^ *(Container|Network) ' || true
echo "ClaudeBox stopped. Volumes kept."
