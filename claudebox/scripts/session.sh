#!/usr/bin/env bash
# Start an interactive Claude session inside ClaudeBox, under OpenAPPA.
#
#   scripts/session.sh                       on the current folder
#   scripts/session.sh ~/code/my-project     on that folder
#   scripts/session.sh . --model opus        extra arguments go to Claude
#   scripts/session.sh . --continue          pick up the most recent session
#
# Only the chosen folder is visible to Claude, mounted at /workspace. For one-shot prompts
# (-p) or a shell, use scripts/use.sh.
#
# Every project is mounted at the same path (/workspace) and sessions are stored in one shared
# volume, so --continue resumes the most recent session from any project. Use --resume to pick.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"

if [ $# -gt 0 ] && [ -d "$1" ]; then
    target="$(cd "$1" && pwd)"; shift
else
    target="$PWD"
fi

for arg in "$@"; do
    case "$arg" in
        -p|--print) echo "session.sh is for interactive sessions; use scripts/use.sh for -p" >&2; exit 2 ;;
    esac
done

[ -t 0 ] && [ -t 1 ] || { echo "session.sh needs an interactive terminal" >&2; exit 2; }
docker image inspect claudebox >/dev/null 2>&1 \
    || { echo "Image not built yet: run scripts/start.sh or docker compose build" >&2; exit 1; }

echo "ClaudeBox session on $target (mounted at /workspace). Type /exit to leave."
cd "$target"
exec "$here/bin/claudebox" "$@"
