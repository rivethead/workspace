# Show what's running and whether each part is healthy.
#
#   scripts\status.ps1
. (Join-Path $PSScriptRoot 'common.ps1')
Set-Location $Repo
function Line($Name, $Value) { Write-Host ("  {0,-18} {1}" -f $Name, $Value) }

$port = Get-EnvValue CLAUDEBOX_PORT; if (-not $port) { $port = '3210' }

Write-Host "ClaudeBox"
if (Test-ImageBuilt) {
    $claude = docker run --rm --entrypoint claude claudebox --version 2>$null | Select-Object -First 1
    $appa = docker run --rm --entrypoint appa claudebox --version 2>$null
    Line "image" "$claude, $appa"
} else {
    Line "image" "not built (scripts\start.ps1 builds it)"
}

if (Test-ServiceRunning claudebox-server) {
    try { $health = Invoke-RestMethod "http://localhost:$port/health" -TimeoutSec 3 | ConvertTo-Json -Compress } catch { $health = 'not answering' }
    $logs = docker compose logs claudebox-server 2>&1 | Out-String
    $appa = if ($logs -match 'openappa: protected') { 'OpenAPPA on' } else { 'OpenAPPA line missing' }
    Line "API server" "http://localhost:$port  $health  ($appa)"
} else {
    Line "API server" "stopped"
}

if (Test-ServiceRunning multica-daemon) {
    $st = docker compose exec -T multica-daemon multica daemon status 2>$null | Select-String '^Daemon:' | ForEach-Object { ($_ -split ':\s*', 2)[1] }
    Line "Multica daemon" "$st  (server: $(Get-EnvValue MULTICA_SERVER_URL))"
} else {
    Line "Multica daemon" "stopped (optional: scripts\start.ps1 -Multica)"
}

if (Test-ServiceRunning spire-agent) {
    Line "SPIRE" "running, trust domain claudebox.local"
} else {
    Line "SPIRE" "stopped (optional: scripts\start.ps1 -Spire)"
}

Write-Host ""
Write-Host 'Use it:  scripts\session.ps1 [folder]       interactive Claude on a folder (default: current)'
Write-Host '         scripts\use.ps1 [folder] -p "..."   one-shot prompt'
Write-Host '         scripts\use.ps1 [folder] bash       shell inside the box'
