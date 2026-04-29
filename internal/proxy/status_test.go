package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStatusHandler_ReturnsActive(t *testing.T) {
	state := NewState()
	handler := NewStatusHandler(state)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var snap Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	if snap.Mode != ModeActive {
		t.Errorf("expected mode %q, got %q", ModeActive, snap.Mode)
	}
	if snap.RequestCount != 0 {
		t.Errorf("expected request_count 0, got %d", snap.RequestCount)
	}
}

func TestStatusHandler_ReturnsThrottledSnapshot(t *testing.T) {
	state := NewState()
	expectedUntil := time.Now().Add(time.Hour)
	state.EnterThrottled(expectedUntil, "test", 429)

	handler := NewStatusHandler(state)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var snap Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	if snap.Mode != ModeThrottled {
		t.Errorf("expected mode %q, got %q", ModeThrottled, snap.Mode)
	}
	if snap.ThrottledUntil.IsZero() {
		t.Error("expected non-zero throttled_until")
	}
	diff := snap.ThrottledUntil.Sub(expectedUntil)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("throttled_until %v not within 5s of expected %v", snap.ThrottledUntil, expectedUntil)
	}
	if snap.LastTriggerCode != 429 {
		t.Errorf("expected last_trigger_code 429, got %d", snap.LastTriggerCode)
	}
	if snap.LastTriggerSrc != "test" {
		t.Errorf("expected last_trigger_src %q, got %q", "test", snap.LastTriggerSrc)
	}
}

func TestStatusHandler_ContentType(t *testing.T) {
	state := NewState()
	handler := NewStatusHandler(state)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type to contain application/json, got %q", ct)
	}
}
