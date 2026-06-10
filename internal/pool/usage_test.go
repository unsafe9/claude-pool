package pool

import (
	"testing"
	"time"
)

func TestFormatStatusline_MatchesCompactForm(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	u := Usage{
		FiveHour: Window{Pct: 4.9, ResetsAt: now.Add(4*time.Hour + 40*time.Minute)},
		SevenDay: Window{Pct: 2.1, ResetsAt: now.Add(6*24*time.Hour + 8*time.Hour)},
	}
	got := u.FormatStatusline(now)
	want := "4%/4h40m 2%/6d8h"
	if got != want {
		t.Errorf("FormatStatusline = %q, want %q", got, want)
	}
}

func TestShortDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		days bool
		want string
	}{
		{4*time.Hour + 40*time.Minute, false, "4h40m"},
		{6*24*time.Hour + 8*time.Hour, true, "6d8h"},
		{40 * time.Minute, false, "40m"},
		{-time.Hour, false, "0m"},
		{20 * time.Hour, true, "20h0m"}, // <1 day in days mode falls back to h/m
		{2*time.Hour + 5*time.Minute, false, "2h5m"},
	}
	for _, c := range cases {
		if got := shortDur(c.d, c.days); got != c.want {
			t.Errorf("shortDur(%v, days=%v) = %q, want %q", c.d, c.days, got, c.want)
		}
	}
}

func TestScore_PicksBindingWindow(t *testing.T) {
	u := Usage{
		FiveHour: Window{Pct: 30},
		SevenDay: Window{Pct: 70},
	}
	if got := u.Score(); got != 70 {
		t.Errorf("Score = %v, want 70 (the more-constrained window)", got)
	}
}
