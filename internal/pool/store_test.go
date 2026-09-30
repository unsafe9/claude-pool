package pool

import (
	"os"
	"testing"
	"time"
)

func TestStore_RoundTripAndUpsert(t *testing.T) {
	withMachineID(t, "test-machine-id")
	path := withStorePath(t)

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

// Re-importing is the cure for a revoked grant, so a replaced blob has to clear
// the verdict recorded against the old one.
func TestStore_UpsertClearsAuthError(t *testing.T) {
	s := &Store{}
	s.Upsert(&Account{ID: "alice", Blob: "old", AuthError: "token refresh: HTTP 400 invalid_grant"})
	s.Upsert(&Account{ID: "alice", Blob: "new"})
	if got := s.Find("alice").AuthError; got != "" {
		t.Errorf("AuthError = %q, want cleared", got)
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

func TestStore_DisabledEntriesLeaveRotation(t *testing.T) {
	s := &Store{
		Accounts: []*Account{{ID: "a"}, {ID: "b", Disabled: true}},
		APIKeys:  []*APIKey{{ID: "k1"}, {ID: "k2", Disabled: true}, {ID: "k3"}},
	}

	if got := s.EnabledAccounts(); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("EnabledAccounts = %v, want [a]", got)
	}
	if got := s.EnabledKeys(); len(got) != 2 {
		t.Errorf("EnabledKeys = %d, want 2", len(got))
	}

	// The round-robin steps over k2 rather than serving a key taken out of
	// rotation, and keeps cycling the rest.
	for _, want := range []string{"k3", "k1", "k3"} {
		if k := s.NextKey(); k == nil || k.ID != want {
			t.Fatalf("NextKey = %v, want %s", k, want)
		}
	}

	// Every key disabled reads as no keys at all, which is what demotes the pool
	// out of API-key mode instead of leaving cc on a helper that can only fail.
	for _, k := range s.APIKeys {
		k.Disabled = true
	}
	if k := s.NextKey(); k != nil {
		t.Errorf("NextKey = %v, want nil when every key is disabled", k)
	}
	if k := s.PeekNextKey(); k != nil {
		t.Errorf("PeekNextKey = %v, want nil when every key is disabled", k)
	}
}

func TestStore_SetDisabled(t *testing.T) {
	s := &Store{Accounts: []*Account{{ID: "a"}}, APIKeys: []*APIKey{{ID: "k1"}}}

	if kind, changed := s.SetDisabled("a", true); kind != "account" || !changed || !s.Find("a").Disabled {
		t.Errorf("SetDisabled(account) = (%q, %v)", kind, changed)
	}
	// Idempotent, and says so: the caller reports "already disabled" rather than
	// swapping the live credential a second time.
	if _, changed := s.SetDisabled("a", true); changed {
		t.Error("re-disabling reported a change")
	}
	if kind, changed := s.SetDisabled("k1", true); kind != "key" || !changed || !s.FindKey("k1").Disabled {
		t.Errorf("SetDisabled(key) = (%q, %v)", kind, changed)
	}
	if kind, _ := s.SetDisabled("nope", true); kind != "" {
		t.Errorf("SetDisabled(unknown) = %q, want empty", kind)
	}
}

// Re-adding an entry is asking to use it, so it comes back in rotation — the
// alternative is an import that silently lands on a disabled account.
func TestStore_UpsertReenables(t *testing.T) {
	s := &Store{}
	s.Upsert(&Account{ID: "a", Blob: "old"})
	s.Find("a").Disabled = true
	s.Upsert(&Account{ID: "a", Blob: "new"})
	if s.Find("a").Disabled {
		t.Error("Upsert left the account disabled")
	}

	s.UpsertKey(&APIKey{ID: "k1", Key: "sk-1"})
	s.FindKey("k1").Disabled = true
	s.UpsertKey(&APIKey{ID: "k1", Key: "sk-2"})
	if s.FindKey("k1").Disabled {
		t.Error("UpsertKey left the key disabled")
	}
}

// Opting out must reach the CACHED usages, not just fresh polls: while in
// API-key mode the cache alone decides whether to re-poll, so a stale scoped
// window would otherwise pin the pool to billed keys until its multi-day reset.
func TestLoad_IgnoreScopedLimitsStripsCachedUsage(t *testing.T) {
	withMachineID(t, "test-machine-id")
	path := withStorePath(t)

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

// A pin holds cc on one account only while that account is what cc runs on; a
// swap away must drop it, or a later swap landing back there would revive it.
func TestStore_PinLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	pinned := func() *Store {
		s := &Store{Accounts: []*Account{{ID: "a"}, {ID: "b"}}}
		s.SetCurrent("a")
		s.PinnedID, s.PinnedUntil = "a", now.Add(time.Hour)
		return s
	}

	if s := pinned(); s.PinnedAccount(now) == nil {
		t.Fatal("fresh pin not in force")
	}
	if s := pinned(); s.PinnedAccount(now.Add(time.Hour)) != nil {
		t.Error("pin in force past its deadline")
	}
	s := pinned()
	s.Mode = ModeAPIKey
	if s.PinnedAccount(now) != nil {
		t.Error("pin in force in API-key mode")
	}

	s = pinned()
	s.SetCurrent("a") // re-selecting the pinned account keeps the pin
	if s.PinnedAccount(now) == nil {
		t.Error("SetCurrent on the pinned account dropped the pin")
	}
	s.SetCurrent("b")
	s.SetCurrent("a")
	if s.PinnedAccount(now) != nil {
		t.Error("pin revived after a swap away and back")
	}

	s = pinned()
	s.SetDisabled("a", true)
	s.SetDisabled("a", false)
	if s.PinnedAccount(now) != nil {
		t.Error("pin revived after disable/enable")
	}
}
