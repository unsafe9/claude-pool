#!/bin/sh
set -eu

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *)      echo "error: unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64)          arch=amd64 ;;
  arm64|aarch64)   arch=arm64 ;;
  *)               echo "error: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

url="https://github.com/unsafe9/claude-pool/releases/latest/download/claude-pool-${os}-${arch}"
dest="$HOME/.local/bin/claude-pool"
tmp="${dest}.tmp.$$"

mkdir -p "$HOME/.local/bin"
curl -fsSL "$url" -o "$tmp"
chmod +x "$tmp"
mv "$tmp" "$dest"

"$dest" version

case ":$PATH:" in
  *":$HOME/.local/bin:"*) ;;
  *) echo "hint: add \$HOME/.local/bin to your PATH (e.g. export PATH=\"\$HOME/.local/bin:\$PATH\")" ;;
esac
