package pool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStore_RoundTripAndUpsert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")

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

	// Reload from disk.
	s2 := &Store{path: path}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, s2); err != nil {
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
