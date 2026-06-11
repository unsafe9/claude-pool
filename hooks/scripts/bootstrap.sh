#!/bin/sh
# First-run bootstrap: when no claude-pool binary is installed yet, fetch it in
# the background via the bundled installer (active next session). Wired as an
# exec-form `sh` hook, so it never runs on Windows (no sh on PATH) —
# bootstrap.ps1 covers Windows. Idempotent: exits immediately once installed.
set -u
command -v claude-pool >/dev/null 2>&1 && exit 0
[ -x "$HOME/.local/bin/claude-pool" ] && exit 0
[ -n "${CLAUDE_PLUGIN_ROOT:-}" ] || exit 0
echo "claude-pool: installing the binary in the background (active next session)" >&2
nohup sh "$CLAUDE_PLUGIN_ROOT/install.sh" >/dev/null 2>&1 &
exit 0
