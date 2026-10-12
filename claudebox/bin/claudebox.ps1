# Run ClaudeBox (Claude Code + OpenAPPA, sandboxed) on the current folder.
#   claudebox                      interactive session
#   claudebox -p "summarise this"  one-shot
#   claudebox bash                 shell inside the box
$repo = Split-Path -Parent $PSScriptRoot
$env:PROJECT_DIR = (Get-Location).Path -replace '\\', '/'
docker compose --project-directory $repo -f (Join-Path $repo 'docker-compose.yml') run --rm claudebox @args
exit $LASTEXITCODE
