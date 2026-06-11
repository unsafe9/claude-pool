#!/bin/sh
# Asset name/URL follow the contract in internal/pool/release.go (.goreleaser.yaml).
set -eu

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  MINGW*|MSYS*|CYGWIN*)
    # Git Bash on Windows: the binary is a native Windows exe — delegate to
    # the PowerShell installer (same release contract, plus user-PATH repair).
    exec powershell.exe -NoProfile -ExecutionPolicy Bypass -Command \
      "irm https://raw.githubusercontent.com/unsafe9/claude-pool/main/install.ps1 | iex"
    ;;
  *) echo "error: unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64)          arch=amd64 ;;
  arm64|aarch64)   arch=arm64 ;;
  *)               echo "error: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

url="https://github.com/unsafe9/claude-pool/releases/latest/download/claude-pool-${os}-${arch}"
dest="$HOME/.local/bin/claude-pool"
tmp="${dest}.tmp.$$"
trap 'rm -f "$tmp"' EXIT

mkdir -p "$HOME/.local/bin"
curl -fsSL "$url" -o "$tmp"
chmod +x "$tmp"
# Validate before committing: a captive portal / proxy can hand back HTML with
# HTTP 200, and a broken binary at the final path would be treated as
# installed forever.
"$tmp" version >/dev/null
mv "$tmp" "$dest"

"$dest" version

case ":$PATH:" in
  *":$HOME/.local/bin:"*) ;;
  *) echo "hint: add \$HOME/.local/bin to your PATH (e.g. export PATH=\"\$HOME/.local/bin:\$PATH\")" ;;
esac
