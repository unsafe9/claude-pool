package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- helpers ---

// cloneHeaders clones the relevant headers from r for later inspection.
func cloneHeaders(r *http.Request) http.Header {
	return r.Header.Clone()
}

// recordingMux builds a simple mux that captures received headers on each call
// and responds with the provided handler. Access captured headers via the
// returned slice (protected by the returned mutex).
func newRecordingHandler(handler http.HandlerFunc) (http.Handler, *[]http.Header, *sync.Mutex) {
	var mu sync.Mutex
	var captured []http.Header
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = append(captured, cloneHeaders(r))
		mu.Unlock()
		handler(w, r)
	})
	return h, &captured, &mu
}

// buildProxy creates a proxy test server pointing at upstream.
func buildProxy(t *testing.T, upstreamURL string, state *State, logger *Logger) *httptest.Server {
	t.Helper()
	handler := NewHandler(upstreamURL, "sk-fallback", state, logger)
	return httptest.NewServer(handler)
}

// doRequest sends a simple HTTP request to the proxy and returns the response.
func doRequest(t *testing.T, method, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Do: %v", err)
	}
	return resp
}

// --- Test 1 ---

// TestIntegration_PassThroughActive_OnMessages verifies that in active mode a POST
// to /v1/messages passes the Authorization header through unchanged.
func TestIntegration_PassThroughActive_OnMessages(t *testing.T) {
	var capturedAuth string
	var mu sync.Mutex

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	state := NewState()
	proxy := buildProxy(t, upstream.URL, state, NewLogger(io.Discard))
	defer proxy.Close()

	resp := doRequest(t, http.MethodPost, proxy.URL+"/v1/messages", map[string]string{
		"Authorization": "Bearer real-token",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	mu.Lock()
	got := capturedAuth
	mu.Unlock()

	if got != "Bearer real-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer real-token")
	}
}

// --- Test 2 ---

// TestIntegration_EntersThrottleOn429_UnifiedReset verifies that a 429 response
// with the unified-5h reset header causes the state to enter throttled mode.
func TestIntegration_EntersThrottleOn429_UnifiedReset(t *testing.T) {
	resetTime := time.Now().Add(1 * time.Hour).Unix()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Anthropic-Ratelimit-Unified-5h-Reset", fmt.Sprintf("%d", resetTime))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	state := NewState()
	proxy := buildProxy(t, upstream.URL, state, NewLogger(io.Discard))
	defer proxy.Close()

	resp := doRequest(t, http.MethodPost, proxy.URL+"/v1/messages", map[string]string{
		"Authorization": "Bearer real-token",
	})
	defer resp.Body.Close()

	if state.CurrentMode() != ModeThrottled {
		t.Errorf("mode = %q, want %q", state.CurrentMode(), ModeThrottled)
	}
}

// --- Test 3 ---

// TestIntegration_NextRequestSwapsToApiKey_OnMessages verifies that after entering
// throttled mode the next request to /v1/messages uses X-Api-Key instead of Authorization.
func TestIntegration_NextRequestSwapsToApiKey_OnMessages(t *testing.T) {
	callCount := 0
	var mu sync.Mutex
	var secondAuth, secondApiKey string

	resetTime := time.Now().Add(1 * time.Hour).Unix()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()

		if n == 1 {
			// First call: trigger throttle.
			w.Header().Set("Anthropic-Ratelimit-Unified-5h-Reset", fmt.Sprintf("%d", resetTime))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		// Second call: record what headers were received.
		mu.Lock()
		secondAuth = r.Header.Get("Authorization")
		secondApiKey = r.Header.Get("X-Api-Key")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	state := NewState()
	proxy := buildProxy(t, upstream.URL, state, NewLogger(io.Discard))
	defer proxy.Close()

	// First request — triggers throttle.
	resp1 := doRequest(t, http.MethodPost, proxy.URL+"/v1/messages", map[string]string{
		"Authorization": "Bearer real-token",
	})
	resp1.Body.Close()

	// Second request — should swap headers.
	resp2 := doRequest(t, http.MethodPost, proxy.URL+"/v1/messages", map[string]string{
		"Authorization": "Bearer real-token",
	})
	resp2.Body.Close()

	mu.Lock()
	gotAuth := secondAuth
	gotKey := secondApiKey
	mu.Unlock()

	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty (should be removed on swap)", gotAuth)
	}
	if gotKey != "sk-fallback" {
		t.Errorf("X-Api-Key = %q, want %q", gotKey, "sk-fallback")
	}
}

// --- Test 4 ---

// TestIntegration_DoesNotSwapOAuthPath_EvenWhenThrottled verifies that /v1/oauth/*
// paths are never swapped even in throttled mode.
func TestIntegration_DoesNotSwapOAuthPath_EvenWhenThrottled(t *testing.T) {
	h, captured, mu := newRecordingHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	upstream := httptest.NewServer(h)
	defer upstream.Close()

	state := NewState()
	state.EnterThrottled(time.Now().Add(1*time.Hour), "test", 429)

	proxy := buildProxy(t, upstream.URL, state, NewLogger(io.Discard))
	defer proxy.Close()

	resp := doRequest(t, http.MethodPost, proxy.URL+"/v1/oauth/token", map[string]string{
		"Authorization": "Bearer real-token",
	})
	defer resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	if len(*captured) == 0 {
		t.Fatal("upstream received no requests")
	}
	h0 := (*captured)[0]
	if got := h0.Get("Authorization"); got != "Bearer real-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer real-token")
	}
	if got := h0.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want empty (must not swap oauth path)", got)
	}
}

