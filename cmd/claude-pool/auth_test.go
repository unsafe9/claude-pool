package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unsafe9/claude-pool/internal/pool"
)

// None of these tests may reach the network: a refresh against the real endpoint
// would consume a single-use token. Each keeps ensureFresh on a path that
// returns before pool.Refresh.
func withPoolStore(t *testing.T, accounts ...*pool.Account) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pool.json")
	prev := pool.StorePath
	pool.StorePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { pool.StorePath = prev })

	if _, err := pool.LockedUpdate(func(st *pool.Store) error {
		st.Accounts = accounts
		return nil
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
}

func blob(access string, expiresIn time.Duration) string {
	return fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"refreshToken":"sk-ant-ort-x","expiresAt":%d}}`,
		access, time.Now().Add(expiresIn).UnixMilli())
}

func TestEnsureFresh_RevokedAccountDoesNotRetry(t *testing.T) {
	withPoolStore(t)
	a := &pool.Account{ID: "dead", Blob: blob("stale", -time.Hour), AuthError: "token refresh: HTTP 400 invalid_grant"}

	_, err := ensureFresh(a)
	if err == nil {
		t.Fatal("expected an error for a revoked account")
	}
	if !strings.Contains(err.Error(), "needs re-login") {
		t.Errorf("err = %q, want it to name the remedy", err)
	}
}

// The second process to want a refresh must adopt what the first one stored,
// not replay the token it was holding.
func TestEnsureFresh_AdoptsRotationPersistedByAnotherProcess(t *testing.T) {
	withPoolStore(t, &pool.Account{ID: "a", Blob: blob("rotated", time.Hour)})
	stale := &pool.Account{ID: "a", Blob: blob("stale", -time.Hour)}

	od, err := ensureFresh(stale)
	if err != nil {
		t.Fatalf("ensureFresh: %v", err)
	}
	if od.AccessToken != "rotated" {
		t.Errorf("AccessToken = %q, want the stored rotation", od.AccessToken)
	}
	if stale.Blob != blobOf(t, "a") {
		t.Error("the caller's account was left holding the retired blob")
	}
}

func TestEnsureFresh_HonorsRevocationRecordedByAnotherProcess(t *testing.T) {
	withPoolStore(t, &pool.Account{ID: "a", Blob: blob("stale", -time.Hour), AuthError: "token refresh: HTTP 400 invalid_grant"})
	a := &pool.Account{ID: "a", Blob: blob("stale", -time.Hour)}

	if _, err := ensureFresh(a); err == nil || !strings.Contains(err.Error(), "needs re-login") {
		t.Fatalf("err = %v, want a re-login error", err)
	}
}

func TestMarkAuthError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"revoked grant", &pool.RefreshError{Status: 400, Code: "invalid_grant"}, true},
		{"wrapped revoked grant", fmt.Errorf("refresh: %w", &pool.RefreshError{Status: 400}), true},
		{"server fault", &pool.RefreshError{Status: 503}, false},
		{"network failure", fmt.Errorf("dial tcp: timeout"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withPoolStore(t, &pool.Account{ID: "a", Blob: blob("stale", -time.Hour)})
			a := &pool.Account{ID: "a"}

			markAuthError(a, tc.err)

			if got := a.AuthError != ""; got != tc.want {
				t.Errorf("AuthError = %q, want set = %v", a.AuthError, tc.want)
			}
			s, err := pool.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := s.Find("a").AuthError != ""; got != tc.want {
				t.Errorf("persisted AuthError set = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllAuthFailed(t *testing.T) {
	dead := "token refresh: HTTP 400 invalid_grant"
	for _, tc := range []struct {
		name     string
		accounts []*pool.Account
		want     bool
	}{
		{"one live account", []*pool.Account{{ID: "a"}, {ID: "b", AuthError: dead}}, false},
		{"all revoked", []*pool.Account{{ID: "a", AuthError: dead}, {ID: "b", AuthError: dead}}, true},
		// Vacuous truth would strand an empty pool in the "needs re-login" arm.
		{"empty pool", nil, false},
	} {
		if got := allAuthFailed(&pool.Store{Accounts: tc.accounts}); got != tc.want {
			t.Errorf("%s: allAuthFailed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func blobOf(t *testing.T, id string) string {
	t.Helper()
	s, err := pool.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s.Find(id).Blob
}
