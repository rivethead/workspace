# Start an interactive Claude session inside ClaudeBox, under OpenAPPA.
#
#   scripts\session.ps1                        on the current folder
#   scripts\session.ps1 C:\Code\my-project     on that folder
#   scripts\session.ps1 . --model opus         extra arguments go to Claude
#   scripts\session.ps1 . --continue           pick up the most recent session
#
# Only the chosen folder is visible to Claude, mounted at /workspace. For one-shot prompts
# (-p) or a shell, use scripts\use.ps1.
#
# Every project is mounted at the same path (/workspace) and sessions are stored in one shared
# volume, so --continue resumes the most recent session from any project. Use --resume to pick.
. (Join-Path $PSScriptRoot 'common.ps1')

$rest = @($args)
$target = (Get-Location).Path
if ($rest.Count -gt 0 -and (Test-Path -PathType Container $rest[0])) {
    $target = (Resolve-Path $rest[0]).Path
    $rest = if ($rest.Count -gt 1) { $rest[1..($rest.Count - 1)] } else { @() }
}

if ($rest -contains '-p' -or $rest -contains '--print') {
    Write-Error "session.ps1 is for interactive sessions; use scripts\use.ps1 for -p"; exit 2
}
if ([Console]::IsInputRedirected) { Write-Error "session.ps1 needs an interactive terminal"; exit 2 }
if (-not (Test-ImageBuilt)) { Write-Error "Image not built yet: run scripts\start.ps1 or docker compose build"; exit 1 }

Write-Host "ClaudeBox session on $target (mounted at /workspace). Type /exit to leave."
Push-Location $target
try { & (Join-Path $Repo 'bin\claudebox.ps1') @rest; $code = $LASTEXITCODE } finally { Pop-Location }
exit $code
