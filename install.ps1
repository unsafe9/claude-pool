# Asset name/URL follow the contract in internal/pool/release.go (.goreleaser.yaml).
$ErrorActionPreference = "Stop"

switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $arch = "amd64" }
    "ARM64" { $arch = "arm64" }
    default  { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

$dir = "$env:USERPROFILE\.local\bin"
$exe = "$dir\claude-pool.exe"
$tmp = "$dir\claude-pool.exe.tmp"
$url = "https://github.com/unsafe9/claude-pool/releases/latest/download/claude-pool-windows-$arch.exe"

New-Item -ItemType Directory -Force -Path $dir | Out-Null
Invoke-WebRequest -Uri $url -OutFile $tmp
Move-Item -Force $tmp $exe

& $exe version

# Unlike unix, $dir is not conventionally on PATH on Windows, and the exec-form
# hooks resolve claude-pool.exe through the PATH Claude Code inherited — so add
# it to the user PATH (registry; broadcasts the change to new terminals).
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($userPath -notlike "*$dir*") {
    $newPath = if ([string]::IsNullOrEmpty($userPath)) { $dir } else { "$userPath;$dir" }
    [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
    Write-Host "added $dir to your user PATH (open a new terminal to pick it up)"
}
