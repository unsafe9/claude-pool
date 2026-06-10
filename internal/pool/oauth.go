package pool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthTokenURL = "https://console.anthropic.com/v1/oauth/token"
	oauthBeta     = "oauth-2025-04-20"
)

// 8s keeps a single hung connection from eating a blocking hook's whole budget
// (the SessionStart hook runs with a 15s timeout); polls now run concurrently,
// so worst case is one refresh + one usage call ≈ 16s only on a fully dead
// network.
var httpClient = &http.Client{Timeout: 8 * time.Second}

// Refresh exchanges the blob's refresh token for a fresh access token and
// returns an updated blob. The returned bool is false when no refresh happened
// (e.g. no refresh token). Unknown fields in the credential JSON are preserved.
func Refresh(blob string) (string, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blob), &root); err != nil {
		return blob, false, err
	}
	var oauth map[string]any
	if err := json.Unmarshal(root["claudeAiOauth"], &oauth); err != nil {
		return blob, false, fmt.Errorf("missing claudeAiOauth: %w", err)
	}
	rt, _ := oauth["refreshToken"].(string)
	if rt == "" {
		return blob, false, fmt.Errorf("no refresh token in credentials")
	}

	reqBody, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": rt,
		"client_id":     oauthClientID,
	})
	req, err := http.NewRequest(http.MethodPost, oauthTokenURL, bytes.NewReader(reqBody))
	if err != nil {
		return blob, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", oauthBeta)

	resp, err := httpClient.Do(req)
	if err != nil {
		return blob, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return blob, false, fmt.Errorf("token refresh: HTTP %d", resp.StatusCode)
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return blob, false, err
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
		return blob, false, err
	}
	root["claudeAiOauth"] = newOAuth
	newBlob, err := json.Marshal(root)
	if err != nil {
		return blob, false, err
	}
	return string(newBlob), true, nil
}
