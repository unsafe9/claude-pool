package main

import (
	"errors"
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
	claudeDir := filepath.Join(t.TempDir(), ".claude")
	prevDir := pool.ClaudeDir
	pool.ClaudeDir = func() (string, error) { return claudeDir, nil }
	t.Cleanup(func() { pool.ClaudeDir = prevDir })

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
		// A healthy account the user disabled is not auth the pool may fall back
		// on, so the rotation really is out of logins.
		{"only live account is out of rotation", []*pool.Account{{ID: "a", AuthError: dead}, {ID: "b", Disabled: true}}, true},
		// Disabling everything is a choice, not a broken grant.
		{"all disabled", []*pool.Account{{ID: "a", Disabled: true}}, false},
	} {
		if got := allAuthFailed(&pool.Store{Accounts: tc.accounts}); got != tc.want {
			t.Errorf("%s: allAuthFailed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The idle account is the one keepalive exists for, so it must renew an account
// nothing selects — while leaving a live token alone (each renewal spends a
// single-use refresh token) and never retrying a grant the server killed.
func TestKeepAlive(t *testing.T) {
	// The store already holds a rotation for "idle", so ensureFresh adopts it
	// instead of reaching the token endpoint.
	withPoolStore(t,
		&pool.Account{ID: "idle", Blob: blob("rotated", time.Hour), Disabled: true},
		&pool.Account{ID: "live", Blob: blob("live", time.Hour)},
		&pool.Account{ID: "dead", Blob: blob("stale", -time.Hour), AuthError: "token refresh: HTTP 400 invalid_grant"},
	)
	s, err := pool.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// What a hook holds in memory: the parked account expired where it sat.
	s.Find("idle").Blob = blob("stale", -time.Hour)
	deadBlob := s.Find("dead").Blob

	keepAlive(s)

	if od, _ := pool.ParseBlob(s.Find("idle").Blob); od.AccessToken != "rotated" {
		t.Errorf("idle account not renewed: AccessToken = %q", od.AccessToken)
	}
	if od, _ := pool.ParseBlob(s.Find("live").Blob); od.AccessToken != "live" {
		t.Errorf("live account touched: AccessToken = %q", od.AccessToken)
	}
	if s.Find("dead").Blob != deadBlob {
		t.Error("revoked grant was retried")
	}
}

// Disabled accounts drop out of scoring the way failed polls do, but for a
// reason the caller can tell apart — and without a request.
func TestPollAccounts_SkipsDisabled(t *testing.T) {
	s := &pool.Store{Accounts: []*pool.Account{
		{ID: "off", Disabled: true, Blob: blob("x", time.Hour)},
		{ID: "broken", Blob: `{}`}, // ParseBlob fails before any request
	}}

	usages, errs := pollAccounts(s, nil)

	if !errors.Is(errs[0], errDisabled) {
		t.Errorf("errs[off] = %v, want errDisabled", errs[0])
	}
	if errs[1] == nil || errors.Is(errs[1], errDisabled) {
		t.Errorf("errs[broken] = %v, want a poll failure", errs[1])
	}
	if best, _, polled := bestAccount(s, usages, errs, 80); best != nil || polled != 0 {
		t.Errorf("bestAccount = (%v, polled=%d), want nothing selectable", best, polled)
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
