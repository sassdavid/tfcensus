# Loads the tfcensus PowerShell completion script and completes `tfcensus --format ` with
# TabExpansion2. Fails unless json is offered. CI runs it in pwsh on every OS and in Windows
# PowerShell 5.1 (engineering ADR-0013); tfcensus must be on PATH.
#   pwsh -NoProfile -File scripts/completion-shells.ps1
$ErrorActionPreference = 'Stop'
$tool = 'tfcensus'
$script = Join-Path ([System.IO.Path]::GetTempPath()) "$tool-completion.ps1"
& $tool completion powershell | Out-File -FilePath $script -Encoding utf8
. $script
$line = "$tool --format "
$result = TabExpansion2 -inputScript $line -cursorColumn $line.Length
# Outside MenuComplete, cobra's script appends the description to each CompletionText.
$texts = @($result.CompletionMatches | ForEach-Object { ($_.CompletionText -split '\s+')[0] })
Write-Host "PowerShell $($PSVersionTable.PSVersion): $($texts -join ', ')"
Remove-Item $script
if ($texts -notcontains 'json') {
    Write-Error "completing '$line' did not offer json"
    exit 1
}
Write-Host 'powershell: ok'
