package pool

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Claude Code serializes its own token refreshes across processes with a
// proper-lockfile lock on its config dir: a directory made by mkdir, its mtime
// touched while held, and taken over as abandoned once that mtime is a minute
// old. Under it, cc re-reads the stored credential before posting its refresh
// token and saves the rotation with a compare-and-swap. Holding the same lock is
// the only way to keep cc from spending the refresh token the pool is about to
// spend, or from saving a rotation between the pool's read of the credential
// and its overwrite of it.
const (
	ccLockStale  = 60 * time.Second
	ccLockUpdate = 5 * time.Second
)

// A var so tests can shorten it. cc holds its lock for one token round trip.
var ccLockWait = 10 * time.Second

// ErrCCLockBusy means cc held its refresh lock for the whole wait.
var ErrCCLockBusy = errors.New("Claude Code is renewing its credential")

// ClaudeDir returns ~/.claude, whose refresh lock cc takes. Overridable for
// tests, which must not contend with a real cc.
var ClaudeDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// ccLockPaths lists cc's current lock and the one older builds take
// (realpath(~/.claude) + ".lock"), in the order cc acquires them, so the two
// can never deadlock.
func ccLockPaths() ([]string, error) {
	dir, err := ClaudeDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	real := dir
	if p, err := filepath.EvalSymlinks(dir); err == nil {
		real = p
	}
	return []string{filepath.Join(dir, ".oauth_refresh.lock"), real + ".lock"}, nil
}

// WithCCRefreshLock runs fn holding Claude Code's own refresh lock. Everything
// that reads, renews or overwrites the credential cc holds runs under it. Lock
// order is cc lock → refresh lock → store lock, and it is not reentrant.
func WithCCRefreshLock(fn func() error) error {
	paths, err := ccLockPaths()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(ccLockWait)
	var held []string
	defer func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = os.Remove(held[i])
		}
	}()
	for _, p := range paths {
		if err := acquireDirLock(p, deadline); err != nil {
			return err
		}
		held = append(held, p)
	}

	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(ccLockUpdate)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				for _, p := range held {
					_ = os.Chtimes(p, now, now)
				}
			}
		}
	}()
	defer func() { close(stop); <-done }()
	return fn()
}

func acquireDirLock(path string, deadline time.Time) error {
	for {
		err := os.Mkdir(path, 0o755)
		if err == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if fi, err := os.Stat(path); err == nil && time.Since(fi.ModTime()) > ccLockStale {
			if os.Remove(path) == nil {
				continue
			}
		}
		if time.Now().After(deadline) {
			return ErrCCLockBusy
		}
		time.Sleep(100 * time.Millisecond)
	}
}
