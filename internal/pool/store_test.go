package pool

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_RoundTripAndUpsert(t *testing.T) {
	withMachineID(t, "test-machine-id")
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	prev := StorePath
	StorePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { StorePath = prev })

	s := &Store{path: path}
	s.Upsert(&Account{ID: "alice", Blob: `{"claudeAiOauth":{"accessToken":"a"}}`})
	s.Upsert(&Account{ID: "bob", Blob: `{"claudeAiOauth":{"accessToken":"b"}}`})
	s.Current = "alice"
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 0600 permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}

	// Reload from disk (decrypts the now-encrypted store).
	s2, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(s2.Accounts) != 2 || s2.Current != "alice" {
		t.Fatalf("reloaded store = %+v", s2)
	}

	// Upsert replaces blob, does not duplicate.
	s2.path = path
	s2.Upsert(&Account{ID: "alice", Blob: `{"claudeAiOauth":{"accessToken":"a2"}}`})
	if len(s2.Accounts) != 2 {
		t.Errorf("Upsert duplicated account: %d accounts", len(s2.Accounts))
	}
	if got := s2.Find("alice").Blob; got != `{"claudeAiOauth":{"accessToken":"a2"}}` {
		t.Errorf("Upsert did not replace blob: %s", got)
	}
}

func TestStore_APIKeysAndRemove(t *testing.T) {
	s := &Store{}
	s.UpsertKey(&APIKey{ID: "k1", Key: "sk-1"})
	s.UpsertKey(&APIKey{ID: "k2", Key: "sk-2"})
	s.UpsertKey(&APIKey{ID: "k1", Key: "sk-1b"})
	if len(s.APIKeys) != 2 {
		t.Fatalf("UpsertKey duplicated: %d keys", len(s.APIKeys))
	}
	if s.FindKey("k1").Key != "sk-1b" {
		t.Errorf("UpsertKey did not replace key")
	}

	// Round-robin: cursor 0 → next is k2, then wraps to k1.
	if k := s.NextKey(); k.ID != "k2" || s.CurrentKey != "k2" {
		t.Errorf("NextKey = %v, CurrentKey = %q", k, s.CurrentKey)
	}
	if k := s.NextKey(); k.ID != "k1" {
		t.Errorf("NextKey wrap = %v", k)
	}

	s.Current = "alice"
	s.Accounts = []*Account{{ID: "alice"}}
	if !s.Remove("alice") || len(s.Accounts) != 0 || s.Current != "" {
		t.Errorf("Remove account failed: %+v", s)
	}
	if !s.Remove("k1") || s.CurrentKey != "" || len(s.APIKeys) != 1 {
		t.Errorf("Remove key failed: %+v", s)
	}
	if s.Remove("nope") {
		t.Errorf("Remove of unknown id reported true")
	}
	if s.NextKey().ID != "k2" {
		t.Errorf("NextKey after removal broken")
	}
}

// Opting out must reach the CACHED usages, not just fresh polls: while in
// API-key mode the cache alone decides whether to re-poll, so a stale scoped
// window would otherwise pin the pool to billed keys until its multi-day reset.
func TestLoad_IgnoreScopedLimitsStripsCachedUsage(t *testing.T) {
	withMachineID(t, "test-machine-id")
	dir := t.TempDir()
	path := filepath.Join(dir, "pool.json")
	prev := StorePath
	StorePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { StorePath = prev })

	exhausted := Usage{
		FiveHour: Window{Pct: 20},
		SevenDay: Window{Pct: 54},
		Scoped:   []Window{{Pct: 100, Label: "Fable", ResetsAt: time.Now().Add(48 * time.Hour)}},
	}
	s := &Store{path: path}
	s.Upsert(&Account{ID: "alice", Blob: `{"claudeAiOauth":{"accessToken":"a"}}`})
	s.Accounts[0].Usage = &exhausted
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Opted in (the default): the Fable window still binds.
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if score := got.Accounts[0].Usage.Score(); score != 100 {
		t.Errorf("opted-in Score = %v, want 100", score)
	}

	got.IgnoreScopedLimits = true
	got.path = path
	if err := got.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n := len(got.Accounts[0].Usage.Scoped); n != 0 {
		t.Errorf("opted-out cached usage kept %d scoped window(s), want 0", n)
	}
	if score := got.Accounts[0].Usage.Score(); score != 54 {
		t.Errorf("opted-out Score = %v, want 54 (account-wide windows only)", score)
	}
}
