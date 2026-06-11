# First-run bootstrap (Windows): when no claude-pool.exe is installed yet,
# fetch it in the background via the bundled installer (active next session).
# Idempotent: exits immediately once installed.
if (Get-Command claude-pool -ErrorAction SilentlyContinue) { exit 0 }
if (Test-Path "$env:USERPROFILE\.local\bin\claude-pool.exe") { exit 0 }
[Console]::Error.WriteLine("claude-pool: installing the binary in the background (active next session)")
Start-Process -WindowStyle Hidden powershell.exe -ArgumentList @(
    "-NoProfile", "-ExecutionPolicy", "Bypass",
    "-File", "$env:CLAUDE_PLUGIN_ROOT\install.ps1"
) | Out-Null
exit 0
