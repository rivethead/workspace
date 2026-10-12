# Start ClaudeBox's background services.
#
#   scripts\start.ps1               API server (claudebox-server)
#   scripts\start.ps1 -Multica      plus the Multica daemon (needs MULTICA_TOKEN in .env)
#   scripts\start.ps1 -Spire        plus SPIFFE identities (needs Git Bash for spire/bootstrap.sh)
#   scripts\start.ps1 -All          all of the above
#   scripts\start.ps1 -Build        rebuild the image first (combine with any of the above)
#
# Interactive Claude sessions don't need anything started: use scripts\use.ps1.
param([switch]$Multica, [switch]$Spire, [switch]$All, [switch]$Build)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'common.ps1')
Set-Location $Repo
if ($All) { $Multica = $true; $Spire = $true }

if (-not (Test-Path .env)) { Write-Error "No .env yet: Copy-Item .env.example .env, then set your token (see ONBOARDING.md)" }
if (-not ((Get-EnvValue CLAUDE_CODE_OAUTH_TOKEN) + (Get-EnvValue ANTHROPIC_API_KEY))) {
    Write-Error "Set CLAUDE_CODE_OAUTH_TOKEN (from 'claude setup-token') in .env"
}
if ($Multica -and -not (Get-EnvValue MULTICA_TOKEN)) { Write-Error "-Multica needs MULTICA_TOKEN in .env" }

if ($Build -or -not (Test-ImageBuilt)) {
    Write-Host "Building the image..."
    docker compose build claudebox
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

# SPIRE first, so the other services fetch their SVIDs when they start.
if ($Spire) {
    $bash = Get-Command bash -ErrorAction SilentlyContinue
    if (-not $bash) { Write-Error "-Spire runs spire/bootstrap.sh and needs bash (install Git for Windows)" }
    Write-Host "Starting SPIRE..."
    & bash ./spire/bootstrap.sh
}

$services = @('claudebox-server')
if ($Multica) { $services += 'multica-daemon' }
Write-Host "Starting $($services -join ', ')..."
docker compose up -d @services
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$port = Get-EnvValue CLAUDEBOX_PORT; if (-not $port) { $port = '3210' }
for ($i = 0; $i -lt 30; $i++) {
    try { Invoke-RestMethod "http://localhost:$port/health" -TimeoutSec 2 | Out-Null; break } catch { Start-Sleep 1 }
}

Write-Host ""
& (Join-Path $PSScriptRoot 'status.ps1')
