package pool

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// withStorePath redirects the store — and with it every sidecar under
// configDir — into a temp dir, returning the store path.
func withStorePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pool.json")
	prev := StorePath
	StorePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { StorePath = prev })
	return path
}

// Two overlapping refreshes of one credential would replay a single-use token;
// the lock is what makes the second wait and re-read instead.
func TestWithRefreshLock_SerializesSameID(t *testing.T) {
	withStorePath(t)

	var inside, overlaps atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = WithRefreshLock("acct", func() error {
				if inside.Add(1) > 1 {
					overlaps.Add(1)
				}
				inside.Add(-1)
				return nil
			})
		}()
	}
	wg.Wait()

	if n := overlaps.Load(); n != 0 {
		t.Errorf("%d overlapping refreshes of the same account", n)
	}
}

// Different accounts must stay concurrent, or pollAccounts' fan-out serializes.
func TestWithRefreshLock_DistinctIDsDoNotShareALock(t *testing.T) {
	withStorePath(t)

	a, err := refreshLockPath("alice")
	if err != nil {
		t.Fatal(err)
	}
	b, err := refreshLockPath("bob")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("alice and bob share lock file %s", a)
	}

	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = WithRefreshLock("alice", func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	if err := WithRefreshLock("bob", func() error { return nil }); err != nil {
		t.Errorf("bob blocked behind alice: %v", err)
	}
	close(release)
}

// Account IDs are user-chosen, so they must not reach the filesystem verbatim.
func TestRefreshLockPath_IDIsNotAPathComponent(t *testing.T) {
	store := withStorePath(t)

	got, err := refreshLockPath("../../escape")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != filepath.Dir(store) {
		t.Errorf("lock path %s escaped %s", got, filepath.Dir(store))
	}
}
