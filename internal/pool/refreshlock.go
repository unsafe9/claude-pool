package pool

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
)

// CredentialStoreLockID names the lock for the blob Claude Code itself holds,
// as distinct from any pooled account. The leading NUL keeps it out of reach of
// user-chosen account IDs.
const CredentialStoreLockID = "\x00cc-credential-store"

func refreshLockPath(id string) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	// Hashed rather than used verbatim: account IDs are user-chosen and would
	// otherwise need validating as safe path components.
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(dir, fmt.Sprintf("refresh-%x.lock", sum[:8])), nil
}

// WithRefreshLock runs fn holding an exclusive cross-process lock on one
// credential's refresh. Lock order is always refresh lock → store lock.
func WithRefreshLock(id string, fn func() error) error {
	// A lock of its own, not the store's: a refresh is an HTTP round trip, and
	// cmdHelper serves cc's apiKeyHelper under the store lock against cc's own
	// timeout. Per-credential granularity keeps pollAccounts' fan-out
	// concurrent across accounts.
	path, err := refreshLockPath(id)
	if err != nil {
		return err
	}
	return withFileLock(path, fn)
}

// RefreshCredential renews one credential and is the only way to reach the
// token endpoint, so the single-use contract lives in one place: load re-reads
// the authoritative blob inside the lock, and save persists the rotation before
// it is returned. It returns the live blob, refreshed or not.
func RefreshCredential(lockID string, load func() (string, error), save func(string) error) (string, error) {
	var out string
	err := WithRefreshLock(lockID, func() error {
		blob, err := load()
		if err != nil {
			return err
		}
		// Whoever waited on the lock adopts what the holder stored. Replaying
		// the token in hand instead reads as a stolen-token reuse, which costs
		// the whole grant — the race is routine here, since every prompt fires a
		// background `auto` alongside session-start, stop-failure and the helper.
		if od, perr := ParseBlob(blob); perr == nil && !IsExpired(od.ExpiresAt) {
			out = blob
			return nil
		}
		nb, err := refresh(blob)
		if err != nil {
			return err
		}
		if err := save(nb); err != nil {
			return err
		}
		out = nb
		return nil
	})
	return out, err
}
