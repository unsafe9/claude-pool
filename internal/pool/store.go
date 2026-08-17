package pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Account is one stored Claude subscription account. Blob is the exact
// credential JSON Claude Code keeps in its credential store, preserved verbatim
// so it can be written back unchanged (only refresh mutates it). Email is the
// account identity captured at import time; it lets a Keychain blob refreshed
// by cc itself be attributed back to the right account.
type Account struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
	Blob  string `json:"blob"`

	// AuthError records why the server permanently rejected this credential —
	// a refresh token it has retired or revoked. Non-empty means retrying
	// cannot help; adopting a working credential (import, or harvesting a
	// cc-side login) clears it.
	AuthError string `json:"auth_error,omitempty"`

	Usage   *Usage    `json:"usage,omitempty"`    // last polled usage (cache)
	UsageAt time.Time `json:"usage_at,omitempty"` // when Usage was fetched
}

// Revoked reports whether the server has permanently rejected this account's
// credential, so only a fresh login can revive it.
func (a *Account) Revoked() bool { return a.AuthError != "" }

// APIKey is one stored Anthropic API key, used only after every subscription
// account is exhausted.
type APIKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// Modes: accounts are always preferred; apikey is the fallback once every
// account's binding window is at 100%, and is left as soon as any account
// resets.
const (
	ModeAccount = "account"
	ModeAPIKey  = "apikey"
)

// Store is the on-disk pool.
type Store struct {
	Mode string `json:"mode,omitempty"`
	// Current is the account ID last written to the Keychain.
	Current string `json:"current,omitempty"`
	// CurrentKey and KeyCursor hold the round-robin position over APIKeys.
	CurrentKey string `json:"current_key,omitempty"`
	KeyCursor  int    `json:"key_cursor,omitempty"`
	// SavedHelper preserves a foreign apiKeyHelper displaced on entering apikey
	// mode, so leaving restores it.
	SavedHelper string `json:"saved_api_key_helper,omitempty"`
	// IgnoreScopedLimits opts out of model-scoped windows (Fable's own weekly
	// quota, say), leaving exhaustion to the account-wide 5h and 7d windows.
	IgnoreScopedLimits bool       `json:"ignore_scoped_limits,omitempty"`
	Accounts           []*Account `json:"accounts"`
	APIKeys            []*APIKey  `json:"api_keys,omitempty"`

	path string
}

// StorePath returns ~/.config/claude-pool/pool.json. Overridable for tests.
var StorePath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claude-pool", "pool.json"), nil
}

// configDir is where the store and its sidecars (locks, the wake marker) live,
// so a test that redirects StorePath moves all of them together.
func configDir() (string, error) {
	path, err := StorePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

// Load reads the store, returning an empty store if the file does not exist yet.
// A legacy plaintext file (starting with '{') still reads, and the next Save
// re-writes it encrypted.
func Load() (*Store, error) {
	path, err := StorePath()
	if err != nil {
		return nil, err
	}
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if bytes.HasPrefix(data, storeMagic) {
		if data, err = decrypt(data); err != nil {
			return nil, fmt.Errorf("decrypt %s: %w; the store was likely encrypted on a different machine or under a different user — remove it and re-import accounts", path, err)
		}
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	s.path = path
	s.applyLimitPolicy()
	return s, nil
}

// applyLimitPolicy strips opted-out scoped windows from the cached usages.
// Without it, the cache alone could hold the pool in API-key mode for the whole
// of a scoped window's multi-day reset, long after the opt-out.
func (s *Store) applyLimitPolicy() {
	if !s.IgnoreScopedLimits {
		return
	}
	for _, a := range s.Accounts {
		if a.Usage != nil {
			u := a.Usage.WithoutScoped()
			a.Usage = &u
		}
	}
}

// Save writes the store atomically with 0600 permissions. The bytes hitting disk
// (including the .tmp staging file) are encrypted, never plaintext.
func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	enc, err := encrypt(data)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Find returns the account with the given ID, or nil.
func (s *Store) Find(id string) *Account {
	for _, a := range s.Accounts {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// Upsert adds a, or refreshes an existing account with the same ID. A replaced
// blob clears any recorded auth failure, which is what makes re-import the cure
// for a dead grant.
func (s *Store) Upsert(a *Account) {
	if existing := s.Find(a.ID); existing != nil {
		existing.Blob = a.Blob
		existing.AuthError = ""
		if a.Email != "" {
			existing.Email = a.Email
		}
		return
	}
	s.Accounts = append(s.Accounts, a)
}

// FindKey returns the API key with the given ID, or nil.
func (s *Store) FindKey(id string) *APIKey {
	for _, k := range s.APIKeys {
		if k.ID == id {
			return k
		}
	}
	return nil
}

// UpsertKey adds k, or replaces the key of an existing entry with the same ID.
func (s *Store) UpsertKey(k *APIKey) {
	if existing := s.FindKey(k.ID); existing != nil {
		existing.Key = k.Key
		return
	}
	s.APIKeys = append(s.APIKeys, k)
}

// Remove deletes the account or API key with the given ID, clearing any
// current-pointer to it. It reports whether something was removed.
func (s *Store) Remove(id string) bool {
	for i, a := range s.Accounts {
		if a.ID == id {
			s.Accounts = append(s.Accounts[:i], s.Accounts[i+1:]...)
			if s.Current == id {
				s.Current = ""
			}
			return true
		}
	}
	for i, k := range s.APIKeys {
		if k.ID == id {
			s.APIKeys = append(s.APIKeys[:i], s.APIKeys[i+1:]...)
			if s.CurrentKey == id {
				s.CurrentKey = ""
			}
			if s.KeyCursor >= len(s.APIKeys) {
				s.KeyCursor = 0
			}
			return true
		}
	}
	return false
}

// NextKey advances the round-robin cursor and returns the next API key, or nil
// if none are registered. The caller is responsible for saving the store.
func (s *Store) NextKey() *APIKey {
	if len(s.APIKeys) == 0 {
		return nil
	}
	s.KeyCursor = (s.KeyCursor + 1) % len(s.APIKeys)
	k := s.APIKeys[s.KeyCursor]
	s.CurrentKey = k.ID
	return k
}

// PeekNextKey returns the key NextKey would serve next, without advancing.
func (s *Store) PeekNextKey() *APIKey {
	if len(s.APIKeys) == 0 {
		return nil
	}
	return s.APIKeys[(s.KeyCursor+1)%len(s.APIKeys)]
}

// withFileLock runs fn holding an exclusive cross-process lock on path,
// creating it if needed.
func withFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		return err
	}
	defer unlockFile(f)
	return fn()
}

// LockedUpdate makes a read-modify-write atomic across the processes that race
// through cc hooks (auto, helper, current). An error from fn aborts without
// saving.
func LockedUpdate(fn func(*Store) error) (*Store, error) {
	path, err := StorePath()
	if err != nil {
		return nil, err
	}
	var s *Store
	err = withFileLock(path+".lock", func() error {
		if s, err = Load(); err != nil {
			return err
		}
		if err := fn(s); err != nil {
			return err
		}
		return s.Save()
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Update is LockedUpdate with the result written back into s.
//
// It REPLACES s's contents, slices included, with the freshly-loaded post-save
// state: any *Account taken from s beforehand is detached afterward and must
// not be reused.
func (s *Store) Update(fn func(*Store) error) error {
	ns, err := LockedUpdate(fn)
	if err != nil {
		return err
	}
	*s = *ns
	return nil
}
