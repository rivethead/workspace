# Run Claude in ClaudeBox on a project folder.
#
#   scripts\use.ps1                          interactive session on the current folder
#   scripts\use.ps1 C:\Code\my-project       interactive session on that folder
#   scripts\use.ps1 C:\Code\my-project -p "summarise this repo"
#   scripts\use.ps1 . bash                   shell inside the box
#
# Anything after the folder is passed to Claude (or run as a command, like `bash`).
# Only that folder is visible to Claude, mounted at /workspace.
. (Join-Path $PSScriptRoot 'common.ps1')

$rest = @($args)
$target = (Get-Location).Path
if ($rest.Count -gt 0 -and (Test-Path -PathType Container $rest[0])) {
    $target = (Resolve-Path $rest[0]).Path
    $rest = if ($rest.Count -gt 1) { $rest[1..($rest.Count - 1)] } else { @() }
}

if (-not (Test-ImageBuilt)) { Write-Error "Image not built yet: run scripts\start.ps1 or docker compose build"; exit 1 }

Push-Location $target
try { & (Join-Path $Repo 'bin\claudebox.ps1') @rest; $code = $LASTEXITCODE } finally { Pop-Location }
exit $code
