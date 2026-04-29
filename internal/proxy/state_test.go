package proxy

import (
	"sync"
	"testing"
	"time"
)

func TestStateMachine_StartsActive(t *testing.T) {
	s := NewState()
	snap := s.Snapshot()
	if snap.Mode != ModeActive {
		t.Errorf("expected ModeActive, got %q", snap.Mode)
	}
	if snap.RequestCount != 0 {
		t.Errorf("expected RequestCount 0, got %d", snap.RequestCount)
	}
	if snap.FallbackCount != 0 {
		t.Errorf("expected FallbackCount 0, got %d", snap.FallbackCount)
	}
}

func TestStateMachine_EnterThrottled_SetsModeAndUntil(t *testing.T) {
	s := NewState()
	until := time.Now().Add(time.Hour)
	src := "test-src"
	code := 429

	s.EnterThrottled(until, src, code)
	snap := s.Snapshot()

	if snap.Mode != ModeThrottled {
		t.Errorf("expected ModeThrottled, got %q", snap.Mode)
	}
	if !snap.ThrottledUntil.Equal(until) {
		t.Errorf("expected ThrottledUntil %v, got %v", until, snap.ThrottledUntil)
	}
	if snap.LastTriggerSrc != src {
		t.Errorf("expected LastTriggerSrc %q, got %q", src, snap.LastTriggerSrc)
	}
	if snap.LastTriggerCode != code {
		t.Errorf("expected LastTriggerCode %d, got %d", code, snap.LastTriggerCode)
	}
	if snap.LastTriggerAt.IsZero() {
		t.Error("expected LastTriggerAt to be set, got zero")
	}
}

func TestStateMachine_AutoRecoverWhenUntilPassed(t *testing.T) {
	s := NewState()
	// Set throttled deadline in the past
	until := time.Now().Add(-time.Second)
	s.EnterThrottled(until, "src", 429)

	mode := s.CurrentMode()
	if mode != ModeActive {
		t.Errorf("expected ModeActive after deadline passed, got %q", mode)
	}

	// Snapshot should also reflect recovery
	snap := s.Snapshot()
	if snap.Mode != ModeActive {
		t.Errorf("expected Snapshot.Mode ModeActive after lazy recovery, got %q", snap.Mode)
	}
}

func TestStateMachine_StaysThrottledUntilDeadline(t *testing.T) {
	s := NewState()
	until := time.Now().Add(time.Hour)
	s.EnterThrottled(until, "src", 429)

	mode := s.CurrentMode()
	if mode != ModeThrottled {
		t.Errorf("expected ModeThrottled before deadline, got %q", mode)
	}
}

func TestStateMachine_CountersIncrement_AreThreadSafe(t *testing.T) {
	s := NewState()
	const goroutines = 100
	const incPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < incPerGoroutine; j++ {
				s.IncRequest()
			}
		}()
	}
	wg.Wait()

	snap := s.Snapshot()
	expected := uint64(goroutines * incPerGoroutine)
	if snap.RequestCount != expected {
		t.Errorf("expected RequestCount %d, got %d", expected, snap.RequestCount)
	}
}
