# Asset name/URL follow the contract in internal/pool/release.go (.goreleaser.yaml).
$ErrorActionPreference = "Stop"

switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $arch = "amd64" }
    "ARM64" { $arch = "arm64" }
    default  { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

$dir = "$env:USERPROFILE\.local\bin"
$exe = "$dir\claude-pool.exe"
$url = "https://github.com/unsafe9/claude-pool/releases/latest/download/claude-pool-windows-$arch.exe"

# Skip the download when a working binary is already in place — re-runs (e.g.
# the bootstrap retrying while the PATH change below is not effective yet)
# then only repair the PATH.
$working = $false
if (Test-Path $exe) {
    try { & $exe version | Out-Null; $working = ($LASTEXITCODE -eq 0) } catch {}
}
if (-not $working) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    # Unique temp per run (concurrent session starts may race); .exe extension
    # so the binary can be executed for validation before being committed.
    $tmp = "$dir\claude-pool.$PID.new.exe"
    try {
        Invoke-WebRequest -Uri $url -OutFile $tmp
        # Validate before committing: a captive portal / proxy can hand back
        # HTML with HTTP 200, and a broken exe at the final path would be
        # treated as installed forever.
        & $tmp version | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "downloaded binary failed its self-check" }
        Move-Item -Force $tmp $exe
    } finally {
        Remove-Item -Force -ErrorAction SilentlyContinue $tmp
    }
}

& $exe version

# Unlike unix, $dir is not conventionally on PATH on Windows, and the exec-form
# hooks resolve claude-pool.exe through the PATH Claude Code inherited — append
# it to the user PATH. Raw registry access instead of
# [Environment]::SetEnvironmentVariable, which would rewrite a REG_EXPAND_SZ
# Path as REG_SZ and freeze %VAR% entries (dotnet/runtime#89695).
$envKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
try {
    $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
    $userPath = ""
    if ($envKey.GetValueNames() -contains "Path") {
        $kind = $envKey.GetValueKind("Path")
        $userPath = [string]$envKey.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    }
    if (-not (($userPath -split ';') -contains $dir)) {
        $newPath = if ([string]::IsNullOrEmpty($userPath)) { $dir } else { "$userPath;$dir" }
        $envKey.SetValue("Path", $newPath, $kind)
        # Broadcast WM_SETTINGCHANGE (as [Environment]:: would have) so
        # terminals opened from Explorer pick the change up without logoff.
        $sig = '[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)] public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);'
        $user32 = Add-Type -MemberDefinition $sig -Name SendMsg -Namespace ClaudePool -PassThru
        [UIntPtr]$result = [UIntPtr]::Zero
        $user32::SendMessageTimeout([IntPtr]0xFFFF, 0x1A, [UIntPtr]::Zero, "Environment", 2, 1000, [ref]$result) | Out-Null
        Write-Host "added $dir to your user PATH (open a new terminal to pick it up)"
    }
} finally {
    $envKey.Close()
}
