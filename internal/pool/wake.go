package pool

import (
	"os"
	"path/filepath"
	"time"
)

// wakePath is the marker file recording the next scheduled recovery wake, kept
// next to the store so overlapping apikey entries can dedup their wakers.
func wakePath() (string, error) {
	sp, err := StorePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(sp), "wake"), nil
}

// PendingWakeBefore reports whether a recovery wake is already recorded at or
// before t — i.e. an existing waker will fire no later than the new target, so
// scheduling another is redundant. Best-effort: any error reports false (no
// dedup) so a wake still gets scheduled.
func PendingWakeBefore(t time.Time) bool {
	p, err := wakePath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	prev, err := time.Parse(time.RFC3339, string(data))
	if err != nil {
		return false
	}
	// A pending wake in the past has effectively fired (or its process died);
	// treat it as no longer pending so a fresh wake is scheduled.
	if prev.Before(time.Now()) {
		return false
	}
	return !prev.After(t)
}

// RecordWake stores t as the next scheduled recovery wake. Best-effort: errors
// are ignored, the dedup just degrades to scheduling more wakers.
func RecordWake(t time.Time) {
	p, err := wakePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(t.Format(time.RFC3339)), 0o600)
}
