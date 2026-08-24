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

// cmdHook is the plugin hook entry point, invoked in cc's exec form as
// `claude-pool hook <event>` — shell-free, so it works on Windows too.
func cmdHook(args []string) (err error) {
	// No path here may exit 2: cc reads that as a blocking error and
	// UserPromptSubmit would erase the user's prompt. A Go panic exits 2, so
	// catch it and exit 1 instead.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "claude-pool: hook panic recovered:", r)
			os.Exit(1)
		}
	}()

	if len(args) < 1 {
		return nil // unknown/empty event → silent no-op
	}
	switch args[0] {
	case "session-start":
		return hookSessionStart()
	case "background":
		// Detached so a slow poll never freezes the session — which is also what
		// makes this the one hook that can afford --keepalive, whose renewals run
		// off cc's critical path entirely.
		_ = spawnDetached("auto", "--if-needed", "--threshold", "0.9", "--keepalive")
		return nil
	case "stop-failure":
		return cmdAuto(nil)
	default:
		return nil // unknown event → silent no-op
	}
}

// Nothing on this path may write to stdout: cc injects SessionStart stdout into
// Claude's context. Diagnostics go to stderr only.
func hookSessionStart() error {
	// A leftover .old only exists on Windows, where self-replace renames the
	// running exe aside.
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}

	if want, ok := pluginVersion(); ok && wantSelfUpdate(version, want) {
		fmt.Fprintf(os.Stderr,
			"claude-pool: updating v%s → v%s in the background (active next session)\n",
			normVersion(version), normVersion(want))
		_ = spawnDetached("__selfupdate", "v"+normVersion(want))
	}

	// First-run import runs as a subprocess so cmdImport's stdout is discarded
	// rather than injected into the session context; stderr is inherited to keep
	// a persistently failing import diagnosable.
	if s, err := pool.Load(); err == nil && len(s.Accounts) == 0 && len(s.APIKeys) == 0 {
		if exe, err := os.Executable(); err == nil {
			cmd := exec.Command(exe, "import")
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
		}
	}

	// In-process is safe here: cmdAuto writes only to stderr.
	return cmdAuto([]string{"--if-needed", "--threshold", "0.9"})
}

// pluginVersion reads the wanted version from the installed plugin manifest.
// A false bool means "unknown", and callers skip the self-update.
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

// wantSelfUpdate compares the running binary (have) against the installed
// plugin (want). A "dev" build is someone's working tree and is never replaced.
func wantSelfUpdate(have, want string) bool {
	h, w := normVersion(have), normVersion(want)
	if w == "" || h == "dev" || h == w {
		return false
	}
	return true
}

// cmdWake is the hidden `__wake <seconds>` subcommand: sleep, then run auto.
// In-binary rather than a shell one-liner so the recovery waker works on
// Windows.
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

const selfUpdateTimeout = 5 * time.Minute

// cmdSelfUpdate is the hidden `__selfupdate <tag>` subcommand: download the
// release binary for tag ("v0.2.0", leading v included) and atomically replace
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
// file goes in exe's own directory, since rename is only atomic within a volume.
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