// --- Test 5 ---

// TestIntegration_DoesNotSwapApiOAuthUsage_EvenWhenThrottled verifies that
// /api/oauth/* paths (not under /v1/) are also never swapped.
func TestIntegration_DoesNotSwapApiOAuthUsage_EvenWhenThrottled(t *testing.T) {
	h, captured, mu := newRecordingHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	upstream := httptest.NewServer(h)
	defer upstream.Close()

	state := NewState()
	state.EnterThrottled(time.Now().Add(1*time.Hour), "test", 429)

	proxy := buildProxy(t, upstream.URL, state, NewLogger(io.Discard))
	defer proxy.Close()

	resp := doRequest(t, http.MethodGet, proxy.URL+"/api/oauth/usage", map[string]string{
		"Authorization": "Bearer real-token",
	})
	defer resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	if len(*captured) == 0 {
		t.Fatal("upstream received no requests")
	}
	h0 := (*captured)[0]
	if got := h0.Get("Authorization"); got != "Bearer real-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer real-token")
	}
	if got := h0.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want empty (must not swap api/oauth path)", got)
	}
}

// --- Test 6 ---

// TestIntegration_StreamingSSEPassThrough verifies that SSE chunks are forwarded
// through the proxy and that the statusRecorder's Flush() method works.
func TestIntegration_StreamingSSEPassThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		fmt.Fprint(w, "event: a\ndata: 1\n\n")
		flusher.Flush()

		fmt.Fprint(w, "event: b\ndata: 2\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	state := NewState()
	proxy := buildProxy(t, upstream.URL, state, NewLogger(io.Discard))
	defer proxy.Close()

	resp := doRequest(t, http.MethodPost, proxy.URL+"/v1/messages", map[string]string{
		"Authorization": "Bearer real-token",
	})
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	bodyStr := string(body)

	if !strings.Contains(bodyStr, "event: a") {
		t.Errorf("body does not contain 'event: a'; got: %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "event: b") {
		t.Errorf("body does not contain 'event: b'; got: %q", bodyStr)
	}

	// Verify ordering: event: a must appear before event: b.
	idxA := strings.Index(bodyStr, "event: a")
	idxB := strings.Index(bodyStr, "event: b")
	if idxA >= idxB {
		t.Errorf("event: a (%d) should appear before event: b (%d)", idxA, idxB)
	}
}

// --- Test 7 ---

// TestIntegration_StatusEndpointBypassesProxy verifies that GET /status is served
// by the status handler and does NOT reach the upstream mock.
func TestIntegration_StatusEndpointBypassesProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream received a request but should not have for /status")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	state := NewState()
	proxy := buildProxy(t, upstream.URL, state, NewLogger(io.Discard))
	defer proxy.Close()

	resp := doRequest(t, http.MethodGet, proxy.URL+"/status", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var snap Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snap.Mode != ModeActive {
		t.Errorf("mode = %q, want %q", snap.Mode, ModeActive)
	}
}

// --- Test: log mode reflects Director's read ---

// TestIntegration_LogModeMatchesDirectorDecision verifies the per-request log
// entry's Mode field is populated from the value the Director observed at
// routing-decision time, not from a fresh state read at log time. This pins
// down the contract that the wrapping handler installs a captureMode slot and
// the Director writes through it.
func TestIntegration_LogModeMatchesDirectorDecision(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	state := NewState()
	state.EnterThrottled(time.Now().Add(1*time.Hour), "test", 429)

	var logBuf strings.Builder
	proxy := buildProxy(t, upstream.URL, state, NewLogger(&logBuf))
	defer proxy.Close()

	resp := doRequest(t, http.MethodPost, proxy.URL+"/v1/messages", map[string]string{
		"Authorization": "Bearer real-token",
	})
	resp.Body.Close()

	var entry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(logBuf.String())), &entry); err != nil {
		t.Fatalf("decode log entry: %v\nraw: %q", err, logBuf.String())
	}
	if entry.Mode != ModeThrottled {
		t.Errorf("log Mode = %q, want %q", entry.Mode, ModeThrottled)
	}
}

// --- Test 8 ---

// TestIntegration_LazyRecoveryAfterReset verifies that after the throttle deadline
// expires, the next request recovers lazily to active mode and passes OAuth through.
func TestIntegration_LazyRecoveryAfterReset(t *testing.T) {
	var capturedAuth string
	var mu sync.Mutex

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	state := NewState()
	// Set throttle to expire in 50ms.
	state.EnterThrottled(time.Now().Add(50*time.Millisecond), "test", 429)

	proxy := buildProxy(t, upstream.URL, state, NewLogger(io.Discard))
	defer proxy.Close()

	// Wait for throttle to expire.
	time.Sleep(100 * time.Millisecond)

	// This request should trigger lazy recovery and NOT swap headers.
	resp := doRequest(t, http.MethodPost, proxy.URL+"/v1/messages", map[string]string{
		"Authorization": "Bearer real-token",
	})
	defer resp.Body.Close()

	mu.Lock()
	got := capturedAuth
	mu.Unlock()

	if got != "Bearer real-token" {
		t.Errorf("Authorization = %q, want %q (should have recovered to active, not swapped)", got, "Bearer real-token")
	}
}
