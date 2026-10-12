#!/usr/bin/env bash
# PostToolUse(Edit|Write) guard: format + lint the file the agent just touched.
#
# Formatting and safe lint fixes are a TOOL's job, not the LLM's. Auto-apply
# what a tool can fix; feed anything left back to the model via exit 2 so it
# fixes the residue before continuing.
#
# One branch per language in this repo: Go (orchestrator/, broker/,
# claudebox/launcher/), Terraform, shell (claudebox/, scripts) and Python (hooks). A branch
# whose tool is not installed is dormant, not blocking.
set -uo pipefail

f=$(jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -n "$f" ] && [ -f "$f" ] || exit 0

case "$f" in
  *.go)
    command -v gofmt >/dev/null || exit 0
    residue=$(gofmt -w "$f" 2>&1) || { echo "$residue" >&2; exit 2; }
    ;;
  *.tf|*.tfvars)
    command -v terraform >/dev/null || exit 0
    residue=$(terraform fmt -no-color "$f" 2>&1 >/dev/null) || { echo "$residue" >&2; exit 2; }
    ;;
  *.sh)
    command -v shellcheck >/dev/null || exit 0
    residue=$(shellcheck -f gcc "$f" 2>&1) || { echo "$residue" >&2; exit 2; }
    ;;
  *.py)
    command -v ruff >/dev/null || exit 0
    ruff format --force-exclude "$f" >/dev/null 2>&1
    ruff check --fix --force-exclude "$f" >/dev/null 2>&1
    residue=$(ruff check --force-exclude --output-format=concise "$f" 2>&1) || { echo "$residue" >&2; exit 2; }
    ;;
esac
exit 0
