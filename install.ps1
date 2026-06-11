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

$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($userPath -notlike "*$dir*") {
    Write-Host "hint: add $dir to your PATH:"
    Write-Host "  [Environment]::SetEnvironmentVariable('PATH', `"$dir;`$env:PATH`", 'User')"
}
