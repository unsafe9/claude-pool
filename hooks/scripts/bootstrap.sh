#!/bin/sh
# First-run bootstrap: when no claude-pool binary is installed yet, fetch it in
# the background via the bundled installer (active next session). Wired as a
# string-form SessionStart hook: cc runs it with /bin/sh on unix and with Git
# Bash on Windows (cc resolves bash from the Git install itself, no PATH
# dependence). Idempotent: exits immediately once installed.
set -u
command -v claude-pool >/dev/null 2>&1 && exit 0
[ -n "${CLAUDE_PLUGIN_ROOT:-}" ] || exit 0

case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*)
    # Windows under Git Bash: delegate to the bundled PowerShell installer.
    [ -e "$HOME/.local/bin/claude-pool.exe" ] && exit 0
    echo "claude-pool: installing the binary in the background (active next session)" >&2
    nohup powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$CLAUDE_PLUGIN_ROOT/install.ps1" >/dev/null 2>&1 &
    ;;
  *)
    [ -x "$HOME/.local/bin/claude-pool" ] && exit 0
    echo "claude-pool: installing the binary in the background (active next session)" >&2
    nohup sh "$CLAUDE_PLUGIN_ROOT/install.sh" >/dev/null 2>&1 &
    ;;
esac
exit 0
