package proxy

import (
	"sync"
	"time"
)

// Mode represents the current operating mode of the proxy.
type Mode string

const (
	// ModeActive means the proxy passes OAuth tokens through normally.
	ModeActive Mode = "active"
	// ModeThrottled means the proxy has hit a rate limit and is falling back to an API key.
	ModeThrottled Mode = "throttled"
)

// Snapshot is a point-in-time copy of the state machine's fields.
type Snapshot struct {
	Mode            Mode      `json:"mode"`
	ThrottledUntil  time.Time `json:"throttled_until,omitempty"`
	LastTriggerAt   time.Time `json:"last_trigger_at,omitempty"`
	LastTriggerCode int       `json:"last_trigger_code,omitempty"`
	LastTriggerSrc  string    `json:"last_trigger_src,omitempty"`
	RequestCount    uint64    `json:"request_count"`
	FallbackCount   uint64    `json:"fallback_count"`
}

// State is a thread-safe state machine that tracks whether the proxy is
// currently active (OAuth pass-through) or throttled (API-key fallback).
type State struct {
	mu   sync.Mutex
	snap Snapshot
}

// NewState returns a new State in ModeActive.
func NewState() *State {
	return &State{
		snap: Snapshot{
			Mode: ModeActive,
		},
	}
}

// lazyRecover checks if the throttle deadline has passed and, if so,
// flips the mode back to ModeActive. Must be called with mu held.
func (s *State) lazyRecover() {
	if s.snap.Mode == ModeThrottled && !s.snap.ThrottledUntil.IsZero() && time.Now().After(s.snap.ThrottledUntil) {
		s.snap.Mode = ModeActive
	}
}

// CurrentMode returns the current mode, performing lazy recovery if the
// throttle deadline has passed.
func (s *State) CurrentMode() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lazyRecover()
	return s.snap.Mode
}

// EnterThrottled switches the proxy into ModeThrottled until the given deadline.
// It records the trigger source and HTTP status code that caused throttling.
func (s *State) EnterThrottled(until time.Time, src string, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Mode = ModeThrottled
	s.snap.ThrottledUntil = until
	s.snap.LastTriggerAt = time.Now()
	s.snap.LastTriggerSrc = src
	s.snap.LastTriggerCode = code
}

// IncRequest increments the total request counter.
func (s *State) IncRequest() {
	s.mu.Lock()
	s.snap.RequestCount++
	s.mu.Unlock()
}

// IncFallback increments the fallback-to-API-key counter.
func (s *State) IncFallback() {
	s.mu.Lock()
	s.snap.FallbackCount++
	s.mu.Unlock()
}

// Snapshot returns a value copy of the current state, performing lazy
// recovery if the throttle deadline has passed.
func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lazyRecover()
	return s.snap
}
