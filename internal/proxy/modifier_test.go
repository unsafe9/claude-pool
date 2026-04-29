package proxy

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestModifyResponse_TransitionsOnRateLimitHeader(t *testing.T) {
	state := NewState()
	modifier := NewModifier(state)

	resetTime := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Anthropic-Ratelimit-Requests-Reset": []string{resetTime},
		},
		Body: http.NoBody,
	}

	if err := modifier(resp); err != nil {
		t.Fatalf("modifier returned error: %v", err)
	}

	if got := state.CurrentMode(); got != ModeThrottled {
		t.Errorf("expected ModeThrottled, got %q", got)
	}
}

func TestModifyResponse_NoTransitionOn200(t *testing.T) {
	state := NewState()
	modifier := NewModifier(state)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       http.NoBody,
	}

	if err := modifier(resp); err != nil {
		t.Fatalf("modifier returned error: %v", err)
	}

	if got := state.CurrentMode(); got != ModeActive {
		t.Errorf("expected ModeActive, got %q", got)
	}
}

func TestModifyResponse_NoTransitionOnNonLimit429(t *testing.T) {
	state := NewState()
	modifier := NewModifier(state)

	body := `{"error":{"type":"overloaded_error"}}`
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	if err := modifier(resp); err != nil {
		t.Fatalf("modifier returned error: %v", err)
	}

	if got := state.CurrentMode(); got != ModeActive {
		t.Errorf("expected ModeActive, got %q", got)
	}
}

func TestModifyResponse_PreservesBody(t *testing.T) {
	state := NewState()
	modifier := NewModifier(state)

	// 429 with rate_limit_error body (no headers) — forces Classify to read body.
	originalBody := `{"error":{"type":"rate_limit_error"}}`
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(originalBody)),
	}

	if err := modifier(resp); err != nil {
		t.Fatalf("modifier returned error: %v", err)
	}

	// Body must still be fully readable after modifier runs.
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body after modifier: %v", err)
	}
	if string(got) != originalBody {
		t.Errorf("body mismatch: got %q, want %q", string(got), originalBody)
	}
}
