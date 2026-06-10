package pool

import (
	"encoding/json"
	"errors"
	"time"
)

// OAuthData mirrors the claudeAiOauth object Claude Code stores in the Keychain
// (macOS) or ~/.claude/.credentials.json (Linux/Windows).
type OAuthData struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"` // epoch milliseconds
	Scopes           []string `json:"scopes,omitempty"`
	SubscriptionType string   `json:"subscriptionType,omitempty"`
}

type credBlob struct {
	ClaudeAiOauth OAuthData `json:"claudeAiOauth"`
}

// ParseBlob extracts the OAuth payload from a raw credential blob. Any JSON
// object unmarshals without error, so require the one field every credential
// must carry — otherwise logged-out stubs or foreign payloads pass as valid.
func ParseBlob(blob string) (OAuthData, error) {
	var c credBlob
	if err := json.Unmarshal([]byte(blob), &c); err != nil {
		return OAuthData{}, err
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return OAuthData{}, errors.New("missing claudeAiOauth.accessToken")
	}
	return c.ClaudeAiOauth, nil
}

// expiryBufferMS treats a token as expired this long before its actual expiry,
// matching Claude Code's own refresh window.
const expiryBufferMS = 5 * 60 * 1000

// IsExpired reports whether an access token is expired or about to expire.
func IsExpired(expiresAtMS int64) bool {
	if expiresAtMS == 0 {
		return false
	}
	return time.Now().UnixMilli()+expiryBufferMS >= expiresAtMS
}
