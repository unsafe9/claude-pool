package main

import (
	"strings"
	"testing"

	"github.com/unsafe9/claude-pool/internal/pool"
)

// Every case here keeps the toggled entry out of the live slot, so nothing
// reaches the swap path — cmdAuto would touch the real credential store.
func seedKeys(t *testing.T, keys ...*pool.APIKey) {
	t.Helper()
	if _, err := pool.LockedUpdate(func(st *pool.Store) error {
		st.APIKeys = keys
		return nil
	}); err != nil {
		t.Fatalf("seed keys: %v", err)
	}
}

func TestCmdSetDisabled(t *testing.T) {
	withPoolStore(t, &pool.Account{ID: "a"}, &pool.Account{ID: "b"})
	seedKeys(t, &pool.APIKey{ID: "k1", Key: "sk-1"}, &pool.APIKey{ID: "k2", Key: "sk-2"})

	if err := cmdSetDisabled([]string{"b"}, true); err != nil {
		t.Fatalf("disable account: %v", err)
	}
	if err := cmdSetDisabled([]string{"k1"}, true); err != nil {
		t.Fatalf("disable key: %v", err)
	}

	s, err := pool.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Disabling keeps the entry and its credential — that is the whole
	// difference from `rm`.
	if b := s.Find("b"); b == nil || !b.Disabled {
		t.Errorf("account b = %+v, want kept and disabled", b)
	}
	if k := s.FindKey("k1"); k == nil || !k.Disabled || k.Key != "sk-1" {
		t.Errorf("key k1 = %+v, want kept and disabled", k)
	}
	if len(s.EnabledAccounts()) != 1 || len(s.EnabledKeys()) != 1 {
		t.Errorf("rotation = %d accounts / %d keys, want 1 each", len(s.EnabledAccounts()), len(s.EnabledKeys()))
	}

	// Re-disabling is a no-op, not a second swap.
	if err := cmdSetDisabled([]string{"b"}, true); err != nil {
		t.Errorf("re-disable: %v", err)
	}

	if err := cmdSetDisabled([]string{"b"}, false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if s, _ = pool.Load(); s.Find("b").Disabled {
		t.Error("enable did not put the account back in rotation")
	}
}

func TestCmdSetDisabled_UnknownID(t *testing.T) {
	withPoolStore(t, &pool.Account{ID: "a"})

	err := cmdSetDisabled([]string{"nope"}, true)
	if err == nil || !strings.Contains(err.Error(), "no account or API key") {
		t.Fatalf("err = %v, want an unknown-id error", err)
	}
	if err := cmdSetDisabled(nil, true); err == nil || !strings.Contains(err.Error(), "usage: claude-pool disable") {
		t.Errorf("err = %v, want the disable usage line", err)
	}
	if err := cmdSetDisabled(nil, false); err == nil || !strings.Contains(err.Error(), "usage: claude-pool enable") {
		t.Errorf("err = %v, want the enable usage line", err)
	}
}
