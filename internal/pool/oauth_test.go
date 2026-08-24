package pool

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serveToken(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	prev := oauthTokenURL
	oauthTokenURL = srv.URL
	t.Cleanup(func() { oauthTokenURL = prev; srv.Close() })
}

func TestRefreshError_Revoked(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  RefreshError
		want bool
	}{
		{"invalid_grant", RefreshError{Status: 400, Code: "invalid_grant"}, true},
		{"bare 400", RefreshError{Status: 400}, true},
		{"invalid_client is ours, not the account's", RefreshError{Status: 400, Code: "invalid_client"}, false},
		{"server fault", RefreshError{Status: 503}, false},
		{"rate limited", RefreshError{Status: 429}, false},
	} {
		if got := tc.err.Revoked(); got != tc.want {
			t.Errorf("%s: Revoked() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRefresh_RevokedGrant(t *testing.T) {
	serveToken(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token not found"}`))
	})

	_, err := refresh(sampleBlob)
	var re *RefreshError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want *RefreshError", err)
	}
	if !re.Revoked() {
		t.Error("invalid_grant should report as revoked")
	}
	for _, want := range []string{"HTTP 400", "invalid_grant", "refresh token not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

// The endpoint may answer in Anthropic's nested shape rather than RFC 6749's.
func TestRefresh_NestedErrorShape(t *testing.T) {
	serveToken(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_grant","message":"grant expired"}}`))
	})

	_, err := refresh(sampleBlob)
	var re *RefreshError
	if !errors.As(err, &re) || re.Code != "invalid_grant" || re.Description != "grant expired" {
		t.Fatalf("err = %v (code=%q desc=%q)", err, re.Code, re.Description)
	}
}

// Refresh errors reach stderr, logs and screenshots, so a body that echoes a
// credential must not carry it along.
func TestRefresh_RedactsSecretsInBody(t *testing.T) {
	serveToken(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`token sk-ant-ort01-SUPERSECRET rejected`))
	})

	_, err := refresh(sampleBlob)
	if strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("err leaked the token: %q", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("err = %q, want a redaction marker", err)
	}
}

func TestRefresh_UnparseableBodyStillDiagnosable(t *testing.T) {
	serveToken(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	})

	_, err := refresh(sampleBlob)
	var re *RefreshError
	if !errors.As(err, &re) || re.Revoked() {
		t.Fatalf("502 should not read as revoked: %v", err)
	}
	if !strings.Contains(err.Error(), "upstream unavailable") {
		t.Errorf("err = %q, want the body excerpt", err)
	}
}

func TestRefresh_StoresRotatedToken(t *testing.T) {
	serveToken(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["refresh_token"] != "sk-ant-ort-xyz" || req["grant_type"] != "refresh_token" {
			t.Errorf("unexpected request %v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "sk-ant-oat-new",
			"refresh_token": "sk-ant-ort-rotated",
			"expires_in":    3600,
			"scope":         "user:inference user:profile",
		})
	})

	nb, err := refresh(sampleBlob)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	od, err := ParseBlob(nb)
	if err != nil {
		t.Fatalf("ParseBlob: %v", err)
	}
	if od.AccessToken != "sk-ant-oat-new" {
		t.Errorf("AccessToken = %q", od.AccessToken)
	}
	// The rotated refresh token is the whole point: dropping it strands the
	// credential on a token the server has already retired.
	if od.RefreshToken != "sk-ant-ort-rotated" {
		t.Errorf("RefreshToken = %q, want the rotated one", od.RefreshToken)
	}
	if IsExpired(od.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want ~1h out", time.UnixMilli(od.ExpiresAt))
	}
	// The endpoint returns no deadline for the replacement token, so the grant's
	// own expiry has to survive the rewrite — it is what `list` counts down and
	// the only warning before the account needs a re-login.
	if od.RefreshTokenExpiresAt != 1782000000000 {
		t.Errorf("RefreshTokenExpiresAt = %d, want the blob's own value carried through", od.RefreshTokenExpiresAt)
	}
}
