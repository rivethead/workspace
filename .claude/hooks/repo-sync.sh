#!/usr/bin/env bash
# SessionStart hook — guarantee every agent task starts on the FRESHEST origin/main.
#
# The Multica daemon cuts a per-task git worktree from a shared repo cache, but it
# does NOT force that worktree to origin/main — so a task can run on a stale main
# (observed: worktrees pinned at an old commit while origin/main had advanced).
# This fetches the true latest from origin and hard-resets the worktree to it
# before the agent does anything (reads fleet/ config, branches off main, etc.).
#
# The reset is only safe on a clean main, which is the state a freshly cut agent
# worktree is in. A session can also start in a repo that already holds work — a
# human's checkout, or an onboarding run that installed this hook mid-session —
# and there the reset would destroy it, so the guard below bails first.
set -uo pipefail

root="$(git rev-parse --show-toplevel 2>/dev/null || echo "${CLAUDE_PROJECT_DIR:-.}")"
[ -d "$root/.git" ] || exit 0
cd "$root" || exit 0

# Never reset over existing work: a dirty tree or a non-main branch both mean
# someone is mid-task. `git clean -qfd` below also removes untracked files, so
# this guard covers scaffolding and downloads that were never staged.
branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")"
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  echo "repo-sync: skipped — uncommitted changes in the working tree"
  exit 0
fi
if [ "$branch" != "main" ]; then
  echo "repo-sync: skipped — on '$branch', not main"
  exit 0
fi

# Fetch the real latest from origin; bail quietly if offline (don't block the session).
git fetch --quiet origin main 2>/dev/null || { echo "repo-sync: fetch failed — proceeding on current checkout"; exit 0; }
git reset --hard -q origin/main 2>/dev/null || true
git clean -qfd 2>/dev/null || true
echo "repo-sync: on origin/main @ $(git rev-parse --short HEAD) ($(git log -1 --format=%s | cut -c1-60))"
