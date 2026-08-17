package pool

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthBeta     = "oauth-2025-04-20"
)

// A var so tests can retarget it; the real endpoint must never be exercised by
// the test suite, where a refresh would consume a single-use token.
var oauthTokenURL = "https://console.anthropic.com/v1/oauth/token"

// 8s keeps one hung connection from eating a blocking hook's whole budget (the
// SessionStart hook runs with a 15s timeout).
var httpClient = &http.Client{Timeout: 8 * time.Second, Transport: poolTransport()}

// pollAccounts fans out concurrently onto one host, where the default
// MaxIdleConnsPerHost of 2 would force extra TLS handshakes.
func poolTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 32
	t.MaxConnsPerHost = 32
	return t
}

// RefreshError is a non-200 from the token endpoint.
type RefreshError struct {
	Status      int
	Code        string // OAuth2 error code, e.g. "invalid_grant"
	Description string // OAuth2 error_description, or a redacted body excerpt
}

func (e *RefreshError) Error() string {
	msg := fmt.Sprintf("token refresh: HTTP %d", e.Status)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Description != "" {
		msg += ": " + e.Description
	}
	return msg
}

// Revoked reports whether only a fresh login can repair the grant, so callers
// stop replaying a token whose every reuse the server reads as an attack.
func (e *RefreshError) Revoked() bool {
	switch e.Code {
	case "invalid_grant", "invalid_token", "expired_token":
		return true
	case "":
		// A bare 400 counts: the request body is fixed apart from the refresh
		// token, so nothing else in it can make the request bad.
		return e.Status == http.StatusBadRequest
	}
	// invalid_client and the rest are the pool's fault or the server's, not
	// this account's.
	return false
}

// Refresh errors reach stderr, logs and screenshots, so any credential the
// server echoes back has to be stripped before the body is quoted.
var secretPattern = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

func redactSecrets(s string) string {
	return secretPattern.ReplaceAllString(s, "[redacted]")
}

// parseRefreshError keeps an excerpt even when the body parses as nothing:
// an opaque "HTTP 400" is undiagnosable.
func parseRefreshError(resp *http.Response) *RefreshError {
	e := &RefreshError{Status: resp.StatusCode}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil || len(body) == 0 {
		return e
	}

	// The endpoint answers in either the RFC 6749 shape ({"error":"…"}) or
	// Anthropic's ({"error":{"type":"…","message":"…"}}). Decoding both and
	// taking whichever filled in beats branching: the mismatched shape simply
	// leaves its fields zero.
	var flat struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	var nested struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &flat)
	_ = json.Unmarshal(body, &nested)

	e.Code = cmp.Or(flat.Error, nested.Error.Type)
	e.Description = cmp.Or(flat.Description, nested.Error.Message, string(body))

	e.Description = strings.TrimSpace(e.Description)
	if len(e.Description) > 200 {
		e.Description = e.Description[:200] + "…"
	}
	e.Code, e.Description = redactSecrets(e.Code), redactSecrets(e.Description)
	return e
}

// refresh exchanges the blob's refresh token for a fresh access token and
// returns an updated blob, preserving unknown fields in the credential JSON.
//
// Unexported on purpose: refresh tokens are single-use, so a bare refresh is
// safe only inside RefreshCredential, which holds the credential's lock and
// persists what comes back.
func refresh(blob string) (string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blob), &root); err != nil {
		return "", err
	}
	var oauth map[string]any
	if err := json.Unmarshal(root["claudeAiOauth"], &oauth); err != nil {
		return "", fmt.Errorf("missing claudeAiOauth: %w", err)
	}
	rt, _ := oauth["refreshToken"].(string)
	if rt == "" {
		return "", fmt.Errorf("no refresh token in credentials")
	}

	reqBody, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": rt,
		"client_id":     oauthClientID,
	})
	req, err := http.NewRequest(http.MethodPost, oauthTokenURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", oauthBeta)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", parseRefreshError(resp)
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}

	oauth["accessToken"] = tr.AccessToken
	oauth["expiresAt"] = time.Now().UnixMilli() + tr.ExpiresIn*1000
	if tr.RefreshToken != "" {
		oauth["refreshToken"] = tr.RefreshToken
	}
	if tr.Scope != "" {
		oauth["scopes"] = strings.Fields(tr.Scope)
	}

	newOAuth, err := json.Marshal(oauth)
	if err != nil {
		return "", err
	}
	root["claudeAiOauth"] = newOAuth
	newBlob, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(newBlob), nil
}
