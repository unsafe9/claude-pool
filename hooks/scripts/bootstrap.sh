#!/bin/sh
# First-run bootstrap: when claude-pool is not resolvable yet, run the bundled
# installer in the background. Wired as a string-form SessionStart hook: cc
# runs it with /bin/sh on unix and with Git Bash on Windows (cc resolves bash
# from the Git install itself, no PATH dependence). Idempotent: exits
# immediately once the binary is resolvable.
set -u
command -v claude-pool >/dev/null 2>&1 && exit 0
[ -n "${CLAUDE_PLUGIN_ROOT:-}" ] || exit 0

case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*)
    # claude-pool.exe is unresolvable: not installed, or installed but
    # ~/.local/bin is not on cc's PATH yet. install.ps1 handles both — it
    # skips the download when a working binary is in place and repairs the
    # user PATH (effective once a new terminal / cc starts).
    echo "claude-pool: installing the binary / repairing PATH in the background" >&2
    nohup powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$CLAUDE_PLUGIN_ROOT/install.ps1" >/dev/null 2>&1 &
    ;;
  *)
    [ -x "$HOME/.local/bin/claude-pool" ] && exit 0
    echo "claude-pool: installing the binary in the background (active next session)" >&2
    nohup sh "$CLAUDE_PLUGIN_ROOT/install.sh" >/dev/null 2>&1 &
    ;;
esac
exit 0
