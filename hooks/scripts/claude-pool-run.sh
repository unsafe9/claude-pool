#!/bin/sh
# claude-pool hook wrapper: resolves the claude-pool binary (bootstrapping it
# with `go install` on first session if missing) and runs the auto-swap action
# for the invoking hook event. Must never exit 2 — Claude Code treats hook
# exit 2 as a blocking error.
set -u

MODULE="github.com/unsafe9/claude-pool/cmd/claude-pool@latest"

find_pool() {
  command -v claude-pool 2>/dev/null && return 0
  if [ -x "$HOME/go/bin/claude-pool" ]; then
    echo "$HOME/go/bin/claude-pool"
    return 0
  fi
  if command -v go >/dev/null 2>&1; then
    gobin="$(go env GOBIN 2>/dev/null)"
    [ -z "$gobin" ] && gobin="$(go env GOPATH 2>/dev/null)/bin"
    if [ -x "$gobin/claude-pool" ]; then
      echo "$gobin/claude-pool"
      return 0
    fi
  fi
  return 1
}

POOL="$(find_pool)" || {
  # Bootstrap only at session start, in the background: a first-time
  # `go install` (module download + compile) must not freeze the session.
  if [ "${1:-}" = "session-start" ]; then
    if command -v go >/dev/null 2>&1; then
      echo "claude-pool: not installed; running \`go install $MODULE\` in the background (hooks activate once it finishes)" >&2
      nohup go install "$MODULE" >/dev/null 2>&1 &
    else
      echo "claude-pool: Go toolchain not found — install Go, or install claude-pool manually (see https://github.com/unsafe9/claude-pool)" >&2
    fi
  fi
  exit 0
}

# run remaps exit 2 to 1: even a Go panic (status 2) must not become a
# blocking hook error.
run() {
  "$POOL" "$@"
  rc=$?
  [ "$rc" -eq 2 ] && exit 1
  exit "$rc"
}

case "${1:-}" in
  session-start)
    # Register the logged-in account on first sight, so a fresh /login joins
    # the pool without a manual import. (stdout suppressed: SessionStart
    # stdout is injected into Claude's context.)
    "$POOL" import --if-missing >/dev/null 2>&1
    run auto --if-needed --threshold 0.9
    ;;
  background)
    nohup "$POOL" auto --if-needed --threshold 0.9 >/dev/null 2>&1 &
    exit 0
    ;;
  stop-failure)
    run auto
    ;;
  *)
    exit 0
    ;;
esac
