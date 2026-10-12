# Shared helpers for the ClaudeBox PowerShell scripts.
$Repo = Split-Path -Parent $PSScriptRoot

function Get-EnvValue([string]$Name) {
    $file = Join-Path $Repo '.env'
    if (-not (Test-Path $file)) { return '' }
    $line = Get-Content $file | Where-Object { $_ -match "^$Name=" } | Select-Object -Last 1
    if ($line) { return ($line -split '=', 2)[1] } else { return '' }
}

function Test-ServiceRunning([string]$Service) {
    $id = docker compose --project-directory $Repo ps --status running -q $Service 2>$null
    return [bool]$id
}

function Test-ImageBuilt {
    docker image inspect claudebox *> $null
    return $LASTEXITCODE -eq 0
}
