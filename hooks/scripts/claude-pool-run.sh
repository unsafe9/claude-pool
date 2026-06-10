#!/bin/sh
# claude-pool hook wrapper: resolves the claude-pool binary, keeps it in sync
# with the installed plugin version (downloading the matching release binary
# when missing or outdated), and runs the auto-swap action for the invoking
# hook event. Must never exit 2 — Claude Code treats hook exit 2 as a blocking
# error (UserPromptSubmit would block and erase the user's prompt).
set -u

REPO="unsafe9/claude-pool"
PLUGIN_JSON="${CLAUDE_PLUGIN_ROOT:-}/.claude-plugin/plugin.json"

# bin_dir is where we install the downloaded binary and look for it first.
# Honors $GOBIN / $GOPATH so a Go-installed copy and ours share one location.
bin_dir() {
  if [ -n "${GOBIN:-}" ]; then
    printf '%s\n' "$GOBIN"
  elif [ -n "${GOPATH:-}" ]; then
    printf '%s\n' "$GOPATH/bin"
  else
    printf '%s\n' "$HOME/go/bin"
  fi
}

find_pool() {
  command -v claude-pool 2>/dev/null && return 0
  bd="$(bin_dir)"
  if [ -x "$bd/claude-pool" ]; then
    printf '%s\n' "$bd/claude-pool"
    return 0
  fi
  if [ -x "$HOME/go/bin/claude-pool" ]; then
    printf '%s\n' "$HOME/go/bin/claude-pool"
    return 0
  fi
  return 1
}

# plugin_version reads the semver from the installed plugin manifest (no jq:
# hooks can't assume it). Empty when the manifest is missing/unreadable.
plugin_version() {
  [ -f "$PLUGIN_JSON" ] || return 0
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$PLUGIN_JSON" | head -1
}

# norm strips a leading "v" so a vX.Y.Z tag/binary compares equal to an X.Y.Z
# plugin version.
norm() { printf '%s' "${1#v}"; }

# download_release fetches the darwin/arm64 binary for version $1 (no "v") and
# installs it atomically into bin_dir. Best-effort.
download_release() {
  ver="$1"
  bd="$(bin_dir)"
  url="https://github.com/$REPO/releases/download/v${ver}/claude-pool-darwin-arm64"
  mkdir -p "$bd" || return 1
  tmp="$bd/.claude-pool.download.$$"
  if curl -fsSL "$url" -o "$tmp" 2>/dev/null; then
    chmod +x "$tmp" && mv -f "$tmp" "$bd/claude-pool" && return 0
  fi
  rm -f "$tmp"
  return 1
}

# ensure_binary keeps the local binary in step with the installed plugin
# version. The download runs detached so a slow fetch never freezes the session;
# the new binary activates on the next session. A "dev" binary is a local/source
# build (make install, go install) and is never clobbered.
ensure_binary() {
  want="$(norm "$(plugin_version)")"
  [ -n "$want" ] || return 0
  have=""
  if p="$(find_pool)"; then
    have="$(norm "$("$p" version 2>/dev/null)")"
  fi
  [ "$have" = "dev" ] && return 0
  [ "$have" = "$want" ] && return 0
  if ! command -v curl >/dev/null 2>&1; then
    echo "claude-pool: curl not found — install claude-pool manually (see https://github.com/$REPO)" >&2
    return 0
  fi
  if [ -z "$have" ]; then
    echo "claude-pool: installing v$want in the background (active next session)" >&2
  else
    echo "claude-pool: updating v$have → v$want in the background (active next session)" >&2
  fi
  nohup sh "$0" __download "$want" >/dev/null 2>&1 &
}

# __download is the detached re-invocation ensure_binary spawns; it just fetches
# the release binary for the given version and exits.
if [ "${1:-}" = "__download" ]; then
  download_release "${2:-}"
  exit 0
fi

# run remaps exit 2 to 1: even a Go panic (status 2) must not become a blocking
# hook error.
run() {
  "$POOL" "$@"
  rc=$?
  [ "$rc" -eq 2 ] && exit 1
  exit "$rc"
}

case "${1:-}" in
  session-start)
    # Make sure the binary matches the installed plugin before doing anything;
    # a missing/outdated one downloads in the background and lands next session.
    ensure_binary
    POOL="$(find_pool)" || exit 0
    # First run on a logged-in machine: an empty pool has nothing to manage,
    # so register the current Keychain credential. (stdout suppressed:
    # SessionStart stdout is injected into Claude's context.)
    pooljson="$("$POOL" list --json 2>/dev/null)"
    if printf '%s' "$pooljson" | grep -q '"accounts":\[\]' && printf '%s' "$pooljson" | grep -q '"api_keys":\[\]'; then
      "$POOL" import >/dev/null 2>&1
    fi
    run auto --if-needed --threshold 0.9
    ;;
  background)
    POOL="$(find_pool)" || exit 0
    nohup "$POOL" auto --if-needed --threshold 0.9 >/dev/null 2>&1 &
    exit 0
    ;;
  stop-failure)
    POOL="$(find_pool)" || exit 0
    run auto
    ;;
  *)
    exit 0
    ;;
esac
