package pool

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// FetchProfile returns the email address of the account an OAuth access token
// belongs to. It is the only stable identity a credential blob can be
// attributed by (blobs carry no account ID), used to harvest cc-refreshed
// Keychain credentials back into the right pool entry.
func FetchProfile(accessToken string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/api/oauth/profile", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", oauthBeta)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("profile API: HTTP %d", resp.StatusCode)
	}

	var raw struct {
		Account struct {
			EmailAddress string `json:"email_address"`
			Email        string `json:"email"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", err
	}
	if raw.Account.EmailAddress != "" {
		return raw.Account.EmailAddress, nil
	}
	return raw.Account.Email, nil
}
