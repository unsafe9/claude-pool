package main

import (
	"testing"
	"time"

	"github.com/unsafe9/claude-pool/internal/pool"
)

func TestUsableAt(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	in1h, in2h := now.Add(time.Hour), now.Add(2*time.Hour)

	// Not exhausted → zero.
	if got := usableAt(pool.Usage{
		FiveHour: pool.Window{Pct: 80, ResetsAt: in1h},
		SevenDay: pool.Window{Pct: 50, ResetsAt: in2h},
	}); !got.IsZero() {
		t.Errorf("non-exhausted usableAt = %v, want zero", got)
	}
	// Only 5h exhausted → its reset.
	if got := usableAt(pool.Usage{
		FiveHour: pool.Window{Pct: 100, ResetsAt: in1h},
		SevenDay: pool.Window{Pct: 50, ResetsAt: in2h},
	}); !got.Equal(in1h) {
		t.Errorf("5h-bound usableAt = %v, want %v", got, in1h)
	}
	// Both exhausted → the later reset.
	if got := usableAt(pool.Usage{
		FiveHour: pool.Window{Pct: 100, ResetsAt: in1h},
		SevenDay: pool.Window{Pct: 100, ResetsAt: in2h},
	}); !got.Equal(in2h) {
		t.Errorf("both-bound usableAt = %v, want %v", got, in2h)
	}
	// Exhausted window with unknown reset → zero (can't schedule).
	if got := usableAt(pool.Usage{
		FiveHour: pool.Window{Pct: 100},
		SevenDay: pool.Window{Pct: 50, ResetsAt: in2h},
	}); !got.IsZero() {
		t.Errorf("unknown-reset usableAt = %v, want zero", got)
	}
}

func TestSoonestRecovery(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	in1h, in3h := now.Add(time.Hour), now.Add(3*time.Hour)

	// Two exhausted accounts → the earlier reset wins; an account with no
	// cached usage is skipped, not treated as usable now.
	s := &pool.Store{Accounts: []*pool.Account{
		{ID: "a", Usage: &pool.Usage{FiveHour: pool.Window{Pct: 100, ResetsAt: in3h}}},
		{ID: "b"},
		{ID: "c", Usage: &pool.Usage{FiveHour: pool.Window{Pct: 100, ResetsAt: in1h}}},
	}}
	if got := soonestRecovery(s); !got.Equal(in1h) {
		t.Errorf("soonestRecovery = %v, want %v", got, in1h)
	}

	// No account has a known usable-at time → zero (reset fields omitted).
	s = &pool.Store{Accounts: []*pool.Account{
		{ID: "a", Usage: &pool.Usage{FiveHour: pool.Window{Pct: 80, ResetsAt: in1h}}},
		{ID: "b"},
	}}
	if got := soonestRecovery(s); !got.IsZero() {
		t.Errorf("soonestRecovery with no exhausted account = %v, want zero", got)
	}
}

func TestIsOurHelper(t *testing.T) {
	if !isOurHelper("'/Users/x/go/bin/claude-pool' helper") {
		t.Error("quoted claude-pool helper should match")
	}
	if !isOurHelper("/Users/x/go/bin/claude-pool helper") {
		t.Error("unquoted claude-pool helper should match")
	}
	if isOurHelper("/corp/auth.sh") {
		t.Error("foreign helper must not match")
	}
	if isOurHelper("") {
		t.Error("empty value must not match")
	}
	// Windows cmd.exe format: double-quoted path.
	if !isOurHelper(`"C:\Users\u\.local\bin\claude-pool.exe" helper`) {
		t.Error("windows double-quoted claude-pool.exe helper should match")
	}
}
