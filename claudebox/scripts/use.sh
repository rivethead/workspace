#!/usr/bin/env bash
# Run Claude in ClaudeBox on a project folder.
#
#   scripts/use.sh                          interactive session on the current folder
#   scripts/use.sh ~/code/my-project        interactive session on that folder
#   scripts/use.sh ~/code/my-project -p "summarise this repo"
#   scripts/use.sh . bash                   shell inside the box
#
# Anything after the folder is passed to Claude (or run as a command, like `bash`).
# Only that folder is visible to Claude, mounted at /workspace.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"

if [ $# -gt 0 ] && [ -d "$1" ]; then
    target="$(cd "$1" && pwd)"; shift
else
    target="$PWD"
fi

docker image inspect claudebox >/dev/null 2>&1 \
    || { echo "Image not built yet: run scripts/start.sh or docker compose build" >&2; exit 1; }

cd "$target"
exec "$here/bin/claudebox" "$@"
