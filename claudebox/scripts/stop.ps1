# Stop all ClaudeBox services (API server, Multica daemon, SPIRE).
# Volumes are kept: your settings, OpenAPPA policy, Multica login and SPIRE identities survive.
#
#   scripts\stop.ps1
#
# To also delete all of that, run: docker compose --profile multica down -v
. (Join-Path $PSScriptRoot 'common.ps1')
Set-Location $Repo
docker compose --profile multica down
Write-Host "ClaudeBox stopped. Volumes kept."
