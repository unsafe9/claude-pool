package proxy

import (
	"net/http/httptest"
	"testing"
	"time"
)

// ---- ShouldSwap unit tests ----

func TestShouldSwap_TrueForMessages(t *testing.T) {
	paths := []string{
		"/v1/messages",
		"/v1/messages/count_tokens",
		"/v1/messages/anything",
	}
	for _, p := range paths {
		if !ShouldSwap(p) {
			t.Errorf("ShouldSwap(%q) = false, want true", p)
		}
	}
}

func TestShouldSwap_FalseForOAuthPaths(t *testing.T) {
	paths := []string{
		"/v1/oauth/token",
		"/v1/oauth/refresh",
		"/v1/oauth/anything",
	}
	for _, p := range paths {
		if ShouldSwap(p) {
			t.Errorf("ShouldSwap(%q) = true, want false", p)
		}
	}
}

func TestShouldSwap_FalseForApiPaths(t *testing.T) {
	paths := []string{
		"/api/oauth/profile",
		"/api/oauth/usage",
		"/api/anything",
	}
	for _, p := range paths {
		if ShouldSwap(p) {
			t.Errorf("ShouldSwap(%q) = true, want false", p)
		}
	}
}

func TestShouldSwap_FalseForRoot(t *testing.T) {
	paths := []string{
		"/",
		"/status",
		"/healthz",
	}
	for _, p := range paths {
		if ShouldSwap(p) {
			t.Errorf("ShouldSwap(%q) = true, want false", p)
		}
	}
}

// ---- Director unit tests ----

const (
	testUpstream   = "https://api.anthropic.com"
	testFallbackKey = "sk-fallback-key"
	testAuthToken  = "Bearer oauth-token"
)

func TestDirector_PassthroughInActive(t *testing.T) {
	state := NewState() // ModeActive by default
	director := NewDirector(testUpstream, testFallbackKey, state)

	req := httptest.NewRequest("POST", "http://localhost/v1/messages", nil)
	req.Header.Set("Authorization", testAuthToken)

	director(req)

	if got := req.Header.Get("Authorization"); got != testAuthToken {
		t.Errorf("Authorization = %q, want %q", got, testAuthToken)
	}
	if got := req.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want empty", got)
	}
	if req.URL.Host != "api.anthropic.com" {
		t.Errorf("URL.Host = %q, want %q", req.URL.Host, "api.anthropic.com")
	}
	if req.URL.Scheme != "https" {
		t.Errorf("URL.Scheme = %q, want %q", req.URL.Scheme, "https")
	}
}

func TestDirector_SwapToApiKeyInThrottled_OnMessagesPath(t *testing.T) {
	state := NewState()
	state.EnterThrottled(time.Now().Add(time.Hour), "test", 429)
	director := NewDirector(testUpstream, testFallbackKey, state)

	req := httptest.NewRequest("POST", "http://localhost/v1/messages", nil)
	req.Header.Set("Authorization", testAuthToken)

	director(req)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty (should be deleted)", got)
	}
	if got := req.Header.Get("X-Api-Key"); got != testFallbackKey {
		t.Errorf("X-Api-Key = %q, want %q", got, testFallbackKey)
	}
}

func TestDirector_NoSwapOnOAuthPath_EvenInThrottled(t *testing.T) {
	state := NewState()
	state.EnterThrottled(time.Now().Add(time.Hour), "test", 429)
	director := NewDirector(testUpstream, testFallbackKey, state)

	req := httptest.NewRequest("POST", "http://localhost/v1/oauth/token", nil)
	req.Header.Set("Authorization", testAuthToken)

	director(req)

	if got := req.Header.Get("Authorization"); got != testAuthToken {
		t.Errorf("Authorization = %q, want %q", got, testAuthToken)
	}
	if got := req.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want empty", got)
	}
}

func TestDirector_NoSwapOnApiOAuthPath_EvenInThrottled(t *testing.T) {
	state := NewState()
	state.EnterThrottled(time.Now().Add(time.Hour), "test", 429)
	director := NewDirector(testUpstream, testFallbackKey, state)

	req := httptest.NewRequest("GET", "http://localhost/api/oauth/usage", nil)
	req.Header.Set("Authorization", testAuthToken)

	director(req)

	if got := req.Header.Get("Authorization"); got != testAuthToken {
		t.Errorf("Authorization = %q, want %q", got, testAuthToken)
	}
	if got := req.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want empty", got)
	}
}

func TestDirector_NoApiKeyConfigured_LeavesAuthAlone(t *testing.T) {
	state := NewState()
	state.EnterThrottled(time.Now().Add(time.Hour), "test", 429)
	director := NewDirector(testUpstream, "", state) // empty fallback key

	req := httptest.NewRequest("POST", "http://localhost/v1/messages", nil)
	req.Header.Set("Authorization", testAuthToken)

	director(req)

	if got := req.Header.Get("Authorization"); got != testAuthToken {
		t.Errorf("Authorization = %q, want %q", got, testAuthToken)
	}
	if got := req.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want empty", got)
	}
}

func TestDirector_HostRewrite(t *testing.T) {
	state := NewState()
	director := NewDirector(testUpstream, testFallbackKey, state)

	req := httptest.NewRequest("GET", "http://localhost/v1/models", nil)

	director(req)

	if req.URL.Scheme != "https" {
		t.Errorf("URL.Scheme = %q, want %q", req.URL.Scheme, "https")
	}
	if req.URL.Host != "api.anthropic.com" {
		t.Errorf("URL.Host = %q, want %q", req.URL.Host, "api.anthropic.com")
	}
	if req.Host != "api.anthropic.com" {
		t.Errorf("req.Host = %q, want %q", req.Host, "api.anthropic.com")
	}
}
