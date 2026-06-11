package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/unsafe9/claude-pool/internal/pool"
)

// cmdHook is the plugin hook entry point invoked in cc's exec form as
// `claude-pool hook <event>`. It replaces the old POSIX wrapper
// (hooks/scripts/claude-pool-run.sh) so the hook works on macOS, Linux, and
// Windows (incl. pure PowerShell) without a shell.
//
// Critical invariant: the hook path must NEVER exit 2. cc treats hook exit 2 as
// a blocking error (UserPromptSubmit would erase the user's prompt). A Go panic
// exits with status 2, so we recover any panic here and exit 1 instead. main's
// error path already exits 1, never 2.
//
// Another invariant: the session-start path must write NOTHING to stdout —
// SessionStart stdout is injected into Claude's context. Diagnostics go to
// stderr only, and the empty-pool import is run as a subprocess with its stdout
// discarded so cmdImport's stdout can't leak into the session context.
func cmdHook(args []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "claude-pool: hook panic recovered:", r)
			os.Exit(1)
		}
	}()

	if len(args) < 1 {
		return nil // unknown/empty event → silent no-op (script's `*) exit 0`)
	}
	switch args[0] {
	case "session-start":
		return hookSessionStart()
	case "background":
		// Detached auto so a slow poll never freezes the session; best-effort
		// (port of the script's `nohup ... &`).
		_ = spawnDetached("auto", "--if-needed", "--threshold", "0.9")
		return nil
	case "stop-failure":
		return cmdAuto(nil)
	default:
		return nil // unknown event → silent no-op
	}
}

func hookSessionStart() error {
	// 1. Best-effort cleanup of a leftover self-update artifact. It only exists
	// on Windows after a self-replace renamed the old exe aside; ignore all
	// errors (including os.Executable failing).
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}

	// 2. Self-update check (port of the script's ensure_binary). The binary IS
	// this process, so there is no gobin/GOPATH search to port.
	if want, ok := pluginVersion(); ok && wantSelfUpdate(version, want) {
		fmt.Fprintf(os.Stderr,
			"claude-pool: updating v%s → v%s in the background (active next session)\n",
			normVersion(version), normVersion(want))
		_ = spawnDetached("__selfupdate", "v"+normVersion(want))
	}

	// 3. Empty-pool import (port). Run import as a subprocess with stdout
	// discarded so cmdImport's stdout can't leak into the session-start
	// context; stderr is inherited so a persistently failing first-run import
	// stays diagnosable. Best-effort: ignore the error.
	if s, err := pool.Load(); err == nil && len(s.Accounts) == 0 && len(s.APIKeys) == 0 {
		if exe, err := os.Executable(); err == nil {
			cmd := exec.Command(exe, "import")
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
		}
	}

	// 4. Run the auto-swap in-process. cmdAuto writes only to stderr, so it is
	// safe in the session-start (no-stdout) path.
	return cmdAuto([]string{"--if-needed", "--threshold", "0.9"})
}

// pluginVersion reads the wanted version from the installed plugin manifest
// ($CLAUDE_PLUGIN_ROOT/.claude-plugin/plugin.json). The bool is false when the
// env var is unset, the file is unreadable, or the version field is empty —
// callers skip the self-update in that case.
func pluginVersion() (string, bool) {
	root := os.Getenv("CLAUDE_PLUGIN_ROOT")
	if root == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return "", false
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil || m.Version == "" {
		return "", false
	}
	return m.Version, true
}

// normVersion strips a leading "v" so a vX.Y.Z tag compares equal to an X.Y.Z
// plugin version.
func normVersion(s string) string { return strings.TrimPrefix(s, "v") }

// wantSelfUpdate decides whether to self-update from the running binary's
// version (have) to the installed plugin's version (want). A "dev" build is a
// local/source build and is never replaced; equal versions need no update.
// Both sides are normalized (leading "v" stripped) before comparison.
func wantSelfUpdate(have, want string) bool {
	h, w := normVersion(have), normVersion(want)
	if w == "" || h == "dev" || h == w {
		return false
	}
	return true
}

// cmdWake is the hidden `__wake <seconds>` subcommand: sleep, then run auto.
// It replaces the unix-only `/bin/sh -c "sleep N; exec <exe> auto"` recovery
// waker so it works on Windows too.
func cmdWake(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: claude-pool __wake <seconds>")
	}
	secs, err := strconv.Atoi(args[0])
	if err != nil || secs < 0 {
		return fmt.Errorf("invalid seconds %q", args[0])
	}
	time.Sleep(time.Duration(secs) * time.Second)
	return cmdAuto(nil)
}

// selfUpdateTimeout bounds the release-binary download.
const selfUpdateTimeout = 5 * time.Minute

// cmdSelfUpdate is the hidden `__selfupdate <tag>` subcommand spawned detached
// by the session-start self-update check. It downloads the release binary for
// tag (which includes the leading "v", e.g. "v0.2.0") and atomically replaces
// the running executable.
func cmdSelfUpdate(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: claude-pool __selfupdate <tag>")
	}
	tag := args[0]
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	url := pool.ReleaseAssetURL(tag, runtime.GOOS, runtime.GOARCH)
	return downloadAndReplace(url, exe)
}

// downloadAndReplace fetches url and atomically replaces exe with it. The temp
// file is created in the SAME directory as exe so the final rename stays on one
// volume (rename is only atomic within a volume). On any failure the temp file
// is removed.
func downloadAndReplace(url, exe string) (err error) {
	client := &http.Client{Timeout: selfUpdateTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(exe), ".claude-pool.download-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err = io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}

	return replaceExecutable(tmpPath, exe)
}
