package pool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The lock is only worth holding if cc reads it as its own: a live holder is
// waited out, an abandoned one is taken over, and nothing is left behind.
func TestWithCCRefreshLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude")
	prevDir, prevWait := ClaudeDir, ccLockWait
	ClaudeDir = func() (string, error) { return dir, nil }
	ccLockWait = 300 * time.Millisecond
	t.Cleanup(func() { ClaudeDir, ccLockWait = prevDir, prevWait })
	paths, err := ccLockPaths()
	if err != nil {
		t.Fatal(err)
	}
	ran := func() bool {
		var ok bool
		if err := WithCCRefreshLock(func() error { ok = true; return nil }); err != nil && !errors.Is(err, ErrCCLockBusy) {
			t.Fatal(err)
		}
		return ok
	}

	// cc holding its lock, mtime fresh.
	if err := os.Mkdir(paths[0], 0o755); err != nil {
		t.Fatal(err)
	}
	if ran() {
		t.Fatal("ran while cc held its lock")
	}

	// The same lock, abandoned past the stale age.
	old := time.Now().Add(-2 * ccLockStale)
	if err := os.Chtimes(paths[0], old, old); err != nil {
		t.Fatal(err)
	}
	if !ran() {
		t.Fatal("did not take over an abandoned lock")
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s left behind (err = %v)", p, err)
		}
	}
}
