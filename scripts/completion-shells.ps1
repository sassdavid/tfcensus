# Loads the tfcensus PowerShell completion script and completes `tfcensus --format ` with
# TabExpansion2. Fails unless json is offered. CI runs it in pwsh on every OS and in Windows
# PowerShell 5.1 (engineering ADR-0013); tfcensus must be on PATH.
#   pwsh -NoProfile -File scripts/completion-shells.ps1
$ErrorActionPreference = 'Stop'
$tool = 'tfcensus'
$tmp = [System.IO.Path]::GetTempPath()
$script = Join-Path $tmp "$tool-completion.ps1"
$env:BASH_COMP_DEBUG_FILE = Join-Path $tmp "$tool-completion.log"
Remove-Item -ErrorAction Ignore $env:BASH_COMP_DEBUG_FILE
& $tool completion powershell | Out-File -FilePath $script -Encoding utf8
. $script
$line = "$tool --format "
# Windows PowerShell 5.1 turns the "Completion ended with directive" line that cobra writes to
# stderr into an error record, and under Stop that aborts the completer, so TabExpansion2 falls
# back to file names. Complete with the default preference, as a user's shell does.
$ErrorActionPreference = 'Continue'
$result = TabExpansion2 -inputScript $line -cursorColumn $line.Length
$ErrorActionPreference = 'Stop'
# Outside MenuComplete, cobra's script appends the description to each CompletionText.
$texts = @($result.CompletionMatches | ForEach-Object { ($_.CompletionText -split '\s+')[0] })
Write-Host "PowerShell $($PSVersionTable.PSVersion): $($texts -join ', ')"
Remove-Item $script
if ($texts -notcontains 'json') {
    if (Test-Path $env:BASH_COMP_DEBUG_FILE) {
        Get-Content $env:BASH_COMP_DEBUG_FILE | Write-Host
    }
    Write-Error "completing '$line' did not offer json"
    exit 1
}
Write-Host 'powershell: ok'
